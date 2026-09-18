// Реестр миров Чертога.
//
// Галерея проектов обязана рисоваться раньше любого ядра: Чертог открывается
// первым делом, когда ни одного мира ещё не выбрано, и спросить о списке не у
// кого. Поэтому источник здесь хостовый — недавние окна оболочки плюс своя
// запись в globalState. Таблица `workspaces` в общей базе знает то же самое, но
// отдаёт её ядро, привязанное к миру, и на холодном старте это замкнутый круг.
//
// globalState берётся не случайно: заплата оверлея сажает окно Чертога на
// профиль по умолчанию, поэтому запись видна и окнам IDE. `workspaceState`
// (`point.workspaceRoot`) остаётся при своём — окну IDE с несколькими корнями
// по-прежнему нужен собственный выбор.
const crypto = require('node:crypto')
const fsp = require('node:fs/promises')

const PROJECTS_KEY = 'point.projects.v1'
const HUB_WORKSPACE_PATTERN = /agent-sessions\.code-workspace$/i
const STAT_TIMEOUT_MS = 300
const STAT_CONCURRENCY = 8

function emptyState() {
  return { version: 1, active: '', entries: {} }
}

async function mapWithConcurrency(items, limit, worker) {
  const results = new Array(items.length)
  let cursor = 0
  const runners = new Array(Math.min(limit, items.length)).fill(0).map(async () => {
    while (cursor < items.length) {
      const index = cursor
      cursor += 1
      results[index] = await worker(items[index], index)
    }
  })
  await Promise.all(runners)
  return results
}

function withTimeout(promise, timeoutMs) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(Object.assign(new Error('timeout'), { name: 'TimeoutError' })), timeoutMs)
    promise.then(value => { clearTimeout(timer); resolve(value) }, error => { clearTimeout(timer); reject(error) })
  })
}

