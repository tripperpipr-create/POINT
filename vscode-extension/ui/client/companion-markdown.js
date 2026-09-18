// Security-sensitive renderer for model/user text inside the Companion and
// Master surfaces. The caller supplies the sole HTML escaper.
export function createCompanionMarkdownFormatter(escapeHtml) {
  const esc = escapeHtml
  return function formatCompanionMarkdown(text) {
    // A single-line triple quote is inline code, not an empty fenced block.
    const source = String(text || '').replace(/```([^\n`]+)```/g, (_, code) => '`' + code + '`')
    if (!source) return ''
    const blocks = []
    const withFences = source.replace(/```([^\n`]*)\n?([\s\S]*?)```/g, (_, lang, code) => {
      const index = blocks.length
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
      blocks.push(`<div class="companion-code-wrap"><div class="companion-code-actions">${label}${open}<button type="button" class="secondary" data-action="copy-companion-code" title="Копировать блок кода">Копировать код</button></div><pre class="companion-code"><code>${esc(body)}</code></pre></div>`)
      return `\u0000BLOCK${index}\u0000`
    })
    const formatInline = (value) => {
      const parts = []
      const tokenized = String(value || '').replace(/`([^`\n]+)`/g, (_, code) => {
        const i = parts.length
        parts.push(`<code>${esc(code)}</code>`)
        return `\u0000INL${i}\u0000`
      }).replace(/\*\*([^*]+)\*\*/g, (_, bold) => {
        const i = parts.length
        parts.push(`<strong>${esc(bold)}</strong>`)
        return `\u0000INL${i}\u0000`
      }).replace(/(^|[\s(])([\w./\\-]+\.\w{1,12}):(\d+)\b/g, (_, prefix, path, line) => {
        const i = parts.length
        parts.push(`${prefix}<button type="button" class="companion-file-link" data-action="open-file" data-path="${esc(path)}" data-line="${esc(line)}">${esc(path)}:${esc(line)}</button>`)
        return `\u0000INL${i}\u0000`
      })
      return esc(tokenized).replace(/\u0000INL(\d+)\u0000/g, (_, index) => parts[Number(index)] || '')
    }
    return withFences.split(/\n{2,}/).map(block => {
      const trimmed = block.trim()
      if (!trimmed) return ''
      const fence = trimmed.match(/^\u0000BLOCK(\d+)\u0000$/)
      if (fence) return blocks[Number(fence[1])] || ''
      const lines = trimmed.split('\n')
      if (lines.every(line => /^\s*[-*]\s+/.test(line))) {
        return `<ul>${lines.map(line => `<li>${formatInline(line.replace(/^\s*[-*]\s+/, ''))}</li>`).join('')}</ul>`
      }
      if (lines.every(line => /^\s*\d+\.\s+/.test(line))) {
        return `<ol>${lines.map(line => `<li>${formatInline(line.replace(/^\s*\d+\.\s+/, ''))}</li>`).join('')}</ol>`
      }
      const restored = lines.map(formatInline).join('<br>')
        .replace(/\u0000BLOCK(\d+)\u0000/g, (_, index) => blocks[Number(index)] || '')
      if (trimmed.includes('\u0000BLOCK')) return restored
      return `<p>${restored}</p>`
    }).join('')
  }
}
