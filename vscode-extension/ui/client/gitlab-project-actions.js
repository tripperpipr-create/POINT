// Проекты GitLab в вебвью: состояние, ответы хоста и нажатия для раздела
// «Проекты» окна GitLab и карточки проекта (вкладка редактора или правая
// колонка широкого окна). Избранное и фильтр групп — gitlab-project-filters.js.
//
// Состояние общее с integrations-ui.js: projects — список окна, card —
// карточка. Запрос уходит один раз на ключ (once): ответ сам перерисует
// страницу, а перерисовка не должна спрашивать снова.
import { DEFAULT_PREFS, createProjectPrefs } from './gitlab-project-filters.js'

const TABS = ['overview', 'files', 'commits', 'branches', 'pipelines']
const README = /^readme(\.md|\.markdown|\.txt)?$/i

export function createGitLabProjectActions({ state, gitlab, once, forgetMatching, notice, layout }) {
  Object.assign(state, {
    projects: { scope: 'member', search: '', lists: {}, local: {}, prefs: { ...DEFAULT_PREFS }, filterOpen: false, selected: '' },
    card: undefined,
  })
  const prefs = createProjectPrefs({ state, gitlab, once })
  const listKey = () => `${state.projects.scope}:${state.projects.search}`
  const target = () => ({ project: state.project })
  const card = () => state.card
  const freshCard = () => ({ detail: undefined, clone: { exists: false }, tab: 'overview', ref: '', path: '', trees: {}, commits: {},
    openCommit: '', commitDetails: {}, branches: undefined, readme: undefined, readmePath: '', cloning: '' })

  function loadList() {
    const key = listKey()
    once(`projects:${key}`, () => gitlab('projects', { scope: state.projects.scope, search: state.projects.search }))
    prefs.load()
  }

  function reset() {
    state.projects.lists = {}
    forgetMatching(/^projects:/)
  }

  const treeKey = (ref, path) => `${ref}:${path}`

  // Карточка: сначала сам проект, потом — то, что нужно открытой вкладке.
  function loadCard() {
    state.card ||= freshCard()
    once('card', () => gitlab('project', target()))
    const view = card()
    const detail = view.detail?.state === 'ok' ? view.detail.data?.project : undefined
    if (!detail) return
    const ref = view.ref || detail.defaultBranch || ''
    // Корень дерева нужен и «Обзору»: README ищется среди его файлов.
    once(`tree:${treeKey(ref, '')}`, () => gitlab('tree', { ...target(), path: '', ref }))
    if (view.tab === 'files' && view.path) once(`tree:${treeKey(ref, view.path)}`, () => gitlab('tree', { ...target(), path: view.path, ref }))
    const root = view.trees[treeKey(ref, '')]
    const readme = root?.state === 'ok' ? (root.data?.tree?.entries || []).find(entry => entry.type === 'blob' && README.test(entry.name)) : undefined
    if (readme) once(`readme:${ref}`, () => gitlab('readme', { ...target(), path: readme.path, ref }))
    if (view.tab === 'commits') once(`commits:${ref}:1`, () => gitlab('commits', { ...target(), ref, page: 1 }))
    if (view.tab === 'branches' || view.tab === 'commits') once('branches', () => gitlab('branches', target()))
    if (view.tab === 'pipelines') once(`card-pipelines:${ref}`, () => { view.pipelinesWanted = true; gitlab('pipelines', { ...target(), ref }) })
  }

  function resetCard() {
    state.card = undefined
    forgetMatching(/^(card|branches|tree:|readme:|commits:|commit:)/)
  }

  function message(msg) {
    const response = msg?.response
    const view = card()
    if (prefs.message(msg)) return true
    switch (msg?.type) {
      case 'gitlabProjects':
        state.projects.lists[`${msg.scope}:${msg.search}`] = response
        state.projects.local = { ...state.projects.local, ...(msg.local || {}) }
        return true
      // Пайплайны ветки проекта — ответ на вопрос карточки, а не окна.
      case 'gitlabPipelines':
        if (!view?.pipelinesWanted || (response?.data?.project && response.data.project !== state.project)) return false
        view.pipelines = response
        view.pipelinesWanted = false
        return true
      case 'gitlabProject':
        if (!view) return true
        view.detail = response
        view.clone = msg.clone || { exists: false }
        if (!view.ref) view.ref = response?.data?.project?.defaultBranch || ''
        return true
      case 'gitlabTree':
        if (view) view.trees[treeKey(msg.ref, msg.path)] = response
        return true
      case 'gitlabReadme':
        if (view) { view.readme = response; view.readmePath = String(msg.path || '') }
        return true
      case 'gitlabCommits':
        if (view) {
          const pages = view.commits[msg.ref] ||= []
          pages[Number(msg.page) - 1] = response
        }
        return true
      case 'gitlabCommit':
        if (view) view.commitDetails[msg.sha] = response
        return true
      case 'gitlabBranches':
        if (view) view.branches = response
        return true
      case 'gitlabClone':
        if (!view) return true
        view.cloning = msg.state === 'running' ? 'running' : ''
        if (msg.state === 'done') { view.clone = msg.clone; notice('ok', 'Проект склонирован') }
        if (msg.state === 'error') notice('error', msg.error || 'Клонирование не удалось')
        return true
      case 'gitlabActionResult':
        if (layout !== 'gitlab-project') return false
        if (msg.error || (response && response.state !== 'ok')) notice('error', msg.error || response?.problem || 'Действие не выполнилось')
        else if (msg.action === 'copyUrl') notice('ok', 'Адрес скопирован')
        return true
      case 'gitlabChanged':
        if (layout !== 'gitlab-project') return false
        resetCard()
        return true
      default:
        return false
    }
  }

  function setRef(ref, tab) {
    const view = card()
    if (!view) return
    view.ref = ref
    view.path = ''
    view.openCommit = ''
    view.pipelines = undefined
    if (tab) view.tab = tab
  }

  function click(action, element) {
    const data = element?.dataset || {}
    const view = card()
    if (prefs.click(action, data)) return true
    switch (action) {
      // Окно GitLab → «Проекты»
      case 'gitlab-projects-scope':
        state.projects.scope = data.scope === 'owned' ? 'owned' : 'member'
        return true
      case 'gitlab-projects-search':
        state.projects.search = String(state.drafts.projectSearch || '').trim().slice(0, 100)
        return true
      case 'gitlab-projects-clear':
        state.projects.search = ''
        delete state.drafts.projectSearch
        return true
      case 'gitlab-open-project':
        gitlab('openProject', { project: String(data.project || ''), name: String(data.name || '') })
        return 'sent'
      // Карточка проекта
      case 'gitlab-project-tab':
        if (view) view.tab = TABS.includes(data.tab) ? data.tab : 'overview'
        return true
      case 'gitlab-project-reload':
        resetCard()
        notice('', '')
        return true
      case 'gitlab-tree-open':
        if (!view) return true
        if (data.type === 'tree') view.path = String(data.path || '')
        else if (data.type === 'blob') { gitlab('openFile', { ...target(), path: String(data.path || ''), ref: view.ref }); return 'sent' }
        return true
      case 'gitlab-tree-path':
        if (view) { view.tab = 'files'; view.path = String(data.path || '') }
        return true
      case 'gitlab-branch-commits':
        setRef(String(data.ref || ''), 'commits')
        return true
      case 'gitlab-branch-files':
        setRef(String(data.ref || ''), 'files')
        return true
      case 'gitlab-commits-more': {
        if (!view) return true
        const page = (view.commits[view.ref] || []).length + 1
        once(`commits:${view.ref}:${page}`, () => gitlab('commits', { ...target(), ref: view.ref, page }))
        return true
      }
      case 'gitlab-commit-toggle': {
        if (!view) return true
        const sha = String(data.sha || '')
        view.openCommit = view.openCommit === sha ? '' : sha
        if (view.openCommit) once(`commit:${sha}`, () => gitlab('commit', { ...target(), sha }))
        return true
      }
      case 'gitlab-commit-file':
        gitlab('openCommitDiff', { ...target(), sha: String(data.sha || ''), parent: String(data.parent || ''), path: String(data.path || ''),
          oldPath: String(data.oldPath || ''), newFile: data.newFile === '1', deleted: data.deleted === '1' })
        return 'sent'
      case 'gitlab-clone':
        if (view) view.cloning = 'asking'
        gitlab('clone', { ...target(), kind: data.kind === 'https' ? 'https' : 'ssh' })
        return true
      case 'gitlab-copy-url':
        gitlab('copyUrl', { ...target(), kind: data.kind === 'https' ? 'https' : 'ssh' })
        return 'sent'
      case 'gitlab-open-clone':
        gitlab('openClone', { ...target(), newWindow: data.newWindow === '1' })
        return 'sent'
      default:
        return false
    }
  }

  return { loadList, loadCard, loadPrefs: prefs.load, reset, resetCard, message, click }
}
