const assert = require('assert')
const fs = require('fs')
const Module = require('module')
const path = require('path')

const root = path.resolve(__dirname, '..')
const workspaceRoot = path.join(root, 'examples', 'go-health')
const uri = fsPath => ({ scheme: 'file', fsPath, path: fsPath.replace(/\\/g, '/'), toString: () => `file:///${fsPath.replace(/\\/g, '/')}` })
const folder = { name: 'go-health', uri: uri(workspaceRoot) }
const created = []
const terminals = []

const window = {
  activeTextEditor: undefined,
  terminals,
  createTerminal(options) {
    const terminal = {
      name: options.name,
      options,
      shown: [],
      show(preserveFocus) { this.shown.push(preserveFocus) },
    }
    terminals.push(terminal)
    created.push(terminal)
    return terminal
  },
}

const vscodeMock = {
  workspace: {
    isTrusted: true,
    workspaceFolders: [folder],
    textDocuments: [],
    getConfiguration: () => ({ get: (_key, fallback) => fallback }),
    getWorkspaceFolder: target => {
      if (!target) throw new TypeError("Cannot read properties of undefined (reading 'scheme')")
      return target.fsPath?.startsWith(workspaceRoot) ? folder : undefined
    },
    onDidChangeWorkspaceFolders: () => ({ dispose() {} }),
  },
  window,
  Uri: {
    file: uri,
    joinPath: (base, ...parts) => uri(path.join(base.fsPath || String(base), ...parts)),
  },
  TerminalLocation: { Editor: 2, Panel: 1 },
  ThemeIcon: class { constructor(id) { this.id = id } },
  ThemeColor: class { constructor(id) { this.id = id } },
  StatusBarAlignment: { Left: 1, Right: 2 },
}

const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') return vscodeMock
  return originalLoad.call(this, request, parent, isMain)
}

async function main() {
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'vscode-extension', 'package.json'), 'utf8'))
  const binding = (manifest.contributes?.keybindings || []).find(item => item.key === 'alt+f12' && item.command === 'localAgent.openTerminal')
  assert(binding, 'Alt+F12 must route to localAgent.openTerminal')
  const extensionSource = fs.readFileSync(path.join(root, 'vscode-extension', 'extension.js'), 'utf8')
  assert(extensionSource.includes("registerCommand('localAgent.openTerminal'"), 'terminal command must be registered during activation')

  const { __test } = require(path.join(root, 'vscode-extension', 'extension.js'))
  Module._load = originalLoad
  const { createConsoleChannel, openConsoleChannel } = __test

  const first = await openConsoleChannel()
  assert.strictEqual(first, created[0], 'openConsoleChannel must return the terminal it opened')
  assert.equal(created.length, 1)
  assert.equal(first.options.name, 'Point · Квест')
  assert.equal(first.options.location, vscodeMock.TerminalLocation.Editor)
  assert.strictEqual(first.options.cwd, folder.uri, 'console cwd must be the active Point project')
  assert.equal(first.options.isTransient, false)
  assert.deepEqual(first.shown, [true])

  const second = await openConsoleChannel()
  assert.strictEqual(second, first, 'Alt+F12 must reuse the most recent Point console')
  assert.equal(created.length, 1, 'reopening the console must not duplicate a terminal')
  assert.deepEqual(first.shown, [true, true])

  const tests = createConsoleChannel('Тесты')
  assert.equal(tests.name, 'Point · Тесты')
  assert.equal(tests.options.iconPath.id, 'beaker')
  assert.equal(tests.options.color.id, 'charts.green')

  vscodeMock.workspace.workspaceFolders = []
  const emptyWindow = createConsoleChannel('Пустое окно')
  assert.equal(emptyWindow.options.cwd, require('os').homedir(), 'empty-window console must never send cwd: undefined to Code-OSS')

  console.log(JSON.stringify({
    key: binding.key, command: binding.command, location: 'editor', cwd: workspaceRoot,
    reused: second === first, channels: terminals.map(item => item.name),
  }))
}

main().catch(error => {
  Module._load = originalLoad
  console.error(error)
  process.exitCode = 1
})
