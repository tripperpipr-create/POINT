// Локальное состояние карточек новых исполнителей.
//
// Оно переживает перерисовку ленты, но не принадлежит самому представлению:
// inbox-и ответов также снимают busy и сверяют карточки с новым снимком мира.
export const masterAgentDrafts = new Map()
export const masterAgentBusy = new Set()
export const masterAgentErrors = new Map()
export const masterAgentConsent = new Set()

// Без списка это точный ответ операции над карточкой — отпускаем все. Со
// списком пришёл общий снимок мира: убираем busy только у карточек, которых в
// новом состоянии уже нет. Иначе любое фоновое обновление снимет защиту ещё до
// ответа saveProjectAgent/decideCompanionAction и разрешит создать дубль.
export function releaseMasterAgentCards(cards) {
  if (!Array.isArray(cards)) {
    masterAgentBusy.clear()
    return
  }
  const active = new Set(cards.map(card => String(card?.id || '')).filter(Boolean))
  for (const id of masterAgentBusy) if (!active.has(id)) masterAgentBusy.delete(id)
}

// Ввод в поле карточки сразу становится черновиком: перерисовка ленты не
// теряет набранное, а прежняя ошибка сохранения гаснет вместе с правкой.
export function readMasterAgentCardInput(target) {
  const field = target?.dataset?.agentField
  const host = target?.closest?.('[data-agent-card]')
  if (!field || !host) return ''
  const id = String(host.dataset.agentCard || '')
  const patch = { ...(masterAgentDrafts.get(id) || {}) }
  if (field === 'tool') {
    patch.allowedTools = [...host.querySelectorAll('[data-agent-field="tool"]')].filter(item => item.checked).map(item => item.value)
  } else if (field === 'policy') {
    patch.toolPolicies = { ...(patch.toolPolicies || {}) }
    patch.toolPolicies[String(target.dataset.tool || '')] = target.value
  } else if (field === 'maxSteps' || field === 'maxDurationSeconds') {
    patch[field] = Number(target.value) || 0
  } else {
    patch[field] = target.value
  }
  masterAgentDrafts.set(id, patch)
  masterAgentErrors.delete(id)
  const note = host.querySelector('.master-agent-error')
  if (note) {
    note.textContent = ''
    note.classList.add('is-hidden')
  }
  return id
}
