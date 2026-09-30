// Окно GitLab → «Проекты»: проекты владельца, свежие сверху, с поиском.
//
// Как и список MR, он сгруппирован по тому, что с проектом можно делать:
// «Эта папка» — открытая папка и есть он, «Можно пушить», «Только чтение»,
// «Архив». Строка говорит одной фразой, что владельцу доступно, и открывает
// карточку проекта: файлы, коммиты, ветки и клон.

import { esc } from './html-escape.js'
import { draftId, glAvatar, glIcon, loadingHtml, problemHtml, timeShort } from './gitlab-common.js'

const SCOPES = [['member', 'Мои', 'Проекты, где вы участник'], ['owned', 'Свои', 'Проекты, которые принадлежат вам']]

// Уровень доступа GitLab словом: 30 и выше — можно пушить в незащищённые ветки.
const ACCESS = { 10: 'гость', 15: 'планировщик', 20: 'репортёр', 30: 'разработчик', 40: 'сопровождающий', 50: 'владелец' }

function group(project, current) {
  if (current && project.path === current) return 'current'
  if (project.archived) return 'archived'
  return project.accessLevel >= 30 ? 'push' : 'read'
}

const GROUPS = [['current', 'Эта папка'], ['push', 'Можно пушить'], ['read', 'Только чтение'], ['archived', 'Архив']]

export function projectRow(project, current) {
  const kind = group(project, current)
  const role = ACCESS[project.accessLevel] || ''
  const said = {
    current: ['ok', `открыт в этой папке${project.defaultBranch ? ` · по умолчанию ${project.defaultBranch}` : ''}`],
    push: ['ok', role ? `вы ${role}` : 'можно пушить'],
    read: ['mute', role ? `вы ${role} — только чтение` : 'только чтение'],
    archived: ['mute', 'в архиве — только чтение'],
  }[kind]
  return `<button type="button" class="nc-row gl-project-row is-${kind}" data-action="gitlab-open-project" data-project="${esc(project.path)}" data-name="${esc(project.name)}" title="${esc(project.path)}">
    ${glAvatar({ name: project.name, username: project.path }, { size: 'lg', title: false }).replace('gl-ava', 'gl-ava is-square')}
    <span>
      <strong>${esc(project.name)}<em>${esc(project.namespace || '')}</em></strong>
      ${project.description ? `<small class="gl-project-desc">${esc(project.description)}</small>` : ''}
      <small class="gl-mr-foot"><span class="gl-status is-${said[0]}">${esc(said[1])}</span>${project.stars ? `<span class="gl-stars">${glIcon('star', 11)}${Number(project.stars)}</span>` : ''}<span>${esc(timeShort(project.lastActivityAt))}</span></small>
    </span>
  </button>`
}

export function projectsBody(state) {
  const projects = state.projects
  const response = projects.lists[`${projects.scope}:${projects.search}`]
  const head = `<header class="nc-sub gl-projects-head">
    <div class="nc-seg" role="group" aria-label="Какие проекты">${SCOPES.map(([id, label, title]) =>
      `<button type="button" class="${projects.scope === id ? 'is-on' : ''}" data-action="gitlab-projects-scope" data-scope="${id}" title="${esc(title)}" aria-pressed="${projects.scope === id ? 'true' : 'false'}">${esc(label)}</button>`).join('')}</div>
    <input id="${draftId('projectSearch')}" class="gl-input gl-search" data-draft="projectSearch" value="${esc(state.drafts.projectSearch ?? projects.search)}" placeholder="Поиск: имя или группа/имя" autocomplete="off" spellcheck="false">
    <button type="button" class="nc-icon-btn" data-action="gitlab-projects-search" title="Найти" aria-label="Найти">${glIcon('search', 13)}</button>
  </header>`
  let body
  if (!response) body = loadingHtml('Загружаем проекты…')
  else if (response.state !== 'ok') body = `<div class="gl-pad">${problemHtml(response, { retry: 'gitlab-reload' })}</div>`
  else {
    const items = Array.isArray(response.data?.items) ? response.data.items : []
    const current = String(response.data?.current || '')
    if (!items.length) {
      body = `<div class="point-tool-empty compact"><strong>${projects.search ? 'Ничего не нашлось' : 'Проектов нет'}</strong><p>${esc(projects.search ? `По запросу «${projects.search}» проектов нет.` : 'GitLab не знает проектов, где вы участник.')}</p>${projects.search ? '<button type="button" class="gl-btn" data-action="gitlab-projects-clear">Сбросить поиск</button>' : ''}</div>`
    } else {
      body = GROUPS.map(([id, label]) => {
        const rows = items.filter(project => group(project, current) === id)
        return rows.length ? `<section class="gl-group is-${id}"><h3><span>${esc(label)}</span><em>${rows.length}</em></h3>${rows.map(project => projectRow(project, current)).join('')}</section>` : ''
      }).join('')
    }
  }
  return `${head}<div class="nc-scroll gl-scroll">${body}</div>`
}
