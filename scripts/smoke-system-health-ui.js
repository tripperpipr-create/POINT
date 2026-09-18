const fs = require('fs')
const path = require('path')
const vm = require('vm')

const listeners = {}
const posted = []
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector() { return null },
  querySelectorAll() { return [] },
}
const context = {
  acquireVsCodeApi: () => ({
    postMessage(message) { posted.push(message) },
    getState() { return { selectedTab: 'statistics' } },
    setState() {},
  }),
  document: { getElementById: id => id === 'root' ? root : undefined, body: { dataset: { layout: 'statistics' } } },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set,
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() {}, setTimeout(callback) { callback(); return 0 }, clearTimeout() {},
}

const script = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')
vm.runInNewContext(script, context, { filename: 'media/main.js' })
listeners['window:message']({ data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'statistics',
  boot: { onboarded: true, profiles: [], projectAgents: [], runs: [], usageRecords: [] }, details: undefined,
} })
if (!posted.some(message => message.type === 'loadStatistics')) throw new Error('Statistics did not request lifecycle evidence')

listeners['window:message']({ data: {
  type: 'statistics',
  statistics: {
    agents: 1, quests: 2, usageCount: 3,
    systemHealth: {
      schemaVersion: 1,
      status: 'BLOCKED',
      checkedAt: '2026-08-31T00:00:00Z',
      blockingReasons: ['Digest sandbox image не совпадает с policy'],
      checks: [
        { code: 'point_core', status: 'READY', summary: 'Point Core доступен', metrics: { version: '1.2.2' } },
        { code: 'sqlite', status: 'READY', summary: 'SQLite цела', metrics: { integrity: 'ok', foreignKeyViolations: 0 } },
        { code: 'migration', status: 'READY', summary: 'Схема SQLite актуальна', metrics: { version: 30, expectedVersion: 30, missingVersions: [] } },
        { code: 'disk_space', status: 'DEGRADED', summary: 'Свободное место близко к безопасному минимуму', nextAction: 'Освободите место.', metrics: { availableBytes: 700 * 1024 * 1024 } },
        { code: 'sandbox', status: 'BLOCKED', summary: 'Digest sandbox image не совпадает с policy', nextAction: 'Восстановите закреплённый image.', metrics: { backend: 'docker', version: '27.2.0', apiVersion: '1.47', imageDigest: 'sha256:actual' } },
        { code: 'workspace_permissions', status: 'READY', summary: 'Права workspace подтверждены' },
        { code: 'core_port', status: 'READY', summary: 'Порт Point Core занят ожидаемым процессом', metrics: { port: '48123' } },
        { code: 'backup', status: 'READY', summary: 'Последний backup проверен', metrics: { createdAt: '2026-08-30T22:00:00Z', sizeBytes: 1048576, sha256: 'abcdef0123456789', integrity: 'ok' }, repairActions: [{ id: 'create_backup', label: 'Создать backup' }, { id: 'restore_database', label: 'Восстановить базу', requiresConfirmation: true }] },
        { code: 'provider_model', status: 'DEGRADED', summary: 'Live-доступность ещё не подтверждена', nextAction: 'Повторно проверьте provider.', metrics: { configuredProfiles: 2, providers: 1, models: 1 } },
      ],
    },
  },
} })

for (const expected of ['СОСТОЯНИЕ СИСТЕМЫ', 'ЗАБЛОКИРОВАНО', 'Digest sandbox image не совпадает с policy', 'Следующее безопасное действие', 'API 1.47', '700 MB свободно', 'Создать проверенный backup', 'Восстановить из backup']) {
  if (!root.innerHTML.includes(expected)) throw new Error(`System health omitted ${expected}`)
}

const backupButton = { dataset: { action: 'create-system-backup' }, closest(selector) { return selector === '[data-action]' ? this : null } }
listeners['root:click']({ target: backupButton })
if (!posted.some(message => message.type === 'createSystemBackup')) throw new Error('Backup repair action was not wired to the extension')
const restoreButton = { dataset: { action: 'restore-system-backup' }, closest(selector) { return selector === '[data-action]' ? this : null } }
listeners['root:click']({ target: restoreButton })
if (!posted.some(message => message.type === 'restoreSystemBackup')) throw new Error('Restore action was not wired to the extension')
if (/undefined|NaN|\[object Object\]|api[_ -]?key|token/i.test(root.innerHTML)) {
  throw new Error('System health leaked an invalid value or credential-shaped field')
}

console.log(JSON.stringify({ systemHealth: 'visible', lifecycle: 'READY|DEGRADED|BLOCKED', checks: 9 }))
