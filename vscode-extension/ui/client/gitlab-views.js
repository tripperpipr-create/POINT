// Окно GitLab: merge requests, пайплайны и проекты.
//
// Регистр — тот же, что у окна Git (`nc-*` из 96-tool-windows.css): окна
// инструментов стоят в панели рядом и обязаны выглядеть одним продуктом. Своё
// у GitLab только то, чего у Git нет: состояние MR, пайплайн, одобрения.
//
// Раскладка — список и деталь: в широком окне выбранный MR или проект открыт
// справа (gitlab-window.js), в узкой панели строка открывает вкладку редактора.
// Окно без открытой папки смотрит на все проекты владельца: первым идёт
// каталог, вкладки «Пайплайны» нет — пайплайны живут в детали проекта.
//
// Всё, что пришло из GitLab, — недоверенный текст: он идёт через esc, ссылки
// «в GitLab» открывает хост и только на свой сервер.

import { esc } from './html-escape.js'
import { projectsBody } from './gitlab-projects-list.js'
import { cleanTitle, draftId, duration, glAvatar, glIcon, loadingHtml, mergeStatus, pipelineStatus, problemHtml, shortSha, statusMark, timeAgo, timeShort } from './gitlab-common.js'

const SCOPES = [
  ['mine', 'Мои', 'MR, которые вы открыли'],
  ['review', 'На ревью', 'MR, где вы ревьюер'],
  ['project', 'Все открытые', 'Открытые MR проекта папки'],
]

// Окно без папки — «все проекты»: каталог первым, пайплайнов в шапке нет.
export const isGlobal = state => state.status?.state === 'ok' && !state.status.data?.binding?.workspace

