// Выпадашки жетонов верхней панели: проект, ветка, запуск.
//
// Глазами такое не проверить: список рисуется поверх окна, живёт до первого
// клика мимо и состоит из полутора десятков строк. Зонд нажимает каждый жетон,
// снимает список целиком — подписи, состояние справа, галочку, стрелки в
// подменю, разделы, поиск, ширину — и закрывает его клавишей Esc, чтобы
// следующий замер начинался с чистого окна.
//
// Замер состояния не заменяет взгляда: срезанные хвосты букв и лишние кольца
// фокуса в числа не попадают. Крупный снимок панели обязателен наравне с ним.
//
//   node scripts/probe-point-title-widgets.mjs [endpoint]
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

// Снимок открытого списка. Жетоны раскрывают два разных вида: проект и ветка —
// свой виджет `.point-popup`, запуск — обычное меню Code-OSS. Зонд снимает оба
// одной меркой, иначе про половину панели пришлось бы верить на слово.
const snapshot = selector => `(() => {
  const chip = document.querySelector(${JSON.stringify(selector)})
  // Слой вида не выбрасывает разметку при закрытии, а прячет её и очищает при
  // следующем показе. Проверять надо видимость, иначе закрытый список
  // считается открытым.
  const visible = el => !!el && !!el.offsetParent
  const popup = [...document.querySelectorAll('.context-view .point-popup')].find(visible) || null
  const menu = [...document.querySelectorAll('.context-view.point-title-menu .monaco-menu')].find(visible) || null
  const view = popup || menu
  const rows = popup
    ? [...popup.querySelectorAll('.point-popup-row')].map(row => ({
        подпись: (row.querySelector('.point-popup-label')?.textContent || '').trim(),
        состояние: (row.querySelector('.point-popup-meta')?.textContent || '').trim() || null,
        метка: (row.querySelector('.point-popup-mark')?.textContent || '').trim() || null,
        значок: [...(row.querySelector('.point-popup-icon')?.classList || [])].find(name => name.startsWith('codicon-')) || null,
        отмечен: !!row.querySelector('.point-popup-check'),
        подменю: !!row.querySelector('.point-popup-more'),
      }))
    : menu
      ? [...menu.querySelectorAll('.monaco-action-bar.vertical .action-item')].map(item => {
          const label = item.querySelector('.action-label')
          if (label && label.classList.contains('separator')) { return { разделитель: true } }
          return {
            подпись: (label ? label.textContent : '').trim(),
            сочетание: (item.querySelector('.keybinding')?.textContent || '').trim() || null,
            выключен: item.classList.contains('disabled'),
          }
        })
      : null
  const box = view ? view.getBoundingClientRect() : null
  return {
    жетонРаскрыт: chip ? chip.classList.contains('point-widget-open') : null,
    фонЖетона: chip ? getComputedStyle(chip).backgroundColor : null,
    видВиджета: popup ? 'виджет' : menu ? 'меню' : null,
    поиск: popup ? !!popup.querySelector('.point-popup-search-input') : null,
    разделы: popup ? [...popup.querySelectorAll('.point-popup-section')].map(el => el.textContent.trim()) : null,
    ширина: box ? Math.round(box.width) : null,
    вОкне: box ? box.right <= innerWidth + 1 && box.bottom <= innerHeight + 1 : null,
    подЖетоном: box && chip ? Math.round(box.left - chip.getBoundingClientRect().left) : null,
    пунктов: rows ? rows.filter(row => !row.разделитель).length : null,
    пункты: rows,
  }
})()`

