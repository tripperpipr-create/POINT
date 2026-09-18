// Пассивные наблюдения IDE: что человек открыл, что сломалось, что показал
// терминал.
//
// Это единственный источник, из которого помощник узнаёт о работе, не
// спрашивая. Наблюдения именно пассивные: они ничего не запускают, не
// переключают репозиторий и не трогают состояние Git — иначе «помощник
// смотрит» превратилось бы в «помощник вмешивается».
//
// Модуль был написан фабрикой с самого начала и лежал в extension.js только
// по привычке. Зависимости переданы явно: работа с путями рабочей области и
// выбор репозитория нужны и другим местам.

const vscode = require('vscode')
const path = require('path')

function createIDEObservations({ companionWorkspaceRelativePath, pickGitRepository, stripTerminalControlSequences, workspaceRelativePathIfInside }) {
  function createIDEObservationController(context, service, output, getProvider, onFailure) {
    let diagnosticsTimer
    let diagnosticsSignature = ''
    let lastFailure
    const terminalCaptures = new WeakMap()

    const refreshCompanionLive = async () => {
      const provider = getProvider()
      if (!provider || service.state !== 'running') return
      try {
        if (typeof provider.refreshLiveCompanionInterventions === 'function') {
          await provider.refreshLiveCompanionInterventions()
        } else if (provider.hubVisible?.()) {
          await provider.refresh()
        }
      } catch { /* next ordinary refresh will pick up observations */ }
    }

    let hubRefreshTimer
    let hubRefreshPending = false
    const scheduleHubRefresh = () => {
      // Observations land every diagnostics/terminal tick — never full-bootstrap each time.
      hubRefreshPending = true
      if (hubRefreshTimer) return
      hubRefreshTimer = setTimeout(() => {
        hubRefreshTimer = undefined
        if (!hubRefreshPending) return
        hubRefreshPending = false
        void refreshCompanionLive()
      }, 4_000)
    }

    const send = async batch => {
      if (service.state !== 'running' || !vscode.workspace.isTrusted || !vscode.workspace.workspaceFolders?.length) return false
      try {
        const focus = companionWorkspaceRelativePath(vscode.window.activeTextEditor?.document?.uri)
        await service.request('/api/ide/observations', {
          method: 'POST',
          body: JSON.stringify({ ...batch, focusPath: focus || undefined }),
          timeoutMs: 10_000,
        })
        service.hostLog('debug', `[ide-observation] kind=${batch.kind || '-'} items=${Array.isArray(batch.items) ? batch.items.length : 0} replace=${Boolean(batch.replace)} focus=${focus || '-'}`)
        scheduleHubRefresh()
        return true
      } catch (error) {
        service.hostLog('warn', `[ide-observation] error kind=${batch.kind || '-'}: ${error instanceof Error ? error.message : String(error)}`)
        return false
      }
    }

    const diagnosticLevel = severity => {
      if (severity === vscode.DiagnosticSeverity.Error) return 'error'
      if (severity === vscode.DiagnosticSeverity.Warning) return 'warning'
      return 'info'
    }

    const syncDiagnostics = async () => {
      if (service.state !== 'running') return
      const items = []
      const selectedRoot = service.workspaceFolder()?.uri?.fsPath
      if (!selectedRoot) return
      for (const [uri, diagnostics] of vscode.languages.getDiagnostics()) {
        if (uri.scheme !== 'file') continue
        const relative = workspaceRelativePathIfInside(selectedRoot, uri)
        if (!relative) continue
        for (const diagnostic of diagnostics) {
          if (diagnostic.severity !== vscode.DiagnosticSeverity.Error && diagnostic.severity !== vscode.DiagnosticSeverity.Warning) continue
          const code = typeof diagnostic.code === 'object' ? diagnostic.code?.value : diagnostic.code
          const related = (diagnostic.relatedInformation || []).slice(0, 3).map(item => {
            const relatedPath = workspaceRelativePathIfInside(selectedRoot, item.location.uri)
            return relatedPath ? `${relatedPath}:${item.location.range.start.line + 1} ${item.message}` : ''
          }).filter(Boolean)
          items.push({
            source: String(diagnostic.source || 'Problems').slice(0, 200),
            level: diagnosticLevel(diagnostic.severity),
            summary: String(diagnostic.message || 'Диагностика редактора').slice(0, 500),
            detail: [`code=${code ?? ''}`, ...related].filter(Boolean).join('\n').slice(0, 16 * 1024),
            path: relative,
            line: diagnostic.range.start.line + 1,
          })
          if (items.length >= 200) break
        }
        if (items.length >= 200) break
      }
      items.sort((a, b) => (a.level === b.level ? `${a.path}:${a.line}:${a.summary}`.localeCompare(`${b.path}:${b.line}:${b.summary}`) : a.level === 'error' ? -1 : 1))
      let signature = String(items.length)
      for (const item of items) signature += `|${item.level}:${item.path}:${item.line}:${item.summary}`
      if (signature === diagnosticsSignature) return
      if (await send({ kind: 'diagnostic', replace: true, items })) diagnosticsSignature = signature
    }

    const scheduleDiagnostics = () => {
      if (diagnosticsTimer) clearTimeout(diagnosticsTimer)
      diagnosticsTimer = setTimeout(() => {
        diagnosticsTimer = undefined
        void syncDiagnostics()
      }, 350)
    }

    const startTerminalCapture = event => {
      const chunks = []
      let size = 0
      const promise = (async () => {
        try {
          for await (const chunk of event.execution.read()) {
            const clean = stripTerminalControlSequences(chunk)
            if (!clean) continue
            chunks.push(clean)
            size += clean.length
            while (size > 32 * 1024 && chunks.length > 1) size -= chunks.shift().length
          }
        } catch { /* shell integration may stop before the reader drains */ }
        return chunks.join('')
      })()
      terminalCaptures.set(event.execution, { promise })
    }

    const endTerminalCapture = async event => {
      if (service.state !== 'running') return
      const capture = terminalCaptures.get(event.execution)
      const raw = capture ? await capture.promise : ''
      terminalCaptures.delete(event.execution)
      const exitCode = Number.isInteger(event.exitCode) ? event.exitCode : -1
      const failed = exitCode !== 0
      const detail = failed ? stripTerminalControlSequences(raw).trim().slice(-(16 * 1024)) : ''
      const command = String(event.execution.commandLine?.value || '').trim().slice(0, 4096)
      if (!command && !detail) return
      if (failed) {
        lastFailure = { kind: 'terminal', command, exitCode, detail, source: String(event.terminal?.name || 'Terminal') }
        if (typeof onFailure === 'function') onFailure(lastFailure)
        // Point · shells stay quiet: observation + companion status/inbox, no modal spam.
      }
      await send({
        kind: 'terminal',
        items: [{
          source: String(event.terminal?.name || 'Terminal').slice(0, 200),
          level: failed ? 'error' : 'info',
          summary: failed ? `Команда завершилась с кодом ${exitCode}` : 'Команда завершилась успешно',
          detail,
          command,
          exitCode,
        }],
      })
    }

    const recordTask = async event => {
      if (service.state !== 'running') return
      const exitCode = Number.isInteger(event.exitCode) ? event.exitCode : -1
      const name = String(event.execution?.task?.name || event.execution?.task?.definition?.type || 'IDE Task').slice(0, 200)
      if (exitCode !== 0) {
        lastFailure = { kind: 'task', command: name, exitCode, detail: '', source: 'VS Code Tasks' }
        if (typeof onFailure === 'function') onFailure(lastFailure)
      }
      await send({
        kind: 'task',
        items: [{
          source: 'VS Code Tasks',
          level: exitCode === 0 ? 'info' : 'error',
          summary: exitCode === 0 ? `Задача «${name}» завершена` : `Задача «${name}» завершилась с ошибкой`,
          command: name,
          exitCode,
        }],
      })
    }

    let runSignature = ''
    let debugSignature = ''
    let scmSignature = ''
    let scmTimer
    let scmGitBound = false

    const scheduleScmObservation = () => {
      if (scmTimer) clearTimeout(scmTimer)
      scmTimer = setTimeout(() => {
        scmTimer = undefined
        void syncScmObservation()
      }, 500)
    }

    const syncRunObservation = async getRun => {
      if (service.state !== 'running') return
      const run = typeof getRun === 'function' ? getRun() : undefined
      const label = String(run?.label || run?.short || '').trim()
      const signature = label || ''
      if (signature === runSignature) return
      runSignature = signature
      if (!label) {
        await send({ kind: 'run', replace: true, items: [] })
        return
      }
      await send({
        kind: 'run',
        replace: true,
        items: [{
          source: 'Run',
          level: 'info',
          summary: label.slice(0, 500),
          detail: String(run?.detail || run?.command || '').slice(0, 16 * 1024),
        }],
      })
    }
    const syncDebugObservation = async () => {
      if (service.state !== 'running') return
      const session = vscode.debug?.activeDebugSession
      const name = String(session?.name || '').trim()
      const signature = name || ''
      if (signature === debugSignature) return
      debugSignature = signature
      if (!name) {
        await send({ kind: 'debug', replace: true, items: [] })
        return
      }
      await send({
        kind: 'debug',
        replace: true,
        items: [{
          source: String(session?.type || 'Debug').slice(0, 200),
          level: 'info',
          summary: name.slice(0, 500),
          detail: `type=${session?.type || ''}`,
        }],
      })
    }
    const syncScmObservation = async () => {
      if (service.state !== 'running') return
      const selectedRoot = service.workspaceFolder()?.uri?.fsPath
      if (!selectedRoot) return
      const unsaved = vscode.workspace.textDocuments.filter(doc => doc.isDirty
        && doc.uri.scheme === 'file'
        && Boolean(workspaceRelativePathIfInside(selectedRoot, doc.uri))).length
      let branch = ''
      let changed = 0
      let staged = 0
      let untracked = 0
      let ahead = 0
      let behind = 0
      let samplePath = ''
      try {
        const gitExt = vscode.extensions.getExtension('vscode.git')
        // Passive IDE observations must never activate Git or switch the user's
        // left tool window from Inventory to SCM. Once Git is already active
        // (because the user opened Git or ran a Git action), reuse its shared API.
        const api = gitExt?.isActive ? gitExt.exports?.getAPI?.(1) : undefined
        const folderUri = service.workspaceFolder()?.uri
        const repo = pickGitRepository(api?.repositories || [], folderUri || vscode.window.activeTextEditor?.document?.uri)
        if (repo?.state) {
          branch = String(repo.state.HEAD?.name || '').trim()
          ahead = Number(repo.state.HEAD?.ahead || 0) || 0
          behind = Number(repo.state.HEAD?.behind || 0) || 0
          const wt = Array.isArray(repo.state.workingTreeChanges) ? repo.state.workingTreeChanges : []
          const idx = Array.isArray(repo.state.indexChanges) ? repo.state.indexChanges : []
          // vscode.Status.UNTRACKED === 7
          untracked = wt.filter(item => Number(item?.status) === 7).length
          changed = wt.filter(item => Number(item?.status) !== 7).length
          staged = idx.length
          const sample = wt.find(item => item?.uri) || idx.find(item => item?.uri)
          if (sample?.uri) samplePath = companionWorkspaceRelativePath(sample.uri) || ''
        }
        if (!scmGitBound && api) {
          scmGitBound = true
          const bindRepo = nextRepo => {
            if (!nextRepo?.state?.onDidChange) return
            context.subscriptions.push(nextRepo.state.onDidChange(() => scheduleScmObservation()))
          }
          for (const item of api.repositories || []) bindRepo(item)
          if (typeof api.onDidOpenRepository === 'function') {
            context.subscriptions.push(api.onDidOpenRepository(bindRepo))
          }
        }
      } catch { /* git extension optional */ }
      const gitDirty = changed + staged + untracked
      const signature = `${changed}|${staged}|${untracked}|${unsaved}|${branch}|${ahead}|${behind}|${samplePath}`
      if (signature === scmSignature) return
      scmSignature = signature
      if (!gitDirty && !unsaved && !branch) {
        await send({ kind: 'scm', replace: true, items: [] })
        return
      }
      if (!gitDirty && !unsaved) {
        // Branch-only signal is noise for speak; clear SCM observation.
        await send({ kind: 'scm', replace: true, items: [] })
        return
      }
      const level = (gitDirty >= 8 || (behind > 0 && gitDirty > 0)) ? 'warning' : 'info'
      const parts = []
      if (gitDirty > 0) parts.push(`${gitDirty} в git`)
      if (unsaved > 0) parts.push(`${unsaved} несохранённых`)
      if (behind > 0) parts.push(`behind ${behind}`)
      if (branch) parts.push(branch)
      const summary = parts.join(' · ') || 'SCM'
      await send({
        kind: 'scm',
        replace: true,
        items: [{
          source: 'SCM',
          level,
          summary: summary.slice(0, 500),
          detail: `changed=${changed}; staged=${staged}; untracked=${untracked}; unsaved=${unsaved}; branch=${branch || '—'}; ahead=${ahead}; behind=${behind}`,
          path: samplePath || undefined,
        }],
      })
    }

    const subscriptions = [
      vscode.languages.onDidChangeDiagnostics(scheduleDiagnostics),
      { dispose: () => {
        if (diagnosticsTimer) clearTimeout(diagnosticsTimer)
        if (hubRefreshTimer) clearTimeout(hubRefreshTimer)
        if (scmTimer) clearTimeout(scmTimer)
      } },
    ]
    if (typeof vscode.window.onDidStartTerminalShellExecution === 'function' && typeof vscode.window.onDidEndTerminalShellExecution === 'function') {
      subscriptions.push(
        vscode.window.onDidStartTerminalShellExecution(startTerminalCapture),
        vscode.window.onDidEndTerminalShellExecution(event => { void endTerminalCapture(event) }),
      )
    }
    if (vscode.debug?.onDidStartDebugSession) {
      subscriptions.push(
        vscode.debug.onDidStartDebugSession(() => { void syncDebugObservation() }),
        vscode.debug.onDidTerminateDebugSession(() => { void syncDebugObservation() }),
      )
    }
    subscriptions.push(
      vscode.workspace.onDidSaveTextDocument(() => scheduleScmObservation()),
      vscode.workspace.onDidCloseTextDocument(() => scheduleScmObservation()),
    )
    context.subscriptions.push(...subscriptions)
    return {
      syncDiagnostics,
      recordTask,
      syncRunObservation,
      syncDebugObservation,
      syncScmObservation,
      lastFailure: () => lastFailure,
    }
  }

  return { createIDEObservationController }
}

module.exports = { createIDEObservations }
