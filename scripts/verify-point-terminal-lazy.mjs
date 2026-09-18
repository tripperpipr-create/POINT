import fs from 'node:fs';

const endpoint = process.argv[2];
const screenshotPath = process.argv[3];
if (!endpoint) throw new Error('Usage: node verify-point-terminal-lazy.mjs <endpoint> [screenshot-path]');

const targets = await fetch(`${endpoint}/json/list`).then(response => response.json());
const target = targets.find(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://'));
if (!target?.webSocketDebuggerUrl) throw new Error('Workbench renderer target was not found.');

const socket = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((resolve, reject) => {
  socket.addEventListener('open', resolve, { once: true });
  socket.addEventListener('error', reject, { once: true });
});

let sequence = 0;
const pending = new Map();
socket.addEventListener('message', event => {
  const message = JSON.parse(String(event.data));
  if (!message.id || !pending.has(message.id)) return;
  const handlers = pending.get(message.id);
  pending.delete(message.id);
  if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
});

function command(method, params = {}) {
  const id = ++sequence;
  socket.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
}

async function readTerminalState() {
  const result = await command('Runtime.evaluate', {
    expression: `(() => {
      const panel = document.querySelector('.part.panel');
      const rect = panel?.getBoundingClientRect();
      const terminalInstances = document.querySelectorAll('.part.panel .terminal-wrapper, .part.panel .terminal-instance');
      return {
        panelHeight: rect ? Math.round(rect.height) : 0,
        panelVisible: Boolean(rect && rect.height > 40 && getComputedStyle(panel).display !== 'none'),
        terminalInstances: terminalInstances.length,
        panelText: (panel?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 500),
      };
    })()`,
    returnByValue: true,
  });
  return result.result?.value ?? {};
}

async function selectPanelTab(label) {
  const result = await command('Runtime.evaluate', {
    expression: `(() => {
      const expected = ${JSON.stringify(label)}.toLocaleLowerCase('ru');
      const action = Array.from(document.querySelectorAll('.part.panel > .title .composite-bar .action-item'))
        .find(element => (element.textContent ?? '').trim().toLocaleLowerCase('ru') === expected);
      action?.click();
      return Boolean(action);
    })()`,
    returnByValue: true,
  });
  if (result.result?.value !== true) throw new Error(`Point panel tab was not found: ${label}`);
  await new Promise(resolve => setTimeout(resolve, 500));
  const active = await command('Runtime.evaluate', {
    expression: `(() => (document.querySelector('.part.panel > .title .composite-bar .action-item.checked')?.textContent ?? '').replace(/\\s+/g, ' ').trim())()`,
    returnByValue: true,
  });
  return active.result?.value ?? '';
}

try {
  await command('Runtime.enable');
  const before = await readTerminalState();
  await command('Input.dispatchKeyEvent', {
    type: 'rawKeyDown', key: '`', code: 'Backquote', windowsVirtualKeyCode: 192, nativeVirtualKeyCode: 192, modifiers: 2,
  });
  await command('Input.dispatchKeyEvent', {
    type: 'keyUp', key: '`', code: 'Backquote', windowsVirtualKeyCode: 192, nativeVirtualKeyCode: 192, modifiers: 2,
  });
  await new Promise(resolve => setTimeout(resolve, 3500));
  const after = await readTerminalState();
  if (!after.panelVisible || after.terminalInstances < 1) {
    throw new Error(`Point terminal did not open: ${JSON.stringify({ before, after })}`);
  }
  const upstreamChatHint = /(?:Open chat|Откройте чат|Start typing to dismiss|Начните вводить текст, чтобы отклонить)/iu;
  if (upstreamChatHint.test(after.panelText ?? '')) {
    throw new Error(`Point terminal exposed the upstream chat hint: ${JSON.stringify(after.panelText)}`);
  }
  const problemsActive = await selectPanelTab('Проблемы');
  const terminalActive = await selectPanelTab('Терминал');
  if (!problemsActive.toLocaleLowerCase('ru').includes('проблемы') || !terminalActive.toLocaleLowerCase('ru').includes('терминал')) {
    throw new Error(`Point panel tabs did not switch: ${JSON.stringify({ problemsActive, terminalActive })}`);
  }
  if (screenshotPath) {
    await command('Page.enable');
    const screenshot = await command('Page.captureScreenshot', { format: 'png', fromSurface: true });
    fs.writeFileSync(screenshotPath, Buffer.from(screenshot.data, 'base64'));
  }
  process.stdout.write(JSON.stringify({ before, after, panelTabs: { problemsActive, terminalActive } }));
} finally {
  socket.close();
}
