import fs from 'node:fs'
import path from 'node:path'

const root = path.resolve(import.meta.dirname, '..')
const ignoredDirectories = new Set([
  '.cache', '.git', '.gocache', '.tmp', 'build', 'dist', 'node_modules',
])

function walk(directory, files = []) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    if (entry.isDirectory() && ignoredDirectories.has(entry.name)) continue
    const absolute = path.join(directory, entry.name)
    if (entry.isDirectory()) walk(absolute, files)
    else files.push(absolute)
  }
  return files
}

function relative(file) {
  return path.relative(root, file).replaceAll('\\', '/')
}

const errors = []
const markdownFiles = walk(root).filter(file => file.toLowerCase().endsWith('.md'))
const repositoryPaths = walk(root).map(relative)
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
