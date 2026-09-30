// Окно GitLab: merge requests и пайплайны проекта открытой папки.
//
// Регистр — тот же, что у окна Git (`nc-*` из 96-tool-windows.css): окна
// инструментов стоят в панели рядом и обязаны выглядеть одним продуктом. Своё
// у GitLab только то, чего у Git нет: состояние MR, пайплайн, одобрения.
//
// Всё, что пришло из GitLab, — недоверенный текст: он идёт через esc, ссылки
// «в GitLab» открывает хост и только на свой сервер.

import { esc } from './html-escape.js'

const GL_ICONS = {
  mr: 'M4.5 5.5v5m0-5a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Zm0 5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3Zm7 0V6.5a2 2 0 0 0-2-2H7.5m1.5-2-2 2 2 2m2.5 6a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3Z',
  pipeline: 'M2.5 8h2m7 0h2M4.5 8a1.5 1.5 0 1 0 3 0 1.5 1.5 0 0 0-3 0Zm4 0a1.5 1.5 0 1 0 3 0 1.5 1.5 0 0 0-3 0Z',
  refresh: 'M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2.5V6H10',
  settings: 'M8 10a2 2 0 1 0 0-4 2 2 0 0 0 0 4Zm5.2-2a5 5 0 0 0-.1-1l1.4-1.1-1.4-2.4-1.7.6a5 5 0 0 0-1.7-1L9.4 1.5H6.6l-.3 1.6a5 5 0 0 0-1.7 1l-1.7-.6-1.4 2.4L2.9 7a5 5 0 0 0 0 2l-1.4 1.1 1.4 2.4 1.7-.6a5 5 0 0 0 1.7 1l.3 1.6h2.8l.3-1.6a5 5 0 0 0 1.7-1l1.7.6 1.4-2.4L13.1 9a5 5 0 0 0 .1-1Z',
  external: 'M9.5 2.5h4v4M13.5 2.5 8 8M11.5 9.5v3.5a.5.5 0 0 1-.5.5H3a.5.5 0 0 1-.5-.5V5a.5.5 0 0 1 .5-.5h3.5',
  retry: 'M13 8a5 5 0 1 1-1.6-3.7M13 2.5v3h-3',
  log: 'M3.5 2.5h9v11h-9zM5.5 5.5h5M5.5 8h5M5.5 10.5h3',
  caretRight: 'm6.5 4.5 3.5 3.5-3.5 3.5',
  caretDown: 'm4.5 6.5 3.5 3.5 3.5-3.5',
  warning: 'M8 6v3.5M8 11.6v.1M7.1 2.6 1.7 12a1 1 0 0 0 .9 1.5h10.8a1 1 0 0 0 .9-1.5L8.9 2.6a1 1 0 0 0-1.8 0Z',
  check: 'm3.5 8.5 3 3 6-6.5',
  x: 'm4.5 4.5 7 7m0-7-7 7',
  comment: 'M2.5 3.5h11v7.5H7l-3 2.5V11H2.5z',
  branch: 'M5 4.5a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Zm0 0v7m0 0a1.5 1.5 0 1 0-3 0 1.5 1.5 0 0 0 3 0ZM12.5 4.5a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0ZM11 6v.5a3 3 0 0 1-3 3H5',
}

export function glIcon(name, size = 13) {
  const path = GL_ICONS[name]
  return path ? `<svg class="nc-icon" width="${size}" height="${size}" viewBox="0 0 16 16" aria-hidden="true"><path d="${path}"/></svg>` : ''
}

// Статусы пайплайна и джоба GitLab → тон и слово. Незнакомый статус — как есть.
const PIPELINE_STATUS = {
  success: ['ok', 'успешно'], passed: ['ok', 'успешно'], failed: ['bad', 'упал'], canceled: ['mute', 'отменён'],
  cancelled: ['mute', 'отменён'], skipped: ['mute', 'пропущен'], manual: ['wait', 'вручную'], running: ['run', 'идёт'],
  pending: ['wait', 'в очереди'], created: ['wait', 'создан'], preparing: ['wait', 'готовится'],
  waiting_for_resource: ['wait', 'ждёт ресурс'], scheduled: ['wait', 'по расписанию'],
}

