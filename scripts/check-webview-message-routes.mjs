// Каждое сообщение вебвью обязано иметь ветку во внешнем разборе оболочки.
//
// Разбор в extension.js кончается `default:` — тип без ветки исчезает. Кнопка
// при этом выглядит нажатой и не делает ничего: ни ошибки, ни последствия.
// Так «СНЕСТИ КВЕСТ» доехал до выложенного приложения мёртвым: вебвью слал
// `purgeQuest`, roster-controller его ждал, а внешний switch о таком типе не
// знал и молча ронял сообщение.
//
// Проверять «есть ли маршрут хоть где-нибудь в оболочке» бесполезно — первая
// версия этой проверки именно так и ошиблась: ветка в roster-controller.js
// нашлась, и затвор отчитался «ok» на сломанной кнопке. Вход в разбор ровно
// один — `switch (message.type)` внутри handleMessage; контроллеры получают
// сообщение только из его веток. Поэтому проверяется этот switch и только он.
//
// Ни один существующий затвор этого не видел: смоуки Хаба исполняют собранный
// media/main.js с фейковым DOM и проверяют, что сообщение ОТПРАВЛЕНО, — до
// оболочки они не доходят вовсе, у неё свой процесс и свой `vscode`.
//
//   node scripts/check-webview-message-routes.mjs

import fs from 'node:fs';
import path from 'node:path';

const root = path.resolve(import.meta.dirname, '..');
const read = file => fs.readFileSync(path.join(root, file), 'utf8');
const fail = message => { console.error(message); process.exit(1); };

// ── Кто что шлёт ───────────────────────────────────────────────────────────
// Источник — клиентские модули, а не собранный media/main.js: затвор обязан
// работать в чистом клоне, где бандла ещё нет.
const clientDir = path.join(root, 'vscode-extension/ui/client');
const sent = new Map();
for (const file of fs.readdirSync(clientDir).filter(name => name.endsWith('.js'))) {
  const source = read('vscode-extension/ui/client/' + file);
  // Вычисленный тип (`type: kind`) текстом не проверить, и делать вид, что
  // проверен, нельзя: такие места сюда не попадают и остаются на смоуках.
  for (const match of source.matchAll(/postMessage\(\s*\{\s*type\s*:\s*['"]([A-Za-z0-9_]+)['"]/g)) {
    if (!sent.has(match[1])) sent.set(match[1], file);
  }
}
if (sent.size < 50) fail(`сообщений вебвью найдено ${sent.size} — проверка смотрит не туда`);

// ── Внешний разбор ─────────────────────────────────────────────────────────
// Границы метода берутся по объявлению следующего: файл — класс, методы стоят
// на двух пробелах. Считать скобки нельзя — в теле есть и строки, и шаблоны.
const hostSource = read('vscode-extension/extension.js');
const start = hostSource.indexOf('async handleMessage(message) {');
if (start < 0) fail('в extension.js не найден handleMessage — разбор переехал, проверку надо переписать');
const after = hostSource.slice(start + 1).search(/\n {2}(?:async )?[A-Za-z_][A-Za-z0-9_]*\s*\(/);
if (after < 0) fail('не найдена граница handleMessage — проверку надо переписать');
const dispatcher = hostSource.slice(start, start + 1 + after);
const routed = new Set();
for (const match of dispatcher.matchAll(/case\s+['"]([A-Za-z0-9_]+)['"]\s*:/g)) routed.add(match[1]);
if (routed.size < 50) fail(`во внешнем разборе найдено ${routed.size} веток — срез взят не тот`);

// Сообщения, которым оболочка не адресована. Каждое — с причиной: безымянное
// исключение прячет следующую мёртвую кнопку.
const notForHost = new Map([
  ['masterViewPreferences', 'перехватывается обёрткой postMessage в ui/client/main.js и до оболочки не доходит: это память раскладки чата, а не запрос к ядру'],
]);

const missing = [];
for (const [type, file] of sent) {
  if (routed.has(type) || notForHost.has(type)) continue;
  missing.push(`${type} (шлёт ui/client/${file})`);
}
if (missing.length) {
  console.error('Сообщения вебвью без ветки во внешнем разборе — кнопка нажимается и не делает ничего:');
  for (const line of missing.sort()) console.error('  - ' + line);
  process.exit(1);
}
for (const type of notForHost.keys()) {
  if (!sent.has(type)) fail(`исключение ${type} больше не отправляется — уберите его из списка`);
  if (routed.has(type)) fail(`исключение ${type} на самом деле разбирается — уберите его из списка`);
}

// ── Обратная сторона: ветка контроллера, до которой не доходит сообщение ────
// Контроллер получает сообщение только из внешнего switch. Ветка, которой там
// нет, — мёртвый код, и выглядит он как рабочий маршрут: на нём и ошиблась
// первая версия этой проверки.
const unreachable = [];
for (const file of fs.readdirSync(path.join(root, 'vscode-extension')).filter(name => name.endsWith('-controller.js'))) {
  const source = read('vscode-extension/' + file);
  for (const match of source.matchAll(/case\s+['"]([A-Za-z0-9_]+)['"]\s*:/g)) {
    const type = match[1];
    if (!sent.has(type) || routed.has(type)) continue;
    unreachable.push(`${type} (ждёт ${file})`);
  }
}
if (unreachable.length) {
  console.error('Ветки контроллеров, до которых сообщение не доходит — внешний разбор их не пропускает:');
  for (const line of [...new Set(unreachable)].sort()) console.error('  - ' + line);
  process.exit(1);
}

console.log(JSON.stringify({
  webviewMessageRoutes: 'ok',
  sent: sent.size,
  dispatched: routed.size,
  exempt: notForHost.size,
}));
