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

if (failures.length) {
  console.error('\nОТВЕТ МАСТЕРА ПЕРЕСОБИРАЕТ ЧЕРТОГ:')
  for (const message of failures) console.error('  · ' + message)
  process.exit(1)
}
console.log('\nответ Мастера обновляет ленту, не пересобирая раздел')
