import { fillAttribute, formatBytes } from './format-units.js'
// Hub runtime UI helpers: live run status, exec controls, pending reviews,
// quest progress strips, and context inspector chrome shared by overview/hall/master.

const RUN_TERMINAL_STATUSES = ['completed', 'failed', 'cancelled', 'interrupted']

export function runIsFinished(run) {
  return RUN_TERMINAL_STATUSES.includes(String(run?.status || ''))
}

export function runIsLive(run) {
  return Boolean(run?.status) && !runIsFinished(run)
}

export function runIsActiveNow(run) {
  return runIsLive(run) && String(run?.status || '') !== 'pending'
}

export function pendingChangeSets(boot) {
  return (boot?.changeSets || []).filter(item => item.status === 'pending' || item.status === 'approved')
}

export function pendingQuestProposals(boot) {
  return (boot?.questProposals || []).filter(item => item.status === 'pending' || item.status === 'modified')
}

export function pendingActionProposals(boot) {
  return (boot?.companionActionProposals || []).filter(item => item.status === 'pending' || item.status === 'modified')
}

export function usageSummary(records) {
  const list = records || []
  let totalTokens = 0
  let totalCents = 0
  let discussionTokens = 0
  let planTokens = 0
  let executionTokens = 0
  let learningTokens = 0
  for (const item of list) {
    const tokens = Number(item.totalTokens || 0)
    totalTokens += tokens
    if (item.costCents != null) totalCents += Number(item.costCents)
    const outcome = String(item.outcome || '')
    const questId = String(item.questId || '')
    if (outcome === 'master_model' || (outcome === 'companion_model' && !questId)) discussionTokens += tokens
    else if (outcome === 'orchestrator_plan' || outcome === 'master_planner_model') planTokens += tokens
    else if (outcome === 'agent_self_improvement') learningTokens += tokens
    else if (questId) executionTokens += tokens
  }
  return { totalTokens, totalCents, count: list.length, discussionTokens, planTokens, executionTokens, learningTokens }
}

