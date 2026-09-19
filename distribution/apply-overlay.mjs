import fs from 'node:fs';
import path from 'node:path';

const projectRoot = path.resolve(import.meta.dirname, '..');
const sourceRoot = path.resolve(process.argv[2] || path.join(projectRoot, 'vendor', 'code-oss'));
const version = JSON.parse(fs.readFileSync(path.join(import.meta.dirname, 'version.json'), 'utf8'));

function fail(message) { throw new Error(`[Point overlay] ${message}`); }
function readJson(file) { return JSON.parse(fs.readFileSync(file, 'utf8')); }
function writeJson(file, value) { fs.writeFileSync(file, `${JSON.stringify(value, null, '\t')}\n`); }
function replaceOnce(file, original, replacement, description) {
  const source = fs.readFileSync(file, 'utf8');
  const crlfOriginal = original.replaceAll('\n', '\r\n');
  const crlfReplacement = replacement.replaceAll('\n', '\r\n');
  if (source.includes(replacement) || source.includes(crlfReplacement)) return;
  if (source.includes(original)) {
    fs.writeFileSync(file, source.replace(original, replacement));
  } else if (source.includes(crlfOriginal)) {
    fs.writeFileSync(file, source.replace(crlfOriginal, crlfReplacement));
  } else {
    fail(`Could not locate ${description} in ${file}`);
  }
}
function replaceIfPresent(file, original, replacement) {
  const source = fs.readFileSync(file, 'utf8');
  const crlfOriginal = original.replaceAll('\n', '\r\n');
  const crlfReplacement = replacement.replaceAll('\n', '\r\n');
  if (source.includes(replacement) || source.includes(crlfReplacement)) return;
  if (source.includes(original)) {
    fs.writeFileSync(file, source.replace(original, replacement));
  } else if (source.includes(crlfOriginal)) {
    fs.writeFileSync(file, source.replace(crlfOriginal, crlfReplacement));
  }
}
// Как `replaceAny`, но молчит, если ни одной прежней формы нет: так правится
// то, что ниже по цепочке уже заменено другой заплатой.
function replaceIfPresentAny(file, originals, replacement) {
  const source = fs.readFileSync(file, 'utf8');
  const crlfReplacement = replacement.replaceAll('\n', '\r\n');
  if (source.includes(replacement) || source.includes(crlfReplacement)) return;
  for (const original of originals) {
    const crlfOriginal = original.replaceAll('\n', '\r\n');
    if (source.includes(original)) {
      fs.writeFileSync(file, source.replace(original, replacement));
      return;
    }
    if (source.includes(crlfOriginal)) {
      fs.writeFileSync(file, source.replace(crlfOriginal, crlfReplacement));
      return;
    }
  }
}
// Убрать строку целиком. Через `replaceIfPresent` этого не сделать: он выходит
// раньше, если замена уже есть в файле, а замена здесь — часть оригинала.
function removeIfPresent(file, fragment) {
  const source = fs.readFileSync(file, 'utf8');
  const crlfFragment = fragment.replaceAll('\n', '\r\n');
  if (source.includes(fragment)) {
    fs.writeFileSync(file, source.replace(fragment, ''));
  } else if (source.includes(crlfFragment)) {
    fs.writeFileSync(file, source.replace(crlfFragment, ''));
  }
}
function replaceAny(file, originals, replacement, description) {
  const source = fs.readFileSync(file, 'utf8');
  const crlfReplacement = replacement.replaceAll('\n', '\r\n');
  if (source.includes(replacement) || source.includes(crlfReplacement)) return;
  for (const original of originals) {
    const crlfOriginal = original.replaceAll('\n', '\r\n');
    if (source.includes(original)) {
      fs.writeFileSync(file, source.replace(original, replacement));
      return;
    }
    if (source.includes(crlfOriginal)) {
      fs.writeFileSync(file, source.replace(crlfOriginal, crlfReplacement));
      return;
    }
  }
  fail(`Could not locate ${description} in ${file}`);
}
function assertInside(root, target) {
  const relative = path.relative(root, target);
  if (!relative || relative === '..' || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) fail(`Unsafe generated path: ${target}`);
}

const upstreamPackagePath = path.join(sourceRoot, 'package.json');
const upstreamProductPath = path.join(sourceRoot, 'product.json');
if (!fs.existsSync(upstreamPackagePath) || !fs.existsSync(upstreamProductPath)) fail(`Code-OSS checkout not found at ${sourceRoot}`);
const upstreamPackage = readJson(upstreamPackagePath);
if (upstreamPackage.name !== 'code-oss-dev' || upstreamPackage.version !== version.ref) fail(`Expected Code-OSS ${version.ref}, found ${upstreamPackage.name} ${upstreamPackage.version}`);

const product = readJson(upstreamProductPath);
Object.assign(product, readJson(path.join(import.meta.dirname, 'product-overrides.json')));
delete product.quality;
for (const key of ['trustedExtensionAuthAccess', 'agentsTelemetryAppName', 'webviewContentExternalBaseUrlTemplate']) delete product[key];
// Recent Code-OSS builds iterate this field unconditionally. An empty array
// disables Microsoft-managed built-in updates without breaking extension scan.
product.builtInExtensionsEnabledWithAutoUpdates = [];
product.reportIssueUrl = '';
writeJson(upstreamProductPath, product);

// Point открывается Чертогом. Голый запуск поднимает только окно агентов и не
// восстанавливает прошлую сессию: ветка `agents` делает ранний `return`, поэтому
// `doGetPathsFromLastSession()` даже не вызывается. Любая явная цель — путь в
// аргументах, folder-uri/file-uri, ссылка point://, файл из «Открыть с помощью»,
// хост разработки расширения — по-прежнему ведёт прямо в оболочку IDE.
//
// `const` и `if` кладутся одним куском намеренно. Сторож «уже наложено» узнаёт
// блок целиком; вставка между ними чужой заплаты сломает его проверку и положит
// замену дважды (тот же провал описан ниже у раскладки Чертога).
const appMainPath = path.join(sourceRoot, 'src', 'vs', 'code', 'electron-main', 'app.ts');
replaceAny(
  appMainPath,
  [
    "\t\tif (args['agents']) {",
    "\t\tif (args['agents'] && this.productService.applicationName !== 'point') {",
  ],
  [
    "\t\tconst pointHubFirst = this.productService.applicationName === 'point'",
    '\t\t\t&& !args._.length',
    "\t\t\t&& !args['folder-uri']",
    "\t\t\t&& !args['file-uri']",
    "\t\t\t&& !args['new-window']",
    '\t\t\t&& !args.extensionDevelopmentPath?.length',
    '\t\t\t&& !args.extensionTestsPath',
    '\t\t\t&& !initialProtocolUrls?.openables.length',
    '\t\t\t&& !initialProtocolUrls?.urls.length',
    '\t\t\t&& !((global as { macOpenFiles?: string[] }).macOpenFiles ?? []).length;',
    "\t\tif (args['agents'] || pointHubFirst) {",
  ].join('\n'),
  'Point starts in the Agent Hub',
);

// Point uses Code-OSS' dedicated sessions BrowserWindow as the Agent Hub. The
// handoff below consumes a Point-only open intent instead of submitting it to
// the upstream sessions chat, activates the built-in Point extension in that
// window, and routes file opens back to a regular editor window.
const pointSessionsContributionPath = path.join(sourceRoot, 'src', 'vs', 'sessions', 'contrib', 'chat', 'electron-browser', 'chat.contribution.ts');
replaceOnce(
  pointSessionsContributionPath,
  "import { IStorageService } from '../../../../platform/storage/common/storage.js';",
  [
    "import { IStorageService } from '../../../../platform/storage/common/storage.js';",
    "import { CommandsRegistry, ICommandService } from '../../../../platform/commands/common/commands.js';",
    "import { INativeHostService } from '../../../../platform/native/common/native.js';",
    "import { IProductService } from '../../../../platform/product/common/productService.js';",
    "import { IWorkbenchLayoutService, Parts } from '../../../../workbench/services/layout/browser/layoutService.js';",
    '',
    "const POINT_HUB_OPEN_INTENT = 'point-hub:';",
  ].join('\n'),
  'Point sessions imports',
);
replaceOnce(
  pointSessionsContributionPath,
  "\t\t@IStorageService private readonly storageService: IStorageService,\n\t) {",
  "\t\t@IStorageService private readonly storageService: IStorageService,\n\t\t@ICommandService private readonly commandService: ICommandService,\n\t\t@IProductService private readonly productService: IProductService,\n\t\t@IWorkbenchLayoutService private readonly layoutService: IWorkbenchLayoutService,\n\t) {",
  'Point sessions services',
);
replaceOnce(
  pointSessionsContributionPath,
  "\t\t\tthis.logService.info(`[AgentsHandoff] IPC received: folderUri=${folderUri?.toString() ?? '(none)'} initialQuery=${initialQuery ? 'yes' : 'no'} sessionResource=${sessionResource?.toString() ?? '(none)'} preferredSessionType=${preferredSessionType?.sessionTypeId ?? '(none)'}`);",
  [
    "\t\t\tthis.logService.info(`[AgentsHandoff] IPC received: folderUri=${folderUri?.toString() ?? '(none)'} initialQuery=${initialQuery ? 'yes' : 'no'} sessionResource=${sessionResource?.toString() ?? '(none)'} preferredSessionType=${preferredSessionType?.sessionTypeId ?? '(none)'}`);",
    '',
    "\t\t\tif (this.productService.applicationName === 'point' && initialQuery?.startsWith(POINT_HUB_OPEN_INTENT)) {",
    "\t\t\t\tthis.openPointHub(folderUri, initialQuery)",
    "\t\t\t\t\t.catch(err => this.logService.error('[PointHub] open handoff failed', err));",
    '\t\t\t\treturn;',
    '\t\t\t}',
  ].join('\n'),
  'Point sessions handoff',
);
if (!fs.readFileSync(pointSessionsContributionPath, 'utf8').includes('private async openPointHub(')) replaceOnce(
  pointSessionsContributionPath,
  "\t\tipcRenderer.on('vscode:selectAgentsFolder', handleSelectAgentsFolder);\n\t\tthis._register({ dispose: () => ipcRenderer.removeListener('vscode:selectAgentsFolder', handleSelectAgentsFolder) });\n\t}\n\n\tprivate async handleOpenIntent",
  [
    "\t\tipcRenderer.on('vscode:selectAgentsFolder', handleSelectAgentsFolder);",
    "\t\tthis._register({ dispose: () => ipcRenderer.removeListener('vscode:selectAgentsFolder', handleSelectAgentsFolder) });",
    '',
    "\t\tif (this.productService.applicationName === 'point') {",
    '\t\t\tthis.preparePointHubLayout();',
    '\t\t\tthis.lifecycleService.when(LifecyclePhase.Eventually)',
    '\t\t\t\t.then(async () => {',
    '\t\t\t\t\tthis.preparePointHubLayout();',
    "\t\t\t\t\tawait this.commandService.executeCommand('localAgent.openAgentsHubWindow', { agentsWindow: true });",
    '\t\t\t\t\tawait timeout(100);',
    '\t\t\t\t\tthis.preparePointHubLayout();',
    '\t\t\t\t})',
    "\t\t\t\t.catch(err => this.logService.error('[PointHub] startup failed', err));",
    '\t\t}',
    '\t}',
    '',
    '\tprivate preparePointHubLayout(): void {',
    '\t\tfor (const [part, hidden] of [[Parts.EDITOR_PART, false], [Parts.SESSIONS_PART, true], [Parts.SIDEBAR_PART, true], [Parts.AUXILIARYBAR_PART, true], [Parts.PANEL_PART, true]] as const) {',
    '\t\t\ttry {',
    '\t\t\t\tthis.layoutService.setPartHidden(hidden, part);',
    '\t\t\t} catch (error) {',
    "\t\t\t\tthis.logService.warn(`[PointHub] layout part unavailable: ${part}`, error);",
    '\t\t\t}',
    '\t\t}',
    '\t}',
    '',
    '\tprivate async openPointHub(folderUri: URI | undefined, intent: string): Promise<void> {',
    '\t\tthis.preparePointHubLayout();',
    "\t\tlet tab = 'master';",
    "\t\tlet agentId: string | undefined;",
    "\t\tlet constructorStep: string | undefined;",
    '\t\ttry {',
    '\t\t\tconst payload = JSON.parse(decodeURIComponent(intent.slice(POINT_HUB_OPEN_INTENT.length))) as { tab?: string; agentId?: string; constructorStep?: string };',
    "\t\t\tif (typeof payload.tab === 'string' && payload.tab) {",
    '\t\t\t\ttab = payload.tab;',
    '\t\t\t}',
    "\t\t\tif (typeof payload.agentId === 'string' && payload.agentId) {",
    '\t\t\t\tagentId = payload.agentId;',
    '\t\t\t}',
    "\t\t\tif (typeof payload.constructorStep === 'string' && payload.constructorStep) {",
    '\t\t\t\tconstructorStep = payload.constructorStep;',
    '\t\t\t}',
    '\t\t} catch (error) {',
    "\t\t\tthis.logService.warn('[PointHub] invalid open intent', error);",
    '\t\t}',
    '\t\tawait this.lifecycleService.when(LifecyclePhase.Eventually);',
    '\t\tthis.preparePointHubLayout();',
    "\t\tawait this.commandService.executeCommand('localAgent.openAgentsHubWindow', { agentsWindow: true, folderUri: folderUri?.toJSON(), tab, agentId, constructorStep });",
    '\t\tawait timeout(100);',
    '\t\tthis.preparePointHubLayout();',
    '\t}',
    '',
    '\tprivate async handleOpenIntent',
  ].join('\n'),
  'Point sessions startup',
);
// Сторож вокруг раскладки. Заплата «Point sessions startup» выше накладывается
// только на непропатченный файл — она пропускает дерево, где `openPointHub(`
// уже есть. Поэтому исправленное тело метода до такого дерева не доезжало:
// сборка 12 сентября ушла без сторожа, `setPartHidden` в фазе BlockStartup
// падал на ещё не созданной сетке окна («Cannot read properties of undefined
// (reading 'setViewVisible')»), вклад не создавался целиком, а вместе с ним
// пропадали слушатель IPC и открытие окна агентов. Отдельный шаг чинит и уже
// пропатченное дерево; повторное наложение он переживает, потому что замену
// узнаёт по строке «layout part unavailable».
replaceIfPresent(
  pointSessionsContributionPath,
  [
    '\tprivate preparePointHubLayout(): void {',
    '\t\tthis.layoutService.setPartHidden(false, Parts.EDITOR_PART);',
    '\t\tfor (const part of [Parts.SESSIONS_PART, Parts.SIDEBAR_PART, Parts.AUXILIARYBAR_PART, Parts.PANEL_PART]) {',
    '\t\t\tthis.layoutService.setPartHidden(true, part);',
    '\t\t}',
    '\t}',
  ].join('\n'),
  [
    '\tprivate preparePointHubLayout(): void {',
    '\t\tfor (const [part, hidden] of [[Parts.EDITOR_PART, false], [Parts.SESSIONS_PART, true], [Parts.SIDEBAR_PART, true], [Parts.AUXILIARYBAR_PART, true], [Parts.PANEL_PART, true]] as const) {',
    '\t\t\ttry {',
    '\t\t\t\tthis.layoutService.setPartHidden(hidden, part);',
    '\t\t\t} catch (error) {',
    '\t\t\t\tthis.logService.warn(`[PointHub] layout part unavailable: ${part}`, error);',
    '\t\t\t}',
    '\t\t}',
    '\t}',
  ].join('\n'),
);
replaceOnce(
  pointSessionsContributionPath,
  "\t\tif (this.productService.applicationName === 'point') {\n\t\t\tthis.lifecycleService.when(LifecyclePhase.Eventually)",
  "\t\tif (this.productService.applicationName === 'point') {\n\t\t\tthis.preparePointHubLayout();\n\t\t\tthis.lifecycleService.when(LifecyclePhase.Eventually)",
  'early Point Hub layout guard',
);
replaceOnce(
  pointSessionsContributionPath,
  "\tprivate async openPointHub(folderUri: URI | undefined, intent: string): Promise<void> {\n\t\tlet tab = 'master';",
  "\tprivate async openPointHub(folderUri: URI | undefined, intent: string): Promise<void> {\n\t\tthis.preparePointHubLayout();\n\t\tlet tab = 'master';",
  'early Point Hub handoff layout guard',
);
replaceOnce(
  pointSessionsContributionPath,
  'registerWorkbenchContribution2(SelectAgentsFolderContribution.ID, SelectAgentsFolderContribution, WorkbenchPhase.BlockStartup);',
  [
    'registerWorkbenchContribution2(SelectAgentsFolderContribution.ID, SelectAgentsFolderContribution, WorkbenchPhase.BlockStartup);',
    '',
    "CommandsRegistry.registerCommand('point.openInEditorWindow', async (accessor, raw: { fileUri?: UriComponents; folderUri?: UriComponents; line?: number }) => {",
    '\tconst productService = accessor.get(IProductService);',
    "\tif (productService.applicationName !== 'point' || !raw?.fileUri) {",
    '\t\treturn;',
    '\t}',
    '\tconst nativeHostService = accessor.get(INativeHostService);',
    '\tlet fileUri = URI.revive(raw.fileUri);',
    '\tif (Number.isInteger(raw.line) && Number(raw.line) > 0) {',
    "\t\tfileUri = fileUri.with({ fragment: `L${Number(raw.line)}` });",
    '\t}',
    '\tconst folderUri = raw.folderUri ? URI.revive(raw.folderUri) : undefined;',
    '\tawait nativeHostService.openWindow([',
    '\t\t...(folderUri ? [{ folderUri }] : []),',
    '\t\t{ fileUri },',
    '\t], { forceNewWindow: false });',
    '});',
  ].join('\n'),
  'Point editor window routing',
);
// Открытие проекта из Чертога. `point.openInEditorWindow` для этого не годится:
// он выходит из себя при отсутствии `fileUri`. Соседняя команда берёт одну папку
// и отдаёт решение оболочке: окно на том же проекте получает фокус, другой
// проект открывается новым окном. Окно Чертога кандидатом не станет —
// `findWindowOnWorkspaceOrFolder` сверяет `openedWorkspace`, а у него это всегда
// `agent-sessions.code-workspace`, чем бы ни была подставленная папка.
//
// Якорь — весь хвост соседней регистрации целиком: короткий кусок вроде
// `], { forceNewWindow: false });` в файле не уникален, а разрез чужого блока
// ломает его сторож «уже наложено».
replaceAny(
  pointSessionsContributionPath,
  [
    [
      '\tconst folderUri = raw.folderUri ? URI.revive(raw.folderUri) : undefined;',
      '\tawait nativeHostService.openWindow([',
      '\t\t...(folderUri ? [{ folderUri }] : []),',
      '\t\t{ fileUri },',
      '\t], { forceNewWindow: false });',
      '});',
    ].join('\n'),
  ],
  [
    '\tconst folderUri = raw.folderUri ? URI.revive(raw.folderUri) : undefined;',
    '\tawait nativeHostService.openWindow([',
    '\t\t...(folderUri ? [{ folderUri }] : []),',
    '\t\t{ fileUri },',
    '\t], { forceNewWindow: false });',
    '});',
    '',
    "CommandsRegistry.registerCommand('point.openFolderWindow', async (accessor, raw: { folderUri?: UriComponents; forceNewWindow?: boolean }) => {",
    '\tconst productService = accessor.get(IProductService);',
    "\tif (productService.applicationName !== 'point' || !raw?.folderUri) {",
    '\t\treturn;',
    '\t}',
    '\tconst nativeHostService = accessor.get(INativeHostService);',
    '\tawait nativeHostService.openWindow([{ folderUri: URI.revive(raw.folderUri) }], { forceNewWindow: raw.forceNewWindow === true });',
    '});',
  ].join('\n'),
  'Point project window routing',
);

// Второй запуск Point — это не «ещё одно пустое окно». Без этой заплаты повторный
// клик по значку уходит в ветку «старт без аргументов», где
// `openWithoutArgumentsInNewWindow` по умолчанию даёт `!isMacintosh`, то есть на
// Windows — новое пустое окно оболочки. Чертог при этом остаётся за спиной, и
// обещание «дом открывается первым» ломается ровно на втором клике.
// `openAgentsWindow` уже переиспользует и фокусирует единственное окно сессий.
const launchMainServicePath = path.join(sourceRoot, 'src', 'vs', 'platform', 'launch', 'electron-main', 'launchMainService.ts');
replaceOnce(
  launchMainServicePath,
  "import { IProtocolUrl } from '../../url/electron-main/url.js';",
  "import { IProtocolUrl } from '../../url/electron-main/url.js';\nimport product from '../../product/common/product.js';",
  'Point launch service product import',
);
replaceAny(
  launchMainServicePath,
  ["\t\telse if (args['agents']) {"],
  [
    "\t\telse if (args['agents'] || (product.applicationName === 'point'",
    '\t\t\t&& !args._.length',
    "\t\t\t&& !args['folder-uri']",
    "\t\t\t&& !args['file-uri']",
    "\t\t\t&& !args['new-window'])) {",
  ].join('\n'),
  'Point second instance raises the Agent Hub',
);

// Первое мгновение окна принадлежало Code-OSS, а не Point.
//
// До того как отрисуется верстак, окно красится двумя путями. Главный процесс
// отдаёт Electron цвет фона из themeMainService: на первом запуске сохранённого
// значения нет, и берётся умолчание VS Code #1F1F1F — светлее фона Point
// (#171717), поэтому первый кадр вспыхивал и темнел.
const themeMainServicePath = path.join(sourceRoot, 'src', 'vs', 'platform', 'theme', 'electron-main', 'themeMainServiceImpl.ts');
replaceAny(
  themeMainServicePath,
  ["const DEFAULT_BG_DARK = '#1F1F1F';"],
  "const DEFAULT_BG_DARK = '#171717';",
  'Point window background before the workbench paints',
);

// Второй путь — «заставка частей»: до загрузки верстака Code-OSS рисует его
// скелет по сохранённой раскладке — полосы заголовка, панели действий, боковой
// панели и строки состояния. Для VS Code это уместно: скелет совпадает с тем,
// что появится следом. У Point за этими полосами стоит Чертог — вебвью во всю
// область редактора, — и скелет показывал пустой каркас чужого приложения:
// ровно то «непрогруженное окно VS Code», которое видно на старте.
//
// Заплата оставляет только цвет фона. Плоский прямоугольник цвета Point честнее
// каркаса, которого не будет: он ничего не обещает и ни с чем не спорит.
const workbenchBootPath = path.join(sourceRoot, 'src', 'vs', 'code', 'electron-browser', 'workbench', 'workbench.ts');
replaceAny(
  workbenchBootPath,
  [
    [
      '\t\t// developing an extension -> ignore stored layouts',
      '\t\tif (data && configuration.extensionDevelopmentPath) {',
      '\t\t\tdata.layoutInfo = undefined;',
      '\t\t}',
    ].join('\n'),
  ],
  [
    '\t\t// Point: скелет верстака не рисуется никогда.',
    '\t\t//',
    '\t\t// Полосы заголовка, панели действий и боковой панели — каркас VS Code,',
    '\t\t// а у Point на их месте Чертог. Каркас успевал мелькнуть чужим пустым',
    '\t\t// окном. Остаётся только цвет фона, взятый из сохранённой темы.',
    '\t\tif (data) {',
    '\t\t\tdata.layoutInfo = undefined;',
    '\t\t}',
  ].join('\n'),
  'Point start without the Code-OSS workbench skeleton',
);

// Сохранённой темы может ещё не быть — тогда Code-OSS оставляет цвета
// неопределёнными и пишет в стиль `background-color: undefined`. Правило
// отбрасывается, и первый кадр достаётся тому, что окажется под ним. Точка
// опоры Point — её собственные цвета, а не случайность.
//
// Цвета из сохранённой заставки берутся через `??`: у них тип `string |
// undefined`, и до заплаты они попадали в нетипизированные `let` — ошибку
// прятал вывод типа `any`, а в разметку уходила строка «undefined».
replaceAny(
  workbenchBootPath,
  [
    [
      '\t\tlet baseTheme;',
      '\t\tlet shellBackground;',
      '\t\tlet shellForeground;',
      '\t\tif (data) {',
      '\t\t\tbaseTheme = data.baseTheme;',
      '\t\t\tshellBackground = data.colorInfo.editorBackground;',
      '\t\t\tshellForeground = data.colorInfo.foreground;',
    ].join('\n'),
    [
      "\t\tlet baseTheme = 'vs-dark';",
      "\t\tlet shellBackground = '#171717';",
      "\t\tlet shellForeground = '#e9e9e9';",
      '\t\tif (data) {',
      '\t\t\tbaseTheme = data.baseTheme;',
      '\t\t\tshellBackground = data.colorInfo.editorBackground;',
      '\t\t\tshellForeground = data.colorInfo.foreground;',
    ].join('\n'),
  ],
  [
    "\t\tlet baseTheme = 'vs-dark';",
    "\t\tlet shellBackground = '#171717';",
    "\t\tlet shellForeground = '#e9e9e9';",
    '\t\tif (data) {',
    '\t\t\tbaseTheme = data.baseTheme;',
    '\t\t\tshellBackground = data.colorInfo.editorBackground ?? shellBackground;',
    '\t\t\tshellForeground = data.colorInfo.foreground ?? shellForeground;',
  ].join('\n'),
  'Point colours for the first frame without stored theme',
);

const windowsMainServicePath = path.join(sourceRoot, 'src', 'vs', 'platform', 'windows', 'electron-main', 'windowsMainService.ts');
replaceOnce(
  windowsMainServicePath,
  "\t\t// Open in a new browser window with the agent sessions workspace\n\t\tconst windows = await this.open(await this.ensureAgentsWindow(openConfig));",
  [
    '\t\t// Point keeps a single dedicated agents window. Reusing it preserves the',
    '\t\t// Hub conversation and avoids starting another extension host/core pair.',
    '\t\tconst existingAgentsWindow = this.getWindows().find(window => window.config?.isSessionsWindow);',
    '\t\tconst windows = existingAgentsWindow',
    '\t\t\t? [existingAgentsWindow]',
    '\t\t\t: await this.open(await this.ensureAgentsWindow(openConfig));',
    '\t\texistingAgentsWindow?.focus();',
  ].join('\n'),
  'single Point agents window',
);
// Чертог просыпается пустым; мир возвращает расширение, сверив его с диском.
//
// Рабочая область окна сессий — файл, и подмена папки при переключении проекта
// пишет проект прямо в него. Подставить последний мир здесь, в главном процессе,
// нельзя: он не видит globalState и сделал бы это вслепую — окно открылось бы на
// несуществующем корне ещё до первой строки расширения. На пустом старте стоит и
// заплата доверия ниже: у файла Чертога ноль папок, `getUrisTrust([])` даёт
// `true`, и редактор доверия не встаёт поперёк первого кадра.
//
// Поэтому здесь только сброс, а возврат мира делает `resumeLastPointWorld` в
// расширении — после первого кадра и с проверкой папки на диске. Сброс закрыт
// условием `initialStartup`: вызов `openAgentsWindow` с папкой из окна IDE
// посреди сессии не задет.
replaceAny(
  windowsMainServicePath,
  ['\t\tconst workspaceExists = await this.fileService.exists(agentSessionsWorkspaceUri);\n\t\tif (!workspaceExists) {'],
  [
    '\t\tconst workspaceExists = await this.fileService.exists(agentSessionsWorkspaceUri);',
    "\t\tconst pointResetHubWorkspace = product.applicationName === 'point' && openConfig.initialStartup === true;",
    '\t\tif (!workspaceExists || pointResetHubWorkspace) {',
  ].join('\n'),
  'Point Hub always starts without a project',
);

// Восстановление сессии поверх Чертога. Ранний `return` в `openFirstWindow`
// обходит `doGetPathsFromLastSession()`, но `window.restoreWindows: "preserve"`,
// выставленный руками, возвращает прошлые окна уже внутри `open()`. Для Point
// дом один, и он открывается пустым.
replaceAny(
  windowsMainServicePath,
  ["\t\tif (openConfig.initialStartup && !isRestoringPaths && this.configurationService.getValue<IWindowSettings | undefined>('window')?.restoreWindows === 'preserve') {"],
  "\t\tif (product.applicationName !== 'point' && openConfig.initialStartup && !isRestoringPaths && this.configurationService.getValue<IWindowSettings | undefined>('window')?.restoreWindows === 'preserve') {",
  'Point never restores the previous session over the Agent Hub',
);

replaceOnce(
  windowsMainServicePath,
  "\t\tif (configuration.isSessionsWindow) {\n\t\t\tconfiguration.profiles.profile = this.userDataProfilesMainService.profiles.find(p => p.isAgentsWindowProfile) ?? await this.userDataProfilesMainService.createAgentsWindowProfile();",
  "\t\tif (configuration.isSessionsWindow) {\n\t\t\tconfiguration.profiles.profile = product.applicationName === 'point'\n\t\t\t\t? defaultProfile\n\t\t\t\t: this.userDataProfilesMainService.profiles.find(p => p.isAgentsWindowProfile) ?? await this.userDataProfilesMainService.createAgentsWindowProfile();",
  'shared Point profile for editor and agents windows',
);

// Point targets predictable startup and a low-memory desktop footprint. On the
// tested Windows build the hardware GPU path used ~120 MiB more private memory
// and produced long first-frame stalls on affected drivers. Keep the stable
// software compositor by default; advanced users can opt back in explicitly.
const mainEntryPath = path.join(sourceRoot, 'src', 'main.ts');
replaceAny(
  mainEntryPath,
  [
    `const argvConfig = configureCommandlineSwitchesSync(args);
if (product.applicationName === 'point' && process.env['POINT_ENABLE_HARDWARE_ACCELERATION'] !== '1') {
	app.disableHardwareAcceleration();
}`,
    `const argvConfig = configureCommandlineSwitchesSync(args);`,
  ],
  `const argvConfig = configureCommandlineSwitchesSync(args);
if (product.applicationName === 'point') {
	if (process.env['POINT_ENABLE_HARDWARE_ACCELERATION'] !== '1') {
		app.disableHardwareAcceleration();
	}
	const configuredJsFlags = app.commandLine.getSwitchValue('js-flags');
	app.commandLine.appendSwitch('js-flags', (configuredJsFlags + ' --expose-gc').trim());
}`,
  'Point compositor and lifecycle garbage collection flags',
);
replaceOnce(
  mainEntryPath,
  `if (args['crash-reporter-directory'] || (argvConfig['enable-crash-reporter'] && !args['disable-crash-reporter'])) {`,
  `if (product.applicationName !== 'point' && (args['crash-reporter-directory'] || (argvConfig['enable-crash-reporter'] && !args['disable-crash-reporter']))) {`,
  'Point disabled crash reporter',
);

// Рабочая область Чертога — не проект, а деталь продукта, и доверие по ней не
// считают. Сохранённая рабочая область с нулём папок не проходит за «пустое
// окно» (`isEmptyWorkspace` требует состояния EMPTY либо временной области),
// поэтому без этой заплаты первый запуск Чертога упирался бы в редактор доверия
// вместо галереи миров. При нуле папок `getUrisTrust([])` даёт `true`; когда мир
// подставлен, доверие считается ровно по его папке. Сверка идёт по самому URI, а
// не по имени продукта: `agentSessionsWorkspace` пользовательским проектом не
// бывает, и лишняя инъекция в конструктор здесь дороже пользы.
const workspaceTrustPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'services', 'workspaces', 'common', 'workspaceTrust.ts');
replaceAny(
  workspaceTrustPath,
  ['\t\tif (workspaceConfiguration && isSavedWorkspace(workspaceConfiguration, this.environmentService)) {\n\t\t\tworkspaceUris.push(workspaceConfiguration);\n\t\t}'],
  [
    '\t\tconst pointHubWorkspace = !!workspaceConfiguration',
    '\t\t\t&& this.uriIdentityService.extUri.isEqual(workspaceConfiguration, this.environmentService.agentSessionsWorkspace);',
    '\t\tif (workspaceConfiguration && !pointHubWorkspace && isSavedWorkspace(workspaceConfiguration, this.environmentService)) {',
    '\t\t\tworkspaceUris.push(workspaceConfiguration);',
    '\t\t}',
  ].join('\n'),
  'Point Hub workspace is not a trust subject',
);

const copilotDirectory = path.join(sourceRoot, 'extensions', 'copilot');
assertInside(sourceRoot, copilotDirectory);
fs.rmSync(copilotDirectory, { recursive: true, force: true });

const installDirsPath = path.join(sourceRoot, 'build', 'npm', 'dirs.ts');
let installDirs = fs.readFileSync(installDirsPath, 'utf8');
installDirs = installDirs.replace("\t'extensions/copilot',\n", '');
fs.writeFileSync(installDirsPath, installDirs);

const gulpPath = path.join(sourceRoot, 'build', 'gulpfile.vscode.ts');
let gulpSource = fs.readFileSync(gulpPath, 'utf8');
const shimCall = "\t\tconst builtInCopilotExtensionDir = path.join(appBase, 'extensions', 'copilot');\n\t\tprepareBuiltInCopilotRipgrepShim(platform, arch, builtInCopilotExtensionDir, appNodeModulesDir);";
const guardedShimCall = "\t\tconst builtInCopilotExtensionDir = path.join(appBase, 'extensions', 'copilot');\n\t\tif (!fs.existsSync(builtInCopilotExtensionDir)) {\n\t\t\treturn;\n\t\t}\n\t\tprepareBuiltInCopilotRipgrepShim(platform, arch, builtInCopilotExtensionDir, appNodeModulesDir);";
if (!gulpSource.includes(guardedShimCall)) {
  if (!gulpSource.includes(shimCall)) fail('Could not locate Copilot packaging hook');
  gulpSource = gulpSource.replace(shimCall, guardedShimCall);
  fs.writeFileSync(gulpPath, gulpSource);
}

const extensionSource = path.join(projectRoot, 'vscode-extension');
const extensionTarget = path.join(sourceRoot, 'extensions', 'local-agent-workbench');
assertInside(sourceRoot, extensionTarget);
fs.rmSync(extensionTarget, { recursive: true, force: true });
fs.mkdirSync(extensionTarget, { recursive: true });
for (const name of ['extension.js', 'extension-utils.js', 'backup-controller.js', 'run-config-utils.js', 'ide-navigation-utils.js', 'companion-controller.js', 'companion-chat-controller.js', 'hub-runtime-controller.js', 'infra-controller.js', 'ide-action-controller.js', 'ide-navigation-controller.js', 'connection-controller.js', 'point-panels.js', 'roster-controller.js', 'learning-controller.js', 'tooling-controller.js', 'cursor-controller.js', 'ide-observation-controller.js', 'project-index-controller.js', 'console-ssh-controller.js', 'master-chat-controller.js', 'master-turn-stream.js', 'master-work-order-watch.js', 'master-context-controller.js', 'project-registry.js', 'core-warm-pool.js', 'ssh-utils.js', 'cursor-runtime.js', 'core-stream.js', 'core-log.js', 'core-lease.js', 'git-tool-controller.js', 'chat-documents.js', 'package.json', 'package-lock.json', 'README.md', 'CHANGELOG.md', 'LICENSE', '.vscodeignore', 'dist', 'media', 'themes', 'walkthrough', 'bin']) {
  const source = path.join(extensionSource, name);
  if (!fs.existsSync(source)) {
    if (name === 'package-lock.json') continue;
    fail(`Missing extension asset: ${source}`);
  }
  fs.cpSync(source, path.join(extensionTarget, name), { recursive: true });
}
// Список выше — ручной, и однажды он уже отстал: `extension-utils.js` выделили
// из `extension.js`, а сюда не вписали. Сборка при этом прошла, приложение
// собралось, и только послесборочный смоук показал «Cannot find module
// './extension-utils'» — расширение не активировалось вовсе. Проверка ниже
// ловит это на месте: каждый локальный require из выложенных файлов должен
// разрешаться внутри выложенного дерева.
const localRequires = (file) => {
  const code = fs.readFileSync(file, 'utf8');
  const found = new Set();
  for (const match of code.matchAll(/require\(\s*['"](\.[^'"]*)['"]\s*\)/g)) found.add(match[1]);
  return [...found];
};
const missingModules = [];
for (const name of fs.readdirSync(extensionTarget)) {
  if (!name.endsWith('.js')) continue;
  const file = path.join(extensionTarget, name);
  for (const request of localRequires(file)) {
    const base = path.resolve(extensionTarget, request);
    const candidates = [base, `${base}.js`, `${base}.json`, path.join(base, 'index.js')];
    if (!candidates.some(candidate => fs.existsSync(candidate))) missingModules.push(`${name} → ${request}`);
  }
}
if (missingModules.length) {
  fail('Extension staging misses local modules: ' + missingModules.join('; '));
}

const extensionPackagePath = path.join(extensionTarget, 'package.json');
const extensionPackage = readJson(extensionPackagePath);
extensionPackage.displayName = 'Point';
extensionPackage.description = 'Встроенный локальный coding-агент и рабочие инструменты Point IDE.';
extensionPackage.activationEvents = Array.from(new Set(extensionPackage.activationEvents || []))
  .filter(event => event !== '*' && event !== 'onStartupFinished');
