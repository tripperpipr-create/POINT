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
