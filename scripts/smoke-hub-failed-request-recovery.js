// Что видит человек, когда запрос не удался.
//
// Раздел уходит в 'loading' перед запросом, а выйти может только приходом
// ответа. При отказе ответа не будет: полоса ошибки скажет причину, но раздел
// останется в «загрузка…» до переоткрытия панели, и повторить нечем.
//
// Канал отказа у расширения один и давно существует — notify шлёт
// {type:'error'}. Смоук обязан идти именно им: первая версия этой проверки
// слала выдуманное мной сообщение, и её падение доказывало лишь отсутствие
// моего же канала, а не дефект в продукте.
//
// Проверяем не чтением кода, а поведением: щёлкаем, роняем запрос, шлём
// обычное обновление состояния и смотрим, вышел ли раздел из загрузки.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

const listeners = {}
const posted = []
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector() { return null },
  querySelectorAll() { return [] },
}
const context = {
  acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
  document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set,
  requestAnimationFrame(cb) { cb(); return 0 },
  cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
}
vm.runInNewContext(main, context, { filename: 'main.js' })

const decisions = {
  total: 1,
  items: [{
    id: 'ap-1', kind: 'approval', title: 'Записать файл', risk: 'HIGH', detail: 'agent · run-1',
    resolve: { path: '/api/approvals/ap-1', accept: 'approve', reject: 'deny' },
  }],
}
const boot = {
  onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
  runs: [{ id: 'run-1', status: 'waiting_approval' }],
  quests: [], executions: [], changeSets: [],
}
const sendState = () => listeners['window:message']({ data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true,
  workspace: 'w', selectedTab: 'decisions', boot,
} })

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

sendState()
listeners['window:message']({ data: { type: 'decisions', decisions } })

// Проверка на холостой ход: без отрисованной кнопки весь смоук ничего не значит.
if (!root.innerHTML.includes('resolve-decision')) {
  console.log('очередь не отрисовала кнопку решения — смоук прошёл бы вхолостую')
  process.exit(1)
}

const click = dataset => listeners['root:click']({ target: {
  closest(selector) {
    if (selector === '[data-action]') return { dataset }
    return null
  },
}, preventDefault() {} })

click({ action: 'resolve-decision', id: 'ap-1', path: '/api/approvals/ap-1', field: 'decision', value: 'approve' })

check('нажатие уходит в расширение',
  posted.some(m => m.type === 'resolveDecision'),
  'сообщение resolveDecision не отправлено')
check('раздел показывает загрузку',
  root.innerHTML.includes('загрузка…'),
  'после нажатия загрузки не видно — проверять восстановление не из чего')

// Расширение уронило запрос: ответа нет, есть только сообщение об ошибке —
// ровно то, что шлёт notify.
listeners['window:message']({ data: {
  type: 'error', request: 'resolveDecision', message: 'ядро не ответило',
} })

// Дальше приходит обычное обновление состояния — оно случается само каждые
// несколько секунд. Именно здесь раздел обязан перестать врать про загрузку.
sendState()

check('раздел вышел из загрузки',
  !root.innerHTML.includes('загрузка…'),
  'очередь осталась в «загрузка…» навсегда — отказ её заморозил')
check('человек видит причину',
  root.innerHTML.includes('ядро не ответило') || root.innerHTML.includes('не удалось'),
  'об отказе в разделе ничего не сказано')
check('есть чем повторить',
  root.innerHTML.includes('retry-decisions'),
  'повторить неудавшийся запрос нечем')

// Повтор возвращает раздел к обычной загрузке — иначе кнопка декоративна.
const before = posted.length
click({ action: 'retry-decisions' })
check('повтор действительно запрашивает',
  posted.slice(before).some(m => m.type === 'loadDecisions'),
  'нажатие «повторить» не отправило нового запроса')

if (failures.length) {
  console.log('ВОССТАНОВЛЕНИЕ ПОСЛЕ ОТКАЗА — ПРОВАЛ')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('восстановление после отказа: PASS')
