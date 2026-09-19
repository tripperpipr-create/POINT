// Ветка обработчика клика обязана сказать, что действие обработано.
//
// Обработчики кликов вебвью выстроены в цепочку: `main.js` зовёт их по
// очереди и останавливается на первом, который вернул `true`
// (`if (handleMasterClickAction({...})) return`). Ветка, вышедшая голым
// `return`, отдаёт `undefined` — цепочка не прерывается, и один клик
// продолжает ходить по всем остальным обработчикам.
//
// Так в дереве накопилось двадцать семь таких веток, из них восемнадцать в
// одном файле — с недостижимым `return true` строкой ниже, то есть намерение
// было записано, а работал соседний оператор. Видимого двойного срабатывания
// не наблюдалось только потому, что имена действий пока нигде не совпадают:
// защита была снята, а не сработала.
//
// Проверка текстовая и намеренно грубая: она разбирает только то, что есть в
// дереве, — ветки вида `if (action === '...') { ... }` внутри экспортируемых
// функций `handle*ClickAction` и `handle*Action`. Непонятое считается
// неизвестным, а не ошибочным.
//
//   node scripts/check-click-handler-returns.mjs

import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const clientDir = path.join(root, 'vscode-extension', 'ui', 'client')

const sources = readdirSync(clientDir, { withFileTypes: true })
  .filter(entry => entry.isFile() && entry.name.endsWith('.js'))
  .map(entry => entry.name)
  .sort()

// Сторож «находок мало»: проверка, переставшая что-либо видеть после переезда
// модулей, проходит вхолостую и молчит об этом громче всего.
const handlerPattern = /export\s+function\s+(handle\w*(?:Click)?Action)\s*\(/
const handlerFiles = sources.filter(name => handlerPattern.test(readFileSync(path.join(clientDir, name), 'utf8')))
if (handlerFiles.length < 8) {
  console.error(`обработчиков кликов найдено ${handlerFiles.length} — проверка прошла бы вхолостую`)
  process.exit(1)
}

const problems = []
let branches = 0

// Границы тела обработчика: от `export function handle…Action(` до строки с
// одинокой закрывающей скобкой нулевого отступа. В том же файле живут и другие
// функции — установщик drag&drop, например, — и голый `return` в них законен:
// они ничего не возвращают и ни в какой цепочке не участвуют.
const handlerBodies = text => {
  const lines = text.split(/\r?\n/)
  const ranges = []
  for (let index = 0; index < lines.length; index += 1) {
    if (!handlerPattern.test(lines[index])) continue
    let end = lines.length - 1
    for (let cursor = index + 1; cursor < lines.length; cursor += 1) {
      if (lines[cursor] === '}') {
        end = cursor
        break
      }
    }
    ranges.push([index, end])
  }
  return ranges
}

for (const name of handlerFiles) {
  const text = readFileSync(path.join(clientDir, name), 'utf8')
  const lines = text.split(/\r?\n/)
  const bodies = handlerBodies(text)
  const insideHandler = line => bodies.some(([start, end]) => line > start && line < end)

  for (let index = 0; index < lines.length; index += 1) {
    // Недостижимый `return true` сразу за голым `return` — след намерения,
    // которое не работает. Это самый дешёвый и самый однозначный признак.
    if (/^\s*return\s*$/.test(lines[index]) && /^\s*return true\s*$/.test(lines[index + 1] || '')) {
      problems.push(`${name}:${index + 1} — голый return, за ним недостижимый return true`)
    }
  }

  // Ветки действий разбираются только блочные: от `if (action === '...') {` до
  // строки с закрывающей скобкой того же отступа. Однострочная ветка своего
  // блока не открывает, и искать в ней конец по отступу значит уехать до конца
  // файла — так проверка и обвиняла чужой код ниже по тексту.
  for (let index = 0; index < lines.length; index += 1) {
    const opening = lines[index].match(/^(\s*)if\s*\(\s*action\s*===\s*'([^']+)'/)
    if (!opening) continue
    const [, indent, action] = opening
    branches += 1
    if (!lines[index].trimEnd().endsWith('{')) continue
    const closing = `${indent}}`
    for (let cursor = index + 1; cursor < lines.length; cursor += 1) {
      if (lines[cursor] === closing || lines[cursor].startsWith(`${closing} `)) break
      if (/^\s*return\s*$/.test(lines[cursor])) {
        problems.push(`${name}:${cursor + 1} — ветка '${action}' выходит голым return: клик пойдёт дальше по цепочке`)
      }
    }
  }

  // Однострочный ранний выход внутри самого обработчика: `…;return}` здесь
  // всегда ветка, отдающая undefined.
  for (let index = 0; index < lines.length; index += 1) {
    if (/;\s*return\s*}/.test(lines[index]) && insideHandler(index)) {
      problems.push(`${name}:${index + 1} — однострочная ветка выходит голым return: клик пойдёт дальше по цепочке`)
    }
  }
}

if (branches < 100) {
  console.error(`веток действий найдено ${branches} — проверка прошла бы вхолостую`)
  process.exit(1)
}

if (problems.length > 0) {
  console.error('ветки обработчиков, отдающие undefined вместо true:')
  for (const problem of problems) console.error(`  · ${problem}`)
  console.error('')
  console.error('Ветка, распознавшая действие, обязана вернуть true — даже когда всё,')
  console.error('что она сделала, это показала ошибку валидации.')
  process.exit(1)
}

console.log(JSON.stringify({ clickHandlerReturns: 'ok', files: handlerFiles.length, branches }))
