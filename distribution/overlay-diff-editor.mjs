import fs from 'node:fs';
import path from 'node:path';

// Сравнение в духе JetBrains: номера обеих версий в середине, панель
// управления над сравнением и раскладка виджета под два своих слоя.
//
// Вынесено из apply-overlay.mjs одним связным куском; порядок заплат тот же.
// Строки внутри многострочных шаблонов — дословный текст заплат, поэтому
// отступ функции их не касается.
export function applyDiffEditorPatches(sourceRoot, { fail, replaceOnce }) {
  const resourceSource = path.join(import.meta.dirname, 'resources');
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
}
