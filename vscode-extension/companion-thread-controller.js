// Жизненный цикл ленты компаньона: начали ход, стримим, закончили.
//
// Одиннадцать методов об одном ответе: начать, дополнять по кусочку,
// завершить, провалить, остановить по просьбе человека, запомнить и разослать
// по поверхностям. Компаньон живёт в четырёх обличьях сразу — док, боковая
// панель, всплывающее окно и подглядывание, — и любое из них может быть
// открыто, закрыто или невидимо. Отсюда и `pushCompanionThreadSync`, и очередь
// фокуса: ответ приходит одному вебвью, а показать его надо всем.
//
// Разбор входящих сообщений компаньона держит `companion-chat-controller.js`;
// здесь — состояние хода, а не его протокол.

function createCompanionThreads() {
  function companionWebviewTraffic(provider, type) {
    return [
      'focusCompanion',
      'companionChatStarted',
      'companionChatStopped',
      'companionChatResult',
      'companionChatError',
      'companionChatProgress',
      'companionChatDelta',
      'companionThreadSync',
      'companionSetupTestResult',
      'companionSetupTestError',
      'companionHistoryCleared',
      'companionThreadCreated',
      'companionIdeContext',
      'companionActionApplied',
      'companionInterventions',
    ].includes(type)
  }
  function beginCompanionThread(provider, message, requestId) {
    const text = String(message || '').trim()
    const messages = [...(provider.companionThreadCache?.messages || [])]
    const last = messages[messages.length - 1]
    if (text && !(last?.role === 'user' && last.content === text)) messages.push({ role: 'user', content: text })
    provider.companionThreadCache = {
      ...(provider.companionThreadCache || {}),
      messages: messages.slice(-80),
      streamReply: '',
      loading: true,
      requestId: Number(requestId || 0),
      updatedAt: Date.now(),
    }
  }
  function updateCompanionThreadStream(provider, reply, requestId) {
    if (Number(requestId || 0) !== Number(provider.companionThreadCache?.requestId || 0)) return
    provider.companionThreadCache = {
      ...provider.companionThreadCache,
      streamReply: String(reply || ''),
      loading: true,
      updatedAt: Date.now(),
    }
  }
  function finishCompanionThread(provider, response, requestId) {
    if (Number(requestId || 0) !== Number(provider.companionThreadCache?.requestId || 0)) return
    const reply = String(response?.reply || response?.content || provider.companionThreadCache?.streamReply || '').trim()
      || 'Компаньон ответил без текста. Спросите ещё раз или проверьте подключение модели.'
    const messages = [...(provider.companionThreadCache?.messages || [])]
    const last = messages[messages.length - 1]
    if (!(last?.role === 'assistant' && last.content === reply)) {
      messages.push({ ...response, role: 'assistant', content: reply })
    }
    provider.companionThreadCache = {
      ...provider.companionThreadCache,
      messages: messages.slice(-80),
      streamReply: '',
      loading: false,
      requestId: 0,
      updatedAt: Date.now(),
    }
  }
  function failCompanionThread(provider, message, requestId) {
    if (Number(requestId || 0) !== Number(provider.companionThreadCache?.requestId || 0)) return
    const text = String(message || 'Компаньон не смог ответить.')
    const messages = [...(provider.companionThreadCache?.messages || [])]
    const last = messages[messages.length - 1]
    if (!(last?.role === 'assistant' && last.content === text)) {
      messages.push({ role: 'assistant', content: text, level: 'warning', mode: 'error' })
    }
    provider.companionThreadCache = {
      ...provider.companionThreadCache,
      messages: messages.slice(-80),
      streamReply: '',
      loading: false,
      requestId: 0,
      updatedAt: Date.now(),
    }
  }
  function finishCompanionThreadStopped(provider, requestId, superseded = false) {
    if (requestId && Number(requestId) !== Number(provider.companionThreadCache?.requestId || 0)) return
    const partial = String(provider.companionThreadCache?.streamReply || '').trim()
    const text = partial
      ? `${partial}\n\n— ${superseded ? 'остановлено новым сообщением' : 'остановлено'}. Можно сразу спросить снова.`
      : superseded
        ? 'Предыдущий запрос остановлен новым сообщением.'
        : 'Запрос остановлен. Можно сразу спросить снова.'
    const messages = [...(provider.companionThreadCache?.messages || [])]
    const last = messages[messages.length - 1]
    if (!(last?.role === 'assistant' && last.mode === 'cancelled')) {
      messages.push({ role: 'assistant', content: text, level: 'warning', mode: 'cancelled' })
    }
    provider.companionThreadCache = {
      ...provider.companionThreadCache,
      messages: messages.slice(-80),
      streamReply: '',
      loading: false,
      requestId: 0,
      updatedAt: Date.now(),
    }
  }
  function rememberCompanionThread(provider, payload = {}) {
    const incomingRequestId = Number(payload.requestId || 0)
    const activeRequestId = Number(provider.companionActiveChatRequestId || 0)
    const stale = Boolean(activeRequestId && incomingRequestId && incomingRequestId !== activeRequestId)
    provider.companionThreadCache = {
      messages: !stale && Array.isArray(payload.messages) ? payload.messages.slice(-80) : (provider.companionThreadCache?.messages || []),
      draft: !stale && typeof payload.draft === 'string' ? payload.draft : (provider.companionThreadCache?.draft || ''),
      streamReply: !stale && typeof payload.streamReply === 'string' ? payload.streamReply : (provider.companionThreadCache?.streamReply || ''),
      loading: activeRequestId ? true : Boolean(payload.loading),
      pendingSend: typeof payload.pendingSend === 'string' ? payload.pendingSend : (provider.companionThreadCache?.pendingSend || ''),
      requestId: activeRequestId || incomingRequestId,
      updatedAt: Date.now(),
    }
  }
  function pushCompanionThreadSync(provider, target) {
    const cache = provider.companionThreadCache
    if (!cache?.messages?.length && !cache?.streamReply && !cache?.draft && !cache?.loading) return
    const message = {
      type: 'companionThreadSync',
      messages: cache.messages || [],
      draft: cache.draft || '',
      streamReply: cache.streamReply || '',
      loading: Boolean(provider.companionActiveChatRequestId || cache.loading),
      pendingSend: cache.pendingSend || '',
      requestId: provider.companionActiveChatRequestId || Number(cache.requestId || 0),
    }
    if (target === 'peek' && provider.companionPopup) void provider.companionPopup.webview.postMessage(message)
    else if (target === 'sidebar' && provider.companionSidebar) void provider.companionSidebar.webview.postMessage(message)
    else if (target === 'dock' && provider.view) void provider.view.webview.postMessage(message)
    else provider.post(message)
  }
  function preferLiveCompanionSurface(provider, fallback = 'peek') {
    if (provider.companionFocusTarget === 'dock' && provider.view && provider.view.visible !== false) return 'dock'
    if (provider.companionFocusTarget === 'sidebar' && provider.companionSidebar && provider.companionSidebar.visible !== false) return 'sidebar'
    if (provider.companionFocusTarget === 'peek' && provider.companionPopup && provider.companionPopup.visible !== false) return 'peek'
    if (provider.companionPopup && provider.companionPopup.visible !== false) return 'peek'
    if (provider.companionSidebar && provider.companionSidebar.visible !== false) return 'sidebar'
    if (provider.view && provider.view.visible !== false) return 'dock'
    if (fallback === 'dock' || fallback === 'sidebar') return fallback
    return 'peek'
  }
  function queueCompanionFocus(provider, payload = {}) {
    provider.pendingCompanionFocus = {
      message: typeof payload.message === 'string' ? payload.message : '',
      send: Boolean(payload.send),
      surface: payload.surface === 'peek' ? 'peek' : payload.surface === 'dock' ? 'dock' : 'sidebar',
    }
    provider.companionFocusTarget = provider.pendingCompanionFocus.surface
    provider.flushCompanionFocus()
  }
  function flushCompanionFocus(provider) {
    if (!provider.pendingCompanionFocus) return
    const payload = provider.pendingCompanionFocus
    const target = payload.surface || provider.companionFocusTarget || 'dock'
    if (target === 'dock' && provider.companionDockReady && provider.view) {
      provider.pendingCompanionFocus = undefined
      void provider.view.webview.postMessage({ type: 'focusCompanion', message: payload.message, send: payload.send })
      return
    }
    if (target === 'peek' && provider.companionPopupReady && provider.companionPopup) {
      provider.pendingCompanionFocus = undefined
      void provider.companionPopup.webview.postMessage({ type: 'focusCompanion', message: payload.message, send: payload.send })
      return
    }
    if (target === 'sidebar' && provider.companionSidebarReady && provider.companionSidebar) {
      provider.pendingCompanionFocus = undefined
      void provider.companionSidebar.webview.postMessage({ type: 'focusCompanion', message: payload.message, send: payload.send })
      return
    }
  }

  return {
    companionWebviewTraffic, beginCompanionThread, updateCompanionThreadStream, finishCompanionThread,
    failCompanionThread, finishCompanionThreadStopped, rememberCompanionThread, pushCompanionThreadSync,
    preferLiveCompanionSurface, queueCompanionFocus, flushCompanionFocus,
  }
}

module.exports = { createCompanionThreads }
