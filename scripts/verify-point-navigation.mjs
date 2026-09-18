import fs from 'node:fs';

const endpoint = process.argv[2];
const screenshotPrefix = process.argv[3] || '';
if (!endpoint) throw new Error('Usage: node verify-point-navigation.mjs <endpoint> [screenshot-prefix]');

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
const pointSearchTitle = 'Point — поиск везде (Double Shift / Ctrl+N)';
const visibleQuickPickExpression = `Array.from(document.querySelectorAll('.quick-input-widget')).find(candidate => {
  const style = getComputedStyle(candidate);
  return style.display !== 'none' && style.visibility !== 'hidden';
})`;

async function keyStroke(key, code, windowsVirtualKeyCode, modifiers = 0) {
  const event = { key, code, windowsVirtualKeyCode, nativeVirtualKeyCode: windowsVirtualKeyCode, modifiers };
  await command('Input.dispatchKeyEvent', { type: 'rawKeyDown', ...event });
  await command('Input.dispatchKeyEvent', { type: 'keyUp', ...event });
}

async function typeText(value) {
  // A newly created QuickPick can be visible one frame before Electron gives
  // its input focus. Input.insertText then goes to the editor and the picker
  // stays empty, which made the navigation test intermittently blame search.
  await evaluate(`(() => { const input = (${visibleQuickPickExpression})?.querySelector('input'); input?.focus(); return Boolean(input); })()`);
  await command('Input.insertText', { text: value });
}

async function evaluate(expression) {
  const result = await command('Runtime.evaluate', { expression, returnByValue: true });
  return result.result?.value;
}

async function waitFor(expression, description, timeout = 10000) {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    const value = await evaluate(expression);
    if (value) return value;
    await delay(150);
  }
  const diagnostic = await evaluate(`(() => {
    const widget = ${visibleQuickPickExpression};
    return {
      title: widget?.querySelector('.quick-input-title')?.textContent?.trim() ?? '',
      placeholder: widget?.querySelector('input')?.getAttribute('placeholder') ?? '',
      value: widget?.querySelector('input')?.value ?? '',
      text: (widget?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1200),
      activeTab: document.querySelector('.tab.active .label-name')?.textContent?.trim() ?? '',
      documentTitle: document.title,
      project: document.querySelector('.point-project-switcher-label')?.textContent?.trim() ?? '',
      sidebar: (document.querySelector('.part.sidebar')?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 900),
      contextMenus: Array.from(document.querySelectorAll('.context-view .monaco-menu-container')).map(menu => ({
        display: getComputedStyle(menu).display,
        visibility: getComputedStyle(menu).visibility,
        text: (menu.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1200),
      })),
    };
  })()`);
  throw new Error(`Timed out waiting for ${description}: ${JSON.stringify(diagnostic)}`);
}

async function capture(suffix) {
  if (!screenshotPrefix) return '';
  await command('Page.enable');
  const screenshot = await command('Page.captureScreenshot', { format: 'png', fromSurface: true });
  const targetPath = `${screenshotPrefix}-${suffix}.png`;
  fs.writeFileSync(targetPath, Buffer.from(screenshot.data, 'base64'));
  return targetPath;
}

async function closeQuickPick(expectedTitle = '') {
  for (let attempt = 0; attempt < 3; attempt += 1) {
    await keyStroke('Escape', 'Escape', 27);
    await delay(250);
    const closed = await evaluate(`(() => {
      const widget = Array.from(document.querySelectorAll('.quick-input-widget')).find(candidate => {
        const style = getComputedStyle(candidate);
        return style.display !== 'none' && style.visibility !== 'hidden';
      });
      if (!widget) return true;
      const title = widget.querySelector('.quick-input-title')?.textContent?.trim() ?? '';
      return ${JSON.stringify(expectedTitle)} && title !== ${JSON.stringify(expectedTitle)};
    })()`);
    if (closed) return;
  }
  const clicked = await evaluate(`(() => {
    const widget = Array.from(document.querySelectorAll('.quick-input-widget')).find(candidate => {
      const style = getComputedStyle(candidate);
      return style.display !== 'none' && style.visibility !== 'hidden';
    });
    const button = widget?.querySelector('[aria-label*="Закрыть"], [aria-label*="Close"], .quick-input-close');
    button?.click();
    return Boolean(button);
  })()`);
  await delay(300);
  if (!clicked) throw new Error(`Could not close quick pick: ${expectedTitle}`);
}

