import fs from 'node:fs';

const endpoint = process.argv[2];
const screenshotPath = process.argv[3] || '';
if (!endpoint) throw new Error('Usage: node verify-point-connections-e2e.mjs <endpoint> [screenshot-path]');

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
async function waitFor(description, probe, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const value = await probe();
      if (value) return value;
    } catch (error) {
      lastError = error;
    }
    await delay(120);
  }
  throw new Error(`Timed out waiting for ${description}${lastError ? `: ${lastError.message}` : ''}`);
}

const listTargets = () => fetch(`${endpoint}/json/list`).then(response => response.json());
const target = await waitFor('Agent Hub webview', async () => (await listTargets()).find(candidate =>
  candidate.type === 'iframe' && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench')));
const socket = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((resolve, reject) => {
  socket.addEventListener('open', resolve, { once: true });
  socket.addEventListener('error', reject, { once: true });
});

let sequence = 0;
const pending = new Map();
const contexts = new Map();
socket.addEventListener('message', event => {
  const message = JSON.parse(String(event.data));
  if (message.method === 'Runtime.executionContextCreated') {
    contexts.set(message.params.context.id, message.params.context);
    return;
  }
  if (message.method === 'Runtime.executionContextDestroyed') {
    contexts.delete(message.params.executionContextId);
    return;
  }
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
  await Promise.all([command('Runtime.enable'), command('Page.enable')]);
  const appContextId = await waitFor('Agent Hub application context', async () => {
    for (const context of contexts.values()) {
      try {
        const result = await command('Runtime.evaluate', {
          contextId: context.id,
          expression: `Boolean(document.querySelector('#root')?.children.length)`,
          returnByValue: true,
        });
        if (result.result?.value === true) return context.id;
      } catch {}
    }
    return 0;
  });
  const evaluate = expression => command('Runtime.evaluate', {
    contextId: appContextId,
    expression,
    returnByValue: true,
    awaitPromise: true,
  }).then(result => {
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || 'Agent Hub evaluation failed');
    return result.result?.value;
  });

  // Stay black-box: the bundle deliberately keeps mutable state and the VS
  // Code message channel private. Advance pristine onboarding through its real
  // primary actions, then navigate through the delegated DOM action handler.
  // The first #root paint is a loading shell; do not mistake it for completed
  // onboarding before the extension delivers its initial state.
  await waitFor('initial Agent Hub onboarding', () => evaluate(`Boolean(document.querySelector('.onboarding-wizard'))`));
  for (let step = 0; step < 12; step += 1) {
    const onboarding = await evaluate(`(() => {
      const page = document.querySelector('.onboarding');
      if (!page) return { done: true };
      const button = page.querySelector('.onboarding-footer button.primary');
      if (!button || button.disabled) return { done: false, clicked: false, text: page.innerText.slice(0, 1200) };
      const result = { done: false, clicked: true, action: button.dataset.action || '', targetStep: button.dataset.step || '', label: button.textContent?.trim() || '' };
      button.click();
      return result;
    })()`);
    if (onboarding.done) break;
    if (!onboarding.clicked) throw new Error(`Onboarding primary action is unavailable: ${JSON.stringify(onboarding)}`);
    await delay(500);
  }
  await waitFor('completed Agent Hub onboarding', () => evaluate(`!document.querySelector('.onboarding')`));
  const selectTab = tab => evaluate(`(() => {
    const root = document.querySelector('#root');
    if (!root) return false;
    const button = document.createElement('button');
    button.type = 'button'; button.dataset.action = 'tab'; button.dataset.tab = ${JSON.stringify(tab)};
    root.appendChild(button); button.click(); button.remove();
    return true;
  })()`);
  await selectTab('connections');
  await waitFor('SSH connections surface', () => evaluate(`Boolean(document.querySelector('.hub-connections #server-form'))`));
  const sshSubmitted = await evaluate(`(() => {
    const form = document.querySelector('#server-form');
    if (!form) return false;
    document.querySelector('#server-name').value = 'Point E2E SSH';
    document.querySelector('#server-host').value = '127.0.0.1';
    document.querySelector('#server-port').value = '1';
    document.querySelector('#server-user').value = 'point-e2e';
    document.querySelector('#server-auth').value = 'agent';
    document.querySelector('#server-remote-path').value = '~';
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    return true;
  })()`);
  if (!sshSubmitted) throw new Error('SSH profile form was not submitted');
  const sshSaved = await waitFor('saved SSH profile', () => evaluate(`(() => {
    const card = Array.from(document.querySelectorAll('.hub-connections .hub-card')).find(item =>
      item.querySelector('strong')?.textContent?.trim() === 'Point E2E SSH');
    if (!card) return null;
    return { status: card.querySelector('header span')?.textContent?.trim(), text: card.innerText.replace(/\\s+/g, ' ').trim() };
  })()`));
  const sshProbeStarted = await evaluate(`(() => {
    const card = Array.from(document.querySelectorAll('.hub-connections .hub-card')).find(item =>
      item.querySelector('strong')?.textContent?.trim() === 'Point E2E SSH');
    const button = card?.querySelector('[data-action="probe-server"]');
    button?.click();
    return Boolean(button);
  })()`);
  if (!sshProbeStarted) throw new Error('Saved SSH profile probe button was not found');
  const sshProbed = await waitFor('real OpenSSH probe result', () => evaluate(`(() => {
    const card = Array.from(document.querySelectorAll('.hub-connections .hub-card')).find(item =>
      item.querySelector('strong')?.textContent?.trim() === 'Point E2E SSH');
    const status = card?.querySelector('header span')?.textContent?.trim() || '';
    if (!card || !status || status === 'НЕИЗВЕСТНО') return null;
    return {
      status,
      error: card.querySelector('.create-step-error')?.textContent?.trim() || '',
      cardStatus: status,
      cardText: card?.innerText.replace(/\\s+/g, ' ').trim() || '',
      bodyText: document.body.innerText.replace(/\\s+/g, ' ').trim().slice(0, 1200),
    };
  })()`), 30000);
  if (!sshProbed.error.includes('отклонил соединение') || sshProbed.cardStatus !== 'ОШИБКА') {
    throw new Error(`OpenSSH failure was not reflected in the profile and UI: ${JSON.stringify(sshProbed)}`);
  }

  await selectTab('databases');
  await waitFor('database surface', () => evaluate(`Boolean(document.querySelector('.hub-databases #db-connection-form'))`));
  const dbSubmitted = await evaluate(`(() => {
    const form = document.querySelector('#db-connection-form');
    if (!form) return false;
    document.querySelector('#db-name').value = 'Point E2E SQLite';
    document.querySelector('#db-driver').value = 'sqlite';
    document.querySelector('#db-database').value = 'data/point-connections-e2e.db';
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    return true;
  })()`);
  if (!dbSubmitted) throw new Error('Database form was not submitted');
  const dbSaved = await waitFor('saved SQLite connection', () => evaluate(`(() => {
    const card = Array.from(document.querySelectorAll('.hub-databases .hub-card')).find(item =>
      item.querySelector('strong')?.textContent?.trim() === 'Point E2E SQLite');
    return card ? { status: card.querySelector('header span')?.textContent?.trim(), text: card.innerText.replace(/\\s+/g, ' ').trim() } : null;
  })()`));

  async function submitSQL(sql, expectConfirmation) {
    const submitted = await evaluate(`(() => {
      const input = document.querySelector('#db-sql');
      const form = document.querySelector('#db-query-form');
      if (!form || !input) return false;
      // Rendering is deferred to requestAnimationFrame: ignore the previous result.
      document.querySelector('.db-query-result')?.remove();
      input.value = ${JSON.stringify(sql)};
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      return true;
    })()`);
    if (!submitted) throw new Error(`SQL form was not submitted: ${sql}`);
    if (expectConfirmation) {
      const gate = await waitFor('database write confirmation', () => evaluate(`(() => {
        const button = document.querySelector('[data-action="apply-db-write"]');
        return button ? { visible: true, bodyText: document.body.innerText.replace(/\\s+/g, ' ').trim().slice(-1200) } : null;
      })()`));
      const applied = await evaluate(`(() => {
        const button = document.querySelector('[data-action="apply-db-write"]');
        button?.click();
        return Boolean(button);
      })()`);
      if (!applied) throw new Error('Database write confirmation could not be applied');
    }
    return waitFor(`SQL result for ${sql.slice(0, 30)}`, () => evaluate(`(() => {
      const result = document.querySelector('.db-query-result');
      return result ? result.innerText.replace(/\\s+/g, ' ').trim() : null;
    })()`));
  }

  const createResult = await submitSQL('CREATE TABLE point_e2e (id INTEGER PRIMARY KEY, name TEXT)', true);
  const insertResult = await submitSQL("INSERT INTO point_e2e(name) VALUES ('works')", true);
  const selectResult = await submitSQL('SELECT id, name FROM point_e2e ORDER BY id', false);
  if (!selectResult.includes('works') || !selectResult.includes('name')) {
    throw new Error(`SQLite SELECT result is incomplete: ${selectResult}`);
  }

  const schemaClicked = await evaluate(`(() => {
    const button = document.querySelector('#db-query-form [data-action="schema-db"]');
    button?.click();
    return Boolean(button);
  })()`);
  if (!schemaClicked) throw new Error('Database schema button was not found');
  const schema = await waitFor('SQLite schema result', () => evaluate(`(() => {
    const result = document.querySelector('.db-schema');
    const text = result?.innerText.replace(/\\s+/g, ' ').trim() || '';
    return text.includes('point_e2e') ? text : null;
  })()`));

  const testClicked = await evaluate(`(() => {
    const card = Array.from(document.querySelectorAll('.hub-databases .hub-card')).find(item =>
      item.querySelector('strong')?.textContent?.trim() === 'Point E2E SQLite');
    const button = card?.querySelector('[data-action="test-db"]');
    button?.click();
    return Boolean(button);
  })()`);
  if (!testClicked) throw new Error('Database test button was not found');
  const dbTested = await waitFor('connected SQLite status', () => evaluate(`(() => {
    const card = Array.from(document.querySelectorAll('.hub-databases .hub-card')).find(item =>
      item.querySelector('strong')?.textContent?.trim() === 'Point E2E SQLite');
    const status = card?.querySelector('header span')?.textContent?.trim() || '';
    return status === 'ПОДКЛЮЧЕН' ? { status, text: card.innerText.replace(/\\s+/g, ' ').trim() } : null;
  })()`));

  if (screenshotPath) {
    await command('Page.captureScreenshot', { format: 'png', fromSurface: true }).then(result =>
      fs.writeFileSync(screenshotPath, Buffer.from(result.data, 'base64')));
  }
  process.stdout.write(JSON.stringify({
    ssh: { saved: sshSaved, probed: sshProbed },
    database: { saved: dbSaved, createResult, insertResult, selectResult, schema, tested: dbTested },
    screenshot: screenshotPath,
  }));
} finally {
  socket.close();
}
