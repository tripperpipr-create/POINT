// Ход Мастера, пока он идёт: какая у него фаза и как он выглядит в каждой.
//
// Две беды, которые сторожит проверка, видны только глазу и только мгновение.
// Первая — прыжок на финише: живой след был списком, готовый ход — строкой
// сводки, и ответ уезжал вверх. Вторая — провал между событием `done` и
// приходом истории: реплика человека и ответ исчезали на это время вместе,
// потому что держались на признаке «идёт ход».

import assert from 'node:assert/strict'
import { createMasterChatState } from '../vscode-extension/ui/client/master-chat-state.js'
import { createMasterStreamView, masterStreamPhase } from '../vscode-extension/ui/client/master-stream-view.js'
import { createMasterTrail, trailModelFromItem } from '../vscode-extension/ui/client/master-trail.js'
import { createCompanionMarkdownFormatter } from '../vscode-extension/ui/client/companion-markdown.js'
import { esc } from '../vscode-extension/ui/client/html-escape.js'
import { countOf } from '../vscode-extension/ui/client/format-units.js'

// ——— Таблица фаз ———
const turn = extra => ({ id: 't1', conversationId: 'c1', status: 'streaming', reply: '', trace: [], ...extra })
const tool = { kind: 'tool', tool: 'read_file', argument: 'a.go', result: 'ok', running: false }
const cases = [
  ['нет хода', undefined, { running: false }, ''],
  ['отправлено, ядро молчит', undefined, { running: true }, 'wait'],
  ['ход идёт, рассказывать нечего', turn({ status: 'waiting' }), { running: true }, 'wait'],
  ['идёт след', turn({ status: 'tools', trace: [tool] }), { running: true }, 'trace'],
  ['пошёл текст', turn({ reply: 'Отвечаю', trace: [tool] }), { running: true }, 'text'],
  ['ход кончился, истории ещё нет', turn({ status: 'completed', reply: 'Готово' }), { running: false }, 'settling'],
  ['ход в истории своим turnId', turn({ status: 'completed', reply: 'Готово' }), { running: false, history: [{ role: 'assistant', turnId: 't1' }] }, ''],
  ['реплика человека с тем же turnId историей хода не считается', turn({ status: 'completed', reply: 'Готово' }), { running: false, history: [{ role: 'user', turnId: 't1' }] }, 'settling'],
  ['ход осел по приходу истории', turn({ status: 'completed', reply: 'Готово', settled: true }), { running: false }, ''],
  ['новая отправка поверх осевшего хода', turn({ status: 'completed', reply: 'Старое', settled: true }), { running: true }, 'wait'],
  ['поток оборвался', turn({ status: 'failed', reply: 'Начал', streamError: 'связь' }), { running: false }, 'failed'],
  ['пустой оконченный ход', turn({ status: 'completed' }), { running: false }, ''],
]
for (const [name, value, context, expected] of cases) assert.equal(masterStreamPhase(value, context), expected, name)

// ——— Разметка по фазам ———
const format = createCompanionMarkdownFormatter(esc)
const state = createMasterChatState()
const ui = {
  masterData: { history: [] }, masterSending: true, masterAutoFollow: true,
  masterOpenLive: new Set(), masterOpenReasoning: new Set(), masterOpenSteps: new Set(), masterExpandedSteps: new Set(),
  get masterTurn () { return state.turns[state.active] },
}
state.active = 'c1'
const view = createMasterStreamView({ root: { querySelector: () => null }, ui, esc, countOf, formatStreaming: format.streaming, replaceThread: () => {} })
const event = (type, detail, text) => state.acceptEvent({ turnId: 't1', conversationId: 'c1', type, text: text || '', detail: detail ? JSON.stringify(detail) : '' })

state.acceptTurn({ id: 't1', conversationId: 'c1', status: 'waiting', reply: '' })
assert.match(view.html(), /data-phase="wait"[\s\S]*agent-work-thinking[\s\S]*Думаю…/, 'ожидание — «Думаю…» на месте счётчика часов')

