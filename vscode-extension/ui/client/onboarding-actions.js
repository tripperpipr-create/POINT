// Клик-ветки первого запуска: шаги Чертога, мозг Мастера и мастерская агента.
//
// Сами экраны рисуют `hall-onboarding-views.js` и `agent-constructor.js` —
// сюда переехали только обработчики, которых им не хватало. Состояние
// приходит общим мешком `ui`, как в `git-actions.js` и `companion-actions.js`.
import { COMPANION_PRESETS } from './companion-studio-views.js'

const ONBOARDING_ACTIONS = new Set([
  'cursor-login', 'complete-onboarding', 'complete-master-onboarding', 'restart-onboarding', 'onboarding-step',
  'onboarding-orchestrator-preset', 'orchestrator-select-mode', 'probe-orchestrator-connection',
  'open-orchestrator-setup', 'onboarding-companion-preset', 'onboarding-agent-template', 'save-onboarding-companion',
  'generate-report',
  'onboarding-create-agent', 'onboarding-edit-agent', 'onboarding-apply-connection', 'onboarding-use-cursor',
  'onboarding-pick-provider', 'onboarding-agent-cycle', 'open-agent-constructor', 'open-agent-constructor-edit',
  'close-agent-constructor', 'reload-compiled-prompt', 'constructor-step', 'constructor-advance',
  'constructor-scratch', 'constructor-template', 'constructor-tool-preset', 'save-constructor', 'apply-blueprint',
  'activate-agent-draft', 'reject-agent-draft', 'update-blueprint', 'cancel-blueprint-sync', 'confirm-blueprint-sync',
])

