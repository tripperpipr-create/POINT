// Отказ ядра не оставляет настройку в вечной «загрузке» и не заставляет её врать.
//
// Годность персонажа и политика мастера спрашиваются у ядра отдельными
// запросами. Ключ запроса клали в набор «уже спросили» и снимали оттуда только
// приходом ответа. При отказе ответа не бывает — ключ оставался ждать вечно, и
// повторить было нечем: повтор блокировал сам себя.
//
// На экране это выглядело так. Карточка мастера навсегда оставалась с подписью
// «Политика считается ядром…». А готовность персонажа при неполученном ответе
// намеренно не отрицается — «не наказывать за сетевой ход», — и потому карточка
// навсегда объявляла «Персонаж готов к квесту» по данным, которых никто не
// присылал. Второе хуже первого: заглушка честна хотя бы формой, а «готов» —
// это утверждение о непроверенном.

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

const profile = {
  id: 'agent-1', name: 'Разведчик', provider: 'ollama', model: 'qwen',
  allowedTools: ['read_file'], maxSteps: 30, approvalMode: 'safe',
}
const masterDraft = {
  preset: 'custom', mode: 'local', planningDepth: 50, parallelism: 50,
  approvalStrictness: 50, teamPreference: 50, provider: '', model: '',
}

// Первый спрос уходит в ядро — до ответа обе поверхности честно ждут.
context.profileReadiness(profile)
context.orchestratorPolicyLines(masterDraft)
const asked = posted.filter(item => item.type === 'agentCapability' || item.type === 'orchestratorPolicy')
check('запросы к ядру уходят', asked.length === 2, `ушло запросов: ${JSON.stringify(asked.map(i => i.type))}`)
check('до ответа мастер честно ждёт',
  /считается ядром/.test(context.orchestratorPolicyLines(masterDraft).join(' ')),
  `ожидали заглушку ожидания, получили ${JSON.stringify(context.orchestratorPolicyLines(masterDraft))}`)

// Ядро отказало — ответов на оба запроса не будет никогда.
listeners['window:message']({ data: { type: 'error', message: 'ядро не отвечает', request: 'agentCapability' } })

const afterRefusal = context.orchestratorPolicyLines(masterDraft).join(' ')
check('мастер перестаёт обещать расчёт',
  !/считается ядром/.test(afterRefusal) && /не удалось/.test(afterRefusal),
  `после отказа мастер говорит: ${JSON.stringify(afterRefusal)}`)

const readiness = context.profileReadiness(profile)
check('готовность названа неизвестной, а не готовой',
  readiness.unknown === true,
  `после отказа готовность: ${JSON.stringify({ ready: readiness.ready, unknown: readiness.unknown })}`)

const banner = context.readinessBanner(profile)
check('карточка не заявляет «готов» за ядро',
  /Готовность неизвестна/.test(banner) && !/готов к квесту/.test(banner),
  `баннер: ${JSON.stringify(banner.slice(0, 200))}`)

// Чек-лист найма стоит на том же экране и говорит о том же. Он утверждал
// «Можно нанимать и сразу открыть квест» — по ответу, которого не было.
const checklist = context.hireLiveChecklist(profile)
check('чек-лист найма не зовёт нанимать за ядро',
  /готовность неизвестна/i.test(checklist) && !/Можно нанимать/.test(checklist),
  `чек-лист: ${JSON.stringify(checklist.slice(0, 240))}`)

// Повторных запросов после отказа быть не должно: постоянный отказ превратился
// бы в бесконечный поток. Ждём человека, а не крутим цикл.
const before = posted.length
context.profileReadiness(profile)
context.orchestratorPolicyLines(masterDraft)
check('отказ не запускает поток повторов',
  posted.length === before,
  `после отказа ушло ещё ${posted.length - before} запросов`)

// Ядро ответило — прежний отказ перестаёт быть приговором.
listeners['window:message']({
  data: {
    type: 'agentCapability',
    key: context.agentCapabilityKeyFor(profile),
    capability: { blockers: [], blocking: [], warnings: [], lines: ['Сможет: читать код.'], canRead: true },
  },
})
const revived = context.profileReadiness(profile)
check('после ответа ядра готовность снова считается',
  revived.unknown !== true && revived.ready === true,
  `готовность после ответа: ${JSON.stringify({ ready: revived.ready, unknown: revived.unknown })}`)

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('\nотказ ядра не оставляет настройку в загрузке и не даёт ей врать')
