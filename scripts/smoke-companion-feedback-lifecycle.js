// Отметки под ответами живут ровно столько, сколько живут сами ответы.
//
// Отметка привязана к идентификатору реплики. Когда разговор уезжает в архив или
// историю очищают, этих реплик больше нет ни в ленте, ни в ядре — а отметки
// оставались и копились до предела в двести записей, мешая новым. При этом в
// архиве, где им самое место, они не показывались вовсе.

const Module = require('module')
const path = require('path')

const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      window: { showErrorMessage() {}, createWebviewPanel: () => ({ webview: {} }), showInformationMessage: () => Promise.resolve() },
      workspace: { getConfiguration: () => ({ get: (_key, fallback) => fallback }), onDidChangeConfiguration: () => ({ dispose() {} }) },
      commands: { registerCommand: () => ({ dispose() {} }), executeCommand: () => Promise.resolve() },
      languages: { getDiagnostics: () => [] },
      Uri: { file: value => ({ fsPath: String(value) }), joinPath: (...parts) => ({ fsPath: parts.join('/') }) },
      EventEmitter: class { constructor() { this.event = () => ({ dispose() {} }) } fire() {} dispose() {} },
      ViewColumn: { Active: 1 },
      env: { openExternal: () => Promise.resolve(true) },
    }
  }
  return originalLoad.call(this, request, parent, isMain)
}

const { __test } = require(path.join(__dirname, '..', 'vscode-extension', 'extension.js'))
const { companionFeedbackMarks, withoutFeedbackFor, attachFeedbackToMessages } = require(path.join(__dirname, '..', 'vscode-extension', 'extension-utils.js'))
Module._load = originalLoad

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

const FEEDBACK_KEY = 'point.companion.feedback.v1'
const store = new Map()
const provider = {
  context: {
    workspaceState: {
      get: (key, fallback) => (store.has(key) ? store.get(key) : fallback),
      update: (key, value) => { store.set(key, value); return Promise.resolve() },
    },
  },
  companionFeedbackRecords: __test.AgentViewProvider.prototype.companionFeedbackRecords,
  forgetCompanionFeedback: __test.AgentViewProvider.prototype.forgetCompanionFeedback,
}

store.set(FEEDBACK_KEY, [
  { messageId: 'msg-1', value: 'up', content: 'первый ответ' },
  { messageId: 'msg-2', value: 'down', content: 'второй ответ' },
  { messageId: 'msg-live', value: 'up', content: 'ответ в текущей ленте' },
])

async function main() {
  const marks = companionFeedbackMarks(provider.companionFeedbackRecords())
  check('отметки отдаются интерфейсу компактно',
    marks.length === 3 && marks.every(item => item.messageId && (item.value === 'up' || item.value === 'down')),
    JSON.stringify(marks))

  // Ушедший в архив разговор забирает свои отметки; чужие остаются нетронутыми.
  await provider.forgetCompanionFeedback([{ id: 'msg-1' }, { id: 'msg-2' }])
  const left = store.get(FEEDBACK_KEY)
  check('отметки исчезнувших реплик удалены',
    left.length === 1 && left[0].messageId === 'msg-live',
    JSON.stringify(left))

  // Реплики без идентификатора не должны сносить чужие отметки.
  await provider.forgetCompanionFeedback([{ content: 'без id' }, {}])
  check('реплики без идентификатора ничего не сносят',
    store.get(FEEDBACK_KEY).length === 1,
    JSON.stringify(store.get(FEEDBACK_KEY)))

  // Пустое состояние расширения не роняет разбор: снимок просят раньше готовности.
  const bare = {
    context: {},
    companionFeedbackRecords: __test.AgentViewProvider.prototype.companionFeedbackRecords,
  }
  check('без готового состояния отметок просто нет',
    companionFeedbackMarks(bare.companionFeedbackRecords()).length === 0,
    'бросил исключение или вернул мусор')

  // Архив забирает отметки вместе с репликами: только так они остаются видны.
  const archived = attachFeedbackToMessages(
    [{ id: 'msg-live', role: 'assistant', content: 'ответ в текущей ленте' }, { id: 'msg-x', role: 'assistant', content: 'без отметки' }],
    provider.companionFeedbackRecords(),
  )
  check('архив уносит отметку с репликой',
    archived[0].feedback === 'up' && archived[1].feedback === undefined,
    JSON.stringify(archived))
  check('фильтр отметок не трогает чужие записи',
    withoutFeedbackFor([{ messageId: 'a', value: 'up' }, { messageId: 'b', value: 'down' }], [{ id: 'a' }]).length === 1,
    'фильтр снёс лишнее')

  // В архиве отметка видна: там она и нужна — чтобы вернуться и понять, что было не так.
  const html = __test.AgentViewProvider.prototype.companionDocument.call(provider, 'Архив', 'только чтение', [
    { role: 'user', content: 'Почему падает сборка?' },
    { role: 'assistant', content: 'Смотри логи ядра.', feedback: 'down' },
    { role: 'assistant', content: 'Вот точная причина.', feedback: 'up' },
  ])
  check('в архиве видно, что ответ не помог', html.includes('отмечено: не помогло'), 'в документе нет отметки')
  check('в архиве видно и полезный ответ', html.includes('отмечено: полезно'), 'в документе нет отметки')
  check('реплики без отметки её не получают',
    (html.match(/отмечено/g) || []).length === 2,
    `отметок в документе: ${(html.match(/отмечено/g) || []).length}`)
}

main().then(() => {
  if (failures.length) {
    console.error('\n' + 'ПРОВАЛЕНО:')
    for (const item of failures) console.error('  · ' + item)
    process.exit(1)
  }
  console.log('\n' + 'отметки живут вместе со своими ответами')
}, error => {
  console.error(error)
  process.exit(1)
})
