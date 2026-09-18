// Разбор заголовка окна: что и в каком порядке стоит слева, по центру и справа.
//
// Порядок в заголовке задан флексом, а не разметкой: судить о нём по коду
// нельзя — нужен список того, что видно, с номерами и цветами.
import { applyScenario } from './lib/point-scenario.mjs'

const endpoint = process.argv[2] || 'http://127.0.0.1:9333'
const list = await fetch(`${endpoint}/json/list`).then(r => r.json())
const shell = list.find(t => t.type === 'page' && !String(t.url).startsWith('devtools://'))
if (!shell) throw new Error('Окно Point не найдено.')
const ws = new WebSocket(shell.webSocketDebuggerUrl)
await new Promise((res, rej) => { ws.addEventListener('open', res, { once: true }); ws.addEventListener('error', rej, { once: true }) })
let n = 0
const pending = new Map()
ws.addEventListener('message', e => {
  const m = JSON.parse(String(e.data))
  if (!m.id || !pending.has(m.id)) return
  const h = pending.get(m.id)
  pending.delete(m.id)
  m.error ? h.reject(new Error(m.error.message)) : h.resolve(m.result)
})
const cmd = (method, params = {}) => { const id = ++n; ws.send(JSON.stringify({ id, method, params })); return new Promise((resolve, reject) => pending.set(id, { resolve, reject })) }
const evaluate = expr => cmd('Runtime.evaluate', { returnByValue: true, expression: expr }).then(r => r.result?.value)
await cmd('Runtime.enable')
await applyScenario({ command: cmd, evaluate, scenario: process.env.POINT_SCENARIO || 'file' })

const report = await evaluate(`(() => {
  const describe = el => {
    const r = el.getBoundingClientRect()
    const s = getComputedStyle(el)
    return { класс: el.className, x: Math.round(r.x), ш: Math.round(r.width), порядок: s.order, цвет: s.color, рамка: s.borderColor, фон: s.backgroundColor }
  }
  const zone = selector => [...(document.querySelector(selector)?.children ?? [])].map(describe)
  const zoneBox = selector => { const el = document.querySelector(selector); if (!el) return null; const r = el.getBoundingClientRect(); const st = getComputedStyle(el); return { x: Math.round(r.x), ш: Math.round(r.width), выравнивание: st.justifyContent, показ: st.display, гибкость: st.flex }; }
  const run = document.querySelector('.part.titlebar .point-title-action-run')
  const search = document.querySelector('.part.titlebar .command-center-quick-pick')
  return {
    слева: zone('.part.titlebar .titlebar-left'),
    поЦентру: zone('.part.titlebar .titlebar-center'),
    справа: zone('.part.titlebar .titlebar-right'),
    кнопкаЗапуска: run ? describe(run) : null,
    поиск: search ? { текст: search.textContent, ...describe(search) } : null,
    области: { слева: zoneBox('.part.titlebar .titlebar-left'), центр: zoneBox('.part.titlebar .titlebar-center'), справа: zoneBox('.part.titlebar .titlebar-right') },
  }
})()`)
console.log(JSON.stringify(report, null, 2))
ws.close()
