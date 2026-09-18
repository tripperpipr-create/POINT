import fs from 'node:fs';

const endpoint = process.argv[2];
const output = process.argv[3] || '';
if (!endpoint) throw new Error('Usage: node diagnose-point-auxiliary.mjs <endpoint> [screenshot.png]');
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const list = () => fetch(`${endpoint}/json/list`).then(response => response.json());
const target = (await list()).find(item => item.type === 'page' && String(item.url).includes('/workbench/'));
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
  const item = pending.get(message.id);
  pending.delete(message.id);
  if (message.error) item.reject(new Error(message.error.message)); else item.resolve(message.result);
});
const command = (method, params = {}) => {
  const id = ++sequence;
  socket.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
};
const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true }).then(value => value.result?.value);

try {
  await Promise.all([command('Page.enable'), command('Runtime.enable')]);
  if (!process.argv.includes('--no-toggle')) {
    await command('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: ';', code: 'Semicolon', windowsVirtualKeyCode: 186, nativeVirtualKeyCode: 186, modifiers: 3 });
    await command('Input.dispatchKeyEvent', { type: 'keyUp', key: ';', code: 'Semicolon', windowsVirtualKeyCode: 186, nativeVirtualKeyCode: 186, modifiers: 3 });
  }
  await delay(5000);
  const state = await evaluate(`(() => {
    const rect = element => { const value = element?.getBoundingClientRect(); return value ? { x: Math.round(value.x), y: Math.round(value.y), width: Math.round(value.width), height: Math.round(value.height) } : null; };
    const aux = document.querySelector('.part.auxiliarybar');
    return {
      title: document.title,
      auxiliary: {
        className: aux?.className || '', rect: rect(aux), display: aux ? getComputedStyle(aux).display : '',
        style: aux?.getAttribute('style') || '',
        computed: aux ? {
          position: getComputedStyle(aux).position,
          top: getComputedStyle(aux).top,
          bottom: getComputedStyle(aux).bottom,
          height: getComputedStyle(aux).height,
          minHeight: getComputedStyle(aux).minHeight,
          overflow: getComputedStyle(aux).overflow,
        } : null,
        parent: aux?.parentElement ? {
          className: aux.parentElement.className || '',
          rect: rect(aux.parentElement),
          style: aux.parentElement.getAttribute('style') || '',
        } : null,
        siblings: Array.from(aux?.parentElement?.children || []).map(item => ({
          className: item.className || '',
          rect: rect(item),
          style: item.getAttribute('style') || '',
        })),
        titleRect: rect(aux?.querySelector(':scope > .title')), contentRect: rect(aux?.querySelector(':scope > .content')),
        actions: Array.from(aux?.querySelectorAll('.composite-bar .action-item') || []).map(item => ({
          rect: rect(item), classes: item.className, text: (item.textContent || '').replace(/\\s+/g, ' ').trim(),
          aria: item.querySelector('.action-label')?.getAttribute('aria-label') || '', title: item.querySelector('.action-label')?.getAttribute('title') || '',
        })),
      },
      titlebar: {
        rect: rect(document.querySelector('.part.titlebar')),
        project: (document.querySelector('.point-project-switcher')?.textContent || '').trim(),
        pointActions: Array.from(document.querySelectorAll('.point-title-action')).map(item => ({ title: item.title, rect: rect(item) })),
        menuDisplay: getComputedStyle(document.querySelector('.part.titlebar .menubar') || document.body).display,
      },
      explorerRows: Array.from(document.querySelectorAll('.explorer-folders-view .monaco-list-row')).slice(0, 5).map(rect),
      status: Array.from(document.querySelectorAll('.statusbar-item')).map(item => (item.innerText || '').trim()).filter(Boolean),
    };
  })()`);
  const targets = (await list()).map(item => ({ type: item.type, title: item.title, url: String(item.url).slice(0, 320) }));
  if (output) {
    const shot = await command('Page.captureScreenshot', { format: 'png', fromSurface: true });
    fs.writeFileSync(output, Buffer.from(shot.data, 'base64'));
  }
  process.stdout.write(JSON.stringify({ state, targets, screenshot: output }, null, 2));
} finally {
  socket.close();
}
