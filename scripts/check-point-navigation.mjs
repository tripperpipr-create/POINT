import fs from 'node:fs';
import path from 'node:path';

const projectRoot = path.resolve(import.meta.dirname, '..');
const extensionPath = path.join(projectRoot, 'vscode-extension', 'package.json');
const extensionSourcePath = path.join(projectRoot, 'vscode-extension', 'extension.js');
const overlayPath = path.join(projectRoot, 'distribution', 'apply-overlay.mjs');
const productPath = path.join(projectRoot, 'distribution', 'product-overrides.json');
const codeOssExtensions = path.join(projectRoot, '.cache', 'code-oss', 'extensions');

const manifest = JSON.parse(fs.readFileSync(extensionPath, 'utf8'));
const source = fs.readFileSync(extensionSourcePath, 'utf8');
const actionSource = fs.readFileSync(path.join(projectRoot, 'vscode-extension', 'ide-action-controller.js'), 'utf8');
const sourceOnly = process.argv.includes('--source-only');
const overlay = fs.readFileSync(overlayPath, 'utf8');
const product = JSON.parse(fs.readFileSync(productPath, 'utf8'));

const requiredCommands = [
  'localAgent.searchEverywhere',
  'localAgent.findUsages',
  'localAgent.findImplementations',
  'localAgent.languageSupport',
  'localAgent.openRoster',
  'localAgent.openChronicle',
  'localAgent.renameSymbol',
  'localAgent.optimizeImports',
  'localAgent.reformatCode',
  'localAgent.recentLocations',
  'localAgent.nextError',
  'localAgent.runAnything',
  'localAgent.vcsRollback',
  'localAgent.hideAllToolWindows',
  'localAgent.focusBreadcrumbs',
  'localAgent.goToTypeDefinition',
];
const commands = new Set((manifest.contributes?.commands ?? []).map(command => command.command));
for (const command of requiredCommands) {
  if (!commands.has(command)) throw new Error(`Point navigation command is missing: ${command}`);
  if (!source.includes(`registerCommand('${command}'`)) throw new Error(`Point navigation command is not registered: ${command}`);
}

const expectedKeys = new Map([
  ['ctrl+shift+n', 'localAgent.navigateFile'],
  ['ctrl+shift+a', 'workbench.action.showCommands'],
  ['alt+f7', 'localAgent.findUsages'],
  ['ctrl+b', 'localAgent.goToDefinition'],
  ['ctrl+alt+b', 'localAgent.findImplementations'],
  ['ctrl+shift+b', 'localAgent.goToTypeDefinition'],
  ['shift+f6', 'localAgent.renameSymbol'],
  ['ctrl+alt+o', 'localAgent.optimizeImports'],
  ['ctrl+alt+l', 'localAgent.reformatCode'],
  ['f2', 'localAgent.nextError'],
  ['shift+f2', 'localAgent.previousError'],
  ['ctrl+shift+e', 'localAgent.recentLocations'],
  ['ctrl+e', 'localAgent.recentLocations'],
  ['alt+home', 'localAgent.focusBreadcrumbs'],
  ['alt+j', 'localAgent.selectNextOccurrence'],
  ['ctrl+shift+f12', 'localAgent.hideAllToolWindows'],
  ['ctrl+d', 'editor.action.copyLinesDownAction'],
  ['ctrl+k', 'localAgent.vcsCommit'],
  ['ctrl+h', 'editor.showTypeHierarchy'],
  ['ctrl+alt+h', 'editor.showCallHierarchy'],
  ['alt+1', 'workbench.view.explorer'],
  ['alt+3', 'workbench.action.findInFiles'],
  ['alt+5', 'workbench.view.debug'],
  ['alt+6', 'workbench.actions.view.problems'],
  ['alt+7', 'localAgent.showOutline'],
  ['alt+4', 'workbench.view.extension.pointRun'],
  ['alt+8', 'workbench.view.extension.pointServices'],
  ['alt+9', 'localAgent.openChronicle'],
  ['alt+0', 'localAgent.vcsChanges'],
  ['ctrl+alt+i', 'localAgent.open'],
  ['alt+f12', 'localAgent.openTerminal'],
  ['ctrl+alt+shift+i', 'localAgent.openRoster'],
]);
const keybindings = manifest.contributes?.keybindings ?? [];
for (const [key, command] of expectedKeys) {
  const binding = keybindings.find(candidate => candidate.key === key && !String(candidate.command || '').startsWith('-'));
  if (binding?.command !== command) throw new Error(`Expected ${key} -> ${command}, found ${binding?.command ?? 'nothing'}`);
}

