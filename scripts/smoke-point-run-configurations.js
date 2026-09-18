const Module = require('module')
const { extensionHostSource } = require('./lib/extension-host-source')
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
const fs = require('node:fs')
const path = require('node:path')
const extensionSource = extensionHostSource()
const actionSource = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'ide-action-controller.js'), 'utf8')
if (!extensionSource.includes("require('./ide-action-controller')") || !actionSource.includes("'setContext', 'point.runConfiguration'")) {
  throw new Error('run configuration must be published for the title bar chip')
}
const overlaySource = fs.readFileSync(path.resolve(__dirname, '..', 'distribution', 'apply-overlay.mjs'), 'utf8')
if (!overlaySource.includes('point-run-chip') || !overlaySource.includes('localAgent.selectRunConfiguration')) {
  throw new Error('title bar must carry the run configuration chip')
}

process.stdout.write(JSON.stringify({
  launch: launch.configurations.length,
  configs: configs.length,
  default: picked.id,
  kinds: [...new Set(configs.map(item => item.kind))],
}) + '\n')
