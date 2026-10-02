const fs = require('fs')
const path = require('path')
const { spawn } = require('child_process')
const vscode = require('vscode')

function git(cwd, args) {
  return new Promise((resolve, reject) => {
    const child = spawn('git', ['-C', cwd, ...args], { windowsHide: true, shell: false })
    let output = ''
    child.stdout.on('data', chunk => { if (output.length < 8192) output += chunk.toString() })
    child.stderr.on('data', chunk => { if (output.length < 8192) output += chunk.toString() })
    const timer = setTimeout(() => child.kill(), 30000)
    child.once('error', error => { clearTimeout(timer); reject(error) })
    child.once('close', code => {
      clearTimeout(timer)
      if (code === 0) resolve(output.trim())
      else reject(new Error(output.trim() || `git завершился с кодом ${code}`))
    })
  })
}

function branchSuggestion(title, chatID) {
  const slug = String(title || 'plan').toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '').slice(0, 42) || 'plan'
  return `point/${slug}-${String(chatID).slice(-6)}`
}

// Те же правила, что у internal/tools/git_repos.go: до трёх уровней вглубь,
// не больше 32, без зависимостей и артефактов сборки, внутрь найденного
// репозитория не спускаться.
const REPO_SEARCH_DEPTH = 3
const REPO_SEARCH_LIMIT = 32
const REPO_SKIP_DIRS = new Set(['node_modules', 'vendor', 'dist', 'build', 'out', 'target'])

function discoverNestedRepos(root) {
  const repos = []
  const walk = (dir, rel, depth) => {
    if (depth > REPO_SEARCH_DEPTH || repos.length >= REPO_SEARCH_LIMIT) return
    let entries = []
    try { entries = fs.readdirSync(dir, { withFileTypes: true }) } catch { return }
    for (const entry of entries) {
      if (!entry.isDirectory() || entry.name.startsWith('.') || REPO_SKIP_DIRS.has(entry.name.toLowerCase())) continue
      const child = path.join(dir, entry.name)
      const childRel = rel ? `${rel}/${entry.name}` : entry.name
      if (fs.existsSync(path.join(child, '.git'))) {
        if (repos.length < REPO_SEARCH_LIMIT) repos.push(childRel)
        continue
      }
      walk(child, childRel, depth + 1)
    }
  }
  walk(root, '', 1)
  return repos.sort()
}

// Осмотр git-агента: fetch, основная ветка с сервера (origin/main, а не
// отставшая локальная), свежая основа текущей ветки и предупреждения. Ядро
// недоступно — остаётся прежний локальный выбор.
async function inspectRepos(provider, source) {
  try {
    const inspection = await provider.service.request('/api/v2/git/inspect?path=' + encodeURIComponent(source), { timeoutMs: 60_000 })
    const byPath = new Map((inspection?.repositories || []).map(repo => [repo.path, { ...repo, warnings: (inspection.warnings || {})[repo.path] || [] }]))
    return byPath
  } catch {
    return new Map()
  }
}

async function mainBranchOf(repo) {
  for (const name of ['master', 'main']) {
    try { await git(repo, ['rev-parse', '--verify', '--quiet', `refs/heads/${name}`]); return name } catch {}
  }
  return ''
}

