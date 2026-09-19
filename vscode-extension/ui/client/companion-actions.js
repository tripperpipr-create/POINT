// Клик-ветки помощника: лента, вмешательства и мастер настройки.
//
// Состояние помощника живёт в `main.js` как обычные `let`, поэтому сюда оно
// приходит общим мешком `ui` с парами геттер/сеттер — тем же, которым уже
// пользуются `git-actions.js` и `hub-actions.js`.
import { COMPANION_EXAMPLES, COMPANION_SETUP_STEPS } from './companion-compose.js'
import { COMPANION_PRESETS } from './companion-studio-views.js'

const COMPANION_ACTIONS = new Set([
  'stop-companion-chat', 'companion-scroll-latest', 'copy-companion-message', 'copy-companion-code',
  'regenerate-companion-message', 'feedback-companion-message', 'open-companion-message-details',
  'new-companion-thread', 'show-companion-archives', 'clear-companion-pending', 'close-companion-popup',
  'open-companion-popup', 'open-companion-sidebar', 'dismiss-companion-applied', 'companion-continue',
  'companion-retry-last', 'focus-hub', 'clear-companion-history', 'dismiss-companion-intervention',
  'restore-companion-interventions', 'companion-intervention-action', 'companion-quick-prompt', 'companion-prefill',
  'companion-answer', 'open-companion-setup', 'close-companion-setup', 'companion-setup-step', 'companion-setup-move',
  'companion-select-mode', 'probe-companion-connection', 'companion-select-model', 'companion-setup-preset',
  'companion-toggle-auto-act', 'companion-select-example', 'companion-select-scene', 'save-test-companion',
])

