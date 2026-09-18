const assert = require('assert')
const Module = require('module')
const os = require('os')
const path = require('path')

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds))
const root = path.resolve(__dirname, '..')
const firstRoot = path.join(root, 'examples', 'go-health')
const secondRoot = path.join(root, 'frontend')
const uri = fsPath => ({
  scheme: 'file', fsPath,
  path: fsPath.replace(/\\/g, '/'),
  toString: () => `file:///${fsPath.replace(/\\/g, '/')}`,
})
const folders = [
  { name: 'go-health', uri: uri(firstRoot) },
  { name: 'frontend', uri: uri(secondRoot) },
]
const saveListeners = []
let pickedFolderName = 'frontend'
const commands = []
const workspace = {
  isTrusted: true,
  workspaceFolders: folders,
  textDocuments: [],
  getConfiguration: () => ({ get: (_key, fallback) => fallback }),
  getWorkspaceFolder(target) {
    const resolved = path.resolve(target?.fsPath || '')
    return folders.find(folder => {
      const relative = path.relative(folder.uri.fsPath, resolved)
      return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative))
    })
  },
  asRelativePath(target) {
    const folder = this.getWorkspaceFolder(target)
    if (!folder) return target?.fsPath || ''
    const relative = path.relative(folder.uri.fsPath, target.fsPath).replace(/\\/g, '/')
    return `${folder.name}/${relative}`
  },
  findFiles: async () => [],
  onDidChangeWorkspaceFolders: () => ({ dispose() {} }),
  onDidChangeConfiguration: () => ({ dispose() {} }),
  onDidSaveTextDocument(listener) { saveListeners.push(listener); return { dispose() {} } },
  createFileSystemWatcher: () => ({
    onDidCreate() { return { dispose() {} } },
    onDidChange() { return { dispose() {} } },
    onDidDelete() { return { dispose() {} } },
    dispose() {},
  }),
}
const window = {
  activeTextEditor: undefined,
  showQuickPick: async items => items.find(item => item.folder?.name === pickedFolderName),
  showOpenDialog: async () => undefined,
  showInformationMessage: async () => undefined,
  showErrorMessage: async () => undefined,
  withProgress: async (_opts, task) => task({ report() {} }),
}

const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      workspace,
      window,
      commands: { executeCommand: async command => { commands.push(command) } },
      Uri: {
        file: uri,
        joinPath: (base, ...parts) => uri(path.join(base.fsPath || String(base), ...parts)),
      },
      QuickPickItemKind: { Separator: -1 },
      ProgressLocation: { Window: 1, Notification: 2 },
      StatusBarAlignment: { Left: 1, Right: 2 },
      ThemeColor: class { constructor(id) { this.id = id } },
    }
  }
  return originalLoad.call(this, request, parent, isMain)
}

