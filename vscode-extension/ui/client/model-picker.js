// Подключение и модель выбираются в одном месте.
//
// До этого модуля выбор провайдера был собран заново в четырёх местах — экран
// связей, онбординг, настройка компаньона, конструктор агента, — и каждое
// расходилось с остальными. Контекстное окно при этом спрашивали числом, хотя
// человек его знать не может: провайдеры не отдают лимиты в списке моделей.
//
// Здесь два представления. `connectionManagerHtml` — единственное место, где
// заводят адрес, ключ и каталог моделей. `modelChoiceHtml` — компактный выбор
// «подключение → модель» для всех остальных экранов: сверху видно две вещи,
// остальное свёрнуто и подписано, откуда взялось значение.
export function createModelPicker({ getState, escapeHtml, connectionStatusLabels, connectionOrbHtml, providerCatalog }) {
  // Какой чип сейчас раскрыт: 'companion', 'master' или пусто. Состояние
  // местное и живёт до следующего выбора — сохранять его незачем.
  let openTarget = ''
  const esc = escapeHtml
  const connections = () => getState().boot?.connections || []
  const presets = () => providerCatalog() || []
  const presetOf = connection => presets().find(item => item.id === connection?.presetId)
  const connectionById = id => connections().find(item => item.id === id)

  // Семейство ищется так же, как в ядре (internal/domain/model_reference.go):
  // по самому длинному совпавшему префиксу и по части после косой черты, потому
  // что агрегаторы отдают ID вида "anthropic/claude-sonnet-4-5". Таблица одна и
  // приходит из bootstrap — дублировать её здесь значило бы дать ей разойтись.
  function familyOf(modelId) {
    const id = String(modelId || '').trim().toLowerCase()
    if (!id) return undefined
    const bare = id.includes('/') ? id.slice(id.lastIndexOf('/') + 1) : id
    let best
    for (const item of getState().boot?.modelCatalog || []) {
      const prefix = String(item.model || '').toLowerCase()
      if (!prefix || !bare.startsWith(prefix)) continue
      if (!best || prefix.length > String(best.model || '').length) best = item
    }
    return best
  }

  // Откуда взялись пределы модели. `confirmed` — прислал провайдер, `known` —
  // нашлось в справочнике семейств, `unknown` — не знаем, и тогда поле честно
  // просит ввести, а не подставляет придуманное число.
  function modelFacts(connectionId, modelId) {
    const id = String(modelId || '').trim()
    if (!id) return { source: 'empty' }
    const known = (connectionById(connectionId)?.models || []).find(item => item.id === id)
    if (known && known.contextWindow) {
      return {
        source: known.state === 'confirmed' ? 'confirmed' : 'known',
        contextWindow: known.contextWindow,
        maxOutput: known.maxOutput,
        capabilities: known.capabilities || [],
      }
    }
    const family = familyOf(id)
    if (family && family.contextWindow) {
      return {
        source: 'known',
        family: family.family,
        contextWindow: family.contextWindow,
        maxOutput: family.maxOutput,
        capabilities: family.capabilities || [],
      }
    }
    return { source: 'unknown' }
  }

  function factsLineHtml(facts) {
    if (facts.source === 'empty') return '<small class="model-facts">Укажите model ID — пределы подставятся, если модель известна.</small>'
    if (facts.source === 'unknown') {
      return '<small class="model-facts is-unknown">Модель незнакома: контекстное окно и лимит ответа задайте вручную в тонкой настройке.</small>'
    }
    const origin = facts.source === 'confirmed' ? 'прислал провайдер' : `справочник${facts.family ? ` · ${esc(facts.family)}` : ''}`
    const abilities = (facts.capabilities || []).length ? ` · ${facts.capabilities.map(item => esc(item)).join(', ')}` : ''
    return `<small class="model-facts">Контекст ${formatTokens(facts.contextWindow)}${facts.maxOutput ? ` · ответ до ${formatTokens(facts.maxOutput)}` : ''}${abilities} · ${origin}</small>`
  }

  function formatTokens(value) {
    const number = Number(value || 0)
    if (!number) return '—'
    return number.toLocaleString('ru-RU')
  }

  function connectionLabel(connection) {
    return connection.displayName || presetOf(connection)?.name || connection.presetId || connection.provider
  }

  function connectionOptionsHtml(selectedId) {
    const list = connections()
    if (!list.length) return '<option value="">Подключений ещё нет</option>'
    return list.map(item => {
      const suffix = item.isDefault ? ' · по умолчанию' : ''
      return `<option value="${esc(item.id)}" ${item.id === selectedId ? 'selected' : ''}>${esc(connectionLabel(item))}${suffix}</option>`
    }).join('')
  }

  // Источники сгруппированы: «Компания» отделяет свой шлюз от чужого облака, а
  // локальные серверы от него самого.
  function providerOptionsHtml(selectedId) {
    const blocked = new Set(['cursor-cli', 'codex-cli', 'claude-code-cli'])
    const groups = [
      ['Компания', item => item.id === 'llmux' || item.id === 'custom'],
      ['Локально', item => item.local && item.id !== 'custom'],
      ['Облако', item => !item.local && item.id !== 'llmux' && item.id !== 'custom'],
    ]
    return groups.map(([label, pick]) => {
      const items = presets().filter(item => !blocked.has(item?.kind) && pick(item))
      if (!items.length) return ''
      return `<optgroup label="${esc(label)}">${items.map(item => `<option value="${esc(item.kind)}" data-preset="${esc(item.id)}" data-base-url="${esc(item.baseUrl || '')}" ${item.id === selectedId ? 'selected' : ''}>${esc(item.name)}</option>`).join('')}</optgroup>`
    }).join('')
  }

  // ── Экран подключений ────────────────────────────────────────────────────

  function connectionCardHtml(connection) {
    const preset = presetOf(connection)
    const models = connection.models || []
    const readyCandidates = (getState().boot?.modelCandidates || []).filter(item => item.connectionId === connection.id)
    const evidence = (getState().boot?.modelEvidence || []).filter(item => item.connectionId === connection.id)
    const latestEvidence = evidence[0]
    const runtimeReady = readyCandidates.length
      ? `к назначению готово: ${readyCandidates.length}${latestEvidence?.latencyMs ? ` · ${latestEvidence.latencyMs}мс` : ''}`
      : 'нужен role capability probe'
    const catalogNote = models.length
      ? `${models.length} ${models.length === 1 ? 'модель' : 'моделей'} в каталоге`
      // Статус хранится, а каталог приходит с проверки: у подключённого
      // источника каталог бывает просто ещё не собран. Звать «проверьте
      // подключение» здесь значило поставить рядом два противоречащих
      // утверждения — «ПОДКЛЮЧЕН» и «проверьте подключение».
      : connection.status === 'connected'
        ? 'каталог моделей ещё не собран'
        : 'каталог пуст — проверьте подключение'
    return `<article class="hub-card connection-card ${connection.isDefault ? 'is-default' : ''}" data-keynav-item>
      <header>
        <strong>${connectionOrbHtml(connection.status)}${esc(connectionLabel(connection))}</strong>
        <span>${esc(connectionStatusLabels[connection.status] || connection.status || 'НЕИЗВЕСТНО')}</span>
      </header>
      <small>${esc(preset?.name || connection.provider)} · ${esc(connection.baseUrl || preset?.baseUrl || 'адрес из пресета')}${connection.secretRef ? ' · ключ в SecretStorage' : ' · без ключа'}</small>
      <small>${esc(catalogNote)}${connection.defaultModel ? ` · по умолчанию ${esc(connection.defaultModel)}` : ''}${connection.apiVersion ? ` · api-version ${esc(connection.apiVersion)}` : ''}</small>
      <small>${esc(runtimeReady)}</small>
      ${connection.lastError ? `<p class="create-step-error">${esc(connection.lastError)}</p>` : ''}
      <footer class="hub-card-footer">
        <button type="button" class="secondary" data-action="probe-connection" data-id="${esc(connection.id)}">Проверить</button>
        <button type="button" class="secondary" data-action="edit-connection" data-id="${esc(connection.id)}">Редактировать</button>
        <button type="button" class="secondary" data-action="default-connection" data-id="${esc(connection.id)}" ${connection.isDefault ? 'disabled' : ''}>По умолчанию</button>
        <button type="button" class="danger-button" data-action="delete-connection" data-id="${esc(connection.id)}">Удалить</button>
      </footer>
    </article>`
  }

  function connectionListHtml() {
    const list = connections()
    if (!list.length) {
      return '<p class="muted">Подключений пока нет. Заведите первое ниже — адрес, ключ и проверка займут минуту.</p>'
    }
    return `<section class="connection-list" data-keynav="column" aria-label="Подключения к моделям">${list.map(connectionCardHtml).join('')}</section>`
  }

  // Форма одна и на создание, и на правку: раздельные формы разошлись бы полями.
  //
  // `nested` — та же форма внутри чужой. Вложенный <form> запрещён в HTML:
  // разбор молча выбрасывает внутренний тег, поля остаются, а кнопка отправляет
  // объемлющую форму. Так и вышло в конструкторе агента, где панели живут
  // внутри #constructor-form. Поэтому во вложенном случае это <div>, а
  // сохранение идёт действием, а не отправкой.
  function connectionFormHtml(editing, { nested = false } = {}) {
    const target = editing ? connectionById(editing) : undefined
    const preset = presetOf(target)
    const azure = (target ? target.provider : '') === 'azure-openai'
    const open = nested ? '<div class="connection-form companion-new-connection">' : '<form id="connection-form" class="connection-form companion-new-connection">'
    const close = nested ? '</div>' : '</form>'
    const submit = nested
      ? `<button class="primary" type="button" data-action="save-connection">${target ? 'Сохранить' : 'Сохранить подключение'}</button>`
      : `<button class="primary" type="submit">${target ? 'Сохранить' : 'Сохранить подключение'}</button>`
    return `${open}
      <h3>${target ? `Правка: ${esc(connectionLabel(target))}` : 'Подключить модель'}</h3>
      <p class="companion-connection-lead connection-lead-remote">Ключ уходит только выбранному провайдеру и хранится в SecretStorage IDE. Пустой путь дополнится <code>/v1</code>.</p>
      <p class="companion-connection-lead connection-lead-local" hidden>Адрес и ключ здесь не нужны.</p>
      ${target ? `<input type="hidden" id="connection-id-edit" value="${esc(target.id)}">` : ''}
      <label>Источник<select id="connection-provider" required>${providerOptionsHtml(preset?.id || 'llmux')}</select></label>
      <label>Название<input id="connection-name" maxlength="120" value="${esc(target?.displayName || '')}" placeholder="Например, рабочий ключ OpenAI"></label>
      <label class="connection-base-url">Адрес сервиса<input id="connection-base-url" value="${esc(target?.baseUrl || '')}" placeholder="https://llmux.company.internal/v1"></label>
      <label class="connection-api-version" ${azure ? '' : 'hidden'}>Версия API<input id="connection-api-version" value="${esc(target?.apiVersion || '')}" placeholder="2024-10-21"><small>Нужна только ресурсу Azure OpenAI. Версия хранится отдельно от адреса: в самом адресе она бы потерялась при нормализации.</small></label>
      <label class="connection-api-key">Токен / API-ключ<input id="connection-api-key" type="password" autocomplete="off" placeholder="${target?.secretRef ? 'Сохранён — введите новый, чтобы заменить' : 'Сохранится в SecretStorage IDE'}"></label>
      <label>Модель по умолчанию<input id="connection-default-model" value="${esc(target?.defaultModel || '')}" list="connection-default-model-list" placeholder="Подставится новым агентам"><datalist id="connection-default-model-list">${(target?.models || []).slice(0, 200).map(item => `<option value="${esc(item.id)}"></option>`).join('')}</datalist></label>
      <div class="connection-form-actions">
        ${submit}
        ${target ? '<button type="button" class="secondary" data-action="cancel-edit-connection">Отмена</button>' : ''}
      </div>
    ${close}`
  }

  function modelRoutingHtml() {
    const routing = getState().boot?.modelRouting || {}
    const list = connections()
    if (!list.length) return ''
    const codingStatus = routing.codingRequired
      ? (routing.codingReady ? 'сильная · готова' : `сильная · блок: ${routing.codingBlockReason || 'проверьте подключение'}`)
      : 'сильная · не задана (агент использует свой профиль)'
    const option = (selected) => list.map(item => `<option value="${esc(item.id)}" ${item.id === selected ? 'selected' : ''}>${esc(connectionLabel(item))}</option>`).join('')
    return `<section class="model-routing-card">
      <header><strong>Маршруты моделей (2C)</strong><small>${esc(codingStatus)}</small></header>
      <p class="muted">Coding — Fast Agent и исполнители. Cheap — Мастер, intake, компаньон, обучение.</p>
      <div class="settings-grid">
        <label>Coding · подключение<select data-routing-field="codingConnectionId"><option value="">— как у агента —</option>${option(routing.codingConnectionId || '')}</select></label>
        <label>Coding · модель<input data-routing-field="codingModel" value="${esc(routing.codingModel || '')}" placeholder="model id" list="routing-coding-models"></label>
        <label>Cheap · подключение<select data-routing-field="cheapConnectionId"><option value="">— как у Мастера —</option>${option(routing.cheapConnectionId || '')}</select></label>
        <label>Cheap · модель<input data-routing-field="cheapModel" value="${esc(routing.cheapModel || '')}" placeholder="model id"></label>
      </div>
      <datalist id="routing-coding-models">${(connectionById(routing.codingConnectionId)?.models || []).slice(0, 100).map(item => `<option value="${esc(item.id)}"></option>`).join('')}</datalist>
      <button type="button" class="primary" data-action="save-model-routing">Сохранить маршруты</button>
    </section>`
  }

  function connectionManagerHtml({ editingId } = {}) {
    return `<section class="connection-manager">
      <aside class="connection-secret-note"><span>i</span><div><strong>Ключи остаются в SecretStorage</strong><small>В файлах проекта, хронике и ответах ядра остаётся только ссылка (<code>secretRef</code>). Сам ключ уходит выбранному провайдеру — иначе он не авторизует запрос. Агент, компаньон и Мастер ссылаются на подключение по идентификатору, а не подбирают ключ по совпадению пресета.</small></div></aside>
      ${modelRoutingHtml()}
      ${connectionListHtml()}
      ${connectionFormHtml(editingId)}
    </section>`
  }

  // ── Компактный выбор для остальных экранов ───────────────────────────────

  function modelChoiceHtml({
    connectionId = '',
    model = '',
    contextWindowTokens = 0,
    temperature = 0.2,
    maxOutputTokens = 8192,
    reasoningEffort = 'none',
    showTuning = true,
  } = {}) {
    const list = connections()
    const connection = connectionById(connectionId) || list.find(item => item.isDefault) || list[0]
    const facts = modelFacts(connection?.id, model)
    const catalog = connection?.models || []
    const inheritedWindow = facts.contextWindow || 0
    const windowValue = Number(contextWindowTokens || 0) || inheritedWindow
    const windowHint = inheritedWindow
      ? `Как у модели — ${formatTokens(inheritedWindow)}. Своё значение переопределит.`
      : 'Модель незнакома: значение нужно указать самому.'
    const empty = !list.length
      ? '<p class="create-step-error">Сначала заведите подключение в разделе «Связи» — без него агенту некуда идти.</p>'
      : ''
    return `<div class="model-choice">
      ${empty}
      <div class="settings-grid">
        <label>Подключение<select id="connection-id" ${empty ? 'disabled' : ''}>${connectionOptionsHtml(connection?.id)}</select><small>Адрес и ключ берутся отсюда.</small></label>
        <label>Модель<input id="model" list="model-choice-models" value="${esc(model)}" placeholder="${esc(connection?.defaultModel || 'Точный model ID')}" required><datalist id="model-choice-models">${catalog.slice(0, 200).map(item => `<option value="${esc(item.id)}">${esc(item.displayName || item.ownedBy || '')}</option>`).join('')}</datalist></label>
      </div>
      ${factsLineHtml(facts)}
      ${showTuning ? `<details class="model-tuning-advanced">
        <summary>Тонкая настройка</summary>
        <div class="settings-grid model-tuning">
          <label>Температура<input id="temperature" type="number" min="0" max="2" step="0.05" value="${Number(temperature ?? 0.2)}"><small>0 — строго, 2 — вариативно</small></label>
          <label>Макс. токенов ответа<input id="max-output-tokens" type="number" min="128" max="131072" value="${Number(maxOutputTokens || 8192)}"><small>${facts.maxOutput ? `Как у модели — ${formatTokens(facts.maxOutput)}` : 'Предел модели неизвестен'}</small></label>
          <label>Усилие рассуждения<select id="reasoning-effort">${[['none', 'Обычное'], ['minimal', 'Минимальное'], ['low', 'Низкое'], ['medium', 'Среднее'], ['high', 'Высокое']].map(([value, label]) => `<option value="${value}" ${(reasoningEffort || 'none') === value ? 'selected' : ''}>${label}</option>`).join('')}</select><small>Поддержка зависит от модели</small></label>
          <label>Контекстное окно<input id="context-window-tokens" type="number" min="4096" max="1048576" step="1024" value="${Number(windowValue || 32768)}"><small>${esc(windowHint)}</small></label>
        </div>
      </details>` : ''}
    </div>`
  }

  // ── Выбор модели прямо в разговоре ───────────────────────────────────────
  //
  // Подключения заводят один раз и надолго; выбирать из них модель приходится
  // часто, и до сих пор за этим шли в мастер настройки. Чип ставится ТУДА, ГДЕ
  // ИМЯ МОДЕЛИ УЖЕ СТОЯЛО, а не рядом: то же имя в четвёртом месте ничего не
  // сообщает, но занимает строку.
  //
  // Ядро читает сохранённую настройку и модель в запросе не принимает, поэтому
  // выбор её и меняет — насовсем, а не «на этот разговор». Об этом сказано в
  // самом списке, чтобы смена платного ключа не оказалась неожиданностью.
  function modelChipHtml({ target, connectionId = '', model = '' }) {
    const open = openTarget === target
    const list = connections()
    const connection = connectionById(connectionId)
    // Имя подключения — уточнение к модели, а не замена ей. Настройка,
    // сохранённая до появления connectionId, знает только модель, и надпись
    // «подключение не выбрано · модель» врала бы: модель как раз выбрана.
    const label = model
      ? (connection ? `${connectionLabel(connection)} · ${model}` : model)
      : list.length ? 'модель не выбрана' : 'подключений нет'
    const title = model
      ? `Отвечает ${label}. Нажмите, чтобы выбрать другую модель`
      : 'Выбрать подключение и модель'
    return `<div class="model-chip${open ? ' is-open' : ''}">
      <button type="button" class="model-chip-button${model ? ' is-live' : ''}" data-action="toggle-model-picker" data-target="${esc(target)}" title="${esc(title)}" aria-expanded="${open ? 'true' : 'false'}"><i aria-hidden="true"></i><span>${esc(label)}</span><b aria-hidden="true">▾</b></button>
      ${open ? modelPickerListHtml({ target, connectionId: connection?.id || '', model }) : ''}
    </div>`
  }

  function modelPickerListHtml({ target, connectionId, model }) {
    const list = connections()
    if (!list.length) {
      return `<div class="model-chip-list"><p class="model-chip-empty">Подключений пока нет. Заведите первое — адрес, ключ и проверка занимают минуту.</p><button type="button" class="primary" data-action="open-connections">Открыть подключения</button></div>`
    }
    const groups = list.map(connection => {
      const catalog = connection.models || []
      // Каталог пуст, пока подключение не проверяли. Скрывать его нельзя:
      // модель по умолчанию у него всё равно есть, и выбрать её надо.
      const models = catalog.length
        ? catalog.slice(0, 40).map(item => String(item.id))
        : [connection.defaultModel].filter(Boolean)
      const rows = models.length
        ? models.map(item => `<button type="button" class="model-chip-row${connection.id === connectionId && item === model ? ' selected' : ''}" data-action="pick-model" data-target="${esc(target)}" data-connection="${esc(connection.id)}" data-model="${esc(item)}"><span>${esc(item)}</span></button>`).join('')
        : '<p class="model-chip-empty">Каталог пуст — проверьте подключение в его окне.</p>'
      return `<section><header><strong>${esc(connectionLabel(connection))}</strong>${catalog.length ? `<em>${catalog.length}</em>` : ''}</header>${rows}</section>`
    }).join('')
    return `<div class="model-chip-list" role="menu">
      ${groups}
      <footer><small>Выбор сохранится: ядро читает настройку, а не запрос.</small><button type="button" class="secondary" data-action="open-connections">Настроить подключения…</button></footer>
    </div>`
  }

  // Действия чипа разбираются здесь же. Общий обработчик кликов принимает их
  // одной строкой: ему незачем знать ни про раскрытый список, ни про то, из
  // чего собирается настройка.
  function handleModelChipAction({ action, target, vscode }) {
    if (action === 'open-connections') {
      openTarget = ''
      vscode.postMessage({ type: 'openConnections' })
      return true
    }
    if (action === 'toggle-model-picker') {
      const which = String(target.dataset.target || '')
      openTarget = openTarget === which ? '' : which
      return true
    }
    if (action !== 'pick-model') return false
    // Настройка уходит целиком: ядро принимает конфигурацию, а не поле.
    // Отправить одно поле значило бы обнулить остальные — характер, границы и
    // пределы ответа.
    //
    // Адрес, пресет и вид провайдера принадлежат подключению и уходят вместе с
    // ним: оставь их прежними — и ядро пошло бы к старому серверу под новым
    // ключом. Ровно это делает мастер настройки (companionDraftWithConnection).
    const which = String(target.dataset.target || '')
    const connectionId = String(target.dataset.connection || '')
    const model = String(target.dataset.model || '')
    const connection = connectionById(connectionId)
    const state = getState()
    const bound = base => ({
      ...base,
      connectionId,
      model,
      providerPreset: connection?.presetId || base.providerPreset || '',
      provider: connection?.provider || base.provider || '',
      baseUrl: connection?.baseUrl || '',
    })
    openTarget = ''
    if (which === 'companion') {
      vscode.postMessage({ type: 'saveCompanionConfig', config: bound(state.boot?.companion || {}) })
    } else if (which === 'master') {
      vscode.postMessage({ type: 'saveOrchestratorConfig', config: bound(state.boot?.orchestrator || {}) })
    }
    return true
  }

  // Клик мимо закрывает список — так ведут себя все меню. Возвращает true,
  // если что-то закрылось: перерисовку зовёт тот, кто поймал клик.
  function closeModelPickerOutside(event) {
    if (!openTarget || event.target.closest?.('.model-chip')) return false
    openTarget = ''
    return true
  }

  return { connectionManagerHtml, connectionFormHtml, modelChoiceHtml, modelFacts, connectionLabel, modelChipHtml, handleModelChipAction, closeModelPickerOutside }
}
