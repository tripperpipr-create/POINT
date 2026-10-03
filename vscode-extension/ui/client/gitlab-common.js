// Общее для экранов GitLab: значки, слова статусов, время, инициалы, вердикт
// и сбой. Окно, карточка MR, карточка проекта и вкладки Гильдии рисуют одно и
// то же одинаково — отсюда.
//
// Всё, что пришло из GitLab, — недоверенный текст: он идёт через esc.

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
  folder: 'M2 4.5a1 1 0 0 1 1-1h3l1.5 1.5H13a1 1 0 0 1 1 1V12a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1z',
  file: 'M4 1.5h5l3 3V14a.5.5 0 0 1-.5.5h-7.5A.5.5 0 0 1 3.5 14V2a.5.5 0 0 1 .5-.5ZM9 1.5V5h3',
  copy: 'M5.5 5.5h7v8h-7zM3.5 10.5v-8h7',
  star: 'm8 2 1.8 3.7 4 .6-2.9 2.8.7 4L8 11.2l-3.6 1.9.7-4L2.2 6.3l4-.6z',
  clone: 'M8 2.5v8m0 0L5 7.5m3 3 3-3M3 12.5h10',
  commit: 'M5.5 8a2.5 2.5 0 1 0 5 0 2.5 2.5 0 0 0-5 0Zm0 0H1.5m9 0h4',
  search: 'M7 12a5 5 0 1 0 0-10 5 5 0 0 0 0 10Zm3.6-1.4L14 14',
  play: 'M5.5 4v8l6-4z', slash: 'M5 11 11 5', tab: 'M2.5 3.5h11v9h-11zM2.5 6h11',
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
  const glyph = { ok: glIcon('check', 9), bad: glIcon('x', 9), wait: glIcon('play', 8), mute: glIcon('slash', 9) }[tone] || ''
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

// Загрузка — строки-заготовки на месте будущего списка, подпись — тихая.
export function loadingHtml(text) {
  return `<div class="gl-loading" role="status"><i></i><i></i><i></i><span>${esc(text)}</span></div>`
}

// Строка состояния вместо вердикта на карточках: вывод, причины-ссылки на
// свою вкладку и действия одним рядом; тон несёт кромка и точка.
export function statusLine({ tone = 'mute', title = '', reasons = [], actions = '' }) {
  const reason = item => typeof item === 'string' ? `<span>${esc(item)}</span>`
    : item?.action ? `<button type="button" class="gl-link" data-action="${esc(item.action)}"${Object.entries(item.data || {}).map(([key, value]) => ` data-${key}="${esc(value)}"`).join('')}>${esc(item.text || '')}</button>` : `<span>${esc(item?.text || '')}</span>`
  return `<section class="gl-line is-${esc(tone)}"><div><i class="gl-dot is-${esc(tone)}"></i><strong>${esc(title)}</strong>${reasons.filter(Boolean).map(reason).join('')}</div>${actions ? `<div class="gl-line-acts">${actions}</div>` : ''}</section>`
}

