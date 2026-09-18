// Причина неудачи прогона — по-русски и на экране.
//
// Отказы инструментов в ленте прогона давно переводятся по коду, а строка
// «почему прогон упал» показывалась как есть: «agent final answer rejected by
// completion gate…». Это самый читаемый экран — «что случилось с моей задачей».
//
// Проверяем и обратное: незнакомую причину глотать нельзя, иначе человек
// останется вовсе без следа.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function timeline(errorText) {
  const listeners = {}
  const chatMain = { innerHTML: '', scrollHeight: 0, scrollTop: 0, clientHeight: 0 }
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: sel => (sel === '.chat-main' ? chatMain : null),
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

  const run = { id: 'r1', status: 'failed', task: 'сборка', agentId: 'a1', profileId: 'p1', error: errorText }
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w', selectedTab: 'quests',
    boot: {
      onboarded: true, profiles: [{ id: 'p1', name: 'Кузнец' }], projectAgents: [{ id: 'a1', name: 'Кузнец' }],
      usageRecords: [], runs: [run], quests: [], executions: [], changeSets: [],
    },
    details: { run, events: [{ type: 'run.failed', data: JSON.stringify({ error: errorText }) }], diagnostics: {} },
  } })
  return (root.innerHTML + chatMain.innerHTML).replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')
}

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

// Защита от холостого хода: лента обязана показывать причину вообще. Иначе все
// проверки «по-английски нет» пройдут на пустом экране.
const sample = timeline('совершенно уникальная причина отказа')
if (!sample.includes('совершенно уникальная причина отказа')) {
  console.log('лента не показывает причину неудачи — проверки прошли бы вхолостую')
  process.exit(1)
}

const cases = [
  ['финал без проверки', 'agent final answer rejected by completion gate: verification_required', /не подтвердил её проверкой/, /completion gate/],
  ['дневной лимит', 'daily hub budget exceeded; new runs are blocked', /Дневной лимит расходов исчерпан/, /budget exceeded/],
  ['время вышло', 'context deadline exceeded', /истекло отведённое время/, /deadline exceeded/],
  ['чужой проект', 'resource belongs to another project world', /другому проекту/, /belongs to another/],
]

for (const [name, english, expected, forbidden] of cases) {
  const html = timeline(english)
  check(`${name}: сказано по-русски`, expected.test(html), html.slice(0, 140))
  check(`${name}: английский текст не показан`, !forbidden.test(html), html.slice(0, 140))
}

// Незнакомая причина обязана дойти как есть.
const unknown = timeline('some unmapped core failure text')
check('незнакомая причина не теряется', unknown.includes('some unmapped core failure text'), unknown.slice(0, 140))

if (failures.length) {
  console.log('ПРИЧИНА НЕУДАЧИ ПРОГОНА — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('причина неудачи прогона переведена: PASS')
