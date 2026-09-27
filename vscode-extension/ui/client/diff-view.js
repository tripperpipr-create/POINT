// Diff правки в ленте работы агента.
//
// Правка приходила сырым `<pre>`: белый моноширинный текст, в котором
// добавленное не отличалось от удалённого, а размер правки узнавали, только
// прочитав её целиком. Здесь у каждой строки свой тон (канон
// docs/RPG-DESIGN-SYSTEM.md: добавленное — --vital, удалённое — --wound), а
// у файла — счёт «+N −M», который виден и без раскрытия.
//
// Счёт считается из того diff, что пришёл: ядро строк не считает, а другого
// источника у вебвью нет. Нет diff — нет и счёта, а не ноль.
import { countOf } from './format-units.js'

function kindOf(line) {
  if (/^(?:diff |index |--- |\+\+\+ |new file|deleted file|similarity|rename )/.test(line)) return 'meta'
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

// Счёт правки одной строкой разметки. Пустой, если diff неизвестен.
export function diffCountHtml(stats) {
  if (!stats?.known) return ''
  return `<span class="quest-diff-count"><b class="is-add">+${stats.additions}</b><b class="is-del">−${stats.deletions}</b></span>`
}

// Тело diff. Каждая строка экранируется: diff — вывод агента, а не наша разметка.
// Длинный режется по хвосту с честным счётом остатка.
export function diffHtml(diff, esc, { limit = 400 } = {}) {
  const lines = String(diff || '').replace(/\s+$/, '').split(/\r?\n/)
  if (!lines.length || !lines[0]) return ''
  const shown = lines.slice(0, limit)
  const rest = lines.length - shown.length
  return `<div class="quest-diff">${shown.map(line => `<span class="quest-diff-line is-${kindOf(line)}">${esc(line) || ' '}</span>`).join('')}${rest > 0 ? `<span class="quest-diff-line is-meta">… и ещё ${countOf(rest, 'строка', 'строки', 'строк')}</span>` : ''}</div>`
}
