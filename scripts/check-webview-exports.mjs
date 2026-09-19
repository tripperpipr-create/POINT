// Сверка имён между модулями вебвью.
//
// `ui/client` — дерево из четырёх десятков модулей, которые собираются в один
// `media/main.js`. Опечатка в имени при импорте не ломает сборку: esbuild
// подставит `undefined`, и падение случится в браузере — на смоуке, если он
// дойдёт до этой ветки, или у человека, если не дойдёт. Синтаксическая
// проверка `node --check` такого не видит: она смотрит на файл, а ошибка живёт
// на стыке двух.
//
// Проверка нужна именно сейчас: модули выделяются из `ui/client/main.js`
// пачками, и каждый выделенный кусок — новый десяток имён на стыке.
//
// Разбор текстовый и намеренно грубый: объявления экспортов в этом дереве
// пишутся в трёх формах, и все три однострочные. Динамику (`export { x as y }`
// из переменной, реэкспорт со звёздочкой) проверка не понимает и потому
// считает неизвестной, а не ошибочной — молчать о непонятом честнее, чем
// ронять сборку на догадке.
//
//   node scripts/check-webview-exports.mjs

import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const clientDir = path.join(root, 'vscode-extension', 'ui', 'client')

const sources = readdirSync(clientDir, { withFileTypes: true })
  .filter(entry => entry.isFile() && entry.name.endsWith('.js'))
  .map(entry => entry.name)
  .sort()

if (sources.length < 10) {
  console.error('в ui/client подозрительно мало модулей — проверка прошла бы вхолостую')
  process.exit(1)
}

// Что модуль отдаёт наружу.
const exportsOf = text => {
  const names = new Set()
  let wildcard = false
  for (const match of text.matchAll(/^export\s+(?:async\s+)?function\s+([A-Za-z0-9_$]+)/gm)) names.add(match[1])
  for (const match of text.matchAll(/^export\s+(?:const|let|var|class)\s+([A-Za-z0-9_$]+)/gm)) names.add(match[1])
  for (const match of text.matchAll(/^export\s*\{([^}]*)\}/gm)) {
    for (const piece of match[1].split(',')) {
      const parts = piece.trim().split(/\s+as\s+/)
      const name = (parts[1] || parts[0] || '').trim()
      if (name) names.add(name)
    }
  }
  if (/^export\s+\*/m.test(text)) wildcard = true
  return { names, wildcard }
}

// Что модуль просит у соседей. Импорт по умолчанию и звёздочкой пропускаем:
// в этом дереве их нет, а догадываться проверка не должна.
const namedImportsOf = text => {
  const wanted = []
  for (const match of text.matchAll(/import\s*\{([^}]*)\}\s*from\s*'(\.[^']+)'/g)) {
    const from = match[2]
    for (const piece of match[1].split(',')) {
      const name = piece.trim().split(/\s+as\s+/)[0].trim()
      if (name) wanted.push({ name, from })
    }
  }
  return wanted
}

const errors = []
const exported = new Map()
const imported = new Map()
const texts = new Map()

for (const name of sources) {
  const text = readFileSync(path.join(clientDir, name), 'utf8')
  texts.set(name, text)
  exported.set(name, exportsOf(text))
}

let checkedNames = 0
for (const name of sources) {
  for (const { name: wanted, from } of namedImportsOf(texts.get(name))) {
    const target = path.basename(from)
    if (!exported.has(target)) {
      errors.push(`${name}: импортирует ${wanted} из ${from}, но такого модуля в ui/client нет`)
      continue
    }
    checkedNames += 1
    const known = imported.get(target) || new Set()
    known.add(wanted)
    imported.set(target, known)
    const { names, wildcard } = exported.get(target)
    if (wildcard) continue
    if (!names.has(wanted)) {
      errors.push(`${name}: импортирует ${wanted} из ${from}, но ${target} такого имени не отдаёт`)
    }
  }
}

