// MCP-серверы владельца со стороны хоста: секреты, окно доверия, импорт
// mcp.json.
//
// Значение секрета проходит вебвью → хост → ядро один раз — при сохранении.
// Дальше оно живёт в SecretStorage IDE и в памяти ядра; вебвью видит только
// имена. Ядро теряет память с перезапуском, поэтому перед первым обращением к
// новому ядру хост отдаёт ему все секреты MCP заново (ensureUnlocked).
//
// Доверие даёт только этот файл: модальное окно IDE показывает точную
// программу, аргументы и переменные, а в ядро уходит ровно тот отпечаток,
// который был у показанной конфигурации.

const PROBE_TIMEOUT_MS = 130_000
const MAX_IMPORT_BYTES = 256 << 10

function clip(text, limit) {
  const value = String(text ?? '')
  return value.length > limit ? `${value.slice(0, limit)}…` : value
}

// trustDetail — что именно запустится. Открытые значения переменных видны:
// они и так лежат в базе Point; значения секретов не показываются никогда.
function trustDetail(server) {
  const lines = [
    `«${server.displayName}» запустится на этом компьютере вне песочницы Point — с вашими правами, файлами и сетью.`,
    '',
    `Программа: ${server.resolvedPreview || server.command}`,
  ]
  if (server.args?.length) lines.push(`Аргументы: ${server.args.map(arg => clip(arg, 160)).join(' ')}`)
  if (server.dir) lines.push(`Папка: ${server.dir}`)
  const env = Object.entries(server.env || {})
  if (env.length) lines.push(`Переменные: ${env.map(([name, value]) => `${name}=${clip(value, 120)}`).join('; ')}`)
  const secretNames = Object.keys(server.secretEnv || {})
  if (secretNames.length) lines.push(`Секреты (значения не показываются): ${secretNames.join(', ')}`)
  lines.push('', 'Любая правка команды, аргументов или переменных снимет доверие.')
  return lines.join('\n')
}

// Кандидат импорта уходит в вебвью без значений секретов: только их имена.
function publicCandidate(candidate) {
  const server = { ...(candidate.server || {}) }
  const secretNames = Object.keys(server.secrets || {})
  delete server.secrets
  return { ...candidate, server, secretNames }
}

