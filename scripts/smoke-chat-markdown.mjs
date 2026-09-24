// Разметка ответов: что разбирается, что остаётся текстом и чего разбор не
// выпустит ни при каком входе.
//
// Ответы Мастера и компаньона приходят из модели и частично из того, что
// написал человек, а поток разбирается десятки раз в секунду на недописанном
// тексте. Поэтому кроме списка конструкций здесь фазз: тысячи строк из
// «опасных» знаков и каждый префикс образца, и для всех — одни инварианты
// (scripts/lib/chat-markup.cjs) и предел времени на вызов.

import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { createCompanionMarkdownFormatter } from '../vscode-extension/ui/client/companion-markdown.js'
import { esc } from '../vscode-extension/ui/client/html-escape.js'

const { markupProblems } = createRequire(import.meta.url)('./lib/chat-markup.cjs')
const format = createCompanionMarkdownFormatter(esc)

const cases = [
  ['# Раз', /^<h3>Раз<\/h3>$/],
  ['#### Четыре', /^<h6>Четыре<\/h6>$/],
  ['#хэштег', /^<p>#хэштег<\/p>$/],
  ['---', /^<hr>$/],
  ['> цитата\n> **с** выделением', /^<blockquote><p>цитата<br><strong>с<\/strong> выделением<\/p><\/blockquote>$/],
  ['*курсив* и _тоже_', /<em>курсив<\/em> и <em>тоже<\/em>/],
  ['~~зачёркнуто~~', /<del>зачёркнуто<\/del>/],
  ['***всё сразу***', /<strong><em>всё сразу<\/em><\/strong>/],
  ['snake_case и имя_файла_тут', /^<p>snake_case и имя_файла_тут<\/p>$/],
  ['2 * 3 * 4', /^<p>2 \* 3 \* 4<\/p>$/],
  ['Шаги:\n- один\n- два', /^<p>Шаги:<\/p><ul><li>один<\/li><li>два<\/li><\/ul>$/],
  ['3. три\n4. четыре', /^<ol start="3"><li>три<\/li><li>четыре<\/li><\/ol>$/],
  ['- [x] сделано\n- [ ] нет', /data-checked="true">.*выполнено: <\/span>сделано.*data-checked="false">/],
  ['- раз\n  - два\n    - три', /<ul><li>раз<ul><li>два<ul><li>три<\/li><\/ul><\/li><\/ul><\/li><\/ul>/],
  ['| `a|b` | c |\n| --- | --- |\n| 1 | 2 |', /<th><code>a\|b<\/code><\/th><th>c<\/th>/],
  ['смотри https://example.org/путь.', /href="https:\/\/example\.org\/путь"[^>]*>https:\/\/example\.org\/путь<\/a>\.<\/p>$/u],
  ['[**жирная** ссылка](https://example.org)', /<a [^>]*><strong>жирная<\/strong> ссылка<\/a>/],
  ['файл main.go:42 и `код`', /data-path="main\.go" data-line="42".*<code>код<\/code>/],
  ['```\nнезакрытый блок', /<pre class="companion-code"><code>незакрытый блок<\/code><\/pre>/],
]
for (const [input, expected] of cases) {
  const html = format(input)
  assert.match(html, expected, `${JSON.stringify(input)} → ${html}`)
  assert.deepEqual(markupProblems(html), [], `${JSON.stringify(input)} → ${html}`)
}

// Память разбора: тот же ответ — та же строка, и память не растёт без края.
const answer = '## План\n- **раз**\n- два\n\n| a | b |\n| - | - |\n| 1 | 2 |'
assert.equal(format(answer), format(answer))
for (let i = 0; i < 1000; i++) format(`ответ ${i}`)
assert.equal(format(answer), format(answer), 'вытеснение из памяти не меняет разбор')

// Поток: курсор — в последнем блоке, на готовом тексте без курсора разбор тот же.
const stream = format.streaming
assert.match(stream('Текст **жирн'), /<strong>жирн<\/strong><span class="hall-stream-caret"[^>]*><\/span><\/p>$/)
assert.match(stream('код:\n```go\nfunc main'), /<code>func main<span class="hall-stream-caret"[^>]*><\/span><\/code>/)
assert.match(stream('- раз\n- '), /<li>раз<span class="hall-stream-caret"[^>]*><\/span><\/li><\/ul>$/, 'голый маркер не рисуется пустым пунктом')
assert.match(stream('читай [документацию](https://exa'), /читай документацию<span class="hall-stream-caret"/, 'недописанная ссылка — подписью')
const showcase = [
  '## План миграции', 'Сначала **сверю схему**, потом _перенесу_ ~~всё~~ и [доку](https://example.org).',
  '1. Снять дамп', '2. Применить:', '   - `0007.sql`', '', '- [x] Копия', '', '> Необратимо.', '',
  '| Таблица | Строк |', '| :--- | ---: |', '| users | 12 |', '', '---', '```go', 'func main() {}', '```', 'Готово.',
].join('\n')
assert.equal(stream(showcase, { caret: false }), format(showcase), 'готовый текст в потоке разбирается так же, как в ленте')
for (let end = 0; end <= showcase.length; end++) {
  const html = stream(showcase.slice(0, end))
  const problems = markupProblems(html)
  assert.deepEqual(problems, [], `префикс ${end}: ${problems.join('; ')} :: ${html}`)
  assert.equal((html.match(/hall-stream-caret/g) || []).length, 1, `префикс ${end}: курсор один`)
}

// Фазз: знаки разметки, кавычки, угловые скобки и адреса в случайном порядке.
let seed = 20260924
const random = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648 }
const atoms = ['*', '**', '_', '__', '~~', '`', '```', '[', ']', '(', ')', '|', '#', '> ', '- ', '1. ', '- [x] ', '\n', '\n\n', ' ',
  '<', '>', '"', "'", '&', 'https://a.b/c', 'javascript:alert(1)', 'data:x', 'текст', 'word', 'x.go:12', '\t', '\u0000', '', '\\|', '---']
let slowest = 0
for (let i = 0; i < 5000; i++) {
  let input = ''
  const length = 1 + Math.floor(random() * 40)
  for (let j = 0; j < length; j++) input += atoms[Math.floor(random() * atoms.length)]
  for (const render of [format, stream]) {
    const started = performance.now()
    const html = render(input)
    slowest = Math.max(slowest, performance.now() - started)
    const problems = markupProblems(html)
    assert.deepEqual(problems, [], `${JSON.stringify(input)} → ${problems.join('; ')} :: ${html}`)
  }
}
// Длинная строка без закрывающих знаков — квадратичный разбор выдал бы себя здесь.
// Первая — чуть короче предела разбора (4000 знаков в строке): выделение и
// ссылки в ней разбираются, и именно здесь спрятался бы квадратичный случай.
// Вторая — длиннее предела: её разбор обязан сразу сдаться до экранирования.
const long = ['*a _b ~~c **d [x]('.repeat(200), '*a '.repeat(3000) + '**b '.repeat(3000)].join('\n')
const started = performance.now()
format(long)
stream(long)
const longTime = performance.now() - started
assert.ok(longTime < 400, `длинная строка разбиралась ${Math.round(longTime)} мс`)
assert.ok(slowest < 50, `самый медленный вызов фазза — ${slowest.toFixed(1)} мс`)

console.log(`разметка ответов: PASS (фазз 5000×2, худший вызов ${slowest.toFixed(1)} мс, длинная строка ${Math.round(longTime)} мс)`)
