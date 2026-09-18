const endpoint = process.argv[2];
if (!endpoint) throw new Error('Usage: node prepare-point-trusted-agent.mjs <endpoint>');

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
async function waitFor(description, probe, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      const value = await probe();
      if (value) return value;
    } catch {}
    await delay(120);
  }
  throw new Error(`Timed out waiting for ${description}`);
}
const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true }).then(result => result.result?.value);
const sendKey = async ({ key, code, windowsVirtualKeyCode, modifiers = 0 }) => {
  await command('Input.dispatchKeyEvent', { type: 'rawKeyDown', key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers });
  await command('Input.dispatchKeyEvent', { type: 'keyUp', key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers });
};

try {
  await command('Runtime.enable');
  const startup = await evaluate(`(() => {
    const dialogs = Array.from(document.querySelectorAll('.monaco-dialog-box'));
    const dialog = dialogs.find(item => (item.innerText ?? '').includes('Вы доверяете этому проекту?'));
    const buttons = Array.from(dialog?.querySelectorAll('.monaco-button') ?? []);
    const target = buttons.find(button => (button.textContent ?? '').includes('Доверять проекту'));
    target?.click();
    return { found: Boolean(dialog), clicked: Boolean(target) };
  })()`);
  if (startup.found && !startup.clicked) throw new Error(`Point trust action was not found: ${JSON.stringify(startup)}`);
  // Trust acceptance restarts the extension host. Wait for contributed Point
  // keybindings to be registered again before opening the Hub.
  if (startup.clicked) await delay(3200);

  // The IDE companion owns the activity bar. The full Agent Hub is an editor
  // page and is opened explicitly, so the two surfaces never replace each
  // other based on activity-item ordering.
  const existingAgentFrames = new Set((await fetch(`${endpoint}/json/list`).then(response => response.json()))
    .filter(candidate => candidate.type === 'iframe' && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench'))
    .map(candidate => candidate.webSocketDebuggerUrl));
  // Одного нажатия F1 мало: если окно ещё не в фокусе, палитра не открывается
  // вовсе, и ожидание упирается в тайм-аут «command palette» — до того, как
  // проверка посмотрит хоть на что-то. Тот же приём, что в
  // verify-point-agent-onboarding.mjs: вывести окно вперёд, вернуть фокус и
  // повторить нажатие.
  let paletteReady = false;
  for (let attempt = 0; attempt < 4 && !paletteReady; attempt += 1) {
    await command('Page.bringToFront');
    await evaluate(`(() => { document.querySelector('.monaco-workbench')?.focus(); document.body.focus(); return true; })()`);
    await sendKey({ key: 'F1', code: 'F1', windowsVirtualKeyCode: 112 });
    await delay(500);
    paletteReady = await evaluate(`(() => { const input = document.querySelector('.quick-input-widget input'); input?.focus(); return Boolean(input); })()`);
  }
  if (!paletteReady) throw new Error('Command palette did not open after four focused attempts');
  // Текст запроса тоже доезжает не всегда: вставка может опередить фокус в
  // поле, и палитра остаётся с полным списком команд. Проверяем не время, а
  // содержимое поля, и при промахе набираем заново.
  const paletteState = `(() => {
    const input = document.querySelector('.quick-input-widget input');
    const rows = Array.from(document.querySelectorAll('.quick-input-widget .monaco-list-row'))
      .map(row => (row.innerText || '').replace(/\\s+/g, ' ').trim());
    return { value: input ? input.value : '', rows };
  })()`;
  await waitFor('Point Agent Hub command', async () => {
    const state = await evaluate(paletteState);
    if (!String(state.value).includes('доску квестов')) {
      // Настоящий щелчок по полю, а не element.focus(): Input.insertText уходит
      // туда, где фокус на уровне браузера, и при фокусе в другом кадре — а Хаб
      // это отдельный вебвью — текст не попадал никуда. Оставалось поле с одним
      // «>» и полный список команд.
      const box = await evaluate(`(() => {
        const input = document.querySelector('.quick-input-widget input');
        if (!input) return null;
        const rect = input.getBoundingClientRect();
        return { x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) };
      })()`);
      if (box) {
        for (const type of ['mousePressed', 'mouseReleased']) {
          await command('Input.dispatchMouseEvent', { type, x: box.x, y: box.y, button: 'left', clickCount: 1 });
        }
      }
      await command('Input.insertText', { text: 'Открыть доску квестов' });
      return false;
    }
    return state.rows.some(label => label.includes('Открыть доску квестов'));
  }, 20000).catch(async () => {
    const state = await evaluate(paletteState);
    throw new Error(`Point Agent Hub command was not found: ${JSON.stringify(state)}`);
  });
  // Кликаем отдельно, уже зная, что строка есть: клик внутри пробы повторялся
  // бы на каждой неудачной итерации.
  await evaluate(`(() => {
    const rows = Array.from(document.querySelectorAll('.quick-input-widget .monaco-list-row'));
    rows.find(row => (row.innerText || '').includes('Открыть доску квестов'))?.click();
    return true;
  })()`);
  const hubTarget = await waitFor('separate Agent Hub webview', async () => {
    const liveTargets = await fetch(`${endpoint}/json/list`).then(response => response.json());
    return liveTargets.find(candidate =>
      candidate.type === 'iframe'
        && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench')
        && !existingAgentFrames.has(candidate.webSocketDebuggerUrl));
  });
  // Отдельная страница редактора появляется в списке целей не одновременно с
  // кадром вебвью: раньше список читался один раз сразу после кадра, и на
  // медленном запуске проверка падала «pageTargets: 1» — тоже до единой
  // проверки экрана. Ждём вторую страницу, а не надеемся на порядок.
  const liveTargets = await waitFor('separate Agent Hub editor page', async () => {
    const targets = await fetch(`${endpoint}/json/list`).then(response => response.json());
    const pages = targets.filter(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://'));
    return pages.length >= 2 ? targets : null;
  }, 20000);
  const notifications = await evaluate(`(document.querySelector('.notifications-toasts')?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1600)`);
  const result = {
    pageTargets: liveTargets.filter(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://')).length,
    hubTarget: String(hubTarget.url || ''),
    notifications,
  };
  if (result.pageTargets < 2 || /Сначала подтвердите доверие|Ошибка:\\s*Point/i.test(result.notifications)) {
    throw new Error(`Trusted separate Point Agent Hub did not open cleanly: ${JSON.stringify({ startup, result })}`);
  }
  process.stdout.write(JSON.stringify({ startup, ...result }));
} finally {
  socket.close();
}
