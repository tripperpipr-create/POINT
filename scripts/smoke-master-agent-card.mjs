// Карточка нового исполнителя: отдельная запись ленты Мастера.
//
// До неё создание агента было разорвано на три куска, и ни один не был
// карточкой ленты: предложение ядра рисовалось классами регистра Хаба, для
// которых в разговоре нет ни одного правила, согласие сидело ярусом внутри
// карточки запуска, а карточка найма показывала три поля и кнопку, уводившую на
// другую вкладку. Здесь проверяется то, ради чего их свели в одну карточку:
// она самостоятельна, приходит заполненной, несёт настройки, которые ядро
// действительно применяет, не теряет набранное и не отправляет пустое.
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const clientURL = name => pathToFileURL(path.join(root, 'vscode-extension', 'ui', 'client', name)).href
const {
  handleMasterAgentCardAction, masterAgentCardFromAction, masterAgentCardHtml, masterAgentCardsFor,
  masterAgentCardsHtml, masterAgentConsent, masterAgentDrafts, masterAgentIssue,
  masterAgentValue, readMasterAgentCardInput,
} = await import(clientURL('master-agent-card.js'))
const { masterWorkOrderCardsHtml } = await import(clientURL('master-work-order-v2.js'))
const { masterHiringCardsHtml } = await import(clientURL('master-hiring-card.js'))
const { esc } = await import(clientURL('html-escape.js'))

const fail = message => { throw new Error(message) }
const expectAll = (html, expected, label) => {
  for (const text of expected) {
    if (!html.includes(text)) fail(`${label}: в карточке нет «${text}»`)
  }
}

const deps = {
  connections: [{ id: 'conn-1', displayName: 'Локальный Ollama', isDefault: true, defaultModel: 'qwen2.5-coder:7b', models: [{ id: 'qwen2.5-coder:7b' }] }],
  toolCatalog: [
    { name: 'read_file', displayName: 'Чтение файлов', risk: 'LOW' },
    { name: 'run_command', displayName: 'Запуск команд', risk: 'HIGH' },
  ],
  connectionLabel: item => item.displayName,
}

const draft = {
  id: 'agentdraft-1', name: 'Разработчик проекта', role: 'Владелец реализации',
  mission: 'Собрать проект и довести health-эндпоинт до зелёного',
  requiredTools: ['read_file', 'run_command'], existing: false, requiresConsent: true,
}
const order = {
  id: 'workorder-1', state: 'ready', version: 1, digest: 'sha256:x', goal: 'Собрать API',
  routing: { fixedModel: 'Qwen3.8-27B' },
  roster: { permanent: [draft], temporary: [] },
}
const hiring = {
  workOrderId: 'workorder-1', goal: 'Собрать API', state: 'blueprint', maxAgents: 2,
  reason: 'в ростере нет исполнителя с правом правки файлов',
  draft, blueprints: [{ blueprintId: 'bp', name: 'Кузнец', role: 'Правит backend', why: 'совпало с задачей', tools: ['read_file'] }],
}

const reset = () => { masterAgentDrafts.clear(); masterAgentConsent.clear() }

// 1. Черновик наряда даёт одну карточку, заполненную тем, что прислало ядро.
reset()
const cards = masterAgentCardsFor({ workOrders: [order], hiring: [hiring] })
if (cards.length !== 1) fail(`черновик наряда обязан дать ровно одну карточку, а дал ${cards.length}`)
const html = masterAgentCardsHtml(cards, esc, deps)
expectAll(html, [
  'class="hall-deck master-agent"', 'Новый исполнитель',
  'value="Разработчик проекта"', 'value="Владелец реализации"', 'health-эндпоинт',
  'value="Qwen3.8-27B"', 'Локальный Ollama',
  'data-action="agent-card-create"', 'data-action="agent-card-later"', 'data-action="agent-card-workshop"',
], 'карточка черновика')
if (html.includes('undefined')) fail('карточка не должна пропускать undefined в разметку')

// 2. Карточка самостоятельна: она не вложена в карточку запуска, а та её больше
//    не рисует. Ярус согласия внутри чужого документа — то, ради чего всё это.
const orderHtml = masterWorkOrderCardsHtml([order], esc, new Set(), {})
if (orderHtml.includes('master-v2-consent"') || orderHtml.includes('ЧЕРНОВИК АГЕНТА')) {
  fail('карточка запуска снова рисует черновик агента внутри себя')
}
if (orderHtml.includes('data-action="request-roster-consent-v2"')) {
  fail('карточка запуска снова раскрывает согласие своей кнопкой')
}