// Состояние `main.js` в соседнем модуле обязано быть чем-то связано.
//
// При выносе очередной пачки веток каждое имя состояния переписывается либо в
// `ui.имя`, либо в параметр модуля. Одно пропущенное остаётся свободной
// переменной: сборка молчит, `node --check` молчит, а `ReferenceError`
// выпадает только на живом клике — 18 сентября так уехал `onboardingDraft`.
// Прячется он в спреде: в `{ ...onboardingDraft, … }` имя стоит после точки и
// на поиск отзывается как свойство.
//
// Связанным считается имя, которое модуль объявляет сам, импортирует или
// принимает параметром. Разбор параметров грубый и намеренно щедрый: лишняя
// связка — это молчание о настоящей ошибке, но врать о чужом коде хуже.
// Комментарий — не код. Имена состояния в этом дереве обсуждают словами, и без
// вычёркивания комментариев проверка ловила бы девять объяснений вместо ошибок.
// Строка гасится только тогда, когда `//` открывает её целиком: внутри разметки
// живут ссылки вида `https://…`, и резать по первому `//` значило бы стирать код.
// Текст строки — тоже не код: `'./master-chat-state.js'` и `class="… state-…"`
// содержат имена состояния буквами. Гасится только сам текст; вставки `${…}`
// в шаблонах остаются, потому что разметка этого дерева живёт именно в них.
const codeOnly = text => {
  const plain = text
    .replace(/\/\*[\s\S]*?\*\//g, match => match.replace(/[^\n]/g, ' '))
    .split('\n')
    .map(line => (/^\s*\/\//.test(line) ? '' : line))
    .join('\n')
  const out = plain.split('')
  const nested = []
  let quote = ''
  let depth = 0
  for (let at = 0; at < plain.length; at += 1) {
    const char = plain[at]
    if (quote && char === '\\') {
      if (plain[at + 1] && plain[at + 1] !== '\n') out[at + 1] = ' '
      at += 1
      continue
    }
    if (!quote) {
      if (char === "'" || char === '"' || char === '`') { quote = char; continue }
      if (char === '{') depth += 1
      else if (char === '}') {
        if (depth === 0 && nested.length) { depth = nested.pop(); quote = '`' } else depth -= 1
      }
      continue
    }
    if (char === quote) { quote = ''; continue }
    if (quote === '`' && char === '$' && plain[at + 1] === '{') {
      nested.push(depth)
      depth = 0
      quote = ''
      at += 1
      continue
    }
    if (char !== '\n') out[at] = ' '
  }
  return out.join('')
}

const bindingsOf = text => {
  const bound = new Set()
  for (const match of text.matchAll(/(?:const|let|var|function|class)\s+([A-Za-z0-9_$]+)/g)) bound.add(match[1])
  // Разбор с фигурной скобки до парной ей — но только там, где скобка правда
  // что-то связывает: `const { a } = deps` и список параметров `f({ a, b }) {`.
  // Литерал `x = { ...draft, flag: true }` сюда попадать не должен, иначе
  // `draft` посчитается связанным и спред снова уедет незамеченным.
  for (const match of text.matchAll(/(?:\b(?:const|let|var)\b|\()\s*\{/g)) {
    const declaration = text[match.index] !== '('
    const open = text.indexOf('{', match.index)
    let depth = 0
    let end = open
    for (let at = open; at < text.length; at += 1) {
      if (text[at] === '{') depth += 1
      else if (text[at] === '}') { depth -= 1; if (depth === 0) { end = at; break } }
    }
    if (!declaration && !/^\s*\)\s*(?:\{|=>)/.test(text.slice(end + 1))) continue
    for (const piece of text.slice(open, end + 1).matchAll(/([A-Za-z0-9_$]+)\s*[,:}\n]/g)) bound.add(piece[1])
  }
  for (const match of text.matchAll(/(?:\bfunction\s+[A-Za-z0-9_$]*\s*)?\(([^)(]*)\)(?:\s*=>|\s*\{)/g)) {
    for (const piece of match[1].split(',')) {
      const name = piece.trim().split(/[\s=]/)[0]
      if (/^[A-Za-z0-9_$]+$/.test(name)) bound.add(name)
    }
  }
  for (const match of text.matchAll(/([A-Za-z0-9_$]+)\s*=>/g)) bound.add(match[1])
  return bound
}

const mainState = new Set()
for (const match of (texts.get('main.js') || '').matchAll(/^let\s+([A-Za-z0-9_$]+)/gm)) mainState.add(match[1])
if (mainState.size < 50) {
  errors.push(`в main.js найдено всего ${mainState.size} имён состояния — разбор сломался, проверка идёт вхолостую`)
}
let leaks = 0
for (const name of sources) {
  if (name === 'main.js') continue
  const text = codeOnly(texts.get(name))
  const bound = bindingsOf(text)
  for (const key of mainState) {
    if (bound.has(key)) continue
    // Обращение к свойству (`details.contextItems`) — не наше имя. Спред
    // (`...onboardingDraft`) — наше: три точки, и последняя точка стоит после
    // точки, а не после имени. Ровно этим они и различаются.
    // Ключ объекта (`{ state: … }`) именем не является — это свойство,
    // совпавшее буквами; сокращённая запись `{ state }` за двоеточием не прячется.
    for (const match of text.matchAll(new RegExp(String.raw`(?<![\w$])(?<!(?<!\.)\.)${key}\b(?!\s*:)`, 'g'))) {
      const line = text.slice(0, match.index).split('\n').length
      leaks += 1
      if (leaks <= 20) errors.push(`${name}:${line}: ${key} — имя ниоткуда не приходит: это состояние main.js, и его берут через ui.`)
    }
  }
}
if (leaks > 20) errors.push(`и ещё ${leaks - 20} таких же мест`)

if (checkedNames < 50) {
  errors.push(`сверено всего ${checkedNames} имён — разбор импортов сломался, проверка идёт вхолостую`)
}

// Экспорт, который никто не просит, — либо забытый кусок после переезда, либо
// имя, сохранённое для смоука. Это предупреждение, а не отказ: смоуки читают
// модули напрямую и вправе брать то, чего не берёт main.js.
const orphans = []
for (const name of sources) {
  if (name === 'main.js') continue
  const { names } = exported.get(name)
  const used = imported.get(name) || new Set()
  for (const exportedName of names) {
    if (!used.has(exportedName)) orphans.push(`${name}: ${exportedName}`)
  }
}

if (errors.length) {
  console.error('СВЕРКА ИМЁН ВЕБВЬЮ ПРОВАЛЕНА:')
  for (const message of errors) console.error('  · ' + message)
  process.exit(1)
}

console.log(JSON.stringify({
  webviewExports: 'ok',
  modules: sources.length,
  checkedNames,
  stateNames: mainState.size,
  unusedExports: orphans.length,
}))
