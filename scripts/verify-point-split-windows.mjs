import fs from 'node:fs';

const endpoint = process.argv[2];
const ideScreenshot = process.argv[3] || '';
const agentsScreenshot = process.argv[4] || '';
const windowMode = process.argv[5] || 'sessions';
if (!endpoint || !['auxiliary', 'sessions'].includes(windowMode)) {
  throw new Error('Usage: node verify-point-split-windows.mjs <endpoint> [ide.png] [agents.png] [auxiliary|sessions]');
}

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const listTargets = async () => {
  const response = await fetch(`${endpoint}/json/list`);
  if (!response.ok) throw new Error(`CDP target list failed: ${response.status}`);
  return response.json();
};

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
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}${lastError ? `: ${lastError.message}` : ''}`);
}

async function connect(target) {
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
    if (message.error) handlers.reject(new Error(message.error.message));
    else handlers.resolve(message.result);
  });
  const command = (method, params = {}) => {
    const id = ++sequence;
    socket.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
  };
  const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true }).then(result => result.result?.value);
  await Promise.all([command('Runtime.enable'), command('Page.enable')]);
  return { socket, command, evaluate };
}

async function keyStroke(client, key, code, windowsVirtualKeyCode, modifiers = 0) {
  const event = { key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers };
  await client.command('Input.dispatchKeyEvent', { type: 'rawKeyDown', ...event });
  await client.command('Input.dispatchKeyEvent', { type: 'keyUp', ...event });
}

async function clickPoint(client, point) {
  if (!point || !Number.isFinite(point.x) || !Number.isFinite(point.y)) throw new Error(`Clickable point is missing: ${JSON.stringify(point)}`);
  await client.command('Input.dispatchMouseEvent', { type: 'mouseMoved', x: point.x, y: point.y });
  await client.command('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
  await client.command('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
}

async function capture(client, output) {
  if (!output) return '';
  const screenshot = await client.command('Page.captureScreenshot', { format: 'png', fromSurface: true });
  fs.writeFileSync(output, Buffer.from(screenshot.data, 'base64'));
  return output;
}

async function wideHubState(target) {
  const client = await connect(target);
  try {
    const tree = await client.command('Page.getFrameTree');
    const frames = [];
    const collect = node => {
      frames.push(node.frame);
      for (const child of node.childFrames || []) collect(child);
    };
    collect(tree.frameTree);
    for (const frame of frames.reverse()) {
      try {
        const world = await client.command('Page.createIsolatedWorld', { frameId: frame.id, worldName: `point-split-${Date.now()}` });
        const response = await client.command('Runtime.evaluate', {
          contextId: world.executionContextId,
          returnByValue: true,
          expression: `(() => {
            const text = (document.body?.innerText || '').replace(/\\s+/g, ' ').trim();
            // Готовность переехала из колонки справа в полосу под шапкой:
            // семи однострочным фактам не нужна колонка на всю высоту, а шагу
            // нужна ширина.
            const specialist = Array.from(document.querySelectorAll('.onboarding-readiness > span')).find(item =>
              (item.querySelector('strong')?.textContent || '').trim() === 'Специалист');
            const specialistDetail = (specialist?.querySelector('small')?.textContent || '').trim();
            const specialistMatch = specialistDetail.match(/^(\\d+)\\s+в команде проекта$/);
            return {
              layout: document.body?.dataset?.layout || '',
              title: document.title,
              text: text.slice(0, 1800),
              onboarding: Boolean(document.querySelector('.onboarding')),
              master: Boolean(document.querySelector('#master-input, .hall-thread')),
              onboardingActiveStep: (document.querySelector('.onboarding-step-rail button.on')?.textContent || '').trim(),
              onboardingNextLabel: (document.querySelector('.onboarding-footer button.primary')?.textContent || '').trim(),
              onboardingNextStep: document.querySelector('.onboarding-footer button.primary')?.dataset?.step || '',
              onboardingInlinePrimary: document.querySelectorAll('.onboarding-panel > .onboarding-welcome-cta .primary').length,
              agentCount: specialistMatch ? Number(specialistMatch[1]) : (specialistDetail === 'ещё не подключён' ? 0 : null),
              width: Math.round(document.documentElement?.clientWidth || document.body?.clientWidth || 0),
              horizontalOverflow: Math.max(0, (document.documentElement?.scrollWidth || 0) - (document.documentElement?.clientWidth || 0)),
            };
          })()`,
        });
        if (response.result?.value?.layout === 'wide') return response.result.value;
      } catch {
        // Some iframe frames disappear during the first Hub paint; try the next target.
      }
    }
    return null;
  } finally {
    client.socket.close();
  }
}

async function requestWideHubFileOpen(relativePath, line) {
  const candidates = (await listTargets()).filter(target => target.type === 'iframe' && String(target.url).includes('extensionId=local-agent.local-agent-workbench'));
  for (const candidate of candidates) {
    const client = await connect(candidate);
    try {
      const tree = await client.command('Page.getFrameTree');
      const frames = [];
      const collect = node => {
        frames.push(node.frame);
        for (const child of node.childFrames || []) collect(child);
      };
      collect(tree.frameTree);
      for (const frame of frames.reverse()) {
        try {
          const world = await client.command('Page.createIsolatedWorld', { frameId: frame.id, worldName: `point-open-file-${Date.now()}` });
          const response = await client.command('Runtime.evaluate', {
            contextId: world.executionContextId,
            returnByValue: true,
            expression: `(() => {
              if (document.body?.dataset?.layout !== 'wide') return false;
              const root = document.querySelector('#root');
              if (!root) return false;
              const button = document.createElement('button');
              button.type = 'button';
              button.dataset.action = 'open-file';
              button.dataset.path = ${JSON.stringify(relativePath)};
              button.dataset.line = ${JSON.stringify(String(line || ''))};
              root.appendChild(button);
              button.click();
              button.remove();
              return true;
            })()`,
          });
          if (response.result?.value) return true;
        } catch {
          // The wide webview can repaint while the request is dispatched.
        }
      }
    } finally {
      client.socket.close();
    }
  }
  return false;
}

async function companionSurfaceState(target, expectedLayout, action = null) {
  const client = await connect(target);
  try {
    const tree = await client.command('Page.getFrameTree');
    const frames = [];
    const collect = node => {
      frames.push(node.frame);
      for (const child of node.childFrames || []) collect(child);
    };
    collect(tree.frameTree);
    for (const frame of frames.reverse()) {
      try {
        const world = await client.command('Page.createIsolatedWorld', { frameId: frame.id, worldName: `point-companion-${Date.now()}` });
        const response = await client.command('Runtime.evaluate', {
          contextId: world.executionContextId,
          returnByValue: true,
          expression: `(() => {
            const layout = document.body?.dataset?.layout || '';
            if (layout !== ${JSON.stringify(expectedLayout)}) return null;
            ${action || ''}
            const input = document.querySelector('#companion-input');
            const text = (document.body?.innerText || '').replace(/\\s+/g, ' ').trim();
            return {
              layout,
              text: text.slice(0, 2200),
              input: Boolean(input),
              inputValue: input?.value || '',
              starters: Array.from(document.querySelectorAll('.companion-starter strong')).map(item => (item.textContent || '').trim()),
              userMessages: document.querySelectorAll('.companion-msg.user').length,
              assistantMessages: document.querySelectorAll('.companion-msg.assistant').length,
              loading: Boolean(document.querySelector('.companion-msg.streaming, .companion-activity')),
              actionCards: document.querySelectorAll('.companion-action-card').length,
              actionCardTexts: Array.from(document.querySelectorAll('.companion-action-card')).map(item => (item.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 900)),
              actionApplyLabels: Array.from(document.querySelectorAll('[data-action="companion-action-apply"]')).map(item => (item.textContent || '').trim()),
              actionReviewOpen: Boolean(document.querySelector('#companion-review')?.open),
              actionApplyVisible: Array.from(document.querySelectorAll('[data-action="companion-action-apply"]')).some(item => {
                const rect = item.getBoundingClientRect();
                return rect.width > 0 && rect.height > 0 && getComputedStyle(item).visibility !== 'hidden';
              }),
              questCards: document.querySelectorAll('.draft-sheet-card').length,
              questCardTexts: Array.from(document.querySelectorAll('.draft-sheet-card')).map(item => (item.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 1200)),
              questStartLabels: Array.from(document.querySelectorAll('[data-action="quest-proposal-start"]')).map(item => (item.textContent || '').trim()),
              questStartVisible: Array.from(document.querySelectorAll('[data-action="quest-proposal-start"]')).some(item => {
                const rect = item.getBoundingClientRect();
                return rect.width > 0 && rect.height > 0 && getComputedStyle(item).visibility !== 'hidden';
              }),
              errorText: (document.querySelector('.error-banner')?.textContent || '').replace(/\s+/g, ' ').trim(),
              appliedNotice: (document.querySelector('.companion-applied-cue')?.textContent || '').replace(/\s+/g, ' ').trim(),
            };
          })()`,
        });
        if (response.result?.value) return response.result.value;
      } catch {
        // The webview can repaint while switching surfaces; try its next frame.
      }
    }
    return null;
  } finally {
    client.socket.close();
  }
}

async function findCompanionSurface(layout, action = null, predicate = null) {
  const candidates = (await listTargets()).filter(target => target.type === 'iframe' && String(target.url).includes('extensionId=local-agent.local-agent-workbench'));
  for (const candidate of candidates) {
    const state = await companionSurfaceState(candidate, layout, action);
    if (state && (!predicate || predicate(state))) return state;
  }
  return null;
}

const ideTarget = await waitFor('IDE workbench target', async () => (await listTargets()).find(target => target.type === 'page' && String(target.url).includes('/workbench/')));
const ide = await connect(ideTarget);

try {
  await waitFor('IDE workbench DOM', () => ide.evaluate(`Boolean(document.querySelector('.monaco-workbench'))`));
  const trust = await ide.evaluate(`(() => {
    const dialog = Array.from(document.querySelectorAll('.monaco-dialog-box')).find(item => (item.innerText || '').includes('Вы доверяете этому проекту?'));
    const button = Array.from(dialog?.querySelectorAll('.monaco-button') || []).find(item => (item.textContent || '').includes('Доверять проекту'));
    const sidebar = document.querySelector('.part.sidebar');
    const sidebarText = (sidebar?.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 240);
    button?.click();
    return { found: Boolean(dialog), clicked: Boolean(button), sidebarText };
  })()`);
  if (trust.found && !trust.clicked) throw new Error(`Workspace trust button is missing: ${JSON.stringify(trust)}`);
  if (trust.clicked) await delay(1400);

  const sidebarBeforeAssistant = await ide.evaluate(`(() => {
    const part = document.querySelector('.part.sidebar');
    const rect = part?.getBoundingClientRect();
    return {
      visible: Boolean(rect && rect.width > 40 && getComputedStyle(part).display !== 'none'),
      width: Math.round(rect?.width || 0),
      inventory: Boolean(part?.querySelector('.explorer-folders-view')),
      chronicle: Boolean(part?.querySelector('.scm-view')),
      text: (part?.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 240),
    };
  })()`);
  if (!sidebarBeforeAssistant.inventory || sidebarBeforeAssistant.chronicle) {
    throw new Error(`Fresh Point workspace did not initially start in Inventory: ${JSON.stringify({ trust, sidebarBeforeAssistant })}`);
  }

  const titlebarButtons = await ide.evaluate(`(() => {
    const point = selector => {
      const element = document.querySelector(selector);
      const rect = element?.getBoundingClientRect();
      return rect && rect.width > 0 && rect.height > 0
        ? { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2, width: rect.width, height: rect.height }
        : null;
    };
    return {
      project: point('.point-project-switcher'),
      menu: point('.point-menu-toggle[aria-label="Меню"]'),
      actions: Array.from(document.querySelectorAll('.point-title-action')).map(item => item.getAttribute('aria-label') || '').filter(Boolean),
    };
  })()`);
  const expectedTitlebarActions = ['Меню', 'Запустить проект', 'Запустить с отладкой'];
  if (!titlebarButtons.project || !titlebarButtons.menu
      || expectedTitlebarActions.some(label => !titlebarButtons.actions.includes(label))) {
    throw new Error(`Point titlebar controls are incomplete: ${JSON.stringify(titlebarButtons)}`);
  }
  await clickPoint(ide, titlebarButtons.project);
  const projectPicker = await waitFor('physical project switcher click', () => ide.evaluate(`(() => {
    const widget = document.querySelector('.point-popup.point-project-popup');
    const rect = widget?.getBoundingClientRect();
    return rect && rect.width > 100 && rect.height > 40 && getComputedStyle(widget).display !== 'none'
      ? (widget.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 500)
      : '';
  })()`), 10000);
  await keyStroke(ide, 'Escape', 'Escape', 27);
  await delay(250);
  await clickPoint(ide, titlebarButtons.menu);
  const menuBar = await waitFor('expanded titlebar menu', () => ide.evaluate(`(() => {
    const container = document.querySelector('.part.titlebar .titlebar-container');
    const bar = container?.querySelector('.menubar');
    const rect = bar?.getBoundingClientRect();
    const items = Array.from(bar?.querySelectorAll('.menubar-menu-button') || []).map(item => (item.textContent || '').trim()).filter(Boolean);
    return container?.classList.contains('point-menu') && rect && rect.width > 100 && rect.height > 10 && items.length >= 5
      ? { expanded: true, items }
      : null;
  })()`), 10000);
  await keyStroke(ide, 'Escape', 'Escape', 27);
  await delay(250);
  await keyStroke(ide, 'A', 'KeyA', 65, 9);
  await waitFor('Alt+Shift+A quick actions input', () => ide.evaluate(`Boolean(document.querySelector('.quick-input-widget input'))`), 10000);
  // Electron can deliver the shortcut key to the input after the command has
  // already opened the picker. Clear that transient "A" before asserting the
  // unfiltered catalog.
  await keyStroke(ide, 'a', 'KeyA', 65, 2);
  await keyStroke(ide, 'Backspace', 'Backspace', 8);
  const quickActionsPicker = await waitFor('Alt+Shift+A quick actions', () => ide.evaluate(`(() => {
    const widget = document.querySelector('.quick-input-widget');
    const rect = widget?.getBoundingClientRect();
    const text = (widget?.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 2400);
    return rect && rect.width > 100 && rect.height > 40 && getComputedStyle(widget).display !== 'none' && text.includes('Навигация')
      ? text
      : '';
  })()`), 10000);
  // Quick Pick virtualizes rows below the viewport, so later category
  // separators (including "Агенты") are not guaranteed to exist in the DOM.
  if (!quickActionsPicker.includes('POINT — БЫСТРЫЕ ДЕЙСТВИЯ') || !quickActionsPicker.includes('Навигация') || !quickActionsPicker.includes('Результаты:')) {
    throw new Error(`Point quick actions are incomplete: ${JSON.stringify(quickActionsPicker)}`);
  }
  await keyStroke(ide, 'Escape', 'Escape', 27);
  await delay(250);

  const expectedStarters = ['Обсудить код', 'Создать агента', 'Создать квест', 'Подобрать отряд'];
  // The right rail and its default Assistant surface must be present without a
  // shortcut. A manual open here would hide a startup regression from the test.
  const statusOpen = await waitFor('Point assistant status entry', () => ide.evaluate(`(() => {
    const item = Array.from(document.querySelectorAll('.statusbar-item')).find(candidate => (candidate.innerText || '').includes('Помощник'));
    if (!item) return null;
    const target = item.querySelector('a, button') || item;
    return { found: true, clicked: false, id: item.id || '', text: (item.innerText || '').trim(), target: target.tagName || '' };
  })()`), 30000);
  if (!statusOpen.found) throw new Error(`Assistant status-bar entry is missing: ${JSON.stringify(statusOpen)}`);
  const right = await waitFor('right IDE assistant chat', async () => {
    const value = await findCompanionSurface('companion-sidebar');
    return value?.input ? value : null;
  }, 30000);
  const auxiliary = await ide.evaluate(`(() => {
    const part = document.querySelector('.part.auxiliarybar');
    const rect = part?.getBoundingClientRect();
    const compositeItems = Array.from(part?.querySelectorAll('.composite-bar .action-item') || []).map(item => {
      const itemRect = item.getBoundingClientRect();
      const label = item.querySelector('.action-label');
      return {
        text: (item.textContent || '').replace(/\s+/g, ' ').trim(),
        title: label?.getAttribute('title') || item.getAttribute('title') || '',
        ariaLabel: label?.getAttribute('aria-label') || item.getAttribute('aria-label') || '',
        classes: item.className || '',
        visible: itemRect.width > 0 && itemRect.height > 0 && getComputedStyle(item).display !== 'none',
      };
    });
    return {
      visible: Boolean(rect && rect.width > 40 && getComputedStyle(part).display !== 'none'),
      width: Math.round(rect?.width || 0),
      compositeItems,
    };
  })()`);
  const primarySidebar = await ide.evaluate(`(() => {
    const part = document.querySelector('.part.sidebar');
    const rect = part?.getBoundingClientRect();
    return {
      visible: Boolean(rect && rect.width > 40 && getComputedStyle(part).display !== 'none'),
      width: Math.round(rect?.width || 0),
      inventory: Boolean(part?.querySelector('.explorer-folders-view')),
      chronicle: Boolean(part?.querySelector('.scm-view')),
      text: (part?.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 240),
    };
  })()`);
  if (!primarySidebar.inventory || primarySidebar.chronicle) {
    throw new Error(`Fresh Point workspace did not start in Inventory: ${JSON.stringify(primarySidebar)}`);
  }
  const expectedRightTools = ['Помощник Point', 'Терминал', 'Базы данных', 'SSH', 'Логи'];
  const visibleRightTools = auxiliary.compositeItems
    .filter(item => item.visible)
    .map(item => item.ariaLabel || item.text)
    .filter(Boolean);
  if (expectedRightTools.some(label => !visibleRightTools.includes(label))) {
    throw new Error(`Right tool tabs are not all directly visible: ${JSON.stringify({ expectedRightTools, auxiliary })}`);
  }
  const toolSurfaces = {};
  for (const [label, layout, marker] of [
    ['Терминал', 'tool-terminal', 'Терминал'],
    ['Базы данных', 'tool-database', 'Базы данных'],
    ['SSH', 'tool-ssh', 'SSH'],
    ['Логи', 'tool-logs', 'Логи'],
  ]) {
    const clicked = await ide.evaluate(`(() => {
      const part = document.querySelector('.part.auxiliarybar');
      const action = Array.from(part?.querySelectorAll('.composite-bar .action-item') || []).find(item => {
        const target = item.querySelector('.action-label') || item;
        return (target.getAttribute('aria-label') || '').trim() === ${JSON.stringify(label)};
      });
      (action?.querySelector('.action-label') || action)?.click();
      return Boolean(action);
    })()`);
    if (!clicked) throw new Error(`Right tool tab could not be clicked: ${label}`);
    const markerLower = marker.toLocaleLowerCase('ru-RU');
    const surface = await waitFor(`${label} right tool surface`, () => findCompanionSurface(
      layout,
      null,
      state => state.text.toLocaleLowerCase('ru-RU').includes(markerLower),
    ), 20000);
    toolSurfaces[label] = { layout: surface.layout, text: surface.text.slice(0, 220) };
  }
  const gitActivity = await ide.evaluate(`(() => {
    const bar = document.querySelector('.point-activitybar');
    const action = Array.from(bar?.querySelectorAll('.action-item') || []).find(item => {
      const target = item.querySelector('.action-label') || item;
      return (target.getAttribute('aria-label') || '').trim().startsWith('Git');
    });
    (action?.querySelector('.action-label') || action)?.click();
    return { clicked: Boolean(action), ariaLabel: (action?.querySelector('.action-label') || action)?.getAttribute('aria-label') || '' };
  })()`);
  if (!gitActivity.clicked) throw new Error(`Primary Git activity item is missing: ${JSON.stringify(gitActivity)}`);
  const gitSurface = await waitFor('Git primary tool surface', () => findCompanionSurface(
    'tool-git',
    null,
    state => state.text.toLocaleLowerCase('ru-RU').includes('git'),
  ), 20000);
  toolSurfaces.Git = { layout: gitSurface.layout, text: gitSurface.text.slice(0, 220) };
  await ide.evaluate(`document.querySelector('.point-activitybar .codicon-explorer-view-icon')?.click()`);
  await waitFor('restored Inventory after Git surface', () => ide.evaluate(`Boolean(document.querySelector('.part.sidebar .explorer-folders-view'))`), 10000);
  await ide.evaluate(`(() => {
    const part = document.querySelector('.part.auxiliarybar');
    const action = Array.from(part?.querySelectorAll('.composite-bar .action-item') || []).find(item => {
      const target = item.querySelector('.action-label') || item;
      return (target.getAttribute('aria-label') || '').trim() === 'Помощник Point';
    });
    (action?.querySelector('.action-label') || action)?.click();
  })()`);
  await waitFor('restored right IDE assistant chat', async () => {
    const value = await findCompanionSurface('companion-sidebar');
    return value?.input ? value : null;
  }, 20000);
  const duplicateDock = await findCompanionSurface('companion');
  if (!auxiliary.visible || !right.input || duplicateDock || expectedStarters.some(label => !right.starters.includes(label))) {
    throw new Error(`Right assistant did not become the single complete visible chat: ${JSON.stringify({ auxiliary, primarySidebar, right, duplicateDock })}`);
  }

  const prefillResults = {};
  for (const label of ['Создать агента', 'Создать квест', 'Подобрать отряд']) {
    const action = `
      const button = Array.from(document.querySelectorAll('.companion-starter')).find(item => (item.innerText || '').includes(${JSON.stringify(label)}));
      if (!button) throw new Error('Starter is missing: ' + ${JSON.stringify(label)});
      button.click();
    `;
    await findCompanionSurface('companion-sidebar', action);
    await delay(120);
    const result = await findCompanionSurface('companion-sidebar');
    prefillResults[label] = result?.inputValue || '';
  }
  if (!prefillResults['Создать агента'].startsWith('Создай агента')
      || !prefillResults['Создать квест'].startsWith('Создай квест')
      || !prefillResults['Подобрать отряд'].startsWith('Собери отряд')) {
    throw new Error(`Assistant action starters did not prepare understandable prompts: ${JSON.stringify(prefillResults)}`);
  }

  const roundTripPrompt = 'Коротко: что ты видишь в этом проекте?';
  await findCompanionSurface('companion-sidebar', `
    {
      const composerInput = document.querySelector('#companion-input');
      const composerForm = document.querySelector('#companion-form');
      if (!composerInput || !composerForm) throw new Error('Companion composer is missing');
      composerInput.value = ${JSON.stringify('Коротко: что ты видишь в этом проекте?')};
      composerInput.dispatchEvent(new Event('input', { bubbles: true }));
      if (typeof composerForm.requestSubmit === 'function') composerForm.requestSubmit();
      else composerForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    }
  `);
  let lastChatRoundTrip = null;
  let chatRoundTrip;
  try {
    chatRoundTrip = await waitFor('Companion answer in the right sidebar', async () => {
      const value = await findCompanionSurface('companion-sidebar');
      if (value) lastChatRoundTrip = value;
      return value?.userMessages >= 1
        && value?.assistantMessages >= 1
        && value.text.includes(roundTripPrompt)
        && /В мире\s+\d+\s+агент/.test(value.text)
        && !value?.loading
        ? value
        : null;
    }, 45000);
  } catch (error) {
    throw new Error(`${error.message}; last sidebar state: ${JSON.stringify(lastChatRoundTrip)}`);
  }
  if (!chatRoundTrip.text.includes(roundTripPrompt) || !chatRoundTrip.input || chatRoundTrip.inputValue) {
    throw new Error(`Companion round trip did not finish as one clear conversation: ${JSON.stringify(chatRoundTrip)}`);
  }

  const createAgentPrompt = 'Создай агента с именем QA-страж для проверки этого проекта';
  await findCompanionSurface('companion-sidebar', `
    {
      const composerInput = document.querySelector('#companion-input');
      const composerForm = document.querySelector('#companion-form');
      if (!composerInput || !composerForm) throw new Error('Companion composer is missing');
      composerInput.value = ${JSON.stringify('Создай агента с именем QA-страж для проверки этого проекта')};
      composerInput.dispatchEvent(new Event('input', { bubbles: true }));
      if (typeof composerForm.requestSubmit === 'function') composerForm.requestSubmit();
      else composerForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    }
  `);
  for (let attempt = 0; attempt < 2; attempt++) {
    await delay(350);
    const submitted = await findCompanionSurface('companion-sidebar');
    if (submitted?.userMessages >= 2 || submitted?.loading) break;
    await findCompanionSurface('companion-sidebar', `
      {
        const composerForm = document.querySelector('#companion-form');
        if (typeof composerForm?.requestSubmit === 'function') composerForm.requestSubmit();
        else composerForm?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      }
    `);
  }
  let lastAgentDraft = null;
  let agentDraft;
  try {
    agentDraft = await waitFor('reviewable Agent draft from Companion', async () => {
      const value = await findCompanionSurface('companion-sidebar');
      if (value) lastAgentDraft = value;
      return value?.assistantMessages >= 2 && !value.loading && value.actionCards === 1
        && value.actionReviewOpen && value.actionApplyVisible && value.actionApplyLabels.includes('Создать агента') ? value : null;
    }, 30000);
  } catch (error) {
    throw new Error(`${error.message}; last sidebar state: ${JSON.stringify(lastAgentDraft)}`);
  }
  if (!agentDraft.text.includes(createAgentPrompt)
      || !/Проверьте роль, миссию, модель и разрешения перед созданием/.test(agentDraft.text)
      || !agentDraft.text.includes('QA-страж')
      || !agentDraft.actionApplyLabels.includes('Создать агента')) {
    throw new Error(`Companion did not explain the Agent draft before creation: ${JSON.stringify(agentDraft)}`);
  }
  await findCompanionSurface('companion-sidebar', `
    {
      const apply = document.querySelector('[data-action="companion-action-apply"]');
      if (!apply) throw new Error('Create Agent confirmation is missing');
      apply.click();
    }
  `);
  const agentCreation = await waitFor('confirmed Agent creation', async () => {
    const value = await findCompanionSurface('companion-sidebar');
    return value?.actionCards === 0 && value.appliedNotice.includes('Агент создан') ? value : null;
  }, 30000);
  if (/Агент создано/.test(agentCreation.appliedNotice)) {
    throw new Error(`Agent creation confirmation has broken grammar: ${JSON.stringify(agentCreation)}`);
  }

  const createTeamPrompt = 'Собери отряд QA для проверки качества этого проекта';
  await findCompanionSurface('companion-sidebar', `
    {
      const composerInput = document.querySelector('#companion-input');
      const composerForm = document.querySelector('#companion-form');
      if (!composerInput || !composerForm) throw new Error('Companion composer is missing');
      composerInput.value = ${JSON.stringify('Собери отряд QA для проверки качества этого проекта')};
      composerInput.dispatchEvent(new Event('input', { bubbles: true }));
      if (typeof composerForm.requestSubmit === 'function') composerForm.requestSubmit();
      else composerForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    }
  `);
  const teamDraft = await waitFor('reviewable Team draft from Companion', async () => {
    const value = await findCompanionSurface('companion-sidebar');
    return value?.assistantMessages >= 3 && !value.loading && value.actionCards === 1
      && value.actionReviewOpen && value.actionApplyVisible && value.actionApplyLabels.includes('Создать отряд') ? value : null;
  }, 30000);
  if (!teamDraft.actionCardTexts.some(text => text.includes('ЧЕРНОВИК ОТРЯДА') && text.includes('QA-страж'))
      || !teamDraft.actionApplyLabels.includes('Создать отряд')) {
    throw new Error(`Companion did not show the selected Team before creation: ${JSON.stringify(teamDraft)}`);
  }
  await findCompanionSurface('companion-sidebar', `
    {
      const apply = document.querySelector('[data-action="companion-action-apply"]');
      if (!apply) throw new Error('Create Team confirmation is missing');
      apply.click();
    }
  `);
  let lastTeamCreation = null;
  let teamCreation;
  try {
    teamCreation = await waitFor('confirmed Team creation', async () => {
      const value = await findCompanionSurface('companion-sidebar');
      if (value) lastTeamCreation = value;
      return value?.actionCards === 0 && value.appliedNotice.includes('Отряд создан') ? value : null;
    }, 30000);
  } catch (error) {
    const diagnostic = lastTeamCreation ? {
      actionCards: lastTeamCreation.actionCards,
      actionApplyLabels: lastTeamCreation.actionApplyLabels,
      actionReviewOpen: lastTeamCreation.actionReviewOpen,
      actionApplyVisible: lastTeamCreation.actionApplyVisible,
      actionCardTexts: lastTeamCreation.actionCardTexts,
      appliedNotice: lastTeamCreation.appliedNotice,
      errorText: lastTeamCreation.errorText,
      text: lastTeamCreation.text?.slice(-800),
    } : null;
    throw new Error(`${error.message}; last sidebar state: ${JSON.stringify(diagnostic)}`);
  }

  const createQuestPrompt = 'Создай квест: проверь API проекта; готово, когда все тесты проходят и отчёт содержит найденные риски';
  await findCompanionSurface('companion-sidebar', `
    {
      const composerInput = document.querySelector('#companion-input');
      const composerForm = document.querySelector('#companion-form');
      if (!composerInput || !composerForm) throw new Error('Companion composer is missing');
      composerInput.value = ${JSON.stringify('Создай квест: проверь API проекта; готово, когда все тесты проходят и отчёт содержит найденные риски')};
      composerInput.dispatchEvent(new Event('input', { bubbles: true }));
      if (typeof composerForm.requestSubmit === 'function') composerForm.requestSubmit();
      else composerForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    }
  `);
  const questDraft = await waitFor('reviewable Quest draft from Companion', async () => {
    const value = await findCompanionSurface('companion-sidebar');
    return value?.assistantMessages >= 4 && !value.loading && value.questCards === 1
      && value.actionReviewOpen && value.questStartVisible && value.questStartLabels.includes('Запустить') ? value : null;
  }, 30000);
  if (!questDraft.questCardTexts.some(text => text.toLocaleLowerCase('ru-RU').includes('проверь api проекта') && text.includes('Запустить'))
      || !/Запуск — только после вашего подтверждения/.test(questDraft.text)) {
    throw new Error(`Companion did not prepare a safe reviewable Quest: ${JSON.stringify(questDraft)}`);
  }

  const before = await ide.evaluate(`(() => ({
    title: document.title,
    agentTabs: Array.from(document.querySelectorAll('.tabs-container .tab')).filter(tab => (tab.textContent || '').includes('Агенты Point')).length,
    notifications: (document.querySelector('.notifications-toasts')?.innerText || '').replace(/\\s+/g, ' ').trim(),
  }))()`);
  if (before.agentTabs !== 0) throw new Error(`Agent Hub leaked into the IDE editor before opening the Agents window: ${JSON.stringify(before)}`);

  await keyStroke(ide, 'i', 'KeyI', 73, 3); // Ctrl+Alt+I — Point Agent Hub.
  const agentsTarget = await waitFor('separate Agents window', async () => {
    const pages = (await listTargets()).filter(target => target.type === 'page' && !String(target.url).startsWith('devtools://'));
    return windowMode === 'sessions'
      ? pages.find(target => String(target.url).includes('/sessions/'))
      : pages.find(target => target.id !== ideTarget.id);
  }, 45000);
  const agents = await connect(agentsTarget);
  try {
    await waitFor('Agents workbench DOM', () => agents.evaluate(`Boolean(document.querySelector('.monaco-workbench'))`), 30000);
    const shell = await waitFor('Point Agent Hub editor', async () => {
      const value = await agents.evaluate(`(() => {
        const editor = document.querySelector('.part.editor');
        const sidebar = document.querySelector('.part.sidebar');
        const sidebarRect = sidebar?.getBoundingClientRect();
        const parts = Array.from(document.querySelectorAll('.part')).map(part => {
          const rect = part.getBoundingClientRect();
          return { classes: part.className, width: Math.round(rect.width), height: Math.round(rect.height), display: getComputedStyle(part).display, text: (part.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 240) };
        });
        return {
          title: document.title,
          rootClasses: document.querySelector('.monaco-workbench')?.className || '',
          bodyClasses: document.body?.className || '',
          titlebarChildren: Array.from(document.querySelector('.part.titlebar')?.querySelectorAll('*') || []).slice(0, 36).map(item => item.className || item.tagName),
          agentTabs: Array.from(document.querySelectorAll('.tabs-container .tab')).filter(tab => (tab.textContent || '').includes('Агенты Point')).length,
          editorWidth: Math.round(editor?.getBoundingClientRect().width || 0),
          sidebarVisible: Boolean(sidebarRect && sidebarRect.width > 40 && getComputedStyle(sidebar).display !== 'none'),
          notifications: (document.querySelector('.notifications-toasts')?.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 1200),
          parts,
        };
      })()`);
      return value.editorWidth >= 700 && !value.sidebarVisible ? value : null;
    }, 45000);
    await capture(agents, agentsScreenshot);
    const sessionsTitleBar = shell.parts.find(part => String(part.classes).includes('titlebar'));
    if (shell.editorWidth < 700
        || shell.sidebarVisible
        || /New Session|Открыть в редакторе|Open in Editor/i.test(sessionsTitleBar?.text || '')
        || /Ошибка|Error activating extension|Cannot read/i.test(shell.notifications)) {
      throw new Error(`Agents window shell is not clean: ${JSON.stringify(shell)}`);
    }

    const chatAgentMatch = chatRoundTrip.text.match(/В мире\s+(\d+)\s+агент/);
    if (!chatAgentMatch) throw new Error(`Assistant roster evidence is missing: ${JSON.stringify(chatRoundTrip.text)}`);
    const expectedHubAgentCount = Number(chatAgentMatch[1]) + 1;
    const hub = await waitFor('wide Point Agent Hub webview', async () => {
      const candidates = (await listTargets()).filter(target => target.type === 'iframe' && String(target.url).includes('extensionId=local-agent.local-agent-workbench'));
      const hubs = [];
      for (const candidate of candidates) {
        const value = await wideHubState(candidate);
        if (value) hubs.push(value);
      }
      const largest = hubs.sort((left, right) => right.width - left.width)[0];
      if (!largest || largest.width < 700 || largest.agentCount !== expectedHubAgentCount || /Открываем доску квестов|Гильдия отдыхает|Пробуждаем локальное ядро/.test(largest.text)) return null;
      return largest;
    }, 45000);
    if (!/Point Agent Hub|ГИЛЬДИЯ POINT|МАСТЕР/.test(hub.text) || hub.horizontalOverflow > 1) {
      throw new Error(`Point Agent Hub content is incomplete: ${JSON.stringify(hub)}`);
    }
    if (hub.onboarding && (hub.onboardingNextStep !== 'companion-choose'
        || hub.onboardingNextLabel !== 'Начать →'
        || hub.onboardingInlinePrimary !== 0)) {
      throw new Error(`Fresh Agent Hub skipped or duplicated Companion setup: ${JSON.stringify(hub)}`);
    }
    if (hub.agentCount !== expectedHubAgentCount) {
      throw new Error(`Assistant and Agent Hub disagree about the roster: ${JSON.stringify({ chat: chatRoundTrip.text, hub: hub.text })}`);
    }
    chatRoundTrip.agentCount = Number(chatAgentMatch[1]);

    if (!await requestWideHubFileOpen('main.go', 1)) {
      throw new Error('Wide Agent Hub could not dispatch a file navigation request.');
    }
    let lastFileNavigation;
    let fileNavigation;
    try {
      fileNavigation = await waitFor('Hub file navigation back to the IDE', async () => {
        const stateExpression = `(() => ({
          title: document.title,
          activeTab: (document.querySelector('.tabs-container .tab.active')?.textContent || '').trim(),
          tabs: Array.from(document.querySelectorAll('.tabs-container .tab')).map(tab => (tab.textContent || '').trim()),
          notifications: (document.querySelector('.notifications-toasts')?.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 800),
        }))()`;
        const ideState = await ide.evaluate(stateExpression);
        const hubState = await agents.evaluate(stateExpression);
        lastFileNavigation = { ide: ideState, hub: hubState };
        return ideState.tabs.some(tab => tab.includes('main.go'))
          && !ideState.tabs.some(tab => tab.includes('Агенты Point'))
          && !hubState.tabs.some(tab => tab.includes('main.go'))
          && hubState.tabs.some(tab => tab.includes('Агенты Point'))
          ? lastFileNavigation
          : null;
      }, 20000);
    } catch (error) {
      const targetSummary = (await listTargets()).filter(target => target.type === 'page').map(target => ({ id: target.id, title: target.title, url: target.url }));
      throw new Error(`${error.message}; last navigation state: ${JSON.stringify(lastFileNavigation)}; pages: ${JSON.stringify(targetSummary)}`);
    }

    await capture(ide, ideScreenshot);
    await capture(agents, agentsScreenshot);

    await keyStroke(ide, 'i', 'KeyI', 73, 3);
    await delay(1800);
    const pagesAfterSecondOpen = (await listTargets()).filter(target => target.type === 'page'
      && !String(target.url).startsWith('devtools://')
      && (windowMode === 'sessions' ? String(target.url).includes('/sessions/') : target.id !== ideTarget.id));
    if (pagesAfterSecondOpen.length !== 1) {
      throw new Error(`Repeated Ctrl+Alt+I created duplicate Agents windows: ${pagesAfterSecondOpen.length}`);
    }
    const ideAfter = await ide.evaluate(`(() => ({
      agentTabs: Array.from(document.querySelectorAll('.tabs-container .tab')).filter(tab => (tab.textContent || '').includes('Агенты Point')).length,
      notifications: (document.querySelector('.notifications-toasts')?.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 1200),
    }))()`);
    if (ideAfter.agentTabs !== 0 || /Ошибка|Error activating extension|Cannot read/i.test(ideAfter.notifications)) {
      throw new Error(`IDE window was polluted by the Agent Hub: ${JSON.stringify(ideAfter)}`);
    }

    process.stdout.write(JSON.stringify({ windowMode, trust, titlebar: { titlebarButtons, projectPicker, menuBar, quickActionsPicker }, statusOpen, right, duplicateDock, auxiliary, toolSurfaces, primarySidebar, gitActivity, prefillResults, chatRoundTrip, agentDraft, agentCreation, teamDraft, teamCreation, questDraft, before, shell, hub, fileNavigation, ideAfter, agentsWindows: pagesAfterSecondOpen.length, screenshots: [ideScreenshot, agentsScreenshot].filter(Boolean) }));
  } finally {
    agents.socket.close();
  }
} finally {
  ide.socket.close();
}
