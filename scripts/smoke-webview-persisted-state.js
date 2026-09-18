// Сохранённое состояние вебвью (vscode.getState) переживает перезапуск окна и
// обновление Point. Если инициализация бандла падает на таком состоянии, вебвью
// умирает до первой отрисовки и панель навсегда остаётся на заглушке
// «Открываем диалог…» — без ошибки в логах и без выхода: перезапуск IDE
// восстанавливает то же состояние и ту же поломку.
const fs = require('fs')
const path = require('path')
const vm = require('vm')

const script = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')

// Состояние живого рабочего места: пройденная Studio, история компаньона и
// открытые черновики. Пустой объект прошёл бы даже мимо сломанного бандла.
const persistedState = {
  // Ключ мира лежит рядом с черновиками: по его расхождению вебвью понимает,
  // что панель переехала в другой проект, и забывает всё проектное. Сохранённое
  // состояние с прежним ключом обязано подниматься так же, как и без него.
  projectKey: 'C:\\worlds\\ai-ide',
  selectedTab: 'master',
  selectedProfileId: 'cursor-default',
  taskDraft: 'проверь сборку',
  questGoalDraft: '',
  questCriteriaDraft: '',
  questConstraintsDraft: '',
  contextItems: [{ path: 'main.go', kind: 'file' }],
  companionMessages: [{ id: 'companionmsg_1', role: 'user', content: 'Что сломано?', createdAt: '2026-08-24T20:29:00Z' }],
  companionDraft: '',
  companionRailExpanded: false,
  companionSetupOpen: true,
  companionSetupStep: 'brain',
  companionSetupDraft: {
    mode: 'model',
    connectionMode: 'existing',
    connectionId: 'connection_1',
    connectionName: 'LM Studio',
    providerPreset: 'llmux',
    provider: 'openai-compatible',
    baseUrl: 'https://example.invalid',
    model: 'Qwen3.8-27B',
    preset: 'technical-lead',
    sampleScene: 'logs',
  },
  onboardingStep: 'welcome',
  onboardingDraft: { companionPreset: 'balanced', criticality: 50, creativity: 50, verbosity: 50, initiative: 50, questionStrictness: 70, riskTolerance: 30, agentName: '', agentTemplateId: '', connectionProvider: '', skillIds: [] },
  agentConstructorOpen: false,
  constructorStep: 'identity',
  ignoredCompanionSuggestions: [],
  selectedFlowId: '',
  flowLegacyMode: false,
  masterDraft: '',
}

function bootWebview(layout, persisted) {
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
      getState() { return persisted },
      setState() { },
    }),
    document: {
      getElementById: id => (id === 'root' ? root : undefined),
      body: { dataset: { layout } },
      addEventListener() { },
      querySelector() { return null },
      querySelectorAll() { return [] },
    },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console,
    Date,
    Map,
    Set,
    CSS: { escape(value) { return String(value) } },
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() { },
    setTimeout(callback) { callback(); return 0 },
    clearTimeout() { },
    setInterval() { return 0 },
    clearInterval() { },
  }
  context.globalThis = context
  vm.runInNewContext(script, context, { filename: 'media/main.js' })
  return { posted, root, listeners }
}

for (const layout of ['companion', 'companion-sidebar', 'companion-peek', 'companion-popup', 'wide', 'narrow']) {
  let booted
  try {
    booted = bootWebview(layout, persistedState)
  } catch (error) {
    throw new Error(`Вебвью «${layout}» не пережило сохранённое состояние: ${error?.message || error}`)
  }
  if (!booted.posted.some(message => message.type === 'ready')) {
    throw new Error(`Вебвью «${layout}» не дошло до готовности на сохранённом состоянии`)
  }
  if (!booted.root.innerHTML) {
    throw new Error(`Вебвью «${layout}» не отрисовало первый кадр — заглушка загрузки осталась бы навсегда`)
  }
}

// Черновик из прошлой версии Point может не подойти нынешнему санитайзеру.
// Чат важнее одного черновика: он должен открыться, а не встать на заглушке.
const brokenDraftState = { ...persistedState, companionSetupDraft: { mode: 'model', sampleScene: { unexpected: 'shape' }, temperature: 'горячо' } }
for (const layout of ['companion', 'companion-sidebar', 'wide']) {
  let booted
  try {
    booted = bootWebview(layout, brokenDraftState)
  } catch (error) {
    throw new Error(`Вебвью «${layout}» упало на непонятном черновике Studio вместо того, чтобы открыть чат: ${error?.message || error}`)
  }
  if (!booted.root.innerHTML) {
    throw new Error(`Вебвью «${layout}» осталось пустым после непонятного черновика Studio`)
  }
}

console.log('smoke-webview-persisted-state: ok')
