// Сверка палитры оболочки с единственным источником истины.
//
// `docs/RPG-DESIGN-SYSTEM.md` объявляет `vscode-extension/ui/tokens.css`
// единственным источником истины для цвета. Но оверлей оболочки
// (`distribution/resources/point-*.css`) правит чужую разметку Code-OSS, его
// собирает другой конвейер, и `ui/tokens.css` он не читает: значения там
// вписаны руками. Сейчас они совпадают — но связи нет, и разойтись они могут
// молча, как уже расходились однажды (PROJECT-STATUS, §3.6 «дрейф палитры»).
//
// Этот скрипт и есть связь: не общий файл, которого архитектура не позволяет,
// а проверка, которая падает при первом расхождении.
//
//   node scripts/check-shell-tokens.mjs

import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';

const root = path.join(path.dirname(new URL(import.meta.url).pathname.slice(1)), '..');
const shellDir = path.join(root, 'distribution', 'resources');
const shellFile = path.join(shellDir, 'point-workbench.css');
const tokensFile = path.join(root, 'vscode-extension', 'ui', 'tokens.css');

// Токен оболочки → откуда берётся его значение в ui/tokens.css.
// Для мостовых токенов сверяется фолбэк: в оболочке цвет темы подставляет сам
// Code-OSS, а в вебвью — var(--vscode-…, фолбэк). Совпадать должен фолбэк.
const MAP = {
  '--point-void': { token: '--ui-void', kind: 'fallback' },
  '--point-panel': { token: '--ui-panel', kind: 'fallback' },
  '--point-raised': { token: '--ui-raised', kind: 'fallback' },
  '--point-selected': { token: '--ui-selected', kind: 'fallback' },
  '--point-border': { token: '--ui-border', kind: 'fallback' },
  '--point-primary': { token: '--ui-text', kind: 'fallback' },
  '--point-muted': { token: '--ui-muted', kind: 'fallback' },
  '--point-ember': { token: '--ember', kind: 'literal' },
  '--point-ember-hover': { token: '--ember-hover', kind: 'literal' },
  '--point-ember-pressed': { token: '--ember-pressed', kind: 'literal' },
  '--point-mana': { token: '--mana', kind: 'literal' },
  '--point-vital': { token: '--vital', kind: 'literal' },
  '--point-wound': { token: '--wound', kind: 'literal' },
};

const readDeclarations = source => {
  const found = new Map();
  const pattern = /(--[\w-]+)\s*:\s*([^;}]+)[;}]/g;
  let match;
  while ((match = pattern.exec(source))) {
    if (!found.has(match[1])) found.set(match[1], match[2].trim());
  }
  return found;
};

const normalise = value => {
  const trimmed = String(value).trim();
  const fallback = /^var\(\s*--[\w-]+\s*,\s*(.+)\)$/.exec(trimmed);
  const colour = (fallback ? fallback[1] : trimmed).trim();
  return colour.toLowerCase();
};

const shellSource = readFileSync(shellFile, 'utf8');
const shellTokens = readDeclarations(shellSource);
const uiTokens = readDeclarations(readFileSync(tokensFile, 'utf8'));

const problems = [];

for (const [shellName, source] of Object.entries(MAP)) {
  const shellValue = shellTokens.get(shellName);
  if (shellValue === undefined) {
    problems.push(`${shellName} исчез из point-workbench.css — сверять нечего`);
    continue;
  }
  const uiValue = uiTokens.get(source.token);
  if (uiValue === undefined) {
    problems.push(`${source.token} исчез из ui/tokens.css — ${shellName} сверять не с чем`);
    continue;
  }
  const expected = normalise(uiValue);
  const actual = normalise(shellValue);
  if (expected !== actual) {
    problems.push(`${shellName} = ${actual}, а ${source.token} даёт ${expected}`);
  }
}

// Вторая копия того же токена в соседнем файле оверлея — то, с чего разъезд и
// начинается: правят один файл, второй остаётся с прежним значением.
for (const name of readdirSync(shellDir)) {
  if (!name.endsWith('.css') || name === 'point-workbench.css') continue;
  const declared = readDeclarations(readFileSync(path.join(shellDir, name), 'utf8'));
  for (const token of declared.keys()) {
    if (token.startsWith('--point-') && shellTokens.has(token)) {
      problems.push(`${name} повторно объявляет ${token} — он уже задан в point-workbench.css и приходит наследованием`);
    }
  }
}

// Одна поверхность — одно правило. Стиль оболочки уже однажды нарос слоями:
// вкладку задавали три блока из разных лет, побеждал нижний, и узнать это
// можно было только замером в живом окне. Повтор селектора — первый признак,
// что слой начал расти снова.
const workbenchCss = readFileSync(path.join(shellDir, 'point-workbench.css'), 'utf8');
// Медиазапросы пропускаем: там правило и обязано повторять селектор.
const outsideMedia = workbenchCss.replace(/@media[^{]*\{(?:[^{}]|\{[^{}]*\})*\}/g, '');
const painted = new Map();
for (const match of outsideMedia.matchAll(/([^{}]+)\{[^{}]*\}/g)) {
  const selector = match[1].replace(/\/\*[\s\S]*?\*\//g, ' ').split(/\s+/).join(' ').trim();
  if (!selector || selector.startsWith('@') || selector.endsWith('%')) continue;
  painted.set(selector, (painted.get(selector) ?? 0) + 1);
}
for (const [selector, count] of painted) {
  if (count > 1) problems.push(`point-workbench.css красит «${selector.slice(0, 70)}» ${count} раза — одна поверхность описывается один раз`);
}

if (problems.length) {
  console.error(`палитра оболочки разошлась с ui/tokens.css в ${problems.length} местах:`);
  for (const line of problems) console.error('  ' + line);
  process.exitCode = 1;
} else {
  console.log(`палитра оболочки сходится с ui/tokens.css: сверено ${Object.keys(MAP).length} токенов, вторых копий нет`);
}
