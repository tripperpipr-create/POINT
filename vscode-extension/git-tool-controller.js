const { createGitWorkbenchTools } = require('./git-workbench-controller')
// Git и снимки окон инструментов.
//
// Самый крупный связный кусок `AgentViewProvider` и самый далёкий от его
// работы: класс отвечает за жизненный цикл вебвью, а здесь — чтение
// репозитория, списки изменений, полка, цели push и снимок окна инструментов.
// Половина темы уже жила отдельно: разбор нажатий держит `infra-controller.js`.
//
// Приём тот же, что у `core-log.js`: провайдер приходит первым доводом, в
// классе остаётся строка-переходник. Публичная поверхность класса не меняется,
// поэтому вызовы `this.<метод>()` из остального `extension.js` трогать не
// пришлось.

function createGitTools({ GIT_LISTS_KEY, dispatchGitAction, normalizedWorkspaceRoot, path, runGit, vscode }) {
  const workbench = createGitWorkbenchTools({ vscode, path })
  async function gitContext(provider, rootHint = '') {
    const folder = provider.workspaceFolder()
    const gitExt = vscode.extensions.getExtension('vscode.git')
    const api = gitExt?.isActive ? gitExt.exports?.getAPI?.(1) : (await gitExt?.activate())?.getAPI?.(1)
    const repositories = Array.isArray(api?.repositories) ? api.repositories : []
    const requestedRoot = String(rootHint || provider.gitSelectedRoot || '')
    const requested = requestedRoot
      ? repositories.find(item => normalizedWorkspaceRoot(item?.rootUri?.fsPath) === normalizedWorkspaceRoot(requestedRoot))
      : undefined
    const repo = requested || pickGitRepository(repositories, folder?.uri || vscode.window.activeTextEditor?.document?.uri)
    if (repo?.rootUri?.fsPath) {
      provider.gitSelectedRoot = repo.rootUri.fsPath
      provider.bindGitRepository(repo)
    }
    provider.bindGitApi(api)
    return { api, folder, repositories, repo }
  }
  function bindGitApi(provider, api) {
    if (!api || provider.gitApiListener) return
    const refresh = () => {
      if(provider.toolWindows.get('git-workspace')?.visible) void provider.toolWindows.get('git-workspace').webview.postMessage({type:'gitWorkspaceChanged'})
      if (!provider.toolWindows.has('git')) return
      void provider.refreshToolWindowSnapshot('git')
    }
    const open = api.onDidOpenRepository?.(refresh)
    const close = api.onDidCloseRepository?.(refresh)
    if (!open && !close) return
    provider.gitApiListener = { dispose: () => { open?.dispose?.(); close?.dispose?.() } }
    provider.context.subscriptions.push(provider.gitApiListener)
  }
  function bindGitRepository(provider, repo) {
    const key = String(repo?.rootUri?.toString?.() || '')
    if (!key || key === provider.gitRepositoryKey) return
    provider.gitRepositoryListener?.dispose?.()
    provider.gitRepositoryKey = key
    provider.gitRepositoryListener = repo.state?.onDidChange?.(() => {
      if(provider.toolWindows.get('git-workspace')?.visible) void provider.toolWindows.get('git-workspace').webview.postMessage({type:'gitWorkspaceChanged'})
      if (!provider.toolWindows.has('git')) return
      if (provider.gitRefreshTimer) clearTimeout(provider.gitRefreshTimer)
      provider.gitRefreshTimer = setTimeout(() => {
        provider.gitRefreshTimer = undefined
        void provider.refreshToolWindowSnapshot('git')
      }, 180)
    })
  }
  function gitChanges(provider, repo) {
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
  function gitLists(provider, root) {
    const saved = provider.context.workspaceState.get(GIT_LISTS_KEY) || {}
    return saved[normalizedWorkspaceRoot(root)]
  }
  async function saveGitLists(provider, root, value) {
    const saved = { ...(provider.context.workspaceState.get(GIT_LISTS_KEY) || {}) }
    saved[normalizedWorkspaceRoot(root)] = value
    await provider.context.workspaceState.update(GIT_LISTS_KEY, saved)
    return value
  }
  async function syncGitLists(provider, root, changes) {
    const saved = provider.gitLists(root)
    const next = gitListsState(saved, changes)
    if (JSON.stringify(saved || null) !== JSON.stringify(next)) await provider.saveGitLists(root, next)
    return next
  }
  function paintGitViewChrome(provider, { available, branch = '', count = 0 }) {
    const view = provider.toolWindows.get('git')
    if (!view) return
    view.description = available ? (branch || 'без ветки') : ''
    view.badge = available && count ? { value: count, tooltip: `Изменённых файлов: ${count}` } : undefined
  }
  function postToolWindow(provider, kind, message) {
    void provider.toolWindows.get(kind)?.webview.postMessage(message)
  }
  async function refreshToolWindowSnapshot(provider, kind, extra = {}) {
    if (!provider.toolWindows.has(kind)) return undefined
    const snapshot = await provider.toolWindowSnapshot(kind)
    provider.postToolWindow(kind, { type: 'toolWindowState', snapshot, ...extra })
    return snapshot
  }
  async function toolWindowSnapshot(provider, kind) {
    if (kind === 'logs') return { kind, ...provider.service.readLogSnapshot() }
    if (kind === 'terminal') {
      return {
        kind,
        terminals: vscode.window.terminals.map((terminal, index) => ({
          id: index, name: terminal.name, active: terminal === vscode.window.activeTerminal,
          exitStatus: terminal.exitStatus?.code,
        })),
        run: provider.ideContext?.run || '',
        failure: provider.ideContext?.failure || '',
      }
    }
    if (kind === 'git') {
      const { folder, repositories, repo } = await provider.gitContext()
      if (!repo?.state) {
        provider.paintGitViewChrome({ available: false })
        return { kind, available: false, project: folder?.name || '',
          repositories: repositories.map(item => ({ root: item.rootUri?.fsPath || '' })) }
      }
      return workbench.gitWorkbenchSnapshot(provider, repo)
    }
    return { kind }
  }
  async function gitNumstat(provider, root) {
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
  async function gitStashes(provider, root) {
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
  function gitPushTargets(provider, repo, upstream) {
    const remotes = (repo.state.refs || [])
      .filter(item => Number(item?.type) === 1 && item?.name)
      .map(item => String(item.name))
    const unique = [...new Set([upstream, ...remotes].filter(Boolean))].slice(0, 12)
    return unique.map(name => ({ name, tracked: name === upstream }))
  }
  async function gitFileCommand(provider, repo, method, uris) {
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
  async function handleGitAction(provider, message) {
    return dispatchGitAction.call(this, message)
  }

  return {
    runGitWorkbenchAction: (provider, message) => workbench.runGitWorkbenchAction(provider, message),
    gitContext, bindGitApi, bindGitRepository, gitChanges,
    gitLists, saveGitLists, syncGitLists, paintGitViewChrome,
    postToolWindow, refreshToolWindowSnapshot, toolWindowSnapshot, gitNumstat,
    gitStashes, gitPushTargets, gitFileCommand,
    handleGitAction,
  }
}

module.exports = { createGitTools }
