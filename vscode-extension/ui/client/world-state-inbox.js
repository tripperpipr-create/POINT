// Пришёл полный снимок мира.
//
// Единственная ветка разбора входящих, которая не досылает кусок, а заменяет
// состояние целиком, и потому единственная, где нужно решить, что из прежнего
// пережило смену: черновики правок, запертые кнопки, открытая мастерская,
// ответ «Мастер не настроен», очередь решений и лента помощника.
//
// Каждое из этих решений однажды было ошибкой, и рядом стоит её описание.
// Общее у них одно: снимок приходит и по чужим поводам, поэтому «пришло
// состояние» само по себе ничего не отпускает — судят по самой сущности.
//
// Состояние приходит общим мешком `ui`, как в `companion-transport.js`.

export function createWorldStateInbox({
  ui,
  vscode,
  render,
  persistDraft,
  canonicalTab,
  resetProjectScopedState,
  decisionsQueueIsStale,
  releaseMasterAgentCards,
  currentMasterAgentCards,
  mergeCompanionTranscript,
  prepareAgentConstructor,
  agentById,
  hubAgents,
  isWide,
  proposalStarting,
  proposalModifying,
  proposalEditDrafts,
  companionActionApplying,
  companionActionModifying,
  companionActionEditDrafts,
}) {
  return function applyWorldStateMessage(message) {
    if (message.type !== 'state') return false
      if (message.type === 'state') {
        if (String(message.workspacePath || '') !== ui.projectKey) {
          ui.projectKey = String(message.workspacePath || '')
          resetProjectScopedState()
	      ui.masterDevelopment = undefined
	      ui.masterDevelopmentBusy = false
	      ui.masterDevelopmentError = ''
        }
        ui.state={...message, selectedTab: canonicalTab(message.selectedTab)}
        const discussed = (ui.state.boot?.questProposals || []).find(p => p.id === ui.masterDiscussionProposalId)
        if (discussed && discussed.status !== 'pending') {
          ui.masterDiscussionProposalId = ''
          persistDraft()
        }
        // «Мастер не настроен» — ответ, который устаревает молча.
        //
        // Настраивают его в другом разделе, и раздел разговора об этом не узнавал:
        // он держал прежний ответ ядра и продолжал показывать приглашение к
        // настройке — то самое, из которого человек только что вернулся, всё
        // сделав. Выход был один: переоткрыть панель. Мир уже сообщил, что
        // диспетчер есть, — значит наш ответ неверен, и его надо спросить заново.
        if (ui.masterData?.configured === false && ui.state.boot?.orchestrator?.id) {
          ui.masterData = undefined
          ui.masterStatus = 'idle'
        }
        // Очередь загружалась один раз и больше не обновлялась. Правило сверки
        // живёт рядом со счётчиком ожидающих: оно ловит сдвиг мира в обе стороны.
        if (ui.decisionsStatus === 'ready' && decisionsQueueIsStale()) ui.decisionsStatus = 'idle'
        // Запуск предложения дошёл до ядра — кнопку отпускаем. Судим по самому
        // предложению, а не по факту прихода состояния: состояние приходит и по
        // чужим поводам, и отпущенная на них кнопка снова стала бы двойной.
        for (const id of proposalStarting) {
          const awaited = (ui.state.boot?.questProposals || []).find(item => item.id === id)
          if (!awaited || awaited.status === 'started') proposalStarting.delete(id)
        }
        for (const id of companionActionApplying) {
          const awaited = (ui.state.boot?.companionActionProposals || []).find(item => item.id === id)
          if (!awaited || awaited.status === 'applied' || awaited.status === 'ignored') companionActionApplying.delete(id)
        }
        // Общий state приходит и по чужим фоновым поводам. Оставляем занятыми
        // карточки, которые всё ещё существуют; обработанная исчезает из
        // списка предложений и освобождается здесь.
        releaseMasterAgentCards(currentMasterAgentCards())
        for (const id of proposalEditDrafts.keys()) {
          const awaited = (ui.state.boot?.questProposals || []).find(item => item.id === id)
          if (!awaited || awaited.status === 'started' || awaited.status === 'ignored') {
            proposalEditDrafts.delete(id)
            proposalModifying.delete(id)
            if (ui.proposalEditId === id) ui.proposalEditId = ''
          }
        }
        for (const id of companionActionEditDrafts.keys()) {
          const awaited = (ui.state.boot?.companionActionProposals || []).find(item => item.id === id)
          if (!awaited || awaited.status === 'applied' || awaited.status === 'ignored') {
            companionActionEditDrafts.delete(id)
            companionActionModifying.delete(id)
            if (ui.companionActionEditId === id) ui.companionActionEditId = ''
          }
        }
        if (message.ideContext && typeof message.ideContext === 'object') ui.companionIdeContext = message.ideContext
        if (Array.isArray(message.companionFeedback)) {
          ui.companionFeedbackMarks = new Map(message.companionFeedback
            .filter(item => item && item.messageId)
            .map(item => [String(item.messageId), item.value === 'down' ? 'down' : 'up']))
        }
        if (Array.isArray(ui.state.boot?.companionMessages)) {
          const incoming = ui.state.boot.companionMessages.map(item => ({ ...item, factsUsed: Array.isArray(item.factsUsed) ? item.factsUsed : [] })).slice(-80)
          ui.companionMessages = mergeCompanionTranscript(ui.companionMessages, incoming, ui.companionLoading)
        }
        const defaultId = ui.state.boot?.defaultProfileId
        // Project agents and legacy profiles share the composer. Checking only the
        // legacy profile array made every background boot refresh discard a valid
        // project-agent selection while a preview was being prepared.
        if (!ui.selectedProfileId || !agentById(ui.selectedProfileId)) {
          ui.selectedProfileId = agentById(defaultId)?.id || hubAgents()[0]?.id || ui.state.boot?.profiles?.[0]?.id || ''
        }
        const improvementFocus = message.agentImprovementFocus
        if (isWide && ui.state.selectedTab === 'agents' && improvementFocus?.requestId && improvementFocus.requestId !== ui.lastAgentImprovementFocusId) {
          const selected = agentById(improvementFocus.agentId)
          if (selected) {
            ui.lastAgentImprovementFocusId = improvementFocus.requestId
            ui.selectedProfileId = selected.id
            prepareAgentConstructor(selected, improvementFocus.constructorStep)
            vscode.postMessage({ type: 'agentImprovementFocused', requestId: improvementFocus.requestId })
          } else if (ui.state.boot) {
            ui.lastAgentImprovementFocusId = improvementFocus.requestId
            ui.transientError = 'Агент из рекомендации больше не найден в ростере.'
            vscode.postMessage({ type: 'agentImprovementFocused', requestId: improvementFocus.requestId })
          }
        }
        persistDraft()
        render()
      }
    return true
  }
}
