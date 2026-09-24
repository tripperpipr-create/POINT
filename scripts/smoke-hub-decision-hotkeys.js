// Клавиши над очередью решений.
//
// Кнопки подписаны «· A» и «· R», перебор подписан в той же полосе действий —
// одно обещание на одну клавишу. Ни одна из этих клавиш не была подключена:
// обработчик знал только Escape и Enter.  (Раньше те же A и R обещала вторая
// полоса под очередью; её убрали, поведение осталось.)
//
// Проверяем и обратное, что важнее самой возможности: набор текста горячие
// клавиши перебивать не должны. Случайное «A» в поле ввода означало бы согласие,
// а разрешённое действие выполняется сразу и не отменяется.

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

  const boot = {
    onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
    runs: [{ id: 'run-1', status: 'waiting_approval' }],
    quests: [], executions: [], changeSets: [],
  }
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true,
    workspace: 'w', selectedTab: tab, boot,
  } })
  listeners['window:message']({ data: { type: 'decisions', decisions: {
    total: 3,
    items: [
      { id: 'd1', kind: 'approval', title: 'Первое', risk: 'HIGH', resolve: { path: '/api/approvals/d1', field: 'decision', accept: 'approve', reject: 'deny' } },
      { id: 'd2', kind: 'change-set', title: 'Второе', risk: 'MEDIUM', resolve: { path: '/api/change-sets/d2/apply', accept: 'apply', reject: '/api/change-sets/d2/reject' } },
      { id: 'd3', kind: 'approval', title: 'Третье', risk: 'LOW', resolve: { path: '/api/approvals/d3', field: 'decision', accept: 'approve', reject: 'deny' } },
    ],
  } } })
  const press = (code, target, repeat = false) => listeners['root:keydown']({
    code, key: code.replace('Key', '').toLowerCase(), target: target || { tagName: 'DIV' },
    repeat, preventDefault() {}, ctrlKey: false, metaKey: false, altKey: false,
  })
  // Выбранный элемент очереди помечен классом is-active и aria-current="true".
  const selected = () => {
    const hit = root.innerHTML.match(/class="hall-queue-item is-active"[^>]*data-id="([^"]+)"/)
    return hit ? hit[1] : ''
  }
  return { listeners, posted, root, press, selected }
}

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

// Защита от холостого хода: без отрисованной очереди проверять нечего.
const base = open()
if (!base.root.innerHTML.includes('Первое')) {
  console.log('очередь решений не отрисовалась — проверки прошли бы вхолостую')
  process.exit(1)
}
if (!/J<\/kbd>|<kbd>J<\/kbd>/.test(base.root.innerHTML)) {
  console.log('подсказка клавиш пропала с экрана — проверять нечего')
  process.exit(1)
}

// J идёт вперёд, K назад, список замкнут в кольцо.
{
  const ui = open()
  const first = ui.selected() || 'd1'
  ui.press('KeyJ')
  const afterJ = ui.selected()
  check('J переходит к следующему', afterJ && afterJ !== first, `осталось ${afterJ || 'пусто'}`)
  ui.press('KeyK')
  check('K возвращает назад', ui.selected() === first, `ожидалось ${first}, стало ${ui.selected()}`)
  ui.press('KeyK')
  check('K с первого уходит в конец списка', ui.selected() === 'd3', `стало ${ui.selected()}`)
}

// A отправляет согласие ровно тем же способом, что и кнопка.
{
  const ui = open()
  ui.press('KeyA')
  const sent = ui.posted.filter(m => m.type === 'resolveDecision')
  check('A отправляет согласие', sent.length === 1, `отправлено ${sent.length}`)
  if (sent.length) {
    check('A шлёт путь активного решения', sent[0].path === '/api/approvals/d1', `путь ${sent[0].path}`)
    check('A шлёт код согласия', sent[0].value === 'approve', `значение ${sent[0].value}`)
  }
}

// R отклоняет, а для набора изменений отказ уходит своим путём.
{
  const ui = open()
  ui.press('KeyR')
  const sent = ui.posted.filter(m => m.type === 'resolveDecision')
  check('R отправляет отказ', sent.length === 1 && sent[0].value === 'deny', JSON.stringify(sent[0] || {}))

  const other = open()
  other.press('KeyJ')
  other.press('KeyR')
  const rejected = other.posted.filter(m => m.type === 'resolveDecision')
  check('R по набору изменений уходит своим путём',
    rejected.length === 1 && rejected[0].path === '/api/change-sets/d2/reject',
    JSON.stringify(rejected[0] || {}))
}

// Набор текста важнее горячих клавиш.
for (const target of [{ tagName: 'INPUT' }, { tagName: 'TEXTAREA' }, { tagName: 'DIV', isContentEditable: true }]) {
  const ui = open()
  ui.press('KeyA', target)
  ui.press('KeyR', target)
  const sent = ui.posted.filter(m => m.type === 'resolveDecision')
  check(`в поле ${target.tagName}${target.isContentEditable ? ' (contenteditable)' : ''} клавиши молчат`,
    sent.length === 0, `отправлено ${sent.length}`)
}

