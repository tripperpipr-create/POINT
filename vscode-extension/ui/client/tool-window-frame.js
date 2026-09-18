// Одна рамка окна инструментов на все вебвью Point.
//
// Git, Запуск, Службы и Логи стоят в нижней панели одним рядом, и разнобой
// шапок там виден сразу. Их было две: `point-tool-head` у Git, Запуска и Логов
// и `hub-page-head` у Баз и Серверов — второй писался для страниц Хаба, где у
// заголовка другая ширина и другая роль. Здесь они сведены в одну, а выбор
// делается по поверхности, а не по тому, кто первым написал разметку.
export function createToolWindowFrame({ esc, isToolWindow }) {

  function toolCommandButton(command, label, tone = 'secondary', title = '') {
    return `<button type="button" class="${tone}" data-action="tool-command" data-command="${esc(command)}"${title ? ` title="${esc(title)}"` : ''}>${esc(label)}</button>`
  }

  // Шапка окна: надзаголовок, имя, поясняющая строка, счётчик и обновление.
  function toolWindowHeading(kicker, title, description, badge = '') {
    return `<header class="point-tool-head"><div><span>${esc(kicker)}</span><h1>${esc(title)}</h1><p>${esc(description)}</p></div>${badge ? `<em>${esc(badge)}</em>` : ''}<button type="button" class="icon-button point-tool-refresh" data-action="refresh-tool-window" title="Обновить">↻</button></header>`
  }

  // Базы и Серверы открываются и окном инструментов, и вкладкой Хаба. В окне у
  // них шапка окна, в Хабе — заголовок страницы: одна разметка на оба случая
  // врала бы про ширину и про то, где пользователь находится.
  function toolPageHeading(kicker, title, description, badge = '') {
    if (isToolWindow()) return toolWindowHeading(kicker, title, description, badge)
    return `<header class="hub-page-head"><div><span>${esc(kicker)}</span><h1>${esc(title)}</h1><p>${esc(description)}</p></div>${badge === '' ? '' : `<em>${esc(badge)}</em>`}</header>`
  }

  // Три состояния, в которых окно не показывает содержимого. Каждое окно
  // писало их своей разметкой, и «пусто» у Логов не походило на «пусто» у
  // Служб, хотя означало ровно то же.
  function toolWindowEmpty(title, hint = '', actions = '', compact = false) {
    return `<div class="point-tool-empty${compact ? ' compact' : ''}"><strong>${esc(title)}</strong>${hint ? `<p>${esc(hint)}</p>` : ''}${actions}</div>`
  }

  function toolWindowError(text, actions = '') {
    return `<div class="error-banner"><span>!</span><p>${esc(text)}</p>${actions}</div>`
  }

  // Данные всех четырёх окон собирает ядро. Когда его нет, окно обязано
  // сказать это словом и дать кнопку, а не показывать пустой список, из
  // которого следует, что подключений нет.
  function toolWindowOffline(hint = 'Ядро Point не отвечает — данные этого окна собирает оно.') {
    return toolWindowEmpty('Нет связи с ядром', hint, toolCommandButton('localAgent.restartServer', 'Перезапустить ядро', 'primary'))
  }

  return { isToolWindow, toolCommandButton, toolWindowHeading, toolPageHeading, toolWindowEmpty, toolWindowError, toolWindowOffline }
}
