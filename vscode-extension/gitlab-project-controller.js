// Проекты GitLab со стороны хоста: список в окне GitLab, карточка проекта
// вкладкой редактора, клон на эту машину, файл и diff коммита документами IDE.
//
// Данные дают маршруты ядра /api/integrations/gitlab/{projects,project,commits,
// commit,branches,tree,file}. Хост их не толкует и отвечает только той
// поверхности, что спросила. Клон делает штатный git.clone IDE: ключи SSH,
// менеджер учётных данных и askpass — те же, что у владельца в терминале.
// Адрес клона вебвью не присылает: хост берёт его из ответа ядра, где он уже
// сверен с подключённым GitLab.

const fs = require('fs')
const path = require('path')
const { SCHEME } = require('./gitlab-controller')

const GITLAB_TIMEOUT_MS = 130_000
const CLONES_KEY = 'point.gitlab.clones', PREFS_KEY = 'point.gitlab.prefs'
const PROJECT_PATTERN = /^[A-Za-z0-9_.][A-Za-z0-9_./-]{0,254}$/
const REF_PATTERN = /^[A-Za-z0-9_.][A-Za-z0-9_./+@-]{0,254}$/
const SHA_PATTERN = /^[0-9a-f]{7,64}$/
const ACTIONS = new Set(['projects', 'project', 'openProject', 'commits', 'commit', 'branches', 'tree', 'readme', 'openFile',
  'openCommitDiff', 'clone', 'openClone', 'copyUrl', 'prefs', 'savePrefs'])
// Избранное и фильтр окна «Проекты» по серверу GitLab: только пути и флаги.
const cleanPrefs = value => ({ favorites: [...new Set((Array.isArray(value?.favorites) ? value.favorites : []).map(String).filter(item => PROJECT_PATTERN.test(item)))].slice(0, 500), groups: [...new Set((Array.isArray(value?.groups) ? value.groups : []).map(String).filter(item => PROJECT_PATTERN.test(item)))].slice(0, 100), groupBy: value?.groupBy === 'access' ? 'access' : 'ns', favOnly: value?.favOnly === true, mrFav: value?.mrFav === true })

function query(params) {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '' && value !== 0) search.set(key, String(value))
  }
  const text = search.toString()
  return text ? `?${text}` : ''
}

function projectPath(value) {
  const text = String(value || '')
  if (!PROJECT_PATTERN.test(text) || text.includes('..')) throw new Error('Неверный путь проекта GitLab.')
  return text
}

function ref(value) {
  const text = String(value || '')
  if (text && !REF_PATTERN.test(text)) throw new Error('Неверное имя ветки.')
  return text
}

function sha(value, { optional = false } = {}) {
  const text = String(value || '')
  if (optional && !text) return ''
  if (!SHA_PATTERN.test(text)) throw new Error('Неверный коммит.')
  return text
}

function filePath(value, { optional = false } = {}) {
  const text = String(value || '').replace(/^\/+|\/+$/g, '')
  if (optional && !text) return ''
  if (!text || text.length > 1000 || /[\x00-\x1f]/.test(text) || text.split('/').includes('..')) throw new Error('Неверный путь файла.')
  return text
}

// findClone — папка под parent, чей .git/config знает этот адрес: так клон
// находится и после перезагрузки окна, когда git.clone открыл его сам.
function findClone(parent, url) {
  let entries = []
  try { entries = fs.readdirSync(parent, { withFileTypes: true }) } catch { return '' }
  for (const entry of entries) {
    if (!entry.isDirectory()) continue
    const folder = path.join(parent, entry.name)
    try {
      const config = fs.readFileSync(path.join(folder, '.git', 'config'), 'utf8')
      if (config.split(/\r?\n/).some(line => line.trim() === `url = ${url}`)) return folder
    } catch { /* не репозиторий */ }
  }
  return ''
}

