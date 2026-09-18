// Окно поиска Point на живом окне: ТЗ docs/search-window.md, проверенное вводом.
//
// Аудит оболочки (`audit-point-workbench.mjs`) меряет то, что видно в сценарии,
// а окно поиска не открывает ни один сценарий: до него нужно дойти двойным
// Shift. Поэтому все требования ТЗ — вкладки, состояния, геометрия, роли,
// отклик и сквозная цепочка «нашёл → открыл → увидел в недавних» — до сих пор
// проверялись руками, то есть на слово. Здесь они проверяются вводом.
//
// Зонд идёт ВМЕСТО аудита, поэтому сценарий берётся из POINT_SCENARIO — так же,
// как у остальных зондов.
//
//   powershell -File scripts\test-point-workbench-design.ps1 -Scenario file `
//     -Probe scripts\probe-point-search-window.mjs -Workspace .
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
const evaluateAsync = expr => cmd('Runtime.evaluate', { returnByValue: true, awaitPromise: true, expression: expr }).then(r => r.result?.value)
const wait = ms => new Promise(resolve => setTimeout(resolve, ms))
await cmd('Runtime.enable')
await applyScenario({ command: cmd, evaluate, scenario: process.env.POINT_SCENARIO || 'file' })

const MODIFIER_SHIFT = 8
const MODIFIER_CTRL = 2
const MODIFIER_ALT = 1

// Форма нажатия та же, что в point-scenario.mjs: `rawKeyDown` + `keyUp` с обоими
// кодами. Отдельный `keyUp` здесь важен как нигде: счёт двойного Shift ведётся
// именно по отпусканию.
const нажать = async (key, code, keyCode, modifiers = 0) => {
  await cmd('Input.dispatchKeyEvent', { type: 'rawKeyDown', key, code, windowsVirtualKeyCode: keyCode, nativeVirtualKeyCode: keyCode, modifiers })
  await cmd('Input.dispatchKeyEvent', { type: 'keyUp', key, code, windowsVirtualKeyCode: keyCode, nativeVirtualKeyCode: keyCode, modifiers: modifiers & ~MODIFIER_SHIFT })
}
// Чистое нажатие Shift: на keydown модификатор поднят (так его видит браузер),
// на keyup снят. Между ними ничего не нажимается — иначе счёт сбрасывается,
// ровно как на прописной букве.
const shift = () => нажать('Shift', 'ShiftLeft', 16, MODIFIER_SHIFT)
const двойнойShift = async () => { await shift(); await wait(120); await shift(); await wait(500) }

const состояние = `(() => {
  const win = document.querySelector('.point-search')
  if (!win) { return { открыто: false } }
  const list = win.querySelector('.point-search-list')
  const активная = win.querySelector('.point-search-row.is-active')
  return {
    открыто: true,
    окон: document.querySelectorAll('.point-search').length,
    вкладка: (win.querySelector('.point-search-tab.is-active span')?.textContent || '').trim(),
    запрос: win.querySelector('.point-search-input')?.value ?? null,
    фокусВвода: document.activeElement === win.querySelector('.point-search-input'),
    строк: list.querySelectorAll('.point-search-row').length,
    строки: [...list.querySelectorAll('.point-search-row')].slice(0, 6).map(row => (row.querySelector('.point-search-row-label')?.textContent || '').trim()),
    пусто: (list.querySelector('.point-search-empty')?.textContent || '').trim() || null,
    счётчик: (win.querySelector('.point-search-count')?.textContent || '').trim() || null,
    подвал: (win.querySelector('.point-search-path')?.textContent || '').trim() || null,
    выбрана: активная ? [...list.querySelectorAll('.point-search-row')].indexOf(активная) : null,
    подсветок: list.querySelectorAll('.point-search-row-label mark').length,
  }
})()`

const снять = () => evaluate(состояние)

