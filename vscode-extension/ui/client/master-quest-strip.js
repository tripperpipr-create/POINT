// Полоса идущего квеста над полем ввода.
//
// Состояние запущенной работы жило в карточке ленты и уезжало вверх с каждым
// следующим ходом: чтобы узнать, на каком этапе квест и кто им занят, надо было
// листать разговор назад. Полоса стоит в карточке ввода, то есть там, куда
// человек смотрит всё время, и отвечает на два вопроса — где работа и кто её
// делает. Подробности открываются по нажатию в панели квеста.
//
// Источник — наряд v2: наблюдатель опрашивает его раз в 2.5 с, и этапы с
// исполнителями приходят в runtime.stages. Подписи состояний не свои: их даёт
// runtimePresentation карточки наряда, иначе полоса и карточка однажды назвали
// бы одно состояние по-разному. Прежний маршрут квестов (legacy) полосы не
// получает: он остаётся только при явном выборе старой базы.

import { runtimePresentation } from './master-work-order-v2.js'
import { activeStage, workOrderStageNote } from './work-order-execution-views.js'
import { fillAttribute, list } from './format-units.js'
import { icon } from './ui-icons.js'

// Когда полоса нужна: работа идёт или стоит и ждёт человека. Закончившийся
// квест полосы не держит — его итог стоит в ленте.
const STRIP_STATUSES = new Set(['preflight', 'running', 'verifying', 'applying', 'awaiting_user', 'paused', 'blocked', 'needs_review'])
const LIVE_STATUSES = new Set(['preflight', 'running', 'verifying', 'applying'])
const WORKING_STAGES = new Set(['running', 'waiting', 'waiting_approval'])

// Модель полосы. Читает только то, что пришло от ядра; ничего не хранит.
export function masterQuestStripModel({ workOrders, agentName = () => '' } = {}) {
  const candidates = list(workOrders).filter(order =>
    order?.state === 'approved' && STRIP_STATUSES.has(String(order?.runtime?.status || '')))
  if (!candidates.length) return null
  // Разговор может держать несколько нарядов; полоса говорит о последнем
  // тронутом ядром — он и есть «текущая работа».
  const order = [...candidates].sort((a, b) =>
    String(b.runtime?.updatedAt || '').localeCompare(String(a.runtime?.updatedAt || '')))[0]
  const runtime = order.runtime
  const status = String(runtime.status)
  const stages = list(runtime.stages)
  const done = stages.filter(stage => stage.status === 'completed' || stage.status === 'skipped').length
  const presentation = runtimePresentation(runtime)
  const current = activeStage(stages)
  const working = stages.filter(stage => WORKING_STAGES.has(stage.status))
  // Имя исполнителя — из ростера проекта, затем из состава самого наряда:
  // агент, созданный утверждением, в ростере появляется не сразу. Этап без
  // агента у наряда с одним исполнителем принадлежит ему.
  const roster = [...list(order.roster?.permanent), ...list(order.roster?.temporary)]
  const nameOf = id => (id && (agentName(id) || roster.find(item => item?.id === id)?.name))
    || (roster.length === 1 ? roster[0]?.name : '')
    || 'Исполнитель'
  const workers = (working.length ? working : current ? [current] : []).map(stage => ({
    name: nameOf(stage.agentId),
    stage: String(stage.name || ''),
    note: workOrderStageNote(stage),
  }))
  return {
    id: String(order.id || ''),
    status,
    live: LIVE_STATUSES.has(status),
    tone: presentation.tone,
    label: presentation.label,
    goal: String(order.goal || ''),
    done,
    total: stages.length,
    stage: current ? String(current.name || '') : '',
    workers,
  }
}

// Одна строка: состояние, цель, этап со шкалой, исполнители. Всё, что не
// помещается, обрезается многоточием и остаётся в подсказке.
export function masterQuestStripHtml(model, esc) {
  if (!model) return ''
  const share = model.total ? Math.round((model.done / model.total) * 100) : 0
  const progress = model.total
    ? `<span class="hall-quest-strip-stage">этап ${Math.min(model.done + (model.live ? 1 : 0), model.total)}/${model.total}</span><span class="hall-quest-bar is-sm" role="img" aria-label="Этапы: ${model.done} из ${model.total}"><span ${fillAttribute(share)}></span></span>`
    : ''
  const workers = model.workers.map(worker => {
    const what = [worker.stage, worker.note].filter(Boolean).join(' · ')
    return `<span class="hall-quest-strip-worker">${icon('person')}<b>${esc(worker.name)}</b>${what ? `<span>${esc(what)}</span>` : ''}</span>`
  }).join('')
  const hint = [model.label, model.goal, model.stage].filter(Boolean).join(' · ')
  return `<button type="button" class="hall-quest-strip ${esc(model.tone)}${model.live ? ' is-live' : ''}" data-action="master-inspector-open" data-tab="quest" data-order="${esc(model.id)}" title="${esc(hint)} — открыть квест">
    <span class="hall-quest-strip-state"><i aria-hidden="true"></i>${esc(model.label)}</span>
    <span class="hall-quest-strip-goal">${esc(model.goal)}</span>
    ${progress}
    ${workers}
    <span class="hall-quest-strip-open">${icon('panel-right')}</span>
  </button>`
}
