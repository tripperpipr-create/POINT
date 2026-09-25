// Перебор списков и вкладок Хаба с клавиатуры.
//
// До этой проверки клавиатурный маршрут по Хабу не был гарантирован ничем: на
// весь интерфейс приходился один обработчик keydown и ни одного tabindex, а
// очереди объявляли себя списками — роль listbox на контейнере и option на
// обычных кнопках. Объявление врало скринридеру, а стрелки не делали ничего —
// и заметить это было нечем: contrast.mjs проверяет только контраст.
//
// Здесь проверяется поведение, а не разметка: куда уезжает выбор и фокус,
// когда человек жмёт стрелку. Статические инварианты (снятый listbox, наличие
// data-keynav, tabindex у вкладок) держит ui/contracts.mjs.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function open(tab = 'decisions') {
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: () => null,
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w', selectedTab: tab,
    boot: {
      onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
      runs: [{ id: 'run-1', status: 'waiting_approval' }],
      quests: [], executions: [], changeSets: [],
    },
  } })
  listeners['window:message']({ data: { type: 'decisions', decisions: {
    total: 3,
    items: [
      { id: 'd1', kind: 'approval', title: 'Первое', risk: 'HIGH', resolve: { path: '/api/approvals/d1', field: 'decision', accept: 'approve', reject: 'deny' } },
      { id: 'd2', kind: 'change-set', title: 'Второе', risk: 'MEDIUM', resolve: { path: '/api/change-sets/d2/apply', accept: 'apply', reject: '/api/change-sets/d2/reject' } },
      { id: 'd3', kind: 'approval', title: 'Третье', risk: 'LOW', resolve: { path: '/api/approvals/d3', field: 'decision', accept: 'approve', reject: 'deny' } },
    ],
  } } })

  const press = (key, target, extra = {}) => listeners['root:keydown']({
    key, code: key, target: target || { tagName: 'DIV' },
    repeat: false, preventDefault() { extra.prevented = true }, ctrlKey: false, metaKey: false, altKey: false,
    ...extra,
  })
  const selected = () => {
    const hit = root.innerHTML.match(/class="hall-queue-item is-active"[^>]*data-id="([^"]+)"/)
    return hit ? hit[1] : ''
  }
  return { listeners, posted, root, press, selected }
}

