// Форма подключения не спрашивает того, чего у источника нет.
//
// У локального Claude Code нет ни адреса, ни ключа: вход человек сделал в самом
// CLI. Пока форма показывала «Адрес сервиса» и «Токен / API-ключ», она требовала
// выдумать несуществующее — и человек справедливо спрашивал, зачем это ему.
//
// Проверяется обработчик смены источника, а не полная отрисовка: прятать поля
// он обязан сразу, до сохранения, и именно это поведение здесь и важно.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

const fields = new Map([
  ['.connection-base-url', { hidden: false }],
  ['.connection-api-key', { hidden: false }],
  ['.connection-lead-remote', { hidden: false }],
  ['.connection-lead-local', { hidden: true }],
  ['#connection-base-url', { value: '', placeholder: '' }],
])

const listeners = {}
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[type] = callback },
  querySelector(selector) { return fields.get(selector) || null },
  querySelectorAll() { return [] },
}
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8'), {
  acquireVsCodeApi: () => ({ postMessage() {}, getState() { return undefined }, setState() {} }),
  document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
  window: { addEventListener() {} },
  console, Date, Map, Set, CSS: { escape: value => String(value) },
  requestAnimationFrame() { return 0 },
  cancelAnimationFrame() {}, setTimeout() { return 0 }, clearTimeout() {},
}, { filename: 'media/main.js' })

const change = listeners.change
check('обработчик смены источника на месте', typeof change === 'function', 'форма не слушает смену источника')

if (typeof change === 'function') {
  change({ target: { id: 'connection-provider', value: 'claude-code-cli', selectedOptions: [{ dataset: { preset: 'claude-code' } }] } })
  check('адрес спрятан для локального CLI', fields.get('.connection-base-url').hidden === true, 'поле адреса осталось видимым')
  check('ключ спрятан для локального CLI', fields.get('.connection-api-key').hidden === true, 'поле токена осталось видимым')
  check('пояснение заменено на местное',
    fields.get('.connection-lead-remote').hidden === true && fields.get('.connection-lead-local').hidden === false,
    'осталось объяснение про ключ, уходящий провайдеру')

  change({ target: { id: 'connection-provider', value: 'openai-compatible', selectedOptions: [{ dataset: { preset: 'llmux' } }] } })
  check('для сетевого источника поля возвращаются',
    fields.get('.connection-base-url').hidden === false && fields.get('.connection-api-key').hidden === false,
    'поля остались скрытыми у сетевого провайдера')
  check('пояснение возвращается к ключу провайдера',
    fields.get('.connection-lead-remote').hidden === false && fields.get('.connection-lead-local').hidden === true,
    'осталось местное объяснение у сетевого источника')
}

// Скрывать поле мало — надо, чтобы разметка действительно его прятала. Правила
// с классами перебивают атрибут `hidden` по силе селектора, и однажды форма уже
// показывала адрес и ключ локальному CLI, хотя код честно ставил hidden.
const styles = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'style.css'), 'utf8')
check('атрибут hidden сильнее оформления формы',
  /label\[hidden\][^{]*\{[^}]*display:\s*none/.test(styles),
  'в стилях нет правила, которое делает hidden сильнее правил с классами')

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const item of failures) console.error('  · ' + item)
  process.exit(1)
}
console.log('\nформа подключения спрашивает только то, что у источника есть')
