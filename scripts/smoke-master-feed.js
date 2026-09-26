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

function open({ history = HISTORY, truncated = false, workOrders = [], details, executions = [], sessions } = {}) {
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
      runs: [], quests: [], executions, changeSets: [], questProposals: [],
      orchestrator: { id: 'o1', preset: 'conductor', model: 'qwen' },
    },
    details,
  } })
  listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, history, truncated, workOrders, sessions,
  } } })

  const click = dataset => listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) },
    preventDefault() {},
  })
  return { listeners, posted, root, click, find }
}

{
  const ui = open({ details: {
    run: { id: 'run-files', status: 'completed' },
    patches: [
      { id: 'patch-applied', path: 'src/api.php', status: 'applied' },
      { id: 'patch-applied-2', path: 'src/api.php', status: 'applied' },
      { id: 'patch-pending', path: 'src/api.php', status: 'pending' },
      { id: 'patch-other-pending', path: 'src/new.php', status: 'pending' },
    ],
  } })
  const html = ui.root.innerHTML
  const feed = html.indexOf('session-keep-undo')
  const composer = html.indexOf('<form class="hall-compose')
  if (!(feed >= 0 && feed < composer) || html.includes('Оставить всё')) {
    throw new Error('file changes or a fake Keep action are still in the composer')
  }
  if (!html.includes('data-patch-ids="patch-applied,patch-applied-2"') || html.includes('data-patch-ids="patch-applied,patch-applied-2,patch-pending"')) {
    throw new Error('file undo includes a patch that was not applied')
  }
  if (!html.includes('1 файл применён') || !html.includes('2 файла ждут решения')) {
    throw new Error('patch count is shown as file count')
  }
  if (html.includes('data-action="open-file" data-path="src/new.php"')) {
    throw new Error('pending sandbox file has a project open button')
  }
  ui.click({ action: 'keep-run-all', runId: 'run-files' })
  if (ui.root.innerHTML.includes('session-keep-undo')) throw new Error('Hide list did not hide the feed item')
}

{
  const ui = open({
    executions: [{ id: 'execution-flow', runId: 'run-flow', flowRunId: 'flow-1' }],
    details: { run: { id: 'run-flow', status: 'running' }, patches: [{ id: 'patch-flow', path: 'composer.json', status: 'applied' }] },
  })
  if (ui.root.innerHTML.includes('session-run-files') || ui.root.innerHTML.includes('session-keep-undo')) {
    throw new Error('sandbox files appear as changed project files in the feed or composer')
  }
}

// После запуска квест идёт при той реплике, которая его предложила. Нижняя
// форма остаётся местом для сообщения, а не вторым экраном выполнения.
{
  const history = HISTORY.map(item => item.id === 'ma-1' ? { ...item, proposalId: 'qp-feed' } : item)
  const workOrders = [{
    id: 'workorder-qp-feed', proposalId: 'qp-feed', state: 'approved', goal: 'Миграция базы',
    runtime: { questId: 'quest-feed', status: 'running', stages: [{ id: 'implement', name: 'Реализация', status: 'running', runId: 'run-feed' }] },
  }]
  const html = open({ history, workOrders }).root.innerHTML
  const proposal = html.indexOf('Отряд собран, миграция расписана.')
  const run = html.indexOf('data-work-order-id="workorder-qp-feed"')
  const nextTurn = html.indexOf('Что с обработчиком оплаты?')
  const composer = html.indexOf('<form class="hall-compose')
  if (!(proposal >= 0 && proposal < run && run < nextTurn && nextTurn < composer)) {
    throw new Error('running quest is outside its conversation turn or inside the composer')
  }
  if ((html.match(/data-work-order-id="workorder-qp-feed"/g) || []).length !== 1) {
    throw new Error('running quest is duplicated between its turn and the bottom of the feed')
  }
}

// Предложение запомнить стоит под ответом, который его сделал. В меню «•••» его
// не находили: 29 записей остались неподтверждёнными, а в промпт идут только
// подтверждённые — поправка человека не доживала до следующего задания.
{
  const history = HISTORY.map(item => item.id === 'ma-2' ? { ...item, turnId: 'turn-remember' } : item)
  const sessions = { active: 'c1', items: [{ id: 'c1', title: 'Разговор' }], memoryEntries: [
    { id: 'memory-new', content: 'Go + PostgreSQL — pgx/v5', status: 'proposed', sourceId: 'turn-remember' },
    { id: 'memory-old', content: 'Порт 8080', status: 'accepted', sourceId: 'turn-older' },
  ] }
  const html = open({ history, sessions }).root.innerHTML
  const answer = html.indexOf('Обработчик оплаты падает на повторной доставке.')
  const card = html.indexOf('class="hall-memory-inline"')
  const composer = html.indexOf('<form class="hall-compose')
  if (!(answer >= 0 && answer < card && card < composer)) {
    throw new Error('proposed memory is not shown under the reply that proposed it')
  }
  const inline = html.slice(card, html.indexOf('</div></article></div>', card))
  if (!inline.includes('data-action="master-session-memory-save" data-id="memory-new"') || !inline.includes('Предлагаю запомнить') || inline.includes('data-memory-id="memory-old"')) {
    throw new Error('inline memory card lacks the confirm action or shows accepted entries: ' + inline.slice(0, 300))
  }
  if ((html.match(/class="hall-memory-inline"/g) || []).length !== 1) {
    throw new Error('memory card is attached to replies that did not propose it')
  }
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
