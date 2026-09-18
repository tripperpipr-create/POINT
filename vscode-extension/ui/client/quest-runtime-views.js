export function createQuestRuntimeViews(dependencies) {
  const {
    CREATE_FLOW_STAGES,
    EMPTY_TASK_REASON,
    PROFILE_STEPS,
    TOOL_PRESETS,
    activeExecutions,
    agentById,
    blueprintById,
    changeSetStatusLabels,
    compactQuestTitle,
    createCompanionMarkdownFormatter,
    createGitViews,
    currentHubQuest,
    databasesView,
    decisionsWaitingCount,
    execControlsHtml,
    gitWide,
    hallActiveSection,
    hallAlarmHtml,
    hallCrumb,
    hallSectionBadge,
    hallSpendLabel,
    hubAgents,
    isDockerView,
    isConnectionsView,
    isStatisticsView,
    modelChipHtml,
    isSystemOnboardingStep,
    isToolWindow,
    toolWindowFrame,
    pendingActionProposals,
    pendingChangeSets,
    pendingQuestProposals,
    persistDraft,
    projectSwitcherChipHtml,
    projectChatDirectoryHtml,
    hallChangesAlarmHtml,
    masterBriefTabHtml,
    questStatusLabels,
    render,
    root,
    runIsFinished,
    runIsLive,
    statusLabels,
    toolLabels,
    eventLabels,
    healthLabels,
    stopReasonLabels,
    plural,
    countOf,
    esc,
    formatCompanionMarkdown,
    data,
    toolName,
    providerCatalog,
    providerPreset,
    requiresApiKey,
    lines,
    toolProvidesVerification,
    serversView,
    toolWindowKind,
    ui,
    vscode,
  } = dependencies

  // Готовность агента приходит из ядра: те же правила, что решают, запустится
  // квест или нет. Локальная копия знала про допуски меньше движка и расходилась
  // с ним на пользовательских инструментах — форма обещала запуск, который потом
  // отклонялся.
  let agentCapability
  let agentCapabilityKey = ''
  let capabilityDelta
  let capabilityDeltaKey = ''
  let capabilityDeltaAnswerKey = ''
  let handoffChain
  let handoffFlowRunId = ''
  let questOutcome
  let questOutcomeId = ''
  let questReplans
  let questReplansId = ''
  // Какой квест раскрыт в списке. Разбор готовности запрашивается только для
  // него: одноместный кэш не переживёт списка из нескольких квестов.
  let openQuestId = ''

  function toggleOpenQuest(id) {
    openQuestId = openQuestId === id ? '' : id
  }

  // Удалённый квест обязан закрыться: раскрытой осталась бы строка, которой
  // больше нет, и разбор готовности продолжал бы запрашиваться по её номеру.
  function closeQuestIfOpen(id) {
    if (openQuestId && openQuestId === id) openQuestId = ''
  }

  function setQuestOutcome(value) { questOutcome = value }
  function setQuestReplans(value) { questReplans = Array.isArray(value) ? value : [] }
  function resetQuestReplansCache() { questReplansId = ''; questReplans = undefined }
  function setHandoffChain(value) { handoffChain = value }
  function setCapabilityDelta(value, answerKey) {
    capabilityDelta = value
    capabilityDeltaAnswerKey = String(answerKey || '')
  }
  function setAgentCapability(value) { agentCapability = value }
  
  function agentCapabilityKeyFor(profile) {
    return [profile?.id, profile?.provider, profile?.model, profile?.maxSteps,
      profile?.approvalMode, (profile?.allowedTools || []).join(',')].join('|')
  }
  
  const AGENT_CAPABILITY_LIMIT = 64
  const agentCapabilityCache = new Map()
  const agentCapabilityInflight = new Set()
  // Ключи, на которые ядро отказало. Без них запрос оставался бы в inflight
  // навсегда: снимался он только приходом ответа, а после отказа ответа нет —
  // и повторить было нечем, потому что повтор блокировал тот же inflight.
  // Автоповтор здесь намеренно не заводим: постоянный отказ превратился бы в
  // бесконечный поток запросов. Достаточно перестать врать про загрузку.
  const agentCapabilityFailed = new Set()
  
  function requestAgentCapability(profile) {
    const key = agentCapabilityKeyFor(profile)
    if (!key) return
    // Уже знаем или уже спросили — второй запрос только разбудит новый render.
    if (agentCapabilityCache.has(key) || agentCapabilityInflight.has(key) || agentCapabilityFailed.has(key)) return
    agentCapabilityInflight.add(key)
    setTimeout(() => vscode.postMessage({ type: 'agentCapability', key, profile: {
      id: profile?.id || '', provider: profile?.provider || '', model: profile?.model || '',
      allowedTools: profile?.allowedTools || [], maxSteps: Number(profile?.maxSteps) || 0,
      approvalMode: profile?.approvalMode || '',
    } }), 0)
  }
  
  // Годность именно этого профиля, а не последнего пришедшего ответа.
  function agentCapabilityFor(profile) {
    return agentCapabilityCache.get(agentCapabilityKeyFor(profile))
  }
  
  function profileReadiness(profile) {
    if (!profile) return { ready: false, issues: ['Персонаж не выбран'], blockers: [{ text: 'Персонаж не выбран', step: 'identity' }], hasVerifier: false, canWrite: false, toolCount: 0, lines: [] }
    requestAgentCapability(profile)
    const capability = agentCapabilityFor(profile) || {}
    // Ответа нет и не будет: ядро отказало. Гейт оставляем прежним — запрет всё
    // равно за бэкендом, — но заявлять готовность по неприсланным данным нельзя.
    const unknown = !agentCapabilityFor(profile) && agentCapabilityFailed.has(agentCapabilityKeyFor(profile))
    const blockers = capability.blockers || []
    const issues = capability.blocking || []
    return {
      // Пока ответ ядра не пришёл, готовность не отрицается: блокировать действие
      // из-за незнания — значит наказывать пользователя за сетевой ход. Настоящий
      // запрет всё равно за бэкендом, он независимо отклоняет невозможный запуск.
      ready: issues.length === 0,
      unknown,
      issues,
      blockers,
      warnings: capability.warnings || [],
      lines: capability.lines || [],
      hasVerifier: Boolean(capability.canVerify),
      canWrite: Boolean(capability.canWrite),
      toolCount: (profile.allowedTools || []).length,
    }
  }
  
  function requestCapabilityDelta(profile, addTools, removeTools) {
    const key = [profile?.id, (addTools || []).join(','), (removeTools || []).join(',')].join('|')
    if (key === capabilityDeltaKey) return
    capabilityDeltaKey = key
    setTimeout(() => vscode.postMessage({ type: 'capabilityDelta', key,
      profile: {
        id: profile?.id || '', provider: profile?.provider || '', model: profile?.model || '',
        allowedTools: profile?.allowedTools || [], maxSteps: Number(profile?.maxSteps) || 0,
        approvalMode: profile?.approvalMode || '',
      },
      addTools: addTools || [], removeTools: removeTools || [],
    }), 0)
  }
  
  // «Требует run_command» не отвечает на вопрос, ради которого навык берут.
  // Здесь — что агент начнёт мочь и снимется ли препятствие для квеста.
  function capabilityDeltaHtml(profile, addTools, removeTools) {
    if (!profile || ((addTools || []).length === 0 && (removeTools || []).length === 0)) return ''
    requestCapabilityDelta(profile, addTools, removeTools)
    if (capabilityDeltaAnswerKey !== capabilityDeltaKey) return ''
    const lines = capabilityDelta?.lines || []
    if (!lines.length) return ''
    const breaking = (capabilityDelta?.introduced || []).length > 0
    return `<div class="hall-delta${breaking ? ' is-breaking' : ''}">
      <b>ЧТО ИЗМЕНИТСЯ</b>
      ${lines.map(line => `<span>${esc(line)}</span>`).join('')}
    </div>`
  }
  
  // Сверка обещания с результатом. Квест закрывался, а определение готовности
  // оставалось словами: статус говорил «завершён», и на этом всё.
  // Список квестов проекта.
  //
  // Раздел «Квесты» показывал только форму создания нового: существующие квесты
  // не было видно нигде, хотя счётчик в рейке их считал. Раздел, который называет
  // себя «активные квесты», обязан их показывать.
  // Что происходило по квесту: прогоны и наборы изменений.
  //
  // Раскрытый квест показывал только сверку обещаний — «подтверждено 1 из 3».
  // Верно, но не отвечает на вопрос «как идёт работа»: кто её вёл, чем кончились
  // попытки, что уже лежит на ревью. Всё это есть в состоянии, и запрашивать
  // ничего не нужно — важно, потому что разбор кэшируется одноместно и лишний
  // запрос на строку списка вернул бы бесконечную перерисовку.
  function questWorkHtml(quest) {
    const questId = String(quest?.id || '')
    if (!questId) return ''
    const agents = hubAgents()
    const nameOf = id => (agents.find(item => item.id === id) || {}).name || ''
    const executions = (ui.state.boot?.executions || []).filter(item => item.questId === questId)
    const executionIds = new Set(executions.map(item => item.id))
    const sets = (ui.state.boot?.changeSets || []).filter(item =>
      item.questId === questId || executionIds.has(item.executionId))
  
    const runsBlock = executions.length
      ? `<div class="hall-quest-block">
          <span class="hall-quest-block-label">Прогоны</span>
          ${executions.map(item => `<div class="hall-quest-line">
            <b>${esc(statusLabels[item.status] || item.status || '')}</b>
            <span>${esc(item.task || 'без описания')}</span>
            <small>${esc(nameOf(item.projectAgentId) || 'исполнитель неизвестен')}</small>
          </div>`).join('')}
        </div>`
      : '<div class="hall-quest-block"><span class="hall-quest-block-label">Прогоны</span><small class="hall-quest-empty">Ещё не запускался.</small></div>'
  
    const setsBlock = sets.length
      ? `<div class="hall-quest-block">
          <span class="hall-quest-block-label">Изменения</span>
          ${sets.map(item => `<div class="hall-quest-line">
            <b>${esc(changeSetStatusLabels[item.status] || item.status || '')}</b>
            <span>${esc(item.title || 'Набор изменений')}</span>
            <small>${esc(countOf((item.items || []).length, 'файл', 'файла', 'файлов'))}</small>
          </div>`).join('')}
          <button class="hall-btn is-sm" data-action="tab" data-tab="changesets">ОТКРЫТЬ НАБОРЫ</button>
        </div>`
      : '<div class="hall-quest-block"><span class="hall-quest-block-label">Изменения</span><small class="hall-quest-empty">Правок пока нет.</small></div>'

    const children = (ui.state.boot?.quests || []).filter(item => item.parentId === questId)
    const prep = (ui.state.boot?.agentPrepChains || []).filter(item => item.parentQuestId === questId)
    const messages = (ui.state.boot?.teamEvents || []).filter(item => item.questId === questId)
    const modelRuns = executions.filter(item => item.snapshot?.profile?.model).map(item => {
      const profile = item.snapshot.profile
      const runtime = profile.provider || 'runtime'
      return `<span class="hall-chip">${esc(runtime)} · ${esc(profile.model)}</span>`
    }).join('')
    const controller = quest.controller || {}
    const preview = Array.isArray(controller.planPreview) ? controller.planPreview : []
    const previewLines = preview.map(stage => `<div class="hall-quest-line"><b>${esc(stage.name || 'этап')}</b><span>${esc([stage.runtime, stage.model].filter(Boolean).join(' · ') || '')}</span><small>${esc(stage.estimatedCostCents > 0 ? `${stage.estimatedCostCents}¢` : '')}</small></div>`).join('')
    const blockerEvent = controller.blocker
      ? `<div class="hall-quest-line"><b>blocker</b><span>${esc(controller.blocker)}</span><small>${esc(quest.controllerState || '')}</small></div>`
      : ''
    const autonomyBlock = `<div class="hall-quest-block">
      <span class="hall-quest-block-label">Контроллер · ${esc(quest.controllerState || 'legacy')}</span>
      ${modelRuns || '<small class="hall-quest-empty">Назначения моделей появятся после запуска этапов.</small>'}
      ${previewLines}
      ${children.map(item => `<div class="hall-quest-line"><b>${esc(item.kind || 'milestone')}</b><span>${esc(item.title || '')}</span><small>${esc(questStatusLabels[item.status] || item.status || '')}</small></div>`).join('')}
      ${prep.map(item => `<div class="hall-quest-line"><b>специалист · ${esc(item.state || '')}</b><span>${esc(item.requirement?.role || '')}</span><small>${esc(item.error || '')}</small></div>`).join('')}
      ${messages.slice(-8).map(item => `<div class="hall-quest-line"><b>${esc(item.kind || 'status')}</b><span>${esc(item.message || '')}</span><small>${esc(item.fromAgentId || 'master')}</small></div>`).join('')}
      ${unansweredQuestions(messages)}
      ${controller.pendingReplan ? `<div class="hall-quest-line"><b>replan</b><span>${esc(controller.pendingReplanReason || 'нужно перепланировать волну')}</span><small>форма ниже</small></div>` : ''}
      ${blockerEvent}
      ${messages.length ? `<small>${esc(countOf(messages.length, 'сообщение', 'сообщения', 'сообщений'))} команды</small>` : ''}
    </div>`
    //
    // Раскрытый квест был отчётом: показывал состояние, но действовать из него
    // было нельзя. При этом именно здесь видно, что работа стоит — и именно
    // отсюда естественно уйти разбирать очередь.
    const waitingRuns = executions.filter(item =>
      item.status === 'waiting_approval' || item.status === 'waiting').length
    const waitingSets = sets.filter(item => item.status === 'pending').length
    const waiting = waitingRuns + waitingSets
    const reasons = []
    if (waitingRuns) reasons.push(countOf(waitingRuns, 'запуск ждёт', 'запуска ждут', 'запусков ждут') + ' подтверждения')
    if (waitingSets) reasons.push(countOf(waitingSets, 'набор ждёт', 'набора ждут', 'наборов ждут') + ' ревью')
    const blocker = waiting
      ? `<div class="hall-quest-blocker">
          <span>Работа стоит: ${esc(reasons.join(', '))}.</span>
          <button class="hall-btn is-sm is-primary" data-action="tab" data-tab="decisions">РАЗОБРАТЬ</button>
        </div>`
      : ''
  
    // Убрать квест можно только оттуда, где видно, что за ним стоит: прогоны,
    // наборы правок и подквесты перечислены выше этой кнопки. Отказ ядра
    // назовёт то, что держит квест, — здесь мы лишь предлагаем действие.
    const removal = `<div class="hall-quest-block">
      <span class="hall-quest-block-label">Карточка квеста</span>
      <small class="hall-quest-empty">Удаление убирает квест из списка проекта. Хроника прогонов остаётся и переезжает в «запуски без квеста».</small>
      <button type="button" class="hall-btn is-sm is-danger" data-action="delete-quest" data-id="${esc(questId)}">УДАЛИТЬ КВЕСТ</button>
    </div>`

    return `<div class="hall-quest-work">${blocker}${autonomyBlock}${runsBlock}${setsBlock}${questMidFlightHtml(quest)}${removal}</div>`
  }

  function unansweredQuestions(messages) {
    const open = (messages || []).filter((item, index) => {
      if (item.kind !== 'question') return false
      return !(messages || []).slice(index + 1).some(later => later.kind === 'answer')
    })
    if (!open.length) return ''
    return `<div class="hall-quest-line"><b>без ответа</b><span>${esc(open.map(item => item.message).join(' · '))}</span><small>${esc(String(open.length))}</small></div>`
  }

  function questFlowAgentNodes(quest) {
    const questId = String(quest?.id || '')
    const flowId = String(quest?.flowId || '')
    const runs = (ui.state.boot?.flowRuns || []).filter(run =>
      (questId && run.questId === questId) || (flowId && run.flowId === flowId))
    const live = runs.find(run => ['running', 'waiting', 'paused', 'interrupted'].includes(String(run.status || ''))) || runs[0]
    const snapNodes = live?.snapshot?.graph?.nodes
    const flow = (ui.state.boot?.flows || []).find(item => item.id === flowId)
    const nodes = Array.isArray(snapNodes) ? snapNodes : (flow?.nodes || [])
    return nodes.filter(node => String(node.kind || '') === 'agent')
  }

  function questMidFlightHtml(quest) {
    const brief = quest?.brief
    if (!brief || !['active', 'running', 'paused'].includes(String(quest?.status || ''))) return ''
    const questId = String(quest.id || '')
    if (!questId) return ''
    if (questId !== questReplansId) {
      questReplansId = questId
      setTimeout(() => vscode.postMessage({ type: 'loadQuestReplans', questId }), 0)
    }
    const nodes = questFlowAgentNodes(quest)
    const criteria = brief.criteria || []
    const history = (questReplans || []).filter(item => item.questId === questId)
    const historyBlock = history.length
      ? `<div class="hall-quest-block"><span class="hall-quest-block-label">История replan</span>${history.slice().reverse().slice(0, 5).map(item =>
          `<div class="hall-quest-line"><b>#${esc(String(item.seq))}</b><span>${esc(item.reason || '')}</span><small>${esc((item.updatedNodes || []).join(', '))}</small></div>`
        ).join('')}</div>`
      : ''
    const nodeOptions = nodes.length
      ? nodes.map(node => `<option value="${esc(node.id)}">${esc(node.name || node.id)}</option>`).join('')
      : '<option value="">нет agent-этапов</option>'
    const criterionChecks = criteria.map(item =>
      `<label class="hall-inline-check"><input type="checkbox" name="replanCriterion" value="${esc(item.id)}"> ${esc(item.text || item.id)}</label>`
    ).join('')
    const version = Number(brief.version || 1)
    const runsLive = (ui.state.boot?.executions || []).filter(item =>
      item.questId === questId && ['running', 'waiting', 'waiting_approval'].includes(String(item.status || '')))
    const pauseHint = runsLive.length
      ? `<aside class="readiness-banner compact"><span>!</span><div><strong>Смена цели</strong><small>Сначала поставьте связанные прогоны на паузу (${runsLive.length} ещё идут).</small></div></aside>`
      : `<small class="hall-quest-empty">Смена цели требует паузы всех связанных прогонов и утверждения новой версии.</small>`
    return `<div class="hall-quest-block quest-midflight" data-quest-id="${esc(questId)}">
      <span class="hall-quest-block-label">Корректировка плана</span>
      ${historyBlock}
      <label>Этап<select name="replanNodeId">${nodeOptions}</select></label>
      <label>Новая инструкция<textarea name="replanInstruction" rows="3" placeholder="что должен сделать этап"></textarea></label>
      <label>Причина<textarea name="replanReason" rows="2" placeholder="почему меняем план"></textarea></label>
      ${criterionChecks ? `<div class="hall-quest-criteria">${criterionChecks}</div>` : ''}
      <button type="button" class="hall-btn is-sm is-primary" data-action="submit-quest-replan" data-quest-id="${esc(questId)}">ПЕРЕПЛАНИРОВАТЬ ЭТАП</button>
      <hr>
      <span class="hall-quest-block-label">Смена цели</span>
      ${pauseHint}
      <label>Новая цель<textarea name="reviseGoal" rows="2" placeholder="${esc(brief.goal || '')}">${esc(brief.goal || '')}</textarea></label>
      <button type="button" class="hall-btn is-sm" data-action="submit-quest-revise" data-quest-id="${esc(questId)}" data-expected-version="${version}" ${runsLive.length ? 'disabled' : ''}>УТВЕРДИТЬ НОВУЮ ЦЕЛЬ</button>
    </div>`
  }
  
  // Запуски, не привязанные ни к одному перечисленному квесту.
  //
  // Счётчик раздела считает и такие исполнения, а раздел о них молчал: значок
  // обещал «1» над пустым экраном. Число, указывающее в никуда, хуже отсутствия
  // числа — человек идёт искать то, чего не показали.
  // Запуски, не принадлежащие ни одному известному квесту. Раздел показывает их
  // отдельной секцией, а значок вкладки обязан их учитывать — поэтому определение
  // живёт в одном месте, а не в двух расходящихся копиях.
  function orphanExecutions() {
    const known = new Set((ui.state.boot?.quests || []).map(item => item.id))
    return activeExecutions().filter(item => !item.questId || !known.has(item.questId))
  }
  
  function orphanExecutionsHtml() {
    const orphans = orphanExecutions()
    if (!orphans.length) return ''
    const agents = hubAgents()
    const nameOf = id => (agents.find(item => item.id === id) || {}).name || 'исполнитель неизвестен'
    return `<section class="hall-panel hall-quest-list">
      <header><b>ЗАПУСКИ БЕЗ КВЕСТА</b><small>${esc(countOf(orphans.length, 'запуск', 'запуска', 'запусков'))}</small></header>
      ${orphans.map(item => `<div class="hall-quest-row"><div class="hall-quest-head">
        <span class="hall-quest-status is-${esc(item.status || '')}">${esc(statusLabels[item.status] || item.status || '')}</span>
        <b>${esc(item.task || 'без описания')}</b>
        <small>${esc(nameOf(item.projectAgentId))}</small>
      </div></div>`).join('')}
    </section>`
  }
  
  function questListHtml() {
    const quests = ui.state.boot?.quests || []
    if (!quests.length) return ''
    const agents = hubAgents()
    const nameOf = id => (agents.find(item => item.id === id) || {}).name || id
    const rows = quests.map(quest => {
      const party = (quest.teamAgentIds || []).map(nameOf).filter(Boolean)
      const open = quest.id === openQuestId
      const status = questStatusLabels[quest.status] || quest.status || ''
      return `<div class="hall-quest-row${open ? ' is-open' : ''}">
        <button class="hall-quest-head" data-action="toggle-quest" data-id="${esc(quest.id)}">
          <span class="hall-quest-status is-${esc(quest.status || 'draft')}">${esc(status)}</span>
          <b>${esc(quest.title || 'Без названия')}</b>
          <small>${party.length ? esc(party.join(', ')) : 'отряд не назначен'}</small>
        </button>
        ${open ? `${questWorkHtml(quest)}${questOutcomeHtml(quest)}` : ''}
      </div>`
    }).join('')
    return `<section class="hall-panel hall-quest-list">
      <header><b>КВЕСТЫ ПРОЕКТА</b><small>${esc(countOf(quests.length, 'квест', 'квеста', 'квестов'))}</small></header>
      ${rows}
    </section>`
  }
  
  function questOutcomeHtml(quest) {
    const id = String(quest?.id || '').trim()
    if (!id) return ''
    // Итог — это разбор завершённой работы, а не прогноз. Для активного квеста
    // отсутствие применённых правок и проверок нормально и не является
    // проваленными обещаниями.
    if (!['completed', 'failed', 'cancelled'].includes(String(quest?.status || ''))) return ''
    if (id !== questOutcomeId) {
      questOutcomeId = id
      setTimeout(() => vscode.postMessage({ type: 'loadQuestOutcome', questId: id }), 0)
    }
    const outcome = questOutcome
    if (!outcome || outcome.questId !== id) return ''
    const promises = outcome.promises || []
    if (!promises.length && !outcome.honest) return ''
    const failing = outcome.total > 0 && outcome.met < outcome.total
    const unverified = outcome.unverifiedReason
      ? `<aside class="readiness-banner compact"><span>!</span><div><strong>Не подтверждено</strong><small>${esc(outcome.unverifiedReason)}</small></div></aside>`
      : ''
    return `<section class="hall-panel is-stacked${failing ? ' is-alarm' : ''}">
      <header><b>ОБЕЩАНО И ПОЛУЧЕНО</b><small>${outcome.met}/${outcome.total}</small></header>
      ${unverified}
      ${promises.map(item => `<div class="hall-panel-row hall-promise">
        <span class="hall-promise-mark${item.met ? ' is-met' : ''}">${item.met ? '✓' : '·'}</span>
        <div class="hall-promise-body">
          <span class="text">${esc(item.text)}</span>
          <span class="evidence">${esc(item.evidence)}</span>
        </div>
      </div>`).join('')}
      <div class="hall-panel-row"><span class="hall-promise-honest">${esc(outcome.honest || '')}</span></div>
    </section>`
  }
  
  // Цепочка передач между агентами: кто кому что отдал. Эстафета работала и
  // раньше, но была видна только модели — когда второй агент делал не то,
  // человек не мог отличить «не понял задачу» от «ему не то передали».
  function handoffsHtml(flowRunId) {
    const id = String(flowRunId || '').trim()
    if (!id) return ''
    if (id !== handoffFlowRunId) {
      handoffFlowRunId = id
      setTimeout(() => vscode.postMessage({ type: 'loadHandoffs', flowRunId: id }), 0)
    }
    if (handoffChain && handoffChain.flowRunId !== id) return ''
    const items = handoffChain?.items || []
    const waiting = handoffChain?.waiting || []
    if (!items.length && !waiting.length) return ''
    return `<section class="hall-panel is-stacked">
      <header><b>ПЕРЕДАЧА МЕЖДУ АГЕНТАМИ</b><small>${items.length}</small></header>
      ${items.map(item => `<div class="hall-panel-row hall-handoff">
        <div class="hall-handoff-who">
          <b>${esc(item.fromAgent || item.fromNode)}</b>
          <span>→</span>
          <b>${esc(item.toAgent || item.toNode)}</b>
          ${item.delivered ? '' : '<span class="hall-chip is-dim">подготовлена</span>'}
        </div>
        ${item.summary ? `<span class="hall-handoff-summary">${esc(item.summary)}</span>` : ''}
        ${(item.changedFiles || []).length
          ? `<div class="hall-handoff-files">${item.changedFiles.map(path => `<span>${esc(path)}</span>`).join('')}</div>`
          : '<span class="hall-handoff-summary is-muted">файлы не изменялись</span>'}
        ${(item.changeSetIds || []).length
          ? `<button class="hall-btn is-sm" data-action="tab" data-tab="changesets">НАБОР ${esc(item.changeSetIds[0])}</button>`
          : ''}
      </div>`).join('')}
      ${waiting.length ? `<div class="hall-panel-row"><span class="hall-handoff-summary is-muted">Ждут предшественника: ${waiting.map(name => esc(name)).join(', ')}</span></div>` : ''}
    </section>`
  }
  
  // Что агент сможет — отдельным блоком: список умений сам по себе не отвечает
  // на вопрос «а что он вообще будет делать».
  function agentCapabilityHtml(profile) {
    const readiness = profileReadiness(profile)
    if (!readiness.lines.length && !readiness.issues.length) return ''
    return `<section class="hall-panel is-stacked${readiness.issues.length ? ' is-alarm' : ''}">
      <header><b>ЧТО СМОЖЕТ АГЕНТ</b><small>${readiness.issues.length ? 'запуск невозможен' : 'готов к запуску'}</small></header>
      <div class="hall-panel-row is-stack is-tight">
        ${readiness.lines.map(line => `<span class="hall-cap-line">${esc(line)}</span>`).join('')}
        ${readiness.issues.map(item => `<span class="hall-cap-line is-blocking">${esc(item)}</span>`).join('')}
        ${(readiness.warnings || []).map(item => `<span class="hall-cap-line is-warning">${esc(item)}</span>`).join('')}
      </div>
    </section>`
  }
  
  const PROFILE_STEP_IDS = new Set(['identity', 'model', 'tools', 'limits'])
  // Шаг починки приходит от ядра вместе с причиной. Незнакомый шаг не
  // выдумывается: лучше открыть первый экран, чем увести в несуществующий.
  function blockerStep(blocker) {
    const step = String(blocker?.step || '')
    return PROFILE_STEP_IDS.has(step) ? step : 'identity'
  }
  function firstUnreadinessStep(profile) {
    const readiness = profileReadiness(profile)
    if (readiness.ready || !readiness.blockers.length) return 'identity'
    return blockerStep(readiness.blockers[0])
  }
  function questBriefQuality() {
    const payload = questPayload()
    const tips = []
    if (!payload.task) tips.push(EMPTY_TASK_REASON)
    else {
      if (!payload.goal) tips.push('Добавьте цель: какой итог должен увидеть пользователь')
      if (!payload.acceptanceCriteria.length) tips.push('Критерии готовности делают финал проверяемым (тест/сборка/линт)')
      if (!payload.constraints.length) tips.push('Ограничения сужают правки и снижают риск лишних изменений')
    }
    const filled = [payload.task, payload.goal, payload.acceptanceCriteria.length > 0, payload.constraints.length > 0].filter(Boolean).length
    return { tips, filled, strong: filled >= 3 && payload.acceptanceCriteria.length > 0 }
  }
  function canAcceptQuest(profile) {
    const readiness = profileReadiness(profile)
    const completionBlocked = ui.agentRunPreview?.completion?.blockingConfigurationIssue === true
    const needsPreflight = Boolean(ui.taskDraft.trim()) && !ui.agentRunPreview?.fingerprint
    const reasons = []
    if (!ui.taskDraft.trim()) reasons.push('Заполните задачу брифинга')
    if (!readiness.ready) reasons.push(readiness.issues[0] || 'Карточка персонажа не готова')
    if (completionBlocked) reasons.push('Нет умения-доказательства для обязательной проверки')
    if (needsPreflight) reasons.push('Сначала выполните «Разведку»')
    return { ok: reasons.length === 0, reasons, readiness, completionBlocked, needsPreflight }
  }
  function questBriefGuidanceHtml(active, cursor) {
    if (active || cursor) return ''
    const quality = questBriefQuality()
    if (!quality.tips.length) {
      return quality.strong
        ? '<aside class="quest-brief-tip ready"><span>✓</span><div><strong>Брифинг достаточно полный</strong><small>Цель и критерии помогут агенту завершить квест проверяемо.</small></div></aside>'
        : ''
    }
    return `<aside class="quest-brief-tip"><span>i</span><div><strong>Качество брифинга · ${quality.filled}/4</strong><small>${esc(quality.tips[0])}</small></div></aside>`
  }
  function runQualityOutcomeHtml(details) {
    const run = details?.run
    if (!run || runIsLive(run)) return ''
    const diagnostics = details.diagnostics || {}
    const signals = Array.isArray(diagnostics.signals) ? diagnostics.signals : []
    const denials = Number(diagnostics.approvals?.denied || 0)
    const toolFails = Number(diagnostics.tools?.failed || 0)
    const verificationMissing = signals.some(signal => signal.code === 'verification_missing' || signal.code === 'completion_evidence_missing')
    const verificationOk = signals.some(signal => signal.code === 'verification_recorded' || signal.code === 'completion_revised')
    const failed = run.status === 'failed' || run.status === 'cancelled' || run.status === 'interrupted' || diagnostics.health === 'failed'
    const tone = failed ? 'danger' : (verificationMissing || denials || toolFails ? 'warning' : 'success')
    const title = run.status === 'completed'
      ? (verificationMissing ? 'Квест завершён без доказательства' : 'Квест завершён')
      : (statusLabels[run.status] || run.status)
    const facts = [
      toolFails ? countOf(toolFails, 'сбой умения', 'сбоя умений', 'сбоев умений') : '',
      denials ? countOf(denials, 'отказ', 'отказа', 'отказов') : '',
      verificationOk ? 'проверка зафиксирована' : '',
      verificationMissing ? 'нет успешной проверки' : '',
    ].filter(Boolean)
    const fixStep = verificationMissing || denials || toolFails ? 'tools' : 'limits'
    return `<aside class="run-quality-outcome ${tone}"><span>${tone === 'success' ? '✓' : tone === 'danger' ? '!' : '△'}</span><div><strong>${esc(title)}</strong><small>${esc(facts.join(' · ') || stopReasonLabels[diagnostics.stopReason] || 'Итог по хронике запуска')}</small></div><button type="button" class="secondary" data-action="fix-profile-step" data-step="${esc(fixStep)}">Профиль →</button></aside>`
  }
  function historyQualitySignals(diagnostics) {
    if (!diagnostics) return ''
    const signals = Array.isArray(diagnostics.signals) ? diagnostics.signals : []
    const bits = []
    if (signals.some(signal => signal.code === 'verification_recorded')) bits.push('proof ✓')
    if (signals.some(signal => signal.code === 'verification_missing' || signal.code === 'completion_evidence_missing')) bits.push('proof !')
    if (signals.some(signal => signal.code === 'completion_revised')) bits.push('доработка')
    if (Number(diagnostics.tools?.failed || 0) > 0) bits.push(countOf(diagnostics.tools.failed, 'сбой', 'сбоя', 'сбоев'))
    if (Number(diagnostics.approvals?.denied || 0) > 0) bits.push(countOf(diagnostics.approvals.denied, 'отказ', 'отказа', 'отказов'))
    return bits.length ? bits.map(bit => `<span>${esc(bit)}</span>`).join('') : `<span>${countOf(diagnostics.tools?.failed || 0, 'сбой умения', 'сбоя умений', 'сбоев умений')}</span><span>${countOf(diagnostics.approvals?.denied || 0, 'отказ', 'отказа', 'отказов')}</span>`
  }
  function flowStages(creating) {
    return creating ? CREATE_FLOW_STAGES : PROFILE_STEPS
  }
  function activeToolPresetId(profile) {
    const enabled = [...(profile?.allowedTools || [])].sort().join(',')
    const catalog = (ui.state.boot?.toolCatalog || []).map(item => item.name).sort().join(',')
    for (const preset of TOOL_PRESETS) {
      const tools = preset.tools === null ? catalog : [...(preset.tools || [])].sort().join(',')
      if (tools === enabled) return preset.id
    }
    return ''
  }
  function stepValidationIssue(step, profile) {
    if (!profile) return 'Карточка не загружена'
    if (step === 'class') return ''
    if (step === 'identity') {
      if (!(profile.name || '').trim()) return 'Укажите имя персонажа'
      return ''
    }
    if (step === 'model') {
      if (!(profile.model || '').trim()) return 'Укажите model ID или нажмите «Проверить и найти модели»'
      return ''
    }
    if (step === 'tools') {
      const readiness = profileReadiness(profile)
      if (profile.provider && !readiness.toolCount) return 'Выберите хотя бы одно умение или пресет'
      if (readiness.canWrite && !readiness.hasVerifier) return 'Для правок включите умение-доказательство (Запуск команд)'
      return ''
    }
    if (step === 'limits') {
      const steps = Number(profile.maxSteps || 0)
      if (steps < 1) return 'Задайте лимит ходов (рекомендуем 30)'
      return ''
    }
    return ''
  }
  function templateClassPreview(template) {
    if (!template) {
      return `<aside class="create-class-preview empty"><span class="quest-label">ОСНОВНОЙ ПРОФИЛЬ</span><strong>Выберите специалиста слева</strong><p>Справа появятся его постоянная роль, навыки, умения, лимиты и правила. Для проекта будет создана отдельная адаптация.</p></aside>`
    }
    const tools = template.allowedTools || []
    const catalog = ui.state.boot?.toolCatalog || []
    const hasVerifier = tools.some(name => toolProvidesVerification(catalog.find(item => item.name === name) || { name }))
    const chips = tools.map(name => {
      const tool = catalog.find(item => item.name === name)
      const verifies = toolProvidesVerification(tool || { name })
      return `<span class="${verifies ? 'proof' : ''}">${esc(toolName(name))}${verifies ? ' · proof' : ''}</span>`
    }).join('')
    return `<aside class="create-class-preview"><span class="quest-label">ОСНОВНОЙ ПРОФИЛЬ</span><header><strong>${esc(template.name)}</strong><em>${countOf(tools.length, 'tool', 'tools', 'tools')} · ${countOf(template.maxSteps || 30, 'шаг', 'шага', 'шагов')}</em></header><p>${esc(template.roleDescription || template.description)}</p><div class="create-preview-facts"><span><small>ЛИМИТ</small><b>${esc(template.maxSteps || 30)}</b></span><span><small>ТАЙМ-АУТ</small><b>${esc(template.maxDurationSeconds || 600)}с</b></span><span><small>PROOF</small><b>${hasVerifier ? 'есть' : 'нет'}</b></span><span><small>ПОДТВЕРЖД.</small><b>${template.approvalMode === 'always' ? 'всегда' : 'опасные'}</b></span></div><div class="create-preview-tools">${chips || '<em>Tools не выбраны</em>'}</div>${(template.goals || []).length ? `<ul>${template.goals.slice(0, 3).map(goal => `<li>${esc(goal)}</li>`).join('')}</ul>` : ''}<button type="button" class="primary" data-action="use-template" data-template="${esc(template.id)}">Подключить к проекту →</button>${blueprintById(template.id) ? `<button type="button" class="secondary" data-action="delete-blueprint" data-id="${esc(template.id)}">Удалить профиль</button>` : ''}</aside>`
  }
  function templatePickerHtml(templates, { compact = false, interactive = false } = {}) {
    if (!templates?.length) return ''
    if (!interactive) {
      return `<section class="template-picker ${compact ? 'compact' : ''}">${compact ? '' : `<header><strong>Шаг 0 · Выберите основного специалиста</strong><span>${templates.length}</span></header><p>Основной профиль переносится между проектами и хранит роль, навыки, умения, инструкции и права. Здесь создаётся только его проектная адаптация.</p>`}<div>${templates.map(template => `<button type="button" data-action="use-template" data-template="${esc(template.id)}"><b>${esc(template.name)}</b><small>${esc(template.description)}</small><em>${countOf((template.allowedTools || []).length, 'tool', 'tools', 'tools')} · ${countOf(template.maxSteps || 30, 'шаг', 'шага', 'шагов')}</em></button>`).join('')}</div></section>`
    }
    const selectedId = ui.hirePreviewTemplateId || templates[0]?.id || ''
    if (!ui.hirePreviewTemplateId && templates[0]) ui.hirePreviewTemplateId = templates[0].id
    const preview = templates.find(item => item.id === selectedId) || templates[0]
    return `<section class="create-class-picker template-picker"><header><div><span class="quest-label">КОМАНДА / ПРОФИЛЬ</span><strong>Выберите постоянного специалиста</strong><p>Профиль задаёт переносимую основу агента. На следующих шагах вы адаптируете её к текущему проекту.</p></div><em>${templates.length}</em></header><div class="create-class-layout"><div class="create-class-grid">${templates.map(template => {
      const on = template.id === (ui.hirePreviewTemplateId || preview?.id)
      const toolCount = (template.allowedTools || []).length
      return `<button type="button" class="create-class-card ${on ? 'selected' : ''}" data-action="preview-template" data-template="${esc(template.id)}"><span>✦</span><div><b>${esc(template.name)}</b><small>${esc(template.description)}</small><em>${countOf(toolCount, 'умение', 'умения', 'умений')} · ${countOf(template.maxSteps || 30, 'ход', 'хода', 'ходов')}</em></div></button>`
    }).join('')}</div>${templateClassPreview(preview)}</div></section>`
  }
  function hireLiveChecklist(profile) {
    const readiness = profileReadiness(profile)
    const checks = [
      { ok: Boolean((profile?.name || '').trim()), label: 'Имя' },
      { ok: Boolean((profile?.model || '').trim()), label: 'Модель' },
      { ok: readiness.toolCount > 0, label: 'Умения' },
      { ok: !readiness.canWrite || readiness.hasVerifier, label: 'Доказательство' },
      { ok: Number(profile?.maxSteps || 0) >= 1, label: 'Лимит' },
    ]
    const capabilityBlock = agentCapabilityHtml(profile)
    const done = checks.filter(item => item.ok).length
    return `<aside class="create-live-check ${readiness.ready ? 'ready' : ''}"><header><strong>Готовность</strong><span>${done}/${checks.length}</span></header><div class="create-live-rail"><i style="width:${Math.round(done / checks.length * 100)}%"></i></div><ul>${checks.map(item => `<li class="${item.ok ? 'ok' : ''}"><i></i>${esc(item.label)}</li>`).join('')}</ul>${readiness.unknown ? '<p>Ядро не ответило на проверку — готовность неизвестна.</p>' : readiness.ready ? '<p>Можно нанимать и сразу открыть квест.</p>' : `<p>${esc(readiness.issues[0] || 'Заполните шаги слева')}</p>`}</aside>${capabilityBlock}`
  }
  function hireSummaryPanel(profile, readiness) {
    const preset = providerPreset(profile)
    const tools = (profile?.allowedTools || []).map(toolName)
    return `<section class="create-hire-summary"><header><span class="quest-label">СВОДКА ПЕРЕД НАЙМОМ</span><strong>${esc(profile?.name || 'Новый агент')}</strong><small>${esc(agentClass(profile))} · ${esc(preset?.name || profile?.provider || 'провайдер')}</small></header><div class="create-summary-grid"><span><small>МОДЕЛЬ</small><b>${esc(profile?.model || '—')}</b></span><span><small>ХОДЫ</small><b>${Number(profile?.maxSteps) > 0 ? esc(profile.maxSteps) : '—'}</b></span><span><small>УМЕНИЯ</small><b>${tools.length}</b></span><span><small>PROOF</small><b>${readiness.hasVerifier ? 'да' : 'нет'}</b></span></div><div class="create-summary-tools">${tools.slice(0, 8).map(tool => `<span>${esc(tool)}</span>`).join('') || '<em>Умения не выбраны</em>'}</div>${readiness.ready ? '<p class="ok">Карточка готова — наймите и отправьте в квест или откройте в ростере.</p>' : `<p class="warn">${esc(readiness.issues.join(' · '))}</p>`}</section>`
  }
  function readinessBanner(profile, { editAction = 'edit-roster-profile', showFixes = true } = {}) {
    const readiness = profileReadiness(profile)
    if (readiness.unknown) return `<aside class="readiness-banner"><span>?</span><div><strong>Готовность неизвестна</strong><small>Ядро не ответило на проверку — «готов» здесь было бы сказано за него. Запуск всё равно проверяется ядром.</small></div><button type="button" class="secondary" data-action="${esc(editAction)}">Открыть карточку</button></aside>`
    if (readiness.ready) return `<aside class="readiness-banner ready"><span>✓</span><div><strong>Персонаж готов к квесту</strong><small>Модель, умения и лимит ходов в порядке.</small></div>${editAction === 'start-roster-quest' ? '<button type="button" class="secondary" data-action="start-roster-quest">К квесту →</button>' : ''}</aside>`
    const fixes = showFixes
      ? `<div class="readiness-fixes">${readiness.blockers.map(blocker => `<button type="button" class="secondary" data-action="fix-profile-step" data-step="${esc(blockerStep(blocker))}">${esc(blocker.text)}</button>`).join('')}</div>`
      : `<button type="button" class="secondary" data-action="${esc(editAction)}">Исправить</button>`
    return `<aside class="readiness-banner"><span>!</span><div><strong>Карточка не готова к квесту</strong><small>${esc(readiness.issues.join(' · '))}</small></div>${fixes}</aside>`
  }
  function profileStepNav(activeStep, creating) {
    const stages = flowStages(creating)
    const index = Math.max(0, stages.findIndex(step => step.id === activeStep))
    const progress = Math.round((index / Math.max(1, stages.length - 1)) * 100)
    const current = stages[index] || stages[0]
    return `<nav class="profile-step-nav create-flow-nav" aria-label="Шаги карточки персонажа"><div class="create-flow-progress"><span>ШАГ 0${index + 1} / 0${stages.length}</span><div class="create-live-rail"><i style="width:${progress}%"></i></div><small>${esc(current?.why || '')}</small></div><div class="create-flow-steps">${stages.map((step, i) => {
      const stateClass = step.id === activeStep ? 'on' : i < index ? 'done' : ''
      return `<button type="button" class="${stateClass}" data-action="profile-step" data-step="${esc(step.id)}"><em>${i < index ? '✓' : `0${i + 1}`}</em><span><b>${esc(step.label)}</b><small>${esc(step.hint)}</small></span></button>`
    }).join('')}</div>${creating ? '<p>Интерактивный найм: класс → личность → модель → умения → лимиты → сводка.</p>' : '<p>Правите только нужный шаг — сохранить можно с любого.</p>'}${ui.createStepError ? `<p class="create-step-error">${esc(ui.createStepError)}</p>` : ''}</nav>`
  }
  function profileStepFooter(activeStep, creating, readiness) {
    const stages = flowStages(creating)
    const index = Math.max(0, stages.findIndex(step => step.id === activeStep))
    const prev = stages[index - 1]
    const next = stages[index + 1]
    const onClass = creating && activeStep === 'class'
    const onLimits = activeStep === 'limits'
    const left = prev
      ? `<button type="button" class="secondary" data-action="advance-profile-step" data-step="${esc(prev.id)}" data-dir="back">← ${esc(prev.label)}</button>`
      : creating
        ? '<button type="button" class="secondary" data-action="cancel-profile">Отмена</button>'
        : ''
    let right = ''
    if (onClass) {
      right = `<button type="button" class="secondary" data-action="skip-class-step">Пустая карточка →</button><button type="button" class="primary" data-action="use-template" data-template="${esc(ui.hirePreviewTemplateId || ui.state.boot?.profileTemplates?.[0]?.id || '')}" ${!(ui.hirePreviewTemplateId || ui.state.boot?.profileTemplates?.[0]) ? 'disabled' : ''}>Взять класс →</button>`
    } else if (next) {
      right = `<button type="button" class="secondary" data-action="advance-profile-step" data-step="${esc(next.id)}" data-dir="next">${esc(next.label)} →</button>${creating ? '' : `<button class="primary save" type="submit">Сохранить карточку</button>`}`
    } else if (creating) {
      right = readiness.ready
        ? `<button class="primary save" type="submit" data-hire-intent="card">Нанять и открыть карточку</button><button type="button" class="primary create-quest-cta" data-action="hire-and-quest">Нанять и к квесту →</button>`
        : `<button class="primary save" type="submit" data-hire-intent="card">Нанять персонажа</button>`
    } else {
      right = `<button class="primary save" type="submit">Сохранить карточку</button>${readiness.ready ? '<button type="button" class="secondary" data-action="start-roster-quest">К квесту →</button>' : ''}`
    }
    return `<div class="profile-step-footer create-flow-footer"><div class="profile-step-left">${left}</div><div class="profile-step-right">${right}</div></div>`
  }
  function pendingDecisionsBanner(details) {
    if (!details) return ''
    const pendingApprovals = (details.approvals || []).filter(item => item.status === 'pending')
    if (!pendingApprovals.length) return ''
    const patchCount = pendingApprovals.filter(item => item.toolName === 'propose_patch').length
    const commandCount = pendingApprovals.length - patchCount
    const parts = [patchCount ? `${patchCount} diff` : '', commandCount ? `${commandCount} команд` : ''].filter(Boolean)
    return `<aside class="pending-decisions"><span>!</span><div><strong>Нужно ваше решение</strong><small>${esc(parts.join(' · ') || `${pendingApprovals.length} запросов`)}</small></div><a href="#pending-decision">К решению ↓</a></aside>`
  }
  function patchStatusLabel(value) {
    return ({ proposed: 'ЖДЁТ РЕШЕНИЯ', applied: 'ПРИМЕНЁН', rejected: 'ОТКЛОНЁН', reverted: 'ОТКАЧЕН', pending: 'ЖДЁТ РЕШЕНИЯ' })[value] || value
  }
  function questPayload() {
    return {
      task: ui.taskDraft.trim(),
      goal: ui.questGoalDraft.trim(),
      acceptanceCriteria: lines(ui.questCriteriaDraft),
      constraints: lines(ui.questConstraintsDraft),
    }
  }
  function composeQuestTask() {
    const payload = questPayload()
    return [`ЗАДАЧА:\n${payload.task}`, payload.goal?`ЦЕЛЬ:\n${payload.goal}`:'', payload.acceptanceCriteria.length?`КРИТЕРИИ ГОТОВНОСТИ:\n- ${payload.acceptanceCriteria.join('\n- ')}`:'', payload.constraints.length?`ОГРАНИЧЕНИЯ:\n- ${payload.constraints.join('\n- ')}`:''].filter(Boolean).join('\n\n')
  }
  function formatTime(value) { return value ? new Date(value).toLocaleTimeString('ru-RU',{hour:'2-digit',minute:'2-digit'}) : '' }
  function status(status, label = '') { return `<span class="status status-${esc(status)}"><i></i>${esc(label || statusLabels[status] || status)}</span>` }
  function agentClass(profile) {
    const role = `${profile?.name || ''} ${profile?.roleDescription || ''}`.toLowerCase()
    if (/ревью|review|безопас|security/.test(role)) return 'Хранитель кода'
    if (/архит|план|design|architect/.test(role)) return 'Архитектор систем'
    if (/тест|qa|debug|ошиб/.test(role)) return 'Следопыт ошибок'
    if (/исслед|документ|research|docs/.test(role)) return 'Летописец знаний'
    if (/данн|sql|database|баз/.test(role)) return 'Хранитель данных'
    return 'Мастер кода'
  }
  function agentProgress(profile) {
    const hubAgent = agentById(profile?.id) || profile
    if (typeof hubAgent?.experience === 'number' || typeof hubAgent?.tasksCompleted === 'number') {
      const experience = Number(hubAgent.experience || 0)
      const level = Math.max(1, Number(hubAgent.level || (1 + Math.floor(experience / 100))))
      const completed = Number(hubAgent.successCount || 0)
      const tasks = Number(hubAgent.tasksCompleted || 0)
      const reliability = tasks ? Math.round((completed / tasks) * 100) : undefined
      const xpIntoLevel = experience % 100
      return {
        quests: tasks,
        completed,
        reliability,
        toolMastery: undefined,
        level,
        xp: xpIntoLevel,
        xpTarget: 100,
      }
    }
    const runs = (ui.state.boot?.runs || []).filter(run => run.profileId === profile?.id)
    const diagnosticsByRun = new Map((ui.state.boot?.runDiagnostics || []).map(item => [item.runId, item]))
    const finished = runs.filter(run => ['completed','failed','cancelled','interrupted'].includes(run.status))
    const verified = finished.filter(run => {
      if (run.status !== 'completed') return false
      const diagnostics = diagnosticsByRun.get(run.id)
      if (!diagnostics) return true
      const signals = Array.isArray(diagnostics.signals) ? diagnostics.signals : []
      if (signals.some(signal => signal.code === 'verification_missing' || signal.code === 'completion_evidence_missing')) return false
      if (diagnostics.health === 'failed') return false
      return true
    })
    const completed = verified.length
    const reliability = finished.length ? Math.round(finished.filter(run => run.status === 'completed').length / finished.length * 100) : undefined
    const toolMastery = (() => {
      const items = (ui.state.boot?.runDiagnostics || []).filter(item => runs.some(run => run.id === item.runId))
      let succeeded = 0
      let total = 0
      for (const item of items) {
        succeeded += Number(item.tools?.succeeded || 0)
        total += Number(item.tools?.calls || 0)
      }
      return total ? Math.round(succeeded / total * 100) : undefined
    })()
    return {
      quests: runs.length,
      completed,
      reliability,
      toolMastery,
      level: 1 + Math.floor(completed / 3),
      xp: completed % 3,
      xpTarget: 3,
    }
  }
  function questLevelUpBadge(run) {
    if (!run?.profileId || run.status !== 'completed') return ''
    const profile = (ui.state.boot?.profiles || []).find(item => item.id === run.profileId)
    if (!profile) return ''
    const progress = agentProgress(profile)
    if (progress.xp !== 0 || progress.completed === 0) return ''
    return `<aside class="level-up-badge" role="status"><span>▲</span><div><strong>Уровень ${progress.level}</strong><small>Только подтверждённые квесты дают опыт персонажа.</small></div></aside>`
  }
  /** Shared allowlist overlap — factual synergy, not invented stats. */
  function agentSynergy(profile, profiles) {
    const mine = new Set(profile?.allowedTools || [])
    if (!mine.size) return []
    return (profiles || [])
      .filter(item => item.id !== profile?.id)
      .map(item => {
        const theirs = item.allowedTools || []
        const shared = theirs.filter(tool => mine.has(tool))
        const denom = Math.min(mine.size, Math.max(1, theirs.length))
        return {
          id: item.id,
          name: item.name,
          className: agentClass(item),
          shared: shared.length,
          score: Math.round(shared.length / denom * 100),
        }
      })
      .filter(item => item.shared > 0)
      .sort((a, b) => b.score - a.score || b.shared - a.shared)
      .slice(0, 3)
  }
  // Класс персонажа стоит в шапке карточки, под именем, и только там. В полосе
  // показателей он тоже был — третьей ячейкой рядом с «КВЕСТЫ» и «ЛИМИТ ХОДОВ», —
  // и это был не случайный повтор: обе надписи брались из одного `agentClass()`
  // и совпадали всегда. Полоса меряет квесты и ходы, класс не измеряется; вдобавок
  // в ячейке он обрезался многоточием, а в шапке помещался целиком.
  function agentCharacterCard(profile) {
    const progress = agentProgress(profile)
    const abilities = (profile?.allowedTools || []).slice(0, 4)
    const reliability = progress.reliability === undefined ? '—' : `${progress.reliability}%`
    return `<section class="character-card"><header><div class="character-avatar" aria-hidden="true"><span>✦</span></div><div><small>КАРТОЧКА ПЕРСОНАЖА</small><strong>${esc(profile?.name || 'Новый агент')}</strong><p>${esc(agentClass(profile))}</p></div><em>УР ${progress.level}</em></header><div class="character-bars"><label><span>ОПЫТ · ${progress.completed} завершено</span><b>${progress.xp} / ${progress.xpTarget}</b><progress value="${progress.xp}" max="${progress.xpTarget}"></progress></label><label><span>НАДЁЖНОСТЬ</span><b>${reliability}</b><progress class="vital" value="${progress.reliability || 0}" max="100"></progress></label></div><div class="character-facts"><span><small>КВЕСТЫ</small><b>${progress.quests}</b></span><span><small>ЛИМИТ ХОДОВ</small><b>${esc(profile?.maxSteps || '—')}</b></span></div><div class="ability-chips">${abilities.map(tool=>`<span>${esc(toolName(tool))}</span>`).join('') || '<span class="muted">Умения не выбраны</span>'}</div><footer>Уровень и надёжность рассчитаны по фактическим квестам этого профиля.</footer></section>`
  }
  function activeQuestCard(details) {
    if (!details?.run) return ''
    const run = details.run
    const snapshotProfile = run.configurationSnapshot?.profile
    const profile = snapshotProfile || (ui.state.boot?.profiles || []).find(item=>item.id===run.profileId)
    const execution = (ui.state.boot?.executions || []).find(item => item.runId === run.id)
    const quest = (ui.state.boot?.quests || []).find(item => item.id === execution?.questId)
    const flow = (ui.state.boot?.flows || []).find(item => item.id === quest?.flowId)
    const flowNode = (flow?.nodes || []).find(item => item.id === execution?.flowNodeId)
    const maxSteps = Math.max(1, Number(snapshotProfile?.maxSteps || profile?.maxSteps || run.step || 1))
    const terminal = ['completed','failed','cancelled','interrupted'].includes(run.status)
    const progress = run.status === 'completed' ? 100 : Math.min(96, Math.round(Number(run.step || 0) / maxSteps * 100))
    const changed = run.changedFiles || []
    const runControls = execControlsHtml(execution
      ? { ...execution, status: run.status, runId: run.id }
      : { id: run.id, runId: run.id, status: run.status, legacy: true })
    const trace = execution
      ? `<div class="quest-trace"><small>TRACE</small><span>Execution · ${esc(execution.id)}</span>${quest ? `<span>Quest · ${esc(quest.title || quest.id)}</span>` : ''}${execution.flowRunId ? `<span>Flow · ${esc(flow?.name || execution.flowRunId)}</span>` : ''}${execution.flowNodeId ? `<span>Node · ${esc(flowNode?.name || execution.flowNodeId)}</span>` : ''}</div>`
      : ''
    return `<section class="active-quest"><header><span>АКТИВНЫЙ КВЕСТ</span>${status(run.status)}</header><h3>${esc(run.task)}</h3><div class="quest-agent"><span>✦</span><div><small>ПЕРСОНАЖ</small><strong>${esc(profile?.name || run.profileId)} · ${esc(agentClass(profile))}</strong></div></div>${trace}<div class="quest-progress"><label><span>ПРОГРЕСС</span><b>${progress}%</b></label><progress value="${progress}" max="100"></progress><small>ход ${esc(run.step)} из лимита ${maxSteps}</small></div>${runControls}${changed.length?`<div class="quest-artifacts"><small>ЗАТРОНУТЫЕ АРТЕФАКТЫ</small>${changed.slice(0,5).map(path=>`<span>◇ ${esc(path)}</span>`).join('')}</div>`:''}${terminal?`<footer><small>НАГРАДА</small><strong>${run.status==='completed'?'Результат зафиксирован':'Опыт сохранён'} · ${countOf(changed.length, 'файл', 'файла', 'файлов')}</strong></footer>`:''}</section>`
  }
  function formatBytes(value) {
    const bytes = Number(value || 0)
    if (bytes < 1024) return `${bytes} Б`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} КиБ`
    return `${(bytes / 1024 / 1024).toFixed(1)} МиБ`
  }
  function formatDuration(value) {
    const milliseconds = Math.max(0, Number(value || 0))
    if (milliseconds < 1000) return `${Math.round(milliseconds)} мс`
    if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 1 : 0)} с`
    const minutes = Math.floor(milliseconds / 60_000)
    const seconds = Math.round((milliseconds % 60_000) / 1000)
    return `${minutes} мин ${seconds} с`
  }
  function diagnosticSignalText(signal) {
    const value = Number(signal?.value || 0)
    if (signal?.code === 'blind_edit_prevented') return `Point остановил правку без предварительного чтения файла: ${value}.`
    if (signal?.code === 'edit_scope_prevented') return `Point остановил правку по фрагменту, который агент не видел: ${value}.`
    if (signal?.code === 'stale_edit_prevented') return `Point остановил правку по устаревшему содержимому файла: ${value}.`
    if (signal?.code === 'provider_retried') return `Провайдер временно не ответил; безопасных повторов: ${value}.`
    if (signal?.code === 'agent_stalled') return 'Агент повторял одинаковый план инструментов и был безопасно остановлен.'
    if (signal?.code === 'context_compacted') return `Оперативная память освобождена без удаления хроники: ≈ ${value.toLocaleString('ru-RU')} токенов.`
    if (signal?.code === 'retrieval_truncated') return `Неполных поисковых выдач: ${value}. Агенту нужно уточнить символ или путь.`
    if (signal?.code === 'completion_revised') return `Финал был исправлен после локальной проверки доказательств (${value}).`
    if (signal?.code === 'completion_evidence_missing') return 'Агент не подтвердил готовность реальным успешным результатом проверки.'
    if (signal?.code === 'completion_revision_requested') return 'Локальная проверка запросила у агента подтверждение готовности.'
    const labels = {
      run_completed:'Запуск завершён штатно.', run_failed:'Запуск завершился ошибкой.', run_cancelled:'Запуск остановлен пользователем.', run_interrupted:'Запуск был прерван.',
      tool_failures:`Ошибок инструментов: ${value}.`, approval_denied:`Отклонённых подтверждений: ${value}.`, approval_pending:`Ожидают решения: ${value}.`,
      approval_wait_long:`Самое долгое ожидание решения: ${formatDuration(value)}.`, tokens_unreported:'Провайдер не сообщил использование токенов.', model_requests_unanswered:`Запросов к модели без зафиксированного ответа: ${value}.`,
      verification_recorded:'После изменений зафиксирован успешный запуск команды проверки.', verification_missing:'Файлы изменены, но успешная команда проверки в журнале не зафиксирована.',
      workspace_changes_captured:`В историю безопасного отката записано изменений: ${value}.`, workspace_changes_non_revertible:`Изменений без точного отката: ${value} — бинарные, слишком большие или чувствительные файлы.`,
      workspace_history_limit_reached:`Лимит истории достигнут; точный откат недоступен для ${value} изменений.`, workspace_audit_incomplete:`Неполных снимков рабочей области: ${value}. Проверьте перечисленные файлы вручную.`,
    }
    return labels[signal?.code] || signal?.code || 'Сигнал диагностики'
  }
  function guardrailEventText(payload) {
    if (payload.code === 'agent_stalled') return 'повторяющийся цикл остановлен'
    if (payload.code === 'tool_plan_recovery') return 'повторяющийся план прерван · агенту дана одна попытка сменить подход'
    if (payload.code === 'duplicate_tool_plan') return payload.canRetry ? 'тот же план инструментов повторён · нужна смена аргументов или инструмента' : 'повторный выполненный вызов заблокирован'
    if (payload.code === 'inspection_required') return `правка ${payload.path || 'файла'} остановлена · сначала ${payload.requiredTool || 'нужно изучить файл'}`
    if (payload.code === 'inspection_scope_required') return `правка ${payload.path || 'файла'} остановлена · нужно найти недостающий фрагмент через ${payload.requiredTool || 'search_code'}`
    if (payload.code === 'inspection_stale') return `файл ${payload.path || ''} изменился после чтения · нужно перечитать`
    return 'повторный выполненный вызов заблокирован'
  }
  
  // Причина неудачи прогона приходит из ядра по-английски и показывалась как
  // есть — на самом читаемом экране, «что случилось с моей задачей». Отказы
  // инструментов рядом давно переводятся по коду (toolFailureText), а эта строка
  // оставалась сырой: «agent final answer rejected by completion gate…».
  //
  // Тот же список живёт в extension.js для красной полосы; он там и появился
  // раньше. Две копии не разойдутся молча: их совпадение проверяет сверка
  // договорённостей (ui/contracts.mjs).
  const CORE_FAILURE_HINTS = [
    [/completion gate/i, 'Агент объявил работу законченной, но не подтвердил её проверкой — ядро не приняло финал.'],
    [/daily hub budget exceeded/i, 'Дневной лимит расходов исчерпан. Новые запуски остановлены, пока лимит не обновится или не будет снят жёсткий стоп в «Бюджете проекта».'],
    [/monthly hub budget exceeded/i, 'Месячный лимит расходов исчерпан. Новые запуски остановлены, пока лимит не обновится или не будет снят жёсткий стоп в «Бюджете проекта».'],
    [/quest cost budget exceeded/i, 'Бюджет квеста исчерпан: запуск остановлен, чтобы не тратить сверх заданного.'],
    [/context deadline exceeded|deadline exceeded/i, 'Ядро не дождалось ответа модели — истекло отведённое время.'],
    [/workspace is not open/i, 'Проект не открыт: откройте папку проекта, чтобы ядро могло работать.'],
    [/resource belongs to another project world/i, 'Эта запись принадлежит другому проекту.'],
  ]
  
  // Незнакомую причину не глотаем: показываем как есть, иначе человек останется
  // без единого следа того, что произошло.
  function coreFailureText(raw) {
    const text = String(raw || '').trim()
    if (!text) return ''
    for (const [pattern, russian] of CORE_FAILURE_HINTS) {
      if (pattern.test(text)) return russian
    }
    return text
  }
  
  function toolFailureText(tool, error) {
    const code = error?.code || ''
    const hint = String(error?.hint || '').trim()
    let text = ''
    if (code === 'edit_anchor_missing') text = 'Точный фрагмент больше не найден · агенту нужно свериться с прочитанным кодом'
    else if (code === 'edit_anchor_ambiguous') text = 'Фрагмент встречается несколько раз · агенту нужно добавить уникальный контекст'
    else if (code === 'overlapping_edits') text = 'Точечные правки пересекаются · агенту нужно объединить их в одну замену'
    else if (code === 'edit_target_missing') text = 'Для нового файла нужен полный текст, а не точечная замена'
    else if (code === 'no_changes') text = 'Предложение не меняет файл · подтверждение не запрашивалось'
    else if (code === 'command_denied') text = 'Команда отклонена списком запретов · используйте узкий верификатор (тест/сборка/линт), а не разрушительную или exfil-команду'
    else if (code === 'network_denied') text = 'Сеть закрыта политикой агента · DENY не обходится кастомной командой или процессом'
    else if (code === 'duplicate_tool_call') text = 'Повтор уже успешного вызова заблокирован · используйте прежний результат или смените действие'
    else if (code === 'invalid_input') text = `Некорректные аргументы инструмента ${toolName(tool)}`
    else text = `${toolName(tool)} · ${error?.message || code || 'неизвестная ошибка'}`
    // Подсказка инструмента пишется в одно поле для двух адресатов. Ядру она
    // нужна как указание модели, что сделать иначе («pass a single JSON object
    // whose keys match the tool schema»), и таких большинство. Человеку же
    // адресованы другие: «сохраните профиль в Гильдии → Базы данных».
    //
    // Показывали обе. В ленте прогона человек читал инструкции агенту — на чужом
    // языке и о том, чего он всё равно не делает.
    //
    // Разделяем по языку. Это признак наблюдаемый, а не гарантированный: в ядре
    // сегодня 13 подсказок человеку — все по-русски, и 22 модели — все
    // по-английски. Соглашение закреплено сверкой договорённостей; если оно
    // однажды перестанет держаться, лучше завести в ядре отдельное поле.
    if (hint && /[а-яё]/i.test(hint) && !text.includes(hint)) text += ` · ${hint}`
    return text
  }
  
  function diagnosticsCard(diagnostics) {
    if (!diagnostics?.schemaVersion) return ''
    const model = diagnostics.model || {}
    const tools = diagnostics.tools || {}
    const approvals = diagnostics.approvals || {}
    const patches = diagnostics.patches || {}
    const context = diagnostics.context || {}
    const retrieval = diagnostics.retrieval || {}
    const workspace = diagnostics.workspace || {}
    const tokenValue = model.usageReported ? Number(model.totalTokens || 0).toLocaleString('ru-RU') : 'нет данных'
    const items = Array.isArray(tools.items) ? tools.items : []
    const signals = Array.isArray(diagnostics.signals) ? diagnostics.signals : []
    const breakdown = items.length ? `<details><summary>По инструментам · ${items.length}</summary><div class="diagnostic-tools">${items.map(item=>`<span><b>${esc(toolName(item.name))}</b><small>${esc(item.succeeded||0)} успешно · ${esc(countOf(item.failed||0, 'ошибка', 'ошибки', 'ошибок'))} · ${formatDuration(item.durationMs)}</small></span>`).join('')}</div></details>` : ''
    return `<section class="run-diagnostics health-${esc(diagnostics.health)}"><header><div><strong>Свиток состояния</strong><small>Факты из неизменяемой хроники квеста</small></div><span>${esc(healthLabels[diagnostics.health] || diagnostics.health)}</span></header><div class="diagnostic-grid"><span><small>ВРЕМЯ</small><b>${formatDuration(diagnostics.durationMs)}</b></span><span><small>МОДЕЛЬ</small><b>${esc(model.requests||0)} запр. · ${formatDuration(model.averageLatencyMs)}</b></span><span><small>МАНА / ТОКЕНЫ</small><b>${esc(tokenValue)}</b></span><span><small>ПАМЯТЬ</small><b>${esc(context.compactions||0)} сжат. · ${Number(context.releasedTokens||0).toLocaleString('ru-RU')}</b></span><span><small>РАЗВЕДКА</small><b>${esc(retrieval.searches||0)} поиск. · ${esc(retrieval.relatedFiles||0)} связ.</b></span><span><small>УМЕНИЯ</small><b>${esc(tools.succeeded||0)} ✓ · ${esc(tools.failed||0)} !</b></span><span><small>РЕШЕНИЯ</small><b>${esc(approvals.allowed||0)} ✓ · ${esc(approvals.denied||0)} ×</b></span><span><small>ОЖИДАНИЕ</small><b>${formatDuration(approvals.waitMs)}</b></span><span><small>АРТЕФАКТЫ</small><b>${esc(patches.applied||0)} / ${esc(patches.proposed||0)}</b></span><span><small>ЖУРНАЛ КОМАНД</small><b>${esc(workspace.recordedChanges||0)} ↶ · ${esc(workspace.nonRevertibleChanges||0)} !</b></span><span><small>ФИНАЛ</small><b>${esc(stopReasonLabels[diagnostics.stopReason] || diagnostics.stopReason)}</b></span></div>${breakdown}${signals.length?`<div class="diagnostic-signals">${signals.map(signal=>`<p class="signal-${esc(signal.severity)}">${signal.severity==='success'?'✓':signal.severity==='error'?'!':signal.severity==='warning'?'△':'i'} ${esc(diagnosticSignalText(signal))}</p>`).join('')}</div>`:''}<footer>Показатели рассчитаны только по реальным событиям локального ядра.</footer></section>`
  }
  function runComparison(runs, diagnosticsByRun) {
    const comparable = runs.filter(run=>diagnosticsByRun.has(run.id) && runIsFinished(run)).slice(0,2)
    if (comparable.length < 2) return ''
    const latest = diagnosticsByRun.get(comparable[0].id)
    const previous = diagnosticsByRun.get(comparable[1].id)
    const tokens = item => item.model?.usageReported ? Number(item.model.totalTokens || 0).toLocaleString('ru-RU') : '—'
    const cells = [
      ['Длительность', formatDuration(previous.durationMs), formatDuration(latest.durationMs)],
      ['Токены', tokens(previous), tokens(latest)],
      ['Ошибки инструментов', previous.tools?.failed || 0, latest.tools?.failed || 0],
      ['Ожидание', formatDuration(previous.approvals?.waitMs), formatDuration(latest.approvals?.waitMs)],
    ]
    return `<section class="run-comparison"><header><div><strong>Две последние главы</strong><small>Сравнение фактических затрат и сбоев</small></div><span>раньше → сейчас</span></header><div><b>Показатель</b><b title="${esc(comparable[1].task)}">${esc(comparable[1].task)}</b><b title="${esc(comparable[0].task)}">${esc(comparable[0].task)}</b>${cells.map(row=>`<span>${esc(row[0])}</span><span>${esc(row[1])}</span><span>${esc(row[2])}</span>`).join('')}</div></section>`
  }
  function contextKindLabel(item) {
    if (item.kind === 'image') return `Изображение${item.width && item.height ? ` ${item.width}×${item.height}` : ''}`
    if (item.kind === 'table') return 'Таблица'
    if (item.kind === 'document') return 'Документ'
    return item.kind === 'text' ? 'Текст' : 'Файл'
  }
  function invalidateAgentRunPreview() {
    ui.agentRunPreview=undefined
    ui.agentRunPreviewStatus='idle'
    ui.agentRunPreviewError=''
    root.querySelector('.agent-preflight')?.remove()
  }
  function requestContextPreview() {
    invalidateAgentRunPreview()
    ui.contextPreview = undefined
    ui.contextPreviewError = ''
    if (!ui.contextItems.length) { ui.contextPreviewStatus = 'idle'; render(); return }
    ui.contextPreviewStatus = 'loading'
    render()
    vscode.postMessage({type:'previewContext', contextItems: ui.contextItems})
  }
  function contextPreviewMarkup() {
    if (!ui.contextItems.length) return ''
    if (ui.contextPreviewStatus === 'loading') return '<div class="context-preview loading-inline">Анализируем вложения…</div>'
    if (ui.contextPreviewError) return `<div class="context-preview context-preview-error"><strong>Контекст не готов</strong><span>${esc(ui.contextPreviewError)}</span></div>`
    if (!ui.contextPreview) return ''
    const items = Array.isArray(ui.contextPreview.items) ? ui.contextPreview.items : []
    return `<div class="context-preview"><header><strong>Контекст готов</strong><span>≈ ${esc(ui.contextPreview.estimatedTokens || 0)} токенов · ${formatBytes(ui.contextPreview.totalContextBytes)} текста${ui.contextPreview.totalImageBytes ? ` · ${formatBytes(ui.contextPreview.totalImageBytes)} изображений` : ''}</span></header><div>${items.map(item=>`<span title="${esc(item.digest || '')}"><b>${esc((item.format || contextKindLabel(item)).toUpperCase())}</b>${esc(item.label)}<small>${esc(contextKindLabel(item))} · ${formatBytes(item.sourceSize || item.size)}${item.extractedSize ? ` → ${formatBytes(item.extractedSize)}` : ''}${item.truncated ? ' · сокращено' : ''}</small></span>`).join('')}</div>${(ui.contextPreview.warnings||[]).map(warning=>`<p>⚠ ${esc(warning)}</p>`).join('')}</div>`
  }
  
  function hubNavLocked() {
    return Boolean(ui.state.selectedTab === 'onboarding' && isSystemOnboardingStep(ui.onboardingStep))
  }
  function hubSituation() {
    const mergeConflicts = (ui.state.boot?.flowRuns || []).reduce((total, run) => total + Object.values(run.nodeStates || {}).filter(nodeState => nodeState?.output?.waitReason === 'sandbox_merge_conflict').length, 0)
    const flowApprovals = (ui.state.boot?.flowRuns || []).reduce((total, run) => total + Object.values(run.nodeStates || {}).filter(nodeState => nodeState?.status === 'waiting_approval' && nodeState?.output?.waitReason !== 'sandbox_merge_conflict').length, 0)
    const pending = pendingChangeSets()
    const executions = activeExecutions()
    const quest = currentHubQuest()
    const reviews = pendingQuestProposals().length + pendingActionProposals().length
    if (mergeConflicts) return { tone: 'alert', label: 'Конфликт веток', detail: `${countOf(mergeConflicts, 'узел', 'узла', 'узлов')} ${plural(mergeConflicts, 'ждёт', 'ждут', 'ждут')} выбора итоговых файлов`, tab: 'flows', action: 'Объединить →' }
    if (flowApprovals) return { tone: 'alert', label: 'Нужно решение', detail: `${countOf(flowApprovals, 'этап ждёт', 'этапа ждут', 'этапов ждут')} вашего решения`, tab: 'flows', action: 'Решить →' }
    if (pending.length) return { tone: 'alert', label: 'Ревью изменений', detail: `${countOf(pending.length, 'набор ждёт', 'набора ждут', 'наборов ждут')} применения`, tab: 'changesets', action: 'Открыть наборы →' }
    if (reviews) return { tone: 'alert', label: 'Ревью компаньона', detail: `${countOf(reviews, 'предложение', 'предложения', 'предложений')} ${plural(reviews, 'ждёт', 'ждут', 'ждут')} Start / Ignore`, tab: 'overview', action: 'К ревью →' }
    if (executions.some(item => item.status === 'waiting_approval')) return { tone: 'alert', label: 'Подтвердите умение', detail: 'Запуск ждёт вашего решения', tab: 'quests', action: 'К квесту →' }
    if (executions.some(item => item.status === 'pending')) return { tone: 'next', label: 'Всё готово к запуску', detail: `${countOf(executions.filter(item => item.status === 'pending').length, 'агент готов', 'агента готовы', 'агентов готовы')} начать работу`, tab: 'overview', action: '' }
    if (executions.length) return { tone: 'live', label: 'Идёт работа', detail: `${countOf(executions.length, 'активный запуск', 'активных запуска', 'активных запусков')}`, tab: 'quests', action: 'Следить →' }
    if (quest) return { tone: 'ready', label: compactQuestTitle(quest.title), detail: questStatusLabels[quest.status] || quest.status || 'квест', tab: 'quests', action: 'К брифингу →' }
    if (!hubAgents().length) return { tone: 'next', label: 'Соберите гильдию', detail: 'Сначала агент, потом квест', tab: 'agents', action: 'К ростеру →' }
    if (!ui.state.boot?.orchestrator?.id && !ui.onboardingDraft.orchestratorFinished) return { tone: 'next', label: 'Настройте, как мастер распоряжается', detail: 'Системный агент квестов ещё не выбран', tab: 'onboarding', action: 'К настройке →' }
    return { tone: 'idle', label: 'Гильдия готова', detail: 'Поставьте квест или спросите компаньона', tab: 'quests', action: 'Новый квест →' }
  }
  // Шапка и кнопка действия приходят из общей рамки: одна разметка на Git,
  // Запуск, Службы и Логи — они стоят в нижней панели одним рядом.
  const { toolCommandButton, toolWindowHeading, toolWindowEmpty } = toolWindowFrame
  const {
    gitFileKind,
    gitChanges,
    gitLists,
    gitCheckedPaths,
    gitGroupOf,
    gitGroups,
    gitGroupPaths,
    syncGitChecked,
    syncGitCommitButtons,
    syncGitTree,
    gitToolView,
  } = createGitViews({
    ui: {
      get checked() { return ui.gitChecked },
      set checked(value) { ui.gitChecked = value },
      get known() { return ui.gitKnown },
      set known(value) { ui.gitKnown = value },
      get collapsed() { return ui.gitCollapsed },
      set collapsed(value) { ui.gitCollapsed = value },
      get foldedOnce() { return ui.gitFoldedOnce },
      set foldedOnce(value) { ui.gitFoldedOnce = value },
      get selected() { return ui.gitSelected },
      set selected(value) { ui.gitSelected = value },
      get selectedStash() { return ui.gitSelectedStash },
      set selectedStash(value) { ui.gitSelectedStash = value },
      get pendingAction() { return ui.gitPendingAction },
      set pendingAction(value) { ui.gitPendingAction = value },
      get commitDraft() { return ui.gitCommitDraft },
      set commitDraft(value) { ui.gitCommitDraft = value },
      get menuFor() { return ui.gitMenuFor },
      set menuFor(value) { ui.gitMenuFor = value },
      get notice() { return ui.gitNotice },
      set notice(value) { ui.gitNotice = value },
      get target() { return ui.gitTarget },
      set target(value) { ui.gitTarget = value },
      get amend() { return ui.gitAmend },
      set amend(value) { ui.gitAmend = value },
      get flat() { return ui.gitFlat },
      set flat(value) { ui.gitFlat = value },
      get tab() { return ui.gitTab },
      set tab(value) { ui.gitTab = value },
    },
    getData: () => ui.toolWindowData.git || {},
    gitWide,
    shell,
    esc,
    countOf,
    toolCommandButton,
    toolWindowHeading,
    persistDraft,
    root,
  })
  function terminalToolView() {
    const data = ui.toolWindowData.terminal || {}
    const terminals = Array.isArray(data.terminals) ? data.terminals : []
    const cards = terminals.map(item => `<article class="point-terminal-card ${item.active ? 'active' : ''}"><i>›_</i><div><strong>${esc(item.name || 'Терминал')}</strong><small>${item.exitStatus == null ? (item.active ? 'активный' : 'открыт') : `завершён · ${esc(item.exitStatus)}`}</small></div></article>`).join('')
    return shell(`<main class="point-tool-page point-terminal-tool">
      ${toolWindowHeading('ТЕРМИНАЛ', 'Запуск и консоли', 'Shell, задачи проекта и конфигурации запуска.', `${terminals.length}`)}
      <div class="point-tool-actions primary-row">${toolCommandButton('localAgent.openTerminal', 'Новый терминал', 'primary')}${toolCommandButton('localAgent.runAnything', 'Run Anything')}${toolCommandButton('localAgent.newConsoleChannel', 'Новый канал')}</div>
      <section class="point-run-card"><span>ТЕКУЩАЯ ЦЕЛЬ</span><strong>${esc(data.run || 'Конфигурация не выбрана')}</strong>${data.failure ? `<p class="danger-copy">${esc(data.failure)}</p>` : '<p>Shift+F10 запускает текущую цель без отладчика.</p>'}<footer>${toolCommandButton('localAgent.selectRunConfiguration', 'Выбрать цель')}${toolCommandButton('localAgent.runWithoutDebug', 'Запустить', 'primary')}${toolCommandButton('localAgent.startDebug', 'Отладка')}</footer></section>
      <section class="point-tool-list"><header><strong>Консоли</strong><small>${terminals.length ? 'состояние IDE' : 'нет открытых'}</small></header>${cards || toolWindowEmpty('Консолей пока нет', 'Откройте терминал или запустите задачу.', '', true)}</section>
    </main>`)
  }
  function logsToolView() {
    const data = ui.toolWindowData.logs || {}
    const counts = data.counts || {}
    const source = Array.isArray(data.lines) ? data.lines : []
    const lines = source.filter(item => ui.toolLogFilter === 'all' || item.level === ui.toolLogFilter)
    const rows = lines.map(item => `<article class="point-log-row level-${esc(item.level)}"><span>${esc(item.time || '')}</span><b>${esc(item.level || 'info')}</b><em>${esc(item.source || 'core')}</em><p>${esc(item.message || '')}</p></article>`).join('')
    return shell(`<main class="point-tool-page point-logs-tool">
      ${toolWindowHeading('ЛОГИ', 'Диагностика Point', 'События сгруппированы по уровню и источнику.', `${source.length}`)}
      <section class="point-tool-metrics"><button data-action="set-log-filter" data-filter="error" class="danger"><small>ОШИБКИ</small><b>${Number(counts.error || 0)}</b></button><button data-action="set-log-filter" data-filter="warning"><small>ПРЕДУПРЕЖДЕНИЯ</small><b>${Number(counts.warning || 0)}</b></button><button data-action="set-log-filter" data-filter="info"><small>INFO</small><b>${Number(counts.info || 0)}</b></button><button data-action="set-log-filter" data-filter="all" class="${ui.toolLogFilter === 'all' ? 'active' : ''}"><small>ВСЕ</small><b>${source.length}</b></button></section>
      <div class="point-tool-actions">${toolCommandButton('localAgent.showCoreChronicle', 'Открыть файл лога')}${toolCommandButton('localAgent.openLogChat', 'Отдельный чат по логам', 'primary')}${toolCommandButton('localAgent.rebuildIndex', 'Перестроить индекс')}</div>
      ${data.error ? `<div class="error-banner"><span>!</span><p>${esc(data.error)}</p></div>` : ''}
      <section class="point-log-stream" aria-label="Лента логов">${rows || toolWindowEmpty('Событий этого уровня нет', 'Выберите «Все» или обновите ленту.', '', true)}</section>
      <footer class="point-log-path" title="${esc(data.path || '')}">${esc(data.path || 'Лог создастся после запуска локального ядра')}</footer>
    </main>`)
  }
  function currentToolWindowView() {
    const kind = toolWindowKind()
    if (kind === 'database') return databasesView()
    if (kind === 'ssh') return serversView()
    if (kind === 'git') return gitToolView()
    if (kind === 'terminal') return terminalToolView()
    if (kind === 'logs') return logsToolView()
    return shell(`<main class="point-tool-page">${toolWindowEmpty('Окно инструмента не найдено', 'Откройте его заново из рейки или по своей клавише.')}</main>`)
  }
  function shell(content) {
    // Очередь решений нужна не только её разделу: из неё берётся число на рейке и
    // в шапке. Пока её запрашивал только сам раздел, счётчик на обзоре считался
    // локально и не знал про подтверждения — агент, заблокированный на разрешении,
    // не отражался нигде, и работа стояла молча.
    //
    // Повторного запроса не будет: состояние сразу уходит из 'idle', а ответ
    // переводит его в 'ready'. Без этого ответ вызывал бы отрисовку, а отрисовка —
    // новый запрос.
    if (!isToolWindow() && ui.decisionsStatus === 'idle' && ui.state.workspaceTrusted !== false && ui.state.service?.state === 'running') {
      ui.decisionsStatus = 'loading'
      setTimeout(() => vscode.postMessage({ type: 'loadDecisions' }), 0)
    }
    const connected = ui.state.service?.state === 'running'
    const subtitle = ui.state.workspaceTrusted === false ? `БЕЗОПАСНЫЙ РЕЖИМ · ${esc(ui.state.workspace)}` : connected ? `ЛОКАЛЬНО · ${esc(ui.state.workspace)}` : 'ЛОКАЛЬНОЕ ЯДРО'
    const index = ui.state.boot?.indexStatus
    // Состояния индекса перечислены полностью и намеренно: раньше всё, кроме
    // четырёх известных, показывалось как «БЕЗ ИНДЕКСА» — и отказ индексации
    // (error), и отсутствие открытой папки (no_workspace) выглядели так, будто
    // индекс просто не построен. Строка состояния IDE эти случаи различает
    // давно (formatIndexStatus), а заголовок Чертога с ней расходился.
    const indexLabel = index?.state==='ready'?(index.partial?`ИНДЕКС ЧАСТИЧНЫЙ · ${index.files}`:`ИНДЕКС ${index.files}`):index?.state==='indexing'?'ИНДЕКСАЦИЯ…':index?.state==='pending'?'ОБНОВЛЕНИЕ…':index?.state==='stale'?'ИНДЕКС УСТАРЕЛ':index?.state==='error'?'ИНДЕКС · ОШИБКА':index?.state==='no_workspace'?'ПАПКА НЕ ОТКРЫТА':index?.state==='not_built'?'БЕЗ ИНДЕКСА':'ИНДЕКС НЕИЗВЕСТЕН'
    if (isToolWindow()) {
      return `<div class="point-tool-app tool-${esc(toolWindowKind())}">
        ${ui.transientError ? `<div class="error-banner"><span>!</span><p>${esc(ui.transientError)}</p><button data-action="dismiss-error">×</button></div>` : ''}
        ${content}
        <footer class="point-tool-status"><span><i class="${connected ? 'live' : ''}"></i>${esc(subtitle)}</span><button type="button" data-action="tool-command" data-command="localAgent.showIndexStatus">${esc(indexLabel)}</button></footer>
      </div>`
    }
    if (isConnectionsView()) {
      return `<div class="app connections-app">
        <header class="brand"><div class="brand-mark ${connected ? 'live' : ''}"><span>P</span><i></i></div><div><strong>POINT / ПОДКЛЮЧЕНИЯ</strong><small>${subtitle}</small></div><div class="brand-actions"><button class="icon-button" data-action="show-output" title="Открыть хронику ядра">≡</button></div></header>
        ${ui.transientError ? `<div class="error-banner"><span>!</span><p>${esc(ui.transientError)}</p><button data-action="dismiss-error">×</button></div>` : ''}
        ${content}
      </div>`
    }
    if (isStatisticsView()) {
      return `<div class="app statistics-app">
        <header class="brand"><div class="brand-mark ${connected ? 'live' : ''}"><span>P</span><i></i></div><div><strong>POINT / СТАТИСТИКА</strong><small>${subtitle}</small></div><div class="brand-actions"><button type="button" class="secondary" data-action="focus-hub">← Гильдия</button><button class="icon-button" data-action="show-output" title="Открыть хронику ядра">≡</button></div></header>
        ${ui.transientError ? `<div class="error-banner"><span>!</span><p>${esc(ui.transientError)}</p><button data-action="dismiss-error">×</button></div>` : ''}
        ${content}
      </div>`
    }
    if (isDockerView()) {
      return `<div class="app docker-app">
        <header class="brand"><div class="brand-mark ${connected ? 'live' : ''}"><span>P</span><i></i></div><div><strong>POINT / DOCKER</strong><small>${subtitle}</small></div><div class="brand-actions"><button type="button" class="secondary" data-action="focus-hub">← Гильдия</button><button class="icon-button" data-action="show-output" title="Открыть хронику ядра">≡</button></div></header>
        ${ui.transientError ? `<div class="error-banner"><span>!</span><p>${esc(ui.transientError)}</p><button data-action="dismiss-error">×</button></div>` : ''}
        ${content}
      </div>`
    }
    const lockHub = hubNavLocked()
    const pendingSets = pendingChangeSets().length
    const waiting = decisionsWaitingCount()
    const spend = hallSpendLabel()
    // Чат — дом Чертога, и экран у него свой: слева чаты всех проектов, справа
    // разговор. Рейки разделов здесь нет вовсе — они уехали в настройки
    // проекта, вход одной кнопкой справа. Срочное при этом не спрятано: очередь
    // решений и непроверенные изменения остаются значками в шапке, иначе агент,
    // застрявший на подтверждении, молчал бы до следующего захода в настройки.
    if (ui.state.selectedTab === 'master') {
      const chatTitle = ui.masterData?.sessions?.items?.find(item => item.id === ui.masterData?.sessions?.active)?.title || 'Разговор'
      // `is-focused` остаётся на корне намеренно: на нём висит весь тихий регистр
      // разговора из 05-hall — палитра оболочки вместо палитры Хаба и снятая
      // декорация. `is-chat` добавляет только две колонки.
      return `<div class="hall is-focused is-chat">
        <div class="hall-scan"></div>
        ${projectChatDirectoryHtml()}
        <main class="hall-main">
          <header class="hall-head hall-head-chat">
            <button type="button" class="hall-btn is-sm" data-action="master-session-sidebar" aria-label="Показать или скрыть список чатов" title="Список чатов">☰</button>
            ${/* Имя мира в подписи обязательно: список кросс-проектный, и без
                  него не видно, в каком мире пишешь. Чип модели остался в
                  композере — у эталона модель выбирают там же, где пишут. */''}
            <div class="hall-chat-heading"><strong>${esc(chatTitle)}</strong><small>${esc(ui.state.workspace || 'Проект не выбран')}</small></div>
            ${masterBriefTabHtml ? masterBriefTabHtml() : ''}
            <button type="button" class="hall-btn is-sm" data-action="master-session-toggle" data-panel="history" aria-label="Действия с разговором" title="Действия с разговором">•••</button>
            ${hallAlarmHtml(waiting)}
            ${hallChangesAlarmHtml(pendingSets)}
            <button type="button" class="hall-btn is-sm hall-chat-settings" data-action="tab" data-tab="overview" title="Обзор, квесты, агенты и связи проекта">Настройки проекта</button>
          </header>
          ${ui.transientError ? `<div class="error-banner"><span>!</span><p>${esc(ui.transientError)}</p><button data-action="dismiss-error">×</button></div>` : ''}
          <div class="hall-body">${content}</div>
        </main>
      </div>`
    }
    // Всё остальное — настройки проекта: та же рейка, что была, минус «Мастер»,
    // плюс возврат в чат первым элементом шапки.
    return `<div class="hall is-setup">
      <div class="hall-scan"></div>
      <aside class="hall-rail">
        <div class="hall-rail-head">
          <div class="hall-mark">◇</div>
          ${projectSwitcherChipHtml()}
        </div>
        <nav class="hall-nav" aria-label="Разделы Чертога">${HALL_SECTIONS.map(section => {
          const locked = lockHub
          const badge = hallSectionBadge(section.id, pendingSets, waiting)
          return `<button class="${hallActiveSection() === section.id ? 'is-active' : ''}${section.id === 'decisions' && waiting ? ' is-urgent' : ''}" data-action="tab" data-tab="${section.tabs[0]}" title="${locked ? 'Сначала закончите компаньона и мастера' : esc(section.label + ' — ' + section.title)}" ${locked ? 'disabled' : ''}><i></i><em class="hall-glyph" aria-hidden="true">${esc(section.icon || '·')}</em><span>${section.label}</span><b>${badge}</b></button>`
        }).join('')}</nav>
        <div class="hall-rail-foot">
          <div class="row"><i></i><b>${esc(indexLabel)}</b></div>
          <small>${esc(subtitle)}</small>
        </div>
      </aside>
      <main class="hall-main">
        <header class="hall-head">
          <button type="button" class="hall-btn is-sm" data-action="tab" data-tab="master" title="Вернуться к разговору с мастером">← В чат</button>
          <div class="hall-crumb">ПРОЕКТ · ${esc(ui.state.workspace || 'без мира')} / ${esc(hallCrumb())}</div>
          ${/* На Обзоре расход показан отдельной карточкой с «ПОДРОБНО» и
               примечанием про запуски без цены; датчик здесь был третьим
               показом одной и той же цифры на одном экране. */''}
          ${spend && hallActiveSection() !== 'overview' ? `<dl class="hall-gauge"><dt>РАСХОД</dt><dd>${esc(spend)}</dd></dl>` : ''}
          ${hallAlarmHtml(waiting)}
          ${hallChangesAlarmHtml(pendingSets)}
          <button class="hall-btn is-sm" data-action="tab" data-tab="statistics" title="Расход, запуски и бюджет проекта">СТАТИСТИКА</button>
          <button class="hall-btn is-sm" data-action="show-output" title="Открыть хронику ядра">≡</button>
        </header>
        ${ui.transientError ? `<div class="error-banner"><span>!</span><p>${esc(ui.transientError)}</p><button data-action="dismiss-error">×</button></div>` : ''}
        <div class="hall-body">${content}</div>
      </main>
    </div>`
  }
  
  // Пять разделов настроек проекта. Прежние вкладки не исчезли — они стали
  // подвидами раздела, поэтому смена навигации не потребовала переписывать все экраны
  // разом. Первая вкладка списка — та, куда ведёт клик по разделу.
  //
  // Мастера здесь нет намеренно: чат — дом, а не раздел настроек. Вход в него
  // один — кнопка «← В чат» в шапке; второй вход с рейки разошёлся бы с ним
  // подсветкой активного раздела.
  const HALL_SECTIONS = [
    { id: 'overview', icon: '◇', label: 'ОБЗОР', title: 'Что требует внимания прямо сейчас', tabs: ['overview'] },
    { id: 'decisions', icon: '!', label: 'РЕШЕНИЯ', title: 'Всё, что ждёт вашего решения', tabs: ['decisions'] },
    { id: 'changes', icon: '±', label: 'ИЗМЕНЕНИЯ', title: 'Наборы, журнал правок и откат', tabs: ['changesets', 'filehistory', 'journal', 'changes'] },
    { id: 'quests', icon: '⚑', label: 'КВЕСТЫ', title: 'Активные квесты и хроника прогонов', tabs: ['quests', 'quest', 'history'] },
    { id: 'guild', icon: '⬡', label: 'ГИЛЬДИЯ', title: 'Разовая настройка: агенты, отряды, навыки, связи', tabs: ['agents', 'teams', 'skills', 'memory', 'connections', 'databases', 'tools', 'onboarding'] },
  ]
  
  // Незнакомая вкладка не подсвечивает ни одного раздела. Раньше запасным
  // значением был «Обзор», и на Статистике с Контейнерами рейка утверждала, что
  // вы в Обзоре, пока крошка над ней писала обратное. Пустая подсветка честнее:
  // эти два экрана открываются кнопкой шапки, а не разделом рейки.

  return {
    setAgentCapability,
    agentCapabilityKey,
    setCapabilityDelta,
    capabilityDeltaKey,
    setHandoffChain,
    handoffFlowRunId,
    setQuestOutcome,
    questOutcomeId,
    setQuestReplans,
    resetQuestReplansCache,
    questReplansId,
    toggleOpenQuest,
    closeQuestIfOpen,
    agentCapabilityKeyFor,
    AGENT_CAPABILITY_LIMIT,
    agentCapabilityCache,
    agentCapabilityInflight,
    agentCapabilityFailed,
    requestAgentCapability,
    agentCapabilityFor,
    profileReadiness,
    requestCapabilityDelta,
    capabilityDeltaHtml,
    questWorkHtml,
    orphanExecutions,
    orphanExecutionsHtml,
    questListHtml,
    questOutcomeHtml,
    questMidFlightHtml,
    handoffsHtml,
    agentCapabilityHtml,
    PROFILE_STEP_IDS,
    blockerStep,
    firstUnreadinessStep,
    questBriefQuality,
    canAcceptQuest,
    questBriefGuidanceHtml,
    runQualityOutcomeHtml,
    historyQualitySignals,
    flowStages,
    activeToolPresetId,
    stepValidationIssue,
    templateClassPreview,
    templatePickerHtml,
    hireLiveChecklist,
    hireSummaryPanel,
    readinessBanner,
    profileStepNav,
    profileStepFooter,
    pendingDecisionsBanner,
    patchStatusLabel,
    questPayload,
    composeQuestTask,
    formatTime,
    status,
    agentClass,
    agentProgress,
    questLevelUpBadge,
    agentSynergy,
    agentCharacterCard,
    activeQuestCard,
    formatBytes,
    formatDuration,
    diagnosticSignalText,
    guardrailEventText,
    CORE_FAILURE_HINTS,
    coreFailureText,
    toolFailureText,
    diagnosticsCard,
    runComparison,
    contextKindLabel,
    invalidateAgentRunPreview,
    requestContextPreview,
    contextPreviewMarkup,
    hubNavLocked,
    hubSituation,
    toolCommandButton,
    toolWindowHeading,
    gitFileKind,
    gitChanges,
    gitLists,
    gitCheckedPaths,
    gitGroupOf,
    gitGroups,
    gitGroupPaths,
    syncGitChecked,
    syncGitCommitButtons,
    syncGitTree,
    gitToolView,
    terminalToolView,
    logsToolView,
    currentToolWindowView,
    shell,
    HALL_SECTIONS,
  }
}
