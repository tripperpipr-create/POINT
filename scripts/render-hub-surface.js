// Стенд для глаз: прогоняет настоящий media/main.js и выкладывает полученную
// разметку в страницу с боевым CSS.
//
// Смоки проверяют, что нужная строка присутствует в разметке. Они не видят, что
// колонка не поместилась, что прокрутка заперта, что шаг ушёл за край экрана.
// Это единственная проверка, которую нельзя сделать чтением кода.
//
//   node scripts/render-hub-surface.js onboarding > build/preview/onboarding.html

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const listeners = {}
const posted = []
const requestedSurface = process.argv[2] || 'overview'
// Регистров у компаньона два, и до сих пор стенд знал только док. Боковая
// панель — та, что живёт в окне IDE постоянно и занимает 300px справа; её
// разметку (`div.app.companion-sidebar`) не рисовал никто.
const COMPANION_LAYOUTS = { companion: 'companion', 'companion-sidebar': 'companion-sidebar' }
// «Подключения» живут двумя жизнями: своим окном (`connections-window`) и
// вкладкой Хаба (`connections`), где от них остался только вход. Стенд рисует
// обе — иначе правка одной молча ломала бы другую.
const DEDICATED_LAYOUTS = { statistics: 'statistics', docker: 'docker', 'connections-window': 'connections' }
// Окна инструментов правой панели — терминал, базы, SSH, Git, логи. Разметку им
// выбирает не вкладка, а `data-layout` вебвью: `tool-<вид>`. Пять поверхностей
// живут в окне IDE постоянно и до цикла 34 не рисовались стендом вовсе.
const isToolWindowSurface = surface => surface.startsWith('tool-')
// Поверхности, которые открываются действием внутри «Гильдии», а не своей
// вкладкой: вкладку им надо поставить ту же, что у ростера.
const GUILD_SURFACES = { constructor: 'agents', character: 'agents' }
// Поиск по таблице с оглядкой на прототип. Одна из поверхностей называется
// `constructor`, а это свойство `Object.prototype`: обычный `TABLE[surface]`
// вернёт функцию-конструктор вместо undefined там, где своего ключа нет.
// Раскладка становилась объектом, и падало это не здесь, а в чужом файле —
// «surfaceLayout.startsWith is not a function» посреди media/main.js.
const pick = (table, key) => (Object.hasOwn(table, key) ? table[key] : undefined)
const requestedLayout = isToolWindowSurface(requestedSurface)
  ? requestedSurface
  : pick(COMPANION_LAYOUTS, requestedSurface) || pick(DEDICATED_LAYOUTS, requestedSurface) || 'wide'
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector() { return null },
  querySelectorAll() { return [] },
}
// Шаг задаётся через сохранённое состояние webview: половина шагов заперта до
// завершения компаньона и мастера, и кликами до них не добраться.
// Сцена образца ответа выбирается кликом, а сверка договорённостей рисует шаг
// разово и кликать не умеет. Поэтому шаг принимает суффикс `@сцена`:
// «companion-config@flow». Без него из пяти образцов проверялся один.
const [seedStep, seedScene] = String(process.argv[3] || '').split('@')
// «fresh» — состояние настоящего первого запуска: ничего не настроено, поэтому
// половина шагов заперта. Именно в нём видно поведение замков.
const fresh = process.argv[4] === 'fresh'
const context = {
  acquireVsCodeApi: () => ({
    postMessage(message) { posted.push(message) },
    getState() {
      // Панель задания открывается кликом по вкладке, а клик здесь идёт в
      // заглушку без разметки: переключение правит узлы, а не отрисовку.
      // Поэтому открытость приходит сохранённым состоянием — тем же путём,
      // которым она переживает перезапуск панели у человека.
      const briefPanel = requestedSurface === 'master' && ['brief-panel', 'inspector-team', 'inspector-context'].includes(process.argv[3])
        ? { masterChat: { active: 'pay', briefPanel: { pay: true } } }
        : undefined
      if (briefPanel) return briefPanel
      // Раскрытые подробности карточек приходят тем же путём, каким они живут у
      // человека, — сохранённым состоянием разговора. Прежде стенд жал на
      // раскрывашку кликом; кликать больше некуда, `<details>` открывает себя
      // сам, а на статической странице стенда этого «сам» не случается.
      const cardOpen = {
        'agent-card': { active: 'pay', cardOpen: { pay: { 'agent:order:workorder-bench:agentdraft-bench': true } } },
        'work-order-open': { active: 'ship', cardOpen: { ship: { 'order:workorder-bench': true } } },
        'brief-open': { active: 'pay', cardOpen: { pay: { 'brief:qp-brief': true } } },
      }[String(process.argv[3] || '')]
      if (requestedSurface === 'master' && cardOpen) return { masterChat: cardOpen }
      if (!seedStep) return undefined
      return {
        onboardingStep: seedStep,
        onboardingDraft: { ...(fresh ? {} : { companionFinished: true, orchestratorFinished: true }), ...(seedScene ? { sampleScene: seedScene } : {}) },
      }
    },
    setState() {},
  }),
  document: {
    getElementById: id => (id === 'root' ? root : undefined),
    body: { dataset: { layout: requestedLayout } },
  },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set, CSS: { escape(value) { return String(value) } },
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() {},
  setTimeout(callback) { callback(); return 0 },
  clearTimeout() {},
}

const repo = path.join(__dirname, '..')
vm.runInNewContext(fs.readFileSync(path.join(repo, 'vscode-extension', 'media', 'main.js'), 'utf8'), context,
  { filename: 'media/main.js' })

// Обжитой мир: агенты наняты, квест идёт, есть незакрытые решения и правки.
// Пустой Хаб выглядит опрятно на любом экране — ломается он на содержимом.
const profiles = [
  { id: 'sage', name: 'SAGE-7', roleDescription: 'Проектирует безопасные изменения.',
    provider: 'ollama', model: 'qwen2.5-coder', allowedTools: ['read_file', 'search_code'],
    maxSteps: 24, approvalMode: 'safe' },
  { id: 'forge', name: 'FORGE', roleDescription: 'Реализует backend-функции и проверяет сборкой.',
    provider: 'openai-compatible', model: 'coder', allowedTools: ['read_file', 'propose_patch', 'run_command'],
    maxSteps: 40, approvalMode: 'safe' },
]
const runs = [
  { id: 'run-1', profileId: 'sage', status: 'completed', task: 'Разведать архитектуру биллинга',
    startedAt: '2026-08-14T10:00:00Z', finishedAt: '2026-08-14T10:04:00Z', step: 3, model: 'qwen2.5-coder' },
  { id: 'run-2', profileId: 'forge', status: 'failed', task: 'Исправить флаки-тест оплаты',
    startedAt: '2026-08-14T11:00:00Z', finishedAt: '2026-08-14T11:09:00Z', step: 4, model: 'coder' },
  { id: 'run-3', profileId: 'forge', status: 'waiting_approval', task: 'Обновить схему миграций',
    startedAt: '2026-08-15T09:40:00Z', step: 2, model: 'coder' },
]
const quests = [
  // teamId связывает квест с записью отряда — так его ставит Мастер через
  // AssignParty. Без этой связи стенд не мог показать отряд «в работе»:
  // фикстура знала только teamAgentIds, и любая партия выглядела простаивающей.
  { id: 'q-1', title: 'Починить оплату подписки', status: 'active', importance: 'high',
    createdAt: '2026-08-14T09:00:00Z', teamId: 'team-1', teamAgentIds: ['sage', 'forge'],
    definitionOfDone: ['Изменения приняты', 'Проверка прошла после последней правки'] },
  { id: 'q-2', title: 'Убрать дубли в журнале событий', status: 'proposed', importance: 'normal',
    createdAt: '2026-08-15T08:10:00Z', teamAgentIds: ['sage'] },
]
const changeSets = [
  // Зависимость нужна не для правдоподобия, а для замера: панель «Граф наборов»
  // рисуется только у набора с dependsOn, и без неё стенд её не видел вовсе.
  { id: 'cs-1', questId: 'q-1', title: 'Правка обработчика вебхука', status: 'pending',
    createdAt: '2026-08-15T09:50:00Z', dependsOn: ['cs-2'],
    items: [
      { id: 'i1', path: 'internal/billing/webhook.go', kind: 'modify', additions: 24, deletions: 6 },
      { id: 'i2', path: 'internal/billing/webhook_test.go', kind: 'add', additions: 41, deletions: 0 },
    ] },
  { id: 'cs-2', questId: 'q-1', title: 'Откат неудачной миграции', status: 'applied',
    createdAt: '2026-08-14T18:20:00Z',
    items: [{ id: 'i3', path: 'internal/storage/migrations.go', kind: 'modify', additions: 3, deletions: 11 }] },
]

