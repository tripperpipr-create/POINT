// Синтаксис всех JS-файлов расширения, а не перечисленных руками.
//
// В `npm run check` стояла цепочка из шестидесяти `node --check <файл>`. Список
// ручной, и он отстал: из 97 файлов в нём было 54. Семь контроллеров хоста не
// проверял никто — а их, в отличие от вебвью, никто и не собирает, так что
// опечатка в них всплыла бы только в запущенной IDE.
//
// Это тот же класс отказа, от которого уже заведён затвор осиротевших смоуков:
// список, который ведут руками, отстаёт молча. Поэтому здесь не список, а обход.
//
// Чего эта проверка НЕ ловит: `node --check` разбирает файл не так, как esbuild,
// и на испорченном модуле вебвью проходит. Настоящая проверка сборки — сама
// сборка; здесь только грубый синтаксис и только ради файлов хоста.
import { execFile } from 'node:child_process'
import { readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'

const run = promisify(execFile)
const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const extension = path.join(root, 'vscode-extension')

const listJs = directory => {
  let entries = []
  try {
    entries = readdirSync(directory, { withFileTypes: true })
  } catch {
    return []
  }
  return entries
    .filter(entry => entry.isFile() && entry.name.endsWith('.js'))
    .map(entry => path.join(directory, entry.name))
    .sort()
}

const files = [
  ...listJs(extension),
  ...listJs(path.join(extension, 'ui', 'client')),
  ...listJs(path.join(extension, 'media')),
]

if (files.length < 80) {
  console.error(`найдено всего ${files.length} файлов — обход сломался, проверка идёт вхолостую`)
  process.exit(1)
}

const failures = []
const batch = 12
for (let at = 0; at < files.length; at += batch) {
  const slice = files.slice(at, at + batch)
  const results = await Promise.all(slice.map(file =>
    run(process.execPath, ['--check', file]).then(() => null, error => ({ file, error }))))
  for (const result of results) {
    if (result) failures.push(`${path.relative(root, result.file)}: ${String(result.error.stderr || result.error).split('\n')[0]}`)
  }
}

if (failures.length) {
  console.error('СИНТАКСИС JS ПРОВАЛЕН:')
  for (const message of failures) console.error('  · ' + message)
  process.exit(1)
}

console.log(JSON.stringify({ jsSyntax: 'ok', files: files.length }))
