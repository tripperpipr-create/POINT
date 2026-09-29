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
    runtime.waitingForSandbox ? 'sandbox' : '', runtime.resumeAfterRestart ? 'auto' : '',
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

// Пауза ядра — не решение человека. Квест, который ждёт Docker или уже
// готов продолжиться сам, остаётся под наблюдением: иначе карточка замирала
// на «Ждёт Docker» и после того, как Docker запустили.
function isTransientWorkOrder(order) {
  const runtime = order?.runtime
  if (TRANSIENT.has(String(runtime?.status || ''))) return true
  return runtime?.status === 'paused' && Boolean(runtime.waitingForSandbox || runtime.resumeAfterRestart)
}

// Мир, которому принадлежит поток или наблюдение. Смена проекта двигает эпоху
// (forgetProjectFollowers в master-turn-stream.js): после неё ответ прежнего
// мира не доходит до вебвью, а цикл выходит на следующем шаге. Ядро прежнего
// мира при этом живёт тёплым и доведёт работу само.
function projectScope(host) {
  const epoch = host?.projectEpoch || 0
  const current = () => (host?.projectEpoch || 0) === epoch
  return { current, post: message => { if (current()) host.post(message) } }
}

// История разговора перечитывается при каждой остановке, а не только в
// конце: уведомление ядра («ждёт Docker», «запуск отклонён») иначе появлялось
// в ленте только после исхода квеста.
async function refreshMasterHistory(host, id, conversationId, scope = projectScope(host)) {
  if (!conversationId || !scope.current()) return
  try {
    const master = await host.service.request('/api/master/history?conversationId=' + encodeURIComponent(conversationId))
    // Беседа названа явно: человек мог уйти в другой разговор, и обновление
    // этого не должно переключать ему экран (master-inbox.js).
    scope.post({ type: 'master', master, conversationId, loaded: true, completionRefresh: true })
  } catch (error) {
    host.service.hostLog('warn', `[chat] итог наряда ${id} сохранён, но история не обновилась: ${String(error?.message || error).slice(0, 200)}`)
  }
}

// Остановка и исход наряда — это и новый статус квеста в снимке, который
// читают все вкладки: список квестов проекта, счётчики, «текущий квест». Пока
// наблюдатель обновлял только чат, настройки проекта показывали завершённый
// квест активным до следующего случайного обновления.
async function refreshQuestSnapshot(host, scope) {
  if (typeof host.refreshRuntimeState !== 'function' || !scope.current()) return
  try {
    await host.refreshRuntimeState()
    if (scope.current()) host.postState()
  } catch (error) {
    host.service.hostLog?.('warn', `[chat] снимок квестов не обновился: ${String(error?.message || error).slice(0, 200)}`)
  }
}

// Наблюдение одно на наряд: три нажатия «Повторить запуск» не должны
// превращаться в три опроса одного и того же.
function watchMasterWorkOrder(host, workOrderId, conversationId) {
  const id = String(workOrderId || '')
  if (!id || !host?.service) return
  host.masterWorkOrderWatchers ||= new Map()
  if (host.masterWorkOrderWatchers.has(id)) return host.masterWorkOrderWatchers.get(id)
  const scope = projectScope(host)
  const promise = (async () => {
    let failures = 0
    let signature = ''
    while (true) {
      await new Promise(resolve => setTimeout(resolve, POLL_MS))
      if (!scope.current()) return
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
      if (!scope.current()) return
      const next = runtimeSignature(order?.runtime)
      if (next !== signature) {
        signature = next
        scope.post({ type: 'masterWorkOrder', workOrder: order, conversationId })
        // Этапы карточка привезла сама, а рассуждения и вызовы инструментов
        // живут в прогоне. Наблюдатель зовёт готовый конвейер расширения: он
        // сам шлёт runDelta и наполняет state.details, без которого хроника на
        // экране выполнения остаётся пустой навсегда.
        const runId = activeStageRunId(order)
        if (runId && typeof host.loadRun === 'function') {
          try { await host.loadRun(runId, true, true) } catch { /* прогон мог уже уехать */ }
        }
        if (order?.runtime?.status === 'paused') {
          await refreshQuestSnapshot(host, scope)
          await refreshMasterHistory(host, id, conversationId, scope)
          if (order.runtime.resumeAfterRestart && scope.current()) void resumeAfterRestart(host, order, conversationId)
        }
      }
      if (!isTransientWorkOrder(order)) {
        // Finalization persists the deterministic Master reply in the same
        // lifecycle operation. Reload the active history now so the user does
        // not need to close and reopen the panel to see it.
        await refreshQuestSnapshot(host, scope)
        await refreshMasterHistory(host, id, conversationId, scope)
        return
      }
    }
  })().catch(error => {
    host.service.hostLog('warn', `[chat] наблюдение за нарядом ${id} прервано: ${String(error?.message || error).slice(0, 200)}`)
  }).finally(() => { if (host.masterWorkOrderWatchers.get(id) === promise) host.masterWorkOrderWatchers.delete(id) })
  host.masterWorkOrderWatchers.set(id, promise)
  return promise
}

