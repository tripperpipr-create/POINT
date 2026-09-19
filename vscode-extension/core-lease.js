// Дескриптор ядра, замок запуска и аренды окон.
//
// На этих ста тридцати строках держится общее тёплое ядро: одно окно Point его
// поднимает, остальные присоединяются по дескриптору, а живость каждого окна
// подтверждается файлом аренды с отметкой времени. Отсюда же следует, кого
// можно гасить: ядро без чужих свежих аренд — бесхозное.
//
// Порядок здесь не случайный и читается только подряд: замок берётся до
// проверки здоровья, дескриптор пишется после успешного старта, аренда
// начинается вместе с присоединением. Методами класса это стояло вперемешку
// со стартом, остановкой и HTTP-запросом.
//
// Приём тот же, что у `core-stream.js`: служба приходит первым доводом, а в
// классе остаётся строка-переходник.
function createCoreLease({ fs, path, crypto, normalizedWorkspaceRoot, removeFileIfExists, readJsonFile, processIsAlive }) {
  function runtimePaths(service, folder) {
    const workspaceRoot = normalizedWorkspaceRoot(folder.uri.fsPath)
    const workspaceKey = crypto.createHash('sha256').update(workspaceRoot).digest('hex').slice(0, 24)
    const runtimeDir = path.join(service.dataDirPath, 'runtime')
    fs.mkdirSync(runtimeDir, { recursive: true })
    service.runtimeWorkspaceKey = workspaceKey
    service.runtimeDescriptorPath = path.join(runtimeDir, `core-${workspaceKey}.json`)
    service.runtimeLockPath = path.join(runtimeDir, `core-${workspaceKey}.lock`)
    service.runtimeLeasePath = path.join(runtimeDir, `lease-${workspaceKey}-${service.runtimeLeaseId}.json`)
    return { workspaceRoot, workspaceKey, runtimeDir, descriptorPath: service.runtimeDescriptorPath, lockPath: service.runtimeLockPath }
  }

  async function isHealthy(service, baseUrl, timeoutMs = 1200) {
    if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(String(baseUrl || ''))) return false
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeoutMs)
    try {
      const response = await fetch(`${baseUrl}/api/health`, { signal: controller.signal })
      return response.ok
    } catch {
      return false
    } finally {
      clearTimeout(timer)
    }
  }

  async function tryAttachSharedCore(service, folder) {
    const runtime = service.runtimePaths(folder)
    const descriptor = readJsonFile(runtime.descriptorPath)
    if (!descriptor) return false
    const sameWorkspace = normalizedWorkspaceRoot(descriptor.workspaceRoot) === runtime.workspaceRoot
    const safeAddress = /^http:\/\/127\.0\.0\.1:\d+$/.test(String(descriptor.baseUrl || ''))
    if (!sameWorkspace || !safeAddress || !(await service.isHealthy(descriptor.baseUrl))) {
      removeFileIfExists(runtime.descriptorPath)
      return false
    }
    service.process = undefined
    service.attachedPid = Number(descriptor.pid) || undefined
    service.baseUrl = descriptor.baseUrl
    service.apiToken = await service.readApiToken()
    service.lastHealthAt = Date.now()
    service.startLease()
    service.hostLog('info', `[service] attached shared point-core pid=${service.attachedPid || 'unknown'} workspace=${folder.uri.fsPath}`)
    return true
  }

  async function acquireRuntimeLock(service, folder) {
    const runtime = service.runtimePaths(folder)
    const deadline = Date.now() + 35_000
    while (Date.now() < deadline) {
      if (await service.tryAttachSharedCore(folder)) return false
      try {
        const fd = fs.openSync(runtime.lockPath, 'wx')
        fs.writeFileSync(fd, JSON.stringify({ pid: process.pid, createdAt: new Date().toISOString() }))
        fs.closeSync(fd)
        service.ownsRuntimeLock = true
        return true
      } catch (error) {
        if (error?.code !== 'EEXIST') throw error
        try {
          const lock = readJsonFile(runtime.lockPath)
          const age = Date.now() - fs.statSync(runtime.lockPath).mtimeMs
          if (age > 35_000 && !processIsAlive(lock?.pid)) {
            removeFileIfExists(runtime.lockPath)
            continue
          }
        } catch { /* another host may be replacing the lock */ }
        await new Promise(resolve => setTimeout(resolve, 160))
      }
    }
    throw new Error('Другое окно Point слишком долго запускает общее ядро.')
  }

  function releaseRuntimeLock(service) {
    if (!service.runtimeLockPath || !service.ownsRuntimeLock) return
    removeFileIfExists(service.runtimeLockPath)
    service.ownsRuntimeLock = false
  }

  function writeRuntimeDescriptor(service, folder) {
    if (!service.runtimeDescriptorPath || !service.baseUrl || !service.process?.pid) return
    const descriptor = {
      version: 1,
      pid: service.process.pid,
      baseUrl: service.baseUrl,
      workspaceRoot: normalizedWorkspaceRoot(folder.uri.fsPath),
      startedAt: new Date().toISOString(),
    }
    const temporary = `${service.runtimeDescriptorPath}.${process.pid}.${Date.now()}.tmp`
    fs.writeFileSync(temporary, `${JSON.stringify(descriptor, null, 2)}\n`, { encoding: 'utf8', mode: 0o600 })
    removeFileIfExists(service.runtimeDescriptorPath)
    fs.renameSync(temporary, service.runtimeDescriptorPath)
  }

  function startLease(service) {
    if (!service.runtimeLeasePath) return
    const write = () => {
      try {
        fs.writeFileSync(service.runtimeLeasePath, JSON.stringify({ pid: process.pid, updatedAt: Date.now() }), { encoding: 'utf8', mode: 0o600 })
      } catch { /* closing windows may race removal of the runtime directory */ }
    }
    write()
    if (service.runtimeLeaseTimer) clearInterval(service.runtimeLeaseTimer)
    service.runtimeLeaseTimer = setInterval(write, 5000)
    service.runtimeLeaseTimer.unref?.()
  }

  function releaseLease(service) {
    if (service.runtimeLeaseTimer) {
      clearInterval(service.runtimeLeaseTimer)
      service.runtimeLeaseTimer = undefined
    }
    if (service.runtimeLeasePath) removeFileIfExists(service.runtimeLeasePath)
  }

  function otherLiveLeaseCount(service) {
    if (!service.runtimeWorkspaceKey || !service.runtimeDescriptorPath) return 0
    const runtimeDir = path.dirname(service.runtimeDescriptorPath)
    const prefix = `lease-${service.runtimeWorkspaceKey}-`
    let count = 0
    for (const name of fs.readdirSync(runtimeDir).filter(item => item.startsWith(prefix) && item.endsWith('.json'))) {
      const file = path.join(runtimeDir, name)
      if (file === service.runtimeLeasePath) continue
      const lease = readJsonFile(file)
      const fresh = Date.now() - Number(lease?.updatedAt || 0) < 20_000
      if (fresh && processIsAlive(lease?.pid)) count += 1
      else removeFileIfExists(file)
    }
    return count
  }

  function removeRuntimeDescriptorForPid(service, pid) {
    if (!service.runtimeDescriptorPath) return
    const descriptor = readJsonFile(service.runtimeDescriptorPath)
    if (!descriptor || Number(descriptor.pid) === Number(pid)) removeFileIfExists(service.runtimeDescriptorPath)
  }

  return { runtimePaths, isHealthy, tryAttachSharedCore, acquireRuntimeLock, releaseRuntimeLock, writeRuntimeDescriptor, startLease, releaseLease, otherLiveLeaseCount, removeRuntimeDescriptorForPid }
}

module.exports = { createCoreLease }
