const fs = require('fs')
const path = require('path')

// Keep decision resolution on a closed set of server routes. IDs use a safe
// alphabet so traversal or an injected slash cannot select another endpoint.
const DECISION_RESOLVE_ROUTES = [
  /^\/api\/approvals\/[A-Za-z0-9_-]+\/resolve$/,
  /^\/api\/change-sets\/[A-Za-z0-9_-]+\/(apply|reject|resolve)$/,
  /^\/api\/flow-runs\/[A-Za-z0-9_-]+\/nodes\/[A-Za-z0-9_-]+\/resolve$/,
  /^\/api\/quest-proposals\/decide$/,
  /^\/api\/companion\/actions\/decide$/,
  /^\/api\/egress-asks\/[A-Za-z0-9_-]+\/resolve$/,
]

function decisionResolvePath(value) {
  const candidate = String(value || '')
  return DECISION_RESOLVE_ROUTES.some(pattern => pattern.test(candidate)) ? candidate : ''
}

function isQuietApiRoute(method, route) {
  const pathName = String(route || '').split('?')[0]
  if (pathName === '/api/health' || pathName === '/api/events') return true
  if (method === 'POST' && (pathName === '/api/ide/observations' || pathName === '/api/index/update' || pathName === '/api/index/invalidate' || pathName === '/api/project-agents/capability' || pathName === '/api/project-agents/capability-delta')) return true
  if (method === 'GET' && (pathName === '/api/bootstrap' || pathName === '/api/state/runtime' || pathName === '/api/state/guild' || pathName === '/api/companion/live' || pathName === '/api/index/status' || pathName === '/api/runs' || pathName === '/api/decisions' || pathName === '/api/master/history' || pathName === '/api/workflow-runs')) return true
  return false
}

function sharedPointStoragePath(context) {
  const current = path.resolve(context.globalStorageUri.fsPath)
  const userRoot = /^(.*[\\/]User)(?:[\\/].*)?$/i.exec(current)?.[1]
  return userRoot
    ? path.join(userRoot, 'globalStorage', 'local-agent.local-agent-workbench')
    : current
}

function normalizedWorkspaceRoot(value) {
  const resolved = path.resolve(String(value || ''))
  return process.platform === 'win32' ? resolved.toLowerCase() : resolved
}

function processIsAlive(pid) {
  const target = Number(pid)
  if (!Number.isInteger(target) || target <= 0) return false
  try {
    process.kill(target, 0)
    return true
  } catch (error) {
    return error?.code === 'EPERM'
  }
}

function removeFileIfExists(file) {
  try { fs.unlinkSync(file) } catch (error) { if (error?.code !== 'ENOENT') throw error }
}

function readJsonFile(file) {
  try { return JSON.parse(fs.readFileSync(file, 'utf8')) } catch { return undefined }
}

function upsertById(items, value) {
  const source = Array.isArray(items) ? items : []
  if (!value || typeof value !== 'object' || !String(value.id || '')) return source.slice()
  const index = source.findIndex(item => String(item?.id || '') === String(value.id))
  if (index < 0) return [value, ...source]
  const next = source.slice()
  next[index] = value
  return next
}

function removeById(items, id) {
  const target = String(id || '')
  return (Array.isArray(items) ? items : []).filter(item => String(item?.id || '') !== target)
}

// Какие строки лога уходят в вопрос о логах.
//
// Раньше это были последние тридцать ошибок и предупреждений — независимо от
// того, о чём спросили. Вопрос «почему упал запрос req_abc» или «что было с
// индексом» приводил к разбору чужих строк: нужные оказывались либо уровнем
// info, либо старше тридцати последних ошибок, и помощник рассуждал о том, что
// видел, а не о том, что спрашивали.
//
// Поэтому строки, совпавшие с вопросом, берутся вместе с соседями — одна строка
// лога редко объясняет себя сама, — а свежие ошибки остаются в остатке: общая
// картина нужна и тогда, когда спрашивают о частном. Порядок всегда
// хронологический: переставленный лог читается как другая история.
const LOG_QUESTION_STOPWORDS = new Set([
  'что', 'как', 'почему', 'зачем', 'когда', 'где', 'это', 'этот', 'эта', 'the', 'and', 'why',
  'логи', 'логах', 'лога', 'логе', 'log', 'logs', 'point', 'ошибка', 'ошибки', 'error',
])