// 3. Запуск заперт, пока исполнителя нет, и причина названа словами.
if (!/data-action="approve-master-work-order-v2"[^>]*disabled/.test(orderHtml)) {
  fail('наряд с незаведённым исполнителем обязан запирать запуск')
}
expectAll(orderHtml, ['Сначала заведите исполнителя'], 'причина запрета')

// 4. «Создать при запуске» отпирает запуск: согласие человека дано, и дальше
//    исполнителя создаёт транзакция утверждения — как и требует ядро.
let rendered = 0
handleMasterAgentCardAction('agent-card-later', { dataset: { card: cards[0].id } }, { cards, render: () => { rendered += 1 } })
if (!masterAgentConsent.has('workorder-1')) fail('согласие не записано')
if (!rendered) fail('согласие обязано перерисовать ленту')
const consentedOrder = masterWorkOrderCardsHtml([order], esc, new Set(), {})
if (/data-action="approve-master-work-order-v2"[^>]*disabled/.test(consentedOrder)) {
  fail('после согласия запуск обязан отпираться')
}

// 5. Пустой черновик не уходит в ядро: отказ назван до отправки, по-русски.
reset()
const empty = { id: 'card-empty', kind: 'work-order', workOrderId: 'workorder-1', base: { name: '', role: '', allowedTools: ['read_file'], model: 'm' } }
const issue = masterAgentIssue(masterAgentValue(empty), deps.connections)
if (!issue.includes('имя')) fail(`отказ без имени назван непонятно: ${issue}`)
let posted
handleMasterAgentCardAction('agent-card-create', { dataset: { card: empty.id } }, {
  cards: [empty], render: () => {}, connections: () => deps.connections,
  vscode: { postMessage: message => { posted = message } },
  createAgent: () => { posted = 'создан' },
})
if (posted) fail('карточка без имени не должна ничего отправлять')
if (!masterAgentCardHtml(empty, esc, deps).includes(issue)) fail('отказ обязан быть виден в самой карточке')

// 6. Предложение ядра рисуется той же карточкой, а не видом Хаба: у прежней
//    разметки в ленте не было ни меры колонки, ни геометрии Чертога.
reset()
const action = masterAgentCardFromAction({
  id: 'proposal-1', kind: 'create_agent', title: 'Создать агента', rationale: 'выполнять задачу некому',
  agent: { name: 'Хранитель', roleDescription: 'Ведёт бэкенд', mission: 'Держать сборку зелёной', primaryModel: 'qwen3-coder:30b', allowedTools: ['read_file'] },
})
const actionHtml = masterAgentCardHtml(action, esc, deps)
expectAll(actionHtml, ['class="hall-deck master-agent"', 'value="Хранитель"', 'value="qwen3-coder:30b"', 'data-action="agent-card-dismiss"'], 'предложение ядра')
if (actionHtml.includes('quest-proposal-card') || actionHtml.includes('companion-action-card')) {
  fail('предложение ядра снова рисуется классами чужого регистра')
}
if (actionHtml.includes('data-action="agent-card-later"')) {
  fail('у предложения ядра нет наряда — «создать при запуске» там нечему')
}

// 7. Настройки человека уходят в ядро целиком. Прежде решение несло только имя,
//    роль и миссию, и выбранная модель с умениями терялась по дороге.
masterAgentDrafts.set(action.id, { name: 'Хранитель бэкенда', allowedTools: ['read_file', 'run_command'], toolPolicies: { run_command: 'ASK' }, maxSteps: 12 })
let sent
handleMasterAgentCardAction('agent-card-create', { dataset: { card: action.id } }, {
  cards: [action], render: () => {}, connections: () => deps.connections,
  vscode: { postMessage: message => { sent = message } },
})
if (!sent || sent.type !== 'decideCompanionAction' || sent.action !== 'apply') fail(`создание не ушло в ядро: ${JSON.stringify(sent)}`)
if (sent.name !== 'Хранитель бэкенда' || sent.primaryModel !== 'qwen3-coder:30b') fail('решение потеряло имя или модель')
if (sent.allowedTools.join(',') !== 'read_file,run_command' || sent.toolPolicies.run_command !== 'ASK') fail('решение потеряло умения или права')
if (sent.maxSteps !== 12) fail('решение потеряло предел ходов')

