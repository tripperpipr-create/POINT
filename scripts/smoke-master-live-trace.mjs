// Живой след хода: что видно в ленте, пока Мастер ещё отвечает.
//
// Ход рассказывал о себе одним словом — «Изучаю проект…» — и только задним
// числом, готовой репликой, показывал рассуждение и обращения к инструментам.
// Проверка сторожит три вещи, каждая из которых ломается молча: мысль
// склеивается из приростов, обращение закрывается своим исходом, а свёрнутая
// строка не выдаёт подробностей до нажатия.

import assert from 'node:assert/strict'
import { createMasterChatState, masterStreamHtml } from '../vscode-extension/ui/client/master-chat-state.js'
import { masterTraceTitle, masterTraceDuration } from '../vscode-extension/ui/client/master-live-trace.js'

const esc = value => String(value).replace(/[&<>"]/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[character]))
const state = createMasterChatState()
const event = (type, detail, text) => ({ turnId: 't1', conversationId: 'c1', type, text: text || '', detail: detail ? JSON.stringify(detail) : '' })

state.acceptTurn({ id: 't1', conversationId: 'c1', status: 'waiting', reply: '' })
state.acceptEvent(event('reasoning', { round: 1, delta: 'Сначала посмотрю, ' }))
state.acceptEvent(event('reasoning', { round: 1, delta: 'что уже есть в проекте.' }))
state.acceptEvent(event('tools', { round: 1, tool: 'read_file', argument: 'internal/app/app.go' }, 'read_file'))

const turn = state.turns.c1
const trace = turn.trace
assert.equal(trace.length, 2, 'мысль и обращение — две строки следа')
assert.equal(trace[0].text, 'Сначала посмотрю, что уже есть в проекте.', 'мысль собирается из приростов')
assert.equal(trace[0].running, false, 'начатое обращение закрывает мысль, которая к нему привела')
assert.equal(trace[1].running, true, 'идущее обращение помечено идущим')
assert.equal(turn.status, 'tools', 'строка ожидания называет работающий инструмент')

// Размышление не подменяет состояние хода: иначе со строки ожидания пропадало
// бы имя инструмента, который как раз работает.
state.acceptEvent(event('reasoning', { round: 1, delta: ' И ещё подумаю.' }))
assert.equal(turn.status, 'tools', 'мысль не сбивает состояние хода')

state.acceptEvent(event('tool_result', { round: 1, tool: 'read_file', result: 'файл не найден', failed: true, truncated: false }))
assert.equal(trace[1].running, false, 'исход закрывает обращение')
assert.equal(trace[1].failed, true, 'неудача названа неудачей')

assert.equal(masterTraceTitle(trace[0]), 'Размышление')
assert.equal(masterTraceTitle(trace[1]), 'Читаю файл')
assert.equal(masterTraceTitle({ kind: 'tool', tool: 'нет такого' }), 'Вызов инструмента', 'незнакомое имя не выдаём за знакомое')
assert.equal(masterTraceDuration({ startedAt: 1000, at: 1400 }), '', 'секунда ожидания — шум, а не сведения')
assert.equal(masterTraceDuration({ startedAt: 1000, at: 8000 }), '7 с')

// Повтор хода виден строкой и объясняет себя: первая попытка кончилась ничем,
// вторая идёт с бо́льшим пределом вывода. Без этой строки второй круг выглядит
// как зависшая модель — то же «Ожидаю модель…», только вдвое дольше.
state.acceptEvent(event('retry', { round: 1, reason: 'reasoning_budget', from: 8192, to: 32768, what: 'больше места на ответ' }, 'больше места на ответ'))
const retry = trace[trace.length - 1]
assert.equal(retry.kind, 'retry', 'повтор стоит в следе своей строкой')
assert.equal(turn.status, 'waiting', 'строка ожидания называет повтор')
assert.equal(turn.progress, 'больше места на ответ')
assert.equal(masterTraceTitle(retry), 'Вторая попытка')

const html = masterStreamHtml(turn, esc, new Set())
assert.match(html, /hall-live-row is-retry/, 'повтор помечен в разметке')
assert.match(html, /32768/, 'раскрытая строка повтора называет новый предел вывода')
assert.match(html, /hall-live-row is-mind is-done/, 'мысль стоит в следе строкой')
assert.match(html, /hall-live-row is-tool is-done is-failed/, 'неудачное обращение помечено в разметке')
assert.match(html, /<b>Читаю файл<\/b>/, 'свёрнутая строка называет дело по-русски')
assert.ok(!/<details class="hall-live-item"[^>]*\sopen/.test(html), 'по умолчанию подробности свёрнуты')
assert.match(html, /data-master-open="live" data-id="t1:1"/, 'раскрытие помнится по ключу строки')

const opened = masterStreamHtml(turn, esc, new Set(['t1:1']))
assert.match(opened, /data-id="t1:1" open/, 'раскрытая строка остаётся раскрытой после перерисовки')
assert.match(opened, /файл не найден/, 'раскрытая строка показывает исход обращения')

// След — про происходящее сейчас; к следующему открытию панели ход закончится
// своей репликой, и хранить его в снимке значит возить мегабайты впустую.
const snapshot = state.snapshot()
assert.equal(snapshot.turns.c1.trace, undefined, 'след не попадает в снимок состояния')

state.acceptEvent(event('done', null, 'completed'))
assert.ok(trace.every(item => !item.running), 'конец хода закрывает все строки следа')

console.log('Master live trace: mind deltas, tool outcome, collapsed by default: PASS')