// Удержание клавиши: браузер сам шлёт серию повторов. Решение обязано уйти
// одно. Ответ ядра может прийти посреди серии — тогда список сместится, и
// оставшиеся повторы разрешили бы уже другое решение, которого человек не видел.
{
  const ui = open()
  for (let i = 0; i < 5; i += 1) ui.press('KeyA', null, i > 0)
  const sent = ui.posted.filter(m => m.type === 'resolveDecision')
  check('удержание A отправляет одно согласие', sent.length === 1, `отправлено ${sent.length}`)
}

// Быстрые отдельные нажатия — тоже одно решение: пока ответа нет, список
// показывает прежнее состояние, и второе нажатие относится уже неизвестно к чему.
{
  const ui = open()
  ui.press('KeyA')
  ui.press('KeyA')
  ui.press('KeyR')
  const sent = ui.posted.filter(m => m.type === 'resolveDecision')
  check('пока решение в пути, второе не уходит', sent.length === 1, `отправлено ${sent.length}`)
}

// Отдельно от запрета на повторную отправку: если первое решение упало быстро,
// раздел выходит из «загрузки», и удержанная клавиша отправила бы второе — уже
// без всякой преграды. Автоповтор не должен решать сам по себе.
{
  const ui = open()
  ui.press('KeyA')
  ui.listeners['window:message']({ data: { type: 'error', request: 'resolveDecision', message: 'ядро не ответило' } })
  ui.press('KeyA', null, true)
  const sent = ui.posted.filter(m => m.type === 'resolveDecision')
  check('после отказа автоповтор не решает за человека', sent.length === 1, `отправлено ${sent.length}`)
}

// Двойной щелчок по кнопке — та же опасность, что и удержание клавиши, и
// закрыт он тем же местом: отправкой, а не обработчиком нажатия.
{
  const ui = open()
  const click = () => ui.listeners['root:click']({
    target: { closest: sel => (sel === '[data-action]' ? { dataset: {
      action: 'resolve-decision', id: 'd1', path: '/api/approvals/d1', field: 'decision', value: 'approve',
    } } : null) },
    preventDefault() {},
  })
  click()
  click()
  const sent = ui.posted.filter(m => m.type === 'resolveDecision')
  check('двойной щелчок отправляет одно решение', sent.length === 1, `отправлено ${sent.length}`)
}

// Перебор списка удержанием — наоборот, полезен и остаётся работать.
{
  const ui = open()
  ui.press('KeyJ')
  ui.press('KeyJ', null, true)
  check('удержание J продолжает перебор', ui.selected() === 'd3', `выбрано ${ui.selected()}`)
}

// Кириллическая раскладка: та же физическая клавиша даёт «ф» вместо «a». Если
// смотреть на букву, подсказка не работает ровно у тех, для кого написан
// интерфейс.
{
  const ui = open()
  ui.listeners['root:keydown']({
    code: 'KeyA', key: 'ф', target: { tagName: 'DIV' },
    preventDefault() {}, ctrlKey: false, metaKey: false, altKey: false,
  })
  const sent = ui.posted.filter(m => m.type === 'resolveDecision')
  check('на кириллице «ф» работает как A', sent.length === 1 && sent[0].value === 'approve', JSON.stringify(sent[0] || {}))
}

// Кнопки и клавиши берут действие из одного места. Раньше это проверялось по
// разметке — по атрибутам с путём на кнопке; теперь запрос собирается из самого
// решения, а не переносится через разметку (атрибут умеет только строку, а часть
// маршрутов ждёт булево). Проверяем то же обещание по отправленному запросу:
// нажатие кнопки обязано слать ровно то, что послала бы клавиша.
{
  const ui = open()
  ui.press('KeyJ')
  const html = ui.root.innerHTML
  const click = intent => ui.listeners['root:click']({
    target: { closest: sel => (sel === '[data-action]'
      ? { dataset: { action: 'resolve-decision', id: 'd2', intent } }
      : null) },
    preventDefault() {},
  })
  check('кнопка отказа набора ведёт своим путём',
    (click('reject'), ui.posted.at(-1)?.path === '/api/change-sets/d2/reject'),
    `отправлено ${JSON.stringify(ui.posted.at(-1))}`)
  const before = ui.posted.length
  ui.listeners['window:message']({ data: { type: 'decisions', decisions: {
    total: 1,
    items: [{ id: 'd2', kind: 'change-set', title: 'Второе', risk: 'MEDIUM',
      resolve: { path: '/api/change-sets/d2/apply', accept: 'apply', reject: '/api/change-sets/d2/reject' } }],
  } } })
  check('кнопка согласия набора ведёт на применение',
    (click('accept'), ui.posted.length > before && ui.posted.at(-1)?.path === '/api/change-sets/d2/apply'),
    `отправлено ${JSON.stringify(ui.posted.at(-1))}`)
  check('подписи кнопок остались осмысленными',
    html.includes('Применить') && html.includes('Отклонить'),
    'кнопки набора подписаны обезличенно')
}

// Вне очереди решений эти клавиши не действуют.
{
  const ui = open('quests')
  ui.press('KeyA')
  check('на другой вкладке A ничего не решает',
    ui.posted.filter(m => m.type === 'resolveDecision').length === 0, 'решение отправлено с чужого экрана')
}

if (failures.length) {
  console.log('КЛАВИШИ ОЧЕРЕДИ — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('клавиши очереди решений: PASS')
