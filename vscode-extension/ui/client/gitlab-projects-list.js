// Окно GitLab → «Проекты»: проекты владельца с поиском, избранным и фильтром
// групп GitLab (namespace с подгруппами).
//
// Избранное идёт отдельной группой сверху, остальное — по группам GitLab или
// по доступу («Можно пушить», «Только чтение»); архив всегда внизу. Строка
// говорит, где проект лежит, что владельцу доступно и есть ли копия на этой
// машине, и открывает деталь: файлы, коммиты, ветки, пайплайны и клон.
// Избранное, группы и раскладку хост помнит по серверу GitLab (prefs).

import { esc } from './html-escape.js'
import { draftId, glIcon, loadingHtml, problemHtml, timeShort } from './gitlab-common.js'
import { filterItems, filtersBar, namespaceOf } from './gitlab-project-filters.js'

const SCOPES = [['member', 'Мои', 'Проекты, где вы участник'], ['owned', 'Свои', 'Проекты, которые принадлежат вам']]

// Уровень доступа GitLab словом: 30 и выше — можно пушить в незащищённые ветки.
const ACCESS = { 10: 'гость', 15: 'планировщик', 20: 'репортёр', 30: 'разработчик', 40: 'сопровождающий', 50: 'владелец' }
const ACCESS_GROUPS = [['push', 'Можно пушить'], ['read', 'Только чтение']]

function kindOf(project, current) {
  if (current && project.path === current) return 'current'
  if (project.archived) return 'archived'
  return project.accessLevel >= 30 ? 'push' : 'read'
}

export function projectRow(state, project, current, { namespace = true } = {}) {
  const kind = kindOf(project, current)
  const role = ACCESS[project.accessLevel] || ''
  const said = { current: 'эта папка', push: role || 'можно пушить', read: role ? `${role} — только чтение` : 'только чтение', archived: 'в архиве — только чтение' }[kind]
  const favorite = (state.projects.prefs?.favorites || []).includes(project.path)
  const local = state.projects.local?.[project.path]
  const selected = state.projects.selected === project.path
  return `<div class="gl-proj-row is-${kind}${favorite ? ' is-favorite' : ''}${selected ? ' is-selected' : ''}">
    <button type="button" class="gl-row" data-action="gitlab-open-project" data-project="${esc(project.path)}" data-name="${esc(project.name)}" title="${esc(project.path)}">
      <i class="gl-square" aria-hidden="true">${esc(String(project.name || '?').slice(0, 1).toUpperCase())}</i>
      <span class="gl-row-main"><strong>${namespace && namespaceOf(project) ? `<em>${esc(namespaceOf(project))}/</em>` : ''}${esc(project.name)}</strong>${project.description ? `<small class="gl-project-desc">${esc(project.description)}</small>` : ''}</span>
      <span class="gl-row-meta">${local ? `<span class="gl-local" title="${esc(local)}">${glIcon('folder', 12)}локально</span>` : ''}<span class="gl-role">${esc(said)}</span><span>${esc(timeShort(project.lastActivityAt))}</span></span>
    </button>
    <button type="button" class="gl-star" data-action="gitlab-favorite" data-project="${esc(project.path)}" aria-pressed="${favorite ? 'true' : 'false'}" aria-label="${esc(`${favorite ? 'Убрать из избранного' : 'В избранное'}: ${project.path}`)}" title="${favorite ? 'Убрать из избранного' : 'В избранное'}">${glIcon('star', 13)}</button>
  </div>`
}

const section = (id, label, rows) => rows.length ? `<section class="gl-group is-${id}"><h3><span>${label}</span><em>${rows.length}</em></h3>${rows.join('')}</section>` : ''

export function projectsBody(state) {
  const projects = state.projects
  const prefs = projects.prefs
  const response = projects.lists[`${projects.scope}:${projects.search}`]
  const loaded = response?.state === 'ok' && Array.isArray(response.data?.items) ? response.data.items : []
  const filtered = Boolean(prefs.groups.length || prefs.favOnly || projects.search)
  const head = `<header class="gl-bar gl-projects-head">
    <label class="gl-search"><input id="${draftId('projectSearch')}" data-draft="projectSearch" value="${esc(state.drafts.projectSearch ?? projects.search)}" placeholder="Имя или группа/имя" aria-label="Поиск проекта" autocomplete="off" spellcheck="false"></label>
    <button type="button" class="nc-icon-btn" data-action="gitlab-projects-search" title="Найти" aria-label="Найти">${glIcon('search', 13)}</button>
    <div class="nc-seg" role="group" aria-label="Какие проекты">${SCOPES.map(([id, label, title]) =>
      `<button type="button" class="${projects.scope === id ? 'is-on' : ''}" data-action="gitlab-projects-scope" data-scope="${id}" title="${esc(title)}" aria-pressed="${projects.scope === id ? 'true' : 'false'}">${esc(label)}</button>`).join('')}</div>
  </header>
  ${filtersBar(state, loaded, filtered)}`
  let body
  if (!response) body = loadingHtml('Загружаем проекты…')
  else if (response.state !== 'ok') body = `<div class="gl-pad">${problemHtml(response, { retry: 'gitlab-reload' })}</div>`
  else {
    const current = String(response.data?.current || '')
    const favorites = new Set(prefs.favorites)
    const items = filterItems(prefs, loaded)
    if (!items.length) {
      const why = projects.search ? `По запросу «${projects.search}» проектов нет.` : loaded.length ? 'Под фильтр не попал ни один проект.' : 'GitLab не знает проектов, где вы участник.'
      body = `<div class="gl-empty"><strong>${loaded.length || projects.search ? 'Ничего не нашлось' : 'Проектов нет'}</strong><p>${esc(why)}</p>${filtered ? '<div class="gl-empty-acts"><button type="button" class="gl-btn" data-action="gitlab-filters-clear">Сбросить фильтры</button></div>' : ''}</div>`
    } else {
      const row = (project, options) => projectRow(state, project, current, options)
      const top = items.filter(project => kindOf(project, current) === 'current')
      const starred = prefs.favOnly ? [] : items.filter(project => favorites.has(project.path) && !top.includes(project))
      const rest = items.filter(project => !top.includes(project) && !starred.includes(project))
      const archived = rest.filter(project => project.archived)
      const live = rest.filter(project => !project.archived)
      const grouped = prefs.groupBy === 'access'
        ? ACCESS_GROUPS.map(([id, label]) => section(id, label, live.filter(project => kindOf(project, current) === id).map(project => row(project)))).join('')
        : [...new Set(live.map(namespaceOf))].sort().map(group => section('ns', esc(group || 'без группы'), live.filter(project => namespaceOf(project) === group).map(project => row(project, { namespace: false })))).join('')
      body = section('current', 'Эта папка', top.map(project => row(project))) + section('favorites', `${glIcon('star', 10)}Избранное`, starred.map(project => row(project)))
        + grouped + section('archived', 'Архив', archived.map(project => row(project)))
    }
  }
  return `${head}<div class="nc-scroll gl-scroll">${body}</div>`
}