// Три сочетания поиска принадлежат не расширению, а слою оболочки: по ТЗ
// docs/search-window.md они открывают своё окно поиска, а оно живёт в
// Code-OSS (`point-search-window.ts.txt`), потому что всплывающее окно поверх
// редактора в модели расширений не выражается.
//
// Проверяются обе стороны сразу. Раскладка расширения весит `BuiltinExtension`
// (300) во встроенной поставке и `ExternalExtension` (400) в установленной:
// второе объявление того же сочетания в манифесте молча перебивало бы слой —
// именно так Ctrl+N открывал квик-пик «Поиск везде» вместо окна. Поэтому в
// манифесте их быть не должно, а перевес слоя обязан считаться от верхней
// ступени, а не от вклада оболочки.
const searchWindowPath = path.join(projectRoot, 'distribution', 'resources', 'point-search-window.ts.txt');
const searchWindowLayer = fs.readFileSync(searchWindowPath, 'utf8');
const searchWindowKeys = [
  ['ctrl+n', 'point.searchWindow'],
  ['ctrl+alt+shift+n', 'point.searchWindowSymbols'],
  ['ctrl+shift+f', 'point.searchWindowText'],
];
for (const [key, command] of searchWindowKeys) {
  const claimed = keybindings.find(candidate => candidate.key === key && !String(candidate.command || '').startsWith('-'));
  if (claimed) throw new Error(`Search window keybinding ${key} must not be re-declared by the extension (found ${claimed.command})`);
  if (!searchWindowLayer.includes(`id: '${command}'`)) throw new Error(`Search window layer must register ${command}`);
}
if (!searchWindowLayer.includes('KeybindingWeight.ExternalExtension + 50')) {
  throw new Error('Search window keybindings must outweigh extension keybindings (ExternalExtension + 50)');
}

const requiredUnbinds = [
  ['ctrl+n', '-workbench.action.files.newUntitledFile'],
  ['ctrl+p', '-workbench.action.quickOpen'],
  ['ctrl+b', '-workbench.action.toggleSidebarVisibility'],
  ['ctrl+y', '-redo'],
  ['ctrl+shift+e', '-workbench.view.explorer'],
  ['f2', '-editor.action.rename'],
  ['alt+f12', '-editor.action.peekDefinition'],
];
for (const [key, command] of requiredUnbinds) {
  const binding = keybindings.find(candidate => candidate.key === key && candidate.command === command);
  if (!binding) throw new Error(`Expected JetBrains unbind ${key} -> ${command}`);
}

const openRoster = keybindings.find(candidate => candidate.command === 'localAgent.openRoster');
if (openRoster?.key !== 'ctrl+alt+shift+i') {
  throw new Error('localAgent.openRoster must use ctrl+alt+shift+i to avoid Ctrl+Alt+I Guild conflict');
}
if (keybindings.some(candidate => candidate.key === 'ctrl+alt+i' && candidate.command === 'localAgent.openRoster')) {
  throw new Error('Ctrl+Alt+I must not bind openRoster (reserved for Guild watermark)');
}

const editorMenuCommands = new Set((manifest.contributes?.menus?.['editor/context'] ?? []).map(item => item.command));
for (const command of ['localAgent.findUsages', 'localAgent.findImplementations']) {
  if (!editorMenuCommands.has(command)) throw new Error(`Editor menu command is missing: ${command}`);
}

