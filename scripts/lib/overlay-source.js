// Исходник оверлея Code-OSS как один текст.
//
// Затворы и смоуки ищут заплаты оверлея чтением: «есть ли такая строка».
// Читали они `distribution/apply-overlay.mjs` по имени файла, а заплаты с
// 27 сентября 2026 живут и в модулях `overlay-*.mjs`, которые он подключает:
// рейки, верхняя панель, сравнение, первый кадр. Проверка по одному файлу
// падала бы не на дефекте, а на переезде — или, если она отрицательная,
// проходила бы вхолостую.
//
// Единица — оверлей и ровно те модули, которые он импортирует. Сторож
// слепоты: импортированный модуль обязан найтись, а модулей не может быть
// ноль — иначе разбор импортов сам перестал что-либо видеть.

const fs = require('fs')
const path = require('path')

function overlayFiles(root = path.resolve(__dirname, '..', '..')) {
  const dir = path.join(root, 'distribution')
  const main = fs.readFileSync(path.join(dir, 'apply-overlay.mjs'), 'utf8')
  const modules = [...main.matchAll(/from '\.\/(overlay-[\w-]+\.mjs)'/g)].map(match => match[1])
  if (modules.length === 0) throw new Error('overlay source: apply-overlay.mjs не импортирует ни одного модуля overlay-*.mjs — разбор импортов ослеп')
  for (const name of modules) {
    if (!fs.existsSync(path.join(dir, name))) throw new Error(`overlay source: модуль ${name} импортирован, но не найден`)
  }
  return ['apply-overlay.mjs', ...modules].map(name => path.join(dir, name))
}

function overlaySource(root) {
  return overlayFiles(root).map(file => fs.readFileSync(file, 'utf8')).join('\n')
}

module.exports = { overlayFiles, overlaySource }
