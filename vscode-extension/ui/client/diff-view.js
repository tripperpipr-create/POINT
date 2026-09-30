// Diff правки — одна отрисовка на весь Хаб.
//
// Правка приходила сырым `<pre>`: белый моноширинный текст, в котором
// добавленное не отличалось от удалённого, а размер правки узнавали, только
// прочитав её целиком. Потом у строк появился тон, но заголовки `---`/`+++`,
// строки `@@` и знаки `+` оставались в тексте, а новый файл выглядел стеной
// зелёных плюсов.
//
// Облик выбрал владелец по снимкам стенда 30 сентября 2026 (вариант A, как в
// GitHub): две колонки номеров «было / стало», знак отдельной колонкой, тон
// строки заливкой, фрагмент — тихой строкой «Строки 41–53» с подписью функции.
// Новый файл — код с номерами без плюсов и бейдж «Новый файл» в заголовке;
// удалённый — то же с бейджем «Удалён».
//
// Счёт считается из того diff, что пришёл: ядро строк не считает, а другого
// источника у вебвью нет. Нет diff — нет и счёта, а не ноль.
import { countOf } from './format-units.js'

const META = /^(?:diff |index |--- |\+\+\+ |new file|deleted file|similarity|rename |old mode|new mode|\\ No newline)/
const HUNK = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$/

function kindOf(line) {
  if (META.test(line)) return 'meta'
  if (line.startsWith('@@')) return 'hunk'
  if (line.startsWith('+')) return 'add'
  if (line.startsWith('-')) return 'del'
  return 'ctx'
}

// `created` — файл появился этой правкой, `deleted` — исчез. Узнаётся по
// заголовку diff, а не по статусу правки: статус говорит о решении, не о файле.
export function diffStats(diff) {
  const text = String(diff || '')
  const stats = { additions: 0, deletions: 0, created: /^--- \/dev\/null/m.test(text), deleted: /^\+\+\+ \/dev\/null/m.test(text), known: Boolean(text.trim()) }
  for (const line of text.split(/\r?\n/)) {
    const kind = kindOf(line)
    if (kind === 'add') stats.additions += 1
    else if (kind === 'del') stats.deletions += 1
  }
  return stats
}

// Полоска объёма из пяти клеток: доля добавленного к удалённому, как в GitHub.
function diffBarHtml(stats) {
  const total = stats.additions + stats.deletions
  if (!total) return ''
  const added = Math.round(stats.additions / total * 5)
  return `<span class="diff-bar" aria-hidden="true">${'<i class="is-add"></i>'.repeat(added)}${'<i class="is-del"></i>'.repeat(5 - added)}</span>`
}

// Счёт правки одной строкой разметки. Пустой, если diff неизвестен. Новый и
// удалённый файл называются словом: «+12 −0» у нового файла ничего не говорит.
export function diffCountHtml(stats) {
  if (!stats?.known) return ''
  if (stats.created) return `<span class="diff-badge is-new" title="${countOf(stats.additions, 'строка', 'строки', 'строк')}">Новый файл</span>`
  if (stats.deleted) return `<span class="diff-badge is-gone" title="${countOf(stats.deletions, 'строка', 'строки', 'строк')}">Удалён</span>`
  return `<span class="quest-diff-count"><b class="is-add">+${stats.additions}</b><b class="is-del">−${stats.deletions}</b>${diffBarHtml(stats)}</span>`
}

// Путь файла: папка тише, имя — ярче. Полный путь остаётся в подсказке.
export function diffPathHtml(path, esc) {
  const value = String(path || '')
  const cut = value.lastIndexOf('/') + 1
  return `<span class="diff-dir">${esc(value.slice(0, cut))}</span>${esc(value.slice(cut))}`
}

function rowsOf(diff) {
  const rows = []
  let before = 1
  let after = 1
  for (const line of String(diff || '').replace(/\s+$/, '').split(/\r?\n/)) {
    const kind = kindOf(line)
    if (kind === 'meta') continue
    if (kind === 'hunk') {
      const match = line.match(HUNK)
      if (!match) continue
      before = Number(match[1])
      after = Number(match[3])
      const size = match[4] == null ? 1 : Number(match[4])
      rows.push({ kind, from: after, to: after + Math.max(size, 1) - 1, text: match[5] || '' })
      continue
    }
    const text = kind === 'ctx' && !/^ /.test(line) ? line : line.slice(1)
    if (kind === 'add') rows.push({ kind, after: after++, text })
    else if (kind === 'del') rows.push({ kind, before: before++, text })
    else rows.push({ kind, before: before++, after: after++, text })
  }
  return rows
}

const SIGN = { add: '+', del: '−', ctx: '' }

// Тело diff. Каждая строка экранируется: diff — вывод агента, а не наша разметка.
// Длинный режется по хвосту с честным счётом остатка.
export function diffHtml(diff, esc, { limit = 400 } = {}) {
  const stats = diffStats(diff)
  const rows = rowsOf(diff)
  if (!rows.length) return ''
  const whole = stats.created || stats.deleted
  const lines = whole ? rows.filter(row => row.kind !== 'hunk') : rows
  const shown = lines.slice(0, limit)
  const rest = lines.length - shown.length
  const code = text => `<code>${esc(text) || ' '}</code>`
  const body = shown.map(row => {
    if (whole) return `<div class="diff-row"><span class="diff-n">${stats.created ? row.after : row.before}</span>${code(row.text)}</div>`
    if (row.kind === 'hunk') return `<div class="diff-hunk"><span>${row.from === row.to ? `Строка ${row.from}` : `Строки ${row.from}–${row.to}`}</span>${row.text ? code(row.text) : ''}</div>`
    return `<div class="diff-row is-${row.kind}"><span class="diff-n">${row.before ?? ''}</span><span class="diff-n">${row.after ?? ''}</span><span class="diff-s">${SIGN[row.kind]}</span>${code(row.text)}</div>`
  }).join('')
  const more = rest > 0 ? `<div class="diff-more">… и ещё ${countOf(rest, 'строка', 'строки', 'строк')}</div>` : ''
  return `<div class="diff${whole ? ` is-whole ${stats.created ? 'is-new' : 'is-gone'}` : ''}">${body}${more}</div>`
}