export function handleOnboardingClickAction({
  action,
  target,
  root,
  ui,
  vscode,
  persistDraft,
  render,
  ONBOARDING_STEPS,
  ORCHESTRATOR_PRESETS,
  TOOL_PRESETS,
  acceptFirstAgentProposal,
  agentById,
  canEnterOnboardingStep,
  companionConnections,
  companionProviderPresets,
  companionSetupValidation,
  companionSuggestedTemplateId,
  constructorToProfile,
  constructorToProjectAgent,
  currentConstructorForm,
  currentOnboardingOrchestratorValues,
  defaultCompanionProviderPreset,
  firstAgentDraftFromProposal,
  firstAgentProposal,
  hubAgents,
  hubModeAvailable,
  isCompanionOnboardingStep,
  isOrchestratorOnboardingStep,
  isSystemOnboardingStep,
  legacySaveWouldDrop,
  newConstructorDraft,
  onboardingAgentTemplates,
  onboardingCompanionDraft,
  onboardingLockReason,
  onboardingOrchestratorDraft,
  onboardingPrimaryAgent,
  orchestratorSetupValidation,
  patchOnboardingAgent,
  persistOnboardingCompanion,
  persistOnboardingOrchestrator,
  providerCatalog,
  resetCompiledPromptPreview,
  sanitizeOrchestratorDraft,
  writeOnboardingOrchestratorDraft,
}) {
  if (!ONBOARDING_ACTIONS.has(action)) return false
  if (action === 'generate-report') { vscode.postMessage({ type: 'generateReport' }); return true }
  if (action === 'cursor-login') vscode.postMessage({type:'cursorLogin'})
  if (action === 'complete-onboarding') vscode.postMessage({type:'completeOnboarding'})
  if (action === 'complete-master-onboarding') {
    persistOnboardingOrchestrator(false)
    const issue = orchestratorSetupValidation(onboardingOrchestratorDraft())
    if (issue) {
      ui.companionSetupStatus = issue
      persistDraft()
      render()
      return true
    }
    ui.companionSetupStatus = ''
    persistOnboardingOrchestrator(true)
    ui.onboardingDraft = { ...ui.onboardingDraft, orchestratorFinished: true }
    persistDraft()
    vscode.postMessage({type:'completeOnboarding'})
  }
  if (action === 'restart-onboarding') {
    ui.onboardingStep = 'orchestrator-brain'
    ui.onboardingDraft = { ...ui.onboardingDraft, orchestratorFinished: false }
    persistDraft()
    vscode.postMessage({type:'restartOnboarding'})
  }
  if (action === 'onboarding-step') {
    const nextStep = target.dataset.step || 'welcome'
    const currentIndex = Math.max(0, ONBOARDING_STEPS.findIndex(item => item.id === ui.onboardingStep))
    const nextIndex = Math.max(0, ONBOARDING_STEPS.findIndex(item => item.id === nextStep))
    const advancing = nextIndex > currentIndex
    if (ui.onboardingStep === 'companion-brain' && advancing) {
      persistOnboardingCompanion(false)
      const issue = companionSetupValidation('brain', onboardingCompanionDraft())
      if (issue) {
        ui.companionSetupStatus = issue
        persistDraft()
        render()
        return true
      }
      ui.companionSetupStatus = ''
      persistOnboardingCompanion(true)
      ui.onboardingDraft = { ...ui.onboardingDraft, companionFinished: true }
    } else if (isCompanionOnboardingStep(ui.onboardingStep)) {
      persistOnboardingCompanion(advancing)
    }
    if (ui.onboardingStep === 'orchestrator-brain' && advancing) {
      persistOnboardingOrchestrator(false)
      const issue = orchestratorSetupValidation(onboardingOrchestratorDraft())
      if (issue) {
        ui.companionSetupStatus = issue
        persistDraft()
        render()
        return true
      }
      ui.companionSetupStatus = ''
      persistOnboardingOrchestrator(true)
      // Подключение уже сохранено, но первый запуск завершится только после
      // следующего экрана с политикой Мастера.
      ui.onboardingDraft = { ...ui.onboardingDraft, orchestratorFinished: false }
    } else if (isOrchestratorOnboardingStep(ui.onboardingStep)) {
      persistOnboardingOrchestrator(advancing)
    }
    if (ui.onboardingStep === 'first-agent' && advancing && !onboardingPrimaryAgent()) {
      acceptFirstAgentProposal()
    }
    if (nextStep === 'first-agent' && !ui.onboardingDraft.agentTemplateId) {
      ui.onboardingDraft = { ...ui.onboardingDraft, agentTemplateId: companionSuggestedTemplateId() }
    }
    if (!canEnterOnboardingStep(nextStep)) {
      ui.onboardingLockNotice = onboardingLockReason(nextStep) || 'Шаг откроется позже'
      persistDraft()
      render()
      return true
    }
    ui.onboardingLockNotice = ''
    ui.onboardingStep = nextStep
    persistDraft()
    render()
  }
  if (action === 'onboarding-orchestrator-preset') {
    const preset = ORCHESTRATOR_PRESETS.find(item => item.id === target.dataset.preset) || ORCHESTRATOR_PRESETS[0]
    writeOnboardingOrchestratorDraft({ ...onboardingOrchestratorDraft(), preset: preset.id, ...preset.values })
    persistDraft()
    render()
  }
  if (action === 'orchestrator-select-mode') {
    let draft = onboardingOrchestratorDraft()
    const mode = target.dataset.mode === 'model' ? 'model' : 'local'
    if (mode === 'model' && !draft.connectionId) {
      const connection = companionConnections()[0]
      const preset = defaultCompanionProviderPreset()
      draft = sanitizeOrchestratorDraft({ ...draft, mode, connectionMode: connection ? 'existing' : 'new', connectionId: connection?.id || '', connectionName: connection?.displayName || '', providerPreset: connection?.presetId || preset?.id || '', provider: connection?.provider || preset?.kind || '', baseUrl: connection?.baseUrl || preset?.baseUrl || '', model: draft.model || connection?.model || preset?.defaultModel || '' })
    } else {
      draft.mode = mode
    }
    writeOnboardingOrchestratorDraft(draft)
    ui.companionProviderProbe = undefined
    ui.companionSetupStatus = ''
    persistDraft()
    render()
  }
  if (action === 'probe-orchestrator-connection') {
    const draft = currentOnboardingOrchestratorValues()
    writeOnboardingOrchestratorDraft(draft)
    ui.companionProviderProbe = { loading: true, models: [] }
    ui.companionSetupStatus = ''
    persistDraft()
    render()
    vscode.postMessage({ type: 'probeCompanionConnection', connectionId: target.dataset.id || draft.connectionId })
  }
  if (action === 'open-orchestrator-setup') {
	ui.masterDevelopmentBusy = true
	vscode.postMessage({type:'loadMasterDevelopment',projectKey:ui.projectKey})
    ui.state.selectedTab = 'onboarding'
    // Расширение обязано знать о переходе. Без этого его selectedTab оставался
    // прежним, и первое же состояние — от нажатия «перестроить индекс», от
    // проверки связи, от любого фонового опроса — возвращало прежнюю вкладку и
    // закрывало настройку Мастера на середине.
    vscode.postMessage({ type: 'selectTab', tab: 'onboarding' })
    ui.onboardingStep = 'orchestrator-brain'
    persistDraft()
    render()
  }
  if (action === 'onboarding-companion-preset') {
    const presetId = target.dataset.preset || 'balanced'
    const preset = COMPANION_PRESETS.find(item => item.id === presetId) || COMPANION_PRESETS[0]
    ui.onboardingDraft = { ...ui.onboardingDraft, companionPreset: preset.id, ...preset.values }
    persistDraft()
    render()
  }
  if (action === 'onboarding-agent-template') {
    ui.onboardingDraft = { ...ui.onboardingDraft, agentTemplateId: target.dataset.template || '' }
    persistDraft()
    render()
  }
  if (action === 'save-onboarding-companion') {
    persistOnboardingCompanion(true)
  }
  if (action === 'onboarding-create-agent') {
    acceptFirstAgentProposal()
  }
  if (action === 'onboarding-edit-agent') {
    const proposal = firstAgentProposal()
    const name = (root.querySelector('#onboarding-agent-name')?.value.trim() || ui.onboardingDraft.agentName || proposal.name || '').trim()
    ui.onboardingDraft = { ...ui.onboardingDraft, agentName: name, agentTemplateId: proposal.template?.id || ui.onboardingDraft.agentTemplateId }
    ui.constructorDraft = firstAgentDraftFromProposal(proposal)
    ui.constructorStep = 'identity'
    ui.agentConstructorOpen = true
    ui.profileEditorOpen = false
    persistDraft()
    render()
  }
  if (action === 'onboarding-apply-connection') {
    const connection = (ui.state.boot?.connections || []).find(item => item.id === target.dataset.id)
    if (!connection) return true
    const preset = providerCatalog().find(item => item.id === connection.presetId || item.kind === connection.provider)
    ui.onboardingDraft = { ...ui.onboardingDraft, connectionId: connection.id, providerPreset: connection.presetId || preset?.id || '' }
    persistDraft()
    patchOnboardingAgent({
      provider: connection.provider || preset?.kind || '',
      providerPreset: connection.presetId || preset?.id || '',
      baseUrl: connection.baseUrl || preset?.baseUrl || '',
      primaryModel: ui.onboardingDraft.agentModel || preset?.defaultModel || '',
      model: ui.onboardingDraft.agentModel || preset?.defaultModel || '',
    })
    render()
  }
  if (action === 'onboarding-use-cursor') {
    const preset = companionProviderPresets().find(item => item.local) || companionProviderPresets()[0]
    if (!preset) return true
    ui.onboardingDraft = { ...ui.onboardingDraft, providerPreset: preset.id, companionProvider: preset.kind }
    persistDraft()
    patchOnboardingAgent({ provider: preset.kind, providerPreset: preset.id, primaryModel: preset.defaultModel || '', model: preset.defaultModel || '', baseUrl: preset.baseUrl || '' })
    render()
  }
  if (action === 'onboarding-pick-provider') {
    ui.onboardingDraft = { ...ui.onboardingDraft, providerPreset: target.dataset.preset || '', companionProvider: target.dataset.provider || '' }
    persistDraft()
    render()
    const select = root.querySelector('#connection-provider')
    if (select && target.dataset.provider) select.value = target.dataset.provider
  }
  if (action === 'onboarding-agent-cycle') {
    const templates = onboardingAgentTemplates()
    if (!templates.length) return true
    const current = companionSuggestedTemplateId(templates)
    const index = Math.max(0, templates.findIndex(item => item.id === current))
    const next = templates[(index + 1) % templates.length]
    ui.onboardingDraft = { ...ui.onboardingDraft, agentTemplateId: next.id }
    persistDraft()
    render()
  }
  if (action === 'open-agent-constructor' || action === 'open-agent-constructor-edit') {
    // Незавершённый онбординг системных агентов держит порядок и остаётся.
    if (ui.state.selectedTab === 'onboarding' && isSystemOnboardingStep(ui.onboardingStep)) return true
    // А открытая настройка компаньона просто закрывается: панель не должна
    // молча съедать нажатие на «Нанять агента».
    if (ui.companionSetupOpen) ui.companionSetupOpen = false
    ui.agentConstructorOpen = true
    ui.profileEditorOpen = false
    ui.profileDraft = undefined
    ui.createStepError = ''
    ui.constructorStep = 'identity'
    if (action === 'open-agent-constructor-edit') {
      // Кнопка может назвать агента прямо: карточка наряда ведёт в мастерскую
      // того исполнителя, которого утверждение только что создало, а не того,
      // кто случайно выбран в ростере.
      const named = target.dataset.id ? agentById(String(target.dataset.id)) : null
      if (named) ui.selectedProfileId = named.id
      const selected = named || agentById(ui.selectedProfileId) || hubAgents()[0]
      ui.constructorDraft = newConstructorDraft(selected ? {
        ...selected,
        primaryModel: selected.primaryModel || selected.model,
        model: selected.primaryModel || selected.model,
      } : undefined)
    } else {
      ui.constructorDraft = newConstructorDraft()
    }
    persistDraft()
    render()
  }
  if (action === 'close-agent-constructor') {
    ui.agentConstructorOpen = false
    ui.constructorDraft = undefined
    ui.constructorStep = 'identity'
    ui.createStepError = ''
    resetCompiledPromptPreview()
    persistDraft()
    render()
  }
  if (action === 'reload-compiled-prompt') {
    resetCompiledPromptPreview()
    render()
  }
  if (action === 'constructor-step') {
    const draft = currentConstructorForm()
    if (draft) ui.constructorDraft = draft
    ui.createStepError = ''
    ui.constructorStep = target.dataset.step || 'identity'
    persistDraft()
    render()
  }
  if (action === 'constructor-advance') {
    const draft = currentConstructorForm()
    if (draft) ui.constructorDraft = draft
    ui.createStepError = ''
    ui.constructorStep = target.dataset.step || ui.constructorStep
    persistDraft()
    render()
  }
  if (action === 'constructor-scratch') {
    ui.constructorDraft = newConstructorDraft()
    ui.constructorStep = 'identity'
    render()
  }
  if (action === 'constructor-template') {
    const templateId = target.dataset.template || ''
    const blueprint = (ui.state.boot?.blueprints || []).find(item => item.id === templateId)
    const profileTemplate = (ui.state.boot?.profileTemplates || []).find(item => item.id === templateId)
    const source = blueprint || profileTemplate
    if (source) {
      ui.constructorDraft = newConstructorDraft({
        ...source,
        id: '',
        primaryModel: source.primaryModel || source.model,
        model: source.primaryModel || source.model,
        blueprintId: blueprint?.id || '',
      })
      ui.constructorStep = 'identity'
      render()
    }
  }
  if (action === 'constructor-tool-preset') {
    const preset = TOOL_PRESETS.find(item => item.id === target.dataset.preset)
    const values = preset?.tools === null ? (ui.state.boot?.toolCatalog || []).map(item => item.name) : (preset?.tools || [])
    for (const input of root.querySelectorAll('input[name="constructor-tool"]')) input.checked = values.includes(input.value)
    const draft = currentConstructorForm()
    if (draft) ui.constructorDraft = draft
    render()
  }
  if (action === 'save-constructor') {
    const draft = currentConstructorForm()
    if (!draft) return true
    if (!(draft.name || '').trim()) { ui.createStepError = 'Укажите имя агента'; ui.constructorDraft = draft; render(); return true }
    const dropped = legacySaveWouldDrop(draft)
    if (dropped) { ui.createStepError = dropped; ui.constructorDraft = draft; render(); return true }
    ui.constructorDraft = draft
    ui.createStepError = ''
    if (hubModeAvailable()) {
      vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(draft) })
    } else {
      vscode.postMessage({ type: 'saveProfile', profile: constructorToProfile(draft) })
    }
  }
  if (action === 'activate-agent-draft') {
    const draft = currentConstructorForm()
    if (!draft) return true
    if (!(draft.name || '').trim()) { ui.createStepError = 'Укажите имя агента'; ui.constructorDraft = draft; render(); return true }
    ui.constructorDraft = draft
    ui.createStepError = ''
    vscode.postMessage({ type: 'activateProjectAgentDraft', id: draft.id, agent: constructorToProjectAgent(draft) })
  }
  if (action === 'reject-agent-draft') {
    const draft = currentConstructorForm() || ui.constructorDraft
    if (draft?.id) vscode.postMessage({ type: 'rejectProjectAgentDraft', id: draft.id })
  }
  if (action === 'apply-blueprint') {
    const draft = currentConstructorForm()
    if (draft) ui.constructorDraft = draft
    if (draft?.id) {
      ui.blueprintSyncDirection = 'apply-blueprint'
      ui.blueprintSyncPreview = { projectAgentId: draft.id, loading: true }
      render()
      vscode.postMessage({ type: 'previewBlueprintSync', agentId: draft.id, direction: ui.blueprintSyncDirection })
    }
  }
  if (action === 'update-blueprint') {
    const draft = currentConstructorForm()
    if (draft) ui.constructorDraft = draft
    if (draft?.id) {
      ui.blueprintSyncDirection = 'update-blueprint'
      ui.blueprintSyncPreview = { projectAgentId: draft.id, loading: true }
      render()
      vscode.postMessage({ type: 'previewBlueprintSync', agentId: draft.id, direction: ui.blueprintSyncDirection })
    }
  }
  if (action === 'cancel-blueprint-sync') {
    ui.blueprintSyncPreview = undefined
    ui.blueprintSyncDirection = ''
    render()
  }
  if (action === 'confirm-blueprint-sync') {
    const agentId = ui.blueprintSyncPreview?.projectAgentId
    if (!agentId) return true
    const type = ui.blueprintSyncDirection === 'update-blueprint' ? 'updateBlueprintFromAgent' : 'applyBlueprintToAgent'
    vscode.postMessage({ type, agentId })
  }
  return true
}