export function pipelineStatus(status) {
  const [tone, label] = PIPELINE_STATUS[String(status || '')] || ['mute', String(status || '—')]
  return { tone, label }
}

export function statusMark(status) {
  const { tone, label } = pipelineStatus(status)
  const glyph = tone === 'ok' ? glIcon('check', 11) : tone === 'bad' ? glIcon('x', 11) : ''
  return `<i class="gl-mark is-${tone}" title="${esc(label)}" aria-label="${esc(label)}">${glyph}</i>`
}

// Состояние слияния GitLab (detailed_merge_status) — словом владельца.
const MERGE_STATUS = {
  mergeable: ['ok', 'можно слить'], can_be_merged: ['ok', 'можно слить'], checking: ['wait', 'проверяется'],
  unchecked: ['wait', 'не проверен'], ci_must_pass: ['wait', 'ждёт пайплайн'], ci_still_running: ['wait', 'идёт пайплайн'],
  discussions_not_resolved: ['warn', 'есть открытые обсуждения'], draft_status: ['mute', 'черновик'],
  not_approved: ['warn', 'нужны одобрения'], blocked_status: ['warn', 'заблокирован'], conflict: ['bad', 'конфликт'],
  broken_status: ['bad', 'не сливается'], cannot_be_merged: ['bad', 'не сливается'], need_rebase: ['warn', 'нужен rebase'],
  jira_association_missing: ['warn', 'нет задачи Jira'], not_open: ['mute', 'закрыт'], requested_changes: ['warn', 'просят изменений'],
}

export function mergeStatus(status) {
  const [tone, label] = MERGE_STATUS[String(status || '')] || ['mute', String(status || '').replace(/_/g, ' ')]
  return { tone, label }
}

export function timeAgo(value) {
  const at = Date.parse(String(value || ''))
  if (!Number.isFinite(at) || at <= 0) return ''
  const minutes = Math.round((Date.now() - at) / 60000)
  if (minutes < 1) return 'только что'
  if (minutes < 60) return `${minutes} мин назад`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours} ч назад`
  const days = Math.round(hours / 24)
  if (days < 30) return `${days} дн назад`
  return new Date(at).toLocaleDateString('ru-RU')
}

export function duration(seconds) {
  const total = Math.round(Number(seconds || 0))
  if (!total) return ''
  const minutes = Math.floor(total / 60)
  return minutes ? `${minutes} мин ${total % 60} с` : `${total} с`
}

export const shortSha = sha => String(sha || '').slice(0, 8)

// Инициалы вместо аватара: картинки GitLab окно не грузит (чужой адрес), а
// лицо в строке узнаётся быстрее имени. Цвет — от имени пользователя, чтобы
// один человек был одного цвета во всех строках.
export function glAvatar(user, { size = 'sm', title = true } = {}) {
  const name = String(user?.name || user?.username || '').trim()
  if (!name) return ''
  const letters = name.split(/\s+/).slice(0, 2).map(part => part[0]).join('').toUpperCase()
  let hash = 0
  for (const char of String(user?.username || name)) hash = (hash * 31 + char.codePointAt(0)) >>> 0
  return `<i class="gl-ava is-${size} is-c${hash % 5}"${title ? ` title="${esc(name)}${user?.username ? ` · @${esc(user.username)}` : ''}"` : ''} aria-hidden="true">${esc(letters)}</i>`
}

// «Draft:» GitLab пишет в название; черновик окно называет своим признаком.
export const cleanTitle = title => String(title || '').replace(/^\s*(\[draft\]|\(draft\)|draft:|wip:)\s*/i, '')

// Короткое время для правого края строки: «17 ч», «2 дн».
export function timeShort(value) {
  const at = Date.parse(String(value || ''))
  if (!Number.isFinite(at) || at <= 0) return ''
  const minutes = Math.round((Date.now() - at) / 60000)
  if (minutes < 1) return 'сейчас'
  if (minutes < 60) return `${minutes} мин`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours} ч`
  const days = Math.round(hours / 24)
  if (days < 30) return `${days} дн`
  return new Date(at).toLocaleDateString('ru-RU', { day: 'numeric', month: 'short' })
}