export function createHubRuntimeUi({
  getState,
  esc,
  countOf,
  statusLabels,
  hubAgents,
  agentDisplayName,
  formatCents,
  getPlannerFallbackNotice,
  getContextInspectorRunId,
  getContextInspector,
  getContextInspectorStatus,
  getContextInspectorNotice,
  getKeptRunId = () => '',
}) {

  function contextKindLabel(item) {
    if (item.kind === 'image') return `Изображение${item.width && item.height ? ` ${item.width}×${item.height}` : ''}`
    if (item.kind === 'table') return 'Таблица'
    if (item.kind === 'document') return 'Документ'
    return item.kind === 'text' ? 'Текст' : 'Файл'
  }

  function plannerFallbackBannerHtml(questId) {
    const notice = getPlannerFallbackNotice()
    if (!notice?.message) return ''
    if (questId && notice.questId && notice.questId !== questId) return ''
    return `<aside class="readiness-banner compact" data-planner-fallback="1"><span>!</span><div><strong>План собрал движок Point, не модель</strong><small>${esc(notice.message)}</small></div><button type="button" class="secondary" data-action="dismiss-planner-fallback">Скрыть</button></aside>`
  }

  function currentHubQuest() {
    const quests = getState().boot?.quests || []
    const active = quests.find(item => item.status === 'active') || quests.find(item => item.status === 'proposed') || quests[0]
    if (active) return active
    const legacyRun = (getState().boot?.runs || []).find(runIsLive)
    if (legacyRun) return { id: legacyRun.id, title: legacyRun.task, status: 'active', legacyRun: true }
    return null
  }

  function currentHubTeam(quest) {
    const teams = getState().boot?.teams || []
    if (quest?.teamId) return teams.find(item => item.id === quest.teamId)
    return teams[0]
  }

  function activeExecutions() {
    const active = ['running', 'waiting_approval', 'pending', 'paused']
    const executions = (getState().boot?.executions || []).filter(item => active.includes(item.status))
    if (executions.length) return executions
    return (getState().boot?.runs || []).filter(run => active.includes(run.status)).map(run => ({
      id: run.id, task: run.task, status: run.status, projectAgentId: run.profileId, runId: run.id, legacy: true,
    }))
  }

  function executionRunId(item) {
    return String(item?.runId || (item?.legacy ? item?.id : '') || '')
  }

  function executionGuaranteesHtml() {
    const sandbox = getState().boot?.sandbox || {}
    const backend = [sandbox.backend || 'среда не определена', sandbox.version || ''].filter(Boolean).join(' · ')
    const guarantees = [
          ['РАБОЧАЯ КОПИЯ', Boolean(sandbox.liveWorkspaceIsolation), sandbox.liveWorkspaceIsolation ? 'изменения только через набор изменений' : 'нет гарантии'],
          ['ПРОЦЕСС', Boolean(sandbox.processIsolation), sandbox.processIsolation ? 'контейнерная граница' : 'процесс пользователя Point'],
          ['СЕТЬ', Boolean(sandbox.networkIsolation), sandbox.networkIsolation ? 'политика выхода в сеть Point' : 'без сетевой границы'],
          ['СЕКРЕТЫ', Boolean(sandbox.secretEnvironmentSanitization), sandbox.secretEnvironmentSanitization ? 'переменные окружения фильтруются' : 'нет гарантии'],
        ]
    return `<details class="execution-guarantees"><summary>Гарантии исполнения · ${esc(backend)}</summary><div>${guarantees.map(([label, enabled, detail]) => `<span class="${enabled ? 'available' : 'unavailable'}"><b>${enabled ? '✓' : '!'}</b><small>${esc(label)}</small><em>${esc(detail)}</em></span>`).join('')}</div></details>`
  }

  function execControlsHtml(item) {
    const runId = executionRunId(item)
    const guarantees = executionGuaranteesHtml(item)
    const isPending = (item.status === 'pending' || item.status === 'interrupted') && !item.legacy
    if (isPending) {
      const waiting = 'Агент готов к работе'
      return `${guarantees}<div class="hub-exec-actions"><em class="muted">${waiting}</em><button type="button" class="primary" data-action="launch-execution" data-id="${esc(item.id)}">Запустить агента</button><button type="button" class="secondary" data-action="revert-execution" data-id="${esc(item.id)}">Отменить</button></div>`
    }
    if (!runId) return guarantees
    const linkedRun = (getState().boot?.runs || []).find(run => run.id === runId) || (item.legacy ? item : null)
    const controller = linkedRun?.controller || {}
    const isPaused = item.status === 'paused' || linkedRun?.status === 'paused'
    const isStoppable = runIsLive(item) || runIsLive(linkedRun)
    const canPause = ['running', 'waiting_approval'].includes(item.status)
    const exhausted = controller.pauseReason === 'active_time_exhausted'
    const remaining = Number(controller.activeSecondsRemaining || 0)
    const budget = Number(controller.activeSecondsBudget || 0)
    const controllerNote = budget > 0
      ? `<em class="muted">${isPaused && controller.pauseReason === 'active_time_exhausted' ? 'Лимит активного времени' : isPaused && controller.pauseReason ? `Пауза · ${esc(controller.pauseReason)}` : `Активное время · осталось ${remaining} с`}${controller.resumable && isPaused ? ' · можно продолжить' : ''}</em>`
      : (isPaused && controller.pauseReason ? `<em class="muted">Пауза · ${esc(controller.pauseReason)}</em>` : '')
    const pauseBtn = isPaused
      ? (exhausted
        ? `<button type="button" class="primary" data-action="extend-active-time" data-run-id="${esc(runId)}">Продлить и продолжить</button>`
        : `<button type="button" class="primary" data-action="resume-run" data-run-id="${esc(runId)}">Продолжить</button>`)
      : `<button type="button" class="secondary" data-action="pause-run" data-run-id="${esc(runId)}" ${canPause ? '' : 'disabled'}>Пауза</button>`
    const stopBtn = `<button type="button" class="danger-button" data-action="cancel" data-id="${esc(runId)}" ${isStoppable ? '' : 'disabled'}>Стоп</button>`
    const contextBtn = `<button type="button" class="secondary" data-action="load-context-inspector" data-run-id="${esc(runId)}">Контекст</button>`
    const revertBtn = item.legacy || !item.id ? '' : `<button type="button" class="secondary" data-action="revert-execution" data-id="${esc(item.id)}">Откат</button>`
    const msgForm = isStoppable
      ? `<form class="exec-inline-form" data-exec-form="message" data-run-id="${esc(runId)}"><input type="text" placeholder="Сообщение…" maxlength="2000"><label class="exec-learning-consent" title="Разрешить Agent Hub использовать это сообщение в отдельном review после Run"><input type="checkbox" name="learn-from-message"><span>Урок</span></label><button type="submit" class="small-button" title="Отправить агенту">→</button></form>`
      : ''
    const forbidForm = isStoppable
      ? `<form class="exec-inline-form" data-exec-form="forbid" data-run-id="${esc(runId)}"><input type="text" placeholder="Запретить путь…"><button type="submit" class="small-button" title="Запретить файл">⊘</button></form>`
      : ''
    return `${guarantees}<div class="hub-exec-actions">${controllerNote}${pauseBtn}${stopBtn}${contextBtn}${revertBtn}${msgForm}${forbidForm}</div>`
  }

  function flowApprovalStripHtml() {
    const runs = getState().boot?.flowRuns || []
    const mergeWaiting = runs.filter(run => Object.values(run.nodeStates || {}).some(nodeState => nodeState?.output?.waitReason === 'sandbox_merge_conflict'))
    const mergeRunIDs = new Set(mergeWaiting.map(run => run.id))
    const approvals = runs.flatMap(run => Object.entries(run.nodeStates || {})
      .filter(([, nodeState]) => nodeState.status === 'waiting_approval' && nodeState?.output?.waitReason !== 'sandbox_merge_conflict')
      .map(([nodeId]) => ({ run, nodeId })))
      .filter(item => !mergeRunIDs.has(item.run.id))
    if (!approvals.length && !mergeWaiting.length) return ''
    const mergeRows = mergeWaiting.slice(0, 3).map(run => {
      const count = Object.values(run.nodeStates || {}).reduce((sum, nodeState) => sum + Number(nodeState?.output?.mergeConflictCount || 0), 0)
      return `<article class="hub-exec-row"><div><strong>Слияние веток</strong><small>${countOf(count, 'файл требует', 'файла требуют', 'файлов требуют')} выбора</small></div><div class="hub-exec-actions"><button type="button" class="primary" data-action="tab" data-tab="decisions">Решить →</button></div></article>`
    }).join('')
    const approvalRows = approvals.slice(0, 3).map(({ run, nodeId }) => {
      const flow = (getState().boot?.flows || []).find(item => item.id === run.flowId)
      const node = (flow?.nodes || []).find(item => item.id === nodeId)
      const label = node?.name || 'Этап квеста'
      return `<article class="hub-exec-row"><div><strong>${esc(label)}</strong><small>Продолжить после этого этапа?</small></div><div class="hub-exec-actions"><button type="button" class="primary" data-action="resolve-flow-node" data-flow-run-id="${esc(run.id)}" data-node-id="${esc(nodeId)}" data-approved="true">Продолжить</button><button type="button" class="danger-button" data-action="resolve-flow-node" data-flow-run-id="${esc(run.id)}" data-node-id="${esc(nodeId)}" data-approved="false">Остановить</button></div></article>`
    }).join('')
    return `<section class="hub-flow-approvals hub-card accent-ember live"><header class="section-title"><span>НУЖНО ВАШЕ РЕШЕНИЕ</span><em>${approvals.length + mergeWaiting.length}</em></header>${mergeRows}${approvalRows}</section>`
  }

  function questProgressHtml(quest) {
    if (!quest || quest.legacyRun) return ''
    const flowRuns = (getState().boot?.flowRuns || []).filter(run => run.questId === quest.id)
    const flowRun = flowRuns[0]
    if (!flowRun?.nodeStates) return `<div class="quest-progress-rail"><i ${fillAttribute(8)}></i></div>`
    const states = Object.values(flowRun.nodeStates)
    const done = states.filter(item => item.status === 'completed' || item.status === 'skipped').length
    const pct = states.length ? Math.round((done / states.length) * 100) : 0
    return `<div class="quest-progress-rail" title="${pct}%"><i ${fillAttribute(pct)}></i><small>${pct}%</small></div>`
  }

  function runPatchGroups(details) {
    const patches = Array.isArray(details?.patches) ? details.patches : []
    const groups = new Map()
    for (const patch of patches) {
      const status = String(patch.status || '')
      if (status !== 'applied' && status !== 'pending' && status !== 'proposed') continue
      const path = String(patch.path || patch.id || 'файл')
      if (!groups.has(path)) groups.set(path, { path, patchIds: [], applied: 0, pending: 0 })
      const group = groups.get(path)
      if (patch.id) group.patchIds.push(patch.id)
      if (status === 'applied') group.applied += 1
      else group.pending += 1
    }
    return [...groups.values()]
  }

  function sessionRunHasKeepUndoPatches(details) {
    return runPatchGroups(details).length > 0
  }

  function sessionKeepUndoVisible(workMode) {
    const details = getState().details
    const run = details?.run
    if (!run?.id) return false
    if (String(getKeptRunId() || '') === String(run.id)) return false
    if (!sessionRunHasKeepUndoPatches(details)) return false
    // Пока прогон идёт, изменённые файлы показывает лента
    // (sessionRunChangedFilesHtml): они принадлежат ходу и едут вместе с ним.
    // Композер — место для реплики человека, и список файлов, растущий в нём на
    // каждую правку агента, отнимает это место у разговора и повторяет ленту
    // вторым голосом. Решение «оставить или откатить» появляется здесь, когда
    // прогон кончился и решать стало что.
    if (runIsLive(run)) return false
    if (workMode === 'agent') return true
    return runIsFinished(run) && runPatchGroups(details).some(item => item.applied > 0)
  }

  function sessionKeepUndoBarHtml(workMode = 'discuss') {
    const details = getState().details
    if (!sessionKeepUndoVisible(workMode)) return ''
    const run = details.run
    const groups = runPatchGroups(details)
    const appliedCount = groups.reduce((sum, item) => sum + item.applied, 0)
    const pendingCount = groups.reduce((sum, item) => sum + item.pending, 0)
    const summary = [
      appliedCount ? countOf(appliedCount, 'файл применён', 'файла применены', 'файлов применено') : '',
      pendingCount ? countOf(pendingCount, 'файл ждёт', 'файла ждут', 'файлов ждут') + ' решения' : '',
    ].filter(Boolean).join(' · ')
    const fileRows = groups.slice(0, 8).map(item => {
      const undo = item.applied && item.patchIds.length
        ? `<button type="button" class="hall-btn is-sm" data-action="undo-run-file" data-run-id="${esc(run.id)}" data-patch-ids="${esc(item.patchIds.join(','))}" title="Откатить ${esc(item.path)}">↶</button>`
        : ''
      const mark = item.pending && !item.applied ? '<em class="session-run-pending">ждёт</em>' : ''
      return `<li><button type="button" class="hall-chip" data-action="open-file" data-path="${esc(item.path)}">${esc(item.path)}</button>${mark}${undo}</li>`
    }).join('')
    const overflow = groups.length > 8 ? `<li class="session-run-more"><small>ещё ${groups.length - 8}</small></li>` : ''
    return `<section class="hall-strip session-keep-undo" data-run-id="${esc(run.id)}"><i></i><div class="session-keep-undo-body"><span>${esc(summary || 'Изменения агента')}</span><ul class="session-keep-undo-files">${fileRows}${overflow}</ul></div><div class="session-keep-undo-actions"><button type="button" class="hall-btn is-primary" data-action="keep-run-all" data-run-id="${esc(run.id)}">Оставить всё</button><button type="button" class="hall-btn" data-action="undo-run-all" data-run-id="${esc(run.id)}">Откатить всё</button></div></section>`
  }

  function sessionRunChangedFilesHtml() {
    const details = getState().details
    const run = details?.run
    if (!run?.id || !runIsLive(run)) return ''
    const groups = runPatchGroups(details)
    if (!groups.length) return ''
    const chips = groups.slice(0, 6).map(item =>
      `<button type="button" class="hall-chip" data-action="open-file" data-path="${esc(item.path)}">${esc(item.path)}${item.pending && !item.applied ? ' · ждёт' : ''}</button>`
    ).join('')
    const more = groups.length > 6 ? `<small>+${groups.length - 6}</small>` : ''
    return `<div class="hall-strip is-quiet session-run-files"><i></i><span class="session-run-files-label">Изменённые файлы</span><div class="session-run-files-list">${chips}${more}</div></div>`
  }

  function questStatusStripHtml(quest) {
    if (!quest || quest.legacyRun) return ''
    const questId = String(quest.id || '')
    if (!questId) return ''
    const progress = questProgressHtml(quest)
    const executions = activeExecutions().filter(item => item.questId === questId)
    const sets = pendingChangeSets().filter(item => item.questId === questId)
    const waitingRuns = executions.filter(item => item.status === 'waiting_approval' || item.status === 'waiting').length
    const waitingSets = sets.filter(item => item.status === 'pending').length
    const waiting = waitingRuns + waitingSets
    let blocker = ''
    if (waiting) {
      const reasons = []
      if (waitingRuns) reasons.push(countOf(waitingRuns, 'запуск ждёт', 'запуска ждут', 'запусков ждут') + ' подтверждения')
      if (waitingSets) reasons.push(countOf(waitingSets, 'набор ждёт', 'набора ждут', 'наборов ждут') + ' ревью')
      blocker = `<span class="quest-status-blocker">${esc(reasons.join(', '))}</span>`
    }
    const flowRuns = (getState().boot?.flowRuns || []).filter(run => run.questId === questId)
    let residual = flowRuns.reduce((sum, run) =>
      sum + Object.values(run.nodeStates || {}).filter(nodeState =>
        nodeState.status === 'waiting_approval' && nodeState?.output?.waitReason !== 'sandbox_merge_conflict'
      ).length, 0)
    const details = getState().details
    const linkedRun = details?.run
    if (linkedRun && (linkedRun.questId === questId || executions.some(item => item.runId === linkedRun.id))) {
      residual += (details.approvals || []).filter(item => item.status === 'pending').length
    }
    const approvalBadge = residual
      ? `<span class="quest-status-approvals"><em>${residual}</em> ${residual === 1 ? 'решение' : residual < 5 ? 'решения' : 'решений'}</span>`
      : ''
    if (!progress && !blocker && !approvalBadge) return ''
    return `<div class="party-status-strip quest-status-strip">${progress}${blocker}${approvalBadge}</div>`
  }

  function partyStatusStripHtml(teamAgents, quest) {
    if (!teamAgents.length) return ''
    const executions = activeExecutions()
    return `<div class="party-status-strip">${teamAgents.map(agent => {
      const exec = executions.find(item => item.projectAgentId === agent.id)
      const label = exec ? (statusLabels[exec.status] || exec.status) : 'ОЖИДАЕТ'
      const cls = exec ? exec.status : 'idle'
      return `<span class="party-status ${esc(cls)}"><strong>${esc(agentDisplayName(agent))}</strong><small>${esc(label)}</small></span>`
    }).join('')}</div>`
  }

  function contextInspectorPanelHtml() {
    const contextInspectorRunId = getContextInspectorRunId()
    if (!contextInspectorRunId) return ''
    const contextInspectorStatus = getContextInspectorStatus()
    const contextInspector = getContextInspector()
    const contextInspectorNotice = getContextInspectorNotice()
    if (contextInspectorStatus === 'loading') {
      return `<section class="context-inspector"><header><strong>Инспектор контекста</strong><button type="button" class="secondary" data-action="close-context-inspector">×</button></header><p class="muted">Загрузка…</p></section>`
    }
    if (contextInspectorStatus === 'error') {
      return `<section class="context-inspector"><header><strong>Инспектор контекста</strong><button type="button" class="secondary" data-action="close-context-inspector">×</button></header><p class="create-step-error">${esc(contextInspector?.error || 'Не удалось загрузить контекст')}</p></section>`
    }
    const preview = contextInspector || {}
    const items = Array.isArray(preview.items) ? preview.items : []
    const groups = new Map()
    for (const item of items) {
      const key = item.category || 'прочее'
      if (!groups.has(key)) groups.set(key, [])
      groups.get(key).push(item)
    }
    const categoryHtml = [...groups.entries()].map(([category, groupItems]) => {
      const tokens = groupItems.reduce((sum, item) => sum + Number(item.tokenEstimate || 0), 0)
      return `<section class="context-inspector-category"><header><strong>${esc(category)}</strong><span>${groupItems.length} · ≈ ${tokens.toLocaleString('ru-RU')} ток.</span></header>${groupItems.map(item => `<article class="context-inspector-item ${item.pinned ? 'pinned' : ''} ${item.pending ? 'pending' : ''}"><div><b>${esc(contextKindLabel(item))}</b><strong>${esc(item.label || item.path || item.id)}</strong><small>${esc(item.source || item.addedBy || '')}${item.tokenEstimate ? ` · ≈ ${item.tokenEstimate} ток.` : ''}</small></div><footer>${item.pending ? '<span class="context-pending-badge">Ожидает безопасного шага</span>' : item.amendable ? `${item.pinned ? `<button type="button" class="secondary" data-action="context-amend" data-run-id="${esc(contextInspectorRunId)}" data-item-id="${esc(item.id)}" data-amend="unpin">Открепить</button>` : `<button type="button" class="secondary" data-action="context-amend" data-run-id="${esc(contextInspectorRunId)}" data-item-id="${esc(item.id)}" data-amend="pin">Закрепить</button>`}<button type="button" class="danger-button" data-action="context-amend" data-run-id="${esc(contextInspectorRunId)}" data-item-id="${esc(item.id)}" data-amend="remove">Убрать</button>` : '<span class="muted">Неизменяемый слой</span>'}</footer></article>`).join('')}</section>`
    }).join('')
    const liveActions = preview.active ? `<div class="context-inspector-actions"><button type="button" class="secondary" data-action="add-run-context-files" data-run-id="${esc(contextInspectorRunId)}">＋ Файл</button><button type="button" class="secondary" data-action="add-run-context-selection" data-run-id="${esc(contextInspectorRunId)}">＋ Выделение</button><button type="button" class="secondary context-close" data-action="close-context-inspector">×</button></div>` : '<button type="button" class="secondary context-close" data-action="close-context-inspector">×</button>'
    const liveHint = preview.active ? '<p class="context-live-hint">Добавьте файл или выделение — компаньон получит снимок перед следующим ходом. Текущая работа не перезапускается.</p>' : ''
    return `<section class="context-inspector"><header><div><strong>Инспектор контекста</strong><small>≈ ${Number(preview.estimatedTokens || 0).toLocaleString('ru-RU')} токенов · ${formatBytes(preview.totalContextBytes)}</small></div>${liveActions}</header>${liveHint}${contextInspectorNotice ? `<p class="context-live-notice">✓ ${esc(contextInspectorNotice)}</p>` : ''}${(preview.warnings || []).map(w => `<p class="context-live-warning">◷ ${esc(w)}</p>`).join('')}${categoryHtml || '<p class="muted">Контекст пуст.</p>'}</section>`
  }

  function questBudgetPhasesHtml(quest, usage) {
    if (!quest) return ''
    const ceiling = Number(quest.budgetTokens || 0)
    const questId = String(quest.id || '')
    const records = (getState().boot?.usageRecords || []).filter(item => String(item.questId || '') === questId)
    let plan = 0, execution = 0, learning = 0
    for (const item of records) {
      const tokens = Number(item.totalTokens || 0)
      const outcome = String(item.outcome || '')
      if (outcome === 'orchestrator_plan' || outcome === 'master_planner_model') plan += tokens
      else if (outcome === 'agent_self_improvement') learning += tokens
      else execution += tokens
    }
    const used = plan + execution + learning
    const ceilingLabel = ceiling > 0 ? `${ceiling.toLocaleString('ru-RU')} ток.` : (quest.budgetCents ? formatCents(quest.budgetCents) : '—')
    return `<div class="hub-budget-phases"><span><small>ПОТОЛОК КВЕСТА</small><b>${ceilingLabel}</b></span><span><small>ПЛАН</small><b>${plan.toLocaleString('ru-RU')}</b></span><span><small>ИСПОЛНЕНИЕ</small><b>${execution.toLocaleString('ru-RU')}</b></span><span><small>ОБУЧЕНИЕ</small><b>${learning.toLocaleString('ru-RU')}</b></span><span><small>УЧТЕНО В КВЕСТЕ</small><b>${used.toLocaleString('ru-RU')}</b></span><span><small>ОБСУЖДЕНИЕ (WORKSPACE)</small><b>${Number(usage.discussionTokens || 0).toLocaleString('ru-RU')}</b></span><small class="hall-fineprint">Покрытие бюджета: частичное — обсуждение до Quest считается только в лимите проекта, не в потолке квеста.</small></div>`
  }

  return {
    runIsFinished,
    runIsLive,
    runIsActiveNow,
    plannerFallbackBannerHtml,
    currentHubQuest,
    currentHubTeam,
    activeExecutions,
    executionRunId,
    executionGuaranteesHtml,
    execControlsHtml,
    flowApprovalStripHtml,
    questProgressHtml,
    questStatusStripHtml,
    sessionKeepUndoBarHtml,
    sessionRunChangedFilesHtml,
    partyStatusStripHtml,
    contextInspectorPanelHtml,
    usageSummary,
    questBudgetPhasesHtml,
    pendingChangeSets: () => pendingChangeSets(getState().boot),
    pendingQuestProposals: () => pendingQuestProposals(getState().boot),
    pendingActionProposals: () => pendingActionProposals(getState().boot),
  }
}
