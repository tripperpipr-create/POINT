// Companion chat transport: threads, streaming chat, feedback, config save/test.
// Credentials and AbortController stay on the extension host (`this`).
const vscode = require('vscode')
const { POINT_COMPANION_ARCHIVES_KEY } = require('./chat-documents')
const { attachFeedbackToMessages, upsertById } = require('./extension-utils')

const POINT_COMPANION_FEEDBACK_KEY = 'point.companion.feedback.v1'

let formatCompanionChatError = (error) => (error instanceof Error ? error.message : String(error || ''))

function bindCompanionChatHelpers(helpers = {}) {
  if (typeof helpers.formatCompanionChatError === 'function') {
    formatCompanionChatError = helpers.formatCompanionChatError
  }
}

function newRequestId() {
  return `req_${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`
}

function companionConfigPayload(config = {}) {
  const payload = {
    id: String(config.id || ''),
    workspaceId: String(config.workspaceId || ''),
    preset: String(config.preset || ''),
    connectionId: String(config.connectionId || ''),
    provider: config.provider || '',
    providerPreset: String(config.providerPreset || ''),
    baseUrl: String(config.baseUrl || ''),
    model: String(config.model || ''),
    temperature: Number(config.temperature ?? 0),
    maxOutputTokens: Number(config.maxOutputTokens || 0),
    criticality: Number(config.criticality ?? 50),
    creativity: Number(config.creativity ?? 50),
    verbosity: Number(config.verbosity ?? 50),
    initiative: Number(config.initiative ?? 50),
    questionStrictness: Number(config.questionStrictness ?? 70),
    riskTolerance: Number(config.riskTolerance ?? 30),
    autoAct: Boolean(config.autoAct),
    autoOpenChatOnCritical: Boolean(config.autoOpenChatOnCritical),
    autoSendModelPrompt: Boolean(config.autoSendModelPrompt),
    // Навыки помощника — часть его настройки, и снятие последнего должно
    // доезжать до ядра. Поэтому список уходит всегда, пустым в том числе.
    skillIds: Array.isArray(config.skillIds) ? config.skillIds.map(String) : [],
  }
  if (config.createdAt) payload.createdAt = config.createdAt
  return payload
}