// Папка без своего Git, но с вложенными репозиториями (cf-pages/, cf-vue-apps/):
// рабочая копия чата — обычный каталог, в нём по тем же путям worktree каждого
// репозитория на общей ветке. Сбой в одном откатывает уже созданные в других.
async function offerNestedReposBranch(provider, chat, source, markSkipped) {
  const id = String(chat.id)
  const rels = discoverNestedRepos(source)
  if (!rels.length) throw new Error('папка проекта не под Git, и вложенных репозиториев в ней нет')
  const inspected = await inspectRepos(provider, source)
  const repos = []
  for (const rel of rels) {
    const dir = path.join(source, ...rel.split('/'))
    const report = inspected.get(rel)
    repos.push({ rel, dir, current: report?.currentBase || await git(dir, ['branch', '--show-current']).catch(() => ''), main: report?.defaultRef || await mainBranchOf(dir) })
  }
  const describe = key => repos.map(repo => `${repo.rel}: ${repo[key] || '—'}`).join(' · ')
  const options = [
    { label: 'От основной ветки', description: describe('main'), key: 'main', disabled: repos.some(repo => !repo.main) },
    { label: 'От текущих веток', description: describe('current'), key: 'current', disabled: repos.some(repo => !repo.current) },
    { label: 'Продолжить без ветки', key: '' },
  ]
  const selected = await vscode.window.showQuickPick(options.filter(item => !item.disabled), { title: 'Ветка для плана · вложенные репозитории', placeHolder: 'Ветка создаётся в каждом репозитории, в отдельной рабочей копии' })
  if (!selected?.key) { await markSkipped(); return false }
  for (const repo of repos) {
    repo.base = repo[selected.key]
    repo.commit = await git(repo.dir, ['rev-parse', '--verify', `${repo.base}^{commit}`])
  }
  const name = String(await vscode.window.showInputBox({ title: 'Ветка для плана · имя', value: branchSuggestion(chat.title, id), prompt: `Основа: ${repos.map(repo => `${repo.rel}@${repo.base}`).join(', ')}. Незакоммиченные правки и файлы вне репозиториев не копируются.`, validateInput: async value => {
    const branch = String(value || '').trim()
    if (!branch) return 'Введите имя ветки'
    try { await git(repos[0].dir, ['check-ref-format', '--branch', branch]) } catch { return 'Недопустимое имя ветки Git' }
    for (const repo of repos) {
      try { await git(repo.dir, ['rev-parse', '--verify', '--quiet', `refs/heads/${branch}`]); return `Ветка уже есть в ${repo.rel}` } catch {}
    }
    return undefined
  } }) || '').trim()
  if (!name) { await markSkipped(); return false }
  const managed = path.join(provider.service.dataDirPath, 'managed-workspaces')
  const target = path.join(managed, `chat-${id}`)
  if (fs.existsSync(target)) {
    for (const repo of repos) {
      const dir = path.join(target, ...repo.rel.split('/'))
      const existingBranch = await git(dir, ['branch', '--show-current']).catch(() => '')
      const existingCommit = await git(dir, ['rev-parse', 'HEAD']).catch(() => '')
      if (existingBranch !== name || existingCommit !== repo.commit) throw new Error('Рабочая копия этого чата уже существует с другой веткой или коммитом')
    }
  } else {
    await fs.promises.mkdir(target, { recursive: true })
    const created = []
    try {
      for (const repo of repos) {
        const dir = path.join(target, ...repo.rel.split('/'))
        await fs.promises.mkdir(path.dirname(dir), { recursive: true })
        await git(repo.dir, ['worktree', 'add', '-b', name, dir, repo.commit])
        created.push({ repo, dir })
      }
    } catch (error) {
      for (const { repo, dir } of created.reverse()) {
        await git(repo.dir, ['worktree', 'remove', '--force', dir]).catch(() => {})
        await git(repo.dir, ['branch', '-D', name]).catch(() => {})
      }
      await fs.promises.rm(target, { recursive: true, force: true }).catch(() => {})
      throw error
    }
  }
  const repositories = repos.map(repo => ({ path: repo.rel, base: repo.base, commit: repo.commit }))
  await provider.service.request('/api/master/conversations/'+encodeURIComponent(id)+'/bind-branch', { method: 'POST', body: JSON.stringify({ path: target, name, repositories }) })
  await provider.switchToProject(target)
  await provider.service.ensureStarted()
  provider.post({ type: 'masterBranchNotice', tone: 'ok', message: `Ветка ${name} создана во вложенных репозиториях: ${repos.map(repo => `${repo.rel} от ${repo.base}`).join(', ')}. Чат открыт в отдельной рабочей копии.` })
  return true
}

