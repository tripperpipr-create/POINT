import { masterPlanHtml, masterPlanState } from './master-plan-views.js'
import { formatElapsed, list } from './format-units.js'
import { stageFlowNode, stageLabel, stageLabelText } from './stage-labels.js'
import { stageFailureHtml } from './stage-failure-views.js'

// Экран выполнения утверждённого наряда.
//
// Наряд утверждали — и дальше была тишина: карточка говорила «План создан,
// выполнение началось» и замолкала на девять минут, пока узел Flow стоял с
// непрочитанной причиной отказа в nodeStates. Ни ошибки, ни хода работы, ни
// того, что под наряд создали агента.
//
// Экран собран из готовых частей: перечень этапов — та же ячейка плана, что и в
// ленте разговора, журнал — журнал этапа (quest-journal-views.js), который
// отдаёт agentWorkTranscriptHtml по просьбе `journal`. Новое здесь одно:
// причина затыка, которой раньше не было нигде, и источник данных — сам наряд.
// Наблюдатель опрашивает его раз в 2.5 с, поэтому этапы приходят в
// runtime.stages, а не отдельным запросом /api/state/runtime.
//
// Строку прогона и порядок частей собирает quest-run-views.js: здесь только
// сами части, чтобы их можно было ставить в разном порядке у идущего и у
// законченного квеста.

// Пометка этапа, которую перечень плана рисует справа. Совпадает по смыслу с
// STAGE_NOTE ленты разговора: состояние узла, а не его название.
// Причина ожидания точнее состояния узла: waiting_agent говорит «не запущен», а
// человеку нужно знать, чего именно ждут — ключа, авторизации, его решения.
const STAGE_NOTE = {
  start_failed: 'не запустился',
  waiting_api_key: 'нужен ключ',
  waiting_interactive_cursor: 'нужна авторизация CLI',
  sandbox_merge_conflict: 'расхождение в песочнице',
  waiting: 'нужно решение',
  waiting_approval: 'нужно решение',
  waiting_agent: 'не запущен',
  failed: 'ошибка',
  cancelled: 'отменён',
  skipped: 'пропущен',
}

// Почему узел стоит. Текст waitReason — внутреннее слово ядра, и человеку оно
// ничего не говорит; причину надо назвать по-русски.
const STALL_REASON = {
  stage_failed: 'Этап завершился ошибкой; изменения не доставлены',
  start_failed: 'Исполнитель не запустился',
  waiting_api_key: 'Нужен ключ подключения',
  waiting_interactive_cursor: 'Нужна активная авторизация CLI',
  sandbox_merge_conflict: 'Расхождение в песочнице',
}

// Пометка этапа по-русски: причина ожидания точнее состояния узла.
export function workOrderStageNote(stage) {
  return STAGE_NOTE[stage?.waitReason] || STAGE_NOTE[stage?.status] || ''
}

const LAUNCH_PHASE = { preflight: 0, planning: 1, compiling: 2, launching: 3 }
const LAUNCH_STEPS = ['Проверка окружения', 'Планирование', 'Сборка Flow', 'Запуск исполнителя']

function launchPlan(runtime, esc) {
  if (runtime?.status !== 'preflight' || !runtime?.launchPhase) return ''
  const current = LAUNCH_PHASE[runtime.launchPhase] ?? 0
  return masterPlanHtml('Запуск плана', LAUNCH_STEPS.map((text, index) => ({
    text,
    state: index < current ? 'done' : index === current ? 'now' : 'wait',
    note: index === current ? String(runtime.message || 'выполняется') : '',
  })), esc, { limit: 4 })
}

// Идёт ли этап прямо сейчас. Хронику показываем по нему: у завершённых прогонов
// своя история, и подменять ею текущую работу нельзя.
export function activeStage(stages) {
  return stages.find(stage => stage.status === 'running' || stage.status === 'waiting' || stage.status === 'waiting_approval')
    || stages.find(stage => stage.waitReason)
    || stages.filter(stage => stage.runId).at(-1)
    || null
}

// Сколько шёл этап: от старта до конца, у идущего — до сейчас.
export function stageSpan(stage, now = Date.now()) {
  const start = Date.parse(stage?.startedAt || '')
  if (Number.isNaN(start)) return null
  const end = Date.parse(stage?.finishedAt || '')
  if (!Number.isNaN(end)) return Math.max(0, end - start)
  return masterPlanState(stage?.status) === 'now' ? Math.max(0, now - start) : null
}

// Ряды перечня этапов: русское имя, английское имя модели и длительность —
// одной тихой пометкой справа.
export function workOrderStageRows(order, ui, deps = {}) {
  const runtime = order?.runtime || {}
  return list(runtime.stages).map(stage => {
    const { label, detail } = stageLabel(stage, { node: stageFlowNode(ui?.state?.boot, runtime, stage), kindLabels: deps.flowNodeKindLabels || {} })
    const span = stageSpan(stage)
    return {
      text: label,
      // Сорвавшийся этап — крестом, а не пустым квадратом ждущего: по
      // перечню ищут именно его.
      state: ['failed', 'cancelled'].includes(stage.status) || ['start_failed', 'stage_failed'].includes(stage.waitReason) ? 'fail' : masterPlanState(stage.status),
      // Этап короче секунды (вход, выход) длительности не носит: «1 с» у
      // мгновенного узла была бы округлённой неправдой.
      note: [workOrderStageNote(stage), detail, span >= 1000 ? formatElapsed(span, { seconds: true }) : ''].filter(Boolean).join(' · '),
    }
  })
}

