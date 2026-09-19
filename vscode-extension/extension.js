const { handleMasterMessage, rememberMasterEditor } = require('./master-chat-controller')
const {
  handleCompanionChatMessage,
  bindCompanionChatHelpers,
  POINT_COMPANION_FEEDBACK_KEY,
} = require('./companion-chat-controller')
const { handleHubRuntimeMessage } = require('./hub-runtime-controller')
const { handleInfraMessage, handleGitAction: dispatchGitAction } = require('./infra-controller')
const vscode = require('vscode')
const path = require('path')
const fs = require('fs')
const net = require('net')
const crypto = require('crypto')
const os = require('os')
const { spawn } = require('child_process')
const cursorRuntime = require('./cursor-runtime')
const { restoreSystemBackup } = require('./backup-controller')
const { createNdjsonReader } = require('./core-stream')
const { createChatDocuments, companionDocumentHtml, POINT_COMPANION_ARCHIVES_KEY } = require('./chat-documents')
const {
  companionFactPairs,
  companionFeedbackMarks,
  withoutFeedbackFor,
  selectLogExcerptLines,
  isQuietApiRoute,
  sharedPointStoragePath,
  normalizedWorkspaceRoot,
  processIsAlive,
  removeFileIfExists,
  readJsonFile,
  upsertById,
  removeById,
} = require('./extension-utils')
const {
  parseJsonc,
  makefileTargets,
  buildRunConfigurations,
  pickDefaultRunConfiguration,
} = require('./run-config-utils')
const {
  symbolIcon,
  fuzzyScore,
  parseDocumentOutline,
  resolveOutlineSymbolAt,
  formatOutlineBreadcrumb,
  outlineKindIcon,
  mergeRecentFiles,
  formatCopyReference,
  parseSearchEverywhereQuery,
} = require('./ide-navigation-utils')
const {
  normalizeSSHRemotePath,
  sshRemotePathParent,
  sshRemotePathJoin,
  sshRemotePickerEntries,
} = require('./ssh-utils')
const { createCompanionController } = require('./companion-controller')
const { createProjectRegistry, PROJECTS_KEY } = require('./project-registry')
const { coreStateByKey, rememberWarmCore, reapWarmCores } = require('./core-warm-pool')
const { createIdeActionController } = require('./ide-action-controller')
const { createIdeNavigationController } = require('./ide-navigation-controller')
const { createConnectionController } = require('./connection-controller')
const { createPointPanels } = require('./point-panels')
const { handleRosterMessage } = require('./roster-controller')
const { handleLearningMessage } = require('./learning-controller')
const { handleToolingMessage } = require('./tooling-controller')
const { handleCursorMessage } = require('./cursor-controller')
const { createIDEObservations } = require('./ide-observation-controller')
const { createProjectIndex } = require('./project-index-controller')
const { createConsoleSSH } = require('./console-ssh-controller')

let activeView
let activeService
// Уборщик тёплых ядер держится отдельно от `activeView`: тот ставится только
// когда оболочка раскрывает вкладываемый вид, а в окне Чертога боковые панели
// скрыты и не раскрываются вовсе. Закрытие Чертога — единственный момент, когда
// бесхозные ядра можно убрать наверняка, и пропускать его нельзя.
let activeWarmPool

const HOST_LOG_RANK = { debug: 10, info: 20, warn: 30, error: 40 }
const CORE_LOG_MAX_BYTES = 4 * 1024 * 1024
const CORE_LOG_ARCHIVE_COUNT = 3
const POINT_WORKSPACE_ROOT_KEY = 'point.workspaceRoot'
// Кольцо тёплых ядер живёт в globalState, а не в памяти: перезагрузка окна
// Чертога не должна терять список к уборке. Потолок считается только по
// бесхозным ядрам — каждое открытое окно IDE законно держит своё сверх этого.
const WARM_CORES_KEY = 'point.warmCores.v1'
const WARM_CORES_KEEP = 2

// Окно Чертога узнаётся по своей рабочей области: оболочка открывает его файлом
// `agent-sessions.code-workspace`. Это верно уже при активации — раньше, чем
// вклад sessions успеет сказать про режим Хаба, — и потому годится там, где
// решение нужно принять до появления провайдера.
function isPointHubWindow() {
  const file = vscode.workspace.workspaceFile
  return file?.scheme === 'file' && /agent-sessions\.code-workspace$/i.test(file.fsPath)
}

let extensionGarbageCollector
function collectExtensionGarbage() {
  try {
    if (!extensionGarbageCollector) {
      require('node:v8').setFlagsFromString('--expose_gc')
      extensionGarbageCollector = require('node:vm').runInNewContext('gc')
      require('node:v8').setFlagsFromString('--no-expose_gc')
    }
    extensionGarbageCollector()
  } catch {
    // Memory release is opportunistic; lifecycle correctness never depends on it.
  }
}

function configuredLogLevel() {
  return String(vscode.workspace.getConfiguration('localAgent').get('logLevel', 'info') || 'info').toLowerCase()
}

function hostLogEnabled(level) {
  return (HOST_LOG_RANK[level] || HOST_LOG_RANK.info) >= (HOST_LOG_RANK[configuredLogLevel()] || HOST_LOG_RANK.info)
}

function hostLogStamp() {
  const d = new Date()
  const pad = n => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${String(d.getMilliseconds()).padStart(3, '0')}`
}

// Чтение потока ядра живёт в core-stream.js; здесь остаются оба объяснителя.
const readNdjson = createNdjsonReader({ newRequestId: (...args) => newRequestId(...args), describeCoreFailure: (...args) => describeCoreFailure(...args) })

function newRequestId() {
  return `req_${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`
}

// Папки изменений Git-панели: ключ в состоянии рабочей области и папка,
// которая есть всегда и которую нельзя удалить.
const GIT_LISTS_KEY = 'point.git.lists'
const GIT_DEFAULT_LIST = 'default'

class BackendService {
  constructor(context, output, onStatus) {
    this.context = context
    this.output = output
    this.onStatus = onStatus
    this.process = undefined
    this.attachedPid = undefined
    this.baseUrl = undefined
    this.apiToken = undefined
    this.state = 'stopped'
    this.lastDetail = ''
    this.startPromise = undefined
    this.lastHealthAt = 0
    this.dataDirPath = sharedPointStoragePath(this.context)
    this.logPath = path.join(this.dataDirPath, 'logs', 'point-core.log')
    // Keep every individual file comfortably below the editor's large-file
    // threshold. Three bounded archives retain useful history without letting
    // a permanently running IDE consume unbounded disk space.
    this.maxLogBytes = CORE_LOG_MAX_BYTES
    this.maxLogArchives = CORE_LOG_ARCHIVE_COUNT
    this.logRotationLockPath = `${this.logPath}.rotate.lock`
    this.logBuffer = ''
    this.logFlushTimer = undefined
    this.workspaceRootOverride = undefined
    this.runtimeDescriptorPath = undefined
    this.runtimeLockPath = undefined
    this.ownsRuntimeLock = false
    this.runtimeWorkspaceKey = undefined
    this.runtimeLeaseId = crypto.randomUUID()
    this.runtimeLeasePath = undefined
    this.runtimeLeaseTimer = undefined
  }

  setWorkspaceRoot(uri) {
    this.workspaceRootOverride = uri?.scheme === 'file' ? uri : undefined
  }

  workspaceFolder() {
    const folders = vscode.workspace.workspaceFolders || []
    if (this.workspaceRootOverride?.scheme === 'file') {
      const selectedRoot = normalizedWorkspaceRoot(this.workspaceRootOverride.fsPath)
      const selected = folders.find(folder => folder?.uri?.scheme === 'file'
        && normalizedWorkspaceRoot(folder.uri.fsPath) === selectedRoot)
      return selected || { uri: this.workspaceRootOverride, name: path.basename(this.workspaceRootOverride.fsPath) }
    }
    return folders.find(folder => folder?.uri?.scheme === 'file')
  }

  databaseToolPath() {
    const executable = process.platform === 'win32' ? 'point-db.exe' : 'point-db'
    const configuredCore = vscode.workspace.getConfiguration('localAgent').get('backendPath', '').trim()
    const candidates = [
      configuredCore ? path.join(path.dirname(configuredCore), executable) : '',
      path.join(this.context.extensionPath, 'bin', executable),
      path.resolve(this.context.extensionPath, '..', 'build', 'bin', executable),
    ].filter(Boolean)
    const found = candidates.find(candidate => fs.existsSync(candidate))
    if (!found) throw new Error(`Не найден ${executable}. Переустановите Point или соберите database recovery tool.`)
    return found
  }

  hostLog(level, message) {
    if (!hostLogEnabled(level)) return
    const line = `${hostLogStamp()} ${String(level).toUpperCase().padEnd(5)} ${message}`
    this.output.appendLine(line)
    this.appendLog(`${line}\n`)
  }

  enqueueCoreLog(text, toOutput = true) {
    if (!text) return
    if (toOutput) this.output.append(text)
    this.logBuffer += text
    if (this.logBuffer.length > 256 * 1024) {
      this.flushCoreLog()
      return
    }
    if (this.logFlushTimer) return
    this.logFlushTimer = setTimeout(() => {
      this.logFlushTimer = undefined
      this.flushCoreLog()
    }, 100)
  }

  flushCoreLog() {
    if (this.logFlushTimer) {
      clearTimeout(this.logFlushTimer)
      this.logFlushTimer = undefined
    }
    if (!this.logBuffer) return
    const chunk = this.logBuffer
    this.logBuffer = ''
    this.appendLog(chunk)
  }

  corePid() {
    return this.process?.pid || this.attachedPid || undefined
  }

  coreStatusTooltip() {
    const pid = this.corePid()
    const pidText = pid ? `pid ${pid}` : 'процесс не запущен'
    const detail = this.lastDetail ? `\n${this.lastDetail}` : ''
    return `Ядро Point · ${this.state} · ${pidText}${detail}\nХроника: ${this.logPath}`
  }

  setState(state, detail = '') {
    this.state = state
    this.lastDetail = detail || ''
    this.onStatus({ state, detail: this.lastDetail, pid: this.corePid(), logPath: this.logPath })
  }

  async ensureLogDir() {
    fs.mkdirSync(path.dirname(this.logPath), { recursive: true })
  }

  acquireLogRotationLock() {
    for (let attempt = 0; attempt < 2; attempt += 1) {
      try {
        const handle = fs.openSync(this.logRotationLockPath, 'wx', 0o600)
        fs.writeFileSync(handle, JSON.stringify({ pid: process.pid, createdAt: Date.now() }))
        return handle
      } catch (error) {
        if (error?.code !== 'EEXIST') return undefined
        try {
          const stale = Date.now() - fs.statSync(this.logRotationLockPath).mtimeMs > 10_000
          if (!stale) return undefined
          fs.unlinkSync(this.logRotationLockPath)
        } catch { /* another extension host released it */ }
      }
    }
    return undefined
  }

  releaseLogRotationLock(handle) {
    if (handle === undefined) return
    try { fs.closeSync(handle) } catch { /* already closed */ }
    try { fs.unlinkSync(this.logRotationLockPath) } catch { /* another host may have cleaned a stale lock */ }
  }

  moveBoundedLog(source, target) {
    if (!fs.existsSync(source)) return
    try { fs.unlinkSync(target) } catch (error) { if (error?.code !== 'ENOENT') throw error }
    const temporary = `${target}.rotate-${process.pid}-${crypto.randomUUID()}`
    fs.renameSync(source, temporary)
    try {
      const size = fs.statSync(temporary).size
      if (size <= this.maxLogBytes) {
        fs.renameSync(temporary, target)
        return
      }
      const length = Math.min(size, this.maxLogBytes)
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

  rotateLogIfNeeded(incomingBytes = 0) {
    try {
      if (!fs.existsSync(this.logPath)) return
      const size = fs.statSync(this.logPath).size
      if (size + Math.max(0, Number(incomingBytes) || 0) < this.maxLogBytes) return
      const lock = this.acquireLogRotationLock()
      if (lock === undefined) return
      try {
        if (!fs.existsSync(this.logPath)) return
        const currentSize = fs.statSync(this.logPath).size
        if (currentSize + Math.max(0, Number(incomingBytes) || 0) < this.maxLogBytes) return
        for (let index = this.maxLogArchives; index >= 2; index -= 1) {
          this.moveBoundedLog(`${this.logPath}.${index - 1}`, `${this.logPath}.${index}`)
        }
        this.moveBoundedLog(this.logPath, `${this.logPath}.1`)
      } finally {
        this.releaseLogRotationLock(lock)
      }
    } catch { /* ignore rotation failures */ }
  }

  appendLog(text) {
    try {
      this.rotateLogIfNeeded(Buffer.byteLength(text, 'utf8'))
      fs.appendFileSync(this.logPath, text, 'utf8')
    } catch { /* ignore disk failures; Output still receives the line */ }
  }

  readLogSnapshot(maxLines = 260) {
    this.flushCoreLog()
    const empty = { path: this.logPath, lines: [], counts: { error: 0, warning: 0, info: 0, debug: 0 } }
    try {
      if (!fs.existsSync(this.logPath)) return empty
      const size = fs.statSync(this.logPath).size
      const length = Math.min(size, 256 * 1024)
      const buffer = Buffer.alloc(length)
      const handle = fs.openSync(this.logPath, 'r')
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
      return { path: this.logPath, size, lines, counts }
    } catch (error) {
      return { ...empty, error: error instanceof Error ? error.message : String(error) }
    }
  }

  runtimePaths(folder) {
    const workspaceRoot = normalizedWorkspaceRoot(folder.uri.fsPath)
    const workspaceKey = crypto.createHash('sha256').update(workspaceRoot).digest('hex').slice(0, 24)
    const runtimeDir = path.join(this.dataDirPath, 'runtime')
    fs.mkdirSync(runtimeDir, { recursive: true })
    this.runtimeWorkspaceKey = workspaceKey
    this.runtimeDescriptorPath = path.join(runtimeDir, `core-${workspaceKey}.json`)
    this.runtimeLockPath = path.join(runtimeDir, `core-${workspaceKey}.lock`)
    this.runtimeLeasePath = path.join(runtimeDir, `lease-${workspaceKey}-${this.runtimeLeaseId}.json`)
    return { workspaceRoot, workspaceKey, runtimeDir, descriptorPath: this.runtimeDescriptorPath, lockPath: this.runtimeLockPath }
  }

  async isHealthy(baseUrl, timeoutMs = 1200) {
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

  async tryAttachSharedCore(folder) {
    const runtime = this.runtimePaths(folder)
    const descriptor = readJsonFile(runtime.descriptorPath)
    if (!descriptor) return false
    const sameWorkspace = normalizedWorkspaceRoot(descriptor.workspaceRoot) === runtime.workspaceRoot
    const safeAddress = /^http:\/\/127\.0\.0\.1:\d+$/.test(String(descriptor.baseUrl || ''))
    if (!sameWorkspace || !safeAddress || !(await this.isHealthy(descriptor.baseUrl))) {
      removeFileIfExists(runtime.descriptorPath)
      return false
    }
    this.process = undefined
    this.attachedPid = Number(descriptor.pid) || undefined
    this.baseUrl = descriptor.baseUrl
    this.apiToken = await this.readApiToken()
    this.lastHealthAt = Date.now()
    this.startLease()
    this.hostLog('info', `[service] attached shared point-core pid=${this.attachedPid || 'unknown'} workspace=${folder.uri.fsPath}`)
    return true
  }

  async acquireRuntimeLock(folder) {
    const runtime = this.runtimePaths(folder)
    const deadline = Date.now() + 35_000
    while (Date.now() < deadline) {
      if (await this.tryAttachSharedCore(folder)) return false
      try {
        const fd = fs.openSync(runtime.lockPath, 'wx')
        fs.writeFileSync(fd, JSON.stringify({ pid: process.pid, createdAt: new Date().toISOString() }))
        fs.closeSync(fd)
        this.ownsRuntimeLock = true
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

  releaseRuntimeLock() {
    if (!this.runtimeLockPath || !this.ownsRuntimeLock) return
    removeFileIfExists(this.runtimeLockPath)
    this.ownsRuntimeLock = false
  }

  writeRuntimeDescriptor(folder) {
    if (!this.runtimeDescriptorPath || !this.baseUrl || !this.process?.pid) return
    const descriptor = {
      version: 1,
      pid: this.process.pid,
      baseUrl: this.baseUrl,
      workspaceRoot: normalizedWorkspaceRoot(folder.uri.fsPath),
      startedAt: new Date().toISOString(),
    }
    const temporary = `${this.runtimeDescriptorPath}.${process.pid}.${Date.now()}.tmp`
    fs.writeFileSync(temporary, `${JSON.stringify(descriptor, null, 2)}\n`, { encoding: 'utf8', mode: 0o600 })
    removeFileIfExists(this.runtimeDescriptorPath)
    fs.renameSync(temporary, this.runtimeDescriptorPath)
  }

  startLease() {
    if (!this.runtimeLeasePath) return
    const write = () => {
      try {
        fs.writeFileSync(this.runtimeLeasePath, JSON.stringify({ pid: process.pid, updatedAt: Date.now() }), { encoding: 'utf8', mode: 0o600 })
      } catch { /* closing windows may race removal of the runtime directory */ }
    }
    write()
    if (this.runtimeLeaseTimer) clearInterval(this.runtimeLeaseTimer)
    this.runtimeLeaseTimer = setInterval(write, 5000)
    this.runtimeLeaseTimer.unref?.()
  }

  releaseLease() {
    if (this.runtimeLeaseTimer) {
      clearInterval(this.runtimeLeaseTimer)
      this.runtimeLeaseTimer = undefined
    }
    if (this.runtimeLeasePath) removeFileIfExists(this.runtimeLeasePath)
  }

  otherLiveLeaseCount() {
    if (!this.runtimeWorkspaceKey || !this.runtimeDescriptorPath) return 0
    const runtimeDir = path.dirname(this.runtimeDescriptorPath)
    const prefix = `lease-${this.runtimeWorkspaceKey}-`
    let count = 0
    for (const name of fs.readdirSync(runtimeDir).filter(item => item.startsWith(prefix) && item.endsWith('.json'))) {
      const file = path.join(runtimeDir, name)
      if (file === this.runtimeLeasePath) continue
      const lease = readJsonFile(file)
      const fresh = Date.now() - Number(lease?.updatedAt || 0) < 20_000
      if (fresh && processIsAlive(lease?.pid)) count += 1
      else removeFileIfExists(file)
    }
    return count
  }

  removeRuntimeDescriptorForPid(pid) {
    if (!this.runtimeDescriptorPath) return
    const descriptor = readJsonFile(this.runtimeDescriptorPath)
    if (!descriptor || Number(descriptor.pid) === Number(pid)) removeFileIfExists(this.runtimeDescriptorPath)
  }

  async ensureStarted() {
    if (this.state === 'running' && this.baseUrl && Date.now() - this.lastHealthAt < 3000) return
    if (this.state === 'running' && this.baseUrl && await this.isHealthy(this.baseUrl)) {
      this.lastHealthAt = Date.now()
      return
    }
    if (this.state === 'running') {
      this.releaseLease()
      this.process = undefined
      this.attachedPid = undefined
      this.baseUrl = undefined
      this.apiToken = undefined
      this.lastHealthAt = 0
      this.setState('stopped', 'Общее ядро было перезапущено другим окном.')
    }
    await this.start()
  }

  async start() {
    if (this.state === 'running' && this.baseUrl) return
    if (this.startPromise) return this.startPromise
    const pending = this.startCore()
    this.startPromise = pending
    try {
      return await pending
    } finally {
      if (this.startPromise === pending) this.startPromise = undefined
    }
  }

  async startCore() {
    if (!vscode.workspace.isTrusted) throw new Error('Сначала подтвердите доверие к рабочей папке Point.')
    const folder = this.workspaceFolder()
    if (!folder) throw new Error('Откройте папку проекта перед запуском агента Point.')
    if (folder.uri.scheme !== 'file') throw new Error('Point поддерживает только локальные папки проекта.')
    this.setState('starting', 'Запускаем локальный сервис…')
    fs.mkdirSync(this.dataDirPath, { recursive: true })
    await this.ensureLogDir()
    if (await this.tryAttachSharedCore(folder)) {
      this.setState('running', `${folder.name} · общее ядро`)
      return
    }
    const ownsRuntimeLock = await this.acquireRuntimeLock(folder)
    if (!ownsRuntimeLock) {
      this.setState('running', `${folder.name} · общее ядро`)
      return
    }
    const configured = vscode.workspace.getConfiguration('localAgent').get('backendPath', '').trim()
    const executable = process.platform === 'win32' ? 'point-core.exe' : 'point-core'
    const bundled = path.join(this.context.extensionPath, 'bin', executable)
    const development = path.resolve(this.context.extensionPath, '..', 'build', 'bin', executable)
    const binary = configured || (fs.existsSync(bundled) ? bundled : development)
    if (!fs.existsSync(binary)) {
      this.releaseRuntimeLock()
      this.setState('error', 'Не найден point-core')
      throw new Error(`Не найден ${executable}. Укажите путь к локальному ядру Point в настройках.`)
    }
    const port = await freePort()
    this.baseUrl = `http://127.0.0.1:${port}`
    const logLevel = String(vscode.workspace.getConfiguration('localAgent').get('logLevel', 'info') || 'info').toLowerCase()
    const logFormat = String(vscode.workspace.getConfiguration('localAgent').get('logFormat', 'text') || 'text').toLowerCase()
    const sandboxBackend = String(vscode.workspace.getConfiguration('localAgent').get('sandboxBackend', 'filtered-copy') || 'filtered-copy').toLowerCase()
    const sandboxImage = String(vscode.workspace.getConfiguration('localAgent').get('sandboxImage', '') || '').trim()
    const liveWorkspace = vscode.workspace.getConfiguration('localAgent').get('liveWorkspace', true) !== false
    const env = {
      ...process.env,
      DATA_DIR: this.dataDirPath,
      WORKSPACE_ROOT: folder.uri.fsPath,
      HTTP_ADDR: `127.0.0.1:${port}`,
      REDIS_ADDR: '',
      DEFAULT_OLLAMA_URL: vscode.workspace.getConfiguration('localAgent').get('ollamaBaseUrl', 'http://127.0.0.1:11434'),
      POINT_LOG_LEVEL: ['debug', 'info', 'warn', 'error'].includes(logLevel) ? logLevel : 'info',
      POINT_LOG_FORMAT: logFormat === 'json' ? 'json' : 'text',
      POINT_LOG_FILE: this.logPath,
      POINT_SANDBOX_BACKEND: sandboxBackend === 'docker' || sandboxBackend === 'container' ? 'docker' : 'filtered-copy',
      POINT_LIVE_WORKSPACE: liveWorkspace ? '1' : '0',
    }
    if (sandboxImage) env.POINT_SANDBOX_IMAGE = sandboxImage
    if (env.POINT_SANDBOX_BACKEND === 'docker') {
      env.POINT_SANDBOX_REQUIRE_STRONG = 'true'
      this.hostLog('info', `[sandbox] backend=docker requireStrong=true liveWorkspace=${liveWorkspace}${sandboxImage ? ` image=${sandboxImage}` : ''}`)
    } else {
      this.hostLog('info', `[sandbox] backend=filtered-copy liveWorkspace=${liveWorkspace}`)
    }
    this.hostLog('info', `[service] ${binary}`)
    this.hostLog('info', `[workspace] ${folder.uri.fsPath}`)
    this.hostLog('info', `[log] level=${env.POINT_LOG_LEVEL} format=${env.POINT_LOG_FORMAT} file=${this.logPath}`)
    const keepAliveAfterWindowClose = process.platform === 'win32'
    const child = spawn(binary, [], {
      cwd: folder.uri.fsPath,
      env,
      windowsHide: true,
      detached: keepAliveAfterWindowClose,
      stdio: keepAliveAfterWindowClose ? ['ignore', 'ignore', 'ignore'] : ['ignore', 'pipe', 'pipe'],
    })
    if (keepAliveAfterWindowClose) child.unref()
    this.process = child
    this.hostLog('info', `[service] point-core pid=${child.pid}`)
    child.on('error', error => {
      this.hostLog('error', `[service] spawn failed: ${error.message}`)
      if (this.process === child) {
        this.removeRuntimeDescriptorForPid(child.pid)
        this.releaseRuntimeLock()
        this.releaseLease()
        this.process = undefined
        this.attachedPid = undefined
        this.baseUrl = undefined
        if (this.state !== 'stopped') this.setState('error', `Не удалось запустить ядро: ${error.message}`)
      }
    })
    child.stdout?.on('data', chunk => {
      this.enqueueCoreLog(chunk.toString(), true)
    })
    child.stderr?.on('data', chunk => {
      this.enqueueCoreLog(`[stderr] ${chunk.toString()}`, true)
    })
    child.once('exit', (code, signal) => {
      this.flushCoreLog()
      const reason = signal || (code == null ? 'signal' : code)
      this.hostLog(code === 0 && !signal ? 'info' : 'warn', `[service] stopped (${reason})`)
      if (this.process === child) {
        this.removeRuntimeDescriptorForPid(child.pid)
        this.releaseLease()
        this.process = undefined
        this.attachedPid = undefined
        this.baseUrl = undefined
        if (this.state !== 'stopped') this.setState(code === 0 && !signal ? 'stopped' : 'error', `Сервис завершён: ${reason}`)
      }
    })
    try {
      await this.waitUntilReady(child)
      this.apiToken = await this.readApiToken()
      this.attachedPid = child.pid
      this.writeRuntimeDescriptor(folder)
      this.startLease()
      this.releaseRuntimeLock()
      this.lastHealthAt = Date.now()
      this.setState('running', folder.name)
    } catch (error) {
      this.releaseRuntimeLock()
      await this.stop()
      // Docker sandbox проверяется ядром при старте, и без ответа Docker Engine
      // ядро не поднимается вовсе. Настройка глобальная: без этой подсказки
      // включённая однажды песочница оставляет IDE без ядра во всех окнах.
      const strongSandbox = env.POINT_SANDBOX_BACKEND === 'docker'
      const detail = strongSandbox
        ? `${error.message} Включён Docker sandbox: ядро не стартует, пока Docker Engine не отвечает.`
        : error.message
      this.setState('error', detail)
      if (strongSandbox) void this.offerSandboxRollback()
      throw new Error(detail)
    }
  }

  // Выключается песочница тем же способом, каким включалась: настройка плюс
  // перезапуск ядра. Иначе человеку остаётся искать настройку вслепую в
  // нерабочей IDE.
  async offerSandboxRollback() {
    if (this.sandboxRollbackPrompted) return
    this.sandboxRollbackPrompted = true
    try {
      const choice = await vscode.window.showErrorMessage(
        'Ядро Point не запустилось с Docker sandbox. Docker Engine должен быть запущен, иначе автономные проекты выполнять негде.',
        'Выключить Docker sandbox', 'Хроника ядра',
      )
      if (choice === 'Хроника ядра') this.output.show(true)
      if (choice !== 'Выключить Docker sandbox') return
      await vscode.workspace.getConfiguration('localAgent').update('sandboxBackend', 'filtered-copy', vscode.ConfigurationTarget.Global)
      await vscode.commands.executeCommand('localAgent.restartServer')
    } catch (error) {
      this.hostLog('warn', `[sandbox] откат песочницы не выполнен: ${String(error?.message || error).slice(0, 200)}`)
    } finally {
      this.sandboxRollbackPrompted = false
    }
  }

  async readApiToken() {
    const tokenPath = path.join(this.dataDirPath, 'api-token')
    for (let attempt = 0; attempt < 40; attempt += 1) {
      try {
        const token = fs.readFileSync(tokenPath, 'utf8').trim()
        if (token) return token
      } catch { /* token file may not be ready yet */ }
      await new Promise(resolve => setTimeout(resolve, 50))
    }
    throw new Error('Не удалось прочитать API-токен локального ядра Point.')
  }

  async waitUntilReady(child) {
    let lastError
    const deadline = Date.now() + 30_000
    while (Date.now() < deadline) {
      if (!child || child.exitCode != null || child.signalCode) {
        const reason = child?.signalCode || child?.exitCode
        throw new Error(
          `Локальное ядро Point завершилось до готовности${reason != null ? ` (${reason})` : ''}. `
          + `Откройте «Хроника ядра» (Output) — ${this.logPath}`,
        )
      }
      try {
        const response = await fetch(`${this.baseUrl}/api/health`)
        if (response.ok) return
        lastError = new Error(`HTTP ${response.status}`)
      } catch (error) {
        lastError = error
      }
      await new Promise(resolve => setTimeout(resolve, 150))
    }
    const detail = lastError?.message || lastError?.cause?.message || 'нет ответа'
    const hint = /fetch failed|ECONNREFUSED|network/i.test(String(detail))
      ? 'Процесс не отвечает на 127.0.0.1 (проверьте антивирус/брандмауэр или «Хроника ядра»).'
      : detail
    throw new Error(`Локальный сервис не запустился за 30 с: ${hint}`)
  }

  async stop() {
    this.flushCoreLog()
    const child = this.process
    const pid = this.corePid()
    this.releaseLease()
    let otherLeases = 0
    try { otherLeases = this.otherLiveLeaseCount() } catch { /* runtime directory may already be gone */ }
    this.process = undefined
    this.attachedPid = undefined
    this.baseUrl = undefined
    this.apiToken = undefined
    this.lastHealthAt = 0
    this.setState('stopped')
    if (!pid) {
      this.releaseRuntimeLock()
      return
    }
    if (otherLeases > 0) {
      this.hostLog('info', `[service] detached from shared point-core pid=${pid}; clients=${otherLeases}`)
      return
    }
    this.removeRuntimeDescriptorForPid(pid)
    this.releaseRuntimeLock()
    if (child) await forceStopProcess(child)
    else await forceStopPid(pid)
  }

  // Closing Code-OSS is not an instruction to cancel a quest. On Windows the
  // detached Core keeps its descriptor and durable checkpoints; the next
  // window attaches to the same loopback endpoint. Explicit Stop still calls
  // stop() and terminates the process.
  detach() {
    this.flushCoreLog()
    this.releaseLease()
    this.process?.unref?.()
    this.process = undefined
    this.attachedPid = undefined
    this.baseUrl = undefined
    this.apiToken = undefined
    this.lastHealthAt = 0
    this.state = 'stopped'
  }

  // Адрес ядра и заголовок доступа собираются в одном месте.
  //
  // Их складывали руками в трёх: здесь, в потоке NDJSON и в потоке событий
  // хода Мастера. Третий при этом обходил службу целиком — а с ней ретраи,
  // таймауты и перевод отказа на русский. Пока формула повторяется, она
  // расходится молча: сменится схема доступа — и один из трёх останется на
  // старой.
  apiUrl(route) {
    return `${this.baseUrl}${route}`
  }

  authHeaders(extra = {}) {
    const headers = { ...extra }
    if (this.apiToken) headers.Authorization = `Bearer ${this.apiToken}`
    return headers
  }

  async request(route, options = {}) {
    const { timeoutMs = 20_000, allowStart = true, signal: externalSignal, ...requestOptions } = options
    if (allowStart) await this.ensureStarted()
    else if (this.state !== 'running' || !this.baseUrl) throw new Error('Локальное ядро остановлено.')
    const controller = new AbortController()
    let timedOut = false
    const timer = setTimeout(() => {
      timedOut = true
      controller.abort()
    }, timeoutMs)
    const onExternalAbort = () => controller.abort()
    if (externalSignal) {
      if (externalSignal.aborted) controller.abort()
      else externalSignal.addEventListener('abort', onExternalAbort, { once: true })
    }
    const method = String(requestOptions.method || 'GET').toUpperCase()
    const started = Date.now()
    const bodyBytes = typeof requestOptions.body === 'string' ? requestOptions.body.length : 0
    const incomingHeaders = requestOptions.headers || {}
    const requestId = String(incomingHeaders['X-Request-Id'] || incomingHeaders['x-request-id'] || newRequestId())
    try {
      const headers = this.authHeaders({
        ...(requestOptions.body ? { 'Content-Type': 'application/json' } : {}),
        ...incomingHeaders,
        'X-Request-Id': requestId,
      })
      // Keep-alive to 127.0.0.1: Node/Electron fetch already pools; Connection
      // header reinforces reuse across health/index/bootstrap chatter.
      const response = await fetch(this.apiUrl(route), {
        ...requestOptions,
        signal: controller.signal,
        headers: { Connection: 'keep-alive', ...headers },
      })
      const payload = response.status === 204 ? undefined : await response.json().catch(() => ({}))
      const ms = Date.now() - started
      const coreRequestId = response.headers?.get?.('X-Request-Id') || requestId
      if (!response.ok) {
        const message = payload?.error?.message || `HTTP ${response.status}`
        this.hostLog('warn', `[api] ${coreRequestId} ${method} ${route} -> ${response.status} ${ms}ms body=${bodyBytes}B error=${String(message).slice(0, 400)}`)
        throw new Error(message)
      }
      const quiet = isQuietApiRoute(method, route)
      if (!quiet || hostLogEnabled('debug')) {
        this.hostLog(quiet ? 'debug' : 'info', `[api] ${coreRequestId} ${method} ${route} -> ${response.status} ${ms}ms body=${bodyBytes}B`)
      }
      this.lastHealthAt = Date.now()
      return payload
    } catch (error) {
      if (error?.name === 'AbortError' || controller.signal.aborted) {
        if (externalSignal?.aborted && !timedOut) {
          const cancel = new Error('Companion chat cancelled')
          cancel.name = 'AbortError'
          cancel.cancelled = true
          this.hostLog('info', `[api] ${requestId} ${method} ${route} cancelled after ${Date.now() - started}ms`)
          throw cancel
        }
        this.hostLog('warn', `[api] ${requestId} ${method} ${route} timeout after ${timeoutMs}ms`)
        throw new Error(`Локальное ядро не ответило за ${Math.round(timeoutMs / 1000)} с. Откройте «Хроника ядра».`)
      }
      if (!String(error?.message || '').startsWith('HTTP ') && !String(error?.message || '').includes('provider') && !String(error?.message || '').includes('companion')) {
        this.hostLog('error', `[api] ${requestId} ${method} ${route} failed: ${String(error?.message || error).slice(0, 400)}`)
      }
      throw new Error(describeCoreFailure(error))
    } finally {
      clearTimeout(timer)
      if (externalSignal) externalSignal.removeEventListener('abort', onExternalAbort)
    }
  }

  requestNdjson(route, options = {}) { return readNdjson(this, route, options) }

  dispose() { this.detach() }
}

