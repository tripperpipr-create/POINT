// Карточка найма: кем делать работу.
//
// Прежняя карточка «Кого нанять» знала один чертёж, ничего не знала о наряде и
// исчезала при перезагрузке. Здесь проверяется то, ради чего её заменили: видна
// причина, по которой исполнитель не готов; подходящего можно взять в наряд;
// помощник под готовым агентом не предлагается без разрешения; длинное имя не
// выезжает за карточку.
//
// Создание исполнителя отсюда ушло в свою карточку ленты — его проверяет
// scripts/smoke-master-agent-card.mjs.
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const clientURL = name => pathToFileURL(path.join(root, 'vscode-extension', 'ui', 'client', name)).href
const { masterHiringCardsHtml } = await import(clientURL('master-hiring-card.js'))
const { esc } = await import(clientURL('html-escape.js'))

const base = { workOrderId: 'workorder-1', goal: 'Собрать API', maxAgents: 2, allowSubagents: false }
const fail = message => { throw new Error(message) }
const expectAll = (html, expected, label) => {
  for (const text of expected) {
    if (!html.includes(text)) fail(`${label}: в карточке нет «${text}»`)
  }
}

// 1. Состав собран: исполнитель назван, причина выбора видна словами задачи.
const ready = masterHiringCardsHtml([{
  ...base, state: 'ready',
  selected: [{ agentId: 'agent-1', name: 'Backend', role: 'Backend-разработчик', readiness: 'READY', matched: ['api', 'health'] }],
}], esc)
expectAll(ready, ['Кем делать', 'Состав собран', 'Backend', 'совпало с задачей: api, health'], 'состав собран')

// 2. Заблокированный исполнитель: причина дословно и путь к починке.
const blocked = masterHiringCardsHtml([{
  ...base, state: 'blocked',
  selected: [{ agentId: 'agent-1', name: 'Backend', readiness: 'BLOCKED', blocking: ['лимит ходов не задан'] }],
}], esc)
expectAll(blocked, ['Исполнитель не готов', 'лимит ходов не задан', 'data-action="open-agent-constructor-edit"'], 'блокировка')

// 3. Подходящего можно взять в наряд, а заблокированного — нет: его сначала чинят.
const candidate = masterHiringCardsHtml([{
  ...base, state: 'candidate',
  selected: [{ agentId: 'agent-1', name: 'Backend', readiness: 'READY' }],
  considered: [
    { agentId: 'agent-2', name: 'QA', readiness: 'READY', whyNot: 'бюджет проекта — 2 агента' },
    { agentId: 'agent-3', name: 'Frontend', readiness: 'BLOCKED', blocking: ['модель не задана'] },
  ],
}], esc)
expectAll(candidate, ['data-action="hire-into-work-order"', 'data-agent-id="agent-2"', 'бюджет проекта — 2 агента'], 'кандидат')
if (candidate.includes('data-agent-id="agent-3"')) fail('заблокированного агента нельзя брать в наряд одной кнопкой')

// 4. Исполнителя нет и решать в карточке нечего: заводят его в своей карточке,
// которая стоит прямо над этой. Пустая рамка «Кем делать · Исполнителя нет»
// повторяла бы её заголовок и ничего не добавляла.
const create = masterHiringCardsHtml([{
  ...base, state: 'create',
  draft: { id: 'agentdraft-1', name: 'Backend-разработчик', role: 'Владелец серверной части', mission: 'Вести серверную часть', requiredTools: ['read_file', 'propose_patch'] },
}], esc)
if (create !== '') fail('карточка найма без кандидатов обязана молчать — создание ведёт карточка исполнителя')

// 5. Помощник: без права на новых исполнителей кнопка неактивна и объяснена.
const subagentForbidden = masterHiringCardsHtml([{
  ...base, state: 'subagent', allowSubagents: false,
  selected: [{ agentId: 'agent-1', name: 'Backend', readiness: 'READY' }],
  subagent: { parentAgentId: 'agent-1', role: 'testing', mission: 'Покрыть тестами', requiredTools: ['run_command'] },
  parentName: 'Backend',
}], esc)
expectAll(subagentForbidden, ['Помощник: testing', 'под «Backend»', 'disabled', 'не разрешено в задании'], 'помощник без права')

const subagentAllowed = masterHiringCardsHtml([{
  ...base, state: 'subagent', allowSubagents: true,
  selected: [{ agentId: 'agent-1', name: 'Backend', readiness: 'READY' }],
  subagent: { parentAgentId: 'agent-1', role: 'testing', mission: 'Покрыть тестами', requiredTools: ['run_command'] },
  parentName: 'Backend',
}], esc)
if (!subagentAllowed.includes('data-action="hire-add-subagent"')) fail('с разрешением помощника обязана быть кнопка')
if (/data-action="hire-add-subagent"[^>]*disabled/.test(subagentAllowed)) fail('с разрешением кнопка помощника не должна быть заблокирована')

// 6. Длинное имя кандидата не выезжает за карточку: подпись ограничена, полное
// имя остаётся в подсказке. Эта регрессия уже случалась.
const longName = 'КузнецОченьДлинноеИмяПерсонажаБезПробеловКотороеНиктоНеОграничивал'
const longCandidate = masterHiringCardsHtml([{
  ...base, state: 'candidate',
  selected: [{ agentId: 'agent-1', name: 'Backend', readiness: 'READY' }],
  considered: [{ agentId: 'agent-2', name: longName, readiness: 'READY', whyNot: 'бюджет проекта' }],
}], esc)
if (longCandidate.includes(`<b title="${longName}">${longName}</b>`)) fail('подпись кандидата обязана ограничивать длинное имя')
if (!longCandidate.includes(`title="${longName}"`)) fail('полное имя обязано остаться в подсказке')

// 7. Пустой список карточек не рисует ничего: найм — подсказка, а не рамка.
if (masterHiringCardsHtml([], esc) !== '') fail('без карточек найма лента обязана остаться чистой')

console.log('smoke-master-hiring-card: ok')
