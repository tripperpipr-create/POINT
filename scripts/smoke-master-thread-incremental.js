// Ответ Мастера не пересобирает весь Чертог.
//
// Раньше каждый ход Мастера присваивал `root.innerHTML` целиком: раздел
// собирался заново вместе с рейкой, шапкой и лентой. Место чтения при этом
// терялось, и его переносили руками — снимком прокрутки в `restoreUi`, где об
// этом и написано: «разговор после каждой такой перерисовки прыгал к самой
// первой реплике». Плата за такую отрисовку росла: каждое новое место со
// скроллом требовало ещё одной строки переноса.
//
// Теперь ответ обновляет только содержимое ленты, а поле и кнопка отправки
// меняют свойства, а не разметку. Полная отрисовка осталась ровно для двух
// причин: Мастер не настроен (меняется весь раздел) и ленты нет на экране
// (человек смотрит другой раздел).
//
// Проверяется поведением: считаем присвоения `root.innerHTML` и смотрим, где
// остаётся прокрутка.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

// Лента отдаётся только тогда, когда последняя отрисовка её действительно
// нарисовала: иначе фейк утверждал бы наличие узла, которого на экране нет, и
// проверка разошлась бы с приложением.
function open() {
  const listeners = {}
  const posted = []
  const field = { value: '', disabled: false, focus() {}, setSelectionRange() {} }
  const sendButton = { disabled: false }
  const thread = { innerHTML: '', scrollTop: 0, scrollHeight: 1000, clientHeight: 300 }
  let paints = 0
  let html = ''
  const root = {
    get innerHTML() { return html },
    set innerHTML(value) { paints += 1; html = value },
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector(selector) {
      if (selector === '#master-input') return html.includes('id="master-input"') ? field : null
      if (selector === '#master-thread') return html.includes('id="master-thread"') ? thread : null
      if (selector === '.hall-compose .hall-compose-send') return html.includes('hall-compose') ? sendButton : null
      return null
    },
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set,
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {}, setTimeout(callback) { callback(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
    selectedTab: 'master',
    boot: {
      onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
      runs: [], quests: [], executions: [], changeSets: [], questProposals: [],
      orchestrator: { id: 'o1', preset: 'conductor' },
    },
  } })

  const reply = (text, extra = {}) => listeners['window:message']({ data: { type: 'master', master: {
    configured: true,
    config: { model: 'qwen' },
    history: [{ role: 'assistant', content: text }],
    ...extra,
  } } })
  // Прокрутка ленты — единственный способ сообщить приложению, что человек
  // читает выше и доезжать до конца не нужно.
  const scrollTo = top => {
    thread.scrollTop = top
    listeners['root:scroll']({ target: { id: 'master-thread', scrollTop: top, scrollHeight: thread.scrollHeight, clientHeight: thread.clientHeight } })
  }
  return { listeners, posted, root, thread, field, sendButton, reply, scrollTo, paintsOf: () => paints }
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 200)}`)
}

// ── Ход Мастера обновляет ленту, а не раздел ──────────────────────────────
{
  const ui = open()
  check('лента нарисована при открытии',
    ui.root.innerHTML.includes('id="master-thread"'),
    'раздел Мастера открылся без ленты — обновлять нечего')

  const before = ui.paintsOf()
  ui.reply('Разобрал проект: три пакета, тесты зелёные.')
  check('раздел не пересобран целиком',
    ui.paintsOf() === before,
    `root.innerHTML присвоен ${ui.paintsOf() - before} раз(а) — ответ снова пересобирает Чертог`)
  check('ответ появился в ленте',
    ui.thread.innerHTML.includes('Разобрал проект: три пакета, тесты зелёные.'),
    'лента не получила реплику — точечная замена ничего не нарисовала')
}

// ── Место чтения переживает ответ ─────────────────────────────────────────
{
  const ui = open()
  ui.reply('Первая реплика.')
  // Человек ушёл читать выше: до дна 600 пикселей.
  ui.scrollTo(100)
  ui.reply('Вторая реплика.')
  check('прокрутка осталась там, где читали',
    ui.thread.scrollTop === 100,
    `прокрутка уехала на ${ui.thread.scrollTop} — свежий ответ выбросил человека из места чтения`)

  // А у дна ответ обязан доехать до конца: иначе новую реплику не видно.
  ui.scrollTo(ui.thread.scrollHeight - ui.thread.clientHeight)
  ui.reply('Третья реплика.')
  check('у дна лента доезжает до свежей реплики',
    ui.thread.scrollTop === ui.thread.scrollHeight,
    `прокрутка осталась на ${ui.thread.scrollTop} — новый ответ оказался за экраном`)
}

// ── Поле отправки размораживается без замены разметки ─────────────────────
{
  const ui = open()
  ui.reply('Готов.')
  ui.field.value = 'Собери отряд'
  ui.field.disabled = true
  ui.sendButton.disabled = true
  ui.reply('Отряд собран.')
  check('поле отперто после ответа',
    ui.field.disabled === false && ui.sendButton.disabled === false,
    'поле или кнопка остались запертыми — разговор встал')
  check('черновик очищен вместе с состоянием',
    ui.field.value === '',
    `в поле осталось «${ui.field.value}» — ядро приняло реплику, а поле про это не знает`)
}

// ── Причины полной отрисовки ──────────────────────────────────────────────
{
  const ui = open()
  ui.reply('Готов.')
  const before = ui.paintsOf()
  ui.listeners['window:message']({ data: { type: 'master', master: { configured: false } } })
  check('незаданный Мастер перерисовывает раздел',
    ui.paintsOf() > before,
    'раздел не пересобран — на экране осталась лента вместо экрана настройки')
  check('показан экран настройки, а не пустая лента',
    !ui.root.innerHTML.includes('id="master-thread"'),
    'лента осталась на экране, хотя Мастер не настроен')
}


{
  const ui = open()
  const sessions = active => ({active,mode:'auto',memory:'',items:[{id:'a',title:'A'},{id:'b',title:'B'}]})
  const deliver = active => ui.listeners['window:message']({data:{type:'master',sessionChanged:true,master:{configured:true,history:[],sessions:sessions(active)}}})
  deliver('a')
  ui.listeners['root:input']({target:{id:'master-input',value:'Черновик первого чата',closest:()=>null,matches:()=>false}})
  deliver('b')
  check('новый чат не получает чужой черновик', !ui.root.innerHTML.includes('>Черновик первого чата</textarea>'), 'чужой черновик')
  deliver('a')
  check('возврат восстанавливает черновик чата', ui.root.innerHTML.includes('>Черновик первого чата</textarea>'), 'черновик потерян')
  deliver('a')
  check('смена настроек сохраняет черновик', ui.root.innerHTML.includes('>Черновик первого чата</textarea>'), 'черновик потерян')
}

// ── Ход от отправки до истории: без провала и без повтора ─────────────────
//
// Между событием `done` и приходом истории ход держался на признаке «идёт
// ход», а тот гас первым: реплика человека и ответ пропадали из ленты и
// возвращались через мгновение. Теперь ход в это время стоит своим блоком в
// фазе «оседания», а с приходом истории уходит — и ответ остаётся ровно один.
{
  const ui = open()
  const deliver = extra => ui.listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, sessions: { active: 'c1', items: [{ id: 'c1', title: 'C' }] }, history: [], ...extra,
  } } })
  deliver()
  ui.field.value = 'Почини вебхук оплаты'
  ui.listeners['root:input']({ target: { id: 'master-input', value: 'Почини вебхук оплаты', closest: () => null, matches: () => false } })
  ui.listeners['root:click']({ target: { closest: selector => (selector === '[data-action]' ? { dataset: { action: 'master-send' } } : null) }, preventDefault() {} })
  const turnId = [...ui.posted].reverse().find(message => message.type === 'masterChat')?.turnId
  check('реплика ушла в ядро с ходом', Boolean(turnId), JSON.stringify(ui.posted.slice(-2)))
  const event = (type, text, detail) => ui.listeners['window:message']({ data: { type: 'masterEvent', event: {
    turnId, conversationId: 'c1', type, text: text || '', detail: detail ? JSON.stringify(detail) : '',
  } } })
  const paints = ui.paintsOf()
  event('tools', 'read_file', { tool: 'read_file', argument: 'webhook.go' })
  event('tool_result', '', { tool: 'read_file', result: 'package billing' })
  event('reply', 'Нашёл **обработчик**')
  event('reply', 'Нашёл **обработчик** и поправил повтор.')
  check('события хода не пересобирают раздел', ui.paintsOf() === paints,
    `root.innerHTML присвоен ${ui.paintsOf() - paints} раз(а) за ход`)
  check('ответ в потоке уже оформлен', ui.thread.innerHTML.includes('<strong>обработчик</strong>'),
    'в потоке сырые звёздочки — на финише текст перескочит в оформленный')
  event('done', 'completed')
  const between = ui.thread.innerHTML
  check('между done и историей реплика человека на месте', between.includes('Почини вебхук оплаты'),
    'своя реплика пропала из ленты до прихода истории')
  check('между done и историей ответ на месте', between.includes('поправил повтор') && between.includes('data-phase="settling"'),
    'ответ пропал из ленты до прихода истории')
  check('после конца хода курсора нет', !between.includes('hall-stream-caret'), 'курсор мигает у законченного ответа')
  ui.listeners['window:message']({ data: { type: 'master', turnFinished: true, master: {
    configured: true, config: { model: 'qwen' }, sessions: { active: 'c1', items: [{ id: 'c1', title: 'C' }] },
    history: [
      { id: 'u1', role: 'user', turnId, content: 'Почини вебхук оплаты' },
      { id: 'a1', role: 'assistant', turnId, mode: 'model', content: 'Нашёл **обработчик** и поправил повтор.' },
    ],
  } } })
  const after = ui.thread.innerHTML
  check('с историей ответ стоит один раз', (after.match(/поправил повтор/g) || []).length === 1,
    `ответ в ленте ${(after.match(/поправил повтор/g) || []).length} раз(а)`)
  check('с историей блок хода ушёл', !after.includes('data-master-stream'), 'блок хода остался рядом с записью истории')
  check('лента — область, а не живой журнал', /id="master-thread" role="region"/.test(ui.root.innerHTML) && !/id="master-thread"[^>]*aria-live/.test(ui.root.innerHTML),
    'лента с aria-live зачитывается заново на каждой пересборке')
  check('диктор стоит вне ленты', /<\/div>\s*<div class="hall-sr" id="master-announcer" role="status" aria-live="polite"/.test(ui.root.innerHTML),
    'диктора нет или он внутри пересобираемой ленты')
  // Считается тело реплики, а не строка: текст стоит ещё и в атрибуте кнопки
  // «Изменить и отправить заново».
  check('с историей реплика человека не задвоилась', (after.match(/<p>Почини вебхук оплаты<\/p>/g) || []).length === 1,
    'своя реплика стоит дважды — ожидающая и сохранённая')
}

// ── Поток оборвался: написанное остаётся, отправка не заперта ──────────────
{
  const ui = open()
  const deliver = extra => ui.listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, sessions: { active: 'c1', items: [{ id: 'c1', title: 'C' }] }, history: [], ...extra,
  } } })
  deliver()
  ui.field.value = 'Проверь миграцию'
  ui.listeners['root:input']({ target: { id: 'master-input', value: 'Проверь миграцию', closest: () => null, matches: () => false } })
  ui.listeners['root:click']({ target: { closest: selector => (selector === '[data-action]' ? { dataset: { action: 'master-send' } } : null) }, preventDefault() {} })
  const turnId = [...ui.posted].reverse().find(message => message.type === 'masterChat')?.turnId
  ui.listeners['window:message']({ data: { type: 'masterEvent', event: { turnId, conversationId: 'c1', type: 'reply', text: 'Начал проверку схемы', detail: '' } } })
  ui.listeners['window:message']({ data: { type: 'masterStreamError', conversationId: 'c1', message: 'соединение с ядром потеряно' } })
  const html = ui.thread.innerHTML
  check('написанное до сбоя осталось в ленте', html.includes('Начал проверку схемы'), 'частичный ответ пропал вместе с потоком')
  check('сбой назван в ленте', html.includes('hall-turn-error') && html.includes('соединение с ядром потеряно'), 'причина сбоя не показана у ответа')
  check('«Повторить» задаёт тот же вопрос', html.includes('data-action="master-retry-turn" data-message="Проверь миграцию"'), 'повторить нечего')
  deliver()
  // Разметка раздела с тех пор не пересобиралась: замок снимает досборка
  // композера, и мерить надо саму кнопку, а не прежнюю строку разметки.
  ui.sendButton.disabled = true
  deliver()
  check('следующий ответ ядра не запирает отправку', ui.sendButton.disabled === false,
    'после сбоя отправка осталась запертой')
}

if (failures.length) {
  console.error('\nОТВЕТ МАСТЕРА ПЕРЕСОБИРАЕТ ЧЕРТОГ:')
  for (const message of failures) console.error('  · ' + message)
  process.exit(1)
}
console.log('\nответ Мастера обновляет ленту, не пересобирая раздел')
