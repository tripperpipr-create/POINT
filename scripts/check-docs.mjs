import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const root = path.resolve(import.meta.dirname, '..')

// Состав файлов берётся у git, а не обходом каталога.
//
// Обход видел всё, что лежит на диске, включая worktree другой ветки в
// `.claude/` — вторую полную копию репозитория. Расходилось это в обе стороны:
// сломанная ссылка в чужой копии роняла проверку локально и проходила в CI,
// где копии нет; а путь в бэктиках «находился» в копии, которой в чистом клоне
// не будет, — затвор зеленел на ссылке в никуда. Весь смысл проверки в том,
// что CI работает на чистом клоне, значит и состав файлов обязан быть тот же.
//
// Отсутствие git здесь — отказ, а не повод обойти дерево молча: тихий запасной
// путь вернул бы ровно ту разницу, ради которой проверка и переписана.
function trackedFiles() {
  const output = execFileSync('git', ['ls-files', '-z'], {
    cwd: root, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024,
  })
  // Индекс может нести имя, которого на диске уже нет: файл удалён, но
  // удаление ещё не записано в коммит. Чтение такого имени уронило бы
  // проверку исключением вместо внятного отказа. Пропуск безопасен: ссылку
  // на удалённый документ поймает проверка со стороны тех, кто на него ссылается.
  return output
    .split('\0')
    .filter(Boolean)
    .filter(file => fs.existsSync(path.join(root, file)))
}

function relative(file) {
  return path.relative(root, file).replaceAll('\\', '/')
}

const errors = []
const repositoryPaths = trackedFiles()
const markdownFiles = repositoryPaths
  .filter(file => file.toLowerCase().endsWith('.md'))
  .map(file => path.join(root, file))
const evidenceRoots = '(?:internal|cmd|scripts|distribution|\\.github|vscode-extension|frontend|docs)'
const sourceExtensions = new Set([
  '.css', '.go', '.html', '.js', '.json', '.jsx', '.md', '.mjs', '.mod', '.ps1',
  '.sh', '.sql', '.sum', '.toml', '.ts', '.tsx', '.yaml', '.yml',
])

function globExpression(pattern) {
  const escaped = pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&')
  return new RegExp(`^${escaped.replaceAll('*', '[^/]*').replaceAll('?', '[^/]')}$`)
}

for (const file of markdownFiles) {
  const source = fs.readFileSync(file, 'utf8')
  const links = source.matchAll(/!?\[[^\]]*\]\(([^)]+)\)/g)
  for (const match of links) {
    let target = match[1].trim().replace(/^<|>$/g, '').split(/\s+["']/)[0]
    if (!target || target.startsWith('#') || /^[a-z][a-z0-9+.-]*:/i.test(target)) continue
    target = target.split('#', 1)[0].split('?', 1)[0]
    try { target = decodeURIComponent(target) } catch { /* report the filesystem form below */ }
    const resolved = path.resolve(path.dirname(file), target)
    if (!fs.existsSync(resolved)) errors.push(`${relative(file)}: broken link ${match[1]}`)
  }

  for (const match of source.matchAll(/(?:\.\.\/?|\.\/)?scripts[\\/][A-Za-z0-9_.\\/-]+/g)) {
    const normalized = match[0].replace(/^\.\.\//, '').replace(/^\.\//, '').replaceAll('\\', '/')
    if (!fs.existsSync(path.join(root, normalized))) {
      errors.push(`${relative(file)}: missing referenced script ${match[0]}`)
    }
  }

  for (const match of source.matchAll(new RegExp('`(' + evidenceRoots + '[\\\\/][^`\\s]+)`', 'g'))) {
    let referenced = match[1]
      .replaceAll('\\\\', '/')
      .replace(/:[0-9]+(?:,[0-9]+)*$/, '')
      .replace(/[),;:]$/, '')
    if (referenced.includes('...') || referenced.includes('…')) continue

    if (referenced.includes('*') || referenced.includes('?')) {
      const pattern = globExpression(referenced)
      if (!repositoryPaths.some(candidate => pattern.test(candidate))) {
        errors.push(`${relative(file)}: missing referenced path pattern ${match[1]}`)
      }
      continue
    }

    if (fs.existsSync(path.join(root, referenced))) continue
    const extension = path.posix.extname(referenced)
    if (!extension || sourceExtensions.has(extension)) {
      errors.push(`${relative(file)}: missing referenced path ${match[1]}`)
    }
  }
}

// Список файлов был ручным и уже успел ослепнуть: маршруты жили в двух
// перечисленных файлах, а появившийся третий проверка не видела вовсе —
// недокументированный маршрут прошёл бы гейт молча. Читаем весь пакет.
const routeSources = fs.readdirSync(path.join(root, 'internal/httpapi'))
  .filter(name => name.endsWith('.go') && !name.endsWith('_test.go'))
  .sort()
  .map(name => fs.readFileSync(path.join(root, 'internal/httpapi', name), 'utf8'))
  .join('\n')
const sourceRoutes = new Set([...routeSources.matchAll(/HandleFunc\("((?:GET|POST|PUT|PATCH|DELETE) \/api\/[^" ]+)/g)].map(match => match[1]))
const v2Routes = new Set([...sourceRoutes].filter(route => route.includes(' /api/v2/')))
const apiDocument = fs.readFileSync(path.join(root, 'docs/api.md'), 'utf8')
const documentedRoutes = new Set([...apiDocument.matchAll(/\| `((?:GET|POST|PUT|PATCH|DELETE))` \| `(\/api\/[^`?]+)(?:\?[^`]*)?`/g)].map(match => `${match[1]} ${match[2]}`))

