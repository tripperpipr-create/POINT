// Все шаги онбординга рядом, каждый в коробке размером с настоящую панель.
// Смотреть глазами и мерить скриптом — обе проверки на одной странице.
const fs = require('fs')
const path = require('path')
const { execFileSync } = require('child_process')

const STEPS = ['orchestrator-brain', 'orchestrator-choose']
const [width, height] = (process.argv[2] || '1100x720').split('x').map(Number)
const repo = path.join(__dirname, '..')

const css = ['rpg-tokens.css', 'style.css']
  .map(file => fs.readFileSync(path.join(repo, 'vscode-extension', 'media', file), 'utf8')).join('\n')

const frames = STEPS.map(step => {
  const page = execFileSync(process.execPath,
    [path.join(__dirname, 'render-hub-surface.js'), 'onboarding', step],
    { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 })
  const body = page.slice(page.indexOf('<div id="root"'), page.lastIndexOf('</body>'))
  return `<figure data-step="${step}"><figcaption>${step}</figcaption>
    <div class="pane" style="width:${width}px;height:${height}px">${body}</div></figure>`
}).join('\n')

process.stdout.write(`<!doctype html>
<html lang="ru"><head><meta charset="UTF-8"><title>Онбординг — ${width}x${height}</title>
<style>
${css}
body { margin: 0; padding: 24px; background: #17171a; font-family: system-ui; }
figure { margin: 0 0 28px; }
figcaption { margin-bottom: 6px; color: #9aa0a6; font: 600 12px/1 ui-monospace, monospace; }
.pane { overflow: hidden; border: 1px solid #3a3a40; resize: both; }
.pane > .app { height: 100%; }
</style></head>
<body data-layout="wide">${frames}</body></html>
`)
