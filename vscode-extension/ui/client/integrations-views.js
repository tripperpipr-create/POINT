// Гильдия → «Интеграции»: встроенные плагины сверху, свои MCP-серверы и
// импорт mcp.json ниже.
//
// Страница говорит правду о трёх вещах, которые владелец обязан видеть до
// запуска: что именно запустится на его машине (программа, аргументы,
// переменные), что сервер работает вне песочницы Point и какие инструменты
// сервера включены для агентов. Значения секретов сюда не приходят никогда —
// только их имена.

import { esc } from './html-escape.js'
import { countOf } from './format-units.js'
import { draftId, glIcon, timeAgo } from './gitlab-views.js'

const RISKS = [['LOW', 'Н', 'низкий'], ['MEDIUM', 'С', 'средний'], ['HIGH', 'В', 'высокий'], ['CRITICAL', 'К', 'критический']]
const TOOL_STATE = { new: ['new', 'новый'], changed: ['warn', 'изменился — посмотрите'], missing: ['mute', 'пропал'], ok: ['', ''] }
const GITLAB_ID = 'mcp-gitlab'

function commandLine(server) {
  if (server.transport === 'http') return server.url || ''
  return [server.command, ...(server.args || [])].map(part => /\s/.test(part) ? `"${part}"` : part).join(' ')
}

function serverState(server) {
  if (server.transport === 'stdio' && !server.trusted) return ['warn', 'ждёт доверия']
  if (server.secretsLocked?.length) return ['warn', 'нет секрета']
  if (server.runtime?.broken) return ['bad', 'остановлен после сбоев']
  if (server.status === 'error') return ['bad', 'ошибка']
  if (server.runtime?.running) return ['ok', 'работает']
  if (server.status === 'connected') return ['ok', 'проверен']
  return ['mute', 'не проверен']
}

