// Правила композера помощника: во что обязана уложиться реплика человека и что
// он видит, пока ответа нет. Оба числа здесь парные к числам ядра, и сверяют их
// договорённости 22 и 23 в ui/contracts.mjs.

// Тот же предел, что и в ядре (internal/companion/service.go).
export const COMPANION_MESSAGE_LIMIT_BYTES = 32 * 1024

// Порог предупреждения о долгом ответе держится заметно раньше таймаута
// обращения к модели в ядре (internal/companion/model_chat.go): предупреждение
// после отката на местный разбор бессмысленно.
export const COMPANION_SLOW_ANSWER_SECONDS = 45

// Предел ядра считается в байтах, а поле ввода живёт в символах: кириллица
// весит вдвое, и «32 тысячи знаков» прошли бы проверку, не пройдя ядро.
export function companionMessageBytes (value) {
  const text = String(value || '')
  return typeof TextEncoder === 'function' ? new TextEncoder().encode(text).length : text.length
}

// Отказ по длине называет и во что упёрлись, и что с этим делать: у помощника
// есть чтение файлов, и путь к логу он разберёт сам — в отличие от лога,
// вставленного в поле целиком.
export function oversizedCompanionMessageNote (bytes) {
  return `Сообщение не помещается: ${Math.round(bytes / 1024)} КБ при пределе ${COMPANION_MESSAGE_LIMIT_BYTES / 1024} КБ. Сократите его или назовите путь к файлу — помощник прочитает файл сам.`
}

// Пока модель думает, событий не приходит вовсе: строка ожидания замирает, и
// через минуту молчания окно неотличимо от зависшего. Первые секунды не
// считаются вслух, чтобы не мигать на быстрых ответах.
export function companionWaitSuffix (waitedSeconds) {
  const waited = Number(waitedSeconds) || 0
  if (waited < 3) return ''
  const slow = waited >= COMPANION_SLOW_ANSWER_SECONDS
    ? ' · дольше обычного; если модель промолчит, ответит движок Point'
    : ''
  return ` · ${waited} с${slow}`
}

// Подпись под полем: пока ответ идёт, она объясняет, что будет со следующим
// вопросом, а в покое — как отправить, не отправив случайно на переносе строки.
export function companionComposeMetaHtml (loading, pending) {
  if (loading) return pending ? 'Следующий вопрос сохранён' : 'Можно задать следующий вопрос'
  return 'Enter — отправить · Shift+Enter — перенос'
}

// Кнопки композера: во время ответа отправка называется очередью, потому что
// именно это с вопросом и произойдёт, а рядом стоит «Стоп».
export function companionComposeActionsHtml (loading) {
  if (loading) {
    return `<div class="companion-compose-actions"><button type="button" class="secondary companion-stop-action" data-action="stop-companion-chat" title="Остановить ответ" aria-label="Остановить ответ">Стоп</button><button class="primary companion-send-action" type="submit" title="Задать следующий вопрос" aria-label="Задать следующий вопрос">В очередь ↑</button></div>`
  }
  return `<div class="companion-compose-actions"><button class="primary companion-send-action" type="submit" title="Отправить" aria-label="Отправить">Отправить ↑</button></div>`
}

// Порядок шагов и наборы мастера настройки. Это данные, а не разметка: держать
// их рядом с формой мозга удобнее, чем в главном файле, который стоит у своего
// предела строк.
export const COMPANION_SETUP_STEPS = [
  { id: 'brain', label: 'Мозг', hint: 'Модель по API' },
  { id: 'role', label: 'Роль', hint: 'Пресет наставника' },
  { id: 'personality', label: 'Характер', hint: 'Стиль работы' },
  { id: 'skills', label: 'Навыки', hint: 'Практики помощника' },
  { id: 'boundaries', label: 'Границы', hint: 'Совет или действие' },
  { id: 'examples', label: 'Проверка', hint: 'Живые примеры' },
]

export const COMPANION_EXAMPLES = [
  { id: 'logs', icon: '☰', title: 'Логи и проблемы', surface: 'IDE · диагностика', user: 'В «Проблемах» ошибки, в терминале упала сборка.', prompt: 'Что сломано в IDE прямо сейчас? Разбери «Проблемы» и последние неуспешные команды.', result: 'Разбор логов и черновик правки', tone: 'advice' },
  { id: 'code', icon: '✎', title: 'Написание кода', surface: 'Редактор · код', user: 'Добавь Google OAuth.', prompt: 'Помоги спроектировать и написать Google OAuth в текущем стеке.', result: 'План кода или квест на реализацию', tone: 'quest' },
  { id: 'agent', icon: '♙', title: 'Создать агента', surface: 'Чертог · агенты', user: 'Собери агента для бэкенда под этот репозиторий.', prompt: 'Создай агента для бэкенда этого проекта.', result: 'Карточка агента на осмотр', tone: 'agent' },
  { id: 'quest', icon: '⚑', title: 'Поставить квест', surface: 'Чертог · квесты', user: 'Поставь квест на следующее улучшение.', prompt: 'Подготовь проверяемый квест для следующего улучшения проекта.', result: 'Карточка квеста: запустить, изменить или отклонить', tone: 'quest' },
  { id: 'flow', icon: '⇢', title: 'Собрать флоу', surface: 'Чертог · флоу', user: 'Собери безопасный флоу релиза.', prompt: 'Создай флоу для безопасной подготовки релиза.', result: 'Схема флоу на осмотр', tone: 'flow' },
]

