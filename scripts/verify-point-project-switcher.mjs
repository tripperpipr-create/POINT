import fs from 'node:fs';

const endpoint = process.argv[2];
const screenshotPath = process.argv[3];
if (!endpoint) throw new Error('Usage: node verify-point-project-switcher.mjs <endpoint>');

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

try {
  await command('Runtime.enable');
  const clicked = await command('Runtime.evaluate', {
    expression: `(() => {
      const button = document.querySelector('.point-project-switcher');
      if (!button) return false;
      button.click();
      return true;
    })()`,
    returnByValue: true,
  });
  await delay(800);
  const result = await command('Runtime.evaluate', {
    expression: `(() => {
      const widget = document.querySelector('.quick-input-widget');
      const style = widget ? getComputedStyle(widget) : null;
      return {
        visible: Boolean(widget && style.display !== 'none' && style.visibility !== 'hidden'),
        title: (widget?.querySelector('.quick-input-title')?.textContent ?? '').trim(),
        placeholder: widget?.querySelector('input')?.getAttribute('placeholder') ?? '',
        rows: Array.from(widget?.querySelectorAll('.monaco-list-row') ?? []).map(row => ({
          text: (row.innerText ?? '').trim(),
          label: (row.querySelector('.label-name')?.textContent ?? '').trim(),
          description: (row.querySelector('.label-description')?.textContent ?? '').trim(),
          ariaLabel: row.getAttribute('aria-label') ?? '',
          className: row.className,
          html: row.outerHTML.slice(0, 1800),
        })),
        text: (widget?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1200),
      };
    })()`,
    returnByValue: true,
  });
  const value = result.result?.value ?? {};
  value.clicked = clicked.result?.value === true;
  const visibleText = value.rows?.map(row => row.text).join(' ') ?? '';
  const currentRows = value.rows?.filter(row => row.text.includes('Активный проект Point')) ?? [];
  const internalRows = value.rows?.filter(row => /agent-sessions(?:\.code-workspace)?/i.test(`${row.label} ${row.description} ${row.ariaLabel} ${row.text}`)) ?? [];
  const staleRows = (value.rows ?? []).slice(1).filter(row => {
    const parent = row.description.replace(/^Активный проект Point\s*/, '');
    // Workspace entries use a friendly label rather than a child filesystem
    // name, so only validate rows whose reconstructed folder path exists as a
    // meaningful candidate. Internal Point workspaces are checked separately.
    return /^[a-z]:\\/i.test(parent) && !/рабочая область/i.test(row.text) && !fs.existsSync(`${parent}\\${row.label}`);
  });
  if (!value.clicked
      || !value.visible
      || value.title !== 'Point — активный проект'
      || !value.placeholder.includes('Агенты, индекс и относительные пути')
      || !visibleText.includes('Выбрать папку проекта')
      || !visibleText.includes('Добавить проект в рабочую область')
      || value.rows?.length < 3
      || currentRows.length !== 1
      || internalRows.length
      || staleRows.length) {
    throw new Error(`Point project switcher verification failed: ${JSON.stringify(value)}`);
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