extensionPackage.contributes.configurationDefaults = {
  'window.zoomLevel': 0,
  'window.commandCenter': true,
  // Layout only knows 22 (compact) or 35 (default). Point paints tabs at 28px
  // via --editor-group-tab-height; compact reserved only 22px and clipped labels.
  'window.density.editorTabHeight': 'default',
  // Point keeps rare workbench actions in Search Everywhere / Quick Actions.
  // The permanent upstream menu row made the product look and feel like an
  // overlay over Code-OSS and displaced the project controls users need daily.
  // Меню собирается, но лежит вторым слоем заголовка: его показывает
  // гамбургер, как в макете. При 'hidden' Code-OSS не строит пункты вовсе, и
  // разворачивать было бы нечего.
  'window.menuBarVisibility': 'classic',
  'window.restoreWindows': 'one',
  'window.title': '${activeEditorShort}${separator}${rootName}${separator}Point',
  'window.titleSeparator': ' — ',
  'workbench.startupEditor': 'none',
  'workbench.colorTheme': 'Point Dark',
  // Built-in Seti has file icons but no folders; Point injects open/closed
  // folder glyphs into vs-seti so Инвентарь / Летопись stay JetBrains-clear.
  // Значки файлов — минимальный набор: в макете дерево одноцветное, а цвет
  // несёт только буква состояния Git. Пёстрый seti спорил с ней за внимание.
  'workbench.iconTheme': 'vs-minimal',
  'workbench.layoutControl.enabled': false,
  'workbench.navigationControl.enabled': false,
  'workbench.activityBar.accountsVisibility': false,
  'workbench.activityBar.iconClickBehavior': 'toggle',
  // Keep the five Point tool windows directly visible instead of collapsing
  // their long Russian labels into the overflow menu.
  'workbench.secondarySideBar.showLabels': false,
  'workbench.list.openMode': 'doubleClick',
  'workbench.secondarySideBar.defaultVisibility': 'visible',
  'workbench.tips.enabled': false,
  // JetBrains-like: select with a click, open with a double-click. Preview tabs stay off.
  'workbench.editor.enablePreview': false,
  'workbench.editor.enablePreviewFromQuickOpen': false,
  'workbench.editor.enablePreviewFromCodeNavigation': false,
  'workbench.editor.limit.enabled': true,
  'workbench.editor.limit.value': 12,
  'workbench.editor.tabSizing': 'fit',
  'workbench.editor.pinnedTabSizing': 'compact',
  'workbench.editor.wrapTabs': false,
  'workbench.editor.highlightModifiedTabs': true,
  'workbench.editor.revealIfOpen': true,
  'workbench.editor.openPositioning': 'last',
  'workbench.editor.focusRecentEditorAfterClose': true,
  'workbench.editor.decorations.badges': true,
  'workbench.editor.tabActionCloseVisibility': 'hover',
  'editor.fontSize': 14,
  'editor.fontFamily': "'JetBrains Mono', Consolas, 'Courier New', monospace",
  'editor.fontLigatures': true,
  'editor.minimap.enabled': false,
  'editor.smoothScrolling': false,
  'editor.cursorSmoothCaretAnimation': 'off',
  'editor.cursorBlinking': 'solid',
  'editor.inlayHints.enabled': 'offUnlessPressed',
  'editor.largeFileOptimizations': true,
  'editor.semanticHighlighting.enabled': true,
  'editor.stickyScroll.enabled': true,
  'editor.stickyScroll.maxLineCount': 5,
  'editor.stickyScroll.defaultModel': 'outlineModel',
  'editor.bracketPairColorization.enabled': true,
  'editor.guides.bracketPairs': false,
  'editor.guides.indentation': true,
  'editor.guides.highlightActiveIndentation': true,
  'editor.renderLineHighlight': 'all',
  'editor.roundedSelection': false,
  'editor.cursorSurroundingLines': 2,
  'editor.padding.top': 4,
  'editor.unfoldOnClickAfterEndOfLine': true,
  'editor.codeLens': false,
  'editor.linkedEditing': true,
  'editor.hover.delay': 300,
  'editor.hover.sticky': true,
  'editor.suggest.preview': false,
  'editor.suggest.showStatusBar': true,
  'editor.suggest.showIcons': true,
  'editor.wordBasedSuggestions': 'off',
  'editor.quickSuggestions': { other: 'on', comments: 'off', strings: 'off' },
  'editor.find.seedSearchStringFromSelection': 'always',
  'editor.scrollbar.verticalScrollbarSize': 8,
  'editor.scrollbar.horizontalScrollbarSize': 8,
  // JetBrains-like: Ctrl+Click jumps to definition (Alt+Click = multi-cursor).
  'editor.multiCursorModifier': 'alt',
  'editor.parameterHints.enabled': true,
  'editor.parameterHints.cycle': true,
  'files.autoSave': 'off',
  'git.blame.editorDecoration.enabled': false,
  'git.blame.statusBarItem.enabled': false,
  'git.autofetch': false,
  'git.confirmSync': true,
  'git.enableSmartCommit': false,
  'git.showPushSuccessNotification': false,
  'git.openRepositoryInParentFolders': 'never',
  'problems.showCurrentInStatus': true,
  'editor.definitionLinkOpensInPeek': false,
  'editor.gotoLocation.multipleDefinitions': 'goto',
  'editor.gotoLocation.multipleDeclarations': 'goto',
  'editor.gotoLocation.multipleTypeDefinitions': 'goto',
  'editor.gotoLocation.multipleImplementations': 'goto',
  'editor.gotoLocation.multipleReferences': 'goto',
  'workbench.list.smoothScrolling': false,
  'telemetry.telemetryLevel': 'off',
  'search.followSymlinks': false,
  'files.watcherExclude': {
    '**/.git/objects/**': true,
    '**/.git/subtree-cache/**': true,
    '**/node_modules/**': true,
    '**/node_modules/*/**': true,
    '**/bower_components/**': true,
    '**/vendor/**': true,
    '**/.cache/**': true,
    '**/build/**': true,
    '**/dist/**': true,
    '**/out/**': true,
    '**/target/**': true,
    '**/.venv/**': true,
    '**/venv/**': true,
    '**/__pycache__/**': true,
    '**/.next/**': true,
    '**/.turbo/**': true,
    '**/coverage/**': true,
    '**/.idea/**': true,
    '**/.point/**': true,
    '**/.pnpm/**': true,
    '**/.pnpm-store/**': true,
    '**/.yarn/cache/**': true,
    '**/.yarn/unplugged/**': true,
    '**/.gradle/**': true,
    '**/.dart_tool/**': true,
    '**/.tox/**': true,
    '**/.mypy_cache/**': true,
    '**/.pytest_cache/**': true,
    '**/.ruff_cache/**': true,
    '**/.parcel-cache/**': true,
    '**/.svelte-kit/**': true,
    '**/Pods/**': true,
  },
  'search.exclude': {
    '**/node_modules': true,
    '**/bower_components': true,
    '**/vendor': true,
    '**/.cache': true,
    '**/build': true,
    '**/dist': true,
    '**/out': true,
    '**/target': true,
    '**/.venv': true,
    '**/venv': true,
    '**/__pycache__': true,
    '**/.next': true,
    '**/.turbo': true,
    '**/coverage': true,
    '**/.idea': true,
    '**/.point': true,
    '**/.pnpm': true,
    '**/.pnpm-store': true,
    '**/.yarn': true,
    '**/.gradle': true,
    '**/.dart_tool': true,
    '**/.tox': true,
    '**/.mypy_cache': true,
    '**/.pytest_cache': true,
    '**/.ruff_cache': true,
    '**/.parcel-cache': true,
    '**/.svelte-kit': true,
    '**/Pods': true,
  },
  'files.exclude': {
    '**/.git': true,
    '**/.svn': true,
    '**/.hg': true,
    '**/CVS': true,
    '**/.DS_Store': true,
    '**/Thumbs.db': true,
  },
  'editor.renderWhitespace': 'none',
  'editor.renderControlCharacters': false,
  'editor.occurrencesHighlight': 'singleFile',
  'workbench.editor.closeOnFileDelete': true,
  'workbench.editor.limit.perEditorGroup': true,
  'breadcrumbs.enabled': true,
  'breadcrumbs.filePath': 'on',
  'breadcrumbs.symbolPath': 'on',
  'breadcrumbs.symbolSortOrder': 'position',
  'breadcrumbs.icons': false,
  'outline.collapseItems': 'collapsible',
  'outline.icons': true,
  'explorer.openEditors.visible': 0,
  'explorer.autoReveal': 'focusNoScroll',
  // JetBrains-like Find in Files: Ctrl+Shift+F opens the Point search window
  // (shell layer, see docs/search-window.md); Alt+2 opens the panel.
  // Folder-scoped search belongs in the editor area: the result stays visible
  // next to code and can be reopened from editor history.
  'search.mode': 'reuseEditor',
  'search.defaultViewMode': 'tree',
  'search.smartCase': true,
  'search.useIgnoreFiles': true,
  'search.useGlobalIgnoreFiles': true,
  'search.useParentIgnoreFiles': true,
  'search.searchOnType': false,
  'search.quickAccess.preserveInput': true,
  'search.collapseResults': 'always',
  'search.seedOnFocus': true,
  'search.showLineNumbers': true,
  'workbench.editor.editorActionsLocation': 'default',
  'workbench.sash.size': 2,
  'workbench.sash.hoverDelay': 80,
  'workbench.tree.indent': 12,
  'workbench.tree.renderIndentGuides': 'always',
  'diffEditor.hideUnchangedRegions.enabled': true,
  'diffEditor.renderSideBySide': true,
  // Апстрим сам роняет сравнение в одну колонку, когда редактору меньше 900px,
  // — а с открытой панелью Git это ровно рабочий случай: файл из списка
  // открывался «всё в одном», без второй версии рядом. Две колонки — то, ради
  // чего сравнение и открывают, поэтому подмену ширины мы отключаем.
  'diffEditor.useInlineViewWhenSpaceIsLimited': false,
  // Полок «скрытые строки» апстрим ставит по умолчанию с трёх строк: между
  // двумя близкими правками вырастает полка, которая занимает столько же
  // места, сколько спрятала, но требует щелчка. Шесть строк — та длина, ниже
  // которой прятать нечего; четыре строки соседства вместо трёх дают правке
  // видимую опору сверху и снизу.
  'diffEditor.hideUnchangedRegions.minimumLineCount': 6,
  'diffEditor.hideUnchangedRegions.contextLineCount': 4,
  'diffEditor.hideUnchangedRegions.revealLineCount': 30,
  'workbench.localHistory.enabled': true,
  'workbench.localHistory.maxFileEntries': 50,
  'workbench.reduceMotion': 'on',
  'window.openFoldersInNewWindow': 'off',
  'explorer.confirmDelete': true,
  'outline.problems.enabled': false,
  'outline.showVariables': false,
  'terminal.integrated.tabs.enabled': true,
  // Point has its own assistant; the upstream terminal-suggest banner invites
  // users into a second chat and adds noise before the first command.
  'terminal.integrated.suggest.enabled': false,
  'terminal.integrated.initialHint': false,
  'terminal.integrated.tabs.showTitle': true,
  'terminal.integrated.tabs.description': '${cwdFolder}',
  'terminal.integrated.tabs.hideCondition': 'never',
  'terminal.integrated.defaultLocation': 'view',
  'terminal.integrated.scrollback': 10000,
  'terminal.integrated.gpuAcceleration': 'off',
  'terminal.integrated.enableImages': false,
  'terminal.integrated.showExitAlert': false,
  'terminal.integrated.confirmOnExit': 'hasChildProcesses',
  'terminal.integrated.cursorStyle': 'line',
  'terminal.integrated.cursorBlinking': true,
  'terminal.integrated.lineHeight': 1.18,
  'terminal.integrated.letterSpacing': 0.4,
  'terminal.integrated.stickyScroll.enabled': true,
  'terminal.integrated.shellIntegration.decorationsEnabled': 'both',
  'terminal.integrated.shellIntegration.showCommandGuide': true,
  'debug.console.fontSize': 13,
  'debug.console.fontFamily': "'JetBrains Mono', Consolas, monospace",
  'debug.console.lineHeight': 18,
  'debug.console.closeOnEnd': false,
  'references.preferredLocation': 'view',
  'scm.defaultViewMode': 'tree',
  'scm.alwaysShowRepositories': true,
  'scm.repositories.visible': 10,
  'scm.showActionButton': false,
  'scm.inputMinLineCount': 3,
  'explorer.decorations.badges': true,
  'explorer.compactFolders': false,
  'explorer.fileNesting.enabled': false,
  'explorer.sortOrder': 'type',
  'explorer.expandSingleFolderWorkspaces': true,
  'workbench.tree.indent': 12,
  'workbench.tree.renderIndentGuides': 'always',
  'scm.providerCountBadge': 'auto',
  'scm.diffDecorations': 'gutter',
  'git.decorations.enabled': true,
  'debug.toolBarLocation': 'docked',
  'debug.showInlineBreakpointCandidates': false,
  'terminal.integrated.fontSize': 13,
  'terminal.integrated.fontFamily': "'JetBrains Mono', Consolas, monospace",
  'terminal.integrated.smoothScrolling': false,
  'terminal.integrated.enablePersistentSessions': false,
  'terminal.integrated.minimumContrastRatio': 4.5,
  'terminal.integrated.fastScrollSensitivity': 3,
  'extensions.autoCheckUpdates': false,
  'extensions.autoUpdate': false,
  'extensions.ignoreRecommendations': true,
  'update.mode': 'manual',
  'workbench.settings.enableNaturalLanguageSearch': false,
  'accessibility.signals.volume': 0,
  '[git-commit]': {
    'editor.rulers': [50, 72],
    'editor.wordWrap': 'off',
    'workbench.editor.restoreViewState': false,
  },
  // Code-OSS / Point cannot run Microsoft's proprietary vsce-sign check.
  // Without this, Marketplace installs fail with "signature verification was not executed".
  'extensions.verifySignature': false,
  // Point owns its AI surface and backend. Keep the upstream Microsoft
  // agent host and its account-gated Copilot flows completely inactive.
  'chat.agentHost.enabled': false,
  'chat.disableAIFeatures': true,
  'chat.titleBar.signIn.enabled': false,
  'chat.titleBar.openInAgentsWindow.enabled': false,
  'chat.agentsControl.enabled': 'hidden',
  // Microsoft onboarding assumes a Copilot-backed account. Point ships
  // its own walkthrough and never enters that upstream experiment.
  'workbench.welcomePage.experimentalOnboarding': false
};
delete extensionPackage.devDependencies;
extensionPackage.scripts = { check: extensionPackage.scripts?.check || 'node --check extension.js && node --check media/main.js' };
writeJson(extensionPackagePath, extensionPackage);

// The upstream Git extension contributes a VS Code documentation link to the
// empty Files view. Point keeps the useful clone action but owns the copy and
// does not send users to another product's onboarding.
const gitExtensionPackagePath = path.join(sourceRoot, 'extensions', 'git', 'package.json');
const gitExtensionPackage = readJson(gitExtensionPackagePath);
const gitViewsWelcome = gitExtensionPackage.contributes?.viewsWelcome;
if (!Array.isArray(gitViewsWelcome)) fail(`Git viewsWelcome contribution is missing: ${gitExtensionPackagePath}`);
const gitCloneWelcome = gitViewsWelcome.find(item => item.view === 'explorer' && item.contents === '%view.workbench.cloneRepository%');
if (!gitCloneWelcome && !gitViewsWelcome.some(item => item.view === 'explorer' && item.contents === '%view.point.cloneRepository%')) {
  fail(`Git explorer clone welcome contribution is missing: ${gitExtensionPackagePath}`);
}
if (gitCloneWelcome) gitCloneWelcome.contents = '%view.point.cloneRepository%';
gitExtensionPackage.contributes.viewsWelcome = gitViewsWelcome.filter(item => !(item.view === 'explorer' && item.contents === '%view.workbench.learnMore%'));
// Point exposes version control through its dedicated Versions button. Delay
// repository discovery and Git processes until that surface is actually used.
// Command and filesystem events keep clone/init and git: editors functional.
gitExtensionPackage.activationEvents = Array.from(new Set([
  ...(gitExtensionPackage.activationEvents || []).filter(event => event !== '*'),
  'onView:workbench.scm',
]));
writeJson(gitExtensionPackagePath, gitExtensionPackage);

// git-base is an API dependency of Git/GitHub. It does not need to create its
// model on every Point launch; dependency activation keeps the APIs available.
const gitBaseExtensionPackagePath = path.join(sourceRoot, 'extensions', 'git-base', 'package.json');
const gitBaseExtensionPackage = readJson(gitBaseExtensionPackagePath);
gitBaseExtensionPackage.activationEvents = Array.from(new Set([
  ...(gitBaseExtensionPackage.activationEvents || []).filter(event => event !== '*'),
  'onCommand:git-base.api.getRemoteSources',
  'onLanguage:git-commit',
  'onView:workbench.scm',
]));
writeJson(gitBaseExtensionPackagePath, gitBaseExtensionPackage);

// Upstream Emmet uses a wildcard onLanguage event, which activates it even on
// Point's empty editor and in Go projects. Keep automatic completions for the
// web languages Emmet actually serves while avoiding unrelated startup work.
const emmetExtensionPackagePath = path.join(sourceRoot, 'extensions', 'emmet', 'package.json');
const emmetExtensionPackage = readJson(emmetExtensionPackagePath);
const pointEmmetLanguages = [
  'html', 'css', 'scss', 'sass', 'less', 'stylus', 'xml', 'xsl',
  'javascript', 'javascriptreact', 'typescript', 'typescriptreact',
  'handlebars', 'razor', 'php', 'vue', 'svelte', 'astro', 'pug', 'slim', 'haml',
];
emmetExtensionPackage.activationEvents = Array.from(new Set([
  ...(emmetExtensionPackage.activationEvents || []).filter(event => event !== 'onLanguage'),
  ...pointEmmetLanguages.map(language => `onLanguage:${language}`),
]));
writeJson(emmetExtensionPackagePath, emmetExtensionPackage);

// Keep the local extension host asleep on a plain editor launch. Debug auto
// attach only needs to wake when a terminal actually exposes shell integration;
// its contributed command remains an implicit activation event.
const debugAutoLaunchPackagePath = path.join(sourceRoot, 'extensions', 'debug-auto-launch', 'package.json');
const debugAutoLaunchPackage = readJson(debugAutoLaunchPackagePath);
debugAutoLaunchPackage.activationEvents = Array.from(new Set([
  ...(debugAutoLaunchPackage.activationEvents || []).filter(event => event !== 'onStartupFinished'),
  'onTerminalShellIntegration:*',
]));
writeJson(debugAutoLaunchPackagePath, debugAutoLaunchPackage);

// Merely opening a package.json must not start the Node extension host and npm
// task scanner. Commands are implicit activation events; the hidden script
// explorer and task provider still activate npm when the user asks for them.
const npmPackagePath = path.join(sourceRoot, 'extensions', 'npm', 'package.json');
const npmPackage = readJson(npmPackagePath);
npmPackage.activationEvents = Array.from(new Set([
  ...(npmPackage.activationEvents || []).filter(event => event !== 'workspaceContains:package.json' && event !== 'onLanguage:json'),
  'onTaskType:npm',
  'onView:npm',
]));
writeJson(npmPackagePath, npmPackage);

// The legacy merge decorations should not allocate an extension host for every
// project. Wake them only while Git is in a merge/rebase state; command
// contributions still activate the extension on demand.
const mergeConflictPackagePath = path.join(sourceRoot, 'extensions', 'merge-conflict', 'package.json');
const mergeConflictPackage = readJson(mergeConflictPackagePath);
mergeConflictPackage.activationEvents = Array.from(new Set([
  ...(mergeConflictPackage.activationEvents || []).filter(event => event !== 'onStartupFinished'),
  'workspaceContains:.git/MERGE_HEAD',
  'workspaceContains:.git/rebase-merge',
  'workspaceContains:.git/rebase-apply',
]));
writeJson(mergeConflictPackagePath, mergeConflictPackage);

// Point keeps the desktop extension host dormant until an installed extension
// receives a real activation event. This removes one Electron/Node utility
// process from the idle IDE while preserving the ordinary extension API after
// the first command, view, language or workspace activation.
const nativeExtensionServicePath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'services', 'extensions', 'electron-browser', 'nativeExtensionService.ts');
replaceOnce(
  nativeExtensionServicePath,
  `\t\t\textensionEnablementService,
\t\t\tconfigurationService,
\t\t\tremoteAgentService,`,
  `\t\t\textensionEnablementService,
\t\t\tconfigurationService,
\t\t\tproductService,
\t\t\tremoteAgentService,`,
  'Point native extension-host product service argument',
);
replaceOnce(
  nativeExtensionServicePath,
  `\t\t@IWorkbenchExtensionEnablementService private readonly _extensionEnablementService: IWorkbenchExtensionEnablementService,
\t\t@IConfigurationService configurationService: IConfigurationService,
\t\t@IRemoteAgentService private readonly _remoteAgentService: IRemoteAgentService,`,
  `\t\t@IWorkbenchExtensionEnablementService private readonly _extensionEnablementService: IWorkbenchExtensionEnablementService,
\t\t@IConfigurationService configurationService: IConfigurationService,
\t\t@IProductService private readonly _productService: IProductService,
\t\t@IRemoteAgentService private readonly _remoteAgentService: IRemoteAgentService,`,
  'Point native extension-host product service',
);
replaceOnce(
  nativeExtensionServicePath,
  `\t\t\t\t\tisInitialStart
\t\t\t\t\t\t? ExtensionHostStartup.EagerManualStart
\t\t\t\t\t\t: ExtensionHostStartup.EagerAutoStart`,
  `\t\t\t\t\tisInitialStart
\t\t\t\t\t\t? (this._productService.applicationName === 'point' ? ExtensionHostStartup.LazyAutoStart : ExtensionHostStartup.EagerManualStart)
\t\t\t\t\t\t: ExtensionHostStartup.EagerAutoStart`,
  'Point lazy native extension-host startup',
);

const localProcessExtensionHostPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'services', 'extensions', 'electron-browser', 'localProcessExtensionHost.ts');
replaceOnce(
  localProcessExtensionHostPath,
  `\t\tpublic readonly startup: ExtensionHostStartup.EagerAutoStart | ExtensionHostStartup.EagerManualStart,`,
  `\t\tpublic readonly startup: ExtensionHostStartup.EagerAutoStart | ExtensionHostStartup.EagerManualStart | ExtensionHostStartup.LazyAutoStart,`,
  'Point lazy local-process extension-host type',
);
replaceOnce(
  localProcessExtensionHostPath,
  `\t\t\tautoStart: (this.startup === ExtensionHostStartup.EagerAutoStart),`,
  `\t\t\tautoStart: (this.startup === ExtensionHostStartup.EagerAutoStart || this.startup === ExtensionHostStartup.LazyAutoStart),`,
  'Point lazy local-process extension-host handshake',
);

const lazyExtensionHostManagerPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'services', 'extensions', 'common', 'lazyCreateExtensionHostManager.ts');
replaceOnce(
  lazyExtensionHostManagerPath,
  `import { URI } from '../../../../base/common/uri.js';
import { ExtensionIdentifier, IExtensionDescription } from '../../../../platform/extensions/common/extensions.js';`,
  `import { URI } from '../../../../base/common/uri.js';
import { ImplicitActivationEvents } from '../../../../platform/extensionManagement/common/implicitActivationEvents.js';
import { ExtensionIdentifier, IExtensionDescription } from '../../../../platform/extensions/common/extensions.js';`,
  'Point lazy extension-host implicit events import',
);
replaceOnce(
  lazyExtensionHostManagerPath,
  `import { ILogService } from '../../../../platform/log/common/log.js';
import { RemoteAuthorityResolverErrorCode } from '../../../../platform/remote/common/remoteAuthorityResolver.js';`,
  `import { ILogService } from '../../../../platform/log/common/log.js';
import { IProductService } from '../../../../platform/product/common/productService.js';
import { RemoteAuthorityResolverErrorCode } from '../../../../platform/remote/common/remoteAuthorityResolver.js';`,
  'Point lazy extension-host product service import',
);
replaceOnce(
  lazyExtensionHostManagerPath,
  `\tprivate _startCalled: Barrier;
\tprivate _actual: ExtensionHostManager | null;`,
  `\tprivate _startCalled: Barrier;
\tprivate _actual: ExtensionHostManager | null;
\tprivate _myExtensions: ExtensionIdentifier[] = [];
\tprivate readonly _myActivationEvents = new Set<string>();`,
  'Point lazy extension-host state',
);
replaceOnce(
  lazyExtensionHostManagerPath,
  `\t\t@IInstantiationService private readonly _instantiationService: IInstantiationService,
\t\t@ILogService private readonly _logService: ILogService
\t) {`,
  `\t\t@IInstantiationService private readonly _instantiationService: IInstantiationService,
\t\t@ILogService private readonly _logService: ILogService,
\t\t@IProductService private readonly _productService: IProductService,
\t) {`,
  'Point lazy extension-host product service',
);
replaceOnce(
  lazyExtensionHostManagerPath,
  `\tpublic containsExtension(extensionId: ExtensionIdentifier): boolean {
\t\treturn this._extensionHost.extensions?.containsExtension(extensionId) ?? false;
\t}`,
  `\tpublic containsExtension(extensionId: ExtensionIdentifier): boolean {
\t\treturn this._extensionHost.extensions?.containsExtension(extensionId) ?? this._myExtensions.some(candidate => ExtensionIdentifier.equals(candidate, extensionId));
\t}`,
  'Point lazy extension-host membership',
);
replaceOnce(
  lazyExtensionHostManagerPath,
  `\t\tif (this._actual) {
\t\t\treturn this._actual.activate(extension, reason);
\t\t}
\t\treturn false;`,
  `\t\tif (this._actual) {
\t\t\treturn this._actual.activate(extension, reason);
\t\t}
\t\tif (this._productService.applicationName === 'point' && this.containsExtension(extension)) {
\t\t\tconst actual = await this._getOrCreateActualAndStart(\`activate extension \${extension.value}\`);
\t\t\treturn actual.activate(extension, reason);
\t\t}
\t\treturn false;`,
  'Point lazy extension-host activation by id',
);
replaceOnce(
  lazyExtensionHostManagerPath,
  `\t\tif (activationKind === ActivationKind.Immediate) {
\t\t\t// this is an immediate request, so we cannot wait for start to be called
\t\t\tif (this._actual) {
\t\t\t\treturn this._actual.activateByEvent(activationEvent, activationKind);
\t\t\t}
\t\t\treturn;
\t\t}
\t\tawait this._startCalled.wait();
\t\tif (this._actual) {
\t\t\treturn this._actual.activateByEvent(activationEvent, activationKind);
\t\t}
\t}`,
  `\t\tif (activationKind === ActivationKind.Immediate && !this._startCalled.isOpen()) {
\t\t\t// this is an immediate request, so we cannot wait for start to be called
\t\t\treturn;
\t\t}
\t\tawait this._startCalled.wait();
\t\tif (this._actual) {
\t\t\treturn this._actual.activateByEvent(activationEvent, activationKind);
\t\t}
\t\tif (this._productService.applicationName === 'point' && this._myActivationEvents.has(activationEvent)) {
\t\t\tconst actual = await this._getOrCreateActualAndStart(\`activation event \${activationEvent}\`);
\t\t\treturn actual.activateByEvent(activationEvent, activationKind);
\t\t}
\t}`,
  'Point lazy extension-host activation by event',
);
replaceOnce(
  lazyExtensionHostManagerPath,
  `\tpublic async start(extensionRegistryVersionId: number, allExtensions: IExtensionDescription[], myExtensions: ExtensionIdentifier[]): Promise<void> {
\t\tif (myExtensions.length > 0) {`,
  `\tpublic async start(extensionRegistryVersionId: number, allExtensions: IExtensionDescription[], myExtensions: ExtensionIdentifier[]): Promise<void> {
\t\tthis._myExtensions = myExtensions;
\t\tthis._myActivationEvents.clear();
\t\tfor (const extension of allExtensions) {
\t\t\tif (myExtensions.some(identifier => ExtensionIdentifier.equals(identifier, extension.identifier))) {
\t\t\t\tfor (const activationEvent of ImplicitActivationEvents.readActivationEvents(extension)) {
\t\t\t\t\tthis._myActivationEvents.add(activationEvent);
\t\t\t\t}
\t\t\t}
\t\t}
\t\tif (this._productService.applicationName === 'point') {
\t\t\tthis._startCalled.open();
\t\t\tconst requestedActivationEvent = this._initialActivationEvents.find(event => this._myActivationEvents.has(event));
\t\t\tif (requestedActivationEvent) {
\t\t\t\tconst actual = this._createActual(\`initial activation event \${requestedActivationEvent}\`);
\t\t\t\treturn actual.ready();
\t\t\t}
\t\t\treturn;
\t\t}
\t\tif (myExtensions.length > 0) {`,
  'Point true lazy extension-host start',
);

const terminalServicePath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'terminal', 'browser', 'terminalService.ts');
replaceOnce(
  terminalServicePath,
  `import { INotificationService } from '../../../../platform/notification/common/notification.js';`,
  `import { INotificationService } from '../../../../platform/notification/common/notification.js';\nimport { IProductService } from '../../../../platform/product/common/productService.js';`,
  'Point terminal product service import',
);
replaceOnce(
  terminalServicePath,
  `\t\t@ITimerService private readonly _timerService: ITimerService,\n\t\t@IThemeService private readonly _themeService: IThemeService\n\t) {`,
  `\t\t@ITimerService private readonly _timerService: ITimerService,\n\t\t@IThemeService private readonly _themeService: IThemeService,\n\t\t@IProductService private readonly _productService: IProductService\n\t) {`,
  'Point terminal product service injection',
);
replaceOnce(
  terminalServicePath,
  `\t\tinstanceDisposables.add(Event.runAndSubscribe(instance.capabilities.onDidAddCapability, (() => {\n\t\t\tif (instance.capabilities.has(TerminalCapability.CommandDetection)) {\n\t\t\t\tthis._extensionService.activateByEvent(\`onTerminalShellIntegration:\${instance.shellType}\`);\n\t\t\t}\n\t\t})));`,
  `\t\tinstanceDisposables.add(Event.runAndSubscribe(instance.capabilities.onDidAddCapability, (() => {\n\t\t\tif (instance.capabilities.has(TerminalCapability.CommandDetection)) {\n\t\t\t\t// Point keeps the local extension host asleep until an extension is actually needed.\n\t\t\t\t// The eager host normally emits this wildcard event through its terminal API bridge;\n\t\t\t\t// emit it here to avoid a lazy-start chicken-and-egg for debug-auto-launch.\n\t\t\t\tif (this._productService.applicationName === 'point') {\n\t\t\t\t\tthis._extensionService.activateByEvent('onTerminalShellIntegration:*');\n\t\t\t\t}\n\t\t\t\tthis._extensionService.activateByEvent(\`onTerminalShellIntegration:\${instance.shellType}\`);\n\t\t\t}\n\t\t})));`,
  'Point lazy terminal shell integration activation',
);

const gitExtensionNlsPath = path.join(sourceRoot, 'extensions', 'git', 'package.nls.json');
const gitExtensionNls = readJson(gitExtensionNlsPath);
const pointGitMessages = {
  'config.terminalAuthentication': 'Использовать Point как обработчик аутентификации для процессов Git во встроенном терминале. После изменения перезапустите терминалы.',
  'config.terminalGitEditor': 'Использовать Point как редактор Git для процессов из встроенного терминала. После изменения перезапустите терминалы.',
  'view.point.cloneRepository': 'Или клонируйте существующий Git-репозиторий.\n[Клонировать репозиторий](command:git.clone)',
  'view.workbench.cloneRepository': 'Или клонируйте существующий Git-репозиторий.\n[Клонировать репозиторий](command:git.clone)',
  'view.workbench.learnMore': '',
  'view.workbench.scm.disabled': 'Git отключён для этого профиля.\n[Открыть настройки Git](command:workbench.action.openSettings?%5B%22git.enabled%22%5D)',
  'view.workbench.scm.empty': 'Откройте проект с Git-репозиторием или клонируйте его по URL.\n[Открыть проект](command:vscode.openFolder)\n[Клонировать репозиторий](command:git.cloneRecursive)',
  'view.workbench.scm.emptyWorkspace': 'Добавьте папку с Git-репозиторием.\n[Добавить папку](command:workbench.action.addRootFolder)',
  'view.workbench.scm.folder': 'В этом проекте ещё нет Git-репозитория.\n[Создать репозиторий](command:git.init?%5Btrue%5D)',
  'view.workbench.scm.workspace': 'В рабочем пространстве ещё нет Git-репозитория.\n[Создать репозиторий](command:git.init)',
  'view.workbench.scm.missing': 'Для версионирования установите Git, затем перезапустите Point.',
  'view.workbench.scm.missing.windows': 'Для версионирования установите Git.\n[Скачать Git для Windows](https://git-scm.com/download/win)\nПосле установки [перезапустите Point](command:workbench.action.reloadWindow) или [откройте журнал Git](command:git.showOutput).',
  'view.workbench.scm.missing.mac': 'Для версионирования установите Git.\n[Скачать Git для macOS](https://git-scm.com/download/mac)\nПосле установки [перезапустите Point](command:workbench.action.reloadWindow) или [откройте журнал Git](command:git.showOutput).',
  'view.workbench.scm.missing.linux': 'Для версионирования установите Git.\n[Скачать Git для Linux](https://git-scm.com/download/linux)\nПосле установки [перезапустите Point](command:workbench.action.reloadWindow) или [откройте журнал Git](command:git.showOutput).',
  'view.workbench.scm.repositoryInParentFolders': 'В родительской папке найден Git-репозиторий.\n[Открыть репозиторий](command:git.openRepositoriesInParentFolders)',
  'view.workbench.scm.repositoriesInParentFolders': 'В родительских папках найдены Git-репозитории.\n[Открыть репозитории](command:git.openRepositoriesInParentFolders)',
  'view.workbench.scm.unsafeRepository': 'Git считает найденный репозиторий потенциально небезопасным из-за владельца папки.\n[Управление небезопасными репозиториями](command:git.manageUnsafeRepositories)',
  'view.workbench.scm.unsafeRepositories': 'Git считает найденные репозитории потенциально небезопасными из-за владельцев папок.\n[Управление небезопасными репозиториями](command:git.manageUnsafeRepositories)',
  'view.workbench.scm.closedRepository': 'Найден ранее закрытый Git-репозиторий.\n[Открыть репозиторий снова](command:git.reopenClosedRepositories)',
  'view.workbench.scm.closedRepositories': 'Найдены ранее закрытые Git-репозитории.\n[Открыть репозитории снова](command:git.reopenClosedRepositories)',
};
Object.assign(gitExtensionNls, pointGitMessages);
writeJson(gitExtensionNlsPath, gitExtensionNls);

const languagePackSource = path.join(projectRoot, '.cache', 'language-packs', `vscode-language-pack-ru-${version.russianLanguagePack.version}`, 'extension');
const languagePackTarget = path.join(sourceRoot, 'extensions', 'vscode-language-pack-ru');
if (!fs.existsSync(path.join(languagePackSource, 'package.json'))) fail('Run install-language-pack.ps1 before applying the overlay');
assertInside(sourceRoot, languagePackTarget);
fs.rmSync(languagePackTarget, { recursive: true, force: true });
fs.cpSync(languagePackSource, languagePackTarget, { recursive: true });
const languagePackManifestPath = path.join(languagePackTarget, 'package.json');
const languagePackManifest = readJson(languagePackManifestPath);
const russianLocalization = languagePackManifest.contributes?.localizations?.find(item => item.languageId === 'ru');
if (!russianLocalization?.translations) fail(`Russian language-pack localization is missing: ${languagePackManifestPath}`);
const pointExtensionTranslation = {
  id: 'local-agent.local-agent-workbench',
  path: './translations/extensions/local-agent.local-agent-workbench.i18n.json',
};
if (!russianLocalization.translations.some(item => item.id === pointExtensionTranslation.id)) {
  russianLocalization.translations.push(pointExtensionTranslation);
}
writeJson(languagePackManifestPath, languagePackManifest);
// The Point extension already ships Russian source strings and does not call
// vscode.l10n directly. Built-in extensions are nevertheless expected to have
// a bundle in every active language pack; an explicit empty bundle prevents a
// misleading extension-host error on every startup.
writeJson(
  path.join(languagePackTarget, 'translations', 'extensions', 'local-agent.local-agent-workbench.i18n.json'),
  { contents: { bundle: {} } },
);
const gitTranslationPath = path.join(languagePackTarget, 'translations', 'extensions', 'vscode.git.i18n.json');
const gitTranslation = readJson(gitTranslationPath);
if (!gitTranslation.contents?.package) fail(`Bundled Russian Git translations are missing package messages: ${gitTranslationPath}`);
Object.assign(gitTranslation.contents.package, pointGitMessages);
if (!gitTranslation.contents?.bundle?.['Message ({0} to commit on "{1}")']) fail(`Bundled Russian Git commit placeholder is missing: ${gitTranslationPath}`);
gitTranslation.contents.bundle['Message ({0} to commit on "{1}")'] = 'Сообщение коммита · {0}';
gitTranslation.contents.bundle['Message ({0} to commit)'] = 'Сообщение коммита · {0}';
writeJson(gitTranslationPath, gitTranslation);

// Point uses JetBrains terminology for the stable reference-results tool
// window. The underlying language-provider protocol stays language agnostic.
const referencesTranslationPath = path.join(languagePackTarget, 'translations', 'extensions', 'vscode.references-view.i18n.json');
const referencesTranslation = readJson(referencesTranslationPath);
const referencesPackageTranslations = referencesTranslation.contents?.package;
const referencesBundleTranslations = referencesTranslation.contents?.bundle;
if (!referencesPackageTranslations || !referencesBundleTranslations) fail(`Reference view translations are missing: ${referencesTranslationPath}`);
Object.assign(referencesPackageTranslations, {
  'cmd.category.references': 'Использования',
  'cmd.references-view.findReferences': 'Найти использования',
  'cmd.references-view.next': 'Следующее использование',
  'cmd.references-view.prev': 'Предыдущее использование',
  'container.title': 'Использования',
  'displayName': 'Поиск использований',
  'view.title': 'Использования в проекте',
});
Object.assign(referencesBundleTranslations, {
  'Open Reference': 'Открыть использование',
  References: 'Использования',
  'Select previous reference search': 'Выбрать предыдущий поиск использований',
});
writeJson(referencesTranslationPath, referencesTranslation);

