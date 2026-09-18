// Онбординг не закрывается сам.
//
// Настройку системных агентов открывают из Чертога уже после первого запуска, и
// вкладку интерфейс переключал у себя молча: расширение продолжало считать, что
// человек на «Обзоре». Любое состояние оттуда — нажали «перестроить индекс»,
// начали проверять связь с моделью, сработал фоновый опрос — возвращало прежнюю
// вкладку, и настройка Мастера закрывалась на середине.
//
// Проверяем оба конца: интерфейс сообщает о переходе, и пришедшее состояние с
// вкладкой «onboarding» его не выбрасывает.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 240)}`)
}

const boot = {
  onboarded: true, profiles: [], projectAgents: [], blueprints: [], usageRecords: [], runs: [], quests: [],
  executions: [], changeSets: [], questProposals: [], serverProfiles: [], dbConnections: [], skills: [],
  connections: [{ id: 'c1', provider: 'ollama', presetId: 'ollama', displayName: 'Ollama', baseUrl: 'http://127.0.0.1:11434', status: 'connected' }],
  providerCatalog: [{ id: 'ollama', name: 'Ollama', kind: 'ollama', baseUrl: 'http://127.0.0.1:11434', local: true }],
  modelCatalog: [], indexStatus: { state: 'ready', files: 12 },
  companion: { id: 'cmp', preset: 'balanced', configured: true },
}

function open() {
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: () => null,
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })
  const send = selectedTab => listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
    selectedTab, onboarding: { complete: true }, boot,
  } })
  send('overview')
  const click = dataset => listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) },
    preventDefault() {},
  })
  return { posted, root, click, send }
}

const onboardingShown = ui =>
  ui.root.innerHTML.includes('data-action="onboarding-step"') || ui.root.innerHTML.includes('complete-onboarding')

const ui = open()
check('до перехода онбординга нет', !onboardingShown(ui), 'обзор уже показывает онбординг')

ui.click({ action: 'open-orchestrator-setup' })
check('настройка Мастера открылась', onboardingShown(ui), 'экран настройки системных агентов не отрисован')
check('интерфейс сообщил расширению о переходе',
  ui.posted.some(message => message.type === 'selectTab' && message.tab === 'onboarding'),
  `отправлено: ${JSON.stringify(ui.posted.map(message => `${message.type}:${message.tab || ''}`))}`)

// Расширение, узнав о вкладке, присылает её обратно — и это ничего не рушит.
ui.send('onboarding')
check('состояние с вкладкой onboarding не закрывает настройку', onboardingShown(ui),
  'онбординг закрылся от собственного состояния')

// А вот прежняя вкладка закрывает — именно так это и выглядело у человека.
// Сторож живёт в extension.js (focusTab), здесь фиксируем сам механизм.
ui.send('overview')
check('вкладка из состояния всё ещё главная', !onboardingShown(ui),
  'интерфейс перестал слушаться вкладки из состояния — проверка ослепла')

if (failures.length) {
  console.error('ОНБОРДИНГ ЗАКРЫВАЕТСЯ — ПРОВАЛ:')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('онбординг держится: PASS')
