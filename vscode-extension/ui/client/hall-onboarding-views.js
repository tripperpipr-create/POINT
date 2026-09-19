import { fillAttribute, formatDateTime } from './format-units.js'
import { normalizeBrainMode } from './companion-compose.js'
import { createAgentWorkTranscript } from './agent-work-transcript.js'
import {
  newConstructorDraft,
  constructorToProfile,
  constructorToProjectAgent,
  normalizeToolRisk,
  defaultToolPolicyForRisk,
} from './agent-constructor.js'
// Опасность умения человек читает по-русски, а ядро получает то же значение
// латиницей: оно остаётся в class="risk-…" и в отправляемом профиле.
const TOOL_RISK_LABELS = { LOW: 'низкая', MEDIUM: 'средняя', HIGH: 'высокая', CRITICAL: 'критическая' }

export function createHallOnboardingViews(dependencies) {
  const {
    COMPANION_PRESETS,
    CONSTRUCTOR_STEPS,
    HALL_SECTIONS,
    ONBOARDING_CHAPTERS,
    ONBOARDING_STEPS,
    ORCHESTRATOR_PRESETS,
    ORCHESTRATOR_TRAIT_FIELDS,
    TOOL_PRESETS,
    activeQuestCard,
    activeToolPresetId,
    agentClass,
    agentProgress,
    agentSynergy,
    blueprintById,
    capabilityDeltaHtml,
    companionConfigFromDraft,
    companionConnectionFieldsHtml,
    companionConnections,
    companionLocalReadyHtml,
    companionModeCardsHtml,
    companionOnboardingFinished,
    companionPersonalityControlsHtml,
    companionPersonalityPreviewHtml,
    companionPresetStudioHtml,
    companionProbeCtaLabel,
    companionProbeHtml,
    connectionDrawerHtml,
    connectionFormHtml,
    modelChoiceHtml,
    companionProviderPresets,
    companionScenePickerHtml,
    companionSetupStatusHtml,
    companionStyleStudioHtml,
    companionSuggestionChipsHtml,
    companionTraitKeyToId,
    compiledPromptPreviewHtml,
    completionProofHtml,
    connectionOrbHtml,
    connectionStatusLabels,
    constructorSkillGapHtml,
    constructorStepNav,
    contextInspectorPanelHtml,
    coreFailureText,
    countOf,
    data,
    defaultCompanionProviderPreset,
    diagnosticsCard,
    esc,
    execControlsHtml,
    featuredProviderChips,
    firstUnreadinessStep,
    formatCents,
    formatDuration,
    guardrailEventText,
    healthLabels,
    historyQualitySignals,
    hubAgents,
    hubModeAvailable,
    isCompanionOnboardingStep,
    isCompanionView,
    isOrchestratorOnboardingStep,
    isSystemOnboardingStep,
    isWide,
    mergeCompanionConnectionForm,
    newProfile,
    normalizeRunnableAgent,
    onboardingStepNav,
    onboardingWelcomeResume,
    orchestratorOnboardingFinished,
    orphanExecutions,
    orphanExecutionsHtml,
    patchStatusLabel,
    pendingActionProposals,
    pendingChangeSets,
    pendingDecisionsBanner,
    pendingQuestProposals,
    persistDraft,
    plural,
    profileEditor,
    profileReadiness,
    providerPreset,
    providerWantsToken,
    questAsideHtml,
    questBriefGuidanceHtml,
    questLevelUpBadge,
    questListHtml,
    readinessBanner,
    requiresApiKey,
    root,
    runComparison,
    runIsActiveNow,
    runIsLive,
    runQualityOutcomeHtml,
    sanitizeCompanionSetupDraft,
    shell,
    status,
    templatePickerHtml,
    toolFailureText,
    toolName,
    ui,
    usageSummary,
    vscode,
  } = dependencies

  const { agentWorkTranscriptHtml, approvalCard, patchCard } = createAgentWorkTranscript({
    esc,
    data,
    toolName,
    formatDuration,
    guardrailEventText,
    toolFailureText,
    coreFailureText,
    patchStatusLabel,
    questLevelUpBadge,
  })

  function hallActiveSection() {
    const tab = ui.state.selectedTab
    const found = HALL_SECTIONS.find(section => section.tabs.includes(tab))
    return found ? found.id : ''
  }
  
  function hallCrumb() {
    const labels = {
      overview: 'ОБЗОР', master: 'МАСТЕР', decisions: 'РЕШЕНИЯ', changesets: 'ИЗМЕНЕНИЯ / НАБОРЫ',
      journal: 'ИЗМЕНЕНИЯ / ЖУРНАЛ', filehistory: 'ИЗМЕНЕНИЯ / ПО ФАЙЛУ', changes: 'ИЗМЕНЕНИЯ', quests: 'КВЕСТЫ', quest: 'КВЕСТЫ',
      flows: 'КВЕСТЫ / ФЛОУ', workflows: 'КВЕСТЫ / ФЛОУ', history: 'КВЕСТЫ / ХРОНИКА',
      agents: 'ГИЛЬДИЯ / АГЕНТЫ', teams: 'ГИЛЬДИЯ / ОТРЯДЫ', skills: 'ГИЛЬДИЯ / НАВЫКИ',
      memory: 'ГИЛЬДИЯ / ПАМЯТЬ', connections: 'ГИЛЬДИЯ / СВЯЗИ', databases: 'ГИЛЬДИЯ / БАЗЫ',
      tools: 'ГИЛЬДИЯ / ИНСТРУМЕНТЫ', onboarding: 'ГИЛЬДИЯ / ОНБОРДИНГ',
      // Отдельные окна IDE — не разделы Чертога, но путь показывать всё равно надо.
      statistics: 'СТАТИСТИКА', docker: 'КОНТЕЙНЕРЫ', quest: 'КВЕСТЫ',
    }
    // Незнакомая вкладка называется Чертогом, а не Гильдией: запасной вариант,
    // указывающий в конкретное неверное место, врёт увереннее пустого. Экран
    // статистики так и объявлял себя Гильдией.
    return labels[ui.state.selectedTab] || 'ЧЕРТОГ'
  }
  
  // Счётчик показывает не всё подряд, а число ожидающих решения. Пока очередь не
  // загружена, берём наборы изменений: они видны из bootstrap без лишнего запроса.
  // Что видно как ожидающее по одному лишь состоянию мира.
  //
  // Правило то же, что в ядре: прогон в waiting_approval стоит и ждёт человека.
  // Служит и запасным значением (пока очередь не пришла), и признаком того, что
  // пришедшая очередь устарела.
  function locallyWaitingCount() {
    const waitingRuns = (ui.state.boot?.runs || [])
      .filter(run => run.status === 'waiting_approval' || run.status === 'waiting').length
    return waitingRuns + pendingChangeSets().length
      + pendingQuestProposals().length + pendingActionProposals().length
  }
  
  // Устарела ли загруженная очередь решений.
  //
  // Прежде перезапрос случался, только когда локально ждущих стало БОЛЬШЕ, чем
  // в очереди. Убыль так не ловилась ни разу: отклонённое предложение уходило
  // из ядра, а на экране оставалось до перезагрузки окна — человек жал
  // «отклонить» по второму и третьему разу на том, чего уже нет.
  //
  // Сравнивать local с total нельзя в обе стороны: очередь ядра считает и то,
  // чего в состоянии мира не видно (остановленные узлы Flow), и `!==` держал бы
  // очередь в вечном перезапросе. Поэтому сравниваем локальный счёт с ним же на
  // момент загрузки: изменился — значит мир сдвинулся, и очередь надо взять
  // заново, в какую бы сторону он ни сдвинулся.
  let localWhenQueueLoaded = -1
  function markDecisionsLoaded() {
    localWhenQueueLoaded = locallyWaitingCount()
  }
  function decisionsQueueIsStale() {
    if (localWhenQueueLoaded < 0) return false
    const local = locallyWaitingCount()
    if (local !== localWhenQueueLoaded) return true
    return typeof ui.decisionsData?.total === 'number' && local > ui.decisionsData.total
  }

  // Подпись под числом обязана считать то же, что само число.
  //
  // На Обзоре крупная цифра бралась из очереди решений, а строка под ней — из
  // hubSituation(), то есть из другого счётчика: рядом стояли «2» и «1 этап
  // ждёт вашего решения». Оба утверждения верны по отдельности и противоречат
  // друг другу вместе. Здесь разбивка собирается из тех же слагаемых, что и
  // счёт; если ядро насчитало больше локально известного, разбивка не
  // выдумывается — строка говорит про очередь целиком.
  function decisionsWaitingBreakdown() {
    const approvals = (ui.state.boot?.runs || [])
      .filter(run => run.status === 'waiting_approval' || run.status === 'waiting').length
    const sets = pendingChangeSets().length
    const quests = pendingQuestProposals().length
    const actions = pendingActionProposals().length
    const parts = []
    if (approvals) parts.push(countOf(approvals, 'подтверждение', 'подтверждения', 'подтверждений'))
    if (sets) parts.push(countOf(sets, 'набор изменений', 'набора изменений', 'наборов изменений'))
    if (quests) parts.push(countOf(quests, 'предложение квеста', 'предложения квеста', 'предложений квеста'))
    if (actions) parts.push(countOf(actions, 'черновик', 'черновика', 'черновиков'))
    const local = approvals + sets + quests + actions
    if (!parts.length || local !== decisionsWaitingCount()) return 'Разберите очередь решений'
    return parts.join(' · ')
  }

  function decisionsWaitingCount() {
    const local = locallyWaitingCount()
    if (ui.decisionsData && typeof ui.decisionsData.total === 'number') {
      // Очередь ядра — источник истины, но пока она не догнала состояние мира,
      // берём большее. Ошибка несимметрична: недосчитать значит спрятать
      // остановившуюся работу, пересчитать — привести человека на экран, где
      // строк окажется меньше.
      return Math.max(ui.decisionsData.total, local)
    }
    return local
  }
  
  function hallSectionBadge(id, pendingSets, waiting) {
    if (id === 'decisions') return waiting ? String(waiting) : '·'
    if (id === 'changes') return pendingSets ? String(pendingSets) : '·'
    if (id === 'quests') {
      // Счётчик обязан считать то, что показывает раздел. Он считал активные
      // исполнения, а раздел перечисляет квесты: два квеста без запущенных
      // исполнений давали пустой значок, а запущенное исполнение без записи
      // квеста — значок «1» над пустым списком. Число, противоречащее экрану,
      // подрывает доверие к обоим.
      // Правка была неполной: значок считал только активные квесты, а список
      // рисует все, и «либо квесты, либо запуски» вместо суммы. Два активных
      // квеста с тремя запусками без квеста давали значок «2» над пятью строками.
      // Считаем ровно то, что печатают заголовки раздела: «N квестов» + «N
      // запусков без квеста».
      const quests = (ui.state.boot?.quests || []).length
      return String(quests + orphanExecutions().length || '·')
    }
    return '·'
  }
  
  // Тревога очереди — самый громкий элемент шапки и единственное, что остаётся на
  // виду в разговоре с Мастером. Два огреха на нём и держались:
  //
  // «1 ЖДУТ РЕШЕНИЯ» — глагол выбран заранее, хотя число приходит из данных.
  // Проверка склонений это место не видит: она ищет существительное сразу после
  // подстановки, а здесь между ними глагол, который и требует согласования.
  //
  // И это была надпись, а не кнопка. Названная очередь — ровно то место, куда
  // уходят предложения Мастера, но добраться до неё из разговора можно было
  // только через «← ЧЕРТОГ» и рейку.
  function hallAlarmHtml(waiting) {
    if (!waiting) return ''
    return `<button type="button" class="hall-alarm" data-action="tab" data-tab="decisions" title="Открыть очередь решений">
      <i></i><span>${waiting} ${plural(waiting, 'ждёт', 'ждут', 'ждут')} решения</span>
    </button>`
  }
  
  // Второй значок срочного — непроверенные наборы изменений. На экране чата
  // рейки нет, и без него правки агента ждали бы человека молча, пока он сам не
  // заглянет в настройки. Цветом он не спорит с очередью решений: два алых
  // пятна в одной шапке одинаково срочны, то есть одинаково незаметны.
  function hallChangesAlarmHtml(pending) {
    if (!pending) return ''
    return `<button type="button" class="hall-alarm is-quiet" data-action="tab" data-tab="changesets" title="Открыть наборы изменений">
      <i></i><span>${countOf(pending, 'набор', 'набора', 'наборов')} без проверки</span>
    </button>`
  }

  function hallSpendLabel() {
    const records = ui.state.boot?.usageRecords
    if (!Array.isArray(records) || records.length === 0) return ''
    return formatCents(usageSummary(records).totalCents)
  }
  
  // Сколько запусков прошло без известной цены.
  //
  // Сумма считается только по записям с ценой — это правильно, но подписана она
  // была как просто «РАСХОД». Ядро в том же месте осторожнее: поле называется
  // knownCostCents, а страница статистики ставит «—» вместо нуля. Крупное число
  // без оговорки читается как «столько всего потрачено», хотя часть запусков в
  // него не вошла: у локальных моделей цены нет, у облачной модели вне каталога
  // цен — тоже, и там сумма занижена по-настоящему.
  function hallUnpricedRuns() {
    const records = ui.state.boot?.usageRecords
    if (!Array.isArray(records)) return 0
    return records.filter(item => item?.costCents == null).length
  }
  
  function workspaceTrustRequired() {
    return shell(`<main class="workspace-locked">
      <div class="safe-mode-mark" aria-hidden="true"><span>✓</span></div>
      <span class="safe-mode-label">БЕЗОПАСНЫЙ РЕЖИМ</span>
      <h2>Умения агентов пока запечатаны</h2>
      <p>Point не запускает локальное ядро, команды и умения без вашего разрешения для этого мира.</p>
      <div class="safe-mode-facts"><span><i>✓</i> Инвентарь можно просматривать и редактировать</span><span><i>—</i> Команды и содержимое проекта не отправляются модели</span></div>
      <button class="primary trust-button" data-action="manage-trust">Настроить доступ</button>
      <small>Режим можно изменить позже в безопасности проекта</small>
    </main>`)
  }
  
  function offline() {
    const starting = ui.state.service?.state === 'starting'
    const errored = ui.state.service?.state === 'error'
    const detail = String(ui.state.service?.detail || '').trim()
    if (errored) {
      return shell(`<main class="offline"><div class="orbit"><span>!</span></div><span class="quest-label">ЯДРО POINT</span><h2>Ядро не поднялось</h2><p>${esc(detail || 'Локальный процесс завершился с ошибкой. Откройте хронику ядра — там полный лог, а не только «fetch failed».')}</p><div class="empty-next"><button class="primary" data-action="start-server">Повторить запуск</button><button class="secondary" data-action="show-output">Хроника ядра</button></div><small>Антивирус или брандмауэр иногда блокируют порт 127.0.0.1</small></main>`)
    }
    return shell(`<main class="offline"><div class="orbit"><span>✦</span></div><span class="quest-label">ГИЛЬДИЯ POINT</span><h2>${starting?'Пробуждаем локальное ядро…':'Гильдия отдыхает'}</h2><p>Локальное ядро подключает выбранного персонажа, модель и безопасные умения только для текущего проекта.</p><button class="primary" data-action="start-server" ${starting?'disabled':''}>${starting?'Подключаемся…':'Пробудить ядро'}</button><small>Хроника и артефакты остаются на вашем компьютере</small></main>`)
  }
  
  function projectRequired() {
    const companion = isCompanionView()
    return shell(`<main class="offline project-required"><div class="orbit"><span>⌂</span></div><span class="quest-label">РАБОЧЕЕ ПРОСТРАНСТВО</span><h2>Выберите проект</h2><p>${companion
      ? 'Помощник Point привязывает контекст, историю и действия к конкретному проекту.'
      : 'Agent Hub, индекс, терминалы и инструменты Point начнут работу сразу после выбора локальной папки проекта.'}</p><div class="empty-next"><button class="primary" data-action="choose-project">Выбрать папку проекта</button></div><small>Можно переключиться на другой проект в любой момент через кнопку «Проекты» в шапке Point</small></main>`)
  }
  
  function onboardingCompanionDraft() {
    return sanitizeCompanionSetupDraft({
      // Навыки первый запуск не спрашивает: каталог на этом шаге ещё пуст.
      // Но сохранение отсюда уходит целой настройкой, и без переноса надетое
      // ранее снялось бы молча.
      skillIds: ui.state.boot?.companion?.skillIds,
      preset: ui.onboardingDraft.companionPreset || 'balanced',
      mode: ui.onboardingDraft.companionMode,
      connectionMode: ui.onboardingDraft.companionConnectionMode,
      connectionId: ui.onboardingDraft.companionConnectionId,
      connectionName: ui.onboardingDraft.companionConnectionName,
      providerPreset: ui.onboardingDraft.companionProviderPreset,
      provider: ui.onboardingDraft.companionProvider,
      baseUrl: ui.onboardingDraft.companionBaseUrl,
      model: ui.onboardingDraft.companionModel,
      autoAct: ui.onboardingDraft.autoAct,
      sampleScene: ui.onboardingDraft.sampleScene,
      criticality: ui.onboardingDraft.criticality,
      creativity: ui.onboardingDraft.creativity,
      verbosity: ui.onboardingDraft.verbosity,
      initiative: ui.onboardingDraft.initiative,
      questionStrictness: ui.onboardingDraft.questionStrictness,
      riskTolerance: ui.onboardingDraft.riskTolerance,
    })
  }
  function writeOnboardingCompanionDraft(value) {
    const next = sanitizeCompanionSetupDraft(value)
    ui.onboardingDraft = {
      ...ui.onboardingDraft,
      companionPreset: next.preset,
      companionMode: next.mode,
      companionConnectionMode: next.connectionMode,
      companionConnectionId: next.connectionId,
      companionConnectionName: next.connectionName,
      companionProviderPreset: next.providerPreset,
      companionProvider: next.provider,
      companionBaseUrl: next.baseUrl,
      companionModel: next.model,
      autoAct: next.autoAct,
      sampleScene: next.sampleScene,
      criticality: next.criticality,
      creativity: next.creativity,
      verbosity: next.verbosity,
      initiative: next.initiative,
      questionStrictness: next.questionStrictness,
      riskTolerance: next.riskTolerance,
    }
    return next
  }
  function sanitizeOrchestratorDraft(value = {}) {
    const preset = ORCHESTRATOR_PRESETS.find(item => item.id === value.preset) || ORCHESTRATOR_PRESETS[0]
    const number = (key, fallback) => {
      const parsed = Number(value[key])
      return Number.isFinite(parsed) ? Math.max(0, Math.min(100, parsed)) : fallback
    }
    // Companion is API-only, but the Master also has Point's built-in
    // deterministic engine. Reusing normalizeBrainMode here silently changed
    // a fresh Master draft to `model` and blocked onboarding on credentials.
    const mode = value.mode === 'model' ? 'model' : 'local'
    return {
      preset: preset.id,
      mode,
      connectionMode: value.connectionMode === 'new' ? 'new' : 'existing',
      connectionId: String(value.connectionId || ''),
      connectionName: String(value.connectionName || ''),
      providerPreset: String(value.providerPreset || ''),
      provider: String(value.provider || ''),
      baseUrl: String(value.baseUrl || ''),
      model: String(value.model || ''),
      planningDepth: number('planningDepth', preset.values.planningDepth),
      parallelism: number('parallelism', preset.values.parallelism),
      approvalStrictness: number('approvalStrictness', preset.values.approvalStrictness),
      teamPreference: number('teamPreference', preset.values.teamPreference),
    }
  }
  function onboardingOrchestratorDraft() {
    return sanitizeOrchestratorDraft({
      preset: ui.onboardingDraft.orchestratorPreset || 'conductor',
      mode: ui.onboardingDraft.orchestratorMode,
      connectionMode: ui.onboardingDraft.orchestratorConnectionMode,
      connectionId: ui.onboardingDraft.orchestratorConnectionId,
      connectionName: ui.onboardingDraft.orchestratorConnectionName,
      providerPreset: ui.onboardingDraft.orchestratorProviderPreset,
      provider: ui.onboardingDraft.orchestratorProvider,
      baseUrl: ui.onboardingDraft.orchestratorBaseUrl,
      model: ui.onboardingDraft.orchestratorModel,
      planningDepth: ui.onboardingDraft.planningDepth,
      parallelism: ui.onboardingDraft.parallelism,
      approvalStrictness: ui.onboardingDraft.approvalStrictness,
      teamPreference: ui.onboardingDraft.teamPreference,
    })
  }
  function writeOnboardingOrchestratorDraft(value) {
    const next = sanitizeOrchestratorDraft(value)
    ui.onboardingDraft = {
      ...ui.onboardingDraft,
      orchestratorPreset: next.preset,
      orchestratorMode: next.mode,
      orchestratorConnectionMode: next.connectionMode,
      orchestratorConnectionId: next.connectionId,
      orchestratorConnectionName: next.connectionName,
      orchestratorProviderPreset: next.providerPreset,
      orchestratorProvider: next.provider,
      orchestratorBaseUrl: next.baseUrl,
      orchestratorModel: next.model,
      planningDepth: next.planningDepth,
      parallelism: next.parallelism,
      approvalStrictness: next.approvalStrictness,
      teamPreference: next.teamPreference,
    }
    return next
  }
  function currentOnboardingOrchestratorValues() {
    const draft = onboardingOrchestratorDraft()
    const number = (id, fallback) => {
      const parsed = Number(root.querySelector(`#orchestrator-${id}`)?.value ?? fallback)
      return Number.isFinite(parsed) ? parsed : fallback
    }
    // Адрес, ключ и вид провайдера принадлежат подключению: с экрана снимаются
    // только выбор связи и model ID.
    const connectionId = root.querySelector('#connection-id')?.value || draft.connectionId
    const connection = companionConnections().find(item => item.id === connectionId)
    return sanitizeOrchestratorDraft({
      ...draft,
      connectionId,
      connectionName: connection?.displayName || draft.connectionName,
      providerPreset: connection?.presetId || draft.providerPreset,
      provider: connection?.provider || draft.provider,
      baseUrl: connection?.baseUrl || draft.baseUrl,
      model: (root.querySelector('#model')?.value || draft.model).trim(),
      planningDepth: number('planningDepth', draft.planningDepth),
      parallelism: number('parallelism', draft.parallelism),
      approvalStrictness: number('approvalStrictness', draft.approvalStrictness),
      teamPreference: number('teamPreference', draft.teamPreference),
    })
  }
  function orchestratorConfigFromDraft(value) {
    const draft = sanitizeOrchestratorDraft(value)
    const current = ui.state.boot?.orchestrator || {}
    if (draft.mode !== 'model') {
      return { ...current, connectionId: '', provider: '', providerPreset: '', baseUrl: '', model: '', preset: draft.preset, planningDepth: draft.planningDepth, parallelism: draft.parallelism, approvalStrictness: draft.approvalStrictness, teamPreference: draft.teamPreference }
    }
    const connection = companionConnections().find(item => item.id === draft.connectionId)
    const providerPreset = connection?.presetId || draft.providerPreset
    const provider = connection?.provider || draft.provider
    const preset = companionProviderPresets().find(item => item.id === providerPreset)
    return {
      ...current,
      preset: draft.preset,
      connectionId: draft.connectionId,
      provider,
      providerPreset,
      baseUrl: connection?.baseUrl || draft.baseUrl || preset?.baseUrl || '',
      model: draft.model,
      planningDepth: draft.planningDepth,
      parallelism: draft.parallelism,
      approvalStrictness: draft.approvalStrictness,
      teamPreference: draft.teamPreference,
    }
  }
  function persistOnboardingOrchestrator(save) {
    const draft = currentOnboardingOrchestratorValues()
    writeOnboardingOrchestratorDraft(draft)
    persistDraft()
    if (!save) return
    let config = orchestratorConfigFromDraft(draft)
    if (draft.mode === 'model' && (!config.provider || !config.model)) {
      config = { ...config, provider: '', providerPreset: '', baseUrl: '', model: '' }
    }
    vscode.postMessage({ type: 'saveOrchestratorConfig', config })
  }
  function orchestratorPresetStudioHtml(selectedId) {
    return `<div class="companion-preset-studio orchestrator-preset-studio">${ORCHESTRATOR_PRESETS.map(item => `<button type="button" class="${item.id === selectedId ? 'selected' : ''}" data-action="onboarding-orchestrator-preset" data-preset="${esc(item.id)}"><span>${esc(item.icon)}</span><strong>${esc(item.label)}</strong><small>${esc(item.hint)}</small></button>`).join('')}</div>`
  }
  function orchestratorPolicyControlsHtml(draft) {
    return `<div class="companion-personality-controls">${ORCHESTRATOR_TRAIT_FIELDS.map(item => `<label class="companion-range"><span><strong>${esc(item.label)}</strong><small>${esc(item.hint)}</small></span><div class="companion-range-track"><em>${esc(item.low)}</em><input id="orchestrator-${esc(item.id)}" data-orchestrator-policy data-trait="${esc(item.id)}" type="range" min="0" max="100" value="${Number(draft[item.id])}"><em>${esc(item.high)}</em></div><output>${Number(draft[item.id])}%</output></label>`).join('')}</div>`
  }
  // Политика Мастера приходит из ядра: её считает тот же код, который её
  // исполняет. Локальный пересказ правил разъезжался с движком — показывал
  // «высокая параллельность» там, где агентов ровно четыре.
  let orchestratorPolicy
  function setOrchestratorPolicy(value) { orchestratorPolicy = value }
  
  function orchestratorPolicyDraftKey(draft) {
    return [draft.preset, draft.planningDepth, draft.parallelism, draft.approvalStrictness,
      draft.teamPreference, draft.provider, draft.model].join('|')
  }
  
  // Политика кэшируется по черновику, а не в одной ячейке.
  //
  // На экране бывает два разных черновика: редактируемый в форме и сохранённый в
  // карточке обзора. С одной ячейкой они перезаписывали ключ друг друга, ответ
  // вызывал отрисовку, отрисовка — новый запрос: тот же бесконечный цикл, который
  // однажды уже сделал интерфейс неотзывчивым.
  const ORCHESTRATOR_POLICY_LIMIT = 32
  const orchestratorPolicyCache = new Map()
  const orchestratorPolicyInflight = new Set()
  const orchestratorPolicyFailed = new Set()
  
  function orchestratorPolicyLines(draft) {
    const key = orchestratorPolicyDraftKey(draft)
    if (key && !orchestratorPolicyCache.has(key) && !orchestratorPolicyInflight.has(key) && !orchestratorPolicyFailed.has(key)) {
      orchestratorPolicyInflight.add(key)
      setTimeout(() => vscode.postMessage({ type: 'orchestratorPolicy', key, draft: {
        preset: draft.preset, planningDepth: Number(draft.planningDepth) || 0,
        parallelism: Number(draft.parallelism) || 0, approvalStrictness: Number(draft.approvalStrictness) || 0,
        teamPreference: Number(draft.teamPreference) || 0,
        provider: draft.mode === 'model' ? (draft.provider || '') : '',
        model: draft.mode === 'model' ? (draft.model || '') : '',
      } }), 0)
    }
    const cached = orchestratorPolicyCache.get(orchestratorPolicyDraftKey(draft))
    if (cached?.lines) return cached.lines
    // «Считается…» после отказа — обещание, которого никто не выполнит.
    if (orchestratorPolicyFailed.has(key)) return ['Политику посчитать не удалось: ядро не ответило.']
    return ['Политика считается ядром…']
  }
  
  function orchestratorPreviewHtml(draft) {
    const preset = ORCHESTRATOR_PRESETS.find(item => item.id === draft.preset)
    return `<aside class="companion-persona-dock companion-live-preview" data-orchestrator-preview><header><span>${esc(preset?.icon || '⬡')}</span><div><strong>${esc(preset?.label || 'Компаньон')}</strong><small>Системный агент квестов, не компаньон IDE</small></div></header><div class="companion-trait-bars">${ORCHESTRATOR_TRAIT_FIELDS.map(item => `<span><small>${esc(item.label)}</small><i><b ${fillAttribute(draft[item.id])}></b></i></span>`).join('')}</div><ul>${orchestratorPolicyLines(draft).map(item => `<li>${esc(item)}</li>`).join('')}</ul></aside>`
  }
  function orchestratorModeCardsHtml(draft) {
    return `<div class="companion-mode-grid"><button type="button" class="companion-mode-card ${draft.mode === 'model' ? 'selected' : ''}" data-action="orchestrator-select-mode" data-mode="model"><span>⬡</span><strong>Своя модель мастера</strong><p>Отдельный мозг для назначения отряда и глубины плана. Это не диалог компаньона.</p><ul class="companion-mode-can"><li>Своя модель, не компаньон</li><li>Планирование квеста и партии</li><li>Исполнение — движок Point</li></ul><small>Нужна локальная или облачная модель</small></button><button type="button" class="companion-mode-card ${draft.mode === 'local' ? 'selected' : ''}" data-action="orchestrator-select-mode" data-mode="local"><span>⌁</span><strong>Движок Point</strong><p>Детерминированный движок: пресет сам решает размер отряда и автостарт Flow.</p><ul class="companion-mode-can"><li>Без ключа и токенов</li><li>Предсказуемые правила</li><li>Работает сразу</li></ul><small>Рекомендуется на старте</small></button></div>`
  }
  // Тот же общий выбор «подключение -> модель», что у компаньона и агентов.
  // Своя сетка источников, поля адреса и токена и свой список моделей были
  // третьей копией одного экрана и расходились с остальными.
  function orchestratorConnectionFieldsHtml(draft) {
    const list = companionConnections()
    const selected = list.find(item => item.id === draft.connectionId) || list.find(item => item.isDefault) || list[0]
    const probe = selected
      ? `<button type="button" class="secondary companion-probe-cta" data-action="probe-orchestrator-connection" data-id="${esc(selected.id)}" ${ui.companionProviderProbe?.loading ? 'disabled' : ''}>${esc(companionProbeCtaLabel('existing'))}</button>`
      : ''
    return `${modelChoiceHtml({ connectionId: selected?.id || '', model: draft.model, showTuning: false })}${probe}${companionProbeHtml()}${connectionDrawerHtml(!list.length)}`
  }
  function systemAgentsStripHtml() {
    const companion = ui.state.boot?.companion || {}
    const orch = ui.state.boot?.orchestrator
    const companionPreset = COMPANION_PRESETS.find(item => item.id === (companion.preset || ui.onboardingDraft.companionPreset))
    const orchPreset = ORCHESTRATOR_PRESETS.find(item => item.id === (orch?.preset || ui.onboardingDraft.orchestratorPreset))
    const companionMode = companion.model ? `модель ${companion.model}` : 'локальный режим'
    const orchDraft = orch ? sanitizeOrchestratorDraft({
      preset: orch.preset,
      mode: orch.model ? 'model' : 'local',
      model: orch.model,
      planningDepth: orch.planningDepth,
      parallelism: orch.parallelism,
      approvalStrictness: orch.approvalStrictness,
      teamPreference: orch.teamPreference,
    }) : null
    const orchMode = orch?.model ? `модель ${orch.model}` : orch ? 'движок Point' : 'не настроен'
    const orchDetail = orchDraft
      ? orchestratorPolicyLines(orchDraft)[0]
      : 'Отдельный агент: раздаёт задачи отряду, собирает Flow и ведёт квест.'
    // Две карточки, потому что это две роли. Компаньон смотрит за IDE и только
    // советует; Мастер — диспетчер, он раздаёт задачи отряду. Сливать их нельзя:
    // на разделении держится правило «наблюдение не начинает работу само».
    return `<section class="system-agents-pair"><header class="section-title"><span>СИСТЕМНЫЕ АГЕНТЫ</span><em>настраиваются в начале</em></header><div class="system-agents-grid"><article class="system-agent-card companion hub-card" data-action="open-companion-setup" title="Открыть настройку компаньона"><span>✦</span><div><small>КОМПАНЬОН</small><strong>${esc(companionPreset?.label || 'Помощник IDE')}</strong><p>Встроен в IDE и только рекомендует. Квесты сам не стартует.</p><em>${esc(companionMode)}</em></div><button type="button" class="secondary" data-action="open-companion-setup">Настроить</button></article><article class="system-agent-card orchestrator hub-card" data-action="open-orchestrator-setup" title="Открыть настройку мастера"><span>⬡</span><div><small>МАСТЕР</small><strong>${esc(orchPreset?.label || 'Не настроен')}</strong><p>${esc(orchDetail)}</p><em>${esc(orchMode)}</em></div><button type="button" class="secondary" data-action="open-orchestrator-setup">Настроить</button></article></div></section>`
  }
  function onboardingCompanionActive() {
    return ui.state.selectedTab === 'onboarding' && isCompanionOnboardingStep(ui.onboardingStep) && !ui.companionSetupOpen
  }
  function onboardingOrchestratorActive() {
    return ui.state.selectedTab === 'onboarding' && isOrchestratorOnboardingStep(ui.onboardingStep) && !ui.companionSetupOpen
  }
  function orchestratorSetupValidation(value) {
    const draft = sanitizeOrchestratorDraft(value)
    if (draft.mode === 'model') {
      if (!draft.connectionId) return 'Сначала создайте и проверьте подключение или выберите уже сохранённое.'
      if (!String(draft.model || '').trim()) return 'Выберите или введите model ID мастера.'
    }
    return ''
  }
  function currentOnboardingCompanionValues() {
    const number = (id, fallback) => {
      const parsed = Number(root.querySelector(`#${companionTraitKeyToId(id, 'onboarding')}`)?.value ?? fallback)
      return Number.isFinite(parsed) ? parsed : fallback
    }
    return {
      companionPreset: ui.onboardingDraft.companionPreset || 'balanced',
      criticality: number('criticality', ui.onboardingDraft.criticality ?? 50),
      creativity: number('creativity', ui.onboardingDraft.creativity ?? 50),
      verbosity: number('verbosity', ui.onboardingDraft.verbosity ?? 50),
      initiative: number('initiative', ui.onboardingDraft.initiative ?? 50),
      questionStrictness: number('questionStrictness', ui.onboardingDraft.questionStrictness ?? 70),
      riskTolerance: number('riskTolerance', ui.onboardingDraft.riskTolerance ?? 30),
    }
  }
  function indexLanguages() {
    const raw = ui.state.boot?.indexStatus?.languages
    if (Array.isArray(raw)) return raw.map(item => String(item || '')).filter(Boolean)
    if (raw && typeof raw === 'object') {
      return Object.entries(raw).sort((left, right) => Number(right[1] || 0) - Number(left[1] || 0)).map(([name]) => name)
    }
    return []
  }
  function onboardingAgentTemplates() {
    const seen = new Set()
    return [...(ui.state.boot?.profileTemplates || []), ...(ui.state.boot?.blueprints || [])].filter(item => {
      if (!item?.id || seen.has(item.id)) return false
      seen.add(item.id)
      return true
    })
  }
  function companionSuggestedTemplateId(templates = onboardingAgentTemplates()) {
    if (ui.onboardingDraft.agentTemplateId && templates.some(item => item.id === ui.onboardingDraft.agentTemplateId)) {
      return ui.onboardingDraft.agentTemplateId
    }
    const hay = `${indexLanguages().join(' ')} ${ui.state.workspace || ''}`.toLowerCase()
    const pick = (...ids) => templates.find(item => ids.includes(item.id))
    if (/\b(test|spec|qa)\b/.test(hay)) return pick('tester')?.id || templates[0]?.id || ''
    if (/\b(md|docs?|readme)\b/.test(hay) && !/\b(go|ts|js|py|rs)\b/.test(hay)) return pick('analyst', 'reviewer')?.id || templates[0]?.id || ''
    return pick('developer')?.id || templates[0]?.id || ''
  }
  function firstAgentProposal() {
    const templates = onboardingAgentTemplates()
    const template = templates.find(item => item.id === companionSuggestedTemplateId(templates)) || templates[0]
    const companion = ui.state.boot?.companion || {}
    const brain = onboardingCompanionDraft()
    const model = companion.model || brain.model || template?.primaryModel || template?.model || ''
    const langs = indexLanguages()
    const reasons = []
    if (langs.length) reasons.push(`В индексе видны языки: ${langs.slice(0, 4).join(', ')}.`)
    else reasons.push('Индекс ещё не построен — предлагаю универсальный класс, его можно сменить.')
    if (template) reasons.push(`Роль «${template.name}» даёт ${countOf((template.allowedTools || []).length, 'умение', 'умения', 'умений')} без скрытого расширения прав.`)
    if (brain.mode === 'model' && model) reasons.push(`Primary model возьму из компаньона: ${model}.`)
    else reasons.push('Модель агента можно уточнить на следующем шаге или в конструкторе.')
    return {
      template,
      templates,
      model,
      reasons,
      name: (ui.onboardingDraft.agentName || template?.name || 'Первый агент').trim(),
    }
  }
  function onboardingPrimaryAgent() {
    return hubAgents().find(item => item.id) || hubAgents()[0]
  }
  // Профиль, к которому относится подтверждение: тот же выбор, что и в ростере.
  function currentAgentProfile() {
    const profiles = ui.state.boot?.profiles || []
    return profiles.find(item => item.id === ui.selectedProfileId) || profiles[0]
  }
  
  function skillEquipConfirmHtml() {
    const confirm = ui.pendingSkillEquip
    if (!confirm) return ''
    // Подтверждение permissions показывает список инструментов. Человеку нужен
    // ответ на другой вопрос: что агент начнёт мочь и снимется ли препятствие.
    const deltaBlock = capabilityDeltaHtml(currentAgentProfile(), confirm.skill?.requiredTools || [], [])
    return `<aside class="skill-equip-confirm"><header><strong>Подтвердите permissions</strong><small>${esc(confirm.skill?.name || confirm.skillId)}</small></header><ul>${Object.entries(confirm.permissionDelta || {}).map(([key, value]) => `<li><code>${esc(key)}</code> → <b>${esc(value)}</b></li>`).join('') || '<li>Без расширения permissions</li>'}</ul>${(confirm.requiredTools || []).length ? `<p>Tools: ${confirm.requiredTools.map(esc).join(', ')}</p>` : ''}<footer><button type="button" class="primary" data-action="confirm-equip-skill" data-id="${esc(confirm.skillId)}">Подключить</button><button type="button" class="secondary" data-action="cancel-equip-skill">Отмена</button></footer>${deltaBlock}</aside>`
  }
  function patchOnboardingAgent(patch) {
    const agent = onboardingPrimaryAgent()
    if (!agent?.id) {
      ui.onboardingDraft = { ...ui.onboardingDraft, ...patch }
      persistDraft()
      return false
    }
    const draft = newConstructorDraft({
      ...agent,
      ...patch,
      primaryModel: patch.primaryModel || patch.model || agent.primaryModel || agent.model,
      model: patch.primaryModel || patch.model || agent.primaryModel || agent.model,
    })
    vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(draft) })
    return true
  }
  function firstAgentDraftFromProposal(proposal) {
    const template = proposal?.template
    const profile = template ? newProfile(template) : newProfile()
    const name = (root.querySelector('#onboarding-agent-name')?.value.trim() || ui.onboardingDraft.agentName || proposal?.name || profile.name || '').trim()
    if (name) profile.name = name
    return newConstructorDraft({
      ...profile,
      mission: template?.mission || (template?.goals || [])[0] || template?.description || '',
      primaryModel: proposal?.model || profile.model,
      model: proposal?.model || profile.model,
      blueprintId: template && (ui.state.boot?.blueprints || []).some(item => item.id === template.id) ? template.id : '',
    })
  }
  function acceptFirstAgentProposal() {
    const proposal = firstAgentProposal()
    const name = (root.querySelector('#onboarding-agent-name')?.value.trim() || ui.onboardingDraft.agentName || proposal.name || '').trim()
    ui.onboardingDraft = { ...ui.onboardingDraft, agentName: name, agentTemplateId: proposal.template?.id || ui.onboardingDraft.agentTemplateId }
    persistDraft()
    vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(firstAgentDraftFromProposal(proposal)) })
  }
  // Карта пути показывается только на первом экране и рядом с рейкой шагов.
  // Обе подсвечивали «здесь», но по-разному: рейка — открытый шаг, карта — первый
  // незаконченный. На первом экране рейка говорила «шаг 01, Добро пожаловать», а
  // карта в тот же миг — «текущая глава: Готово». Положение показывает рейка, и
  // показывает верно; карта отвечает на другой вопрос — что уже сделано, — и
  // собственной подсветки «здесь» ей не нужно.
  function onboardingPathHtml() {
    return `<ol class="onboarding-path">${ONBOARDING_CHAPTERS.filter(chapter => chapter.id !== 'wake').map(chapter => {
      // Глава Мастера называется 'master' (см. ONBOARDING_CHAPTERS), а проверялось
      // 'orchestrator' — ни одна ветка не совпадала, и пройденный шаг на первом
      // экране продукта навсегда оставался непройденным. Рядом, в колонке
      // готовности, тот же факт стоял с галочкой: два списка на одном экране
      // говорили о Мастере разное.
      const done = chapter.id === 'companion' ? companionOnboardingFinished()
        : chapter.id === 'master' ? orchestratorOnboardingFinished()
        : chapter.id === 'party' ? hubAgents().length > 0
        : false
      return `<li class="${done ? 'done' : ''}"><b>${esc(chapter.label)}</b><small>${esc(chapter.hint)}</small></li>`
    }).join('')}</ol>`
  }
  function persistOnboardingCompanion(save) {
    const values = currentOnboardingCompanionValues()
    let draft = sanitizeCompanionSetupDraft({
      ...onboardingCompanionDraft(),
      preset: values.companionPreset,
      criticality: values.criticality,
      creativity: values.creativity,
      verbosity: values.verbosity,
      initiative: values.initiative,
      questionStrictness: values.questionStrictness,
      riskTolerance: values.riskTolerance,
    })
    if (root.querySelector('#connection-id, #model')) {
      draft = mergeCompanionConnectionForm(draft)
    }
    writeOnboardingCompanionDraft(draft)
    persistDraft()
    if (!save) return
    vscode.postMessage({
      type: 'saveCompanionConfig',
      config: companionConfigFromDraft(onboardingCompanionDraft()),
    })
  }
  
  function onboarding() {
    const boot = ui.state.boot || {}
    const index = boot.indexStatus || { state: 'not_built' }
    const agents = hubAgents()
    const connections = boot.connections || []
    const step = ONBOARDING_STEPS.some(item => item.id === ui.onboardingStep) ? ui.onboardingStep : ONBOARDING_STEPS[0].id
    const stepIndex = Math.max(0, ONBOARDING_STEPS.findIndex(item => item.id === step))
    const indexReady = index.state === 'ready'
    const prev = ONBOARDING_STEPS[stepIndex - 1]
    const next = ONBOARDING_STEPS[stepIndex + 1]
    const nextStepId = next?.id || step
    const nextLabel = next?.label || 'Сохранить и открыть Мастера'
    // Выход в Чертог — в подвале рядом с «Назад»: это навигация шага, а не
    // строка состояния. В колонке готовности он был третьей кнопкой на экране.
    const leaveButton = isSystemOnboardingStep(step)
      ? '<button type="button" class="secondary" disabled title="Сначала закончите настройку Мастера">В Хаб</button>'
      : '<button type="button" class="secondary" data-action="tab" data-tab="overview">В Чертог</button>'
    const primaryAction = next
      ? `<button type="button" class="primary" data-action="onboarding-step" data-step="${esc(nextStepId)}">${esc(nextLabel)} →</button>`
      : `<button type="button" class="primary" data-action="complete-master-onboarding">${esc(nextLabel)} →</button>`
    const navFooter = `<footer class="onboarding-footer"><div>${prev ? `<button type="button" class="secondary" data-action="onboarding-step" data-step="${esc(prev.id)}">← ${esc(prev.label)}</button>` : `<button type="button" class="secondary" data-action="restart-onboarding">Сбросить</button>`}${leaveButton}</div><div>${primaryAction}</div></footer>`
    const indexQuick = `<aside class="onboarding-index-quick ${indexReady ? 'ready' : ''}"><header><strong>Карта кода</strong><span>${indexReady ? countOf(index.files || 0, 'файл', 'файла', 'файлов') : 'необязательно'}</span></header><p>Быстрая пересборка индекса — без AI-анализа. Экономит токены в квестах.</p><button type="button" class="secondary" data-action="rebuild-index">${indexReady ? 'Перестроить индекс' : index.state === 'stale' ? 'Обновить индекс' : 'Построить индекс'}</button></aside>`
    // Шапка отвечает на «куда я попал», а не «что на этом шаге»: назначение
    // шага и без того стоит под «ШАГ NN / NN» в колонке слева. Раньше здесь
    // печаталось то же самое `why`, и одна и та же фраза встречала человека
    // дважды, а вместе с заголовком панели — трижды.
    const heroCopy = 'Два шага до работы: выберите подключение и правила Мастера. Companion и постоянные агенты настраиваются позже, только когда понадобятся.'
    let panel = ''
    if (step === 'welcome') {
      panel = `<section class="onboarding-panel"><h2>Добро пожаловать в Point Agent Hub</h2><p>Компаньон сопровождает IDE и только рекомендует. Мастер координирует работу. Вы развиваете небольшую постоянную команду, а Point адаптирует её к открытому проекту.</p>${onboardingPathHtml()}${indexQuick}</section>`
    } else if (step === 'companion-choose') {
      const selected = ui.onboardingDraft.companionPreset || 'balanced'
      panel = `<section class="onboarding-panel companion-onboarding"><h2>Выберите компаньона</h2><p>Роль задаёт тон и то, о чём он заговорит первым. Сменить её можно в любой момент.</p>${companionPresetStudioHtml(selected, 'onboarding-companion-preset')}${companionScenePickerHtml(onboardingCompanionDraft())}${companionPersonalityPreviewHtml(onboardingCompanionDraft(), true)}</section>`
    } else if (step === 'companion-config') {
      const draft = onboardingCompanionDraft()
      panel = `<section class="onboarding-panel companion-onboarding"><h2>Настройте компаньона</h2><p>Сначала характер. Дальше мозг компаньона, затем как он распоряжается. К первому агенту перейдём только после обоих.</p><div class="companion-personality-layout">${companionStyleStudioHtml(draft.preset, 'onboarding-companion-preset')}${companionPersonalityControlsHtml(draft, 'onboarding')}${companionPersonalityPreviewHtml(draft)}</div></section>`
    } else if (step === 'companion-brain') {
      const draft = onboardingCompanionDraft()
      const status = companionSetupStatusHtml()
      panel = `<section class="onboarding-panel companion-onboarding"><h2>Мозг компаньона</h2><p>Помощник отвечает только через HTTP API: выберите связь и модель.</p>${status}${companionModeCardsHtml(draft)}${companionConnectionFieldsHtml(draft)}<aside class="companion-safety-strip"><b>Мастер — следующий шаг</b><span>Дальше настроите отдельного системного агента. Компаньон только предлагает; отряд назначает и Flow запускает Мастер — после вашей команды.</span></aside></section>`
    } else if (step === 'orchestrator-choose') {
      const draft = onboardingOrchestratorDraft()
      panel = `<section class="onboarding-panel companion-onboarding orchestrator-onboarding"><h2>Настройте Мастера</h2><p>Выберите стиль планирования. Мастер сам предложит нужных постоянных агентов и временных специалистов в карточке задачи — создавать их сейчас не требуется.</p>${orchestratorPresetStudioHtml(draft.preset)}<div class="companion-personality-layout">${orchestratorPolicyControlsHtml(draft)}${orchestratorPreviewHtml(draft)}</div><aside class="companion-safety-strip"><b>Это последний обязательный шаг</b><span>После сохранения откроется основной чат Мастера. Companion остаётся необязательной функцией наблюдения.</span></aside></section>`
    } else if (step === 'orchestrator-brain') {
      const draft = onboardingOrchestratorDraft()
      const status = companionSetupStatusHtml()
      panel = `<section class="onboarding-panel companion-onboarding orchestrator-onboarding"><h2>Подключение Мастера</h2><p>Начните сразу на встроенном движке Point или выберите сохранённое подключение модели. Секреты остаются в SecretStorage.</p>${status}${orchestratorModeCardsHtml(draft)}${draft.mode === 'model' ? orchestratorConnectionFieldsHtml(draft) : `<section class="companion-local-ready"><span>✓</span><div><h3>Движок Point готов сразу</h3><p>Он детерминированно собирает карточку запуска и не требует ключа.</p><small>Подключение модели можно добавить сейчас или позже в настройках Мастера.</small></div></section>`}<aside class="companion-safety-strip"><b>Дальше — правила Мастера</b><span>На втором и последнем шаге выберете глубину плана, параллельность и строгость проверки.</span></aside></section>`
    } else if (step === 'first-agent') {
      const proposal = firstAgentProposal()
      const templates = proposal.templates
      const selectedTemplate = proposal.template
      const selectedId = selectedTemplate?.id || ''
      const tools = selectedTemplate?.allowedTools || []
      const canAccept = Boolean(selectedTemplate || (ui.onboardingDraft.agentName || '').trim())
      panel = `<section class="onboarding-panel companion-onboarding"><h2>Подключите первого специалиста</h2><p>Компаньон предлагает переносимый основной профиль. Point создаст отдельную адаптацию текущего проекта; Мастер назначит специалиста только после запуска задачи.</p><aside class="companion-inline"><header><span>КОМПАНЬОН</span><small>Предложение, не запуск</small></header><article class="companion-agent-proposal"><strong>${esc(proposal.name)}</strong><p>${esc(selectedTemplate?.roleDescription || selectedTemplate?.description || 'Профиль не выбран')}</p><small>${esc(selectedTemplate?.goals?.[0] || selectedTemplate?.description || '')}</small><ul>${proposal.reasons.map(item => `<li>${esc(item)}</li>`).join('')}</ul><div class="companion-proposal-tools">${tools.map(name => `<em>${esc(toolName(name))}</em>`).join('') || '<em>инструменты из профиля</em>'}</div><small>Модель: ${esc(proposal.model || 'настроим позже')}</small></article><label>Имя специалиста<input id="onboarding-agent-name" maxlength="200" value="${esc(ui.onboardingDraft.agentName || proposal.name)}" placeholder="Например, SAGE-7"></label>${templates.length ? `<details class="companion-template-more"><summary>Все основные профили</summary><div class="template-picker compact"><div>${templates.map(item => `<button type="button" class="${item.id === selectedId ? 'selected' : ''}" data-action="onboarding-agent-template" data-template="${esc(item.id)}"><b>${esc(item.name)}</b><small>${esc(item.description || item.roleDescription || '')}</small></button>`).join('')}</div></div></details>` : ''}<footer><button type="button" class="primary" data-action="onboarding-create-agent" ${canAccept ? '' : 'disabled'}>Подключить</button><button type="button" class="secondary" data-action="onboarding-edit-agent">Настроить профиль</button><button type="button" class="secondary" data-action="onboarding-agent-cycle" ${templates.length < 2 ? 'disabled' : ''}>Другой профиль</button></footer></aside></section>`
    } else if (step === 'model-connection') {
      const providers = boot.providerCatalog || []
      const agent = onboardingPrimaryAgent()
      const selectedId = ui.onboardingDraft.connectionId || ''
      const connectionCards = connections.length
        ? `<div class="companion-connection-list">${connections.slice(0, 8).map(item => `<button type="button" class="companion-connection-card ${item.id === selectedId ? 'selected' : ''}" data-action="onboarding-apply-connection" data-id="${esc(item.id)}">${connectionOrbHtml(item.status)}<div><strong>${esc(item.displayName || item.provider)}</strong><small>${esc(item.presetId || item.provider)} · ${esc(item.baseUrl || 'адрес из пресета')}</small></div><em>${esc(connectionStatusLabels[item.status] || item.status || 'НЕИЗВЕСТНО')}</em></button>`).join('')}</div>`
        : '<p class="muted">Сохранённых подключений пока нет — добавьте ниже через HTTP API.</p>'
      panel = `<section class="onboarding-panel"><h2>Модель и подключение</h2><p>Ключ хранится только в SecretStorage, а при запросе уходит выбранному провайдеру. Этот шаг можно пропустить — специалист заработает после связи.</p>${agent ? `<p class="muted">Специалист: <strong>${esc(agent.name)}</strong> · ${esc(agent.primaryModel || agent.model || 'модель не задана')}</p>` : '<p class="muted">Сначала подключите специалиста — тогда связь запишется в его проектную адаптацию.</p>'}${connectionCards}<div class="onboarding-providers">${featuredProviderChips(providers).filter(item => !['cursor-cli', 'codex-cli', 'claude-code-cli'].includes(item.kind)).map(item => `<button type="button" data-action="onboarding-pick-provider" data-provider="${esc(item.kind)}" data-preset="${esc(item.id)}" title="${esc(item.description || '')}"><strong>${esc(item.name)}</strong><small>${item.id === 'llmux' || item.id === 'custom' ? 'компания' : item.local ? 'локально' : 'облако'}</small></button>`).join('')}</div>${connectionFormHtml()}</section>`
    } else {
      panel = `<section class="onboarding-panel onboarding-complete-panel"><h2>Гильдия готова</h2><p>Центр командования — квест, отряд, компаньон и активные запуски на одном экране. Компаньон по-прежнему только рекомендует.</p><ul class="onboarding-facts"><li><i>✓</i> Компаньон: ${esc(ui.onboardingDraft.companionPreset || 'balanced')}</li><li><i>✓</i> Распоряжение: ${esc(ui.onboardingDraft.orchestratorPreset || boot.orchestrator?.preset || 'conductor')}</li><li><i>${agents.length ? '✓' : '·'}</i> Агентов: ${agents.length}</li><li><i>${connections.length ? '✓' : '·'}</i> Подключений: ${connections.length}</li><li><i>${indexReady ? '✓' : '·'}</i> Индекс: ${indexReady ? 'готов' : 'можно позже'}</li></ul><div class="onboarding-next-actions"><button type="button" class="primary" data-action="complete-onboarding">Открыть обзор гильдии</button><button type="button" class="secondary" data-action="tab" data-tab="quests">Поставить первый квест</button></div>
        <div class="onboarding-later">
          <span>Необязательное — когда понадобится</span>
          <div>
            <button type="button" class="secondary" data-action="tab" data-tab="skills">Навыки проекта</button>
            <button type="button" class="secondary" data-action="tab" data-tab="agents">Умения и разрешения агента</button>
          </div>
          <small>Первый специалист уже получил навыки и инструменты основного профиля — это дальнейшее развитие, а не условие запуска.</small>
        </div>${indexQuick}<button type="button" class="secondary" data-action="restart-onboarding">Пройти онбординг заново</button></section>`
    }
    // Один список готовности на экран. Раньше их было три: карта пути, короткий
    // перечень в центре и эта колонка — причём «ядро» жило только в центральном,
    // а «проект» и «агенты» повторялись дважды.
    const coreRunning = ui.state.service?.state === 'running'
    const orchestratorDraft = onboardingOrchestratorDraft()
    const connectionReady = orchestratorDraft.mode === 'local' || Boolean(orchestratorDraft.connectionId && orchestratorDraft.model)
    const checklist = [
      { ready: coreRunning, label: 'Ядро', detail: coreRunning ? 'работает' : 'ожидает запуска' },
      { ready: connectionReady, label: 'Подключение', detail: orchestratorDraft.mode === 'local' ? 'движок Point' : connectionReady ? orchestratorDraft.model : 'нужно выбрать', step: 'orchestrator-brain' },
      { ready: orchestratorOnboardingFinished(), label: 'Мастер', detail: orchestratorOnboardingFinished() ? (ORCHESTRATOR_PRESETS.find(item => item.id === (boot.orchestrator?.preset || ui.onboardingDraft.orchestratorPreset))?.label || 'готов') : 'осталось сохранить', step: 'orchestrator-choose' },
    ]
    return shell(`<main class="onboarding onboarding-wizard">
      <header class="onboarding-hero"><div><span>ПЕРВОЕ ПРОБУЖДЕНИЕ</span><h1>Point Agent Hub</h1><p>${esc(heroCopy)}</p></div><div class="onboarding-rune">✦</div></header>
      <div class="onboarding-readiness"><b>ГОТОВНОСТЬ<em>${checklist.filter(item => item.ready).length} / ${checklist.length}</em></b>${checklist.map(item => { const body = `<i>${item.ready ? '✓' : '·'}</i><span><strong>${esc(item.label)}</strong><small>${esc(item.detail)}</small></span>`; const cls = item.ready ? 'ready' : ''; return item.step   ? `<button type="button" class="${cls}" data-action="onboarding-step" data-step="${esc(item.step)}" title="${esc(item.label)} — ${esc(item.detail)}">${body}</button>`   : `<span class="${cls}" title="${esc(item.label)} — ${esc(item.detail)}">${body}</span>`;}).join('')}</div>
      <div class="onboarding-layout">${onboardingStepNav(step)}<div class="onboarding-main">${panel}${navFooter}</div></div>
    </main>`)
  }
  
  function blueprintDiffValue(value) {
    if (value === undefined || value === null || value === '') return '—'
    if (Array.isArray(value)) return value.length ? value.join('\n') : '—'
    if (typeof value === 'object') return Object.keys(value).length ? JSON.stringify(value, null, 2) : '—'
    return String(value)
  }
  function blueprintSyncPreviewHtml(agentId) {
    const preview = ui.blueprintSyncPreview
    if (!preview || preview.projectAgentId !== agentId) return ''
    const direction = ui.blueprintSyncDirection
    if (preview.loading) return '<aside class="blueprint-sync-preview"><p class="muted">Собираю diff сохранённой конфигурации…</p></aside>'
    const updateBlueprint = direction === 'update-blueprint'
    const fields = Array.isArray(preview.fields) ? preview.fields : []
    const rows = fields.map(field => `<article><strong>${esc(field.label || field.key)}</strong><div><span><small>PROJECT AGENT</small><pre>${esc(blueprintDiffValue(field.agentValue))}</pre></span><b>${updateBlueprint ? '→' : '←'}</b><span><small>BLUEPRINT</small><pre>${esc(blueprintDiffValue(field.blueprintValue))}</pre></span></div></article>`).join('')
    const projectRules = preview.projectOnly?.projectRules || []
    return `<aside class="blueprint-sync-preview"><header><div><strong>${updateBlueprint ? 'Улучшить основной профиль' : 'Обновить адаптацию из основного профиля'}</strong><small>${esc(preview.agentName || '')} ↔ ${esc(preview.blueprintName || '')}</small></div><em>${fields.length} изм.</em></header><p>${updateBlueprint ? 'Изменения текущего проекта станут новой переносимой версией специалиста. Другие проекты не изменятся без отдельной синхронизации.' : 'Основной профиль обновит локальную адаптацию. Опыт, статистика и проектные правила сохранятся.'}</p>${rows || '<p class="muted">Проектная адаптация уже совпадает с основным профилем.</p>'}${projectRules.length ? `<details><summary>Только в этом проекте · не переносится</summary><pre>${esc(projectRules.join('\n'))}</pre></details>` : ''}<small>Diff построен по последней сохранённой конфигурации. Несохранённые поля конструктора сначала примените кнопкой «Сохранить для проекта».</small><footer>${preview.hasChanges ? `<button type="button" class="primary" data-action="confirm-blueprint-sync">${updateBlueprint ? 'Улучшить основной профиль' : 'Обновить адаптацию'}</button>` : ''}<button type="button" class="secondary" data-action="cancel-blueprint-sync">${preview.hasChanges ? 'Отмена' : 'Закрыть'}</button></footer></aside>`
  }
  function agentConstructor() {
    const draft = ui.constructorDraft || newConstructorDraft()
    const step = CONSTRUCTOR_STEPS.some(item => item.id === ui.constructorStep) ? ui.constructorStep : 'identity'
    const tools = ui.state.boot?.toolCatalog || []
    const skills = ui.state.boot?.skills || []
    const templates = [...(ui.state.boot?.blueprints || []), ...(ui.state.boot?.profileTemplates || [])]
    const enabled = new Set(draft.allowedTools || [])
    const creating = !draft.id
    const hub = hubModeAvailable()
    const compiledPrompt = step === 'review' ? compiledPromptPreviewHtml(draft) : ''
    const networkHosts = Object.entries(draft.toolPolicies || {})
      .filter(([key, value]) => key.toLowerCase().startsWith('network:') && value === 'ALLOW')
      .map(([key]) => key.slice('network:'.length))
    const networkPolicy = networkHosts.length || draft.toolPolicies?.network === 'ALLOWLIST' ? 'ALLOWLIST' : 'DENY'
    const identityPanel = `<section class="profile-config-block profile-step" data-step-panel="identity"><header><span>01</span><div><strong>Личность</strong><small>Имя и характер агента</small></div></header><label>Имя<input id="constructor-name" maxlength="200" value="${esc(draft.name)}" placeholder="SAGE-7" required></label><label>Характер / тон<textarea id="constructor-personality" rows="3" maxlength="4096" placeholder="Спокойный, точный, задаёт уточняющие вопросы">${esc(draft.personality)}</textarea></label></section>`
    const rolePanel = `<section class="profile-config-block profile-step" data-step-panel="role"><header><span>02</span><div><strong>Роль</strong><small>Экспертиза и зона ответственности</small></div></header><label>Описание роли<textarea id="constructor-role" rows="4" maxlength="4096" placeholder="В чём агент эксперт">${esc(draft.roleDescription)}</textarea></label></section>`
    const missionPanel = `<section class="profile-config-block profile-step" data-step-panel="mission"><header><span>03</span><div><strong>Миссия</strong><small>Зачем агент нужен отряду</small></div></header><label>Миссия<textarea id="constructor-mission" rows="3" maxlength="4096" placeholder="Главная миссия агента">${esc(draft.mission)}</textarea></label><label>Цели · по одной на строку<textarea id="constructor-goals" rows="4" maxlength="16384">${esc((draft.goals || []).join('\n'))}</textarea></label></section>`
    const rulesPanel = `<section class="profile-config-block profile-step" data-step-panel="rules"><header><span>04</span><div><strong>Правила</strong><small>Жёсткие ограничения</small></div></header><label>Правила<textarea id="constructor-rules" rows="5" maxlength="32768" placeholder="Одно правило на строку">${esc((draft.rules || []).join('\n'))}</textarea></label><label>Доп. инструкции<textarea id="constructor-system-prompt" rows="4" maxlength="65536" placeholder="Стиль рассуждения, формат ответа">${esc(draft.systemPrompt)}</textarea></label></section>`
    // Тот же общий выбор «подключение -> модель», что у компаньона, Мастера и
    // карточки персонажа. Здесь была последняя своя копия: сетка провайдеров,
    // отдельное поле адреса и свой список моделей. Адрес и ключ принадлежат
    // подключению, поэтому спрашивать их у агента незачем.
    const brainPanel = `<section class="profile-config-block profile-step" data-step-panel="brain"><header><span>05</span><div><strong>Мозг</strong><small>Подключение и модель</small></div></header>${companionSuggestionChipsHtml('brain', draft)}${modelChoiceHtml({ connectionId: draft.connectionId, model: draft.primaryModel || draft.model, contextWindowTokens: draft.contextWindowTokens, temperature: draft.temperature, maxOutputTokens: draft.maxOutputTokens, reasoningEffort: draft.reasoningEffort })}<label>Запасные модели · по одной на строку<textarea id="constructor-fallback-models" rows="3" placeholder="fallback-model-1&#10;fallback-model-2">${esc((draft.fallbackModels || []).join('\n'))}</textarea><small>Берутся только при тайм-ауте, недоступности провайдера или исчерпании лимита.</small></label>${connectionDrawerHtml(!(ui.state.boot?.connections || []).length)}</section>`
    const skillsPanel = `<section class="profile-config-block profile-step" data-step-panel="skills"><header><span>06</span><div><strong>Навыки</strong><small>Модули из каталога</small></div></header><p class="skill-form-hint">Отметка навыка добавляет нужные ему умения в список разрешённых. Сам навык запрет не обходит. Длинные инструкции агент читает отдельным вызовом read_skill.</p>${constructorSkillGapHtml(draft)}${skills.length ? `<div class="constructor-skill-grid">${skills.slice(0, 24).map(item => {
      const tools = Array.isArray(item.requiredTools) ? item.requiredTools : []
      const selected = (draft.skillIds || []).includes(item.id)
      const deprecated = item.configuration?.lifecycleStatus === 'deprecated'
      return `<label class="tool-toggle ${selected ? 'is-on' : ''}${deprecated ? ' is-deprecated' : ''}"><input type="checkbox" name="constructor-skill" value="${esc(item.id)}" ${selected ? 'checked' : ''} ${deprecated && !selected ? 'disabled' : ''}><span><strong>${esc(item.name || item.id)}${deprecated ? ' · устарел' : ''}</strong><small>${esc(item.description || '')}${tools.length ? ` · ${tools.join(', ')}` : ''}</small></span></label>`
    }).join('')}</div>` : '<p class="muted">Каталог навыков пуст — ядро подставит базовые при первом запуске.</p>'}</section>`
    const toolsPanel = `<section class="tool-section profile-config-block profile-step" data-step-panel="tools"><header><span>07</span><div><strong>Инструменты</strong><small>Список разрешённых умений и правило вызова</small></div><b>${enabled.size} / ${tools.length}</b></header>${companionSuggestionChipsHtml('tools', draft)}<div class="permission-presets">${TOOL_PRESETS.map(item => `<button type="button" class="${activeToolPresetId(draft) === item.id ? 'on' : ''}" data-action="constructor-tool-preset" data-preset="${esc(item.id)}"><b>${esc(item.label)}</b><small>${esc(item.hint)}</small></button>`).join('')}</div><div class="tool-policy-legend"><span>Опасность — насколько рискованно умение</span><span>Правило — разрешить, спросить или запретить</span><span>Запрет действует без подтверждения</span></div><div class="tool-policy-grid">${tools.map(tool => {
      const risk = normalizeToolRisk(tool.risk)
      const policy = ['ALLOW', 'ASK', 'DENY'].includes(String(draft.toolPolicies?.[tool.name] || '').toUpperCase()) ? String(draft.toolPolicies[tool.name]).toUpperCase() : defaultToolPolicyForRisk(risk)
      const on = enabled.has(tool.name)
      return `<article class="tool-policy-row risk-${esc(risk)} ${on ? 'is-on' : ''}"><label class="tool-toggle risk-${esc(risk)} ${on ? 'is-on' : ''}"><input type="checkbox" name="constructor-tool" value="${esc(tool.name)}" ${on ? 'checked' : ''}><span><strong>${esc(tool.displayName)}</strong><small>${esc(tool.description)}</small></span></label><em>${esc(TOOL_RISK_LABELS[risk] || risk)}</em><select name="constructor-tool-policy" data-tool="${esc(tool.name)}" ${on ? '' : 'disabled'}><option value="ALLOW" ${policy === 'ALLOW' ? 'selected' : ''}>Разрешить</option><option value="ASK" ${policy === 'ASK' ? 'selected' : ''}>Спросить</option><option value="DENY" ${policy === 'DENY' ? 'selected' : ''}>Запретить</option></select></article>`
    }).join('')}</div></section>`
    const memoryPanel = `<section class="profile-config-block profile-step" data-step-panel="memory"><header><span>08</span><div><strong>Проектная адаптация</strong><small>Контекст только этого репозитория</small></div></header><p class="skill-form-hint">Эти правила и память не попадут в основной профиль специалиста и не перенесутся в другие проекты.</p><label>Ограничения рабочей папки<textarea id="constructor-constraints" rows="3" maxlength="8192">${esc((draft.constraints || []).join('\n'))}</textarea></label><label>Проектные инструкции<textarea id="constructor-project-rules" rows="4" maxlength="16384" placeholder="Специфика репозитория">${esc((draft.projectRules || []).join('\n'))}</textarea></label></section>`
    const permissionsPanel = `<section class="profile-config-block profile-step" data-step-panel="permissions"><header><span>09</span><div><strong>Разрешения</strong><small>Лимиты и подтверждения</small></div></header>${companionSuggestionChipsHtml('permissions', draft)}<div class="settings-grid"><label>Макс. шагов<input id="constructor-max-steps" type="number" min="1" max="100" value="${Number(draft.maxSteps || 30)}"></label><label>Тайм-аут, сек<input id="constructor-timeout" type="number" min="1" max="3600" value="${Number(draft.maxDurationSeconds || 600)}"></label></div><label>Подтверждения<select id="constructor-approval-mode"><option value="safe" ${draft.approvalMode === 'safe' ? 'selected' : ''}>Опасные действия</option><option value="always" ${draft.approvalMode === 'always' ? 'selected' : ''}>Каждое умение</option></select></label><div class="settings-grid"><label>Исходящая сеть, TLS<select id="constructor-network-policy"><option value="DENY" ${networkPolicy === 'DENY' ? 'selected' : ''}>Запрещена · по умолчанию</option><option value="ALLOWLIST" ${networkPolicy === 'ALLOWLIST' ? 'selected' : ''}>Точный список хостов</option></select></label><label>Разрешённые адреса и порты<textarea id="constructor-network-hosts" rows="3" placeholder="registry.npmjs.org:443&#10;proxy.golang.org:443">${esc(networkHosts.join('\n'))}</textarea><small>По одному точному публичному имени и порту на строку; без масок, числовых адресов и поддоменов. Без порта берётся TLS 443.</small></label></div></section>`
    const reviewPanel = `<section class="profile-config-block profile-step" data-step-panel="review"><header><span>10</span><div><strong>Обзор</strong><small>Сводка и скомпилированный промпт</small></div></header>${blueprintSyncPreviewHtml(draft.id)}${constructorSkillGapHtml(draft)}<div class="constructor-review-grid"><span><small>ИМЯ</small><b>${esc(draft.name || '—')}</b></span><span><small>РОЛЬ</small><b>${esc(draft.roleDescription ? 'задана' : '—')}</b></span><span><small>МОДЕЛЬ</small><b>${esc(draft.primaryModel || draft.model || '—')}</b></span><span><small>УМЕНИЯ</small><b>${enabled.size}</b></span><span><small>НАВЫКИ</small><b>${(draft.skillIds || []).length}</b></span><span><small>ХРАНЕНИЕ</small><b>${hub ? 'чертёж и проектный агент' : 'профили · устаревший путь'}</b></span></div>${compiledPrompt}<details class="constructor-prompt-preview"><summary>Подробно · JSON</summary><pre>${esc(JSON.stringify(hub ? constructorToProjectAgent(draft) : constructorToProfile(draft), null, 2))}</pre></details></section>`
    const panels = { identity: identityPanel, role: rolePanel, mission: missionPanel, rules: rulesPanel, brain: brainPanel, skills: skillsPanel, tools: toolsPanel, memory: memoryPanel, permissions: permissionsPanel, review: reviewPanel }
    const stepIndex = Math.max(0, CONSTRUCTOR_STEPS.findIndex(item => item.id === step))
    const prev = CONSTRUCTOR_STEPS[stepIndex - 1]
    const next = CONSTRUCTOR_STEPS[stepIndex + 1]
    const reviewActions = step === 'review'
      ? `<button type="button" class="primary" data-action="save-constructor">${creating ? 'Подключить к проекту' : 'Сохранить для проекта'}</button>${hub && draft.id && draft.blueprintId ? `<button type="button" class="secondary" data-action="update-blueprint">Улучшить основной профиль</button>` : ''}${hub && draft.id ? `<button type="button" class="secondary" data-action="apply-blueprint">Обновить из профиля</button>` : ''}${hub && draft.id ? `<button type="button" class="danger-button" data-action="disband-agent" data-id="${esc(draft.id)}">Убрать из проекта</button>` : ''}`
      : `<button type="button" class="primary" data-action="constructor-advance" data-step="${esc(next?.id || 'review')}" data-dir="next">${esc(next?.label || 'Обзор')} →</button>`
    const footer = `<div class="profile-step-footer create-flow-footer"><div class="profile-step-left">${prev ? `<button type="button" class="secondary" data-action="constructor-advance" data-step="${esc(prev.id)}" data-dir="back">← ${esc(prev.label)}</button>` : '<button type="button" class="secondary" data-action="close-agent-constructor">← К команде</button>'}</div><div class="profile-step-right">${reviewActions}</div></div>`
    const templateBar = templates.length ? `<details class="hire-drawer"><summary>Основные профили / с нуля <span>${templates.length}</span></summary><div class="create-class-grid">${templates.slice(0, 8).map(item => `<button type="button" data-action="constructor-template" data-template="${esc(item.id)}"><b>${esc(item.name)}</b><small>${esc(item.roleDescription || item.description || '')}</small></button>`).join('')}<button type="button" data-action="constructor-scratch">＋ Новый профиль</button></div></details>` : ''
    return shell(`<main class="settings agent-constructor"><div class="section-title"><span>ПРОФИЛЬ СПЕЦИАЛИСТА</span><em>${hub ? 'основа + проект' : 'legacy'}</em></div><button class="secondary roster-back" type="button" data-action="close-agent-constructor">← К команде</button>${templateBar}${constructorStepNav(step)}<form id="constructor-form" class="profile-wizard-form" novalidate>${Object.entries(panels).map(([id, html]) => html.replace('profile-step"', `profile-step${id === step ? '' : ' is-hidden'}"`)).join('')}${footer}</form></main>`)
  }
  
  function conversation() {
    const details = ui.state.details
    if (!details) {
      const hasProfiles = (ui.state.boot?.profiles || []).length > 0
      return `<div class="empty quest-board"><div class="orbit small"><span>✦</span></div><span class="quest-label">ДОСКА КВЕСТОВ</span><h3>Новый квест ждёт брифинга</h3><p>Заполните задачу, цель и критерии. «Разведка» обязательна перед стартом — покажет умения, подтверждения и условия завершения без вызова модели.</p><div class="examples"><button data-example="Объясни архитектуру проекта и основные точки входа." data-goal="Понятная карта системы для нового разработчика" data-criteria="Перечислены точки входа и ключевые пакеты&#10;Выводы опираются на конкретные файлы" data-constraints="Не менять файлы&#10;Не запускать команды"><span>Разведка</span>Объяснить архитектуру</button><button data-example="Найди потенциальную ошибку и предложи минимальное исправление." data-goal="Минимальный diff, который устраняет дефект" data-criteria="Указаны файл и сценарий отказа&#10;После правки проходит узкий тест или объясняется, почему тест невозможен" data-constraints="Не трогать нерелевантные модули"><span>Охота</span>Найти ошибку</button><button data-example="Запусти тесты проекта и объясни результат." data-goal="Фактический результат проверки, а не предположение" data-criteria="Выполнена распознаваемая команда теста/сборки&#10;Exit code и краткий разбор зафиксированы" data-constraints="Не менять код без отдельного подтверждения"><span>Испытание</span>Проверить тесты</button></div>${hasProfiles?'':`<button type="button" class="primary empty-cta" data-action="tab" data-tab="agents">Сначала наймите персонажа →</button>`}</div>`
    }
    const attached = details.run.contextItems || []
    const head = `<article class="message user"><small>СВИТОК КВЕСТА</small><p>${esc(details.run.task)}</p>${attached.length?`<div class="run-context">${attached.map(item=>`<span title="${esc(item.path || item.label)}">${item.kind==='workspace_file'?'▤':'¶'} ${esc(item.label)}${item.truncated?' · сокращено':''}</span>`).join('')}</div>`:''}</article>`
    const transcript = agentWorkTranscriptHtml(details, { limit: 240 })
    const snapshot = details.run.configurationSnapshot
    const capturedProfile = snapshot?.profile
    const snapshotCard = snapshot?.schemaVersion ? `<details class="run-snapshot"><summary>Снаряжение квеста · ${esc(capturedProfile?.name || details.run.profileId)}</summary><div><span>Модель</span><strong>${esc(capturedProfile?.model || details.run.model)}</strong><span>Провайдер</span><strong>${esc(capturedProfile?.provider || details.run.provider)}</strong><span>Умения</span><strong>${esc((capturedProfile?.allowedTools || []).map(toolName).join(', ') || 'нет')}</strong><span>Лимиты</span><strong>${Number(capturedProfile?.maxSteps) > 0 ? countOf(capturedProfile.maxSteps, 'ход', 'хода', 'ходов') : 'ходы без лимита'} · ${Number(capturedProfile?.maxDurationSeconds) > 0 ? `${esc(capturedProfile.maxDurationSeconds)} сек.` : 'время без лимита'}</strong><small>Неизменяемый снимок Point ${esc(snapshot.applicationVersion)} · ${formatDateTime(snapshot.capturedAt)}</small></div></details>` : `<div class="legacy-snapshot">Старая глава: доступна только базовая конфигурация модели.</div>`
    return `<div class="hall-quest-split"><div class="hall-quest-main">${pendingDecisionsBanner(details)}${activeQuestCard(details)}${completionProofHtml(details)}${runQualityOutcomeHtml(details)}<div class="run-heading">${status(details.run.status)}<span>ход ${details.run.step}</span>${details.run.status==='waiting_approval'?'<span class="run-heading-hint">подтверждения ниже</span>':''}</div>${diagnosticsCard(details.diagnostics)}${snapshotCard}<div class="conversation">${head}${transcript}</div></div>${questAsideHtml(details)}</div>`
  }

  function agentRunPreviewMarkup() {
    if(ui.agentRunPreviewStatus==='loading')return '<section class="agent-preflight loading-inline">Собираем точный запуск без обращения к модели…</section>'
    if(ui.agentRunPreviewError)return `<section class="agent-preflight agent-preflight-error"><strong>Запуск не прошёл проверку</strong><span>${esc(ui.agentRunPreviewError)}</span></section>`
    if(!ui.agentRunPreview){
      if(!ui.taskDraft.trim()) return '<section class="agent-preflight idle"><strong>Разведка перед стартом</strong><span>Заполните брифинг и нажмите «Разведка» — увидите точный промпт, умения, подтверждения и условия завершения без вызова модели.</span></section>'
      return '<section class="agent-preflight idle nudge"><strong>Брифинг готов к разведке</strong><span>Проверьте запуск перед «Принять квест»: точный промпт, умения и доказательства готовности. Без разведки старт заблокирован.</span><button type="button" class="secondary" data-action="preview-run">Запустить разведку</button></section>'
    }
    const preview=ui.agentRunPreview
    const approvalTools=(preview.tools||[]).filter(tool=>tool.requiresApproval)
    const tokens=preview.tokens||{}
    const tokenSummary=tokens.availableInput?`${Number(tokens.total||0).toLocaleString('ru-RU')} / ${Number(tokens.availableInput).toLocaleString('ru-RU')} токенов входа`:`${Number(tokens.total||0).toLocaleString('ru-RU')} токенов`
    const contextStat=tokens.contextWindow?`<span><b>${Number(tokens.contextWindow).toLocaleString('ru-RU')}</b> окно модели</span>`:''
    const completion=preview.completion
    const evidenceLabels={test:'тест',build:'сборка',lint:'линтер',static_analysis:'статический анализ'}
    const completionTitle=completion?.blockingConfigurationIssue?'Квест не сможет подтвердить готовность':completion?.explicitVerification?'Проверка обязательна до финала':completion?.fileChangesRequireVerification?'Проверка обязательна после изменений':'Дополнительная проверка не задана'
    const completionText=completion?.blockingConfigurationIssue?'Разрешите агенту «Запуск команд» или уберите обязательный критерий проверки.':completion?.verificationToolAvailable?`Принимается только успешный результат: ${(completion.acceptedEvidence||[]).map(item=>evidenceLabels[item]||item).join(', ')}. Голословный финал получит ${countOf(completion.correctionEpisodes||1, 'попытку', 'попытки', 'попыток')} исправления.`:'Агент сможет завершить только текстовую задачу без обязательного запуска тестов.'
    const completionCard=completion?`<div class="agent-preflight-completion ${completion.blockingConfigurationIssue?'blocked':''}"><span>УСЛОВИЯ ЗАВЕРШЕНИЯ</span><strong>${esc(completionTitle)}</strong><small>${esc(completionText)}</small></div>`:''
    return `<section class="agent-preflight ready"><header><div><strong>✓ Запуск проверен</strong><small>${esc(preview.profile?.name)} · ${esc(preview.profile?.model)} · ${esc(tokenSummary)}</small></div><span title="${esc(preview.fingerprint)}">${esc((preview.fingerprint||'').slice(7,19))}</span></header><div class="agent-preflight-stats"><span><b>${preview.tools?.length||0}</b> инструментов</span><span><b>${approvalTools.length}</b> с подтверждением</span><span><b>${preview.context?.items?.length||0}</b> вложений</span><span><b>${esc(preview.profile?.maxSteps||0)}</b> шагов</span>${contextStat}</div>${completionCard}<div class="agent-preflight-tools">${(preview.tools||[]).map(tool=>`<span class="${[tool.requiresApproval?'approval-required':'',tool.providesVerification?'verification':''].filter(Boolean).join(' ')}"><b>${esc(tool.displayName||tool.definition?.name)}</b><small>${tool.requiresApproval?'подтверждение':'автоматически'}${tool.providesVerification?' · доказательство':''}</small></span>`).join('')||'<em>Только текстовый ответ</em>'}</div><details><summary>Точная системная инструкция</summary><pre>${esc(preview.systemMessage)}</pre></details><details><summary>JSON-схемы инструментов</summary><pre>${esc(JSON.stringify((preview.tools||[]).map(tool=>tool.definition),null,2))}</pre></details>${(preview.warnings||[]).map(warning=>`<p>⚠ ${esc(warning)}</p>`).join('')}<footer>Контрольный отпечаток привязан к задаче, профилю, инструментам и содержимому вложений. Если они изменятся, локальное ядро потребует новую проверку.</footer></section>`
  }
  
  function composerActionsHtml(profile, active, cursor) {
    const completionBlocked = ui.agentRunPreview?.completion?.blockingConfigurationIssue === true
    const run = ui.state.details?.run
    if (active && run?.id) {
      // Same control strip as overview: pause/resume/extend, message, forbid.
      const execution = (ui.state.boot?.executions || []).find(item => item.runId === run.id || item.id === run.executionId)
      return execControlsHtml(execution
        ? { ...execution, status: run.status, runId: run.id }
        : { id: run.id, runId: run.id, status: run.status, legacy: true })
    }
    if (active) return `<button type="button" class="stop" data-action="${ui.cursorRunActive ? 'cancel-cursor' : 'cancel'}" data-id="${esc(ui.state.details?.run?.id || '')}"><span aria-hidden="true">■</span> Отозвать</button>`
    if (!profile) return '<button type="button" class="send" disabled><span>Сначала наймите агента</span></button>'
    if (cursor) return `<button type="button" class="send cursor-launch" data-action="launch-cursor"><span>${ui.state.cursorRuntime?.available && ui.state.cursorRuntime?.authenticated ? 'Запустить Cursor Agent' : 'Открыть Cursor Agent'}</span><span aria-hidden="true">↗</span></button>`
    return `<button type="button" class="preflight-button" data-action="preview-run" title="Показать точную инструкцию, умения и токены"><span aria-hidden="true">✓</span> Разведка</button><button type="submit" class="send" ${completionBlocked?'disabled':''} title="${completionBlocked?'Сначала разрешите запуск команд или измените критерии':'Начать квест'}"><span>${completionBlocked?'Исправьте разрешения':'Принять квест'}</span><span aria-hidden="true">→</span></button>`
  }
  
  function chat() {
    const profiles = hubModeAvailable() ? hubAgents() : (ui.state.boot?.profiles || []).map(normalizeRunnableAgent)
    const selectedIsAvailable = profiles.some(item => item.id === ui.selectedProfileId)
    if (!selectedIsAvailable) {
      // quickChatDefaultProfileId historically points at a legacy profile
      // (most often Cursor). Once the Hub roster arrives, that id may no
      // longer belong to the list rendered by this composer. Never write an
      // unavailable default back into the selection: a background boot update
      // would otherwise switch a prepared Point run to Cursor mid-preview.
      const preferredIds = [
        ui.state.boot?.defaultProfileId,
        profiles[0]?.id,
      ]
      ui.selectedProfileId = preferredIds.find(id => id && profiles.some(item => item.id === id)) || ''
    }
    const profile = profiles.find(item => item.id === ui.selectedProfileId) || profiles[0]
    const active = ui.cursorRunActive || runIsActiveNow(ui.state.details?.run)
    const cursor = false
    const readiness = profileReadiness(profile)
    const readinessTip = !active && profile && !readiness.ready
      ? `<aside class="readiness-banner compact"><span>!</span><div><strong>Старт заблокирован · персонаж не готов</strong><small>${esc(readiness.issues[0])}</small></div><button type="button" class="secondary" data-action="fix-profile-step" data-step="${esc(firstUnreadinessStep(profile))}">Исправить →</button></aside>`
      : ''
    const briefTip = questBriefGuidanceHtml(active, cursor)
    const emptyRoster = !profiles.length
      ? `<aside class="readiness-banner"><span>✦</span><div><strong>Сначала наймите персонажа</strong><small>Без карточки квест не стартует. Выберите класс — модель и умения подставятся.</small></div><button type="button" class="primary" data-action="tab" data-tab="agents">К найму →</button></aside>`
      : ''
    const cursorGate = cursor && ui.state.cursorRuntime?.available && !ui.state.cursorRuntime?.authenticated
      ? `<aside class="cursor-login"><div><strong>Нужен вход Cursor SDK</strong><small>Один браузерный вход mint’ит ключ в ~/.cursor/sdk (не из приложения Cursor). После этого квест запускается без API-форм.</small></div><button class="primary" type="button" data-action="cursor-login">Войти через Cursor</button></aside>` : ''
    return shell(`<main class="chat-main">${questListHtml()}${orphanExecutionsHtml()}${quickChatSettingsHtml(profiles)}${emptyRoster}${ui.contextInspectorRunId ? contextInspectorPanelHtml() : ''}${conversation()}</main><footer class="composer quest-brief">${cursorGate}${readinessTip}${briefTip}<div class="composer-meta"><select id="profile" title="${esc(profile ? `${profile.name} · ${profile.model}` : 'Выберите персонажа')}">${profiles.map(item=>`<option value="${esc(item.id)}" ${item.id===ui.selectedProfileId?'selected':''}>${esc(item.name)} · ${esc(agentClass(item))}${profileReadiness(item).ready?'':' · настройка'}</option>`).join('')}</select><span title="Провайдер">${esc(providerPreset(profile)?.name||profile?.provider||'')}</span><button data-action="fix-profile-step" data-step="${esc(profile?firstUnreadinessStep(profile):'identity')}" title="Открыть карточку персонажа">⚙</button></div>${requiresApiKey(profile)?`<input id="api-key" type="password" autocomplete="off" placeholder="Ключ API — только в памяти" value="${esc(ui.apiKey)}">`:''}<form id="agent-form"><div class="quest-primary"><label><span>ЗАДАЧА КВЕСТА</span><textarea id="task" rows="3" placeholder="Что именно нужно сделать?" ${active?'disabled':''}>${esc(ui.taskDraft)}</textarea></label><div class="quest-brief-grid"><label><span>ЦЕЛЬ</span><textarea id="quest-goal" rows="2" placeholder="Ожидаемый результат" ${active?'disabled':''}>${esc(ui.questGoalDraft)}</textarea></label><label><span>КРИТЕРИИ</span><textarea id="quest-criteria" rows="2" placeholder="Проверяемые условия" ${active?'disabled':''}>${esc(ui.questCriteriaDraft)}</textarea></label></div></div><details class="quest-advanced"><summary>Контекст и ограничения</summary><label><span>ОГРАНИЧЕНИЯ</span><textarea id="quest-constraints" rows="2" placeholder="Что нельзя менять" ${active?'disabled':''}>${esc(ui.questConstraintsDraft)}</textarea></label><div class="context-toolbar"><button type="button" class="secondary" data-action="attach-files" ${active||cursor?'disabled':''}>＋ Артефакт</button><button type="button" class="secondary" data-action="attach-selection" ${active||cursor?'disabled':''}>＋ Фрагмент</button><span>${ui.contextItems.length?`${ui.contextItems.length} влож.`:'Контекст не добавлен'}</span></div>${ui.contextItems.length?`<div class="context-chips">${ui.contextItems.map((item,index)=>`<span title="${esc(item.path || item.label)}">${item.kind==='workspace_file'?'▤':'¶'} ${esc(item.label || item.path)}<button type="button" data-action="remove-context" data-index="${index}" ${active?'disabled':''}>×</button></span>`).join('')}</div>`:''}</details>${cursor?'':agentRunPreviewMarkup()}<div class="composer-actions">${composerActionsHtml(profile, active, cursor)}</div></form></footer>`)
  }
  
  function history() {
    const runs = ui.state.boot?.runs || []
    const profiles = new Map((ui.state.boot?.profiles || []).map(item => [item.id, item]))
    const diagnosticsByRun = new Map((ui.state.boot?.runDiagnostics || []).map(item=>[item.runId,item]))
    const visibleRuns = runs.slice(0, 100)
    return shell(`<main class="history"><div class="section-title"><span>ХРОНИКА КВЕСТОВ</span><em>${runs.length}</em></div><p class="history-intro">Здесь прошлые походы. Полоса качества показывает доказательства, сбои умений и отказы — откройте главу для полного свитка.</p>${runComparison(runs, diagnosticsByRun)}${visibleRuns.length?visibleRuns.map(run=>{const diagnostics=diagnosticsByRun.get(run.id);const profile=profiles.get(run.profileId)||run.configurationSnapshot?.profile;return `<button class="run quest-row" data-action="load-run" data-id="${esc(run.id)}"><div>${status(run.status)}<small>${formatDateTime(run.startedAt)}</small></div><strong>${esc(run.task)}</strong><footer><span>${esc(profile?.name || run.profileId || 'персонаж')} · ${esc(run.model)}</span><span>ходов: ${run.step}${diagnostics?.approvals?.pending?` · ждёт решений: ${diagnostics.approvals.pending}`:''}</span></footer>${diagnostics?`<div class="run-diagnostic-strip health-${esc(diagnostics.health)}"><span>${esc(healthLabels[diagnostics.health]||diagnostics.health)}</span><span>${formatDuration(diagnostics.durationMs)}</span><span>${diagnostics.model?.usageReported?`${esc(diagnostics.model.totalTokens||0)} ток.`:'токены —'}</span>${historyQualitySignals(diagnostics)}</div>`:''}</button>`}).join(''):`<div class="empty compact"><span class="quest-label">ХРОНИКА МИРА</span><h3>Здесь пока нет глав</h3><p>Завершённые и прерванные квесты станут летописью проекта. Начните с вкладки «Квесты».</p><button class="primary" data-action="tab" data-tab="quests">К брифингу →</button></div>`}${runs.length>visibleRuns.length?`<p class="history-limit">Показаны 100 последних глав из ${runs.length}. Полная история хранится локально.</p>`:''}<footer class="hub-transitional" aria-label="Смежные разделы"><b>ПЕРЕЙТИ</b><button type="button" class="secondary" data-action="tab" data-tab="overview">← Обзор</button></footer></main>`)
  }
  
  function changes() {
    const changes = ui.state.boot?.changes || []
    const runs = new Map((ui.state.boot?.runs || []).map(run=>[run.id,run]))
    const applied = changes.filter(item=>item.status==='applied').length
    return shell(`<main class="changes-history"><header class="changes-heading"><div><span>ХРОНИКА АРТЕФАКТОВ</span><h1>Изменения файлов агентами</h1><p>Point сохраняет точные текстовые изменения как из предложенных diff, так и из подтверждённых команд и своих инструментов. Откат разрешён только если файл не менялся после действия агента.</p></div><div><strong>${changes.length}</strong><small>всего записей</small><b>${applied}</b><small>можно откатить</small></div></header>${changes.length?`<section class="change-list">${changes.map(change=>{const run=runs.get(change.runId);const source=change.sourceTool&&change.sourceTool!=='propose_patch'?toolName(change.sourceTool):'diff агента';return `<article class="change-record status-${esc(change.status)}"><header><div><button data-action="open-file" data-path="${esc(change.path)}">${esc(change.path)}</button><small>${formatDateTime(change.createdAt)} · ${esc(source)} · ${esc(run?.model||'агент')}</small></div><span>${change.status==='applied'?'ПРИМЕНЕНО':change.status==='reverted'?'ОТКАЧЕНО':change.status==='rejected'?'ОТКЛОНЕНО':'ОЖИДАЕТ'}</span></header><details><summary>Посмотреть diff</summary><pre>${esc(change.diff)}</pre></details><footer>${run?`<button class="secondary" data-action="load-run" data-id="${esc(run.id)}">Открыть квест</button>`:''}<button class="secondary" data-action="open-file" data-path="${esc(change.path)}">Открыть файл</button>${change.status==='applied'?`<button class="danger-button" data-action="revert-patch" data-id="${esc(change.id)}">↶ Безопасно откатить</button>`:''}</footer></article>`}).join('')}</section>`:`<div class="empty compact"><h3>Агенты ещё не меняли файлы</h3><p>После первого diff или подтверждённой команды здесь появится контрольная точка с возможностью отката.</p></div>`}<footer class="hub-transitional" aria-label="Смежные разделы"><b>ПЕРЕЙТИ</b><button type="button" class="secondary" data-action="tab" data-tab="journal">Журнал изменений</button><button type="button" class="secondary" data-action="tab" data-tab="changesets">Наборы изменений (Hub)</button><button type="button" class="secondary" data-action="tab" data-tab="overview">← Обзор</button></footer></main>`)
  }
  
  function persistentProfileSummary(agent) {
    const blueprint = blueprintById(agent?.blueprintId)
    const projectRules = Array.isArray(agent?.projectRules) ? agent.projectRules.length : 0
    const skillCount = Array.isArray(agent?.skillIds) ? agent.skillIds.length : 0
    if (!blueprint) {
      return `<section class="agent-runtime" aria-label="Основной профиль и проектная адаптация"><div><small>ОСНОВА</small><strong>Независимый основной профиль</strong></div><div><small>ТЕКУЩИЙ ПРОЕКТ</small><strong>${countOf(projectRules, 'локальное правило', 'локальных правила', 'локальных правил')}</strong></div><div><small>НАВЫКИ</small><strong>${skillCount}</strong></div><div><small>РАЗВИТИЕ</small><strong>через конструктор</strong></div></section>`
    }
    return `<section class="agent-runtime" aria-label="Основной профиль и проектная адаптация"><div><small>ОСНОВНОЙ ПРОФИЛЬ</small><strong>${esc(blueprint.name || blueprint.id)}</strong></div><div><small>В ЭТОМ ПРОЕКТЕ</small><strong>отдельная адаптация</strong></div><div><small>ПРОЕКТНЫЕ ПРАВИЛА</small><strong>${projectRules}</strong></div><div><small>SKILLS / TOOLS</small><strong>${skillCount} / ${(agent.allowedTools || []).length}</strong></div></section>`
  }
  
  function guildRoster() {
    const profiles = hubAgents()
    const templates = ui.state.boot?.profileTemplates || []
    const runs = ui.state.boot?.runs || []
    if (!profiles.length) {
      const firstTemplate = templates[0]
      const primaryHire = firstTemplate
        ? `<button class="primary" data-action="use-template" data-template="${esc(ui.hirePreviewTemplateId || firstTemplate.id)}">Подключить специалиста →</button>`
        : `<button class="primary" data-action="new-profile">＋ Создать основной профиль →</button>`
      return shell(`<main class="guild-roster empty-roster create-flow">
        <header class="guild-heading"><div><span>ПОСТОЯННАЯ AI-КОМАНДА</span><h1>Подключите первого специалиста</h1><p>Выберите переносимый основной профиль. Point создаст для него адаптацию текущего проекта, не дублируя самого агента.</p></div></header>
        
        ${templatePickerHtml(templates, { interactive: true })}
        <div class="empty-next">${primaryHire}<button class="secondary" data-action="open-agent-constructor">⚙ Настроить профиль</button><button class="secondary" data-action="new-profile">＋ Новый специалист</button><button class="secondary" data-action="import-profile">⇧ Импортировать профиль</button></div>
      </main>`)
    }
    if (!ui.selectedProfileId && profiles[0]) ui.selectedProfileId = profiles[0].id
    const selected = profiles.find(item=>item.id===ui.selectedProfileId) || profiles[0]
    if (!selected) return shell('<main class="offline"><p>Профили ещё не загружены.</p></main>')
    const progress = agentProgress(selected)
    const reliability = progress.reliability === undefined ? '—' : `${progress.reliability}%`
    const selectedRuns = runs.filter(run=>run.profileId===selected.id).slice(0, 6)
    const completedRuns = runs.filter(run=>run.status==='completed').length
    const terminalRuns = runs.filter(run=>['completed','failed','cancelled','interrupted'].includes(run.status)).length
    const guildReliability = terminalRuns ? Math.round(completedRuns / terminalRuns * 100) : undefined
    const tools = (selected.allowedTools || []).map(toolName)
    const readiness = profileReadiness(selected)
    const readyCount = profiles.filter(item => profileReadiness(item).ready).length
    const synergies = agentSynergy(selected, profiles)
    const selectedActiveRun = (ui.state.boot?.runs || []).find(run => run.profileId === selected.id && runIsLive(run))
    const activeQuestHtml = selectedActiveRun
      ? activeQuestCard(ui.state.details?.run?.id === selectedActiveRun.id ? ui.state.details : { run: selectedActiveRun })
      : ''
    const synergyHtml = synergies.length
      ? `<section class="roster-synergy"><header><strong>ПОКРЫТИЕ КОМАНДЫ</strong><span>${synergies.length}</span></header><div>${synergies.map(item => `<button type="button" data-action="select-roster-profile" data-id="${esc(item.id)}"><span>◈</span><div><strong>${esc(item.name)}</strong><small>${esc(item.className)} · ${countOf(item.shared, 'общий инструмент', 'общих инструмента', 'общих инструментов')}</small></div><em>${item.score}%</em></button>`).join('')}</div><p>Пересечение разрешённых инструментов помогает видеть дублирование возможностей.</p></section>`
      : `<section class="roster-synergy empty"><header><strong>ПОКРЫТИЕ КОМАНДЫ</strong><span>0</span></header><p>Нет пересечения инструментов с другими специалистами — расширьте навыки текущей команды, прежде чем добавлять новую роль.</p></section>`
    // Блюпринты — единственное, что в гильдии общее для всех проектов: таблица
    // agent_blueprints живёт без workspace_id, а в мир копируется уже
    // проектный агент. Разницу надо говорить вслух: правка блюпринта видна
    // везде, правка агента проекта — только здесь.
    const blueprints = ui.state.boot?.blueprints || []
    const blueprintsHtml = blueprints.length
      ? `<details class="hire-drawer"><summary><span class="hire-kicker">ОБЩЕЕ</span> Блюпринты для всех проектов <span>${blueprints.length}</span></summary><p>Это образцы, общие для всех миров. Правка блюпринта видна каждому проекту, где есть его специалист; сам специалист — копия и живёт только в своём мире.</p>${templatePickerHtml(blueprints, { compact: true })}</details>`
      : ''
    return shell(`<main class="guild-roster">
      <header class="guild-heading"><div><span>ПОСТОЯННАЯ AI-КОМАНДА</span><h1>Команда текущего проекта</h1><p>${countOf(profiles.length, 'специалист', 'специалиста', 'специалистов')} подключено, ${readyCount} ${plural(readyCount, 'готов', 'готовы', 'готовы')} к работе. Улучшайте их через навыки, умения, инструкции и права вместо создания дублей.</p></div><div><button class="secondary" data-action="open-agent-constructor" type="button">⚙ УЛУЧШИТЬ ПРОФИЛЬ</button><button class="recruit" data-action="new-profile" type="button">＋ ПОДКЛЮЧИТЬ СПЕЦИАЛИСТА</button><button class="primary launch" data-action="start-roster-quest" type="button" ${readiness.ready?'':'title="Сначала настройте проектный профиль"'}>${readiness.ready?'▷ ПОСТАВИТЬ ЗАДАЧУ':'▷ СНАЧАЛА НАСТРОИТЬ'}</button></div></header>
      
      ${templates.length ? `<details class="hire-drawer"><summary><span class="hire-kicker">БИБЛИОТЕКА</span> Основные профили команды <span>${templates.length}</span></summary>${templatePickerHtml(templates, { compact: true })}</details>` : ''}
      ${blueprintsHtml}
      <footer class="guild-summary"><span><small>ВСЕГО КВЕСТОВ</small><b>${runs.length}</b></span><span><small>ЗАВЕРШЕНО</small><b>${completedRuns}</b></span><span><small>НАДЁЖНОСТЬ</small><b>${guildReliability===undefined?'—':`${guildReliability}%`}</b></span><p>Метрики рассчитаны локально по сохранённым запускам Point.</p></footer>
      <div class="guild-layout">
        <section class="roster-list"><header><strong>РОСТЕР</strong><span>${profiles.length}</span></header><div>${profiles.map(profile=>{const stats=agentProgress(profile);const reliability=stats.reliability===undefined?'—':`${stats.reliability}%`;const ready=profileReadiness(profile).ready;const activeRun=(ui.state.boot?.runs||[]).find(run=>run.profileId===profile.id&&runIsLive(run));const statusClass=activeRun?'channeling':ready?'idle':'setup';const statusLabel=activeRun?'В РАБОТЕ':ready?'ЖДЁТ':'НАСТРОЙКА';const catalogSize=(ui.state.boot?.toolCatalog||[]).length;const allowedCount=(profile.allowedTools||[]).length;const contextCap=Math.min(100, Math.round((allowedCount/Math.max(1,catalogSize||8))*100));const toolsLabel=catalogSize?`${allowedCount}/${catalogSize}`:String(allowedCount);return `<button class="roster-card ${profile.id===selected.id?'selected':''} ${ready?'':'needs-setup'}" data-action="select-roster-profile" data-id="${esc(profile.id)}"><span class="roster-avatar" aria-hidden="true"><i>✦</i></span><div><header class="roster-card-head"><div><strong>${esc(profile.name)}</strong><small>${esc(agentClass(profile))}${ready?'':' · настройка'}</small></div><em class="roster-lv">УР ${stats.level}</em></header><div class="roster-bars"><label><span>НАДЁЖНОСТЬ</span><b>${reliability}</b></label><progress class="vital" value="${stats.reliability||0}" max="100"></progress><label><span>ОПЫТ</span><b>${stats.xp}/${stats.xpTarget}</b></label><progress value="${stats.xp}" max="${stats.xpTarget}"></progress><label><span>ИНСТРУМЕНТЫ</span><b>${toolsLabel}</b></label><progress class="mana" value="${contextCap}" max="100"></progress></div><footer><span class="roster-status ${statusClass}"><i></i>${statusLabel}</span></footer></div></button>`}).join('')}</div></section>
        <section class="roster-detail"><header><span class="detail-avatar" aria-hidden="true"><i>✦</i></span><div><small>ВЫБРАННЫЙ СПЕЦИАЛИСТ</small><h2>${esc(selected.name)}</h2><p>${esc(agentClass(selected))}</p></div><em>УР ${progress.level}</em></header>${readinessBanner(selected, { editAction: readiness.ready ? 'start-roster-quest' : 'edit-roster-profile' })}${persistentProfileSummary(selected)}<section class="character-purpose"><small>ПОСТОЯННАЯ РОЛЬ</small><p>${esc(selected.roleDescription || 'Описание роли пока не задано.')}</p>${(selected.goals||[]).length?`<ul>${selected.goals.map(goal=>`<li>${esc(goal)}</li>`).join('')}</ul>`:''}</section><div class="detail-stats"><span><small>ЗАДАЧИ</small><b>${progress.quests}</b></span><span><small>ЗАВЕРШЕНО</small><b>${progress.completed}</b></span><span><small>НАДЁЖНОСТЬ</small><b>${reliability}</b></span><span><small>УДАЧНЫХ ВЫЗОВОВ</small><b>${progress.toolMastery===undefined?'—':`${progress.toolMastery}%`}</b></span></div><section class="equipped-tools"><header><strong>НАВЫКИ И УМЕНИЯ</strong><span>${tools.length}</span></header><div>${tools.map((tool,index)=>`<span class="tone-${index%3}">${esc(tool)}</span>`).join('')||'<em>Умения не выбраны</em>'}</div></section><section class="agent-runtime"><div><small>МОДЕЛЬ</small><strong>${esc(selected.model)}</strong></div><div><small>ПРОВАЙДЕР</small><strong>${esc(providerPreset(selected)?.name||selected.provider)}</strong></div><div><small>ПОДТВЕРЖДЕНИЯ</small><strong>${selected.approvalMode==='always'?'каждое действие':'опасные действия'}</strong></div><div><small>ДОКАЗАТЕЛЬСТВА</small><strong>${readiness.hasVerifier?'есть верификатор':'нет верификатора'}</strong></div></section><footer><button class="secondary" data-action="edit-roster-profile">✎ Проектная адаптация</button><button class="secondary" data-action="open-agent-constructor-edit">⚙ Улучшить профиль</button><button class="secondary" data-action="start-roster-quest">${readiness.ready?'Поставить задачу →':'Настроить и к задаче →'}</button>${hubModeAvailable()?`<button class="danger-button" data-action="disband-agent" data-id="${esc(selected.id)}">Распустить</button>`:''}</footer></section>
        <aside class="roster-progression"><header><strong>ПРОГРЕССИЯ</strong><span title="Куда ведёт этот путь">→ УР ${progress.level + 1}</span></header>${activeQuestHtml}<div class="level-track"><label><span>ОПЫТ</span><b>${progress.xp} / ${progress.xpTarget}</b></label><progress value="${progress.xp}" max="${progress.xpTarget}"></progress><small>До следующего уровня — ещё ${countOf(progress.xpTarget - progress.xp, 'подтверждённый квест', 'подтверждённых квеста', 'подтверждённых квестов')}.</small></div>${synergyHtml}<div class="recent-quests"><header><strong>ХРОНИКА</strong><span>${selectedRuns.length}</span></header>${selectedRuns.map((run,index)=>`<button data-action="load-run" data-id="${esc(run.id)}"><span>${index+1}</span><div><strong>${esc(run.task)}</strong><small>${formatDateTime(run.startedAt)} · ходов ${run.step}</small></div>${status(run.status)}</button>`).join('')||'<p>У персонажа ещё нет глав в хронике.</p>'}</div></aside>
      </div>
    </main>`)
  }
  
  function settings() {
    if (ui.agentConstructorOpen) return agentConstructor()
    if (isWide && !ui.profileEditorOpen && !ui.profileDraft) return guildRoster()
    return profileEditor()
  }
  
  function quickChatSettingsHtml(profiles) {
    const qc = ui.state.quickChat || { defaultProfileId: '', openSidebar: true }
    const selected = qc.defaultProfileId || ''
    return `<section class="quick-chat-hub"><header><div><span class="quest-label">БЫСТРЫЙ ЧАТ</span><strong>Исполнитель по умолчанию</strong><p><kbd>Ctrl+Shift+L</kbd> открывает быстрый чат с компаньоном. Здесь выбирается тот, кто возьмёт задачу, если вы не назвали другого. Сам квест ставится справа.</p></div></header><div class="quick-chat-grid"><label>Агент по умолчанию<select id="quick-chat-profile"><option value="">Текущий / первый готовый</option>${profiles.map(item => `<option value="${esc(item.id)}" ${item.id===selected?'selected':''}>${esc(item.name)} · ${esc(agentClass(item))}</option>`).join('')}</select></label><button type="button" class="secondary" data-action="save-quick-chat">Сохранить</button></div></section>`
  }

  return {
    hallActiveSection,
    hallCrumb,
    locallyWaitingCount,
    markDecisionsLoaded,
    decisionsQueueIsStale,
    decisionsWaitingCount,
    decisionsWaitingBreakdown,
    hallSectionBadge,
    hallAlarmHtml,
    hallChangesAlarmHtml,
    hallSpendLabel,
    hallUnpricedRuns,
    workspaceTrustRequired,
    offline,
    projectRequired,
    onboardingCompanionDraft,
    writeOnboardingCompanionDraft,
    sanitizeOrchestratorDraft,
    onboardingOrchestratorDraft,
    writeOnboardingOrchestratorDraft,
    currentOnboardingOrchestratorValues,
    orchestratorConfigFromDraft,
    persistOnboardingOrchestrator,
    orchestratorPresetStudioHtml,
    orchestratorPolicyControlsHtml,
    setOrchestratorPolicy,
    orchestratorPolicyDraftKey,
    ORCHESTRATOR_POLICY_LIMIT,
    orchestratorPolicyCache,
    orchestratorPolicyInflight,
    orchestratorPolicyFailed,
    orchestratorPolicyLines,
    orchestratorPreviewHtml,
    orchestratorModeCardsHtml,
    orchestratorConnectionFieldsHtml,
    systemAgentsStripHtml,
    onboardingCompanionActive,
    onboardingOrchestratorActive,
    orchestratorSetupValidation,
    currentOnboardingCompanionValues,
    indexLanguages,
    onboardingAgentTemplates,
    companionSuggestedTemplateId,
    firstAgentProposal,
    onboardingPrimaryAgent,
    currentAgentProfile,
    skillEquipConfirmHtml,
    patchOnboardingAgent,
    firstAgentDraftFromProposal,
    acceptFirstAgentProposal,
    onboardingPathHtml,
    persistOnboardingCompanion,
    onboarding,
    blueprintDiffValue,
    blueprintSyncPreviewHtml,
    agentConstructor,
    conversation,
    approvalCard,
    patchCard,
    agentRunPreviewMarkup,
    composerActionsHtml,
    chat,
    history,
    changes,
    persistentProfileSummary,
    guildRoster,
    settings,
    quickChatSettingsHtml,
  }
}
