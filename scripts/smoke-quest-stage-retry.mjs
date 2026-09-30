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
console.log(JSON.stringify({ stageRetry: 'ok', heldButtons: true, verdictHasNoRetry: true, oneRequest: true }))
