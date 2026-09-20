// Раскрытые подробности карточек ленты: одна память на четыре регистра.
//
// Проверяется то, ради чего память заведена: раскрывашка помнит ключ своего
// рода карточки; решение человека переживает перерисовку ленты; закрытое
// вручную не распахивается обратно умолчанием вида — а именно это и делала
// панель доказательств, которая открывает себя сама у завершённого прогона и
// перерисовывается опросом наряда каждые две с половиной секунды.
//
// Хранение в снимке состояния (`masterChat.cardOpen`) проверяет
// scripts/smoke-webview-persisted-state.js — здесь только поведение вида.
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const clientURL = name => pathToFileURL(path.join(root, 'vscode-extension', 'ui', 'client', name)).href
const { masterCardMoreAttrs, masterCardOpen, masterCardMoreHtml, useMasterCardOpen } = await import(clientURL('master-card-open.js'))
const { masterWorkOrderCardsHtml } = await import(clientURL('master-work-order-v2.js'))
const { masterAgentCardHtml } = await import(clientURL('master-agent-card.js'))
const { masterHiringCardsHtml } = await import(clientURL('master-hiring-card.js'))
const { esc } = await import(clientURL('html-escape.js'))

const fail = message => { throw new Error(message) }
const ok = label => console.log(`ok   ${label}`)

// Разговор — тот же, что у человека: карта раскрытий своя у каждого чата.
let conversation = 'pay'
const store = {}
useMasterCardOpen(() => (store[conversation] ||= {}))

// 1. Без решения человека работает умолчание вида — в обе стороны.
if (masterCardMoreAttrs('order:one', { esc }).includes(' open')) fail('умолчание «закрыто» не сработало')
if (!masterCardMoreAttrs('run-evidence:one', { esc, open: true }).includes(' open')) fail('умолчание «открыто» не сработало')
ok('нетронутая раскрывашка слушает вид')

// 2. Решение человека старше умолчания в обе стороны.
masterCardOpen.add('order:one')
masterCardOpen.delete('run-evidence:one')
if (!masterCardMoreAttrs('order:one', { esc }).includes(' open')) fail('открытое человеком не открылось')
if (masterCardMoreAttrs('run-evidence:one', { esc, open: true }).includes(' open')) {
  fail('закрытая вручную панель доказательств распахнулась обратно умолчанием')
}
ok('решение человека старше умолчания')

// 3. Карта своя у каждого разговора: соседний чат не наследует чужое.
conversation = 'ship'
if (masterCardMoreAttrs('order:one', { esc }).includes(' open')) fail('раскрытие утекло в соседний разговор')
conversation = 'pay'
ok('раскрытие принадлежит своему разговору')

// 4. «Забыть» возвращает умолчание — это не то же самое, что «закрыть».
masterCardOpen.forget('run-evidence:one')
if (!masterCardMoreAttrs('run-evidence:one', { esc, open: true }).includes(' open')) {
  fail('после forget умолчание не вернулось')
}
ok('forget возвращает умолчание')

// 5. Общая разметка подробностей несёт метку рода и экранирует ключ.
const more = masterCardMoreHtml('quest:<b>', 'Подробности', '<p>тело</p>', { esc })
if (!more.includes('data-master-open="card"')) fail('в общей разметке нет метки рода')
if (more.includes('data-id="quest:<b>"')) fail('ключ не экранирован')
ok('общая разметка помечена и экранирована')

// 6. Все четыре регистра карточек отдают свой ключ.
const order = {
  id: 'workorder-1', version: 2, state: 'ready', digest: 'sha256:x', goal: 'Собрать API',
  scope: ['endpoint'], criteria: [{ id: 'c1', text: 'health отвечает', kind: 'verification' }],
  budget: { tokens: 1000 }, roster: { agents: [] },
}
const orderHtml = masterWorkOrderCardsHtml([order], esc)
if (!orderHtml.includes('data-id="order:workorder-1"')) fail('карточка запуска не назвала свой ключ')

const agentHtml = masterAgentCardHtml({ id: 'order:workorder-1:agentdraft-1', kind: 'work-order', workOrderId: 'workorder-1', draft: { name: 'Сборщик' } }, esc)
if (!agentHtml.includes('data-id="agent:order:workorder-1:agentdraft-1"')) fail('карточка исполнителя не назвала свой ключ')

const hireHtml = masterHiringCardsHtml([{
  workOrderId: 'workorder-1', goal: 'Собрать API', maxAgents: 2, state: 'ready',
  selected: [{ agentId: 'a1', name: 'Backend', role: 'Backend', readiness: 'READY' }],
  considered: [{ agentId: 'a2', name: 'Fronted', role: 'Frontend', readiness: 'DEGRADED', blocking: ['нет модели'] }],
}], esc)
if (!hireHtml.includes('data-id="hire:workorder-1"')) fail('карточка найма не назвала свой ключ')
ok('каждый регистр отдаёт свой ключ')

// 7. Старых веток клика больше нет: `<details>` переключает себя сам, и полная
//    отрисовка раздела ради этого стоила каретки в полях той же карточки.
if (agentHtml.includes('data-action="agent-card-toggle"')) fail('карточка исполнителя всё ещё зовёт отрисовку на раскрытие')
if (hireHtml.includes('data-action="hire-toggle"')) fail('карточка найма всё ещё зовёт отрисовку на раскрытие')
ok('раскрытие не зовёт отрисовку')

console.log('Подробности карточек: память одна, решение человека старше умолчания: PASS')
