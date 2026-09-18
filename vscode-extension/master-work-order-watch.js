// Карточка утверждённого наряда живёт дольше одного ответа ядра: проверка
// окружения, планировщик milestone и сам Flow идут минутами. Пока карточку
// обновлял только ответ на нажатие, «Проверяем окружение» замирало навсегда —
// независимо от того, что на самом деле делал квест. Здесь она опрашивается,
// пока состояние переходное, и гаснет сама на исходе.
const TRANSIENT = new Set(['preflight', 'running', 'verifying', 'applying', 'awaiting_user'])
const POLL_MS = 2500
const MAX_FAILURES = 5

function runtimeSignature(runtime) {
  if (!runtime) return 'none'
  return [
    runtime.status || '', runtime.message || '', runtime.flowRunId || '',
    runtime.evidence?.id || '', runtime.deliveryReceipt?.id || '',
    (runtime.milestones || []).map(item => `${item.id || ''}:${item.status || ''}`).join(','),
    // Переход этапа — это и есть ход работы. Без подписи этапов наблюдатель
    // спал, пока не сменится статус квеста: экран выполнения показывал первый
    // же снимок и не двигался до самого исхода.
    (runtime.stages || []).map(item => `${item.id || ''}:${item.status || ''}:${item.runId || ''}`).join(','),
    runtime.stall ? `${runtime.stall.nodeId || ''}:${runtime.stall.waitReason || ''}` : '',
  ].join('|')
}

// Прогон, чью хронику показывает экран выполнения. Идущий этап важнее
// завершённых: их история у экрана уже есть, а текущая работа — нет.
function activeStageRunId(order) {
  const stages = order?.runtime?.stages || []
  const active = stages.find(item => item.status === 'running' || item.status === 'waiting' || item.status === 'waiting_approval')
    || stages.filter(item => item.runId).at(-1)
  return String(active?.runId || '')
}

function isTransientWorkOrder(order) {
  return TRANSIENT.has(String(order?.runtime?.status || ''))
}

// Наблюдение одно на наряд: три нажатия «Повторить запуск» не должны
// превращаться в три опроса одного и того же.
function watchMasterWorkOrder(host, workOrderId, conversationId) {
  const id = String(workOrderId || '')
  if (!id || !host?.service) return
  host.masterWorkOrderWatchers ||= new Map()
  if (host.masterWorkOrderWatchers.has(id)) return host.masterWorkOrderWatchers.get(id)
  const promise = (async () => {
    let failures = 0
    let signature = ''
    while (true) {
      await new Promise(resolve => setTimeout(resolve, POLL_MS))
      let order
      try {
        order = await host.service.request('/api/v2/work-orders/' + encodeURIComponent(id))
        failures = 0
      } catch (error) {
        // Ядро могло перезапуститься под нами (смена песочницы это делает).
        // Пара неудач подряд — не повод бросать карточку.
        if (++failures >= MAX_FAILURES) throw error
        continue
      }
      const next = runtimeSignature(order?.runtime)
      if (next !== signature) {
        signature = next
        host.post({ type: 'masterWorkOrder', workOrder: order, conversationId })
        // Этапы карточка привезла сама, а рассуждения и вызовы инструментов
        // живут в прогоне. Наблюдатель зовёт готовый конвейер расширения: он
        // сам шлёт runDelta и наполняет state.details, без которого хроника на
        // экране выполнения остаётся пустой навсегда.
        const runId = activeStageRunId(order)
        if (runId && typeof host.loadRun === 'function') {
          try { await host.loadRun(runId, true, true) } catch { /* прогон мог уже уехать */ }
        }
      }
      if (!isTransientWorkOrder(order)) return
    }
  })().catch(error => {
    host.service.hostLog('warn', `[chat] наблюдение за нарядом ${id} прервано: ${String(error?.message || error).slice(0, 200)}`)
  }).finally(() => host.masterWorkOrderWatchers.delete(id))
  host.masterWorkOrderWatchers.set(id, promise)
  return promise
}

// История разговора приходит вместе с нарядами: открытая заново вкладка тоже
// должна показывать живой квест, а не снимок на момент загрузки.
function watchMasterWorkOrders(host, master) {
  const conversationId = master?.sessions?.active
  for (const order of master?.workOrders || []) {
    if (isTransientWorkOrder(order)) watchMasterWorkOrder(host, order.id, conversationId)
  }
}

module.exports = { watchMasterWorkOrder, watchMasterWorkOrders, isTransientWorkOrder, activeStageRunId, runtimeSignature }
