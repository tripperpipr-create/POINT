import { countOf } from './format-units.js'

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

