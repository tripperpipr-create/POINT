const vscode = require('vscode')

// Stateless navigation/search contracts are isolated from extension activation.
function symbolIcon(kind) {
  const icons = {
    [vscode.SymbolKind.Class]: 'symbol-class',
    [vscode.SymbolKind.Interface]: 'symbol-interface',
    [vscode.SymbolKind.Struct]: 'symbol-struct',
    [vscode.SymbolKind.Enum]: 'symbol-enum',
    [vscode.SymbolKind.Function]: 'symbol-function',
    [vscode.SymbolKind.Method]: 'symbol-method',
    [vscode.SymbolKind.Variable]: 'symbol-variable',
    [vscode.SymbolKind.Constant]: 'symbol-constant',
    [vscode.SymbolKind.Constructor]: 'symbol-constructor',
    [vscode.SymbolKind.Namespace]: 'symbol-namespace',
  }
  return icons[kind] || 'symbol-misc'
}

function fuzzyScore(value, query) {
  if (!query) return 3
  const text = value.toLocaleLowerCase()
  const needle = query.toLocaleLowerCase()
  if (text === needle) return 0
  if (text.startsWith(needle)) return 1
  const direct = text.indexOf(needle)
  if (direct >= 0) return 2 + direct / Math.max(text.length, 1)
  let cursor = 0
  for (const character of needle) {
    cursor = text.indexOf(character, cursor)
    if (cursor < 0) return Number.POSITIVE_INFINITY
    cursor += 1
  }
  return 4 + cursor / Math.max(text.length, 1)
}

