// Почему тело правой панели пусто. Находка цикла 22 подтверждалась четыре раза
// по одному признаку — «текста нет, iframe нет», — и этого мало, чтобы понять
// причину: пустым тело выглядит и когда представление не создано, и когда
// создано, но вебвью не загрузился, и когда панель показывает не тот контейнер.
// Зонд разбирает тело на части и отдельно спрашивает у оболочки, какой
// контейнер выбран и дошло ли дело до создания панели представления.
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

const snapshot = `(() => {
  const box = e => { const b = e.getBoundingClientRect(); return [Math.round(b.left), Math.round(b.top), Math.round(b.width), Math.round(b.height)].join(',') }
  const part = document.querySelector('.part.auxiliarybar')
  if (!part) return { part: null }
  const content = part.querySelector(':scope > .content')
  const describe = e => ({
    tag: e.tagName.toLowerCase(),
    cls: String(e.className || '').slice(0, 80),
    box: box(e),
    text: (e.innerText || '').trim().slice(0, 60),
  })
  const rail = [...part.querySelectorAll('.composite-bar-container .action-item a.action-label')]
    .filter(e => e.getBoundingClientRect().width > 0)
  return {
    part: box(part),
    content: content ? box(content) : null,
    // Дерево тела на два уровня: пустая .content и .content с пустой панелью —
    // разные диагнозы.
    contentTree: content ? [...content.children].map(child => ({
      ...describe(child),
      children: [...child.children].map(describe),
    })) : null,
    // Что оболочка успела создать для выбранного контейнера.
    panes: [...part.querySelectorAll('.pane')].map(p => ({
      id: p.getAttribute('data-view-id') || p.id || '',
      cls: String(p.className || ''),
      box: box(p),
      header: (p.querySelector('.pane-header')?.innerText || '').trim().slice(0, 40),
      expanded: p.getAttribute('aria-expanded'),
      // Полный разбор тела: «детей ноль» бывает и когда тела нет вовсе, и
      // когда оно есть, но пустое, и это разные диагнозы.
      body: (() => { const b = p.querySelector('.pane-body'); if (!b) return null
        return { box: box(b), cls: String(b.className || ''), children: [...b.children].map(c => ({ tag: c.tagName.toLowerCase(), cls: String(c.className || '').slice(0, 60), box: box(c) })) } })(),
    })),
    // Вебвью ищется по всему документу, а не внутри части: оболочка держит его
    // в собственном слое поверх панели, а не в теле панели. Поиск внутри
    // .part.auxiliarybar не находит ничего даже когда вебвью нарисован.
    webviews: [...document.querySelectorAll('iframe, webview')].map(e => ({
      tag: e.tagName.toLowerCase(),
      cls: String(e.className || '').slice(0, 60),
      box: box(e),
      src: String(e.getAttribute('src') || '').slice(0, 80),
      // Чей это слой, видно только по цепочке родителей.
      chain: (() => { const parts = []; let node = e.parentElement; while (node && parts.length < 4) { parts.push(String(node.className || node.tagName).split(' ')[0]); node = node.parentElement } return parts.join(' < ') })(),
      // Совпадение с прямоугольником панели отвечает на главный вопрос:
      // нарисован ли вебвью именно над правой панелью.
      overAuxiliary: (() => { const a = part.getBoundingClientRect(); const b = e.getBoundingClientRect(); return b.width > 0 && b.left >= a.left - 4 && b.right <= a.right + 4 })(),
    })),
    // Индикатор загрузки живёт ровно в том случае, когда оболочка ждёт
    // расширение: без него пустота означает, что ждать уже нечего.
    progress: [...part.querySelectorAll('.monaco-progress-container')].map(e => ({
      cls: String(e.className || ''), active: e.classList.contains('active'), box: box(e),
    })),
    welcome: [...part.querySelectorAll('.welcome-view, .welcome-view-content, .monaco-list-row')].length,
    rail: rail.map(e => (e.getAttribute('aria-label') || '').slice(0, 24)),
    active: rail.filter(e => e.closest('.action-item')?.classList.contains('checked'))
      .map(e => (e.getAttribute('aria-label') || '').slice(0, 24))[0] || null,
    // Расширение живо, если его показания стоят в статус-баре: тогда пустота
    // не про активацию, а про то, что представление не запрошено.
    statusPointItems: [...document.querySelectorAll('.part.statusbar .statusbar-item')]
      .map(e => e.id || '').filter(id => id.includes('point')),
    editorWidth: Math.round(document.querySelector('.part.editor')?.getBoundingClientRect().width || 0),
  }
})()`

const clickRail = async index => {
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
  await wait(1500)
  return true
}

const report = { 'при старте': await evaluate(snapshot) }
const railSize = report['при старте']?.rail?.length || 0
// Каждый инструмент проверяется отдельно: пустая панель у одного и у всех —
// разные причины, а по одному «Помощнику» этого не видно.
for (let i = 0; i < railSize; i++) {
  await clickRail(i)
  const state = await evaluate(snapshot)
  report[`вкладка ${i + 1}: ${state?.active || '—'}`] = state
}
console.log(JSON.stringify(report, null, 2))
ws.close()
