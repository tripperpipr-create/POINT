import fs from 'node:fs';

const endpoint = process.argv[2];
const output = process.argv[3];
if (!endpoint) throw new Error('Usage: node verify-point-agent-onboarding.mjs <endpoint> [output.png]');
const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
async function waitFor(description, probe, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try { const value = await probe(); if (value) return value; } catch {}
    await delay(100);
  }
  throw new Error(`Timed out waiting for ${description}`);
}
async function captureTarget(target) {
  if (!target?.webSocketDebuggerUrl) throw new Error('Screenshot target is missing');
  const captureSocket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    captureSocket.addEventListener('open', resolve, { once: true });
    captureSocket.addEventListener('error', reject, { once: true });
  });
  let captureSequence = 0;
  const capturePending = new Map();
  captureSocket.addEventListener('message', event => {
    const message = JSON.parse(String(event.data));
    if (!message.id || !capturePending.has(message.id)) return;
    const handlers = capturePending.get(message.id); capturePending.delete(message.id);
    if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
  });
  const call = (method, params = {}) => {
    const id = ++captureSequence;
    captureSocket.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => capturePending.set(id, { resolve, reject }));
  };
  try {
    await call('Page.enable');
    return await call('Page.captureScreenshot', { format: 'png', fromSurface: true });
  } finally {
    captureSocket.close();
  }
}
async function targetHasWideOnboarding(target) {
  if (!target?.webSocketDebuggerUrl) return false;
  const probeSocket = new WebSocket(target.webSocketDebuggerUrl);
  try {
    await new Promise((resolve, reject) => {
      probeSocket.addEventListener('open', resolve, { once: true });
      probeSocket.addEventListener('error', reject, { once: true });
    });
    let probeSequence = 0;
    const probePending = new Map();
    probeSocket.addEventListener('message', event => {
      const message = JSON.parse(String(event.data));
      if (!message.id || !probePending.has(message.id)) return;
      const handlers = probePending.get(message.id); probePending.delete(message.id);
      if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
    });
    const call = (method, params = {}) => {
      const id = ++probeSequence;
      probeSocket.send(JSON.stringify({ id, method, params }));
      return new Promise((resolve, reject) => probePending.set(id, { resolve, reject }));
    };
    await Promise.all([call('Runtime.enable'), call('Page.enable')]);
    const tree = await call('Page.getFrameTree');
    const frames = [];
    const collect = node => { frames.push(node.frame); for (const child of node.childFrames || []) collect(child); };
    collect(tree.frameTree);
    for (const frame of frames.reverse()) {
      try {
        const world = await call('Page.createIsolatedWorld', { frameId: frame.id, worldName: `point-onboarding-probe-${Date.now()}` });
        const result = await call('Runtime.evaluate', {
          contextId: world.executionContextId,
          returnByValue: true,
          expression: `document.body?.dataset?.layout === 'wide' && Boolean(document.querySelector('.onboarding'))`,
        });
        if (result.result?.value) return true;
      } catch {}
    }
    return false;
  } catch {
    return false;
  } finally {
    probeSocket.close();
  }
}

const listTargets = () => fetch(`${endpoint}/json/list`).then(response => response.json());
const page = await waitFor('workbench target', async () => (await listTargets()).find(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://')));
const socket = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
let sequence = 0;
const pending = new Map();
socket.addEventListener('message', event => {
  const message = JSON.parse(String(event.data));
  if (!message.id || !pending.has(message.id)) return;
  const handlers = pending.get(message.id); pending.delete(message.id);
  if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
});
const command = (method, params = {}) => {
  const id = ++sequence; socket.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
};
const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true }).then(result => result.result?.value);

