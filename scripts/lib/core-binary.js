// Ядро, которое поднимают смоуки, — то же самое или вчерашнее?
//
// Два смоука запускают настоящий point-core.exe и разговаривают с ним по HTTP.
// Если двоичный файл собран раньше последней правки в Go, они проверяют
// вчерашний код и уверенно печатают «PASS». Ровно так и вышло: правка ядра
// прошла Go-тесты, смоуки отчитались зелёным против старой сборки, и разошлись
// они только через сутки, на пересборке.
//
// Отсутствие файла смоуки ловили и раньше. Не ловили устаревание — а оно хуже:
// отсутствие видно сразу, а устаревший файл выглядит как успех.

const fs = require('fs')
const path = require('path')

const repo = path.join(__dirname, '..', '..')

function newestGoSource(dir, newest = 0) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'node_modules' || entry.name === '.git' || entry.name === 'build') continue
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      newest = newestGoSource(full, newest)
      continue
    }
    if (entry.name.endsWith('_test.go')) continue
    if (!entry.name.endsWith('.go') && entry.name !== 'go.mod' && entry.name !== 'go.sum') continue
    const stamp = fs.statSync(full).mtimeMs
    if (stamp > newest) newest = stamp
  }
  return newest
}

// resolveCoreBinary возвращает путь к ядру и падает, если проверять им нечего.
function resolveCoreBinary() {
  const binary = process.env.POINT_CORE_BINARY
    || path.join(repo, 'vscode-extension', 'bin', process.platform === 'win32' ? 'point-core.exe' : 'point-core')
  if (!fs.existsSync(binary)) {
    throw new Error(`ядро не собрано (${binary}) — проверка прошла бы вхолостую`)
  }
  // Своё ядро подставляют осознанно: проверять его возраст по здешним исходникам
  // бессмысленно.
  if (process.env.POINT_CORE_BINARY) return binary

  const built = fs.statSync(binary).mtimeMs
  const sources = Math.max(newestGoSource(path.join(repo, 'internal')), newestGoSource(path.join(repo, 'cmd')), fs.statSync(path.join(repo, 'go.mod')).mtimeMs, fs.statSync(path.join(repo, 'go.sum')).mtimeMs)
  if (sources > built) {
    const age = Math.round((sources - built) / 1000)
    throw new Error(
      `ядро старше исходников на ${age} с — смоук проверил бы вчерашний код и напечатал PASS.\n` +
      '     Пересоберите: go build -trimpath -ldflags "-s -w" -o vscode-extension/bin/point-core.exe ./cmd/server',
    )
  }
  return binary
}

module.exports = { resolveCoreBinary }
