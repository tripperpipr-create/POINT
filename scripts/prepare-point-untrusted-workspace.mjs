const endpoint = process.argv[2];
if (!endpoint) throw new Error('Usage: node prepare-point-untrusted-workspace.mjs <endpoint>');

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
const pressKey = async (key, code, windowsVirtualKeyCode, modifiers = 0) => {
  await command('Input.dispatchKeyEvent', { type: 'rawKeyDown', key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers });
  await command('Input.dispatchKeyEvent', { type: 'keyUp', key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers });
};

try {
  await Promise.all([command('Runtime.enable'), command('Input.setIgnoreInputEvents', { ignore: false })]);
  const startupDialog = await command('Runtime.evaluate', {
    expression: `(() => {
      const dialogs = Array.from(document.querySelectorAll('.monaco-dialog-box'));
      const dialog = dialogs.find(item => (item.innerText ?? '').includes('Вы доверяете этому проекту?'));
      const buttons = Array.from(dialog?.querySelectorAll('.monaco-button') ?? []);
      const target = buttons.find(button => (button.textContent ?? '').includes('Доверять проекту'));
      target?.click();
      return { found: Boolean(dialog), clicked: Boolean(target) };
    })()`,
    returnByValue: true,
  });
  if (startupDialog.result?.value?.clicked) await delay(900);

  await pressKey('F1', 'F1', 112);
  await delay(400);
  await command('Input.insertText', { text: 'Безопасность проекта' });
  await delay(700);
  const commandChoice = await command('Runtime.evaluate', {
    expression: `(() => {
      const rows = Array.from(document.querySelectorAll('.quick-input-widget .monaco-list-row'));
      const target = rows.find(row => {
        const text = (row.innerText ?? '').replace(/\\s+/g, ' ').trim();
        return text.includes('Безопасность проекта') && !text.includes('Настройки безопасности проектов');
      });
      if (target) {
        target.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 }));
        target.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, button: 0 }));
        target.click();
      }
      return { clicked: Boolean(target), rows: rows.map(row => (row.innerText ?? '').replace(/\\s+/g, ' ').trim()).slice(0, 12) };
    })()`,
    returnByValue: true,
  });
  if (!commandChoice.result?.value?.clicked) throw new Error(`Project Security command was not found: ${JSON.stringify(commandChoice.result?.value)}`);
  await delay(1400);

  const before = await command('Runtime.evaluate', {
    expression: `(() => {
      const editor = document.querySelector('.workspace-trust-editor');
      if (editor) editor.focus();
      return { found: Boolean(editor), pointSurface: editor?.classList.contains('point-workspace-trust') ?? false, trusted: editor?.classList.contains('trusted') ?? null, text: (editor?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1600) };
    })()`,
    returnByValue: true,
  });
  if (!before.result?.value?.found) throw new Error(`Workspace Trust editor did not open: ${JSON.stringify(before.result?.value)}`);

  if (before.result.value.trusted) {
    await pressKey('Enter', 'Enter', 13, 2);
    await delay(1000);
  }

  const after = await command('Runtime.evaluate', {
    expression: `(() => {
      const editor = document.querySelector('.workspace-trust-editor');
      return { found: Boolean(editor), pointSurface: editor?.classList.contains('point-workspace-trust') ?? false, trusted: editor?.classList.contains('trusted') ?? null, text: (editor?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1600) };
    })()`,
    returnByValue: true,
  });
  if (after.result?.value?.trusted !== false) throw new Error(`Workspace did not enter restricted mode: ${JSON.stringify(after.result?.value)}`);
  const text = after.result.value.text ?? '';
  const requiredCopy = ['Проект открыт безопасно', 'Полный режим', 'Безопасный режим', 'Доверять проекту', 'Доверенные проекты и папки'];
  if (!after.result.value.pointSurface || requiredCopy.some(copy => !text.includes(copy)) || /VS Code|Visual Studio Code|Workspace Trust|learn more/i.test(text)) {
    throw new Error(`Point project security surface verification failed: ${JSON.stringify(after.result.value)}`);
  }
  process.stdout.write(JSON.stringify({ before: before.result.value, after: after.result.value }));
} finally {
  socket.close();
}
