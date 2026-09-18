// Конструктор не теряет заполненное молча.
//
// Сохранение идёт двумя путями. Hub-путь кладёт карточку целиком. Legacy-путь
// знает только старый профиль: личность, миссия, ограничения, навыки и
// проектные правила в него не помещаются — пять шагов настройки из десяти.
//
// Выбор между путями делался по наличию ключей blueprints и projectAgents в
// bootstrap, а оба стояли с omitempty: пустой ростер исчезал из ответа, и
// «агентов пока нет» становилось неотличимо от «ядро старое». Ядро теперь эти
// ключи присылает всегда (internal/app/hub_bootstrap_test.go), а интерфейс,
// если их всё же нет, называет потерю до сохранения, а не после.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const listeners = {}
const posted = []
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector() { return null },
  querySelectorAll() { return [] },
}
const context = {
  acquireVsCodeApi: () => ({
    postMessage(message) { posted.push(message) },
    getState() { return undefined },
    setState() { },
  }),
  document: { getElementById: id => id === 'root' ? root : undefined, body: { dataset: { layout: 'wide' } } },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set,
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() { },
  setTimeout(callback) { callback(); return 0 },
  clearTimeout() { },
}

const source = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')
vm.runInNewContext(source, context, { filename: 'media/main.js' })

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

const draft = {
  name: 'Разведчик', personality: 'дотошный', mission: 'держать биллинг рабочим',
  constraints: ['не трогать миграции'], skillIds: ['skill-code-review'],
  projectRules: ['сначала тесты'], allowedTools: ['read_file'], maxSteps: 30,
}

// Ядро назвало свои коллекции — Hub на месте, теряться нечему.
listeners['window:message']({
  data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'agents',
    boot: { blueprints: [], projectAgents: [], profiles: [], skills: [], runs: [], toolCatalog: [] },
    details: undefined,
  },
})
check('пустой ростер — это ответ, а не старое ядро',
  context.hubModeAvailable() === true,
  'пустые blueprints/projectAgents приняты за отсутствие поддержки Hub')
check('при живом Hub ничего не теряется',
  context.legacySaveWouldDrop(draft) === '',
  `предупреждение не к месту: ${JSON.stringify(context.legacySaveWouldDrop(draft))}`)

// Ядро о Hub не сообщило — потеря обязана быть названа до сохранения.
listeners['window:message']({
  data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'agents',
    boot: { profiles: [], skills: [], runs: [], toolCatalog: [] },
    details: undefined,
  },
})
const warning = context.legacySaveWouldDrop(draft)
check('старое ядро распознано', context.hubModeAvailable() === false, 'ключей нет, но Hub считается доступным')
check('потеря названа поимённо',
  ['характер', 'миссия', 'ограничения', 'навыки', 'проектные правила'].every(word => warning.includes(word)),
  `предупреждение: ${JSON.stringify(warning)}`)
check('пустую карточку зря не пугаем',
  context.legacySaveWouldDrop({ name: 'Пустой', allowedTools: [], maxSteps: 30 }) === '',
  'предупреждение показано там, где терять нечего')

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('\nконструктор не теряет заполненное молча')
