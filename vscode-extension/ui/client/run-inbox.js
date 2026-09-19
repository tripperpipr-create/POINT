// Вокруг запуска: вложения и предпросмотры, старт, откат и отказ ядра.
//
// Первая половина — подготовка: что приложено к запуску и что показали
// предпросмотры (контекст, прогон агента, собранный промпт, свой инструмент).
// Вторая — сам запуск, его откат и отказ.
//
// Ветка `error` живёт здесь же, хотя отпускает не только запуск. Отказ ядра
// приходит один на все разделы и не всегда называет упавший запрос, поэтому
// она обязана знать каждый набор ожидания: наряды Мастера, решения по
// предложениям, карточки исполнителей, годность персонажа, политику Мастера.
// Разложить её по разделам нельзя — она и есть то место, где их список
// сходится, и держать его порознь значит забыть один при следующей правке.
//
// Состояние приходит общим мешком `ui`, как в `companion-transport.js`.

const RUN_MESSAGES = new Set([
  'contextAdded', 'contextPreview', 'contextPreviewError',
  'agentRunPreview', 'agentRunPreviewError', 'compiledPromptPreview',
  'compiledPromptPreviewError', 'customToolPreview', 'customToolPreviewError',
  'runUndoResult', 'runStarted', 'workflowRunStarted',
  'error',
])

