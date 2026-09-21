// Statistics owns evidence rendering only. Mutable request state and message
// transport remain in the main webview controller through explicit adapters.
import { fillAttribute } from './format-units.js'

export function createStatisticsViews({
  getStatisticsStatus,
  setStatisticsStatus,
  getStatisticsData,
  postMessage,
  shell,
  escapeHtml,
  countOf,
  formatDuration,
  formatCents,
  isStatisticsView,
}) {
  const esc = escapeHtml

function statisticsBreakdownHtml(title, items) {
  if (!Array.isArray(items) || !items.length) return ''
  const percent = (value, total) => total > 0 ? `${Math.round(Number(value || 0) / Number(total) * 100)}%` : '—'
  const evidence = item => {
    const quality = item.quality
    if (!quality || !quality.assessedExecutions) return ''
    const terminalTools = Number(quality.toolSucceeded || 0) + Number(quality.toolFailed || 0)
    const recommendations = Array.isArray(quality.recommendations) ? quality.recommendations : []
    const improvements = recommendations.length ? `<div class="statistics-improvements"><header><span>СЛЕДУЮЩИЙ ШАГ РАЗВИТИЯ</span><small>только по повторяемым сигналам</small></header>${recommendations.map(recommendation => `<div class="statistics-improvement is-${esc(recommendation.severity || 'attention')}"><div><strong>${esc(recommendation.title)}</strong><p>${esc(recommendation.detail)}</p><small>${esc(recommendation.evidence)}</small></div><button type="button" class="secondary" data-action="improve-agent" data-id="${esc(item.id)}" data-step="${esc(recommendation.step)}">${esc(recommendation.actionLabel || 'Открыть настройку')}</button></div>`).join('')}</div>` : ''
    return `<div class="statistics-quality-evidence"><header><span>ДОКАЗАТЕЛЬСТВА КАЧЕСТВА</span><small>${countOf(quality.assessedExecutions, 'запуск', 'запуска', 'запусков')} из ${Number(item.executions || 0).toLocaleString('ru-RU')}</small></header><span><small>ЗДОРОВЫЕ</small><strong>${percent(quality.healthyExecutions, quality.assessedExecutions)}</strong></span><span><small>ВЕРИФИКАЦИЯ</small><strong>${quality.verificationRequired ? percent(quality.verificationSatisfied, quality.verificationRequired) : 'не треб.'}</strong></span><span><small>TOOLS</small><strong>${terminalTools ? percent(quality.toolSucceeded, terminalTools) : '—'}</strong></span></div>${improvements}`
  }
  const phaseRow = item => {
    const hasPhases = Number(item.planTokens || 0) || Number(item.executionTokens || 0) || Number(item.learningTokens || 0) || item.budgetCoverage
    if (!hasPhases) return ''
    return `<div class="statistics-budget-phases"><span><small>ПЛАН</small><strong>${Number(item.planTokens || 0).toLocaleString('ru-RU')}</strong></span><span><small>ИСПОЛНЕНИЕ</small><strong>${Number(item.executionTokens || 0).toLocaleString('ru-RU')}</strong></span><span><small>ОБУЧЕНИЕ</small><strong>${Number(item.learningTokens || 0).toLocaleString('ru-RU')}</strong></span><span><small>ПОКРЫТИЕ</small><strong>${item.budgetCoverage === 'partial' ? 'частичное' : 'полное'}</strong></span></div>`
  }
  return `<section class="statistics-breakdown detailed"><header><strong>${esc(title)}</strong></header>${items.map(item => `<article><b>${esc(item.name || item.id)}</b><span><small>ЗАПУСКИ</small><strong>${Number(item.executions || 0).toLocaleString('ru-RU')}</strong></span><span><small>УСПЕХ</small><strong>${Number(item.successRate || 0).toFixed(0)}%</strong></span><span><small>ТОКЕНЫ (СУММА ЗАПИСЕЙ)</small><strong>${Number(item.totalTokens || 0).toLocaleString('ru-RU')}</strong></span><span><small>СРЕДНЕЕ</small><strong>${Number(item.averageTokens || 0).toLocaleString('ru-RU')}</strong></span><span><small>ВРЕМЯ</small><strong>${formatDuration(item.averageTimeMs || 0)}</strong></span><span><small>СТОИМОСТЬ</small><strong>${item.knownCostCents ? formatCents(item.knownCostCents) : '—'}</strong></span>${phaseRow(item)}${evidence(item)}</article>`).join('')}</section>`
}
function budgetPhaseSummaryHtml(stats) {
  const coverage = stats.budgetCoverage === 'partial' ? 'частичное' : (stats.budgetCoverage || '—')
  return `<section class="statistics-breakdown detailed budget-phase-summary"><header><strong>Фазы корневого бюджета</strong><small>покрытие: ${esc(coverage)}</small></header><article><b>Разбивка по этапам</b><span><small>ОБСУЖДЕНИЕ (WORKSPACE)</small><strong>${Number(stats.discussionTokens || 0).toLocaleString('ru-RU')}</strong></span><span><small>ПЛАН</small><strong>${Number(stats.planTokens || 0).toLocaleString('ru-RU')}</strong></span><span><small>ИСПОЛНЕНИЕ</small><strong>${Number(stats.executionTokens || 0).toLocaleString('ru-RU')}</strong></span><span><small>ОБУЧЕНИЕ</small><strong>${Number(stats.learningTokens || 0).toLocaleString('ru-RU')}</strong></span><aside class="statistics-quality-method"><strong>Как читать</strong><p>Обсуждение до Quest учитывается только лимитом проекта. Сумма токенов у квеста — не весь путь квеста: отдельно смотрите этапы план / исполнение / обучение.</p></aside></article></section>`
}
function budgetMeterHtml(label, used, limit) {
  const normalizedUsed = Math.max(0, Number(used || 0))
  const normalizedLimit = Math.max(0, Number(limit || 0))
  if (!normalizedLimit) return `<article class="budget-meter unset"><header><strong>${esc(label)}</strong><span>лимит не задан</span></header><div><i></i></div><small>${formatCents(normalizedUsed)} учтено</small></article>`
  const percent = Math.round(normalizedUsed / normalizedLimit * 100)
  const width = Math.min(100, Math.max(0, percent))
  const tone = percent >= 100 ? 'danger' : percent >= 80 ? 'warning' : 'safe'
  return `<article class="budget-meter ${tone}"><header><strong>${esc(label)}</strong><span>${percent}%</span></header><div><i ${fillAttribute(width)}></i></div><small>${formatCents(normalizedUsed)} из ${formatCents(normalizedLimit)}</small></article>`
}
function budgetSettingsHtml(stats) {
  const statisticsStatus = getStatisticsStatus()
  const daily = Number(stats.budgetDailyCents || 0)
  const monthly = Number(stats.budgetMonthlyCents || 0)
  const dailyValue = daily > 0 ? (daily / 100).toFixed(2) : ''
  const monthlyValue = monthly > 0 ? (monthly / 100).toFixed(2) : ''
  return `<section class="budget-settings"><header><div><span>БЮДЖЕТ ПРОЕКТА</span><strong>Контроль AI-расходов</strong><small>Лимиты относятся только к текущему проекту. Пустое значение отключает период.</small></div><em class="${stats.budgetHardStop ? 'hard' : ''}">${stats.budgetHardStop ? 'HARD STOP' : 'ПРЕДУПРЕЖДЕНИЯ'}</em></header><div class="budget-settings-grid"><form id="budget-form" class="budget-form"><label>Дневной лимит, $<input id="budget-daily" type="number" min="0" step="0.01" inputmode="decimal" value="${esc(dailyValue)}" placeholder="Не ограничен"></label><label>Месячный лимит, $<input id="budget-monthly" type="number" min="0" step="0.01" inputmode="decimal" value="${esc(monthlyValue)}" placeholder="Не ограничен"></label><label class="budget-hard-stop"><input id="budget-hard-stop" type="checkbox" ${stats.budgetHardStop ? 'checked' : ''}><span><strong>Останавливать новые запуски</strong><small>При достижении известного расхода ядро не запустит следующий квест.</small></span></label><button type="submit" class="primary" ${statisticsStatus === 'loading' ? 'disabled' : ''}>${statisticsStatus === 'loading' ? 'Сохраняю…' : 'Сохранить бюджет'}</button></form><div class="budget-meters">${budgetMeterHtml('Сегодня', stats.dailyCostCents, daily)}${budgetMeterHtml('Этот месяц', stats.monthlyCostCents, monthly)}<aside>Стоимость учитывается только когда провайдер сообщает достоверные данные. Неизвестный расход Point не выдумывает.</aside></div></div></section>`
}
function autonomousLearningHtml(stats) {
  const items = Array.isArray(stats.agentImprovements) ? stats.agentImprovements : []
  const agentNames = new Map((stats.agentStats || []).map(item => [item.id, item.name || item.id]))
  const statusLabel = {
    applied: 'ПРИМЕНЕНО',
    applied_unproven: 'ПРИМЕНЕНО · ЭФФЕКТ НЕ ДОКАЗАН',
    applied_proven: 'ПРИМЕНЕНО · ЭФФЕКТ ПОДТВЕРЖДЁН',
    rolled_back: 'ОТКАЧЕНО', skipped: 'ПРОПУЩЕНО', failed: 'СБОЙ', applying: 'ПРИМЕНЯЕТСЯ',
  }
  const kindLabel = {
    skill_created: 'создан новый skill', skill_updated: 'обновлена версия skill', skill_recovery: 'recovery / антипаттерн',
    memory_learned: 'добавлена подтверждённая Memory', instruction_learned: 'добавлено подтверждённое Rule',
    curation_merge_proposed: 'предложение merge Skills',
  }
  const effectLabel = { improved: 'эффект: улучшение', neutral: 'эффект: нейтрально', regressed: 'эффект: регресс', insufficient_sample: 'эффект: мало данных' }
  const triggerLabel = {
    successful_complex_run: 'успешная сложная траектория',
    user_feedback_after_recovery: 'явная коррекция',
    failed_or_regressed_run: 'провал / пробел верификации',
    curator_merge_proposal: 'куратор',
    manual_teach: 'ручной урок',
  }
  const cards = items.slice(0, 12).map(item => {
    const skill = item.afterSkill || item.beforeSkill || {}
    const revision = Number(skill.configuration?.revision || 0)
    const promotion = item.promotionStatus || skill.configuration?.promotionStatus || (item.skillId ? 'project_only' : '')
    const projectCount = Number(skill.configuration?.promotionWorkspaceCount || 0)
    const scopeLabel = promotion === 'promoted'
      ? `УНИВЕРСАЛЬНЫЙ SKILL · ПРОЕКТОВ: ${projectCount || 2}`
      : promotion === 'candidate' ? `КАНДИДАТ · ${projectCount || 1}/2 ПРОЕКТОВ` : 'ТОЛЬКО ЭТОТ ПРОЕКТ'
    const scope = promotion ? `<b class="agent-learning-scope is-${esc(promotion)}">${esc(scopeLabel)}</b>` : ''
    const memory = item.afterMemory || item.beforeMemory
    const memoryStatusLabel = { candidate: 'КАНДИДАТ ПАМЯТИ · 1/2', promoted: 'ПЕРЕНОСИМАЯ ПАМЯТЬ', confirmed: 'ПАМЯТЬ ПОДТВЕРЖДЕНА' }
    const memoryBlock = memory && item.memoryStatus
      ? `<aside class="agent-learning-memory is-${esc(item.memoryStatus)}"><header><span>ПАМЯТЬ СПЕЦИАЛИСТА</span><em>${esc(memoryStatusLabel[item.memoryStatus] || item.memoryStatus)}</em></header><p>${esc(memory.content || '')}</p><small>${esc(item.memoryKey || 'устойчивый принцип')} · независимых проектов: ${Number(item.memorySourceWorkspaces?.length || 1)}</small></aside>`
      : ''
    const instructionStatusLabel = { candidate: 'КАНДИДАТ · 1/2', promoted: 'В BLUEPRINT', confirmed: 'ПОДТВЕРЖДЕНА' }
    const instructionBlock = item.instruction && item.instructionStatus
      ? `<aside class="agent-learning-instruction is-${esc(item.instructionStatus)}"><header><span>ПОСТОЯННАЯ ИНСТРУКЦИЯ</span><em>${esc(instructionStatusLabel[item.instructionStatus] || item.instructionStatus)}</em></header><p>${esc(item.instruction)}</p><small>${esc(item.instructionKey || 'поведение')} · независимых проектов: ${Number(item.instructionSourceWorkspaces?.length || 1)}</small></aside>`
      : ''
    const canary = item.canaryEvaluation
    const canaryStatusLabel = { pending: 'НАБИРАЕТ ВЫБОРКУ', healthy: 'ГЕЙТ ПРОЙДЕН', regressed: item.status === 'rolled_back' ? 'АВТООТКАТ' : 'ДЕГРАДАЦИЯ' }
    const canaryPercent = value => `${Math.round(Number(value || 0) * 100)}%`
    const canaryMetrics = metrics => metrics ? `<div><span><small>RUNS</small><b>${Number(metrics.runs || 0)}/${Number(canary.minimumCandidateRuns || 3)}</b></span><span><small>ЗАВЕРШЕНЫ</small><b>${canaryPercent(metrics.completionRate)}</b></span><span><small>ЗДОРОВЫЕ</small><b>${canaryPercent(metrics.healthyRate)}</b></span><span><small>TOOLS FAILED</small><b>${canaryPercent(metrics.toolFailureRate)}</b></span><span><small>ВЕРИФИКАЦИЯ</small><b>${metrics.verificationRequired ? canaryPercent(metrics.verificationRate) : 'не треб.'}</b></span></div>` : ''
    const effectText = effectLabel[item.effect || canary?.effect] || ''
    const canaryBlock = canary
      ? `<aside class="agent-learning-canary is-${esc(canary.status || 'pending')}"><header><span>CANARY / REGRESSION GATE</span><em>${esc(canaryStatusLabel[canary.status] || canary.status)}</em></header><p><b>candidate revision ${Number(canary.candidate?.revision || 0)}</b> · <code>${esc(String(canary.candidate?.digest || '').slice(0, 12))}</code>${canary.baseline ? ` · baseline revision ${Number(canary.baseline.revision || 0)} <code>${esc(String(canary.baseline.digest || '').slice(0, 12))}</code>` : ' · новый Skill без baseline'}${effectText ? ` · ${esc(effectText)}` : ''}</p>${canaryMetrics(canary.candidateMetrics)}${canary.baselineMetrics ? `<small>BASELINE: ${Number(canary.baselineMetrics.runs || 0)} runs · завершены ${canaryPercent(canary.baselineMetrics.completionRate)} · здоровые ${canaryPercent(canary.baselineMetrics.healthyRate)} · tools failed ${canaryPercent(canary.baselineMetrics.toolFailureRate)}</small>` : ''}${Array.isArray(canary.reasons) && canary.reasons.length ? `<ul>${canary.reasons.map(reason => `<li>${esc(reason)}</li>`).join('')}</ul>` : ''}</aside>`
      : (effectText ? `<aside class="agent-learning-canary"><header><span>ЭФФЕКТ</span><em>${esc(effectText)}</em></header></aside>` : '')
    const shadow = item.shadowEvaluation
    const shadowBlock = shadow
      ? `<aside class="agent-learning-shadow"><header><span>HISTORICAL BENCHMARK COMPARE</span><em>${esc(shadow.status || '')}</em></header><p>${esc(shadow.benchmarkSetName || 'без набора')} · set cases: ${Number(shadow.baselineCases || 0)} · evaluations used: ${Number(shadow.candidateCases || 0)}${shadow.passed ? ' · gate passed' : ''}</p>${Array.isArray(shadow.reasons) && shadow.reasons.length ? `<ul>${shadow.reasons.map(reason => `<li>${esc(reason)}</li>`).join('')}</ul>` : ''}</aside>`
      : ''
    const evidence = (item.evidence || []).map(line => `<li>${esc(line)}</li>`).join('')
    const mode = item.reviewMode === 'model' ? `рецензент модели · ${esc(item.model || 'модель запуска')}` : item.reviewMode === 'manual' ? 'явно подтверждено пользователем' : 'детерминированный резервный разбор'
    const appliedLike = item.status === 'applied' || item.status === 'applied_unproven' || item.status === 'applied_proven'
    const subagentReview = item.kind === 'subagent_specialization'
    const promoteAction = appliedLike && promotion === 'candidate' && (canary?.status === 'healthy' || subagentReview)
      ? `<button type="button" class="primary" data-action="promote-agent-improvement" data-id="${esc(item.id)}">${subagentReview ? 'Оставить в Blueprint родителя' : 'Продвинуть в Blueprint'}</button>` : ''
    const rollbackAction = subagentReview && appliedLike && promotion === 'candidate'
      ? `<button type="button" class="secondary" data-action="reject-agent-improvement" data-id="${esc(item.id)}">Не создавать Blueprint</button>`
      : (item.rollbackAvailable ? `<button type="button" class="secondary" data-action="rollback-agent-improvement" data-id="${esc(item.id)}">Откатить версию</button>` : '')
    const action = promoteAction || rollbackAction ? `<div>${promoteAction}${rollbackAction}</div>` : ''
    const visibleStatus = appliedLike && !item.rollbackAvailable ? 'ПРЕДЫДУЩАЯ ВЕРСИЯ' : (statusLabel[item.status] || item.status || '—')
    const title = subagentReview
      ? (item.status === 'skipped' ? 'Субагент не дал переносимой пользы' : `Мастер предлагает сохранить субагента: ${skill.name || 'специализация'}`)
      : skill.name || (item.status === 'skipped' ? (item.kind === 'curation_merge_proposed' ? 'Куратор предлагает объединить Skills' : 'Рецензент не выделил переносимый навык') : item.reviewMode === 'manual' ? 'Подтверждённый ручной урок' : 'Автономное улучшение')
    return `<article class="agent-learning-card is-${esc(item.status || 'unknown')}"><header><div><small>${esc(agentNames.get(item.projectAgentId) || item.projectAgentId || 'Агент')}</small><strong>${esc(title)}</strong></div><em>${esc(visibleStatus)}</em></header><p>${scope}${esc(kindLabel[item.kind] || '')}${item.trigger ? ` · ${esc(triggerLabel[item.trigger] || item.trigger)}` : ''}${revision ? ` · revision ${revision}` : ''}${item.status === 'applied_unproven' ? ' · это не доказанное улучшение' : ''}</p>${canaryBlock}${shadowBlock}${instructionBlock}${memoryBlock}${evidence ? `<ul>${evidence}</ul>` : ''}${item.failure ? `<small class="agent-learning-fallback">Рецензент модели недоступен: ${esc(item.failure)}. Применён безопасный резервный разбор без новых прав, памяти и постоянных инструкций.</small>` : ''}<footer><span>${mode} · ${new Date(item.updatedAt || item.createdAt).toLocaleString('ru-RU')}</span>${action}</footer></article>`
  }).join('')
  const proven = items.filter(item => item.status === 'applied_proven').length
  const unproven = items.filter(item => item.status === 'applied_unproven' || item.status === 'applied').length
  const summary = items.length
    ? `доказанных: ${proven} · без доказательства эффекта: ${unproven} · память: ${Number(stats.agentMemoriesPromoted || 0)} · инструкции: ${Number(stats.agentInstructionsPromoted || 0)}`
    : 'ожидает сложной подтверждённой траектории или разбора провала'
  return `<section class="agent-learning"><header><div><span>АВТОНОМНОЕ РАЗВИТИЕ</span><h2>Система учится на успехах и провалах</h2><p>Новая ревизия Skill получает отдельный неизменяемый ID. Число Skills не считается улучшением: смотрите долю подтверждённых ревизий и то, что показал канареечный прогон. После минимум трёх Runs в двух независимых проектах регрессионный гейт разрешает явное продвижение в Blueprint. Memory и Rules используют отдельный двухпроектный гейт. Рецензент не получает содержимое файлов или финального ответа, а новые инструменты и разрешения автоматически не выдаются.</p></div><em>${esc(summary)}</em></header>${cards || '<div class="agent-learning-empty"><strong>Пока нечего переносить в Skill, Memory или Rules</strong><p>Нужен завершённый run минимум с 5 tool-вызовами и записанной верификацией, если она требовалась — или явный разбор провала.</p></div>'}</section>`
}
function benchmarkQualityHtml(stats) {
  const sets = Array.isArray(stats.benchmarkSets) ? stats.benchmarkSets : []
  const evaluations = Array.isArray(stats.benchmarkEvaluations) ? stats.benchmarkEvaluations : []
  const percent = (value, total) => total > 0 ? `${Math.round(Number(value || 0) / Number(total) * 100)}%` : '—'
  const cards = sets.map(set => {
    const history = evaluations.filter(item => item.benchmarkSetId === set.id && item.setRevision === set.revision && item.setDigest === set.digest)
    const latest = history[0]
    const previous = history[1]
    const configurationChanged = latest && previous && (latest.cases || []).some(item => (previous.cases || []).some(before => before.caseId === item.caseId && before.configurationDigest !== item.configurationDigest))
    const regressions = latest && previous && configurationChanged
      ? (latest.cases || []).filter(item => !item.passed && (previous.cases || []).some(before => before.caseId === item.caseId && before.passed)).map(item => item.caseName)
      : []
    const comparison = latest && previous && configurationChanged
      ? `<aside class="benchmark-comparison is-${regressions.length ? 'regressed' : 'healthy'}"><strong>${esc(previous.label)} → ${esc(latest.label)}</strong><span>${regressions.length ? `REGRESSION GATE: ${regressions.length} кейс(а) ухудшились` : 'REGRESSION GATE: ранее проходившие кейсы не ухудшились'}</span>${regressions.length ? `<small>${regressions.map(esc).join(' · ')}</small>` : ''}</aside>`
      : `<aside class="benchmark-comparison"><span>${latest && previous ? 'Before/after требует различающиеся exact configuration digests.' : 'Для before/after нужны две оценки одной revision и digest набора.'}</span></aside>`
    const metrics = latest?.metrics
    const outcomes = latest?.cases?.map(item => `<li class="is-${item.passed ? 'passed' : 'failed'}"><b>${esc(item.caseName)}</b><span>${item.passed ? 'PASS' : 'FAIL'} · run <code>${esc(String(item.runId || '').slice(0, 12))}</code></span>${(item.reasons || []).map(reason => `<small>${esc(reason)}</small>`).join('')}</li>`).join('') || ''
    return `<article class="benchmark-card"><header><div><strong>${esc(set.name)}</strong><small>${esc(set.description || 'Персональный набор агента')}</small></div><em>revision ${Number(set.revision || 0)} · <code>${esc(String(set.digest || '').slice(0, 12))}</code></em></header><p>${Number(set.cases?.length || 0)} кейс(а) · ${history.length} оценок этой точной версии</p>${latest ? `<div class="benchmark-metrics"><span><small>PASS</small><b>${metrics?.passed || 0}/${metrics?.cases || 0}</b></span><span><small>ЗАВЕРШЕНЫ</small><b>${percent(metrics?.completed, metrics?.cases)}</b></span><span><small>ЗДОРОВЫЕ</small><b>${percent(metrics?.healthy, metrics?.cases)}</b></span><span><small>TOOLS FAILED</small><b>${metrics?.toolCalls ? percent(metrics.toolFailures, metrics.toolCalls) : '—'}</b></span><span><small>ВЕРИФИКАЦИЯ</small><b>${metrics?.verificationRequired ? percent(metrics.verificationRecorded, metrics.verificationRequired) : 'не треб.'}</b></span></div><small class="benchmark-label">${esc(latest.label)} · ${new Date(latest.createdAt).toLocaleString('ru-RU')}</small>${outcomes ? `<ul>${outcomes}</ul>` : ''}` : '<small>Реальные завершённые Runs ещё не сопоставлены с кейсами.</small>'}${comparison}</article>`
  }).join('')
  return `<section class="benchmark-quality"><header><div><span>ПЕРСОНАЛЬНЫЕ ПРОВЕРКИ</span><h2>Сравнение до и после на реальных Runs</h2><p>Каждая оценка закрепляет revision и SHA-256 набора, digest конфигурации запуска и точные версии Skills. Решение строится из пройденных и непройденных кейсов и исходных счётчиков — без единого синтетического рейтинга.</p></div><em>${countOf(sets.length, 'набор', 'набора', 'наборов')} · ${countOf(evaluations.length, 'оценка', 'оценки', 'оценок')}</em></header>${cards ? `<div class="benchmark-grid">${cards}</div>` : '<div class="agent-learning-empty"><strong>Наборов проверок пока нет</strong><p>Создайте персональный набор для постоянного агента и сопоставьте каждому кейсу отдельный завершённый Run.</p></div>'}</section>`
}
function skillVersionQualityHtml(stats) {
  const items = Array.isArray(stats.skillVersionStats) ? stats.skillVersionStats : []
  const percent = (value, total) => total > 0 ? `${Math.round(Number(value || 0) / Number(total) * 100)}%` : '—'
  const cards = items.slice(0, 24).map(item => {
    const toolFinished = Math.max(0, Number(item.toolCalls || 0))
    const toolSucceeded = Math.max(0, toolFinished - Number(item.toolFailures || 0))
    const version = item.revision ? `revision ${Number(item.revision)}` : 'без номера revision'
    const digest = String(item.digest || '').slice(0, 12)
    const promotion = item.promotionStatus === 'promoted' ? 'универсальный' : item.promotionStatus === 'candidate' ? 'кандидат' : 'проектный'
    const feedback = Number(item.feedbackRuns || 0) || Number(item.revisionRuns || 0) || Number(item.approvalDenied || 0)
      ? `<footer><span>коррекции: ${Number(item.feedbackRuns || 0)}</span><span>возвраты: ${Number(item.revisionRuns || 0)}</span><span>отказы: ${Number(item.approvalDenied || 0)}</span></footer>`
      : ''
    return `<article class="skill-version-evidence"><header><div><strong>${esc(item.name || item.skillId)}</strong><small>${esc(version)} · <code>${esc(digest)}</code></small></div><em class="is-${esc(item.promotionStatus || 'project_only')}">${esc(promotion)}</em></header><div><span><small>RUNS</small><b>${Number(item.runs || 0)}</b></span><span><small>ЗДОРОВЫЕ</small><b>${percent(item.healthy, item.runs)}</b></span><span><small>ЗАВЕРШЕНЫ</small><b>${percent(item.completed, item.runs)}</b></span><span><small>TOOLS</small><b>${toolFinished ? percent(toolSucceeded, toolFinished) : '—'}</b></span><span><small>ВЕРИФИКАЦИЯ</small><b>${item.verificationRequired ? percent(item.verificationSatisfied, item.verificationRequired) : 'не треб.'}</b></span></div>${feedback}<small class="skill-version-seen">Последнее наблюдение: ${item.lastSeenAt ? new Date(item.lastSeenAt).toLocaleString('ru-RU') : '—'}</small></article>`
  }).join('')
  const coverage = `${Number(stats.skillOutcomeCount || 0).toLocaleString('ru-RU')} наблюдений · ${items.length} точных версий`
  return `<section class="skill-version-quality"><header><div><span>КАЧЕСТВО SKILLS</span><h2>Результаты точных версий</h2><p>Run связывается с digest реально загруженного Skill. Это корреляция, не доказательство причинности: один запуск может использовать несколько навыков.</p></div><em>${esc(coverage)}</em></header>${cards ? `<div class="skill-version-grid">${cards}</div>` : '<div class="agent-learning-empty"><strong>Наблюдений пока нет</strong><p>Метрики появятся после завершённого Run, в снимке конфигурации которого был загружен хотя бы один Skill.</p></div>'}</section>`
}
function compatibilityLifecycleHtml(stats) {
  const items = Array.isArray(stats.compatibilityUsage) ? stats.compatibilityUsage : []
  const featureLabel = {
    legacy_profile_save: 'Сохранение через API профилей',
    legacy_profile_delete: 'Удаление через API профилей',
    legacy_profile_run_fallback: 'Запуск без ProjectAgent',
    legacy_workflow_save: 'Сохранение последовательного Workflow',
    legacy_workflow_delete: 'Удаление последовательного Workflow',
    legacy_workflow_run: 'Запуск последовательного Workflow',
    legacy_custom_command_save: 'Сохранение составной shell-команды',
    legacy_custom_command_execute: 'Выполнение составной shell-команды',
    legacy_run_snapshot_read: 'Открытие истории Run старого формата',
  }
  const cards = items.map(item => `<article class="benchmark-card"><header><div><strong>${esc(featureLabel[item.feature] || item.feature)}</strong><small>${item.workspaceId ? 'текущий проект' : 'общая библиотека'}</small></div><em>${Number(item.count || 0).toLocaleString('ru-RU')} обращений</em></header><p>Point ${esc(item.applicationVersion || '—')} · формат ${esc(item.legacyVersion || '—')}</p><small class="benchmark-label">первое: ${item.firstSeen ? new Date(item.firstSeen).toLocaleString('ru-RU') : '—'} · последнее: ${item.lastSeen ? new Date(item.lastSeen).toLocaleString('ru-RU') : '—'}</small></article>`).join('')
  return `<section class="benchmark-quality compatibility-lifecycle"><header><div><span>ЖИЗНЕННЫЙ ЦИКЛ СОВМЕСТИМОСТИ</span><h2>Фактическое использование старых путей</h2><p>Счётчики не содержат путей, промптов, аргументов, имён агентов или секретов. Данные разделены по точной версии Point и версии старого формата; единого условного рейтинга нет.</p></div><em>${Number(stats.compatibilityUsageTotal || 0).toLocaleString('ru-RU')} обращений · ${Number(stats.compatibilityReleaseVersions || 0)} версий</em></header><aside class="statistics-quality-method"><strong>Критерий удаления</strong><p>Ноль обращений в двух последовательных релизных окнах, успешная миграция на копиях реальных баз данных, подтверждённые резервное копирование и восстановление, а также сквозная проверка заменяющего сценария. Отсутствие записи в одной локальной базе доказательством не считается.</p></aside>${cards ? `<div class="benchmark-grid">${cards}</div>` : '<div class="agent-learning-empty"><strong>Обращений в этой базе пока не зафиксировано</strong><p>Это локальное наблюдение, а не разрешение удалить слой совместимости.</p></div>'}</section>`
}
function systemHealthHtml(report) {
  if (!report || !Array.isArray(report.checks)) return '<section class="benchmark-quality"><header><div><span>СОСТОЯНИЕ СИСТЕМЫ</span><h2>Состояние ещё не измерено</h2><p>Обновите статистику, чтобы проверить ядро Point, SQLite, диск, изоляцию, подключение модели, права проекта, порт, резервную копию и миграцию.</p></div><em>НЕИЗВЕСТНО</em></header></section>'
  const statusLabel = { READY: 'ГОТОВО', DEGRADED: 'ОГРАНИЧЕНО', BLOCKED: 'ЗАБЛОКИРОВАНО' }
  const checkLabel = {
    point_core: 'Point Core', sqlite: 'SQLite', migration: 'Миграция', disk_space: 'Диск', sandbox: 'Sandbox',
    workspace_permissions: 'Workspace', core_port: 'Порт Core', backup: 'Backup', provider_model: 'Provider / модель',
  }
  const metricText = check => {
    const metrics = check.metrics || {}
    if (check.code === 'disk_space' && Number.isFinite(Number(metrics.availableBytes))) return `${(Number(metrics.availableBytes) / 1024 / 1024).toFixed(0)} MB свободно`
    if (check.code === 'migration') return `версия ${Number(metrics.version || 0)} / ${Number(metrics.expectedVersion || 0)}`
    if (check.code === 'sqlite') return `${esc(metrics.integrity || '—')} · FK: ${Number(metrics.foreignKeyViolations || 0)}`
    if (check.code === 'sandbox') return [metrics.backend, metrics.version, metrics.apiVersion ? `API ${metrics.apiVersion}` : '', String(metrics.imageDigest || '').slice(0, 19)].filter(Boolean).map(esc).join(' · ')
    if (check.code === 'backup' && metrics.createdAt) return `${new Date(metrics.createdAt).toLocaleString('ru-RU')} · ${(Number(metrics.sizeBytes || 0) / 1024 / 1024).toFixed(1)} MB · ${esc(String(metrics.sha256 || '').slice(0, 16))}`
    if (check.code === 'provider_model') return `профилей: ${Number(metrics.configuredProfiles || 0)} · подключений: ${Number(metrics.providers || 0)} · моделей: ${Number(metrics.models || 0)}`
    if (check.code === 'core_port' && metrics.port) return `порт ${esc(metrics.port)}`
    if (check.code === 'point_core' && metrics.version) return `Point ${esc(metrics.version)}`
    return ''
  }
  const cards = report.checks.map(check => `<article class="benchmark-card is-${esc(String(check.status || '').toLowerCase())}"><header><div><strong>${esc(checkLabel[check.code] || check.code)}</strong><small>${esc(check.summary || 'Причина не указана')}</small></div><em>${esc(statusLabel[check.status] || check.status || 'BLOCKED')}</em></header>${metricText(check) ? `<p>${metricText(check)}</p>` : ''}${check.detail ? `<small>${esc(check.detail)}</small>` : ''}${check.nextAction ? `<aside class="statistics-quality-method"><strong>Следующее безопасное действие</strong><p>${esc(check.nextAction)}</p></aside>` : ''}</article>`).join('')
  const reasons = Array.isArray(report.blockingReasons) && report.blockingReasons.length ? `<aside class="statistics-warnings">${report.blockingReasons.map(reason => `<p>⚠ ${esc(reason)}</p>`).join('')}</aside>` : ''
  const canCreateBackup = report.checks.some(check => Array.isArray(check.repairActions) && check.repairActions.some(action => action.id === 'create_backup'))
  const canRestoreBackup = report.checks.some(check => Array.isArray(check.repairActions) && check.repairActions.some(action => action.id === 'restore_database'))
  const backupAction = canCreateBackup ? '<button type="button" class="secondary" data-action="create-system-backup">Создать проверенный backup</button>' : ''
  const restoreAction = canRestoreBackup ? '<button type="button" class="secondary" data-action="restore-system-backup">Восстановить из backup</button>' : ''
  return `<section class="benchmark-quality system-health"><header><div><span>СОСТОЯНИЕ СИСТЕМЫ</span><h2>${esc(statusLabel[report.status] || report.status || 'BLOCKED')}</h2><p>Единый жизненный цикл строится из исходных проверок. BLOCKED запрещает запуск; DEGRADED оставляет только безопасную ограниченную работу.</p></div><em>${esc(report.status || 'BLOCKED')}</em></header>${reasons}<div class="benchmark-grid">${cards}</div><footer>${backupAction}${restoreAction}<small class="benchmark-label">Проверено: ${report.checkedAt ? new Date(report.checkedAt).toLocaleString('ru-RU') : '—'}</small></footer></section>`
}
function statisticsView() {
  let statisticsStatus = getStatisticsStatus()
  if (statisticsStatus === 'idle') {
    statisticsStatus = 'loading'
    setStatisticsStatus('loading')
    setTimeout(() => postMessage({ type: 'loadStatistics' }), 0)
  }
  const statisticsData = getStatisticsData()
  const stats = statisticsData || {}
  const warnings = [stats.dailyBudgetWarning, stats.monthlyBudgetWarning].filter(Boolean)
  if (stats.qualityDiagnosticsUnavailable) warnings.push(`Не удалось разобрать ${countOf(stats.qualityDiagnosticsUnavailable, 'запуск', 'запуска', 'запусков')}. Покрытие качества ниже полного.`)
  if (stats.qualityHistoryWindowLimited) warnings.push(`Метрики качества ограничены последними ${Number(stats.qualityHistoryLimit || 400).toLocaleString('ru-RU')} запусками проекта.`)
  if (stats.skillOutcomeHistoryLimited) warnings.push(`Метрики Skills ограничены последними ${Number(stats.skillOutcomeHistoryLimit || 1000).toLocaleString('ru-RU')} наблюдениями проекта.`)
  const breakdowns = [
    budgetPhaseSummaryHtml(stats),
    statisticsBreakdownHtml('Агенты', stats.agentStats),
    statisticsBreakdownHtml('Команды', stats.teamStats),
    statisticsBreakdownHtml('Квесты', stats.questStats),
    statisticsBreakdownHtml('Flows', stats.flowStats),
    statisticsBreakdownHtml('Модели', stats.modelStats),
    statisticsBreakdownHtml('Провайдеры', stats.providerStats),
  ].join('')
  const back = isStatisticsView()
    ? `<button type="button" class="secondary" data-action="focus-hub">← Гильдия</button>`
    : `<button type="button" class="secondary" data-action="tab" data-tab="overview">← Обзор</button>`
  return shell(`<main class="hub-statistics-page"><header class="changes-heading"><div><span>СТАТИСТИКА</span><h1>Саморазвитие, качество и AI-расходы</h1><p>Версии навыков, фактические запуски, проверяемые сигналы качества, время, токены и стоимость по всем уровням Hub.</p></div><button type="button" class="secondary" data-action="reload-statistics">Обновить</button></header><section class="hub-stat-grid wide"><span><small>АГЕНТЫ</small><b>${esc(stats.agents ?? '—')}</b></span><span><small>КВЕСТЫ</small><b>${esc(stats.quests ?? '—')}</b></span><span><small>ЗАПИСИ</small><b>${esc(stats.usageCount ?? '—')}</b></span><span><small>ТОКЕНЫ</small><b>${Number(stats.totalTokens || 0).toLocaleString('ru-RU')}</b></span><span><small>ИЗВЕСТНЫЙ РАСХОД</small><b>${stats.knownCostCents != null ? formatCents(stats.knownCostCents) : '—'}</b></span><span><small>ДЕНЬ</small><b>${stats.dailyCostCents != null ? formatCents(stats.dailyCostCents) : '—'}</b></span><span><small>МЕСЯЦ</small><b>${stats.monthlyCostCents != null ? formatCents(stats.monthlyCostCents) : '—'}</b></span><span><small>БЮДЖЕТ / ДЕНЬ</small><b>${stats.budgetDailyCents ? formatCents(stats.budgetDailyCents) : '—'}</b></span></section>${statisticsStatus === 'loading' ? '<div class="hall-strip is-quiet"><i></i><span>Спрашиваю ядро — числа ниже ещё не пришли.</span></div>' : statisticsStatus === 'error' && !statisticsData ? '<div class="hall-strip"><i></i><span>Не удалось получить статистику от ядра: числа ниже неизвестны, а не нулевые. Нажмите «Обновить».</span></div>' : statisticsStatus === 'error' ? '<div class="hall-strip"><i></i><span>Обновить не удалось: показаны числа последнего успешного запроса, свежее могло не попасть.</span></div>' : ''}${warnings.length ? `<aside class="statistics-warnings">${warnings.map(item => `<p>⚠ ${esc(item)}</p>`).join('')}</aside>` : ''}${systemHealthHtml(stats.systemHealth)}<aside class="statistics-quality-method"><strong>Как читается качество</strong><p>Hub не выводит условный единый рейтинг. Он показывает покрытие сохранённой диагностикой, долю здоровых прогонов, выполнение обязательной верификации и надёжность tools. Нет доказательства — нет положительной оценки.</p><span>Разобрано запусков: ${Number(stats.qualityRunsAnalyzed || 0).toLocaleString('ru-RU')}</span></aside>${benchmarkQualityHtml(stats)}${compatibilityLifecycleHtml(stats)}${autonomousLearningHtml(stats)}${skillVersionQualityHtml(stats)}${budgetSettingsHtml(stats)}${breakdowns}<footer class="hub-transitional" aria-label="Смежные разделы"><b>ПЕРЕЙТИ</b>${back}</footer></main>`)
}

  return { statisticsView }
}
