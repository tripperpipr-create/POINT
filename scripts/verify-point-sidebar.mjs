const endpoint = process.argv[2];
if (!endpoint) throw new Error('Usage: node verify-point-sidebar.mjs <endpoint>');
const requestedSection = process.argv[3];

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
const allSections = [
  { name: 'files', selector: '.point-activitybar .codicon-explorer-view-icon', wait: 700 },
  { name: 'versions', selector: '.point-activitybar .codicon-source-control-view-icon', wait: 5000 },
  { name: 'ai', selector: '.point-activitybar .action-label.uri-icon', wait: 2500 },
];
const sections = requestedSection ? allSections.filter(section => section.name === requestedSection) : allSections;
if (!sections.length) throw new Error(`Unknown Point section: ${requestedSection}`);

try {
  await command('Runtime.enable');
  const results = [];
  for (const section of sections) {
    const activation = await command('Runtime.evaluate', {
      expression: `(() => {
        const element = document.querySelector(${JSON.stringify(section.selector)});
        if (!element) return { found: false, alreadyOpen: false, clicked: false };
        const item = element.closest('.action-item');
        const sidebar = document.querySelector('.part.sidebar');
        const rect = sidebar?.getBoundingClientRect();
        const alreadyOpen = item?.getAttribute('aria-selected') === 'true' && Boolean(rect && rect.width > 40);
        if (!alreadyOpen) element.click();
        return { found: true, alreadyOpen, clicked: !alreadyOpen };
      })()`,
      returnByValue: true,
    });
    await delay(section.wait);
    const state = await command('Runtime.evaluate', {
      expression: `(() => {
        const sidebar = document.querySelector('.part.sidebar');
        const selected = document.querySelector('.point-activitybar .action-item[aria-selected="true"] .action-label');
        const rect = sidebar?.getBoundingClientRect();
        const sidebarText = (sidebar?.innerText ?? '').replace(/\\s+/g, ' ').trim().slice(0, 1200);
        return {
          selected: selected?.getAttribute('aria-label') ?? null,
          sidebarWidth: rect ? Math.round(rect.width) : 0,
          sidebarDisplay: sidebar ? getComputedStyle(sidebar).display : null,
          sidebarText,
          treeRows: [...new Set(Array.from(sidebar?.querySelectorAll('.explorer-viewlet .monaco-list-row .label-name, .explorer-viewlet .monaco-list-row .monaco-icon-label') ?? []).map(element => element.textContent?.trim()).filter(Boolean))].slice(0, 40),
          upstreamBrandMentions: sidebarText.match(/VS Code|Visual Studio Code|Code - OSS/gi) ?? [],
          webviewCount: document.querySelectorAll('webview, iframe.webview').length,
        };
      })()`,
      returnByValue: true,
    });
    const value = { section: section.name, activation: activation.result?.value, ...state.result?.value };
    const expectedLabels = { files: 'Инвентарь', versions: 'Летопись', ai: 'Гильдия' };
    const selectedMatches = section.name === 'ai' ? value.selected === expectedLabels[section.name] : value.selected?.startsWith(expectedLabels[section.name]);
    if (!value.activation?.found || !selectedMatches || value.sidebarWidth < 240 || value.sidebarDisplay === 'none' || value.upstreamBrandMentions.length) {
      throw new Error(`Point sidebar section failed: ${JSON.stringify(value)}`);
    }
    if (section.name === 'files') {
      // VS Code may expand a single-folder workspace directly under the localized
      // "Project" heading, so the root folder label itself is not a stable row.
      // Verify the actual fixture inventory instead of coupling the gate to that
      // presentation setting.
      const requiredFiles = ['main.go', 'main_test.go', 'README.md', 'go.mod'];
      if (!requiredFiles.every(name => value.treeRows.includes(name))) {
        throw new Error(`Point files tree is incomplete: ${JSON.stringify(value)}`);
      }
    }
    if (section.name === 'versions' && (!value.sidebarText.toLocaleLowerCase('ru').includes('изменения') || !value.sidebarText.includes('main.go') || !value.sidebarText.includes('internal') || !value.sidebarText.includes('Сообщение коммита · CTRL+Enter') || value.sidebarText.toLocaleLowerCase('ru').includes('нет элементов журнала системы управления версиями'))) {
      throw new Error(`Point versions surface is not focused on changes: ${JSON.stringify(value)}`);
    }
    results.push(value);
  }
  process.stdout.write(JSON.stringify(results));
} finally {
  socket.close();
}
