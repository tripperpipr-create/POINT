const fs = require('fs')
const Module = require('module')
const path = require('path')

const root = path.resolve(__dirname, '..')
const storagePath = process.argv[2]
if (!storagePath) {
  throw new Error('Usage: node scripts/rotate-point-log.js <Point globalStorage path>')
}

const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      workspace: {
        isTrusted: true,
        workspaceFolders: [],
        getConfiguration: () => ({ get: (_key, fallback) => fallback }),
      },
      Uri: { joinPath: (...parts) => ({ fsPath: parts.map(item => item?.fsPath || item).join(path.sep), scheme: 'file' }) },
    }
  }
  return originalLoad.call(this, request, parent, isMain)
}
const { BackendService } = require(path.join(root, 'vscode-extension', 'extension.js')).__test
Module._load = originalLoad

const service = new BackendService(
  { globalStorageUri: { fsPath: path.resolve(storagePath) } },
  { append() {}, appendLine() {} },
  () => {},
)
fs.mkdirSync(path.dirname(service.logPath), { recursive: true })
service.rotateLogIfNeeded(1)

const files = fs.readdirSync(path.dirname(service.logPath))
  .filter(name => /^point-core\.log(?:\.\d+)?$/.test(name))
  .sort()
  .map(name => {
    const file = path.join(path.dirname(service.logPath), name)
    return { name, bytes: fs.statSync(file).size }
  })
process.stdout.write(JSON.stringify({ logPath: service.logPath, maxBytes: service.maxLogBytes, files }, null, 2))