function createGitLabProjectController({ vscode, provider, request, unlock }) {
  const panels = new Map()
  const details = new Map()
  // Откуда разрешено открывать ссылки карточки: только свой GitLab.
  let origin = ''

  async function call(route) {
    await unlock()
    return request(route, { timeoutMs: GITLAB_TIMEOUT_MS })
  }

  function deliver(surface, message) {
    const target = String(surface || '')
    if (target === 'tool' || target === 'tool-all' || target === 'tool-every') {
      for (const key of { tool: ['gitlab', 'gitlab-panel'], 'tool-all': ['gitlab-global'] }[target] || ['gitlab', 'gitlab-panel', 'gitlab-global']) void provider.toolWindows.get(key)?.webview.postMessage(message)
      return
    }
    if (target.startsWith('project:')) {
      void panels.get(target.slice(8))?.webview.postMessage(message)
      return
    }
    provider.post(message)
  }

  function announceChange() {
    for (const panel of panels.values()) void panel.webview.postMessage({ type: 'gitlabChanged' })
  }

  const clones = () => provider.context.globalState?.get?.(CLONES_KEY) || {}
  async function rememberClone(project, record) {
    await provider.context.globalState?.update?.(CLONES_KEY, { ...clones(), [project]: record })
  }

  // Локальная копия: записанная клоном и всё ещё на месте. Незавершённый клон
  // (окно перезагрузилось, пока git.clone открывал папку) дозаписывается здесь.
  async function cloneInfo(project) {
    const record = clones()[project]
    if (!record) return { exists: false }
    let folder = record.path
    if (!folder && record.parent && record.url) {
      folder = findClone(record.parent, record.url)
      if (folder) await rememberClone(project, { path: folder, url: record.url })
    }
    return folder && fs.existsSync(path.join(folder, '.git')) ? { exists: true, path: folder } : { exists: false }
  }

  function openProject(message) {
    const project = projectPath(message.project)
    const existing = panels.get(project)
    if (existing) return existing.reveal(vscode.ViewColumn.Active)
    const title = String(message.name || project.split('/').pop())
    const panel = vscode.window.createWebviewPanel('point.gitlabProject', title.length > 40 ? `${title.slice(0, 39)}…` : title, vscode.ViewColumn.Active, {
      enableScripts: true,
      retainContextWhenHidden: true,
      localResourceRoots: [vscode.Uri.joinPath(provider.context.extensionUri, 'media')],
    })
    const attribute = project.replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char])
    panel.webview.html = provider.html(panel.webview, 'gitlab-project')
      .replace('data-layout="gitlab-project"', `data-layout="gitlab-project" data-gitlab-project="${attribute}"`)
    panel.webview.onDidReceiveMessage(incoming => provider.handleMessage(incoming), undefined, provider.context.subscriptions)
    // Как карточка MR: в ряду окон инструментов, чтобы получать состояние ядра.
    const surfaceKey = `gitlab-project:${project}`
    provider.toolWindows.set(surfaceKey, panel)
    panels.set(project, panel)
    panel.onDidDispose(() => {
      panels.delete(project)
      if (provider.toolWindows.get(surfaceKey) === panel) provider.toolWindows.delete(surfaceKey)
      provider.toolWindowStateSignatures?.delete(surfaceKey)
    }, undefined, provider.context.subscriptions)
  }

  function documentUri(label, spec) {
    const name = String(label || 'file').split('/').pop() || 'file'
    return vscode.Uri.from({ scheme: SCHEME, path: `/file/${name}`, query: JSON.stringify(spec) })
  }

  async function openFile(message) {
    const project = projectPath(message.project)
    const file = filePath(message.path)
    const at = ref(message.ref) || sha(message.ref)
    const document = await vscode.workspace.openTextDocument(documentUri(file, { project, path: file, ref: at }))
    await vscode.window.showTextDocument(document, { preview: true })
  }

  // Diff файла коммита: слева первый родитель, справа сам коммит. У нового
  // файла нет левой стороны, у удалённого — правой: документ без ref пуст.
  async function openCommitDiff(message) {
    const project = projectPath(message.project)
    const head = sha(message.sha)
    const parent = sha(message.parent, { optional: true })
    const file = filePath(message.path)
    const old = filePath(message.oldPath || file)
    const left = documentUri(old, { project, path: old, ref: message.newFile || !parent ? '' : parent })
    const right = documentUri(file, { project, path: file, ref: message.deleted ? '' : head })
    await vscode.commands.executeCommand('vscode.diff', left, right, `${file.split('/').pop()} · ${head.slice(0, 8)}`, { preview: true })
  }

  async function clone(message, surface) {
    const project = projectPath(message.project)
    const kind = message.kind === 'https' ? 'https' : 'ssh'
    const response = await call(`/api/integrations/gitlab/project${query({ project })}`)
    if (response?.state !== 'ok') return deliver(surface, { type: 'gitlabClone', state: 'error', error: response?.problem || 'GitLab не ответил' })
    const url = kind === 'https' ? response.data?.project?.httpUrl : response.data?.project?.sshUrl
    if (!url) throw new Error(kind === 'https' ? 'У проекта нет адреса HTTPS на этом GitLab.' : 'У проекта нет адреса SSH на этом GitLab.')
    const picked = await vscode.window.showOpenDialog({ canSelectFiles: false, canSelectFolders: true, canSelectMany: false,
      openLabel: 'Клонировать сюда', title: `Куда клонировать ${project}` })
    const parent = picked?.[0]?.fsPath
    if (!parent) return deliver(surface, { type: 'gitlabClone', state: 'cancelled' })
    // Запись до клона: если git.clone откроет папку в этом окне, расширение
    // перезапустится, а карточка найдёт копию по адресу в .git/config.
    await rememberClone(project, { parent, url })
    deliver(surface, { type: 'gitlabClone', state: 'running', url })
    await vscode.commands.executeCommand('git.clone', url, parent)
    const info = await cloneInfo(project)
    deliver(surface, info.exists ? { type: 'gitlabClone', state: 'done', clone: info }
      : { type: 'gitlabClone', state: 'error', error: 'git не создал папку проекта — подробности в выводе Git' })
  }

  async function handle(message) {
    const action = String(message?.action || '')
    const surface = String(message?.surface || 'hub')
    // Ссылка «в GitLab» из карточки проекта: окно GitLab могло ещё не
    // спрашивать состояние, поэтому origin карточки помнит этот модуль.
    const browser = action === 'openBrowser' && surface.startsWith('project:') && origin
    if (!ACTIONS.has(action) && !browser) return false
    try {
      switch (action) {
        case 'openBrowser': {
          let parsed
          try { parsed = new URL(String(message.url || '')) } catch { throw new Error('Ссылка не разобралась.') }
          if (parsed.origin !== origin) throw new Error('Ссылка ведёт не на подключённый GitLab — Point её не откроет.')
          await vscode.env.openExternal(vscode.Uri.parse(parsed.toString()))
          break
        }
        case 'projects': {
          const scope = message.scope === 'owned' ? 'owned' : 'member'
          const search = String(message.search || '').slice(0, 100)
          deliver(surface, { type: 'gitlabProjects', scope, search, local: Object.fromEntries(Object.entries(clones()).filter(([, record]) => record?.path && fs.existsSync(path.join(record.path, '.git'))).map(([key, record]) => [key, record.path])), response: await call(`/api/integrations/gitlab/projects${query({ scope, search })}`) })
          break
        }
        case 'openProject':
          openProject(message)
          break
        case 'prefs': case 'savePrefs': {
          const server = String(message.server || '').slice(0, 300), all = provider.context.globalState?.get?.(PREFS_KEY) || {}, prefs = cleanPrefs(action === 'savePrefs' ? message.prefs : all[server])
          if (action === 'savePrefs') await provider.context.globalState?.update?.(PREFS_KEY, { ...all, [server]: prefs })
          deliver('tool-every', { type: 'gitlabPrefs', server, prefs }); break
        }
        case 'project': {
          const project = projectPath(message.project)
          const response = await call(`/api/integrations/gitlab/project${query({ project })}`)
          if (response?.state === 'ok') {
            details.set(project, response.data?.project || {})
            try { origin = new URL(String(response.data?.project?.webUrl || '')).origin } catch { /* без ссылки — без origin */ }
          }
          deliver(surface, { type: 'gitlabProject', response, clone: await cloneInfo(project) })
          break
        }
        case 'commits': {
          const project = projectPath(message.project)
          const at = ref(message.ref)
          const page = Math.max(1, Math.min(1000, Number(message.page) || 1))
          deliver(surface, { type: 'gitlabCommits', ref: at, page, response: await call(`/api/integrations/gitlab/commits${query({ project, ref: at, page })}`) })
          break
        }
        case 'commit': {
          const project = projectPath(message.project)
          const id = sha(message.sha)
          deliver(surface, { type: 'gitlabCommit', sha: id, response: await call(`/api/integrations/gitlab/commit${query({ project, sha: id })}`) })
          break
        }
        case 'branches': {
          const project = projectPath(message.project)
          deliver(surface, { type: 'gitlabBranches', response: await call(`/api/integrations/gitlab/branches${query({ project })}`) })
          break
        }
        case 'tree': {
          const project = projectPath(message.project)
          const folder = filePath(message.path, { optional: true })
          const at = ref(message.ref)
          deliver(surface, { type: 'gitlabTree', path: folder, ref: at, response: await call(`/api/integrations/gitlab/tree${query({ project, path: folder, ref: at })}`) })
          break
        }
        case 'readme': {
          const project = projectPath(message.project)
          const file = filePath(message.path)
          const at = ref(message.ref)
          deliver(surface, { type: 'gitlabReadme', path: file, response: await call(`/api/integrations/gitlab/file${query({ project, path: file, ref: at })}`) })
          break
        }
        case 'openFile':
          await openFile(message)
          break
        case 'openCommitDiff':
          await openCommitDiff(message)
          break
        case 'clone':
          await clone(message, surface)
          break
        case 'openClone': {
          const info = await cloneInfo(projectPath(message.project))
          if (!info.exists) throw new Error('Локальной копии больше нет на месте.')
          await vscode.commands.executeCommand('vscode.openFolder', vscode.Uri.file(info.path), { forceNewWindow: message.newWindow === true })
          break
        }
        case 'copyUrl': {
          const project = details.get(projectPath(message.project)) || {}
          const url = message.kind === 'https' ? project.httpUrl : project.sshUrl
          if (!url) throw new Error('Адрес клона ещё не загружен.')
          await vscode.env.clipboard.writeText(url)
          deliver(surface, { type: 'gitlabActionResult', action, response: { state: 'ok' } })
          break
        }
      }
    } catch (error) {
      deliver(surface, { type: action === 'clone' ? 'gitlabClone' : 'gitlabActionResult', action, state: 'error', error: error instanceof Error ? error.message : String(error) })
    }
    return true
  }

  return { handle, announceChange }
}

module.exports = { createGitLabProjectController, findClone }
