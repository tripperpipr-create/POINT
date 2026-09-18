import fs from 'node:fs';

const endpoint = process.argv[2];
const output = process.argv[3];
if (!endpoint || !output) throw new Error('Usage: node capture-cdp-page.mjs <endpoint> <output.png>');

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
  const { resolve, reject } = pending.get(message.id);
  pending.delete(message.id);
  if (message.error) reject(new Error(message.error.message)); else resolve(message.result);
});

function command(method, params = {}) {
  const id = ++sequence;
  socket.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
}

try {
  await command('Page.enable');
  await command('Runtime.enable');
  const screenshot = await command('Page.captureScreenshot', { format: 'png', fromSurface: true });
  fs.writeFileSync(output, Buffer.from(screenshot.data, 'base64'));
  const visible = await command('Runtime.evaluate', { expression: 'document.body?.innerText ?? ""', returnByValue: true });
  const appIcon = await command('Runtime.evaluate', {
    expression: `(() => {
      const element = document.querySelector('.window-appicon');
      return element ? { html: element.outerHTML, backgroundImage: getComputedStyle(element).backgroundImage } : null;
    })()`,
    returnByValue: true
  });
  const layout = await command('Runtime.evaluate', {
    expression: `(() => Array.from(document.querySelectorAll('.monaco-workbench .part, .editor-group-container, .gettingStartedContainer, .gettingStarted, .gettingStartedSlide, .categoriesScrollbar, .point-native-page, .point-native-hero, .point-native-bottom')).map(element => {
      const rect = element.getBoundingClientRect();
      const style = getComputedStyle(element);
      return { className: element.className, display: style.display, visibility: style.visibility, width: Math.round(rect.width), height: Math.round(rect.height), x: Math.round(rect.x), y: Math.round(rect.y) };
    }))()`,
    returnByValue: true
  });
  const verification = await command('Runtime.evaluate', {
    expression: `(() => {
      const heading = document.querySelector('.point-native-copy h1');
      const project = document.querySelector('.point-native-project');
      const primary = document.querySelector('.point-native-button.primary');
      const rect = element => element ? element.getBoundingClientRect() : null;
      const headingRect = rect(heading);
      const projectRect = rect(project);
      return {
        nativePointHome: Boolean(document.querySelector('.point-native-home')),
        webviewCount: document.querySelectorAll('webview, iframe.webview').length,
        accountActions: Array.from(document.querySelectorAll('.activitybar .action-label')).map(element => element.getAttribute('aria-label') || element.getAttribute('title')).filter(Boolean).filter(label => /account|аккаунт|уч[её]тн/i.test(label)),
        activityItems: Array.from(document.querySelectorAll('.activitybar .action-item')).map(element => ({
          className: element.className,
          id: element.getAttribute('id'),
          dataId: element.getAttribute('data-id'),
          display: getComputedStyle(element).display,
          label: element.querySelector('.action-label')?.getAttribute('aria-label') || element.querySelector('.action-label')?.getAttribute('title'),
          pseudoLabelFont: (() => {
            const label = element.querySelector('.action-label');
            return label ? getComputedStyle(label, '::after').fontFamily : null;
          })(),
          html: element.outerHTML.slice(0, 1200),
        })),
        activitybarClassName: document.querySelector('.part.activitybar')?.className ?? null,
        activitybarBrandBackground: (() => {
          const content = document.querySelector('.point-activitybar .content');
          return content ? getComputedStyle(content, '::before').backgroundImage : null;
        })(),
        titlebarActions: Array.from(document.querySelectorAll('.part.titlebar button, .part.titlebar a, .part.titlebar .action-item')).map(element => ({
          className: element.className,
          display: getComputedStyle(element).display,
          text: (element.textContent ?? '').trim(),
          label: element.getAttribute('aria-label') || element.getAttribute('title'),
          html: element.outerHTML.slice(0, 900),
        })).filter(item => item.display !== 'none'),
        watermark: (() => {
          const container = document.querySelector('.editor-group-watermark .watermark-container');
          const letterpress = container?.querySelector('.letterpress');
          return container ? {
            text: getComputedStyle(container, '::after').content,
            image: letterpress ? getComputedStyle(letterpress).backgroundImage : null,
            imageWidth: letterpress ? Math.round(letterpress.getBoundingClientRect().width) : 0,
          } : null;
        })(),
        headingProjectOverlap: Boolean(headingRect && projectRect && headingRect.right > projectRect.left && headingRect.bottom > projectRect.top && headingRect.top < projectRect.bottom),
        headingRect,
        projectRect,
        primaryOuterHtml: primary?.outerHTML ?? null,
        primaryBackground: primary ? getComputedStyle(primary).background : null,
        primaryBackgroundImage: primary ? getComputedStyle(primary).backgroundImage : null,
      };
    })()`,
    returnByValue: true
  });
  process.stdout.write(JSON.stringify({ title: target.title, url: target.url, text: String(visible.result?.value ?? '').slice(0, 4000), appIcon: appIcon.result?.value ?? null, layout: layout.result?.value ?? [], verification: verification.result?.value ?? null, output }));
} finally {
  socket.close();
}
