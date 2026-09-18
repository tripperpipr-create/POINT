// Замер переключения мира в Чертоге.
//
// От клика в галерее до первого кадра нового мира. Цифра, ради которой всё это
// делалось, иначе не проверяется ничем: смоук видит разметку, но не видит, что
// ядро поднималось четыре секунды, а человек всё это время смотрел на скелет.
//
// Прогоны идут парой A → B → A: первые два холодные (ядра ещё нет), с третьего
// прошлое ядро уже тёплое, и его подхватывает `tryAttachSharedCore` одной
// проверкой здоровья. Поэтому в отчёте две цифры, а не одна средняя.
//
//   Point.exe --remote-debugging-port=9222
//   node scripts/measure-point-project-switch.mjs http://127.0.0.1:9222 C:\worlds\a C:\worlds\b 7
//
// Бюджет — distribution/performance-slo.json: desktop.projectSwitchWarmMs и
// desktop.projectSwitchColdMs.

import fs from 'node:fs';
import path from 'node:path';

const endpoint = process.argv[2];
const first = process.argv[3];
const second = process.argv[4];
const samples = Number(process.argv[5] || 7);
if (!endpoint || !first || !second) {
  throw new Error('Usage: node measure-point-project-switch.mjs <cdp-endpoint> <projectA> <projectB> [samples]');
}

const root = path.resolve(import.meta.dirname, '..');
const slo = JSON.parse(fs.readFileSync(path.join(root, 'distribution', 'performance-slo.json'), 'utf8')).desktop;
const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));

async function waitFor(description, probe, timeout = 30000, interval = 40) {
  const deadline = Date.now() + timeout;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const value = await probe();
      if (value) return value;
    } catch (error) {
      lastError = error;
    }
    await delay(interval);
  }
  throw new Error(`Не дождались: ${description}${lastError ? ` (${lastError.message})` : ''}`);
}

async function connect(target) {
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('CDP не открыл сокет за 5 с')), 5000);
    socket.addEventListener('open', () => { clearTimeout(timer); resolve(); }, { once: true });
    socket.addEventListener('error', () => { clearTimeout(timer); reject(new Error('CDP сокет отказал')); }, { once: true });
  });
  let sequence = 0;
  const pending = new Map();
  socket.addEventListener('message', event => {
    const message = JSON.parse(String(event.data));
    if (!message.id || !pending.has(message.id)) return;
    const handlers = pending.get(message.id);
    pending.delete(message.id);
    clearTimeout(handlers.timer);
    if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
  });
  const command = (method, params = {}) => {
    const id = ++sequence;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP ${method} молчит`)); }, 8000);
      pending.set(id, { resolve, reject, timer });
      socket.send(JSON.stringify({ id, method, params }));
    });
  };
  await command('Runtime.enable');
  await command('Page.enable');
  return { socket, command };
}

// Чертог живёт в вебвью внутри вложенных кадров, и обычный Runtime.evaluate
// попадает не туда. Обходим кадры с конца: нужный — самый глубокий из тех, где
// разметка галереи вообще есть.
async function inHub(client, expression) {
  const tree = await client.command('Page.getFrameTree');
  const frames = [];
  const collect = node => { frames.push(node.frame); for (const child of node.childFrames || []) collect(child); };
  collect(tree.frameTree);
  for (const frame of frames.reverse()) {
    try {
      const world = await client.command('Page.createIsolatedWorld', { frameId: frame.id, worldName: `point-switch-${Date.now()}` });
      const response = await client.command('Runtime.evaluate', {
        contextId: world.executionContextId,
        expression,
        returnByValue: true,
      });
      if (response.result?.value != null && response.result.value !== false) return response.result.value;
    } catch {
      // Вебвью мог переехать между обходом кадров и вызовом — пробуем следующий.
    }
  }
  return null;
}

const nameOf = fsPath => path.basename(fsPath);
const quote = value => JSON.stringify(value);

async function openGallery(client) {
  await inHub(client, `(() => {
    const chip = document.querySelector('[data-action="gallery-toggle"]');
    if (!chip) return document.querySelector('.point-gallery') ? 'already' : false;
    if (!document.querySelector('.point-gallery')) chip.click();
    return 'opened';
  })()`);
  await waitFor('галерея миров', () => inHub(client, `Boolean(document.querySelector('.point-gallery'))`));
}

async function switchTo(client, target) {
  await openGallery(client);
  await waitFor(`карточка мира ${nameOf(target)}`, () => inHub(client,
    `Boolean([...document.querySelectorAll('[data-action="gallery-open"]')].find(node => node.dataset.path === ${quote(target)}))`));
  const startedAt = Date.now();
  await inHub(client, `(() => {
    const card = [...document.querySelectorAll('[data-action="gallery-open"]')].find(node => node.dataset.path === ${quote(target)});
    if (!card) return false;
    card.click();
    return true;
  })()`);
  // Готово = скелет ушёл И шапка называет новый мир. Одного скелета мало:
  // он снимается ещё до того, как придёт состояние поднятого ядра.
  await waitFor(`мир ${nameOf(target)} на экране`, () => inHub(client, `(() => {
    if (document.querySelector('.point-gallery-switch')) return false;
    if (document.querySelector('.point-gallery')) return false;
    const chip = document.querySelector('.point-gallery-chip b');
    return chip && chip.textContent.trim() === ${quote(nameOf(target))};
  })()`), 30000);
  return Date.now() - startedAt;
}

const percentile = (values, share) => {
  const sorted = [...values].sort((left, right) => left - right);
  return sorted[Math.min(sorted.length - 1, Math.floor(share * sorted.length))];
};

const list = await fetch(`${endpoint}/json/list`, { signal: AbortSignal.timeout(5000) }).then(response => response.json());
const page = list.find(candidate => candidate.type === 'page' && !String(candidate.url).startsWith('devtools://'));
if (!page) throw new Error('Не нашли окно Point на этом CDP-порту');
const client = await connect(page);

const cold = [];
const warm = [];
for (let index = 0; index < samples; index += 1) {
  const target = index % 2 === 0 ? second : first;
  const elapsed = await switchTo(client, target);
  // Первые два перехода поднимают оба ядра с нуля; дальше прошлое уже тёплое.
  (index < 2 ? cold : warm).push(elapsed);
  await delay(400);
}
client.socket.close();

const report = {
  samples,
  cold: { runs: cold, p95: percentile(cold, 0.95), budgetMs: slo.projectSwitchColdMs },
  warm: { runs: warm, p50: percentile(warm, 0.5), p95: percentile(warm, 0.95), budgetMs: slo.projectSwitchWarmMs },
};
report.withinBudget = report.cold.p95 <= slo.projectSwitchColdMs && report.warm.p95 <= slo.projectSwitchWarmMs;
process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
if (!report.withinBudget) process.exitCode = 1;
