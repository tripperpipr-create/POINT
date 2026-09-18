import assert from 'node:assert/strict';
import fs from 'node:fs';

const endpoint = process.argv[2] || 'http://127.0.0.1:9448';
const pages = await fetch(`${endpoint}/json/list`).then(r => r.json());
const page = pages.find(item => item.type === 'page' && !item.url.startsWith('devtools://'));
assert.ok(page, 'Point renderer is available');
const socket = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
let sequence = 0;
const pending = new Map();
socket.onmessage = event => {
  const message = JSON.parse(String(event.data));
  const handler = pending.get(message.id);
  if (!handler) return;
  pending.delete(message.id);
  message.error ? handler.reject(new Error(message.error.message)) : handler.resolve(message.result);
};
const command = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++sequence;
  pending.set(id, { resolve, reject });
  socket.send(JSON.stringify({ id, method, params }));
});
const evaluate = async expression => {
  const result = await command('Runtime.evaluate', { expression, returnByValue: true });
  assert.ok(!result.exceptionDetails, JSON.stringify(result.exceptionDetails));
  return result.result.value;
};
try {
  const metrics = [];
  for (const width of [1440, 1000, 800]) {
    await command('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: false });
    await new Promise(resolve => setTimeout(resolve, 500));
    const state = await evaluate(`(() => {
      const box = selector => {
        const element = document.querySelector(selector);
        if (!element) return null;
        const b = element.getBoundingClientRect();
        return { x: b.x, right: b.right, width: b.width, height: b.height };
      };
      return { width: innerWidth, project: box('.point-project-switcher'), search: box('.command-center'), run: box('.point-run-actions') };
    })()`);
    assert.ok(state.project.width > 0, 'project is visible');
    assert.ok(state.search.width >= 100, 'search remains usable');
    assert.ok(state.project.right <= state.search.x + 1, 'project and search do not overlap');
    assert.ok(state.search.right <= state.run.x + 1, 'search and run controls do not overlap');
    metrics.push(state);
  }
  console.log(JSON.stringify(metrics, null, 2));
  if (process.argv[3]) {
    const shot = await command('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(process.argv[3], Buffer.from(shot.data, 'base64'));
  }
} finally {
  await command('Emulation.clearDeviceMetricsOverride');
  socket.close();
}
