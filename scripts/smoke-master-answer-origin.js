// Чем отвечено — моделью Мастера или движком Point — видно на экране.
//
// Ядро различает это честно: в ответе стоят mode, model и причина отката.
// Но история хранит только текст реплик, и без переноса метка не появлялась бы
// никогда: человек настраивает модель, она молча не отвечает, и разговор
// выглядит исправным. Разница между «ответила модель» и «модель не ответила,
// отвечает движок» — та же, ради которой в остальном интерфейсе разведены
// пустота и незнание.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const listeners = {}
const posted = []
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector() { return null },
  querySelectorAll() { return [] },
}
const context = {
  acquireVsCodeApi: () => ({
    postMessage(message) { posted.push(message) },
    getState() { return undefined },
    setState() { },
  }),
  document: { getElementById: id => id === 'root' ? root : undefined, body: { dataset: { layout: 'wide' } } },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set,
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() { },
  setTimeout(callback) { callback(); return 0 },
  clearTimeout() { },
}

const source = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')
vm.runInNewContext(source, context, { filename: 'media/main.js' })

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

const history = [
  { id: 'm1', role: 'user', content: 'Сколько есть агентов?' },
  { id: 'm2', role: 'assistant', content: 'В ростере 1 агент: Разведчик.' },
]

// 1. Ответила модель — так и сказано, и названа какая.
{
  const stamped = context.stampMasterAnswer(history, { mode: 'model', model: 'qwen3', fallbackReason: '' })
  const html = context.masterThreadHtml(stamped)
  check('модельный ответ помечен', /ответила модель мастера/.test(html), `разметка: ${html.slice(0, 300)}`)
  check('названа модель, которая отвечала', /qwen3/.test(html), `разметка: ${html.slice(0, 300)}`)
  check('метка стоит на реплике Мастера, а не на реплике человека',
    html.indexOf('ответила модель') > html.indexOf('В ростере 1 агент'),
    'метка оказалась выше ответа Мастера')
}

// 2. Модель не ответила — движок отвечает, причина названа.
{
  const stamped = context.stampMasterAnswer(history, {
    mode: 'deterministic', model: '', fallbackReason: 'connection refused',
  })
  const html = context.masterThreadHtml(stamped)
  check('откат назван прямо', /ответил движок Point/.test(html), `разметка: ${html.slice(0, 300)}`)
  check('причина отката видна', /connection refused/.test(html), `разметка: ${html.slice(0, 300)}`)
}

// 3. Модель не настроена — метки нет: сообщать не о чем, а лишняя метка на
//    каждой реплике превратилась бы в шум.
{
  const stamped = context.stampMasterAnswer(history, { mode: 'deterministic', model: '', fallbackReason: '' })
  const html = context.masterThreadHtml(stamped)
  check('без модели метка не появляется',
    !/ответил движок Point/.test(html) && !/ответила модель/.test(html),
    `разметка: ${html.slice(0, 300)}`)
}

// 4. Подпись под чатом описывает то, что происходит сейчас, а не прежнее
//    разделение «разговор — движок, модель только на запуске квеста».
{
  // masterData — переменная модуля: снаружи её не присвоить, поэтому ставим
  // настоящим путём — сообщением от расширения, как это делает живой ход.
  listeners['window:message']({
    data: { type: 'master', master: { configured: true, config: { model: 'qwen3' }, history, response: {} } },
  })
  const label = context.masterModelLabel()
  check('подпись называет модель разговора',
    /модель мастера — qwen3/.test(label) && !/при запуске квеста/.test(label),
    `подпись: ${JSON.stringify(label)}`)

  listeners['window:message']({
    data: { type: 'master', master: { configured: true, config: { model: '' }, history, response: {} } },
  })
  const noModel = context.masterModelLabel()
  check('без модели подпись говорит об этом прямо',
    /модель мастера не подключена/.test(noModel),
    `подпись: ${JSON.stringify(noModel)}`)
}

// 5. Проводка, а не только помощники.
//
// Первая версия этой проверки звала stampMasterAnswer напрямую и потому
// оставалась зелёной, когда вызов убирали из отрисовки: метка исчезала с экрана
// молча. Ведём через настоящий рендер раздела.
{
  listeners['window:message']({
    data: {
      type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
      selectedTab: 'master',
      boot: {
        onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
        runs: [], quests: [], executions: [], changeSets: [], questProposals: [],
        orchestrator: { id: 'o1', preset: 'conductor', model: 'qwen3' },
      },
    },
  })
  listeners['window:message']({
    data: {
      type: 'master',
      master: {
        configured: true, config: { model: 'qwen3' }, history,
        response: { mode: 'deterministic', model: '', fallbackReason: 'connection refused' },
      },
    },
  })
  check('на отрисованном экране виден откат и его причина',
    /ответил движок Point/.test(root.innerHTML) && /connection refused/.test(root.innerHTML),
    `на экране: ${root.innerHTML.slice(0, 400)}`)
}

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('\nпроисхождение ответа Мастера видно на экране')