// Keep version control local by default. The upstream GitHub extension adds a
// prominent publish action to every folder without a repository, which leads
// directly into an account flow and competes with Point's local Git action.
const githubExtensionPackagePath = path.join(sourceRoot, 'extensions', 'github', 'package.json');
const githubExtensionPackage = readJson(githubExtensionPackagePath);
const githubViewsWelcome = githubExtensionPackage.contributes?.viewsWelcome;
if (!Array.isArray(githubViewsWelcome)) fail(`GitHub viewsWelcome contribution is missing: ${githubExtensionPackagePath}`);
githubExtensionPackage.contributes.viewsWelcome = githubViewsWelcome.filter(item => !(item.view === 'scm' && ['%welcome.publishFolder%', '%welcome.publishWorkspaceFolder%'].includes(item.contents)));
// GitHub integration is useful once the user opens Versions, but it should not
// run or request account state while Point is idle on the editor/home screen.
githubExtensionPackage.activationEvents = Array.from(new Set([
  ...(githubExtensionPackage.activationEvents || []).filter(event => event !== '*'),
  'onView:workbench.scm',
]));
writeJson(githubExtensionPackagePath, githubExtensionPackage);

// Core SCM views are not extension-contributed, so upstream does not emit an
// onView activation event for them. Emit it when the SCM container is created;
// Point can then keep Git/GitHub asleep until the user first opens Versions.
const scmViewPaneContainerPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'scm', 'browser', 'scmViewPaneContainer.ts');
replaceOnce(
  scmViewPaneContainerPath,
  "\t\tparent.classList.add('scm-viewlet');",
  "\t\tparent.classList.add('scm-viewlet');\n\t\tvoid this.extensionService.activateByEvent('onView:workbench.scm');",
  'Point lazy SCM activation',
);
const pointScmTrustContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'scm', 'browser', 'scm.contribution.ts');
replaceOnce(
  pointScmTrustContributionPath,
  `viewsRegistry.registerViewWelcomeContent(VIEW_PANE_ID, {
\tcontent: localize('no open repo in an untrusted workspace', "None of the registered source control providers work in Restricted Mode."),
\twhen: ContextKeyExpr.and(ContextKeyExpr.equals('scm.providerCount', 0), WorkspaceTrustContext.IsEnabled, WorkspaceTrustContext.IsTrusted.toNegated())
});`,
  `viewsRegistry.registerViewWelcomeContent(VIEW_PANE_ID, {
\tcontent: product.applicationName === 'point'
\t\t? localize('point.untrustedScm', "Чтобы включить Git, разрешите Point выполнять инструменты в этом проекте.")
\t\t: localize('no open repo in an untrusted workspace', "None of the registered source control providers work in Restricted Mode."),
\twhen: ContextKeyExpr.and(ContextKeyExpr.equals('scm.providerCount', 0), WorkspaceTrustContext.IsEnabled, WorkspaceTrustContext.IsTrusted.toNegated())
});`,
  'Point restricted-mode Git copy',
);
replaceOnce(
  pointScmTrustContributionPath,
  `\tcontent: \`[\${localize('manageWorkspaceTrustAction', "Manage Workspace Trust")}](command:\${MANAGE_TRUST_COMMAND_ID})\`,`,
  `\tcontent: \`[\${product.applicationName === 'point' ? localize('point.allowGit', "Разрешить Git для проекта") : localize('manageWorkspaceTrustAction', "Manage Workspace Trust")}](command:\${MANAGE_TRUST_COMMAND_ID})\`,`,
  'Point restricted-mode Git action',
);

// Keep the Files surface focused on the project tree. Point already exposes
// File Structure and symbol navigation through search/shortcuts, so the two
// upstream Explorer panes must not return from a persisted Code-OSS profile.
const outlineContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'outline', 'browser', 'outline.contribution.ts');
replaceOnce(
  outlineContributionPath,
  "import { IOutlinePane } from './outline.js';",
  "import { IOutlinePane } from './outline.js';\nimport product from '../../../../platform/product/common/product.js';",
  'Point outline product import',
);
replaceOnce(
  outlineContributionPath,
  '\thideByDefault: false,',
  "\thideByDefault: product.applicationName === 'point',",
  'Point hidden-by-default Outline',
);
replaceOnce(
  outlineContributionPath,
  'Registry.as<IViewsRegistry>(ViewExtensions.ViewsRegistry).registerViews([{',
  "if (product.applicationName !== 'point') {\nRegistry.as<IViewsRegistry>(ViewExtensions.ViewsRegistry).registerViews([{",
  'Point Outline registration guard',
);
replaceOnce(
  outlineContributionPath,
  '}], VIEW_CONTAINER);\n\n// --- configurations',
  '}], VIEW_CONTAINER);\n}\n\n// --- configurations',
  'Point Outline registration guard end',
);
const timelineContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'timeline', 'browser', 'timeline.contribution.ts');
replaceOnce(
  timelineContributionPath,
  "import { URI } from '../../../../base/common/uri.js';",
  "import { URI } from '../../../../base/common/uri.js';\nimport product from '../../../../platform/product/common/product.js';",
  'Point timeline product import',
);
replaceOnce(
  timelineContributionPath,
  '\treadonly hideByDefault = false;',
  "\treadonly hideByDefault = product.applicationName === 'point';",
  'Point hidden-by-default Timeline',
);
replaceOnce(
  timelineContributionPath,
  'Registry.as<IViewsRegistry>(ViewExtensions.ViewsRegistry).registerViews([new TimelinePaneDescriptor()], VIEW_CONTAINER);',
  "if (product.applicationName !== 'point') {\n\tRegistry.as<IViewsRegistry>(ViewExtensions.ViewsRegistry).registerViews([new TimelinePaneDescriptor()], VIEW_CONTAINER);\n}",
  'Point Timeline registration guard',
);

// Point owns the first-run trust explanation. Keep the security boundary but
// avoid upstream documentation links and use project-oriented Russian copy.
const workspaceContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'workspace', 'browser', 'workspace.contribution.ts');
replaceOnce(
  workspaceContributionPath,
  `import { IFileService } from '../../../../platform/files/common/files.js';`,
  `import { IFileService } from '../../../../platform/files/common/files.js';\nimport product from '../../../../platform/product/common/product.js';`,
  'Point workspace product import',
);
replaceOnce(
  workspaceContributionPath,
  `import './media/workspaceTrustEditor.css';`,
  `import './media/workspaceTrustEditor.css';\nimport './media/pointWorkspaceTrust.css';`,
  'Point project security stylesheet import',
);
replaceOnce(
  workspaceContributionPath,
  `\t\t\tconst title = titleString ?? (this.useWorkspaceLanguage ?
\t\t\t\tlocalize('workspaceTrust', "Do you trust the authors of the files in this workspace?") :
\t\t\t\tlocalize('folderTrust', "Do you trust the authors of the files in this folder?"));`,
  `\t\t\tconst title = titleString ?? (this.productService.applicationName === 'point' ?
\t\t\t\t(this.useWorkspaceLanguage ? localize('point.workspaceTrust', "Вы доверяете этому рабочему пространству?") : localize('point.folderTrust', "Вы доверяете этому проекту?")) :
\t\t\t\t(this.useWorkspaceLanguage ? localize('workspaceTrust', "Do you trust the authors of the files in this workspace?") : localize('folderTrust', "Do you trust the authors of the files in this folder?")));`,
  'Point workspace trust title',
);
replaceOnce(
  workspaceContributionPath,
  `\t\t\t\tcheckboxText = localize('checkboxString', "Trust the authors of all files in the parent folder '{0}'", name);`,
  `\t\t\t\tcheckboxText = this.productService.applicationName === 'point'
\t\t\t\t\t? localize('point.checkboxParentTrust', "Доверять всем проектам в родительской папке «{0}»", name)
\t\t\t\t\t: localize('checkboxString', "Trust the authors of all files in the parent folder '{0}'", name);`,
  'Point parent trust copy',
);
replaceOnce(
  workspaceContributionPath,
  `\t\t\t\tlearnMoreString ?? localize('startupTrustRequestLearnMore', "If you don't trust the authors of these files, we recommend to continue in restricted mode as the files may be malicious. See [our docs](https://aka.ms/vscode-workspace-trust) to learn more."),`,
  `\t\t\t\tlearnMoreString ?? (this.productService.applicationName === 'point'
\t\t\t\t\t? localize('point.startupTrustRequestLearnMore', "Если проект вам незнаком, откройте его безопасно: Point не будет запускать команды и расширения. Доверие можно изменить позже.")
\t\t\t\t\t: localize('startupTrustRequestLearnMore', "If you don't trust the authors of these files, we recommend to continue in restricted mode as the files may be malicious. See [our docs](https://aka.ms/vscode-workspace-trust) to learn more.")),`,
  'Point workspace trust explanation',
);
replaceOnce(
  workspaceContributionPath,
  `\t\t\t\t{ label: trustOption ?? localize({ key: 'trustOption', comment: ['&& denotes a mnemonic'] }, "&&Yes, I trust the authors"), sublabel: isSingleFolderWorkspace ? localize('trustFolderOptionDescription', "Trust folder and enable all features") : localize('trustWorkspaceOptionDescription', "Trust workspace and enable all features") },
\t\t\t\t{ label: dontTrustOption ?? localize({ key: 'dontTrustOption', comment: ['&& denotes a mnemonic'] }, "&&No, I don't trust the authors"), sublabel: isSingleFolderWorkspace ? localize('dontTrustFolderOptionDescription', "Open folder in restricted mode") : localize('dontTrustWorkspaceOptionDescription', "Open workspace in restricted mode") },`,
  `\t\t\t\t{ label: trustOption ?? (this.productService.applicationName === 'point' ? localize({ key: 'point.trustOption', comment: ['&& denotes a mnemonic'] }, "&&Доверять проекту") : localize({ key: 'trustOption', comment: ['&& denotes a mnemonic'] }, "&&Yes, I trust the authors")), sublabel: this.productService.applicationName === 'point' ? localize('point.trustOptionDescription', "Включить Git, терминал, расширения и ИИ-инструменты") : (isSingleFolderWorkspace ? localize('trustFolderOptionDescription', "Trust folder and enable all features") : localize('trustWorkspaceOptionDescription', "Trust workspace and enable all features")) },
\t\t\t\t{ label: dontTrustOption ?? (this.productService.applicationName === 'point' ? localize({ key: 'point.dontTrustOption', comment: ['&& denotes a mnemonic'] }, "&&Открыть безопасно") : localize({ key: 'dontTrustOption', comment: ['&& denotes a mnemonic'] }, "&&No, I don't trust the authors")), sublabel: this.productService.applicationName === 'point' ? localize('point.dontTrustOptionDescription', "Не запускать команды и расширения") : (isSingleFolderWorkspace ? localize('dontTrustFolderOptionDescription', "Open folder in restricted mode") : localize('dontTrustWorkspaceOptionDescription', "Open workspace in restricted mode")) },`,
  'Point workspace trust actions',
);
replaceOnce(
  workspaceContributionPath,
  `\t\tconst actions =
\t\t\t[
\t\t\t\t{
\t\t\t\t\tlabel: localize('restrictedModeBannerManage', "Manage"),`,
  `\t\tconst actions = this.productService.applicationName === 'point'
\t\t\t? [
\t\t\t\t{
\t\t\t\t\tlabel: localize('point.configureProjectAccess', "Настроить доступ"),
\t\t\t\t\thref: 'command:' + MANAGE_TRUST_COMMAND_ID
\t\t\t\t}
\t\t\t]
\t\t\t: [
\t\t\t\t{
\t\t\t\t\tlabel: localize('restrictedModeBannerManage', "Manage"),`,
  'Point safe-mode banner actions',
);
replaceOnce(
  workspaceContributionPath,
  `\tprivate getBannerItemAriaLabels(): string {
\t\tswitch (this.workspaceContextService.getWorkbenchState()) {`,
  `\tprivate getBannerItemAriaLabels(): string {
\t\tif (this.productService.applicationName === 'point') {
\t\t\treturn localize('point.safeModeBannerAriaLabel', "Проект открыт безопасно. Команды и инструменты не запускаются. Откройте настройки доступа, чтобы включить их.");
\t\t}

\t\tswitch (this.workspaceContextService.getWorkbenchState()) {`,
  'Point safe-mode banner accessible label',
);
replaceOnce(
  workspaceContributionPath,
  `\tprivate getBannerItemMessages(): string {
\t\tswitch (this.workspaceContextService.getWorkbenchState()) {`,
  `\tprivate getBannerItemMessages(): string {
\t\tif (this.productService.applicationName === 'point') {
\t\t\treturn localize('point.safeModeBannerMessage', "Проект открыт безопасно: Git, терминал, расширения и ИИ-инструменты не запускаются.");
\t\t}

\t\tswitch (this.workspaceContextService.getWorkbenchState()) {`,
  'Point safe-mode banner message',
);
replaceOnce(
  workspaceContributionPath,
  `\t\t\tconst markdownStrings = [
\t\t\t\t!isSingleFolderWorkspace ?
\t\t\t\t\tlocalize('workspaceStartupTrustDetails', "{0} provides features that may automatically execute files in this workspace.", this.productService.nameShort) :
\t\t\t\t\tlocalize('folderStartupTrustDetails', "{0} provides features that may automatically execute files in this folder.", this.productService.nameShort),`,
  `\t\t\tconst markdownStrings = [
\t\t\t\tthis.productService.applicationName === 'point'
\t\t\t\t\t? localize('point.startupTrustDetails', "Point может запускать код, команды и инструменты этого проекта.")
\t\t\t\t\t: (!isSingleFolderWorkspace ?
\t\t\t\t\t\tlocalize('workspaceStartupTrustDetails', "{0} provides features that may automatically execute files in this workspace.", this.productService.nameShort) :
\t\t\t\t\t\tlocalize('folderStartupTrustDetails', "{0} provides features that may automatically execute files in this folder.", this.productService.nameShort)),`,
  'Point workspace trust capability explanation',
);
replaceOnce(
  workspaceContributionPath,
  `\t\tlocalize('workspaceTrustEditor', "Workspace Trust Editor")`,
  `\t\tproduct.applicationName === 'point' ? localize('point.projectSecurityEditor', "Безопасность проекта") : localize('workspaceTrustEditor', "Workspace Trust Editor")`,
  'Point project security editor label',
);
replaceOnce(
  workspaceContributionPath,
  `const WORKSPACES_CATEGORY = localize2('workspacesCategory', 'Workspaces');`,
  `const WORKSPACES_CATEGORY = product.applicationName === 'point' ? localize2('point.projectsCategory', 'Проекты') : localize2('workspacesCategory', 'Workspaces');`,
  'Point project command category',
);
replaceOnce(
  workspaceContributionPath,
  `\t\t\ttitle: localize2('configureWorkspaceTrustSettings', "Configure Workspace Trust Settings"),`,
  `\t\t\ttitle: product.applicationName === 'point' ? localize2('point.configureProjectSecurity', "Настройки безопасности проектов") : localize2('configureWorkspaceTrustSettings', "Configure Workspace Trust Settings"),`,
  'Point project security settings command',
);
replaceOnce(
  workspaceContributionPath,
  `\t\t\ttitle: localize2('manageWorkspaceTrust', "Manage Workspace Trust"),`,
  `\t\t\ttitle: product.applicationName === 'point' ? localize2('point.manageProjectSecurity', "Безопасность проекта") : localize2('manageWorkspaceTrust', "Manage Workspace Trust"),`,
  'Point project security command',
);
replaceOnce(
  workspaceContributionPath,
  `\t\t\t\tdescription: localize('workspace.trust.description', "Controls whether or not Workspace Trust is enabled within VS Code."),`,
  `\t\t\t\tdescription: product.applicationName === 'point' ? localize('point.workspaceTrust.description', "Запрашивать разрешение перед запуском команд, расширений и инструментов проекта.") : localize('workspace.trust.description', "Controls whether or not Workspace Trust is enabled within VS Code."),`,
  'Point project security setting description',
);
replaceOnce(
  workspaceContributionPath,
  `\t\t\t\tmarkdownDescription: localize('workspace.trust.emptyWindow.description', "Controls whether or not the empty window is trusted by default within VS Code. When used with \`#{0}#\`, you can enable the full functionality of VS Code without prompting in an empty window.", WORKSPACE_TRUST_UNTRUSTED_FILES),`,
  `\t\t\t\tmarkdownDescription: product.applicationName === 'point' ? localize('point.workspace.trust.emptyWindow.description', "Считать пустое окно безопасным для запуска локальных инструментов. Эта настройка действует вместе с \`#{0}#\`.", WORKSPACE_TRUST_UNTRUSTED_FILES) : localize('workspace.trust.emptyWindow.description', "Controls whether or not the empty window is trusted by default within VS Code. When used with \`#{0}#\`, you can enable the full functionality of VS Code without prompting in an empty window.", WORKSPACE_TRUST_UNTRUSTED_FILES),`,
  'Point empty-window security setting description',
);

const workspaceTrustEditorInputPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'services', 'workspaces', 'browser', 'workspaceTrustEditorInput.ts');
replaceOnce(
  workspaceTrustEditorInputPath,
  `import { registerIcon } from '../../../../platform/theme/common/iconRegistry.js';`,
  `import { registerIcon } from '../../../../platform/theme/common/iconRegistry.js';\nimport { IProductService } from '../../../../platform/product/common/productService.js';`,
  'Point project security input product import',
);
replaceOnce(
  workspaceTrustEditorInputPath,
  `export class WorkspaceTrustEditorInput extends EditorInput {
\tstatic readonly ID: string = 'workbench.input.workspaceTrust';`,
  `export class WorkspaceTrustEditorInput extends EditorInput {
\tstatic readonly ID: string = 'workbench.input.workspaceTrust';

\tconstructor(@IProductService private readonly productService: IProductService) {
\t\tsuper();
\t}`,
  'Point project security input product service',
);
replaceOnce(
  workspaceTrustEditorInputPath,
  `\t\treturn localize('workspaceTrustEditorInputName', "Workspace Trust");`,
  `\t\treturn this.productService.applicationName === 'point' ? localize('point.projectSecurity', "Безопасность проекта") : localize('workspaceTrustEditorInputName', "Workspace Trust");`,
  'Point project security input name',
);

const workspaceTrustEditorPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'workspace', 'browser', 'workspaceTrustEditor.ts');
replaceOnce(
  workspaceTrustEditorPath,
  `import { IProductService } from '../../../../platform/product/common/productService.js';`,
  `import { IProductService } from '../../../../platform/product/common/productService.js';\nimport product from '../../../../platform/product/common/product.js';`,
  'Point project security product import',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\t\t\t\tlabel: localize('hostColumnLabel', "Host"),`,
  `\t\t\t\t\tlabel: product.applicationName === 'point' ? localize('point.sourceColumnLabel', "Источник") : localize('hostColumnLabel', "Host"),`,
  'Point trusted folder source column',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\t\t\t\tlabel: localize('pathColumnLabel', "Path"),`,
  `\t\t\t\t\tlabel: product.applicationName === 'point' ? localize('point.pathColumnLabel', "Путь") : localize('pathColumnLabel', "Path"),`,
  'Point trusted folder path column',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\tthis.descriptionElement.innerText = entries.length ?
\t\t\tlocalize('trustedFoldersDescription', "You trust the following folders, their subfolders, and workspace files.") :
\t\t\tlocalize('noTrustedFoldersDescriptions', "You haven't trusted any folders or workspace files yet.");`,
  `\t\tthis.descriptionElement.innerText = product.applicationName === 'point'
\t\t\t? (entries.length
\t\t\t\t? localize('point.trustedFoldersDescription', "Point разрешает запуск инструментов в этих папках и вложенных проектах.")
\t\t\t\t: localize('point.noTrustedFoldersDescription', "Пока нет папок с постоянным доверием."))
\t\t\t: (entries.length
\t\t\t\t? localize('trustedFoldersDescription', "You trust the following folders, their subfolders, and workspace files.")
\t\t\t\t: localize('noTrustedFoldersDescriptions', "You haven't trusted any folders or workspace files yet."));`,
  'Point trusted folders description',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\tthis.rootElement = append(parent, $('.workspace-trust-editor', { tabindex: '0' }));`,
  `\t\tthis.rootElement = append(parent, $('.workspace-trust-editor', { tabindex: '0' }));\n\t\tthis.rootElement.classList.toggle('point-workspace-trust', this.productService.applicationName === 'point');`,
  'Point project security editor class',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\tprivate getHeaderTitleText(trusted: boolean): string {
\t\tif (trusted) {`,
  `\tprivate getHeaderTitleText(trusted: boolean): string {
\t\tif (this.productService.applicationName === 'point') {
\t\t\treturn trusted
\t\t\t\t? localize('point.projectToolsEnabled', "Проекту разрешён запуск инструментов")
\t\t\t\t: localize('point.projectOpenedSafely', "Проект открыт безопасно");
\t\t}

\t\tif (trusted) {`,
  'Point project security header title',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\tprivate getFeaturesHeaderText(trusted: boolean): [string, string] {
\t\tlet title: string = '';`,
  `\tprivate getFeaturesHeaderText(trusted: boolean): [string, string] {
\t\tif (this.productService.applicationName === 'point') {
\t\t\treturn trusted
\t\t\t\t? [localize('point.fullMode', "Полный режим"), localize('point.fullModeDescription', "Point может использовать инструменты этого проекта:")]
\t\t\t\t: [localize('point.safeMode', "Безопасный режим"), localize('point.safeModeDescription', "Point не запускает инструменты этого проекта:")];
\t\t}

\t\tlet title: string = '';`,
  'Point security mode card titles',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\theaderDescriptionText.innerText = isWorkspaceTrusted ?
\t\t\tlocalize('trustedDescription', "All features are enabled because trust has been granted to the workspace.") :
\t\t\tlocalize('untrustedDescription', "{0} is in a restricted mode intended for safe code browsing.", this.productService.nameShort);`,
  `\t\theaderDescriptionText.innerText = this.productService.applicationName === 'point'
\t\t\t? (isWorkspaceTrusted
\t\t\t\t? localize('point.trustedDescription', "Git, терминал, расширения и ИИ-инструменты доступны для этого проекта.")
\t\t\t\t: localize('point.untrustedDescription', "Можно читать и редактировать файлы, но Point не запустит команды без разрешения."))
\t\t\t: (isWorkspaceTrusted
\t\t\t\t? localize('trustedDescription', "All features are enabled because trust has been granted to the workspace.")
\t\t\t\t: localize('untrustedDescription', "{0} is in a restricted mode intended for safe code browsing.", this.productService.nameShort));`,
  'Point project security header description',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\tconst headerDescriptionActionsText = localize({ key: 'workspaceTrustEditorHeaderActions', comment: ['Please ensure the markdown link syntax is not broken up with whitespace [text block](link block)'] }, "[Configure your settings]({0}) or [learn more](https://aka.ms/vscode-workspace-trust).", \`command:workbench.trust.configure\`);`,
  `\t\tconst headerDescriptionActionsText = this.productService.applicationName === 'point'
\t\t\t? localize({ key: 'point.projectSecuritySettings', comment: ['Please ensure the markdown link syntax is not broken up with whitespace [text block](link block)'] }, "[Настройки безопасности]({0})", \`command:workbench.trust.configure\`)
\t\t\t: localize({ key: 'workspaceTrustEditorHeaderActions', comment: ['Please ensure the markdown link syntax is not broken up with whitespace [text block](link block)'] }, "[Configure your settings]({0}) or [learn more](https://aka.ms/vscode-workspace-trust).", \`command:workbench.trust.configure\`);`,
  'Point project security settings link',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\tthis.rootElement.setAttribute('aria-label', \`\${localize('root element label', "Manage Workspace Trust")}:  \${this.headerContainer.innerText}\`);`,
  `\t\tthis.rootElement.setAttribute('aria-label', \`\${this.productService.applicationName === 'point' ? localize('point.projectSecurityAriaLabel', "Безопасность проекта") : localize('root element label', "Manage Workspace Trust")}:  \${this.headerContainer.innerText}\`);`,
  'Point project security accessible label',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\tconfigurationTitle.innerText = localize('trustedFoldersAndWorkspaces', "Trusted Folders & Workspaces");`,
  `\t\tconfigurationTitle.innerText = this.productService.applicationName === 'point' ? localize('point.trustedProjectsAndFolders', "Доверенные проекты и папки") : localize('trustedFoldersAndWorkspaces', "Trusted Folders & Workspaces");`,
  'Point trusted projects title',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\tconst trustedContainerItems = this.workspaceService.getWorkbenchState() === WorkbenchState.EMPTY ?`,
  `\t\tconst trustedContainerItems = this.productService.applicationName === 'point' ?
\t\t\t[
\t\t\t\tlocalize('point.trustedGitTerminal', "Git и терминал доступны"),
\t\t\t\tlocalize('point.trustedRunDebug', "Запуск и отладка включены"),
\t\t\t\tlocalize('point.trustedSettings', "Настройки проекта применяются"),
\t\t\t\tlocalize('point.trustedExtensionsAi', "Расширения и ИИ-инструменты активны")
\t\t\t] : this.workspaceService.getWorkbenchState() === WorkbenchState.EMPTY ?`,
  'Point trusted mode features',
);
replaceOnce(
  workspaceTrustEditorPath,
  `\t\tconst untrustedContainerItems = this.workspaceService.getWorkbenchState() === WorkbenchState.EMPTY ?`,
  `\t\tconst untrustedContainerItems = this.productService.applicationName === 'point' ?
\t\t\t[
\t\t\t\tlocalize('point.untrustedGitTerminal', "Git, терминал и задачи не запускаются"),
\t\t\t\tlocalize('point.untrustedDebug', "Отладка отключена"),
\t\t\t\tlocalize('point.untrustedSettings', "Опасные настройки проекта не применяются"),
\t\t\t\tlocalize('point.untrustedExtensionsAi', "Расширения и ИИ-инструменты ограничены")
\t\t\t] : this.workspaceService.getWorkbenchState() === WorkbenchState.EMPTY ?`,
  'Point safe mode features',
);
replaceOnce(
  workspaceTrustEditorPath,
  `new Action('workspace.trust.button.action.grant', localize('trustButton', "Trust"),`,
  `new Action('workspace.trust.button.action.grant', this.productService.applicationName === 'point' ? localize('point.trustProjectButton', "Доверять проекту") : localize('trustButton', "Trust"),`,
  'Point trust project button',
);
replaceOnce(
  workspaceTrustEditorPath,
  `trustMessageElement.innerText = localize('trustMessage', "Trust the authors of all files in the current folder or its parent '{0}'.", name);`,
  `trustMessageElement.innerText = this.productService.applicationName === 'point' ? localize('point.trustMessage', "Разрешить инструменты только для проекта или для всей папки «{0}»?", name) : localize('trustMessage', "Trust the authors of all files in the current folder or its parent '{0}'.", name);`,
  'Point trust scope message',
);
replaceOnce(
  workspaceTrustEditorPath,
  `new Action('workspace.trust.button.action.grantParent', localize('trustParentButton', "Trust Parent"),`,
  `new Action('workspace.trust.button.action.grantParent', this.productService.applicationName === 'point' ? localize('point.trustParentButton', "Доверять папке «{0}»", name) : localize('trustParentButton', "Trust Parent"),`,
  'Point trust parent button',
);
replaceOnce(
  workspaceTrustEditorPath,
  `new Action('workspace.trust.button.action.deny', localize('dontTrustButton', "Don't Trust"),`,
  `new Action('workspace.trust.button.action.deny', this.productService.applicationName === 'point' ? localize('point.openSafelyButton', "Открыть безопасно") : localize('dontTrustButton', "Don't Trust"),`,
  'Point safe mode button',
);

const pointWorkspaceTrustCssSource = path.join(import.meta.dirname, 'resources', 'point-workspace-trust.css');
if (!fs.existsSync(pointWorkspaceTrustCssSource)) fail(`Point project security stylesheet is missing: ${pointWorkspaceTrustCssSource}`);
fs.copyFileSync(pointWorkspaceTrustCssSource, path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'workspace', 'browser', 'media', 'pointWorkspaceTrust.css'));

// Turn the generic Open Recent picker behind Point's titlebar switcher into a
// project-focused surface: short copy, recent projects only, and a direct path
// to choosing a different local folder.
const windowActionsPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'actions', 'windowActions.ts');
replaceOnce(
  windowActionsPath,
  "import { CommandsRegistry } from '../../../platform/commands/common/commands.js';",
  "import { CommandsRegistry, ICommandService } from '../../../platform/commands/common/commands.js';",
  'Point recent-project command service import',
);
replaceOnce(
  windowActionsPath,
  "import { getActiveElement, getActiveWindow, isHTMLElement } from '../../../base/browser/dom.js';",
  "import { getActiveElement, getActiveWindow, isHTMLElement } from '../../../base/browser/dom.js';\nimport product from '../../../platform/product/common/product.js';",
  'Point recent-project product import',
);
replaceOnce(
  windowActionsPath,
  "import { FileKind } from '../../../platform/files/common/files.js';",
  "import { FileKind, IFileService } from '../../../platform/files/common/files.js';",
  'Point recent-project file service import',
);
if (!fs.readFileSync(windowActionsPath, 'utf8').includes('interface IOpenPointProjectPick extends IQuickPickItem')) {
  replaceOnce(
    windowActionsPath,
    `interface IRecentlyOpenedPick extends IQuickPickItem {
\tresource: URI;
\topenable: IWindowOpenable;
\tremoteAuthority: string | undefined;
}`,
    `interface IRecentlyOpenedPick extends IQuickPickItem {
\tresource: URI;
\topenable: IWindowOpenable;
\tremoteAuthority: string | undefined;
}

interface IOpenPointProjectPick extends IQuickPickItem {
\topenPointProject: true;
}`,
    'Point open-project picker type',
  );
}
replaceOnce(
  windowActionsPath,
  `\t\tconst hostService = accessor.get(IHostService);
\t\tconst dialogService = accessor.get(IDialogService);`,
  `\t\tconst hostService = accessor.get(IHostService);
\t\tconst dialogService = accessor.get(IDialogService);
\t\tconst commandService = accessor.get(ICommandService);`,
  'Point recent-project command service',
);
replaceOnce(
  windowActionsPath,
  `\t\tconst commandService = accessor.get(ICommandService);`,
  `\t\tconst commandService = accessor.get(ICommandService);
\t\tconst fileService = accessor.get(IFileService);`,
  'Point recent-project file service',
);
replaceAny(
  windowActionsPath,
  [
    `\t\tconst filePicks = recentlyOpened.files.map(p => this.toQuickPick(modelService, languageService, labelService, p, { isDirty: false, windowState: undefined }));`,
    `\t\tif (product.applicationName === 'point') {
\t\t\tconst existing = await Promise.all(workspacePicks.map(async pick => pick.resource.scheme !== 'file' || await fileService.exists(pick.resource)));
\t\t\tconst staleResources = workspacePicks.filter((_, index) => !existing[index]).map(pick => pick.resource);
\t\t\tif (staleResources.length) {
\t\t\t\tawait workspacesService.removeRecentlyOpened(staleResources);
\t\t\t\tfor (let index = workspacePicks.length - 1; index >= 0; index--) {
\t\t\t\t\tif (!existing[index]) {
\t\t\t\t\t\tworkspacePicks.splice(index, 1);
\t\t\t\t\t}
\t\t\t\t}
\t\t\t}
\t\t}

\t\tconst filePicks = recentlyOpened.files.map(p => this.toQuickPick(modelService, languageService, labelService, p, { isDirty: false, windowState: undefined }));`,
  ],
  `\t\tif (product.applicationName === 'point') {
\t\t\tconst visible = await Promise.all(workspacePicks.map(async pick => {
\t\t\t\tconst internalAgentsWorkspace = pick.resource.scheme === 'file' && /\\/agent-sessions\\.code-workspace$/i.test(pick.resource.path);
\t\t\t\treturn !internalAgentsWorkspace && (pick.resource.scheme !== 'file' || await fileService.exists(pick.resource));
\t\t\t}));
\t\t\tconst hiddenResources = workspacePicks.filter((_, index) => !visible[index]).map(pick => pick.resource);
\t\t\tif (hiddenResources.length) {
\t\t\t\tawait workspacesService.removeRecentlyOpened(hiddenResources);
\t\t\t\tfor (let index = workspacePicks.length - 1; index >= 0; index--) {
\t\t\t\t\tif (!visible[index]) {
\t\t\t\t\t\tworkspacePicks.splice(index, 1);
\t\t\t\t\t}
\t\t\t\t}
\t\t\t}
\t\t}

\t\tconst filePicks = recentlyOpened.files.map(p => this.toQuickPick(modelService, languageService, labelService, p, { isDirty: false, windowState: undefined }));`,
  'Point recent-project cleanup',
);
replaceOnce(
  windowActionsPath,
  `\t\tconst workspaceSeparator: IQuickPickSeparator = { type: 'separator', label: hasWorkspaces ? localize('workspacesAndFolders', "folders & workspaces") : localize('folders', "folders") };
\t\tconst fileSeparator: IQuickPickSeparator = { type: 'separator', label: localize('files', "files") };
\t\tconst picks = [workspaceSeparator, ...workspacePicks, fileSeparator, ...filePicks];`,
  `\t\tconst workspaceSeparator: IQuickPickSeparator = { type: 'separator', label: product.applicationName === 'point' ? localize('point.recentProjects', "Недавние проекты") : (hasWorkspaces ? localize('workspacesAndFolders', "folders & workspaces") : localize('folders', "folders")) };
\t\tconst fileSeparator: IQuickPickSeparator = { type: 'separator', label: localize('files', "files") };
\t\tconst openPointProjectPick: IOpenPointProjectPick = {
\t\t\topenPointProject: true,
\t\t\tlabel: localize('point.openAnotherProject', "Открыть другой проект…"),
\t\t\tdescription: localize('point.chooseFolder', "Выбрать папку на компьютере"),
\t\t\ticonClass: ThemeIcon.asClassName(Codicon.folderOpened),
\t\t\talwaysShow: true
\t\t};
\t\tconst picks = product.applicationName === 'point'
\t\t\t? [openPointProjectPick, ...workspacePicks]
\t\t\t: [workspaceSeparator, ...workspacePicks, fileSeparator, ...filePicks];`,
  'Point project-only recent picker',
);
replaceOnce(
  windowActionsPath,
  `\t\tconst pick = await quickInputService.pick(picks, {
\t\t\tcontextKey: inRecentFilesPickerContextKey,`,
  `\t\tconst pick = await quickInputService.pick(picks, {
\t\t\ttitle: product.applicationName === 'point' ? localize('point.projectSwitcherQuickTitle', "Point — проекты") : undefined,
\t\t\tcontextKey: inRecentFilesPickerContextKey,`,
  'Point recent-project title',
);
replaceOnce(
  windowActionsPath,
  `\t\t\tplaceHolder: isMacintosh ? localize('openRecentPlaceholderMac', "Select to open (hold Cmd-key to force new window or Option-key for same window)") : localize('openRecentPlaceholder', "Select to open (hold Ctrl-key to force new window or Alt-key for same window)"),`,
  `\t\t\tplaceHolder: product.applicationName === 'point'
\t\t\t\t? localize('point.projectSwitcherPlaceholder', "Найти проект · Ctrl — в новом окне")
\t\t\t\t: (isMacintosh ? localize('openRecentPlaceholderMac', "Select to open (hold Cmd-key to force new window or Option-key for same window)") : localize('openRecentPlaceholder', "Select to open (hold Ctrl-key to force new window or Alt-key for same window)")),`,
  'Point recent-project placeholder',
);
replaceOnce(
  windowActionsPath,
  `\t\t\tonDidTriggerItemButton: async context => {

\t\t\t\t// Remove`,
  `\t\t\tonDidTriggerItemButton: async context => {
\t\t\t\tif ('openPointProject' in context.item) {
\t\t\t\t\treturn;
\t\t\t\t}

\t\t\t\t// Remove`,
  'Point recent-project button guard',
);
replaceOnce(
  windowActionsPath,
  `\t\tif (pick) {
\t\t\treturn hostService.openWindow([pick.openable], {`,
  `\t\tif (pick) {
\t\t\tif ('openPointProject' in pick) {
\t\t\t\tawait commandService.executeCommand('workbench.action.files.openFolder');
\t\t\t\treturn;
\t\t\t}
\t\t\treturn hostService.openWindow([pick.openable], {`,
  'Point open-another-project action',
);
replaceAny(
  windowActionsPath,
  [
    `\t\treturn {
\t\t\ticonClasses,
\t\t\tlabel: name,
\t\t\tariaLabel: kind.isDirty ? isWorkspace ? localize('recentDirtyWorkspaceAriaLabel', "{0}, workspace with unsaved changes", name) : localize('recentDirtyFolderAriaLabel', "{0}, folder with unsaved changes", name) : name,
\t\t\tdescription: parentPath,`,
    `\t\treturn {
\t\t\ticonClasses,
\t\t\tlabel: name,
\t\t\tariaLabel: product.applicationName === 'point' && kind.windowState?.isActive ? localize('point.currentProjectAriaLabel', "{0}, текущий проект", name) : (kind.isDirty ? isWorkspace ? localize('recentDirtyWorkspaceAriaLabel', "{0}, workspace with unsaved changes", name) : localize('recentDirtyFolderAriaLabel', "{0}, folder with unsaved changes", name) : name),
\t\t\tdescription: product.applicationName === 'point' && kind.windowState?.isActive ? localize('point.currentProject', "Текущий проект · {0}", parentPath) : parentPath,`,
  ],
  `\t\treturn {
\t\t\ticonClasses,
\t\t\tlabel: name,
\t\t\tariaLabel: product.applicationName === 'point' && kind.windowState?.isActive ? localize('point.currentProjectAriaLabel', "{0}, активный мир", name) : (kind.isDirty ? isWorkspace ? localize('recentDirtyWorkspaceAriaLabel', "{0}, workspace with unsaved changes", name) : localize('recentDirtyFolderAriaLabel', "{0}, folder with unsaved changes", name) : name),
\t\t\tdescription: product.applicationName === 'point' && kind.windowState?.isActive ? localize('point.currentProject', "Активный мир · {0}", parentPath) : parentPath,`,
  'Point current project marker',
);

replaceOnce(
  path.join(sourceRoot, 'src', 'main.ts'),
  'const userLocale = getUserDefinedLocale(argvConfig);',
  "const userLocale = getUserDefinedLocale(argvConfig) ?? 'ru';",
  'default UI locale',
);

const onboardingImplementationPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'welcomeOnboarding', 'browser', 'onboardingVariationA.ts');
replaceOnce(
  onboardingImplementationPath,
  `assertDefined(product.defaultChatAgent, 'Onboarding requires a default chat agent product configuration.');
const defaultChat = product.defaultChatAgent;`,
  `if (product.defaultChatAgent) {
	assertDefined(product.defaultChatAgent, 'Onboarding requires a default chat agent product configuration.');
}
const defaultChat = product.defaultChatAgent;`,
  'optional default chat agent guard',
);

const onboardingContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'welcomeOnboarding', 'browser', 'welcomeOnboarding.contribution.ts');
replaceOnce(
  onboardingContributionPath,
  `import { localize2 } from '../../../../nls.js';`,
  `import { Event } from '../../../../base/common/event.js';
import product from '../../../../platform/product/common/product.js';
import { localize2 } from '../../../../nls.js';`,
  'disabled onboarding service imports',
);
replaceOnce(
  onboardingContributionPath,
  `registerSingleton(IOnboardingService, OnboardingVariationA, InstantiationType.Delayed);`,
  `class DisabledOnboardingService implements IOnboardingService {
	readonly _serviceBrand = undefined;
	readonly onDidDismiss = Event.None;
	show(): void { }
}

if (product.defaultChatAgent) {
	registerSingleton(IOnboardingService, OnboardingVariationA, InstantiationType.Delayed);
} else {
	registerSingleton(IOnboardingService, DisabledOnboardingService, InstantiationType.Delayed);
}`,
  'no-op onboarding service for non-Copilot products',
);

const sessionsSetupPath = path.join(sourceRoot, 'src', 'vs', 'sessions', 'browser', 'sessionsSetUpService.ts');
replaceAny(
  sessionsSetupPath,
  [
    `\tprivate _start(): void {
\t\tif (!this.productService.defaultChatAgent?.chatExtensionId) {`,
    `\tprivate _start(): void {
\t\t// Local Agent ships its own local-only onboarding and never requires an account.
\t\tif (this.productService.applicationName === 'local-agent') {
\t\t\tthis.onCompleted();
\t\t\treturn;
\t\t}

\t\tif (!this.productService.defaultChatAgent?.chatExtensionId) {`,
  ],
  `\tprivate _start(): void {
\t\t// Point is local-first and never requires an account to open the IDE.
\t\tif (this.productService.applicationName === 'point') {
\t\t\tthis.onCompleted();
\t\t\treturn;
\t\t}

\t\tif (!this.productService.defaultChatAgent?.chatExtensionId) {`,
  'Point sessions welcome guard',
);

// Point owns its start surface as native workbench code. This deliberately
// avoids an extension webview and its extra renderer process, so the product
// starts and behaves like an IDE fork rather than an extension inside VS Code.
const gettingStartedPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'welcomeGettingStarted', 'browser', 'gettingStarted.ts');
replaceOnce(
  gettingStartedPath,
  `\t\t\tcase 'selectCategory': {`,
  `\t\t\tcase 'pointCommand': {
\t\t\t\tawait this.commandService.executeCommand(argument);
\t\t\t\tbreak;
\t\t\t}
\t\t\tcase 'selectCategory': {`,
  'native Point start-page command dispatch',
);
replaceOnce(
  gettingStartedPath,
  `\tprivate async buildCategoriesSlide(preserveFocus?: boolean) {

\t\tthis.categoriesSlideDisposables.clear();
\t\tconst showOnStartupCheckbox = new Toggle({`,
  `\tprivate async buildCategoriesSlide(preserveFocus?: boolean) {

\t\tthis.categoriesSlideDisposables.clear();
\t\tif (this.productService.applicationName === 'point') {
\t\t\tthis.buildPointHome();
\t\t\treturn;
\t\t}
\t\tthis.container.classList.remove('point-native-home');
\t\tconst showOnStartupCheckbox = new Toggle({`,
  'native Point start-page selection',
);
const pointHomeMethodPath = path.join(import.meta.dirname, 'resources', 'point-native-home.ts.txt');
const pointHomeMethod = fs.readFileSync(pointHomeMethodPath, 'utf8').trimEnd();
const gettingStartedSource = fs.readFileSync(gettingStartedPath, 'utf8');
const pointHomeMethodStart = '\tprivate buildPointHome(): void {';
const methodAnchor = '\tprivate buildRecentlyOpenedList(): GettingStartedIndexList<RecentEntry> {';
if (gettingStartedSource.includes(pointHomeMethodStart)) {
  const start = gettingStartedSource.indexOf(pointHomeMethodStart);
  const end = gettingStartedSource.indexOf(methodAnchor, start);
  if (end < 0) fail(`Could not locate native Point method end in ${gettingStartedPath}`);
  fs.writeFileSync(gettingStartedPath, `${gettingStartedSource.slice(0, start)}${pointHomeMethod}\n\n${gettingStartedSource.slice(end)}`);
} else {
  if (!gettingStartedSource.includes(methodAnchor)) fail(`Could not locate native Point method anchor in ${gettingStartedPath}`);
  fs.writeFileSync(gettingStartedPath, gettingStartedSource.replace(methodAnchor, `${pointHomeMethod}\n\n${methodAnchor}`));
}
replaceIfPresent(
  gettingStartedPath,
  `title: localize('recent', "Recent"),`,
  `title: localize('recent', "Недавние миры"),`,
);
replaceIfPresent(
  gettingStartedPath,
  `localize('noRecents', "You have no recent folders,"),`,
  `localize('noRecents', "Пока нет недавних миров,"),`,
);
replaceIfPresent(
  gettingStartedPath,
  `localize('openFolder', "open a folder")),`,
  `localize('openFolder', "откройте папку")),`,
);
replaceIfPresent(
  gettingStartedPath,
  `localize('toStart', "to start.")),`,
  `localize('toStart', "чтобы начать.")),`,
);

const gettingStartedInputPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'welcomeGettingStarted', 'browser', 'gettingStartedInput.ts');
replaceOnce(
  gettingStartedInputPath,
  `import { IEditorOptions } from '../../../../platform/editor/common/editor.js';`,
  `import { IEditorOptions } from '../../../../platform/editor/common/editor.js';
import product from '../../../../platform/product/common/product.js';`,
  'Point start-page product import',
);
// Якорь берётся хвостом выражения, а не строкой целиком: в чистом дереве
// Code-OSS 1.124.2 тернарник записан одной строкой, а прежние формы заплаты
// ждали его разложенным по строкам. Заплата, привязанная к разложенному виду,
// работала только на дереве, которое уже кто-то трогал, и падала на настоящем
// чистом checkout — то есть ровно там, где её и проверяет production workflow.
replaceAny(
  gettingStartedInputPath,
  [
    `localize('getStarted', "Welcome");`,
    `product.applicationName === 'point' ? 'Point' : localize('getStarted', "Welcome");`,
    `product.applicationName === 'point' ? localize('point.home', "Главная") : localize('getStarted', "Welcome");`,
  ],
  `product.applicationName === 'point' ? localize('point.home', "Главная") : localize('getStarted', "Welcome");`,
  'Point native editor label',
);

const startupPagePath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'welcomeGettingStarted', 'browser', 'startupPage.ts');
replaceAny(
  startupPagePath,
  [
    `\t\t// Wait for resolving startup editor until we are restored to reduce startup pressure
\t\tawait this.lifecycleService.when(LifecyclePhase.Restored);

\t\tif (AuxiliaryBarMaximizedContext.getValue(this.contextKeyService)) {`,
    `\t\t// Wait for resolving startup editor until we are restored to reduce startup pressure
\t\tawait this.lifecycleService.when(LifecyclePhase.Restored);
\t\tif (this.productService.applicationName === 'point') {
\t\t\tthis.layoutService.setPartHidden(true, Parts.AUXILIARYBAR_PART);
\t\t}

\t\tif (AuxiliaryBarMaximizedContext.getValue(this.contextKeyService)) {`,
    `\t\t// Wait for resolving startup editor until we are restored to reduce startup pressure
\t\tawait this.lifecycleService.when(LifecyclePhase.Restored);
\t\tif (this.productService.applicationName === 'point') {
\t\t\tthis.layoutService.setPartHidden(false, Parts.AUXILIARYBAR_PART);
\t\t\tvoid this.commandService.executeCommand('workbench.view.extension.pointCompanion');
\t\t}

\t\tif (AuxiliaryBarMaximizedContext.getValue(this.contextKeyService)) {`,
  ],
  `\t\t// Wait for resolving startup editor until we are restored to reduce startup pressure
\t\tawait this.lifecycleService.when(LifecyclePhase.Restored);
\t\tif (this.productService.applicationName === 'point') {
\t\t\tthis.layoutService.setPartHidden(false, Parts.AUXILIARYBAR_PART);
\t\t\tvoid this.commandService.executeCommand('workbench.view.extension.pointCompanion');
\t\t}

\t\tif (AuxiliaryBarMaximizedContext.getValue(this.contextKeyService)) {`,
  'Point auxiliary bar startup guard',
);
replaceOnce(
  startupPagePath,
  `\t\tconst enabled = isStartupPageEnabled(this.configurationService, this.contextService, this.environmentService);
\t\tif (enabled && this.lifecycleService.startupKind !== StartupKind.ReloadedWindow) {`,
  `\t\tconst pointHome = this.productService.applicationName === 'point' && this.contextService.getWorkbenchState() === WorkbenchState.EMPTY;
\t\tconst enabled = pointHome || isStartupPageEnabled(this.configurationService, this.contextService, this.environmentService);
\t\tif (enabled && this.lifecycleService.startupKind !== StartupKind.ReloadedWindow) {`,
  'Point native startup condition',
);
replaceOnce(
  startupPagePath,
  `\t\t\t\tif (startupEditorSetting.value === 'readme') {`,
  `\t\t\t\tif (pointHome) {
\t\t\t\t\tawait this.openGettingStarted(true);
\t\t\t\t} else if (startupEditorSetting.value === 'readme') {`,
  'Point native startup editor',
);
replaceOnce(
  startupPagePath,
  `\tprivate tryShowOnboarding(): void {
\t\tif (this.environmentService.skipWelcome) {`,
  `\tprivate tryShowOnboarding(): void {
\t\tif (this.productService.applicationName === 'point') {
\t\t\treturn;
\t\t}
\t\tif (this.environmentService.skipWelcome) {`,
  'Point upstream onboarding guard',
);

const gettingStartedCssPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'welcomeGettingStarted', 'browser', 'media', 'gettingStarted.css');
const pointHomeCss = fs.readFileSync(path.join(import.meta.dirname, 'resources', 'point-native-home.css'), 'utf8').trim();
const gettingStartedCss = fs.readFileSync(gettingStartedCssPath, 'utf8');
const pointHomeCssMarker = '/* Point native start surface';
if (gettingStartedCss.includes(pointHomeCssMarker)) {
  const markerIndex = gettingStartedCss.indexOf(pointHomeCssMarker);
  fs.writeFileSync(gettingStartedCssPath, `${gettingStartedCss.slice(0, markerIndex).trimEnd()}\n\n${pointHomeCss}\n`);
} else {
  fs.writeFileSync(gettingStartedCssPath, `${gettingStartedCss.trimEnd()}\n\n${pointHomeCss}\n`);
}

const chatServicePath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'chat', 'common', 'chatService', 'chatServiceImpl.ts');
const chatActionsPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'chat', 'browser', 'actions', 'chatActions.ts');
const chatSetupContributionsPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'chat', 'browser', 'chatSetup', 'chatSetupContributions.ts');
const authenticationContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'authentication', 'browser', 'authentication.contribution.ts');
const developerActionsPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'actions', 'developerActions.ts');
replaceOnce(
  authenticationContributionPath,
  `import { Registry } from '../../../../platform/registry/common/platform.js';`,
  `import { Registry } from '../../../../platform/registry/common/platform.js';\nimport product from '../../../../platform/product/common/product.js';`,
  'Point authentication contribution product import',
);
replaceOnce(
  authenticationContributionPath,
  `\tprivate _registerActions(): void {\n\t\tthis._register(registerAction2(ManageAccountsAction));`,
  `\tprivate _registerActions(): void {\n\t\tif (product.applicationName === 'point') {\n\t\t\treturn;\n\t\t}\n\t\tthis._register(registerAction2(ManageAccountsAction));`,
  'hidden Point account actions',
);
replaceOnce(
  developerActionsPath,
  `registerAction2(SyncAccountPolicyAction);`,
  `if (product.applicationName !== 'point') {\n\tregisterAction2(SyncAccountPolicyAction);\n}`,
  'hidden Point account policy developer action',
);
replaceOnce(
  chatSetupContributionsPath,
  `\tprivate registerActions(context: ChatEntitlementContext, requests: ChatEntitlementRequests, controller: Lazy<ChatSetupController>): void {\n\n\t\t//#region Global Chat Setup Actions`,
  `\tprivate registerActions(context: ChatEntitlementContext, requests: ChatEntitlementRequests, controller: Lazy<ChatSetupController>): void {\n\t\tif (product.applicationName === 'point') {\n\t\t\treturn;\n\t\t}\n\n\t\t//#region Global Chat Setup Actions`,
  'disabled Point upstream chat setup actions',
);
replaceOnce(
  chatActionsPath,
  `\toverride async run(accessor: ServicesAccessor, opts?: string | IChatViewOpenOptions): Promise<IChatAgentResult & { type?: 'confirmation' } | undefined> {\n\t\topts = typeof opts === 'string' ? { query: opts } : opts;`,
  `\toverride async run(accessor: ServicesAccessor, opts?: string | IChatViewOpenOptions): Promise<IChatAgentResult & { type?: 'confirmation' } | undefined> {\n\t\tif (product.applicationName === 'point') {\n\t\t\tawait accessor.get(ICommandService).executeCommand('localAgent.open');\n\t\t\treturn;\n\t\t}\n\t\topts = typeof opts === 'string' ? { query: opts } : opts;`,
  'Point upstream chat command redirect',
);

const chatParticipantContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'chat', 'browser', 'chatParticipant.contribution.ts');
replaceOnce(
  chatParticipantContributionPath,
  `import { IProductService } from '../../../../platform/product/common/productService.js';`,
  `import product from '../../../../platform/product/common/product.js';
import { IProductService } from '../../../../platform/product/common/productService.js';`,
  'Point chat participant product import',
);
replaceOnce(
  chatParticipantContributionPath,
  `}, ViewContainerLocation.AuxiliaryBar, { isDefault: true, doNotRegisterOpenCommand: true });`,
  `}, ViewContainerLocation.AuxiliaryBar, { isDefault: product.applicationName !== 'point', doNotRegisterOpenCommand: true });`,
  'Point chat view not default auxiliary bar',
);
replaceOnce(
  chatParticipantContributionPath,
  `\topenCommandActionDescriptor: {
\t\tid: ChatViewContainerId,
\t\ttitle: chatViewContainer.title,
\t\tmnemonicTitle: localize({ key: 'miToggleChat', comment: ['&& denotes a mnemonic'] }, "&&Chat"),
\t\tkeybindings: {
\t\t\tprimary: KeyMod.CtrlCmd | KeyMod.Alt | KeyCode.KeyI,
\t\t\tmac: {
\t\t\t\tprimary: KeyMod.CtrlCmd | KeyMod.WinCtrl | KeyCode.KeyI
\t\t\t}
\t\t},
\t\torder: 1
\t},
\tctorDescriptor: new SyncDescriptor(ChatViewPane),
\twhen: ContextKeyExpr.and(
\t\tChatContextKeys.accountPolicyGateActive.negate(),
\t\tContextKeyExpr.or(
\t\t\tContextKeyExpr.and(
\t\t\t\tChatContextKeys.Setup.hidden.negate(),
\t\t\t\tChatContextKeys.Setup.disabledInWorkspace.negate(),
\t\t\t),
\t\t\tChatContextKeys.panelParticipantRegistered,
\t\t\tChatContextKeys.extensionInvalid
\t\t)
\t)
};`,
  `\topenCommandActionDescriptor: product.applicationName === 'point' ? undefined : {
\t\tid: ChatViewContainerId,
\t\ttitle: chatViewContainer.title,
\t\tmnemonicTitle: localize({ key: 'miToggleChat', comment: ['&& denotes a mnemonic'] }, "&&Chat"),
\t\tkeybindings: {
\t\t\tprimary: KeyMod.CtrlCmd | KeyMod.Alt | KeyCode.KeyI,
\t\t\tmac: {
\t\t\t\tprimary: KeyMod.CtrlCmd | KeyMod.WinCtrl | KeyCode.KeyI
\t\t\t}
\t\t},
\t\torder: 1
\t},
\tctorDescriptor: new SyncDescriptor(ChatViewPane),
\twhen: product.applicationName === 'point'
\t\t? ContextKeyExpr.false()
\t\t: ContextKeyExpr.and(
\t\t\tChatContextKeys.accountPolicyGateActive.negate(),
\t\t\tContextKeyExpr.or(
\t\t\t\tContextKeyExpr.and(
\t\t\t\t\tChatContextKeys.Setup.hidden.negate(),
\t\t\t\t\tChatContextKeys.Setup.disabledInWorkspace.negate(),
\t\t\t\t),
\t\t\t\tChatContextKeys.panelParticipantRegistered,
\t\t\t\tChatContextKeys.extensionInvalid
\t\t\t)
\t\t)
};`,
  'Point hide upstream Chat view and Ctrl+Alt+I',
);

const chatQuickInputActionsPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'chat', 'browser', 'actions', 'chatQuickInputActions.ts');
replaceOnce(
  chatQuickInputActionsPath,
  `import { localize, localize2 } from '../../../../../nls.js';
import { Action2, MenuId, registerAction2 } from '../../../../../platform/actions/common/actions.js';
import { ServicesAccessor } from '../../../../../platform/instantiation/common/instantiation.js';`,
  `import { localize, localize2 } from '../../../../../nls.js';
import { Action2, MenuId, registerAction2 } from '../../../../../platform/actions/common/actions.js';
import { ICommandService } from '../../../../../platform/commands/common/commands.js';
import { ServicesAccessor } from '../../../../../platform/instantiation/common/instantiation.js';
import product from '../../../../../platform/product/common/product.js';`,
  'Point quick chat product import',
);
replaceAny(
  chatQuickInputActionsPath,
  [
  `\toverride run(accessor: ServicesAccessor, query?: string | Omit<IQuickChatOpenOptions, 'selection'>): void {
\t\tconst quickChatService = accessor.get(IQuickChatService);
\t\tlet options: IQuickChatOpenOptions | undefined;
\t\tswitch (typeof query) {
\t\t\tcase 'string': options = { query }; break;
\t\t\tcase 'object': options = query; break;
\t\t}
\t\tif (options?.query) {
\t\t\toptions.selection = new Selection(1, options.query.length + 1, 1, options.query.length + 1);
\t\t}
\t\tquickChatService.toggle(options);
\t}
}`,
  `\toverride run(accessor: ServicesAccessor, query?: string | Omit<IQuickChatOpenOptions, 'selection'>): void {
\t\tif (product.applicationName === 'point') {
\t\t\tvoid accessor.get(ICommandService).executeCommand('localAgent.open');
\t\t\treturn;
\t\t}
\t\tconst quickChatService = accessor.get(IQuickChatService);
\t\tlet options: IQuickChatOpenOptions | undefined;
\t\tswitch (typeof query) {
\t\t\tcase 'string': options = { query }; break;
\t\t\tcase 'object': options = query; break;
\t\t}
\t\tif (options?.query) {
\t\t\toptions.selection = new Selection(1, options.query.length + 1, 1, options.query.length + 1);
\t\t}
\t\tquickChatService.toggle(options);
\t}
}`,
  ],
  `\toverride run(accessor: ServicesAccessor, query?: string | Omit<IQuickChatOpenOptions, 'selection'>): void {
\t\tif (product.applicationName === 'point') {
\t\t\tconst pointQuery = typeof query === 'string' ? query : query?.query;
\t\t\tvoid accessor.get(ICommandService).executeCommand('localAgent.quickChat', pointQuery);
\t\t\treturn;
\t\t}
\t\tconst quickChatService = accessor.get(IQuickChatService);
\t\tlet options: IQuickChatOpenOptions | undefined;
\t\tswitch (typeof query) {
\t\t\tcase 'string': options = { query }; break;
\t\t\tcase 'object': options = query; break;
\t\t}
\t\tif (options?.query) {
\t\t\toptions.selection = new Selection(1, options.query.length + 1, 1, options.query.length + 1);
\t\t}
\t\tquickChatService.toggle(options);
\t}
}`,
  'Point quick chat redirect',
);
replaceAny(
  chatQuickInputActionsPath,
  [
  `\toverride run(accessor: ServicesAccessor, query?: string): void {
\t\tconst quickChatService = accessor.get(IQuickChatService);
\t\tquickChatService.toggle(query ? {
\t\t\tquery,
\t\t\tselection: new Selection(1, query.length + 1, 1, query.length + 1)
\t\t} : undefined);
\t}
}`,
  `\toverride run(accessor: ServicesAccessor, query?: string): void {
\t\tif (product.applicationName === 'point') {
\t\t\tvoid accessor.get(ICommandService).executeCommand('localAgent.open');
\t\t\treturn;
\t\t}
\t\tconst quickChatService = accessor.get(IQuickChatService);
\t\tquickChatService.toggle(query ? {
\t\t\tquery,
\t\t\tselection: new Selection(1, query.length + 1, 1, query.length + 1)
\t\t} : undefined);
\t}
}`,
  ],
  `\toverride run(accessor: ServicesAccessor, query?: string): void {
\t\tif (product.applicationName === 'point') {
\t\t\tvoid accessor.get(ICommandService).executeCommand('localAgent.quickChat', query);
\t\t\treturn;
\t\t}
\t\tconst quickChatService = accessor.get(IQuickChatService);
\t\tquickChatService.toggle(query ? {
\t\t\tquery,
\t\t\tselection: new Selection(1, query.length + 1, 1, query.length + 1)
\t\t} : undefined);
\t}
}`,
  'Point openQuickChat redirect',
);
replaceAny(
  chatActionsPath,
  [
    `\t\t\ttitle: localize2('openChat', "Open Chat"),`,
    `\t\t\ttitle: product.applicationName === 'point' ? localize2('point.openAgent', "Открыть ИИ-агента") : localize2('openChat', "Open Chat"),`,
  ],
  `\t\t\ttitle: product.applicationName === 'point' ? localize2('point.openAgent', "Открыть Гильдию") : localize2('openChat', "Open Chat"),`,
  'Point chat command title',
);
replaceOnce(
  chatServicePath,
  `import { localize } from '../../../../../nls.js';`,
  `import { localize } from '../../../../../nls.js';
import product from '../../../../../platform/product/common/product.js';`,
  'Point chat service product import',
);
replaceOnce(
  chatServicePath,
  `\tasync activateDefaultAgent(location: ChatAgentLocation): Promise<void> {
\t\tawait this.extensionService.whenInstalledExtensionsRegistered();`,
  `\tasync activateDefaultAgent(location: ChatAgentLocation): Promise<void> {
\t\tif (product.applicationName === 'point' || this.configurationService.getValue<boolean>('chat.disableAIFeatures')) {
\t\t\treturn;
\t\t}
\t\tawait this.extensionService.whenInstalledExtensionsRegistered();`,
  'disabled upstream chat activation guard',
);
replaceOnce(
  chatServicePath,
  `\t\tthis.activateDefaultAgent(model.initialLocation).catch(e => this.logService.error(e));`,
  `\t\tif (product.applicationName !== 'point') {
\t\t\tthis.activateDefaultAgent(model.initialLocation).catch(e => this.logService.error(e));
\t\t}`,
  'Point chat session activation guard',
);

const defaultAccountPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'services', 'accounts', 'browser', 'defaultAccount.ts');
replaceOnce(
  defaultAccountPath,
  `\tprivate async fetchDefaultAccount(options?: { forceRefresh?: boolean }): Promise<IDefaultAccountData | null> {
\t\tconst defaultAccountProvider = this.getDefaultAccountAuthenticationProvider();
\t\tthis.logService.debug('[DefaultAccount] Default account provider ID:', defaultAccountProvider.id);`,
  `\tprivate async fetchDefaultAccount(options?: { forceRefresh?: boolean }): Promise<IDefaultAccountData | null> {
\t\tconst defaultAccountProvider = this.getDefaultAccountAuthenticationProvider();
\t\tif (defaultAccountProvider.id === 'point') {
\t\t\treturn null;
\t\t}
\t\tthis.logService.debug('[DefaultAccount] Default account provider ID:', defaultAccountProvider.id);`,
  'Point local-only default account guard',
);

const globalCompositeBarPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'globalCompositeBar.ts');
replaceOnce(
  globalCompositeBarPath,
  `import { ILogService } from '../../../platform/log/common/log.js';
import { IProductService } from '../../../platform/product/common/productService.js';`,
  `import { ILogService } from '../../../platform/log/common/log.js';
import product from '../../../platform/product/common/product.js';
import { IProductService } from '../../../platform/product/common/productService.js';`,
  'Point accounts product import',
);
replaceOnce(
  globalCompositeBarPath,
  `\treturn storageService.getBoolean(AccountsActivityActionViewItem.ACCOUNTS_VISIBILITY_PREFERENCE_KEY, StorageScope.PROFILE, true);`,
  `\treturn storageService.getBoolean(AccountsActivityActionViewItem.ACCOUNTS_VISIBILITY_PREFERENCE_KEY, StorageScope.PROFILE, product.applicationName !== 'point');`,
  'Point accounts visibility default',
);

const extensionsContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'extensions', 'browser', 'extensions.contribution.ts');
replaceOnce(
  extensionsContributionPath,
  `\t\t\t[VerifyExtensionSignatureConfigKey]: {
\t\t\t\ttype: 'boolean',
\t\t\t\tdescription: localize('extensions.verifySignature', "When enabled, extensions are verified to be signed before getting installed."),
\t\t\t\tdefault: true,
\t\t\t\tscope: ConfigurationScope.APPLICATION,
\t\t\t\tincluded: isNative
\t\t\t},`,
  `\t\t\t[VerifyExtensionSignatureConfigKey]: {
\t\t\t\ttype: 'boolean',
\t\t\t\tdescription: localize('extensions.verifySignature', "When enabled, extensions are verified to be signed before getting installed."),
\t\t\t\t// Point is Code-OSS: Microsoft vsce-sign is unavailable, so verification never executes.
\t\t\t\tdefault: false,
\t\t\t\tscope: ConfigurationScope.APPLICATION,
\t\t\t\tincluded: isNative
\t\t\t},`,
  'Point disable extension signature verification default',
);

const extensionManagementServicePath = path.join(sourceRoot, 'src', 'vs', 'platform', 'extensionManagement', 'node', 'extensionManagementService.ts');
replaceOnce(
  extensionManagementServicePath,
  `\tprivate async downloadExtension(extension: IGalleryExtension, operation: InstallOperation, verifySignature: boolean, clientTargetPlatform?: TargetPlatform): Promise<{ readonly location: URI; readonly verificationStatus: ExtensionSignatureVerificationCode | undefined }> {
\t\tif (verifySignature) {
\t\t\tconst value = this.configurationService.getValue(VerifyExtensionSignatureConfigKey);
\t\t\tverifySignature = isBoolean(value) ? value : true;
\t\t}`,
  `\tprivate async downloadExtension(extension: IGalleryExtension, operation: InstallOperation, verifySignature: boolean, clientTargetPlatform?: TargetPlatform): Promise<{ readonly location: URI; readonly verificationStatus: ExtensionSignatureVerificationCode | undefined }> {
\t\t// Point is Code-OSS without Microsoft vsce-sign: never block installs on signature checks.
\t\tconst configuredVerifySignature = this.configurationService.getValue(VerifyExtensionSignatureConfigKey);
\t\tconst allowVerifySignature = isBoolean(configuredVerifySignature) ? configuredVerifySignature : false;
\t\tverifySignature = verifySignature && allowVerifySignature;`,
  'Point skip marketplace signature enforcement',
);

const agentHostConfigPath = path.join(sourceRoot, 'src', 'vs', 'platform', 'agentHost', 'common', 'agentHost.config.contribution.ts');
replaceAny(
  agentHostConfigPath,
  [
    `\t\t\tdefault: !isWeb && product.quality !== 'stable',`,
    `\t\t\tdefault: !isWeb && product.quality !== 'stable' && product.applicationName !== 'local-agent',`,
  ],
  `			default: !isWeb && product.quality !== 'stable' && product.applicationName !== 'point',`,
  'Point agent host default',
);

const nlsPath = path.join(sourceRoot, 'src', 'vs', 'base', 'node', 'nls.ts');
replaceOnce(
  nlsPath,
  `\t\tconst languagePacks = await getLanguagePackConfigurations(userDataPath);\n\t\tif (!languagePacks) {`,
  `\t\tconst languagePacks = {\n\t\t\t...((await getLanguagePackConfigurations(userDataPath)) ?? {}),\n\t\t\t...(await getBundledLanguagePackConfigurations(nlsMetadataPath))\n\t\t};\n\t\tif (Object.keys(languagePacks).length === 0) {`,
  'bundled language pack fallback',
);
if (!fs.readFileSync(nlsPath, 'utf8').includes('async function getBundledLanguagePackConfigurations(')) {
replaceOnce(
  nlsPath,
  'function resolveLanguagePackLanguage(languagePacks: ILanguagePacks, locale: string | undefined): string | undefined {',
  `async function getBundledLanguagePackConfigurations(nlsMetadataPath: string): Promise<ILanguagePacks> {\n\tconst extensionPath = join(nlsMetadataPath, '..', 'extensions', 'vscode-language-pack-ru');\n\ttry {\n\t\tconst manifest = JSON.parse(await promises.readFile(join(extensionPath, 'package.json'), 'utf-8')) as {\n\t\t\tpublisher?: string;\n\t\t\tname?: string;\n\t\t\tversion?: string;\n\t\t\tcontributes?: {\n\t\t\t\tlocalizations?: Array<{\n\t\t\t\t\tlanguageId?: string;\n\t\t\t\t\tlanguageName?: string;\n\t\t\t\t\tlocalizedLanguageName?: string;\n\t\t\t\t\ttranslations?: Array<{ id?: string; path?: string }>;\n\t\t\t\t}>;\n\t\t\t};\n\t\t};\n\t\tconst localization = manifest.contributes?.localizations?.find(candidate => candidate.languageId === 'ru');\n\t\tif (!localization?.translations || !manifest.publisher || !manifest.name || !manifest.version) {\n\t\t\treturn {};\n\t\t}\n\n\t\tconst translations: Record<string, string | undefined> = {};\n\t\tfor (const translation of localization.translations) {\n\t\t\tif (translation.id && translation.path) {\n\t\t\t\ttranslations[translation.id] = join(extensionPath, translation.path);\n\t\t\t}\n\t\t}\n\n\t\treturn {\n\t\t\tru: {\n\t\t\t\thash: \`builtin-\${manifest.version}\`,\n\t\t\t\tlabel: localization.localizedLanguageName ?? localization.languageName,\n\t\t\t\textensions: [{\n\t\t\t\t\textensionIdentifier: { id: \`\${manifest.publisher}.\${manifest.name}\` },\n\t\t\t\t\tversion: manifest.version\n\t\t\t\t}],\n\t\t\t\ttranslations\n\t\t\t}\n\t\t};\n\t} catch {\n\t\treturn {};\n\t}\n}\n\nfunction resolveLanguagePackLanguage(languagePacks: ILanguagePacks, locale: string | undefined): string | undefined {`,
  'bundled Russian language pack resolver',
);
}
replaceOnce(
  nlsPath,
  `import { promises } from 'fs';`,
  `import { promises } from 'fs';\nimport { createHash } from 'crypto';`,
  'bundled language pack metadata hashing import',
);
replaceOnce(
  nlsPath,
  `\t\treturn {\n\t\t\tru: {\n\t\t\t\thash: \`builtin-\${manifest.version}\`,`,
  `\t\tconst defaultMessages = await promises.readFile(join(nlsMetadataPath, 'nls.messages.json'));\n\t\tconst metadataHash = createHash('sha256').update(defaultMessages).digest('hex').slice(0, 12);\n\n\t\treturn {\n\t\t\tru: {\n\t\t\t\thash: \`builtin-\${manifest.version}-\${metadataHash}\`,`,
  'bundled language pack cache identity',
);

const activitybarPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'activitybar', 'activitybarPart.ts');
const editorGroupWatermarkPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'editor', 'editorGroupWatermark.ts');
replaceOnce(
  editorGroupWatermarkPath,
  `import { coalesce, shuffle } from '../../../../base/common/arrays.js';`,
  `import { coalesce, shuffle } from '../../../../base/common/arrays.js';\nimport { KeyCode, KeyMod } from '../../../../base/common/keyCodes.js';`,
  'Point editor watermark key codes',
);
replaceOnce(
  editorGroupWatermarkPath,
  `import { CommandsRegistry } from '../../../../platform/commands/common/commands.js';`,
  `import { CommandsRegistry, ICommandService } from '../../../../platform/commands/common/commands.js';`,
  'Point editor watermark command service',
);
replaceOnce(
  editorGroupWatermarkPath,
  `import { IKeybindingService } from '../../../../platform/keybinding/common/keybinding.js';`,
  `import { IKeybindingService } from '../../../../platform/keybinding/common/keybinding.js';\nimport { KeybindingWeight, KeybindingsRegistry } from '../../../../platform/keybinding/common/keybindingsRegistry.js';`,
  'Point editor watermark keybinding registry',
);
replaceIfPresent(
  editorGroupWatermarkPath,
  `ContextKeyExpr.notEquals('activeViewlet', 'localAgent')`,
  `ContextKeyExpr.notEquals('activeViewlet', 'workbench.view.extension.localAgent')`,
);
replaceAny(
  editorGroupWatermarkPath,
  [
    `const showChatContextKey = ContextKeyExpr.and(ContextKeyExpr.equals('chatSetupHidden', false), ContextKeyExpr.equals('chatSetupDisabledInWorkspace', false));\n\nconst openChat: WatermarkEntry = { text: localize('watermark.openChat', "Open Chat"), id: 'workbench.action.chat.open', when: { native: showChatContextKey, web: showChatContextKey } };`,
    `const openChat: WatermarkEntry = { text: localize('watermark.openChat', "Open Chat"), id: 'workbench.action.chat.open', when: { native: showChatContextKey, web: showChatContextKey } };`,
    `const OPEN_POINT_AGENT_COMMAND_ID = 'point.action.openAgent';\nKeybindingsRegistry.registerCommandAndKeybindingRule({\n\tid: OPEN_POINT_AGENT_COMMAND_ID,\n\tweight: KeybindingWeight.WorkbenchContrib + 100,\n\tprimary: KeyMod.CtrlCmd | KeyMod.Alt | KeyCode.KeyI,\n\tmac: { primary: KeyMod.CtrlCmd | KeyMod.WinCtrl | KeyCode.KeyI },\n\thandler: accessor => accessor.get(ICommandService).executeCommand('localAgent.open')\n});\nconst openPointAgent: WatermarkEntry = { text: 'Открыть ИИ-агента', id: OPEN_POINT_AGENT_COMMAND_ID };`,
    `const OPEN_POINT_AGENT_COMMAND_ID = 'point.action.openAgent';\nKeybindingsRegistry.registerCommandAndKeybindingRule({\n\tid: OPEN_POINT_AGENT_COMMAND_ID,\n\tweight: KeybindingWeight.WorkbenchContrib + 100,\n\tprimary: KeyMod.CtrlCmd | KeyMod.Alt | KeyCode.KeyI,\n\tmac: { primary: KeyMod.CtrlCmd | KeyMod.WinCtrl | KeyCode.KeyI },\n\thandler: accessor => accessor.get(ICommandService).executeCommand('localAgent.open')\n});\nconst pointAgentWatermarkContextKeys = new Set(['activeViewlet', 'sideBarVisible']);\nconst showOpenPointAgent = ContextKeyExpr.or(ContextKeyExpr.notEquals('activeViewlet', 'workbench.view.extension.localAgent'), ContextKeyExpr.not('sideBarVisible'));\nconst openPointAgent: WatermarkEntry = { text: 'Открыть ИИ-агента', id: OPEN_POINT_AGENT_COMMAND_ID, when: { native: showOpenPointAgent, web: showOpenPointAgent } };`,
  ],
  `const OPEN_POINT_AGENT_COMMAND_ID = 'point.action.openAgent';\nKeybindingsRegistry.registerCommandAndKeybindingRule({\n\tid: OPEN_POINT_AGENT_COMMAND_ID,\n\tweight: KeybindingWeight.WorkbenchContrib + 100,\n\tprimary: KeyMod.CtrlCmd | KeyMod.Alt | KeyCode.KeyI,\n\tmac: { primary: KeyMod.CtrlCmd | KeyMod.WinCtrl | KeyCode.KeyI },\n\thandler: accessor => accessor.get(ICommandService).executeCommand('localAgent.open')\n});\nconst pointAgentWatermarkContextKeys = new Set(['activeViewlet', 'sideBarVisible']);\nconst showOpenPointAgent = ContextKeyExpr.or(ContextKeyExpr.notEquals('activeViewlet', 'workbench.view.extension.localAgent'), ContextKeyExpr.not('sideBarVisible'));\nconst openPointAgent: WatermarkEntry = { text: 'Открыть Гильдию', id: OPEN_POINT_AGENT_COMMAND_ID, when: { native: showOpenPointAgent, web: showOpenPointAgent } };`,
  'Point Agent editor watermark command',
);
replaceAny(
  editorGroupWatermarkPath,
  [
    `const baseEntries: WatermarkEntry[] = [\n\topenChat,\n\tshowCommands,\n];`,
    `const baseEntries: WatermarkEntry[] = [\n\topenPointAgent,\n\tshowCommands,\n];`,
    `const openPointCompanion: WatermarkEntry = { text: 'Спросить компаньона', id: 'localAgent.askCompanion' };\nconst searchEverywhere: WatermarkEntry = { text: 'Поиск везде', id: 'localAgent.searchEverywhere' };\nconst baseEntries: WatermarkEntry[] = [\n\topenPointAgent,\n\topenPointCompanion,\n\tsearchEverywhere,\n\tgotoFile,\n];`,
  ],
  `const openPointCompanion: WatermarkEntry = { text: 'Спросить компаньона', id: 'localAgent.askCompanion' };\nconst searchEverywhere: WatermarkEntry = { text: 'Поиск везде', id: 'localAgent.searchEverywhere' };\nconst baseEntries: WatermarkEntry[] = [\n\topenPointAgent,\n\topenPointCompanion,\n\tsearchEverywhere,\n\tgotoFile,\n];`,
  'Point editor watermark entries',
);
// Убираем и объявление: свой список записей больше не ссылается на
// `showCommands`, а `noUnusedLocals` в сборке Code-OSS считает это ошибкой.
// На дереве, пропатченном прежним оверлеем, строки уже нет — форма молчаливая.
replaceIfPresent(
  editorGroupWatermarkPath,
  `const showCommands: WatermarkEntry = { text: localize('watermark.showCommands', "Show All Commands"), id: 'workbench.action.showCommands' };`,
  `// Point: showCommands снят вместе со своей записью водяного знака.`,
);
replaceIfPresent(
  editorGroupWatermarkPath,
  `const showCommands: WatermarkEntry = { text: localize('watermark.showCommands', "Show All Commands"), id: 'workbench.action.showCommands' };\n`,
  '',
);
replaceOnce(
  editorGroupWatermarkPath,
  `\t\tthis._register(this.contextService.onDidChangeWorkbenchState(workbenchState => {\n\t\t\tif (this.workbenchState !== workbenchState) {\n\t\t\t\tthis.workbenchState = workbenchState;\n\t\t\t\tthis.render();\n\t\t\t}\n\t\t}));`,
  `\t\tthis._register(this.contextService.onDidChangeWorkbenchState(workbenchState => {\n\t\t\tif (this.workbenchState !== workbenchState) {\n\t\t\t\tthis.workbenchState = workbenchState;\n\t\t\t\tthis.render();\n\t\t\t}\n\t\t}));\n\n\t\tthis._register(this.contextKeyService.onDidChangeContext(event => {\n\t\t\tif (event.affectsSome(pointAgentWatermarkContextKeys)) {\n\t\t\t\tthis.render();\n\t\t\t}\n\t\t}));`,
  'Point contextual editor watermark listener',
);
replaceAny(
  activitybarPath,
  [
    `import './media/activityaction.css';`,
    `import './media/activityaction.css';
import './media/point-activitybar.css';
import './media/point-workbench.css';`,
  ],
  `import './media/activityaction.css';
import './media/point-fonts.css';
import './media/point-activitybar.css';
import './media/point-workbench.css';`,
  'Point activity bar stylesheet import',
);
replaceOnce(
  activitybarPath,
  `import { SwitchCompositeViewAction } from '../compositeBarActions.js';`,
  `import { SwitchCompositeViewAction } from '../compositeBarActions.js';
import { IProductService } from '../../../../platform/product/common/productService.js';`,
  'Point activity bar product service import',
);
// Ширина рейки задаётся и здесь, и в CSS. Раскладка считает по этому числу,
// куда поставить боковую панель: при расхождении панель наезжает на рейку и
// съедает у неё правый край.
replaceAny(
  activitybarPath,
  [
    `\tget minimumWidth(): number { return this._isCompact ? ActivitybarPart.COMPACT_ACTIVITYBAR_WIDTH : ActivitybarPart.ACTIVITYBAR_WIDTH; }
\tget maximumWidth(): number { return this._isCompact ? ActivitybarPart.COMPACT_ACTIVITYBAR_WIDTH : ActivitybarPart.ACTIVITYBAR_WIDTH; }`,
    `\tget minimumWidth(): number { return this.productService.applicationName === 'point' ? 44 : this._isCompact ? ActivitybarPart.COMPACT_ACTIVITYBAR_WIDTH : ActivitybarPart.ACTIVITYBAR_WIDTH; }
\tget maximumWidth(): number { return this.productService.applicationName === 'point' ? 44 : this._isCompact ? ActivitybarPart.COMPACT_ACTIVITYBAR_WIDTH : ActivitybarPart.ACTIVITYBAR_WIDTH; }`,
  ],
  `\tget minimumWidth(): number { return this.productService.applicationName === 'point' ? 46 : this._isCompact ? ActivitybarPart.COMPACT_ACTIVITYBAR_WIDTH : ActivitybarPart.ACTIVITYBAR_WIDTH; }
\tget maximumWidth(): number { return this.productService.applicationName === 'point' ? 46 : this._isCompact ? ActivitybarPart.COMPACT_ACTIVITYBAR_WIDTH : ActivitybarPart.ACTIVITYBAR_WIDTH; }`,
  'Point activity bar width',
);
replaceOnce(
  activitybarPath,
  `\t\t@IStorageService storageService: IStorageService,
\t\t@IConfigurationService private readonly configurationService: IConfigurationService,
\t) {`,
  `\t\t@IStorageService storageService: IStorageService,
\t\t@IConfigurationService private readonly configurationService: IConfigurationService,
\t\t@IProductService private readonly productService: IProductService,
\t) {`,
  'Point activity bar product service injection',
);
// Значок 18 в кнопке 38 — метрика макета. Числа нужны и раскладке: по высоте
// кнопки полоса считает, сколько инструментов помещается до переполнения.
replaceAny(
  activitybarPath,
  [
    `\t\t\tthis.element.style.setProperty('--activity-bar-action-height', \`\${this._isCompact ? ActivitybarPart.COMPACT_ACTION_HEIGHT : ActivitybarPart.ACTION_HEIGHT}px\`);
\t\t\tthis.element.style.setProperty('--activity-bar-icon-size', \`\${this._isCompact ? ActivitybarPart.COMPACT_ICON_SIZE : ActivitybarPart.ICON_SIZE}px\`);`,
    `\t\t\tthis.element.style.setProperty('--activity-bar-action-height', \`\${this.productService.applicationName === 'point' ? 44 : this._isCompact ? ActivitybarPart.COMPACT_ACTION_HEIGHT : ActivitybarPart.ACTION_HEIGHT}px\`);
\t\t\tthis.element.style.setProperty('--activity-bar-icon-size', \`\${this.productService.applicationName === 'point' ? 16 : this._isCompact ? ActivitybarPart.COMPACT_ICON_SIZE : ActivitybarPart.ICON_SIZE}px\`);`,
  ],
  `\t\t\tthis.element.style.setProperty('--activity-bar-action-height', \`\${this.productService.applicationName === 'point' ? 38 : this._isCompact ? ActivitybarPart.COMPACT_ACTION_HEIGHT : ActivitybarPart.ACTION_HEIGHT}px\`);
\t\t\tthis.element.style.setProperty('--activity-bar-icon-size', \`\${this.productService.applicationName === 'point' ? 18 : this._isCompact ? ActivitybarPart.COMPACT_ICON_SIZE : ActivitybarPart.ICON_SIZE}px\`);`,
  'Point activity bar action sizing',
);
replaceAny(
  activitybarPath,
  [
    `\t\tconst actionHeight = this._isCompact ? ActivitybarPart.COMPACT_ACTION_HEIGHT : ActivitybarPart.ACTION_HEIGHT;
\t\tconst iconSize = this._isCompact ? ActivitybarPart.COMPACT_ICON_SIZE : ActivitybarPart.ICON_SIZE;`,
    `\t\tconst actionHeight = this.productService.applicationName === 'point' ? 44 : this._isCompact ? ActivitybarPart.COMPACT_ACTION_HEIGHT : ActivitybarPart.ACTION_HEIGHT;
\t\tconst iconSize = this.productService.applicationName === 'point' ? 16 : this._isCompact ? ActivitybarPart.COMPACT_ICON_SIZE : ActivitybarPart.ICON_SIZE;`,
  ],
  `\t\tconst actionHeight = this.productService.applicationName === 'point' ? 38 : this._isCompact ? ActivitybarPart.COMPACT_ACTION_HEIGHT : ActivitybarPart.ACTION_HEIGHT;
\t\tconst iconSize = this.productService.applicationName === 'point' ? 18 : this._isCompact ? ActivitybarPart.COMPACT_ICON_SIZE : ActivitybarPart.ICON_SIZE;`,
  'Point activity bar composite sizing',
);

const paneCompositeBarPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'paneCompositeBar.ts');
// Незакреплённый инструмент виден в рейке, только пока он открыт: стоит
// переключиться на соседнюю вкладку, и его иконка исчезает. Для помощника это
// читается как поломка продукта — чат «пропал», а способ вернуть его лежит в
// контекстном меню рейки, куда человек не пойдёт, потому что не знает, что
// искать. Один случайный «Скрыть» — и `pinned: false` остаётся в профиле
// навсегда: закрепление по умолчанию срабатывает лишь при первом появлении
// контейнера. Поэтому помощник закрепляется на каждом старте.
replaceAny(
  paneCompositeBarPath,
  [
    `\t\t\tif (!cachedViewContainer) {\n\t\t\t\tthis.compositeBar.pin(viewContainer.id);\n\t\t\t}`,
    `\t\t\tif (!cachedViewContainer || (this.options.partContainerClass === 'activitybar' && viewContainer.id === 'localAgent')) {\n\t\t\t\tthis.compositeBar.pin(viewContainer.id);\n\t\t\t}`,
    `\t\t\tif (!cachedViewContainer || (this.options.partContainerClass === 'activitybar' && viewContainer.id === 'localAgent') || (product.applicationName === 'point' && viewContainer.id === 'workbench.view.extension.pointCompanion')) {\n\t\t\t\tthis.compositeBar.pin(viewContainer.id);\n\t\t\t}`,
  ],
  `\t\t\tif (!cachedViewContainer || (this.options.partContainerClass === 'activitybar' && viewContainer.id === 'localAgent') || (product.applicationName === 'point' && viewContainer.id === 'workbench.view.extension.pointCompanion')) {\n\t\t\t\tthis.compositeBar.pin(viewContainer.id);\n\t\t\t}`,
  'Point AI activity pinning',
);

// ── Правая панель: рейка не исчезает, вкладка открывается и закрывается кликом ──
// У левой панели переключатель есть с самого начала: рейка (ACTIVITYBAR_PART) и
// содержимое (SIDEBAR_PART) — разные части, поэтому клик по активной иконке
// прячет содержимое, а рейка остаётся. У правой рейка живёт ВНУТРИ части
// (замер: `.part.auxiliarybar` 300px = `.title` 44px рейки + `.content` 255px),
// и `setPartHidden` убрал бы вместе с телом сам способ вернуть инструмент.
// Поэтому здесь не «скрыть часть», а «сузить до рейки»: тело схлопывается,
// редактор забирает 256px, иконки остаются на месте.
replaceOnce(
  paneCompositeBarPath,
  `import { IWorkbenchLayoutService, Parts } from '../../services/layout/browser/layoutService.js';`,
  `import { IWorkbenchLayoutService, Parts } from '../../services/layout/browser/layoutService.js';
import product from '../../../platform/product/common/product.js';

// Ширина, до которой правая панель разворачивается обратно. Запоминается при
// сворачивании, чтобы клик возвращал ту ширину, которую человек сам выставил
// сашем, а не константу.
const POINT_AUXILIARY_RAIL_WIDTH = 44;
let pointAuxiliaryBarExpandedWidth = 300;`,
  'Point auxiliary rail toggle imports',
);
replaceOnce(
  paneCompositeBarPath,
  `		if (this.part === Parts.ACTIVITYBAR_PART) {`,
  `		if (product.applicationName === 'point' && this.part === Parts.AUXILIARYBAR_PART) {
			const size = this.layoutService.getSize(Parts.AUXILIARYBAR_PART);
			const collapsed = size.width <= POINT_AUXILIARY_RAIL_WIDTH + 2;
			const active = this.paneCompositePart.getActivePaneComposite();
			if (!collapsed && active?.getId() === this.compositeBarActionItem.id) {
				pointAuxiliaryBarExpandedWidth = size.width;
				this.layoutService.resizePart(Parts.AUXILIARYBAR_PART, POINT_AUXILIARY_RAIL_WIDTH - size.width, 0);
				return;
			}
			if (collapsed) {
				this.layoutService.resizePart(Parts.AUXILIARYBAR_PART, pointAuxiliaryBarExpandedWidth - size.width, 0);
			}
		}

		if (this.part === Parts.ACTIVITYBAR_PART) {`,
  'Point auxiliary rail click toggles the body, not the part',
);
const titlebarPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'titlebar', 'titlebarPart.ts');
const auxiliaryBarPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'auxiliarybar', 'auxiliaryBarPart.ts');
const workbenchLayoutPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'layout.ts');
const viewsExtensionPointPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'api', 'browser', 'viewsExtensionPoint.ts');

// ── Вкладка инструмента не исчезает из рейки ───────────────────────────────
// Контейнеры представлений от расширений регистрируются с `hideIfEmpty: true`.
// Инструменты Point — по одному вебвью в каждом, и как только вьюшка перестаёт
// быть активной (открыли соседнюю вкладку), у контейнера не остаётся активных
// представлений, `shouldBeHidden` отвечает «да», и `hideComposite` убирает
// иконку из рейки. Отсюда жалоба: «при открытии другой вкладки вкладка
// помощника пропадает».
//
// Правится один флаг и только для расширения Point: чужие расширения ведут
// себя как раньше.
replaceOnce(
  viewsExtensionPointPath,
  `import { URI } from '../../../base/common/uri.js';`,
  `import { URI } from '../../../base/common/uri.js';
import product from '../../../platform/product/common/product.js';`,
  'Point views extension point product import',
);
replaceOnce(
  viewsExtensionPointPath,
  `				hideIfEmpty: true,`,
  `				hideIfEmpty: !(product.applicationName === 'point' && extensionId?.value === 'local-agent.local-agent-workbench'),`,
  'Point tool tabs stay in the rail when their view is inactive',
);

const auxiliaryBarActionsPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'auxiliarybar', 'auxiliaryBarActions.ts');

// ── Правая панель не скрывается целиком ────────────────────────────────────
// Рейка инструментов живёт внутри части, и `setPartHidden` убирает её вместе с
// телом: замер показал `partVisible: false`, `railVisible: false` — вернуть
// инструмент становится нечем. Поэтому оба пользовательских способа скрытия —
// переключатель (Ctrl+Alt+B, кнопка раскладки) и «Скрыть правую панель» —
// сворачивают тело до рейки, как это делает клик по активной вкладке.
//
// Патчатся именно действия, а не общий `setPartHidden`: тот же вызов идёт из
// zen mode и восстановления состояния, где скрыть часть целиком — правильно.
replaceOnce(
  auxiliaryBarActionsPath,
  `import { ActivityBarPosition, IWorkbenchLayoutService, LayoutSettings, Parts } from '../../../services/layout/browser/layoutService.js';`,
  `import { ActivityBarPosition, IWorkbenchLayoutService, LayoutSettings, Parts } from '../../../services/layout/browser/layoutService.js';
import product from '../../../../platform/product/common/product.js';

// Ширина рейки и ширина, до которой панель разворачивается обратно.
const POINT_AUXILIARY_RAIL_WIDTH = 44;
let pointAuxiliaryBarRestoreWidth = 300;

// true, если сворачивание/разворачивание взял на себя Point и штатное скрытие
// делать не нужно.
function pointCollapseAuxiliaryBar(layoutService: IWorkbenchLayoutService, hide: boolean): boolean {
	if (product.applicationName !== 'point') {
		return false;
	}
	const size = layoutService.getSize(Parts.AUXILIARYBAR_PART);
	const collapsed = size.width <= POINT_AUXILIARY_RAIL_WIDTH + 2;
	if (hide) {
		if (!collapsed) {
			pointAuxiliaryBarRestoreWidth = size.width;
			layoutService.resizePart(Parts.AUXILIARYBAR_PART, POINT_AUXILIARY_RAIL_WIDTH - size.width, 0);
		}
		return true;
	}
	if (collapsed) {
		layoutService.resizePart(Parts.AUXILIARYBAR_PART, pointAuxiliaryBarRestoreWidth - size.width, 0);
	}
	return true;
}`,
  'Point auxiliary bar collapse helper',
);
replaceOnce(
  auxiliaryBarActionsPath,
  `		layoutService.setPartHidden(isCurrentlyVisible, Parts.AUXILIARYBAR_PART);`,
  `		if (!pointCollapseAuxiliaryBar(layoutService, isCurrentlyVisible)) {
			layoutService.setPartHidden(isCurrentlyVisible, Parts.AUXILIARYBAR_PART);
		}`,
  'Point auxiliary bar toggle collapses instead of hiding',
);
// ── Внизу правой панели не нужны «развернуть» и «закрыть» ──────────────────
// Закрывать нечего: панель больше не скрывается, а сворачивается кликом по
// активной вкладке — кнопка «закрыть» делала бы то же самое второй раз.
// «Развернуть» в рейке инструментов тоже лишнее: ширина меняется сашем.
// Команды остаются доступны через палитру, убирается только кнопка в заголовке.
replaceOnce(
  auxiliaryBarActionsPath,
  `MenuRegistry.appendMenuItem(MenuId.AuxiliaryBarTitle, {
	command: {
		id: ToggleAuxiliaryBarAction.ID,
		title: localize('closeSecondarySideBar', 'Hide Secondary Side Bar'),
		icon: closeIcon
	},
	group: 'navigation',
	order: 2,
	when: ContextKeyExpr.equals(\`config.\${LayoutSettings.ACTIVITY_BAR_LOCATION}\`, ActivityBarPosition.DEFAULT)
});`,
  `if (product.applicationName !== 'point') {
	MenuRegistry.appendMenuItem(MenuId.AuxiliaryBarTitle, {
		command: {
			id: ToggleAuxiliaryBarAction.ID,
			title: localize('closeSecondarySideBar', 'Hide Secondary Side Bar'),
			icon: closeIcon
		},
		group: 'navigation',
		order: 2,
		when: ContextKeyExpr.equals(\`config.\${LayoutSettings.ACTIVITY_BAR_LOCATION}\`, ActivityBarPosition.DEFAULT)
	});
}`,
  'Point drops the auxiliary bar close button',
);
replaceOnce(
  auxiliaryBarActionsPath,
  `			menu: {
				id: MenuId.AuxiliaryBarTitle,
				group: 'navigation',
				order: 1,
			}`,
  `			menu: product.applicationName === 'point' ? undefined : {
				id: MenuId.AuxiliaryBarTitle,
				group: 'navigation',
				order: 1,
			}`,
  'Point drops the auxiliary bar maximize button',
);

replaceOnce(
  auxiliaryBarActionsPath,
  `		accessor.get(IWorkbenchLayoutService).setPartHidden(true, Parts.AUXILIARYBAR_PART);`,
  `		const layoutService = accessor.get(IWorkbenchLayoutService);
		if (!pointCollapseAuxiliaryBar(layoutService, true)) {
			layoutService.setPartHidden(true, Parts.AUXILIARYBAR_PART);
		}`,
  'Point close action collapses the auxiliary bar to the rail',
);

// Пол ширины части — рейка. Со штатными 170px сузить до рейки невозможно:
// раскладка зажимает часть на минимуме, и «свёрнутое» состояние оставалось бы
// полосой в 170px с обрезанным содержимым.
replaceAny(
  auxiliaryBarPath,
  [
    `	override readonly minimumWidth: number = 170;`,
    `	override readonly minimumWidth: number = product.applicationName === 'point' ? 44 : 170;`,
  ],
  `	override readonly minimumWidth: number = product.applicationName === 'point' ? 46 : 170;`,
  'Point auxiliary bar collapses to the rail',
);

replaceOnce(
  workbenchLayoutPath,
  `import { CodeWindow, mainWindow } from '../../base/browser/window.js';`,
  `import { CodeWindow, mainWindow } from '../../base/browser/window.js';\nimport product from '../../platform/product/common/product.js';`,
  'Point workbench layout product import',
);
replaceAny(
  workbenchLayoutPath,
  [
    `\t\t\tlet viewContainerToRestore = this.storageService.get(SidebarPart.activeViewletSettingsKey, StorageScope.WORKSPACE, this.viewDescriptorService.getDefaultViewContainer(ViewContainerLocation.Sidebar)?.id);`,
    `\t\t\tlet viewContainerToRestore = this.storageService.get(SidebarPart.activeViewletSettingsKey, StorageScope.WORKSPACE, product.applicationName === 'point' ? 'workbench.view.explorer' : this.viewDescriptorService.getDefaultViewContainer(ViewContainerLocation.Sidebar)?.id);`,
  ],
  `\t\t\tconst pointInventoryInitializedKey = 'point.workbench.inventory.initialized';
\t\t\tconst pointNeedsInitialInventory = product.applicationName === 'point' && !this.storageService.getBoolean(pointInventoryInitializedKey, StorageScope.WORKSPACE, false);
\t\t\tlet viewContainerToRestore = pointNeedsInitialInventory
\t\t\t\t? 'workbench.view.explorer'
\t\t\t\t: this.storageService.get(SidebarPart.activeViewletSettingsKey, StorageScope.WORKSPACE, this.viewDescriptorService.getDefaultViewContainer(ViewContainerLocation.Sidebar)?.id);
\t\t\tif (pointNeedsInitialInventory) {
\t\t\t\tthis.storageService.store(pointInventoryInitializedKey, true, StorageScope.WORKSPACE, StorageTarget.MACHINE);
\t\t\t}`,
  'Point Inventory first-run fallback',
);
replaceOnce(
  workbenchLayoutPath,
  `\t\t\t} else if (\n\t\t\t\tviewContainerToRestore !== this.viewDescriptorService.getDefaultViewContainer(ViewContainerLocation.Sidebar)?.id &&`,
  `\t\t\t} else if (\n\t\t\t\tproduct.applicationName !== 'point' &&\n\t\t\t\tviewContainerToRestore !== this.viewDescriptorService.getDefaultViewContainer(ViewContainerLocation.Sidebar)?.id &&`,
  'Point preserve explicit Inventory fallback',
);
replaceOnce(
  auxiliaryBarPath,
  `import { VisibleViewContainersTracker } from '../visibleViewContainersTracker.js';`,
  `import { VisibleViewContainersTracker } from '../visibleViewContainersTracker.js';\nimport product from '../../../../platform/product/common/product.js';`,
  'Point auxiliary rail product import',
);
replaceAny(
  auxiliaryBarPath,
  [
    `\t\t\torientation: ActionsOrientation.HORIZONTAL,`,
    `\t\t\torientation: ActionsOrientation.VERTICAL,`,
  ],
  `\t\t\torientation: product.applicationName === 'point' ? ActionsOrientation.VERTICAL : ActionsOrientation.HORIZONTAL,`,
  'Point auxiliary rail native layout orientation',
);
replaceOnce(
  auxiliaryBarPath,
  `\t\t\t\tposition: () => this.getCompositeBarPosition() === CompositeBarPosition.BOTTOM ? HoverPosition.ABOVE : HoverPosition.BELOW,`,
  `\t\t\t\tposition: () => product.applicationName === 'point' ? HoverPosition.LEFT : this.getCompositeBarPosition() === CompositeBarPosition.BOTTOM ? HoverPosition.ABOVE : HoverPosition.BELOW,`,
  'Point auxiliary rail hover position',
);
replaceAny(
  auxiliaryBarPath,
  [
    `\t\t\tcompositeSize: 0,\n\t\t\ticonSize: 16,`,
    `\t\t\tcompositeSize: product.applicationName === 'point' ? 44 : 0,\n\t\t\ticonSize: product.applicationName === 'point' ? 20 : 16,`,
    `\t\t\tcompositeSize: 0,\n\t\t\ticonSize: product.applicationName === 'point' ? 20 : 16,`,
  ],
  `\t\t\tcompositeSize: 0,\n\t\t\ticonSize: product.applicationName === 'point' ? 18 : 16,`,
  'Point auxiliary rail item sizing',
);
// Хром sessions-окна поверх Хаба: командный центр «New Session», действия
// сессии, переключатели раскладки и полоса вкладок с единственной вкладкой.
// Sessions собирается отдельной точкой входа, поэтому стиль нужно импортировать
// и в обычный workbench, и непосредственно в sessions workbench.
const pointAgentsWindowCssSource = path.join(import.meta.dirname, 'resources', 'point-agents-window.css');
if (!fs.existsSync(pointAgentsWindowCssSource)) fail(`Point agents window stylesheet is missing: ${pointAgentsWindowCssSource}`);
fs.copyFileSync(pointAgentsWindowCssSource, path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'titlebar', 'media', 'point-agents-window.css'));
replaceOnce(
  titlebarPath,
  `import './media/titlebarpart.css';`,
  `import './media/titlebarpart.css';
import './media/point-agents-window.css';`,
  'Point agents window stylesheet import',
);
const sessionsWorkbenchPath = path.join(sourceRoot, 'src', 'vs', 'sessions', 'browser', 'workbench.ts');
replaceOnce(
  sessionsWorkbenchPath,
  `import './media/style.css';`,
  `import './media/style.css';
import '../../workbench/browser/parts/titlebar/media/point-agents-window.css';`,
  'Point agents window stylesheet import in sessions workbench',
);
replaceOnce(
  titlebarPath,
  `import { HoverPosition } from '../../../../base/browser/ui/hover/hoverWidget.js';`,
  `import { HoverPosition } from '../../../../base/browser/ui/hover/hoverWidget.js';\nimport product from '../../../../platform/product/common/product.js';\nimport { ICommandService } from '../../../../platform/commands/common/commands.js';\nimport { IWorkspaceContextService } from '../../../../platform/workspace/common/workspace.js';`,
  'Point title bar product import',
);
// Отложенное сворачивание ряда меню: задержка живёт в одноразовом таймере, а он
// обязан умереть вместе с частью заголовка.
replaceOnce(
  titlebarPath,
  `import { DisposableStore, IDisposable, MutableDisposable } from '../../../../base/common/lifecycle.js';`,
  `import { DisposableStore, IDisposable, MutableDisposable } from '../../../../base/common/lifecycle.js';\nimport { disposableTimeout } from '../../../../base/common/async.js';`,
  'Point title bar timeout import',
);
// Высота меняется вместе с макетом, поэтому здесь `replaceAny`: оверлей
// ложится и на дерево, где патч уже стоит с прежним числом, а искать в таком
// дереве исходную строку Code-OSS бесполезно — сборка падала именно на этом.
replaceAny(
  titlebarPath,
  [
    `\t\tlet value = this.isCommandCenterVisible || wcoEnabled ? DEFAULT_CUSTOM_TITLEBAR_HEIGHT : 30;`,
    `\t\tlet value = product.applicationName === 'point' ? 38 : this.isCommandCenterVisible || wcoEnabled ? DEFAULT_CUSTOM_TITLEBAR_HEIGHT : 30;`,
  ],
  `\t\tlet value = product.applicationName === 'point' ? 44 : this.isCommandCenterVisible || wcoEnabled ? DEFAULT_CUSTOM_TITLEBAR_HEIGHT : 30;`,
  'Point title bar height',
);
if (!fs.readFileSync(titlebarPath, 'utf8').includes('const projectLabel =')) {
replaceOnce(
  titlebarPath,
  `\t\tif ((isWindows || isLinux) && !hasNativeTitlebar(this.configurationService, this.titleBarStyle)) {\n\t\t\tthis.appIcon = prepend(this.leftContent, $('a.window-appicon'));\n\t\t}`,
  `\t\tif ((isWindows || isLinux) && !hasNativeTitlebar(this.configurationService, this.titleBarStyle)) {\n\t\t\tthis.appIcon = prepend(this.leftContent, $('a.window-appicon'));\n\t\t}\n\t\tif (!this.isAuxiliary && product.applicationName === 'point') {\n\t\t\tconst projectSwitcher = append(this.leftContent, $('button.point-project-switcher', {\n\t\t\t\ttype: 'button',\n\t\t\t\t'aria-label': localize('point.projectSwitcherLabel', \"Быстрое переключение проектов\"),\n\t\t\t\ttitle: localize('point.projectSwitcherTitle', \"Открыть недавние проекты\"),\n\t\t\t},\n\t\t\t\t$('span.codicon.codicon-folder-library', { 'aria-hidden': 'true' }),\n\t\t\t\t$('span.point-project-switcher-label', {}, localize('point.projects', \"Проекты\")),\n\t\t\t\t$('span.codicon.codicon-chevron-down', { 'aria-hidden': 'true' })));\n\t\t\tthis._register(addDisposableListener(projectSwitcher, EventType.CLICK, () => {\n\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('workbench.action.openRecent'));\n\t\t\t}));\n\t\t}`,
  'Point project switcher',
);
}
if (fs.readFileSync(titlebarPath, 'utf8').includes('const projectLabel =')) {
// Не `replaceOnce`: ниже по цепочке этот же обработчик становится выпадашкой
// проекта, и на повторном проходе по наложенному дереву искать здесь нечего —
// строгая заплата упала бы на исходнике, который уже правее по течению.
replaceIfPresent(
  titlebarPath,
  "\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('workbench.action.openRecent'));",
  "\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('localAgent.switchProject'));",
);
} else replaceOnce(
  titlebarPath,
  `\t\tif (!this.isAuxiliary && product.applicationName === 'point') {
\t\t\tconst projectSwitcher = append(this.leftContent, $('button.point-project-switcher', {
\t\t\t\ttype: 'button',
\t\t\t\t'aria-label': localize('point.projectSwitcherLabel', "Быстрое переключение проектов"),
\t\t\t\ttitle: localize('point.projectSwitcherTitle', "Открыть недавние проекты"),
\t\t\t},
\t\t\t\t$('span.codicon.codicon-folder-library', { 'aria-hidden': 'true' }),
\t\t\t\t$('span.point-project-switcher-label', {}, localize('point.projects', "Проекты")),
\t\t\t\t$('span.codicon.codicon-chevron-down', { 'aria-hidden': 'true' })));
\t\t\tthis._register(addDisposableListener(projectSwitcher, EventType.CLICK, () => {
\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('workbench.action.openRecent'));
\t\t\t}));
\t\t}`,
  `\t\tif (!this.isAuxiliary && product.applicationName === 'point') {
\t\t\tconst workspaceContextService = this.instantiationService.invokeFunction(accessor => accessor.get(IWorkspaceContextService));
\t\t\tconst projectLabel = $('span.point-project-switcher-label');
\t\t\tconst projectSwitcher = append(this.leftContent, $('button.point-project-switcher', {
\t\t\t\ttype: 'button',
\t\t\t\t'aria-label': localize('point.projectSwitcherLabel', "Быстрое переключение проектов"),
\t\t\t\ttitle: localize('point.projectSwitcherTitle', "Открыть недавние проекты"),
\t\t\t},
\t\t\t\t$('span.codicon.codicon-folder-library', { 'aria-hidden': 'true' }),
\t\t\t\tprojectLabel,
\t\t\t\t$('span.codicon.codicon-chevron-down', { 'aria-hidden': 'true' })));
\t\t\tconst updateProjectSwitcher = () => {
\t\t\t\tconst folders = workspaceContextService.getWorkspace().folders;
\t\t\t\tconst label = folders.length > 1
\t\t\t\t\t? localize('point.projectGroup', "{0} +{1}", folders[0].name, folders.length - 1)
\t\t\t\t\t: folders[0]?.name ?? localize('point.projects', "Проекты");
\t\t\t\tprojectLabel.textContent = label;
\t\t\t\tprojectSwitcher.title = folders.length
\t\t\t\t\t? localize('point.switchCurrentProject', "Переключить проект · {0}", label)
\t\t\t\t\t: localize('point.projectSwitcherTitle', "Открыть недавние проекты");
\t\t\t};
\t\t\tupdateProjectSwitcher();
\t\t\tthis._register(workspaceContextService.onDidChangeWorkbenchState(updateProjectSwitcher));
\t\t\tthis._register(workspaceContextService.onDidChangeWorkspaceFolders(updateProjectSwitcher));
\t\t\tthis._register(workspaceContextService.onDidChangeWorkspaceName(updateProjectSwitcher));
\t\t\tthis._register(addDisposableListener(projectSwitcher, EventType.CLICK, () => {
\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('localAgent.switchProject'));
\t\t\t}));
\t\t}`,
  'Point contextual project switcher',
);


if (!fs.readFileSync(titlebarPath, 'utf8').includes('point-title-actions')) {
replaceOnce(
  titlebarPath,
  `\t\t\tthis._register(addDisposableListener(projectSwitcher, EventType.CLICK, () => {
\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('localAgent.switchProject'));
\t\t\t}));
\t\t}`,
  `\t\t\tthis._register(addDisposableListener(projectSwitcher, EventType.CLICK, () => {
\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('localAgent.switchProject'));
\t\t\t}));
\t\t\tconst pointTitleActions = append(this.leftContent, $('div.point-title-actions'));
\t\t\tconst addPointTitleAction = (command: string, icon: string, label: string) => {
\t\t\t\tconst button = append(pointTitleActions, $('button.point-title-action', {
\t\t\t\t\ttype: 'button',
\t\t\t\t\t'aria-label': label,
\t\t\t\t\ttitle: label,
\t\t\t\t}, $('span.codicon', { class: icon, 'aria-hidden': 'true' })));
\t\t\t\tthis._register(addDisposableListener(button, EventType.CLICK, () => {
\t\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand(command));
\t\t\t\t}));
\t\t\t};
\t\t\taddPointTitleAction('localAgent.gitClone', 'codicon codicon-repo-clone', localize('point.cloneGit', "Клонировать проект из Git"));
\t\t\taddPointTitleAction('localAgent.runWithoutDebug', 'codicon codicon-play', localize('point.run', "Запустить проект"));
\t\t\taddPointTitleAction('localAgent.startDebug', 'codicon codicon-debug-alt', localize('point.debug', "Запустить с отладкой"));
\t\t\taddPointTitleAction('localAgent.quickActions', 'codicon codicon-menu', localize('point.quickActions', "Частые действия Point"));
\t\t}`,
  'Point title actions',
);
}

// Чип ветки в заголовке. Ветку держит контрибуция SCM в контекстном ключе —
// тот же источник, из которого шаблон заголовка окна берёт
// `activeRepositoryBranchName`. Читать ключ можно из части заголовка, а
// тянуть туда службу SCM нельзя: контриб лежит слоем выше.
// Ключи объявляются одним блоком. Список прежних форм обязателен: исходник
// Code-OSS правится на месте и живёт между сборками, поэтому дописать сюда
// строку и оставить `replaceOnce` — значит наложить блок второй раз и получить
// «symbol has already been declared» на сборке, а не на проверке.
replaceAny(
  titlebarPath,
  [
    `const pointBranchChipContextKeys = new Set(['scmActiveRepositoryBranchName']);
const pointRunChipContextKeys = new Set(['point.runConfiguration']);

export interface ITitleVariable {`,
    `const pointBranchChipContextKeys = new Set(['scmActiveRepositoryBranchName']);

export interface ITitleVariable {`,
    `export interface ITitleVariable {`,
  ],
  `const pointBranchChipContextKeys = new Set(POINT_BRANCH_CHIP_KEYS);
const pointRunChipContextKeys = new Set(['point.runConfiguration']);

export interface ITitleVariable {`,
  'Point branch chip context keys',
);
if (!fs.readFileSync(titlebarPath, 'utf8').includes('point-branch-chip')) {
replaceOnce(
  titlebarPath,
  `\t\t\tconst pointTitleActions = append(this.leftContent, $('div.point-title-actions'));`,
  `\t\t\tconst branchLabel = $('span.point-branch-chip-label');
\t\t\tconst branchChip = append(this.leftContent, $('button.point-branch-chip', {
\t\t\t\ttype: 'button',
\t\t\t\t'aria-label': localize('point.branchChip', "Текущая ветка"),
\t\t\t},
\t\t\t\t$('span.codicon.codicon-git-branch', { 'aria-hidden': 'true' }),
\t\t\t\tbranchLabel));
\t\t\tconst updateBranchChip = () => {
\t\t\t\tconst branch = this.contextKeyService.getContextKeyValue<string>('scmActiveRepositoryBranchName') ?? '';
\t\t\t\tbranchLabel.textContent = branch;
\t\t\t\tbranchChip.classList.toggle('point-branch-chip-idle', !branch);
\t\t\t\tbranchChip.title = branch
\t\t\t\t\t? localize('point.branchChipOpen', "Ветка {0} · открыть Git", branch)
\t\t\t\t\t: localize('point.branchChipEmpty', "Репозиторий Git не найден");
\t\t\t};
\t\t\tupdateBranchChip();
\t\t\tthis._register(this.contextKeyService.onDidChangeContext(event => {
\t\t\t\tif (event.affectsSome(pointBranchChipContextKeys)) {
\t\t\t\t\tupdateBranchChip();
\t\t\t\t}
\t\t\t}));
\t\t\tthis._register(addDisposableListener(branchChip, EventType.CLICK, () => {
\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('git.checkout'));
\t\t\t}));
\t\t\tconst pointTitleActions = append(this.leftContent, $('div.point-title-actions'));`,
  'Point branch chip',
);
}

