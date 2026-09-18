// Документы разговоров: страница диалога, окно сведений об ответе и архив.
//
// Группа жила в extension.js и росла вместе с ним против границы модуля
// (scripts/check-release-contracts.mjs: 7500 строк). Здесь ей и место: это
// единственная поверхность, которая рисует переписку вне вебвью Чертога, и
// разговор Мастера ей предстоит показывать тем же кодом, что и разговор
// помощника.
//
// Экранирование приходит доводом, а не заводится своё: две реализации одного
// экранирования — это два разных ответа на вопрос, что считать безопасным.
const vscode = require('vscode')
const { companionFactPairs } = require('./extension-utils')

// Архив прошлых диалогов живёт в состоянии рабочей области: он нужен только
// этой машине и только для чтения.
const POINT_COMPANION_ARCHIVES_KEY = 'point.companion.archives.v1'

// Страница разговора. Чистая: ни панели, ни контекста ей не нужно, поэтому
// её зовут и из класса, и напрямую — в том числе смоуки, собирающие панель
// вручную.
function companionDocumentHtml(title, subtitle, messages, details, escapeHtml, answerLabel = 'Помощник Point') {
  const rows = (Array.isArray(messages) ? messages : []).map(item => {
    const role = item?.role === 'user' ? 'user' : 'assistant'
    const label = role === 'user' ? 'Вы' : answerLabel
    const mark = item?.feedback === 'down' ? 'отмечено: не помогло' : item?.feedback === 'up' ? 'отмечено: полезно' : ''
    return `<article class="${role}"><header>${escapeHtml(label)}${mark ? `<em>${escapeHtml(mark)}</em>` : ''}${item?.createdAt ? `<time>${escapeHtml(new Date(item.createdAt).toLocaleString('ru-RU'))}</time>` : ''}</header><pre>${escapeHtml(item?.content || '')}</pre></article>`
  }).join('')
  return `<!doctype html><html lang="ru"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline';"><title>${escapeHtml(title)}</title><style>
    :root{color-scheme:dark;font-family:"Segoe UI Variable","Segoe UI",sans-serif;background:#0a0a0a;color:#f3f5f8}body{margin:0}main{box-sizing:border-box;margin:0 auto;max-width:820px;padding:28px 34px 56px}h1{font-size:19px;font-weight:600;margin:0 0 6px}main>header>p{color:#8e9aad;font-size:13px;margin:0 0 24px}.details{display:grid;gap:16px;margin-bottom:24px}.details dl{display:grid;gap:12px 20px;grid-template-columns:repeat(auto-fit,minmax(120px,1fr));margin:0}.details dt{color:#8e9aad;font-size:12px}.details dd{font-size:13px;margin:3px 0 0}.details h2{color:#8e9aad;font-size:12px;font-weight:500;margin:0 0 -8px}.details pre{background:#111;border:1px solid #232323;border-radius:6px;margin:0;max-height:260px;overflow:auto;padding:12px 14px;white-space:pre-wrap}article{border-top:1px solid #232323;margin:0;padding:16px 0}article.user{color:#c8cede}article header{align-items:baseline;color:#8e9aad;display:flex;font-size:12px;font-weight:500;gap:12px;justify-content:space-between}article pre{font:400 13px/1.6 "Segoe UI Variable","Segoe UI",sans-serif;margin:8px 0 0;white-space:pre-wrap;word-break:break-word}time{font-weight:400}@media(max-width:620px){main{padding:20px 14px}}
  </style></head><body><main><header><h1>${escapeHtml(title)}</h1><p>${escapeHtml(subtitle)}</p></header>${details}${rows}</main></body></html>`
}

class ChatDocuments {
  constructor({ escapeHtml, extensionUri, readContext }) {
    this.escapeHtml = escapeHtml
    this.extensionUri = extensionUri
    this.readContext = readContext
  }

  get context() { return this.readContext() }

  companionDocument(title, subtitle, messages, details = '') {
    return companionDocumentHtml(title, subtitle, messages, details, this.escapeHtml)
  }

