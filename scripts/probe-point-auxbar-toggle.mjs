// Проверка требования: правая рейка не исчезает, а вкладка открывается и
// закрывается кликом по своей иконке.
//
// Меряется три состояния подряд на одном окне: исходное, после клика по
// активной иконке (должно свернуться до рейки), после повторного клика
// (должно развернуться обратно). Рейка обязана быть видима во всех трёх.
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
  const round = v => Math.round(v)
  const part = document.querySelector('.part.auxiliarybar')
  const rail = part && part.querySelector('.title')
  const content = part && part.querySelector('.content')
  const editor = document.querySelector('.part.editor')
  const active = part && part.querySelector('.action-item.checked, .action-item.active')
  const visible = el => { if (!el) return false; const s = getComputedStyle(el); const b = el.getBoundingClientRect(); return s.display !== 'none' && s.visibility !== 'hidden' && b.width > 0 && b.height > 0 }
  return {
    partWidth: part ? round(part.getBoundingClientRect().width) : null,
    partVisible: visible(part),
    railWidth: rail ? round(rail.getBoundingClientRect().width) : null,
    railVisible: visible(rail),
    contentWidth: content ? round(content.getBoundingClientRect().width) : null,
    editorWidth: editor ? round(editor.getBoundingClientRect().width) : null,
    activeIcon: active ? (active.getAttribute('aria-label') || '').slice(0, 24) : null,
    icons: part ? part.querySelectorAll('.composite-bar-container .action-item').length : 0,
  }
})()`

// Клик по активной иконке — настоящим событием мыши, а не .click():
// действие рейки слушает mousedown/up, и синтетический click его не запускает.
const clickActive = async () => {
  const point = await evaluate(`(() => {
    const part = document.querySelector('.part.auxiliarybar')
    const active = part && (part.querySelector('.action-item.checked') || part.querySelector('.action-item'))
    if (!active) return null
    const b = active.getBoundingClientRect()
    return { x: Math.round(b.left + b.width / 2), y: Math.round(b.top + b.height / 2) }
  })()`)
  if (!point) return false
  for (const type of ['mousePressed', 'mouseReleased']) {
    await cmd('Input.dispatchMouseEvent', { type, x: point.x, y: point.y, button: 'left', clickCount: 1 })
  }
  await wait(900)
  return true
}

// Кнопки в самой панели: «свернуть/скрыть» рядом с рейкой. Если какая-то из
// них прячет часть целиком, рейка исчезнет — а требование «всегда открыта»
// как раз про то, что она остаётся.
const panelButtons = await evaluate(`(() => {
  const part = document.querySelector('.part.auxiliarybar')
  if (!part) return []
  return [...part.querySelectorAll('.title a.action-label, .title-actions a, .title .action-item a')]
    .map(e => ({ label: (e.getAttribute('aria-label') || e.title || '').slice(0, 40), cls: String(e.className).slice(0, 60) }))
})()`)

const shots = { 'кнопки панели': panelButtons }
shots['1 исходное'] = await evaluate(state)
await clickActive()
shots['2 после клика по активной'] = await evaluate(state)
await clickActive()
shots['3 после повторного клика'] = await evaluate(state)

// Штатное скрытие части: если оно оставляет рейку — требование выполнено и для
// него; если убирает — нужен ещё один патч.
await evaluate(`(() => {
  const part = document.querySelector('.part.auxiliarybar')
  const hide = part && [...part.querySelectorAll('a.action-label, .action-item a')]
    .find(e => /скрыть|закрыть|hide|close/i.test((e.getAttribute('aria-label') || e.title || '')))
  if (hide) { hide.click(); return true }
  return false
})()`)
await wait(900)
shots['4 после кнопки скрытия'] = await evaluate(state)
console.log(JSON.stringify(shots, null, 2))
ws.close()
