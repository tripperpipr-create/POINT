// Левая панель Чертога: чаты всех проектов.
//
// Чат стал домом, и панель рядом с ним — единственная навигация. Она обязана
// держать три вещи, которые чтением кода не проверяются: активный мир раскрыт,
// а чужие свёрнуты; поиск раскрывает свёрнутую группу с совпадением; клик по
// чужому чату уходит переключением мира, а не выбором разговора в своём.
//
// Отдельно проверяется, что рейки разделов на этом экране нет, а срочное из неё
// не пропало: очередь решений и непроверенные изменения остались значками.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

const DIRECTORY = {
  currentWorkspaceId: 'ws-current',
  worlds: [
    { workspaceId: 'ws-current', name: 'ai-ide', hash: 'aaaaaaaaaaaaaaaaaaaaaaaa', path: 'C:\\worlds\\ai-ide', current: true, chats: [] },
    {
      workspaceId: 'ws-billing', name: 'billing-api', hash: 'bbbbbbbbbbbbbbbbbbbbbbbb', current: false,
      chats: [
        { id: 'chat-webhook', title: 'Вебхук повторов', updatedAt: '2026-09-14T12:00:00Z', running: true },
        { id: 'chat-pricing', title: 'Тарифы и лимиты', updatedAt: '2026-09-13T12:00:00Z' },
      ],
    },
    {
      workspaceId: 'ws-front', name: 'frontend', hash: 'cccccccccccccccccccccccc', current: false,
      chats: [{ id: 'chat-theme', title: 'Тёмная тема', updatedAt: '2026-09-12T12:00:00Z' }],
    },
  ],
}

const SESSIONS = {
  active: 'chat-migrations',
  items: [
    { id: 'chat-migrations', title: 'Схема миграций', updatedAt: '2026-09-14T13:00:00Z' },
    { id: 'chat-refactor', title: 'Рефакторинг хаба', updatedAt: '2026-09-14T11:00:00Z' },
    { id: 'chat-old', title: 'Старое обсуждение', updatedAt: '2026-09-01T11:00:00Z', archived: true },
  ],
}

const PROJECTS = [
  { path: 'C:\\worlds\\ai-ide', parent: 'C:\\worlds', name: 'ai-ide', hash: 'aaaaaaaaaaaaaaaaaaaaaaaa', pinned: false, lastOpenedAt: Date.now(), branch: '', core: 'running', slow: false },
  { path: 'C:\\worlds\\billing-api', parent: 'C:\\worlds', name: 'billing-api', hash: 'bbbbbbbbbbbbbbbbbbbbbbbb', pinned: false, lastOpenedAt: Date.now(), branch: '', core: 'warm', slow: false },
  { path: 'C:\\worlds\\frontend', parent: 'C:\\worlds', name: 'frontend', hash: 'cccccccccccccccccccccccc', pinned: false, lastOpenedAt: 0, branch: '', core: 'idle', slow: false },
]

function surface({ changeSets = [] } = {}) {
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: () => null,
    querySelectorAll: () => [],
    contains: () => false,
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } }, activeElement: undefined },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {},
    setTimeout(cb, delay) { if (!Number(delay)) cb(); return 0 },
    clearTimeout() {},
  }, { filename: 'main.js' })
  const send = data => listeners['window:message']({ data })
  send({
    type: 'state', service: { state: 'running' }, workspaceTrusted: true,
    workspace: 'ai-ide', workspacePath: 'C:\\worlds\\ai-ide', selectedTab: 'master',
    boot: { changeSets, runs: [], profiles: [], usageRecords: [] },
  })
  send({ type: 'projects', active: 'C:\\worlds\\ai-ide', projects: PROJECTS })
  send({ type: 'master', master: { sessions: SESSIONS, history: [], activeTurns: [{ conversationId: 'chat-refactor', id: 'turn-1', status: 'streaming' }], configured: true }, loaded: true })
  send({ type: 'chatDirectory', directory: DIRECTORY })
  const click = dataset => listeners['root:click']({ target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) } })
  const input = value => listeners['root:input']({
    target: { value, dataset: {}, matches: selector => selector === '[data-master-sidebar-search]', closest: () => null },
  })
  // Заголовок активного разговора стоит и в шапке чата, поэтому проверки списка
  // смотрят только внутрь панели: иначе «поиск спрятал строку» мерило бы шапку.
  const aside = () => {
    const html = root.innerHTML
    const start = html.indexOf('<aside class="hall-chats"')
    if (start < 0) return ''
    const end = html.indexOf('</aside>', start)
    return html.slice(start, end < 0 ? undefined : end)
  }
  const strip = value => value.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim()
  return { root, posted, send, click, input, aside, asideText: () => strip(aside()), text: () => strip(root.innerHTML) }
}

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

