// Наряд утверждён, а дальше тишина.
//
// Карточка говорила «План создан, выполнение началось» и замолкала, пока узел
// Flow стоял с непрочитанной причиной отказа. Ни ошибки, ни этапов, ни того,
// что под наряд создали агента. Здесь проверяется обратное: причина затыка
// видна, этапы нарисованы, провал не притворяется успехом, а нового агента без
// согласия человека не создают.
import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const clientURL = name => pathToFileURL(path.join(root, 'vscode-extension', 'ui', 'client', name)).href
const { masterWorkOrderCardsHtml } = await import(clientURL('master-work-order-v2.js'))
const { masterAgentCardsFor, masterAgentCardsHtml, masterAgentConsent } = await import(clientURL('master-agent-card.js'))
const { workOrderExecutionHtml } = await import(clientURL('work-order-execution-views.js'))
const { esc } = await import(clientURL('html-escape.js'))
const ui = { state: {} }

const base = {
  id: 'workorder-1', state: 'approved', version: 2, goal: 'Собрать API',
  scope: ['health endpoint'], criteria: [{ id: 'health', text: 'GET /health = 200' }],
  roster: {}, network: [], secrets: [], sources: [], assumptions: [], outOfScope: [],
  workspace: {}, stack: {}, routing: { fixedModel: 'Qwen3.8-27B' }, budget: {}, delivery: {},
}
const startError = 'apply stage model binding: model "Qwen3.8-27B" is not in connection "connection_06b4cde" catalog'
const stalled = {
  ...base,
  runtime: {
    questId: 'quest-1', status: 'blocked', flowRunId: 'flowrun-1',
    stall: { nodeId: 'node-1', nodeName: 'Написать API', waitReason: 'start_failed', error: startError },
    stages: [
      { id: 'node-1', name: 'Написать API', kind: 'agent', status: 'waiting_agent', waitReason: 'start_failed', executionId: 'exec-1' },
      { id: 'node-2', name: 'Проверить', kind: 'agent', status: 'pending' },
    ],
  },
}

// 1. Причина затыка. Именно этот текст девять минут жил только в nodeStates.
const stalledHtml = masterWorkOrderCardsHtml([stalled], esc, new Set(), { ui })
for (const expected of ['work-order-exec', 'Исполнитель не запустился', esc(startError), 'Повторить запуск', 'data-control="resume"']) {
  if (!stalledHtml.includes(expected)) throw new Error(`stalled WorkOrder hides the reason: ${expected}`)
}

// 2. Этапы плана. Без них человек видел слово состояния и ничего больше.
for (const expected of ['hall-plan', 'Написать API', 'Проверить', 'Этапы · 0 из 2']) {
  if (!stalledHtml.includes(expected)) throw new Error(`execution screen lost its stages: ${expected}`)
}

// 3. Состав задания у утверждённого наряда сворачивается: решать в нём нечего,
// а место нужно тому, что происходит сейчас.
if (!stalledHtml.includes('Состав задания')) throw new Error('approved WorkOrder did not collapse its composition')
if (stalledHtml.includes('Проверить детали и разрешения')) {
  throw new Error('approved WorkOrder still spends the screen on pre-approval details')
}

// 3b. Ожидание ключа — такой же затык, как провал запуска.
//
// Узел стоял в waiting_agent с waitReason=waiting_api_key, причина не
// показывалась нигде, а квест оставался `running` — и кнопки возобновления не
// было, потому что `running` не возобновляем. Из этого состояния не было
// выхода вовсе: «Квест выполняется» у работы, которая не движется.
const waitingKey = {
  ...base,
  runtime: {
    questId: 'quest-1', status: 'awaiting_user', flowRunId: 'flowrun-1',
    stall: { nodeId: 'node-1', nodeName: 'Implement', waitReason: 'waiting_api_key' },
    stages: [
      { id: 'node-0', name: 'Bootstrap', status: 'completed' },
      { id: 'node-1', name: 'Implement', status: 'waiting_agent', waitReason: 'waiting_api_key' },
    ],
  },
}
const waitingHtml = masterWorkOrderCardsHtml([waitingKey], esc, new Set(), { ui })
for (const expected of ['Нужен ключ подключения', 'нужен ключ', 'data-control="resume"', 'Повторить запуск']) {
  if (!waitingHtml.includes(expected)) throw new Error(`quest waiting for a credential has no way out: ${expected}`)
}
// Кнопка только там, где ядро её примет. У квеста в `running` переход
// running -> preflight запрещён доменом, и «Повторить запуск» отдавал отказ:
// снаружи это выглядело как «нажал — и ничего не произошло».
const stillRunning = { ...waitingKey, runtime: { ...waitingKey.runtime, status: 'running' } }
const runningHtml = masterWorkOrderCardsHtml([stillRunning], esc, new Set(), { ui })
if (!runningHtml.includes('Нужен ключ подключения')) {
  throw new Error('a running quest whose flow is stalled must still name the reason')
}
if (runningHtml.includes('data-control="resume"')) {
  throw new Error('dead resume button: the core refuses resume from running, so pressing it does nothing')
}

