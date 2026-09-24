// Правила композера Мастера: во что обязана уложиться реплика человека и что он
// видит, пока ответа нет. Оба числа здесь парные к числам ядра, и сверяют их
// договорённости 25 и 26 в ui/contracts.mjs.
//
// Ход не запирает поле. Мысль, пришедшая, пока модель думает, должна куда-то
// записываться — раньше поле на это время гасло и очищалось, и записать её было
// некуда. Предохранитель от двойной отправки остался, но переехал с поля на
// кнопку и на ранний возврат sendMasterMessage; стережёт его
// scripts/smoke-master-send-unfreezes.js.

import { icon } from './ui-icons.js'

// Тот же предел, что и в ядре (maxChatMessage в internal/orchestrator/chat.go).
export const MASTER_MESSAGE_LIMIT_BYTES = 32 * 1024

// Порог предупреждения о долгом ответе держится заметно раньше таймаута
// обращения к модели в ядре (masterIntakeTimeoutSeconds в
// internal/orchestrator/task_intake.go): предупреждение после отката на движок
// Point — это не предупреждение, а объяснение задним числом.
export const MASTER_SLOW_ANSWER_SECONDS = 180

// Пока запас велик, счётчик молчит. Постоянная цифра у поля перестаёт читаться
// ровно к тому моменту, когда она понадобится.
const COUNT_VISIBLE_FROM = 0.7

// Поле растёт до этого числа строк и дальше прокручивается: композер, съевший
// пол-экрана, прячет тот самый разговор, ради которого его и открыли.
const MAX_ROWS = 12
// Поле начинается с одной строки: оно растёт по набранному, и держать его
// пустым в две строки не за что — эта строка отнята у разговора.
const MIN_ROWS = 1

// Предел ядра считается в байтах, а поле ввода живёт в символах: кириллица
// весит вдвое, и «32 тысячи знаков» прошли бы проверку, не пройдя ядро.
export function masterMessageBytes (value) {
  const text = String(value || '')
  return typeof TextEncoder === 'function' ? new TextEncoder().encode(text).length : text.length
}

// Отказ по длине называет и во что упёрлись, и что с этим делать: у Мастера есть
// чтение проекта, и путь к файлу он разберёт сам — в отличие от файла,
// вставленного в поле целиком.
export function oversizedMasterMessageNote (bytes) {
  return `Сообщение не помещается: ${Math.round(bytes / 1024)} КБ при пределе ${MASTER_MESSAGE_LIMIT_BYTES / 1024} КБ. Сократите его или назовите путь к файлу — Мастер прочитает файл сам.`
}

// Высота поля по набранному. Считаются переносы строки, а не перенос по ширине:
// CSP запрещает инлайновые стили, менять высоту через CSSOM в этом вебвью
// некому и нечем, и остаётся атрибут rows — он знает только явные строки.
export function masterComposeRows (value) {
  const breaks = String(value || '').split('\n').length
  return Math.min(MAX_ROWS, Math.max(MIN_ROWS, breaks))
}

// Поле свободного ответа растёт до шести строк, а не до двенадцати: оно стоит
// над полем реплики, внутри той же карточки, и отнятое им место отнято у
// разговора дважды.
const ANSWER_MAX_ROWS = 6

export function masterAnswerRows (value) {
  const breaks = String(value || '').split('\n').length
  return Math.min(ANSWER_MAX_ROWS, Math.max(MIN_ROWS, breaks))
}

// Сколько осталось до предела. Число появляется, когда запас кончается, и
// краснеет, когда кончился: до этого оно шум.
//
// Состояние отделено от разметки, потому что рисуется оно дважды: целиком при
// отрисовке раздела и точечно на каждом нажатии клавиши. Второй путь обязан
// обойтись без замены разметки — она стоила бы каретки в поле.
export function masterComposeCountState (value) {
  const bytes = masterMessageBytes(value)
  const kb = size => (size / 1024).toFixed(1).replace('.', ',')
  return {
    text: `${kb(bytes)} из ${kb(MASTER_MESSAGE_LIMIT_BYTES)} КБ`,
    hidden: bytes < MASTER_MESSAGE_LIMIT_BYTES * COUNT_VISIBLE_FROM,
    over: bytes > MASTER_MESSAGE_LIMIT_BYTES,
  }
}

export function masterComposeCountClass (state) {
  return `hall-compose-count${state.hidden ? ' is-hidden' : ''}${state.over ? ' is-over' : ''}`
}

