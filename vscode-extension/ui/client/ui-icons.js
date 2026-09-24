// Значки интерфейса: один набор на весь разговор с Мастером.
//
// Раньше действия подписывались словами («Копировать», «Сведения», «Полезно»)
// или случайными символами шрифта (☰ ••• ↑ ■): слова под каждой репликой
// превращали ленту в меню, а символы шрифта рисуются по-разному в каждой
// гарнитуре и не совпадают ни размером, ни толщиной линии.
//
// Значок — встроенный SVG, а не файл и не шрифт: CSP вебвью не пропускает
// чужие шрифты, а файл стоил бы запроса на каждую реплику. Линия рисуется
// цветом текста (currentColor), поэтому значок сам следует за темой и за
// состоянием кнопки. Сетка 16×16, линия 1.25 — как у значков редактора.
//
// Значок всегда декоративен (aria-hidden): смысл кнопке даёт её aria-label и
// title, и вызывающий обязан их поставить.

const PATHS = {
  copy: '<rect x="5.5" y="5.5" width="8" height="8" rx="1.5"/><path d="M10.5 5.5V3.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2"/>',
  edit: '<path d="M10.8 2.7l2.5 2.5-7.8 7.8H3v-2.5z"/><path d="M9.3 4.2l2.5 2.5"/>',
  retry: '<path d="M13 8a5 5 0 1 1-1.6-3.7"/><path d="M13 2.5v3h-3"/>',
  info: '<circle cx="8" cy="8" r="5.75"/><path d="M8 7.3v3.7M8 5v.2"/>',
  'thumbs-up': '<path d="M5 7.2l2.6-4.4c.9 0 1.6.7 1.6 1.6v2h3.2a1 1 0 0 1 1 1.2l-.8 4.3a1 1 0 0 1-1 .8H5z"/><path d="M5 7.2H2.5v5.9H5"/>',
  'thumbs-down': '<path d="M11 8.8l-2.6 4.4c-.9 0-1.6-.7-1.6-1.6v-2H3.6a1 1 0 0 1-1-1.2l.8-4.3a1 1 0 0 1 1-.8H11z"/><path d="M11 8.8h2.5V2.9H11"/>',
  send: '<path d="M8 13V3.5M4 7.5l4-4 4 4"/>',
  stop: '<rect x="4.5" y="4.5" width="7" height="7" rx="1" fill="currentColor" stroke="none"/>',
  attach: '<path d="M10.5 5.2L6.2 9.5a1.2 1.2 0 0 0 1.7 1.7l4.6-4.6a2.5 2.5 0 0 0-3.5-3.5L4.3 7.8a3.8 3.8 0 0 0 5.4 5.4l3.8-3.8"/>',
  file: '<path d="M4 2.5h5l3 3v8H4z"/><path d="M9 2.5v3h3"/>',
  folder: '<path d="M2.5 4.5h3.8l1.4 1.5h5.8v6.5h-11z"/>',
  search: '<circle cx="7" cy="7" r="4.25"/><path d="M10.2 10.2l3.3 3.3"/>',
  terminal: '<rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M5 6.5l2 1.5-2 1.5M8.5 10H11"/>',
  'file-edit': '<path d="M9 2.5H4v11h3.5"/><path d="M9 2.5l3 3v1"/><path d="M12.3 8.2l1.5 1.5-3.8 3.8H8.5V12z"/>',
  git: '<circle cx="5" cy="4" r="1.5"/><circle cx="5" cy="12" r="1.5"/><circle cx="11" cy="6" r="1.5"/><path d="M5 5.5v5M11 7.5c0 2-2.5 2.5-5.5 3.5"/>',
  map: '<path d="M2.5 4l3.7-1.5 3.6 1.5 3.7-1.5v9.5l-3.7 1.5-3.6-1.5-3.7 1.5z"/><path d="M6.2 2.5v9.5M9.8 4v9.5"/>',
  quest: '<path d="M4 2.5h8v11l-4-2.5-4 2.5z"/>',
  team: '<circle cx="6" cy="5.5" r="2.25"/><path d="M2 13a4 4 0 0 1 8 0"/><path d="M10.5 3.5a2.25 2.25 0 0 1 0 4.2M11.5 9.3A4 4 0 0 1 14 13"/>',
  person: '<circle cx="8" cy="5.5" r="2.5"/><path d="M3 13.5a5 5 0 0 1 10 0"/>',
  think: '<path d="M6 12.5h4M6.6 14h2.8"/><path d="M8 2.5a4 4 0 0 0-2.4 7.2V11h4.8V9.7A4 4 0 0 0 8 2.5z"/>',
  tool: '<path d="M10.2 2.6a3 3 0 0 0-3 4L2.6 11.2l2.2 2.2 4.6-4.6a3 3 0 0 0 4-3l-1.9 1.9-1.8-.4-.4-1.8z"/>',
  check: '<path d="M3.5 8.5l3 3 6-7"/>',
  x: '<path d="M4.5 4.5l7 7M11.5 4.5l-7 7"/>',
  warning: '<path d="M8 2.5l6 10.5H2z"/><path d="M8 7v2.6M8 11.3v.2"/>',
  'chevron-right': '<path d="M6 3.5L10.5 8 6 12.5"/>',
  'chevron-down': '<path d="M3.5 6L8 10.5 12.5 6"/>',
  more: '<circle cx="3.5" cy="8" r="1" fill="currentColor" stroke="none"/><circle cx="8" cy="8" r="1" fill="currentColor" stroke="none"/><circle cx="12.5" cy="8" r="1" fill="currentColor" stroke="none"/>',
  'panel-right': '<rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M10 3v10"/>',
  'panel-left': '<rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M6 3v10"/>',
  list: '<path d="M6 4.5h7.5M6 8h7.5M6 11.5h7.5"/><path d="M2.8 4.5h.4M2.8 8h.4M2.8 11.5h.4"/>',
  clock: '<circle cx="8" cy="8" r="5.75"/><path d="M8 5v3.2l2.2 1.4"/>',
  settings: '<circle cx="8" cy="8" r="2"/><path d="M8 1.8v1.7M8 12.5v1.7M1.8 8h1.7M12.5 8h1.7M3.6 3.6l1.2 1.2M11.2 11.2l1.2 1.2M3.6 12.4l1.2-1.2M11.2 4.8l1.2-1.2"/>',
  plus: '<path d="M8 3v10M3 8h10"/>',
  memory: '<path d="M3 3.5h4a1.5 1.5 0 0 1 1 .4 1.5 1.5 0 0 1 1-.4h4v9H9a1 1 0 0 0-1 1 1 1 0 0 0-1-1H3z"/><path d="M8 4v9.5"/>',
}


// Разметка значка. Неизвестное имя — пустая строка, а не чужой значок: пустое
// место заметно на стенде, подменённый смысл — нет.
export function icon (name, { className = '' } = {}) {
  const paths = PATHS[name]
  if (!paths) return ''
  const cls = className ? `hall-icon ${className}` : 'hall-icon'
  return `<svg class="${cls}" viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${paths}</svg>`
}