function createMcpController({ vscode, secrets, request, coreKey, post }) {
  let unlockedFor = ''
  const pendingImport = new Map()

  async function servers() {
    const payload = await request('/api/mcp/servers')
    return Array.isArray(payload?.servers) ? payload.servers : []
  }

  async function ensureUnlocked() {
    const key = coreKey()
    if (!key || unlockedFor === key) return
    const values = {}
    for (const server of await servers()) {
      for (const ref of Object.values(server.secretRefs || {})) {
        const value = await secrets.get(ref)
        if (value) values[ref] = value
      }
    }
    if (Object.keys(values).length) {
      await request('/api/mcp/secrets/unlock', { method: 'POST', body: JSON.stringify({ values }) })
    }
    unlockedFor = key
  }

  async function publish(extra = {}) {
    post({ type: 'mcpServers', servers: await servers(), ...extra })
  }

  // storeSecrets кладёт в SecretStorage то, что владелец ввёл: ссылки
  // выдало ядро в ответе на сохранение.
  async function storeSecrets(view, values) {
    for (const [key, value] of Object.entries(values || {})) {
      const ref = view?.secretRefs?.[key]
      if (ref && value) await secrets.store(ref, String(value))
    }
  }

  function upsertFrom(input) {
    const list = value => (Array.isArray(value) ? value.map(String) : [])
    const map = value => Object.fromEntries(Object.entries(value && typeof value === 'object' ? value : {}).map(([k, v]) => [String(k), String(v)]))
    return {
      id: String(input?.id || ''), displayName: String(input?.displayName || ''), kind: 'custom',
      transport: input?.transport === 'http' ? 'http' : 'stdio',
      command: String(input?.command || ''), args: list(input?.args), dir: String(input?.dir || ''),
      env: map(input?.env), secretEnv: list(input?.secretEnv),
      url: String(input?.url || ''), headers: map(input?.headers), secretHeaders: list(input?.secretHeaders),
      allowPrivateHost: String(input?.allowPrivateHost || ''), settings: {},
    }
  }

  async function save(server, values) {
    const view = await request('/api/mcp/servers', { method: 'POST', body: JSON.stringify({ ...server, secrets: values || {} }) })
    await storeSecrets(view, values)
    return view
  }

  async function trust(id) {
    const server = (await servers()).find(item => item.id === id)
    if (!server) throw new Error('Сервер не найден — обновите список.')
    if (server.transport !== 'stdio') return server
    if (!server.pendingDigest) throw new Error(`Программа ${server.command} не найдена на этой машине — доверять нечему.`)
    const answer = await vscode.window.showWarningMessage(
      `Доверить запуск «${server.displayName}»?`,
      { modal: true, detail: trustDetail(server) },
      'Доверяю',
    )
    if (answer !== 'Доверяю') return undefined
    await request(`/api/mcp/servers/${encodeURIComponent(id)}/trust`, { method: 'POST', body: JSON.stringify({ digest: server.pendingDigest }) })
    return request(`/api/mcp/servers/${encodeURIComponent(id)}/probe`, { method: 'POST', body: '{}', timeoutMs: PROBE_TIMEOUT_MS })
  }

  async function remove(id) {
    const server = (await servers()).find(item => item.id === id)
    if (!server) return false
    const answer = await vscode.window.showWarningMessage(
      `Удалить «${server.displayName}»?`,
      { modal: true, detail: 'Сервер остановится, его инструменты и секреты будут удалены из Point.' },
      'Удалить',
    )
    if (answer !== 'Удалить') return false
    await request(`/api/mcp/servers/${encodeURIComponent(id)}`, { method: 'DELETE' })
    for (const ref of Object.values(server.secretRefs || {})) {
      try { await secrets.delete(ref) } catch { /* ключа уже нет */ }
    }
    return true
  }

  async function importText(message) {
    let text = String(message.text || '')
    if (message.pick) {
      const picked = await vscode.window.showOpenDialog({
        canSelectMany: false, openLabel: 'Импортировать', title: 'Point · импорт mcp.json',
        filters: { 'MCP config': ['json'] },
      })
      if (!picked?.[0]) return undefined
      const raw = await vscode.workspace.fs.readFile(picked[0])
      if (raw.byteLength > MAX_IMPORT_BYTES) throw new Error('Файл больше 256 КБ — это не конфиг MCP.')
      text = Buffer.from(raw).toString('utf8')
    }
    let config
    try { config = JSON.parse(text) } catch { throw new Error('Текст не разобрался как JSON.') }
    return config
  }

  async function handle(message) {
    const action = String(message?.action || '')
    const id = String(message?.id || '')
    try {
      await ensureUnlocked()
      switch (action) {
        case 'list':
          await publish()
          return
        case 'save': {
          const view = await save(upsertFrom(message.server), message.secrets)
          await publish({ saved: view.id })
          return
        }
        case 'trust':
          if (await trust(id)) await publish({ trusted: id })
          return
        case 'probe':
          await request(`/api/mcp/servers/${encodeURIComponent(id)}/probe`, { method: 'POST', body: '{}', timeoutMs: PROBE_TIMEOUT_MS })
          await publish()
          return
        case 'stop':
          await request(`/api/mcp/servers/${encodeURIComponent(id)}/stop`, { method: 'POST', body: '{}' })
          await publish()
          return
        case 'delete':
          if (await remove(id)) await publish({ deleted: id })
          return
        case 'tool':
          await request(`/api/mcp/servers/${encodeURIComponent(id)}/tools`, {
            method: 'POST',
            body: JSON.stringify({ name: String(message.name || ''), enabled: message.enabled === true, risk: String(message.risk || '') }),
          })
          await publish()
          return
        case 'log': {
          const payload = await request(`/api/mcp/servers/${encodeURIComponent(id)}/log`)
          post({ type: 'mcpLog', id, log: String(payload?.log || '') })
          return
        }
        case 'secret': {
          // Новое значение одного секрета: в SecretStorage и сразу в ядро.
          const server = (await servers()).find(item => item.id === id)
          const ref = server?.secretRefs?.[String(message.key || '')]
          if (!ref || !message.value) throw new Error('Секрет не найден — обновите список.')
          await secrets.store(ref, String(message.value))
          await request('/api/mcp/secrets/unlock', { method: 'POST', body: JSON.stringify({ values: { [ref]: String(message.value) } }) })
          await publish()
          return
        }
        case 'importPreview': {
          const config = await importText(message)
          if (config === undefined) return
          const preview = await request('/api/mcp/import/preview', { method: 'POST', body: JSON.stringify({ config }) })
          pendingImport.clear()
          for (const candidate of preview?.candidates || []) pendingImport.set(candidate.name, candidate)
          post({ type: 'mcpImportPreview', candidates: [...pendingImport.values()].map(publicCandidate) })
          return
        }
        case 'importAdd': {
          const candidate = pendingImport.get(String(message.name || ''))
          if (!candidate || candidate.refused) throw new Error('Этого сервера нет в предпросмотре — импортируйте файл заново.')
          const values = { ...(candidate.server.secrets || {}) }
          for (const key of candidate.needsValue || []) {
            const value = String(message.values?.[key] || '')
            if (!value) throw new Error(`Нужно значение ${key.replace(/^(env|header):/, '')}.`)
            values[key] = value
          }
          const view = await save(upsertFrom(candidate.server), values)
          pendingImport.delete(candidate.name)
          post({ type: 'mcpImportPreview', candidates: [...pendingImport.values()].map(publicCandidate) })
          await publish({ saved: view.id })
          return
        }
        case 'importClear':
          pendingImport.clear()
          post({ type: 'mcpImportPreview', candidates: [] })
          return
        default:
          throw new Error('Неизвестное действие MCP')
      }
    } catch (error) {
      post({ type: 'mcpError', action, id, message: error instanceof Error ? error.message : String(error) })
    }
  }

  return { handle, ensureUnlocked, publish, storeSecrets, servers }
}

module.exports = { createMcpController, trustDetail, publicCandidate }
