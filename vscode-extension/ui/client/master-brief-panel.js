import { masterPlanHtml, questPlanProgress } from './master-plan-views.js'

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

  function masterBriefPanelHtml() {
    const item = masterBriefProposal()
    if (!item) return ''
    const open = Boolean(ui.masterBriefPanelOpen)
    const started = item.status === 'started'
    const busy = Boolean(ui.proposalStarting?.has(item.id) || ui.proposalModifying?.has(item.id))
    // Редактор брифа рисуется в одном месте. readTaskBriefEditor ищет поля по
    // всему разделу и при двух наборах молча прочтёт первый: панель открыта —
    // правка идёт здесь, закрыта — в карточке ленты.
    const editing = !started && open && ui.proposalEditId === item.id
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
    // Шапка называет род документа, и после запуска он другой: обсуждают
    // задание, идёт — квест. Слева от него состояние работы, а не версия
    // брифа: версия решала, то ли утверждают, что читают, и у запущенного
    // квеста решать нечего.
    const live = started ? startedQuestSummary?.(item.id) : null
    const order = proposalWorkOrder(item)
    const head = started
      ? `<b>Квест</b><small>${esc(live?.statusText || 'выполняется')}${live?.step != null ? ` · ход ${esc(live.step)}` : ''}</small>`
      : `<b>Задание</b><small>${item.brief ? `Версия ${Number(item.brief.version)} · ` : ''}${esc(order?.state === 'staffing' ? 'Собираем состав' : taskBriefStateLabel(item.brief, rosterHasAgent()))}</small>`
    return `<aside class="hall-brief-panel" id="master-brief-panel" role="tabpanel" tabindex="-1" aria-labelledby="master-brief-tab"${open ? '' : ' hidden'}>
      <header class="hall-brief-panel-head">${head}<button type="button" class="hall-chip hall-brief-panel-drop" data-action="master-brief-close" aria-label="Закрыть панель задания">×</button></header>
      <div class="hall-brief-panel-body">${body}</div>
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
    const tab = root.querySelector('#master-brief-tab')
    if (!panel || !tab) return false
    ui.masterBriefPanelOpen = open
    root.querySelector('.hall-dialogue')?.classList?.toggle('is-brief-open', open)
    panel.hidden = !open
    tab.classList?.toggle('is-on', open)
    tab.setAttribute('aria-selected', open ? 'true' : 'false')
    // Фокус не остаётся на скрытом узле: закрывая панель, возвращаем его на
    // вкладку, которой её и открывали.
    if (!open) tab.focus?.()
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
  function handleMasterBriefAction({ action, root, persist }) {
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
