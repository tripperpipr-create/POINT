// Проверка двух требований: вкладки не исчезают из рейки при переключении,
// и внизу панели нет кнопок «развернуть» и «закрыть».
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

const rail = `(() => {
  const part = document.querySelector('.part.auxiliarybar')
  if (!part) return null
  const icons = [...part.querySelectorAll('.composite-bar-container .action-item a.action-label')]
    .filter(e => e.getBoundingClientRect().width > 0)
    .map(e => (e.getAttribute('aria-label') || '').slice(0, 20))
  // Служебные кнопки заголовка панели — «развернуть» и «скрыть».
  const titleButtons = [...part.querySelectorAll('a.action-label')]
    .filter(e => e.getBoundingClientRect().width > 0 && !e.closest('.composite-bar-container'))
    .map(e => (e.getAttribute('aria-label') || e.title || '').slice(0, 44))
    .filter(Boolean)
  const active = part.querySelector('.composite-bar-container .action-item.checked a.action-label')
  return { icons, count: icons.length, titleButtons,
    active: active ? (active.getAttribute('aria-label') || '').slice(0, 20) : null,
    partWidth: Math.round(part.getBoundingClientRect().width) }
})()`

const clickIcon = async index => {
  const point = await evaluate(`(() => {
    const part = document.querySelector('.part.auxiliarybar')
    const items = [...part.querySelectorAll('.composite-bar-container .action-item a.action-label')].filter(e => e.getBoundingClientRect().width > 0)
    const el = items[${index}]
    if (!el) return null
    const b = el.getBoundingClientRect()
    return { x: Math.round(b.left + b.width / 2), y: Math.round(b.top + b.height / 2) }
  })()`)
  if (!point) return false
  for (const type of ['mousePressed', 'mouseReleased']) {
    await cmd('Input.dispatchMouseEvent', { type, x: point.x, y: point.y, button: 'left', clickCount: 1 })
  }
  await wait(1100)
  return true
}

const report = { 'исходное': await evaluate(rail) }
// Переключаемся по всем вкладкам по очереди — именно на этом иконка пропадала.
for (let i = 1; i < (report['исходное']?.count || 0); i++) {
  await clickIcon(i)
  report[`после вкладки ${i + 1}`] = await evaluate(rail)
}
// Возвращаемся на первую и проверяем сворачивание кликом по активной.
await clickIcon(0)
report['вернулись на первую'] = await evaluate(rail)
await clickIcon(0)
report['клик по активной'] = await evaluate(rail)
console.log(JSON.stringify(report, null, 2))
ws.close()