// Командный центр — поле поиска, а не повтор заголовка окна. Имя открытого
// файла уже стоит во вкладке и в крошках, а строка «main.go — проект — Point»
// на месте подсказки читается как надпись, и по ней не догадаешься, что это
// поиск по проекту и действиям.
const commandCenterPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'titlebar', 'commandCenterControl.ts');
replaceOnce(
  commandCenterPath,
  `import { WindowTitle } from './windowTitle.js';`,
  `import { WindowTitle } from './windowTitle.js';\nimport product from '../../../../platform/product/common/product.js';`,
  'Point command center product import',
);
// Поле в центре полосы обещает «поиск по проекту и действиям» и показывает
// сочетание Ctrl+N — а выполняло `workbench.action.quickOpenWithModes`, то есть
// поиск файлов по имени. Подпись, сочетание и действие разъезжались втроём.
// Теперь поле зовёт «Поиск везде» Point — ту команду, от которой у него и
// подпись, и сочетание. Класс подменённого действия сохраняется: из него поле
// рисует значок лупы, и без него в поле остаётся один текст.
replaceOnce(
  commandCenterPath,
  `import { IAction, SubmenuAction } from '../../../../base/common/actions.js';`,
  `import { IAction, SubmenuAction, toAction } from '../../../../base/common/actions.js';
import { ICommandService } from '../../../../platform/commands/common/commands.js';`,
  'Point command center search imports',
);
replaceOnce(
  commandCenterPath,
  `\t\t\t\t\treturn this._instaService.createInstance(class CommandCenterQuickPickItem extends BaseActionViewItem {

\t\t\t\t\t\tconstructor() {
\t\t\t\t\t\t\tsuper(undefined, action, options);
\t\t\t\t\t\t}`,
  `\t\t\t\t\tconst pointSearchAction = product.applicationName === 'point'
\t\t\t\t\t\t? toAction({
\t\t\t\t\t\t\tid: 'point.searchWindow',
\t\t\t\t\t\t\tlabel: action.label,
\t\t\t\t\t\t\tclass: action.class,
\t\t\t\t\t\t\trun: () => this._instaService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('point.searchWindow')),
\t\t\t\t\t\t})
\t\t\t\t\t\t: action;

\t\t\t\t\treturn this._instaService.createInstance(class CommandCenterQuickPickItem extends BaseActionViewItem {

\t\t\t\t\t\tconstructor() {
\t\t\t\t\t\t\tsuper(undefined, pointSearchAction, options);
\t\t\t\t\t\t}`,
  'Point command center opens Search Everywhere',
);
replaceOnce(
  commandCenterPath,
  `\t\t\t\t\t\tprivate _getLabel(): string {
\t\t\t\t\t\t\tconst { prefix, suffix } = that._windowTitle.getTitleDecorations();`,
  `\t\t\t\t\t\tprivate _getLabel(): string {
\t\t\t\t\t\t\tif (product.applicationName === 'point') {
\t\t\t\t\t\t\t\treturn localize('point.commandCenterSearch', "Поиск по проекту и действиям");
\t\t\t\t\t\t\t}
\t\t\t\t\t\t\tconst { prefix, suffix } = that._windowTitle.getTitleDecorations();`,
  'Point command center placeholder',
);

// Заплата под сторожем: чип конфигурации ниже вставляет свой код внутрь
// этого тела, и дословная замена перестаёт совпадать сама с собой.
if (!fs.readFileSync(titlebarPath, 'utf8').includes('point-run-actions')) {
// Раскладка заголовка по макету: гамбургер у левого края, чипы проекта и ветки
// следом, запуск и отладка — справа, у элементов управления окном. Заплата
// переписывает уже наложенное тело: блок выше стоит под сторожем и второй раз
// не срабатывает, а исходник Code-OSS правится на месте и живёт между сборками.
replaceAny(
  titlebarPath,
  [
    `\t\t\tconst pointTitleActions = append(this.leftContent, $('div.point-title-actions'));
\t\t\tconst addPointTitleAction = (command: string, icon: string, label: string) => {
\t\t\t\tconst button = append(pointTitleActions, $('button.point-title-action', {
\t\t\t\t\ttype: 'button',
\t\t\t\t\t'aria-label': label,
\t\t\t\t\ttitle: label,
\t\t\t\t}, $('span.codicon', { class: icon, 'aria-hidden': 'true' })));
\t\t\t\tthis._register(addDisposableListener(button, EventType.CLICK, () => {
\t\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand(command));
\t\t\t\t}));
\t\t\t};
\t\t\taddPointTitleAction('localAgent.gitClone', 'codicon codicon-repo-clone', localize('point.cloneGit', "Клонировать проект из Git"));
\t\t\taddPointTitleAction('localAgent.runWithoutDebug', 'codicon codicon-play', localize('point.run', "Запустить проект"));
\t\t\taddPointTitleAction('localAgent.startDebug', 'codicon codicon-debug-alt', localize('point.debug', "Запустить с отладкой"));
\t\t\taddPointTitleAction('localAgent.quickActions', 'codicon codicon-menu', localize('point.quickActions', "Частые действия Point"));`,
  ],
  `\t\t\tconst pointLeadActions = append(this.leftContent, $('div.point-title-actions.point-lead-actions'));
\t\t\tconst pointRunActions = append(this.rightContent, $('div.point-title-actions.point-run-actions'));
\t\t\tconst addPointTitleAction = (host: HTMLElement, command: string, icon: string, label: string, modifier: string = '') => {
\t\t\t\tconst button = append(host, $('button.point-title-action' + modifier, {
\t\t\t\t\ttype: 'button',
\t\t\t\t\t'aria-label': label,
\t\t\t\t\ttitle: label,
\t\t\t\t}, $('span.codicon', { class: icon, 'aria-hidden': 'true' })));
\t\t\t\tthis._register(addDisposableListener(button, EventType.CLICK, () => {
\t\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand(command));
\t\t\t\t}));
\t\t\t};
\t\t\taddPointTitleAction(pointLeadActions, 'localAgent.quickActions', 'codicon codicon-menu', localize('point.quickActions', "Частые действия Point"));
\t\t\taddPointTitleAction(pointRunActions, 'localAgent.runWithoutDebug', 'codicon codicon-play', localize('point.run', "Запустить проект"), '.point-title-action-run');
\t\t\taddPointTitleAction(pointRunActions, 'localAgent.startDebug', 'codicon codicon-debug-alt', localize('point.debug', "Запустить с отладкой"));
			addPointTitleAction(pointRunActions, 'localAgent.searchEverywhere', 'codicon codicon-search', localize('point.searchEverywhere', "Поиск везде · Ctrl+N"), '.point-title-action-search');`,
  'Point title bar layout',
);
}


// Чип ветки открывал список изменений. В JetBrains тот же виджет даёт выбрать
// ветку, поэтому чип зовёт `git.checkout` — переключение и создание ветки в
// одном списке. Список изменений открывается своей кнопкой в рейке и в окне
// инструмента Git. Заплата свежему дереву не нужна — оно получает команду сразу
// из блока «Point branch chip»; здесь она догоняет уже наложенное дерево.
replaceIfPresent(
  titlebarPath,
  `			void this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('localAgent.vcsChanges'));`,
  `			void this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('git.checkout'));`,
);

// ── Ряд меню следует за указателем ─────────────────────────────────────────
// Требование владельца от 30 августа: «выпадашки должны сразу срабатывать когда
// я навожу на кнопки; секция должна скрываться когда я увожу мышку с верхней
// панели».
//
// Двумя неделями раньше уход мыши ряд не закрывал — и правильно, пока список
// открывался только нажатием: пункт был виден, рука шла к нему, а полоса
// исчезала на полпути. Теперь наведение само раскрывает список (заплата к
// `menubar.ts` ниже), список висит **внутри** панели, и пока указатель на нём —
// панель не покинута. Уход с панели значит «я закончил» и закрывает всё.
//
// Блок догоняет уже наложенное дерево: свежее получает тот же код из заплаты
// «Point title bar menu layer».
if (!fs.readFileSync(titlebarPath, 'utf8').includes('point-menu-hover-close')) {
// На чистом дереве этой правки ещё нет: ряд меню целиком вставляет заплата
// ниже по цепочке, и вставляет уже в конечном виде. Здесь — миграция дерева,
// пропатченного прежним оверлеем, поэтому молчаливая форма.
replaceIfPresentAny(
  titlebarPath,
  [
    `			// point-menu-outside-click: ряд меню закрывается нажатием мимо него или
			// клавишей Escape — как любое меню. Раньше он сворачивался, едва указатель
			// уходил с заголовка: пункт был виден, рука шла к нему, а полоса исчезала
			// на полпути. Мышь больше не закрывает то, что открыли нажатием.
			this._register(addDisposableListener(this.rootContainer.ownerDocument, EventType.MOUSE_DOWN, (event: MouseEvent) => {
				if (!this.rootContainer.classList.contains('point-menu')) {
					return;
				}
				const target = event.target as HTMLElement | null;
				if (target && this.rootContainer.contains(target)) {
					return;
				}
				setMenuExpanded(false);
			}));`,
    `			// Уводя указатель, ряд сворачивается — но не пока открыт выпадающий
			// список: он живёт вне заголовка, и полоса схлопывалась бы у человека
			// под рукой на полпути к пункту.
			this._register(addDisposableListener(this.rootContainer, EventType.MOUSE_LEAVE, () => {
				if (!this.rootContainer.querySelector('.menubar-menu-button.open')) {
					setMenuExpanded(false);
				}
			}));`,
  ],
  `			// point-menu-hover-close: ряд открыт, пока указатель на панели. Уходя с
			// неё, полоса сворачивается вместе с раскрытым списком: список висит под
			// своим пунктом внутри панели, поэтому «мышь на списке» — это ещё «мышь
			// на панели», и событие ухода оттуда не приходит. Задержка держит ряд на
			// случайном пересечении края: рука, идущая от гамбургера к «Правке»,
			// иногда срезает угол ниже полосы.
			const menuCollapse = this._register(new MutableDisposable<IDisposable>());
			// point-menu-dismiss: закрыть раскрытый список умеет только сам ряд меню —
			// поле \`menubar\` в его управлении закрыто. Событие и есть весь стык.
			const dismissMenuRow = () => {
				this.rootContainer.querySelector('.menubar')?.dispatchEvent(new CustomEvent('point-menu-dismiss'));
				setMenuExpanded(false);
			};
			this._register(addDisposableListener(this.rootContainer, EventType.MOUSE_ENTER, () => menuCollapse.clear()));
			this._register(addDisposableListener(this.rootContainer, EventType.MOUSE_LEAVE, () => {
				if (!this.rootContainer.classList.contains('point-menu')) {
					return;
				}
				menuCollapse.value = disposableTimeout(() => dismissMenuRow(), 200);
			}));
			// point-menu-outside-click: нажатие мимо панели закрывает ряд сразу, не
			// дожидаясь задержки ухода.
			this._register(addDisposableListener(this.rootContainer.ownerDocument, EventType.MOUSE_DOWN, (event: MouseEvent) => {
				if (!this.rootContainer.classList.contains('point-menu')) {
					return;
				}
				const target = event.target as HTMLElement | null;
				if (target && this.rootContainer.contains(target)) {
					return;
				}
				setMenuExpanded(false);
			}));`,
  'Point menu row follows the pointer',
);
}
// Escape и гамбургер закрывают ряд вместе с раскрытым списком: иначе список
// остаётся открытым в погашенном слое и всплывает при следующем нажатии.
//
// Сторож — по самой функции, а не по маркеру блока выше: обе заплаты догоняют
// деревья, где ряд уже следует за указателем, а блок для них не сработал.
if (fs.readFileSync(titlebarPath, 'utf8').includes('const dismissMenuRow =')) {
replaceIfPresent(
  titlebarPath,
  `			this._register(addDisposableListener(this.rootContainer, EventType.KEY_DOWN, (event: KeyboardEvent) => {
				if (event.key === 'Escape') {
					setMenuExpanded(false);
				}
			}));`,
  `			this._register(addDisposableListener(this.rootContainer, EventType.KEY_DOWN, (event: KeyboardEvent) => {
				if (event.key === 'Escape') {
					dismissMenuRow();
				}
			}));`,
);
replaceIfPresent(
  titlebarPath,
  `			this._register(addDisposableListener(menuToggle, EventType.CLICK, () => {
				setMenuExpanded(!this.rootContainer.classList.contains('point-menu'));
			}));`,
  `			this._register(addDisposableListener(menuToggle, EventType.CLICK, () => {
				if (this.rootContainer.classList.contains('point-menu')) {
					dismissMenuRow();
				} else {
					setMenuExpanded(true);
				}
			}));`,
);
}

// Верхняя панель по образцу JetBrains: слева меню, проект и ветка; справа —
// конфигурация запуска, запуск, отладка и поиск. Командный центр выключен
// (см. configurationDefaults), поэтому поиск живёт кнопкой с лупой, а не полем
// на пол-заголовка, которое вдобавок повторяло заголовок окна целиком.
//
// Заплата правит отдельные строки, а не тело целиком: внутрь тела свой код
// вставляют чип конфигурации и переключатель меню, и дословное совпадение с
// исходником перестаёт работать. Блок выше стоит под сторожем и на уже
// наложенном дереве второй раз не срабатывает.
// Признак «блок компоновки уже наложен» — кнопка запуска, а не лупа: лупу
// ниже по цепочке убирает отдельная заплата, и по ней сторож перестал бы
// узнавать наложенное дерево.
if (!fs.readFileSync(titlebarPath, 'utf8').includes('point-title-action-run')) {
replaceAny(
  titlebarPath,
  [
    `			const pointTitleActions = append(this.leftContent, $('div.point-title-actions'));`,
  ],
  `			// Левая группа действий убрана: клонирование из Git — действие раз в проект, и живёт в палитре.`,
  'Point title bar drops the empty left action group',
);
replaceAny(
  titlebarPath,
  [
    `			addPointTitleAction(pointTitleActions, 'localAgent.gitClone', 'codicon codicon-repo-clone', localize('point.cloneGit', "Клонировать проект из Git"));
			addPointTitleAction(pointRunActions, 'localAgent.runWithoutDebug', 'codicon codicon-play', localize('point.run', "Запустить проект"), '.point-title-action-run');
			addPointTitleAction(pointRunActions, 'localAgent.startDebug', 'codicon codicon-debug-alt', localize('point.debug', "Запустить с отладкой"));`,
  ],
  `			addPointTitleAction(pointRunActions, 'localAgent.runWithoutDebug', 'codicon codicon-play', localize('point.run', "Запустить проект"), '.point-title-action-run');
			addPointTitleAction(pointRunActions, 'localAgent.startDebug', 'codicon codicon-debug-alt', localize('point.debug', "Запустить с отладкой"));
			addPointTitleAction(pointRunActions, 'localAgent.searchEverywhere', 'codicon codicon-search', localize('point.searchEverywhere', "Поиск везде · Ctrl+N"), '.point-title-action-search');`,
  'Point JetBrains title bar',
);
}

// Развёрнутый режим заголовка: гамбургер меняет ряд чипов на ряд меню, как в
// макете. Меню настоящее — то самое, что собирает Code-OSS; мы лишь прячем его
// вторым слоем и показываем по кнопке.
if (!fs.readFileSync(titlebarPath, 'utf8').includes('point-menu-toggle')) {
replaceOnce(
  titlebarPath,
  `\t\t\taddPointTitleAction(pointLeadActions, 'localAgent.quickActions', 'codicon codicon-menu', localize('point.quickActions', "Частые действия Point"));`,
  `\t\t\tconst menuToggle = append(pointLeadActions, $('button.point-title-action.point-menu-toggle', {
\t\t\t\ttype: 'button',
\t\t\t\t'aria-label': localize('point.menu', "Меню"),
\t\t\t\ttitle: localize('point.menu', "Меню"),
\t\t\t}, $('span.codicon.codicon-menu', { 'aria-hidden': 'true' })));
\t\t\tconst setMenuExpanded = (expanded: boolean) => {
\t\t\t\tthis.rootContainer.classList.toggle('point-menu', expanded);
\t\t\t\tmenuToggle.setAttribute('aria-expanded', String(expanded));
\t\t\t};
\t\t\tsetMenuExpanded(false);
\t\t\t// Свернуть ряд — значит свернуть и раскрытый список: иначе он остаётся
\t\t\t// открытым в погашенном слое и всплывает при следующем нажатии.
\t\t\tthis._register(addDisposableListener(menuToggle, EventType.CLICK, () => {
\t\t\t\tif (this.rootContainer.classList.contains('point-menu')) {
\t\t\t\t\tdismissMenuRow();
\t\t\t\t} else {
\t\t\t\t\tsetMenuExpanded(true);
\t\t\t\t}
\t\t\t}));
\t\t\t// point-menu-hover-close: ряд открыт, пока указатель на панели. Уходя с
\t\t\t// неё, полоса сворачивается вместе с раскрытым списком: список висит под
\t\t\t// своим пунктом внутри панели, поэтому «мышь на списке» — это ещё «мышь
\t\t\t// на панели», и событие ухода оттуда не приходит. Задержка держит ряд на
\t\t\t// случайном пересечении края: рука, идущая от гамбургера к «Правке»,
\t\t\t// иногда срезает угол ниже полосы.
\t\t\tconst menuCollapse = this._register(new MutableDisposable<IDisposable>());
\t\t\t// point-menu-dismiss: закрыть раскрытый список умеет только сам ряд меню —
\t\t\t// поле \`menubar\` в его управлении закрыто. Событие и есть весь стык.
\t\t\tconst dismissMenuRow = () => {
\t\t\t\tthis.rootContainer.querySelector('.menubar')?.dispatchEvent(new CustomEvent('point-menu-dismiss'));
\t\t\t\tsetMenuExpanded(false);
\t\t\t};
\t\t\tthis._register(addDisposableListener(this.rootContainer, EventType.MOUSE_ENTER, () => menuCollapse.clear()));
\t\t\tthis._register(addDisposableListener(this.rootContainer, EventType.MOUSE_LEAVE, () => {
\t\t\t\tif (!this.rootContainer.classList.contains('point-menu')) {
\t\t\t\t\treturn;
\t\t\t\t}
\t\t\t\tmenuCollapse.value = disposableTimeout(() => dismissMenuRow(), 200);
\t\t\t}));
\t\t\t// point-menu-outside-click: нажатие мимо панели закрывает ряд сразу, не
\t\t\t// дожидаясь задержки ухода.
\t\t\tthis._register(addDisposableListener(this.rootContainer.ownerDocument, EventType.MOUSE_DOWN, (event: MouseEvent) => {
\t\t\t\tif (!this.rootContainer.classList.contains('point-menu')) {
\t\t\t\t\treturn;
\t\t\t\t}
\t\t\t\tconst target = event.target as HTMLElement | null;
\t\t\t\tif (target && this.rootContainer.contains(target)) {
\t\t\t\t\treturn;
\t\t\t\t}
\t\t\t\tsetMenuExpanded(false);
\t\t\t}));
\t\t\tthis._register(addDisposableListener(this.rootContainer, EventType.KEY_DOWN, (event: KeyboardEvent) => {
\t\t\t\tif (event.key === 'Escape') {
\t\t\t\t\tdismissMenuRow();
\t\t\t\t}
\t\t\t}));`,
  'Point title bar menu layer',
);
}

// ── Наведение раскрывает список ────────────────────────────────────────────
// В Code-OSS первый список открывается нажатием, и только потом соседние
// подхватываются наведением. В JetBrains ряд меню развёрнут постоянно и
// раскрывается под указателем сразу — у Point ряд появляется по гамбургеру, и
// требовать после этого второе нажатие значит просить два действия там, где
// человек уже сказал «покажи меню».
//
// Признак берётся из разметки (`.titlebar-container.point-menu`), а не из
// product.json: `vs/base` не вправе тянуть `vs/platform`, и импорт продукта
// здесь развалил бы проверку слоёв. Заодно это и есть точное условие: наведение
// раскрывает список ровно в развёрнутом ряду Point, а не в любом меню Code-OSS.
const menubarPath = path.join(sourceRoot, 'src', 'vs', 'base', 'browser', 'ui', 'menu', 'menubar.ts');
replaceOnce(
  menubarPath,
  `import { mainWindow } from '../../window.js';

const $ = DOM.$;`,
  `import { mainWindow } from '../../window.js';

const $ = DOM.$;

// point-menubar-hover: развёрнутый ряд верхней панели Point раскрывает список
// наведением, без предварительного нажатия.
function pointMenuOpensOnHover(container: HTMLElement): boolean {
	return !!container.closest('.titlebar-container.point-menu');
}`,
  'Point menubar hover helper',
);
replaceOnce(
  menubarPath,
  `				this._register(DOM.addDisposableListener(buttonElement, DOM.EventType.MOUSE_ENTER, () => {
					if (this.isOpen && !this.isCurrentMenu(menuIndex)) {
						buttonElement.focus();
						this.cleanupCustomMenu();
						this.showCustomMenu(menuIndex, false);
					} else if (this.isFocused && !this.isOpen) {
						this.focusedMenu = { index: menuIndex };
						buttonElement.focus();
					}
				}));`,
  `				this._register(DOM.addDisposableListener(buttonElement, DOM.EventType.MOUSE_ENTER, () => {
					if (this.isOpen && !this.isCurrentMenu(menuIndex)) {
						buttonElement.focus();
						this.cleanupCustomMenu();
						this.showCustomMenu(menuIndex, false);
					} else if (!this.isOpen && pointMenuOpensOnHover(this.container)) {
						buttonElement.focus();
						this.onMenuTriggered(menuIndex, true);
					} else if (this.isFocused && !this.isOpen) {
						this.focusedMenu = { index: menuIndex };
						buttonElement.focus();
					}
				}));`,
  'Point menubar opens a menu on hover',
);
replaceOnce(
  menubarPath,
  `		this._register(DOM.addDisposableListener(buttonElement, DOM.EventType.MOUSE_ENTER, () => {
			if (this.isOpen && !this.isCurrentMenu(MenuBar.OVERFLOW_INDEX)) {
				this.overflowMenu.buttonElement.focus();
				this.cleanupCustomMenu();
				this.showCustomMenu(MenuBar.OVERFLOW_INDEX, false);
			} else if (this.isFocused && !this.isOpen) {
				this.focusedMenu = { index: MenuBar.OVERFLOW_INDEX };
				buttonElement.focus();
			}
		}));`,
  `		this._register(DOM.addDisposableListener(buttonElement, DOM.EventType.MOUSE_ENTER, () => {
			if (this.isOpen && !this.isCurrentMenu(MenuBar.OVERFLOW_INDEX)) {
				this.overflowMenu.buttonElement.focus();
				this.cleanupCustomMenu();
				this.showCustomMenu(MenuBar.OVERFLOW_INDEX, false);
			} else if (!this.isOpen && pointMenuOpensOnHover(this.container)) {
				buttonElement.focus();
				this.onMenuTriggered(MenuBar.OVERFLOW_INDEX, true);
			} else if (this.isFocused && !this.isOpen) {
				this.focusedMenu = { index: MenuBar.OVERFLOW_INDEX };
				buttonElement.focus();
			}
		}));`,
  'Point menubar opens the overflow menu on hover',
);
// Закрыть раскрытый список по просьбе заголовка: указатель ушёл с панели, и ряд
// сворачивается вместе со списком. Поле `menubar` в управлении меню закрыто, и
// событие — единственный стык, который не тянет `vs/base` наверх.
replaceOnce(
  menubarPath,
  `		this._register(DOM.addDisposableListener(window, DOM.EventType.MOUSE_DOWN, () => {
			// This mouse event is outside the menubar so it counts as a focus out
			if (this.isFocused) {
				this.setUnfocusedState();
			}
		}));`,
  `		this._register(DOM.addDisposableListener(window, DOM.EventType.MOUSE_DOWN, () => {
			// This mouse event is outside the menubar so it counts as a focus out
			if (this.isFocused) {
				this.setUnfocusedState();
			}
		}));

		// point-menu-dismiss: верхняя панель Point просит закрыть раскрытый список.
		this._register(DOM.addDisposableListener(this.container, 'point-menu-dismiss', () => {
			this.setUnfocusedState();
		}));`,
  'Point menubar dismiss hook',
);

// ── Верхнее меню по образцу JetBrains ──────────────────────────────────────
// Code-OSS даёт восемь меню, половина из которых про редактор текста: отдельное
// «Выделение» есть только здесь, «Терминал» занимает верхний уровень ради шести
// пунктов, а кода, рефакторинга и Git в строке нет вовсе — при том, что все
// нужные команды у Point давно есть, с жетбрейнсовскими сочетаниями клавиш.
//
// Само меню собирается отдельным файлом: регистраций там полсотни, и внутри
// заплаты они превратились бы в нечитаемую строку. Здесь только перенос файла и
// два сторожа на верхнем уровне.
const pointMenubarSource = path.join(import.meta.dirname, 'resources', 'point-menubar.ts.txt');
if (!fs.existsSync(pointMenubarSource)) fail(`Point menubar contribution is missing: ${pointMenubarSource}`);
fs.writeFileSync(
  path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'titlebar', 'pointMenubar.contribution.ts'),
  fs.readFileSync(pointMenubarSource, 'utf8'),
);
const menubarContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'titlebar', 'menubar.contribution.ts');
replaceOnce(
  menubarContributionPath,
  `import { IsMacNativeContext } from '../../../../platform/contextkey/common/contextkeys.js';`,
  `import { IsMacNativeContext } from '../../../../platform/contextkey/common/contextkeys.js';
import product from '../../../../platform/product/common/product.js';
import './pointMenubar.contribution.js';`,
  'Point menubar contribution import',
);
replaceOnce(
  menubarContributionPath,
  `MenuRegistry.appendMenuItem(MenuId.MenubarMainMenu, {
	submenu: MenuId.MenubarSelectionMenu,
	title: {
		value: 'Selection',
		original: 'Selection',
		mnemonicTitle: localize({ key: 'mSelection', comment: ['&& denotes a mnemonic'] }, "&&Selection")
	},
	order: 3
});`,
  `// Point прячет «Выделение» с верхнего уровня: такого меню нет ни в одной IDE
// JetBrains, а его пункты возвращаются подменю «Правки» — см.
// pointMenubar.contribution.
if (product.applicationName !== 'point') {
	MenuRegistry.appendMenuItem(MenuId.MenubarMainMenu, {
		submenu: MenuId.MenubarSelectionMenu,
		title: {
			value: 'Selection',
			original: 'Selection',
			mnemonicTitle: localize({ key: 'mSelection', comment: ['&& denotes a mnemonic'] }, "&&Selection")
		},
		order: 3
	});
}`,
  'Point hides the Selection menubar entry',
);
replaceOnce(
  menubarContributionPath,
  `MenuRegistry.appendMenuItem(MenuId.MenubarMainMenu, {
	submenu: MenuId.MenubarTerminalMenu,
	title: {
		value: 'Terminal',
		original: 'Terminal',
		mnemonicTitle: localize({ key: 'mTerminal', comment: ['&& denotes a mnemonic'] }, "&&Terminal")
	},
	order: 7
});`,
  `// «Терминал» уходит с верхнего уровня подменю в «Инструменты»: шесть пунктов
// не стоят места в строке, за которое спорят код, запуск и Git.
if (product.applicationName !== 'point') {
	MenuRegistry.appendMenuItem(MenuId.MenubarMainMenu, {
		submenu: MenuId.MenubarTerminalMenu,
		title: {
			value: 'Terminal',
			original: 'Terminal',
			mnemonicTitle: localize({ key: 'mTerminal', comment: ['&& denotes a mnemonic'] }, "&&Terminal")
		},
		order: 7
	});
}`,
  'Point hides the Terminal menubar entry',
);

// Чип конфигурации запуска: точка, имя, клик открывает выбор. Имя приходит
// контекстным ключом от расширения — оно же рисует эту конфигурацию в строке
// состояния, поэтому чип и строка не могут разойтись.
if (!fs.readFileSync(titlebarPath, 'utf8').includes('point-run-chip')) {
replaceOnce(
  titlebarPath,
  `\t\t\tconst pointRunActions = append(this.rightContent, $('div.point-title-actions.point-run-actions'));`,
  `\t\t\tconst pointRunActions = append(this.rightContent, $('div.point-title-actions.point-run-actions'));
\t\t\tconst runChipLabel = $('span.point-run-chip-label');
\t\t\tconst runChip = append(pointRunActions, $('button.point-run-chip', {
\t\t\t\ttype: 'button',
\t\t\t\t'aria-label': localize('point.runChip', "Конфигурация запуска"),
\t\t\t},
\t\t\t\t$('span.point-run-chip-dot', { 'aria-hidden': 'true' }),
\t\t\t\trunChipLabel,
\t\t\t\t$('span.codicon.codicon-chevron-down', { 'aria-hidden': 'true' })));
\t\t\tconst updateRunChip = () => {
\t\t\t\tconst configuration = this.contextKeyService.getContextKeyValue<string>('point.runConfiguration') ?? '';
\t\t\t\trunChipLabel.textContent = configuration;
\t\t\t\trunChip.classList.toggle('point-run-chip-idle', !configuration);
\t\t\t\trunChip.title = configuration
\t\t\t\t\t? localize('point.runChipPick', "Конфигурация {0} · выбрать другую", configuration)
\t\t\t\t\t: localize('point.runChipEmpty', "Конфигурация запуска не выбрана");
\t\t\t};
\t\t\tupdateRunChip();
\t\t\tthis._register(this.contextKeyService.onDidChangeContext(event => {
\t\t\t\tif (event.affectsSome(pointRunChipContextKeys)) {
\t\t\t\t\tupdateRunChip();
\t\t\t\t}
\t\t\t}));
\t\t\tthis._register(addDisposableListener(runChip, EventType.CLICK, () => {
\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('localAgent.selectRunConfiguration'));
\t\t\t}));`,
  'Point run configuration chip',
);
}

// Значок проекта — квадрат с буквой, как в макете: он читается как метка
// проекта, а папка рядом с именем папки ничего не добавляет.
replaceIfPresent(
  titlebarPath,
  `\t\t\t\t$('span.codicon.codicon-folder-library', { 'aria-hidden': 'true' }),
\t\t\t\tprojectLabel,`,
  `\t\t\t\tprojectMark,
\t\t\t\tprojectLabel,`,
);
replaceIfPresent(
  titlebarPath,
  `\t\t\tconst projectLabel = $('span.point-project-switcher-label');`,
  `\t\t\tconst projectLabel = $('span.point-project-switcher-label');
\t\t\tconst projectMark = $('span.point-project-mark', { 'aria-hidden': 'true' });`,
);
replaceIfPresent(
  titlebarPath,
  `\t\t\t\tprojectLabel.textContent = label;`,
  `\t\t\t\tprojectLabel.textContent = label;
\t\t\t\tprojectMark.textContent = (label.trim()[0] ?? 'P').toUpperCase();`,
);

// Число изменений в рабочей копии — контекстным ключом. Оно уже посчитано для
// значка на панели действий; жетон проекта показывает им строку «открыт · N
// изменений», а тянуть в заголовок службу SCM нельзя — контриб лежит слоем выше.
const scmActivityPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'scm', 'browser', 'activity.ts');
replaceOnce(
  scmActivityPath,
  `\tActiveRepositoryBranchName: new RawContextKey<string>('scmActiveRepositoryBranchName', ''),`,
  `\tActiveRepositoryBranchName: new RawContextKey<string>('scmActiveRepositoryBranchName', ''),
\tPointChangeCount: new RawContextKey<number>('point.scmChangeCount', 0),`,
  'Point SCM change count context key',
);
replaceOnce(
  scmActivityPath,
  `\tprivate _activeRepositoryNameContextKey: IContextKey<string>;`,
  `\tprivate _pointChangeCountContextKey: IContextKey<number>;
\tprivate _activeRepositoryNameContextKey: IContextKey<string>;`,
  'Point SCM change count field',
);
replaceOnce(
  scmActivityPath,
  `\t\tthis._activeRepositoryBranchNameContextKey = ActiveRepositoryContextKeys.ActiveRepositoryBranchName.bindTo(this.contextKeyService);`,
  `\t\tthis._activeRepositoryBranchNameContextKey = ActiveRepositoryContextKeys.ActiveRepositoryBranchName.bindTo(this.contextKeyService);
\t\tthis._pointChangeCountContextKey = ActiveRepositoryContextKeys.PointChangeCount.bindTo(this.contextKeyService);`,
  'Point SCM change count binding',
);
// Ключ ставится в уже существующем autorun по значку: там число готово, а
// отдельный autorun рядом с привязкой сработал бы раньше, чем `_countBadge`
// вообще присвоен — конструктор объявляет его ниже.
replaceOnce(
  scmActivityPath,
  `\t\t\tconst countBadge = this._countBadge.read(reader);
\t\t\tthis._updateActivityCountBadge(countBadge, reader.store);`,
  `\t\t\tconst countBadge = this._countBadge.read(reader);
\t\t\tthis._pointChangeCountContextKey.set(countBadge);
\t\t\tthis._updateActivityCountBadge(countBadge, reader.store);`,
  'Point SCM change count update',
);

// Состояние Git для верхней панели: несохранённое, обмен с сервером и ветка-
// источник. Жетон ветки показывал одно имя, и ответить по нему нельзя было ни
// на один вопрос, с которого начинается работа с Git. Ключи ставятся отсюда,
// потому что заголовку служба SCM не видна; разбор подписи обмена живёт в
// модуле самого жетона — его же читает выпадашка, и две копии разошлись бы.
replaceOnce(
  scmActivityPath,
  `import { Command } from '../../../../editor/common/languages.js';`,
  `import { Command } from '../../../../editor/common/languages.js';
import { pointSyncCounts } from '../../../browser/parts/titlebar/pointTitleWidgets.js';`,
  'Point SCM sync counts import',
);
replaceOnce(
  scmActivityPath,
  `\tPointChangeCount: new RawContextKey<number>('point.scmChangeCount', 0),`,
  `\tPointChangeCount: new RawContextKey<number>('point.scmChangeCount', 0),
\tPointDirtyCount: new RawContextKey<number>('point.scmDirty', 0),
\tPointAhead: new RawContextKey<number>('point.scmAhead', 0),
\tPointBehind: new RawContextKey<number>('point.scmBehind', 0),
\tPointUpstream: new RawContextKey<string>('point.scmUpstream', ''),`,
  'Point SCM sync context keys',
);
replaceOnce(
  scmActivityPath,
  `\tprivate _pointChangeCountContextKey: IContextKey<number>;
\tprivate _activeRepositoryNameContextKey: IContextKey<string>;`,
  `\tprivate _pointDirtyCountContextKey: IContextKey<number>;
\tprivate _pointAheadContextKey: IContextKey<number>;
\tprivate _pointBehindContextKey: IContextKey<number>;
\tprivate _pointUpstreamContextKey: IContextKey<string>;
\tprivate readonly _pointResourceCounts = new WeakMap<ISCMRepository, IObservable<number>>();
\tprivate _pointChangeCountContextKey: IContextKey<number>;
\tprivate _activeRepositoryNameContextKey: IContextKey<string>;`,
  'Point SCM sync context key fields',
);
replaceOnce(
  scmActivityPath,
  `\t\tthis._pointChangeCountContextKey = ActiveRepositoryContextKeys.PointChangeCount.bindTo(this.contextKeyService);`,
  `\t\tthis._pointChangeCountContextKey = ActiveRepositoryContextKeys.PointChangeCount.bindTo(this.contextKeyService);
\t\tthis._pointDirtyCountContextKey = ActiveRepositoryContextKeys.PointDirtyCount.bindTo(this.contextKeyService);
\t\tthis._pointAheadContextKey = ActiveRepositoryContextKeys.PointAhead.bindTo(this.contextKeyService);
\t\tthis._pointBehindContextKey = ActiveRepositoryContextKeys.PointBehind.bindTo(this.contextKeyService);
\t\tthis._pointUpstreamContextKey = ActiveRepositoryContextKeys.PointUpstream.bindTo(this.contextKeyService);`,
  'Point SCM sync context key bindings',
);
// Всё считается по активному репозиторию — тому же, чью ветку показывает жетон.
// Иначе в проекте с двумя репозиториями числа были бы от одного, а имя ветки от
// другого, и никакой ошибки при этом не случилось бы: просто чужое состояние.
replaceOnce(
  scmActivityPath,
  `\t\tthis._register(autorun(reader => {
\t\t\tconst activeRepository = this.scmViewService.activeRepository.read(reader);
\t\t\tconst historyItemRefName = this._activeRepositoryHistoryItemRefName.read(reader);

\t\t\tthis._updateActiveRepositoryContextKeys(activeRepository?.repository.provider.name, historyItemRefName);
\t\t}));`,
  `\t\tthis._register(autorun(reader => {
\t\t\tconst activeRepository = this.scmViewService.activeRepository.read(reader);
\t\t\tconst historyItemRefName = this._activeRepositoryHistoryItemRefName.read(reader);

\t\t\tthis._updateActiveRepositoryContextKeys(activeRepository?.repository.provider.name, historyItemRefName);
\t\t}));

\t\tthis._register(autorun(reader => {
\t\t\tconst activeRepository = this.scmViewService.activeRepository.read(reader);
\t\t\tconst provider = activeRepository?.repository.provider;
\t\t\tif (!activeRepository || !provider) {
\t\t\t\tthis._pointDirtyCountContextKey.set(0);
\t\t\t\tthis._pointBehindContextKey.set(0);
\t\t\t\tthis._pointAheadContextKey.set(0);
\t\t\t\tthis._pointUpstreamContextKey.set('');
\t\t\t\treturn;
\t\t\t}
\t\t\t// Считается по группам, а не по \`provider.count\`: число на значке
\t\t\t// подчиняется настройке \`git.countBadge\`, и при \`off\` оно ноль —
\t\t\t// жетон тогда сообщал бы «всё сохранено» на грязной рабочей копии.
\t\t\tthis._pointDirtyCountContextKey.set(this._pointRepositoryResourceCount(activeRepository.repository).read(reader));
\t\t\tconst sync = pointSyncCounts(provider.statusBarCommands.read(reader));
\t\t\tthis._pointBehindContextKey.set(sync.behind);
\t\t\tthis._pointAheadContextKey.set(sync.ahead);
\t\t\tthis._pointUpstreamContextKey.set(provider.historyProvider.read(reader)?.historyItemRemoteRef.read(reader)?.name ?? '');
\t\t}));`,
  'Point SCM sync context key update',
);
replaceOnce(
  scmActivityPath,
  `\tprivate _getRepositoryResourceCount(repository: ISCMRepository): IObservable<number> {`,
  `\t/**
\t * Наблюдаемое число записей рабочей копии — по одному на репозиторий.
\t * Заводить его на каждый прогон нельзя: \`observableFromEvent\` вешает
\t * подписку на владельца, а владелец здесь живёт всё окно, и подписки
\t * копились бы на каждой смене активного репозитория.
\t */
\tprivate _pointRepositoryResourceCount(repository: ISCMRepository): IObservable<number> {
\t\tlet count = this._pointResourceCounts.get(repository);
\t\tif (!count) {
\t\t\tcount = this._getRepositoryResourceCount(repository);
\t\t\tthis._pointResourceCounts.set(repository, count);
\t\t}
\t\treturn count;
\t}

\tprivate _getRepositoryResourceCount(repository: ISCMRepository): IObservable<number> {`,
  'Point SCM per-repository resource count',
);