// История разговора приходит вместе с нарядами: открытая заново вкладка тоже
// должна показывать живой квест, а не снимок на момент загрузки.
function watchMasterWorkOrders(host, master) {
  const conversationId = master?.sessions?.active
  for (const order of master?.workOrders || []) {
    if (!isTransientWorkOrder(order)) continue
    watchMasterWorkOrder(host, order.id, conversationId)
    void resumeAfterRestart(host, order, conversationId)
  }
}

// Пауза ядра, после которой продолжать можно без человека: запуск прерван
// перезапуском ядра до первого шага или Docker, которого квест ждал, снова
// отвечает. «Нажмите Продолжить» было единственным, что человек делал в этом
// квесте. Ключ модели живёт только в SecretStorage расширения, поэтому
// продолжает расширение, а не ядро, — один раз на каждую такую паузу.
async function resumeAfterRestart(host, order, conversationId) {
  const runtime = order?.runtime
  if (runtime?.status !== 'paused' || !runtime?.resumeAfterRestart || !runtime?.questId || !host?.service) return
  host.autoResumedQuests ||= new Set()
  const pauseKey = `${runtime.questId}:${runtime.updatedAt || ''}`
  if (host.autoResumedQuests.has(pauseKey) || typeof host.credentialFor !== 'function') return
  host.autoResumedQuests.add(pauseKey)
  const scope = projectScope(host)
  try {
    const routing = order.routing || {}
    const connectionId = routing.mode === 'auto' ? routing.routerConnectionId : routing.fixedConnectionId
    const apiKey = connectionId ? await host.credentialFor({ connectionId }, 'утверждённого маршрута WorkOrder') : await host.credentialForOrchestrator()
    // Пока спрашивали ключ, человек мог открыть другой проект: продолжать чужой
    // квест через ядро нового мира нельзя. Квест продолжится, когда его мир
    // откроют снова.
    if (!scope.current()) { host.autoResumedQuests.delete(pauseKey); return }
    await host.service.request('/api/v2/master/quests/' + encodeURIComponent(runtime.questId) + '/resume', {
      method: 'POST', body: JSON.stringify({ message: '', apiKey }),
    })
    host.service.hostLog?.('info', `[chat] квест ${runtime.questId} продолжен без человека: ${runtime.message || 'пауза ядра'}`)
    watchMasterWorkOrder(host, order.id, conversationId)
  } catch (error) {
    host.service.hostLog?.('warn', `[chat] квест не продолжен после паузы ядра: ${String(error?.message || error).slice(0, 200)}`)
  }
}

module.exports = { watchMasterWorkOrder, watchMasterWorkOrders, resumeAfterRestart, isTransientWorkOrder, activeStageRunId, runtimeSignature, projectScope }
