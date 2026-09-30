// Окно GitLab: merge requests и пайплайны проекта открытой папки.
//
// Регистр — тот же, что у окна Git (`nc-*` из 96-tool-windows.css): окна
// инструментов стоят в панели рядом и обязаны выглядеть одним продуктом. Своё
// у GitLab только то, чего у Git нет: состояние MR, пайплайн, одобрения.
//
// Всё, что пришло из GitLab, — недоверенный текст: он идёт через esc, ссылки
// «в GitLab» открывает хост и только на свой сервер.

import { esc } from './html-escape.js'
import { projectsBody } from './gitlab-projects-list.js'
import { cleanTitle, draftId, duration, glAvatar, glIcon, loadingHtml, mergeStatus, pipelineStatus, problemHtml, shortSha, statusMark, timeAgo, timeShort, verdictHtml } from './gitlab-common.js'

const SCOPES = [
  ['mine', 'Мои', 'MR, которые вы открыли'],
  ['review', 'На ревью', 'MR, где вы ревьюер'],
  ['project', 'Все открытые', 'Открытые MR проекта папки'],
]

export function createGitLabToolView({ getState, shell }) {
  // Строка MR — название и одна фраза о том, что с ним сейчас: окно отвечает
  // на вопрос «что мне делать», а не перечисляет поля.
  function mergeRequestRow(item) {
    const status = mergeStatus(item.mergeStatus)
    const why = [
      item.hasConflicts ? 'конфликт' : '',
      item.blockingThreads ? 'открытые обсуждения' : '',
    ].filter(Boolean).filter(word => word !== status.label && !(word === 'открытые обсуждения' && item.mergeStatus === 'discussions_not_resolved'))
    const project = item.projectPath ? `${item.projectPath}` : ''
    return `<button type="button" class="nc-row gl-mr-row gl-mr-verdict" data-action="gitlab-open-mr" data-project="${esc(item.projectPath || '')}" data-iid="${Number(item.iid) || 0}" data-title="${esc(item.title || '')}" title="${esc(`${project}!${item.iid} · ${item.title || ''}`)}">
      ${glAvatar(item.author, { size: 'md' })}
      <span>
        <strong>${esc(cleanTitle(item.title) || 'Без названия')}</strong>
        <small><span class="gl-status is-${esc(status.tone)}">${esc([status.label, ...why].join(' · '))}</span></small>
        <small class="gl-mr-foot"><em>!${Number(item.iid) || 0}</em><span class="gl-branch">${esc(item.sourceBranch || '')}</span><span>${esc(timeShort(item.updatedAt))}</span></small>
      </span>
    </button>`
  }

  // Группы — по тому, чего MR ждёт: сначала то, где нужен человек.
  const GROUPS = [
    ['attention', 'Требуют внимания', item => ['bad', 'warn'].includes(mergeStatus(item.mergeStatus).tone) || item.hasConflicts],
    ['ready', 'Можно слить', item => mergeStatus(item.mergeStatus).tone === 'ok'],
    ['waiting', 'Ждут проверок', item => mergeStatus(item.mergeStatus).tone === 'wait'],
    ['draft', 'Черновики', () => true],
  ]

  function groupedRows(items) {
    const left = [...items]
    return GROUPS.map(([id, label, test]) => {
      const mine = left.filter(item => !(item.draft || item.mergeStatus === 'draft_status') || id === 'draft').filter(test)
      mine.forEach(item => left.splice(left.indexOf(item), 1))
      return mine.length ? `<section class="gl-group is-${id}"><h3><span>${esc(label)}</span><em>${mine.length}</em></h3>${mine.map(mergeRequestRow).join('')}</section>` : ''
    }).join('')
  }

  function mergeRequestsBody(state) {
    const response = state.lists[state.scope]
    if (!response) return loadingHtml('Загружаем merge requests…')
    if (response.state !== 'ok') return problemHtml(response, { retry: 'gitlab-reload' })
    const items = Array.isArray(response.data?.items) ? response.data.items : []
    if (!items.length) {
      const empty = { mine: 'Открытых MR, созданных вами, нет.', review: 'Сейчас вас не ждёт ни один MR.', project: 'Открытых MR в проекте нет.' }[state.scope]
      return `<div class="point-tool-empty compact"><strong>Пусто</strong><p>${esc(empty)}</p></div>`
    }
    return groupedRows(items)
  }

  function jobRows(state, project, pipelineId) {
    const response = state.jobs[pipelineId]
    if (!response) return loadingHtml('Загружаем джобы…')
    if (response.state !== 'ok') return problemHtml(response)
    const jobs = Array.isArray(response.data?.jobs) ? response.data.jobs : []
    if (!jobs.length) return '<p class="gl-muted">Джобов нет.</p>'
    return `<ul class="gl-jobs">${jobs.map(job => `<li>
      ${statusMark(job.status)}
      <span><b>${esc(job.name || '')}</b><small>${esc(job.stage || '')}${job.duration ? ` · ${esc(duration(job.duration))}` : ''}${job.failureReason ? ` · ${esc(job.failureReason.replace(/_/g, ' '))}` : ''}${job.allowFailure ? ' · можно падать' : ''}</small></span>
      <button type="button" class="nc-icon-btn" data-action="gitlab-job-log" data-project="${esc(project)}" data-job="${Number(job.id) || 0}" data-name="${esc(job.name || '')}" title="Открыть лог джоба в редакторе" aria-label="Лог джоба ${esc(job.name || '')}">${glIcon('log', 13)}</button>
      <button type="button" class="nc-icon-btn" data-action="gitlab-retry-job" data-project="${esc(project)}" data-job="${Number(job.id) || 0}" data-pipeline="${Number(pipelineId) || 0}" title="Перезапустить джоб" aria-label="Перезапустить ${esc(job.name || '')}"${state.busy ? ' disabled' : ''}>${glIcon('retry', 13)}</button>
    </li>`).join('')}</ul>`
  }

  function pipelineRows(state, response) {
    if (!response) return loadingHtml('Загружаем пайплайны…')
    if (response.state !== 'ok') return problemHtml(response, { retry: 'gitlab-reload' })
    const items = Array.isArray(response.data?.items) ? response.data.items : []
    const project = String(response.data?.project || '')
    // Ветка списка уже названа над ним; строка называет её, только если
    // пайплайн шёл по другой.
    const listRef = String(response.data?.ref || '')
    if (!items.length) return `<div class="point-tool-empty compact"><strong>Пайплайнов нет</strong><p>${esc(response.data?.ref ? `Для ветки ${response.data.ref} GitLab пайплайнов не запускал.` : 'Пайплайнов ещё не было.')}</p></div>`
    return items.map(pipeline => {
      const open = state.openPipeline === pipeline.id
      const { label } = pipelineStatus(pipeline.status)
      return `<div class="gl-pipeline${open ? ' is-open' : ''}">
        <button type="button" class="nc-row" data-action="gitlab-toggle-pipeline" data-project="${esc(project)}" data-pipeline="${Number(pipeline.id) || 0}" aria-expanded="${open ? 'true' : 'false'}">
          ${statusMark(pipeline.status)}
          <span><strong>#${Number(pipeline.id) || 0} · ${esc(label)}</strong><small><span class="nc-hash">${esc(shortSha(pipeline.sha))}</span>${pipeline.ref && pipeline.ref !== listRef ? `<span>${esc(pipeline.ref)}</span>` : ''}${pipeline.duration ? `<span>${esc(duration(pipeline.duration))}</span>` : ''}<span>${esc(timeAgo(pipeline.createdAt))}</span></small></span>
          ${glIcon(open ? 'caretDown' : 'caretRight', 12)}
        </button>
        ${open ? jobRows(state, project, pipeline.id) : ''}
      </div>`
    }).join('')
  }

  // Связь проекта с GitLab: окно и вкладка проекта «GitLab» правят её одним
  // редактором. На вкладке он открыт всегда, поэтому без «Отмены».
  function bindingEditor(state, binding, { cancel = true } = {}) {
    const mode = state.drafts.bindingMode || binding.mode || 'auto'
    const radio = (value, label, hint) => `<label class="gl-radio"><input type="radio" name="gitlab-binding-mode" value="${value}" data-action="gitlab-binding-mode"${mode === value ? ' checked' : ''}><span><b>${esc(label)}</b><small>${esc(hint)}</small></span></label>`
    // Подсказка у «По git remote» говорит, что даст origin, при любом текущем
    // выборе: Note к этому моменту может объяснять уже другое.
    const origin = binding.detected ? `origin → ${binding.detected}`
      : binding.remote ? `origin ведёт на ${binding.remote.split('/')[0]} — не на этот GitLab` : 'у папки нет git remote origin'
    return `<section class="gl-binding-edit" aria-label="Связь проекта с GitLab">
      ${radio('auto', 'По git remote', origin)}
      ${radio('manual', 'Выбрать проект', 'путь вида группа/проект')}
      ${mode === 'manual' ? `<input id="${draftId('bindingProject')}" class="gl-input is-sub" data-draft="bindingProject" value="${esc(state.drafts.bindingProject ?? binding.manual ?? '')}" placeholder="group/project" autocomplete="off" spellcheck="false">` : ''}
      ${radio('all', 'Все мои проекты', 'MR, где вы автор или ревьюер, без привязки к папке')}
      ${radio('off', 'Не связывать', 'проект живёт вне этого GitLab — окно молчит, сервер плагина ради него не запускается')}
      ${mode === 'off' ? '' : `<label class="gl-field"><span>Имя пользователя GitLab <small>необязательно — Point узнаёт его по токену</small></span><input id="${draftId('bindingUsername')}" class="gl-input" data-draft="bindingUsername" value="${esc(state.drafts.bindingUsername ?? binding.username ?? '')}" placeholder="${esc(state.status?.data?.user?.username || 'username')}" autocomplete="off" spellcheck="false"></label>`}
      <footer><button type="button" class="gl-btn is-primary" data-action="gitlab-binding-save"${state.busy ? ' disabled' : ''}>Сохранить</button>${cancel ? '<button type="button" class="gl-btn" data-action="gitlab-binding-cancel">Отмена</button>' : ''}</footer>
    </section>`
  }

  // Проект вне GitLab — не сбой: спокойная строка и один шаг, без красного.
  // Если origin всё же ведёт на этот GitLab (связь выключили вручную), шаг —
  // один клик; иначе — выбор проекта в редакторе.
  function unlinkedHtml(state, binding) {
    const name = binding.workspace ? `Проект «${binding.workspace}»` : 'Проект'
    const actions = state.bindingOpen ? ''
      : binding.detected ? `<button type="button" class="gl-btn is-primary" data-action="gitlab-link-detected"${state.busy ? ' disabled' : ''}>Связать с ${esc(binding.detected)}</button><button type="button" class="gl-btn" data-action="gitlab-binding-toggle">Другой проект…</button>`
        : '<button type="button" class="gl-btn" data-action="gitlab-binding-toggle">Связать…</button>'
    return `<div class="gl-pad">${verdictHtml({ tone: 'mute', glyph: 'mr', title: `${name} не связан с GitLab`, className: 'is-stacked',
      reasons: [binding.note || 'окно покажет merge requests и пайплайны, когда проект будет связан'], actions })}</div>`
  }

  function toolView() {
    const state = getState()
    const status = state.status
    const head = (tabs = '') => `<header class="nc-head">
      <span class="nc-brand">${glIcon('mr', 14)}<b>GitLab</b></span>
      ${tabs}
      <i class="nc-gap"></i>
      <button type="button" class="nc-icon-btn${state.bindingOpen ? ' is-on' : ''}" data-action="gitlab-binding-toggle" title="Связь проекта с GitLab: по git remote, вручную, все мои проекты или не связывать" aria-label="Связь проекта с GitLab" aria-pressed="${state.bindingOpen ? 'true' : 'false'}"${status?.data?.configured ? '' : ' disabled'}>${glIcon('settings', 14)}</button>
      <button type="button" class="nc-icon-btn" data-action="gitlab-reload" title="Обновить" aria-label="Обновить">${glIcon('refresh', 14)}</button>
    </header>`
    if (!status) return shell(`<main class="nc-app gl-app">${head()}${loadingHtml('Спрашиваем GitLab…')}</main>`)
    const binding = status.data?.binding || {}
    if (status.state !== 'ok') {
      return shell(`<main class="nc-app gl-app">${head()}
        ${state.bindingOpen ? bindingEditor(state, binding) : ''}
        <div class="nc-scroll gl-scroll"><div class="gl-pad">${problemHtml(status, { retry: 'gitlab-reload' })}</div></div></main>`)
    }
    // «Проекты» не зависят от связи папки: вкладка открыта и в несвязанной.
    const tabs = `<div class="nc-tabs" role="tablist" data-keynav="row">${[['mrs', 'mr', 'MR', 'Merge requests'], ['pipelines', 'pipeline', 'Пайплайны', 'Пайплайны ветки'], ['projects', 'folder', 'Проекты', 'Проекты GitLab: файлы, коммиты, ветки, клон']].map(([id, glyph, label, title]) =>
      `<button type="button" role="tab" class="nc-tab${state.section === id ? ' is-active' : ''}" aria-selected="${state.section === id ? 'true' : 'false'}" tabindex="${state.section === id ? '0' : '-1'}" data-action="gitlab-section" data-section="${id}" title="${esc(title)}">${glIcon(glyph, 13)}<span>${esc(label)}</span></button>`).join('')}</div>`
    if (state.section === 'projects') return shell(`<main class="nc-app gl-app">${head(tabs)}${projectsBody(state)}</main>`)
    if (status.data?.linked === false) {
      return shell(`<main class="nc-app gl-app">${head(tabs)}
        ${state.bindingOpen ? bindingEditor(state, binding) : ''}
        <div class="nc-scroll gl-scroll">${unlinkedHtml(state, binding)}</div></main>`)
    }
    const where = binding.mode === 'all' ? 'Все мои проекты' : binding.project || 'Проект не выбран'
    const user = status.data?.user?.username ? `@${status.data.user.username}` : ''
    const strip = `<button type="button" class="gl-where" data-action="gitlab-binding-toggle" title="${esc(binding.note || 'Сменить проект окна')}">
      <span>${esc(where)}</span>${binding.branch && binding.mode !== 'all' ? `<small>${glIcon('branch', 11)}${esc(binding.branch)}</small>` : ''}<em>${esc(user)}</em></button>`
    const notice = state.notice
      ? `<div class="nc-notice ${state.notice.tone === 'error' ? 'is-error' : 'is-ok'}">${glIcon(state.notice.tone === 'error' ? 'warning' : 'check', 14)}<p>${esc(state.notice.text)}</p><button type="button" class="nc-icon-btn" data-action="gitlab-dismiss-notice" aria-label="Скрыть">${glIcon('x', 12)}</button></div>`
      : ''
    let body
    if (state.section === 'pipelines') {
      // Ветку списка называет строка проекта над ним — второй подписи не нужно.
      body = `<div class="nc-scroll gl-scroll">${pipelineRows(state, state.pipelines)}</div>`
    } else {
      const count = state.lists[state.scope]?.data?.items?.length
      body = `<header class="nc-sub gl-scopes"><div class="nc-seg" role="group" aria-label="Какие MR">${SCOPES.map(([id, label, title]) =>
        `<button type="button" class="${state.scope === id ? 'is-on' : ''}" data-action="gitlab-scope" data-scope="${id}" title="${esc(title)}" aria-pressed="${state.scope === id ? 'true' : 'false'}"${id === 'project' && binding.mode === 'all' ? ' disabled' : ''}>${esc(label)}</button>`).join('')}</div><i class="nc-gap"></i>${Number.isInteger(count) ? `<em class="gl-count">${count}</em>` : ''}</header>
        <div class="nc-scroll gl-scroll">${mergeRequestsBody(state)}</div>`
    }
    return shell(`<main class="nc-app gl-app">${head(tabs)}${strip}${state.bindingOpen ? bindingEditor(state, binding) : ''}${notice}${body}</main>`)
  }

  return { toolView, jobRows, pipelineRows, bindingEditor }
}
