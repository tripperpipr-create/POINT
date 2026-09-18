// Что за кнопки внизу правой панели и что убирает вкладку компаньона из рейки.
import { applyScenario } from './lib/point-scenario.mjs'

const endpoint = process.argv[2] || 'http://127.0.0.1:9333'
const scenario = process.env.POINT_SCENARIO || 'file'
const list = await fetch(`${endpoint}/json/list`).then(r => r.json())
const shell = list.find(t => t.type === 'page' && !String(t.url).startsWith('devtools://'))
const ws = new WebSocket(shell.webSocketDebuggerUrl)
await new Promise((res, rej) => { ws.addEventListener('open', res, { once: true }); ws.addEventListener('error', rej, { once: true }) })
let n = 0; const pending = new Map()
ws.addEventListener('message', e => {
  const m = JSON.parse(String(e.data)); if (!m.id || !pending.has(m.id)) return
  const h = pending.get(m.id); pending.delete(m.id)
  m.error ? h.reject(new Error(m.error.message)) : h.resolve(m.result)
})
const cmd = (method, params = {}) => { const id = ++n; ws.send(JSON.stringify({ id, method, params })); return new Promise((resolve, reject) => pending.set(id, { resolve, reject })) }
const evaluate = expr => cmd('Runtime.evaluate', { returnByValue: true, expression: expr }).then(r => r.result?.value)
await cmd('Runtime.enable')
await applyScenario({ command: cmd, evaluate, scenario })

console.log(JSON.stringify(await evaluate(`(() => {
  const box = e => { const b = e.getBoundingClientRect(); return [Math.round(b.left), Math.round(b.top), Math.round(b.width), Math.round(b.height)].join(',') }
  const part = document.querySelector('.part.auxiliarybar')
  if (!part) return { part: null }
  // Все интерактивные элементы части с их подписями и классами, чтобы отличить
  // иконки инструментов от служебных кнопок панели.
  const items = [...part.querySelectorAll('a.action-label, .action-item > a, .action-item > .action-label')]
    .filter(e => e.getBoundingClientRect().width > 0)
    .map(e => ({
      box: box(e),
      label: (e.getAttribute('aria-label') || e.title || '').slice(0, 44),
      cls: String(e.className).slice(0, 70),
      inRail: !!e.closest('.composite-bar-container'),
    }))
  const titleActions = [...part.querySelectorAll('.title-actions a, .title .actions-container a')]
    .filter(e => e.getBoundingClientRect().width > 0)
    .map(e => ({ box: box(e), label: (e.getAttribute('aria-label') || e.title || '').slice(0, 44), cls: String(e.className).slice(0, 70) }))
  return { part: box(part), items, titleActions }
})()`), null, 2))
ws.close()