export function handleCompanionClickAction({
  action,
  target,
  root,
  ui,
  vscode,
  persistDraft,
  render,
  stopCompanionChat,
  scrollCompanionThread,
  sendCompanionUserMessage,
  focusCompanionInput,
  canonicalTab,
  normalizeCompanionSetupStep,
  companionSetupDraftFromConfig,
  currentCompanionSetupDraft,
  companionSetupValidation,
  companionConfigFromDraft,
  refreshCompanionSetup,
  refreshCompanionLiveSurfaces,
  sanitizeCompanionSetupDraft,
  companionConnections,
  companionNormalizeSceneId,
  companionSceneById,
  defaultCompanionProviderPreset,
  onboardingCompanionActive,
  onboardingCompanionDraft,
  writeOnboardingCompanionDraft,
  onboardingOrchestratorActive,
  writeOnboardingOrchestratorDraft,
  currentOnboardingOrchestratorValues,
}) {
  if (!COMPANION_ACTIONS.has(action)) return false
  if (action === 'stop-companion-chat') stopCompanionChat()
  if (action === 'companion-scroll-latest') scrollCompanionThread(true)
  if (action === 'copy-companion-message' || action === 'copy-companion-code') {
    const source = action === 'copy-companion-code'
      ? target.closest?.('.companion-code-wrap')?.querySelector?.('code')
      : target.closest?.('.companion-msg')?.querySelector?.('.companion-msg-body, p')
    const text = String(source?.innerText || source?.textContent || '').trim()
    if (text) {
      vscode.postMessage({ type: 'copyCompanionText', text })
      const previous = target.textContent
      target.textContent = 'Скопировано ✓'
      setTimeout(() => { if (target.isConnected) target.textContent = previous }, 1400)
    }
  }
  if (action === 'regenerate-companion-message') {
    const index = Number(target.dataset.messageIndex || -1)
    const previous = ui.companionMessages.slice(0, index).reverse().find(item => item?.role === 'user')
    if (previous?.content && !ui.companionLoading) sendCompanionUserMessage(previous.content, { retry: true })
  }
  if (action === 'feedback-companion-message') {
    const index = Number(target.dataset.messageIndex || -1)
    const item = ui.companionMessages[index]
    vscode.postMessage({
      type: 'companionFeedback',
      messageId: target.dataset.messageId || item?.id || '',
      value: target.dataset.value === 'down' ? 'down' : 'up',
      content: String(item?.content || '').slice(0, 400),
    })
    // Отметка сразу попадает в состояние: ответ расширения придёт позже, а
    // перерисовка может случиться раньше него.
    const markedId = String(target.dataset.messageId || item?.id || '')
    if (markedId) ui.companionFeedbackMarks.set(markedId, target.dataset.value === 'down' ? 'down' : 'up')
    render()
  }
  if (action === 'open-companion-message-details') {
    const index = Number(target.dataset.messageIndex || -1)
    const item = ui.companionMessages[index]
    const request = ui.companionMessages.slice(0, index).reverse().find(entry => entry?.role === 'user')
    if (item) vscode.postMessage({ type: 'openCompanionMessageDetails', item, request: request?.content || '' })
  }
  if (action === 'new-companion-thread') {
    if (ui.companionLoading) stopCompanionChat()
    vscode.postMessage({ type: 'newCompanionThread', messages: ui.companionMessages })
  }
  if (action === 'show-companion-archives') vscode.postMessage({ type: 'showCompanionArchives' })
  if (action === 'clear-companion-pending') {
    ui.companionPendingSend = ''
    ui.companionDraft = ''
    persistDraft()
    render()
    focusCompanionInput()
  }
  if (action === 'close-companion-popup') vscode.postMessage({ type: 'closeCompanionPopup' })
  if (action === 'open-companion-popup') vscode.postMessage({ type: 'openCompanionPopup' })
  if (action === 'open-companion-sidebar') {
    vscode.postMessage({
      type: 'companionThreadUpdate',
      messages: ui.companionMessages,
      draft: ui.companionDraft,
      streamReply: ui.companionStreamReply,
      loading: ui.companionLoading,
      pendingSend: ui.companionPendingSend,
    })
    vscode.postMessage({ type: 'openCompanionSidebar' })
  }
  if (action === 'dismiss-companion-applied') {
    ui.companionAppliedNotice = undefined
    render()
  }
  if (action === 'companion-continue') {
    sendCompanionUserMessage('Продолжи прошлый ответ с места обрыва, не повторяя написанное.')
  }
  if (action === 'companion-retry-last') {
    const lastUser = [...ui.companionMessages].reverse().find(item => item.role === 'user')
    if (lastUser?.content) sendCompanionUserMessage(lastUser.content)
  }
  if (action === 'focus-hub') vscode.postMessage({ type: 'focusHub', tab: target.dataset.tab || 'overview' })
  if (action === 'clear-companion-history') vscode.postMessage({ type: 'clearCompanionHistory' })
  if (action === 'dismiss-companion-intervention') vscode.postMessage({ type: 'dismissCompanionIntervention', id: target.dataset.id || '', occurrenceKey: target.dataset.occurrence || '' })
  if (action === 'restore-companion-interventions') vscode.postMessage({ type: 'restoreCompanionInterventions' })
  if (action === 'companion-intervention-action') {
    const relatedId = target.dataset.relatedId || ''
    const actionKind = target.dataset.kind || ''
    if (actionKind === 'probe_connection' && relatedId) {
      ui.companionInterventionProbe = {
        interventionId: target.dataset.interventionId || '',
        connectionId: relatedId,
        loading: true,
      }
      render()
      vscode.postMessage({ type: 'probeCompanionConnection', connectionId: relatedId })
      return true
    }
    if (actionKind === 'companion_prompt' && target.dataset.message && !ui.companionLoading) {
      ui.companionDraft = target.dataset.message
      persistDraft()
      vscode.postMessage({ type: 'openCompanionPopup', message: target.dataset.message, send: false })
      render()
      focusCompanionInput()
      return true
    }
    const execution = (ui.state.boot?.executions || []).find(item => item.id === relatedId)
    const runId = execution?.runId || ''
    if (actionKind === 'message_run' && runId && target.dataset.message) {
      vscode.postMessage({ type: 'messageRun', runId, message: target.dataset.message })
    } else if (runId) {
      vscode.postMessage({ type: 'loadRun', id: runId })
    } else if (target.dataset.tab) {
      vscode.postMessage({ type: 'selectTab', tab: canonicalTab(target.dataset.tab) })
    }
  }
  if (action === 'companion-quick-prompt') {
    sendCompanionUserMessage(target.dataset.prompt || '')
  }
  if (action === 'companion-prefill') {
    ui.companionDraft = target.dataset.prompt || ''
    persistDraft()
    render()
    focusCompanionInput()
  }
  if (action === 'companion-answer') {
    sendCompanionUserMessage(target.dataset.prompt || '')
  }
  if (action === 'open-companion-setup') {
    ui.companionSetupOpen = true
    ui.companionSetupStep = normalizeCompanionSetupStep(target.dataset.step || 'brain')
    ui.companionSetupDraft = companionSetupDraftFromConfig()
    ui.companionProviderProbe = undefined
    ui.companionSetupStatus = ''
    ui.companionSetupTestResult = undefined
    ui.agentConstructorOpen = false
    persistDraft()
    render()
  }
  if (action === 'close-companion-setup') {
    ui.companionSetupDraft = currentCompanionSetupDraft()
    ui.companionSetupOpen = false
    ui.companionSetupStatus = ''
    ui.companionProviderProbe = undefined
    persistDraft()
    render()
  }
  if (action === 'companion-setup-step') {
    ui.companionSetupDraft = currentCompanionSetupDraft()
    ui.companionSetupStep = normalizeCompanionSetupStep(target.dataset.step || 'role')
    ui.companionSetupStatus = ''
    ui.companionSetupQuiet = false
    persistDraft()
    refreshCompanionSetup({ quiet: true })
  }
  if (action === 'companion-setup-move') {
    ui.companionSetupDraft = currentCompanionSetupDraft()
    const currentIndex = Math.max(0, COMPANION_SETUP_STEPS.findIndex(item => item.id === ui.companionSetupStep))
    const direction = Number(target.dataset.direction || 1)
    const issue = direction > 0 ? companionSetupValidation(ui.companionSetupStep, ui.companionSetupDraft) : ''
    if (issue) { ui.companionSetupStatus = issue; refreshCompanionSetup({ quiet: true }); return true }
    ui.companionSetupStatus = ''
    ui.companionSetupStep = COMPANION_SETUP_STEPS[Math.max(0, Math.min(COMPANION_SETUP_STEPS.length - 1, currentIndex + direction))].id
    persistDraft()
    refreshCompanionSetup({ quiet: false })
  }
  if (action === 'companion-select-mode') {
    const mode = 'model'
    let draft = onboardingCompanionActive() ? onboardingCompanionDraft() : currentCompanionSetupDraft()
    if (!draft.connectionId) {
      const connection = companionConnections()[0]
      const preset = defaultCompanionProviderPreset()
      draft = sanitizeCompanionSetupDraft({ ...draft, mode, connectionMode: connection ? 'existing' : 'new', connectionId: connection?.id || '', connectionName: connection?.displayName || '', providerPreset: connection?.presetId || preset?.id || '', provider: connection?.provider || preset?.kind || '', baseUrl: connection?.baseUrl || preset?.baseUrl || '', model: draft.model || preset?.defaultModel || '' })
    } else {
      draft.mode = mode
    }
    if (onboardingCompanionActive()) writeOnboardingCompanionDraft(draft)
    else ui.companionSetupDraft = draft
    ui.companionSetupStatus = ''
    persistDraft()
    refreshCompanionSetup({ quiet: true })
  }
  if (action === 'probe-companion-connection') {
    const draft = onboardingCompanionActive() ? onboardingCompanionDraft() : currentCompanionSetupDraft()
    if (!onboardingCompanionActive()) ui.companionSetupDraft = draft
    ui.companionProviderProbe = { loading: true, models: [] }
    ui.companionSetupStatus = ''
    refreshCompanionSetup({ quiet: true })
    vscode.postMessage({ type: 'probeCompanionConnection', connectionId: target.dataset.id || draft.connectionId })
  }
  if (action === 'companion-select-model') {
    if (onboardingOrchestratorActive()) {
      writeOnboardingOrchestratorDraft({ ...currentOnboardingOrchestratorValues(), model: target.dataset.model || '' })
      persistDraft()
      render()
      return true
    }
    const draft = onboardingCompanionActive() ? onboardingCompanionDraft() : currentCompanionSetupDraft()
    draft.model = target.dataset.model || ''
    if (onboardingCompanionActive()) writeOnboardingCompanionDraft(draft)
    else ui.companionSetupDraft = draft
    persistDraft()
    refreshCompanionSetup({ quiet: true })
  }
  if (action === 'companion-setup-preset') {
    const preset = COMPANION_PRESETS.find(item => item.id === target.dataset.preset)
    if (preset) {
      ui.companionSetupDraft = sanitizeCompanionSetupDraft({ ...currentCompanionSetupDraft(), preset: preset.id, ...preset.values })
      ui.companionSetupStatus = ''
      refreshCompanionSetup({ liveOnly: true })
    }
  }
  if (action === 'companion-toggle-auto-act') {
    ui.companionSetupDraft = sanitizeCompanionSetupDraft({ ...currentCompanionSetupDraft(), autoAct: target.dataset.autoAct === 'true' })
    ui.companionSetupStatus = ''
    refreshCompanionSetup({ liveOnly: true })
  }
  if (action === 'companion-select-example') {
    ui.companionSetupDraft = currentCompanionSetupDraft()
    ui.companionSetupDraft.examplePrompt = target.dataset.prompt || COMPANION_EXAMPLES[0].prompt
    ui.companionSetupDraft.sampleScene = companionNormalizeSceneId(ui.companionSetupDraft.examplePrompt)
    ui.companionSetupTestResult = undefined
    persistDraft()
    refreshCompanionSetup({ quiet: true })
  }
  if (action === 'companion-select-scene') {
    const scene = companionSceneById(target.dataset.scene)
    if (root.querySelector('.companion-onboarding') && !ui.companionSetupOpen) {
      ui.onboardingDraft = { ...ui.onboardingDraft, sampleScene: scene.id }
      persistDraft()
      refreshCompanionLiveSurfaces(onboardingCompanionDraft(), root.querySelector('.companion-onboarding') || root)
      return true
    }
    ui.companionSetupDraft = sanitizeCompanionSetupDraft({ ...currentCompanionSetupDraft(), sampleScene: scene.id, examplePrompt: scene.prompt })
    refreshCompanionSetup({ liveOnly: true })
  }
  if (action === 'save-test-companion') {
    ui.companionSetupDraft = currentCompanionSetupDraft()
    const issue = companionSetupValidation('brain', ui.companionSetupDraft) || companionSetupValidation('boundaries', ui.companionSetupDraft) || companionSetupValidation('skills', ui.companionSetupDraft)
    if (issue) { ui.companionSetupStatus = issue; render(); return true }
    ui.companionLoading = true
    ui.companionSetupStatus = ''
    ui.companionSetupTestResult = undefined
    persistDraft()
    render()
    vscode.postMessage({ type: 'saveCompanionConfigAndChat', config: companionConfigFromDraft(ui.companionSetupDraft), message: ui.companionSetupDraft.examplePrompt })
  }
  return true
}
