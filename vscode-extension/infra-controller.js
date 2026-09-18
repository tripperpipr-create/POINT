const { formatVcsError } = require('./extension-utils')
// Infra surfaces: Git tool window, Docker, SSH servers, and database connections.
const vscode = require('vscode')
const path = require('path')


async function handleGitAction(message) {
  const action = String(message?.action || '')
  const { repositories, repo } = await this.gitContext(message?.repoRoot)
  if (action === 'selectRepository') {
    if (!repositories.length) throw new Error('В открытом проекте Git-репозитории не найдены.')
    const selected = await vscode.window.showQuickPick(repositories.map(item => ({
      label: path.basename(item.rootUri.fsPath) || item.rootUri.fsPath,
      description: item.rootUri.fsPath,
      repo: item,
    })), { title: 'Git · выбрать репозиторий', placeHolder: 'Репозиторий для панели Git' })
    if (!selected?.repo) return 'Репозиторий не изменён'
    this.gitSelectedRoot = selected.repo.rootUri.fsPath
    this.bindGitRepository(selected.repo)
    return `Открыт репозиторий ${selected.label}`
  }
  if (!repo?.state) throw new Error('Git-репозиторий не найден. Откройте проект или клонируйте его.')

  const changes = this.gitChanges(repo)
  const root = repo.rootUri?.fsPath || ''
  const lists = await this.syncGitLists(root, changes)
  const cleanPath = value => String(value || '').replace(/\\/g, '/').replace(/^\.\//, '')
  const targetPath = cleanPath(message?.path)
  const change = targetPath ? changes.find(item => item.path === targetPath) : undefined
  const requireChange = () => {
    if (!change?.uri) throw new Error('Файл уже изменился. Обновите Git-панель и повторите действие.')
    return change
  }
  // Отмеченные файлы приходят из панели списком путей. Берём только те, что
  // Git всё ещё считает изменёнными: между отметкой и нажатием кнопки файл
  // могли сохранить обратно.
  const selection = [...new Set((Array.isArray(message?.paths) ? message.paths : []).map(cleanPath).filter(Boolean))]
  const selected = changes.filter(item => selection.includes(item.path))
  const listById = id => lists.lists.find(item => item.id === String(id || ''))
  // Переименование живёт в индексе двумя записями: удалением старого пути и
  // добавлением нового. Убирая такой файл из коммита, надо убрать обе —
  // иначе снятый с отметки файл всё равно уезжает в коммит удалением.
  const entryUris = item => {
    const uris = [item.uri]
    if (item.originalPath && item.originalPath !== item.path) {
      uris.push(vscode.Uri.file(path.join(root, item.originalPath)))
    }
    return uris
  }
  const askListName = async (title, value = '') => {
    const name = String(await vscode.window.showInputBox({
      title, value, prompt: 'Как назвать папку изменений', placeHolder: 'Например: рефакторинг',
      validateInput: input => {
        const text = String(input || '').trim()
        if (!text) return 'Введите название'
        if (text.length > 60) return 'Слишком длинное название (максимум 60 символов)'
        if (text !== value && lists.lists.some(item => item.name === text)) return 'Папка с таким названием уже есть'
        return undefined
      },
    }) || '').trim()
    return name
  }

  if (action === 'stashApply') {
    const ref = String(message?.stash || '')
    if (!ref) throw new Error('Запись полки не выбрана.')
    await runGit(root, ['stash', 'apply', ref])
    return `Полка возвращена в рабочую копию: ${ref}`
  }
  if (action === 'stashDrop') {
    const ref = String(message?.stash || '')
    if (!ref) throw new Error('Запись полки не выбрана.')
    const confirm = await vscode.window.showWarningMessage(
      `Удалить запись полки «${ref}»?`,
      { modal: true, detail: 'Отложенные изменения будут потеряны — Git хранит их только здесь.' },
      'Удалить',
    )
    if (!confirm) return 'Удаление отменено'
    await runGit(root, ['stash', 'drop', ref])
    return `Запись полки удалена: ${ref}`
  }
  if (action === 'stashPush') {
    const title = String(await vscode.window.showInputBox({
      title: 'Git · отложить изменения на полку',
      prompt: 'Как назвать отложенное',
      placeHolder: 'Например: правки перед ревью',
    }) || '').trim()
    if (!title) return 'Откладывание отменено'
    await runGit(root, ['stash', 'push', '-u', '-m', title])
    return `Отложено на полку: ${title}`
  }
  if (action === 'setPushTarget') {
    // Цель отправки — выбор человека на этот сеанс, а не настройка Git:
    // менять upstream втихую панель не станет.
    this.gitPushTarget = String(message?.target || '')
    return this.gitPushTarget ? `Отправлять в ${this.gitPushTarget}` : 'Цель отправки сброшена'
  }
  if (action === 'openChange') {
    await vscode.commands.executeCommand('git.openChange', requireChange().uri)
    return 'Diff открыт в редакторе'
  }
  if (action === 'openFile') {
    const item = requireChange()
    await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(item.uri), { preview: true })
    return 'Файл открыт в редакторе'
  }
  if (action === 'createList') {
    const name = await askListName('Git · новая папка изменений')
    if (!name) return 'Создание папки отменено'
    const id = `list-${Date.now().toString(36)}`
    const assign = { ...lists.assign }
    for (const item of selected) if (item.area !== 'untracked') assign[item.path] = id
    await this.saveGitLists(root, { lists: [...lists.lists, { id, name }], active: id, assign })
    return selected.length
      ? `Папка «${name}» создана · перенесено файлов: ${selected.length}`
      : `Папка «${name}» создана и стала активной`
  }
  if (action === 'renameList') {
    const list = listById(message?.list)
    if (!list) throw new Error('Папка изменений не найдена. Обновите панель.')
    const name = await askListName('Git · переименовать папку', list.name)
    if (!name || name === list.name) return 'Название не изменено'
    await this.saveGitLists(root, {
      ...lists,
      lists: lists.lists.map(item => item.id === list.id ? { ...item, name } : item),
    })
    return `Папка переименована: ${name}`
  }
  if (action === 'deleteList') {
    const list = listById(message?.list)
    if (!list) throw new Error('Папка изменений не найдена. Обновите панель.')
    if (list.id === GIT_DEFAULT_LIST) throw new Error('Основную папку удалить нельзя — в неё возвращаются файлы из удалённых.')
    // Файлы не пропадают вместе с папкой: они возвращаются в основную.
    const assign = Object.fromEntries(Object.entries(lists.assign)
      .map(([file, id]) => [file, id === list.id ? GIT_DEFAULT_LIST : id]))
    await this.saveGitLists(root, {
      lists: lists.lists.filter(item => item.id !== list.id),
      active: lists.active === list.id ? GIT_DEFAULT_LIST : lists.active,
      assign,
    })
    return `Папка «${list.name}» удалена · файлы вернулись в основную`
  }
  if (action === 'setActiveList') {
    const list = listById(message?.list)
    if (!list) throw new Error('Папка изменений не найдена. Обновите панель.')
    await this.saveGitLists(root, { ...lists, active: list.id })
    return `Новые изменения попадут в «${list.name}»`
  }
  if (action === 'moveToList') {
    const moving = (selected.length ? selected : change ? [change] : []).filter(item => item.area !== 'untracked')
    if (!moving.length) throw new Error('Выберите файлы, которые нужно перенести.')
    let list = listById(message?.list)
    if (!list) {
      if (String(message?.list || '') !== 'new') throw new Error('Папка изменений не найдена. Обновите панель.')
      const name = await askListName('Git · новая папка изменений')
      if (!name) return 'Перенос отменён'
      list = { id: `list-${Date.now().toString(36)}`, name }
      lists.lists.push(list)
    }
    const assign = { ...lists.assign }
    for (const item of moving) assign[item.path] = list.id
    await this.saveGitLists(root, { ...lists, assign })
    // В сообщении — имя файла, а не путь: путь в узкой панели переносится на
    // две строки и вытесняет всё остальное.
    const moved = moving[0].path.split('/').pop()
    return moving.length === 1
      ? `${moved} → «${list.name}»`
      : `Перенесено в «${list.name}» файлов: ${moving.length}`
  }
  if (action === 'discard') {
    // Один файл — из меню строки; несколько — из галочек или меню папки.
    const targets = (selected.length ? selected : change ? [change] : [])
    if (!targets.length) throw new Error('Выберите файлы, которые нужно откатить.')
    const onlyUntracked = targets.every(item => item.area === 'untracked')
    const label = targets.length === 1
      ? (onlyUntracked
        ? `Переместить новый файл «${targets[0].path}» в корзину?`
        : `Отменить локальные изменения в «${targets[0].path}»?`)
      : (onlyUntracked
        ? `Переместить ${targets.length} новых файлов в корзину?`
        : `Отменить локальные изменения в ${targets.length} файлах?`)
    const confirm = await vscode.window.showWarningMessage(
      label,
      {
        modal: true,
        detail: onlyUntracked
          ? 'Файлы ещё не сохранены в Git. Их можно будет восстановить из корзины.'
          : 'Файлы вернутся к версии из текущего коммита. Это действие нельзя отменить в Point.',
      },
      onlyUntracked ? 'В корзину' : 'Отменить изменения',
    )
    if (!confirm) return 'Откат отменён'
    for (const item of targets) {
      if (item.area === 'untracked') {
        await vscode.workspace.fs.delete(item.uri, { recursive: true, useTrash: true })
        continue
      }
      // Откат целиком: и то, что уже в индексе, и то, что рядом.
      if (item.staged) await this.gitFileCommand(repo, 'revert', entryUris(item))
      await this.gitFileCommand(repo, 'clean', entryUris(item))
    }
    if (targets.length === 1) {
      return onlyUntracked
        ? `Перемещено в корзину: ${targets[0].path}`
        : `Изменения отменены: ${targets[0].path}`
    }
    return onlyUntracked
      ? `Перемещено в корзину файлов: ${targets.length}`
      : `Изменения отменены в файлах: ${targets.length}`
  }
  if (action === 'commit' || action === 'commitAndPush') {
    const commitMessage = String(message?.message || '').replace(/\r\n/g, '\n').trim()
    const conflicts = changes.filter(item => item.area === 'conflict')
    if (!commitMessage) throw new Error('Напишите, что изменилось, перед созданием коммита.')
    if (commitMessage.length > 8192) throw new Error('Сообщение коммита слишком длинное (максимум 8192 символа).')
    if (conflicts.length) throw new Error('Сначала разрешите конфликты — коммит с ними не собрать.')
    const amend = Boolean(message?.amend)
    if (!selected.length && !amend) throw new Error('Отметьте хотя бы один файл — коммит соберётся из отмеченных.')
    if (amend && !repo.state.HEAD?.commit) throw new Error('Править нечего: в этой ветке ещё нет коммитов.')
    // Коммит собирается из отмеченных путей, а не из индекса: `git commit --
    // <пути>` берёт содержимое рабочего дерева ровно по ним и всё остальное
    // оставляет как есть. Через индекс было бы проще, но пришлось бы сначала
    // снять с подготовки чужую работу — а её никто не просил трогать, и
    // переименование, которое панель показывает одной строкой, при этом
    // разваливалось на удаление и новый файл.
    //
    // Новые файлы Git по имени не найдёт: пока он о них не знает, путь для
    // него не путь. Их добавление и есть та самая отметка в панели.
    const fresh = selected.filter(item => item.area === 'untracked')
    if (fresh.length) await this.gitFileCommand(repo, 'add', fresh.map(item => item.uri))
    const paths = [...new Set(selected.flatMap(item => [item.path, item.originalPath].filter(Boolean)))]
    await this.commitPaths(root, commitMessage, paths, amend)
    const title = commitMessage.split('\n', 1)[0].slice(0, 60)
    const count = `${selected.length}`
    const done = amend
      ? (selected.length ? `Последний коммит переписан и принял ${count} — «${title}»` : `Сообщение последнего коммита: «${title}»`)
      : `Коммит на ${count} — «${title}»`
    if (action !== 'commitAndPush') return done
    const pushed = await this.pushCurrentBranch(repo)
    return pushed ? `${done} · отправлено в ${pushed.remote}/${pushed.branch}` : `${done} · отправка отменена`
  }
  if (action === 'fetch') {
    await repo.fetch({ all: true, prune: true })
    return 'Данные с удалённых репозиториев обновлены'
  }
  if (action === 'pull') {
    if ((repo.state.mergeChanges || []).length) throw new Error('Pull недоступен, пока есть неразрешённые конфликты.')
    const dirty = changes.length > 0
    if (dirty) {
      const confirm = await vscode.window.showWarningMessage('В рабочей копии есть локальные изменения. Выполнить Pull?', { modal: true, detail: 'Git попробует объединить входящие изменения с вашей работой.' }, 'Выполнить Pull')
      if (!confirm) return 'Pull отменён'
    }
    await repo.pull()
    return 'Входящие изменения получены'
  }
  if (action === 'push') {
    const pushed = await this.pushCurrentBranch(repo)
    return pushed ? `Ветка ${pushed.branch} отправлена в ${pushed.remote}` : 'Push отменён'
  }
  if (action === 'switchBranch') {
    const branches = [...new Set((repo.state.refs || []).filter(item => Number(item?.type) === 0 && item?.name).map(item => String(item.name)))]
    if (!branches.length) throw new Error('Локальные ветки не найдены.')
    const current = String(repo.state.HEAD?.name || '')
    const selected = await vscode.window.showQuickPick(branches.map(name => ({ label: name, description: name === current ? 'текущая' : '' })), { title: 'Git · переключить ветку', placeHolder: current || 'Выберите ветку' })
    if (!selected?.label || selected.label === current) return 'Ветка не изменена'
    await repo.checkout(selected.label)
    return `Открыта ветка ${selected.label}`
  }
  if (action === 'createBranch') {
    const name = String(await vscode.window.showInputBox({ title: 'Git · новая ветка', prompt: 'Короткое имя без пробелов', placeHolder: 'feature/понятное-имя', validateInput: value => {
      const branch = String(value || '').trim()
      if (!branch) return 'Введите имя ветки'
      const forbidden = ['~', '^', ':', '?', '*', '[', '\\']
      if (/\s/.test(branch) || forbidden.some(mark => branch.includes(mark)) || branch.includes('..') || branch.startsWith('/') || branch.endsWith('/') || branch.endsWith('.')) return 'Это имя нельзя использовать для ветки Git'
      return undefined
    } }) || '').trim()
    if (!name) return 'Создание ветки отменено'
    await repo.createBranch(name, true)
    return `Создана и открыта ветка ${name}`
  }
  if (action === 'history') {
    await vscode.commands.executeCommand('localAgent.openChronicle')
    return 'История открыта'
  }
  throw new Error('Неизвестное действие Git-панели.')
}

