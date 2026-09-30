// «Что Point вынес из квеста»: разбор завершённого квеста.
//
// Разбор собирает ядро (GET /api/v2/master/quests/{id}/retrospective) из уже
// записанных данных, без модели: наблюдение, откуда оно, что из него выросло
// и чем кончилась проверка выросшего. Блок стоит в карточке квеста рядом с
// ручной приёмкой и грузится по кнопке — отрисовка карточки запросов не шлёт.
//
// Состояние лежит в общем мешке `ui` по идентификатору квеста:
// `ui.questRetrospectives[questId] = { status, data, error }`.

// Те же статусы, что считает завершёнными ядро: domain.IsTerminalQuestStatus.
const TERMINAL = new Set(['completed', 'needs_review', 'blocked', 'failed', 'cancelled'])

const SOURCE_LABELS = { master: 'Мастер', human: 'Человек', gate: 'Проверка', agent: 'Исполнитель' }
const JOB_LABELS = {
  queued: 'в очереди', running: 'идёт проверка', deferred: 'отложено', rejected: 'кандидат отклонён',
  canary: 'пробное применение', local: 'подтверждено в проекте', shared: 'общий навык', rolled_back: 'откат',
}
const IMPROVEMENT_LABELS = {
  applying: 'применяется', applied: 'применено', applied_unproven: 'применено, польза не доказана',
  applied_proven: 'польза доказана', skipped: 'не выучено', failed: 'сбой применения', rolled_back: 'откачено',
}
const CANARY_LABELS = { pending: 'наблюдение', healthy: 'без регрессии', regressed: 'регрессия' }

const list = value => Array.isArray(value) ? value : []

function retrospectiveState(ui, questId) {
  return ui?.questRetrospectives?.[questId]
}

export function questRetrospectiveHtml(order, ui, esc) {
  const questId = String(order?.runtime?.questId || '')
  if (!questId || !TERMINAL.has(String(order.runtime.status || ''))) return ''
  const state = retrospectiveState(ui, questId)
  const load = label => `<button type="button" class="hall-chip" data-action="load-quest-retrospective" data-quest-id="${esc(questId)}"${state?.status === 'loading' ? ' disabled' : ''}>${label}</button>`
  const head = `<b>Что Point вынес из квеста</b>`
  if (!state || state.status === 'loading') {
    return `<section class="quest-retrospective" aria-label="Что Point вынес из квеста">${head}${load(state ? 'Собираем разбор…' : 'Показать разбор')}</section>`
  }
  if (state.status === 'error') {
    return `<section class="quest-retrospective" aria-label="Что Point вынес из квеста">${head}<p role="alert">${esc(state.error || 'Разбор недоступен')}</p>${load('Повторить')}</section>`
  }
  const data = state.data || {}
  const observations = list(data.observations)
  const master = list(data.masterLearning)
  const agents = list(data.agentLearning)
  if (!observations.length && !master.length && !agents.length) {
    return `<section class="quest-retrospective" aria-label="Что Point вынес из квеста">${head}<p>Ни дефектов, ни уроков по этому квесту не записано.</p>${load('Обновить')}</section>`
  }
  const observationRows = observations.map(item => `<li><span class="quest-retro-source">${esc(SOURCE_LABELS[item.source] || item.source || '')}</span> <span>${esc([item.kind, item.outcome].filter(Boolean).join(' · '))}</span>${item.detail ? `<small>${esc(item.detail)}</small>` : ''}</li>`).join('')
  const masterRows = master.map(item => `<li><span>Методика ${esc(item.skillId || '')} · ${esc(item.phase || '')}</span> <span class="quest-retro-status">${esc(JOB_LABELS[item.status] || item.status || '')}</span>${item.reason ? `<small>${esc(item.reason)}</small>` : ''}</li>`).join('')
  const agentRows = agents.map(item => {
    const name = item.skillName || item.instruction || item.kind || 'Улучшение'
    const canary = item.canary ? ` · проверка: ${esc(CANARY_LABELS[item.canary] || item.canary)}` : ''
    const reasons = list(item.canaryReasons).slice(0, 2).map(reason => `<small>${esc(reason)}</small>`).join('')
    const rollback = item.rollbackAvailable ? `<button type="button" class="hall-chip" data-action="rollback-agent-improvement" data-id="${esc(item.id)}">Откатить</button>` : ''
    return `<li><span>${esc(name)}</span> <span class="quest-retro-status">${esc(IMPROVEMENT_LABELS[item.status] || item.status || '')}${canary}</span>${item.failure ? `<small>${esc(item.failure)}</small>` : ''}${reasons}${rollback}</li>`
  }).join('')
  return `<section class="quest-retrospective" aria-label="Что Point вынес из квеста">${head}
    ${observationRows ? `<div><small class="quest-retro-rubric">Наблюдения</small><ul>${observationRows}</ul></div>` : ''}
    ${masterRows ? `<div><small class="quest-retro-rubric">Обучение Мастера</small><ul>${masterRows}</ul></div>` : ''}
    ${agentRows ? `<div><small class="quest-retro-rubric">Обучение исполнителей</small><ul>${agentRows}</ul></div>` : ''}
    ${load('Обновить')}</section>`
}

// Запрос уходит один раз на квест, пока предыдущий не ответил.
export function requestQuestRetrospective(target, ui, vscode, render) {
  const questId = String(target?.dataset?.questId || '')
  if (!questId) return true
  ui.questRetrospectives ||= {}
  if (ui.questRetrospectives[questId]?.status === 'loading') return true
  ui.questRetrospectives[questId] = { ...ui.questRetrospectives[questId], status: 'loading' }
  vscode.postMessage({ type: 'loadQuestRetrospective', questId })
  render()
  return true
}

export function applyQuestRetrospectiveMessage(message, ui, render) {
  if (message?.type !== 'questRetrospective' && message?.type !== 'questRetrospectiveError') return false
  const questId = String(message.questId || '')
  if (!questId) return true
  ui.questRetrospectives ||= {}
  ui.questRetrospectives[questId] = message.type === 'questRetrospective'
    ? { status: 'ready', data: message.retrospective || {} }
    : { status: 'error', error: String(message.error || 'Разбор недоступен') }
  render()
  return true
}
