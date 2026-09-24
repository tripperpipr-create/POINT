// Композер Мастера: очередь реплик во время хода, возврат по «↑» и команды «/».
//
// Всё это — поведение поля ввода, а не ленты, и в main.js ему места нет: тот
// стоит у своей границы (scripts/check-release-contracts.mjs). Здесь живут
// состояние списка команд, правила очереди и разметка её чипов; main.js
// только передаёт сюда нажатия и отрисовку.

import { masterParseAnswers } from './master-questions-views.js'
import { icon } from './ui-icons.js'

// ——— Очередь реплик ———
//
// Раньше во время хода отправка была заперта: «Мастер ещё отвечает, отправьте
// после». Мысль, пришедшая, пока модель думает, ждала в поле, а человек — конца
// хода, чтобы нажать Enter ещё раз. Теперь Enter ставит её в очередь, и она
// уходит сама, как только ход кончится.
//
// Сама — не всегда. Реплика, написанная до ответа, писалась без него, и есть
// случаи, когда второй взгляд на неё обязателен: Мастер задал уточнения (её
// приняли бы за ответ на них), ход остановили или он сорвался, ход кончился в
// другом чате, панель перезапускалась. Тогда очередь встаёт на паузу, и
// реплика ждёт кнопки «Отправить».
export const MASTER_QUEUE_LIMIT = 5

const PAUSE_REASONS = {
  questions: 'Мастер задал уточнения — ответьте на них или отправьте очередь',
  stopped: 'Ход остановлен — очередь ждёт вашего решения',
  failed: 'Ответ прервался — очередь ждёт вашего решения',
  elsewhere: 'Ход закончился, пока вы были в другом чате',
  reload: 'Панель перезапускалась — проверьте реплику перед отправкой',
}

export function masterQueueOf (client, id) {
  const queues = client.queue || (client.queue = {})
  if (!queues[id]) queues[id] = { items: [], paused: '' }
  return queues[id]
}

export function masterQueuePush (client, id, text) {
  const queue = masterQueueOf(client, id)
  if (queue.items.length >= MASTER_QUEUE_LIMIT) return false
  queue.items.push({ id: `q${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`, text })
  return true
}

export function masterQueueDrop (client, id, itemId) {
  const queue = masterQueueOf(client, id)
  const item = queue.items.find(entry => entry.id === itemId)
  queue.items = queue.items.filter(entry => entry.id !== itemId)
  if (!queue.items.length) queue.paused = ''
  return item || null
}

export function masterQueuePause (client, id, reason) {
  const queue = masterQueueOf(client, id)
  if (queue.items.length) queue.paused = reason
}

// Что делать с очередью, когда ход закончился. Возвращает реплику, которую пора
// отправить, или null — тогда очередь на паузе (или пуста).
export function masterQueueAfterTurn (client, id, { status = '', last = null, active = true } = {}) {
  const queue = masterQueueOf(client, id)
  if (!queue.items.length || queue.paused) return null
  const asked = last?.role === 'assistant' && ((last.questions || []).length || (last.clarifications || []).length)
  const reason = !active ? 'elsewhere'
    : status === 'cancelled' ? 'stopped'
      : status === 'failed' ? 'failed'
        : asked ? 'questions' : ''
  if (reason) { queue.paused = reason; return null }
  return queue.items.shift() || null
}

export function masterQueueHtml (queue, esc, { sending = false } = {}) {
  const items = queue?.items || []
  if (!items.length) return ''
  const rows = items.map((item, index) => {
    // Отправить вне очереди можно первую реплику и только между ходами: во
    // время хода её и так отправит конец хода.
    const send = index === 0 && !sending
      ? `<button type="button" class="hall-outbox-send" data-action="master-queue-send" data-id="${esc(item.id)}">Отправить</button>`
      : ''
    return `<li class="hall-outbox-item">
      <span class="hall-outbox-mark" aria-hidden="true">${icon('clock')}</span>
      <span class="hall-outbox-text" title="${esc(item.text)}">${esc(item.text)}</span>
      ${send}
      <button type="button" class="hall-outbox-icon" data-action="master-queue-edit" data-id="${esc(item.id)}" aria-label="Вернуть в поле" title="Вернуть в поле">${icon('edit')}</button>
      <button type="button" class="hall-outbox-icon" data-action="master-queue-drop" data-id="${esc(item.id)}" aria-label="Убрать из очереди" title="Убрать из очереди">${icon('x')}</button>
    </li>`
  }).join('')
  const note = queue.paused
    ? `<small class="hall-outbox-note">${esc(PAUSE_REASONS[queue.paused] || 'Очередь ждёт вашего решения')}</small>`
    : `<small class="hall-outbox-note">${sending ? 'Уйдёт после ответа Мастера' : 'Уйдёт следующей репликой'}</small>`
  return `<div class="hall-outbox${queue.paused ? ' is-paused' : ''}"><ol aria-label="Очередь реплик">${rows}</ol>${note}</div>`
}

