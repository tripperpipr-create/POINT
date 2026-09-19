import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const moduleURL = pathToFileURL(path.join(root, 'vscode-extension', 'ui', 'client', 'master-work-order-v2.js')).href
const { masterWorkOrderCardsHtml } = await import(moduleURL)
const esc = value => String(value ?? '').replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;')
const base = {
  id: 'workorder-1', state: 'approved', version: 2, goal: 'Проверить управление',
  scope: [], criteria: [], roster: {}, network: [], secrets: [], sources: [], assumptions: [], outOfScope: [],
  workspace: {}, stack: {}, routing: {}, budget: {}, delivery: {},
}

const running = masterWorkOrderCardsHtml([{ ...base, runtime: { questId: 'quest-1', status: 'running' } }], esc)
for (const expected of ['data-control="pause"', 'data-control="cancel"', 'data-control="message"', 'data-work-order-message']) {
  if (!running.includes(expected)) throw new Error(`running WorkOrder lost control: ${expected}`)
}
if (running.includes('data-control="resume"')) throw new Error('running WorkOrder incorrectly offers Resume')

// Утверждённый наряд без рантайма — договор, по которому работа не пошла: квест
// не создан или уже удалён. Он обещал «выполнение отслеживается в квесте» и
// висел в ленте вечно, потому что убрать его было нечем.
const orphan = masterWorkOrderCardsHtml([{ ...base, runtime: undefined }], esc)
if (!orphan.includes('Квест не создан')) throw new Error('quest-less WorkOrder still claims it is being tracked')
if (orphan.includes('выполнение отслеживается в квесте')) throw new Error('quest-less WorkOrder promises tracking of nothing')
if (!orphan.includes('data-action="delete-work-order-v2"')) throw new Error('quest-less WorkOrder cannot be removed from the feed')
// У работающего наряда убирать нечего: сначала остановите квест.
if (running.includes('data-action="delete-work-order-v2"')) {
  throw new Error('running WorkOrder offers to delete the contract its work runs on')
}

const editable = masterWorkOrderCardsHtml([{ ...base, state: 'ready', digest: 'sha256:editable' }], esc)
for (const expected of ['data-action="save-master-work-order-v2"', 'data-work-order-field="goal"', 'data-work-order-json="criteria"', 'Профессиональные настройки']) {
  if (!editable.includes(expected)) throw new Error(`WorkOrder editor lost field: ${expected}`)
}

const paused = masterWorkOrderCardsHtml([{ ...base, runtime: { questId: 'quest-1', status: 'paused' } }], esc)
if (!paused.includes('data-control="resume"') || paused.includes('data-control="pause"')) {
  throw new Error('paused WorkOrder controls are incorrect')
}

// Блокировка: галочка успеха там врала, а выхода не было вовсе — только отмена.
const blocked = masterWorkOrderCardsHtml([{ ...base, runtime: { questId: 'quest-1', status: 'blocked', message: 'автономный проект требует Docker sandbox; выполнение на Windows автоматически не включается' } }], esc)
if (!blocked.includes('data-control="resume"') || !blocked.includes('Повторить запуск')) {
  throw new Error('blocked WorkOrder cannot be retried')
}
if (!blocked.includes('data-action="enable-docker-sandbox"')) {
  throw new Error('blocked WorkOrder does not offer the sandbox fix it names')
}
if (!blocked.includes('master-v2-approved is-attention') || blocked.includes('master-v2-approved is-done')) {
  throw new Error('blocked WorkOrder still reads as success')
}
if (blocked.includes('>✓ Заблокирован')) throw new Error('blocked WorkOrder keeps the success mark')

// Готовый квест — единственный, кому принадлежит галочка.
const done = masterWorkOrderCardsHtml([{ ...base, runtime: { questId: 'quest-1', status: 'completed' } }], esc)
if (!done.includes('master-v2-approved is-done') || !done.includes('✓ Готово')) {
  throw new Error('completed WorkOrder lost its success mark')
}

const completed = masterWorkOrderCardsHtml([{ ...base, runtime: { questId: 'quest-1', status: 'completed' } }], esc)
if (completed.includes('master-v2-runtime-controls')) throw new Error('completed WorkOrder still offers runtime mutation')

