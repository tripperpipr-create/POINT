// Проверка чипа ветки в заголовке: появляется ли имя ветки и когда.
//
// Чип берёт ветку из контекстного ключа SCM, а ключ наполняет расширение git
// уже после запуска. Замер «сразу после открытия окна» ничего не доказывает:
// нужно дождаться и увидеть, за сколько секунд имя доехало.
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

const snapshot = `(() => {
  const chip = document.querySelector('.part.titlebar .point-branch-chip')
  const items = [...document.querySelectorAll('.part.statusbar .statusbar-item')]
  return {
    чип: chip ? chip.textContent : null,
    видно: chip ? getComputedStyle(chip).display !== 'none' : false,
    строкаСостояния: items.map(i => (i.id || '') + '=' + i.textContent.trim()).filter(Boolean),
  }
})()`

let report = null
const started = Date.now()
for (let attempt = 0; attempt < 30; attempt++) {
  report = await evaluate(snapshot)
  if (report?.видно) break
  await new Promise(resolve => setTimeout(resolve, 1000))
}
console.log(JSON.stringify({ ...report, секунд: Math.round((Date.now() - started) / 1000) }, null, 2))
ws.close()
