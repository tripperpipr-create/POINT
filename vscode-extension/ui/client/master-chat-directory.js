// Левая панель Чертога: чаты всех проектов сразу.
//
// Источник сознательно двойной. Активный мир рисуется из `ui.masterData.sessions`
// — это уже загруженные живые данные: временные чаты, архив, закрепление, и все
// прежние действия `master-session-*` продолжают работать без единой правки.
// Прочие миры приходят каталогом от ядра (`GET /api/master/directory`), где нет
// ни архивных, ни временных. Грузить второй раз то, что уже в руках, незачем, а
// учить чужие миры операциям, которых в них всё равно не выполнить, — вредно.
//
// Путь чужого мира ядро не отдаёт (см. комментарий у App.MasterChatDirectory).
// Он берётся из хостового реестра по отпечатку, и открыть можно только тот мир,
// который реестр уже знает.
import { icon } from './ui-icons.js'

// Знаки панели — значки набора разговора, а не символы шрифта: «⌄» и «›»
// разной ширины сдвигали имя мира на четыре пикселя при сворачивании, а «＋» и
// «×» рисуются каждой гарнитурой по-своему (ui-icons.js).
export function createMasterChatDirectory(dependencies) {
  const { ui, vscode, esc, countOf, projectPathByHash } = dependencies

  let directory = { currentWorkspaceId: '', worlds: [] }
  let query = ''
  let openingChat = ''
  // Одно множество вместо двух: в нём миры, чьё состояние отличается от
  // умолчания. Умолчание разное — активный мир раскрыт, прочие свёрнуты, — и
  // хранить «свёрнутые» пришлось бы вместе со «развёрнутыми».
  const toggled = new Set()
  const isOpen = world => (world.own ? !toggled.has(world.workspaceId) : toggled.has(world.workspaceId))

  function receiveChatDirectory(message) {
    const value = message?.directory
    directory = {
      currentWorkspaceId: String(value?.currentWorkspaceId || ''),
      worlds: Array.isArray(value?.worlds) ? value.worlds : [],
    }
    ui.chatDirectoryStatus = message?.failed ? 'error' : 'ready'
  }

  // Каталог просится один раз: состояние уходит из 'idle' до ответа, иначе
  // ответ вызвал бы отрисовку, а отрисовка — новый запрос.
  function requestChatDirectory() {
    if (ui.chatDirectoryStatus !== 'idle') return
    if (ui.state.service?.state !== 'running') return
    ui.chatDirectoryStatus = 'loading'
    setTimeout(() => vscode.postMessage({ type: 'loadChatDirectory' }), 0)
  }

  function chatWhen(value) {
    const stamp = Date.parse(String(value || ''))
    if (!Number.isFinite(stamp)) return ''
    const minutes = Math.round((Date.now() - stamp) / 60000)
    if (minutes < 1) return 'сейчас'
    if (minutes < 60) return countOf(minutes, 'минута', 'минуты', 'минут')
    const hours = Math.round(minutes / 60)
    if (hours < 24) return countOf(hours, 'час', 'часа', 'часов')
    const days = Math.round(hours / 24)
    if (days === 1) return 'вчера'
    if (days < 30) return countOf(days, 'день', 'дня', 'дней')
    return 'давно'
  }

  function matches(title) {
    const needle = query.trim().toLowerCase()
    return !needle || String(title || '').toLowerCase().includes(needle)
  }

  // Активный мир — из живых данных мастера. Заголовок и время берутся оттуда же,
  // поэтому переименование видно сразу, без похода в ядро за каталогом.
  function currentWorldChats() {
    const sessions = ui.masterData?.sessions
    const items = Array.isArray(sessions?.items) ? sessions.items : []
    const active = Array.isArray(ui.masterData?.activeTurns) ? ui.masterData.activeTurns : []
    const running = new Set(active.map(turn => String(turn?.conversationId || '')))
    return items.map(item => ({
      id: item.id,
      title: item.title,
      updatedAt: item.updatedAt,
      pinned: Boolean(item.pinned),
      archived: Boolean(item.archived),
      temporary: Boolean(item.temporary),
      running: running.has(String(item.id)),
      current: item.id === sessions?.active,
    }))
  }

  function currentWorldName() {
    return String(ui.state.workspace || '')
  }

  function rowHtml(chat, world) {
    const own = Boolean(world.own)
    const classes = ['hall-chat-row']
    if (chat.current && own) classes.push('is-current')
    if (chat.running) classes.push('is-running')
    if (!own && openingChat === chat.id) classes.push('is-loading')
    const live = chat.running ? '<span class="hall-chat-live" aria-label="Идёт ответ"><i></i></span>' : ''
    const when = chatWhen(chat.updatedAt)
    return `<div class="${classes.join(' ')}">
      <button type="button" data-action="chat-open" data-world="${esc(world.workspaceId)}" data-path="${esc(world.path || '')}" data-chat="${esc(chat.id)}"${chat.current && own ? ' aria-current="true"' : ''} title="${esc(chat.title)}">
        <span class="hall-chat-title">${esc(chat.title)}</span>${live}<time class="hall-chat-when">${esc(when)}</time>
      </button>
      ${own ? `<button type="button" class="hall-chat-drop" data-keynav-skip data-action="master-session-delete" data-id="${esc(chat.id)}" aria-label="Удалить разговор «${esc(chat.title)}»" title="Удалить разговор">${icon('x')}</button>` : ''}
    </div>`
  }

  function groupHtml(world) {
    const open = isOpen(world)
    const visible = world.chats.filter(chat => !chat.archived && !chat.temporary && matches(chat.title))
    const archived = world.own ? world.chats.filter(chat => chat.archived) : []
    // Поиск раскрывает свёрнутую группу, в которой есть совпадение: прятать
    // найденное за закрытым заголовком — то же, что не найти.
    const expanded = open || (Boolean(query.trim()) && visible.length > 0)
    if (query.trim() && !visible.length) return ''
    const add = world.own
      ? '<button type="button" class="hall-chats-add" data-action="master-session-new" aria-label="Новый чат в этом проекте" title="Новый чат">' + icon('plus') + '</button>'
      : `<button type="button" class="hall-chats-add" data-action="chat-new" data-world="${esc(world.workspaceId)}" data-path="${esc(world.path || '')}" aria-label="Новый чат в проекте «${esc(world.name)}»" title="Новый чат в этом проекте">${icon('plus')}</button>`
    const body = expanded
      ? `<div class="hall-chats-items" data-keynav="column" aria-label="Чаты проекта «${esc(world.name)}»">
          ${visible.map(chat => rowHtml(chat, world)).join('') || '<p class="hall-chats-blank">Здесь пока пусто</p>'}
          ${archived.length ? `<details class="hall-chats-archive"><summary>${esc(countOf(archived.length, 'разговор в архиве', 'разговора в архиве', 'разговоров в архиве'))}</summary>${archived.map(chat => rowHtml(chat, world)).join('')}</details>` : ''}
        </div>`
      : ''
    const unreachable = !world.own && !world.path
    return `<section class="hall-chats-group${expanded ? ' is-open' : ''}${world.own ? ' is-own' : ''}">
      <h3 class="hall-chats-world">
        <button type="button" data-action="chat-group" data-world="${esc(world.workspaceId)}" aria-expanded="${expanded ? 'true' : 'false'}">
          <em class="hall-chats-caret" aria-hidden="true">${icon('chevron-right')}</em><span>${esc(world.name)}</span><b>${world.chats.filter(chat => !chat.archived && !chat.temporary).length}</b>
        </button>
        ${unreachable ? '' : add}
      </h3>
      ${unreachable && expanded ? '<p class="hall-chats-blank">Мира нет в списке Point — откройте папку заново.</p>' : body}
    </section>`
  }

  function worlds() {
    const out = []
    const currentName = currentWorldName()
    if (currentName) {
      out.push({
        workspaceId: directory.currentWorkspaceId || 'current',
        name: currentName,
        path: String(ui.state.workspacePath || ''),
        own: true,
        chats: currentWorldChats(),
      })
    }
    for (const world of directory.worlds) {
      if (world.current && currentName) continue
      out.push({
        workspaceId: String(world.workspaceId || ''),
        name: String(world.name || ''),
        // Путь чужого мира приходит не от ядра, а из реестра хоста по отпечатку.
        path: projectPathByHash(String(world.hash || '')),
        own: false,
        chats: (world.chats || []).map(chat => ({
          id: chat.id, title: chat.title, updatedAt: chat.updatedAt,
          pinned: Boolean(chat.pinned), archived: false, temporary: false,
          running: Boolean(chat.running), current: false,
        })),
      })
    }
    return out
  }

  function chatDirectoryHtml() {
    requestChatDirectory()
    const list = worlds()
    const groups = list.map(groupHtml).join('')
    // «Временный чат» — строка сразу под проектами, а не подвал панели: внизу
    // пустой рейки он висел отдельно от всего, что с ним связано (выбор
    // владельца по снимкам, сентябрь 2026).
    const temporary = `<button type="button" class="hall-chats-temporary" data-action="master-session-temporary"${ui.state.workspace ? '' : ' disabled'}>${icon('plus')}<span>Временный чат</span></button>`
    const empty = !list.length
      ? '<div class="hall-chats-empty"><strong>Миров пока нет</strong><p>Откройте папку — она станет миром для мастера и гильдии.</p><button type="button" class="hall-chats-new" data-action="gallery-open-folder">Открыть папку</button></div>'
      : ''
    return `<aside class="hall-chats" aria-label="Чаты по проектам">
      <header class="hall-chats-head">
        <button type="button" class="hall-chats-new" data-action="master-session-new"${ui.state.workspace ? '' : ' disabled'}>Новый чат</button>
        <button type="button" class="hall-chats-world-new" data-action="gallery-toggle" aria-label="Все проекты" title="Все проекты">${icon('folder')}</button>
      </header>
      <input type="search" id="chat-directory-search" data-master-sidebar-search placeholder="Поиск по всем чатам" aria-label="Поиск по чатам всех проектов" value="${esc(query)}">
      <div class="hall-chats-groups">${groups || empty}${groups && query.trim() && !list.some(world => world.chats.some(chat => matches(chat.title))) ? `<p class="hall-chats-blank">Ничего не нашлось по запросу «${esc(query.trim())}»</p>` : ''}${groups ? temporary : ''}</div>
    </aside>`
  }

  // Первый запуск и любой другой момент без мира: раскладка та же, что
  // всегда, чтобы человек сразу видел то, с чем будет жить. Поле ввода
  // показано и заперто: писать мастеру некуда, пока нет мира.
  function masterWithoutWorldHtml() {
    return `<main class="hall-chat-blank">
      <div class="hall-chat-blank-copy">
        <h2>Выберите папку проекта</h2>
        <p>Она станет миром: мастер получит контекст, гильдия — место для работы, а разговоры начнут храниться рядом с кодом.</p>
        <button type="button" class="hall-chats-new" data-action="gallery-open-folder">Открыть папку</button>
      </div>
      <form class="hall-compose is-sealed" aria-hidden="true">
        <textarea rows="3" disabled placeholder="Что хотите сделать в проекте?"></textarea>
      </form>
    </main>`
  }

  // Что рисуется до разбора вкладок. Возвращает undefined, когда экран
  // обычный и решать нечего.
  //
  // Разбор живёт здесь, а не в paint(): все три случая — про список чатов и
  // миры, а main.js давно упирается в бюджет строк.
  function chatScreenBeforeTabs({ gallery, wide }) {
    if (gallery) return dependencies.projectGallery()
    const onChat = wide && ui.state.selectedTab === 'master'
    // Компаньону и окнам инструментов чат без мира не нужен: они живут в окне
    // IDE, где проект уже есть, и им довольно короткого «выберите проект».
    if (!ui.state.workspace && !onChat) return dependencies.projectRequired()
    // Первый запуск: та же раскладка, что и всегда.
    if (!ui.state.workspace) return dependencies.shell(masterWithoutWorldHtml())
    const switching = dependencies.projectSwitchInfo()
    if (!switching) return undefined
    // Переключение мира не убирает список чатов: он не про тот мир, который
    // меняется. Полноэкранный скелет остаётся для перехода из галереи.
    const skeleton = dependencies.projectSwitchSkeleton(switching)
    return onChat ? dependencies.shell(skeleton) : skeleton
  }

  function handleChatDirectoryAction(action, target) {
    if (action === 'chat-group') {
      const id = target?.dataset?.world || ''
      if (toggled.has(id)) toggled.delete(id)
      else toggled.add(id)
      return true
    }
    if (action === 'chat-open') {
      const world = target?.dataset?.world || ''
      const chat = target?.dataset?.chat || ''
      const own = world === (directory.currentWorkspaceId || 'current') || world === 'current'
      if (own) {
        vscode.postMessage({ type: 'masterSession', action: 'select', id: chat })
        return true
      }
      const path = target?.dataset?.path || ''
      if (!path) return true
      openingChat = chat
      vscode.postMessage({ type: 'openProjectChat', path, conversationId: chat })
      return true
    }
    if (action === 'chat-new') {
      const path = target?.dataset?.path || ''
      if (!path) return true
      vscode.postMessage({ type: 'openProjectChat', path, newChat: true })
      return true
    }
    return false
  }

  function handleChatDirectoryInput(target) {
    if (!target?.matches?.('[data-master-sidebar-search]')) return false
    query = String(target.value || '')
    return true
  }

  return {
    chatDirectoryHtml,
    chatScreenBeforeTabs,
    receiveChatDirectory,
    handleChatDirectoryAction,
    handleChatDirectoryInput,
  }
}
