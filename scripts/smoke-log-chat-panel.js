// Панель «Помощник · логи» отвечает на глазах, а не молчит до конца разбора.
//
// Разбор логов занимает десятки секунд. Панель показывала пустоту всё это время,
// а отправленная реплика появлялась только после ответа — если он вообще
// приходил. Здесь проверяется живое поведение самой панели: её скрипт
// исполняется как в webview, с теми же сообщениями, что шлёт расширение.

const Module = require('module')
const path = require('path')
const vm = require('vm')

const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      window: { showErrorMessage() {}, createWebviewPanel: () => ({ webview: {} }) },
      workspace: { getConfiguration: () => ({ get: (_key, fallback) => fallback }), onDidChangeConfiguration: () => ({ dispose() {} }) },
      commands: { registerCommand: () => ({ dispose() {} }), executeCommand: () => Promise.resolve() },
      languages: { getDiagnostics: () => [] },
      Uri: { file: value => ({ fsPath: String(value) }) },
      EventEmitter: class { constructor() { this.event = () => ({ dispose() {} }) } fire() {} dispose() {} },
      ViewColumn: { One: 1 },
      env: { openExternal: () => Promise.resolve(true) },
    }
  }
  return originalLoad.call(this, request, parent, isMain)
}

const { __test } = require(path.join(__dirname, '..', 'vscode-extension', 'extension.js'))
Module._load = originalLoad

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

const html = __test.AgentViewProvider.prototype.logChatHtml.call({})
const script = html.slice(html.lastIndexOf('<script nonce='), html.lastIndexOf('</script>'))
const source = script.slice(script.indexOf('>') + 1)

// Разметку панели заменяет минимальный DOM: проверяется её скрипт, а не браузер.
function element(id) {
  const node = {
    id,
    children: [],
    value: '',
    disabled: false,
    hidden: false,
    textContent: '',
    className: '',
    scrollTop: 0,
    scrollHeight: 100,
    listeners: {},
    addEventListener(type, callback) { this.listeners[type] = callback },
    append(...items) { this.children.push(...items) },
    replaceChildren(...items) { this.children = [...items] },
  }
  return node
}

const nodes = {
  thread: element('thread'),
  input: element('input'),
  send: element('send'),
  error: element('error'),
  meta: element('meta'),
  form: element('form'),
  refresh: element('refresh'),
}
const posted = []
const context = {
  acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) } }),
  document: {
    getElementById: id => nodes[id],
    createElement: tag => element(tag),
  },
  window: { addEventListener(type, callback) { context.__message = type === 'message' ? callback : context.__message } },
  console,
}
vm.runInNewContext(source, context, { filename: 'log-chat-panel.js' })

const send = message => nodes.form.listeners.submit({ preventDefault() {}, target: nodes.form })
const receive = data => context.__message({ data })
const threadText = () => nodes.thread.children.map(node => node.children.map(child => child.textContent).join(' ')).join(' | ')

check('панель здоровается с расширением', posted.some(item => item.type === 'ready'),
  JSON.stringify(posted))

nodes.input.value = 'Почему упал запрос req_abc?'
send()
check('вопрос уходит расширению', posted.some(item => item.type === 'send' && item.message.includes('req_abc')),
  JSON.stringify(posted))
check('своя реплика видна сразу', threadText().includes('req_abc'),
  threadText())
check('пока ответа нет, видно, что идёт разбор', threadText().includes('Разбираю логи'),
  threadText())

receive({ type: 'attached', lines: 12, total: 180 })
check('видно, по чему отвечают', nodes.meta.textContent.includes('12') && nodes.meta.textContent.includes('180'),
  nodes.meta.textContent)

receive({ type: 'delta', text: 'Запрос оборвался ' })
receive({ type: 'delta', text: 'по таймауту ядра.' })
check('ответ виден по мере поступления', threadText().includes('Запрос оборвался по таймауту ядра.'),
  threadText())
check('заглушка уступает место ответу', !threadText().includes('Разбираю логи'),
  threadText())

receive({
  type: 'state',
  messages: [
    { role: 'user', content: 'Почему упал запрос req_abc?' },
    { role: 'assistant', content: 'Запрос оборвался по таймауту ядра.' },
  ],
  snapshot: { counts: { error: 2, warning: 1 } },
})
check('после ответа лента живёт историей', threadText().includes('таймауту ядра'), threadText())
check('своя реплика не задваивается',
  threadText().split('req_abc').length - 1 === 1,
  threadText())
check('счётчик приложенных строк остаётся на месте',
  nodes.meta.textContent.includes('12') && nodes.meta.textContent.includes('2 ошибок'),
  nodes.meta.textContent)

// Пока идёт разбор, поле заперто: второй вопрос не уходит и не теряется.
nodes.input.value = 'А что с индексом?'
send()
check('во время разбора вопрос не уходит вторым',
  posted.filter(item => item.type === 'send').length === 1,
  JSON.stringify(posted.filter(item => item.type === 'send')))

// Отказ не стирает вопрос: человек должен видеть, что отправлял.
receive({ type: 'busy', value: false })
nodes.input.value = 'А что с индексом?'
send()
receive({ type: 'error', message: 'Провайдер не знает модель «qwen».' })
check('при отказе вопрос остаётся на экране', threadText().includes('индексом'), threadText())
check('текст отказа показан', nodes.error.textContent.includes('не знает модель'), nodes.error.textContent)

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const item of failures) console.error('  · ' + item)
  process.exit(1)
}
console.log('\nпанель логов отвечает на глазах')
