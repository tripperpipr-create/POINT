import path from 'node:path';

// Первый кадр окон Point: что видит человек до того, как появится Чертог.
//
// Вынесено из apply-overlay.mjs одним связным куском: фон окна до отрисовки
// верстака, отказ от скелета верстака IDE, цвета первого кадра и раскладка
// sessions-окна, в котором живёт Хаб. Заплаты те же — replaceOnce/replaceAny
// приходят из apply-overlay.mjs, чтобы проверка «уже наложено» была одна.
export function applyFirstFramePatches(sourceRoot, { replaceOnce, replaceAny }) {
  // Первое мгновение окна принадлежало Code-OSS, а не Point.
  //
  // До того как отрисуется верстак, окно красится двумя путями. Главный процесс
  // отдаёт Electron цвет фона из themeMainService: на первом запуске сохранённого
  // значения нет, и берётся умолчание VS Code #1F1F1F — светлее фона Point
  // (#0A0A0A), поэтому первый кадр вспыхивал и темнел.
  const themeMainServicePath = path.join(sourceRoot, 'src', 'vs', 'platform', 'theme', 'electron-main', 'themeMainServiceImpl.ts');
  replaceAny(
    themeMainServicePath,
    // Вторая форма — наложенная заплата до перехода на #0A0A0A: дерево
    // `.cache/code-oss` живёт между сборками и уже несёт прежний цвет.
    ["const DEFAULT_BG_DARK = '#1F1F1F';", "const DEFAULT_BG_DARK = '#171717';"],
    "const DEFAULT_BG_DARK = '#0A0A0A';",
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
        "\t\tlet shellBackground = '#0A0A0A';",
        "\t\tlet shellForeground = '#e9e9e9';",
        '\t\tif (data) {',
        '\t\t\tbaseTheme = data.baseTheme;',
        '\t\t\tshellBackground = data.colorInfo.editorBackground;',
        '\t\t\tshellForeground = data.colorInfo.foreground;',
      ].join('\n'),
      // Наложенная заплата до перехода на #0A0A0A (см. DEFAULT_BG_DARK выше).
      [
        "\t\tlet baseTheme = 'vs-dark';",
        "\t\tlet shellBackground = '#171717';",
        "\t\tlet shellForeground = '#e9e9e9';",
        '\t\tif (data) {',
        '\t\t\tbaseTheme = data.baseTheme;',
        '\t\t\tshellBackground = data.colorInfo.editorBackground ?? shellBackground;',
        '\t\t\tshellForeground = data.colorInfo.foreground ?? shellForeground;',
      ].join('\n'),
    ],
    [
      "\t\tlet baseTheme = 'vs-dark';",
      "\t\tlet shellBackground = '#0A0A0A';",
      "\t\tlet shellForeground = '#e9e9e9';",
      '\t\tif (data) {',
      '\t\t\tbaseTheme = data.baseTheme;',
      '\t\t\tshellBackground = data.colorInfo.editorBackground ?? shellBackground;',
      '\t\t\tshellForeground = data.colorInfo.foreground ?? shellForeground;',
    ].join('\n'),
    'Point colours for the first frame without stored theme',
  );

  // Первый кадр окна Чертога — уже Чертог, а не пустое окно агентов Code-OSS.
  //
  // Сетку sessions-окна верстак строит до всех вкладов. По умолчанию в ней видны
  // список сессий («New», «Customizations», «Agents», «Skills», «MCP Servers»),
  // карточка сессии и правая панель; редактор, где живёт Хаб, скрыт. Прятала их
  // только `preparePointHubLayout` в фазе Eventually — через 2,5–5 с после
  // восстановления окна. Её ранний вызов в BlockStartup падает на ещё не
  // созданной сетке и по дороге сохраняет видимость `sidebar: true`, а
  // сохранённое значение на следующем старте перебивает умолчание. Поэтому
  // каждый запуск начинался каркасом чужого приложения.
  //
  // Теперь у Point раскладка Чертога — это умолчание sessions-окна при любом
  // классе окна, а сохранённая видимость частей не читается: окно сессий у Point
  // одно, и принадлежит оно Хабу. Сетка создаётся сразу с одним редактором,
  // ранний вызов `preparePointHubLayout` ничего не меняет и больше не падает.
  //
  // Третья заплата снимает инвариант «редактор не бывает без правой панели»:
  // контроллер раскладки проверяет его в AfterRestored и сам открыл бы правую
  // панель рядом с Хабом — ровно до фазы Eventually. Водяной знак пустой группы
  // до прихода Хаба убирает point-agents-window.css.
  const sessionsLayoutPolicyPath = path.join(sourceRoot, 'src', 'vs', 'sessions', 'browser', 'layoutPolicy.ts');
  replaceOnce(
    sessionsLayoutPolicyPath,
    `import { Gesture } from '../../base/browser/touch.js';`,
    `import { Gesture } from '../../base/browser/touch.js';
import product from '../../platform/product/common/product.js';`,
    'Point sessions layout policy product import',
  );
  replaceOnce(
    sessionsLayoutPolicyPath,
    [
      '\tgetPartVisibilityDefaults(viewportClass?: ViewportClass): IPartVisibilityDefaults {',
      '\t\tconst vc = viewportClass ?? this._viewportClass.get();',
    ].join('\n'),
    [
      '\tgetPartVisibilityDefaults(viewportClass?: ViewportClass): IPartVisibilityDefaults {',
      "\t\tif (product.applicationName === 'point') {",
      '\t\t\treturn { sidebar: false, auxiliaryBar: false, panel: false, sessions: false, editor: true };',
      '\t\t}',
      '\t\tconst vc = viewportClass ?? this._viewportClass.get();',
    ].join('\n'),
    'Point Hub layout as the sessions window default',
  );
  const sessionsWorkbenchPath = path.join(sourceRoot, 'src', 'vs', 'sessions', 'browser', 'workbench.ts');
  replaceOnce(
    sessionsWorkbenchPath,
    `import { Registry } from '../../platform/registry/common/platform.js';`,
    `import { Registry } from '../../platform/registry/common/platform.js';
import product from '../../platform/product/common/product.js';`,
    'Point sessions workbench product import',
  );
  replaceOnce(
    sessionsWorkbenchPath,
    [
      '\tprivate _loadPartVisibility(storageService: IStorageService): { editor?: boolean; auxiliaryBar?: boolean; sidebar?: boolean } {',
      "\t\tif (this.layoutPolicy.viewportClass.get() === 'phone') {",
    ].join('\n'),
    [
      '\tprivate _loadPartVisibility(storageService: IStorageService): { editor?: boolean; auxiliaryBar?: boolean; sidebar?: boolean } {',
      "\t\tif (this.layoutPolicy.viewportClass.get() === 'phone' || product.applicationName === 'point') {",
    ].join('\n'),
    'Point Hub ignores saved sessions part visibility',
  );
  const sessionLayoutControllerPath = path.join(sourceRoot, 'src', 'vs', 'sessions', 'contrib', 'layout', 'browser', 'sessionLayoutController.ts');
  replaceOnce(
    sessionLayoutControllerPath,
    `import { observableConfigValue } from '../../../../platform/observable/common/platformObservableUtils.js';`,
    `import { observableConfigValue } from '../../../../platform/observable/common/platformObservableUtils.js';
import product from '../../../../platform/product/common/product.js';`,
    'Point session layout controller product import',
  );
  replaceOnce(
    sessionLayoutControllerPath,
    [
      '\tprivate _enforceAuxiliaryBarWhenEditorVisible(): void {',
      '\t\tif (this._suppressAuxiliaryBarEnforcement) {',
    ].join('\n'),
    [
      '\tprivate _enforceAuxiliaryBarWhenEditorVisible(): void {',
      "\t\tif (this._suppressAuxiliaryBarEnforcement || product.applicationName === 'point') {",
    ].join('\n'),
    'Point Hub editor without the auxiliary bar',
  );
}
