const Module = require('module')
const { extensionHostSource } = require('./lib/extension-host-source')
const { overlaySource: readOverlaySource } = require('./lib/overlay-source')
const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      workspace: {
        workspaceFolders: [],
        fs: { stat: async () => { throw new Error('no fs') }, readFile: async () => Buffer.from('') },
        getConfiguration: () => ({ get: (_key, fallback) => fallback }),
        onDidChangeWorkspaceFolders: () => ({ dispose() {} }),
        createFileSystemWatcher: () => ({
          onDidCreate() { return { dispose() {} } },
          onDidChange() { return { dispose() {} } },
          onDidDelete() { return { dispose() {} } },
          dispose() {},
        }),
      },
      window: { createStatusBarItem: () => ({ dispose() {}, show() {}, hide() {} }), createTerminal: () => ({ show() {}, sendText() {} }) },
      StatusBarAlignment: { Left: 1 },
      ThemeIcon: class { constructor(id) { this.id = id } },
      ThemeColor: class { constructor(id) { this.id = id } },
      TerminalLocation: { Panel: 1 },
      Uri: { joinPath: (...parts) => ({ fsPath: parts.join('/'), toString: () => parts.join('/') }) },
    }
  }
  return originalLoad.call(this, request, parent, isMain)
}

const {
  parseJsonc,
  makefileTargets,
  buildRunConfigurations,
  pickDefaultRunConfiguration,
} = require('../vscode-extension/extension.js').__test
Module._load = originalLoad

const launch = parseJsonc(`{
  // debug
  "version": "0.2.0",
  "configurations": [
    { "name": "Main", "type": "go", "request": "launch", "program": "\${fileDirname}", },
  ],
}`)
if (launch?.configurations?.[0]?.name !== 'Main') throw new Error('parseJsonc did not keep launch.json comments/commas')
const quotedComma = parseJsonc('{ // comment\n "configurations": [{"name": "run,}", "type": "go",},], }')
if (quotedComma?.configurations?.[0]?.name !== 'run,}') throw new Error('parseJsonc changed a comma inside a configuration name')

const targets = makefileTargets('all: build\nbuild:\n\tgo build\n.PHONY: all\ntest:\n\tgo test\n')
if (targets.join(',') !== 'all,build,test') throw new Error(`makefile targets: ${targets}`)

const folder = { name: 'app', uri: { fsPath: 'C:/work/app' } }
const configs = buildRunConfigurations(folder, {
  launch,
  tasks: { tasks: [{ label: 'compile', type: 'shell', command: 'go build', group: 'build' }] },
  packageJson: { scripts: { start: 'node .', test: 'go test' } },
  goMod: 'module app',
  makefile: 'run:\n\tgo run .\n',
  cargo: '[package]\nname = "app"\n',
  pyproject: '[project]\nname = "app"\n',
})
const ids = configs.map(item => item.id)
for (const required of [
  'launch:C:/work/app:Main',
  'task:C:/work/app:compile',
  'npm:C:/work/app:start',
  'npm:C:/work/app:test',
  'go:C:/work/app:run',
  'make:C:/work/app:run',
  'cargo:C:/work/app:run',
  'python:C:/work/app:pytest',
]) {
  if (!ids.includes(required)) throw new Error(`missing run config ${required}`)
}
const picked = pickDefaultRunConfiguration(configs)
if (picked?.kind !== 'launch' || picked.label !== 'Main') throw new Error(`default run should be launch Main, got ${picked?.id}`)
const npmOnly = pickDefaultRunConfiguration(configs.filter(item => item.kind === 'npm'))
if (npmOnly?.script !== 'start') throw new Error(`npm default should be start, got ${npmOnly?.script}`)

// Чип конфигурации в заголовке читает контекстный ключ, а наполняет его
// контроллер строки состояния. Если ключ перестанут публиковать, чип молча
// исчезнет: заголовок просто не найдёт значения и спрячет себя.
const extensionSource = extensionHostSource()
if (!extensionSource.includes("require('./ide-action-controller')") || !extensionSource.includes("'setContext', 'point.runConfiguration'")) {
  throw new Error('run configuration must be published for the title bar chip')
}
const overlaySource = readOverlaySource()
if (!overlaySource.includes('point-run-chip') || !overlaySource.includes('localAgent.selectRunConfiguration')) {
  throw new Error('title bar must carry the run configuration chip')
}

process.stdout.write(JSON.stringify({
  launch: launch.configurations.length,
  configs: configs.length,
  default: picked.id,
  kinds: [...new Set(configs.map(item => item.kind))],
}) + '\n')

// Run File invokes the shared shell quoting helper when a filename has spaces.
// This path used to throw because ide-action-controller did not import it.
const { createIdeRunController } = require('../vscode-extension/ide-run-controller')
class FileUri {
  constructor(fsPath) { this.fsPath = fsPath; this.scheme = 'file' }
}
const sent = []
const disposable = { dispose() {} }
const runVscode = {
  Uri: FileUri,
  workspace: {
    workspaceFolders: [],
    getWorkspaceFolder: () => undefined,
    createFileSystemWatcher: () => ({
      ...disposable,
      onDidCreate() {}, onDidChange() {}, onDidDelete() {},
    }),
    onDidChangeWorkspaceFolders: () => disposable,
  },
  window: {
    terminals: [],
    createStatusBarItem: () => ({ ...disposable, show() {}, hide() {} }),
    createTerminal: () => ({ show() {}, sendText(command) { sent.push(command) } }),
    showInformationMessage: async () => undefined,
  },
  commands: { executeCommand: async () => undefined },
  StatusBarAlignment: { Left: 1 },
  TerminalLocation: { Panel: 1 },
  ThemeIcon: class {},
  ThemeColor: class {},
}
const runActions = createIdeRunController({ vscode: runVscode, pickDefaultRunConfiguration: () => undefined })
const runController = runActions.createRunConfigurationController({
  workspaceState: { get: () => '', update: async () => undefined },
  subscriptions: [],
})
runController.runFile(new FileUri('C:/work/my script.py')).then(() => {
  const { shellQuote } = require('../vscode-extension/run-config-utils')
  if (sent[0] !== `python ${shellQuote('my script.py')}`) throw new Error(`Run File command: ${sent[0]}`)
  runController.dispose()
  process.stdout.write('run file with spaces: ok\n')
}).catch(error => { console.error(error); process.exitCode = 1 })
