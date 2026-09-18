const endpoint = process.argv[2]
if (!endpoint) throw new Error('Usage: node verify-point-agents-window.mjs <endpoint>')

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds))
const listTargets = async () => {
  const response = await fetch(`${endpoint}/json/list`)
  if (!response.ok) throw new Error(`CDP target list failed: ${response.status}`)
  return response.json()
}

async function waitFor(description, probe, timeout = 45000) {
  const deadline = Date.now() + timeout
  let lastError
  while (Date.now() < deadline) {
    try {
      const value = await probe()
      if (value) return value
    } catch (error) {
      lastError = error
    }
    await delay(150)
  }
  throw new Error(`Timed out waiting for ${description}${lastError ? `: ${lastError.message}` : ''}`)
}

async function connect(target) {
  const socket = new WebSocket(target.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true })
    socket.addEventListener('error', reject, { once: true })
  })
  let sequence = 0
  const pending = new Map()
  socket.addEventListener('message', event => {
    const message = JSON.parse(String(event.data))
    if (!message.id || !pending.has(message.id)) return
    const handlers = pending.get(message.id)
    pending.delete(message.id)
    if (message.error) handlers.reject(new Error(message.error.message))
    else handlers.resolve(message.result)
  })
  const command = (method, params = {}) => {
    const id = ++sequence
    socket.send(JSON.stringify({ id, method, params }))
    return new Promise((resolve, reject) => pending.set(id, { resolve, reject }))
  }
  const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true }).then(result => result.result?.value)
  await Promise.all([command('Runtime.enable'), command('Page.enable')])
  return { socket, command, evaluate }
}

async function keyStroke(client, key, code, windowsVirtualKeyCode, modifiers = 0) {
  const event = { key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers }
  await client.command('Input.dispatchKeyEvent', { type: 'rawKeyDown', ...event })
  await client.command('Input.dispatchKeyEvent', { type: 'keyUp', ...event })
}

async function wideHubState(target) {
  const client = await connect(target)
  try {
    const tree = await client.command('Page.getFrameTree')
    const frames = []
    const collect = node => {
      frames.push(node.frame)
      for (const child of node.childFrames || []) collect(child)
    }
    collect(tree.frameTree)
    for (const frame of frames.reverse()) {
      try {
        const world = await client.command('Page.createIsolatedWorld', { frameId: frame.id, worldName: `point-agents-${Date.now()}` })
        const response = await client.command('Runtime.evaluate', {
          contextId: world.executionContextId,
          returnByValue: true,
          expression: `(() => ({
            layout: document.body?.dataset?.layout || '',
            title: document.title,
            master: Boolean(document.querySelector('#master-input, .hall-thread')),
            onboarding: Boolean(document.querySelector('.onboarding')),
            text: (document.body?.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 1000)
          }))()`,
        })
        if (response.result?.value?.layout === 'wide') return response.result.value
      } catch {
        // Webview frames can be replaced during initial paint.
      }
    }
    return null
  } finally {
    client.socket.close()
  }
}

async function findWideHubState() {
  const candidates = (await listTargets()).filter(target =>
    target.type === 'iframe' && String(target.url).includes('extensionId=local-agent.local-agent-workbench'))
  for (const candidate of candidates) {
    const value = await wideHubState(candidate)
    if (value?.layout === 'wide') return value
  }
  return null
}

const ideTarget = await waitFor('IDE workbench target', async () =>
  (await listTargets()).find(target => target.type === 'page' && String(target.url).includes('/workbench/')))
const ide = await connect(ideTarget)
try {
  await waitFor('IDE workbench DOM', () => ide.evaluate(`Boolean(document.querySelector('.monaco-workbench'))`))
  const trusted = await ide.evaluate(`(() => {
    const dialog = Array.from(document.querySelectorAll('.monaco-dialog-box')).find(item => (item.innerText || '').includes('Вы доверяете этому проекту?'))
    const button = Array.from(dialog?.querySelectorAll('.monaco-button') || []).find(item => (item.textContent || '').includes('Доверять проекту'))
    button?.click()
    return !dialog || Boolean(button)
  })()`)
  if (!trusted) throw new Error('Workspace trust button is missing')
  await delay(1400)

  await keyStroke(ide, 'i', 'KeyI', 73, 3)
  const agentsTarget = await waitFor('independent Agents window', async () =>
    (await listTargets()).find(target => target.type === 'page' && String(target.url).includes('/sessions/')))
  const agents = await connect(agentsTarget)
  try {
    await waitFor('Agents workbench DOM', () => agents.evaluate(`Boolean(document.querySelector('.monaco-workbench'))`))
    const hub = await waitFor('ready Agent Hub', async () => {
      const value = await findWideHubState()
      return value?.layout === 'wide' && !value.text.includes('Открываем доску квестов') ? value : null
    })
    if ((!hub.master && !hub.onboarding) || !/Мастер|Настроить мастера|С чего начнём|Начать настройку/.test(hub.text)) {
      throw new Error(`Independent Agents window did not render Hub content: ${JSON.stringify(hub)}`)
    }

    await keyStroke(ide, 'i', 'KeyI', 73, 3)
    await delay(1600)
    const agentsWindows = (await listTargets()).filter(target =>
      target.type === 'page' && String(target.url).includes('/sessions/'))
    if (agentsWindows.length !== 1) {
      throw new Error(`Repeated Hub open created ${agentsWindows.length} Agents windows`)
    }
    const ideAgentTabs = await ide.evaluate(`document.querySelectorAll('.tabs-container .tab[aria-label*="Агенты Point"]').length`)
    if (ideAgentTabs !== 0) throw new Error('Agent Hub leaked back into the IDE editor tabs')
    process.stdout.write(JSON.stringify({ mode: 'sessions', hub, agentsWindows: agentsWindows.length, ideAgentTabs }))
  } finally {
    agents.socket.close()
  }
} finally {
  ide.socket.close()
}
