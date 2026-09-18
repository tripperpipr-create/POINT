// Экраны, которые человек видит раньше всех остальных.
//
// Пока ядро не поднялось, Хаб показывает не разделы, а состояние службы. Это
// первое, что видит человек при поломке, и здесь легче всего соврать: показать
// пустую гильдию вместо «ядро не запущено», или сказать «ошибка» и не назвать
// причину, которую расширение уже знает.
//
// Проверяем три состояния и то, что у каждого есть своё действие: запускается —
// сообщить и ждать, остановлено — предложить запустить, ошибка — назвать
// причину, дать повтор и путь к полному логу.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function screen(service, boot, layout = 'wide') {
  const listeners = {}
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: () => null,
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })
  listeners['window:message']({ data: {
    type: 'state', service, workspaceTrusted: true, workspace: 'w', selectedTab: 'overview', boot,
  } })
  return root.innerHTML.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim()
}

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

// Защита от холостого хода: с поднятым ядром Хаб обязан показывать сам себя, а
// не экран службы. Иначе все проверки ниже прошли бы на одном и том же экране.
const alive = screen({ state: 'running' }, {
  onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
  runs: [], quests: [], executions: [], changeSets: [],
})
if (/Ядро не поднялось|Гильдия отдыхает|Пробуждаем локальное ядро/.test(alive)) {
  console.log('с поднятым ядром показан экран службы — проверки прошли бы вхолостую')
  process.exit(1)
}

const starting = screen({ state: 'starting' }, undefined)
check('запуск: сказано, что ядро поднимается', /Пробуждаем локальное ядро/.test(starting), starting.slice(0, 120))
check('запуск: не выдаётся за поломку', !/не поднялось|ошибк/i.test(starting), 'на экране запуска говорится об ошибке')

const stopped = screen({ state: 'stopped' }, undefined)
check('остановлено: есть чем запустить', /Пробудить ядро/.test(stopped), stopped.slice(0, 120))

const reason = 'listen tcp 127.0.0.1:8081: bind: address already in use'
const failed = screen({ state: 'error', detail: reason }, undefined)
check('ошибка: названа настоящая причина', failed.includes(reason), failed.slice(0, 160))
check('ошибка: есть повтор запуска', /Повторить запуск/.test(failed), 'кнопки повтора нет')
check('ошибка: есть путь к полному логу', /Хроника ядра/.test(failed), 'ссылки на хронику нет')

// Причина может и не прийти — тогда экран обязан остаться осмысленным, а не
// показывать пустое место там, где ждали текст.
const failedSilent = screen({ state: 'error', detail: '' }, undefined)
check('ошибка без причины: экран не пустой',
  /Ядро не поднялось/.test(failedSilent) && failedSilent.length > 60,
  failedSilent.slice(0, 120))

// У компаньона в боковой панели свой экран для того же случая — и он тоже
// показывает причину. Их два, они написаны по отдельности, и проверка одного
// ничего не говорит о втором: мутация в одном месте роняла только его.
const dockFailed = screen({ state: 'error', detail: reason }, undefined, 'companion-peek')
check('компаньон: названа настоящая причина', dockFailed.includes(reason), dockFailed.slice(0, 160))
check('компаньон: есть чем запустить ядро',
  /Пробудить ядро|Повторить запуск/.test(dockFailed),
  dockFailed.slice(0, 160))

if (failures.length) {
  console.log('ЭКРАНЫ СОСТОЯНИЯ ЯДРА — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('экраны состояния ядра: PASS')
