// Композер Мастера: очередь во время хода, «↑» в пустом поле и команды «/».
//
// У очереди одно обещание и пять исключений из него: реплика уходит сама после
// ответа — кроме случаев, когда её писали, не видя того, что случилось потом.
// Каждое исключение ломается молча: реплика уходит ответом на невиданные
// вопросы или после «Стоп», которым человек как раз хотел всё прекратить.

import assert from 'node:assert/strict'
import { createMasterChatState } from '../vscode-extension/ui/client/master-chat-state.js'
import {
  MASTER_QUEUE_LIMIT, closeMasterSlash, handleMasterComposeKey, masterQueueAfterTurn, masterQueueHtml, masterQueueOf,
  masterQueuePause, masterQueuePush, masterRecallText, masterSlashHtml, masterSlashInput, masterSlashOpen, pickMasterSlash,
} from '../vscode-extension/ui/client/master-compose-keys.js'
import { esc } from '../vscode-extension/ui/client/html-escape.js'

// ——— Очередь ———
{
  const client = createMasterChatState()
  masterQueuePush(client, 'c1', 'раз')
  masterQueuePush(client, 'c1', 'два')
  assert.equal(masterQueueAfterTurn(client, 'c1', { status: 'completed', last: { role: 'assistant', content: 'готово' } })?.text, 'раз', 'после ответа уходит первая реплика')
  assert.equal(masterQueueOf(client, 'c1').items.length, 1, 'за ход уходит одна реплика')
  for (const [status, last, reason] of [
    ['completed', { role: 'assistant', questions: ['Какой бюджет?'] }, 'questions'],
    ['completed', { role: 'assistant', clarifications: [{ question: '?' }] }, 'questions'],
    ['cancelled', null, 'stopped'],
    ['failed', null, 'failed'],
  ]) {
    const queue = masterQueueOf(client, 'c1'); queue.paused = ''
    assert.equal(masterQueueAfterTurn(client, 'c1', { status, last }), null, `${reason}: очередь не уходит сама`)
    assert.equal(queue.paused, reason, `${reason}: пауза названа`)
  }
  masterQueueOf(client, 'c1').paused = ''
  masterQueuePause(client, 'c1', 'stopped')
  assert.equal(masterQueueAfterTurn(client, 'c1', { status: 'completed' }), null, 'остановленная очередь ждёт и после следующего хода')
  const html = masterQueueHtml(masterQueueOf(client, 'c1'), esc)
  assert.match(html, /hall-outbox is-paused/)
  assert.match(html, /Ход остановлен/)
  assert.match(html, /data-action="master-queue-send"/, 'на паузе отправка под рукой')
  assert.doesNotMatch(masterQueueHtml(masterQueueOf(client, 'c1'), esc, { sending: true }), /master-queue-send/, 'во время хода вне очереди не отправить')

  const full = createMasterChatState()
  for (let i = 0; i < MASTER_QUEUE_LIMIT; i++) assert.equal(masterQueuePush(full, 'c1', `r${i}`), true)
  assert.equal(masterQueuePush(full, 'c1', 'лишняя'), false, 'очередь не бесконечна')

  // Перезапуск панели: очередь цела, но сама не уходит.
  const restored = createMasterChatState(full.snapshot())
  assert.equal(masterQueueOf(restored, 'c1').items.length, MASTER_QUEUE_LIMIT, 'очередь пережила перезапуск')
  assert.equal(masterQueueOf(restored, 'c1').paused, 'reload', 'после перезапуска очередь ждёт второго взгляда')
  assert.equal(masterQueueAfterTurn(restored, 'c1', { status: 'completed' }), null)
}

// ——— «↑» в пустом поле ———
{
  const client = createMasterChatState()
  const history = [
    { role: 'user', content: 'Почини вебхук' },
    { role: 'assistant', content: 'Уточню', questions: ['Бюджет?'] },
    { role: 'user', content: 'Бюджет?\nОтвет: 10 000' },
  ]
  masterQueuePush(client, 'c1', 'из очереди')
  assert.equal(masterRecallText(client, 'c1', history), 'из очереди', 'сперва — последняя реплика очереди')
  assert.equal(masterQueueOf(client, 'c1').items.length, 0, 'возвращённая реплика ушла из очереди в поле')
  const recalled = masterRecallText(client, 'c1', history)
  assert.ok(recalled === 'Почини вебхук' || recalled === 'Бюджет?\nОтвет: 10 000', recalled)

  let draft = ''
  let renders = 0
  const deps = { client, id: () => 'c1', history: () => [{ role: 'user', content: 'Почини вебхук' }], setDraft: text => { draft = text }, draft: () => draft,
    persist() {}, render: () => { renders += 1 }, mentionOpen: () => false, root: { querySelector: () => null }, post() {}, openFind() {} }
  const key = (name, value = '', extra = {}) => handleMasterComposeKey({ key: name, target: { value }, shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, ...extra }, deps)
  assert.equal(key('ArrowUp', ''), true)
  assert.equal(draft, 'Почини вебхук', '«↑» в пустом поле вернул последнюю реплику')
  assert.equal(key('ArrowUp', 'уже набрано'), false, 'в непустом поле стрелка — это стрелка')
}

// ——— Команды «/» ———
{
  assert.equal(masterSlashInput('/пл', 3), true)
  assert.match(masterSlashHtml(esc), /Спланировать<small>\/план<\/small>/, 'русский запрос находит команду')
  assert.doesNotMatch(masterSlashHtml(esc), /Новый чат/, 'список сужается по запросу')
  assert.equal(masterSlashInput('/plan', 5), true)
  assert.match(masterSlashHtml(esc), /Спланировать/, 'латинский алиас тоже работает')
  assert.equal(masterSlashInput('Проверь /usr/bin', 16), true, 'закрытие списка — тоже изменение')
  assert.equal(masterSlashOpen(), false, '«/» посреди фразы — это текст, а не команда')
  assert.equal(masterSlashInput('/нет-такой', 10), true)
  assert.match(masterSlashHtml(esc), /Такой команды нет/)

  const clicked = []
  let draft = '/пл'
  const deps = { root: { querySelector: selector => ({ click: () => clicked.push(selector) }) }, setDraft: text => { draft = text }, draft: () => draft,
    post() {}, openFind() {}, render() {}, persist() {}, mentionOpen: () => false, client: createMasterChatState(), id: () => 'c1', history: () => [] }
  masterSlashInput(draft, draft.length)
  const handled = handleMasterComposeKey({ key: 'Enter', target: { value: draft }, shiftKey: false, isComposing: false }, deps)
  assert.equal(handled, true, 'Enter выбирает команду, а не отправляет «/пл»')
  assert.deepEqual(clicked, ['[data-action="master-session-workMode"][data-value="plan"]'], 'команда нажимает то, что уже есть в композере')
  assert.equal(draft, '', '«/запрос» убран из поля')
  assert.equal(masterSlashOpen(), false)

  const posted = []
  draft = '/нов'
  masterSlashInput(draft, draft.length)
  pickMasterSlash(0, { ...deps, post: message => posted.push(message) })
  assert.deepEqual(posted, [{ type: 'masterSession', action: 'new' }], '«/новый» открывает новый чат')
  closeMasterSlash()
}

console.log('композер Мастера: очередь, «↑» и команды «/»: PASS')