// Выпадашки жетонов проекта и ветки. Стрелка вниз у жетона проекта стояла с
// самого начала, но обещания не держала: нажатие открывало палитру посреди
// экрана, а не список под жетоном. Списки живут отдельным файлом — внутри
// заплаты они превратились бы в нечитаемую строку; здесь только перенос файла,
// импорт и подмена двух обработчиков.
const pointTitleWidgetsSource = path.join(import.meta.dirname, 'resources', 'point-title-widgets.ts.txt');
// Keep the existing CommandCenter + title-widgets import anchor contiguous.
// Older overlays may have inserted the tool-window import between those lines.
{
  const widgetImport = "import { POINT_BRANCH_CHIP_KEYS, showPointProjectMenu, showPointRunMenu, updatePointBranchChip } from './pointTitleWidgets.js';";
  let seenWidgetImport = false;
  const source = fs.readFileSync(titlebarPath, 'utf8');
  const normalized = source.split('\n').filter(line => {
    if (line.trim() === "import './pointToolWindows.js';") return false;
    if (line.trim() !== widgetImport) return true;
    if (seenWidgetImport) return false;
    seenWidgetImport = true;
    return true;
  }).join('\n');
  if (normalized !== source) fs.writeFileSync(titlebarPath, normalized);
}
if (!fs.existsSync(pointTitleWidgetsSource)) fail(`Point title widgets module is missing: ${pointTitleWidgetsSource}`);
fs.writeFileSync(
  path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'titlebar', 'pointTitleWidgets.ts'),
  fs.readFileSync(pointTitleWidgetsSource, 'utf8'),
);
// Порядок здесь обязателен: сначала привести к нынешнему виду уже наложенный
// импорт, потом добавлять новый. Наоборот — и на наложенном дереве появится
// вторая строка импорта того же модуля рядом с прежней, а сборка упадёт на
// повторе имён. Прежние формы перечислены все: исходник живёт между сборками.
replaceIfPresentAny(
  titlebarPath,
  [
    `import { showPointGitMenu, showPointProjectMenu } from './pointTitleWidgets.js';`,
    `import { showPointGitMenu, showPointProjectMenu, showPointRunMenu } from './pointTitleWidgets.js';`,
    `import { showPointProjectMenu, showPointRunMenu } from './pointTitleWidgets.js';`,
  ],
  `import { POINT_BRANCH_CHIP_KEYS, showPointProjectMenu, showPointRunMenu, updatePointBranchChip } from './pointTitleWidgets.js';`,
);
replaceOnce(
  titlebarPath,
  `import { CommandCenterControl } from './commandCenterControl.js';`,
  `import { CommandCenterControl } from './commandCenterControl.js';
import { POINT_BRANCH_CHIP_KEYS, showPointProjectMenu, showPointRunMenu, updatePointBranchChip } from './pointTitleWidgets.js';`,
  'Point title widgets import',
);
replaceOnce(
  titlebarPath,
  `\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('localAgent.switchProject'));`,
  `\t\t\t\tvoid showPointProjectMenu(this.instantiationService, projectSwitcher);`,
  'Point project widget dropdown',
);
// Список веток даёт служба SCM, а она лежит слоем выше частей рабочего стола:
// заголовок только зовёт команду и отдаёт свой жетон как якорь. Сам список —
// вкладом рядом с остальным SCM.
// Своё окно поиска: две вкладки — файл по имени и строка в содержимом. Живёт
// вкладом поиска, потому что берёт данные у `ISearchService` — того же
// поставщика, на котором работает встроенная панель поиска. Регистрация команд
// и сочетаний внутри самого слоя, поэтому апстрим правится одной строкой
// импорта.
const pointSearchWindowSource = path.join(import.meta.dirname, 'resources', 'point-search-window.ts.txt');
if (!fs.existsSync(pointSearchWindowSource)) fail(`Point search window is missing: ${pointSearchWindowSource}`);
fs.writeFileSync(
  path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'search', 'browser', 'pointSearchWindow.contribution.ts'),
  fs.readFileSync(pointSearchWindowSource, 'utf8'),
);
replaceOnce(
  path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'search', 'browser', 'search.contribution.ts'),
  `import { SearchView } from './searchView.js';`,
  `import { SearchView } from './searchView.js';
import './pointSearchWindow.contribution.js';`,
  'Point search window contribution import',
);

const pointBranchPopupSource = path.join(import.meta.dirname, 'resources', 'point-branch-popup.ts.txt');
if (!fs.existsSync(pointBranchPopupSource)) fail(`Point branch popup is missing: ${pointBranchPopupSource}`);
fs.writeFileSync(
  path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'scm', 'browser', 'pointBranchPopup.contribution.ts'),
  fs.readFileSync(pointBranchPopupSource, 'utf8'),
);
replaceOnce(
  path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'scm', 'browser', 'scm.contribution.ts'),
  `import { localize, localize2 } from '../../../../nls.js';`,
  `import { localize, localize2 } from '../../../../nls.js';
import './pointBranchPopup.contribution.js';`,
  'Point branch popup contribution import',
);
// Прежние формы перечислены обе: сперва жетон звал `git.checkout`, потом меню
// Git, теперь — свой список. Исходник живёт между сборками, и заплата должна
// узнавать любое из этих состояний.
replaceAny(
  titlebarPath,
  [
    `\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('git.checkout'));`,
    `\t\t\t\tshowPointGitMenu(this.instantiationService, branchChip);`,
  ],
  `\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('point.showBranchPopup', branchChip));`,
  'Point branch widget popup',
);
// Жетон ветки тоже раскрывает список — значит, носит ту же стрелку, что жетон
// проекта и конфигурация запуска. Без неё в панели два одинаковых на вид чипа,
// и только один из них выглядит нажимаемым.
replaceOnce(
  titlebarPath,
  `\t\t\t\t$('span.codicon.codicon-git-branch', { 'aria-hidden': 'true' }),
\t\t\t\tbranchLabel));`,
  `\t\t\t\t$('span.codicon.codicon-git-branch', { 'aria-hidden': 'true' }),
\t\t\t\tbranchLabel,
\t\t\t\t$('span.codicon.codicon-chevron-down', { 'aria-hidden': 'true' })));`,
  'Point branch chip chevron',
);
replaceIfPresent(
  titlebarPath,
  `\t\t\t\tbranchChip.title = branch
\t\t\t\t\t? localize('point.branchChipOpen', "Ветка {0} · открыть Git", branch)`,
  `\t\t\t\tbranchChip.title = branch
\t\t\t\t\t? localize('point.branchChipOpen', "Ветка {0} · действия Git", branch)`,
);
// Жетон показывает состояние, а не одно имя: точку несохранённого, счётчики
// обмена с сервером и значок неопубликованной ветки. Разметку и подсказку
// собирает модуль виджетов — в заплате это была бы нечитаемая строка, а
// меняться ей ещё не раз. Прежние формы перечислены обе: исходник Code-OSS
// правится на месте и живёт между сборками.
replaceAny(
  titlebarPath,
  [
    `\t\t\tconst updateBranchChip = () => {
\t\t\t\tconst branch = this.contextKeyService.getContextKeyValue<string>('scmActiveRepositoryBranchName') ?? '';
\t\t\t\tbranchLabel.textContent = branch;
\t\t\t\tbranchChip.classList.toggle('point-branch-chip-idle', !branch);
\t\t\t\tbranchChip.title = branch
\t\t\t\t\t? localize('point.branchChipOpen', "Ветка {0} · действия Git", branch)
\t\t\t\t\t: localize('point.branchChipEmpty', "Репозиторий Git не найден");
\t\t\t};`,
    `\t\t\tconst updateBranchChip = () => {
\t\t\t\tconst branch = this.contextKeyService.getContextKeyValue<string>('scmActiveRepositoryBranchName') ?? '';
\t\t\t\tbranchLabel.textContent = branch;
\t\t\t\tbranchChip.classList.toggle('point-branch-chip-idle', !branch);
\t\t\t\tbranchChip.title = branch
\t\t\t\t\t? localize('point.branchChipOpen', "Ветка {0} · открыть Git", branch)
\t\t\t\t\t: localize('point.branchChipEmpty', "Репозиторий Git не найден");
\t\t\t};`,
  ],
  `\t\t\tconst updateBranchChip = () => {
\t\t\t\tupdatePointBranchChip(branchChip, branchLabel, this.contextKeyService);
\t\t\t};`,
  'Point branch chip state',
);
// Третий жетон панели — конфигурация запуска. Стрелку он носил с самого начала,
// а раскрывал палитру: три жетона подряд обещали список, и ни один его не давал.
replaceOnce(
  titlebarPath,
  `\t\t\t\tvoid this.instantiationService.invokeFunction(accessor => accessor.get(ICommandService).executeCommand('localAgent.selectRunConfiguration'));`,
  `\t\t\t\tshowPointRunMenu(this.instantiationService, runChip);`,
  'Point run widget dropdown',
);
replaceIfPresent(
  titlebarPath,
  `\t\t\t\t\t? localize('point.runChipPick', "Конфигурация {0} · выбрать другую", configuration)`,
  `\t\t\t\t\t? localize('point.runChipPick', "Конфигурация {0} · запуск и настройка", configuration)`,
);
replaceIfPresent(
  titlebarPath,
  `\t\t\t\ttitle: localize('point.projectSwitcherTitle', "Открыть недавние проекты"),`,
  `\t\t\t\ttitle: localize('point.projectSwitcherTitle', "Недавние проекты и действия"),`,
);
replaceIfPresent(
  titlebarPath,
  `\t\t\t\t\t? localize('point.switchCurrentProject', "Переключить проект · {0}", label)
\t\t\t\t\t: localize('point.projectSwitcherTitle', "Открыть недавние проекты");`,
  `\t\t\t\t\t? localize('point.switchCurrentProject', "Проект {0} · недавние и действия", label)
\t\t\t\t\t: localize('point.projectSwitcherTitle', "Недавние проекты и действия");`,
);

// Лупа справа звала «Поиск везде» — и то же самое теперь делает поле в центре
// полосы. Две кнопки одной команды в одной полосе: поле видно всегда, значит
// лишняя из них — лупа. `replaceIfPresent`: на дереве, где заплата уже прошла,
// искать нечего.
removeIfPresent(
  titlebarPath,
  `
			addPointTitleAction(pointRunActions, 'localAgent.searchEverywhere', 'codicon codicon-search', localize('point.searchEverywhere', "Поиск везде · Ctrl+N"), '.point-title-action-search');`,
);

const extensionEnablementPath = path.join(sourceRoot, 'src', 'vs', 'platform', 'extensionManagement', 'common', 'extensionEnablementService.ts');
const extensionEnablementTestPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'services', 'extensionManagement', 'test', 'browser', 'extensionEnablementService.test.ts');
replaceOnce(
  extensionEnablementPath,
  `import { IProfileStorageValueChangeEvent, IStorageService, StorageScope, StorageTarget } from '../../storage/common/storage.js';`,
  `import { IProfileStorageValueChangeEvent, IStorageService, StorageScope, StorageTarget } from '../../storage/common/storage.js';\nimport { IProductService } from '../../product/common/productService.js';`,
  'Point core extension product service import',
);
replaceOnce(
  extensionEnablementPath,
  `\t\t@IExtensionManagementService extensionManagementService: IExtensionManagementService,\n\t) {`,
  `\t\t@IExtensionManagementService extensionManagementService: IExtensionManagementService,\n\t\t@IProductService private readonly productService: IProductService,\n\t) {`,
  'Point core extension product service injection',
);
replaceOnce(
  extensionEnablementPath,
  `\tasync disableExtension(extension: IExtensionIdentifier, source?: string): Promise<boolean> {\n\t\tif (this._addToDisabledExtensions(extension)) {`,
  `\tasync disableExtension(extension: IExtensionIdentifier, source?: string): Promise<boolean> {\n\t\tif (this.isPointCoreExtension(extension)) {\n\t\t\treturn false;\n\t\t}\n\t\tif (this._addToDisabledExtensions(extension)) {`,
  'Point core extension disable protection',
);
replaceOnce(
  extensionEnablementPath,
  `\tgetDisabledExtensions(): IExtensionIdentifier[] {\n\t\treturn this._getExtensions(DISABLED_EXTENSIONS_STORAGE_PATH);\n\t}`,
  `\tgetDisabledExtensions(): IExtensionIdentifier[] {\n\t\treturn this._getExtensions(DISABLED_EXTENSIONS_STORAGE_PATH).filter(extension => !this.isPointCoreExtension(extension));\n\t}\n\n\tprivate isPointCoreExtension(extension: IExtensionIdentifier): boolean {\n\t\treturn this.productService.applicationName === 'point' && areSameExtensions(extension, { id: 'local-agent.local-agent-workbench' });\n\t}`,
  'Point core extension enablement migration',
);
replaceOnce(
  extensionEnablementTestPath,
  `disposables.add(new GlobalExtensionEnablementService(storageService, extensionManagementService)),`,
  `disposables.add(new GlobalExtensionEnablementService(storageService, extensionManagementService, TestProductService)),`,
  'Point core extension enablement test product service',
);
const activitybarTestPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'test', 'browser', 'parts', 'activitybar', 'activitybarPart.test.ts');
replaceOnce(
  activitybarTestPath,
  `import { TestStorageService } from '../../../common/workbenchTestServices.js';`,
  `import { TestProductService, TestStorageService } from '../../../common/workbenchTestServices.js';`,
  'Point activity bar test product import',
);
replaceOnce(
  activitybarTestPath,
  `\t\t\tstorageService,\n\t\t\tconfigService,\n\t\t));`,
  `\t\t\tstorageService,\n\t\t\tconfigService,\n\t\t\tTestProductService,\n\t\t));`,
  'Point activity bar test product service',
);
replaceAny(
  activitybarPath,
  [
    `\t\t\tcompositeSize: 52,`,
    `\t\t\tcompositeSize: this.productService.applicationName === 'point' ? 44 : 52,`,
  ],
  `\t\t\tcompositeSize: this.productService.applicationName === 'point' ? 38 : 52,`,
  'Point activity bar composite item size',
);
replaceOnce(
  activitybarPath,
  `\tprotected override createContentArea(parent: HTMLElement): HTMLElement {
\t\tthis.element = parent;
\t\tthis.content = append(this.element, $('.content'));`,
  `\tprotected override createContentArea(parent: HTMLElement): HTMLElement {
\t\tthis.element = parent;
\t\tthis.element.classList.toggle('point-activitybar', this.productService.applicationName === 'point');
\t\tthis.content = append(this.element, $('.content'));`,
  'Point activity bar class',
);

// Шапка панели проекта называет панель, а не проект: имя проекта уже стоит
// чипом в заголовке окна, и повтор в двух сантиметрах друг от друга ничего не
// сообщает. Заодно шапка перестаёт прыгать при переключении проектов.
const explorerViewPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'files', 'browser', 'views', 'explorerView.ts');
replaceOnce(
  explorerViewPath,
  `import * as nls from '../../../../../nls.js';`,
  `import * as nls from '../../../../../nls.js';\nimport product from '../../../../../platform/product/common/product.js';`,
  'Point explorer product import',
);
replaceOnce(
  explorerViewPath,
  `\tget name(): string {
\t\treturn this.labelService.getWorkspaceLabel(this.contextService.getWorkspace());
\t}`,
  `\tget name(): string {
\t\tif (product.applicationName === 'point') {
\t\t\treturn nls.localize('point.projectPane', "Проект");
\t\t}
\t\treturn this.labelService.getWorkspaceLabel(this.contextService.getWorkspace());
\t}`,
  'Point project pane title',
);

const explorerViewletPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'files', 'browser', 'explorerViewlet.ts');
replaceOnce(
  explorerViewletPath,
  `import { ILogService } from '../../../../platform/log/common/log.js';`,
  `import { ILogService } from '../../../../platform/log/common/log.js';
import product from '../../../../platform/product/common/product.js';`,
  'Point files product import',
);
replaceAny(
  explorerViewletPath,
  [
    `\ttitle: localize2('explore', "Explorer"),`,
    `\ttitle: product.applicationName === 'point' ? localize2('point.files', "Файлы") : localize2('explore', "Explorer"),`,
  ],
  `\ttitle: product.applicationName === 'point' ? localize2('point.files', "Инвентарь") : localize2('explore', "Explorer"),`,
  'Point files container title',
);
replaceAny(
  explorerViewletPath,
  [
    `\thideIfEmpty: true,`,
    `\thideIfEmpty: product.applicationName !== 'point',`,
  ],
  `\thideIfEmpty: product.applicationName !== 'point',`,
  'Point Inventory remains available before async file views register',
);
replaceAny(
  explorerViewletPath,
  [
    `\t\ttitle: localize2('explore', "Explorer"),
\t\tmnemonicTitle: localize({ key: 'miViewExplorer', comment: ['&& denotes a mnemonic'] }, "&&Explorer"),`,
    `\t\ttitle: product.applicationName === 'point' ? localize2('point.files', "Файлы") : localize2('explore', "Explorer"),
\t\tmnemonicTitle: product.applicationName === 'point' ? localize({ key: 'point.miViewFiles', comment: ['&& denotes a mnemonic'] }, "&&Файлы") : localize({ key: 'miViewExplorer', comment: ['&& denotes a mnemonic'] }, "&&Explorer"),`,
  ],
  `\t\ttitle: product.applicationName === 'point' ? localize2('point.files', "Инвентарь") : localize2('explore', "Explorer"),
\t\tmnemonicTitle: product.applicationName === 'point' ? localize({ key: 'point.miViewFiles', comment: ['&& denotes a mnemonic'] }, "&&Инвентарь") : localize({ key: 'miViewExplorer', comment: ['&& denotes a mnemonic'] }, "&&Explorer"),`,
  'Point files open action title',
);
replaceOnce(
  explorerViewletPath,
  `const openFolder = localize('openFolder', "Open Folder");`,
  `const openFolder = product.applicationName === 'point' ? localize('point.openProject', "Открыть проект") : localize('openFolder', "Open Folder");`,
  'Point empty files open-project label',
);
replaceOnce(
  explorerViewletPath,
  `viewsRegistry.registerViewWelcomeContent(EmptyView.ID, {
\tcontent: localize({ key: 'noFolderButEditorsHelp', comment: ['Please do not translate the word "command", it is part of our internal syntax which must not change'] },
\t\t"You have not yet opened a folder.\\n{0}\\nOpening a folder will close all currently open editors. To keep them open, {1} instead.", openFolderButton, addAFolderButton),`,
  `viewsRegistry.registerViewWelcomeContent(EmptyView.ID, {
\tcontent: product.applicationName === 'point'
\t\t? localize({ key: 'point.noFolderButEditorsHelp', comment: ['Please do not translate the word "command", it is part of our internal syntax which must not change'] },
\t\t\t"Откройте проект, чтобы увидеть дерево файлов.\\n{0}", openFolderButton)
\t\t: localize({ key: 'noFolderButEditorsHelp', comment: ['Please do not translate the word "command", it is part of our internal syntax which must not change'] },
\t\t\t"You have not yet opened a folder.\\n{0}\\nOpening a folder will close all currently open editors. To keep them open, {1} instead.", openFolderButton, addAFolderButton),`,
  'Point empty files message with editors',
);
replaceOnce(
  explorerViewletPath,
  `viewsRegistry.registerViewWelcomeContent(EmptyView.ID, {
\tcontent: localize({ key: 'noFolderHelp', comment: ['Please do not translate the word "command", it is part of our internal syntax which must not change'] },
\t\t"You have not yet opened a folder.\\n{0}", openFolderButton),`,
  `viewsRegistry.registerViewWelcomeContent(EmptyView.ID, {
\tcontent: product.applicationName === 'point'
\t\t? localize({ key: 'point.noFolderHelp', comment: ['Please do not translate the word "command", it is part of our internal syntax which must not change'] },
\t\t\t"Откройте проект, чтобы увидеть дерево файлов.\\n{0}", openFolderButton)
\t\t: localize({ key: 'noFolderHelp', comment: ['Please do not translate the word "command", it is part of our internal syntax which must not change'] },
\t\t\t"You have not yet opened a folder.\\n{0}", openFolderButton),`,
  'Point empty files message',
);

const emptyExplorerViewPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'files', 'browser', 'views', 'emptyView.ts');
replaceOnce(
  emptyExplorerViewPath,
  `import { IHoverService } from '../../../../../platform/hover/browser/hover.js';`,
  `import { IHoverService } from '../../../../../platform/hover/browser/hover.js';
import product from '../../../../../platform/product/common/product.js';`,
  'Point empty files product import',
);
replaceOnce(
  emptyExplorerViewPath,
  `\tstatic readonly NAME: ILocalizedString = nls.localize2('noWorkspace', "No Folder Opened");`,
  `\tstatic readonly NAME: ILocalizedString = product.applicationName === 'point' ? nls.localize2('point.noProject', "Проект не открыт") : nls.localize2('noWorkspace', "No Folder Opened");`,
  'Point empty files title',
);

const scmContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'scm', 'browser', 'scm.contribution.ts');
replaceAny(
  scmContributionPath,
  [
    `\ttitle: localize2('source control', 'Source Control'),`,
    `\ttitle: product.applicationName === 'point' ? localize2('point.versions', 'Версии') : localize2('source control', 'Source Control'),`,
  ],
  `\ttitle: product.applicationName === 'point' ? localize2('point.versions', 'Летопись') : localize2('source control', 'Source Control'),`,
  'Point versions container title',
);
replaceAny(
  scmContributionPath,
  [
    `const containerTitle = localize('source control view', "Source Control");`,
    `const containerTitle = product.applicationName === 'point' ? localize('point.versionsView', "Версии") : localize('source control view', "Source Control");`,
  ],
  `const containerTitle = product.applicationName === 'point' ? localize('point.versionsView', "Летопись") : localize('source control view', "Source Control");`,
  'Point versions view title',
);
replaceOnce(
  scmContributionPath,
  `\tid: HISTORY_VIEW_PANE_ID,
\tcontainerTitle,
\tname: localize2('scmGraph', "Graph"),
\tsingleViewPaneContainerTitle: localize('source control graph', "Source Control Graph"),
\tctorDescriptor: new SyncDescriptor(SCMHistoryViewPane),
\tcanToggleVisibility: true,
\tcanMoveView: true,`,
  `\tid: HISTORY_VIEW_PANE_ID,
\tcontainerTitle,
\tname: localize2('scmGraph', "Graph"),
\tsingleViewPaneContainerTitle: localize('source control graph', "Source Control Graph"),
\tctorDescriptor: new SyncDescriptor(SCMHistoryViewPane),
\tcanToggleVisibility: true,
\tcanMoveView: true,
\thideByDefault: product.applicationName === 'point',`,
  'Point hidden-by-default source control graph',
);

// --- Point independence surface: Help / About / Extensions / walkthroughs / chat settings ---

const helpActionsPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'actions', 'helpActions.ts');
replaceOnce(
  helpActionsPath,
  `\t\t\t\t...localize2('openDocumentationUrl', "Documentation"),
\t\t\t\tmnemonicTitle: localize({ key: 'miDocumentation', comment: ['&& denotes a mnemonic'] }, "&&Documentation"),`,
  `\t\t\t\t...(product.applicationName === 'point' ? localize2('point.openDocumentationUrl', "Документация Point") : localize2('openDocumentationUrl', "Documentation")),
\t\t\t\tmnemonicTitle: product.applicationName === 'point' ? localize({ key: 'point.miDocumentation', comment: ['&& denotes a mnemonic'] }, "&&Документация Point") : localize({ key: 'miDocumentation', comment: ['&& denotes a mnemonic'] }, "&&Documentation"),`,
  'Point Help documentation titles',
);
replaceOnce(
  helpActionsPath,
  `\t\t\t\t...localize2('openLicenseUrl', "View License"),
\t\t\t\tmnemonicTitle: localize({ key: 'miLicense', comment: ['&& denotes a mnemonic'] }, "View &&License"),`,
  `\t\t\t\t...(product.applicationName === 'point' ? localize2('point.openLicenseUrl', "Лицензия Point") : localize2('openLicenseUrl', "View License")),
\t\t\t\tmnemonicTitle: product.applicationName === 'point' ? localize({ key: 'point.miLicense', comment: ['&& denotes a mnemonic'] }, "&&Лицензия Point") : localize({ key: 'miLicense', comment: ['&& denotes a mnemonic'] }, "View &&License"),`,
  'Point Help license titles',
);
replaceOnce(
  helpActionsPath,
  `\t\t\t\t...localize2('openPrivacyStatement', "Privacy Statement"),
\t\t\t\tmnemonicTitle: localize({ key: 'miPrivacyStatement', comment: ['&& denotes a mnemonic'] }, "Privac&&y Statement"),`,
  `\t\t\t\t...(product.applicationName === 'point' ? localize2('point.openPrivacyStatement', "Конфиденциальность Point") : localize2('openPrivacyStatement', "Privacy Statement")),
\t\t\t\tmnemonicTitle: product.applicationName === 'point' ? localize({ key: 'point.miPrivacyStatement', comment: ['&& denotes a mnemonic'] }, "&&Конфиденциальность") : localize({ key: 'miPrivacyStatement', comment: ['&& denotes a mnemonic'] }, "Privac&&y Statement"),`,
  'Point Help privacy titles',
);
replaceOnce(
  helpActionsPath,
  `registerAction2(GetStartedWithAccessibilityFeatures);

registerAction2(AskVSCodeCopilot);`,
  `if (product.applicationName !== 'point') {
\tregisterAction2(GetStartedWithAccessibilityFeatures);
\tregisterAction2(AskVSCodeCopilot);
}`,
  'Point hide VS Code Help Copilot and accessibility walkthrough',
);
replaceOnce(
  helpActionsPath,
  `MenuRegistry.appendMenuItem(MenuId.MenubarHelpMenu, {
\tcommand: {
\t\tid: AskVSCodeCopilot.ID,
\t\ttitle: localize2('askVScode', 'Ask @vscode'),
\t},
\torder: 7,
\tgroup: '1_welcome',
\twhen: ContextKeyExpr.and(ContextKeyExpr.equals('chatSetupHidden', false), ContextKeyExpr.equals('chatSetupDisabledInWorkspace', false), IsSessionsWindowContext.negate())
});`,
  `if (product.applicationName !== 'point') {
\tMenuRegistry.appendMenuItem(MenuId.MenubarHelpMenu, {
\t\tcommand: {
\t\t\tid: AskVSCodeCopilot.ID,
\t\t\ttitle: localize2('askVScode', 'Ask @vscode'),
\t\t},
\t\torder: 7,
\t\tgroup: '1_welcome',
\t\twhen: ContextKeyExpr.and(ContextKeyExpr.equals('chatSetupHidden', false), ContextKeyExpr.equals('chatSetupDisabledInWorkspace', false), IsSessionsWindowContext.negate())
\t});
}`,
  'Point hide Ask @vscode Help menu item',
);

replaceOnce(
  windowActionsPath,
  `\t\t\ttitle: {
\t\t\t\t...localize2('about', "About"),
\t\t\t\tmnemonicTitle: localize({ key: 'miAbout', comment: ['&& denotes a mnemonic'] }, "&&About"),
\t\t\t},`,
  `\t\t\ttitle: {
\t\t\t\t...(product.applicationName === 'point' ? localize2('point.about', "О программе Point") : localize2('about', "About")),
\t\t\t\tmnemonicTitle: product.applicationName === 'point' ? localize({ key: 'point.miAbout', comment: ['&& denotes a mnemonic'] }, "&&О программе Point") : localize({ key: 'miAbout', comment: ['&& denotes a mnemonic'] }, "&&About"),
\t\t\t},`,
  'Point About command title',
);

const aboutDialogPath = path.join(sourceRoot, 'src', 'vs', 'platform', 'dialogs', 'electron-browser', 'dialog.ts');
if (!fs.readFileSync(aboutDialogPath, 'utf8').includes('point.aboutDetail')) {
replaceOnce(
  aboutDialogPath,
  `\tconst getDetails = (useAgo: boolean): string => {
\t\treturn localize({ key: 'aboutDetail', comment: ['Electron, Chromium, Node.js and V8 are product names that need no translation'] },`,
  `\tconst getDetails = (useAgo: boolean): string => {
\t\tif (productService.applicationName === 'point') {
\t\t\treturn localize({ key: 'point.aboutDetail', comment: ['Electron, Chromium, Node.js and V8 are product names that need no translation'] },
\t\t\t\t"Версия: {0}\\nКоммит: {1}\\nДата: {2}\\nДвижок: Code-OSS (MIT)\\nElectron: {3}\\nChromium: {4}\\nNode.js: {5}\\nV8: {6}\\nОС: {7}",
\t\t\t\tversion,
\t\t\t\tproductService.commit || 'неизвестно',
\t\t\t\tproductService.date ? \`\${productService.date}\${useAgo ? ' (' + fromNow(new Date(productService.date), true) + ')' : ''}\` : 'неизвестно',
\t\t\t\tprocess.versions['electron'],
\t\t\t\tprocess.versions['chrome'],
\t\t\t\tprocess.versions['node'],
\t\t\t\tprocess.versions['v8'],
\t\t\t\t\`\${osProps.type} \${osProps.arch} \${osProps.release}\${isLinuxSnap ? ' snap' : ''}\`
\t\t\t);
\t\t}
\t\treturn localize({ key: 'aboutDetail', comment: ['Electron, Chromium, Node.js and V8 are product names that need no translation'] },`,
  'Point About dialog Russian details',
);
}
if (!fs.readFileSync(aboutDialogPath, 'utf8').includes('point.aboutTitle')) {
replaceOnce(
  aboutDialogPath,
  `\treturn {
\t\ttitle: productService.nameLong,
\t\tdetails: details,
\t\tdetailsToCopy: detailsToCopy
\t};
}`,
  `\treturn {
\t\ttitle: productService.applicationName === 'point' ? localize('point.aboutTitle', "О программе Point IDE") : productService.nameLong,
\t\tdetails: details,
\t\tdetailsToCopy: detailsToCopy
\t};
}`,
  'Point About dialog title',
);
}

replaceOnce(
  extensionsContributionPath,
  `export const VIEW_CONTAINER = Registry.as<IViewContainersRegistry>(ViewContainerExtensions.ViewContainersRegistry).registerViewContainer(
\t{
\t\tid: VIEWLET_ID,
\t\ttitle: localize2('extensions', "Extensions"),
\t\topenCommandActionDescriptor: {
\t\t\tid: VIEWLET_ID,
\t\t\tmnemonicTitle: localize({ key: 'miViewExtensions', comment: ['&& denotes a mnemonic'] }, "E&&xtensions"),
\t\t\tkeybindings: { primary: KeyMod.CtrlCmd | KeyMod.Shift | KeyCode.KeyX },
\t\t\torder: 4,
\t\t},
\t\tctorDescriptor: new SyncDescriptor(ExtensionsViewPaneContainer),
\t\ticon: extensionsViewIcon,
\t\torder: 4,
\t\trejectAddedViews: true,
\t\talwaysUseContainerInfo: true,
\t}, ViewContainerLocation.Sidebar);`,
  `export const VIEW_CONTAINER = Registry.as<IViewContainersRegistry>(ViewContainerExtensions.ViewContainersRegistry).registerViewContainer(
\t{
\t\tid: VIEWLET_ID,
\t\ttitle: product.applicationName === 'point' ? localize2('point.relics', "Реликвии") : localize2('extensions', "Extensions"),
\t\topenCommandActionDescriptor: {
\t\t\tid: VIEWLET_ID,
\t\t\tmnemonicTitle: product.applicationName === 'point' ? localize({ key: 'point.miViewRelics', comment: ['&& denotes a mnemonic'] }, "&&Реликвии") : localize({ key: 'miViewExtensions', comment: ['&& denotes a mnemonic'] }, "E&&xtensions"),
\t\t\tkeybindings: { primary: KeyMod.CtrlCmd | KeyMod.Shift | KeyCode.KeyX },
\t\t\torder: 4,
\t\t},
\t\tctorDescriptor: new SyncDescriptor(ExtensionsViewPaneContainer),
\t\ticon: extensionsViewIcon,
\t\torder: 4,
\t\trejectAddedViews: true,
\t\talwaysUseContainerInfo: true,
\t}, ViewContainerLocation.Sidebar);`,
  'Point relics view container title',
);
replaceOnce(
  extensionsContributionPath,
  `\t\ttitle: localize('extensionsConfigurationTitle', "Extensions"),`,
  `\t\ttitle: product.applicationName === 'point' ? localize('point.relicsConfigurationTitle', "Реликвии") : localize('extensionsConfigurationTitle', "Extensions"),`,
  'Point relics settings category title',
);
replaceOnce(
  extensionsContributionPath,
  `\t\t\t\tdescription: localize('extensions.autoUpdate', "Controls the automatic update behavior of extensions. The updates are fetched from a Microsoft online service."),`,
  `\t\t\t\tdescription: product.applicationName === 'point' ? localize('point.extensions.autoUpdate', "Автообновление реликвий из каталога Point (Open VSX).") : localize('extensions.autoUpdate', "Controls the automatic update behavior of extensions. The updates are fetched from a Microsoft online service."),`,
  'Point relics auto-update setting copy',
);
replaceOnce(
  extensionsContributionPath,
  `\t\t\t\tdescription: localize('extensionsCheckUpdates', "When enabled, automatically checks extensions for updates. If an extension has an update, it is marked as outdated in the Extensions view. The updates are fetched from a Microsoft online service."),`,
  `\t\t\t\tdescription: product.applicationName === 'point' ? localize('point.extensionsCheckUpdates', "Автоматически проверять обновления реликвий в каталоге Point (Open VSX).") : localize('extensionsCheckUpdates', "When enabled, automatically checks extensions for updates. If an extension has an update, it is marked as outdated in the Extensions view. The updates are fetched from a Microsoft online service."),`,
  'Point relics update-check setting copy',
);

const extensionsViewletPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'extensions', 'browser', 'extensionsViewlet.ts');
replaceOnce(
  extensionsViewletPath,
  `import { ExtensionsListView, EnabledExtensionsView, DisabledExtensionsView, RecommendedExtensionsView, WorkspaceRecommendedExtensionsView, ServerInstalledExtensionsView, DefaultRecommendedExtensionsView, UntrustedWorkspaceUnsupportedExtensionsView, UntrustedWorkspacePartiallySupportedExtensionsView, VirtualWorkspaceUnsupportedExtensionsView, VirtualWorkspacePartiallySupportedExtensionsView, DefaultPopularExtensionsView, DeprecatedExtensionsView, SearchMarketplaceExtensionsView, RecentlyUpdatedExtensionsView, OutdatedExtensionsView, StaticQueryExtensionsView, NONE_CATEGORY, AbstractExtensionsListView } from './extensionsViews.js';`,
  `import { ExtensionsListView, EnabledExtensionsView, DisabledExtensionsView, RecommendedExtensionsView, WorkspaceRecommendedExtensionsView, ServerInstalledExtensionsView, DefaultRecommendedExtensionsView, UntrustedWorkspaceUnsupportedExtensionsView, UntrustedWorkspacePartiallySupportedExtensionsView, VirtualWorkspaceUnsupportedExtensionsView, VirtualWorkspacePartiallySupportedExtensionsView, DefaultPopularExtensionsView, DeprecatedExtensionsView, SearchMarketplaceExtensionsView, RecentlyUpdatedExtensionsView, OutdatedExtensionsView, StaticQueryExtensionsView, NONE_CATEGORY, AbstractExtensionsListView } from './extensionsViews.js';
import product from '../../../../platform/product/common/product.js';`,
  'Point relics viewlet product import',
);
{
  const viewletSource = fs.readFileSync(extensionsViewletPath, 'utf8');
  const marketplaceNameOriginal = `\t\t\tname: localize2('marketPlace', "Marketplace"),`;
  const marketplaceNamePoint = `\t\t\tname: product.applicationName === 'point' ? localize2('point.catalog', "Каталог Point") : localize2('marketPlace', "Marketplace"),`;
  if (!viewletSource.includes(marketplaceNamePoint)) {
    if (!viewletSource.includes(marketplaceNameOriginal)) fail(`Could not locate Marketplace view name in ${extensionsViewletPath}`);
    fs.writeFileSync(extensionsViewletPath, viewletSource.split(marketplaceNameOriginal).join(marketplaceNamePoint));
  }
}
replaceOnce(
  extensionsViewletPath,
  `\t\tconst placeholder = localize('searchExtensions', "Search Extensions in Marketplace");`,
  `\t\tconst placeholder = product.applicationName === 'point' ? localize('point.searchRelics', "Поиск реликвий в каталоге Point") : localize('searchExtensions', "Search Extensions in Marketplace");`,
  'Point relics search placeholder',
);
replaceOnce(
  extensionsViewletPath,
  `\t\t\tcontent: localize('sign in', "[Sign in to access Extensions Marketplace]({0})", \`command:\${DEFAULT_ACCOUNT_SIGN_IN_COMMAND}\`),`,
  `\t\t\tcontent: product.applicationName === 'point'
\t\t\t\t? localize('point.catalogOpen', "Каталог Point использует Open VSX и не требует аккаунта Microsoft.")
\t\t\t\t: localize('sign in', "[Sign in to access Extensions Marketplace]({0})", \`command:\${DEFAULT_ACCOUNT_SIGN_IN_COMMAND}\`),`,
  'Point catalog sign-in welcome',
);