export function masterComposeCountHtml (value) {
  const state = masterComposeCountState(value)
  return `<small class="${masterComposeCountClass(state)}">${state.text}</small>`
}

// Пока модель думает, событий не приходит вовсе: строка ожидания замирает, и
// через минуту молчания раздел неотличим от зависшего. Первые секунды не
// считаются вслух, чтобы не мигать на быстрых ответах.
export function masterWaitSuffix (waitedSeconds) {
  const waited = Number(waitedSeconds) || 0
  if (waited < 3) return ''
  const slow = waited >= MASTER_SLOW_ANSWER_SECONDS
    ? ' · дольше обычного; если модель промолчит, ответит движок Point'
    : ''
  return ` · ${waited} с${slow}`
}

// Подпись под полем: в покое она объясняет, как отправить, не отправив случайно
// на переносе строки, а во время хода — что поле заперто не навсегда.
export function masterComposeMetaHtml (loading) {
  // Во время хода подпись говорит ровно то, что происходит: набранное сохранится
  // в поле, а отправит его человек сам. Очереди с самоотправкой здесь нет, и
  // обещать её нельзя — реплика ушла бы без второго взгляда на неё.
  return loading
    ? 'Enter — в очередь: реплика уйдёт после ответа'
    : 'Enter — отправить · Shift+Enter — перенос'
}

// Кнопки композера. «Отправить» остаётся кнопкой отправки формы с теми же
// классами: по ним её находят и syncMasterComposeState, и смоуки разговора.
//
// Подписи заменены знаками: слово «Отправить» повторяло подсказку под полем
// («Enter — отправить») и было самым громким пятном разговора, а «Остановить»
// занимало полряда ради состояния, которое длится секунды. Знак остаётся
// названным — aria-label и title несут то же слово, что стояло на кнопке, —
// поэтому читалка и всплывающая подсказка ничего не теряют.
//
// `data-action` принадлежит кнопке, а не форме. Пока он висел на `<form>`,
// делегирование по `closest('[data-action]')` находило форму от любого места
// внутри неё: клик в поле, чтобы поправить опечатку, отправлял черновик, и клик
// по меню режима — тоже. Своего действия нет ни у поля, ни у подписей, ни у
// `<summary>`, поэтому ближайшим для всех оказывалась форма.
//
// Пустота отмечена `aria-disabled`, а не `disabled`: запертая кнопка выпадает из
// обхода клавиатурой и молчит о причине. Так она остаётся достижимой и на
// нажатие отвечает словами. `disabled` остаётся за одним состоянием — идущим
// ходом, и по нему же смоуки проверяют, что разговор не заперся насмерть.
//
// Во время хода отправка больше не заперта, если есть что отправить: реплика
// встаёт в очередь (master-compose-keys.js) и уходит после ответа. Заперта она
// только с пустым полем — ставить в очередь нечего.
export function masterComposeActionsHtml (loading, draft) {
  const stop = loading
    ? `<button type="button" class="hall-btn is-sm hall-compose-stop" data-action="stop-master-chat" aria-label="Остановить ход" title="Остановить ход">${icon('stop')}</button>`
    : ''
  const empty = !String(draft || '').trim()
  const label = loading ? 'В очередь' : 'Отправить'
  // Подсказка о клавишах в покое спрятана, и её `title` был недостижим; клавиши
  // названы здесь, на кнопке, куда подводят указатель.
  const keys = 'Enter · Shift+Enter — перенос · «/» — команды · «@» — файлы · ↑ — прошлая реплика'
  return `${stop}<button type="submit" class="hall-btn is-primary hall-compose-send${loading ? ' is-queue' : ''}" data-action="master-send" aria-label="${label}" title="${label} · ${keys}"${loading && empty ? ' disabled' : ''}${empty ? ' aria-disabled="true"' : ''}>${icon('send')}</button>`
}

// Класс формы. Пустому полю отправлять нечего, и акцент на стрелке в этот
// момент ничего не значит — она гаснет. Считается это в одном месте: отрисовка
// раздела и правка на каждом нажатии обязаны давать один и тот же класс, иначе
// стрелка загорается только после перерисовки.
//
// Ход сюда больше не входит. Пока он шёл, форма звалась пустой при любом поле —
// потому что поле в это время и правда очищалось. Теперь оно открыто, и замок
// хода носит кнопка, а не класс формы.
export function masterComposeFormClass (draft) {
  return `hall-compose${String(draft || '').trim() ? '' : ' is-empty'}`
}
