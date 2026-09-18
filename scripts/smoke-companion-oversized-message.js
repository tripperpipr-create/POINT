// Слишком длинная реплика не должна стоить человеку набранного.
//
// Ядро отклоняет сообщение длиннее 32 КБ, а вставить в чат лог целиком —
// обычное дело. До проверки на стороне интерфейса реплика успевала уйти из
// поля: в ленте она была, отправить её было нельзя, а вернуть — только
// копированием из собственного сообщения. Проверяем поведением: набранное
// остаётся в поле, причина названа человеческими словами, ядро не тревожится.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

function open(text) {
  const listeners = {}
  const posted = []
  const field = { value: text, focus() {}, setSelectionRange() {} }
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector: selector => (selector === '#companion-input' ? field : null),
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'companion' } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set, TextEncoder, CSS: { escape: value => String(value) },
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {}, setTimeout(callback) { callback(); return 0 }, clearTimeout() {},
  }, { filename: 'media/main.js' })
  listeners['window:message']({
    data: {
      type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'overview',
      boot: {
        questProposals: [], companionActionProposals: [], projectAgents: [], profiles: [], flows: [], skills: [],
        runs: [], toolCatalog: [], connections: [],
        companion: { id: 'c1', preset: 'balanced', configured: true, provider: 'ollama', model: 'qwen' },
        companionMessages: [],
      },
      details: undefined,
    },
  })
  field.value = text
  listeners['root:submit']({ target: { id: 'companion-form' }, preventDefault() {} })
  return { posted, html: () => root.innerHTML }
}

// Лог на 40 КБ: латиница, чтобы байты считались честно, а не по числу символов.
const huge = open('x'.repeat(40 * 1024))
check('слишком длинная реплика до ядра не доходит',
  !huge.posted.some(message => message.type === 'companionChat'),
  'сообщение ушло в ядро, хотя оно его не примет')
// Именно в поле, а не «где-то на экране»: реплика, попавшая только в ленту,
// возвращается человеку копированием, и это ровно та плата, которой быть не должно.
const composerText = html => (html.match(/<textarea id="companion-input"[^>]*>([^<]*)<\/textarea>/) || [])[1] || ''
check('набранное остаётся в поле ввода',
  composerText(huge.html()).startsWith('x'.repeat(64)),
  'текст пропал из поля вместе с отказом')
check('сказано, во что упёрлись',
  huge.html().includes('Сообщение не помещается') && huge.html().includes('32 КБ'),
  'предел не назван человеческими словами')
check('сказано, что делать',
  huge.html().includes('помощник прочитает файл сам'),
  'выход не подсказан')

// Кириллица весит вдвое: 20 тысяч знаков — это 40 КБ, и проверка по символам
// пропустила бы их в ядро.
const cyrillic = open('я'.repeat(20 * 1024))
check('кириллица меряется байтами, а не знаками',
  !cyrillic.posted.some(message => message.type === 'companionChat'),
  'сообщение из 20 тысяч кириллических знаков ушло в ядро')

// Обычная реплика проходит без помех.
const normal = open('Почему падает сборка?')
check('обычная реплика уходит как прежде',
  normal.posted.some(message => message.type === 'companionChat'),
  'проверка длины съела обычное сообщение')

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const item of failures) console.error('  · ' + item)
  process.exit(1)
}
console.log('\nдлинная реплика не стоит человеку набранного')
