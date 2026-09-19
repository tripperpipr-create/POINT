import { createMasterChatState } from './master-chat-state.js'
import { masterTraceMindPatch } from './master-live-trace.js'
import { bindMasterContexts, installMasterDropzone, receiveMasterContext, clearMasterContext, masterContextPayload, handleMasterContextAction } from './master-context-ui.js'
import { acceptMasterMentionItems, closeMasterMention, handleMasterMentionKey, masterMentionInput, masterMentionOpen, masterMentionState, masterMentionStripped } from './master-mention-ui.js'
import { handleMasterSessionAction, patchMasterAnswerNote } from './master-session-ui.js'
import { taskBriefCardHtml, taskBriefActionsHtml, taskBriefBodyHtml, taskBriefReady, taskBriefStateLabel, proposalEditorHtml, readProposalDecisionEditor } from './task-brief-views.js'
import { createMasterBriefPanel } from './master-brief-panel.js'
import { createStatisticsViews } from './statistics-views.js'
import { createInfrastructureViews } from './infrastructure-views.js'
import { createToolWindowFrame } from './tool-window-frame.js'
import { createChangeSetViews } from './change-set-views.js'
import { createCompanionMarkdownFormatter } from './companion-markdown.js'
import { createGitViews } from './git-views.js'
import { createViewRuntime } from './view-runtime.js'
import { createQuestOverviewViews } from './quest-overview-views.js'
import { createQuestRuntimeViews } from './quest-runtime-views.js'
import { createHallOnboardingViews } from './hall-onboarding-views.js'
import { createProjectGalleryViews } from './project-gallery-views.js'
import { createMasterChatDirectory } from './master-chat-directory.js'
import { createAgentWorkTranscript } from './agent-work-transcript.js'
import { createAgentWorkflowEditors } from './agent-workflow-editors.js'
import { createAgentConstructor, CONSTRUCTOR_STEPS as AGENT_CONSTRUCTOR_STEPS } from './agent-constructor.js'
import { createHubRuntimeUi } from './hub-runtime-ui.js'
import { handleGitClickAction, handleGitChangeAction } from './git-actions.js'
import { handleHubClickAction } from './hub-actions.js'
import { handleCompanionClickAction } from './companion-actions.js'
import { handleOnboardingClickAction } from './onboarding-actions.js'
import { createCompanionTransport } from './companion-transport.js'
import { createMasterInbox } from './master-inbox.js'
import { createHubEntityInbox } from './hub-entity-inbox.js'
import { createDecisionViews } from './decision-views.js'
import { createCompanionThreadViews } from './companion-thread-views.js'
import { createKeyboardNavigation } from './keyboard-navigation.js'
import { createModelPicker } from './model-picker.js'
import { createCompanionStudioViews, COMPANION_PRESETS } from './companion-studio-views.js'
import { createCompanionSetupWizard } from './companion-setup-wizard.js'
import { createMasterThreadViews } from './master-thread-views.js'
import { masterWorkOrderCardsHtml } from './master-work-order-v2.js'
import { handleMasterHiringAction } from './master-hiring-card.js'
import { handleMasterAgentCardAction, masterAgentCardsAll, masterAgentConsent, readMasterAgentCardInput, releaseMasterAgentCards } from './master-agent-card.js'
import { MASTER_MESSAGE_LIMIT_BYTES, masterComposeCountClass, masterComposeCountState, masterComposeFormClass, masterAnswerRows, masterComposeRows, masterMessageBytes, masterWaitSuffix, oversizedMasterMessageNote } from './master-compose.js'
import { createMasterFeedRuntime } from './master-feed.js'
import { COMPANION_EXAMPLES, COMPANION_MESSAGE_LIMIT_BYTES, COMPANION_SETUP_STEPS, COMPANION_SETUP_STEP_ALIAS, applyLocalSourceFields, companionBrainMode, companionConfigForBrain, normalizeBrainMode, companionSpendCaveats, companionSceneById, companionModeCardsHtml, companionLocalReadyHtml, companionComposeActionsHtml, companionComposeMetaHtml, companionMessageBytes, companionWaitSuffix, oversizedCompanionMessageNote } from './companion-compose.js'
// Счётчик отправок нужен защите форм от повторной отправки: обработчик формы
// может выйти раньше, ничего не отправив (не заполнено поле, не пройдена
// проверка), и запирать её в этом случае нельзя — человек исправит и отправит
// снова. Считаем сами отправки, а не факт нажатия.
let sentCount = 0
const vscodeApi = acquireVsCodeApi()
const masterClient = createMasterChatState(vscodeApi.getState()?.masterChat || {})
const masterViewId = Date.now().toString(36) + Math.random().toString(36).slice(2)
let masterRequestId = 0
const vscode = {
  postMessage(message) {
    if (/master/i.test(message.type || '')) {
      if(message.type==='masterViewPreferences'){masterClient.historyHidden=message.hidden;masterClient.historyOpen=message.open;persistDraft();return}
      message = {...message,viewId:masterViewId,conversationId:message.conversationId || masterClient.active || undefined}
      if (message.type==='loadMaster' || message.type==='masterSession') message.requestId=++masterRequestId
      if (message.type==='stopMasterChat') message.turnId=masterClient.turns[masterClient.active]?.id
    }
    sentCount += 1; return vscodeApi.postMessage(message)
  },
  getState() { return vscodeApi.getState() },
  setState(value) { return vscodeApi.setState(value) },
}
const root = document.getElementById('root')
const { handleListKeydown } = createKeyboardNavigation({ root })
const isWide = document.body?.dataset?.layout === 'wide'
const surfaceLayout = document.body?.dataset?.layout || ''
function toolWindowKind() { return surfaceLayout.startsWith('tool-') ? surfaceLayout.slice(5) : '' }
function isToolWindow() { return Boolean(toolWindowKind()) }
function isStatisticsView() { return document.body?.dataset?.layout === 'statistics' }
function isConnectionsView() { return document.body?.dataset?.layout === 'connections' }
function isCompanionPopup() {
  const layout = document.body?.dataset?.layout
  return layout === 'companion-popup' || layout === 'companion-peek'
}
function isCompanionPeek() { return document.body?.dataset?.layout === 'companion-peek' }
function isCompanionSidebar() { return document.body?.dataset?.layout === 'companion-sidebar' }
function isCompanionDock() { return document.body?.dataset?.layout === 'companion' }
function isCompanionView() {
  const layout = document.body?.dataset?.layout
  return layout === 'companion' || layout === 'companion-popup' || layout === 'companion-peek' || layout === 'companion-sidebar'
}
const persisted = vscode.getState() || {}
const tabAlias = { chat: 'quests', quest: 'quests', settings: 'agents', workflows: 'flows', history: 'history', changes: 'changes', changesets: 'changesets', journal: 'journal', databases: 'databases', db: 'databases' }
function canonicalTab(tab) { return tabAlias[tab] || tab || 'overview' }
let selectedIntakeId = ''
let intakeBusy = false
let intakeError = ''
let intakeURL = ''
let state = { service: { state: 'starting' }, workspaceTrusted: true, workspace: '', boot: undefined, details: undefined, selectedTab: canonicalTab(persisted.selectedTab || 'overview') }
let apiKey = typeof persisted.apiKey === 'string' ? persisted.apiKey : ''
let selectedProfileId = typeof persisted.selectedProfileId === 'string' ? persisted.selectedProfileId : ''
let lastAgentImprovementFocusId = ''
let taskDraft = typeof persisted.taskDraft === 'string' ? persisted.taskDraft : ''
let questGoalDraft = typeof persisted.questGoalDraft === 'string' ? persisted.questGoalDraft : ''
let questCriteriaDraft = typeof persisted.questCriteriaDraft === 'string' ? persisted.questCriteriaDraft : ''
let questConstraintsDraft = typeof persisted.questConstraintsDraft === 'string' ? persisted.questConstraintsDraft : ''
let agentRunPreview
let agentRunPreviewStatus = 'idle'
let agentRunPreviewError = ''
let profileDraft
let contextItems = Array.isArray(persisted.contextItems) ? persisted.contextItems.slice(0, 16) : []
let contextPreview
let contextPreviewStatus = 'idle'
let contextPreviewError = ''
let selectedCustomToolId = ''
let customToolDraft
let customToolPreview
let customToolPreviewStatus = 'idle'
let customToolPreviewError = ''
let customToolPreviewArguments = {}
let toolEquipAfterSave = false
let providerProbe
let modelCapabilityProbe
let selectedWorkflowId = ''
let workflowDraft
let transientError = ''
let profileEditorOpen = false
let profileEditorStep = 'identity'
let hirePreviewTemplateId = ''
let hireAfterSave = ''
// Наряд, на правку которого мы ждём свежую карточку найма.
let hiringReloadFor = ''
let createStepError = ''
let hireLiveTimer = 0
let renderFrame = 0
let paintFrame = 0
let restoredTabPosted = false
let cursorRunActive = false
let cursorRunEvents = []
let companionMessages = Array.isArray(persisted.companionMessages) ? persisted.companionMessages.slice(-80) : []
let companionDraft = typeof persisted.companionDraft === 'string' ? persisted.companionDraft : ''
let plannerFallbackNotice = null
let keptRunId = typeof persisted.keptRunId === 'string' ? persisted.keptRunId : ''
let companionIdeContext = persisted.companionIdeContext && typeof persisted.companionIdeContext === 'object' ? persisted.companionIdeContext : {}
let companionLoading = false
let companionPendingSend = ''
let companionThinkPhase = ''
let companionActivitySteps = []
let companionStreamReply = ''
let companionRequestId = 0
let companionActiveRequestId = 0
let companionAutoFollow = true
// Отметки ответов приходят от расширения и переживают перерисовку: раньше
// «Спасибо ✓» держалось до первого render, и человек терял след своих оценок.
let companionFeedbackMarks = new Map()
let toolWindowData = {}
let toolLogFilter = typeof persisted.toolLogFilter === 'string' ? persisted.toolLogFilter : 'all'
let gitCommitDraft = typeof persisted.gitCommitDraft === 'string' ? persisted.gitCommitDraft : ''
let gitPendingAction = ''
let gitNotice = undefined
// Отметки переживают переключение вкладки и перезапуск панели: снятая галочка —
// это решение человека, а не состояние кадра. gitKnown помнит, о каком файле
// решение уже принималось: без него снятая отметка возвращалась бы обратно на
// первом же обновлении снимка.
let gitChecked = new Set(Array.isArray(persisted.gitChecked) ? persisted.gitChecked : [])
let gitKnown = new Set(Array.isArray(persisted.gitKnown) ? persisted.gitKnown : [])
let gitCollapsed = new Set(Array.isArray(persisted.gitCollapsed) ? persisted.gitCollapsed : [])
let gitHistoryOpen = false
// Открытое меню хранится состоянием, а не разметкой: снимок Git обновляется от
// каждой правки в редакторе, отрисовка пересобирает панель целиком, и <details>
// закрывался бы под рукой.
let gitMenuFor = ''
let gitDragPath = ''
let gitNoticeTimer = 0
let projectSwitchTimer = 0
let chatDirectoryStatus = 'idle'
// Черновики привязаны к панели, а не к миру: `vscode.setState` живёт на панели,
// и после переключения проекта задача, набранная для одного мира, всплыла бы в
// другом. Ключ мира кладётся рядом с черновиками, и его расхождение — сигнал
// всё проектное забыть.
let projectKey = typeof persisted.projectKey === 'string' ? persisted.projectKey : ''
// Правка последнего коммита — решение на один раз: сохранять его между
// запусками нельзя, иначе следующий коммит незаметно перепишет чужую историю.
let gitAmend = false
// Список «Вне репозитория» бывает в тысячу файлов. Один раз на проект он
// сворачивается сам; дальше решает человек, и его решение переживает перезапуск.
let gitFoldedOnce = Boolean(persisted.gitFoldedOnce)
// Вкладка панели и вид списка — настройки взгляда, они переживают перезапуск.
let gitTab = ['changes', 'stash'].includes(persisted.gitTab) ? persisted.gitTab : 'changes'
let gitFlat = Boolean(persisted.gitFlat)
// Выбранная строка живёт только в сеансе: файл мог уехать в коммит, коммит —
// уползти вниз истории, и восстанавливать такой выбор незачем.
let gitSelected = ''
let gitSelectedStash = ''
let gitTarget = ''
// Карточка коммита и записи полки помещаются только в широкой панели. Правило
// то же, что в CSS: разметка и раскладка не должны расходиться о ширине.
const gitWideQuery = typeof window.matchMedia === 'function' ? window.matchMedia('(min-width: 640px)') : undefined
function gitWide() { return Boolean(gitWideQuery?.matches) }
if (gitWideQuery?.addEventListener) gitWideQuery.addEventListener('change', () => render())
let companionAppliedNotice = undefined
let proposalEditId = ''
// Предложения, по которым запуск уже отправлен и ответа ещё нет.
//
// Запуск создаёт квест, отряд, Flow и прогон — второе нажатие делает вторые, а
// не повторяет первые. Ждать приходится заметно: ключ оркестратора, решение
// ядра, bootstrap, старт прогона. Ядро повтор отвергает, но человек не должен
// доходить до ошибки там, где он просто нажал дважды.
//
// Набор, а не одно значение: запрет обязан совпадать с тем, что видно. Одна
// строка блокировала бы и запуск соседнего предложения — действия отдельного и
// намеренного, — а кнопка у него при этом оставалась бы живой.
const proposalStarting = new Set()
const proposalModifying = new Set()
const proposalEditDrafts = new Map()
let masterDiscussionProposalId = typeof persisted.masterDiscussionProposalId === 'string' ? persisted.masterDiscussionProposalId : ''
function taskProposalById(id) {
  return (state.boot?.questProposals || []).find(p => p.id === id) || (masterData?.response?.proposal?.id === id ? masterData.response.proposal : null) || (decisionsData?.items || []).find(p => p.id === id && p.brief)
}
// Создание сущности тоже необратимо в пределах одного клика: два запроса
// создадут две карточки с разными ID. Пока ядро не подтвердило решение,
// повторное Apply блокируется отдельно для каждого черновика.
const companionActionApplying = new Set()
const companionActionModifying = new Set()
const companionActionEditDrafts = new Map()
let companionActionEditId = ''
let companionSetupOpen = false
let companionSetupStep = typeof persisted.companionSetupStep === 'string' ? persisted.companionSetupStep : 'brain'
// Черновик Studio восстанавливается ниже, после объявления
// sanitizeCompanionSetupDraft: санитайзер читает таблицу COMPANION_EXAMPLES,
// которая на этой строке ещё не инициализирована. Вызов отсюда бросал
// TypeError, весь бандл вебвью умирал до первой отрисовки, и панель навсегда
// оставалась на заглушке «Открываем диалог…».
let companionSetupDraft
let companionProviderProbe
// Отдельное состояние проверки из предупреждения в чате. Настроечный probe
// живёт внутри Studio; переиспользование только его состояния делало результат
// невидимым, когда Studio закрыта.
let companionInterventionProbe
let companionSetupStatus = ''
let companionSetupTestResult
let companionSetupPendingClose = false
let companionSetupQuiet = false
let pendingSkillEquip = undefined
// Сохранённые шаги v1 (welcome/companion/first-agent) не должны возвращать
// обновлённый Hub в удалённый девятиэкранный сценарий.
let onboardingStep = ['orchestrator-brain', 'orchestrator-choose'].includes(persisted.onboardingStep)
  ? persisted.onboardingStep
  : 'orchestrator-brain'
// Почему шаг не открылся. Клик по запертому шагу раньше просто ничего не делал:
// человек жал и не понимал, сломано ли это, не туда ли он нажал, или так задумано.
let onboardingLockNotice = ''
let onboardingDraft = persisted.onboardingDraft && typeof persisted.onboardingDraft === 'object'
  ? persisted.onboardingDraft
  : { companionPreset: 'balanced', criticality: 50, creativity: 50, verbosity: 50, initiative: 50, questionStrictness: 70, riskTolerance: 30, agentName: '', agentTemplateId: '', connectionProvider: '', skillIds: [] }
