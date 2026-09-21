// Узлы графа флоу стоят там, где их положили.
//
// Холст рисует места узлов, и до этой проверки он задавал их инлайновым стилем:
// `style="left:210px;top:140px"`. CSP вебвью (`style-src` без `'unsafe-inline'`)
// выбрасывает атрибут `style=""` целиком, поэтому смещений не было ни у одного
// узла — все садились в начало координат друг на друга. Заметить это разметкой
// было нельзя: строка в ней стояла, и смоук, ищущий `left:210px`, был бы зелёным
// ровно на сломанном экране.
//
// Отсюда две половины проверки. Первая: в разметке холста нет инлайнового стиля
// вовсе — величины уезжают атрибутами `data-*`, которые CSP не трогает. Вторая:
// расстановка после отрисовки читает те самые атрибуты и пишет пиксели через
// CSSOM. Обе половины обязаны сойтись на одних именах, поэтому фальшивый DOM
// собирается разбором настоящей разметки, а не заводится рядом с ней руками:
// разъехавшиеся имена — это ровно тот случай, который здесь и ловится.
//
//   node scripts/smoke-flow-canvas-placement.mjs

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const clientDir = path.join(root, 'vscode-extension', 'ui', 'client')
const load = name => import(pathToFileURL(path.join(clientDir, name)).href)

const { esc } = await load('html-escape.js')
const { createAgentWorkflowEditors } = await load('agent-workflow-editors.js')

const ui = { state: { boot: { flows: [], flowRuns: [] } } }
const editors = createAgentWorkflowEditors({
  esc,
  ui,
  statusLabels: { running: 'В ПОХОДЕ' },
  countOf: () => '',
  shell: html => html,
  root: { querySelector: () => null, querySelectorAll: () => [] },
})

// Места намеренно не совпадают с сеткой по умолчанию (24 + 150·колонка): узел,
// которому смещение не доехало, сел бы как раз в сетку, и проверка на «место
// есть» прошла бы на сломанном холсте.
const flow = {
  id: 'flow-1',
  name: 'Схема',
  description: '',
  nodes: [
    { id: 'alpha', kind: 'input', name: 'Вход', positionX: 210, positionY: 140 },
    { id: 'beta', kind: 'agent', name: 'Агент', positionX: 480, positionY: 300 },
    { id: 'gamma', kind: 'output', name: 'Выход', positionX: 77, positionY: 412 },
  ],
  edges: [{ id: 'edge-0', from: 'alpha', to: 'beta' }],
}
const html = editors.flowCanvasHtml(flow, false, 'alpha', undefined)

// --- половина первая: разметка ------------------------------------------------

if (/\bstyle\s*=\s*["'`]/.test(html)) {
  throw new Error('холст флоу снова задаёт стиль в разметке — CSP выбросит его целиком')
}
for (const node of flow.nodes) {
  if (!html.includes(`data-x="${node.positionX}"`) || !html.includes(`data-y="${node.positionY}"`)) {
    throw new Error(`координаты узла ${node.id} не доехали до разметки`)
  }
}
if (!/<div class="flow-canvas" data-width="\d+" data-height="\d+">/.test(html)) {
  throw new Error('размер холста не доехал до разметки')
}

// --- половина вторая: расстановка --------------------------------------------

// Разбор настоящей разметки: имена атрибутов проверка берёт у неё, а не у себя.
const camel = name => name.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase())
function element (tagSource) {
  const attributes = {}
  for (const [, name, value] of tagSource.matchAll(/([a-zA-Z-]+)="([^"]*)"/g)) attributes[name] = value
  const dataset = {}
  for (const [name, value] of Object.entries(attributes)) {
    if (name.startsWith('data-')) dataset[camel(name.slice(5))] = value
  }
  const classes = new Set(String(attributes.class || '').split(/\s+/).filter(Boolean))
  const written = {}
  return {
    attributes,
    dataset,
    written,
    classes,
    style: { setProperty (property, value) { written[property] = value } },
    classList: { add: name => classes.add(name), remove: name => classes.delete(name) },
  }
}

const canvasTag = html.match(/<div class="flow-canvas"[^>]*>/)
const nodeTags = [...html.matchAll(/<button[^>]*class="flow-node[^>]*>/g)].map(match => match[0])
if (!canvasTag || nodeTags.length !== flow.nodes.length) {
  throw new Error('разметку холста не удалось разобрать — проверка прошла бы вхолостую')
}

const canvas = element(canvasTag[0])
const nodes = nodeTags.map(element)
canvas.querySelectorAll = selector => (selector === '.flow-node' ? nodes : [])
const wrap = element('<div class="flow-canvas-wrap">')
wrap.querySelector = selector => (selector === '.flow-canvas' ? canvas : null)
const host = { querySelector: selector => (selector === '.flow-canvas-wrap' ? wrap : null) }

const placed = editors.applyFlowNodePlacement(host)
if (placed !== flow.nodes.length) {
  throw new Error(`расставлено ${placed} узлов из ${flow.nodes.length}`)
}
flow.nodes.forEach((source, index) => {
  const written = nodes[index].written
  if (written.left !== `${source.positionX}px` || written.top !== `${source.positionY}px`) {
    throw new Error(`узел ${source.id} встал в ${written.left}/${written.top} вместо ${source.positionX}px/${source.positionY}px`)
  }
})
if (!canvas.written.width || !canvas.written.height) throw new Error('размер холста не проставлен')
if (!wrap.classes.has('is-placed')) throw new Error('холст не помечен расставленным — связи останутся скрытыми')

// Узел с испорченной координатой не притворяется расставленным: холст остаётся
// без признака, и слой раскладывает узлы запасным рядом вместо стопки в углу.
nodes[1].dataset.y = 'неизвестно'
nodes[1].written.top = undefined
if (editors.applyFlowNodePlacement(host) !== flow.nodes.length - 1) {
  throw new Error('узел без координаты посчитан расставленным')
}
if (wrap.classes.has('is-placed')) throw new Error('холст помечен расставленным, хотя место известно не у всех')

// --- обе половины обязаны быть подключены ------------------------------------

// Расстановка живёт после отрисовки, а не внутри разметки: вызов у неё ровно
// один, и без него холст снова складывается в угол — молча.
const main = readFileSync(path.join(clientDir, 'main.js'), 'utf8')
if (!/^\s*applyFlowNodePlacement\(\)/m.test(main)) {
  throw new Error('main.js не вызывает расстановку после отрисовки')
}
const layer = readFileSync(path.join(root, 'vscode-extension', 'ui', 'layers', '60-suggestions.css'), 'utf8')
for (const rule of ['.flow-canvas-wrap.is-placed .flow-node { position: absolute; }', '.flow-canvas-wrap:not(.is-placed) .flow-edges']) {
  if (!layer.includes(rule)) throw new Error(`слой холста потерял правило: ${rule}`)
}

process.stdout.write(JSON.stringify({ flowCanvas: 'placed', nodes: flow.nodes.length, inlineStyles: 0 }))
