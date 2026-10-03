// Окно GitLab → «Проекты»: избранное и фильтр групп GitLab.
//
// Проекты лежат в разных группах и подгруппах (namespace), и найти нужный
// среди сотни — работа на каждый день. Избранное — звезда у строки и своя
// группа сверху; фильтр — дерево групп со счётом проектов; раскладка — по
// группам или по доступу. Это выбор человека, и он переживает закрытие окна:
// хост хранит его по серверу GitLab (действия prefs и savePrefs), в GitLab
// ничего не пишется. Фильтр групп работает по загруженному списку.

import { esc } from './html-escape.js'
import { glIcon } from './gitlab-common.js'

export const DEFAULT_PREFS = Object.freeze({ favorites: [], groups: [], groupBy: 'ns', favOnly: false, mrFav: false })

export const namespaceOf = project => String(project.path || '').split('/').slice(0, -1).join('/')
const inGroup = (project, group) => namespaceOf(project) === group || namespaceOf(project).startsWith(`${group}/`)
const strings = value => (Array.isArray(value) ? value : []).map(String).filter(Boolean)

export function normalizePrefs(value) {
  return { favorites: [...new Set(strings(value?.favorites))], groups: [...new Set(strings(value?.groups))],
    groupBy: value?.groupBy === 'access' ? 'access' : 'ns', favOnly: value?.favOnly === true, mrFav: value?.mrFav === true }
}

export function filterItems(prefs, items) {
  const favorites = new Set(prefs.favorites)
  return items.filter(project => !prefs.favOnly || favorites.has(project.path))
    .filter(project => !prefs.groups.length || prefs.groups.some(group => inGroup(project, group)))
}

// Дерево групп для фильтра: каждая ступень пути со счётом проектов под ней.
function groupTree(items) {
  const counts = new Map()
  for (const project of items) {
    const parts = namespaceOf(project).split('/').filter(Boolean)
    parts.forEach((_, index) => { const key = parts.slice(0, index + 1).join('/'); counts.set(key, (counts.get(key) || 0) + 1) })
  }
  return [...counts.entries()].sort((a, b) => a[0].localeCompare(b[0]))
}

function filterPopup(prefs, items) {
  return `<section class="gl-popup gl-filter-popup" aria-label="Фильтр проектов"><h4>Группы GitLab</h4>
    ${groupTree(items).map(([group, count]) => `<label class="gl-ns is-d${Math.min(group.split('/').length - 1, 3)}"><input type="checkbox" data-action="gitlab-group" data-group="${esc(group)}"${prefs.groups.includes(group) ? ' checked' : ''}><span>${esc(group.split('/').pop())}</span><small>${count}</small></label>`).join('') || '<p class="gl-muted">Групп нет.</p>'}
    <h4>Раскладка</h4>
    <div class="nc-seg" role="group" aria-label="Раскладка проектов">${[['ns', 'По группам'], ['access', 'По доступу']].map(([id, label]) => `<button type="button" class="${prefs.groupBy === id ? 'is-on' : ''}" data-action="gitlab-group-by" data-by="${id}" aria-pressed="${prefs.groupBy === id ? 'true' : 'false'}">${label}</button>`).join('')}</div>
    <footer><button type="button" class="gl-btn is-quiet" data-action="gitlab-filters-clear">Сбросить</button><button type="button" class="gl-btn" data-action="gitlab-filters-toggle">Готово</button></footer>
  </section>`
}

export function filtersBar(state, loaded, filtered) {
  const prefs = state.projects.prefs
  return `<div class="gl-bar gl-filters">
    <button type="button" class="gl-filter${prefs.favOnly ? ' is-on' : ''}" data-action="gitlab-favorites-only" aria-pressed="${prefs.favOnly ? 'true' : 'false'}" title="Только избранные проекты">${glIcon('star', 12)}<span>Избранное</span>${prefs.favorites.length ? `<b>${prefs.favorites.length}</b>` : ''}</button>
    <button type="button" class="gl-filter${prefs.groups.length ? ' is-on' : ''}" data-action="gitlab-filters-toggle" aria-expanded="${state.projects.filterOpen ? 'true' : 'false'}" title="Фильтр по группам GitLab и раскладка">${glIcon('folder', 12)}<span>Группы</span>${prefs.groups.length ? `<b>${prefs.groups.length}</b>` : ''}${glIcon('caretDown', 11)}</button>
    ${prefs.groups.map(group => `<button type="button" class="gl-chip" data-action="gitlab-group" data-group="${esc(group)}" title="Убрать фильтр" aria-label="${esc(`Убрать фильтр ${group}`)}"><span>${esc(group)}</span>${glIcon('x', 10)}</button>`).join('')}
    <i class="nc-gap"></i>${filtered ? '<button type="button" class="gl-link" data-action="gitlab-filters-clear">сбросить</button>' : ''}
  </div>${state.projects.filterOpen ? filterPopup(prefs, loaded) : ''}`
}

// Выбор человека: читается раз на сервер, пишется каждой правкой целиком.
export function createProjectPrefs({ state, gitlab, once }) {
  const server = () => String(state.status?.data?.url || '')
  const load = () => { if (state.status?.state === 'ok') once(`prefs:${server()}`, () => gitlab('prefs', { server: server() })) }
  const change = patch => {
    state.projects.prefs = normalizePrefs({ ...state.projects.prefs, ...patch })
    gitlab('savePrefs', { server: server(), prefs: state.projects.prefs })
  }
  const toggled = (list, value) => list.includes(value) ? list.filter(item => item !== value) : [...list, value]

  function message(msg) {
    if (msg?.type !== 'gitlabPrefs') return false
    if (String(msg.server || '') === server()) state.projects.prefs = normalizePrefs(msg.prefs)
    return true
  }

  function click(action, data) {
    const prefs = state.projects.prefs
    switch (action) {
      case 'gitlab-favorite': change({ favorites: toggled(prefs.favorites, String(data.project || '')) }); return true
      case 'gitlab-favorites-only': change({ favOnly: !prefs.favOnly }); return true
      case 'gitlab-mr-favorites': change({ mrFav: !prefs.mrFav }); return true
      case 'gitlab-group': change({ groups: toggled(prefs.groups, String(data.group || '')) }); return true
      case 'gitlab-group-by': change({ groupBy: data.by }); return true
      case 'gitlab-filters-toggle': state.projects.filterOpen = !state.projects.filterOpen; return true
      case 'gitlab-filters-clear':
        state.projects.filterOpen = false
        if (state.projects.search || state.drafts.projectSearch) { state.projects.search = ''; delete state.drafts.projectSearch }
        change({ groups: [], favOnly: false })
        return true
      default:
        return false
    }
  }

  return { load, message, click }
}