// Кнопки чипов очереди. Возвращает true, если нажатие было её.
export function handleMasterQueueAction (action, target, { client, id, sending, send, setDraft, render, persist }) {
  if (!['master-queue-send', 'master-queue-edit', 'master-queue-drop'].includes(action)) return false
  const itemId = String(target?.dataset?.id || '')
  if (action === 'master-queue-send') {
    if (sending) return true
    const item = masterQueueDrop(client, id, itemId)
    if (item) send(item.text)
    return true
  }
  const item = masterQueueDrop(client, id, itemId)
  if (action === 'master-queue-edit' && item) setDraft(item.text)
  persist()
  render()
  return true
}

// ——— Меню разговора ———
// Меню композера (контекст, режим, подробность ответа) и «···» у карточек —
// `<details>`, а у `<details>` нет ни закрытия кликом мимо, ни Escape: меню
// висело открытым, пока по нему не щёлкнут ещё раз. Возвращает, закрылось ли
// что-то; `except` — узел, внутри которого щёлкнули (своё меню не трогаем).
const MENUS = ['hall-work-menu', 'hall-context-menu', 'hall-answer-style', 'hall-deck-menu']
  .map(name => `.hall-dialogue details.${name}[open]`).join(', ')

export function closeMasterMenus (root, except = null) {
  let closed = null
  for (const menu of root.querySelectorAll?.(MENUS) || []) {
    if (except && menu.contains?.(except)) continue
    menu.open = false
    closed = menu
  }
  return closed
}

// ——— «↑» в пустом поле ———
// Как в терминале и у эталона: стрелка вверх в пустом поле возвращает то, что
// писалось последним, — сперва последнюю реплику очереди (её, скорее всего, и
// хотят поправить), иначе последнюю свою реплику из разговора. Пакет ответов
// на уточнения репликой не считается: это служебный формат отправки.
export function masterRecallText (client, id, history = []) {
  const queue = masterQueueOf(client, id)
  const queued = queue.items.pop()
  if (queued) {
    if (!queue.items.length) queue.paused = ''
    return queued.text
  }
  for (let index = history.length - 1; index >= 0; index -= 1) {
    const item = history[index]
    if (item?.role !== 'user') continue
    const text = String(item.content || '')
    if (masterParseAnswers(text).length) continue
    return text
  }
  return ''
}

// ——— Команды «/» ———
//
// «/» в начале поля открывает список тем же видом и теми же клавишами, что
// «@»: стрелки, Enter или Tab — выбрать, Escape — закрыть. Своей логики у
// команд нет: каждая нажимает то, что уже есть в композере или в шапке, —
// двух путей к одному действию быть не должно.
const COMMANDS = [
  { id: 'discuss', label: 'Обсудить', hint: '/обсудить', aliases: ['обсудить', 'обсуждение', 'discuss', 'чат'], click: '[data-action="master-session-workMode"][data-value="discuss"]' },
  { id: 'plan', label: 'Спланировать', hint: '/план', aliases: ['план', 'спланировать', 'plan'], click: '[data-action="master-session-workMode"][data-value="plan"]' },
  { id: 'execute', label: 'Выполнить', hint: '/выполнить', aliases: ['выполнить', 'execute', 'run'], click: '[data-action="master-session-workMode"][data-value="execute"]' },
  { id: 'agent', label: 'Агент', hint: '/агент', aliases: ['агент', 'agent'], click: '[data-action="master-session-workMode"][data-value="agent"]' },
  { id: 'file', label: 'Приложить открытый файл', hint: '/файл', aliases: ['файл', 'открытый', 'file'], click: '[data-action="master-context-attach"]' },
  { id: 'source', label: 'Выбрать источник контекста', hint: '/контекст', aliases: ['контекст', 'источник', 'context', 'source'], click: '[data-action="master-context-pick"]' },
  { id: 'model', label: 'Сменить модель', hint: '/модель', aliases: ['модель', 'model'], click: '[data-action="toggle-model-picker"][data-target="master"]' },
  { id: 'find', label: 'Поиск по разговору', hint: '/поиск', aliases: ['поиск', 'найти', 'find', 'search'], run: 'find' },
  { id: 'new', label: 'Новый чат', hint: '/новый', aliases: ['новый', 'new'], run: 'new' },
]
// Запрос — от «/» в самом начале поля до каретки, без пробелов: «/план» —
// команда, а «/usr/bin/go» в середине фразы или путь с пробелом после — текст.
const SLASH = /^\/([\p{L}\p{N}-]*)$/u

