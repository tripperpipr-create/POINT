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

async function offerMasterChatBranch(provider, chat, { manual = false } = {}) {
  const id = String(chat?.id || '')
  if (!id || (!manual && (chat.branchOffer !== 'pending' || chat.workMode !== 'plan'))) return false
  const source = provider.workspaceFolder()?.uri?.fsPath
  const markSkipped = async () => {
    await provider.service.request('/api/master/conversations/'+encodeURIComponent(id)+'/branch-offer', { method: 'POST', body: JSON.stringify({ state: 'skipped' }) })
  }
  try {
    if (!source) throw new Error('Откройте папку проекта')
    const root = await git(source, ['rev-parse', '--show-toplevel'])
    const current = await git(root, ['branch', '--show-current'])
    const options = [
      { label: 'От master', description: 'Локальная ветка master', base: 'master' },
      { label: 'От текущей ветки', description: current || 'Текущая ветка не определена', base: current, disabled: !current },
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
