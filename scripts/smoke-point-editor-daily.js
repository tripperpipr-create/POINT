const Module = require('module')
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
      StatusBarAlignment: { Left: 1, Right: 2 },
      ThemeIcon: class { constructor(id) { this.id = id } },
      ThemeColor: class { constructor(id) { this.id = id } },
      TerminalLocation: { Panel: 1 },
      Uri: { joinPath: (...parts) => ({ fsPath: parts.join('/'), toString: () => parts.join('/') }) },
    }
  }
  return originalLoad.call(this, request, parent, isMain)
}

const {
  parseDocumentOutline,
  mergeRecentFiles,
  formatCopyReference,
  outlineKindIcon,
  resolveOutlineSymbolAt,
  formatOutlineBreadcrumb,
} = require('../vscode-extension/extension.js').__test
Module._load = originalLoad

const go = parseDocumentOutline(`package auth

type User struct{}

func (u *User) Login() error { return nil }

func ParseToken() {}

const DefaultTTL = 1
`, 'go')
const goNames = go.map(item => item.name).join(',')
if (goNames !== 'User,Login,ParseToken,DefaultTTL') throw new Error(`go outline: ${goNames}`)
if (go.find(item => item.name === 'Login')?.kind !== 'method') throw new Error('Login should be a method')
if (go.find(item => item.name === 'ParseToken')?.kind !== 'function') throw new Error('ParseToken should be a function')

const js = parseDocumentOutline(`export function load() {}
export class Store {}
const save = async () => {}
export interface Repo {}
`, 'typescript')
if (!js.some(item => item.name === 'load' && item.kind === 'function')) throw new Error('ts function missing')
if (!js.some(item => item.name === 'Store' && item.kind === 'class')) throw new Error('ts class missing')
if (!js.some(item => item.name === 'save' && item.kind === 'function')) throw new Error('ts arrow missing')
if (!js.some(item => item.name === 'Repo' && item.kind === 'interface')) throw new Error('ts interface missing')

const py = parseDocumentOutline(`class App:
    def start(self):
        pass

def main():
    pass
`, 'python')
if (py.find(item => item.name === 'start')?.indent !== 1) throw new Error('python method indent')
if (py.find(item => item.name === 'main')?.kind !== 'function') throw new Error('python function')

const md = parseDocumentOutline('# Title\n## Section\ntext\n', 'markdown')
if (md.length !== 2 || md[1].indent !== 1) throw new Error(`markdown outline: ${JSON.stringify(md)}`)

const java = parseDocumentOutline(`public class Service {
  public void start() {
  }
}
`, 'java')
if (!java.some(item => item.name === 'Service' && item.kind === 'class')) throw new Error('java class missing')
if (!java.some(item => item.name === 'start')) throw new Error('java method missing')

const kotlin = parseDocumentOutline(`class Gateway {
  fun ping() {}
}
`, 'kotlin')
if (!kotlin.some(item => item.name === 'Gateway')) throw new Error('kotlin class missing')
if (!kotlin.some(item => item.name === 'ping')) throw new Error('kotlin fun missing')

const chain = resolveOutlineSymbolAt(py, 2)
if (formatOutlineBreadcrumb(chain) !== 'App.start') throw new Error(`breadcrumb: ${formatOutlineBreadcrumb(chain)}`)
if (resolveOutlineSymbolAt(go, 4).map(item => item.name).join('.') !== 'User.Login') {
  throw new Error(`go breadcrumb: ${formatOutlineBreadcrumb(resolveOutlineSymbolAt(go, 4))}`)
}

const recent = mergeRecentFiles(
  [{ uri: 'file:///a.go', path: 'a.go', at: 2, line: 10, character: 3 }],
  [{ uri: 'file:///b.go', path: 'b.go' }, { uri: 'file:///a.go', path: 'a.go', line: 1 }],
  8,
)
if (recent.map(item => item.path).join(',') !== 'a.go,b.go') throw new Error(`recent merge: ${JSON.stringify(recent)}`)
if (recent[0].line !== 10 || recent[0].character !== 3) throw new Error(`recent position: ${JSON.stringify(recent[0])}`)

if (formatCopyReference('internal\\\\auth.go', 40, 3, 'Login') !== 'Login (internal/auth.go:40:3)') {
  throw new Error(`copy reference: ${formatCopyReference('internal\\\\auth.go', 40, 3, 'Login')}`)
}
if (outlineKindIcon('method') !== 'symbol-method') throw new Error('outline icon')

process.stdout.write(JSON.stringify({
  go: go.length,
  typescript: js.length,
  python: py.length,
  markdown: md.length,
  java: java.length,
  kotlin: kotlin.length,
  breadcrumb: formatOutlineBreadcrumb(chain),
  recent: recent.length,
  recentLine: recent[0].line,
  reference: formatCopyReference('auth.go', 12, 1, 'Login'),
}) + '\n')
