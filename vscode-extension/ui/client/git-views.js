// The Git tool window owns presentation and selection semantics; host actions
// remain in main.js and communicate through the existing webview protocol.
export function createGitViews({
  ui,
  getData,
  gitWide,
  shell,
  esc,
  countOf,
  toolCommandButton,
  toolWindowHeading,
  persistDraft,
  root,
}) {
  const GIT_STATUS_LABELS = {
    0: 'в индексе · изменён', 1: 'в индексе · добавлен', 2: 'в индексе · удалён',
    3: 'в индексе · переименован', 4: 'в индексе · скопирован', 5: 'изменён',
    6: 'удалён', 7: 'новый', 8: 'игнорируется', 9: 'будет добавлен',
    10: 'будет переименован', 11: 'сменился тип', 12: 'добавлен нами',
    13: 'добавлен ими', 14: 'удалён нами', 15: 'удалён ими', 16: 'добавлен с обеих сторон',
    17: 'удалён с обеих сторон', 18: 'изменён с обеих сторон',
  }

  const GIT_ICONS = {
    branch: 'M6 4.5a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Zm7 0a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0ZM4.5 6v4m0 1.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3Zm7-5.5v1a3 3 0 0 1-3 3H7a3 3 0 0 0-2.5 1.4',
    changes: 'M4.5 6v6.5M4.5 4.5a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Zm0 10a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Zm7 0a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Zm0-3V6a2.5 2.5 0 0 0-2.5-2.5H6.5m1.5-2-2 2 2 2',
    history: 'M8 4.5V8l2.5 1.5M2.5 8a5.5 5.5 0 1 0 1.6-3.9M4 2.5v2.2h2.2',
    stash: 'M2 5.5h12M2 5.5 3 3h10l1 2.5M3 5.5v7.5a.5.5 0 0 0 .5.5h9a.5.5 0 0 0 .5-.5V5.5M6.5 8.5h3',
    tree: 'M3 3.5h4M3 8h4M3 12.5h4M9 3.5h4M9 8h4M9 12.5h4',
    list: 'M2.5 4h11M2.5 8h11M2.5 12h11',
    refresh: 'M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2.5V6H10',
    caretDown: 'm4.5 6.5 3.5 3.5 3.5-3.5',
    caretRight: 'm6.5 4.5 3.5 3.5-3.5 3.5',
    folder: 'M2.5 12.5v-9h4l1.5 2h5.5v7a.5.5 0 0 1-.5.5H3a.5.5 0 0 1-.5-.5Z',
    warning: 'M8 6v3.5M8 11.6v.1M7.1 2.6 1.7 12a1 1 0 0 0 .9 1.5h10.8a1 1 0 0 0 .9-1.5L8.9 2.6a1 1 0 0 0-1.8 0Z',
    check: 'm3.5 8.5 3 3 6-6.5',
    minus: 'M4 8h8',
    cloud: 'M8 12V6.5m0 0L6 8.5m2-2 2 2M4.6 11.5A3.1 3.1 0 0 1 5 5.4a4 4 0 0 1 7.6 1.3 2.9 2.9 0 0 1-.6 5.7',
    undo: 'M2.5 5.5h6.8a3.7 3.7 0 0 1 0 7.4H5M2.5 5.5 5.5 2.5M2.5 5.5l3 3',
    plus: 'M8 3.5v9M3.5 8h9',
    dots: 'M4 8h.01M8 8h.01M12 8h.01',
    up: 'M8 13V3.5M8 3.5 4 7.5M8 3.5l4 4',
    down: 'M8 3v9.5M8 12.5l4-4M8 12.5l-4-4',
    fetch: 'M4.5 2.5v7M4.5 9.5l-2-2M4.5 9.5l2-2M11.5 13.5v-7M11.5 6.5l-2 2M11.5 6.5l2 2',
    file: 'M9 2.5H4.5a.5.5 0 0 0-.5.5v10a.5.5 0 0 0 .5.5h7a.5.5 0 0 0 .5-.5V6M9 2.5 12 6M9 2.5V6h3',
  }

  function gitIcon(name, size = 13) {
    const path = GIT_ICONS[name]
    if (!path) return ''
    return `<svg class="nc-icon" width="${size}" height="${size}" viewBox="0 0 16 16" aria-hidden="true"><path d="${path}"/></svg>`
  }

  const GIT_ADDED = [1, 7, 9, 12, 13, 16]
  const GIT_DELETED = [2, 6, 14, 15, 17]
  const GIT_RENAMED = [3, 10]
  const GIT_KINDS = {
    conflict: { mark: 'C', title: 'конфликт' },
    untracked: { mark: '?', title: 'нет в репозитории' },
    added: { mark: 'A', title: 'добавлен' },
    deleted: { mark: 'D', title: 'удалён' },
    renamed: { mark: 'R', title: 'переименован' },
    modified: { mark: 'M', title: 'изменён' },
  }

  // Вкладки окна — то, из чего выбирают: незакоммиченное и отложенное.
  // Журнала здесь нет: он читается целиком, с графом и поиском, и живёт
  // вкладкой редактора (Летопись, Alt+9). Две ленты коммитов в двух местах
  // расходились бы одна с другой на первой же правке.
  const GIT_TABS = [
    { id: 'changes', label: 'Изменения', title: 'Локальные изменения', icon: 'changes' },
    { id: 'stash', label: 'Полка', title: 'Отложенное', icon: 'stash' },
  ]

  // Имя вкладки могло сохраниться от прежней раскладки. Неизвестное — это
  // «Изменения»: иначе окно осталось бы и без списка, и без формы коммита.
  function gitActiveTab() {
    return GIT_TABS.some(item => item.id === ui.tab) ? ui.tab : GIT_TABS[0].id
  }

  function gitFileKind(item) {
    if (item?.area === 'conflict') return 'conflict'
    if (item?.area === 'untracked') return 'untracked'
    const status = Number(item?.status)
    if (GIT_DELETED.includes(status)) return 'deleted'
    if (GIT_RENAMED.includes(status)) return 'renamed'
    if (GIT_ADDED.includes(status)) return 'added'
    return 'modified'
  }

  function gitChanges() {
    const items = Array.isArray(getData()?.changes) ? getData().changes : []
    return items.filter(item => item && item.path)
  }

  function gitLists() {
    const lists = Array.isArray(getData()?.changeLists) ? getData().changeLists : []
    return lists.filter(item => item && item.id)
  }

  function gitCheckedPaths() {
    return gitChanges().map(item => String(item.path)).filter(path => ui.checked.has(path))
  }

  function gitGroupOf(item) {
    if (item?.area === 'conflict') return 'conflict'
    if (item?.area === 'untracked') return 'untracked'
    return String(item?.list || 'default')
  }

  function gitGroupPaths(id) {
    return gitChanges().filter(item => gitGroupOf(item) === id).map(item => String(item.path))
  }

  function syncGitChecked(changes) {
    const alive = new Set(changes.map(item => String(item.path || '')))
    for (const path of [...ui.checked]) if (!alive.has(path)) ui.checked.delete(path)
    for (const path of [...ui.known]) if (!alive.has(path)) ui.known.delete(path)
    for (const item of changes) {
      const path = String(item.path || '')
      if (!path || ui.known.has(path)) continue
      ui.known.add(path)
      if (item.area !== 'untracked') ui.checked.add(path)
    }
    if (!ui.foldedOnce && changes.length) {
      ui.foldedOnce = true
      if (changes.filter(item => item.area === 'untracked').length > 20) ui.collapsed.add('untracked')
    }
    if (ui.selected && !alive.has(ui.selected)) ui.selected = ''
    persistDraft()
  }

  function gitActionButton(action, label, tone = 'secondary', options = {}) {
    const data = [
      `data-action="git-action"`,
      `data-git-action="${esc(action)}"`,
      options.path ? `data-path="${esc(options.path)}"` : '',
      options.list ? `data-list="${esc(options.list)}"` : '',
      options.stash ? `data-stash="${esc(options.stash)}"` : '',
      options.paths ? `data-paths="${esc(options.paths.join('\n'))}"` : '',
      options.title ? `title="${esc(options.title)}"` : '',
    ].filter(Boolean).join(' ')
    const kbd = options.shortcut ? `<kbd>${esc(options.shortcut)}</kbd>` : ''
    return `<button type="${options.submit ? 'submit' : 'button'}" class="${tone}" ${data}${options.disabled ? ' disabled' : ''}><span>${esc(label)}</span>${kbd}</button>`
  }

  function gitFileBadge(name, kind) {
    if (kind === 'untracked') {
      return `<span class="nc-file-badge is-untracked" aria-hidden="true">?</span>`
    }
    const ext = name.includes('.') ? name.split('.').pop().toLowerCase() : ''
    const KNOWN = {
      php: 'php', js: 'js', jsx: 'jsx', ts: 'ts', tsx: 'tsx',
      go: 'go', py: 'py', rs: 'rs', java: 'java', kt: 'kt',
      html: 'html', htm: 'htm', vue: 'vue', xml: 'xml',
      css: 'css', scss: 'scss', sass: 'sass', less: 'less',
      json: 'json', yaml: 'yml', yml: 'yml', toml: 'toml',
      md: 'md', txt: 'txt', sql: 'sql', sh: 'sh', ps1: 'ps1',
      // Подпись фишки не длиннее четырёх знаков: колонка одной ширины, и
      // «media» в неё не влезало — от него оставалось «medi».
      mp4: 'mp4', mp3: 'mp3', png: 'img', jpg: 'img', jpeg: 'img', svg: 'svg',
    }
    const badge = KNOWN[ext] || (ext ? ext.slice(0, 3) : '')
    if (badge) {
      return `<span class="nc-file-badge ext-${esc(ext || 'file')}" aria-hidden="true">${esc(badge)}</span>`
    }
    return `<span class="nc-file-badge ext-file" aria-hidden="true">${gitIcon('file', 11)}</span>`
  }

  function gitFileMenuHtml(item) {
    const path = String(item.path || '')
    const untracked = item.area === 'untracked'
    const otherLists = gitLists().filter(list => list.id !== item.list)
    const targets = untracked
      ? '<small>Файлы вне репозитория живут в своей папке</small>'
      : `<small>Перенести в список изменений</small>${otherLists
        .map(list => gitActionButton('moveToList', list.name, 'quiet', { path, list: list.id })).join('')
      }${gitActionButton('moveToList', '＋ Новый список...', 'quiet', { path, list: 'new' })}`

    return `<div class="nc-menu-body">
      ${gitActionButton('openChange', 'Сравнить в редакторе', 'quiet', { path, shortcut: 'Ctrl+D' })}
      ${gitActionButton('openFile', 'Перейти к файлу', 'quiet', { path, shortcut: 'F4' })}
      ${gitActionButton('discard', untracked ? 'Удалить файл...' : 'Откатить изменения...', 'quiet danger', {
        path,
        shortcut: 'Ctrl+Alt+Z',
        title: untracked ? 'Переместить новый файл в корзину' : 'Вернуть версию из последнего коммита',
      })}
      ${targets}
    </div>`
  }

  function gitFileRow(item) {
    const path = String(item.path || '')
    const kind = gitFileKind(item)
    const cut = path.lastIndexOf('/')
    const name = cut < 0 ? path : path.slice(cut + 1)
    const dir = cut < 0 ? '' : path.slice(0, cut)
    const checked = ui.checked.has(path)
    const mark = GIT_KINDS[kind] || GIT_KINDS.modified
    const selected = ui.selected === path
    const hint = `${path} · ${GIT_STATUS_LABELS[item.status] || mark.title}${item.originalPath ? ` (было ${item.originalPath})` : ''}`
    const add = Number(item.add) || 0
    const del = Number(item.del) || 0
    const stats = item.area === 'untracked' || (!add && !del)
      ? ''
      : `<span class="nc-plus">${add ? `+${add}` : ''}</span><span class="nc-minus">${del ? `−${del}` : ''}</span>`
    // Прежний путь — подсказка, а не второе имя строки, и показывать в ней
    // надо ровно то, что изменилось. Сменилось имя — прежнее имя; имя то же,
    // а папка другая — прежняя папка. Git присылает `originalPath` и там, где
    // путь не менялся вовсе (смена регистра, смена прав), и тогда строка
    // получала подпись «.gitignore ← .gitignore», не сообщавшую ничего.
    const oldPath = String(item.originalPath || '')
    const oldName = oldPath.split('/').pop()
    const oldDir = oldName === oldPath ? '' : oldPath.slice(0, -oldName.length - 1)
    const wasName = !oldPath || oldPath === path ? '' : oldName === name ? (oldDir || 'корня') : oldName

    return `<div class="nc-file is-${esc(kind)}${checked ? ' is-checked' : ''}${selected ? ' is-selected' : ''}" data-path="${esc(path)}"${item.area === 'untracked' ? '' : ' draggable="true"'}>
      <input type="checkbox" name="git-file" value="${esc(path)}"${checked ? ' checked' : ''} aria-label="${esc(`${name} — включить в коммит`)}">
      <button type="button" class="nc-file-main" data-action="git-select" data-path="${esc(path)}" title="${esc(hint)}">
        ${gitFileBadge(name, kind)}
        <strong>${esc(name)}</strong>
        ${ui.flat && dir ? `<small>${esc(dir)}</small>` : ''}
        ${wasName ? `<small class="nc-renamed-from">← ${esc(wasName)}</small>` : ''}
        <i class="nc-gap"></i>
        ${item.area === 'conflict' ? `<em class="nc-warn-mark" title="Конфликт">${gitIcon('warning', 12)}</em>` : ''}
        ${stats}
      </button>
      <div class="nc-menu">
        <button type="button" class="nc-icon-btn nc-more" data-action="git-menu" data-menu="file:${esc(path)}" title="Действия с файлом" aria-label="${esc(`Действия с файлом ${name}`)}">${gitIcon('dots', 13)}</button>
        ${ui.menuFor === `file:${path}` ? gitFileMenuHtml(item) : ''}
      </div>
    </div>`
  }

  function gitDirOf(item) {
    const path = String(item?.path || '')
    const cut = path.lastIndexOf('/')
    return cut < 0 ? '' : path.slice(0, cut)
  }

  function gitBuildTree(files) {
    const root = { dirs: new Map(), files: [] }
    for (const item of files) {
      const parts = gitDirOf(item).split('/').filter(Boolean)
      let node = root
      for (const part of parts) {
        if (!node.dirs.has(part)) node.dirs.set(part, { dirs: new Map(), files: [] })
        node = node.dirs.get(part)
      }
      node.files.push(item)
    }
    return root
  }

  function gitCollectPaths(node) {
    const result = node.files.map(item => String(item.path))
    for (const child of node.dirs.values()) {
      result.push(...gitCollectPaths(child))
    }
    return result
  }

  function gitCountFiles(node) {
    let total = node.files.length
    for (const child of node.dirs.values()) total += gitCountFiles(child)
    return total
  }

  // Имя папки — последний сегмент; проходные уровни (у которых один
  // подкаталог и ни одного своего файла) остаются перед ним приглушённой
  // строкой пути. Так вложенность видна целиком, а лишних пустых строк нет.
  function gitDirLabel(segments) {
    const last = segments[segments.length - 1]
    const lead = segments.slice(0, -1)
    return `${lead.length ? `<i class="nc-dir-lead">${esc(`${lead.join('/')}/`)}</i>` : ''}${esc(last)}`
  }

  function gitDirRow(path, segments, collapsed, node) {
    const name = segments.join('/')
    const count = gitCountFiles(node)
    const childPaths = gitCollectPaths(node)
    const checkedCount = childPaths.filter(p => ui.checked.has(p)).length
    const isAllChecked = childPaths.length > 0 && checkedCount === childPaths.length
    const isIndeterminate = checkedCount > 0 && checkedCount < childPaths.length
    const words = countOf(count, 'файл', 'файла', 'файлов')
    // Счётчик нужен там, где содержимое не на виду: у свёрнутой папки и там,
    // где файлов больше одного. Подпись «1 файл» у каждой раскрытой папки
    // ничего не сообщала, а серого шума давала на весь столбец.
    const showCount = collapsed || count > 1

    return `<div class="nc-dir" data-path="${esc(path)}">
      <button type="button" class="nc-dir-twist" data-action="git-collapse" data-list="dir:${esc(path)}" aria-expanded="${collapsed ? 'false' : 'true'}" aria-label="${esc(collapsed ? `Раскрыть ${name}` : `Свернуть ${name}`)}">${gitIcon(collapsed ? 'caretRight' : 'caretDown', 13)}</button>
      <input type="checkbox" name="git-dir" value="${esc(path)}"${isAllChecked ? ' checked' : ''}${isIndeterminate ? ' data-indeterminate="true"' : ''} aria-label="${esc(`${name} — включить папку в коммит`)}">
      <button type="button" class="nc-dir-main" data-action="git-collapse" data-list="dir:${esc(path)}" title="${esc(`${path} · ${words}`)}">
        <i class="nc-dir-glyph">${gitIcon('folder', 13)}</i>
        <span>${gitDirLabel(segments)}</span>
        ${showCount ? `<em>${esc(words)}</em>` : ''}
      </button>
      <div class="nc-menu">
        <button type="button" class="nc-icon-btn nc-more" data-action="git-menu" data-menu="dir:${esc(path)}" title="Действия с папкой" aria-label="${esc(`Действия с папкой ${name}`)}">${gitIcon('dots', 13)}</button>
        ${ui.menuFor === `dir:${path}` ? `<div class="nc-menu-body">
          ${gitActionButton('discard', 'Откатить файлы папки...', 'quiet danger', { paths: childPaths, shortcut: 'Ctrl+Alt+Z' })}
        </div>` : ''}
      </div>
    </div>`
  }

  function gitTreeHtml(node, prefix) {
    let html = ''
    for (const [name, child] of [...node.dirs.entries()].sort((a, b) => a[0].localeCompare(b[0], 'ru'))) {
      const segments = [name]
      let path = prefix ? `${prefix}/${name}` : name
      let inner = child
      while (!inner.files.length && inner.dirs.size === 1) {
        const [nextName, nextNode] = [...inner.dirs.entries()][0]
        segments.push(nextName)
        path = `${path}/${nextName}`
        inner = nextNode
      }
      const collapsed = ui.collapsed.has(`dir:${path}`)
      html += gitDirRow(path, segments, collapsed, inner)
      if (!collapsed) {
        html += `<div class="nc-dir-files">${gitTreeHtml(inner, path)}</div>`
      }
    }
    for (const item of [...node.files].sort((a, b) => String(a.path).localeCompare(String(b.path), 'ru'))) {
      html += gitFileRow(item)
    }
    return html
  }

  function gitRowsHtml(items) {
    const byPath = (a, b) => String(a.path).localeCompare(String(b.path), 'ru')
    const files = [...items].sort(byPath)
    if (ui.flat) return files.map(item => gitFileRow(item)).join('')
    return gitTreeHtml(gitBuildTree(files), '')
  }

  function gitGroupMenuHtml(group) {
    const list = group.menu
    const paths = group.items.map(item => String(item.path))
    const only = group.items.length && group.selectable
      ? `<button type="button" class="quiet" data-action="git-select-only" data-list="${esc(group.id)}" title="Отметить для коммита только файлы этого списка"><span>Отметить только этот список</span></button>`
      : ''
    if (!list) {
      return `<div class="nc-menu-body">
        ${only}
        ${group.items.length ? gitActionButton('discard', 'Откатить все файлы списка...', 'quiet danger', { paths, shortcut: 'Ctrl+Alt+Z' }) : ''}
      </div>`
    }
    return `<div class="nc-menu-body">
      ${only}
      ${list.active ? '<small>Новые изменения уже попадают сюда</small>' : gitActionButton('setActiveList', 'Собирать новое здесь', 'quiet', { list: list.id })}
      ${gitActionButton('renameList', 'Переименовать список...', 'quiet', { list: list.id, shortcut: 'F2' })}
      ${group.items.length ? gitActionButton('discard', 'Откатить файлы списка...', 'quiet danger', { paths, shortcut: 'Ctrl+Alt+Z' }) : ''}
      ${list.id === 'default'
        ? '<small>Основной список удалить нельзя</small>'
        : gitActionButton('deleteList', 'Удалить список изменений', 'quiet danger', { list: list.id, title: 'Файлы вернутся в основной список' })}
    </div>`
  }

  function gitGroupCount(checked, total) {
    if (!total) return 'пусто'
    if (checked && checked < total) return `${checked} из ${countOf(total, 'файла', 'файлов', 'файлов')}`
    return countOf(total, 'файл', 'файла', 'файлов')
  }

  function gitGroupHtml(group) {
    const items = group.items
    const collapsed = ui.collapsed.has(group.id)
    const checked = items.filter(item => ui.checked.has(String(item.path))).length
    const box = items.length
      ? `<input type="checkbox" name="git-group" value="${esc(group.id)}"${checked === items.length ? ' checked' : ''}${checked > 0 && checked < items.length ? ' data-indeterminate="true"' : ''} aria-label="${esc(`${group.title} — включить в коммит`)}">`
      : '<i class="nc-box-dot" aria-hidden="true"></i>'
    const menu = group.menu || (items.length && group.selectable)

    return `<section class="nc-group tone-${esc(group.tone)}${collapsed ? ' is-collapsed' : ''}${group.active ? ' is-active' : ''}" data-list="${esc(group.id)}">
      <header>
        <button type="button" class="nc-twist" data-action="git-collapse" data-list="${esc(group.id)}" aria-expanded="${collapsed ? 'false' : 'true'}" aria-label="${esc(collapsed ? `Раскрыть ${group.title}` : `Свернуть ${group.title}`)}">${gitIcon(collapsed ? 'caretRight' : 'caretDown', 13)}</button>
        ${box}
        <button type="button" class="nc-group-main" data-action="git-collapse" data-list="${esc(group.id)}" title="${esc(group.note ? `${group.title} · ${group.note}` : group.title)}">
          <strong>${esc(group.title)}</strong>
          <em>${esc(gitGroupCount(checked, items.length))}</em>
        </button>
        <i class="nc-gap"></i>
        ${group.active && gitLists().length > 1 ? '<span class="nc-chip">активный</span>' : ''}
        ${menu ? `<div class="nc-menu">
          <button type="button" class="nc-icon-btn nc-more" data-action="git-menu" data-menu="list:${esc(group.id)}" title="Действия со списком" aria-label="${esc(`Действия со списком ${group.title}`)}">${gitIcon('dots', 13)}</button>
          ${ui.menuFor === `list:${group.id}` ? gitGroupMenuHtml(group) : ''}
        </div>` : ''}
      </header>
      ${collapsed ? '' : `<div class="nc-group-body">${items.length
        ? gitRowsHtml(items)
        : `<p class="nc-empty">${esc(group.empty)}</p>`}</div>`}
    </section>`
  }

  function gitCommitDate(value) {
    const date = new Date(value)
    if (!value || Number.isNaN(date.getTime())) return ''
    return date.toLocaleString('ru-RU', { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' })
  }

  function gitCommitHint(changes, checked, conflicts) {
    if (conflicts) return 'Сначала разрешите конфликты'
    if (checked) return ''
    if (ui.amend) return 'Сменится только сообщение'
    return changes ? 'Отметьте файлы для коммита' : 'Локальных изменений нет'
  }

  function syncGitCommitButtons() {
    const changes = gitChanges()
    const checked = gitCheckedPaths().length
    const conflicts = changes.some(item => item.area === 'conflict')
    const blocked = Boolean(ui.pendingAction) || !ui.commitDraft.trim() || (!checked && !ui.amend) || conflicts
    const submit = root.querySelector('#git-commit-submit')
    const push = root.querySelector('#git-commit-push')
    const hint = root.querySelector('#git-commit-hint')
    const picked = root.querySelector('#git-commit-picked')
    if (submit) {
      submit.disabled = blocked
      if (!ui.pendingAction) submit.textContent = ui.amend ? 'Переписать' : 'Коммит'
    }
    if (push) push.disabled = blocked
    if (picked) picked.textContent = `выбрано ${checked} из ${changes.length}`
    if (hint && !ui.pendingAction) {
      const text = gitCommitHint(changes.length, checked, conflicts)
      hint.textContent = text
      hint.hidden = !text
    }
  }

  function syncGitTree() {
    for (const box of root.querySelectorAll('input[name="git-group"]')) {
      const paths = gitGroupPaths(String(box.value || ''))
      const checked = paths.filter(path => ui.checked.has(path)).length
      box.checked = paths.length > 0 && checked === paths.length
      box.indeterminate = checked > 0 && checked < paths.length
      const counter = box.closest('.nc-group')?.querySelector('.nc-group-main em')
      if (counter) counter.textContent = gitGroupCount(checked, paths.length)
    }
    for (const box of root.querySelectorAll('input[name="git-dir"]')) {
      const dir = String(box.value || '')
      const paths = gitChanges()
        .map(item => String(item.path || ''))
        .filter(path => path === dir || path.startsWith(`${dir}/`))
      const checked = paths.filter(path => ui.checked.has(path)).length
      box.checked = paths.length > 0 && checked === paths.length
      box.indeterminate = checked > 0 && checked < paths.length
    }
  }

  function gitGroups() {
    const changes = gitChanges()
    const byPath = (a, b) => String(a.path).localeCompare(String(b.path), 'ru')
    const groups = []
    const conflicts = changes.filter(item => item.area === 'conflict').sort(byPath)
    if (conflicts.length) {
      groups.push({
        id: 'conflict', tone: 'conflict', title: 'Конфликты', note: 'держат коммит',
        items: conflicts, empty: '', selectable: false,
      })
    }
    for (const list of gitLists()) {
      groups.push({
        id: list.id,
        tone: 'change',
        title: list.name,
        note: list.active ? 'новое попадает сюда' : '',
        items: changes.filter(item => gitGroupOf(item) === list.id).sort(byPath),
        empty: 'Пусто. Перетащите сюда файлы из другого списка.',
        selectable: true,
        active: Boolean(list.active),
        menu: list,
      })
    }
    const untracked = changes.filter(item => gitGroupOf(item) === 'untracked').sort(byPath)
    groups.push({
      id: 'untracked', tone: 'untracked', title: 'Вне репозитория',
      note: 'Git их ещё не видел',
      items: untracked, empty: 'Новых файлов нет.', selectable: true,
    })
    return groups
  }

  function gitStashDetailHtml(data) {
    const stashes = Array.isArray(data.stashes) ? data.stashes : []
    const stash = stashes.find(item => item.ref === ui.selectedStash) || stashes[0]
    if (!stash) return '<p class="nc-empty">Полка пуста. Отложить незаконченное можно кнопкой сверху.</p>'
    const files = Array.isArray(stash.files) ? stash.files : []
    return `<div class="nc-detail">
      <strong>${esc(stash.message || stash.ref)}</strong>
      <div class="nc-detail-meta"><span class="nc-hash">${esc(stash.ref)}</span><span>${esc(gitCommitDate(stash.when))}</span></div>
      <div class="nc-rule"></div>
      <div class="nc-detail-files">${files.map(file =>
        `<div class="nc-detail-file"><b>${esc(String(file.status || 'M').slice(0, 1))}</b><span>${esc(file.path)}</span></div>`
      ).join('') || '<p class="nc-empty">Состав записи прочитать не удалось.</p>'}</div>
      <div class="nc-detail-actions">
        ${gitActionButton('stashApply', 'Вернуть в работу', 'secondary', { stash: stash.ref, title: 'Применить отложенное к рабочей копии' })}
        ${gitActionButton('stashDrop', 'Удалить', 'quiet danger', { stash: stash.ref })}
      </div>
    </div>`
  }

  function gitListBodyHtml(data) {
    if (gitActiveTab() === 'stash') {
      const stashes = Array.isArray(data.stashes) ? data.stashes : []
      if (!stashes.length) return '<p class="nc-empty">Полка пуста.</p>'
      return stashes.map(item => {
        const selected = ui.selectedStash === item.ref || (!ui.selectedStash && item === stashes[0])
        return `<button type="button" class="nc-row nc-stash${selected ? ' is-selected' : ''}" data-action="git-select-stash" data-stash="${esc(item.ref)}">
          ${gitIcon('stash', 13)}
          <span>
            <strong>${esc(item.message || item.ref)}</strong>
            <small><b class="nc-hash">${esc(item.ref)}</b><span>${esc(gitCommitDate(item.when))}</span><span>${esc(countOf((item.files || []).length, 'файл', 'файла', 'файлов'))}</span></small>
          </span>
        </button>`
      }).join('')
    }
    if (!gitChanges().length) {
      return `<div class="nc-clean">${gitIcon('check', 22)}<strong>Изменений нет</strong><p>Рабочая копия совпадает с последним коммитом. Отложенное лежит на полке, прошлое — в истории.</p></div>`
    }
    const groups = gitGroups()
    return `${groups.map(gitGroupHtml).join('')}
      ${Number(data.changesHidden || 0) ? `<p class="nc-empty nc-cut">…и ещё ${esc(countOf(Number(data.changesHidden), 'файл', 'файла', 'файлов'))}: панель показывает первые триста</p>` : ''}`
  }

  function gitToolView() {
    const data = getData() || {}
    if (!data.loaded) return shell(`<main class="point-tool-page">${toolWindowHeading('GIT', 'Репозиторий', 'Загружаем ветку и изменения…')}<div class="point-tool-loading"><span class="spinner"></span>Читаем Git</div></main>`)
    if (!data.available) {
      return shell(`<main class="point-tool-page">${toolWindowHeading('GIT', 'Репозиторий не найден', 'Откройте или клонируйте Git-проект.')}<section class="point-tool-empty"><strong>Нет локального репозитория</strong><p>Клонирование откроет безопасный мастер выбора URL и папки.</p>${toolCommandButton('localAgent.gitClone', 'Клонировать из Git', 'primary')}</section></main>`)
    }
    const changes = gitChanges()
    const conflicts = changes.filter(item => item.area === 'conflict')
    const checked = gitCheckedPaths()
    const commits = Array.isArray(data.commits) ? data.commits : []
    const repositories = Array.isArray(data.repositories) ? data.repositories : []
    const busy = Boolean(ui.pendingAction)
    const branch = data.branch || (data.detached ? `detached · ${String(data.head || '').slice(0, 8)}` : 'без ветки')
    const syncText = data.operation
      ? `Сейчас выполняется ${data.operation === 'rebase' ? 'rebase' : 'слияние'}`
      : data.remote ? `Связана с ${data.remote}` : 'Ветка ещё не опубликована'
    const tab = GIT_TABS.find(item => item.id === ui.tab) || GIT_TABS[0]
    const notice = ui.notice
      ? `<div class="nc-notice ${ui.notice.tone === 'error' ? 'is-error' : 'is-ok'}">${gitIcon(ui.notice.tone === 'error' ? 'warning' : 'check', 14)}<p>${esc(ui.notice.text || '')}</p><button type="button" class="nc-icon-btn" data-action="dismiss-git-notice" aria-label="Скрыть">${gitIcon('plus', 12)}</button></div>`
      : ''
    const blocked = busy || !ui.commitDraft.trim() || (!checked.length && !ui.amend) || Boolean(conflicts.length)
    const hint = gitCommitHint(changes.length, checked.length, conflicts.length)
    const target = ui.target || data.remote || 'ветка не опубликована'
    const targets = Array.isArray(data.pushTargets) ? data.pushTargets : []

    return shell(`<main class="nc-app">
      <header class="nc-head">
        <span class="nc-brand">${gitIcon('branch', 14)}<b>Git</b></span>
        <div class="nc-tabs" role="tablist" data-keynav="row">
          ${/* Роуминговый tabindex: полоса вкладок — один остановочный пункт
                табуляции, а не пять. Внутри полосы переключают стрелки. */''}
          ${GIT_TABS.map(item => `<button type="button" role="tab" aria-selected="${item.id === tab.id ? 'true' : 'false'}" tabindex="${item.id === tab.id ? '0' : '-1'}" class="nc-tab${item.id === tab.id ? ' is-active' : ''}" data-action="git-tab" data-tab="${esc(item.id)}" title="${esc(item.title)}">${gitIcon(item.icon, 13)}<span>${esc(item.label)}</span></button>`).join('')}
        </div>
        <i class="nc-gap"></i>
        ${/* Журнал открывается тем же действием, что и раньше вела кнопка
              «История» из меню Git-панели: путь один, а не два. */''}
        <button type="button" class="nc-icon-btn" data-action="git-action" data-git-action="history" title="Журнал коммитов: граф, поиск и состав (Alt+9)" aria-label="Открыть журнал коммитов">${gitIcon('history', 14)}</button>
        ${repositories.length > 1 ? `<button type="button" class="nc-icon-btn" data-action="git-action" data-git-action="selectRepository" title="${esc(`Репозиторий · ${data.repository || ''}`)}"${busy ? ' disabled' : ''}>${gitIcon('file', 13)}</button>` : ''}
        <button type="button" class="nc-icon-btn" data-action="refresh-tool-window" title="${esc(`${branch} · ${syncText}. Обновить панель`)}" aria-label="Обновить">${gitIcon('refresh', 14)}</button>
      </header>
      <div class="nc-body${tab.id === 'changes' ? ' is-single' : ''}">
        <section class="nc-list">
          <header class="nc-sub">
            <span class="nc-sub-title">${esc(tab.title)}</span>
            <i class="nc-gap"></i>
            ${tab.id === 'changes' ? `
              <button type="button" class="nc-icon-btn" data-action="git-action" data-git-action="pull" title="${esc(data.behind ? `Забрать ${data.behind} входящих из ${data.remote || 'сервера'}` : 'Забрать входящие изменения (Pull)')}"${busy || conflicts.length ? ' disabled' : ''}>${gitIcon('down', 13)}${Number(data.behind || 0) ? `<b>${Number(data.behind)}</b>` : ''}</button>
              <button type="button" class="nc-icon-btn" data-action="git-action" data-git-action="push" title="${esc(data.remote ? `Отправить в ${data.remote} (Push)` : 'Опубликовать ветку')}"${busy || conflicts.length ? ' disabled' : ''}>${gitIcon('up', 13)}${Number(data.ahead || 0) ? `<b>${Number(data.ahead)}</b>` : ''}</button>
              ${checked.length ? `<button type="button" class="nc-icon-btn" data-action="git-action" data-git-action="discard" data-use-checked="1" title="Откатить отмеченные изменения" aria-label="Откатить отмеченные изменения">${gitIcon('undo', 13)}</button>` : ''}
              <button type="button" class="nc-icon-btn" data-action="git-collapse-all" title="Свернуть / развернуть все списки" aria-label="Свернуть / развернуть все списки">${gitIcon('minus', 13)}</button>
              <button type="button" class="nc-icon-btn" data-action="git-layout" data-flat="${ui.flat ? '0' : '1'}" title="${esc(ui.flat ? 'Показать деревом папок' : 'Показать плоским списком')}" aria-label="${esc(ui.flat ? 'Показать деревом папок' : 'Показать плоским списком')}">${gitIcon(ui.flat ? 'tree' : 'list', 14)}</button>
              <button type="button" class="nc-icon-btn" data-action="git-action" data-git-action="createList" title="Новый список изменений: отмеченные файлы уедут в него" aria-label="Новый список изменений"${busy ? ' disabled' : ''}>${gitIcon('plus', 14)}</button>
            ` : ''}
            ${tab.id === 'stash' ? `<button type="button" class="nc-icon-btn" data-action="git-action" data-git-action="stashPush" title="Отложить текущие изменения на полку"${busy ? ' disabled' : ''}>${gitIcon('plus', 13)}</button>` : ''}
          </header>
          ${conflicts.length ? `<div class="nc-banner">${gitIcon('warning', 14)}<p>${esc(countOf(conflicts.length, 'файл разошёлся', 'файла разошлись', 'файлов разошлись'))} с чужой правкой — соберите итог, иначе коммит не собрать.</p></div>` : ''}
          <div class="nc-scroll point-git-tree${ui.flat ? ' is-flat' : ''}">${gitListBodyHtml(data)}</div>
        </section>
        ${tab.id === 'changes' ? '' : `<section class="nc-diff">
          <header class="nc-sub">
            <span class="nc-path">Запись полки</span>
          </header>
          <div class="nc-scroll nc-diff-body">${gitStashDetailHtml(data)}</div>
        </section>`}
      </div>
      ${notice}
      ${tab.id === 'changes' ? `
        <form id="git-commit-form" class="nc-commit">
          <div class="nc-commit-head">
            <label class="nc-amend"><input type="checkbox" name="git-amend"${ui.amend ? ' checked' : ''}${commits.length ? '' : ' disabled'}><span>Дополнить</span></label>
            <div class="nc-menu">
              <button type="button" class="nc-ghost" data-action="git-menu" data-menu="message" title="История сообщений коммитов"${commits.length ? '' : ' disabled'}>${gitIcon('history', 12)}<span>История</span></button>
              ${ui.menuFor === 'message' ? `<div class="nc-menu-body">${commits.length
                ? `<small>Взять сообщение из истории</small>${commits.slice(0, 6).map(item => `<button type="button" class="quiet" data-action="git-reuse-message" data-message="${esc(item.message || '')}"><span>${esc((item.message || 'Коммит без сообщения').slice(0, 60))}</span></button>`).join('')}`
                : `<small>${esc(data.historyError || 'Коммитов пока нет')}</small>`}</div>` : ''}
            </div>
            <div class="nc-commit-chips">
              <button type="button" class="nc-chip-btn" data-action="git-insert-tag" data-tag="feat: ">feat</button>
              <button type="button" class="nc-chip-btn" data-action="git-insert-tag" data-tag="fix: ">fix</button>
              <button type="button" class="nc-chip-btn" data-action="git-insert-tag" data-tag="refactor: ">refactor</button>
              <button type="button" class="nc-chip-btn" data-action="git-insert-tag" data-tag="docs: ">docs</button>
            </div>
            <i class="nc-gap"></i>
            <small id="git-commit-picked">выбрано ${checked.length} из ${changes.length}</small>
          </div>
          <textarea id="git-commit-message" maxlength="8192" placeholder="${esc(ui.amend ? 'Новое сообщение последнего коммита' : 'Что изменилось и почему')}"${busy ? ' disabled' : ''}>${esc(ui.commitDraft)}</textarea>
          <div class="nc-commit-foot">
            <small id="git-commit-hint"${hint ? '' : ' hidden'}>${esc(hint)}</small>
            <div class="nc-menu">
              <button type="button" class="nc-target" data-action="git-menu" data-menu="target" title="Куда отправлять ветку">${gitIcon('branch', 12)}<span>${esc(target)}</span>${gitIcon('caretDown', 11)}</button>
              ${ui.menuFor === 'target' ? `<div class="nc-menu-body">${targets.length
                ? `<small>Отправлять в</small>${targets.map(item => `<button type="button" class="quiet${item.name === target ? ' is-on' : ''}" data-action="git-action" data-git-action="setPushTarget" title="${esc(item.tracked ? 'отслеживается' : '')}"><span>${esc(item.name)}</span></button>`).join('')}`
                : '<small>Удалённых веток нет</small>'}</div>` : ''}
            </div>
            <div class="nc-commit-actions">
              <button id="git-commit-submit" type="submit" class="nc-btn"${blocked ? ' disabled' : ''}>${ui.pendingAction === 'commit' ? 'Коммит…' : ui.amend ? 'Переписать' : 'Коммит'}</button>
              <button id="git-commit-push" type="button" class="nc-btn is-primary" data-action="git-commit-push"${blocked ? ' disabled' : ''} title="Создать коммит и сразу отправить ветку">${gitIcon('cloud', 13)}<span>${ui.pendingAction === 'commitAndPush' ? 'Отправляем…' : 'Коммит и пуш'}</span></button>
            </div>
          </div>
        </form>
      ` : ''}
    </main>`)
  }

  return {
    gitFileKind,
    gitChanges,
    gitLists,
    gitCheckedPaths,
    gitGroupOf,
    gitGroups,
    gitGroupPaths,
    syncGitChecked,
    syncGitCommitButtons,
    syncGitTree,
    gitToolView,
  }
}
