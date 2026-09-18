const endpoint = process.argv[2];
if (!endpoint) throw new Error('Usage: node diagnose-cdp-page.mjs <endpoint>');

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
const events = [];
socket.addEventListener('message', event => {
  const message = JSON.parse(String(event.data));
  if (message.id && pending.has(message.id)) {
    const { resolve, reject } = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) reject(new Error(message.error.message)); else resolve(message.result);
    return;
  }
  if (message.method === 'Runtime.exceptionThrown' || message.method === 'Runtime.consoleAPICalled' || message.method === 'Log.entryAdded') {
    events.push({ method: message.method, params: message.params });
  }
});

function command(method, params = {}) {
  const id = ++sequence;
  socket.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
}

try {
  await Promise.all([command('Page.enable'), command('Runtime.enable'), command('Log.enable')]);
  await command('Page.reload', { ignoreCache: true });
  await new Promise(resolve => setTimeout(resolve, 5000));
  const diagnostics = await command('Runtime.evaluate', {
    expression: `(() => ({
      readyState: document.readyState,
      title: document.title,
      body: document.body?.innerHTML.slice(0, 4000) ?? null,
      scripts: Array.from(document.scripts).map(script => ({ src: script.src, type: script.type })),
      resources: performance.getEntriesByType('resource').map(entry => ({ name: entry.name, initiatorType: entry.initiatorType, duration: entry.duration })).slice(0, 100),
      bootstrap: typeof globalThis.MonacoBootstrap,
      workbench: Boolean(document.querySelector('.monaco-workbench')),
    }))()`,
    returnByValue: true,
  });
  process.stdout.write(JSON.stringify({ target: { title: target.title, url: target.url }, diagnostics: diagnostics.result?.value, events }, null, 2));
} finally {
  socket.close();
}
