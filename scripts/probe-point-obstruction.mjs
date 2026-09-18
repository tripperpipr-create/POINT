// Разбор находки «перекрыт другим элементом»: кто именно кого закрывает, какой
// геометрией и в каком слое. Общий аудит называет пару, но починить можно
// только зная, что лежит сверху и почему.
//
// Вебвью Point вложенный: внешний документ держит только iframe, разметка
// расширения живёт в дочернем фрейме со своим контекстом исполнения. Обычный
// Runtime.evaluate попадает во внешний и видит один элемент — первая версия
// этого зонда молча отвечала «перекрытий нет» именно поэтому.
//
//   powershell -File scripts/test-point-workbench-design.ps1 -Probe scripts/probe-point-obstruction.mjs
const endpoint = process.argv[2] || 'http://127.0.0.1:9333'
const list = await fetch(`${endpoint}/json/list`).then(r => r.json())
const targets = list.filter(t => String(t.url).startsWith('vscode-webview://') && t.webSocketDebuggerUrl)
if (!targets.length) { console.log('вебвью не найдено'); process.exit(0) }

const expression = `(() => {
  const name = e => (e.tagName.toLowerCase() + (e.className && typeof e.className === 'string' ? '.' + e.className.trim().split(/\s+/).slice(0,3).join('.') : '')).slice(0,48)
  const geo = e => { const b = e.getBoundingClientRect(); const s = getComputedStyle(e)
    return { box: [Math.round(b.left), Math.round(b.top), Math.round(b.width), Math.round(b.height)].join(','),
      position: s.position, z: s.zIndex, margin: s.margin, transform: s.transform === 'none' ? '' : s.transform,
      pointerEvents: s.pointerEvents, overflow: s.overflow } }
  const buttons = [...document.querySelectorAll('button, a[href], [role="button"]')]
  const out = []
  for (const el of buttons) {
    const s = getComputedStyle(el); if (s.display === 'none' || s.visibility === 'hidden') continue
    const b = el.getBoundingClientRect(); if (b.width < 2 || b.height < 2) continue
    const x = b.left + b.width/2, y = b.top + b.height/2
    if (x < 0 || y < 0 || x > document.documentElement.clientWidth || y > document.documentElement.clientHeight) continue
    const hit = document.elementFromPoint(x, y)
    if (!hit || hit === el || el.contains(hit) || hit.contains(el)) continue
    out.push({ target: name(el), text: (el.textContent||'').trim().slice(0,32), targetGeo: geo(el),
      cover: name(hit), coverText: (hit.textContent||'').trim().slice(0,32), coverGeo: geo(hit),
      chain: (() => { const p = []; let n = el.parentElement
        while (n && p.length < 7) { const s2 = getComputedStyle(n)
          p.push(name(n) + ' box=' + geo(n).box + ' pos=' + s2.position + ' z=' + s2.zIndex
            + (s2.isolation !== 'auto' ? ' isolation=' + s2.isolation : '')
            + (s2.transform !== 'none' ? ' transform' : '') + (s2.filter !== 'none' ? ' filter' : '')
            + (+s2.opacity < 1 ? ' opacity=' + s2.opacity : '')
            + (s2.willChange !== 'auto' ? ' will-change=' + s2.willChange : '')
            + (s2.contain !== 'none' ? ' contain=' + s2.contain : ''))
          n = n.parentElement }
        return p })(),
      coverChain: (() => { const p = []; let n = hit
        while (n && p.length < 7) { const s2 = getComputedStyle(n)
          p.push(name(n) + ' pos=' + s2.position + ' z=' + s2.zIndex
            + (s2.isolation !== 'auto' ? ' isolation=' + s2.isolation : '')
            + (s2.transform !== 'none' ? ' transform' : '')
            + (+s2.opacity < 1 ? ' opacity=' + s2.opacity : ''))
          n = n.parentElement }
        return p })() })
  }
  // Отдельно — состояние выпадающего меню: открыто ли оно и что говорит о себе
  // сама панель. Без этого «перекрыт» читается как ошибка слоя, а причиной
  // может быть и закрытое меню, и снятые события указателя.
  const menu = document.querySelector('.companion-chat-menu')
  const panel = menu && menu.querySelector(':scope > div')
  const menuInfo = menu ? { open: menu.open, hasPanel: !!panel, panel: panel ? (() => { const s = getComputedStyle(panel)
    const b = panel.getBoundingClientRect()
    return { box: [Math.round(b.left), Math.round(b.top), Math.round(b.width), Math.round(b.height)].join(','),
      display: s.display, visibility: s.visibility, opacity: s.opacity, pointerEvents: s.pointerEvents,
      position: s.position, z: s.zIndex, contentVisibility: s.contentVisibility, clipPath: s.clipPath } })() : null } : null
  return { buttons: buttons.length, viewport: document.documentElement.clientWidth + 'x' + document.documentElement.clientHeight, menuInfo, found: out.slice(0, 1) }
})()`

for (const target of targets) {
  const ws = new WebSocket(target.webSocketDebuggerUrl)
  await new Promise((res, rej) => { ws.addEventListener('open', res, { once: true }); ws.addEventListener('error', rej, { once: true }) })
  let n = 0; const pending = new Map(); const events = []
  ws.addEventListener('message', e => {
    const m = JSON.parse(String(e.data))
    if (!m.id) { events.push(m); return }
    if (!pending.has(m.id)) return
    const h = pending.get(m.id); pending.delete(m.id)
    m.error ? h.reject(new Error(m.error.message)) : h.resolve(m.result)
  })
  const cmd = (method, params = {}) => { const id = ++n; ws.send(JSON.stringify({ id, method, params })); return new Promise((resolve, reject) => pending.set(id, { resolve, reject })) }
  await cmd('Runtime.enable')
  await new Promise(r => setTimeout(r, 400))
  const contexts = events.filter(m => m.method === 'Runtime.executionContextCreated').map(m => m.params.context)
  let best = null
  for (const context of contexts) {
    const got = await cmd('Runtime.evaluate', { expression, returnByValue: true, contextId: context.id })
      .then(r => (r.exceptionDetails ? null : r.result?.value)).catch(() => null)
    if (got && (!best || got.buttons > best.buttons)) best = got
  }
  console.log(JSON.stringify({ target: target.url.slice(0, 46), contexts: contexts.length, ...(best || { buttons: 0, found: [] }) }, null, 2))
  ws.close()
}
