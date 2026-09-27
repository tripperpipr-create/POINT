// Общие настройки → «Интеграции и MCP»: нажатия по своим MCP-серверам —
// доверие, проверка, остановка и удаление, инструменты и их риск, секреты,
// форма сервера и импорт mcp.json.
//
// Состояние общее с integrations-ui.js и приходит ссылкой: страницу рисует
// один модуль, а черновики полей и занятость живут в одном месте. Хосту
// уходит только mcpAction. Разбор формы здесь же: переменные и заголовки
// делятся на открытые и секретные, и неверная строка до хоста не доходит.

function parsePairs(text, separator, label) {
  const values = {}
  for (const raw of String(text || '').split('\n')) {
    const line = raw.trim()
    if (!line) continue
    const at = line.indexOf(separator)
    if (at <= 0) throw new Error(`${label}: строка «${line.slice(0, 40)}» — ожидается ИМЯ${separator.trim() === ':' ? ': ' : '='}значение`)
    values[line.slice(0, at).trim()] = line.slice(at + separator.length).trim()
  }
  return values
}

export function createMcpServerActions({ state, mcp, render }) {
  function saveServerForm() {
    const d = key => state.drafts[`form.${key}`]
    const editing = (state.servers || []).find(item => item.id === state.formId)
    const transport = d('transport') || editing?.transport || 'stdio'
    const pairs = (key, fallback, separator, label) => d(key) !== undefined ? parsePairs(d(key), separator, label) : fallback
    const server = { id: state.formId, displayName: d('displayName') ?? editing?.displayName ?? '', transport }
    const secrets = {}
    if (transport === 'http') {
      server.url = d('url') ?? editing?.url ?? ''
      server.headers = pairs('headers', editing?.headers || {}, ':', 'Заголовки')
      const secret = pairs('secretHeaders', Object.fromEntries(Object.keys(editing?.secretHeaders || {}).map(name => [name, ''])), ':', 'Секретные заголовки')
      server.secretHeaders = Object.keys(secret)
      for (const [name, value] of Object.entries(secret)) if (value) secrets[`header:${name}`] = value
      const allow = d('allowPrivate') ?? Boolean(editing?.allowPrivateHost)
      try { server.allowPrivateHost = allow ? new URL(server.url).hostname : '' } catch { server.allowPrivateHost = '' }
    } else {
      server.command = d('command') ?? editing?.command ?? ''
      server.args = d('args') !== undefined ? d('args').split('\n').map(line => line.trim()).filter(Boolean) : editing?.args || []
      server.dir = d('dir') ?? editing?.dir ?? ''
      server.env = pairs('env', editing?.env || {}, '=', 'Переменные')
      const secret = pairs('secretEnv', Object.fromEntries(Object.keys(editing?.secretEnv || {}).map(name => [name, ''])), '=', 'Секретные переменные')
      server.secretEnv = Object.keys(secret)
      for (const [name, value] of Object.entries(secret)) if (value) secrets[`env:${name}`] = value
    }
    state.busy = 'save'
    mcp('save', { server, secrets })
  }

  // Своё действие — true; чужое — false, и нажатие уходит дальше по цепочке.
  function click(action, target) {
    const data = target?.dataset || {}
    const id = String(data.id || '')
    switch (action) {
      case 'mcp-trust': state.busy = 'trust'; state.error = ''; mcp('trust', { id }); break
      case 'mcp-probe': state.busy = 'probe'; state.error = ''; mcp('probe', { id }); break
      case 'mcp-stop': mcp('stop', { id }); break
      case 'mcp-delete': state.error = ''; mcp('delete', { id }); break
      case 'mcp-log':
        state.logOpen = state.logOpen === id ? '' : id
        if (state.logOpen) { delete state.logs[id]; mcp('log', { id }) }
        break
      case 'mcp-tools-toggle': state.toolsOpen = state.toolsOpen === id ? '' : id; break
      case 'mcp-tool-toggle': mcp('tool', { id, name: String(data.name || ''), enabled: Boolean(target.checked), risk: '' }); return true
      case 'mcp-tool-risk': mcp('tool', { id, name: String(data.name || ''), enabled: data.enabled === '1', risk: String(data.risk || '') }); break
      case 'mcp-secret': {
        const key = String(data.draftKey || '')
        const value = String(state.drafts[key] || '')
        if (!value) { state.error = 'Введите значение секрета.'; break }
        delete state.drafts[key]
        mcp('secret', { id, key: String(data.key || ''), value })
        break
      }
      case 'mcp-edit':
        for (const key of Object.keys(state.drafts)) if (key.startsWith('form.')) delete state.drafts[key]
        Object.assign(state, { formOpen: true, formId: id, importOpen: false })
        break
      case 'mcp-form-open':
        for (const key of Object.keys(state.drafts)) if (key.startsWith('form.')) delete state.drafts[key]
        Object.assign(state, { formOpen: true, formId: '', importOpen: false })
        break
      case 'mcp-form-cancel': Object.assign(state, { formOpen: false, formId: '' }); break
      case 'mcp-form-transport': state.drafts['form.transport'] = String(data.transport || 'stdio'); break
      case 'mcp-form-save':
        try { state.error = ''; saveServerForm() } catch (error) { state.error = error instanceof Error ? error.message : String(error) }
        break
      case 'mcp-import-open': Object.assign(state, { importOpen: true, formOpen: false }); break
      case 'mcp-import-close': Object.assign(state, { importOpen: false, importCandidates: [] }); mcp('importClear'); break
      case 'mcp-import-preview': state.busy = 'import'; state.error = ''; mcp('importPreview', { text: String(state.drafts['import.text'] || '') }); break
      case 'mcp-import-pick': state.error = ''; mcp('importPreview', { pick: true }); break
      case 'mcp-import-add': {
        const name = String(data.name || '')
        const candidate = state.importCandidates.find(item => item.name === name)
        const values = {}
        for (const key of candidate?.needsValue || []) values[key] = String(state.drafts[`importValue:${name}:${key}`] || '')
        state.busy = 'import'
        mcp('importAdd', { name, values })
        break
      }
      case 'mcp-dismiss-error': state.error = ''; break
      default:
        return false
    }
    render()
    return true
  }

  return { click }
}
