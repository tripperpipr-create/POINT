import fs from 'node:fs';
import path from 'node:path';

const projectRoot = path.resolve(import.meta.dirname, '..');
const sourcePackage = path.join(projectRoot, 'vscode-extension', 'package.json');
const packaged = path.join(projectRoot, '.cache', 'VSCode-win32-x64', 'resources', 'app', 'extensions', 'local-agent-workbench', 'package.json');
const workbenchCss = path.join(projectRoot, '.cache', 'VSCode-win32-x64', 'resources', 'app', 'out', 'vs', 'workbench', 'workbench.desktop.main.css');
const overlayDefaults = path.join(projectRoot, 'distribution', 'apply-overlay.mjs');
const checklist = [
  'tabs (modified highlight + close on hover)',
  'suggest / hover / parameter hints',
  'Point search window (double Shift / Ctrl+N / Ctrl+Shift+F / Ctrl+Alt+Shift+N)',
  'File Structure via shortcut/search; no permanent Outline or Timeline tabs',
  'split editor sash',
  'Run / Debug tool window',
  'terminal tabs',
  'console channel editor + Point Dark tokens',
  'debug console / output panel chrome',
  'status busy stripe during index/task',
  'keyboard focus-visible rings',
];

const overlay = fs.readFileSync(overlayDefaults, 'utf8');
for (const fragment of [
  "'workbench.editor.highlightModifiedTabs': true",
  "'window.commandCenter': true",
  "'window.density.editorTabHeight': 'default'",
  "'workbench.list.openMode': 'doubleClick'",
  "'workbench.localHistory.enabled': true",
  "'search.seedOnFocus': true",
  "'update.mode': 'manual'",
  "'editor.scrollbar.verticalScrollbarSize': 8",
  "'terminal.integrated.stickyScroll.enabled': true",
  "'terminal.integrated.suggest.enabled': false",
  "'terminal.integrated.initialHint': false",
  "'terminal.integrated.minimumContrastRatio': 4.5",
  "'debug.console.fontFamily': \"'JetBrains Mono', Consolas, monospace\"",
  'Point status bar busy class wiring',
  'Point editor watermark entries',
]) {
  if (!overlay.includes(fragment)) throw new Error(`Overlay defaults missing ${fragment}`);
}

const themePath = path.join(projectRoot, 'vscode-extension', 'themes', 'point-dark-color-theme.json');
const theme = JSON.parse(fs.readFileSync(themePath, 'utf8'));
for (const key of [
  'terminal.findMatchBackground',
  'terminalStickyScroll.background',
  'outputView.background',
  'debugConsole.errorForeground',
  'panelInput.border',
]) {
  if (!theme.colors?.[key]) throw new Error(`Point Dark missing console token ${key}`);
}

const extensionSource = fs.readFileSync(path.join(projectRoot, 'vscode-extension', 'extension.js'), 'utf8');
if (!extensionSource.includes('TerminalLocation.Editor') || !extensionSource.includes('createConsoleChannel')) {
  throw new Error('Console channels must open in TerminalLocation.Editor for Alt+F12 smoke');
}

const workbenchCssSource = path.join(projectRoot, 'distribution', 'resources', 'point-workbench.css');
const workbenchCssText = fs.readFileSync(workbenchCssSource, 'utf8');
// Проверяем селекторы, а не заголовки комментариев: комментарий переживает
// переименование раздела, а поверхность — нет. Раньше здесь стояли подписи
// вроде «Console channel as editor tab», и переписанный стиль с теми же
// правилами валил проверку, ничего при этом не сломав.
for (const fragment of [
  '.terminal-editor',
  '.pane-body.output-view',
  '.terminal-sticky-scroll',
  '.repl .repl-input-wrapper',
  '--editor-group-tab-height: var(--point-tab-h)',
]) {
  if (!workbenchCssText.includes(fragment)) throw new Error(`Workbench CSS missing console chrome: ${fragment}`);
}

