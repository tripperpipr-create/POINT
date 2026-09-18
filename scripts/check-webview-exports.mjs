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
  unusedExports: orphans.length,
}))
