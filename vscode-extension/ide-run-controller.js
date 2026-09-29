const { shellQuote } = require('./run-config-utils')

function createIdeRunController({
  vscode, parseJsonc, buildRunConfigurations, pickDefaultRunConfiguration,
  pointWorkspaceFolder,
}) {
  const runAnythingManifestCache = new Map()
  const RUN_KIND_TITLES = {
    launch: 'Отладчик',
    task: 'Задачи',
    npm: 'npm',
    go: 'Go',
    make: 'Make',
    cargo: 'Cargo',
    python: 'Python',
  }

  async function readRunAnythingManifest(uri, parse) {
    let stat
    try {
      stat = await vscode.workspace.fs.stat(uri)
    } catch {
      runAnythingManifestCache.delete(uri.toString())
      return undefined
    }
    const key = uri.toString()
    const cached = runAnythingManifestCache.get(key)
    if (cached && cached.mtime === stat.mtime) return cached.value
    try {
      const value = await parse(Buffer.from(await vscode.workspace.fs.readFile(uri)).toString('utf8'))
      runAnythingManifestCache.set(key, { mtime: stat.mtime, value })
      return value
    } catch {
      runAnythingManifestCache.delete(key)
      return undefined
    }
  }

  async function readFirstManifest(uris, parse) {
    for (const uri of uris) {
      const value = await readRunAnythingManifest(uri, parse)
      if (value !== undefined) return value
    }
    return undefined
  }

  async function discoverRunConfigurations() {
    const result = []
    for (const folder of vscode.workspace.workspaceFolders || []) {
      const manifests = {
        launch: await readRunAnythingManifest(vscode.Uri.joinPath(folder.uri, '.vscode', 'launch.json'), parseJsonc),
        tasks: await readRunAnythingManifest(vscode.Uri.joinPath(folder.uri, '.vscode', 'tasks.json'), parseJsonc),
        packageJson: await readRunAnythingManifest(vscode.Uri.joinPath(folder.uri, 'package.json'), parseJsonc),
        goMod: await readRunAnythingManifest(vscode.Uri.joinPath(folder.uri, 'go.mod'), raw => raw),
        makefile: await readFirstManifest([
          vscode.Uri.joinPath(folder.uri, 'Makefile'),
          vscode.Uri.joinPath(folder.uri, 'makefile'),
          vscode.Uri.joinPath(folder.uri, 'GNUmakefile'),
        ], raw => raw),
        cargo: await readRunAnythingManifest(vscode.Uri.joinPath(folder.uri, 'Cargo.toml'), raw => raw),
        pyproject: await readRunAnythingManifest(vscode.Uri.joinPath(folder.uri, 'pyproject.toml'), raw => raw),
      }
      result.push(...buildRunConfigurations(folder, manifests))
    }
    return result
  }

  function ensureRunTerminal(channel, cwd) {
    const name = `Point · ${channel || 'Запуск'}`.slice(0, 60)
    const existing = vscode.window.terminals.find(item => item.name === name)
    if (existing) {
      existing.show(true)
      return existing
    }
    const terminal = vscode.window.createTerminal({
      name,
      cwd: cwd || vscode.workspace.workspaceFolders?.[0]?.uri,
      location: vscode.TerminalLocation.Panel,
      iconPath: new vscode.ThemeIcon('play'),
      color: new vscode.ThemeColor('charts.orange'),
      isTransient: false,
    })
    terminal.show(true)
    return terminal
  }

  function workspaceFolderForConfig(config) {
    const folders = vscode.workspace.workspaceFolders || []
    return folders.find(item => item.uri.fsPath === config.cwd || item.name === config.folderName) || folders[0]
  }

  async function executeRunConfiguration(config, mode = 'run') {
    if (!config) return false
    if (config.kind === 'launch') {
      const folder = workspaceFolderForConfig(config)
      const started = await vscode.debug.startDebugging(folder, config.launchName, { noDebug: mode !== 'debug' })
      if (!started) {
        await vscode.window.showErrorMessage(`Не удалось запустить «${config.label}». Проверьте launch.json.`)
        return false
      }
      return true
    }
    if (config.kind === 'task') {
      const tasks = await vscode.tasks.fetchTasks()
      const task = tasks.find(item => item.name === config.taskLabel || item.definition?.label === config.taskLabel)
      if (task) {
        await vscode.tasks.executeTask(task)
        return true
      }
      await vscode.commands.executeCommand('workbench.action.tasks.runTask', config.taskLabel)
      return true
    }
    if (mode === 'debug') {
      await vscode.window.showInformationMessage(`«${config.label}» идёт в терминале. Отладчик доступен только для launch.json.`)
    }
    if (!config.command) return false
    ensureRunTerminal(config.channel || config.short, config.cwd).sendText(config.command, true)
    return true
  }

  function createRunConfigurationController(context, hooks = {}) {
    const status = vscode.window.createStatusBarItem('point.run', vscode.StatusBarAlignment.Left, 95)
    status.name = 'Запуск Point'
    status.command = 'localAgent.selectRunConfiguration'
    let configs = []
    let lastStarted
    let currentId = String(context.workspaceState.get('point.run.currentId') || '')
    const notify = () => { if (typeof hooks.onChange === 'function') hooks.onChange() }
    const paint = () => {
      if (!vscode.workspace.workspaceFolders?.length) {
        void vscode.commands.executeCommand('setContext', 'point.runConfiguration', '')
        status.hide()
        return
      }
      const current = configs.find(item => item.id === currentId)
      // Чип конфигурации в заголовке читает то же состояние через контекстный
      // ключ: часть заголовка живёт слоем ниже расширений и достучаться до
      // контроллера иначе не может.
      void vscode.commands.executeCommand('setContext', 'point.runConfiguration', current ? current.short : '')
      status.text = current ? `$(play) ${current.short}` : '$(play) Запуск'
      status.tooltip = current
        ? `${current.label}\nShift+F10 — запустить · Alt+Shift+F10 — выбрать · Shift+F9 — отладка`
        : 'Выберите конфигурацию запуска — Alt+Shift+F10'
      status.show()
    }
    const persist = async id => {
      currentId = id || ''
      await context.workspaceState.update('point.run.currentId', currentId)
      paint()
      notify()
    }
    const refresh = async () => {
      configs = await discoverRunConfigurations()
      if (currentId && !configs.some(item => item.id === currentId)) currentId = ''
      if (!currentId) {
        const next = pickDefaultRunConfiguration(configs)
        if (next) {
          currentId = next.id
          await context.workspaceState.update('point.run.currentId', currentId)
        }
      }
      paint()
      return configs
    }
    const select = async (opts = {}) => {
      const items = await refresh()
      const kinds = Array.isArray(opts.kinds) ? opts.kinds : undefined
      const visible = kinds ? items.filter(item => kinds.includes(item.kind)) : items
      if (!visible.length) {
        const choice = await vscode.window.showInformationMessage(
          'Конфигураций запуска нет. Добавьте launch.json, tasks.json, npm-скрипты или go.mod.',
          'Создать launch.json',
          'Изменить…',
        )
        if (choice === 'Создать launch.json') await vscode.commands.executeCommand('debug.addConfiguration')
        if (choice === 'Изменить…') await editRunConfigurations()
        return
      }
      const current = visible.find(item => item.id === currentId)
      const picks = []
      let lastKind = ''
      for (const item of visible) {
        if (item.kind !== lastKind) {
          picks.push({ label: RUN_KIND_TITLES[item.kind] || item.kind, kind: vscode.QuickPickItemKind.Separator })
          lastKind = item.kind
        }
        picks.push({
          label: `${item.id === currentId ? '$(check) ' : '$(play) '}${item.label}`,
          description: item.folderName,
          detail: item.detail,
          config: item,
        })
      }
      picks.push({ label: 'Настройка', kind: vscode.QuickPickItemKind.Separator })
      picks.push({ label: '$(gear) Изменить конфигурации…', edit: true })
      const selected = await vscode.window.showQuickPick(picks, {
        title: 'Конфигурации запуска',
        placeHolder: current ? `Текущая: ${current.label}` : 'Цель для Shift+F10',
        matchOnDescription: true,
        matchOnDetail: true,
      })
      if (!selected) return
      if (selected.edit) {
        await editRunConfigurations()
        return
      }
      await persist(selected.config.id)
      if (opts.runAfter !== false) {
        lastStarted = selected.config
        notify()
        await executeRunConfiguration(selected.config, opts.mode || 'run')
      }
    }
    const run = async (mode = 'run') => {
      await refresh()
      const current = configs.find(item => item.id === currentId)
      if (!current) return select({ mode, runAfter: true })
      lastStarted = current
      notify()
      if (mode === 'debug' && current.kind !== 'launch') {
        const launch = configs.filter(item => item.kind === 'launch')
        if (!launch.length) {
          await vscode.window.showInformationMessage('Для отладки нужна конфигурация launch.json. Запускаю текущую цель в терминале.')
          return executeRunConfiguration(current, 'run')
        }
        if (launch.length === 1) {
          await persist(launch[0].id)
          return executeRunConfiguration(launch[0], 'debug')
        }
        return select({ mode: 'debug', runAfter: true, kinds: ['launch'] })
      }
      return executeRunConfiguration(current, mode)
    }
    const anything = async () => {
      const items = [
        { label: '$(play) Запуск текущей', description: 'Shift+F10', command: 'localAgent.runWithoutDebug' },
        { label: '$(debug-alt) Отладка', description: 'Shift+F9', command: 'localAgent.startDebug' },
        { label: '$(list-unordered) Выбрать конфигурацию…', description: 'Alt+Shift+F10', command: 'localAgent.selectRunConfiguration' },
        { label: '$(tools) Задачи VS Code…', description: 'Tasks: Run Task', command: 'workbench.action.tasks.runTask' },
      ]
      let lastKind = ''
      for (const item of await refresh()) {
        if (item.kind !== lastKind) {
          items.push({ label: `${RUN_KIND_TITLES[item.kind] || item.kind} · ${item.folderName}`, kind: vscode.QuickPickItemKind.Separator })
          lastKind = item.kind
        }
        items.push({
          label: `${item.id === currentId ? '$(check) ' : '$(play) '}${item.label}`,
          description: item.folderName,
          detail: item.detail,
          config: item,
        })
      }
      const selected = await vscode.window.showQuickPick(items, {
        title: 'Point — Run Anything',
        placeHolder: 'launch · npm · go · make · cargo · tasks',
        matchOnDescription: true,
        matchOnDetail: true,
      })
      if (!selected) return
      if (selected.command) {
        await vscode.commands.executeCommand(selected.command)
        return
      }
      if (selected.config) {
        await persist(selected.config.id)
        lastStarted = selected.config
        notify()
        await executeRunConfiguration(selected.config, 'run')
      }
    }
    const runFile = async resource => {
      const uri = resource instanceof vscode.Uri
        ? resource
        : resource?.resourceUri instanceof vscode.Uri
          ? resource.resourceUri
          : vscode.window.activeTextEditor?.document.uri
      if (!uri || uri.scheme !== 'file') {
        await vscode.window.showInformationMessage('Откройте файл, чтобы запустить его здесь.')
        return
      }
      const ext = (uri.fsPath.match(/\.[^.\\/]+$/) || [''])[0].toLowerCase()
      const folder = vscode.workspace.getWorkspaceFolder(uri)
      const cwd = ext === '.rs' ? (folder?.uri.fsPath || uri.fsPath.replace(/[\\/][^\\/]+$/, '')) : uri.fsPath.replace(/[\\/][^\\/]+$/, '')
      const file = uri.fsPath.replace(/^.*[\\/]/, '')
      const command = {
        '.go': `go run ${shellQuote(file)}`,
        '.py': `python ${shellQuote(file)}`,
        '.js': `node ${shellQuote(file)}`,
        '.mjs': `node ${shellQuote(file)}`,
        '.cjs': `node ${shellQuote(file)}`,
        '.rs': 'cargo run',
      }[ext]
      if (!command) {
        await vscode.window.showInformationMessage('Для этого файла нет быстрого запуска. Выберите конфигурацию — Alt+Shift+F10.')
        return
      }
      lastStarted = { id: `file:${uri.fsPath}`, kind: 'file', label: `файл ${file}`, short: file, command, cwd, channel: /test|spec/.test(file) ? 'Тесты' : 'Запуск' }
      notify()
      ensureRunTerminal(lastStarted.channel, cwd).sendText(command, true)
    }
    const watcher = vscode.workspace.createFileSystemWatcher('**/{launch.json,tasks.json,package.json,go.mod,Makefile,makefile,GNUmakefile,Cargo.toml,pyproject.toml}')
    const refreshSoon = () => { void refresh() }
    watcher.onDidCreate(refreshSoon)
    watcher.onDidChange(refreshSoon)
    watcher.onDidDelete(refreshSoon)
    context.subscriptions.push(
      status,
      watcher,
      vscode.workspace.onDidChangeWorkspaceFolders(() => {
        runAnythingManifestCache.clear()
        void refresh()
      }),
    )
    void refresh()
    return {
      status,
      refresh,
      select,
      run,
      anything,
      runFile,
      current: () => configs.find(item => item.id === currentId) || lastStarted,
      lastStarted: () => lastStarted,
      configs: () => configs.slice(),
      dispose() {
        status.dispose()
        watcher.dispose()
      },
    }
  }

  async function editRunConfigurations() {
    const folder = vscode.workspace.getWorkspaceFolder(vscode.window.activeTextEditor?.document?.uri) || pointWorkspaceFolder()
    if (!folder) {
      await vscode.window.showInformationMessage('Откройте проект, чтобы настроить конфигурации запуска.')
      return
    }
    const selected = await vscode.window.showQuickPick([
      { label: '$(debug-alt) launch.json', description: 'Конфигурации отладчика', file: ['.vscode', 'launch.json'], add: true },
      { label: '$(tools) tasks.json', description: 'Задачи сборки и тестов', file: ['.vscode', 'tasks.json'] },
      { label: '$(package) package.json', description: 'npm-скрипты', file: ['package.json'] },
      { label: '$(add) Добавить конфигурацию отладки', addOnly: true },
    ], { title: 'Изменить конфигурации запуска', placeHolder: 'Файл цели Shift+F10' })
    if (!selected) return
    if (selected.addOnly) {
      await vscode.commands.executeCommand('debug.addConfiguration')
      return
    }
    const uri = vscode.Uri.joinPath(folder.uri, ...selected.file)
    try {
      await vscode.workspace.fs.stat(uri)
      await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(uri))
    } catch {
      if (selected.add) {
        await vscode.commands.executeCommand('debug.addConfiguration')
        return
      }
      await vscode.window.showInformationMessage(`Файла ${selected.file.join('/')} в проекте нет.`)
    }
  }

  return {
    readRunAnythingManifest, discoverRunConfigurations, ensureRunTerminal,
    workspaceFolderForConfig, executeRunConfiguration,
    createRunConfigurationController, editRunConfigurations,
  }
}

module.exports = { createIdeRunController }
