import fs from 'node:fs';

const endpoint = process.argv[2];
const files = process.argv.slice(3);
if (!endpoint || !files.length) throw new Error('Usage: node preview-point-css.mjs <endpoint> <css-file...>');

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
  const css = files.map(file => fs.readFileSync(file, 'utf8')).join('\n\n');
  const result = await command('Runtime.evaluate', {
    expression: `(() => {
      document.getElementById('point-css-preview')?.remove();
      const style = document.createElement('style');
      style.id = 'point-css-preview';
      style.textContent = ${JSON.stringify(css)};
      document.head.append(style);
      return { bytes: style.textContent.length };
    })()`,
    returnByValue: true,
  });
  process.stdout.write(JSON.stringify(result.result?.value ?? null));
} finally {
  socket.close();
}