for (let i = 0; i < 8; i++) {
  event('tools', { round: 1, tool: 'read_file', argument: `file${i}.go` }, 'read_file')
  event('tool_result', { round: 1, tool: 'read_file', result: 'package app', failed: i === 5 })
}
event('tools', { round: 2, tool: 'search_text', argument: 'Webhook' }, 'search_text')
const traced = view.html()
assert.match(traced, /data-phase="trace"/)
assert.match(traced, /hall-live-earlier[^>]*><summary>ещё 6 шагов<\/summary>/, 'из девяти строк видно три, шесть — под «ещё»')
assert.match(traced, /hall-stream-status">Поиск по проекту…|hall-stream-status">[^<]+…/, 'строка состояния называет дело')

event('tool_result', { round: 2, tool: 'search_text', result: '[]' })
event('reply', null, 'Нашёл **обработчик**:\n```go\nfunc handle')
const texted = view.html()
assert.match(texted, /data-phase="text"/)
assert.doesNotMatch(texted, /hall-live-row/, 'с началом текста след — строка сводки, а не список')
assert.match(texted, /<strong>обработчик<\/strong>/, 'текст разбирается как markdown уже в потоке')
assert.match(texted, /<code>func handle<span class="hall-stream-caret"/, 'курсор — в конце открытого блока кода')
assert.match(texted, /hall-trail-fail[\s\S]*1 ошибка/, 'упавшее обращение названо в сводке')

// Сводка потока — та же строка, что встанет у готового хода.
const trail = createMasterTrail({ esc, countOf, ui }).masterTrailHtml
const finished = trail(trailModelFromItem({ id: 'm9', turnId: 't1', steps: ui.masterTurn.trace.filter(item => item.kind === 'tool'), reasoning: '' }))
const streamed = texted.slice(texted.indexOf('<div class="hall-trail'), texted.indexOf('<article'))
assert.equal(streamed.replace(/ is-running/g, '').replace(/\s+/g, ' ').trim(), finished.replace(/\s+/g, ' ').trim(), 'сводка следа в потоке совпадает со сводкой готового хода')

// Конец хода: курсора нет, текст и сводка на месте, пока не пришла история.
event('done', null, 'completed')
ui.masterSending = state.running()
const settled = view.html()
assert.match(settled, /data-phase="settling"/, 'между done и историей ход остаётся в ленте')
assert.doesNotMatch(settled, /hall-stream-caret/, 'после конца хода курсора нет')
assert.match(settled, /<strong>обработчик<\/strong>/)

state.settle('c1')
assert.equal(view.html(), '', 'пришла история — блок уходит, ответ стоит записью')

// Сбой потока: написанное остаётся, строка сбоя повторяет тот же вопрос.
state.acceptTurn({ id: 't2', conversationId: 'c1', status: 'streaming', reply: 'Начал отвечать' })
ui.masterSending = true
state.fail('c1', 'соединение с ядром потеряно', 'Почини вебхук')
ui.masterSending = state.running()
assert.equal(ui.masterSending, false, 'оборванный ход не держит отправку запертой')
const failed = view.html()
assert.match(failed, /data-phase="failed"[\s\S]*Начал отвечать/, 'написанное до сбоя остаётся')
assert.match(failed, /hall-turn-error[\s\S]*соединение с ядром потеряно/, 'строка сбоя называет причину')
assert.match(failed, /data-action="master-retry-turn" data-message="Почини вебхук"/, '«Повторить» задаёт тот же вопрос')

// Повторный приход того же хода не стирает след и отметку «осел».
state.acceptTurn({ id: 't3', conversationId: 'c1', status: 'streaming', reply: '' })
state.acceptEvent({ turnId: 't3', conversationId: 'c1', type: 'tools', text: 'read_file', detail: JSON.stringify({ tool: 'read_file' }) })
state.settle('c1')
state.acceptTurn({ id: 't3', conversationId: 'c1', status: 'completed', reply: 'x' })
assert.equal(state.turns.c1.trace.length, 1, 'след пережил повторный приход хода')
assert.equal(state.turns.c1.settled, true, 'осевший ход не всплывает заново')
assert.equal(state.snapshot().turns.c1.settled, true, 'снимок помечает не идущий ход осевшим')

console.log('Master stream view: phases, one trail, caret, settling and failure: PASS')