export function createRunInbox({
  ui,
  render,
  persistDraft,
  countOf,
  FAILED_REQUEST_SECTIONS,
  invalidateAgentRunPreview,
  requestContextPreview,
  releaseMasterAgentCards,
  forgetMasterSent,
  stopMasterWaitClock,
  masterClient,
  masterSentText,
  masterWorkOrderBusy,
  proposalStarting,
  proposalModifying,
  companionActionApplying,
  companionActionModifying,
  agentCapabilityInflight,
  agentCapabilityFailed,
  orchestratorPolicyInflight,
  orchestratorPolicyFailed,
}) {
  return function applyRunMessage(message) {
    if (!RUN_MESSAGES.has(message.type)) return false
      if (message.type === 'contextAdded') {
        const incoming=Array.isArray(message.items)?message.items:[]
        for(const item of incoming){
          const duplicate=ui.contextItems.some(current=>current.kind===item.kind&&current.path===item.path&&current.label===item.label&&current.content===item.content)
          if(!duplicate&&ui.contextItems.length<16)ui.contextItems.push(item)
        }
        persistDraft()
        requestContextPreview()
      }
      if (message.type === 'contextPreview') { ui.contextPreview=message.preview; ui.contextPreviewStatus='ready'; ui.contextPreviewError=''; render() }
      if (message.type === 'contextPreviewError') { ui.contextPreview=undefined; ui.contextPreviewStatus='error'; ui.contextPreviewError=message.message||'Не удалось проверить вложения'; render() }
      if (message.type === 'agentRunPreview') { ui.agentRunPreview=message.preview;ui.agentRunPreviewStatus='ready';ui.agentRunPreviewError='';render() }
      if (message.type === 'agentRunPreviewError') { ui.agentRunPreview=undefined;ui.agentRunPreviewStatus='error';ui.agentRunPreviewError=message.message||'Не удалось проверить запуск';render() }
      if (message.type === 'compiledPromptPreview') { ui.compiledPromptPreview=message.preview;ui.compiledPromptStatus='ready';ui.compiledPromptError='';render() }
      if (message.type === 'compiledPromptPreviewError') { ui.compiledPromptPreview=undefined;ui.compiledPromptStatus='error';ui.compiledPromptError=message.message||'Не удалось собрать runtime-промпт';render() }
      if (message.type === 'customToolPreview') { ui.customToolPreview=message.preview;ui.customToolPreviewStatus='ready';ui.customToolPreviewError='';render() }
      if (message.type === 'customToolPreviewError') { ui.customToolPreview=undefined;ui.customToolPreviewStatus='error';ui.customToolPreviewError=message.message||'Не удалось проверить инструмент';render() }
      if (message.type === 'runUndoResult') {
        const result = message.result || {}
        const reverted = Array.isArray(result.reverted) ? result.reverted.length : 0
        const skipped = Array.isArray(result.skipped) ? result.skipped : []
        if (reverted) {
          ui.transientError = skipped.length
            ? `Откатили ${countOf(reverted, 'правка', 'правки', 'правок')}. Не всё: ${skipped.join('; ')}`
            : `Откатили ${countOf(reverted, 'правка', 'правки', 'правок')}.`
          if (!skipped.length && result.runId && ui.keptRunId === result.runId) ui.keptRunId = ''
        } else if (skipped.length) {
          ui.transientError = `Не удалось откатить: ${skipped.join('; ')}`
        } else if (message.message) {
          ui.transientError = message.message
        }
        persistDraft()
        render()
      }
      if (message.type === 'runStarted') {
        ui.runStarting = false
        ui.masterSending = false
        stopMasterWaitClock()
        ui.taskDraft='';ui.questGoalDraft='';ui.questCriteriaDraft='';ui.questConstraintsDraft='';invalidateAgentRunPreview();ui.contextItems=[]; ui.contextPreview=undefined; ui.contextPreviewStatus='idle'; ui.contextPreviewError=''; persistDraft()
        if (message.fastAgent) {
          ui.keptRunId = ''
          ui.masterDraft = ''
          persistDraft()
          if (masterClient?.acceptTurn) {
            const runId = ui.state.details?.run?.id || ''
            masterClient.acceptTurn({
              id: 'fast_' + Date.now().toString(36),
              conversationId: masterClient.active,
              status: 'ready',
              reply: runId ? `Агент запущен (run ${runId}). Правки появятся ниже — Keep / Undo.` : 'Агент запущен.',
            })
          }
        }
      }
      if (message.type === 'workflowRunStarted') { ui.contextItems=[]; ui.contextPreview=undefined; ui.contextPreviewStatus='idle'; ui.contextPreviewError=''; persistDraft() }
      if (message.type === 'error') {
        ui.providerProbe = undefined
        ui.companionProviderProbe = undefined
        // Запуск не состоялся — форму отпираем, иначе повторить будет нельзя.
        ui.runStarting = false
        // Какое из предложений не запустилось, отказ не называет — отпускаем все:
        // застрявшая навсегда кнопка хуже лишнего разблокированного нажатия,
        // которое ядро всё равно отвергнет.
        const failedRequest = String(message.request || '')
        if (!failedRequest || failedRequest === 'approveMasterWorkOrderV2' || failedRequest === 'reviseMasterWorkOrderV2' || failedRequest === 'controlMasterWorkOrderQuestV2' || failedRequest === 'controlMasterApplicationV2') masterWorkOrderBusy.clear()
        if (!failedRequest || failedRequest === '/api/quest-proposals/decide') {
          proposalStarting.clear()
          proposalModifying.clear()
        }
        if (!failedRequest || failedRequest === '/api/companion/actions/decide') {
          companionActionApplying.clear()
          companionActionModifying.clear()
        }
        // Отказ ядра обязан отпускать и карточку исполнителя: иначе её кнопка
        // остаётся запертой навсегда, а набранное человеком некуда отправить.
        releaseMasterAgentCards()
        ui.submittingForm = ''
        // Годность персонажа и политика мастера ждут ответа в своих наборах, а
        // снимались оттуда только ответом. После отказа ключ оставался ждать
        // вечно: повтор блокировал сам себя, кэш пустовал, и оба экрана держали
        // заглушку загрузки. Хуже того, готовность при неполученном ответе
        // намеренно не отрицается — персонаж навсегда объявлялся готовым по
        // данным, которых никто не присылал.
        for (const key of agentCapabilityInflight) agentCapabilityFailed.add(key)
        agentCapabilityInflight.clear()
        for (const key of orchestratorPolicyInflight) orchestratorPolicyFailed.add(key)
        orchestratorPolicyInflight.clear()
        ui.transientError = message.message
        if (ui.contextInspectorStatus === 'loading') {
          ui.contextInspectorStatus = 'error'
          ui.contextInspector = { error: message.message }
        }
        // Раздел уходит в «загрузку» перед запросом, а выходит из неё только
        // приходом ответа. При отказе ответа не будет: полоса ошибки скажет
        // причину, но раздел так и останется в «загрузка…» до переоткрытия панели.
        // Спасали двоих из девяти — теперь всех. Состояние 'error' тупиковое
        // намеренно: места запроса смотрят на 'idle', и автоповтор превратил бы
        // постоянный отказ в бесконечный цикл запросов. Повторяет человек.
        //
        // Если ядро назвало упавший запрос и он знаком — гасим только его раздел,
        // чтобы не винить соседей. Незнакомый или неназванный гасит всё ждущее:
        // лишняя пометка сама сойдёт с приходом ответа, а вечная «загрузка» — нет.
        const only = FAILED_REQUEST_SECTIONS[String(message.request || '')] || ''
        const hit = name => !only || only === name
        if (hit('statistics') && ui.statisticsStatus === 'loading') ui.statisticsStatus = 'error'
        if (hit('docker') && ui.dockerStatus === 'loading') ui.dockerStatus = 'error'
        if (hit('fileHistory') && ui.fileHistoryStatus === 'loading') ui.fileHistoryStatus = 'error'
        if (hit('master') && ui.masterStatus === 'loading') ui.masterStatus = 'error'
        if (hit('chatDirectory') && ui.chatDirectoryStatus === 'loading') ui.chatDirectoryStatus = 'error'
        // Отправка Мастеру запирает поле и кнопку до ответа. Ответа не будет —
        // и без снятия замка разговор вставал намертво: «Думает…» висело вечно,
        // писать было нечем, а разморозить это могло только переоткрытие панели.
        // Реплика цела в masterSentText и возвращается в поле — отправить её снова,
        // а не набирать заново. Причину человек уже читает в полосе ошибки.
        //
        // Возврат делается только в пустое поле: ход больше не запирает композер, и
        // человек мог написать в него следующую мысль, пока ответ не пришёл. Затереть
        // её отказавшей репликой значило бы потерять обе.
        const restoreSent = () => {
          const sent = masterSentText()
          if (sent && !String(ui.masterDraft || '').trim()) ui.masterDraft = sent
          forgetMasterSent()
        }
        if (hit('master')) { ui.masterSending = false; restoreSent(); stopMasterWaitClock(); ui.runStarting = false }
        if (hit('agent')) { ui.runStarting = false; ui.masterSending = false; restoreSent() }
        if (hit('dbQuery') && ui.dbQueryStatus === 'loading') ui.dbQueryStatus = 'error'
        if (hit('experienceSearch') && ui.experienceSearchStatus === 'loading') ui.experienceSearchStatus = 'error'
        if (hit('manualLearning') && (ui.manualLearningStatus === 'loading' || ui.manualLearningStatus === 'applying')) ui.manualLearningStatus = 'error'
        // Эти двое объясняются не статусом, а своей строкой ошибки: без неё раздел
        // вышел бы из спиннера и молча показал пустоту.
        if (hit('compiledPrompt') && ui.compiledPromptStatus === 'loading') {
          ui.compiledPromptStatus = 'error'
          ui.compiledPromptError = String(message.message || '') || 'запрос не удался'
        }
        if (hit('contextPreview') && ui.contextPreviewStatus === 'loading') {
          ui.contextPreviewStatus = 'error'
          ui.contextPreviewError = String(message.message || '') || 'запрос не удался'
        }
        if (hit('decisions') && ui.decisionsStatus === 'loading') {
          ui.decisionsStatus = 'error'
          ui.decisionsError = String(message.message || '') || 'запрос не удался'
        }
        render()
      }
    return true
  }
}
