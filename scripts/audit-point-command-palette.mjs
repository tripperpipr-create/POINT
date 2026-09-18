import fs from 'node:fs';

const endpoint = process.argv[2];
const screenshotPath = process.argv[3];
if (!endpoint) throw new Error('Usage: node audit-point-command-palette.mjs <endpoint>');

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
  if (startup.clicked) await delay(900);

  const queries = ['чат', 'copilot', 'войти', 'sign in', 'vs code', 'visual studio', 'аккаунт', 'account'];
  const results = {};
  for (const query of queries) {
    await pressKey('F1', 'F1', 112);
    await delay(350);
    await command('Input.insertText', { text: query });
    await delay(700);
    results[query] = await evaluate(`(() => ({
      open: Boolean(document.querySelector('.quick-input-widget')),
      rows: Array.from(document.querySelectorAll('.quick-input-widget .monaco-list-row')).map(row => (row.innerText ?? '').replace(/\\s+/g, ' ').trim()).filter(Boolean).slice(0, 30),
      message: (document.querySelector('.quick-input-message')?.textContent ?? '').replace(/\\s+/g, ' ').trim(),
    }))()`);
    await pressKey('Escape', 'Escape', 27);
    await delay(250);
  }
  const forbiddenCommandPatterns = [
    /Copilot/i,
    /Manage Accounts/i,
    /Manage Trusted Extensions For Account/i,
    /Manage Extension Account Preferences/i,
    /Sync Account Policy/i,
    /Sign In/i,
    /Войти/i,
    /\bVS Code\b/i,
    /Visual Studio Code/i,
  ];
  const violations = Object.entries(results).flatMap(([query, result]) =>
    result.rows
      .filter(row => forbiddenCommandPatterns.some(pattern => pattern.test(row)))
      .map(row => ({ query, row })),
  );
  if (violations.length) {
    throw new Error(`Forbidden upstream commands are visible: ${JSON.stringify(violations)}`);
  }

  const pointQueries = ['point', 'быстрые действия', 'главная', 'агента', 'переключить проект', 'локальный сервис', 'обучение'];
  const pointCommands = {};
  for (const query of pointQueries) {
    await pressKey('F1', 'F1', 112);
    await delay(350);
    await command('Input.insertText', { text: query });
    await delay(700);
    pointCommands[query] = await evaluate(`(() => ({
      rows: Array.from(document.querySelectorAll('.quick-input-widget .monaco-list-row')).map(row => (row.innerText ?? '').replace(/\\s+/g, ' ').trim()).filter(Boolean).slice(0, 30),
    }))()`);
    await pressKey('Escape', 'Escape', 27);
    await delay(250);
  }
  const ownCommandRows = Object.values(pointCommands).flatMap(result => result.rows).filter(row => /Point\s*[—:-]/i.test(row));
  if (!ownCommandRows.length) {
    throw new Error(`Point commands are not discoverable: ${JSON.stringify(pointCommands)}`);
  }
  if (!pointCommands['быстрые действия'].rows.some(row => /Point\s*[—:-]\s*Быстрые действия/i.test(row))) {
    throw new Error(`Point quick actions are not discoverable: ${JSON.stringify(pointCommands['быстрые действия'])}`);
  }

  await pressKey('F1', 'F1', 112);
  await delay(350);
  await command('Input.insertText', { text: 'быстрые действия' });
  await delay(700);
  const quickActionLaunch = await evaluate(`(() => {
    const row = Array.from(document.querySelectorAll('.quick-input-widget .monaco-list-row')).find(item => /Point\\s*[—:-]\\s*Быстрые действия/i.test((item.innerText ?? '').trim()));
    row?.click();
    return { clicked: Boolean(row) };
  })()`);
  if (!quickActionLaunch.clicked) {
    throw new Error(`Point quick actions could not be launched: ${JSON.stringify(quickActionLaunch)}`);
  }
  await delay(1200);
  const quickActions = await evaluate(`(() => ({
    rows: Array.from(document.querySelectorAll('.quick-input-widget .monaco-list-row')).map(row => (row.innerText ?? '').replace(/\\s+/g, ' ').trim()).filter(Boolean),
    placeholder: document.querySelector('.quick-input-widget input')?.getAttribute('placeholder') ?? '',
  }))()`);
  const expectedQuickActions = ['Переключить проект', 'Открыть Гильдию', 'Главная Point', 'Консоль проекта'];
  if (!expectedQuickActions.every(label => quickActions.rows.some(row => row.includes(label)))) {
    throw new Error(`Point quick actions are incomplete: ${JSON.stringify(quickActions)}`);
  }
  if (screenshotPath) {
    await command('Page.enable');
    const screenshot = await command('Page.captureScreenshot', { format: 'png', fromSurface: true });
    fs.writeFileSync(screenshotPath, Buffer.from(screenshot.data, 'base64'));
  }

  process.stdout.write(JSON.stringify({ startup, results, violations, pointCommands, quickActions }));
} finally {
  socket.close();
}