const boot = {
  onboarded: true,
  profiles, runs, quests, changeSets,
  projectAgents: profiles,
  executions: [
    { id: 'ex-1', questId: 'q-1', projectAgentId: 'forge', runId: 'run-3',
      status: 'waiting_approval', task: 'Обновить схему миграций' },
  ],
  companion: { id: 'c1', preset: 'balanced', provider: 'ollama', model: 'qwen2.5-coder' },
  orchestrator: { id: 'o1', preset: 'conductor', provider: 'ollama', model: 'qwen2.5-coder' },
  usageRecords: [
    { id: 'u1', profileId: 'sage', costCents: 12, tokensIn: 4200, tokensOut: 900, createdAt: '2026-08-15T09:00:00Z' },
    { id: 'u2', profileId: 'forge', costCents: 41, tokensIn: 9100, tokensOut: 2400, createdAt: '2026-08-15T09:30:00Z' },
  ],
  blueprints: [
    { id: 'bp-scout', name: 'Разведчик', role: 'observer', summary: 'Читает код и объясняет',
      allowedTools: ['read_file', 'search_code'], maxSteps: 24 },
    { id: 'bp-smith', name: 'Кузнец', role: 'editor', summary: 'Правит код и проверяет',
      allowedTools: ['read_file', 'propose_patch', 'run_command'], maxSteps: 40 },
  ],
  // Пояснение у умения не для правдоподобия: строка умения двухстрочная —
  // название и под ним пояснение, — и без него стенд рисовал её в одну строку.
  // Раскладка, которая на одной строке держится, а на двух разъезжается,
  // проверялась бы вхолостую.
  toolCatalog: [
    { name: 'read_file', displayName: 'Чтение файлов', category: 'read', risk: 'low',
      description: 'Читает файлы внутри папки проекта; за её границу не выходит.' },
    { name: 'search_code', displayName: 'Поиск по коду', category: 'index', risk: 'low',
      description: 'Ищет по индексу проекта — символы, ссылки и тексты.' },
    { name: 'propose_patch', displayName: 'Правка файлов', category: 'write', risk: 'medium',
      description: 'Предлагает изменение файла; в рабочую копию оно попадает только после осмотра.' },
    { name: 'run_command', displayName: 'Запуск команд', category: 'exec', risk: 'high',
      description: 'Выполняет команду в песочнице проекта с урезанным окружением.' },
  ],
  // Заготовки инструментов ядро отдаёт всегда (domain.BuiltInCustomToolTemplates),
  // а стенд их не засеивал — стадия «Шаблон» на всех снимках была пустой рамкой.
  customToolTemplates: [
    { id: 'go-tests', name: 'Тесты Go', description: 'Запускает все тесты Go без оболочки.',
      tool: { kind: 'process', displayName: 'Запустить тесты Go', program: 'go', arguments: ['test', './...'], providesVerification: true, cwd: '.', timeoutSeconds: 300 } },
    { id: 'git-status', name: 'Git status', description: 'Показывает краткое состояние репозитория.',
      tool: { kind: 'process', displayName: 'Проверить Git status', program: 'git', arguments: ['status', '--short'], cwd: '.', timeoutSeconds: 60 } },
    { id: 'python-script', name: 'Python-скрипт', description: 'Запускает выбранный агентом скрипт только внутри рабочей папки.',
      tool: { kind: 'process', displayName: 'Запустить Python-скрипт', program: 'python', arguments: ['{{script}}'], cwd: '.', timeoutSeconds: 120,
        parameters: [{ name: 'script', displayName: 'Скрипт', description: 'Путь к скрипту внутри рабочей папки.', type: 'workspace_path', required: true, maxLength: 1024 }] } },
    { id: 'fixed-command', name: 'Фиксированная команда', description: 'Совместимый режим для составной команды без параметров модели.',
      tool: { kind: 'command', displayName: 'Новая фиксированная команда', command: 'go test ./...', providesVerification: true, cwd: '.', timeoutSeconds: 120 } },
  ],
  flows: [
    { id: 'fl-1', name: 'Разбор и починка', description: 'Разведка → правка → проверка',
      nodes: [
        { id: 'n1', kind: 'agent', name: 'Разведка', agentId: 'sage', positionX: 0, positionY: 0 },
        { id: 'n2', kind: 'agent', name: 'Правка', agentId: 'forge', positionX: 220, positionY: 0 },
        { id: 'n3', kind: 'verifier', name: 'Проверка', toolName: 'run_command', positionX: 440, positionY: 0 },
      ],
      edges: [{ id: 'e1', from: 'n1', to: 'n2' }, { id: 'e2', from: 'n2', to: 'n3' }],
      createdAt: '2026-08-14T09:00:00Z', updatedAt: '2026-08-15T09:00:00Z' },
  ],
  flowRuns: [
    { id: 'fr-1', flowId: 'fl-1', questId: 'q-1', status: 'waiting_approval',
      nodeStates: {
        n1: { status: 'completed' },
        n2: { status: 'waiting_approval', outcome: 'нужно решение' },
        n3: { status: 'pending' },
      },
      startedAt: '2026-08-15T09:40:00Z' },
  ],
  teams: [
    { id: 'team-1', name: 'Биллинг', description: 'Разбор и починка оплаты',
      agentIds: ['sage', 'forge'], createdAt: '2026-08-14T09:00:00Z', updatedAt: '2026-08-15T09:00:00Z' },
  ],
  skills: [
    { id: 'sk-review', name: 'Ревью по чек-листу', summary: 'Единый разбор правок',
      requiredTools: ['read_file'], instructions: 'Проверяй по списку.' },
    { id: 'sk-tests', name: 'Прогон тестов', summary: 'Запускает и разбирает падения',
      requiredTools: ['run_command'], instructions: 'Запусти go test.' },
  ],
  projectSkills: [{ id: 'ps-1', skillId: 'sk-review', enabled: true, agentIds: ['sage'] }],
  // Виды памяти — те же, что у ядра (domain.MemoryKind): project, profile,
  // agent, companion, quest. Фикстура держала выдуманные fact и preference, и
  // экран показывал сырое «FACT» вместо подписи: у этих значений подписи нет и
  // быть не может. Настоящие подписи — «Проект», «Основной профиль» — не
  // рендерились ни разу.
  memories: [
    { id: 'm-1', kind: 'project', ownerId: '', content: 'Оплата идёт через вебхук провайдера.',
      source: 'разговор', confidence: 0.9, pinned: true, createdAt: '2026-08-14T10:00:00Z' },
    { id: 'm-2', kind: 'agent', ownerId: 'forge', content: 'Правки в биллинге — только с тестом.',
      source: 'решение', confidence: 0.8, pinned: false, createdAt: '2026-08-15T08:00:00Z' },
    { id: 'm-3', kind: 'profile', ownerId: 'bp-sage', content: 'Проверку завершения подтверждать запуском теста, а не словами.',
      source: 'урок', confidence: 0.7, pinned: false, createdAt: '2026-08-16T09:30:00Z' },
  ],
  connections: [
    { id: 'conn-1', provider: 'ollama', presetId: 'ollama', displayName: 'Локальный Ollama',
      baseUrl: 'http://127.0.0.1:11434', status: 'connected', createdAt: '2026-08-14T09:00:00Z' },
    { id: 'conn-2', provider: 'openai-compatible', presetId: 'llmux', displayName: 'Шлюз компании',
      baseUrl: 'https://gateway.example/v1', status: 'error', lastError: 'ключ отклонён',
      secretRef: 'point.connection.llmux.1', createdAt: '2026-08-13T09:00:00Z' },
  ],
  // Форма пресета — как у ядра (domain.ProviderPreset), а не сокращённая. С
  // {id,label} селектор «Источник» рисовал пустые строки: value, подпись и
  // адрес брались из полей kind/name/baseUrl, которых в фикстуре не было. Ни
  // один замер этого не видел — единственный на весь продукт выбор источника
  // приходил на все страницы пустым.
  providerCatalog: [
    { id: 'ollama', name: 'Ollama', description: 'Локальные модели через Ollama', kind: 'ollama', baseUrl: 'http://127.0.0.1:11434', defaultModel: 'qwen2.5-coder:7b', local: true },
    { id: 'lm-studio', name: 'LM Studio', description: 'Локальный OpenAI-совместимый сервер', kind: 'openai-compatible', baseUrl: 'http://127.0.0.1:1234/v1', defaultModel: 'local-model', local: true },
    { id: 'llmux', name: 'LLMux', description: 'Корпоративный шлюз: свой адрес и токен', kind: 'openai-compatible', baseUrl: '', defaultModel: '', requiresApiKey: true },
    { id: 'custom', name: 'Свой адрес', description: 'Любой OpenAI-совместимый адрес, model ID и токен', kind: 'openai-compatible', baseUrl: '', defaultModel: '', requiresApiKey: true },
    { id: 'anthropic', name: 'Anthropic', description: 'Claude по Messages API', kind: 'anthropic', baseUrl: 'https://api.anthropic.com/v1', defaultModel: 'claude-sonnet-4-5', requiresApiKey: true },
    { id: 'azure-openai', name: 'Azure OpenAI', description: 'Ресурс Azure: адрес, развёртывание и версия API', kind: 'azure-openai', baseUrl: '', defaultModel: '', requiresApiKey: true },
  ],
  indexStatus: { state: 'ready', files: 812 },
}

if (requestedSurface === 'master-handoff') {
  const task = 'Создай квест на анализ проекта. Посмотри на агентов и скажи если нужно создать новых'
  const developers = [
    { ...profiles[0], id: 'dev-1', name: 'Разработчик', provider: 'ollama', primaryModel: 'qwen2.5-coder:7b' },
    { ...profiles[1], id: 'dev-2', name: 'Разработчик', provider: 'openai-compatible', primaryModel: 'local-model' },
  ]
  Object.assign(boot, {
    profiles: developers, projectAgents: developers, runs: [], changeSets: [], questProposals: [], companionActionProposals: [],
    quests: [{ id: 'q-master', title: task, description: task, status: 'active', teamId: 'team-master', objectives: ['Разобраться в текущем состоянии проекта', 'Оценить состав агентов и предложить недостающие роли'] }],
    teams: [{ id: 'team-master', name: 'Party · ' + task, agentIds: ['dev-1', 'dev-2'] }],
    executions: [{ id: 'exec-master', questId: 'q-master', flowRunId: 'flowrun_internal_123', projectAgentId: 'dev-1', status: 'pending', task: 'Primary: ' + task }],
    flowRuns: [{ id: 'flowrun_internal_123', questId: 'q-master', status: 'waiting', nodeStates: { primary: { status: 'waiting_agent' } } }],
  })
}

// Настоящий первый запуск, а не «мастер поверх настроенного мира».
//
// `fresh` до сих пор сбрасывал только черновик вебвью, а мир оставался
// обжитым: компаньон настроен, мастер настроен, два агента, два подключения,
// индекс на 812 файлов. Поэтому колонка готовности показывала 7 / 7 на первом
// же шаге, а пустые состояния шагов не рендерились никогда — ни глазу, ни
// проверке. Здесь мир становится таким, каким его видит человек в первую
// минуту: ничего не настроено.
if (fresh) {
  Object.assign(boot, {
    companion: undefined,
    orchestrator: undefined,
    profiles: [],
    projectAgents: [],
    connections: [],
    runs: [],
    quests: [],
    teams: [],
    executions: [],
    changeSets: [],
    questProposals: [],
    companionActionProposals: [],
    usageRecords: [],
    indexStatus: { state: 'not_built', files: 0 },
  })
}

// Нажатие вместо подмены состояния. Половина поверхностей Хаба открывается не
// вкладкой, а действием — «Улучшить профиль», «Дублировать». Подставить их
// состоянием нельзя: `profileEditorOpen` и `profileEditorStep` живут в модуле
// и в сохранённое состояние webview не попадают. Клик проходит тем же путём,
// что у человека, и потому мерит то, что человек увидит.
const click = dataset => listeners['root:click']({ target: { closest(selector) {
  return selector === '[data-action]' ? { dataset } : null
} } })

// Разговор без мира: первый запуск, папка ещё не выбрана. Раскладка чата та
// же, а карточка ввода стоит запертой вне `.hall-dialogue` — и оформляют её
// базовые правила композера, которые ни одна другая страница стенда не видит.
const withoutWorld = requestedSurface === 'gallery' || (requestedSurface === 'master' && process.argv[3] === 'no-world')
listeners['window:message']({ data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: withoutWorld ? '' : 'ai-ide',
  workspacePath: withoutWorld ? '' : 'C:\worlds\ai-ide',
  selectedTab: pick(COMPANION_LAYOUTS, requestedSurface) || requestedSurface === 'master-handoff' ? 'overview'
    : pick(GUILD_SURFACES, requestedSurface) || requestedSurface,
  ideContext: pick(COMPANION_LAYOUTS, requestedSurface)
    ? { file: 'internal/companion/service.go', line: 948, language: 'go', diagnostics: 2, dirty: true, run: 'go test ./internal/companion' }
    : undefined,
  boot, details: undefined,
} })

