const Module = require('module')
const path = require('path')

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds))
const originalLoad = Module._load
let workspaceFileExclude = ''
const executedCommands = []
const workspace = {
  isTrusted: true,
  workspaceFolders: [{ name: 'smoke', uri: { scheme: 'file', fsPath: process.cwd() } }],
  getConfiguration: () => ({ get: (_key, fallback) => fallback }),
  findFiles: async (_include, exclude) => {
    workspaceFileExclude = String(exclude || '')
    return [{ fsPath: path.join(process.cwd(), 'a.go'), path: '/a.go', scheme: 'file' }]
  },
  asRelativePath: uri => path.relative(process.cwd(), uri.fsPath || uri.path || '').replace(/\\/g, '/'),
  onDidChangeWorkspaceFolders: () => ({ dispose() {} }),
  onDidChangeConfiguration: () => ({ dispose() {} }),
  onDidSaveTextDocument: () => ({ dispose() {} }),
  createFileSystemWatcher: () => ({
    onDidCreate() { return { dispose() {} } },
    onDidChange() { return { dispose() {} } },
    onDidDelete() { return { dispose() {} } },
    dispose() {},
  }),
}
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      workspace,
      Uri: { joinPath: (...parts) => ({ fsPath: parts.join('/'), toString: () => parts.join('/') }) },
      window: {
        createQuickPick: () => ({
          title: '', placeholder: '', items: [], busy: false,
          onDidChangeValue: () => ({ dispose() {} }),
          onDidAccept: () => ({ dispose() {} }),
          onDidHide: () => ({ dispose() {} }),
          show() {}, hide() {}, dispose() {},
        }),
        withProgress: async (_opts, task) => task({ report() {} }),
        showInformationMessage: async () => undefined,
        showErrorMessage: async () => undefined,
      },
      ProgressLocation: { Window: 1, Notification: 2 },
      commands: { executeCommand: async command => { executedCommands.push(command); return [] } },
      StatusBarAlignment: { Left: 1 },
      ThemeColor: class { constructor(id) { this.id = id } },
    }
  }
  return originalLoad.call(this, request, parent, isMain)
}

