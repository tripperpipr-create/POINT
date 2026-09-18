const endpoint = process.argv[2];
const startedAt = Number(process.argv[3] || Date.now());
const mode = process.argv[4] || 'idle';
if (!endpoint) throw new Error('Usage: node measure-point-startup.mjs <endpoint> <startedAtEpochMs> [idle|agent]');

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const elapsed = () => Math.round(Date.now() - startedAt);

async function waitFor(description, probe, timeout = 30000, interval = 50) {
  const deadline = Date.now() + timeout;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const value = await probe();
      if (value) return value;
    } catch (error) {
      lastError = error;
    }
    await delay(interval);
  }
  throw new Error(`Timed out waiting for ${description}${lastError ? `: ${lastError.message}` : ''}`);
}

async function targets() {
  return fetch(`${endpoint}/json/list`, { signal: AbortSignal.timeout(5000) }).then(response => {
    if (!response.ok) throw new Error(`CDP returned ${response.status}`);
    return response.json();
  });
}

async function connect(target) {
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`CDP socket open timed out for ${target.id}`)), 5000);
    socket.addEventListener('open', () => { clearTimeout(timer); resolve(); }, { once: true });
    socket.addEventListener('error', event => { clearTimeout(timer); reject(new Error(event.message || `CDP socket failed for ${target.id}`)); }, { once: true });
    socket.addEventListener('close', () => { clearTimeout(timer); reject(new Error(`CDP socket closed before opening for ${target.id}`)); }, { once: true });
  });
  let sequence = 0;
  const pending = new Map();
  socket.addEventListener('message', event => {
    const message = JSON.parse(String(event.data));
    if (!message.id || !pending.has(message.id)) return;
    const handlers = pending.get(message.id);
    pending.delete(message.id);
    clearTimeout(handlers.timer);
    if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
  });
  socket.addEventListener('close', () => {
    for (const handlers of pending.values()) {
      clearTimeout(handlers.timer);
      handlers.reject(new Error(`CDP socket closed for ${target.id}`));
    }
    pending.clear();
  });
  const command = (method, params = {}) => {
    const id = ++sequence;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`CDP ${method} timed out for ${target.id}`));
      }, 5000);
      pending.set(id, { resolve, reject, timer });
      try {
        socket.send(JSON.stringify({ id, method, params }));
      } catch (error) {
        clearTimeout(timer);
        pending.delete(id);
        reject(error);
      }
    });
  };
  const evaluate = expression => command('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }).then(result => result.result?.value);
  await command('Runtime.enable');
  return { socket, command, evaluate };
}

async function evaluateInAgentFrame(client, expression) {
  await client.command('Page.enable');
  const tree = await client.command('Page.getFrameTree');
  const frames = [];
  const collect = node => {
    frames.push(node.frame);
    for (const child of node.childFrames || []) collect(child);
  };
  collect(tree.frameTree);
  for (const frame of frames.reverse()) {
    try {
      const world = await client.command('Page.createIsolatedWorld', { frameId: frame.id, worldName: `point-performance-${Date.now()}` });
      const response = await client.command('Runtime.evaluate', {
        contextId: world.executionContextId,
        expression,
        returnByValue: true,
      });
      if (response.result?.value) return response.result.value;
    } catch {
      // The webview may navigate between discovery and evaluation; retry the next poll.
    }
  }
  return null;
}

