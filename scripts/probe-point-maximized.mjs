// Оболочка в развёрнутом окне.
//
// Стенд поднимает окно фиксированного размера, и всё, что расходится только на
// широком экране — пустоты в заголовке, растянутые полосы, — на таком снимке не
// видно вовсе. Здесь окно разворачивается на весь экран, как у человека.
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

// Развернуть окно средствами Browser.* нельзя: в этом Electron домен не
// поднят. Растягиваем область просмотра — раскладка перестраивается так же,
// как на широком экране, и снимок стенда выходит того же размера.
const width = Number(process.env.POINT_WIDTH || 2560)
const height = Number(process.env.POINT_HEIGHT || 1400)
await cmd('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false })
await wait(1200)
await applyScenario({ command: cmd, evaluate, scenario: process.env.POINT_SCENARIO || 'file' })
await wait(800)

const report = await evaluate(`(() => {
  const box = selector => {
    const el = document.querySelector(selector)
    if (!el) return null
    const r = el.getBoundingClientRect()
    return { x: Math.round(r.x), ш: Math.round(r.width), в: Math.round(r.height) }
  }
  const chip = selector => { const el = document.querySelector(selector); if (!el) return null; const r = el.getBoundingClientRect(); return { x: Math.round(r.x), ш: Math.round(r.width) } }
  const gaps = []
  const bar = [...document.querySelectorAll('.part.titlebar .titlebar-left > *, .part.titlebar .titlebar-center > *, .part.titlebar .titlebar-right > *')]
    .map(el => ({ el, r: el.getBoundingClientRect() }))
    .filter(item => item.r.width > 0)
    .sort((a, b) => a.r.x - b.r.x)
  for (let i = 1; i < bar.length; i += 1) {
    const previous = bar[i - 1]
    const current = bar[i]
    const gap = Math.round(current.r.x - (previous.r.x + previous.r.width))
    if (gap > 24) gaps.push({ между: (previous.el.className || previous.el.tagName).slice(0, 30) + ' → ' + (current.el.className || current.el.tagName).slice(0, 30), просвет: gap })
  }
  return {
    окно: { ш: window.innerWidth, в: window.innerHeight },
    заголовок: box('.part.titlebar'),
    поиск: chip('.part.titlebar .command-center-quick-pick'),
    рядЗапуска: chip('.part.titlebar .point-run-actions'),
    ветка: chip('.part.titlebar .point-branch-chip'),
    боковая: box('.part.sidebar'),
    редактор: box('.part.editor'),
    правая: box('.part.auxiliarybar'),
    полосаВкладок: box('.part.editor .tabs-container'),
    крупныеПросветыВЗаголовке: gaps,
  }
})()`)
console.log(JSON.stringify(report, null, 2))
ws.close()
