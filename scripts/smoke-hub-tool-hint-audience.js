// Человеку показывают советы для человека, а не указания агенту.
//
// Подсказка инструмента в ядре пишется в одно поле для двух адресатов: большая
// часть — указания модели, что сделать иначе («pass a single JSON object whose
// keys match the tool schema»), меньшая — советы человеку («сохраните профиль в
// Гильдии → Базы данных»). Лента прогона показывала обе, и человек читал
// инструкции агенту — на чужом языке и о том, чего он не делает.
//
// Различить их можно только по языку, и это признак наблюдаемый, а не
// гарантированный: проверяем поведение здесь, а само соглашение — сверкой
// договорённостей.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

const context = {
  acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
  document: {
    getElementById: () => ({ innerHTML: '', addEventListener() {}, querySelector: () => null, querySelectorAll: () => [] }),
    body: { dataset: {} },
  },
  window: { addEventListener() {} },
  console, Date, Map, Set,
  requestAnimationFrame(cb) { cb(); return 0 },
  cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
}
vm.runInNewContext(`${main}\nthis.__toolFailureText = toolFailureText`, context, { filename: 'main.js' })
const toolFailureText = context.__toolFailureText

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

if (typeof toolFailureText !== 'function') {
  console.log('toolFailureText не найден — проверка прошла бы вхолостую')
  process.exit(1)
}

// Совет человеку обязан дойти: он объясняет, что именно сделать в Хабе.
const human = toolFailureText('db_query', {
  code: 'not_found', message: 'profile missing',
  hint: 'сохраните профиль в Гильдии → Базы данных',
})
check('совет человеку показан', human.includes('Гильдии → Базы данных'), human)

// Указание агенту показывать не нужно — человек ему не адресат.
const agent = toolFailureText('search_code', {
  code: 'invalid_input', message: 'bad query',
  hint: 'retry search_code with max_chunks between 1 and 20',
})
check('указание агенту не показано', !agent.includes('max_chunks'), agent)

// Защита от холостого хода: сам отказ обязан быть объяснён, иначе «английского
// нет» будет верно просто потому, что нет ничего.
check('отказ всё равно объяснён', agent.length > 10 && /[а-яё]/i.test(agent), agent)

// Известный код переводится независимо от подсказки.
const denied = toolFailureText('run_command', {
  code: 'command_denied', message: 'blocked',
  hint: 'use a non-interactive local test, build, lint, or inspection command',
})
check('известный код объяснён по-русски', /Команда отклонена/.test(denied), denied)
check('английская подсказка не приклеена', !denied.includes('non-interactive'), denied)

if (failures.length) {
  console.log('АДРЕСАТ ПОДСКАЗКИ — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('подсказки показываются по адресату: PASS')
