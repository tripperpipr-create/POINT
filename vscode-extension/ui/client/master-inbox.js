// Ответы Мастера: ход разговора, наряды и вложения.
//
// Тринадцать веток разбора входящих, которые до 19 сентября стояли посреди
// девяноста чужих. Вместе их держит одно: у каждой на другом конце —
// `masterClient` из `master-chat-state.js` и лента из `master-thread-views.js`.
// Первая строка — сторож: ответ на отменённый запрос приходит позже нового и
// перезаписал бы свежий разговор старым.
//
// Состояние приходит общим мешком `ui`, как в `companion-transport.js`.

const MASTER_MESSAGES = new Set([
	'masterDevelopment', 'masterDevelopmentError',
  'master', 'masterTurn', 'masterEvent',
  'masterStreamError', 'masterWorkOrder', 'masterWorkOrderApproved',
  'masterWorkOrderDeleted', 'masterWorkOrderRevised', 'masterWorkOrderControlled',
  'masterApplicationControlled', 'masterPage', 'masterContextSuggestions',
  'masterContext',
])

export function createMasterInbox({
  ui,
  root,
  vscode,
  render,
  persistDraft,
  masterClient,
  masterSessionDrafts,
  masterTraceMindPatch,
  masterStream,
  masterSentText,
  acceptMasterMentionItems,
  receiveMasterContext,
  clearMasterContext,
  forgetMasterSent,
  replaceMasterThreadHtml,
  syncMasterComposeState,
  stopMasterWaitClock,
  sendMasterMessage,
  applyMasterFind,
}) {
  return function applyMasterMessage(message) {
    if (!MASTER_MESSAGES.has(message.type)) return false
	  if (message.type === 'masterDevelopment' || message.type === 'masterDevelopmentError') {
	    if (message.projectKey !== ui.projectKey) return true
	    ui.masterDevelopmentBusy = false
	    ui.masterDevelopmentError = message.error || ''
	    if (message.development) ui.masterDevelopment = message.development
	    render()
	    return true
	  }
      if (message.type==='master' && message.requestId && message.requestId!==ui.masterRequestId) return true
      if (message.type==='masterTurn') {masterClient.acceptTurn(message.turn);if(message.turn.conversationId===masterClient.active){ui.masterSending=masterClient.running();render()};persistDraft()}
      // Событие хода правит только его блок в ленте (master-stream-view.js):
      // текст — тело ответа, мысль — свою строку следа, остальное — блок.
      if (message.type==='masterEvent') {
        masterClient.acceptEvent(message.event)
        if(message.event.conversationId===masterClient.active){
          ui.masterSending=masterClient.running()
          if(message.event.type==='reasoning'&&masterTraceMindPatch(root,masterClient.turns[masterClient.active],ui.masterAutoFollow)) {}
          else masterStream.accept(message.event.type)
        }
      }
      // Поток оборвался: написанное и вопрос остаются в ленте со строкой сбоя,
      // а не причиной под полем ввода — там её читали, уже потеряв ответ.
      if (message.type==='masterStreamError' && message.conversationId===masterClient.active){masterClient.fail(masterClient.active,message.message,masterSentText());ui.masterSending=false;stopMasterWaitClock();if(!replaceMasterThreadHtml())render();syncMasterComposeState()}
      if (message.type==='masterWorkOrder') {
        // Наблюдение за живым квестом продолжается и после перехода в другой
        // разговор: его обновление не должно подкладывать чужую карточку.
        if(message.conversationId && message.conversationId!==masterClient.active) return true
        const current=Array.isArray(ui.masterData?.workOrders)?ui.masterData.workOrders:[]
        ui.masterData={...(ui.masterData || {}),workOrders:[message.workOrder,...current.filter(item=>item.id!==message.workOrder.id)]}
        if (ui.hiringReloadFor && ui.hiringReloadFor === message.workOrder?.id) {
          ui.hiringReloadFor = ''
          vscode.postMessage({ type: 'loadMaster', conversationId: masterClient.active })
        }
        render()
      }
      if (message.type==='masterWorkOrderApproved') {
        const order=message.approval?.workOrder
        if(order){ui.masterWorkOrderBusy.delete(order.id);const current=Array.isArray(ui.masterData?.workOrders)?ui.masterData.workOrders:[];ui.masterData={...(ui.masterData || {}),workOrders:[order,...current.filter(item=>item.id!==order.id)]}}
		ui.masterComposeNote=''
        render()
      }
      if (message.type==='masterWorkOrderDeleted') {
        const id=String(message.workOrderId || '');const current=Array.isArray(ui.masterData?.workOrders)?ui.masterData.workOrders:[]
        ui.masterData={...(ui.masterData || {}),workOrders:current.filter(item=>item.id!==id)};ui.masterComposeNote='Наряд убран';render()
      }
      if (message.type==='masterWorkOrderRevised') {
        const order=message.workOrder
        if(order){ui.masterWorkOrderBusy.delete(order.id);const current=Array.isArray(ui.masterData?.workOrders)?ui.masterData.workOrders:[];ui.masterData={...(ui.masterData || {}),workOrders:[order,...current.filter(item=>item.id!==order.id)]}}
        ui.masterComposeNote=''
        render()
      }
      if (message.type==='masterWorkOrderControlled') {
        const order=message.workOrder
        if(order){ui.masterWorkOrderBusy.delete(order.id);const current=Array.isArray(ui.masterData?.workOrders)?ui.masterData.workOrders:[];ui.masterData={...(ui.masterData || {}),workOrders:[order,...current.filter(item=>item.id!==order.id)]}}
        ui.masterComposeNote=''
        render()
      }
      if (message.type==='masterApplicationControlled') {
        const order=message.workOrder
        if(order){ui.masterWorkOrderBusy.delete(order.id);const current=Array.isArray(ui.masterData?.workOrders)?ui.masterData.workOrders:[];ui.masterData={...(ui.masterData || {}),workOrders:[order,...current.filter(item=>item.id!==order.id)]}}
        ui.masterComposeNote=''
        render()
      }
      if (message.type==='masterPage' && message.conversationId===masterClient.active && (message.query || '')===masterClient.query){const items=message.page.items || [];ui.masterData.history=items;ui.masterData.paginated=true;ui.masterData.before=message.page.before;ui.masterData.truncated=message.page.hasMore;ui.masterLoadingEarlier=false;replaceMasterThreadHtml();syncMasterComposeState();applyMasterFind()}
      if (message.type === 'masterContextSuggestions') { if (acceptMasterMentionItems(message.query, message.items)) render() }
      if (message.type === 'masterContext') { try {receiveMasterContext(message);ui.masterDraft=ui.masterDraft.replace(/@$/, '');persistDraft();render()} catch(error){ui.masterComposeNote=error.message;render()} }
      if (message.type === 'master') {
        if(message.turn) masterClient.acceptTurn(message.turn)
        for(const turn of message.master?.activeTurns || []) masterClient.acceptTurn(turn)
        const incomingConversation=message.master?.sessions?.active
        if(message.turnFinished && incomingConversation && incomingConversation!==masterClient.active) {persistDraft();return}
        if(message.sessionChanged || message.loaded){masterClient.restoreScroll=masterClient.scroll[incomingConversation] ?? Infinity;masterClient.query='';ui.masterFindQuery=''}
        masterClient.active=incomingConversation || masterClient.active
        if (message.turnFinished) {
          masterClient.settle(incomingConversation || masterClient.active)
          clearMasterContext(ui.masterData?.sessions?.active)
          // Все чипы, а не первый: querySelector возвращал один узел, и после хода
          // с тремя вложениями на экране оставалось два призрака.
          root.querySelectorAll?.('.hall-context-file')?.forEach?.(node => node.remove?.())
          const activeId = incomingConversation || masterClient.active
          const current = masterClient.turns[activeId]
          if (current && !message.turn) masterClient.acceptTurn({ ...current, status: 'done', settled: true })
        }
        const previousSession = ui.masterData?.sessions?.active
        if (message.sessionChanged && previousSession) masterSessionDrafts[previousSession] = ui.masterDraft
        if (message.sessionChanged && previousSession !== message.master?.sessions?.active) ui.masterDiscussionProposalId = ''
        const sessionTitleChanged = JSON.stringify(ui.masterData?.sessions?.items) !== JSON.stringify(message.master?.sessions?.items)
        ui.masterData = message.master
        if(message.regenerate)setTimeout(()=>sendMasterMessage(message.draft || '',{retry:true}),0)
        const nextProposal = ui.masterData?.response?.proposal
        if (nextProposal?.brief) {
          ui.masterDiscussionProposalId = nextProposal.brief.state === 'discussion' || nextProposal.brief.mode === 'project' ? nextProposal.id : ''
        }
        ui.masterStatus = 'ready'
        ui.masterSending = masterClient.running()
        ui.masterComposeNote = ''
        ui.masterLoadingEarlier = false
        stopMasterWaitClock()
        // Черновик поля ход больше не трогает: его очищает сама отправка, а смена
        // разговора — своим сохранённым значением.
        if (message.draft != null || message.sessionChanged || message.loaded) {
          ui.masterDraft = message.draft ?? (masterSessionDrafts[ui.masterData?.sessions?.active] ?? (message.loaded ? ui.masterDraft : ''))
        }
        if (message.turnFinished || message.turn?.status === 'done') forgetMasterSent()
        persistDraft()
        // Причины полной отрисовки перечислены явно. Незаданный Мастер меняет весь
        // раздел, а не ленту; отсутствие ленты означает, что человек смотрит другой
        // раздел и обновлять нечего. Всё остальное — предложения, состав, наём,
        // основания — рисуется внутри самой ленты и переживает точечную замену.
        const needsFullRender = message.sessionChanged || sessionTitleChanged || ui.masterData?.configured === false || !root.querySelector('#master-thread')
        if (needsFullRender) {
          render()
        } else {
          replaceMasterThreadHtml()
          syncMasterComposeState()
        }
      }
    return true
  }
}