if (requestedSurface === 'statistics') {
  listeners['window:message']({ data: { type: 'statistics', statistics: {
    workspaceId: 'world-ai-ide', agents: 2, quests: 2, usageCount: 3,
    totalTokens: 68420, knownCostCents: 53, dailyCostCents: 28, monthlyCostCents: 53,
    budgetDailyCents: 500, budgetMonthlyCents: 5000,
    qualityRunsAnalyzed: 2, qualityHistoryWindow: 3, qualityHistoryLimit: 400,
    agentImprovementsApplied: 1, agentImprovementsRolledBack: 1, agentMemoryCandidates: 0, agentMemoriesPromoted: 1, agentMemoriesConfirmed: 0, agentInstructionCandidates: 0, agentInstructionsPromoted: 1,
    agentImprovements: [
      { id: 'improvement-2', projectAgentId: 'forge', blueprintId: 'backend-blueprint', promotionStatus: 'promoted', sourceRunId: 'run-82', skillId: 'skill-learned-review', kind: 'skill_updated', status: 'applied', trigger: 'successful_complex_run', reviewMode: 'model', model: 'qwen2.5-coder', rollbackAvailable: true, memoryId: 'memory-evidence', memoryStatus: 'promoted', memoryKey: 'evidence-before-completion', memorySourceWorkspaces: ['world-api', 'world-billing'], instructionStatus: 'promoted', instructionKey: 'verify-before-finish', instruction: 'Перед завершением зафиксировать явный успешный результат подходящей проверки.', instructionSourceWorkspaces: ['world-api', 'world-billing'], evidence: ['7 завершённых tool-вызовов', 'верификация записана · успешных проверок: 1', 'изменено файлов: 2', 'универсальный skill · подтверждён независимых проектов: 2', 'постоянная инструкция Blueprint · подтверждено проектов: 2', 'постоянная память Blueprint · подтверждено проектов: 2'], canaryEvaluation: { schemaVersion: 1, status: 'healthy', minimumCandidateRuns: 3, candidate: { skillId: 'skill-learned-review', revision: 2, digest: 'a0d94c173cd4beef' }, baseline: { skillId: 'skill-learned-review', revision: 1, digest: '6ab03ed01131beef' }, candidateMetrics: { runs: 3, completed: 3, healthy: 3, toolCalls: 18, toolFailures: 0, completionRate: 1, healthyRate: 1, toolFailureRate: 0 }, baselineMetrics: { runs: 5, completed: 4, healthy: 4, toolCalls: 27, toolFailures: 2, completionRate: .8, healthyRate: .8, toolFailureRate: .074 }, reasons: ['no measured regression crossed a declared gate'], evaluatedAt: '2026-08-27T10:30:00Z' }, afterMemory: { id: 'memory-evidence', kind: 'profile', ownerId: 'backend-blueprint', content: 'Считать работу завершённой только после явного успешного результата подходящей проверки.', confidence: .85, pinned: true }, afterSkill: { id: 'skill-learned-review', name: 'Безопасная правка backend', description: 'Проверенная процедура изменения backend-кода.', configuration: { revision: 2, promotionStatus: 'promoted', promotionWorkspaceCount: 2 } }, createdAt: '2026-08-27T10:30:00Z', updatedAt: '2026-08-27T10:30:00Z' },
      { id: 'improvement-1', projectAgentId: 'sage', blueprintId: 'backend-blueprint', promotionStatus: 'promoted', sourceRunId: 'run-78', skillId: 'skill-learned-map', kind: 'skill_created', status: 'rolled_back', trigger: 'successful_complex_run', reviewMode: 'deterministic', evidence: ['5 завершённых tool-вызовов', 'универсальный skill · подтверждён независимых проектов: 2'], afterSkill: { id: 'skill-learned-map', name: 'Разведка архитектуры', configuration: { revision: 1, promotionStatus: 'promoted', promotionWorkspaceCount: 2 } }, createdAt: '2026-08-26T09:00:00Z', updatedAt: '2026-08-26T11:00:00Z' },
    ],
    agentStats: [
      { id: 'sage', name: 'SAGE-7', executions: 1, completed: 1, successRate: 100,
        totalTokens: 21400, averageTokens: 21400, averageTimeMs: 240000, knownCostCents: 18,
        quality: { assessedExecutions: 1, healthyExecutions: 1,
          verificationRequired: 1, verificationSatisfied: 1, toolSucceeded: 5, toolFailed: 0 } },
      { id: 'forge', name: 'FORGE', executions: 3, completed: 1, failed: 2, successRate: 33,
        totalTokens: 47020, averageTokens: 15673, averageTimeMs: 270000, knownCostCents: 35,
        quality: { assessedExecutions: 3, attentionExecutions: 2, healthyExecutions: 1,
          verificationRequired: 3, verificationSatisfied: 1, toolSucceeded: 6, toolFailed: 2,
          recommendations: [
            { code: 'verification_gap', severity: 'warning', step: 'skills', title: 'Усилить навык верификации', detail: 'Проверьте testing/build Skill и его required tools. Hub ничего не подключит без вашего подтверждения.', evidence: 'Верификация подтверждена в 1 из 3 обязательных запусков.', actionLabel: 'Открыть навыки агента' },
            { code: 'tool_instability', severity: 'warning', step: 'tools', title: 'Стабилизировать инструменты', detail: 'Проверьте конфигурацию и policy проблемных tools; не расширяйте доступ без необходимости.', evidence: 'Повторные сбои: run_command ×2; всего 2 из 8 вызовов.', actionLabel: 'Открыть tools агента' },
          ] } },
    ],
  } } })
}

// Очередь решений с содержимым. Без неё стенд знал раздел «Решения» только
// пустым: строка очереди, разбор одного решения и вердикт снизу не мерились
// вовсе, хотя именно сюда ведёт тревога «ждёт вас» с Обзора.
// Галерея миров — первый экран Чертога, и единственный, которому не нужно
// ядро. Списка в `boot` нет и быть не может: он приходит от хоста своим
// сообщением, поэтому стенду его надо посеять отдельно.
// Каталог чатов приходит своим сообщением от ядра, а не в `boot`: левую
// панель Чертога без него не нарисовать ни глазом, ни смоуком.
if (requestedSurface === 'master' || requestedSurface === 'chat-directory') {
  listeners['window:message']({ data: { type: 'chatDirectory', directory: {
    currentWorkspaceId: 'ws-current',
    worlds: [
      { workspaceId: 'ws-current', name: 'ai-ide', hash: 'a'.repeat(24), path: String.raw`C:\worldsi-ide`, current: true, chats: [] },
      { workspaceId: 'ws-billing', name: 'billing-api', hash: 'b'.repeat(24), current: false, chats: [
        { id: 'chat-webhook', title: 'Вебхук повторов', updatedAt: new Date(Date.now() - 3600 * 1000).toISOString(), running: true },
        { id: 'chat-pricing', title: 'Тарифы и лимиты', updatedAt: new Date(Date.now() - 26 * 3600 * 1000).toISOString() },
        { id: 'chat-audit', title: 'Аудит счётов', updatedAt: new Date(Date.now() - 72 * 3600 * 1000).toISOString() },
      ] },
      { workspaceId: 'ws-front', name: 'frontend', hash: 'c'.repeat(24), current: false, chats: [
        { id: 'chat-theme', title: 'Тёмная тема', updatedAt: new Date(Date.now() - 5 * 24 * 3600 * 1000).toISOString() },
      ] },
    ],
  } } })
  listeners['window:message']({ data: { type: 'projects', active: String.raw`C:\worldsi-ide`, projects: [
    { path: String.raw`C:\worldsi-ide`, parent: String.raw`C:\worlds`, name: 'ai-ide', hash: 'a'.repeat(24), pinned: false, lastOpenedAt: Date.now(), branch: 'main', core: 'running', slow: false },
    { path: String.raw`C:\worldsilling-api`, parent: String.raw`C:\worlds`, name: 'billing-api', hash: 'b'.repeat(24), pinned: false, lastOpenedAt: Date.now() - 3600 * 1000, branch: 'feature/webhook-retry', core: 'warm', slow: false },
    { path: String.raw`C:\worldsrontend`, parent: String.raw`C:\worlds`, name: 'frontend', hash: 'c'.repeat(24), pinned: false, lastOpenedAt: 0, branch: 'main', core: 'idle', slow: false },
  ] } })
}

if (requestedSurface === 'gallery') {
  const hours = value => Date.now() - value * 3600 * 1000
  listeners['window:message']({ data: { type: 'projects', active: '', projects: [
    { path: String.raw`C:\worlds\ai-ide`, parent: String.raw`C:\worlds`, name: "ai-ide", pinned: true, lastOpenedAt: hours(0.02), lastTab: "master", openCount: 42, branch: "main", core: "running", slow: false },
    { path: String.raw`C:\worlds\billing-api`, parent: String.raw`C:\worlds`, name: "billing-api", pinned: true, lastOpenedAt: hours(3), lastTab: "quests", openCount: 18, branch: "feature/webhook-retry", core: "warm", slow: false },
    { path: String.raw`C:\worlds\frontend`, parent: String.raw`C:\worlds`, name: "frontend", pinned: false, lastOpenedAt: hours(26), lastTab: "", openCount: 7, branch: "main", core: "idle", slow: false },
    { path: String.raw`D:\архив\go-health`, parent: String.raw`D:\архив`, name: "go-health", pinned: false, lastOpenedAt: hours(200), lastTab: "", openCount: 2, branch: "", core: "idle", slow: false },
    { path: String.raw`\\nas\worlds\legacy-monolith`, parent: String.raw`\\nas\worlds`, name: "legacy-monolith", pinned: false, lastOpenedAt: 0, lastTab: "", openCount: 0, branch: "", core: "idle", slow: true },
  ] } })
}

if (requestedSurface === 'decisions') {
  listeners['window:message']({ data: { type: 'decisions', decisions: { total: 2, items: [
    {
      id: 'dec-1', kind: 'command', label: 'КОМАНДА', title: 'Выполнить `go test ./internal/billing/...`',
      detail: 'Агент просит запустить тесты биллинга перед правкой обработчика вебхука.',
      who: 'FORGE · Правка обработчика вебхука', runId: 'run-77', risk: 'MEDIUM', blocking: true, waitingMs: 14 * 60 * 1000,
      resolve: { path: 'runs/run-77/approvals/dec-1' },
    },
    {
      id: 'dec-2', kind: 'patch', label: 'ПРАВКА', title: 'Применить изменение в internal/billing/webhook.go',
      detail: 'Обработчик повторной доставки переписан; публичный контракт не тронут.',
      who: 'SAGE-7 · Разведать архитектуру биллинга', runId: 'run-78', risk: 'LOW', blocking: false, waitingMs: 3 * 60 * 1000,
      resolve: { path: 'runs/run-78/approvals/dec-2' },
    },
  ] } } })
}

