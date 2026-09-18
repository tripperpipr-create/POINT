// Слово, написанное двумя алфавитами сразу.
//
// «Строгость revью» на экране настройки Мастера выглядела обычным словом, а на
// деле «rev» там латиницей и «ью» кириллицей. Заглавными это видно сразу —
// «СТРОГОСТЬ REVЬЮ», — но подпись ползунка набрана строчными, и промах прожил
// в продукте незамеченным.
//
// Вред не только косметический: такое слово не находится поиском ни по одному
// из написаний, читалка произносит его по буквам, а перевод и словарь на нём
// спотыкаются. Клавиатура переключается посреди слова — значит опечатка сделана
// один раз и повторится.
//
// Проверка механическая и грубая: ищем токен, внутри которого есть буквы обоих
// алфавитов. Escape-последовательности строк («\nПодробности») отбрасываем —
// это не слово, а перевод строки перед словом.

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, '..', '..')
const files = ['vscode-extension/extension.js']
const clientRoot = path.join(root, 'vscode-extension', 'ui', 'client')
const visit = directory => {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const target = path.join(directory, entry.name)
    if (entry.isDirectory()) visit(target)
    else if (entry.isFile() && entry.name.endsWith('.js')) files.push(path.relative(root, target))
  }
}
visit(clientRoot)

const WORD = /[A-Za-zА-Яа-яЁё]{2,}/g
const CYRILLIC = /[А-Яа-яЁё]/
const LATIN = /[A-Za-z]/
// «\n» + слово: буква escape-последовательности к слову не относится.
const ESCAPE_PREFIX = /^[ntrbfv][А-Яа-яЁё]+$/

const failures = []
for (const file of files) {
  const lines = fs.readFileSync(path.join(root, file), 'utf8').split('\n')
  for (let index = 0; index < lines.length; index += 1) {
    for (const match of lines[index].matchAll(WORD)) {
      const word = match[0]
      if (!CYRILLIC.test(word) || !LATIN.test(word)) continue
      if (ESCAPE_PREFIX.test(word)) continue
      failures.push(`${file}:${index + 1} — «${word}»: слово набрано двумя алфавитами`)
    }
  }
}

if (failures.length) {
  console.error('ДВА АЛФАВИТА В ОДНОМ СЛОВЕ:')
  for (const line of failures) console.error('  · ' + line)
  console.error('  Такое слово не находится поиском и читается по буквам. Наберите его одним алфавитом.')
  process.exit(1)
}
console.log('слова набраны одним алфавитом')
