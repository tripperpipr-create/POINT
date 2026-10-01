// Провал этапа, который Point доводит без человека.
//
// Решает ядро: сбой среды помечен autoRetry.allowed, безопасное предложение
// Мастера — autoApply. Окно исполняет: повторяет этап с ключом маршрута, один
// раз на провал и на предложение. Где решение за человеком, окно зовёт Мастера
// разобрать провал — тоже один раз — и ничего не повторяет само.
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const assert = require('node:assert/strict')

const source = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'master-work-order-watch.js'), 'utf8')
const followed = []
const sandbox = {
  module: { exports: {} }, setTimeout: fn => setImmediate(fn), String, Number, Math, Array, Set, Map, Promise, JSON, encodeURIComponent,
  require: name => { if (name === './master-turn-stream') return { followMasterTurn: async (_, turn) => followed.push(turn) }; throw new Error('unexpected require ' + name) },
}
vm.runInNewContext(source, sandbox, { filename: 'master-work-order-watch.js' })
const { stageFailureAutopilot, stageFailurePrompt } = sandbox.module.exports

function makeHost() {
  return {
    projectEpoch: 0, calls: [], boot: { currentWorkspace: { id: 'ws-1' } },
    post() {}, postState() {}, async refreshRuntimeState() {},
    async credentialFor() { return 'route-key' },
    async credentialForOrchestrator() { return 'master-key' },
    service: {
      hostLog() {},
      async request(url, options) {
        this.owner.calls.push({ url, body: options?.body ? JSON.parse(options.body) : null })
        if (url.startsWith('/api/v2/work-orders/')) return { id: 'wo-1', runtime: { status: 'completed' } }
        if (url === '/api/v2/master/turns') return { id: 'turn-1' }
        return {}
      },
    },
  }
}
const order = (failure, proposal) => ({ id: 'wo-1', conversationId: 'chat-1', routing: { mode: 'fixed', fixedConnectionId: 'conn-1' },
  runtime: { status: 'awaiting_user', questId: 'quest-1', stageFailure: failure, stageRetryProposal: proposal } })
const failure = (extra = {}) => ({ at: '2026-09-30T12:37:56Z', nodeId: 'accept', nodeName: 'Accept', diagnosis: { class: 'runtime', checks: [{ criterionId: 'verify-pack', cause: 'npm 12 заблокировал скрипты установки', hint: 'Node 20' }] }, ...extra })

;(async () => {
  // Сбой среды: повтор без правок, с ключом маршрута наряда, один раз.
  const env = makeHost(); env.service.owner = env
  const envOrder = order(failure({ autoRetry: { allowed: true, delaySeconds: 0 } }))
  await stageFailureAutopilot(env, envOrder, 'chat-1')
  await stageFailureAutopilot(env, envOrder, 'chat-1')
  const envRetries = env.calls.filter(call => call.url.endsWith('/retry'))
  assert.equal(envRetries.length, 1, 'environment failure retried more than once')
  assert.deepEqual([envRetries[0].body.source, envRetries[0].body.apiKey, envRetries[0].body.proposalDigest], ['auto', 'route-key', undefined])

  // Безопасное предложение Мастера: повтор с его digest.
  const safe = makeHost(); safe.service.owner = safe
  await stageFailureAutopilot(safe, order(failure(), { digest: 'sha256:safe', autoApply: true, needsApproval: false, failureAt: '2026-09-30T12:37:56Z' }), 'chat-1')
  const safeRetry = safe.calls.find(call => call.url.endsWith('/retry'))
  assert.equal(safeRetry?.body.proposalDigest, 'sha256:safe', 'safe proposal not applied by itself')

  // Правка проверки ждёт человека: окно ничего не повторяет и Мастера не зовёт.
  const approval = makeHost(); approval.service.owner = approval
  await stageFailureAutopilot(approval, order(failure(), { digest: 'sha256:crit', autoApply: false, needsApproval: true, failureAt: '2026-09-30T12:37:56Z' }), 'chat-1')
  assert.equal(approval.calls.length, 0, 'a criterion change was acted on without the human')

  // Решение за человеком и предложения нет: Мастер разбирает провал, один раз.
  const human = makeHost(); human.service.owner = human
  await stageFailureAutopilot(human, order(failure()), 'chat-1')
  await stageFailureAutopilot(human, order(failure()), 'chat-1')
  const turns = human.calls.filter(call => call.url === '/api/v2/master/turns')
  assert.equal(turns.length, 1, 'the Master was asked more than once about one failure')
  assert.equal(human.calls.some(call => call.url.endsWith('/retry')), false, 'retried without the right to')
  assert.equal(turns[0].body.conversationId, 'chat-1')
  assert.ok(turns[0].body.message.includes('npm 12 заблокировал скрипты установки') && turns[0].body.message.includes('propose_stage_retry'), 'the prompt carries the cause and the tool')
  assert.equal(followed.length, 1, 'the Master turn is not followed')
  assert.ok(stageFailurePrompt('q', { nodeName: 'Accept', error: 'boom' }).includes('boom'))
  console.log(JSON.stringify({ stageFailureAutopilot: 'ok', environmentRetryOnce: true, safeProposalApplied: true, criterionWaitsForHuman: true, masterAskedOnce: true }))
})().catch(error => { console.error(error); process.exitCode = 1 })
