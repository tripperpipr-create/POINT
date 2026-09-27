import fs from 'node:fs';
import path from 'node:path';

// Верхняя панель Point по образцу JetBrains: хром sessions-окна над Хабом,
// ряд меню вторым слоем с раскрытием наведением и своим составом меню,
// жетоны проекта и ветки с выпадашками, чип конфигурации запуска, поле
// поиска в командном центре, окно «Поиск везде» и список веток.
//
// Вынесено из apply-overlay.mjs одним связным куском; порядок заплат тот же,
// помощники приходят оттуда, чтобы проверка «уже наложено» была одна.
// Строки внутри многострочных шаблонов — дословный текст заплат, поэтому
// отступ функции их не касается.
export function applyTitlebarPatches(sourceRoot, { fail, replaceOnce, replaceAny, replaceIfPresent, replaceIfPresentAny, removeIfPresent }) {
  const titlebarPath = path.join(sourceRoot, 'src', 'vs', 'workbench', 'browser', 'parts', 'titlebar', 'titlebarPart.ts');
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
}
