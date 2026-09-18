const path = require('path')
const { restoreSystemBackup } = require('../vscode-extension/backup-controller')

const snapshot = {
  id: 'point-20260831T120000.000000000Z-manual.db',
  reason: 'manual', createdAt: '2026-08-31T12:00:00Z', sizeBytes: 1048576,
  sha256: 'a'.repeat(64), integrity: 'ok',
}

async function successCase() {
  const calls = []
  let getCount = 0
  const service = {
    dataDirPath: path.join('C:', 'Point Data'),
    async request(route, options = {}) {
      calls.push(`${options.method || 'GET'} ${route}`)
      if (options.method === 'POST') return { integrity: 'ok' }
      getCount += 1
      return [snapshot]
    },
    otherLiveLeaseCount() { return 0 },
    databaseToolPath() { return path.join('C:', 'Point', 'point-db.exe') },
    corePid() { return 42 },
    async stop() { calls.push('stop') },
    async start() { calls.push('start') },
    hostLog() {},
  }
  const messages = []
  const result = await restoreSystemBackup({
    service,
    window: {
      async showQuickPick(items) { return items[0] },
      async showWarningMessage() { return 'Создать recovery point и восстановить' },
      showInformationMessage(message) { messages.push(message) },
    },
    processIsAlive() { return false },
    async runTool(binary, args) {
      calls.push(`tool ${path.basename(binary)} ${args[0]}`)
      if (args.includes('--confirm-offline') !== true || args.includes('--backup') !== true || args.includes('--db') !== true) {
        throw new Error('offline restore arguments are incomplete')
      }
      return { backup: { sha256: snapshot.sha256, integrity: 'ok' }, restored: { integrity: 'ok' }, previousPath: 'recovery.db' }
    },
    async refresh() { calls.push('refresh') },
    async loadStatisticsSnapshot() { return { systemHealth: { status: 'READY' } } },
    post(message) { messages.push(message) },
  })
  if (result.status !== 'restored' || getCount !== 2) throw new Error(`Restore did not complete: ${JSON.stringify(result)}`)
  const expected = ['GET /api/system/backups', 'POST /api/system/backups', 'GET /api/system/backups', 'stop', 'tool point-db.exe restore', 'start', 'refresh']
  if (JSON.stringify(calls) !== JSON.stringify(expected)) throw new Error(`Unsafe restore order: ${JSON.stringify(calls)}`)
  if (!messages.some(message => message?.type === 'statistics') || !messages.some(message => String(message).includes('восстановлена'))) {
    throw new Error('Restore completion was not reported to the UI')
  }
}

async function sharedCoreCase() {
  let mutated = false
  try {
    await restoreSystemBackup({
      service: {
        async request() { return [snapshot] },
        otherLiveLeaseCount() { return 1 },
        async stop() { mutated = true },
      },
      window: {
        async showQuickPick(items) { return items[0] },
        async showWarningMessage() { return 'Создать recovery point и восстановить' },
        showInformationMessage() {},
      },
      processIsAlive() { return false }, refresh() {}, loadStatisticsSnapshot() {}, post() {},
    })
    throw new Error('Shared Core restore should be blocked')
  } catch (error) {
    if (!String(error.message).includes('другие окна') || mutated) throw error
  }
}

successCase().then(sharedCoreCase).then(() => {
  console.log(JSON.stringify({ restore: 'verified-offline', recoveryPoint: true, sharedCore: 'blocked' }))
}).catch(error => {
  console.error(error)
  process.exitCode = 1
})