function createProjectRegistry(dependencies) {
  const {
    vscode,
    path,
    context,
    normalizedWorkspaceRoot,
    recentPointProjects,
    runtimeDirPath,
    coreStateByKey,
    hostLog,
  } = dependencies

  function workspaceKey(fsPath) {
    return crypto.createHash('sha256').update(normalizedWorkspaceRoot(fsPath)).digest('hex').slice(0, 24)
  }

  function read() {
    const stored = context.globalState.get(PROJECTS_KEY)
    if (!stored || typeof stored !== 'object' || !stored.entries || typeof stored.entries !== 'object') return emptyState()
    return { version: 1, ...stored, entries: { ...stored.entries } }
  }

  async function write(next) {
    await context.globalState.update(PROJECTS_KEY, next)
    return next
  }

  // Разовый перенос: до реестра активный корень знал только `workspaceState`
  // текущего окна. Без засева первая галерея оказалась бы пустой у человека,
  // который проектом пользуется каждый день.
  async function migrateFrom(workspaceRootPath) {
    if (context.globalState.get(PROJECTS_KEY)) return
    const state = emptyState()
    if (workspaceRootPath) {
      const key = normalizedWorkspaceRoot(workspaceRootPath)
      state.active = workspaceRootPath
      state.entries[key] = {
        path: workspaceRootPath,
        name: path.basename(workspaceRootPath),
        pinned: false,
        lastOpenedAt: Date.now(),
        lastTab: '',
        openCount: 1,
      }
    }
    await write(state)
  }

  function activePath() {
    return String(read().active || '')
  }

  async function setActive(fsPath) {
    const state = read()
    state.active = fsPath ? String(fsPath) : ''
    return write(state)
  }

  async function markOpened(fsPath, options = {}) {
    if (!fsPath) return read()
    const state = read()
    const key = normalizedWorkspaceRoot(fsPath)
    const previous = state.entries[key] || {}
    state.entries[key] = {
      path: String(fsPath),
      name: options.name || previous.name || path.basename(String(fsPath)),
      pinned: previous.pinned === true,
      lastOpenedAt: Date.now(),
      lastTab: options.lastTab || previous.lastTab || '',
      openCount: Number(previous.openCount || 0) + 1,
    }
    state.active = String(fsPath)
    return write(state)
  }

  // Вкладка запоминается на мир, а не на панель: возврат в проект должен
  // возвращать и место, на котором его оставили.
  async function rememberTab(fsPath, tab) {
    if (!fsPath || !tab) return
    const state = read()
    const key = normalizedWorkspaceRoot(fsPath)
    const entry = state.entries[key]
    if (!entry || entry.lastTab === tab) return
    state.entries[key] = { ...entry, lastTab: String(tab) }
    await write(state)
  }

  function lastTabFor(fsPath) {
    if (!fsPath) return ''
    return String(read().entries[normalizedWorkspaceRoot(fsPath)]?.lastTab || '')
  }

  async function setPinned(fsPath, pinned) {
    const state = read()
    const key = normalizedWorkspaceRoot(fsPath)
    const entry = state.entries[key]
    if (entry) state.entries[key] = { ...entry, pinned: pinned === true }
    else if (pinned === true) {
      state.entries[key] = {
        path: String(fsPath),
        name: path.basename(String(fsPath)),
        pinned: true,
        lastOpenedAt: 0,
        lastTab: '',
        openCount: 0,
      }
    }
    return write(state)
  }

  async function forget(fsPath) {
    const state = read()
    const key = normalizedWorkspaceRoot(fsPath)
    delete state.entries[key]
    if (normalizedWorkspaceRoot(state.active) === key) state.active = ''
    await write(state)
    await dropFromRecents(fsPath)
    return state
  }

  async function dropFromRecents(fsPath) {
    try {
      await vscode.commands.executeCommand('vscode.removeFromRecentlyOpened', vscode.Uri.file(String(fsPath)))
    } catch (error) {
      hostLog?.('warn', `[projects] не удалось снять из недавних: ${error?.message || error}`)
    }
  }

  // Ветка читается из `.git/HEAD` напрямую. Форкать git на каждый проект в
  // списке нельзя: галерея перерисовывается на каждое сообщение состояния, и
  // десяток процессов на кадр съел бы ровно ту скорость, ради которой всё это.
  async function gitBranch(fsPath) {
    try {
      const gitPath = path.join(fsPath, '.git')
      const stat = await withTimeout(fsp.stat(gitPath), STAT_TIMEOUT_MS)
      if (!stat.isDirectory()) return ''
      const head = await withTimeout(fsp.readFile(path.join(gitPath, 'HEAD'), 'utf8'), STAT_TIMEOUT_MS)
      const ref = /^ref:\s*refs\/heads\/(.+)$/m.exec(String(head).trim())
      return ref ? ref[1].trim() : ''
    } catch {
      return ''
    }
  }

  function isHubWorkspace(fsPath) {
    return HUB_WORKSPACE_PATTERN.test(String(fsPath || ''))
  }

  async function list(options = {}) {
    const limit = Number(options.limit) || 40
    const state = read()
    const candidates = new Map()
    for (const [key, entry] of Object.entries(state.entries)) {
      if (!entry?.path || isHubWorkspace(entry.path)) continue
      candidates.set(key, {
        path: entry.path,
        name: entry.name || path.basename(entry.path),
        pinned: entry.pinned === true,
        lastOpenedAt: Number(entry.lastOpenedAt || 0),
        lastTab: entry.lastTab || '',
        openCount: Number(entry.openCount || 0),
      })
    }
    // Недавние оболочки — окна IDE, о которых Чертог мог и не знать. Команда
    // приватная, поэтому обёрнута ещё на вызывающей стороне: если её не отдадут,
    // остаётся полноценный список из globalState, а не пустой экран.
    const recents = await recentPointProjects(limit)
    recents.forEach(uri => {
      if (!uri?.fsPath || isHubWorkspace(uri.fsPath)) return
      const key = normalizedWorkspaceRoot(uri.fsPath)
      if (candidates.has(key)) return
      candidates.set(key, {
        path: uri.fsPath,
        name: path.basename(uri.fsPath),
        pinned: false,
        lastOpenedAt: 0,
        lastTab: '',
        openCount: 0,
      })
    })

    const items = [...candidates.values()]
    const checked = await mapWithConcurrency(items, STAT_CONCURRENCY, async item => {
      try {
        const stat = await withTimeout(fsp.stat(item.path), STAT_TIMEOUT_MS)
        return { item, exists: stat.isDirectory(), slow: false }
      } catch (error) {
        // Таймаут — не приговор: сетевой диск может просто спать. Забываем
        // только то, что файловая система назвала отсутствующим.
        if (error?.name === 'TimeoutError') return { item, exists: true, slow: true }
        return { item, exists: false, slow: false }
      }
    })

    const missing = checked.filter(result => !result.exists).map(result => result.item.path)
    if (missing.length) {
      const next = read()
      for (const fsPath of missing) delete next.entries[normalizedWorkspaceRoot(fsPath)]
      if (missing.some(fsPath => normalizedWorkspaceRoot(fsPath) === normalizedWorkspaceRoot(next.active))) next.active = ''
      await write(next)
      for (const fsPath of missing) await dropFromRecents(fsPath)
    }

    const alive = checked.filter(result => result.exists)
    const decorated = await mapWithConcurrency(alive, STAT_CONCURRENCY, async result => ({
      path: result.item.path,
      name: result.item.name,
      parent: path.dirname(result.item.path),
      pinned: result.item.pinned,
      lastOpenedAt: result.item.lastOpenedAt,
      lastTab: result.item.lastTab,
      openCount: result.item.openCount,
      slow: result.slow,
      branch: result.slow ? '' : await gitBranch(result.item.path),
      // Отпечаток — тот же, что считает ядро для каталога чатов. По нему
      // интерфейс сшивает чужой мир из каталога со своей записью реестра и
      // берёт путь отсюда: ядро чужие пути наружу не отдаёт.
      hash: workspaceKey(result.item.path),
      core: runtimeDirPath ? coreStateByKey(runtimeDirPath, workspaceKey(result.item.path)) : 'idle',
    }))

    decorated.sort((left, right) => {
      if (left.pinned !== right.pinned) return left.pinned ? -1 : 1
      if (left.lastOpenedAt !== right.lastOpenedAt) return right.lastOpenedAt - left.lastOpenedAt
      return left.name.localeCompare(right.name, 'ru')
    })
    return decorated.slice(0, limit)
  }

  return {
    PROJECTS_KEY,
    workspaceKey,
    read,
    migrateFrom,
    activePath,
    setActive,
    markOpened,
    rememberTab,
    lastTabFor,
    setPinned,
    forget,
    list,
  }
}

module.exports = { createProjectRegistry, PROJECTS_KEY }
