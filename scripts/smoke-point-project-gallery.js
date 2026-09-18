// Галерея миров — первый экран Чертога.
//
// Чертог открывается раньше любого проекта, и до сих пор в этом состоянии он
// показывал одну кнопку «Выбрать папку проекта». Теперь это домашний
// экран: список миров, поиск, закрепление, открытие в редакторе. Ядро ему не
// нужно вовсе — список приходит от хоста сообщением `projects`, — и проверка
// обязана держать именно это: экран рисуется при `service.state !== 'running'`
// и при недоверенной рабочей области.
//
// Ещё здесь проверяется то, что нельзя увидеть чтением: клик по миру уходит
// сообщением `openProject`, а не `chooseProject` (старый путь через QuickPick
// оболочки), и скелет переключения не подменяется экраном «Гильдия отдыхает».

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

const PROJECTS = [
  { path: 'C:\\worlds\\ai-ide', parent: 'C:\\worlds', name: 'ai-ide', pinned: true, lastOpenedAt: Date.now() - 60_000, lastTab: 'master', openCount: 12, branch: 'main', core: 'running', slow: false },
  { path: 'C:\\worlds\\frontend', parent: 'C:\\worlds', name: 'frontend', pinned: false, lastOpenedAt: Date.now() - 7_200_000, lastTab: '', openCount: 3, branch: 'feature/hall', core: 'warm', slow: false },
  { path: 'C:\\worlds\\go-health', parent: 'C:\\worlds', name: 'go-health', pinned: false, lastOpenedAt: 0, lastTab: '', openCount: 0, branch: '', core: 'idle', slow: true },
]

function surface({ workspace = '', workspacePath = '', service = { state: 'stopped' }, trusted = true, projects = PROJECTS, layout = 'wide', gallery = true } = {}) {
  const listeners = {}
  const posted = []
  const nodes = []
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: () => null,
    querySelectorAll: () => nodes,
    contains: () => false,
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout } }, activeElement: undefined },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {},
    // Отложенное здесь исполняется сразу — иначе половина экранов не дорисуется.
    // Но только по-настоящему отложенное на ноль: у скелета переключения есть
    // своё окно ожидания в 12 с, и мгновенный вызов снял бы скелет раньше, чем
    // проверка успела бы его увидеть.
    setTimeout(cb, delay) { if (!Number(delay)) cb(); return 0 },
    clearTimeout() {},
  }, { filename: 'main.js' })
  const send = data => listeners['window:message']({ data })
  send({ type: 'state', service, workspaceTrusted: trusted, workspace, workspacePath, selectedTab: 'master', boot: undefined })
  send({ type: 'projects', active: workspacePath, projects })
  // Галерея больше не стартовый экран: домом стал чат, а миры открывают
  // явным вызовом. Стенд повторяет этот вызов, иначе мерял бы чужой экран.
  if (gallery) send({ type: 'projectGallery', open: true })
  const click = dataset => listeners['root:click']({ target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) } })
  const text = () => root.innerHTML.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim()
  return { root, posted, send, click, text }
}

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

// Без мира — галерея, а не заглушка. Проверяется по содержимому, а не по
// классу: класс можно переименовать, а обещание «здесь видно все миры» — нет.
const empty = surface()
check('без мира рисуется галерея', /ГАЛЕРЕЯ МИРОВ/.test(empty.text()), empty.text().slice(0, 160))
check('видны все миры', ['ai-ide', 'frontend', 'go-health'].every(name => empty.text().includes(name)), empty.text().slice(0, 200))
check('видна ветка', empty.text().includes('feature/hall'), 'ветка не показана')
check('видно тёплое ядро', /ЯДРО Т/.test(empty.text()), 'состояние ядра не показано')
check('медленный диск назван', /ДИСК НЕ ОТВЕЧАЕТ/.test(empty.text()), 'мир на спящем диске не помечен')
check('счётчик склоняется', /3 мира/.test(empty.text()), empty.text().slice(0, 200))
check('нет старой заглушки', !/Agent Hub, индекс, терминалы/.test(empty.text()), 'вместо галереи показана прежняя заглушка')