// Ввод: значение ставится и посылается `input` — тем же событием, на котором
// висит пауза перед запросом. Отклик считается от него до первой перерисовки
// списка, то есть ровно так, как обещает ТЗ: «от ввода до появления строк».
const набрать = query => evaluateAsync(`(async () => {
  const input = document.querySelector('.point-search-input')
  const list = document.querySelector('.point-search-list')
  if (!input || !list) { return null }
  const started = performance.now()
  const drawn = new Promise(resolve => {
    const observer = new MutationObserver(() => { observer.disconnect(); resolve(performance.now() - started) })
    observer.observe(list, { childList: true })
    setTimeout(() => { observer.disconnect(); resolve(null) }, 10000)
  })
  input.value = ${JSON.stringify(query)}
  input.dispatchEvent(new Event('input', { bubbles: true }))
  const ms = await drawn
  return { мс: ms === null ? null : Math.round(ms) }
})()`)

const искать = async query => {
  const отклик = await набрать(query)
  // Счётчик и подвал ставятся тем же заходом, что и строки, но снимок сразу
  // после мутации ловит список до `markActive`.
  await wait(120)
  return { запрос: query, ...отклик, ...(await снять()) }
}

const закрыть = async () => {
  await нажать('Escape', 'Escape', 27)
  await wait(300)
}

const провалы = []
const требование = (условие, текст) => { if (!условие) { провалы.push(текст) } }

const отчёт = {}

// ── 05. Клавиатура ──────────────────────────────────────────────────────────
// Одиночный Shift не открывает ничего: без этого замера «двойной Shift
// работает» ничего не доказывает — окно открылось бы и на одиночном.
await shift()
await wait(700)
отчёт.одиночныйShift = await снять()
требование(отчёт.одиночныйShift.открыто === false, 'одиночный Shift открыл окно')

await двойнойShift()
отчёт.двойнойShift = await снять()
требование(отчёт.двойнойShift.открыто === true, 'двойной Shift не открыл окно')
требование(отчёт.двойнойShift.вкладка === 'Файлы', `двойной Shift открыл вкладку «${отчёт.двойнойShift.вкладка}», а не «Файлы»`)
требование(отчёт.двойнойShift.фокусВвода === true, 'фокус при открытии не в строке ввода')

// Каждое сочетание проверяется от закрытого окна: ТЗ обещает именно «открыть»,
// а не «переключить уже открытое». Открытое окно к тому же скрыло бы подмену —
// чужая команда на том же сочетании оставила бы вкладку прежней и сошла бы за
// «ничего не изменилось».
const открытьСочетанием = async (имя, нажатие) => {
  await закрыть()
  await нажатие()
  await wait(900)
  const снимок = await снять()
  требование(снимок.открыто === true, `${имя} не открыло окно поиска`)
  return снимок
}
отчёт.ctrlN = await открытьСочетанием('Ctrl+N', () => нажать('N', 'KeyN', 78, MODIFIER_CTRL))
требование(отчёт.ctrlN.вкладка === 'Файлы', `Ctrl+N открыл «${отчёт.ctrlN.вкладка}», а не «Файлы»`)
отчёт.ctrlShiftF = await открытьСочетанием('Ctrl+Shift+F', () => нажать('F', 'KeyF', 70, MODIFIER_CTRL | MODIFIER_SHIFT))
требование(отчёт.ctrlShiftF.вкладка === 'Текст', `Ctrl+Shift+F открыл «${отчёт.ctrlShiftF.вкладка}», а не «Текст»`)
отчёт.ctrlAltShiftN = await открытьСочетанием('Ctrl+Alt+Shift+N', () => нажать('N', 'KeyN', 78, MODIFIER_CTRL | MODIFIER_ALT | MODIFIER_SHIFT))
требование(отчёт.ctrlAltShiftN.вкладка === 'Символы', `Ctrl+Alt+Shift+N открыл «${отчёт.ctrlAltShiftN.вкладка}», а не «Символы»`)

// Повторный вызов при открытом окне не открывает второе: переводит открытое на
// нужную вкладку и возвращает фокус в строку ввода.
await нажать('F', 'KeyF', 70, MODIFIER_CTRL | MODIFIER_SHIFT)
await wait(500)
отчёт.повторныйВызов = await снять()
требование(отчёт.повторныйВызов.окон === 1, `после повторного вызова окон стало ${отчёт.повторныйВызов.окон}`)
требование(отчёт.повторныйВызов.вкладка === 'Текст', `повторный вызов привёл на «${отчёт.повторныйВызов.вкладка}»`)
требование(отчёт.повторныйВызов.фокусВвода === true, 'повторный вызов не вернул фокус в строку ввода')