const sourceManifest = JSON.parse(fs.readFileSync(sourcePackage, 'utf8'));
const sourceKeys = sourceManifest.contributes?.keybindings || [];
if (!sourceKeys.some(item => item.command === 'localAgent.openRoster' && item.key === 'ctrl+alt+shift+i')) {
  throw new Error('Source openRoster keybinding must be ctrl+alt+shift+i');
}
if (!sourceKeys.some(item => item.command === '-redo' && item.key === 'ctrl+y')) {
  throw new Error('Source Ctrl+Y redo unbind is missing');
}
const sourceCommands = (sourceManifest.contributes?.commands || []).map(item => item.command)
for (const command of [
  'localAgent.companionInbox',
  'localAgent.askCompanionFix',
  'localAgent.askCompanionAboutTerminal',
  'localAgent.askCompanionAboutDiff',
]) {
  if (!sourceCommands.includes(command)) throw new Error(`Source commands missing ${command}`)
}
if (!sourceManifest.contributes?.menus?.['terminal/title']?.some(item => item.command === 'localAgent.askCompanionAboutTerminal')) {
  throw new Error('Terminal title is missing Companion analyze-failure')
}
if (!sourceManifest.contributes?.menus?.['scm/resourceState/context']?.some(item => item.command === 'localAgent.askCompanionAboutDiff')) {
  throw new Error('SCM resource menu is missing Companion diff')
}
if (!sourceCommands.includes('localAgent.runFile')) {
  throw new Error('Source commands missing localAgent.runFile')
}
if (!sourceManifest.contributes?.menus?.['explorer/context']?.some(item => item.command === 'localAgent.runFile')) {
  throw new Error('Explorer is missing Run file')
}
for (const command of [
  'localAgent.askCompanionAboutRun',
  'localAgent.askCompanionAboutDebug',
  'localAgent.askCompanionAboutBlame',
  'localAgent.askCompanionAboutHistory',
  'localAgent.copyReference',
  'localAgent.newScratch',
  'localAgent.compareWithFile',
  'localAgent.focusBreadcrumbs',
  'localAgent.goToTypeDefinition',
]) {
  if (!sourceCommands.includes(command)) throw new Error(`Source commands missing ${command}`)
}
if (!sourceKeys.some(item => item.command === 'localAgent.recentLocations' && item.key === 'ctrl+e')) {
  throw new Error('Ctrl+E must open Point recent locations with cursor restore')
}
if (!sourceKeys.some(item => item.command === 'localAgent.focusBreadcrumbs' && item.key === 'alt+home')) {
  throw new Error('Alt+Home must focus breadcrumbs / navigation bar')
}
if (!sourceManifest.contributes?.menus?.['debug/callstack/context']?.some(item => item.command === 'localAgent.askCompanionAboutDebug')) {
  throw new Error('Debug call stack is missing Companion analyze')
}

const result = {
  overlayDefaults: true,
  sourceKeybindings: true,
  packagedExtension: false,
  cssHintsPresent: false,
  visualChecklist: checklist,
};

if (fs.existsSync(packaged)) {
  const manifest = JSON.parse(fs.readFileSync(packaged, 'utf8'));
  const defaults = manifest.contributes?.configurationDefaults || {};
  // Stale portable trees are refreshed by the full build; only enforce when new defaults are present.
  if (defaults['workbench.editor.highlightModifiedTabs'] === true) {
    if (defaults['editor.scrollbar.verticalScrollbarSize'] !== 8) {
      throw new Error('Packaged scrollbar size must be 8');
    }
    if (defaults['update.mode'] !== 'manual') {
      throw new Error('Packaged update.mode must be manual');
    }
  }
  const keybindings = manifest.contributes?.keybindings || [];
  if (defaults['workbench.editor.highlightModifiedTabs'] === true) {
    if (!keybindings.some(item => item.command === 'localAgent.openRoster' && item.key === 'ctrl+alt+shift+i')) {
      throw new Error('Packaged openRoster keybinding must be ctrl+alt+shift+i');
    }
  }
  result.packagedExtension = true;
  result.packagedDefaultsFresh = defaults['workbench.editor.highlightModifiedTabs'] === true;
}

if (fs.existsSync(workbenchCss)) {
  const css = fs.readFileSync(workbenchCss, 'utf8');
  result.cssHintsPresent = css.includes('suggest-widget') || css.includes('point-busy') || css.includes('editor-group-watermark');
}

process.stdout.write(JSON.stringify(result, null, 2));
