// Консольные каналы, терминалы SSH и поиск по папке.
//
// Семейство держалось в `extension.js` между наблюдениями IDE и `activate`
// и занимало 238 строк на три темы сразу: вкладки консоли Чертога, работа с
// удалённой машиной по SSH и «Найти в папке». Общего у них ровно одно — все
// три открывают человеку окно терминала или поиска, и все три обходятся без
// состояния расширения.
//
// Разбор путей удалённой машины остаётся в `ssh-utils.js`: он чистый и на нём
// держатся отдельные проверки. Отсюда он берётся напрямую, а не через слой.

const {
  normalizeSSHRemotePath,
  sshRemotePathParent,
  sshRemotePathJoin,
  sshRemotePickerEntries,
} = require('./ssh-utils')

function createConsoleSSH({ vscode, fs, os, path, pointWorkspaceFolder }) {
  function consoleChannelChrome(name = 'Квест') {
    if (name === 'Сборка') {
      return { iconPath: new vscode.ThemeIcon('tools'), color: new vscode.ThemeColor('charts.blue') }
    }
    if (name === 'Тесты') {
      return { iconPath: new vscode.ThemeIcon('beaker'), color: new vscode.ThemeColor('charts.green') }
    }
    return { iconPath: new vscode.ThemeIcon('terminal'), color: new vscode.ThemeColor('charts.orange') }
  }

  function createConsoleChannel(name = 'Квест', cwd) {
    const activeUri = vscode.window.activeTextEditor?.document?.uri
    const folder = (activeUri ? vscode.workspace.getWorkspaceFolder(activeUri) : undefined) || pointWorkspaceFolder()
    const chrome = consoleChannelChrome(name)
    // Code-OSS 1.124 tries to URI.revive an explicitly undefined cwd. This can
    // happen during the first cold keybinding before workspaceFolders arrives.
    // Always send a concrete cwd; a folder window still wins over the fallback.
    const terminalCwd = cwd || folder?.uri || os.homedir()
    // Editor location matches Alt+F12 smoke: console as an editor tab, not the bottom panel.
    return vscode.window.createTerminal({
      name: `Point · ${name}`,
      cwd: terminalCwd,
      location: vscode.TerminalLocation.Editor,
      iconPath: chrome.iconPath,
      color: chrome.color,
      isTransient: false,
    })
  }

  async function openConsoleChannel() {
    const channels = vscode.window.terminals.filter(terminal => terminal.name.startsWith('Point · '))
    let terminal = channels.at(-1)
    if (!terminal) {
      // A cold Alt+F12 may activate the extension host a fraction before the
      // folder event. Prefer the real project cwd without delaying warm opens.
      let folder = pointWorkspaceFolder()
      for (let attempt = 0; !folder && attempt < 20; attempt += 1) {
        await new Promise(resolve => setTimeout(resolve, 50))
        folder = pointWorkspaceFolder()
      }
      terminal = createConsoleChannel('Квест', folder?.uri)
    }
    terminal.show(true)
    return terminal
  }

  async function openSSHTerminalForProfile(service, id) {
    if (!id) throw new Error('Не выбран SSH-профиль')
    const payload = await service.request(`/api/servers/${encodeURIComponent(id)}/terminal`)
    const sshPath = String(payload?.sshPath || 'ssh')
    const args = Array.isArray(payload?.args) ? payload.args : []
    const label = String(payload?.label || 'SSH')
    const terminal = vscode.window.createTerminal({
      name: `Point · ${label}`,
      shellPath: sshPath,
      shellArgs: args,
      iconPath: new vscode.ThemeIcon('remote'),
      color: new vscode.ThemeColor('charts.blue'),
    })
    terminal.show(true)
    return terminal
  }

  async function sshProfilePassword(context, profile) {
    return profile?.secretRef ? await context.secrets.get(profile.secretRef) || '' : ''
  }

  async function requestSSHRemoteList(service, context, profile, remotePath) {
    const password = await sshProfilePassword(context, profile)
    return service.request(`/api/servers/${encodeURIComponent(profile.id)}/list`, {
      method: 'POST',
      body: JSON.stringify({ path: normalizeSSHRemotePath(remotePath), password }),
    })
  }

  async function openSSHRemotePreview(service, context, profile, remotePath) {
    const password = await sshProfilePassword(context, profile)
    const result = await service.request(`/api/servers/${encodeURIComponent(profile.id)}/read`, {
      method: 'POST',
      body: JSON.stringify({ path: remotePath, password }),
    })
    const document = await vscode.workspace.openTextDocument({ content: String(result?.content || '') })
    const extension = path.extname(remotePath).slice(1).toLowerCase()
    if (extension && typeof vscode.languages?.setTextDocumentLanguage === 'function') {
      try { await vscode.languages.setTextDocumentLanguage(document, extension) } catch { /* keep plain text */ }
    }
    await vscode.window.showTextDocument(document, { preview: true })
    if (result?.truncated) {
      void vscode.window.showWarningMessage(`SSH: показаны первые 64 КиБ файла ${remotePath}`)
    } else {
      void vscode.window.showInformationMessage(`SSH: открыт безопасный предпросмотр ${remotePath}`)
    }
    return result
  }

  async function browseSSHRemotePath(service, context, profile, initialPath) {
    if (!profile?.id) throw new Error('Не выбран SSH-профиль')
    let current = normalizeSSHRemotePath(initialPath || profile.defaultRemotePath || '~')
    while (true) {
      const listed = await requestSSHRemoteList(service, context, profile, current)
      current = normalizeSSHRemotePath(listed?.path || current)
      const parent = sshRemotePathParent(current)
      const actions = [
        ...(parent !== current ? [{ label: '$(arrow-left) ..', description: parent, action: 'parent' }] : []),
        { label: '$(copy) Скопировать текущий путь', description: current, action: 'copy' },
        { label: '$(terminal) Открыть SSH-терминал', description: profile.displayName || profile.host, action: 'terminal' },
      ]
      const remoteItems = sshRemotePickerEntries(listed?.entries)
      const items = [
        ...actions,
        ...(remoteItems.length ? [{ label: 'Содержимое', kind: vscode.QuickPickItemKind.Separator }, ...remoteItems] : []),
      ]
      const selected = await vscode.window.showQuickPick(items, {
        title: `SSH · ${profile.displayName || profile.host} · ${current}`,
        placeHolder: remoteItems.length ? 'Каталог — перейти, файл — открыть безопасный предпросмотр' : 'Каталог пуст',
      })
      if (!selected) return current
      if (selected.action === 'parent') {
        current = parent
        continue
      }
      if (selected.action === 'copy') {
        await vscode.env.clipboard.writeText(current)
        void vscode.window.showInformationMessage(`SSH: путь скопирован — ${current}`)
        continue
      }
      if (selected.action === 'terminal') {
        await openSSHTerminalForProfile(service, profile.id)
        return current
      }
      const selectedPath = sshRemotePathJoin(current, selected.remoteName)
      if (selected.directory) {
        current = selectedPath
        continue
      }
      await openSSHRemotePreview(service, context, profile, selectedPath)
      return selectedPath
    }
  }

  async function manageSSHServers(service, context, provider) {
    await service.start()
    const profiles = await service.request('/api/servers')
    const items = [
      { label: '$(add) Новый профиль…', action: 'create' },
      ...(Array.isArray(profiles) && profiles.length ? [{ label: 'Сохранённые', kind: vscode.QuickPickItemKind.Separator }] : []),
      ...(Array.isArray(profiles) ? profiles.map(item => ({
        label: `$(server) ${item.displayName || item.host}`,
        description: `${item.user}@${item.host}:${item.port || 22}`,
        detail: `${item.authMethod || 'agent'} · ${item.status || 'unknown'}`,
        profile: item,
      })) : []),
    ]
    const selected = await vscode.window.showQuickPick(items, {
      title: 'Point — подключение к серверу',
      placeHolder: 'Проверка, SSH-терминал или новый профиль',
    })
    if (!selected) return
    if (selected.action === 'create') {
      provider?.showWide?.('connections')
      return
    }
    const profile = selected.profile
    if (!profile?.id) return
    const action = await vscode.window.showQuickPick([
      { label: '$(terminal) Открыть SSH-терминал', action: 'terminal' },
      { label: '$(check) Проверить соединение', action: 'probe' },
      { label: '$(folder) Список удалённого пути', action: 'list' },
    ], { title: profile.displayName || profile.host })
    if (!action) return
    if (action.action === 'terminal') {
      await openSSHTerminalForProfile(service, profile.id)
      return
    }
    const password = profile.secretRef ? await context.secrets.get(profile.secretRef) || '' : ''
    if (action.action === 'probe') {
      const result = await service.request(`/api/servers/${encodeURIComponent(profile.id)}/probe`, {
        method: 'POST', body: JSON.stringify({ password }),
      })
      if (result?.ok) void vscode.window.showInformationMessage(`SSH: ${result.message || 'ок'}`)
      else void vscode.window.showWarningMessage(`SSH: ${result?.message || 'ошибка'}`)
      if (provider) {
        provider.patchBoot({ serverProfiles: await service.request('/api/servers') })
        provider.postState()
      }
      return
    }
    const path = await vscode.window.showInputBox({ title: 'Удалённый путь', value: profile.defaultRemotePath || '~' })
    if (path == null) return
    await browseSSHRemotePath(service, context, profile, path)
  }

  async function chooseConsoleChannel() {
    const channels = vscode.window.terminals.filter(terminal => terminal.name.startsWith('Point · '))
    const existing = channels.map(terminal => ({ label: `$(terminal) ${terminal.name}`, description: 'Открыт', terminal }))
    const selected = await vscode.window.showQuickPick([
      ...existing,
      ...(existing.length ? [{ label: 'Новый канал', kind: vscode.QuickPickItemKind.Separator }] : []),
      { label: '$(add) Point · Квест', description: 'Основная интерактивная консоль', create: 'Квест' },
      { label: '$(tools) Point · Сборка', description: 'Команды сборки проекта', create: 'Сборка' },
      { label: '$(beaker) Point · Тесты', description: 'Запуски тестов и диагностика', create: 'Тесты' },
    ], { title: 'Point — консольные каналы', placeHolder: 'Канал запускается только после выбора' })
    if (!selected) return
    const terminal = selected.terminal || createConsoleChannel(selected.create)
    terminal.show(true)
  }

  async function findInFolder(resource) {
    let uri = resource instanceof vscode.Uri ? resource : undefined
    if (!uri && vscode.window.activeTextEditor?.document?.uri?.scheme === 'file') {
      uri = vscode.Uri.joinPath(vscode.window.activeTextEditor.document.uri, '..')
    }
    if (!uri) {
      await vscode.commands.executeCommand('workbench.action.findInFiles')
      return
    }
    try {
      const stat = await vscode.workspace.fs.stat(uri)
      if (!(stat.type & vscode.FileType.Directory)) {
        uri = vscode.Uri.joinPath(uri, '..')
      }
    } catch { /* use uri as-is */ }
    const relative = vscode.workspace.asRelativePath(uri, false)
    const include = relative && relative !== uri.fsPath ? relative.replace(/\\/g, '/') : ''
    await vscode.commands.executeCommand('search.action.openEditor', {
      query: '',
      filesToInclude: include,
      showIncludesExcludes: true,
      location: 'reuse',
    })
  }

  function stripTerminalControlSequences(value) {
    return String(value || '')
      .replace(/\x1B\][^\x07]*(?:\x07|\x1B\\)/g, '')
      .replace(/\x1B\[[0-?]*[ -\/]*[@-~]/g, '')
      .replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/g, '')
  }

  return {
    consoleChannelChrome,
    createConsoleChannel,
    openConsoleChannel,
    openSSHTerminalForProfile,
    sshProfilePassword,
    requestSSHRemoteList,
    openSSHRemotePreview,
    browseSSHRemotePath,
    manageSSHServers,
    chooseConsoleChannel,
    findInFolder,
    stripTerminalControlSequences,
  }
}

module.exports = { createConsoleSSH }
