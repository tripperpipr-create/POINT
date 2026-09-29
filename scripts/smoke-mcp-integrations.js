// Общие настройки → «Интеграции и MCP» и вкладка проекта Гильдия → «GitLab».
//
// Что проверяется:
// - страница сама спрашивает хост о серверах и о здоровье плагина GitLab
//   (scope plugin), и ровно один раз;
// - общая карточка GitLab не правит связь проекта, а называет её одной
//   строкой «Этот проект» со ссылкой на вкладку проекта;
// - вкладка проекта спрашивает связь своего мира, показывает «не связан»
//   без сбоя и сохраняет выбор тем же сообщением, что окно GitLab;
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
const fs = require('fs')
const path = require('path')

const failures = []
const check = (name, ok, detail = '') => { if (!ok) failures.push(`${name}${detail ? `: ${detail}` : ''}`) }
const manifest = require('../vscode-extension/package.json')
const gitlabContainer = manifest.contributes.viewsContainers.activitybar.find(item => item.id === 'pointGitLab')
check('контейнер окна GitLab имеет SVG-значок', gitlabContainer?.icon?.endsWith('.svg') && fs.existsSync(path.join(__dirname, '..', 'vscode-extension', gitlabContainer.icon)))
const gitlabController = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'gitlab-controller.js'), 'utf8')
const nativeToolWindows = fs.readFileSync(path.join(__dirname, '..', 'distribution', 'resources', 'point-tool-windows.ts.txt'), 'utf8')
const integrationsController = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'integrations-controller.js'), 'utf8')
check('кнопка окна GitLab открывает panel', gitlabController.includes("createWebviewPanel('point.gitlabTools'"))
check('команда окна GitLab зарегистрирована расширением', integrationsController.includes("registerCommand('localAgent.openGitLabWindow'"))
check('меню окна GitLab вызывает команду расширения', nativeToolWindows.includes("'GitLab', 'git-merge', 'localAgent.openGitLabWindow'"))

const view = bootWebview({ layout: 'wide' })
view.state({ selectedTab: 'integrations' })
let sent = view.take()
check('вкладка спрашивает список серверов', sent.some(m => m.type === 'mcpAction' && m.action === 'list'), JSON.stringify(sent))
check('страница спрашивает здоровье плагина GitLab', sent.some(m => m.type === 'gitlabAction' && m.action === 'status' && m.surface === 'hub' && m.scope === 'plugin'), JSON.stringify(sent))
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
view.send({ type: 'gitlabStatus', scope: 'plugin', response: { state: 'error', reason: 'not_configured', problem: 'GitLab не подключён', data: { configured: false } } })
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

// Подключённый GitLab: карточка называет связь текущего проекта и ведёт в
// его настройки, но сама её не правит.
view.send({ type: 'mcpServers', servers: [{ id: 'mcp-gitlab', displayName: 'GitLab', kind: 'gitlab', transport: 'stdio', command: 'npx', args: [], trusted: true,
  outsideSandbox: true, status: 'connected', runtime: {}, settings: { url: 'https://gitlab.company.local' }, tools: [], secretRefs: {} }] })
view.take()
view.send({ type: 'gitlabStatus', scope: 'plugin', response: { state: 'ok', data: { configured: true, url: 'https://gitlab.company.local', user: { username: 'anna' }, linked: false,
  binding: { mode: 'off', workspace: 'dotfiles', note: 'origin ведёт на github.com' } } } })
html = view.root.innerHTML
check('карточка подключена независимо от проекта', html.includes('подключён · @anna'), html.slice(0, 300))
check('строка «Этот проект» называет связь', html.includes('dotfiles: не связан'))
check('из карточки — переход во вкладку проекта', html.includes('data-tab="project-gitlab"'))
check('карточка не правит связь сама', !html.includes('data-action="gitlab-binding-save"'))
check('проектный факт окна ушёл с общей страницы', !html.includes('Проект окна'))
view.click({ action: 'gitlab-plugin-check' })
check('«Проверить» запрашивает новый снимок MCP', view.take().some(m => m.type === 'mcpAction' && m.action === 'probe' && m.id === 'mcp-gitlab'))
view.send({ type: 'mcpServers', probed: 'mcp-gitlab', servers: [] })
check('после проверки запрашивается здоровье GitLab', view.take().some(m => m.type === 'gitlabAction' && m.action === 'status' && m.scope === 'plugin'))

// Вкладка проекта «GitLab».
view.state({ selectedTab: 'project-gitlab', workspacePath: 'C:/work/dotfiles' })
sent = view.take()
check('вкладка проекта спрашивает связь своего мира', sent.some(m => m.type === 'gitlabAction' && m.action === 'status' && m.surface === 'hub' && !m.scope), JSON.stringify(sent))
view.send({ type: 'gitlabStatus', response: { state: 'ok', data: { configured: true, url: 'https://gitlab.company.local', linked: false,
  binding: { mode: 'off', workspace: 'dotfiles', remote: 'github.com/anna/dotfiles', note: 'связь с GitLab отключена в настройках проекта' } } } })
html = view.root.innerHTML
check('вкладка проекта — в Гильдии', html.includes('data-tab="project-gitlab" aria-current="page"'), html.slice(0, 600))
check('«не связан» — без сбоя', html.includes('не связан') && !html.includes('int-problem'), html.slice(0, 400))
check('выбор «Не связывать» отмечен', /value="off" data-action="gitlab-binding-mode" checked/.test(html))
check('редактор на вкладке без «Отмены»', html.includes('data-action="gitlab-binding-save"') && !html.includes('data-action="gitlab-binding-cancel"'))
view.click({ action: 'gitlab-binding-mode' }, { value: 'auto' })
view.click({ action: 'gitlab-binding-save' })
sent = view.take()
check('выбор уходит хосту тем же сообщением, что из окна', sent.some(m => m.action === 'binding' && m.mode === 'auto' && m.surface === 'hub'), JSON.stringify(sent))
view.send({ type: 'gitlabBinding', response: { state: 'error', reason: 'bad_request', problem: 'откройте папку проекта' } })
check('отказ сохранения виден на вкладке', view.root.innerHTML.includes('откройте папку проекта'))
view.click({ action: 'mcp-dismiss-error' })
view.send({ type: 'gitlabStatus', response: { state: 'ok', data: { configured: true, url: 'https://gitlab.company.local', linked: false,
  binding: { mode: 'off', workspace: 'payments', detected: 'billing/payments', note: 'связь с GitLab отключена в настройках проекта' } } } })
check('вкладка предлагает связать найденный проект одним кликом', view.root.innerHTML.includes('Связать с billing/payments'))
view.take()
view.click({ action: 'gitlab-link-detected' })
check('один клик на вкладке сохраняет связь «по git remote»', view.take().some(m => m.action === 'binding' && m.mode === 'auto' && m.surface === 'hub'))
view.send({ type: 'gitlabStatus', response: { state: 'error', reason: 'not_configured', problem: 'GitLab не подключён', data: { configured: false, binding: { mode: 'off', workspace: 'dotfiles' } } } })
html = view.root.innerHTML
check('без подключения вкладка ведёт в общие настройки', html.includes('GitLab не подключён') && html.includes('data-tab="integrations"') && !html.includes('gitlab-binding-save'))

if (failures.length) {
  console.error('Интеграции: проверки провалены')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('интеграции: ok')
