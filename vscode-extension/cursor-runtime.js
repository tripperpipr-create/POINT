'use strict'

const path = require('path')

const BUNDLED_SDK_ENTRY = path.join(__dirname, 'dist', 'cursor-sdk', 'index.js')

// The SDK is optional at runtime so Point can still open on installs that have
// not restored npm dependencies yet.
function loadSdk() {
  try {
    configurePlatformAssets()
    return { sdk: require(BUNDLED_SDK_ENTRY) }
  } catch (error) {
    return { sdk: undefined, error }
  }
}

function sdkIsInstalled() {
  try {
    require.resolve(BUNDLED_SDK_ENTRY)
    return true
  } catch {
    return false
  }
}

function configurePlatformAssets() {
  if (process.env.CURSOR_TREE_SITTER_VENDOR_DIR) return
  const packageName = `@cursor/sdk-${process.platform}-${process.arch}`
  try {
    const packageFile = require.resolve(`${packageName}/package.json`)
    process.env.CURSOR_TREE_SITTER_VENDOR_DIR = path.join(path.dirname(packageFile), 'vendor')
  } catch { /* optional platform package is unavailable */ }
}

const POINT_TOOL_MAP = {
  project_map: ['ls', 'glob'],
  search_code: ['grep'],
  list_files: ['ls', 'glob'],
  read_file: ['read'],
  search_text: ['grep'],
  propose_patch: ['edit', 'write', 'delete'],
  run_command: ['shell'],
  git_diff: ['shell'],
}

const SDK_TOOLS = ['read', 'edit', 'write', 'delete', 'grep', 'glob', 'ls', 'shell']

function mapAllowedTools(allowedTools) {
  const requested = Array.isArray(allowedTools) ? allowedTools : []
  const enabled = new Set()
  for (const tool of requested) {
    for (const sdkTool of POINT_TOOL_MAP[String(tool)] || []) enabled.add(sdkTool)
    if (SDK_TOOLS.includes(String(tool))) enabled.add(String(tool))
  }
  return {
    tools: [...enabled],
    disallowedTools: SDK_TOOLS.filter(tool => !enabled.has(tool)),
  }
}

function buildPrompt(profile = {}, task = '') {
  const sections = []
  const add = (title, value) => {
    const text = String(value || '').trim()
    if (text) sections.push(`${title}:\n${text}`)
  }
  add('Role', profile.roleDescription)
  add('System instructions', profile.systemPrompt)
  const goals = Array.isArray(profile.goals) ? profile.goals.map(String).filter(Boolean) : []
  if (goals.length) sections.push(`Goals:\n${goals.map(goal => `- ${goal}`).join('\n')}`)
  const rules = Array.isArray(profile.rules) ? profile.rules.map(String).filter(Boolean) : []
  if (rules.length) sections.push(`Rules:\n${rules.map(rule => `- ${rule}`).join('\n')}`)
  add('User task', task)
  return sections.join('\n\n')
}

function textFromMessage(message) {
  return (message?.message?.content || [])
    .filter(block => block?.type === 'text')
    .map(block => block.text)
    .join('')
}

function normalizeEvent(message) {
  const common = {
    agentId: message?.agent_id,
    runId: message?.run_id,
    raw: message,
  }
  if (message?.type === 'assistant') return { type: 'assistant', ...common, text: textFromMessage(message), message: message.message }
  if (message?.type === 'tool_call') return { type: 'tool', ...common, callId: message.call_id, name: message.name, status: message.status, args: message.args, result: message.result }
  if (message?.type === 'status' && message.status === 'ERROR') return { type: 'error', ...common, message: message.message || 'Cursor run failed', status: message.status }
  return { type: 'system', ...common, message }
}

function isAuthenticated(status) {
  if (status === true) return true
  if (!status || typeof status !== 'object') return false
  if (status.authenticated === true || status.loggedIn === true) return true
  const value = String(status.status || '').toLowerCase()
  return value === 'authenticated' || value === 'logged-in' || value === 'logged_in'
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error || 'Unknown Cursor SDK error')
}

function resolveApiKey(explicit) {
  const value = String(explicit || process.env.CURSOR_API_KEY || '').trim()
  return value || undefined
}