try {
  await Promise.all([command('Runtime.enable'), command('Page.enable')]);
  await command('Page.bringToFront');
  await waitFor('workbench DOM', () => evaluate(`Boolean(document.querySelector('.monaco-workbench'))`));
  await delay(900);
  // The main IDE can already have companion/home webviews from this extension.
  // Their iframe URL has the same extensionId, so accepting the first match can
  // silently inspect the wrong surface and skip opening the dedicated Hub window.
  const existingAgentFrames = new Set((await listTargets())
    .filter(candidate => candidate.type === 'iframe' && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench'))
    .map(candidate => candidate.webSocketDebuggerUrl));
  let paletteReady = false;
  for (let attempt = 0; attempt < 4 && !paletteReady; attempt += 1) {
    await command('Page.bringToFront');
    await evaluate(`(() => { document.querySelector('.monaco-workbench')?.focus(); document.body.focus(); return true; })()`);
    await command('Input.dispatchKeyEvent', { type:'rawKeyDown', key:'F1', code:'F1', windowsVirtualKeyCode:112, nativeVirtualKeyCode:112 });
    await command('Input.dispatchKeyEvent', { type:'keyUp', key:'F1', code:'F1', windowsVirtualKeyCode:112, nativeVirtualKeyCode:112 });
    await delay(500);
    paletteReady = await evaluate(`(() => { const input = document.querySelector('.quick-input-widget input'); input?.focus(); return Boolean(input); })()`);
  }
  if (!paletteReady) throw new Error('Timed out waiting for command palette input after four focused attempts');
  await command('Input.insertText', { text:'Открыть доску квестов' });
  const opened = await waitFor('Agent Hub command row', async () => {
    const result = await evaluate(`(() => {
      const rows = Array.from(document.querySelectorAll('.quick-input-widget .monaco-list-row'));
      const target = rows.find(row => (row.innerText || '').includes('Открыть доску квестов'));
      target?.click();
      return { clicked: Boolean(target), rows: rows.map(row => (row.innerText || '').replace(/\\s+/g, ' ').trim()) };
    })()`);
    return result?.clicked ? result : false;
  }, 15000);
  const iframeTarget = await waitFor('new semantic Agent Hub webview target', async () => {
    const candidates = (await listTargets()).filter(candidate =>
      candidate.type === 'iframe'
        && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench')
        && !existingAgentFrames.has(candidate.webSocketDebuggerUrl));
    for (const candidate of candidates) {
      if (await targetHasWideOnboarding(candidate)) return candidate;
    }
    return undefined;
  });
  const agentSocket = new WebSocket(iframeTarget.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { agentSocket.addEventListener('open', resolve, { once: true }); agentSocket.addEventListener('error', reject, { once: true }); });
  let agentSequence = 0;
  const agentPending = new Map();
  agentSocket.addEventListener('message', event => {
    const message = JSON.parse(String(event.data));
    if (!message.id || !agentPending.has(message.id)) return;
    const handlers = agentPending.get(message.id); agentPending.delete(message.id);
    if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
  });
  const agentCommand = (method, params = {}) => {
    const id = ++agentSequence; agentSocket.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => agentPending.set(id, { resolve, reject }));
  };
  await Promise.all([agentCommand('Runtime.enable'), agentCommand('Page.enable')]);
  const onboarding = await waitFor('onboarding page', async () => {
    const tree = await agentCommand('Page.getFrameTree');
    const frames = [];
    const collect = node => { frames.push(node.frame); for (const child of node.childFrames || []) collect(child); };
    collect(tree.frameTree);
    for (const frame of frames.reverse()) {
      try {
        const world = await agentCommand('Page.createIsolatedWorld', { frameId: frame.id, worldName: `point-onboarding-${Date.now()}` });
        const response = await agentCommand('Runtime.evaluate', { contextId: world.executionContextId, returnByValue: true, expression: `(() => {
          const page = document.querySelector('.onboarding');
          if (!page) return null;
          const rect = page.getBoundingClientRect();
          const providers = Array.from(page.querySelectorAll('.onboarding-providers strong')).map(item => item.textContent?.trim());
          const text = page.innerText || '';
          return {
            layout: document.body.dataset.layout,
            heading: page.querySelector('h1')?.textContent?.trim(),
            steps: page.querySelectorAll('.onboarding-step-rail [data-action="onboarding-step"]').length,
            providers,
            hasStart: /начать|продолжить/i.test(text),
            hasCompanion: text.includes('Компаньон'),
            hasMaster: text.includes('Мастер'),
            hasPath: Boolean(page.querySelector('.onboarding-path')),
            horizontalOverflow: Math.max(0, page.scrollWidth - page.clientWidth),
            width: Math.round(rect.width),
            fontSize: Number.parseFloat(getComputedStyle(document.body).fontSize),
          };
        })()` });
        if (response.result?.value) return response.result.value;
      } catch {}
    }
    return null;
  });
  const liveTargets = await listTargets();
  const shell = await evaluate(`(() => ({
    agentTabsInIde: Array.from(document.querySelectorAll('.tabs-container .tab')).filter(item => (item.textContent || '').includes('Агенты Point')).length,
    editorWidth: Math.round(document.querySelector('.part.editor')?.getBoundingClientRect().width || 0),
    notifications: (document.querySelector('.notifications-toasts')?.innerText || '').trim(),
  }))()`);
  shell.pageTargets = liveTargets.filter(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://')).length;
  if (onboarding.layout !== 'wide' || onboarding.heading !== 'Point Agent Hub' || onboarding.steps !== 9 || !onboarding.hasStart || !onboarding.hasCompanion || !onboarding.hasMaster || !onboarding.hasPath || onboarding.horizontalOverflow > 1 || onboarding.width < 700 || onboarding.fontSize < 14 || shell.pageTargets < 2 || shell.agentTabsInIde !== 0 || shell.editorWidth < 700) {
    throw new Error(`Agent onboarding verification failed: ${JSON.stringify({onboarding,shell})}`);
  }
  if (output) {
    const agentsPage = liveTargets.find(candidate => candidate.type === 'page' && candidate.webSocketDebuggerUrl !== page.webSocketDebuggerUrl && !String(candidate.url).startsWith('devtools://'));
    const screenshot = await captureTarget(agentsPage);
    fs.writeFileSync(output, Buffer.from(screenshot.data, 'base64'));
  }
  agentSocket.close();
  process.stdout.write(JSON.stringify({ onboarding, shell, output:output || '' }));
} finally {
  socket.close();
}