export function createGitLabToolView({ getState, shell, window: gitlabWindow }) {
  // Строка MR — одна линия: точка состояния, название, справа номер, ветка и
  // время. Без папки под названием стоит проект: строки из разных проектов.
  function mergeRequestRow(item, state) {
    const status = mergeStatus(item.draft ? 'draft_status' : item.mergeStatus)
    const why = [item.hasConflicts && status.label !== 'конфликт' ? 'конфликт' : '', item.blockingThreads && item.mergeStatus !== 'discussions_not_resolved' ? 'открытые обсуждения' : ''].filter(Boolean)
    const selected = gitlabWindow?.()?.selected('mr', item.projectPath, item.iid)
    const tone = item.hasConflicts ? 'bad' : status.tone
    return `<button type="button" class="gl-row${selected ? ' is-selected' : ''}" data-action="gitlab-open-mr" data-project="${esc(item.projectPath || '')}" data-iid="${Number(item.iid) || 0}" data-title="${esc(item.title || '')}" title="${esc([`${item.projectPath || ''}!${item.iid}`, status.label, ...why].join(' · '))}">
      <i class="gl-dot is-${esc(tone)}"></i>
      <span class="gl-row-main"><strong>${esc(cleanTitle(item.title) || 'Без названия')}</strong>${isGlobal(state) && item.projectPath ? `<small>${esc(item.projectPath)}</small>` : ''}</span>
      <span class="gl-row-meta">${glAvatar(item.author)}<span class="gl-branch">${esc(item.sourceBranch || '')}</span><em>!${Number(item.iid) || 0}</em><span>${esc(timeShort(item.updatedAt))}</span></span>
    </button>`
  }

  // Группы — по тому, чего MR ждёт: сначала то, где нужен человек.
  const GROUPS = [
    ['attention', 'Требуют внимания', item => ['bad', 'warn'].includes(mergeStatus(item.mergeStatus).tone) || item.hasConflicts],
    ['ready', 'Можно слить', item => mergeStatus(item.mergeStatus).tone === 'ok'],
    ['waiting', 'Ждут проверок', item => mergeStatus(item.mergeStatus).tone === 'wait'],
    ['draft', 'Черновики', () => true],
  ]

  function groupedRows(items, state) {
    const left = [...items]
    return GROUPS.map(([id, label, test]) => {
      const mine = left.filter(item => !(item.draft || item.mergeStatus === 'draft_status') || id === 'draft').filter(test)
      mine.forEach(item => left.splice(left.indexOf(item), 1))
      return mine.length ? `<section class="gl-group is-${id}"><h3><span>${esc(label)}</span><em>${mine.length}</em></h3>${mine.map(item => mergeRequestRow(item, state)).join('')}</section>` : ''
    }).join('')
  }

  function mergeRequestsBody(state) {
    const response = state.lists[state.scope]
    if (!response) return loadingHtml('Загружаем merge requests…')
    if (response.state !== 'ok') return `<div class="gl-pad">${problemHtml(response, { retry: 'gitlab-reload' })}</div>`
    const favorites = new Set(state.projects?.prefs?.favorites || [])
    const all = Array.isArray(response.data?.items) ? response.data.items : []
    const items = isGlobal(state) && state.projects?.prefs?.mrFav ? all.filter(item => favorites.has(item.projectPath)) : all
    if (!items.length) {
      const empty = { mine: 'Открытых MR, созданных вами, нет.', review: 'Сейчас вас не ждёт ни один MR.', project: 'Открытых MR в проекте нет.' }[state.scope]
      return `<div class="gl-empty"><strong>Пусто</strong><p>${esc(all.length ? 'У избранных проектов таких MR нет.' : empty)}</p></div>`
    }
    return groupedRows(items, state)
  }

  // Джобы — по этапам, как их видит GitLab: build, test, deploy. Лог и повтор
  // появляются под указателем, у упавшего джоба видны всегда.
  function jobRows(state, project, pipelineId) {
    const response = state.jobs[pipelineId]
    if (!response) return loadingHtml('Загружаем джобы…')
    if (response.state !== 'ok') return problemHtml(response)
    const jobs = Array.isArray(response.data?.jobs) ? response.data.jobs : []
    if (!jobs.length) return '<p class="gl-muted gl-jobs-empty">Джобов нет.</p>'
    const stages = [...new Set(jobs.map(job => String(job.stage || '')))]
    return `<div class="gl-jobs">${stages.map(stage => `${stage ? `<h4>${esc(stage)}</h4>` : ''}${jobs.filter(job => String(job.stage || '') === stage).map(job => `<div class="gl-job${pipelineStatus(job.status).tone === 'bad' ? ' is-failed' : ''}">
      ${statusMark(job.status)}
      <span><b>${esc(job.name || '')}</b>${job.failureReason ? `<small>${esc(job.failureReason.replace(/_/g, ' '))}</small>` : ''}${job.allowFailure ? '<small class="is-mute">можно падать</small>' : ''}</span>
      <em>${esc(duration(job.duration) || pipelineStatus(job.status).label)}</em>
      <span class="gl-job-tools"><button type="button" class="nc-icon-btn" data-action="gitlab-job-log" data-project="${esc(project)}" data-job="${Number(job.id) || 0}" data-name="${esc(job.name || '')}" title="Открыть лог джоба в редакторе" aria-label="Лог джоба ${esc(job.name || '')}">${glIcon('log', 13)}</button><button type="button" class="nc-icon-btn" data-action="gitlab-retry-job" data-project="${esc(project)}" data-job="${Number(job.id) || 0}" data-pipeline="${Number(pipelineId) || 0}" title="Перезапустить джоб" aria-label="Перезапустить ${esc(job.name || '')}"${state.busy ? ' disabled' : ''}>${glIcon('retry', 13)}</button></span>
    </div>`).join('')}`).join('')}</div>`
  }

  function pipelineRows(state, response) {
    if (!response) return loadingHtml('Загружаем пайплайны…')
    if (response.state !== 'ok') return `<div class="gl-pad">${problemHtml(response, { retry: 'gitlab-reload' })}</div>`
    const items = Array.isArray(response.data?.items) ? response.data.items : []
    const project = String(response.data?.project || '')
    // Ветка списка уже названа над ним; строка называет её, только если
    // пайплайн шёл по другой.
    const listRef = String(response.data?.ref || '')
    if (!items.length) return `<div class="gl-empty"><strong>Пайплайнов нет</strong><p>${esc(response.data?.ref ? `Для ветки ${response.data.ref} GitLab пайплайнов не запускал.` : 'Пайплайнов ещё не было.')}</p></div>`
    return items.map(pipeline => {
      const open = state.openPipeline === pipeline.id
      const { tone, label } = pipelineStatus(pipeline.status)
      return `<div class="gl-pipeline${open ? ' is-open' : ''}">
        <button type="button" class="gl-pipe-row" data-action="gitlab-toggle-pipeline" data-project="${esc(project)}" data-pipeline="${Number(pipeline.id) || 0}" aria-expanded="${open ? 'true' : 'false'}">
          ${statusMark(pipeline.status)}<strong>#${Number(pipeline.id) || 0}</strong><span class="gl-status is-${esc(tone)}">${esc(label)}</span>
          <span class="gl-row-meta"><span class="gl-sha">${esc(shortSha(pipeline.sha))}</span>${pipeline.ref && pipeline.ref !== listRef ? `<span class="gl-branch">${esc(pipeline.ref)}</span>` : ''}${pipeline.duration ? `<span>${esc(duration(pipeline.duration))}</span>` : ''}<span>${esc(timeAgo(pipeline.createdAt))}</span></span>
          ${glIcon(open ? 'caretDown' : 'caretRight', 12)}
        </button>
        ${open ? jobRows(state, project, pipeline.id) : ''}
      </div>`
    }).join('')
  }

  // Связь проекта с GitLab: окно и вкладка проекта «GitLab» правят её одним
  // редактором. В окне он всплывает под шапкой, на вкладке открыт всегда.
  function bindingEditor(state, binding, { cancel = true } = {}) {
    const mode = state.drafts.bindingMode || binding.mode || 'auto'
    const radio = (value, label, hint) => `<label class="gl-radio"><input type="radio" name="gitlab-binding-mode" value="${value}" data-action="gitlab-binding-mode"${mode === value ? ' checked' : ''}><span><b>${esc(label)}</b><small>${esc(hint)}</small></span></label>`
    // Подсказка у «По git remote» говорит, что даст origin, при любом текущем
    // выборе: Note к этому моменту может объяснять уже другое.
    const origin = binding.detected ? `origin → ${binding.detected}`
      : binding.remote ? `origin ведёт на ${binding.remote.split('/')[0]} — не на этот GitLab` : 'у папки нет git remote origin'
    return `<section class="gl-binding-edit" aria-label="Связь проекта с GitLab">
      ${cancel ? '<h4>Что показывать в окне</h4>' : ''}
      ${radio('auto', 'По git remote', origin)}
      ${radio('manual', 'Выбрать проект', 'путь вида группа/проект')}
      ${mode === 'manual' ? `<input id="${draftId('bindingProject')}" class="gl-input is-sub" data-draft="bindingProject" value="${esc(state.drafts.bindingProject ?? binding.manual ?? '')}" placeholder="group/project" autocomplete="off" spellcheck="false">` : ''}
      ${radio('all', 'Все мои проекты', 'MR, где вы автор или ревьюер, без привязки к папке')}
      ${radio('off', 'Не связывать', 'проект живёт вне этого GitLab — окно молчит, сервер плагина ради него не запускается')}
      ${mode === 'off' ? '' : `<label class="gl-field"><span>Имя пользователя GitLab <small>необязательно — Point узнаёт его по токену</small></span><input id="${draftId('bindingUsername')}" class="gl-input" data-draft="bindingUsername" value="${esc(state.drafts.bindingUsername ?? binding.username ?? '')}" placeholder="${esc(state.status?.data?.user?.username || 'username')}" autocomplete="off" spellcheck="false"></label>`}
      <footer>${cancel ? '<button type="button" class="gl-btn is-quiet" data-action="gitlab-binding-cancel">Отмена</button>' : ''}<button type="button" class="gl-btn is-primary" data-action="gitlab-binding-save"${state.busy ? ' disabled' : ''}>Сохранить</button></footer>
    </section>`
  }

  // Проект вне GitLab — не сбой: спокойная строка и один шаг, без красного.
  // Если origin всё же ведёт на этот GitLab (связь выключили вручную), шаг —
  // один клик; иначе — выбор проекта в редакторе.
  function unlinkedHtml(state, binding) {
    const name = binding.workspace ? `Проект «${binding.workspace}»` : 'Проект'
    const actions = state.bindingOpen ? ''
      : binding.detected ? `<button type="button" class="gl-btn is-primary" data-action="gitlab-link-detected"${state.busy ? ' disabled' : ''}>Связать с ${esc(binding.detected)}</button><button type="button" class="gl-btn is-quiet" data-action="gitlab-binding-toggle">Другой проект…</button>`
        : '<button type="button" class="gl-btn" data-action="gitlab-binding-toggle">Связать…</button>'
    return `<div class="gl-empty"><strong>${esc(`${name} не связан с GitLab`)}</strong><p>${esc(binding.note || 'окно покажет merge requests и пайплайны, когда проект будет связан')}</p>${actions ? `<div class="gl-empty-acts">${actions}</div>` : ''}</div>`
  }

  // Список слева, деталь справа. Узкая панель детали не показывает (CSS), и
  // строка там открывает вкладку редактора.
  const split = (state, section, list) => {
    const detail = gitlabWindow?.()?.detailHtml(section) || ''
    return `<div class="gl-split${detail ? ' has-detail' : ''}"><section class="gl-list">${list}</section>${detail ? `<section class="gl-detail">${detail}</section>` : ''}</div>`
  }

  function toolView() {
    const state = getState()
    gitlabWindow?.()?.prepare()
    const status = state.status
    const global = isGlobal(state)
    const binding = status?.data?.binding || {}
    const where = global ? 'Все проекты' : binding.mode === 'all' ? 'Все мои проекты' : binding.project || ''
    const user = status?.data?.user?.username ? `@${status.data.user.username}` : ''
    // Общее окно к папке не привязано: «где смотрим» — подпись, а не выбор связи.
    const strip = status?.state === 'ok' && where ? `<button type="button" class="gl-where"${global ? ' tabindex="-1"' : ' data-action="gitlab-binding-toggle"'} title="${esc(binding.note || 'Что показывает окно')}"${status?.data?.configured ? '' : ' disabled'}>
      <span>${esc(where)}</span>${binding.branch && binding.mode !== 'all' && !global ? `<small>${glIcon('branch', 11)}${esc(binding.branch)}</small>` : ''}<em>${esc(user)}</em></button>` : ''
    const head = (tabs = '') => `<header class="nc-head gl-head">
      <span class="nc-brand">${glIcon('mr', 14)}<b>GitLab</b></span>
      ${tabs}
      <i class="nc-gap"></i>${strip}
      ${global ? '' : `<button type="button" class="nc-icon-btn${state.bindingOpen ? ' is-on' : ''}" data-action="gitlab-binding-toggle" title="Связь проекта с GitLab: по git remote, вручную, все мои проекты или не связывать" aria-label="Связь проекта с GitLab" aria-pressed="${state.bindingOpen ? 'true' : 'false'}"${status?.data?.configured ? '' : ' disabled'}>${glIcon('settings', 14)}</button>`}
      <button type="button" class="nc-icon-btn" data-action="gitlab-reload" title="Обновить" aria-label="Обновить">${glIcon('refresh', 14)}</button>
    </header>${strip ? `<div class="gl-strip">${strip}</div>` : ''}`
    const popup = binding => state.bindingOpen ? bindingEditor(state, binding) : ''
    if (!status) return shell(`<main class="nc-app gl-app">${head()}${loadingHtml('Спрашиваем GitLab…')}</main>`)
    if (status.state !== 'ok') {
      return shell(`<main class="nc-app gl-app">${head()}${popup(binding)}
        <div class="nc-scroll gl-scroll"><div class="gl-pad">${problemHtml(status, { retry: 'gitlab-reload' })}</div></div></main>`)
    }
    // «Проекты» не зависят от связи папки: вкладка открыта и в несвязанной.
    const sections = global ? [['projects', 'Проекты', 'Проекты GitLab: файлы, коммиты, ветки, клон'], ['mrs', 'MR', 'Ваши merge requests во всех проектах']]
      : [['mrs', 'MR', 'Merge requests'], ['pipelines', 'Пайплайны', 'Пайплайны ветки'], ['projects', 'Проекты', 'Проекты GitLab: файлы, коммиты, ветки, клон']]
    const section = sections.some(([id]) => id === state.section) ? state.section : sections[0][0]
    const count = state.lists[state.scope]?.data?.items?.length
    const tabs = `<div class="nc-tabs gl-tabs" role="tablist" data-keynav="row">${sections.map(([id, label, title]) =>
      `<button type="button" role="tab" class="nc-tab${section === id ? ' is-active' : ''}" aria-selected="${section === id ? 'true' : 'false'}" tabindex="${section === id ? '0' : '-1'}" data-action="gitlab-section" data-section="${id}" title="${esc(title)}"><span>${esc(label)}</span>${id === 'mrs' && Number.isInteger(count) ? `<b>${count}</b>` : ''}</button>`).join('')}</div>`
    const notice = state.notice
      ? `<div class="nc-notice gl-notice ${state.notice.tone === 'error' ? 'is-error' : 'is-ok'}">${glIcon(state.notice.tone === 'error' ? 'warning' : 'check', 14)}<p>${esc(state.notice.text)}</p><button type="button" class="nc-icon-btn" data-action="gitlab-dismiss-notice" aria-label="Скрыть">${glIcon('x', 12)}</button></div>`
      : ''
    const frame = body => shell(`<main class="nc-app gl-app">${head(tabs)}${popup(binding)}${notice}${body}</main>`)
    if (section === 'projects') return frame(split(state, 'projects', projectsBody(state)))
    if (status.data?.linked === false) return frame(`<div class="nc-scroll gl-scroll">${unlinkedHtml(state, binding)}</div>`)
    if (section === 'pipelines') {
      const ref = state.pipelines?.data?.ref || binding.branch || ''
      return frame(`<header class="gl-bar">${ref ? `<span class="gl-bar-label">Ветка <b>${esc(ref)}</b></span>` : ''}<i class="nc-gap"></i>${state.pipelines?.data?.items ? `<em class="gl-count">${state.pipelines.data.items.length}</em>` : ''}</header><div class="nc-scroll gl-scroll">${pipelineRows(state, state.pipelines)}</div>`)
    }
    const favorites = global ? `<button type="button" class="gl-filter${state.projects?.prefs?.mrFav ? ' is-on' : ''}" data-action="gitlab-mr-favorites" aria-pressed="${state.projects?.prefs?.mrFav ? 'true' : 'false'}" title="Только MR избранных проектов">${glIcon('star', 12)}<span>Избранные</span></button>` : ''
    const list = `<header class="gl-bar"><div class="nc-seg" role="group" aria-label="Какие MR">${SCOPES.map(([id, label, title]) =>
      `<button type="button" class="${state.scope === id ? 'is-on' : ''}" data-action="gitlab-scope" data-scope="${id}" title="${esc(title)}" aria-pressed="${state.scope === id ? 'true' : 'false'}"${id === 'project' && (binding.mode === 'all' || global) ? ' disabled' : ''}>${esc(label)}</button>`).join('')}</div><i class="nc-gap"></i>${favorites}</header>
      <div class="nc-scroll gl-scroll">${mergeRequestsBody(state)}</div>`
    return frame(split(state, 'mrs', list))
  }

  return { toolView, jobRows, pipelineRows, bindingEditor }
}
