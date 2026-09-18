// Шкалы оболочки — под затвором, как шкалы слоёв.
//
// Регистр «оболочка» описан в docs/RPG-DESIGN-SYSTEM.md одним каноном: радиус
// 2px база, кегли 9·10·11·12·13·15·18·22, ритм отступов 4px, декоративных
// градиентов нет. Слои вебвью держит трещотка `ui/build.mjs`, а листы оболочки
// не держал никто: `check-shell-tokens.mjs` сверяет одиннадцать цветов и
// повторы селекторов, но ни кегля, ни радиуса, ни отступа не видит. За этим
// слепым пятном накопились три системы сразу — канон Point, плотность 0.7
// «Nocturne» и мёртвые фолбэки прежней палитры из устаревшей таблицы
// документа.
//
// Проверка статическая и грубая намеренно: что видно на экране, меряет браузер
// (scripts/audit-point-workbench.mjs на живом окне). Здесь важно другое —
// геометрию правят редко и вслепую, и вернуть литерал легче всего в
// сокращённой записи `font:`, где он теряется среди толщины и интерлиньяжа.
//
//   node scripts/check-shell-scales.mjs
//   node scripts/check-shell-scales.mjs --write   // переписать бюджет
//
// Возврат 0, пока нарушений не больше бюджета (distribution/shell-budget.json)
// и пока каждое из них либо в бюджете, либо в списке принятых с причиной.

import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const shellDir = path.join(root, 'distribution', 'resources')
const budgetFile = path.join(root, 'distribution', 'shell-budget.json')
const write = process.argv.includes('--write')

// Шкалы канона. Совпадают со ступенями vscode-extension/ui/tokens.css: одна
// система на регистр, а не «почти такая же».
const TYPE_SCALE = new Set(['9px', '10px', '11px', '12px', '13px', '15px', '18px', '22px'])
const RADIUS_SCALE = new Set(['0', '2px', '4px', '8px', '999px', '50%'])
const SPACE_SCALE = new Set(['0', '2px', '4px', '6px', '8px', '10px', '12px', '16px', '24px', '32px'])

// Значок — не текст. Больше половины `font-size` в оболочке стоит на codicon,
// и это метрика глифа: 14 · 16 · 18 — лесенка значков, а не кеглей. Загонять
// её в типографическую шкалу значит уменьшить значки ради числа в отчёте.
const ICON_SELECTOR = /codicon|monaco-icon|-icon\b|\bicon\b|::before|::after|twistie|\.mark\b/
const isIconRule = selector => ICON_SELECTOR.test(selector)

const stripComments = css => css.replace(/\/\*[\s\S]*?\*\//g, ' ')

// Разбор по блокам: внутренний блок @media матчится первым, потому что его
// тело не содержит скобок. Селектор чистим от хвоста at-правила — иначе
// «@media (max-width: 880px) { .tab» считается селектором целиком.
function* rules(css) {
  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const head = match[1]
    const selector = head.slice(head.lastIndexOf('{') + 1).split(/\s+/).join(' ').trim()
    yield { selector, body: match[2] }
  }
}

const problems = []
const note = (kind, file, value, selector) =>
  problems.push({ kind, file, value, selector: String(selector).slice(0, 90) })

const files = readdirSync(shellDir).filter(name => name.endsWith('.css')).sort()
const sources = new Map(files.map(name => [name, readFileSync(path.join(shellDir, name), 'utf8')]))

// Объявления токенов оболочки: значения фолбэков в соседних листах обязаны
// совпадать с ними. Расхождение молчит до первого случая, когда токен не
// доехал, — и тогда поверхность красится палитрой позапрошлого года.
const declaredTokens = new Map()
for (const [, source] of sources) {
  for (const match of stripComments(source).matchAll(/(--point-[\w-]+)\s*:\s*([^;}]+)[;}]/g)) {
    if (!declaredTokens.has(match[1])) declaredTokens.set(match[1], match[2].trim().toLowerCase())
  }
}