for (const fragment of [
  "'references.preferredLocation': 'view'",
  "'scm.defaultViewMode': 'tree'",
  "'scm.alwaysShowRepositories': true",
  "'search.mode': 'reuseEditor'",
  "'search.defaultViewMode': 'tree'",
  "'workbench.editor.highlightModifiedTabs': true",
  "'workbench.localHistory.enabled': true",
  "'search.seedOnFocus': true",
  "'update.mode': 'manual'",
  "'editor.scrollbar.verticalScrollbarSize': 8",
  "'editor.stickyScroll.defaultModel': 'outlineModel'",
  "'breadcrumbs.symbolSortOrder': 'position'",
  'Point status bar product import',
  'Point status bar busy class wiring',
  "'container.title': 'Использования'",
  // Помощник закрепляется на каждом старте. Незакреплённый инструмент виден в
  // рейке, только пока открыт: одно случайное «Скрыть» — и чат исчезает при
  // первом же переключении вкладки, а `pinned: false` остаётся в профиле.
  "viewContainer.id === 'workbench.view.extension.pointCompanion'",
  // Полоса ярлыков окон нижней панели. Без неё закрытая панель уносит с экрана
  // Запуск, Отладку, Проблемы, Поиск, Службы и Логи, и о них не напоминает
  // ничто, кроме памяти на Alt+N.
  "point-panel-strip.ts.txt",
  'Point panel strip attach helper',
  // Ряд вкладок в шапке панели выключен: полный список окон живёт в рейке, и
  // второе место для одного выбора — та же ошибка, что была у терминала.
  'Point panel composite bar off',
  // Отладчик — нижнее окно, как в JetBrains, и консоль отладки внутри него.
  'Point debug view container in panel',
  'Point debug console inside debug window',
  // Структура — своё окно в боковой панели: вьюшку Outline внутри Проекта
  // оболочка не регистрирует, и Alt+7 без этого окна вёл бы в ошибку.
  'Point structure view container',
]) {
  if (!overlay.includes(fragment)) throw new Error(`Point overlay navigation default is missing: ${fragment}`);
}

if (!source.includes('POINT_ACTION_REGISTRY')) {
  throw new Error('Quick Actions / Search Everywhere must share POINT_ACTION_REGISTRY');
}
if (!source.includes('createWorkspaceFileCache')) {
  throw new Error('Search Everywhere workspace file cache is missing');
}
if (!source.includes('parseDocumentOutline') || !source.includes('createRecentFilesTracker') || !source.includes('createProblemsStatus')) {
  throw new Error('Point editor daily tools (outline / recent files / problems status) are missing');
}
if (!source.includes('createStructureStatus') || !source.includes('resolveOutlineSymbolAt') || !source.includes('flushFormatAfterOrganize')) {
  throw new Error('Point editor structure status / outline breadcrumb / format-after-organize are missing');
}
if (!source.includes('openRecentFileRecord') || !source.includes('goToTypeDefinition') || !source.includes('focusBreadcrumbs')) {
  throw new Error('Point editor navigation helpers (recent position / type / breadcrumbs) are missing');
}
if (!source.includes('busyFlags.tasks') || !source.includes('updateAgentBusy')) {
  throw new Error('Busy-state task Set / agent wiring is missing');
}

if (!product.extensionsGallery?.serviceUrl?.startsWith('https://open-vsx.org/')) {
  throw new Error('Point needs a vendor-neutral extension gallery for on-demand language support.');
}

const languageIds = new Set();
if (fs.existsSync(codeOssExtensions)) {
  for (const entry of fs.readdirSync(codeOssExtensions, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    const packagePath = path.join(codeOssExtensions, entry.name, 'package.json');
    if (!fs.existsSync(packagePath)) continue;
    try {
      const candidate = JSON.parse(fs.readFileSync(packagePath, 'utf8'));
      for (const language of candidate.contributes?.languages ?? []) {
        if (language.id) languageIds.add(language.id);
      }
    } catch {
      // A broken upstream manifest will be reported by the full Code-OSS build.
    }
  }
}
if (!sourceOnly) {
for (const language of ['javascript', 'typescript', 'json', 'html', 'css', 'markdown', 'python', 'java', 'cpp', 'rust', 'go']) {
  if (!languageIds.has(language)) throw new Error(`Code-OSS language grammar is missing: ${language}`);
}
if (languageIds.size < 40) throw new Error(`Expected broad built-in syntax support, found ${languageIds.size} language IDs.`);

}

const languageProfiles = [...actionSource.matchAll(/extension:\s*'([^']+)'/g)].map(match => match[1]);
if (new Set(languageProfiles).size < 15) throw new Error('Point on-demand language catalog is too narrow.');

process.stdout.write(JSON.stringify({
  commands: requiredCommands.length,
  jetBrainsKeybindings: expectedKeys.size,
  unbinds: requiredUnbinds.length,
  builtInLanguageIds: sourceOnly ? null : languageIds.size,
  grammarCheck: sourceOnly ? 'not requested (source-only)' : 'passed',
  onDemandLanguageProfiles: new Set(languageProfiles).size,
  extensionGallery: product.extensionsGallery.serviceUrl,
}, null, 2));
