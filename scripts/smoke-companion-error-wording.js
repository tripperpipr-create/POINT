// Отказ провайдера человек читает как отказ Point.
//
// Ядро отдаёт ответ провайдера как есть — «provider returned 404 Not Found: {…}».
// Без разбора это доезжало до чата сырым JSON: причина не названа, следующий шаг
// не подсказан. Здесь проверяется, что частые отказы объясняются словами и что
// объяснение не путает одну причину с другой: «сменить модель» там, где надо
// подождать, стоит человеку настройки, которую он и не ломал.

const path = require('path')
const { createCompanionController } = require(path.join(__dirname, '..', 'vscode-extension', 'companion-controller.js'))

const controller = createCompanionController({
  vscode: { window: {}, workspace: {}, languages: { getDiagnostics: () => [] } },
  path,
  runGit: async () => ({ stdout: '', stderr: '' }),
  normalizedWorkspaceRoot: () => process.cwd(),
  POINT_WORKSPACE_ROOT_KEY: 'point.workspaceRoot',
  GIT_DEFAULT_LIST: [],
  getActiveView: () => undefined,
  getActiveService: () => undefined,
})

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

const say = (error, model) => controller.formatCompanionChatError(error, model)

// Вставленный лог целиком: ядро отвергает такое сообщение, и «exceeds 32 KiB»
// человеку ничего не подсказывает.
check('слишком длинное сообщение объяснено словами',
  say(new Error('companion message exceeds 32 KiB')).includes('длиннее 32 КБ')
  && say(new Error('companion message exceeds 32 KiB')).includes('прочитает его сам'),
  'отказ по длине остался техническим')

const notFound = say(new Error('provider returned 404 Not Found: {"error":{"message":"model not found"}}'), 'Qwen3.6-35B-A3B')
check('незнакомая модель названа по имени',
  notFound.includes('не знает модель') && notFound.includes('Qwen3.6-35B-A3B'),
  `текст: ${notFound}`)
check('незнакомая модель не выдаётся за проблему с ключом',
  !notFound.includes('токен'),
  `текст: ${notFound}`)

const overflow = say(new Error('provider returned 400: This model maximum context length is 32768 tokens'), 'qwen2.5-coder')
check('переполнение окна ведёт к новому чату, а не к настройке',
  overflow.includes('не поместился') && overflow.includes('короче'),
  `текст: ${overflow}`)

const rate = say(new Error('provider returned 429 Too Many Requests'), 'qwen2.5-coder')
check('ограничение частоты просит подождать',
  rate.includes('частоту') && rate.includes('Подождите'),
  `текст: ${rate}`)
check('ограничение частоты не зовёт менять модель',
  !rate.includes('Выберите'),
  `текст: ${rate}`)

const upstream = say(new Error('provider returned 502 Bad Gateway'), 'qwen2.5-coder')
check('сбой сервиса назван сбоем сервиса',
  upstream.includes('на своей стороне'),
  `текст: ${upstream}`)

const unauthorized = say(new Error('provider returned 401 Unauthorized'), 'qwen2.5-coder')
check('отказ по ключу остался про ключ',
  unauthorized.includes('токен'),
  `текст: ${unauthorized}`)

const timeout = say(new Error('The operation was aborted due to timeout'), 'qwen2.5-coder')
check('таймаут остался про время',
  timeout.includes('не успел'),
  `текст: ${timeout}`)

// Имя модели неизвестно — текст обязан остаться читаемым, а не показывать пустые
// кавычки: помощник отвечает и до того, как настройка загрузилась.
const nameless = say(new Error('provider returned 404: unknown model'), '')
check('без имени модели текст не ломается',
  nameless.includes('не знает модель') && !nameless.includes('«»'),
  `текст: ${nameless}`)

// Незнакомая ошибка не выдумывает причину и остаётся узнаваемой.
const unknown = say(new Error('provider returned 418: I am a teapot'), 'qwen2.5-coder')
check('неизвестный отказ передаётся как есть',
  unknown.startsWith('Компаньон не смог ответить'),
  `текст: ${unknown}`)

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('\nотказ провайдера объясняется словами')
