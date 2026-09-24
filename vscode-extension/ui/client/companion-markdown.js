// Security-sensitive renderer for model/user text inside the Companion and
// Master surfaces. The caller supplies the sole HTML escaper.
//
// Разбор построчный: блоки (заголовок, цитата, список, таблица, абзац) узнаются
// по началу строки, а строчная разметка внутри них — заглушками. Каждая
// узнанная конструкция уходит в общий массив готового HTML, в тексте остаётся
// метка `\0N\0`, а весь остальной текст проходит через `esc` ровно один раз —
// в самом конце. Метки нельзя подделать: оба служебных знака вычищаются со
// входа до разбора. Прежний разбор резал текст по пустым строкам и терял всё,
// кроме абзацев и плоских списков: заголовки, таблицы и цитаты модели стояли
// в ленте сырыми решётками и чертами, хотя оформление для них было написано.

import { icon } from './ui-icons.js'

const MARK = /\u0000(\d+)\u0000/g
const HAS_MARK = /\u0000\d+\u0000/
// Курсор потока. Ставится разбором в конец текста и заменяется разметкой только
// в текстовых отрезках вывода: внутри атрибута (например, адреса ссылки,
// которую модель ещё дописывает) он просто пропадает.
const CARET = '\uE000'
// Выше этих длин строчная разметка не разбирается, текст только экранируется:
// строка в сотни килобайт без закрывающих знаков делала бы регулярные
// выражения квадратичными прямо на каждом кадре потока.
const INLINE_LIMIT = 4000
const MEMO_LIMIT = 400
const TABLE_COLUMNS = 50
const TABLE_ROWS = 500