class AgentViewProvider {
  constructor(context, service, output, hooks = {}) {
    this.context = context
    this.service = service
    this.output = output
    this.view = undefined
    this.boot = undefined
    this.details = undefined
    this.activeRunId = undefined
    this.activeWorkflowRunId = undefined
    this.workflowDetails = undefined
    this.pollTimer = undefined
    this.pollInFlight = false
    this.flowCoordinatorTimer = undefined
    this.flowCoordinatorInFlight = false
    this.pendingExecutionLaunchInFlight = false
    this.companionPollTick = 0
    this.companionInterventionSignature = ''
    this.workflowPollTimer = undefined
    this.workflowPollInFlight = false
    this.autoStartTimer = undefined
    this.autoStartPromise = undefined
    // Hub v2 настраивается один раз для приложения, а не заново для каждой
    // открытой папки. Старый workspace-флаг относится к legacy Hub и намеренно
    // не влияет на чистый v2 namespace.
    this.onboardingComplete = Boolean(this.context.globalState?.get?.('point.agentHubV2.onboardingComplete', false))
    this.selectedTab = this.onboardingComplete ? 'master' : 'onboarding'
    this.panel = undefined
    this.statisticsPanel = undefined
    this.connectionsPanel = undefined
    this.dockerPanel = undefined
    this.companionPopup = undefined
    this.companionSidebar = undefined
    this.logChatPanel = undefined
    this.toolWindows = new Map()
    this.toolWindowStateSignatures = new Map()
    this.gitSelectedRoot = ''
    this.gitRepositoryKey = ''
    this.gitRepositoryListener = undefined
    this.gitRefreshTimer = undefined
    this.viewStateSignature = ''
    this.panelStateSignature = ''
    this.statisticsStateSignature = ''
    this.connectionsStateSignature = ''
    this.dockerStateSignature = ''
    this.companionPopupStateSignature = ''
    this.companionSidebarStateSignature = ''
    this.onAgentBusy = typeof hooks.onAgentBusy === 'function' ? hooks.onAgentBusy : () => {}
    this.onCompanionState = typeof hooks.onCompanionState === 'function' ? hooks.onCompanionState : () => {}
    this.ideContext = {}
    this.pendingCompanionFocus = undefined
    this.companionFocusTarget = 'dock'
    this.companionChatAbort = undefined
    this.hubPanelReady = false
    this.hubGarbageTimer = undefined
    this.companionDockReady = false
    this.companionPopupReady = false
    this.companionSidebarReady = false
    this.companionChatRequestSequence = 0
    this.companionActiveChatRequestId = 0
    this.companionThreadCache = { messages: [], draft: '', streamReply: '', loading: false, pendingSend: '', requestId: 0, updatedAt: 0 }
    this.cursorRuntimeState = { available: cursorRuntime.available, authenticated: false }
    this.cursorRun = undefined
    this.cursorHubExecutionId = ''
    this.cursorWorkflowStepBusy = false
    this.agentsWindowMode = false
    this.auxiliaryHubMode = false
    this.agentImprovementFocus = undefined
    this.agentImprovementFocusSequence = 0
  }

  workspaceFolder() {
    return this.service.workspaceFolder?.()
      || workspaceFolderForUri(vscode.window.activeTextEditor?.document?.uri)
  }

  async gitContext(rootHint = '') {
    const folder = this.workspaceFolder()
    const gitExt = vscode.extensions.getExtension('vscode.git')
    const api = gitExt?.isActive ? gitExt.exports?.getAPI?.(1) : (await gitExt?.activate())?.getAPI?.(1)
    const repositories = Array.isArray(api?.repositories) ? api.repositories : []
    const requestedRoot = String(rootHint || this.gitSelectedRoot || '')
    const requested = requestedRoot
      ? repositories.find(item => normalizedWorkspaceRoot(item?.rootUri?.fsPath) === normalizedWorkspaceRoot(requestedRoot))
      : undefined
    const repo = requested || pickGitRepository(repositories, folder?.uri || vscode.window.activeTextEditor?.document?.uri)
    if (repo?.rootUri?.fsPath) {
      this.gitSelectedRoot = repo.rootUri.fsPath
      this.bindGitRepository(repo)
    }
    this.bindGitApi(api)
    return { api, folder, repositories, repo }
  }

  // Расширение Git находит репозиторий не сразу: панель успевает спросить
  // состояние раньше и получить «репозитория нет». Своего слушателя у неё в
  // этот момент ещё нет — привязываться было не к чему, — и надпись оставалась
  // на экране до ручного обновления, хотя Git давно всё нашёл.
  bindGitApi(api) {
    if (!api || this.gitApiListener) return
    const refresh = () => {
      if (!this.toolWindows.has('git')) return
      void this.refreshToolWindowSnapshot('git')
    }
    const open = api.onDidOpenRepository?.(refresh)
    const close = api.onDidCloseRepository?.(refresh)
    if (!open && !close) return
    this.gitApiListener = { dispose: () => { open?.dispose?.(); close?.dispose?.() } }
    this.context.subscriptions.push(this.gitApiListener)
  }

  bindGitRepository(repo) {
    const key = String(repo?.rootUri?.toString?.() || '')
    if (!key || key === this.gitRepositoryKey) return
    this.gitRepositoryListener?.dispose?.()
    this.gitRepositoryKey = key
    this.gitRepositoryListener = repo.state?.onDidChange?.(() => {
      if (!this.toolWindows.has('git')) return
      if (this.gitRefreshTimer) clearTimeout(this.gitRefreshTimer)
      this.gitRefreshTimer = setTimeout(() => {
        this.gitRefreshTimer = undefined
        void this.refreshToolWindowSnapshot('git')
      }, 180)
    })
  }

  gitChanges(repo) {
    if (!repo?.state) return []
    const rootPath = repo.rootUri?.fsPath || ''
    const mapChange = (item, area) => {
      const status = Number(item?.status ?? -1)
      return {
        // Новый файл узнаётся по состоянию, а не по списку, в котором пришёл.
        // При настройке `git.untrackedChanges: mixed` — а она стоит по
        // умолчанию — расширение Git кладёт такие файлы в рабочую копию вместе
        // с изменёнными, и отдельная папка «Вне репозитория» оставалась пустой.
        area: area === 'working' && status === 7 ? 'untracked' : area,
        path: pathRelativeToRoot(item?.uri, rootPath),
        originalPath: pathRelativeToRoot(item?.originalUri, rootPath),
        status,
        uri: item?.uri,
      }
    }
    const map = (items, area) => Array.isArray(items) ? items.map(item => mapChange(item, area)) : []
    // Одна строка на файл. Git держит подготовленное и рабочее состояние
    // раздельно, и файл, подготовленный наполовину, показывался дважды: два
    // одинаковых пути, и непонятно, который из них ты отмечаешь. Панель говорит
    // о файле; индекс собирается из отметок в момент коммита.
    const merged = new Map()
    for (const item of [
      ...map(repo.state.mergeChanges, 'conflict'),
      ...map(repo.state.indexChanges, 'staged'),
      ...map(repo.state.workingTreeChanges, 'working'),
      ...map(repo.state.untrackedChanges, 'untracked'),
    ]) {
      if (!item.path) continue
      const seen = merged.get(item.path)
      if (!seen) {
        merged.set(item.path, { ...item, staged: item.area === 'staged', areas: [item.area] })
        continue
      }
      // Область первой встречи и есть главная: конфликт идёт раньше индекса,
      // индекс — раньше рабочей копии.
      if (!seen.areas.includes(item.area)) seen.areas.push(item.area)
      if (item.area === 'staged') seen.staged = true
      if (!seen.originalPath && item.originalPath) seen.originalPath = item.originalPath
    }
    return [...merged.values()]
  }

  // ── Папки изменений ──────────────────────────────────────────────────────
  // Git о них не знает и знать не обязан: это раскладка рабочего стола, а не
  // состояние репозитория. Живут в состоянии рабочей области, чтобы пережить
  // перезапуск, и чистятся сами — назначение хранится ровно пока файл изменён.
  gitLists(root) {
    const saved = this.context.workspaceState.get(GIT_LISTS_KEY) || {}
    return saved[normalizedWorkspaceRoot(root)]
  }

  async saveGitLists(root, value) {
    const saved = { ...(this.context.workspaceState.get(GIT_LISTS_KEY) || {}) }
    saved[normalizedWorkspaceRoot(root)] = value
    await this.context.workspaceState.update(GIT_LISTS_KEY, saved)
    return value
  }

  async syncGitLists(root, changes) {
    const saved = this.gitLists(root)
    const next = gitListsState(saved, changes)
    if (JSON.stringify(saved || null) !== JSON.stringify(next)) await this.saveGitLists(root, next)
    return next
  }

  // Заголовок и счётчик вкладки Git. Заголовок раздела в оболочке — часть
  // панели: дублировать ветку ещё и внутри вебвью значит потратить строку зря.
  paintGitViewChrome({ available, branch = '', count = 0 }) {
    const view = this.toolWindows.get('git')
    if (!view) return
    view.description = available ? (branch || 'без ветки') : ''
    view.badge = available && count ? { value: count, tooltip: `Изменённых файлов: ${count}` } : undefined
  }

  postToolWindow(kind, message) {
    void this.toolWindows.get(kind)?.webview.postMessage(message)
  }

  async refreshToolWindowSnapshot(kind, extra = {}) {
    if (!this.toolWindows.has(kind)) return undefined
    const snapshot = await this.toolWindowSnapshot(kind)
    this.postToolWindow(kind, { type: 'toolWindowState', snapshot, ...extra })
    return snapshot
  }

  async toolWindowSnapshot(kind) {
    if (kind === 'logs') return { kind, ...this.service.readLogSnapshot() }
    if (kind === 'terminal') {
      return {
        kind,
        terminals: vscode.window.terminals.map((terminal, index) => ({
          id: index, name: terminal.name, active: terminal === vscode.window.activeTerminal,
          exitStatus: terminal.exitStatus?.code,
        })),
        run: this.ideContext?.run || '',
        failure: this.ideContext?.failure || '',
      }
    }
    if (kind === 'git') {
      const { folder, repositories, repo } = await this.gitContext()
      const repositoryChoices = repositories.map(item => ({
        root: item.rootUri?.fsPath || '',
        name: path.basename(item.rootUri?.fsPath || '') || item.rootUri?.fsPath || 'Git',
        selected: item === repo,
      }))
      if (!repo?.state) {
        this.paintGitViewChrome({ available: false })
        return { kind, available: false, project: folder?.name || '', repositories: repositoryChoices }
      }
      const head = repo.state.HEAD || {}
      const tracked = this.gitChanges(repo)
      const lists = await this.syncGitLists(repo.rootUri?.fsPath || '', tracked)
      // Панель рисует до трёхсот строк: полторы тысячи новых файлов в дереве —
      // это не список, а стена. Сколько осталось за краем, она говорит вслух.
      const stats = await this.gitNumstat(repo.rootUri?.fsPath || '')
      const changes = tracked.slice(0, 300).map(({ uri, ...item }) => ({
        ...item,
        list: item.area === 'untracked' ? 'untracked' : (lists.assign[item.path] || lists.active),
        // Новый файл Git ещё не с чем сравнивать — у него нет ни плюсов, ни минусов.
        add: item.area === 'untracked' ? 0 : Number(stats[item.path]?.add || 0),
        del: item.area === 'untracked' ? 0 : Number(stats[item.path]?.del || 0),
      }))
      const hidden = Math.max(0, tracked.length - changes.length)
      const changeLists = lists.lists.map(item => ({
        id: item.id,
        name: item.name,
        active: item.id === lists.active,
        count: changes.filter(change => change.list === item.id).length,
      }))
      const branches = [...new Set([
        String(head.name || ''),
        ...(repo.state.refs || []).filter(item => Number(item?.type) === 0).map(item => String(item?.name || '')),
      ].filter(Boolean))].slice(0, 120)
      let commits = []
      let historyError = ''
      try {
        commits = (await repo.log({ maxEntries: 8, shortStats: true })).map(item => {
          const date = item.authorDate || item.commitDate
          return {
            hash: String(item.hash || ''),
            shortHash: String(item.hash || '').slice(0, 8),
            message: String(item.message || '').split(/\r?\n/, 1)[0].slice(0, 240),
            author: String(item.authorName || item.authorEmail || ''),
            date: date instanceof Date ? date.toISOString() : String(date || ''),
            files: Number(item.shortStat?.files || 0),
            insertions: Number(item.shortStat?.insertions || 0),
            deletions: Number(item.shortStat?.deletions || 0),
          }
        })
      } catch (error) {
        historyError = error instanceof Error ? error.message : String(error)
      }
      let workingStats = {}
      let stagedStats = {}
      try { workingStats = await repo.diffWithHEADShortStats() || {} } catch { /* unborn HEAD or binary-only diff */ }
      try { stagedStats = await repo.diffIndexWithHEADShortStats() || {} } catch { /* unborn HEAD or empty index */ }
      const upstream = head.upstream
        ? `${String(head.upstream.remote || '')}/${String(head.upstream.name || '')}`.replace(/^\//, '')
        : ''
      // Ветка и число изменений принадлежат не только содержимому панели: на
      // рейке слева Point теперь единственный вход в Git, и то, что раньше
      // показывал бейдж штатного SCM, обязано быть здесь.
      this.paintGitViewChrome({ available: true, branch: String(head.name || ''), count: tracked.length })
      return {
        kind, available: true, project: folder?.name || '', root: repo.rootUri?.fsPath || '',
        repository: path.basename(repo.rootUri?.fsPath || '') || folder?.name || '', repositories: repositoryChoices,
        branch: String(head.name || ''), detached: !head.name && Boolean(head.commit), head: String(head.commit || ''),
        ahead: Number(head.ahead || 0), behind: Number(head.behind || 0),
        remote: upstream, remotes: (repo.state.remotes || []).map(item => ({ name: String(item.name || ''), readOnly: Boolean(item.isReadOnly) })),
        branches, changes, changeLists, activeList: lists.active, commits, historyError,
        changesTotal: tracked.length, changesHidden: hidden,
        stashes: await this.gitStashes(repo.rootUri?.fsPath || ''),
        pushTargets: this.gitPushTargets(repo, upstream),
        operation: repo.state.rebaseCommit ? 'rebase' : (repo.state.mergeChanges || []).length ? 'merge' : '',
        stats: {
          working: { files: Number(workingStats.files || 0), insertions: Number(workingStats.insertions || 0), deletions: Number(workingStats.deletions || 0) },
          staged: { files: Number(stagedStats.files || 0), insertions: Number(stagedStats.insertions || 0), deletions: Number(stagedStats.deletions || 0) },
        },
      }
    }
    return { kind }
  }

  // Сколько строк прибавилось и убыло в каждом файле. Считается против HEAD:
  // панель говорит о будущем коммите, а не о том, что успело попасть в индекс.
  // `--no-renames` намеренно: переименование панель показывает одной строкой по
  // новому пути, и статистика должна лечь на неё же.
  async gitNumstat(root) {
    if (!root) return {}
    try {
      const out = await runGit(root, ['diff', '--numstat', '--no-renames', 'HEAD'])
      const stats = {}
      for (const line of out.split(/\r?\n/)) {
        const parts = line.split('\t')
        if (parts.length < 3) continue
        const file = parts.slice(2).join('\t').trim()
        if (!file) continue
        // Двоичный файл Git считает прочерками — чисел для него не существует.
        stats[file] = { add: Number(parts[0]) || 0, del: Number(parts[1]) || 0 }
      }
      return stats
    } catch {
      // Ветка без первого коммита или занятый индекс: панель обойдётся без чисел.
      return {}
    }
  }

  // Полка: записи `git stash` с датой и составом.
  async gitStashes(root) {
    if (!root) return []
    try {
      const out = await runGit(root, ['stash', 'list', '--pretty=%gd%x1f%s%x1f%cI'])
      const entries = out.split(/\r?\n/).filter(Boolean).slice(0, 20).map(line => {
        // Разделитель — управляющий символ 0x1F: в сообщении коммита может быть
        // что угодно, кроме него.
        // Дата приходит в ISO, а не готовой строкой: `%cr` говорит по-английски
        // («11 minutes ago»), а панель — по-русски, и форматирует её сама.
        const [ref, message, when] = line.split(String.fromCharCode(31))
        return { ref: String(ref || '').trim(), message: String(message || '').trim(), when: String(when || '').trim(), files: [] }
      })
      for (const entry of entries) {
        try {
          const files = await runGit(root, ['stash', 'show', '--name-status', entry.ref])
          entry.files = files.split(/\r?\n/).filter(Boolean).slice(0, 40).map(row => {
            const [status, ...rest] = row.split('\t')
            return { status: String(status || 'M').trim().slice(0, 1), path: rest.join('\t').trim() }
          }).filter(item => item.path)
        } catch {
          entry.files = []
        }
      }
      return entries
    } catch {
      return []
    }
  }

  // Куда отправлять: отслеживаемая ветка первой, следом остальные удалённые.
  gitPushTargets(repo, upstream) {
    const remotes = (repo.state.refs || [])
      .filter(item => Number(item?.type) === 1 && item?.name)
      .map(item => String(item.name))
    const unique = [...new Set([upstream, ...remotes].filter(Boolean))].slice(0, 12)
    return unique.map(name => ({ name, tracked: name === upstream }))
  }

  // Коммит по списку путей. Ждём дольше обычного: хуки бывают медленные, а
  // оборвать git на середине коммита хуже, чем подождать.
  async commitPaths(root, message, paths, amend = false) {
    // Правка последнего коммита без путей меняет одно сообщение; с путями —
    // добавляет в него отмеченные файлы. Оба случая штатные для `--amend`.
    const args = ['commit']
    if (amend) args.push('--amend')
    args.push('-m', message)
    if (paths.length) args.push('--', ...paths)
    try {
      await runGit(root, args, 2 * 1024 * 1024, 120_000)
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error || '')
      // Git объясняет отсутствие подписи автора тремя абзацами с примерами для
      // командной строки. В панели от них толку нет.
      if (/Please tell me who you are|empty ident|unable to auto-detect email/i.test(detail)) {
        throw new Error('Git не знает, кто вы. Задайте имя и почту: git config --global user.name «Имя» и user.email «почта».')
      }
      throw error
    }
  }

