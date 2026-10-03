// Окно GitLab: список слева, выбранный MR или проект справа.
//
// Раскладка как у журнала Git в JetBrains: в широком окне (вкладка редактора,
// от 900px) строка не открывает новую вкладку, а показывает деталь рядом со
// списком — та же карточка MR и проекта, что во вкладке, без своей шапки
// окна. В узкой панели детали нет места, и строка, как раньше, открывает
// вкладку редактора; туда же ведёт значок в шапке детали.
//
// Деталь живёт на общем состоянии integrations-ui.js: state.mr и state.card
// с их запросами. Ответы хоста приходят окну с поверхностью `tool`, а окон
// GitLab может быть два (боковая панель и вкладка), поэтому ответ о чужом MR
// отбрасывается по project и iid.
//
// Окно без открытой папки смотрит на все проекты владельца и открывается
// каталогом, пока человек сам не выбрал вкладку.

export function createGitLabWindow({ state, gitlab, once, forget, layout, render, card, projectCard, projects }) {
  const query = layout === 'tool-gitlab' && typeof window.matchMedia === 'function' ? window.matchMedia('(min-width: 900px)') : undefined
  query?.addEventListener?.('change', () => render())
  const wide = () => Boolean(query?.matches)
  const selection = { mr: null, project: '' }
  let shownMr = ''
  let shownProject = ''
  let chosen = false

  const placeholder = (title, text) => `<div class="gl-empty is-detail"><strong>${title}</strong><p>${text}</p></div>`

  function mrDetail() {
    const { project, iid } = selection.mr
    const key = `${project}!${iid}`
    if (shownMr !== key) {
      state.mr = state.discussions = state.changes = undefined
      state.mrTab = 'overview'
      forget('mr', 'discussions', 'changes')
      shownMr = key
    }
    state.project = project
    state.iid = iid
    for (const action of ['mr', 'discussions', 'changes']) once(action, () => gitlab(action, { project, iid }))
    return card.mrBody(true)
  }

  function projectDetail() {
    if (shownProject !== selection.project) {
      projects.resetCard()
      shownProject = selection.project
    }
    state.project = selection.project
    projects.loadCard()
    return projectCard.projectBody(true)
  }

  // Пусто, если окно узкое: тогда раскладка — один список.
  function detailHtml(section) {
    if (!wide()) return ''
    if (section === 'mrs') return selection.mr ? mrDetail() : placeholder('Выберите merge request', 'Здесь откроются описание, обсуждение, изменения и пайплайн.')
    if (section === 'projects') return selection.project ? projectDetail() : placeholder('Выберите проект', 'Здесь откроются README, файлы, коммиты, ветки и пайплайны; проект можно склонировать.')
    return ''
  }

  const selected = (kind, project, iid) => wide() && kind === 'mr' && selection.mr?.project === project && selection.mr?.iid === Number(iid)

  // true — нажатие разобрано здесь; false — дальше, к прежним обработчикам.
  function click(action, target) {
    const data = target?.dataset || {}
    if (action === 'gitlab-section') chosen = true
    if (!wide() || data.tabOpen === '1') return false
    if (action === 'gitlab-open-mr') {
      selection.mr = { project: String(data.project || ''), iid: Number(data.iid || 0) }
      return true
    }
    if (action === 'gitlab-open-project') {
      selection.project = state.projects.selected = String(data.project || '')
      return true
    }
    return false
  }

  // Сообщение хоста до общего разбора: true — отбросить.
  function before(msg) {
    if (layout !== 'tool-gitlab') return false
    const response = msg?.response
    if (msg?.type === 'gitlabStatus' && msg.scope !== 'plugin' && !chosen && response?.state === 'ok' && !response.data?.binding?.workspace) state.section = 'projects'
    const own = selection.mr && msg?.project === selection.mr.project && Number(msg?.iid) === selection.mr.iid
    return ['gitlabMr', 'gitlabDiscussions', 'gitlabChanges'].includes(msg?.type) && Boolean(msg.project) && !own
  }

  // Избранное нужно и списку MR окна без папки, а не только каталогу.
  const prepare = () => projects.loadPrefs()

  return { detailHtml, selected, click, before, prepare }
}
