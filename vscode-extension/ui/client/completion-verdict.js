// Evidence takes precedence over legacy optimistic completion events.
export function completionStatus(payload = {}) {
  const evidence = payload.evidence || payload.completionEvidence || {}
  const criteria = Array.isArray(evidence.criteria) ? evidence.criteria : (payload.verification?.criteria || [])
  if (payload.status === 'preparation_failed') return 'preparation_failed'
  if (evidence.status === 'blocked' || criteria.some(item => ['failed', 'stale'].includes(item.status)) || (payload.verification?.ran && payload.verification.passed === false)) return 'rejected'
  if (evidence.status === 'needs_review' || payload.verification?.needsReview || criteria.some(item => ['needs_review', 'unavailable'].includes(item.status))) return 'needs_review'
  if (payload.acceptancePassed === false && ['accepted', 'accepted_after_revision'].includes(payload.status)) return 'implementation_ready'
  return payload.status || payload.completionStatus || ''
}

export function pendingAcceptanceText(payload = {}) {
  const criteria = payload.evidence?.criteria || payload.completionEvidence?.criteria || payload.verification?.criteria || []
  const names = criteria.filter(item => ['needs_review', 'unavailable'].includes(item.status)).map(item => item.text || item.criterionId)
  const ids = payload.pendingCriterionIds || payload.verification?.pendingCriterionIds || []
  const pending = names.length ? names : ids
  return `Машинные проверки завершены; требуется ручная приёмка.${pending.length ? ` Проверьте: ${pending.join('; ')}.` : ' Откройте критерии квеста и подтвердите результат.'}`
}

export function verificationReuseText(payload = {}) {
  const note = payload.verificationService
  if (!note) return ''
  if (note.reusedFrom) return `Проверки переиспользованы: ${note.reusedFrom}`
  if (note.wouldReuse) return `Shadow: проверки выполнены повторно; ${note.agrees === true ? 'результаты совпали' : note.agrees === false ? 'результаты разошлись' : 'сравнение не завершено'}; источник ${note.wouldReuse}`
  return note.mode ? `Проверки: ${note.mode}, свежий прогон` : ''
}