// Ядру здесь делать нечего: экран обязан жить и при остановленном ядре, и в
// безопасном режиме — выбрать другой мир это ровно то действие, которым из
// недоверенной папки и уходят.
check('экран не требует ядра', !/Гильдия отдыхает|Пробудить ядро/.test(empty.text()), 'без мира показан экран службы')
const untrusted = surface({ trusted: false })
check('безопасный режим не прячет галерею', /ГАЛЕРЕЯ МИРОВ/.test(untrusted.text()), untrusted.text().slice(0, 160))

// Клик по миру — переключение на месте, а не выпадайка оболочки.
const picked = surface()
picked.click({ action: 'gallery-open', path: 'C:\\worlds\\frontend' })
const openMessage = picked.posted.find(message => message.type === 'openProject')
check('клик просит открыть мир', Boolean(openMessage), JSON.stringify(picked.posted.slice(-3)))
check('клик несёт путь мира', openMessage?.path === 'C:\\worlds\\frontend', String(openMessage?.path))
check('клик не зовёт прежний выбор', !picked.posted.some(message => message.type === 'chooseProject'),
  'клик ушёл в QuickPick оболочки — это перезагрузка окна')

// Закрепление и «забыть» ходят своими сообщениями и несут состояние, а не
// догадку: кнопка знает, закреплён ли мир сейчас.
const pinned = surface()
pinned.click({ action: 'gallery-pin', path: 'C:\\worlds\\ai-ide', pinned: '1' })
const pinMessage = pinned.posted.find(message => message.type === 'pinProject')
check('закрепление снимается', pinMessage?.pinned === false, JSON.stringify(pinMessage))
pinned.click({ action: 'gallery-forget', path: 'C:\\worlds\\go-health' })
check('мир можно забыть', pinned.posted.some(message => message.type === 'forgetProject'), 'нет сообщения forgetProject')
pinned.click({ action: 'gallery-ide', path: 'C:\\worlds\\ai-ide' })
check('мир открывается в редакторе', pinned.posted.some(message => message.type === 'openProjectInIde'), 'нет сообщения openProjectInIde')

// Галерея доступна и с открытым миром: это переключатель, а не только заглушка.
const inWorld = surface({ workspace: 'ai-ide', workspacePath: 'C:\\worlds\\ai-ide', service: { state: 'running' }, gallery: false })
check('с миром галерея закрыта', !/ГАЛЕРЕЯ МИРОВ/.test(inWorld.text()), 'галерея открылась поверх рабочего мира')
inWorld.send({ type: 'projectGallery', open: true })
check('галерея открывается командой', /ГАЛЕРЕЯ МИРОВ/.test(inWorld.text()), inWorld.text().slice(0, 160))
check('есть выход обратно в мир', /Вернуться в мир/.test(inWorld.text()), 'из галереи некуда вернуться')
inWorld.click({ action: 'gallery-close' })
check('выход работает', !/ГАЛЕРЕЯ МИРОВ/.test(inWorld.text()), 'кнопка возврата не закрыла галерею')

// Пока мир подключается, экран свой. «Пробудить ядро» здесь было бы ложью:
// ядро не отдыхает, оно поднимается.
const switching = surface({ workspace: 'ai-ide', workspacePath: 'C:\\worlds\\ai-ide', service: { state: 'stopped' }, gallery: false })
switching.send({ type: 'projectSwitch', phase: 'start', path: 'C:\\worlds\\frontend', name: 'frontend' })
check('скелет назван по миру', /ПОДКЛЮЧАЕМ МИР/.test(switching.text()) && switching.text().includes('frontend'), switching.text().slice(0, 160))
check('скелет не предлагает будить ядро', !/Пробудить ядро/.test(switching.text()), 'во время переключения показан экран службы')
switching.send({ type: 'projectSwitch', phase: 'done', path: 'C:\\worlds\\frontend', name: 'frontend' })
check('скелет уходит', !/ПОДКЛЮЧАЕМ МИР/.test(switching.text()), 'скелет остался после конца переключения')

// Пустой список — не ошибка и не пустой экран.
const nothing = surface({ projects: [] })
check('пустой список объяснён', /Миров пока нет/.test(nothing.text()), nothing.text().slice(0, 160))
check('пустой список предлагает действие', /Открыть папку/.test(nothing.text()), 'из пустой галереи некуда двинуться')

if (failures.length) {
  console.error('ГАЛЕРЕЯ МИРОВ ПРОВАЛЕНА:')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log(JSON.stringify({ gallery: 'ok', worlds: PROJECTS.length, checks: 20 }))
