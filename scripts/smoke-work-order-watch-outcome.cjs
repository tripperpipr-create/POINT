// Исход наряда доезжает до всех вкладок, а наблюдатель прежнего проекта молчит.
//
// Наблюдатель наряда обновлял только чат. Статус квеста в снимке, который
// читают список квестов проекта, счётчики и «текущий квест», оставался
// прежним: настройки проекта показывали завершённый квест активным. А после
// смены проекта тот же наблюдатель продолжал слать карточку и историю своей
// беседы — в первом чате нового проекта, который тоже зовётся `legacy`.
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const assert = require('node:assert/strict')

const source = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'master-work-order-watch.js'), 'utf8')
const sandbox = { module: { exports: {} }, setTimeout: fn => setImmediate(fn), String, Set, Map, Promise, JSON, encodeURIComponent }
vm.runInNewContext(source, sandbox, { filename: 'master-work-order-watch.js' })
const { watchMasterWorkOrder } = sandbox.module.exports

function makeHost(statuses) {
  const host = {
    projectEpoch: 0, posted: [], snapshots: 0, statePosts: 0,
    post(message) { this.posted.push(message) },
    postState() { this.statePosts++ },
    async refreshRuntimeState() { this.snapshots++ },
    service: {
      hostLog() {},
      async request(url) {
        if (url.startsWith('/api/v2/work-orders/')) return { id: 'wo-1', runtime: { status: statuses.shift() || 'completed', updatedAt: String(statuses.length) } }
        if (url.startsWith('/api/master/history')) return { sessions: { active: 'legacy' }, history: [] }
        return {}
      },
    },
  }
  return host
}

;(async () => {
  const finished = makeHost(['running', 'completed'])
  await watchMasterWorkOrder(finished, 'wo-1', 'legacy')
  assert.ok(finished.snapshots >= 1, 'исход наряда не обновил снимок квестов для вкладок')
  assert.ok(finished.statePosts >= 1, 'обновлённый снимок квестов не разослан вкладкам')
  const refresh = finished.posted.find(message => message.type === 'master' && message.completionRefresh)
  assert.equal(refresh?.conversationId, 'legacy', 'обновление истории не называет свою беседу')

  const switched = makeHost(['running', 'completed'])
  const watching = watchMasterWorkOrder(switched, 'wo-1', 'legacy')
  switched.projectEpoch++
  await watching
  assert.deepEqual(switched.posted, [], 'наблюдатель прежнего проекта прислал ответ после переключения')
  assert.equal(switched.snapshots, 0, 'наблюдатель прежнего проекта обновил снимок нового')

  console.log('work order watch: outcome refreshes quest snapshot, silent after project switch: PASS')
})().catch(error => { console.error(error); process.exitCode = 1 })
