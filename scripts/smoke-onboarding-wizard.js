// Мастер первого запуска — на чистом состоянии.
//
// Общий смок к моменту своих проверок успевает пройти половину мастера, и замки
// в нём уже сняты. Первое пробуждение бывает ровно один раз, поэтому проверять
// его надо в мире, где ещё ничего не настроено.

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
    setState() {},
  }),
  document: {
    getElementById: id => (id === 'root' ? root : undefined),
    body: { dataset: { layout: 'wide' } },
  },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set,
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() {},
  setTimeout(callback) { callback(); return 0 },
  clearTimeout() {},
}

vm.runInNewContext(
  fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8'),
  context, { filename: 'media/main.js' })

function click(action, extra = {}) {
  listeners['root:click']({ target: { closest(selector) {
    if (selector === '[data-example]') return null
    if (selector === '[data-action]') return { dataset: { action, ...extra } }
    return null
  } } })
}

listeners['window:message']({ data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture',
  selectedTab: 'onboarding', boot: { profiles: [], runs: [], usageRecords: [] }, details: undefined,
} })

if (!root.innerHTML.includes('onboarding-wizard')) {
  throw new Error('Onboarding wizard did not render')
}

// Hub v2 оставляет в первом запуске только подключение и Мастера. Проверяем
// сам пользовательский путь, включая атомарное сохранение последнего шага.
if (!root.innerHTML.includes('Шаг 01 / 02') || !root.innerHTML.includes('Подключение Мастера')) {
  throw new Error('Fresh Hub v2 must start at Master connection, step 1 of 2')
}
for (const forbidden of ['data-step="companion-choose"', 'data-step="first-agent"', 'data-step="model-connection"']) {
  if (root.innerHTML.includes(forbidden)) {
    throw new Error(`Optional setup leaked into first-run navigation: ${forbidden}`)
  }
}
if (root.innerHTML.includes(' locked')) {
  throw new Error('Master setup must not depend on Companion or roster setup')
}
if (context.firstIncompleteOnboardingStep()?.id !== 'orchestrator-brain') {
  throw new Error('Fresh onboarding did not resume at Master connection')
}
if (!root.innerHTML.includes('companion-mode-card selected') || !root.innerHTML.includes('Движок Point готов сразу')) {
  throw new Error('Fresh Master onboarding must default to the credential-free Point engine')
}
click('onboarding-step', { step: 'orchestrator-choose' })
if (!root.innerHTML.includes('Шаг 02 / 02') || !root.innerHTML.includes('Настройте Мастера')) {
  throw new Error('Master policy step did not open')
}
if (!posted.some(message => message.type === 'saveOrchestratorConfig')) {
  throw new Error('Connection was not saved before the policy step')
}
if (!root.innerHTML.includes('data-action="complete-master-onboarding"')) {
  throw new Error('Final step lost Save and Open Master action')
}
click('complete-master-onboarding')
if (!posted.some(message => message.type === 'completeOnboarding')) {
  throw new Error('Master setup did not complete onboarding')
}
if (posted.filter(message => message.type === 'saveOrchestratorConfig').length < 2) {
  throw new Error('Final Master policy was not saved atomically with completion')
}
click('restart-onboarding')
if (!posted.some(message => message.type === 'restartOnboarding') || context.firstIncompleteOnboardingStep()?.id !== 'orchestrator-brain') {
  throw new Error('Restart did not return to Master connection')
}
console.log(JSON.stringify({ onboarding: 'master-first', steps: 2, optionalGates: 0 }))