// Две навигации по шагам, которых стенд не знал до цикла 28: конструктор
// агента и карточка персонажа. Обе живут на вкладке «Гильдия» и открываются
// действием, а не выбором вкладки, поэтому ни одна страница их не показывала —
// правки в них проверялись гейтом, но не замером.
if (requestedSurface === 'constructor') {
  click({ action: 'open-agent-constructor' })
  // Шаг задаётся вторым доводом: первый шаг короткий, а разъезжаются длинные —
  // «Инструменты» и «Разрешения» с их списками.
  const step = process.argv[3] || ''
  if (step) click({ action: 'constructor-step', step })
}
// Студия компаньона: единственное место, где рисуется образец его ответа —
// «как он ответит на такую просьбу». Её не рендерил ни один стенд, и половина
// образца оставалась английской: сверка договорённостей туда не заглядывала.
if (requestedSurface === 'companion-studio') {
  click({ action: 'open-companion-setup' })
  if (seedStep) click({ action: 'companion-setup-step', step: seedStep })
  if (seedScene) click({ action: 'companion-select-scene', scene: seedScene })
}
if (requestedSurface === 'character') {
  // «Дублировать» — единственный вход в карточку, не перехваченный режимом
  // Хаба: «Улучшить профиль» и «Новый специалист» из ростера уводят в
  // конструктор, и карточка через них не открывается вовсе.
  click({ action: 'duplicate-profile', id: 'sage' })
  const step = process.argv[3] || ''
  if (step) click({ action: 'profile-step', step })
}

// Окно инструмента получает снимок тем же сообщением, что и в продукте, —
// `toolWindowState`. Без него git показывает «Загружаем ветку и изменения…»,
// терминал — пустой список консолей, логи — пустую ленту. Мерить состояние
// ожидания вместо рабочего экрана значит мерить не тот продукт: строку с путём
// файла, ряд уровней и список веток на 255px никто бы не увидел.
const TOOL_SNAPSHOTS = {
  git: {
    available: true, branch: 'feature/billing-webhook', root: 'C:/Users/dev/ai-ide',
    // Отслеживаемые правки раскладываются по спискам изменений, а не по
    // области Git: `gitGroupOf` смотрит `item.list`, и без `changeLists` три
    // файла из четырёх не попадали ни в одну группу — дерево рисовалось из
    // одной строки, а «выбрано 3 из 4» под ним говорило правду.
    changeLists: [
      { id: 'default', name: 'Изменения по умолчанию', active: true },
      { id: 'review', name: 'На проверке' },
    ],
    changes: [
      { path: 'internal/billing/webhook.go', status: 'M', list: 'default' },
      { path: 'internal/billing/webhook_test.go', status: 'A', list: 'default' },
      { path: 'internal/storage/migrations.go', status: 'M', list: 'review' },
      { path: 'docs/api.md', status: '??', area: 'untracked' },
    ],
    commits: [
      { hash: '9f2c1ab4d5e6', subject: 'Идемпотентная повторная доставка вебхука', author: 'FORGE', date: '2026-08-15T09:50:00Z' },
      { hash: '4b8e77c0a112', subject: 'Откат неудачной миграции', author: 'SAGE-7', date: '2026-08-14T18:20:00Z' },
    ],
    repositories: [{ root: 'C:/Users/dev/ai-ide', name: 'ai-ide' }],
  },
  terminal: {
    run: 'go test ./internal/billing/...',
    terminals: [
      { name: 'Point · Квест', active: true, cwd: 'C:/Users/dev/ai-ide' },
      { name: 'Point · Тесты', active: false, cwd: 'C:/Users/dev/ai-ide' },
      { name: 'go run .', active: false, cwd: 'C:/Users/dev/ai-ide/examples/go-health' },
    ],
  },
  logs: {
    // Уровень называется `warning`, а не `warn`: так его пишет расширение
    // (`counts = { error, warning, info, debug }`), и по нему же фильтрует
    // лента. Со снимком на `warn` панель показывала «ПРЕДУПРЕЖДЕНИЯ 0» над
    // двумя жёлтыми строками — стенд врал ровно там, где его завели смотреть.
    counts: { error: 1, warning: 2, info: 24 },
    path: 'C:/Users/dev/AppData/Roaming/Point/logs/point-core.log',
    lines: [
      { time: '16:34:25', level: 'error', source: 'core', message: 'Индексация прервана: файл занят другим процессом' },
      { time: '16:34:21', level: 'warning', source: 'agent', message: 'Верификация не записана после последней правки' },
      { time: '16:34:18', level: 'warning', source: 'index', message: 'Пропущено 3 файла вне рабочей папки' },
      { time: '16:34:02', level: 'info', source: 'core', message: 'Локальное ядро поднято на 127.0.0.1:8765' },
      { time: '16:33:58', level: 'info', source: 'index', message: 'Индекс готов: 812 файлов' },
    ],
  },
}
if (isToolWindowSurface(requestedSurface)) {
  const kind = requestedSurface.slice('tool-'.length)
  const snapshot = pick(TOOL_SNAPSHOTS, kind)
  if (snapshot) listeners['window:message']({ data: { type: 'toolWindowState', snapshot: { kind, ...snapshot } } })
}

// Время реплик стенда считается от полуночи сегодняшнего дня: снимок обязан
// быть одинаковым в любой день, иначе замер раскладки меняется сам по себе.
const atMidnight = (shiftDays, hours, minutes) => {
  const date = new Date()
  date.setHours(0, 0, 0, 0)
  date.setDate(date.getDate() + shiftDays)
  date.setHours(hours, minutes)
  return date.toISOString()
}
const today = (hours, minutes) => atMidnight(0, hours, minutes)
const yesterday = (hours, minutes) => atMidnight(-1, hours, minutes)

