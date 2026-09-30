// «Что Point вынес из квеста» и «Навыки и развитие» в меню Мастера.
//
// Блок разбора стоит в карточке завершённого квеста и грузится по кнопке:
// отрисовка запросов не шлёт. Разбор связывает наблюдение, его источник и
// то, что из него выросло, а откат выросшего доступен прямо из блока.
// Панель развития Мастера раньше открывалась только с шагов онбординга.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { applyQuestRetrospectiveMessage, questRetrospectiveHtml, requestQuestRetrospective } from '../vscode-extension/ui/client/quest-retrospective-views.js'
import { masterSessionHtml } from '../vscode-extension/ui/client/master-session-views.js'
import { handleRosterClickAction } from '../vscode-extension/ui/client/roster-actions.js'
import { esc } from '../vscode-extension/ui/client/html-escape.js'

const order = status => ({ id: 'order-1', runtime: { questId: 'quest-1', status } })
const ui = {}
assert.equal(questRetrospectiveHtml(order('running'), ui, esc), '', 'live quest has no retrospective')
let html = questRetrospectiveHtml(order('completed'), ui, esc)
assert.ok(html.includes('Что Point вынес из квеста') && html.includes('data-action="load-quest-retrospective"'), 'load button')

const posted = []
const vscode = { postMessage(message) { posted.push(message) } }
const target = { dataset: { questId: 'quest-1' } }
assert.equal(handleRosterClickAction({ action: 'load-quest-retrospective', target, ui, vscode, render() {} }), true)
requestQuestRetrospective(target, ui, vscode, () => {})
assert.deepEqual(posted, [{ type: 'loadQuestRetrospective', questId: 'quest-1' }], 'one request per quest while loading')
assert.ok(questRetrospectiveHtml(order('completed'), ui, esc).includes('Собираем разбор'))

assert.equal(applyQuestRetrospectiveMessage({ type: 'statistics' }, ui, () => {}), false, 'foreign message untouched')
applyQuestRetrospectiveMessage({ type: 'questRetrospective', questId: 'quest-1', retrospective: {
  observations: [
    { source: 'master', kind: 'planning/plan_rejected', detail: 'этап требует Docker <b>там, где его нет</b>' },
    { source: 'human', kind: 'manual_review', outcome: 'rejected', detail: 'ui: окно не открывается' },
  ],
  masterLearning: [{ skillId: 'master-planning', phase: 'planning', status: 'canary' }],
  agentLearning: [{ id: 'improvement-1', skillName: 'Проверка перед отчётом', status: 'applied_unproven', canary: 'pending', canaryReasons: ['need 3 candidate runs; observed 1'], rollbackAvailable: true }],
} }, ui, () => {})
html = questRetrospectiveHtml(order('blocked'), ui, esc)
for (const text of ['Наблюдения', 'Мастер', 'Человек', 'окно не открывается', 'Обучение Мастера', 'пробное применение',
  'Обучение исполнителей', 'Проверка перед отчётом', 'польза не доказана', 'наблюдение', 'data-action="rollback-agent-improvement"']) {
  assert.ok(html.includes(text), text)
}
assert.ok(!html.includes('<b>там'), 'detail is escaped')

applyQuestRetrospectiveMessage({ type: 'questRetrospectiveError', questId: 'quest-1', error: 'foreign world' }, ui, () => {})
html = questRetrospectiveHtml(order('failed'), ui, esc)
assert.ok(html.includes('foreign world') && html.includes('Повторить'), 'error with retry')

applyQuestRetrospectiveMessage({ type: 'questRetrospective', questId: 'quest-1', retrospective: {} }, ui, () => {})
assert.ok(questRetrospectiveHtml(order('completed'), ui, esc).includes('не записано'), 'empty retrospective says so')

const sessions = { active: 'c1', items: [{ id: 'c1', title: 'Разговор' }], memoryEntries: [] }
const menu = masterSessionHtml(sessions, esc, 'development', '<section class="onboarding-panel master-development">панель</section>')
assert.ok(menu.includes('data-panel="development"') && menu.includes('Навыки и развитие') && menu.includes('панель'), 'development in master menu')
assert.ok(!masterSessionHtml(sessions, esc, '').includes('data-panel="development"'), 'no row without a panel')
// Сквозь настоящий media/main.js: ответ хоста доходит до карточки квеста,
// остановленного шлюзом (такой квест карточка держит в «живой» ветке).
const repo = path.resolve(import.meta.dirname, '..')
const stand = execFileSync(process.execPath, [path.join(repo, 'scripts', 'render-hub-surface.js'), 'master', 'work-order-blocked'], { cwd: repo, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
const block = stand.match(/<section class="quest-retrospective"[\s\S]*?<\/section>/)?.[0] || ''
assert.ok(block.includes('Проверка перед отчётом') && block.includes('Docker'), 'retrospective is not rendered in the blocked quest card')
console.log(JSON.stringify({ questRetrospective: 'ok', loadOnce: true, escaping: true, error: true, masterMenuDevelopment: true }))
