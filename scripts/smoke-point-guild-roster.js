// Гильдия: ростер, выбор специалиста и происхождение метрик.
//
// Прошлая редакция этой проверки сверяла заголовки — «РОСТЕР АГЕНТОВ»,
// «СНАРЯЖЁННЫЕ УМЕНИЯ». Гильдию переработали, заголовки стали другими, и
// проверка покраснела, не найдя ни одного настоящего дефекта. Хуже: она была
// не подключена к гейту, поэтому расхождение пролежало неизвестно сколько.
//
// Поэтому здесь не проверяется ни один заголовок раздела. Проверяются данные и
// маршруты: попал ли каждый профиль в ростер, ведут ли кнопки дальше, меняется
// ли деталь при выборе другого специалиста и считаются ли показатели из
// настоящих запусков. Переименование раздела такую проверку не ломает —
// сломает её только настоящая регрессия.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const listeners = {}
const posted = []
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector() { return undefined },
  querySelectorAll() { return [] },
}
const context = {
  acquireVsCodeApi: () => ({
    postMessage(message) { posted.push(message) },
    getState() { return undefined },
    setState() {},
  }),
  document: { body: { dataset: { layout: 'wide' } }, getElementById: id => id === 'root' ? root : undefined },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set,
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() {},
  setTimeout(callback) { callback(); return 0 },
  clearTimeout() {},
}
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8'), context, { filename: 'media/main.js' })

// SAGE-7 завершил свой единственный запуск, FORGE — провалил. Показатели двух
// специалистов обязаны разойтись; одинаковые числа означали бы, что они взяты
// не из запусков.
const profiles = [
  { id: 'sage', name: 'SAGE-7', roleDescription: 'Проектирует безопасные изменения.', provider: 'ollama', model: 'qwen-coder', allowedTools: ['list_files', 'read_file'], maxSteps: 20, maxDurationSeconds: 600, approvalMode: 'safe' },
  { id: 'forge', name: 'FORGE', roleDescription: 'Реализует backend-функции.', provider: 'openai-compatible', model: 'coder', allowedTools: ['read_file', 'propose_patch', 'run_command'], maxSteps: 28, maxDurationSeconds: 900, approvalMode: 'always' },
]
const runs = [
  { id: 'run-1', profileId: 'sage', status: 'completed', task: 'Разведать архитектуру', startedAt: '2026-08-09T10:00:00Z', step: 3, model: 'qwen-coder' },
  { id: 'run-2', profileId: 'forge', status: 'failed', task: 'Исправить тест', startedAt: '2026-08-09T11:00:00Z', step: 4, model: 'coder' },
]
listeners['window:message']({ data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'settings',
  boot: { profiles, runs, toolCatalog: [
    { name: 'list_files', displayName: 'Инвентарь' }, { name: 'read_file', displayName: 'Чтение' },
    { name: 'propose_patch', displayName: 'Правка' }, { name: 'run_command', displayName: 'Консоль' },
  ] },
} })

const fail = message => { throw new Error(message) }
// Показатель ищется по метке и значению рядом, а не по точной цепочке тегов:
// перестановка <small>/<b> — это вёрстка, а разошедшееся число — регрессия.
const plain = html => String(html).replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim()
const hasRoute = (html, action, id) => {
  const attributes = `data-action="${action}"`
  if (!id) return html.includes(attributes)
  return new RegExp(`${attributes}[^>]*data-id="${id}"|data-id="${id}"[^>]*${attributes}`).test(html)
}
// Деталь выбранного специалиста опознаётся по структурному имени, а не по
// заголовку: раздел может называться как угодно, но он обязан существовать
// отдельно от списка — иначе выбор нечему обновлять.
const detailOf = html => {
  const start = html.indexOf('class="roster-detail"')
  if (start < 0) fail('в Гильдии нет отдельной детали выбранного специалиста')
  const end = html.indexOf('class="roster-progression"', start)
  return html.slice(start, end < 0 ? html.length : end)
}

