// Реплика в чате — текст, а не разметка.
//
// Ответы Мастера и компаньона рисуются formatCompanionMarkdown, и туда доходит
// то, что пишет человек: название предложенного квеста собирается из его же
// сообщения и подставляется в реплику целиком. Значит, к экранированию здесь
// требование не косметическое — webview умеет слать расширению команды, и
// разметка, попавшая внутрь как разметка, получает этот канал.
//
// Один путь его и обходил: абзац, в котором встретился блок кода, возвращался
// склейкой без экранирования — сам блок был собран безопасно, а текст вокруг
// него нет. Проверка держит обе стороны: чужой тег обязан остаться текстом при
// любом соседстве, а полезная разметка — блоки, списки, врезки, ссылки на
// файлы — обязана продолжать работать.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const listeners = {}
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector() { return null },
  querySelectorAll() { return [] },
}
const context = {
  acquireVsCodeApi: () => ({ postMessage() {}, getState() { return undefined }, setState() {} }),
  document: {
    getElementById: id => (id === 'root' ? root : undefined),
    body: { dataset: { layout: 'wide' } },
  },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set,
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() {},
  setTimeout(callback) { callback(); return 0 },
  clearTimeout() {},
}
vm.runInNewContext(fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8'), context,
  { filename: 'media/main.js' })

const render = context.formatCompanionMarkdown
if (typeof render !== 'function') {
  console.log('formatCompanionMarkdown не найден — проверка вхолостую')
  process.exit(1)
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 200)}`)
}

// Что разметчик вправе выпустить, записано тегом с атрибутами целиком
// (scripts/lib/chat-markup.cjs): имя тега без атрибутов пропустило бы
// `<p onclick=…>` как своё. Проверка держит обе стороны — чужой тег остаётся
// текстом при любом соседстве, а своя разметка не выходит из своей формы.
const { markupProblems } = require('./lib/chat-markup.cjs')

for (const [name, input] of [
  ['тег в обычном тексте', 'Предложение «<img src=x onerror=alert(1)>» ждёт решения'],
  ['тег рядом с блоком кода', 'Предложение «<img src=x onerror=alert(1)>» ждёт ```js\nкод\n``` решения'],
  ['однострочный блок в названии', 'Предложение «Почини ```x``` <b>жирный</b>» ждёт решения'],
  ['тег в списке', '- <script>alert(1)</script>\n- второй пункт'],
  ['тег в нумерованном списке', '1. <iframe src=x>\n2. второй'],
  ['ссылка на javascript:', '[жми](javascript:alert(1))'],
  ['ссылка на data:', '[жми](data:text/html,<script>alert(1)</script>)'],
  ['ссылка на vbscript: заглавными', '[жми](VBSCRIPT:msgbox) и JAVASCRIPT:alert(1)'],
  ['кавычка рвёт адрес', '[x](https://a/"onmouseover="alert(1))'],
  ['кавычка в голом адресе', 'смотри https://a/"onmouseover="alert(1)'],
  ['атрибут после адреса', '[x](https://ok.example "t" onclick=alert(1))'],
  ['тег в подписи ссылки', '[<img src=x onerror=alert(1)>](https://ok.example)'],
  ['тег в ячейке таблицы', '| a | b |\n| --- | --- |\n| <script>x</script> | <b onclick=y>z</b> |'],
  ['тег в заголовке', '# <h1 onclick=x>заголовок</h1>'],
  ['тег в цитате', '> <iframe src=x></iframe>'],
  ['тег в задаче', '- [x] <img src=x onerror=y>'],
  ['тег внутри выделения', '**<b>x</b>** и ***<i>y</i>*** и ~~<s>z</s>~~'],
  ['подделка служебных знаков', 'текст \u00000\u0000 и \u0000B0\u0000 и \uE000 конец'],
  ['разметка в пути к файлу', '```"><img src=x>.go\nкод\n```'],
]) {
  for (const [mode, html] of [['готовый ответ', render(input)], ['поток', render.streaming ? render.streaming(input) : '']]) {
    const problems = markupProblems(html)
    check(`${name} (${mode}): остался текстом`, !problems.length, problems.join('; ') + ' :: ' + html)
  }
}

for (const [name, input, expect] of [
  ['блок кода', '```js\nconst a = 1\n```', /companion-code-wrap/],
  ['жирный', 'совсем **важно** тут', /<strong>важно<\/strong>/],
  ['врезка кода', 'вызов `go build` тут', /<code>go build<\/code>/],
  ['ссылка на файл', 'смотри main.go:42 там', /data-action="open-file"/],
  ['маркированный список', '- один\n- два', /<ul><li>один<\/li><li>два<\/li><\/ul>/],
  ['перенос строки в абзаце', 'первая\nвторая', /<p>первая<br>вторая<\/p>/],
  ['текст вокруг блока не потерян', 'до\n```\nкод\n```\nпосле', /до/],
  // Разбор блока считает языком всё до перевода строки: у однострочной тройной
  // кавычки языком становилось написанное, а телом — пустота, и на экране
  // оставалась пустая рамка с кнопкой «копировать код». Текст пропадал молча.
  ['однострочная тройная кавычка — врезка', 'Почини ```go build ./...``` в CI', /<code>go build \.\/\.\.\.<\/code>/],
  ['текст вокруг неё на месте', 'Почини ```go build``` в CI', /Почини .*в CI/],
  ['заголовок — на два уровня ниже страницы', '## План', /<h4>План<\/h4>/],
  ['таблица с выравниванием', '| a | b |\n| :-- | --: |\n| 1 | 2 |', /<th data-align="left">a<\/th><th data-align="right">b<\/th>/],
  ['ссылка', '[документация](https://example.org/a?b=1&c=2)', /<a class="companion-md-link" href="https:\/\/example\.org\/a\?b=1&amp;c=2"/],
  ['вложенный список', '1. раз\n   - два', /<ol><li>раз<ul><li>два<\/li><\/ul><\/li><\/ol>/],
  ['нумерация не сбрасывается пустой строкой', '1. раз\n\n2. два', /<ol><li>раз<\/li><li>два<\/li><\/ol>/],
]) {
  check(name + ': работает', expect.test(render(input)), render(input))
}

// Пустая рамка вместо написанного — отдельная проверка: блока здесь быть не должно.
check('однострочная кавычка не даёт пустой блок',
  !/companion-code-wrap/.test(render('Почини ```go build ./...``` в CI')),
  render('Почини ```go build ./...``` в CI'))

if (failures.length) {
  console.log('РАЗМЕТКА РЕПЛИК — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('реплика остаётся текстом: PASS')
