// Сборка media/style.css из ui/tokens.css и ui/layers/*.css.
//
// Зависимостей нет намеренно: для склейки слоёв каскада бандлер не нужен,
// а лишняя зависимость в сборке расширения — лишний риск поставки.
//
// Кроме склейки скрипт работает трещоткой: считает значения вне шкал и
// сравнивает с зафиксированным бюджетом в ui/budget.json. Число может только
// уменьшаться — новая самодеятельность роняет сборку.
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const layersDir = join(here, 'layers')
const outFile = join(here, '..', 'media', 'style.css')
const tokensOutFile = join(here, '..', 'media', 'rpg-tokens.css')
const budgetFile = join(here, 'budget.json')

const RADIUS_SCALE = new Set(['0', '2px', '4px', '8px', '999px', '50%'])
const TYPE_SCALE = new Set(['9px', '10px', '11px', '12px', '13px', '15px', '18px', '22px', '0', 'inherit'])
// Абсолютные чёрный и белый допустимы: это тени и оверлеи, а не цвета темы.
const COLOR_ALLOWED = new Set(['#000', '#fff', '#000000', '#ffffff'])

// Структурная проверка: `*/` внутри текста комментария закрывает его досрочно,
// остаток становится мусором и молча съедает следующее правило. Баланс скобок
// такое не ловит — правило-жертва свои скобки сохраняет. Поэтому вырезаем
// комментарии сканером и требуем, чтобы после этого не осталось ни одного `*/`.
function structuralCheck(name, css) {
  const errors = []
  let out = ''
  let index = 0
  while (index < css.length) {
    const open = css.indexOf('/*', index)
    if (open === -1) { out += css.slice(index); break }
    out += css.slice(index, open)
    const close = css.indexOf('*/', open + 2)
    if (close === -1) { errors.push(`${name}: незакрытый комментарий на позиции ${open}`); break }
    index = close + 2
  }
  const stray = out.indexOf('*/')
  if (stray !== -1) {
    const around = out.slice(Math.max(0, stray - 60), stray + 2).split('\n').pop()
    errors.push(`${name}: лишний "*/" — комментарий выше закрылся раньше времени возле «${around.trim()}»`)
  }
  const opened = (out.match(/{/g) || []).length
  const closed = (out.match(/}/g) || []).length
  if (opened !== closed) errors.push(`${name}: дисбаланс скобок ${opened} против ${closed}`)
  return errors
}

// Комментарии вырезаются до проверки: слова «!important» или образец цвета,
// упомянутые в пояснении, — это документация, а не нарушение шкалы.
function withoutComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

// Слои, обязанные держать собственное пространство имён. Исторические страты
// содержат правила с !important на общих именах вроде `.primary`, поэтому новый
// слой, переиспользовавший такое имя, проигрывает независимо от специфичности.
// Проверка ловит это на сборке, а не глазами в браузере.
const NAMESPACES = { '05-hall.css': /^(hall-|is-)/, '07-master-quiet.css': /^(hall-|is-)/, '07c-master-feed.css': /^(hall-|is-)/, '07d-master-inspector.css': /^(hall-|is-)/, '27-chat-directory.css': /^(hall-|is-)/ }

function classNames(source) {
  const css = withoutComments(source)
  const names = new Set()
  for (const block of css.split('}')) {
    const head = block.split('{')[0]
    if (!head || head.includes('@')) continue
    for (const match of head.matchAll(/\.([A-Za-z0-9_-]+)/g)) names.add(match[1])
  }
  return names
}

// Нарушение — не «имя без префикса», а имя, которое УЖЕ занято другим слоём.
// Вложенные `.hall-card .name` безопасны, пока `.name` больше нигде не объявлен;
// как только объявлен — правило проигрывает чужому !important, и это ловится.
function namespaceCheck(name, own, others) {
  const rule = NAMESPACES[name]
  if (!rule) return []
  const clash = [...classNames(own)].filter(cls => !rule.test(cls) && others.has(cls))
  return clash.length
    ? [`${name}: имена заняты другими слоями — ${clash.sort().join(', ')}. `
       + `Легаси-страты перебивают их через !important; добавьте префикс hall- или is-.`]
    : []
}

