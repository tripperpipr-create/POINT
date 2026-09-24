// Клик-ветки разговора с Мастером и карточки наряда.
//
// Двадцать три ветки об одном: лента, поиск по ней, ответы и управление
// нарядом. В общем обработчике они лежали вперемешку с Docker, Гильдией и
// прогонами и читались только вместе со всем файлом.
//
// Границу держит `masterClient`: активная беседа, её черновики и ленты — его,
// а не глобального состояния. Ветка ничего не решает сама, она доносит
// намерение до ядра и просит перерисовку.

import { closeMasterMention, masterMentionState } from './master-mention-ui.js'
import { masterAgentConsent } from './master-agent-card.js'
import { icon } from './ui-icons.js'

export function handleMasterClickAction({ action, target, ui, applyMasterFind, forgetMasterSent, masterAskBefore, masterClient, masterMessageById, persistDraft, pickMasterMention, render, root, sendMasterMessage, stopMasterWaitClock, updateMasterScrollCue, vscode }) {
	if (['master-development-load','master-development-toggle','master-development-rollback'].includes(action)) {
	  if (ui.masterDevelopmentBusy) return true
	  ui.masterDevelopmentBusy = true
	  ui.masterDevelopmentError = ''
	  const projectKey = ui.projectKey
	  if (action === 'master-development-load') vscode.postMessage({type:'loadMasterDevelopment',projectKey})
	  if (action === 'master-development-toggle') vscode.postMessage({type:'setMasterLearning',enabled:!ui.masterDevelopment?.config?.enabled,projectKey})
	  if (action === 'master-development-rollback') vscode.postMessage({type:'rollbackMasterSkill',id:String(target.dataset.id || ''),projectKey})
	  render()
	  return true
	}
  if (action === 'master-ask') {
    // Курсор ставится не здесь: отрисовка отложена до кадра, и поле, которому
    // мы бы его задали, к тому времени уже заменено новым. Раньше каретка
    // оставалась в начале — дописанное уточнение оказывалось перед вопросом.
    ui.masterDraft = target.dataset.question || ''
    ui.masterCaretToEnd = true
    persistDraft()
    render()
    return true
  }
  if (action === 'master-send-prompt') {
    sendMasterMessage(target.dataset.message || '')
    return true
  }
  if (action === 'approve-master-work-order-v2') {
    const id=String(target.dataset.id || '')
    if (!id || ui.masterWorkOrderBusy.has(id)) return
    const order=(Array.isArray(ui.masterData?.workOrders)?ui.masterData.workOrders:[]).find(item=>item.id===id)
    const rosterConsent=(order?.roster?.permanent || []).filter(draft=>draft?.requiresConsent && !draft?.existing).map(draft=>String(draft.id || ''))
    ui.masterWorkOrderBusy.add(id)
    masterAgentConsent.delete(id)
    const idempotencyKey=globalThis.crypto?.randomUUID?.() || `approve-${Date.now()}-${Math.random().toString(36).slice(2)}`
    vscode.postMessage({type:'approveMasterWorkOrderV2',workOrderId:id,version:Number(target.dataset.version),digest:String(target.dataset.digest || ''),idempotencyKey,rosterConsent,turnId:masterClient.turns[masterClient.active]?.id})
    render()
    return true
  }
  if (action === 'save-master-work-order-v2') {
    const id=String(target.dataset.id || '')
    const order=(Array.isArray(ui.masterData?.workOrders)?ui.masterData.workOrders:[]).find(item=>item.id===id)
    const card=target.closest?.('.master-v2-order')
    if (!id || !order || !card || ui.masterWorkOrderBusy.has(id)) return
    try {
      const draft=JSON.parse(JSON.stringify(order))
      delete draft.digest;delete draft.runtime;delete draft.approvedVersion;delete draft.approvedDigest
      const lineValues=name=>String(card.querySelector(`[data-work-order-field="${name}"]`)?.value || '').split(/\r?\n/).map(value=>value.trim()).filter(Boolean)
      draft.goal=String(card.querySelector('[data-work-order-field="goal"]')?.value || '').trim()
      draft.scope=lineValues('scope')
      draft.assumptions=lineValues('assumptions')
      draft.outOfScope=lineValues('outOfScope')
      for (const input of card.querySelectorAll('[data-work-order-json]')) {
        const field=String(input.dataset.workOrderJson || '')
        if (field) draft[field]=JSON.parse(String(input.value || 'null'))
      }
      ui.masterWorkOrderBusy.add(id)
      const idempotencyKey=globalThis.crypto?.randomUUID?.() || `revise-${Date.now()}-${Math.random().toString(36).slice(2)}`
      vscode.postMessage({type:'reviseMasterWorkOrderV2',workOrderId:id,expectedVersion:Number(order.version),expectedDigest:String(order.digest || ''),idempotencyKey,workOrder:draft})
      render()
    } catch (error) {
      ui.masterComposeNote=`Карточка не сохранена: ${error instanceof Error?error.message:String(error)}`
      render()
    }
    return true
  }
  if (action === 'control-master-work-order-v2') {
    const id=String(target.dataset.id || '')
    const questId=String(target.dataset.questId || '')
    const control=String(target.dataset.control || '')
    // Занятый наряд — обработанный клик: повторное нажатие «Паузы» не должно
    // уходить дальше по цепочке обработчиков только потому, что первое ещё идёт.
    if (!id || !questId || !['pause','resume','cancel','message'].includes(control) || ui.masterWorkOrderBusy.has(id)) return true
    // Запущенный наряд — прогон, а не карточка: только `.master-v2-order` терял бы поле сообщения ровно там, где оно и нужно.
    const card=target.closest?.('.master-v2-order, .master-v2-run')
    const message=control==='message' ? String(card?.querySelector?.('[data-work-order-message]')?.value || '').trim() : ''
    if (control==='message' && !message) { ui.masterComposeNote='Введите сообщение активному квесту';render();return true }
    ui.masterWorkOrderBusy.add(id)
    vscode.postMessage({type:'controlMasterWorkOrderQuestV2',workOrderId:id,questId,action:control,message})
    render()
    return true
  }
  if (action === 'control-master-application-v2') {
    const id=String(target.dataset.id || '')
    const questId=String(target.dataset.questId || '')
    const control=String(target.dataset.control || '')
    if (!id || !questId || !['start','stop'].includes(control) || ui.masterWorkOrderBusy.has(id)) return
    ui.masterWorkOrderBusy.add(id)
    const idempotencyKey=globalThis.crypto?.randomUUID?.() || `application-${Date.now()}-${Math.random().toString(36).slice(2)}`
    vscode.postMessage({type:'controlMasterApplicationV2',workOrderId:id,questId,action:control,version:Number(target.dataset.version),digest:String(target.dataset.digest || ''),deliveryReceiptId:String(target.dataset.receiptId || ''),idempotencyKey})
    render()
    return true
  }
  if (action === 'generate-work-order-report') {
    const id=String(target.dataset.id || '')
    const order=(Array.isArray(ui.masterData?.workOrders)?ui.masterData.workOrders:[]).find(item=>item.id===id)
    if (!order) return true
    const evidence=order.runtime?.evidence || {}
    const safeFacts={
      goal:order.goal || '', status:order.runtime?.status || '', criteria:order.criteria || [],
      verificationChecks:evidence.verificationChecks || [], changedFiles:evidence.changedFiles || [],
      knownLimitations:evidence.knownLimitations || [], deliveryUrl:order.runtime?.deliveryReceipt?.url || '',
    }
    const prompt=`Собери итоговый отчёт по завершённому плану для владельца проекта. Начни с результата и решения, затем покажи выполненные критерии, проверки, изменения, ограничения и следующие шаги. Не выдумывай факты или ссылки.\n\nФакты, которые я проверю перед отправкой:\n${JSON.stringify(safeFacts,null,2)}`
    vscode.postMessage({type:'generateReport',prompt})
    return true
  }
  if (action === 'revise-master-work-order-v2') {
    ui.masterDraft='Измени карточку запуска: '
    ui.masterCaretToEnd=true
    persistDraft();render()
    return true
  }
  if (action === 'copy-master-message') {
    const item = masterMessageById(target.dataset.id)
    if (!item) return true
    vscode.postMessage({ type: 'copyMasterText', text: String(item.content || '') })
    // Отклик на месте нажатия: строка в строке состояния IDE далеко от
    // реплики, и без галочки второе нажатие казалось нужным.
    const label = target.getAttribute?.('aria-label')
    target.innerHTML = icon('check')
    target.classList?.add('is-done')
    target.setAttribute?.('aria-label', 'Скопировано')
    target.setAttribute?.('title', 'Скопировано')
    setTimeout(() => {
      if (!target.isConnected) return
      target.innerHTML = icon('copy')
      target.classList.remove('is-done')
      target.setAttribute('aria-label', label || 'Копировать')
      target.setAttribute('title', label || 'Копировать')
    }, 1400)
    return true
  }
  if (action === 'regenerate-master-message') {
    // «Ответить иначе» переспрашивает тот же вопрос с признаком повтора —
    // иначе модель вернёт тот же ответ слово в слово. Ветвление ленты —
    // отдельная кнопка master-fork-message.
    sendMasterMessage(target.dataset.message || '', { retry: true })
    return true
  }
  if (action === 'master-fork-message') {
    const anchor = target.dataset.id
    if (anchor) vscode.postMessage({ type: 'forkMasterConversation', messageId: anchor, regenerate: false, draft: target.dataset.message || '' })
    return true
  }
  if (action === 'master-message-details') {
    const id = String(target.dataset.id || '')
    const item = masterMessageById(id)
    // Окно объясняет ответ, и без вопроса объяснять нечего: показываем реплику
    // человека, на которую отвечали, а не «запрос не найден».
    if (item) vscode.postMessage({ type: 'openMasterMessageDetails', item, request: masterAskBefore(id) })
    return true
  }
  if (action === 'master-feedback') {
    const id = String(target.dataset.id || '')
    const item = masterMessageById(id)
    if (!item) return
    // Повторное нажатие снимает отметку: передумать можно, и «полезно» второй
    // раз значит именно это, а не подтверждение.
    const value = item.feedback === target.dataset.value ? '' : target.dataset.value
    vscode.postMessage({ type: 'masterFeedback', messageId: id, value })
    return true
  }
  if (action === 'master-find-open') {
    ui.masterFindOpen = true
    render()
    // Раскрыли — значит собираются искать: второй клик по полю лишний.
    // preventScroll обязателен: фокус без него утаскивает ленту (договорённость 19).
    root.querySelector('#master-find')?.focus({ preventScroll: true })
    return true
  }
  if (action === 'master-find-clear') {
    ui.masterFindQuery = ''
    masterClient.query = ''
    vscode.postMessage({type:'masterPage',conversationId:masterClient.active})
    ui.masterFindOpen = false
    ui.masterFindIndex = 0
    render()
    return true
  }
  if (action === 'master-find-step') {
    ui.masterFindIndex += Number(target.dataset.step || 1)
    applyMasterFind(true)
    return true
  }
  if (action === 'master-scroll-latest') {
    const thread = root.querySelector('#master-thread')
    if (thread) thread.scrollTop = thread.scrollHeight
    ui.masterAutoFollow = true
    updateMasterScrollCue()
    return true
  }
  if (action === 'master-load-earlier') {
    ui.masterLoadingEarlier = true
    // Полный хвост за один запрос: страничная догрузка вверх на ленте с
    // «липкими» датами дороже, чем весь разговор целиком.
    vscode.postMessage({ type: 'loadMaster', full: true, conversationId: masterClient.active })
    render()
    return true
  }
  if (action === 'master-new-discussion') {
    ui.masterDiscussionProposalId = ''
    if (ui.masterData?.response?.proposal) {
      ui.masterData = { ...ui.masterData, response: { ...ui.masterData.response, proposal: undefined } }
    }
    persistDraft()
    render()
    return true
  }
  if (action === 'master-mention-pick') {
    // Мышью — то же, что Enter с клавиатуры: путь берётся из строки, а «@» с
    // запросом уходит из черновика.
    const { at, query } = masterMentionState()
    closeMasterMention()
    pickMasterMention({ path: target.dataset.path || '' }, at, query)
    return true
  }
  if (action === 'master-step-expand') {
    const key = String(target.dataset.key || '')
    if (!key) return
    if (ui.masterExpandedSteps.has(key)) ui.masterExpandedSteps.delete(key)
    else ui.masterExpandedSteps.add(key)
    render()
    return true
  }
  if (action === 'retry-master') {
    // Тот же путь, что у очереди решений: 'idle' — единственное состояние, из
    // которого раздел сам запрашивает переписку. Повторяет человек, не таймер.
    ui.masterStatus = 'idle'
    render()
    return true
  }
  if (action === 'stop-master-chat') {
    // Без turnId расширение не звало отмену вовсе: `if (message.turnId)` не
    // срабатывал, и кнопка только снимала local-замок. Теперь ход гасится и в ядре.
    vscode.postMessage({ type: 'stopMasterChat', turnId: masterClient.turns[masterClient.active]?.id || '' })
    ui.masterSending = false
    // Отправленная реплика не забывается здесь: остановленный ход остаётся в
    // ленте с тем, что успел написать, и реплика человека над ним должна
    // дожить до истории. Забудет её конец хода (turnFinished).
    stopMasterWaitClock()
    // Честно: отмена ушла, но ядро могло успеть довести ход до конца.
    ui.masterComposeNote = 'Ядро могло довести его до конца. Частичный текст останется в разговоре.'
    render()
    return true
  }
  // Ответ прервался — тот же вопрос заново. Не «ответить иначе» (retry): ход не
  // был плохим, он не дошёл, и просить у модели другой путь было бы неправдой.
  if (action === 'master-retry-turn') {
    const text = String(target.dataset.message || '').trim()
    if (text && !ui.masterSending) sendMasterMessage(text)
    return true
  }
  if (action === 'master-send') {
    sendMasterMessage()
    return true
  }
  return false
}