async function clickEditorToken(lineNeedle, token) {
  const point = await evaluate(`(() => {
    const normalize = value => (value ?? '').replaceAll('\u00a0', ' ');
    const line = Array.from(document.querySelectorAll('.monaco-editor .view-line')).find(candidate => normalize(candidate.textContent).includes(${JSON.stringify(lineNeedle)}));
    if (!line) return null;
    const walker = document.createTreeWalker(line, NodeFilter.SHOW_TEXT);
    let node;
    while ((node = walker.nextNode())) {
      const index = normalize(node.textContent).indexOf(${JSON.stringify(token)});
      if (index < 0) continue;
      const range = document.createRange();
      range.setStart(node, Math.min(index + 1, node.textContent.length - 1));
      range.setEnd(node, Math.min(index + 2, node.textContent.length));
      const rect = range.getBoundingClientRect();
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
    }
    return null;
  })()`);
  if (!point) throw new Error(`Could not locate ${token} in editor line: ${lineNeedle}`);
  await command('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
  await command('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
  await delay(250);
}

async function verifyUsages(kind, lineNeedle, token) {
  await clickEditorToken(lineNeedle, token);
  await keyStroke('F7', 'F7', 118, 1); // Alt+F7
  await waitFor(`document.querySelector('.part.sidebar')?.innerText?.toLowerCase().includes('использования')`, `${kind} usages tool window`, 12000);
  await delay(900);
  const state = await evaluate(`(() => {
    const sidebar = document.querySelector('.part.sidebar');
    return {
      title: sidebar?.querySelector('.title-label h2')?.textContent?.trim() ?? '',
      text: (sidebar?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1800),
    };
  })()`);
  if (!state.text.toLowerCase().includes('использования') || !state.text.includes('model.ts') || !/Результатов:\s*[1-9]/i.test(state.text)) {
    throw new Error(`Point ${kind} usages verification failed: ${JSON.stringify(state)}`);
  }
  return state;
}

try {
  await command('Runtime.enable');
  await keyStroke('N', 'KeyN', 78, 2 | 8); // Ctrl+Shift+N — file search
  await waitFor(`(${visibleQuickPickExpression})?.querySelector('input')`, 'file search');
  const fileSearch = await evaluate(`(() => {
    const widget = ${visibleQuickPickExpression};
    return {
      title: widget?.querySelector('.quick-input-title')?.textContent?.trim() ?? '',
      placeholder: widget?.querySelector('input')?.getAttribute('placeholder') ?? '',
    };
  })()`);
  if (!fileSearch.placeholder.includes('Поиск файлов по имени')) {
    throw new Error(`Ctrl+Shift+N did not open file search: ${JSON.stringify(fileSearch)}`);
  }
  await closeQuickPick();

  await keyStroke('n', 'KeyN', 78, 2); // Ctrl+N — Point search everywhere
  await waitFor(`(${visibleQuickPickExpression})?.querySelector('.quick-input-title')?.textContent?.trim() === ${JSON.stringify(pointSearchTitle)}`, 'Point file search');
  await typeText('model');
  await waitFor(`Array.from((${visibleQuickPickExpression})?.querySelectorAll('.monaco-list-row') ?? []).some(row => row.innerText?.includes('model.ts'))`, 'model.ts Point search result');
  const opened = await evaluate(`(() => {
    const widget = ${visibleQuickPickExpression};
    const row = Array.from(widget?.querySelectorAll('.monaco-list-row') ?? []).find(candidate => candidate.innerText?.includes('model.ts'));
    if (!row) return false;
    row.click();
    return true;
  })()`);
  if (!opened) throw new Error(`Point search did not expose model.ts: ${JSON.stringify(fileSearch)}`);
  await waitFor(`document.querySelector('.tab.active .label-name')?.textContent?.includes('model.ts')`, 'model.ts editor');
  await delay(1800);

  await keyStroke('n', 'KeyN', 78, 2); // Ctrl+N — Point search everywhere
  await waitFor(`(${visibleQuickPickExpression})?.querySelector('.quick-input-title')?.textContent?.trim() === ${JSON.stringify(pointSearchTitle)}`, 'Point search everywhere');
  await typeText('Greeter');
  await delay(1800);
  const search = await evaluate(`(() => {
    const widget = ${visibleQuickPickExpression};
    return {
      title: widget?.querySelector('.quick-input-title')?.textContent?.trim() ?? '',
      placeholder: widget?.querySelector('input')?.getAttribute('placeholder') ?? '',
      text: (widget?.innerText ?? '').replace(/\\s+/g, ' ').trim(),
      rows: Array.from(widget?.querySelectorAll('.monaco-list-row') ?? []).map(row => (row.innerText ?? '').replace(/\\s+/g, ' ').trim()),
    };
  })()`);
  if (search.title !== pointSearchTitle || !search.placeholder.includes('#символы') || !search.placeholder.includes('/файлы')
      || !search.text.includes('Искать текст «Greeter»') || !search.text.includes('Greeter')) {
    throw new Error(`Point search verification failed: ${JSON.stringify(search)}`);
  }
  const searchScreenshot = await capture('search-everywhere');
  await closeQuickPick(pointSearchTitle);

  const usagesByKind = {
    interface: await verifyUsages('interface', 'interface Greeter', 'Greeter'),
    class: await verifyUsages('class', 'class FriendlyGreeter', 'FriendlyGreeter'),
    variable: await verifyUsages('variable', 'const greeter', 'greeter'),
  };
  const usages = usagesByKind.variable;
  const usagesScreenshot = await capture('usages');

  await keyStroke('A', 'KeyA', 65, 2 | 8); // Ctrl+Shift+A — actions
  await waitFor(`(${visibleQuickPickExpression})?.querySelector('input')`, 'action search');
  await typeText('Point Поддержка языков');
  await delay(600);
  await keyStroke('Enter', 'Enter', 13);
  await waitFor(`(${visibleQuickPickExpression})?.querySelector('.quick-input-title')?.textContent?.trim() === 'Point — поддержка языков'`, 'language support');
  const languages = await evaluate(`(() => {
    const widget = ${visibleQuickPickExpression};
    return {
      title: widget?.querySelector('.quick-input-title')?.textContent?.trim() ?? '',
      placeholder: widget?.querySelector('input')?.getAttribute('placeholder') ?? '',
      text: (widget?.innerText ?? '').replace(/\\s+/g, ' ').trim(),
    };
  })()`);
  for (const language of ['Go', 'Python', 'Java', 'Rust', 'C / C++']) {
    if (!languages.text.includes(language)) throw new Error(`Language profile is missing from UI: ${language}`);
  }
  if (!languages.placeholder.includes('только для открытого языка')) {
    throw new Error(`Language support explanation is missing: ${JSON.stringify(languages)}`);
  }
  const languagesScreenshot = await capture('languages');
  await closeQuickPick('Point — поддержка языков');

  const folderPoint = await evaluate(`(() => {
    document.querySelector('.point-activitybar .codicon-explorer-view-icon')?.click();
    return true;
  })()`);
  if (!folderPoint) throw new Error('Files tool window is missing.');
  await delay(800);
  const pythonFolder = await evaluate(`(() => {
    const row = Array.from(document.querySelectorAll('.part.sidebar .monaco-list-row')).find(candidate => candidate.innerText?.replace(/\\s+/g, ' ').trim() === 'python');
    const rect = row?.getBoundingClientRect();
    return rect ? { x: Math.min(rect.right - 16, rect.left + 110), y: rect.top + rect.height / 2 } : null;
  })()`);
  if (!pythonFolder) throw new Error('Python folder row is missing from Files.');
  await command('Input.dispatchMouseEvent', { type: 'mousePressed', x: pythonFolder.x, y: pythonFolder.y, button: 'left', buttons: 1, clickCount: 1 });
  await command('Input.dispatchMouseEvent', { type: 'mouseReleased', x: pythonFolder.x, y: pythonFolder.y, button: 'left', buttons: 0, clickCount: 1 });
  await waitFor(`(() => {
    const row = Array.from(document.querySelectorAll('.part.sidebar .monaco-list-row')).find(candidate => candidate.innerText?.replace(/\\s+/g, ' ').trim() === 'python');
    return Boolean(row && (row.getAttribute('aria-selected') === 'true' || row.classList.contains('selected')) && row.closest('.monaco-list')?.contains(document.activeElement));
  })()`, 'selected Python folder in Files');
  // CDP does not reliably deliver Alt-modified shortcuts into a hidden
  // Electron window. Open a file from the selected folder, then invoke the
  // same Point command through the palette; the Explorer binding is verified
  // statically in the extension manifest.
  const editorPoint = await evaluate(`(() => {
    const rect = document.querySelector('.part.editor .monaco-editor')?.getBoundingClientRect();
    return rect ? { x: rect.left + Math.min(180, rect.width / 2), y: rect.top + Math.min(180, rect.height / 2) } : null;
  })()`);
  if (!editorPoint) throw new Error('Code editor is missing before Python navigation.');
  await command('Input.dispatchMouseEvent', { type: 'mousePressed', x: editorPoint.x, y: editorPoint.y, button: 'left', buttons: 1, clickCount: 1 });
  await command('Input.dispatchMouseEvent', { type: 'mouseReleased', x: editorPoint.x, y: editorPoint.y, button: 'left', buttons: 0, clickCount: 1 });
  await keyStroke('N', 'KeyN', 78, 2 | 8);
  await waitFor(`(${visibleQuickPickExpression})?.querySelector('input')`, 'file picker for Python file');
  await typeText('service.py');
  await delay(350);
  await keyStroke('Enter', 'Enter', 13);
  await waitFor(`document.querySelector('.tab.active .label-name')?.textContent?.trim() === 'service.py'`, 'Python file editor');
  await keyStroke('P', 'KeyP', 80, 2 | 8);
  await waitFor(`(${visibleQuickPickExpression})?.querySelector('input')`, 'command palette for Find in Folder');
  await typeText('Point Найти в папке');
  await delay(350);
  await keyStroke('Enter', 'Enter', 13);
  await waitFor(`!!document.querySelector('.search-editor')`, 'Find in Folder search editor');
  const folderSearch = await evaluate(`(() => {
    const editor = document.querySelector('.search-editor');
    return {
      title: document.querySelector('.tab.active .label-name')?.textContent?.trim() ?? '',
      text: (editor?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1600),
      inputs: Array.from(editor?.querySelectorAll('input, textarea') ?? []).map(input => ({ value: input.value, placeholder: input.getAttribute('placeholder') ?? '' })),
    };
  })()`);
  if (!folderSearch.inputs.some(input => /python/i.test(input.value)) && !/python/i.test(folderSearch.text)) {
    throw new Error(`Find in Folder did not preserve the selected path: ${JSON.stringify(folderSearch)}`);
  }
  const folderScreenshot = await capture('folder-search');
  process.stdout.write(JSON.stringify({ search, usages, usagesByKind, languages, folderSearch, screenshots: [searchScreenshot, usagesScreenshot, languagesScreenshot, folderScreenshot] }));
} finally {
  socket.close();
}