// 8. Чертёж подставляется в поля, не уводя из ленты. Прежняя кнопка «Нанять»
//    переключала вкладку на «Агенты» и открывала конструктор.
reset()
const blueprintCards = masterAgentCardsFor({ workOrders: [order], hiring: [hiring] })
handleMasterAgentCardAction('agent-card-blueprint', { dataset: { card: blueprintCards[0].id, template: 'bp' } }, {
  cards: blueprintCards, render: () => {}, blueprintById: () => undefined,
})
if (masterAgentValue(blueprintCards[0]).name !== 'Кузнец') fail('чертёж не подставился в поля карточки')

// 9. Карточка найма больше не заводит исполнителей и не рисует пустую рамку,
//    когда решать в ней нечего.
if (masterHiringCardsHtml([hiring], esc) !== '') {
  fail('карточка найма без кандидатов обязана молчать — за создание отвечает карточка исполнителя')
}

// 10. Утверждённый наряд карточки не получает: состав уже зафиксирован.
if (masterAgentCardsFor({ workOrders: [{ ...order, state: 'approved' }], hiring: [] }).length) {
  fail('утверждённый наряд не должен звать заводить исполнителя')
}

// 10а. Наряд на уточнении — тоже не получает.
//
// Пока Мастер спрашивает, чего от задания хотят, решать о новой сущности
// проекта нечем: от ответа меняется состав наряда, а вместе с ним и то, кто
// для него нужен. Карточка стояла рядом с неотвеченным вопросом — с ролью,
// моделью и снаряжением, выбранными под задание, которого ещё нет.
const unsettled = { ...order, state: 'discussion' }
if (masterAgentCardsFor({ workOrders: [unsettled], hiring: [] }).length) {
  fail('наряд на уточнении не должен звать заводить исполнителя')
}
// Пробел ростера считается по тому же наряду: сочинённый наблюдателем черновик
// говорит, кого не хватает, и молчит о том, решено ли задание.
if (masterAgentCardsFor({ workOrders: [unsettled], hiring: [hiring] }).length) {
  fail('черновик наблюдателя провёл карточку мимо неготового наряда')
}
if (masterAgentCardsFor({ workOrders: [], hiring: [hiring] }).length) {
  fail('пробел ростера без наряда не с чем сверить — карточки быть не должно')
}
// Собранный наряд карточку по-прежнему даёт — затвор закрыт не для всех.
if (masterAgentCardsFor({ workOrders: [order], hiring: [hiring] }).length !== 1) {
  fail('затвор закрылся и перед готовым нарядом — заводить исполнителя стало негде')
}

// 11. Пустые данные не рисуют ничего и не роняют вид.
if (masterAgentCardsHtml([], esc, deps) !== '') fail('без карточек лента обязана остаться чистой')
if (masterAgentCardsFor(undefined).length) fail('отсутствие данных не должно порождать карточки')
if (masterAgentCardHtml(undefined, esc, deps) !== '') fail('пустая карточка не должна рисоваться')

// 12. Набранное переживает перерисовку. Прежние формы создания снимали значения
// только при отправке, и любой ход Мастера стирал написанное молча и целиком.
reset()
const typing = masterAgentCardsFor({ workOrders: [order], hiring: [] })[0]
const note = { textContent: 'старая ошибка', classList: { add() { note.hidden = true } } }
const host = {
  dataset: { agentCard: typing.id },
  querySelector: () => note,
  querySelectorAll: () => [
    { checked: true, value: 'read_file' },
    { checked: false, value: 'run_command' },
  ],
}
const field = (name, value, extra = {}) => ({ dataset: { agentField: name, ...extra }, value, closest: () => host })
readMasterAgentCardInput(field('name', 'Хранитель сборки'))
readMasterAgentCardInput(field('maxSteps', '7'))
if (masterAgentValue(typing).name !== 'Хранитель сборки') fail('набранное имя не пережило снятия')
if (masterAgentValue(typing).maxSteps !== 7) fail('предел ходов снят не числом')
if (!masterAgentCardHtml(typing, esc, deps).includes('value="Хранитель сборки"')) {
  fail('перерисовка вернула карточку к черновику ядра — набранное потеряно')
}
if (!note.hidden) fail('правка обязана гасить прежнее объяснение отказа')

