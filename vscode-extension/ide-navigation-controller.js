function createIdeNavigationController(dependencies) {
  const {
    vscode,
    path,
    fs,
    pointWorkspaceFolder,
    companionWorkspaceRelativePath,
    companionClosing,
    LANGUAGE_SUPPORT,
    BUILTIN_LANGUAGE_SUPPORT,
    LANGUAGE_LABELS,
    POINT_ACTION_REGISTRY,
    matchSearchEverywhereActions,
    openRecentFileRecord,
    fuzzyScore,
    symbolIcon,
    parseDocumentOutline,
    resolveOutlineSymbolAt,
    formatOutlineBreadcrumb,
    outlineKindIcon,
    mergeRecentFiles,
    formatCopyReference,
    parseSearchEverywhereQuery,
  } = dependencies

  function createWorkspaceFileCache() {
    let files
    let pending
    let generation = 0
    const excluded = '**/{.git,.hg,.svn,node_modules,vendor,dist,build,out,.cache,.gocache,.tmp,.idea,.pnpm,.next,target}/**'
    const sameUri = (left, right) => left.toString() === right.toString()
    return {
      async get() {
        if (files) return files
        if (!pending) {
          const requestGeneration = generation
          pending = vscode.workspace.findFiles('**/*', excluded, 2500)
          .catch(() => [])
          .then(result => {
            if (requestGeneration === generation) {
              files = result
              pending = undefined
            }
            return result
          })
        }
        return pending
      },
      apply(uri, kind) {
        if (!files || !uri) return
        if (kind === 'delete') {
          files = files.filter(item => !sameUri(item, uri))
          return
        }
        if (!files.some(item => sameUri(item, uri))) files = files.concat([uri])
      },
      invalidate() {
        generation += 1
        files = undefined
        pending = undefined
      },
    }
  }
  
  const workspaceFileCache = createWorkspaceFileCache()
  
  let queryPointIndexHits = async () => []
  const pointIndexQueryCache = { key: '', at: 0, hits: [] }
  
  function bindPointIndexSearch(fn) {
    queryPointIndexHits = typeof fn === 'function' ? fn : async () => []
    pointIndexQueryCache.key = ''
    pointIndexQueryCache.hits = []
  }
  
  let listRecentFileRecords = () => []
  function bindRecentFiles(fn) {
    listRecentFileRecords = typeof fn === 'function' ? fn : () => []
  }
  
  function collectOpenTabUris() {
    const ordered = []
    const seen = new Set()
    for (const group of vscode.window.tabGroups.all) {
      for (const tab of group.tabs) {
        const uri = tab.input?.uri
        if (!uri || seen.has(uri.toString())) continue
        seen.add(uri.toString())
        ordered.push(uri)
      }
    }
    return ordered
  }
  
  function createRecentFilesTracker(context) {
    let items = mergeRecentFiles([], Array.isArray(context.workspaceState.get('point.recentFiles')) ? context.workspaceState.get('point.recentFiles') : [], 40)
    let persistTimer
    const persist = () => {
      clearTimeout(persistTimer)
      persistTimer = setTimeout(() => {
        void context.workspaceState.update('point.recentFiles', items)
      }, 400)
    }
    const remember = (uri, position) => {
      if (!uri || uri.scheme !== 'file') return
      const line = Number.isInteger(position?.line) ? position.line : 0
      const character = Number.isInteger(position?.character) ? position.character : 0
      const key = uri.toString()
      const head = items[0]
      if (head?.uri === key && head.line === line && head.character === character) return
      items = mergeRecentFiles([{
        uri: key,
        path: vscode.workspace.asRelativePath(uri, false),
        line,
        character,
        at: Date.now(),
      }], items, 40)
      persist()
    }
    return {
      list: () => items.slice(),
      remember,
      uris: () => items.map(item => {
        try { return vscode.Uri.parse(item.uri) } catch { return undefined }
      }).filter(Boolean),
      dispose() { clearTimeout(persistTimer) },
    }
  }
  
  function createStructureStatus() {
    const item = vscode.window.createStatusBarItem('point.structure', vscode.StatusBarAlignment.Left, 9)
    item.name = 'Структура Point'
    item.command = 'localAgent.fileStructure'
    let timer
    const paint = () => {
      const editor = vscode.window.activeTextEditor
      if (!editor || (editor.document.uri.scheme !== 'file' && editor.document.uri.scheme !== 'untitled')) {
        item.hide()
        return
      }
      const outline = parseDocumentOutline(editor.document.getText(), editor.document.languageId)
      const chain = resolveOutlineSymbolAt(outline, editor.selection.active.line)
      const crumb = formatOutlineBreadcrumb(chain)
      if (!crumb) {
        item.hide()
        return
      }
      const short = crumb.length > 52 ? `…${crumb.slice(-50)}` : crumb
      const leaf = chain[chain.length - 1]
      item.text = `$(${outlineKindIcon(leaf?.kind)}) ${short}`
      item.tooltip = `Структура · ${crumb}\nCtrl+F12 — весь файл · Alt+7 — панель Outline`
      item.show()
    }
    const schedule = () => {
      clearTimeout(timer)
      timer = setTimeout(paint, 90)
    }
    const subscription = vscode.window.onDidChangeActiveTextEditor(schedule)
    const selectionSub = vscode.window.onDidChangeTextEditorSelection(event => {
      if (event.textEditor === vscode.window.activeTextEditor) schedule()
    })
    const docSub = vscode.workspace.onDidChangeTextDocument(event => {
      if (event.document === vscode.window.activeTextEditor?.document) schedule()
    })
    paint()
    return {
      item,
      paint,
      dispose() {
        clearTimeout(timer)
        subscription.dispose()
        selectionSub.dispose()
        docSub.dispose()
        item.dispose()
      },
    }
  }
  
  function createProblemsStatus() {
    const item = vscode.window.createStatusBarItem('point.problems', vscode.StatusBarAlignment.Right, 100)
    item.name = 'Проблемы Point'
    item.command = 'localAgent.nextError'
    const paint = () => {
      let errors = 0
      let warnings = 0
      for (const [, diagnostics] of vscode.languages.getDiagnostics()) {
        for (const diagnostic of diagnostics) {
          if (diagnostic.severity === vscode.DiagnosticSeverity.Error) errors += 1
          else if (diagnostic.severity === vscode.DiagnosticSeverity.Warning) warnings += 1
        }
      }
      if (!errors && !warnings) {
        item.hide()
        return
      }
      item.text = errors
        ? `$(error) ${errors}${warnings ? `  $(warning) ${warnings}` : ''}`
        : `$(warning) ${warnings}`
      item.tooltip = 'F2 — следующая ошибка · Shift+F2 — предыдущая'
      item.backgroundColor = errors ? new vscode.ThemeColor('statusBarItem.errorBackground') : undefined
      item.show()
    }
    const subscription = vscode.languages.onDidChangeDiagnostics(paint)
    paint()
    return { item, paint, dispose() { subscription.dispose(); item.dispose() } }
  }
  
  function flattenDocumentSymbols(symbols, indent = 0) {
    const out = []
    for (const symbol of symbols || []) {
      const range = symbol.selectionRange || symbol.range || symbol.location?.range
      const line = Number(range?.start?.line)
      out.push({
        name: symbol.name,
        kind: typeof symbol.kind === 'number' ? (vscode.SymbolKind[symbol.kind] || 'symbol') : String(symbol.kind || 'symbol'),
        line: Number.isFinite(line) ? line : 0,
        indent,
        icon: symbolIcon(symbol.kind),
      })
      if (symbol.children?.length) out.push(...flattenDocumentSymbols(symbol.children, indent + 1))
    }
    return out
  }
  
  async function fileStructure() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл, чтобы показать структуру (Ctrl+F12).')
      return
    }
    let symbols = []
    try {
      symbols = await vscode.commands.executeCommand('vscode.executeDocumentSymbolProvider', editor.document.uri) || []
    } catch {
      symbols = []
    }
    let items = flattenDocumentSymbols(Array.isArray(symbols) ? symbols : [])
    if (!items.length) {
      items = parseDocumentOutline(editor.document.getText(), editor.document.languageId).map(item => ({
        ...item,
        icon: outlineKindIcon(item.kind),
      }))
    }
    if (!items.length) {
      try {
        await vscode.commands.executeCommand('workbench.action.gotoSymbol')
      } catch {
        await vscode.window.showInformationMessage('В этом файле нет распознанной структуры.')
      }
      return
    }
    const picks = items.map(item => ({
      label: `${'  '.repeat(item.indent || 0)}$(${item.icon}) ${item.name}`,
      description: `:${item.line + 1}`,
      detail: item.kind,
      line: item.line,
    }))
    const picker = vscode.window.createQuickPick()
    picker.title = 'Point — структура файла (Ctrl+F12)'
    picker.placeholder = 'Функции, типы и заголовки текущего буфера'
    picker.matchOnDescription = true
    picker.matchOnDetail = true
    picker.items = picks
    const closest = picks.filter(item => item.line <= editor.selection.active.line).pop()
    if (closest) picker.activeItems = [closest]
    picker.show()
    await new Promise(resolve => {
      const accept = picker.onDidAccept(() => {
        const selected = picker.selectedItems[0]
        picker.hide()
        if (!selected || !Number.isInteger(selected.line) || editor.document.isClosed) return
        const line = Math.min(selected.line, Math.max(editor.document.lineCount - 1, 0))
        const range = editor.document.lineAt(line).range
        editor.selection = new vscode.Selection(range.start, range.start)
        editor.revealRange(range, vscode.TextEditorRevealType.InCenter)
      })
      picker.onDidHide(() => {
        accept.dispose()
        picker.dispose()
        resolve()
      })
    })
  }
  
  async function copyReference() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл, чтобы скопировать ссылку.')
      return
    }
    const text = formatCopyReference(
      vscode.workspace.asRelativePath(editor.document.uri, false),
      editor.selection.active.line + 1,
      editor.selection.active.character + 1,
      currentEditorWord(),
    )
    await vscode.env.clipboard.writeText(text)
    vscode.window.setStatusBarMessage(`Скопировано: ${text}`, 2500)
  }
  
  async function newScratchFile() {
    const languages = [
      { label: 'Текст', language: 'plaintext', ext: 'txt' },
      { label: 'Go', language: 'go', ext: 'go' },
      { label: 'JavaScript', language: 'javascript', ext: 'js' },
      { label: 'TypeScript', language: 'typescript', ext: 'ts' },
      { label: 'JSON', language: 'json', ext: 'json' },
      { label: 'Markdown', language: 'markdown', ext: 'md' },
      { label: 'Python', language: 'python', ext: 'py' },
    ]
    const picked = await vscode.window.showQuickPick(languages, {
      title: 'Черновик Point',
      placeHolder: 'Язык черновика — файл в .point/scratches, индекс его не берёт',
    })
    if (!picked) return
    const folder = vscode.workspace.getWorkspaceFolder(vscode.window.activeTextEditor?.document?.uri) || pointWorkspaceFolder()
    if (folder?.uri.scheme === 'file') {
      const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
      const dir = vscode.Uri.joinPath(folder.uri, '.point', 'scratches')
      await vscode.workspace.fs.createDirectory(dir)
      const uri = vscode.Uri.joinPath(dir, `scratch-${stamp}.${picked.ext}`)
      await vscode.workspace.fs.writeFile(uri, new Uint8Array())
      await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(uri))
      return
    }
    await vscode.window.showTextDocument(await vscode.workspace.openTextDocument({ language: picked.language, content: '' }))
  }
  
  async function compareWithFile(resource) {
    const left = resource instanceof vscode.Uri ? resource : vscode.window.activeTextEditor?.document.uri
    if (!left) {
      await vscode.window.showInformationMessage('Выберите файл для сравнения.')
      return
    }
    const files = await workspaceFileCache.get()
    const candidates = mergeRecentFiles(
      listRecentFileRecords(),
      files.map(uri => ({ uri: uri.toString(), path: vscode.workspace.asRelativePath(uri, false) })),
      80,
    ).filter(item => item.uri !== left.toString())
    const selected = await vscode.window.showQuickPick(candidates.map(item => ({
      label: path.basename(item.path || item.uri),
      description: item.path,
      uri: item.uri,
    })), {
      title: 'Сравнить с файлом',
      placeHolder: vscode.workspace.asRelativePath(left, false),
      matchOnDescription: true,
    })
    if (!selected) return
    await vscode.commands.executeCommand(
      'vscode.diff',
      left,
      vscode.Uri.parse(selected.uri),
      `${path.basename(left.fsPath || left.path)} ↔ ${selected.label}`,
    )
  }
  
  const pendingFormatAfterOrganize = new Set()
  let formatAfterSaveBusy = false
  
  async function collectSaveActionEdits(document, { format, organize }) {
    const edits = []
    if (organize) {
      try {
        const range = document.validateRange(new vscode.Range(0, 0, Math.max(document.lineCount - 1, 0), 0))
        const actions = await vscode.commands.executeCommand(
          'vscode.executeCodeActionProvider',
          document.uri,
          range,
          vscode.CodeActionKind.SourceOrganizeImports.value,
        )
        for (const action of actions || []) {
          if (!action.edit) continue
          for (const [uri, itemEdits] of action.edit.entries()) {
            if (uri.toString() === document.uri.toString()) edits.push(...itemEdits)
          }
        }
      } catch { /* no organize provider */ }
    }
    if (format) {
      if (edits.length) {
        pendingFormatAfterOrganize.add(document.uri.toString())
      } else {
        try {
          const formatted = await vscode.commands.executeCommand('vscode.executeFormatDocumentProvider', document.uri)
          if (Array.isArray(formatted)) edits.push(...formatted)
        } catch { /* no formatter */ }
      }
    }
    return edits
  }
  
  async function flushFormatAfterOrganize(document) {
    const key = document?.uri?.toString()
    if (!key || !pendingFormatAfterOrganize.has(key) || formatAfterSaveBusy) return
    pendingFormatAfterOrganize.delete(key)
    formatAfterSaveBusy = true
    try {
      await vscode.commands.executeCommand('editor.action.formatDocument')
      if (document.isDirty) await document.save()
    } catch { /* formatter unavailable */ } finally {
      formatAfterSaveBusy = false
    }
  }
  
  async function openPointIdeSettings() {
    const cfg = vscode.workspace.getConfiguration('localAgent')
    const editor = vscode.workspace.getConfiguration('editor')
    const crumbs = vscode.workspace.getConfiguration('breadcrumbs')
    const workbench = vscode.workspace.getConfiguration('workbench')
    const format = cfg.get('formatOnSave') === true
    const organize = cfg.get('organizeImportsOnSave') === true
    const sticky = editor.get('stickyScroll.enabled') === true
    const crumbsOn = crumbs.get('enabled') !== false
    const minimap = editor.get('minimap.enabled') === true
    const doubleClick = workbench.get('list.openMode') === 'doubleClick'
    const selected = await vscode.window.showQuickPick([
      { label: format ? '$(check) Форматировать при сохранении' : '$(circle-slash) Форматировать при сохранении', action: 'format' },
      { label: organize ? '$(check) Оптимизировать импорты при сохранении' : '$(circle-slash) Оптимизировать импорты при сохранении', action: 'organize' },
      { label: sticky ? '$(check) Липкая структура при прокрутке' : '$(circle-slash) Липкая структура при прокрутке', action: 'sticky' },
      { label: crumbsOn ? '$(check) Навигационная строка' : '$(circle-slash) Навигационная строка', action: 'crumbs' },
      { label: doubleClick ? '$(check) Открывать файл двойным щелчком' : '$(circle-slash) Открывать файл двойным щелчком', action: 'openMode' },
      { label: minimap ? '$(check) Мини-карта' : '$(circle-slash) Мини-карта', action: 'minimap' },
      { label: '$(settings-gear) Все настройки IDE…', action: 'all' },
    ], { title: 'Point — настройки редактора', placeHolder: 'Переключить поведение IDE' })
    if (!selected) return
    const target = vscode.ConfigurationTarget.Global
    if (selected.action === 'format') await cfg.update('formatOnSave', !format, target)
    else if (selected.action === 'organize') await cfg.update('organizeImportsOnSave', !organize, target)
    else if (selected.action === 'sticky') await editor.update('stickyScroll.enabled', !sticky, target)
    else if (selected.action === 'crumbs') await crumbs.update('enabled', !crumbsOn, target)
    else if (selected.action === 'openMode') await workbench.update('list.openMode', doubleClick ? 'singleClick' : 'doubleClick', target)
    else if (selected.action === 'minimap') await editor.update('minimap.enabled', !minimap, target)
    else await vscode.commands.executeCommand('workbench.action.openSettings')
  }
  
  function currentEditorWord() {
    const editor = vscode.window.activeTextEditor
    if (!editor) return ''
    const range = editor.document.getWordRangeAtPosition(editor.selection.active)
    return range ? editor.document.getText(range).trim() : ''
  }
  
  async function fetchPointIndexHits(query, limit = 40) {
    const q = String(query || '').trim()
    if (!q) return []
    const key = `${q}\0${limit}`
    if (pointIndexQueryCache.key === key && Date.now() - pointIndexQueryCache.at < 1500) {
      return pointIndexQueryCache.hits
    }
    try {
      const hits = await queryPointIndexHits(q, limit)
      const list = Array.isArray(hits) ? hits : []
      pointIndexQueryCache.key = key
      pointIndexQueryCache.at = Date.now()
      pointIndexQueryCache.hits = list
      return list
    } catch {
      return []
    }
  }
  
  function indexHitToPickerItem(hit) {
    const line = Number(hit.line) > 0 ? Number(hit.line) : 1
    const kind = hit.kind === 'symbol' ? 'Индекс · символ' : 'Индекс · фрагмент'
    return {
      label: `$(${hit.kind === 'symbol' ? 'symbol-method' : 'file-code'}) ${hit.name || path.basename(hit.path || '')}`,
      description: hit.path ? `${hit.path}:${line}` : '',
      detail: [kind, hit.language, hit.snippet].filter(Boolean).join(' · '),
      target: { type: 'index', path: hit.path, line },
    }
  }
  
  async function openIndexHit(hit) {
    const folder = pointWorkspaceFolder()
    if (!folder || !hit?.path) return false
    const uri = vscode.Uri.joinPath(folder.uri, String(hit.path).replace(/\\/g, '/'))
    const document = await vscode.workspace.openTextDocument(uri)
    const line = Math.max(0, (Number(hit.line) || 1) - 1)
    const position = new vscode.Position(Math.min(line, Math.max(document.lineCount - 1, 0)), 0)
    await vscode.window.showTextDocument(document, { selection: new vscode.Range(position, position), preview: false })
    return true
  }
  
  async function revealIndexHits(hits, title) {
    if (!hits.length) return false
    if (hits.length === 1) {
      await openIndexHit(hits[0])
      vscode.window.setStatusBarMessage('Переход по индексу Point', 2500)
      return true
    }
    const selected = await vscode.window.showQuickPick(hits.map(indexHitToPickerItem), {
      title: title || 'Индекс Point',
      placeHolder: 'Несколько совпадений в локальном индексе',
      matchOnDescription: true,
      matchOnDetail: true,
    })
    if (selected?.target) {
      await openIndexHit({ path: selected.target.path, line: selected.target.line })
      return true
    }
    return false
  }
  
  async function companionProblemsMessage() {
    const lines = []
    const push = (uri, diagnostics) => {
      const relative = companionWorkspaceRelativePath(uri)
      if (!relative) return false
      for (const item of diagnostics) {
        if (item.severity !== vscode.DiagnosticSeverity.Error && item.severity !== vscode.DiagnosticSeverity.Warning) continue
        lines.push(`${relative}:${item.range.start.line + 1} ${item.message}`)
        if (lines.length >= 24) return true
      }
      return false
    }
    const editor = vscode.window.activeTextEditor
    if (editor) push(editor.document.uri, vscode.languages.getDiagnostics(editor.document.uri))
    if (!lines.length) {
      for (const [uri, diagnostics] of vscode.languages.getDiagnostics()) {
        if (push(uri, diagnostics)) break
      }
    }
    if (!lines.length) {
      return `Какие ошибки сейчас в Problems? Если диагностика чистая — так и скажи. ${companionClosing('analyze')}`
    }
    return `Разбери текущие проблемы IDE и предложи проверяемый план.\n\n${lines.join('\n')}\n\n${companionClosing('analyze')}`
  }
  
  async function searchEverywhere(opts = {}) {
    const picker = vscode.window.createQuickPick()
    picker.title = 'Point — поиск везде (палитра)'
    picker.placeholder = 'Все · #символы · /файлы · @действия · пустой запрос — недавние и переходы'
    if (opts.initial) picker.value = String(opts.initial)
    picker.matchOnDescription = true
    picker.matchOnDetail = true
    picker.keepScrollPosition = true
    let updateId = 0
    let timer
  
    const update = async value => {
      const currentUpdate = ++updateId
      const { mode, query } = parseSearchEverywhereQuery(value)
      picker.busy = true
      const shouldSearchSymbols = mode === 'symbols' ? query.length >= 1 : mode === 'all' && query.length >= 2
      const [files, symbols, indexHits] = await Promise.all([
        mode === 'actions' || mode === 'symbols' ? Promise.resolve([]) : workspaceFileCache.get(),
        shouldSearchSymbols
          ? Promise.resolve(vscode.commands.executeCommand('vscode.executeWorkspaceSymbolProvider', query)).then(value => value || []).catch(() => [])
          : Promise.resolve([]),
        (mode === 'all' || mode === 'symbols') && query
          ? fetchPointIndexHits(query, mode === 'symbols' ? 60 : 24)
          : Promise.resolve([]),
      ])
      if (currentUpdate !== updateId) return
      const indexItems = indexHits.map(indexHitToPickerItem)
  
      const fileItems = (mode === 'all' || mode === 'files')
        ? files
          .map(uri => {
            const relative = vscode.workspace.asRelativePath(uri, false)
            const label = path.basename(uri.fsPath || uri.path)
            const scoreQuery = query || ''
            return { uri, label, relative, score: Math.min(fuzzyScore(label, scoreQuery), fuzzyScore(relative, scoreQuery)) }
          })
          .filter(item => !query || Number.isFinite(item.score))
          .sort((left, right) => left.score - right.score || left.relative.localeCompare(right.relative))
          .slice(0, mode === 'files' ? 120 : 80)
          .map(item => ({
            label: `$(file) ${item.label}`,
            description: path.dirname(item.relative) === '.' ? 'файл' : path.dirname(item.relative),
            detail: `Файл · ${item.relative}`,
            target: { type: 'file', uri: item.uri },
          }))
        : []
  
      const symbolItems = symbols.slice(0, mode === 'symbols' ? 120 : 80).map(symbol => {
        const uri = symbol.location?.uri || symbol.location
        const range = symbol.location?.range
        const relative = uri ? vscode.workspace.asRelativePath(uri, false) : ''
        const kindLabel = vscode.SymbolKind[symbol.kind] || 'Symbol'
        return {
          label: `$(${symbolIcon(symbol.kind)}) ${symbol.name}`,
          description: symbol.containerName || kindLabel,
          detail: `Символ · ${relative}`,
          target: { type: 'symbol', uri, range },
        }
      }).filter(item => item.target.uri)
  
      const actionItems = (mode === 'all' || mode === 'actions') ? matchSearchEverywhereActions(query) : []
      const items = []
      if (!query && mode === 'all') {
        const recent = mergeRecentFiles(
          listRecentFileRecords(),
          collectOpenTabUris().map(uri => ({ uri: uri.toString(), path: vscode.workspace.asRelativePath(uri, false) })),
          16,
        ).map(item => {
          let uri
          try { uri = vscode.Uri.parse(item.uri) } catch { return undefined }
          const relative = item.path || vscode.workspace.asRelativePath(uri, false)
          const line = Number.isInteger(item.line) && item.line >= 0 ? item.line + 1 : 0
          return {
            label: `$(history) ${path.basename(uri.fsPath || uri.path)}`,
            description: line > 0
              ? `${path.dirname(relative) === '.' ? relative : path.dirname(relative)}:${line}`
              : (path.dirname(relative) === '.' ? 'недавний' : path.dirname(relative)),
            detail: line > 0 ? `Недавний · ${relative}:${line}` : `Недавний · ${relative}`,
            target: { type: 'recent', uri, line: item.line || 0, character: item.character || 0 },
          }
        }).filter(Boolean)
        if (recent.length) items.push({ label: 'Недавние вкладки', kind: vscode.QuickPickItemKind.Separator }, ...recent)
        items.push(
          { label: 'Разделы поиска', kind: vscode.QuickPickItemKind.Separator },
          { label: '$(symbol-class) Только символы', description: 'префикс #', alwaysShow: true, target: { type: 'prefix', value: '#' } },
          { label: '$(file) Только файлы', description: 'префикс /', alwaysShow: true, target: { type: 'prefix', value: '/' } },
          { label: '$(zap) Только действия', description: 'префикс @', alwaysShow: true, target: { type: 'prefix', value: '@' } },
        )
        items.push({ label: 'Быстрые переходы', kind: vscode.QuickPickItemKind.Separator }, ...matchSearchEverywhereActions('').slice(0, 8))
      }
      if (query && mode !== 'actions') {
        items.push({
          label: `$(search) Искать текст «${query}» во всём проекте`,
          description: 'Ctrl+Shift+F · всплывающее окно',
          detail: 'Текст',
          alwaysShow: true,
          target: { type: 'text', query },
        })
      }
      if (actionItems.length && (mode === 'actions' || (mode === 'all' && query))) {
        items.push({ label: 'Действия', kind: vscode.QuickPickItemKind.Separator }, ...actionItems)
      }
      if (indexItems.length) items.push({ label: 'Индекс Point', kind: vscode.QuickPickItemKind.Separator }, ...indexItems)
      if (symbolItems.length) items.push({ label: 'Символы language server', kind: vscode.QuickPickItemKind.Separator }, ...symbolItems)
      if (fileItems.length) items.push({ label: 'Файлы', kind: vscode.QuickPickItemKind.Separator }, ...fileItems)
      if (!items.length) {
        items.push({
          label: mode === 'actions' ? '$(zap) Найти действие в палитре' : '$(search) Искать текст в проекте',
          description: mode === 'actions' ? 'Ctrl+Shift+A' : 'Введите имя или фрагмент',
          alwaysShow: true,
          target: mode === 'actions'
            ? { type: 'command', command: 'workbench.action.showCommands' }
            : { type: 'text', query },
        })
      }
      picker.items = items
      picker.busy = false
    }
  
    const valueSubscription = picker.onDidChangeValue(value => {
      clearTimeout(timer)
      timer = setTimeout(() => void update(value), 120)
    })
    const acceptSubscription = picker.onDidAccept(async () => {
      const item = picker.selectedItems[0]
      const target = item?.target
      if (!target) return
      if (target.type === 'prefix') {
        picker.value = target.value
        return
      }
      picker.hide()
      if (target.type === 'text') {
        await vscode.commands.executeCommand('workbench.action.quickOpen', `%${target.query || ''}`)
        return
      }
      if (target.type === 'command') {
        await vscode.commands.executeCommand(target.command)
        return
      }
      if (target.type === 'index') {
        await openIndexHit(target)
        return
      }
      if (target.type === 'recent') {
        await openRecentFileRecord({
          uri: target.uri.toString(),
          line: target.line,
          character: target.character,
        })
        return
      }
      const document = await vscode.workspace.openTextDocument(target.uri)
      await vscode.window.showTextDocument(document, target.range ? { selection: target.range } : undefined)
    })
    picker.show()
    await update(picker.value || '')
    await new Promise(resolve => picker.onDidHide(resolve))
    clearTimeout(timer)
    valueSubscription.dispose()
    acceptSubscription.dispose()
    picker.dispose()
  }
  
  function languageSupportInstalled(languageId) {
    if (!languageId || BUILTIN_LANGUAGE_SUPPORT.has(languageId)) return true
    const profile = LANGUAGE_SUPPORT.find(item => item.ids.includes(languageId))
    if (!profile) return false
    return Boolean(vscode.extensions.getExtension(profile.extension))
  }
  
  function languageDisplayName(languageId) {
    if (!languageId) return 'этого языка'
    const profile = LANGUAGE_SUPPORT.find(item => item.ids.includes(languageId))
    return profile?.label || LANGUAGE_LABELS[languageId] || languageId
  }
  
  async function promptLanguageSupport(languageId, kind = 'usages') {
    if (BUILTIN_LANGUAGE_SUPPORT.has(languageId)) return false
    const action = 'Поддержка языков'
    const label = languageDisplayName(languageId)
    const hints = {
      definition: 'Ctrl+Click и Ctrl+B (переход к определению)',
      usages: 'Alt+F7 (использования / Find Usages)',
      implementations: 'Ctrl+Alt+B (реализации интерфейса)',
    }
    const hint = hints[kind] || 'навигация по символам'
    const selected = await vscode.window.showWarningMessage(
      `Для «${label}» нужен language server — без него ${hint} не работает. Откройте «Поддержка языков» (Language Center) и установите LS; Point ставит его по требованию.`,
      action,
    )
    if (selected === action) await vscode.commands.executeCommand('localAgent.languageSupport')
    return true
  }
  
  async function goToDefinition() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл и поставьте курсор на переменную, класс или метод.')
      return
    }
    let locations
    try {
      locations = await vscode.commands.executeCommand('vscode.executeDefinitionProvider', editor.document.uri, editor.selection.active)
    } catch {
      locations = undefined
    }
    if (Array.isArray(locations) && locations.length) {
      await vscode.commands.executeCommand('editor.action.revealDefinition')
      return
    }
    const word = currentEditorWord()
    if (word) {
      const hits = (await fetchPointIndexHits(word, 12)).filter(item => item.kind === 'symbol' || item.name === word)
      if (await revealIndexHits(hits, `Индекс Point · ${word}`)) return
    }
    if (!languageSupportInstalled(editor.document.languageId)) {
      await promptLanguageSupport(editor.document.languageId, 'definition')
      return
    }
    await vscode.window.showInformationMessage(word ? `Определение «${word}» не найдено ни у language server, ни в индексе Point.` : 'Определение не найдено для символа под курсором.')
  }
  
  async function goToTypeDefinition() {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл и поставьте курсор на символ (Ctrl+Shift+B — тип).')
      return
    }
    let locations
    try {
      locations = await vscode.commands.executeCommand('vscode.executeTypeDefinitionProvider', editor.document.uri, editor.selection.active)
    } catch {
      locations = undefined
    }
    if (Array.isArray(locations) && locations.length) {
      try {
        await vscode.commands.executeCommand('editor.action.goToTypeDefinition')
        return
      } catch {
        const first = locations[0]
        const uri = first.uri || first.targetUri || first.location?.uri
        const range = first.range || first.targetRange || first.targetSelectionRange || first.location?.range
        if (uri) {
          const document = await vscode.workspace.openTextDocument(uri)
          await vscode.window.showTextDocument(document, range ? { selection: range } : undefined)
          return
        }
      }
    }
    const word = currentEditorWord()
    if (word) {
      const hits = (await fetchPointIndexHits(word, 12)).filter(item => item.kind === 'symbol' || item.name === word)
      if (await revealIndexHits(hits, `Тип · индекс Point · ${word}`)) return
    }
    if (!languageSupportInstalled(editor.document.languageId)) {
      await promptLanguageSupport(editor.document.languageId, 'definition')
      return
    }
    await vscode.window.showInformationMessage(word ? `Тип «${word}» не найден.` : 'Тип символа под курсором не найден.')
  }
  
  async function focusBreadcrumbs() {
    for (const command of ['breadcrumbs.focusAndSelect', 'breadcrumbs.focus']) {
      try {
        await vscode.commands.executeCommand(command)
        return
      } catch { /* try next */ }
    }
    await vscode.window.showInformationMessage('Навигационная строка скрыта — включите в Настройках IDE (Ctrl+Alt+S).')
  }
  
  async function moveStatement(direction) {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл, чтобы переместить строки (Ctrl+Shift+↑/↓).')
      return
    }
    await vscode.commands.executeCommand(direction < 0 ? 'editor.action.moveLinesUpAction' : 'editor.action.moveLinesDownAction')
  }
  
  async function findSymbolLocations(providerCommand, title, kind) {
    const editor = vscode.window.activeTextEditor
    if (!editor) {
      await vscode.window.showInformationMessage('Откройте файл и поставьте курсор на переменную, класс или метод.')
      return
    }
    let locations
    try {
      locations = await vscode.commands.executeCommand(providerCommand, editor.document.uri, editor.selection.active)
    } catch {
      locations = undefined
    }
    if (!Array.isArray(locations) || !locations.length) {
      const word = currentEditorWord()
      if (word) {
        const hits = await fetchPointIndexHits(word, 24)
        if (hits.length) {
          const folder = pointWorkspaceFolder()
          if (folder) {
            const indexLocations = hits.map(hit => {
              const uri = vscode.Uri.joinPath(folder.uri, String(hit.path || '').replace(/\\/g, '/'))
              const line = Math.max(0, (Number(hit.line) || 1) - 1)
              return new vscode.Location(uri, new vscode.Position(line, 0))
            })
            await vscode.commands.executeCommand('editor.action.showReferences', editor.document.uri, editor.selection.active, indexLocations)
            vscode.window.setStatusBarMessage(`${title} по индексу Point`, 2500)
            return
          }
        }
      }
      if (!languageSupportInstalled(editor.document.languageId)) {
        await promptLanguageSupport(editor.document.languageId, kind)
        return
      }
      await vscode.window.showInformationMessage(`${title}: ничего не найдено.`)
      return
    }
    const viewCommand = providerCommand === 'vscode.executeImplementationProvider'
      ? 'references-view.findImplementations'
      : 'references-view.findReferences'
    try {
      await vscode.commands.executeCommand(viewCommand)
      return
    } catch {
      await vscode.commands.executeCommand('editor.action.showReferences', editor.document.uri, editor.selection.active, locations)
    }
  }
  
  async function showCoreChronicle(service, output) {
    output.show(true)
    try {
      await service.ensureLogDir()
      if (!fs.existsSync(service.logPath)) {
        fs.writeFileSync(service.logPath, '', 'utf8')
      }
      const doc = await vscode.workspace.openTextDocument(vscode.Uri.file(service.logPath))
      // Вкладкой в текущей группе, а не `Beside`: тот раскалывает редактор
      // пополам, потому что означает «в соседнюю колонку, а нет её — создать».
      // Хроника — читалка, её открывают рядом с работой, а не вместо половины
      // экрана.
      await vscode.window.showTextDocument(doc, { preview: true, viewColumn: vscode.ViewColumn.Active })
    } catch (error) {
      await vscode.window.showWarningMessage(`Журнал ядра: ${error instanceof Error ? error.message : String(error)}`)
    }
  }

  return {
    createWorkspaceFileCache,
    workspaceFileCache,
    bindPointIndexSearch,
    bindRecentFiles,
    collectOpenTabUris,
    createRecentFilesTracker,
    createStructureStatus,
    createProblemsStatus,
    flattenDocumentSymbols,
    fileStructure,
    copyReference,
    newScratchFile,
    compareWithFile,
    collectSaveActionEdits,
    flushFormatAfterOrganize,
    openPointIdeSettings,
    currentEditorWord,
    fetchPointIndexHits,
    indexHitToPickerItem,
    openIndexHit,
    revealIndexHits,
    companionProblemsMessage,
    searchEverywhere,
    languageSupportInstalled,
    languageDisplayName,
    promptLanguageSupport,
    goToDefinition,
    goToTypeDefinition,
    focusBreadcrumbs,
    moveStatement,
    findSymbolLocations,
    showCoreChronicle,
    listRecentFileRecords,
  }
}

module.exports = { createIdeNavigationController }