  // Расширение Git меняло вид этих методов: сейчас `add`, `revert` и `clean`
  // принимают пути строками и сами делают из них Uri, а раньше принимали Uri.
  // Передашь не то — получишь «path.replace is not a function», и падает вызов
  // на преобразовании списка, ДО запуска git: ни одного файла тронуть не
  // успевает. Поэтому сначала документированная форма, а на этой ошибке —
  // вторая попытка строками; она безопасна ровно потому, что первая ничего не
  // сделала.
  async gitFileCommand(repo, method, uris) {
    const list = (uris || []).filter(Boolean)
    if (!list.length) return
    try {
      await repo[method](list)
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error || '')
      if (!/is not a function/i.test(detail)) throw error
      await repo[method](list.map(uri => uri.fsPath))
    }
  }

  // Отправка ветки нужна двум действиям — «Отправить» и «Коммит и отправить», —
  // и первая публикация ветки в обоих случаях одна и та же. Возвращает null,
  // если человек закрыл выбор удалённого репозитория.
  async pushCurrentBranch(repo) {
    const head = repo.state.HEAD || {}
    if (!head.name) throw new Error('Создайте или выберите ветку перед Push.')
    // Человек мог выбрать в панели другую цель — тогда она главнее upstream.
    const chosen = String(this.gitPushTarget || '')
    let remoteName = chosen ? chosen.split('/', 1)[0] : String(head.upstream?.remote || '')
    if (!remoteName) {
      const writable = (repo.state.remotes || []).filter(item => !item.isReadOnly)
      if (!writable.length) throw new Error('Сначала добавьте удалённый репозиторий.')
      if (writable.length === 1) remoteName = writable[0].name
      else {
        const selected = await vscode.window.showQuickPick(writable.map(item => ({ label: item.name })), { title: 'Git · куда отправить ветку' })
        if (!selected?.label) return null
        remoteName = selected.label
      }
    }
    await repo.push(remoteName, head.name, !head.upstream)
    return { remote: remoteName, branch: head.name }
  }

  async handleGitAction(message) {
    return dispatchGitAction.call(this, message)
  }

  // Отметку читает следующий вопрос — и снимает её: недовольство одним ответом
  // не должно тянуться через весь разговор.
  consumeCompanionRejection() {
    const rejected = Boolean(this.companionRejectedAnswer)
    this.companionRejectedAnswer = false
    return rejected
  }

  // Состояние появляется не сразу, а отрисовка просит его рано: отметки —
  // украшение реплики, и ронять из-за них весь снимок нельзя.
  companionFeedbackRecords() { return this.context?.workspaceState?.get?.(POINT_COMPANION_FEEDBACK_KEY, []) || [] }

  async forgetCompanionFeedback(messages) {
    const records = this.companionFeedbackRecords()
    const kept = withoutFeedbackFor(records, messages)
    if (kept.length !== records.length) await this.context.workspaceState.update(POINT_COMPANION_FEEDBACK_KEY, kept)
  }

  patchBoot(fields) {
    this.boot = { ...(this.boot || {}), ...(fields || {}) }
    return this.boot
  }

  upsertBootItem(collection, value) {
    this.patchBoot({ [collection]: upsertById(this.boot?.[collection], value) })
    return value
  }

  // Контроллер подключений собирается на месте: он не держит состояния, а всё
  // нужное берёт через эти замыкания — так домен не получает доступ ко всему
  // composition root.
  connections() {
    return createConnectionController({
      secrets: this.context.secrets,
      request: (path, init) => this.service.request(path, init),
      getConnections: () => this.boot?.connections || [],
      upsert: value => this.upsertBootItem('connections', value),
      remove: id => this.removeBootItem('connections', id),
      patch: list => this.patchBoot({ connections: list }),
    })
  }

  removeBootItem(collection, id) {
    this.patchBoot({ [collection]: removeById(this.boot?.[collection], id) })
  }

  syncCustomTool(value) {
    const previousCustomIds = new Set((this.boot?.customTools || []).map(tool => String(tool?.id || '')))
    const customTools = upsertById(this.boot?.customTools, value)
    const builtIns = (this.boot?.toolCatalog || []).filter(item => !previousCustomIds.has(String(item?.name || '')))
    const customCatalog = customTools.map(tool => ({
      name: tool.id,
      displayName: tool.displayName,
      description: tool.description,
      category: 'execute',
      risk: 'CRITICAL',
      requiresApproval: true,
      providesVerification: Boolean(tool.providesVerification),
    }))
    this.patchBoot({ customTools, toolCatalog: [...builtIns, ...customCatalog] })
  }

  removeCustomToolFromBoot(id) {
    const customTools = removeById(this.boot?.customTools, id)
    const toolCatalog = (this.boot?.toolCatalog || []).filter(item => String(item?.name || '') !== String(id || ''))
    this.patchBoot({ customTools, toolCatalog })
  }

  async refreshRuntimeState({ companion = false } = {}) {
    const state = await this.service.request('/api/state/runtime')
    this.patchBoot(state)
    if (companion) await this.refreshLiveCompanionInterventions()
    return state
  }

  async refreshGuildState() {
    const state = await this.service.request('/api/state/guild')
    this.patchBoot(state)
    return state
  }

  async loadStatisticsSnapshot() {
    const [statistics, systemHealth] = await Promise.all([
      this.service.request('/api/statistics'),
      this.service.request('/api/system/diagnostics'),
    ])
    statistics.systemHealth = systemHealth
    return statistics
  }

  async refreshRuntimeAndGuildState({ companion = false } = {}) {
    const [runtime, guild] = await Promise.all([
      this.service.request('/api/state/runtime'),
      this.service.request('/api/state/guild'),
    ])
    this.patchBoot({ ...runtime, ...guild })
    if (companion) await this.refreshLiveCompanionInterventions()
    return { runtime, guild }
  }

  resolveWebviewView(view) {
    this.view = view
    this.viewStateSignature = ''
    this.companionDockReady = false
    activeView = this
    view.webview.options = { enableScripts: true, localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')] }
    view.webview.html = this.html(view.webview, 'companion')
    view.webview.onDidReceiveMessage(message => this.handleMessage(message), undefined, this.context.subscriptions)
    view.onDidChangeVisibility(() => { this.onHubVisibility(Boolean(view.visible)) }, undefined, this.context.subscriptions)
    view.onDidDispose(() => {
      this.view = undefined
      this.viewStateSignature = ''
      this.companionDockReady = false
      if (activeView === this) activeView = undefined
    })
  }

  resolveCompanionSidebar(view) {
    this.companionSidebar = view
    this.companionSidebarStateSignature = ''
    this.companionSidebarReady = false
    activeView = this
    view.webview.options = { enableScripts: true, localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')] }
    view.webview.html = this.html(view.webview, 'companion-sidebar')
    view.webview.onDidReceiveMessage(message => this.handleMessage(message), undefined, this.context.subscriptions)
    view.onDidChangeVisibility(() => { this.onHubVisibility(Boolean(view.visible)) }, undefined, this.context.subscriptions)
    view.onDidDispose(() => {
      this.companionSidebar = undefined
      this.companionSidebarStateSignature = ''
      this.companionSidebarReady = false
    })
  }

  resolveToolWindow(view, kind) {
    const key = String(kind || '').trim()
    if (!key) return
    this.toolWindows.set(key, view)
    this.toolWindowStateSignatures.delete(key)
    activeView = this
    view.webview.options = { enableScripts: true, localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')] }
    view.webview.html = this.html(view.webview, `tool-${key}`)
    view.webview.onDidReceiveMessage(message => this.handleMessage(message), undefined, this.context.subscriptions)
    view.onDidChangeVisibility(() => { this.onHubVisibility(Boolean(view.visible)) }, undefined, this.context.subscriptions)
    view.onDidDispose(() => {
      if (this.toolWindows.get(key) === view) this.toolWindows.delete(key)
      this.toolWindowStateSignatures.delete(key)
    })
  }

  async handleMessage(message) {
    try {
      const type = String(message?.type || '')
      const notableUi = new Set(['startRun', 'startFastAgent', 'undoRunPatches', 'cancelRun', 'pauseRun', 'resumeRun', 'rebuildIndex', 'companionChat', 'saveCompanionConfigAndChat', 'decideQuestProposal', 'startWorkflow', 'probeCompanionProvider', 'applyChangeSet', 'applyChangeSetChain', 'resolveFlowMerge'])
      if (type && type !== 'ready' && type !== 'selectTab') {
        this.service.hostLog(notableUi.has(type) ? 'info' : 'debug', `[ui] ${type}`)
      }
      // Галерее миров ядро не нужно, поэтому она работает и в безопасном режиме:
      // выбрать другой проект — ровно то действие, которым из недоверенной папки
      // и уходят.
      const safeModeActions = new Set(['ready', 'chooseProject', 'openProject', 'openProjectInIde', 'chooseProjectFolder',
        'pinProject', 'forgetProject', 'refreshProjects', 'cloneProject',
        // Чат чужого мира — законный выход из недоверенной папки, ровно как
        // галерея. Запирать его безопасным режимом значит запирать дверь.
        'openProjectChat',
        'manageTrust', 'showOutput', 'selectTab', 'openHub', 'openConnections', 'openStatistics', 'openDocker', 'focusHub', 'agentImprovementFocused', 'openCompanionPopup', 'closeCompanionPopup', 'openCompanionSidebar', 'copyCompanionText', 'openCompanionMessageDetails', 'showCompanionArchives', 'companionFeedback', 'copyMasterText', 'openMasterMessageDetails', 'masterFeedback'])
      if (!vscode.workspace.isTrusted && !safeModeActions.has(message.type)) {
        if (message.type === 'companionChat') {
          this.post({ type: 'companionChatError', message: 'Сначала разрешите доступ к папке проекта — тогда компаньон сможет ответить.' })
        } else if (message.type === 'saveCompanionConfigAndChat') {
          this.post({ type: 'companionSetupTestError', message: 'Сначала разрешите доступ к папке проекта — тогда компаньон сможет ответить.' })
        }
        this.postState()
        return
      }
      switch (message.type) {
        case 'ready':
          if (message.surface === 'wide' && this.panel) this.hubPanelReady = true
          if (message.surface === 'wide') void this.postProjects?.()
          if (message.surface === 'companion' && this.view) this.companionDockReady = true
          if ((message.surface === 'companion-popup' || message.surface === 'companion-peek') && this.companionPopup) this.companionPopupReady = true
          if (message.surface === 'companion-sidebar' && this.companionSidebar) this.companionSidebarReady = true
          if (!vscode.workspace.isTrusted) {
            this.postState(true)
          } else {
            // Paint the Guild immediately. Starting the local core and loading
            // its bootstrap data happen after the webview gets its first frame.
            this.postState(true)
            void this.refreshCursorRuntime()
            // A contributed webview can finish loading while its sidebar is
            // hidden. Editor/navigation commands must not start point-core as
            // a side effect of merely activating this extension.
            if (this.hubVisible() && this.workspaceFolder()) {
              if (vscode.workspace.getConfiguration('localAgent').get('autoStart', true)) this.scheduleAutoStart()
              else await this.refresh()
            }
          }
          this.flushCompanionFocus()
          break
        case 'openHub':
          this.showWide(this.onboardingComplete ? 'master' : 'onboarding'); break
        case 'chooseProject':
          await vscode.commands.executeCommand('localAgent.switchProject'); break
        // Путь из вебвью — не путь, а ключ поиска по реестру. Открываем только
        // то, что реестр уже знает; всё остальное идёт через диалог оболочки.
        case 'openProject': {
          const known = this.knownProject(message.path)
          if (known) await this.switchToProject?.(known)
          break
        }
        case 'openProjectInIde':
          await vscode.commands.executeCommand('localAgent.openProjectInIde', this.knownProject(message.path))
          break
        case 'chooseProjectFolder': {
          const picked = await vscode.window.showOpenDialog({
            canSelectFiles: false,
            canSelectFolders: true,
            canSelectMany: false,
            openLabel: this.agentsWindowMode ? 'Открыть в Чертоге' : 'Открыть в Point',
            title: 'Point — выбрать проект',
          })
          const uri = picked?.[0]
          if (!uri) break
          if (this.agentsWindowMode) await this.switchToProject?.(uri.fsPath)
          else await vscode.commands.executeCommand('vscode.openFolder', uri, false)
          break
        }
        case 'pinProject': {
          const known = this.knownProject(message.path)
          if (known) await this.projects?.setPinned?.(known, message.pinned === true)
          void this.postProjects?.()
          break
        }
        case 'forgetProject': {
          const known = this.knownProject(message.path)
          if (known) await this.projects?.forget?.(known)
          void this.postProjects?.()
          break
        }
        case 'refreshProjects':
          void this.postProjects?.()
          break
        case 'cloneProject':
          await vscode.commands.executeCommand('localAgent.gitClone'); break
        case 'openConnections':
          this.showConnections(); break
        case 'openStatistics':
          this.showStatistics(); break
        case 'openDocker':
          this.showDocker(); break
        case 'toolCommand': {
          const allowed = new Set([
            'localAgent.openTerminal', 'localAgent.runAnything', 'localAgent.newConsoleChannel',
            'localAgent.selectRunConfiguration', 'localAgent.runWithoutDebug', 'localAgent.startDebug',
            'localAgent.vcsChanges', 'localAgent.openChronicle', 'localAgent.gitClone',
            'localAgent.vcsCommit', 'localAgent.vcsPush', 'localAgent.vcsPull',
            'localAgent.vcsRollback', 'localAgent.vcsShowDiff', 'localAgent.showCoreChronicle',
            'localAgent.openLogChat',
            'localAgent.askCompanionAboutTerminal', 'localAgent.askCompanionAboutDiff',
            'localAgent.askCompanionAboutProblems', 'localAgent.connectServer',
            'localAgent.openDatabases', 'localAgent.rebuildIndex', 'localAgent.showIndexStatus',
          ])
          const command = String(message.command || '')
          if (!allowed.has(command)) throw new Error('Недоступное действие окна инструментов')
          await vscode.commands.executeCommand(command)
          break
        }
        case 'gitAction':
        case 'loadToolWindowState':
        case 'loadDocker':
        case 'dockerContainerAction':
        case 'dockerLogs':
        case 'dockerOpenTerminal':
        case 'saveServerProfile':
        case 'probeServerProfile':
        case 'listServerRemote':
        case 'openServerTerminal':
        case 'deleteServerProfile':
        case 'saveDBConnection':
        case 'deleteDBConnection':
        case 'testDBConnection':
        case 'schemaDBConnection':
        case 'queryDBConnection':
          await handleInfraMessage.call(this, message)
          break
        case 'openCompanionSidebar':
          await this.showCompanionSidebar()
          this.pushCompanionThreadSync('sidebar')
          break
        case 'openCompanionPopup':
          this.showCompanionPeek()
          this.pushCompanionThreadSync('peek')
          if (typeof message.message === 'string' && message.message.trim()) {
            this.queueCompanionFocus({
              message: message.message,
              send: Boolean(message.send),
              surface: 'peek',
            })
          }
          break
        case 'companionThreadUpdate':
        case 'copyCompanionText':
        case 'openCompanionMessageDetails':
        case 'companionFeedback':
        case 'newCompanionThread':
        case 'showCompanionArchives':
        case 'stopCompanionChat':
        case 'companionChat':
        case 'clearCompanionHistory':
        case 'dismissCompanionIntervention':
        case 'restoreCompanionInterventions':
        case 'probeCompanionConnection':
        case 'saveCompanionConfig':
        case 'saveCompanionConfigAndChat':
          await handleCompanionChatMessage.call(this, message)
          break
        case 'closeCompanionPopup':
          this.closeCompanionPopup(); break
        case 'focusHub':
          if (message.agentId) this.focusAgentImprovement(message.agentId, message.constructorStep)
          else this.showWide(message.tab || 'overview')
          break
        case 'agentImprovementFocused':
          if (this.agentImprovementFocus?.requestId === message.requestId) {
            this.agentImprovementFocus = undefined
            this.postState()
          }
          break
        case 'manageTrust':
          await vscode.commands.executeCommand('workbench.trust.manage'); break
        case 'startServer':
          if (!this.workspaceFolder()) {
            await vscode.commands.executeCommand('localAgent.switchProject')
            break
          }
          await this.service.start(); await this.refresh(); break
        case 'restartServer':
          await this.service.stop(); await this.service.start(); await this.refresh(); break
        case 'startRun':
        case 'startFastAgent':
        case 'undoRunPatches':
        case 'previewRun':
        case 'cancelRun':
        case 'pauseRun':
        case 'resumeRun':
        case 'extendActiveTime':
        case 'messageRun':
        case 'forbidFile':
        case 'loadContextInspector':
        case 'contextAmend':
        case 'addRunContextFiles':
        case 'addRunContextSelection':
        case 'saveFlow':
        case 'startFlowRun':
        case 'loadQuestOutcome':
        case 'loadQuestReplans':
        case 'replanQuest':
        case 'reviseQuestBrief':
        case 'loadHandoffs':
        case 'loadDecisions':
        case 'resolveDecision':
        case 'resolveChangeSet':
        case 'resolveApproval':
        case 'loadRun':
        case 'decideQuestProposal':
        case 'decideCompanionAction':
        case 'previewEquipSkill':
        case 'equipSkill':
        case 'revertExecution':
        case 'revertQuest':
        case 'revertFlowNode':
        case 'launchExecution':
        case 'resolveFlowNode':
        case 'resolveFlowMerge':
        case 'applyChangeSetChain':
        case 'applyChangeSet':
        case 'rejectChangeSet':
        case 'revertChangeSet':
          await handleHubRuntimeMessage.call(this, message)
          break
        case 'saveMemory': {
          const saved = await this.service.request('/api/memories', { method: 'POST', body: JSON.stringify(message.memory) })
          this.upsertBootItem('memories', saved)
          this.postState()
          break
        }
        case 'deleteMemory': {
          const answer = await vscode.window.showWarningMessage(
            'Удалить эту запись памяти текущего проекта? Действие нельзя отменить.',
            { modal: true },
            'Удалить',
          )
          if (answer !== 'Удалить') break
          await this.service.request(`/api/memories/${encodeURIComponent(message.memoryId)}`, { method: 'DELETE' })
          this.removeBootItem('memories', message.memoryId)
          this.post({ type: 'memoryDeleted', memoryId: message.memoryId })
          this.postState()
          break
        }
        case 'loadFileHistory': {
          const path = String(message.path || '').trim()
          if (!path) break
          // Путь не чистим здесь: границу рабочей папки проверяет ядро, и
          // вторая, отличающаяся проверка в расширении только создала бы
          // расхождение между тем, что разрешено, и тем, что показано.
          const history = await this.service.request(`/api/files/history?path=${encodeURIComponent(path)}`)
          this.post({ type: 'fileHistory', history })
          break
        }
        case 'capabilityDelta': {
          // Что изменится от навыка или умения — считает ядро тем же кодом,
          // что и саму годность.
          const delta = await this.service.request('/api/project-agents/capability-delta', {
            method: 'POST', body: JSON.stringify({
              profile: message.profile || {},
              addTools: message.addTools || [],
              removeTools: message.removeTools || [],
            }),
          })
          this.post({ type: 'capabilityDelta', delta, key: message.key || '' })
          break
        }
        case 'agentCapability': {
          // Что агент сможет — считает ядро тем же кодом, который это разрешает.
          const capability = await this.service.request('/api/project-agents/capability', {
            method: 'POST', body: JSON.stringify(message.profile || {}),
          })
          // Ключ возвращается вместе с ответом: у веб-вью несколько агентов на
          // экране, и без ключа ответ невозможно отнести к нужному.
          this.post({ type: 'agentCapability', capability, key: String(message.key || '') })
          break
        }
        case 'orchestratorPolicy': {
          // Политику считает ядро — тем же кодом, который её исполняет.
          const policy = await this.service.request('/api/orchestrator/policy', {
            method: 'POST', body: JSON.stringify(message.draft || {}),
          })
          // Ключ возвращается с ответом: на экране может быть два разных
          // черновика (форма настройки и карточка обзора), и без ключа ответ
          // невозможно отнести к нужному.
          this.post({ type: 'orchestratorPolicy', policy, key: String(message.key || '') })
          break
        }
        case 'forkMasterConversation':
        case 'deleteMasterConversation':
        case 'exportMasterConversation':
        case 'pickMasterModel':
        case 'pickMasterContext':
        case 'previewMasterContext':
        case 'masterPage':
        case 'attachMasterContext':
        case 'masterSession':
        case 'loadMaster':
        case 'masterChat':
        case 'copyMasterText':
        case 'openMasterMessageDetails':
        case 'masterFeedback':
        case 'stopMasterChat':
        case 'approveMasterWorkOrderV2':
		case 'reviseMasterWorkOrderV2':
        case 'controlMasterWorkOrderQuestV2':
        case 'controlMasterApplicationV2':
          await handleMasterMessage.call(this, message)
          break
        case 'loadStatistics':
        case 'createSystemBackup':
        case 'restoreSystemBackup':
        case 'searchExperience':
        case 'previewManualLearning':
        case 'applyManualLearning':
        case 'rollbackAgentImprovement':
        case 'promoteAgentImprovement':
        case 'saveBudget':
          await handleLearningMessage.call(this, message)
          break
        case 'saveProfile':
        case 'deleteProfile':
        case 'deleteProjectAgent':
        case 'deleteQuest':
        case 'deleteTeam':
        case 'deleteFlow':
        case 'deleteWorkOrderV2':
        case 'deleteBlueprint':
        case 'exportProfile':
        case 'importProfile':
          await handleRosterMessage.call(this, message)
          break
        case 'attachFiles':
          await this.attachFiles(); break
        case 'attachSelection':
          await this.attachSelection(); break
        case 'previewContext': {
          const contextItems = Array.isArray(message.contextItems) ? message.contextItems : []
          try {
            const preview = await this.service.request('/api/context/preview', { method: 'POST', body: JSON.stringify({ contextItems }) })
            this.post({ type: 'contextPreview', preview })
          } catch (error) {
            this.post({ type: 'contextPreviewError', message: error instanceof Error ? error.message : String(error) })
          }
          break
        }
        case 'saveCustomTool':
        case 'previewCustomTool':
        case 'previewTool':
        case 'requestToolExecutionApproval':
        case 'resolveToolExecutionApproval':
        case 'executeTool':
        case 'claimWorkflowStep':
        case 'heartbeatWorkflowStep':
        case 'completeWorkflowStep':
        case 'deleteCustomTool':
        case 'exportCustomTool':
        case 'importCustomTool':
        case 'probeProvider':
        case 'probeModelCapability':
          await handleToolingMessage.call(this, message)
          break
        case 'rebuildIndex': {
          this.service.hostLog('info', '[index] rebuild requested')
          if (this.indexController) {
            await this.indexController.rebuildNow({ notify: false })
          } else {
            const indexStatus = await this.service.request('/api/index/rebuild', { method: 'POST', body: '{}', timeoutMs: 120_000 })
            this.patchBoot({ indexStatus })
          }
          this.post({ type: 'indexRebuilt' })
          this.postState()
          break
        }
        case 'saveQuickChatSettings': {
          const cfg = vscode.workspace.getConfiguration('localAgent')
          if (typeof message.defaultProfileId === 'string') {
            await cfg.update('quickChatDefaultProfileId', message.defaultProfileId, vscode.ConfigurationTarget.Global)
          }
          this.postState(true)
          break
        }
        case 'openQuickChat':
          await vscode.commands.executeCommand('localAgent.openAgentComposer')
          break
        case 'enableDockerSandbox': {
          // Ядро читает бэкенд песочницы один раз — при запуске, из окружения.
          // Поэтому настройки мало: без перезапуска ядро осталось бы в
          // filtered-copy, а карточка показывала бы ту же блокировку, хотя
          // переключатель уже стоит в docker.
          const cfg = vscode.workspace.getConfiguration('localAgent')
          await cfg.update('sandboxBackend', 'docker', vscode.ConfigurationTarget.Global)
          await vscode.commands.executeCommand('localAgent.restartServer')
          this.postState(true)
          break
        }
        case 'revertPatch':
          await this.revertPatch(message.id); break
        case 'launchCursorAgent':
        case 'cursorRefresh':
        case 'cursorLogin':
        case 'cursorLogout':
        case 'startCursorRun':
        case 'cancelCursorRun':
        case 'cancelCursorExecution':
          await handleCursorMessage.call(this, message)
          break
        case 'completeOnboarding':
          this.onboardingComplete = true
          await this.context.globalState?.update?.('point.agentHubV2.onboardingComplete', true)
          this.selectedTab = 'master'
          this.postState(true)
          break
        case 'restartOnboarding':
          this.onboardingComplete = false
          await this.context.globalState?.update?.('point.agentHubV2.onboardingComplete', false)
          this.selectedTab = 'onboarding'
          this.postState(true)
          break
        case 'saveWorkflow':
        case 'deleteWorkflow':
        case 'startWorkflow':
        case 'cancelWorkflow':
        case 'loadWorkflowRun':
          await handleToolingMessage.call(this, message)
          break
        case 'openFile':
          await openWorkspaceFile(message.path, message.line, this.auxiliaryHubMode ? 'main' : this.agentsWindowMode); break
        case 'showOutput':
          this.output.show(true); break
        case 'saveTeam':
        case 'saveSkill':
          await handleRosterMessage.call(this, message)
          break
        case 'saveConnection':
        case 'probeConnection':
        case 'defaultConnection':
        case 'deleteConnection': {
          await this.connections().handle(message)
          this.postState()
          break
        }
        case 'saveModelRouting': {
          const saved = await this.service.request('/api/workspace/model-routing', {
            method: 'PUT',
            body: JSON.stringify(message.routing || {}),
          })
          this.patchBoot({ modelRouting: saved })
          this.postState()
          break
        }
        case 'saveOrchestratorConfig': {
          const saved = await this.service.request('/api/orchestrator/config', { method: 'POST', body: JSON.stringify(orchestratorConfigPayload(message.config)) })
          this.patchBoot({ orchestrator: saved })
          this.post({ type: 'orchestratorConfigSaved', configId: saved.id })
          this.postState()
          break
        }
        case 'saveBlueprint':
        case 'previewCompiledPrompt':
        case 'saveProjectAgent':
        case 'applyBlueprintToAgent':
        case 'previewBlueprintSync':
        case 'updateBlueprintFromAgent':
          await handleRosterMessage.call(this, message)
          break
        case 'selectTab':
          if (message.tab === 'statistics') {
            this.showStatistics()
            break
          }
          if (message.tab === 'docker') {
            this.showDocker()
            break
          }
          if (!this.onboardingComplete && message.tab && message.tab !== 'onboarding') {
            this.selectedTab = 'onboarding'
            this.postState()
            break
          }
          this.selectedTab = message.tab
          // Вкладка запоминается на мир: возврат в проект должен возвращать и
          // место, на котором его оставили.
          void this.projects?.rememberTab?.(this.workspaceFolder()?.uri?.fsPath || '', message.tab)
          this.postState()
          break
        case 'openRoster':
          this.showWide('settings'); break
        default:
          break
      }
    } catch (error) {
      // notify уже посылает webview сообщение вида {type:'error'} — отдельного
      // канала об отказе заводить не нужно. Не хватало ему только имени
      // запроса: без него интерфейс не знает, какой раздел замер в «загрузке».
      this.notify(error, String(message?.type || ''))
    }
  }

  async refresh() {
    void this.refreshCursorRuntime()
    if (this.service.state !== 'running') { this.postState(); return }
    this.boot = await this.service.request('/api/bootstrap')
    await this.coordinateActiveFlows(false)
    this.companionInterventionSignature = this.liveCompanionSignature(this.boot)
    if (this.activeRunId) await this.loadRun(this.activeRunId, false)
    if (this.activeWorkflowRunId) await this.loadWorkflowRun(this.activeWorkflowRunId, false)
    this.postState()
    this.onCompanionState(this.boot)
  }

  liveCompanionSignature(boot = this.boot) {
    return JSON.stringify({
      items: (boot?.companionInterventions || []).map(item => [item.id, item.occurrenceKey]),
      dismissed: Number(boot?.companionDismissedCount || 0),
    })
  }

  async refreshLiveCompanionInterventions() {
    // Editor focus changes are part of normal IDE navigation. They may refresh
    // an already running assistant, but must never boot point-core on their own.
    if (this.service.state !== 'running') return
    const focus = companionWorkspaceRelativePath(vscode.window.activeTextEditor?.document?.uri)
    const query = focus ? `?focusPath=${encodeURIComponent(focus)}` : ''
    const live = await this.service.request(`/api/companion/live${query}`)
    if (this.boot) {
      if (live?.companion) this.boot.companion = live.companion
      this.boot.companionInterventions = live?.companionInterventions || []
      this.boot.companionDismissedCount = Number(live?.companionDismissedCount || 0)
    }
    const signature = this.liveCompanionSignature(this.boot)
    if (signature === this.companionInterventionSignature) return
    this.companionInterventionSignature = signature
    this.post({
      type: 'companionInterventions',
      interventions: live?.companionInterventions || [],
      dismissedCount: Number(live?.companionDismissedCount || 0),
      executions: this.boot?.executions || [],
    })
    this.onCompanionState(this.boot)
  }

  // Одно место, где решается, каким подключением идёт запрос.
  //
  // Раньше этот поиск был написан трижды копипастой — для квеста, компаньона и
  // Мастера, — и каждый раз брал первое подключение с подходящим пресетом. При
  // двух ключах одного провайдера (личный и рабочий, dev и prod) запрос молча
  // уходил с чужим. Теперь сущность несёт connectionId, а совпадение по пресету
  // осталось только для строк, которым связь не досталась: там неоднозначность
  // называется вслух, а не разрешается за человека.
  resolveConnection(owner, what) {
    const connections = this.boot?.connections || []
    const linked = owner?.connectionId ? connections.find(item => item.id === owner.connectionId) : undefined
    if (owner?.connectionId && !linked) {
      throw new Error(`Подключение для ${what} не найдено. Выберите его заново в разделе «Связи».`)
    }
    if (linked) return linked
    const candidates = connections.filter(item => owner?.providerPreset
      ? item.presetId === owner.providerPreset
      : Boolean(owner?.provider) && item.provider === owner.provider)
    if (candidates.length > 1) {
      const names = candidates.map(item => item.displayName || item.id).join(', ')
      throw new Error(`Для ${what} подходят несколько подключений: ${names}. Выберите одно явно в разделе «Связи».`)
    }
    return candidates[0]
  }

  async credentialFor(owner, what, fallback = '') {
    const connection = this.resolveConnection(owner, what)
    const apiKey = connection?.secretRef
      ? await this.context.secrets.get(connection.secretRef) || fallback
      : fallback
    const preset = (this.boot?.providerCatalog || []).find(item => item.id === (connection?.presetId || owner?.providerPreset))
    if (preset?.requiresApiKey && !apiKey) {
      throw new Error(`Для ${preset.name || preset.id} не найден credential в SecretStorage`)
    }
    return apiKey
  }

  async credentialForExecution(executionId, fallback = '') {
    const execution = this.boot?.executions?.find(item => item.id === executionId)
    if (!execution) return fallback
    const agent = this.boot?.projectAgents?.find(item => item.id === execution.projectAgentId)
      || this.boot?.profiles?.find(item => item.id === execution.projectAgentId)
    if (!agent) return fallback
    return await this.credentialFor(agent, `агента «${agent.name || agent.id}»`, fallback)
  }

  async credentialForCompanion() {
    const companion = this.boot?.companion
    if (!companion?.provider || !companion?.model) return ''
    return await this.credentialFor(companion, 'компаньона')
  }

  async credentialForOrchestrator() {
    const orchestrator = this.boot?.orchestrator
    if (!orchestrator?.provider || !orchestrator?.model) return ''
    return await this.credentialFor(orchestrator, 'Мастера')
  }

  async launchHubExecution(executionId, fallbackKey = '') {
    const execution = (this.boot?.executions || []).find(item => item.id === executionId)
    const projectAgent = (this.boot?.projectAgents || []).find(item => item.id === execution?.projectAgentId)
    if (execution && projectAgent?.provider === 'cursor-cli') {
      return await this.launchCursorHubExecution(execution)
    }
    const apiKey = await this.credentialForExecution(executionId, fallbackKey)
    return await this.service.request(`/api/executions/${encodeURIComponent(executionId)}/launch`, {
      method: 'POST',
      body: JSON.stringify({ apiKey }),
    })
  }

  async launchCursorHubExecution(execution) {
    if (!execution?.id) throw new Error('Cursor-исполнение не найдено.')
    if (this.cursorRun) {
      if (this.cursorHubExecutionId === execution.id) return { external: true, executionId: execution.id }
      throw new Error('Cursor Agent уже выполняет другую задачу.')
    }
    const state = await this.refreshCursorRuntime()
    if (!state.available) throw new Error(state.error || 'Cursor SDK недоступен.')
    if (!state.authenticated) throw new Error(state.error || 'Войдите в Cursor перед запуском агента.')

    const launch = await this.service.request(`/api/executions/${encodeURIComponent(execution.id)}/cursor/start`, {
      method: 'POST', body: '{}',
    })
    let cursorRun
    try {
      cursorRun = cursorRuntime.startRun({
        profile: launch.profile || {},
        task: launch.execution?.task || execution.task || '',
        cwd: launch.sandboxPath,
        onEvent: event => {
          this.post({ type: 'cursorRunEvent', event, executionId: execution.id })
          this.post({ type: 'runDelta', details: this.details, workflowDetails: this.workflowDetails, cursorRunEvent: event })
        },
      })
    } catch (error) {
      await this.service.request(`/api/executions/${encodeURIComponent(execution.id)}/cursor/complete`, {
        method: 'POST',
        body: JSON.stringify({ status: 'failed', error: describeCoreFailure(error) }),
      }).catch(() => {})
      throw error
    }
    this.cursorRun = cursorRun
    this.cursorHubExecutionId = execution.id
    this.updateAgentBusy()
    this.post({ type: 'cursorRunStarted', profileId: launch.profile?.id || execution.projectAgentId, executionId: execution.id })
    this.postState(true)

    void (async () => {
      let result
      let status = 'failed'
      let failure = ''
      try {
        result = await cursorRun.done
        const rawStatus = String(result?.status || '').toLowerCase()
        status = rawStatus === 'cancelled' || rawStatus === 'canceled'
          ? 'cancelled'
          : rawStatus === 'error' || rawStatus === 'failed' ? 'failed' : 'completed'
        if (status === 'failed') failure = String(result?.result || result?.error || 'Cursor Agent завершился с ошибкой')
      } catch (error) {
        failure = describeCoreFailure(error)
      }
      let resultText = ''
      if (typeof result?.result === 'string') resultText = result.result
      else if (typeof result?.summary === 'string') resultText = result.summary
      else if (result) {
        try { resultText = JSON.stringify(result) } catch { resultText = String(result) }
      }
      resultText = resultText.slice(0, 64 * 1024)
      failure = failure.slice(0, 8 * 1024)
      try {
        await this.service.request(`/api/executions/${encodeURIComponent(execution.id)}/cursor/complete`, {
          method: 'POST', timeoutMs: 60_000,
          body: JSON.stringify({ status, result: resultText, error: failure }),
        })
        this.post({ type: 'cursorRunFinished', status, result, executionId: execution.id })
      } catch (error) {
        this.post({ type: 'cursorRunFinished', status: 'error', error: describeCoreFailure(error), executionId: execution.id })
        this.notify(error)
      } finally {
        if (this.cursorRun === cursorRun) this.cursorRun = undefined
        if (this.cursorHubExecutionId === execution.id) this.cursorHubExecutionId = ''
        this.updateAgentBusy()
        await this.refreshCursorRuntime().catch(() => this.cursorRuntimeState)
        await this.refresh().catch(error => this.notify(error))
        this.scheduleFlowCoordinatorIfNeeded()
        this.postState(true)
      }
    })()
    return { external: true, executionId: execution.id }
  }

  async launchPendingHubExecutions(questId = '', flowRunId = '') {
    if (this.pendingExecutionLaunchInFlight) return undefined
    this.pendingExecutionLaunchInFlight = true
    try {
      const pending = (this.boot?.executions || []).filter(item =>
        (item.status === 'pending' || item.status === 'interrupted')
        && (!questId || item.questId === questId)
        && (!flowRunId || item.flowRunId === flowRunId)
      )
      let firstRun
      for (const execution of pending) {
        try {
          const run = await this.launchHubExecution(execution.id)
          if (!firstRun && run?.id) firstRun = run
        } catch (error) {
          this.service.hostLog('warn', `[hub] execution ${execution.id} remains pending: ${error instanceof Error ? error.message : String(error)}`)
        }
      }
      if (pending.length) {
        await this.refreshRuntimeState()
      }
      if (firstRun?.id) await this.loadRun(firstRun.id, false)
      this.scheduleFlowCoordinatorIfNeeded()
      return firstRun
    } finally {
      this.pendingExecutionLaunchInFlight = false
    }
  }

  activeFlowRunIds(boot = this.boot) {
    return new Set((boot?.flowRuns || [])
      .filter(item => item.status === 'running' || item.status === 'waiting_approval' || item.status === 'waiting')
      .map(item => item.id))
  }

  hasRunningFlowExecution(boot = this.boot) {
    const active = this.activeFlowRunIds(boot)
    return (boot?.executions || []).some(item => active.has(item.flowRunId) && item.status === 'running')
  }

  async coordinateActiveFlows(refreshState = true) {
    if (this.flowCoordinatorInFlight || this.pendingExecutionLaunchInFlight || this.service.state !== 'running') return undefined
    this.flowCoordinatorInFlight = true
    this.pendingExecutionLaunchInFlight = true
    try {
      if (refreshState || !this.boot) await this.refreshRuntimeState()
      const active = this.activeFlowRunIds()
      if (!active.size) {
        this.stopFlowCoordinator(false)
        return undefined
      }
      const pending = (this.boot?.executions || []).filter(item =>
        active.has(item.flowRunId) && (item.status === 'pending' || item.status === 'interrupted')
      )
      let firstRun
      for (const execution of pending) {
        try {
          const run = await this.launchHubExecution(execution.id)
          if (!firstRun && run?.id) firstRun = run
        } catch (error) {
          this.service.hostLog('warn', `[flow] execution ${execution.id} remains pending: ${error instanceof Error ? error.message : String(error)}`)
        }
      }
      if (pending.length) await this.refreshRuntimeState()
      const activeAfter = this.activeFlowRunIds()
      const running = (this.boot?.executions || []).find(item =>
        activeAfter.has(item.flowRunId) && item.status === 'running' && item.runId
      )
      const currentStatus = this.details?.run?.status
      if (firstRun?.id) {
        await this.loadRun(firstRun.id, false)
      } else if (running?.runId && (!this.activeRunId || !['running', 'waiting_approval'].includes(currentStatus))) {
        await this.loadRun(running.runId, false)
      }
      return firstRun
    } finally {
      this.pendingExecutionLaunchInFlight = false
      this.flowCoordinatorInFlight = false
      this.scheduleFlowCoordinatorIfNeeded()
    }
  }

  scheduleFlowCoordinatorIfNeeded() {
    if (!this.hasRunningFlowExecution() || this.flowCoordinatorTimer || this.flowCoordinatorInFlight) return
    this.flowCoordinatorTimer = setTimeout(async () => {
      this.flowCoordinatorTimer = undefined
      try {
        await this.coordinateActiveFlows(true)
      } catch (error) {
        this.service.hostLog('warn', `[flow] background coordination failed: ${error instanceof Error ? error.message : String(error)}`)
      }
    }, this.hubVisible() ? 2000 : 3000)
  }

  scheduleAutoStart() {
    if (this.autoStartPromise || this.autoStartTimer || this.service.state === 'running') return
    this.autoStartTimer = setTimeout(() => {
      this.autoStartTimer = undefined
      this.autoStartPromise = (async () => {
        await this.service.ensureStarted()
        await this.refresh()
      })().catch(error => this.notify(error)).finally(() => { this.autoStartPromise = undefined })
    }, 120)
  }

  async loadRun(id, post = true, asDelta = false) {
    if (this.activeRunId !== id) {
      this.companionPollTick = 0
      this.companionInterventionSignature = ''
    }
    this.details = await this.service.request(`/api/runs/${encodeURIComponent(id)}`)
    this.activeRunId = id
    this.updateAgentBusy()
    if (post) {
      if (asDelta) this.postRunDelta()
      else this.postState()
    }
    if (['running', 'waiting_approval'].includes(this.details.run.status)) this.startPolling()
  }

  async deleteBlueprint(id) {
    const blueprint = this.boot?.blueprints?.find(item => item.id === id)
    if (!blueprint) throw new Error('Класс не найден.')
    const answer = await vscode.window.showWarningMessage(
      `Убрать класс «${blueprint.name}» из списка найма? Уже нанятые персонажи не изменятся.`,
      { modal: true },
      'Убрать',
    )
    if (answer !== 'Убрать') return
    await this.service.request(`/api/blueprints/${encodeURIComponent(id)}`, { method: 'DELETE' })
    this.removeBootItem('blueprints', id)
    this.postState()
  }

  // Квест удаляется там же, где виден, — в списке квестов проекта. Ядро не
  // отдаст квест, за которым стоит незакрытая работа, и скажет, что именно его
  // держит; здесь остаётся только спросить человека и убрать карточку.
  async deleteQuest(id) {
    const quest = this.boot?.quests?.find(item => item.id === id)
    if (!quest) throw new Error('Квест не найден в проекте.')
    const answer = await vscode.window.showWarningMessage(
      `Удалить квест «${quest.title || 'без названия'}»? Хроника его запусков останется в проекте.`,
      { modal: true },
      'Удалить',
    )
    if (answer !== 'Удалить') return
    await this.service.request(`/api/quests/${encodeURIComponent(id)}`, { method: 'DELETE' })
    this.removeBootItem('quests', id)
    this.post({ type: 'questDeleted', questId: id })
    this.postState()
  }

  // Отряд держит своих участников: пока он есть, персонажа не распустить. Ядро
  // не отдаст отряд, за которым стоит незакрытый квест, и назовёт этот квест.
  async deleteTeam(id) {
    const team = this.boot?.teams?.find(item => item.id === id)
    if (!team) throw new Error('Отряд не найден в проекте.')
    const answer = await vscode.window.showWarningMessage(
      `Распустить отряд «${team.name}»? Персонажи останутся в ростере, хроника его квестов сохранится.`,
      { modal: true },
      'Распустить',
    )
    if (answer !== 'Распустить') return
    await this.service.request(`/api/teams/${encodeURIComponent(id)}`, { method: 'DELETE' })
    this.removeBootItem('teams', id)
    this.post({ type: 'teamDeleted', teamId: id })
    this.postState()
  }

  // Схема живёт дольше своего квеста и держит исполнителей по идентификаторам
  // узлов. Пока её нельзя было удалить, роспуск персонажа отказывал «замените
  // его в схеме» — а редактор схем скрыт, и заменять было негде.
  async deleteWorkOrderV2(id) {
    const answer = await vscode.window.showWarningMessage(
      'Убрать карточку запуска? Договор и его версии удалятся; хроника уже выполненной работы останется.',
      { modal: true },
      'Убрать',
    )
    if (answer !== 'Убрать') return
    await this.service.request(`/api/v2/work-orders/${encodeURIComponent(id)}`, { method: 'DELETE' })
    this.post({ type: 'masterWorkOrderDeleted', workOrderId: id })
    this.postState()
  }

  async deleteFlow(id) {
    const flow = this.boot?.flows?.find(item => item.id === id)
    if (!flow) throw new Error('Схема не найдена в проекте.')
    const answer = await vscode.window.showWarningMessage(
      `Удалить схему «${flow.name}»? Хроника её запусков останется, персонажи — в ростере.`,
      { modal: true },
      'Удалить',
    )
    if (answer !== 'Удалить') return
    await this.service.request(`/api/flows/${encodeURIComponent(id)}`, { method: 'DELETE' })
    this.removeBootItem('flows', id)
    this.postState()
  }

  async deleteProjectAgent(id) {
    const agent = this.boot?.projectAgents?.find(item => item.id === id)
    if (!agent) throw new Error('Персонаж не найден в ростере.')
    const answer = await vscode.window.showWarningMessage(
      `Распустить «${agent.name}»? Хроника его квестов останется.`,
      { modal: true },
      'Распустить',
    )
    if (answer !== 'Распустить') return
    await this.service.request(`/api/project-agents/${encodeURIComponent(id)}`, { method: 'DELETE' })
    this.removeBootItem('projectAgents', id)
    this.post({ type: 'projectAgentDeleted', agentId: id })
    this.postState()
  }

  async deleteProfile(id) {
    if (!id || id === 'default') throw new Error('Профиль по умолчанию нельзя удалить.')
    const profile = this.boot?.profiles?.find(item => item.id === id)
    if (!profile) throw new Error('Профиль агента не найден.')
    const answer = await vscode.window.showWarningMessage(
      `Удалить профиль «${profile.name}»? История запусков сохранится.`,
      { modal: true },
      'Удалить',
    )
    if (answer !== 'Удалить') return
    await this.service.request(`/api/profiles/${encodeURIComponent(id)}`, { method: 'DELETE' })
    this.removeBootItem('profiles', id)
    const nextId = this.boot.profiles?.[0]?.id || ''
    this.post({ type: 'profileDeleted', profileId: nextId })
    this.postState()
  }

  async exportProfile(profile) {
    if (!profile?.id) throw new Error('Сначала сохраните профиль.')
    const baseUri = this.workspaceFolder()?.uri || this.context.globalStorageUri
    const uri = await vscode.window.showSaveDialog({
      title: 'Экспорт класса Point',
      defaultUri: vscode.Uri.joinPath(baseUri, `${safeFileName(profile.name)}.agent.json`),
      filters: { 'Класс Point': ['json'] },
    })
    if (!uri) return
    const payload = {
      format: 'local-agent-profile',
      version: 1,
      exportedAt: new Date().toISOString(),
      profile: portableProfile(profile),
    }
    await vscode.workspace.fs.writeFile(uri, Buffer.from(`${JSON.stringify(payload, null, 2)}\n`, 'utf8'))
    void vscode.window.showInformationMessage(`Класс «${profile.name}» экспортирован.`)
  }

  async importProfile() {
    const selected = await vscode.window.showOpenDialog({
      title: 'Импорт класса Point',
      canSelectMany: false,
      canSelectFiles: true,
      canSelectFolders: false,
      filters: { 'Класс Point': ['json'] },
    })
    if (!selected?.[0]) return
    const bytes = await vscode.workspace.fs.readFile(selected[0])
    if (bytes.byteLength > 256 * 1024) throw new Error('Файл класса превышает 256 КиБ.')
    let decoded
    try { decoded = JSON.parse(Buffer.from(bytes).toString('utf8')) } catch { throw new Error('Файл не является корректным JSON-описанием класса.') }
    const source = decoded?.format === 'local-agent-profile' ? decoded.profile : decoded
    const fallback = this.boot?.profiles?.[0]
    const profile = importedProfile(source, fallback)
    const saved = await this.service.request('/api/profiles', { method: 'POST', body: JSON.stringify(profile) })
    this.upsertBootItem('profiles', saved)
    this.focusTab('settings')
    this.post({ type: 'profileSaved', profileId: saved.id })
    this.postState()
    void vscode.window.showInformationMessage(`Класс «${saved.name}» импортирован.`)
  }

  async exportCustomTool(tool) {
    if (!tool?.id) throw new Error('Сначала сохраните инструмент.')
    const baseUri = this.workspaceFolder()?.uri || this.context.globalStorageUri
    const uri = await vscode.window.showSaveDialog({
      title: 'Экспорт инструмента Point',
      defaultUri: vscode.Uri.joinPath(baseUri, `${safeFileName(tool.displayName)}.tool.json`),
      filters: { 'Инструмент Point': ['json'] },
    })
    if (!uri) return
    const payload = { format: 'local-agent-tool', version: 2, exportedAt: new Date().toISOString(), tool: portableCustomTool(tool) }
    await vscode.workspace.fs.writeFile(uri, Buffer.from(`${JSON.stringify(payload, null, 2)}\n`, 'utf8'))
    void vscode.window.showInformationMessage(`Инструмент «${tool.displayName}» экспортирован.`)
  }

  async importCustomTool() {
    const selected = await vscode.window.showOpenDialog({
      title: 'Импорт инструмента Point', canSelectMany: false, canSelectFiles: true, canSelectFolders: false,
      filters: { 'Инструмент Point': ['json'] },
    })
    if (!selected?.[0]) return
    const bytes = await vscode.workspace.fs.readFile(selected[0])
    if (bytes.byteLength > 256 * 1024) throw new Error('Файл инструмента превышает 256 КиБ.')
    let decoded
    try { decoded = JSON.parse(Buffer.from(bytes).toString('utf8')) } catch { throw new Error('Файл не является корректным JSON-инструментом.') }
    const source = decoded?.format === 'local-agent-tool' ? decoded.tool : decoded
    const saved = await this.service.request('/api/custom-tools', { method: 'POST', body: JSON.stringify(importedCustomTool(source)) })
    this.syncCustomTool(saved)
    this.focusTab('tools')
    this.post({ type: 'customToolSaved', toolId: saved.id })
    this.postState()
    void vscode.window.showInformationMessage(`Инструмент «${saved.displayName}» импортирован.`)
  }

  async attachFiles() {
    const folder = this.workspaceFolder()
    if (!folder || folder.uri.scheme !== 'file') throw new Error('Сначала откройте локальную папку проекта.')
    const selected = await vscode.window.showOpenDialog({
      title: 'Прикрепить файлы к задаче агента',
      defaultUri: folder.uri,
      canSelectMany: true,
      canSelectFiles: true,
      canSelectFolders: false,
    })
    if (!selected?.length) return
    const items = selected.map(uri => {
      const relative = workspaceRelativePath(folder.uri.fsPath, uri)
      return { kind: 'workspace_file', label: relative, path: relative }
    })
	await this.service.request('/api/context/preview', { method: 'POST', body: JSON.stringify({ contextItems: items }) })
    this.post({ type: 'contextAdded', items })
  }

  async attachSelection() {
    const editor = vscode.window.activeTextEditor
    if (!editor || editor.selection.isEmpty) throw new Error('Сначала выделите текст в редакторе.')
    const content = editor.document.getText(editor.selection)
    const folder = this.workspaceFolder()
    let source = editor.document.fileName || 'редактор'
    if (folder && editor.document.uri.scheme === 'file') {
      try { source = workspaceRelativePath(folder.uri.fsPath, editor.document.uri) } catch { source = path.basename(source) }
    }
    const line = editor.selection.start.line + 1
    const items = [{ kind: 'text', label: `Выделение: ${source}:${line}`, content }]
    await this.service.request('/api/context/preview', { method: 'POST', body: JSON.stringify({ contextItems: items }) })
    this.post({ type: 'contextAdded', items })
  }

  async addRunContextFiles(runId) {
    const folder = this.workspaceFolder()
    if (!folder || folder.uri.scheme !== 'file') throw new Error('Сначала откройте локальную папку проекта.')
    const selected = await vscode.window.showOpenDialog({
      title: 'Добавить файлы в контекст работающего помощника',
      defaultUri: folder.uri,
      canSelectMany: true,
      canSelectFiles: true,
      canSelectFolders: false,
    })
    if (!selected?.length) return
    const items = selected.map(uri => {
      const relative = workspaceRelativePath(folder.uri.fsPath, uri)
      return { kind: 'workspace_file', label: relative, path: relative, source: `IDE · ${relative}` }
    })
    await this.queueRunContext(runId, items)
  }

  async addRunContextSelection(runId) {
    const editor = vscode.window.activeTextEditor
    if (!editor || editor.selection.isEmpty) throw new Error('Сначала выделите нужный фрагмент кода в редакторе.')
    const content = editor.document.getText(editor.selection)
    const folder = this.workspaceFolder()
    let source = editor.document.fileName || 'редактор'
    if (folder && editor.document.uri.scheme === 'file') {
      try { source = workspaceRelativePath(folder.uri.fsPath, editor.document.uri) } catch { source = path.basename(source) }
    }
    const startLine = editor.selection.start.line + 1
    const endLine = editor.selection.end.line + 1
    const lineRange = startLine === endLine ? `${startLine}` : `${startLine}–${endLine}`
    const items = [{
      kind: 'text',
      label: `Фрагмент: ${source}:${lineRange}`,
      content,
      source: `IDE · ${source}:${lineRange}`,
    }]
    await this.queueRunContext(runId, items)
  }

  async queueRunContext(runId, items) {
    const preview = await this.service.request(`/api/runs/${encodeURIComponent(String(runId || ''))}/context-add`, {
      method: 'POST',
      body: JSON.stringify({ contextItems: items }),
    })
    this.post({ type: 'runContextQueued', runId, preview })
    const inspector = await this.service.request(`/api/runs/${encodeURIComponent(String(runId || ''))}/context-inspector`)
    this.post({ type: 'contextInspector', runId, inspector })
  }

  async deleteCustomTool(id) {
    const tool = this.boot?.customTools?.find(item => item.id === id)
    if (!tool) throw new Error('Пользовательский инструмент не найден.')
    const answer = await vscode.window.showWarningMessage(
      `Удалить инструмент «${tool.displayName}»?`,
      { modal: true },
      'Удалить',
    )
    if (answer !== 'Удалить') return
    await this.service.request(`/api/custom-tools/${encodeURIComponent(id)}`, { method: 'DELETE' })
    this.removeCustomToolFromBoot(id)
    this.post({ type: 'customToolDeleted', toolId: this.boot.customTools?.[0]?.id || '' })
    this.postState()
  }

  async deleteWorkflow(id) {
    const workflow = this.boot?.workflows?.find(item => item.id === id)
    if (!workflow) throw new Error('Сценарий не найден.')
    const answer = await vscode.window.showWarningMessage(
      `Удалить сценарий «${workflow.name}»? История запусков и её снимки сохранятся.`,
      { modal: true },
      'Удалить',
    )
    if (answer !== 'Удалить') return
    await this.service.request(`/api/workflows/${encodeURIComponent(id)}`, { method: 'DELETE' })
    this.removeBootItem('workflows', id)
    this.post({ type: 'workflowDeleted', workflowId: this.boot.workflows?.[0]?.id || '' })
    this.postState()
  }

  startPolling() {
    if (!this.shouldPollRun()) {
      this.stopRunPolling(false)
      return
    }
    if (this.pollTimer) return
    if (this.pollInFlight) return
    const tick = async () => {
      this.pollTimer = undefined
      if (!this.shouldPollRun()) return
      this.pollInFlight = true
      let keepPolling = false
      try {
        if (!this.activeRunId || this.service.state !== 'running') return
        const visible = this.hubVisible()
        await this.loadRun(this.activeRunId, visible, true)
        keepPolling = this.shouldPollRun()
        if (keepPolling) {
          this.companionPollTick += 1
          if (visible && this.companionPollTick % 4 === 0) await this.refreshLiveCompanionInterventions()
        }
        if (!keepPolling) {
          await this.refreshRuntimeState({ companion: true })
          await this.coordinateActiveFlows(false)
          keepPolling = this.shouldPollRun()
          if (visible) this.postState()
        }
      } catch (error) { this.notify(error) }
      finally {
        this.pollInFlight = false
        if (keepPolling && !this.pollTimer) this.pollTimer = setTimeout(tick, this.hubVisible() ? 1500 : 2500)
      }
    }
    this.pollTimer = setTimeout(tick, 600)
  }

  shouldPollRun() {
    const status = this.details?.run?.status
    return Boolean(this.activeRunId) && (status === 'running' || (this.hubVisible() && status === 'waiting_approval'))
  }

  async loadWorkflowRun(id, post = true, asDelta = false) {
    this.workflowDetails = await this.service.request(`/api/workflow-runs/${encodeURIComponent(id)}`)
    this.activeWorkflowRunId = id
    void this.maybeRunCursorWorkflowStep()
    this.updateAgentBusy()
    if (post) {
      if (asDelta) this.postRunDelta()
      else this.postState()
    }
    if (['running', 'waiting_approval'].includes(this.workflowDetails.status)) this.startWorkflowPolling()
  }

  async maybeRunCursorWorkflowStep() {
    const run = this.workflowDetails
    if (this.cursorRun || this.cursorWorkflowStepBusy || !run || !['running', 'waiting_approval'].includes(run.status)) return
    const step = (run.stepRuns || []).find(item => item.status === 'waiting_approval' && item.kind === 'cursor')
    if (!step?.stepId) return
    const definition = (run.snapshot?.workflow?.steps || []).find(item => item.id === step.stepId)
    const profile = this.boot?.profiles?.find(item => item.id === (step.profileId || definition?.profileId))
    if (!profile || profile.provider !== 'cursor-cli') return
    this.cursorWorkflowStepBusy = true
    let heartbeat
    let claimToken = ''
    try {
      const claimed = await this.service.request(`/api/workflow-runs/${encodeURIComponent(run.id)}/steps/${encodeURIComponent(step.stepId)}/claim`, { method: 'POST', body: '{}' })
      claimToken = claimed?.claimToken || ''
      if (!claimToken) throw new Error('Cursor step claim token was not issued.')
      const cwd = this.workspaceFolder()?.uri?.fsPath
      const runtime = await this.refreshCursorRuntime()
      if (!cwd || !runtime.available || !runtime.authenticated) throw new Error(runtime.error || 'Cursor Agent недоступен для этапа.')
      const task = [definition?.instruction, run.task].filter(Boolean).join('\n\n') || run.task
      heartbeat = setInterval(() => {
        void this.service.request(`/api/workflow-runs/${encodeURIComponent(run.id)}/steps/${encodeURIComponent(step.stepId)}/heartbeat`, {
          method: 'POST',
          body: JSON.stringify({ claimToken }),
        }).catch(() => {})
      }, 15_000)
      this.cursorRun = cursorRuntime.startRun({
        profile,
        cwd,
        task,
        onEvent: event => this.post({ type: 'cursorRunEvent', event, workflowRunId: run.id, stepId: step.stepId }),
      })
      this.updateAgentBusy()
      const result = await this.cursorRun.done
      clearInterval(heartbeat)
      heartbeat = undefined
      await this.service.request(`/api/workflow-runs/${encodeURIComponent(run.id)}/steps/${encodeURIComponent(step.stepId)}/complete`, {
        method: 'POST',
        body: JSON.stringify({
          claimToken,
          status: result?.status === 'error' || result?.status === 'cancelled' ? 'failed' : 'completed',
          result: result?.result || result?.summary || '',
          error: result?.status === 'error' ? (result?.result || 'Cursor run failed') : '',
        }),
      })
      await this.loadWorkflowRun(run.id)
    } catch (error) {
      if (claimToken) {
        await this.service.request(`/api/workflow-runs/${encodeURIComponent(run.id)}/steps/${encodeURIComponent(step.stepId)}/complete`, {
          method: 'POST',
          body: JSON.stringify({ claimToken, status: 'failed', error: describeCoreFailure(error) }),
        }).catch(() => {})
      }
      this.notify(error)
    } finally {
      if (heartbeat) clearInterval(heartbeat)
      this.cursorRun = undefined
      this.cursorWorkflowStepBusy = false
      this.updateAgentBusy()
    }
  }

  startWorkflowPolling() {
    if (!this.hubVisible()) {
      this.stopPollingTimers(false)
      return
    }
    if (this.workflowPollTimer) return
    if (this.workflowPollInFlight) {
      this.workflowPollTimer = setTimeout(() => {
        this.workflowPollTimer = undefined
        this.startWorkflowPolling()
      }, 150)
      return
    }
    const tick = async () => {
      this.workflowPollTimer = undefined
      if (!this.hubVisible()) return
      this.workflowPollInFlight = true
      let keepPolling = false
      try {
        if (!this.activeWorkflowRunId || this.service.state !== 'running') return
        await this.loadWorkflowRun(this.activeWorkflowRunId, true, true)
        keepPolling = ['running', 'waiting_approval'].includes(this.workflowDetails?.status)
        if (!keepPolling) {
          await this.refreshRuntimeState()
          this.postState()
        }
      } catch (error) { this.notify(error) }
      finally {
        this.workflowPollInFlight = false
        if (keepPolling && this.hubVisible() && !this.workflowPollTimer) this.workflowPollTimer = setTimeout(tick, 1500)
      }
    }
    this.workflowPollTimer = setTimeout(tick, 600)
  }

  hubVisible() {
    return Boolean(
      (this.panel && this.panel.visible)
      || (this.view && this.view.visible)
      || (this.companionSidebar && this.companionSidebar.visible)
      || (this.companionPopup && this.companionPopup.visible)
      || (this.connectionsPanel && this.connectionsPanel.visible)
      || (this.statisticsPanel && this.statisticsPanel.visible)
      || (this.dockerPanel && this.dockerPanel.visible)
      || [...this.toolWindows.values()].some(view => view && view.visible),
    )
  }

  stopPollingTimers(clearInFlight = true) {
    this.stopRunPolling(clearInFlight)
    this.stopWorkflowPolling(clearInFlight)
    this.stopFlowCoordinator(clearInFlight)
  }

  stopRunPolling(clearInFlight = true) {
    if (this.pollTimer) clearTimeout(this.pollTimer)
    this.pollTimer = undefined
    if (clearInFlight) this.pollInFlight = false
  }

  stopWorkflowPolling(clearInFlight = true) {
    if (this.workflowPollTimer) clearTimeout(this.workflowPollTimer)
    this.workflowPollTimer = undefined
    if (clearInFlight) this.workflowPollInFlight = false
  }

  stopFlowCoordinator(clearInFlight = true) {
    if (this.flowCoordinatorTimer) clearTimeout(this.flowCoordinatorTimer)
    this.flowCoordinatorTimer = undefined
    if (clearInFlight) {
      this.flowCoordinatorInFlight = false
      this.pendingExecutionLaunchInFlight = false
    }
  }

  resumePollingIfNeeded() {
    if (!this.hubVisible()) return
    if (this.activeRunId && ['running', 'waiting_approval'].includes(this.details?.run?.status)) this.startPolling()
    if (this.activeWorkflowRunId && ['running', 'waiting_approval'].includes(this.workflowDetails?.status)) this.startWorkflowPolling()
  }

  updateAgentBusy() {
    const active = status => ['running', 'waiting_approval'].includes(status)
    this.onAgentBusy(Boolean(this.cursorRun) || active(this.details?.run?.status) || active(this.workflowDetails?.status))
  }

  onHubVisibility(visible) {
    if (visible) {
      this.postState(true)
      if (vscode.workspace.isTrusted && vscode.workspace.getConfiguration('localAgent').get('autoStart', true)) {
        this.scheduleAutoStart()
      }
      this.resumePollingIfNeeded()
    } else {
      if (this.details?.run?.status === 'running') this.startPolling()
      else this.stopRunPolling(false)
      this.stopWorkflowPolling(false)
    }
  }

  onServiceStatus(value) {
    if (value?.state === 'running') this.updateAgentBusy()
    else this.onAgentBusy(false)
    this.postState()
  }

  notify(error, request = '') {
    const message = describeCoreFailure(error)
    if (!vscode.workspace.isTrusted) {
      this.postState()
      return
    }
    // `request` — имя запроса, на котором всё сломалось. Раздел, ушедший в
    // «загрузку», по нему узнаёт себя и перестаёт врать про загрузку.
    this.post({ type: 'error', message, request })
    void this.showCoreFailure(message)
  }

  async showCoreFailure(message) {
    const coreRelated = /ядро|хроник|порт|127\.0\.0\.1|companion|не найден|не отвеча|антивирус|брандмауэр|fetch failed/i.test(message)
    if (!coreRelated) {
      await vscode.window.showErrorMessage(`Point: ${message}`)
      return
    }
    const choice = await vscode.window.showErrorMessage(`Point: ${message}`, 'Хроника ядра', 'Перезапустить ядро')
    if (choice === 'Хроника ядра') {
      this.output.show(true)
      await showCoreChronicle(this.service, this.output)
      return
    }
    if (choice === 'Перезапустить ядро') {
      try {
        await this.service.stop()
        await this.service.start()
        await this.refresh()
      } catch (error) {
        await vscode.window.showErrorMessage(`Point: ${describeCoreFailure(error)}`)
      }
    }
  }

  postRunDelta() {
    if (!this.hubVisible()) return
    const message = { type: 'runDelta', details: this.details, workflowDetails: this.workflowDetails }
    if (this.view && this.view.visible !== false) void this.view.webview.postMessage(message)
    if (this.panel && this.panel.visible !== false) void this.panel.webview.postMessage(message)
  }

  postState(force = false) {
    if (!force && !this.hubVisible()) return
    const message = {
      type: 'state',
      service: { state: this.service.state, detail: this.service.lastDetail || '' },
      workspaceTrusted: vscode.workspace.isTrusted,
      workspace: this.workspaceFolder()?.name || '',
      // Путь нужен галерее, чтобы отметить активный мир в списке: имена папок
      // повторяются, и по одному имени активный мир не опознать.
      workspacePath: this.workspaceFolder()?.uri?.fsPath || '',
      boot: this.boot,
      details: this.details,
      workflowDetails: this.workflowDetails,
      selectedTab: this.selectedTab,
      onboarding: { complete: this.onboardingComplete },
      agentImprovementFocus: this.agentImprovementFocus,
      cursorRuntime: this.cursorRuntimeState,
      ideContext: this.ideContext,
      // Без них «Спасибо ✓» держалось до первой перерисовки.
      companionFeedback: companionFeedbackMarks(this.companionFeedbackRecords()),
      quickChat: {
        defaultProfileId: String(vscode.workspace.getConfiguration('localAgent').get('quickChatDefaultProfileId') || ''),
      },
    }
    // Cheap signature: avoid JSON.stringify of full bootstrap (catalogs, trees, history).
    const signature = cheapStateSignature(message)
    if (this.view && (force || this.view.visible !== false) && (force || signature !== this.viewStateSignature)) {
      this.viewStateSignature = signature
      void this.view.webview.postMessage(message)
    }
    if (this.panel && (force || this.panel.visible !== false) && (force || signature !== this.panelStateSignature)) {
      this.panelStateSignature = signature
      void this.panel.webview.postMessage(message)
    }
    if (this.companionPopup && (force || this.companionPopup.visible !== false) && (force || signature !== this.companionPopupStateSignature)) {
      this.companionPopupStateSignature = signature
      void this.companionPopup.webview.postMessage(message)
    }
    if (this.companionSidebar && (force || this.companionSidebar.visible !== false) && (force || signature !== this.companionSidebarStateSignature)) {
      this.companionSidebarStateSignature = signature
      void this.companionSidebar.webview.postMessage(message)
    }
    if (this.connectionsPanel && (force || this.connectionsPanel.visible !== false) && (force || signature !== this.connectionsStateSignature)) {
      this.connectionsStateSignature = signature
      void this.connectionsPanel.webview.postMessage({ ...message, selectedTab: 'connections' })
    }
    if (this.statisticsPanel && (force || this.statisticsPanel.visible !== false) && (force || signature !== this.statisticsStateSignature)) {
      this.statisticsStateSignature = signature
      void this.statisticsPanel.webview.postMessage({ ...message, selectedTab: 'statistics' })
    }
    if (this.dockerPanel && (force || this.dockerPanel.visible !== false) && (force || signature !== this.dockerStateSignature)) {
      this.dockerStateSignature = signature
      void this.dockerPanel.webview.postMessage({ ...message, selectedTab: 'docker' })
    }
    for (const [kind, view] of this.toolWindows) {
      if (!view || (!force && view.visible === false)) continue
      if (!force && this.toolWindowStateSignatures.get(kind) === signature) continue
      this.toolWindowStateSignatures.set(kind, signature)
      void view.webview.postMessage({ ...message, selectedTab: `tool-${kind}` })
    }
    this.onCompanionState(this.boot)
  }

  companionWebviewTraffic(type) {
    return [
      'focusCompanion',
      'companionChatStarted',
      'companionChatStopped',
      'companionChatResult',
      'companionChatError',
      'companionChatProgress',
      'companionChatDelta',
      'companionThreadSync',
      'companionSetupTestResult',
      'companionSetupTestError',
      'companionHistoryCleared',
      'companionThreadCreated',
      'companionIdeContext',
      'companionActionApplied',
      'companionInterventions',
    ].includes(type)
  }

  beginCompanionThread(message, requestId) {
    const text = String(message || '').trim()
    const messages = [...(this.companionThreadCache?.messages || [])]
    const last = messages[messages.length - 1]
    if (text && !(last?.role === 'user' && last.content === text)) messages.push({ role: 'user', content: text })
    this.companionThreadCache = {
      ...(this.companionThreadCache || {}),
      messages: messages.slice(-80),
      streamReply: '',
      loading: true,
      requestId: Number(requestId || 0),
      updatedAt: Date.now(),
    }
  }

  updateCompanionThreadStream(reply, requestId) {
    if (Number(requestId || 0) !== Number(this.companionThreadCache?.requestId || 0)) return
    this.companionThreadCache = {
      ...this.companionThreadCache,
      streamReply: String(reply || ''),
      loading: true,
      updatedAt: Date.now(),
    }
  }

  finishCompanionThread(response, requestId) {
    if (Number(requestId || 0) !== Number(this.companionThreadCache?.requestId || 0)) return
    const reply = String(response?.reply || response?.content || this.companionThreadCache?.streamReply || '').trim()
      || 'Компаньон ответил без текста. Спросите ещё раз или проверьте подключение модели.'
    const messages = [...(this.companionThreadCache?.messages || [])]
    const last = messages[messages.length - 1]
    if (!(last?.role === 'assistant' && last.content === reply)) {
      messages.push({ ...response, role: 'assistant', content: reply })
    }
    this.companionThreadCache = {
      ...this.companionThreadCache,
      messages: messages.slice(-80),
      streamReply: '',
      loading: false,
      requestId: 0,
      updatedAt: Date.now(),
    }
  }

  failCompanionThread(message, requestId) {
    if (Number(requestId || 0) !== Number(this.companionThreadCache?.requestId || 0)) return
    const text = String(message || 'Компаньон не смог ответить.')
    const messages = [...(this.companionThreadCache?.messages || [])]
    const last = messages[messages.length - 1]
    if (!(last?.role === 'assistant' && last.content === text)) {
      messages.push({ role: 'assistant', content: text, level: 'warning', mode: 'error' })
    }
    this.companionThreadCache = {
      ...this.companionThreadCache,
      messages: messages.slice(-80),
      streamReply: '',
      loading: false,
      requestId: 0,
      updatedAt: Date.now(),
    }
  }

  finishCompanionThreadStopped(requestId, superseded = false) {
    if (requestId && Number(requestId) !== Number(this.companionThreadCache?.requestId || 0)) return
    const partial = String(this.companionThreadCache?.streamReply || '').trim()
    const text = partial
      ? `${partial}\n\n— ${superseded ? 'остановлено новым сообщением' : 'остановлено'}. Можно сразу спросить снова.`
      : superseded
        ? 'Предыдущий запрос остановлен новым сообщением.'
        : 'Запрос остановлен. Можно сразу спросить снова.'
    const messages = [...(this.companionThreadCache?.messages || [])]
    const last = messages[messages.length - 1]
    if (!(last?.role === 'assistant' && last.mode === 'cancelled')) {
      messages.push({ role: 'assistant', content: text, level: 'warning', mode: 'cancelled' })
    }
    this.companionThreadCache = {
      ...this.companionThreadCache,
      messages: messages.slice(-80),
      streamReply: '',
      loading: false,
      requestId: 0,
      updatedAt: Date.now(),
    }
  }

  rememberCompanionThread(payload = {}) {
    const incomingRequestId = Number(payload.requestId || 0)
    const activeRequestId = Number(this.companionActiveChatRequestId || 0)
    const stale = Boolean(activeRequestId && incomingRequestId && incomingRequestId !== activeRequestId)
    this.companionThreadCache = {
      messages: !stale && Array.isArray(payload.messages) ? payload.messages.slice(-80) : (this.companionThreadCache?.messages || []),
      draft: !stale && typeof payload.draft === 'string' ? payload.draft : (this.companionThreadCache?.draft || ''),
      streamReply: !stale && typeof payload.streamReply === 'string' ? payload.streamReply : (this.companionThreadCache?.streamReply || ''),
      loading: activeRequestId ? true : Boolean(payload.loading),
      pendingSend: typeof payload.pendingSend === 'string' ? payload.pendingSend : (this.companionThreadCache?.pendingSend || ''),
      requestId: activeRequestId || incomingRequestId,
      updatedAt: Date.now(),
    }
  }

  pushCompanionThreadSync(target) {
    const cache = this.companionThreadCache
    if (!cache?.messages?.length && !cache?.streamReply && !cache?.draft && !cache?.loading) return
    const message = {
      type: 'companionThreadSync',
      messages: cache.messages || [],
      draft: cache.draft || '',
      streamReply: cache.streamReply || '',
      loading: Boolean(this.companionActiveChatRequestId || cache.loading),
      pendingSend: cache.pendingSend || '',
      requestId: this.companionActiveChatRequestId || Number(cache.requestId || 0),
    }
    if (target === 'peek' && this.companionPopup) void this.companionPopup.webview.postMessage(message)
    else if (target === 'sidebar' && this.companionSidebar) void this.companionSidebar.webview.postMessage(message)
    else if (target === 'dock' && this.view) void this.view.webview.postMessage(message)
    else this.post(message)
  }

  preferLiveCompanionSurface(fallback = 'peek') {
    if (this.companionFocusTarget === 'dock' && this.view && this.view.visible !== false) return 'dock'
    if (this.companionFocusTarget === 'sidebar' && this.companionSidebar && this.companionSidebar.visible !== false) return 'sidebar'
    if (this.companionFocusTarget === 'peek' && this.companionPopup && this.companionPopup.visible !== false) return 'peek'
    if (this.companionPopup && this.companionPopup.visible !== false) return 'peek'
    if (this.companionSidebar && this.companionSidebar.visible !== false) return 'sidebar'
    if (this.view && this.view.visible !== false) return 'dock'
    if (fallback === 'dock' || fallback === 'sidebar') return fallback
    return 'peek'
  }

  queueCompanionFocus(payload = {}) {
    this.pendingCompanionFocus = {
      message: typeof payload.message === 'string' ? payload.message : '',
      send: Boolean(payload.send),
      surface: payload.surface === 'peek' ? 'peek' : payload.surface === 'dock' ? 'dock' : 'sidebar',
    }
    this.companionFocusTarget = this.pendingCompanionFocus.surface
    this.flushCompanionFocus()
  }

  flushCompanionFocus() {
    if (!this.pendingCompanionFocus) return
    const payload = this.pendingCompanionFocus
    const target = payload.surface || this.companionFocusTarget || 'dock'
    if (target === 'dock' && this.companionDockReady && this.view) {
      this.pendingCompanionFocus = undefined
      void this.view.webview.postMessage({ type: 'focusCompanion', message: payload.message, send: payload.send })
      return
    }
    if (target === 'peek' && this.companionPopupReady && this.companionPopup) {
      this.pendingCompanionFocus = undefined
      void this.companionPopup.webview.postMessage({ type: 'focusCompanion', message: payload.message, send: payload.send })
      return
    }
    if (target === 'sidebar' && this.companionSidebarReady && this.companionSidebar) {
      this.pendingCompanionFocus = undefined
      void this.companionSidebar.webview.postMessage({ type: 'focusCompanion', message: payload.message, send: payload.send })
      return
    }
  }

  post(message) {
    const companionTraffic = this.companionWebviewTraffic(message?.type)
    if (!this.hubVisible() && message?.type !== 'state' && !companionTraffic) return
    if (this.companionPopup && (this.companionPopup.visible !== false || companionTraffic)) void this.companionPopup.webview.postMessage(message)
    if (this.companionSidebar && (this.companionSidebar.visible !== false || companionTraffic)) void this.companionSidebar.webview.postMessage(message)
    if (this.view && (this.view.visible !== false || companionTraffic)) void this.view.webview.postMessage(message)
    if (this.panel && (this.panel.visible !== false || companionTraffic)) void this.panel.webview.postMessage(message)
    if (this.connectionsPanel && this.connectionsPanel.visible !== false && !companionTraffic) void this.connectionsPanel.webview.postMessage(message)
    if (this.statisticsPanel && this.statisticsPanel.visible !== false && !companionTraffic) void this.statisticsPanel.webview.postMessage(message)
    if (this.dockerPanel && this.dockerPanel.visible !== false && !companionTraffic) void this.dockerPanel.webview.postMessage(message)
    if (!companionTraffic) {
      for (const view of this.toolWindows.values()) {
        if (view && view.visible !== false) void view.webview.postMessage(message)
      }
    }
  }

  postCursorRuntime() {
    this.post({ type: 'cursorRuntime', ...this.cursorRuntimeState })
  }

  async refreshCursorRuntime() {
    this.cursorRuntimeState = await cursorRuntime.status()
    this.postCursorRuntime()
    return this.cursorRuntimeState
  }

  async show(tab = 'overview') {
    this.showWide(tab)
  }

  showWide(tab = 'overview') {
    if (!this.agentsWindowMode) {
      void this.openAgentsWindow(tab)
      return
    }
    this.showWideHere(tab)
  }

  normalizedAgentImprovementFocus(agentId, constructorStep = 'review') {
    const normalizedAgentId = String(agentId || '').trim().slice(0, 160)
    if (!normalizedAgentId) return undefined
    const allowedSteps = new Set(['identity', 'role', 'mission', 'rules', 'brain', 'skills', 'tools', 'memory', 'permissions', 'review'])
    const step = allowedSteps.has(constructorStep) ? constructorStep : 'review'
    this.agentImprovementFocusSequence += 1
    return {
      requestId: `agent-improvement-${Date.now()}-${this.agentImprovementFocusSequence}`,
      agentId: normalizedAgentId,
      constructorStep: step,
    }
  }

  focusAgentImprovement(agentId, constructorStep = 'review') {
    const focus = this.normalizedAgentImprovementFocus(agentId, constructorStep)
    if (!focus) return
    if (!this.agentsWindowMode) {
      void this.openAgentsWindow('agents', focus)
      return
    }
    this.agentImprovementFocus = focus
    this.showWideHere('agents')
  }

  async openAgentsWindow(tab = 'master', focus = undefined) {
    // Production uses a real Agents window. Moving an editor tab after a fixed
    // timeout was a race against the active editor: if focus changed during
    // those 100 ms, Point moved or focused the IDE editor instead of the Hub.
    // An auxiliary editor also belongs to the IDE window lifecycle, so it
    // cannot remain an independently usable Agent Hub. Keep that route only
    // as an explicit diagnostic fallback.
    if (process.env.POINT_AUXILIARY_HUB === '1') {
      this.agentsWindowMode = true
      this.auxiliaryHubMode = true
      if (focus?.agentId) this.agentImprovementFocus = this.normalizedAgentImprovementFocus(focus.agentId, focus.constructorStep)
      this.showWideHere(tab || 'master')
      await new Promise(resolve => setTimeout(resolve, 100))
      await vscode.commands.executeCommand('workbench.action.moveEditorToNewWindow')
      return
    }
    const folder = this.workspaceFolder()
    const payload = { tab: tab || 'master' }
    if (focus?.agentId) {
      payload.agentId = focus.agentId
      payload.constructorStep = focus.constructorStep
    }
    const initialQuery = `point-hub:${encodeURIComponent(JSON.stringify(payload))}`
    await vscode.commands.executeCommand('workbench.action.openAgentsWindow', {
      ...(folder ? { folderUri: folder.uri.toJSON() } : {}),
      initialQuery,
    })
  }

  // Каталог чатов всех миров. Живёт рядом с состоянием, а не внутри него:
  // приходит своим сообщением, потому что нужен раньше и чаще, чем `boot`.
  async postChatDirectory() {
    if (!this.hubPanelReady && !this.panel) return
    if (this.service.state !== 'running') return
    try {
      const directory = await this.service.request('/api/master/directory', { allowStart: false })
      this.lastChatDirectory = directory
      this.post({ type: 'chatDirectory', directory })
    } catch (error) {
      this.service.hostLog('warn', `[chat] каталог не собрался: ${error?.message || error}`)
      this.post({ type: 'chatDirectory', directory: { currentWorkspaceId: '', worlds: [] }, failed: true })
    }
  }

  // Ожидающий разговор отдаётся ровно один раз и только тому миру, ради
  // которого его запомнили: переключение могло уйти в сторону, и выбирать в
  // чужом мире чужой id — верный способ показать «разговор не найден».
  takePendingMasterConversation() {
    const pending = this.pendingMasterConversation
    if (!pending?.id && !pending?.create) return undefined
    const folder = this.workspaceFolder()
    if (!folder || normalizedWorkspaceRoot(folder.uri.fsPath) !== normalizedWorkspaceRoot(pending.path)) return undefined
    this.pendingMasterConversation = undefined
    return pending
  }

  // Уход из мира, где мастер ещё отвечает. Ядро не гасится — ход допишется в
  // общую базу, и реплику человек увидит, вернувшись. Но сказать об этом надо:
  // молча увести экран с идущего ответа значит соврать, что его не было.
  async confirmLeavingBusyWorld() {
    const busy = this.masterTurnStreams?.size > 0
    if (!busy) return true
    const folder = this.workspaceFolder()
    const answer = await vscode.window.showWarningMessage(
      `Мастер ещё отвечает в проекте «${folder?.name || 'текущем'}». Переключиться?`,
      { modal: true, detail: 'Ответ допишется и будет ждать вас в этом чате.' },
      'Переключиться',
    )
    return answer === 'Переключиться'
  }

  // Путь из вебвью — не путь, а ключ поиска. Сверяем с тем самым списком,
  // который вебвью и показали: пересобирать его на каждый клик нельзя (это до
  // сорока обращений к диску ровно в тот момент, скорость которого мы и меряем),
  // а брать на веру присланную строку — значит открыть произвольную папку по
  // сообщению из вебвью.
  knownProject(value) {
    const wanted = normalizedWorkspaceRoot(String(value || ''))
    if (!wanted) return undefined
    return (this.lastProjectList || []).find(item => normalizedWorkspaceRoot(item.path) === wanted)?.path
  }

  // Галерея миров доступна и с открытым проектом: это домашний экран Чертога, а
  // не только заглушка «проект не выбран».
  openProjectGallery() {
    this.post({ type: 'projectGallery', open: true })
    void this.postProjects?.()
  }

  enterAgentsWindow(tab = 'master', focus = undefined) {
    this.agentsWindowMode = true
    this.auxiliaryHubMode = false
    if (focus?.agentId) this.agentImprovementFocus = this.normalizedAgentImprovementFocus(focus.agentId, focus.constructorStep)
    void vscode.commands.executeCommand('setContext', 'point.agentsWindow', true)
    this.showWideHere(tab || 'master')
  }

  // Побочная навигация не выбрасывает из онбординга.
  //
  // Полтора десятка обработчиков ставят вкладку как следствие своего дела:
  // сохранил навык — «Навыки», запустил флоу — «Обзор», открыл прогон —
  // «Квесты». Пока человек в онбординге, любое такое следствие закрывало первый
  // запуск, и он терял место. Заметнее всего это в настройке Мастера, куда
  // заходят из Чертога уже после первого запуска: там вкладка «onboarding» не
  // закреплена, и её сносило любым состоянием — от нажатия «перестроить индекс»
  // до фонового опроса. Явный переход (selectTab с рейки, restartOnboarding,
  // команда «Открыть Point») идёт мимо сторожа: там человек сам сказал, куда.
  focusTab(tab) {
    if (this.selectedTab === 'onboarding' && tab !== 'onboarding') return
    this.selectedTab = tab
  }

  showWideHere(tab = 'master') {
    if (this.hubGarbageTimer) clearTimeout(this.hubGarbageTimer)
    this.hubGarbageTimer = undefined
    this.selectedTab = tab
    if (this.panel) {
      if (this.auxiliaryHubMode) void vscode.commands.executeCommand('point.focusAgentHubWindow')
      else this.panel.reveal(vscode.ViewColumn.One)
      this.postState()
      this.flushCompanionFocus()
      return
    }
    this.panel = vscode.window.createWebviewPanel(
      'point.agentHub',
      'Агенты Point',
      vscode.ViewColumn.One,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
      },
    )
    this.panelStateSignature = ''
    this.panel.iconPath = vscode.Uri.joinPath(this.context.extensionUri, 'media', 'agent.svg')
    this.panel.webview.html = this.html(this.panel.webview, 'wide')
    const panel = this.panel
    const panelListeners = [
      panel.webview.onDidReceiveMessage(message => this.handleMessage(message)),
      panel.onDidChangeViewState(event => { this.onHubVisibility(Boolean(event.webviewPanel.visible)) }),
    ]
    panel.onDidDispose(() => {
      for (const listener of panelListeners.splice(0)) listener.dispose()
      if (this.panel === panel) this.panel = undefined
      if (this.auxiliaryHubMode) {
        this.agentsWindowMode = false
        this.auxiliaryHubMode = false
      }
      this.panelStateSignature = ''
      this.hubPanelReady = false
      this.scheduleHubGarbageCollection()
      if (!this.hubVisible()) this.onHubVisibility(false)
    })
    this.postState()
  }

  showStatistics() {
    if (!this.agentsWindowMode) {
      void this.openAgentsWindow('statistics')
      return
    }
    this.showWideHere('statistics')
    this.post({ type: 'loadStatistics' })
  }

  scheduleHubGarbageCollection() {
    if (this.hubGarbageTimer) clearTimeout(this.hubGarbageTimer)
    this.hubGarbageTimer = setTimeout(() => {
      this.hubGarbageTimer = undefined
      if (this.panel) return
      this.collectWebviewGarbage()
      collectExtensionGarbage()
      void vscode.commands.executeCommand('point.collectHubGarbage').then(() => {}, () => {})
    }, 750)
  }

  collectWebviewGarbage() {
    const surfaces = [this.view, this.companionSidebar, this.companionPopup, this.connectionsPanel, this.statisticsPanel, this.dockerPanel, ...this.toolWindows.values()]
    for (const surface of surfaces) {
      if (!surface?.webview) continue
      void Promise.resolve(surface.webview.postMessage({ type: 'collectGarbage' })).catch(() => {})
    }
  }

  showDocker() {
    if (!this.agentsWindowMode) {
      void this.openAgentsWindow('docker')
      return
    }
    this.showWideHere('docker')
    this.post({ type: 'dockerNeedRefresh' })
  }

  showCompanionPeek() {
    this.companionFocusTarget = 'peek'
    if (this.companionPopup) {
      this.companionPopup.reveal(vscode.ViewColumn.Beside, true)
      this.flushCompanionFocus()
      this.pushCompanionThreadSync('peek')
      this.postState(true)
      return 'peek'
    }
    this.companionPopupReady = false
    this.companionPopup = vscode.window.createWebviewPanel(
      'point.companionPeek',
      'Компаньон',
      { viewColumn: vscode.ViewColumn.Beside, preserveFocus: true },
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
      },
    )
    this.companionPopupStateSignature = ''
    this.companionPopup.iconPath = vscode.Uri.joinPath(this.context.extensionUri, 'media', 'agent.svg')
    this.companionPopup.webview.html = this.html(this.companionPopup.webview, 'companion-peek')
    this.companionPopup.webview.onDidReceiveMessage(message => this.handleMessage(message), undefined, this.context.subscriptions)
    this.companionPopup.onDidChangeViewState(() => { this.onHubVisibility(this.hubVisible()) }, undefined, this.context.subscriptions)
    this.companionPopup.onDidDispose(() => {
      this.companionPopup = undefined
      this.companionPopupReady = false
      this.companionPopupStateSignature = ''
      if (!this.hubVisible()) this.onHubVisibility(false)
    }, undefined, this.context.subscriptions)
    this.postState(true)
    this.pushCompanionThreadSync('peek')
    return 'peek'
  }

  showCompanionPopup() {
    return this.showCompanionPeek()
  }

  // Подключения — своё окно, а не вкладка Хаба. Их заводят один раз и надолго,
  // а потом только выбирают в разговоре; место внутри Хаба означало бы, что за
  // сменой модели надо идти в агентов. Поверхность та же, что у остальных
  // страниц (`layout: 'connections'`), поэтому обработчики подключений,
  // рассылка состояния и разбор сообщений остаются общими.
  showConnections() {
    if (this.connectionsPanel) {
      this.connectionsPanel.reveal(vscode.ViewColumn.Active, false)
      this.postState(true)
      return
    }
    this.connectionsPanel = vscode.window.createWebviewPanel(
      'point.connections',
      'Подключения Point',
      vscode.ViewColumn.Active,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
      },
    )
    this.connectionsStateSignature = ''
    this.connectionsPanel.iconPath = vscode.Uri.joinPath(this.context.extensionUri, 'media', 'agent.svg')
    this.connectionsPanel.webview.html = this.html(this.connectionsPanel.webview, 'connections')
    this.connectionsPanel.webview.onDidReceiveMessage(message => this.handleMessage(message), undefined, this.context.subscriptions)
    this.connectionsPanel.onDidChangeViewState(() => { this.onHubVisibility(this.hubVisible()) }, undefined, this.context.subscriptions)
    this.connectionsPanel.onDidDispose(() => {
      this.connectionsPanel = undefined
      this.connectionsStateSignature = ''
      if (!this.hubVisible()) this.onHubVisibility(false)
    }, undefined, this.context.subscriptions)
    this.postState(true)
  }

  async waitForCompanionSurface(surface, timeoutMs = 1600) {
    const ready = () => {
      if (surface === 'sidebar') return Boolean(this.companionSidebar && this.companionSidebar.visible !== false)
      if (surface === 'dock') return Boolean(this.view && this.view.visible !== false)
      return Boolean(this.companionPopup && this.companionPopup.visible !== false)
    }
    const deadline = Date.now() + timeoutMs
    while (!ready() && Date.now() < deadline) {
      await new Promise(resolve => setTimeout(resolve, 80))
    }
    return ready()
  }

  async showCompanionDock() {
    return this.showCompanionSidebar()
  }

  async showCompanionSidebar() {
    this.companionFocusTarget = 'sidebar'
    const closeLeftAssistant = Boolean(this.view && this.view.visible !== false)
    try {
      await vscode.commands.executeCommand('workbench.view.extension.pointCompanion')
    } catch { /* the view may already be visible */ }
    try {
      await vscode.commands.executeCommand('localAgent.companionChat.focus')
    } catch { /* opening the container above is sufficient on older hosts */ }
    try {
      await vscode.commands.executeCommand('workbench.action.focusAuxiliaryBar')
    } catch { /* older hosts may not expose the command */ }
    if (!await this.waitForCompanionSurface('sidebar')) {
      this.service.hostLog('warn', '[ui] assistant right sidebar did not become visible; opening editor chat fallback')
      return this.showCompanionPeek()
    }
    if (closeLeftAssistant) {
      try {
        await vscode.commands.executeCommand('workbench.action.closeSidebar')
      } catch { /* the primary sidebar may already be closed */ }
    }
    this.flushCompanionFocus()
    this.pushCompanionThreadSync('sidebar')
    this.postState(true)
    return 'sidebar'
  }

  closeCompanionPopup() {
    if (!this.companionPopup) return
    this.companionPopup.dispose()
  }

  // Страница диалога, окно сведений и архив живут в chat-documents.js. Собираем
  // их по первому обращению, а не в конструкторе: смоуки зовут эти методы через
  // prototype на подставном объекте, и поле, заполняемое конструктором, там
  // осталось бы пустым. Контекст читается замыканием — при перезапуске панели
  // он подменяется, а сохранённая ссылка указывала бы на прежний.
  get chatDocuments() {
    if (!this.chatDocumentsCache) {
      this.chatDocumentsCache = createChatDocuments({
        escapeHtml, extensionUri: this.context?.extensionUri, readContext: () => this.context,
      })
    }
    return this.chatDocumentsCache
  }

  companionDocument(title, subtitle, messages, details = '') { return companionDocumentHtml(title, subtitle, messages, details, escapeHtml) }
  showCompanionMessageDetails(item, request) { return this.chatDocuments.showCompanionMessageDetails(item, request) }
  showCompanionArchives() { return this.chatDocuments.showCompanionArchives() }


  async showLogChat() {
    if (this.logChatPanel) {
      this.logChatPanel.reveal(vscode.ViewColumn.Active)
      await this.refreshLogChat()
      return
    }
    const panel = vscode.window.createWebviewPanel('point.logChat', 'Помощник · логи', vscode.ViewColumn.Active, {
      enableScripts: true,
      retainContextWhenHidden: true,
    })
    this.logChatPanel = panel
    panel.iconPath = vscode.Uri.joinPath(this.context.extensionUri, 'media', 'logs.svg')
    panel.webview.html = this.logChatHtml()
    panel.webview.onDidReceiveMessage(async message => {
      try {
        if (message?.type === 'ready' || message?.type === 'refresh') {
          await this.refreshLogChat()
          return
        }
        if (message?.type !== 'send') return
        const text = String(message.message || '').trim()
        if (!text) return
        panel.webview.postMessage({ type: 'busy', value: true })
        await this.service.ensureStarted()
        const snapshot = this.service.readLogSnapshot(180)
        // Строки выбираются под вопрос: см. selectLogExcerptLines.
        const picked = selectLogExcerptLines(snapshot.lines, text, { maxLines: 30 })
        // Человек должен видеть, по чему отвечают: выдержка теперь зависит от
        // вопроса, и молчаливая подмена «последних ошибок» на «строки по теме»
        // выглядела бы как разные ответы на один и тот же вопрос.
        panel.webview.postMessage({ type: 'attached', lines: picked.length, total: (snapshot.lines || []).length })
        const excerpt = picked
          .map(item => `${item.time || ''} ${String(item.level || 'info').toUpperCase()} [${item.source || 'core'}] ${item.message || ''}`)
          .join('\n')
          .slice(-7000)
        // Ответ идёт потоком: разбор занимает десятки секунд.
        await this.service.requestNdjson('/api/companion/chat', {
          method: 'POST',
          timeoutMs: 90_000,
          onDelta: reply => panel.webview.postMessage({ type: 'delta', text: String(reply || '') }),
          body: JSON.stringify({
            speaker: 'companion-log',
            message: text,
            apiKey: await this.credentialForCompanion(),
            focus: { failure: excerpt ? `Снимок логов Point:\n${excerpt}` : 'Лог Point пока пуст.' },
          }),
        })
        await this.refreshLogChat()
      } catch (error) {
        panel.webview.postMessage({ type: 'error', message: formatCompanionChatError(error, this.boot?.companion?.model) })
      } finally {
        panel.webview.postMessage({ type: 'busy', value: false })
      }
    }, undefined, this.context.subscriptions)
    panel.onDidDispose(() => { if (this.logChatPanel === panel) this.logChatPanel = undefined }, undefined, this.context.subscriptions)
    await this.refreshLogChat()
  }

  async refreshLogChat() {
    if (!this.logChatPanel) return
    try {
      await this.service.ensureStarted()
      const [messages, snapshot] = await Promise.all([
        this.service.request('/api/companion/history?speaker=companion-log&limit=80'),
        Promise.resolve(this.service.readLogSnapshot(180)),
      ])
      void this.logChatPanel.webview.postMessage({ type: 'state', messages, snapshot })
    } catch (error) {
      void this.logChatPanel.webview.postMessage({ type: 'error', message: formatCompanionChatError(error, this.boot?.companion?.model) })
    }
  }

  logChatHtml() {
    const nonce = crypto.randomBytes(16).toString('base64')
    return `<!doctype html><html lang="ru"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';"><title>Помощник · логи</title><style>
      :root{color-scheme:dark;font-family:"Segoe UI Variable","Segoe UI",sans-serif;background:#0a0a0a;color:#f3f5f8}*{box-sizing:border-box}body{margin:0;height:100vh}.app{display:grid;grid-template-rows:auto minmax(0,1fr) auto;height:100%}header{align-items:center;background:#121212;border-bottom:1px solid #2e2e2e;display:flex;gap:12px;padding:12px 16px}header div{display:flex;flex:1;flex-direction:column}header strong{font-size:14px}header small{color:#8e9aad}button{background:#1a1a1a;border:1px solid #2e2e2e;color:#f3f5f8;cursor:pointer;min-height:30px;padding:0 10px}button:hover{border-color:#ff8a4c}.thread{display:flex;flex-direction:column;gap:12px;overflow:auto;padding:18px}.empty{align-self:center;color:#8e9aad;margin:auto;max-width:480px;text-align:center}.msg{border:1px solid #2e2e2e;max-width:88%;padding:12px 14px}.msg.user{align-self:flex-end;background:#10181a;border-right:2px solid #46d8e8}.msg.assistant{align-self:flex-start;background:#121212;border-left:2px solid #ff8a4c}.msg b{color:#8e9aad;display:block;font-size:10px;margin-bottom:7px;text-transform:uppercase}.msg pre{font:400 13px/1.55 "Segoe UI Variable","Segoe UI",sans-serif;margin:0;white-space:pre-wrap;word-break:break-word}.compose{background:#121212;border-top:1px solid #2e2e2e;display:grid;gap:8px;grid-template-columns:minmax(0,1fr) auto;padding:12px}.compose textarea{background:#0a0a0a;border:1px solid #2e2e2e;color:#f3f5f8;font:400 13px/1.45 inherit;min-height:62px;padding:10px;resize:vertical}.compose textarea:focus{border-color:#ff8a4c;outline:0}.compose button{background:#ff8a4c;border-color:#ff8a4c;color:#18100c;font-weight:700}.compose small{color:#8e9aad;grid-column:1/-1}.error{background:#34191d;border-bottom:1px solid #ee6873;color:#ffd7dc;padding:8px 16px}[hidden]{display:none!important}
    </style></head><body><div class="app"><div><header><div><strong>Диагностика логов</strong><small id="meta">Отдельный контекст · основной чат не смешивается</small></div><button id="refresh">Обновить</button></header><p id="error" class="error" hidden></p></div><main id="thread" class="thread"></main><form id="form" class="compose"><textarea id="input" placeholder="Спросите о причинах ошибок, связях событий или следующей проверке…"></textarea><button id="send" type="submit">Спросить</button><small>В вопрос добавляются последние ошибки и предупреждения из локального лога. Ключи и секреты не сохраняются.</small></form></div><script nonce="${nonce}">
      const vscode=acquireVsCodeApi(),thread=document.getElementById('thread'),input=document.getElementById('input'),send=document.getElementById('send'),error=document.getElementById('error'),meta=document.getElementById('meta');let busy=false,history=[],pending='',streamed='',attached='';
      function bubble(role,label,text){const article=document.createElement('article');article.className='msg '+role;const b=document.createElement('b');b.textContent=label;const body=document.createElement('pre');body.textContent=text||'';article.append(b,body);thread.append(article)}
      // Ядро сохраняет вопрос и ответ одной парой в конце круга, а лента
      // рисовалась только из сохранённой истории: отправил — и до ответа не
      // видно ни своей реплики, ни признака работы, а при обрыве круга не
      // остаётся и её. Поэтому отправленное показывается сразу и держится на
      // экране, пока пара не доедет до истории.
      function paint(){thread.replaceChildren();if(!history.length&&!pending){const empty=document.createElement('div');empty.className='empty';empty.textContent='Здесь будет отдельный разговор о логах. Начните с вопроса «Что сломалось и с чего проверить?»';thread.append(empty);return}for(const item of history){bubble(item.role==='user'?'user':'assistant',item.role==='user'?'Вы':'Помощник по логам',item.content)}if(pending){bubble('user','Вы',pending);if(busy)bubble('assistant','Помощник по логам',streamed||'Разбираю логи…')}thread.scrollTop=thread.scrollHeight}
      function setBusy(value){busy=Boolean(value);send.disabled=busy;input.disabled=busy;send.textContent=busy?'Разбираю…':'Спросить';paint()}
      window.addEventListener('message',event=>{const m=event.data||{};if(m.type==='state'){history=Array.isArray(m.messages)?m.messages:[];streamed='';if(pending&&history.some(item=>item.role==='user'&&item.content===pending))pending='';const c=m.snapshot?.counts||{};meta.textContent='Отдельный контекст · '+Number(c.error||0)+' ошибок · '+Number(c.warning||0)+' предупреждений'+attached;error.hidden=true;paint()}if(m.type==='delta'){streamed+=String(m.text||'');paint()}if(m.type==='attached'){attached=' · в вопрос ушло '+Number(m.lines||0)+' строк из '+Number(m.total||0);if(meta.textContent.indexOf(' · в вопрос')<0)meta.textContent+=attached}if(m.type==='busy')setBusy(m.value);if(m.type==='error'){error.textContent=m.message||'Не удалось открыть чат';error.hidden=false;paint()}});
      document.getElementById('refresh').addEventListener('click',()=>vscode.postMessage({type:'refresh'}));document.getElementById('form').addEventListener('submit',event=>{event.preventDefault();const text=input.value.trim();if(!text||busy)return;input.value='';pending=text;streamed='';setBusy(true);vscode.postMessage({type:'send',message:text})});vscode.postMessage({type:'ready'});
    </script></body></html>`
  }

  async onWorkspaceTrustGranted() {
    this.postState()
    if (!this.hubVisible() || !vscode.workspace.getConfiguration('localAgent').get('autoStart', true)) return
    try {
      await this.service.ensureStarted()
      await this.refresh()
    } catch (error) {
      this.notify(error)
    }
  }

  html(webview, layout = 'sidebar') {
    const nonce = Math.random().toString(36).slice(2)
    const tokens = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'rpg-tokens.css'))
    const style = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'style.css'))
    const script = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'main.js'))
    const companionLayouts = new Set(['companion', 'companion-popup', 'companion-peek', 'companion-sidebar'])
    const loading = companionLayouts.has(layout)
      ? '<strong>ПОМОЩНИК POINT</strong><small>Открываем диалог…</small>'
      : '<strong>POINT / ГИЛЬДИЯ</strong><small>Открываем доску квестов…</small>'
    return `<!doctype html><html lang="ru"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src ${webview.cspSource} data:; style-src ${webview.cspSource}; font-src ${webview.cspSource}; script-src 'nonce-${nonce}';"><link rel="stylesheet" href="${tokens}"><link rel="stylesheet" href="${style}"><title>Point</title></head><body data-layout="${layout}"><div id="root"><div class="loading"><span class="spinner"></span>${loading}</div></div><script nonce="${nonce}" src="${script}"></script></body></html>`
  }

  launcherHtml(webview) {
    const nonce = Math.random().toString(36).slice(2)
    const tokens = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'rpg-tokens.css'))
    const style = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'style.css'))
    return `<!doctype html><html lang="ru"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource}; script-src 'nonce-${nonce}';"><link rel="stylesheet" href="${tokens}"><link rel="stylesheet" href="${style}"><title>Агенты Point</title></head><body data-layout="launcher"><main class="agent-launcher"><span>✦</span><strong>АГЕНТЫ POINT</strong><p>Ростер, квесты, инструменты и flow работают на отдельной полноценной странице.</p><button id="open" class="primary">Открыть Agent Hub →</button></main><script nonce="${nonce}">const vscode=acquireVsCodeApi();document.getElementById('open').addEventListener('click',()=>vscode.postMessage({type:'openHub'}));</script></body></html>`
  }

  async revertPatch(id) {
    const patch = this.boot?.changes?.find(item => item.id === id)
    if (!patch) throw new Error('Изменение не найдено в локальной хронике.')
    const answer = await vscode.window.showWarningMessage(
      `Откатить изменение файла «${patch.path}»? Point проверит, что файл не менялся после действия агента.`,
      { modal: true },
      'Откатить',
    )
    if (answer !== 'Откатить') return
    await this.service.request(`/api/patches/${encodeURIComponent(id)}/revert`, { method: 'POST', body: '{}' })
    await this.refreshRuntimeState({ companion: true })
    if (this.activeRunId === patch.runId) await this.loadRun(this.activeRunId, false)
    this.post({ type: 'patchReverted', patchId: id })
    this.postState(true)
  }

  async startCursorRun(message) {
    const task = String(message.task || '').trim()
    if (!task) throw new Error('Сначала сформулируйте задачу для Cursor Agent.')
    if (this.cursorRun) throw new Error('Cursor Agent уже выполняет задачу.')
    const profile = this.boot?.profiles?.find(item => item.id === message.profileId)
    if (!profile) throw new Error('Профиль агента не найден.')
    if (profile.provider !== 'cursor-cli') throw new Error('Cursor SDK доступен только для профилей Cursor Agent.')
    const state = await this.refreshCursorRuntime()
    if (!state.available) throw new Error(state.error || 'Cursor SDK недоступен.')
    if (!state.authenticated) throw new Error(state.error || 'Войдите в Cursor перед запуском агента.')
    const cwd = this.workspaceFolder()?.uri?.fsPath
    if (!cwd) throw new Error('Откройте локальную рабочую папку перед запуском Cursor Agent.')
    this.cursorRun = cursorRuntime.startRun({
      profile,
      task,
      cwd,
      onEvent: event => {
        this.post({ type: 'cursorRunEvent', event })
        // Consumers that already render Point's incremental updates can accept
        // the normalized Cursor event without waiting for a backend poll.
        this.post({ type: 'runDelta', details: this.details, workflowDetails: this.workflowDetails, cursorRunEvent: event })
      },
    })
    this.updateAgentBusy()
    this.post({ type: 'cursorRunStarted', profileId: profile.id })
    this.postState(true)
    this.cursorRun.done
      .then(result => this.post({ type: 'cursorRunFinished', status: result?.status || 'finished', result }))
      .catch(error => this.post({ type: 'cursorRunFinished', status: 'error', error: describeCoreFailure(error) }))
      .finally(async () => {
        this.cursorRun = undefined
        this.updateAgentBusy()
        await this.refreshCursorRuntime()
        this.postState(true)
      })
  }

  async launchCursorAgent(message) {
    const task = String(message.task || '').trim()
    if (!task) throw new Error('Сначала сформулируйте задачу для Cursor Agent.')
    const configured = String(vscode.workspace.getConfiguration('localAgent').get('cursorCommand', 'cursor-agent')).trim()
    if (!configured) throw new Error('Команда Cursor Agent не настроена.')
    const model = String(message.model || '').trim()
    const quote = value => process.platform === 'win32'
      ? `'${String(value).replace(/'/g, "''")}'`
      : `'${String(value).replace(/'/g, `'"'"'`)}'`
    const terminal = vscode.window.createTerminal({
      name: 'Point · Cursor Agent',
      cwd: this.workspaceFolder()?.uri,
      ...(process.platform === 'win32' ? { shellPath: 'powershell.exe', shellArgs: ['-NoLogo'] } : {}),
    })
    terminal.show(true)
    const modelFlag = model && model !== 'auto' ? ` --model ${quote(model)}` : ''
    terminal.sendText(`${configured}${modelFlag} ${quote(task)}`, true)
  }

  dispose() {
    this.resetRunState()
    if (this.gitRefreshTimer) clearTimeout(this.gitRefreshTimer)
    this.gitRefreshTimer = undefined
    this.gitRepositoryListener?.dispose?.()
    this.gitRepositoryListener = undefined
    void this.cursorRun?.cancel()
    void this.cursorRun?.dispose()
    this.panel?.dispose()
    if (this.hubGarbageTimer) clearTimeout(this.hubGarbageTimer)
    this.hubGarbageTimer = undefined
    this.statisticsPanel?.dispose()
    this.dockerPanel?.dispose()
  }

  resetRunState() {
    this.stopPollingTimers(true)
    if (this.autoStartTimer) clearTimeout(this.autoStartTimer)
    this.autoStartTimer = undefined
    this.activeRunId = undefined
    this.activeWorkflowRunId = undefined
    this.details = undefined
    this.workflowDetails = undefined
    this.updateAgentBusy()
  }
}