// 13. Отметка умения снимается по всей карточке, а политика — по своему полю:
// порознь набор и права расходились, и человек не видел, что разрешил.
readMasterAgentCardInput(field('tool', 'read_file'))
if (masterAgentValue(typing).allowedTools.join(',') !== 'read_file') fail('снятая отметка умения не учтена')
readMasterAgentCardInput(field('policy', 'DENY', { tool: 'read_file' }))
if (masterAgentValue(typing).toolPolicies.read_file !== 'DENY') fail('политика умения не снята')

// 14. Лист персонажа показывает настройки, а не выдумку.
//
// Условие макета «Квесты в чате» (вариант 3a) здесь и проверяется:
// характеристики персонажа — это реальные поля агента. Полоса, которая
// нарисована от литерала, врёт вдвойне: она и неверна, и не меняется, когда
// человек правит то самое поле.
reset()
const sheetCard = masterAgentCardFromAction({
  id: 'hubaction-sheet', kind: 'create_agent', status: 'pending',
  rationale: 'для задания «Повторная доставка вебхука»',
  agent: {
    name: 'Ковач', roleDescription: 'Разработчик проекта', roleFamily: 'developer',
    mission: 'Чинит бэкенд и закрывает регрессом', allowedTools: ['read_file', 'run_command'],
    toolPolicies: { read_file: 'ALLOW', run_command: 'ASK' },
    primaryModel: 'qwen2.5-coder:7b', reasoningEffort: 'high', approvalMode: 'safe',
    maxSteps: 45, maxDurationSeconds: 1800, level: 4, experience: 340, tasksCompleted: 7, successCount: 6,
  },
})
const sheet = masterAgentCardHtml(sheetCard, esc, deps)
expectAll(sheet, [
  'master-agent-portrait', '>КО<', 'разработчик', 'уровень 4 · 340 опыта', '7 квестов · 6 успешно',
  'master-agent-stats', 'Автономность', 'Тщательность', 'Размах', 'Выдержка',
  'master-agent-gear', 'Чтение файлов', '+ умение',
], 'лист персонажа')
// Тщательность у high — пять из пяти, выдержка у получаса — три: считаны от
// полей, а не написаны рядом с ними.
if (!sheet.includes('data-fill="100"') || !sheet.includes('data-fill="60"')) {
  fail('полосы характеристик не считаются от полей исполнителя')
}
// Правка поля меняет полосу. Здесь и ломается всякая «шкала для красоты».
const sheetHost = {
  dataset: { agentCard: sheetCard.id },
  querySelector: () => null,
  querySelectorAll: () => [],
}
readMasterAgentCardInput({ dataset: { agentField: 'reasoningEffort' }, value: 'none', closest: () => sheetHost })
if (masterAgentCardHtml(sheetCard, esc, deps).includes('data-fill="100"')) {
  fail('полоса тщательности не заметила, что усилие рассуждения сняли')
}

// 15. Черновик без послужного списка о нём молчит: уровня и класса ядро не
// давало, и выдумывать «уровень 1» карточке нечем.
const blankSheet = masterAgentCardHtml(masterAgentCardsFor({ workOrders: [order], hiring: [] })[0], esc, deps)
if (blankSheet.includes('уровень') || blankSheet.includes('квестов ·')) {
  fail('черновик получил уровень и послужной список, которых у него нет')
}
if (!blankSheet.includes('master-agent-mark')) {
  fail('портрет исчез вместе с отсутствующим уровнем')
}

// 16. Полоса и пустой слот ведут к настоящему полю, а не заводят второе.
//
// Второй редактор тех же значений однажды уже стоил двойного чтения формы
// (master-brief-panel.js), и здесь его нет: нажатие раскрывает «Права и
// пределы» и ставит курсор в поле, от которого полоса считается.
let focused = ''
const more = { open: false }
const sheetDom = {
  dataset: { agentCard: sheetCard.id },
  querySelector: selector => (selector === '.master-agent-more'
    ? more
    : { focus() { focused = selector } }),
}
handleMasterAgentCardAction('agent-card-open-more', {
  dataset: { card: sheetCard.id, field: 'reasoningEffort' },
  closest: () => sheetDom,
}, { cards: [sheetCard], render() { fail('открытие настройки не должно пересобирать ленту — каретка и раскрытие уедут') } })
if (!more.open) fail('нажатие на полосу не раскрыло «Права и пределы»')
if (focused !== '[data-agent-field="reasoningEffort"]') fail(`курсор встал не в то поле: ${focused}`)

console.log('smoke-master-agent-card: ok')
