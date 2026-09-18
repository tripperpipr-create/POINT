const endpoint = process.argv[2];
if (!endpoint) throw new Error('Usage: node verify-point-safe-mode.mjs <endpoint>');

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

function command(method, params = {}, sessionId = undefined) {
  const id = ++sequence;
  socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
}

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true }).then(result => result.result?.value);

try {
  await command('Runtime.enable');
  await evaluate(`(() => { const close = document.querySelector('.modal-editor-part .codicon-close'); if (!close) return false; close.click(); return true; })()`);
  await delay(500);
  const banner = await evaluate(`(() => {
    const element = document.querySelector('.part.banner');
    return {
      visible: Boolean(element && getComputedStyle(element).display !== 'none' && element.getBoundingClientRect().height > 0),
      text: (element?.innerText ?? '').replace(/\\s+/g, ' ').trim(),
      actions: Array.from(element?.querySelectorAll('a, button') ?? []).map(action => (action.textContent ?? action.getAttribute('aria-label') ?? '').trim()).filter(Boolean),
    };
  })()`);

  await evaluate(`document.querySelector('.point-activitybar .codicon-source-control-view-icon')?.click()`);
  await delay(1200);
  const versions = await evaluate(`(() => ({ text: (document.querySelector('.part.sidebar')?.innerText ?? '').replace(/\\s+/g, ' ').trim() }))()`);

  await evaluate(`document.querySelector('.point-activitybar .action-label.uri-icon')?.click()`);
  await delay(1800);
  const aiShell = await evaluate(`(() => ({ text: (document.querySelector('.part.sidebar')?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1600), notifications: (document.querySelector('.notifications-toasts')?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1600), webviews: document.querySelectorAll('webview, iframe.webview').length }))()`);
  const targetSnapshot = await command('Target.getTargets');
  const webviewTexts = [];
  for (const candidate of targetSnapshot.targetInfos ?? []) {
    if (candidate.targetId === target.id || !['iframe', 'webview', 'page'].includes(candidate.type) || !/vscode-webview|webview/i.test(candidate.url ?? '')) continue;
    try {
      const attached = await command('Target.attachToTarget', { targetId: candidate.targetId, flatten: true });
      const content = await command('Runtime.evaluate', { expression: `(document.body?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 2400)`, returnByValue: true }, attached.sessionId);
      if (content.result?.value) webviewTexts.push(content.result.value);
      await command('Target.detachFromTarget', { sessionId: attached.sessionId });
    } catch {
      // The workbench can replace a webview target while its view is resolving.
    }
  }
  const ai = { ...aiShell, webviewText: webviewTexts.join(' ') };

  if (!banner?.visible || !banner.text.includes('Проект открыт безопасно: Git, терминал, расширения и ИИ-инструменты не запускаются.') || !banner.text.includes('Настроить доступ') || /Дополнительные сведения|Learn More|VS Code|Visual Studio Code/i.test(banner.text)) {
    throw new Error(`Point safe-mode banner verification failed: ${JSON.stringify(banner)}`);
  }
  if (!versions?.text.includes('Разрешить Git для проекта') || !versions.text.includes('Чтобы включить Git')) {
    throw new Error(`Point safe-mode Versions verification failed: ${JSON.stringify(versions)}`);
  }
  if (/VS Code|Visual Studio Code|Code - OSS/i.test(`${versions.text} ${ai?.text ?? ''} ${ai?.webviewText ?? ''}`)) {
    throw new Error(`Upstream branding leaked in safe mode: ${JSON.stringify({ versions, ai })}`);
  }
  if (ai.webviews < 1 || (ai.webviewText && (!ai.webviewText.includes('Умения агентов пока запечатаны') || !ai.webviewText.includes('Настроить доступ') || !ai.webviewText.includes('Команды и содержимое проекта не отправляются модели')))) {
    throw new Error(`Point safe-mode AI surface verification failed: ${JSON.stringify(ai)}`);
  }
  if (/Ошибка:\s*Point|Сначала подтвердите доверие|Агент не запущен/i.test(`${ai.text} ${ai.notifications} ${ai.webviewText}`)) {
    throw new Error(`Technical error leaked into Point safe-mode AI surface: ${JSON.stringify(ai)}`);
  }
  process.stdout.write(JSON.stringify({ banner, versions, ai }));
} finally {
  socket.close();
}
