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
export function rowsOf(diff) {
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

