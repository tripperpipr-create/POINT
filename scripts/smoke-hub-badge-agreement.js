// Число на значке вкладки против числа на экране.
//
// Для очереди решений расхождение уже оказывалось настоящим дефектом: значок
// брал одно число, раздел показывал другое, и человек не знал, какому верить.
// У квестов правка была неполной — значок считал только активные квесты, а
// список рисует все, и брал «либо квесты, либо запуски» вместо суммы: два
// активных квеста с тремя запусками без квеста давали «2» над пятью строками.
//
// Проверяем не формулу, а совпадение: сколько написано на значке и сколько
// строк на экране.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function render(tab, bootExtra) {
  const listeners = {}
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: () => null,
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })
  const boot = {
    onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
    runs: [], quests: [], executions: [], changeSets: [], ...bootExtra,
  }
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true,
    workspace: 'w', selectedTab: tab, boot,
  } })
  return root.innerHTML
}

function badgeOf(html, tab) {
  const nav = html.match(new RegExp(`<button[^>]*data-tab="${tab}"[\\s\\S]*?</button>`))
  if (!nav) return '(кнопки нет)'
  const marks = nav[0].replace(/<[^>]+>/g, ' ').match(/\d+|·/g)
  return marks ? marks[marks.length - 1] : '(нет числа)'
}
const questRows = html => (html.match(/hall-quest-row/g) || []).length

const failures = []
const check = (name, badge, screen) => {
  if (String(badge) !== String(screen)) failures.push(`${name}: значок ${badge}, на экране ${screen}`)
}

// Квесты: раздел показывает все квесты плюс запуски, не принадлежащие квестам.
const questCases = {
  'два квеста и три запуска без квеста': {
    quests: [{ id: 'q1', title: 'A', status: 'active' }, { id: 'q2', title: 'B', status: 'active' }],
    executions: [
      { id: 'e1', status: 'running', questId: '' },
      { id: 'e2', status: 'running', questId: 'нет-такого' },
      { id: 'e3', status: 'running', questId: 'тоже-нет' },
    ],
  },
  'завершённый квест с идущим запуском': {
    quests: [{ id: 'q1', title: 'A', status: 'completed' }],
    executions: [{ id: 'e1', status: 'running', questId: 'q1' }],
  },
  'два завершённых квеста и посторонний запуск': {
    quests: [{ id: 'q1', title: 'A', status: 'completed' }, { id: 'q2', title: 'B', status: 'completed' }],
    executions: [{ id: 'e1', status: 'running', questId: '' }],
  },
  'только квесты': {
    quests: [{ id: 'q1', title: 'A', status: 'active' }, { id: 'q2', title: 'B', status: 'proposed' }],
    executions: [],
  },
  'только запуск без квеста': {
    quests: [],
    executions: [{ id: 'e1', status: 'running', questId: '' }],
  },
}

for (const [name, boot] of Object.entries(questCases)) {
  const html = render('quests', boot)
  check(`квесты · ${name}`, badgeOf(html, 'quests'), questRows(html))
}

// Защита от холостого хода: если бы строки не рисовались, все сравнения
// сошлись бы на нуле и проверка ничего не значила.
const sample = render('quests', questCases['два квеста и три запуска без квеста'])
if (questRows(sample) !== 5) {
  console.log(`строки квестов не отрисовались (${questRows(sample)}) — проверки прошли бы вхолостую`)
  process.exit(1)
}

// Изменения: раздел честно печатает два числа — «всего» и «на ревью».
// Значок обязан совпадать со вторым, а не с первым.
const changesHtml = render('changesets', {
  changeSets: [
    { id: 'cs1', status: 'pending', items: [{ path: 'a.go' }] },
    { id: 'cs2', status: 'applied', items: [{ path: 'b.go' }] },
    { id: 'cs3', status: 'approved', items: [{ path: 'c.go' }] },
  ],
})
const review = (changesHtml.match(/<b>(\d+)<\/b><small>на ревью<\/small>/) || [])[1]
if (review === undefined) {
  console.log('на экране изменений не нашлось числа «на ревью» — проверка вхолостую')
  process.exit(1)
}
// Вкладка называется changesets, хотя раздел внутри зовётся changes: значок
// ищем по тому имени, что стоит в разметке.
check('изменения · значок против «на ревью»', badgeOf(changesHtml, 'changesets'), review)

if (failures.length) {
  console.log('ЧИСЛА НЕ СХОДЯТСЯ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('значки вкладок сходятся с экраном')
