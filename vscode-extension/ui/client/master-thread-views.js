import { masterUsedMemoryHtml } from './master-memory-ui.js'
import { masterContextAddHtml, masterContextHtml, masterMessageAttachmentsHtml } from './master-context-ui.js'
import { masterSessionHtml, masterComposerHtml } from './master-session-ui.js'
import { masterDayKey, masterDayLabel, masterTimeLabel } from './master-feed.js'
import { masterComposeActionsHtml, masterComposeCountHtml, masterComposeFormClass, masterComposeMetaHtml, masterComposeRows, masterWaitSuffix } from './master-compose.js'
import { createMasterQuestionsViews, masterParseAnswers } from './master-questions-views.js'
import { masterMentionActiveId, masterMentionHtml, masterMentionOpen } from './master-mention-ui.js'
import { masterQueueHtml, masterQueueOf, masterSlashActiveId, masterSlashHtml, masterSlashOpen } from './master-compose-keys.js'
import { questPlanRows } from './master-plan-views.js'
import { questChecklistHtml, questMenuHtml } from './master-quest-views.js'
import { masterToolNameNow } from './master-tool-names.js'
import { icon } from './ui-icons.js'
import { masterQuestStripHtml, masterQuestStripModel } from './master-quest-strip.js'
import { masterHiringCardsHtml } from './master-hiring-card.js'
import { masterAgentCardFromAction, masterAgentCardHtml, masterAgentCardsFor, masterAgentCardsHtml } from './master-agent-card.js'
import { masterCardMoreAttrs } from './master-card-open.js'
import { monogram } from './master-agent-sheet.js'
import { createMasterTrail, trailModelFromItem } from './master-trail.js'

