const { formatVcsError } = require('./extension-utils')
function createCompanionController(dependencies) {
  const {
    vscode,
    path,
    runGit,
    normalizedWorkspaceRoot,
    POINT_WORKSPACE_ROOT_KEY,
    GIT_DEFAULT_LIST,
    getActiveView,
    getActiveService,
  } = dependencies

  function normalizeCompanionArg(arg = {}) {
    if (!arg) return {}
    if (typeof arg === 'string') return arg.trim() ? { message: arg.trim() } : {}
    if (typeof arg.fsPath === 'string' && arg.scheme) return { uri: String(arg.toString?.() || arg.fsPath), intent: arg.intent }
    if (arg.resourceUri) {
      return { ...arg, uri: String(arg.resourceUri.toString?.() || arg.resourceUri), intent: arg.intent }
    }
    if (arg.uri && typeof arg.uri !== 'string') return { ...arg, uri: String(arg.uri.toString?.() || arg.uri) }
    return arg
  }
  
  function companionClosing(intent) {
    if (intent === 'fix') {
      return 'Подготовь проверяемый квест исправления: файлы, строки и критерий успешной команды. Не запускай квест без подтверждения.'
    }
    if (intent === 'analyze') {
      return 'Объясни причину и риски. Квест не создавай, пока я явно не попрошу исправить.'
    }
    return 'Подготовь проверяемый план; не запускай квест без моего подтверждения.'
  }
  
  function nearbyEditorSnippet(document, position, radius = 8) {
    if (!document || !position) return ''
    const start = Math.max(0, position.line - radius)
    const end = Math.min(document.lineCount - 1, position.line + radius)
    const text = document.getText(new vscode.Range(start, 0, end, document.lineAt(end).text.length)).slice(0, 2500)
    return text.trim() ? `Контекст L${start + 1}–${end + 1}:\n${text}` : ''
  }
  
  async function resolveCompanionDocument(arg = {}, editor = vscode.window.activeTextEditor) {
    if (arg.uri) {
      const wanted = String(arg.uri)
      const open = vscode.workspace.textDocuments.find(item => item.uri.toString() === wanted)
      if (open) return open
      try {
        return await vscode.workspace.openTextDocument(vscode.Uri.parse(wanted))
      } catch {
        return editor?.document
      }
    }
    return editor?.document
  }
  
  function companionFailureMessage(failure, intent = 'analyze') {
    if (!failure) {
      return `Последней упавшей команды IDE нет в этой сессии. ${companionClosing(intent)}`
    }
    const parts = [
      `${failure.kind === 'task' ? 'Задача' : 'Команда'} завершилась с кодом ${failure.exitCode}.`,
      failure.command ? `Команда: ${failure.command}` : '',
      failure.source ? `Источник: ${failure.source}` : '',
      failure.detail ? `Вывод:\n${String(failure.detail).slice(-4000)}` : '',
    ].filter(Boolean)
    return `${parts.join('\n\n')}\n\n${companionClosing(intent)}`
  }
  
  async function companionAskMessage(arg = {}, getFailure) {
    arg = normalizeCompanionArg(arg)
    if (arg.message && String(arg.message).trim() && arg.intent !== 'fix' && arg.intent !== 'analyze') {
      return String(arg.message).trim()
    }
    const intent = arg.intent || 'ask'
    const editor = vscode.window.activeTextEditor
    const document = await resolveCompanionDocument(arg, editor)
    const parts = []
    if (arg.message && String(arg.message).trim()) parts.push(String(arg.message).trim())
    if (document) {
      const relative = vscode.workspace.asRelativePath(document.uri, false).replace(/\\/g, '/')
      parts.push(`Файл: ${relative}${document.languageId ? ` · ${document.languageId}` : ''}${document.isDirty ? ' · не сохранён' : ''}`)
      const selection = arg.selection?.start && arg.selection?.end
        ? new vscode.Range(
            new vscode.Position(Number(arg.selection.start.line || 0), Number(arg.selection.start.character || 0)),
            new vscode.Position(Number(arg.selection.end.line || 0), Number(arg.selection.end.character || 0)),
          )
        : editor?.document?.uri?.toString() === document.uri.toString() ? editor.selection : undefined
      if (selection && !selection.isEmpty) {
        parts.push(`Выделение (${selection.start.line + 1}:${selection.start.character + 1}):\n${document.getText(selection).slice(0, 4000)}`)
      } else {
        const position = selection?.start || (editor?.document?.uri?.toString() === document.uri.toString() ? editor.selection.active : new vscode.Position(0, 0))
        const wordRange = document.getWordRangeAtPosition(position)
        if (wordRange && !wordRange.isEmpty) {
          parts.push(`Символ под курсором: ${document.getText(wordRange)} (L${wordRange.start.line + 1})`)
        }
        const nearby = nearbyEditorSnippet(document, position)
        if (nearby) parts.push(nearby)
      }
      const diagnosticMessages = Array.isArray(arg.diagnosticMessages) ? arg.diagnosticMessages.filter(Boolean) : []
      const liveDiagnostics = vscode.languages.getDiagnostics(document.uri)
        .filter(item => item.severity === vscode.DiagnosticSeverity.Error || item.severity === vscode.DiagnosticSeverity.Warning)
        .slice(0, 8)
        .map(item => `L${item.range.start.line + 1}: ${item.message}`)
      const diagnostics = diagnosticMessages.length ? diagnosticMessages.slice(0, 8) : liveDiagnostics
      if (diagnostics.length) parts.push(`Диагностика:\n${diagnostics.join('\n')}`)
    }
    const extras = typeof getFailure === 'object' && getFailure ? getFailure : { getFailure }
    const getRun = extras.getRun
    const failure = typeof extras.getFailure === 'function' ? extras.getFailure() : typeof getFailure === 'function' ? getFailure() : undefined
    const run = typeof getRun === 'function' ? getRun() : undefined
    if (run) parts.push(`Цель запуска: ${run.label || run.short}${run.kind ? ` · ${run.kind}` : ''}`)
    if (vscode.debug?.activeDebugSession) parts.push(`Отладка: ${vscode.debug.activeDebugSession.name}`)
    if (failure && (intent === 'fix' || intent === 'analyze' || !parts.length)) {
      parts.push(companionFailureMessage(failure, intent).replace(`\n\n${companionClosing(intent)}`, ''))
    }
    if (!parts.length) {
      return `Какие ошибки, сбои и риски сейчас в проекте? ${companionClosing(intent)}`
    }
    const lead = intent === 'fix'
      ? 'Исправь этот фрагмент IDE.'
      : intent === 'analyze'
        ? 'Разбери этот фрагмент IDE.'
        : 'Помоги с этим фрагментом IDE.'
    return `${lead}\n\n${parts.join('\n\n')}\n\n${companionClosing(intent)}`
  }
  
  async function openCompanionChat(provider, revealInfra, opts = {}) {
    if (typeof revealInfra === 'function') revealInfra()
    if (!provider) return
    const requested = opts.surface === 'peek' ? 'peek' : opts.surface === 'sidebar' ? 'sidebar' : opts.surface === 'dock' ? 'dock' : ''
    const surface = opts.forceSurface
      ? (requested || 'peek')
      : provider.preferLiveCompanionSurface(requested || 'peek')
    let openedSurface = surface
    if (surface === 'peek') openedSurface = provider.showCompanionPeek() || 'peek'
    else if (surface === 'dock') openedSurface = await provider.showCompanionDock()
    else openedSurface = await provider.showCompanionSidebar()
    provider.queueCompanionFocus({
      message: typeof opts.message === 'string' ? opts.message : '',
      send: Boolean(opts.send),
      surface: openedSurface || surface,
    })
  }
  
  function createCompanionDecorations() {
    const type = vscode.window.createTextEditorDecorationType({
      isWholeLine: false,
      overviewRulerColor: '#46d8e8',
      overviewRulerLane: vscode.OverviewRulerLane.Right,
      borderWidth: '0 0 0 2px',
      borderStyle: 'solid',
      borderColor: '#46d8e899',
    })
    const paint = (interventions = []) => {
      const folder = pointWorkspaceFolder()
      const byUri = new Map()
      if (folder) {
        for (const item of interventions) {
          // Decorate only gated speak signals that already passed companion live gate.
          if (!item?.relatedPath) continue
          if (item.level !== 'critical' && item.level !== 'warning') continue
          const uri = workspaceFileUri(item.relatedPath)
          if (!uri) continue
          const line = Math.max(0, Number(item.relatedLine || 1) - 1)
          const list = byUri.get(uri.toString()) || []
          list.push({
            range: new vscode.Range(line, 0, line, 0),
            hoverMessage: new vscode.MarkdownString(`**Компаньон** · ${item.title || 'сигнал'}\n\n${item.detail || ''}`),
          })
          byUri.set(uri.toString(), list)
        }
      }
      for (const editor of vscode.window.visibleTextEditors) {
        editor.setDecorations(type, byUri.get(editor.document.uri.toString()) || [])
      }
    }
    return {
      paint,
      dispose() {
        type.dispose()
      },
    }
  }
  
  function createCompanionCodeLensProvider() {
    const emitter = new vscode.EventEmitter()
    return {
      onDidChangeCodeLenses: emitter.event,
      refresh: () => emitter.fire(),
      provideCodeLenses(document) {
        const lenses = []
        const editor = vscode.window.activeTextEditor
        if (
          editor
          && editor.document.uri.toString() === document.uri.toString()
          && document.uri.scheme === 'file'
          && !editor.selection.isEmpty
        ) {
          const sel = editor.selection
          const arg = {
            uri: document.uri.toString(),
            selection: { start: sel.start, end: sel.end },
          }
          const anchor = new vscode.Range(sel.start, sel.start)
          lenses.push(new vscode.CodeLens(anchor, {
            title: '✦ Спросить компаньона',
            tooltip: 'Открыть быстрый чат с выделенным кодом',
            command: 'localAgent.askCompanionAbout',
            arguments: [arg],
          }))
          lenses.push(new vscode.CodeLens(anchor, {
            title: 'Исправить',
            tooltip: 'Подготовить исправление через компаньона',
            command: 'localAgent.askCompanionFix',
            arguments: [{ ...arg, intent: 'fix' }],
          }))
        }
        const seen = new Set()
        for (const item of vscode.languages.getDiagnostics(document.uri)) {
          if (item.severity !== vscode.DiagnosticSeverity.Error) continue
          const line = item.range.start.line
          if (seen.has(line)) continue
          seen.add(line)
          const arg = {
            uri: document.uri.toString(),
            selection: { start: item.range.start, end: item.range.end },
            diagnosticMessages: [`L${line + 1}: ${item.message}`],
            intent: 'fix',
          }
          lenses.push(new vscode.CodeLens(item.range, {
            title: 'Компаньон: подготовить исправление',
            tooltip: 'Только рекомендация — квест не стартует',
            command: 'localAgent.askCompanionFix',
            arguments: [arg],
          }))
          if (lenses.filter(lens => String(lens.command?.title || '').includes('подготовить')).length >= 8) break
        }
        return lenses
      },
      dispose() { emitter.dispose() },
    }
  }
  
  function companionCommandMarkdown(command, arg, label) {
    const uri = vscode.Uri.parse(`command:${command}?${encodeURIComponent(JSON.stringify([arg]))}`)
    return `[${label}](${uri.toString()})`
  }
  
  function companionWorkspaceRelativePath(uri) {
    if (!uri) return ''
    const selected = getActiveService()?.workspaceFolder?.()
    if (selected?.uri?.scheme === 'file') {
      return workspaceRelativePathIfInside(selected.uri.fsPath, uri)
    }
    const relative = String(vscode.workspace.asRelativePath(uri, false) || '').replace(/\\/g, '/')
    if (!relative || relative.split('/').includes('..')) return ''
    return relative
  }
  
  function formatCompanionFailure(failure) {
    if (!failure) return ''
    const command = String(failure.command || '').trim()
    const detail = String(failure.detail || '').trim().split(/\r?\n/).filter(Boolean).slice(-2).join(' · ')
    return [command, detail].filter(Boolean).join(' · ').slice(0, 200)
  }
  
  // Отказ провайдера человек читает как отказ Point, поэтому причина называется
  // словами и с ближайшим шагом. Разбор идёт по ответу провайдера: ядро отдаёт
  // его как есть — «provider returned 404 Not Found: {...}», — и без разбора
  // человек видел сырой JSON вместо «модель не найдена».
  function formatCompanionChatError(error, modelName = '') {
    const text = error instanceof Error ? error.message : String(error || '')
    const lower = text.toLowerCase()
    const model = String(modelName || '').trim()
    const named = model ? ` «${model}»` : ''
    if (lower.includes('timeout') || lower.includes('timed out') || lower.includes('aborted') || lower.includes('abort')) {
      return 'Компаньон не успел ответить. Проверьте LLMux или повторите короче.'
    }
    if (lower.includes('401') || lower.includes('403') || lower.includes('unauthorized') || lower.includes('api key') || lower.includes('credential')) {
      return 'Модель не приняла токен. Проверьте ключ в настройке компаньона.'
    }
    // Провайдер не знает такую модель — самая частая причина после ключа: имя
    // модели меняется на стороне сервиса, а в настройке остаётся прежним.
    if (lower.includes('model_not_found') || lower.includes('unknown model') || lower.includes('does not exist')
      || ((lower.includes('404') || lower.includes('not found') || lower.includes('unsupported')) && lower.includes('model'))) {
      return `Провайдер не знает модель${named}. Выберите её из списка в настройке компаньона — имя могло измениться на стороне сервиса.`
    }
    // Запрос не поместился в окно модели: чинится не повтором, а укорачиванием.
    if (lower.includes('context length') || lower.includes('context_length') || lower.includes('maximum context')
      || lower.includes('too many tokens') || lower.includes('reduce the length')) {
      return `Запрос не поместился в окно модели${named}. Начните новый чат или задайте вопрос короче — длинная история занимает место ответа.`
    }
    // Ограничение частоты: ждать, а не менять настройку.
    if (lower.includes('429') || lower.includes('rate limit') || lower.includes('too many requests')) {
      return 'Провайдер ограничил частоту запросов. Подождите минуту и повторите.'
    }
    if (lower.includes('econnrefused') || lower.includes('enotfound') || lower.includes('fetch failed') || lower.includes('network')) {
      return 'Нет связи с моделью. Проверьте адрес LLMux или локальный сервер.'
    }
    // Сбой на стороне сервиса: настройка ни при чём, и звать её чинить незачем.
    if (lower.includes('500') || lower.includes('502') || lower.includes('503') || lower.includes('504')
      || lower.includes('internal server error') || lower.includes('bad gateway') || lower.includes('service unavailable')) {
      return 'Провайдер ответил ошибкой на своей стороне. Повторите позже или выберите другую модель.'
    }
    // Вставленный лог целиком — обычное дело: ядро отклоняет такое сообщение,
    // и человеку нужен не размер в KiB, а что с этим делать.
    if (lower.includes('exceeds 32 kib') || (lower.includes('message') && lower.includes('exceeds'))) {
      return 'Сообщение длиннее 32 КБ — столько ядро не принимает. Сократите текст или назовите путь к файлу: помощник прочитает его сам.'
    }
    if (text.includes('Сначала разрешите')) return text
    if (text.startsWith('Компаньон')) return text
    return `Компаньон не смог ответить: ${text.slice(0, 220)}`
  }
  
  function currentCompanionIdeContext(getFailureOrOpts) {
    const opts = typeof getFailureOrOpts === 'function' ? { getFailure: getFailureOrOpts } : (getFailureOrOpts || {})
    const editor = vscode.window.activeTextEditor
    const file = editor ? companionWorkspaceRelativePath(editor.document.uri) : ''
    const failure = typeof opts.getFailure === 'function' ? opts.getFailure() : undefined
    const run = typeof opts.getRun === 'function' ? opts.getRun() : undefined
    const diagnostics = editor && file
      ? vscode.languages.getDiagnostics(editor.document.uri).filter(item =>
        item.severity === vscode.DiagnosticSeverity.Error || item.severity === vscode.DiagnosticSeverity.Warning
      ).length
      : 0
    return {
      file,
      line: editor && file ? editor.selection.active.line + 1 : 0,
      language: editor?.document.languageId || '',
      dirty: Boolean(editor?.document.isDirty),
      diagnostics,
      selection: Boolean(editor && !editor.selection.isEmpty),
      failure: formatCompanionFailure(failure),
      run: run?.label || run?.short || '',
      debug: vscode.debug?.activeDebugSession?.name || '',
    }
  }
  
  function liveCompanionFocus(getFailure, getRun) {
    const ctx = currentCompanionIdeContext({ getFailure, getRun })
    const editor = vscode.window.activeTextEditor
    let snippet = ''
    // A multi-root window can display another project while Point is attached to
    // the selected one. Never leak a neighbouring project's text into this
    // project's companion context.
    if (editor && ctx.file && !editor.selection.isEmpty) {
      snippet = editor.document.getText(editor.selection).slice(0, 2500)
    } else if (editor && ctx.file) {
      snippet = nearbyEditorSnippet(editor.document, editor.selection.active, 8).replace(/^Контекст[^\n]*\n/, '').slice(0, 2500)
    }
    return { ...ctx, snippet }
  }
  
  function pathIsUnder(childPath, parentPath) {
    if (!childPath || !parentPath) return false
    const child = path.resolve(String(childPath))
    const parent = path.resolve(String(parentPath))
    const normalize = value => (process.platform === 'win32' ? value.toLowerCase() : value)
    const c = normalize(child)
    const p = normalize(parent)
    if (c === p) return true
    const prefix = p.endsWith(path.sep) ? p : `${p}${path.sep}`
    return c.startsWith(prefix)
  }
  
  function pickGitRepository(repos, uri) {
    if (!Array.isArray(repos) || !repos.length) return undefined
    if (!uri?.fsPath) return repos[0]
    const matches = repos.filter(item => item?.rootUri?.fsPath && pathIsUnder(uri.fsPath, item.rootUri.fsPath))
    if (!matches.length) return repos[0]
    return matches.sort((a, b) => String(b.rootUri.fsPath).length - String(a.rootUri.fsPath).length)[0]
  }
  
  // Раскладка изменённых файлов по папкам. Чистая функция: то же состояние и тот
  // же список изменений дают тот же ответ, поэтому её проверяет смоук, а не глаз.
  //
  // Правило одно: назначение живёт ровно столько, сколько живёт само изменение.
  // Иначе список назначений рос бы вечно и однажды вернул бы файл в чужую папку
  // через месяц после коммита — там, где человек ждёт новую работу в активной.
  function gitListsState(saved, changes = []) {
    const lists = []
    for (const item of Array.isArray(saved?.lists) ? saved.lists : []) {
      const id = String(item?.id || '').trim()
      const name = String(item?.name || '').trim().slice(0, 60)
      if (!id || !name || lists.some(entry => entry.id === id)) continue
      lists.push({ id, name })
    }
    if (!lists.some(item => item.id === GIT_DEFAULT_LIST)) lists.unshift({ id: GIT_DEFAULT_LIST, name: 'Изменения' })
    const active = lists.some(item => item.id === saved?.active) ? String(saved.active) : GIT_DEFAULT_LIST
    const previous = saved?.assign && typeof saved.assign === 'object' ? saved.assign : {}
    const assign = {}
    for (const change of Array.isArray(changes) ? changes : []) {
      const file = String(change?.path || '')
      // Файлы вне репозитория живут в своей папке и по чужим не раскладываются.
      if (!file || change?.area === 'untracked') continue
      const stored = String(previous[file] || '')
      assign[file] = lists.some(item => item.id === stored) ? stored : active
    }
    return { lists, active, assign }
  }
  
  function workspaceFolderForUri(uri) {
    return (uri && vscode.workspace.getWorkspaceFolder(uri)) || vscode.workspace.workspaceFolders?.[0]
  }
  
  function pointWorkspaceFolder() {
    return getActiveService()?.workspaceFolder?.()
      || workspaceFolderForUri(vscode.window.activeTextEditor?.document?.uri)
  }
  
  function localWorkspaceFolders() {
    return (vscode.workspace.workspaceFolders || []).filter(folder => folder?.uri?.scheme === 'file')
  }
  
  function workspaceFolderByPath(rootPath) {
    if (!rootPath) return undefined
    const target = normalizedWorkspaceRoot(rootPath)
    return localWorkspaceFolders().find(folder => normalizedWorkspaceRoot(folder.uri.fsPath) === target)
  }
  
  function restorePointWorkspaceRoot(service, context, options = {}) {
    // Чертог всегда просыпается галереей. Рабочая область окна сессий — файл, и
    // папка прошлого мира могла остаться в нём после переключения; без этого
    // выхода она молча воскресила бы привязку вместо списка проектов.
    if (options.agentsWindowMode) {
      service.setWorkspaceRoot(undefined)
      return undefined
    }
    const folders = localWorkspaceFolders()
    if (!folders.length) {
      service.setWorkspaceRoot(undefined)
      return undefined
    }
    const current = service.workspaceRootOverride?.fsPath
      ? workspaceFolderByPath(service.workspaceRootOverride.fsPath)
      : undefined
    const savedPath = String(context?.workspaceState?.get?.(POINT_WORKSPACE_ROOT_KEY, '') || '')
    const saved = workspaceFolderByPath(savedPath)
    // Активный мир Чертога подсказывает выбор окну IDE, но только если это окно
    // его и открыло: тянуть окно на проект, которого в нём нет, нельзя.
    const activeHubPath = String(options.activeProjectPath || '')
    const activeHub = activeHubPath ? workspaceFolderByPath(activeHubPath) : undefined
    const editorFolder = workspaceFolderForUri(vscode.window.activeTextEditor?.document?.uri)
    const selected = current || saved || activeHub || (editorFolder?.uri?.scheme === 'file' ? editorFolder : undefined) || folders[0]
    service.setWorkspaceRoot(selected.uri)
    return selected
  }

  // Возврат на последний мир при старте Чертога.
  //
  // Окно сессий оболочка открывает пустым намеренно: главный процесс не видит
  // globalState и подставил бы мир вслепую — окно открылось бы на несуществующем
  // корне ещё до первой строки расширения, а заплата доверия стоит на том, что у
  // файла Чертога при старте ноль папок. Поэтому мир возвращает расширение и
  // только после первого кадра: сначала человек видит экран, потом под ним
  // появляется мир — и диалог доверия не гоняется с первой отрисовкой.
  async function resumeLastPointWorld(service, context, options = {}) {
    const registry = options.registry
    const last = String(registry?.activePath?.() || '')
    if (!last) return undefined
    let exists = true
    try {
      const stat = await Promise.race([
        require('node:fs/promises').stat(last),
        new Promise((_, reject) => setTimeout(() => reject(Object.assign(new Error('timeout'), { name: 'TimeoutError' })), 300)),
      ])
      exists = stat.isDirectory()
    } catch (error) {
      // Таймаут — не приговор: сетевой диск может спать. Запись не забываем и
      // мир не поднимаем: человек выберет сам из списка.
      if (error?.name === 'TimeoutError') {
        options.hostLog?.('warn', `[world] диск не ответил про ${last}`)
        return undefined
      }
      exists = false
    }
    if (!exists) {
      options.hostLog?.('info', `[world] последний мир исчез с диска: ${last}`)
      await registry?.setActive?.('')
      return undefined
    }
    await options.switchToProject?.(last)
    return last
  }

  // Переключение мира прямо в окне Чертога, без перезагрузки.
  //
  // Окно сессий открыто сохранённой рабочей областью, то есть уже в состоянии
  // WORKSPACE: подмена папки правит только JSON области и не роняет окно и не
  // перезапускает хост расширений. Прошлое ядро при этом отпускается
  // (`detach`), а не гасится, — возврат к нему стоит одной проверки здоровья.
  // `vscode.openFolder` здесь не зовётся ни в одной ветке: он перезагружает
  // окно и гасит ядро, а это ровно та медленная дорога, от которой уходим.
  async function switchPointWorkspaceInPlace(service, context, uri, onSwitched, options = {}) {
    if (!uri || uri.scheme !== 'file') return undefined
    const name = path.basename(uri.fsPath)
    const target = normalizedWorkspaceRoot(uri.fsPath)
    const current = service.workspaceFolder()
    if (current && normalizedWorkspaceRoot(current.uri.fsPath) === target) {
      await options.registry?.setActive(uri.fsPath)
      return { uri: current.uri, name: current.name || name }
    }
    const startedAt = Date.now()
    options.onPhase?.('start', { path: uri.fsPath, name })
    // Незаконченный ход мастера бронирует ядро уходящего мира: без этого
    // третье переключение снесло бы его вместе с ответом, который человек ждёт.
    const busyUntil = options.busy ? Date.now() + 10 * 60_000 : 0
    const outgoing = current && service.runtimeWorkspaceKey
      ? { key: service.runtimeWorkspaceKey, path: current.uri.fsPath, pid: service.corePid(), busyUntil }
      : undefined
    service.detach()
    if (outgoing?.pid) await options.warmPool?.remember(outgoing)
    service.setWorkspaceRoot(uri)
    await options.registry?.markOpened(uri.fsPath, { name })
    await context?.workspaceState?.update?.(POINT_WORKSPACE_ROOT_KEY, uri.fsPath)
    const count = (vscode.workspace.workspaceFolders || []).length
    const changed = vscode.workspace.updateWorkspaceFolders(0, count, { uri, name })
    const folder = { uri, name }
    if (typeof onSwitched === 'function') await onSwitched(folder)
    if (!changed) void vscode.window.showWarningMessage('Point не смог заменить рабочую папку Agent Hub.')
    void options.warmPool?.reap()
    options.onPhase?.('done', { path: uri.fsPath, name, ms: Date.now() - startedAt })
    service.hostLog?.('info', `[switch] project=${name} warm=${outgoing?.pid ? 1 : 0} ms=${Date.now() - startedAt}`)
    return folder
  }
  
  // Недавние проекты для переключателя. JetBrains показывает их прямо в
  // выпадающем списке виджета проекта — имя и путь, — а не прячет за пунктом,
  // который открывает второй диалог. Команда приватная, поэтому обёрнута: если
  // оболочка её не отдаст, список просто останется без раздела недавних.
  async function recentPointProjects(limit = 8) {
    try {
      const recent = await vscode.commands.executeCommand('_workbench.getRecentlyOpened')
      const entries = Array.isArray(recent?.workspaces) ? recent.workspaces : []
      return entries
        .map(entry => entry?.folderUri || entry?.workspace?.configPath)
        .filter(uri => uri && (uri.scheme === 'file' || typeof uri === 'object'))
        .map(uri => vscode.Uri.from(uri))
        .filter(uri => uri.scheme === 'file')
        .slice(0, limit)
    } catch {
      return []
    }
  }
  
  async function choosePointWorkspace(service, context, onSwitched, options = {}) {
    const folders = localWorkspaceFolders()
    const current = service.workspaceFolder()
    const openPaths = new Set(folders.map(folder => normalizedWorkspaceRoot(folder.uri.fsPath)))
    const recent = (await recentPointProjects()).filter(uri => !openPaths.has(normalizedWorkspaceRoot(uri.fsPath)))
    const selected = await vscode.window.showQuickPick([
      ...(folders.length ? [{ label: 'Проекты рабочей области', kind: vscode.QuickPickItemKind.Separator }] : []),
      ...folders.map(folder => ({
        label: `$(${current && normalizedWorkspaceRoot(current.uri.fsPath) === normalizedWorkspaceRoot(folder.uri.fsPath) ? 'check' : 'folder'}) ${folder.name}`,
        description: current && normalizedWorkspaceRoot(current.uri.fsPath) === normalizedWorkspaceRoot(folder.uri.fsPath) ? 'Активный проект Point' : '',
        detail: folder.uri.fsPath,
        folder,
      })),
      ...(recent.length ? [{ label: 'Недавние проекты', kind: vscode.QuickPickItemKind.Separator }] : []),
      ...recent.map(uri => ({
        label: `$(folder) ${path.basename(uri.fsPath)}`,
        detail: uri.fsPath,
        action: 'open',
        uri,
      })),
      { label: 'Открыть проект', kind: vscode.QuickPickItemKind.Separator },
      ...(recent.length ? [] : [{ label: '$(history) Недавние проекты…', description: 'Открыть другой проект Point', action: 'recent' }]),
      { label: '$(folder-opened) Выбрать папку проекта…', description: options.agentsWindowMode ? 'Привязать Agent Hub без перезапуска окна' : 'Открыть в текущем окне Point', action: 'browse' },
      { label: '$(repo-clone) Клонировать из Git…', description: 'Добавить новый проект из удалённого репозитория', action: 'clone' },
      ...(folders.length ? [{ label: '$(folder-library) Добавить проект в рабочую область…', description: 'Multi-root workspace', action: 'add' }] : []),
    ], {
      title: 'Point — активный проект',
      placeHolder: 'Агенты, индекс и относительные пути работают только с выбранным проектом',
      matchOnDescription: true,
      matchOnDetail: true,
    })
    if (!selected) return undefined
    if (selected.action === 'recent') {
      await vscode.commands.executeCommand('workbench.action.openRecent')
      return undefined
    }
    if (selected.action === 'open' && selected.uri) {
      // В окне Чертога недавний проект подставляется на месте. Прежняя ветка
      // звала `vscode.openFolder`, то есть перезагружала окно и гасила ядро —
      // самый дорогой способ сделать ровно то же самое.
      if (options.agentsWindowMode) return switchPointWorkspaceInPlace(service, context, selected.uri, onSwitched, options)
      await vscode.commands.executeCommand('vscode.openFolder', selected.uri, false)
      return { uri: selected.uri, name: path.basename(selected.uri.fsPath) }
    }
    if (selected.action === 'clone') {
      await vscode.commands.executeCommand('localAgent.gitClone')
      return undefined
    }
    if (selected.action === 'browse') {
      const picked = await vscode.window.showOpenDialog({
        canSelectFiles: false,
        canSelectFolders: true,
        canSelectMany: false,
        openLabel: options.agentsWindowMode ? 'Подключить к Agent Hub' : 'Открыть в Point',
        title: 'Point — выбрать проект',
      })
      const uri = picked?.[0]
      if (!uri) return undefined
      if (!options.agentsWindowMode) {
        await vscode.commands.executeCommand('vscode.openFolder', uri, false)
        return { uri, name: path.basename(uri.fsPath) }
      }
      return switchPointWorkspaceInPlace(service, context, uri, onSwitched, options)
    }
    if (selected.action === 'add') {
      await vscode.commands.executeCommand('workbench.action.addRootFolder')
      return undefined
    }
    const folder = selected.folder
    if (!folder) return undefined
    const same = current && normalizedWorkspaceRoot(current.uri.fsPath) === normalizedWorkspaceRoot(folder.uri.fsPath)
    await context?.workspaceState?.update?.(POINT_WORKSPACE_ROOT_KEY, folder.uri.fsPath)
    if (same) return folder
    await service.stop()
    service.setWorkspaceRoot(folder.uri)
    if (typeof onSwitched === 'function') await onSwitched(folder)
    void vscode.window.showInformationMessage(`Активный проект Point: ${folder.name}. Ядро запустится по требованию.`)
    return folder
  }
  
  function chronicleWorkspaceFolder() {
    const active = vscode.window.activeTextEditor?.document?.uri
    if (active) {
      const folder = vscode.workspace.getWorkspaceFolder(active)
      if (folder?.uri?.scheme === 'file') return folder
    }
    const folders = vscode.workspace.workspaceFolders || []
    return folders.find(item => item.uri.scheme === 'file') || folders[0]
  }
  
  function gitCwdForUri(uri) {
    return workspaceFolderForUri(uri)?.uri.fsPath
  }
  
  async function resolveGitRoot(cwd) {
    if (!cwd) return ''
    try {
      const root = String(await runGit(cwd, ['rev-parse', '--show-toplevel'])).trim()
      return root || cwd
    } catch {
      return cwd
    }
  }
  
  function pathRelativeToRoot(uri, rootPath) {
    if (!uri?.fsPath) return ''
    if (!rootPath) return companionWorkspaceRelativePath(uri)
    const relative = path.relative(rootPath, uri.fsPath)
    if (!relative || relative === '..' || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
      return companionWorkspaceRelativePath(uri)
    }
    return relative.replace(/\\/g, '/')
  }
  
  
  async function companionDiffMessage(arg = {}) {
    arg = normalizeCompanionArg(arg)
    const uri = arg.uri
      ? vscode.Uri.parse(String(arg.uri))
      : vscode.window.activeTextEditor?.document.uri
    if (!uri) {
      return `Какие незакоммиченные изменения сейчас рискованны? ${companionClosing('analyze')}`
    }
    const relative = companionWorkspaceRelativePath(uri) || path.basename(String(uri.fsPath || uri.path || 'файл'))
    let patch = ''
    try {
      const gitExt = vscode.extensions.getExtension('vscode.git')
      const api = gitExt?.isActive ? gitExt.exports?.getAPI?.(1) : (await gitExt?.activate())?.getAPI?.(1)
      const repos = api?.repositories || []
      const repo = pickGitRepository(repos, uri)
      if (repo?.diffWithHEAD) patch = String(await repo.diffWithHEAD(uri.fsPath) || '').slice(0, 6000)
    } catch { /* ignore */ }
    const parts = [`Файл из набора изменений: ${relative}`]
    if (patch) parts.push(`Diff vs HEAD:\n${patch}`)
    else parts.push('Точный diff недоступен — опирайся на файл, Problems и незакоммиченные правки.')
    return `Разбери этот diff. ${companionClosing('analyze')}\n\n${parts.join('\n\n')}`
  }
  
  function companionTerminalMessage(getFailure) {
    const failure = typeof getFailure === 'function' ? getFailure() : undefined
    if (failure) return companionFailureMessage(failure, 'analyze')
    const term = vscode.window.activeTerminal
    if (!term) return `Активного терминала нет. ${companionClosing('analyze')}`
    return `Разбери вывод активного терминала «${term.name}». Если команды не падали — так и скажи. ${companionClosing('analyze')}`
  }
  
  function companionIdeExtras(getFailure, getRun) {
    return { getFailure, getRun }
  }
  
  function companionRunMessage(getRun, getFailure) {
    const run = typeof getRun === 'function' ? getRun() : undefined
    const failure = typeof getFailure === 'function' ? getFailure() : undefined
    const parts = []
    if (run) parts.push(`Текущая цель запуска: ${run.label || run.short} (${run.kind || 'run'})${run.command ? `\nКоманда: ${run.command}` : ''}`)
    else parts.push('Текущая цель запуска не выбрана.')
    if (failure) parts.push(companionFailureMessage(failure, 'analyze').replace(`\n\n${companionClosing('analyze')}`, ''))
    return `Разбери последний запуск IDE и риски цели Shift+F10.\n\n${parts.join('\n\n')}\n\n${companionClosing('analyze')}`
  }
  
  function companionDebugMessage(getFailure) {
    const session = vscode.debug?.activeDebugSession
    const editor = vscode.window.activeTextEditor
    const failure = typeof getFailure === 'function' ? getFailure() : undefined
    const parts = []
    if (session) parts.push(`Сессия отладки: ${session.name}${session.type ? ` · ${session.type}` : ''}`)
    else parts.push('Активной сессии отладки нет — разбери последнюю остановку по файлу и Problems.')
    if (editor) {
      const relative = vscode.workspace.asRelativePath(editor.document.uri, false).replace(/\\/g, '/')
      parts.push(`Кадр/файл: ${relative}:${editor.selection.active.line + 1}`)
      const line = editor.document.lineAt(editor.selection.active.line).text.trim()
      if (line) parts.push(`Строка: ${line.slice(0, 400)}`)
    }
    if (failure) parts.push(companionFailureMessage(failure, 'analyze').replace(`\n\n${companionClosing('analyze')}`, ''))
    return `Разбери текущую отладочную остановку. ${companionClosing('analyze')}\n\n${parts.join('\n\n')}`
  }
  
  async function companionBlameMessage(arg = {}) {
    arg = normalizeCompanionArg(arg)
    const editor = vscode.window.activeTextEditor
    const document = await resolveCompanionDocument(arg, editor)
    if (!document) return `Откройте файл для blame. ${companionClosing('analyze')}`
    const cwd = gitCwdForUri(document.uri)
    const gitRoot = await resolveGitRoot(cwd)
    const relative = pathRelativeToRoot(document.uri, gitRoot) || vscode.workspace.asRelativePath(document.uri, false).replace(/\\/g, '/')
    const line = Number(arg.selection?.start?.line) >= 0
      ? Number(arg.selection.start.line) + 1
      : (editor?.document?.uri?.toString() === document.uri.toString() ? editor.selection.active.line + 1 : 1)
    const source = document.lineAt(Math.max(0, line - 1)).text
    let blame = ''
    try {
      blame = await runGit(gitRoot || cwd, ['blame', '-w', '-L', `${line},${line}`, '--', relative])
    } catch (error) {
      blame = error instanceof Error ? error.message : String(error)
    }
    return `Разбери строку ${relative}:${line} по git blame.\n\nСтрока: ${source}\n\nBlame:\n${String(blame).trim().slice(0, 2000)}\n\n${companionClosing('analyze')}`
  }
  
  async function companionHistoryMessage(arg = {}) {
    arg = normalizeCompanionArg(arg)
    const editor = vscode.window.activeTextEditor
    const document = await resolveCompanionDocument(arg, editor)
    if (!document) return `Откройте файл, чтобы разобрать историю. ${companionClosing('analyze')}`
    const cwd = gitCwdForUri(document.uri)
    const gitRoot = await resolveGitRoot(cwd)
    const relative = pathRelativeToRoot(document.uri, gitRoot) || vscode.workspace.asRelativePath(document.uri, false).replace(/\\/g, '/')
    let log = ''
    try {
      log = await runGit(gitRoot || cwd, ['log', '-n', '10', '--date=short', '--pretty=format:%h %ad %an %s', '--', relative])
    } catch (error) {
      log = error instanceof Error ? error.message : String(error)
    }
    return `Разбери историю файла ${relative}. Какие коммиты рискованны и что проверить?\n\n${String(log).trim().slice(0, 4000) || 'История пуста.'}\n\n${companionClosing('analyze')}`
  }
  
  async function companionSavedMessage(arg = {}) {
    arg = normalizeCompanionArg(arg)
    const editor = vscode.window.activeTextEditor
    const document = await resolveCompanionDocument(arg, editor)
    if (!document) return `Откройте файл, чтобы сравнить с диском. ${companionClosing('analyze')}`
    const relative = vscode.workspace.asRelativePath(document.uri, false).replace(/\\/g, '/')
    if (!document.isDirty) return `Файл ${relative} сохранён. Несохранённых правок нет. ${companionClosing('analyze')}`
    let disk = ''
    try {
      disk = Buffer.from(await vscode.workspace.fs.readFile(document.uri)).toString('utf8')
    } catch {
      disk = ''
    }
    const current = document.getText()
    return `Разбери несохранённые правки ${relative}. Квест не создавай.\n\nБуфер (${current.length} символов):\n${current.slice(0, 3000)}\n\nДиск (${disk.length} символов):\n${disk.slice(0, 3000)}\n\n${companionClosing('analyze')}`
  }
  
  async function showCompanionInbox(provider, revealInfra, opts = {}) {
    const items = Array.isArray(provider?.boot?.companionInterventions) ? provider.boot.companionInterventions : []
    const picks = []
    if (items.length) {
      picks.push({ label: 'Сигналы', kind: vscode.QuickPickItemKind.Separator })
      for (const item of items.slice(0, 8)) {
        picks.push({
          label: `$(comment-unresolved) ${item.title || 'Сигнал'}`,
          description: item.level || '',
          detail: item.detail || '',
          intervention: item,
        })
      }
    }
    const failure = typeof opts.getFailure === 'function' ? opts.getFailure() : undefined
    const run = typeof opts.getRun === 'function' ? opts.getRun() : undefined
    const local = []
    if (failure) local.push({ label: `$(error) Сбой: ${failure.command}`, description: `код ${failure.exitCode}`, detail: failure.source || '', command: 'localAgent.askCompanionAboutTerminal' })
    if (run) local.push({ label: `$(play) Цель: ${run.label || run.short}`, description: run.kind || '', command: 'localAgent.askCompanionAboutRun' })
    if (vscode.debug?.activeDebugSession) local.push({ label: `$(debug-alt) Отладка: ${vscode.debug.activeDebugSession.name}`, command: 'localAgent.askCompanionAboutDebug' })
    if (vscode.window.activeTextEditor?.document.isDirty) local.push({ label: '$(edit) Несохранённые правки', command: 'localAgent.askCompanionAboutSaved' })
    if (local.length) {
      picks.push({ label: 'Сейчас в IDE', kind: vscode.QuickPickItemKind.Separator })
      picks.push(...local)
    }
    picks.push({ label: 'Действия', kind: vscode.QuickPickItemKind.Separator })
    picks.push(
      { label: '$(comment-discussion) Спросить о текущем файле', command: 'localAgent.askCompanionAbout' },
      { label: '$(wrench) Подготовить исправление', command: 'localAgent.askCompanionFix' },
      { label: '$(warning) Разобрать Problems', command: 'localAgent.askCompanionAboutProblems' },
      { label: '$(terminal) Разобрать терминал', command: 'localAgent.askCompanionAboutTerminal' },
      { label: '$(play) Разобрать запуск', command: 'localAgent.askCompanionAboutRun' },
      { label: '$(debug-alt) Разобрать отладку', command: 'localAgent.askCompanionAboutDebug' },
      { label: '$(diff) Разобрать diff', command: 'localAgent.askCompanionAboutDiff' },
      { label: '$(git-compare) Разобрать blame', command: 'localAgent.askCompanionAboutBlame' },
      { label: '$(history) Разобрать историю файла', command: 'localAgent.askCompanionAboutHistory' },
      { label: '$(comment-unresolved) Открыть диалог', command: 'localAgent.askCompanion' },
    )
    const selected = await vscode.window.showQuickPick(picks, {
      title: 'Компаньон — входящие',
      placeHolder: 'Сигнал, файл, терминал или diff — квест не стартует',
      matchOnDescription: true,
      matchOnDetail: true,
    })
    if (!selected) return
    if (selected.intervention) {
      if (selected.intervention.relatedPath) {
        try {
          await openWorkspaceFile(selected.intervention.relatedPath, selected.intervention.relatedLine)
        } catch { /* ignore */ }
      }
      if (selected.intervention.actionKind === 'companion_prompt' && selected.intervention.actionMessage) {
        return openCompanionChat(provider, revealInfra, { surface: 'peek', message: selected.intervention.actionMessage, send: false })
      }
      return openCompanionChat(provider, revealInfra, {
        surface: 'peek',
        message: `Разбери сигнал: ${selected.intervention.title || 'компаньон'}\n\n${selected.intervention.detail || ''}\n\n${companionClosing('ask')}`,
        send: false,
      })
    }
    if (selected.command) return vscode.commands.executeCommand(selected.command)
  }
  
  function workspaceFileUri(relativePath) {
    const folder = getActiveView()?.service?.workspaceFolder?.() || vscode.workspace.workspaceFolders?.[0]
    if (!folder || !relativePath || folder.uri.scheme !== 'file') return undefined
    const root = path.resolve(folder.uri.fsPath)
    const target = path.resolve(root, String(relativePath).replace(/\\/g, '/'))
    const relation = path.relative(root, target)
    if (!relation || relation === '..' || relation.startsWith(`..${path.sep}`) || path.isAbsolute(relation)) return undefined
    return vscode.Uri.file(target)
  }
  
  async function openWorkspaceFile(relativePath, line, openInEditorWindow = false) {
    const uri = workspaceFileUri(relativePath)
    if (!uri) {
      if (relativePath) throw new Error('Файл находится вне открытой рабочей папки.')
      return
    }
    if (openInEditorWindow) {
      const folderUri = getActiveView()?.service?.workspaceFolder?.()?.uri
      await vscode.commands.executeCommand(openInEditorWindow === 'main' ? 'point.openInMainEditor' : 'point.openInEditorWindow', {
        fileUri: uri.toJSON(),
        folderUri: folderUri?.toJSON(),
        line: Number(line) || undefined,
      })
      return
    }
    const editor = await vscode.window.showTextDocument(uri, { preview: false })
    const targetLine = Number(line)
    if (Number.isInteger(targetLine) && targetLine > 0) {
      const position = new vscode.Position(Math.max(0, targetLine - 1), 0)
      editor.selection = new vscode.Selection(position, position)
      editor.revealRange(new vscode.Range(position, position), vscode.TextEditorRevealType.InCenter)
    }
  }
  
  function workspaceRelativePath(rootPath, uri) {
    if (!uri || uri.scheme !== 'file') throw new Error('Можно прикреплять только локальные файлы.')
    const root = path.resolve(rootPath)
    const target = path.resolve(uri.fsPath)
    const relation = path.relative(root, target)
    if (!relation || relation === '..' || relation.startsWith(`..${path.sep}`) || path.isAbsolute(relation)) {
      throw new Error('Файл находится вне открытой рабочей папки.')
    }
    return relation.split(path.sep).join('/')
  }
  
  function workspaceRelativePathIfInside(rootPath, uri) {
    try {
      return workspaceRelativePath(rootPath, uri)
    } catch {
      return ''
    }
  }

  return {
    normalizeCompanionArg,
    companionClosing,
    nearbyEditorSnippet,
    resolveCompanionDocument,
    companionFailureMessage,
    companionAskMessage,
    openCompanionChat,
    createCompanionDecorations,
    createCompanionCodeLensProvider,
    companionCommandMarkdown,
    companionWorkspaceRelativePath,
    formatCompanionFailure,
    formatCompanionChatError,
    currentCompanionIdeContext,
    liveCompanionFocus,
    pathIsUnder,
    pickGitRepository,
    gitListsState,
    workspaceFolderForUri,
    pointWorkspaceFolder,
    localWorkspaceFolders,
    workspaceFolderByPath,
    restorePointWorkspaceRoot,
    resumeLastPointWorld,
    recentPointProjects,
    choosePointWorkspace,
    switchPointWorkspaceInPlace,
    chronicleWorkspaceFolder,
    gitCwdForUri,
    resolveGitRoot,
    pathRelativeToRoot,
    formatVcsError,
    companionDiffMessage,
    companionTerminalMessage,
    companionIdeExtras,
    companionRunMessage,
    companionDebugMessage,
    companionBlameMessage,
    companionHistoryMessage,
    companionSavedMessage,
    showCompanionInbox,
    workspaceFileUri,
    openWorkspaceFile,
    workspaceRelativePath,
    workspaceRelativePathIfInside,
  }
}

module.exports = { createCompanionController }
