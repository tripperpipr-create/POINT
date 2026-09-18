// Ряд меню из гамбургера: открывается нажатием, раскрывает список **наведением**
// и закрывается, когда указатель ушёл с панели.
//
// Проверка ведёт себя как человек: нажимает гамбургер, ведёт указатель по
// пунктам, потом уводит его в редактор. Наведение и уход тут одна пара — ряд
// закрывается мышью ровно потому, что мышью же и открывается: пока список
// требовал нажатия, уход указателя отнимал у руки цель на полпути.
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
const wait = ms => new Promise(r => setTimeout(r, ms))
await cmd('Runtime.enable')
await applyScenario({ command: cmd, evaluate, scenario })

const state = `(() => {
  const root = document.querySelector('.part.titlebar .titlebar-container')
  const bar = document.querySelector('.part.titlebar .menubar')
  const style = bar ? getComputedStyle(bar) : null
  const buttons = [...document.querySelectorAll('.part.titlebar .menubar-menu-button')]
    .filter(e => e.getBoundingClientRect().width > 0)
  const open = document.querySelector('.part.titlebar .menubar-menu-button.open')
  const holder = document.querySelector('.part.titlebar .menubar-menu-items-holder')
  return {
    развёрнут: Boolean(root && root.classList.contains('point-menu')),
    непрозрачность: style ? Number(style.opacity) : null,
    пунктов: buttons.length,
    первый: buttons[0]?.textContent?.trim() || null,
    списокОткрыт: open ? open.textContent.trim() : null,
    пунктовВСписке: holder ? holder.querySelectorAll('.action-menu-item').length : 0,
  }
})()`

const at = async selector => evaluate(`(() => {
  const el = document.querySelector(${JSON.stringify(selector)})
  if (!el) return null
  const b = el.getBoundingClientRect()
  return { x: Math.round(b.left + b.width / 2), y: Math.round(b.top + b.height / 2) }
})()`)

const click = async point => {
  for (const type of ['mousePressed', 'mouseReleased']) {
    await cmd('Input.dispatchMouseEvent', { type, x: point.x, y: point.y, button: 'left', clickCount: 1 })
  }
  await wait(500)
}
const moveTo = async point => { await cmd('Input.dispatchMouseEvent', { type: 'mouseMoved', x: point.x, y: point.y }); await wait(500) }

const nth = async index => evaluate(`(() => {
  const el = [...document.querySelectorAll('.part.titlebar .menubar-menu-button')]
    .filter(e => e.getBoundingClientRect().width > 0)[${index}]
  if (!el) return null
  const b = el.getBoundingClientRect()
  return { x: Math.round(b.left + b.width / 2), y: Math.round(b.top + b.height / 2) }
})()`)

const report = {}
const toggle = await at('.part.titlebar .point-menu-toggle')
if (!toggle) throw new Error('Гамбургер не найден')
report['до нажатия'] = await evaluate(state)
await click(toggle)
report['после нажатия'] = await evaluate(state)
// Наведение на пункт — без второго нажатия.
const first = await nth(0)
if (!first) throw new Error('Пункты меню не найдены')
await moveTo(first)
report['навели на первый'] = await evaluate(state)
// Соседний пункт подхватывает список на ходу.
const second = await nth(1)
if (second) {
  await moveTo(second)
  report['перевели на второй'] = await evaluate(state)
}
// Уводим указатель глубоко в редактор: ряд сворачивается вместе со списком.
await moveTo({ x: toggle.x + 500, y: 400 })
await wait(400)
report['мышь уведена'] = await evaluate(state)
// Открываем снова и закрываем щелчком мимо — он не ждёт задержки ухода.
await moveTo(toggle)
await click(toggle)
report['открыли снова'] = await evaluate(state)
await click({ x: toggle.x + 500, y: 400 })
report['щелчок мимо'] = await evaluate(state)
console.log(JSON.stringify(report, null, 2))
ws.close()
