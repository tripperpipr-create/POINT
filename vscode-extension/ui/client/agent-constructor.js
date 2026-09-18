// Agent constructor: draft ↔ profile/blueprint/project-agent transforms,
// skill/tool policy helpers, and the step wizard chrome that reads the form DOM.

export const CONSTRUCTOR_STEPS = [
  { id: 'identity', label: 'Личность', hint: 'Имя и тон', why: 'Как зовут агента и какой у него тон общения' },
  { id: 'role', label: 'Роль', hint: 'Экспертиза', why: 'В чём агент эксперт и какой результат от него ожидается' },
  { id: 'mission', label: 'Миссия', hint: 'Цели', why: 'Постоянные цели, которые агент преследует в каждом квесте' },
  { id: 'rules', label: 'Правила', hint: 'Ограничения', why: 'Жёсткие правила, которые нельзя нарушать' },
  { id: 'brain', label: 'Мозг', hint: 'Модель', why: 'Провайдер, модель и параметры рассуждения' },
  { id: 'skills', label: 'Навыки', hint: 'Модули каталога', why: 'Подключаемые модули инструкций из каталога' },
  { id: 'tools', label: 'Инструменты', hint: 'Умения и права', why: 'Какие умения агент может вызывать в квесте' },
  { id: 'memory', label: 'Память', hint: 'Правила проекта', why: 'Проектные инструкции и ограничения workspace' },
  { id: 'permissions', label: 'Разрешения', hint: 'Политики', why: 'Подтверждения, лимиты ходов и политики инструментов' },
  { id: 'review', label: 'Обзор', hint: 'Проверка', why: 'Проверьте собранный промпт перед сохранением' },
]

const LEGACY_DROPPED_FIELDS = [
  ['personality', 'характер'],
  ['mission', 'миссия'],
  ['constraints', 'ограничения'],
  ['skillIds', 'навыки'],
  ['projectRules', 'проектные правила'],
]

const TOOL_POLICY_STRICTNESS = { DENY: 3, ASK: 2, ALLOW: 1 }