// 1. Каждый профиль попал в ростер и ведёт к выбору.
for (const profile of profiles) {
  if (!hasRoute(root.innerHTML, 'select-roster-profile', profile.id)) fail(`профиля ${profile.id} нет в ростере`)
  if (!root.innerHTML.includes(profile.name)) fail(`имя профиля ${profile.name} не показано`)
}

// 2. Показатели берутся из настоящих запусков. У SAGE-7 один завершённый —
//    значит одна задача и стопроцентная надёжность.
{
  const detail = plain(detailOf(root.innerHTML))
  if (!detail.includes('ЗАДАЧИ 1')) fail('деталь не показывает число задач из сохранённых запусков')
  if (!detail.includes('НАДЁЖНОСТЬ 100%')) fail('надёжность SAGE-7 посчитана не по завершённому запуску')
}
if (!root.innerHTML.includes('Метрики рассчитаны локально по сохранённым запускам Point')) {
  fail('снято утверждение о локальном происхождении метрик')
}

function click(action, extra = {}) {
  listeners['root:click']({ target: { closest(selector) {
    if (selector === '[data-example]') return null
    if (selector === '[data-action]') return { dataset: { action, ...extra } }
    return null
  } } })
}

// 3. Выбор другого специалиста перерисовывает деталь его данными. Рассинхрон
//    выбора и детали — настоящая регрессия, ради которой проверка и живёт.
click('select-roster-profile', { id: 'forge' })
{
  const detail = detailOf(root.innerHTML)
  if (!detail.includes('Реализует backend-функции.')) fail('деталь не переехала на выбранный профиль')
  if (detail.includes('Проектирует безопасные изменения.')) fail('в детали остались данные прошлого выбора')
  if (!plain(detail).includes('НАДЁЖНОСТЬ 0%')) fail('надёжность FORGE не посчитана по проваленному запуску')
}

// 4. Из детали есть все три выхода: проектная адаптация, конструктор профиля и
//    постановка задачи.
for (const action of ['edit-roster-profile', 'open-agent-constructor-edit', 'start-roster-quest']) {
  if (!hasRoute(detailOf(root.innerHTML), action)) fail(`из детали пропал выход ${action}`)
}

// 5. Редактор открывается и возвращает обратно в ростер.
click('edit-roster-profile')
if (!hasRoute(root.innerHTML, 'close-profile-editor')) fail('редактор профиля открылся без пути назад')
click('close-profile-editor')
if (!hasRoute(root.innerHTML, 'select-roster-profile', 'forge')) fail('закрытие редактора не вернуло ростер')

// 6. Постановка задачи уводит в квесты, а не остаётся кнопкой без проводки.
click('start-roster-quest')
if (!posted.some(message => message.type === 'selectTab' && message.tab === 'chat')) fail('кнопка задачи не подключена')

// 7. Схемы проекта достижимы оттуда, где роспуск персонажа и отказывает.
//
// Роспуск отвечает «персонаж стоит в узле %q схемы %q — замените его в схеме»,
// а экран уборки схем (projectFlowsView) до 21 сентября 2026 не имел ни одного
// входа: кнопки data-tab="flows" не было нигде, кроме карточек внимания при
// конфликте веток. Отказ называл дверь, которой не существовало, и персонаж
// оставался неудаляемым навсегда. Зовут раздел только когда схемы есть.
listeners['window:message']({ data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'settings',
  boot: { profiles, runs, toolCatalog: [], flows: [{ id: 'flow-1', name: 'pipeline · тест', nodes: [{ id: 'n1', kind: 'agent', name: 'Bootstrap', agentId: 'sage' }] }] },
} })
if (!root.innerHTML.includes('data-tab="flows"')) {
  fail('со схемами в проекте ростер не зовёт в раздел схем — распустить персонажа станет негде')
}

listeners['window:message']({ data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'settings',
  boot: { profiles, runs, toolCatalog: [], flows: [] },
} })
if (root.innerHTML.includes('data-tab="flows"')) {
  fail('пустой раздел схем зовут без нужды')
}

process.stdout.write(JSON.stringify({ roster: 'ok', profiles: profiles.length, runs: runs.length, metrics: 'actual', flowsDoor: 'reachable' }))
