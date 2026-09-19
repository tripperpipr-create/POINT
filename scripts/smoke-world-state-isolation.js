// Состояние одного мира не переживает переключение на другой.
//
// `CONTRIBUTING.md` требует: «Bootstrap, usage, change sets and runs must not
// leak across workspaceId». Течь давала не сама ячейка с данными, а её сторож.
//
// Экраны Хаба грузятся лениво: пока `statisticsStatus === 'idle'`, отрисовка
// один раз просит `loadStatistics` и переводит сторож в `loading`, а ответ
// ставит `ready`. Сторож переключение мира не переживал только на словах —
// в `resetProjectScopedState` его не было. Поэтому после смены проекта
// `statisticsStatus` оставался `ready`, ленивая загрузка не срабатывала уже
// никогда, и экран навсегда показывал расход прежнего мира.
//
// Хуже цифр была форма бюджета: она заполняется из тех же устаревших данных,
// а сохранение уходит в текущий мир. Человек видел лимиты проекта A и,
// нажав «Сохранить бюджет», переносил их на проект B.
//
// Проверка ведёт вебвью через оба состояния и требует ровно того, что сломалось:
// после смены мира запрос обязан уйти повторно, а цифры прежнего мира — исчезнуть.
const fs = require('fs')
const path = require('path')
const vm = require('vm')

const script = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')

function bootWebview(layout) {
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  const context = {
    acquireVsCodeApi: () => ({
      postMessage(message) { posted.push(message) },
      getState() { return undefined },
      setState() { },
    }),
    document: {
      getElementById: id => (id === 'root' ? root : undefined),
      body: { dataset: { layout } },
      addEventListener() { },
      querySelector() { return null },
      querySelectorAll() { return [] },
    },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console,
    Date,
    Map,
    Set,
    CSS: { escape(value) { return String(value) } },
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() { },
    setTimeout(callback) { callback(); return 0 },
    clearTimeout() { },
    setInterval() { return 0 },
    clearInterval() { },
  }
  context.globalThis = context
  vm.runInNewContext(script, context, { filename: 'media/main.js' })
  const send = message => listeners['window:message']({ data: message })
  return { posted, root, send }
}

const worldState = workspacePath => ({
  type: 'state',
  workspacePath,
  workspace: { path: workspacePath, name: workspacePath.slice(workspacePath.lastIndexOf('/') + 1) },
  selectedTab: 'statistics',
  service: { state: 'running' },
  workspaceTrusted: true,
  boot: { profiles: [], projectAgents: [], quests: [], teams: [], flows: [], blueprints: [], skills: [], customTools: [], connections: [], questProposals: [], companionActionProposals: [] },
})

const statisticsFor = (dailyCostCents, budgetDailyCents) => ({
  type: 'statistics',
  statistics: { dailyCostCents, monthlyCostCents: dailyCostCents, budgetDailyCents, budgetMonthlyCents: budgetDailyCents, agentStats: [], agentImprovements: [] },
})

const countRequests = posted => posted.filter(message => message.type === 'loadStatistics').length

const hub = bootWebview('statistics')

// Мир A: экран просит статистику один раз и показывает её.
hub.send(worldState('C:/worlds/alpha'))
if (countRequests(hub.posted) !== 1) {
  throw new Error(`мир A: ожидался один запрос loadStatistics, было ${countRequests(hub.posted)}`)
}
hub.send(statisticsFor(4200, 9900))
if (!hub.root.innerHTML.includes('99.00')) {
  throw new Error('мир A: лимит бюджета не доехал до формы — проверять на этом дальше нечего')
}

// Мир B: тот же экран, другой проект.
hub.send(worldState('C:/worlds/beta'))

if (countRequests(hub.posted) !== 2) {
  throw new Error(
    `смена мира не сбросила сторож ленивой загрузки: запросов loadStatistics ${countRequests(hub.posted)}, ожидалось 2. `
    + 'Экран остался бы на расходе прежнего проекта навсегда.',
  )
}
if (hub.root.innerHTML.includes('99.00')) {
  throw new Error(
    'после смены мира форма бюджета всё ещё несёт лимит прежнего проекта — '
    + 'сохранение перенесло бы его в новый мир',
  )
}
if (hub.root.innerHTML.includes('42.00')) {
  throw new Error('после смены мира на экране остался расход прежнего проекта')
}

// И наоборот: ответ нового мира приходит на тот же экран.
hub.send(statisticsFor(100, 500))
if (!hub.root.innerHTML.includes('5.00')) {
  throw new Error('мир B: свежая статистика не отрисовалась')
}

console.log('smoke-world-state-isolation: ok')
