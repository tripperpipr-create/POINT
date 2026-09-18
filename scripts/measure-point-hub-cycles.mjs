const endpoint = String(process.argv[2] || '').replace(/\/$/, '')
const action = process.argv[3] || 'cycle'
const cycles = Number(process.argv[4] || 50)
const workbenchTargetId = String(process.argv[5] || '').replace(/^auto$/, '')
const baseTargetId = String(process.argv[6] || '')
const settleSeconds = Number(process.argv[7] || 0)
const diagnosticGc = process.env.POINT_CYCLE_DIAGNOSTIC_GC === '1'
if (!endpoint || !['close', 'cycle'].includes(action) || !Number.isInteger(cycles) || cycles < 1 || cycles > 200
  || !Number.isFinite(settleSeconds) || settleSeconds < 0 || settleSeconds > 120) {
  throw new Error('Usage: node measure-point-hub-cycles.mjs <cdp-endpoint> <close|cycle> [cycles] [workbench-target] [base-target] [settle-seconds]')
}

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds))

async function waitFor(description, probe, timeout = 30_000, interval = 75) {
  const deadline = Date.now() + timeout
  let lastError
  while (Date.now() < deadline) {
    try {
      const value = await probe()
      if (value) return value
    } catch (error) {
      lastError = error
    }
    await delay(interval)
  }
  throw new Error(`Timed out waiting for ${description}${lastError ? `: ${lastError.message}` : ''}`)
}

async function targets() {
  const response = await fetch(`${endpoint}/json/list`, { signal: AbortSignal.timeout(5_000) })
  if (!response.ok) throw new Error(`CDP returned ${response.status}`)
  return response.json()
}

async function connect(target) {
  const socket = new WebSocket(target.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`CDP socket open timed out for ${target.id}`)), 5_000)
    socket.addEventListener('open', () => { clearTimeout(timer); resolve() }, { once: true })
    socket.addEventListener('error', event => { clearTimeout(timer); reject(new Error(`CDP socket failed for ${target.id}: ${event.message || 'connection error'}`)) }, { once: true })
    socket.addEventListener('close', () => { clearTimeout(timer); reject(new Error(`CDP socket closed before opening for ${target.id}`)) }, { once: true })
  })
  let sequence = 0
  const pending = new Map()
  socket.addEventListener('message', event => {
    const message = JSON.parse(String(event.data))
    if (!message.id || !pending.has(message.id)) return
    const handlers = pending.get(message.id)
    pending.delete(message.id)
    clearTimeout(handlers.timer)
    if (message.error) handlers.reject(new Error(message.error.message))
    else handlers.resolve(message.result)
  })
  socket.addEventListener('close', () => {
    for (const handlers of pending.values()) {
      clearTimeout(handlers.timer)
      handlers.reject(new Error(`CDP socket closed for ${target.id}`))
    }
    pending.clear()
  })
  const command = (method, params = {}) => {
    const id = ++sequence
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id)
        reject(new Error(`CDP ${method} timed out for ${target.id}`))
      }, 5_000)
      pending.set(id, { resolve, reject, timer })
      try {
        socket.send(JSON.stringify({ id, method, params }))
      } catch (error) {
        clearTimeout(timer)
        pending.delete(id)
        reject(error)
      }
    })
  }
  const evaluate = expression => command('Runtime.evaluate', {
    expression, awaitPromise: true, returnByValue: true,
  }).then(result => result.result?.value)
  await command('Runtime.enable')
  return { socket, command, evaluate }
}

async function evaluateFrames(client, expression) {
  await client.command('Page.enable')
  const tree = await client.command('Page.getFrameTree')
  const frames = []
  const collect = node => {
    frames.push(node.frame)
    for (const child of node.childFrames || []) collect(child)
  }
  collect(tree.frameTree)
  for (const frame of frames.reverse()) {
    try {
      const world = await client.command('Page.createIsolatedWorld', {
        frameId: frame.id, worldName: `point-hub-cycle-${Date.now()}`,
      })
      const response = await client.command('Runtime.evaluate', {
        contextId: world.executionContextId,
        expression,
        returnByValue: true,
      })
      if (response.result?.value) return response.result.value
    } catch {
      // A workbench/webview can navigate while a target is being inspected.
    }
  }
  return null
}

