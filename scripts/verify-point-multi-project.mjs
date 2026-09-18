import fs from 'node:fs';

const endpoint = process.argv[2];
const screenshotPath = process.argv[3];
if (!endpoint) throw new Error('Usage: node verify-point-multi-project.mjs <endpoint> [screenshot]');

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
const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true });

async function keyStroke(key, code, windowsVirtualKeyCode, modifiers = 0) {
  const event = { key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers };
  await command('Input.dispatchKeyEvent', { type: 'rawKeyDown', ...event });
  await command('Input.dispatchKeyEvent', { type: 'keyUp', ...event });
}

try {
  await command('Runtime.enable');
  await command('Page.enable');
  // Invoke through the native command palette so the test also covers lazy
  // extension activation in a pristine profile. Ctrl+Alt+P is separately
  // guarded by the manifest smoke.
  await keyStroke('F1', 'F1', 112);
  await delay(250);
  await command('Input.insertText', { text: 'Переключить проект (мир)' });
  await delay(450);
  const launch = await evaluate(`(() => {
    const row = Array.from(document.querySelectorAll('.quick-input-widget .monaco-list-row'))
      .find(item => (item.innerText ?? '').includes('Переключить проект (мир)'));
    row?.click();
    return { clicked: Boolean(row) };
  })()`);
  if (!launch.result?.value?.clicked) throw new Error('Point project command was not found in the command palette.');
  await delay(4000);

  const pickerResult = await evaluate(`(() => {
    const widgets = Array.from(document.querySelectorAll('.quick-input-widget'));
    const widget = widgets.find(item => {
      const style = getComputedStyle(item);
      return style.display !== 'none' && style.visibility !== 'hidden' && item.getBoundingClientRect().width > 0;
    });
    const style = widget ? getComputedStyle(widget) : null;
    return {
      visible: Boolean(widget && style.display !== 'none' && style.visibility !== 'hidden'),
      title: (widget?.querySelector('.quick-input-title')?.textContent ?? '').trim(),
      placeholder: widget?.querySelector('input')?.getAttribute('placeholder') ?? '',
      rows: Array.from(widget?.querySelectorAll('.monaco-list-row') ?? []).map(row => (row.innerText ?? '').replace(/\\s+/g, ' ').trim()),
      notifications: (document.querySelector('.notifications-toasts')?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1200),
      status: Array.from(document.querySelectorAll('.part.statusbar .statusbar-item')).map(item => (item.innerText ?? '').replace(/\\s+/g, ' ').trim()).filter(Boolean),
    };
  })()`);
  const picker = pickerResult.result?.value || {};
  const text = (picker.rows || []).join(' ');
  if (!picker.visible
    || picker.title !== 'Point — активный проект'
    || !picker.placeholder.includes('Агенты, индекс и относительные пути')
    || !text.includes('go-health')
    || !text.includes('frontend')
    || !text.includes('Выбрать папку проекта')
    || !text.includes('Добавить проект')) {
    throw new Error(`Point active-project picker is incomplete: ${JSON.stringify(picker)}`);
  }

  await command('Input.insertText', { text: 'frontend' });
  await delay(250);
  await keyStroke('Enter', 'Enter', 13);
  await delay(900);

  const statusResult = await evaluate(`(() => Array.from(document.querySelectorAll('.part.statusbar .statusbar-item')).map(item => ({
    id: item.id || '', text: (item.innerText ?? '').replace(/\\s+/g, ' ').trim(), title: item.getAttribute('aria-label') || item.getAttribute('title') || ''
  })).filter(item => /frontend|Активный проект Point/i.test(item.text + ' ' + item.title)))()`);
  const statuses = statusResult.result?.value || [];
  if (!statuses.some(item => item.text.includes('frontend') && item.text.includes('2'))) {
    throw new Error(`Point status did not switch to the second root: ${JSON.stringify(statuses)}`);
  }

  if (screenshotPath) {
    const screenshot = await command('Page.captureScreenshot', { format: 'png', fromSurface: true });
    fs.writeFileSync(screenshotPath, Buffer.from(screenshot.data, 'base64'));
  }
  process.stdout.write(JSON.stringify({ picker, active: 'frontend', status: statuses[0] || {}, coreStarted: false }));
} finally {
  socket.close();
}
