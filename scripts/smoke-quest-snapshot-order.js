// Снимок квеста, снятый раньше, не возвращает отменённый квест в работу (Q01).
//
// Состояние мира запрашивают наблюдатель наряда, поток хода и кнопки, и ответ,
// снятый до отмены, приходил после ответа, снятого после неё. Хост расширения
// (`patchBoot`) и вебвью (`state`, наряды Мастера) принимали его целиком, и
// вкладки снова показывали отменённый квест «в работе».
//
// Проверка ведёт через оба конца: правило в `snapshot-order.js` и вебвью,
// собранное из него, на экране обзора.
const fs = require('fs')
const path = require('path')
const vm = require('vm')
const order = require('../vscode-extension/snapshot-order.js')

const t1 = '2026-09-29T10:00:00.000Z'
const t2 = '2026-09-29T10:05:00.000Z'
const quest = (status, updatedAt) => ({ id: 'q-1', title: 'Квест снимка', status, updatedAt })
const workOrder = (status, updatedAt) => ({ id: 'wo-1', runtime: { status, updatedAt } })

// Хост: поздний ответ /api/state/runtime.
let boot = order.mergeBootSnapshot(undefined, { quests: [quest('running', t1)], workOrders: [workOrder('running', t1)] })
boot = order.mergeBootSnapshot(boot, { quests: [quest('cancelled', t2)], workOrders: [workOrder('cancelled', t2)] })
boot = order.mergeBootSnapshot(boot, { quests: [quest('running', t1)], workOrders: [workOrder('running', t1)] })
if (boot.quests[0].status !== 'cancelled' || boot.workOrders[0].runtime.status !== 'cancelled') {
  throw new Error(`устаревший снимок вернул отменённое в работу: квест ${boot.quests[0].status}, наряд ${boot.workOrders[0].runtime.status}`)
}
// Состав задаёт свежий ответ: удалённый квест уходит, новый — приходит.
boot = order.mergeBootSnapshot(boot, { quests: [{ id: 'q-2', status: 'running', updatedAt: t2 }] })
if (boot.quests.length !== 1 || boot.quests[0].id !== 'q-2') throw new Error('слияние удержало квест, которого в новом снимке нет')
// Без времени снимок принимается: иначе состояние можно заморозить.
if (order.upsertNewer([workOrder('running', t2)], { id: 'wo-1', runtime: { status: 'paused' } }, order.workOrderStamp)[0].runtime.status !== 'paused') {
  throw new Error('снимок без времени отброшен')
}
if (order.upsertNewer([workOrder('running', t1)], workOrder('completed', t2), order.workOrderStamp)[0].runtime.status !== 'completed') {
  throw new Error('свежий наряд не принят')
}

// Вебвью: экран обзора после отмены и позднего старого снимка.
const script = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')
const listeners = {}
const root = { innerHTML: '', addEventListener(type, callback) { listeners[`root:${type}`] = callback }, querySelector() { return null }, querySelectorAll() { return [] } }
const context = {
  acquireVsCodeApi: () => ({ postMessage() { }, getState() { return undefined }, setState() { } }),
  document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'overview' } }, addEventListener() { }, querySelector() { return null }, querySelectorAll() { return [] } },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set,
  CSS: { escape(value) { return String(value) } },
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() { }, setTimeout() { return 0 }, clearTimeout() { }, setInterval() { return 0 }, clearInterval() { },
}
context.globalThis = context
vm.runInNewContext(script, context, { filename: 'media/main.js' })
const send = message => listeners['window:message']({ data: message })
const state = quests => ({
  type: 'state', workspacePath: 'C:/worlds/alpha', workspace: { path: 'C:/worlds/alpha', name: 'alpha' }, selectedTab: 'overview',
  service: { state: 'running' }, workspaceTrusted: true,
  boot: { profiles: [], projectAgents: [], quests, teams: [], flows: [], blueprints: [], skills: [], customTools: [], connections: [], questProposals: [], companionActionProposals: [] },
})
const current = () => /<h2 title="Квест снимка">/.test(root.innerHTML)

send(state([quest('running', t1)]))
if (!current()) throw new Error('живой квест не стал текущим — проверять дальше нечего')
send(state([quest('cancelled', t2)]))
if (current()) throw new Error('отменённый квест остался текущим')
send(state([quest('running', t1)]))
if (current()) throw new Error('поздний снимок, снятый до отмены, вернул квест в текущие')

console.log('smoke-quest-snapshot-order: ok')
