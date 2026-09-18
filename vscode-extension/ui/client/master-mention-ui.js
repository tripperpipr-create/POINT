// «@» под кареткой: список файлов прямо в поле, а не окно поверх редактора.
//
// «@» работало и раньше, но срабатывало только в самом конце строки и открывало
// нативный выбор источника, а в нём — второй диалог файлов. От «хочу приложить
// файл» до вложения было четыре действия и два окна поверх всего редактора.
// У эталона это два нажатия: «@», пара букв, Enter.
//
// Нативное окно никуда не делось и остаётся кнопкой «@ Контекст»: в списке под
// кареткой нечего показать для ошибок сборки, git diff и буфера терминала — у
// них нет пути в дереве проекта.
//
// Состояние живёт здесь, а не в main.js: тот упирается в границу модуля
// (scripts/check-release-contracts.mjs), и расти ему нечем.

// Запрос считается от «@» до каретки и обрывается на пробеле: «@pay ments» — это
// «@pay» и слово рядом, а не запрос из двух слов. Внутри запроса разрешены
// точки, дефисы и косые — из них состоят пути.
const QUERY = /(^|[\s(])@([\w./-]*)$/u

let state = { open: false, query: '', items: [], active: 0, at: -1 }

export function masterMentionState() { return state }
export function masterMentionOpen() { return state.open }
// Идентификатор выбранной строки. Читалка узнаёт о перемещении по списку только
// через aria-activedescendant поля: фокус при наборе остаётся в поле, и сама
// строка его не получает.
export function masterMentionActiveId() { return state.open && state.items.length ? `hall-mention-${state.active}` : '' }

export function closeMasterMention() {
  state = { open: false, query: '', items: [], active: 0, at: -1 }
}

// Что набрано перед кареткой. Возвращает null, если «@» там нет: тогда список
// закрывается — человек ушёл от упоминания, а не отменил его.
function masterMentionQuery(value, caret) {
  const at = Number.isFinite(caret) ? caret : String(value || '').length
  const before = String(value || '').slice(0, at)
  const match = before.match(QUERY)
  if (!match) return null
  return { query: match[2], at: before.length - match[2].length - 1 }
}

// Ввод в поле: открыть, обновить или закрыть список. Возвращает запрос, который
// надо спросить у расширения, или null — если спрашивать нечего.
export function masterMentionInput(value, caret) {
  const found = masterMentionQuery(value, caret)
  if (!found) { closeMasterMention(); return null }
  // Список остаётся открытым, пока запрос уточняют: подменяем только запрос,
  // чтобы выбранная строка не прыгала на каждую букву.
  state = { open: true, query: found.query, items: state.query === found.query ? state.items : [], active: 0, at: found.at }
  return found.query
}

export function acceptMasterMentionItems(query, items) {
  if (!state.open || state.query !== String(query || '')) return false
  state = { ...state, items: Array.isArray(items) ? items : [], active: 0 }
  return true
}

// Клавиши списка. Перехватывать их надо раньше отправки по Enter — иначе
// выбранная строка уедет в ядро вместо того, чтобы приложить файл.
export function handleMasterMentionKey(key, { pick, render }) {
  if (!state.open) return false
  if (key === 'Escape') { closeMasterMention(); render(); return true }
  if (!state.items.length) return false
  if (key === 'ArrowDown' || key === 'ArrowUp') {
    const step = key === 'ArrowDown' ? 1 : -1
    state = { ...state, active: (state.active + step + state.items.length) % state.items.length }
    render()
    return true
  }
  if (key === 'Enter' || key === 'Tab') {
    // Запрос и место «собачки» снимаются ДО закрытия: закрытие обнуляет
    // состояние, и вырезать из черновика стало бы нечего.
    const item = state.items[state.active]
    const { at, query } = state
    closeMasterMention()
    if (item) pick(item, at, query)
    return true
  }
  return false
}

// Убрать «@запрос» из черновика: файл уходит во вложения, а знак в реплике
// остался бы мусором и уехал бы в ядро вместе с ней.
export function masterMentionStripped(value, at, query) {
  const text = String(value || '')
  if (at < 0) return text
  return text.slice(0, at) + text.slice(at + 1 + String(query || '').length)
}

export function masterMentionHtml(esc) {
  if (!state.open) return ''
  const rows = state.items.map((item, index) => {
    const path = String(item.path || '')
    const cut = path.lastIndexOf('/')
    const dir = cut < 0 ? '' : path.slice(0, cut + 1)
    const base = cut < 0 ? path : path.slice(cut + 1)
    return `<button type="button" class="hall-mention-row${index === state.active ? ' is-active' : ''}" role="option" aria-selected="${index === state.active}" data-action="master-mention-pick" id="hall-mention-${index}" data-index="${index}" data-path="${esc(path)}">${dir ? `<em>${esc(dir)}</em>` : ''}${esc(base)}${item.open ? '<small>открыт</small>' : ''}</button>`
  }).join('')
  // Пустой ответ — тоже ответ: молчащий список читается как «ищет», и человек
  // ждёт того, чего не будет.
  const body = rows || `<p class="hall-mention-empty">${state.query ? 'Ничего не нашлось' : 'Наберите часть имени файла'}</p>`
  return `<div class="hall-mention" role="listbox" aria-label="Файлы проекта">${body}</div>`
}
