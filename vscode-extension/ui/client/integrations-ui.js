// Интеграции в вебвью: вкладка Гильдии, окно GitLab и карточка MR.
//
// Одно состояние на поверхность и три вида над ним. main.js знает о модуле
// пять строк: создать, отдать ему нажатие, отдать сообщение, нарисовать
// вкладку и карточку. Черновики полей и прокрутка списков живут здесь же:
// фоновое обновление состояния перерисовывает страницу целиком, и без этого
// набранный адрес или прокрученный список MR терялись бы на каждом кадре.
//
// Хосту уходят ровно два типа сообщений — mcpAction и gitlabAction; какая
// поверхность спросила, говорит поле surface, и ответ приходит только ей.

import { createGitLabToolView, draftId } from './gitlab-views.js'
import { createGitLabMergeRequestView } from './gitlab-mr-views.js'
import { createIntegrationsViews } from './integrations-views.js'

const SCROLLERS = ['.gl-scroll', '.hall-body']

function parsePairs(text, separator, label) {
  const values = {}
  for (const raw of String(text || '').split('\n')) {
    const line = raw.trim()
    if (!line) continue
    const at = line.indexOf(separator)
    if (at <= 0) throw new Error(`${label}: строка «${line.slice(0, 40)}» — ожидается ИМЯ${separator.trim() === ':' ? ': ' : '='}значение`)
    values[line.slice(0, at).trim()] = line.slice(at + separator.length).trim()
  }
  return values
}