// Диалог с Мастером: лента, реплика и всё, что к ней приложено.
//
// Экран жил в main.js и упирался вместе с ним в границу модуля
// (scripts/check-release-contracts.mjs: 7000 строк). Вынесен целиком, без
// правки поведения: состояние осталось в main.js и приходит сюда общим
// ui-объектом, как у остальных вынесенных видов.
export function createMasterThreadViews(dependencies) {
  const {
    agentById,
    agentClass,
    agentWorkTranscriptHtml,
    approvalCard,
    changeSetCardHtml,
    companionActionProposalHtml,
    connectionLabel,
    countOf,
    esc,
    execControlsHtml,
    flowNodeKindLabels,
    formatCompanionMarkdown,
    modelChipHtml,
    pendingChangeSets,
    patchCard,
    plannerFallbackBannerHtml,
    sessionKeepUndoBarHtml,
    sessionRunChangedFilesHtml,
    questImportanceLabel,
    questProposalEditorHtml,
    questStatusLabels,
    shell,
    statusLabels,
    masterBriefPanelHtml,
    masterWorkOrderCardsHtml,
    masterBriefTabHtml,
    masterStreamBlockHtml,
    masterStreamPhaseNow,
    masterTurnErrorHtml,
    taskBriefCardHtml,
    taskBriefReady,
    taskProposalById,
    rosterHasAgent,
    ui,
    vscode,
  } = dependencies

  const {
    masterAnswersResolvedHtml,
    masterAskSlotHtml,
    masterLiveQuestionsHtml,
    masterQuestionsOf,
    masterTurnQuestionsHtml,
  } = createMasterQuestionsViews({ esc, countOf, ui })
  const { masterTrailHtml } = createMasterTrail({ esc, countOf, ui })

  // ── Диалог с Мастером ──────────────────────────────────────────────────────
  // Собственная поверхность диспетчера, а не переиспользование чата компаньона.
  // Результат хода здесь другой: компаньон отвечает советом, Мастер — предложением
  // работы, которое человек принимает или отклоняет.

  // Предложение, созданное этой репликой.
  //
  // Карточка квеста жила только в `response` — в ответе на последний ход. Стоило
  // переоткрыть панель, и разговор загружался из истории уже без неё: реплика
  // «квест предложен и ждёт вашего решения» оставалась, а решать в ней было
  // нечего. Само предложение никуда не девалось — оно ждало в очереди решений,
  // то есть в другом разделе, куда человека никто не отправлял.
  //
  // Здесь карточка собирается по `proposalId` реплики из bootstrap, поэтому
  // переживает и перезагрузку панели, и переключение раздела. Решённое
  // предложение остаётся строкой: разговор должен читаться честно и потом.
  const MASTER_PROPOSAL_RESOLVED = { started: 'Квест запущен', ignored: 'Предложение отклонено' }

  function masterPinnedForProposal(proposalId) {
    const id = String(proposalId || '')
    if (!id) return null
    const pinned = ui.masterPinnedWork.get(id)
    if (pinned) return pinned
    const proposal=(ui.state.boot?.questProposals || []).find(item=>item.id===id)
    const quest=proposal?.flowId && (ui.state.boot?.quests || []).find(item=>!item.parentId && item.flowId===proposal.flowId)
    if(quest){
      const execution=(ui.state.boot?.executions || []).find(item=>item.questId===quest.id && item.runId)
      return {proposalId:id,questId:quest.id,runId:execution?.runId || ''}
    }
    // Fallback: активный details, если стартовали отсюда и pin ещё не пришёл.
    const run = ui.state.details?.run
    if (!run) return null
    const execution = (ui.state.boot?.executions || []).find(item => item.runId === run.id)
    if (!execution?.questId) return null
    return { proposalId: id, questId: execution.questId, runId: run.id }
  }

  function masterWorkDetailsFor(pinned) {
    if (!pinned) return null
    const details = ui.state.details
    if (!details?.run) return null
    if (pinned.runId && details.run.id === pinned.runId) return details
    if (pinned.questId && details.run.questId === pinned.questId) return details
    const execution = (ui.state.boot?.executions || []).find(item =>
      item.questId === pinned.questId && item.runId === details.run.id
    )
    return execution ? details : null
  }

  function masterWorkChangeSetsHtml(questId) {
    if (!questId) return ''
    const sets = pendingChangeSets().filter(item => item.questId === questId).slice(0, 3)
    if (!sets.length) return ''
    return `<div class="hall-work-changes">${sets.map(set => changeSetCardHtml(set, true)).join('')}<button type="button" class="hall-btn is-sm" data-action="tab" data-tab="changesets">Все наборы</button></div>`
  }

  // Из чего состоит запущенная работа и где она сейчас.
  //
  // Состояния этапов приходят в bootstrap с первого же обновления, но рисовал
  // их только обзор квеста: в разговоре, где работу и согласовали, о ней было
  // сказано одно слово состояния и свёрнутая хроника событий. Событие — не
  // прогресс: по «инструмент завершён» не видно, сколько этапов позади.
  //
  // Ряды считает master-plan-views: после запуска перечень этапов живёт в
  // правой панели разговора, и два перебора одних и тех же состояний однажды
  // разошлись бы.
  function masterRunPlanRows(questId) {
    return questPlanRows(ui.state.boot, questId, flowNodeKindLabels)
  }

  // Ждущее решение — наверх карточки, а не в хронику.
  //
  // Работа стоит именно на нём, а лежало оно внутри свёрнутой «Хроники» с
  // лимитом в восемьдесят последних событий: чтобы увидеть, чего от тебя ждут,
  // надо было догадаться раскрыть хронику — и успеть, пока событие не уехало за
  // лимит. Ниже, в самой хронике, поднятое не повторяется.
  function masterPendingDecisionsHtml(details) {
    const waiting = (details?.approvals || []).filter(item => item.status === 'pending')
    if (!waiting.length) return ''
    const patches = new Map((details?.patches || []).map(item => [item.approvalId, item]))
    const cards = waiting.map(approval => {
      const patch = patches.get(approval.id)
      return patch ? patchCard(patch, approval, true) : approvalCard(approval, true)
    })
    return `<div class="hall-work-decisions">${cards.join('')}</div>`
  }

  // Где запущенная работа сейчас. Считается один раз на два вида: карточка в
  // ленте показывает, чем агент занят, вкладка и панель справа — по каким
  // этапам он идёт. Разойдясь, они назвали бы одному квесту два состояния.
  function masterStartedWorkState(proposalId) {
    const pinned = masterPinnedForProposal(proposalId)
    const questId = pinned?.questId || ''
    const quest = questId ? (ui.state.boot?.quests || []).find(item => item.id === questId) : null
    const details = masterWorkDetailsFor(pinned)
    const execution = questId
      ? (ui.state.boot?.executions || []).find(item => item.questId === questId && item.runId)
        || (ui.state.boot?.executions || []).find(item => item.questId === questId)
      : null
    const run = details?.run || (pinned?.runId ? (ui.state.boot?.runs || []).find(item => item.id === pinned.runId) : null)
    const statusKey = run?.status || execution?.status || quest?.status || 'active'
    return {
      pinned, questId, quest, details, execution, run,
      statusText: statusLabels[statusKey] || questStatusLabels[statusKey] || statusKey,
      step: run?.step,
      rows: masterRunPlanRows(questId),
    }
  }

  // Запущенный квест для правой панели разговора: состояние работы и её этапы.
  function masterStartedQuestSummary(proposalId) {
    const state = masterStartedWorkState(proposalId)
    return { questId: state.questId, statusText: state.statusText, step: state.step, rows: state.rows }
  }

  function masterStartedWorkHtml(stored) {
    const { pinned, quest, details, execution, run, statusText } = masterStartedWorkState(stored?.id)
    const controlsTarget = execution
      ? { ...execution, status: run?.status || execution.status, runId: run?.id || execution.runId }
      : run
        ? { id: run.id, runId: run.id, status: run.status, legacy: true }
        : null
    const controls = controlsTarget ? execControlsHtml(controlsTarget) : ''
    const transcript = details
      ? agentWorkTranscriptHtml(details, { limit: 80, compact: true, withoutPending: true })
      : '<div class="agent-work-empty"><span>Загружаем хронику работы агента…</span></div>'
    const runId = run?.id || pinned?.runId || ''
    return `<section class="hall-work" data-proposal-id="${esc(stored.id)}" data-quest-id="${esc(pinned?.questId || '')}">
      <header class="hall-work-head">
        <div>
          <b>${esc(MASTER_PROPOSAL_RESOLVED.started)}</b>
          <strong>${esc(stored.title || quest?.title || '')}</strong>
        </div>
        <span class="hall-work-status">${esc(statusText)}${run?.step != null ? ` · ход ${esc(run.step)}` : ''}</span>
      </header>
      ${controls ? `<div class="hall-work-controls">${controls}</div>` : ''}
      ${/* Этапы отсюда ушли во вкладку задания: она стоит в шапке разговора и
           после запуска называет прогресс числом, а перечень открывает панелью.
           Пока перечень стоял здесь, он повторялся с панелью, а лента вместо
           работы агента показывала второй раз тот же план. */''}
      ${masterPendingDecisionsHtml(details)}
      <details class="hall-work-log">
        <summary>Хроника</summary>
        ${transcript}
      </details>
      ${masterWorkChangeSetsHtml(pinned?.questId || '')}
      <footer class="hall-work-foot">
        ${runId ? `<button type="button" class="hall-btn is-sm" data-action="load-run" data-id="${esc(runId)}">Открыть хронику</button>` : ''}
        <button type="button" class="hall-btn is-sm" data-action="tab" data-tab="overview">Обзор квеста</button>
      </footer>
    </section>`
  }

  function masterThreadProposalHtml(proposalId) {
    const id = String(proposalId || '')
    if (!id) return ''
    const stored = (ui.state.boot?.questProposals || []).find(item => item.id === id)
    // Наряд и его выполнение принадлежат ходу, в котором Мастер предложил
    // квест. После запуска карточка предложения уступает место ходу работы;
    // оба вида рисуются здесь один раз и остаются в ленте разговора.
    const workOrder = (ui.masterData?.workOrders || []).find(order => order.proposalId === id || order.id === `workorder-${id}`)
    if (workOrder) return masterWorkOrderCardsHtml([workOrder], esc, ui.masterWorkOrderBusy, {
      ui, agentWorkTranscriptHtml, flowNodeKindLabels,
    })
    // Состояние предложения решает хранилище, а не ответ.
    //
    // Ответ хода — снимок на момент реплики, и он остаётся прежним, когда квест
    // уже отклонили или запустили: решают-то его в той же переписке. Пока снимок
    // проверялся первым, отклонённое предложение оставалось полной карточкой с
    // кнопкой «Запустить» — и запускало то, от чего только что отказались.
    if (stored?.status === 'started') return masterStartedWorkHtml(stored)
    const resolved = stored && MASTER_PROPOSAL_RESOLVED[stored.status]
    if (resolved) return `<div class="hall-msg-resolved"><b>${esc(resolved)}</b><span>${esc(stored.title || '')}</span></div>`
    // Необсуждённое задание — вкладка справа, а не карточка в ленте.
    //
    // Карточка с запертой кнопкой предлагала решение, которого принять нельзя,
    // и занимала весь экран разговора ровно тогда, когда разговор и нужен.
    // Вернётся она сюда в тот ход, когда кнопка начнёт работать. Состав задания
    // до тех пор виден в панели — она открывается вкладкой в полосе разговора.
    // Спросить о развилке лента всё же успевает: неотвеченный пакет несёт свою
    // карточку при том ходе, который спросил (masterQuestionsMarkHtml).
    //
    // Условие написано от «показываем, когда готово», а не от «прячем, когда
    // не готово». Прежняя запись проверяла brief и молчала, когда его нет:
    // предложение старого ключевого маршрута приходило вовсе без brief, шло
    // мимо затвора полной карточкой и вкладки при этом не получало. Маршрут
    // снят (internal/orchestrator/chat.go), но сохранённые предложения
    // переживают правку, и затвор обязан знать про них.
    const briefNow = stored?.brief || (String(ui.masterData?.response?.proposal?.id || '') === id ? ui.masterData.response.proposal.brief : null)
    if (!taskBriefReady(briefNow)) return ''
    // Пока предложение открыто, у свежего хода есть то, чего сохранённая карточка
    // не знает: состав отряда и причина выбора. Они живут в ответе и до истории
    // не доезжают.
    const live = ui.masterData?.response
    if (live?.proposal && String(live.proposal.id) === id) {
      // Bootstrap — состояние после Modify, response — снимок до него. Берём
      // свежие поля из хранилища, но сохраняем живое объяснение подбора.
      const current = stored ? { ...live.proposal, ...stored } : live.proposal
      return masterProposalHtml(current, live.partyWhy || stored?.rationale, live.party)
    }
    if (!stored) return ''
    return masterProposalHtml(stored)
  }

  const MASTER_ACTION_RESOLVED = {
    applied: { create_agent: 'Агент создан', create_team: 'Отряд создан', default: 'Действие выполнено' },
    ignored: { default: 'Предложение отклонено' },
  }

  function masterThreadActionProposalHtml(actionProposalId) {
    const id = String(actionProposalId || '')
    if (!id) return ''
    const item = (ui.state.boot?.companionActionProposals || []).find(entry => entry.id === id)
      || (ui.masterData?.response?.actionProposal?.id === id ? ui.masterData.response.actionProposal : undefined)
    if (!item) return ''
    const labels = MASTER_ACTION_RESOLVED[item.status]
    if (labels) {
      const label = labels[item.kind] || labels.default
      const continuationPrompt = String(item.continuationPrompt || '').trim()
      const continueAction = item.status === 'applied' && item.kind === 'create_agent' && continuationPrompt
        ? `<button type="button" class="hall-btn is-sm" data-action="master-send-prompt" data-message="${esc(continuationPrompt)}">${esc(item.continuationLabel || 'Продолжить')}</button>`
        : ''
      return `<div class="hall-msg-resolved"><b>${esc(label)}</b><span>${esc(item.title || '')}</span>${continueAction}</div>`
    }
    // Создание исполнителя — своя карточка ленты, а не вид действий
    // компаньона. Тот рисовал её классами регистра Хаба, для которых в
    // разговоре нет ни одного правила: карточка стояла без меры колонки, без
    // геометрии Чертога и без его кнопок. Место прежнее — при своей реплике:
    // предложение принадлежит ходу и переживает перезагрузку вместе с ним.
    if (item.kind === 'create_agent') return masterAgentCardHtml(masterAgentCardFromAction(item), esc, masterAgentCardDeps())
    return companionActionProposalHtml(item)
  }

  // Каталоги, из которых карточка исполнителя собирает свои поля. Вид их не
  // хранит: подключения и умения приходят общим состоянием, как и всё
  // остальное в этом модуле.
  function masterAgentCardDeps() {
    return {
      connections: ui.state.boot?.connections || [],
      toolCatalog: ui.state.boot?.toolCatalog || [],
      connectionLabel,
    }
  }

  // Реплика вместе со всем, что к ней прилагалось. Основания, карточка квеста и
  // уточняющие вопросы принадлежат конкретному ходу, а не концу разговора: висели
  // они внизу ленты и относились к последнему ответу молча — прокрутив выше, понять,
  // на чём основан старый ответ, было нельзя.
  // Сколько шёл ход. Аналог «Worked for 14m 22s» у эталона: одна служебная
  // строка вместо молчания о полутора минутах ожидания. Задержку ядро
  // сохраняет вместе с репликой — выдумывать её не приходится.
  //
  // Строка стоит в подвале хода, рядом с моделью, а не отдельной строкой над
  // ответом: это сведения о ходе, как и то, кто на него ответил.
  function masterTurnTimeHtml(item) {
    const ms = Number(item?.latencyMs || 0)
    if (!(ms > 1500)) return ''
    const seconds = Math.round(ms / 1000)
    const label = seconds < 60
      ? `${seconds} с`
      : `${Math.floor(seconds / 60)} мин ${seconds % 60} с`
    return `<span class="hall-turn-time" title="Сколько шёл ход">${icon('clock')}${esc(label)}</span>`
  }
  // Что можно сделать с готовой репликой.
  //
  // Панель проявляется по наведению и по фокусу, но в разметке стоит всегда:
  // спрятанная через display:none, она выпала бы из обхода клавиатурой — а это
  // единственный способ добраться до неё без мыши. Оценённая реплика держит
  // панель видимой и без наведения: иначе поставленная отметка исчезает вместе
  // с курсором, и человек ставит её второй раз.
  //
  // Кнопки — значки с подписью в подсказке: шесть слов под каждой репликой
  // читались как меню, а не как тихие действия над сказанным.
  function masterToolButtonHtml(glyph, label, attrs, className = '') {
    return `<button type="button"${className ? ` class="${className}"` : ''} ${attrs} aria-label="${esc(label)}" title="${esc(label)}">${icon(glyph)}</button>`
  }

  function masterMessageToolsHtml(item, mine, previousAsk) {
    const id = String(item?.id || '')
    if (!id) return ''
    const copy = masterToolButtonHtml('copy', 'Копировать', `data-action="copy-master-message" data-id="${esc(id)}"`)
    if (mine) {
      // Хроника Мастера неизменяема, и «редактировать» здесь было бы неправдой:
      // кнопка готовит реплику заново в поле, а прежняя остаётся в разговоре.
      return `<div class="hall-msg-tools">${copy}${masterToolButtonHtml('edit', 'Изменить и отправить заново', `data-action="master-fork-message" data-id="${esc(id)}" data-message="${esc(item.content || '')}"`)}</div>`
    }
    const feedback = String(item.feedback || '')
    // «Ответить иначе» есть только у модельного ответа: у движка Point путь один,
    // и кнопка вернула бы тот же текст слово в слово.
    const again = item.mode === 'model' && previousAsk
      ? masterToolButtonHtml('retry', 'Ответить ещё раз', `data-action="regenerate-master-message" data-id="${esc(id)}" data-message="${esc(previousAsk)}"`)
      : ''
    const mark = value => masterToolButtonHtml(value === 'up' ? 'thumbs-up' : 'thumbs-down', value === 'up' ? 'Полезно' : 'Не помогло', `data-action="master-feedback" data-id="${esc(id)}" data-value="${value}"`, feedback === value ? 'is-on' : '')
    return `<div class="hall-msg-tools${feedback ? ' is-marked' : ''}">${copy}${again}${masterToolButtonHtml('info', 'Сведения о ходе', `data-action="master-message-details" data-id="${esc(id)}"`)}${mark('up')}${mark('down')}</div>`
  }

  // Реплика-ответ, которой не нашлось вопросов: старая запись, чужая сборка или
  // сбой сохранения. Показывается тем же блоком, что и обмен под вопросами.
  function masterAnswersCardHtml(content) {
    return masterAnswersResolvedHtml(masterParseAnswers(content))
  }

  function masterMessageHtml(item, facts, answered, previousAsk, showProposal) {
    const mine = item.role === 'user'
    const answersCard = mine ? masterAnswersCardHtml(item.content) : ''
    const body = answersCard || formatCompanionMarkdown(String(item.content || ''))
    const pending = item.id === 'pending-user'
    // Говорящего называет форма, а не подпись: своя реплика — плашка, ответ —
    // текст по колонке. Имя и время остаются для читалки экрана; глазу время
    // показывает подвал хода, когда к нему подводят указатель.
    const time = pending ? 'сейчас' : masterTimeLabel(item.createdAt)
    const article = `<article class="hall-msg ${mine ? 'is-mine' : ''}${answersCard ? ' is-answers' : ''}${pending ? ' is-pending' : ''}">
      <span class="who hall-sr"><span class="hall-speaker-name">${mine ? 'Вы' : 'Мастер'}</span><time>${esc(time)}</time></span>
      <div class="body">${body}</div>
    </article>`
    // Путь к ответу стоит над ним: сначала что делал, потом вывод.
    const trail = mine ? '' : masterTrailHtml(trailModelFromItem(item))
    // Уточнения принадлежат ходу, который их задал, и стоят сразу под ответом:
    // сперва то, что сказано, потом то, что спрошено.
    const questions = mine ? '' : masterTurnQuestionsHtml(item, answered)
    const stamp = time ? `<time class="hall-turn-stamp" aria-hidden="true">${esc(time)}</time>` : ''
    // Ход, сорванный или остановленный, ядро сохраняет с режимом `failed` или
    // `cancelled` и причиной в поле отката. Метка «ответил движок Point» врала
    // бы о нём: движок не отвечал, ответ просто не дошёл до конца.
    const broken = !mine && ['failed', 'cancelled'].includes(String(item.mode || ''))
    const brokenHtml = !broken ? '' : item.mode === 'failed'
      ? masterTurnErrorHtml({ streamError: item.fallbackReason, ask: previousAsk })
      : `<div class="hall-turn-note">${icon('stop')}<span>Ответ остановлен</span></div>`
    const foot = `${mine || broken ? '' : masterAnswerBadgeHtml(item)}${pending ? '' : masterMessageToolsHtml(item, mine, previousAsk)}${stamp}${mine ? '' : masterTurnTimeHtml(item)}${masterUsedMemoryHtml(item.memoryIds,ui.masterData?.sessions?.memoryEntries,esc,item.id,ui.masterOpenReasoning.has('memory:'+item.id))}`
    const attached = `${masterMessageAttachmentsHtml(item.attachments,esc)}${trail}${String(item.content || '').trim() || !broken ? article : ''}${brokenHtml}${questions}${foot ? `<div class="hall-turn-foot">${foot}</div>` : ''}${masterFactsHtml(facts)}${showProposal === false ? '' : `${masterThreadProposalHtml(item.proposalId)}${masterThreadActionProposalHtml(item.actionProposalId)}`}`
    // Ключ появления (master-feed-motion.js): у ответа — номер хода, тот же, что
    // у блока идущего хода, поэтому готовый ответ не «появляется» второй раз.
    const feedKey = mine ? '' : ` data-feed-key="a:${esc(item.turnId || item.id || '')}"`
    return `<div class="hall-turn${mine ? ' is-user-turn' : ' is-master-turn'}${pending ? ' is-pending-turn' : ''}"${feedKey}>${attached}</div>`
  }

  // Пока ядро не вернуло историю, своя реплика уже стоит в ленте — иначе
  // кажется, что сообщение «пропало», пока модель думает.
  function masterPendingUserHtml() {
    // Держится, пока ход виден в ленте своим блоком, а не только пока он идёт:
    // между концом хода и приходом истории реплика иначе пропадала вместе с
    // ответом и возвращалась через мгновение.
    if (!ui.masterSending && !['settling', 'failed'].includes(masterStreamPhaseNow())) return ''
    const pending = String(ui.masterSentText || '').trim()
    if (!pending) return ''
    const history = ui.masterData?.history || []
    const last = history[history.length - 1]
    if (last?.role === 'user' && String(last.content || '').trim() === pending) return ''
    // Ответы на уточнения уже видны под своими вопросами: отдельной репликой
    // они повторили бы сами себя, да ещё и служебным форматом отправки.
    if (last?.role === 'assistant' && masterQuestionsOf(last).length && masterParseAnswers(pending).length) return ''
    return masterMessageHtml({ role: 'user', content: pending, id: 'pending-user', createdAt: new Date().toISOString() }, undefined, null, '')
  }

  // Начало разговора осталось за кадром.
  //
  // История отдаётся хвостом, и на длинном разговоре лента начиналась с середины
  // без единого знака: первая показанная реплика читалась как первая вообще.
  // Набрано как основания ответа — тем же приглушённым по колонке реплики,
  // потому что это тоже контекст ленты, а не чьё-то сообщение.
  function masterThreadCutHtml(shownCount) {
    if (!ui.masterData?.truncated && !ui.masterData?.paginated) return ''
    // Прежняя строка была тупиком: она сообщала, что разговор начался раньше, и
    // не давала туда попасть. Кнопка просит у ядра весь хвост, какой оно
    // хранит, — за один раз, потому что страничная догрузка вверх на ленте с
    // «липкими» датами и поиском стоит дороже, чем весь этот разговор целиком.
    return `<div class="hall-facts-line">
      <span>Страница · ${countOf(shownCount, 'реплика', 'реплики', 'реплик')}</span>
      ${ui.masterData?.truncated ? `<button type="button" class="hall-btn is-sm" data-action="master-load-earlier" ${ui.masterLoadingEarlier ? 'disabled' : ''}>${ui.masterLoadingEarlier ? 'Загружаем…' : 'Показать раньше'}</button>` : ''}
      <button type="button" class="hall-btn is-sm" data-action="master-load-latest">Последние сообщения</button>
    </div>`
  }

  // Основания показываются там, где изменились.
  //
  // Мастер повторяет их каждым ответом — размер ростера, активные квесты, размер
  // отряда, — и повторённые пять раз подряд они перестают быть основанием и
  // становятся шумом. Изменение же читается сразу: агентов стало больше, квест
  // пошёл. Молчание между повторами честно: последняя показанная строка всё ещё
  // верна и всё ещё на экране.
  // stampMasterAnswer переносит режим ответа на последнюю реплику Мастера.
  //
  // История хранит только текст: чем именно отвечено — модельным путём или
  // движком — знает ответ на текущий ход. Клеймим последнюю реплику ассистента,
  // иначе метка либо не появится, либо расползётся по всей переписке.
  function stampMasterAnswer(history, response) {
    if (!Array.isArray(history) || !history.length || !response) return history || []
    const mode = String(response.mode || '')
    const reason = String(response.fallbackReason || '')
    if (!reason && mode !== 'model') return history
    const copy = history.slice()
    for (let index = copy.length - 1; index >= 0; index -= 1) {
      if (copy[index]?.role === 'user') continue
      copy[index] = { ...copy[index], mode, model: response.model || '', fallbackReason: reason }
      break
    }
    return copy
  }

  function masterThreadHtml(history) {
    // Ответы на уточнения показываются под вопросами, которые их вызвали, —
    // значит, реплика человека, целиком состоящая из них, в ленте не повторяется.
    const answersFor = new Map()
    const folded = new Set()
    for (let index = 0; index < history.length - 1; index += 1) {
      if (history[index]?.role !== 'assistant' || !masterQuestionsOf(history[index]).length) continue
      const next = history[index + 1]
      if (next?.role !== 'user') continue
      const rows = masterParseAnswers(next.content)
      if (!rows.length) continue
      answersFor.set(index, rows)
      folded.add(index + 1)
    }
    // Пока ответы едут в ядро, обмен уже решён: блок показывает их на месте, а
    // не спрашивает второй раз то, на что человек только что ответил.
    if (ui.masterSending) {
      const tail = history.length - 1
      const sent = masterParseAnswers(ui.masterSentText)
      if (sent.length && history[tail]?.role === 'assistant' && masterQuestionsOf(history[tail]).length) answersFor.set(tail, sent)
    }
    // Одно предложение — одна карточка, у последнего хода, который его принёс.
    //
    // Карточка живая: состояние, состав и кнопку запуска она читает сейчас, а не
    // из снимка хода. Когда Мастер переписывал задание вторым ходом — а он делает
    // это сам, если первая попытка не прошла проверку, — лента показывала две
    // одинаковые карточки с одной и той же кнопкой. Вторая копия ничего не
    // добавляла, кроме сомнения, которую из них жать.
    const lastProposalTurn = new Map()
    history.forEach((item, index) => {
      for (const id of [String(item?.proposalId || ''), String(item?.actionProposalId || '')]) {
        if (id) lastProposalTurn.set(id, index)
      }
    })
    const showsProposal = (item, index) => {
      for (const id of [String(item?.proposalId || ''), String(item?.actionProposalId || '')]) {
        if (id && lastProposalTurn.get(id) === index) return true
      }
      return !item?.proposalId && !item?.actionProposalId
    }
    let shown = ''
    // Реплика человека, на которую отвечали. «Ответить иначе» переспрашивает
    // именно её: без вопроса просьба о другом пути повисла бы ни на чём.
    let previousAsk = ''
    // День предыдущей реплики. Разговор с Мастером живёт неделями, и без
    // разделителей вчерашнее решение неотличимо от сегодняшнего: в ленте это
    // две соседние строки.
    let day = ''
    return history.map((item, index) => {
      const signature = (item.factsUsed || []).join('|')
      const facts = signature && signature !== shown ? item.factsUsed : undefined
      if (signature) shown = signature
      const ask = previousAsk
      if (item.role === 'user') previousAsk = String(item.content || '')
      // Свёрнутая реплика не съедает разделитель дня: день остаётся неотмеченным,
      // и его поставит следующая показанная реплика.
      if (folded.has(index)) return ''
      const key = masterDayKey(item.createdAt)
      const separator = key && key !== day
        ? `<div class="hall-day">${esc(masterDayLabel(item.createdAt))}</div>`
        : ''
      if (key) day = key
      return separator + masterMessageHtml(item, facts, answersFor.get(index), ask, showsProposal(item, index))
    }).join('')
  }

  // Состав отряда с причиной выбора. Оценка и совпавшие термины считались и
  // раньше, но не показывались: человек видел список имён и не мог ни поспорить,
  // ни поправить. Показанное обоснование делает подбор проверяемым.
  //
  // Оценки может не быть вовсе: тем же составом Мастер перечисляет ростер на
  // вопрос «кто есть», а там задачи нет и оценивать не с чем. Ноль вместо пустоты
  // выдавал бы «не оценивали» за «оценили и не подошёл».
  function masterPartyMemberHtml(member) {
    const blocking = member.blocking || []
    const matched = member.matched || []
    const why = blocking.length
      ? esc(blocking[0])
      : matched.length
        ? `совпало: ${matched.map(term => esc(term)).join(', ')}`
        : 'выбран по составу отряда'
    // Голое число у правого края читалось как неизвестно что: смысл жил только в
    // подсказке по наведению, а шкалы не было вовсе.
    const score = member.score == null
      ? ''
      : `<span class="hall-party-score" title="Оценка соответствия задаче — чем выше, тем ближе агент к этой задаче"><small>Соответствие</small><b>${Number(member.score)}</b><i>/100</i></span>`
    // Имя и роль обрезаются по колонке — целиком они остаются в подсказке, иначе
    // обрезанное не восстановить ничем, кроме похода в карточку агента.
    const name = String(member.name || member.agentId || '')
    // Строка ведёт к листу персонажа в правой панели: модель, умения, послужной
    // список. Прежде за подробностями надо было уходить в Гильдию.
    return `<button type="button" class="hall-party-row" data-action="master-inspector-agent" data-id="${esc(member.agentId || '')}" title="Открыть лист персонажа">
      <span class="hall-insp-mark" aria-hidden="true">${esc(monogram(name) || '?')}</span>
      <span class="hall-party-who">
        <b title="${esc(name)}">${esc(name)}</b>
        ${member.role ? `<span title="${esc(member.role)}">${esc(member.role)}</span>` : ''}
      </span>
      <span class="hall-party-why${blocking.length ? ' is-blocking' : ''}">${why}</span>
      ${score}
    </button>`
  }

  function masterPartyHtml(party) {
    if (!Array.isArray(party) || party.length === 0) return ''
    return `<div class="hall-party">${party.map(masterPartyMemberHtml).join('')}</div>`
  }

  function masterProposalParty(proposal, liveParty) {
    const ids = Array.isArray(proposal?.teamAgentIds) ? proposal.teamAgentIds : []
    if (!ids.length) return Array.isArray(liveParty) ? liveParty : []
    const liveById = new Map((liveParty || []).map(member => [member.agentId, member]))
    return ids.map(id => {
      if (liveById.has(id)) return liveById.get(id)
      const agent = agentById(id)
      return agent ? { agentId: id, name: agent.name, role: agent.roleDescription || agentClass(agent) } : { agentId: id, name: id }
    })
  }

  // Предложение квеста — то, ради чего с Мастером и разговаривают.
  // Отступ сверху задаёт ход (.hall-turn), а не карточка: свой margin складывался
  // бы с промежутком контейнера и отрывал предложение от его же реплики.
  function masterProposalHtml(proposal, partyWhy, party) {
    if (!proposal) return ''
    // Редактор брифа рисуется в одном месте. readTaskBriefEditor ищет поля по
    // всему разделу и при двух наборах молча прочтёт первый: открыта панель —
    // правка идёт там, закрыта — здесь. Кнопки правки остаются в обоих местах:
    // они шлют одно и то же решение.
    if (proposal.brief) {
      const editing = ui.proposalEditId === proposal.id && !(ui.masterBriefPanelOpen && (ui.masterInspectorTab || 'quest') === 'quest')
      return taskBriefCardHtml(proposal, { esc, countOf, editing, busy: ui.proposalStarting.has(proposal.id) || ui.proposalModifying.has(proposal.id), editor: editing ? questProposalEditorHtml(proposal) : '', rosterReady: rosterHasAgent() })
    }
    // Предложение без brief рисуется теперь в одном месте — в панели задания:
    // затвор ленты пропускает только готовое, а готовым бывает только то, у
    // чего brief есть. Оговорка про открытую панель тут и стояла ради второго
    // места; оставь мы её, редактор в панели не открылся бы никогда.
    const editing = ui.proposalEditId === proposal.id
    const modifying = ui.proposalModifying.has(proposal.id)
    const shownParty = masterProposalParty(proposal, party)
    const partyNote = proposal.teamAgentIdsLocked
      ? 'состав выбран вами и сохранён — Мастер не заменит его при запуске'
      : partyWhy
    return `<section class="hall-deck hall-proposal">
      <header><b>Предложен квест</b><small>${editing ? 'Редактирование' : esc(questImportanceLabel(proposal.importance))}</small></header>
      <div class="hall-panel-row is-stack">
        <span class="hall-lead">${esc(proposal.title || '')}</span>
        ${(proposal.objectives || []).length ? `<ol class="hall-objectives">${proposal.objectives.map(item => `<li>${esc(item)}</li>`).join('')}</ol>` : ''}
        ${/* «Готово, когда» вышло из-под раскрывашки: по нему решают, запускать
             ли квест, а состав отряда — про то, кем он будет выполнен. Второе
             читают, когда решили читать. */''}
        ${/* Вида проверки у предложения нет: его условия — слова, а не договор
             с ядром. Счёт и полоса при этом те же, что у задания и квеста: одна
             форма на все три места, где условия читают. */''}
        ${questChecklistHtml('Готово, когда', (proposal.definitionOfDone || []).map(item => ({ text: item })), esc)}
        ${shownParty.length || partyNote ? `<details class="hall-proposal-more"${masterCardMoreAttrs(`quest:${proposal.id}`, { esc })}><summary>Кто будет делать</summary>${masterPartyHtml(shownParty)}${partyNote ? `<small class="hall-fineprint">${esc(partyNote)}</small>` : ''}</details>` : ''}
        ${editing ? questProposalEditorHtml(proposal) : ''}
      </div>
      ${Number(proposal.estimateTokens) > 0 ? `<div class="hall-panel-row"><small class="hall-fineprint">Потолок расхода: ${Number(proposal.estimateTokens).toLocaleString('ru-RU')} токенов. Дальше квест остановится сам — это предел, а не прогноз.</small></div>` : ''}
      <div class="hall-panel-row hall-actions">
        <div class="hall-quest-acts">
          <button class="hall-btn is-primary" data-action="quest-proposal-start" data-id="${esc(proposal.id)}" ${ui.proposalStarting.has(proposal.id) || modifying ? 'disabled' : ''}>${ui.proposalStarting.has(proposal.id) ? 'Запускаем…' : 'Запустить'}</button>
          ${editing ? `<button class="hall-btn" data-action="quest-proposal-modify" data-id="${esc(proposal.id)}" ${modifying ? 'disabled' : ''}>${modifying ? 'Сохраняем…' : 'Сохранить'}</button>` : ''}
          ${questMenuHtml([
            editing ? null : { action: 'quest-proposal-modify', id: proposal.id, label: 'Изменить', busy: modifying },
            { action: 'quest-proposal-ignore', id: proposal.id, label: 'Отклонить', busy: modifying },
          ], esc)}
        </div>
        <small class="hall-fineprint is-trailing">сам квест не стартует — решение за вами</small>
      </div>
    </section>`
  }

  // На чём основан ответ. Мастер отвечает не из воздуха, и показать основание
  // дешевле, чем потом объяснять, почему он выбрал не тех.
  function masterFactsHtml(facts) {
    if (!Array.isArray(facts) || facts.length === 0) return ''
    return `<div class="hall-facts-line">${facts.map(fact => `<span>${esc(fact)}</span>`).join('')}</div>`
  }

  // Ненастроенный Мастер — состояние, а не ошибка: раздел открывается и говорит,
  // чего не хватает, вместо пустого окна с непонятным сбоем.
  function masterNotConfiguredHtml() {
    return `<div class="hall-page">
      <div class="hall-title">
        <span class="kicker">Мастер не настроен</span>
        <h1>Разговаривать пока не с кем</h1>
      </div>
      <p class="hall-note">Мастер подбирает отряд по своей политике: она решает размер отряда, строгость подтверждений и автостарт Flow. Пока политика не задана, предложение квеста опиралось бы на выдуманные настройки.</p>
      <div><button class="hall-btn is-primary" data-action="open-orchestrator-setup">Настроить мастера</button></div>
    </div>`
  }

  // Подпись под полем ввода — кто отвечает и когда вступает модель.
  //
  // Разговор ведёт движок Point всегда: модель Мастера — планировщик, она берётся
  // за дело при запуске квеста и пересобирает отряд. Одно её имя под полем читалось
  // так, будто отвечает она, а «модель мастера не выбрана» звучало упрёком —
  // хотя пресет без модели рабочая настройка, а не недоделанная.
  //
  // Отдельно разведены ещё два случая: ответ не пришёл и ответ ещё едет. Раньше
  // второй выдавался за «модель не выбрана», и при живых настройках человек шёл
  // их чинить.
  function masterModelLabel() {
    if (!ui.masterData) return ui.masterStatus === 'error' ? 'настройки мастера не загрузились' : 'проверяем настройки мастера…'
    const model = String(ui.masterData.config?.model || '').trim()
    // Подпись описывала прежнее разделение: разговор — движок, модель только на
    // запуске квеста. Модель Мастера теперь ведёт и разговор, и подпись обязана
    // говорить то же, что происходит.
    return model ? `модель мастера — ${model} · исполнение узлов — движок Point` : 'движок Point · модель мастера не подключена'
  }

  // Чем отвечено: моделью Мастера или движком Point.
  //
  // Ядро различает это честно и называет причину отката, но на экран это не
  // доходило: человек настроил модель, она молча не отвечала, и разговор выглядел
  // исправным. Метка ставится только там, где есть что сказать: у модельного
  // ответа и у отката с причиной.
  function masterAnswerBadgeHtml(item) {
    const mode = String(item.mode || '').trim()
    const reason = String(item.fallbackReason || '').trim()
    if (!reason && mode !== 'model') return ''
    if (reason) {
      // Причина уже начинается словами «модель Мастера …» — прежний зачин
      // «модель не ответила:» повторял то же самое третий раз в одной строке.
      // Выход из этого состояния один — другая модель, и он теперь под рукой:
      // раньше человек читал причину и оставался в тупике.
      return `<div class="hall-answer-badge is-fallback" title="${esc(reason)}">${icon('warning')}<b>ответил движок Point</b><span>${esc(reason)}</span><button type="button" class="hall-chip" data-action="open-orchestrator-setup">Сменить модель</button></div>`
    }
    // Обычный случай — тихая метка модели: «ответила модель мастера» полной
    // фразой у каждого хода повторяла одно и то же; фраза осталась подсказкой.
    const model = String(item.model || '').trim()
    return `<div class="hall-answer-badge" title="Ответила модель мастера${model ? `: ${esc(model)}` : ''}"><b class="hall-sr">ответила модель мастера</b>${model ? `<span>${esc(model)}</span>` : ''}</div>`
  }

  function masterStartersHtml() {
    const items = [
      ['Поговорить', 'Расскажи, что сейчас происходит в проекте', 'Разобраться в проекте и выбрать следующий шаг'],
      ['Создать агента', 'Создай агента для ', 'Подготовить специалиста под вашу задачу'],
      ['Создать квест', 'Создай квест: ', 'Превратить идею в план работы'],
      ['Выбрать отряд', 'Подбери отряд для задачи: ', 'Собрать подходящих исполнителей'],
    ]
    return `<div class="master-starters" aria-label="Быстрый старт разговора"><small>Быстрый старт</small><div>${items.map(([label, prompt, detail]) => `<button type="button" class="hall-chip" data-action="master-ask" data-question="${esc(prompt)}"><strong>${esc(label)}<i aria-hidden="true">↗</i></strong><span>${esc(detail)}</span></button>`).join('')}</div></div>`
  }

  // «Обсуждаем задание: Укрепить обработчик оплаты» шло одной серой фразой в
  // одиннадцать пикселей: метка и название весили одинаково, и название —
  // единственное, что здесь читают, — терялось. Ярусы разведены разметкой.
  function masterDiscussionContextHtml() {
    if (!ui.masterDiscussionProposalId) return ''
    // У задания с брифом имени нет — его роль играет цель. Раньше вместо имени
    // в подпись уезжал внутренний идентификатор: «Задание qp-brief».
    const discussed = taskProposalById(ui.masterDiscussionProposalId)
    const title = discussed?.title || discussed?.brief?.goal || ui.masterDiscussionProposalId
    return `<small class="hall-compose-task"><span>Задание</span><b>${esc(title)}</b></small> <button type="button" class="hall-chip" data-action="master-new-discussion" ${ui.masterSending ? 'disabled' : ''}>Новое обсуждение</button>`
  }

  // Содержимое строки под полем собрано в одном месте: её рисует и полная
  // отрисовка раздела, и досборка после ответа ядра. Разъехавшись, эти два
  // пути дали бы разный композер на одном и том же состоянии.
  function masterComposeActionsInnerHtml() {
    // Модель названа под полем только тогда, когда с ней что-то не так.
    //
    // Одно и то же имя стояло в трёх местах сразу: под каждым ответом («ответила
    // модель мастера …»), в шапке («Настройки мастера») и здесь. Третье
    // повторение не сообщало ничего, но занимало целую строку композера и
    // отправляло подсказку о клавишах на вторую. Неподключённая модель — другое
    // дело: это состояние, о котором надо сказать.
    const configured = Boolean(String(ui.masterData?.config?.model || '').trim())
    const model = configured ? '' : `<small class="hall-compose-model">${esc(masterModelLabel())}</small>`
    const hintHidden = ui.masterSending ? '' : ' is-quiet'
    // Ряд разведён по краям: слева — чем готовят запрос, справа — чем его
    // отправляют. Пока всё лежало одной кучей с прижимом вправо, разрыв в ряду
    // держала подсказка о клавишах: невидимая подпись занимала 230 пикселей из
    // 702 и работала распоркой. Распорку заменили края, подсказка ушла.
    return `<div class="hall-compose-lead">${masterContextAddHtml(ui.masterConversationId, esc, ui.masterSending)}${masterComposerHtml(ui.masterData?.sessions, ui.state.boot?.orchestrator?.model, esc, ui.masterSending, modelChipHtml({ target: 'master', connectionId: ui.state.boot?.orchestrator?.connectionId || '', model: ui.state.boot?.orchestrator?.model || '' }))}${model}</div><div class="hall-compose-trail"><small class="hall-compose-hint${hintHidden}" title="Ctrl+L — курсор в поле · Ctrl+F — поиск по разговору · Ctrl+Shift+N — новый чат">${esc(masterComposeMetaHtml(ui.masterSending))}</small>${masterComposeCountHtml(ui.masterDraft)}${masterComposeActionsHtml(ui.masterSending, ui.masterDraft)}</div>`
  }

  // Поиск по разговору.
  //
  // Разговор с Мастером живёт неделями, и найти в нём прежнее решение прокруткой
  // нельзя. Ищем по тексту реплик и уводим к найденному ходу целиком, а не
  // красим буквы: ответ проходит через разбор markdown, и подсветка внутри
  // готовой разметки рвала бы теги.
  //
  // Поле стоит над лентой всегда, а не за кнопкой: спрятанный поиск не находят.
  // Пустое оно тихое — счётчик и стрелки появляются с первым набранным знаком.
  function masterFindHtml() {
    const query = String(ui.masterFindQuery || '')
    const found = query.trim() ? `<small class="hall-find-count">${esc(ui.masterFindSummary || '')}</small>
      <button type="button" class="hall-icon-btn" data-action="master-find-step" data-step="-1" aria-label="Предыдущее совпадение" title="Предыдущее совпадение">${icon('chevron-down', { className: 'is-up' })}</button>
      <button type="button" class="hall-icon-btn" data-action="master-find-step" data-step="1" aria-label="Следующее совпадение" title="Следующее совпадение">${icon('chevron-down')}</button>` : ''
    // Свёрнут, пока не нужен: полоса поиска во всю ширину стояла над каждым
    // разговором и почти всегда пустовала — сорок четыре пикселя, отнятые у
    // самого разговора. Раскрытым остаётся, пока в нём что-то набрано.
    // Свёрнутый поиск живёт значком в шапке разговора (quest-runtime-views.js).
    if (!ui.masterFindOpen && !query.trim()) return ''

    return `<div class="hall-find">
      <input type="search" id="master-find" aria-label="Поиск по разговору" placeholder="Найти в разговоре" value="${esc(query)}">
      ${found}
      <button type="button" class="hall-icon-btn" data-action="master-find-clear" aria-label="Закрыть поиск" title="Закрыть поиск">${icon('x')}</button>
    </div>`
  }

  function masterQuestStripSlotHtml() {
    const model = masterQuestStripModel({
      workOrders: ui.masterData?.workOrders,
      agentName: id => agentById(id)?.name || '',
    })
    return masterQuestStripHtml(model, esc)
  }

  function masterDialogueHtml() {
    if (ui.masterStatus === 'idle') {
      ui.masterStatus = 'loading'
      setTimeout(() => vscode.postMessage({ type: 'loadMaster' }), 0)
    }
    if (ui.masterData && ui.masterData.configured === false && !ui.masterData.sessions?.items?.length) return shell(masterNotConfiguredHtml())

    const workMode = ui.masterData?.sessions?.workMode || 'discuss'

    // Панель задания собирается один раз: её разметка решает и класс раздела —
    // без задания сужать разговор не подо что.
    const briefPanel = masterBriefPanelHtml()

    return shell(`<div class="hall-dialogue${briefPanel && ui.masterBriefPanelOpen ? ' is-brief-open' : ''}">
      ${plannerFallbackBannerHtml()}
      ${masterSessionHtml(ui.masterData?.sessions, esc)}
      ${masterFindHtml()}
      ${/* Лента — область, а не живой журнал: её разметка пересобирается на
           каждое событие хода, и `aria-live` зачитывал бы её заново каждый раз.
           О ходе говорит диктор рядом с ней, один раз на каждую смену фазы. */''}
      <div class="hall-thread" id="master-thread" role="region" aria-label="Диалог с Мастером">${masterThreadContentHtml()}</div>
      <div class="hall-sr" id="master-announcer" role="status" aria-live="polite" aria-atomic="true"></div>
      ${/* Якорь стоит рядом с лентой, а не внутри неё. Внутри он был обычным
           элементом потока: на короткой переписке садился сразу под последней
           репликой и висел посреди пустого экрана, а `width: 100%` из 05-hall
           растягивал его на всю колонку — вместо жетона получалась полоса.
           Снаружи он держится над карточкой ввода и переживает перерисовку
           ленты, потому что больше не выкидывается вместе с её содержимым. */''}
      ${/* Видимость якоря считается от следования за лентой, а не вписана
           скрытой: полная отрисовка (ответ ядра, чужое состояние) иначе
           прятала его у человека, который как раз читает выше. */''}
      <button type="button" class="hall-thread-cue${ui.masterAutoFollow ? ' is-hidden' : ''}" id="master-scroll-cue" data-action="master-scroll-latest" aria-label="К новым сообщениям">К новым${icon('chevron-down')}</button>
      ${ui.masterData?.configured===false ? `<div class="hall-compose"><p>История доступна. Чтобы продолжить разговор, настройте модель мастера.</p><button type="button" class="hall-btn" data-action="open-orchestrator-setup">Настроить модель</button></div>` : `<form class="${masterComposeFormClass(ui.masterDraft)}">
        ${/* Неотвеченные уточнения спрашивают здесь, а не в ленте: там они
             уезжали вверх с каждым следующим ходом, и человек отвечал не на то,
             что видел. Слот стоит отдельным узлом: досборка после ответа ядра
             переписывает его, не трогая форму и каретку в поле реплики. */''}
        ${/* Идущий квест — первой строкой карточки ввода: туда человек смотрит
             всё время, пока работа идёт. */''}
        <div class="hall-quest-strip-slot" id="master-quest-strip">${masterQuestStripSlotHtml()}</div>
        <div id="master-questions-ask">${masterAskSlotHtml()}</div>
        ${/* Обсуждаемое задание и вложения — один ряд слотов, а не два. Каждый
             занимал свою строку во всю ширину, и карточка со всеми слотами
             вырастала вдвое против покоя. Ряд сворачивается целиком, когда
             пусты оба: пустой div задания и сам по себе стоил промежутка. */''}
        <div class="hall-compose-slots">
          <div id="master-discussion-context">${masterDiscussionContextHtml()}</div>
          ${masterContextHtml(ui.masterConversationId, esc, ui.masterSending, ui.masterData?.contextBudgetChars)}
        </div>
        ${/* Очередь реплик — над полем: это то, что уйдёт следующим. */''}
        <div id="master-queue">${masterQueueHtml(masterQueueOf(ui.masterClient, ui.masterConversationId), esc, { sending: ui.masterSending })}</div>
        ${masterMentionHtml(esc)}${masterSlashHtml(esc)}
        ${/* Поле — комбобокс, пока над ним открыт список «@» или «/»: читалка
             узнаёт о выбранной строке через aria-activedescendant, фокус при
             этом остаётся в поле. */''}
        <textarea id="master-input" rows="${masterComposeRows(ui.masterDraft)}" aria-label="Сообщение Мастеру" role="combobox" aria-autocomplete="list" aria-expanded="${masterMentionOpen() || masterSlashOpen()}"${masterSlashOpen() ? ' aria-controls="master-slash-list"' : masterMentionOpen() ? ' aria-controls="master-mention-list"' : ''}${masterSlashActiveId() || masterMentionActiveId() ? ` aria-activedescendant="${masterSlashActiveId() || masterMentionActiveId()}"` : ''} placeholder="Что хотите сделать в проекте? «/» — команды, «@» — файлы">${esc(ui.masterDraft)}</textarea>
        <small class="hall-compose-note${ui.masterComposeNote ? '' : ' is-hidden'}" role="alert">${esc(ui.masterComposeNote)}</small>
        <div class="hall-compose-actions">${masterComposeActionsInnerHtml()}</div>
      </form>`}
      ${briefPanel}
    </div>`)
  }

  // Содержимое ленты отделено от разметки раздела: ответ ядра обновляет только
  // его. Раньше на каждый ход Мастера пересобирался весь Чертог, и разговор
  // прыгал к первой реплике — см. перенос прокрутки в restoreUi. Полная
  // отрисовка осталась законным путём (открытие раздела, приход предложения),
  // поэтому перенос там нужен по-прежнему.
  function masterThreadContentHtml() {
    const history = ui.masterData?.history || []
    const response = ui.masterData?.response
    // Режим ответа и причина отката приходят в ответе на ход, а рисуется история:
    // без переноса метка «ответила модель» не появлялась бы никогда. Клеймим
    // только последнюю реплику Мастера — ответ относится к ней.
    const stamped = stampMasterAnswer(history, response)
    const thread = stamped.length
      ? masterThreadCutHtml(stamped.length) + masterThreadHtml(stamped)
      // Пустая переписка после неудачной загрузки читается как «диалога не было»,
      // хотя он мог быть: мы просто не смогли его получить. Состояние 'error'
      // тупиковое намеренно — автоповтор превратил бы постоянный отказ в цикл
      // запросов, — но выход из него должен быть под рукой, а не в переоткрытии
      // панели: у очереди решений такая кнопка есть, у Мастера её не было.
      : ui.masterStatus === 'error' && !ui.masterData
        ? `<div class="hall-empty"><b>Переписка не загрузилась</b><span>Ядро не ответило. Прежние сообщения, если они были, здесь не показаны — это не пустой диалог.</span><div><button type="button" class="hall-btn" data-action="retry-master">Повторить</button></div></div>`
        // Пока переписка едет, «диалога нет» — такое же враньё, как и «не
        // загрузилась»: мы просто ещё не знаем. Приглашение писать появляется,
        // когда ядро ответило и переписка действительно пуста.
        : !ui.masterData
          ? `<div class="hall-empty"><b>Открываем переписку</b><span>Ядро отдаёт прежние реплики Мастера.</span></div>`
          : `<div class="hall-empty hall-chat-welcome"><b>С чего начнём?</b><span>Обсудите идею с мастером. Он поможет разобраться в проекте, составить план и подобрать отряд.</span>${masterStartersHtml()}</div>`

    const workMode = ui.masterData?.sessions?.workMode || 'discuss'
    const runChangedFiles = (workMode === 'agent' || ui.state.details?.run) ? sessionRunChangedFilesHtml() : ''
    // Карточке наряда нужны те же зависимости, что и остальным видам: после
    // утверждения она рисует экран выполнения, а он читает хронику прогона.
    const shownProposalIds = new Set(history.map(item => String(item?.proposalId || '')).filter(Boolean))
    const workOrders = masterWorkOrderCardsHtml((ui.masterData?.workOrders || []).filter(order =>
      !shownProposalIds.has(String(order.proposalId || order.id?.replace(/^workorder-/, '') || ''))
    ), esc, ui.masterWorkOrderBusy, {
      ui, agentWorkTranscriptHtml, flowNodeKindLabels,
    })
    // Кем делать работу — рядом с тем, что делать. Карточка найма считается на
    // чтении, поэтому переживает перезагрузку: прежнее предложение найма жило в
    // ответе последнего хода и исчезало вместе с ним.
    const hiring = masterHiringCardsHtml(ui.masterData?.hiring || [], esc)
    // Нового исполнителя заводят отдельной карточкой, а не ярусом внутри
    // карточки запуска: создание агента — решение о новой сущности проекта, и
    // прятать его под раскрывашку чужого документа значит терять его совсем.
    const agentCards = masterAgentCardsHtml(masterAgentCardsFor(ui.masterData), esc, masterAgentCardDeps())

    // Основания, карточка квеста и уточняющие вопросы стоят при своей реплике —
    // они принадлежат ходу и переживают перезагрузку вместе с ним. Внизу остаётся
    // только то, чего история не хранит и что относится к последнему ходу: куда
    // уйти из разговора и кого нанять.
    //
    // Но ответ текущего хода — источник не менее достоверный, чем история, и
    // терять из-за неё присланное нельзя. Реплика связывается с предложением
    // меткой, а с основаниями — своим полем; не оказалось метки или поля (старая
    // запись, чужая сборка, сбой сохранения) — показываем присланное здесь.
    // Иначе ядро говорит «вот предложение», а на экране решать нечего.
    const lastMessage = history[history.length - 1]
    const proposalShown = Boolean(response?.proposal)
      && history.some(item => item.proposalId && item.proposalId === response.proposal.id)
    const factsShown = (lastMessage?.factsUsed || []).length > 0
    const actionProposalShown = Boolean(response?.actionProposal)
      && history.some(item => item.actionProposalId && item.actionProposalId === response.actionProposal.id)
    // Состав без предложения квеста рисуется сам.
    //
    // Он приходит в двух ответах: на «кто есть» — перечислением ростера, и на
    // отказ, когда весь подходящий отряд неработоспособен. Рисовала состав только
    // карточка квеста, а её в этих ответах нет — и ядро зря считало причины
    // неготовности: реплика говорила «поправьте их настройку», не называя ни
    // кого, ни чего не хватает. Там, где карточка есть, состав внутри неё.
    const partyOutsideProposal = response?.proposal ? '' : masterPartyHtml(response?.party)
    // Реплика связывается с уточнениями своим полем; не оказалось поля — ход всё
    // равно спросил, и спросить надо здесь, иначе вопросы ядра пропадают молча.
    const questionsShown = lastMessage?.role === 'assistant' && masterQuestionsOf(lastMessage).length > 0
    return `
        ${thread}
        ${questionsShown || ui.masterSending ? '' : masterLiveQuestionsHtml(response)}
        ${masterPendingUserHtml()}
        ${/* Идущий ход — одним блоком (master-stream-view.js): «Думаю…», след,
             живой ответ, а после конца — тот же ответ, пока не придёт история. */''}
        ${masterStreamBlockHtml()}
        ${factsShown ? '' : masterFactsHtml(response?.facts)}
        ${proposalShown ? '' : masterThreadProposalHtml(response?.proposal?.id)}
        ${actionProposalShown ? '' : masterThreadActionProposalHtml(response?.actionProposal?.id)}
        ${partyOutsideProposal}
        ${masterActionsHtml(response?.actions)}
        ${workOrders}
        ${agentCards}
        ${hiring}
        ${runChangedFiles}
        ${sessionKeepUndoBarHtml(workMode)}
      `
  }

  // Куда уйти прямо из разговора. Мастер раньше только рассказывал, и любое
  // действие человек искал сам по разделам.
  function masterActionsHtml(actions) {
    const list = Array.isArray(actions) ? actions.filter(item => item && item.label) : []
    if (!list.length) return ''
    return `<div class="hall-chat-actions">${list.map(item => `
      <button class="hall-btn is-sm" data-action="tab" data-tab="${esc(item.tab || 'overview')}"${item.hint ? ` title="${esc(item.hint)}"` : ''}>${esc(item.label)}</button>
    `).join('')}</div>`
  }



  return {
    masterAskSlotHtml,
    masterComposeActionsInnerHtml,
    masterDialogueHtml,
    masterDiscussionContextHtml,
    masterModelLabel,
    masterProposalHtml,
    masterQuestStripSlotHtml,
    masterStartedQuestSummary,
    masterThreadContentHtml,
    masterThreadHtml,
    stampMasterAnswer,
  }
}