async function handleCompanionChatMessage(message) {
  switch (message.type) {
    case 'companionThreadUpdate':
      this.rememberCompanionThread(message)
      break
    case 'copyCompanionText': {
      const text = String(message.text || '').slice(0, 1024 * 1024)
      if (!text) break
      await vscode.env.clipboard.writeText(text)
      vscode.window.setStatusBarMessage('Скопировано из диалога с компаньоном', 1800)
      break
    }
    case 'openCompanionMessageDetails':
      this.showCompanionMessageDetails(message.item, message.request)
      break
    case 'companionFeedback': {
      const records = this.context.workspaceState.get(POINT_COMPANION_FEEDBACK_KEY, [])
      const entry = {
        messageId: String(message.messageId || newRequestId()).slice(0, 160),
        value: message.value === 'down' ? 'down' : 'up',
        content: String(message.content || '').slice(0, 400),
        createdAt: new Date().toISOString(),
      }
      await this.context.workspaceState.update(POINT_COMPANION_FEEDBACK_KEY, [entry, ...(Array.isArray(records) ? records : []).filter(item => item?.messageId !== entry.messageId)].slice(0, 200))
      // «Не помогло» — указание на следующий ответ, а не оценка в архив.
      this.companionRejectedAnswer = entry.value === 'down'
      vscode.window.setStatusBarMessage(entry.value === 'up' ? 'Спасибо — ответ отмечен как полезный' : 'Учту в следующем ответе: попробую другой путь', 2600)
      break
    }
    case 'newCompanionThread': {
      this.companionChatAbort?.abort()
      this.companionChatAbort = undefined
      this.companionActiveChatRequestId = 0
      const messages = (Array.isArray(message.messages) ? message.messages : this.companionThreadCache?.messages || [])
        .filter(item => item && (item.role === 'user' || item.role === 'assistant'))
        .slice(-80)
      if (messages.length) {
        const archives = this.context.workspaceState.get(POINT_COMPANION_ARCHIVES_KEY, [])
        const firstUser = messages.find(item => item.role === 'user')
        // Отметки уезжают в архив вместе с разговором: без них «полезно» и
        // «не помогло» превращаются в мёртвый груз — сообщений, к которым
        // они привязаны, в ленте уже нет, а в архиве их не видно.
        const archived = attachFeedbackToMessages(messages, this.companionFeedbackRecords())
        const archive = {
          id: newRequestId(),
          title: String(firstUser?.content || 'Диалог с помощником').replace(/\s+/g, ' ').slice(0, 80),
          createdAt: new Date().toISOString(),
          messages: archived,
        }
        await this.context.workspaceState.update(POINT_COMPANION_ARCHIVES_KEY, [archive, ...(Array.isArray(archives) ? archives : [])].slice(0, 24))
        await this.forgetCompanionFeedback(messages)
      }
      await this.service.request('/api/companion/history', { method: 'DELETE' })
      this.companionThreadCache = { messages: [], draft: '', streamReply: '', loading: false, pendingSend: '', requestId: 0, updatedAt: Date.now() }
      this.patchBoot({ companionMessages: [] })
      this.post({ type: 'companionHistoryCleared', newThread: true })
      this.postState(true)
      break
    }
    case 'showCompanionArchives':
      await this.showCompanionArchives()
      break
    case 'stopCompanionChat': {
      const requestedRequestId = Number(message.requestId || 0)
      if (requestedRequestId && this.companionActiveChatRequestId && requestedRequestId !== this.companionActiveChatRequestId) break
      const requestId = this.companionActiveChatRequestId
      this.companionChatAbort?.abort()
      this.companionChatAbort = undefined
      this.companionActiveChatRequestId = 0
      this.finishCompanionThreadStopped(requestId)
      this.post({ type: 'companionChatStopped', requestId })
      break
    }
    case 'companionChat': {
      const previousRequestId = this.companionActiveChatRequestId
      this.companionChatAbort?.abort()
      if (previousRequestId) {
        this.finishCompanionThreadStopped(previousRequestId, true)
        this.post({ type: 'companionChatStopped', requestId: previousRequestId, superseded: true })
      }
      const requestId = ++this.companionChatRequestSequence
      this.companionActiveChatRequestId = requestId
      const abort = new AbortController()
      this.companionChatAbort = abort
      const chatMessage = String(message.message || '')
      this.beginCompanionThread(chatMessage, requestId)
      this.post({ type: 'companionChatStarted', message: chatMessage, requestId })
      try {
        const apiKey = await this.credentialForCompanion()
        if (abort.signal.aborted) break
        const companion = this.boot?.companion || {}
        const focus = typeof this.companionFocus === 'function' ? this.companionFocus() : (this.ideContext || {})
        const traceId = newRequestId()
        this.service.hostLog('info', `[companion] ${traceId} chat start provider=${companion.provider || 'none'} model=${companion.model || 'none'} msg_bytes=${String(message.message || '').length} focus=${focus?.file || '-'} diagnostics=${focus?.diagnostics || 0} has_key=${Boolean(apiKey)}`)
        this.post({ type: 'companionChatProgress', phase: companion.provider && companion.model ? 'model' : 'local', step: 'gather', status: 'running', requestId })
        const response = await this.service.requestNdjson('/api/companion/chat', {
          method: 'POST',
          timeoutMs: 90_000,
          signal: abort.signal,
          headers: { 'X-Request-Id': traceId },
          body: JSON.stringify({
            message: String(message.message || ''),
            apiKey,
            focus,
            // Отметка доезжает ровно один раз: она про прошлый ответ.
            // «Ответить иначе» — та же просьба, высказанная кнопкой.
            previousAnswerRejected: Boolean(message.retry) || this.consumeCompanionRejection(),
          }),
          onProgress: ({ step, status }) => {
            if (abort.signal.aborted) return
            const phase = step === 'model' ? 'model' : step === 'local' ? 'local' : 'gather'
            this.post({ type: 'companionChatProgress', phase, step, status, requestId })
          },
          onDelta: ({ reply }) => {
            if (abort.signal.aborted) return
            this.updateCompanionThreadStream(reply, requestId)
            this.post({ type: 'companionChatDelta', reply: String(reply || ''), requestId })
          },
        })
        if (abort.signal.aborted) break
        this.service.hostLog('info', `[companion] ${traceId} chat done mode=${response?.mode || '-'} level=${response?.level || '-'} fallback=${Boolean(response?.fallbackReason)} reply_bytes=${String(response?.reply || '').length}`)
        if (response?.fallbackReason) {
          this.service.hostLog('warn', `[companion] ${traceId} fallback: ${String(response.fallbackReason).slice(0, 500)}`)
        }
        if (response?.proposal?.id) {
          this.patchBoot({ questProposals: upsertById(this.boot?.questProposals, response.proposal) })
        }
        if (response?.actionProposal?.id) {
          this.patchBoot({ companionActionProposals: upsertById(this.boot?.companionActionProposals, response.actionProposal) })
        }
        if (this.companionActiveChatRequestId === requestId) this.companionActiveChatRequestId = 0
        this.finishCompanionThread(response, requestId)
        this.post({ type: 'companionChatResult', response, requestId })
        await this.refreshLiveCompanionInterventions()
        this.postState()
      } catch (error) {
        if (error?.cancelled || abort.signal.aborted) break
        const messageText = formatCompanionChatError(error, this.boot?.companion?.model)
        this.service.hostLog('error', `[companion] chat error: ${messageText}`)
        if (this.companionActiveChatRequestId === requestId) this.companionActiveChatRequestId = 0
        this.failCompanionThread(messageText, requestId)
        this.post({ type: 'companionChatError', message: messageText, requestId })
        this.postState()
      } finally {
        if (this.companionChatAbort === abort) this.companionChatAbort = undefined
        if (this.companionActiveChatRequestId === requestId) this.companionActiveChatRequestId = 0
      }
      break
    }
    case 'clearCompanionHistory': {
      const answer = await vscode.window.showWarningMessage(
        'Очистить историю Companion только для текущего проекта? Это действие нельзя отменить.',
        { modal: true },
        'Очистить',
      )
      if (answer !== 'Очистить') break
      await this.service.request('/api/companion/history', { method: 'DELETE' })
      await this.forgetCompanionFeedback(this.companionThreadCache?.messages || this.boot?.companionMessages || [])
      this.patchBoot({ companionMessages: [] })
      this.post({ type: 'companionHistoryCleared' })
      this.postState()
      break
    }
    case 'dismissCompanionIntervention': {
      await this.service.request(`/api/companion/interventions/${encodeURIComponent(String(message.id || ''))}/dismiss`, {
        method: 'POST',
        body: JSON.stringify({ occurrenceKey: String(message.occurrenceKey || '') }),
      })
      await this.refreshLiveCompanionInterventions()
      this.postState()
      break
    }
    case 'restoreCompanionInterventions': {
      await this.service.request('/api/companion/interventions/dismissed', { method: 'DELETE' })
      await this.refreshLiveCompanionInterventions()
      this.postState()
      break
    }
    case 'probeCompanionConnection': {
      const connectionId = String(message.connectionId || '')
      const connection = (this.boot?.connections || []).find(item => item.id === connectionId)
      if (!connection) {
        this.post({ type: 'companionProviderProbeResult', connectionId, result: { connected: false, error: 'Сохранённое подключение не найдено.', phase: 'probe' } })
        break
      }
      try {
        const preset = (this.boot?.providerCatalog || []).find(item => item.id === connection.presetId)
        const apiKey = connection.secretRef ? await this.context.secrets.get(connection.secretRef) || '' : ''
        if (preset?.requiresApiKey && !apiKey) throw Object.assign(new Error('Ключ подключения не найден в SecretStorage IDE.'), { phase: 'probe' })
        let result
        try {
          result = await this.service.request('/api/providers/probe', {
            method: 'POST',
            body: JSON.stringify({ provider: connection.provider, baseUrl: connection.baseUrl || preset?.baseUrl || '', apiKey }),
          })
        } catch (error) {
          throw Object.assign(error instanceof Error ? error : new Error(String(error)), { phase: 'probe' })
        }
        // Ядро теперь объясняет несостоявшуюся связь ответом 200 с
        // connected:false. Без этой проверки подключение сохранялось бы как
        // рабочее при неудачной проверке — ровно наоборот смыслу кнопки.
        if (!result?.connected) {
          this.post({ type: 'companionProviderProbeResult', connectionId: connection.id, result: {
            connected: false, phase: 'probe',
            problem: result?.problem || '', fix: result?.fix || '',
            error: result?.problem || 'Связь не установилась.',
          } })
          break
        }
        try {
          // UpsertRequest only — never spread timestamps like createdAt/updatedAt.
          const savedConnection = await this.service.request('/api/connections', {
            method: 'POST',
            body: JSON.stringify({
              id: connection.id,
              provider: connection.provider,
              presetId: connection.presetId || '',
              displayName: connection.displayName || '',
              baseUrl: connection.baseUrl || '',
              secretRef: connection.secretRef || '',
              status: 'connected',
            }),
          })
          this.upsertBootItem('connections', savedConnection)
        } catch (error) {
          throw Object.assign(error instanceof Error ? error : new Error(String(error)), { phase: 'save' })
        }
        this.post({ type: 'companionProviderProbeResult', connectionId: connection.id, result })
        this.postState()
      } catch (error) {
        const phase = error?.phase === 'save' ? 'save' : 'probe'
        const raw = error instanceof Error ? error.message : String(error)
        const messageText = phase === 'save'
          ? `Не удалось сохранить подключение: ${raw}`
          : `Не удалось подключиться к шлюзу: ${raw}`
        this.post({ type: 'companionProviderProbeResult', connectionId: connection.id, result: { connected: false, error: messageText, phase } })
      }
      break
    }
    case 'saveCompanionConfig': {
      const saved = await this.service.request('/api/companion/config', { method: 'POST', body: JSON.stringify(companionConfigPayload(message.config)) })
      this.patchBoot({ companion: saved })
      this.post({ type: 'companionConfigSaved', configId: saved.id })
      this.postState()
      break
    }
    case 'saveCompanionConfigAndChat': {
      let saved
      const traceId = newRequestId()
      try {
        try {
          this.service.hostLog('info', `[companion] ${traceId} setup save+test: saving config`)
          saved = await this.service.request('/api/companion/config', {
            method: 'POST',
            headers: { 'X-Request-Id': traceId },
            body: JSON.stringify(companionConfigPayload(message.config)),
          })
        } catch (error) {
          throw Object.assign(error instanceof Error ? error : new Error(String(error)), { phase: 'save' })
        }
        this.patchBoot({ companion: saved })
        try {
          const apiKey = await this.credentialForCompanion()
          this.service.hostLog('info', `[companion] ${traceId} setup save+test: chat prompt_bytes=${String(message.message || '').length} has_key=${Boolean(apiKey)}`)
          const response = await this.service.request('/api/companion/chat', {
            method: 'POST',
            timeoutMs: 90_000,
            headers: { 'X-Request-Id': `${traceId}c` },
            body: JSON.stringify({
              message: String(message.message || ''),
              apiKey,
              focus: typeof this.companionFocus === 'function' ? this.companionFocus() : (this.ideContext || {}),
            }),
          })
          this.service.hostLog('info', `[companion] ${traceId} setup save+test done mode=${response?.mode || '-'} fallback=${Boolean(response?.fallbackReason)}`)
          if (response?.fallbackReason) {
            this.service.hostLog('warn', `[companion] ${traceId} setup fallback: ${String(response.fallbackReason).slice(0, 500)}`)
          }
          await this.refreshLiveCompanionInterventions()
          this.post({ type: 'companionSetupTestResult', configId: saved.id, response })
          this.postState()
        } catch (error) {
          throw Object.assign(error instanceof Error ? error : new Error(String(error)), { phase: 'chat', configId: saved?.id })
        }
      } catch (error) {
        const phase = error?.phase === 'save' ? 'save' : 'chat'
        const raw = error instanceof Error ? error.message : String(error)
        const messageText = phase === 'save'
          ? `Не удалось сохранить настройки: ${raw}`
          : `Настройки сохранены, пробный ответ не получен: ${raw}`
        this.service.hostLog('error', `[companion] ${traceId} setup save+test error phase=${phase}: ${String(raw).slice(0, 500)}`)
        this.post({ type: 'companionSetupTestError', message: messageText, phase, configId: error?.configId || saved?.id })
        this.postState()
      }
      break
    }
  }
}

module.exports = {
  handleCompanionChatMessage,
  companionConfigPayload,
  bindCompanionChatHelpers,
  POINT_COMPANION_FEEDBACK_KEY,
}