if (waitingHtml.includes('Загружаем хронику')) {
  throw new Error('empty transcript still promises a chronicle that will never load')
}
// Цель стоит в шапке карточки; экран выполнения повторял её второй раз.
if ((waitingHtml.match(/Собрать API/g) || []).length !== 1) {
  throw new Error('execution screen repeats the goal already shown in the card header')
}

// 3c. Запуск убирает карточку из ленты и оставляет на её месте прогон.
//
// Карточка — предложение: её читают, правят и утверждают. После запуска решать
// в ней нечего, и «ЕДИНАЯ КАРТОЧКА ЗАПУСКА» над работающим квестом читалась как
// незакрытая форма, к которой надо вернуться. Поток работы при этом обязан
// лежать открытым: логи — то, ради чего на запущенный квест и смотрят.
const liveRun = {
  ...base,
  runtime: {
    questId: 'quest-1', status: 'running', flowRunId: 'flowrun-1',
    stages: [{ id: 'node-1', name: 'Написать API', status: 'running', runId: 'run-1' }],
  },
}
const liveUI = {
  state: {
    details: {
      run: { id: 'run-1', questId: 'quest-1', status: 'running', requestCount: 2, changedFiles: [] },
      events: [{ type: 'tool.finished', step: 1, data: { name: 'list_files', ok: true } }],
      approvals: [], patches: [],
    },
  },
}
const liveHtml = masterWorkOrderCardsHtml([liveRun], esc, new Set(), {
  ui: liveUI,
  agentWorkTranscriptHtml: () => '<div class="agent-work-transcript is-compact">поток</div>',
})
if (liveHtml.includes('master-v2-order')) throw new Error('running quest still renders the launch card in the feed')
if (liveHtml.includes('ЕДИНАЯ КАРТОЧКА ЗАПУСКА')) throw new Error('running quest still reads as a form awaiting approval')
if (!liveHtml.includes('master-v2-run')) throw new Error('running quest has no run block to replace the card')
if (!liveHtml.includes('agent-work-transcript')) throw new Error('running quest shows no work log')
if (liveHtml.includes('<details class="work-order-exec-log"')) {
  throw new Error('work log is hidden behind a disclosure on the screen built to show it')
}
// Цель и исход названы один раз: шапка прогона взяла их себе, и экран
// выполнения свою шапку больше не рисует.
if (liveHtml.includes('ВЫПОЛНЕНИЕ')) throw new Error('run block repeats the execution rubric under its own header')
if ((liveHtml.match(/Квест выполняется/g) || []).length !== 1) {
  throw new Error('run block states the same status twice')
}
// Сообщение активному квесту ищется от корня прогона, а не карточки.
if (!liveHtml.includes('data-work-order-message')) throw new Error('running quest lost the message field')
// До запуска карточка на месте: утверждать всё ещё есть что.
const beforeLaunch = masterWorkOrderCardsHtml([{ ...base, state: 'ready', digest: 'sha256:ready', runtime: undefined }], esc, new Set(), { ui })
if (!beforeLaunch.includes('master-v2-order') || !beforeLaunch.includes('ЕДИНАЯ КАРТОЧКА ЗАПУСКА')) {
  throw new Error('unapproved WorkOrder lost its launch card')
}

// 4. Провал — не зелёная галочка. Ключа failed не было ни в подписях, ни в
// знаках, ни в тонах, и все три словаря отдавали запасное значение «готово».
const failed = masterWorkOrderCardsHtml([{ ...base, runtime: { questId: 'quest-1', status: 'failed', message: 'исполнитель не запустился' } }], esc, new Set(), { ui })
if (failed.includes('master-v2-approved is-done')) throw new Error('failed WorkOrder still reads as success')
if (!failed.includes('✕ Провален')) throw new Error('failed WorkOrder lost its failure mark')
if (!failed.includes('data-control="resume"')) throw new Error('failed WorkOrder cannot be retried')

// 5. Примечание планировщика: план мог собрать движок, а не модель.
const templated = workOrderExecutionHtml({ ...base, runtime: { questId: 'quest-1', status: 'running', plannerNote: 'План собран движком Point: модель не ответила', stages: [] } }, ui, { esc })
if (!templated.includes('План собран движком Point')) throw new Error('planner fallback note never reaches the human')

