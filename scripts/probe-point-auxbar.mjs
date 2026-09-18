// Устройство правой панели: где рейка инструментов, какой ширины, что в теле.
// Нужно, чтобы понять, можно ли «закрыть вкладку», оставив рейку на экране.
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
  const bar = part.querySelector('.composite-bar, .composite-bar-container, .monaco-action-bar')
  const title = part.querySelector('.title, .composite.title')
  const content = part.querySelector('.content')
  const icons = [...part.querySelectorAll('.action-item.icon, .activity-bar .action-item, li.action-item')]
  return {
    part: box(part),
    title: title ? box(title) : null,
    content: content ? box(content) : null,
    bar: bar ? { box: box(bar), cls: String(bar.className).slice(0, 60) } : null,
    icons: icons.slice(0, 8).map(e => box(e) + ' | ' + (e.getAttribute('aria-label') || '').slice(0, 28)),
    contentText: content ? (content.innerText || '').trim().slice(0, 60) : null,
    activeCount: part.querySelectorAll('.action-item.checked, .action-item.active').length,
  }
})()`), null, 2))
ws.close()
