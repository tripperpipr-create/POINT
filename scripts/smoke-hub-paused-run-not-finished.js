// Приостановленный прогон — не законченный.
//
// Ядро знает восемь статусов прогона, а интерфейс задавал вопрос «прогон ещё не
// закончился» пятью разными списками. Шесть мест перечисляли
// ['pending','running','waiting_approval'] — без 'paused'. Пауза ставится самим
// человеком кнопкой, и после неё Хаб считал прогон завершённым: показывал
// «ЗАВЕРШЁН С ДОКАЗАТЕЛЬСТВОМ» на остановленной работе, сравнивал неполную
// диагностику как итоговую и терял прогон при поиске незавершённых.
//
// Проверяем по видимому следствию, а не по формуле.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function screen(status) {
  const listeners = {}
  // Панель хода работы рисуется не в корень, а в существующий узел .chat-main
  // (paintRun). Без него разметка не появляется вовсе, и проверять было бы
  // нечего — первая версия этого смоука на том и споткнулась.
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

  const run = { id: 'r1', status, task: 'починить сборку', agentId: 'a1', profileId: 'p1' }
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true,
    workspace: 'w', selectedTab: 'quests',
    boot: {
      onboarded: true, profiles: [{ id: 'p1', name: 'Кузнец' }], projectAgents: [{ id: 'a1', name: 'Кузнец' }],
      usageRecords: [], runs: [run], quests: [], executions: [], changeSets: [],
    },
    details: {
      run,
      diagnostics: { verification: { required: true, recorded: true, successfulCommands: 2 } },
    },
  } })
  return root.innerHTML.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')
}

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

// Защита от холостого хода: если доказательство не рисуется даже для
// завершённого прогона, все запреты пройдут сами собой и ничего не докажут.
const done = screen('completed')
if (!/Завершён с доказательством/.test(done)) {
  console.log('доказательство завершения не отрисовалось — проверки прошли бы вхолостую')
  process.exit(1)
}

check('завершённый прогон показывает доказательство', /Завершён с доказательством/.test(done), 'не показано')

for (const status of ['paused', 'running', 'waiting_approval', 'pending']) {
  const html = screen(status)
  check(`прогон в состоянии ${status} не объявляется завершённым`,
    !/Завершён с доказательством|Завершён без доказательства|Проверка не требовалась/.test(html),
    'на экране висит итог незаконченной работы')
}

// Остальные терминальные статусы итог показывают — иначе правка съела бы
// нужное вместе с лишним.
for (const status of ['failed', 'cancelled', 'interrupted']) {
  const html = screen(status)
  check(`прогон в состоянии ${status} показывает итог`,
    /Завершён с доказательством/.test(html),
    'итог законченной работы пропал')
}

if (failures.length) {
  console.log('ПАУЗА И ЗАВЕРШЕНИЕ — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('приостановленный прогон не считается законченным: PASS')
