import { masterPlanHtml, masterPlanState } from './master-plan-views.js'

// Экран выполнения утверждённого наряда.
//
// Наряд утверждали — и дальше была тишина: карточка говорила «План создан,
// выполнение началось» и замолкала на девять минут, пока узел Flow стоял с
// непрочитанной причиной отказа в nodeStates. Ни ошибки, ни хода работы, ни
// того, что под наряд создали агента.
//
// Экран собран из готовых частей: перечень этапов — та же ячейка плана, что и в
// ленте разговора, хроника — тот же agentWorkTranscriptHtml, что у прогона
// агента. Новое здесь одно: причина затыка, которой раньше не было нигде, и
// источник данных — сам наряд. Наблюдатель опрашивает его раз в 2.5 с, поэтому
// этапы приходят в runtime.stages, а не отдельным запросом /api/state/runtime.
//
// Модуль отдельный, потому что main.js стоит у границы своего бюджета строк
// (scripts/check-release-contracts.mjs), и расти там нельзя.

const list = value => Array.isArray(value) ? value : []

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
  start_failed: 'Исполнитель не запустился',
  waiting_api_key: 'Нужен ключ подключения',
  waiting_interactive_cursor: 'Нужна активная авторизация CLI',
  sandbox_merge_conflict: 'Расхождение в песочнице',
}

// Идёт ли этап прямо сейчас. Хронику показываем по нему: у завершённых прогонов
// своя история, и подменять ею текущую работу нельзя.
function activeStage(stages) {
  return stages.find(stage => stage.status === 'running' || stage.status === 'waiting' || stage.status === 'waiting_approval')
    || stages.find(stage => stage.waitReason)
    || stages.filter(stage => stage.runId).at(-1)
    || null
}

// Хроника того прогона, который сейчас идёт. Раньше путь v2 не звал ни
// coordinateActiveFlows, ни loadRun, поэтому state.details оставался чужим или
// пустым — и брать его без сверки значило показать хронику другого квеста.
function transcriptFor(order, ui, deps) {
  const details = ui?.state?.details
  if (!details?.run) return ''
  const runtime = order.runtime || {}
  const stage = activeStage(list(runtime.stages))
  const matches = (stage?.runId && details.run.id === stage.runId)
    || (runtime.questId && details.run.questId === runtime.questId)
  if (!matches) return ''
  return deps.agentWorkTranscriptHtml(details, { limit: 80, compact: true })
}

// Что написать вместо хроники, когда её нет.
//
// «Загружаем хронику работы агента…» висело вечно у работы, которая не
// начиналась: грузить было нечего, и строка обещала то, чего не будет.
// Загрузка идёт только когда есть запущенный прогон.
function emptyTranscriptText(stall, stages) {
  if (stall) return 'Работа не начиналась: агент ждёт.'
  if (!stages.some(stage => stage.runId)) return 'Работа ещё не начиналась.'
  return 'Загружаем хронику работы агента…'
}

// Один экран: что стоит, из чего работа состоит и что агент уже сделал.
//
// `deps` — те же зависимости, что у остальных вынесенных видов: esc, хроника,
// названия видов узлов и разметка управления, общая с карточкой наряда.
// Экранирование приходит снаружи и обязано прийти. Фолбэк-«тождество»
// выглядел безобидной осторожностью, а был открытой дверью: в stall.error
// попадает текст от модели и от ядра, и без esc он уехал бы в разметку как
// есть. Отсутствие esc — ошибка вызывающего, и она должна быть слышна сразу,
// а не превращаться в инъекцию у того, кто откроет карточку наряда.
function requireEsc(deps) {
  if (typeof deps.esc !== 'function') {
    throw new Error('work-order-execution-views: не передан esc — экранировать текст ядра нечем')
  }
  return deps.esc
}