let slash = { open: false, query: '', items: [], active: 0 }

export function masterSlashOpen () { return slash.open }
export function closeMasterSlash () { slash = { open: false, query: '', items: [], active: 0 } }
export function masterSlashActiveId () { return slash.open && slash.items.length ? `hall-slash-${slash.active}` : '' }

// Ввод в поле. Возвращает true, когда список открылся, закрылся или сменил
// состав — тогда нужна отрисовка.
export function masterSlashInput (value, caret) {
  const text = String(value || '')
  const at = Number.isFinite(caret) ? caret : text.length
  const match = text.slice(0, at).match(SLASH)
  if (!match) {
    const was = slash.open
    closeMasterSlash()
    return was
  }
  const query = match[1].toLowerCase()
  if (slash.open && slash.query === query) return false
  const items = COMMANDS.filter(command => !query || command.aliases.some(alias => alias.startsWith(query)) || command.label.toLowerCase().includes(query))
  slash = { open: true, query, items, active: 0 }
  return true
}

export function masterSlashHtml (esc) {
  if (!slash.open) return ''
  const rows = slash.items.map((command, index) => `<button type="button" class="hall-mention-row${index === slash.active ? ' is-active' : ''}" role="option" aria-selected="${index === slash.active}" id="hall-slash-${index}" data-action="master-slash-pick" data-index="${index}">${esc(command.label)}<small>${esc(command.hint)}</small></button>`).join('')
  const body = rows || '<p class="hall-mention-empty">Такой команды нет</p>'
  return `<div class="hall-mention hall-slash" id="master-slash-list" role="listbox" aria-label="Команды">${body}</div>`
}

// Выполнить команду: убрать «/запрос» из поля и нажать то, что команда
// называет. Нажатие идёт через обычный разбор кликов — с его же проверками.
function runCommand (command, { root, setDraft, draft, post, openFind, render }) {
  setDraft(String(draft() || '').replace(/^\/[\p{L}\p{N}-]*\s?/u, ''))
  closeMasterSlash()
  if (command.run === 'new') post({ type: 'masterSession', action: 'new' })
  else if (command.run === 'find') openFind()
  render()
  if (command.click) root.querySelector(command.click)?.click?.()
}

export function pickMasterSlash (index, deps) {
  const command = slash.items[Number(index) || 0]
  if (command) runCommand(command, deps)
}

// Клавиши поля ввода Мастера: список команд и «↑». Возвращает true, если
// нажатие обработано здесь (тогда вызывающий гасит действие по умолчанию).
export function handleMasterComposeKey (event, deps) {
  if (slash.open) {
    if (event.key === 'Escape') { closeMasterSlash(); deps.render(); return true }
    if (slash.items.length && (event.key === 'ArrowDown' || event.key === 'ArrowUp')) {
      const step = event.key === 'ArrowDown' ? 1 : -1
      slash = { ...slash, active: (slash.active + step + slash.items.length) % slash.items.length }
      deps.render()
      return true
    }
    if (slash.items.length && (event.key === 'Enter' || event.key === 'Tab') && !event.shiftKey && !event.isComposing) {
      runCommand(slash.items[slash.active], deps)
      return true
    }
    return false
  }
  if (event.key === 'ArrowUp' && !event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey
    && !String(event.target?.value || '') && !deps.mentionOpen()) {
    const text = masterRecallText(deps.client, deps.id(), deps.history())
    if (!text) return false
    deps.setDraft(text)
    deps.persist()
    deps.render()
    return true
  }
  return false
}