if (process.argv[2] === 'master') {
  const variant = process.argv[3] || 'quest'
  if (variant === 'sessions') {
    listeners['window:message']({data:{type:'master',master:{configured:true,config:boot.orchestrator,sessions:{active:'design',mode:'questions',memory:'Примеры на Go. Объяснения по-русски.',items:[{id:'design',title:'Интерфейс IDE'},{id:'legacy',title:'План проекта'}]},history:[{id:'ask',role:'user',content:'Помоги продумать интерфейс IDE'},{id:'reply',role:'assistant',mode:'model',model:'qwen2.5-coder:7b',content:'Давайте уточним основной сценарий, затем составим план.',questions:['Для каких задач вы чаще всего открываете IDE?']}]}}})
  } else if (variant === 'sending') {
    // Ход идёт: у композера в этом состоянии своя разметка — «Остановить»,
    // другая подсказка, — и без стенда она не рисовалась ни разу.
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      // Без sessions masterComposerHtml возвращает пусто, и ряд управления в
      // идущем ходе не рисовался ни разу — снимок был о композере без композера.
      sessions: { active: 'db', mode: 'auto', workMode: 'discuss', items: [{ id: 'db', title: 'Миграция базы' }] },
      history: [{ id: 'mu-1', role: 'user', content: 'Собери отряд под миграцию базы' }],
    } } })
    listeners['root:input']({ target: { id: 'master-input', value: 'Собери отряд под миграцию базы', closest: () => null, matches: () => false } })
    click({ action: 'master-send' })
    // Ядро уже сообщило, чем занято: событие `tools` несёт сырое имя
    // инструмента, и русскую подпись строки ожидания без него не увидеть.
    listeners['window:message']({ data: { type: 'masterTurn', turn: { id: 'turn-tools', conversationId: '', status: 'tools', progress: 'read_file', reply: '' } } })
  } else if (/^(streaming-|stream-error$|turn-failed$|turn-cancelled$)/.test(variant)) {
    // Ход Мастера по фазам: ожидание, след, текст с открытым блоком кода,
    // конец хода до прихода истории, оборванный поток — и сохранённые ядром
    // сорванный и остановленный ходы. Каждая фаза — отдельный облик одного и
    // того же блока, и прыжок между ними виден только при сравнении страниц.
    const sessions = { active: 'db', mode: 'auto', workMode: 'discuss', items: [{ id: 'db', title: 'Миграция базы' }] }
    const ask = 'Проверь миграцию заказов и поправь повтор вебхука'
    const history = [
      { id: 'u0', role: 'user', content: 'Что в проекте с базой?', createdAt: today(9, 40) },
      { id: 'a0', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b', content: 'Миграции лежат в `internal/storage/migrations`, последняя — **0008_orders**.', createdAt: today(9, 41) },
    ]
    const partial = 'Начал с проверки схемы: таблица **orders** уже перенесена, а'
    if (variant === 'turn-failed' || variant === 'turn-cancelled') {
      history.push({ id: 'u1', role: 'user', content: ask, createdAt: today(10, 2) }, {
        id: 'a1', role: 'assistant', createdAt: today(10, 3), content: partial,
        mode: variant === 'turn-failed' ? 'failed' : 'cancelled',
        fallbackReason: variant === 'turn-failed' ? 'модель вернула 502 Bad Gateway' : 'Ответ остановлен',
      })
      listeners['window:message']({ data: { type: 'master', master: { configured: true, config: boot.orchestrator, sessions, history } } })
    } else {
      listeners['window:message']({ data: { type: 'master', master: { configured: true, config: boot.orchestrator, sessions, history } } })
      listeners['root:input']({ target: { id: 'master-input', value: ask, closest: () => null, matches: () => false } })
      click({ action: 'master-send' })
      const turnId = [...posted].reverse().find(message => message.type === 'masterChat')?.turnId
      const event = (type, text, detail) => listeners['window:message']({ data: { type: 'masterEvent', event: {
        turnId, conversationId: 'db', type, text: text || '', detail: detail ? JSON.stringify(detail) : '',
      } } })
      let status = 'waiting'
      let progress = ''
      let reply = ''
      if (variant !== 'streaming-wait') {
        event('reasoning', '', { round: 1, delta: 'Посмотрю миграции и обработчик вебхука, потом сверю тесты.' })
        const files = ['internal/storage/migrations/0008_orders.sql', 'internal/billing/webhook.go', 'internal/billing/webhook_test.go', 'internal/storage/sqlite.go', 'internal/billing/retry.go']
        files.forEach((file, index) => {
          event('tools', 'read_file', { round: 1, tool: 'read_file', argument: file })
          event('tool_result', '', { round: 1, tool: 'read_file', result: index === 3 ? 'нет файла: internal/storage/sqlite.go' : `${[42, 118, 64, 0, 37][index]} строк`, failed: index === 3 })
        })
        event('tools', 'search_text', { round: 2, tool: 'search_text', argument: 'RetryDelivery' })
        status = 'tools'
        progress = 'search_text'
      }
      if (['streaming-text', 'streaming-settling', 'stream-error'].includes(variant)) {
        event('tool_result', '', { round: 2, tool: 'search_text', result: '[]' })
        reply = variant === 'streaming-settling'
          ? 'Нашёл причину: **повтор вебхука** не проверял ключ идемпотентности.\n\n1. Добавил проверку ключа\n2. Покрыл тестом повторную доставку\n\n```go\nif seen(key) { return nil }\n```'
          : variant === 'stream-error' ? partial
            : 'Нашёл причину: **повтор вебхука** не проверял ключ идемпотентности.\n\n1. Добавил проверку ключа\n2. Покрыл тестом\n\n```go\nif seen(key) {\n  return nil'
        event('reply', reply)
        status = 'streaming'
      }
      if (variant === 'streaming-settling') { event('done', 'completed'); status = 'completed' }
      if (variant === 'stream-error') {
        listeners['window:message']({ data: { type: 'masterStreamError', conversationId: 'db', message: 'соединение с ядром потеряно' } })
        status = 'failed'
      }
      // Стенд рисует разметку разделом целиком, а точечные правки ленты у него
      // некуда применять: повторный приход того же хода просит полную отрисовку.
      listeners['window:message']({ data: { type: 'masterTurn', turn: { id: turnId, conversationId: 'db', status, progress, reply } } })
    }
  } else if (variant === 'markdown') {
    // Разметка ответа целиком: заголовки, списки со вложением и задачами,
    // таблица шире колонки, цитата, ссылки и блок кода с длинной строкой. По
    // отдельности каждая вещь где-нибудь да встречается, а переполнение колонки
    // и спор отступов видны только вместе.
    const answer = [
      '## План миграции',
      'Сначала **сверю схему**, потом _перенесу данные_ и ~~удалю~~ отключу старые таблицы. Подробности — в [документации Postgres](https://www.postgresql.org/docs/current/ddl-alter.html) и в `internal/storage/sqlite.go:412`.',
      '### Шаги',
      '1. Снять дамп',
      '2. Применить миграции:',
      '   - `0007_users.sql`',
      '   - `0008_orders.sql`',
      '3. Прогнать тесты',
      '',
      '- [x] Резервная копия',
      '- [ ] Проверка на стенде',
      '',
      '> Миграция необратима без дампа: откат сделает только восстановление.',
      '',
      '| Таблица | Строк | Размер | Индексы | Владелец | Комментарий к переносу |',
      '| :--- | ---: | ---: | :---: | --- | --- |',
      '| users | 12 480 | 3,1 МБ | 4 | auth | переносится первой, от неё зависят заказы и платежи |',
      '| orders | 318 002 | 96 МБ | 7 | billing | переносится пачками по 10 000 строк, чтобы не держать блокировку |',
      '',
      '---',
      '```go',
      'func migrate(ctx context.Context, db *sql.DB, steps []Step) error { for _, step := range steps { if err := step.Apply(ctx, db); err != nil { return fmt.Errorf("шаг %s: %w", step.Name, err) } }; return nil }',
      '```',
      'Готово к запуску после вашего подтверждения.',
    ].join('\n')
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      sessions: { active: 'db', mode: 'auto', workMode: 'discuss', items: [{ id: 'db', title: 'Миграция базы' }] },
      history: [
        { id: 'mu-1', role: 'user', content: 'Распиши план миграции базы', createdAt: today(10, 2) },
        { id: 'ma-1', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b', content: answer, createdAt: today(10, 3) },
      ],
    } } })
  } else if (variant === 'composer') {
    // Композер со всеми слотами сразу: обсуждаемое задание, вложенные файлы,
    // уточнение модели и длинная реплика в поле. По отдельности каждый слот
    // где-нибудь да рисуется, а вместе — нигде, и разъезжаются они именно
    // вместе: карточка обязана остаться одной вещью, а не стопкой рамок.
    const proposal = {
      id: 'qp-composer', title: 'Укрепить обработчик оплаты', status: 'pending', importance: 'important',
      objectives: ['Воспроизвести отказ вебхука'], definitionOfDone: ['Регресс-набор зелёный'],
      teamAgentIds: ['sage'], estimateTokens: 31000,
    }
    boot.questProposals = [proposal]
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      sessions: { active: 'pay', mode: 'auto', workMode: 'discuss', items: [{ id: 'pay', title: 'Оплата' }] },
      history: [
        { id: 'mu-1', role: 'user', content: 'Вебхук оплаты падает на повторной доставке' },
        {
          id: 'ma-1', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b',
          content: 'Уточню пару деталей, потом предложу план.',
          questions: ['С какой стороны воспроизводится отказ?'],
        },
      ],
    } } })
    click({ action: 'quest-proposal-discuss', id: proposal.id })
    listeners['root:input']({ target: {
      id: 'master-input',
      value: 'Вебхук падает на повторной доставке: первый вызов проходит, второй с тем же идентификатором возвращает 500. Нужно понять, где теряется идемпотентность и как закрыть это регрессом.',
      closest: () => null, matches: () => false,
    } })
    listeners['window:message']({ data: { type: 'masterContext', conversationId: 'pay', contexts: [
      { name: 'internal/app/payments.go', kind: 'file', content: 'package app' },
      { name: 'webhook-error.log', kind: 'file', content: '500 duplicate delivery' },
    ] } })
  } else if (variant === 'questions') {
    // Обмен уточнениями целиком: решённый пакет с ответами на своих местах,
    // неотвеченный вопрос, названный неотвеченным, и живой пакет ниже — тот, на
    // который ещё отвечать. По отдельности ни одно из трёх состояний нигде не
    // рисовалось, а расходятся они именно вместе: решённое обязано быть тише
    // живого, иначе разговор читается как анкета из четырёх одинаковых форм.
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      sessions: { active: 'pay', mode: 'questions', workMode: 'discuss', items: [{ id: 'pay', title: 'Оплата' }] },
      history: [
        { id: 'mq-1', role: 'user', content: 'Вебхук оплаты падает на повторной доставке', createdAt: today(11, 4) },
        {
          id: 'mq-2', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b',
          content: 'Уточню пару деталей, потом соберу задание.',
          createdAt: today(11, 5),
          questions: [
            { text: 'С какой стороны воспроизводится отказ?', options: ['Со стороны платёжного шлюза', 'Со стороны нашего обработчика'], kind: 'single' },
            { text: 'Что считать готовым результатом?', options: ['Повторная доставка не создаёт второй платёж', 'Регресс-набор зелёный'], kind: 'multiple' },
            { text: 'Есть ли окно, когда правку нельзя выкладывать?', kind: 'text' },
          ],
        },
        {
          id: 'mq-3', role: 'user', createdAt: today(11, 9),
          content: [
            'Вопрос: С какой стороны воспроизводится отказ?\nМой ответ: Со стороны нашего обработчика',
            'Вопрос: Что считать готовым результатом?\nМой ответ: Повторная доставка не создаёт второй платёж; Регресс-набор зелёный; и логи без дублей',
          ].join('\n\n'),
        },
        {
          id: 'mq-4', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b',
          content: 'Понял. Остался один вопрос — без него не соберу окно выкладки.',
          createdAt: today(11, 10),
          questions: [{ text: 'Есть ли окно, когда правку нельзя выкладывать?', options: ['Выкладывать можно в любое время', 'Только вне часов пик'], kind: 'single' }],
        },
      ],
    } } })
  } else if (variant === 'brief-panel' || variant === 'inspector-team' || variant === 'inspector-context') {
    // Задание в обсуждении: карточки в ленте нет, состав задания в правой
    // панели, неотвеченный пакет — в карточке ввода. Три поверхности меряются
    // вместе, потому что они делят ширину раздела: панель сужает ленту и
    // карточку ввода, и разъезжаются они именно так, все сразу.
    const proposal = {
      id: 'qp-panel', status: 'pending',
      brief: {
        version: 1, state: 'discussion', mode: 'undecided', resultKind: '',
        goal: 'Сделать повторную доставку вебхука оплаты безопасной',
        scope: ['Обработчик вебхука и его хранилище идемпотентности', 'Регресс-набор по оплате'],
        outOfScope: ['Публичный контракт API', 'Миграции биллинга'],
        criteria: [
          { id: 'c1', text: 'Повторная доставка с тем же идентификатором не создаёт второй платёж', kind: 'verification' },
          { id: 'c2', text: 'Отказ воспроизводится до правки и не воспроизводится после', kind: 'reproduction' },
        ],
        decisions: [],
        openQuestions: ['С какой стороны воспроизводится отказ?', 'Что считать готовым результатом?'],
        permissions: { writeFiles: true, executeCommands: true, networkHosts: [] },
        budget: { tokens: 200000, costCents: 0, activeSeconds: 3600, maxParallel: 2, maxAttempts: 3, maxReplans: 6 },
      },
    }
    boot.questProposals = [proposal]
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      sessions: { active: 'pay', mode: 'questions', workMode: 'discuss', items: [{ id: 'pay', title: 'Оплата' }] },
      history: [
        { id: 'mp-1', role: 'user', content: 'Вебхук оплаты падает на повторной доставке', createdAt: today(12, 1) },
        {
          id: 'mp-2', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b',
          content: 'Собрал черновик задания. Уточню две детали, и его можно будет запускать.',
          proposalId: proposal.id, createdAt: today(12, 4),
          questions: [
            { text: 'С какой стороны воспроизводится отказ?', options: ['Со стороны платёжного шлюза', 'Со стороны нашего обработчика'], kind: 'single' },
            { text: 'Что считать готовым результатом?', kind: 'text' },
          ],
        },
      ],
      response: { proposal },
    } } })
    // Вкладки правой панели: команда с раскрытым листом агента и контекст.
    // Вкладку в сохранённое состояние не кладут, поэтому её выбирает клик, а
    // отрисовку — повтор ответа ядра.
    if (variant !== 'brief-panel') {
      click(variant === 'inspector-team' ? { action: 'master-inspector-agent', id: boot.projectAgents?.[0]?.id || '' } : { action: 'master-inspector-tab', tab: 'context' })
      listeners['window:message']({ data: { type: 'master', master: {
        configured: true, config: boot.orchestrator,
        sessions: { active: 'pay', mode: 'questions', workMode: 'discuss', memory: 'Примеры на Go. Объяснения по-русски.', items: [{ id: 'pay', title: 'Оплата' }] },
        history: [
          { id: 'mp-1', role: 'user', content: 'Вебхук оплаты падает на повторной доставке', createdAt: today(12, 1) },
          { id: 'mp-2', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b', content: 'Собрал черновик задания.', proposalId: proposal.id, createdAt: today(12, 4), factsUsed: ['агентов в ростере: 2', 'активных квестов: 0'] },
        ],
        response: { proposal },
      } } })
    }
  } else if (variant === 'brief' || variant === 'brief-open') {
    // Карточка задания: из чего состоит работа и по чему её примут. До правки
    // всё это лежало за одной свёрнутой строкой «Состав задания», и ни одна
    // страница стенда карточку с брифом не рисовала — она правилась вслепую.
    const proposal = {
      id: 'qp-brief', status: 'pending',
      brief: {
        version: 3, state: 'ready', mode: 'project', resultKind: 'workspace_change',
        goal: 'Сделать повторную доставку вебхука оплаты безопасной',
        scope: ['Обработчик вебхука и его хранилище идемпотентности', 'Регресс-набор по оплате'],
        outOfScope: ['Публичный контракт API', 'Миграции биллинга'],
        criteria: [
          { id: 'c1', text: 'Повторная доставка с тем же идентификатором не создаёт второй платёж', kind: 'verification' },
          { id: 'c2', text: 'Регресс-набор по оплате проходит целиком', kind: 'verification', tool: 'run_command', arguments: { command: 'go test ./internal/billing/...' } },
          { id: 'c3', text: 'Отказ воспроизводится до правки и не воспроизводится после', kind: 'reproduction' },
          { id: 'c4', text: 'Логи не содержат дублей по одному идентификатору', kind: 'manual' },
        ],
        decisions: [{ topic: 'Хранилище ключей', decision: 'таблица в основной базе, без Redis' }],
        openQuestions: [],
        permissions: { writeFiles: true, executeCommands: true, networkHosts: [] },
        budget: { tokens: 200000, costCents: 0, activeSeconds: 3600, maxParallel: 2, maxAttempts: 3, maxReplans: 6 },
      },
    }
    boot.questProposals = [proposal]
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      // Разговор назван только у сцены с раскрытыми подробностями: память
      // раскрытия живёт по разговору, и без его имени сеять её некуда. У самой
      // страницы `master-brief` разговора не было с самого начала — трогать её
      // ради соседки значило бы менять то, что уже отмерено.
      ...(variant === 'brief-open' ? { sessions: { active: 'pay', mode: 'auto', workMode: 'discuss', items: [{ id: 'pay', title: 'Оплата' }] } } : {}),
      history: [
        { id: 'mb-1', role: 'user', content: 'Вебхук оплаты падает на повторной доставке', createdAt: today(12, 1) },
        { id: 'mb-2', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b', content: 'Собрал задание. Проверьте условия готовности перед запуском.', proposalId: proposal.id, createdAt: today(12, 4) },
      ],
      response: { proposal },
    } } })
  } else if (variant === 'work') {
    // Квест запущен: этапы с состояниями, управление и хроника. Лента работы
    // рисовалась в разговоре без единой страницы стенда, а состояния этапов
    // жили в обзоре квеста — то есть в другом разделе.
    const proposal = {
      id: 'qp-work', title: 'Починить оплату подписки', status: 'started', importance: 'important',
      flowId: 'fl-1', teamAgentIds: ['sage', 'forge'],
    }
    boot.questProposals = [proposal]
    boot.quests = boot.quests.map(quest => (quest.id === 'q-1' ? { ...quest, flowId: 'fl-1' } : quest))
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      history: [
        { id: 'mw-1', role: 'user', content: 'Запускай', createdAt: today(12, 30) },
        { id: 'mw-2', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b', content: 'Квест запущен. Ниже — этапы и то, где работа сейчас.', proposalId: proposal.id, createdAt: today(12, 31) },
      ],
    } } })
    // Работа стоит на решении человека. Ждущее подтверждение обязано быть видно
    // без раскрытия хроники — и мериться на всех ширинах: раньше его не рисовала
    // ни одна страница стенда.
    listeners['window:message']({ data: { type: 'runDelta', details: {
      run: { id: 'run-3', questId: 'q-1', status: 'waiting_approval', step: 2, requestCount: 7, changedFiles: [] },
      approvals: [{
        id: 'ap-1', status: 'pending', toolName: 'run_command',
        reason: 'проверка после правки идемпотентности',
        arguments: { command: 'go test ./internal/billing/...', cwd: '.', timeoutSeconds: 300, reason: 'проверка после правки идемпотентности' },
      }],
      patches: [],
      events: [
        { type: 'tool.requested', step: 1, data: { tool: 'read_file' } },
        { type: 'model.responded', step: 1, data: { content: 'Нашёл, где теряется идемпотентность: ключ не пишется до ответа шлюза.' } },
        { type: 'approval.requested', step: 2, data: { id: 'ap-1' } },
      ],
    } } })
  } else if (variant === 'hire') {
    // Нанимать некого: ростер пуст, и Мастер предлагает чертёж. Карточка найма
    // жила только в живом ответе — ни одна страница стенда её не рисовала, и
    // правки в неё ложились вслепую.
    boot.profiles = []
    boot.projectAgents = []
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      history: [
        { id: 'mh-1', role: 'user', content: 'Кто может заняться платёжным API?', createdAt: today(10, 12) },
        { id: 'mh-2', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b', content: 'В ростере пусто — заняться некому. Ближе всех к задаче чертёж «Кузнец».', createdAt: today(10, 13), factsUsed: ['агентов в ростере: 0', 'активных квестов: 0'] },
      ],
      response: {
        hire: {
          blueprintId: 'bp-smith', name: 'Кузнец платёжного контура',
          role: 'Правит backend и проверяет сборкой',
          why: 'задача про изменение кода и проверку, а в ростере нет ни одного агента с правом правки файлов',
          tools: ['read_file', 'search_code', 'propose_patch', 'run_command'],
        },
      },
    } } })
  } else if (variant === 'agent-card') {
    // Карточка нового исполнителя: самое крупное решение ленты после карточки
    // запуска. Раскрытые права и пределы — её худший случай по высоте и по
    // числу целей указателя, поэтому стенд раскрывает их сам.
    boot.profiles = []
    boot.projectAgents = []
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      sessions: { active: 'pay', mode: 'auto', workMode: 'plan', items: [{ id: 'pay', title: 'Платёжный контур' }] },
      history: [
        { id: 'ac-1', role: 'user', content: 'Сделай health-эндпоинт и README', createdAt: today(11, 4) },
        { id: 'ac-2', role: 'assistant', mode: 'model', model: 'Qwen3.8-27B', content: 'Собрал карточку запуска. Выполнять некому — заведите исполнителя.', createdAt: today(11, 6) },
      ],
      workOrders: [{
        id: 'workorder-bench', version: 1, state: 'ready', digest: 'sha256:bench', conversationId: 'pay',
        goal: 'Рабочий Symfony-проект с health-эндпоинтом и README',
        scope: ['composer create-project', 'health-endpoint GET /health', 'README с командами запуска'],
        criteria: [{ id: 'health', kind: 'verification', text: 'GET /health возвращает HTTP 200' }],
        sources: [], network: [], secrets: [],
        workspace: { mode: 'existing', path: 'C:\\Users\\Rif\\Point\\systemio', isolation: 'snapshot' },
        stack: { id: 'recommended-web', category: 'web', version: '1' },
        routing: { mode: 'fixed', fixedModel: 'Qwen3.8-27B', certification: 'experimental', costKnown: false },
        budget: { tokens: 200000, activeSeconds: 3600, maxSteps: 64, maxParallel: 2 },
        completion: { id: 'mvp-web', version: '1', checks: [{ kind: 'acceptance' }] },
        delivery: { applyMode: 'automatic', commitMode: 'none', keepPartialDays: 30 },
        roster: { permanent: [{
          id: 'agentdraft-bench', name: 'Разработчик проекта', role: 'Владелец реализации',
          mission: 'Собрать проект и довести health-эндпоинт до зелёного',
          requiredTools: ['read_file', 'search_code', 'propose_patch', 'run_command'],
          existing: false, requiresConsent: true,
        }], temporary: [] },
      }],
      hiring: [{
        workOrderId: 'workorder-bench', goal: 'Рабочий Symfony-проект', state: 'blueprint',
        maxAgents: 2, allowSubagents: false,
        reason: 'в ростере нет ни одного исполнителя с правом правки файлов',
        draft: { id: 'agentdraft-bench', name: 'Разработчик проекта', role: 'Владелец реализации', mission: 'Собрать проект', requiredTools: ['read_file'], blueprintId: 'bp-smith' },
        blueprints: [
          { blueprintId: 'bp-smith', name: 'Кузнец платёжного контура', role: 'Правит backend', why: 'совпало с задачей', tools: ['read_file', 'propose_patch'] },
          { blueprintId: 'bp-scout', name: 'Разведчик', role: 'Читает и объясняет', why: 'знает индекс проекта', tools: ['search_code'] },
        ],
      }],
    } } })
  } else if (variant === 'mention') {
    // Открытый список файлов под кареткой. Он перекрывает ленту и стоит выше
    // вуали — проверить это можно только замером, и до этой страницы его не
    // рисовал никто.
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      sessions: { active: 'pay', mode: 'auto', workMode: 'discuss', items: [{ id: 'pay', title: 'Оплата' }] },
      history: [{ id: 'mm-1', role: 'user', content: 'Разберись с вебхуком', createdAt: today(14, 20) }],
    } } })
    listeners['root:input']({ target: { id: 'master-input', value: 'Посмотри @webhook', closest: () => null, matches: () => false } })
    listeners['window:message']({ data: { type: 'masterContextSuggestions', conversationId: 'pay', query: 'webhook', items: [
      { path: 'internal/billing/webhook.go', open: true },
      { path: 'internal/billing/webhook_test.go', open: false },
      { path: 'vscode-extension/ui/client/master-context-ui.js', open: false },
      { path: 'docs/webhook-idempotency.md', open: false },
    ] } })
  } else if (variant === 'attachments') {
    // Все виды вложений сразу: файл, картинка с миниатюрой, выделение, ошибки,
    // git diff и вывод терминала. По отдельности ни один не рисовался стендом, а
    // расходятся они именно вместе — ряд переносится, и счётчик бюджета встаёт
    // в его конец.
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator, contextBudgetChars: 12000,
      sessions: { active: 'pay', mode: 'auto', workMode: 'discuss', items: [{ id: 'pay', title: 'Оплата' }] },
      history: [{ id: 'ma-1', role: 'user', content: 'Разберись с оплатой', createdAt: today(15, 10) }],
    } } })
    listeners['window:message']({ data: { type: 'masterContext', conversationId: 'pay', contexts: [
      { name: 'internal/billing/webhook.go', path: 'internal/billing/webhook.go', kind: 'file', content: 'package billing\n'.repeat(300) },
      // Однопиксельный PNG: миниатюре хватает, а снимок остаётся одинаковым в
      // любой день — большой файл сделал бы страницу тяжелее без пользы.
      { name: 'shot.png', kind: 'image', mime: 'image/png', content: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==' },
      { name: 'internal/app/master_chat.go:120', path: 'internal/app/master_chat.go', kind: 'selection', startLine: 120, content: 'func (a *App) MasterChat() {}' },
      { name: 'Ошибки и предупреждения', kind: 'diagnostics', content: 'internal/billing/webhook.go:41: повторная доставка не обрабатывается' },
      { name: 'Изменения Git относительно HEAD', kind: 'diff', content: 'diff --git a/internal/billing/webhook.go b/internal/billing/webhook.go\n'.repeat(60) },
      { name: 'Вывод терминала из буфера', kind: 'terminal', content: 'go test ./internal/billing/...\nFAIL\n'.repeat(30) },
    ] } })
  } else if (variant === 'compose-error') {
    // Отказ по длине: набранное остаётся в поле, объяснение стоит между полем и
    // рядом управления. Разметку отказа не рисовала ни одна страница стенда.
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      sessions: { active: 'pay', mode: 'auto', workMode: 'discuss', items: [{ id: 'pay', title: 'Оплата' }] },
      history: [{ id: 'me-1', role: 'user', content: 'Вставил сюда весь файл', createdAt: today(16, 5) }],
    } } })
    const oversized = 'Разбери этот файл целиком. '.repeat(1400)
    listeners['root:input']({ target: { id: 'master-input', value: oversized, closest: () => null, matches: () => false } })
    click({ action: 'master-send' })
  } else if (variant.startsWith('work-order')) {
    // Единая карточка запуска. Её не рисовал ни один стенд, поэтому раскладку
    // карточки — самого крупного объекта ленты — не мерил ни один замер.
    // Три состояния разведены по страницам: у них разные блоки, и общего
    // снимка, на котором видно и вопросы, и доказательства, не бывает.
    const state = variant === 'work-order-ready' ? 'ready' : variant === 'work-order-approved' ? 'approved' : 'discussion'
    const order = {
      id: 'workorder-bench', version: state === 'discussion' ? 1 : 2, state,
      digest: 'sha256:bench', conversationId: 'ship',
      goal: 'Рабочий Symfony-проект в рабочей области с задокументированным запуском',
      scope: ['composer create-project на актуальной мажорной версии Symfony', 'минимальный health-endpoint (GET /health)', 'composer install и проверка запуска', 'README с командами запуска'],
      assumptions: ['версия Symfony: актуальная мажорная (7.x)', 'health-endpoint: GET /health возвращает 200 JSON', 'запуск: symfony serve или php -S localhost:8000'],
      outOfScope: ['бизнес-логика и доменные сущности', 'миграции БД и ORM-настройка', 'CI/CD и деплой'],
      openQuestions: state === 'discussion' ? ['шаблон: skeleton или full', 'объём функционала после развёртывания'] : [],
      criteria: [
        { id: 'compose', kind: 'verification', text: 'composer.json содержит symfony/framework-bundle 7.x' },
        { id: 'health', kind: 'verification', text: 'GET /health возвращает HTTP 200 с JSON-телом' },
        { id: 'readme', kind: 'manual', text: 'README описывает команды установки и запуска' },
      ],
      sources: [],
      workspace: { mode: 'existing', path: 'C:\\Users\\Rif\\Point\\systemio', isolation: 'snapshot' },
      stack: { id: 'recommended-web', category: 'web', version: '1' },
      routing: { mode: 'fixed', fixedModel: 'Qwen3.8-27B', certification: 'experimental', costKnown: false },
      budget: { tokens: 200000, activeSeconds: 3600, maxSteps: 64, maxParallel: 2 },
      roster: { permanent: [{ id: 'agentdraft-bench', name: 'Разработчик проекта', role: 'Владелец реализации', mission: 'Собрать проект и довести health-эндпоинт до зелёного', requiredTools: ['read_file', 'write_file', 'run_command'], existing: false, requiresConsent: true }], temporary: [] },
      network: [{ host: 'packagist.org:443', purpose: 'Доступ, необходимый для выполнения утверждённого задания' }, { host: 'repo.packagist.org:443', purpose: 'Доступ, необходимый для выполнения утверждённого задания' }],
      secrets: [],
      completion: { id: 'mvp-web', version: '1', checks: [
        { kind: 'acceptance' },
        { kind: 'automated_tests', command: 'vendor/bin/phpunit' },
        { kind: 'service_start', command: 'docker compose up -d --wait' },
        { kind: 'health', command: 'curl -fsS http://localhost:8080' },
      ] },
      delivery: { applyMode: 'automatic', commitMode: 'none', keepPartialDays: 30, keepServicesRunning: true, applicationUrl: 'http://localhost:8080' },
    }
    // Квест в пути: строка с пульсом, этапы наполовину, поток работы агента
    // раскрыт. Состояния квеста расходятся именно между «идёт» и «взят», и без
    // этой страницы первое не мерил никто.
    if (variant === 'work-order-running') {
      order.state = 'approved'
      order.runtime = {
        questId: 'quest-bench', status: 'running', flowRunId: 'flowrun-bench',
        agentIds: ['agentdraft-bench'],
        stages: [
          { id: 'node-scaffold', name: 'Развернуть Symfony', kind: 'agent', status: 'completed', agentId: 'agentdraft-bench', runId: 'run-bench-1' },
          { id: 'node-health', name: 'Добавить health-эндпоинт', kind: 'agent', status: 'running', agentId: 'agentdraft-bench', runId: 'run-bench-2' },
          { id: 'node-verify', name: 'Проверить запуск и README', kind: 'verifier', status: 'pending' },
        ],
      }
    }
    if (state === 'approved') {
      const zero = 0
      order.runtime = {
        questId: 'quest-bench', status: 'completed', message: 'Проверки пройдены, результат перенесён',
        // Утверждённый наряд сворачивает состав и отдаёт место экрану
        // выполнения: этапы приходят вместе с самим нарядом, а созданный
        // исполнитель называется по имени.
        agentIds: ['agentdraft-bench'],
        stages: [
          { id: 'node-scaffold', name: 'Развернуть Symfony', kind: 'agent', status: 'completed', agentId: 'agentdraft-bench', runId: 'run-bench-1' },
          { id: 'node-health', name: 'Добавить health-эндпоинт', kind: 'agent', status: 'completed', agentId: 'agentdraft-bench', runId: 'run-bench-2' },
          { id: 'node-verify', name: 'Проверить запуск и README', kind: 'verifier', status: 'completed', agentId: 'agentdraft-bench', runId: 'run-bench-3' },
        ],
        deliveryReceipt: { id: 'delivery-bench', url: 'http://localhost:8080', commitId: 'a1b2c3d', workspaceRevision: 'sha256:tree', target: 'C:\\Users\\Rif\\Point\\systemio', servicesRunning: true, composeFile: 'compose.yaml' },
        evidence: {
          id: 'evidence-bench', version: 3, workspaceRevision: 'sha256:tree', deliveryTarget: 'C:\\Users\\Rif\\Point\\systemio',
          changedFiles: ['composer.json', 'src/Controller/HealthController.php', 'README.md'], commitIds: ['a1b2c3d'],
          knownLimitations: [],
          // Закрытость условий ядро кладёт сюда — по записи на каждое условие
          // наряда, включая ручное. Ручное остаётся незакрытым даже у взятого
          // квеста: его закрывает человек, а не прогон, и страница обязана
          // показывать именно это, а не круглое «всё зелёное».
          criteria: [
            { criterionId: 'compose', satisfied: true, command: 'composer show symfony/framework-bundle', exitCode: zero },
            { criterionId: 'health', satisfied: true, command: 'curl -fsS http://localhost:8080/health', exitCode: zero },
            { criterionId: 'readme', satisfied: false, summary: 'Требуется ручная приёмка' },
          ],
          verificationChecks: [
            { id: 'completion:automated_tests', kind: 'automated_tests', command: 'vendor/bin/phpunit', exitCode: zero, satisfied: true },
            { id: 'completion:service_start', kind: 'service_start', command: 'docker compose up -d --wait', exitCode: zero, satisfied: true },
            { id: 'completion:health', kind: 'health', command: 'curl -fsS http://localhost:8080', exitCode: zero, satisfied: true },
          ],
          modelCalls: [{ id: 'call-1', provider: 'openai-compatible', model: 'Qwen3.8-27B', inputTokens: 18400, outputTokens: 3100, costCents: 0, costKnown: false }],
        },
      }
    }
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      sessions: { active: 'ship', mode: 'auto', workMode: 'plan', items: [{ id: 'ship', title: 'Symfony-проект' }] },
      history: [
        { id: 'wo-1', role: 'user', content: 'Сделай рабочий Symfony-проект с health-эндпоинтом и README', createdAt: today(14, 45) },
        { id: 'wo-2', role: 'assistant', mode: 'model', model: 'Qwen3.8-27B', content: 'Собрал карточку запуска. Проверьте объём работ и критерии готовности.', createdAt: today(14, 48) },
      ],
      workOrders: [order],
    } } })
    // Согласие на создание исполнителя ушло в свою карточку ленты и меряется
    // отдельной страницей (вариант agent-card). Здесь остаётся сама карточка
    // запуска с запертой кнопкой и причиной рядом с ней.
  } else if (variant === 'empty') {
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator, history: [],
    } } })
  } else if (variant === 'origin') {
    // Происхождение ответа: кто ответил и на чём. Метка была видна только в
    // живом разговоре — ни один стенд её не рисовал, и она годами жила без
    // оформления: браузерный <b> в системном шрифте посреди моноширинного
    // разговора и модель без пробела: «ответила модель мастераQwen3.6-35B».
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      // Разговор идёт второй день: без разных дат разделитель дня не рисуется
      // ни разу, и замер считал бы его зелёным, ни разу не увидев.
      truncated: true,
      history: [
        { id: 'mo-1', role: 'user', content: 'Что можешь сказать по проекту?', createdAt: yesterday(9, 41) },
        {
          id: 'mo-2', role: 'assistant', mode: 'model', model: 'qwen2.5-coder:7b',
          content: 'В ростере один агент — «Разработчик». Активных квестов нет, задач на решение тоже.',
          createdAt: yesterday(9, 42),
          latencyMs: 72400,
          facts: ['агентов в ростере: 1', 'активных квестов: 0', 'отряд по политике: до 3'],
          // Путь к ответу: рассуждение модели и раунды читающих инструментов.
          // Без них в фикстуре новые блоки не рисовались бы ни разу — и замер
          // раскладки считал бы их зелёными, ни разу не увидев.
          reasoning: 'Сначала смотрю, кто есть в ростере: если агент один, отряд собирать не из чего.\nПотом читаю карту проекта — по ней видно, чем он занят и какие модули живые.\nОтвечать про квесты рано: активных нет, и предлагать работу без задачи было бы выдумкой.',
          steps: [
            { round: 1, tool: 'read_file', argument: 'internal/app/master_chat.go', result: 'package app — 389 строк' },
            { round: 1, tool: 'search_code', argument: 'master chat history', result: 'найдено 6 совпадений в 3 файлах' },
            { round: 2, tool: 'read_file', argument: 'docs/api.md', result: 'таблица маршрутов, 151 строка', truncated: true },
            { round: 3, tool: 'project_map', argument: '', result: '{"topDirectories":["internal","vscode-extension"],"symbols":[],"status":{"state":"ready"}}' },
            { round: 3, tool: 'read_quest', argument: 'q-1', result: '{"id":"q-1","status":"active"}' },
            { round: 4, tool: 'list_files', argument: 'internal/orchestrator', result: 'каталог недоступен: путь вне рабочей области', failed: true },
            { round: 4, tool: 'read_changeset', argument: 'cs-1', result: '{"files":2,"status":"pending"}' },
            { round: 4, tool: 'git_log', argument: '-n 20', result: '20 записей' },
          ],
        },
        { id: 'mo-3', role: 'user', content: 'Расскажи про сам проект', createdAt: today(14, 2) },
        {
          id: 'mo-4', role: 'assistant', mode: 'deterministic',
          fallbackReason: 'модель Мастера ответила не по схеме — она вернула обычный текст вместо структуры ответа',
          content: 'Скажу, что сейчас в работе, или перечислю ростер — или опишите задачу, и я предложу квест.',
          createdAt: today(14, 3),
        },
      ],
    } } })
  } else if (variant === 'agent') {
    const action = {
      id: 'hubaction-master', kind: 'create_agent', status: 'pending', title: 'Создать агента · СТРАЖ',
      rationale: 'совпало с задачей: безопасность, API',
      agent: { id: 'agent-draft', blueprintId: 'bp-smith', name: 'СТРАЖ', roleDescription: 'Проверяет безопасность API', mission: 'Находить и закрывать уязвимости до релиза', primaryModel: 'qwen2.5-coder', allowedTools: ['read_file', 'search_code', 'git_diff'] },
    }
    boot.companionActionProposals = [action]
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      history: [
        { id: 'mu-1', role: 'user', content: 'Создай агента для проверки безопасности API' },
        { id: 'ma-1', role: 'assistant', content: 'Подготовил агента СТРАЖ. Проверьте роль и создайте карточку.', actionProposalId: action.id, factsUsed: ['агентов в ростере: 2'] },
      ], response: { actionProposal: action },
    } } })
  } else if (variant === 'team' || variant === 'team-edit') {
    const action = {
      id: 'hubaction-team', kind: 'create_team', status: 'pending', title: 'Создать отряд · ПЛАТЁЖИ',
      rationale: 'подходят к backend-задаче и взаимной проверке',
      team: { id: 'team-draft', name: 'ПЛАТЁЖИ', description: 'Реализация и проверка платёжного API', agentIds: ['forge', 'sage'] },
    }
    boot.companionActionProposals = [action]
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      history: [
        { id: 'mu-1', role: 'user', content: 'Выбери отряд для платёжного API' },
        { id: 'ma-1', role: 'assistant', content: 'Подобрал двух исполнителей. Состав можно изменить перед созданием отряда.', actionProposalId: action.id, factsUsed: ['агентов в ростере: 2'] },
      ], response: { actionProposal: action },
    } } })
    if (variant === 'team-edit') {
      click({ action: 'companion-action-modify', id: action.id })
    }
  } else {
    const proposal = {
      id: 'qp-master', title: 'Укрепить обработчик оплаты', status: 'pending', importance: 'important',
      objectives: ['Воспроизвести отказ вебхука', 'Исправить обработку повторной доставки'],
      constraints: ['Не менять публичный контракт API'],
      definitionOfDone: ['Тест повторной доставки проходит', 'Регресс-набор зелёный'],
      teamAgentIds: ['sage', 'forge'], teamAgentIdsLocked: variant === 'locked', estimateTokens: 31000,
    }
    boot.questProposals = [proposal]
    listeners['window:message']({ data: { type: 'master', master: {
      configured: true, config: boot.orchestrator,
      history: [
        { id: 'mu-1', role: 'user', content: 'Создай квест: укрепить обработчик оплаты' },
        { id: 'ma-1', role: 'assistant', content: 'Собрал отряд и подготовил квест.', proposalId: proposal.id, factsUsed: ['агентов в ростере: 2', 'активных квестов: 1'] },
      ],
      response: {
        proposal, partyWhy: 'предварительный выбор Мастера · состав можно изменить',
        party: [
          { agentId: 'sage', name: 'SAGE-7', role: 'Архитектор', score: 31, matched: ['обработчик'] },
          { agentId: 'forge', name: 'FORGE', role: 'Разработчик бэкенда', score: 44, matched: ['оплаты'] },
        ],
      },
    } } })
    if (variant === 'edit') {
      click({ action: 'quest-proposal-modify', id: proposal.id })
    }
  }
}

