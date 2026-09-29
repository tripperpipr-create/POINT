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
  const saved = []
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
      setState(value) { saved.push(value) },
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
  return { posted, root, send, saved }
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

// Черновик первого разговора. Первый разговор каждого проекта — `legacy`, а
// черновики разговоров master-inbox держит псевдонимом. Сброс мира подменял
// объект черновиков новым, псевдоним оставался при старом — и набранное в
// проекте A всплывало в поле первого чата проекта B.
const chat = bootWebview('master')
const masterView = active => ({ sessions: { active, items: [{ id: 'legacy', title: 'Первый разговор' }, { id: 'other', title: 'Другой' }] }, history: [], workOrders: [], configured: true })
chat.send({ ...worldState('C:/worlds/alpha'), selectedTab: 'master' })
chat.send({ type: 'master', master: masterView('legacy'), loaded: true, draft: 'черновик мира A' })
chat.send({ type: 'master', master: masterView('other'), sessionChanged: true })
chat.send({ ...worldState('C:/worlds/beta'), selectedTab: 'master' })
chat.send({ type: 'master', master: masterView('legacy'), sessionChanged: true })
const lastSaved = chat.saved.at(-1) || {}
if (lastSaved.masterDraft === 'черновик мира A' || Object.values(lastSaved.masterSessionDrafts || {}).includes('черновик мира A')) {
  throw new Error('черновик первого разговора проекта A всплыл в первом разговоре проекта B')
}

console.log('smoke-world-state-isolation: ok')
