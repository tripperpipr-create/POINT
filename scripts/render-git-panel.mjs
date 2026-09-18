// Стенд Git-панели: разметку рисует настоящий ui/client/git-views.js.
//
// Панель живёт в вебвью, а вебвью — чужая цель отладчика: чтобы посмотреть на
// дерево изменений, приходилось поднимать окно Point и ходить туда зондом
// (scripts/probe-point-git-panel.mjs). Для правки оформления это дорого, а
// главное — не воспроизводимо: дерево зависит от того, что сейчас в рабочей
// копии. Здесь снимок задан фикстурой: восемнадцать файлов, пять уровней папок,
// конфликт и файл вне репозитория — то же, что было на снимке владельца.
//
// Каждая ширина — отдельный <iframe>, а не <div>: медиазапросы панели считают
// вьюпорт, и в блоке узкие правила не срабатывают вовсе.
//
//   node scripts/render-git-panel.mjs build/preview/git-panel.html
//   cd build/preview && python -m http.server 8791 --bind 127.0.0.1
import { writeFileSync, readFileSync, mkdirSync, readdirSync, copyFileSync } from 'node:fs'
import { basename, dirname, join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const extension = join(here, '..', 'vscode-extension')
const { createGitViews } = await import(pathToFileURL(join(extension, 'ui', 'client', 'git-views.js')))

const out = resolve(process.argv[2] || join(here, '..', 'build', 'preview', 'git-panel.html'))
const widths = (process.argv[3] || '300,420').split(',').map(value => Number(value.trim())).filter(Boolean)

const paths = [
  'cf-bitrix/source/bitrix/templates/centrofinans/assets/app.css',
  'cf-bitrix/source/bitrix/templates/centrofinans/components/bitrix/news/jobs/bitrix/news.list/vacancy/template.php',
  'cf-bitrix/source/bitrix/templates/centrofinans/img/credit-line-zero/banner.svg',
  'cf-bitrix/source/bitrix/templates/centrofinans/js/calc.js',
  'cf-bitrix/source/bitrix/templates/centrofinans/js/forms.js',
  'cf-bitrix/source/bitrix/templates/centrofinans/js/geo.js',
  'cf-bitrix/source/bitrix/templates/centrofinans/js/main.js',
  'cf-bitrix/source/bitrix/templates/centrofinans/js/map.js',
  'cf-bitrix/source/bitrix/templates/centrofinans/js/offices.js',
  'cf-bitrix/source/cron/soap.exchange.update_geolocation.php',
  'cf-bitrix/source/edinaya-biometricheskaya-sistema/how-to.mp4',
  'cf-bitrix/source/edinaya-biometricheskaya-sistema/index.php',
  'cf-bitrix/source/local/components/centrofinans/calculator.carmone/templates/pts/result_modifier.php',
  'cf-bitrix/source/local/components/wondarbase/calculator/templates/pts/result_modifier.php',
  'cf-bitrix/source/local/components/wondarbase/calculator/templates/pts/template.php',
  'cf-bitrix/source/local/components/wondarbase/calculator/class.php',
  'cf-bitrix/source/local/php_interface/init.php',
  'cf-bitrix/source/upload/README.md',
  'cf-bitrix/docker-compose.yml',
]
// Один файл переименован: именно на такой строке прежний путь съедал имя
// файла целиком — на снимке владельца от него оставалось «clas…».
const renamed = 15
const changes = paths.map((path, index) => ({
  path,
  list: 'default',
  area: index === 10 ? 'untracked' : index === 3 ? 'conflict' : 'working',
  status: index === renamed ? 3 : index % 7 === 0 ? 7 : 5,
  originalPath: index === renamed ? 'cf-bitrix/source/local/components/wondarbase/calculator/calculator.php' : '',
  add: (index * 7) % 40,
  del: (index * 3) % 12,
}))
const data = {
  loaded: true, available: true, branch: 'feature/credit-line', remote: 'origin/feature/credit-line',
  ahead: 2, behind: 1, changes,
  changeLists: [{ id: 'default', name: 'Изменения по умолчанию', active: true }],
  commits: [{ hash: 'abc12345', shortHash: 'abc12345', message: 'Калькулятор ПТС', author: 'Point', date: '2026-08-31T10:00:00Z' }],
  repositories: [{ root: 'C:/cf-bitrix', name: 'cf-bitrix', selected: true }],
}
const makeUi = (extra = {}) => ({
  tab: 'changes', checked: new Set(), known: new Set(), collapsed: new Set(), selected: paths[4],
  flat: false, menuFor: '', commitDraft: '', amend: false, pendingAction: '', notice: null, target: '',
  foldedOnce: false, ...extra,
})
const esc = value => String(value).replace(/[&<>"']/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[ch])
// Склонение по числу — то же правило, что в вебвью (ui/plural.mjs следит, чтобы
// подстановки не расходились).
const countOf = (count, one, few, many) => {
  const tail = count % 100
  const last = count % 10
  if (tail >= 11 && tail <= 14) return `${count} ${many}`
  if (last === 1) return `${count} ${one}`
  if (last >= 2 && last <= 4) return `${count} ${few}`
  return `${count} ${many}`
}
const render = (ui, only) => {
  const shown = only || changes
  const views = createGitViews({
    ui,
    getData: () => (only ? { ...data, changes: only } : data),
    gitWide: () => false,
    shell: html => html,
    esc,
    countOf,
    toolCommandButton: () => '',
    toolWindowHeading: () => '',
    persistDraft: () => {},
    root: { querySelector: () => null, querySelectorAll: () => [] },
  })
  views.syncGitChecked(shown)
  return views.gitToolView()
}

// Стиль подключается файлом, а страница — с тем же CSP, что и настоящее вебвью
// (`html()` в extension.js): `style-src` без `'unsafe-inline'`. Значит и
// `<style>` в разметке, и атрибут `style=` здесь так же мертвы, как в продукте.
// Раньше стенд вклеивал стили тегом `<style>` и никакого CSP не ставил — и
// показывал дерево с отступами, которых в панели не было вовсе: отступ считался
// инлайновой переменной `--nc-depth`, а вебвью её выбрасывал. Стенд обязан
// врать в ту же сторону, что и продукт, иначе он не стенд.
const CSP = `default-src 'none'; img-src 'self' data:; style-src 'self'; font-src 'self'`
const stylesheet = basename(out).replace(/\.html$/, '-frame.css')
mkdirSync(dirname(out), { recursive: true })
// Шрифт стенд берёт тот же, что панель: метрики Inter и запасного шрифта
// расходятся, а спорят здесь как раз о пикселях. `font-src 'self'` пускает
// только свой источник — значит файлы должны лежать рядом со стендом.
mkdirSync(join(dirname(out), 'fonts'), { recursive: true })
for (const font of readdirSync(join(extension, 'media', 'fonts')).filter(name => name.endsWith('.woff2'))) {
  copyFileSync(join(extension, 'media', 'fonts', font), join(dirname(out), 'fonts', font))
}
writeFileSync(join(dirname(out), stylesheet), `${['rpg-tokens.css', 'style.css']
  .map(file => readFileSync(join(extension, 'media', file), 'utf8')).join('\n')}
html, body { height: 100%; margin: 0; overflow: hidden; }
body { display: flex; }
body > * { flex: 1 1 auto; min-width: 0; }
`, 'utf8')
const frame = (suffix, ui, only) => {
  const name = basename(out).replace(/\.html$/, `${suffix}-frame.html`)
  writeFileSync(join(dirname(out), name), `<!doctype html><html lang="ru"><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${CSP}">
<title>Git-панель</title>
<link rel="stylesheet" href="${stylesheet}">
</head><body>${render(ui, only)}</body></html>`, 'utf8')
  return name
}
// Второй кадр — с открытым меню строки: слой поверх списка руками не поймать,
// а именно он однажды и приехал неоформленным.
const views = [
  ...widths.map(width => ({ width, label: `${width}px`, src: frame('', makeUi()) })),
  { width: 300, label: 'меню строки', src: frame('-menu', makeUi({ menuFor: `file:${paths[13]}` })) },
  // Короткий список с открытым меню: случай со снимка владельца. Список из
  // пяти строк не прокручивается, и меню, обрезанное нижним краем прокрутки,
  // доводить было нечем — `scrollIntoView` в пустоту не двигает. Кадр держит
  // то, что список тянется на всю высоту панели, а меню помещается целиком.
  { width: 300, label: 'короткий список', src: frame('-short', makeUi({ menuFor: `file:${paths[18]}` }), changes.slice(17)) },
  // Меню подвала открывается вверх — вниз ему некуда, там край панели.
  { width: 300, label: 'меню ветки', src: frame('-target', makeUi({ menuFor: 'target' })) },
]
writeFileSync(out, `<!doctype html><html lang="ru"><head><meta charset="utf-8">
<title>Git-панель — стенд</title>
<style>body { background: #1e1f22; color: #8b8b8b; display: flex; font: 12px system-ui; gap: 24px; margin: 0; padding: 16px; }
figure { display: grid; gap: 6px; margin: 0; } iframe { border: 1px solid #000; display: block; }</style>
</head><body>
${views.map(item => `<figure><iframe src="${item.src}" width="${item.width}" height="860" title="Git-панель ${item.label}"></iframe><figcaption>${item.label}</figcaption></figure>`).join('\n')}
</body></html>`, 'utf8')
console.log(`стенд собран: ${out} (${views.map(item => item.label).join(', ')})`)
