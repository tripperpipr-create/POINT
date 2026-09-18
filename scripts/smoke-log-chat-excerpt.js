// Вопрос о логах разбирается по тем строкам, о которых спросили.
//
// Раньше в запрос уходили последние тридцать ошибок и предупреждений независимо
// от вопроса: «почему упал запрос req_…» приводил к разбору чужих строк, потому
// что нужные были уровнем info или старше последних ошибок. Здесь проверяется,
// что совпавшие строки попадают вместе с соседями, что общая картина не
// пропадает, и что порядок остаётся хронологическим — переставленный лог
// читается как другая история.

const path = require('path')
const { selectLogExcerptLines, logQuestionTerms } = require(path.join(__dirname, '..', 'vscode-extension', 'extension-utils.js'))

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

const line = (time, level, message, source = 'core') => ({ time, level, message, source })
const lines = [
  line('17:32:02', 'info', 'workspace opened cf-bitrix'),
  line('17:32:05', 'info', 'index build start root=cf-bitrix'),
  line('17:32:15', 'info', 'index build done files=511 chunks=4780 partial=true'),
  line('17:33:44', 'info', 'companion chat start request_id=req_mto8w6b78hbgd8'),
  line('17:33:45', 'info', 'companion tool call request_id=req_mto8w6b78hbgd8 tool=git_log'),
  line('17:35:14', 'error', 'companion model stream failed request_id=req_mto8w6b78hbgd8 error=context canceled'),
  line('17:36:01', 'warning', 'index partial limit_reason=entries'),
  line('17:40:00', 'error', 'ssh probe failed host=build-01'),
  line('17:41:00', 'error', 'docker daemon unreachable'),
]

const messages = rows => rows.map(row => row.message)

// Вопрос про конкретный запрос: его строки обязаны быть в выдержке, включая
// начало разговора — по одной строке об обрыве причину не восстановить.
const byRequest = selectLogExcerptLines(lines, 'почему упал запрос req_mto8w6b78hbgd8?', { maxLines: 6 })
check('строки запроса попали в выдержку',
  messages(byRequest).some(text => text.includes('companion chat start')) &&
  messages(byRequest).some(text => text.includes('model stream failed')),
  JSON.stringify(messages(byRequest)))
check('соседняя строка взята как контекст',
  messages(byRequest).some(text => text.includes('tool call')),
  JSON.stringify(messages(byRequest)))

// Вопрос про индекс: строки уровня info раньше не попадали вовсе — они не
// ошибки, а спрашивают именно про них.
const byIndex = selectLogExcerptLines(lines, 'что было с индексом проекта?', { maxLines: 6 })
check('информационные строки по теме доходят',
  messages(byIndex).some(text => text.includes('index build done')),
  JSON.stringify(messages(byIndex)))

// Общий вопрос без совпадений: остаются свежие ошибки, а не случайные строки.
const general = selectLogExcerptLines(lines, 'всё ли в порядке?', { maxLines: 3 })
check('без совпадений показываются свежие ошибки',
  messages(general).every(text => text.includes('failed') || text.includes('unreachable') || text.includes('partial')),
  JSON.stringify(messages(general)))

// Порядок строк — хронологический, каким бы ни был порядок совпадений.
const ordered = selectLogExcerptLines(lines, 'docker и индекс', { maxLines: 8 })
const times = ordered.map(row => row.time)
check('порядок остаётся хронологическим',
  times.join(',') === [...times].sort().join(','),
  JSON.stringify(times))

// Ни одна строка не повторяется: совпадение и «свежая ошибка» — часто одна и та
// же строка, и дубль в выдержке модель читает как два события.
const dedup = selectLogExcerptLines(lines, 'ssh probe failed', { maxLines: 9 })
check('строки не дублируются',
  new Set(messages(dedup)).size === dedup.length,
  JSON.stringify(messages(dedup)))

// Пустой лог не ломает разбор: помощник должен ответить, что смотреть нечего.
check('пустой лог отдаёт пустую выдержку',
  selectLogExcerptLines([], 'что случилось?', { maxLines: 5 }).length === 0,
  'пустой лог вернул строки')

// Служебные слова вопроса не превращаются в поисковые термины, иначе совпадает
// каждая строка и выборка снова становится случайной.
check('служебные слова отброшены',
  !logQuestionTerms('что в логах Point?').includes('логах'),
  JSON.stringify(logQuestionTerms('что в логах Point?')))

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const item of failures) console.error('  · ' + item)
  process.exit(1)
}
console.log('\nвыдержка лога собирается под вопрос')
