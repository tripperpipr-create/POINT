import { list } from './format-units.js'

// Ручной критерий закрывает человек. Без этих кнопок карточка просила
// «выполните ручную проверку» и не давала отметить, что она сделана: квест
// навсегда оставался «с ограничениями» или «заблокирован».
export function manualReviewHtml(order, esc) {
  const runtime = order.runtime
  const manual = list(order.criteria).filter(item => item.kind === 'manual')
  if (!runtime?.questId || !runtime?.evidence?.id || !manual.length) return ''
  const evidence = new Map(list(runtime.evidence.criteria).map(item => [String(item.criterionId), item]))
  const button = (item, decision, label) => `<button type="button" class="hall-chip" data-action="review-master-manual-criterion-v2" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId)}" data-criterion-id="${esc(item.id)}" data-decision="${decision}">${label}</button>`
  return `<section class="master-v2-manual-review" aria-label="Ручная приёмка"><b>Ручная приёмка</b>${manual.map(item => {
    const decided = evidence.get(String(item.id))?.review
    const state = decided === 'accepted' ? '<span class="is-accepted">Принято</span>'
      : decided === 'rejected' ? '<span class="is-rejected">Не принято</span>'
      : button(item, 'accepted', 'Принято') + button(item, 'rejected', 'Не принято')
    return `<div class="master-v2-manual-row"><span>${esc(item.text || item.id)}</span><div>${state}</div></div>`
  }).join('')}</section>`
}


// Решение уходит в ядро один раз: пока наряд занят, повторный клик — уже
// обработанный клик, а не второе решение по тому же критерию.
export function handleManualReviewClick(target, ui, vscode, render) {
  const id = String(target.dataset.id || '')
  const questId = String(target.dataset.questId || '')
  const criterionId = String(target.dataset.criterionId || '')
  const decision = String(target.dataset.decision || '')
  if (!id || !questId || !criterionId || !['accepted', 'rejected'].includes(decision) || ui.masterWorkOrderBusy.has(id)) return true
  ui.masterWorkOrderBusy.add(id)
  vscode.postMessage({ type: 'reviewMasterManualCriterionV2', workOrderId: id, questId, criterionId, decision })
  render()
  return true
}
