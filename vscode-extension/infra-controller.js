const { formatVcsError } = require('./extension-utils')
// Infra surfaces: Git tool window, Docker, SSH servers, and database connections.
const vscode = require('vscode')
const path = require('path')


async function handleGitAction(message) {
  const action = String(message?.action || '')
  const coreResult = await this.runGitWorkbenchAction(message)
  if (coreResult !== undefined) return coreResult
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

  if (action === 'setPushTarget') {
    // Цель отправки — выбор человека на этот сеанс, а не настройка Git:
    // менять upstream втихую панель не станет.
    this.gitPushTarget = String(message?.target || '')
    return this.gitPushTarget ? `Отправлять в ${this.gitPushTarget}` : 'Цель отправки сброшена'
  }
  if (action === 'openMerge') {
    await vscode.commands.executeCommand('git.openMergeEditor', requireChange().uri)
    return 'Merge-редактор открыт'
  }
  if (action === 'openChange') {
    const item = requireChange()
    const api = (await this.gitContext(root)).api
    if (message.area === 'staged' && api?.toGitUri) {
      const left = api.toGitUri(vscode.Uri.file(path.join(root, item.originalPath || item.path)), 'HEAD')
      const right = api.toGitUri(item.uri, '')
      await vscode.commands.executeCommand('vscode.diff', left, right, item.path + ' · index', {preview:true})
    } else await vscode.commands.executeCommand('git.openChange', item.uri)
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
    const tracked=targets.filter(item=>item.area!=='untracked')
    if(tracked.length)await this.runGitWorkbenchAction({...message,action:'discardTracked',confirmed:true,
      paths:[...new Set(tracked.flatMap(item=>[item.path,item.originalPath].filter(Boolean)))]})
    for (const item of targets) {
      if (item.area === 'untracked') {
        await vscode.workspace.fs.delete(item.uri, { recursive: true, useTrash: true })
        continue
      }
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
          type: 'gitActionResult', ok: true, action, repoRoot:message.repoRoot, workspaceId:message.workspaceId,
          message: String(result || 'Готово'), snapshot,
        })
      } catch (error) {
        this.postToolWindow('git', {
          type: 'gitActionResult', ok: false, action, repoRoot:message.repoRoot, workspaceId:message.workspaceId,
          message: formatVcsError('Git', error),
          snapshot: await this.toolWindowSnapshot('git').catch(() => undefined),
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
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
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
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
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
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'openServerTerminal': {
      try {
        await openSSHTerminalForProfile(this.service, String(message.id || ''))
      } catch (error) {
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
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
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
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
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
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
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
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
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
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
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
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
