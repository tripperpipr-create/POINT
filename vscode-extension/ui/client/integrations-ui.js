// Интеграции в вебвью: общая страница «Интеграции и MCP», вкладка проекта
// «GitLab», окно GitLab и карточка MR.
//
// Одно состояние на поверхность и четыре вида над ним. main.js знает о модуле
// пять строк: создать, отдать ему нажатие, отдать сообщение, нарисовать
// вкладку и карточку. Черновики полей и прокрутка списков живут здесь же:
// фоновое обновление состояния перерисовывает страницу целиком, и без этого
// набранный адрес или прокрученный список MR терялись бы на каждом кадре.
//
// Хосту уходят ровно два типа сообщений — mcpAction и gitlabAction; какая
// поверхность спросила, говорит поле surface, и ответ приходит только ей.

import { createGitLabToolView } from './gitlab-views.js'
import { draftId } from './gitlab-common.js'
import { createGitLabMergeRequestView } from './gitlab-mr-views.js'
import { createIntegrationsViews } from './integrations-views.js'
import { createGitLabProjectView } from './gitlab-project-view.js'
import { createMcpServerActions } from './mcp-server-actions.js'

const SCROLLERS = ['.gl-scroll', '.hall-body']

export function createIntegrationsUi({ root, vscode, render, shell, toolPageHeading, markdown }) {
  const dataset = document.body?.dataset || {}
  const layout = String(dataset.layout || '')
  const state = {
    // Общая страница: pluginStatus — здоровье плагина в любом проекте
    servers: undefined, pluginStatus: undefined, logs: {}, toolsOpen: '', logOpen: '', formOpen: false, formId: '', importOpen: false,
    importCandidates: [], pluginEditing: false, journalOpen: false, actions: undefined, error: '',
    // Окно GitLab и вкладка проекта: status — связь и данные проекта папки
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
  const dropBindingDrafts = () => ['bindingMode', 'bindingProject', 'bindingUsername'].forEach(key => delete state.drafts[key])
  const notice = (tone, text) => { state.notice = text ? { tone, text } : null }
  const mrTarget = () => ({ project: state.project, iid: state.iid })

  const tool = createGitLabToolView({ getState: () => state, shell })
  const card = createGitLabMergeRequestView({ getState: () => state, markdown, pipelineRows: tool.pipelineRows })
  const guild = createIntegrationsViews({ getState: () => state, shell, toolPageHeading })
  const project = createGitLabProjectView({ getState: () => state, shell, toolPageHeading, bindingEditor: tool.bindingEditor })
  const servers = createMcpServerActions({ state, mcp, render })
  // Мир и ядро, чей статус лежит в state: смена проекта в Чертоге и перезапуск
  // ядра не пересоздают вебвью, и без этих меток окно показывало бы связь
  // прошлого мира или ответ ядра, которого больше нет.
  let workspacePath
  let coreRunning

  function loadSection() {
    if (state.status?.state !== 'ok' || state.status.data?.linked === false) return
    if (state.section === 'pipelines') once('pipelines', () => gitlab('pipelines'))
    else once(`list:${state.scope}`, () => gitlab('mergeRequests', { scope: state.scope }))
  }

  function resetGitLab() {
    state.status = state.pluginStatus = undefined
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
  // Общая страница спрашивает здоровье плагина, вкладка проекта — его связь.
  function guildView(tab = 'integrations') {
    if (tab === 'project-gitlab') {
      once('status', () => gitlab('status'))
      return project.projectView()
    }
    once('servers', () => mcp('list'))
    once('pluginStatus', () => gitlab('status', { scope: 'plugin' }))
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
    if (msg?.type === 'state') {
      const path = String(msg.workspacePath || '')
      const running = msg.service?.state === 'running'
      const moved = workspacePath !== undefined && path !== workspacePath
      if ((moved || (running && coreRunning === false)) && layout !== 'gitlab-mr') { resetGitLab(); state.bindingOpen = false; dropBindingDrafts() }
      workspacePath = path
      coreRunning = running
      return false
    }
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
        if (layout !== 'gitlab-mr' && layout !== 'tool-gitlab') { forget('status', 'pluginStatus'); if (state.status) gitlab('status'); if (state.pluginStatus || msg.probed === 'mcp-gitlab') gitlab('status', { scope: 'plugin' }) }
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
        state[msg.scope === 'plugin' ? 'pluginStatus' : 'status'] = response
        state.busy = ''
        requested.add(msg.scope === 'plugin' ? 'pluginStatus' : 'status')
        if (layout === 'tool-gitlab') loadSection()
        break
      case 'gitlabBinding':
        // Ответ Хаба долетает и сюда общей рассылкой; чужое сохранение окну
        // объявит gitlabChanged, второй сброс дал бы второй запрос статуса.
        if (state.busy !== 'binding') return true
        state.busy = ''
        if (response?.state === 'ok') {
          state.bindingOpen = false
          dropBindingDrafts()
          resetGitLab()
        } else if (layout === 'tool-gitlab') notice('error', response?.problem || 'Связь не сохранилась')
        else state.error = response?.problem || 'Связь не сохранилась'
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
  function click(action, target) {
    if (servers.click(action, target)) return true
    const data = target?.dataset || {}
    switch (action) {
      // Общая страница: журнал действий во внешних сервисах
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
      // Один клик: origin уже ведёт на этот GitLab, и связь — «по git remote».
      case 'gitlab-link-detected':
        state.busy = 'binding'
        gitlab('binding', { mode: 'auto', project: '', username: String(state.status?.data?.binding?.username || '') })
        break
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