function createRuntime({ sdk: injectedSdk, apiKeyProvider, sdkLoader = loadSdk, sdkAvailable } = {}) {
  let loaded = injectedSdk ? { sdk: injectedSdk } : undefined
  const available = injectedSdk
    ? true
    : (typeof sdkAvailable === 'boolean' ? sdkAvailable : sdkIsInstalled())

  function currentSdk() {
    if (!loaded) loaded = sdkLoader()
    return loaded
  }

  async function currentApiKey(explicit) {
    if (explicit) return resolveApiKey(explicit)
    if (typeof apiKeyProvider === 'function') {
      try {
        const provided = await apiKeyProvider()
        if (provided) return resolveApiKey(provided)
      } catch { /* fall through */ }
    }
    return resolveApiKey()
  }

  async function status({ includeModels = true, apiKey } = {}) {
    const loaded = currentSdk()
    const sdk = loaded.sdk
    if (!sdk) return { available: false, authenticated: false, error: errorMessage(loaded.error) }
    const Cursor = sdk.Cursor
    if (!Cursor) return { available: true, authenticated: false, error: 'Installed @cursor/sdk does not expose Cursor.' }

    try {
      let auth
      let authenticated = false
      let email
      let expiresAt

      if (Cursor.auth?.status) {
        auth = await Cursor.auth.status()
        authenticated = isAuthenticated(auth)
        email = auth?.email
        expiresAt = auth?.apiKeyExpiresAtMs
      }

      const key = await currentApiKey(apiKey)
      if (!authenticated && key && Cursor.me) {
        try {
          const me = await Cursor.me({ apiKey: key })
          authenticated = true
          email = me?.email || me?.apiKeyName || email
          auth = { status: 'logged-in', source: 'apiKey', me }
        } catch (error) {
          return {
            available: true,
            authenticated: false,
            error: errorMessage(error),
            auth,
          }
        }
      }

      if (!authenticated && !Cursor.auth?.status && !Cursor.me) {
        return {
          available: true,
          authenticated: false,
          error: 'Installed @cursor/sdk does not expose Cursor.auth or Cursor.me(). Upgrade @cursor/sdk.',
        }
      }

      const state = {
        available: true,
        authenticated,
        email,
        expiresAt,
        model: undefined,
        auth,
      }
      if (authenticated && includeModels && Cursor.models?.list) {
        const options = key ? { apiKey: key } : undefined
        state.models = await Cursor.models.list(options)
        state.model = state.models.find(model => model.id === 'auto')?.id || state.models[0]?.id
      }
      return state
    } catch (error) {
      return { available: true, authenticated: false, error: errorMessage(error) }
    }
  }

  async function login(options = {}) {
    const sdk = currentSdk().sdk
    if (!sdk?.Cursor) throw new Error('Installed @cursor/sdk does not expose Cursor.')
    if (!sdk.Cursor.auth?.login) {
      throw new Error('Installed @cursor/sdk does not expose Cursor.auth.login(). Upgrade to @cursor/sdk >= 1.0.27.')
    }
    const loginOptions = {
      apiKeyName: options.apiKeyName || 'Point IDE',
      openBrowser: options.openBrowser,
      onLoginUrl: options.onLoginUrl,
      signal: options.signal,
      store: options.store,
    }
    Object.keys(loginOptions).forEach(key => {
      if (loginOptions[key] === undefined) delete loginOptions[key]
    })
    await sdk.Cursor.auth.login(loginOptions)
    return status()
  }

  async function logout(options = {}) {
    const sdk = currentSdk().sdk
    if (!sdk?.Cursor?.auth?.logout) {
      throw new Error('Installed @cursor/sdk does not expose Cursor.auth.logout(). Upgrade to @cursor/sdk >= 1.0.27.')
    }
    await sdk.Cursor.auth.logout(options)
    return status({ includeModels: false })
  }

  function startRun({ profile = {}, task, cwd, apiKey, onEvent = () => {} }) {
    const sdk = currentSdk().sdk
    if (!sdk?.Agent) throw new Error('Cursor SDK is unavailable.')
    let agent
    let run
    let disposed = false
    let cancelled = false
    const emit = event => onEvent(event)
    const dispose = async () => {
      if (disposed) return
      disposed = true
      if (agent?.[Symbol.asyncDispose]) await agent[Symbol.asyncDispose]()
      else agent?.close?.()
    }
    const cancel = async () => {
      cancelled = true
      if (run && (!run.supports || run.supports('cancel'))) await run.cancel()
    }
    const done = (async () => {
      try {
        const mappedTools = mapAllowedTools(profile.allowedTools)
        const key = await currentApiKey(apiKey)
        const local = { cwd, settingSources: [] }
        if (typeof profile.sandboxEnabled === 'boolean') local.sandboxOptions = { enabled: profile.sandboxEnabled }
        const createOptions = {
          local,
          model: { id: String(profile.model || 'auto').trim() || 'auto' },
          tools: mappedTools.tools,
          disallowedTools: mappedTools.disallowedTools,
        }
        if (key) createOptions.apiKey = key
        agent = await sdk.Agent.create(createOptions)
        if (cancelled) {
          await dispose()
          return { status: 'cancelled' }
        }
        run = await agent.send(buildPrompt(profile, task))
        emit({ type: 'system', agentId: run.agentId, runId: run.id, message: { type: 'run_started' } })
        let streamError
        try {
          for await (const message of run.stream()) emit(normalizeEvent(message))
        } catch (error) {
          streamError = error
          emit({ type: 'error', agentId: run.agentId, runId: run.id, kind: 'stream', message: errorMessage(error), error })
        } finally {
          const result = await run.wait()
          if (result.status === 'error') emit({ type: 'error', agentId: run.agentId, runId: run.id, kind: 'result', message: result.result || 'Cursor run failed', result })
          emit({ type: 'done', agentId: run.agentId, runId: run.id, status: result.status, result, streamError: streamError ? errorMessage(streamError) : undefined })
          return result
        }
      } catch (error) {
        const startup = sdk.CursorAgentError && error instanceof sdk.CursorAgentError
        emit({ type: 'error', kind: startup ? 'startup' : 'runtime', message: errorMessage(error), retryable: error?.isRetryable === true, error })
        throw error
      } finally {
        await dispose()
      }
    })()
    return { done, cancel, dispose, get run() { return run } }
  }

  return { available, status, login, logout, startRun }
}

const runtime = createRuntime()

module.exports = {
  ...runtime,
  __test: { buildPrompt, configurePlatformAssets, createRuntime, isAuthenticated, mapAllowedTools, normalizeEvent, resolveApiKey, sdkIsInstalled, textFromMessage },
}
