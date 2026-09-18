// Живой замер Git-панели: папки изменений, отметки и статусы файлов.
//
// Разметка панели живёт в вебвью, а вебвью — чужой origin, то есть отдельная
// цель отладчика: `document.querySelector` в окне Code-OSS доходит только до
// <iframe> и дальше слепнет, а в `/json/list` такие цели не показываются.
// Поэтому здесь одно соединение и много сессий: подключаемся к окну, просим
// авто-подключение ко всем вложенным целям (у вебвью их две — оболочка вебвью
// и сам документ) и спрашиваем каждую, не она ли Git-панель.
import { applyScenario } from './lib/point-scenario.mjs'

const endpoint = process.argv[2] || 'http://127.0.0.1:9333'
const wait = ms => new Promise(resolve => setTimeout(resolve, ms))

const targets = await fetch(`${endpoint}/json/list`).then(response => response.json())
const shellTarget = targets.find(item => item.type === 'page' && !String(item.url).startsWith('devtools://'))
if (!shellTarget) throw new Error('Окно Point не найдено — оболочка не поднялась.')

const socket = new WebSocket(shellTarget.webSocketDebuggerUrl)
await new Promise((resolve, reject) => {
  socket.addEventListener('open', resolve, { once: true })
  socket.addEventListener('error', reject, { once: true })
})
let sequence = 0
const pending = new Map()
const sessions = new Set()
socket.addEventListener('message', event => {
  const message = JSON.parse(String(event.data))
  if (message.id && pending.has(message.id)) {
    const handler = pending.get(message.id)
    pending.delete(message.id)
    if (message.error) handler.reject(new Error(message.error.message))
    else handler.resolve(message.result)
    return
  }
  if (message.method === 'Target.attachedToTarget') {
    const sessionId = message.params?.sessionId
    if (!sessionId) return
    sessions.add(sessionId)
    // Вложенные цели тоже могут иметь свои: документ вебвью лежит внутри
    // оболочки вебвью, и без этого шага он остаётся невидимым.
    void command('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: false, flatten: true }, sessionId).catch(() => {})
    void command('Runtime.enable', {}, sessionId).catch(() => {})
  }
})
function command(method, params = {}, sessionId) {
  const id = ++sequence
  socket.send(JSON.stringify(sessionId ? { id, method, params, sessionId } : { id, method, params }))
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }))
}
const evaluateIn = (sessionId, expression) => command('Runtime.evaluate', { returnByValue: true, expression }, sessionId)
  .then(result => result.result?.value)
  .catch(() => undefined)
const evaluate = expression => evaluateIn(undefined, expression)

