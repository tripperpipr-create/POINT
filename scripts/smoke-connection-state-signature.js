const path = require('path')

const { cheapStateSignature: signature } = require(path.join(__dirname, '..', 'vscode-extension', 'hub-state-signature.js'))
const base = {
  service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'connections',
  onboarding: { complete: true },
  boot: {
    connections: [{ id: 'llm-1', status: 'unknown', lastError: '', updatedAt: '1' }],
    serverProfiles: [{ id: 'ssh-1', status: 'unknown', lastError: '', lastProbeAt: null, updatedAt: '1' }],
    dbConnections: [{ id: 'db-1', status: 'unknown', lastError: '', lastProbeAt: null, updatedAt: '1' }],
  },
}
const clone = value => JSON.parse(JSON.stringify(value))
const initial = signature(base)
for (const [collection, patch] of [
  ['connections', { status: 'connected', updatedAt: '2' }],
  ['serverProfiles', { status: 'error', lastError: 'connection refused', lastProbeAt: '2', updatedAt: '2' }],
  ['dbConnections', { status: 'connected', lastProbeAt: '2', updatedAt: '2' }],
]) {
  const changed = clone(base)
  Object.assign(changed.boot[collection][0], patch)
  if (signature(changed) === initial) {
    throw new Error(`Connection state change is invisible to postState signature: ${collection}`)
  }
}
console.log('connection state signature: PASS')