// Сценарий v1 сохранён рядом как спецификация удалённого пути. Он намеренно не
// исполняется: возврат любого из этих обязательных экранов пойман выше.
if (false) {

// На welcome есть одна главная навигация — закреплённый футер. Вторая такая же
// кнопка внутри прокручиваемой карточки выглядела как два разных действия.
if ((root.innerHTML.match(/class="primary" data-action="onboarding-step"/g) || []).length !== 1) {
  throw new Error('Welcome screen must expose one primary Continue action')
}

// Автоматически созданный balanced-конфиг делает чат рабочим сразу, но не
// является выбором человека и не должен перескакивать три шага компаньона.
listeners['window:message']({ data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture',
  selectedTab: 'onboarding',
  boot: { profiles: [], projectAgents: [], blueprints: [], companion: { id: 'default', configured: false }, runs: [], usageRecords: [] },
  details: undefined,
} })
if (context.firstIncompleteOnboardingStep()?.id !== 'companion-choose') {
  throw new Error('Technical companion default skipped the human setup steps')
}

// Шаги за ненастроенными системными агентами закрыты — и это видно до клика.
if (!root.innerHTML.includes('locked')) {
  throw new Error('Steps behind unfinished system agents were not marked as locked')
}

// Клик по закрытому шагу раньше молча ничего не делал: человек жал и не понимал,
// сломано ли это, не туда ли он нажал, или так задумано.
click('onboarding-step', { step: 'model-connection' })
if (!root.innerHTML.includes('onboarding-lock-note')) {
  throw new Error('Clicking a locked onboarding step gave no explanation')
}
if (!root.innerHTML.includes('Сначала закончите')) {
  throw new Error('Lock explanation did not say what to finish first')
}
if (!root.innerHTML.includes('Шаг 01')) {
  throw new Error('A locked step must not become the current step')
}

// Разрешённый шаг открывается и уносит объяснение с собой.
click('onboarding-step', { step: 'companion-choose' })
if (root.innerHTML.includes('onboarding-lock-note')) {
  throw new Error('Lock explanation survived a successful step change')
}
if (!root.innerHTML.includes('Шаг 02')) {
  throw new Error('An allowed step did not open')
}

// Управление шагом живёт в подвале и обязано присутствовать на каждом шаге:
// именно его отсутствие на экране делало мастер непроходимым.
for (const step of ['companion-config', 'companion-brain']) {
  click('onboarding-step', { step })
  if (!root.innerHTML.includes('onboarding-footer')) {
    throw new Error(`Step ${step} rendered without its footer controls`)
  }
}


// ── Один шаг — один вопрос ────────────────────────────────────────────────
// Стили поведения («Сбалансированный», «Осторожный», …) лежали на шаге выбора
// роли под заголовком «Ещё роли»: ролями они не были и дублировали следующий
// шаг, поэтому характер выбирался до того, как человек доходил до экрана
// характера.
click('onboarding-step', { step: 'companion-choose' })
if (!root.innerHTML.includes('companion-preset-studio')) {
  throw new Error('Role step lost its role choices')
}
if (root.innerHTML.includes('Ещё роли')) {
  throw new Error('Behaviour styles are back on the role step disguised as roles')
}
click('onboarding-step', { step: 'companion-config' })
if (!root.innerHTML.includes('companion-style-presets')) {
  throw new Error('Behaviour styles did not move to the character step')
}
if (!root.innerHTML.includes('companion-range')) {
  throw new Error('Character step lost its trait sliders')
}


// Первый запуск не спрашивает про доводку.
//
// «Навыки» и «Разрешения» были отдельными шагами, хотя ни один не нужен, чтобы
// начать работать: чертёж уже выдаёт агенту умения, а текст самого шага говорил
// «экипировка необязательна». Одиннадцать шагов до первого квеста — препятствие,
// а не тщательность.
const railSteps = (root.innerHTML.match(/data-action="onboarding-step" data-step="([a-z-]+)"/g) || [])
for (const gone of ['data-step="skills"', 'data-step="permissions"']) {
  if (railSteps.some(step => step.includes(gone))) {
    throw new Error(`Optional step came back to the first run: ${gone}`)
  }
}
// Проверяем общее число, а не текущий шаг: к этому месту смок уже ходил по
// мастеру, и «шаг 01» здесь ничего не значил бы.
if (!root.innerHTML.includes('/ 09')) {
  throw new Error('First run should be nine steps; the counter says otherwise')
}

