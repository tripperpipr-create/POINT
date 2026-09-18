// Исходник хоста расширения как один текст.
//
// Смоуки проверяют хост чтением: «есть ли такая строка», «зовётся ли этот
// метод». Читали они `extension.js` по имени файла — и это ломалось каждый
// раз, когда однородная группа переезжала в свой контроллер: проверка
// переставала что-либо находить и падала не на дефекте, а на переезде.
//
// Единица, которая не меняется от перекладывания, — дерево модулей хоста:
// сам extension.js и все соседние CommonJS-файлы, которые он подключает.
// Вебвью сюда не входит: у него своя среда и свои проверки (ui/contracts.mjs).

const fs = require('fs')
const path = require('path')

function extensionHostSource(root = path.resolve(__dirname, '..', '..')) {
  const dir = path.join(root, 'vscode-extension')
  return fs.readdirSync(dir, { withFileTypes: true })
    .filter(entry => entry.isFile() && entry.name.endsWith('.js'))
    .map(entry => entry.name)
    .sort()
    .map(name => fs.readFileSync(path.join(dir, name), 'utf8'))
    .join('\n')
}

module.exports = { extensionHostSource }
