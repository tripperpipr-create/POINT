// Обзор обязан держать своё обещание.
//
// Вкладка подписана «Что требует внимания прямо сейчас» — это обещание, и его
// можно проверить. Ожидающее решение и набор на ревью экран показывал, идущий
// запуск показывал, а упавший прогон не упоминал вовсе: человек узнавал о
// неудаче, только если сам шёл в хронику.
//
// Проверяем и обратное: отменённый вручную прогон и вчерашняя неудача сюда
// попадать не должны, иначе панель «что сломано» превратится в шум и её
// перестанут читать.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function overview(bootExtra, decisions) {
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
    onboarded: true, profiles: [{ id: 'p1', name: 'модель' }], projectAgents: [{ id: 'a1', name: 'Кузнец' }],
    usageRecords: [], runs: [], quests: [], executions: [], changeSets: [], ...bootExtra,
  }
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true,
    workspace: 'w', selectedTab: 'overview', boot,
  } })
  if (decisions) listeners['window:message']({ data: { type: 'decisions', decisions } })
  // Боковая навигация не считается: значок вкладки — это не содержимое обзора.
  const html = root.innerHTML
  const navEnd = html.indexOf('</nav>')
  return (navEnd < 0 ? html : html.slice(navEnd)).replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')
}

const now = () => new Date().toISOString()
const daysAgo = days => new Date(Date.now() - days * 24 * 60 * 60 * 1000).toISOString()

const cases = [
  {
    имя: 'прогон ждёт разрешения человека',
    got: overview({ runs: [{ id: 'r1', status: 'waiting_approval', task: 'записать файл', agentId: 'a1' }] },
      { total: 1, items: [{ id: 'ap1', kind: 'approval', title: 'Записать файл', risk: 'HIGH', resolve: { path: '/api/approvals/ap1', accept: 'approve', reject: 'deny' } }] }),
    обязано: [/ждёт|ожида|решени/i],
  },
  {
    имя: 'набор изменений ждёт ревью',
    got: overview({ changeSets: [{ id: 'cs1', status: 'pending', items: [{ path: 'a.go' }] }] }, null),
    обязано: [/набор|ревью/i],
  },
  {
    имя: 'свежая неудача названа',
    got: overview({ runs: [{ id: 'r1', status: 'failed', task: 'сборка', agentId: 'a1', finishedAt: now() }] }, null),
    обязано: [/ПРОГОН/, /1 прогон не дошёл до конца/],
  },
  {
    имя: 'две неудачи считаются вместе и склоняются',
    got: overview({ runs: [
      { id: 'r1', status: 'failed', finishedAt: now() },
      { id: 'r2', status: 'interrupted', finishedAt: now() },
    ] }, null),
    обязано: [/2 прогона не дошли до конца/],
  },
  {
    имя: 'неудача без отметок времени всё равно названа',
    got: overview({ runs: [{ id: 'r1', status: 'failed' }] }, null),
    обязано: [/прогон не дошёл до конца/],
  },
  {
    имя: 'вчерашняя неудача сюда не попадает',
    got: overview({ runs: [{ id: 'r1', status: 'failed', finishedAt: daysAgo(3) }] }, null),
    запрещено: [/не дошёл до конца/, /не дошли до конца/],
  },
  {
    имя: 'отменённый вручную прогон — не поломка',
    got: overview({ runs: [{ id: 'r1', status: 'cancelled', finishedAt: now() }] }, null),
    запрещено: [/не дошёл до конца/, /не дошли до конца/],
  },
]

const failures = []
for (const c of cases) {
  for (const rule of c.обязано || []) {
    if (!rule.test(c.got)) failures.push(`${c.имя}: на экране не сказано ${rule}`)
  }
  for (const rule of c.запрещено || []) {
    if (rule.test(c.got)) failures.push(`${c.имя}: на экране сказано лишнее ${rule}`)
  }
}

// Защита от холостого хода: если бы обзор вообще не рисовался, запреты прошли бы
// сами собой, а разрешения провалились бы — проверяем, что экран не пуст.
const sample = overview({}, null)
if (!/ТЕКУЩИЙ КВЕСТ|ЗДОРОВЬЕ|ОБЗОР/.test(sample)) {
  console.log('обзор не отрисовался — проверки прошли бы вхолостую')
  process.exit(1)
}

if (failures.length) {
  console.log('ОБЗОР НЕ ДЕРЖИТ ОБЕЩАНИЕ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('обзор называет то, что требует внимания')