function lint(name, source) {
  const css = withoutComments(source)
  const problems = []
  const strip = (value) => value.replace(/!important/g, '').trim()

  for (const match of css.matchAll(/border-radius:\s*([^;}]+)/g)) {
    for (const token of strip(match[1]).split(/\s+/)) {
      if (token.startsWith('var(') || token.includes('(')) continue
      if (!RADIUS_SCALE.has(token)) problems.push({ kind: 'radius', value: token, name })
    }
  }
  for (const match of css.matchAll(/font-size:\s*([^;}]+)/g)) {
    const value = strip(match[1])
    if (value.startsWith('var(') || value.includes('(')) continue
    if (!TYPE_SCALE.has(value)) problems.push({ kind: 'type', value, name })
  }
  for (const match of css.matchAll(/#[0-9a-fA-F]{3,8}\b/g)) {
    if (COLOR_ALLOWED.has(match[0].toLowerCase())) continue
    problems.push({ kind: 'color', value: match[0], name })
  }
  const important = css.match(/!important/g)
  if (important) for (const _ of important) problems.push({ kind: 'important', value: '!important', name })

  // Отступы вне шкалы. Не ошибка сама по себе: 10px — самое частое значение в
  // продукте, и сдвигать его к 8 или 12 значило бы поехать по всей вёрстке.
  // Но счётчик обязан быть виден, чтобы новых магических чисел не прибавлялось.
  for (const match of css.matchAll(/(?:padding|margin|gap|row-gap|column-gap)(?:-inline|-block)?:\s*([^;}]+)/g)) {
    const value = strip(match[1])
    if (value.includes('var(') || value.includes('(') || value.includes('%')) continue
    for (const token of value.split(/\s+/)) {
      if (token === '0' || token === 'auto' || !token.endsWith('px')) continue
      problems.push({ kind: 'space', value: token, name })
    }
  }
  return problems
}

const layerNames = readdirSync(layersDir).filter(n => n.endsWith('.css')).sort()
if (layerNames.length === 0) {
  console.error('ui/layers пуст — нечего собирать')
  process.exit(1)
}

const chunks = []
const tokens = readFileSync(join(here, 'tokens.css'), 'utf8')
chunks.push(tokens)

const sources = new Map(layerNames.map(name => [name, readFileSync(join(layersDir, name), 'utf8')]))

let problems = []
let structural = structuralCheck('tokens.css', tokens)
for (const name of layerNames) {
  const css = sources.get(name)
  // Имена всех прочих слоёв — база для проверки коллизий.
  const others = new Set()
  for (const [other, source] of sources) {
    if (other !== name) for (const cls of classNames(source)) others.add(cls)
  }
  problems = problems.concat(lint(name, css))
  structural = structural.concat(structuralCheck(name, css), namespaceCheck(name, css, others))
  chunks.push(`/* ==== слой: ${name} ==================================================== */\n${css}`)
}

if (structural.length) {
  console.error('СБОРКА ОТКЛОНЕНА: структурные ошибки CSS\n' + structural.map(e => '  ' + e).join('\n'))
  process.exit(1)
}

const header = [
  '/* СГЕНЕРИРОВАНО ui/build.mjs — не редактировать напрямую.',
  '   Источник: vscode-extension/ui/tokens.css и vscode-extension/ui/layers/*.css',
  `   Слоёв: ${layerNames.length}. Пересобрать: npm run build:css */`,
  '',
].join('\n')

writeFileSync(outFile, header + chunks.join('\n'), 'utf8')

// Point Home и Летопись подключают только rpg-tokens.css и не грузят style.css.
// Публикуем токены и туда, чтобы источник истины остался один на все поверхности.
const tokensHeader = [
  '/* СГЕНЕРИРОВАНО ui/build.mjs из ui/tokens.css — не редактировать напрямую.',
  '   Подключается всеми поверхностями Point: Хаб, компаньон, Home, Летопись. */',
  '',
].join('\n')
writeFileSync(tokensOutFile, tokensHeader + tokens, 'utf8')

const counts = problems.reduce((acc, item) => {
  acc[item.kind] = (acc[item.kind] || 0) + 1
  return acc
}, {})
const total = problems.length

const budget = existsSync(budgetFile) ? JSON.parse(readFileSync(budgetFile, 'utf8')) : null
const summary = Object.entries(counts).map(([k, v]) => `${k}=${v}`).sort().join(' ')
console.log(`собрано: ${layerNames.length} слоёв → media/style.css`)
console.log(`вне шкал: всего=${total} ${summary}`)

if (process.argv.includes('--record')) {
  writeFileSync(budgetFile, JSON.stringify({ total, counts }, null, 2) + '\n', 'utf8')
  console.log(`бюджет зафиксирован: ${total}`)
  process.exit(0)
}

if (budget && total > budget.total) {
  console.error(`\nСБОРКА ОТКЛОНЕНА: значений вне шкал стало больше (${budget.total} → ${total}).`)
  console.error('Используйте токены из ui/tokens.css или обновите бюджет осознанно: npm run build:css -- --record')
  const worst = Object.entries(counts)
    .filter(([kind, value]) => value > (budget.counts?.[kind] || 0))
    .map(([kind, value]) => `  ${kind}: ${budget.counts?.[kind] || 0} → ${value}`)
  if (worst.length) console.error(worst.join('\n'))
  process.exit(1)
}

if (budget && total < budget.total) {
  console.log(`трещотка: ${budget.total} → ${total}, можно зафиксировать: npm run build:css -- --record`)
}