  // Сведения об ответе Мастера. Окно то же, что у помощника, но собеседник
  // другой, и цена хода здесь настоящая: расход и задержку ядро стало писать
  // вместе с репликой (internal/orchestrator/chat.go, persistReply). До этого
  // половина полей показывала нули — окно, которое врёт про расход, хуже
  // отсутствующего.
  showMasterMessageDetails(item, request) {
    if (!item || typeof item !== 'object') return
    const esc = this.escapeHtml
    const facts = Array.isArray(item.factsUsed) ? item.factsUsed.filter(Boolean) : []
    const metrics = [
      ['Чем отвечено', item.mode === 'model' ? 'модель Мастера' : 'движок Point'],
      ['Провайдер', item.provider || 'локальный'],
      ['Модель', item.model || '—'],
      ['Вход', Number(item.inputTokens || 0).toLocaleString('ru-RU') + ' ток.'],
      ['Выход', Number(item.outputTokens || 0).toLocaleString('ru-RU') + ' ток.'],
      ['Задержка', item.latencyMs ? Number(item.latencyMs).toLocaleString('ru-RU') + ' мс' : '—'],
    ]
    const pairs = list => list.map(([label, value]) => '<div><dt>' + esc(label) + '</dt><dd>' + esc(value) + '</dd></div>').join('')
    const details = '<section class="details"><h2>Что было отправлено</h2><pre>' +
      esc(String(request || 'Реплика не найдена в видимой части переписки.')) + '</pre><dl>' + pairs(metrics) + '</dl>' +
      (item.fallbackReason ? '<h2>Почему ответил движок Point</h2><pre>' + esc(item.fallbackReason) + '</pre>' : '') +
      '<h2>Основания ответа</h2><pre>' +
      esc(facts.length ? facts.join('\n') : 'Мастер не назвал оснований для этого ответа.') + '</pre></section>'
    const panel = vscode.window.createWebviewPanel('point.masterAnswerDetails', 'Сведения об ответе Мастера', vscode.ViewColumn.Active, { enableScripts: false })
    panel.iconPath = vscode.Uri.joinPath(this.extensionUri, 'media', 'agent.svg')
    panel.webview.html = companionDocumentHtml('Сведения об ответе Мастера',
      'Реплика, происхождение, расход и основания одного хода.',
      [{ ...item, role: 'assistant' }], details, esc, 'Мастер')
  }


  showCompanionMessageDetails(item, request) {
    if (!item || typeof item !== 'object') return
    const facts = Array.isArray(item.factsUsed) ? item.factsUsed.filter(Boolean) : []
    const metrics = [
      ['Режим', item.mode || '—'], ['Провайдер', item.provider || 'локальный'], ['Модель', item.model || '—'],
      ['Вход', Number(item.inputTokens || 0).toLocaleString('ru-RU') + ' ток.'],
      ['Выход', Number(item.outputTokens || 0).toLocaleString('ru-RU') + ' ток.'],
      ['Задержка', item.latencyMs ? `${Number(item.latencyMs).toLocaleString('ru-RU')} мс` : '—'],
    ]
    // Окно существует ради проверки ответа, а факты приходят машинной строкой:
    // «memories=6 selected=3». Ключевые из них переводятся в понятные пары,
    // сырой список остаётся ниже — перевод знает не про всё.
    const influence = companionFactPairs(facts)
    const influenceBlock = influence.length
      ? `<h2>Что повлияло на ответ</h2><dl>${influence.map(([label, value]) => `<div><dt>${this.escapeHtml(label)}</dt><dd>${this.escapeHtml(value)}</dd></div>`).join('')}</dl>`
      : ''
    const details = `<section class="details"><h2>Что было отправлено</h2><pre>${this.escapeHtml(String(request || 'Запрос не найден в видимой части истории.'))}</pre><dl>${metrics.map(([label, value]) => `<div><dt>${this.escapeHtml(label)}</dt><dd>${this.escapeHtml(value)}</dd></div>`).join('')}</dl>${item.fallbackReason ? `<h2>Причина fallback</h2><pre>${this.escapeHtml(item.fallbackReason)}</pre>` : ''}${influenceBlock}<h2>Контекст и факты</h2><pre>${this.escapeHtml(facts.length ? facts.join('\n') : 'Дополнительные факты проекта для ответа не использовались.')}</pre></section>`
    const panel = vscode.window.createWebviewPanel('point.companionAnswerDetails', 'Сведения об ответе', vscode.ViewColumn.Active, { enableScripts: false })
    panel.iconPath = vscode.Uri.joinPath(this.extensionUri, 'media', 'agent.svg')
    panel.webview.html = this.companionDocument('Сведения об ответе', 'Запрос, происхождение, расход и факты конкретной реплики.', [{ ...item, role: 'assistant' }], details)
  }

  async showCompanionArchives() {
    const archives = this.context.workspaceState.get(POINT_COMPANION_ARCHIVES_KEY, [])
    const items = (Array.isArray(archives) ? archives : []).filter(item => Array.isArray(item?.messages) && item.messages.length)
    if (!items.length) {
      await vscode.window.showInformationMessage('Архив диалогов пока пуст. Он пополняется при создании нового чата.')
      return
    }
    const selected = await vscode.window.showQuickPick(items.map(item => ({
      label: item.title || 'Диалог с помощником',
      description: new Date(item.createdAt).toLocaleString('ru-RU'),
      detail: `${item.messages.length} сообщений · только чтение`,
      archive: item,
    })), { title: 'Архив диалогов помощника', placeHolder: 'Выберите диалог для просмотра' })
    if (!selected?.archive) return
    const panel = vscode.window.createWebviewPanel('point.companionArchive', selected.archive.title || 'Архив диалога', vscode.ViewColumn.Active, { enableScripts: false })
    panel.iconPath = vscode.Uri.joinPath(this.extensionUri, 'media', 'agent.svg')
    panel.webview.html = this.companionDocument(selected.archive.title || 'Диалог с помощником', `Архив · ${new Date(selected.archive.createdAt).toLocaleString('ru-RU')} · только чтение`, selected.archive.messages)
  }
}

function createChatDocuments(dependencies) {
  return new ChatDocuments(dependencies)
}

module.exports = { createChatDocuments, companionDocumentHtml, POINT_COMPANION_ARCHIVES_KEY }
