// Хроника локального ядра: буфер, ротация и чтение хвоста.
//
// Семейство держалось методами `BackendService` и занимало 160 строк из его
// 725 — при том что к жизненному циклу ядра оно не относится вовсе. Здесь
// видно целиком и то, зачем ротации замок (два окна Point пишут в один файл),
// и почему перенос идёт через временное имя.
//
// Приём тот же, что у `core-stream.js`: служба приходит первым доводом, а в
// классе остаётся строка-переходник. Два объяснителя уровня журнала живут в
// `extension.js` рядом с настройкой и передаются фабрике.
function createCoreLog({ fs, path, crypto, hostLogEnabled, hostLogStamp }) {
  function hostLog(service, level, message) {
    if (!hostLogEnabled(level)) return
    const line = `${hostLogStamp()} ${String(level).toUpperCase().padEnd(5)} ${message}`
    service.output.appendLine(line)
    service.appendLog(`${line}\n`)
  }

  function enqueueCoreLog(service, text, toOutput = true) {
    if (!text) return
    if (toOutput) service.output.append(text)
    service.logBuffer += text
    if (service.logBuffer.length > 256 * 1024) {
      service.flushCoreLog()
      return
    }
    if (service.logFlushTimer) return
    service.logFlushTimer = setTimeout(() => {
      service.logFlushTimer = undefined
      service.flushCoreLog()
    }, 100)
  }

  function flushCoreLog(service) {
    if (service.logFlushTimer) {
      clearTimeout(service.logFlushTimer)
      service.logFlushTimer = undefined
    }
    if (!service.logBuffer) return
    const chunk = service.logBuffer
    service.logBuffer = ''
    service.appendLog(chunk)
  }

  async function ensureLogDir(service) {
    fs.mkdirSync(path.dirname(service.logPath), { recursive: true })
  }

  function acquireLogRotationLock(service) {
    for (let attempt = 0; attempt < 2; attempt += 1) {
      try {
        const handle = fs.openSync(service.logRotationLockPath, 'wx', 0o600)
        fs.writeFileSync(handle, JSON.stringify({ pid: process.pid, createdAt: Date.now() }))
        return handle
      } catch (error) {
        if (error?.code !== 'EEXIST') return undefined
        try {
          const stale = Date.now() - fs.statSync(service.logRotationLockPath).mtimeMs > 10_000
          if (!stale) return undefined
          fs.unlinkSync(service.logRotationLockPath)
        } catch { /* another extension host released it */ }
      }
    }
    return undefined
  }

  function releaseLogRotationLock(service, handle) {
    if (handle === undefined) return
    try { fs.closeSync(handle) } catch { /* already closed */ }
    try { fs.unlinkSync(service.logRotationLockPath) } catch { /* another host may have cleaned a stale lock */ }
  }

  function moveBoundedLog(service, source, target) {
    if (!fs.existsSync(source)) return
    try { fs.unlinkSync(target) } catch (error) { if (error?.code !== 'ENOENT') throw error }
    const temporary = `${target}.rotate-${process.pid}-${crypto.randomUUID()}`
    fs.renameSync(source, temporary)
    try {
      const size = fs.statSync(temporary).size
      if (size <= service.maxLogBytes) {
        fs.renameSync(temporary, target)
        return
      }
      const length = Math.min(size, service.maxLogBytes)
      const buffer = Buffer.allocUnsafe(length)
      const handle = fs.openSync(temporary, 'r')
      try {
        fs.readSync(handle, buffer, 0, length, size - length)
      } finally {
        fs.closeSync(handle)
      }
      // Start the compacted archive at a line boundary whenever possible.
      const newline = buffer.indexOf(0x0a)
      fs.writeFileSync(target, newline >= 0 ? buffer.subarray(newline + 1) : buffer, { mode: 0o600 })
    } finally {
      try { fs.unlinkSync(temporary) } catch { /* renamed whole file or cleanup raced */ }
    }
  }

  function rotateLogIfNeeded(service, incomingBytes = 0) {
    try {
      if (!fs.existsSync(service.logPath)) return
      const size = fs.statSync(service.logPath).size
      if (size + Math.max(0, Number(incomingBytes) || 0) < service.maxLogBytes) return
      const lock = service.acquireLogRotationLock()
      if (lock === undefined) return
      try {
        if (!fs.existsSync(service.logPath)) return
        const currentSize = fs.statSync(service.logPath).size
        if (currentSize + Math.max(0, Number(incomingBytes) || 0) < service.maxLogBytes) return
        for (let index = service.maxLogArchives; index >= 2; index -= 1) {
          service.moveBoundedLog(`${service.logPath}.${index - 1}`, `${service.logPath}.${index}`)
        }
        service.moveBoundedLog(service.logPath, `${service.logPath}.1`)
      } finally {
        service.releaseLogRotationLock(lock)
      }
    } catch { /* ignore rotation failures */ }
  }

  function appendLog(service, text) {
    try {
      service.rotateLogIfNeeded(Buffer.byteLength(text, 'utf8'))
      fs.appendFileSync(service.logPath, text, 'utf8')
    } catch { /* ignore disk failures; Output still receives the line */ }
  }

  function readLogSnapshot(service, maxLines = 260) {
    service.flushCoreLog()
    const empty = { path: service.logPath, lines: [], counts: { error: 0, warning: 0, info: 0, debug: 0 } }
    try {
      if (!fs.existsSync(service.logPath)) return empty
      const size = fs.statSync(service.logPath).size
      const length = Math.min(size, 256 * 1024)
      const buffer = Buffer.alloc(length)
      const handle = fs.openSync(service.logPath, 'r')
      try { fs.readSync(handle, buffer, 0, length, Math.max(0, size - length)) } finally { fs.closeSync(handle) }
      let rows = buffer.toString('utf8').split(/\r?\n/).filter(Boolean)
      if (size > length && rows.length) rows = rows.slice(1)
      rows = rows.slice(-Math.max(20, Math.min(500, Number(maxLines) || 260)))
      const counts = { error: 0, warning: 0, info: 0, debug: 0 }
      const lines = rows.map((raw, index) => {
        let time = ''
        let level = 'info'
        let message = raw
        if (raw.trimStart().startsWith('{')) {
          try {
            const item = JSON.parse(raw)
            time = String(item.time || item.ts || item.timestamp || '')
            level = String(item.level || item.severity || 'info').toLowerCase()
            message = String(item.msg || item.message || raw)
          } catch { /* plain text line */ }
        } else {
          const match = raw.match(/^(\S+(?:\s+\S+)?)\s+(DEBUG|INFO|WARN|WARNING|ERROR)\s+(.+)$/i)
          if (match) {
            time = match[1]
            level = match[2].toLowerCase()
            message = match[3]
          }
        }
        if (level === 'warn') level = 'warning'
        if (!Object.prototype.hasOwnProperty.call(counts, level)) level = 'info'
        counts[level] += 1
        const sourceMatch = message.match(/^\[([^\]]+)\]\s*(.*)$/)
        const source = sourceMatch?.[1] || (/companion|model/i.test(message) ? 'model' : /api|http/i.test(message) ? 'api' : /index/i.test(message) ? 'index' : 'core')
        if (sourceMatch) message = sourceMatch[2]
        return { id: `${size}-${index}`, time, level, source: String(source).slice(0, 48), message: String(message).slice(0, 1000) }
      })
      return { path: service.logPath, size, lines, counts }
    } catch (error) {
      return { ...empty, error: error instanceof Error ? error.message : String(error) }
    }
  }

  return { hostLog, enqueueCoreLog, flushCoreLog, ensureLogDir, acquireLogRotationLock, releaseLogRotationLock, moveBoundedLog, rotateLogIfNeeded, appendLog, readLogSnapshot }
}

module.exports = { createCoreLog }