// Tab обходит три вкладки по кругу, Shift+Tab — обратно. Порядок полосы —
// Файлы → Символы → Текст, поэтому от «Текста» первый Tab возвращает к началу.
const обход = []
for (let step = 0; step < 3; step++) {
  await нажать('Tab', 'Tab', 9)
  await wait(200)
  обход.push((await снять()).вкладка)
}
отчёт.обходTab = обход
требование(обход.join(' → ') === 'Файлы → Символы → Текст', `Tab обошёл вкладки как «${обход.join(' → ')}»`)
await нажать('Tab', 'Tab', 9, MODIFIER_SHIFT)
await wait(200)
отчёт.обходShiftTab = (await снять()).вкладка
требование(отчёт.обходShiftTab === 'Символы', `Shift+Tab дал «${отчёт.обходShiftTab}», а не «Символы»`)
// Возвращаемся на «Текст»: следующий раздел меряет его состояния.
await нажать('Tab', 'Tab', 9)
await wait(200)

// ── 06. Состояния ───────────────────────────────────────────────────────────
// Каждое состояние обязано отличаться от соседнего словами, а не пустотой.
отчёт.состояния = {}
отчёт.состояния.текстПустойЗапрос = await искать('')
требование(отчёт.состояния.текстПустойЗапрос.пусто === 'Введите запрос', `пустой запрос на «Тексте» дал «${отчёт.состояния.текстПустойЗапрос.пусто}»`)
отчёт.состояния.текстОдинЗнак = await искать('h')
требование(отчёт.состояния.текстОдинЗнак.пусто === 'Нужно хотя бы два знака', `один знак на «Тексте» дал «${отчёт.состояния.текстОдинЗнак.пусто}»`)
// Безнадёжный запрос собирается на месте: любая записанная в зонде строка
// лежит в проекте — в самом зонде — и текстовый поиск честно её находит.
const небывалый = `нетутакой-${Math.random().toString(36).slice(2)}-строки`
отчёт.состояния.текстНичего = await искать(небывалый)
требование(отчёт.состояния.текстНичего.пусто === 'Ничего не найдено', `безнадёжный запрос дал «${отчёт.состояния.текстНичего.пусто}»`)

// ── 09. Отклик, вкладка «Текст» ─────────────────────────────────────────────
отчёт.откликТекст = await искать('workspaceId')
требование(отчёт.откликТекст.строк > 0, 'текстовый поиск не нашёл workspaceId')
требование(отчёт.откликТекст.строк <= 50, `текстовый поиск отдал ${отчёт.откликТекст.строк} строк при потолке 50`)
требование((отчёт.откликТекст.счётчик || '').startsWith('совпадений:'), `счётчик текста: «${отчёт.откликТекст.счётчик}»`)
требование(отчёт.откликТекст.мс !== null && отчёт.откликТекст.мс <= 450, `отклик «Текста» ${отчёт.откликТекст.мс} мс при пороге 450`)

// Символы: языковых служб в сборке нет, и окно обязано сказать именно это, а не
// «ничего не найдено» — иначе оно врёт про содержимое проекта.
await нажать('N', 'KeyN', 78, MODIFIER_CTRL | MODIFIER_ALT | MODIFIER_SHIFT)
await wait(400)
отчёт.состояния.символыПустойЗапрос = await искать('')
требование(отчёт.состояния.символыПустойЗапрос.вкладка === 'Символы', 'Ctrl+Alt+Shift+N не привёл на «Символы»')
требование(отчёт.состояния.символыПустойЗапрос.пусто === 'Введите запрос', `пустой запрос на «Символах» дал «${отчёт.состояния.символыПустойЗапрос.пусто}»`)
отчёт.состояния.символы = await искать('main')
требование(
  отчёт.состояния.символы.пусто === 'Языковая служба не подключена — символы искать нечем' || отчёт.состояния.символы.строк > 0,
  `«Символы» ответили «${отчёт.состояния.символы.пусто}»`,
)