export function createIntegrationsViews({ getState, shell, toolPageHeading }) {
  const field = (label, key, value, { placeholder = '', type = 'text', hint = '', mono = false } = {}) =>
    `<label class="int-field"><span>${esc(label)}${hint ? ` <small>${esc(hint)}</small>` : ''}</span><input id="${draftId(key)}" class="gl-input${mono ? ' is-mono' : ''}" type="${type}" data-draft="${esc(key)}" value="${esc(value)}" placeholder="${esc(placeholder)}" autocomplete="off" spellcheck="false"></label>`
  const area = (label, key, value, { placeholder = '', hint = '', rows = 3 } = {}) =>
    `<label class="int-field"><span>${esc(label)}${hint ? ` <small>${esc(hint)}</small>` : ''}</span><textarea id="${draftId(key)}" class="gl-input is-mono" rows="${rows}" data-draft="${esc(key)}" placeholder="${esc(placeholder)}" spellcheck="false">${esc(value)}</textarea></label>`

  function problem(server) {
    if (!server.problem) return ''
    return `<div class="int-problem">${glIcon('warning', 13)}<p><b>${esc(server.problem)}</b>${server.fix ? `<span>${esc(server.fix)}</span>` : ''}</p></div>`
  }

  function lockedSecrets(state, server) {
    if (!server.secretsLocked?.length) return ''
    return `<div class="int-secrets">${server.secretsLocked.map(name => {
      const kind = server.secretEnv?.[name] ? 'env' : 'header'
      const key = `secret:${server.id}:${kind}:${name}`
      return `<label class="int-inline"><span>${esc(name)}</span><input id="${draftId(key)}" class="gl-input" type="password" data-draft="${esc(key)}" value="${esc(state.drafts[key] || '')}" placeholder="значение ляжет в SecretStorage" autocomplete="off"><button type="button" class="gl-btn" data-action="mcp-secret" data-id="${esc(server.id)}" data-key="${esc(`${kind}:${name}`)}" data-draft-key="${esc(key)}">Сохранить</button></label>`
    }).join('')}</div>`
  }

  function toolsTable(state, server) {
    const tools = server.tools || []
    if (!tools.length) return `<p class="int-muted">Инструментов ещё нет — нажмите «Проверить», чтобы снять список.</p>`
    return `<div class="int-tools" role="table" aria-label="Инструменты ${esc(server.displayName)}">
      <p class="int-muted">Включённый инструмент получат агенты, которым вы его выдадите. Описание инструмента попадает в промпт модели: если сервер его изменит, инструмент выключится до вашего взгляда.</p>
      ${tools.map(tool => {
        const [tone, label] = TOOL_STATE[tool.state] || ['', '']
        return `<div class="int-tool${tool.enabled ? ' is-on' : ''}" role="row">
          <label class="int-tool-toggle" title="${esc(tool.state === 'missing' ? 'Сервер больше не отдаёт этот инструмент' : tool.enabled ? 'Выключить для агентов' : 'Включить для агентов — одобряет текущее описание')}"><input type="checkbox" data-action="mcp-tool-toggle" data-id="${esc(server.id)}" data-name="${esc(tool.name)}"${tool.enabled ? ' checked' : ''}${tool.state === 'missing' ? ' disabled' : ''}></label>
          <span class="int-tool-main"><b>${esc(tool.name)}</b>${label ? `<em class="is-${tone}">${esc(label)}</em>` : ''}<small>${esc(tool.title || tool.description || '')}</small></span>
          <span class="int-risk" role="group" aria-label="Риск">${RISKS.map(([value, short, title]) => `<button type="button" class="${tool.risk === value ? 'is-on' : ''} is-${value.toLowerCase()}" data-action="mcp-tool-risk" data-id="${esc(server.id)}" data-name="${esc(tool.name)}" data-risk="${value}" data-enabled="${tool.enabled ? '1' : '0'}" title="Риск: ${esc(title)}" aria-pressed="${tool.risk === value ? 'true' : 'false'}">${short}</button>`).join('')}</span>
        </div>`
      }).join('')}
    </div>`
  }

  function serverCard(state, server) {
    const [tone, label] = serverState(server)
    const open = state.toolsOpen === server.id
    const logOpen = state.logOpen === server.id
    const stdio = server.transport === 'stdio'
    const enabled = (server.tools || []).filter(tool => tool.enabled).length
    return `<article class="int-server" data-server="${esc(server.id)}">
      <header>
        <i class="int-dot is-${tone}" aria-hidden="true"></i>
        <div><strong>${esc(server.displayName)}</strong><small>${stdio ? 'на этом компьютере' : esc(`по адресу · ${server.allowPrivateHost ? 'внутренняя сеть разрешена' : 'публичная сеть'}`)}${server.serverVersion ? ` · ${esc(server.serverName || '')} ${esc(server.serverVersion)}` : ''}</small></div>
        <span class="int-state is-${tone}">${esc(label)}</span>
        ${stdio ? '<span class="int-sandbox" title="Процесс сервера не ограничен песочницей Point: у него ваши права, файлы и сеть">вне песочницы</span>' : ''}
      </header>
      <code class="int-command" title="${esc(server.resolvedPreview || '')}">${esc(commandLine(server))}</code>
      ${problem(server)}
      ${lockedSecrets(state, server)}
      <footer class="int-actions">
        ${stdio && !server.trusted ? `<button type="button" class="gl-btn is-primary" data-action="mcp-trust" data-id="${esc(server.id)}"${state.busy ? ' disabled' : ''}>Доверяю…</button>` : ''}
        <button type="button" class="gl-btn" data-action="mcp-probe" data-id="${esc(server.id)}"${state.busy ? ' disabled' : ''}>Проверить</button>
        <button type="button" class="gl-btn${open ? ' is-on' : ''}" data-action="mcp-tools-toggle" data-id="${esc(server.id)}" aria-expanded="${open ? 'true' : 'false'}">Инструменты <b>${enabled}/${(server.tools || []).length}</b></button>
        <button type="button" class="gl-btn${logOpen ? ' is-on' : ''}" data-action="mcp-log" data-id="${esc(server.id)}" aria-expanded="${logOpen ? 'true' : 'false'}">Журнал</button>
        <i class="nc-gap"></i>
        ${server.kind === 'custom' ? `<button type="button" class="gl-btn is-quiet" data-action="mcp-edit" data-id="${esc(server.id)}">Изменить</button>` : ''}
        ${server.runtime?.running ? `<button type="button" class="gl-btn is-quiet" data-action="mcp-stop" data-id="${esc(server.id)}">Остановить</button>` : ''}
        <button type="button" class="gl-btn is-quiet is-danger" data-action="mcp-delete" data-id="${esc(server.id)}">Удалить</button>
      </footer>
      ${open ? toolsTable(state, server) : ''}
      ${logOpen ? `<pre class="int-log">${esc(state.logs[server.id] ?? 'Загружаем журнал…') || 'Журнал пуст.'}</pre>` : ''}
    </article>`
  }

  function gitlabCard(state) {
    const server = (state.servers || []).find(item => item.id === GITLAB_ID)
    const status = state.status
    const editing = state.pluginEditing || !server
    const user = status?.data?.user
    let pill = ['mute', 'не подключён']
    if (server) pill = serverState(server)
    if (server && status?.state === 'ok') pill = ['ok', user?.username ? `подключён · @${user.username}` : 'подключён']
    else if (server && status?.state === 'error' && !['not_trusted', 'secret_locked'].includes(status.reason)) pill = ['bad', 'ошибка']
    const url = server?.settings?.url || ''
    const form = editing ? `<div class="int-form">
      ${field('Адрес GitLab', 'plugin.url', state.drafts['plugin.url'] ?? url, { placeholder: 'https://gitlab.company.local' })}
      ${field('Личный токен доступа', 'plugin.token', state.drafts['plugin.token'] || '', { type: 'password', placeholder: server ? 'сохранён — оставьте пустым, чтобы не менять' : 'glpat-…', hint: 'права api; для просмотра хватит read_api' })}
      ${field('Сертификат внутреннего CA', 'plugin.caPath', state.drafts['plugin.caPath'] ?? server?.settings?.caPath ?? '', { placeholder: 'C:\\certs\\company-ca.pem', hint: 'необязательно — если GitLab подписан своим центром', mono: true })}
      <p class="int-muted">Point запустит закреплённый сервер <code>@zereight/mcp-gitlab@2.1.66</code> через npx на этом компьютере. Токен ляжет в SecretStorage IDE и в память ядра — в базу Point и журналы он не попадает.</p>
      <footer class="int-actions"><button type="button" class="gl-btn is-primary" data-action="gitlab-plugin-save"${state.busy ? ' disabled' : ''}>${server ? 'Сохранить' : 'Подключить'}</button>${server ? '<button type="button" class="gl-btn is-quiet" data-action="gitlab-plugin-cancel">Отмена</button>' : ''}</footer>
    </div>` : ''
    const details = server && !editing ? `<dl class="int-facts">
        <dt>Адрес</dt><dd>${esc(url)}</dd>
        <dt>Проект окна</dt><dd>${esc(status?.data?.binding?.mode === 'all' ? 'все мои проекты' : status?.data?.binding?.project || status?.data?.binding?.note || '—')}</dd>
        <dt>Сервер</dt><dd><code>${esc(status?.data?.pinned || '@zereight/mcp-gitlab@2.1.66')}</code> · вне песочницы</dd>
      </dl>
      ${status?.state === 'error' ? `<div class="int-problem">${glIcon('warning', 13)}<p><b>${esc(status.problem || '')}</b>${status.fix ? `<span>${esc(status.fix)}</span>` : ''}</p></div>` : ''}
      ${lockedSecrets(state, server)}
      <footer class="int-actions">
        ${!server.trusted ? `<button type="button" class="gl-btn is-primary" data-action="mcp-trust" data-id="${GITLAB_ID}"${state.busy ? ' disabled' : ''}>Доверяю…</button>` : ''}
        <button type="button" class="gl-btn" data-action="gitlab-plugin-check"${state.busy ? ' disabled' : ''}>Проверить</button>
        <button type="button" class="gl-btn" data-action="gitlab-open-window">Окно GitLab</button>
        <button type="button" class="gl-btn${state.toolsOpen === GITLAB_ID ? ' is-on' : ''}" data-action="mcp-tools-toggle" data-id="${GITLAB_ID}">Инструменты</button>
        <button type="button" class="gl-btn${state.logOpen === GITLAB_ID ? ' is-on' : ''}" data-action="mcp-log" data-id="${GITLAB_ID}">Журнал</button>
        <i class="nc-gap"></i>
        <button type="button" class="gl-btn is-quiet" data-action="gitlab-plugin-edit">Изменить</button>
        <button type="button" class="gl-btn is-quiet is-danger" data-action="mcp-delete" data-id="${GITLAB_ID}">Отключить</button>
      </footer>
      ${state.toolsOpen === GITLAB_ID ? toolsTable(state, server) : ''}
      ${state.logOpen === GITLAB_ID ? `<pre class="int-log">${esc(state.logs[GITLAB_ID] ?? 'Загружаем журнал…') || 'Журнал пуст.'}</pre>` : ''}` : ''
    return `<article class="int-plugin">
      <header><span class="int-plugin-mark">${glIcon('mr', 18)}</span><div><strong>GitLab</strong><small>Merge requests, пайплайны и ревью в окне IDE</small></div><span class="int-state is-${pill[0]}">${esc(pill[1])}</span></header>
      ${details}${form}
    </article>`
  }

  function serverForm(state) {
    const editing = (state.servers || []).find(item => item.id === state.formId)
    const d = key => state.drafts[`form.${key}`]
    const transport = d('transport') || editing?.transport || 'stdio'
    const lines = (value, sep) => Object.entries(value || {}).map(([k, v]) => `${k}${sep}${v}`).join('\n')
    const secretNames = kind => Object.keys((kind === 'env' ? editing?.secretEnv : editing?.secretHeaders) || {}).map(name => `${name}${kind === 'env' ? '=' : ': '}`).join('\n')
    const seg = `<div class="nc-seg" role="group" aria-label="Как запускается сервер">${[['stdio', 'На этом компьютере'], ['http', 'По адресу (HTTP)']].map(([id, label]) =>
      `<button type="button" class="${transport === id ? 'is-on' : ''}" data-action="mcp-form-transport" data-transport="${id}" aria-pressed="${transport === id ? 'true' : 'false'}">${esc(label)}</button>`).join('')}</div>`
    const body = transport === 'http'
      ? `${field('Адрес сервера', 'form.url', d('url') ?? editing?.url ?? '', { placeholder: 'https://mcp.company.local/mcp', mono: true })}
        ${area('Заголовки', 'form.headers', d('headers') ?? lines(editing?.headers, ': '), { placeholder: 'X-Team: platform', hint: 'по одному в строке, открытые значения' })}
        ${area('Секретные заголовки', 'form.secretHeaders', d('secretHeaders') ?? secretNames('header'), { placeholder: 'Authorization: Bearer …', hint: 'значение — в SecretStorage; пустое значение сохраняет прежнее' })}
        <label class="gl-check"><input type="checkbox" id="${draftId('form.allowPrivate')}" data-draft="form.allowPrivate"${d('allowPrivate') ?? Boolean(editing?.allowPrivateHost) ? ' checked' : ''}><span>Разрешить узел во внутренней сети или VPN — только этот узел из адреса</span></label>`
      : `${field('Команда', 'form.command', d('command') ?? editing?.command ?? '', { placeholder: 'npx', mono: true, hint: 'программа, а не оболочка: sh -c и cmd /c не принимаются' })}
        ${area('Аргументы', 'form.args', d('args') ?? (editing?.args || []).join('\n'), { placeholder: '-y\n@scope/server@1.2.3', hint: 'по одному в строке; закрепляйте версию пакета' })}
        ${field('Папка запуска', 'form.dir', d('dir') ?? editing?.dir ?? '', { placeholder: 'необязательно', mono: true })}
        ${area('Переменные окружения', 'form.env', d('env') ?? lines(editing?.env, '='), { placeholder: 'LOG_LEVEL=info', hint: 'открытые значения хранятся в базе Point' })}
        ${area('Секретные переменные', 'form.secretEnv', d('secretEnv') ?? secretNames('env'), { placeholder: 'API_TOKEN=…', hint: 'значение — в SecretStorage; пустое значение сохраняет прежнее' })}`
    return `<section class="int-form is-card" aria-label="${editing ? 'Изменить сервер' : 'Новый MCP-сервер'}">
      <header><strong>${editing ? `Изменить «${esc(editing.displayName)}»` : 'Новый MCP-сервер'}</strong>${seg}</header>
      ${field('Название', 'form.displayName', d('displayName') ?? editing?.displayName ?? '', { placeholder: 'например, Sentry' })}
      ${body}
      ${transport === 'stdio' ? '<p class="int-muted">После сохранения сервер не стартует, пока вы не посмотрите команду и не нажмёте «Доверяю». Любая правка команды снимет доверие.</p>' : ''}
      <footer class="int-actions"><button type="button" class="gl-btn is-primary" data-action="mcp-form-save"${state.busy ? ' disabled' : ''}>Сохранить</button><button type="button" class="gl-btn is-quiet" data-action="mcp-form-cancel">Отмена</button></footer>
    </section>`
  }

  function importPanel(state) {
    const candidates = state.importCandidates || []
    return `<section class="int-form is-card" aria-label="Импорт mcp.json">
      <header><strong>Импорт mcp.json</strong><small>формат Claude, Cursor или VS Code</small></header>
      ${area('Конфиг', 'import.text', state.drafts['import.text'] || '', { rows: 5, placeholder: '{ "mcpServers": { "sentry": { "command": "npx", "args": ["-y", "@sentry/mcp-server@1.0.0"] } } }' })}
      <footer class="int-actions"><button type="button" class="gl-btn is-primary" data-action="mcp-import-preview"${state.busy ? ' disabled' : ''}>Разобрать</button><button type="button" class="gl-btn" data-action="mcp-import-pick">Выбрать файл…</button><i class="nc-gap"></i><button type="button" class="gl-btn is-quiet" data-action="mcp-import-close">Закрыть</button></footer>
      ${candidates.map(candidate => {
        const server = candidate.server || {}
        return `<article class="int-candidate${candidate.refused ? ' is-refused' : ''}">
          <header><strong>${esc(candidate.name)}</strong>${candidate.refused ? '<span class="int-state is-bad">не принят</span>' : `<span class="int-state is-mute">${server.transport === 'http' ? 'удалённый' : 'локальный'}</span>`}</header>
          ${candidate.refused ? `<p class="int-muted">${esc(candidate.refused)}</p>` : `<code class="int-command">${esc(commandLine(server))}</code>`}
          ${(candidate.warnings || []).map(line => `<p class="int-warn">${glIcon('warning', 12)} ${esc(line)}</p>`).join('')}
          ${candidate.secretNames?.length ? `<p class="int-muted">Секреты из файла лягут в SecretStorage: ${esc(candidate.secretNames.map(key => key.replace(/^(env|header):/, '')).join(', '))}</p>` : ''}
          ${(candidate.needsValue || []).map(key => field(`Значение ${key.replace(/^(env|header):/, '')}`, `importValue:${candidate.name}:${key}`, state.drafts[`importValue:${candidate.name}:${key}`] || '', { type: 'password', hint: 'в файле заглушка — введите значение' })).join('')}
          ${candidate.refused ? '' : `<footer class="int-actions"><button type="button" class="gl-btn" data-action="mcp-import-add" data-name="${esc(candidate.name)}"${state.busy ? ' disabled' : ''}>Добавить</button></footer>`}
        </article>`
      }).join('')}
    </section>`
  }

  function journal(state) {
    if (!state.journalOpen) return ''
    const actions = state.actions
    if (!actions) return '<p class="int-muted">Загружаем журнал…</p>'
    if (!actions.length) return '<p class="int-muted">Действий во внешних сервисах ещё не было.</p>'
    return `<ul class="int-journal">${actions.map(item => `<li class="${item.outcome === 'ok' ? '' : 'is-bad'}"><time title="${esc(item.at)}">${esc(timeAgo(item.at))}</time><b>${esc(item.tool)}</b><span>${esc(item.target)}</span><em>${item.outcome === 'ok' ? 'выполнено' : esc(item.error || 'отказ')}</em></li>`).join('')}</ul>`
  }

  function integrationsView() {
    const state = getState()
    const custom = (state.servers || []).filter(server => server.kind !== 'gitlab')
    const servers = state.servers === undefined
      ? '<div class="gl-loading"><span class="spinner"></span>Читаем серверы…</div>'
      : custom.length ? custom.map(server => serverCard(state, server)).join('')
        : '<div class="point-tool-empty compact"><strong>Своих серверов нет</strong><p>Добавьте сервер формой или импортируйте mcp.json из Claude, Cursor или VS Code.</p></div>'
    return shell(`<main class="hub-page int-page">
      ${toolPageHeading('Интеграции', 'Сервисы и MCP-серверы', 'Point работает с GitLab и другими сервисами через MCP. Встроенный плагин рисует своё окно; любой MCP-сервер даёт инструменты, которые вы выдаёте агентам сами.', '')}
      ${state.error ? `<div class="int-problem is-banner">${glIcon('warning', 13)}<p><b>${esc(state.error)}</b></p><button type="button" class="nc-icon-btn" data-action="mcp-dismiss-error" aria-label="Скрыть">${glIcon('x', 12)}</button></div>` : ''}
      <section class="int-section" aria-labelledby="int-plugins"><h2 id="int-plugins">Плагины</h2>
        ${gitlabCard(state)}
        <article class="int-plugin is-soon"><header><span class="int-plugin-mark">J</span><div><strong>Jira</strong><small>Задачи и статусы — следующий плагин; сейчас подключается как свой MCP-сервер</small></div><span class="int-state is-mute">в плане</span></header></article>
      </section>
      <section class="int-section" aria-labelledby="int-servers"><h2 id="int-servers">Свои MCP-серверы <small>${esc(countOf(custom.length, 'сервер', 'сервера', 'серверов'))}</small></h2>
        ${state.formOpen || state.importOpen ? '' : '<div class="int-actions is-head"><button type="button" class="gl-btn" data-action="mcp-form-open">Добавить сервер</button><button type="button" class="gl-btn" data-action="mcp-import-open">Импорт mcp.json</button></div>'}
        ${state.formOpen ? serverForm(state) : ''}
        ${state.importOpen ? importPanel(state) : ''}
        ${servers}
      </section>
      <section class="int-section" aria-labelledby="int-journal"><h2 id="int-journal">Журнал действий</h2>
        <p class="int-muted">Что Point сделал во внешних сервисах от вашего имени: комментарии, одобрения, merge, перезапуски. Текст комментариев не хранится — только отпечаток и длина.</p>
        <div class="int-actions is-head"><button type="button" class="gl-btn" data-action="integrations-journal">${state.journalOpen ? 'Обновить' : 'Показать'}</button></div>
        ${journal(state)}
      </section>
    </main>`)
  }

  return { integrationsView }
}
