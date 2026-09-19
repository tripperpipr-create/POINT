// Клик-ветки прогонов, квестов, изменений и очереди решений.
//
// Одна тема, растянутая по всему обработчику: запустить, приостановить,
// откатить, принять Change Set, ответить на решение. Сорок веток, и почти
// каждая — доставка намерения в ядро.
//
// Решения сюда входят намеренно: очередь целиком про прогоны, а её пункт несёт
// собственный путь разрешения, который вебвью не строит сам, а берёт у ядра
// (`GET /api/decisions`). Ветка передаёт этот путь дальше и ничего о нём не
// решает — проверку по закрытому списку делает `extension.js`.

export function handleRunClickAction({
  action, target, ui, persistDraft, render, vscode,
  decisionIntents, questPayload, requestContextPreview, sendDecisionResolve, toggleOpenQuest,
}) {
  if (action === 'keep-run-all') {
    const runId = String(target.dataset.runId || ui.state.details?.run?.id || '')
    if (runId) ui.keptRunId = runId
    persistDraft()
    render()
    return
    return true
  }
  if (action === 'undo-run-all') {
    const runId = String(target.dataset.runId || ui.state.details?.run?.id || '')
    if (runId) vscode.postMessage({ type: 'undoRunPatches', runId })
    return
    return true
  }
  if (action === 'undo-run-file') {
    const runId = String(target.dataset.runId || ui.state.details?.run?.id || '')
    const patchIds = String(target.dataset.patchIds || '').split(',').map(item => item.trim()).filter(Boolean)
    if (runId) vscode.postMessage({ type: 'undoRunPatches', runId, patchIds })
    return
    return true
  }
  if (action === 'repeat-quest') {
    // Повтор не запускает прогон молча: задача переносится в брифинг, модель и
    // персонажа человек выбирает сам. Иначе кнопка тратила бы бюджет вслепую.
    ui.taskDraft = target.dataset.task || ''
    ui.state.selectedTab = 'quests'
    vscode.postMessage({ type: 'selectTab', tab: 'quests' })
    persistDraft()
    render()
    return true
  }
  if (action === 'pick-file-history') {
    ui.fileHistoryPath = target.dataset.path || ''
    ui.fileHistoryStatus = 'loading'
    ui.fileHistoryData = undefined
    vscode.postMessage({ type: 'loadFileHistory', path: ui.fileHistoryPath })
    render()
    return true
  }
  if (action === 'revert-file-entry') {
    const source = target.dataset.path || ''
    const id = (target.dataset.id || '').split('/')[0]
    if (source.startsWith('/api/patches/')) vscode.postMessage({ type: 'revertPatch', id })
    else if (source.startsWith('/api/change-sets/')) vscode.postMessage({ type: 'revertChangeSet', id })
    return true
  }
  if (action === 'toggle-quest') {
    const id = String(target.dataset.id || '')
    toggleOpenQuest(id)
    render()
    return true
  }
  if (action === 'submit-quest-replan') {
    const questId = String(target.dataset.questId || '')
    const panel = target.closest('.quest-midflight')
    if (!questId || !panel) return
    const nodeId = String(panel.querySelector('[name="replanNodeId"]')?.value || '').trim()
    const instruction = String(panel.querySelector('[name="replanInstruction"]')?.value || '').trim()
    const reason = String(panel.querySelector('[name="replanReason"]')?.value || '').trim()
    const criterionIds = [...panel.querySelectorAll('input[name="replanCriterion"]:checked')].map(el => el.value)
    if (!nodeId || !reason) {
      ui.transientError = 'Для replan нужны этап и причина'
      render()
      return
    }
    vscode.postMessage({
      type: 'replanQuest',
      questId,
      reason,
      criterionIds,
      stages: [{ nodeId, instruction }],
    })
    return true
  }
  if (action === 'submit-quest-revise') {
    const questId = String(target.dataset.questId || '')
    const panel = target.closest('.quest-midflight')
    if (!questId || !panel || target.disabled) return
    const expectedVersion = Number(target.dataset.expectedVersion || 0)
    const goal = String(panel.querySelector('[name="reviseGoal"]')?.value || '').trim()
    if (!goal) {
      ui.transientError = 'Новая цель пуста'
      render()
      return
    }
    const quest = (ui.state.boot?.quests || []).find(item => item.id === questId)
    const brief = { ...(quest?.brief || {}), goal }
    vscode.postMessage({
      type: 'reviseQuestBrief',
      questId,
      brief,
      expectedVersion,
      approveVersion: expectedVersion + 1,
    })
    return true
  }
  if (action === 'revert-execution') {
    vscode.postMessage({ type: 'revertExecution', id: target.dataset.id })
    return true
  }
  if (action === 'revert-quest') {
    vscode.postMessage({ type: 'revertQuest', id: target.dataset.id })
    return true
  }
  if (action === 'delete-quest') {
    vscode.postMessage({ type: 'deleteQuest', id: target.dataset.id })
    return true
  }
  if (action === 'delete-team') {
    vscode.postMessage({ type: 'deleteTeam', id: target.dataset.id })
    return true
  }
  if (action === 'launch-execution') {
    vscode.postMessage({ type: 'launchExecution', id: target.dataset.id, apiKey: ui.apiKey })
    return true
  }
  if (action === 'cancel-cursor-execution') {
    vscode.postMessage({ type: 'cancelCursorExecution', id: target.dataset.id })
    return true
  }
  if (action === 'cancel-cursor') {
    vscode.postMessage({type:'cancelCursorRun'})
    return true
  }
  if (action === 'revert-patch') {
    vscode.postMessage({type:'revertPatch',id:target.dataset.id})
    return true
  }
  if (action === 'apply-changeset') {
    vscode.postMessage({ type: 'applyChangeSet', id: target.dataset.id })
    return true
  }
  if (action === 'apply-changeset-chain') {
    vscode.postMessage({ type: 'applyChangeSetChain', id: target.dataset.id })
    return true
  }
  if (action === 'reject-changeset') {
    vscode.postMessage({ type: 'rejectChangeSet', id: target.dataset.id })
    return true
  }
  if (action === 'revert-changeset') {
    vscode.postMessage({ type: 'revertChangeSet', id: target.dataset.id })
    return true
  }
  if (action === 'resolve-changeset') {
    const content = target.closest('.conflict-row')?.querySelector('.conflict-manual-content')?.value ?? ''
    vscode.postMessage({
      type: 'resolveChangeSet',
      id: target.dataset.id,
      strategy: target.dataset.strategy,
      path: target.dataset.path,
      content: target.dataset.strategy === 'manual' ? content : undefined,
    })
    return true
  }
  if (action === 'load-run') {
    vscode.postMessage({type:'loadRun',id:target.dataset.id})
    return true
  }
  if (action === 'cancel') {
    vscode.postMessage({type:'cancelRun',runId:target.dataset.id})
    return true
  }
  if (action === 'pause-run') {
    vscode.postMessage({ type: 'pauseRun', runId: target.dataset.runId })
    return true
  }
  if (action === 'resume-run') {
    vscode.postMessage({ type: 'resumeRun', runId: target.dataset.runId, apiKey: ui.apiKey })
    return true
  }
  if (action === 'extend-active-time') {
    vscode.postMessage({ type: 'extendActiveTime', runId: target.dataset.runId, apiKey: ui.apiKey })
    return true
  }
  if (action === 'preview-run') { const quest=questPayload();if(quest.task){ui.agentRunPreview=undefined;ui.agentRunPreviewError='';ui.agentRunPreviewStatus='loading';render();vscode.postMessage({type:'previewRun',profileId:ui.selectedProfileId,...quest,contextItems: ui.contextItems})}; return true }
  if (action === 'load-context-inspector') {
    ui.contextInspectorRunId = target.dataset.runId || ''
    ui.contextInspectorStatus = 'loading'
    ui.contextInspector = undefined
    ui.contextInspectorNotice = ''
    render()
    vscode.postMessage({ type: 'loadContextInspector', runId: ui.contextInspectorRunId })
    return true
  }
  if (action === 'close-context-inspector') {
    ui.contextInspectorRunId = ''
    ui.contextInspectorStatus = 'idle'
    ui.contextInspector = undefined
    ui.contextInspectorNotice = ''
    render()
    return true
  }
  if (action === 'context-amend') {
    vscode.postMessage({
      type: 'contextAmend',
      runId: target.dataset.runId,
      action: target.dataset.amend,
      itemId: target.dataset.itemId,
    })
    return true
  }
  if (action === 'add-run-context-files') {
    ui.contextInspectorNotice = ''
    vscode.postMessage({ type: 'addRunContextFiles', runId: target.dataset.runId })
    return true
  }
  if (action === 'add-run-context-selection') {
    ui.contextInspectorNotice = ''
    vscode.postMessage({ type: 'addRunContextSelection', runId: target.dataset.runId })
    return true
  }
  if (action === 'remove-context') { ui.contextItems.splice(Number(target.dataset.index),1); persistDraft(); requestContextPreview(); return true }
  if (action === 'attach-files') {
    vscode.postMessage({type:'attachFiles'})
    return true
  }
  if (action === 'attach-selection') {
    vscode.postMessage({type:'attachSelection'})
    return true
  }
  if (action === 'pick-decision') { ui.decisionPick = target.dataset.id || ''; render(); return true }
  if (action === 'retry-decisions') {
    ui.decisionsStatus = 'idle'
    ui.decisionsError = ''
    render()
    return true
  }
  if (action === 'resolve-decision') {
    // Что и куда слать, считается из самого решения, а не переносится через
    // разметку: атрибут умеет только строку, а часть маршрутов ждёт булево —
    // «true» строкой для них не согласие, а чужой тип. Кнопка и горячая
    // клавиша теперь считают одинаково, потому что считают одним кодом.
    const id = target.dataset.id || ''
    const item = (ui.decisionsData?.items || []).find(entry => entry.id === id)
    if (item) sendDecisionResolve(id, decisionIntents(item)[target.dataset.intent === 'reject' ? 'reject' : 'accept'])
    return true
  }
  if (action === 'resolve') {
    vscode.postMessage({type:'resolveApproval',id:target.dataset.id,allow:target.dataset.allow==='true'})
    return true
  }
  return false
}
