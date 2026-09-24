// Базы данных, внешние модели и SSH образуют одну инфраструктурную поверхность.
// Здесь только представление: состояние и команды по-прежнему принадлежат
// главному контроллеру webview и передаются явными зависимостями.
export function createInfrastructureViews({
  getState,
  getDatabaseState,
  getEditingState = () => ({}),
  setDatabaseSelectedId,
  shell,
  escapeHtml,
  countOf,
  connectionStatusLabels,
  connectionManagerHtml,
  toolWindowFrame,
  isConnectionsView = () => false,
}) {
  const esc = escapeHtml
  // Базы и Серверы живут двумя жизнями: окном нижней панели и вкладкой Хаба.
  // Шапку выбирает рамка, а не эта страница, — иначе окна, стоящие в панели
  // одним рядом, выглядели бы из разных продуктов.
  const { isToolWindow, toolPageHeading, toolWindowEmpty } = toolWindowFrame

  function dbDriverLabel(driver) {
    return ({ sqlite: 'SQLite', postgres: 'PostgreSQL', mysql: 'MySQL' })[driver] || driver || 'БД'
  }

  function databaseCards(items, selected) {
    if (!items.length) {
      return toolWindowEmpty('Подключений пока нет', 'Добавьте SQLite-файл или PostgreSQL/MySQL формой ниже.', '', true)
    }
    return `<section class="connection-list">${items.map(item => `
      <article class="hub-card ${item.id === selected?.id ? 'selected' : ''}">
        <header>
          <strong>${esc(item.displayName || item.database)}</strong>
          <span>${esc(connectionStatusLabels[item.status] || item.status || 'Неизвестно')}</span>
        </header>
        <small>${esc(dbDriverLabel(item.driver))} · ${esc(item.host || item.database || '')}${item.secretRef ? ' · пароль в SecretStorage' : ''}</small>
        ${item.lastError ? `<p class="create-step-error">${esc(item.lastError)}</p>` : ''}
        <footer class="hub-card-footer">
          <button type="button" class="secondary" data-action="select-db" data-id="${esc(item.id)}">Выбрать</button>
          <button type="button" class="secondary" data-action="test-db" data-id="${esc(item.id)}">Проверить</button>
          <button type="button" class="secondary" data-action="schema-db" data-id="${esc(item.id)}">Схема</button>
          <button type="button" class="secondary" data-action="edit-db" data-id="${esc(item.id)}">Редактировать</button>
          <button type="button" class="danger-button" data-action="delete-db" data-id="${esc(item.id)}">Удалить</button>
        </footer>
      </article>`).join('')}</section>`
  }

  function databaseConnectionForm(items) {
    const editingId = getEditingState().dbId || ''
    const editing = items.find(item => item.id === editingId)
    const driver = editing?.driver || 'sqlite'
    const passwordHint = editing?.secretRef
      ? 'Оставьте пустым, чтобы сохранить текущий'
      : 'SecretStorage IDE'
    return `
      <form id="db-connection-form" class="connection-form companion-new-connection">
        <h3>${editing ? 'Изменить подключение' : 'Новое подключение'}</h3>
        <div class="settings-grid">
          <label>Название<input id="db-name" maxlength="120" value="${esc(editing?.displayName || '')}" placeholder="app-db"></label>
          <label>Драйвер<select id="db-driver">
            <option value="sqlite" ${driver === 'sqlite' ? 'selected' : ''}>SQLite</option>
            <option value="postgres" ${driver === 'postgres' ? 'selected' : ''}>PostgreSQL</option>
            <option value="mysql" ${driver === 'mysql' ? 'selected' : ''}>MySQL</option>
          </select></label>
          <label>Хост<input id="db-host" value="${esc(editing?.host || '')}" placeholder="127.0.0.1 (не для SQLite)"></label>
          <label>Порт<input id="db-port" type="number" min="1" max="65535" value="${esc(editing?.port || '')}" placeholder="5432 / 3306"></label>
          <label>База / путь к файлу<input id="db-database" required value="${esc(editing?.database || '')}" placeholder="data/app.db или mydb"></label>
          <label>Пользователь<input id="db-user" value="${esc(editing?.username || '')}" placeholder="postgres"></label>
          <label>SSL mode<input id="db-ssl" value="${esc(editing?.sslMode || '')}" placeholder="prefer (postgres)"></label>
          <label>Пароль<input id="db-password" type="password" autocomplete="off" placeholder="${passwordHint}"></label>
        </div>
        <div class="hub-card-footer">
          <button class="primary" type="submit">${editing ? 'Сохранить изменения' : 'Сохранить подключение'}</button>
          ${editing ? '<button type="button" class="secondary" data-action="cancel-edit-db">Отменить</button>' : ''}
        </div>
      </form>`
  }

  function databaseResult(databaseState) {
    const result = databaseState.queryResult
    if (result) {
      const table = Array.isArray(result.columns) && result.columns.length
        ? `<div class="db-table-wrap"><table class="db-table"><thead><tr>${result.columns.map(col => `<th>${esc(col)}</th>`).join('')}</tr></thead><tbody>${(result.rows || []).map(row => `<tr>${row.map(cell => `<td>${esc(cell == null ? 'NULL' : cell)}</td>`).join('')}</tr>`).join('')}</tbody></table></div>`
        : ''
      return `<section class="db-query-result">
        <header><strong>Результат</strong><small>${esc(result.kind || '')} · ${esc(countOf(result.rowCount ?? 0, 'строка', 'строки', 'строк'))}${result.truncated ? ' · обрезано' : ''}${result.rowsAffected != null ? ` · затронуто ${result.rowsAffected}` : ''}</small></header>
        ${result.message ? `<p>${esc(result.message)}</p>` : ''}${table}
      </section>`
    }
    if (databaseState.queryStatus === 'loading') return '<p class="muted">Выполняется SQL…</p>'
    if (databaseState.queryStatus === 'error') {
      return '<p class="muted">Запрос не выполнен: ядро не ответило. Результата нет — это не пустая выборка.</p>'
    }
    return ''
  }

  function databaseSchema(databaseState) {
    const schema = databaseState.schemaResult
    if (!schema) return ''
    const tables = (schema.tables || []).map(table => `
      <article>
        <strong>${esc([table.schema, table.name].filter(Boolean).join('.'))}</strong>
        <small>${esc(table.type || '')}${(table.columns || []).length ? ' · ' + esc((table.columns || []).join(', ')) : ''}</small>
      </article>`).join('') || '<p class="muted">Таблиц нет.</p>'
    return `<section class="db-schema"><header><strong>Схема · ${esc(dbDriverLabel(schema.driver))}</strong></header><div class="hub-skill-list">${tables}</div></section>`
  }

  function databasesView() {
    const state = getState()
    const databaseState = getDatabaseState()
    const items = state.boot?.dbConnections || []
    let selectedId = databaseState.selectedId
    if (!selectedId && items[0]?.id) {
      selectedId = items[0].id
      setDatabaseSelectedId(selectedId)
    }
    const selected = items.find(item => item.id === selectedId) || items[0]
    const writePending = databaseState.writePending
    const writeGate = writePending
      ? `<aside class="safety-note"><span>!</span><div><strong>Подтвердите запись в БД</strong><small>${esc(writePending.sql)}</small><div class="hub-card-footer"><button type="button" class="primary" data-action="apply-db-write">Выполнить</button><button type="button" class="secondary" data-action="ignore-db-write">Отменить</button></div></div></aside>`
      : ''
    const queryForm = selected
      ? `<form id="db-query-form" class="connection-form"><h3>SQL · ${esc(selected.displayName || selected.database)}</h3>${writeGate}<label>Запрос<textarea id="db-sql" rows="5" placeholder="SELECT * FROM ..."></textarea></label><div class="hub-card-footer"><button class="primary" type="submit">Выполнить</button><button type="button" class="secondary" data-action="schema-db" data-id="${esc(selected.id)}">Схема</button></div></form>`
      : ''
    return shell(`<main class="hub-databases hub-page">
      ${toolPageHeading('Базы данных', 'Подключения и SQL', 'SQLite, PostgreSQL и MySQL. Пароли только в SecretStorage. Запись — только после явного «Выполнить».', String(items.length))}
      <aside class="connection-secret-note"><span>i</span><div><strong>Поддерживаются sqlite · postgres · mysql</strong><small>Для удалённых хостов агенту нужен <code>network:&lt;host&gt;=ALLOW</code>. Инструменты: db_list_connections, db_schema, db_query, db_exec.</small></div></aside>
      ${databaseCards(items, selected)}
      ${databaseConnectionForm(items)}
      ${queryForm}
      ${databaseResult(databaseState)}
      ${databaseSchema(databaseState)}
    </main>`)
  }

  function featuredProviderChips(providers) {
    const pick = id => providers.find(item => item.id === id)
    const featured = ['llmux', 'custom', 'ollama', 'anthropic', 'openai', 'cursor'].map(pick).filter(Boolean)
    const extra = providers.filter(item => !featured.includes(item))
    return [...featured, ...extra].slice(0, 7)
  }

  function serverCards(servers) {
    if (!servers.length) {
      return toolWindowEmpty('SSH-профилей пока нет', 'Добавьте хост формой ниже или командой «Подключиться к серверу».', '', true)
    }
    return `<section class="connection-list">${servers.map(item => `
      <article class="hub-card">
        <header><strong>${esc(item.displayName || item.host)}</strong><span>${esc(connectionStatusLabels[item.status] || item.status || 'Неизвестно')}</span></header>
        <small>${esc(item.user || '')}@${esc(item.host || '')}:${esc(item.port || 22)} · ${esc(item.authMethod || 'agent')}${item.secretRef ? ' · пароль в SecretStorage' : ''}</small>
        ${item.lastError ? `<p class="create-step-error">${esc(item.lastError)}</p>` : ''}
        <footer class="hub-card-footer">
          <button type="button" class="secondary" data-action="probe-server" data-id="${esc(item.id)}">Проверить</button>
          <button type="button" class="primary" data-action="open-server-terminal" data-id="${esc(item.id)}">SSH-терминал</button>
          <button type="button" class="secondary" data-action="list-server-path" data-id="${esc(item.id)}" data-path="${esc(item.defaultRemotePath || '~')}">Файлы ${esc(item.defaultRemotePath || '~')}</button>
          <button type="button" class="secondary" data-action="edit-server" data-id="${esc(item.id)}">Редактировать</button>
          <button type="button" class="danger-button" data-action="delete-server" data-id="${esc(item.id)}">Удалить</button>
        </footer>
      </article>`).join('')}</section>`
  }

  function serverForm(servers) {
    const editingId = getEditingState().serverId || ''
    const editing = servers.find(item => item.id === editingId)
    const authMethod = editing?.authMethod || 'agent'
    const passwordHint = editing?.secretRef
      ? 'Оставьте пустым, чтобы сохранить текущий'
      : 'только в SecretStorage'
    return `<form id="server-form" class="connection-form companion-new-connection">
      <h3>${editing ? 'Изменить SSH-профиль' : 'Новый SSH-профиль'}</h3>
      <p class="companion-connection-lead">Предпочтительны ключ или ssh-agent. Пароль можно сохранить в SecretStorage, но агент его не использует.</p>
      <div class="settings-grid">
        <label>Название<input id="server-name" maxlength="120" value="${esc(editing?.displayName || '')}" placeholder="prod-api"></label>
        <label>Хост<input id="server-host" required value="${esc(editing?.host || '')}" placeholder="192.168.1.10 или box.example"></label>
        <label>Порт<input id="server-port" type="number" min="1" max="65535" value="${esc(editing?.port || 22)}"></label>
        <label>Пользователь<input id="server-user" required value="${esc(editing?.user || '')}" placeholder="deploy"></label>
        <label>Способ входа<select id="server-auth">
          <option value="agent" ${authMethod === 'agent' ? 'selected' : ''}>ssh-agent</option>
          <option value="key" ${authMethod === 'key' ? 'selected' : ''}>ключ</option>
          <option value="password" ${authMethod === 'password' ? 'selected' : ''}>пароль</option>
        </select></label>
        <label>Путь к ключу<input id="server-key" value="${esc(editing?.privateKeyPath || '')}" placeholder="C:\\Users\\…\\.ssh\\id_ed25519"></label>
        <label>Удалённый путь<input id="server-remote-path" value="${esc(editing?.defaultRemotePath || '~')}" placeholder="~"></label>
        <label>Пароль (опционально)<input id="server-password" type="password" autocomplete="off" placeholder="${passwordHint}"></label>
      </div>
      <div class="hub-card-footer">
        <button class="primary" type="submit">${editing ? 'Сохранить изменения' : 'Сохранить сервер'}</button>
        ${editing ? '<button type="button" class="secondary" data-action="cancel-edit-server">Отменить</button>' : ''}
      </div>
    </form>`
  }

  function serversView() {
    const servers = getState().boot?.serverProfiles || []
    return shell(`<main class="hub-connections hub-page point-ssh-tool">
      ${toolPageHeading('SSH', 'Серверы и файлы', 'Один профиль используется вами и агентами. Агент получает доступ только через разрешения профиля.', String(servers.length))}
      <aside class="connection-secret-note"><span>i</span><div><strong>Секреты остаются в SecretStorage</strong><small>Для агентов предпочтительны ssh-agent или ключ. Пароль никогда не попадает в prompt, журнал или настройки проекта.</small></div></aside>
      ${serverCards(servers)}
      ${serverForm(servers)}
    </main>`)
  }

  // Подключения к моделям рисует общий модуль model-picker: раньше этот экран
  // был четвёртым местом со своей версией выбора провайдера, и они расходились.
  //
  // Страница одна, а мест два. В своём окне это каталог подключений целиком —
  // столько, сколько нужно, с ключами и моделями. Во вкладке Хаба от него
  // остаётся только вход: держать список в двух местах значило бы завести две
  // правды о том, чем отвечает Помощник и Мастер.
  function connectionsView() {
    const state = getState()
    const connections = state.boot?.connections || []
    const servers = state.boot?.serverProfiles || []
    if (isConnectionsView()) {
      return shell(`<main class="hub-connections hub-page">
        <header class="hub-page-head"><div><h1>Модели и ключи</h1><p>Подключений может быть сколько угодно: рабочий ключ, личный, локальный сервер, Claude Code на этой машине. Адрес, ключ и каталог моделей живут здесь, а Помощник, Мастер и агенты ссылаются на подключение по идентификатору и берут из него модель.</p></div><em>${connections.length}</em></header>
        ${connectionManagerHtml({ editingId: getEditingState().connectionId || '' })}
      </main>`)
    }
    return shell(`<main class="hub-connections hub-page">
      <header class="hub-page-head"><div><h1>Серверы проекта</h1><p>Модели и ключи переехали в своё окно — <button type="button" class="linkish" data-action="open-connections">Подключения</button>. Здесь остаются серверы SSH. Базы данных — отдельный раздел <button type="button" class="linkish" data-action="tab" data-tab="databases">Базы</button>.</p></div><em>${servers.length}</em></header>
      <aside class="connection-secret-note"><span>i</span><div><strong>Подключений к моделям: ${connections.length}</strong><small>Завести новое, сменить ключ или посмотреть каталог моделей — в окне «Подключения». Оттуда же их выбирают в разговоре с Помощником и Мастером.</small><div class="hub-card-footer"><button type="button" class="secondary" data-action="open-connections">Открыть подключения</button></div></div></aside>
      <header class="hub-page-head is-stacked"><div><span>SSH-серверы</span><h2>Подключение к серверу</h2><p>Проверка, терминал, навигация по каталогам и безопасный предпросмотр UTF-8 файлов через OpenSSH.</p></div><em>${servers.length}</em></header>
      ${serverCards(servers)}
      ${serverForm(servers)}
    </main>`)
  }

  return { dbDriverLabel, databasesView, featuredProviderChips, connectionsView, serversView }
}