// Полоса или список из настоящих узлов: обработчик читает dataset, ищет кнопки
// своего контейнера, переносит фокус и переставляет остановку табуляции —
// каждое из этих действий проверяется по отдельности.
function makeList(axis, count) {
  const container = { dataset: { keynav: axis } }
  const focused = []
  const items = []
  for (let index = 0; index < count; index += 1) {
    const attributes = { tabindex: index === 0 ? '0' : '-1' }
    items.push({
      index,
      attributes,
      disabled: false,
      tagName: 'BUTTON',
      focus() { focused.push(this.index) },
      setAttribute(name, value) { attributes[name] = value },
      closest(selector) {
        if (selector === '[data-keynav]') return container
        if (selector === 'button') return this
        return null
      },
    })
  }
  container.querySelectorAll = () => items
  return { container, items, focused, stops: () => items.map(item => item.attributes.tabindex) }
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 200)}`)
}

// Защита от холостого хода.
{
  const base = open()
  if (!base.root.innerHTML.includes('Первое')) {
    console.error('очередь решений не отрисовалась — проверки прошли бы вхолостую')
    process.exit(1)
  }
  if (!base.root.innerHTML.includes('data-keynav="column"')) {
    console.error('очередь решений не помечена data-keynav — перебирать нечего')
    process.exit(1)
  }
}

// ── Очередь решений: стрелки делают то же, что J и K ──────────────────────
{
  const ui = open()
  const first = ui.selected()
  ui.press('ArrowDown')
  const second = ui.selected()
  check('стрелка вниз ведёт к следующему решению',
    second && second !== first,
    `выбор остался на ${first} — стрелка не подключена`)
  ui.press('ArrowUp')
  check('стрелка вверх возвращает к прежнему',
    ui.selected() === first,
    `выбор ушёл на ${ui.selected()} вместо ${first}`)

  // Стрелка внутри поля двигает каретку, а не выбор: то же правило, что у J/K.
  const before = ui.selected()
  ui.press('ArrowDown', { tagName: 'TEXTAREA', id: 'note', closest: () => null })
  check('в поле ввода стрелка не трогает выбор',
    ui.selected() === before,
    'стрелка в поле переставила решение — набирать текст стало опасно')
}

// ── Полоса вкладок: стрелки по горизонтали и одна остановка табуляции ─────
{
  const ui = open('git')
  const strip = makeList('row', 4)
  ui.press('ArrowRight', strip.items[0])
  check('стрелка вправо ведёт к следующей вкладке',
    strip.focused[strip.focused.length - 1] === 1,
    `фокус ушёл на ${JSON.stringify(strip.focused)} вместо второй вкладки`)
  check('остановка табуляции переехала вместе с фокусом',
    strip.stops().join(',') === '-1,0,-1,-1',
    `tabindex стал ${strip.stops().join(',')} — Tab снова обходит все вкладки`)

  ui.press('ArrowLeft', strip.items[0])
  check('полоса замкнута в кольцо',
    strip.focused[strip.focused.length - 1] === 3,
    `влево от первой вкладки фокус ушёл на ${strip.focused[strip.focused.length - 1]}, а не на последнюю`)

  ui.press('End', strip.items[0])
  check('End прыгает к последней',
    strip.focused[strip.focused.length - 1] === 3,
    'End никуда не привёл')
  ui.press('Home', strip.items[2])
  check('Home прыгает к первой',
    strip.focused[strip.focused.length - 1] === 0,
    'Home никуда не привёл')

  // Вертикальные стрелки на горизонтальной полосе не наши: их может ждать
  // прокрутка страницы.
  const seen = strip.focused.length
  ui.press('ArrowDown', strip.items[0])
  check('вертикальная стрелка не трогает полосу',
    strip.focused.length === seen,
    'полоса отреагировала на чужую ось')
}

// ── Вертикальный список ───────────────────────────────────────────────────
{
  const ui = open('journal')
  const list = makeList('column', 3)
  ui.press('ArrowDown', list.items[0])
  check('стрелка вниз ведёт по вертикальному списку',
    list.focused[list.focused.length - 1] === 1,
    `фокус ушёл на ${JSON.stringify(list.focused)}`)
  // Остановка табуляции переставляется только у полосы: в обычном списке все
  // кнопки и должны быть доступны табуляцией.
  check('в списке остановки табуляции не переставляются',
    list.stops().join(',') === '0,-1,-1',
    `tabindex стал ${list.stops().join(',')} — список потерял естественный обход`)

  const seen = list.focused.length
  ui.press('ArrowRight', list.items[0])
  check('горизонтальная стрелка не трогает список',
    list.focused.length === seen,
    'список отреагировал на чужую ось')

  ui.press('ArrowDown', list.items[0], { ctrlKey: true })
  check('чужая комбинация не перехвачена',
    list.focused.length === seen,
    'Ctrl+стрелка ушла в перебор — перехвачена комбинация редактора')
}

// ── Второстепенная кнопка строки не встаёт на пути стрелки ─────────────────
// В списке чатов у каждой строки два пункта: название и «×». Стрелка вниз
// вставала на «×» через шаг; помеченная data-keynav-skip кнопка из перебора
// выпадает и остаётся в обходе табуляцией.
{
  const ui = open('journal')
  const list = makeList('column', 4)
  list.items[1].dataset = { keynavSkip: '' }
  list.items[3].dataset = { keynavSkip: '' }
  ui.press('ArrowDown', list.items[0])
  check('стрелка перешагивает второстепенную кнопку строки',
    list.focused[list.focused.length - 1] === 2,
    `фокус ушёл на ${JSON.stringify(list.focused)} вместо следующей строки`)
}

if (failures.length) {
  console.error('\nКЛАВИАТУРНЫЙ МАРШРУТ ПО ХАБУ ПРОВАЛЕН:')
  for (const message of failures) console.error('  · ' + message)
  process.exit(1)
}
console.log('\nсписки и вкладки Хаба перебираются с клавиатуры')