async function main() {
  const { __test } = require(path.join(root, 'vscode-extension', 'extension.js'))
  Module._load = originalLoad
  const {
    BackendService,
    choosePointWorkspace,
    createProjectIndexController,
    restorePointWorkspaceRoot,
    workspaceRelativePathIfInside,
  } = __test

  const state = new Map()
  const context = {
    globalStorageUri: uri(path.join(os.tmpdir(), 'point-multi-project-smoke')),
    workspaceState: {
      get: (key, fallback) => state.has(key) ? state.get(key) : fallback,
      update: async (key, value) => { state.set(key, value) },
    },
  }
  const service = new BackendService(context, { append() {}, appendLine() {} }, () => {})
  assert.equal(service.workspaceFolder()?.name, 'go-health', 'default project must be the first local root')
  service.setWorkspaceRoot(folders[1].uri)
  assert.strictEqual(service.workspaceFolder(), folders[1], 'selected root must resolve to the real workspace folder')

  service.setWorkspaceRoot(undefined)
  state.set('point.workspaceRoot', secondRoot)
  assert.strictEqual(restorePointWorkspaceRoot(service, context), folders[1], 'saved active project was not restored')
  assert.equal(workspaceRelativePathIfInside(secondRoot, uri(path.join(secondRoot, 'src', 'App.tsx'))), 'src/App.tsx')
  assert.equal(workspaceRelativePathIfInside(secondRoot, uri(path.join(firstRoot, 'main.go'))), '', 'foreign project path escaped the selected root')

  service.setWorkspaceRoot(folders[0].uri)
  service.state = 'running'
  let stops = 0
  let switched
  service.stop = async () => { stops += 1; service.state = 'stopped' }
  pickedFolderName = 'frontend'
  await choosePointWorkspace(service, context, folder => { switched = folder })
  assert.equal(stops, 1, 'switching an active project must detach the previous core')
  assert.strictEqual(service.workspaceFolder(), folders[1])
  assert.strictEqual(switched, folders[1])
  assert.equal(state.get('point.workspaceRoot'), secondRoot)
  // Переключатель читает недавние проекты, чтобы показать их прямо в списке —
  // как виджет проекта в JetBrains. Это чтение, а не открытие окна, и проверку
  // оно не касается: важно, что выбор уже открытого корня не зовёт openFolder.
  assert.deepEqual(commands.filter(command => command !== '_workbench.getRecentlyOpened'), [],
    'selecting an existing root must not open another window')

  const updates = []
  let invalidates = 0
  const indexService = {
    state: 'running',
    workspaceFolder: () => folders[1],
    hostLog() {},
    ensureStarted: async () => {},
    request: async (route, options = {}) => {
      if (route === '/api/index/status') return { state: 'ready', files: 1, chunks: 1, symbols: 1 }
      if (route === '/api/index/invalidate') { invalidates += 1; return { state: 'stale', files: 1 } }
      if (route === '/api/index/update') {
        updates.push(JSON.parse(options.body || '{}'))
        return { state: 'ready', files: 1, chunks: 1, symbols: 1, mode: 'incremental' }
      }
      if (route === '/api/index/rebuild') throw new Error('multi-root save unexpectedly triggered a full rebuild')
      return {}
    },
  }
  const index = createProjectIndexController(indexService, { text: '', tooltip: '', show() {}, hide() {} })
  await index.refresh()
  const save = saveListeners.at(-1)
  assert.equal(typeof save, 'function', 'index save listener was not registered')
  save({ uri: uri(path.join(firstRoot, 'main.go')) })
  await delay(550)
  assert.equal(invalidates, 0, 'a foreign project invalidated the selected project index')
  assert.equal(updates.length, 0, 'a foreign project updated the selected project index')

  save({ uri: uri(path.join(secondRoot, 'src', 'App.tsx')) })
  await delay(700)
  assert.equal(invalidates, 1)
  assert.deepEqual(updates, [{ changed: ['src/App.tsx'], deleted: [] }])
  index.dispose()

  const selectedBeforeEmpty = service.workspaceFolder().name
  const emptyProjectRoot = path.join(root, 'examples', 'go-health')
  folders.splice(0, folders.length)
  workspace.updateWorkspaceFolders = (_start, _count, added) => {
    folders.push({ name: added.name, uri: added.uri })
    return true
  }
  window.showQuickPick = async items => items.find(item => item.action === 'browse')
  window.showOpenDialog = async () => [uri(emptyProjectRoot)]
  let rebound
  const selectedFromEmpty = await choosePointWorkspace(service, context, folder => { rebound = folder }, { agentsWindowMode: true })
  assert.equal(selectedFromEmpty?.uri.fsPath, emptyProjectRoot, 'empty Agent Hub did not accept a project folder')
  assert.equal(rebound?.uri.fsPath, emptyProjectRoot, 'empty Agent Hub did not rebind its live provider')
  assert.equal(service.workspaceFolder()?.uri.fsPath, emptyProjectRoot, 'core root stayed detached after choosing a project in Agent Hub')

  // Быстрая дорога переключения. В окне Чертога `vscode.openFolder` не должен
  // звучать ни в одной ветке: он перезагружает окно и гасит ядро — ровно та
  // цена, ради снятия которой подмена папки и делается на месте. Прошлое ядро
  // при этом отпускается (`detach`), а не убивается: возврат к нему должен
  // стоить одной проверки здоровья.
  const { switchPointWorkspaceInPlace, resumeLastPointWorld } = __test
  commands.length = 0
  folders.splice(0, folders.length)
  folders.push({ name: 'go-health', uri: uri(firstRoot) })
  service.setWorkspaceRoot(folders[0].uri)
  service.runtimeWorkspaceKey = 'deadbeefdeadbeefdeadbeef'
  service.attachedPid = 4242
  service.state = 'running'
  let inPlaceStops = 0
  let detaches = 0
  const realDetach = service.detach.bind(service)
  service.stop = async () => { inPlaceStops += 1 }
  service.detach = () => { detaches += 1; realDetach() }
  const phases = []
  const warmed = []
  const switchedInPlace = await switchPointWorkspaceInPlace(service, context, uri(secondRoot), undefined, {
    agentsWindowMode: true,
    warmPool: { remember: async entry => { warmed.push(entry) }, reap: async () => ({ stopped: 0 }) },
    registry: { markOpened: async () => {}, setActive: async () => {} },
    onPhase: (phase, info) => phases.push({ phase, name: info?.name || '' }),
  })
  assert.equal(switchedInPlace?.uri.fsPath, secondRoot, 'быстрая дорога не переключила мир')
  assert.equal(inPlaceStops, 0, 'быстрая дорога погасила ядро вместо того, чтобы отпустить его')
  assert.equal(detaches, 1, 'прошлое ядро не было отпущено — тёплым оно не останется')
  assert.equal(warmed.length, 1, 'ядро прошлого мира не попало в кольцо тёплых')
  assert.equal(warmed[0].key, 'deadbeefdeadbeefdeadbeef')
  assert.deepEqual(phases.map(item => item.phase), ['start', 'done'], 'вебвью не получил ни начала, ни конца переключения')
  assert.ok(!commands.includes('vscode.openFolder'), 'переключение в Чертоге перезагрузило окно через openFolder')
  assert.equal(service.workspaceFolder()?.uri.fsPath, secondRoot, 'корень ядра не переехал в новый мир')

  // Возврат на последний мир при старте Чертога. Без него домашний экран
  // пуст после каждого запуска: список чатов отдаёт ядро, а ядро не поднимается
  // без папки. Исчезнувшая с диска папка при этом не воскрешается.
  const resumed = []
  const aliveRegistry = { activePath: () => firstRoot, setActive: async () => {} }
  const back = await resumeLastPointWorld(service, context, {
    registry: aliveRegistry,
    switchToProject: async target => { resumed.push(target) },
  })
  assert.equal(back, firstRoot, 'Чертог не вернулся на последний мир')
  assert.deepEqual(resumed, [firstRoot], 'возврат не позвал переключение мира')

  let cleared = ''
  const goneRoot = path.join(root, 'examples', 'этой-папки-нет')
  const goneRegistry = { activePath: () => goneRoot, setActive: async value => { cleared = value } }
  const missing = await resumeLastPointWorld(service, context, {
    registry: goneRegistry,
    switchToProject: async target => { resumed.push(target) },
  })
  assert.equal(missing, undefined, 'исчезнувший мир всё равно поднялся')
  assert.equal(cleared, '', 'активный мир не очищен после пропажи папки')
  assert.equal(resumed.length, 1, 'переключение ушло в несуществующую папку')

  process.stdout.write(JSON.stringify({
    roots: 2,
    resumeLastWorld: { restored: back === firstRoot, missingIgnored: missing === undefined },
    selected: selectedBeforeEmpty,
    inPlaceSwitch: { detaches, stops: inPlaceStops, warmKept: warmed.length, reload: false },
    coreDetached: stops,
    foreignIndexEvents: 0,
    incrementalPath: updates[0].changed[0],
    emptyHubRebound: true,
  }) + '\n')
}

main().catch(error => {
  Module._load = originalLoad
  console.error(error)
  process.exitCode = 1
})
