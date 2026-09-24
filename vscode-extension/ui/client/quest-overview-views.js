import { intakePanelHtml } from './intake-views.js'

export function createQuestOverviewViews(dependencies) {
  const {
    activeExecutions,
    activeQuestCard,
    agentById,
    agentClass,
    agentDisplayName,
    agentRuntimeLabel,
    changeSetCardHtml,
    compactExecutionTask,
    compactQuestDescription,
    compactQuestTitle,
    compactTeamName,
    companionChatHtml,
    connectionManagerHtml,
    connectionStatusLabels,
    contextInspectorPanelHtml,
    countOf,
    createInfrastructureViews,
    createStatisticsViews,
    toolWindowFrame,
    currentHubQuest,
    currentHubTeam,
    decisionsWaitingCount,
    decisionsWaitingBreakdown,
    esc,
    toolName,
    execControlsHtml,
    flowApprovalStripHtml,
    formatCents,
    formatDuration,
    hallSpendLabel,
    hallUnpricedRuns,
    handoffsHtml,
    hubAgents,
    hubSituation,
    isConnectionsView,
    isStatisticsView,
    memoryKindLabels,
    partyStatusStripHtml,
    pendingChangeSets,
    plannerFallbackBannerHtml,
    questOutcomeHtml,
    questMidFlightHtml,
    questProgressHtml,
    questStatusStripHtml,
    questStatusLabels,
    runIsActiveNow,
    shell,
    skillEquipConfirmHtml,
    statisticsPanelHtml,
    status,
    systemAgentsStripHtml,
    ui,
    vscode,
  } = dependencies

  function sandboxBoundaryHtml() {
    const capabilities = ui.state.boot?.sandbox
    if (!capabilities) return ''
    const strong = Boolean(capabilities.strongOsBoundary)
  	const digest = String(capabilities.imageDigest || '').replace(/^sha256:/, '')
	  const identity = [capabilities.backend || 'среда не определена', capabilities.version, capabilities.image, digest ? `sha256:${digest.slice(0, 12)}` : ''].filter(Boolean).join(' · ')
    const guarantees = [
      ['Рабочая копия', capabilities.liveWorkspaceIsolation, 'изменяется только после Apply'],
      ['Процессы', capabilities.processIsolation, capabilities.processIsolation ? 'изолированы' : 'пользователь Point'],
      ['Сеть', capabilities.networkIsolation, capabilities.networkIsolation ? 'изолирована' : 'без egress-границы'],
      ['Секреты', capabilities.secretEnvironmentSanitization, 'очищаются из env'],
    ]
    return `<section class="hub-sandbox-boundary ${strong ? 'strong' : 'limited'}"><header class="section-title"><span>Граница sandbox</span><em title="${esc(capabilities.imageDigest || '')}">${esc(identity)}</em></header><div>${guarantees.map(([label, enabled, detail]) => `<span class="${enabled ? 'available' : 'unavailable'}"><b>${enabled ? '✓' : '!'}</b><small>${esc(label)}</small><em>${esc(detail)}</em></span>`).join('')}</div>${strong ? '<p>Команды исполняются внутри атрибутированного контейнера. Egress по умолчанию DENY; точный TLS FQDN:port проходит через изолированный gateway.</p>' : '<p>Filtered copy защищает live workspace, но не является OS-контейнером. Подтверждённые команды выполняются с правами процесса Point.</p>'}</section>`
  }
  function overview() {
    if (ui.companionSetupOpen) {
      return shell(`<main class="command-center companion-setup-only">${companionChatHtml()}</main>`)
    }
    const quest = currentHubQuest()
    const team = currentHubTeam(quest)
    const pendingSets = pendingChangeSets()
    const teamAgents = (team?.agentIds || []).map(id => agentById(id)).filter(Boolean)
    const situation = hubSituation()
    const questDescription = compactQuestDescription(quest?.description, quest?.title)
    const questHtml = quest
      ? `<section class="hub-current-quest accent-ember featured"><header><span>Текущий квест</span><em>${esc(questStatusLabels[quest.status] || quest.status || 'Активен')}</em></header><h2 title="${esc(quest.title || '')}">${esc(compactQuestTitle(quest.title))}</h2>${questProgressHtml(quest)}${questDescription ? `<p>${esc(questDescription)}</p>` : ''}${quest.objectives?.length ? `<ul>${quest.objectives.slice(0, 4).map(item => `<li>${esc(item)}</li>`).join('')}</ul>` : ''}<footer><button type="button" class="primary" data-action="tab" data-tab="quests">${quest.legacyRun ? 'Открыть запуск →' : 'К брифингу →'}</button>${quest.legacyRun ? '' : `<button type="button" class="secondary" data-action="revert-quest" data-id="${esc(quest.id)}">Откатить квест</button>`}</footer></section>`
      : `<section class="hub-current-quest accent-ember featured empty"><header><span>Текущий квест</span></header><h2>Нет активного квеста</h2><p>Примите новый квест или попросите компаньона предложить задачу.</p><button type="button" class="primary" data-action="tab" data-tab="quests">Новый квест →</button></section>`
    const teamHtml = team
      ? `<section class="hub-party accent-mana"><header class="section-title"><span>Отряд</span><em title="${esc(team.name || '')}">${esc(compactTeamName(team, quest))}</em></header>${partyStatusStripHtml(teamAgents, quest)}<div class="hub-party-grid">${teamAgents.length ? teamAgents.map(agent => `<button type="button" data-action="tab" data-tab="agents"><strong>${esc(agentDisplayName(agent))}</strong><small>${esc(agent.roleDescription || agentClass(agent))}${agentRuntimeLabel(agent) ? ` · ${esc(agentRuntimeLabel(agent))}` : ''}</small></button>`).join('') : '<p class="muted">Агенты не назначены</p>'}</div><button type="button" class="secondary" data-action="tab" data-tab="teams">Изменить состав</button></section>`
      : `<section class="hub-party accent-mana empty"><header class="section-title"><span>Отряд</span></header><p>Отряды не собраны — агенты работают по одиночке.</p><button type="button" class="secondary" data-action="tab" data-tab="teams">Создать отряд</button></section>`
    // Исполнения показывает ситуационная комната выше на этом же экране — вместе
    // с управлением прогоном. Здесь остался только разбор контекста: он к списку
    // исполнений не привязан и своего места больше нигде не имеет.
    const inspector = contextInspectorPanelHtml()
    const changeSetsHtml = pendingSets.length
      ? `<section class="hub-changesets accent-ember"><header class="section-title"><span>Наборы изменений</span><em>${pendingSets.length} на проверке</em></header>${pendingSets.slice(0, 3).map(set => changeSetCardHtml(set, true)).join('')}<button type="button" class="secondary" data-action="tab" data-tab="changesets">Все наборы →</button></section>`
      : ''
    const activeDetails = ui.state.details?.run && runIsActiveNow(ui.state.details.run) ? activeQuestCard(ui.state.details) : ''
    // Чат компаньона в Хаб не встраивается: он живёт в IDE — боковая панель,
    // док и peek. Гильдия принадлежит Мастеру.
    const activeFlowRun = (ui.state.boot?.flowRuns || []).find(run => run.status === 'running' || run.status === 'paused')
    const secondary = `<div class="hub-overview-secondary">${teamHtml}${changeSetsHtml}</div>`
    const intake = intakePanelHtml({
      boot: ui.state.boot,
      esc,
      selectedIntakeId: ui.selectedIntakeId || '',
      intakeBusy: Boolean(ui.intakeBusy),
      intakeError: ui.intakeError || '',
      intakeURL: ui.intakeURL || '',
    })
    const guild = `<div class="guild-ops">${questStatusStripHtml(quest)}${plannerFallbackBannerHtml(quest?.id)}${flowApprovalStripHtml()}${questOutcomeHtml(quest)}${questMidFlightHtml(quest)}${handoffsHtml(activeFlowRun?.id)}${intake}${questHtml}${secondary}${activeDetails}${inspector}${systemAgentsStripHtml()}${statisticsPanelHtml()}${sandboxBoundaryHtml()}</div>`
    return shell(`<main class="command-center">${situationRoomHtml(situation)}<div class="command-center-grid guild-priority">${guild}</div></main>`)
  }
  
  // Ситуационная комната: показывает только отклонения и пуста, когда всё спокойно.
  // Порядок блоков — по цене простоя: заблокированный агент дороже всего, поэтому
  // очередь решений стоит первой и единственная имеет право на акцент.
  function situationRoomHtml(situation) {
    const waiting = decisionsWaitingCount()
    const executions = activeExecutions()
    const unverified = unverifiedChangesHtml()
    const spend = hallSpendLabel()
    const health = hubHealthRowsHtml()
  
    const waitingPanel = waiting
      ? `<section class="hall-panel is-alarm">
          <header>
            <span class="hall-count">${waiting}</span>
            <div class="hall-alarm-text">
              <b>Ждёт вас</b>
              <small>${esc(decisionsWaitingBreakdown())}</small>
            </div>
            <button class="hall-btn is-primary" data-action="tab" data-tab="decisions">Разобрать очередь</button>
          </header>
        </section>`
      : `<section class="hall-panel">
          <header>
            <b>${esc(situation.label)}</b>
            <small>${esc(situation.detail)}</small>
            ${situation.action && situation.tab
              ? `<button class="hall-btn is-sm" data-action="tab" data-tab="${esc(situation.tab)}">${esc(situation.action)}</button>`
              : ''}
          </header>
        </section>`
  
    // Список исполнений на Обзоре ровно один. До цикла 30 их было два: этот и
    // «АКТИВНЫЕ ЗАПУСКИ» экраном ниже — оба из `activeExecutions()`, оба по
    // четыре строки, с одинаковой развилкой заголовка. Два имени у одного списка
    // читались как две разные вещи. Осталась верхняя панель: активная работа
    // должна быть над сгибом. Управление прогоном переехало сюда же второй
    // строкой — в узкой колонке Обзора кнопки и поля рядом с задачей не
    // помещаются, а терять их ради краткости нельзя.
    const runningPanel = executions.length
      ? `<section class="hall-panel">
          <header><b>${executions.some(item => item.status === 'pending') ? 'Готово к запуску' : 'Идёт сейчас'}</b><small>${executions.length}</small></header>
          ${executions.slice(0, 4).map(item => {
            const agent = agentById(item.projectAgentId)
            const controls = execControlsHtml(item)
            return `<div class="hall-panel-row${controls ? ' is-stack is-tight' : ''}">
            <div class="hall-item">
              <div class="hall-item-text">
                <b title="${esc(item.task || '')}">${esc(compactExecutionTask(item, currentHubQuest()))}</b>
                <small>${esc(agent ? agentDisplayName(agent) : 'Агент')}</small>
              </div>
              ${status(item.status, item.status === 'pending' ? 'Готов к запуску' : '')}
              <button class="hall-btn is-sm" data-action="tab" data-tab="quests">Открыть</button>
            </div>
            ${controls}
          </div>` }).join('')}
        </section>`
      : ''
  
    // Расход — карточка постоянной ширины рядом со здоровьем, а не полоса во всю
    // страницу: цифра одна, растягивать её не на что.
    const spendPanel = spend
      ? `<section class="hall-panel hall-spend">
          <header><b>Расход</b></header>
          <div class="hall-panel-row hall-item">
            <div class="hall-item-text">
              <strong class="hall-figure">${esc(spend)}</strong>
            </div>
            <button class="hall-btn is-sm" data-action="tab" data-tab="statistics">Подробно</button>
          </div>
          ${hallUnpricedRuns()
            ? `<div class="hall-panel-row"><small class="hall-meta">известная стоимость · ${esc(countOf(hallUnpricedRuns(), 'запуск', 'запуска', 'запусков'))} без цены (например, локальные модели)</small></div>`
            : ''}
        </section>`
      : ''
  
    return `<div class="hall-page is-stacked">
      ${waitingPanel}
      ${runningPanel || unverified ? `<div class="hall-grid is-two">${runningPanel}${unverified}</div>` : ''}
      ${health || spendPanel ? `<div class="hall-summary">${health}${spendPanel}</div>` : ''}
    </div>`
  }
  
  // Файлы, тронутые агентом и не подтверждённые успешной проверкой. Это не общий
  // список изменений: сюда попадает только то, за что ещё никто не поручился.
  function unverifiedChangesHtml() {
    const sets = pendingChangeSets()
    if (sets.length === 0) return ''
    // Название набора стояло под КАЖДЫМ его файлом: у набора из двух файлов оно
    // печаталось дважды, из четырёх — четырежды, и ещё раз ниже, заголовком
    // карточки в разделе «Наборы изменений». Проверка повторов насчитала три
    // копии одной строки на первом экране. Название принадлежит набору, а не
    // файлу, и стоит один раз — на первой его строке.
    const rows = []
    for (const set of sets.slice(0, 4)) {
      const items = (set.items || []).slice(0, 2)
      items.forEach((item, index) => {
        rows.push(`<button type="button" class="hall-panel-row hall-item is-path" data-action="tab" data-tab="changesets" title="${esc(item.path || 'файл')}">
          <div class="hall-item-text">
            <b>${esc(item.path || 'файл')}</b>
            ${index === 0 ? `<small>${esc(set.title || set.id)}</small>` : ''}
          </div>
          <span class="hall-chip is-hot">Не проверен</span>
        </button>`)
      })
    }
    if (rows.length === 0) return ''
    return `<section class="hall-panel">
      <header><b>Изменено, не проверено</b><button type="button" class="hall-more" data-action="tab" data-tab="changesets">Все →</button></header>
      ${rows.join('')}
    </section>`
  }
  
  // Прогоны, оборвавшиеся за последние сутки. Отмена — не поломка: её сделал сам
  // человек, и напоминать ему о собственном решении незачем.
  //
  // Прогон без обеих отметок времени считаем недавним намеренно: датировать его
  // нечем, а промолчать о настоящей неудаче хуже, чем показать лишнюю строку.
  function recentlyBrokenRuns() {
    const DAY = 24 * 60 * 60 * 1000
    const now = Date.now()
    return (ui.state.boot?.runs || []).filter(run => {
      if (run?.status !== 'failed' && run?.status !== 'interrupted') return false
      const stamp = Date.parse(run.finishedAt || run.startedAt || '')
      return Number.isNaN(stamp) ? true : now - stamp <= DAY
    })
  }
  
  // Здоровье показывается только когда сломано: исправное состояние не новость.
  function hubHealthRowsHtml() {
    const rows = []
    const index = ui.state.boot?.indexStatus
    if (index && index.state !== 'ready') {
      // Набор состояний закрыт ядром (internal/workspace/index.go): ready, stale,
      // indexing, pending, error, not_built. Пропущенное состояние печаталось
      // сырым — на первом же экране нового проекта висело «ИНДЕКС not_built».
      const labels = {
        stale: 'индекс устарел', indexing: 'идёт индексация', pending: 'ожидает обновления',
        error: 'ошибка индекса', not_built: 'ещё не построен',
      }
      rows.push(['Индекс', labels[index.state] || index.state, 'поиск по коду может промахиваться', 'onboarding'])
    }
    const connections = ui.state.boot?.connections || []
    if (connections.length === 0) rows.push(['Связь', 'модель не подключена', 'без неё агент не стартует', 'connections'])
    // Обзор подписан «что требует внимания прямо сейчас», а упавший прогон на нём
    // не упоминался вовсе: активные исполнения показываются, ожидающие решения
    // показываются, а неудача — нет. Человек узнавал о ней, только если сам шёл в
    // хронику. Сутки — чтобы вчерашняя неудача не висела здесь вечно.
    const broken = recentlyBrokenRuns()
    if (broken.length) {
      rows.push(['Прогон', countOf(broken.length, 'прогон не дошёл до конца', 'прогона не дошли до конца', 'прогонов не дошли до конца'),
        'причина видна в журнале', 'history'])
    }
    if (rows.length === 0) return ''
    return `<section class="hall-panel hall-health">
      <header><b>Здоровье</b><small>исправное скрыто</small></header>
      ${rows.map(([tag, name, detail, tab]) => `<div class="hall-panel-row hall-item">
        <span class="hall-chip is-hot">${tag}</span>
        <div class="hall-item-text">
          <b>${esc(name)}</b>
          <small>${esc(detail)}</small>
        </div>
        <button class="hall-btn is-sm" data-action="tab" data-tab="${tab}">Исправить</button>
      </div>`).join('')}
    </section>`
  }
  // Экран управления открывается тем, что уже есть, а не тем, что можно завести.
  // Измерено: на Навыках форма занимала 607px и начиналась на y=220, а список
  // существующих уходил на y=851 — за сгиб при высоте окна 860. Раскрывашка
  // здесь не новый приём: `hire-drawer` уже носит «Чертежи агентов» и
  // «Основные профили». Раскрыта, когда заводить нечего или когда идёт правка
  // существующей записи — иначе кнопка «Изменить» ничего видимого не даст.
  function creationDrawer({ kicker, title, body, open, badge }) {
    return `<details class="hire-drawer creation-drawer"${open ? ' open' : ''}><summary><span class="hire-kicker">${esc(kicker)}</span> ${esc(title)}${badge ? `<span>${esc(badge)}</span>` : ''}</summary>${body}</details>`
  }
  // Отряд без квестов и отряд, который ведёт работу прямо сейчас, выглядели
  // одинаково: имя, описание, фишки участников. На вопрос «какая из моих партий
  // вообще работает» экран ответить не мог. Считаем по тем же квестам, что
  // показывает Чертог, — своя арифметика поссорила бы экраны.
  function teamQuests(team) {
    return (ui.state.boot?.quests || []).filter(quest => quest.teamId === team.id)
  }
  function teamWorkBadge(team) {
    const active = teamQuests(team).filter(quest => quest.status === 'active' || quest.status === 'running').length
    return active ? `<em class="hub-team-active">в работе · ${active}</em>` : ''
  }
  function teamFactsLine(team) {
    const members = (team.agentIds || []).filter(id => agentById(id)).length
    const quests = teamQuests(team)
    const done = quests.filter(quest => quest.status === 'completed' || quest.status === 'done').length
    const parts = [countOf(members, 'агент', 'агента', 'агентов')]
    parts.push(quests.length ? countOf(quests.length, 'квест', 'квеста', 'квестов') : 'квестов пока не было')
    if (quests.length) parts.push(`завершено ${done}`)
    return esc(parts.join(' · '))
  }
  function teamsView() {
    const teams = ui.state.boot?.teams || []
    const agents = hubAgents()
    const form = `<form id="team-form" class="team-form"><label>Название<input id="team-name" required maxlength="120" placeholder="Backend Team"></label><label>Описание<textarea id="team-description" rows="2" placeholder="Для feature quests"></textarea></label><fieldset><legend>Агенты</legend>${agents.map(agent => `<label class="team-agent-pick"><input type="checkbox" name="team-agent" value="${esc(agent.id)}"> ${esc(agent.name)}</label>`).join('') || '<p class="muted">Сначала создайте агентов</p>'}</fieldset><button class="primary" type="submit">Создать отряд</button></form>`
    return shell(`<main class="hub-teams hub-page"><header class="hub-page-head"><div><h1>Партии проекта</h1><p>Группы агентов для квестов и схем. Мастер назначает состав при запуске квеста.</p></div><em>${teams.length}</em></header>${teams.length ? `<section class="hub-card-grid">${teams.map(team => `<article class="hub-team-card"><header><strong>${esc(team.name)}</strong>${teamWorkBadge(team)}</header><small>${esc(team.description || 'Без описания')}</small><div class="hub-member-chips">${(team.agentIds || []).map(id => agentById(id)).filter(Boolean).map(agent => `<span>${esc(agent.name)}</span>`).join('') || '<em>Агенты не назначены</em>'}</div><p class="hub-team-facts">${teamFactsLine(team)}</p><footer class="hub-card-footer"><button type="button" class="danger-button" data-action="delete-team" data-id="${esc(team.id)}">Распустить отряд</button></footer></article>`).join('')}</section>` : `<div class="empty compact point-frame"><span class="empty-glyph">⬡</span><h3>Отрядов пока нет</h3><p>Соберите первую команду для совместных квестов.</p><div class="empty-next"><button type="button" class="primary" data-action="tab" data-tab="agents">К агентам →</button><button type="button" class="secondary" data-action="tab" data-tab="overview">Спросить компаньона</button></div></div>`}${creationDrawer({ kicker: 'Создать', title: 'Новый отряд', body: form, open: !teams.length })}</main>`)
  }
  // Схемы проекта: перенаправление плюс уборка.
  //
  // Редактор флоу скрыт намеренно — план и ростер задаёт Мастер в карточке
  // наряда. Но схемы он создаёт сам, под каждый квест, и они переживают квест:
  // узел схемы держит исполнителя по идентификатору, поэтому роспуск персонажа
  // отказывал «замените его в схеме», а заменить было негде — ни редактора, ни
  // удаления. Персонаж оставался в ростере навсегда. Здесь видно, какие схемы
  // есть, кого они держат, и их можно удалить.
  function projectFlowsView() {
    const flows = ui.state.boot?.flows || []
    const cards = flows.map(flow => {
      const nodes = Array.isArray(flow.nodes) ? flow.nodes : []
      const held = [...new Set(nodes.map(node => node.agentId).filter(Boolean))]
        .map(id => agentById(id)).filter(Boolean)
      return `<article class="hub-team-card"><header><strong>${esc(flow.name || flow.id)}</strong></header>
        <small>${esc(flow.description || 'Схема собрана Мастером под квест')}</small>
        <div class="hub-member-chips">${held.length ? held.map(agent => `<span>${esc(agent.name)}</span>`).join('') : '<em>Агенты не заняты</em>'}</div>
        <p class="hub-team-facts">${esc(countOf(nodes.length, 'узел', 'узла', 'узлов'))}</p>
        <footer class="hub-card-footer"><button type="button" class="danger-button" data-action="delete-flow" data-id="${esc(flow.id)}">Удалить схему</button></footer>
      </article>`
    }).join('')
    return shell(`<main class="hub-legacy-redirect hub-page"><header class="section-title"><span>Схемы</span><em>через Мастера</em></header>
      <h2>Запуск идёт через карточку наряда</h2>
      <p>Отдельный редактор схем скрыт: план, состав и проверки задаёт Мастер в карточке наряда v2. Откройте чат Мастера и подтвердите карточку запуска.</p>
      ${flows.length ? `<section class="hub-card-grid">${cards}</section>` : '<p class="muted">Схем в проекте нет.</p>'}
      <footer><button type="button" class="primary" data-action="tab" data-tab="master">Открыть Мастера →</button><button type="button" class="secondary" data-action="tab" data-tab="quests">К квестам</button></footer>
    </main>`)
  }
  function skillAttachedAgents(skillId) {
    return hubAgents().filter(agent => Array.isArray(agent.skillIds) && agent.skillIds.includes(skillId))
  }
  function skillCurationHtml() {
    const suggestions = Array.isArray(ui.state.boot?.skillCuration) ? ui.state.boot.skillCuration : []
    if (!suggestions.length) return ''
    const skills = new Map((ui.state.boot?.skills || []).map(item => [item.id, item]))
    const labels = { duplicate: 'Дубликат', conflict: 'Конфликт', regression: 'Регрессия', stale: 'Не используется' }
    const actions = { merge: 'сравнить и объединить', review: 'разрешить вручную', deprecate: 'проверить и устарить' }
    return `<section class="skill-curation"><header><div><span>CURATOR</span><h2>Гигиена каталога</h2><p>Это только предложения по наблюдаемым определениям и использованию. Curator ничего не объединяет, не отключает и не удаляет автоматически.</p></div><em>${suggestions.length}</em></header><div>${suggestions.slice(0, 16).map(item => {
      const primary = skills.get(item.primarySkillId)
      const related = (item.relatedSkillIds || []).map(id => skills.get(id)).filter(Boolean)
      return `<article class="skill-curation-card is-${esc(item.kind)}"><header><span>${esc(labels[item.kind] || item.kind)}</span><em title="Насколько ядро уверено в этом выводе">уверенность ${Math.round(Number(item.confidence || 0) * 100)}%</em></header><strong>${esc(item.title)}</strong><p>${esc(item.summary)}</p><ul>${(item.evidence || []).map(line => `<li>${esc(line)}</li>`).join('')}</ul><footer><small>Предложение: ${esc(actions[item.action] || item.action)}</small>${primary ? `<button type="button" class="primary" data-action="skill-edit" data-id="${esc(primary.id)}">Открыть ${esc(primary.name)}</button>` : ''}${related.map(skill => `<button type="button" class="secondary" data-action="skill-edit" data-id="${esc(skill.id)}">Сравнить ${esc(skill.name)}</button>`).join('')}</footer></article>`
    }).join('')}</div></section>`
  }
  function skillFormHtml() {
    const editing = ui.skillEditId ? (ui.skillDraft || (ui.state.boot?.skills || []).find(item => item.id === ui.skillEditId)) : (ui.skillDraft || null)
    const selected = new Set(Array.isArray(editing?.requiredTools) ? editing.requiredTools : ['read_file', 'search_text'])
    const catalog = ui.state.boot?.toolCatalog || []
    const toolGrid = catalog.length
      ? `<fieldset><legend>Требуемые инструменты</legend><div class="check-grid skill-tool-grid">${catalog.map(tool => `<label class="tool-toggle ${selected.has(tool.name) ? 'is-on' : ''}"><input type="checkbox" name="skill-tool" value="${esc(tool.name)}" ${selected.has(tool.name) ? 'checked' : ''}><span><strong>${esc(tool.displayName || tool.name)}</strong><small>${esc(tool.name)}</small></span></label>`).join('')}</div><small>Навык требует у агента определённых инструментов, но сам их не выдаёт — иначе проверка перед стартом остановит запуск.</small></fieldset>`
      : '<p class="muted">Каталог tools ещё не загружен.</p>'
    const form = `<form id="skill-form" class="skill-form"><p class="skill-form-hint">Описание в каталоге → подключение к проекту → отметка в конструкторе агента. Компаньон тоже может подготовить черновик на ревью.</p><label>Название<input id="skill-name" required maxlength="120" value="${esc(editing?.name || '')}" placeholder="PostgreSQL Expert"></label><label>Описание<textarea id="skill-description" rows="2" maxlength="4096" placeholder="Проверяемая практика для агентов проекта">${esc(editing?.description || '')}</textarea></label><label>Инструкции<textarea id="skill-instructions" rows="6" maxlength="32768" required placeholder="Что делать, какие проверки выполнить, чего избегать…">${esc(editing?.instructions || '')}</textarea></label>${toolGrid}${editing?.id ? `<label class="skill-equip-option"><input id="skill-deprecated" type="checkbox" ${editing?.configuration?.lifecycleStatus === 'deprecated' ? 'checked' : ''}><span><strong>Пометить навык устаревшим</strong><small>Он останется в истории и существующих snapshots, но будет скрыт из выбора при новых назначениях.</small></span></label>` : ''}<label class="skill-equip-option"><input id="skill-equip-after-save" type="checkbox" ${ui.skillEquipAfterSave ? 'checked' : ''}><span><strong>После сохранения предложить подключение к проекту</strong><small>Подключение всё равно потребует отдельного подтверждения прав.</small></span></label>${ui.skillFormError ? `<p class="create-step-error">${esc(ui.skillFormError)}</p>` : ''}<div class="skill-form-actions"><button type="submit" class="primary">${editing?.id ? 'Сохранить навык' : 'Создать навык'}</button>${editing?.id ? `<button type="button" class="secondary" data-action="skill-cancel-edit">Отмена</button>` : ''}</div></form>`
    return creationDrawer({
      kicker: 'Каталог',
      title: editing?.id ? 'Редактировать навык' : 'Новый навык',
      body: form,
      open: !(ui.state.boot?.skills || []).length || Boolean(editing),
    })
  }
  function skillsView() {
    const skills = ui.state.boot?.skills || []
    const projectSkills = ui.state.boot?.projectSkills || []
    const blueprints = ui.state.boot?.blueprints || []
    const equipped = new Set(projectSkills.filter(item => item.enabled).map(item => item.skillId))
    const confirmHtml = skillEquipConfirmHtml()
    const cards = skills.slice(0, 24).map(skill => {
      const agents = skillAttachedAgents(skill.id)
      const tools = Array.isArray(skill.requiredTools) ? skill.requiredTools : []
      const deprecated = skill.configuration?.lifecycleStatus === 'deprecated'
      return `<article class="hub-skill-card${deprecated ? ' is-deprecated' : ''}"><header><strong>${esc(skill.name || skill.id)}</strong>${deprecated ? '<em class="equip-off">устарел</em>' : equipped.has(skill.id) ? '<em class="equip-on">в проекте</em>' : '<em class="equip-off">каталог</em>'}</header><small>${esc(skill.description || 'Без описания')}</small><p class="skill-card-tools">${tools.map(tool => `<code title="${esc(tool)}">${esc(toolName(tool))}</code>`).join(' ') || '<span class="muted">без обязательных инструментов</span>'}</p><small class="skill-card-attach">${agents.length ? `У агентов: ${agents.map(agent => esc(agent.name)).join(', ')}` : deprecated ? 'Сохранён для истории и старых snapshots' : 'Не назначен агентам — отметьте в конструкторе'}</small><footer class="hub-card-footer"><button type="button" class="secondary" data-action="skill-edit" data-id="${esc(skill.id)}">Изменить</button>${deprecated || equipped.has(skill.id) ? '' : `<button type="button" class="secondary" data-action="preview-equip-skill" data-id="${esc(skill.id)}">Подключить…</button>`}<button type="button" class="secondary" data-action="tab" data-tab="agents">К агентам</button></footer></article>`
    }).join('')
    return shell(`<main class="hub-skills hub-page"><header class="hub-page-head"><div><h1>Навыки проекта</h1><p>Создайте навык здесь или через компаньона. Подключение к проекту не расширяет права агента скрыто — сначала подтверждение.</p></div><em>${skills.length} шабл. · ${projectSkills.length} в проекте</em></header>${confirmHtml}${skillCurationHtml()}${skills.length ? `<section class="hub-skill-list hub-card-grid">${cards}</section>` : `<div class="empty compact point-frame"><span class="empty-glyph">◈</span><h3>Каталог навыков пуст</h3><p>Создайте первый навык формой ниже — ядро также подставляет базовые при первом запуске.</p></div>`}${skillFormHtml()}${blueprints.length ? `<details class="hire-drawer"><summary>Чертежи агентов <span>${blueprints.length}</span></summary><div class="hub-skill-list hub-card-grid">${blueprints.slice(0, 12).map(item => `<article class="hub-card"><strong>${esc(item.name)}</strong><small>${esc(item.roleDescription || '')}</small></article>`).join('')}</div></details>` : ''}</main>`)
  }
  function experienceSearchHtml() {
    const labels = { memory: 'MEMORY', signal: 'Сигнал', skill_outcome: 'SKILL OUTCOME', improvement: 'Изменение' }
    const result = ui.experienceSearchStatus === 'loading'
      ? '<div class="point-tool-loading"><span class="spinner"></span>Ищу по сохранённому опыту</div>'
      : ui.experienceSearchStatus === 'ready' && ui.experienceSearchItems.length
        ? `<div class="experience-search-results">${ui.experienceSearchItems.map(item => `<article><header><span>${esc(labels[item.kind] || item.kind)}</span><small>${item.createdAt ? new Date(item.createdAt).toLocaleString('ru-RU') : '—'}</small></header><strong>${esc(item.title || item.id)}</strong><p>${esc(item.summary || '')}</p>${item.runId && !String(item.runId).startsWith('manual:') ? `<button type="button" class="secondary" data-action="load-run" data-id="${esc(item.runId)}">Открыть Run</button>` : ''}</article>`).join('')}</div>`
        : ui.experienceSearchStatus === 'ready' ? '<p class="muted">В сохранённой истории совпадений нет.</p>' : ''
    return `<section class="experience-search"><header><div><span>Поиск по опыту</span><strong>Опыт, сигналы, итоги навыков и изменения</strong></div><small>Только сохранённые артефакты текущего проекта</small></header><form id="experience-search-form"><input id="experience-search-query" minlength="2" maxlength="200" value="${esc(ui.experienceSearchQuery)}" placeholder="Например: verification, PostgreSQL, boundary"><button type="submit" class="secondary" ${ui.experienceSearchStatus === 'loading' ? 'disabled' : ''}>Найти</button></form>${result}</section>`
  }
  function manualLearningHtml() {
    const agents = ui.state.boot?.projectAgents || []
    if (!agents.length) return '<section class="manual-learning"><div class="agent-learning-empty"><strong>Сначала добавьте агента</strong><p>Сначала добавьте постоянного агента в проект.</p></div></section>'
    if (ui.manualLearningPreview) {
      const preview = ui.manualLearningPreview
      return `<section class="manual-learning"><header><div><span>Ручное обучение · preview</span><h2>${esc(preview.summary)}</h2></div><em>${preview.scope === 'profile' ? 'Основной профиль' : 'Текущий проект'}</em></header><article class="manual-learning-preview"><p>${esc(preview.content)}</p><ul>${(preview.changes || []).map(item => `<li>${esc(item)}</li>`).join('')}</ul>${(preview.warnings || []).map(item => `<small>⚠ ${esc(item)}</small>`).join('')}${preview.noChange ? '<strong>Изменений нет: такой урок уже сохранён.</strong>' : ''}<footer><button type="button" class="secondary" data-action="cancel-manual-learning">Изменить</button><button type="button" class="primary" data-action="apply-manual-learning" ${preview.noChange || ui.manualLearningStatus === 'applying' ? 'disabled' : ''}>${ui.manualLearningStatus === 'applying' ? 'Применяю…' : 'Подтвердить и обучить'}</button></footer></article></section>`
    }
    const draft = ui.manualLearningDraft || { projectAgentId: agents[0].id, kind: 'memory', scope: 'project', content: '' }
    return `<section class="manual-learning"><p class="skill-form-hint">Сначала Hub покажет точный предпросмотр. Только отдельное подтверждение добавит Memory или Rule; изменение попадёт в журнал и получит точный откат.</p><form id="manual-learning-form"><label>Агент<select id="manual-learning-agent">${agents.map(agent => `<option value="${esc(agent.id)}" ${agent.id === draft.projectAgentId ? 'selected' : ''}>${esc(agent.name)}</option>`).join('')}</select></label><label>Вид урока<select id="manual-learning-kind"><option value="memory" ${draft.kind === 'memory' ? 'selected' : ''}>Memory · факт или принцип</option><option value="instruction" ${draft.kind === 'instruction' ? 'selected' : ''}>Rule · обязательное поведение</option></select></label><label>Область<select id="manual-learning-scope"><option value="project" ${draft.scope === 'project' ? 'selected' : ''}>Только этот проект</option><option value="profile" ${draft.scope === 'profile' ? 'selected' : ''}>Основной профиль · все проекты</option></select></label><label class="manual-learning-content">Урок<textarea id="manual-learning-content" rows="4" maxlength="2000" required placeholder="Короткий проверяемый принцип без секретов и деталей одного тикета">${esc(draft.content || '')}</textarea></label><button type="submit" class="primary" ${ui.manualLearningStatus === 'loading' ? 'disabled' : ''}>${ui.manualLearningStatus === 'loading' ? 'Собираю preview…' : 'Проверить изменение'}</button></form></section>`
  }
  function memoryView() {
    const memories = (ui.state.boot?.memories || []).filter(item => String(item.content || '').trim())
    const editing = ui.memoryEditId ? (ui.memoryDraft || memories.find(item => item.id === ui.memoryEditId)) : null
    const blueprints = ui.state.boot?.blueprints || []
    const agents = ui.state.boot?.projectAgents || []
    const quests = ui.state.boot?.quests || []
    const ownerOptions = `<option value="">Весь проект / без владельца</option><optgroup label="Основные профили">${blueprints.map(item => `<option value="${esc(item.id)}" ${editing?.ownerId === item.id ? 'selected' : ''}>${esc(item.name)}</option>`).join('')}</optgroup><optgroup label="Проектные агенты">${agents.map(item => `<option value="${esc(item.id)}" ${editing?.ownerId === item.id ? 'selected' : ''}>${esc(item.name)}</option>`).join('')}</optgroup><optgroup label="Квесты">${quests.map(item => `<option value="${esc(item.id)}" ${editing?.ownerId === item.id ? 'selected' : ''}>${esc(item.title)}</option>`).join('')}</optgroup>`
    const ownerLabel = item => item.kind === 'profile'
      ? blueprints.find(profile => profile.id === item.ownerId)?.name || item.ownerId
      : agents.find(agent => agent.id === item.ownerId)?.name || quests.find(quest => quest.id === item.ownerId)?.title || item.ownerId || 'весь проект'
    const newForm = `<form id="memory-form" class="memory-form"><label>Область памяти<select id="memory-kind" ${editing?.id ? 'disabled' : ''}>${Object.entries(memoryKindLabels).map(([value, label]) => `<option value="${esc(value)}" ${editing?.kind === value ? 'selected' : ''}>${esc(label)}</option>`).join('')}</select><small>«Основной профиль» переносится между проектами; остальные записи остаются локальными.</small></label><label>Владелец<select id="memory-owner">${ownerOptions}</select><small>Для основного профиля, проектного агента или квеста выберите владельца.</small></label><label>Содержание<textarea id="memory-content" rows="3" maxlength="65536" required>${esc(editing?.content || '')}</textarea></label><label>Источник<input id="memory-source" maxlength="4096" value="${esc(editing?.source || '')}" placeholder="квест, диалог, Change Set…"></label><label>Уверенность<input id="memory-confidence" type="number" min="0" max="1" step="0.05" value="${Number(editing?.confidence ?? 0.5)}"></label><label><input id="memory-pinned" type="checkbox" ${editing?.pinned ? 'checked' : ''}> Закрепить</label><div class="memory-form-actions"><button type="submit" class="primary">${editing?.id ? 'Сохранить' : 'Добавить'}</button>${editing?.id ? `<button type="button" class="secondary" data-action="memory-cancel-edit">Отмена</button>` : ''}</div></form>`
    return shell(`<main class="hub-memory hub-page"><header class="hub-page-head"><div><h1>Опыт команды и факты проекта</h1><p>Переносимый опыт хранится у основного профиля агента. Архитектурные факты, квесты и контекст остаются внутри текущего проекта.</p></div><em>${memories.length}</em></header>${experienceSearchHtml()}${memories.length ? memories.slice(0, 50).map(item => `<article class="memory-card hub-card ${item.pinned ? 'pinned' : ''}"><header><strong class="memory-kind-badge kind-${esc(item.kind)}">${esc(memoryKindLabels[item.kind] || item.kind)}</strong><span title="Насколько ядро уверено в этой записи">уверенность ${Math.round(Number(item.confidence || 0) * 100)}%</span></header><p>${esc(item.content)}</p><small>Владелец: ${esc(ownerLabel(item))} · ${item.kind === 'profile' ? 'все проекты' : 'текущий проект'} · ${new Date(item.updatedAt || item.createdAt).toLocaleString('ru-RU')}</small><details><summary>Источник и запись</summary><code>${esc(item.source || 'без источника')}</code><small>ID: ${esc(item.id)} · создано ${new Date(item.createdAt).toLocaleString('ru-RU')}</small></details><footer class="hub-card-footer"><button type="button" class="secondary" data-action="memory-edit" data-id="${esc(item.id)}">Изменить</button><button type="button" class="secondary" data-action="memory-pin" data-id="${esc(item.id)}" data-pinned="${item.pinned ? 'false' : 'true'}">${item.pinned ? 'Открепить' : 'Закрепить'}</button><button type="button" class="danger-button" data-action="memory-delete" data-id="${esc(item.id)}">Удалить</button></footer></article>`).join('') : `<div class="empty compact point-frame"><span class="empty-glyph">✦</span><h3>Память пуста</h3><p>Добавьте переносимый опыт основному профилю или локальный факт текущему проекту.</p><div class="empty-next"><button type="button" class="secondary" data-action="tab" data-tab="overview">К компаньону →</button><button type="button" class="secondary" data-action="tab" data-tab="quests">К квестам →</button></div></div>`}${creationDrawer({ kicker: 'Ручное обучение', title: 'Покажите агенту устойчивый урок', body: manualLearningHtml(), open: Boolean(ui.manualLearningPreview), badge: '2 Шага' })}${creationDrawer({ kicker: 'Запись', title: editing?.id ? 'Редактировать память' : 'Новая запись', body: newForm, open: !memories.length || Boolean(editing?.id) })}</main>`)
  }
  const statisticsViews = createStatisticsViews({
    getStatisticsStatus: () => ui.statisticsStatus,
    setStatisticsStatus: value => { ui.statisticsStatus = value },
    getStatisticsData: () => ui.statisticsData,
    postMessage: message => vscode.postMessage(message),
    shell,
    escapeHtml: esc,
    countOf,
    formatDuration,
    formatCents,
    isStatisticsView,
  })
  function statisticsView() { return statisticsViews.statisticsView() }
  function dockerContainerName(item) {
    return String(item?.Names || item?.names || item?.Name || item?.ID || item?.Id || item?.raw || '').split(',')[0].trim().replace(/^\/+/, '')
  }
  function dockerView() {
    if (ui.dockerStatus === 'idle') {
      ui.dockerStatus = 'loading'
      setTimeout(() => vscode.postMessage({ type: 'loadDocker' }), 0)
    }
    const status = ui.dockerData?.status || {}
    const containers = Array.isArray(ui.dockerData?.containers) ? ui.dockerData.containers : []
    const images = Array.isArray(ui.dockerData?.images) ? ui.dockerData.images : []
    const available = Boolean(status.available)
    const daemon = Boolean(status.daemon)
    // Отказ запроса и отсутствие Docker выглядели одинаково: раздел уверенно
    // писал «CLI НЕ НАЙДЕН» и «Контейнеров нет», хотя на деле не сумел спросить
    // ядро. Человек делал вывод про свою машину по нашей неудаче.
    const unknown = ui.dockerStatus === 'error' && !ui.dockerData
    // Обновление не удалось, но прежние данные есть. Показывать их лучше, чем
    // стирать, — врать про их свежесть нельзя: рядом с сообщением «демон не
    // отвечает» раздел уверенно писал «ДЕМОН ДОСТУПЕН», показывал контейнер как
    // работающий и предлагал кнопку Stop. Кнопки оставляем: команда сама скажет,
    // если контейнера уже нет, а запрет действия помешал бы там, где всё цело.
    const stale = ui.dockerStatus === 'error' && Boolean(ui.dockerData)
    const statusTone = unknown || stale ? 'warning' : !available ? 'danger' : daemon ? 'safe' : 'warning'
    const statusLabel = unknown ? 'Неизвестно' : !available ? 'CLI не найден' : daemon ? 'Демон доступен' : 'Демон недоступен'
    const containerRows = containers.length
      ? containers.map(item => {
        const name = dockerContainerName(item)
        const stateLabel = esc(item.Status || item.State || '—')
        const image = esc(item.Image || item.Repository || '—')
        const running = /up|running/i.test(String(item.Status || item.State || ''))
        return `<article class="docker-row"><header><strong>${esc(name || '—')}</strong><em>${stateLabel}</em></header><p>${image}</p><footer><button type="button" class="secondary" data-action="docker-logs" data-container="${esc(name)}" ${name ? '' : 'disabled'}>Логи</button><button type="button" class="secondary" data-action="docker-terminal-logs" data-container="${esc(name)}" ${name ? '' : 'disabled'}>Терминал · logs</button><button type="button" class="secondary" data-action="docker-terminal-shell" data-container="${esc(name)}" ${name ? '' : 'disabled'}>Терминал · sh</button><button type="button" class="primary" data-action="docker-control" data-action-kind="${running ? 'stop' : 'start'}" data-container="${esc(name)}" ${name ? '' : 'disabled'}>${running ? 'Stop' : 'Start'}</button></footer></article>`
      }).join('')
      : unknown
        ? '<p class="muted">Не удалось спросить ядро — какие контейнеры есть, неизвестно.</p>'
        : '<p class="muted">Контейнеров нет (или демон недоступен).</p>'
    const imageRows = images.length
      ? `<div class="docker-images">${images.slice(0, 40).map(item => `<span><b>${esc(item.Repository || item.ID || '—')}</b><small>${esc(item.Tag || '')} · ${esc(item.Size || '')}</small></span>`).join('')}</div>`
      : unknown ? '<p class="muted">Не удалось спросить ядро — образы неизвестны.</p>' : '<p class="muted">Образов нет.</p>'
    const logsBlock = ui.dockerLogs
      ? `<section class="docker-logs"><header><strong>Логи · ${esc(ui.dockerLogsContainer || ui.dockerLogs.container || '')}</strong><button type="button" class="secondary" data-action="docker-clear-logs">Скрыть</button></header><pre>${esc(ui.dockerLogs.logs || '')}</pre></section>`
      : ''
    return shell(`<main class="hub-docker-page"><header class="changes-heading"><div><span>Docker</span><h1>Локальные контейнеры</h1><p>Статус CLI и демона, контейнеры, логи, start/stop. Удаление (rm/rmi/prune) здесь недоступно. Это не sandbox-изоляция Point.</p></div><div class="docker-heading-actions"><button type="button" class="secondary" data-action="docker-terminal-ps">Терминал · ps</button><button type="button" class="secondary" data-action="reload-docker">Обновить</button></div></header><section class="docker-status ${statusTone}"><div><small>Статус</small><strong>${statusLabel}</strong></div><div><small>CLI</small><b>${esc(status.clientVersion || (unknown ? '—' : available ? 'есть' : 'нет'))}</b></div><div><small>SERVER</small><b>${esc(status.serverVersion || '—')}</b></div><div><small>PATH</small><b>${esc(status.cliPath || '—')}</b></div>${status.error || ui.dockerData?.error ? `<p>${esc(status.error || ui.dockerData.error)}</p>` : ''}${status.hint ? `<small>${esc(status.hint)}</small>` : ''}</section>${ui.dockerStatus === 'loading' ? '<div class="hall-strip is-quiet"><i></i><span>Спрашиваю ядро — состояние ниже ещё не пришло.</span></div>' : ''}${stale ? '<div class="hall-strip"><i></i><span>Обновить не удалось: показано состояние последнего успешного опроса. Оно могло измениться.</span></div>' : ''}<section class="docker-section"><header><strong>Контейнеры</strong><em>${unknown ? '—' : containers.length}</em></header>${containerRows}</section><section class="docker-section"><header><strong>Образы</strong><em>${unknown ? '—' : images.length}</em></header>${imageRows}</section>${logsBlock}</main>`)
  }
  const infrastructureViews = createInfrastructureViews({
    getState: () => ui.state,
    getDatabaseState: () => ({
      selectedId: ui.dbSelectedId,
      queryResult: ui.dbQueryResult,
      queryStatus: ui.dbQueryStatus,
      schemaResult: ui.dbSchemaResult,
      writePending: ui.dbWritePending,
    }),
    getEditingState: () => ({ dbId: ui.dbEditingId, serverId: ui.serverEditingId, connectionId: ui.connectionEditingId }),
    setDatabaseSelectedId: value => { ui.dbSelectedId = value },
    shell,
    escapeHtml: esc,
    countOf,
    connectionStatusLabels,
    connectionManagerHtml,
    toolWindowFrame,
    isConnectionsView,
  })
  function dbDriverLabel(driver) { return infrastructureViews.dbDriverLabel(driver) }
  function databasesView() { return infrastructureViews.databasesView() }
  function serversView() { return infrastructureViews.serversView() }
  function featuredProviderChips(providers) { return infrastructureViews.featuredProviderChips(providers) }
  function connectionsView() { return infrastructureViews.connectionsView() }

  return {
    sandboxBoundaryHtml,
    overview,
    situationRoomHtml,
    unverifiedChangesHtml,
    recentlyBrokenRuns,
    hubHealthRowsHtml,
    teamsView,
    projectFlowsView,
    skillAttachedAgents,
    skillCurationHtml,
    skillFormHtml,
    skillsView,
    experienceSearchHtml,
    manualLearningHtml,
    memoryView,
    statisticsViews,
    statisticsView,
    dockerContainerName,
    dockerView,
    infrastructureViews,
    dbDriverLabel,
    databasesView,
    serversView,
    featuredProviderChips,
    connectionsView,
  }
}