// Вопрос задают по-русски, а лог ядра пишется по-английски: «что было с
// индексом» не совпадает ни с одной строкой про index. Без этой таблицы поиск по
// вопросу молча вырождался в «последние ошибки» — то самое поведение, от
// которого уходили. Пары даны основой слова, чтобы падежи ловились сами.
const LOG_TERM_SYNONYMS = [
  ['индекс', 'index'], ['запрос', 'request'], ['модел', 'model'], ['ошиб', 'error'],
  ['сборк', 'build'], ['собра', 'build'], ['ветк', 'branch'], ['коммит', 'commit'],
  ['памят', 'memory'], ['терминал', 'terminal'], ['квест', 'quest'], ['агент', 'agent'],
  ['помощник', 'companion'], ['компаньон', 'companion'], ['подключен', 'connection'],
  ['токен', 'token'], ['ключ', 'key'], ['файл', 'file'], ['проект', 'workspace'],
  ['папк', 'workspace'], ['баз', 'database'], ['докер', 'docker'], ['таймаут', 'timeout'],
  ['сет', 'network'], ['навык', 'skill'], ['инструмент', 'tool'], ['поиск', 'search'],
  ['запуск', 'run'], ['старт', 'start'], ['падени', 'failed'], ['упал', 'failed'],
]

function logQuestionTerms(question) {
  const raw = String(question || '').toLowerCase().match(/[\p{L}\p{N}_./:-]{3,}/gu) || []
  const terms = []
  const add = value => {
    if (!value || terms.includes(value) || terms.length >= 16) return
    terms.push(value)
  }
  for (const term of raw) {
    if (LOG_QUESTION_STOPWORDS.has(term)) continue
    add(term)
    for (const [russian, english] of LOG_TERM_SYNONYMS) {
      if (term.startsWith(russian)) add(english)
    }
    if (terms.length >= 16) break
  }
  return terms
}

function selectLogExcerptLines(lines, question, options = {}) {
  const rows = Array.isArray(lines) ? lines : []
  if (!rows.length) return []
  const maxLines = Math.max(4, Number(options.maxLines) || 30)
  const terms = logQuestionTerms(question)
  const text = row => `${row?.time || ''} ${row?.level || ''} ${row?.source || ''} ${row?.message || ''}`.toLowerCase()
  const chosen = new Set()
  if (terms.length) {
    for (let index = 0; index < rows.length; index += 1) {
      const haystack = text(rows[index])
      if (!terms.some(term => haystack.includes(term))) continue
      // Соседи — контекст: причина обычно стоит строкой выше, следствие ниже.
      for (const near of [index - 1, index, index + 1]) {
        if (near >= 0 && near < rows.length) chosen.add(near)
      }
      if (chosen.size >= maxLines) break
    }
  }
  const isLoud = row => row?.level === 'error' || row?.level === 'warning'
  for (let index = rows.length - 1; index >= 0 && chosen.size < maxLines; index -= 1) {
    if (isLoud(rows[index])) chosen.add(index)
  }
  for (let index = rows.length - 1; index >= 0 && chosen.size < maxLines; index -= 1) {
    chosen.add(index)
  }
  return [...chosen].sort((left, right) => left - right).map(index => rows[index])
}

// «Сведения об ответе» показывают факты контекста так, как их собрало ядро:
// строками вида «memories=6 selected=3». Это точный список, но проверить по нему
// ответ трудно — а окно существует ровно для проверки. Здесь ключевые факты
// переводятся на язык человека; сырой список остаётся рядом, потому что перевод
// знает не про всё, а терять непереведённое нельзя.
// Стоимость приходит в стотысячных долях доллара: складывать деньги из дробных
// чисел значит однажды разойтись с суммой по строкам. Человеку показываются
// центы, а совсем мелкое — как «меньше цента», потому что «$0.00» читается как
// «бесплатно».
function companionCostWords (microUsd) {
  if (!Number.isFinite(microUsd) || microUsd <= 0) return 'не сообщена'
  if (microUsd < 10000) return 'меньше цента'
  return `$${(microUsd / 1000000).toFixed(2)}`
}

// Пределы модели в тысячах: «200000 токенов» человек читает дольше, чем «200K».
function companionTokenWords (tokens) {
  if (!Number.isFinite(tokens) || tokens <= 0) return 'неизвестно'
  if (tokens >= 1000) return `${Math.round(tokens / 1000)}K токенов`
  return `${tokens} токенов`
}

// Возраст снимка словами: «14 минут» человек читает медленнее, чем «14 мин»,
// а в списке фактов важна не точность до секунды, а порядок величины.
function companionAgeWords (minutes) {
  if (!Number.isFinite(minutes) || minutes < 0) return 'неизвестно когда'
  if (minutes < 1) return 'только что'
  if (minutes < 60) return `${Math.round(minutes)} мин назад`
  if (minutes < 60 * 24) return `${Math.round(minutes / 60)} ч назад`
  return `${Math.round(minutes / (60 * 24))} дн. назад`
}

