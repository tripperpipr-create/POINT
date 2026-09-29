const { createIdeRunController } = require('./ide-run-controller')
function createIdeActionController(dependencies) {
  const {
    vscode,
    path,
    fs,
    parseJsonc,
    makefileTargets,
    buildRunConfigurations,
    pickDefaultRunConfiguration,
    pointWorkspaceFolder,
    gitCwdForUri,
    resolveGitRoot,
    pathRelativeToRoot,
    formatVcsError,
    runGit,
    getActiveView,
    listRecentFileRecords,
  } = dependencies

  const LANGUAGE_SUPPORT = [
    { ids: ['go'], label: 'Go', extension: 'golang.Go' },
    { ids: ['python'], label: 'Python', extension: 'ms-python.python' },
    { ids: ['java'], label: 'Java', extension: 'redhat.java' },
    { ids: ['rust'], label: 'Rust', extension: 'rust-lang.rust-analyzer' },
    { ids: ['c', 'cpp', 'objective-c', 'objective-cpp'], label: 'C / C++', extension: 'llvm-vs-code-extensions.vscode-clangd' },
    { ids: ['csharp'], label: 'C#', extension: 'muhammad-sammy.csharp' },
    { ids: ['php'], label: 'PHP', extension: 'bmewburn.vscode-intelephense-client' },
    { ids: ['ruby'], label: 'Ruby', extension: 'shopify.ruby-lsp' },
    { ids: ['kotlin'], label: 'Kotlin', extension: 'fwcd.kotlin' },
    { ids: ['scala'], label: 'Scala', extension: 'scalameta.metals' },
    { ids: ['dart'], label: 'Dart / Flutter', extension: 'Dart-Code.dart-code' },
    { ids: ['vue'], label: 'Vue', extension: 'Vue.volar' },
    { ids: ['svelte'], label: 'Svelte', extension: 'svelte.svelte-vscode' },
    { ids: ['astro'], label: 'Astro', extension: 'astro-build.astro-vscode' },
    { ids: ['yaml'], label: 'YAML', extension: 'redhat.vscode-yaml' },
    { ids: ['shellscript'], label: 'Shell', extension: 'timonwong.shellcheck' },
    { ids: ['sql'], label: 'SQL', extension: 'mtxr.sqltools' },
  ]
  
  const BUILTIN_LANGUAGE_SUPPORT = new Set([
    'javascript', 'javascriptreact', 'typescript', 'typescriptreact',
    'json', 'jsonc', 'html', 'css', 'scss', 'less', 'markdown',
  ])
  
  const LANGUAGE_LABELS = {
    javascript: 'JavaScript',
    javascriptreact: 'JavaScript React',
    typescript: 'TypeScript',
    typescriptreact: 'TypeScript React',
    json: 'JSON',
    jsonc: 'JSON with Comments',
    html: 'HTML',
    css: 'CSS',
    scss: 'SCSS',
    less: 'Less',
    markdown: 'Markdown',
  }
  
  async function openRecentFileRecord(item) {
    if (!item?.uri) return false
    let uri
    try { uri = vscode.Uri.parse(item.uri) } catch { return false }
    const document = await vscode.workspace.openTextDocument(uri)
    const line = Math.min(Math.max(0, Number(item.line) || 0), Math.max(document.lineCount - 1, 0))
    const maxChar = document.lineAt(line).text.length
    const character = Math.min(Math.max(0, Number(item.character) || 0), maxChar)
    const position = new vscode.Position(line, character)
    await vscode.window.showTextDocument(document, {
      selection: new vscode.Range(position, position),
      preview: false,
    })
    return true
  }
  
  const POINT_ACTION_REGISTRY = [
    { label: 'Найти действие', description: 'Ctrl+Shift+A', detail: 'Find Action', icons: 'zap', command: 'workbench.action.showCommands', keywords: 'действие action find command' },
    { label: 'Перейти к файлу', description: 'Ctrl+Shift+N', detail: 'Navigate File', icons: 'file', command: 'localAgent.navigateFile', keywords: 'файл file navigate' },
    { label: 'Перейти к классу / символу', description: 'палитра', detail: 'Navigate Class', icons: 'symbol-class', command: 'localAgent.navigateClass', keywords: 'класс class символ symbol navigate' },
    { label: 'Структура файла', description: 'Ctrl+F12', detail: 'File Structure', icons: 'list-tree', command: 'localAgent.fileStructure', keywords: 'структура structure outline' },
    { label: 'Панель «Структура»', description: 'Alt+7', detail: 'Structure tool window', icons: 'symbol-class', command: 'localAgent.showOutline', keywords: 'outline структура' },
    { label: 'Навигационная строка', description: 'Alt+Home', detail: 'Focus Breadcrumbs', icons: 'breadcrumb', command: 'localAgent.focusBreadcrumbs', keywords: 'breadcrumbs навигация крошки navigation bar' },
    { label: 'Перейти к типу', description: 'Ctrl+Shift+B', detail: 'Go to Type Definition', icons: 'symbol-class', command: 'localAgent.goToTypeDefinition', keywords: 'тип type definition declaration' },
    { label: 'Рефакторинг…', description: 'Ctrl+Alt+Shift+T', detail: 'Refactor This', icons: 'tools', command: 'localAgent.refactorThis', keywords: 'рефакторинг refactor rename extract' },
    { label: 'Переименовать', description: 'Shift+F6', detail: 'Rename', icons: 'edit', command: 'localAgent.renameSymbol', keywords: 'переименовать rename' },
    { label: 'Оптимизировать импорты', description: 'Ctrl+Alt+O', detail: 'Optimize Imports', icons: 'organization', command: 'localAgent.optimizeImports', keywords: 'импорт import organize optimize' },
    { label: 'Реформатировать код', description: 'Ctrl+Alt+L', detail: 'Reformat Code', icons: 'symbol-ruler', command: 'localAgent.reformatCode', keywords: 'формат format reformat prettier' },
    { label: 'Недавние места', description: 'Ctrl+Shift+E', detail: 'Recent Locations', icons: 'history', command: 'localAgent.recentLocations', keywords: 'recent locations места правки history' },
    { label: 'Следующая ошибка', description: 'F2', detail: 'Next Highlighted Error', icons: 'error', command: 'localAgent.nextError', keywords: 'ошибка error marker next f2' },
    { label: 'Предыдущая ошибка', description: 'Shift+F2', detail: 'Previous Error', icons: 'error', command: 'localAgent.previousError', keywords: 'ошибка error marker previous' },
    { label: 'Сравнить с буфером', description: 'Compare', detail: 'Compare with Clipboard', icons: 'diff', command: 'localAgent.compareWithClipboard', keywords: 'сравнить clipboard diff буфер' },
    { label: 'Сравнить с файлом', description: 'Compare Files', detail: 'Diff two files', icons: 'diff', command: 'localAgent.compareWithFile', keywords: 'сравнить файл compare diff' },
    { label: 'Копировать ссылку', description: 'Ctrl+Alt+Shift+C', detail: 'Copy Reference', icons: 'copy', command: 'localAgent.copyReference', keywords: 'ссылка reference copy path строка' },
    { label: 'Черновик', description: 'Scratch', detail: 'New Scratch File', icons: 'note', command: 'localAgent.newScratch', keywords: 'черновик scratch временный' },
    { label: 'История файла / Timeline', description: 'Local History', detail: 'Show History', icons: 'history', command: 'localAgent.showFileHistory', keywords: 'история history timeline local' },
    { label: 'Режим без отвлечений (Zen)', description: 'Distraction Free', detail: 'Zen Mode', icons: 'screen-full', command: 'localAgent.toggleZenMode', keywords: 'zen distraction free фокус' },
    { label: 'Скрыть все окна инструментов', description: 'Ctrl+Shift+F12', detail: 'Hide All Tool Windows', icons: 'layout', command: 'localAgent.hideAllToolWindows', keywords: 'скрыть tool windows layout' },
    { label: 'Выделить следующее вхождение', description: 'Alt+J', detail: 'Select Next Occurrence', icons: 'selection', command: 'localAgent.selectNextOccurrence', keywords: 'multi cursor вхождение occurrence alt+j' },
    { label: 'Run Anything…', description: 'npm / go / make / cargo', detail: 'Run Anything', icons: 'rocket', command: 'localAgent.runAnything', keywords: 'run anything npm go task make cargo запуск' },
    { label: 'Конфигурации запуска', description: 'Alt+Shift+F10', detail: 'Run Configurations', icons: 'play', command: 'localAgent.selectRunConfiguration', keywords: 'запуск run debug конфигурация launch' },
    { label: 'Запустить файл', description: 'текущий файл', detail: 'Run File', icons: 'play', command: 'localAgent.runFile', keywords: 'run file запустить файл go python node' },
    { label: 'Изменить конфигурации запуска', description: 'launch.json', detail: 'Edit Configurations', icons: 'settings-gear', command: 'localAgent.editRunConfigurations', keywords: 'launch.json конфигурации run' },
    { label: 'Закладки', description: 'Ctrl+Shift+F11', detail: 'Bookmarks', icons: 'bookmark', command: 'localAgent.showBookmarks', keywords: 'закладка bookmark favorites' },
    { label: 'Коммит (Летопись)', description: 'Ctrl+K', detail: 'Commit', icons: 'git-commit', command: 'localAgent.vcsCommit', keywords: 'коммит commit vcs git летопись' },
    { label: 'Откатить изменения файла', description: 'Rollback', detail: 'VCS Rollback', icons: 'discard', command: 'localAgent.vcsRollback', keywords: 'откат rollback discard clean git' },
    { label: 'Показать diff файла', description: 'Show Diff', detail: 'Open Changes', icons: 'diff', command: 'localAgent.vcsShowDiff', keywords: 'diff изменения git' },
    { label: 'Push', description: 'VCS', detail: 'Git Push', icons: 'cloud-upload', command: 'localAgent.vcsPush', keywords: 'push git vcs' },
    { label: 'Pull', description: 'VCS', detail: 'Git Pull', icons: 'cloud-download', command: 'localAgent.vcsPull', keywords: 'pull git vcs' },
    { label: 'Аннотации / Blame', description: 'VCS', detail: 'Annotate', icons: 'git-compare', command: 'localAgent.gitAnnotate', keywords: 'annotate blame аннотация git' },
    { label: 'Изменения (changelist)', description: 'Alt+0', detail: 'Local Changes', icons: 'source-control', command: 'localAgent.vcsChanges', keywords: 'changelist изменения scm git' },
    { label: 'Подсказки параметров', description: 'Ctrl+P', detail: 'Parameter Info', icons: 'info', command: 'localAgent.parameterHints', keywords: 'параметр parameter hints signature' },
    { label: 'Показать использования (peek)', description: 'Ctrl+Alt+F7', detail: 'Show Usages', icons: 'references', command: 'localAgent.peekUsages', keywords: 'usages использования peek' },
    { label: 'Последнее место правки', description: 'Ctrl+Shift+Backspace', detail: 'Last Edit Location', icons: 'edit', command: 'localAgent.lastEditLocation', keywords: 'правка edit location recent' },
    { label: 'Спросить компаньона', description: 'Ctrl+Alt+;', detail: 'Ask Companion', icons: 'comment-unresolved', command: 'localAgent.askCompanion', keywords: 'компаньон companion ask помощник ide' },
    { label: 'Компаньон: входящие', description: 'сигналы IDE', detail: 'Companion inbox', icons: 'inbox', command: 'localAgent.companionInbox', keywords: 'компаньон inbox сигналы intervention' },
    { label: 'Компаньон: подготовить исправление', description: 'Prepare fix', detail: 'Ask Companion to prepare a fix', icons: 'wrench', command: 'localAgent.askCompanionFix', keywords: 'исправить fix companion quest' },
    { label: 'Компаньон: разобрать Problems', description: 'ошибки редактора', detail: 'Ask Companion about Problems', icons: 'warning', command: 'localAgent.askCompanionAboutProblems', keywords: 'problems диагностика ошибки companion' },
    { label: 'Компаньон: разобрать терминал', description: 'Analyze failure', detail: 'Ask Companion about terminal', icons: 'terminal', command: 'localAgent.askCompanionAboutTerminal', keywords: 'терминал terminal failure companion' },
    { label: 'Компаньон: разобрать diff', description: 'SCM', detail: 'Ask Companion about diff', icons: 'diff', command: 'localAgent.askCompanionAboutDiff', keywords: 'diff scm git companion изменения' },
    { label: 'Компаньон: разобрать запуск', description: 'Shift+F10', detail: 'Ask Companion about run', icons: 'play', command: 'localAgent.askCompanionAboutRun', keywords: 'запуск run companion' },
    { label: 'Компаньон: разобрать отладку', description: 'debug', detail: 'Ask Companion about debug', icons: 'debug-alt', command: 'localAgent.askCompanionAboutDebug', keywords: 'отладка debug companion' },
    { label: 'Компаньон: разобрать blame', description: 'git blame', detail: 'Ask Companion about blame', icons: 'git-compare', command: 'localAgent.askCompanionAboutBlame', keywords: 'blame annotate companion git' },
    { label: 'Компаньон: разобрать историю', description: 'git log', detail: 'Ask Companion about file history', icons: 'history', command: 'localAgent.askCompanionAboutHistory', keywords: 'история history companion git' },
    { label: 'Открыть Гильдию', description: 'Ctrl+Alt+I', detail: 'Quest board', icons: 'sparkle', command: 'localAgent.open', keywords: 'гильдия guild хаб hub квест агент' },
    { label: 'Статистика проекта', description: 'токены и бюджет', detail: 'Statistics', icons: 'graph', command: 'localAgent.openStatistics', keywords: 'статистика statistics usage бюджет' },
    { label: 'Docker', description: 'контейнеры и образы', detail: 'Docker', icons: 'server', command: 'localAgent.openDocker', keywords: 'docker контейнер compose образ' },
    { label: 'Базы данных', description: 'SQL', detail: 'Databases', icons: 'database', command: 'localAgent.openDatabases', keywords: 'база sql postgres sqlite mysql database' },
    { label: 'Подключиться к серверу', description: 'SSH', detail: 'Профили, терминал, проверка', icons: 'remote', command: 'localAgent.connectServer', keywords: 'ssh сервер remote подключение terminal' },
  ]
  
  function matchSearchEverywhereActions(query) {
    const needle = String(query || '').trim().toLocaleLowerCase()
    return POINT_ACTION_REGISTRY
      .map(action => {
        const haystack = `${action.label} ${action.description} ${action.detail} ${action.keywords}`
        return { action, score: needle ? fuzzyScore(haystack, needle) : 3 }
      })
      .filter(item => Number.isFinite(item.score))
      .sort((left, right) => left.score - right.score)
      .slice(0, 24)
      .map(({ action }) => ({
        label: `$(${action.icons}) ${action.label}`,
        description: action.description,
        detail: action.detail,
        alwaysShow: true,
        target: { type: 'command', command: action.command },
      }))
  }
  
  const BOOKMARKS_STATE_KEY = 'point.bookmarks'
  
  function loadBookmarks(context) {
    const raw = context.workspaceState.get(BOOKMARKS_STATE_KEY, [])
    if (!Array.isArray(raw)) return []
    return raw.filter(item => item && typeof item.uri === 'string' && Number.isInteger(item.line) && item.line >= 0)
  }
  
  function saveBookmarks(context, bookmarks) {
    return context.workspaceState.update(BOOKMARKS_STATE_KEY, bookmarks)
  }
  
  function createBookmarkController(context) {
    const decorationType = vscode.window.createTextEditorDecorationType({
      isWholeLine: true,
      overviewRulerLane: vscode.OverviewRulerLane.Left,
      overviewRulerColor: '#C9A227',
      dark: { backgroundColor: '#C9A22722' },
      light: { backgroundColor: '#C9A22733' },
    })
    let bookmarks = loadBookmarks(context)
  
    const refreshEditor = editor => {
      if (!editor) return
      const uri = editor.document.uri.toString()
      const ranges = bookmarks
        .filter(item => item.uri === uri)
        .map(item => {
          const line = Math.min(item.line, Math.max(editor.document.lineCount - 1, 0))
          return editor.document.lineAt(line).range
        })
      editor.setDecorations(decorationType, ranges)
    }
  
    const refreshAll = () => {
      for (const editor of vscode.window.visibleTextEditors) refreshEditor(editor)
    }
  
    const persist = async () => {
      await saveBookmarks(context, bookmarks)
      refreshAll()
    }
  
    const revealBookmark = async item => {
      if (!item) return
      const uri = vscode.Uri.parse(item.uri)
      const document = await vscode.workspace.openTextDocument(uri)
      const line = Math.min(item.line, Math.max(document.lineCount - 1, 0))
      const range = document.lineAt(line).range
      await vscode.window.showTextDocument(document, { selection: range, preview: false })
    }
  
    const toggle = async () => {
      const editor = vscode.window.activeTextEditor
      if (!editor) {
        await vscode.window.showInformationMessage('Откройте файл, чтобы поставить закладку.')
        return
      }
      const uri = editor.document.uri.toString()
      const line = editor.selection.active.line
      const existing = bookmarks.findIndex(item => item.uri === uri && item.line === line)
      if (existing >= 0) {
        bookmarks.splice(existing, 1)
        await persist()
        return
      }
      bookmarks.push({
        uri,
        line,
        label: editor.document.lineAt(line).text.trim().slice(0, 120) || `строка ${line + 1}`,
        relative: vscode.workspace.asRelativePath(editor.document.uri, false),
      })
      bookmarks.sort((left, right) => String(left.relative || '').localeCompare(String(right.relative || '')) || left.line - right.line)
      await persist()
    }
  
    const show = async () => {
      if (!bookmarks.length) {
        await vscode.window.showInformationMessage('Закладок пока нет. Поставьте через Ctrl+F11.')
        return
      }
      const selected = await vscode.window.showQuickPick(
        bookmarks.map((item, index) => ({
          label: `$(bookmark) ${path.basename(vscode.Uri.parse(item.uri).fsPath || item.relative || '')}`,
          description: `:${item.line + 1}`,
          detail: item.label || item.relative,
          index,
        })),
        { title: 'Point — закладки (как Favorites / Bookmarks)', placeHolder: 'Перейти к закладке', matchOnDescription: true, matchOnDetail: true },
      )
      if (selected == null) return
      await revealBookmark(bookmarks[selected.index])
    }
  
    const navigate = async direction => {
      if (!bookmarks.length) {
        await vscode.window.showInformationMessage('Закладок пока нет. Поставьте через Ctrl+F11.')
        return
      }
      const editor = vscode.window.activeTextEditor
      const currentUri = editor?.document.uri.toString()
      const currentLine = editor?.selection.active.line ?? -1
      let index = bookmarks.findIndex(item => item.uri === currentUri && item.line === currentLine)
      if (index < 0) index = direction > 0 ? -1 : 0
      const next = bookmarks[(index + direction + bookmarks.length * 2) % bookmarks.length]
      await revealBookmark(next)
    }
  
    refreshAll()
    context.subscriptions.push(
      decorationType,
      vscode.window.onDidChangeActiveTextEditor(refreshEditor),
      vscode.window.onDidChangeVisibleTextEditors(refreshAll),
      vscode.workspace.onDidChangeTextDocument(event => {
        const uri = event.document.uri.toString()
        if (!bookmarks.some(item => item.uri === uri)) return
        refreshEditor(vscode.window.visibleTextEditors.find(ed => ed.document.uri.toString() === uri))
      }),
    )
  
    return {
      toggle,
      show,
      next: () => navigate(1),
      previous: () => navigate(-1),
    }
  }
  
  async function refactorThis() {
    const selected = await vscode.window.showQuickPick([
      { label: '$(edit) Переименовать…', description: 'Shift+F6 · Rename', run: async () => renameSymbol() },
      { label: '$(tools) Рефакторинг…', description: 'меню Extract / Inline / Move', run: async () => vscode.commands.executeCommand('editor.action.refactor') },
      { label: '$(symbol-method) Извлечь…', description: 'Extract Method / Variable / Constant', run: async () => vscode.commands.executeCommand('editor.action.codeAction', { kind: 'refactor.extract', apply: 'never' }) },
      { label: '$(arrow-both) Встроить…', description: 'Inline', run: async () => vscode.commands.executeCommand('editor.action.codeAction', { kind: 'refactor.inline', apply: 'never' }) },
      { label: '$(move) Переместить…', description: 'Move', run: async () => vscode.commands.executeCommand('editor.action.codeAction', { kind: 'refactor.move', apply: 'never' }) },
      { label: '$(symbol-ruler) Изменить / rewrite…', description: 'Rewrite', run: async () => vscode.commands.executeCommand('editor.action.codeAction', { kind: 'refactor.rewrite', apply: 'never' }) },
      { label: '$(lightbulb) Quick Fix', description: 'Alt+Enter', run: async () => vscode.commands.executeCommand('editor.action.quickFix') },
      { label: '$(organization) Оптимизировать импорты', description: 'Ctrl+Alt+O', run: async () => optimizeImports() },
      { label: '$(symbol-ruler) Реформатировать', description: 'Ctrl+Alt+L · документ / выделение', run: async () => reformatCode() },
    ], {
      title: 'Point — Refactor This (как Ctrl+Alt+Shift+T)',
      placeHolder: 'Переименование, извлечение, встраивание…',
      matchOnDescription: true,
    })
    if (selected?.run) await selected.run()
  }
  
  async function renameSymbol() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл и поставьте курсор на символ для переименования (Shift+F6).')
      return
    }
    if (editor.document.uri.scheme === 'output' || editor.document.uri.scheme === 'log') {
      await vscode.window.showInformationMessage('Переименование недоступно в этом редакторе.')
      return
    }
    try {
      await vscode.commands.executeCommand('vscode.prepareRename', editor.document.uri, editor.selection.active)
    } catch (error) {
      const detail = error instanceof Error && error.message ? error.message : 'языковой сервер не поддерживает Rename здесь'
      const choice = await vscode.window.showWarningMessage(
        `Rename через LS недоступен (${detail}). Чем заменить?`,
        'Заменить в файле',
        'Заменить в проекте',
      )
      if (choice === 'Заменить в файле') await vscode.commands.executeCommand('editor.action.startFindReplaceAction')
      else if (choice === 'Заменить в проекте') await vscode.commands.executeCommand('workbench.action.replaceInFiles')
      return
    }
    await vscode.commands.executeCommand('editor.action.rename')
  }
  
  async function optimizeImports() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл, чтобы оптимизировать импорты (Ctrl+Alt+O).')
      return
    }
    try {
      await vscode.commands.executeCommand('editor.action.organizeImports')
    } catch {
      await vscode.window.showInformationMessage('Organize Imports недоступен для этого языка — проверьте Language Center.')
    }
  }
  
  async function reformatCode() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл для реформатирования (Ctrl+Alt+L).')
      return
    }
    if (editor.document.isUntitled && !editor.document.getText().trim()) {
      await vscode.window.showInformationMessage('Нечего форматировать.')
      return
    }
    const command = editor.selection.isEmpty ? 'editor.action.formatDocument' : 'editor.action.formatSelection'
    try {
      await vscode.commands.executeCommand(command)
    } catch {
      await vscode.window.showInformationMessage('Форматирование недоступно — нет formatter для этого языка.')
    }
  }
  
  async function recentLocations() {
    const remembered = listRecentFileRecords().slice(0, 16).map(item => {
      let uri
      try { uri = vscode.Uri.parse(item.uri) } catch { return undefined }
      const line = Number.isInteger(item.line) ? item.line + 1 : 0
      return {
        label: `$(history) ${path.basename(uri.fsPath || item.path || '')}`,
        description: line > 0 ? `${item.path || uri.fsPath}:${line}` : (item.path || uri.fsPath),
        detail: line > 0 ? `Недавний · строка ${line}` : 'Недавний файл',
        record: item,
      }
    }).filter(Boolean)
    const jumps = [
      { label: '$(edit) Последнее место правки', description: 'Ctrl+Shift+Backspace', command: 'workbench.action.navigateToLastEditLocation' },
      { label: '$(arrow-left) Предыдущее место правки', description: 'Edit Locations ←', command: 'workbench.action.navigatePreviousInEditLocations' },
      { label: '$(arrow-right) Следующее место правки', description: 'Edit Locations →', command: 'workbench.action.navigateForwardInEditLocations' },
      { label: '$(history) Недавние редакторы IDE', description: 'встроенный список', command: 'workbench.action.quickOpenRecent' },
      { label: '$(files) История вкладок', description: 'Previous Editor from History', command: 'workbench.action.openPreviousEditorFromHistory' },
      { label: '$(breadcrumb) Навигационная строка', description: 'Alt+Home', command: 'localAgent.focusBreadcrumbs' },
    ]
    const items = []
    if (remembered.length) items.push({ label: 'Недавние файлы', kind: vscode.QuickPickItemKind.Separator }, ...remembered)
    items.push({ label: 'Переходы', kind: vscode.QuickPickItemKind.Separator }, ...jumps)
    const selected = await vscode.window.showQuickPick(items, {
      title: 'Point — недавние места (как Recent Files / Locations)',
      placeHolder: 'Закрытые файлы с позицией курсора, правки, вкладки…',
      matchOnDescription: true,
    })
    if (selected?.record) {
      await openRecentFileRecord(selected.record)
      return
    }
    if (selected?.command) await vscode.commands.executeCommand(selected.command)
  }
  
  async function nextError() {
    try {
      await vscode.commands.executeCommand('editor.action.marker.nextInFiles')
    } catch {
      await vscode.commands.executeCommand('editor.action.marker.next')
    }
  }
  
  async function previousError() {
    try {
      await vscode.commands.executeCommand('editor.action.marker.prevInFiles')
    } catch {
      await vscode.commands.executeCommand('editor.action.marker.prev')
    }
  }
  
  async function compareWithClipboard() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл для сравнения с буфером обмена.')
      return
    }
    const clipboard = await vscode.env.clipboard.readText()
    if (!clipboard) {
      await vscode.window.showInformationMessage('Буфер обмена пуст.')
      return
    }
    const left = await vscode.workspace.openTextDocument({
      content: clipboard,
      language: editor.document.languageId,
    })
    const title = `Буфер ↔ ${path.basename(editor.document.fileName || editor.document.uri.path || 'файл')}`
    await vscode.commands.executeCommand('vscode.diff', left.uri, editor.document.uri, title)
  }
  
  async function showFileHistory() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл, чтобы показать историю.')
      return
    }
    const selected = await vscode.window.showQuickPick([
      { label: '$(history) Timeline / Local History', action: 'timeline' },
      { label: '$(diff) Сравнить с сохранённым', action: 'saved' },
      { label: '$(git-compare) Сравнить с HEAD', action: 'head' },
      { label: '$(comment-unresolved) Компаньон: разобрать историю', action: 'companion-history' },
      { label: '$(git-commit) Компаньон: разобрать blame', action: 'companion-blame' },
      { label: '$(edit) Компаньон: разобрать несохранённое', action: 'companion-saved' },
    ], { title: 'История файла', placeHolder: 'Timeline, diff или компаньон — без автозапуска квеста' })
    if (!selected) return
    if (selected.action === 'timeline') {
      try { await vscode.commands.executeCommand('workbench.view.extension.timeline') } catch { /* older cores */ }
      try { await vscode.commands.executeCommand('timeline.focus') } catch {
        await vscode.window.showInformationMessage('Панель Timeline недоступна в этой сборке.')
      }
      return
    }
    if (selected.action === 'saved') {
      try { await vscode.commands.executeCommand('workbench.files.action.compareWithSaved') } catch {
        await vscode.window.showInformationMessage('Сравнение с сохранённым недоступно.')
      }
      return
    }
    if (selected.action === 'head') {
      try { await vscode.commands.executeCommand('git.openChange') } catch {
        await vscode.window.showInformationMessage('Diff с HEAD недоступен — файл не в Git?')
      }
      return
    }
    if (selected.action === 'companion-history') return vscode.commands.executeCommand('localAgent.askCompanionAboutHistory')
    if (selected.action === 'companion-blame') return vscode.commands.executeCommand('localAgent.askCompanionAboutBlame')
    if (selected.action === 'companion-saved') return vscode.commands.executeCommand('localAgent.askCompanionAboutSaved')
  }
  
  async function hideAllToolWindows() {
    // The native shell snapshots actual visibility and restores it on repeat.
    if ((await vscode.commands.getCommands(true)).includes('point.toggleToolWindows')) {
      return vscode.commands.executeCommand('point.toggleToolWindows')
    }
    for (const command of [
      'workbench.action.closeSidebar',
      'workbench.action.closePanel',
      'workbench.action.closeAuxiliaryBar',
    ]) {
      try { await vscode.commands.executeCommand(command) } catch { /* ignore */ }
    }
    try {
      await vscode.commands.executeCommand('workbench.action.maximizeEditorHideSidebar')
    } catch {
      try { await vscode.commands.executeCommand('workbench.action.toggleMaximizeEditorGroup') } catch { /* ignore */ }
    }
  }
  
  async function selectNextOccurrence() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл. Alt+J — следующее вхождение; Ctrl+D — дублировать строку (как в JetBrains). Alt+Click — доп. курсор.')
      return
    }
    await vscode.commands.executeCommand('editor.action.addSelectionToNextFindMatch')
  }
  
  const {
    readRunAnythingManifest, discoverRunConfigurations, ensureRunTerminal,
    workspaceFolderForConfig, executeRunConfiguration,
    createRunConfigurationController, editRunConfigurations,
  } = createIdeRunController({
    vscode, parseJsonc, buildRunConfigurations, pickDefaultRunConfiguration,
    pointWorkspaceFolder,
  })

  async function vcsRollback() {
    try {
      await vscode.commands.executeCommand('git.clean')
    } catch (error) {
      await vscode.window.showWarningMessage(formatVcsError('Откат (Discard) недоступен', error))
    }
  }
  
  async function vcsShowDiff() {
    try {
      await vscode.commands.executeCommand('git.openChange')
    } catch (firstError) {
      try {
        await vscode.commands.executeCommand('git.openAllChanges')
      } catch (error) {
        await vscode.window.showWarningMessage(formatVcsError('Diff недоступен', error || firstError))
      }
    }
  }
  
  async function vcsPush() {
    try {
      await vscode.commands.executeCommand('git.push')
    } catch (error) {
      await vscode.window.showWarningMessage(`${formatVcsError('Push недоступен', error)}. Проверьте удалённый репозиторий в «Летописи» (SCM). Force push из агента отключён.`)
    }
  }
  
  async function vcsPull() {
    try {
      await vscode.commands.executeCommand('git.pull')
    } catch (error) {
      await vscode.window.showWarningMessage(`${formatVcsError('Pull недоступен', error)}. Проверьте удалённый репозиторий в «Летописи» (SCM).`)
    }
  }
  
  // Ctrl+K и Alt+0 ведут в Git-панель Point, а не в чужой SCM-виджет: коммит
  // собирается там же, где человек разложил файлы по папкам. Если панели почему-то
  // нет, остаётся штатный путь Code-OSS — он лучше, чем тупик.
  async function focusGitTool() {
    try {
      await vscode.commands.executeCommand('localAgent.gitTools.focus')
      return true
    } catch {
      try { await vscode.commands.executeCommand('workbench.view.scm') } catch { /* ignore */ }
      return false
    }
  }
  
  async function vcsCommit() {
    if (await focusGitTool()) {
      // Панель могла открыться только что и ещё не загрузиться: тогда просьбу
      // подхватит её первый запрос состояния.
      if (getActiveView()) getActiveView().pendingGitFocus = true
      getActiveView()?.postToolWindow?.('git', { type: 'gitFocusMessage' })
      return
    }
    try {
      await vscode.commands.executeCommand('git.commit')
    } catch (error) {
      await vscode.window.showWarningMessage(`${formatVcsError('Коммит недоступен', error)}. Откройте панель «Летопись» (SCM) или Alt+9 для чтения истории.`)
    }
  }
  
  async function vcsChanges() {
    await focusGitTool()
  }
  
  async function gitAnnotate() {
    const candidates = [
      'gitlens.toggleFileBlame',
      'gitlens.toggleLineBlame',
      'git.toggleEditorBlame',
      'git.viewBlame',
      'editor.action.toggleInlineBlame',
    ]
    for (const command of candidates) {
      try {
        await vscode.commands.executeCommand(command)
        return
      } catch { /* try next */ }
    }
    const editor = vscode.window.activeTextEditor
    if (!editor || editor.document.uri.scheme !== 'file') {
      await vscode.window.showInformationMessage('Откройте файл в Git, чтобы показать blame.')
      return
    }
    const cwd = gitCwdForUri(editor.document.uri)
    const gitRoot = await resolveGitRoot(cwd)
    const relative = pathRelativeToRoot(editor.document.uri, gitRoot) || vscode.workspace.asRelativePath(editor.document.uri, false).replace(/\\/g, '/')
    const line = editor.selection.active.line + 1
    try {
      const blame = String(await runGit(gitRoot || cwd, ['blame', '-w', '-L', `${line},${line}`, '--', relative])).trim()
      const choice = await vscode.window.showInformationMessage(
        blame ? `Blame L${line}: ${blame.slice(0, 180)}` : `Нет blame для ${relative}:${line}`,
        'Разобрать с компаньоном',
        'Timeline',
      )
      if (choice === 'Разобрать с компаньоном') return vscode.commands.executeCommand('localAgent.askCompanionAboutBlame')
      if (choice === 'Timeline') return vscode.commands.executeCommand('timeline.focus')
    } catch (error) {
      const choice = await vscode.window.showInformationMessage(
        formatVcsError('Blame недоступен', error),
        'Разобрать с компаньоном',
      )
      if (choice === 'Разобрать с компаньоном') return vscode.commands.executeCommand('localAgent.askCompanionAboutBlame')
    }
  }
  
  async function peekUsages() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл и поставьте курсор на символ.')
      return
    }
    try {
      await vscode.commands.executeCommand('editor.action.referenceSearch.trigger')
    } catch {
      await findSymbolLocations('vscode.executeReferenceProvider', 'Использования', 'usages')
    }
  }
  
  async function parameterHints() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Поставьте курсор внутри вызова функции — Ctrl+P покажет параметры.')
      return
    }
    await vscode.commands.executeCommand('editor.action.triggerParameterHints')
  }

  return {
    LANGUAGE_SUPPORT,
    BUILTIN_LANGUAGE_SUPPORT,
    LANGUAGE_LABELS,
    openRecentFileRecord,
    POINT_ACTION_REGISTRY,
    matchSearchEverywhereActions,
    loadBookmarks,
    saveBookmarks,
    createBookmarkController,
    refactorThis,
    renameSymbol,
    optimizeImports,
    reformatCode,
    recentLocations,
    nextError,
    previousError,
    compareWithClipboard,
    showFileHistory,
    hideAllToolWindows,
    selectNextOccurrence,
    readRunAnythingManifest,
    discoverRunConfigurations,
    ensureRunTerminal,
    workspaceFolderForConfig,
    executeRunConfiguration,
    createRunConfigurationController,
    vcsRollback,
    vcsShowDiff,
    vcsPush,
    vcsPull,
    editRunConfigurations,
    focusGitTool,
    vcsCommit,
    vcsChanges,
    gitAnnotate,
    peekUsages,
    parameterHints,
  }
}

module.exports = { createIdeActionController }
