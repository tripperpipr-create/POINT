// Что разметчик реплик вправе выпустить — тег за тегом, вместе с атрибутами.
//
// Прежний список разрешал имя тега с любыми атрибутами: `<p onclick=…>` прошёл
// бы проверку как свой. Теперь форма каждого тега записана целиком, и всё, что
// в неё не укладывается, — чужое. Сверх списка держатся инварианты, которые
// проверяются по всему выводу разом: ни одного обработчика событий и стиля,
// адрес ссылки — только http(s), кавычки и угловые скобки внутри атрибутов
// запрещены, вложенность сбалансирована.
//
// Общий для двух проверок: smoke-chat-markup-escaping.js гоняет собранный
// вебвью, smoke-chat-markdown.mjs — исходник и фазз.

const ATTR = '[^"<>]*'
const OURS = [
  '</?(p|ul|ol|li|strong|em|del|code|pre|blockquote|table|thead|tbody|tr|h3|h4|h5|h6)>',
  '<br>', '<hr>', '</(a|div|span|button|th|td)>',
  '<ol start="\\d+">',
  '<li class="companion-md-task" data-checked="(true|false)">',
  '<(th|td)( data-align="(left|center|right)")?>',
  `<a class="companion-md-link" href="https?://${ATTR}" title="${ATTR}" rel="noopener noreferrer">`,
  '<span class="(companion-code-lang|hall-sr)">',
  '<span class="(companion-md-check|hall-stream-caret)" aria-hidden="true">',
  '<div class="(companion-code-wrap|companion-code-actions|companion-md-table)">',
  '<pre class="companion-code">',
  '<button type="button" class="companion-code-copy" data-action="copy-companion-code" title="Копировать код" aria-label="Копировать код">',
  // Значок копирования — постоянная разметка ui-icons.js, записанная целиком.
  '<svg class="hall-icon" viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1\\.25" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">',
  '<rect x="5\\.5" y="5\\.5" width="8" height="8" rx="1\\.5"/>',
  '<path d="M10\\.5 5\\.5V3\\.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2"/>',
  '</svg>',
  `<button type="button" class="secondary companion-open-file" data-action="open-file" data-path="${ATTR}" data-line="\\d*">`,
  `<button type="button" class="companion-file-link" data-action="open-file" data-path="${ATTR}" data-line="\\d+">`,
]
const OUR_TAG = new RegExp(`^(?:${OURS.join('|')})$`)
const VOID = new Set(['br', 'hr'])

function markupProblems(html) {
  const problems = []
  const stack = []
  for (const tag of String(html).match(/<[^>]*>?/g) || []) {
    if (!OUR_TAG.test(tag)) { problems.push(`чужой тег ${tag}`); continue }
    if (/\son\w+\s*=/i.test(tag) || /\sstyle\s*=/i.test(tag)) problems.push(`обработчик или стиль в ${tag}`)
    const name = tag.match(/^<\/?([a-z0-9]+)/)[1]
    if (VOID.has(name) || tag.endsWith('/>')) continue
    if (tag.startsWith('</')) {
      if (stack.pop() !== name) problems.push(`вложенность разорвана на ${tag}`)
    } else stack.push(name)
  }
  if (stack.length) problems.push(`не закрыты: ${stack.join(', ')}`)
  const text = String(html).replace(/<[^>]*>/g, '')
  if (/[<>]/.test(text)) problems.push('угловая скобка в тексте осталась неэкранированной')
  return problems
}

module.exports = { markupProblems }