// API-only: помощник отвечает только через HTTP-модель. Выбор мозга больше не
// переключает режимы — карточка объясняет требование подключения.
export function companionModeCardsHtml(draft) {
  const ready = draft.mode === 'model' && draft.model
  return `<div class="companion-mode-switch" role="group" aria-label="Режим компаньона"><button type="button" class="companion-mode-seg selected" data-action="companion-select-mode" data-mode="model"><span>✦</span><strong>Модель по API</strong><small>${ready ? 'Подключена' : 'Нужны адрес сервиса и ключ на вкладке «Связи»'}</small></button></div>`
}

// Сцена примера по имени: набор живёт здесь же, и искать в нём удобнее рядом.
export function companionSceneById(id) {
  return COMPANION_EXAMPLES.find(item => item.id === id) || COMPANION_EXAMPLES[0]
}

// Прежние имена шага «Мозг»: настройка помнит их в сохранённом состоянии.
export const COMPANION_SETUP_STEP_ALIAS = { mode: 'brain', connection: 'brain' }

// Расход и пределы под ответом. Показывается только то, что сообщил сам
// исполнитель: у подписки это единственное, что видно про её трату, а
// выдуманные числа хуже отсутствующих.
export function companionSpendCaveats ({ modelWindow, modelCeiling, replyCost, cacheSplit }) {
  const thousands = value => `${Math.round(Number(value) / 1000)}K`
  return [
    modelWindow ? `Окно модели ${thousands(modelWindow)}${modelCeiling ? `, ответ до ${thousands(modelCeiling)}` : ''}` : '',
    replyCost ? `Стоимость ${Number(replyCost) < 10000 ? 'меньше цента' : '$' + (Number(replyCost) / 1000000).toFixed(2)}` : '',
    cacheSplit ? `Из кэша ${Number(cacheSplit).toLocaleString('ru-RU')} токенов` : '',
  ].filter(Boolean)
}

export function applyLocalSourceFields (root) {
  for (const selector of ['.connection-base-url', '.connection-api-key', '.connection-lead-remote']) {
    const node = root.querySelector(selector)
    if (node) node.hidden = false
  }
  const localLead = root.querySelector('.connection-lead-local')
  if (localLead) localLead.hidden = true
}

// Из выбора мозга собирается настройка помощника. Остался один путь — API.
export function companionConfigForBrain(draft, { current = {}, connection, preset } = {}) {
  const providerPreset = connection?.presetId || draft.providerPreset
  const provider = connection?.provider || draft.provider
  return {
    ...current,
    connectionId: draft.connectionId,
    provider,
    providerPreset,
    baseUrl: connection?.baseUrl || draft.baseUrl || preset?.baseUrl || '',
    model: draft.model,
    preset: draft.preset,
    temperature: draft.temperature,
    maxOutputTokens: draft.maxOutputTokens,
    criticality: draft.criticality,
    creativity: draft.creativity,
    verbosity: draft.verbosity,
    initiative: draft.initiative,
    questionStrictness: draft.questionStrictness,
    riskTolerance: draft.riskTolerance,
    autoAct: draft.autoAct,
    autoOpenChatOnCritical: draft.autoOpenChatOnCritical,
    autoSendModelPrompt: draft.autoSendModelPrompt,
    skillIds: draft.skillIds,
  }
}

export function companionBrainMode (companion = {}) {
  return companion.provider && companion.model ? 'model' : 'model'
}

// Единственный вид мозга: сетевая / локальная HTTP-модель.
export const COMPANION_BRAIN_MODES = ['model']

export function normalizeBrainMode (mode) {
  return COMPANION_BRAIN_MODES.includes(mode) ? mode : 'model'
}

// Совместимость: старые вызовы локальной карточки больше не показывают CLI.
export function companionLocalReadyHtml() {
  return `<section class="companion-local-ready"><span>✦</span><div><h3>Нужна модель по API</h3><p>Помощник отвечает только через подключение Ollama, OpenAI-совместимый шлюз, Anthropic или Azure OpenAI.</p><small>Claude Code, Codex и Cursor CLI сняты — выберите связь на вкладке «Связи».</small></div></section>`
}