// Компаньон в рабочем состоянии. Пустой док опрятен на любой ширине — рвётся
// он на содержимом: диалог, уведомление о применённом действии и список
// предложений живут в одной узкой колонке и делят её высоту между собой.
// Именно это состояние показано в настоящем IDE, и мерить надо его.
if (requestedSurface === 'companion' && seedStep === 'busy') {
  boot.questProposals = [{
    id: 'qp-companion', title: 'Проверь API проекта', status: 'pending', importance: 'normal',
    rationale: 'готово, когда обработчик вебхука отвечает 200 на повторную доставку и регресс-набор зелёный',
    objectives: ['Пройти по маршрутам', 'Свести контракт с документацией'],
    definitionOfDone: ['Повторная доставка идемпотентна', 'Регресс-набор зелёный'],
    teamAgentIds: ['sage'], estimateTokens: 18000,
  }]
  listeners['window:message']({ data: {
    type: 'companionThreadSync',
    messages: [
      { role: 'user', content: 'Коротко: что ты видишь в этом проекте?' },
      { role: 'assistant', content: 'В мире 0 агентов, срочных сигналов нет. Для изменения сформулируйте ожидаемый результат — я подготовлю квест и не запущу его без подтверждения.' },
      { role: 'user', content: 'Создай агента с именем QA-страж для проверки этого проекта' },
      { role: 'assistant', content: 'Последнее: Project Agent · QA-страж готов к найму. Проверьте роль и создайте карточку.' },
    ],
    loading: false,
  } })
  listeners['window:message']({ data: { type: 'companionActionApplied', teamId: 'team-qa', stayInCompanion: true, guildTab: 'teams' } })
}
// Разметка ответа в узкой колонке компаньона: тот же разбор, что у Мастера,
// но колонка в 300 пикселей, и таблица с блоком кода обязаны прокручиваться
// внутри себя, а не раздвигать док.
if (pick(COMPANION_LAYOUTS, requestedSurface) && seedStep === 'markdown') {
  listeners['window:message']({ data: {
    type: 'companionThreadSync',
    messages: [
      { role: 'user', content: 'Как проверить миграцию?' },
      { role: 'assistant', content: [
        '### Проверка', '1. Снять дамп', '2. Прогнать:', '   - `go test ./internal/storage/...`', '- [x] Копия', '- [ ] Стенд',
        '> Без дампа отката нет.', '', '| Таблица | Строк | Комментарий |', '| --- | ---: | --- |', '| users | 12 480 | первой |',
        '```go', 'if err := migrate(ctx, db); err != nil { return fmt.Errorf("миграция: %w", err) }', '```',
        'Подробнее — в [документации](https://www.postgresql.org/docs/).',
      ].join('\n') },
    ],
    loading: false,
  } })
}
const css = ['rpg-tokens.css', 'style.css']
  .map(file => fs.readFileSync(path.join(repo, 'vscode-extension', 'media', file), 'utf8'))
  .join('\n')