async function main() {
  const extension = require(path.resolve(__dirname, '..', 'vscode-extension', 'extension.js'))
  Module._load = originalLoad
  const {
    BackendService,
    AgentViewProvider,
    createWorkspaceFileCache,
    createProjectIndexController,
    formatIndexStatus,
    isIndexNoiseUri,
    indexScheduleWaitMs,
    applyIndexDirty,
  } = extension.__test

  const backend = new BackendService({
    globalStorageUri: { fsPath: path.join(process.cwd(), '.tmp-point-smoke'), scheme: 'file', path: '/.tmp-point-smoke' },
    extensionUri: { fsPath: path.join(process.cwd(), 'vscode-extension'), scheme: 'file' },
    subscriptions: [],
  }, { append() {}, appendLine() {} }, () => {})
  let starts = 0
  backend.startCore = async () => { starts += 1; await delay(40); backend.state = 'running'; backend.baseUrl = 'http://127.0.0.1:1' }
  await Promise.all([backend.ensureStarted(), backend.ensureStarted(), backend.ensureStarted()])
  if (starts !== 1) throw new Error(`Concurrent starts were not coalesced: ${starts}`)

  let concurrent = 0
  let maxConcurrent = 0
  let polls = 0
  const posted = []
  const service = {
    state: 'running',
    request: async route => {
      if (!route.startsWith('/api/runs/')) return {}
      polls += 1
      concurrent += 1
      maxConcurrent = Math.max(maxConcurrent, concurrent)
      await delay(1_800)
      concurrent -= 1
      return { run: { id: 'quest-1', status: 'running' }, events: [], approvals: [], patches: [] }
    },
  }
  const provider = new AgentViewProvider({ subscriptions: [] }, service, {}, {
    onAgentBusy: () => {},
  })
  provider.view = { visible: true, webview: { postMessage: message => posted.push(message) } }
  provider.postState()
  provider.postState()
  if (posted.filter(message => message.type === 'state').length !== 1) throw new Error('Duplicate state was sent to the webview')
  provider.view.visible = false
  provider.selectedTab = 'history'
  provider.postState()
  if (posted.length !== 1) throw new Error('A hidden webview received a background state update')
  provider.postState(true)
  if (posted.length !== 2) throw new Error('A forced initial state was not delivered')

  provider.panel = { dispose() {}, visible: true }
  provider.scheduleHubGarbageCollection()
  await delay(900)
  if (executedCommands.includes('point.collectHubGarbage')) throw new Error('Hub memory collection ran while the panel was open')
  provider.panel = undefined
  provider.scheduleHubGarbageCollection()
  await delay(900)
  const garbageCollections = executedCommands.filter(command => command === 'point.collectHubGarbage').length
  if (garbageCollections !== 1) throw new Error(`Closed Hub did not collect workbench garbage once: ${garbageCollections}`)
  if (!posted.some(message => message.type === 'collectGarbage')) throw new Error('Closed Hub did not release retained webview garbage')

  provider.view.visible = true
  provider.activeRunId = 'quest-1'
  provider.details = { run: { id: 'quest-1', status: 'running' }, events: [], approvals: [], patches: [] }
  provider.startPolling()
  await delay(3_000)
  provider.dispose()
  if (polls !== 1 || maxConcurrent !== 1) throw new Error(`Quest polling overlapped: polls=${polls}, concurrent=${maxConcurrent}`)

  let nextExecutionLaunched = 0
  const backgroundRoutes = []
  const backgroundRuntime = () => ({
    flowRuns: [{ id: 'flow-run', status: 'waiting_approval' }],
    executions: [{
      id: 'execution-two', questId: 'quest-flow', flowRunId: 'flow-run', projectAgentId: 'agent-two',
      status: nextExecutionLaunched ? 'running' : 'pending', runId: nextExecutionLaunched ? 'run-two' : '',
    }],
  })
  const backgroundService = {
    state: 'running',
    hostLog() {},
    request: async (route, options = {}) => {
      backgroundRoutes.push(route)
      if (route === '/api/runs/run-one') {
        return { run: { id: 'run-one', status: 'completed' }, events: [], approvals: [], patches: [] }
      }
      if (route === '/api/state/runtime') return backgroundRuntime()
      if (route === '/api/companion/live') return {}
      if (route === '/api/executions/execution-two/launch') {
        const body = JSON.parse(options.body || '{}')
        if (body.apiKey !== 'background-secret') throw new Error('Background coordinator dropped the SecretStorage credential')
        nextExecutionLaunched += 1
        return { id: 'run-two', status: 'running' }
      }
      if (route === '/api/runs/run-two') {
        return { run: { id: 'run-two', status: 'running' }, events: [], approvals: [], patches: [] }
      }
      return {}
    },
  }
  const backgroundProvider = new AgentViewProvider({
    subscriptions: [],
    secrets: { get: async ref => ref === 'agent-two-secret' ? 'background-secret' : '' },
  }, backgroundService, {}, { onAgentBusy: () => {} })
  backgroundProvider.view = { visible: false, webview: { postMessage() {} } }
  backgroundProvider.boot = {
    executions: [],
    projectAgents: [{ id: 'agent-two', provider: 'openai', providerPreset: 'openai' }],
    providerCatalog: [{ id: 'openai', name: 'OpenAI', requiresApiKey: true }],
    connections: [{ provider: 'openai', presetId: 'openai', secretRef: 'agent-two-secret' }],
  }
  backgroundProvider.activeRunId = 'run-one'
  backgroundProvider.details = { run: { id: 'run-one', status: 'running' }, events: [], approvals: [], patches: [] }
  backgroundProvider.startPolling()
  await delay(1_200)
  if (nextExecutionLaunched !== 1 || backgroundProvider.activeRunId !== 'run-two') {
    throw new Error(`Hidden Hub stalled a multi-agent Flow: launches=${nextExecutionLaunched} active=${backgroundProvider.activeRunId} routes=${backgroundRoutes.join(',')}`)
  }
  if (backgroundRoutes.includes('/api/bootstrap')) throw new Error('Hidden Flow polling returned to a full bootstrap')
  backgroundProvider.dispose()

  let restoredLaunches = 0
  const restartBoot = {
    flowRuns: [{ id: 'restored-flow', status: 'waiting_approval' }],
    executions: [{ id: 'restored-execution', questId: 'restored-quest', flowRunId: 'restored-flow', projectAgentId: 'restored-agent', status: 'interrupted' }],
    projectAgents: [{ id: 'restored-agent', provider: 'openai', providerPreset: 'openai' }],
    providerCatalog: [{ id: 'openai', name: 'OpenAI', requiresApiKey: true }],
    connections: [{ provider: 'openai', presetId: 'openai', secretRef: 'restored-secret-ref' }],
  }
  const restartService = {
    state: 'running',
    hostLog() {},
    request: async (route, options = {}) => {
      if (route === '/api/bootstrap') return restartBoot
      if (route === '/api/executions/restored-execution/launch') {
        if (JSON.parse(options.body || '{}').apiKey !== 'restored-secret') throw new Error('Restart resume dropped the credential')
        restoredLaunches += 1
        return { id: 'restored-run', status: 'running' }
      }
      if (route === '/api/runs/restored-run') return { run: { id: 'restored-run', status: 'running' }, events: [], approvals: [], patches: [] }
      return {}
    },
  }
  const restartedProvider = new AgentViewProvider({
    subscriptions: [],
    secrets: { get: async ref => ref === 'restored-secret-ref' ? 'restored-secret' : '' },
  }, restartService, {}, { onAgentBusy: () => {}, onCompanionState: () => {} })
  restartedProvider.view = { visible: false, webview: { postMessage() {} } }
  await restartedProvider.refresh()
  if (restoredLaunches !== 1 || restartedProvider.activeRunId !== 'restored-run') {
    throw new Error(`IDE restart did not resume an authorized Flow: launches=${restoredLaunches} active=${restartedProvider.activeRunId}`)
  }
  restartedProvider.dispose()

  const cursorRuntime = require(path.resolve(__dirname, '..', 'vscode-extension', 'cursor-runtime.js'))
  let cursorExecutionStatus = 'pending'
  let cursorStarted = 0
  let cursorCompleted = 0
  let headlessCursorLaunches = 0
  const cursorBoot = () => ({
    flowRuns: [{ id: 'cursor-flow', status: cursorExecutionStatus === 'completed' ? 'completed' : 'waiting_approval' }],
    executions: [{
      id: 'cursor-execution', questId: 'cursor-quest', flowRunId: 'cursor-flow', flowNodeId: 'cursor-node',
      projectAgentId: 'cursor-agent', task: 'Edit only the reviewed sandbox.', status: cursorExecutionStatus,
    }],
    projectAgents: [{ id: 'cursor-agent', provider: 'cursor-cli', primaryModel: 'auto' }],
    providerCatalog: [], connections: [],
  })
  const cursorService = {
    state: 'running',
    hostLog() {},
    request: async (route, options = {}) => {
      if (route === '/api/bootstrap') return cursorBoot()
      if (route === '/api/executions/cursor-execution/launch') {
        headlessCursorLaunches += 1
        throw new Error('Cursor must not use headless launch')
      }
      if (route === '/api/executions/cursor-execution/cursor/start') {
        cursorStarted += 1
        cursorExecutionStatus = 'running'
        return {
          execution: { ...cursorBoot().executions[0], status: 'running' },
          profile: { id: 'cursor-agent', provider: 'cursor-cli', model: 'auto', allowedTools: ['read_file'] },
          sandboxPath: path.join(process.cwd(), '.tmp', 'cursor-reviewed-sandbox'),
        }
      }
      if (route === '/api/executions/cursor-execution/cursor/complete') {
        const body = JSON.parse(options.body || '{}')
        if (body.status !== 'completed' || body.result !== 'Cursor finished') throw new Error(`Bad Cursor completion: ${options.body}`)
        cursorCompleted += 1
        cursorExecutionStatus = 'completed'
        return { ...cursorBoot().executions[0], status: 'completed' }
      }
      return {}
    },
  }
  cursorRuntime.status = async () => ({ available: true, authenticated: true, model: 'auto' })
  cursorRuntime.startRun = ({ profile, task, cwd, onEvent }) => {
    if (profile.id !== 'cursor-agent' || !task.includes('reviewed sandbox') || !cwd.includes('cursor-reviewed-sandbox')) {
      throw new Error(`Cursor launch contract lost data: ${JSON.stringify({ profile, task, cwd })}`)
    }
    onEvent({ type: 'system', message: { type: 'run_started' } })
    return {
      done: delay(20).then(() => ({ status: 'completed', result: 'Cursor finished' })),
      cancel: async () => {}, dispose: async () => {},
    }
  }
  const cursorProvider = new AgentViewProvider({ subscriptions: [] }, cursorService, {}, { onAgentBusy: () => {}, onCompanionState: () => {} })
  cursorProvider.view = { visible: false, webview: { postMessage() {} } }
  cursorProvider.boot = cursorBoot()
  await cursorProvider.launchPendingHubExecutions('cursor-quest')
  await delay(100)
  if (cursorStarted !== 1 || cursorCompleted !== 1 || headlessCursorLaunches !== 0 || cursorExecutionStatus !== 'completed') {
    throw new Error(`Cursor Hub routing failed: start=${cursorStarted} complete=${cursorCompleted} headless=${headlessCursorLaunches} status=${cursorExecutionStatus}`)
  }
  cursorProvider.dispose()

  const cache = createWorkspaceFileCache()
  const first = await cache.get()
  if (!workspaceFileExclude.includes('.gocache') || !workspaceFileExclude.includes('.tmp')) {
    throw new Error(`Workspace file cache still scans Go build artifacts: ${workspaceFileExclude}`)
  }
  for (const artifact of ['.gocache/ab/cache-d', '.tmp/gocache/ab/cache-d']) {
    const uri = { fsPath: path.join(process.cwd(), ...artifact.split('/')), scheme: 'file' }
    if (!isIndexNoiseUri(uri)) throw new Error(`Index watcher still reacts to ${artifact}`)
  }
  const second = await cache.get()
  if (first !== second) throw new Error('Workspace file cache did not reuse the pending/resolved list')
  cache.invalidate()
  const third = await cache.get()
  if (third === first) throw new Error('Workspace file cache invalidate did not clear cached files')
  const extra = { fsPath: path.join(process.cwd(), 'b.go'), path: '/b.go', toString: () => '/b.go' }
  cache.apply(extra, 'change')
  if (!(await cache.get()).some(item => item.toString() === '/b.go')) throw new Error('Workspace file cache apply did not add a file')
  cache.apply(extra, 'delete')
  if ((await cache.get()).some(item => item.toString() === '/b.go')) throw new Error('Workspace file cache apply did not remove a file')

  const ready = formatIndexStatus({ state: 'ready', files: 12, chunks: 1, symbols: 2, mode: 'incremental' }, true)
  if (!ready.hide) throw new Error('Ready index status should stay quiet in the status bar')
  if (!String(ready.tooltip || '').includes('точечно')) throw new Error('Ready index tooltip should mention incremental mode')
  const partialIndex = formatIndexStatus({ state: 'ready', partial: true, limitReason: 'files', maxFiles: 20000, files: 20000, chunks: 40000 }, true)
  if (partialIndex.hide || !String(partialIndex.text).includes('частично') || !String(partialIndex.tooltip).includes('20000')) {
    throw new Error(`Partial index is hidden or unexplained: ${JSON.stringify(partialIndex)}`)
  }
  if (indexScheduleWaitMs('save', 1, 5000) > 450) throw new Error('Save should index quickly')
  if (indexScheduleWaitMs('fs', 20, 5000) !== 5000) throw new Error('Bulk FS changes should keep the full debounce')
  const changed = new Set(['pkg/a.go', 'keep.go'])
  const deleted = new Set()
  applyIndexDirty(changed, deleted, 'pkg', 'delete')
  if (!deleted.has('pkg') || !deleted.has('pkg/a.go') || changed.has('pkg/a.go') || !changed.has('keep.go')) {
    throw new Error(`directory delete dirty set is wrong: changed=${[...changed]} deleted=${[...deleted]}`)
  }

  let invalidates = 0
  let rebuilds = 0
  let statusCalls = 0
  let startedByIndex = 0
  const indexService = {
    state: 'running',
    ensureStarted: async () => { startedByIndex += 1 },
    request: async (route, options = {}) => {
      if (options.allowStart !== false && route.startsWith('/api/index/')) startedByIndex += 1
      if (route === '/api/index/invalidate' && options.method === 'POST') {
        if (options.allowStart !== false) throw new Error('invalidate must pass allowStart:false')
        invalidates += 1
        return { state: 'stale', files: 1 }
      }
      if (route === '/api/index/rebuild' && options.method === 'POST') {
        if (options.allowStart !== false) throw new Error('rebuild must pass allowStart:false')
        rebuilds += 1
        return { state: 'ready', files: 1, chunks: 1, symbols: 1 }
      }
      if (route === '/api/index/status') {
        if (options.allowStart !== false) throw new Error('status must pass allowStart:false')
        statusCalls += 1
        return { state: 'ready', files: 1 }
      }
      if (String(route).startsWith('/api/index/update')) {
        if (options.allowStart !== false) throw new Error('update must pass allowStart:false')
        rebuilds += 1
        return { state: 'ready', files: 1, chunks: 1, symbols: 1 }
      }
      return {}
    },
  }
  const indexItem = { text: '', tooltip: '', show() {}, hide() {} }
  const index = createProjectIndexController(indexService, indexItem, {
    onBusy() {},
    onPaint() {},
  })
  index.scheduleRebuild('fs')
  index.scheduleRebuild('fs')
  index.scheduleRebuild('fs')
  await delay(50)
  if (invalidates !== 1) throw new Error(`Expected one invalidate for coalesced schedules, got ${invalidates}`)
  await index.refresh()
  if (statusCalls !== 1) throw new Error(`Expected one allowStart:false status refresh, got ${statusCalls}`)
  if (startedByIndex !== 0) throw new Error(`Index traffic restarted core: ${startedByIndex}`)
  index.dispose()

  // Empty dirty set with ready index must not trigger a full rebuild.
  let emptyDirtyRebuilds = 0
  const readyIndexService = {
    state: 'running',
    ensureStarted: async () => {},
    request: async (route, options = {}) => {
      if (route === '/api/index/rebuild' && options.method === 'POST') {
        emptyDirtyRebuilds += 1
        return { state: 'ready', files: 2, chunks: 2, symbols: 2 }
      }
      if (route === '/api/index/update' && options.method === 'POST') {
        emptyDirtyRebuilds += 1
        return { state: 'ready', files: 2, chunks: 2, symbols: 2 }
      }
      if (route === '/api/index/status') return { state: 'ready', files: 2, chunks: 2, symbols: 2 }
      if (route === '/api/index/invalidate') return { state: 'stale', files: 2 }
      return {}
    },
  }
  const quietIndex = createProjectIndexController(readyIndexService, { text: '', tooltip: '', show() {}, hide() {} }, {
    onBusy() {},
    onPaint() {},
  })
  // Seed lastKnown via refresh, then schedule with no dirty paths.
  await quietIndex.refresh()
  quietIndex.scheduleRebuild('fs')
  await delay(50)
  // Force the debounced rebuild to run immediately by disposing timer path: call rebuildNow is force-full;
  // instead wait for debounce with a short config — schedule already fired invalidate only.
  // Directly exercise runRebuild via ensureReady after status ready — should no-op full rebuild.
  await quietIndex.ensureReady()
  if (emptyDirtyRebuilds !== 0) throw new Error(`Ready index with no dirty paths rebuilt: ${emptyDirtyRebuilds}`)
  quietIndex.dispose()

  process.stdout.write(JSON.stringify({
    concurrentStarts: starts,
    duplicateStates: 'suppressed',
    hiddenUpdates: 'suppressed',
    forcedInitialState: 'delivered',
    polling: 'serialized',
    fileCache: 'reuse+invalidate',
    quietReadyIndex: true,
    invalidateCoalesce: invalidates,
    rebuildsQueued: rebuilds,
    indexAllowStartGuard: true,
    emptyDirtySkip: true,
  }))
}

main().catch(error => { Module._load = originalLoad; console.error(error); process.exitCode = 1 })
