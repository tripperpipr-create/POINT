// Лента разговора с Мастером: где я в ней нахожусь и как найти прежнее.
//
// Разговор живёт неделями, а лента была плоской: без дат вчерашнее решение
// неотличимо от сегодняшнего, без поиска прежнее решение ищут прокруткой, а
// строка «разговор начался раньше» была тупиком — сообщала и не пускала.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

// Даты считаются от полуночи: снимок обязан быть одинаковым в любой день.
const at = (shiftDays, hours, minutes) => {
  const date = new Date()
  date.setHours(0, 0, 0, 0)
  date.setDate(date.getDate() + shiftDays)
  date.setHours(hours, minutes)
  return date.toISOString()
}

const HISTORY = [
  { id: 'mu-1', role: 'user', content: 'Собери отряд под миграцию базы', createdAt: at(-1, 9, 41) },
  { id: 'ma-1', role: 'assistant', mode: 'model', content: 'Отряд собран, миграция расписана.', createdAt: at(-1, 9, 42) },
  { id: 'mu-2', role: 'user', content: 'Что с обработчиком оплаты?', createdAt: at(0, 14, 2) },
  { id: 'ma-2', role: 'assistant', mode: 'model', content: 'Обработчик оплаты падает на повторной доставке.', createdAt: at(0, 14, 3) },
]

function open({ history = HISTORY, truncated = false } = {}) {
  const listeners = {}
  const posted = []
  const field = { id: 'master-input', value: '', rows: 2, focus() {}, setSelectionRange() {}, closest: () => null, matches: () => false }
  // У настоящего поля есть focus(): раскрыв поиск, туда ставят каретку, и
  // подставное поле без него роняло бы проверку на своей же неверности.
  const find = { id: 'master-find', value: '', focus() {}, closest: () => null, matches: () => false }
  // Узла ленты в стенде нет намеренно: пока его нет, раздел рисуется целиком, и
  // вся разметка видна в root.innerHTML. Точечную замену ленты стережёт
  // отдельная проверка — smoke-master-thread-incremental.js.
  const nodes = { '#master-input': field, '#master-find': find }
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector: selector => nodes[selector] || null,
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set, TextEncoder,
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {}, setTimeout(callback) { callback(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
    selectedTab: 'master',
    boot: {
      onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
      runs: [], quests: [], executions: [], changeSets: [], questProposals: [],
      orchestrator: { id: 'o1', preset: 'conductor', model: 'qwen' },
    },
  } })
  listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, history, truncated,
  } } })

  const click = dataset => listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) },
    preventDefault() {},
  })
  return { listeners, posted, root, click, find }
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 260)}`)
}

// ── Дни и время ────────────────────────────────────────────────────────────
{
  const ui = open()
  const html = ui.root.innerHTML
  check('вчерашний день отделён от сегодняшнего',
    html.includes('Вчера') && html.includes('Сегодня'),
    'лента плоская: вчерашнее решение неотличимо от сегодняшнего')
  check('разделитель ставится один раз на день',
    (html.match(/hall-day/g) || []).length === 2,
    'разделителей столько же, сколько реплик — это уже не разделитель')
  check('у реплики есть время',
    html.includes('<time>09:41</time>') && html.includes('<time>14:02</time>'),
    'по ленте не понять, когда это было сказано')
}

// ── Начало разговора перестало быть тупиком ────────────────────────────────
{
  const ui = open({ truncated: true })
  check('усечённый разговор предлагает выход',
    ui.root.innerHTML.includes('Показать раньше'),
    'сказано, что разговор начался раньше, и не сказано, как туда попасть')
  ui.click({ action: 'master-load-earlier' })
  const asked = ui.posted.filter(message => message.type === 'loadMaster')
  check('за началом разговора сходили в ядро',
    asked.some(message => message.full === true),
    'кнопка есть, а запроса за прежними репликами нет: ' + JSON.stringify(asked))
  check('пока грузим, кнопка не зовёт второй раз',
    ui.root.innerHTML.includes('Загружаем…'),
    'по кнопке можно нажать повторно, пока идёт первый запрос')

  const ui2 = open({ truncated: false })
  check('полному разговору кнопка не предлагается',
    !ui2.root.innerHTML.includes('Показать раньше'),
    'предлагаем показать то, что уже показано')
}

// ── Поиск по разговору ─────────────────────────────────────────────────────
{
  const ui = open()
  check('поиск виден, но свёрнут',
    ui.root.innerHTML.includes('Найти в разговоре') && ui.root.innerHTML.includes('master-find-open'),
    'спрятанный поиск не находят, а развёрнутый занимает полосу над каждым разговором')
  check('свёрнутый поиск не держит поля ввода',
    !ui.root.innerHTML.includes('id="master-find"'),
    'полоса поиска стоит над разговором и почти всегда пустует')
  check('пустой поиск не шумит',
    !ui.root.innerHTML.includes('master-find-step'),
    'счётчик и стрелки видны, когда искать ещё нечего')

  ui.click({ action: 'master-find-open' })
  check('по нажатию поиск раскрывается',
    ui.root.innerHTML.includes('id="master-find"'),
    'кнопка есть, а поля не появилось')

  ui.find.value = 'оплаты'
  ui.listeners['root:input']({ target: ui.find })
  check('набранное пережило отрисовку',
    ui.root.innerHTML.includes('value="оплаты"'),
    'запрос пропал из поля — искать заново')
  check('появились перебор и сброс',
    ui.root.innerHTML.includes('master-find-step') && ui.root.innerHTML.includes('master-find-clear'),
    'найденное некуда перебирать')

  ui.click({ action: 'master-find-clear' })
  check('сброс очищает поиск',
    !ui.root.innerHTML.includes('master-find-step') && !ui.root.innerHTML.includes('value="оплаты"'),
    'сброс не сбросил')
}

// ── Якорь «к свежему» ──────────────────────────────────────────────────────
{
  const ui = open()
  check('якорь есть в ленте', ui.root.innerHTML.includes('master-scroll-latest'),
    'ушёл читать выше — и вернуться к свежему нечем')
  check('пока лента внизу, якорь скрыт',
    /hall-thread-cue is-hidden/.test(ui.root.innerHTML),
    'кнопка предлагает вернуться туда, где человек и так стоит')
}

if (failures.length) {
  console.log('ЛЕНТА РАЗГОВОРА СЛОМАНА:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('лента Мастера: дни, время, поиск, якорь и начало разговора: PASS')
