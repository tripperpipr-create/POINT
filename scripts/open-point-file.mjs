import fs from 'node:fs';

const endpoint = process.argv[2];
const fileName = process.argv[3];
const screenshotPath = process.argv[4];
if (!endpoint || !fileName) throw new Error('Usage: node open-point-file.mjs <endpoint> <file-name>');

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

try {
  await command('Runtime.enable');
  const result = await command('Runtime.evaluate', {
    expression: `(() => {
      const name = ${JSON.stringify(fileName)};
      const label = Array.from(document.querySelectorAll('.explorer-viewlet .label-name, .explorer-viewlet .monaco-icon-label')).find(element => element.textContent?.trim() === name);
      const row = label?.closest('.monaco-list-row');
      if (!row) return null;
      const rect = row.getBoundingClientRect();
      return { x: rect.left + Math.min(rect.width - 8, Math.max(24, rect.width * 0.45)), y: rect.top + rect.height / 2 };
    })()`,
    returnByValue: true,
  });
  const point = result.result?.value;
  if (point) {
    // Point deliberately follows the IDE convention: one click selects a file,
    // a double click opens it in a permanent editor tab. Dispatch trusted CDP
    // mouse events so the smoke test exercises that exact user interaction.
    for (const clickCount of [1, 2]) {
      await command('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount });
      await command('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount });
      if (clickCount === 1) await new Promise(resolve => setTimeout(resolve, 75));
    }
  }
  await new Promise(resolve => setTimeout(resolve, 1200));
  const editorState = await command('Runtime.evaluate', {
    expression: `(() => {
      const activeTab = document.querySelector('.part.editor .tab.active');
      const breadcrumbs = document.querySelector('.part.editor .breadcrumbs-control');
      const editor = document.querySelector('.part.editor .monaco-editor');
      const minimap = document.querySelector('.part.editor .monaco-editor .minimap');
      const tabStyle = activeTab ? getComputedStyle(activeTab) : null;
      const breadcrumbStyle = breadcrumbs ? getComputedStyle(breadcrumbs) : null;
      const editorStyle = editor ? getComputedStyle(editor) : null;
      const minimapRect = minimap?.getBoundingClientRect();
      return {
        activeTab: (activeTab?.innerText ?? '').replace(/\\s+/g, ' ').trim(),
        tabCount: document.querySelectorAll('.part.editor .tab').length,
        tabHeight: activeTab ? Math.round(activeTab.getBoundingClientRect().height) : 0,
        tabRadius: tabStyle?.borderRadius ?? '',
        breadcrumbs: (breadcrumbs?.innerText ?? '').replace(/\\s+/g, ' ').trim(),
        breadcrumbsHeight: breadcrumbs ? Math.round(breadcrumbs.getBoundingClientRect().height) : 0,
        editorFontSize: editorStyle?.fontSize ?? '',
        minimapVisible: Boolean(minimap && minimapRect && minimapRect.width > 1 && getComputedStyle(minimap).display !== 'none'),
      };
    })()`,
    returnByValue: true,
  });
  const value = { clicked: Boolean(point), ...editorState.result?.value };
  if (!value.clicked || !value.activeTab.includes(fileName) || value.minimapVisible) {
    throw new Error(`Point editor file did not open: ${JSON.stringify(value)}`);
  }
  if (screenshotPath) {
    await command('Page.enable');
    const screenshot = await command('Page.captureScreenshot', { format: 'png', fromSurface: true });
    fs.writeFileSync(screenshotPath, Buffer.from(screenshot.data, 'base64'));
  }
  process.stdout.write(JSON.stringify(value));
} finally {
  socket.close();
}
