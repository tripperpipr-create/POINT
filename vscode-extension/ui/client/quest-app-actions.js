// Нажатия блока приложения, кнопки отчёта и повтора проваленного этапа в
// карточке прогона.
//
// Жили в master-actions.js, который стоит у потолка своего бюджета строк
// (scripts/check-release-contracts.mjs); выросли, когда у приложения появились
// открытие и запуск в терминале, а у отчёта — мгновенная сборка и повторное
// открытие. Ранний выход по набору действий — договор вынесенных обработчиков.

import { markQuestAppPending, questReports } from './quest-app-state.js'
import { handleQuestGitAction } from './quest-git-views.js'

const ACTIONS = new Set(['control-master-application-v2', 'generate-work-order-report', 'open-work-order-report', 'retry-work-order-stage', 'analyze-stage-failure'])
const APP_CONTROLS = new Set(['start', 'stop', 'open', 'terminal'])

export function handleQuestAppAction({ action, target, ui, vscode, render }) {
  if (handleQuestGitAction({ action, target, ui, vscode, render })) return true
  if (!ACTIONS.has(action)) return false
  // Повтор проваленного этапа в выбранной среде или по предложению Мастера,
  // которое человек разрешил. Ядро перепроверяет и среду, и предложение.
  if (action === 'retry-work-order-stage') {
    const id = String(target.dataset.id || '')
    const questId = String(target.dataset.questId || '')
    if (!id || !questId || ui.masterWorkOrderBusy.has(id)) return true
    ui.masterWorkOrderBusy.add(id)
    vscode.postMessage({ type: 'controlMasterWorkOrderQuestV2', workOrderId: id, questId, action: 'retry', proposalDigest: String(target.dataset.proposalDigest || '') })
    render()
    return true
  }
  if (action === 'analyze-stage-failure') {
    const id = String(target.dataset.id || '')
    if (id) vscode.postMessage({ type: 'analyzeStageFailureWithMaster', workOrderId: id, conversationId: String(ui.masterData?.sessions?.active || '') })
    return true
  }
  if (action === 'control-master-application-v2') {
    const id = String(target.dataset.id || '')
    const questId = String(target.dataset.questId || '')
    const control = String(target.dataset.control || '')
    if (!id || !questId || !APP_CONTROLS.has(control)) return true
    // Открыть и запустить в терминале — действия самой IDE: ядро их не
    // выполняет, и держать карточку занятой незачем.
    if (control === 'open' || control === 'terminal') {
      vscode.postMessage({ type: 'controlMasterApplicationV2', workOrderId: id, questId, action: control })
      return true
    }
    if (ui.masterWorkOrderBusy.has(id)) return true
    ui.masterWorkOrderBusy.add(id)
    markQuestAppPending(questId, control)
    const idempotencyKey = globalThis.crypto?.randomUUID?.() || `application-${Date.now()}-${Math.random().toString(36).slice(2)}`
    vscode.postMessage({ type: 'controlMasterApplicationV2', workOrderId: id, questId, action: control, version: Number(target.dataset.version), digest: String(target.dataset.digest || ''), deliveryReceiptId: String(target.dataset.receiptId || ''), idempotencyKey })
    render()
    return true
  }
  if (action === 'generate-work-order-report') {
    const id = String(target.dataset.id || '')
    const order = (Array.isArray(ui.masterData?.workOrders) ? ui.masterData.workOrders : []).find(item => item.id === id)
    if (!order || questReports.get(id)?.phase === 'working') return true
    const evidence = order.runtime?.evidence || {}
    const safeFacts = {
      goal: order.goal || '', status: order.runtime?.status || '', criteria: order.criteria || [],
      verificationChecks: evidence.verificationChecks || [], changedFiles: evidence.changedFiles || [],
      knownLimitations: evidence.knownLimitations || [], deliveryUrl: order.runtime?.deliveryReceipt?.url || '',
    }
    const prompt = `Собери итоговый отчёт по завершённому плану для владельца проекта. Начни с результата и решения, затем покажи выполненные критерии, проверки, изменения, ограничения и следующие шаги. Не выдумывай факты или ссылки.\n\nФакты, которые я проверю перед отправкой:\n${JSON.stringify(safeFacts, null, 2)}`
    // Отчёт собирается сразу: без правки запроса, выбора формата и окна
    // сохранения. Хост сохраняет HTML в .point/reports и открывает его.
    questReports.set(id, { phase: 'working', path: '', uri: '', error: '' })
    vscode.postMessage({ type: 'generateReport', prompt, quick: true, workOrderId: id })
    render()
    return true
  }
  const uri = String(target.dataset.uri || '')
  if (uri) vscode.postMessage({ type: 'generateReport', openUri: uri })
  return true
}