const result = { mode, startedAt };
let renderer;
let agent;
try {
  const page = await waitFor('workbench CDP target', async () => {
    const list = await targets();
    return list.find(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://'));
  });
  result.rendererTargetMs = elapsed();
  result.rendererTargetId = page.id;
  renderer = await connect(page);
  result.rendererConnectedMs = elapsed();

  const workbench = await waitFor('visible workbench', async () => {
    const value = await renderer.evaluate(`(() => {
      const workbench = document.querySelector('.monaco-workbench');
      const editor = document.querySelector('.part.editor');
      const rect = workbench?.getBoundingClientRect();
      const readyMark = performance.getEntriesByName('code/didStartWorkbench').at(-1);
      return workbench && editor && readyMark && rect?.width > 200 && rect?.height > 200 ? {
        readyState: document.readyState,
        title: document.title,
        width: Math.round(rect.width),
        height: Math.round(rect.height),
        timeOrigin: performance.timeOrigin,
        didStartWorkbench: Math.round(readyMark.startTime),
        navigation: performance.getEntriesByType('navigation').map(entry => ({
          domInteractive: Math.round(entry.domInteractive),
          domContentLoaded: Math.round(entry.domContentLoadedEventEnd),
          loadEvent: Math.round(entry.loadEventEnd),
        }))[0] || null,
      } : null;
    })()`);
    return value;
  });
  result.workbenchDetectedMs = elapsed();
  // `code/didStartWorkbench` is emitted by Code-OSS after the layout and
  // visible editors are restored. Its timestamp measures product readiness;
  // the later CDP observation time is retained separately for probe health.
  result.workbenchVisibleMs = Math.round(workbench.timeOrigin - startedAt + workbench.didStartWorkbench);
  result.workbench = workbench;

  const frameCost = await renderer.evaluate(`new Promise(resolve => {
    const started = performance.now();
    requestAnimationFrame(() => requestAnimationFrame(() => resolve(Math.round((performance.now() - started) * 10) / 10)));
  })`);
  result.twoFrameLatencyMs = frameCost;

  if (mode === 'agent') {
    const trust = await renderer.evaluate(`(() => {
      const dialog = Array.from(document.querySelectorAll('.monaco-dialog-box')).find(item => (item.innerText || '').includes('Вы доверяете этому проекту?'));
      const button = Array.from(dialog?.querySelectorAll('.monaco-button') || []).find(item => (item.textContent || '').includes('Доверять проекту'));
      button?.click();
      return { found: Boolean(dialog), clicked: Boolean(button) };
    })()`);
    result.trust = trust;
    if (trust.clicked) await delay(800);
    // The canonical Hub is a dedicated window. Do not rely on a historical
    // Activity Bar label: that bar contains project tools, while the stable
    // user contract for opening the Hub is the registered command.
    // A clean Code-OSS profile starts its lazy extension host after workbench
    // visibility. Let that activation settle before the explicit user action;
    // the Hub SLO below still starts at the key event itself.
    await delay(3000);
    const agentStartedAt = Date.now();
    // Ctrl+Alt+I is the shipped, contract-tested Hub shortcut. Measuring it avoids
    // making the SLO depend on command-palette search/index timing, which is not
    // part of Agent Hub startup and can be flaky in a clean profile.
    const keyEvent = { key: 'i', code: 'KeyI', windowsVirtualKeyCode: 73, nativeVirtualKeyCode: 73, modifiers: 3 };
    await renderer.command('Input.dispatchKeyEvent', { type: 'rawKeyDown', ...keyEvent });
    await renderer.command('Input.dispatchKeyEvent', { type: 'keyUp', ...keyEvent });
    result.agentClickMs = elapsed();

    const agentSurface = await waitFor('agent webview CDP target', async () => {
      const list = await targets();
      const candidates = list.filter(candidate => candidate.type === 'iframe'
        && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench'));
      for (const candidate of candidates) {
        let candidateClient;
        try {
          candidateClient = await connect(candidate);
          const surface = await evaluateInAgentFrame(candidateClient, `(() => {
            const root = document.querySelector('#root');
            const wide = document.body?.dataset?.layout === 'wide';
            const hub = Boolean(root?.querySelector('.onboarding, .hub-page, .hub-changesets-page, .hub-journal'));
            return root && wide && hub ? { wide, hub } : null;
          })()`);
          if (surface) return { target: candidate, client: candidateClient, surface };
        } catch {
          // A new webview can still be navigating. The next poll retries it.
        }
        candidateClient?.socket.close();
      }
      return null;
    });
    result.agentTargetAfterClickMs = Math.round(Date.now() - agentStartedAt);
    agent = agentSurface.client;
    result.agentSurface = agentSurface.surface;
    const firstDom = await waitFor('agent first DOM', async () => evaluateInAgentFrame(agent, `(() => {
      const root = document.querySelector('#root');
      return root && root.getBoundingClientRect().height > 20 ? {
        text: (root.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 300),
        loading: Boolean(root.querySelector('.loading')),
      } : null;
    })()`));
    result.agentFirstDomAfterClickMs = Math.round(Date.now() - agentStartedAt);
    result.agentFirstDom = firstDom;
    const ready = await waitFor('agent usable UI', async () => evaluateInAgentFrame(agent, `(() => {
      const root = document.querySelector('#root');
      const text = (root?.innerText || '').replace(/\\s+/g, ' ').trim();
      const loading = Boolean(root?.querySelector('.loading'));
      const task = Boolean(root?.querySelector('#task'));
      const locked = Boolean(root?.querySelector('.workspace-locked'));
      const onboarding = Boolean(root?.querySelector('.onboarding'));
      const hubPage = Boolean(root?.querySelector('.hub-page, .hub-changesets-page, .hub-journal'));
      const wide = document.body?.dataset?.layout === 'wide';
      return !loading && wide && (task || locked || onboarding || hubPage)
        ? { text: text.slice(0, 500), task, locked, onboarding, hubPage, wide }
        : null;
    })()`), 30000);
    result.agentUsableAfterClickMs = Math.round(Date.now() - agentStartedAt);
    result.agentReady = ready;
  }

  process.stdout.write(JSON.stringify(result));
} finally {
  agent?.socket.close();
  renderer?.socket.close();
}