// Хроника того прогона, который сейчас идёт. Раньше путь v2 не звал ни
// coordinateActiveFlows, ни loadRun, поэтому state.details оставался чужим или
// пустым — и брать его без сверки значило показать хронику другого квеста.
function transcriptFor(order, ui, deps) {
  const details = ui?.state?.details
  if (!details?.run || typeof deps.agentWorkTranscriptHtml !== 'function') return ''
  const runtime = order.runtime || {}
  const stage = activeStage(list(runtime.stages))
  if (!stage?.runId || details.run.id !== stage.runId) return ''
  return deps.agentWorkTranscriptHtml(details, { limit: 80, compact: true, sandboxOnly: true, journal: true })
}

// Что написать вместо хроники, когда её нет.
//
// «Загружаем хронику работы агента…» висело вечно у работы, которая не
// начиналась: грузить было нечего, и строка обещала то, чего не будет.
// Загрузка идёт только когда есть запущенный прогон.
function emptyTranscriptText(stall, stages) {
  if (stall && !stages.some(stage => stage.runId)) return 'Работа не начиналась: агент ждёт.'
  if (stall) return 'Работа остановлена; результат агента остался в изоляции.'
  if (!stages.some(stage => stage.runId)) return 'Работа ещё не начиналась.'
  return 'Загружаем журнал работы агента…'
}

// Экранирование приходит снаружи и обязано прийти. Фолбэк-«тождество»
// выглядел безобидной осторожностью, а был открытой дверью: в stall.error
// попадает текст от модели и от ядра, и без esc он уехал бы в разметку как
// есть. Отсутствие esc — ошибка вызывающего, и она должна быть слышна сразу,
// а не превращаться в инъекцию у того, кто откроет карточку наряда.
export function requireEsc(deps) {
  if (typeof deps.esc !== 'function') {
    throw new Error('work-order-execution-views: не передан esc — экранировать текст ядра нечем')
  }
  return deps.esc
}

// Части экрана выполнения по отдельности: что стоит, из чего работа состоит и
// что агент уже сделал.
export function workOrderExecutionParts(order, ui, deps = {}) {
  const runtime = order?.runtime
  if (!runtime) return null
  const esc = requireEsc(deps)
  const stages = list(runtime.stages)
  const stall = runtime.stall
  const stallTitle = stall ? (STALL_REASON[stall.waitReason] || 'Выполнение остановлено') : ''
  const stallStage = stall ? stages.find(stage => stage.id === stall.nodeId) || { id: stall.nodeId, name: stall.nodeName } : null
  const stallName = stallStage ? stageLabelText(stallStage, { node: stageFlowNode(ui?.state?.boot, runtime, stallStage), kindLabels: deps.flowNodeKindLabels || {} }) : ''
  // Причина затыка — блок внимания, а не строка в подвале. Именно сюда попадает
  // `model … is not in connection … catalog`, которое девять минут жило только
  // в nodeStates.
  const stallHtml = stall ? `<div class="work-order-exec-stall">
      <b>${esc(stallTitle)}</b>
      ${stall.nodeName || stall.nodeId ? `<small>Этап «${esc(stallName || stall.nodeName || stall.nodeId)}»</small>` : ''}
      ${stall.error && !runtime.stageFailure?.diagnosis?.checks?.length ? `<p>${esc(stall.error)}</p>` : ''}
      ${stall.waitReason === 'stage_failed' ? stageFailureHtml(order, runtime, esc) : ''}
    </div>` : ''
  const plan = launchPlan(runtime, esc) || masterPlanHtml('Этапы', workOrderStageRows(order, ui, deps), esc, { limit: 12 })
  const transcript = transcriptFor(order, ui, deps)
  const provisioning = ['runtime_provisioning', 'runtime_building'].includes(runtime.launchPhase)
    && !['completed', 'needs_review', 'blocked', 'failed', 'cancelled'].includes(runtime.status)
    ? `<small class="work-order-exec-note">${esc(runtime.message || 'Подготавливаем инструменты sandbox')}</small>` : ''
  // Примечание планировщика: план мог собрать движок Point, а не модель. Без
  // этой строки человек читает шаблонный план как ответ модели.
  const plannerNote = runtime.plannerNote ? `<small class="work-order-exec-note">${esc(runtime.plannerNote)}</small>` : ''
  const log = transcript ? `<div class="work-order-exec-log">${transcript}</div>`
    : `<div class="work-order-exec-empty"><span>${esc(emptyTranscriptText(stall, stages))}</span></div>`
  return { stall: stallHtml, plan, log, hasLog: Boolean(transcript), provisioning, plannerNote }
}

// Один экран: что стоит, из чего работа состоит и что агент уже сделал.
// Собранный целиком — для мест, где порядок частей не спорный.
export function workOrderExecutionHtml(order, ui, deps = {}) {
  const parts = workOrderExecutionParts(order, ui, deps)
  if (!parts) return ''
  const esc = requireEsc(deps)
  return `<section class="work-order-exec" data-work-order-execution="${esc(order.id)}" data-quest-id="${esc(order.runtime.questId || '')}">
      ${parts.stall}
      ${deps.controlsHtml || ''}
      ${parts.provisioning}
      ${parts.plan}
      ${parts.log}
      ${parts.plannerNote}
    </section>`
}
