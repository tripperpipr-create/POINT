// Индекс проекта: карта мира, её состояние в строке и сторож изменений.
//
// Семейство жило в `extension.js` сплошным куском на четыре сотни строк между
// разбором ошибок ядра и поддержкой языков — то есть между двумя чужими темами.
// Здесь оно одно, и видно целиком: что считается шумом, как состояние читается
// человеком, когда пересборка откладывается и чем её отменяют.
//
// Зависимости приходят снаружи по договорённости B («фабрика с DI», образцы —
// `ide-action-controller.js`, `connection-controller.js`): `describeCoreFailure`
// остаётся в `extension.js`, потому что на его таблице подсказок держится
// смоук, а `workspaceFileCache` приходит от контроллера навигации.

function createProjectIndex({
  vscode,
  fs,
  path,
  describeCoreFailure,
  workspaceFileCache,
  workspaceRelativePathIfInside,
}) {
  const INDEX_NOISE_DIRS = new Set([
    '.git', '.hg', '.svn', 'node_modules', 'vendor', 'dist', 'build', 'out',
    '.cache', '.gocache', '.tmp', '.idea', '.point', 'target', '.venv', 'venv', '__pycache__', '.next', '.turbo',
    'coverage', '.pnpm', '.pnpm-store', '.gradle', '.dart_tool', 'pods',
    '.yarn', 'bower_components', '.tox', '.mypy_cache', '.pytest_cache', '.ruff_cache',
    '.parcel-cache', '.svelte-kit',
  ])

  function formatIndexStatus(status, coreRunning) {
    if (!coreRunning) return { text: '$(database) Индекс', tooltip: 'Запустите локальное ядро Point, чтобы построить карту проекта', hide: true }
    if (!status || !status.state || status.state === 'not_built' || status.state === 'no_workspace') {
      return { text: '$(database) Индекс', tooltip: 'Построить локальный индекс проекта (Карта мира)' }
    }
    if (status.state === 'indexing') {
      const pct = Number(status.progressPercent ?? status.percent ?? status.progress)
      const pctLabel = Number.isFinite(pct) ? ` ${Math.max(0, Math.min(100, Math.round(pct)))}%` : ''
      const done = Number(status.processedFiles ?? status.done ?? status.current)
      const total = Number(status.totalFiles ?? status.total)
      const countLabel = Number.isFinite(done) && Number.isFinite(total) && total > 0 ? ` ${done}/${total}` : ''
      return { text: `$(sync~spin) Индекс${pctLabel}${countLabel}`, tooltip: 'Point обновляет карту проекта' }
    }
    if (status.state === 'pending') {
      return { text: '$(database) Индекс…', tooltip: 'Файлы изменились — индекс скоро обновится' }
    }
    if (status.state === 'stale') {
      return { text: '$(database) Индекс · устарел', tooltip: 'Индекс устарел — обновится автоматически или нажмите, чтобы перестроить' }
    }
    if (status.state === 'error') {
      return { text: '$(database) Индекс · ошибка', tooltip: 'Перестроить индекс после ошибки' }
    }
    if (status.state === 'ready') {
      const mode = status.mode === 'incremental' ? 'точечно' : 'полный проход'
      if (status.partial) {
        const reason = {
          entries: `обход ограничен ${status.maxEntries || 0} элементами`,
          files: `достигнут предел ${status.maxFiles || 0} файлов`,
          bytes: `достигнут предел ${Math.max(1, Math.round(Number(status.maxBytes || 0) / (1024 * 1024)))} МБ исходников`,
          chunks: `достигнут предел ${status.maxChunks || 0} фрагментов`,
        }[status.limitReason] || 'достигнут безопасный предел индекса'
        return {
          text: `$(warning) Индекс · ${status.files || 0} частично`,
          tooltip: `Индекс работает, но охватывает часть проекта: ${reason}. Поиск может не найти файлы за пределом; нажмите для подробностей или перестройки.`,
        }
      }
      return {
        text: `$(database) ${status.files || 0}`,
        tooltip: `Индекс готов (${mode}) · ${status.files || 0} файл. · ${status.chunks || 0} фрагм. · ${status.symbols || 0} симв.`,
        hide: true,
      }
    }
    return { text: `$(database) ${status.state}`, tooltip: 'Статус локального индекса Point' }
  }

  async function rebuildProjectIndex(service, indexStatus, options = {}) {
    const allowStart = options.allowStart !== false
    if (allowStart) await service.ensureStarted()
    else if (service.state !== 'running' || !service.baseUrl) throw new Error('Локальное ядро остановлено.')
    const result = await service.request('/api/index/rebuild', { method: 'POST', body: '{}', timeoutMs: 120_000, allowStart })
    if (indexStatus) applyIndexStatus(indexStatus, result, true)
    return result
  }

  function applyIndexStatus(item, status, coreRunning) {
    const view = formatIndexStatus(status, coreRunning)
    item.text = view.text
    item.tooltip = view.tooltip
    item.accessibilityInformation = { label: view.tooltip || view.text }
    if (view.hide) item.hide()
    else item.show()
  }

  function indexAutoConfig() {
    const cfg = vscode.workspace.getConfiguration('localAgent')
    const debounceRaw = Number(cfg.get('indexDebounceMs', 5000))
    return {
      autoIndex: cfg.get('autoIndex', true) !== false,
      debounceMs: Number.isFinite(debounceRaw) ? Math.max(200, Math.min(60_000, debounceRaw)) : 5000,
    }
  }

  function indexScheduleWaitMs(reason, dirtyCount, debounceMs) {
    const base = Number.isFinite(debounceMs) ? Math.max(200, Math.min(60_000, debounceMs)) : 5000
    const count = Number(dirtyCount) || 0
    if (reason === 'save' || count <= 2) return Math.min(450, base)
    if (count >= 16) return base
    return Math.min(1200, base)
  }

  function applyIndexDirty(changed, deleted, relative, kind) {
    const path = String(relative || '').replace(/\\/g, '/')
    if (!path) return
    if (kind === 'delete') {
      deleted.add(path)
      changed.delete(path)
      for (const item of [...changed]) {
        if (item === path || item.startsWith(`${path}/`)) {
          changed.delete(item)
          deleted.add(item)
        }
      }
      return
    }
    changed.add(path)
    deleted.delete(path)
  }

  function isIndexNoiseUri(uri) {
    if (!uri || uri.scheme !== 'file') return true
    const relative = vscode.workspace.asRelativePath(uri, false)
    if (!relative || relative === uri.fsPath) return false
    return relative.split(/[/\\]/).some(part => INDEX_NOISE_DIRS.has(part.toLowerCase()))
  }

  function createProjectIndexController(service, indexStatus, hooks = {}) {
    let debounceTimer
    let fsEventTimer
    let rebuildPromise
    let indexGeneration = 0
    let pendingRebuild = false
    let notifyAfter = false
    let forceFullRebuild = false
    let lastKnown
    let disposed = false
    let watcher
    let watcherActive = false
    const dirtyChanged = new Set()
    const dirtyDeleted = new Set()
    const INDEX_UPDATE_LIMIT = 48

    const noteDirty = (uri, kind) => {
      if (!uri || uri.scheme !== 'file') return false
      const folder = service.workspaceFolder()
      const relative = folder?.uri?.scheme === 'file'
        ? workspaceRelativePathIfInside(folder.uri.fsPath, uri)
        : ''
      if (!relative) return false
      applyIndexDirty(dirtyChanged, dirtyDeleted, relative, kind)
      workspaceFileCache.apply(uri, kind)
      return true
    }

    const takeDirty = () => {
      const changed = [...dirtyChanged]
      const deleted = [...dirtyDeleted]
      dirtyChanged.clear()
      dirtyDeleted.clear()
      return { changed, deleted }
    }

    const setBusy = (busy) => {
      if (typeof hooks.onBusy === 'function') {
        try { hooks.onBusy(Boolean(busy)) } catch { /* ignore */ }
      }
    }

    const paint = (status, coreRunning = service.state === 'running') => {
      if (status && status.state !== 'indexing' && status.state !== 'pending') lastKnown = status
      applyIndexStatus(indexStatus, status, coreRunning)
      setBusy(Boolean(rebuildPromise) || status?.state === 'indexing' || status?.state === 'pending')
      if (typeof hooks.onPaint === 'function') {
        try { hooks.onPaint(status, coreRunning) } catch { /* ignore */ }
      }
    }

    const refreshFromBootstrap = async () => {
      if (service.state !== 'running') {
        paint(undefined, false)
        return undefined
      }
      try {
        const status = await service.request('/api/index/status', { allowStart: false })
        if (!rebuildPromise && !debounceTimer) paint(status, true)
        return status
      } catch {
        if (!rebuildPromise && !debounceTimer) paint({ state: 'error' }, true)
        return { state: 'error' }
      }
    }

    const notifyUpdated = async (status) => {
      if (typeof hooks.onUpdated === 'function') {
        try { await hooks.onUpdated(status) } catch { /* provider refresh is best-effort */ }
      }
    }

    const invalidateRemote = async () => {
      if (service.state !== 'running') return undefined
      try {
        return await service.request('/api/index/invalidate', { method: 'POST', body: '{}', allowStart: false })
      } catch {
        return undefined
      }
    }

    const runRebuild = async ({ notify = false } = {}) => {
      if (disposed || service.state !== 'running') return lastKnown
      if (notify) notifyAfter = true
      if (rebuildPromise) {
        pendingRebuild = true
        paint({ ...(lastKnown || {}), state: 'indexing' }, true)
        return rebuildPromise
      }
      const generation = indexGeneration
      rebuildPromise = (async () => {
        let result = lastKnown
        try {
          // Status bar index item is the only progress chrome — no Window progress duplicate.
          do {
            if (disposed || generation !== indexGeneration || service.state !== 'running') return result
            pendingRebuild = false
            paint({ ...(lastKnown || {}), state: 'indexing' }, true)
            const dirty = takeDirty()
            const hasDirty = dirty.changed.length + dirty.deleted.length > 0
            if (!forceFullRebuild && !hasDirty && lastKnown?.state === 'ready') {
              // Spurious schedule (no paths) — do not kick a full rebuild.
              pendingRebuild = false
              paint(lastKnown, true)
              result = lastKnown
              continue
            }
            service.hostLog('info', `[index] rebuild start incremental_candidate=${!forceFullRebuild} changed=${dirty.changed.length} deleted=${dirty.deleted.length}`)
            const incremental = !forceFullRebuild
              && (lastKnown?.state === 'ready' || lastKnown?.state === 'stale')
              && hasDirty
              && dirty.changed.length + dirty.deleted.length <= INDEX_UPDATE_LIMIT
            forceFullRebuild = false
            try {
              result = incremental
                ? await service.request('/api/index/update', {
                  method: 'POST',
                  body: JSON.stringify(dirty),
                  timeoutMs: 60_000,
                  allowStart: false,
                })
                : await rebuildProjectIndex(service, undefined, { allowStart: false })
            } catch (error) {
              if (!incremental) throw error
              for (const path of dirty.changed) applyIndexDirty(dirtyChanged, dirtyDeleted, path, 'change')
              for (const path of dirty.deleted) applyIndexDirty(dirtyChanged, dirtyDeleted, path, 'delete')
              forceFullRebuild = true
              result = await rebuildProjectIndex(service, undefined, { allowStart: false })
            }
            paint(result, true)
            service.hostLog('info', `[index] rebuild done state=${result?.state || '-'} files=${result?.files ?? '-'} chunks=${result?.chunks ?? '-'} duration_ms=${result?.durationMs ?? '-'}`)
            await notifyUpdated(result)
          } while (pendingRebuild && !disposed && generation === indexGeneration && service.state === 'running')
          if (notifyAfter) {
            notifyAfter = false
            void vscode.window.showInformationMessage('Индекс проекта обновлён.')
          }
          return result
        } catch (error) {
          const wantNotify = notifyAfter
          notifyAfter = false
          paint({ ...(lastKnown || {}), state: 'error' }, service.state === 'running')
          const detail = describeCoreFailure(error)
          const choice = await vscode.window.showErrorMessage(
            `Индексация: ${detail}`,
            'Перестроить снова',
            'Проблемы',
            'Журнал ядра',
          )
          if (choice === 'Перестроить снова') {
            void runRebuild({ notify: true }).catch(() => {})
          } else if (choice === 'Проблемы') {
            await vscode.commands.executeCommand('workbench.actions.view.problems')
          } else if (choice === 'Журнал ядра') {
            await vscode.commands.executeCommand('localAgent.showCoreChronicle')
          }
          if (wantNotify) throw error
          return { state: 'error' }
        } finally {
          rebuildPromise = undefined
          setBusy(false)
        }
      })()
      return rebuildPromise
    }

    const scheduleRebuild = (reason = 'change') => {
      if (disposed || !vscode.workspace.isTrusted || !indexAutoConfig().autoIndex) return
      if (service.state !== 'running') return
      const alreadyQueued = Boolean(debounceTimer) || Boolean(rebuildPromise)
      if (debounceTimer) clearTimeout(debounceTimer)
      paint({ ...(lastKnown || {}), state: 'pending' }, true)
      if (!alreadyQueued) {
        void invalidateRemote().then(status => {
          if (!rebuildPromise && debounceTimer) {
            paint(status || { ...(lastKnown || {}), state: 'stale' }, true)
          }
        })
      }
      const wait = indexScheduleWaitMs(reason, dirtyChanged.size + dirtyDeleted.size, indexAutoConfig().debounceMs)
      debounceTimer = setTimeout(() => {
        debounceTimer = undefined
        void runRebuild({ notify: false }).catch(() => {})
      }, wait)
    }

    const onFsEvent = (uri, kind = 'change') => {
      if (isIndexNoiseUri(uri)) return
      if (!noteDirty(uri, kind)) return
      if (fsEventTimer) clearTimeout(fsEventTimer)
      fsEventTimer = setTimeout(() => {
        fsEventTimer = undefined
        scheduleRebuild('fs')
      }, 300)
    }

    const startWatcher = () => {
      if (disposed || watcherActive || !indexAutoConfig().autoIndex) return
      watcher = vscode.workspace.createFileSystemWatcher('**/*')
      watcher.onDidCreate(uri => onFsEvent(uri, 'change'))
      watcher.onDidChange(uri => onFsEvent(uri, 'change'))
      watcher.onDidDelete(uri => onFsEvent(uri, 'delete'))
      watcherActive = true
    }

    const stopWatcher = () => {
      if (watcher) {
        watcher.dispose()
        watcher = undefined
      }
      watcherActive = false
    }

    const saveSub = vscode.workspace.onDidSaveTextDocument(document => {
      if (isIndexNoiseUri(document.uri)) return
      if (!noteDirty(document.uri, 'change')) return
      scheduleRebuild('save')
    })

    const configSub = vscode.workspace.onDidChangeConfiguration(event => {
      if (!event.affectsConfiguration('localAgent.autoIndex') && !event.affectsConfiguration('localAgent.indexDebounceMs')) return
      if (indexAutoConfig().autoIndex && service.state === 'running') {
        startWatcher()
        void ensureReady()
      } else {
        stopWatcher()
      }
    })

    async function ensureReady() {
      if (disposed || !vscode.workspace.isTrusted || !indexAutoConfig().autoIndex) return
      if (rebuildPromise) return rebuildPromise
      try {
        await service.ensureStarted()
        startWatcher()
        const status = await refreshFromBootstrap()
        if (!status || ['not_built', 'stale', 'no_workspace', 'error'].includes(status.state)) {
          await runRebuild({ notify: false })
        }
      } catch {
        paint(undefined, service.state === 'running')
      }
    }

    return {
      ensureReady,
      refresh: refreshFromBootstrap,
      rebuildNow: (options) => {
        forceFullRebuild = true
        dirtyChanged.clear()
        dirtyDeleted.clear()
        return runRebuild(options)
      },
      scheduleRebuild,
      startWatcher,
      markCoreStopped: () => {
        indexGeneration += 1
        pendingRebuild = false
        dirtyChanged.clear()
        dirtyDeleted.clear()
        forceFullRebuild = false
        lastKnown = undefined
        if (fsEventTimer) {
          clearTimeout(fsEventTimer)
          fsEventTimer = undefined
        }
        if (debounceTimer) {
          clearTimeout(debounceTimer)
          debounceTimer = undefined
        }
        stopWatcher()
        paint(undefined, false)
      },
      dispose: () => {
        disposed = true
        if (fsEventTimer) clearTimeout(fsEventTimer)
        fsEventTimer = undefined
        if (debounceTimer) clearTimeout(debounceTimer)
        debounceTimer = undefined
        stopWatcher()
        saveSub.dispose()
        configSub.dispose()
      },
    }
  }

  return {
    INDEX_NOISE_DIRS,
    formatIndexStatus,
    applyIndexStatus,
    indexAutoConfig,
    indexScheduleWaitMs,
    applyIndexDirty,
    isIndexNoiseUri,
    createProjectIndexController,
  }
}

module.exports = { createProjectIndex }