// ── Полоса готовности ───────────────────────────────────────────────────────
// Готовность жила отдельной колонкой во всю высоту справа. Семи однострочным
// фактам столько места не нужно, а шагу его не хватало: замер на 1280 давал
// колонку шага 540px при содержимом до 1121px — больше половины пряталось под
// внутренней прокруткой. Полоса под шапкой отдала шагу 260px и стала видна на
// каждом шаге, а не только там, куда дотянулся взгляд.
{
  const strip = root.innerHTML.match(/<div class="onboarding-readiness">[\s\S]*?<\/div>\s*<div class="onboarding-layout"/)
  if (!strip) {
    throw new Error('Полоса готовности не отрисована под шапкой')
  }
  if (root.innerHTML.includes('onboarding-status')) {
    throw new Error('Вернулась колонка готовности — шаг снова теряет четверть ширины')
  }
  // Факт, за которым стоит шаг, — кнопка: увидел незакрытое и нажал.
  for (const step of ['companion-choose', 'orchestrator-choose', 'first-agent', 'model-connection']) {
    if (!strip[0].includes(`data-action="onboarding-step" data-step="${step}"`)) {
      throw new Error(`Факт готовности «${step}» никуда не ведёт`)
    }
  }
  // А факт без шага остаётся фактом: идти некуда, и кнопкой он прикидываться
  // не должен.
  const projectFact = strip[0].match(/<span[^>]*>\s*<i>[^<]*<\/i><span><strong>Проект открыт/)
  if (!projectFact) {
    throw new Error('«Проект открыт» стал кнопкой или пропал из полосы')
  }
}

// Онбординг не считает чертежи нанятыми персонажами.
//
// hubAgents() берёт projectAgents, а при отсутствии ключа откатывается на
// profiles — legacy-адаптеры чертежей. Ключ приходил с omitempty и при пустом
// ростере пропадал, поэтому на свежей установке онбординг видел «в ростере 2» и
// перепрыгивал собственный шаг «Создайте первого агента»: настроив компаньона и
// мастера, человек попадал сразу на «Готово», не создав ни одного персонажа.
// Ядро теперь присылает ключ всегда (internal/app/hub_bootstrap_test.go).
{
  const seeded = [
    { id: 'p1', name: 'Локальный агент', provider: 'ollama', model: 'qwen', allowedTools: ['read_file'], maxSteps: 30 },
    { id: 'p2', name: 'Cursor Agent', provider: 'cursor-cli', model: 'auto', allowedTools: [], maxSteps: 30 },
  ]
  const configured = { companion: { id: 'c1', configured: true }, orchestrator: { id: 'o1' } }

  listeners['window:message']({
    data: {
      type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture',
      selectedTab: 'onboarding',
      boot: { ...configured, profiles: seeded, projectAgents: [], blueprints: [], runs: [], usageRecords: [] },
      details: undefined,
    },
  })
  const resume = context.firstIncompleteOnboardingStep()
  if (resume?.id !== 'first-agent') {
    throw new Error(`Пустой ростер обязан вести на шаг создания агента, а ведёт на «${resume?.id}»`)
  }
  if (context.hubAgents().length !== 0) {
    throw new Error('Чертежи снова считаются нанятыми персонажами')
  }

  listeners['window:message']({
    data: {
      type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture',
      selectedTab: 'onboarding',
      boot: { ...configured, profiles: seeded, projectAgents: null, blueprints: [], runs: [], usageRecords: [] },
      details: undefined,
    },
  })
  if (context.hubAgents().length !== 0) {
    throw new Error('Null-ростер снова включает legacy-профили и выдумывает агентов')
  }
}

console.log(JSON.stringify({ onboarding: 'locks-explained', steps: 'navigable' }))
}