const gettingStartedServicePath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'welcomeGettingStarted', 'browser', 'gettingStartedService.ts');
replaceOnce(
  gettingStartedServicePath,
  `import { walkthroughs } from '../common/gettingStartedContent.js';`,
  `import { walkthroughs } from '../common/gettingStartedContent.js';
import product from '../../../../platform/product/common/product.js';`,
  'Point walkthrough service product import',
);
replaceOnce(
  gettingStartedServicePath,
  `\tprivate registerWalkthroughs() {

\t\twalkthroughs.forEach(async (category, index) => {

\t\t\tthis._registerWalkthrough({`,
  `\tprivate registerWalkthroughs() {
\t\t// Point ships its own onboarding/home; skip upstream VS Code walkthrough chrome.
\t\tif (product.applicationName !== 'point') {
\t\twalkthroughs.forEach(async (category, index) => {

\t\t\tthis._registerWalkthrough({`,
  'Point skip builtin VS Code walkthroughs open',
);
replaceOnce(
  gettingStartedServicePath,
  `\t\t\t});
\t\t});

\t\twalkthroughsExtensionPoint.setHandler((_, { added, removed }) => {
\t\t\tadded.map(e => this.registerExtensionWalkthroughContributions(e.description));
\t\t\tremoved.map(e => this.unregisterExtensionWalkthroughContributions(e.description));
\t\t});
\t}`,
  `\t\t\t});
\t\t});
\t\t}

\t\twalkthroughsExtensionPoint.setHandler((_, { added, removed }) => {
\t\t\tadded.map(e => this.registerExtensionWalkthroughContributions(e.description));
\t\t\tremoved.map(e => this.unregisterExtensionWalkthroughContributions(e.description));
\t\t});
\t}`,
  'Point skip builtin VS Code walkthroughs close',
);

const chatSharedContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'chat', 'browser', 'chat.shared.contribution.ts');
replaceOnce(
  chatSharedContributionPath,
  `\t\t'chat.experimentalSessionsWindowOverride': {
\t\t\ttype: 'boolean',
\t\t\tdescription: nls.localize('chat.experimentalSessionsWindowOverride', "When true, enables sessions-window-specific behavior for extensions."),
\t\t\tdefault: false,
\t\t\ttags: ['experimental'],
\t\t\tagentsWindow: { default: true },
\t\t},`,
  `\t\t'chat.experimentalSessionsWindowOverride': {
\t\t\ttype: 'boolean',
\t\t\tdescription: nls.localize('chat.experimentalSessionsWindowOverride', "When true, enables sessions-window-specific behavior for extensions."),
\t\t\tdefault: false,
\t\t\ttags: ['experimental'],
\t\t\tincluded: product.applicationName !== 'point',
\t\t\tagentsWindow: { default: true },
\t\t},`,
  'Point hide experimental chat sessions setting',
);
replaceOnce(
  chatSharedContributionPath,
  `\t\t[ChatConfiguration.AIDisabled]: {
\t\t\ttype: 'boolean',
\t\t\tdescription: nls.localize('chat.disableAIFeatures', "Disable and hide built-in AI features provided by GitHub Copilot, including chat and inline suggestions."),
\t\t\tdefault: false,
\t\t\tscope: ConfigurationScope.WINDOW,
\t\t},
\t\t[ChatConfiguration.TitleBarSignInEnabled]: {
\t\t\ttype: 'boolean',
\t\t\tdescription: nls.localize('chat.titleBar.signIn.enabled', "Controls whether the Copilot Sign In button is shown in the title bar when signed out. When disabled, the Sign In affordance falls back to the status bar."),
\t\t\tdefault: true,
\t\t},
\t\t[ChatConfiguration.TitleBarOpenInAgentsWindowEnabled]: {
\t\t\ttype: 'boolean',
\t\t\tdescription: nls.localize('chat.titleBar.openInAgentsWindow.enabled', "Controls whether the Open in Agents Window button is shown in the title bar."),
\t\t\tdefault: true,
\t\t},`,
  `\t\t[ChatConfiguration.AIDisabled]: {
\t\t\ttype: 'boolean',
\t\t\tdescription: nls.localize('chat.disableAIFeatures', "Disable and hide built-in AI features provided by GitHub Copilot, including chat and inline suggestions."),
\t\t\tdefault: false,
\t\t\tscope: ConfigurationScope.WINDOW,
\t\t\tincluded: product.applicationName !== 'point',
\t\t},
\t\t[ChatConfiguration.TitleBarSignInEnabled]: {
\t\t\ttype: 'boolean',
\t\t\tdescription: nls.localize('chat.titleBar.signIn.enabled', "Controls whether the Copilot Sign In button is shown in the title bar when signed out. When disabled, the Sign In affordance falls back to the status bar."),
\t\t\tdefault: true,
\t\t\tincluded: product.applicationName !== 'point',
\t\t},
\t\t[ChatConfiguration.TitleBarOpenInAgentsWindowEnabled]: {
\t\t\ttype: 'boolean',
\t\t\tdescription: nls.localize('chat.titleBar.openInAgentsWindow.enabled', "Controls whether the Open in Agents Window button is shown in the title bar."),
\t\t\tdefault: true,
\t\t\tincluded: product.applicationName !== 'point',
\t\t},`,
  'Point hide Copilot chat settings from Settings UI',
);

// Russian language pack: high-visibility VS Code / Marketplace chrome → Point wording.
const mainI18nPath = path.join(languagePackTarget, 'translations', 'main.i18n.json');
const mainI18n = readJson(mainI18nPath);
const pointMainI18nPatches = {
  'vs/workbench/contrib/extensions/browser/extensionsViewlet': {
    marketPlace: 'Каталог Point',
    searchExtensions: 'Поиск реликвий в каталоге Point',
    suggestProxyError: 'Каталог Point вернул ECONNREFUSED. Проверьте параметр http.proxy.',
    'sign in enterprise marketplace': 'Войдите для доступа к каталогу',
  },
  'vs/workbench/contrib/extensions/browser/extensions.contribution': {
    extensions: 'Реликвии',
    miViewExtensions: '&&Реликвии',
    miPreferencesExtensions: '&&Реликвии',
    InstallVSIXAction_successReload: undefined, // keyed differently below
  },
  'vs/workbench/browser/actions/helpActions': {
    newsletterSignup: 'Подписаться на новости Point',
  },
  'vs/workbench/contrib/welcomeGettingStarted/common/gettingStartedContent': {
    'gettingStarted.setup.title': 'Начало работы с Point',
    'gettingStarted.setup.walkthroughPageTitle': 'Настройка Point',
    'gettingStarted.setupWeb.title': 'Начало работы с Point в Интернете',
    'gettingStarted.setupWeb.walkthroughPageTitle': 'Настройка веб-интерфейса Point',
  },
};
for (const [sectionId, patches] of Object.entries(pointMainI18nPatches)) {
  const section = mainI18n.contents?.[sectionId];
  if (!section || typeof section !== 'object') continue;
  for (const [key, value] of Object.entries(patches)) {
    if (value === undefined) continue;
    if (Object.prototype.hasOwnProperty.call(section, key)) section[key] = value;
  }
}
const extensionsContributionI18n = mainI18n.contents?.['vs/workbench/contrib/extensions/browser/extensions.contribution'];
if (extensionsContributionI18n) {
  for (const [key, value] of Object.entries(extensionsContributionI18n)) {
    if (typeof value !== 'string') continue;
    extensionsContributionI18n[key] = value
      .replaceAll('Visual Studio Code', 'Point')
      .replaceAll('Visual Studio Marketplace', 'каталог Point')
      .replaceAll('Marketplace', 'каталог Point');
  }
}
writeJson(mainI18nPath, mainI18n);

const extensionsCommonPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'extensions', 'common', 'extensions.ts');
replaceOnce(
  extensionsCommonPath,
  `import { localize2 } from '../../../../nls.js';`,
  `import { localize2 } from '../../../../nls.js';
import product from '../../../../platform/product/common/product.js';`,
  'Point relics category product import',
);
replaceOnce(
  extensionsCommonPath,
  `export const EXTENSIONS_CATEGORY = localize2('extensions', "Extensions");`,
  `export const EXTENSIONS_CATEGORY = product.applicationName === 'point' ? localize2('point.relics', "Реликвии") : localize2('extensions', "Extensions");`,
  'Point relics command category',
);

// Point: Find in Files opens as a bottom tool window (JetBrains-like), not the activity sidebar.
const searchContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'search', 'browser', 'search.contribution.ts');
replaceOnce(
  searchContributionPath,
  `import { AccessibleViewRegistry } from '../../../../platform/accessibility/browser/accessibleViewRegistry.js';
import { SearchAccessibilityHelp } from './searchAccessibilityHelp.js';`,
  `import { AccessibleViewRegistry } from '../../../../platform/accessibility/browser/accessibleViewRegistry.js';
import { SearchAccessibilityHelp } from './searchAccessibilityHelp.js';
import product from '../../../../platform/product/common/product.js';`,
  'Point search contribution product import',
);
replaceOnce(
  searchContributionPath,
  `}, ViewContainerLocation.Sidebar, { doNotRegisterOpenCommand: true });`,
  `}, product.applicationName === 'point' ? ViewContainerLocation.Panel : ViewContainerLocation.Sidebar, { doNotRegisterOpenCommand: true });`,
  'Point search view container in panel',
);

// Quiet JetBrains-like busy stripe: extension sets context `point.statusBusy`;
// CSS already styles `.part.statusbar.point-busy::before`.
// Use the static product module (not constructor DI) so Main/Auxiliary
// StatusbarPart subclasses keep their existing super(...) call sites.
const statusbarPartPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'statusbar', 'statusbarPart.ts');
replaceOnce(
  statusbarPartPath,
  `import { IContextKeyService } from '../../../../platform/contextkey/common/contextkey.js';`,
  `import { IContextKeyService } from '../../../../platform/contextkey/common/contextkey.js';\nimport product from '../../../../platform/product/common/product.js';`,
  'Point status bar product import',
);
replaceOnce(
  statusbarPartPath,
  `\t\t// Initial status bar entries\n\t\tthis.createInitialStatusbarEntries();\n\n\t\treturn this.element;`,
  `\t\t// Initial status bar entries\n\t\tthis.createInitialStatusbarEntries();\n\n\t\tif (product.applicationName === 'point') {\n\t\t\tconst busyKeys = new Set(['point.statusBusy']);\n\t\t\tconst syncBusyClass = () => {\n\t\t\t\tthis.element.classList.toggle('point-busy', this.contextKeyService.getContextKeyValue('point.statusBusy') === true);\n\t\t\t};\n\t\t\tsyncBusyClass();\n\t\t\tthis._register(this.contextKeyService.onDidChangeContext(e => {\n\t\t\t\tif (e.affectsSome(busyKeys)) {\n\t\t\t\t\tsyncBusyClass();\n\t\t\t\t}\n\t\t\t}));\n\t\t}\n\n\t\treturn this.element;`,
  'Point status bar busy class wiring',
);

const resourceSource = path.join(import.meta.dirname, 'resources');
const pointActivitybarCssSource = path.join(resourceSource, 'point-activitybar.css');
if (!fs.existsSync(pointActivitybarCssSource)) fail(`Point activity bar stylesheet is missing: ${pointActivitybarCssSource}`);
fs.copyFileSync(pointActivitybarCssSource, path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'activitybar', 'media', 'point-activitybar.css'));
const pointWorkbenchCssSource = path.join(resourceSource, 'point-workbench.css');
if (!fs.existsSync(pointWorkbenchCssSource)) fail(`Point workbench stylesheet is missing: ${pointWorkbenchCssSource}`);
fs.copyFileSync(pointWorkbenchCssSource, path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'activitybar', 'media', 'point-workbench.css'));
// Шрифт оболочки — Inter из макета. Начертания вшиваются в стиль как data:
// вместо ссылок на файлы: сборщик Code-OSS переносит в `out/` только те
// вложения, которые знает сам, и молчит о пропущенных — шрифт бы просто не
// приехал, а на месте Inter оказался бы системный Segoe и никто бы не заметил.
const fontsSource = path.join(resourceSource, 'fonts');
const pointFontFaces = [
  { weight: 400, subset: 'latin' },
  { weight: 500, subset: 'latin' },
  { weight: 600, subset: 'latin' },
  { weight: 400, subset: 'cyrillic' },
  { weight: 500, subset: 'cyrillic' },
  { weight: 600, subset: 'cyrillic' },
];
const pointFontRanges = {
  latin: 'U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA, U+02DC, U+0304, U+0308, U+0329, U+2000-206F, U+20AC, U+2122, U+2191, U+2193, U+2212, U+2215, U+FEFF, U+FFFD',
  cyrillic: 'U+0301, U+0400-045F, U+0490-0491, U+04B0-04B1, U+2116',
};
const pointFontCss = ['/* Inter (SIL OFL 1.1) — начертания оболочки Point. Файл собирается наложением\n   из distribution/resources/fonts; править надо там. */'];
for (const face of pointFontFaces) {
  const file = path.join(fontsSource, `inter-${face.subset}-${face.weight}.woff2`);
  if (!fs.existsSync(file)) fail(`Point interface font is missing: ${file}`);
  const data = fs.readFileSync(file).toString('base64');
  pointFontCss.push(`@font-face {\n\tfont-family: 'Inter';\n\tfont-style: normal;\n\tfont-weight: ${face.weight};\n\tfont-display: block;\n\tsrc: url(data:font/woff2;base64,${data}) format('woff2');\n\tunicode-range: ${pointFontRanges[face.subset]};\n}`);
}
fs.writeFileSync(path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'activitybar', 'media', 'point-fonts.css'), `${pointFontCss.join('\n')}\n`);

const pointAgentIconSource = path.join(resourceSource, 'point-agent.svg');
if (!fs.existsSync(pointAgentIconSource)) fail(`Point agent icon is missing: ${pointAgentIconSource}`);
fs.copyFileSync(pointAgentIconSource, path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'activitybar', 'media', 'point-agent.svg'));
for (const name of ['code.ico', 'code_70x70.png', 'code_150x150.png']) {
  const source = path.join(resourceSource, name);
  if (!fs.existsSync(source)) fail(`Run generate-icons.mjs first: ${source} is missing`);
  fs.copyFileSync(source, path.join(sourceRoot, 'resources', 'win32', name));
}
const titlebarIconSource = path.join(resourceSource, 'point-titlebar.svg');
if (!fs.existsSync(titlebarIconSource)) fail(`Point titlebar icon is missing: ${titlebarIconSource}`);
fs.copyFileSync(titlebarIconSource, path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'media', 'code-icon.svg'));

// Seti ships file glyphs only; Point adds open/closed folder icons so trees
// stay readable like JetBrains without replacing the whole icon theme.
const pointFileIconSource = path.join(resourceSource, 'fileicons');
const setiIconsDir = path.join(sourceRoot, 'extensions', 'theme-seti', 'icons');
const setiThemePath = path.join(setiIconsDir, 'vs-seti-icon-theme.json');
if (!fs.existsSync(setiThemePath)) fail(`Seti icon theme is missing: ${setiThemePath}`);
for (const name of ['folder.svg', 'folder-open.svg', 'root-folder.svg', 'root-folder-open.svg']) {
  const source = path.join(pointFileIconSource, name);
  if (!fs.existsSync(source)) fail(`Point folder icon is missing: ${source}`);
  fs.copyFileSync(source, path.join(setiIconsDir, name));
}
const setiTheme = readJson(setiThemePath);
setiTheme.iconDefinitions = setiTheme.iconDefinitions || {};
setiTheme.iconDefinitions['_point_folder'] = { iconPath: './folder.svg' };
setiTheme.iconDefinitions['_point_folder_open'] = { iconPath: './folder-open.svg' };
setiTheme.iconDefinitions['_point_root_folder'] = { iconPath: './root-folder.svg' };
setiTheme.iconDefinitions['_point_root_folder_open'] = { iconPath: './root-folder-open.svg' };
setiTheme.folder = '_point_folder';
setiTheme.folderExpanded = '_point_folder_open';
setiTheme.rootFolder = '_point_root_folder';
setiTheme.rootFolderExpanded = '_point_root_folder_open';
setiTheme.hidesExplorerArrows = false;
writeJson(setiThemePath, setiTheme);

// ════ Сравнение в духе JetBrains ════════════════════════════════════════════
// Просмотрщик различий JetBrains держит номера обеих версий рядом в середине,
// закрашивает промежуток цветом правки и ставит над сравнением строку
// управления. У Code-OSS номера стоят по внешним краям, промежуток пуст, а
// настройки сравнения разбросаны по палитре. Два своих слоя лежат отдельными
// файлами; апстрим правится в трёх местах: признак «полноразмерное сравнение»,
// снятые колонки номеров у обоих редакторов и раскладка виджета, которая
// выделяет слоям место.
const diffEditorRootDir = path.join(sourceRoot, 'src', 'vs', 'editor', 'browser', 'widget', 'diffEditor');
for (const [resource, target] of [
  ['point-diff-center.ts.txt', path.join('features', 'pointDiffCenterFeature.ts')],
  ['point-diff-panel.ts.txt', path.join('features', 'pointDiffPanelFeature.ts')],
]) {
  const featureSource = path.join(resourceSource, resource);
  if (!fs.existsSync(featureSource)) fail(`Point diff feature is missing: ${featureSource}`);
  fs.writeFileSync(path.join(diffEditorRootDir, target), fs.readFileSync(featureSource, 'utf8'));
}

const diffOptionsPath = path.join(diffEditorRootDir, 'diffEditorOptions.ts');
replaceOnce(
  diffOptionsPath,
  `		this.compactMode = derived(this, reader => this._options.read(reader).compactMode);`,
  `		this.compactMode = derived(this, reader => this._options.read(reader).compactMode);
		// Point: середина сравнения и панель над ним — только у полноразмерного
		// сравнения двумя колонками. Во встроенных (заглядывание, чат,
		// многофайловое) они съели бы те несколько строк, ради которых их и
		// открывают.
		// Линейка правок — надёжный признак полноразмерного сравнения: её
		// гасят все встроенные (заглядывание, чат, тетради, многофайловое,
		// быстрый diff), а признака «встроенное» половина из них не ставит.
		this.pointCenterEnabled = derived(this, reader => this.renderSideBySide.read(reader)
			&& this.renderOverviewRuler.read(reader)
			&& !this.compactMode.read(reader)
			&& !this.isInEmbeddedEditor.read(reader));`,
  'Point diff center flag',
);
replaceOnce(
  diffOptionsPath,
  `	public readonly compactMode;
	private readonly trueInlineDiffRenderingEnabled: IObservable<boolean>;`,
  `	public readonly compactMode;
	public readonly pointCenterEnabled;
	private readonly trueInlineDiffRenderingEnabled: IObservable<boolean>;`,
  'Point diff center flag declaration',
);

// Номера строк рисует середина, поэтому своя колонка снимается у обоих
// редакторов сразу — общее место для обеих сторон здесь одно.
replaceOnce(
  path.join(diffEditorRootDir, 'components', 'diffEditorEditors.ts'),
  `		} else {
			clonedOptions.stickyScroll = this._options.editorOptions.get().stickyScroll;
		}
		return clonedOptions;`,
  `		} else {
			clonedOptions.stickyScroll = this._options.editorOptions.get().stickyScroll;
		}
		// Point: номера строк обеих версий рисует середина сравнения. Свою
		// колонку каждому редактору тут выключают — иначе номера стояли бы в
		// трёх местах сразу, а середина ради этого и заводилась.
		if (this._options.pointCenterEnabled.get()) {
			clonedOptions.lineNumbers = 'off';
		}
		return clonedOptions;`,
  'Point diff line numbers',
);

const diffWidgetPath = path.join(diffEditorRootDir, 'diffEditorWidget.ts');
replaceOnce(
  diffWidgetPath,
  `import { DiffEditorGutter } from './features/gutterFeature.js';`,
  `import { DiffEditorGutter } from './features/gutterFeature.js';
import { PointDiffCenterFeature } from './features/pointDiffCenterFeature.js';
import { PointDiffPanelFeature } from './features/pointDiffPanelFeature.js';`,
  'Point diff feature imports',
);
replaceOnce(
  diffWidgetPath,
  `	private readonly _gutter: IObservable<DiffEditorGutter | undefined>;`,
  `	private readonly _gutter: IObservable<DiffEditorGutter | undefined>;

	/** Point: середина сравнения (перемычки и парные номера) и панель над ним. */
	private readonly _pointCenter: IObservable<PointDiffCenterFeature | undefined>;
	private readonly _pointPanel: IObservable<PointDiffPanelFeature | undefined>;
	private readonly _pointHost: HTMLElement;`,
  'Point diff feature fields',
);
// Панель стоит СНАРУЖИ корня сравнения. Внутри пришлось бы двигать сверху и
// редакторы, и жёлоб действий, и линейку правок — каждого своей заплатой;
// обёртка отдаёт корню остаток высоты, и дальше всё считается как раньше.
replaceOnce(
  diffWidgetPath,
  `		this._domElement.appendChild(this.elements.root);
		this._register(toDisposable(() => this.elements.root.remove()));`,
  `		this._pointHost = h('div.point-diff-host', { style: { position: 'relative', width: '100%', height: '100%' } }, []).root;
		this._pointHost.appendChild(this.elements.root);
		this._domElement.appendChild(this._pointHost);
		this._register(toDisposable(() => this._pointHost.remove()));`,
  'Point diff host',
);
replaceOnce(
  diffWidgetPath,
  `		this._register(recomputeInitiallyAndOnChange(this._layoutInfo));`,
  `		this._pointCenter = derivedDisposable(this, reader => this._options.pointCenterEnabled.read(reader)
			? new (readHotReloadableExport(PointDiffCenterFeature, reader))(
				this.elements.root,
				this._diffModel,
				this._editors,
				this._options,
			)
			: undefined);
		this._pointPanel = derivedDisposable(this, reader => this._options.pointCenterEnabled.read(reader)
			? this._instantiationService.createInstance(
				readHotReloadableExport(PointDiffPanelFeature, reader),
				this._pointHost,
				this.elements.root,
				this._diffModel,
				this._options,
			)
			: undefined);

		this._register(recomputeInitiallyAndOnChange(this._layoutInfo));`,
  'Point diff features',
);
// Высота: при своей раскладке наблюдатель мерит уже уменьшенный корень, при
// заданной снаружи — полную высоту части, и вычесть панель нужно самим.
replaceOnce(
  diffWidgetPath,
  `			const fullWidth = this._rootSizeObserver.width.read(reader);
			const fullHeight = this._rootSizeObserver.height.read(reader);

			if (this._rootSizeObserver.automaticLayout) {
				this.elements.root.style.height = '100%';
			} else {
				this.elements.root.style.height = fullHeight + 'px';
			}`,
  `			const fullWidth = this._rootSizeObserver.width.read(reader);
			const pointPanelHeight = this._pointPanel.read(reader)?.height.read(reader) ?? 0;
			const fullHeight = this._rootSizeObserver.height.read(reader)
				- (this._rootSizeObserver.automaticLayout ? 0 : pointPanelHeight);

			if (this._rootSizeObserver.automaticLayout) {
				this.elements.root.style.height = pointPanelHeight > 0 ? \`calc(100% - \${pointPanelHeight}px)\` : '100%';
			} else {
				this.elements.root.style.height = fullHeight + 'px';
			}`,
  'Point diff panel height',
);
// Ширина: середина встаёт между жёлобом действий и правой колонкой, а перемычки
// рисуются по всему промежутку сразу — поэтому ей передаётся и левый край
// жёлоба, и полная ширина промежутка.
replaceOnce(
  diffWidgetPath,
  `			const gutter = this._gutter.read(reader);
			const gutterWidth = gutter?.width.read(reader) ?? 0;`,
  `			const gutter = this._gutter.read(reader);
			const gutterWidth = gutter?.width.read(reader) ?? 0;
			const pointCenter = this._pointCenter.read(reader);
			const pointCenterWidth = pointCenter?.width.read(reader) ?? 0;`,
  'Point diff center width',
);
replaceOnce(
  diffWidgetPath,
  `				originalLeft = 0;
				originalWidth = sashLeft - gutterWidth - movedBlocksLinesWidth;

				gutterLeft = sashLeft - gutterWidth;`,
  `				originalLeft = 0;
				originalWidth = sashLeft - gutterWidth - pointCenterWidth - movedBlocksLinesWidth;

				gutterLeft = sashLeft - gutterWidth - pointCenterWidth;`,
  'Point diff center reservation',
);
replaceOnce(
  diffWidgetPath,
  `			gutter?.layout(gutterLeft);`,
  `			gutter?.layout(gutterLeft);
			pointCenter?.layout(gutterLeft, gutterWidth + pointCenterWidth);`,
  'Point diff center layout',
);

// Native tool-window controls share the titlebar popup and layout services.
fs.copyFileSync(
  path.join(resourceSource, 'point-tool-windows.ts.txt'),
  path.join(sourceRoot, 'src/vs/workbench/browser/parts/titlebar/pointToolWindows.ts'),
);
replaceOnce(titlebarPath,
  `import { POINT_BRANCH_CHIP_KEYS, showPointProjectMenu, showPointRunMenu, updatePointBranchChip } from './pointTitleWidgets.js';`,
  `import { POINT_BRANCH_CHIP_KEYS, showPointProjectMenu, showPointRunMenu, updatePointBranchChip } from './pointTitleWidgets.js';\nimport './pointToolWindows.js';`,
  'Point tool windows contribution');
const pointWindowActions = `
\t\t\taddPointTitleAction(pointRunActions, 'point.toolWindows', 'codicon codicon-layout', localize('point.toolWindows', "Окна и расположение"), '.point-title-action-windows');
\t\t\taddPointTitleAction(pointRunActions, 'point.keymap', 'codicon codicon-keyboard', localize('point.keymap', "Горячие клавиши"), '.point-title-action-keymap');`;
replaceOnce(titlebarPath,
  `\t\t\taddPointTitleAction(pointRunActions, 'localAgent.runWithoutDebug', 'codicon codicon-play', localize('point.run', "Запустить проект"), '.point-title-action-run');`,
  `\t\t\taddPointTitleAction(pointRunActions, 'localAgent.runWithoutDebug', 'codicon codicon-play', localize('point.run', "Запустить проект"), '.point-title-action-run');${pointWindowActions}`,
  'Point windows and keymap toolbar actions');


// Point: ярлыки окон нижней панели живут в рейке. Панель можно закрыть, а
// окна обязаны остаться на виду — иначе о них напоминает только память на
// Alt+N. Полоса читает контейнеры панели по публичным службам и потому не
// расходится ни с расширением, ни с апстримом.
fs.copyFileSync(
  path.join(resourceSource, 'point-panel-strip.ts.txt'),
  path.join(sourceRoot, 'src/vs/workbench/browser/parts/activitybar/pointPanelStrip.ts'),
);
replaceOnce(
  activitybarPath,
  `import { IViewDescriptorService, ViewContainerLocation, ViewContainerLocationToString } from '../../../common/views.js';`,
  `import { IViewDescriptorService, ViewContainerLocation, ViewContainerLocationToString } from '../../../common/views.js';
import { PointPanelStrip } from './pointPanelStrip.js';`,
  'Point panel strip import',
);
replaceOnce(
  activitybarPath,
  `	private content: HTMLElement | undefined;
	private _isCompact: boolean;`,
  `	private content: HTMLElement | undefined;
	private _isCompact: boolean;
	private pointPanelStrip: PointPanelStrip | undefined;`,
  'Point panel strip field',
);
replaceOnce(
  activitybarPath,
  `	getPinnedPaneCompositeIds(): string[] {`,
  `	// Полоса создаётся один раз, но возвращается в DOM после каждой очистки
	// содержимого рейки: смена размера значков и скрытие рейки чистят узел
	// целиком, а окна нижней панели обязаны оставаться на виду.
	private ensurePointPanelStrip(): void {
		if (this.productService.applicationName !== 'point' || this.location !== ViewContainerLocation.Sidebar || !this.content) {
			return;
		}
		if (!this.pointPanelStrip) {
			this.pointPanelStrip = this._register(this.instantiationService.createInstance(PointPanelStrip));
		}
		if (this.pointPanelStrip.element.parentElement !== this.content) {
			this.content.appendChild(this.pointPanelStrip.element);
		}
	}

	getPinnedPaneCompositeIds(): string[] {`,
  'Point panel strip attach helper',
);
replaceOnce(
  activitybarPath,
  `		this.content = append(this.element, $('.content'));

		this.updateCompactStyle();`,
  `		this.content = append(this.element, $('.content'));
		this.ensurePointPanelStrip();

		this.updateCompactStyle();`,
  'Point panel strip in content area',
);
replaceOnce(
  activitybarPath,
  `		this.compositeBar.clear();
		clearNode(this.content);
		this.compositeBar.value = this.createCompositeBar();
		this.compositeBar.value.create(this.content);`,
  `		this.compositeBar.clear();
		clearNode(this.content);
		this.compositeBar.value = this.createCompositeBar();
		this.compositeBar.value.create(this.content);
		this.ensurePointPanelStrip();`,
  'Point panel strip after composite bar rebuild',
);
replaceOnce(
  activitybarPath,
  `			this.compositeBar.value = this.createCompositeBar();
			this.compositeBar.value.create(this.content);

			if (this.dimension) {`,
  `			this.compositeBar.value = this.createCompositeBar();
			this.compositeBar.value.create(this.content);
			this.ensurePointPanelStrip();

			if (this.dimension) {`,
  'Point panel strip after composite bar show',
);
replaceOnce(
  activitybarPath,
  `		this.compositeBar.value.layout(width, contentAreaSize.height);`,
  `		this.compositeBar.value.layout(width, contentAreaSize.height - (this.pointPanelStrip?.height ?? 0));`,
  'Point panel strip height reservation',
);


// Point: ряд вкладок в шапке нижней панели не нужен. Полный список окон живёт
// в рейке (полоса ярлыков), и повторять его здесь значило бы завести второе
// место для одного выбора — ту же ошибку, что была у терминала. Подпись
// активного окна Code-OSS вернёт сам: он гасит её только тогда, когда ряд
// вкладок стоит в шапке.
const panelPartPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'panel', 'panelPart.ts');
replaceOnce(
  panelPartPath,
  `import { Extensions } from '../../panecomposite.js';`,
  `import { Extensions } from '../../panecomposite.js';\nimport product from '../../../../platform/product/common/product.js';`,
  'Point panel part product import',
);
replaceOnce(
  panelPartPath,
  `\tprotected override shouldShowCompositeBar(): boolean {\n\t\treturn true;\n\t}`,
  `\tprotected override shouldShowCompositeBar(): boolean {\n\t\treturn product.applicationName !== 'point';\n\t}`,
  'Point panel composite bar off',
);

// Point: отладчик — нижнее окно, как в JetBrains. Стек, переменные, наблюдения
// и точки останова встают в ряд по горизонтали, а не колонкой в боковой
// панели, и Alt+5 открывает низ, как соседний Alt+4.
const debugContributionPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'contrib', 'debug', 'browser', 'debug.contribution.ts');
replaceOnce(
  debugContributionPath,
  `import { IViewContainersRegistry, IViewsRegistry, ViewContainer, ViewContainerLocation, Extensions as ViewExtensions } from '../../../common/views.js';`,
  `import { IViewContainersRegistry, IViewsRegistry, ViewContainer, ViewContainerLocation, Extensions as ViewExtensions } from '../../../common/views.js';\nimport product from '../../../../platform/product/common/product.js';`,
  'Point debug contribution product import',
);
replaceOnce(
  debugContributionPath,
  `\ttitle: nls.localize2('run and debug', "Run and Debug"),`,
  `\ttitle: product.applicationName === 'point' ? nls.localize2('point.debugWindow', "Отладка") : nls.localize2('run and debug', "Run and Debug"),`,
  'Point debug window title',
);
replaceOnce(
  debugContributionPath,
  `\tctorDescriptor: new SyncDescriptor(DebugViewPaneContainer),\n\ticon: icons.runViewIcon,\n\talwaysUseContainerInfo: true,\n\torder: 3,\n}, ViewContainerLocation.Sidebar);`,
  `\tctorDescriptor: new SyncDescriptor(DebugViewPaneContainer),\n\ticon: icons.runViewIcon,\n\talwaysUseContainerInfo: true,\n\torder: 3,\n}, product.applicationName === 'point' ? ViewContainerLocation.Panel : ViewContainerLocation.Sidebar);`,
  'Point debug view container in panel',
);
// Консоль отладки переезжает внутрь окна отладки, а не стоит рядом отдельной
// вкладкой: в JetBrains это вкладка того же окна. Перенос делается после обеих
// регистраций — переставлять их порядок в чужом модуле дороже, чем передвинуть
// готовую вьюшку. Пустой контейнер `workbench.panel.repl` спрячется сам:
// он объявлен `hideIfEmpty`.
replaceOnce(
  debugContributionPath,
  `\n// Register disassembly view\n`,
  `\nif (product.applicationName === 'point') {\n\tconst replView = viewsRegistry.getView(REPL_VIEW_ID);\n\tif (replView) {\n\t\tviewsRegistry.moveViews([replView], viewContainer);\n\t}\n}\n\n// Register disassembly view\n`,
  'Point debug console inside debug window',
);

// Point: Структура — своё окно в боковой панели, а не вьюшка внутри Проекта.
// Два дерева в одной панели спорят за высоту, и символы файла тонут под
// деревом папок; в JetBrains у Структуры свой значок в рейке и свой Alt+7.
// Регистрация Outline выше для Point выключена — вьюшка регистрируется здесь,
// в собственный контейнер, и `outline.focus` вместе с ней оживает.
replaceOnce(
  outlineContributionPath,
  `import { IViewsRegistry, Extensions as ViewExtensions } from '../../../common/views.js';`,
  `import { IViewContainersRegistry, IViewsRegistry, ViewContainerLocation, Extensions as ViewExtensions } from '../../../common/views.js';\nimport { ViewPaneContainer } from '../../../browser/parts/views/viewPaneContainer.js';`,
  'Point structure container imports',
);
replaceOnce(
  outlineContributionPath,
  `\t\t'outline.showTypeParameters': {\n\t\t\ttype: 'boolean',\n\t\t\tdefault: true,\n\t\t\tscope: ConfigurationScope.LANGUAGE_OVERRIDABLE,\n\t\t\tmarkdownDescription: localize('filteredTypes.typeParameter', "When enabled, Outline shows \`typeParameter\`-symbols.")\n\t\t}\n\t}\n});`,
  `\t\t'outline.showTypeParameters': {\n\t\t\ttype: 'boolean',\n\t\t\tdefault: true,\n\t\t\tscope: ConfigurationScope.LANGUAGE_OVERRIDABLE,\n\t\t\tmarkdownDescription: localize('filteredTypes.typeParameter', "When enabled, Outline shows \`typeParameter\`-symbols.")\n\t\t}\n\t}\n});\n\n// --- Point: окно «Структура»\n\nif (product.applicationName === 'point') {\n\tconst pointStructureContainer = Registry.as<IViewContainersRegistry>(ViewExtensions.ViewContainersRegistry).registerViewContainer({\n\t\tid: 'point.structure',\n\t\ttitle: localize2('point.structure', "Структура"),\n\t\topenCommandActionDescriptor: { id: 'point.structure', order: 5 },\n\t\tctorDescriptor: new SyncDescriptor(ViewPaneContainer, ['point.structure', { mergeViewWithContainerWhenSingleView: true }]),\n\t\ticon: outlineViewIcon,\n\t\talwaysUseContainerInfo: true,\n\t\torder: 5,\n\t}, ViewContainerLocation.Sidebar);\n\tRegistry.as<IViewsRegistry>(ViewExtensions.ViewsRegistry).registerViews([{\n\t\tid: IOutlinePane.Id,\n\t\tname: localize2('point.structureView', "Структура"),\n\t\tcontainerIcon: outlineViewIcon,\n\t\tctorDescriptor: new SyncDescriptor(OutlinePane),\n\t\tcanToggleVisibility: false,\n\t\tcanMoveView: true,\n\t\torder: 1,\n\t\tweight: 100,\n\t\tfocusCommand: { id: 'outline.focus' }\n\t}], pointStructureContainer);\n}`,
  'Point structure view container',
);
const marker = {
  product: 'Point IDE',
  version: upstreamPackage.version,
  upstreamCommit: version.commit,
  extension: `${extensionPackage.publisher}.${extensionPackage.name}@${extensionPackage.version}`,
  languagePack: `MS-CEINTL.vscode-language-pack-ru@${version.russianLanguagePack.version}`,
  generatedAt: new Date().toISOString()
};
writeJson(path.join(sourceRoot, '.local-agent-overlay.json'), marker);
console.log(JSON.stringify(marker, null, 2));
