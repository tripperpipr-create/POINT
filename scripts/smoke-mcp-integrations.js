// Гильдия → «Интеграции»: свои MCP-серверы и плагин GitLab.
//
// Что проверяется:
// - вкладка сама спрашивает хост о серверах и о GitLab, и ровно один раз;
// - текст сервера (имя, команда, описание инструмента) экранируется: это
//   чужой текст из mcp.json и из tools/list;
// - «вне песочницы» видно у каждого сервера, который запускается на машине;
// - «Доверяю», включение инструмента и риск уходят хосту с тем сервером и
//   тем инструментом, которые нажаты;
// - форма разбирает переменные и секреты: открытые значения — в env,
//   секретные — отдельно и по имени; неверная строка не уходит хосту;
// - токен GitLab не остаётся в черновиках вебвью после отправки.
//
//   node scripts/smoke-mcp-integrations.js   (после npm run build)

const { bootWebview } = require('./lib/webview-harness')

const failures = []
const check = (name, ok, detail = '') => { if (!ok) failures.push(`${name}${detail ? `: ${detail}` : ''}`) }

const view = bootWebview({ layout: 'wide' })
view.state({ selectedTab: 'integrations' })
let sent = view.take()
check('вкладка спрашивает список серверов', sent.some(m => m.type === 'mcpAction' && m.action === 'list'), JSON.stringify(sent))
check('вкладка спрашивает состояние GitLab у хоста как Гильдия', sent.some(m => m.type === 'gitlabAction' && m.action === 'status' && m.surface === 'hub'), JSON.stringify(sent))
view.state({ selectedTab: 'integrations' })
check('повторная отрисовка не спрашивает снова', !view.take().some(m => m.action === 'list'))

const hostile = '<img src=x onerror="alert(1)">'
view.send({ type: 'mcpServers', servers: [
  { id: 'mcp-evil', displayName: hostile, kind: 'custom', transport: 'stdio', command: 'npx', args: ['-y', hostile], env: {}, trusted: false,
    outsideSandbox: true, status: 'unknown', runtime: {}, secretEnv: { API_TOKEN: 'point.mcp.mcp-evil.env.API_TOKEN' }, secretRefs: {},
    tools: [{ name: 'read_thing', description: hostile, risk: 'LOW', state: 'changed', enabled: false }] },
  { id: 'mcp-remote', displayName: 'Remote', kind: 'custom', transport: 'http', url: 'https://mcp.example.com/mcp', trusted: true, status: 'error',
    runtime: {}, secretsLocked: ['Authorization'], secretHeaders: { Authorization: 'ref' }, problem: 'ядро не получило секрет', tools: [], secretRefs: {} },
] })
view.send({ type: 'gitlabStatus', response: { state: 'error', reason: 'not_configured', problem: 'GitLab не подключён', data: { configured: false } } })
let html = view.root.innerHTML
check('имя и команда сервера экранированы', !html.includes('<img') && html.includes('&lt;img'), html.slice(0, 200))
check('метка «вне песочницы» у локального сервера', html.includes('вне песочницы'))
check('сервер без доверия предлагает «Доверяю»', html.includes('data-action="mcp-trust" data-id="mcp-evil"'))
check('у удалённого сервера нет кнопки доверия', !html.includes('data-action="mcp-trust" data-id="mcp-remote"'))
check('недостающий секрет можно ввести на месте', html.includes('data-action="mcp-secret" data-id="mcp-remote"'))
check('GitLab без настроек показывает форму подключения', html.includes('data-action="gitlab-plugin-save"') && html.includes('Подключить'))
check('значения секретов не выводятся', !html.includes('point.mcp.mcp-evil.env.API_TOKEN'))

view.click({ action: 'mcp-trust', id: 'mcp-evil' })
sent = view.take()
check('«Доверяю» уходит хосту с id сервера', sent.some(m => m.type === 'mcpAction' && m.action === 'trust' && m.id === 'mcp-evil'), JSON.stringify(sent))

