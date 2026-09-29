import { companionBrainMode, companionConfigForBrain, COMPANION_SETUP_STEPS, COMPANION_SETUP_STEP_ALIAS } from './companion-compose.js'

// Правила настройки работают с текущим состоянием и DOM панели.
export function createCompanionSetupController({
  root, vscode, esc, getState, live, render, sanitizeCompanionSetupDraft,
  providerCatalog, companionSkillSelection, companionBlockedSkills,
  connectionFormHtml, modelChoiceHtml, companionProbeCtaLabel, companionProbeHtml,
}) {
  function companionProviderPresets() {
    const blocked = new Set(['cursor-cli', 'codex-cli', 'claude-code-cli'])
    return (getState().boot?.providerCatalog || []).filter(item => !blocked.has(item.kind))
  }
  function providerWantsToken(preset) {
    if (!preset) return true
    if (preset.requiresApiKey || preset.id === 'llmux' || preset.id === 'custom') return true
    return !preset.local
  }
  function defaultCompanionProviderPreset() {
    return companionProviderPresets().find(item => item.id === 'llmux') || companionProviderPresets()[0]
  }
  function normalizeCompatibleBaseUrl(raw, kind) {
    const value = String(raw || '').trim()
    if (!value) return ''
    try {
      const url = new URL(value)
      if (url.protocol !== 'http:' && url.protocol !== 'https:') return value
      if (kind === 'openai-compatible' && (!url.pathname || url.pathname === '/')) url.pathname = '/v1'
      url.search = ''
      url.hash = ''
      return url.toString().replace(/\/$/, '')
    } catch {
      return value
    }
  }
  // Компаньон и Мастер ходят тем же providers.New, что и агенты, — он знает и
  // Anthropic, и Azure. Отбор по двум видам провайдера был ограничением экрана,
  // а не движка, и прятал уже заведённые подключения.
  function companionConnections() {
    return getState().boot?.connections || []
  }
  function companionSetupDraftFromConfig() {
    const companion = getState().boot?.companion || {}
    const connections = companionConnections()
    const providerPresets = companionProviderPresets()
    // Привязка хранится идентификатором. Подбор по пресету и виду провайдера
    // остаётся только для конфигов, сохранённых до появления connectionId.
    const bound = connections.find(item => companion.connectionId && item.id === companion.connectionId)
    const exact = connections.find(item => companion.providerPreset && item.presetId === companion.providerPreset)
    const connection = bound || exact || connections.find(item => companion.provider && item.provider === companion.provider)
    const providerPreset = companion.providerPreset || connection?.presetId || providerPresets.find(item => item.kind === companion.provider)?.id || providerPresets[0]?.id || ''
    const provider = companion.provider || connection?.provider || providerPresets.find(item => item.id === providerPreset)?.kind || ''
    const presetMeta = providerPresets.find(item => item.id === providerPreset)
    return sanitizeCompanionSetupDraft({
      ...companion,
      mode: companionBrainMode(companion),
      connectionMode: connection ? 'existing' : 'new',
      connectionId: connection?.id || '',
      connectionName: connection?.displayName || '',
      providerPreset,
      provider,
      baseUrl: companion.baseUrl || connection?.baseUrl || presetMeta?.baseUrl || '',
      model: companion.model || presetMeta?.defaultModel || '',
    })
  }
  function normalizeCompanionSetupStep(step) {
    const aliased = COMPANION_SETUP_STEP_ALIAS[step] || step
    return COMPANION_SETUP_STEPS.some(item => item.id === aliased) ? aliased : 'brain'
  }
  function ensureCompanionSetupDraft() {
    if (!live.companionSetupDraft) live.companionSetupDraft = companionSetupDraftFromConfig()
    live.companionSetupStep = normalizeCompanionSetupStep(live.companionSetupStep)
    return live.companionSetupDraft
  }
  // Адрес, ключ и вид провайдера принадлежат подключению, а не черновику: с
  // экрана снимаются только выбор связи и model ID, остальное читается у неё.
  function companionDraftWithConnection(draft, connectionId, model) {
    const connection = companionConnections().find(item => item.id === connectionId)
    return sanitizeCompanionSetupDraft({
      ...draft,
      connectionId,
      connectionName: connection?.displayName || draft.connectionName,
      providerPreset: connection?.presetId || draft.providerPreset,
      provider: connection?.provider || draft.provider,
      baseUrl: connection?.baseUrl || draft.baseUrl,
      model,
    })
  }
  function mergeCompanionConnectionForm(base) {
    const draft = sanitizeCompanionSetupDraft(base)
    const value = (selector, fallback = '') => root.querySelector(selector)?.value ?? fallback
    return companionDraftWithConnection(
      draft,
      value('#connection-id', draft.connectionId),
      value('#model', draft.model).trim(),
    )
  }
  function currentCompanionSetupDraft() {
    const draft = sanitizeCompanionSetupDraft(live.companionSetupDraft || companionSetupDraftFromConfig())
    const value = (selector, fallback = '') => root.querySelector(selector)?.value ?? fallback
    const number = (selector, fallback) => {
      const parsed = Number(value(selector, fallback))
      return Number.isFinite(parsed) ? parsed : fallback
    }
    return sanitizeCompanionSetupDraft({
      ...companionDraftWithConnection(
        draft,
        value('#connection-id', draft.connectionId),
        value('#model', draft.model).trim(),
      ),
      temperature: number('#companion-setup-temperature', draft.temperature),
      maxOutputTokens: number('#companion-setup-max-output', draft.maxOutputTokens),
      criticality: number('#companion-criticality', draft.criticality),
      creativity: number('#companion-creativity', draft.creativity),
      verbosity: number('#companion-verbosity', draft.verbosity),
      initiative: number('#companion-initiative', draft.initiative),
      questionStrictness: number('#companion-question-strictness', draft.questionStrictness),
      riskTolerance: number('#companion-risk-tolerance', draft.riskTolerance),
      autoAct: root.querySelector('#companion-auto-act')?.checked ?? draft.autoAct,
      autoOpenChatOnCritical: root.querySelector('#companion-auto-open-critical')?.checked ?? draft.autoOpenChatOnCritical,
      autoSendModelPrompt: root.querySelector('#companion-auto-send-model')?.checked ?? draft.autoSendModelPrompt,
      skillIds: companionSkillSelection(draft),
    })
  }
  function companionConfigFromDraft(value) {
    const draft = sanitizeCompanionSetupDraft(value)
    const connection = companionConnections().find(item => item.id === draft.connectionId)
    const preset = companionProviderPresets().find(item => item.id === (connection?.presetId || draft.providerPreset))
    return companionConfigForBrain(draft, { current: getState().boot?.companion || {}, connection, preset })
  }

  function companionSetupValidation(step, value) {
    const draft = sanitizeCompanionSetupDraft(value)
    if (step === 'skills') {
      // Такой навык ядро отвергает вместе со всей настройкой, поэтому отказ
      // называется здесь — на шаге, где отметку видно и есть чем её снять.
      const blocked = companionBlockedSkills(draft)
      if (blocked.length) return `Навык «${blocked[0].name || blocked[0].id}» требует инструмент вне доступа помощника. Снимите отметку — с ней настройка не сохранится.`
    }
    if (step === 'brain' || step === 'connection') {
      if (!draft.connectionId) return 'Сначала создайте и проверьте подключение или выберите уже сохранённое.'
      if (!draft.model.trim()) return 'Выберите найденную модель или введите её точный ID.'
    }
    if (step === 'boundaries') {
      if (draft.temperature < 0 || draft.temperature > 2) return 'Температура должна быть от 0 до 2.'
      if (draft.maxOutputTokens < 128 || draft.maxOutputTokens > 8192) return 'Размер ответа должен быть от 128 до 8192 токенов.'
    }
    return ''
  }
  // Выбор «подключение → модель» — тот же контрол, что в конструкторе агента и в
  // карточке персонажа. Своя сетка провайдеров, свои поля адреса и ключа и свой
  // список моделей были здесь четвёртой копией одного экрана. Подключение
  // заводится одной формой — той же, что на экране «Связи»; на онбординге уходить
  // туда некуда, поэтому форма доступна здесь же, свёрнутой.
  function saveConnectionFromFields() {
    const providerEl = root.querySelector('#connection-provider')
    const provider = providerEl?.value || ''
    const presetId = providerEl?.selectedOptions?.[0]?.dataset?.preset || ''
    const preset = providerCatalog().find(item => item.id === presetId)
    const displayName = root.querySelector('#connection-name')?.value.trim() || preset?.name || presetId || provider
    const baseUrl = normalizeCompatibleBaseUrl(root.querySelector('#connection-base-url')?.value.trim() || '', provider)
    const apiKey = root.querySelector('#connection-api-key')?.value || ''
    // Версия API принадлежит подключению, а не адресу: у Azure она уходит в
    // query, и нормализация URL её срезала бы.
    const apiVersion = root.querySelector('#connection-api-version')?.value.trim() || ''
    if (!baseUrl) { live.transientError = 'Укажите адрес сервиса, например https://llmux.company.internal/v1'; render(); return }
    if (providerWantsToken(preset) && !apiKey) { live.transientError = 'Для этого источника нужен токен.'; render(); return }
    if (provider === 'azure-openai' && !apiVersion) { live.transientError = 'Для Azure укажите версию API — без неё запрос будет отвергнут.'; render(); return }
    const id = root.querySelector('#connection-id-edit')?.value || ''
    const defaultModel = root.querySelector('#connection-default-model')?.value.trim() || ''
    live.connectionEditingId = ''
    vscode.postMessage({ type: 'saveConnection', id, provider, presetId, displayName, baseUrl, apiKey, apiVersion, defaultModel })
  }
  function connectionDrawerHtml(open) {
    return `<details class="hire-drawer creation-drawer"${open ? ' open' : ''}><summary><span class="hire-kicker">Связи</span> Новое подключение</summary>${connectionFormHtml('', { nested: true })}</details>`
  }
  function companionConnectionFieldsHtml(value) {
    const draft = sanitizeCompanionSetupDraft(value)
    const list = companionConnections()
    const selected = list.find(item => item.id === draft.connectionId) || list.find(item => item.isDefault) || list[0]
    const probe = selected
      ? `<button type="button" class="secondary companion-probe-cta" data-action="probe-companion-connection" data-id="${esc(selected.id)}" ${live.companionProviderProbe?.loading ? 'disabled' : ''}>${esc(companionProbeCtaLabel('existing'))}</button>`
      : ''
    return `${modelChoiceHtml({ connectionId: selected?.id || '', model: draft.model, showTuning: false })}${probe}${companionProbeHtml()}${connectionDrawerHtml(!list.length)}`
  }
  return {
    companionProviderPresets, providerWantsToken, defaultCompanionProviderPreset,
    normalizeCompatibleBaseUrl, companionConnections, companionSetupDraftFromConfig,
    normalizeCompanionSetupStep, ensureCompanionSetupDraft, companionDraftWithConnection,
    mergeCompanionConnectionForm, currentCompanionSetupDraft, companionConfigFromDraft,
    companionSetupValidation, saveConnectionFromFields, connectionDrawerHtml,
    companionConnectionFieldsHtml,
  }
}