const URL_OK = /^https?:\/\/[\p{L}\p{N}\-._~:/?#[\]@!$&()*+,;=%]+$/iu
const LIST_ITEM = /^( *)([-*+]|\d{1,3}[.)])(?: +(.*)|$)/
const HEADING = /^ {0,3}(#{1,4}) +(.*?)(?: +#+)? *$/
const RULE = /^ {0,3}([-*_])(?: *\1){2,} *$/
const QUOTE = /^ {0,3}> ?(.*)$/
const BLOCK_TOKEN = /^\u0000B(\d+)\u0000$/
const TABLE_DELIMITER = /^ *\|? *:?-+:? *(\| *:?-+:? *)*\|? *$/

const indentOf = line => line.length - line.trimStart().length

export function createCompanionMarkdownFormatter(escapeHtml) {
  const esc = escapeHtml
  const memo = new Map()

  function render(input, { caret = false } = {}) {
    const parts = []
    const put = html => { parts.push(html); return `\u0000${parts.length - 1}\u0000` }
    // A single-line triple quote is inline code, not an empty fenced block.
    let source = String(input || '').replace(/[\u0000\uE000]/g, '').replace(/\r\n?/g, '\n')
      .replace(/```([^\n`]+)```/g, (_, code) => '`' + code + '`')
    if (!source.trim() && !caret) return ''
    // Курсор ставится до разбора блоков кода: если модель ещё пишет код, он
    // окажется внутри блока, в конце последней строки.
    if (caret) source = source.trimEnd() + CARET
    const blocks = []
    const codeBlock = (lang, code) => {
      const language = String(lang || '').trim()
      const body = String(code || '').replace(/\n$/, '')
      const pathMatch = language.match(/^([\w./\\-]+\.\w{1,12})(?::(\d+))?$/)
        || body.match(/^\/\/\s*([\w./\\-]+\.\w{1,12})(?::(\d+))?/)
      let open = ''
      if (pathMatch) {
        const path = pathMatch[1]
        const line = pathMatch[2] || ''
        open = `<button type="button" class="secondary companion-open-file" data-action="open-file" data-path="${esc(path)}" data-line="${esc(line)}">Открыть ${esc(path)}${line ? `:${esc(line)}` : ''}</button>`
      }
      // Язык блока модель называет сама, а шапка его теряла: над кодом висела
      // полоса с одной кнопкой, и чем этот блок является, читалось только из
      // самого кода. Подпись встаёт слева — там, где её ищут.
      const label = !open && language && /^[\w+#.-]{1,20}$/.test(language)
        ? `<span class="companion-code-lang">${esc(language)}</span>`
        : ''
      // Копирование — значок, а не слово: шапка блока не полоса с кнопкой, а
      // подпись языка и тихий знак справа, как у реплики (выбор владельца по
      // снимкам стенда, 24 сентября 2026).
      blocks.push(`<div class="companion-code-wrap"><div class="companion-code-actions">${label}${open}<button type="button" class="companion-code-copy" data-action="copy-companion-code" title="Копировать код" aria-label="Копировать код">${icon('copy')}</button></div><pre class="companion-code"><code>${esc(body)}</code></pre></div>`)
      return `\n\n\u0000B${blocks.length - 1}\u0000\n\n`
    }
    // Закрытые блоки кода вынимаются из текста целиком, где бы ни стояли, — в
    // том числе посреди строки: «ждёт ```js … ``` решения». Незакрытый блок у
    // начала строки идёт до конца текста: так его покажет и CommonMark, и так
    // выглядит блок, который модель ещё дописывает.
    source = source.replace(/```([^\n`]*)\n?([\s\S]*?)```/g, (_, lang, code) => codeBlock(lang, code))
    source = source.replace(/(^|\n)```([^\n`]*)(?:\n([\s\S]*))?$/, (_, lead, lang, code) => lead + codeBlock(lang, code || ''))

    // ——— Строчная разметка ———
    function inline(text, depth = 0, links = true) {
      let out = String(text)
      out = out.replace(/`([^`\n]+)`/g, (_, code) => put(`<code>${esc(code)}</code>`))
      if (out.length > INLINE_LIMIT || depth > 3) return out
      if (links) {
        out = out.replace(/\[([^\]\n]{1,400})\]\(\s*(https?:\/\/[^\s)<>"'`]+)\s*(?:"[^"\n]*"\s*)?\)/giu, (whole, label, url) =>
          URL_OK.test(url) ? put(link(url, esc(inline(label, depth + 1, false)))) : whole)
        out = out.replace(/(^|[\s(«"'])(https?:\/\/[^\s<>"'`\u0000]+)/giu, (whole, lead, found) => {
          // Курсор потока в хвосте адреса — не часть адреса: без этого ссылка,
          // которую модель ещё печатает, до последней буквы оставалась бы текстом.
          const trimmed = found.replace(/+$/, '').replace(/[.,;:!?»]+$/, '')
          const url = /\)$/.test(trimmed) && !trimmed.includes('(') ? trimmed.slice(0, -1) : trimmed
          if (!URL_OK.test(url)) return whole
          return lead + put(link(url, esc(url))) + found.slice(url.length)
        })
      }
      out = out.replace(/(^|[\s(])([\w./\\-]+\.\w{1,12}):(\d+)\b/g, (_, lead, path, line) =>
        `${lead}${put(`<button type="button" class="companion-file-link" data-action="open-file" data-path="${esc(path)}" data-line="${esc(line)}">${esc(path)}:${esc(line)}</button>`)}`)
      const wrap = (tag, inner) => put(`<${tag}>${esc(inline(inner, depth + 1, links))}</${tag}>`)
      out = out.replace(/~~(?=\S)(.+?)(?<=\S)~~/g, (_, inner) => wrap('del', inner))
      out = out.replace(/\*\*\*(?=\S)(.+?)(?<=\S)\*\*\*/g, (_, inner) => put(`<strong><em>${esc(inline(inner, depth + 1, links))}</em></strong>`))
      out = out.replace(/\*\*(?=\S)(.+?)(?<=\S)\*\*/g, (_, inner) => wrap('strong', inner))
      out = out.replace(/(?<![\p{L}\p{N}_])__(?=\S)(.+?)(?<=\S)__(?![\p{L}\p{N}_])/gu, (_, inner) => wrap('strong', inner))
      // Одиночная звёздочка — курсив, только если сразу за ней не пробел:
      // `2 * 3 * 4` остаётся арифметикой. Подчёркивание — только на границе
      // слова, иначе `snake_case` и `имя_файла` разваливались бы на курсив.
      out = out.replace(/(?<![*\p{L}\p{N}])\*(?=[^\s*])([^*\n]+?)(?<=\S)\*(?![*\p{L}\p{N}])/gu, (_, inner) => wrap('em', inner))
      out = out.replace(/(?<![\p{L}\p{N}_])_(?=[^\s_])([^_\n]+?)(?<=\S)_(?![\p{L}\p{N}_])/gu, (_, inner) => wrap('em', inner))
      return out
    }
    function link(url, label) {
      // Подсказка называет настоящий адрес: подпись ссылки пишет модель, и
      // «документация» может вести куда угодно.
      return `<a class="companion-md-link" href="${esc(url)}" title="${esc(url)}" rel="noopener noreferrer">${label}</a>`
    }
    const inlineHtml = text => esc(inline(text))

    // ——— Блоки ———
    const startsBlock = (line, next) => BLOCK_TOKEN.test(line) || HEADING.test(line) || RULE.test(line)
      || QUOTE.test(line) || LIST_ITEM.test(line) || isTableStart(line, next)
    function isTableStart(line, next) {
      if (!line.includes('|') || next == null || !TABLE_DELIMITER.test(next) || !next.includes('-')) return false
      return cells(line).length === cells(next).length
    }
    function cells(line) {
      const trimmed = line.trim().replace(/^\|/, '').replace(/(?<!\\)\|$/, '')
      const found = []
      let current = ''
      let code = false
      for (let i = 0; i < trimmed.length; i++) {
        const char = trimmed[i]
        if (char === '\\' && trimmed[i + 1] === '|') { current += '|'; i++; continue }
        if (char === '`') code = !code
        if (char === '|' && !code) { found.push(current.trim()); current = ''; continue }
        current += char
      }
      found.push(current.trim())
      return found
    }
    function table(lines, at) {
      const header = cells(lines[at]).slice(0, TABLE_COLUMNS)
      const align = cells(lines[at + 1]).map(cell => cell.startsWith(':') && cell.endsWith(':') ? 'center' : cell.endsWith(':') ? 'right' : cell.startsWith(':') ? 'left' : '')
      const cell = (tag, text, index) => `<${tag}${align[index] ? ` data-align="${align[index]}"` : ''}>${inlineHtml(text)}</${tag}>`
      const rows = []
      let i = at + 2
      while (i < lines.length && lines[i].includes('|') && lines[i].trim() && rows.length < TABLE_ROWS) {
        const row = cells(lines[i])
        rows.push(`<tr>${header.map((_, index) => cell('td', row[index] || '', index)).join('')}</tr>`)
        i++
      }
      const html = `<div class="companion-md-table"><table><thead><tr>${header.map((text, index) => cell('th', text, index)).join('')}</tr></thead>${rows.length ? `<tbody>${rows.join('')}</tbody>` : ''}</table></div>`
      return { html, next: i }
    }
    function list(lines, at, depth) {
      const first = lines[at].match(LIST_ITEM)
      const base = first[1].length
      const ordered = /\d/.test(first[2])
      const items = []
      let i = at
      while (i < lines.length) {
        const match = lines[i].match(LIST_ITEM)
        if (!match || match[1].length !== base || /\d/.test(match[2]) !== ordered) break
        const content = base + match[2].length + 1
        const sub = []
        let head = match[3] || ''
        i++
        while (i < lines.length) {
          const line = lines[i]
          if (!line.trim()) {
            // Пустая строка список не рвёт, если за ней идёт продолжение того же
            // пункта или следующий пункт того же уровня: «1.\n\n2.» — один список,
            // а не два, начинающих нумерацию заново.
            let j = i
            while (j < lines.length && !lines[j].trim()) j++
            const after = lines[j]
            const again = after?.match(LIST_ITEM)
            if (after != null && (indentOf(after) > base || (again && again[1].length === base && /\d/.test(again[2]) === ordered))) {
              if (indentOf(after) > base) sub.push('')
              i = j
              continue
            }
            break
          }
          if (indentOf(line) > base) { sub.push(line.slice(Math.min(indentOf(line), content))); i++; continue }
          // Ленивое продолжение: строка без отступа, которая не начинает свой
          // блок, дописывает текст пункта.
          if (!sub.length && !startsBlock(line, lines[i + 1])) { head += '\n' + line; i++; continue }
          break
        }
        items.push({ head, sub })
      }
      const start = ordered ? Number.parseInt(first[2], 10) : 1
      const tag = ordered ? 'ol' : 'ul'
      const html = items.map(({ head, sub }) => {
        const task = head.match(/^\[([ xX])\]\s+([\s\S]*)$/)
        const text = (task ? task[2] : head).split('\n').map(inlineHtml).join('<br>')
        const nested = sub.length ? blocksHtml(sub, depth + 1) : ''
        if (!task) return `<li>${text}${nested}</li>`
        const done = task[1] !== ' '
        return `<li class="companion-md-task" data-checked="${done}"><span class="companion-md-check" aria-hidden="true"></span><span class="hall-sr">${done ? 'выполнено: ' : 'не выполнено: '}</span>${text}${nested}</li>`
      }).join('')
      return { html: `<${tag}${ordered && start !== 1 ? ` start="${start}"` : ''}>${html}</${tag}>`, next: i }
    }
    function blocksHtml(lines, depth = 0) {
      const out = []
      let i = 0
      while (i < lines.length) {
        const line = lines[i]
        if (!line.trim()) { i++; continue }
        const token = line.trim().match(BLOCK_TOKEN)
        if (token) { out.push(blocks[Number(token[1])] || ''); i++; continue }
        const heading = line.match(HEADING)
        if (heading) {
          // Заголовок ответа — на два уровня ниже: лента стоит под заголовками
          // самой страницы, и навигация читалки по ним не должна принимать
          // реплику за раздел верхнего уровня.
          out.push(`<h${heading[1].length + 2}>${inlineHtml(heading[2])}</h${heading[1].length + 2}>`)
          i++
          continue
        }
        if (RULE.test(line)) { out.push('<hr>'); i++; continue }
        if (QUOTE.test(line)) {
          const inner = []
          while (i < lines.length && QUOTE.test(lines[i])) inner.push(lines[i++].match(QUOTE)[1])
          out.push(`<blockquote>${depth < 2 ? blocksHtml(inner, depth + 1) : `<p>${inner.map(inlineHtml).join('<br>')}</p>`}</blockquote>`)
          continue
        }
        if (isTableStart(line, lines[i + 1])) { const found = table(lines, i); out.push(found.html); i = found.next; continue }
        if (LIST_ITEM.test(line) && depth < 6) { const found = list(lines, i, depth); out.push(found.html); i = found.next; continue }
        const paragraph = [line]
        i++
        while (i < lines.length && lines[i].trim() && !startsBlock(lines[i], lines[i + 1])) paragraph.push(lines[i++])
        out.push(`<p>${paragraph.map(text => inlineHtml(text.trim())).join('<br>')}</p>`)
      }
      return out.join('')
    }

    const lines = source.split('\n').map(line => line.replace(/^\t+/, run => '    '.repeat(run.length)))
    let html = blocksHtml(lines)
    for (let pass = 0; pass < 8 && HAS_MARK.test(html); pass++) html = html.replace(MARK, (_, index) => parts[Number(index)] || '')
    return html
  }

  // Готовый ответ разбирается один раз: лента пересобирается на каждое событие
  // хода, и без памяти разбор всей истории повторялся бы десятки раз за ход.
  function formatCompanionMarkdown(text) {
    const key = String(text || '')
    if (memo.has(key)) {
      const html = memo.get(key)
      memo.delete(key)
      memo.set(key, html)
      return html
    }
    const html = render(key)
    memo.set(key, html)
    if (memo.size > MEMO_LIMIT) memo.delete(memo.keys().next().value)
    return html
  }

  // Потоковый вариант того же разбора: незакрытые `**`, `` ` `` и `~~` в хвосте
  // закрываются, чтобы недописанная пара не показывалась звёздочками, а курсор
  // встаёт в конец последнего блока. Без курсора вывод совпадает с готовым.
  formatCompanionMarkdown.streaming = function formatStreamingMarkdown(text, { caret = true } = {}) {
    let source = String(text || '')
    const tail = source.slice(source.lastIndexOf('\n') + 1)
    const fenceOpen = (source.match(/(^|\n)```/g) || []).length % 2 === 1
    if (!fenceOpen) {
      for (const mark of ['**', '~~']) if ((tail.split(mark).length - 1) % 2 === 1) source += mark
      if ((tail.split('`').length - 1) % 2 === 1) source += '`'
      // Голый маркер в последней строке — начало блока, которому ещё нечего
      // показать: «- » или «## » без текста не выводятся пустым пунктом.
      source = source.replace(/(^|\n) *(?:[-*+]|\d{1,3}[.)]|#{1,4}|>) *$/, '$1')
      // Недописанная ссылка показывается своей подписью: «[документация](https://po»
      // иначе мелькало бы сырыми скобками и превращалось в ссылку скачком.
      source = source.replace(/\[([^\]\n]*)\]\([^)\s]*$/, '$1')
    }
    const html = render(source, { caret })
    const caretHtml = '<span class="hall-stream-caret" aria-hidden="true"></span>'
    if (!caret) return html
    let placed = false
    const out = html.split(/(<[^>]*>)/).map(piece => {
      if (piece.startsWith('<')) return piece.replace(/\uE000/g, '')
      return piece.replace(/\uE000/g, () => { placed = true; return caretHtml })
    }).join('')
    return placed ? out : out + caretHtml
  }

  return formatCompanionMarkdown
}
