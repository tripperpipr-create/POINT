// Все поверхности Хаба рядом, каждая в коробке размером с панель редактора.
//
// ВАЖНО: доска врёт про медиазапросы. Коробка 900px живёт внутри окна 1200px, а
// @media смотрит на окно — правила для узкой панели не срабатывают, и доска
// показывает переполнение, которого в настоящем webview нет. Всё, что зависит от
// ширины окна, проверяйте одиночной страницей с окном нужного размера.
//
//   node scripts/build-hub-board.js 1100x720 > build/preview/hub-1100.html

const fs = require('fs')
const path = require('path')
const { execFileSync } = require('child_process')

const SURFACES = ['overview', 'master', 'decisions', 'changesets', 'quests', 'agents',
  'teams', 'skills', 'flows', 'journal', 'filehistory', 'connections', 'history', 'memory']
const [width, height] = (process.argv[2] || '1100x720').split('x').map(Number)
const repo = path.join(__dirname, '..')

const css = ['rpg-tokens.css', 'style.css']
  .map(file => fs.readFileSync(path.join(repo, 'vscode-extension', 'media', file), 'utf8')).join('\n')

const frames = SURFACES.map(surface => {
  const page = execFileSync(process.execPath,
    [path.join(__dirname, 'render-hub-surface.js'), surface],
    { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 })
  const body = page.slice(page.indexOf('<div id="root"'), page.lastIndexOf('</body>'))
  return `<figure data-surface="${surface}"><figcaption>${surface}</figcaption>
    <div class="pane" style="width:${width}px;height:${height}px">${body}</div></figure>`
}).join('\n')

process.stdout.write(`<!doctype html>
<html lang="ru"><head><meta charset="UTF-8"><title>Хаб — ${width}x${height}</title>
<style>
${css}
body { margin: 0; padding: 24px; background: #17171a; font-family: system-ui; }
figure { margin: 0 0 28px; }
figcaption { margin-bottom: 6px; color: #9aa0a6; font: 600 12px/1 ui-monospace, monospace; }
.pane { overflow: hidden; border: 1px solid #3a3a40; }
.pane > .app { height: 100%; }
</style></head>
<body data-layout="wide">${frames}</body></html>
`)
