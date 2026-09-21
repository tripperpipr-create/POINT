// Инлайновых стилей в разметке вебвью быть не может.
//
// CSP Хаба (`vscode-extension/extension.js`, `point-panels.js`: `style-src
// ${webview.cspSource}` без `'unsafe-inline'`) выбрасывает атрибут `style=""`
// целиком. Написанный в разметке стиль не падает и не ругается — он просто не
// доезжает, и элемент показывает не то, что задумано, а то, что осталось от
// правил. Дважды это уже стоило экрана: полосы прогресса со `style="width:45%"`
// занимали всю ширину и рапортовали «готово» на любом значении (лечение —
// `data-fill` и `ui/layers/11-progress-fill.css`), а узлы графа флоу со
// `style="left:210px"` складывались стопкой в начале координат при том, что
// связи между ними рисовались по местам.
//
// Оба раза ошибку находили глазами спустя недели. Проверка текстом — потому
// что в разметке этого дерева стиль пишется буквами и только так: строка с
// `style="` в шаблоне видна без браузера, а браузера в гейте нет.
//
// Куда девать величину:
//   · доля от нуля до ста — `fillAttribute` из `ui/client/format-units.js`;
//   · произвольное число (пиксели, координаты) — атрибут `data-*` в разметке
//     и запись свойства из скрипта после отрисовки: программная правка
//     `element.style` не инлайновый стиль, и CSP её не касается. Образцы —
//     `applyFlowNodePlacement` в `ui/client/agent-workflow-editors.js` и
//     `applyMasterComposeReserve` в `ui/client/master-feed.js`.
//
//   node scripts/check-webview-inline-styles.mjs

import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const extension = path.join(root, 'vscode-extension')
const clientDir = path.join(extension, 'ui', 'client')

// Разметка, которая уезжает в вебвью со строгим CSP. Дерево `ui/client`
// собирается в `media/main.js` — единственный скрипт Хаба; `point-panels.js`
// и `media/chronicle.js` рисуют окна инструментов и хронику под тем же CSP.
// Список складывается из каталога, а не из имён: гейт, перечисляющий файлы,
// слепнет на первом же переезде.
const files = [
  ...readdirSync(clientDir, { withFileTypes: true })
    .filter(entry => entry.isFile() && entry.name.endsWith('.js'))
    .map(entry => path.join('vscode-extension', 'ui', 'client', entry.name)),
  path.join('vscode-extension', 'point-panels.js'),
  path.join('vscode-extension', 'media', 'chronicle.js'),
].sort()

// Сторож вхолостую. Дерево вебвью — это четыре десятка модулей; если их вдруг
// оказалось мало, проверка не «прошла», а не нашла, что проверять.
if (files.length < 30) {
  console.error(`сборка вебвью: найдено ${files.length} файлов — гейт прошёл бы вхолостую`)
  process.exit(1)
}

// Комментарий — не разметка. Про этот самый запрет в дереве написано словами и
// с примером `style="width:45%"` внутри: без вычёркивания комментариев гейт
// ловил бы объяснения вместо ошибок. Гасится только строка, которую `//`
// открывает целиком, — внутри шаблонов живут ссылки вида `https://…`.
const codeOnly = text => text
  .replace(/\/\*[\s\S]*?\*\//g, match => match.replace(/[^\n]/g, ' '))
  .split('\n')
  .map(line => (/^\s*\/\//.test(line) ? '' : line))
  .join('\n')

// Два способа задать инлайновый стиль: атрибутом в разметке и тем же атрибутом
// через `setAttribute`. CSP отвергает оба. Запись `element.style.setProperty`
// и `element.style.left = …` разрешена и здесь не ищется.
const PATTERNS = [
  { name: 'style="…" в разметке', regex: /\bstyle\s*=\s*(["'`])/g },
  { name: "setAttribute('style', …)", regex: /\bsetAttribute\s*\(\s*(["'`])style\1/g },
]

// Сторож слепоты. Гейт без находок неотличим от гейта, который разучился
// искать, поэтому детектор сначала показывает, что ловит образец.
const fixture = `const html = \`<i style="width:45%"></i>\`; node.setAttribute('style', 'left:0')`
for (const { name, regex } of PATTERNS) {
  regex.lastIndex = 0
  if (!regex.test(codeOnly(fixture))) {
    console.error(`детектор «${name}» не нашёл образец — проверка ослепла`)
    process.exit(1)
  }
}

const findings = []
for (const relative of files) {
  const lines = codeOnly(readFileSync(path.join(root, relative), 'utf8')).split('\n')
  lines.forEach((line, index) => {
    for (const { name, regex } of PATTERNS) {
      regex.lastIndex = 0
      if (regex.test(line)) findings.push(`${relative}:${index + 1}: ${name}`)
    }
  })
}

if (findings.length) {
  console.error('инлайновый стиль в разметке вебвью — CSP выбросит его целиком:')
  for (const finding of findings) console.error('  · ' + finding)
  console.error('  доля → fillAttribute(); произвольное число → data-* и запись свойства после отрисовки')
  process.exit(1)
}

console.log(JSON.stringify({ inlineStyles: 'none', files: files.length }))
