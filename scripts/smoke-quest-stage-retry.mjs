// Повтор этапа с места сбоя.
//
// Этап Flow провалился — квест ждёт решения человека, а не получает вердикт.
// В блоке «Этап завершился ошибкой» у такого квеста две кнопки: «Повторить
// этап» продолжает тот же Flow с проваленного этапа, «Завершить квест»
// выносит вердикт. У квеста, который уже получил вердикт, их нет: пакет
// доказательств у квеста один, и дальше — только новая версия наряда.
import assert from 'node:assert/strict'
import { workOrderExecutionParts } from '../vscode-extension/ui/client/work-order-execution-views.js'
import { handleMasterClickAction } from '../vscode-extension/ui/client/master-actions.js'
import { esc } from '../vscode-extension/ui/client/html-escape.js'
import { preAcceptNoteHtml } from '../vscode-extension/ui/client/pre-accept-views.js'

const order = status => ({ id: 'order-1', runtime: {
  questId: 'quest-1', status,
  stages: [{ id: 'verify', name: 'Проверка сборки', status: 'failed' }],
  stall: { nodeId: 'verify', nodeName: 'Проверка сборки', waitReason: 'stage_failed', error: 'workspace mutation audit failed' },
} })
const stall = status => workOrderExecutionParts(order(status), {}, { esc })?.stall || ''

const held = stall('awaiting_user')
assert.ok(held.includes('data-control="retry"') && held.includes('Повторить этап'), 'retry button for a held stage failure')
assert.ok(held.includes('data-control="finalize"') && held.includes('Завершить квест'), 'finalize button for a held stage failure')
assert.ok(held.includes('Обсудить новую версию'), 'new version path stays')
const settled = stall('blocked')
assert.ok(!settled.includes('data-control="retry"') && settled.includes('Обсудить новую версию'), 'a verdict quest offers only a new version')

const posted = []
const ui = { masterWorkOrderBusy: new Set() }
const target = { dataset: { id: 'order-1', questId: 'quest-1', control: 'retry' }, closest: () => null }
assert.equal(handleMasterClickAction({ action: 'control-master-work-order-v2', target, ui, render() {}, vscode: { postMessage: message => posted.push(message) } }), true)
assert.equal(posted[0]?.type, 'controlMasterWorkOrderQuestV2')
assert.equal(posted[0]?.action, 'retry')
handleMasterClickAction({ action: 'control-master-work-order-v2', target, ui, render() {}, vscode: { postMessage: message => posted.push(message) } })
assert.equal(posted.length, 1, 'double click sends one retry')

// Разобранный провал (30.09): причина по каждой проверке вместо «x Build
// failed», образ проверки, среды для повтора — рекомендованная первой.
const diagnosed = orderStatus => {
  const value = order(orderStatus)
  value.runtime.stall.error = 'Accept: verify-pack: x Build failed in 17.43s'
  value.runtime.stageFailure = {
    at: '2026-09-30T12:37:56Z', nodeId: 'verify', nodeName: 'Accept', autoRetry: { allowed: false },
    diagnosis: { class: 'runtime', image: 'point-agent-sandbox:release', checks: [
      { criterionId: 'verify-pack', command: 'cd cf-vue-apps && npm ci && npm run verify', class: 'runtime', cause: 'npm 12 заблокировал скрипты установки: vue-demi, ssh2', hint: 'повторить в образе Node 20 или 22' },
    ] },
  }
  return value
}
const why = workOrderExecutionParts(diagnosed('awaiting_user'), {}, { esc })?.stall || ''
assert.ok(why.includes('npm 12 заблокировал скрипты установки') && why.includes('verify-pack') && why.includes('point-agent-sandbox:release'), 'the cause, not the build summary')
assert.ok(!why.includes('x Build failed in 17.43s'), 'the raw summary is replaced by the diagnosis')
// Кнопок под отдельную причину нет: что менять в повторе, решает Мастер.
assert.ok(!why.includes('data-runtime=') && !why.includes('Повторить в ') && why.includes('data-control="retry"'), 'no cause-specific retry buttons')
assert.ok(why.includes('data-action="analyze-stage-failure"') && why.includes('Разобрать с Мастером'), 'the Master can be asked')

// Сбой среды Point повторит сам — человек видит это, а не ждёт кнопки.
const environment = diagnosed('awaiting_user')
environment.runtime.stageFailure.autoRetry = { allowed: true, delaySeconds: 30, attempt: 1, max: 2 }
assert.ok((workOrderExecutionParts(environment, {}, { esc })?.stall || '').includes('Point повторит этап сам через 30 с'), 'automatic retry is announced')

// Правка проверки ждёт разрешения: было и станет рядом, одна кнопка.
const amended = diagnosed('awaiting_user')
amended.runtime.stageRetryProposal = { digest: 'sha256:abc', needsApproval: true, autoApply: false, diagnosis: 'npm pack не создаёт каталог',
  criteria: [{ criterionId: 'tgz-content', previousCommand: 'npm pack --pack-destination /tmp/p', command: 'mkdir -p /tmp/p && npm pack --pack-destination /tmp/p', reason: 'нет каталога' }] }
const card = workOrderExecutionParts(amended, {}, { esc })?.stall || ''
assert.ok(card.includes('Разрешить и повторить') && card.includes('data-proposal-digest="sha256:abc"'), 'approval button carries the proposal digest')
assert.ok(card.includes('mkdir -p /tmp/p &amp;&amp; npm pack') && card.includes('было') && card.includes('станет'), 'the exact diff is shown')

const sent = []
const retryUi = { masterWorkOrderBusy: new Set(), masterData: { sessions: { active: 'chat-1' } } }
const click = (action, dataset) => handleMasterClickAction({ action, target: { dataset, closest: () => null }, ui: retryUi, render() {}, vscode: { postMessage: message => sent.push(message) } })
assert.equal(click('retry-work-order-stage', { id: 'order-1', questId: 'quest-1', proposalDigest: 'sha256:abc' }), true)
assert.deepEqual([sent[0].type, sent[0].action, sent[0].proposalDigest], ['controlMasterWorkOrderQuestV2', 'retry', 'sha256:abc'])
retryUi.masterWorkOrderBusy.clear()
click('analyze-stage-failure', { id: 'order-1' })
assert.deepEqual([sent[1].type, sent[1].conversationId], ['analyzeStageFailureWithMaster', 'chat-1'])
// Проверка Point перед приёмкой: карточка говорит, сколько проверок прошло, а
// без прогона молчит.
assert.equal(preAcceptNoteHtml({}, esc), '')
assert.equal(preAcceptNoteHtml({ preAcceptCheck: { passed: 0, total: 0, allPassed: true } }, esc), '')
const failedNote = preAcceptNoteHtml({ preAcceptCheck: { passed: 1, total: 3, allPassed: false } }, esc)
assert.ok(failedNote.includes('1 из 3 прошли') && failedNote.includes('исправляет') && failedNote.includes('data-pre-accept-check="failed"'), failedNote)
const passedNote = preAcceptNoteHtml({ preAcceptCheck: { passed: 3, total: 3, allPassed: true } }, esc)
assert.ok(passedNote.includes('3 из 3 прошли') && !passedNote.includes('исправляет'), passedNote)
console.log(JSON.stringify({ stageRetry: 'ok', heldButtons: true, verdictHasNoRetry: true, oneRequest: true, diagnosis: true, autoRetryNotice: true, approvalCard: true, preAcceptNote: true }))
