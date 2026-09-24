import { masterPlanHtml, masterPlanState, questPlanProgress } from './master-plan-views.js'
import { inspectorTab, masterContextPanelHtml, masterInspectorTabsHtml, masterTeamGroups, masterTeamHtml } from './master-inspector.js'
import { masterContextPayload } from './master-context-ui.js'
import { workOrderStageNote } from './work-order-execution-views.js'
import { list } from './format-units.js'
import { icon } from './ui-icons.js'

// Задание в правой панели разговора.
//
// Ядро собирает задание с первой же реплики, и раньше оно сразу вставало
// карточкой в ленту — с кнопкой «Выполнить поручение», нажать которую нельзя,
// пока в задании остались нерешённые вопросы. Карточка предлагала решение,
// которого принять нельзя, и занимала весь экран разговора.
//
// Пока задание обсуждают, оно живёт вкладкой в полосе разговора и раскрывается
// панелью справа: состав задания под рукой, но места у разговора не отнимает.
// Карточка возвращается в ленту тогда, когда её кнопка начинает работать, —
// и вкладка при этом остаётся: разговор длинный, карточка уедет прокруткой.
//
// Эталон — правая панель Cursor: прижата к краю, сужает разговор, а не
// накрывает его; на узком окне отступать некуда, и она выезжает поверх.

// Отклонённое предложение панели не принадлежит: обсуждать его больше нечего,
// и вкладка на нём была бы дверью в никуда.
//
// Запущенный квест — принадлежит. Раньше он уходил отсюда вместе с отклонённым,
// и в тот самый миг, когда работа началась, дверь к ней закрывалась: лента
// показывала, чем агент занят сейчас, а из чего состоит работа и сколько её
// позади — не показывал никто, кроме обзора квеста в другом разделе. Теперь
// вкладка остаётся на месте и меняет речь: вместо готовности называет прогресс,
// вместо состава задания открывает этапы.
const RESOLVED = new Set(['ignored'])

// Имя задания на вкладке. Режется по рунам, а не по знакам строки: цель пишет
// человек, длину ему никто не ограничивает, а срез посреди суррогатной пары
// оставляет на вкладке битый знак.
function shortGoal(goal) {
  const runes = [...String(goal || '').trim()]
  if (!runes.length) return 'Задание'
  return runes.length > 24 ? runes.slice(0, 23).join('').trimEnd() + '…' : runes.join('')
}

