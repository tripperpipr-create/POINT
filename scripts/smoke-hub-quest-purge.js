// Снос квеста: одна кнопка вместо трёх отказов подряд.
//
// Удаление квеста беречёт сделанное и отказывается, пока работа идёт: живой
// прогон, идущая схема и незакрытый набор правок держат квест по очереди, а
// правки в проекте остаются и после того, как квест наконец удалось убрать.
// Человек, решивший, что этой работы быть не должно, обходил три отказа в трёх
// разделах. Здесь проверяется, что у него есть один путь — и что этот путь
// назван словами, а не спрятан под тем же «удалить».
//
// Подтверждение спрашивает оболочка (extension.js), поэтому вебвью обязан лишь
// отправить намерение: смоук смотрит на сообщение, а не на модальное окно.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 400)}`)
}

function screen() {
  const listeners = {}
  const posted = []
  const chatMain = { innerHTML: '', scrollHeight: 0, scrollTop: 0, clientHeight: 0 }
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: sel => (sel === '.chat-main' ? chatMain : null),
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(msg) { posted.push(msg) }, getState() { return {} }, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set, structuredClone, CSS: { escape: String }, TextEncoder,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  const boot = {
    onboarded: true,
    service: { state: 'running' },
    workspace: { id: 'ws', path: 'C:/proj', name: 'proj' },
    profiles: [],
    projectAgents: [{ id: 'a1', name: 'Dev', provider: 'ollama', primaryModel: 'qwen', model: 'qwen', allowedTools: ['read_file'] }],
    // Квест в самом неудобном для удаления состоянии: прогон идёт, схема
    // выполняется, набор правок ждёт решения. Именно здесь три отказа и жили.
    quests: [{ id: 'q-live', title: 'Починить оплату', status: 'active', flowId: 'flow-1', teamAgentIds: ['a1'] }],
    flows: [{ id: 'flow-1', nodes: [{ id: 'stage-1', kind: 'agent', name: 'Правка', agentId: 'a1' }] }],
    flowRuns: [{ id: 'fr-1', flowId: 'flow-1', questId: 'q-live', status: 'running', nodeStates: { 'stage-1': { status: 'running' } } }],
    executions: [{ id: 'ex-1', questId: 'q-live', runId: 'run-1', projectAgentId: 'a1', status: 'running', task: 'правка обработчика' }],
    changeSets: [{ id: 'cs-1', questId: 'q-live', executionId: 'ex-1', title: 'Правка обработчика', status: 'pending', items: [{ id: 'ci-1', path: 'billing.go' }] }],
    runs: [{ id: 'run-1', status: 'running', task: 'правка обработчика' }],
    teams: [], skills: [], connections: [], usageRecords: [],
  }
  listeners['window:message']({
    data: { type: 'state', service: { state: 'running' }, boot, selectedTab: 'quests', workspaceTrusted: true, workspace: 'ws' },
  })
  const click = (action, id) => listeners['root:click']({
    target: { closest: sel => (sel === '[data-action]' ? { dataset: { action, id }, closest: () => null } : null) },
    preventDefault() {},
  })
  return { html: () => root.innerHTML + chatMain.innerHTML, posted, click }
}

const ui = screen()
// Строка квеста раскрывается: блок уборки живёт там, где видно, что за квестом стоит.
ui.click('toggle-quest', 'q-live')
const html = ui.html()

check('обе кнопки стоят рядом',
  html.includes('data-action="delete-quest"') && html.includes('data-action="purge-quest"'),
  'у квеста либо нет сноса, либо снос подменил собой удаление — это разные решения')

check('снос назван тем, что он делает',
  /Снести квест и откатить изменения/.test(html),
  'кнопка названа так же, как соседнее удаление: человек не отличит бережное от необратимого')

check('перед кнопкой сказано, что исчезнет',
  html.includes('останавливает прогон') && html.includes('откатывает изменения') && html.includes('Записи о потраченном остаются'),
  'снос предлагается без единого слова о последствиях')

ui.click('purge-quest', 'q-live')
const purge = ui.posted.filter(message => message.type === 'purgeQuest')
check('нажатие уходит в ядро намерением, а не удалением',
  purge.length === 1 && purge[0].id === 'q-live',
  `отправлено: ${JSON.stringify(ui.posted.slice(-3))}`)

ui.click('delete-quest', 'q-live')
check('обычное удаление осталось прежним',
  ui.posted.some(message => message.type === 'deleteQuest' && message.id === 'q-live'),
  'снос забрал маршрут бережного удаления')

if (failures.length) {
  console.error('\nКВЕСТ НЕ СНЕСТИ:\n  ' + failures.join('\n  '))
  process.exitCode = 1
} else {
  console.log('\nСнос квеста: кнопка, слова и намерение на месте: PASS')
}
