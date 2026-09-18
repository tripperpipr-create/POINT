// Две одинаковые страницы с разным CSS — для сравнения вычисленных стилей.
//
// Замена пиксельных значений на токены шкалы обязана быть тождественной. Довод
// «--s-4 это и есть 8px» верен, но проверяется он не рассуждением, а сравнением
// того, что посчитает браузер.
//
//   node scripts/build-css-diff-pair.js <путь-к-старому-style.css> <out-dir>
//
// Порядок замера важнее самого замера. Первая загрузка страницы систематически
// даёт размеры на 1–2px меньше, чем последующие: сравнение «свежая против
// прогретой» дало 390 ложных расхождений при полностью одинаковом CSS. Дважды.
//
// Как мерить честно:
//   1. загрузить обе страницы по разу — прогреть;
//   2. дождаться document.fonts.ready и небольшой паузы;
//   3. снять два снимка одной страницы и убедиться, что шум нулевой;
//   4. только потом сравнивать страницы между собой.
// Без шага 3 нельзя отличить настоящее расхождение от артефакта загрузки.

const fs = require('fs')
const path = require('path')
const { execFileSync } = require('child_process')

const repo = path.join(__dirname, '..')
const [oldCssPath, outDir] = process.argv.slice(2)
if (!oldCssPath || !outDir) throw new Error('нужны путь к старому CSS и каталог вывода')

const SURFACES = ['overview', 'decisions', 'changesets', 'quests', 'agents', 'teams',
  'skills', 'flows', 'journal', 'connections', 'history', 'memory']

const markup = SURFACES.map(surface => {
  const page = execFileSync(process.execPath,
    [path.join(__dirname, 'render-hub-surface.js'), surface],
    { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 })
  const body = page.slice(page.indexOf('<div id="root"'), page.lastIndexOf('</body>'))
  return `<div class="pane" data-surface="${surface}" style="width:1100px;height:720px">${body}</div>`
}).join('\n')

const tokens = fs.readFileSync(path.join(repo, 'vscode-extension/media/rpg-tokens.css'), 'utf8')
const write = (name, css) => {
  fs.writeFileSync(path.join(outDir, name), `<!doctype html>
<html lang="ru"><head><meta charset="UTF-8"><title>${name}</title>
<style>${tokens}\n${css}</style>
<style>body { margin: 0 } .pane { overflow: hidden } .pane > .app { height: 100% }</style>
</head><body data-layout="wide">${markup}</body></html>
`, 'utf8')
}

write('css-after.html', fs.readFileSync(path.join(repo, 'vscode-extension/media/style.css'), 'utf8'))
write('css-before.html', fs.readFileSync(oldCssPath, 'utf8'))
console.log('готово: css-before.html и css-after.html с одинаковой разметкой')
