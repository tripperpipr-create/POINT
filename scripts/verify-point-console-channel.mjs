import fs from 'node:fs';

const endpoint = process.argv[2];
const output = process.argv[3];
if (!endpoint) throw new Error('Usage: node verify-point-console-channel.mjs <endpoint> [output.png]');
const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
async function waitFor(description, probe, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try { const value = await probe(); if (value) return value; } catch {}
    await delay(100);
  }
  throw new Error(`Timed out waiting for ${description}`);
}
const target = await waitFor('workbench target', async () => {
  const list = await fetch(`${endpoint}/json/list`).then(response => response.json());
  return list.find(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://'));
});
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
const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true }).then(result => result.result?.value);

try {
  await Promise.all([command('Runtime.enable'), command('Page.enable')]);
  await waitFor('workbench DOM', () => evaluate(`Boolean(document.querySelector('.monaco-workbench'))`));
  await delay(1400);
  const trust = await evaluate(`(() => {
    const dialog = Array.from(document.querySelectorAll('.monaco-dialog-box')).find(item => (item.innerText || '').includes('Вы доверяете этому проекту?'));
    const button = Array.from(dialog?.querySelectorAll('.monaco-button') || []).find(item => (item.textContent || '').includes('Доверять проекту'));
    button?.click(); return { found: Boolean(dialog), clicked: Boolean(button) };
  })()`);
  if (trust.clicked) await delay(900);
  await command('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'F12', code: 'F12', windowsVirtualKeyCode: 123, nativeVirtualKeyCode: 123, modifiers: 1 });
  await command('Input.dispatchKeyEvent', { type: 'keyUp', key: 'F12', code: 'F12', windowsVirtualKeyCode: 123, nativeVirtualKeyCode: 123, modifiers: 1 });
  // The first command intentionally starts the otherwise-idle extension host.
  // On a cold Windows machine antivirus can make that process creation much
  // slower than subsequent commands, so keep the product lazy and give this
  // one cold-start assertion a separate budget.
  let consoleChannel;
  try {
    consoleChannel = await waitFor('Point console channel', () => evaluate(`(() => {
    const tabs = Array.from(document.querySelectorAll('.tabs-container .tab'));
    const tab = tabs.find(item => (item.textContent || '').includes('Point · Квест'));
    const terminal = document.querySelector('.editor-instance .terminal-wrapper, .editor-instance .terminal-overflow-guard, .terminal-editor');
    if (!tab || !terminal) return null;
    const rect = terminal.getBoundingClientRect();
    return {
      tab: tab.textContent?.replace(/\\s+/g, ' ').trim(),
      active: tab.classList.contains('active'),
      terminalWidth: Math.round(rect.width), terminalHeight: Math.round(rect.height),
      terminalVisible: getComputedStyle(terminal).display !== 'none' && rect.width > 300 && rect.height > 200,
      canvases: Array.from(terminal.querySelectorAll('canvas')).map(canvas => ({ width: canvas.width, height: canvas.height })),
      xtermReady: Boolean(terminal.querySelector('.xterm-screen canvas, .xterm-viewport')),
      promptText: (terminal.innerText || '').replace(/\\s+/g, ' ').trim().slice(-500),
      panelVisible: (() => { const panel=document.querySelector('.part.panel'); const panelRect=panel?.getBoundingClientRect(); return Boolean(panelRect && panelRect.height > 60 && getComputedStyle(panel).display !== 'none') })(),
      notifications: (document.querySelector('.notifications-toasts')?.innerText || '').trim(),
    };
    })()`), 60000);
  } catch (error) {
    const diagnostic = await evaluate(`(() => ({
      tabs: Array.from(document.querySelectorAll('.tabs-container .tab')).map(item => (item.textContent || '').trim()),
      editorTerminals: document.querySelectorAll('.editor-instance .terminal-wrapper, .editor-instance .terminal-overflow-guard, .terminal-editor').length,
      panelTerminals: document.querySelectorAll('.part.panel .terminal-wrapper, .part.panel .terminal-instance').length,
      notifications: (document.querySelector('.notifications-toasts')?.innerText || '').trim(),
      dialogs: Array.from(document.querySelectorAll('.monaco-dialog-box')).map(item => (item.innerText || '').trim()),
    }))()`);
    throw new Error(`${error instanceof Error ? error.message : String(error)}; DOM=${JSON.stringify(diagnostic)}`);
  }
  if (!consoleChannel.active || !consoleChannel.terminalVisible || !consoleChannel.xtermReady || consoleChannel.panelVisible || /error|ошибка/i.test(consoleChannel.notifications)) {
    throw new Error(`Console channel verification failed: ${JSON.stringify(consoleChannel)}`);
  }
  await delay(3500);
  if (output) {
    const screenshot = await command('Page.captureScreenshot', { format: 'png', fromSurface: true });
    fs.writeFileSync(output, Buffer.from(screenshot.data, 'base64'));
  }
  process.stdout.write(JSON.stringify({ trust, consoleChannel, output: output || '' }));
} finally {
  socket.close();
}
