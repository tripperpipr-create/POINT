// Стенд выпадашки ветки: жетон в заголовке и раскрытый список Git.
//
// Выпадашку рисует оболочка, а её пересборка — час. Для правки оформления это
// дорого, и главное — состояние репозитория в живом окне не задать: чтобы
// увидеть «три входящих и конфликт слияния», их надо сперва себе устроить.
// Здесь состояния заданы фикстурами, а стиль берётся настоящий — тот же
// distribution/resources/point-workbench.css, что накладывается на Code-OSS.
//
// Разметка повторяет ту, что собирают
// `distribution/resources/point-title-widgets.ts.txt` (шапка, плитки, действия)
// и `distribution/resources/point-branch-popup.ts.txt` (строки веток). Стенд её
// не выполняет — модули живут внутри Code-OSS и без него не грузятся, — поэтому
// при правке разметки в тех файлах поправить надо и здесь: расхождение стенд
// покажет красиво, а продукт — как есть.
//
//   node scripts/render-branch-popup.mjs build/preview/branch-popup.html
import { writeFileSync, readFileSync, mkdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..')
const out = resolve(process.argv[2] || join(root, 'build', 'preview', 'branch-popup.html'))

const workbenchCss = readFileSync(join(root, 'distribution', 'resources', 'point-workbench.css'), 'utf8')
const codicons = join(root, '.cache', 'code-oss', 'src', 'vs', 'base', 'browser', 'ui', 'codicons', 'codicon')
const codiconCss = readFileSync(join(codicons, 'codicon.css'), 'utf8')
const codiconFont = readFileSync(join(codicons, 'codicon.ttf')).toString('base64')
// Правила глифов Code-OSS собирает на ходу службой тем, и в codicon.css их нет:
// там только @font-face и общее правило. Стенд собирает их сам из той же
// таблицы имён и кодов, по которой их строит продукт.
const library = readFileSync(join(root, '.cache', 'code-oss', 'src', 'vs', 'base', 'common', 'codiconsLibrary.ts'), 'utf8')
const glyphs = [...library.matchAll(/register\('([a-z0-9-]+)', (0x[0-9a-f]+)\)/g)]
	.map(([, name, code]) => `.codicon-${name}::before { content: "\\${Number(code).toString(16)}"; }`)
	.join('\n')

const fonts = ['400', '500', '600'].map(weight => `@font-face {
	font-family: "Inter";
	font-style: normal;
	font-weight: ${weight};
	src: url(data:font/woff2;base64,${readFileSync(join(root, 'distribution', 'resources', 'fonts', `inter-cyrillic-${weight}.woff2`)).toString('base64')}) format("woff2");
}`).join('\n')

const escape = value => String(value).replace(/[&<>"]/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[character])

const icon = (name, extra = '') => `<span class="${extra}codicon codicon-${name}" aria-hidden="true"></span>`

// ── Жетон в заголовке ───────────────────────────────────────────────────────

function chip({ branch, dirty, behind, ahead, upstream }) {
	const marks = [
		dirty > 0 ? '<span class="point-branch-chip-dot"></span>' : '',
		behind > 0 ? `<span class="point-branch-chip-count point-branch-chip-behind">↓${behind}</span>` : '',
		ahead > 0 ? `<span class="point-branch-chip-count point-branch-chip-ahead">↑${ahead}</span>` : '',
		branch && !upstream ? icon('cloud-upload', 'point-branch-chip-unpublished ') : '',
	].join('')
	return `<button class="point-branch-chip" type="button">
		${icon('git-branch')}
		<span class="point-branch-chip-label">${escape(branch)}</span>
		<span class="point-branch-chip-marks" aria-hidden="true">${marks}</span>
		${icon('chevron-down')}
	</button>`
}

// ── Список ──────────────────────────────────────────────────────────────────

const stat = ({ name, value, label, tone }) => `<button class="point-popup-stat point-popup-stat-${tone}" type="button">
	${icon(name, 'point-popup-stat-icon ')}
	<span class="point-popup-stat-value">${escape(value)}</span>
	<span class="point-popup-stat-label">${escape(label)}</span>
</button>`

const headAction = ({ name, label, disabled }) => `<button class="point-popup-head-action${disabled ? ' point-popup-head-action-disabled' : ''}" type="button">
	${icon(name)}
	<span class="point-popup-head-action-label">${escape(label)}</span>
</button>`

const row = ({ name, label, meta, current, submenu, plain }) => `<button class="point-popup-row${current ? ' point-popup-row-current' : ''}" type="button">
	${icon(name, 'point-popup-icon ')}
	<span class="point-popup-label">${escape(label)}</span>
	${meta ? `<span class="point-popup-meta">${escape(meta)}</span>` : ''}
	${current ? icon('check', 'point-popup-check ') : ''}
	${submenu && !plain ? icon('chevron-right', 'point-popup-more ') : ''}
</button>`

const section = (title, count, rows) => `<div class="point-popup-section"><span>${escape(title)}</span>${count === undefined ? '' : `<span class="point-popup-section-count">${count}</span>`}</div>${rows}`

function popup({ head, note, stats, actions, sections }) {
	return `<div class="point-popup point-branch-popup" role="menu">
		<div class="point-popup-head">
			<div class="point-popup-head-line">
				${icon('git-branch', 'point-popup-head-icon ')}
				<span class="point-popup-head-title">${escape(head.title)}</span>
			</div>
			<div class="point-popup-head-sub">${icon(head.tracked ? 'cloud' : 'cloud-upload')}<span class="point-popup-head-sub-text">${escape(head.subtitle)}</span></div>
			${note ? `<div class="point-popup-note point-popup-note-${note.tone}">${icon(note.name)}<span>${escape(note.text)}</span></div>` : ''}
			<div class="point-popup-stats">${stats.map(stat).join('')}</div>
			<div class="point-popup-head-actions">${actions.map(headAction).join('')}</div>
		</div>
		<div class="point-popup-search">
			${icon('search')}
			<input class="point-popup-search-input" type="text" placeholder="Ветка, тег или хеш" spellcheck="false">
		</div>
		<div class="point-popup-list">${sections}</div>
		<div class="point-popup-separator"></div>
		<div class="point-popup-foot">${row({ name: 'add', label: 'Новая ветка…' })}</div>
	</div>`
}

const locals = [
	{ label: 'feature/credit-line', meta: '4f21ab0' },
	{ label: 'main', meta: '9c0d3e7' },
	{ label: 'feature/webhook-retry', meta: '1b77c42' },
	{ label: 'hotfix/geolocation-timeout', meta: 'ae30119' },
	{ label: 'feature/very-long-branch-name-here', meta: '6db2f08' },
]
const remoteRows = [
	row({ name: 'cloud', label: 'origin/main', meta: '9c0d3e7', submenu: true }),
	row({ name: 'cloud', label: 'origin/feature/credit-line', meta: '77b5d90', submenu: true }),
].join('')
const tagRows = row({ name: 'tag', label: 'v1.4.0', meta: '5510cd3', submenu: true })

// Галочка ставится по той же ветке, что стоит в шапке. Стенд, у которого шапка
// говорит одно, а список помечает другое, врёт ровно там, где на него смотрят.
const branches = current => section('Локальные ветки', 12, locals
	.map(item => row({ name: 'git-branch', ...item, current: item.label === current, submenu: true })).join(''))
	+ '<div class="point-popup-separator"></div>'
	+ section('Удалённые', 8, remoteRows)
	+ '<div class="point-popup-separator"></div>'
	+ section('Теги', 3, tagRows)

// Три состояния подряд: обычная работа, чистая копия и незавершённое слияние.
// Порядок такой, потому что средний случай — эталон, а крайние читаются как
// отклонения от него.
const cases = [
	{
		caption: 'есть несохранённое, три входящих и два исходящих',
		chip: { branch: 'feature/credit-line', dirty: 7, behind: 3, ahead: 2, upstream: 'origin/feature/credit-line' },
		popup: {
			head: { title: 'feature/credit-line', subtitle: 'отслеживает origin/feature/credit-line', tracked: true },
			stats: [
				{ name: 'arrow-down', value: '3', label: 'Входящие', tone: 'attention' },
				{ name: 'arrow-up', value: '2', label: 'Исходящие', tone: 'attention' },
				{ name: 'diff', value: '7', label: 'Изменения', tone: 'attention' },
			],
			actions: [
				{ name: 'refresh', label: 'Обновить' },
				{ name: 'arrow-down', label: 'Стянуть' },
				{ name: 'arrow-up', label: 'Отправить' },
				{ name: 'git-commit', label: 'Закоммитить' },
			],
			sections: branches('feature/credit-line'),
		},
	},
	{
		caption: 'всё сохранено и отправлено',
		chip: { branch: 'main', dirty: 0, behind: 0, ahead: 0, upstream: 'origin/main' },
		popup: {
			head: { title: 'main', subtitle: 'отслеживает origin/main', tracked: true },
			stats: [
				{ name: 'arrow-down', value: '0', label: 'Входящие', tone: 'idle' },
				{ name: 'arrow-up', value: '0', label: 'Исходящие', tone: 'idle' },
				{ name: 'diff', value: '0', label: 'Изменения', tone: 'idle' },
			],
			actions: [
				{ name: 'refresh', label: 'Обновить' },
				{ name: 'arrow-down', label: 'Стянуть' },
				{ name: 'arrow-up', label: 'Отправить' },
				{ name: 'git-commit', label: 'Закоммитить', disabled: true },
			],
			sections: branches('main'),
		},
	},
	{
		caption: 'ветка не опубликована, слияние не завершено',
		chip: { branch: 'feature/very-long-branch-name-here', dirty: 4, behind: 0, ahead: 0, upstream: '' },
		popup: {
			head: { title: 'feature/very-long-branch-name-here', subtitle: 'нет ветки-источника — ветку ещё не публиковали', tracked: false },
			note: { tone: 'wound', name: 'warning', text: 'Слияние не завершено: 2 изменения с конфликтами' },
			stats: [
				{ name: 'arrow-down', value: '—', label: 'Входящие', tone: 'idle' },
				{ name: 'arrow-up', value: '—', label: 'Исходящие', tone: 'idle' },
				{ name: 'diff', value: '4', label: 'Изменения', tone: 'attention' },
			],
			actions: [
				{ name: 'refresh', label: 'Обновить' },
				{ name: 'arrow-down', label: 'Стянуть', disabled: true },
				{ name: 'cloud-upload', label: 'Опубликовать' },
				{ name: 'git-commit', label: 'Закоммитить' },
			],
			sections: branches('feature/very-long-branch-name-here'),
		},
	},
]

const html = `<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<title>Point — выпадашка ветки</title>
<style>
${fonts}
@font-face { font-family: "codicon"; src: url(data:font/ttf;base64,${codiconFont}) format("truetype"); }
${codiconCss.replace(/@font-face[\s\S]*?\}/, '')}
${glyphs}
${workbenchCss}
body { background: #0A0A0A; margin: 0; padding: 24px; }
.stand { display: flex; gap: 32px; align-items: flex-start; }
.case { display: flex; flex-direction: column; gap: 12px; }
.caption { color: #7a7a7a; font: 400 12px/1.4 "Inter", sans-serif; max-width: 340px; }
/* Полоса заголовка настоящая по грунту и высоте: жетон меряется на своём фоне. */
.bar { align-items: center; background: #111111; border-bottom: 1px solid #222222; display: flex; height: 36px; padding: 0 8px; }
.point-popup { position: relative; }
</style>
</head>
<body>
<div class="monaco-workbench">
	<div class="point-activitybar" style="display:none"></div>
	<div class="stand">
		${cases.map(item => `<div class="case">
			<div class="caption">${escape(item.caption)}</div>
			<div class="part titlebar"><div class="bar">${chip(item.chip)}</div></div>
			${popup(item.popup)}
		</div>`).join('')}
	</div>
</div>
</body>
</html>
`

mkdirSync(dirname(out), { recursive: true })
writeFileSync(out, html)
console.log(out)
