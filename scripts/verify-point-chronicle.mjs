import fs from 'node:fs';

const endpoint = process.argv[2];
const output = process.argv[3];
if (!endpoint) throw new Error('Usage: node verify-point-chronicle.mjs <endpoint> [output.png]');
const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
async function waitFor(description, probe, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try { const value = await probe(); if (value) return value; } catch {}
    await delay(100);
  }
  throw new Error(`Timed out waiting for ${description}`);
}
const targets = () => fetch(`${endpoint}/json/list`).then(response => response.json());
async function connect(target) {
  const socket = new WebSocket(target.webSocketDebuggerUrl);
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
  await Promise.all([command('Runtime.enable'), command('Page.enable')]);
  return { socket, command, evaluate: expression => command('Runtime.evaluate', { expression, returnByValue: true }).then(result => result.result?.value) };
}

const page = await waitFor('workbench target', async () => (await targets()).find(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://')));
const workbench = await connect(page);
let chronicle;
try {
  await waitFor('workbench DOM', () => workbench.evaluate(`Boolean(document.querySelector('.monaco-workbench'))`));
  await delay(1400);
  const trust = await workbench.evaluate(`(() => {
    const dialog = Array.from(document.querySelectorAll('.monaco-dialog-box')).find(item => (item.innerText || '').includes('Вы доверяете этому проекту?'));
    const button = Array.from(dialog?.querySelectorAll('.monaco-button') || []).find(item => (item.textContent || '').includes('Доверять проекту'));
    button?.click(); return { found: Boolean(dialog), clicked: Boolean(button) };
  })()`);
  if (trust.clicked) await delay(900);
  const existingAgentFrames = new Set((await targets())
    .filter(candidate => candidate.type === 'iframe' && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench'))
    .map(candidate => candidate.webSocketDebuggerUrl));
  await workbench.command('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: '9', code: 'Digit9', windowsVirtualKeyCode: 57, nativeVirtualKeyCode: 57, modifiers: 1 });
  await workbench.command('Input.dispatchKeyEvent', { type: 'keyUp', key: '9', code: 'Digit9', windowsVirtualKeyCode: 57, nativeVirtualKeyCode: 57, modifiers: 1 });
  const target = await waitFor('new Chronicle webview target', async () => (await targets()).find(candidate =>
    candidate.type === 'iframe'
      && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench')
      && !existingAgentFrames.has(candidate.webSocketDebuggerUrl)));
  chronicle = await connect(target);
  const summary = await waitFor('Chronicle data', async () => {
    const tree = await chronicle.command('Page.getFrameTree');
    const frames = [];
    const collect = node => { frames.push(node.frame); for (const child of node.childFrames || []) collect(child); };
    collect(tree.frameTree);
    for (const frame of frames.reverse()) {
      try {
        const world = await chronicle.command('Page.createIsolatedWorld', { frameId: frame.id, worldName: `point-chronicle-${Date.now()}` });
        const response = await chronicle.command('Runtime.evaluate', { contextId: world.executionContextId, returnByValue: true, expression: `(() => {
          const page = document.querySelector('.chronicle');
          if (!page) return null;
          const rect = page.getBoundingClientRect();
          return {
            heading: page.querySelector('h1')?.textContent?.trim(),
            branch: page.querySelector(':scope > header label strong')?.textContent?.trim(),
            commits: page.querySelectorAll('.commit-row').length,
            selected: page.querySelector('.commit-row.selected strong')?.textContent?.trim(),
            files: page.querySelectorAll('.changed-files button').length,
            diffLines: page.querySelectorAll('.diff-scroll pre span').length,
            localCopy: (page.innerText || '').includes('Летопись читается напрямую из локального Git'),
            width: Math.round(rect.width), height: Math.round(rect.height),
            horizontalOverflow: Math.max(0, page.scrollWidth - page.clientWidth),
            overflowers: Array.from(page.querySelectorAll('*')).map(element => {
              const box = element.getBoundingClientRect();
              return {
                selector: element.tagName.toLowerCase() + '.' + String(element.className || '').trim().replace(/\\s+/g, '.'),
                clientWidth: element.clientWidth,
                scrollWidth: element.scrollWidth,
                right: Math.round(box.right - rect.right),
              };
            }).filter(item => item.scrollWidth > item.clientWidth + 1 || item.right > 1)
              .sort((left, right) => Math.max(right.scrollWidth - right.clientWidth, right.right) - Math.max(left.scrollWidth - left.clientWidth, left.right))
              .slice(0, 8),
          };
        })()` });
        if (response.result?.value) return response.result.value;
      } catch {}
    }
    return null;
  });
  if (!summary.heading || summary.commits < 2 || !summary.selected || summary.files < 1 || summary.diffLines < 1 || !summary.localCopy || summary.horizontalOverflow > 1 || summary.width < 700) {
    throw new Error(`Chronicle verification failed: ${JSON.stringify(summary)}`);
  }
  // The webview DOM can become queryable a frame before Electron composites
  // its surface into the workbench screenshot, especially in software mode.
  await delay(900);
  const shell = await workbench.evaluate(`(() => ({
    tab: Array.from(document.querySelectorAll('.tabs-container .tab')).find(item => (item.textContent || '').includes('Летопись Point'))?.textContent?.trim() || null,
    notifications: (document.querySelector('.notifications-toasts')?.innerText || '').trim(),
  }))()`);
  if (output) {
    const screenshot = await workbench.command('Page.captureScreenshot', { format: 'png', fromSurface: true });
    fs.writeFileSync(output, Buffer.from(screenshot.data, 'base64'));
  }
  process.stdout.write(JSON.stringify({ trust, summary, shell, output: output || '' }));
} finally {
  chronicle?.socket.close();
  workbench.socket.close();
}