function parseDocumentOutline(text, languageId) {
  const lines = String(text || '').split(/\r?\n/)
  const lang = String(languageId || '').toLowerCase()
  const items = []
  const push = (name, line, kind, indent = 0) => {
    const label = String(name || '').trim()
    if (!label || label.length > 160) return
    items.push({ name: label, line, kind, indent })
  }
  const jsLike = lang === 'javascript' || lang === 'typescript' || lang === 'javascriptreact' || lang === 'typescriptreact'
  const go = lang === 'go'
  const py = lang === 'python'
  const md = lang === 'markdown'
  const rust = lang === 'rust'
  const jvm = lang === 'java' || lang === 'kotlin' || lang === 'scala'
  const csharp = lang === 'csharp' || lang === 'c' || lang === 'cpp'
  const json = lang === 'json' || lang === 'jsonc'
  const css = lang === 'css' || lang === 'scss' || lang === 'less'
  for (let i = 0; i < lines.length && items.length < 400; i += 1) {
    const raw = lines[i]
    const trimmed = raw.trim()
    if (!trimmed) continue
    if (go) {
      let match
      if ((match = /^func\s+(?:\(([^)]*)\)\s*)?(\w+)\s*\(/.exec(trimmed))) {
        push(match[2], i, match[1] ? 'method' : 'function', match[1] ? 1 : 0)
      } else if ((match = /^type\s+(\w+)\s+(struct|interface)\b/.exec(trimmed))) {
        push(match[1], i, match[2] === 'interface' ? 'interface' : 'class')
      } else if ((match = /^type\s+(\w+)\s+/.exec(trimmed))) {
        push(match[1], i, 'type')
      } else if ((match = /^(?:var|const)\s+(\w+)\b/.exec(trimmed))) {
        push(match[1], i, trimmed.startsWith('const') ? 'constant' : 'variable')
      }
      continue
    }
    if (jsLike) {
      if (trimmed.startsWith('//') || trimmed.startsWith('*') || trimmed.startsWith('/*')) continue
      let match
      if ((match = /^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+(\w+)/.exec(trimmed))) push(match[1], i, 'function')
      else if ((match = /^(?:export\s+)?(?:default\s+)?class\s+(\w+)/.exec(trimmed))) push(match[1], i, 'class')
      else if ((match = /^(?:export\s+)?(?:async\s+)?(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_]\w*)\s*=>/.exec(trimmed))) push(match[1], i, 'function')
      else if ((match = /^(?:export\s+)?(?:interface|type|enum)\s+(\w+)/.exec(trimmed))) push(match[1], i, trimmed.includes('interface') ? 'interface' : 'type')
      else if ((match = /^(?:public|private|protected|async)?\s*(\w+)\s*\([^)]*\)\s*\{/.exec(trimmed))) {
        if (!['if', 'for', 'while', 'switch', 'catch', 'function'].includes(match[1])) push(match[1], i, 'method', 1)
      }
      continue
    }
    if (py) {
      if (trimmed.startsWith('#')) continue
      const indent = Math.min(6, Math.floor((raw.length - raw.trimStart().length) / 4))
      let match
      if ((match = /^(?:async\s+)?def\s+(\w+)\s*\(/.exec(trimmed))) push(match[1], i, indent > 0 ? 'method' : 'function', indent)
      else if ((match = /^class\s+(\w+)/.exec(trimmed))) push(match[1], i, 'class', indent)
      continue
    }
    if (md) {
      const match = /^(#{1,6})\s+(.+)$/.exec(trimmed)
      if (match) push(match[2], i, 'heading', match[1].length - 1)
      continue
    }
    if (rust) {
      let match
      if ((match = /^(?:pub(?:\([^)]+\))?\s+)?(?:async\s+)?fn\s+(\w+)/.exec(trimmed))) push(match[1], i, 'function')
      else if ((match = /^(?:pub(?:\([^)]+\))?\s+)?(?:struct|enum|trait|type)\s+(\w+)/.exec(trimmed))) {
        push(match[1], i, trimmed.includes('trait') ? 'interface' : 'class')
      } else if ((match = /^impl(?:\s*<[^>]+>)?\s+(?:\w+\s+for\s+)?(\w+)/.exec(trimmed))) push(match[1], i, 'class')
      continue
    }
    if (jvm) {
      if (trimmed.startsWith('//') || trimmed.startsWith('*') || trimmed.startsWith('/*') || trimmed.startsWith('@')) continue
      const indent = Math.min(4, Math.floor((raw.length - raw.trimStart().length) / 2))
      let match
      if ((match = /^(?:public|private|protected|internal|open|abstract|final|data|sealed|enum|object|companion|\s)*\s*(?:class|interface|object|enum\s+class|data\s+class)\s+(\w+)/.exec(trimmed))) {
        push(match[1], i, trimmed.includes('interface') ? 'interface' : 'class', indent)
      } else if ((match = /^(?:public|private|protected|internal|open|override|suspend|fun|\s)*fun\s+(\w+)\s*[<(]/.exec(trimmed))) {
        push(match[1], i, indent > 0 ? 'method' : 'function', indent)
      } else if ((match = /^(?:public|private|protected|static|final|synchronized|abstract|default|\s)*[\w.<>,\[\]\s]+\s+(\w+)\s*\([^;]*\)\s*(?:throws\s+[\w.,\s]+)?\s*\{/.exec(trimmed))) {
        if (!['if', 'for', 'while', 'switch', 'catch', 'return', 'new'].includes(match[1])) push(match[1], i, indent > 0 ? 'method' : 'function', indent)
      }
      continue
    }
    if (csharp) {
      if (trimmed.startsWith('//') || trimmed.startsWith('*') || trimmed.startsWith('/*') || trimmed.startsWith('#')) continue
      const indent = Math.min(4, Math.floor((raw.length - raw.trimStart().length) / 2))
      let match
      if ((match = /^(?:public|private|protected|internal|static|sealed|abstract|partial|\s)*\s*(?:class|struct|interface|enum|record)\s+(\w+)/.exec(trimmed))) {
        push(match[1], i, trimmed.includes('interface') ? 'interface' : 'class', indent)
      } else if ((match = /^(?:public|private|protected|internal|static|async|virtual|override|sealed|\s)*[\w.<>,\[\]?]+\s+(\w+)\s*\([^;]*\)\s*\{/.exec(trimmed))) {
        if (!['if', 'for', 'while', 'switch', 'catch', 'return', 'new', 'sizeof'].includes(match[1])) push(match[1], i, indent > 0 ? 'method' : 'function', indent)
      }
      continue
    }
    if (json) {
      const match = /^(\s{0,2})"([^"]+)"\s*:/.exec(raw)
      if (match) push(match[2], i, 'property')
      continue
    }
    if (css) {
      if (trimmed.startsWith('@') || trimmed.startsWith('/*') || trimmed.startsWith('*') || trimmed.startsWith('}')) continue
      const match = /^([^{}/]{1,120})\s*\{/.exec(trimmed)
      if (match) push(match[1].trim(), i, 'property')
      continue
    }
    let match
    if ((match = /^(?:export\s+)?(?:async\s+)?(?:function|fn|def|func)\s+(\w+)/.exec(trimmed))) push(match[1], i, 'function')
    else if ((match = /^(?:export\s+)?(?:pub(?:lic)?\s+)?class\s+(\w+)/.exec(trimmed))) push(match[1], i, 'class')
  }
  return items
}

function resolveOutlineSymbolAt(items, line) {
  const list = Array.isArray(items) ? items : []
  const target = Number(line)
  if (!Number.isFinite(target) || target < 0) return []
  const stack = []
  let best = []
  for (const item of list) {
    if (!item || !Number.isInteger(item.line) || item.line > target) continue
    const indent = Number.isInteger(item.indent) ? item.indent : 0
    while (stack.length && stack[stack.length - 1].indent >= indent) stack.pop()
    stack.push({ name: item.name, kind: item.kind, line: item.line, indent })
    best = stack.slice()
  }
  return best
}

function formatOutlineBreadcrumb(chain) {
  return (chain || []).map(item => item.name).filter(Boolean).join('.')
}

function outlineKindIcon(kind) {
  const icons = {
    function: 'symbol-function', method: 'symbol-method', class: 'symbol-class',
    interface: 'symbol-interface', variable: 'symbol-variable', constant: 'symbol-constant',
    heading: 'markdown', property: 'symbol-property', type: 'symbol-struct',
  }
  return icons[kind] || 'symbol-misc'
}

function mergeRecentFiles(head, tail, limit = 40) {
  const seen = new Set()
  const out = []
  for (const item of [...(head || []), ...(tail || [])]) {
    const uri = typeof item === 'string' ? item : item?.uri
    if (!uri || seen.has(uri)) continue
    seen.add(uri)
    out.push(typeof item === 'string'
      ? { uri, path: uri, at: 0, line: 0, character: 0 }
      : {
          uri,
          path: item.path || uri,
          at: item.at || 0,
          line: Number.isInteger(item.line) && item.line >= 0 ? item.line : 0,
          character: Number.isInteger(item.character) && item.character >= 0 ? item.character : 0,
        })
    if (out.length >= limit) break
  }
  return out
}

function formatCopyReference(relativePath, line, column, symbol) {
  const file = String(relativePath || '').replace(/\\+/g, '/')
  if (!file) return ''
  const loc = Number(line) > 0
    ? (Number(column) > 0 ? `${file}:${line}:${column}` : `${file}:${line}`)
    : file
  const name = String(symbol || '').trim()
  return name ? `${name} (${loc})` : loc
}

function parseSearchEverywhereQuery(raw) {
  const trimmed = String(raw || '').trim()
  if (!trimmed) return { mode: 'all', query: '' }
  const prefix = trimmed[0]
  const rest = trimmed.slice(1).trim()
  if (prefix === '#' || prefix === ':') return { mode: 'symbols', query: rest }
  if (prefix === '@' || prefix === '>') return { mode: 'actions', query: rest }
  if (prefix === '/' || prefix === '\\') return { mode: 'files', query: rest }
  return { mode: 'all', query: trimmed }
}

module.exports = {
  symbolIcon,
  fuzzyScore,
  parseDocumentOutline,
  resolveOutlineSymbolAt,
  formatOutlineBreadcrumb,
  outlineKindIcon,
  mergeRecentFiles,
  formatCopyReference,
  parseSearchEverywhereQuery,
}
