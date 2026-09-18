// Развёрнутый ряд меню в заголовке: нажимаем гамбургер и смотрим, что слои
// поменялись местами.
//
// Проверять глазами тут нечего: оба слоя занимают одно место, и «меню видно»
// на снимке ничего не говорит о том, кликабельны ли чипы под ним.
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
const wait = ms => new Promise(resolve => setTimeout(resolve, ms))
await cmd('Runtime.enable')
await applyScenario({ command: cmd, evaluate, scenario: process.env.POINT_SCENARIO || 'file' })

const snapshot = `(() => {
  const bar = document.querySelector('.part.titlebar')
  const container = document.querySelector('.part.titlebar .titlebar-container')
  const menubar = document.querySelector('.part.titlebar .menubar')
  const chip = document.querySelector('.part.titlebar .point-project-switcher')
  const buttons = [...document.querySelectorAll('.part.titlebar .menubar-menu-button')].slice(0, 6).map(el => {
    const s = getComputedStyle(el)
    return { текст: (el.textContent || '').trim(), кегль: s.fontSize + '/' + s.fontWeight, поля: s.padding, радиус: s.borderRadius }
  })
  const style = getComputedStyle(bar)
  const menuStyle = menubar ? getComputedStyle(menubar) : null
  return {
    развёрнут: container.classList.contains('point-menu'),
    фонПолосы: style.backgroundImage.slice(0, 64),
    кромка: style.boxShadow.slice(0, 48),
    менюВидно: menuStyle ? menuStyle.opacity : null,
    менюСобытия: menuStyle ? menuStyle.pointerEvents : null,
    пунктов: buttons.length,
    пункты: buttons,
    чипВиден: chip ? getComputedStyle(chip).opacity : null,
  }
})()`

const before = await evaluate(snapshot)
await evaluate(`document.querySelector('.part.titlebar .point-menu-toggle')?.click(), 'ok'`)
await wait(600)
const after = await evaluate(snapshot)
// POINT_MENU_KEEP=1 оставляет ряд развёрнутым — снимок стенда делается после
// пробы, и иначе на нём всегда будет свёрнутое состояние.
let back = null
if (process.env.POINT_MENU_KEEP !== '1') {
  await evaluate(`document.querySelector('.part.titlebar .point-menu-toggle')?.click(), 'ok'`)
  await wait(600)
  back = await evaluate(snapshot)
}
console.log(JSON.stringify({ свёрнуто: before, развёрнуто: after, свёрнутоСнова: back }, null, 2))
ws.close()