const open = async selector => {
  await evaluate(`document.querySelector(${JSON.stringify(selector)})?.click(), 'ok'`)
  // Недавние проекты приходят из главного процесса, список рисуется после них.
  for (let attempt = 0; attempt < 20; attempt++) {
    const report = await evaluate(snapshot(selector))
    if (report?.видВиджета) {
      // Подсветка жетона идёт переходом 120 мс, и снимок сразу после появления
      // списка ловит её в начале: `getComputedStyle` в этот момент отдаёт
      // промежуточное значение, а не то, что увидит глаз.
      await wait(300)
      return evaluate(snapshot(selector))
    }
    await wait(150)
  }
  return evaluate(snapshot(selector))
}
const dismiss = async () => {
  await cmd('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 })
  await cmd('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 })
  await wait(300)
}

// Набор в поле поиска: значение ставится и посылается `input`, как это делает
// клавиатура. Через CDP нажимать по букве дольше, а разницы для фильтра нет.
const искать = async (query, selector) => {
  await evaluate(`(() => { const input = document.querySelector('.point-popup-search-input'); if (!input) return 'none'; input.value = ${JSON.stringify(query)}; input.dispatchEvent(new Event('input', { bubbles: true })); return 'ok' })()`)
  await wait(300)
  const report = await evaluate(snapshot(selector))
  await evaluate(`(() => { const input = document.querySelector('.point-popup-search-input'); if (!input) return 'none'; input.value = ''; input.dispatchEvent(new Event('input', { bubbles: true })); return 'ok' })()`)
  await wait(300)
  return { запрос: query, пунктов: report?.пунктов ?? null, пункты: (report?.пункты || []).map(row => row.подпись) }
}

// Боковая панель раскрывается наведением, поэтому и проверяется наведением:
// клик по строке переключил бы ветку.
const навестиНаПодменю = async selector => {
  const point = await evaluate(`(() => { const row = [...document.querySelectorAll('.point-popup-row')].find(el => el.querySelector('.point-popup-more')); if (!row) return null; const b = row.getBoundingClientRect(); return { x: Math.round(b.x + 40), y: Math.round(b.y + b.height / 2) } })()`)
  if (!point) { return null }
  await cmd('Input.dispatchMouseEvent', { type: 'mouseMoved', x: point.x, y: point.y, button: 'none', clickCount: 0 })
  await wait(900)
  const state = await evaluate(`(() => {
    const popup = document.querySelector('.point-popup')
    const side = document.querySelector('.point-popup-side-open')
    if (!popup || !side) { return { панель: false } }
    const p = popup.getBoundingClientRect()
    const b = side.getBoundingClientRect()
    const owner = document.querySelector('.point-popup-row-expanded')
    return {
      панель: true,
      списокНаМесте: p.width > 0 && !!popup.offsetParent,
      строкаПодсвечена: !!owner,
      строка: owner ? (owner.querySelector('.point-popup-label')?.textContent || '').trim() : null,
      сбоку: Math.round(b.left - p.right),
      вОкне: b.right <= innerWidth + 1 && b.bottom <= innerHeight + 1,
      пункты: [...side.querySelectorAll('.point-popup-row')].map(row => ({
        подпись: (row.querySelector('.point-popup-label')?.textContent || '').trim(),
        выключен: row.classList.contains('point-popup-row-disabled'),
      })),
    }
  })()`)
  const box = await evaluate(`(() => { const row = [...document.querySelectorAll('.point-popup-row')].find(el => !el.querySelector('.point-popup-more') && !el.closest('.point-popup-side')); if (!row) return null; const b = row.getBoundingClientRect(); return { x: Math.round(b.x + 20), y: Math.round(b.y + b.height / 2) } })()`)
  if (box) {
    // Уводим указатель на строку без подменю: панель должна закрыться сама.
    await cmd('Input.dispatchMouseEvent', { type: 'mouseMoved', x: box.x, y: box.y, button: 'none', clickCount: 0 })
    await wait(700)
    state.закрываетсяУходом = !(await evaluate(`!!document.querySelector('.point-popup-side-open')`))
  }
  return state
}

// POINT_WIDGET_KEEP=project|git|run оставляет список раскрытым: снимок стенда
// делается после зонда, и иначе на нём всегда будет закрытая панель.
const keep = process.env.POINT_WIDGET_KEEP || ''
const жетоны = [
  ['project', 'проект', '.part.titlebar .point-project-switcher'],
  ['git', 'ветка', '.part.titlebar .point-branch-chip'],
  ['run', 'запуск', '.part.titlebar .point-run-chip'],
]
const отчёт = {}
for (const [id, имя, selector] of жетоны) {
  отчёт[имя] = await open(selector)
  if (имя === 'ветка') {
    // Поиск и боковая панель проверяются здесь же: закрыв список, к ним уже не
    // вернуться, а открывать его второй раз — мерить другое состояние.
    отчёт.поискПоВеткам = await искать('fix', selector)
    отчёт.боковаяПанель = await навестиНаПодменю(selector)
  }
  if (keep === id) { break }
  await dismiss()
  отчёт[`${имя}ПослеЗакрытия`] = await evaluate(snapshot(selector))
}

console.log(JSON.stringify(отчёт, null, 2))
ws.close()