const delivered = masterWorkOrderCardsHtml([{ ...base, digest: 'sha256:brief', runtime: { questId: 'quest-1', status: 'completed', deliveryReceipt: { id: 'delivery-1', url: 'http://localhost:8080' }, evidence: { id: 'evidence-1', version: 3, verificationChecks: [{ id: 'tests', kind: 'automated_tests', command: 'npm test', exitCode: 0, satisfied: true }], changedFiles: ['src/app.js'], commitIds: ['abc123'], modelCalls: [{ inputTokens: 10, outputTokens: 5, costKnown: true, costCents: 2 }], workspaceRevision: 'sha256:tree' } } }], esc)
for (const expected of ['data-action="control-master-application-v2"', 'data-control="start"', 'data-control="stop"', 'http://localhost:8080', 'EvidenceBundle', 'npm test', '15 токенов', 'abc123']) {
  if (!delivered.includes(expected)) throw new Error(`delivered WorkOrder lost application control: ${expected}`)
}

// Исполнители создаются утверждением. Карточка обязана перестать обещать то,
// что уже сделано: иначе человек ищет создание агента, которого ядро создало.
const hiring = { ...base, roster: { permanent: [{ id: 'agentdraft-1', name: 'Разработчик проекта', role: 'Владелец реализации', requiresConsent: true }] } }
const pending = masterWorkOrderCardsHtml([{ ...hiring, state: 'ready', digest: 'sha256:hiring' }], esc)
if (!pending.includes('· будет создан')) throw new Error('roster draft lost its hiring promise before approval')
const hired = masterWorkOrderCardsHtml([{ ...hiring, runtime: { questId: 'quest-1', status: 'running', agentIds: ['agentdraft-1'] } }], esc)
if (!hired.includes('· создан') || hired.includes('· будет создан')) {
  throw new Error('approved WorkOrder still promises to create an agent it already created')
}

// Вебвью читается деревом, а не одним `main.js`: 19 сентября разбор ответов
// Мастера уехал в `ui/client/master-inbox.js`, и сторож чужого разговора
// перестал находиться там, где его искали.
const clientDir = path.join(root, 'vscode-extension', 'ui', 'client')
const clientFiles = fs.readdirSync(clientDir).filter((name) => name.endsWith('.js')).sort()
if (clientFiles.length < 10) {
  throw new Error(`webview package looks empty (${clientFiles.length} modules) — the checks below would pass blindly`)
}
const main = clientFiles.map((name) => fs.readFileSync(path.join(clientDir, name), 'utf8')).join('\n')
const transport = fs.readFileSync(path.join(root, 'vscode-extension', 'master-chat-controller.js'), 'utf8')
const host = fs.readFileSync(path.join(root, 'vscode-extension', 'extension.js'), 'utf8')
for (const [name, source] of [['webview', main], ['transport', transport], ['extension host', host]]) {
  if (!source.includes('controlMasterWorkOrderQuestV2')) throw new Error(`${name} does not route WorkOrder runtime controls`)
  if (!source.includes('controlMasterApplicationV2')) throw new Error(`${name} does not route delivered application controls`)
	if (!source.includes('reviseMasterWorkOrderV2')) throw new Error(`${name} does not route deterministic WorkOrder revision`)
}
if (!transport.includes("if(action==='resume')") || !transport.includes("this.credentialFor({connectionId},'утверждённого маршрута WorkOrder')")) {
  throw new Error('resume does not obtain the approved route credential from SecretStorage')
}

// Запуск идёт минутами и переживает свой запрос. Без наблюдения карточка
// замирала на «Проверяем окружение» независимо от того, что делал квест.
const watch = fs.readFileSync(path.join(root, 'vscode-extension', 'master-work-order-watch.js'), 'utf8')
for (const expected of ['/api/v2/work-orders/', "type: 'masterWorkOrder'", 'masterWorkOrderWatchers']) {
  if (!watch.includes(expected)) throw new Error(`work order watcher lost: ${expected}`)
}
if (!transport.includes('watchMasterWorkOrder(') || !transport.includes('watchMasterWorkOrders(')) {
  throw new Error('transport does not follow a live WorkOrder quest')
}
const stream = fs.readFileSync(path.join(root, 'vscode-extension', 'master-turn-stream.js'), 'utf8')
if (!stream.includes('watchMasterWorkOrder(')) throw new Error('finished master turn does not follow the quest it started')
if (!main.includes("message.conversationId!==masterClient.active")) {
  throw new Error('webview accepts a live WorkOrder update from another conversation')
}

console.log(JSON.stringify({ workOrderControls: ['pause', 'resume', 'cancel', 'message'], terminalMutation: false, liveQuestWatch: true }))