export function workOrderExecutionHtml(order, ui, deps = {}) {
  const runtime = order?.runtime
  if (!runtime) return ''
  const esc = requireEsc(deps)
  const stages = list(runtime.stages)
  const stall = runtime.stall
  const stallTitle = stall ? (STALL_REASON[stall.waitReason] || 'Выполнение остановлено') : ''
  // Причина затыка — блок внимания, а не строка в подвале. Именно сюда попадает
  // `model … is not in connection … catalog`, которое девять минут жило только
  // в nodeStates.
  const stallHtml = stall ? `<div class="work-order-exec-stall">
      <b>${esc(stallTitle)}</b>
      ${stall.nodeName || stall.nodeId ? `<small>Этап «${esc(stall.nodeName || stall.nodeId)}»</small>` : ''}
      ${stall.error ? `<p>${esc(stall.error)}</p>` : ''}
      ${/* Кнопка только там, где ядро её примет. Мёртвая кнопка «Повторить
           запуск» у квеста в `running` отдавала отказ перехода, и снаружи это
           выглядело как «нажал — и ничего не произошло». */''}
      ${deps.resumable && runtime.questId ? `<div><button type="button" class="hall-btn is-primary" data-action="control-master-work-order-v2" data-control="resume" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId)}">${esc(deps.resumeLabel || 'Повторить запуск')}</button></div>` : ''}
    </div>` : ''
  const planRows = stages.map(stage => ({
    text: stage.name || deps.flowNodeKindLabels?.[stage.kind] || stage.id,
    state: masterPlanState(stage.status),
    note: STAGE_NOTE[stage.waitReason] || STAGE_NOTE[stage.status] || '',
  }))
  const plan = masterPlanHtml('Этапы', planRows, esc, { limit: 12 })
  const transcript = transcriptFor(order, ui, deps)
  // Примечание планировщика: план мог собрать движок Point, а не модель. Без
  // этой строки человек читает шаблонный план как ответ модели.
  const plannerNote = runtime.plannerNote ? `<small class="work-order-exec-note">${esc(runtime.plannerNote)}</small>` : ''
  const stageCount = stages.length
  const doneCount = stages.filter(stage => stage.status === 'completed' || stage.status === 'skipped').length
  // Внутри прогона своей шапки у экрана нет: цель и исход уже названы строкой
  // выше, а «ВЫПОЛНЕНИЕ · Квест выполняется» под ними читалось как второе,
  // другое состояние.
  const head = deps.headless ? '' : `<header class="work-order-exec-head">
        <small>ВЫПОЛНЕНИЕ</small>
        <span class="work-order-exec-status">${esc(deps.statusText || '')}</span>
      </header>`
  return `<section class="work-order-exec" data-work-order-execution="${esc(order.id)}" data-quest-id="${esc(runtime.questId || '')}">
      ${head}
      ${stallHtml}
      ${/* В прогоне управление стоит под потоком: сперва читают, что делает
           агент, и только потом решают, вмешиваться ли. Поле «сообщение
           активному квесту» над этапами занимало верх экрана формой. */''}
      ${deps.headless ? '' : deps.controlsHtml || ''}
      ${plan}
      ${transcript ? `<div class="work-order-exec-log">${transcript}</div>`
        : `<div class="work-order-exec-empty"><span>${esc(emptyTranscriptText(stall, stages))}</span></div>`}
      ${plannerNote}
    </section>`
}

// Запущенный квест вместо карточки запуска.
//
// Карточка — это предложение: её читают, правят и утверждают. После запуска
// решать в ней нечего, а место в ленте нужно тому, что происходит сейчас.
// Поэтому утверждённый наряд уходит из ленты целиком, а на его месте остаётся
// прогон: строка исхода, этапы и поток работы агента — как в ленте
// CLI-агентов, где запуск разворачивается в журнал, а не в форму.
//
// Договор при этом никуда не девается: состав задания лежит под свёрнутым
// заголовком, а доказательства и управление приложением встают после потока —
// там, где их ищут, когда работа кончилась.
export function workOrderRunHtml(order, ui, deps = {}) {
  const esc = requireEsc(deps)
  const runtime = order?.runtime || {}
  const status = String(runtime.status || '')
  const tone = deps.tone || ''
  const mark = deps.mark || '·'
  const live = ['preflight', 'running', 'verifying', 'applying'].includes(status)
  return `<section class="master-v2-run ${esc(tone)}${live ? ' is-live' : ''}" data-work-order-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId || '')}">
      <header class="master-v2-run-head">
        ${/* Колонка маркера пустует у всех, кроме работающего квеста: знак
             исхода принадлежит строке состояния и рисовать его дважды —
             значит показать «✓» дважды у одной галочки. Живой квест получает
             сюда пульс: у него в строке знака нет. */''}
        <span class="master-v2-run-mark" aria-hidden="true"></span>
        <div>
          <strong>${esc(order.goal || 'Задание')}</strong>
          <span class="master-v2-approved ${esc(tone)}">${live ? '' : esc(mark) + ' '}${esc(deps.statusText || status)}</span>
        </div>
      </header>
      ${deps.createdHtml || ''}
      ${workOrderExecutionHtml(order, ui, { ...deps, headless: true })}
      ${deps.controlsHtml || ''}
      ${deps.compositionHtml || ''}
      ${deps.applicationHtml || ''}
      ${deps.evidenceHtml || ''}
    </section>`
}