await command('Runtime.enable')
await command('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: false, flatten: true })
await applyScenario({ command, evaluate, scenario: process.env.POINT_SCENARIO || 'idle' })

// Вкладку ищем в обеих рейках: Git живёт слева, инструменты — справа.
const clickRail = async label => {
  const point = await evaluate(`(() => {
    const items = [...document.querySelectorAll('.part.activitybar .action-item a.action-label, .part.auxiliarybar .composite-bar-container .action-item a.action-label')]
      .filter(element => element.getBoundingClientRect().width > 0)
    const item = items.find(element => (element.getAttribute('aria-label') || '').includes(${JSON.stringify(label)}))
    if (!item) return null
    const box = item.getBoundingClientRect()
    return { x: Math.round(box.left + box.width / 2), y: Math.round(box.top + box.height / 2) }
  })()`)
  if (!point) throw new Error(`Вкладка «${label}» в рейках не найдена.`)
  for (const type of ['mousePressed', 'mouseReleased']) {
    await command('Input.dispatchMouseEvent', { type, x: point.x, y: point.y, button: 'left', clickCount: 1 })
  }
  await wait(1800)
}
await clickRail('Git')

// Содержимое вебвью лежит во вложенном <iframe> того же origin, отдельной цели
// у него нет. Поэтому ищем документ обходом кадров, а выражения выполняем в
// найденном документе — он подменяет `document` внутри выражения.
const SEEK = `const seek = doc => {
    try { if (doc?.body?.dataset?.layout === 'tool-git') return doc } catch { return null }
    for (const frame of doc?.querySelectorAll?.('iframe') || []) {
      try { const found = seek(frame.contentDocument); if (found) return found } catch { /* чужой origin */ }
    }
    return null
  }`
const inPanel = expression => `(() => {
  ${SEEK}
  const document = seek(window.document)
  if (!document) return null
  const css = element => element.ownerDocument.defaultView.getComputedStyle(element)
  return (${expression})
})()`

let panel
for (let attempt = 0; attempt < 15 && !panel; attempt += 1) {
  for (const sessionId of sessions) {
    const found = await evaluateIn(sessionId, inPanel('true'))
    if (found) { panel = sessionId; break }
  }
  if (!panel) await wait(1000)
}
if (!panel) throw new Error(`Вебвью Git-панели не открылось (сессий: ${sessions.size}).`)

const readPanel = `(() => {
  const groups = [...document.querySelectorAll('.nc-group')].map(group => {
    const box = group.querySelector('input[name="git-group"]')
    return {
      папка: (group.querySelector('.nc-group-main strong')?.textContent || '').trim(),
      пояснение: (group.querySelector('.nc-group-main small')?.textContent || '').trim(),
      счёт: (group.querySelector('.nc-group-main em')?.textContent || '').trim(),
      отметка: box ? (box.indeterminate ? 'частично' : box.checked ? 'все' : 'нет') : '—',
      активная: group.classList.contains('is-active'),
      // Уровень вложенности виден только отступом — значит отступ надо мерить.
      папки: [...group.querySelectorAll('.nc-dir')].map(dir => ({
        путь: (dir.querySelector('strong')?.textContent || '').trim().slice(0, 30),
        счёт: (dir.querySelector('small')?.textContent || '').trim(),
        отступ: Math.round(parseFloat(css(dir).paddingLeft)),
      })),
      файлы: [...group.querySelectorAll('.nc-file')].map(file => ({
        отступ: Math.round(parseFloat(css(file).paddingLeft)),
        путь: file.dataset.path,
        статус: [...file.classList].find(name => name.startsWith('is-') && name !== 'is-checked') || '',
        буква: (file.querySelector('.nc-file-main b')?.textContent || '').trim(),
        отмечен: Boolean(file.querySelector('input[name="git-file"]')?.checked),
        начертание: css(file.querySelector('.nc-file-main strong')).textDecorationLine,
        цвет: css(file.querySelector('.nc-file-main b')).color,
      })),
    }
  })
  // Обрезанная надпись — не мелочь: «Опубли…» не объясняет, что случится.
  const кнопки = [...document.querySelectorAll('.nc-commit-foot button')].map(button => ({
    текст: button.textContent.trim(),
    шрифт: css(button).fontSize,
    ширина: Math.round(button.clientWidth),
    нужно: Math.round(button.scrollWidth),
    обрезано: button.scrollWidth > button.clientWidth + 1,
  }))
  // Заголовок раздела рисует оболочка — он снаружи вебвью, но проверять его
  // надо: ради него из панели убран собственный заголовок.
  // Проверка на месте: применяется ли вообще атрибут style. Политика
  // безопасности вебвью запрещает инлайновые стили без 'unsafe-inline', и это
  // объясняет отступы, которых нет.
  const sample = document.createElement('div')
  sample.setAttribute('style', 'padding-left: 33px')
  document.body.appendChild(sample)
  const инлайн = { применён: css(sample).paddingLeft, атрибутРяда: document.querySelector('.nc-file')?.getAttribute('style') || '' }
  sample.remove()
  const rows = [...document.querySelectorAll('.nc-group > header, .nc-dir, .nc-file')]
  const плотность = {
    строк: rows.length,
    высотаЗаголовка: Math.round(document.querySelector('.nc-group > header')?.getBoundingClientRect().height || 0),
    высотаСтроки: Math.round(document.querySelector('.nc-file')?.getBoundingClientRect().height || 0),
    надДеревом: Math.round(document.querySelector('.point-git-tree')?.getBoundingClientRect().top || 0),
    подДеревом: Math.round((document.body.getBoundingClientRect().height || 0) - (document.querySelector('.point-git-tree')?.getBoundingClientRect().bottom || 0)),
  }
  const commit = document.querySelector('#git-commit-submit')
  const app = document.querySelector('.nc-app')
  const tree = document.querySelector('.point-git-tree')
  return {
    ширина: Math.round(document.body.getBoundingClientRect().width),
    ветка: (document.querySelector('.nc-branch')?.textContent || '').trim(),
    уведомление: (document.querySelector('.nc-notice p')?.textContent || '').trim(),
    ошибка: Boolean(document.querySelector('.nc-notice.is-error')),
    группы: groups,
    плотность,
    инлайн,
    кнопки,
    кнопка: (commit?.textContent || '').trim(),
    подсказка: (document.querySelector('#git-commit-hint')?.textContent || '').trim(),
    кнопкаДоступна: commit ? !commit.disabled : null,
    прокрутка: tree ? { видно: Math.round(tree.clientHeight), всего: Math.round(tree.scrollHeight) } : null,
    переполнение: app ? Math.round(app.scrollWidth) - Math.round(app.clientWidth) : null,
  }
})()`

// Клик делается самим документом: координаты вложенного кадра и координаты
// окна — разные системы, а нам важно поведение обработчиков, а не попадание
// мышью.
const clickPanel = async (selector, nth = 0) => {
  const done = await evaluateIn(panel, inPanel(`(() => {
    const element = [...document.querySelectorAll(${JSON.stringify(selector)})].at(${nth})
    if (!element) return false
    element.click()
    return true
  })()`))
  await wait(700)
  return Boolean(done)
}

// Расширение Git находит репозиторий не мгновенно: первый снимок бывает пустым,
// и панель честно показывает «изменений нет». Ждём, пока состояние доедет, —
// мерить нужно установившуюся панель, а не гонку.
for (let attempt = 0; attempt < 20; attempt += 1) {
  const rows = await evaluateIn(panel, inPanel(`document.querySelectorAll('.nc-file').length`))
  if (rows) break
  await wait(900)
}
// Заголовок раздела в оболочке: он снаружи вебвью.
const sidebarTitle = await evaluate(`(() => {
  const title = document.querySelector('.part.sidebar .composite.title')
  if (!title) return null
  return {
    заголовок: (title.querySelector('h2')?.textContent || title.innerText || '').trim().slice(0, 60),
    подпись: (title.querySelector('.description')?.textContent || '').trim(),
  }
})()`)
const rails = await evaluate(`(() => {
  const read = selector => [...document.querySelectorAll(selector)]
    .filter(element => element.getBoundingClientRect().width > 0)
    .map(element => \`\${(element.getAttribute('aria-label') || '').slice(0, 28)} [\${[...element.classList].filter(name => name.startsWith('codicon-') || name === 'uri-icon').join(' ') || 'без класса'}]\`)
  return {
    слева: read('.part.activitybar .action-item a.action-label'),
    справа: read('.part.auxiliarybar .composite-bar-container .action-item a.action-label'),
  }
})()`)
// Проверка канала: буквальная строка, отправленная в панель и вернувшаяся назад.
const echo = await evaluateIn(panel, inPanel(`'stash@{0} seconds src/app.js'`))
const report = { эхо: echo, рейки: rails, 'заголовок раздела': sidebarTitle, 'как открылась': await evaluateIn(panel, inPanel(readPanel)) }
const first = (report['как открылась']?.группы || []).flatMap(group => group.файлы)[0]
if (first) {
  await clickPanel(`.nc-file[data-path="${first.путь}"] input[name="git-file"]`)
  report['сняли отметку'] = await evaluateIn(panel, inPanel(readPanel))
}
if (await clickPanel('.nc-group.tone-untracked input[name="git-group"]')) {
  report['отметили новые файлы'] = await evaluateIn(panel, inPanel(readPanel))
}
// Новая папка и перенос в неё. Имя спрашивает окно ввода оболочки, поэтому
// текст набирается в окне, а не в вебвью.
const typeInQuickInput = async text => {
  await command('Input.insertText', { text })
  await wait(300)
  for (const type of ['rawKeyDown', 'keyUp']) {
    await command('Input.dispatchKeyEvent', { type, key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 })
  }
  await wait(1600)
}
const target = (report['как открылась']?.группы || [])
  .flatMap(group => group.файлы)
  .find(file => file.статус === 'is-modified')
if (target && await clickPanel(`.nc-file[data-path="${target.путь}"] .nc-more`)) {
  if (await clickPanel('.nc-menu-body button[data-git-action="moveToList"][data-list="new"]')) {
    await typeInQuickInput('Рефакторинг')
    report['после новой папки'] = await evaluateIn(panel, inPanel(readPanel))
    if (await clickPanel('.nc-group.tone-change > header .nc-more', -1)
      && await clickPanel('.nc-menu-body button[data-action="git-select-only"]')) {
      report['только одна папка'] = await evaluateIn(panel, inPanel(readPanel))
    }
  }
}

if (await clickPanel('.nc-file .nc-more')) {
  report['меню файла'] = await evaluateIn(panel, inPanel(`(() => {
    const menu = document.querySelector('.nc-menu-body')
    if (!menu) return null
    const box = menu.getBoundingClientRect()
    return {
      пункты: [...menu.querySelectorAll('button')].map(button => button.textContent.trim()),
      подписи: [...menu.querySelectorAll('small')].map(item => item.textContent.trim()),
      влезает: Math.round(box.right) <= Math.round(document.body.getBoundingClientRect().width),
    }
  })()`))
}

// Настоящий коммит — только по явной просьбе: зонд запускают и на живых
// репозиториях, а коммит там сделает не то, чего от него ждут.
if (process.env.POINT_GIT_COMMIT === 'all' || process.env.POINT_GIT_COMMIT === 'push') {
  // Отмечаем всё: переименование и новый файл в одном коммите — отдельный
  // случай, у Git для них два разных механизма.
  await evaluateIn(panel, inPanel(`(() => {
    for (const box of document.querySelectorAll('input[name="git-group"]')) {
      if (!box.checked) box.click()
    }
    return true
  })()`))
  await wait(700)
  report['отметили всё'] = await evaluateIn(panel, inPanel(readPanel))
}
if (process.env.POINT_GIT_COMMIT) {
  await evaluateIn(panel, inPanel(`(() => {
    const field = document.querySelector('#git-commit-message')
    if (!field) return false
    field.value = 'Проверка панели: коммит из одной папки'
    field.dispatchEvent(new field.ownerDocument.defaultView.Event('input', { bubbles: true }))
    return true
  })()`))
  await wait(400)
  await clickPanel(process.env.POINT_GIT_COMMIT === 'push' ? '#git-commit-push' : '#git-commit-submit')
  await wait(4000)
  report['после коммита'] = await evaluateIn(panel, inPanel(readPanel))
}

// Ctrl+Shift+G: привычное сочетание обязано открывать панель Point, а не
// штатный SCM, значка которого в рейке больше нет.
{
  await clickRail('Инвентарь')
  const modifiers = 10 // Ctrl + Shift, как в остальных зондах
  for (const type of ['rawKeyDown', 'keyUp']) {
    await command('Input.dispatchKeyEvent', { type, key: 'G', code: 'KeyG', windowsVirtualKeyCode: 71, nativeVirtualKeyCode: 71, modifiers })
  }
  await wait(1500)
  report['ctrl+shift+g открыл'] = await evaluate(`(() => {
    const title = document.querySelector('.part.sidebar .composite.title')
    return (title?.innerText || '').trim().slice(0, 40)
  })()`)
}

// Широкая панель. Различия показываются рядом со списком только когда для них
// есть место, поэтому раздвигаем сайдбар за саш — как это делает рука.
const widen = async width => {
  const spot = await evaluate(`(() => {
    const part = document.querySelector('.part.sidebar')
    if (!part) return null
    const box = part.getBoundingClientRect()
    const sashes = [...document.querySelectorAll('.monaco-sash.vertical')]
      .map(sash => { const rect = sash.getBoundingClientRect(); return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) } })
      .filter(item => item.x > 0)
    if (!sashes.length) return null
    const edge = Math.round(box.right)
    const nearest = sashes.sort((a, b) => Math.abs(a.x - edge) - Math.abs(b.x - edge))[0]
    return { x: nearest.x, y: nearest.y, edge, left: Math.round(box.left) }
  })()`)
  if (!spot) return false
  const target = spot.left + width
  await command('Input.dispatchMouseEvent', { type: 'mousePressed', x: spot.x, y: spot.y, button: 'left', clickCount: 1 })
  for (const step of [0.35, 0.7, 1]) {
    await command('Input.dispatchMouseEvent', { type: 'mouseMoved', x: Math.round(spot.x + (target - spot.x) * step), y: spot.y, button: 'left', buttons: 1 })
    await wait(120)
  }
  await command('Input.dispatchMouseEvent', { type: 'mouseReleased', x: target, y: spot.y, button: 'left', clickCount: 1 })
  await wait(900)
  return true
}
// Ширину панели можно не трогать: POINT_GIT_WIDE=0 оставляет боковую рейку
// как есть — там своя раскладка, и её тоже надо смотреть.
if (process.env.POINT_GIT_WIDE !== '0' && await widen(900)) {
  await clickPanel('.nc-file .nc-file-main')
  await wait(1400)
  report['широкая панель'] = await evaluateIn(panel, inPanel(`(() => {
    const diff = document.querySelector('.nc-diff')
    const rows = [...document.querySelectorAll('.nc-dir')]
    return {
      ширина: Math.round(document.body.getBoundingClientRect().width),
      различияВидны: Boolean(diff && css(diff).display !== 'none'),
      списокШирина: Math.round(document.querySelector('.nc-list')?.getBoundingClientRect().width || 0),
      строкСписка: document.querySelectorAll('.nc-file').length,
      перваяПапка: (rows[0]?.innerText || '').trim().slice(0, 60),
      папок: rows.length,
      путь: (document.querySelector('.nc-path')?.textContent || '').trim(),
      полкаИсторияВкладки: [...document.querySelectorAll('.nc-tab')].map(tab => tab.textContent.trim()),
      переносТулбара: Math.round(document.querySelector('.nc-head')?.getBoundingClientRect().height || 0),
    }
  })()`))
}

// Вкладки «История» и «Полка»: у каждой свой список слева и своя карточка справа.
for (const tab of ['history', 'stash']) {
  if (!await clickPanel(`.nc-tab[data-tab="${tab}"]`)) continue
  await wait(900)
  report[tab === 'history' ? 'вкладка История' : 'вкладка Полка'] = await evaluateIn(panel, inPanel(`(() => ({
    заголовок: (document.querySelector('.nc-listhead > span')?.textContent || '').trim(),
    строк: document.querySelectorAll('.nc-row').length,
    первая: (document.querySelector('.nc-row')?.innerText || '').trim().slice(0, 70),
    карточка: (document.querySelector('.nc-detail')?.innerText || document.querySelector('.nc-diff-body')?.innerText || '').trim().slice(0, 90),
    панельКоммита: Boolean(document.querySelector('.nc-commit')),
  }))()`))
}
await clickPanel('.nc-tab[data-tab="changes"]')
await wait(700)

// На какой вкладке оставить панель для снимка — иногда нужен не список
// изменений, а история или полка.
if (process.env.POINT_GIT_TAB) {
  await clickPanel(`.nc-tab[data-tab="${process.env.POINT_GIT_TAB}"]`)
  await wait(1200)
}

// Меню закрывается перед снимком: иначе оно накрывает половину панели, а
// снимок нужен для оценки самой панели.
await clickPanel('.point-git-tree')
console.log(JSON.stringify(report, null, 2))
socket.close()
