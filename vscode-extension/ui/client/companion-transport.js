// Разговор с помощником: отправка, поток, приём.
//
// Одна тема жила в `main.js` двумя кусками на разных концах файла: отправка и
// остановка — среди разметки, приём ответа — среди девяноста веток разбора
// входящих сообщений. Читать их порознь нельзя: остановка живёт номером
// запроса, который выдаёт отправка и проверяет приём, а склейка ленты после
// перезапуска окна разбирается только целиком.
//
// Состояние приходит общим мешком `ui`, как в `companion-actions.js`.
import {
  COMPANION_MESSAGE_LIMIT_BYTES,
  companionMessageBytes,
  companionWaitSuffix,
  oversizedCompanionMessageNote,
} from './companion-compose.js'

const COMPANION_CHAT_MESSAGES = new Set([
  'companionChatStarted', 'companionChatStopped', 'companionChatProgress',
  'companionChatDelta', 'companionChatResult', 'companionChatError',
  'companionSetupTestResult', 'companionSetupTestError', 'companionHistoryCleared',
])

export function createCompanionTransport({
  ui,
  root,
  vscode,
  render,
  persistDraft,
  focusCompanionInput,
  isCompanionView,
  patchCompanionComposeChrome,
  replaceCompanionThreadHtml,
  replaceHtmlNodes,
  scrollCompanionThread,
  companionQuickPromptsHtml,
  patchCompanionThinkingLabel,
  patchCompanionStreamingBubble,
  pendingQuestProposals,
  pendingActionProposals,
}) {
  function stopCompanionChat(options = {}) {
    const expectedRequestId = Number(options.requestId || 0)
    if (expectedRequestId && ui.companionActiveRequestId && expectedRequestId !== ui.companionActiveRequestId) return
    if (!ui.companionLoading && !ui.companionPendingSend) return
    const stoppedRequestId = ui.companionActiveRequestId
    ui.companionActiveRequestId = ++ui.companionRequestId
    ui.companionLoading = false
    ui.companionThinkPhase = ''
    ui.companionActivitySteps = []
    ui.companionPendingSend = ''
    const partial = String(ui.companionStreamReply || '').trim()
    ui.companionStreamReply = ''
    const last = ui.companionMessages[ui.companionMessages.length - 1]
    if (partial) {
      if (last?.role === 'assistant' && (last.mode === 'streaming' || last.mode === 'cancelled')) {
        ui.companionMessages = [...ui.companionMessages.slice(0, -1), {
          role: 'assistant',
          content: `${partial}\n\n— ${options.superseded ? 'остановлено новым сообщением' : 'остановлено'}. Можно сразу спросить снова.`,
          level: 'warning',
          mode: 'cancelled',
          superseded: Boolean(options.superseded),
        }].slice(-80)
      } else if (!(last?.role === 'assistant' && last.mode === 'cancelled' && last.content.includes(partial))) {
        ui.companionMessages = [...ui.companionMessages, {
          role: 'assistant',
          content: `${partial}\n\n— ${options.superseded ? 'остановлено новым сообщением' : 'остановлено'}. Можно сразу спросить снова.`,
          level: 'warning',
          mode: 'cancelled',
          superseded: Boolean(options.superseded),
        }].slice(-80)
      }
    } else if (!(last?.role === 'assistant' && last.mode === 'cancelled')) {
      ui.companionMessages = [...ui.companionMessages, {
        role: 'assistant',
        content: options.superseded ? 'Предыдущий запрос остановлен новым сообщением.' : 'Запрос остановлен. Можно сразу спросить снова.',
        level: 'warning',
        mode: 'cancelled',
        superseded: Boolean(options.superseded),
      }].slice(-80)
    }
    if (options.notifyHost !== false) vscode.postMessage({ type: 'stopCompanionChat', requestId: stoppedRequestId })
    persistDraft()
    render()
    focusCompanionInput()
  }
  // options.retry — «ответь иначе»: тот же вопрос, но другим путём. Без него
  // перегенерация при низкой температуре возвращала тот же ответ, и кнопка
  // выглядела сломанной.
  function sendCompanionUserMessage(message, options = {}) {
    const text = String(message || '').trim()
    if (!text) return false
    // Узнавать о пределе после отправки поздно: реплика уже ушла из поля, а
    // вернуть её можно только копированием из ленты.
    const size = companionMessageBytes(text)
    if (size > COMPANION_MESSAGE_LIMIT_BYTES) {
      ui.companionDraft = text
      ui.transientError = oversizedCompanionMessageNote(size)
      persistDraft()
      render()
      focusCompanionInput()
      return false
    }
    if (ui.companionLoading) {
      ui.companionPendingSend = text
      ui.companionDraft = text
      persistDraft()
      patchCompanionComposeChrome()
      focusCompanionInput()
      return false
    }
    ui.companionDraft = ''
    ui.companionPendingSend = ''
    ui.companionStreamReply = ''
    ui.companionMessages = [...ui.companionMessages, { role: 'user', content: text }].slice(-80)
    ui.companionLoading = true
    ui.companionThinkPhase = ''
    ui.companionActivitySteps = [{ step: 'gather', status: 'running', at: Date.now() }]
    startCompanionWaitTicker()
    ui.companionActiveRequestId = ++ui.companionRequestId
    ui.companionSetupOpen = false
    if (!isCompanionView() && ui.state.selectedTab !== 'overview') {
      ui.state = { ...ui.state, selectedTab: 'overview' }
      vscode.postMessage({ type: 'selectTab', tab: 'overview' })
    }
    persistDraft()
    render()
    // A full webview render and textarea re-focus can each change the available
    // thread height. Anchor after both layout passes; one rAF left a visible
    // frame at scrollTop=0 on tall answers.
    requestAnimationFrame(() => requestAnimationFrame(() => scrollCompanionThread(true)))
    focusCompanionInput()
    vscode.postMessage({ type: 'companionChat', message: text, requestId: ui.companionActiveRequestId, retry: Boolean(options.retry) })
    return true
  }
  function flushCompanionPendingSend() {
    const next = String(ui.companionPendingSend || '').trim()
    if (!next || ui.companionLoading) return
    ui.companionPendingSend = ''
    sendCompanionUserMessage(next)
  }
  function isActiveCompanionRequest(message) {
    const incoming = Number(message?.requestId || 0)
    if (!incoming) return false
    return incoming === ui.companionActiveRequestId
  }
  function mergeCompanionTranscript(local, incoming, loading) {
    if (!incoming.length) return local
    if (!local.length) return incoming
    const localLast = local[local.length - 1]
    if (!loading && localLast?.role === 'assistant' && (localLast.mode === 'error' || localLast.mode === 'cancelled')) {
      if (!incoming.some(item => item.role === 'assistant' && item.content === localLast.content)) {
        return [...incoming, localLast].slice(-80)
      }
    }
    if (loading) {
      if (localLast?.role === 'user' && !incoming.some(item => item.role === 'user' && item.content === localLast.content)) {
        return [...incoming, localLast].slice(-80)
      }
    }
    if (incoming.length >= local.length) return incoming
    return local
  }
  function companionStepLabel(step) {
    const labels = {
      gather: 'Сбор контекста',
      focus: 'Фокус IDE',
      roster: 'Ростер',
      quests: 'Квесты',
      memory: 'Память',
      index: 'Карта проекта',
      search: 'Поиск',
      model: 'Модель',
      local: 'Локальный разбор',
    }
    // Шаг инструмента приходит как «tool:git_log»: в ленте нужно имя того, что
    // помощник читает прямо сейчас, — иначе десятки секунд ожидания выглядят как
    // одно бесконечное «Сбор контекста».
    const tool = /^tool:(.+)$/.exec(String(step || ''))
    if (tool) return `Смотрю ${tool[1]}`
    return labels[step] || step || 'Работа'
  }
  function upsertCompanionActivity(step, status) {
    if (!step) return
    const next = ui.companionActivitySteps.filter(item => item.step !== step)
    next.push({ step, status: status || 'running', at: Date.now() })
    ui.companionActivitySteps = next.slice(-8)
  }
  // Секунды идут сами: событий от молчащей модели не приходит, и без тика полоса
  // стоит на месте. Тикер гасит себя, как только ожидание кончилось.
  let companionWaitTicker = 0
  function startCompanionWaitTicker() {
    if (companionWaitTicker || typeof setInterval !== 'function') return
    companionWaitTicker = setInterval(() => {
      if (!ui.companionLoading) {
        clearInterval(companionWaitTicker)
        companionWaitTicker = 0
        return
      }
      patchCompanionThinkingLabel()
    }, 1000)
  }
  function companionThinkingLabel() {
    const current = [...ui.companionActivitySteps].reverse().find(item => item.status === 'running')
    const step = current
      ? companionStepLabel(current.step) + '…'
      : ui.companionThinkPhase === 'model' ? 'Модель…'
        : ui.companionThinkPhase === 'local' ? 'Локальный разбор…'
          : 'Думает…'
    // Ожидание считается от первого шага: от молчащей модели событий не дождаться,
    // и без счёта секунд окно неотличимо от зависшего.
    const startedAt = Number(ui.companionActivitySteps[0]?.at || 0)
    const waited = startedAt && ui.companionLoading ? Math.floor((Date.now() - startedAt) / 1000) : 0
    return step + companionWaitSuffix(waited)
  }

  // Ветки разбираются здесь целиком: каждая из девяти отвечает «взял», и ни
  // одно из этих имён ниже по слушателю второй раз не встречается.
  function applyCompanionChatMessage(message) {
    if (!COMPANION_CHAT_MESSAGES.has(message.type)) return false
      if (message.type === 'companionChatStarted') {
        const requestId = Number(message.requestId || 0)
        if (!requestId) return true
        const text = String(message.message || '').trim()
        ui.companionRequestId = Math.max(ui.companionRequestId, requestId)
        ui.companionActiveRequestId = requestId
        ui.companionLoading = true
        ui.companionThinkPhase = ''
        ui.companionActivitySteps = [{ step: 'gather', status: 'running', at: Date.now() }]
        startCompanionWaitTicker()
        ui.companionStreamReply = ''
        const last = ui.companionMessages[ui.companionMessages.length - 1]
        if (text && !(last?.role === 'user' && last.content === text)) {
          ui.companionMessages = [...ui.companionMessages, { role: 'user', content: text }].slice(-80)
        }
        if (ui.companionDraft.trim() === text) ui.companionDraft = ''
        if (ui.companionPendingSend.trim() === text) ui.companionPendingSend = ''
        persistDraft()
        render()
        requestAnimationFrame(() => scrollCompanionThread())
      }
      if (message.type === 'companionChatStopped') {
        stopCompanionChat({ notifyHost: false, requestId: Number(message.requestId || 0), superseded: Boolean(message.superseded) })
      }
      if (message.type === 'companionChatProgress') {
        if (!isActiveCompanionRequest(message) && message.requestId) return true
        ui.companionThinkPhase = message.phase === 'model' ? 'model' : message.phase === 'local' ? 'local' : 'gather'
        if (message.step) upsertCompanionActivity(message.step, message.status || 'running')
        if (ui.companionLoading) {
          patchCompanionThinkingLabel()
          if (!ui.companionStreamReply) patchCompanionStreamingBubble()
        }
      }
      if (message.type === 'companionChatDelta') {
        if (!isActiveCompanionRequest(message) && message.requestId) return true
        const reply = String(message.reply || '')
        if (!reply || reply.length < ui.companionStreamReply.length) return true
        ui.companionStreamReply = reply
        ui.companionThinkPhase = 'model'
        upsertCompanionActivity('model', 'running')
        if (ui.companionLoading) {
          patchCompanionThinkingLabel()
          patchCompanionStreamingBubble()
          vscode.postMessage({
            type: 'companionThreadUpdate',
            messages: ui.companionMessages,
            draft: ui.companionDraft,
            streamReply: ui.companionStreamReply,
            loading: true,
            pendingSend: ui.companionPendingSend,
            requestId: ui.companionActiveRequestId,
          })
        }
      }
      if (message.type === 'companionChatResult') {
        if (!isActiveCompanionRequest(message) && message.requestId) return true
        if (!ui.companionLoading && Number(message.requestId || 0) === 0) {
          /* late reply after soft-stop without requestId — ignore if already cancelled */
          const last = ui.companionMessages[ui.companionMessages.length - 1]
          if (last?.mode === 'cancelled') return true
        }
        ui.companionLoading = false
        ui.companionActiveRequestId = 0
        ui.companionThinkPhase = ''
        ui.companionActivitySteps = []
        ui.companionSetupOpen = false
        const reply = String(message.response?.reply || message.response?.content || ui.companionStreamReply || '').trim()
          || 'Компаньон ответил без текста. Спросите ещё раз или проверьте подключение модели.'
        ui.companionStreamReply = ''
        const last = ui.companionMessages[ui.companionMessages.length - 1]
        if (last?.role === 'assistant' && last.mode === 'streaming') {
          ui.companionMessages = [...ui.companionMessages.slice(0, -1), {
            ...message.response,
            role: 'assistant',
            content: reply,
          }].slice(-80)
        } else if (!(last?.role === 'assistant' && last.content === reply)) {
          ui.companionMessages = [...ui.companionMessages, {
            ...message.response,
            role: 'assistant',
            content: reply,
          }].slice(-80)
        }
        if (message.error) ui.transientError = message.error
        persistDraft()
        const needsFullRender = Boolean(message.response?.proposal) || Boolean(message.response?.actionProposal)
          || pendingQuestProposals().length > 0 || pendingActionProposals().length > 0
        if (needsFullRender || !root.querySelector('#companion-thread')) {
          render()
        } else {
          replaceCompanionThreadHtml()
          patchCompanionThinkingLabel()
          patchCompanionComposeChrome()
          if (root.querySelector('.companion-quick-prompts')) {
            replaceHtmlNodes('.companion-quick-prompts', companionQuickPromptsHtml(Boolean(root.querySelector('.companion-quick-prompts.compact'))))
          }
        }
        setTimeout(() => {
          scrollCompanionThread()
          if (pendingQuestProposals().length || pendingActionProposals().length) {
            root.querySelector('#companion-review')?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
          }
        }, 20)
        focusCompanionInput()
        flushCompanionPendingSend()
      }
      if (message.type === 'companionChatError') {
        if (!isActiveCompanionRequest(message) && message.requestId) return true
        ui.companionLoading = false
        ui.companionActiveRequestId = 0
        ui.companionThinkPhase = ''
        ui.companionActivitySteps = []
        // Написанное до сбоя остаётся на экране. Ответ шёл потоком, человек читал
        // его вживую — и стирать прочитанное ради дежурной строки значит терять
        // разобранное вместе с причиной отказа. Остановка ведёт себя так же.
        const partial = String(ui.companionStreamReply || '').trim()
        ui.companionStreamReply = ''
        const text = message.message || 'Компаньон не смог ответить.'
        const body = partial ? `${partial}\n\n— ${text} Написанное сохранено.` : text
        const last = ui.companionMessages[ui.companionMessages.length - 1]
        if (!(last?.role === 'assistant' && last.content === body)) {
          ui.companionMessages = [...ui.companionMessages, { role: 'assistant', content: body, level: 'warning', mode: 'error', failure: text }].slice(-80)
        }
        ui.transientError = text
        persistDraft()
        render()
        focusCompanionInput()
        flushCompanionPendingSend()
      }
      if (message.type === 'companionSetupTestResult') {
        ui.companionLoading = false
        ui.companionSetupTestResult = message.response
        const fallback = String(message.response?.fallbackReason || '').trim()
        ui.companionSetupStatus = fallback
          ? `Настройки сохранены. Модель не ответила (${fallback}). Показан локальный ответ.`
          : 'Настройки сохранены, пробный ответ добавлен в историю проекта.'
        persistDraft()
        render()
      }
      if (message.type === 'companionSetupTestError') {
        ui.companionLoading = false
        ui.companionSetupTestResult = undefined
        ui.companionSetupStatus = message.message
          || (message.phase === 'save' ? 'Не удалось сохранить настройки.' : 'Настройки сохранены, пробный ответ не получен.')
        ui.companionSetupPendingClose = false
        render()
      }
      if (message.type === 'companionHistoryCleared') {
        ui.companionMessages = []
        ui.transientError = ''
        persistDraft()
        render()
      }
    return true
  }

  return {
    stopCompanionChat,
    sendCompanionUserMessage,
    flushCompanionPendingSend,
    mergeCompanionTranscript,
    companionThinkingLabel,
    applyCompanionChatMessage,
  }
}