async function inspectPage(target) {
  let client
  try {
    client = await connect(target)
    const wide = await evaluateFrames(client, `(() => {
      const root = document.querySelector('#root')
      return document.body?.dataset?.layout === 'wide' && root
        ? { ready: !root.querySelector('.loading'), height: Math.round(root.getBoundingClientRect().height) }
        : null
    })()`)
    const workbench = await client.evaluate(`(() => {
      const node = document.querySelector('.monaco-workbench')
      const rect = node?.getBoundingClientRect()
      return rect?.width > 200 && rect?.height > 200
    })()`)
    const agentWorkbench = await client.evaluate(`(() => {
      const active = document.querySelector('.editor-group-container.active .tab.active, .tabs-container .tab.active')
      const text = [document.title, active?.textContent || ''].join(' ')
      return /Агенты Point|Agent Hub/i.test(text)
    })()`)
    return { target, wide, workbench: Boolean(workbench), agentWorkbench: Boolean(agentWorkbench) }
  } catch {
    return { target, wide: null, workbench: false, agentWorkbench: false }
  } finally {
    client?.socket.close()
  }
}

async function pageInventory() {
  const pages = (await targets()).filter(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://'))
  return Promise.all(pages.map(inspectPage))
}

async function runtimeMemory() {
  const candidates = (await targets()).filter(candidate => ['page', 'iframe'].includes(candidate.type)
    && !String(candidate.url).startsWith('devtools://'))
  const samples = []
  for (const candidate of candidates) {
    let client
    try {
      client = await connect(candidate)
      const heap = await client.command('Runtime.getHeapUsage')
      let dom = {}
      try {
        dom = await client.command('Memory.getDOMCounters')
      } catch {
        // Some iframe targets expose Runtime but not the Memory domain.
      }
      samples.push({
        targetId: candidate.id,
        type: candidate.type,
        role: candidate.type === 'iframe' && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench')
          ? 'point-webview'
          : candidate.type === 'page' ? 'workbench' : 'other-frame',
        usedHeapMB: Math.round(Number(heap.usedSize || 0) / 1024 / 1024 * 10) / 10,
        totalHeapMB: Math.round(Number(heap.totalSize || 0) / 1024 / 1024 * 10) / 10,
        documents: Number(dom.documents || 0),
        nodes: Number(dom.nodes || 0),
        jsEventListeners: Number(dom.jsEventListeners || 0),
      })
    } catch {
      // Targets can disappear while an auxiliary window or webview is closing.
    } finally {
      client?.socket.close()
    }
  }
  return samples.sort((left, right) => `${left.role}:${left.targetId}`.localeCompare(`${right.role}:${right.targetId}`))
}

async function collectRuntimeGarbage() {
  const candidates = (await targets()).filter(candidate => ['page', 'iframe'].includes(candidate.type)
    && !String(candidate.url).startsWith('devtools://'))
  for (const candidate of candidates) {
    let client
    try {
      client = await connect(candidate)
      await client.command('HeapProfiler.enable')
      await client.command('HeapProfiler.collectGarbage')
    } catch {
      // Targets can disappear while an auxiliary window or webview is closing.
    } finally {
      client?.socket.close()
    }
  }
}

async function agentPage() {
  const inventory = await pageInventory()
  const explicit = inventory.find(item => item.agentWorkbench || item.wide || /point-hub|agentsWindow/i.test(String(item.target.url)))
  if (explicit) return explicit.target
  const remaining = inventory.filter(item => !baseTargetId || item.target.id !== baseTargetId)
  if (remaining.length === 1 && await agentFrame()) return remaining[0].target
  return undefined
}

async function agentFrame() {
  const candidates = (await targets()).filter(candidate => candidate.type === 'iframe'
    && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench'))
  let hidden
  for (const candidate of candidates) {
    let client
    try {
      client = await connect(candidate)
      const value = await evaluateFrames(client, `(() => {
        const root = document.querySelector('#root')
        return document.body?.dataset?.layout === 'wide' && root
          ? { ready: !root.querySelector('.loading'), height: Math.round(root.getBoundingClientRect().height), visible: document.visibilityState === 'visible' }
          : null
      })()`)
      if (value?.visible) return { target: candidate, value }
      if (value && !hidden) hidden = { target: candidate, value }
    } catch {
      // Webview target may be between navigations.
    } finally {
      client?.socket.close()
    }
  }
  return hidden
}

async function basePage() {
  const inventory = await pageInventory()
  return (inventory.find(item => item.workbench && !item.wide) || inventory.find(item => !item.wide))?.target
}

async function targetById(id) {
  if (!id) return undefined
  return (await targets()).find(candidate => candidate.type === 'page' && candidate.id === id)
}

async function closeHub(targetId = '') {
  // Prefer the page that actually owns the visible Hub. Auxiliary editor
  // windows can replace their CDP page target when the window is closed, while
  // the original IDE workbench remains available under another target id.
  const target = await agentPage() || await targetById(targetId)
  if (!target) return { found: false, closeMs: 0, targetId: '' }
  const before = await agentFrame()
  if (!before) return { found: false, closeMs: 0, targetId: target.id }
  const started = Date.now()
  const dispatchClose = async candidate => {
    const client = await connect(candidate)
    try {
      try {
        await client.command('Input.dispatchKeyEvent', {
          type: 'rawKeyDown', key: 'F4', code: 'F4', modifiers: 2, windowsVirtualKeyCode: 115, nativeVirtualKeyCode: 115,
        })
        await client.command('Input.dispatchKeyEvent', {
          type: 'keyUp', key: 'F4', code: 'F4', modifiers: 2, windowsVirtualKeyCode: 115, nativeVirtualKeyCode: 115,
        })
      } catch (error) {
        // Closing an auxiliary editor window can destroy its CDP page between
        // key-down and key-up. That is the expected close transition; only keep
        // the protocol error when the target actually survived.
        if (await targetById(candidate.id)) throw error
      }
    } finally {
      client.socket.close()
    }
  }
  await dispatchClose(target)
  // Auxiliary page focus can race webview readiness. Retry against the page
  // that still owns the visible Hub instead of waiting the entire timeout.
  for (let attempt = 0; attempt < 2; attempt++) {
    await delay(750)
    const frame = await agentFrame()
    if (!frame?.value?.visible) break
    const retryTarget = await agentPage()
    if (retryTarget) await dispatchClose(retryTarget)
  }
  const survivingWorkbench = await waitFor('Agent Hub editor close', async () => {
    const frame = await agentFrame()
    if (frame?.value?.visible) return undefined
    return await basePage() || await targetById(target.id)
  }, 20_000)
  return { found: true, closeMs: Date.now() - started, targetId: survivingWorkbench.id }
}

async function openHub(targetId = '') {
  const page = await waitFor('Agent workbench page', async () => await targetById(targetId) || await basePage())
  const client = await connect(page)
  const started = Date.now()
  try {
    await client.command('Input.dispatchKeyEvent', {
      type: 'rawKeyDown', key: 'i', code: 'KeyI', modifiers: 3, windowsVirtualKeyCode: 73, nativeVirtualKeyCode: 73,
    })
    await client.command('Input.dispatchKeyEvent', {
      type: 'keyUp', key: 'i', code: 'KeyI', modifiers: 3, windowsVirtualKeyCode: 73, nativeVirtualKeyCode: 73,
    })
  } finally {
    client.socket.close()
  }
  await waitFor('usable Agent Hub editor', async () => {
    const current = await targetById(page.id)
    if (!current) throw new Error('Agent workbench target disappeared')
    const frame = await agentFrame()
    return frame?.value?.visible && frame.value.ready && frame.value.height > 20
  }, 30_000)
  return { openMs: Date.now() - started, targetId: page.id }
}

const initialClose = await closeHub(workbenchTargetId)
const stableTargetId = initialClose.targetId || workbenchTargetId
const report = {
  schemaVersion: 1,
  action,
  requestedCycles: action === 'cycle' ? cycles : 0,
  completedCycles: 0,
  workbenchTargetId: stableTargetId,
  initialClose,
  baselineRuntimeMemory: await runtimeMemory(),
  samples: [],
}
if (action === 'cycle') {
  for (let index = 1; index <= cycles; index++) {
    const opened = await openHub(stableTargetId)
    const closed = await closeHub(stableTargetId)
    report.samples.push({ cycle: index, openMs: opened.openMs, closeMs: closed.closeMs })
    report.completedCycles = index
  }
}
report.maxOpenMs = report.samples.length ? Math.max(...report.samples.map(item => item.openMs)) : 0
report.maxCloseMs = report.samples.length ? Math.max(...report.samples.map(item => item.closeMs)) : report.initialClose.closeMs
if (settleSeconds > 0) await delay(settleSeconds * 1000)
report.finalRuntimeMemory = await runtimeMemory()
if (diagnosticGc) {
  await collectRuntimeGarbage()
  report.postCollectionRuntimeMemory = await runtimeMemory()
}
process.stdout.write(JSON.stringify(report))
