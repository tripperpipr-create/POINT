import path from 'node:path';

// Рейки Point: левая рейка инструментов с её шириной и метриками значков,
// водяной знак пустого редактора и правая панель, которая сворачивается до
// своей рейки, а не прячется вместе с ней. Вкладки инструментов Point не
// пропадают из рейки, когда их вьюшка неактивна.
//
// Вынесено из apply-overlay.mjs одним связным куском; порядок заплат тот же,
// помощники replaceOnce/replaceAny приходят оттуда, чтобы проверка «уже
// наложено» была одна.
export function applyRailPatches(sourceRoot, { replaceOnce, replaceAny, replaceIfPresent }) {
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
}