async function handleInfraMessage(message) {
  switch (message.type) {
    case 'gitAction': {
      const action = String(message.action || '')
      try {
        const result = await this.handleGitAction(message)
        const snapshot = await this.toolWindowSnapshot('git')
        this.postToolWindow('git', {
          type: 'gitActionResult', ok: true, action,
          message: String(result || 'Готово'), snapshot,
        })
      } catch (error) {
        this.postToolWindow('git', {
          type: 'gitActionResult', ok: false, action,
          message: formatVcsError('Git', error),
        })
      }
      break
    }
    case 'loadToolWindowState': {
      const kind = String(message.kind || '')
      if (!this.toolWindows.has(kind)) break
      const snapshot = await this.toolWindowSnapshot(kind)
      void this.toolWindows.get(kind)?.webview.postMessage({ type: 'toolWindowState', snapshot })
      if (kind === 'git' && this.pendingGitFocus) {
        this.pendingGitFocus = false
        this.postToolWindow('git', { type: 'gitFocusMessage' })
      }
      break
    }
    case 'loadDocker': {
      const docker = await this.service.request('/api/docker')
      this.post({ type: 'docker', docker })
      break
    }
    case 'dockerContainerAction': {
      const action = String(message.action || '').toLowerCase()
      const container = String(message.container || '').trim()
      if (!container || (action !== 'start' && action !== 'stop')) {
        throw new Error('Укажите контейнер и действие start/stop.')
      }
      const label = action === 'start' ? 'Запустить' : 'Остановить'
      const answer = await vscode.window.showWarningMessage(
        `${label} контейнер «${container}»? Удаление из этой панели недоступно.`,
        { modal: true },
        label,
      )
      if (answer !== label) break
      await this.service.request('/api/docker/containers/action', {
        method: 'POST',
        body: JSON.stringify({ action, container }),
      })
      const docker = await this.service.request('/api/docker')
      this.post({ type: 'docker', docker })
      break
    }
    case 'dockerLogs': {
      const container = String(message.container || '').trim()
      if (!container) throw new Error('Укажите контейнер для логов.')
      const logs = await this.service.request(`/api/docker/logs?container=${encodeURIComponent(container)}&tail=120`)
      this.post({ type: 'dockerLogs', logs })
      break
    }
    case 'dockerOpenTerminal': {
      const container = String(message.container || '').trim()
      const mode = String(message.mode || 'logs').toLowerCase()
      if (container && !/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(container)) {
        throw new Error('Некорректное имя контейнера.')
      }
      const term = vscode.window.createTerminal({ name: container ? `Docker · ${container}` : 'Docker' })
      if (mode === 'shell' && container) {
        term.sendText(`docker exec -it ${container} sh`)
      } else if (mode === 'logs' && container) {
        term.sendText(`docker logs -f --tail 200 ${container}`)
      } else {
        term.sendText('docker ps -a')
      }
      term.show()
      break
    }
    case 'saveServerProfile': {
      try {
        const authMethod = String(message.authMethod || 'agent')
        const password = String(message.password || '')
        let secretRef = String(message.secretRef || '')
        if (authMethod === 'password' && password && !secretRef) {
          secretRef = `point.server.${Date.now()}`
        }
        if (password && secretRef) {
          await this.context.secrets.store(secretRef, password)
        }
        const saved = await this.service.request('/api/servers', {
          method: 'POST',
          body: JSON.stringify({
            id: message.id || '',
            displayName: String(message.displayName || ''),
            host: String(message.host || ''),
            port: Number(message.port || 22),
            user: String(message.user || ''),
            authMethod,
            privateKeyPath: String(message.privateKeyPath || ''),
            secretRef,
            defaultRemotePath: String(message.defaultRemotePath || '~'),
          }),
        })
        this.upsertBootItem('serverProfiles', saved)
        this.focusTab('connections')
        this.postState()
        this.post({ type: 'serverProfileSaved', id: saved?.id || '' })
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'probeServerProfile': {
      try {
        const id = String(message.id || '')
        const profile = (this.boot?.serverProfiles || []).find(item => item.id === id)
        const password = profile?.secretRef ? await this.context.secrets.get(profile.secretRef) || '' : ''
        const result = await this.service.request(`/api/servers/${encodeURIComponent(id)}/probe`, {
          method: 'POST',
          body: JSON.stringify({ password }),
        })
        this.patchBoot({ serverProfiles: await this.service.request('/api/servers') })
        this.postState()
        if (result?.ok) {
          void vscode.window.showInformationMessage(`SSH: ${result.message || 'соединение установлено'}`)
        } else {
          void vscode.window.showWarningMessage(`SSH: ${result?.message || 'проверка не удалась'}`)
        }
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'listServerRemote': {
      try {
        const id = String(message.id || '')
        const profile = (this.boot?.serverProfiles || []).find(item => item.id === id)
        if (!profile) throw new Error('SSH-профиль не найден')
        await browseSSHRemotePath(this.service, this.context, profile, String(message.path || profile.defaultRemotePath || '~'))
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'openServerTerminal': {
      try {
        await openSSHTerminalForProfile(this.service, String(message.id || ''))
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'deleteServerProfile': {
      try {
        const id = String(message.id || '')
        const profile = (this.boot?.serverProfiles || []).find(item => item.id === id)
        await this.service.request(`/api/servers/${encodeURIComponent(id)}`, { method: 'DELETE' })
        if (profile?.secretRef) {
          try { await this.context.secrets.delete(profile.secretRef) } catch { /* ignore */ }
        }
        this.removeBootItem('serverProfiles', id)
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'saveDBConnection': {
      try {
        const password = String(message.password || '')
        let secretRef = String(message.secretRef || '')
        const driver = String(message.driver || 'sqlite')
        if (password && !secretRef) secretRef = `point.db.${Date.now()}`
        if (password && secretRef) await this.context.secrets.store(secretRef, password)
        const saved = await this.service.request('/api/db-connections', {
          method: 'POST',
          body: JSON.stringify({
            id: message.id || '',
            displayName: String(message.displayName || ''),
            driver,
            host: String(message.host || ''),
            port: Number(message.port || 0),
            database: String(message.database || ''),
            username: String(message.username || ''),
            secretRef,
            sslMode: String(message.sslMode || ''),
            readOnlyDefault: message.readOnlyDefault !== false,
            password,
          }),
        })
        this.upsertBootItem('dbConnections', saved)
        this.focusTab('databases')
        this.postState()
        this.post({ type: 'dbConnectionSaved', id: saved?.id || '' })
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'deleteDBConnection': {
      try {
        const id = String(message.id || '')
        const profile = (this.boot?.dbConnections || []).find(item => item.id === id)
        await this.service.request(`/api/db-connections/${encodeURIComponent(id)}`, { method: 'DELETE' })
        if (profile?.secretRef) {
          try { await this.context.secrets.delete(profile.secretRef) } catch { /* ignore */ }
        }
        this.removeBootItem('dbConnections', id)
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'testDBConnection': {
      try {
        const id = String(message.id || '')
        const profile = (this.boot?.dbConnections || []).find(item => item.id === id)
        const password = profile?.secretRef ? await this.context.secrets.get(profile.secretRef) || '' : ''
        if (profile?.secretRef && password) {
          await this.service.request('/api/db-connections/unlock', {
            method: 'POST',
            body: JSON.stringify({ secretRef: profile.secretRef, password }),
          })
        }
        const result = await this.service.request(`/api/db-connections/${encodeURIComponent(id)}/test`, {
          method: 'POST',
          body: JSON.stringify({ password }),
        })
        this.upsertBootItem('dbConnections', result)
        this.postState()
        void vscode.window.showInformationMessage(`БД: ${result?.status === 'connected' ? 'подключение успешно' : (result?.lastError || 'проверено')}`)
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'schemaDBConnection': {
      try {
        const id = String(message.id || '')
        const profile = (this.boot?.dbConnections || []).find(item => item.id === id)
        const password = profile?.secretRef ? await this.context.secrets.get(profile.secretRef) || '' : ''
        if (profile?.secretRef && password) {
          await this.service.request('/api/db-connections/unlock', {
            method: 'POST',
            body: JSON.stringify({ secretRef: profile.secretRef, password }),
          })
        }
        const result = await this.service.request(`/api/db-connections/${encodeURIComponent(id)}/schema`, {
          method: 'POST',
          body: JSON.stringify({ password }),
        })
        this.post({ type: 'dbSchemaResult', result })
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'queryDBConnection': {
      try {
        const id = String(message.connectionId || '')
        const profile = (this.boot?.dbConnections || []).find(item => item.id === id)
        const password = profile?.secretRef ? await this.context.secrets.get(profile.secretRef) || '' : ''
        if (profile?.secretRef && password) {
          await this.service.request('/api/db-connections/unlock', {
            method: 'POST',
            body: JSON.stringify({ secretRef: profile.secretRef, password }),
          })
        }
        const result = await this.service.request(`/api/db-connections/${encodeURIComponent(id)}/query`, {
          method: 'POST',
          body: JSON.stringify({
            sql: String(message.sql || ''),
            allowWrite: Boolean(message.allowWrite),
            approved: Boolean(message.approved),
            maxRows: Number(message.maxRows || 200),
            password,
          }),
        })
        this.post({ type: 'dbQueryResult', result })
      } catch (error) {
        const text = error instanceof Error ? error.message : String(error)
        if (/AllowWrite|записывающий SQL|подтверждения/i.test(text) && !message.approved) {
          this.post({ type: 'dbWriteRequired', connectionId: String(message.connectionId || ''), sql: String(message.sql || ''), message: text })
        } else {
          this.post({ type: 'dbQueryResult', error: text })
        }
      }
      break
    }
  }
}

module.exports = { handleInfraMessage, handleGitAction }
