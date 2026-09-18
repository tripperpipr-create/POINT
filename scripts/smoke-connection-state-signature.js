const fs = require('fs')
const path = require('path')
const vm = require('vm')

// Переводы строк нормализуются: срез опирался на пустую строку между
// функцией и следующим комментарием и молча ломался от того, что файл
// выровняли к CRLF, — проверка зависела от невидимого артефакта, а не от кода.
const source = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'extension.js'), 'utf8')
  .replace(/\r\n/g, '\n')
const start = source.indexOf('function cheapStateSignature(message)')
const end = source.indexOf('\n\n// Ошибки ядра', start)
if (start < 0 || end < 0) throw new Error('cheapStateSignature source was not found')

const context = { JSON }
vm.runInNewContext(`${source.slice(start, end)}\nthis.signature = cheapStateSignature`, context)
const signature = context.signature
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
