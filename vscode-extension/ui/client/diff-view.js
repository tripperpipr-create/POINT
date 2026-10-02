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
import { diffStats, rowsOf } from './diff-model.js'
export { diffStats }
export { diffCountHtml, diffPathHtml } from './diff-summary.js'

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
