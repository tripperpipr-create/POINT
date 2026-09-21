// Ядро отклоняет любой не-GET запрос под /api/ без Content-Type:
// application/json (internal/httpapi/middleware.go). Служба расширения ставит
// этот заголовок только там, где у запроса есть тело, поэтому `{method:'POST'}`
// без body уходит без заголовка и возвращается ошибкой
// «Content-Type must be application/json». Один такой вызов уже стоил кнопки
// «Остановить» в разговоре с Мастером: ход не отменялся, а падал.
//
//   node scripts/check-api-content-type.mjs

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const roots = [
  path.join(repo, 'vscode-extension'),
  path.join(repo, 'vscode-extension', 'ui', 'client'),
]
const files = roots.flatMap(dir => fs.readdirSync(dir, { withFileTypes: true })
  .filter(entry => entry.isFile() && entry.name.endsWith('.js'))
  .map(entry => path.join(dir, entry.name)))

// Сторож против ослепшего скана: если файлы переедут, пустой обход отчитается
// «нарушений нет» и затвор перестанет что-либо держать.
const MIN_FILES = 60
const MIN_CALLS = 120
if (files.length < MIN_FILES) {
  console.error(`сканер ослеп: файлов ${files.length}, ожидалось не меньше ${MIN_FILES}`)
  process.exit(1)
}

const call = /\.request\(\s*([^,]+?),\s*\{((?:[^{}]|\{[^{}]*\})*)\}/gs
let calls = 0
const offenders = []
for (const file of files) {
  const source = fs.readFileSync(file, 'utf8')
  for (const match of source.matchAll(call)) {
    calls += 1
    const options = match[2]
    const method = options.match(/method\s*:\s*['"](\w+)['"]/)
    if (!method || ['GET', 'DELETE'].includes(method[1].toUpperCase())) continue
    if (/\bbody\b/.test(options) || /Content-Type/i.test(options)) continue
    const line = source.slice(0, match.index).split('\n').length
    offenders.push(`${path.relative(repo, file)}:${line} — ${method[1].toUpperCase()} ${match[1].trim().slice(0, 80)}`)
  }
}
if (calls < MIN_CALLS) {
  console.error(`сканер ослеп: разобрано вызовов с настройками ${calls}, ожидалось не меньше ${MIN_CALLS}`)
  process.exit(1)
}
if (offenders.length) {
  console.error('запрос к ядру без тела и без Content-Type (ядро ответит 415):')
  for (const offender of offenders) console.error('  ' + offender)
  console.error("добавьте body: '{}' — заголовок ставится службой по наличию тела")
  process.exit(1)
}
console.log(`запросы к ядру: ${calls} вызовов в ${files.length} файлах, все не-GET несут тело`)