export function newConstructorDraft(source) {
  const base = source || {}
  return {
    id: base.id || '',
    blueprintId: base.blueprintId || '',
    name: base.name || '',
    personality: base.personality || '',
    roleDescription: base.roleDescription || '',
    mission: base.mission || '',
    systemPrompt: base.systemPrompt || '',
    goals: [...(base.goals || [])],
    rules: [...(base.rules || [])],
    constraints: [...(base.constraints || [])],
    projectRules: [...(base.projectRules || [])],
    skillIds: [...(base.skillIds || [])],
    allowedTools: [...(base.allowedTools !== undefined ? base.allowedTools : ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff'])],
    toolPolicies: { ...(base.toolPolicies || {}) },
    connectionId: base.connectionId || '',
    provider: base.provider || '',
    providerPreset: base.providerPreset || '',
    baseUrl: base.baseUrl || '',
    primaryModel: base.primaryModel || base.model || '',
    model: base.primaryModel || base.model || '',
    fallbackModels: [...(base.fallbackModels || [])],
    temperature: Number(base.temperature ?? 0.2),
    maxOutputTokens: Number(base.maxOutputTokens || 4096),
    contextWindowTokens: Number(base.contextWindowTokens || 32768),
    reasoningEffort: base.reasoningEffort || 'medium',
    maxSteps: Number(base.maxSteps || 30),
    maxDurationSeconds: Number(base.maxDurationSeconds || 600),
    approvalMode: base.approvalMode || 'safe',
    ...(typeof base.experience === 'number' ? { experience: base.experience } : {}),
    ...(typeof base.level === 'number' ? { level: base.level } : {}),
    ...(typeof base.tasksCompleted === 'number' ? { tasksCompleted: base.tasksCompleted } : {}),
    ...(typeof base.successCount === 'number' ? { successCount: base.successCount } : {}),
  }
}

export function constructorStepForProfileStep(step) {
  return ({ identity: 'identity', model: 'brain', tools: 'tools', limits: 'permissions' })[step] || 'identity'
}

export function constructorToProfile(draft) {
  return {
    id: draft.id || '',
    name: draft.name,
    roleDescription: draft.roleDescription,
    systemPrompt: draft.systemPrompt || '',
    goals: [...(draft.goals || [])],
    rules: [...(draft.rules || [])],
    provider: draft.provider,
    providerPreset: draft.providerPreset,
    baseUrl: draft.baseUrl,
    model: draft.primaryModel || draft.model || '',
    fallbackModels: [...(draft.fallbackModels || [])],
    temperature: draft.temperature,
    maxOutputTokens: draft.maxOutputTokens,
    contextWindowTokens: draft.contextWindowTokens,
    reasoningEffort: draft.reasoningEffort,
    allowedTools: [...(draft.allowedTools || [])],
    toolPolicies: { ...(draft.toolPolicies || {}) },
    maxSteps: draft.maxSteps,
    maxDurationSeconds: draft.maxDurationSeconds,
    approvalMode: draft.approvalMode,
  }
}

export function constructorToBlueprint(draft) {
  return {
    id: draft.blueprintId || '',
    name: draft.name,
    roleDescription: draft.roleDescription,
    personality: draft.personality,
    mission: draft.mission,
    systemPrompt: draft.systemPrompt || '',
    goals: [...(draft.goals || [])],
    rules: [...(draft.rules || [])],
    constraints: [...(draft.constraints || [])],
    skillIds: [...(draft.skillIds || [])],
    allowedTools: [...(draft.allowedTools || [])],
    toolPolicies: { ...(draft.toolPolicies || {}) },
    connectionId: draft.connectionId || '',
    provider: draft.provider,
    providerPreset: draft.providerPreset,
    baseUrl: draft.baseUrl,
    primaryModel: draft.primaryModel || draft.model || '',
    fallbackModels: [...(draft.fallbackModels || [])],
    temperature: draft.temperature,
    maxOutputTokens: draft.maxOutputTokens,
    contextWindowTokens: draft.contextWindowTokens,
    reasoningEffort: draft.reasoningEffort,
    maxSteps: draft.maxSteps,
    maxDurationSeconds: draft.maxDurationSeconds,
    approvalMode: draft.approvalMode,
  }
}

export function legacySaveWouldDrop(draft, hubMode) {
  if (hubMode) return ''
  const filled = LEGACY_DROPPED_FIELDS
    .filter(([key]) => Array.isArray(draft?.[key]) ? draft[key].length : String(draft?.[key] || '').trim())
    .map(([, label]) => label)
  if (!filled.length) return ''
  return `Ядро не сообщило о поддержке Hub — в этом режиме не сохранятся: ${filled.join(', ')}. Обновите Point или очистите эти поля.`
}

export function constructorToProjectAgent(draft, existing) {
  const blueprint = constructorToBlueprint(draft)
  const agent = {
    ...blueprint,
    id: draft.id || '',
    blueprintId: draft.blueprintId || '',
    projectRules: [...(draft.projectRules || [])],
  }
  // Progress is server-owned on update; omit zeros so App.SaveProjectAgent preserves them.
  if (!existing) {
    if (typeof draft.experience === 'number') agent.experience = draft.experience
    if (typeof draft.level === 'number') agent.level = draft.level
    if (typeof draft.tasksCompleted === 'number') agent.tasksCompleted = draft.tasksCompleted
    if (typeof draft.successCount === 'number') agent.successCount = draft.successCount
  }
  return agent
}

export function normalizeToolRisk(value) {
  const risk = String(value || '').toUpperCase()
  if (risk === 'LOW' || risk === 'MEDIUM' || risk === 'HIGH' || risk === 'CRITICAL') return risk
  if (risk === 'SAFE') return 'LOW'
  if (risk === 'REVIEW') return 'HIGH'
  if (risk === 'APPROVAL') return 'CRITICAL'
  return 'MEDIUM'
}

export function defaultToolPolicyForRisk(risk) {
  return normalizeToolRisk(risk) === 'LOW' ? 'ALLOW' : 'ASK'
}

export function toolPolicyStrictness(value) {
  return TOOL_POLICY_STRICTNESS[String(value || '').toUpperCase()] || 0
}

export function constructorSkillGaps(draft, skills = []) {
  const allowed = new Set(draft?.allowedTools || [])
  const policies = draft?.toolPolicies || {}
  const gaps = []
  for (const id of draft?.skillIds || []) {
    const skill = skills.find(item => item.id === id)
    if (!skill) continue
    const required = skill.requiredTools || []
    const missing = required.filter(tool => !allowed.has(tool))
    // Умение в allowlist, но запрещённое политикой, — та же нехватка: навык не
    // заработает. Проверка смотрела только на allowlist и такой случай молчала.
    const denied = required.filter(tool => allowed.has(tool) && toolPolicyStrictness(policies[tool]) === TOOL_POLICY_STRICTNESS.DENY)
    if (missing.length || denied.length) gaps.push({ name: skill.name || skill.id, missing, denied })
  }
  return gaps
}

export function applySkillToolGrants(draft, previousSkillIds, skills = []) {
  const allowed = new Set(draft.allowedTools || [])
  const policies = { ...(draft.toolPolicies || {}) }
  const previous = new Set(previousSkillIds || [])
  for (const id of draft.skillIds || []) {
    if (previous.has(id)) continue
    const skill = skills.find(item => item.id === id)
    if (!skill) continue
    for (const tool of skill.requiredTools || []) allowed.add(tool)
    for (const [key, value] of Object.entries(skill.permissionDelta || {})) {
      if (!value) continue
      // Шаг «Навыки» обещает: «Skill сам не обходит DENY». Обещание держалось
      // только на исполнении — в настройке отметка навыка молча переписывала
      // выбранный человеком DENY на свой ALLOW, и обходить было уже нечего.
      // Дописать недостающее и ужесточить навык вправе; ослабить — нет.
      if (toolPolicyStrictness(value) >= toolPolicyStrictness(policies[key])) policies[key] = value
    }
  }
  draft.allowedTools = [...allowed]
  draft.toolPolicies = policies
  return draft
}

export function createAgentConstructor({
  ui,
  root,
  vscode,
  esc,
  lines,
  providerCatalog,
  agentById,
  hubModeAvailable,
  persistDraft,
  getState,
  getIgnoredCompanionSuggestions,
}) {
  function constructorPromptSignature(draft) {
    return JSON.stringify(constructorToProjectAgent(draft || {}, draft?.id ? agentById(draft.id) : undefined))
  }

  function resetCompiledPromptPreview() {
    ui.compiledPromptPreview = undefined
    ui.compiledPromptStatus = 'idle'
    ui.compiledPromptError = ''
    ui.compiledPromptSignature = ''
  }

  function compiledPromptPreviewHtml(draft) {
    const signature = constructorPromptSignature(draft)
    if (ui.compiledPromptSignature !== signature) {
      ui.compiledPromptPreview = undefined
      ui.compiledPromptError = ''
      ui.compiledPromptStatus = 'idle'
      ui.compiledPromptSignature = signature
    }
    if (ui.compiledPromptStatus === 'idle') {
      ui.compiledPromptStatus = 'loading'
      setTimeout(() => vscode.postMessage({
        type: 'previewCompiledPrompt',
        agent: constructorToProjectAgent(draft, draft?.id ? agentById(draft.id) : undefined),
      }), 0)
    }
    if (ui.compiledPromptStatus === 'loading') {
      return `<details class="constructor-prompt-preview" open><summary>Скомпилированный промпт · рантайм</summary><p class="muted">Собираем тот же system message, что получит модель…</p></details>`
    }
    if (ui.compiledPromptError) {
      return `<details class="constructor-prompt-preview" open><summary>Скомпилированный промпт · рантайм</summary><p class="create-step-error">${esc(ui.compiledPromptError)}</p><button type="button" class="secondary" data-action="reload-compiled-prompt">Повторить</button></details>`
    }
    const text = ui.compiledPromptPreview?.systemMessage || ''
    return `<details class="constructor-prompt-preview" open><summary>Скомпилированный промпт · рантайм</summary><p class="muted">Точная инструкция запуска, не отдельный черновик конструктора.</p><pre>${esc(text || 'Заполните шаги конструктора')}</pre></details>`
  }

  function prepareAgentConstructor(source, step = 'identity') {
    ui.agentConstructorOpen = true
    ui.profileEditorOpen = false
    ui.profileDraft = undefined
    ui.createStepError = ''
    ui.constructorStep = CONSTRUCTOR_STEPS.some(item => item.id === step) ? step : 'identity'
    ui.constructorDraft = newConstructorDraft(source)
    resetCompiledPromptPreview()
    persistDraft()
  }

  function hubLegacyDropMessage(draft) {
    return legacySaveWouldDrop(draft, hubModeAvailable())
  }

  function projectAgentFromDraft(draft) {
    return constructorToProjectAgent(draft, draft?.id ? agentById(draft.id) : undefined)
  }

  function companionInlineSuggestions(step, draft) {
    const suggestions = []
    const role = `${draft?.roleDescription || ''} ${draft?.mission || ''} ${draft?.name || ''}`.toLowerCase()
    const tools = new Set(draft?.allowedTools || [])
    const ignored = getIgnoredCompanionSuggestions()
    if (step === 'brain') {
      if (!(draft?.primaryModel || draft?.model || '').trim()) {
        suggestions.push({ id: 'brain-model', text: 'Подключите модель на вкладке «Связи» или выберите среду Cursor', kind: 'nav', tab: 'connections' })
      }
    }
    if (step === 'tools' || step === 'permissions') {
      if (/разработ|dev|код|implement|backend|frontend/.test(role) && !tools.has('run_command')) {
        suggestions.push({ id: 'tool-terminal', text: 'Для этой роли имеет смысл добавить Terminal', kind: 'tool', tool: 'run_command' })
      }
      if (/ревью|review|audit|хранител/.test(role) && !tools.has('git_diff')) {
        suggestions.push({ id: 'tool-git', text: 'Для ревьюера полезен Git diff', kind: 'tool', tool: 'git_diff' })
      }
      if (/тест|qa|debug|ошиб/.test(role) && !tools.has('run_command')) {
        suggestions.push({ id: 'tool-test', text: 'Для QA-роли добавьте запуск команд (тесты)', kind: 'tool', tool: 'run_command' })
      }
      if (/архит|architect|план/.test(role) && !tools.has('propose_patch')) {
        suggestions.push({ id: 'tool-patch', text: 'Архитектору может понадобиться propose_patch для минимальных правок', kind: 'tool', tool: 'propose_patch' })
      }
    }
    return suggestions.filter(item => !ignored.has(item.id))
  }

  function companionSuggestionChipsHtml(step, draft) {
    const suggestions = companionInlineSuggestions(step, draft)
    if (!suggestions.length) return ''
    return `<aside class="companion-inline"><header><span>КОМПАНЬОН</span><small>Контекстные подсказки · не блокируют работу</small></header>${suggestions.map(item => `<div class="companion-chip" data-suggest-id="${esc(item.id)}"><p>${esc(item.text)}</p><footer><button type="button" class="primary" data-action="companion-suggest-add" data-suggest-id="${esc(item.id)}" data-kind="${esc(item.kind)}" data-tool="${esc(item.tool || '')}" data-tab="${esc(item.tab || '')}">Добавить</button><button type="button" class="secondary" data-action="companion-suggest-ignore" data-suggest-id="${esc(item.id)}">Игнорировать</button></footer></div>`).join('')}</aside>`
  }

  function readConstructorToolPolicies(source, base) {
    const toolPolicies = { ...(base || source?.toolPolicies || {}) }
    const policyInputs = root.querySelectorAll('select[name="constructor-tool-policy"]')
    for (const input of policyInputs) {
      const name = input.dataset.tool
      if (name) toolPolicies[name] = input.value || defaultToolPolicyForRisk('MEDIUM')
    }
    return toolPolicies
  }

  function constructorSkillGapHtml(draft) {
    const gaps = constructorSkillGaps(draft, getState().boot?.skills || [])
    if (!gaps.length) return ''
    const line = item => {
      const parts = []
      if (item.missing.length) parts.push(`требует ${item.missing.map(esc).join(', ')} — отметьте эти tools на шаге «Инструменты»`)
      if (item.denied.length) parts.push(`требует ${item.denied.map(esc).join(', ')}, но политика запрещает — снимите DENY на шаге «Инструменты» или уберите навык`)
      return `${esc(item.name)} ${parts.join('; ')}.`
    }
    return `<aside class="create-step-error">${gaps.map(line).join('<br>')}</aside>`
  }

  function grantSkills(draft, previousSkillIds) {
    return applySkillToolGrants(draft, previousSkillIds, getState().boot?.skills || [])
  }

  function currentConstructorForm() {
    const source = ui.constructorDraft || newConstructorDraft()
    const connectionId = root.querySelector('#connection-id')?.value ?? source.connectionId ?? ''
    const connection = (getState().boot?.connections || []).find(item => item.id === connectionId)
    const preset = providerCatalog().find(item => item.id === (connection?.presetId || source.providerPreset || source.provider))
    const toolInputs = root.querySelectorAll('input[name="constructor-tool"]')
    const skillInputs = root.querySelectorAll('input[name="constructor-skill"]')
    const toolPolicies = { ...(source.toolPolicies || {}) }
    const networkPolicyInput = root.querySelector('#constructor-network-policy')
    if (networkPolicyInput) {
      for (const key of Object.keys(toolPolicies)) {
        if (key === 'network' || key.toLowerCase().startsWith('network:')) delete toolPolicies[key]
      }
      toolPolicies.network = networkPolicyInput.value || 'DENY'
      if (toolPolicies.network === 'ALLOWLIST') {
        for (const host of lines(root.querySelector('#constructor-network-hosts')?.value)) {
          toolPolicies[`network:${host.toLowerCase()}`] = 'ALLOW'
        }
      }
    }
    return {
      ...source,
      name: root.querySelector('#constructor-name')?.value.trim() ?? source.name ?? '',
      personality: root.querySelector('#constructor-personality')?.value.trim() ?? source.personality ?? '',
      roleDescription: root.querySelector('#constructor-role')?.value.trim() ?? source.roleDescription ?? '',
      mission: root.querySelector('#constructor-mission')?.value.trim() ?? source.mission ?? '',
      systemPrompt: root.querySelector('#constructor-system-prompt')?.value ?? source.systemPrompt ?? '',
      goals: root.querySelector('#constructor-goals') ? lines(root.querySelector('#constructor-goals')?.value) : [...(source.goals || [])],
      rules: root.querySelector('#constructor-rules') ? lines(root.querySelector('#constructor-rules')?.value) : [...(source.rules || [])],
      constraints: root.querySelector('#constructor-constraints') ? lines(root.querySelector('#constructor-constraints')?.value) : [...(source.constraints || [])],
      projectRules: root.querySelector('#constructor-project-rules') ? lines(root.querySelector('#constructor-project-rules')?.value) : [...(source.projectRules || [])],
      skillIds: skillInputs.length ? [...skillInputs].filter(item => item.checked).map(item => item.value) : [...(source.skillIds || [])],
      allowedTools: toolInputs.length ? [...toolInputs].filter(item => item.checked).map(item => item.value) : [...(source.allowedTools || [])],
      toolPolicies: readConstructorToolPolicies(source, toolPolicies),
      connectionId,
      provider: connection?.provider || preset?.kind || source.provider,
      providerPreset: connection?.presetId || preset?.id || source.providerPreset || source.provider,
      baseUrl: connection?.baseUrl || preset?.baseUrl || source.baseUrl || '',
      primaryModel: root.querySelector('#model')?.value.trim() ?? source.primaryModel ?? source.model ?? '',
      model: root.querySelector('#model')?.value.trim() ?? source.primaryModel ?? source.model ?? '',
      fallbackModels: root.querySelector('#constructor-fallback-models')
        ? lines(root.querySelector('#constructor-fallback-models')?.value)
        : [...(source.fallbackModels || [])],
      temperature: Number(root.querySelector('#temperature')?.value ?? source.temperature ?? 0.2),
      maxOutputTokens: Number(root.querySelector('#max-output-tokens')?.value || source.maxOutputTokens || 4096),
      contextWindowTokens: Number(root.querySelector('#context-window-tokens')?.value || source.contextWindowTokens || 32768),
      // Значение по умолчанию совпадает с тем, что показывает экран. Здесь стояло
      // 'medium': агент, созданный мимо отрисованной формы, получал включённое
      // рассуждение, о котором на экране написано «Обычное».
      reasoningEffort: root.querySelector('#reasoning-effort')?.value || source.reasoningEffort || 'none',
      maxSteps: Number(root.querySelector('#constructor-max-steps')?.value || source.maxSteps || 30),
      maxDurationSeconds: Number(root.querySelector('#constructor-timeout')?.value || source.maxDurationSeconds || 600),
      approvalMode: root.querySelector('#constructor-approval-mode')?.value || source.approvalMode || 'safe',
    }
  }

  function constructorStepNav(activeStep) {
    const index = Math.max(0, CONSTRUCTOR_STEPS.findIndex(step => step.id === activeStep))
    const progress = Math.round((index / Math.max(1, CONSTRUCTOR_STEPS.length - 1)) * 100)
    const current = CONSTRUCTOR_STEPS[index] || CONSTRUCTOR_STEPS[0]
    return `<nav class="profile-step-nav constructor-nav" aria-label="Шаги конструктора агента"><div class="create-flow-progress"><span>ШАГ ${String(index + 1).padStart(2, '0')} / ${String(CONSTRUCTOR_STEPS.length).padStart(2, '0')}</span><div class="create-live-rail"><i style="width:${progress}%"></i></div><small>${esc(current.why || '')}</small></div><div class="create-flow-steps constructor-steps">${CONSTRUCTOR_STEPS.map((step, i) => {
      const stateClass = step.id === activeStep ? 'on' : i < index ? 'done' : ''
      return `<button type="button" class="${stateClass}" data-action="constructor-step" data-step="${esc(step.id)}"><em>${i < index ? '✓' : String(i + 1).padStart(2, '0')}</em><span><b>${esc(step.label)}</b><small>${esc(step.hint)}</small></span></button>`
    }).join('')}</div>${ui.createStepError ? `<p class="create-step-error">${esc(ui.createStepError)}</p>` : ''}</nav>`
  }

  return {
    CONSTRUCTOR_STEPS,
    newConstructorDraft,
    constructorStepForProfileStep,
    prepareAgentConstructor,
    constructorToProfile,
    constructorToBlueprint,
    legacySaveWouldDrop: hubLegacyDropMessage,
    constructorToProjectAgent: projectAgentFromDraft,
    companionSuggestionChipsHtml,
    normalizeToolRisk,
    defaultToolPolicyForRisk,
    readConstructorToolPolicies,
    toolPolicyStrictness,
    constructorSkillGaps: draft => constructorSkillGaps(draft, getState().boot?.skills || []),
    constructorSkillGapHtml,
    applySkillToolGrants: grantSkills,
    currentConstructorForm,
    constructorStepNav,
    resetCompiledPromptPreview,
    compiledPromptPreviewHtml,
  }
}