// ── 07/08/09. Вид, доступность и отклик «Файлов» ────────────────────────────
// Замер идёт на вкладке «Файлы» с выдачей: пустое окно не показывает ни строки
// результата, ни подвала с путём.
await нажать('N', 'KeyN', 78, MODIFIER_CTRL)
await wait(400)
отчёт.откликФайлы = await искать('main')
требование(отчёт.откликФайлы.вкладка === 'Файлы', `Ctrl+N привёл на «${отчёт.откликФайлы.вкладка}»`)
требование(отчёт.откликФайлы.строк > 0, 'поиск по файлам не нашёл main')
требование((отчёт.откликФайлы.счётчик || '').startsWith('файлов:'), `счётчик файлов: «${отчёт.откликФайлы.счётчик}»`)
требование(отчёт.откликФайлы.подсветок > 0, 'совпадение в имени файла не подсвечено')
требование(отчёт.откликФайлы.мс !== null && отчёт.откликФайлы.мс <= 250, `отклик «Файлов» ${отчёт.откликФайлы.мс} мс при пороге 250`)
требование(!!отчёт.откликФайлы.подвал, 'подвал не показывает путь выбранной строки')

отчёт.вид = await evaluate(`(() => {
  const win = document.querySelector('.point-search')
  const scrim = document.querySelector('.point-search-scrim')
  if (!win) { return null }
  const мера = (selector, свойство) => {
    const el = win.querySelector(selector)
    if (!el) { return null }
    const box = el.getBoundingClientRect()
    return свойство === 'высота' ? Math.round(box.height) : Math.round(box.width)
  }
  const box = win.getBoundingClientRect()
  const input = win.querySelector('.point-search-input')
  const стильВвода = getComputedStyle(input)
  const строка = win.querySelector('.point-search-row')
  const вкладка = win.querySelector('.point-search-tab')
  return {
    ширина: Math.round(box.width),
    ожидаемаяШирина: Math.min(760, Math.round(innerWidth - 48)),
    отступСверху: Math.round(box.top - scrim.getBoundingClientRect().top),
    // 14vh считается от высоты затемнения: оно растянуто на всё окно.
    ожидаемыйОтступ: Math.round(scrim.getBoundingClientRect().height * 0.14),
    полосаВкладок: мера('.point-search-tabs', 'высота'),
    строкаВвода: мера('.point-search-field', 'высота'),
    строкаРезультата: строка ? Math.round(строка.getBoundingClientRect().height) : null,
    высотаВкладки: вкладка ? Math.round(вкладка.getBoundingClientRect().height) : null,
    подвал: мера('.point-search-foot', 'высота'),
    скругление: getComputedStyle(win).borderTopLeftRadius,
    боковоеПоле: строка ? getComputedStyle(строка).paddingLeft : null,
    кегльВвода: стильВвода.fontSize,
    кольцоФокуса: стильВвода.outlineStyle,
    рамкаВвода: стильВвода.borderTopWidth,
    роли: {
      окно: win.getAttribute('role'),
      имя: win.getAttribute('aria-label'),
      список: win.querySelector('.point-search-list')?.getAttribute('role') ?? null,
      строка: строка?.getAttribute('role') ?? null,
      выбрана: строка?.getAttribute('aria-selected') ?? null,
      полоса: win.querySelector('.point-search-tabs')?.getAttribute('role') ?? null,
      вкладка: вкладка?.getAttribute('role') ?? null,
      вкладкаВыбрана: вкладка?.getAttribute('aria-selected') ?? null,
    },
  }
})()`)

const в = отчёт.вид || {}
требование(в.ширина === в.ожидаемаяШирина, `ширина окна ${в.ширина} при ожидаемых ${в.ожидаемаяШирина}`)
требование(Math.abs(в.отступСверху - в.ожидаемыйОтступ) <= 2, `отступ сверху ${в.отступСверху} при ожидаемых ${в.ожидаемыйОтступ} (14vh)`)
требование(в.полосаВкладок === 36, `полоса вкладок ${в.полосаВкладок} px при 36`)
требование(в.строкаВвода === 38, `строка ввода ${в.строкаВвода} px при 38`)
требование(в.строкаРезультата === 28, `строка результата ${в.строкаРезультата} px при 28`)
требование(в.высотаВкладки === 28, `высота вкладки ${в.высотаВкладки} px при 28`)
требование(в.подвал === 26, `подвал ${в.подвал} px при 26`)
требование(в.боковоеПоле === '12px', `боковое поле ${в.боковоеПоле} при 12px`)
требование(в.скругление === '8px', `скругление ${в.скругление} при 8px`)
требование(в.кегльВвода === '14px', `кегль строки ввода ${в.кегльВвода} при 14px`)
требование(в.кольцоФокуса === 'none' && в.рамкаВвода === '0px', `у строки ввода осталась коробка: контур ${в.кольцоФокуса}, рамка ${в.рамкаВвода}`)
требование(в.роли?.окно === 'dialog' && !!в.роли?.имя, 'окно не объявлено диалогом с именем')
требование(в.роли?.список === 'listbox', `список объявлен как «${в.роли?.список}»`)
требование(в.роли?.строка === 'option' && в.роли?.выбрана !== null, 'строка не объявлена вариантом с aria-selected')
требование(в.роли?.полоса === 'tablist' && в.роли?.вкладка === 'tab' && в.роли?.вкладкаВыбрана !== null, 'вкладки не объявлены как tablist/tab')