const {
  normalizeCompanionArg, companionClosing, nearbyEditorSnippet, resolveCompanionDocument,
  companionFailureMessage, companionAskMessage, openCompanionChat, createCompanionDecorations,
  createCompanionCodeLensProvider, companionCommandMarkdown, companionWorkspaceRelativePath,
  formatCompanionFailure, formatCompanionChatError, currentCompanionIdeContext, liveCompanionFocus,
  pathIsUnder, pickGitRepository, gitListsState, workspaceFolderForUri, pointWorkspaceFolder,
  localWorkspaceFolders, workspaceFolderByPath, restorePointWorkspaceRoot, resumeLastPointWorld, recentPointProjects,
  choosePointWorkspace, switchPointWorkspaceInPlace, chronicleWorkspaceFolder, gitCwdForUri, resolveGitRoot, pathRelativeToRoot,
  formatVcsError, companionDiffMessage, companionTerminalMessage, companionIdeExtras,
  companionRunMessage, companionDebugMessage, companionBlameMessage, companionHistoryMessage,
  companionSavedMessage, showCompanionInbox, workspaceFileUri, openWorkspaceFile,
  workspaceRelativePath, workspaceRelativePathIfInside,
} = createCompanionController({
  vscode, path, runGit, normalizedWorkspaceRoot, POINT_WORKSPACE_ROOT_KEY, GIT_DEFAULT_LIST,
  getActiveView: () => activeView,
  getActiveService: () => activeService,
})
bindCompanionChatHelpers({ formatCompanionChatError })

