// Проверка шрифта в панелях: доехал ли Inter внутрь вебвью.
//
// Вебвью живёт под своей политикой безопасности и со своим адресом ресурсов.
// «Файлы выложены, стиль собран» ничего не доказывает: без `font-src` браузер
// молча откатывается на системный шрифт, и панель рядом с оболочкой читается
// чужой. Спрашиваем сам документ панели.
import { applyScenario } from './lib/point-scenario.mjs'

const endpoint = process.argv[2] || 'http://127.0.0.1:9333'
const list = await fetch(`${endpoint}/json/list`).then(r => r.json())
const shell = list.find(t => t.type === 'page' && !String(t.url).startsWith('devtools://'))
if (!shell) throw new Error('Окно Point не найдено.')
const socket = new WebSocket(shell.webSocketDebuggerUrl)
await new Promise((resolve, reject) => {
  socket.addEventListener('open', resolve, { once: true })
  socket.addEventListener('error', reject, { once: true })
})
let sequence = 0
const pending = new Map()
const sessions = new Set()
socket.addEventListener('message', event => {
  const message = JSON.parse(String(event.data))
  if (message.id && pending.has(message.id)) {
    const handler = pending.get(message.id)
    pending.delete(message.id)
    if (message.error) handler.reject(new Error(message.error.message))
    else handler.resolve(message.result)
    return
  }
  if (message.method === 'Target.attachedToTarget') {
    const sessionId = message.params?.sessionId
    if (!sessionId) return
    sessions.add(sessionId)
    void command('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: false, flatten: true }, sessionId).catch(() => {})
    void command('Runtime.enable', {}, sessionId).catch(() => {})
  }
})
function command(method, params = {}, sessionId) {
  const id = ++sequence
  socket.send(JSON.stringify(sessionId ? { id, method, params, sessionId } : { id, method, params }))
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }))
}
const evaluateIn = (sessionId, expression) => command('Runtime.evaluate', { returnByValue: true, expression, awaitPromise: true }, sessionId)
  .then(result => result.result?.value)
  .catch(() => undefined)
const evaluate = expression => evaluateIn(undefined, expression)
const wait = ms => new Promise(resolve => setTimeout(resolve, ms))

await command('Runtime.enable')
await command('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: false, flatten: true })
await applyScenario({ command, evaluate, scenario: process.env.POINT_SCENARIO || 'file' })
await wait(2500)

const SEEK = `const seek = doc => {
    try { if (doc?.body?.dataset?.layout) return doc } catch { return null }
    for (const frame of doc?.querySelectorAll?.('iframe') || []) {
      try { const found = seek(frame.contentDocument); if (found) return found } catch { /* чужой origin */ }
    }
    return null
  }`
const inPanel = expression => `(() => {
  ${SEEK}
  const document = seek(window.document)
  if (!document) return null
  return (${expression})
})()`

let panel
for (let attempt = 0; attempt < 12 && !panel; attempt += 1) {
  for (const sessionId of sessions) {
    if (await evaluateIn(sessionId, inPanel('true'))) { panel = sessionId; break }
  }
  if (!panel) await wait(700)
}
if (!panel) throw new Error('Документ панели не найден.')

const report = await evaluateIn(panel, inPanel(`(() => {
  const style = getComputedStyle(document.body)
  return {
    раскладка: document.body.dataset.layout,
    семейство: style.fontFamily,
    interЗагружен: document.fonts.check('500 12px Inter'),
    начертаний: [...document.fonts].filter(f => f.family === 'Inter').map(f => f.weight + ':' + f.status).join(', '),
    фон: style.backgroundColor,
  }
})()`))
console.log(JSON.stringify(report, null, 2))
socket.close()