// Стабильный id поля по ключу черновика: перерисовка возвращает фокус и
// каретку только полю с id (captureUi в main.js).
export const draftId = key => `draft-${String(key).replace(/[^A-Za-z0-9_-]/g, '_')}`

// Сбой экрана: причина и следующий шаг ядра, кнопка — по причине.
export function problemHtml(response, { retry = '', surfaceAction = '' } = {}) {
  const reason = String(response?.reason || '')
  const open = ['not_configured', 'not_trusted', 'secret_locked', 'tool_missing'].includes(reason)
  const button = open
    ? `<button type="button" class="gl-btn is-primary" data-action="${esc(surfaceAction || 'gitlab-open-integrations')}">Открыть интеграции</button>`
    : retry ? `<button type="button" class="gl-btn" data-action="${esc(retry)}">Повторить</button>` : ''
  // Отказ GitLab (токен, сеть, ответ не той формы) — красным; остальное —
  // шаг настройки или временное состояние, жёлтым.
  const tone = ['auth', 'unreachable', 'format'].includes(reason) ? 'bad' : 'warn'
  return verdictHtml({ tone, title: response?.problem || 'GitLab не ответил', reasons: [response?.fix].filter(Boolean), actions: button, className: `gl-problem is-${reason || 'error'} is-stacked` })
}

// Вердикт — стиль всех экранов GitLab: одна фраза, можно ли и что дальше,
// причины словами, действия справа. reasons: строки или {text, action,
// data} — причина с действием становится ссылкой на свой экран.
export function verdictHtml({ tone = 'mute', title = '', reasons = [], actions = '', glyph = '', className = '' }) {
  const mark = glyph || { ok: 'check', bad: 'x', warn: 'warning', wait: 'pipeline', merged: 'mr' }[tone] || 'mr'
  const reason = item => {
    if (typeof item === 'string') return `<li>${esc(item)}</li>`
    if (!item?.action) return `<li>${esc(item?.text || '')}</li>`
    const data = Object.entries(item.data || {}).map(([key, value]) => ` data-${key}="${esc(value)}"`).join('')
    return `<li><button type="button" class="gl-link" data-action="${esc(item.action)}"${data}>${esc(item.text || '')}</button></li>`
  }
  const list = reasons.filter(Boolean)
  return `<section class="gl-verdict is-${esc(tone)}${className ? ` ${esc(className)}` : ''}">
    <i class="gl-verdict-mark">${glIcon(mark, 16)}</i>
    <div><strong>${esc(title)}</strong>${list.length ? `<ul>${list.map(reason).join('')}</ul>` : ''}</div>
    ${actions ? `<div class="gl-verdict-actions">${actions}</div>` : ''}
  </section>`
}

export function loadingHtml(text) {
  return `<div class="gl-loading"><span class="spinner"></span>${esc(text)}</div>`
}

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
    if (status.state !== 'ok' || status.data?.linked === false) {
      return shell(`<main class="nc-app gl-app">${head()}
        ${state.bindingOpen ? bindingEditor(state, binding) : ''}
        <div class="nc-scroll gl-scroll">${status.state === 'ok' ? unlinkedHtml(state, binding) : `<div class="gl-pad">${problemHtml(status, { retry: 'gitlab-reload' })}</div>`}</div></main>`)
    }
    const tabs = `<div class="nc-tabs" role="tablist" data-keynav="row">${[['mrs', 'mr', 'Merge requests'], ['pipelines', 'pipeline', 'Пайплайны']].map(([id, glyph, label]) =>
      `<button type="button" role="tab" class="nc-tab${state.section === id ? ' is-active' : ''}" aria-selected="${state.section === id ? 'true' : 'false'}" tabindex="${state.section === id ? '0' : '-1'}" data-action="gitlab-section" data-section="${id}">${glIcon(glyph, 13)}<span>${esc(label)}</span></button>`).join('')}</div>`
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