function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer()
    server.unref()
    server.on('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      server.close(() => resolve(address.port))
    })
  })
}

async function forceStopProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode) return
  try { child.kill() } catch { /* already gone */ }
  const exited = await Promise.race([
    new Promise(resolve => child.once('exit', () => resolve(true))),
    new Promise(resolve => setTimeout(() => resolve(false), 2500)),
  ])
  if (exited || !child.pid) return
  if (process.platform === 'win32') {
    await new Promise(resolve => {
      const killer = spawn('taskkill', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' })
      killer.once('exit', resolve)
      killer.once('error', resolve)
      setTimeout(resolve, 2000)
    })
  } else {
    try { process.kill(child.pid, 'SIGKILL') } catch { /* already gone */ }
  }
  await Promise.race([
    new Promise(resolve => child.once('exit', resolve)),
    new Promise(resolve => setTimeout(resolve, 1000)),
  ])
}

async function forceStopPid(pid) {
  const target = Number(pid)
  if (!Number.isInteger(target) || target <= 0 || !processIsAlive(target)) return
  if (process.platform === 'win32') {
    await new Promise(resolve => {
      const killer = spawn('taskkill', ['/PID', String(target), '/T', '/F'], { windowsHide: true, stdio: 'ignore' })
      killer.once('exit', resolve)
      killer.once('error', resolve)
      setTimeout(resolve, 2500)
    })
    return
  }
  try { process.kill(target, 'SIGTERM') } catch { return }
  await new Promise(resolve => setTimeout(resolve, 400))
  if (processIsAlive(target)) {
    try { process.kill(target, 'SIGKILL') } catch { /* already gone */ }
  }
}

