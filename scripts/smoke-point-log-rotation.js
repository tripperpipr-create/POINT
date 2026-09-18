const fs = require('fs')
const Module = require('module')
const os = require('os')
const path = require('path')

const root = path.resolve(__dirname, '..')
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

function assert(condition, message) {
  if (!condition) throw new Error(message)
}

const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'point-log-rotation-'))
try {
  const storage = path.join(tempRoot, 'Point', 'User', 'globalStorage', 'local-agent.local-agent-workbench')
  const context = { globalStorageUri: { fsPath: storage } }
  const service = new BackendService(context, { append() {}, appendLine() {} }, () => {})
  service.maxLogBytes = 1024
  service.maxLogArchives = 3
  fs.mkdirSync(path.dirname(service.logPath), { recursive: true })

  // Simulate files created by the old unbounded/32 MiB implementation. The
  // first append must compact them instead of merely renaming a huge file.
  fs.writeFileSync(service.logPath, `${'old-active-line\n'.repeat(700)}`)
  fs.writeFileSync(`${service.logPath}.1`, `${'old-archive-line\n'.repeat(600)}`)
  service.appendLog('first line after upgrade\n')

  for (let index = 0; index < 80; index += 1) {
    service.appendLog(`${String(index).padStart(3, '0')} ${'x'.repeat(180)}\n`)
  }

  const files = fs.readdirSync(path.dirname(service.logPath))
    .filter(name => /^point-core\.log(?:\.\d+)?$/.test(name))
    .map(name => path.join(path.dirname(service.logPath), name))
  assert(files.length <= 4, `rotation retained too many files: ${files.length}`)
  for (const file of files) {
    assert(fs.statSync(file).size <= service.maxLogBytes, `${path.basename(file)} exceeds the per-file limit`)
  }
  assert(fs.readFileSync(service.logPath, 'utf8').includes('079 '), 'latest log entry was lost during rotation')
  assert(!fs.existsSync(service.logRotationLockPath), 'rotation lock was left behind')
  assert(!fs.readdirSync(path.dirname(service.logPath)).some(name => name.includes('.rotate-')), 'rotation temporary file was left behind')
  console.log('log rotation: bounded active file, bounded archives and oversized-log migration verified')
} finally {
  fs.rmSync(tempRoot, { recursive: true, force: true })
}