for (const route of [...sourceRoutes].sort()) {
  if (!documentedRoutes.has(route)) errors.push(`docs/api.md: undocumented route ${route}`)
}
for (const route of [...documentedRoutes].sort()) {
  if (!sourceRoutes.has(route)) errors.push(`docs/api.md: stale route ${route}`)
}

for (const name of ['docs/PROJECT-STATUS.md']) {
  const lines = fs.readFileSync(path.join(root, name), 'utf8').split(/\r?\n/)
  for (const [index, line] of lines.entries()) {
    // Замыкающего \b здесь быть не должно: в JS он определён по ASCII, и после
    // кириллического «маршрутов» границы нет — русская формулировка проверку
    // не проходила вовсе, и число в документе устаревало молча.
    for (const match of line.matchAll(/\b(\d+)\s+(?:HTTP[- ]?)?(?:routes?\b|маршрут(?:а|ов)?)/giu)) {
      const claimed = Number(match[1])
      if (claimed !== sourceRoutes.size) {
        errors.push(`${name}:${index + 1}: API route claim is ${claimed}, source has ${sourceRoutes.size}`)
      }
    }
    // «Эндпоинт» считается отдельно и только рядом с `/api/v2/*`.
    //
    // В правило выше это слово добавить нельзя: там сравнение со всеми маршрутами,
    // а речь идёт о подмножестве. Без этой проверки число расходилось молча:
    // документ говорил 18 в двух местах и 19 в третьем — внутри одного файла.
    for (const match of line.matchAll(/(\d+)\s+эндпоинт(?:а|ов)?.{0,40}\/api\/v2/giu)) {
      const claimed = Number(match[1])
      if (claimed !== v2Routes.size) {
        errors.push(`${name}:${index + 1}: /api/v2 endpoint claim is ${claimed}, source has ${v2Routes.size}`)
      }
    }
  }
}

const appSource = fs.readFileSync(path.join(root, 'internal/app/app.go'), 'utf8')
const appVersion = appSource.match(/const Version = "([^"]+)"/)?.[1]
const frontendVersion = JSON.parse(fs.readFileSync(path.join(root, 'frontend/package.json'), 'utf8')).version
const extensionVersion = JSON.parse(fs.readFileSync(path.join(root, 'vscode-extension/package.json'), 'utf8')).version
if (!appVersion || appVersion !== frontendVersion || appVersion !== extensionVersion) {
  errors.push(`version drift: core=${appVersion || 'missing'}, frontend=${frontendVersion}, extension=${extensionVersion}`)
}

const uiSource = fs.readFileSync(path.join(root, 'vscode-extension/ui/client/main.js'), 'utf8')
const onboardingBlock = uiSource.match(/const ONBOARDING_STEPS = \[([\s\S]*?)\n\]/)?.[1] || ''
const onboardingSteps = [...onboardingBlock.matchAll(/\{ id:/g)].length
if (!onboardingSteps) errors.push('cannot determine onboarding step count')

const countWords = new Map([
  ['eight', 8], ['nine', 9], ['eleven', 11],
  ['восемь', 8], ['девять', 9], ['одиннадцать', 11],
])
const livingOnboardingDocs = [
  'README.md',
  'CONTRIBUTING.md',
  'docs/architecture.md',
  'docs/agent-hub-mvp.md',
  'distribution/README.md',
  'vscode-extension/README.md',
]
for (const name of livingOnboardingDocs) {
  const lines = fs.readFileSync(path.join(root, name), 'utf8').split(/\r?\n/)
  for (const [index, line] of lines.entries()) {
    if (!/(onboarding|онбординг|первый запуск)/i.test(line)) continue
    for (const match of line.matchAll(/(\d+|eight|nine|eleven|восемь|девять|одиннадцать)[-\s]*(?:step|шаг)/giu)) {
      const raw = match[1].toLowerCase()
      const claimed = /^\d+$/.test(raw) ? Number(raw) : countWords.get(raw)
      if (claimed !== onboardingSteps) {
        errors.push(`${name}:${index + 1}: onboarding claims ${claimed}, source has ${onboardingSteps}`)
      }
    }
  }
}

if (errors.length) {
  console.error(`Documentation check failed (${errors.length}):`)
  for (const error of errors) console.error(`- ${error}`)
  process.exit(1)
}

console.log(JSON.stringify({
  documentation: 'ok',
  markdownFiles: markdownFiles.length,
  apiRoutes: sourceRoutes.size,
  applicationVersion: appVersion,
  onboardingSteps,
}))
