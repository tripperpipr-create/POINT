// Раскладывает каждую поверхность Хаба отдельной страницей в build/preview.
//
// Доска (build-hub-board.js) удобна для беглого взгляда, но врёт про
// медиазапросы: коробка 900px живёт внутри окна 1200px, а @media смотрит на
// окно. Отдельная страница на окно нужного размера не врёт — в настоящем
// вебвью окно и есть поверхность. Отсюда же их читает audit-hub-layout.mjs.
//
//   node scripts/build-hub-preview.js
//   cd build/preview && python -m http.server 8791 --bind 127.0.0.1

const fs = require('fs')
const path = require('path')
const { execFileSync } = require('child_process')

// Список общий с audit-hub-layout.mjs. Раньше он был написан в обоих файлах и
// расходился: `hub-companion.html` мерился аудитом, но сборкой не
// перерисовывался — страница оставалась от прежней версии скриптов.
const SURFACES = require('./lib/hub-surfaces.json').surfaces

const repo = path.join(__dirname, '..')
const out = path.join(repo, 'build', 'preview')
fs.mkdirSync(out, { recursive: true })

// Стенд рисует страницы из `media/main.js` и `media/style.css` — из сборки, а
// не из исходников. Если сборка старше исходника, страница показывает прошлое
// состояние продукта, и замер по ней говорит о прошлом. Один раз это уже стоило
// ложного «правка не сработала»: гейт отчитался exit 0, а `media/main.js`
// остался от прежнего прогона, и аудит нашёл только что снятый повтор.
const newestUnder = dir => {
  let newest = 0
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    newest = Math.max(newest, entry.isDirectory() ? newestUnder(full) : fs.statSync(full).mtimeMs)
  }
  return newest
}
const staleBundles = [
  ['media/main.js', 'ui/client'],
  ['media/style.css', 'ui/layers'],
].filter(([bundle, sources]) => {
  const bundlePath = path.join(repo, 'vscode-extension', bundle)
  if (!fs.existsSync(bundlePath)) return true
  return fs.statSync(bundlePath).mtimeMs < newestUnder(path.join(repo, 'vscode-extension', sources))
})
if (staleBundles.length) {
  console.error('сборка старше исходников — страницы показали бы прошлое состояние:')
  for (const [bundle, sources] of staleBundles) console.error(`  ${bundle} старше, чем ${sources}/`)
  console.error('')
  console.error('сначала: cd vscode-extension && npm run build')
  process.exit(1)
}

const failures = []
for (const entry of SURFACES) {
  try {
    const page = execFileSync(process.execPath,
      [path.join(__dirname, 'render-hub-surface.js'), entry.surface, ...(entry.args || [])],
      { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'] })
    fs.writeFileSync(path.join(out, `hub-${entry.page}.html`), page)
  } catch (error) {
    // Падение отрисовки — это чёрный экран вкладки, а не отсутствие файла.
    // Молчать о нём нельзя: страница просто не появится, и замер её пропустит.
    failures.push(`${entry.page}: ${String(error.stderr || error.message).split('\n')[0]}`)
  }
}

console.log(`разложено: ${SURFACES.length - failures.length} из ${SURFACES.length} → build/preview`)
if (failures.length) {
  console.error('\nотрисовка упала:')
  for (const line of failures) console.error('  ' + line)
  process.exitCode = 1
}