export function createIntegrationsUi({ root, vscode, render, shell, toolPageHeading, markdown }) {
  const dataset = document.body?.dataset || {}
  const layout = String(dataset.layout || '')
  const state = {
    // Гильдия
    servers: undefined, logs: {}, toolsOpen: '', logOpen: '', formOpen: false, formId: '', importOpen: false,
    importCandidates: [], pluginEditing: false, journalOpen: false, actions: undefined, error: '',
    // Окно GitLab
    status: undefined, section: 'mrs', scope: 'mine', lists: {}, pipelines: undefined, jobs: {}, openPipeline: 0,
    bindingOpen: false, notice: null,
    // Карточка MR
    project: String(dataset.gitlabProject || ''), iid: Number(dataset.gitlabIid || 0), mr: undefined, mrTab: 'overview',
    discussions: undefined, changes: undefined, postingKey: '',
    busy: '', drafts: {},
  }
  const requested = new Set()
  const surface = () => layout === 'gitlab-mr' ? `mr:${state.project}!${state.iid}` : layout === 'tool-gitlab' ? 'tool' : 'hub'
  const mcp = (action, extra = {}) => vscode.postMessage({ type: 'mcpAction', action, ...extra })
  const gitlab = (action, extra = {}) => vscode.postMessage({ type: 'gitlabAction', action, surface: surface(), ...extra })
  // Запрос из отрисовки уходит после кадра и один раз: ответ сам вызовет
  // перерисовку, а она не должна спрашивать снова.
  const once = (key, send) => {
    if (requested.has(key)) return
    requested.add(key)
    setTimeout(send, 0)
  }
  const forget = (...keys) => keys.forEach(key => requested.delete(key))
  const notice = (tone, text) => { state.notice = text ? { tone, text } : null }
  const mrTarget = () => ({ project: state.project, iid: state.iid })

  const tool = createGitLabToolView({ getState: () => state, shell })
  const card = createGitLabMergeRequestView({ getState: () => state, markdown, pipelineRows: tool.pipelineRows })
  const guild = createIntegrationsViews({ getState: () => state, shell, toolPageHeading })

  function loadSection() {
    if (state.status?.state !== 'ok') return
    if (state.section === 'pipelines') once('pipelines', () => gitlab('pipelines'))
    else once(`list:${state.scope}`, () => gitlab('mergeRequests', { scope: state.scope }))
  }

  function resetGitLab() {
    state.status = undefined
    state.lists = {}
    state.pipelines = undefined
    state.jobs = {}
    forget(...[...requested].filter(key => key !== 'servers'))
  }

  function loadMergeRequest() {
    once('mr', () => gitlab('mr', mrTarget()))
    once('discussions', () => gitlab('discussions', mrTarget()))
    once('changes', () => gitlab('changes', mrTarget()))
  }

  // ── Виды ────────────────────────────────────────────────────────────────
  function guildView() {
    once('servers', () => mcp('list'))
    once('status', () => gitlab('status'))
    return guild.integrationsView()
  }

  function toolView() {
    once('status', () => gitlab('status'))
    loadSection()
    return tool.toolView()
  }

  function mrView() {
    loadMergeRequest()
    return card.mrView()
  }

  // ── Сообщения хоста ─────────────────────────────────────────────────────
  function message(msg) {
    const response = msg?.response
    switch (msg?.type) {
      case 'mcpServers':
        state.servers = Array.isArray(msg.servers) ? msg.servers : []
        state.busy = ''
        if (msg.saved) {
          if (msg.saved === state.formId || state.formOpen) Object.assign(state, { formOpen: false, formId: '' })
          if (msg.saved === 'mcp-gitlab') state.pluginEditing = false
          for (const key of Object.keys(state.drafts)) if (key.startsWith('form.') || key.startsWith('plugin.')) delete state.drafts[key]
        }
        // Доверие, проверка и токен меняют и состояние плагина GitLab.
        if (layout !== 'gitlab-mr' && layout !== 'tool-gitlab') { forget('status'); if (state.status) gitlab('status') }
        break
      case 'mcpLog':
        state.logs[String(msg.id || '')] = String(msg.log || '')
        break
      case 'mcpImportPreview':
        state.importCandidates = Array.isArray(msg.candidates) ? msg.candidates : []
        state.busy = ''
        delete state.drafts['import.text']
        break
      case 'mcpError':
        state.busy = ''
        state.error = String(msg.message || 'Действие не выполнилось')
        break
      case 'gitlabStatus':
        state.status = response
        state.busy = ''
        requested.add('status')
        if (layout === 'tool-gitlab') loadSection()
        break
      case 'gitlabBinding':
        state.busy = ''
        if (response?.state === 'ok') {
          state.bindingOpen = false
          for (const key of ['bindingMode', 'bindingProject', 'bindingUsername']) delete state.drafts[key]
          resetGitLab()
        } else notice('error', response?.problem || 'Привязка не сохранилась')
        break
      case 'gitlabMergeRequests':
        state.lists[String(msg.scope || state.scope)] = response
        break
      case 'gitlabPipelines':
        state.pipelines = response
        break
      case 'gitlabJobs':
        state.jobs[Number(msg.pipeline) || 0] = response
        break
      case 'gitlabMr':
        state.mr = response
        state.busy = ''
        requested.add('mr')
        break
      case 'gitlabDiscussions':
        state.busy = ''
        if (response?.state === 'ok' || !state.discussions) state.discussions = response
        if (msg.posted && state.postingKey) { delete state.drafts[state.postingKey]; notice('ok', 'Комментарий опубликован') }
        else if (response?.state !== 'ok' && state.postingKey) notice('error', response?.problem || 'Комментарий не опубликован')
        state.postingKey = ''
        break
      case 'gitlabChanges':
        state.changes = response
        break
      case 'gitlabActionResult': {
        state.busy = ''
        const failed = msg.error || (response && response.state !== 'ok')
        const text = msg.error || response?.problem || ''
        if (msg.cancelled) break
        if (failed) {
          if (layout === 'tool-gitlab' || layout === 'gitlab-mr') notice('error', [text, response?.fix].filter(Boolean).join(' — '))
          else state.error = text
          break
        }
        if (msg.action === 'retry') {
          notice('ok', 'Джоб перезапущен')
          const pipeline = Number(msg.pipeline) || 0
          delete state.jobs[pipeline]
          forget(`jobs:${pipeline}`)
          if (pipeline) gitlab('jobs', { project: String(msg.project || state.jobsProject || ''), pipeline })
        } else if (msg.action === 'merge') notice('ok', 'Merge request слит')
        else if (msg.action === 'approval') notice('ok', 'Одобрение обновлено')
        break
      }
      case 'gitlabChanged':
        if (layout === 'gitlab-mr') {
          state.mr = state.discussions = state.changes = undefined
          forget('mr', 'discussions', 'changes')
        } else resetGitLab()
        break
      case 'integrationActions':
        state.actions = Array.isArray(msg.actions) ? msg.actions : []
        break
      default:
        return false
    }
    render()
    return true
  }

  // ── Нажатия ─────────────────────────────────────────────────────────────
  function saveServerForm() {
    const d = key => state.drafts[`form.${key}`]
    const editing = (state.servers || []).find(item => item.id === state.formId)
    const transport = d('transport') || editing?.transport || 'stdio'
    const pairs = (key, fallback, separator, label) => d(key) !== undefined ? parsePairs(d(key), separator, label) : fallback
    const server = { id: state.formId, displayName: d('displayName') ?? editing?.displayName ?? '', transport }
    const secrets = {}
    if (transport === 'http') {
      server.url = d('url') ?? editing?.url ?? ''
      server.headers = pairs('headers', editing?.headers || {}, ':', 'Заголовки')
      const secret = pairs('secretHeaders', Object.fromEntries(Object.keys(editing?.secretHeaders || {}).map(name => [name, ''])), ':', 'Секретные заголовки')
      server.secretHeaders = Object.keys(secret)
      for (const [name, value] of Object.entries(secret)) if (value) secrets[`header:${name}`] = value
      const allow = d('allowPrivate') ?? Boolean(editing?.allowPrivateHost)
      try { server.allowPrivateHost = allow ? new URL(server.url).hostname : '' } catch { server.allowPrivateHost = '' }
    } else {
      server.command = d('command') ?? editing?.command ?? ''
      server.args = d('args') !== undefined ? d('args').split('\n').map(line => line.trim()).filter(Boolean) : editing?.args || []
      server.dir = d('dir') ?? editing?.dir ?? ''
      server.env = pairs('env', editing?.env || {}, '=', 'Переменные')
      const secret = pairs('secretEnv', Object.fromEntries(Object.keys(editing?.secretEnv || {}).map(name => [name, ''])), '=', 'Секретные переменные')
      server.secretEnv = Object.keys(secret)
      for (const [name, value] of Object.entries(secret)) if (value) secrets[`env:${name}`] = value
    }
    state.busy = 'save'
    mcp('save', { server, secrets })
  }

  function click(action, target) {
    const data = target?.dataset || {}
    const id = String(data.id || '')
    switch (action) {
      // Гильдия: свои серверы
      case 'mcp-trust': state.busy = 'trust'; state.error = ''; mcp('trust', { id }); break
      case 'mcp-probe': state.busy = 'probe'; state.error = ''; mcp('probe', { id }); break
      case 'mcp-stop': mcp('stop', { id }); break
      case 'mcp-delete': state.error = ''; mcp('delete', { id }); break
      case 'mcp-log':
        state.logOpen = state.logOpen === id ? '' : id
        if (state.logOpen) { delete state.logs[id]; mcp('log', { id }) }
        break
      case 'mcp-tools-toggle': state.toolsOpen = state.toolsOpen === id ? '' : id; break
      case 'mcp-tool-toggle': mcp('tool', { id, name: String(data.name || ''), enabled: Boolean(target.checked), risk: '' }); return true
      case 'mcp-tool-risk': mcp('tool', { id, name: String(data.name || ''), enabled: data.enabled === '1', risk: String(data.risk || '') }); break
      case 'mcp-secret': {
        const key = String(data.draftKey || '')
        const value = String(state.drafts[key] || '')
        if (!value) { state.error = 'Введите значение секрета.'; break }
        delete state.drafts[key]
        mcp('secret', { id, key: String(data.key || ''), value })
        break
      }
      case 'mcp-edit':
        for (const key of Object.keys(state.drafts)) if (key.startsWith('form.')) delete state.drafts[key]
        Object.assign(state, { formOpen: true, formId: id, importOpen: false })
        break
      case 'mcp-form-open':
        for (const key of Object.keys(state.drafts)) if (key.startsWith('form.')) delete state.drafts[key]
        Object.assign(state, { formOpen: true, formId: '', importOpen: false })
        break
      case 'mcp-form-cancel': Object.assign(state, { formOpen: false, formId: '' }); break
      case 'mcp-form-transport': state.drafts['form.transport'] = String(data.transport || 'stdio'); break
      case 'mcp-form-save':
        try { state.error = ''; saveServerForm() } catch (error) { state.error = error instanceof Error ? error.message : String(error) }
        break
      case 'mcp-import-open': Object.assign(state, { importOpen: true, formOpen: false }); break
      case 'mcp-import-close': Object.assign(state, { importOpen: false, importCandidates: [] }); mcp('importClear'); break
      case 'mcp-import-preview': state.busy = 'import'; state.error = ''; mcp('importPreview', { text: String(state.drafts['import.text'] || '') }); break
      case 'mcp-import-pick': state.error = ''; mcp('importPreview', { pick: true }); break
      case 'mcp-import-add': {
        const name = String(data.name || '')
        const candidate = state.importCandidates.find(item => item.name === name)
        const values = {}
        for (const key of candidate?.needsValue || []) values[key] = String(state.drafts[`importValue:${name}:${key}`] || '')
        state.busy = 'import'
        mcp('importAdd', { name, values })
        break
      }
      case 'mcp-dismiss-error': state.error = ''; break
      case 'integrations-journal': state.journalOpen = true; state.actions = undefined; gitlab('actions'); break
      // Гильдия: плагин GitLab
      case 'gitlab-plugin-edit': state.pluginEditing = true; break
      case 'gitlab-plugin-cancel': state.pluginEditing = false; for (const key of Object.keys(state.drafts)) if (key.startsWith('plugin.')) delete state.drafts[key]; break
      case 'gitlab-plugin-save': {
        const server = (state.servers || []).find(item => item.id === 'mcp-gitlab')
        const token = String(state.drafts['plugin.token'] || '')
        delete state.drafts['plugin.token']
        state.busy = 'plugin'
        state.error = ''
        gitlab('savePlugin', {
          url: String(state.drafts['plugin.url'] ?? server?.settings?.url ?? ''),
          caPath: String(state.drafts['plugin.caPath'] ?? server?.settings?.caPath ?? ''),
          token,
        })
        break
      }
      case 'gitlab-plugin-check': state.busy = 'probe'; mcp('probe', { id: 'mcp-gitlab' }); break
      case 'gitlab-open-window': gitlab('openWindow'); return true
      case 'gitlab-open-integrations': vscode.postMessage({ type: 'toolCommand', command: 'localAgent.openIntegrations' }); return true
      // Окно GitLab
      case 'gitlab-reload':
        if (layout === 'gitlab-mr') { state.mr = state.discussions = state.changes = undefined; forget('mr', 'discussions', 'changes') } else resetGitLab()
        notice('', '')
        break
      case 'gitlab-mr-reload': state.mr = state.discussions = state.changes = undefined; forget('mr', 'discussions', 'changes'); break
      case 'gitlab-section': state.section = data.section === 'pipelines' ? 'pipelines' : 'mrs'; break
      case 'gitlab-scope': state.scope = ['mine', 'review', 'project'].includes(data.scope) ? data.scope : 'mine'; break
      case 'gitlab-open-mr': gitlab('openMr', { project: String(data.project || ''), iid: Number(data.iid || 0), title: String(data.title || '') }); return true
      case 'gitlab-toggle-pipeline': {
        const pipeline = Number(data.pipeline) || 0
        state.openPipeline = state.openPipeline === pipeline ? 0 : pipeline
        state.jobsProject = String(data.project || '')
        if (state.openPipeline) once(`jobs:${pipeline}`, () => gitlab('jobs', { project: String(data.project || ''), pipeline }))
        break
      }
      case 'gitlab-job-log': gitlab('openJobLog', { project: String(data.project || ''), job: Number(data.job || 0), name: String(data.name || '') }); return true
      case 'gitlab-retry-job': state.busy = 'retry'; gitlab('retry', { project: String(data.project || ''), job: Number(data.job || 0), pipeline: Number(data.pipeline || 0) }); break
      case 'gitlab-binding-toggle': state.bindingOpen = !state.bindingOpen; break
      case 'gitlab-binding-mode': state.drafts.bindingMode = String(target.value || 'auto'); break
      case 'gitlab-binding-cancel': state.bindingOpen = false; break
      case 'gitlab-binding-save': {
        const binding = state.status?.data?.binding || {}
        const mode = state.drafts.bindingMode || binding.mode || 'auto'
        state.busy = 'binding'
        gitlab('binding', { mode, project: String(state.drafts.bindingProject ?? binding.manual ?? ''), username: String(state.drafts.bindingUsername ?? binding.username ?? '') })
        break
      }
      case 'gitlab-dismiss-notice': notice('', ''); break
      // Карточка MR
      case 'gitlab-mr-tab': state.mrTab = ['overview', 'discussion', 'changes', 'pipeline'].includes(data.tab) ? data.tab : 'overview'; break
      case 'gitlab-approve': state.busy = 'approval'; gitlab('approval', { ...mrTarget(), approve: data.approve === '1', sha: String(data.sha || '') }); break
      case 'gitlab-merge': {
        const mr = state.mr?.data?.mergeRequest || {}
        state.busy = 'merge'
        gitlab('merge', {
          ...mrTarget(), sha: String(data.sha || ''), title: String(mr.title || ''), sourceBranch: String(mr.sourceBranch || ''),
          targetBranch: String(mr.targetBranch || ''), removeSourceBranch: Boolean(state.drafts.removeSource ?? mr.removeSourceBranch),
        })
        break
      }
      case 'gitlab-comment': {
        const discussionId = String(data.discussion || '')
        const key = discussionId ? `reply:${discussionId}` : 'comment'
        const body = String(state.drafts[key] || '').trim()
        if (!body) { notice('error', 'Комментарий пуст.'); break }
        state.busy = 'comment'
        state.postingKey = key
        gitlab('comment', { ...mrTarget(), discussionId, body })
        break
      }
      case 'gitlab-open-diff': {
        const refs = state.mr?.data?.mergeRequest?.diffRefs || {}
        gitlab('openDiff', {
          ...mrTarget(), path: String(data.path || ''), oldPath: String(data.oldPath || ''), base: String(refs.baseSha || ''),
          head: String(refs.headSha || ''), newFile: data.newFile === '1', deleted: data.deleted === '1',
        })
        return true
      }
      case 'gitlab-open-browser': gitlab('openBrowser', { url: String(data.url || '') }); return true
      default:
        return false
    }
    render()
    return true
  }

  // ── Черновики и прокрутка ───────────────────────────────────────────────
  const keep = event => {
    const field = event.target
    const key = field?.dataset?.draft
    if (!key) return
    state.drafts[key] = field.type === 'checkbox' ? Boolean(field.checked) : String(field.value ?? '')
  }
  root.addEventListener?.('input', keep)
  root.addEventListener?.('change', keep)
  const scroll = {}
  root.addEventListener?.('scroll', event => {
    const element = event.target
    const selector = SCROLLERS.find(item => element?.matches?.(item))
    if (selector && root.querySelector?.('.gl-app, .int-page')) scroll[selector] = element.scrollTop
  }, true)
  if (typeof MutationObserver === 'function') {
    new MutationObserver(() => {
      if (!root.querySelector('.gl-app, .int-page')) return
      for (const selector of SCROLLERS) {
        const element = root.querySelector(selector)
        if (element && scroll[selector] && element.scrollTop !== scroll[selector]) element.scrollTop = scroll[selector]
      }
    }).observe(root, { childList: true })
  }

  return { guildView, toolView, mrView, click, message, draftId }
}
