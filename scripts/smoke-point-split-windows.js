const fs = require('fs')
const { extensionHostSource } = require('./lib/extension-host-source')
const http = require('http')
const Module = require('module')
const os = require('os')
const path = require('path')

const root = path.resolve(__dirname, '..')
const extensionSource = extensionHostSource()
const overlaySource = fs.readFileSync(path.join(root, 'distribution', 'apply-overlay.mjs'), 'utf8')
const menubarSource = fs.readFileSync(path.join(root, 'distribution', 'resources', 'point-menubar.ts.txt'), 'utf8')
const product = JSON.parse(fs.readFileSync(path.join(root, 'distribution', 'product-overrides.json'), 'utf8'))
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'vscode-extension', 'package.json'), 'utf8'))

function assert(condition, message) {
  if (!condition) throw new Error(message)
}

assert(product.sessionsWindowAllowedExtensions?.includes('local-agent.local-agent-workbench'), 'Point is not allowed in the Agents window')
assert(manifest.activationEvents?.includes('onCommand:localAgent.openAgentsHubWindow'), 'Agents window command cannot activate Point')
const agentsCommand = manifest.contributes?.commands?.find(item => item.command === 'localAgent.openAgentsHubWindow')
assert(agentsCommand?.enablement === 'isSessionsWindow', 'Internal Agents window command is exposed in regular IDE windows')
assert(extensionSource.includes("process.env.POINT_AUXILIARY_HUB === '1'"), 'Sessions Agent Hub is not the production default')
assert(extensionSource.includes("executeCommand('workbench.action.moveEditorToNewWindow'"), 'Auxiliary diagnostic fallback is missing')
assert(extensionSource.includes("executeCommand('point.focusAgentHubWindow'"), 'Repeated Hub open cannot focus the existing auxiliary window')
assert(menubarSource.includes("registerCommand('point.focusAgentHubWindow'"), 'Workbench focus contract for the auxiliary Hub is missing')
assert(extensionSource.includes("executeCommand('workbench.action.openAgentsWindow'"), 'Production Agents window route is missing')
assert(extensionSource.includes("initialQuery = `point-hub:"), 'Project/tab handoff to the Agents window is missing')
assert(extensionSource.includes('agentImprovementFocus') && overlaySource.includes('constructorStep'), 'Agent improvement handoff loses its constructor target')
assert(menubarSource.includes("registerCommand('point.openInMainEditor'"), 'Auxiliary Hub file navigation is not routed to the main editor group')
assert(overlaySource.includes('existingAgentsWindow?.focus()'), 'Repeated Hub opens can create duplicate Agents windows')
assert(overlaySource.includes("product.applicationName === 'point'") && overlaySource.includes('? defaultProfile'), 'Agents window does not share the IDE profile/secrets')

const originalLoad = Module._load
const workspaceRoot = path.join(root, 'examples', 'go-health')
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      workspace: {
        isTrusted: true,
        workspaceFolders: [{ name: 'go-health', uri: { scheme: 'file', fsPath: workspaceRoot } }],
        getConfiguration: () => ({ get: (_key, fallback) => fallback }),
      },
      Uri: {
        joinPath: (...parts) => ({ fsPath: parts.map(item => item?.fsPath || item).join(path.sep), scheme: 'file' }),
      },
    }
  }
  return originalLoad.call(this, request, parent, isMain)
}
const { BackendService, sharedPointStoragePath } = require(path.join(root, 'vscode-extension', 'extension.js')).__test
Module._load = originalLoad

async function main() {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'point-split-window-'))
  const profileStorage = path.join(tempRoot, 'Point', 'User', 'profiles', 'agents', 'globalStorage', 'local-agent.local-agent-workbench')
  const context = { globalStorageUri: { fsPath: profileStorage } }
  const expectedSharedStorage = path.join(tempRoot, 'Point', 'User', 'globalStorage', 'local-agent.local-agent-workbench')
  assert(sharedPointStoragePath(context) === expectedSharedStorage, 'Agents profile resolved a different Point data directory')

  const server = http.createServer((_request, response) => {
    response.setHeader('Content-Type', 'application/json')
    response.end('{}')
  })
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  const baseUrl = `http://127.0.0.1:${server.address().port}`
  const output = { append() {}, appendLine() {} }
  const statuses = []
  const first = new BackendService(context, output, status => statuses.push(status))
  const second = new BackendService(context, output, status => statuses.push(status))
  const folder = { name: 'go-health', uri: { scheme: 'file', fsPath: workspaceRoot } }

  try {
    fs.mkdirSync(expectedSharedStorage, { recursive: true })
    fs.writeFileSync(path.join(expectedSharedStorage, 'api-token'), 'split-window-token\n')
    const runtime = first.runtimePaths(folder)
    fs.writeFileSync(runtime.descriptorPath, JSON.stringify({
      version: 1,
      pid: 2147483000,
      baseUrl,
      workspaceRoot,
      startedAt: new Date().toISOString(),
    }))

    assert(await first.tryAttachSharedCore(folder), 'First window did not attach to the published core')
    assert(await second.tryAttachSharedCore(folder), 'Second window did not reuse the published core')
    assert(first.baseUrl === second.baseUrl && first.apiToken === second.apiToken, 'Windows attached to different core state')
    assert(first.process === undefined && second.process === undefined, 'Attaching spawned or claimed a second child process')

    await first.stop()
    assert(fs.existsSync(runtime.descriptorPath), 'Closing one window removed the core used by another window')
    assert(await second.isHealthy(baseUrl), 'Closing one window stopped the shared core')
    await second.stop()
    assert(!fs.existsSync(runtime.descriptorPath), 'Last window left a stale core descriptor')
  } finally {
    first.releaseLease()
    second.releaseLease()
    await new Promise(resolve => server.close(resolve))
    fs.rmSync(tempRoot, { recursive: true, force: true })
  }

  console.log('split windows: sessions default, auxiliary fallback, routing and single-core leases verified')
}

main().catch(error => {
  console.error(error)
  process.exitCode = 1
})
