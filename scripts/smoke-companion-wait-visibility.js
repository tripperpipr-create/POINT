// Ожидание ответа должно быть видно, а откат — предсказуем.
//
// Пока модель думает, событий не приходит вовсе: полоса писала «Модель…» и
// замирала. Через минуту молчания она неотличима от зависшего окна, а когда
// ядро сдаётся по таймауту, на вопрос отвечает движок Point — и для человека
// это выглядит подменой без предупреждения.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

// Часы под управлением: секунды ожидания считаются от Date.now(), и без сдвига
// времени проверить можно только нулевую секунду.
function dock() {
  const listeners = {}
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners['root:' + type] = callback },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  let offset = 0
  const RealDate = Date
  const clock = function (...args) { return new RealDate(...args) }
  clock.now = () => RealDate.now() + offset
  clock.prototype = RealDate.prototype
  const context = {
    acquireVsCodeApi: () => ({ postMessage() {}, getState() { return undefined }, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'companion' } } },
    window: { addEventListener(type, callback) { listeners['window:' + type] = callback } },
    console, Date: clock, Map, Set, CSS: { escape: value => String(value) },
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {},
    setTimeout(callback) { callback(); return 0 },
    clearTimeout() {},
  }
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8'), context, { filename: 'media/main.js' })
  const send = payload => listeners['window:message']({ data: payload })
  send({
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'overview',
    boot: {
      questProposals: [], companionActionProposals: [], projectAgents: [], profiles: [], flows: [], skills: [],
      runs: [], toolCatalog: [], connections: [],
      companion: { id: 'c1', preset: 'balanced', configured: true, provider: 'ollama', model: 'qwen' },
      companionMessages: [],
    },
    details: undefined,
  })
  return {
    send,
    advance(seconds) { offset += seconds * 1000 },
    html: () => root.innerHTML,
  }
}

const panel = dock()
panel.send({ type: 'companionChatStarted', requestId: 1, message: 'Разбери сбой сборки' })
check('ожидание видно с первой секунды', panel.html().includes('data-companion-thinking'), 'признака ожидания нет')
check('первые секунды не считаются вслух',
  !/· \d+ с/.test(panel.html()),
  'счётчик появился раньше, чем ожидание стало заметным')

// Через десять секунд молчания счётчик обязан идти: иначе окно выглядит мёртвым.
panel.advance(10)
panel.send({ type: 'companionThreadSync', loading: true, requestId: 1 })
check('счётчик показывает, сколько идёт ожидание',
  panel.html().includes('· 10 с'),
  'секунды ожидания не показаны')
check('раньше порога не пугают откатом',
  !panel.html().includes('дольше обычного'),
  'предупреждение появилось слишком рано')

// К пятидесятой секунде ядро уже близко к своему таймауту: человек должен
// узнать, чем кончится молчание, до того как это случится.
panel.advance(40)
panel.send({ type: 'companionThreadSync', loading: true, requestId: 1 })
check('о долгом ожидании предупреждают заранее',
  panel.html().includes('дольше обычного'),
  'предупреждения о долгом ответе нет')
check('в предупреждении названо, чем кончится молчание',
  panel.html().includes('ответит движок Point'),
  'человеку не сказано, что ответит Point')

// Ответ пришёл — полоса уходит вместе с ожиданием.
panel.send({ type: 'companionChatResult', requestId: 1, response: { reply: 'Падает на тестах.', mode: 'model', model: 'qwen' } })
check('после ответа признак ожидания исчезает',
  !panel.html().includes('дольше обычного') && !panel.html().includes('data-companion-thinking'),
  'ожидание осталось на экране после ответа')

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const item of failures) console.error('  · ' + item)
  process.exit(1)
}
console.log('\nожидание ответа видно, откат предсказуем')