function safeFileName(value) {
  const safe = String(value || 'agent').trim().replace(/[<>:"/\\|?*\x00-\x1F]/g, '-').replace(/[. ]+$/g, '')
  return safe || 'agent'
}

function portableProfile(profile) {
  return {
    name: String(profile.name || ''),
    roleDescription: String(profile.roleDescription || ''),
    systemPrompt: String(profile.systemPrompt || ''),
    goals: Array.isArray(profile.goals) ? profile.goals.map(String) : [],
    rules: Array.isArray(profile.rules) ? profile.rules.map(String) : [],
    provider: profile.provider,
    providerPreset: String(profile.providerPreset || ''),
    baseUrl: String(profile.baseUrl || ''),
    model: String(profile.model || ''),
    temperature: Number(profile.temperature ?? 0.2),
    maxOutputTokens: Number(profile.maxOutputTokens || 4096),
    reasoningEffort: String(profile.reasoningEffort || 'none'),
    allowedTools: Array.isArray(profile.allowedTools) ? profile.allowedTools.map(String) : [],
    maxSteps: Number(profile.maxSteps),
    maxDurationSeconds: Number(profile.maxDurationSeconds),
    approvalMode: profile.approvalMode,
  }
}

function orchestratorConfigPayload(config = {}) {
  const payload = {
    id: String(config.id || ''),
    workspaceId: String(config.workspaceId || ''),
    preset: String(config.preset || ''),
    connectionId: String(config.connectionId || ''),
    provider: config.provider || '',
    providerPreset: String(config.providerPreset || ''),
    baseUrl: String(config.baseUrl || ''),
    model: String(config.model || ''),
    temperature: Number(config.temperature ?? 0),
    maxOutputTokens: Number(config.maxOutputTokens || 0),
    planningDepth: Number(config.planningDepth ?? 50),
    parallelism: Number(config.parallelism ?? 50),
    approvalStrictness: Number(config.approvalStrictness ?? 50),
    teamPreference: Number(config.teamPreference ?? 50),
  }
  if (config.createdAt) payload.createdAt = config.createdAt
  return payload
}

function importedProfile(value, fallback) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('В файле отсутствует объект профиля.')
  const base = portableProfile(fallback || {})
  const imported = portableProfile({ ...base, ...value })
  return { ...imported, id: '', createdAt: undefined, updatedAt: undefined }
}

function portableCustomTool(tool) {
  return {
    kind: tool.kind === 'command' ? 'command' : 'process',
    displayName: String(tool.displayName || ''), description: String(tool.description || ''),
    command: String(tool.command || ''), program: String(tool.program || ''),
    arguments: Array.isArray(tool.arguments) ? tool.arguments.map(String) : [],
    parameters: Array.isArray(tool.parameters) ? tool.parameters.map(parameter => ({
      name: String(parameter.name || ''), displayName: String(parameter.displayName || ''), description: String(parameter.description || ''),
      type: String(parameter.type || 'string'), required: Boolean(parameter.required),
      enumValues: Array.isArray(parameter.enumValues) ? parameter.enumValues.map(String) : [], maxLength: Number(parameter.maxLength || 0),
    })) : [],
    cwd: String(tool.cwd || '.'), timeoutSeconds: Number(tool.timeoutSeconds || 120),
    providesVerification: Boolean(tool.providesVerification),
  }
}

function importedCustomTool(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('В файле отсутствует объект инструмента.')
  return { ...portableCustomTool(value), id: '', createdAt: undefined, updatedAt: undefined }
}

// Домашний экран и хроника — отдельный модуль: с провайдером вебвью их
// связывает только вызов «показать».
const { PointHome, PointChronicle } = createPointPanels({
  escapeHtml, runGit, resolveGitRoot, openWorkspaceFile, chronicleWorkspaceFolder,
})

// Ожидание — параметр, а не константа: чтение истории обязано быть быстрым, а
// коммит ждёт чужие хуки (форматирование, тесты) и на двенадцати секундах
// обрывался бы посреди работы.
function runGit(cwd, args, maxBytes = 2 * 1024 * 1024, timeoutMs = 12_000) {
  return new Promise((resolve, reject) => {
    const child = spawn('git', args, { cwd, windowsHide: true, shell: false, stdio: ['ignore', 'pipe', 'pipe'] })
    const stdout = []
    const stderr = []
    let size = 0
    let settled = false
    const seconds = Math.round(timeoutMs / 1000)
    const timer = setTimeout(() => { if (!settled) { settled = true; child.kill(); reject(new Error(`Git не ответил за ${seconds} с.`)) } }, timeoutMs)
    child.stdout.on('data', chunk => {
      size += chunk.length
      if (size > maxBytes) { if (!settled) { settled = true; child.kill(); clearTimeout(timer); reject(new Error('Глава слишком велика для быстрого просмотра. Откройте файл из рабочего дерева.')) } return }
      stdout.push(chunk)
    })
    child.stderr.on('data', chunk => stderr.push(chunk))
    child.once('error', error => { if (!settled) { settled = true; clearTimeout(timer); reject(error) } })
    child.once('exit', code => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      if (code === 0) resolve(Buffer.concat(stdout).toString('utf8'))
      else reject(new Error(Buffer.concat(stderr).toString('utf8').trim() || `git завершился с кодом ${code}`))
    })
  })
}

function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character]))
}

const {
  LANGUAGE_SUPPORT, BUILTIN_LANGUAGE_SUPPORT, LANGUAGE_LABELS, openRecentFileRecord,
  POINT_ACTION_REGISTRY, matchSearchEverywhereActions, createBookmarkController, refactorThis,
  renameSymbol, optimizeImports, reformatCode, recentLocations, nextError, previousError,
  compareWithClipboard, showFileHistory, hideAllToolWindows, selectNextOccurrence,
  readRunAnythingManifest, discoverRunConfigurations, createRunConfigurationController,
  vcsRollback, vcsShowDiff, vcsPush, vcsPull, editRunConfigurations, focusGitTool, vcsCommit,
  vcsChanges, gitAnnotate, peekUsages, parameterHints,
} = createIdeActionController({
  vscode, path, fs, parseJsonc, makefileTargets, buildRunConfigurations, pickDefaultRunConfiguration,
  pointWorkspaceFolder, gitCwdForUri, resolveGitRoot, pathRelativeToRoot, formatVcsError, runGit,
  getActiveView: () => activeView,
  listRecentFileRecords: (...args) => listRecentFileRecords(...args),
})

const {
  createWorkspaceFileCache, workspaceFileCache, bindPointIndexSearch, bindRecentFiles,
  collectOpenTabUris, createRecentFilesTracker, createStructureStatus, createProblemsStatus,
  flattenDocumentSymbols, fileStructure, copyReference, newScratchFile, compareWithFile,
  collectSaveActionEdits, flushFormatAfterOrganize, openPointIdeSettings, currentEditorWord,
  fetchPointIndexHits, indexHitToPickerItem, openIndexHit, revealIndexHits, companionProblemsMessage,
  searchEverywhere, languageSupportInstalled, languageDisplayName, promptLanguageSupport,
  goToDefinition, goToTypeDefinition, focusBreadcrumbs, moveStatement, findSymbolLocations,
  showCoreChronicle, listRecentFileRecords,
} = createIdeNavigationController({
  vscode, path, fs, pointWorkspaceFolder, companionWorkspaceRelativePath, companionClosing,
  LANGUAGE_SUPPORT, BUILTIN_LANGUAGE_SUPPORT, LANGUAGE_LABELS, POINT_ACTION_REGISTRY,
  matchSearchEverywhereActions, openRecentFileRecord, fuzzyScore, symbolIcon, parseDocumentOutline,
  resolveOutlineSymbolAt, formatOutlineBreadcrumb, outlineKindIcon, mergeRecentFiles,
  formatCopyReference, parseSearchEverywhereQuery,
})

const {
  formatIndexStatus, applyIndexStatus, indexAutoConfig, indexScheduleWaitMs,
  applyIndexDirty, isIndexNoiseUri, createProjectIndexController,
} = createProjectIndex({
  vscode, fs, path, workspaceFileCache, workspaceRelativePathIfInside, describeCoreFailure,
})

function cheapStateSignature(message) {
  const boot = message.boot
  const details = message.details
  const workflow = message.workflowDetails
  return JSON.stringify({
    s: message.service,
    t: message.workspaceTrusted,
    w: message.workspace,
    tab: message.selectedTab,
    on: message.onboarding,
    imp: message.agentImprovementFocus,
    idx: boot?.indexStatus,
    ws: boot?.currentWorkspace?.id,
    pr: boot?.profiles?.map(p => [p.id, p.name, p.model]),
    runs: boot?.runs?.map(r => [r.id, r.status, r.updatedAt || r.finishedAt || r.startedAt]),
    wf: boot?.workflows?.map(item => item.id),
    wfr: boot?.workflowRuns?.map(r => [r.id, r.status]),
    tools: boot?.customTools?.map(item => item.id),
    connections: boot?.connections?.map(item => [item.id, item.status, item.lastError, item.updatedAt]),
    servers: boot?.serverProfiles?.map(item => [item.id, item.status, item.lastError, item.lastProbeAt, item.updatedAt]),
    databases: boot?.dbConnections?.map(item => [item.id, item.status, item.lastError, item.lastProbeAt, item.updatedAt]),
    ch: boot?.changes?.length,
    hubAgents: boot?.projectAgents?.map(item => [item.id, item.updatedAt]),
    hubTeams: boot?.teams?.map(item => [item.id, item.updatedAt, item.agentIds?.length]),
    hubExec: boot?.executions?.map(item => [item.id, item.status, item.durationMs]),
    hubSets: boot?.changeSets?.map(item => [item.id, item.status, item.items?.length]),
    companionHistory: boot?.companionMessages?.at?.(-1)?.id,
    companionInterventions: boot?.companionInterventions?.map(item => [item.id, item.level]),
    companionActions: boot?.companionActionProposals?.map(item => [item.id, item.status]),
    // Задание меняется внутри одного предложения: id и статус остаются прежними,
    // а обсуждение превращается в готовое к запуску. По двум полям подпись этого
    // не видела, состояние в вебвью не уезжало — и человек читал «задание готово
    // к запуску» над карточкой, которой в ленте нет, потому что там всё ещё
    // лежит прежняя, обсуждаемая версия. Версия задания растёт при каждом
    // изменении содержания, поэтому её и состояния достаточно.
    questProposals: boot?.questProposals?.map(item => [item.id, item.status, item.brief?.version, item.brief?.state]),
    d: details && {
      id: details.run?.id,
      st: details.run?.status,
      ev: details.events?.length,
      ap: details.approvals?.length,
      pa: details.patches?.length,
    },
    wd: workflow && {
      id: workflow.id,
      st: workflow.status,
      ev: workflow.events?.length,
    },
  })
}