for (const [file, raw] of sources) {
  const css = stripComments(raw)

  for (const { selector, body } of rules(css)) {
    const icons = isIconRule(selector)

    // Кегль: и полная запись, и сокращённая. В сокращённой размер стоит перед
    // косой чертой — `font: 400 11.5px/1.45 var(--point-font)`.
    for (const match of body.matchAll(/font-size:\s*([^;}]+)/g)) {
      const value = match[1].replace(/!important/g, '').trim()
      if (value.startsWith('var(') || value.includes('(')) continue
      if (value === '0' || value === 'inherit' || /(%|em|rem)$/.test(value)) continue
      if (icons || TYPE_SCALE.has(value)) continue
      note('type', file, value, selector)
    }
    for (const match of body.matchAll(/(?:^|[;\s])font:\s*([^;}]+)/g)) {
      const size = /(\d+(?:\.\d+)?px)\s*\//.exec(match[1])
      if (!size || icons || TYPE_SCALE.has(size[1])) continue
      note('type', file, size[1], selector)
    }

    for (const match of body.matchAll(/border-radius:\s*([^;}]+)/g)) {
      for (const token of match[1].replace(/!important/g, '').trim().split(/\s+/)) {
        if (token.startsWith('var(') || token.includes('(') || token === 'inherit') continue
        if (RADIUS_SCALE.has(token)) continue
        note('radius', file, token, selector)
      }
    }

    for (const match of body.matchAll(/(?:padding|margin|gap|row-gap|column-gap)(?:-(?:top|right|bottom|left|inline|block))?:\s*([^;}]+)/g)) {
      const value = match[1].replace(/!important/g, '').trim()
      if (value.includes('var(') || value.includes('(') || value.includes('%')) continue
      for (const token of value.split(/\s+/)) {
        if (token === 'auto' || token === '0' || !token.endsWith('px')) continue
        // Волосок 1px — не отступ, а линия: им отбивают рамку, а не ритм.
        const step = token.replace('-', '')
        if (step === '1px' || SPACE_SCALE.has(step)) continue
        note('space', file, token, selector)
      }
    }

    // Градиент на хроме документ запрещает прямо: «скруглённые мобильные
    // карточки и декоративные градиенты не используются».
    if (/linear-gradient|radial-gradient/.test(body)) note('gradient', file, 'gradient', selector)

    // Литеральный цвет мимо палитры. В объявлении токена он законен — там его
    // и объявляют; в правиле это цвет, о котором не знает ни одна шкала.
    for (const declaration of body.split(';')) {
      const property = declaration.split(':')[0]
      if (property && property.trim().startsWith('--point-')) continue
      // Цвет внутри `var(--point-x, #hex)` — это фолбэк, и его стережёт своя
      // проверка ниже. Без изъятия одна строка считалась дважды, и счёт долга
      // врал вдвое.
      const withoutFallbacks = declaration.replace(/var\([^()]*\)/g, ' ')
      for (const hex of withoutFallbacks.matchAll(/#[0-9a-fA-F]{3,8}\b/g)) note('hex', file, hex[0], selector)
    }
  }

  // Фолбэк, разошедшийся с объявленным токеном.
  for (const match of css.matchAll(/var\(\s*(--point-[\w-]+)\s*,\s*([^)]+)\)/g)) {
    const declared = declaredTokens.get(match[1])
    const fallback = match[2].trim().toLowerCase()
    if (!declared || declared === fallback) continue
    note('fallback', file, `${match[1]}: ${fallback} вместо ${declared}`, '')
  }
}

const budget = JSON.parse(readFileSync(budgetFile, 'utf8'))
const accepted = budget.accepted ?? []
const usedAcceptances = new Set()
const isAccepted = item => accepted.some((rule, index) => {
  if (rule.kind !== item.kind || rule.file !== item.file || rule.value !== item.value) return false
  if (rule.selector && !item.selector.includes(rule.selector)) return false
  usedAcceptances.add(index)
  return true
})

const fresh = problems.filter(item => !isAccepted(item))
const counts = {}
for (const item of fresh) counts[item.kind] = (counts[item.kind] ?? 0) + 1
const total = fresh.length

const byKind = new Map()
for (const item of fresh) {
  const key = `${item.kind} · ${item.file} · ${item.value}`
  if (!byKind.has(key)) byKind.set(key, [])
  byKind.get(key).push(item.selector)
}
for (const [key, where] of [...byKind.entries()].sort()) {
  console.log(`  ${key} × ${where.length}`)
  for (const selector of where.slice(0, 2)) if (selector) console.log(`      ${selector}`)
}

const summary = Object.entries(counts).sort().map(([kind, count]) => `${kind}=${count}`).join(' ')
console.log(`вне шкал оболочки: всего=${total} ${summary}`)

const stale = accepted.filter((_, index) => !usedAcceptances.has(index))
if (stale.length) {
  console.log(`принятых записей без находки — ${stale.length}: строку пора убрать`)
  for (const rule of stale) console.log(`  ${rule.kind} · ${rule.file} · ${rule.value}`)
}

if (write) {
  writeFileSync(budgetFile, JSON.stringify({ ...budget, total, counts }, null, 2) + '\n')
  console.log(`бюджет переписан: всего=${total}`)
} else if (total > budget.total) {
  console.error(`\nЗАТВОР: значений вне шкал стало больше (${budget.total} → ${total}).`)
  console.error('Шкалы — docs/RPG-DESIGN-SYSTEM.md и vscode-extension/ui/tokens.css.')
  console.error('Осознанное отступление вписывается в distribution/shell-budget.json с причиной.')
  process.exitCode = 1
} else if (total < budget.total) {
  console.log(`долг уменьшился: ${budget.total} → ${total}. Перепишите бюджет: node scripts/check-shell-scales.mjs --write`)
}
