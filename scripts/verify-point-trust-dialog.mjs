const endpoint = process.argv[2];
if (!endpoint) throw new Error('Usage: node verify-point-trust-dialog.mjs <endpoint>');
const action = process.argv[3];

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
      const dialog = document.querySelector('.monaco-dialog-box');
      const rect = dialog?.getBoundingClientRect();
      const buttons = Array.from(dialog?.querySelectorAll('.monaco-button') ?? []).map(button => ({
        text: (button.textContent ?? '').trim(),
        width: Math.round(button.getBoundingClientRect().width),
        height: Math.round(button.getBoundingClientRect().height),
      }));
      return {
        visible: Boolean(dialog && getComputedStyle(dialog).display !== 'none'),
        text: (dialog?.innerText ?? '').replace(/\\s+/g, ' ').trim(),
        width: rect ? Math.round(rect.width) : 0,
        height: rect ? Math.round(rect.height) : 0,
        buttons,
      };
    })()`,
    returnByValue: true,
  });
  const value = result.result?.value ?? {};
  if (!value.visible && action === 'accept-if-present') {
    value.skipped = true;
    process.stdout.write(JSON.stringify(value));
  } else {
    const requiredCopy = ['Вы доверяете этому проекту?', 'Point может запускать код, команды и инструменты этого проекта.', 'Доверять проекту', 'Открыть безопасно'];
  if (!value.visible || requiredCopy.some(copy => !value.text?.includes(copy)) || /VS Code|Visual Studio Code|Code - OSS/i.test(value.text ?? '') || value.buttons?.length !== 2 || value.buttons.some(button => button.height < 23)) {
    throw new Error(`Point trust dialog verification failed: ${JSON.stringify(value)}`);
  }
  if (action === 'accept' || action === 'accept-if-present') {
    const accepted = await command('Runtime.evaluate', {
      expression: `(() => {
        const dialogs = Array.from(document.querySelectorAll('.monaco-dialog-box'));
        const dialog = dialogs.find(item => (item.innerText ?? '').includes('Вы доверяете этому проекту?'));
        const button = Array.from(dialog?.querySelectorAll('.monaco-button') ?? []).find(item => (item.textContent ?? '').includes('Доверять проекту'));
        button?.click();
        return Boolean(button);
      })()`,
      returnByValue: true,
    });
    if (!accepted.result?.value) throw new Error('Point trust accept action was not found.');
    await new Promise(resolve => setTimeout(resolve, 1800));
    const finalState = await command('Runtime.evaluate', {
      expression: `(() => ({
        dialogVisible: Array.from(document.querySelectorAll('.monaco-dialog-box')).some(item => getComputedStyle(item).display !== 'none'),
        status: (document.querySelector('.part.statusbar')?.innerText ?? '').replace(/\\s+/g, ' ').trim(),
      }))()`,
      returnByValue: true,
    });
    if (finalState.result?.value?.dialogVisible || finalState.result?.value?.status?.includes('Ограниченный режим')) {
      throw new Error(`Point trust was not applied: ${JSON.stringify(finalState.result?.value)}`);
    }
    value.accepted = true;
    value.finalState = finalState.result?.value;
  } else if (action) {
    throw new Error(`Unknown trust dialog action: ${action}`);
  }
    process.stdout.write(JSON.stringify(value));
  }
} finally {
  socket.close();
}