// Ошибки ядра, которые человек встречает при обычной работе.
//
// Ядро отвечает по-английски — это его язык логов и API. Через describeCoreFailure
// текст попадает прямо в красную полосу русскоязычного Хаба, и человек читает
// «daily hub budget exceeded; new runs are blocked until…» вместо объяснения.
// Переводим не всё подряд (в одном internal/app таких строк под две сотни), а
// те, что приходят в ответ на обычные действия: запустить квест, применить или
// откатить набор, удалить персонажа.
//
// Совпадение ищем по устойчивому куску, а не по всей строке: ядро подставляет в
// них статусы и числа.
const CORE_FAILURE_HINTS = [
  [/daily hub budget exceeded/i, 'Дневной лимит расходов исчерпан. Новые запуски остановлены, пока лимит не обновится или не будет снят жёсткий стоп в «Бюджете проекта».'],
  [/monthly hub budget exceeded/i, 'Месячный лимит расходов исчерпан. Новые запуски остановлены, пока лимит не обновится или не будет снят жёсткий стоп в «Бюджете проекта».'],
  [/quest cost budget exceeded/i, 'Бюджет квеста исчерпан: запуск остановлен, чтобы не тратить сверх заданного.'],
  [/change set cannot be applied from status/i, 'Набор изменений нельзя применить из текущего состояния — он уже применён, отклонён или откачен.'],
  [/only an applied change set can be reverted/i, 'Откатить можно только применённый набор изменений.'],
  [/change path escapes workspace/i, 'Путь из набора ведёт за пределы проекта — применение остановлено.'],
  [/the default profile cannot be deleted/i, 'Персонажа по умолчанию удалить нельзя.'],
  [/workspace is not open/i, 'Проект не открыт: откройте папку проекта, чтобы ядро могло работать.'],
  [/resource belongs to another project world/i, 'Эта запись принадлежит другому проекту.'],
]

function describeCoreFailure(error) {
  const cause = error && typeof error === 'object' ? error.cause : undefined
  const raw = error instanceof Error
    ? (error.message || (cause && cause.message) || String(error))
    : String(error ?? '')
  const text = String(raw || '').trim()
  // Ошибка без сообщения даёт String(error) === 'Error', и проверка на пустоту
  // не срабатывала: человек видел в красной полосе одно английское слово
  // «Error» — ни причины, ни языка интерфейса.
  if (!text || /^Error:?$/i.test(text)) return 'Неизвестная ошибка локального ядра Point.'
  if (/fetch failed|Failed to fetch|ECONNREFUSED|ENOTFOUND|ECONNRESET|network/i.test(text)) {
    return 'Локальное ядро Point не отвечает на 127.0.0.1. Откройте «Хроника ядра» или перезапустите ядро — антивирус/брандмауэр могут блокировать порт.'
  }
  for (const [pattern, russian] of CORE_FAILURE_HINTS) {
    if (pattern.test(text)) return russian
  }
  if (/^TypeError:/i.test(text) && /fetch/i.test(text)) {
    return 'Не удалось связаться с локальным ядром Point. Откройте «Хроника ядра» или перезапустите ядро.'
  }
  return text
}

async function ensureMarketplaceInstallAllowed() {
  const config = vscode.workspace.getConfiguration('extensions')
  if (config.get('verifySignature') === false) return
  try {
    await config.update('verifySignature', false, vscode.ConfigurationTarget.Application)
  } catch {
    // Application settings can be locked; Marketplace install may still prompt.
  }
}

async function ensureGoVulncheckCompatible() {
  // golang.Go defaults go.diagnostic.vulncheck to "Prompt", but gopls <0.21 rejects it.
  const config = vscode.workspace.getConfiguration('go')
  const inspect = config.inspect('diagnostic.vulncheck')
  const values = [
    inspect?.globalValue,
    inspect?.workspaceValue,
    inspect?.workspaceFolderValue,
    inspect?.defaultValue,
  ]
  if (!values.includes('Prompt')) return
  const current = config.get('diagnostic.vulncheck')
  if (current && current !== 'Prompt') return
  try {
    await config.update('diagnostic.vulncheck', 'Off', vscode.ConfigurationTarget.Global)
  } catch {
    // Best-effort override; configurationDefaults in package.json covers new installs.
  }
}

async function installLanguageExtension(extensionId, label) {
  await ensureMarketplaceInstallAllowed()
  if (vscode.extensions.getExtension(extensionId)) {
    await vscode.window.showInformationMessage(`${label} уже установлено (${extensionId}).`)
    return
  }
  await vscode.window.withProgress(
    { location: vscode.ProgressLocation.Notification, title: `Установка ${label}…`, cancellable: false },
    async () => {
      await vscode.commands.executeCommand('workbench.extensions.installExtension', extensionId)
    },
  )
  const installed = vscode.extensions.getExtension(extensionId)
  if (installed) {
    if (/^golang\.go$/i.test(extensionId)) await ensureGoVulncheckCompatible()
    const reload = await vscode.window.showInformationMessage(
      `${label} установлено. Перезагрузите окно, чтобы language server начал Ctrl+Click и usages.`,
      'Перезагрузить',
    )
    if (reload === 'Перезагрузить') await vscode.commands.executeCommand('workbench.action.reloadWindow')
    return
  }
  await vscode.commands.executeCommand('workbench.extensions.search', `@id:${extensionId}`)
  await vscode.window.showWarningMessage(
    `Не удалось установить ${label} автоматически. Откройте карточку расширения и нажмите «Установить» (при запросе подписи — «Все равно установить»).`,
  )
}

async function openLanguageSupport() {
  const languageId = vscode.window.activeTextEditor?.document.languageId || ''
  const current = LANGUAGE_SUPPORT.find(item => item.ids.includes(languageId))
  const builtin = BUILTIN_LANGUAGE_SUPPORT.has(languageId)
  const items = []
  if (languageId) {
    const languageLabel = current?.label || LANGUAGE_LABELS[languageId] || languageId
    items.push({
      label: `$(symbol-keyword) Текущий язык: ${languageLabel}`,
      description: builtin ? 'Расширенная поддержка уже встроена' : current ? 'Установить / открыть расширение' : 'Найти language server',
      detail: current?.extension || '',
      profile: current,
      builtin,
      languageId,
    })
    items.push({ label: 'Популярные языки', kind: vscode.QuickPickItemKind.Separator })
  }
  items.push(...LANGUAGE_SUPPORT.map(profile => ({
    label: `$(extensions) ${profile.label}`,
    description: vscode.extensions.getExtension(profile.extension) ? 'Установлено' : 'Установить по требованию',
    detail: profile.extension,
    profile,
  })))
  items.push({ label: 'Другой язык', kind: vscode.QuickPickItemKind.Separator })
  items.push({
    label: '$(search) Найти поддержку другого языка',
    description: 'Каталог расширений',
    languageId,
  })
  const selected = await vscode.window.showQuickPick(items, {
    title: 'Point — поддержка языков',
    placeHolder: 'Language server запускается только для открытого языка',
    matchOnDescription: true,
    matchOnDetail: true,
  })
  if (!selected) return
  if (selected.builtin) {
    await vscode.window.showInformationMessage(`${selected.label.replace(/^\$\([^)]*\)\s*/, '')}: навигация и поиск использований уже встроены в Point.`)
    return
  }
  if (selected.profile) {
    await installLanguageExtension(selected.profile.extension, selected.profile.label)
    return
  }
  await vscode.commands.executeCommand('workbench.extensions.search', `@category:"Programming Languages" ${selected.languageId || ''}`.trim())
}

const {
  consoleChannelChrome, createConsoleChannel, openConsoleChannel, openSSHTerminalForProfile,
  sshProfilePassword, requestSSHRemoteList, openSSHRemotePreview, browseSSHRemotePath,
  manageSSHServers, chooseConsoleChannel, findInFolder, stripTerminalControlSequences,
} = createConsoleSSH({ vscode, fs, os, path, pointWorkspaceFolder })

// Наблюдения IDE живут отдельным модулем: фабрика была готова, менялось
// только место.
const { createIDEObservationController } = createIDEObservations({
  companionWorkspaceRelativePath, pickGitRepository, stripTerminalControlSequences,
  workspaceRelativePathIfInside,
})