view.click({ action: 'mcp-tools-toggle', id: 'mcp-evil' })
html = view.root.innerHTML
check('описание инструмента экранировано', html.includes('&lt;img') && !html.includes('<img'))
check('изменившийся инструмент помечен', html.includes('изменился — посмотрите'))
view.click({ action: 'mcp-tool-toggle', id: 'mcp-evil', name: 'read_thing' }, { checked: true })
view.click({ action: 'mcp-tool-risk', id: 'mcp-evil', name: 'read_thing', risk: 'HIGH', enabled: '0' })
sent = view.take()
check('включение инструмента уходит с его именем', sent.some(m => m.action === 'tool' && m.name === 'read_thing' && m.enabled === true && m.risk === ''), JSON.stringify(sent))
check('риск уходит без включения', sent.some(m => m.action === 'tool' && m.risk === 'HIGH' && m.enabled === false), JSON.stringify(sent))

// Форма нового сервера: открытые и секретные переменные разделяются.
view.click({ action: 'mcp-form-open' })
view.type('form.displayName', 'Sentry')
view.type('form.command', 'npx')
view.type('form.args', '-y\n@sentry/mcp-server@0.18.0\n')
view.type('form.env', 'SENTRY_HOST=sentry.local')
view.type('form.secretEnv', 'SENTRY_TOKEN=sntrys_secret_value')
view.click({ action: 'mcp-form-save' })
sent = view.take()
const save = sent.find(m => m.action === 'save')
check('форма уходит хосту', Boolean(save), JSON.stringify(sent))
if (save) {
  check('аргументы — по строке, без пустых', JSON.stringify(save.server.args) === JSON.stringify(['-y', '@sentry/mcp-server@0.18.0']), JSON.stringify(save.server.args))
  check('открытая переменная — в env', save.server.env.SENTRY_HOST === 'sentry.local', JSON.stringify(save.server.env))
  check('секрет — отдельно, по имени', save.secrets['env:SENTRY_TOKEN'] === 'sntrys_secret_value' && JSON.stringify(save.server.secretEnv) === '["SENTRY_TOKEN"]', JSON.stringify(save))
  check('секрет не попал в открытые переменные', !JSON.stringify(save.server.env).includes('sntrys'))
}
view.type('form.env', 'просто строка')
view.click({ action: 'mcp-form-save' })
check('строка без «=» не уходит хосту', !view.take().some(m => m.action === 'save'))
check('и объясняется', view.root.innerHTML.includes('ожидается ИМЯ=значение'))

// Импорт: текст уходит хосту, значения заглушек — по ключу.
view.click({ action: 'mcp-import-open' })
view.type('import.text', '{"mcpServers":{}}')
view.click({ action: 'mcp-import-preview' })
check('импорт отправляет текст конфига', view.take().some(m => m.action === 'importPreview' && m.text === '{"mcpServers":{}}'))
view.send({ type: 'mcpImportPreview', candidates: [{ name: 'jira', server: { transport: 'http', url: 'https://jira.local/mcp' }, needsValue: ['header:Authorization'], secretNames: [] }] })
view.type('importValue:jira:header:Authorization', 'Bearer abc')
view.click({ action: 'mcp-import-add', name: 'jira' })
check('кандидат добавляется со значением заглушки', view.take().some(m => m.action === 'importAdd' && m.values['header:Authorization'] === 'Bearer abc'))

// Плагин GitLab: токен уходит один раз и не остаётся в черновике.
view.type('plugin.url', 'https://gitlab.company.local')
view.type('plugin.token', 'glpat-secret-token-value-123456')
view.click({ action: 'gitlab-plugin-save' })
sent = view.take()
const plugin = sent.find(m => m.type === 'gitlabAction' && m.action === 'savePlugin')
check('плагин сохраняется с адресом и токеном', plugin?.url === 'https://gitlab.company.local' && plugin?.token === 'glpat-secret-token-value-123456', JSON.stringify(sent))
view.click({ action: 'gitlab-plugin-save' })
const again = view.take().find(m => m.action === 'savePlugin')
check('токен не хранится в черновике после отправки', again && again.token === '', JSON.stringify(again))
check('токен не рисуется в поле', !view.root.innerHTML.includes('glpat-secret-token-value-123456'))

if (failures.length) {
  console.error('Интеграции Гильдии: проверки провалены')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('интеграции Гильдии: ok')
