// Подготовленный, но не доставленный файл квеста — в diff-редакторе IDE
// (TODO Q13). Карточка квеста показывает усечённый diff прямо в ленте; полный
// файл до и после ядро отдаёт по одному запросу, а IDE сравнивает их в
// документах только для чтения: править недоставленный результат здесь нечего —
// путь к нему лежит через новую версию наряда.

const vscode = require('vscode')

const SCHEME = 'point-prepared'
const documents = new Map()
let provider = null

function ensureProvider(host) {
  if (provider) return
  provider = vscode.workspace.registerTextDocumentContentProvider(SCHEME, {
    provideTextDocumentContent: uri => documents.get(uri.toString()) ?? '',
  })
  host?.context?.subscriptions?.push(provider)
}

async function openPreparedDiff(message) {
  const changeSetId = String(message.changeSetId || '')
  const itemId = String(message.itemId || '')
  if (!changeSetId || !itemId) throw new Error('Не выбран файл набора изменений.')
  const content = await this.service.request(`/api/change-sets/${encodeURIComponent(changeSetId)}/items/${encodeURIComponent(itemId)}/content`)
  ensureProvider(this)
  const file = String(content?.path || message.path || 'file')
  const name = file.split('/').pop()
  const left = vscode.Uri.from({ scheme: SCHEME, path: '/' + file, query: `${changeSetId}.${itemId}.original` })
  const right = vscode.Uri.from({ scheme: SCHEME, path: '/' + file, query: `${changeSetId}.${itemId}.proposed` })
  documents.set(left.toString(), String(content?.original ?? ''))
  documents.set(right.toString(), String(content?.proposed ?? ''))
  await vscode.commands.executeCommand('vscode.diff', left, right, `${name} · подготовлено, не доставлено`, { preview: true })
}

module.exports = { openPreparedDiff }
