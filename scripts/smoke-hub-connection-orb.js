// Состояние связи читается не только цветом.
//
// В трёх местах выбора подключения — настройка компаньона, настройка мастера и
// онбординг — состояние показывалось одним крашеным шариком: класс со статусом
// и ни слова рядом. При дальтонизме и в высококонтрастной теме «подключён» и
// «ошибка» неразличимы, а читалка не произносит ничего. Вдобавок цвет был задан
// только для двух состояний из пяти: disconnected и probing выглядели как
// «неизвестно».
//
// Проверяем два свойства: у каждого состояния есть человеческая подпись, и
// разметку шарика никто не собирает в обход общего помощника.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const mainPath = path.join(repo, 'vscode-extension/media/main.js')
const main = fs.readFileSync(mainPath, 'utf8')
const clientRoot = path.join(repo, 'vscode-extension/ui/client')
const sourceFiles = []
const visit = directory => {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const target = path.join(directory, entry.name)
    if (entry.isDirectory()) visit(target)
    else if (entry.isFile() && entry.name.endsWith('.js')) sourceFiles.push(target)
  }
}
visit(clientRoot)
const source = sourceFiles.sort().map(file => fs.readFileSync(file, 'utf8')).join('\n')

const context = {
  acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
  document: { getElementById: () => ({ innerHTML: '', addEventListener() {}, querySelector: () => null, querySelectorAll: () => [] }), body: { dataset: {} } },
  window: { addEventListener() {} },
  console, Date, Map, Set,
  requestAnimationFrame(cb) { cb(); return 0 },
  cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
}
vm.runInNewContext(`${main}\nthis.__orb = connectionOrbHtml\nthis.__labels = connectionStatusLabels`, context, { filename: 'main.js' })

const orb = context.__orb
const labels = context.__labels
const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

if (typeof orb !== 'function') {
  console.log('помощник connectionOrbHtml не найден — проверка прошла бы вхолостую')
  process.exit(1)
}

// Каждое состояние из словаря обязано доходить до человека словами.
for (const [status, label] of Object.entries(labels)) {
  const html = orb(status)
  check(`${status}: подпись для читалки`, html.includes(`aria-label="${label}"`), html)
  check(`${status}: подсказка при наведении`, html.includes(`title="${label}"`), html)
  check(`${status}: класс состояния сохранён`, html.includes(`connection-orb ${status}`), html)
}

// Незнакомое состояние не должно оставаться немым: пусть говорит хотя бы само
// значение, как это делает карточка на странице «Связи».
const unknown = orb('throttled')
check('незнакомое состояние всё равно названо', unknown.includes('aria-label="throttled"'), unknown)

// Пустое состояние — это «неизвестно», а не пустая подпись.
const empty = orb('')
check('пустое состояние названо «неизвестно»', empty.includes(`aria-label="${labels.unknown}"`), empty)

// Структурная половина: никто не собирает шарик мимо помощника. Поведенческая
// проверка увидела бы это только если автор новой копии сам добавит свой случай.
// Тело самого помощника из просмотра исключаем: разметка шарика живёт там по
// определению, и без этого проверка ловила бы собственную реализацию.
const withoutComments = source
  .replace(/^\s*\/\/.*$/gm, ' ')
  .replace(/function connectionOrbHtml\(status\) \{[\s\S]*?\n\}/, ' ')
const raw = withoutComments.match(/<span class="connection-orb \$\{/g) || []
check('шарик собирается только помощником', raw.length === 0,
  `${raw.length} мест собирают разметку сами`)

// Защита от холостого хода: сам помощник обязан присутствовать в разметке.
// Мест стало меньше не потому, что шарик убрали: три копии выбора подключения
// свелись к одному общему контролу, и карточка связи теперь одна.
check('помощник используется в разметке',
  (withoutComments.match(/\$\{connectionOrbHtml\(/g) || []).length >= 2,
  'помощник не вызывается в ожидаемых местах')

if (failures.length) {
  console.log('СОСТОЯНИЕ СВЯЗИ — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('состояние связи читается словами: PASS')
