// Чтение потока NDJSON от локального ядра.
//
// Метод жил в extension.js и рос вместе с ним против границы модуля
// (scripts/check-release-contracts.mjs: 7500 строк). Вынесен как есть:
// служба ядра передаётся первым доводом, а два её объяснителя — фабрике,
// потому что таблица CORE_FAILURE_HINTS по договорённости 12 обязана
// остаться в extension.js.
function createNdjsonReader({ newRequestId, describeCoreFailure }) {
  return async function requestNdjson(service, route, options = {}) {
  const { timeoutMs = 90_000, allowStart = true, signal: externalSignal, onProgress, onDelta, ...requestOptions } = options
  if (allowStart) await service.ensureStarted()
  else if (service.state !== 'running' || !service.baseUrl) throw new Error('Локальное ядро остановлено.')
  const controller = new AbortController()
  let timedOut = false
  const timer = setTimeout(() => {
    timedOut = true
    controller.abort()
  }, timeoutMs)
  const onExternalAbort = () => controller.abort()
  if (externalSignal) {
    if (externalSignal.aborted) controller.abort()
    else externalSignal.addEventListener('abort', onExternalAbort, { once: true })
  }
  const method = String(requestOptions.method || 'GET').toUpperCase()
  const started = Date.now()
  const incomingHeaders = requestOptions.headers || {}
  const requestId = String(incomingHeaders['X-Request-Id'] || incomingHeaders['x-request-id'] || newRequestId())
  try {
    const headers = {
      ...(requestOptions.body ? { 'Content-Type': 'application/json' } : {}),
      ...incomingHeaders,
      Accept: 'application/x-ndjson',
      'X-Request-Id': requestId,
    }
    if (service.apiToken) headers.Authorization = `Bearer ${service.apiToken}`
    const sep = route.includes('?') ? '&' : '?'
    const response = await fetch(`${service.baseUrl}${route}${sep}stream=1`, {
      ...requestOptions,
      signal: controller.signal,
      headers: { Connection: 'keep-alive', ...headers },
    })
    if (!response.ok) {
      const payload = await response.json().catch(() => ({}))
      throw new Error(payload?.error?.message || `HTTP ${response.status}`)
    }
    const reader = response.body?.getReader?.()
    if (!reader) {
      const payload = await response.json().catch(() => ({}))
      return payload?.response || payload
    }
    const decoder = new TextDecoder()
    let buffer = ''
    let result
    const handleEvent = (event) => {
      if (event?.type === 'progress' && typeof onProgress === 'function') {
        onProgress({ step: event.step, status: event.status })
      } else if (event?.type === 'delta' && typeof onDelta === 'function') {
        onDelta({ reply: String(event.reply || '') })
      } else if (event?.type === 'result') {
        result = event.response
      } else if (event?.type === 'error') {
        throw new Error(event.error?.message || 'Companion stream error')
      }
    }
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        const text = line.trim()
        if (!text) continue
        let event
        try { event = JSON.parse(text) } catch { continue }
        handleEvent(event)
      }
    }
    const tail = buffer.trim()
    if (tail) {
      try {
        handleEvent(JSON.parse(tail))
      } catch (error) {
        if (error?.message && !String(error.message).includes('JSON')) throw error
      }
    }
    service.hostLog('info', `[api] ${requestId} ${method} ${route}?stream=1 -> 200 ${Date.now() - started}ms ndjson`)
    if (!result) throw new Error('Companion stream ended without a result')
    return result
  } catch (error) {
    if (error?.name === 'AbortError' || controller.signal.aborted) {
      if (externalSignal?.aborted && !timedOut) {
        const cancel = new Error('Companion chat cancelled')
        cancel.name = 'AbortError'
        cancel.cancelled = true
        throw cancel
      }
      throw new Error(`Локальное ядро не ответило за ${Math.round(timeoutMs / 1000)} с. Откройте «Хроника ядра».`)
    }
    throw new Error(describeCoreFailure(error))
  } finally {
    clearTimeout(timer)
    if (externalSignal) externalSignal.removeEventListener('abort', onExternalAbort)
  }
  }
}

module.exports = { createNdjsonReader }