function activate(context) {
  const activationStartedAt = Date.now()
  const output = vscode.window.createOutputChannel('Point · Хроника ядра')
  output.appendLine(`[activation] start pid=${process.pid}`)
  const status = vscode.window.createStatusBarItem('point.core', vscode.StatusBarAlignment.Left, 90)
  status.name = 'Ядро Point'
  status.command = 'localAgent.coreStatusAction'
  status.text = vscode.workspace.isTrusted ? '$(server-process) Ядро' : '$(shield) Ядро'
  status.tooltip = 'Состояние локального ядра Point'
  status.hide()
  const projectStatus = vscode.window.createStatusBarItem('point.project', vscode.StatusBarAlignment.Left, 100)
  projectStatus.name = 'Мир Point'
  projectStatus.command = 'localAgent.switchProject'
  let service
  const updateProjectStatus = () => {
    const folders = localWorkspaceFolders()
    const project = service?.workspaceFolder?.() || folders[0]
    const count = folders.length
    projectStatus.text = `$(${count > 1 ? 'folder-library' : 'folder-opened'}) ${project?.name || 'Открыть проект'}${count > 1 ? ` · ${count}` : ''}`
    projectStatus.tooltip = project
      ? (count > 1
        ? `Активный проект Point: ${project.name}\nАгенты, индекс и относительные пути изолированы этим корнем. Нажмите, чтобы переключить.`
        : 'Переключить проект — Ctrl+Alt+P')
      : 'Открыть папку проекта'
    projectStatus.command = project ? 'localAgent.switchProject' : 'workbench.action.files.openFolder'
  }
  updateProjectStatus()
  projectStatus.show()
  const indexStatus = vscode.window.createStatusBarItem('point.index', vscode.StatusBarAlignment.Left, 85)
  indexStatus.name = 'Индекс Point'
  indexStatus.command = 'localAgent.rebuildIndex'
  applyIndexStatus(indexStatus, undefined, false)
  let infraVisible = false
  const busyFlags = { index: false, tasks: new Set(), agent: false }
  const paintInfraChrome = (serviceState = 'stopped') => {
    const active = infraVisible
      || serviceState === 'running'
      || serviceState === 'starting'
      || serviceState === 'error'
    if (active) status.show()
    else {
      status.hide()
      indexStatus.hide()
    }
  }
  const revealInfra = () => {
    infraVisible = true
    paintInfraChrome(service?.state || 'stopped')
  }
  const paintBusyChrome = () => {
    const busy = busyFlags.index || busyFlags.tasks.size > 0 || busyFlags.agent
    void vscode.commands.executeCommand('setContext', 'point.statusBusy', busy)
  }
  let provider
  let indexController
  let ideObserver
  let runController
  service = new BackendService(context, output, value => {
    const labels = { running: 'Ядро', starting: 'Ядро…', stopped: 'Ядро', error: 'Ядро · ошибка' }
    status.text = vscode.workspace.isTrusted
      ? `$(server-process) ${labels[value.state] || 'Ядро'}`
      : '$(shield) Ядро'
    status.tooltip = service.coreStatusTooltip()
    status.backgroundColor = vscode.workspace.isTrusted && value.state === 'error' ? new vscode.ThemeColor('statusBarItem.errorBackground') : undefined
    paintInfraChrome(value.state)
    provider?.onServiceStatus(value)
    if (value.state === 'running') {
      void indexController?.ensureReady()
      void ideObserver?.syncDiagnostics()
    } else {
      indexController?.markCoreStopped()
    }
  })
  restorePointWorkspaceRoot(service, context, {
    agentsWindowMode: isPointHubWindow(),
    activeProjectPath: String(context.globalState.get(PROJECTS_KEY)?.active || ''),
  })
  activeService = service
  updateProjectStatus()
  const companionStatus = vscode.window.createStatusBarItem('point.companion', vscode.StatusBarAlignment.Left, 88)
  companionStatus.name = 'Помощник Point'
  companionStatus.command = 'localAgent.askCompanion'
  const companionDecorations = createCompanionDecorations()
  const paintCompanionStatus = boot => {
    const items = Array.isArray(boot?.companionInterventions) ? boot.companionInterventions : []
    const speak = items.filter(item => item?.level === 'critical' || item?.level === 'warning')
    const critical = speak.some(item => item.level === 'critical')
    companionStatus.command = 'localAgent.askCompanion'
    if (speak.length) {
      companionStatus.text = `$(comment-unresolved) Помощник · ${speak.length}`
      companionStatus.tooltip = `${speak[0]?.title || 'Рекомендации помощника'} — открыть чат, без автозапуска`
      companionStatus.backgroundColor = new vscode.ThemeColor(critical ? 'statusBarItem.errorBackground' : 'statusBarItem.warningBackground')
      companionStatus.show()
    } else {
      companionStatus.text = '$(comment-discussion) Помощник'
      companionStatus.tooltip = 'Открыть помощника Point — Ctrl+Alt+;'
      companionStatus.backgroundColor = undefined
      companionStatus.show()
    }
    companionDecorations.paint(speak)
  }
  let lastAutoOpenedOccurrence = ''
  const maybeAutoOpenCompanion = boot => {
    const companion = boot?.companion || {}
    const items = Array.isArray(boot?.companionInterventions) ? boot.companionInterventions : []
    if (!items.length) {
      lastAutoOpenedOccurrence = ''
      return
    }
    if (!companion.autoOpenChatOnCritical || !provider) return
    const critical = items.find(item => item.level === 'critical')
    if (!critical?.occurrenceKey || critical.occurrenceKey === lastAutoOpenedOccurrence) return
    lastAutoOpenedOccurrence = critical.occurrenceKey
    void openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: critical.actionMessage || critical.title || '',
      send: Boolean(companion.autoSendModelPrompt),
    })
  }
  paintCompanionStatus()
  bindPointIndexSearch(async (query, limit) => {
    if (service.state !== 'running') return []
    const result = await service.request(`/api/index/search?q=${encodeURIComponent(query)}&limit=${Number(limit) || 40}`)
    return Array.isArray(result?.hits) ? result.hits : []
  })
  provider = new AgentViewProvider(context, service, output, {
    onAgentBusy: busy => {
      busyFlags.agent = busy
      paintBusyChrome()
    },
    onCompanionState: boot => {
      paintCompanionStatus(boot)
      maybeAutoOpenCompanion(boot)
    },
  })
  indexController = createProjectIndexController(service, indexStatus, {
    onBusy: (busy) => {
      busyFlags.index = busy
      paintBusyChrome()
    },
    onPaint: () => paintInfraChrome(service.state),
    onUpdated: async (result) => {
      if (!provider || service.state !== 'running') return
      if (provider.boot && result) provider.boot.indexStatus = result
      if (!provider.hubVisible()) return
      // Patch index status in-place — avoid a full /api/bootstrap round-trip.
      provider.postState()
    },
  })
  provider.indexController = indexController
  // Реестр миров и кольцо тёплых ядер. Галерея Чертога рисуется до любого ядра,
  // поэтому источник у неё хостовый, а состояние ядра по каждому проекту
  // читается с диска — из дескрипторов и аренд, без единого запроса по HTTP.
  const warmPool = {
    runtimeDirPath: path.join(service.dataDirPath, 'runtime'),
    ring: () => {
      const stored = context.globalState.get(WARM_CORES_KEY)
      return Array.isArray(stored) ? stored : []
    },
    remember: async entry => context.globalState.update(WARM_CORES_KEY, rememberWarmCore(warmPool.ring(), entry, WARM_CORES_KEEP)),
    reap: async (keep = WARM_CORES_KEEP, awaitStop = true) => {
      const result = await reapWarmCores({
        runtimeDir: warmPool.runtimeDirPath,
        ring: warmPool.ring(),
        keep,
        awaitStop,
        forceStopPid,
        hostLog: (level, message) => service.hostLog(level, message),
      })
      await context.globalState.update(WARM_CORES_KEY, result.ring)
      return result
    },
  }
  const projects = createProjectRegistry({
    vscode,
    path,
    context,
    normalizedWorkspaceRoot,
    recentPointProjects,
    runtimeDirPath: warmPool.runtimeDirPath,
    coreStateByKey,
    hostLog: (level, message) => service.hostLog(level, message),
  })
  provider.projects = projects
  provider.warmPool = warmPool
  activeWarmPool = isPointHubWindow() ? warmPool : undefined
  const postProjects = async () => {
    if (!provider.hubPanelReady && !provider.panel) return
    try {
      const list = await projects.list()
      provider.lastProjectList = list
      provider.post({ type: 'projects', active: projects.activePath(), projects: list })
    } catch (error) {
      service.hostLog('warn', `[projects] список не собрался: ${error?.message || error}`)
      provider.lastProjectList = []
      provider.post({ type: 'projects', active: projects.activePath(), projects: [] })
    }
  }
  provider.postProjects = postProjects
  const afterProjectSwitch = async () => {
    workspaceFileCache.invalidate()
    updateProjectStatus()
    home.refresh()
    provider.resetRunState()
    provider.boot = undefined
    indexController.markCoreStopped()
    const folder = provider.workspaceFolder()
    // Переключились ради конкретного чата — значит и открывается чат, а не та
    // вкладка, на которой этот мир бросили в прошлый раз.
    if (provider.pendingMasterConversation) provider.focusTab('master')
    else {
      const remembered = folder ? projects.lastTabFor(folder.uri.fsPath) : ''
      if (remembered) provider.focusTab(remembered)
    }
    provider.postState(true)
    void postProjects()
    if (provider.hubVisible() && folder && vscode.workspace.isTrusted) {
      if (vscode.workspace.getConfiguration('localAgent').get('autoStart', true)) provider.scheduleAutoStart()
      else await provider.refresh()
    }
  }
  const projectSwitchOptions = () => ({
    agentsWindowMode: provider.agentsWindowMode,
    // Мастер ещё отвечает — значит ядро уходящего мира уйдёт в тёплые под бронью.
    busy: provider.masterTurnStreams?.size > 0,
    registry: projects,
    warmPool,
    onPhase: (phase, info) => provider.post({ type: 'projectSwitch', phase, ...info }),
  })
  // Единственная дорога смены мира из галереи. Сторож `projectSwitchInFlight`
  // держит `onDidChangeWorkspaceFolders` в стороне: подмена папки — часть этой
  // работы, а не отдельное событие, на которое надо отвечать вторым сбросом.
  const switchToProject = async fsPath => {
    if (!fsPath) return
    provider.projectSwitchInFlight = true
    try {
      await switchPointWorkspaceInPlace(service, context, vscode.Uri.file(String(fsPath)), afterProjectSwitch, projectSwitchOptions())
    } finally {
      provider.projectSwitchInFlight = false
    }
  }
  provider.switchToProject = switchToProject
  runController = createRunConfigurationController(context, {
    onChange: () => {
      postCompanionIdeContext()
      // A selected launch.json entry is only a choice for the next Run. Treating
      // it as a live run made that label leak into every Companion turn even
      // before the user had started anything.
      void ideObserver?.syncRunObservation?.(() => runController?.lastStarted())
    },
  })
  ideObserver = createIDEObservationController(context, service, output, () => provider, () => postCompanionIdeContext())
  provider.companionFocus = () => liveCompanionFocus(() => ideObserver?.lastFailure(), () => runController?.lastStarted())
  void ideObserver?.syncRunObservation?.(() => runController?.lastStarted())
  void ideObserver?.syncDebugObservation?.()
  void ideObserver?.syncScmObservation?.()
  let ideContextTimer
  let lastIdeContextSignature = ''
  const companionIdeContextSignature = (context) => JSON.stringify({
    f: context?.file || '',
    l: context?.line || 0,
    lang: context?.language || '',
    d: Boolean(context?.dirty),
    diag: context?.diagnostics || 0,
    sel: Boolean(context?.selection),
    fail: context?.failure || '',
    run: context?.run || '',
    debug: context?.debug || '',
  })
  const postCompanionIdeContext = () => {
    clearTimeout(ideContextTimer)
    ideContextTimer = setTimeout(() => {
      if (!provider) return
      provider.ideContext = currentCompanionIdeContext({
        getFailure: () => ideObserver?.lastFailure(),
        getRun: () => runController?.lastStarted(),
      })
      const signature = companionIdeContextSignature(provider.ideContext)
      if (signature === lastIdeContextSignature) return
      lastIdeContextSignature = signature
      provider.post({ type: 'companionIdeContext', context: provider.ideContext })
    }, 220)
  }
  const companionLenses = createCompanionCodeLensProvider()
  postCompanionIdeContext()
  const home = new PointHome(context, provider)
  const chronicle = new PointChronicle(context, output)
  const bookmarks = createBookmarkController(context)
  const recentFiles = createRecentFilesTracker(context)
  bindRecentFiles(() => recentFiles.list())
  const problemsStatus = createProblemsStatus()
  const structureStatus = createStructureStatus()
  const rememberActive = editor => {
    rememberMasterEditor(editor)
    if (!editor?.document.uri) return
    recentFiles.remember(editor.document.uri, editor.selection?.active)
  }
  if (vscode.window.activeTextEditor) rememberActive(vscode.window.activeTextEditor)
  context.subscriptions.push(
    output,
    status,
    projectStatus,
    companionStatus,
    indexStatus,
    runController,
    companionDecorations,
    companionLenses,
    problemsStatus,
    structureStatus,
    recentFiles,
    vscode.workspace.onWillSaveTextDocument(event => {
      if (event.document.uri.scheme !== 'file') return
      const cfg = vscode.workspace.getConfiguration('localAgent', event.document)
      const format = cfg.get('formatOnSave') === true
      const organize = cfg.get('organizeImportsOnSave') === true
      if (!format && !organize) return
      event.waitUntil(collectSaveActionEdits(event.document, { format, organize }))
    }),
    vscode.workspace.onDidOpenTextDocument(document => {
      const editor = vscode.window.visibleTextEditors.find(item => item.document.uri.toString() === document.uri.toString())
      recentFiles.remember(document.uri, editor?.selection?.active)
    }),
    vscode.workspace.onDidSaveTextDocument(document => {
      recentFiles.remember(document.uri, vscode.window.activeTextEditor?.document.uri.toString() === document.uri.toString()
        ? vscode.window.activeTextEditor.selection.active
        : undefined)
      void flushFormatAfterOrganize(document)
    }),
    { dispose: () => { clearTimeout(ideContextTimer); indexController.dispose() } },
    vscode.window.onDidChangeActiveTextEditor(editor => {
      rememberActive(editor)
      companionDecorations.paint(
        (provider?.boot?.companionInterventions || []).filter(item => item?.level === 'critical' || item?.level === 'warning'),
      )
      postCompanionIdeContext()
      void provider?.refreshLiveCompanionInterventions?.()
    }),
    vscode.window.onDidChangeTextEditorSelection(event => {
      if (event.textEditor === vscode.window.activeTextEditor && event.textEditor?.document.uri.scheme === 'file') {
        recentFiles.remember(event.textEditor.document.uri, event.textEditor.selection.active)
      }
      companionLenses.refresh()
      postCompanionIdeContext()
    }),
    vscode.window.onDidChangeActiveTerminal(() => postCompanionIdeContext()),
    vscode.debug.onDidStartDebugSession(() => {
      postCompanionIdeContext()
      void ideObserver?.syncDebugObservation?.()
    }),
    vscode.debug.onDidTerminateDebugSession(() => {
      postCompanionIdeContext()
      void ideObserver?.syncDebugObservation?.()
    }),
    vscode.languages.onDidChangeDiagnostics(() => companionLenses.refresh()),
    vscode.workspace.onDidChangeTextDocument(event => {
      if (event.document === vscode.window.activeTextEditor?.document) postCompanionIdeContext()
    }),
    service,
    provider,
    vscode.tasks.onDidStartTask(event => {
      busyFlags.tasks.add(event.execution)
      paintBusyChrome()
    }),
    vscode.tasks.onDidEndTask(event => {
      busyFlags.tasks.delete(event.execution)
      paintBusyChrome()
    }),
    vscode.tasks.onDidEndTaskProcess(event => {
      void ideObserver.recordTask(event)
    }),
    vscode.window.registerWebviewViewProvider('localAgent.companionChat', {
      resolveWebviewView(view) { provider.resolveCompanionSidebar(view) },
    }, { webviewOptions: { retainContextWhenHidden: true } }),
    ...[
      ['localAgent.terminalTools', 'terminal'],
      ['localAgent.databaseTools', 'database'],
      ['localAgent.sshTools', 'ssh'],
      ['localAgent.gitTools', 'git'],
      ['localAgent.logTools', 'logs'],
    ].map(([id, kind]) => vscode.window.registerWebviewViewProvider(id, {
      resolveWebviewView(view) { provider.resolveToolWindow(view, kind) },
    }, { webviewOptions: { retainContextWhenHidden: true } })),
    vscode.commands.registerCommand('localAgent.openAgentsHubWindow', async args => {
      if (args?.agentsWindow !== true) return
      const folderUri = args?.folderUri ? vscode.Uri.from(args.folderUri) : service.workspaceFolder()?.uri
      if (folderUri?.scheme === 'file') {
        service.setWorkspaceRoot(folderUri)
        const current = vscode.workspace.workspaceFolders || []
        if (current.length !== 1 || current[0].uri.toString() !== folderUri.toString()) {
          vscode.workspace.updateWorkspaceFolders(0, current.length, { uri: folderUri, name: path.basename(folderUri.fsPath) })
          await new Promise(resolve => setTimeout(resolve, 80))
        }
      }
      status.hide()
      projectStatus.hide()
      companionStatus.hide()
      indexStatus.hide()
      problemsStatus.hide?.()
      structureStatus.hide?.()
      for (const command of ['workbench.action.closeSidebar', 'workbench.action.closeAuxiliaryBar', 'workbench.action.closePanel']) {
        try { await vscode.commands.executeCommand(command) } catch { /* optional workbench part */ }
      }
      const tab = String(args?.tab || (provider.onboardingComplete ? 'master' : 'onboarding'))
      provider.enterAgentsWindow(tab, { agentId: args?.agentId, constructorStep: args?.constructorStep })
      revealInfra()
    }),
    vscode.commands.registerCommand('localAgent.quickActions', async () => {
      const selected = await vscode.window.showQuickPick([
        { label: 'Навигация', kind: vscode.QuickPickItemKind.Separator },
        { label: '$(search) Поиск везде', description: 'палитра', detail: 'Файлы, символы (#) и действия (@) в одном окне', command: 'localAgent.searchEverywhere' },
        { label: '$(history) Недавние файлы', description: 'Ctrl+E', detail: 'С позицией курсора, как Recent Files', command: 'localAgent.recentLocations' },
        { label: '$(arrow-left) Назад', description: 'Ctrl+Alt+←', detail: 'Предыдущее место курсора', command: 'workbench.action.navigateBack' },
        { label: '$(breadcrumb) Навигационная строка', description: 'Alt+Home', detail: 'Крошки файла и символов', command: 'localAgent.focusBreadcrumbs' },
        { label: '$(folder-active) Показать в Инвентаре', description: 'Alt+F1', detail: 'Подсветить файл в дереве', command: 'localAgent.revealInInventory' },
        { label: '$(files) Найти в папке', description: 'Shift+Alt+F', detail: 'Поиск по выбранной папке Инвентаря', command: 'localAgent.findInFolder' },
        { label: '$(go-to-file) Перейти к определению', description: 'Ctrl+B', detail: 'Определение символа под курсором', command: 'localAgent.goToDefinition' },
        { label: '$(symbol-class) Перейти к типу', description: 'Ctrl+Shift+B', detail: 'Type Definition', command: 'localAgent.goToTypeDefinition' },
        { label: '$(references) Найти использования', description: 'Alt+F7', detail: 'Все вхождения символа', command: 'localAgent.findUsages' },
        { label: '$(symbol-interface) Найти реализации', description: 'Ctrl+Alt+B', detail: 'Реализации интерфейса / абстракции', command: 'localAgent.findImplementations' },
        { label: '$(files) Найти в проекте', description: 'Quick Search', detail: 'Всплывающее окно Code-OSS; Ctrl+Shift+F открывает своё окно поиска Point', command: 'workbench.action.quickTextSearch' },
        { label: '$(search-fuzzy) Найти в панели', description: 'Alt+3', detail: 'Полные результаты Find in Files в нижней панели', command: 'workbench.action.findInFiles' },
        { label: '$(replace) Заменить в проекте', description: 'Ctrl+Shift+R', detail: 'Replace in Files в нижней панели', command: 'workbench.action.replaceInFiles' },
        { label: 'Действия Point', kind: vscode.QuickPickItemKind.Separator },
        ...POINT_ACTION_REGISTRY.map(action => ({
          label: `$(${action.icons}) ${action.label}`,
          description: action.description,
          detail: action.detail,
          command: action.command,
        })),
        { label: 'Запуск', kind: vscode.QuickPickItemKind.Separator },
        { label: '$(play) Запуск', description: 'Shift+F10', detail: 'Текущая конфигурация, не только launch.json', command: 'localAgent.runWithoutDebug' },
        { label: '$(debug-alt) Отладка', description: 'Shift+F9', detail: 'launch.json или выбор отладочной цели', command: 'localAgent.startDebug' },
        { label: '$(list-unordered) Конфигурации запуска', description: 'Alt+Shift+F10', detail: 'npm, go, make, cargo, tasks', command: 'localAgent.selectRunConfiguration' },
        { label: '$(terminal) Консольный канал', description: 'Alt+F12', detail: 'Терминал Point для квеста/сборки', command: 'localAgent.openTerminal' },
        { label: 'Агенты', kind: vscode.QuickPickItemKind.Separator },
        { label: '$(comment-discussion) Быстрый чат с компаньоном', description: 'Ctrl+Shift+L', detail: 'Компактное окно поверх редактора с текущим контекстом IDE', command: 'localAgent.quickChat' },
        { label: '$(sparkle) Новая задача агенту', description: 'Agents Hub', detail: 'Открыть композер квеста для исполнительного агента', command: 'localAgent.openAgentComposer' },
        { label: '$(comment-unresolved) Спросить компаньона', description: 'Ctrl+Alt+;', detail: 'Логи, код, агенты и квесты — без запуска', command: 'localAgent.askCompanion' },
        { label: '$(inbox) Входящие компаньона', description: 'сигналы IDE', detail: 'Problems, терминал, diff и рекомендации', command: 'localAgent.companionInbox' },
        { label: '$(sparkle) Доска квестов', description: 'Ctrl+Alt+I', detail: 'Гильдия: брифинг и запуск квеста', command: 'localAgent.open' },
        { label: '$(organization) Ростер / Agents Hub', description: 'Ctrl+Alt+Shift+I', detail: 'Наём, карточки и быстрый чат', command: 'localAgent.openRoster' },
        { label: '$(folder-opened) Переключить проект', description: 'Ctrl+Alt+P', detail: 'Активный корень workspace или недавнее окно', command: 'localAgent.switchProject' },
        { label: '$(extensions) Поддержка языков', description: 'Language Center', detail: 'Установить language server по требованию', command: 'localAgent.languageSupport' },
        { label: '$(database) Перестроить индекс', description: 'Карта мира', detail: 'Локальный индекс для search_code', command: 'localAgent.rebuildIndex' },
        { label: '$(server-process) Процессы', description: '', detail: 'Запущенные процессы IDE', command: 'localAgent.openProcesses' },
        { label: '$(output) Хроника ядра', description: '', detail: 'Лог point-core', command: 'localAgent.showCoreChronicle' },
        { label: '$(settings-gear) Настройки IDE', description: 'Ctrl+Alt+S', detail: 'Параметры редактора и Point', command: 'localAgent.openIdeSettings' },
        { label: '$(home) Главная Point', description: 'Ctrl+Shift+1', detail: 'Стартовая страница мира', command: 'localAgent.openHome' },
        { label: '$(book) Летопись Git', description: 'Alt+9', detail: 'Коммиты и ветка текущего мира', command: 'localAgent.openChronicle' },
        { label: '$(plug) Подключения', description: 'Модели и ключи', detail: 'Отдельное окно: сколько угодно подключений, их модели и ключи', command: 'localAgent.openConnections' },
        { label: '$(graph) Статистика проекта', description: 'Расход и бюджет', detail: 'Отдельное окно: токены, стоимость, бюджет текущего мира', command: 'localAgent.openStatistics' },
        { label: '$(server) Docker', description: 'Контейнеры', detail: 'Статус Docker, контейнеры, логи, start/stop', command: 'localAgent.openDocker' },
        { label: '$(database) Базы данных', description: 'SQL', detail: 'SQLite / PostgreSQL / MySQL: профили и запросы', command: 'localAgent.openDatabases' },
        { label: '$(remote) Подключиться к серверу', description: 'SSH', detail: 'Профили, проверка и SSH-терминал', command: 'localAgent.connectServer' },
      ], {
        title: 'Point — быстрые действия (Alt+Shift+A)',
        placeHolder: 'Навигация · Правка · VCS · Запуск · Окно · Агенты',
        matchOnDescription: true,
        matchOnDetail: true,
      })
      if (selected?.command) await vscode.commands.executeCommand(selected.command)
    }),
    vscode.commands.registerCommand('localAgent.openHome', () => home.show()),
    vscode.commands.registerCommand('localAgent.switchProject', async () => {
      // В Чертоге переключение мира — это галерея, а не второй список в
      // выпадайке оболочки: она же и есть домашний экран. В окне IDE привычный
      // QuickPick остаётся.
      if (provider.agentsWindowMode) {
        provider.openProjectGallery()
        return
      }
      await choosePointWorkspace(service, context, afterProjectSwitch, projectSwitchOptions())
    }),
    vscode.commands.registerCommand('localAgent.openProjectGallery', () => {
      if (provider.agentsWindowMode) provider.openProjectGallery()
      else void vscode.commands.executeCommand('localAgent.switchProject')
    }),
    // Открыть мир в редакторе. Решение о том, новое это окно или фокус на
    // существующем, принимает оболочка: `forceNewWindow: false` заставляет её
    // сначала поискать окно на той же папке. Окно Чертога кандидатом не станет —
    // его `openedWorkspace` всегда `agent-sessions.code-workspace`.
    vscode.commands.registerCommand('localAgent.openProjectInIde', async target => {
      const fsPath = typeof target === 'string' ? target : String(target?.path || provider.workspaceFolder()?.uri?.fsPath || '')
      if (!fsPath) {
        await vscode.commands.executeCommand('workbench.action.files.openFolder')
        return
      }
      await projects.setActive(fsPath)
      await vscode.commands.executeCommand('point.openFolderWindow', { folderUri: vscode.Uri.file(fsPath).toJSON(), forceNewWindow: false })
    }),
    vscode.commands.registerCommand('localAgent.quickChat', async query => {
      const message = typeof query === 'string' ? query : typeof query?.query === 'string' ? query.query : ''
      await openCompanionChat(provider, revealInfra, { surface: 'peek', forceSurface: true, message })
    }),
    vscode.commands.registerCommand('localAgent.openAgentComposer', async () => {
      revealInfra()
      const cfg = vscode.workspace.getConfiguration('localAgent')
      const defaultId = String(cfg.get('quickChatDefaultProfileId') || '').trim()
      provider.showWide('quests')
      setTimeout(() => provider.post({ type: 'focusComposer', profileId: defaultId || undefined }), 160)
    }),
    vscode.commands.registerCommand('localAgent.askCompanion', () => openCompanionChat(provider, revealInfra, { surface: 'sidebar', forceSurface: true })),
    vscode.commands.registerCommand('localAgent.companionInbox', () => showCompanionInbox(provider, revealInfra, {
      getFailure: () => ideObserver?.lastFailure(),
      getRun: () => runController?.lastStarted(),
    })),
    vscode.commands.registerCommand('localAgent.askCompanionAbout', async arg => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: await companionAskMessage(arg, companionIdeExtras(() => ideObserver?.lastFailure(), () => runController?.lastStarted())),
    })),
    vscode.commands.registerCommand('localAgent.askCompanionFix', async arg => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: await companionAskMessage({ ...normalizeCompanionArg(arg), intent: 'fix' }, companionIdeExtras(() => ideObserver?.lastFailure(), () => runController?.lastStarted())),
      send: true,
    })),
    vscode.commands.registerCommand('localAgent.askCompanionAboutProblems', async () => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: await companionProblemsMessage(),
      send: true,
    })),
    vscode.commands.registerCommand('localAgent.askCompanionAboutTerminal', async () => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: companionTerminalMessage(() => ideObserver?.lastFailure()),
      send: true,
    })),
    vscode.commands.registerCommand('localAgent.openLogChat', () => provider.showLogChat()),
    vscode.commands.registerCommand('localAgent.askCompanionAboutRun', async () => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: companionRunMessage(() => runController?.current(), () => ideObserver?.lastFailure()),
      send: true,
    })),
    vscode.commands.registerCommand('localAgent.askCompanionAboutDebug', async () => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: companionDebugMessage(() => ideObserver?.lastFailure()),
      send: true,
    })),
    vscode.commands.registerCommand('localAgent.askCompanionAboutDiff', async arg => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: await companionDiffMessage(arg),
      send: true,
    })),
    vscode.commands.registerCommand('localAgent.askCompanionAboutBlame', async arg => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: await companionBlameMessage(arg),
      send: true,
    })),
    vscode.commands.registerCommand('localAgent.askCompanionAboutHistory', async arg => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: await companionHistoryMessage(arg),
      send: true,
    })),
    vscode.commands.registerCommand('localAgent.askCompanionAboutSaved', async arg => openCompanionChat(provider, revealInfra, {
      surface: 'peek',
      message: await companionSavedMessage(arg),
      send: true,
    })),
    vscode.languages.registerCodeLensProvider({ scheme: 'file' }, companionLenses),
    vscode.languages.registerHoverProvider({ scheme: 'file' }, {
      provideHover(document, position) {
        const diagnostics = vscode.languages.getDiagnostics(document.uri).filter(item =>
          item.range.contains(position) &&
          (item.severity === vscode.DiagnosticSeverity.Error || item.severity === vscode.DiagnosticSeverity.Warning)
        )
        if (!diagnostics.length) return
        const arg = {
          uri: document.uri.toString(),
          selection: { start: position, end: position },
          diagnosticMessages: diagnostics.map(item => `L${item.range.start.line + 1}: ${item.message}`),
        }
        const md = new vscode.MarkdownString(undefined, true)
        md.isTrusted = true
        md.appendMarkdown(`**Компаньон** · ${diagnostics[0].message.slice(0, 180)}\n\n`)
        md.appendMarkdown(`${companionCommandMarkdown('localAgent.askCompanionAbout', arg, 'Разобрать')} · ${companionCommandMarkdown('localAgent.askCompanionFix', { ...arg, intent: 'fix' }, 'Подготовить исправление')}`)
        return new vscode.Hover(md, diagnostics[0].range)
      },
    }),
    vscode.languages.registerCodeActionsProvider({ scheme: 'file' }, {
      provideCodeActions(document, range, context) {
        const diagnostics = (context.diagnostics || []).filter(item =>
          item.severity === vscode.DiagnosticSeverity.Error || item.severity === vscode.DiagnosticSeverity.Warning
        )
        if (!diagnostics.length && range.isEmpty) return
        const arg = {
          uri: document.uri.toString(),
          selection: { start: range.start, end: range.end },
          diagnosticMessages: diagnostics.map(item => `L${item.range.start.line + 1}: ${item.message}`),
        }
        const ask = new vscode.CodeAction(
          diagnostics.length ? 'Спросить компаньона' : '✦ Спросить компаньона',
          diagnostics.length ? vscode.CodeActionKind.QuickFix : vscode.CodeActionKind.RefactorRewrite,
        )
        ask.diagnostics = diagnostics
        ask.isPreferred = !diagnostics.length
        ask.command = {
          command: 'localAgent.askCompanionAbout',
          title: ask.title,
          arguments: [arg],
        }
        const actions = [ask]
        if (diagnostics.length) {
          const fix = new vscode.CodeAction('Исправить с компаньоном', vscode.CodeActionKind.QuickFix)
          fix.diagnostics = diagnostics
          fix.isPreferred = diagnostics.some(item => item.severity === vscode.DiagnosticSeverity.Error)
          fix.command = {
            command: 'localAgent.askCompanionFix',
            title: fix.title,
            arguments: [{ ...arg, intent: 'fix' }],
          }
          actions.unshift(fix)
        } else if (!range.isEmpty) {
          const fix = new vscode.CodeAction('Исправить с компаньоном', vscode.CodeActionKind.RefactorRewrite)
          fix.command = {
            command: 'localAgent.askCompanionFix',
            title: fix.title,
            arguments: [{ ...arg, intent: 'fix' }],
          }
          actions.push(fix)
        }
        return actions
      },
    }, { providedCodeActionKinds: [vscode.CodeActionKind.QuickFix, vscode.CodeActionKind.RefactorRewrite] }),
    vscode.commands.registerCommand('localAgent.searchEverywhere', () => searchEverywhere()),
    vscode.commands.registerCommand('localAgent.findAction', () => vscode.commands.executeCommand('workbench.action.showCommands')),
    vscode.commands.registerCommand('localAgent.navigateFile', () => vscode.commands.executeCommand('workbench.action.quickOpen')),
    vscode.commands.registerCommand('localAgent.navigateClass', () => searchEverywhere({ initial: currentEditorWord() ? `#${currentEditorWord()}` : '#' })),
    vscode.commands.registerCommand('localAgent.fileStructure', () => fileStructure()),
    vscode.commands.registerCommand('localAgent.showOutline', async () => {
      // Окно «Структура» приносит оболочка: в Code-OSS список символов живёт
      // вьюшкой внутри Проекта, и Point её там не регистрирует — она стоит
      // своим окном `point.structure`. Откат на всплывающий список нужен для
      // сборки без этой заплаты: пустое нажатие хуже другого окна.
      try {
        await vscode.commands.executeCommand('point.structure')
        return
      } catch { /* окно приходит из оболочки, в старой сборке его нет */ }
      await vscode.commands.executeCommand('localAgent.fileStructure')
    }),
    vscode.commands.registerCommand('localAgent.openIdeSettings', () => openPointIdeSettings()),
    vscode.commands.registerCommand('localAgent.revealInInventory', async () => {
      await vscode.commands.executeCommand('workbench.files.action.showActiveFileInExplorer')
    }),
    vscode.commands.registerCommand('localAgent.findInFolder', resource => findInFolder(resource)),
    vscode.commands.registerCommand('localAgent.coreStatusAction', async () => {
      revealInfra()
      if (!vscode.workspace.isTrusted) {
        await vscode.commands.executeCommand('workbench.trust.manage')
        return
      }
      if (service.state === 'error') {
        const detail = service.lastDetail || 'Локальное ядро завершилось с ошибкой.'
        const choice = await vscode.window.showErrorMessage(`Ядро Point: ${detail}`, 'Хроника ядра', 'Перезапустить', 'Открыть Гильдию')
        if (choice === 'Хроника ядра') await showCoreChronicle(service, output)
        else if (choice === 'Перезапустить') await vscode.commands.executeCommand('localAgent.restartServer')
        else if (choice === 'Открыть Гильдию') await vscode.commands.executeCommand('localAgent.open')
        return
      }
      if (service.state === 'running') {
        await vscode.commands.executeCommand('localAgent.open')
        return
      }
      await vscode.commands.executeCommand('localAgent.startServer')
    }),
    vscode.commands.registerCommand('localAgent.refactorThis', () => refactorThis()),
    vscode.commands.registerCommand('localAgent.renameSymbol', () => renameSymbol()),
    vscode.commands.registerCommand('localAgent.optimizeImports', () => optimizeImports()),
    vscode.commands.registerCommand('localAgent.reformatCode', () => reformatCode()),
    vscode.commands.registerCommand('localAgent.recentLocations', () => recentLocations()),
    vscode.commands.registerCommand('localAgent.nextError', () => nextError()),
    vscode.commands.registerCommand('localAgent.previousError', () => previousError()),
    vscode.commands.registerCommand('localAgent.compareWithClipboard', () => compareWithClipboard()),
    vscode.commands.registerCommand('localAgent.compareWithFile', resource => compareWithFile(resource)),
    vscode.commands.registerCommand('localAgent.copyReference', () => copyReference()),
    vscode.commands.registerCommand('localAgent.newScratch', () => newScratchFile()),
    vscode.commands.registerCommand('localAgent.showFileHistory', () => showFileHistory()),
    vscode.commands.registerCommand('localAgent.toggleZenMode', () => vscode.commands.executeCommand('workbench.action.toggleZenMode')),
    vscode.commands.registerCommand('localAgent.hideAllToolWindows', () => hideAllToolWindows()),
    vscode.commands.registerCommand('localAgent.selectNextOccurrence', () => selectNextOccurrence()),
    vscode.commands.registerCommand('localAgent.runAnything', () => runController.anything()),
    vscode.commands.registerCommand('localAgent.editRunConfigurations', () => editRunConfigurations()),
    vscode.commands.registerCommand('localAgent.selectRunConfiguration', () => runController.select({ runAfter: true })),
    vscode.commands.registerCommand('localAgent.runFile', resource => runController.runFile(resource)),
    vscode.commands.registerCommand('localAgent.toggleBookmark', () => bookmarks.toggle()),
    vscode.commands.registerCommand('localAgent.showBookmarks', () => bookmarks.show()),
    vscode.commands.registerCommand('localAgent.nextBookmark', () => bookmarks.next()),
    vscode.commands.registerCommand('localAgent.previousBookmark', () => bookmarks.previous()),
    vscode.commands.registerCommand('localAgent.vcsCommit', () => vcsCommit()),
    vscode.commands.registerCommand('localAgent.vcsChanges', () => vcsChanges()),
    vscode.commands.registerCommand('localAgent.vcsRollback', () => vcsRollback()),
    vscode.commands.registerCommand('localAgent.vcsShowDiff', () => vcsShowDiff()),
    vscode.commands.registerCommand('localAgent.vcsPush', () => vcsPush()),
    vscode.commands.registerCommand('localAgent.vcsPull', () => vcsPull()),
    vscode.commands.registerCommand('localAgent.gitClone', async () => {
      try {
        await vscode.commands.executeCommand('git.clone')
      } catch (error) {
        await vscode.window.showErrorMessage(`Point: клонирование Git недоступно — ${error instanceof Error ? error.message : String(error)}`)
      }
    }),
    vscode.commands.registerCommand('localAgent.gitAnnotate', () => gitAnnotate()),
    vscode.commands.registerCommand('localAgent.peekUsages', () => peekUsages()),
    vscode.commands.registerCommand('localAgent.parameterHints', () => parameterHints()),
    vscode.commands.registerCommand('localAgent.lastEditLocation', () => vscode.commands.executeCommand('workbench.action.navigateToLastEditLocation')),
    vscode.commands.registerCommand('localAgent.runWithoutDebug', () => runController.run('run')),
    vscode.commands.registerCommand('localAgent.startDebug', () => runController.run('debug')),
    vscode.commands.registerCommand('localAgent.goToDefinition', () => goToDefinition()),
    vscode.commands.registerCommand('localAgent.goToTypeDefinition', () => goToTypeDefinition()),
    vscode.commands.registerCommand('localAgent.focusBreadcrumbs', () => focusBreadcrumbs()),
    vscode.commands.registerCommand('localAgent.moveStatementUp', () => moveStatement(-1)),
    vscode.commands.registerCommand('localAgent.moveStatementDown', () => moveStatement(1)),
    vscode.commands.registerCommand('localAgent.findUsages', () => findSymbolLocations('vscode.executeReferenceProvider', 'Использования', 'usages')),
    vscode.commands.registerCommand('localAgent.findImplementations', () => findSymbolLocations('vscode.executeImplementationProvider', 'Реализации', 'implementations')),
    vscode.commands.registerCommand('localAgent.languageSupport', openLanguageSupport),
    vscode.commands.registerCommand('localAgent.openProcesses', () => vscode.commands.executeCommand('workbench.action.openProcessExplorer')),
    vscode.commands.registerCommand('localAgent.showCoreChronicle', () => showCoreChronicle(service, output)),
    vscode.commands.registerCommand('localAgent.rebuildIndex', async () => {
      revealInfra()
      try {
        await indexController.rebuildNow({ notify: true })
      } catch (error) {
        if (provider) provider.notify(error)
        else void vscode.window.showErrorMessage(`Point: ${describeCoreFailure(error)}`)
      }
    }),
    vscode.commands.registerCommand('localAgent.showIndexStatus', () => { revealInfra(); void indexController.refresh() }),
    vscode.commands.registerCommand('localAgent.open', () => { revealInfra(); provider.showWide(provider.onboardingComplete ? 'master' : 'onboarding') }),
    vscode.commands.registerCommand('localAgent.openRoster', () => { revealInfra(); provider.showWide('settings') }),
    vscode.commands.registerCommand('localAgent.openChronicle', () => chronicle.show()),
    vscode.commands.registerCommand('localAgent.openConnections', () => provider.showConnections()),
    vscode.commands.registerCommand('localAgent.openStatistics', () => { revealInfra(); provider.showStatistics() }),
    vscode.commands.registerCommand('localAgent.openDocker', () => { revealInfra(); provider.showDocker() }),
    vscode.commands.registerCommand('localAgent.openDatabases', () => { revealInfra(); provider.showWide('databases') }),
    vscode.commands.registerCommand('localAgent.connectServer', async () => {
      revealInfra()
      try {
        await manageSSHServers(service, context, provider)
      } catch (error) {
        if (provider) provider.notify(error)
        else void vscode.window.showErrorMessage(`Point: ${error instanceof Error ? error.message : String(error)}`)
      }
    }),
    vscode.commands.registerCommand('localAgent.startServer', async () => { revealInfra(); try { await service.start(); await provider.refresh(); await indexController.refresh(); void indexController.ensureReady(); provider.showWide(provider.onboardingComplete ? 'master' : 'onboarding') } catch (error) { provider.notify(error) } }),
    vscode.commands.registerCommand('localAgent.stopServer', async () => { await service.stop(); indexController.markCoreStopped() }),
    vscode.commands.registerCommand('localAgent.restartServer', async () => { revealInfra(); try { await service.stop(); await service.start(); await provider.refresh(); await indexController.refresh(); void indexController.ensureReady() } catch (error) { provider.notify(error) } }),
    vscode.commands.registerCommand('localAgent.openSettings', () => { revealInfra(); provider.showWide('settings') }),
    vscode.commands.registerCommand('localAgent.openTerminal', async () => {
      service.hostLog('info', '[terminal] Alt+F12 requested')
      try {
        const terminal = await openConsoleChannel()
        service.hostLog('info', `[terminal] editor channel ready name=${terminal?.name || '-'}`)
        return terminal
      } catch (error) {
        const detail = error instanceof Error ? (error.stack || error.message) : String(error)
        service.hostLog('error', `[terminal] editor channel failed: ${detail}`)
        void vscode.window.showErrorMessage(`Point: не удалось открыть консольный канал — ${error instanceof Error ? error.message : String(error)}`)
        throw error
      }
    }),
    vscode.commands.registerCommand('localAgent.newConsoleChannel', chooseConsoleChannel),
    vscode.commands.registerCommand('localAgent.showWalkthrough', () => vscode.commands.executeCommand('workbench.action.openWalkthrough')),
    vscode.workspace.onDidChangeWorkspaceFolders(async () => {
      workspaceFileCache.invalidate()
      // Переключение мира в Чертоге само меняет папки, и его продолжение уже
      // расписано в `afterProjectSwitch`. Без этого выхода тот же самый сброс
      // шёл бы вторым заходом и гасил бы только что поднятое ядро.
      if (provider.projectSwitchInFlight) return
      const retained = service.workspaceRootOverride?.fsPath
        ? workspaceFolderByPath(service.workspaceRootOverride.fsPath)
        : undefined
      if (retained) {
        service.setWorkspaceRoot(retained.uri)
        await context.workspaceState.update(POINT_WORKSPACE_ROOT_KEY, retained.uri.fsPath)
        updateProjectStatus()
        home.refresh()
        provider.postState(true)
        if (provider.hubVisible() && vscode.workspace.isTrusted) {
          if (vscode.workspace.getConfiguration('localAgent').get('autoStart', true)) provider.scheduleAutoStart()
          else await provider.refresh()
        }
        return
      }
      await service.stop()
      const selected = restorePointWorkspaceRoot(service, context, {
        agentsWindowMode: provider.agentsWindowMode || isPointHubWindow(),
        activeProjectPath: projects.activePath(),
      })
      await context.workspaceState.update(POINT_WORKSPACE_ROOT_KEY, selected?.uri?.fsPath || '')
      updateProjectStatus()
      home.refresh()
      provider.resetRunState()
      provider.boot = undefined
      indexController.markCoreStopped()
      provider.postState(true)
      if (provider.hubVisible() && selected && vscode.workspace.isTrusted) {
        if (vscode.workspace.getConfiguration('localAgent').get('autoStart', true)) provider.scheduleAutoStart()
        else await provider.refresh()
      }
    }),
    vscode.workspace.onDidGrantWorkspaceTrust(() => {
      status.text = service.state === 'running' ? '$(server-process) Ядро' : '$(server-process) Ядро'
      status.tooltip = service.coreStatusTooltip()
      status.backgroundColor = undefined
      void provider.onWorkspaceTrustGranted()
      // Index ensureReady follows core start via onStatus when autoStart runs.
    }),
  )
  // Расширение git просыпается по открытию встроенной «Летописи», а она у нас
  // скрыта: без своего толчка чип ветки в заголовке и строка состояния молчали
  // до первого открытия панели Git. Будим отложенно, чтобы не удлинять запуск.
  if (localWorkspaceFolders().length) {
    const gitWarmup = setTimeout(() => { void provider.gitContext().catch(() => {}) }, 1500)
    context.subscriptions.push({ dispose: () => clearTimeout(gitWarmup) })
  }
  // Cold activation leaves parser/JIT objects behind in both JS runtimes. Once
  // startup is idle, reuse the normal lifecycle collector instead of retaining
  // that one-time heap until V8 happens to run a major collection.
  const startupGarbage = setTimeout(() => {
    if (provider.hubVisible()) return
    collectExtensionGarbage()
    void vscode.commands.executeCommand('point.collectHubGarbage').then(() => {}, () => {})
  }, 1800)
  context.subscriptions.push({ dispose: () => clearTimeout(startupGarbage) })
  void projects.migrateFrom(String(context.workspaceState.get(POINT_WORKSPACE_ROOT_KEY, '') || ''))
  // Сначала кадр, потом мир. Список чатов — домашний экран, и он обязан
  // появиться раньше, чем поднимется ядро последнего мира.
  if (isPointHubWindow()) {
    const resumeWorld = setTimeout(() => {
      void resumeLastPointWorld(service, context, {
        registry: projects,
        switchToProject,
        hostLog: (level, message) => service.hostLog(level, message),
      }).catch(error => service.hostLog('warn', `[world] возврат не состоялся: ${error?.message || error}`))
    }, 400)
    resumeWorld.unref?.()
    context.subscriptions.push({ dispose: () => clearTimeout(resumeWorld) })
  }
  // Бесхозные ядра остаются и после жёсткого закрытия окна: `detach()` снимает
  // аренду, а процесс на Windows запущен отвязанным и переживает хост. Убирает
  // их только Чертог — так два окна не гонятся за одним pid.
  if (isPointHubWindow()) {
    const warmSweep = setTimeout(() => { void warmPool.reap().catch(() => {}) }, 30_000)
    warmSweep.unref?.()
    context.subscriptions.push({ dispose: () => clearTimeout(warmSweep) })
  }
  output.appendLine(`[activation] ready duration_ms=${Date.now() - activationStartedAt}`)
  // Defer core start + index rebuild until Hub open / autoStart / explicit command.
}

async function deactivate() {
  const warmPool = activeWarmPool
  activeWarmPool = undefined
  if (activeView) activeView.dispose()
  if (activeService) {
    activeService.detach()
    activeService = undefined
  }
  // Чертог уходит домой последним и уносит бесхозные ядра с собой. Ядра,
  // арендованные открытыми окнами IDE, инвариант уборщика не трогает. Убийство
  // не ждём: бюджет `deactivate` короткий, а снятого дескриптора уже достаточно,
  // чтобы следующее окно не подцепилось к умирающему ядру.
  if (warmPool) {
    try { await warmPool.reap(0, false) } catch { /* окно всё равно закрывается */ }
  }
}

module.exports = { activate, deactivate, __test: { BackendService, AgentViewProvider, sharedPointStoragePath, normalizedWorkspaceRoot, processIsAlive, upsertById, removeById, formatIndexStatus, describeCoreFailure, isIndexNoiseUri, indexAutoConfig, indexScheduleWaitMs, applyIndexDirty, createProjectIndexController, createWorkspaceFileCache, readRunAnythingManifest, parseJsonc, makefileTargets, buildRunConfigurations, pickDefaultRunConfiguration, cheapStateSignature, openCompanionChat, companionWorkspaceRelativePath, workspaceFileUri, workspaceRelativePathIfInside, restorePointWorkspaceRoot, choosePointWorkspace, switchPointWorkspaceInPlace, resumeLastPointWorld, formatCompanionFailure, formatCompanionChatError, liveCompanionFocus, createCompanionCodeLensProvider, parseDocumentOutline, mergeRecentFiles, formatCopyReference, outlineKindIcon, resolveOutlineSymbolAt, formatOutlineBreadcrumb, pathIsUnder, pickGitRepository, pathRelativeToRoot, formatVcsError, gitListsState, normalizeSSHRemotePath, sshRemotePathParent, sshRemotePathJoin, sshRemotePickerEntries, browseSSHRemotePath, openSSHRemotePreview, createConsoleChannel, openConsoleChannel } }
