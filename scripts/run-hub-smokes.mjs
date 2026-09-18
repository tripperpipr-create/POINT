// Проверки Хаба, которые раньше не запускал никто.
//
// Двадцать восемь смоуков Хаба и Мастера были написаны, положены в `scripts/`
// и забыты: ни `npm run check`, ни CI, ни другой скрипт их не вызывал. Прогон
// 2 сентября 2026 года показал цену: двадцать семь зелёных и одна красная —
// `smoke-point-guild-roster.js` ждал заголовок «РОСТЕР АГЕНТОВ», которого
// Гильдия не рендерит с прошлой переработки. Проверка разошлась с экраном и
// пролежала так неизвестно сколько, потому что её никто не запускал.
//
// Список ниже намеренно ручной. Автопоиск по маске `smoke-*.js` подключал бы
// новую проверку к гейту по имени файла — то есть случайно, вместе с
// черновиком и временным воспроизведением бага. Здесь проверка попадает в
// гейт одним осознанным действием: дописать строку.
//
//   node scripts/run-hub-smokes.mjs

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const root = path.resolve(import.meta.dirname, '..');

// Смоуки Хаба, Мастера и компаньона. Каждый исполняет собранный
// `vscode-extension/media/main.js` в изолированном контексте с фейковым DOM,
// поэтому раннер обязан идти после `npm run build`.
const checks = [
  'smoke-chat-directory.js',
  'smoke-point-project-gallery.js',
  'smoke-task-brief.js',
  'smoke-agent-hub-roster-routing.js',
  'smoke-companion-error-recovery.js',
  'smoke-companion-wait-visibility.js',
  'smoke-companion-oversized-message.js',
  'smoke-companion-error-wording.js',
  'smoke-companion-feedback-lifecycle.js',
  'smoke-log-chat-excerpt.js',
  'smoke-log-chat-panel.js',
  'smoke-companion-skill-equip.js',
  'smoke-connection-state-signature.js',
  'smoke-constructor-loses-nothing.js',
  'smoke-core-failure-wording.js',
  'smoke-extension-backend.js',
  'smoke-hub-badge-agreement.js',
  'smoke-hub-connection-orb.js',
  'smoke-hub-core-offline.js',
  'smoke-hub-decision-hotkeys.js',
  'smoke-hub-degenerate-data.js',
  'smoke-hub-failed-request-recovery.js',
  'smoke-hub-form-double-submit.js',
  'smoke-hub-onboarding-not-closed.js',
  'smoke-hub-keyboard-navigation.js',
  'smoke-hub-no-double-start.js',
  'smoke-hub-overview-attention.js',
  'smoke-hub-paused-run-not-finished.js',
  'smoke-hub-composer-extend-active-time.js',
  'smoke-hub-quest-midflight-controls.js',
  'smoke-hub-master-work-transcript.js',
  'smoke-hub-run-failure-wording.js',
  'smoke-hub-secret-claims.js',
  'smoke-hub-tool-hint-audience.js',
  'smoke-hub-unknown-vs-empty.js',
  'smoke-model-picker.js',
  'smoke-master-answer-origin.js',
  'smoke-master-brief-panel.js',
  'smoke-master-compose.js',
  'smoke-master-context.js',
  'smoke-master-editor-context.cjs',
  'smoke-master-feed.js',
  'smoke-master-live-trace.mjs',
  'smoke-master-stream.cjs',
  'smoke-master-message-actions.js',
  'smoke-master-questions.js',
  'smoke-master-quest-path.js',
  'smoke-master-send-unfreezes.js',
  'smoke-master-thread-incremental.js',
  'smoke-master-work-order-controls.mjs',
  'smoke-work-order-execution-ui.mjs',
  'smoke-master-hiring-card.mjs',
  'smoke-master-agent-card.mjs',
  'smoke-quest-brief-state-signature.js',
  'smoke-onboarding-wizard.js',
  'smoke-point-connections.js',
  'smoke-point-guild-roster.js',
  'smoke-proposal-edit-survives-refresh.js',
  'smoke-point-log-rotation.js',
  'smoke-point-split-windows.js',
  'smoke-setup-survives-core-refusal.js',
  'smoke-skill-cannot-loosen-deny.js',
];

const failures = [];
let passed = 0;

for (const name of checks) {
  const file = path.join(root, 'scripts', name);
  // Пропавший файл — не «нечего проверять», а потерянная проверка. Молчать об
  // этом нельзя: ровно так гейт и слепнет.
  if (!fs.existsSync(file)) {
    failures.push(`${name}: файла нет — проверка потеряна, а не пройдена`);
    continue;
  }
  try {
    execFileSync(process.execPath, [file], { cwd: root, stdio: 'pipe' });
    passed += 1;
  } catch (error) {
    const output = [error.stdout, error.stderr]
      .map(chunk => String(chunk || '').trim())
      .filter(Boolean)
      .join('\n');
    const reason = output.split(/\r?\n/).find(line => /Error:/.test(line)) || output.split(/\r?\n/)[0] || 'без вывода';
    failures.push(`${name}: ${reason.trim()}`);
  }
}

if (failures.length) {
  console.error(`ПРОВЕРКИ ХАБА ПРОВАЛЕНЫ (${passed}/${checks.length} зелёных):`);
  for (const message of failures) console.error('  · ' + message);
  process.exit(1);
}
console.log(`проверки Хаба зелёные: ${passed}/${checks.length}`);
