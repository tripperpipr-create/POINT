// Замер хрома оболочки: высоты частей, рейки, вкладок и строки состояния.
//
// Нужен, чтобы сверять оболочку с макетом числами, а не на глаз: «заголовок
// 38 против 44» — это разговор, а «выглядит ниже» — нет.
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
  const box = selector => {
    const el = document.querySelector(selector)
    if (!el) return null
    const r = el.getBoundingClientRect()
    const s = getComputedStyle(el)
    return { ш: Math.round(r.width), в: Math.round(r.height), фон: s.backgroundColor, кегль: s.fontSize }
  }
  const chip = document.querySelector('.part.titlebar .point-branch-chip')
  const railItem = document.querySelector('.part.activitybar .action-item')
  const tab = document.querySelector('.part.editor .tab')
  const activeTab = document.querySelector('.part.editor .tab.active')
  const row = document.querySelector('.explorer-viewlet .monaco-list-row')
  const title = document.querySelector('.part.sidebar .composite.title')
  return {
    заголовокОкна: box('.part.titlebar'),
    рейкаСлева: box('.part.activitybar'),
    рейкаСправа: box('.part.auxiliarybar'),
    боковая: box('.part.sidebar'),
    заголовокРаздела: title ? { в: Math.round(title.getBoundingClientRect().height), кегль: getComputedStyle(title).fontSize } : null,
    строкаДерева: row ? { в: Math.round(row.getBoundingClientRect().height), кегль: getComputedStyle(row).fontSize } : null,
    полосаВкладок: box('.part.editor .tabs-container'),
    вкладка: tab ? { в: Math.round(tab.getBoundingClientRect().height), кегль: getComputedStyle(tab).fontSize } : null,
    активнаяВкладка: activeTab ? { верхняяЛиния: getComputedStyle(activeTab).borderTopWidth, цвет: getComputedStyle(activeTab).borderTopColor } : null,
    значокРейки: railItem ? { в: Math.round(railItem.getBoundingClientRect().height), значок: getComputedStyle(railItem.querySelector('.action-label') || railItem).fontSize } : null,
    строкаСостояния: box('.part.statusbar'),
    палитраСверху: box('.action-item.command-center-center'),
    чипВетки: chip ? { в: Math.round(chip.getBoundingClientRect().height), ш: Math.round(chip.getBoundingClientRect().width), текст: chip.textContent, видно: getComputedStyle(chip).display !== 'none' } : null,
    шрифтОболочки: getComputedStyle(document.querySelector('.monaco-workbench') || document.body).fontFamily,
  }
})()`)
console.log(JSON.stringify(report, null, 2))
ws.close()
