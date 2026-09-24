// Полоса идущего квеста над полем ввода.
//
// Полоса отвечает на два вопроса — где работа и кто её делает — и ошибается
// молча: показать «выполняется» у законченного квеста, потерять исполнителя
// или назвать состояние иначе, чем карточка наряда в ленте. Проверка сторожит
// эти три вещи и то, что без живой работы полосы нет вовсе.

import assert from 'node:assert/strict'
import { masterQuestStripHtml, masterQuestStripModel } from '../vscode-extension/ui/client/master-quest-strip.js'
import { runtimePresentation } from '../vscode-extension/ui/client/master-work-order-v2.js'
import { esc } from '../vscode-extension/ui/client/html-escape.js'

const order = (status, extra = {}) => ({
  id: 'wo-1',
  state: 'approved',
  goal: 'Health-эндпоинт и README',
  roster: { permanent: [{ id: 'dev', name: 'Разработчик проекта' }] },
  runtime: {
    status,
    updatedAt: '2026-09-24T10:00:00Z',
    stages: [
      { id: 's1', name: 'Развернуть Symfony', status: 'completed', agentId: 'dev' },
      { id: 's2', name: 'Добавить health-эндпоинт', status: 'running', agentId: 'dev' },
      { id: 's3', name: 'Проверить запуск', status: 'pending' },
    ],
    ...extra,
  },
})

// Без работы полосы нет.
assert.equal(masterQuestStripModel({ workOrders: [] }), null, 'пустой разговор не рисует полосу')
assert.equal(masterQuestStripModel({ workOrders: [{ ...order('running'), state: 'ready' }] }), null,
  'неутверждённый наряд — ещё не работа')
assert.equal(masterQuestStripModel({ workOrders: [order('completed')] }), null,
  'законченный квест держит итог в ленте, а не полосу')
assert.equal(masterQuestStripHtml(null, esc), '', 'нет модели — нет разметки')

// Идущая работа: этапы, исполнитель, подпись от карточки наряда.
const live = masterQuestStripModel({ workOrders: [order('running')], agentName: () => '' })
assert.equal(live.live, true)
assert.equal(live.done, 1)
assert.equal(live.total, 3)
assert.equal(live.stage, 'Добавить health-эндпоинт', 'текущий этап — тот, что идёт')
assert.equal(live.label, runtimePresentation({ status: 'running' }).label, 'полоса и карточка называют состояние одинаково')
assert.deepEqual(live.workers.map(worker => worker.name), ['Разработчик проекта'],
  'исполнитель найден по составу наряда, когда ростер его ещё не знает')
const html = masterQuestStripHtml(live, esc)
assert.match(html, /data-action="master-inspector-open"/, 'полоса ведёт к подробностям')
assert.match(html, /этап 2\/3/, 'идущий этап назван номером')
assert.match(html, /aria-label="Этапы: 1 из 3"/, 'шкала названа для читалки')
assert.match(html, /is-live/, 'живая работа помечена')

// Ждёт человека — полоса остаётся и меняет тон.
const blocked = masterQuestStripModel({ workOrders: [order('blocked')] })
assert.equal(blocked.live, false)
assert.equal(blocked.tone, 'is-attention', 'остановка читается предупреждением, а не работой')
assert.doesNotMatch(masterQuestStripHtml(blocked, esc), /is-live/)

// Ростер проекта важнее состава наряда: там актуальное имя.
const renamed = masterQuestStripModel({ workOrders: [order('running')], agentName: id => (id === 'dev' ? 'Кузнец' : '') })
assert.deepEqual(renamed.workers.map(worker => worker.name), ['Кузнец'])

// Из двух нарядов полоса говорит о последнем тронутом.
const older = { ...order('paused'), id: 'wo-0', runtime: { ...order('paused').runtime, updatedAt: '2026-09-24T09:00:00Z' } }
assert.equal(masterQuestStripModel({ workOrders: [older, order('running')] }).id, 'wo-1')

// Цель модели — недоверенный текст: в разметку она идёт экранированной.
const hostile = masterQuestStripModel({ workOrders: [{ ...order('running'), goal: '<img src=x onerror=alert(1)>' }] })
assert.doesNotMatch(masterQuestStripHtml(hostile, esc), /<img/)

console.log('полоса квеста: PASS')