const view = surface({ changeSets: [{ id: 'set-1', status: 'pending' }, { id: 'set-2', status: 'pending' }] })
const html = () => view.root.innerHTML
const aside = view.aside
const asideText = view.asideText

// Экран чата: рейки разделов нет, список чатов есть, вход в настройки один.
check('рейки разделов нет', !html().includes('hall-nav'), 'на экране чата осталась рейка из шести разделов')
check('список чатов на месте', html().includes('hall-chats'), 'левой панели чатов нет')
check('вход в настройки один', (html().match(/data-tab="overview"/g) || []).length === 1,
  `входов в настройки ${(html().match(/data-tab="overview"/g) || []).length}, а должен быть один`)
check('имя мира в шапке', /hall-chat-heading[\s\S]{0,200}ai-ide/.test(html()), 'шапка не называет мир — в кросс-проектном списке это обязательно')

// Группы: свой мир раскрыт, чужие свёрнуты и названы числом.
check('свой мир раскрыт', /aria-expanded="true"[\s\S]*?ai-ide/.test(aside()), 'активный проект свёрнут')
check('чужие миры видны', asideText().includes('billing-api') && asideText().includes('frontend'), asideText().slice(0, 200))
check('чужие миры свёрнуты', (aside().match(/aria-expanded="false"/g) || []).length === 2,
  `свёрнутых групп ${(aside().match(/aria-expanded="false"/g) || []).length}, ожидалось две`)
check('чужие чаты спрятаны', !asideText().includes('Вебхук повторов'), 'чат свёрнутой группы показан')
check('свои чаты видны', asideText().includes('Схема миграций') && asideText().includes('Рефакторинг хаба'), asideText().slice(0, 200))
check('архив отделён', asideText().includes('разговор в архиве'), 'архив активного мира не назван')

// «Идёт ответ» — по живому ходу своего мира и по признаку из ядра у чужого.
check('идущий ответ отмечен', aside().includes('hall-chat-live'), 'ни один чат не помечен идущим')

// Срочное не пропало вместе с рейкой.
check('изменения без проверки названы', /2 набора без проверки/.test(view.text()), view.text().slice(0, 300))

// Раскрытие чужой группы показывает её чаты.
view.click({ action: 'chat-group', world: 'ws-billing' })
check('группа раскрывается', asideText().includes('Вебхук повторов'), 'клик по заголовку не раскрыл группу')
view.click({ action: 'chat-group', world: 'ws-billing' })
check('группа сворачивается', !asideText().includes('Вебхук повторов'), 'повторный клик не свернул группу')

// Поиск идёт по всем мирам и раскрывает свёрнутую группу с совпадением.
view.input('Вебхук')
check('поиск раскрывает чужую группу', asideText().includes('Вебхук повторов'), asideText().slice(0, 250))
check('поиск прячет непохожее', !asideText().includes('Схема миграций'), 'в результатах поиска остались чужие строки')
view.input('')
check('сброс поиска возвращает свои', asideText().includes('Схема миграций'), 'после сброса поиска свой мир не вернулся')

// Клик по своему чату — выбор разговора; по чужому — переключение мира.
view.click({ action: 'chat-open', world: 'ws-current', path: 'C:\\worlds\\ai-ide', chat: 'chat-refactor' })
const own = view.posted.at(-1)
check('свой чат выбирается на месте', own?.type === 'masterSession' && own.action === 'select' && own.id === 'chat-refactor', JSON.stringify(own))

view.click({ action: 'chat-group', world: 'ws-billing' })
view.click({ action: 'chat-open', world: 'ws-billing', path: 'C:\\worlds\\billing-api', chat: 'chat-webhook' })
const foreign = view.posted.at(-1)
check('чужой чат уходит переключением', foreign?.type === 'openProjectChat', JSON.stringify(foreign))
check('переключение несёт путь и разговор', foreign?.path === 'C:\\worlds\\billing-api' && foreign?.conversationId === 'chat-webhook', JSON.stringify(foreign))
check('чужой чат не зовёт masterSession', view.posted.filter(message => message.type === 'masterSession').length === 1,
  'клик по чужому чату попытался выбрать разговор в текущем мире')

// Новый чат в чужом мире — то же переключение, но без разговора.
view.click({ action: 'chat-new', world: 'ws-front', path: 'C:\\worlds\\frontend' })
const created = view.posted.at(-1)
check('новый чат в чужом мире', created?.type === 'openProjectChat' && created.newChat === true && created.path === 'C:\\worlds\\frontend', JSON.stringify(created))

if (failures.length) {
  console.error('КАТАЛОГ ЧАТОВ ПРОВАЛЕН:')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log(JSON.stringify({ chatDirectory: 'ok', worlds: DIRECTORY.worlds.length, checks: 20 }))
