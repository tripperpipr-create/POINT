const assert = require('assert')
const Module = require('module')
const path = require('path')

const root = path.resolve(__dirname, '..')
const quickPickSelections = ['src', 'main.go']
const requests = []
const documents = []
const shownDocuments = []
const languageChanges = []
const notifications = []

const vscodeMock = {
  workspace: {
    workspaceFolders: [],
    textDocuments: [],
    getConfiguration: () => ({ get: (_key, fallback) => fallback }),
    openTextDocument: async options => {
      const document = { uri: { scheme: 'untitled', toString: () => 'untitled:Point-SSH' }, content: options.content }
      documents.push(document)
      return document
    },
  },
  window: {
    activeTextEditor: undefined,
    showQuickPick: async items => {
      const wanted = quickPickSelections.shift()
      return items.find(item => item.remoteName === wanted)
    },
    showTextDocument: async document => { shownDocuments.push(document) },
    showInformationMessage: async message => { notifications.push(message) },
    showWarningMessage: async message => { notifications.push(message) },
  },
  languages: {
    setTextDocumentLanguage: async (document, language) => {
      languageChanges.push({ document, language })
      return document
    },
  },
  env: { clipboard: { writeText: async () => {} } },
  Uri: {
    file: fsPath => ({ scheme: 'file', fsPath, path: fsPath.replace(/\\/g, '/'), toString: () => `file:///${fsPath}` }),
    joinPath: (base, ...parts) => ({ scheme: 'file', fsPath: path.join(base.fsPath, ...parts) }),
  },
  QuickPickItemKind: { Separator: -1 },
  ProgressLocation: { Window: 1, Notification: 2 },
  StatusBarAlignment: { Left: 1, Right: 2 },
  ThemeColor: class { constructor(id) { this.id = id } },
  ThemeIcon: class { constructor(id) { this.id = id } },
}

const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') return vscodeMock
  return originalLoad.call(this, request, parent, isMain)
}

async function main() {
  const { __test } = require(path.join(root, 'vscode-extension', 'extension.js'))
  Module._load = originalLoad
  const {
    normalizeSSHRemotePath,
    sshRemotePathParent,
    sshRemotePathJoin,
    sshRemotePickerEntries,
    browseSSHRemotePath,
  } = __test

  assert.equal(normalizeSSHRemotePath('  ~/app///  '), '~/app')
  assert.equal(sshRemotePathParent('/srv/app'), '/srv')
  assert.equal(sshRemotePathParent('~/app'), '~')
  assert.equal(sshRemotePathJoin('/', 'srv'), '/srv')
  assert.equal(sshRemotePathJoin('~/app', 'src'), '~/app/src')
  assert.throws(() => sshRemotePathJoin('/srv', '../escape'), /Недопустимое/)
  assert.deepEqual(
    sshRemotePickerEntries(['src/', 'main.go']).map(item => [item.remoteName, item.directory]),
    [['src', true], ['main.go', false]],
  )

  const service = {
    async request(route, options) {
      const body = JSON.parse(options.body)
      requests.push({ route, body })
      if (route.endsWith('/list') && body.path === '/srv/app') return { path: body.path, entries: ['README.md', 'src/'] }
      if (route.endsWith('/list') && body.path === '/srv/app/src') return { path: body.path, entries: ['main.go'] }
      if (route.endsWith('/read') && body.path === '/srv/app/src/main.go') {
        return { path: body.path, content: 'package main\n', truncated: false }
      }
      throw new Error(`unexpected SSH request ${route} ${JSON.stringify(body)}`)
    },
  }
  const context = { secrets: { get: async ref => ref === 'secret-1' ? 'one-time-password' : '' } }
  const profile = {
    id: 'ssh-1', displayName: 'prod', host: 'prod.example', defaultRemotePath: '/srv/app', secretRef: 'secret-1',
  }
  const selectedPath = await browseSSHRemotePath(service, context, profile, profile.defaultRemotePath)

  assert.equal(selectedPath, '/srv/app/src/main.go')
  assert.deepEqual(requests.map(item => [item.route.split('/').at(-1), item.body.path]), [
    ['list', '/srv/app'],
    ['list', '/srv/app/src'],
    ['read', '/srv/app/src/main.go'],
  ])
  assert(requests.every(item => item.body.password === 'one-time-password'), 'password must only be forwarded from SecretStorage')
  assert.equal(documents[0].content, 'package main\n')
  assert.strictEqual(shownDocuments[0], documents[0])
  assert.equal(languageChanges[0].language, 'go')
  assert(notifications.some(message => message.includes('безопасный предпросмотр')))

  console.log('Point SSH smoke test passed: configured root -> nested directory -> bounded UTF-8 preview')
}

main().catch(error => {
  Module._load = originalLoad
  console.error(error)
  process.exitCode = 1
})
