// Композер Мастера: отправка, предел длины, остановка хода и рост поля.
//
// Жалоба, с которой это началось: «ни нормальной отправки, ни процесса». Поле
// было одной строкой без подсказки, без счёта, без остановки — ход шёл до трёх
// минут, и всё это время человеку оставалось смотреть на слово «Думает…».
//
// Проверяется поведением на собранном бандле, а не наличием строк в исходнике:
// половина здешних правил — про то, что происходит между нажатием и ответом.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

// Каждый случай — свой чистый мир: состояние раздела живёт в модуле.
function open() {
  const listeners = {}
  const posted = []
  const field = { id: 'master-input', value: '', rows: 2, focus() {}, setSelectionRange() {}, closest: () => null, matches: () => false }
  // Отказ по длине снимается точечной правкой узла, а не перерисовкой: иначе
  // каретка уезжала бы в конец на каждом нажатии. Поэтому узел здесь настоящий
  // (насколько он вообще бывает настоящим в стенде), а проверяется он по себе,
  // а не по innerHTML — innerHTML пересобирается только при отрисовке.
  const note = { className: 'hall-compose-note is-hidden', textContent: '' }
  // Пустота кнопки отмечается тем же точечным путём, что и отказ: на каждом
  // нажатии правится узел, а не разметка. Значит и проверять её надо по узлу —
  // innerHTML между нажатиями не пересобирается.
  const send = { marks: {}, disabled: false, setAttribute(name, value) { this.marks[name] = value } }
  const form = { className: '', querySelector: selector => (selector === '.hall-compose-send' ? send : null) }
  const nodes = { '#master-input': field, '.hall-compose-note': note, '.hall-compose': form }
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
    configured: true, config: { model: 'qwen' }, history: [],
  } } })

  const click = dataset => listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) },
    preventDefault() {},
  })
  const type = value => {
    field.value = value
    listeners['root:input']({ target: field })
  }
  const key = (name, shiftKey = false) => listeners['root:keydown']({
    key: name, code: name, shiftKey, isComposing: false,
    target: { id: 'master-input', value: field.value, tagName: 'TEXTAREA', closest: () => null },
    preventDefault() {}, ctrlKey: false, metaKey: false, altKey: false,
  })
  return { listeners, posted, root, field, note, send, form, click, type, key }
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 240)}`)
}
const sent = ui => ui.posted.filter(message => message.type === 'masterChat')

// ── Действие живёт на кнопке, а не на карточке ─────────────────────────────
//
// `data-action="master-send"` висел на самом `<form>`, а доставка кликов ищет
// ближайшего предка с `data-action`. Своего действия нет ни у поля, ни у
// подписей, ни у `<summary>` меню режимов — ближайшим для всех оказывалась
// форма, и клик в поле, чтобы поправить опечатку, отправлял черновик.
//
// Проверка ходит по разметке, а не по выдуманному действию: остальные случаи
// здесь кликают синтетическим `{action:'master-send'}` и этого увидеть не могли.
{
  const ui = open()
  ui.type('Собери отряд под миграцию базы')
  const html = ui.root.innerHTML
  const form = html.slice(html.indexOf('<form'), html.indexOf('</form>'))

  check('у карточки ввода нет собственного действия',
    !/<form[^>]*data-action=/.test(form),
    'действие снова на форме: клик в поле, в подпись или в меню режима отправит реплику')

  // Правило шире одного случая: любое действие внутри карточки обязано сидеть
  // на кнопке. Контейнер с действием ловит все клики внутри себя.
  const holders = []
  for (const match of form.matchAll(/<([a-z]+)[^>]*\sdata-action="([^"]+)"/g)) holders.push(match[1] + ' · ' + match[2])
  check('действия карточки сидят на кнопках',
    holders.length > 0 && holders.every(item => item.startsWith('button')),
    'действие на контейнере: ' + (holders.filter(item => !item.startsWith('button')).join(', ') || 'действий нет вовсе'))

  check('отправка названа на самой кнопке',
    /<button[^>]*hall-compose-send[^>]*data-action="master-send"|<button[^>]*data-action="master-send"[^>]*hall-compose-send/.test(form),
    'кнопка отправки потеряла своё действие — отправлять стало нечем')
}

// ── Пустому полю отвечают словами ──────────────────────────────────────────
{
  const ui = open()
  const html = ui.root.innerHTML
  check('пустая кнопка отправки названа пустой',
    /<button[^>]*hall-compose-send[^>]*aria-disabled="true"/.test(html),
    'кнопка выглядит живой при пустом поле — читалка про это не узнает')
  check('пустая кнопка остаётся достижимой',
    !/<button[^>]*hall-compose-send[^>]*\sdisabled/.test(html),
    'кнопка заперта насовсем — она выпала из обхода клавиатурой и молчит о причине')
  ui.click({ action: 'master-send' })
  check('нажатие на пустом поле объяснено',
    sent(ui).length === 0 && ui.root.innerHTML.includes('отправлять пока нечего'),
    'нажатие ушло в никуда без единого слова')
  ui.type('Есть что сказать')
  check('с набранным кнопка оживает',
    ui.send.marks['aria-disabled'] === 'false',
    `пометка пустоты осталась на непустом поле: ${JSON.stringify(ui.send.marks)}`)
  ui.type('')
  check('опустело — кнопка снова помечена',
    ui.send.marks['aria-disabled'] === 'true',
    `пустое поле не отмечено: ${JSON.stringify(ui.send.marks)}`)
}

// ── Набранное во время хода не выдаётся за отправленное ────────────────────
//
// Поле больше не запирается ходом, и в нём можно записать следующую мысль.
// Отправленное при этом живёт отдельной переменной: пока обе роли играл один
// masterDraft, набранное во время хода становилось «своей репликой» в ленте.
{
  const ui = open()
  ui.type('Почини вебхук оплаты')
  ui.click({ action: 'master-send' })
  check('своя реплика встала в ленту сразу',
    ui.root.innerHTML.includes('Почини вебхук оплаты'),
    'ядро ещё молчит, а в ленте пусто — непонятно, ушло ли сообщение')

  ui.type('И заодно посмотри логи')
  check('вторая мысль не ушла в ядро',
    sent(ui).length === 1,
    `во время хода ушло ${sent(ui).length} реплик вместо одной`)
  ui.click({ action: 'master-send' })
  check('повторное нажатие во время хода объяснено',
    sent(ui).length === 1 && ui.root.innerHTML.includes('Мастер ещё отвечает'),
    'нажатие во время хода либо отправило вторую реплику, либо промолчало')

  ui.listeners['window:message']({ data: { type: 'master', turnFinished: true, master: {
    configured: true, config: { model: 'qwen' },
    history: [
      { id: 'u1', role: 'user', content: 'Почини вебхук оплаты' },
      { id: 'a1', role: 'assistant', content: 'Посмотрел обработчик.' },
    ],
  } } })
  const html = ui.root.innerHTML
  check('набранное во время хода дождалось конца хода в поле',
    /<textarea[^>]*id="master-input"[^>]*>И заодно посмотри логи/.test(html),
    'мысль, записанная пока модель думала, пропала вместе с ходом')
  check('вторая мысль не попала в ленту как сказанное',
    (html.match(/И заодно посмотри логи/g) || []).length === 1,
    'набранное показано в разговоре так, будто его уже отправили')
}

// ── Клавиши отправки ───────────────────────────────────────────────────────
{
  const ui = open()
  ui.type('Собери отряд под миграцию базы')
  ui.key('Enter', true)
  check('Shift+Enter не отправляет', sent(ui).length === 0,
    'реплика ушла на переносе строки — описать задачу в несколько строк нельзя')
  check('подсказка о клавишах на экране',
    ui.root.innerHTML.includes('Shift+Enter — перенос'),
    'правило отправки нигде не написано — узнать его можно только промахнувшись')
  ui.key('Enter')
  check('Enter отправляет', sent(ui).length === 1, 'нажатие Enter не отправило ничего')
}

// ── Поле растёт по набранному ──────────────────────────────────────────────
{
  const ui = open()
  ui.type('одна строка')
  const single = ui.field.rows
  ui.type(['раз', 'два', 'три', 'четыре'].join('\n'))
  check('поле выросло под многострочную реплику', ui.field.rows > single,
    `строк было ${single}, стало ${ui.field.rows} — набранное не видно целиком`)
  ui.type(new Array(40).fill('строка').join('\n'))
  check('рост поля ограничен', ui.field.rows <= 12,
    `${ui.field.rows} строк — композер съел разговор, ради которого открыт`)
}

// ── Предел длины ───────────────────────────────────────────────────────────
{
  const ui = open()
  // Предел ядра — 32 КБ; кириллица весит два байта, поэтому 20 тысяч знаков уже
  // за ним. Договорённость 25 стережёт, чтобы числа не разъехались.
  const huge = 'я'.repeat(20_000)
  ui.type(huge)
  ui.click({ action: 'master-send' })
  check('слишком длинная реплика не уходит', sent(ui).length === 0,
    'реплика ушла в ядро, где её отклонят — а из поля она уже пропала')
  check('отказ по длине объяснён', ui.root.innerHTML.includes('не помещается'),
    'кнопка молча ничего не сделала')
  check('выход из отказа назван', ui.root.innerHTML.includes('прочитает файл сам'),
    'сказано, что нельзя, но не сказано, что делать')
  check('набранное осталось в поле', ui.root.innerHTML.includes('яяя'),
    'текст пропал вместе с отказом — набирать заново')

  // Отрисовка кладёт отказ в разметку — стенд держит разметку строкой, поэтому
  // узел ставим руками в то состояние, которое даёт настоящая отрисовка.
  // Проверяется дальше не она, а точечное снятие: перерисовывать композер на
  // каждом нажатии нельзя — уедет каретка.
  ui.note.className = 'hall-compose-note'
  ui.note.textContent = 'Сообщение не помещается: 39 КБ при пределе 32 КБ.'
  ui.type('коротко')
  check('отказ снят при правке',
    ui.note.textContent === '' && ui.note.className.includes('is-hidden'),
    'объяснение висит над уже исправленной репликой: ' + JSON.stringify(ui.note))
  ui.click({ action: 'master-send' })
  check('исправленная реплика уходит', sent(ui).length === 1, 'после отказа отправка не ожила')
}

// ── Счётчик молчит, пока запас велик ───────────────────────────────────────
{
  const ui = open()
  ui.type('короткая реплика')
  check('счётчик скрыт на короткой реплике',
    /hall-compose-count[^"]*is-hidden/.test(ui.root.innerHTML),
    'цифра у поля видна всегда — к нужному моменту её перестанут читать')
}

// ── Остановка хода ─────────────────────────────────────────────────────────
{
  const ui = open()
  ui.type('Разбери падение сборки')
  ui.click({ action: 'master-send' })
  check('пока ход идёт, остановка под рукой',
    ui.root.innerHTML.includes('stop-master-chat') && ui.root.innerHTML.includes('Остановить'),
    'ход идёт до трёх минут, и прервать его нечем')

  ui.click({ action: 'stop-master-chat' })
  check('остановка доехала до расширения',
    ui.posted.some(message => message.type === 'stopMasterChat'),
    'кнопка есть, а запрос не рвётся — обещание, которого расширение не держит')
  check('после остановки поле отперто',
    !/<textarea[^>]*id="master-input"[^>]*disabled/.test(ui.root.innerHTML),
    'разговор остался запертым после собственной остановки')
  check('про судьбу хода сказано честно',
    ui.root.innerHTML.includes('Ядро могло довести его до конца'),
    'молчание читается как «ход отменён», хотя ядро могло его закончить')
  check('кнопка остановки убрана',
    !ui.root.innerHTML.includes('Остановить'),
    'останавливать больше нечего, а кнопка предлагает')
}

if (failures.length) {
  console.log('КОМПОЗЕР МАСТЕРА СЛОМАН:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('композер Мастера: отправка, предел, остановка и рост поля: PASS')