const COMPANION_FACT_LABELS = [
  ['gatherMode', 'Сбор контекста', value => (value === 'lean' ? 'сокращённый' : 'полный')],
  ['memories', 'Память проекта', (value, facts) => {
    const selected = facts.selected
    return selected === undefined ? `${value} записей` : `использовано ${selected} из ${value}`
  }],
  ['toolsUsed', 'Помощник смотрел', value => String(value).split(',').filter(Boolean).join(', ')],
  ['toolFailures', 'Посмотреть не удалось', value => String(value).split(',').filter(Boolean).join(', ')],
  ['toolsTruncated', 'Показано частично', value => String(value).split(',').filter(Boolean).join(', ')],
  ['replyTruncated', 'Ответ', () => 'оборван на пределе длины'],
  ['replyLimitTokens', 'Предел ответа', value => `${value} токенов`],
  ['cacheReadTokens', 'Прочитано из кэша', value => `${Number(value).toLocaleString('ru-RU')} токенов`],
  ['cacheWriteTokens', 'Записано в кэш', value => `${Number(value).toLocaleString('ru-RU')} токенов`],
  ['replyCostMicroUsd', 'Стоимость ответа', value => companionCostWords(Number(value))],
  ['modelContextWindow', 'Окно модели', value => companionTokenWords(Number(value))],
  ['modelMaxOutput', 'Потолок ответа модели', value => companionTokenWords(Number(value))],
  ['codeContext', 'Фрагменты кода', value => String(value).split(',').filter(Boolean).join(', ')],
  ['codeContextDropped', 'Отсеяно как не по теме', value => `${value} фрагм.`],
  ['projectIndexPartial', 'Индекс неполон', value => `остановлен на пределе: ${value}`],
  ['projectIndexAgeMinutes', 'Снимок индекса', value => companionAgeWords(Number(value))],
  ['projectIndexFiles', 'Индекс проекта', (value, facts) => (facts.symbols === undefined ? `${value} файлов` : `${value} файлов, ${facts.symbols} символов`)],
  ['ideDiagnosticErrors', 'Ошибок в IDE', value => String(value)],
  ['ideDiagnosticWarnings', 'Предупреждений в IDE', value => String(value)],
  ['ideFailedCommands', 'Неуспешных команд', value => String(value)],
  ['pendingChangeSets', 'Наборов изменений на ревью', value => String(value)],
  ['activeQuests', 'Активных квестов', value => String(value)],
  ['activeExecutions', 'Идущих запусков', value => String(value)],
  ['connectedProviders', 'Подключённых провайдеров', value => String(value)],
  ['usageCurrentMonthRecords', 'Запросов за месяц', value => String(value)],
]

function companionFactPairs(facts) {
  // Пары идут вперемешку: в одной строке и «gatherMode=full; activeQuests=0», и
  // «memories=6 selected=3». Разбор по разделителю ловил бы только первую пару.
  const flat = {}
  for (const entry of Array.isArray(facts) ? facts : []) {
    for (const match of String(entry || '').matchAll(/([A-Za-z][A-Za-z0-9_]*)=([^\s\s;]+)/g)) {
      if (flat[match[1]] === undefined) flat[match[1]] = match[2].trim()
    }
  }
  const pairs = []
  for (const [key, label, format] of COMPANION_FACT_LABELS) {
    if (flat[key] === undefined || flat[key] === '') continue
    const value = format(flat[key], flat)
    if (value) pairs.push([label, value])
  }
  return pairs
}

// Отметки под ответами: «полезно» и «не помогло».
//
// Они привязаны к идентификатору реплики и живут ровно столько, сколько живёт
// сам ответ. Разговор уезжает в архив — отметки уезжают с ним; историю очищают —
// исчезают и они. Иначе записи копятся до предела и мешают новым, указывая на
// сообщения, которых больше нет ни в ленте, ни в ядре.
function companionFeedbackMarks(records) {
  return (Array.isArray(records) ? records : [])
    .filter(item => item && item.messageId)
    .slice(0, 200)
    .map(item => ({ messageId: String(item.messageId), value: item.value === 'down' ? 'down' : 'up' }))
}

function withoutFeedbackFor(records, messages) {
  const gone = new Set((Array.isArray(messages) ? messages : []).map(item => String(item?.id || '')).filter(Boolean))
  if (!gone.size) return Array.isArray(records) ? records : []
  return (Array.isArray(records) ? records : []).filter(item => !gone.has(String(item?.messageId || '')))
}

function attachFeedbackToMessages(messages, records) {
  const marks = new Map(companionFeedbackMarks(records).map(item => [item.messageId, item.value]))
  return (Array.isArray(messages) ? messages : []).map(item => {
    const mark = item?.id ? marks.get(String(item.id)) : undefined
    return mark ? { ...item, feedback: mark } : item
  })
}

module.exports = {
  decisionResolvePath,
  companionFeedbackMarks,
  withoutFeedbackFor,
  attachFeedbackToMessages,
  companionFactPairs,
  logQuestionTerms,
  selectLogExcerptLines,
  isQuietApiRoute,
  sharedPointStoragePath,
  normalizedWorkspaceRoot,
  processIsAlive,
  removeFileIfExists,
  readJsonFile,
  upsertById,
  removeById,
}
