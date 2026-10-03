// Build a patch from selected changes, retaining unselected deletions as context.
function parsePatch(text) {
  if (/GIT binary patch|Binary files|^rename (from|to)/m.test(text)) throw new Error('Двоичные файлы и переименования готовятся целиком.')
  const lines = String(text).replace(/\r\n/g, '\n').split('\n')
  const header = []; const hunks = []; let hunk
  for (const line of lines) {
    const match = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$/.exec(line)
    if (match) {
      hunk = { old: Number(match[1]), oldCount: Number(match[2] ?? 1),
        next: Number(match[3]), newCount: Number(match[4] ?? 1), context: match[5], lines: [] }
      hunks.push(hunk)
    } else if (hunk && /^[ +\\-]/.test(line)) hunk.lines.push(line)
    else if (!hunk) header.push(line)
  }
  return { header, hunks }
}
function reversePatch(parsed) {
  return { header: parsed.header.map(line => line.startsWith('--- ') ? '--- ' + (parsed.header.find(x=>x.startsWith('+++ '))||'+++ /dev/null').slice(4)
    : line.startsWith('new file mode ') ? 'deleted file mode '+line.slice(14)
    : line.startsWith('deleted file mode ') ? 'new file mode '+line.slice(18)
    : line.startsWith('+++ ') ? '+++ ' + (parsed.header.find(x=>x.startsWith('--- '))||'--- /dev/null').slice(4) : line),
    hunks: parsed.hunks.map(h => ({ ...h, old: h.next, oldCount: h.newCount, next: h.old, newCount: h.oldCount,
      lines: h.lines.map(line => line[0] === '+' ? '-' + line.slice(1) : line[0] === '-' ? '+' + line.slice(1) : line) })) }
}
function selectedPatch(parsed, selected) {
  let header=[...parsed.header]
  const incompleteDeletion=header.includes('+++ /dev/null')&&parsed.hunks.some((h,hi)=>h.lines.some((l,li)=>l[0]==='-'&&!selected.has(hi+':'+li)))
  if(incompleteDeletion) {
    const old=header.find(l=>l.startsWith('--- ')).slice(4).replace(/^(\"?)a\//,'$1b/')
    header=header.filter(l=>!l.startsWith('deleted file mode ')&&!l.startsWith('index ')).map(l=>l==='+++ /dev/null'?'+++ '+old:l)
  }
  const output = [...header]; let offset = 0
  parsed.hunks.forEach((h, hi) => {
    let changed = false; const lines = []
    h.lines.forEach((line, li) => {
      if (line[0] === '+' || line[0] === '-') {
        if (selected.has(hi + ':' + li)) { lines.push(line); changed = true }
        else if (line[0] === '-') lines.push(' ' + line.slice(1))
      } else lines.push(line)
    })
    if (!changed) return
    const oldCount = lines.filter(line => /^[ -]/.test(line)).length
    const newCount = lines.filter(line => /^[ +]/.test(line)).length
    const newStart = oldCount === 0 ? h.old + offset + 1 : newCount === 0 ? Math.max(0, h.old + offset - 1) : h.old + offset
    output.push('@@ -' + h.old + ',' + oldCount + ' +' + newStart + ',' + newCount + ' @@' + h.context, ...lines)
    offset += newCount - oldCount
  })
  return output.join('\n') + '\n'
}
async function chooseIndexPatch(vscode, text, reverse, individual) {
  const parsed = reverse ? reversePatch(parsePatch(text)) : parsePatch(text)
  const items = []
  parsed.hunks.forEach((h, hi) => {
    if (individual) h.lines.forEach((line, li) => {
      if (/^[+-]/.test(line)) items.push({ label: line.slice(0, 160), description: 'блок ' + (hi + 1),
        keys: [hi + ':' + li], picked: false })
    })
    else items.push({ label: 'Блок ' + (hi + 1) + ' · строка ' + h.old,
      description: h.lines.filter(line => /^[+-]/.test(line)).map(line => line.slice(0, 60)).join(' · '),
      keys: h.lines.map((line, li) => /^[+-]/.test(line) ? hi + ':' + li : '').filter(Boolean) })
  })
  const chosen = await vscode.window.showQuickPick(items, { canPickMany: true,
    title: reverse ? 'Снять подготовку изменений' : 'Подготовить изменения', placeHolder: 'Выберите изменения для index' })
  if (!chosen?.length) return ''
  return selectedPatch(parsed, new Set(chosen.flatMap(item => item.keys)))
}
module.exports = { parsePatch, reversePatch, selectedPatch, chooseIndexPatch }