export function createMasterBriefPanel({
  esc, countOf, ui, taskProposalById, proposalEditorHtml,
  taskBriefBodyHtml, taskBriefActionsHtml, taskBriefReady, taskBriefStateLabel,
  rosterHasAgent, legacyProposalHtml, startedQuestSummary,
  agentById = () => null, projectAgents = () => [],
}) {
  // Какое задание показывает вкладка — вычисляется, а не хранится.
  //
  // masterDiscussionProposalId обнуляется ровно тогда, когда точное задание
  // становится готовым (main.js, разбор сообщения 'master'), и вкладка на нём
  // исчезала бы в момент готовности — то есть когда она нужнее всего.
  // Порядок: живой ответ хода, потом последняя реплика с меткой предложения,
  // и только потом сохранённый признак обсуждения.
  // Предложение без brief панели тоже принадлежит. Прежде вкладка требовала
  // brief, и квест старого ключевого маршрута оставался вовсе без неё: в ленту
  // он входил полной карточкой, а открыть его состав было негде. Маршрут снят
  // (internal/orchestrator/chat.go), но сохранённые предложения переживают
  // правку, и остаться им без двери нельзя.
  function masterBriefProposal() {
    const live = ui.masterData?.response?.proposal
    // Три ответа, а не два: «вот оно», «решено — панели не принадлежит» и «в
    // хранилище его нет». Пока их было два, отклонённое предложение возвращало
    // null и запасной путь подставлял вместо него снимок хода — а снимок
    // остаётся прежним и после отказа, и панель показывала карточку с рабочей
    // кнопкой «Запустить» у квеста, от которого только что отказались.
    const pick = id => {
      const stored = taskProposalById(String(id || ''))
      if (!stored) return undefined
      if (RESOLVED.has(stored.status)) return null
      // Живое и сохранённое сливаются так же, как в ленте: состав отряда и
      // причина выбора живут в ответе и до истории не доезжают.
      return live && String(live.id) === String(stored.id) ? { ...live, ...stored } : stored
    }
    if (live && !RESOLVED.has(live.status)) {
      const chosen = pick(live.id)
      return chosen === undefined ? live : chosen
    }
    const history = ui.masterData?.history || []
    for (let index = history.length - 1; index >= 0; index -= 1) {
      const id = history[index]?.proposalId
      if (!id) continue
      return pick(id) || null
    }
    return pick(ui.masterDiscussionProposalId) || null
  }

  // Что вкладка говорит о задании справа от его имени.
  //
  // До запуска это готовность, после — прогресс: «3 из 7». Число на вкладке —
  // весь прогресс, который помещается в шапку разговора, и оно же обещает, что
  // за вкладкой есть перечень. Пока плана нет (ядро собирает его при запуске),
  // говорим состояние работы, а не «0 из 0».
  function briefTabState(item) {
    if (item.status !== 'started') return taskBriefStateLabel(item.brief, rosterHasAgent())
    const live = startedQuestSummary?.(item.id)
    const progress = questPlanProgress(live?.rows)
    if (progress.total) return `${progress.done} из ${progress.total}`
    return live?.statusText || 'выполняется'
  }

  function proposalWorkOrder(item) {
    const id = String(item?.id || '')
    return (ui.masterData?.workOrders || []).find(order => order?.id === `workorder-${id}`)
  }

  function masterBriefTabHtml() {
    const item = masterBriefProposal()
    if (!item) return ''
    const started = item.status === 'started'
    const order = proposalWorkOrder(item)
    const ready = !started && order?.state === 'ready'
    const goal = String(item.brief?.goal || item.title || '')
    const open = Boolean(ui.masterBriefPanelOpen)
    return `<div class="hall-brief-tabs" id="master-brief-tabs" role="tablist" data-keynav="row" aria-label="Панели разговора">
      <button type="button" role="tab" tabindex="0" id="master-brief-tab" class="hall-brief-tab${open ? ' is-on' : ''}${ready ? ' is-ready' : ''}${started ? ' is-live' : ''}" aria-selected="${open ? 'true' : 'false'}" aria-controls="master-brief-panel" data-action="master-brief-toggle" title="${esc(goal)}"><span>${esc(shortGoal(goal))}</span><small>${esc(order?.state === 'staffing' ? 'Собираем состав' : briefTabState(item))}</small></button>
    </div>`
  }

  // Этапы запущенного квеста: на каком он сейчас и какие позади.
  //
  // Перечень стоял в ленте, в карточке запущенной работы, и уезжал прокруткой
  // вместе с ней. Здесь он держится у края экрана, пока работа идёт.
  function startedBriefBodyHtml(item, opts) {
    const live = startedQuestSummary?.(item.id)
    const plan = masterPlanHtml('Этапы', live?.rows, esc, { limit: 12 })
      // Плана нет только до того, как ядро соберёт прогон. Молчать об этом
      // нельзя: пустая панель у запущенного квеста читается как поломка.
      || '<p class="hall-brief-wait">Этапы появятся, когда Мастер соберёт прогон.</p>'
    // Задание остаётся здесь же и только для чтения: по нему сверяют, что
    // именно ушло в работу. Действий над ним больше нет — ни правки, ни
    // отказа: квест уже идёт, и отменяют его управлением прогоном в ленте.
    const brief = item.brief
      ? `<section class="hall-deck hall-proposal">${taskBriefBodyHtml(item, { ...opts, editing: false, editor: '' })}</section>`
      : ''
    const overview = live?.questId
      ? '<div class="hall-panel-row"><button type="button" class="hall-btn is-sm" data-action="tab" data-tab="overview">Обзор квеста</button></div>'
      : ''
    return `${plan}${brief}${overview}`
  }

  // Какая вкладка панели открыта. Без выбора — квест, если он есть: панель
  // выросла из панели задания, и открывают её чаще всего ради него.
  function activeTab(item) {
    return inspectorTab(ui.masterInspectorTab, item || activeWorkOrder() ? 'quest' : 'team')
  }

  // Наряд, который сейчас исполняется в этом разговоре, — для разговора, где
  // предложения в истории нет (запуск из прежней сессии, агентский режим).
  function activeWorkOrder() {
    return list(ui.masterData?.workOrders)
      .filter(order => order?.state === 'approved' && order?.runtime)
      .sort((a, b) => String(b.runtime?.updatedAt || '').localeCompare(String(a.runtime?.updatedAt || '')))[0] || null
  }

  // Вкладка «Квест»: задание, пока его обсуждают, и этапы, когда оно идёт.
  function questTabHtml(item, open, tab) {
    if (!item) {
      const order = activeWorkOrder()
      if (!order) {
        return `<div class="hall-insp-blank">${icon('quest')}<b>Квеста пока нет</b><span>Опишите задачу — Мастер соберёт задание, и оно появится здесь: цель, условия готовности, этапы и исполнители.</span></div>`
      }
      const rows = list(order.runtime?.stages).map(stage => ({
        text: stage.name || stage.id, state: masterPlanState(stage.status), note: workOrderStageNote(stage),
      }))
      return `<div class="hall-brief-panel-sub"><b>Квест</b><small>${esc(order.goal || '')}</small></div>
        ${masterPlanHtml('Этапы', rows, esc, { limit: 12 }) || '<p class="hall-brief-wait">Этапы появятся, когда Мастер соберёт прогон.</p>'}`
    }
    const started = item.status === 'started'
    const busy = Boolean(ui.proposalStarting?.has(item.id) || ui.proposalModifying?.has(item.id))
    // Редактор брифа рисуется в одном месте. readTaskBriefEditor ищет поля по
    // всему разделу и при двух наборах молча прочтёт первый: открыта вкладка
    // квеста — правка идёт здесь, иначе — в карточке ленты.
    const editing = !started && open && tab === 'quest' && ui.proposalEditId === item.id
    const opts = { esc, countOf, editing, busy, editor: editing ? proposalEditorHtml(item) : '', rosterReady: rosterHasAgent() }
    // Предложение старого маршрута брифа не знает, и разбирать его нечем:
    // taskBriefBodyHtml читает goal, версию и критерии. Рисует его прежняя
    // карточка — та же, что стояла в ленте; кнопка запуска у неё своя, и
    // панель для неё теперь единственное место, где её можно нажать.
    //
    // Состав отряда и причина выбора живут в ответе хода, а не в предложении, и
    // до истории не доезжают: не передав их сюда, панель показала бы имена без
    // единой причины — то есть состав, который нельзя ни оспорить, ни поправить.
    const turn = ui.masterData?.response
    const fromTurn = turn?.proposal && String(turn.proposal.id) === String(item.id) ? turn : null
    const body = started
      ? startedBriefBodyHtml(item, opts)
      : item.brief
        ? `<section class="hall-deck hall-proposal">${taskBriefBodyHtml(item, opts)}${taskBriefActionsHtml(item, { ...opts, withStart: false })}</section>`
        : (legacyProposalHtml?.(item, fromTurn?.partyWhy || item.rationale, fromTurn?.party) || '')
    // Подзаголовок называет род документа, и после запуска он другой:
    // обсуждают задание, идёт — квест. Рядом — состояние работы, а не версия
    // брифа: у запущенного квеста решать нечего.
    const live = started ? startedQuestSummary?.(item.id) : null
    const order = proposalWorkOrder(item)
    const head = started
      ? `<b>Квест</b><small>${esc(live?.statusText || 'выполняется')}${live?.step != null ? ` · ход ${esc(live.step)}` : ''}</small>`
      : `<b>Задание</b><small>${item.brief ? `Версия ${Number(item.brief.version)} · ` : ''}${esc(order?.state === 'staffing' ? 'Собираем состав' : taskBriefStateLabel(item.brief, rosterHasAgent()))}</small>`
    // У задания с брифом род и версию называет шапка самой карточки — второй
    // строкой над ней они повторялись бы слово в слово.
    const sub = started || !item.brief ? `<div class="hall-brief-panel-sub">${head}</div>` : ''
    return `${sub}${body}`
  }

  // Вкладка «Команда»: кто работает, кто в отряде задания, кто есть в проекте.
  function teamGroups(item) {
    const order = (item && proposalWorkOrder(item)) || activeWorkOrder()
    const roster = [...list(order?.roster?.permanent), ...list(order?.roster?.temporary)]
    const member = (id, extra = {}) => {
      const agent = agentById(id)
      const drafted = roster.find(entry => entry?.id === id)
      return {
        id,
        name: agent?.name || drafted?.name || id,
        role: agent?.roleDescription || drafted?.role || '',
        model: agent?.model || '',
        status: agent?.status || (drafted ? 'draft' : ''),
        ...extra,
      }
    }
    const working = list(order?.runtime?.stages)
      .filter(stage => stage?.agentId && ['running', 'waiting', 'waiting_approval'].includes(stage.status))
      .map(stage => member(stage.agentId, { working: true, note: stage.name || 'в работе' }))
    const partyIds = list(item?.teamAgentIds).length
      ? list(item.teamAgentIds)
      : list(ui.masterData?.response?.party).map(entry => entry?.agentId)
    const party = [...partyIds, ...roster.map(entry => entry?.id)].filter(Boolean).map(id => member(id))
    const everyone = list(projectAgents()).map(agent => member(agent.id))
    return masterTeamGroups({ working, party, roster: everyone })
  }

  // Вкладка «Контекст»: что уйдёт со следующей репликой и на что опирался
  // последний ответ.
  function contextTabHtml() {
    const history = list(ui.masterData?.history)
    const last = [...history].reverse().find(entry => entry?.role === 'assistant')
    const entries = list(ui.masterData?.sessions?.memoryEntries)
    const usedMemory = list(last?.memoryIds).map(id => entries.find(entry => entry.id === id)?.content).filter(Boolean)
    const facts = list(last?.factsUsed).length ? last.factsUsed : list(ui.masterData?.response?.facts)
    const model = String(ui.masterData?.sessions?.model || ui.masterData?.config?.model || '')
    return masterContextPanelHtml({
      attachments: masterContextPayload(ui.masterConversationId),
      memory: ui.masterData?.sessions?.memory,
      usedMemory, facts, model, esc,
    })
  }

  function masterBriefPanelHtml() {
    const item = masterBriefProposal()
    const open = Boolean(ui.masterBriefPanelOpen)
    const tab = activeTab(item)
    const groups = teamGroups(item)
    const body = tab === 'team'
      ? masterTeamHtml({ groups, openId: ui.masterInspectorAgent, agentById, esc })
      : tab === 'context'
        ? contextTabHtml()
        : questTabHtml(item, open, tab)
    const working = groups.find(group => group.id === 'working')?.members.length || 0
    return `<aside class="hall-brief-panel" id="master-brief-panel" role="tabpanel" tabindex="-1" aria-labelledby="${item ? 'master-brief-tab' : 'master-inspector-toggle'}"${open ? '' : ' hidden'}>
      <header class="hall-brief-panel-head">${masterInspectorTabsHtml(tab, esc, { team: working })}<button type="button" class="hall-chip hall-brief-panel-drop" data-action="master-brief-close" aria-label="Закрыть панель" title="Закрыть панель">${icon('x')}</button></header>
      <div class="hall-brief-panel-body" id="master-inspector-body" role="tabpanel" aria-labelledby="master-inspector-tab-${tab}">${body}</div>
    </aside>`
  }

  // Точечное обновление вкладки и панели.
  //
  // Ответ хода приходит без полной отрисовки раздела — так задумано, иначе
  // разговор прыгал бы к первой реплике. Без этого вызова вкладка осталась бы
  // с прежним признаком: «обсуждение» над лентой, в которой уже стоит карточка
  // с рабочей кнопкой запуска.
  function syncMasterBriefSurfaces(root) {
    const dialogue = root.querySelector?.('.hall-dialogue')
    if (!dialogue) return
    const tabHtml = masterBriefTabHtml()
    const tabs = root.querySelector('#master-brief-tabs')
    // Шапка экрана чата — новый дом вкладки задания. Старая полоса
    // остаётся запасным якорем: в узком режиме шапки может не быть.
    const toolbar = root.querySelector?.('.hall-head-chat') || dialogue.querySelector?.('.hall-session-toolbar')
    if (tabs) tabs.outerHTML = tabHtml
    else if (tabHtml && toolbar) toolbar.insertAdjacentHTML?.('beforeend', tabHtml)
    const panelHtml = masterBriefPanelHtml()
    const panel = root.querySelector('#master-brief-panel')
    // Пока в панели набирают, разметку не трогаем: между открытием редактора
    // брифа и нажатием «Сохранить» приходит фоновое обновление — индекс,
    // очередь решений, ответ хода, — и замена узла стоила бы каретки посреди
    // слова. Значения переживают замену сами (черновик пишется на каждом
    // вводе), место в строке — нет.
    const editingHere = panel && panel.contains?.(globalThis.document?.activeElement)
    if (panel && !editingHere) panel.outerHTML = panelHtml
    else if (!panel && panelHtml) dialogue.insertAdjacentHTML?.('beforeend', panelHtml)
    dialogue.classList?.toggle('is-brief-open', Boolean(panelHtml) && Boolean(ui.masterBriefPanelOpen))
  }

  function applyOpen(root, open) {
    const panel = root.querySelector('#master-brief-panel')
    if (!panel) return false
    const tab = root.querySelector('#master-brief-tab')
    const toggle = root.querySelector('#master-inspector-toggle')
    ui.masterBriefPanelOpen = open
    root.querySelector('.hall-dialogue')?.classList?.toggle('is-brief-open', open)
    panel.hidden = !open
    tab?.classList?.toggle('is-on', open)
    tab?.setAttribute?.('aria-selected', open ? 'true' : 'false')
    toggle?.setAttribute?.('aria-pressed', open ? 'true' : 'false')
    // Фокус не остаётся на скрытом узле: закрывая панель, возвращаем его на
    // вкладку задания, а без неё — на кнопку панели в шапке.
    if (!open) (tab || toggle)?.focus?.()
    return true
  }

  // Смена вкладки перерисовывает только панель: лента и поле ввода остаются
  // нетронутыми, как и при открытии.
  function redrawPanel(root, focusTab) {
    const panel = root.querySelector('#master-brief-panel')
    if (!panel) return false
    panel.outerHTML = masterBriefPanelHtml()
    applyOpen(root, true)
    if (focusTab) root.querySelector(`#master-inspector-tab-${focusTab}`)?.focus?.()
    return true
  }

  function closeMasterBriefPanel(root, persist) {
    if (!ui.masterBriefPanelOpen) return false
    if (!applyOpen(root, false)) return false
    persist?.()
    return true
  }

  // Переключение идёт правкой узлов, а не отрисовкой раздела: полная отрисовка
  // пересобрала бы ленту и поле ввода ради одного класса.
  function handleMasterBriefAction({ action, target, root, persist }) {
    // Полоса квеста над полем ввода ведёт к подробностям. Панель квеста есть у
    // разговора, где задание обсуждали; нет её — ведём к карточке наряда в ленте.
    if (action === 'master-inspector-open') {
      if (target?.dataset?.tab) ui.masterInspectorTab = inspectorTab(target.dataset.tab)
      if (redrawPanel(root)) {
        persist?.()
        return true
      }
      const id = String(target?.dataset?.order || '')
      const card = id ? root.querySelector?.(`[data-work-order-id="${id}"]`) : null
      card?.scrollIntoView?.({ block: 'center' })
      return true
    }
    if (action === 'master-inspector-tab') {
      ui.masterInspectorTab = inspectorTab(target?.dataset?.tab)
      redrawPanel(root, ui.masterInspectorTab)
      return true
    }
    // Строка агента раскрывает его лист на месте; вторая — сворачивает.
    if (action === 'master-inspector-agent') {
      const id = String(target?.dataset?.id || '')
      ui.masterInspectorAgent = ui.masterInspectorAgent === id ? '' : id
      ui.masterInspectorTab = 'team'
      redrawPanel(root)
      persist?.()
      return true
    }
    if (action !== 'master-brief-toggle' && action !== 'master-brief-close') return false
    applyOpen(root, action === 'master-brief-toggle' ? !ui.masterBriefPanelOpen : false)
    persist?.()
    return true
  }

  return {
    closeMasterBriefPanel,
    handleMasterBriefAction,
    masterBriefPanelHtml,
    masterBriefProposal,
    masterBriefTabHtml,
    syncMasterBriefSurfaces,
  }
}