// 6. Согласие на создание агента. Поле requiresConsent было флагом валидации, а
// теперь это шаг человека — и на сервере тоже. Сам черновик переехал в свою
// карточку ленты: внутри карточки запуска он был ярусом чужого документа.
const hiring = {
  ...base, id: 'workorder-2', state: 'ready', digest: 'sha256:hiring', runtime: undefined,
  roster: { permanent: [{ id: 'agentdraft-1', name: 'Разработчик проекта', role: 'Владелец реализации', mission: 'Собрать и проверить API', requiredTools: ['read_file'], requiresConsent: true }] },
}
masterAgentConsent.clear()
const beforeConsent = masterWorkOrderCardsHtml([hiring], esc, new Set(), { ui })
if (beforeConsent.includes('master-v2-consent"') || beforeConsent.includes('ЧЕРНОВИК АГЕНТА')) {
  throw new Error('the agent draft is back inside the launch card')
}
if (!/data-action="approve-master-work-order-v2"[^>]*disabled/.test(beforeConsent)) {
  throw new Error('launch button creates a new agent without asking the human first')
}
if (!beforeConsent.includes('Сначала заведите исполнителя')) {
  throw new Error('a locked launch button must say why it is locked')
}
// Черновик показан целиком — но своей карточкой, рядом с нарядом.
const agentCard = masterAgentCardsHtml(masterAgentCardsFor({ workOrders: [hiring], hiring: [] }), esc, {
  connections: [{ id: 'conn-1', displayName: 'Локальный Ollama', isDefault: true }], toolCatalog: [], connectionLabel: item => item.displayName,
})
for (const expected of ['Разработчик проекта', 'Владелец реализации', 'Собрать и проверить API', 'read_file', 'Qwen3.8-27B', 'Создать исполнителя']) {
  if (!agentCard.includes(expected)) throw new Error(`agent card lost: ${expected}`)
}
masterAgentConsent.add(hiring.id)
const withConsent = masterWorkOrderCardsHtml([hiring], esc, new Set(), { ui })
if (!withConsent.includes('data-action="approve-master-work-order-v2"') || /data-action="approve-master-work-order-v2"[^>]*disabled/.test(withConsent)) {
  throw new Error('after the human agreed, the launch button must work')
}
// Защита от двойного нажатия — тот же договор, что у карточки create_agent.
const busyConsent = masterWorkOrderCardsHtml([hiring], esc, new Set([hiring.id]), { ui })
if (!busyConsent.includes('Запускаем…') || !busyConsent.includes('disabled')) {
  throw new Error('consent card allows a second launch while the first one runs')
}
masterAgentConsent.clear()

// 7. Факт создания виден, и из карточки есть дорога в мастерскую агента.
const hired = masterWorkOrderCardsHtml([{ ...hiring, state: 'approved', runtime: { questId: 'quest-1', status: 'running', agentIds: ['agentdraft-1'], stages: [] } }], esc, new Set(), { ui })
if (!hired.includes('Агент создан') || !hired.includes('data-action="open-agent-constructor-edit"')) {
  throw new Error('created agent is invisible in the card that created it')
}

// 8. Данные до экрана доходят: наблюдатель просыпается на переходе этапа и
// тянет прогон, а утверждение доводит pending-executions до запуска.
const watch = fs.readFileSync(path.join(root, 'vscode-extension', 'master-work-order-watch.js'), 'utf8')
if (!watch.includes('runtime.stages') || !watch.includes('runtime.stall')) {
  throw new Error('watcher signature ignores stage transitions and keeps sleeping')
}
if (!watch.includes('host.loadRun(')) throw new Error('watcher never loads the run whose transcript the screen shows')
const transport = fs.readFileSync(path.join(root, 'vscode-extension', 'master-chat-controller.js'), 'utf8')
if (!transport.includes('coordinateActiveFlows()')) {
  throw new Error('v2 approval never starts pending executions, so activeRunId stays empty forever')
}
if (!transport.includes('rosterConsent')) throw new Error('transport drops the human consent on its way to the core')
// Глушилка гасила весь экран предложения, как только у него появлялся наряд v2.
const thread = fs.readFileSync(path.join(root, 'vscode-extension', 'ui', 'client', 'master-thread-views.js'), 'utf8')
if (/workOrders \|\| \[\]\)\.some\(order => order\.id === .workorder-\$\{id\}.\)\) return/.test(thread)) {
  throw new Error('the v2 silencer is back: a work order blanks the whole proposal in the feed')
}

console.log(JSON.stringify({ stallVisible: true, stages: 2, failedIsNotSuccess: true, rosterConsent: 'required', transcriptWired: true }))