let agentConstructorOpen = Boolean(persisted.agentConstructorOpen)
let constructorStep = typeof persisted.constructorStep === 'string' ? persisted.constructorStep : 'identity'
let constructorDraft = persisted.constructorDraft && typeof persisted.constructorDraft === 'object' ? persisted.constructorDraft : undefined
let ignoredCompanionSuggestions = new Set(Array.isArray(persisted.ignoredCompanionSuggestions) ? persisted.ignoredCompanionSuggestions : [])
let selectedFlowId = typeof persisted.selectedFlowId === 'string' ? persisted.selectedFlowId : ''
let flowDraft
let flowLegacyMode = Boolean(persisted.flowLegacyMode)
let contextInspectorRunId = ''
let contextInspector
let contextInspectorStatus = 'idle'
let contextInspectorNotice = ''
let masterData
let masterStatus = 'idle'
const masterSessionDrafts = Object.assign(masterClient.drafts, persisted.masterSessionDrafts || {})
let masterDraft = typeof persisted.masterDraft === 'string' ? persisted.masterDraft : ''
// Что уже ушло в ядро и ждёт ответа — по разговору. Живёт только на время хода
// и только в памяти: после перезагрузки панели своя реплика придёт из истории.
//
// Раньше обе роли играл masterDraft, и поле приходилось запирать и очищать —
// иначе набранное во время хода уехало бы в ленту как отправленное.
//
// Ключ по разговору, а не одна строка на всех: ход идёт в своём чате, а уйти
// из него на время ответа можно — и вернувшись, человек увидел бы под своей
// репликой текст из соседнего разговора.
const masterSentTexts = {}
const masterSentText = () => masterSentTexts[masterClient.active] || ''
const rememberMasterSent = text => { masterSentTexts[masterClient.active] = text }
const forgetMasterSent = () => { delete masterSentTexts[masterClient.active] }
let masterSending = false
bindMasterContexts(masterClient.attachments)
installMasterDropzone(root,()=>masterClient.active,()=>{persistDraft();render()},error=>{masterComposeNote=error;render()},()=>masterSending)
// После старта с Мастера держим привязку proposal → quest/run, чтобы лента
// работы агентов оставалась в переписке, а не только на вкладке квестов.
const masterPinnedWork = new Map()
const masterWorkOrderBusy = new Set()
// Лента разговора держится на конце, пока человек сам не ушёл читать выше:
// перерисовка не должна ни утаскивать его от прочитанного, ни прятать ответ.
let masterAutoFollow = true
// Отказ по длине и счёт ожидания относятся к тому, что человек делает прямо
// сейчас, и живут при поле, а не в ленте.
let masterComposeNote = ''
let masterWaitedSeconds = 0
let masterWaitTimer = 0
// Какие «Рассуждение» и «Что смотрел» раскрыты. Лента заменяется точечно на
// каждом ходе, и без памяти о раскрытом длинное рассуждение схлопывалось бы
// прямо под читающим — прочесть его до конца было бы нельзя.
const masterOpenReasoning = new Set()
const masterExpandedSteps = new Set()
const masterOpenSteps = new Set()
// Поиск по разговору и догрузка начала. Поиск живёт в состоянии, а не только в
// поле: лента заменяется точечно на каждом ходе, и без этого набранное искалось
// бы заново после каждого ответа.
let masterFindQuery = ''
let masterFindIndex = 0
let masterFindSummary = ''
let masterLoadingEarlier = false
let masterFindOpen = false
// Подставленный вопрос дописывают, а не переписывают: курсор должен встать в
// конец. Ставит его отрисовка — поле к тому времени уже другое.
let masterCaretToEnd = false
// Запуск в пути: повторная отправка формы породила бы второй прогон той же задачи.
let runStarting = false
let decisionsData
let decisionsStatus = 'idle'
let decisionsError = ''
let decisionPick = ''
let fileHistoryData
let fileHistoryPath = ''
let fileHistoryStatus = 'idle'
let statisticsData
let statisticsStatus = 'idle'
let dockerData
let dockerStatus = 'idle'
let dockerLogs
let dockerLogsContainer = ''
let dbQueryResult
let dbQueryStatus = 'idle'
let dbSchemaResult
let dbSelectedId = ''
let dbWritePending = null
let dbEditingId = ''
let serverEditingId = ''
// Правка подключения к модели: пусто — форма создаёт новое.
let connectionEditingId = ''
let memoryEditId = ''
let memoryDraft
let experienceSearchQuery = ''
let experienceSearchItems = []
let experienceSearchStatus = 'idle'
let manualLearningDraft
let manualLearningPreview
let manualLearningStatus = 'idle'
let skillEditId = ''
let skillDraft
let skillFormError = ''
let skillEquipAfterSave = true
let skillPendingEquipId = ''
let selectedFlowNodeId = ''
let blueprintSyncPreview
let blueprintSyncDirection = ''
let compiledPromptPreview
let compiledPromptStatus = 'idle'
let compiledPromptError = ''
let compiledPromptSignature = ''
const ONBOARDING_STEPS = [
  { id: 'orchestrator-brain', label: 'Подключение', hint: 'Движок или модель', why: 'Выберите локальный движок Point или подключение модели для Мастера' },
  { id: 'orchestrator-choose', label: 'Мастер', hint: 'Правила работы', why: 'Настройте глубину плана, параллельность и строгость проверки' },
]
const ONBOARDING_CHAPTERS = [
  { id: 'connection', label: 'Подключение', hint: 'Где думает Мастер', steps: ['orchestrator-brain'] },
  { id: 'master', label: 'Мастер', hint: 'Как он ведёт задачу', steps: ['orchestrator-choose'] },
]
const ORCHESTRATOR_PRESETS = [
  { id: 'conductor', icon: '⬡', label: 'Дирижёр', hint: 'Собирает отряд и ведёт Flow', values: { planningDepth: 70, parallelism: 60, approvalStrictness: 40, teamPreference: 85 } },
  { id: 'dispatcher', icon: '⇥', label: 'Диспетчер', hint: 'Один лучший агент, быстрый старт', values: { planningDepth: 35, parallelism: 80, approvalStrictness: 30, teamPreference: 20 } },
  { id: 'conservative', icon: '▣', label: 'Контролёр', hint: 'Запуск создаёт Flow с гейтами подтверждения', values: { planningDepth: 55, parallelism: 15, approvalStrictness: 85, teamPreference: 50 } },
  { id: 'custom', icon: '◌', label: 'Свои правила', hint: 'Ручные пороги планирования и отряда', values: { planningDepth: 50, parallelism: 50, approvalStrictness: 50, teamPreference: 50 } },
]
const ORCHESTRATOR_TRAIT_FIELDS = [
  { id: 'planningDepth', label: 'Глубина плана', hint: 'Насколько дробить квест на Flow', low: 'прямо', high: 'глубже' },
  { id: 'parallelism', label: 'Параллельность', hint: 'Сколько исполнений запускать сразу', low: 'по одному', high: 'параллельно' },
  { id: 'approvalStrictness', label: 'Строгость ревью', hint: 'Гейты подтверждения в Flow после запуска', low: 'быстрее', high: 'строже' },
  { id: 'teamPreference', label: 'Отряд', hint: 'Один агент или партия', low: 'соло', high: 'партия' },
]
const CONSTRUCTOR_STEPS = AGENT_CONSTRUCTOR_STEPS
function companionNormalizeSceneId(value) {
  const raw = String(value || '')
  if (COMPANION_EXAMPLES.some(item => item.id === raw)) return raw
  return COMPANION_EXAMPLES.find(item => item.prompt === raw)?.id || COMPANION_EXAMPLES[0].id
}
function companionSelectedScene(value) {
  const draft = value && typeof value === 'object' ? value : {}
  return companionSceneById(companionNormalizeSceneId(draft.sampleScene || draft.examplePrompt))
}
// Доступ помощника записан группами каталога, а не списком имён: ядро выдаёт
// ему read, index и git (internal/policy/grants.go, ProjectReadingGroups). Тот
// же список нужен экрану — иначе человек отметит навык, который ядро откажется
// надеть, и настройка не сохранится. Расхождение сторожит ui/contracts.mjs.
const COMPANION_TOOL_GROUPS = ['read', 'index', 'git']
// Ядро отвергает слишком длинный список целиком, вместе с остальной настройкой.
const COMPANION_SKILL_LIMIT = 64
function normalizeCompanionSkillIds(value) {
  const seen = new Set()
  const ids = []
  for (const item of Array.isArray(value) ? value : []) {
    const id = String(item || '').trim()
    if (!id || seen.has(id)) continue
    seen.add(id)
    ids.push(id)
    if (ids.length >= COMPANION_SKILL_LIMIT) break
  }
  return ids
}
function sanitizeCompanionSetupDraft(value) {
  const input = value && typeof value === 'object' ? value : {}
  const mode = normalizeBrainMode(input.mode)
  return {
    mode,
    connectionMode: input.connectionMode === 'new' ? 'new' : 'existing',
    connectionId: String(input.connectionId || ''),
    connectionName: String(input.connectionName || ''),
    providerPreset: String(input.providerPreset || ''),
    provider: String(input.provider || ''),
    baseUrl: String(input.baseUrl || ''),
    model: String(input.model || ''),
    preset: String(input.preset || 'balanced'),
    temperature: Number.isFinite(Number(input.temperature)) ? Number(input.temperature) : 0.2,
    maxOutputTokens: Number.isFinite(Number(input.maxOutputTokens)) ? Number(input.maxOutputTokens) : 1200,
    criticality: Number.isFinite(Number(input.criticality)) ? Number(input.criticality) : 50,
    creativity: Number.isFinite(Number(input.creativity)) ? Number(input.creativity) : 50,
    verbosity: Number.isFinite(Number(input.verbosity)) ? Number(input.verbosity) : 50,
    initiative: Number.isFinite(Number(input.initiative)) ? Number(input.initiative) : 50,
    questionStrictness: Number.isFinite(Number(input.questionStrictness)) ? Number(input.questionStrictness) : 70,
    riskTolerance: Number.isFinite(Number(input.riskTolerance)) ? Number(input.riskTolerance) : 30,
    autoAct: Boolean(input.autoAct),
    autoOpenChatOnCritical: Boolean(input.autoOpenChatOnCritical),
    autoSendModelPrompt: Boolean(input.autoSendModelPrompt),
    skillIds: normalizeCompanionSkillIds(input.skillIds),
    sampleScene: companionNormalizeSceneId(input.sampleScene || input.examplePrompt),
    examplePrompt: String(input.examplePrompt || companionSceneById(input.sampleScene)?.prompt || COMPANION_EXAMPLES[0].prompt),
  }
}
// Сохранённое состояние вебвью переживает обновления Point, поэтому черновик из
// прошлой версии может не подойти нынешнему санитайзеру. Отдельный чат дороже
// одного черновика: при любой поломке чат открывается с чистой Studio.
if (persisted.companionSetupDraft && typeof persisted.companionSetupDraft === 'object') {
  try {
    companionSetupDraft = sanitizeCompanionSetupDraft(persisted.companionSetupDraft)
  } catch {
    companionSetupDraft = undefined
  }
}
const PROFILE_STEPS = [
  { id: 'identity', label: 'Личность', hint: 'Имя и миссия', why: 'Как зовут персонажа, в чём он эксперт и какие правила не нарушает' },
  { id: 'model', label: 'Модель', hint: 'Провайдер и ID', why: 'Откуда берётся интеллект — проверьте подключение до первого квеста' },
  { id: 'tools', label: 'Умения', hint: 'Разрешения', why: 'В модель уходят только включённые схемы; для правок нужен верификатор' },
  { id: 'limits', label: 'Лимиты', hint: 'Ходы и контроль', why: 'Потолок ходов, тайм-аут и когда спрашивать подтверждение' },
]
const CREATE_FLOW_STAGES = [
  { id: 'class', label: 'Класс', hint: 'Шаблон роли', why: 'Класс подставляет роль, умения и безопасные лимиты — дальше останется модель' },
  ...PROFILE_STEPS,
]
const TOOL_PRESETS = [
  { id: 'observer', label: 'Наблюдатель', hint: 'Только чтение и индекс', tools: ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff'] },
  { id: 'editor', label: 'Редактор', hint: 'Правки + верификатор', tools: ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff', 'propose_patch', 'run_command'] },
  { id: 'developer', label: 'Разработчик', hint: 'Все умения + верификатор', tools: null },
  { id: 'none', label: 'Без умений', hint: 'Только ответы модели', tools: [] },
]
// Жизненный цикл прогона — одним набором на весь интерфейс.
//
// Ядро знает восемь статусов (internal/domain/types.go). Вопрос «прогон ещё не
// «Прогон закончился» / контролы исполнения / pending review — hub-runtime-ui.js
// Смена мира обнуляет всё проектное.
//
// `vscode.setState` живёт на панели, а панель при переключении та же самая:
// задача, набранная для одного проекта, без этого всплывала бы в другом — и не
// только задача, но и разговор с мастером, очередь решений, состояние Git и
// кэши способностей агентов, ключи которых у каждого мира свои. Настройки вида
// (фильтр журнала, вкладка и режим Git, развёрнутая рейка компаньона) человеку
// принадлежат, а не проекту, и переживают переключение.
function resetProjectScopedState() {
  taskDraft = ''
  questGoalDraft = ''
  questCriteriaDraft = ''
  questConstraintsDraft = ''
  contextItems = []
  selectedProfileId = ''
  selectedFlowId = ''
  keptRunId = ''
  masterData = undefined
  masterStatus = 'idle'
  masterDraft = ''
  masterDiscussionProposalId = ''
  masterClient.active = ''
  masterClient.drafts = {}
  masterClient.scroll = {}
  masterClient.attachments = {}
  masterClient.turns = {}
  masterClient.questionDrafts = {}
  masterClient.briefPanel = {}
  masterClient.pages = {}
  companionMessages = []
  companionDraft = ''
  companionStreamReply = ''
  companionLoading = false
  ignoredCompanionSuggestions = new Set()
  decisionsData = undefined
  decisionsStatus = 'idle'
  toolWindowData = {}
  gitCommitDraft = ''
  gitChecked = new Set()
  gitKnown = new Set()
  gitCollapsed = new Set()
  gitFoldedOnce = false
  agentCapabilityCache.clear()
  agentCapabilityInflight.clear()
  agentCapabilityFailed.clear()
}

function persistDraft() {
  masterClient.remember(masterData?.sessions?.active || masterClient.active,masterDraft,root.querySelector('#master-thread')?.scrollTop)
  vscode.setState({
    projectKey,
    taskDraft,
    questGoalDraft,
    questCriteriaDraft,
    questConstraintsDraft,
    selectedProfileId,
    selectedTab: state.selectedTab || 'overview',
    toolLogFilter,
    gitCommitDraft,
    gitChecked: [...gitChecked],
    gitKnown: [...gitKnown],
    gitCollapsed: [...gitCollapsed],
    gitFoldedOnce,
    gitTab,
    gitFlat,
    contextItems,
    companionMessages,
    companionDraft,
    // Черновик Мастера читался при старте, но никогда не сохранялся: набранная
    // задача пропадала при перезапуске панели, хотя код делал вид, что вернёт её.
    masterChat: masterClient.snapshot(),
    masterSessionDrafts:masterClient.snapshot().drafts,
    masterDraft:masterClient.active.startsWith('temporary')?'':masterDraft,
    masterDiscussionProposalId,
    // companionSetupOpen намеренно не сохраняется: открытая панель настроек,
    // пережившая перезапуск, однажды уже встречала человека мёртвым Чертогом.
    companionSetupStep,
    companionSetupDraft: companionSetupDraft ? sanitizeCompanionSetupDraft(companionSetupDraft) : undefined,
    onboardingStep,
    onboardingDraft,
    agentConstructorOpen,
    constructorStep,
    constructorDraft,
    ignoredCompanionSuggestions: [...ignoredCompanionSuggestions],
    selectedFlowId,
    flowLegacyMode,
    keptRunId,
  })
  if (isCompanionView() || companionLoading || companionMessages.length || companionStreamReply) {
    vscode.postMessage({
      type: 'companionThreadUpdate',
      messages: companionMessages,
      draft: companionDraft,
      streamReply: companionStreamReply,
      loading: companionLoading,
      pendingSend: companionPendingSend,
      requestId: companionActiveRequestId,
    })
  }
}

// Одно указание — одна формулировка. Пустая задача объясняется одинаково и в
// подсказке качества брифинга, и при обоих способах запуска: два глагола для
// одного требования читаются как два разных требования.
const EMPTY_TASK_REASON = 'Сформулируйте задачу — без неё квест не стартует'
const questStatusLabels = {
  draft:'ЧЕРНОВИК', proposed:'ПРЕДЛОЖЕН', awaiting_approval:'ЖДЁТ ПОДТВЕРЖДЕНИЯ',
  preflight:'ПРОВЕРКА СРЕДЫ', active:'АКТИВЕН', running:'ВЫПОЛНЯЕТСЯ',
  awaiting_user:'НУЖНО РЕШЕНИЕ', paused:'ПАУЗА', verifying:'ПРОВЕРЯЕТСЯ', applying:'ПРИМЕНЯЕТСЯ',
  needs_review:'НУЖНА ПРИЁМКА', blocked:'ЗАБЛОКИРОВАН', completed:'ЗАВЕРШЁН',
  failed:'ПРОВАЛЕН', cancelled:'ОТМЕНЁН',
}
const changeSetStatusLabels = { pending:'ОЖИДАЕТ', approved:'ОДОБРЕН', applied:'ПРИМЕНЁН', reverted:'ОТКАЧЕН', rejected:'ОТКЛОНЁН', conflict:'КОНФЛИКТ', superseded:'ВКЛЮЧЁН В СЛИЯНИЕ' }
// disconnected ядро объявляет (domain/hub.go), но пока не присваивает. Подпись
// нужна заранее: без неё карточка напечатала бы сырое английское слово среди
// русских — при отсутствии подписи здесь показывается само значение статуса.
// probing — состояние клиента на время опроса, у ядра его нет.
const connectionStatusLabels = { connected:'ПОДКЛЮЧЕН', unknown:'НЕИЗВЕСТНО', error:'ОШИБКА', probing:'ПРОВЕРКА', disconnected:'ОТКЛЮЧЁН' }

// Состояние связи в списках выбора показывалось одним цветом: шарик с классом
// статуса, без единого слова рядом. Цвет — не единственный способ читать экран:
// при дальтонизме и в высококонтрастной теме «подключён» и «ошибка» неразличимы,
// а читалка не произносит ничего. Подпись берём из того же словаря, что и
// страница «Связи», — чтобы одно состояние не называлось двумя способами.
function connectionOrbHtml(status) {
  const key = String(status || 'unknown')
  const label = connectionStatusLabels[key] || key
  return `<span class="connection-orb ${esc(key)}" role="img" title="${esc(label)}" aria-label="${esc(label)}"></span>`
}
const memoryKindLabels = { project:'Проект', profile:'Основной профиль', agent:'Проектный агент', companion:'Компаньон', quest:'Квест' }
const {
  statusLabels, toolLabels, eventLabels, healthLabels, stopReasonLabels,
  plural, countOf, esc, formatCompanionMarkdown, data, toolName,
  providerCatalog, providerPreset, requiresApiKey, lines, toolProvidesVerification, modelCapabilityProbeHtml,
} = createViewRuntime({ getState: () => state, createCompanionMarkdownFormatter })

// Собирается выше обеих ветвей отрисовки: поверхностей Хаба и окон панели.
const toolWindowFrame = createToolWindowFrame({ esc, isToolWindow })

// Студия характера компаньона живёт отдельным модулем: main.js держит её
// состояние, а разметку роли, стиля, черт и образца ответа считает она.
const {
  companionScenePickerHtml, companionRoleShowcaseHtml, companionActCompareHtml,
  companionPredictedReplyHtml, companionSetupTestAnswerHtml, refreshCompanionLiveSurfaces,
  companionTraitKeyToId, companionPresetStudioHtml, companionStyleStudioHtml,
  companionPersonalityControlsHtml, companionPersonalityPreviewHtml, companionProbeHtml,
  companionProbeCtaLabel,
} = createCompanionStudioViews({
  root,
  esc,
  sanitizeCompanionSetupDraft,
  companionSelectedScene,
  getProviderProbe: () => companionProviderProbe,
  getSetupTestResult: () => companionSetupTestResult,
})
let agentWorkTranscriptHtml = () => ''

// Один выбор «подключение → модель» на все экраны. До него провайдера
// выбирали заново в четырёх местах, и эти места расходились между собой.
const { connectionManagerHtml, connectionFormHtml, connectionLabel, modelChoiceHtml, modelFacts, modelChipHtml, handleModelChipAction, closeModelPickerOutside } = createModelPicker({
  getState: () => state,
  escapeHtml: esc,
  connectionStatusLabels,
  connectionOrbHtml: status => connectionOrbHtml(status),
  providerCatalog,
})

function normalizeRunnableAgent(agent) {
  return agent ? { ...agent, model: agent.model || agent.primaryModel || '' } : agent
}
function hubAgents() {
  // Наличие поля означает Hub-ядро даже тогда, когда старый backend прислал
  // null вместо пустого массива. Null — это пустой ростер, а не разрешение
  // показывать два встроенных legacy-профиля как уже созданных персонажей.
  const hasProjectRoster = Boolean(state.boot) && Object.prototype.hasOwnProperty.call(state.boot, 'projectAgents')
  const source = hasProjectRoster
    ? (Array.isArray(state.boot.projectAgents) ? state.boot.projectAgents : [])
    : (state.boot?.profiles || [])
  return source.map(normalizeRunnableAgent)
}
function blueprintById(id) {
  return (state.boot?.blueprints || []).find(item => item.id === id)
}
function agentById(id) {
	const project = (state.boot?.projectAgents || []).find(item => item.id === id)
	if (project) return normalizeRunnableAgent(project)
	return normalizeRunnableAgent((state.boot?.profiles || []).find(item => item.id === id))
}
// Названия из старых данных могут содержать всю разговорную команду. На
// рабочем экране человеку нужна тема задачи, а не повтор его сообщения.
function compactQuestTitle(value) {
  let title = String(value || '').trim().split(/\r?\n/, 1)[0]
  title = title.replace(/^(?:создай|создать|подготовь|подготовить|поставь|поставить)\s+квест(?:\s+на)?\s*[:—-]?\s*/i, '')
  const sentenceEnd = title.search(/[.!?](?:\s|$)/)
  if (sentenceEnd >= 0) title = title.slice(0, sentenceEnd)
  title = title.trim().replace(/[.!?,;:\s]+$/g, '')
  if (!title) return 'Задача без названия'
  const limit = 72
  if ([...title].length > limit) {
    let short = [...title].slice(0, limit).join('')
    const wordEnd = short.lastIndexOf(' ')
    if (wordEnd >= Math.floor(limit / 2)) short = short.slice(0, wordEnd)
    title = short.trim().replace(/[.,;:!—-]+$/g, '') + '…'
  }
  return title.charAt(0).toUpperCase() + title.slice(1)
}
function compactTeamName(team, quest) {
  const raw = String(team?.name || '').trim().replace(/^(?:party|отряд)\s*[·:—-]\s*/i, '')
  return compactQuestTitle(raw || quest?.title || 'Команда квеста')
}
function compactQuestDescription(value, title) {
  let description = String(value || '').trim()
  if (!description) return ''
  description = description.replace(/^(?:создай|создать|подготовь|подготовить|поставь|поставить)\s+квест(?:\s+на)?\s*[:—-]?\s*/i, '')
  const firstEnd = description.search(/[.!?](?:\s|$)/)
  if (firstEnd >= 0 && compactQuestTitle(description.slice(0, firstEnd)) === compactQuestTitle(title)) {
    description = description.slice(firstEnd + 1).trim()
  }
  if (description === String(title || '').trim()) return ''
  const runes = [...description]
  return runes.length > 240 ? runes.slice(0, 239).join('').trim() + '…' : description
}
function agentRuntimeLabel(agent) {
  const provider = String(agent?.provider || '').toLowerCase()
  if (provider === 'ollama') return 'Ollama'
  if (provider === 'openai' || provider === 'openai-compatible') return 'OpenAI'
  if (provider === 'anthropic') return 'Anthropic'
  if (provider === 'azure-openai') return 'Azure'
  return String(agent?.primaryModel || agent?.model || agent?.provider || '').trim()
}
function agentDisplayName(agent) {
  if (!agent) return 'Агент'
  const name = String(agent.name || 'Агент').trim()
  const peers = hubAgents().filter(item => String(item.name || '').trim().toLowerCase() === name.toLowerCase())
  if (peers.length < 2) return name
  // The runtime already has its own secondary label and can change after a
  // profile edit. Keep duplicate names stable and unambiguous instead of
  // repeating the provider in both the title and metadata.
  return `${name} ${Math.max(1, peers.findIndex(item => item.id === agent.id) + 1)}`
}
function compactExecutionTask(item, quest) {
  const raw = String(item?.task || quest?.title || 'Запуск').replace(/^(?:primary|reviewer|planner|implementer)\s*:\s*/i, '')
  return compactQuestTitle(raw)
}
// Есть ли в ростере хоть кто-нибудь.
//
// Тем же вопросом ядро решает, дописывать ли в задание «Подготовка исполнителя»
// и создавать ли на запуске квест создания агента. Карточка обязана спрашивать
// заново на каждой отрисовке: задание — снимок на момент постановки, а ростер
// живёт дальше и пустеет.
//
// Считаем именно наличие, а не готовность: profileReadiness на вызове сама
// просит у ядра разбор возможностей, и рисование ленты начало бы опрашивать
// весь ростер. Пустой ростер двусмысленным не бывает.
function rosterHasAgent() {
  return hubAgents().length > 0
}
function hubModeAvailable() {
  return state.boot?.projectAgents !== undefined || state.boot?.blueprints !== undefined
}
function isCompanionOnboardingStep(step) {
  return step === 'companion-choose' || step === 'companion-config' || step === 'companion-brain'
}
function isOrchestratorOnboardingStep(step) {
  return step === 'orchestrator-choose' || step === 'orchestrator-brain'
}
function isSystemOnboardingStep(step) {
  return isOrchestratorOnboardingStep(step)
}
function companionOnboardingFinished() {
  if (Object.prototype.hasOwnProperty.call(onboardingDraft, 'companionFinished')) return Boolean(onboardingDraft.companionFinished)
  const config = state.boot?.companion
  // Новое ядро отличает автоматически созданный рабочий default от выбора
  // человека. Старое поля не знает — для него сохраняем прежнюю совместимость.
  return config?.configured !== undefined ? Boolean(config.configured) : Boolean(config?.id)
}
function orchestratorOnboardingFinished() {
  if (Object.prototype.hasOwnProperty.call(onboardingDraft, 'orchestratorFinished')) return Boolean(onboardingDraft.orchestratorFinished)
  return Boolean(state.boot?.orchestrator?.id)
}
function canEnterOnboardingStep(step) {
  return ONBOARDING_STEPS.some(item => item.id === step)
}
function onboardingLockReason(step) {
  return canEnterOnboardingStep(step) ? '' : 'Этот шаг не входит в первый запуск'
}
function firstIncompleteOnboardingStep() {
  const byId = id => ONBOARDING_STEPS.find(item => item.id === id) || ONBOARDING_STEPS[0]
  if (!orchestratorOnboardingFinished()) {
    return isOrchestratorOnboardingStep(onboardingStep) ? byId(onboardingStep) : byId('orchestrator-brain')
  }
  return byId('orchestrator-choose')
}
function onboardingWelcomeResume() {
  const resume = firstIncompleteOnboardingStep()
  return {
    stepId: resume.id,
    label: `Продолжить: ${resume.label}`,
  }
}
// Сводка над рейкой называет номер шага, рисует полосу и объясняет, зачем шаг
// нужен. Названия шага в ней нет намеренно: рейка под ней — вертикальный
// список всех шагов, и открытый в нём подсвечен акцентом в сотне пикселей
// ниже. Название стояло в обеих, и проверка повторов видела его дважды.
// Горизонтальная полоса шагов настройки компаньона — другой случай: там
// названия обрезаются многоточием и уезжают в прокрутку, поэтому подпись в
// шапке остаётся единственным полным именем шага и снимать её нельзя.
function onboardingStepNav(activeStep) {
  const index = Math.max(0, ONBOARDING_STEPS.findIndex(step => step.id === activeStep))
  const progress = Math.round((index / Math.max(1, ONBOARDING_STEPS.length - 1)) * 100)
  const current = ONBOARDING_STEPS[index] || ONBOARDING_STEPS[0]
  const rail = ONBOARDING_CHAPTERS.map(chapter => {
    const currentChapter = chapter.steps.includes(activeStep)
    const done = chapter.steps.every(id => ONBOARDING_STEPS.findIndex(item => item.id === id) < index)
    const buttons = chapter.steps.map(id => {
      const step = ONBOARDING_STEPS.find(item => item.id === id)
      const i = ONBOARDING_STEPS.findIndex(item => item.id === id)
      const locked = !canEnterOnboardingStep(id)
      const reason = onboardingLockReason(id)
      return `<button type="button" class="${id === activeStep ? 'on' : i < index ? 'done' : ''}${locked ? ' locked' : ''}" data-action="onboarding-step" data-step="${esc(id)}" title="${esc(reason || step?.hint || '')}" ${locked ? 'disabled' : ''}>${esc(step?.label || id)}</button>`
    }).join('')
    return `<div class="onboarding-chapter${currentChapter ? ' current' : ''}${done ? ' done' : ''}"><small>${esc(chapter.label)}</small>${buttons}</div>`
  }).join('')
  return `<nav class="onboarding-nav" aria-label="Шаги онбординга"><div class="create-flow-progress"><span>ШАГ ${String(index + 1).padStart(2, '0')} / ${String(ONBOARDING_STEPS.length).padStart(2, '0')}</span><div class="create-live-rail"><i style="width:${progress}%"></i></div><small>${esc(current.why || '')}</small></div><div class="onboarding-step-rail">${rail}</div>${onboardingLockNotice
    ? `<p class="onboarding-lock-note" role="status">${esc(onboardingLockNotice)}</p>`
    : ''}</nav>`
}
function formatCents(value) {
  const cents = Number(value || 0)
  if (!cents) return '—'
	return `$${(cents / 100).toFixed(cents % 100 === 0 ? 0 : 2)}`
}

const {
  runIsFinished, runIsLive, runIsActiveNow, plannerFallbackBannerHtml,
  currentHubQuest, currentHubTeam, activeExecutions, executionRunId, executionGuaranteesHtml,
  execControlsHtml, flowApprovalStripHtml, questProgressHtml, questStatusStripHtml,
  sessionKeepUndoBarHtml, sessionRunChangedFilesHtml, partyStatusStripHtml,
  contextInspectorPanelHtml, usageSummary, questBudgetPhasesHtml,
  pendingChangeSets, pendingQuestProposals, pendingActionProposals,
} = createHubRuntimeUi({
  getState: () => state,
  esc,
  countOf,
  statusLabels,
  hubAgents,
  agentDisplayName,
  formatCents,
  getPlannerFallbackNotice: () => plannerFallbackNotice,
  getContextInspectorRunId: () => contextInspectorRunId,
  getContextInspector: () => contextInspector,
  getContextInspectorStatus: () => contextInspectorStatus,
  getContextInspectorNotice: () => contextInspectorNotice,
  getKeptRunId: () => keptRunId,
})

// ── Боковая панель квеста ──────────────────────────────────────────────────
// Сводит три вещи, которые до сих пор жили на разных экранах: что агент видел,
// чем кончились прошлые попытки и кто сейчас в отряде. Без этого отладка агента
// была гаданием — по одному экрану нельзя понять, почему он сделал глупость.

function questContextBarsHtml(details) {
  const items = Array.isArray(details?.run?.contextItems) ? details.run.contextItems : []
  if (items.length === 0) {
    return `<p class="hall-note">Вложенного контекста нет — агент работает только с тем, что нашёл сам.</p>`
  }
  // Полоса показывает долю элемента в общем объёме вложений, а не абсолютный
  // размер: важно, что именно съело контекст, а не сколько там килобайт.
  const total = items.reduce((sum, item) => sum + Number(item.extractedSize || item.size || 0), 0) || 1
  return items.map(item => {
    const size = Number(item.extractedSize || item.size || 0)
    const percent = Math.max(2, Math.round((size / total) * 100))
    return `<div class="hall-context-item">
      <div class="hall-spread">
        <span class="hall-context-path">${esc(item.path || item.label || item.kind || 'вложение')}</span>
        <span class="hall-context-share">${percent}%</span>
      </div>
      <div class="hall-track"><i style="width:${percent}%"></i></div>
    </div>`
  }).join('')
}

function questRunHistoryHtml(details) {
  const runs = (state.boot?.runs || []).filter(run => run.id !== details?.run?.id).slice(0, 4)
  if (runs.length === 0) {
    return `<p class="hall-note">Прошлых прогонов нет.</p>`
  }
  const diagnosticsByRun = new Map((state.boot?.runDiagnostics || []).map(item => [item.runId, item]))
  return runs.map(run => {
    const diagnostics = diagnosticsByRun.get(run.id)
    const verification = diagnostics?.verification
    // Метка честная: «с доказательством» только когда верификатор отработал.
    const mark = !verification?.required ? '·' : verification.recorded ? '✓' : '!'
    const proof = !verification?.required ? 'проверка не требовалась'
      : verification.recorded ? 'проверка зафиксирована' : 'без доказательства'
    return `<button type="button" class="hall-panel-row hall-item" data-action="load-run" data-id="${esc(run.id)}">
      <span class="hall-chip ${mark === '!' ? 'is-hot' : 'is-dim'}">${mark}</span>
      <div class="hall-item-text">
        <b>${esc(run.task || run.id)}</b>
        <small>${esc(proof)}</small>
      </div>
    </button>`
  }).join('')
}

function questAsideHtml(details) {
  if (!details?.run) return ''
  const task = details.run.task || ''
  return `<aside class="hall-quest-aside">
    <section>
      <header><b>ЧТО АГЕНТ ВИДЕЛ</b></header>
      <div class="hall-aside-stack">
        ${questContextBarsHtml(details)}
        <button class="hall-btn is-sm" data-action="load-context-inspector" data-run-id="${esc(details.run.id)}">РАЗБОР КОНТЕКСТА</button>
        ${runIsActiveNow(details.run)
          ? `<form class="exec-inline-form" data-exec-form="forbid" data-run-id="${esc(details.run.id)}"><input type="text" placeholder="Запретить путь…"><button type="submit" class="small-button" title="Запретить файл">⊘</button></form>`
          : ''}
      </div>
    </section>
    <section>
      <header><b>ИСТОРИЯ ПРОГОНОВ</b></header>
      <div>${questRunHistoryHtml(details)}</div>
      <div class="hall-aside-stack is-tight">
        <button class="hall-btn is-sm" data-action="tab" data-tab="history">СРАВНИТЬ 2 ПОСЛЕДНИХ</button>
        <button class="hall-btn is-sm is-dashed" data-action="repeat-quest" data-task="${esc(task)}">ПОВТОРИТЬ НА ДРУГОЙ МОДЕЛИ</button>
      </div>
    </section>
  </aside>`
}

// ── История по файлу ───────────────────────────────────────────────────────
// Хроника отвечает «что делал прогон #47». Человек спрашивает «что случилось с
// engine.go». Тот же неизменяемый факт, повёрнутый вокруг файла.

function filesTouchedByAgents() {
  const paths = new Set()
  for (const patch of state.boot?.changes || []) if (patch.path) paths.add(patch.path)
  for (const set of state.boot?.changeSets || []) {
    for (const item of set.items || []) if (item.path) paths.add(item.path)
  }
  return [...paths].sort()
}

function fileHistoryEntryHtml(entry) {
  const operations = { create: 'создан', modify: 'изменён', delete: 'удалён' }
  const statuses = { applied: 'применено', reverted: 'откачено', pending: 'ждёт', approved: 'одобрено', rejected: 'отклонено', conflict: 'конфликт', superseded: 'поглощено' }
  const origin = entry.source === 'patch'
    ? `прогон ${entry.runId || '—'}${entry.tool ? ` · ${esc(entry.tool)}` : ''}`
    : `набор ${entry.changeSetId}${entry.title ? ` · ${esc(entry.title)}` : ''}`
  return `<div class="hall-panel-row hall-item">
    <span class="hall-chip ${entry.status === 'reverted' ? 'is-dim' : ''}">${esc(operations[entry.operation] || entry.operation || '')}</span>
    <div class="hall-item-text">
      <b>${esc(statuses[entry.status] || entry.status || '')}</b>
      <small>${origin}</small>
    </div>
    ${entry.revertible
      ? `<button class="hall-btn is-sm" data-action="revert-file-entry" data-path="${esc(entry.revertPath)}" data-id="${esc(entry.id)}">ОТКАТИТЬ</button>`
      : `<span class="hall-item-flag">необратимо</span>`}
  </div>`
}

function fileHistoryView() {
  const files = filesTouchedByAgents()
  if (fileHistoryStatus === 'idle' && !fileHistoryPath && files.length) {
    fileHistoryPath = files[0]
    fileHistoryStatus = 'loading'
    setTimeout(() => vscode.postMessage({ type: 'loadFileHistory', path: fileHistoryPath }), 0)
  }
  const entries = fileHistoryData?.entries || []
  return shell(`<div class="hall-split">
    <div class="hall-queue">
      <header>
        <b>ФАЙЛЫ · ${files.length}</b>
        <small>тронуты агентами</small>
      </header>
      <div class="hall-queue-list" data-keynav="column" aria-label="Файлы с историей правок">
        ${files.map(path => `<button class="hall-queue-item is-path ${path === fileHistoryPath ? 'is-active' : ''}" data-action="pick-file-history" data-path="${esc(path)}"${path === fileHistoryPath ? ' aria-current="true"' : ''}><span class="title">${esc(path)}</span></button>`).join('')
          || '<div class="hall-panel-row"><p class="hall-note">Агенты пока ничего не меняли.</p></div>'}
      </div>
    </div>
    <div class="hall-page">
      <div class="hall-title">
        <span class="kicker">ИСТОРИЯ ФАЙЛА</span>
        <h1 class="is-mono">${esc(fileHistoryPath || '—')}</h1>
      </div>
      ${fileHistoryData ? `<div class="hall-tally">
        <span>всего ${fileHistoryData.total}</span><span>применено ${fileHistoryData.applied}</span><span>откачено ${fileHistoryData.reverted}</span><span>ждёт ${fileHistoryData.pending}</span>
      </div>` : ''}
      <section class="hall-panel">
        <header><b>ЖУРНАЛ ПРАВОК</b><small>неизменяемая история</small></header>
        ${entries.length ? entries.map(fileHistoryEntryHtml).join('')
          : `<div class="hall-panel-row"><p class="hall-note">${fileHistoryStatus === 'loading' ? 'Спрашиваю ядро — правки этого файла ещё не пришли.' : fileHistoryStatus === 'error' ? 'Не удалось получить историю файла — правки неизвестны, а не отсутствуют.' : 'По этому файлу правок нет.'}</p></div>`}
      </section>
    </div>
  </div>`)
}

function statisticsPanelHtml() {
  const boot = state.boot || {}
  const usage = usageSummary(boot.usageRecords)
  const quest = currentHubQuest()
  const runs = boot.runs || []
  const completed = runs.filter(item => item.status === 'completed').length
  const agents = hubAgents()
  return `<section class="hub-statistics accent-mana"><header class="section-title"><span>СТАТИСТИКА</span><em>${countOf(usage.count, 'запись', 'записи', 'записей')}</em></header><div class="hub-stat-grid"><span><small>АГЕНТЫ</small><b>${agents.length}</b></span><span><small>КВЕСТЫ</small><b>${(boot.quests || []).length || runs.length}</b></span><span><small>ЗАВЕРШЕНО</small><b>${completed}</b></span><span><small>ТОКЕНЫ</small><b>${usage.totalTokens.toLocaleString('ru-RU')}</b></span><span><small>ПОТОЛОК КВЕСТА</small><b>${quest?.budgetTokens ? `${Number(quest.budgetTokens).toLocaleString('ru-RU')} ток.` : quest?.budgetCents ? formatCents(quest.budgetCents) : '—'}</b></span></div>${questBudgetPhasesHtml(quest, usage)}<footer class="hub-card-footer"><button type="button" class="secondary" data-action="tab" data-tab="statistics">Подробная статистика →</button><button type="button" class="secondary" data-action="tab" data-tab="history">Хроника запусков</button></footer></section>`
}
const changeSetViews = createChangeSetViews({
  getState: () => state,
  shell: (...args) => shell(...args),
  escapeHtml: esc,
  countOf,
  pendingChangeSets,
  changeSetStatusLabels,
  toolName,
  agentById,
  getStatusLabels: () => statusLabels,
})
function changeSetCardHtml(set, compact) { return changeSetViews.changeSetCardHtml(set, compact) }
function changeSetsView() { return changeSetViews.changeSetsView() }
function journalView() { return changeSetViews.journalView() }

// Доказательство завершения относится к карточке прогона, а не к Change Set:
// оставляем его в контроллере выполнения при выносе Git-review renderer.
function completionProofHtml(details) {
  const run = details?.run
  if (!run || runIsLive(run)) return ''
  const verification = details?.diagnostics?.verification
  if (!verification) return ''
  if (!verification.required) {
    return `<div class="hall-proof"><b>ПРОВЕРКА НЕ ТРЕБОВАЛАСЬ</b><span>Квест не просил тестов и агент не менял файлы при доступном верификаторе.</span></div>`
  }
  if (verification.recorded) {
    const count = Number(verification.successfulCommands || 0)
    return `<div class="hall-proof is-verified"><b>ЗАВЕРШЁН С ДОКАЗАТЕЛЬСТВОМ</b><span>Успешная проверка зафиксирована после последнего принятого изменения${count ? ` · успешных команд: ${count}` : ''}.</span></div>`
  }
  return `<div class="hall-proof"><b>ЗАВЕРШЁН БЕЗ ДОКАЗАТЕЛЬСТВА</b><span>Проверка требовалась, но успешного запуска верификатора после последнего изменения не зафиксировано. Текстовое утверждение агента доказательством не считается.</span></div>`
}

function questImportanceLabel(value) {
  return ({ normal: 'обычный', important: 'важный', critical: 'критический' })[value] || value || 'обычный'
}
function questProposalEditorHtml(item) {
  return proposalEditorHtml({ ...item, ...(proposalEditDrafts.get(item.id) || {}) }, hubAgents(), state.boot?.flows || [], esc, agentClass)
}
function proposalDecisionPayload(id) {
  return readProposalDecisionEditor(root, id, taskProposalById(id))
}
function companionActionDecisionPayload(id) {
  const name = root.querySelector(`[data-companion-action-field="name"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
  const description = root.querySelector(`[data-companion-action-field="description"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
  const roleDescription = root.querySelector(`[data-companion-action-field="roleDescription"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
  const mission = root.querySelector(`[data-companion-action-field="mission"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
  const agentIds = [...root.querySelectorAll(`[data-companion-action-field="agentId"][data-id="${CSS.escape(id)}"]:checked`)].map(input => input.value)
  const instructions = root.querySelector(`[data-companion-action-field="instructions"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
  const toolChecks = [...root.querySelectorAll(`[data-companion-action-field="requiredTool"][data-id="${CSS.escape(id)}"]`)]
  const requiredToolsInput = root.querySelector(`[data-companion-action-field="requiredTools"][data-id="${CSS.escape(id)}"]`)
  let requiredTools
  if (toolChecks.length) requiredTools = toolChecks.filter(item => item.checked).map(item => item.value)
  else if (requiredToolsInput) requiredTools = requiredToolsInput.value.split(/[\r\n,]+/).map(value => value.trim()).filter(Boolean)
  return { name, description, roleDescription, mission, agentIds, instructions, requiredTools }
}
function companionActionFooter(item, applyLabel) {
  const editing = companionActionEditId === item.id
  const applying = companionActionApplying.has(item.id)
  const modifying = companionActionModifying.has(item.id)
  const busy = applying || modifying
  return `<footer><button type="button" class="primary" data-action="companion-action-apply" data-id="${esc(item.id)}" ${busy ? 'disabled' : ''}>${applying ? 'СОЗДАЁМ…' : esc(applyLabel)}</button><button type="button" class="secondary" data-action="companion-action-modify" data-id="${esc(item.id)}" ${busy ? 'disabled' : ''}>${modifying ? 'СОХРАНЯЕМ…' : editing ? 'Сохранить черновик' : 'Изменить'}</button><button type="button" class="secondary" data-action="companion-action-ignore" data-id="${esc(item.id)}" ${busy ? 'disabled' : ''}>Игнорировать</button></footer>`
}
// Карточки предложений компаньона: панель Хаба, его же классы и его регистр.
//
// В ленте Мастера создание агента рисуется не отсюда, а своей карточкой
// (ui/client/master-agent-card.js): там другой регистр, и здешняя разметка
// стояла в разговоре без меры колонки и без геометрии Чертога. Ветка
// create_agent осталась ради панели компаньона, где предложение приходит тем
// же маршрутом, но живёт среди своих.
function companionActionProposalHtml(item) {
  if (item.kind === 'create_agent') {
    const agent = item.agent || {}
    const editing = companionActionEditId === item.id
    const pending = companionActionEditDrafts.get(item.id)
    const shownAgent = pending ? {
      ...agent,
      name: pending.name !== undefined ? pending.name : agent.name,
      roleDescription: pending.roleDescription !== undefined ? pending.roleDescription : agent.roleDescription,
      mission: pending.mission !== undefined ? pending.mission : agent.mission,
    } : agent
    const editor = editing ? `<div class="companion-action-editor"><label>Имя агента<input data-companion-action-field="name" data-id="${esc(item.id)}" maxlength="120" value="${esc(shownAgent.name || '')}"></label><label>Роль<textarea data-companion-action-field="roleDescription" data-id="${esc(item.id)}" rows="2" maxlength="1000">${esc(shownAgent.roleDescription || '')}</textarea></label><label>Миссия<textarea data-companion-action-field="mission" data-id="${esc(item.id)}" rows="3" maxlength="4096">${esc(shownAgent.mission || '')}</textarea></label></div>` : ''
    return `<article class="quest-proposal-card companion-action-card"><header><strong>${esc(item.title)}</strong><em>ЧЕРНОВИК АГЕНТА</em></header><p>${esc(item.rationale || '')}</p><div class="companion-flow-preview"><span><small>ШАБЛОН</small><b>${esc(agent.blueprintId || '—')}</b></span><span><small>МОДЕЛЬ</small><b>${esc(agent.primaryModel || 'auto')}</b></span><span><small>ИНСТРУМЕНТЫ</small><b>${(agent.allowedTools || []).length}</b></span></div><div class="companion-agent-summary"><strong>${esc(agent.roleDescription || 'Роль не указана')}</strong><small>${esc(agent.mission || '')}</small><p>${(agent.allowedTools || []).map(tool => `<code>${esc(tool)}</code>`).join(' ') || 'Без инструментов'}</p></div>${editor}${companionActionFooter(item, 'Создать агента')}</article>`
  }
  if (item.kind === 'create_team') {
    const team = item.team || {}
    const editing = companionActionEditId === item.id
    const pending = companionActionEditDrafts.get(item.id)
    const shownTeam = pending ? {
      ...team,
      name: pending.name !== undefined ? pending.name : team.name,
      description: pending.description !== undefined ? pending.description : team.description,
      agentIds: Array.isArray(pending.agentIds) ? pending.agentIds : team.agentIds,
    } : team
    const selected = new Set(shownTeam.agentIds || [])
    const members = (shownTeam.agentIds || []).map(id => agentById(id)).filter(Boolean)
    const editor = editing ? `<div class="companion-action-editor"><label>Название отряда<input data-companion-action-field="name" data-id="${esc(item.id)}" maxlength="120" value="${esc(shownTeam.name || '')}"></label><label>Описание<textarea data-companion-action-field="description" data-id="${esc(item.id)}" rows="3" maxlength="4096">${esc(shownTeam.description || '')}</textarea></label><fieldset><legend>Состав</legend><small>Выберите от 1 до 8 исполнителей.</small><div class="check-grid">${hubAgents().map(agent => `<label><input type="checkbox" data-companion-action-field="agentId" data-id="${esc(item.id)}" value="${esc(agent.id)}" ${selected.has(agent.id) ? 'checked' : ''}> ${esc(agent.name)}</label>`).join('')}</div></fieldset></div>` : ''
    return `<article class="quest-proposal-card companion-action-card"><header><strong>${esc(item.title)}</strong><em>ЧЕРНОВИК ОТРЯДА</em></header><p>${esc(item.rationale || '')}</p><div class="companion-team-members">${members.map(agent => `<span><b>${esc(agent.name)}</b><small>${esc(agent.roleDescription || agentClass(agent))}</small></span>`).join('') || '<small>Состав не выбран</small>'}</div>${editor}${companionActionFooter(item, 'Создать отряд')}</article>`
  }
  if (item.kind === 'create_skill') {
    const skill = item.skill || {}
    const editing = companionActionEditId === item.id
    const pending = companionActionEditDrafts.get(item.id)
    const shownSkill = pending ? {
      ...skill,
      name: pending.name !== undefined ? pending.name : skill.name,
      description: pending.description !== undefined ? pending.description : skill.description,
      instructions: pending.instructions !== undefined ? pending.instructions : skill.instructions,
      requiredTools: Array.isArray(pending.requiredTools) ? pending.requiredTools : skill.requiredTools,
    } : skill
    const requiredTools = Array.isArray(shownSkill.requiredTools) ? shownSkill.requiredTools : []
    const selectedTools = new Set(requiredTools)
    const permissions = Object.entries(skill.permissionDelta || {})
    const catalog = (state.boot?.toolCatalog || []).slice(0, 24)
    const toolEditor = catalog.length
      ? `<fieldset><legend>Требуемые умения</legend><div class="check-grid skill-tool-grid">${catalog.map(tool => `<label><input type="checkbox" data-companion-action-field="requiredTool" data-id="${esc(item.id)}" value="${esc(tool.name)}" ${selectedTools.has(tool.name) ? 'checked' : ''}> ${esc(tool.displayName || tool.name)}</label>`).join('')}</div><small>Skill не расширяет permissions агента — tools должны уже быть в allowlist.</small></fieldset>`
      : `<label>Требуемые умения — по одному на строку<textarea data-companion-action-field="requiredTools" data-id="${esc(item.id)}" rows="4">${esc(requiredTools.join('\n'))}</textarea></label>`
    const editor = editing ? `<div class="companion-action-editor"><label>Название Skill<input data-companion-action-field="name" data-id="${esc(item.id)}" maxlength="120" value="${esc(shownSkill.name || '')}"></label><label>Описание<textarea data-companion-action-field="description" data-id="${esc(item.id)}" rows="2" maxlength="4096">${esc(shownSkill.description || '')}</textarea></label><label>Инструкции<textarea data-companion-action-field="instructions" data-id="${esc(item.id)}" rows="7" maxlength="32768">${esc(shownSkill.instructions || '')}</textarea></label>${toolEditor}</div>` : ''
    return `<article class="quest-proposal-card companion-action-card"><header><strong>${esc(item.title)}</strong><em>SKILL DRAFT</em></header><p>${esc(item.rationale || '')}</p><div class="companion-flow-preview"><span><small>TOOLS</small><b>${requiredTools.length}</b></span><span><small>SCRIPTS</small><b>${(skill.scripts || []).length}</b></span><span><small>PERMISSIONS</small><b>${permissions.length ? permissions.length : 'НЕ РАСШИРЯЕТ'}</b></span></div><div class="companion-agent-summary"><strong>${esc(skill.description || 'Описание не указано')}</strong><small class="companion-skill-instructions">${esc(skill.instructions || '')}</small><p>${requiredTools.map(tool => `<code>${esc(tool)}</code>`).join(' ') || 'Без обязательных tools'}</p>${permissions.length ? `<ul>${permissions.map(([key, value]) => `<li>${esc(key)} = ${esc(value)}</li>`).join('')}</ul>` : '<p>Скрытых требований доступа нет. Tool grants агента не изменяются.</p>'}<p class="muted">После Apply definition попадёт в каталог и будет подключён к проекту. Чтобы агент использовал Skill — отметьте его в конструкторе.</p></div>${editor}${companionActionFooter(item, 'Создать и подключить Skill')}</article>`
  }
  const flow = item.flow || {}
  const editing = companionActionEditId === item.id
  const pending = companionActionEditDrafts.get(item.id)
  const shownFlow = pending ? {
    ...flow,
    name: pending.name !== undefined ? pending.name : flow.name,
    description: pending.description !== undefined ? pending.description : flow.description,
  } : flow
  const nodes = Array.isArray(flow.nodes) ? flow.nodes : []
  const edges = Array.isArray(flow.edges) ? flow.edges : []
  const nodePreview = nodes.map(node => {
    const agent = node.agentId ? agentById(node.agentId) : undefined
    return `<li><b>${esc(node.name || node.kind)}</b><small>${esc(node.kind)}${agent ? ` · ${esc(agent.name)}` : ''}</small></li>`
  }).join('')
  const editor = editing ? `<div class="companion-action-editor"><label>Название Flow<input data-companion-action-field="name" data-id="${esc(item.id)}" maxlength="200" value="${esc(shownFlow.name || '')}"></label><label>Описание<textarea data-companion-action-field="description" data-id="${esc(item.id)}" rows="3" maxlength="4096">${esc(shownFlow.description || '')}</textarea></label></div>` : ''
  return `<article class="quest-proposal-card companion-action-card"><header><strong>${esc(item.title)}</strong><em>FLOW DRAFT</em></header><p>${esc(item.rationale || '')}</p><div class="companion-flow-preview"><span><small>УЗЛЫ</small><b>${nodes.length}</b></span><span><small>СВЯЗИ</small><b>${edges.length}</b></span><span><small>СОСТОЯНИЕ</small><b>${esc(item.status || 'pending')}</b></span></div><ol>${nodePreview}</ol>${editor}${companionActionFooter(item, 'Создать Flow')}</article>`
}
function companionQuestionsHtml(item) {
  const questions = Array.isArray(item.questions)
    ? item.questions
      .map(question => String(question || '').replace(/(?:&#x20;|&#32;|&nbsp;)/gi, ' ').trim())
      // Versions before this cleanup stored IDE context as pseudo-questions on
      // every answer. Hide those legacy records without touching real model
      // clarifying questions or deleting conversation history.
      .filter(question => question && !/^Разбери цель запуска «[^»]+»[.!?]?$/i.test(question) && !/^Исправь \S+:\d+[.!?]?$/i.test(question))
    : []
  if (!questions.length) return ''
  // Нажатие готовит ответ, а не отправляет вопрос обратно. Раньше чип уточнения
  // работал как готовая реплика человека, и помощник получал собственный вопрос
  // словами собеседника: отвечал на него сам и спрашивал снова. Заготовка «вопрос
  // — » оставляет вопрос в реплике: без него ответ «master» через день не
  // прочитать даже человеку.
  return `<aside class="companion-questions"><strong>Нужно уточнить</strong><div class="companion-question-chips">${questions.map(question => `<button type="button" class="secondary" data-action="companion-prefill" data-prompt="${esc(question + ' — ')}" title="Ответить на этот вопрос">${esc(question)}</button>`).join('')}</div></aside>`
}
function companionQuickPrompts() {
  const ctx = companionIdeContext || {}
  const prompts = []
  if (ctx.file) prompts.push(`Что не так в ${ctx.file}${ctx.line ? `:${ctx.line}` : ''}?`)
  if (ctx.selection && ctx.file) prompts.push(`Разбери выделенный код в ${ctx.file}`)
  // Run/debug targets already have explicit context chips. Repeating them in
  // generic quick prompts made the same action dominate every chat surface.
  if (ctx.failure) prompts.push(`Разбери сбой «${ctx.failure}»`)
  if (Number(ctx.diagnostics) > 0 && !prompts.some(item => item.startsWith('Исправь'))) prompts.push('Исправь текущие ошибки')
  // Про коммит спрашивают часто, а узнать, что помощник это умеет, было негде:
  // историю он показывал заголовками, а изменения — только рабочего дерева.
  for (const item of ['Какие ошибки сейчас?', 'Что изменилось в последнем коммите?', 'Покажи текущие риски', 'Подготовь квест для следующего улучшения']) {
    if (prompts.length >= 5) break
    if (!prompts.includes(item)) prompts.push(item)
  }
  return prompts
}
function companionQuickPromptsHtml(compact = false) {
  const prompts = companionQuickPrompts().slice(0, 2)
  if (!prompts.length) return ''
  return `<div class="companion-quick-prompts${compact ? ' compact' : ''}">${prompts.map(prompt => `<button type="button" class="secondary" data-action="companion-quick-prompt" data-prompt="${esc(prompt)}">${esc(prompt)}</button>`).join('')}</div>`
}
function companionGettingStartedHtml() {
  const discuss = companionQuickPrompts()[0] || 'Что сейчас важно в проекте?'
  const actions = [
    { label: 'Обсудить код', detail: 'Открытый файл и Problems уже в контексте', prompt: discuss, send: true },
    { label: 'Создать агента', detail: 'Роль, модель и инструменты — перед созданием будет карточка', prompt: 'Создай агента для ', send: false },
    { label: 'Создать квест', detail: 'Цель, критерии готовности и подходящий исполнитель', prompt: 'Создай квест: ', send: false },
    { label: 'Подобрать отряд', detail: 'Помощник предложит подходящих агентов из ростера', prompt: 'Собери отряд для задачи: ', send: false },
    // Единственное место, где человек узнаёт о памяти: сказать «запомни» можно
    // и без подсказки, но догадаться, что это работает, — неоткуда.
    { label: 'Запомнить правило', detail: 'Факт закрепится и вернётся в следующие разговоры', prompt: 'Запомни: ', send: false },
  ]
  return `<div class="companion-starters" aria-label="Быстрый старт">${actions.map(item => `<button type="button" class="companion-starter" data-action="${item.send ? 'companion-quick-prompt' : 'companion-prefill'}" data-prompt="${esc(item.prompt)}"><strong>${esc(item.label)}</strong><small>${esc(item.detail)}</small><span aria-hidden="true">→</span></button>`).join('')}</div>`
}
function replaceHtmlNodes(selector, html) {
  for (const node of root.querySelectorAll(selector)) {
    const wrap = document.createElement('div')
    wrap.innerHTML = html
    const fresh = wrap.firstElementChild
    if (fresh) node.replaceWith(fresh)
    else node.remove()
  }
}
function companionInterventionActionHtml(item) {
  if (item.actionKind && item.relatedId) {
    const probe = companionInterventionProbe?.interventionId === item.id && companionInterventionProbe?.connectionId === item.relatedId
      ? companionInterventionProbe
      : undefined
    const label = item.actionKind === 'probe_connection'
      ? (probe?.loading ? 'Проверяем…' : probe ? 'Проверить снова' : (item.actionLabel || 'Проверить связь'))
      : (item.actionLabel || 'Проверить')
    return `<button type="button" class="${item.actionKind === 'message_run' || item.actionKind === 'companion_prompt' ? 'primary' : 'secondary'}" data-action="companion-intervention-action" data-intervention-id="${esc(item.id)}" data-kind="${esc(item.actionKind)}" data-related-id="${esc(item.relatedId)}" data-tab="${esc(item.actionTab || 'quests')}" data-message="${esc(item.actionMessage || '')}" ${probe?.loading ? 'disabled' : ''}>${esc(label)}</button>`
  }
  if (!item.actionTab) return ''
  // В отдельной панели Компаньона внутренней вкладки Гильдии не видно.
  // Поэтому fallback должен открыть окно Хаба, а не молча переключить скрытое
  // состояние этой же webview.
  const action = isCompanionView() ? 'focus-hub' : 'tab'
  return `<button type="button" class="secondary" data-action="${action}" data-tab="${esc(item.actionTab)}">${esc(item.actionLabel || 'Проверить')}</button>`
}

function companionInterventionProbeHtml(item) {
  const probe = companionInterventionProbe
  if (!probe || probe.interventionId !== item.id || probe.connectionId !== item.relatedId) return ''
  if (probe.loading) return '<div class="companion-probe-result loading" role="status"><i></i><div><strong>Проверяем связь…</strong><small>Endpoint, авторизация и каталог моделей</small></div></div>'
  if (probe.connected) return `<div class="companion-probe-result success" role="status"><b>✓</b><div><strong>Связь работает · ${Number(probe.latencyMs || 0).toLocaleString('ru-RU')} мс</strong><small>Сервис ответил, доступно моделей: ${(probe.models || []).length}.</small></div></div>`
  const title = probe.problem || 'Связь не установлена'
  const next = probe.fix || probe.error || probe.message || 'Проверьте адрес, ключ и доступность сервиса.'
  return `<div class="companion-probe-result error" role="alert"><b>!</b><div><strong>${esc(title)}</strong><small>${esc(next)}</small></div></div>`
}
function companionInterventionFileHtml(item) {
  if (!item.relatedPath) return ''
  return `<button type="button" class="secondary" data-action="open-file" data-path="${esc(item.relatedPath)}" data-line="${esc(item.relatedLine || '')}">Открыть файл</button>`
}
// Фокус возвращается в поле после каждой отправки, и обычный focus() тянет за
// собой раскладку: браузер подтягивает поле в видимую область и прокручивает
// ближайшего предка — в боковой панели это `.companion-chat-workspace` с
// `overflow: hidden`. Скроллбара у него нет, вернуть прокрутку нечем, и разговор
// уезжал вверх при каждой реплике. Замер на живом стенде: обычный focus сдвигал
// контейнер на 241px, focus с preventScroll — на ноль. К концу ленты её ведёт
// scrollCompanionThread, и это единственное, что имеет право её двигать.
function focusCompanionInput() {
  requestAnimationFrame(() => {
    const input = root.querySelector('#companion-input')
    if (!input || typeof input.focus !== 'function') return
    input.focus({ preventScroll: true })
    if (typeof input.setSelectionRange === 'function') {
      const end = input.value.length
      try { input.setSelectionRange(end, end) } catch {}
    }
  })
}
function companionPendingCueHtml() {
  const pending = String(companionPendingSend || '').trim()
  if (!pending) return ''
  return `<div class="companion-pending-cue" role="status"><span>Заменит следующее</span><p>${esc(pending)}</p><button type="button" class="secondary" data-action="clear-companion-pending" title="Убрать из очереди">×</button></div>`
}

// Строка над полем ввода — метка вложения, а не второй список фактов.
// Полоса «Сейчас:» стоит прямо над ней и уже называет и выделение, и
// проблемы, и несохранённость, только другими словами («2 в Problems»
// против «2 проблем»): рядом это читается как эхо, и два списка одних и тех
// же фактов приходится сверять глазами. Здесь остаётся адрес — то, что
// уйдёт с сообщением, — и сбой запуска, которого в полосе может не быть.
function companionComposeContextHtml() {
  const ctx = companionIdeContext || {}
  const parts = []
  if (ctx.failure) parts.push('сбой запуска')
  const suffix = parts.slice(0, 2).join(' · ')
  if (ctx.file) {
    const file = String(ctx.file)
    const shortFile = file.split(/[\\/]/).pop() || file
    const location = `${shortFile}:${ctx.line || 1}`
    const prompt = ctx.selection ? `Разбери выделенный код в ${file}` : `Что не так в ${file}${ctx.line ? `:${ctx.line}` : ''}?`
    return `<div class="companion-compose-context"><button type="button" data-action="companion-answer" data-prompt="${esc(prompt)}" title="Контекст IDE: ${esc(file)}">${esc(location)}</button>${suffix ? `<span>${esc(suffix)}</span>` : ''}</div>`
  }
  const active = ctx.failure || ctx.run || ctx.debug
  if (!active && !suffix) return ''
  return `<div class="companion-compose-context"><span>${esc(active || suffix)}</span>${active && suffix ? `<span>${esc(suffix)}</span>` : ''}</div>`
}
function patchCompanionComposeChrome() {
  replaceHtmlNodes('.companion-compose-actions', companionComposeActionsHtml(companionLoading))
  for (const node of root.querySelectorAll('.companion-compose-meta small')) {
    node.textContent = companionComposeMetaHtml(companionLoading, companionPendingSend)
  }
  const pending = companionPendingCueHtml()
  const existing = root.querySelector('.companion-pending-cue')
  if (pending) {
    if (existing) replaceHtmlNodes('.companion-pending-cue', pending)
    else {
      const form = root.querySelector('#companion-form')
      if (form) form.insertAdjacentHTML('beforebegin', pending)
    }
  } else if (existing) {
    existing.remove()
  }
}
// Годится любой ленте: у компаньона и у Мастера «внизу» значит одно и то же.
function threadNearBottom(thread) {
  return !thread || thread.scrollHeight - thread.scrollTop - thread.clientHeight < 72
}
function scrollCompanionThread(force = false) {
  const thread = root.querySelector('#companion-thread')
  if (!thread) return
  if (force || companionAutoFollow || threadNearBottom(thread)) {
    thread.scrollTo({ top: thread.scrollHeight, behavior: 'auto' })
    companionAutoFollow = true
  }
  updateCompanionScrollCue()
}
function replaceCompanionThreadHtml() {
  const thread = root.querySelector('#companion-thread')
  if (!thread) return false
  const follow = companionAutoFollow || threadNearBottom(thread)
  const top = thread.scrollTop
  thread.innerHTML = companionThreadHtml()
  companionAutoFollow = follow
  thread.scrollTop = follow ? thread.scrollHeight : Math.min(top, Math.max(0, thread.scrollHeight - thread.clientHeight))
  updateCompanionScrollCue()
  return true
}
// То же для ленты Мастера. Отдельная функция, а не общая с компаньоном: у лент
// разные источники разметки и разные признаки следования, и параметр вместо
// двух функций спрятал бы это различие за флагом.
function replaceMasterThreadHtml() {
  const thread = root.querySelector('#master-thread')
  if (!thread) return false
  const follow = masterAutoFollow || threadNearBottom(thread)
  const top = thread.scrollTop
  // Уточнения переехали в ленту, и вместе с ними — поле свободного ответа.
  // Замена разметки отбирает у него каретку: фоновое обновление посреди
  // набранного слова выбрасывало бы человека из ответа. Набранное переживает
  // замену само (черновик пишется на каждом вводе), а место в строке — нет.
  const typing = thread.contains?.(document.activeElement) && document.activeElement?.classList?.contains('hall-question-extra')
    ? { key: document.activeElement.closest('[data-question-key]')?.dataset.questionKey, at: document.activeElement.selectionStart }
    : null
  // Якорь живёт снаружи ленты и перерисовку переживает сам: дописывать его к
  // содержимому больше не нужно.
  thread.innerHTML = masterThreadContentHtml()
  if (typing?.key) {
    const field = thread.querySelector(`[data-question-key="${typing.key}"] .hall-question-extra`)
    if (field) { field.focus(); if (typing.at != null) field.setSelectionRange(typing.at, typing.at) }
  }
  masterAutoFollow = follow
  thread.scrollTop = follow ? thread.scrollHeight : Math.min(top, Math.max(0, thread.scrollHeight - thread.clientHeight))
  // Замена разметки стирает и пометки поиска, и якорь: набранное в поиске при
  // этом никуда не делось, и возвращать его руками человек не должен.
  applyMasterFind()
  updateMasterScrollCue()
  applyMasterComposeReserve()
  return true
}
// Поле и кнопка отправки стоят вне ленты, и заменять им разметку незачем:
// достаточно снять запрет и подставить черновик, который ядро уже приняло.
// Замена HTML стоила бы каретки в поле.
// Слот уточнений в карточке ввода.
//
// Пакет вопросов живёт здесь, а досборка после каждого ответа ядра переписывает
// слот — то есть посреди набора. Пока поле в фокусе и пакет тот же, разметку не
// трогаем вовсе: замена стоила бы каретки, а иногда и набранного слова. Когда
// пакет сменился, каретка переносится так же, как в ленте.
function patchMasterQuestionSlot() {
  const slot = root.querySelector('#master-questions-ask')
  if (!slot) return
  const next = masterAskSlotHtml()
  const typing = slot.contains?.(document.activeElement) && document.activeElement?.classList?.contains('hall-question-extra')
    ? { key: document.activeElement.closest('[data-question-key]')?.dataset.questionKey, at: document.activeElement.selectionStart }
    : null
  const pack = slot.querySelector('.hall-questions')?.dataset.pack || ''
  if (typing && next.includes(`data-pack="${pack}"`)) return
  slot.innerHTML = next
  if (typing?.key) {
    const field = slot.querySelector(`[data-question-key="${typing.key}"] .hall-question-extra`)
    if (field) { field.focus(); if (typing.at != null) field.setSelectionRange?.(typing.at, typing.at) }
  }
}
function syncMasterComposeState() {
  patchMasterQuestionSlot()
  // Вкладка задания и панель обновляются тем же путём: ответ хода приходит без
  // полной отрисовки, и без этого вкладка осталась бы с прежним признаком —
  // «обсуждение» над лентой, где уже стоит карточка с рабочей кнопкой запуска.
  syncMasterBriefSurfaces(root)
  const discussion = root.querySelector('#master-discussion-context')
  if (discussion) discussion.innerHTML = masterDiscussionContextHtml()
  const input = root.querySelector('#master-input')
  if (input) {
    // Поле не запирается ходом. Мысль, пришедшая, пока модель думает, должна
    // куда-то записываться; уйдёт она следующей репликой, когда ход закончится.
    input.disabled = false
    if (input.value !== masterDraft) input.value = masterDraft
  }
  // Замок с кнопки снимается здесь, хотя строкой ниже ряд управления обычно
  // заменяется целиком вместе с ней. «Обычно» — не «всегда»: ряда может не
  // оказаться в разметке, и тогда единственным, что отпирает разговор, остаётся
  // эта строка. Убрать её как лишнюю я уже пробовал — разговор встал.
  // Стрелка отправки ищется по своему классу, а не по is-primary: в карточке
  // ввода теперь живут уточнения со своей кнопкой «Продолжить», и она стояла бы
  // в разметке раньше — то есть забирала бы себе запирание на время хода.
  const send = root.querySelector('.hall-compose .hall-compose-send')
  if (send) send.disabled = masterSending
  // Строка под полем меняется вместе с ходом: в ней появляется «Остановить»,
  // подсказка меняет смысл, счётчик пересчитывается. Собирает её тот же код,
  // что и полная отрисовка, — двух источников у одной строки быть не должно.
  const actions = root.querySelector('.hall-compose-actions')
  if (actions) actions.innerHTML = masterComposeActionsInnerHtml()
  patchMasterComposeForm()
  patchMasterComposeNote()
}
function updateCompanionScrollCue() {
  const cue = root.querySelector('[data-action="companion-scroll-latest"]')
  if (cue) cue.hidden = companionAutoFollow
}
function companionIdeNowHtml() {
  const ctx = companionIdeContext || {}
  const chips = []
  if (ctx.file) chips.push({
    label: `Сейчас: ${ctx.file}:${ctx.line || 1}`,
    prompt: `Что не так в ${ctx.file}${ctx.line ? `:${ctx.line}` : ''}?`,
    tone: 'file',
  })
  if (ctx.selection) chips.push({
    label: 'выделение',
    prompt: ctx.file ? `Разбери выделенный код в ${ctx.file}` : 'Разбери выделенный код',
    tone: 'selection',
  })
  if (ctx.language) chips.push({ label: ctx.language, tone: 'meta' })
  if (ctx.dirty) chips.push({ label: 'не сохранён', tone: 'warn' })
  if (Number(ctx.diagnostics) > 0) chips.push({ label: `${ctx.diagnostics} в Problems`, prompt: 'Исправь текущие ошибки', tone: 'warn' })
  if (ctx.run) chips.push({ label: `запуск: ${ctx.run}`, prompt: `Разбери цель запуска «${ctx.run}»`, tone: 'run' })
  if (ctx.debug) chips.push({ label: `отладка: ${ctx.debug}`, prompt: `Разбери отладку «${ctx.debug}»`, tone: 'run' })
  if (ctx.failure) chips.push({ label: `сбой: ${ctx.failure}`, prompt: `Разбери сбой «${ctx.failure}»`, tone: 'fail' })
  if (!chips.length) return '<div class="companion-ide-now muted">Контекст IDE не выбран. Откройте файл — помощник добавит его к следующему вопросу.</div>'
  return `<div class="companion-ide-now">${chips.map(chip => chip.prompt
    ? `<button type="button" class="companion-focus-chip ${esc(chip.tone)}" data-action="companion-answer" data-prompt="${esc(chip.prompt)}">${esc(chip.label)}</button>`
    : `<span class="companion-focus-chip ${esc(chip.tone)}">${esc(chip.label)}</span>`).join('')}</div>`
}
function applyCompanionIdeContext(context = {}) {
  companionIdeContext = context && typeof context === 'object' ? context : {}
  replaceHtmlNodes('.companion-ide-now', companionIdeNowHtml())
  replaceHtmlNodes('.companion-peek-context', companionPeekContextChipHtml())
  const composeContext = companionComposeContextHtml()
  const currentComposeContext = root.querySelector('.companion-compose-context')
  if (currentComposeContext && composeContext) replaceHtmlNodes('.companion-compose-context', composeContext)
  else if (currentComposeContext) currentComposeContext.remove()
  else if (composeContext) root.querySelector('#companion-form')?.insertAdjacentHTML('afterbegin', composeContext)
  const compact = Boolean(root.querySelector('.companion-quick-prompts.compact'))
  if (root.querySelector('.companion-quick-prompts')) {
    replaceHtmlNodes('.companion-quick-prompts', companionQuickPromptsHtml(compact))
  }
}
function companionProviderPresets() {
  const blocked = new Set(['cursor-cli', 'codex-cli', 'claude-code-cli'])
  return (state.boot?.providerCatalog || []).filter(item => !blocked.has(item.kind))
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
  return state.boot?.connections || []
}
function companionSetupDraftFromConfig() {
  const companion = state.boot?.companion || {}
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
  if (!companionSetupDraft) companionSetupDraft = companionSetupDraftFromConfig()
  companionSetupStep = normalizeCompanionSetupStep(companionSetupStep)
  return companionSetupDraft
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
  const draft = sanitizeCompanionSetupDraft(companionSetupDraft || companionSetupDraftFromConfig())
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
  return companionConfigForBrain(draft, { current: state.boot?.companion || {}, connection, preset })
}

function companionSetupValidation(step, value) {
  const draft = sanitizeCompanionSetupDraft(value)
  if (step === 'skills') {
    // Такой навык ядро отвергает вместе со всей настройкой, поэтому отказ
    // называется здесь — на шаге, где отметку видно и есть чем её снять.
    const blocked = companionBlockedSkills(draft)
    if (blocked.length) return `Навык «${blocked[0].name || blocked[0].id}» требует умение вне доступа помощника. Снимите отметку — с ней настройка не сохранится.`
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
  if (!baseUrl) { transientError = 'Укажите адрес сервиса, например https://llmux.company.internal/v1'; render(); return }
  if (providerWantsToken(preset) && !apiKey) { transientError = 'Для этого источника нужен токен.'; render(); return }
  if (provider === 'azure-openai' && !apiVersion) { transientError = 'Для Azure укажите версию API — без неё запрос будет отвергнут.'; render(); return }
  const id = root.querySelector('#connection-id-edit')?.value || ''
  const defaultModel = root.querySelector('#connection-default-model')?.value.trim() || ''
  connectionEditingId = ''
  vscode.postMessage({ type: 'saveConnection', id, provider, presetId, displayName, baseUrl, apiKey, apiVersion, defaultModel })
}
function connectionDrawerHtml(open) {
  return `<details class="hire-drawer creation-drawer"${open ? ' open' : ''}><summary><span class="hire-kicker">СВЯЗИ</span> Новое подключение</summary>${connectionFormHtml('', { nested: true })}</details>`
}
function companionConnectionFieldsHtml(value) {
  const draft = sanitizeCompanionSetupDraft(value)
  const list = companionConnections()
  const selected = list.find(item => item.id === draft.connectionId) || list.find(item => item.isDefault) || list[0]
  const probe = selected
    ? `<button type="button" class="secondary companion-probe-cta" data-action="probe-companion-connection" data-id="${esc(selected.id)}" ${companionProviderProbe?.loading ? 'disabled' : ''}>${esc(companionProbeCtaLabel('existing'))}</button>`
    : ''
  return `${modelChoiceHtml({ connectionId: selected?.id || '', model: draft.model, showTuning: false })}${probe}${companionProbeHtml()}${connectionDrawerHtml(!list.length)}`
}
// Навык помощника доступен ровно тогда, когда все его умения входят в группы
// помощника. Правило то же, что у ядра в Grants.Allows: имени нет в каталоге —
// значит это пользовательский инструмент или опечатка, и группой такое не
// выдаётся.
// Мастер настройки вынесен в модуль; здесь остаются только его состояние и
// обработчики действий, которые это состояние меняют.
const {
  companionSkillBlockers, companionBlockedSkills, companionSkillSelection, companionSetupStatusHtml, companionSetupWizardHtml,
} = createCompanionSetupWizard({
  root,
  esc,
  getState: () => state,
  COMPANION_PRESETS,
  COMPANION_EXAMPLES,
  COMPANION_SETUP_STEPS,
  COMPANION_TOOL_GROUPS,
  COMPANION_SKILL_LIMIT,
  companionModeCardsHtml,
  normalizeCompanionSkillIds,
  sanitizeCompanionSetupDraft,
  companionConnections,
  ensureCompanionSetupDraft,
  companionConnectionFieldsHtml,
  companionPresetStudioHtml,
  companionStyleStudioHtml,
  companionPersonalityControlsHtml,
  companionPersonalityPreviewHtml,
  companionRoleShowcaseHtml,
  companionActCompareHtml,
  companionPredictedReplyHtml,
  companionSetupTestAnswerHtml,
  getCompanionLoading: () => companionLoading,
  getSetupStep: () => companionSetupStep,
  getProviderProbe: () => companionProviderProbe,
  getSetupStatus: () => companionSetupStatus,
  getSetupQuiet: () => companionSetupQuiet,
})

function companionBubbleBodyHtml(content, { streaming = false } = {}) {
  const text = String(content || '')
  if (!text && streaming) {
    return `<div class="companion-msg-body streaming"><span class="companion-stream-cursor" aria-hidden="true"></span></div>`
  }
  const rendered = formatCompanionMarkdown(text)
  return `<div class="companion-msg-body${streaming ? ' streaming' : ''}">${rendered}${streaming ? '<span class="companion-stream-cursor" aria-hidden="true"></span>' : ''}</div>`
}
// Лента компаньона живёт отдельным модулем — как и лента Мастера.
const {
  companionBubbleHtml, companionThreadHtml, companionNudgeBannerHtml, companionSidebarBriefHtml,
  patchCompanionThinkingLabel, patchCompanionStreamingBubble,
} = createCompanionThreadViews({
  live: {
    get companionAppliedNotice() { return companionAppliedNotice },
    get companionAutoFollow() { return companionAutoFollow },
    get companionFeedbackMarks() { return companionFeedbackMarks },
    get companionInterventionProbe() { return companionInterventionProbe },
    get companionLoading() { return companionLoading },
    get companionMessages() { return companionMessages },
    get companionStreamReply() { return companionStreamReply },
    get state() { return state },
  },
  COMPANION_PRESETS, data, esc, root,
  companionBubbleBodyHtml: (...args) => companionBubbleBodyHtml(...args),
  companionGettingStartedHtml: (...args) => companionGettingStartedHtml(...args),
  companionIdeNowHtml: (...args) => companionIdeNowHtml(...args),
  companionInterventionActionHtml: (...args) => companionInterventionActionHtml(...args),
  companionInterventionFileHtml: (...args) => companionInterventionFileHtml(...args),
  companionInterventionProbeHtml: (...args) => companionInterventionProbeHtml(...args),
  companionQuestionsHtml: (...args) => companionQuestionsHtml(...args),
  companionThinkingLabel: (...args) => companionThinkingLabel(...args),
  scrollCompanionThread: (...args) => scrollCompanionThread(...args),
  threadNearBottom: (...args) => threadNearBottom(...args),
})

function companionAppliedNoticeHtml() {
  if (!companionAppliedNotice) return ''
  const status = companionAppliedNotice.status || 'Готово'
  const hint = companionAppliedNotice.hint || 'Компаньон только рекомендовал — открыть в гильдии?'
  // Отказ открывать в гильдии нечего: там ничего не появилось. Кнопка есть
  // только у того, что действительно создано.
  const open = companionAppliedNotice.tab
    ? `<button type="button" class="primary" data-action="focus-hub" data-tab="${esc(companionAppliedNotice.tab)}">Открыть в гильдии</button>`
    : ''
  return `<div class="companion-applied-cue" role="status"><div><strong>${esc(status)}</strong><small>${esc(hint)}</small></div>${open}<button type="button" class="secondary" data-action="dismiss-companion-applied" title="Скрыть уведомление" aria-label="Скрыть уведомление">×</button></div>`
}
// ── Диалог с Мастером — см. ui/client/master-thread-views.js ───────────────

// Счёт ожидания. Пока модель думает, событий не приходит вовсе: строка
// ожидания замирает, и через минуту молчания раздел неотличим от зависшего.
// Правится ровно одна надпись: перерисовка ленты раз в секунду потеряла бы
// прокрутку, выделение и фокус.
function startMasterWaitClock() {
  stopMasterWaitClock()
  masterWaitedSeconds = 0
  // Смоуки разговора рисуют раздел без таймеров: часов там нет и не нужно.
  if (typeof setInterval !== 'function') return
  const started = Date.now()
  masterWaitTimer = setInterval(() => {
    masterWaitedSeconds = Math.round((Date.now() - started) / 1000)
    // Строку рисует лента — «Думаю…» или русское имя инструмента, — а часы
    // только дописывают к ней счёт. Пока они переписывали её целиком, имя
    // инструмента жило ровно одну секунду, после чего сменялось на «Думает…».
    const label = root.querySelector('#master-thread .agent-work-thinking span')
    if (label) {
      const suffix = masterWaitSuffix(masterWaitedSeconds)
      const base = String(label.dataset?.wait || label.textContent || '').split(' · ')[0]
      if (label.dataset) label.dataset.wait = base
      label.textContent = suffix ? base + suffix : base
    }
  }, 1000)
}
function stopMasterWaitClock() {
  if (masterWaitTimer && typeof clearInterval === 'function') clearInterval(masterWaitTimer)
  masterWaitTimer = 0
  masterWaitedSeconds = 0
}

// Реплика по её метке. Панель действий и окно сведений говорят о конкретном
// ходе, и брать его надо из истории, а не из последнего ответа: разбирают чаще
// всего не последний ход.
function masterMessageById(id) {
  const key = String(id || '')
  if (!key) return undefined
  return (masterData?.history || []).find(item => item.id === key)
}
// Вопрос, на который отвечали. Ответ без вопроса объясняет сам себя, а это как
// раз то, что человек и пришёл проверить.
function masterAskBefore(id) {
  const history = masterData?.history || []
  const at = history.findIndex(item => item.id === String(id || ''))
  for (let index = at - 1; index >= 0; index -= 1) {
    if (history[index]?.role === 'user') return String(history[index].content || '')
  }
  return ''
}

// Поле растёт по набранному, а счётчик считает байты — на каждом нажатии.
// Разметку композера при этом не заменяем: замена стоила бы каретки.
function patchMasterCompose(input) {
  if (input) input.rows = masterComposeRows(masterDraft)
  patchMasterComposeNote()
  patchMasterComposeForm()
  const count = root.querySelector('.hall-compose-count')
  if (!count) return
  const state = masterComposeCountState(masterDraft)
  count.className = masterComposeCountClass(state)
  count.textContent = state.text
}
// Пустая ли реплика — состояние формы, а не кнопки: по нему гаснет стрелка
// отправки. Класс считает masterComposeFormClass, тот же, что и при полной
// отрисовке, — иначе набранное и нарисованное разошлись бы.
function patchMasterComposeForm() {
  const form = root.querySelector('.hall-compose')
  if (!form) return
  form.className = masterComposeFormClass(masterDraft)
  // Пустота отмечена и на самой кнопке: класс формы гасит её для глаза, а
  // читалке нужен признак на том узле, который нажимают.
  const send = form.querySelector('.hall-compose-send')
  if (send) send.setAttribute('aria-disabled', String(!String(masterDraft || '').trim()))
  applyMasterComposeReserve()
}
// Отказ по длине снимается вместе с правкой: объяснение, висящее над уже
// исправленной репликой, читается как «всё ещё не так».
function patchMasterComposeNote() {
  const note = root.querySelector('.hall-compose-note')
  if (!note) return
  note.className = masterComposeNote ? 'hall-compose-note' : 'hall-compose-note is-hidden'
  note.textContent = masterComposeNote
}

// Отправка одна на оба пути — кнопку и Enter, — чтобы они не разъезжались.
// Выбранный в списке файл читает расширение — вебвью о дереве проекта не
// знает ничего. Знак «@» с запросом убирается из реплики сразу: файл уже
// приложен, и оставленный знак уехал бы в ядро мусором.
// Запрос файлов уходит с отбивкой: поиск по дереву делает расширение, и
// дёргать его на каждое нажатие значит гонять обход проекта по букве. Ответ
// сверяется с запросом на приёме, так что опоздавший просто не примут.
let masterMentionTimer = 0
function askMasterMention(query) {
  if (masterMentionTimer && typeof clearTimeout === 'function') clearTimeout(masterMentionTimer)
  const ask = () => vscode.postMessage({ type: 'searchMasterContext', query, conversationId: masterClient.active })
  masterMentionTimer = typeof setTimeout === 'function' ? setTimeout(ask, 120) : (ask(), 0)
}

function pickMasterMention(item, at, query) {
  masterDraft = masterMentionStripped(masterDraft, at, String(query || ''))
  persistDraft()
  vscode.postMessage({ type: 'attachMasterContextPath', path: item.path, conversationId: masterClient.active })
  render()
}

function sendMasterMessage(forcedText = '', options = {}) {
  const input = root.querySelector('#master-input')
  const text = String(forcedText || (input ? input.value : masterDraft)).trim()
  // Отправлять нечего — это состояние, а не сбой, но молчать о нём нельзя:
  // кнопка достижима с клавиатуры, и нажатие обязано чем-то ответить.
  if (!text) {
    if (!masterSending) { masterComposeNote = 'Напишите сообщение — отправлять пока нечего.'; render() }
    return
  }
  // Ход ещё идёт: поле открыто, но реплика уйдёт следующей. Молчать об этом
  // нельзя — нажатие выглядит как проглоченное.
  if (masterSending) { masterComposeNote = 'Мастер ещё отвечает. Набранное останется в поле — отправьте его, когда ход закончится.'; render(); return }
  const workMode = masterData?.sessions?.workMode || 'discuss'
  if (workMode === 'agent') {
    const profile = agentById(selectedProfileId) || hubAgents()[0] || (state.boot?.profiles || [])[0]
    if (!profile?.id) {
      masterComposeNote = 'Выберите агента проекта — режим «Агент» пишет через его профиль.'
      render()
      return
    }
    const routing = state.boot?.modelRouting
    if (routing?.codingRequired && routing?.codingReady === false) {
      masterComposeNote = routing.codingBlockReason || 'Сильная модель (coding) не прошла проверку. Откройте Связи и проверьте подключение.'
      render()
      return
    }
    const attachments = masterContextPayload(masterClient.active)
    masterComposeNote = ''
    rememberMasterSent(text)
    if (!forcedText) masterDraft = ''
    masterSending = true
    masterAutoFollow = true
    persistDraft()
    runStarting = true
    vscode.postMessage({
      type: 'startFastAgent',
      profileId: profile.id,
      task: text,
      apiKey,
      contextItems: attachments.filter(item => item.kind !== 'image').map(item => ({
        kind: item.kind || 'file',
        path: item.path || '',
        content: item.content || '',
        label: item.name || item.label || '',
      })),
    })
    render()
    return
  }
  const attachments=masterContextPayload(masterClient.active)
  if(attachments.some(item=>item.kind==='image') && masterData?.supportsImages===false){masterComposeNote='Выберите модель с поддержкой изображений внизу поля сообщения.';render();return}
  const contextSize=attachments.filter(item=>item.kind!=='image').reduce((sum,item)=>sum+[...(item.content || '')].length,0)
  if(Number.isFinite(masterData?.contextBudgetChars)&&contextSize>masterData.contextBudgetChars){masterComposeNote=`Вложения: ${countOf(contextSize, 'символ', 'символа', 'символов')}, доступно ${masterData.contextBudgetChars}. Уберите файлы или выберите фрагменты.`;render();return}
  // Предел у ядра тот же (maxChatMessage), и узнавать о нём после отправки
  // поздно: реплика уже ушла бы из поля, а возвращать её пришлось бы
  // копированием из ленты.
  const bytes = masterMessageBytes(text)
  if (bytes > MASTER_MESSAGE_LIMIT_BYTES) {
    masterComposeNote = oversizedMasterMessageNote(bytes)
    masterDraft = text
    persistDraft()
    render()
    return
  }
  masterComposeNote = ''
  masterClient.acceptTurn({id:'turn_'+Date.now().toString(36)+Math.random().toString(36).slice(2),conversationId:masterClient.active,status:'preparing',reply:''})
  startMasterWaitClock()
  masterSending = true
  rememberMasterSent(text)
  // Поле освобождается сразу: реплика уже в ленте, а следующую мысль надо где-то
  // записать. Отправку до ответа держит запертой кнопка, а не пустое поле.
  if (!forcedText) masterDraft = ''
  // Своя реплика — повод вернуться к концу ленты, даже если человек читал выше.
  masterAutoFollow = true
  persistDraft()
  vscode.postMessage({ type: 'masterChat', message: text, attachments: masterContextPayload(masterClient.active),turnId:masterClient.turns[masterClient.active]?.id, conversationId: masterData?.sessions?.active, taskIntake: true, proposalId: masterDiscussionProposalId || undefined, retry: Boolean(options.retry) })
  render()
}

function companionChatHtml() {
  const companion = state.boot?.companion
  const dismissedCount = Number(state.boot?.companionDismissedCount || 0)
  const interventionHtml = dismissedCount
    ? `<div class="companion-interventions"><header class="companion-intervention-toolbar"><small>Скрыто для текущего состояния: ${dismissedCount}</small><button type="button" class="secondary" data-action="restore-companion-interventions">Вернуть</button></header></div>`
    : ''
  const proposals = pendingQuestProposals()
  const actionProposals = pendingActionProposals()
  const reviewCount = proposals.length + actionProposals.length
  const proposalHtml = proposals.length || actionProposals.length
    ? `<details class="quest-proposal-list companion-review" id="companion-review" open><summary><span>Предложения</span><strong>${reviewCount}</strong><small>требуют подтверждения</small></summary><div class="companion-review-body">${proposals.slice(0, 3).map(item => {
      const editing = proposalEditId === item.id
      const modifying = proposalModifying.has(item.id)
      const agents = (item.teamAgentIds || []).map(id => agentById(id)).filter(Boolean).map(agent => agent.name).join(', ')
      const orchHint = 'Запуск — через карточку наряда у Мастера (Hub v2)'
      if (item.brief) return masterProposalHtml(item)
      return `<article class="quest-proposal-card draft-sheet-card"><header><strong>${esc(item.title)}</strong><em>${esc(questImportanceLabel(item.importance))}</em></header><p>${esc(item.rationale || '')}</p><small>Критериев готовности: ${(item.definitionOfDone || []).length}</small><small class="orchestrator-start-hint">${esc(orchHint)}</small><footer class="proposal-actions"><button type="button" class="primary" data-action="tab" data-tab="master" title="Открыть Мастера">Обсудить с Мастером</button><button type="button" class="secondary" data-action="quest-proposal-ignore" data-id="${esc(item.id)}" title="Скрыть предложение">Отклонить</button></footer></article>`
    }).join('')}${actionProposals.length ? `<div class="companion-action-list">${actionProposals.slice(0, 3).map(companionActionProposalHtml).join('')}</div>` : ''}</div></details>`
    : ''
  const studio = companionSetupOpen ? companionSetupWizardHtml() : ''
  const live = companionLoading || companionMessages.length > 0 || proposals.length > 0 || actionProposals.length > 0
  const interventionBlock = isCompanionPopup() ? '' : interventionHtml
  const popup = isCompanionPopup()
  const connectedModel = Boolean(companion?.provider && companion?.model)
  const setupAction = companionSetupOpen
    ? 'close-companion-setup'
    : 'open-companion-setup'
  const setupLabel = companionSetupOpen ? 'Закрыть' : 'Настроить'
  const setupStepAttr = !companionSetupOpen && connectedModel ? ' data-step="brain"' : ''
  // Обе кнопки очищают ленту, и по прежним подписям — «Новый чат» и «Очистить
  // диалог» — разница не читалась вовсе. Она одна и важная: первая уносит
  // разговор в архив, вторая удаляет его насовсем.
  const clearAction = companionMessages.length
    ? '<button type="button" class="secondary" data-action="clear-companion-history" title="История удаляется без архива и не восстанавливается">Удалить историю · без архива</button>'
    : ''
  const newChatAction = '<button type="button" class="primary" data-action="new-companion-thread" title="Текущий разговор уедет в «Архив диалогов», лента начнётся заново">＋ Новый чат · текущий в архив</button>'
  const archiveAction = '<button type="button" class="secondary" data-action="show-companion-archives">Архив диалогов</button>'
  const chatHeader = popup || companionSetupOpen || isCompanionDock()
    ? ''
    : `<header class="companion-chat-toolbar"><div class="companion-chat-identity"><span class="companion-presence" aria-hidden="true"></span><div><strong>Помощник Point</strong>${modelChipHtml({ target: 'companion', connectionId: state.boot?.companion?.connectionId || '', model: state.boot?.companion?.model || '' })}</div></div><details class="companion-chat-menu"><summary title="Действия" aria-label="Действия">•••</summary><div>${newChatAction}${archiveAction}${clearAction}<button type="button" class="secondary" data-action="${setupAction}"${setupStepAttr}>${esc(setupLabel)}</button></div></details></header>`
  // Подсказка о непрочитанном — навигация внутри ленты, а не главное действие:
  // залитый акцент ставил её вровень с «Отправить» в двух сантиметрах ниже.
  const scrollCue = `<button type="button" class="companion-scroll-latest secondary" data-action="companion-scroll-latest"${companionAutoFollow ? ' hidden' : ''}>↓ Новые сообщения</button>`
  const notices = `${companionIdeNowHtml()}${companionNudgeBannerHtml()}${interventionBlock}${companionAppliedNoticeHtml()}${proposalHtml}`
  const chatWorkspace = companionSetupOpen
    ? ''
    : `<div class="companion-chat-workspace dialogue-first">${chatHeader}<div class="companion-thread-wrap"><div class="companion-thread" id="companion-thread" role="log" aria-live="polite" aria-relevant="additions text" aria-label="Диалог с помощником Point">${companionThreadHtml()}</div>${scrollCue}</div>${notices ? `<div class="companion-chat-notices">${notices}</div>` : ''}${companionPendingCueHtml()}<form id="companion-form" class="companion-compose minimal">${companionComposeContextHtml()}<textarea id="companion-input" rows="1" aria-label="Сообщение помощнику Point" placeholder="Спросите о коде или напишите: «Создай квест…»">${esc(companionDraft)}</textarea><div class="companion-compose-meta"><small>${companionComposeMetaHtml(companionLoading, companionPendingSend)}</small>${companionComposeActionsHtml(companionLoading)}</div></form></div>`
  return `<section class="companion-panel companion-studio${companionSetupOpen ? ' setup-active' : ''}${live ? ' has-thread' : ''}${popup ? ' popup-lean' : ''}${isCompanionPeek() ? ' peek-lean' : ''}${isCompanionSidebar() ? ' sidebar-lean' : ''}">${studio}${chatWorkspace}</section>`
}
function companionPeekContextChipHtml() {
  const ctx = companionIdeContext || {}
  const parts = []
  if (ctx.file) parts.push(`${ctx.file}${ctx.line ? `:${ctx.line}` : ''}`)
  if (ctx.selection) parts.push('выделение')
  if (!parts.length) return '<small class="companion-peek-context muted">быстрый разбор</small>'
  return `<small class="companion-peek-context" title="${esc(parts.join(' · '))}">${esc(parts.join(' · '))}</small>`
}
function companionDockShell(content) {
  const error = transientError ? `<div class="error-banner"><span>!</span><p>${esc(transientError)}</p><button data-action="dismiss-error">×</button></div>` : ''
  if (isCompanionPeek()) {
    return `<div class="app companion-peek"><header class="companion-peek-brand"><div><span class="companion-presence" aria-hidden="true"></span><div><strong>Помощник Point</strong>${companionPeekContextChipHtml()}</div></div><div class="companion-peek-actions"><button type="button" class="secondary" data-action="open-companion-sidebar">Открыть справа</button><button type="button" class="secondary companion-popup-close" data-action="close-companion-popup" title="Закрыть" aria-label="Закрыть">×</button></div></header>${error}<div class="companion-peek-body">${content}</div></div>`
  }
  if (isCompanionSidebar()) {
    return `<div class="app companion-sidebar">${error}${content}</div>`
  }
  if (isCompanionPopup()) {
    return `<div class="app companion-peek"><header class="companion-peek-brand"><div><span class="companion-presence" aria-hidden="true"></span><div><strong>Помощник Point</strong>${companionPeekContextChipHtml()}</div></div><div class="companion-peek-actions"><button type="button" class="secondary" data-action="open-companion-sidebar">Открыть справа</button><button type="button" class="secondary companion-popup-close" data-action="close-companion-popup" title="Закрыть" aria-label="Закрыть">×</button></div></header>${error}<div class="companion-peek-body">${content}</div></div>`
  }
  return `<div class="app companion-dock"><header class="companion-dock-brand"><div><span class="companion-presence" aria-hidden="true"></span><strong>Помощник Point</strong></div><div class="companion-dock-actions"><button type="button" class="secondary" data-action="open-companion-sidebar" title="Открыть этот чат в правой панели">Справа</button><button type="button" class="secondary" data-action="focus-hub" data-tab="overview" title="Открыть агентов, квесты и отряды">Agent Hub</button></div></header>${error}${content}</div>`
}
function companionDockHtml() {
  return companionDockShell(`<main class="companion-dock-main">${companionChatHtml()}</main>`)
}
function companionDockOffline() {
  const starting = state.service?.state === 'starting'
  const errored = state.service?.state === 'error'
  const detail = String(state.service?.detail || '').trim()
  if (errored) {
    return companionDockShell(`<main class="offline companion-dock-status"><span class="quest-label">ЯДРО POINT</span><h2>Ядро не поднялось</h2><p>${esc(detail || 'Откройте хронику ядра для полного лога.')}</p><div class="empty-next"><button class="primary" data-action="start-server">Повторить запуск</button><button class="secondary" data-action="show-output">Хроника ядра</button></div></main>`)
  }
  return companionDockShell(`<main class="offline companion-dock-status"><span class="quest-label">ПОМОЩНИК POINT</span><h2>${starting ? 'Подключаем помощника…' : 'Помощник пока не подключён'}</h2><p>Запустите локальное ядро Point, чтобы начать диалог прямо в IDE.</p><button class="primary" data-action="start-server" ${starting ? 'disabled' : ''}>${starting ? 'Подключаемся…' : 'Подключить помощника'}</button></main>`)
}
function companionDockTrust() {
  return companionDockShell(`<main class="workspace-locked companion-dock-status"><span class="safe-mode-label">БЕЗОПАСНЫЙ РЕЖИМ</span><h2>Доступ к папке закрыт</h2><p>Разрешите доступ к проекту — тогда помощник сможет отвечать с учётом кода IDE.</p><button class="primary trust-button" data-action="manage-trust">Настроить доступ</button></main>`)
}
// Пометка найденного и якорь «к свежему» правят живую ленту — см. master-feed.js.
let applyMasterFind = () => {}
let updateMasterScrollCue = () => {}
let applyMasterComposeReserve = () => {}

const modularUiState = {
  get agentConstructorOpen() { return agentConstructorOpen }, set agentConstructorOpen(value) { agentConstructorOpen = value },
  get agentRunPreview() { return agentRunPreview }, set agentRunPreview(value) { agentRunPreview = value },
  get agentRunPreviewError() { return agentRunPreviewError }, set agentRunPreviewError(value) { agentRunPreviewError = value },
  get agentRunPreviewStatus() { return agentRunPreviewStatus }, set agentRunPreviewStatus(value) { agentRunPreviewStatus = value },
  get apiKey() { return apiKey }, set apiKey(value) { apiKey = value },
  get blueprintSyncDirection() { return blueprintSyncDirection }, set blueprintSyncDirection(value) { blueprintSyncDirection = value },
  get blueprintSyncPreview() { return blueprintSyncPreview }, set blueprintSyncPreview(value) { blueprintSyncPreview = value },
  get companionActiveRequestId() { return companionActiveRequestId }, set companionActiveRequestId(value) { companionActiveRequestId = value },
  get companionActivitySteps() { return companionActivitySteps }, set companionActivitySteps(value) { companionActivitySteps = value },
  get companionAppliedNotice() { return companionAppliedNotice }, set companionAppliedNotice(value) { companionAppliedNotice = value },
  get companionDraft() { return companionDraft }, set companionDraft(value) { companionDraft = value },
  get companionFeedbackMarks() { return companionFeedbackMarks }, set companionFeedbackMarks(value) { companionFeedbackMarks = value },
  get companionInterventionProbe() { return companionInterventionProbe }, set companionInterventionProbe(value) { companionInterventionProbe = value },
  get companionLoading() { return companionLoading }, set companionLoading(value) { companionLoading = value },
  get companionMessages() { return companionMessages }, set companionMessages(value) { companionMessages = value },
  get companionPendingSend() { return companionPendingSend }, set companionPendingSend(value) { companionPendingSend = value },
  get companionRequestId() { return companionRequestId }, set companionRequestId(value) { companionRequestId = value },
  get companionProviderProbe() { return companionProviderProbe }, set companionProviderProbe(value) { companionProviderProbe = value },
  get companionSetupDraft() { return companionSetupDraft }, set companionSetupDraft(value) { companionSetupDraft = value },
  get companionSetupOpen() { return companionSetupOpen }, set companionSetupOpen(value) { companionSetupOpen = value },
  get companionSetupPendingClose() { return companionSetupPendingClose }, set companionSetupPendingClose(value) { companionSetupPendingClose = value },
  get companionSetupQuiet() { return companionSetupQuiet }, set companionSetupQuiet(value) { companionSetupQuiet = value },
  get companionSetupStatus() { return companionSetupStatus }, set companionSetupStatus(value) { companionSetupStatus = value },
  get companionSetupStep() { return companionSetupStep }, set companionSetupStep(value) { companionSetupStep = value },
  get companionSetupTestResult() { return companionSetupTestResult }, set companionSetupTestResult(value) { companionSetupTestResult = value },
  get companionStreamReply() { return companionStreamReply }, set companionStreamReply(value) { companionStreamReply = value },
  get companionThinkPhase() { return companionThinkPhase }, set companionThinkPhase(value) { companionThinkPhase = value },
  get constructorDraft() { return constructorDraft }, set constructorDraft(value) { constructorDraft = value },
  get constructorStep() { return constructorStep }, set constructorStep(value) { constructorStep = value },
  get compiledPromptPreview() { return compiledPromptPreview }, set compiledPromptPreview(value) { compiledPromptPreview = value },
  get compiledPromptStatus() { return compiledPromptStatus }, set compiledPromptStatus(value) { compiledPromptStatus = value },
  get compiledPromptError() { return compiledPromptError }, set compiledPromptError(value) { compiledPromptError = value },
  get compiledPromptSignature() { return compiledPromptSignature }, set compiledPromptSignature(value) { compiledPromptSignature = value },
  get contextInspectorRunId() { return contextInspectorRunId }, set contextInspectorRunId(value) { contextInspectorRunId = value },
  get contextItems() { return contextItems }, set contextItems(value) { contextItems = value },
  get contextPreview() { return contextPreview }, set contextPreview(value) { contextPreview = value },
  get contextPreviewError() { return contextPreviewError }, set contextPreviewError(value) { contextPreviewError = value },
  get contextPreviewStatus() { return contextPreviewStatus }, set contextPreviewStatus(value) { contextPreviewStatus = value },
  get createStepError() { return createStepError }, set createStepError(value) { createStepError = value },
  get cursorRunActive() { return cursorRunActive }, set cursorRunActive(value) { cursorRunActive = value },
  get customToolDraft() { return customToolDraft }, set customToolDraft(value) { customToolDraft = value },
  get customToolPreview() { return customToolPreview }, set customToolPreview(value) { customToolPreview = value },
  get customToolPreviewArguments() { return customToolPreviewArguments }, set customToolPreviewArguments(value) { customToolPreviewArguments = value },
  get customToolPreviewError() { return customToolPreviewError }, set customToolPreviewError(value) { customToolPreviewError = value },
  get customToolPreviewStatus() { return customToolPreviewStatus }, set customToolPreviewStatus(value) { customToolPreviewStatus = value },
  get dbEditingId() { return dbEditingId }, set dbEditingId(value) { dbEditingId = value },
  get dbQueryResult() { return dbQueryResult }, set dbQueryResult(value) { dbQueryResult = value },
  get dbQueryStatus() { return dbQueryStatus }, set dbQueryStatus(value) { dbQueryStatus = value },
  get dbSchemaResult() { return dbSchemaResult }, set dbSchemaResult(value) { dbSchemaResult = value },
  get dbSelectedId() { return dbSelectedId }, set dbSelectedId(value) { dbSelectedId = value },
  get dbWritePending() { return dbWritePending }, set dbWritePending(value) { dbWritePending = value },
  get chatDirectoryStatus() { return chatDirectoryStatus }, set chatDirectoryStatus(value) { chatDirectoryStatus = value },
  get decisionPick() { return decisionPick }, set decisionPick(value) { decisionPick = value },
  get decisionsData() { return decisionsData }, set decisionsData(value) { decisionsData = value },
  get decisionsStatus() { return decisionsStatus }, set decisionsStatus(value) { decisionsStatus = value },
  get dockerData() { return dockerData }, set dockerData(value) { dockerData = value },
  get dockerLogs() { return dockerLogs }, set dockerLogs(value) { dockerLogs = value },
  get dockerLogsContainer() { return dockerLogsContainer }, set dockerLogsContainer(value) { dockerLogsContainer = value },
  get dockerStatus() { return dockerStatus }, set dockerStatus(value) { dockerStatus = value },
  get experienceSearchItems() { return experienceSearchItems }, set experienceSearchItems(value) { experienceSearchItems = value },
  get experienceSearchQuery() { return experienceSearchQuery }, set experienceSearchQuery(value) { experienceSearchQuery = value },
  get experienceSearchStatus() { return experienceSearchStatus }, set experienceSearchStatus(value) { experienceSearchStatus = value },
  get flowDraft() { return flowDraft }, set flowDraft(value) { flowDraft = value },
  get flowLegacyMode() { return flowLegacyMode }, set flowLegacyMode(value) { flowLegacyMode = value },
  get gitAmend() { return gitAmend }, set gitAmend(value) { gitAmend = value },
  get gitChecked() { return gitChecked }, set gitChecked(value) { gitChecked = value },
  get gitCollapsed() { return gitCollapsed }, set gitCollapsed(value) { gitCollapsed = value },
  get gitCommitDraft() { return gitCommitDraft }, set gitCommitDraft(value) { gitCommitDraft = value },
  get gitFlat() { return gitFlat }, set gitFlat(value) { gitFlat = value },
  get gitFoldedOnce() { return gitFoldedOnce }, set gitFoldedOnce(value) { gitFoldedOnce = value },
  get gitHistoryOpen() { return gitHistoryOpen }, set gitHistoryOpen(value) { gitHistoryOpen = value },
  get gitKnown() { return gitKnown }, set gitKnown(value) { gitKnown = value },
  get gitMenuFor() { return gitMenuFor }, set gitMenuFor(value) { gitMenuFor = value },
  get gitNotice() { return gitNotice }, set gitNotice(value) { gitNotice = value },
  get gitPendingAction() { return gitPendingAction }, set gitPendingAction(value) { gitPendingAction = value },
  get gitSelected() { return gitSelected }, set gitSelected(value) { gitSelected = value },
  get gitSelectedStash() { return gitSelectedStash }, set gitSelectedStash(value) { gitSelectedStash = value },
  get gitTab() { return gitTab }, set gitTab(value) { gitTab = value },
  get gitTarget() { return gitTarget }, set gitTarget(value) { gitTarget = value },
  get hireAfterSave() { return hireAfterSave }, set hireAfterSave(value) { hireAfterSave = value },
  get hirePreviewTemplateId() { return hirePreviewTemplateId }, set hirePreviewTemplateId(value) { hirePreviewTemplateId = value },
  get manualLearningDraft() { return manualLearningDraft }, set manualLearningDraft(value) { manualLearningDraft = value },
  get manualLearningPreview() { return manualLearningPreview }, set manualLearningPreview(value) { manualLearningPreview = value },
  get manualLearningStatus() { return manualLearningStatus }, set manualLearningStatus(value) { manualLearningStatus = value },
  get hiringReloadFor() { return hiringReloadFor }, set hiringReloadFor(value) { hiringReloadFor = value },
  get masterComposeNote() { return masterComposeNote }, set masterComposeNote(value) { masterComposeNote = value },
  get masterTurn() { return masterClient.turns[masterClient.active] },
  get masterData() { return masterData }, set masterData(value) { masterData = value },
  get masterOpenReasoning() { return masterOpenReasoning },
  get masterOpenLive() { return masterClient.openLive },
  get masterOpenSteps() { return masterOpenSteps },
  get masterExpandedSteps() { return masterExpandedSteps },
  get masterFindQuery() { return masterFindQuery }, set masterFindQuery(value) { masterFindQuery = value },
  get masterFindOpen() { return masterFindOpen },
  get masterFindIndex() { return masterFindIndex }, set masterFindIndex(value) { masterFindIndex = value },
  set masterFindSummary(value) { masterFindSummary = value },
  get masterAutoFollow() { return masterAutoFollow },
  get masterFindSummary() { return masterFindSummary },
  get masterLoadingEarlier() { return masterLoadingEarlier }, set masterLoadingEarlier(value) { masterLoadingEarlier = value },
  get masterDiscussionProposalId() { return masterDiscussionProposalId }, set masterDiscussionProposalId(value) { masterDiscussionProposalId = value },
  get masterDraft() { return masterDraft }, set masterDraft(value) { masterDraft = value },
  get masterSentText() { return masterSentText() },
  // Один мешок вложений на разговор. Проверки брали masterClient.active, а
  // отправка — masterData.sessions.active: разъедутся, и реплика уйдёт без
  // файлов, которые человек видел в ряду.
  get masterConversationId() { return masterClient.active },
  get masterWaitedSeconds() { return masterWaitedSeconds },
  get masterPinnedWork() { return masterPinnedWork },
  get masterWorkOrderBusy() { return masterWorkOrderBusy },
  get masterQuestionDrafts() {return masterClient.questionDrafts[masterClient.active] || {}},
  get masterQuestionCursor() {return masterClient.questionCursor[masterClient.active] || {}},
  // Панель задания открыта у своего разговора: у каждого чата задание своё.
  get masterBriefPanelOpen() { return Boolean(masterClient.briefPanel[masterClient.active]) },
  set masterBriefPanelOpen(value) { if (masterClient.active) masterClient.briefPanel[masterClient.active] = Boolean(value) },
  get masterRequestId() { return masterRequestId }, set masterRequestId(value) { masterRequestId = value },
  get masterSending() { return masterSending }, set masterSending(value) { masterSending = value },
  get masterStatus() { return masterStatus }, set masterStatus(value) { masterStatus = value },
  get memoryDraft() { return memoryDraft }, set memoryDraft(value) { memoryDraft = value },
  get memoryEditId() { return memoryEditId }, set memoryEditId(value) { memoryEditId = value },
  get onboardingDraft() { return onboardingDraft }, set onboardingDraft(value) { onboardingDraft = value },
  get onboardingLockNotice() { return onboardingLockNotice }, set onboardingLockNotice(value) { onboardingLockNotice = value },
  get onboardingStep() { return onboardingStep }, set onboardingStep(value) { onboardingStep = value },
  get pendingSkillEquip() { return pendingSkillEquip }, set pendingSkillEquip(value) { pendingSkillEquip = value },
  get profileDraft() { return profileDraft }, set profileDraft(value) { profileDraft = value },
  get profileEditorOpen() { return profileEditorOpen }, set profileEditorOpen(value) { profileEditorOpen = value },
  get profileEditorStep() { return profileEditorStep }, set profileEditorStep(value) { profileEditorStep = value },
  get proposalEditId() { return proposalEditId }, set proposalEditId(value) { proposalEditId = value },
  get proposalModifying() { return proposalModifying },
  get proposalStarting() { return proposalStarting },
  get providerProbe() { return providerProbe }, set providerProbe(value) { providerProbe = value },
  get modelCapabilityProbe() { return modelCapabilityProbe }, set modelCapabilityProbe(value) { modelCapabilityProbe = value },
  get questConstraintsDraft() { return questConstraintsDraft }, set questConstraintsDraft(value) { questConstraintsDraft = value },
  get questCriteriaDraft() { return questCriteriaDraft }, set questCriteriaDraft(value) { questCriteriaDraft = value },
  get questGoalDraft() { return questGoalDraft }, set questGoalDraft(value) { questGoalDraft = value },
  get selectedCustomToolId() { return selectedCustomToolId }, set selectedCustomToolId(value) { selectedCustomToolId = value },
  get selectedFlowId() { return selectedFlowId }, set selectedFlowId(value) { selectedFlowId = value },
  get selectedFlowNodeId() { return selectedFlowNodeId }, set selectedFlowNodeId(value) { selectedFlowNodeId = value },
  get selectedProfileId() { return selectedProfileId }, set selectedProfileId(value) { selectedProfileId = value },
  get selectedWorkflowId() { return selectedWorkflowId }, set selectedWorkflowId(value) { selectedWorkflowId = value },
  get connectionEditingId() { return connectionEditingId }, set connectionEditingId(value) { connectionEditingId = value },
  get serverEditingId() { return serverEditingId }, set serverEditingId(value) { serverEditingId = value },
  get skillDraft() { return skillDraft }, set skillDraft(value) { skillDraft = value },
  get skillEditId() { return skillEditId }, set skillEditId(value) { skillEditId = value },
  get skillEquipAfterSave() { return skillEquipAfterSave }, set skillEquipAfterSave(value) { skillEquipAfterSave = value },
  get skillFormError() { return skillFormError }, set skillFormError(value) { skillFormError = value },
  get state() { return state }, set state(value) { state = value },
  get selectedIntakeId() { return selectedIntakeId }, set selectedIntakeId(value) { selectedIntakeId = value },
  get intakeBusy() { return intakeBusy }, set intakeBusy(value) { intakeBusy = value },
  get intakeError() { return intakeError }, set intakeError(value) { intakeError = value },
  get intakeURL() { return intakeURL }, set intakeURL(value) { intakeURL = value },
  get submittingForm() { return submittingForm }, set submittingForm(value) { submittingForm = value },
  get statisticsData() { return statisticsData }, set statisticsData(value) { statisticsData = value },
  get statisticsStatus() { return statisticsStatus }, set statisticsStatus(value) { statisticsStatus = value },
  get taskDraft() { return taskDraft }, set taskDraft(value) { taskDraft = value },
  get toolLogFilter() { return toolLogFilter }, set toolLogFilter(value) { toolLogFilter = value },
  get toolWindowData() { return toolWindowData }, set toolWindowData(value) { toolWindowData = value },
  get transientError() { return transientError }, set transientError(value) { transientError = value },
  get workflowDraft() { return workflowDraft }, set workflowDraft(value) { workflowDraft = value },
}

// Разговор с помощником — отдельный модуль: отправка, поток и приём ответа
// связаны номером запроса и читаются только вместе.
const {
  stopCompanionChat, sendCompanionUserMessage, flushCompanionPendingSend,
  mergeCompanionTranscript, companionThinkingLabel, applyCompanionChatMessage,
} = createCompanionTransport({
  ui: modularUiState, root, vscode, render: (...args) => render(...args),
  persistDraft: (...args) => persistDraft(...args),
  focusCompanionInput: (...args) => focusCompanionInput(...args),
  isCompanionView: (...args) => isCompanionView(...args),
  patchCompanionComposeChrome: (...args) => patchCompanionComposeChrome(...args),
  replaceCompanionThreadHtml: (...args) => replaceCompanionThreadHtml(...args),
  replaceHtmlNodes: (...args) => replaceHtmlNodes(...args),
  scrollCompanionThread: (...args) => scrollCompanionThread(...args),
  companionQuickPromptsHtml: (...args) => companionQuickPromptsHtml(...args),
  patchCompanionThinkingLabel: (...args) => patchCompanionThinkingLabel(...args),
  patchCompanionStreamingBubble: (...args) => patchCompanionStreamingBubble(...args),
  pendingQuestProposals: (...args) => pendingQuestProposals(...args),
  pendingActionProposals: (...args) => pendingActionProposals(...args),
})

// Ответы Мастера разбираются своим модулем: тринадцать веток, у которых на
// другом конце один и тот же `masterClient` и одна и та же лента.
const applyMasterMessage = createMasterInbox({
  ui: modularUiState, root, vscode, render: (...args) => render(...args),
  persistDraft: (...args) => persistDraft(...args),
  masterClient, masterSessionDrafts,
  masterTraceMindPatch: (...args) => masterTraceMindPatch(...args),
  acceptMasterMentionItems: (...args) => acceptMasterMentionItems(...args),
  receiveMasterContext: (...args) => receiveMasterContext(...args),
  clearMasterContext: (...args) => clearMasterContext(...args),
  forgetMasterSent: (...args) => forgetMasterSent(...args),
  replaceMasterThreadHtml: (...args) => replaceMasterThreadHtml(...args),
  syncMasterComposeState: (...args) => syncMasterComposeState(...args),
  stopMasterWaitClock: (...args) => stopMasterWaitClock(...args),
  sendMasterMessage: (...args) => sendMasterMessage(...args),
  applyMasterFind: (...args) => applyMasterFind(...args),
})

// Ответы на правку сущностей Гильдии разбираются своим модулем: у всех
// тринадцати одна форма — закрыть редактор и решить, куда вести дальше.
const applyHubEntityMessage = createHubEntityInbox({
  ui: modularUiState, vscode, render: (...args) => render(...args),
  persistDraft: (...args) => persistDraft(...args),
  closeQuestIfOpen: (...args) => closeQuestIfOpen(...args),
  resetQuestReplansCache: (...args) => resetQuestReplansCache(...args),
  releaseMasterAgentCards: (...args) => releaseMasterAgentCards(...args),
  reviseWorkOrderRosterV2: (...args) => reviseWorkOrderRosterV2(...args),
})
// Экран «Решения» живёт отдельным модулем: очередь, карточка и горячие
// клавиши — одна тема, и трогают её вместе.
const {
  decisionsView, decisionQueueItemHtml, decisionDetailHtml, decisionIntents,
  sendDecisionResolve, decisionHotkey,
} = createDecisionViews({
  ui: modularUiState, vscode, render: () => render(), esc, data, state,
  shell: (...args) => shell(...args),
  masterProposalHtml: (...args) => masterProposalHtml(...args),
  taskProposalById: (...args) => taskProposalById(...args),
})

const {
  setAgentCapability, agentCapabilityKey, setCapabilityDelta, capabilityDeltaKey,
  setHandoffChain, handoffFlowRunId, setQuestOutcome, questOutcomeId,
  setQuestReplans, resetQuestReplansCache, questReplansId,
  toggleOpenQuest, closeQuestIfOpen, agentCapabilityKeyFor, AGENT_CAPABILITY_LIMIT, agentCapabilityCache, agentCapabilityInflight,
  agentCapabilityFailed, requestAgentCapability, agentCapabilityFor, profileReadiness, requestCapabilityDelta,
  capabilityDeltaHtml, questWorkHtml, orphanExecutions, orphanExecutionsHtml, questListHtml,
  questOutcomeHtml, questMidFlightHtml, handoffsHtml, agentCapabilityHtml, PROFILE_STEP_IDS, blockerStep,
  firstUnreadinessStep, questBriefQuality, canAcceptQuest, questBriefGuidanceHtml, runQualityOutcomeHtml,
  historyQualitySignals, flowStages, activeToolPresetId, stepValidationIssue, templateClassPreview,
  templatePickerHtml, hireLiveChecklist, hireSummaryPanel, readinessBanner, profileStepNav,
  profileStepFooter, pendingDecisionsBanner, patchStatusLabel, questPayload, composeQuestTask,
  formatTime, status, agentClass, agentProgress, questLevelUpBadge,
  agentSynergy, agentCharacterCard, activeQuestCard, formatBytes, formatDuration,
  diagnosticSignalText, guardrailEventText, CORE_FAILURE_HINTS, coreFailureText, toolFailureText,
  diagnosticsCard, runComparison, contextKindLabel, invalidateAgentRunPreview, requestContextPreview,
  contextPreviewMarkup, hubNavLocked, hubSituation, toolCommandButton, toolWindowHeading,
  gitFileKind, gitChanges, gitLists, gitCheckedPaths, gitGroupOf,
  gitGroups, gitGroupPaths, syncGitChecked, syncGitCommitButtons, syncGitTree, gitToolView,
  terminalToolView, logsToolView, currentToolWindowView, shell, HALL_SECTIONS,
} = createQuestRuntimeViews({
  // Словесный знак в шапке Чертога — вход в галерею миров. Связывание ленивое:
  // модуль галереи собирается ниже, а зовут его уже во время отрисовки.
  projectSwitcherChipHtml: (...args) => projectSwitcherChipHtml(...args),
  // Левая панель чата, значок непроверенных изменений и вкладка задания —
  // тем же ленивым связыванием: их модули собираются ниже по файлу.
  projectChatDirectoryHtml: (...args) => chatDirectoryHtml(...args),
  hallChangesAlarmHtml: (...args) => hallChangesAlarmHtml(...args),
  masterBriefTabHtml: (...args) => masterBriefTabHtml(...args),
  CREATE_FLOW_STAGES, EMPTY_TASK_REASON, PROFILE_STEPS, TOOL_PRESETS, activeExecutions,
  agentById, blueprintById, changeSetStatusLabels, compactQuestTitle, createCompanionMarkdownFormatter,
  createGitViews, currentHubQuest, databasesView: (...args) => databasesView(...args), decisionsWaitingCount: (...args) => decisionsWaitingCount(...args), execControlsHtml,
  gitWide, hallActiveSection: (...args) => hallActiveSection(...args), hallAlarmHtml: (...args) => hallAlarmHtml(...args), hallCrumb: (...args) => hallCrumb(...args), hallSectionBadge: (...args) => hallSectionBadge(...args), isConnectionsView,
  modelChipHtml,
  hallSpendLabel: (...args) => hallSpendLabel(...args), hubAgents, isDockerView: () => document.body?.dataset?.layout === 'docker', isStatisticsView, isSystemOnboardingStep,
  isToolWindow, pendingActionProposals, pendingChangeSets, pendingQuestProposals, persistDraft, toolWindowFrame,
  questStatusLabels, render, root, runIsFinished, runIsLive,
  statusLabels, toolLabels, eventLabels, healthLabels, stopReasonLabels,
  plural, countOf, esc, formatCompanionMarkdown, data, toolName,
  providerCatalog, providerPreset, requiresApiKey, lines, toolProvidesVerification,
  serversView: (...args) => serversView(...args), toolWindowKind, ui: modularUiState, vscode,
})

;({ agentWorkTranscriptHtml } = createAgentWorkTranscript({
  esc,
  data,
  toolName,
  formatDuration,
  guardrailEventText,
  toolFailureText,
  coreFailureText,
  patchStatusLabel,
  questLevelUpBadge,
  formatMarkdown: formatCompanionMarkdown,
}))

const {
  newConstructorDraft, constructorStepForProfileStep, prepareAgentConstructor,
  constructorToProfile, constructorToBlueprint, legacySaveWouldDrop,
  constructorToProjectAgent, companionSuggestionChipsHtml, normalizeToolRisk,
  defaultToolPolicyForRisk, readConstructorToolPolicies, toolPolicyStrictness,
  constructorSkillGaps, constructorSkillGapHtml, applySkillToolGrants,
  currentConstructorForm, constructorStepNav, resetCompiledPromptPreview,
  compiledPromptPreviewHtml,
} = createAgentConstructor({
  ui: modularUiState,
  root,
  vscode,
  esc,
  lines,
  providerCatalog,
  agentById,
  hubModeAvailable,
  persistDraft,
  getState: () => state,
  getIgnoredCompanionSuggestions: () => ignoredCompanionSuggestions,
})

const {
  sandboxBoundaryHtml, overview, situationRoomHtml, unverifiedChangesHtml, recentlyBrokenRuns,
  hubHealthRowsHtml, teamsView, projectFlowsView, skillAttachedAgents, skillCurationHtml, skillFormHtml,
  skillsView, experienceSearchHtml, manualLearningHtml, memoryView, statisticsViews,
  statisticsView, dockerContainerName, dockerView, infrastructureViews, dbDriverLabel,
  databasesView, serversView, featuredProviderChips, connectionsView,
} = createQuestOverviewViews({
  toolName,
  activeExecutions, activeQuestCard, agentById, agentClass, agentDisplayName,
  agentRuntimeLabel, changeSetCardHtml, compactExecutionTask, compactQuestDescription, compactQuestTitle,
  compactTeamName, companionChatHtml, connectionManagerHtml, connectionStatusLabels, contextInspectorPanelHtml, countOf,
  createInfrastructureViews, createStatisticsViews, currentHubQuest, currentHubTeam, decisionsWaitingCount: (...args) => decisionsWaitingCount(...args),
  decisionsWaitingBreakdown: (...args) => decisionsWaitingBreakdown(...args),
  esc, execControlsHtml, flowApprovalStripHtml, formatCents, formatDuration,
  hallSpendLabel: (...args) => hallSpendLabel(...args), hallUnpricedRuns: (...args) => hallUnpricedRuns(...args), handoffsHtml, hubAgents, hubSituation,
  isConnectionsView, isStatisticsView, memoryKindLabels, partyStatusStripHtml, pendingChangeSets, plannerFallbackBannerHtml, questOutcomeHtml, questMidFlightHtml, questStatusStripHtml,
  questProgressHtml, questStatusLabels, runIsActiveNow, shell, skillEquipConfirmHtml: (...args) => skillEquipConfirmHtml(...args),
  statisticsPanelHtml, systemAgentsStripHtml: (...args) => systemAgentsStripHtml(...args), ui: modularUiState, vscode,
  status, toolWindowFrame,
})

const {
  isProjectGalleryOpen, setProjectGalleryOpen, projectSwitchInfo, setProjectSwitchInfo, receiveProjects,
  projectPathByHash, projectGallery, projectSwitchSkeleton, projectSwitcherChipHtml,
  handleProjectGalleryAction, handleProjectGalleryInput,
} = createProjectGalleryViews({ ui: modularUiState, vscode, esc, countOf })

const {
  chatDirectoryHtml, chatScreenBeforeTabs, receiveChatDirectory, handleChatDirectoryAction, handleChatDirectoryInput,
} = createMasterChatDirectory({
  ui: modularUiState, vscode, esc, countOf, projectPathByHash,
  projectGallery, projectRequired: (...args) => projectRequired(...args), projectSwitchInfo, projectSwitchSkeleton,
  shell: (...args) => shell(...args),
})

const {
  hallActiveSection, hallCrumb, locallyWaitingCount, markDecisionsLoaded, decisionsQueueIsStale, decisionsWaitingCount, decisionsWaitingBreakdown, hallSectionBadge,
  hallAlarmHtml, hallChangesAlarmHtml, hallSpendLabel, hallUnpricedRuns, workspaceTrustRequired, offline,
  projectRequired, onboardingCompanionDraft, writeOnboardingCompanionDraft, sanitizeOrchestratorDraft, onboardingOrchestratorDraft,
  writeOnboardingOrchestratorDraft, currentOnboardingOrchestratorValues, orchestratorConfigFromDraft, persistOnboardingOrchestrator, orchestratorPresetStudioHtml,
  orchestratorPolicyControlsHtml, setOrchestratorPolicy, orchestratorPolicyDraftKey, ORCHESTRATOR_POLICY_LIMIT, orchestratorPolicyCache,
  orchestratorPolicyInflight, orchestratorPolicyFailed, orchestratorPolicyLines, orchestratorPreviewHtml, orchestratorModeCardsHtml,
  orchestratorConnectionFieldsHtml, systemAgentsStripHtml, onboardingCompanionActive, onboardingOrchestratorActive, orchestratorSetupValidation,
  currentOnboardingCompanionValues, indexLanguages, onboardingAgentTemplates, companionSuggestedTemplateId,
  firstAgentProposal, onboardingPrimaryAgent, currentAgentProfile, skillEquipConfirmHtml, patchOnboardingAgent,
  firstAgentDraftFromProposal, acceptFirstAgentProposal, onboardingPathHtml, persistOnboardingCompanion, onboarding,
  blueprintDiffValue, blueprintSyncPreviewHtml, agentConstructor, conversation, approvalCard,
  patchCard, agentRunPreviewMarkup, composerActionsHtml, chat, history,
  changes, persistentProfileSummary, guildRoster, settings, quickChatSettingsHtml,
} = createHallOnboardingViews({
  COMPANION_PRESETS, CONSTRUCTOR_STEPS, HALL_SECTIONS, ONBOARDING_CHAPTERS, ONBOARDING_STEPS,
  ORCHESTRATOR_PRESETS, ORCHESTRATOR_TRAIT_FIELDS, TOOL_PRESETS, activeQuestCard, activeToolPresetId,
  agentClass, agentProgress, agentSynergy, blueprintById, capabilityDeltaHtml,
  companionConfigFromDraft, companionConnectionFieldsHtml, companionConnections, companionLocalReadyHtml, companionModeCardsHtml,
  companionOnboardingFinished, companionPersonalityControlsHtml, companionPersonalityPreviewHtml, companionPresetStudioHtml, companionProbeCtaLabel,
  companionProbeHtml, connectionDrawerHtml, connectionFormHtml, modelChoiceHtml, companionProviderPresets, companionScenePickerHtml, companionSetupStatusHtml, companionStyleStudioHtml,
  companionSuggestionChipsHtml, companionTraitKeyToId, compiledPromptPreviewHtml, completionProofHtml, connectionOrbHtml,
  connectionStatusLabels, constructorSkillGapHtml, constructorStepNav,
  contextInspectorPanelHtml, coreFailureText, countOf, data,
  defaultCompanionProviderPreset, diagnosticsCard, esc, execControlsHtml, featuredProviderChips,
  firstUnreadinessStep, formatCents, formatDuration, guardrailEventText, healthLabels,
  historyQualitySignals, hubAgents, hubModeAvailable, isCompanionOnboardingStep, isCompanionView,
  isOrchestratorOnboardingStep, isSystemOnboardingStep, isWide, mergeCompanionConnectionForm,
  newProfile: (...args) => newProfile(...args), normalizeRunnableAgent, onboardingStepNav, onboardingWelcomeResume,
  orchestratorOnboardingFinished, orphanExecutions, orphanExecutionsHtml, patchStatusLabel, pendingActionProposals,
  pendingChangeSets, pendingDecisionsBanner, pendingQuestProposals, persistDraft, plural,
  profileEditor: (...args) => profileEditor(...args), profileReadiness, providerCatalog, providerPreset, providerWantsToken,
  questAsideHtml, questBriefGuidanceHtml, questLevelUpBadge, questListHtml, readinessBanner,
  requiresApiKey, root, runComparison, runIsActiveNow, runIsLive,
  runQualityOutcomeHtml, sanitizeCompanionSetupDraft, shell, status, templatePickerHtml, toolFailureText,
  toolName, ui: modularUiState, usageSummary, vscode,
})

const {
  profileEditor, newProfile, currentFormProfile, cloneCustomTool, currentCustomToolForm,
  resetCustomToolPreview, invalidateCustomToolPreview, toolNetworkPolicyCallout, customToolFormIssue, toolSandbox,
  toolBuilder, newWorkflowStep, newWorkflow, workflowTemplates, workflowFromTemplate,
  currentWorkflowForm, FLOW_NODE_KINDS, flowNodeKindLabels, newFlowNode, flowNodePosition,
  activeFlowRun, mergeCandidateLabel, flowMergeConflictPanelHtml, flowRuntimeSummaryHtml, captureFlowForm,
  flowEdgeOptions, flowCanvasHtml, flowInspectorHtml, visualFlowBuilder, flowsView,
  workflowTimeline, workflowBuilder,
} = createAgentWorkflowEditors({
  TOOL_PRESETS, activeToolPresetId, agentById, agentCapabilityHtml, agentCharacterCard,
  agentClass, countOf, esc, flowStages, hireLiveChecklist,
  hireSummaryPanel, hubAgents, isWide, lines, profileReadiness,
  profileStepFooter, profileStepNav, providerCatalog, providerPreset, readinessBanner,
  requiresApiKey, root, shell, status, statusLabels,
  templatePickerHtml, toolProvidesVerification, modelCapabilityProbeHtml, modelChoiceHtml, ui: modularUiState,
})

// Панель задания собирается раньше видов разговора: три её функции уходят туда
// доводами, чтобы master-thread-views.js остался чистым видом и не знал о том,
// откуда берётся открытость панели.
const {
  closeMasterBriefPanel, handleMasterBriefAction, masterBriefPanelHtml,
  masterBriefTabHtml, syncMasterBriefSurfaces,
} = createMasterBriefPanel({
  countOf, esc, ui: modularUiState,
  proposalEditorHtml: (...args) => questProposalEditorHtml(...args),
  taskProposalById: (...args) => taskProposalById(...args),
  taskBriefActionsHtml, taskBriefBodyHtml, taskBriefReady, taskBriefStateLabel,
  rosterHasAgent: () => rosterHasAgent(),
})

const {
  masterAskSlotHtml,
  masterDialogueHtml, masterDiscussionContextHtml, masterModelLabel, masterProposalHtml,
  masterComposeActionsInnerHtml, masterThreadContentHtml, masterThreadHtml, stampMasterAnswer,
} = createMasterThreadViews({
  masterBriefPanelHtml: (...args) => masterBriefPanelHtml(...args),
  masterBriefTabHtml: (...args) => masterBriefTabHtml(...args),
  taskBriefReady,
  agentById: (...args) => agentById(...args), agentClass: (...args) => agentClass(...args),
  agentWorkTranscriptHtml: (...args) => agentWorkTranscriptHtml(...args),
  approvalCard: (...args) => approvalCard(...args), patchCard: (...args) => patchCard(...args),
  changeSetCardHtml: (...args) => changeSetCardHtml(...args),
  companionActionProposalHtml: (...args) => companionActionProposalHtml(...args),
  connectionLabel: (...args) => connectionLabel(...args),
  countOf, esc, execControlsHtml: (...args) => execControlsHtml(...args), flowNodeKindLabels, formatCompanionMarkdown,
  modelChipHtml: (...args) => modelChipHtml(...args),
  pendingChangeSets: (...args) => pendingChangeSets(...args),
  plannerFallbackBannerHtml: (...args) => plannerFallbackBannerHtml(...args),
  sessionKeepUndoBarHtml: (...args) => sessionKeepUndoBarHtml(...args),
  sessionRunChangedFilesHtml: (...args) => sessionRunChangedFilesHtml(...args),
  questImportanceLabel: (...args) => questImportanceLabel(...args),
  questProposalEditorHtml: (...args) => questProposalEditorHtml(...args),
  questStatusLabels, shell: (...args) => shell(...args), statusLabels, taskBriefCardHtml,
  taskProposalById: (...args) => taskProposalById(...args), ui: modularUiState, vscode,
  masterWorkOrderCardsHtml,
  rosterHasAgent: () => rosterHasAgent(),
})

;({ applyMasterFind, updateMasterScrollCue, applyMasterComposeReserve } = createMasterFeedRuntime({ root, ui: modularUiState }))

function captureUi() {
  const active = document.activeElement
  const focus = active && root.contains(active) && active.id
    ? { id: active.id, start: active.selectionStart, end: active.selectionEnd }
    : undefined
  // У кнопок списка нет id, и по снимку выше они не восстанавливаются. Список
  // опознаётся своей подписью, элемент — позицией: после перерисовки вернуть
  // нужно тот же по счёту, а если разметка сменилась — текущий выбранный.
  const activeList = !focus && active && root.contains(active) ? active.closest?.('[data-keynav]') : undefined
  const keynav = activeList ? {
    label: activeList.getAttribute('aria-label') || '',
    at: [...activeList.querySelectorAll('button')].indexOf(active.closest('button')),
  } : undefined
  const companionThread = root.querySelector('#companion-thread')
  const masterThread = root.querySelector('#master-thread')
  return {
    focus,
    keynav,
    chatMain: root.querySelector('.chat-main')?.scrollTop ?? 0,
    conversation: root.querySelector('.conversation')?.scrollTop ?? 0,
    commandCenter: root.querySelector('.command-center')?.scrollTop ?? 0,
    setupContent: root.querySelector('.companion-setup-content')?.scrollTop ?? 0,
    onboarding: root.querySelector('.onboarding')?.scrollTop ?? 0,
    // Дерево изменений перерисовывается от каждой правки в редакторе: без
    // переноса прокрутки список прыгал бы к началу под рукой.
    gitTree: root.querySelector('.point-git-tree')?.scrollTop ?? 0,
    companionThread: companionThread ? {
      top: companionThread.scrollTop,
      follow: companionAutoFollow || threadNearBottom(companionThread),
    } : undefined,
    masterThread: masterThread ? {
      top: masterThread.scrollTop,
      follow: masterAutoFollow || threadNearBottom(masterThread),
    } : undefined,
  }
}

function restoreUi(snapshot) {
  // Запас ленты под плавающей карточкой меряется здесь, а не только на нажатии
  // клавиши: на первой отрисовке раздела нажатий ещё не было, и лента осталась
  // бы с запасным числом, а карточка с уточнениями закрыла бы хвост разговора.
  // Стоит до выхода по пустому снимку — снимка нет как раз при первом открытии.
  applyMasterComposeReserve()
  if (!snapshot) return
  const chatScreen=root.querySelector('.is-chat')
  if(chatScreen){chatScreen.classList.toggle('is-chats-hidden',!!masterClient.historyHidden);chatScreen.classList.toggle('is-chats-open',!!masterClient.historyOpen)}
  const dialogue=root.querySelector('.hall-dialogue')
  // Панель задания: класс раздела повторяется после отрисовки по той же
  // причине, что и рейка разговоров, — разметку собирает не она одна.
  if(dialogue)dialogue.classList.toggle('is-brief-open',modularUiState.masterBriefPanelOpen&&!!root.querySelector('#master-brief-panel'))
  const chatMain = root.querySelector('.chat-main')
  if (chatMain && snapshot.chatMain != null) chatMain.scrollTop = snapshot.chatMain
  const conversation = root.querySelector('.conversation')
  if (conversation && snapshot.conversation != null) conversation.scrollTop = snapshot.conversation
  const commandCenter = root.querySelector('.command-center')
  if (commandCenter && snapshot.commandCenter != null) commandCenter.scrollTop = snapshot.commandCenter
  const setupContent = root.querySelector('.companion-setup-content')
  if (setupContent && snapshot.setupContent != null) setupContent.scrollTop = snapshot.setupContent
  const onboarding = root.querySelector('.onboarding')
  if (onboarding && snapshot.onboarding != null) onboarding.scrollTop = snapshot.onboarding
  const gitTree = root.querySelector('.point-git-tree')
  if (gitTree && snapshot.gitTree != null) gitTree.scrollTop = snapshot.gitTree
  const companionThread = root.querySelector('#companion-thread')
  if (companionThread && snapshot.companionThread) {
    companionAutoFollow = Boolean(snapshot.companionThread.follow)
    companionThread.scrollTop = companionAutoFollow ? companionThread.scrollHeight : snapshot.companionThread.top
    updateCompanionScrollCue()
  }
  // Лента Мастера пересобирается целиком на каждой отрисовке, а отрисовку
  // вызывает и чужое состояние — обновление очереди решений, ответ ядра.
  // Без переноса прокрутки разговор после каждой такой перерисовки прыгал к
  // самой первой реплике: свежий ответ и «Думает…» оказывались за экраном.
  const masterThread = root.querySelector('#master-thread')
  if (masterThread) {
    if(masterClient.restoreScroll!=null){snapshot.masterThread={top:masterClient.restoreScroll,follow:!Number.isFinite(masterClient.restoreScroll)};delete masterClient.restoreScroll}
    // Снимка нет — раздел только что открыли. Разговор показывается с конца,
    // как его и оставили, а не с начала переписки.
    const follow = snapshot.masterThread ? Boolean(snapshot.masterThread.follow) : true
    masterAutoFollow = follow
    masterThread.scrollTop = follow ? masterThread.scrollHeight : snapshot.masterThread.top
  }
  if (snapshot.focus?.id) {
    const el = root.querySelector(`#${CSS.escape(snapshot.focus.id)}`)
    if (el && typeof el.focus === 'function') {
      // Возврат фокуса после отрисовки возвращает каретку, а не показывает
      // элемент: он и так был на экране. Обычный focus() при этом прокручивал
      // ближайшего предка с `overflow: hidden`, и в боковой панели помощника
      // разговор уезжал вверх на каждой перерисовке — то есть на каждой реплике.
      el.focus({ preventScroll: true })
      if (typeof snapshot.focus.start === 'number' && typeof el.setSelectionRange === 'function') {
        try { el.setSelectionRange(snapshot.focus.start, snapshot.focus.end ?? snapshot.focus.start) } catch {}
      }
    }
  } else if (snapshot.keynav) {
    // Без этого перебор списка работал ровно один раз. Клавиша меняет выбор,
    // выбор вызывает полную отрисовку, отрисовка уничтожает элемент с фокусом —
    // и следующая клавиша приходит в body мимо обработчика на root. Проверено
    // в браузере: так же были сломаны J и K, обещанные подсказкой на экране.
    const list = [...root.querySelectorAll('[data-keynav]')]
      .find(node => (node.getAttribute('aria-label') || '') === snapshot.keynav.label)
    if (list) {
      const items = [...list.querySelectorAll('button')]
      const target = list.querySelector('[aria-current="true"]')
        || items[Math.min(Math.max(snapshot.keynav.at, 0), items.length - 1)]
      if (target && typeof target.focus === 'function') target.focus()
    }
  }
  // Последним — иначе восстановление курсора по снимку вернуло бы каретку туда,
  // где она стояла в прежнем, ещё пустом поле.
  if (masterCaretToEnd) {
    masterCaretToEnd = false
    const field = root.querySelector('#master-input')
    if (field) {
      // Та же причина, что у поля помощника: фокус не должен двигать ленту.
      field.focus({ preventScroll: true })
      try { field.setSelectionRange(field.value.length, field.value.length) } catch {}
    }
  }
}

function syncCompanionSetupSelection() {
  const draft = sanitizeCompanionSetupDraft(companionSetupDraft)
  for (const btn of root.querySelectorAll('[data-action="companion-setup-preset"]')) {
    btn.classList.toggle('selected', btn.dataset.preset === draft.preset)
  }
  for (const btn of root.querySelectorAll('[data-action="companion-select-scene"]')) {
    btn.classList.toggle('selected', btn.dataset.scene === draft.sampleScene)
  }
  for (const btn of root.querySelectorAll('[data-action="companion-toggle-auto-act"]')) {
    btn.classList.toggle('on', (btn.dataset.autoAct === 'true') === Boolean(draft.autoAct))
  }
  for (const btn of root.querySelectorAll('[data-action="companion-select-example"]')) {
    btn.classList.toggle('selected', btn.dataset.prompt === draft.examplePrompt)
  }
  for (const btn of root.querySelectorAll('[data-action="companion-select-mode"]')) {
    btn.classList.toggle('selected', btn.dataset.mode === draft.mode)
  }
}

function paintCompanionStudio() {
  const form = root.querySelector('#companion-setup-form')
  if (!form || !companionSetupOpen) {
    paint()
    return
  }
  const snapshot = captureUi()
  const wrap = document.createElement('div')
  wrap.innerHTML = companionSetupWizardHtml()
  const next = wrap.firstElementChild
  if (next) form.replaceWith(next)
  restoreUi(snapshot)
  persistDraft()
}

function refreshCompanionSetup(options = {}) {
  companionSetupQuiet = options.quiet !== false
  persistDraft()
  if (options.liveOnly && root.querySelector('[data-companion-preview], [data-companion-role-showcase]')) {
    refreshCompanionLiveSurfaces(companionSetupDraft)
    syncCompanionSetupSelection()
    return
  }
  if (root.querySelector('#companion-setup-form')) paintCompanionStudio()
  else render()
}

function syncComposerRunState() {
  const profiles = state.boot?.profiles || []
  const profile = profiles.find(item => item.id === selectedProfileId) || profiles[0]
  const active = runIsActiveNow(state.details?.run)
  for (const id of ['task', 'quest-goal', 'quest-criteria', 'quest-constraints']) {
    const el = root.querySelector(`#${id}`)
    if (el) el.disabled = active
  }
  for (const button of root.querySelectorAll('.context-toolbar [data-action="attach-files"], .context-toolbar [data-action="attach-selection"], .context-chips [data-action="remove-context"]')) {
    button.disabled = active
  }
  const actions = root.querySelector('.composer-actions')
  if (actions) actions.innerHTML = composerActionsHtml(profile, active, false)
}

function paintRun() {
  const chatMain = root.querySelector('.chat-main')
  if (chatMain && (state.selectedTab === 'quests' || state.selectedTab === 'quest')) {
    const atBottom = chatMain.scrollHeight - chatMain.scrollTop - chatMain.clientHeight < 48
    const top = chatMain.scrollTop
    chatMain.innerHTML = conversation()
    chatMain.scrollTop = atBottom ? chatMain.scrollHeight : top
    syncComposerRunState()
  }
  const workflowHost = root.querySelector('[data-workflow-run-host]')
  if (workflowHost) workflowHost.innerHTML = workflowTimeline(state.workflowDetails)
}

function schedulePaintRun() {
  if (paintFrame) return
  paintFrame = requestAnimationFrame(() => {
    paintFrame = 0
    paintRun()
  })
}

function paint() {
  const snapshot = captureUi()
  // Галерея, первый запуск и переключение мира решаются до вкладок: домом стал
  // чат, а галерея — отдельный экран по явному вызову. Разбор случаев живёт
  // в master-chat-directory.js рядом с самим списком.
  const before = chatScreenBeforeTabs({ gallery: isProjectGalleryOpen() && isWide, wide: isWide })
  if (before !== undefined) { root.innerHTML = before; restoreUi(snapshot); return }
  if (isCompanionView()) {
    if (state.workspaceTrusted === false) { root.innerHTML = companionDockTrust(); return }
    if (state.service?.state !== 'running') { root.innerHTML = companionDockOffline(); return }
    root.innerHTML = companionDockHtml()
    restoreUi(snapshot)
    persistDraft()
    return
  }
  if (state.workspaceTrusted === false) { root.innerHTML = workspaceTrustRequired(); restoreUi(snapshot); return }
  if (isToolWindow() && (['git', 'terminal', 'logs'].includes(toolWindowKind()) || state.service?.state === 'running')) {
    root.innerHTML = currentToolWindowView()
    restoreUi(snapshot)
    // Частично отмеченная папка — состояние галочки, а не атрибут: разметкой
    // его не выразить, поэтому проставляется после отрисовки.
    if (toolWindowKind() === 'git') syncGitTree()
    persistDraft()
    return
  }
  if (state.service?.state !== 'running') { root.innerHTML = offline(); return }
  const dedicated = { connections: connectionsView, statistics: statisticsView, docker: dockerView }[document.body?.dataset?.layout]
  if (dedicated) { root.innerHTML = dedicated(); restoreUi(snapshot); persistDraft(); return }
  if (state.selectedTab === 'onboarding') root.innerHTML = (agentConstructorOpen && companionOnboardingFinished()) ? agentConstructor() : onboarding()
  else if (state.selectedTab === 'decisions') root.innerHTML = decisionsView()
  else if (state.selectedTab === 'master') root.innerHTML = masterDialogueHtml()
  else if (state.selectedTab === 'overview') root.innerHTML = overview()
  else if (state.selectedTab === 'agents') root.innerHTML = settings()
  else if (state.selectedTab === 'teams') root.innerHTML = teamsView()
  else if (state.selectedTab === 'quests' || state.selectedTab === 'quest') root.innerHTML = chat()
  else if (state.selectedTab === 'flows' || state.selectedTab === 'workflows') root.innerHTML = projectFlowsView()
  else if (state.selectedTab === 'statistics') root.innerHTML = statisticsView()
  else if (state.selectedTab === 'docker') root.innerHTML = dockerView()
  else if (state.selectedTab === 'skills') root.innerHTML = skillsView()
  else if (state.selectedTab === 'tools') root.innerHTML = toolBuilder()
  else if (state.selectedTab === 'memory') root.innerHTML = memoryView()
  else if (state.selectedTab === 'connections') root.innerHTML = connectionsView()
  else if (state.selectedTab === 'databases') root.innerHTML = databasesView()
  else if (state.selectedTab === 'changesets') root.innerHTML = changeSetsView()
  else if (state.selectedTab === 'journal') root.innerHTML = journalView()
  else if (state.selectedTab === 'filehistory') root.innerHTML = fileHistoryView()
  else if (state.selectedTab === 'history') root.innerHTML = history()
  else if (state.selectedTab === 'changes') root.innerHTML = changes()
  else root.innerHTML = overview()
  const contextChips = root.querySelector('.context-chips')
  if (contextChips) contextChips.insertAdjacentHTML('afterend', contextPreviewMarkup())
  restoreUi(snapshot)
  persistDraft()
}

function render() {
  if (renderFrame) return
  renderFrame = requestAnimationFrame(() => {
    renderFrame = 0
    paint()
  })
}

function requestModelCapabilityProbe(profile) {
  const normalized = profile ? { ...profile, model: profile.model || profile.primaryModel || '' } : undefined
  if (!normalized?.model || !['ollama', 'openai-compatible'].includes(normalized.provider)) {
    modelCapabilityProbe = { error: 'Нужна выбранная Ollama или OpenAI-compatible модель.' }
    render()
    return
  }
  modelCapabilityProbe = { loading: true }
  render()
  vscode.postMessage({ type: 'probeModelCapability', profile: normalized, apiKey })
}

// Раскрытие «Рассуждения» и «Что смотрел» запоминается по метке хода.
//
// Событие toggle не всплывает, поэтому слушаем на перехвате: иначе следующая
// точечная замена ленты вернула бы блок закрытым прямо под читающим.
root.addEventListener('toggle', event => {
  const block = event.target
  const kind = block?.dataset?.masterOpen
  if (!kind) return
  const set = kind === 'live' ? masterClient.openLive : kind === 'steps' ? masterOpenSteps : masterOpenReasoning
  if (block.open) set.add(block.dataset.id)
  else set.delete(block.dataset.id)
}, true)

root.addEventListener('click', event => {
  const example = event.target.closest('[data-example]')
  if (example) {
    taskDraft = example.dataset.example || ''
    questGoalDraft = example.dataset.goal || ''
    questCriteriaDraft = (example.dataset.criteria || '').replace(/&#10;/g, '\n')
    questConstraintsDraft = (example.dataset.constraints || '').replace(/&#10;/g, '\n')
    invalidateAgentRunPreview()
    persistDraft()
    render()
    const task = root.querySelector('#task')
    if (task) task.focus()
    return
  }
  // Клик мимо открытого меню его закрывает — так ведут себя все меню, и ждать
  // от человека повторного нажатия на «⋯» неправильно.
  if (gitMenuFor && !event.target.closest?.('.nc-menu')) {
    gitMenuFor = ''
    render()
  }
  if (closeModelPickerOutside(event)) render()
  const target = event.target.closest('[data-action]')
  if (!target) return
  const action = target.dataset.action
  if (handleChatDirectoryAction(action, target)) { render(); return }
  if (handleProjectGalleryAction(action, target)) { render(); return }
  if (handleMasterContextAction({action,target,vscode,sending:masterSending})) {persistDraft();return}
  if (handleMasterBriefAction({ action, root, persist: persistDraft })) return
  // Путь от пометки в ленте к форме в карточке ввода. Вопрос задан там, где
  // его задали, отвечают ниже — и просить человека искать поле самому нельзя.
  if (action === 'master-focus-ask') {
    const field = root.querySelector('#master-questions-ask .hall-option, #master-questions-ask .hall-question-extra')
    field?.focus?.({ preventScroll: true })
    return
  }
  if (handleMasterSessionAction({
    action, target, root, vscode, sending: masterSending, send: sendMasterMessage,
    render, persist: persistDraft,
    drafts: () => (masterClient.questionDrafts[masterClient.active] ||= {}),
    cursor: () => (masterClient.questionCursor[masterClient.active] ||= {}),
  })) return
  if (handleGitClickAction({
    action, target, root, ui: modularUiState, vscode, persistDraft, render,
    gitSelectFile, gitGroups, gitGroupPaths, submitGitCommit, toolWindowData,
  })) return
  if (handleHubClickAction({
    action, target, vscode, persistDraft, render, toolWindowKind,
    getToolWindowData: () => toolWindowData,
    setToolWindowData: value => { toolWindowData = value },
    getToolLogFilter: () => toolLogFilter,
    setToolLogFilter: value => { toolLogFilter = value },
    setTransientError: value => { transientError = value },
    setPlannerFallbackNotice: value => { plannerFallbackNotice = value },
    handleModelChipAction,
  })) return
  if (action === 'master-ask') {
    // Курсор ставится не здесь: отрисовка отложена до кадра, и поле, которому
    // мы бы его задали, к тому времени уже заменено новым. Раньше каретка
    // оставалась в начале — дописанное уточнение оказывалось перед вопросом.
    masterDraft = target.dataset.question || ''
    masterCaretToEnd = true
    persistDraft()
    render()
  }
  if (action === 'master-send-prompt') {
    sendMasterMessage(target.dataset.message || '')
  }
  // Согласие на создание агента — отдельный шаг перед запуском. Ядро теперь
  // тоже его требует (RosterConsent), поэтому кнопка «Подтвердить и запустить»
  // у наряда с новым исполнителем сначала раскрывает блок согласия.
  // Карточка найма правит ростер существующим маршрутом ревизии и ничего не
  // создаёт сама: её действия живут в своём модуле, здесь — одна строка.
  if (handleMasterHiringAction(action, target, {
    hiring: Array.isArray(masterData?.hiring) ? masterData.hiring : [],
    reviseRoster: (workOrderId, mutate) => reviseWorkOrderRosterV2(workOrderId, mutate),
    render,
  })) return
  // Карточка нового исполнителя заводит агента сама: её действия живут в своём
  // модуле, здесь — одна строка диспетчеризации и три опоры, которых у вида
  // быть не может: каталог чертежей, уход в мастерскую и создание агента.
  if (handleMasterAgentCardAction(action, target, {
    cards: masterAgentCards(),
    vscode,
    render,
    connections: () => state.boot?.connections || [],
    blueprintById: id => (state.boot?.blueprints || []).find(item => item.id === id),
    openWorkshop: (card, value) => openAgentWorkshopFromCard(card, value),
    createAgent: (card, value) => createAgentFromCard(card, value),
  })) return
  if (action === 'approve-master-work-order-v2') {
    const id=String(target.dataset.id || '')
    if (!id || masterWorkOrderBusy.has(id)) return
    const order=(Array.isArray(masterData?.workOrders)?masterData.workOrders:[]).find(item=>item.id===id)
    const rosterConsent=(order?.roster?.permanent || []).filter(draft=>draft?.requiresConsent && !draft?.existing).map(draft=>String(draft.id || ''))
    masterWorkOrderBusy.add(id)
    masterAgentConsent.delete(id)
    const idempotencyKey=globalThis.crypto?.randomUUID?.() || `approve-${Date.now()}-${Math.random().toString(36).slice(2)}`
    vscode.postMessage({type:'approveMasterWorkOrderV2',workOrderId:id,version:Number(target.dataset.version),digest:String(target.dataset.digest || ''),idempotencyKey,rosterConsent,turnId:masterClient.turns[masterClient.active]?.id})
    render()
    return
  }
  // Ревизия ростера существующим маршрутом: карточка найма меняет только состав,
  // версия и digest проверяются ядром, как при любой правке карточки запуска.
  //
  // Карточка найма считается на чтении, а обновление наряда приходит точечным
  // сообщением: без перезагрузки разговора она продолжала бы предлагать взять
  // того, кто уже в наряде. Перезагрузку просим только на свою же правку —
  // наряд обновляется и во время выполнения квеста, и дёргать историю на каждое
  // такое сообщение незачем.
  // Все карточки исполнителя разом: нажатие приходит с идентификатором, и по
  // нему надо найти карточку независимо от того, где она нарисована.
  function masterAgentCards() {
    return masterAgentCardsAll(masterData, state.boot?.companionActionProposals || [])
  }

  // Черновик конструктора из полей карточки. Чертёж, если он назван, даёт
  // промпт, цели и правила; поля карточки перекрывают его — человек правил
  // именно их.
  function agentDraftFromCard(value) {
    const blueprint = (state.boot?.blueprints || []).find(item => item.id === value.blueprintId)
    return newConstructorDraft({
      ...(blueprint || {}),
      id: '', blueprintId: value.blueprintId || '',
      name: value.name, roleDescription: value.role, mission: value.mission,
      allowedTools: value.allowedTools, toolPolicies: value.toolPolicies,
      connectionId: value.connectionId, primaryModel: value.model, model: value.model,
      reasoningEffort: value.reasoningEffort, approvalMode: value.approvalMode,
      maxSteps: value.maxSteps, maxDurationSeconds: value.maxDurationSeconds,
    })
  }

  // Мастерская открывается с набранным в карточке, а не только с чертежом:
  // прежний уход за тонкой настройкой стирал введённые имя, роль и умения.
  function openAgentWorkshopFromCard(card, value) {
    // Намерение задаётся всегда, даже когда наряда нет: оставленное от прошлого
    // найма, оно подставило бы нового агента в чужой, давно закрытый наряд.
    hireAfterSave = card.workOrderId ? 'work-order:' + card.workOrderId + '|' + (card.draftId || '') : 'card'
    state = { ...state, selectedTab: 'agents' }
    agentConstructorOpen = true
    profileEditorOpen = false
    profileDraft = undefined
    createStepError = ''
    constructorDraft = agentDraftFromCard(value)
    constructorStep = 'identity'
    persistDraft()
    render()
    vscode.postMessage({ type: 'selectTab', tab: 'agents' })
  }

  // Создание прямо из ленты. Вкладку не переключаем: агент понадобился ради
  // разговора, который человек ведёт сейчас, и выбрасывать его из разговора
  // ради списка агентов незачем.
  function createAgentFromCard(card, value) {
    hireAfterSave = card.workOrderId ? 'work-order:' + card.workOrderId + '|' + (card.draftId || '') : 'card'
    vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(agentDraftFromCard(value)), stayOnTab: true })
  }

  function reviseWorkOrderRosterV2(workOrderId, mutate) {
    const order = (Array.isArray(masterData?.workOrders) ? masterData.workOrders : []).find(item => item.id === workOrderId)
    if (!order || masterWorkOrderBusy.has(workOrderId)) return
    const draft = JSON.parse(JSON.stringify(order))
    delete draft.digest; delete draft.runtime; delete draft.approvedVersion; delete draft.approvedDigest
    draft.roster = mutate(draft.roster || { permanent: [], temporary: [] })
    masterWorkOrderBusy.add(workOrderId)
    const idempotencyKey = globalThis.crypto?.randomUUID?.() || `roster-${Date.now()}-${Math.random().toString(36).slice(2)}`
    hiringReloadFor = workOrderId
    vscode.postMessage({ type: 'reviseMasterWorkOrderV2', workOrderId, expectedVersion: Number(order.version), expectedDigest: String(order.digest || ''), idempotencyKey, workOrder: draft })
    render()
  }

  if (action === 'save-master-work-order-v2') {
    const id=String(target.dataset.id || '')
    const order=(Array.isArray(masterData?.workOrders)?masterData.workOrders:[]).find(item=>item.id===id)
    const card=target.closest?.('.master-v2-order')
    if (!id || !order || !card || masterWorkOrderBusy.has(id)) return
    try {
      const draft=JSON.parse(JSON.stringify(order))
      delete draft.digest;delete draft.runtime;delete draft.approvedVersion;delete draft.approvedDigest
      const lineValues=name=>String(card.querySelector(`[data-work-order-field="${name}"]`)?.value || '').split(/\r?\n/).map(value=>value.trim()).filter(Boolean)
      draft.goal=String(card.querySelector('[data-work-order-field="goal"]')?.value || '').trim()
      draft.scope=lineValues('scope')
      draft.assumptions=lineValues('assumptions')
      draft.outOfScope=lineValues('outOfScope')
      for (const input of card.querySelectorAll('[data-work-order-json]')) {
        const field=String(input.dataset.workOrderJson || '')
        if (field) draft[field]=JSON.parse(String(input.value || 'null'))
      }
      masterWorkOrderBusy.add(id)
      const idempotencyKey=globalThis.crypto?.randomUUID?.() || `revise-${Date.now()}-${Math.random().toString(36).slice(2)}`
      vscode.postMessage({type:'reviseMasterWorkOrderV2',workOrderId:id,expectedVersion:Number(order.version),expectedDigest:String(order.digest || ''),idempotencyKey,workOrder:draft})
      render()
    } catch (error) {
      masterComposeNote=`Карточка не сохранена: ${error instanceof Error?error.message:String(error)}`
      render()
    }
    return
  }
  if (action === 'control-master-work-order-v2') {
    const id=String(target.dataset.id || '')
    const questId=String(target.dataset.questId || '')
    const control=String(target.dataset.control || '')
    if (!id || !questId || !['pause','resume','cancel','message'].includes(control) || masterWorkOrderBusy.has(id)) return
    // Запущенный наряд — прогон, а не карточка: только `.master-v2-order` терял бы поле сообщения ровно там, где оно и нужно.
    const card=target.closest?.('.master-v2-order, .master-v2-run')
    const message=control==='message' ? String(card?.querySelector?.('[data-work-order-message]')?.value || '').trim() : ''
    if (control==='message' && !message) { masterComposeNote='Введите сообщение активному квесту';render();return }
    masterWorkOrderBusy.add(id)
    vscode.postMessage({type:'controlMasterWorkOrderQuestV2',workOrderId:id,questId,action:control,message})
    render()
    return
  }
  if (action === 'enable-docker-sandbox') {
    // Настройку и перезапуск ядра делает расширение: у вебвью нет доступа ни к
    // конфигурации, ни к процессу. Ответ придёт обычным обновлением состояния.
    vscode.postMessage({type:'enableDockerSandbox'})
    masterComposeNote='Включаем Docker sandbox и перезапускаем ядро…'
    render()
    return
  }
  if (action === 'control-master-application-v2') {
    const id=String(target.dataset.id || '')
    const questId=String(target.dataset.questId || '')
    const control=String(target.dataset.control || '')
    if (!id || !questId || !['start','stop'].includes(control) || masterWorkOrderBusy.has(id)) return
    masterWorkOrderBusy.add(id)
    const idempotencyKey=globalThis.crypto?.randomUUID?.() || `application-${Date.now()}-${Math.random().toString(36).slice(2)}`
    vscode.postMessage({type:'controlMasterApplicationV2',workOrderId:id,questId,action:control,version:Number(target.dataset.version),digest:String(target.dataset.digest || ''),deliveryReceiptId:String(target.dataset.receiptId || ''),idempotencyKey})
    render()
    return
  }
  if (action === 'revise-master-work-order-v2') {
    masterDraft='Измени карточку запуска: '
    masterCaretToEnd=true
    persistDraft();render()
    return
  }
  if (action === 'copy-master-message') {
    const item = masterMessageById(target.dataset.id)
    if (item) vscode.postMessage({ type: 'copyMasterText', text: String(item.content || '') })
    return
  }
  if (action === 'regenerate-master-message') {
    // «Ответить иначе» переспрашивает тот же вопрос с признаком повтора —
    // иначе модель вернёт тот же ответ слово в слово. Ветвление ленты —
    // отдельная кнопка master-fork-message.
    sendMasterMessage(target.dataset.message || '', { retry: true })
    return
  }
  if (action === 'master-fork-message') {
    const anchor = target.dataset.id
    if (anchor) vscode.postMessage({ type: 'forkMasterConversation', messageId: anchor, regenerate: false, draft: target.dataset.message || '' })
    return
  }
  if (action === 'master-step-expand') {
    const key = String(target.dataset.key || '')
    if (!key) return
    if (masterExpandedSteps.has(key)) masterExpandedSteps.delete(key)
    else masterExpandedSteps.add(key)
    render()
    return
  }
  if (action === 'master-message-details') {
    const id = String(target.dataset.id || '')
    const item = masterMessageById(id)
    // Окно объясняет ответ, и без вопроса объяснять нечего: показываем реплику
    // человека, на которую отвечали, а не «запрос не найден».
    if (item) vscode.postMessage({ type: 'openMasterMessageDetails', item, request: masterAskBefore(id) })
    return
  }
  if (action === 'master-feedback') {
    const id = String(target.dataset.id || '')
    const item = masterMessageById(id)
    if (!item) return
    // Повторное нажатие снимает отметку: передумать можно, и «полезно» второй
    // раз значит именно это, а не подтверждение.
    const value = item.feedback === target.dataset.value ? '' : target.dataset.value
    vscode.postMessage({ type: 'masterFeedback', messageId: id, value })
    return
  }
  if (action === 'master-find-step') {
    masterFindIndex += Number(target.dataset.step || 1)
    applyMasterFind(true)
    return
  }
  if (action === 'master-find-open') {
    masterFindOpen = true
    render()
    // Раскрыли — значит собираются искать: второй клик по полю лишний.
    // preventScroll обязателен: фокус без него утаскивает ленту (договорённость 19).
    root.querySelector('#master-find')?.focus({ preventScroll: true })
    return
  }
  if (action === 'master-find-clear') {
    masterFindQuery = ''
    masterClient.query = ''
    vscode.postMessage({type:'masterPage',conversationId:masterClient.active})
    masterFindOpen = false
    masterFindIndex = 0
    render()
    return
  }
  if (action === 'master-scroll-latest') {
    const thread = root.querySelector('#master-thread')
    if (thread) thread.scrollTop = thread.scrollHeight
    masterAutoFollow = true
    updateMasterScrollCue()
    return
  }
  if (action === 'master-load-earlier') {
    masterLoadingEarlier = true
    // Полный хвост за один запрос: страничная догрузка вверх на ленте с
    // «липкими» датами дороже, чем весь разговор целиком.
    vscode.postMessage({ type: 'loadMaster', full: true, conversationId: masterClient.active })
    render()
    return
  }
  if(action==='master-load-latest'){masterClient.query='';masterFindQuery='';vscode.postMessage({type:'masterPage',conversationId:masterClient.active});return}
  if (action === 'keep-run-all') {
    const runId = String(target.dataset.runId || state.details?.run?.id || '')
    if (runId) keptRunId = runId
    persistDraft()
    render()
    return
  }
  if (action === 'undo-run-all') {
    const runId = String(target.dataset.runId || state.details?.run?.id || '')
    if (runId) vscode.postMessage({ type: 'undoRunPatches', runId })
    return
  }
  if (action === 'undo-run-file') {
    const runId = String(target.dataset.runId || state.details?.run?.id || '')
    const patchIds = String(target.dataset.patchIds || '').split(',').map(item => item.trim()).filter(Boolean)
    if (runId) vscode.postMessage({ type: 'undoRunPatches', runId, patchIds })
    return
  }
  if (action === 'master-mention-pick') {
    // Мышью — то же, что Enter с клавиатуры: путь берётся из строки, а «@» с
    // запросом уходит из черновика.
    const { at, query } = masterMentionState()
    closeMasterMention()
    pickMasterMention({ path: target.dataset.path || '' }, at, query)
    return
  }
  if (action === 'master-send') sendMasterMessage()
  if (action === 'stop-master-chat') {
    // Без turnId расширение не звало отмену вовсе: `if (message.turnId)` не
    // срабатывал, и кнопка только снимала local-замок. Теперь ход гасится и в ядре.
    vscode.postMessage({ type: 'stopMasterChat', turnId: masterClient.turns[masterClient.active]?.id || '' })
    masterSending = false
    forgetMasterSent()
    stopMasterWaitClock()
    // Честно: отмена ушла, но ядро могло успеть довести ход до конца.
    masterComposeNote = 'Ядро могло довести его до конца. Частичный текст останется в разговоре.'
    render()
    return
  }
  if (action === 'retry-master') {
    // Тот же путь, что у очереди решений: 'idle' — единственное состояние, из
    // которого раздел сам запрашивает переписку. Повторяет человек, не таймер.
    masterStatus = 'idle'
    render()
  }
  if (action === 'pick-decision') { decisionPick = target.dataset.id || ''; render() }
  if (action === 'repeat-quest') {
    // Повтор не запускает прогон молча: задача переносится в брифинг, модель и
    // персонажа человек выбирает сам. Иначе кнопка тратила бы бюджет вслепую.
    taskDraft = target.dataset.task || ''
    state.selectedTab = 'quests'
    vscode.postMessage({ type: 'selectTab', tab: 'quests' })
    persistDraft()
    render()
  }
  if (action === 'pick-file-history') {
    fileHistoryPath = target.dataset.path || ''
    fileHistoryStatus = 'loading'
    fileHistoryData = undefined
    vscode.postMessage({ type: 'loadFileHistory', path: fileHistoryPath })
    render()
  }
  if (action === 'revert-file-entry') {
    const source = target.dataset.path || ''
    const id = (target.dataset.id || '').split('/')[0]
    if (source.startsWith('/api/patches/')) vscode.postMessage({ type: 'revertPatch', id })
    else if (source.startsWith('/api/change-sets/')) vscode.postMessage({ type: 'revertChangeSet', id })
  }
  if (action === 'retry-decisions') {
    decisionsStatus = 'idle'
    decisionsError = ''
    render()
  }
  if (action === 'resolve-decision') {
    // Что и куда слать, считается из самого решения, а не переносится через
    // разметку: атрибут умеет только строку, а часть маршрутов ждёт булево —
    // «true» строкой для них не согласие, а чужой тип. Кнопка и горячая
    // клавиша теперь считают одинаково, потому что считают одним кодом.
    const id = target.dataset.id || ''
    const item = (decisionsData?.items || []).find(entry => entry.id === id)
    if (item) sendDecisionResolve(id, decisionIntents(item)[target.dataset.intent === 'reject' ? 'reject' : 'accept'])
  }
  if (action === 'toggle-quest') {
    const id = String(target.dataset.id || '')
    toggleOpenQuest(id)
    render()
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
      transientError = 'Для replan нужны этап и причина'
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
  }
  if (action === 'submit-quest-revise') {
    const questId = String(target.dataset.questId || '')
    const panel = target.closest('.quest-midflight')
    if (!questId || !panel || target.disabled) return
    const expectedVersion = Number(target.dataset.expectedVersion || 0)
    const goal = String(panel.querySelector('[name="reviseGoal"]')?.value || '').trim()
    if (!goal) {
      transientError = 'Новая цель пуста'
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
  }
  if (action === 'tab') {
    const tab = canonicalTab(target.dataset.tab)
    if (tab !== 'onboarding' && hubNavLocked()) return
    // Уход в другой раздел закрывает настройку компаньона: панель не должна
    // удерживать оболочку.
    if (companionSetupOpen && tab !== 'overview') companionSetupOpen = false
    if (state.selectedTab !== tab) {
      state = { ...state, selectedTab: tab }
      persistDraft()
      render()
      if (tab === 'overview' && (pendingQuestProposals().length || pendingActionProposals().length)) {
        requestAnimationFrame(() => root.querySelector('#companion-review')?.scrollIntoView({ block: 'nearest', behavior: 'smooth' }))
      }
    }
    vscode.postMessage({ type: 'selectTab', tab })
  }
  if (handleCompanionClickAction({
    action, target, root, ui: modularUiState, vscode, persistDraft, render,
    stopCompanionChat, scrollCompanionThread, sendCompanionUserMessage, focusCompanionInput, canonicalTab,
    normalizeCompanionSetupStep, companionSetupDraftFromConfig, currentCompanionSetupDraft,
    companionSetupValidation, companionConfigFromDraft, refreshCompanionSetup, refreshCompanionLiveSurfaces,
    sanitizeCompanionSetupDraft, companionConnections, companionNormalizeSceneId, companionSceneById,
    defaultCompanionProviderPreset, onboardingCompanionActive, onboardingCompanionDraft,
    writeOnboardingCompanionDraft, onboardingOrchestratorActive, writeOnboardingOrchestratorDraft,
    currentOnboardingOrchestratorValues,
  })) return
  if (handleOnboardingClickAction({
    ui: modularUiState,
    action, target, root, vscode, persistDraft, render, ONBOARDING_STEPS, ORCHESTRATOR_PRESETS, TOOL_PRESETS,
    acceptFirstAgentProposal, agentById, canEnterOnboardingStep, companionConnections, companionProviderPresets,
    companionSetupValidation, companionSuggestedTemplateId, constructorToProfile, constructorToProjectAgent,
    currentConstructorForm, currentOnboardingOrchestratorValues, defaultCompanionProviderPreset,
    firstAgentDraftFromProposal, firstAgentProposal, hubAgents, hubModeAvailable, isCompanionOnboardingStep,
    isOrchestratorOnboardingStep, isSystemOnboardingStep, legacySaveWouldDrop, newConstructorDraft,
    onboardingAgentTemplates, onboardingCompanionDraft, onboardingLockReason, onboardingOrchestratorDraft,
    onboardingPrimaryAgent, orchestratorSetupValidation, patchOnboardingAgent, persistOnboardingCompanion,
    persistOnboardingOrchestrator, providerCatalog, resetCompiledPromptPreview, sanitizeOrchestratorDraft,
    writeOnboardingOrchestratorDraft,
  })) return
  if (action === 'companion-suggest-ignore') {
    ignoredCompanionSuggestions.add(target.dataset.suggestId || '')
    persistDraft()
    render()
  }
  if (action === 'companion-suggest-add') {
    const kind = target.dataset.kind || ''
    const suggestId = target.dataset.suggestId || ''
    ignoredCompanionSuggestions.add(suggestId)
    const draft = currentConstructorForm() || constructorDraft || newConstructorDraft()
    if (kind === 'tool' && target.dataset.tool) {
      draft.allowedTools = [...new Set([...(draft.allowedTools || []), target.dataset.tool])]
      constructorDraft = draft
      persistDraft()
      render()
    } else if (kind === 'nav' && target.dataset.tab) {
      vscode.postMessage({ type: 'selectTab', tab: target.dataset.tab })
    } else {
      persistDraft()
      render()
    }
  }
  if (action === 'master-new-discussion') {
    masterDiscussionProposalId = ''
    if (masterData?.response?.proposal) {
      masterData = { ...masterData, response: { ...masterData.response, proposal: undefined } }
    }
    persistDraft()
    render()
  }
  if (action === 'quest-proposal-discuss') {
    masterDiscussionProposalId = target.dataset.id || ''
    state.selectedTab = 'master'
    persistDraft()
    vscode.postMessage({ type: 'selectTab', tab: 'master' })
    render()
  }
  if (action === 'quest-proposal-start') {
    const id = target.dataset.id || ''
    if (proposalStarting.has(id) || proposalModifying.has(id)) return
    const proposal = taskProposalById(id)
    if (proposal?.brief && proposalEditId === id) return
    const edits = proposalEditId === id ? proposalDecisionPayload(id) : proposal?.brief ? { expectedVersion: proposal.brief.version, approveVersion: proposal.brief.mode === 'project' ? proposal.brief.version : undefined } : {}
    if (proposalEditId === id) proposalEditDrafts.set(id, edits)
    proposalStarting.add(id)
    vscode.postMessage({
      type: 'decideQuestProposal',
      proposalId: id,
      action: 'start',
      startFlow: true,
      origin: state.selectedTab === 'master' ? 'master' : '',
      ...edits,
    })
    render()
  }
  if (action === 'quest-proposal-ignore') {
    vscode.postMessage({ type: 'decideQuestProposal', proposalId: target.dataset.id, action: 'ignore' })
    companionAppliedNotice = { status: 'Предложение отклонено', hint: 'Помощник не предложит то же самое снова, пока вы сами не попросите', tab: '' }
    persistDraft()
    render()
  }
  if (action === 'quest-proposal-modify') {
    const id = target.dataset.id || ''
    if (proposalEditId !== id) {
      proposalEditDrafts.delete(id)
      proposalEditId = id
      render()
      return
    }
    if (proposalModifying.has(id)) return
    const edits = proposalDecisionPayload(id)
    proposalEditDrafts.set(id, edits)
    proposalModifying.add(id)
    vscode.postMessage({ type: 'decideQuestProposal', proposalId: id, action: 'modify', ...edits })
    render()
  }
  if (action === 'companion-action-apply') {
    const id = target.dataset.id || ''
    if (companionActionApplying.has(id) || companionActionModifying.has(id)) return
    const edits = companionActionEditId === id ? companionActionDecisionPayload(id) : {}
    if (companionActionEditId === id) companionActionEditDrafts.set(id, edits)
    companionActionApplying.add(id)
    vscode.postMessage({ type: 'decideCompanionAction', proposalId: id, action: 'apply', origin: state.selectedTab === 'master' ? 'master' : 'companion', ...edits })
    render()
  }
  if (action === 'companion-action-modify') {
    const id = target.dataset.id || ''
    if (companionActionEditId !== id) {
      companionActionEditDrafts.delete(id)
      companionActionEditId = id
      render()
      return
    }
    if (companionActionModifying.has(id) || companionActionApplying.has(id)) return
    const edits = companionActionDecisionPayload(id)
    companionActionEditDrafts.set(id, edits)
    companionActionModifying.add(id)
    vscode.postMessage({ type: 'decideCompanionAction', proposalId: id, action: 'modify', origin: state.selectedTab === 'master' ? 'master' : 'companion', ...edits })
    render()
  }
  if (action === 'companion-action-ignore') {
    vscode.postMessage({ type: 'decideCompanionAction', proposalId: target.dataset.id || '', action: 'ignore', origin: state.selectedTab === 'master' ? 'master' : 'companion' })
    companionAppliedNotice = { status: 'Предложение отклонено', hint: 'Помощник не предложит то же самое снова, пока вы сами не попросите', tab: '' }
    persistDraft()
    render()
  }
  if (action === 'preview-equip-skill') {
    vscode.postMessage({ type: 'previewEquipSkill', skillId: target.dataset.id })
  }
  if (action === 'confirm-equip-skill') {
    vscode.postMessage({ type: 'equipSkill', skillId: target.dataset.id })
    pendingSkillEquip = undefined
  }
  if (action === 'cancel-equip-skill') {
    pendingSkillEquip = undefined
    render()
  }
  if (action === 'revert-execution') {
    vscode.postMessage({ type: 'revertExecution', id: target.dataset.id })
  }
  if (action === 'revert-quest') {
    vscode.postMessage({ type: 'revertQuest', id: target.dataset.id })
  }
  // Откат возвращает правки, удаление убирает саму карточку: это разные
  // действия, и подтверждение у удаления своё — его спрашивает расширение.
  if (action === 'delete-quest') {
    vscode.postMessage({ type: 'deleteQuest', id: target.dataset.id })
  }
  // Отряд под квест собирается сам и переживает свой квест. Пока его нельзя
  // было распустить, он навсегда держал участников: роспуск персонажа
  // отказывал, ссылаясь на отряд, до которого было не дотянуться.
  if (action === 'delete-team') {
    vscode.postMessage({ type: 'deleteTeam', id: target.dataset.id })
  }
  if (action === 'revert-flow-node') {
    vscode.postMessage({ type: 'revertFlowNode', flowRunId: target.dataset.flowRunId, nodeId: target.dataset.nodeId })
  }
  if (action === 'launch-execution') {
    vscode.postMessage({ type: 'launchExecution', id: target.dataset.id, apiKey })
  }
  if (action === 'cancel-cursor-execution') {
    vscode.postMessage({ type: 'cancelCursorExecution', id: target.dataset.id })
  }
  if (action === 'resolve-flow-node') {
    vscode.postMessage({
      type: 'resolveFlowNode',
      flowRunId: target.dataset.flowRunId,
      nodeId: target.dataset.nodeId,
      approved: target.dataset.approved === 'true',
    })
  }
  if (action === 'resolve-flow-merge') {
    vscode.postMessage({
      type: 'resolveFlowMerge',
      flowRunId: target.dataset.flowRunId,
      nodeId: target.dataset.nodeId,
      resolution: { path: target.dataset.path, strategy: 'use_parent', executionId: target.dataset.executionId },
    })
  }
  if (action === 'resolve-flow-merge-manual' || action === 'resolve-flow-merge-delete') {
    const row = target.closest('.flow-merge-conflict-row')
    const content = row?.querySelector('.flow-merge-manual')?.value ?? ''
    vscode.postMessage({
      type: 'resolveFlowMerge',
      flowRunId: target.dataset.flowRunId,
      nodeId: target.dataset.nodeId,
      resolution: {
        path: target.dataset.path,
        strategy: 'manual',
        ...(action === 'resolve-flow-merge-delete' ? { delete: true } : { content }),
      },
    })
  }
  if (action === 'rebuild-index') vscode.postMessage({type:'rebuildIndex'})
  if (action === 'open-quick-chat') vscode.postMessage({type:'openQuickChat'})
  if (action === 'save-quick-chat') {
    const profile = root.querySelector('#quick-chat-profile')?.value || ''
    vscode.postMessage({ type: 'saveQuickChatSettings', defaultProfileId: profile })
  }
  if (action === 'revert-patch') vscode.postMessage({type:'revertPatch',id:target.dataset.id})
	if (action === 'apply-changeset') vscode.postMessage({ type: 'applyChangeSet', id: target.dataset.id })
	if (action === 'apply-changeset-chain') vscode.postMessage({ type: 'applyChangeSetChain', id: target.dataset.id })
	if (action === 'reject-changeset') vscode.postMessage({ type: 'rejectChangeSet', id: target.dataset.id })
	if (action === 'revert-changeset') vscode.postMessage({ type: 'revertChangeSet', id: target.dataset.id })
  if (action === 'load-run') vscode.postMessage({type:'loadRun',id:target.dataset.id})
  if (action === 'cancel') vscode.postMessage({type:'cancelRun',runId:target.dataset.id})
  if (action === 'pause-run') vscode.postMessage({ type: 'pauseRun', runId: target.dataset.runId })
  if (action === 'resume-run') vscode.postMessage({ type: 'resumeRun', runId: target.dataset.runId, apiKey })
  if (action === 'extend-active-time') vscode.postMessage({ type: 'extendActiveTime', runId: target.dataset.runId, apiKey })
  if (action === 'load-context-inspector') {
    contextInspectorRunId = target.dataset.runId || ''
    contextInspectorStatus = 'loading'
    contextInspector = undefined
    contextInspectorNotice = ''
    render()
    vscode.postMessage({ type: 'loadContextInspector', runId: contextInspectorRunId })
  }
  if (action === 'close-context-inspector') {
    contextInspectorRunId = ''
    contextInspectorStatus = 'idle'
    contextInspector = undefined
    contextInspectorNotice = ''
    render()
  }
  if (action === 'context-amend') {
    vscode.postMessage({
      type: 'contextAmend',
      runId: target.dataset.runId,
      action: target.dataset.amend,
      itemId: target.dataset.itemId,
    })
  }
  if (action === 'add-run-context-files') {
    contextInspectorNotice = ''
    vscode.postMessage({ type: 'addRunContextFiles', runId: target.dataset.runId })
  }
  if (action === 'add-run-context-selection') {
    contextInspectorNotice = ''
    vscode.postMessage({ type: 'addRunContextSelection', runId: target.dataset.runId })
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
  }
  if (action === 'reload-statistics') {
    statisticsStatus = 'loading'
    render()
    vscode.postMessage({ type: 'loadStatistics' })
  }
  if (action === 'create-system-backup') {
    statisticsStatus = 'loading'
    render()
    vscode.postMessage({ type: 'createSystemBackup' })
  }
  if (action === 'restore-system-backup') {
    vscode.postMessage({ type: 'restoreSystemBackup' })
  }
  if (action === 'reload-docker') {
    dockerStatus = 'loading'
    dockerLogs = undefined
    render()
    vscode.postMessage({ type: 'loadDocker' })
  }
  if (action === 'docker-control') {
    vscode.postMessage({ type: 'dockerContainerAction', action: target.dataset.actionKind, container: target.dataset.container })
  }
  if (action === 'docker-logs') {
    dockerLogsContainer = target.dataset.container || ''
    vscode.postMessage({ type: 'dockerLogs', container: dockerLogsContainer })
  }
  if (action === 'docker-clear-logs') {
    dockerLogs = undefined
    dockerLogsContainer = ''
    render()
  }
  if (action === 'docker-terminal-logs') {
    vscode.postMessage({ type: 'dockerOpenTerminal', mode: 'logs', container: target.dataset.container })
  }
  if (action === 'docker-terminal-shell') {
    vscode.postMessage({ type: 'dockerOpenTerminal', mode: 'shell', container: target.dataset.container })
  }
  if (action === 'docker-terminal-ps') {
    vscode.postMessage({ type: 'dockerOpenTerminal', mode: 'ps' })
  }
  if (action === 'probe-server') {
    vscode.postMessage({ type: 'probeServerProfile', id: target.dataset.id })
  }
  if (action === 'open-server-terminal') {
    vscode.postMessage({ type: 'openServerTerminal', id: target.dataset.id })
  }
  if (action === 'list-server-path') {
    vscode.postMessage({ type: 'listServerRemote', id: target.dataset.id, path: target.dataset.path || '~' })
  }
  if (action === 'edit-server') {
    serverEditingId = target.dataset.id || ''
    render()
    requestAnimationFrame(() => {
      root.querySelector('#server-form')?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
      root.querySelector('#server-name')?.focus?.()
    })
  }
  if (action === 'cancel-edit-server') {
    serverEditingId = ''
    render()
  }
  if (action === 'delete-server') {
    if (serverEditingId === target.dataset.id) serverEditingId = ''
    vscode.postMessage({ type: 'deleteServerProfile', id: target.dataset.id })
  }
  // Подключения к моделям: правка держится в интерфейсе, остальное уходит ядру.
  if (action === 'edit-connection') {
    connectionEditingId = target.dataset.id || ''
    render()
    root.querySelector('#connection-form')?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
  }
  // Во вложенной раскрывашке форма не может быть <form>, поэтому сохранение

  // приходит действием. Тело одно и то же.

  if (action === 'save-connection') { saveConnectionFromFields(); return }
  if (action === 'save-model-routing') {
    const field = name => root.querySelector(`[data-routing-field="${name}"]`)?.value?.trim() || ''
    vscode.postMessage({
      type: 'saveModelRouting',
      routing: {
        codingConnectionId: field('codingConnectionId'),
        codingModel: field('codingModel'),
        cheapConnectionId: field('cheapConnectionId'),
        cheapModel: field('cheapModel'),
      },
    })
    return
  }

  if (action === 'cancel-edit-connection') {
    connectionEditingId = ''
    render()
  }
  if (action === 'probe-connection') {
    vscode.postMessage({ type: 'probeConnection', id: target.dataset.id })
  }
  if (action === 'default-connection') {
    vscode.postMessage({ type: 'defaultConnection', id: target.dataset.id })
  }
  if (action === 'delete-connection') {
    vscode.postMessage({ type: 'deleteConnection', id: target.dataset.id })
  }
  if (action === 'select-db') {
    dbSelectedId = target.dataset.id || ''
    dbQueryResult = undefined
    dbSchemaResult = undefined
    dbWritePending = null
    render()
  }
  if (action === 'test-db') {
    vscode.postMessage({ type: 'testDBConnection', id: target.dataset.id })
  }
  if (action === 'schema-db') {
    vscode.postMessage({ type: 'schemaDBConnection', id: target.dataset.id || dbSelectedId })
  }
  if (action === 'edit-db') {
    dbEditingId = target.dataset.id || ''
    dbSelectedId = dbEditingId
    render()
    requestAnimationFrame(() => {
      root.querySelector('#db-connection-form')?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
      root.querySelector('#db-name')?.focus?.()
    })
  }
  if (action === 'cancel-edit-db') {
    dbEditingId = ''
    render()
  }
  if (action === 'delete-db') {
    if (dbEditingId === target.dataset.id) dbEditingId = ''
    vscode.postMessage({ type: 'deleteDBConnection', id: target.dataset.id })
  }
  if (action === 'apply-db-write') {
    if (!dbWritePending?.connectionId || !dbWritePending?.sql) return
    dbQueryStatus = 'loading'
    const pending = dbWritePending
    dbWritePending = null
    render()
    vscode.postMessage({ type: 'queryDBConnection', connectionId: pending.connectionId, sql: pending.sql, allowWrite: true, approved: true })
  }
  if (action === 'ignore-db-write') {
    dbWritePending = null
    render()
  }
  if (action === 'memory-edit') {
    memoryEditId = target.dataset.id || ''
    memoryDraft = (state.boot?.memories || []).find(item => item.id === memoryEditId)
    render()
  }
  if (action === 'memory-cancel-edit') {
    memoryEditId = ''
    memoryDraft = undefined
    render()
  }
  if (action === 'cancel-manual-learning') {
    manualLearningPreview = undefined
    manualLearningStatus = 'idle'
    render()
  }
  if (action === 'apply-manual-learning') {
    if (!manualLearningDraft || !manualLearningPreview?.confirmationToken) return
    manualLearningStatus = 'applying'
    vscode.postMessage({ type: 'applyManualLearning', request: { ...manualLearningDraft, confirmationToken: manualLearningPreview.confirmationToken } })
    render()
  }
  if (action === 'skill-edit') {
    skillEditId = target.dataset.id || ''
    skillDraft = (state.boot?.skills || []).find(item => item.id === skillEditId)
    skillFormError = ''
    skillEquipAfterSave = false
    render()
  }
  if (action === 'skill-cancel-edit') {
    skillEditId = ''
    skillDraft = undefined
    skillFormError = ''
    skillEquipAfterSave = true
    render()
  }
  if (action === 'memory-pin' || action === 'memory-delete') {
    const item = (state.boot?.memories || []).find(entry => entry.id === target.dataset.id)
    if (!item) return
    if (action === 'memory-delete') {
      vscode.postMessage({ type: 'deleteMemory', memoryId: item.id })
      if (memoryEditId === item.id) { memoryEditId = ''; memoryDraft = undefined }
      return
    }
    vscode.postMessage({ type: 'saveMemory', memory: { ...item, pinned: target.dataset.pinned === 'true' } })
  }
  if (action === 'flow-legacy-mode') {
    flowLegacyMode = true
    persistDraft()
    render()
  }
  if (action === 'flow-visual-mode') {
    flowLegacyMode = false
    persistDraft()
    render()
  }
  if (action === 'start-flow') {
    vscode.postMessage({
      type: 'startFlowRun',
      flowId: target.dataset.flowId || '',
      input: { title: (flowDraft?.name || 'Flow run') },
    })
  }
  if (action === 'new-flow') {
    flowDraft = { id: '', name: 'Новый флоу', description: '', nodes: [newFlowNode('input'), newFlowNode('agent'), newFlowNode('output')], edges: [] }
    flowDraft.edges = [
      { id: 'edge-0', from: flowDraft.nodes[0].id, to: flowDraft.nodes[1].id },
      { id: 'edge-1', from: flowDraft.nodes[1].id, to: flowDraft.nodes[2].id },
    ]
    selectedFlowId = ''
    selectedFlowNodeId = flowDraft.nodes[1].id
    flowLegacyMode = false
    persistDraft()
    render()
  }
  if (action === 'add-flow-node') {
    const draft = captureFlowForm()
    const node = newFlowNode('agent')
    node.positionX = 24 + ((draft.nodes?.length || 0) % 3) * 150
    node.positionY = 24 + Math.floor((draft.nodes?.length || 0) / 3) * 96
    draft.nodes = [...(draft.nodes || []), node]
    flowDraft = draft
    selectedFlowNodeId = node.id
    render()
  }
  if (action === 'add-flow-edge') {
    const draft = captureFlowForm()
    const nodes = draft.nodes || []
    if (nodes.length < 2) return
    draft.edges = [...(draft.edges || []), { id: `edge-${Date.now()}`, from: nodes[0].id, to: nodes[1].id }]
    flowDraft = draft
    render()
  }
  if (action === 'select-flow-node') {
    const draft = captureFlowForm()
    flowDraft = draft
    selectedFlowNodeId = target.dataset.nodeId || ''
    render()
  }
  if (action === 'remove-flow-node') {
    const draft = captureFlowForm()
    const nodeId = target.dataset.nodeId
    draft.nodes = (draft.nodes || []).filter(item => item.id !== nodeId)
    draft.edges = (draft.edges || []).filter(edge => edge.from !== nodeId && edge.to !== nodeId)
    flowDraft = draft
    if (selectedFlowNodeId === nodeId) selectedFlowNodeId = draft.nodes[0]?.id || ''
    render()
  }
  if (action === 'cancel-cursor') vscode.postMessage({type:'cancelCursorRun'})
  if (action === 'resolve') vscode.postMessage({type:'resolveApproval',id:target.dataset.id,allow:target.dataset.allow==='true'})
  if (action === 'open-file') vscode.postMessage({type:'openFile',path:target.dataset.path,line:Number(target.dataset.line)||undefined})
  if (action === 'open-roster') vscode.postMessage({type:'openRoster'})
  if (action === 'select-roster-profile') { selectedProfileId=target.dataset.id||''; profileDraft=undefined; persistDraft(); render() }
  if (action === 'edit-roster-profile') {
    const selected=(state.boot?.profiles||[]).find(item=>item.id===selectedProfileId)||(state.boot?.profiles||[])[0]
    if (hubModeAvailable()) {
      const hubSelected = agentById(selectedProfileId) || hubAgents()[0]
      prepareAgentConstructor(hubSelected, constructorStepForProfileStep(firstUnreadinessStep(hubSelected)))
    } else {
      profileEditorOpen=true
      profileDraft=undefined
      profileEditorStep=firstUnreadinessStep(selected)
    }
    render()
  }
  if (action === 'close-profile-editor') { profileEditorOpen=false; profileDraft=undefined; profileEditorStep='identity'; createStepError=''; hireAfterSave=''; render() }
  if (action === 'profile-step') {
    const profile=currentFormProfile()
    if(profile) profileDraft=profile
    createStepError=''
    profileEditorStep=target.dataset.step||'identity'
    render()
  }
  if (action === 'advance-profile-step') {
    const profile=currentFormProfile()
    if(profile) profileDraft=profile
    const dir=target.dataset.dir||'next'
    const nextStep=target.dataset.step||'identity'
    if(dir==='next'){
      const issue=stepValidationIssue(profileEditorStep, profile||profileDraft)
      if(issue){createStepError=issue;render();return}
    }
    createStepError=''
    profileEditorStep=nextStep
    render()
  }
  if (action === 'skip-class-step') {
    providerProbe=undefined
    createStepError=''
    hirePreviewTemplateId=''
    profileDraft=newProfile({
      name: 'Новый агент',
      roleDescription: '',
      systemPrompt: '',
      goals: [],
      rules: [],
      allowedTools: ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff'],
      maxSteps: 30,
      maxDurationSeconds: 600,
      approvalMode: 'safe',
    })
    selectedProfileId=''
    profileEditorOpen=true
    profileEditorStep='identity'
    render()
  }
  if (action === 'preview-template') {
    hirePreviewTemplateId=target.dataset.template||''
    render()
  }
  if (action === 'fix-profile-step') {
    const step=target.dataset.step||'identity'
    if (hubModeAvailable()) {
      const selected = agentById(selectedProfileId) || hubAgents()[0]
      prepareAgentConstructor(selected, constructorStepForProfileStep(step))
    } else {
      profileEditorStep=step
      profileEditorOpen=true
      createStepError=''
    }
    if(state.selectedTab!=='agents') vscode.postMessage({type:'selectTab',tab:'agents'})
    else render()
  }
  if (action === 'rollback-agent-improvement') {
    vscode.postMessage({ type: 'rollbackAgentImprovement', id: target.dataset.id })
  }
  if (action === 'promote-agent-improvement') {
    vscode.postMessage({ type: 'promoteAgentImprovement', id: target.dataset.id })
  }
  if (action === 'improve-agent') {
    const agentId = target.dataset.id || ''
    const step = CONSTRUCTOR_STEPS.some(item => item.id === target.dataset.step) ? target.dataset.step : 'review'
    vscode.postMessage({ type: 'focusHub', tab: 'agents', agentId, constructorStep: step })
  }
  if (action === 'preview-run') { const quest=questPayload();if(quest.task){agentRunPreview=undefined;agentRunPreviewError='';agentRunPreviewStatus='loading';render();vscode.postMessage({type:'previewRun',profileId:selectedProfileId,...quest,contextItems})} }
  if (action === 'launch-cursor') {
    const profile=(state.boot?.profiles||[]).find(item=>item.id===selectedProfileId)
    // Тот же ответ, что и у обычного запуска: раньше эта ветка выходила молча,
    // и одна и та же ошибка на одном экране вела себя двумя разными способами.
    if (!taskDraft.trim()) {
      transientError = EMPTY_TASK_REASON
      render()
      return
    }
    if (state.cursorRuntime?.available && state.cursorRuntime?.authenticated) {
      cursorRunEvents=[]
      vscode.postMessage({type:'startCursorRun',profileId:selectedProfileId,task:composeQuestTask()})
    } else {
      vscode.postMessage({type:'launchCursorAgent',task:composeQuestTask(),model:profile?.model||'auto'})
    }
  }
  if (action === 'new-profile') {
    providerProbe=undefined; createStepError=''; hireAfterSave=''; selectedProfileId=''
    if (hubModeAvailable()) {
      prepareAgentConstructor({ name: 'Новый агент', allowedTools: ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff'], maxSteps: 30, maxDurationSeconds: 600, approvalMode: 'safe' })
    } else {
      hirePreviewTemplateId=state.boot?.profileTemplates?.[0]?.id||''; profileEditorOpen=true; profileDraft=newProfile({ name: 'Новый агент', roleDescription: '', systemPrompt: '', goals: [], rules: [], allowedTools: ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff'], maxSteps: 30, maxDurationSeconds: 600, approvalMode: 'safe' }); profileEditorStep='class'
    }
    render()
  }
  if (action === 'setup-provider') {
    const preset=providerCatalog().find(item=>item.id===target.dataset.preset)
    if(preset){profileDraft={...newProfile(),provider:preset.kind,providerPreset:preset.id,baseUrl:preset.baseUrl||'',model:preset.defaultModel||'auto'};selectedProfileId='';profileEditorOpen=true;profileEditorStep='model';vscode.postMessage({type:'selectTab',tab:'agents'})}
  }
  if (action === 'duplicate-profile') {
    const source=(state.boot?.profiles||[]).find(item=>item.id===selectedProfileId)
    if(source){providerProbe=undefined;profileDraft={...source,id:'',name:`${source.name} — копия`,allowedTools:[...(source.allowedTools||[])],createdAt:undefined,updatedAt:undefined};selectedProfileId='';profileEditorOpen=true;profileEditorStep='identity';render()}
  }
  if (action === 'use-template') {
    const template=(state.boot?.profileTemplates||[]).find(item=>item.id===target.dataset.template)
    if(template){
      providerProbe=undefined;createStepError='';hirePreviewTemplateId=template.id;selectedProfileId=''
      if (hubModeAvailable()) prepareAgentConstructor({ ...template, id: '', blueprintId: '' }, 'identity')
      else { profileDraft=newProfile(template);profileEditorOpen=true;profileEditorStep='model' }
      render()
    }
  }
  if (action === 'hire-and-quest') {
    const profile=currentFormProfile()
    if(!profile) return
    const issue=stepValidationIssue('limits', profile) || (!profileReadiness(profile).ready ? profileReadiness(profile).issues[0] : '')
    if(issue){createStepError=issue;profileDraft=profile;render();return}
    hireAfterSave='quest'
    profileDraft=profile
    if (hubModeAvailable()) vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(newConstructorDraft(profile)) })
    else vscode.postMessage({type:'saveProfile',profile})
  }
  if (action === 'start-roster-quest') {
    const selected=(state.boot?.profiles||[]).find(item=>item.id===selectedProfileId)||(state.boot?.profiles||[])[0]
    const readiness=profileReadiness(selected)
    if(selected && !readiness.ready){profileEditorOpen=true;profileDraft=undefined;profileEditorStep=firstUnreadinessStep(selected);render();return}
    profileEditorOpen=false; vscode.postMessage({type:'selectTab',tab:'chat'})
  }
  if (action === 'cancel-profile') { profileDraft=undefined; profileEditorOpen=false; profileEditorStep='identity'; createStepError=''; hireAfterSave=''; selectedProfileId=state.boot?.profiles?.[0]?.id||''; render() }
  if (action === 'delete-profile') vscode.postMessage({type:'deleteProfile',id:target.dataset.id})
  // Персонажа Гильдии распускает свой маршрут: deleteProfile знает только
  // legacy-профили и на проектном агенте отвечал «профиль не найден».
  if (action === 'disband-agent') vscode.postMessage({ type: 'deleteProjectAgent', id: target.dataset.id })
  // Класс, оставшийся от распущенного персонажа, убирается там же, где виден,
  // — в списке найма. Занятый класс ядро не отдаст и скажет, кто его держит.
  if (action === 'delete-blueprint') vscode.postMessage({ type: 'deleteBlueprint', id: target.dataset.id })
  if (action === 'export-profile') { const profile=currentFormProfile(); if(profile)vscode.postMessage({type:'exportProfile',profile}) }
  if (action === 'import-profile') vscode.postMessage({type:'importProfile'})
  if (action === 'probe-provider') {
    const profile=currentFormProfile()
    if(profile){profileDraft=profile;providerProbe={loading:true,models:[]};render();vscode.postMessage({type:'probeProvider',provider:profile.provider,baseUrl:profile.baseUrl,apiKey})}
  }
  if (action === 'probe-model-capability') {
    const profile = currentFormProfile()
    if (profile) {
      profileDraft = profile
      requestModelCapabilityProbe(profile)
    }
  }
  if (action === 'tool-preset') {
    const preset=TOOL_PRESETS.find(item=>item.id===target.dataset.preset)
    const values=preset?.tools === null ? (state.boot?.toolCatalog||[]).map(item=>item.name) : (preset?.tools || [])
    for(const input of root.querySelectorAll('input[name="allowed-tool"]'))input.checked=values.includes(input.value)
    const profile=currentFormProfile()
    if(profile){profileDraft=profile;render()}
  }
  if (action === 'attach-files') vscode.postMessage({type:'attachFiles'})
  if (action === 'attach-selection') vscode.postMessage({type:'attachSelection'})
  if (action === 'remove-context') { contextItems.splice(Number(target.dataset.index),1); persistDraft(); requestContextPreview() }
  if (action === 'new-custom-tool') {
    const source=state.boot?.customToolTemplates?.[0]?.tool||{kind:'process',displayName:'Новый инструмент',description:'Запускает новый инструмент после подтверждения.',program:'',arguments:[],parameters:[],cwd:'.',timeoutSeconds:120}
    resetCustomToolPreview();customToolDraft=cloneCustomTool(source);selectedCustomToolId='';render()
  }
  if (action === 'duplicate-custom-tool') { const source=currentCustomToolForm();if(source){resetCustomToolPreview();customToolDraft=cloneCustomTool({...source,displayName:`${source.displayName} — копия`});selectedCustomToolId='';render()} }
  if (action === 'use-custom-tool-template') {
    const template=(state.boot?.customToolTemplates||[]).find(item=>item.id===target.dataset.template)
    if(template){resetCustomToolPreview();customToolDraft=cloneCustomTool(template.tool);selectedCustomToolId='';render()}
  }
  if (action === 'add-tool-parameter') {
    const value=currentCustomToolForm();if(value&&value.parameters.length<16){resetCustomToolPreview();const used=new Set(value.parameters.map(item=>item.name));let index=value.parameters.length+1;while(used.has(`input_${index}`))index++;value.parameters.push({name:`input_${index}`,displayName:`Параметр ${index}`,description:'Опишите допустимое значение для модели',type:'string',required:true,enumValues:[],maxLength:1024});customToolDraft=value;render()}
  }
  if (action === 'remove-tool-parameter') { const value=currentCustomToolForm();const index=Number(target.dataset.index);if(value){resetCustomToolPreview();value.parameters.splice(index,1);customToolDraft=value;render()} }
  if (action === 'preview-custom-tool') {
    const tool=currentCustomToolForm()
    if(tool){const previewArguments={reason:'Проверка конфигурации в песочнице конструктора'};customToolPreviewArguments={};for(const parameter of tool.parameters||[]){const control=root.querySelector(`[data-preview-param="${parameter.name}"]`);if(!control||control.value==='')continue;const value=parameter.type==='integer'?Number(control.value):control.value;previewArguments[parameter.name]=value;customToolPreviewArguments[parameter.name]=value}customToolDraft=tool;customToolPreview=undefined;customToolPreviewError='';customToolPreviewStatus='loading';render();vscode.postMessage({type:'previewTool',tool,arguments:previewArguments})}
  }
  if (action === 'cancel-custom-tool') { resetCustomToolPreview();customToolDraft=undefined;selectedCustomToolId=state.boot?.customTools?.[0]?.id||'';render() }
  if (action === 'delete-custom-tool') vscode.postMessage({type:'deleteCustomTool',id:target.dataset.id})
  if (action === 'export-custom-tool') { const tool=currentCustomToolForm();if(tool)vscode.postMessage({type:'exportCustomTool',tool}) }
  if (action === 'import-custom-tool') vscode.postMessage({type:'importCustomTool'})
  if (action === 'new-workflow') { workflowDraft=newWorkflow();selectedWorkflowId='';render() }
  if (action === 'use-workflow-template') { workflowDraft=workflowFromTemplate(target.dataset.template);selectedWorkflowId='';render() }
  if (action === 'duplicate-workflow') {
    const source=currentWorkflowForm()
    if(source){workflowDraft={...source,id:'',name:`${source.name} — копия`,steps:source.steps.map(step=>({...step,id:''})),createdAt:undefined,updatedAt:undefined};selectedWorkflowId='';render()}
  }
  if (action === 'cancel-workflow-edit') { workflowDraft=undefined;selectedWorkflowId=state.boot?.workflows?.[0]?.id||'';render() }
  if (action === 'add-workflow-step') { const value=currentWorkflowForm();if(value&&value.steps.length<12){value.steps.push(newWorkflowStep(value.steps.length));workflowDraft=value;render()} }
  if (action === 'remove-workflow-step') { const value=currentWorkflowForm();const index=Number(target.dataset.index);if(value&&value.steps.length>1){value.steps.splice(index,1);workflowDraft=value;render()} }
  if (action === 'move-workflow-step') { const value=currentWorkflowForm();const index=Number(target.dataset.index);const next=index+Number(target.dataset.direction);if(value&&next>=0&&next<value.steps.length){[value.steps[index],value.steps[next]]=[value.steps[next],value.steps[index]];workflowDraft=value;render()} }
  if (action === 'delete-workflow') vscode.postMessage({type:'deleteWorkflow',id:target.dataset.id})
  if (action === 'load-workflow-run') vscode.postMessage({type:'loadWorkflowRun',id:target.dataset.id})
  if (action === 'cancel-workflow') vscode.postMessage({type:'cancelWorkflow',id:target.dataset.id})
})

root.addEventListener('change', event => {
  if (readMasterAgentCardInput(event.target)) { persistDraft(); render(); return }
  // Список исполнителей и важность — те же правки предложения, только через
  // флажок и выпадающий список: они приходят не событием ввода, а изменением.
  if ((event.target.dataset?.briefField || event.target.dataset?.proposalField) && proposalEditId === event.target.dataset.id) {
    proposalEditDrafts.set(proposalEditId, proposalDecisionPayload(proposalEditId))
  }
  // Та же беда была у карточки действия: имя агента, инструкции навыка и набор
  // инструментов снимались только при отправке, а фоновое обновление возвращало
  // исходный текст. Формы разные, потеря одна.
  if (event.target.dataset?.companionActionField && companionActionEditId === event.target.dataset.id) {
    companionActionEditDrafts.set(companionActionEditId, companionActionDecisionPayload(companionActionEditId))
  }
  if (handleGitChangeAction({
    event, ui: modularUiState, persistDraft, render,
    gitGroupPaths, gitChanges, syncGitTree, syncGitCommitButtons, toolWindowData,
  })) return
  if (event.target.id === 'profile') { invalidateAgentRunPreview();selectedProfileId=event.target.value; persistDraft(); render() }
  if (event.target.id === 'settings-profile') { providerProbe=undefined; modelCapabilityProbe=undefined; profileDraft=undefined; selectedProfileId=event.target.value; profileEditorStep=firstUnreadinessStep((state.boot?.profiles||[]).find(item=>item.id===event.target.value)); persistDraft(); render() }
  if (event.target.id === 'api-key') { apiKey=event.target.value; persistDraft() }
  if (event.target.name === 'provider-preset') {
    modelCapabilityProbe=undefined
    const preset=providerCatalog().find(item=>item.id===event.target.value)
    const current=currentFormProfile()
    providerProbe=undefined
    if(preset&&current){profileDraft={...current,provider:preset.kind,providerPreset:preset.id,baseUrl:preset.baseUrl||'',model:preset.defaultModel||'auto',allowedTools:preset.external?[]:current.allowedTools};render()}
  }
  if (event.target.name === 'allowed-tool') {
    const profile = currentFormProfile()
    if (profile) { profileDraft = profile; createStepError = ''; render() }
  }
  if (event.target.name === 'constructor-tool' || event.target.name === 'constructor-tool-policy') {
    const draft = currentConstructorForm()
    if (draft) constructorDraft = draft
    persistDraft()
    render()
  }
  if (event.target.id === 'custom-tool-select') { resetCustomToolPreview();customToolDraft=undefined;selectedCustomToolId=event.target.value;render() }
  if (event.target.id === 'custom-tool-kind') { resetCustomToolPreview();customToolDraft=currentCustomToolForm();render() }
  if (event.target.matches?.('[data-field="parameter-type"]')) { resetCustomToolPreview();customToolDraft=currentCustomToolForm();render() }
  if (event.target.matches?.('[data-preview-param]')) { customToolPreviewArguments[event.target.dataset.previewParam]=event.target.value;invalidateCustomToolPreview() }
  if (event.target.id === 'workflow-select') { workflowDraft=undefined;selectedWorkflowId=event.target.value;render() }
  if (event.target.id === 'flow-select') { flowDraft=undefined;selectedFlowId=event.target.value;selectedFlowNodeId='';flowLegacyMode=false;persistDraft();render() }
  if (event.target.id === 'connection-provider') {
    const option = event.target.selectedOptions?.[0]
    const urlField = root.querySelector('#connection-base-url')
    if (urlField && option?.dataset?.baseUrl !== undefined && !urlField.value) urlField.value = option.dataset.baseUrl
    if (urlField && option?.dataset?.preset === 'llmux' && !urlField.value) urlField.placeholder = 'https://llmux.company.internal/v1'
    applyLocalSourceFields(root, String(event.target.value || ''))
  }
  if (event.target.id === 'companion-preset') {
    const preset = COMPANION_PRESETS.find(item => item.id === event.target.value)
    if (preset) {
      for (const [key, value] of Object.entries(preset.values)) {
        const input = root.querySelector(`#companion-${key.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`)}`)
        if (input) input.value = String(value)
      }
    }
  }
})
root.addEventListener('input', event => {
  // Набранное в карточке исполнителя снимается на каждом знаке. Прежние формы
  // создания снимали значения только при отправке, и любой ход Мастера,
  // приход квеста или фоновое обновление ростера стирали написанное молча.
  if (readMasterAgentCardInput(event.target)) { persistDraft(); return }
  if (handleChatDirectoryInput(event.target)) { render(); return }
  if (handleProjectGalleryInput(event.target)) { render(); return }
  const question=event.target.closest?.('[data-question-key]')
  if(question){
    const answers=masterClient.questionDrafts[masterClient.active] ||= {}
    answers[question.dataset.questionKey]={
      text: question.querySelector('.hall-question-extra')?.value || '',
      selected: [...question.querySelectorAll('.hall-option.is-on')].map(btn => btn.dataset.option),
    }
    persistDraft()
    // Поле свободного ответа растёт по набранному. Где браузер умеет
    // field-sizing, высоту держит он; где не умеет — остаётся атрибут rows,
    // и считать его надо здесь: разметку слота на каждом знаке не пересобрать.
    const free = question.querySelector('textarea.hall-question-extra')
    if (free) free.rows = masterAnswerRows(free.value)
    // Счётчик у кнопки «Продолжить» — тоже на каждом знаке: отвечать на всё
    // разом человек не обязан, и он должен видеть, что уйдёт в ядро.
    if (question.closest('.hall-compose')) patchMasterAnswerNote(root, answers)
  }
  // Правки предложения снимаются на каждом вводе, а не только при отправке.
  // Между открытием формы и нажатием кнопки приходит фоновое обновление —
  // индекс, квесты, живое состояние, — и полная отрисовка возвращала поля к
  // исходному тексту. У предложений с брифом это уже было учтено, у обычных нет.
  if ((event.target.dataset?.briefField || event.target.dataset?.proposalField) && proposalEditId === event.target.dataset.id) {
    proposalEditDrafts.set(proposalEditId, proposalDecisionPayload(proposalEditId))
  }
  // Та же беда была у карточки действия: имя агента, инструкции навыка и набор
  // инструментов снимались только при отправке, а фоновое обновление возвращало
  // исходный текст. Формы разные, потеря одна.
  if (event.target.dataset?.companionActionField && companionActionEditId === event.target.dataset.id) {
    companionActionEditDrafts.set(companionActionEditId, companionActionDecisionPayload(companionActionEditId))
  }
  if(event.target.id==='api-key') { apiKey=event.target.value; persistDraft() }
  if(event.target.id==='git-commit-message') {
    gitCommitDraft = event.target.value
    // Кнопки перерисовываются точечно: полная отрисовка на каждой букве
    // отобрала бы каретку у поля.
    syncGitCommitButtons()
    persistDraft()
  }
  if(event.target.id==='base-url') { providerProbe=undefined; modelCapabilityProbe=undefined }
  if(event.target.id==='model') modelCapabilityProbe=undefined
  if(event.target.id==='companion-input'){companionDraft=event.target.value;persistDraft()}
  // Поле Мастера пересобирается из masterDraft на каждой отрисовке, а отрисовку
  // вызывает и чужое состояние. Пока набранное не попадало в masterDraft, любой
  // ответ ядра посреди набора стирал описание задачи — молча и целиком.
  if(event.target.id==='master-input'){
    masterDraft=event.target.value
    masterComposeNote=''
    persistDraft()
    // «@» ищет файлы прямо под кареткой — в любом месте строки, а не только
    // в её конце. Нативное окно источников осталось за кнопкой «@ Контекст»:
    // у ошибок сборки, git diff и буфера терминала нет пути в дереве.
    const askedFor = masterSending ? (closeMasterMention(), null) : masterMentionInput(event.target.value, event.target.selectionStart)
    if (askedFor !== null) askMasterMention(askedFor)
    if (askedFor !== null || masterMentionOpen()) render()
    else patchMasterCompose(event.target)
  }
  // Набор в поиске не перерисовывает ленту: пометка ходов делается по DOM, и
  // перерисовка на каждую букву стоила бы и прокрутки, и каретки в самом поле.
  if (event.target.id === 'master-find') {
    const had = Boolean(masterFindQuery.trim())
    masterFindQuery = event.target.value
    masterClient.query = masterFindQuery.trim()
    clearTimeout(masterClient.searchTimer)
    masterClient.searchTimer = setTimeout(()=>vscode.postMessage({type:'masterPage',conversationId:masterClient.active,query:masterClient.query}),250)
    masterFindIndex = 0
    if (had !== Boolean(masterFindQuery.trim())) render()
    else applyMasterFind()
  }
  if(event.target.closest?.('#companion-setup-form')) {
    companionSetupDraft = currentCompanionSetupDraft()
    companionSetupStatus = ''
    if (event.target.matches?.('[data-companion-personality]')) {
      companionSetupDraft.preset = 'custom'
      const output = event.target.closest('.companion-range')?.querySelector('output')
      if (output) output.textContent = `${event.target.value}%`
    }
    if (event.target.id === 'companion-setup-temperature') {
      const output = event.target.closest('label')?.querySelector('output')
      if (output) output.textContent = Number(event.target.value).toFixed(2)
    }
    if (event.target.matches?.('[data-companion-personality]') || event.target.id === 'companion-auto-act') {
      refreshCompanionLiveSurfaces(companionSetupDraft)
    }
    persistDraft()
  }
  if(event.target.id==='onboarding-agent-name'){onboardingDraft={...onboardingDraft,agentName:event.target.value};persistDraft()}
  if(event.target.closest?.('.companion-onboarding') && event.target.matches?.('[data-companion-personality]')){
    const values = currentOnboardingCompanionValues()
    onboardingDraft = { ...onboardingDraft, ...values, companionPreset: 'custom' }
    const output = event.target.closest('.companion-range')?.querySelector('output')
    if (output) output.textContent = `${event.target.value}%`
    refreshCompanionLiveSurfaces(onboardingCompanionDraft(), root.querySelector('.companion-onboarding') || root)
    persistDraft()
  }
  if(event.target.closest?.('.orchestrator-onboarding') && event.target.matches?.('[data-orchestrator-policy]')){
    writeOnboardingOrchestratorDraft({ ...currentOnboardingOrchestratorValues(), preset: 'custom' })
    const output = event.target.closest('.companion-range')?.querySelector('output')
    if (output) output.textContent = `${event.target.value}%`
    persistDraft()
  }
  if(event.target.name==='constructor-skill'){
    const previous = [...(constructorDraft?.skillIds || [])]
    const draft=currentConstructorForm()
    if(draft){
      constructorDraft=applySkillToolGrants(draft, previous)
      persistDraft()
      render()
    }
    return
  }
  if(event.target.closest?.('#constructor-form') || event.target.name==='constructor-tool'){
    const draft=currentConstructorForm()
    if(draft)constructorDraft=draft
    persistDraft()
  }
  if(event.target.id==='task'){taskDraft=event.target.value;invalidateAgentRunPreview();persistDraft()}
  if(event.target.id==='quest-goal'){questGoalDraft=event.target.value;invalidateAgentRunPreview();persistDraft()}
  if(event.target.id==='quest-criteria'){questCriteriaDraft=event.target.value;invalidateAgentRunPreview();persistDraft()}
  if(event.target.id==='quest-constraints'){questConstraintsDraft=event.target.value;invalidateAgentRunPreview();persistDraft()}
  if(event.target.closest?.('#settings-form') || event.target.closest?.('#profile-form') || event.target.name==='provider-preset' || event.target.name==='allowed-tool'){
    const profile=currentFormProfile()
    if(profile){
      profileDraft=profile
      if(root.querySelector('.create-flow') && !hireLiveTimer){
        hireLiveTimer=window.setTimeout(()=>{hireLiveTimer=0;render()},280)
      }
    }
  }
  if(event.target.closest?.('#custom-tool-form')){
    if(event.target.matches?.('[data-preview-param]'))customToolPreviewArguments[event.target.dataset.previewParam]=event.target.value
    else customToolDraft=currentCustomToolForm()
    invalidateCustomToolPreview()
  }
  if(event.target.closest?.('#workflow-form')){
    const workflow=currentWorkflowForm()
    if(workflow) workflowDraft=workflow
  }
})

// Форма, отправка которой уже ушла в ядро.
//
// Повтор опасен не везде одинаково: кнопка со своим id повторяет то же самое, а
// форма создания порождает вторую сущность — второго агента, второй отряд,
// второе подключение. Таких форм четырнадцать, и заводить в каждой свой флаг
// значило бы четырнадцать раз повторить одно правило. Запираем в одном месте:
// от отправки до ответа, которым служит состояние мира или сообщение об ошибке.
let submittingForm = ''

root.addEventListener('submit', event => {
  const formId = String(event.target?.id || '')
  const companionForm = formId === 'companion-form'
  if (!companionForm && formId && submittingForm === formId) {
    event.preventDefault()
    return
  }
  const sentBefore = sentCount
  handleSubmit(event)
  // Заперли только если обработчик действительно что-то отправил.
  // The Companion owns its concurrency policy: a second message while a reply
  // is streaming becomes companionPendingSend. The generic entity-form lock
  // would otherwise swallow every message after the first completed reply.
  if (!companionForm && formId && sentCount > sentBefore) submittingForm = formId
})

// Выбор файла открывает сравнение в редакторе. Панель показывает, ЧТО войдёт
// в коммит; смотреть, ЧЕМ отличается файл, надо там же, где его правят, — в
// редакторе, с его подсветкой, навигацией по изменениям и правкой прямо в диффе.
function gitSelectFile(file) {
  if (!file) return
  gitSelected = file
  gitMenuFor = ''
  vscode.postMessage({ type: 'gitAction', action: 'openChange', path: file, paths: [], repoRoot: toolWindowData.git?.root || '' })
  render()
}

// Коммит собирается из отметок. Кнопок две — «Коммит» и «и отправить», — и
// обе отправляют одно и то же, отличаясь только действием.
function submitGitCommit(action) {
  const changes = gitChanges()
  const paths = gitCheckedPaths()
  const conflicts = changes.filter(item => item.area === 'conflict')
  // При правке последнего коммита отмечать нечего: часто меняют одно сообщение.
  if (gitPendingAction || !gitCommitDraft.trim() || (!paths.length && !gitAmend) || conflicts.length) return
  gitPendingAction = action
  gitNotice = undefined
  gitMenuFor = ''
  render()
  vscode.postMessage({
    type: 'gitAction',
    action,
    message: gitCommitDraft,
    paths,
    amend: gitAmend,
    repoRoot: toolWindowData.git?.root || '',
  })
}

function handleSubmit(event) {
  event.preventDefault()
  if (event.target.id === 'git-commit-form') {
    submitGitCommit('commit')
    return
  }
  if (event.target.id === 'agent-form') {
    const quest = questPayload()
    const profile = (state.boot?.profiles || []).find(item => item.id === selectedProfileId) || (state.boot?.profiles || [])[0]
    const gate = canAcceptQuest(profile)
    if (!quest.task || !gate.ok) {
      // Причина называется по существу. Пустая задача — это пустая задача, а не
      // «завершите разведку»: человек искал бы несуществующую проблему.
      transientError = !quest.task
        ? EMPTY_TASK_REASON
        : (gate.reasons[0] || 'Сначала завершите разведку и готовность персонажа')
      render()
      return
    }
    // Форма отправляется и щелчком, и по Enter, а между отправкой и ответом
    // ядра проходит время: задача и отпечаток разведки всё ещё на месте, и
    // вторая отправка запускала второй прогон той же задачи. Это два агента в
    // одних файлах и двойной расход — в отличие от кнопок с собственным id,
    // здесь повтор порождает новую сущность, а не повторяет старую.
    if (runStarting) return
    runStarting = true
    vscode.postMessage({ type: 'startRun', profileId: selectedProfileId, ...quest, apiKey, contextItems, preflightFingerprint: agentRunPreview?.fingerprint || '' })
  }
  if (event.target.id === 'settings-form') {
    const profile=currentFormProfile()
    if (!profile) return
    const submitter=event.submitter
    hireAfterSave=submitter?.dataset?.hireIntent || 'card'
    createStepError=''
    const issue=stepValidationIssue(profileEditorStep === 'class' ? 'identity' : profileEditorStep, profile)
    if(issue && !profile.id){createStepError=issue;profileDraft=profile;render();return}
    profileDraft=profile
    if (hubModeAvailable()) vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(newConstructorDraft(profile)) })
    else vscode.postMessage({type:'saveProfile',profile})
  }
  if (event.target.id === 'constructor-form') {
    event.preventDefault()
    const draft = currentConstructorForm()
    if (!draft) return
    constructorDraft = draft
    if (constructorStep !== 'review') return
    if (!(draft.name || '').trim()) { createStepError = 'Укажите имя агента'; render(); return }
    const dropped = legacySaveWouldDrop(draft)
    if (dropped) { createStepError = dropped; render(); return }
    // Кнопка «Сохранить» гасит прежнюю ошибку, а Enter — нет: имя исправили,
    // сохранение ушло, а под шагами так и висело «Укажите имя агента».
    createStepError = ''
    if (hubModeAvailable()) {
      vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(draft) })
    } else {
      vscode.postMessage({ type: 'saveProfile', profile: constructorToProfile(draft) })
    }
  }
  if (event.target.id === 'custom-tool-form') {
    const tool=currentCustomToolForm()
    const issue=customToolFormIssue(tool)
    if(issue){transientError=issue;render();return}
    const equip=!tool?.id
    toolEquipAfterSave=equip
    transientError=''
    if(tool)vscode.postMessage({type:'saveCustomTool',tool,equip})
  }
  if (event.target.id === 'workflow-form') {
    const workflow=currentWorkflowForm()
    if(workflow)vscode.postMessage({type:'saveWorkflow',workflow})
  }
  if (event.target.id === 'workflow-run-form') {
    const task=root.querySelector('#workflow-task')?.value.trim()
    const apiKeys={}
    for(const input of root.querySelectorAll('.workflow-api-key'))if(input.value)apiKeys[input.dataset.profileId]=input.value
    if(task&&selectedWorkflowId)vscode.postMessage({type:'startWorkflow',workflowId:selectedWorkflowId,task,apiKeys,contextItems})
  }
  if (event.target.id === 'companion-setup-form') {
    companionSetupDraft = currentCompanionSetupDraft()
    const issue = companionSetupValidation('brain', companionSetupDraft) || companionSetupValidation('boundaries', companionSetupDraft) || companionSetupValidation('skills', companionSetupDraft)
    if (issue) { companionSetupStatus = issue; render(); return }
    companionSetupPendingClose = true
    companionSetupStatus = ''
    vscode.postMessage({ type: 'saveCompanionConfig', config: companionConfigFromDraft(companionSetupDraft) })
  }
  if (event.target.id === 'budget-form') {
    const toCents = selector => {
      const raw = root.querySelector(selector)?.value.trim() || ''
      if (!raw) return 0
      const dollars = Number(raw)
      if (!Number.isFinite(dollars) || dollars < 0) throw new Error('Бюджет должен быть неотрицательным числом')
      return Math.round(dollars * 100)
    }
    try {
      statisticsStatus = 'loading'
      transientError = ''
      vscode.postMessage({
        type: 'saveBudget',
        budget: {
          dailyCents: toCents('#budget-daily'),
          monthlyCents: toCents('#budget-monthly'),
          hardStop: Boolean(root.querySelector('#budget-hard-stop')?.checked),
        },
      })
      render()
    } catch (error) {
      transientError = error instanceof Error ? error.message : String(error)
      render()
    }
  }
  if (event.target.id === 'companion-form') {
    const message = root.querySelector('#companion-input')?.value.trim()
    sendCompanionUserMessage(message)
  }
  if (event.target.id === 'team-form') {
    const name = root.querySelector('#team-name')?.value.trim()
    const description = root.querySelector('#team-description')?.value.trim() || ''
    const agentIds = [...root.querySelectorAll('input[name="team-agent"]:checked')].map(item => item.value)
    if (!name) return
    vscode.postMessage({ type: 'saveTeam', team: { name, description, agentIds } })
  }
  if (event.target.id === 'skill-form') {
    const existing = skillEditId ? (state.boot?.skills || []).find(item => item.id === skillEditId) : null
    const name = root.querySelector('#skill-name')?.value.trim() || ''
    const description = root.querySelector('#skill-description')?.value.trim() || ''
    const instructions = root.querySelector('#skill-instructions')?.value.trim() || ''
    const requiredTools = [...root.querySelectorAll('input[name="skill-tool"]:checked')].map(item => item.value)
	const configuration = { ...(existing?.configuration || {}) }
	if (root.querySelector('#skill-deprecated')?.checked) configuration.lifecycleStatus = 'deprecated'
	else delete configuration.lifecycleStatus
    skillEquipAfterSave = Boolean(root.querySelector('#skill-equip-after-save')?.checked)
    skillDraft = {
      ...(existing || {}),
      id: existing?.id || '',
      name,
      description,
      instructions,
      requiredTools,
      permissionDelta: existing?.permissionDelta || {},
	  configuration,
      references: existing?.references || [],
      scripts: existing?.scripts || [],
    }
    if (!name) { skillFormError = 'Укажите название Skill'; render(); return }
    if (name.length > 120) { skillFormError = 'Название не длиннее 120 символов'; render(); return }
    if (!instructions) { skillFormError = 'Инструкции обязательны — опишите практику и проверки'; render(); return }
    if (!requiredTools.length) { skillFormError = 'Выберите хотя бы один требуемый tool'; render(); return }
    skillFormError = ''
    const alreadyEquipped = skillDraft.id && (state.boot?.projectSkills || []).some(item => item.skillId === skillDraft.id && item.enabled)
    vscode.postMessage({
      type: 'saveSkill',
      skill: skillDraft,
      equipAfterSave: skillEquipAfterSave && !alreadyEquipped,
    })
  }
  if (event.target.id === 'connection-form') { saveConnectionFromFields() }
  if (event.target.id === 'server-form') {
    const existing = serverEditingId
      ? (state.boot?.serverProfiles || []).find(item => item.id === serverEditingId)
      : null
    const displayName = root.querySelector('#server-name')?.value.trim() || ''
    const host = root.querySelector('#server-host')?.value.trim() || ''
    const port = Number(root.querySelector('#server-port')?.value || 22)
    const user = root.querySelector('#server-user')?.value.trim() || ''
    const authMethod = root.querySelector('#server-auth')?.value || 'agent'
    const privateKeyPath = root.querySelector('#server-key')?.value.trim() || ''
    const defaultRemotePath = root.querySelector('#server-remote-path')?.value.trim() || '~'
    const password = root.querySelector('#server-password')?.value || ''
    if (!host || !user) { transientError = 'Укажите хост и пользователя SSH.'; render(); return }
    if (authMethod === 'key' && !privateKeyPath) { transientError = 'Для входа по ключу укажите абсолютный путь к ключу.'; render(); return }
    vscode.postMessage({
      type: 'saveServerProfile',
      id: existing?.id || '',
      secretRef: existing?.secretRef || '',
      displayName,
      host,
      port,
      user,
      authMethod,
      privateKeyPath,
      defaultRemotePath,
      password,
    })
  }
  if (event.target.id === 'db-connection-form') {
    const existing = dbEditingId
      ? (state.boot?.dbConnections || []).find(item => item.id === dbEditingId)
      : null
    const displayName = root.querySelector('#db-name')?.value.trim() || ''
    const driver = root.querySelector('#db-driver')?.value || 'sqlite'
    const host = root.querySelector('#db-host')?.value.trim() || ''
    const port = Number(root.querySelector('#db-port')?.value || 0)
    const database = root.querySelector('#db-database')?.value.trim() || ''
    const username = root.querySelector('#db-user')?.value.trim() || ''
    const sslMode = root.querySelector('#db-ssl')?.value.trim() || ''
    const password = root.querySelector('#db-password')?.value || ''
    if (!database) { transientError = 'Укажите имя БД или путь к файлу SQLite.'; render(); return }
    vscode.postMessage({
      type: 'saveDBConnection',
      id: existing?.id || '',
      secretRef: existing?.secretRef || '',
      displayName,
      driver,
      host,
      port,
      database,
      username,
      sslMode,
      password,
      readOnlyDefault: existing?.readOnlyDefault ?? true,
    })
  }
  if (event.target.id === 'db-query-form') {
    const sql = root.querySelector('#db-sql')?.value || ''
    const connectionId = dbSelectedId || (state.boot?.dbConnections || [])[0]?.id || ''
    if (!connectionId) { transientError = 'Сначала сохраните подключение к БД.'; render(); return }
    if (!sql.trim()) { transientError = 'Введите SQL.'; render(); return }
    dbQueryStatus = 'loading'
    dbQueryResult = undefined
    dbWritePending = null
    render()
    vscode.postMessage({ type: 'queryDBConnection', connectionId, sql, allowWrite: false, approved: false })
  }
  if (event.target.id === 'flow-form') {
    const flow = captureFlowForm()
    vscode.postMessage({ type: 'saveFlow', flow })
  }
  if (event.target.id === 'experience-search-form') {
    experienceSearchQuery = root.querySelector('#experience-search-query')?.value.trim() || ''
    if (experienceSearchQuery.length < 2) { transientError = 'Введите минимум 2 символа для поиска по опыту'; render(); return }
    experienceSearchStatus = 'loading'
    vscode.postMessage({ type: 'searchExperience', query: experienceSearchQuery })
    render()
  }
  if (event.target.id === 'manual-learning-form') {
    manualLearningDraft = {
      projectAgentId: root.querySelector('#manual-learning-agent')?.value || '',
      kind: root.querySelector('#manual-learning-kind')?.value || 'memory',
      scope: root.querySelector('#manual-learning-scope')?.value || 'project',
      content: root.querySelector('#manual-learning-content')?.value.trim() || '',
    }
    if (!manualLearningDraft.content) { transientError = 'Сформулируйте урок перед preview'; render(); return }
    manualLearningStatus = 'loading'
    manualLearningPreview = undefined
    vscode.postMessage({ type: 'previewManualLearning', request: manualLearningDraft })
    render()
  }
  if (event.target.id === 'memory-form') {
    const existing = memoryEditId ? (state.boot?.memories || []).find(item => item.id === memoryEditId) : null
    const kind = root.querySelector('#memory-kind')?.value || 'project'
    let ownerId = root.querySelector('#memory-owner')?.value || ''
    if (kind === 'project' || kind === 'companion') ownerId = ''
    if (kind === 'profile' && !(state.boot?.blueprints || []).some(item => item.id === ownerId)) { transientError = 'Для переносимой памяти выберите основной профиль'; render(); return }
    if (kind === 'agent' && !(state.boot?.projectAgents || []).some(item => item.id === ownerId)) { transientError = 'Для Agent Memory выберите агента-владельца'; render(); return }
    if (kind === 'quest' && !(state.boot?.quests || []).some(item => item.id === ownerId)) { transientError = 'Для Quest Memory выберите квест-владельца'; render(); return }
    const memory = {
      ...(existing || {}),
      id: existing?.id || '',
      kind,
      content: root.querySelector('#memory-content')?.value.trim() || '',
      source: root.querySelector('#memory-source')?.value.trim() || '',
      ownerId,
      confidence: Number(root.querySelector('#memory-confidence')?.value ?? existing?.confidence ?? 0.5),
      pinned: Boolean(root.querySelector('#memory-pinned')?.checked),
    }
    if (!memory.content) { transientError = 'Содержание памяти не может быть пустым'; render(); return }
    vscode.postMessage({ type: 'saveMemory', memory })
    memoryEditId = ''
    memoryDraft = undefined
  }
  const execForm = event.target.closest?.('[data-exec-form]')
  if (execForm) {
    const runId = execForm.dataset.runId
    const input = execForm.querySelector('input')
    const value = input?.value.trim()
    if (!runId || !value) return
    if (execForm.dataset.execForm === 'message') {
      const learningIntent = execForm.querySelector('input[name="learn-from-message"]')?.checked ? 'correction' : ''
      vscode.postMessage({ type: 'messageRun', runId, message: value, learningIntent })
    } else if (execForm.dataset.execForm === 'forbid') {
      vscode.postMessage({ type: 'forbidFile', runId, path: value })
    }
    if (input) input.value = ''
  }
}


root.addEventListener('keydown', event => {
  if(state.selectedTab==='master' && (event.ctrlKey||event.metaKey)){
    if(event.key.toLowerCase()==='l'){event.preventDefault();root.querySelector('#master-input')?.focus();return}
    if(event.key.toLowerCase()==='f'){event.preventDefault();masterFindOpen=true;render();requestAnimationFrame(()=>root.querySelector('#master-find')?.focus());return}
    if(event.shiftKey && event.key.toLowerCase()==='n'){event.preventDefault();vscode.postMessage({type:'masterSession',action:'new'});return}
  }
  if(state.selectedTab==='master'&&event.key==='Escape'&&masterClient.historyOpen){event.preventDefault();masterClient.historyOpen=false;render();return}
  // Escape закрывает панель задания — но не тогда, когда набирают в её полях:
  // в открытой панели правят бриф, и закрытие потеряло бы набранный критерий.
  if (state.selectedTab === 'master' && event.key === 'Escape' && !event.target.closest?.('input, textarea, select')
    && closeMasterBriefPanel(root, persistDraft)) { event.preventDefault(); return }
  // Коммит с клавиатуры: Ctrl+Enter из поля сообщения — привычка из любой IDE,
  // и без неё приходится тянуться мышью через всю панель.
  if (event.target?.id === 'git-commit-message' && event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
    event.preventDefault()
    submitGitCommit(event.shiftKey ? 'commitAndPush' : 'commit')
    return
  }
  const hotkey = decisionHotkey(event)
  if (hotkey) {
    const items = decisionsData?.items || []
    if (!items.length) return
    const at = Math.max(0, items.findIndex(item => item.id === decisionPick))
    if (hotkey === 'next' || hotkey === 'prev') {
      const step = hotkey === 'next' ? 1 : -1
      decisionPick = items[(at + step + items.length) % items.length].id
      event.preventDefault()
      render()
      return
    }
    // Автоповтор перебирает список — это удобно; но решать он не должен:
    // человек нажал один раз, а клавиатура повторила за него.
    if (event.repeat) return
    const item = items[at]
    const intent = decisionIntents(item)[hotkey === 'accept' ? 'accept' : 'reject']
    if (sendDecisionResolve(item?.id, intent)) event.preventDefault()
    return
  }
  // Перебор списков и полос вкладок стрелками. Стоит после очереди решений
  // намеренно: на своём экране очередь обрабатывает стрелки сама и меняет
  // выбор, а не только фокус.
  if (handleListKeydown(event)) return
  if (event.key === 'Escape') {
    if (transientError) {
      transientError = ''
      render()
      return
    }
    if (companionLoading || companionPendingSend) {
      event.preventDefault()
      stopCompanionChat()
      return
    }
    if (companionSetupOpen && !event.target.closest?.('input, textarea, select')) {
      companionSetupOpen = false
      persistDraft()
      render()
      return
    }
    if (isCompanionPopup()) {
      event.preventDefault()
      vscode.postMessage({ type: 'closeCompanionPopup' })
      return
    }
  }
  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && event.target.id === 'task') root.querySelector('#agent-form')?.requestSubmit()
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing && event.target.id === 'companion-input') {
    event.preventDefault()
    sendCompanionUserMessage(event.target.value)
  }
  // Два чата в одном приложении не должны отправляться по-разному: у компаньона
  // Enter отправляет, а у Мастера единственным способом была мышь.
  // Shift+Enter по-прежнему переносит строку — задачу описывают и в несколько.
  // Список упоминаний забирает стрелки, Enter, Tab и Escape себе, пока открыт:
  // иначе Enter отправит реплику вместо того, чтобы приложить выбранный файл.
  if (event.target.id === 'master-input' && masterMentionOpen()
    && handleMasterMentionKey(event.key, { pick: pickMasterMention, render })) {
    event.preventDefault()
    return
  }
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing && event.target.id === 'master-input') {
    event.preventDefault()
    sendMasterMessage()
  }
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing && event.target.classList?.contains('hall-question-extra')) {
    event.preventDefault()
    event.target.closest('.hall-questions')?.querySelector('[data-action="master-answer-question"]')?.click()
  }
})
root.addEventListener('scroll', event => {
  if (event.target?.id === 'companion-thread') {
    companionAutoFollow = threadNearBottom(event.target)
    updateCompanionScrollCue()
  }
  // Ушёл читать выше — значит, следующий ответ Мастера не должен дёргать ленту.
  if (event.target?.id === 'master-thread') {
    masterAutoFollow = threadNearBottom(event.target)
    masterClient.scroll[masterClient.active]=event.target.scrollTop
    clearTimeout(masterClient.scrollTimer)
    masterClient.scrollTimer=setTimeout(persistDraft,200)
    updateMasterScrollCue()
  }
}, true)
let draggedWorkflowStep = -1
// Перетаскивание файла между папками изменений. Целями служат только папки
// самого человека: конфликты и файлы вне репозитория никуда не переносятся.
function gitDropTarget(node) {
  const group = node?.closest?.('.nc-group.tone-change')
  return group || undefined
}
function markGitDrop(group) {
  for (const node of root.querySelectorAll('.nc-group.is-drop')) {
    if (node !== group) node.classList.remove('is-drop')
  }
  if (group) group.classList.add('is-drop')
}
function endGitDrag() {
  gitDragPath = ''
  markGitDrop(undefined)
  for (const node of root.querySelectorAll('.nc-file.is-dragging')) node.classList.remove('is-dragging')
}
root.addEventListener('dragstart', event => {
  const file = event.target.closest?.('.nc-file[draggable="true"]')
  if (file) {
    gitDragPath = String(file.dataset.path || '')
    event.dataTransfer.effectAllowed = 'move'
    try { event.dataTransfer.setData('text/plain', gitDragPath) } catch { /* не все среды дают буфер */ }
    file.classList.add('is-dragging')
    return
  }
  const card = event.target.closest?.('.workflow-step')
  if (!card) return
  draggedWorkflowStep = Number(card.dataset.index)
  event.dataTransfer.effectAllowed = 'move'
})
root.addEventListener('dragover', event => {
  if (gitDragPath) {
    const group = gitDropTarget(event.target)
    markGitDrop(group)
    if (group) {
      event.preventDefault()
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
    }
    return
  }
  if (event.target.closest?.('.workflow-step')) event.preventDefault()
})
root.addEventListener('dragend', () => { if (gitDragPath) endGitDrag() })
root.addEventListener('drop', event => {
  if (gitDragPath) {
    const group = gitDropTarget(event.target)
    const list = String(group?.dataset.list || '')
    const path = gitDragPath
    const from = gitChanges().find(item => String(item.path) === path)
    endGitDrag()
    if (!group) return
    event.preventDefault()
    if (!list || !from || gitGroupOf(from) === list) return
    gitPendingAction = `moveToList:${path}`
    gitNotice = undefined
    render()
    vscode.postMessage({ type: 'gitAction', action: 'moveToList', path, list, paths: [], repoRoot: toolWindowData.git?.root || '' })
    return
  }
  const card = event.target.closest?.('.workflow-step')
  const targetIndex = Number(card?.dataset.index)
  if (!card || draggedWorkflowStep < 0 || targetIndex === draggedWorkflowStep) return
  event.preventDefault()
  const workflow = currentWorkflowForm()
  if (workflow) {
    const [step] = workflow.steps.splice(draggedWorkflowStep, 1)
    workflow.steps.splice(targetIndex, 0, step)
    workflowDraft = workflow
    render()
  }
  draggedWorkflowStep = -1
})

// Какой раздел ждёт ответа на какой запрос. Расширение сообщает об отказе одним
// сообщением на все случаи, а знать, что именно замерло, может только здесь.
// Незнакомого запроса бояться не нужно: он гасит всё ждущее, а не ничего.
const FAILED_REQUEST_SECTIONS = {
  loadDecisions: 'decisions',
  resolveDecision: 'decisions',
  loadStatistics: 'statistics',
  createSystemBackup: 'statistics',
  restoreSystemBackup: 'statistics',
  loadFileHistory: 'fileHistory',
  // Обе половины разговора, а не одна: без loadMaster неудачная загрузка
  // переписки считалась безымянной и метила ошибкой все ждущие разделы разом —
  // статистику, Docker, историю файлов, — хотя падал только Мастер.
  startFastAgent: 'master',
  loadMaster: 'master',
  loadChatDirectory: 'chatDirectory',
  masterChat: 'master',
  queryDBConnection: 'dbQuery',
  searchExperience: 'experienceSearch',
  previewManualLearning: 'manualLearning',
  applyManualLearning: 'manualLearning',
}

window.addEventListener('message', event => {
  const message=event.data
  if (message.type === 'collectGarbage') {
    setTimeout(() => globalThis.gc?.(), 0)
    return
  }
  if (message.type === 'chatDirectory') {
    receiveChatDirectory(message)
    render()
    return
  }
  if (message.type === 'projects') {
    receiveProjects(message)
    render()
    return
  }
  if (message.type === 'projectGallery') {
    setProjectGalleryOpen(message.open !== false)
    render()
    return
  }
  if (message.type === 'projectSwitch') {
    // Скелет живёт до первого состояния нового мира. Своё окно ожидания здесь
    // ограничено: если ядро так и не поднялось, человек должен увидеть экран с
    // кнопками «Повторить запуск» и «Хроника ядра», а не вечный шиммер.
    clearTimeout(projectSwitchTimer)
    if (message.phase === 'start') {
      setProjectGalleryOpen(false)
      setProjectSwitchInfo({ path: message.path || '', name: message.name || '' })
      projectSwitchTimer = setTimeout(() => { setProjectSwitchInfo(undefined); render() }, 12000)
    } else {
      setProjectSwitchInfo(undefined)
    }
    render()
    return
  }
  if (message.type === 'gitActionResult') {
    gitPendingAction = ''
    submittingForm = ''
    if (message.snapshot && typeof message.snapshot === 'object') {
      toolWindowData = { ...toolWindowData, git: { ...message.snapshot, loaded: true } }
      syncGitChecked(gitChanges())
    }
    gitNotice = { tone: message.ok ? 'ok' : 'error', text: String(message.message || (message.ok ? 'Готово' : 'Git не выполнил действие')) }
    clearTimeout(gitNoticeTimer)
    if (message.ok) gitNoticeTimer = setTimeout(() => { gitNotice = undefined; render() }, 4200)
    if (message.ok && (message.action === 'commit' || message.action === 'commitAndPush')) {
      gitCommitDraft = ''
      gitAmend = false
      persistDraft()
    }
    render()
    return
  }
  // Ctrl+K из редактора: панель уже открыта, осталось поставить каретку туда,
  // где человек и собирался писать.
  if (message.type === 'gitFocusMessage') {
    const field = root.querySelector('#git-commit-message')
    if (field) field.focus()
    else setTimeout(() => root.querySelector('#git-commit-message')?.focus(), 120)
    return
  }
  if (message.type === 'toolWindowState') {
    const snapshot = message.snapshot && typeof message.snapshot === 'object' ? message.snapshot : {}
    const kind = String(snapshot.kind || toolWindowKind())
    toolWindowData = { ...toolWindowData, [kind]: { ...snapshot, loaded: true } }
    if (kind === 'git') syncGitChecked(gitChanges())
    render()
    return
  }
  if (message.type === 'state') {
    // Ответ пришёл — форму отпираем. Иначе после первой же отправки она
    // осталась бы запертой до перезагрузки панели.
    submittingForm = ''
    if (String(message.workspacePath || '') !== projectKey) {
      projectKey = String(message.workspacePath || '')
      resetProjectScopedState()
    }
    state={...message, selectedTab: canonicalTab(message.selectedTab)}
    const discussed = (state.boot?.questProposals || []).find(p => p.id === masterDiscussionProposalId)
    if (discussed && discussed.status !== 'pending') {
      masterDiscussionProposalId = ''
      persistDraft()
    }
    // «Мастер не настроен» — ответ, который устаревает молча.
    //
    // Настраивают его в другом разделе, и раздел разговора об этом не узнавал:
    // он держал прежний ответ ядра и продолжал показывать приглашение к
    // настройке — то самое, из которого человек только что вернулся, всё
    // сделав. Выход был один: переоткрыть панель. Мир уже сообщил, что
    // диспетчер есть, — значит наш ответ неверен, и его надо спросить заново.
    if (masterData?.configured === false && state.boot?.orchestrator?.id) {
      masterData = undefined
      masterStatus = 'idle'
    }
    // Очередь загружалась один раз и больше не обновлялась. Правило сверки
    // живёт рядом со счётчиком ожидающих: оно ловит сдвиг мира в обе стороны.
    if (decisionsStatus === 'ready' && decisionsQueueIsStale()) decisionsStatus = 'idle'
    // Запуск предложения дошёл до ядра — кнопку отпускаем. Судим по самому
    // предложению, а не по факту прихода состояния: состояние приходит и по
    // чужим поводам, и отпущенная на них кнопка снова стала бы двойной.
    for (const id of proposalStarting) {
      const awaited = (state.boot?.questProposals || []).find(item => item.id === id)
      if (!awaited || awaited.status === 'started') proposalStarting.delete(id)
    }
    for (const id of companionActionApplying) {
      const awaited = (state.boot?.companionActionProposals || []).find(item => item.id === id)
      if (!awaited || awaited.status === 'applied' || awaited.status === 'ignored') companionActionApplying.delete(id)
    }
    // Карточка исполнителя ждала ответа ядра: состояние пришло, и держать её
    // кнопку запертой больше не на чем.
    releaseMasterAgentCards()
    for (const id of proposalEditDrafts.keys()) {
      const awaited = (state.boot?.questProposals || []).find(item => item.id === id)
      if (!awaited || awaited.status === 'started' || awaited.status === 'ignored') {
        proposalEditDrafts.delete(id)
        proposalModifying.delete(id)
        if (proposalEditId === id) proposalEditId = ''
      }
    }
    for (const id of companionActionEditDrafts.keys()) {
      const awaited = (state.boot?.companionActionProposals || []).find(item => item.id === id)
      if (!awaited || awaited.status === 'applied' || awaited.status === 'ignored') {
        companionActionEditDrafts.delete(id)
        companionActionModifying.delete(id)
        if (companionActionEditId === id) companionActionEditId = ''
      }
    }
    if (message.ideContext && typeof message.ideContext === 'object') companionIdeContext = message.ideContext
    if (Array.isArray(message.companionFeedback)) {
      companionFeedbackMarks = new Map(message.companionFeedback
        .filter(item => item && item.messageId)
        .map(item => [String(item.messageId), item.value === 'down' ? 'down' : 'up']))
    }
    if (Array.isArray(state.boot?.companionMessages)) {
      const incoming = state.boot.companionMessages.map(item => ({ ...item, factsUsed: Array.isArray(item.factsUsed) ? item.factsUsed : [] })).slice(-80)
      companionMessages = mergeCompanionTranscript(companionMessages, incoming, companionLoading)
    }
    const defaultId = state.boot?.defaultProfileId
    // Project agents and legacy profiles share the composer. Checking only the
    // legacy profile array made every background boot refresh discard a valid
    // project-agent selection while a preview was being prepared.
    if (!selectedProfileId || !agentById(selectedProfileId)) {
      selectedProfileId = agentById(defaultId)?.id || hubAgents()[0]?.id || state.boot?.profiles?.[0]?.id || ''
    }
    const improvementFocus = message.agentImprovementFocus
    if (isWide && state.selectedTab === 'agents' && improvementFocus?.requestId && improvementFocus.requestId !== lastAgentImprovementFocusId) {
      const selected = agentById(improvementFocus.agentId)
      if (selected) {
        lastAgentImprovementFocusId = improvementFocus.requestId
        selectedProfileId = selected.id
        prepareAgentConstructor(selected, improvementFocus.constructorStep)
        vscode.postMessage({ type: 'agentImprovementFocused', requestId: improvementFocus.requestId })
      } else if (state.boot) {
        lastAgentImprovementFocusId = improvementFocus.requestId
        transientError = 'Агент из рекомендации больше не найден в ростере.'
        vscode.postMessage({ type: 'agentImprovementFocused', requestId: improvementFocus.requestId })
      }
    }
    persistDraft()
    render()
  }
  if (message.type === 'cursorRuntime') {
    state = { ...state, cursorRuntime: message }
    render()
  }
  if (message.type === 'cursorRunStarted') {
    cursorRunActive = true
    cursorRunEvents = []
    transientError = ''
    render()
  }
  if (message.type === 'cursorRunEvent') {
    cursorRunEvents = [...cursorRunEvents.slice(-99), message.event]
    if (message.event?.type === 'error') transientError = message.event.message || 'Cursor Agent завершился с ошибкой'
    render()
  }
  if (message.type === 'cursorRunFinished') {
    cursorRunActive = false
    if (message.status === 'error') transientError = message.error || 'Cursor Agent завершился с ошибкой'
    render()
  }
  if (message.type === 'runDelta') {
    state = { ...state, details: message.details, workflowDetails: message.workflowDetails }
    const chatReady = root.querySelector('.chat-main') || root.querySelector('[data-workflow-run-host]')
    if (state.selectedTab === 'master' && root.querySelector('#master-thread') && masterPinnedWork.size) {
      replaceMasterThreadHtml()
    } else if (chatReady && (state.selectedTab === 'quests' || state.selectedTab === 'quest' || state.selectedTab === 'flows' || state.selectedTab === 'workflows')) {
      schedulePaintRun()
    } else {
      render()
    }
  }
  if (message.type === 'companionInterventions') {
    state = {
      ...state,
      boot: {
        ...(state.boot || {}),
        companionInterventions: Array.isArray(message.interventions) ? message.interventions : [],
        companionDismissedCount: Number(message.dismissedCount || 0),
        executions: Array.isArray(message.executions) ? message.executions : (state.boot?.executions || []),
      },
    }
    if (companionLoading && root.querySelector('.companion-nudge-banner, .companion-chat-workspace, .companion-dock-brief')) {
      const nudge = companionNudgeBannerHtml()
      if (root.querySelector('.companion-nudge-banner')) {
        if (nudge) replaceHtmlNodes('.companion-nudge-banner', nudge)
        else root.querySelectorAll('.companion-nudge-banner').forEach(node => node.remove())
      } else if (nudge) {
        const ideNow = root.querySelector('.companion-ide-now')
        if (ideNow) ideNow.insertAdjacentHTML('afterend', nudge)
      }
      replaceHtmlNodes('.companion-dock-brief', companionSidebarBriefHtml())
    } else {
      render()
    }
  }
  if (message.type === 'companionThreadSync') {
    if (Array.isArray(message.messages) && message.messages.length) {
      companionMessages = message.messages.slice(-80)
    }
    if (typeof message.draft === 'string') companionDraft = message.draft
    if (typeof message.streamReply === 'string') companionStreamReply = message.streamReply
    if (typeof message.pendingSend === 'string') companionPendingSend = message.pendingSend
    companionLoading = Boolean(message.loading)
    companionActiveRequestId = companionLoading
      ? (Number(message.requestId || 0) || companionActiveRequestId || ++companionRequestId)
      : 0
    if (companionLoading && companionStreamReply) companionThinkPhase = 'model'
    if (!companionLoading) {
      companionThinkPhase = ''
      companionActivitySteps = []
    }
    persistDraft()
    render()
    requestAnimationFrame(() => {
      scrollCompanionThread()
      if (!companionLoading && companionPendingSend.trim()) flushCompanionPendingSend()
    })
    focusCompanionInput()
  }
  if (applyHubEntityMessage(message)) return
  if (message.type === 'companionConfigSaved') {
    transientError = ''
    companionSetupStatus = 'Настройки компаньона сохранены.'
    if (companionSetupPendingClose) {
      companionSetupPendingClose = false
      companionSetupOpen = false
    }
    persistDraft()
    render()
  }
  if (message.type === 'companionActionApplied') {
    companionActionEditId = ''
    if (message.proposalId) {
      companionActionApplying.delete(message.proposalId)
      companionActionModifying.delete(message.proposalId)
      companionActionEditDrafts.delete(message.proposalId)
    }
    if (message.flowId) {
      selectedFlowId = message.flowId
      flowDraft = undefined
    }
    if (message.stayInCompanion || isCompanionView()) {
      const status = message.flowId ? 'Flow создан'
        : message.agentId ? 'Агент создан'
        : message.teamId ? 'Отряд создан'
        : message.skillId ? 'Skill создан'
        : 'Действие выполнено'
      companionAppliedNotice = { status, tab: message.guildTab || 'overview' }
    }
    persistDraft()
    render()
  }
  if (message.type === 'companionActionModified') {
    companionActionModifying.delete(message.proposalId || '')
    companionActionEditDrafts.delete(message.proposalId || '')
    if (companionActionEditId === message.proposalId) companionActionEditId = ''
  }
  if (message.type === 'questProposalModified') {
    proposalModifying.delete(message.proposalId || '')
    proposalEditDrafts.delete(message.proposalId || '')
    if (proposalEditId === message.proposalId) proposalEditId = ''
    render()
  }
  if (message.type === 'questProposalStarted') {
    const proposalId = String(message.proposalId || '')
    const questId = String(message.questId || '')
    const runId = String(message.runId || '')
    if (proposalId) {
      masterPinnedWork.set(proposalId, { proposalId, questId, runId })
      proposalStarting.delete(proposalId)
    }
    if (message.stayInMaster) {
      state = { ...state, selectedTab: 'master' }
      masterAutoFollow = true
    }
    if (state.selectedTab === 'master' && root.querySelector('#master-thread')) {
      replaceMasterThreadHtml()
      syncMasterComposeState()
    } else {
      render()
    }
  }
  if (message.type === 'plannerFallbackNotice') {
    plannerFallbackNotice = {
      questId: String(message.questId || ''),
      message: String(message.message || ''),
      mode: String(message.mode || 'model-fallback'),
    }
    render()
  }
  if (message.type === 'customToolSaved' || message.type === 'customToolDeleted') {
    resetCustomToolPreview()
    customToolDraft=undefined
    selectedCustomToolId=message.toolId||''
    const shouldEquip = message.type === 'customToolSaved' && (message.equip || toolEquipAfterSave) && message.toolId
    toolEquipAfterSave = false
    if (shouldEquip) {
      const selected = agentById(selectedProfileId) || hubAgents()[0]
      constructorDraft = newConstructorDraft(selected ? {
        ...selected,
        primaryModel: selected.primaryModel || selected.model,
        model: selected.primaryModel || selected.model,
        allowedTools: [...new Set([...(selected.allowedTools || []), message.toolId])],
      } : {
        allowedTools: [...new Set(['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff', message.toolId])],
      })
      constructorStep = 'tools'
      agentConstructorOpen = true
      profileEditorOpen = false
      persistDraft()
    }
  }
  if (message.type === 'workflowSaved' || message.type === 'workflowDeleted') { workflowDraft=undefined; selectedWorkflowId=message.workflowId||'' }
  if (message.type === 'providerProbeResult') {
    providerProbe=message.result
    if(profileDraft && !profileDraft.model && providerProbe?.models?.[0])profileDraft.model=providerProbe.models[0].id
    if (providerProbe?.connected && profileDraft?.model) requestModelCapabilityProbe(profileDraft)
    else render()
  }
  if (message.type === 'modelCapabilityProbeResult') {
    modelCapabilityProbe = message.result || { error: 'Capability probe не вернул результат.' }
    render()
  }
  if (message.type === 'companionProviderProbeResult') {
    companionProviderProbe = message.result || { connected: false, error: 'Проверка подключения не вернула результат.' }
    if (companionInterventionProbe?.loading && (!message.connectionId || message.connectionId === companionInterventionProbe.connectionId)) {
      companionInterventionProbe = {
        ...companionInterventionProbe,
        ...companionProviderProbe,
        loading: false,
      }
    }
    if (companionProviderProbe.connected && companionProviderProbe.models?.[0]?.id) {
      if (onboardingCompanionActive()) {
        const draft = onboardingCompanionDraft()
        if (!draft.model) writeOnboardingCompanionDraft({ ...draft, model: companionProviderProbe.models[0].id })
      } else if (onboardingOrchestratorActive()) {
        const draft = onboardingOrchestratorDraft()
        if (!draft.model) writeOnboardingOrchestratorDraft({ ...draft, model: companionProviderProbe.models[0].id })
      } else if (companionSetupDraft && !companionSetupDraft.model) {
        companionSetupDraft.model = companionProviderProbe.models[0].id
      }
    }
    companionSetupStatus = companionProviderProbe.connected
      ? 'Подключение проверено. Теперь выберите модель.'
      : (companionProviderProbe.phase === 'save' ? 'Не удалось сохранить подключение.' : '')
    persistDraft()
    refreshCompanionSetup({ quiet: true })
  }
  if (message.type === 'contextAdded') {
    const incoming=Array.isArray(message.items)?message.items:[]
    for(const item of incoming){
      const duplicate=contextItems.some(current=>current.kind===item.kind&&current.path===item.path&&current.label===item.label&&current.content===item.content)
      if(!duplicate&&contextItems.length<16)contextItems.push(item)
    }
    persistDraft()
    requestContextPreview()
  }
  if (message.type === 'contextPreview') { contextPreview=message.preview; contextPreviewStatus='ready'; contextPreviewError=''; render() }
  if (message.type === 'contextPreviewError') { contextPreview=undefined; contextPreviewStatus='error'; contextPreviewError=message.message||'Не удалось проверить вложения'; render() }
  if (message.type === 'agentRunPreview') { agentRunPreview=message.preview;agentRunPreviewStatus='ready';agentRunPreviewError='';render() }
  if (message.type === 'agentRunPreviewError') { agentRunPreview=undefined;agentRunPreviewStatus='error';agentRunPreviewError=message.message||'Не удалось проверить запуск';render() }
  if (message.type === 'compiledPromptPreview') { compiledPromptPreview=message.preview;compiledPromptStatus='ready';compiledPromptError='';render() }
  if (message.type === 'compiledPromptPreviewError') { compiledPromptPreview=undefined;compiledPromptStatus='error';compiledPromptError=message.message||'Не удалось собрать runtime-промпт';render() }
  if (message.type === 'customToolPreview') { customToolPreview=message.preview;customToolPreviewStatus='ready';customToolPreviewError='';render() }
  if (message.type === 'customToolPreviewError') { customToolPreview=undefined;customToolPreviewStatus='error';customToolPreviewError=message.message||'Не удалось проверить инструмент';render() }
  if (message.type === 'runUndoResult') {
    const result = message.result || {}
    const reverted = Array.isArray(result.reverted) ? result.reverted.length : 0
    const skipped = Array.isArray(result.skipped) ? result.skipped : []
    if (reverted) {
      transientError = skipped.length
        ? `Откатили ${countOf(reverted, 'правка', 'правки', 'правок')}. Не всё: ${skipped.join('; ')}`
        : `Откатили ${countOf(reverted, 'правка', 'правки', 'правок')}.`
      if (!skipped.length && result.runId && keptRunId === result.runId) keptRunId = ''
    } else if (skipped.length) {
      transientError = `Не удалось откатить: ${skipped.join('; ')}`
    } else if (message.message) {
      transientError = message.message
    }
    persistDraft()
    render()
  }
  if (message.type === 'runStarted') {
    runStarting = false
    masterSending = false
    stopMasterWaitClock()
    taskDraft='';questGoalDraft='';questCriteriaDraft='';questConstraintsDraft='';invalidateAgentRunPreview();contextItems=[]; contextPreview=undefined; contextPreviewStatus='idle'; contextPreviewError=''; persistDraft()
    if (message.fastAgent) {
      keptRunId = ''
      masterDraft = ''
      persistDraft()
      if (masterClient?.acceptTurn) {
        const runId = state.details?.run?.id || ''
        masterClient.acceptTurn({
          id: 'fast_' + Date.now().toString(36),
          conversationId: masterClient.active,
          status: 'ready',
          reply: runId ? `Агент запущен (run ${runId}). Правки появятся ниже — Keep / Undo.` : 'Агент запущен.',
        })
      }
    }
  }
  if (message.type === 'workflowRunStarted') { contextItems=[]; contextPreview=undefined; contextPreviewStatus='idle'; contextPreviewError=''; persistDraft() }
  if (message.type === 'error') {
    providerProbe = undefined
    companionProviderProbe = undefined
    // Запуск не состоялся — форму отпираем, иначе повторить будет нельзя.
    runStarting = false
    // Какое из предложений не запустилось, отказ не называет — отпускаем все:
    // застрявшая навсегда кнопка хуже лишнего разблокированного нажатия,
    // которое ядро всё равно отвергнет.
    const failedRequest = String(message.request || '')
    if (!failedRequest || failedRequest === 'approveMasterWorkOrderV2' || failedRequest === 'reviseMasterWorkOrderV2' || failedRequest === 'controlMasterWorkOrderQuestV2' || failedRequest === 'controlMasterApplicationV2') masterWorkOrderBusy.clear()
    if (!failedRequest || failedRequest === '/api/quest-proposals/decide') {
      proposalStarting.clear()
      proposalModifying.clear()
    }
    if (!failedRequest || failedRequest === '/api/companion/actions/decide') {
      companionActionApplying.clear()
      companionActionModifying.clear()
    }
    // Отказ ядра обязан отпускать и карточку исполнителя: иначе её кнопка
    // остаётся запертой навсегда, а набранное человеком некуда отправить.
    releaseMasterAgentCards()
    submittingForm = ''
    // Годность персонажа и политика мастера ждут ответа в своих наборах, а
    // снимались оттуда только ответом. После отказа ключ оставался ждать
    // вечно: повтор блокировал сам себя, кэш пустовал, и оба экрана держали
    // заглушку загрузки. Хуже того, готовность при неполученном ответе
    // намеренно не отрицается — персонаж навсегда объявлялся готовым по
    // данным, которых никто не присылал.
    for (const key of agentCapabilityInflight) agentCapabilityFailed.add(key)
    agentCapabilityInflight.clear()
    for (const key of orchestratorPolicyInflight) orchestratorPolicyFailed.add(key)
    orchestratorPolicyInflight.clear()
    transientError = message.message
    if (contextInspectorStatus === 'loading') {
      contextInspectorStatus = 'error'
      contextInspector = { error: message.message }
    }
    // Раздел уходит в «загрузку» перед запросом, а выходит из неё только
    // приходом ответа. При отказе ответа не будет: полоса ошибки скажет
    // причину, но раздел так и останется в «загрузка…» до переоткрытия панели.
    // Спасали двоих из девяти — теперь всех. Состояние 'error' тупиковое
    // намеренно: места запроса смотрят на 'idle', и автоповтор превратил бы
    // постоянный отказ в бесконечный цикл запросов. Повторяет человек.
    //
    // Если ядро назвало упавший запрос и он знаком — гасим только его раздел,
    // чтобы не винить соседей. Незнакомый или неназванный гасит всё ждущее:
    // лишняя пометка сама сойдёт с приходом ответа, а вечная «загрузка» — нет.
    const only = FAILED_REQUEST_SECTIONS[String(message.request || '')] || ''
    const hit = name => !only || only === name
    if (hit('statistics') && statisticsStatus === 'loading') statisticsStatus = 'error'
    if (hit('docker') && dockerStatus === 'loading') dockerStatus = 'error'
    if (hit('fileHistory') && fileHistoryStatus === 'loading') fileHistoryStatus = 'error'
    if (hit('master') && masterStatus === 'loading') masterStatus = 'error'
    if (hit('chatDirectory') && chatDirectoryStatus === 'loading') chatDirectoryStatus = 'error'
    // Отправка Мастеру запирает поле и кнопку до ответа. Ответа не будет —
    // и без снятия замка разговор вставал намертво: «Думает…» висело вечно,
    // писать было нечем, а разморозить это могло только переоткрытие панели.
    // Реплика цела в masterSentText и возвращается в поле — отправить её снова,
    // а не набирать заново. Причину человек уже читает в полосе ошибки.
    //
    // Возврат делается только в пустое поле: ход больше не запирает композер, и
    // человек мог написать в него следующую мысль, пока ответ не пришёл. Затереть
    // её отказавшей репликой значило бы потерять обе.
    const restoreSent = () => {
      const sent = masterSentText()
      if (sent && !String(masterDraft || '').trim()) masterDraft = sent
      forgetMasterSent()
    }
    if (hit('master')) { masterSending = false; restoreSent(); stopMasterWaitClock(); runStarting = false }
    if (hit('agent')) { runStarting = false; masterSending = false; restoreSent() }
    if (hit('dbQuery') && dbQueryStatus === 'loading') dbQueryStatus = 'error'
    if (hit('experienceSearch') && experienceSearchStatus === 'loading') experienceSearchStatus = 'error'
    if (hit('manualLearning') && (manualLearningStatus === 'loading' || manualLearningStatus === 'applying')) manualLearningStatus = 'error'
    // Эти двое объясняются не статусом, а своей строкой ошибки: без неё раздел
    // вышел бы из спиннера и молча показал пустоту.
    if (hit('compiledPrompt') && compiledPromptStatus === 'loading') {
      compiledPromptStatus = 'error'
      compiledPromptError = String(message.message || '') || 'запрос не удался'
    }
    if (hit('contextPreview') && contextPreviewStatus === 'loading') {
      contextPreviewStatus = 'error'
      contextPreviewError = String(message.message || '') || 'запрос не удался'
    }
    if (hit('decisions') && decisionsStatus === 'loading') {
      decisionsStatus = 'error'
      decisionsError = String(message.message || '') || 'запрос не удался'
    }
    render()
  }
  if (applyCompanionChatMessage(message)) return
  if (message.type === 'skillEquipPreview') {
    pendingSkillEquip = {
      skillId: message.preview?.skill?.id || message.skillId,
      skill: message.preview?.skill,
      permissionDelta: message.preview?.permissionDelta || {},
      requiredTools: message.preview?.requiredTools || [],
    }
    render()
  }
  if (message.type === 'skillEquipped') {
    pendingSkillEquip = undefined
    skillPendingEquipId = ''
    render()
  }
  if (message.type === 'skillSaved') {
    skillEditId = ''
    skillDraft = undefined
    skillFormError = ''
    skillEquipAfterSave = true
    if (message.equipAfterSave && message.skillId) {
      skillPendingEquipId = message.skillId
      vscode.postMessage({ type: 'previewEquipSkill', skillId: message.skillId })
    }
  }
  if (message.type === 'contextInspector') {
    if (message.runId === contextInspectorRunId || !contextInspectorRunId) {
      contextInspectorRunId = message.runId || contextInspectorRunId
      contextInspector = message.inspector
      contextInspectorStatus = 'ready'
      render()
    }
  }
  if (message.type === 'runContextQueued') {
    const count = Number(message.preview?.pendingItems || message.preview?.items?.length || 0)
    contextInspectorNotice = count === 1 ? 'Контекст добавлен в очередь компаньона' : `${countOf(count, 'элемент', 'элемента', 'элементов')} добавлено в очередь компаньона`
    render()
  }
  if (message.type === 'fileHistory') {
    fileHistoryData = message.history
    fileHistoryPath = message.history?.path || fileHistoryPath
    fileHistoryStatus = 'ready'
    render()
  }
  if (message.type === 'questOutcome') {
    setQuestOutcome(message.outcome)
    render()
  }
  if (message.type === 'intakeBusy') {
    intakeBusy = Boolean(message.busy)
    if (message.busy) intakeError = ''
    render()
  }
  if (message.type === 'intakeError') {
    intakeBusy = false
    intakeError = String(message.message || 'Intake failed')
    render()
  }
  if (message.type === 'intakeUpdated') {
    intakeBusy = false
    intakeError = ''
    if (message.session?.id) selectedIntakeId = message.session.id
    if (message.session?.url) intakeURL = message.session.url
    render()
  }
  if (message.type === 'intakeSelected') {
    selectedIntakeId = String(message.id || '')
    render()
  }
  if (message.type === 'questReplans') {
    setQuestReplans(message.replans || [])
    render()
  }
  if (message.type === 'questReplanDone' || message.type === 'questReviseDone') {
    resetQuestReplansCache()
    if (message.error) transientError = String(message.error)
    render()
  }
  if (message.type === 'handoffs') {
    setHandoffChain(message.handoffs)
    render()
  }
  if (message.type === 'capabilityDelta') {
    setCapabilityDelta(message.delta, message.key)
    render()
  }
  if (message.type === 'agentCapability') {
    const key = String(message.key || '')
    agentCapabilityInflight.delete(key)
    // Ядро ответило — прежние отказы больше не приговор, можно спрашивать снова.
    agentCapabilityFailed.clear()
    setAgentCapability(message.capability)
    if (key) {
      // Кэш держим грубо: профилей на экране единицы, а сброс целиком дешевле
      // и предсказуемее вытеснителя.
      if (agentCapabilityCache.size >= AGENT_CAPABILITY_LIMIT) agentCapabilityCache.clear()
      agentCapabilityCache.set(key, message.capability)
    }
    render()
  }
  if (message.type === 'orchestratorPolicy') {
    const key = String(message.key || '')
    orchestratorPolicyInflight.delete(key)
    orchestratorPolicyFailed.clear()
    setOrchestratorPolicy(message.policy)
    if (key) {
      if (orchestratorPolicyCache.size >= ORCHESTRATOR_POLICY_LIMIT) orchestratorPolicyCache.clear()
      orchestratorPolicyCache.set(key, message.policy)
    }
    render()
  }
  if (message.viewId && message.viewId !== masterViewId) return
  if (applyMasterMessage(message)) return
  if (message.type === 'decisions') {
    decisionsData = message.decisions
    decisionsStatus = 'ready'
    decisionsError = ''
    markDecisionsLoaded()
    // Выбор держится на идентификаторе, а не на позиции: разрешённое решение
    // исчезает из очереди, и позиция указала бы на соседнее.
    const items = decisionsData?.items || []
    if (!items.some(item => item.id === decisionPick)) decisionPick = items[0]?.id || ''
    render()
  }
  if (message.type === 'statistics') {
    statisticsData = message.statistics
    statisticsStatus = 'ready'
    render()
  }
  if (message.type === 'experienceSearch') {
    experienceSearchQuery = String(message.query || experienceSearchQuery)
    experienceSearchItems = Array.isArray(message.items) ? message.items : []
    experienceSearchStatus = 'ready'
    transientError = ''
    render()
  }
  if (message.type === 'manualLearningPreview') {
    manualLearningDraft = message.request || manualLearningDraft
    manualLearningPreview = message.preview
    manualLearningStatus = 'ready'
    transientError = ''
    render()
  }
  if (message.type === 'manualLearningApplied') {
    manualLearningDraft = undefined
    manualLearningPreview = undefined
    manualLearningStatus = 'idle'
    experienceSearchStatus = experienceSearchQuery ? 'idle' : experienceSearchStatus
    transientError = ''
    render()
  }
  if (message.type === 'docker' || message.type === 'dockerNeedRefresh') {
    if (message.type === 'docker') {
      dockerData = message.docker
      dockerStatus = 'ready'
    } else {
      dockerStatus = 'loading'
      setTimeout(() => vscode.postMessage({ type: 'loadDocker' }), 0)
    }
    render()
  }
  if (message.type === 'dockerLogs') {
    dockerLogs = message.logs
    dockerLogsContainer = message.logs?.container || dockerLogsContainer
    render()
  }
  if (message.type === 'flowSaved') {
    flowDraft = undefined
    selectedFlowId = message.flowId || selectedFlowId
    flowLegacyMode = false
    persistDraft()
    render()
  }
  if (message.type === 'focusComposer') {
    if (message.profileId) selectedProfileId = message.profileId
    state = { ...state, selectedTab: 'quests' }
    render()
    setTimeout(() => {
      const task = root.querySelector('#task')
      if (task) {
        task.focus()
        task.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
      }
    }, 40)
  }
  if (message.type === 'companionIdeContext') {
    applyCompanionIdeContext(message.context)
  }
  if (message.type === 'focusCompanion') {
    companionSetupOpen = false
    if (!isCompanionView()) state = { ...state, selectedTab: 'overview' }
    if (typeof message.message === 'string' && message.message) companionDraft = message.message
    persistDraft()
    if (message.send && companionDraft.trim()) {
      sendCompanionUserMessage(companionDraft.trim())
    } else {
      render()
      setTimeout(() => {
        const input = root.querySelector('#companion-input')
        if (input) {
          input.focus()
          input.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
        }
      }, 40)
    }
  }
})

// До появления bundle эти функции были свойствами Window автоматически:
// smoke/render harnesses используют их для проверки правил без копирования
// реализации. IIFE скрывает модульные объявления, поэтому сохраняем узкий
// совместимый диагностический API; изменяемое состояние наружу не отдаётся.
Object.assign(globalThis, {
  agentCapabilityKeyFor,
  applySkillToolGrants,
  companionQuestionsHtml,
  companionQuickPrompts,
  companionSkillBlockers,
  connectionOrbHtml,
  connectionStatusLabels,
  constructorSkillGapHtml,
  diagnosticSignalText,
  firstIncompleteOnboardingStep,
  formatCompanionMarkdown,
  hireLiveChecklist,
  hubAgents,
  hubModeAvailable,
  legacySaveWouldDrop,
  masterModelLabel,
  masterThreadHtml,
  orchestratorPolicyLines,
  profileReadiness,
  readinessBanner,
  stampMasterAnswer,
  toolFailureText,
})

if (persisted.selectedTab && !restoredTabPosted && !isCompanionView()) {
  restoredTabPosted = true
  vscode.postMessage({ type: 'selectTab', tab: canonicalTab(persisted.selectedTab) })
}
persistDraft()
render()
vscode.postMessage({ type: 'ready', surface: document.body?.dataset?.layout || '' })
if (isToolWindow()) vscode.postMessage({ type: 'loadToolWindowState', kind: toolWindowKind() })