async function offerMasterChatBranch(provider, chat, { manual = false } = {}) {
  const id = String(chat?.id || '')
  if (!id || (!manual && (chat.branchOffer !== 'pending' || chat.workMode !== 'plan'))) return false
  const source = provider.workspaceFolder()?.uri?.fsPath
  const markSkipped = async () => {
    await provider.service.request('/api/master/conversations/'+encodeURIComponent(id)+'/branch-offer', { method: 'POST', body: JSON.stringify({ state: 'skipped' }) })
  }
  try {
    if (!source) throw new Error('Откройте папку проекта')
    const root = await git(source, ['rev-parse', '--show-toplevel']).catch(() => '')
    if (!root) return await offerNestedReposBranch(provider, chat, source, markSkipped)
    const report = (await inspectRepos(provider, source)).get('.')
    const current = report?.currentBase || await git(root, ['branch', '--show-current'])
    const main = report?.defaultRef || await mainBranchOf(root) || 'master'
    const warnings = (report?.warnings || []).join('; ')
    const options = [
      { label: 'От основной ветки', description: main + (report?.defaultRef ? ' · с сервера' : ' · локальная'), base: main },
      { label: 'От текущей ветки', description: (current || 'Текущая ветка не определена') + (warnings ? ' · ' + warnings : ''), base: current, disabled: !current },
      { label: 'Продолжить без ветки', base: '' },
    ]
    const selected = await vscode.window.showQuickPick(options.filter(item => !item.disabled), { title: 'Ветка для плана · выберите базу', placeHolder: 'Ветка создаётся в отдельной рабочей копии' })
    if (!selected?.base) { await markSkipped(); return false }
    const base = selected.base
    const commit = await git(root, ['rev-parse', '--verify', `${base}^{commit}`])
    const name = String(await vscode.window.showInputBox({ title: 'Ветка для плана · имя', value: branchSuggestion(chat.title, id), prompt: `Основа: ${base} · ${commit.slice(0, 10)}. Локальные незакоммиченные правки не копируются.`, validateInput: async value => {
      if (!String(value || '').trim()) return 'Введите имя ветки'
      try { await git(root, ['check-ref-format', '--branch', String(value).trim()]); return undefined } catch { return 'Недопустимое имя ветки Git' }
    } }) || '').trim()
    if (!name) { await markSkipped(); return false }
    const managed = path.join(provider.service.dataDirPath, 'managed-workspaces')
    const target = path.join(managed, `chat-${id}`)
    await fs.promises.mkdir(managed, { recursive: true })
    if (fs.existsSync(target)) {
      const existingBranch = await git(target, ['branch', '--show-current'])
      const existingCommit = await git(target, ['rev-parse', 'HEAD'])
      if (existingBranch !== name || existingCommit !== commit) throw new Error('Рабочая копия этого чата уже существует с другой веткой или коммитом')
    } else {
      await git(root, ['worktree', 'add', '-b', name, target, commit])
    }
    await provider.service.request('/api/master/conversations/'+encodeURIComponent(id)+'/bind-branch', { method: 'POST', body: JSON.stringify({ path: target, name, base, commit }) })
    await provider.switchToProject(target)
    await provider.service.ensureStarted()
    provider.post({ type: 'masterBranchNotice', tone: 'ok', message: `Ветка ${name} создана от ${base}. Чат открыт в отдельной рабочей копии.` })
    return true
  } catch (error) {
    provider.post({ type: 'masterBranchNotice', tone: 'error', message: `Ветка не создана: ${String(error?.message || error)}. План можно продолжить без неё.` })
    try { await markSkipped() } catch {}
    return false
  }
}

module.exports = { offerMasterChatBranch, branchSuggestion }