// ── 05. Ходьба по строкам ───────────────────────────────────────────────────
const ходьба = { начало: (await снять()).выбрана }
await нажать('ArrowDown', 'ArrowDown', 40)
await wait(150)
ходьба.послеСтрелки = (await снять()).выбрана
await нажать('PageDown', 'PageDown', 34)
await wait(150)
ходьба.послеPageDown = (await снять()).выбрана
await нажать('ArrowUp', 'ArrowUp', 38)
await wait(150)
ходьба.послеСтрелкиВверх = (await снять()).выбрана
отчёт.ходьба = ходьба
требование(ходьба.начало === 0, `выбор начинается со строки ${ходьба.начало}`)
требование(ходьба.послеСтрелки === 1 || отчёт.откликФайлы.строк === 1, `стрелка вниз дала строку ${ходьба.послеСтрелки}`)
требование(отчёт.откликФайлы.строк < 12 || ходьба.послеPageDown === 11, `PageDown дал строку ${ходьба.послеPageDown}, а шаг обещан в 10`)
требование(отчёт.откликФайлы.строк < 12 || ходьба.послеСтрелкиВверх === 10, `стрелка вверх дала строку ${ходьба.послеСтрелкиВверх}`)

// ── 11. Обязательная сквозная проверка ──────────────────────────────────────
// Не «окно открылось», а цепочка: нашёл → Enter → вкладка редактора с этим
// файлом → снова открыл → на пустом запросе увидел его в недавних. У стенда
// профиль чистый, и «недавние» доказываются только так.
await искать('main.go')
const цепочка = { нашли: (await снять()).строки[0] ?? null }
await нажать('Enter', 'Enter', 13)
await wait(1600)
цепочка.окноЗакрылось = !(await evaluate(`!!document.querySelector('.point-search')`))
цепочка.вкладкаРедактора = await evaluate(`(() => {
  const tab = document.querySelector('.part.editor .tabs-container .tab.active .label-name, .part.editor .tab.active .label-name')
  return tab ? tab.textContent.trim() : null
})()`)
требование(цепочка.окноЗакрылось === true, 'после Enter окно осталось открытым')
требование(цепочка.вкладкаРедактора === цепочка.нашли, `Enter открыл «${цепочка.вкладкаРедактора}», а найдено было «${цепочка.нашли}»`)

await двойнойShift()
await wait(400)
цепочка.недавние = await снять()
требование((цепочка.недавние.счётчик || '').startsWith('недавние:'), `на пустом запросе счётчик: «${цепочка.недавние.счётчик}»`)
требование(
  цепочка.недавние.строки.includes(цепочка.нашли),
  `открытого файла «${цепочка.нашли}» нет в недавних: ${JSON.stringify(цепочка.недавние.строки)}`,
)
отчёт.сквознаяПроверка = цепочка

// ── 05. Esc закрывает ───────────────────────────────────────────────────────
await закрыть()
отчёт.послеEsc = await снять()
требование(отчёт.послеEsc.открыто === false, 'Esc не закрыл окно')

отчёт.провалы = провалы
отчёт.итог = провалы.length ? `нарушений ТЗ: ${провалы.length}` : 'ТЗ выполняется на живом окне'
console.log(JSON.stringify(отчёт, null, 2))
ws.close()
process.exit(провалы.length ? 1 : 0)