// Переменные темы. Без них стенд врал: половина базовых правил читает
// var(--vscode-*), и в браузере они пусты — поля выходили без фона, кнопки
// без цвета. В настоящем вебвью VS Code подставляет туда цвета активной темы,
// поэтому стенд подставляет ровно ту, которую Point ставит по умолчанию.
const themeColors = JSON.parse(fs.readFileSync(
  path.join(repo, 'vscode-extension', 'themes', 'point-dark-color-theme.json'), 'utf8')).colors || {}
const themeVariables = `:root {\n${Object.entries(themeColors)
  .map(([key, value]) => `  --vscode-${key.replace(/\./g, '-')}: ${value};`).join('\n')}\n}`

// Оболочка webview: те же размеры и та же корневая разметка, что в VS Code.
const page = `<!doctype html>
<html lang="ru"><head><meta charset="UTF-8"><title>Point — ${requestedSurface}</title>
<style>
html, body { height: 100%; margin: 0; }
body { font-family: var(--font-ui, system-ui); background: var(--surface-void, #0b0b0d); }
${themeVariables}
${css}
</style></head>
<body data-layout="${requestedLayout}"><div id="root">${root.innerHTML}</div><script>
(() => {
  const rect = selector => {
    const element = document.querySelector(selector)
    if (!element) return null
    const box = element.getBoundingClientRect()
    return { left: Math.round(box.left), right: Math.round(box.right), width: Math.round(box.width), height: Math.round(box.height) }
  }
  const probe = document.createElement('meta')
  probe.name = 'point-layout-probe'
  probe.content = JSON.stringify({
    viewport: { width: innerWidth, height: innerHeight, scrollWidth: document.documentElement.scrollWidth },
    dock: rect('.companion-dock'), header: rect('.companion-dock-brand'), actions: rect('.companion-dock-actions'),
    empty: rect('.companion-empty'), starters: rect('.companion-starters'), starter: rect('.companion-starter'),
    compose: rect('.companion-compose'), send: rect('.companion-send-action'),
  })
  document.head.appendChild(probe)
})()
</script></body></html>
`
const outputArg = process.argv.find(value => value.startsWith('--out='))
const outputPath = outputArg ? outputArg.slice('--out='.length) : process.env.POINT_RENDER_OUT
if (outputPath) fs.writeFileSync(path.resolve(outputPath), page)
else process.stdout.write(page)
