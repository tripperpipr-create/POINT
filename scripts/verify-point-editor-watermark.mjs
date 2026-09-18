import fs from 'node:fs';

const endpoint = process.argv[2];
const screenshotPath = process.argv[3];
if (!endpoint) throw new Error('Usage: node verify-point-editor-watermark.mjs <endpoint>');

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

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true }).then(result => result.result?.value);
const pressKey = async (key, code, windowsVirtualKeyCode, modifiers = 0) => {
  await command('Input.dispatchKeyEvent', { type: 'rawKeyDown', key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers });
  await command('Input.dispatchKeyEvent', { type: 'keyUp', key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers });
};

try {
  await Promise.all([command('Runtime.enable'), command('Input.setIgnoreInputEvents', { ignore: false })]);
  const startup = await evaluate(`(() => {
    const dialog = Array.from(document.querySelectorAll('.monaco-dialog-box')).find(item => (item.innerText ?? '').includes('Вы доверяете этому проекту?'));
    const target = Array.from(dialog?.querySelectorAll('.monaco-button') ?? []).find(button => (button.textContent ?? '').includes('Доверять проекту'));
    target?.click();
    return { found: Boolean(dialog), clicked: Boolean(target) };
  })()`);
  if (startup.found && !startup.clicked) throw new Error(`Point trust action was not found: ${JSON.stringify(startup)}`);
  if (startup.clicked) await delay(1200);

  const watermark = await evaluate(`(() => ({
    visible: Boolean(document.querySelector('.editor-group-watermark')?.getBoundingClientRect().height),
    entries: Array.from(document.querySelectorAll('.editor-group-watermark dt')).map(element => (element.textContent ?? '').replace(/\\s+/g, ' ').trim()),
    shortcuts: Array.from(document.querySelectorAll('.editor-group-watermark dd')).map(element => (element.textContent ?? '').replace(/\\s+/g, ' ').trim()),
  }))()`);
  if (!watermark.visible || !watermark.entries.includes('Открыть Гильдию') || watermark.entries.some(entry => /Открыть чат|Open Chat/i.test(entry))) {
    throw new Error(`Point editor watermark verification failed: ${JSON.stringify(watermark)}`);
  }
  if (screenshotPath) {
    await command('Page.enable');
    const screenshot = await command('Page.captureScreenshot', { format: 'png', fromSurface: true });
    fs.writeFileSync(screenshotPath, Buffer.from(screenshot.data, 'base64'));
  }

  await pressKey('i', 'KeyI', 73, 3);
  await delay(3500);
  const agent = await evaluate(`(() => ({
    selected: document.querySelector('.point-activitybar .action-item[aria-selected="true"] .action-label')?.getAttribute('aria-label') ?? null,
    webviews: document.querySelectorAll('webview, iframe.webview').length,
    notifications: (document.querySelector('.notifications-toasts')?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1600),
  }))()`);
  if (agent.selected !== 'Гильдия' || agent.webviews < 1 || /sign.?in|войти|требуется вход|Ошибка:\\s*Point/i.test(agent.notifications)) {
    throw new Error(`Point Agent shortcut verification failed: ${JSON.stringify(agent)}`);
  }
  process.stdout.write(JSON.stringify({ startup, watermark, agent }));
} finally {
  socket.close();
}
