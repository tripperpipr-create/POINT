// Git mutations go through the core; Code-OSS supplies discovery and editors.
const { chooseIndexPatch } = require('./git-index-patches')
function createGitWorkbenchTools({ vscode, path }) {
  async function gitWorkbenchSnapshot(provider, repo) {
    const inventory = await provider.service.request('/api/v2/git/repositories')
    const root = repo.rootUri.fsPath
    const query = new URLSearchParams({ repoRoot: root, workspaceId: inventory.workspaceId })
    const response = await provider.service.request('/api/v2/git/status?' + query)
    const data = response.git
    provider.gitWorkbenchState = { ...data, workspaceId: response.workspaceId }
    const lists = await provider.syncGitLists(root, data.changes)
    const changes = data.changes.map(item => ({ ...item, list: lists.assign[item.path] || lists.active }))
    const commits = await provider.service.request('/api/v2/git/read', { method: 'POST',
      body: JSON.stringify({ workspaceId: response.workspaceId, repoRoot: root, kind: 'history' }) })
    provider.paintGitViewChrome({ available: true, branch: data.branch, count: new Set(changes.map(item => item.path)).size })
    return { kind: 'git', available: true, ...data, changes, workspaceId: response.workspaceId,
      project: provider.workspaceFolder()?.name || '', repository: path.basename(root),
      repositories: inventory.repositories.map(item => ({ ...item, selected: item.root === data.root })),
      commits: (commits || []).slice(0, 8).map(item => ({ ...item, shortHash: item.hash.slice(0, 8) })),
      changeLists: lists.lists, activeList: lists.active, changesTotal: changes.length, changesHidden: 0,
      pushTargets: provider.gitPushTargets(repo, data.remote), stashes: await provider.gitStashes(root),
    }
  }
  const actions = new Set(['discardTracked','applyPatch','stage', 'unstage', 'stageBlocks', 'unstageBlocks', 'stageLines', 'unstageLines',
    'commit', 'commitAndPush', 'fetch', 'push', 'forcePush', 'pull', 'createBranch', 'switchBranch',
    'renameBranch', 'deleteBranch', 'addRemote', 'removeRemote', 'setUpstream', 'createTag', 'deleteTag',
    'stashPush', 'stashApply', 'stashDrop', 'merge', 'rebase', 'cherry-pick', 'revert', 'continue', 'abort'])
  async function runGitWorkbenchAction(provider, message) {
    if (!actions.has(message.action)) return undefined
    const { repo } = await provider.gitContext(message.repoRoot)
    if(message.repoRoot && path.resolve(message.repoRoot).toLowerCase()!==path.resolve(repo?.rootUri?.fsPath||'').toLowerCase()) throw new Error('Репозиторий действия больше не открыт')
    if (!repo) throw new Error('Репозиторий недоступен.')
    if (!provider.gitWorkbenchState || provider.gitWorkbenchState.root !== repo.rootUri.fsPath) {
      await gitWorkbenchSnapshot(provider, repo)
    }
    const snapshot = provider.gitWorkbenchState
    if(message.workspaceId && message.workspaceId!==snapshot.workspaceId) throw new Error('Проект действия изменился')
    const action = message.action
    const fields = Object.fromEntries(['action','message','ref','name','remote','url','patch','amend','confirmed','strategy'].filter(k=>message[k]!==undefined).map(k=>[k,message[k]]))
    const input = { ...fields, repoRoot: snapshot.root, workspaceId: snapshot.workspaceId,
      revision: message.revision || snapshot.revision, ref: message.ref || message.stash || '',
      paths: message.path ? [message.path] : (message.paths || []) }
    const matching = snapshot.changes.filter(item => input.paths.includes(item.path))
    input.paths = [...new Set([...input.paths, ...matching.map(item => item.originalPath).filter(Boolean)])]
    if (/^(stage|unstage)(Blocks|Lines)$/.test(action)) {
      const staged = action.startsWith('unstage')
      const patch = await provider.service.request('/api/v2/git/read', { method: 'POST',
        body: JSON.stringify({ repoRoot: snapshot.root, workspaceId: snapshot.workspaceId,
          kind: 'diff', path: message.path, area: staged ? 'staged' : 'working' }) })
      input.patch = await chooseIndexPatch(vscode, patch, staged, action.endsWith('Lines'))
      if (!input.patch) return 'Подготовка отменена'
      input.action = 'stagePatch'
    }
    if (action === 'push' || action === 'forcePush' || action === 'commitAndPush') {
      input.remote = snapshot.remote?.split('/')[0] || ''
      const chosen = provider.gitPushTarget?.split('/')[0]
      if (chosen) {input.remote = chosen;input.ref=provider.gitPushTarget.slice(chosen.length+1)}
      else if(snapshot.remote?.startsWith(input.remote+'/'))input.ref=snapshot.remote.slice(input.remote.length+1)
      if (!input.remote) {
        const selected = await vscode.window.showQuickPick(snapshot.remotes.map(item => item.name), { title: 'Git · remote для отправки' })
        if (!selected) return 'Отправка отменена'
        input.remote = selected
      }
    }
    if (action === 'createBranch' || action === 'renameBranch' || action === 'createTag') {
      input.name = await vscode.window.showInputBox({ title: 'Git · ' + action, prompt: 'Имя', value: input.name || '' })
      if (!input.name) return 'Действие отменено'
    }
    if (['switchBranch', 'deleteBranch', 'setUpstream', 'merge', 'rebase', 'cherry-pick', 'revert'].includes(action) && !input.ref) {
      const refs=action==='deleteBranch'?snapshot.localBranches||snapshot.branches:action==='setUpstream'?snapshot.remoteBranches||snapshot.branches:snapshot.branches
      input.ref = ['cherry-pick','revert'].includes(action)
        ? await vscode.window.showInputBox({title:'Git · '+action,prompt:'Хеш коммита или ревизия'})
        : await vscode.window.showQuickPick(refs, { title: 'Git · выбрать ревизию' })
      if (!input.ref) return 'Действие отменено'
    }
    if (action === 'addRemote') {
      input.name = await vscode.window.showInputBox({ title: 'Git · имя remote' })
      if (!input.name) return 'Действие отменено'
      input.url = await vscode.window.showInputBox({ title: 'Git · адрес remote' })
      if (!input.url) return 'Действие отменено'
    }
    if (action === 'removeRemote') {
      input.remote = await vscode.window.showQuickPick(snapshot.remotes.map(item => item.name), { title: 'Git · удалить remote' })
      if (!input.remote) return 'Действие отменено'
    }
    if (['stashApply','stashDrop'].includes(action) && !input.ref) {
      input.ref = await vscode.window.showInputBox({title:'Git — stash',value:'stash@{0}'})
      if(!input.ref) return 'Действие отменено'
    }
    if (action === 'deleteTag') {
      input.name = await vscode.window.showInputBox({ title: 'Git · удалить тег' })
      if (!input.name) return 'Действие отменено'
    }
    if (action === 'pull' && snapshot.ahead && snapshot.behind && !snapshot.pullConfigured) {
      const strategy = await vscode.window.showQuickPick([
        { label: 'Merge', value: 'merge' }, { label: 'Rebase', value: 'rebase' }, { label: 'Только fast-forward', value: 'ff-only' },
      ], { title: 'Git · способ объединения веток' })
      if (!strategy) return 'Pull отменён'
      input.strategy = strategy.value
    }
    if (['forcePush', 'rebase', 'deleteBranch', 'removeRemote', 'deleteTag', 'stashDrop', 'abort'].includes(action) || input.amend) {
      const yes = await vscode.window.showWarningMessage('Git · подтвердить действие', {
        modal: true, detail: [snapshot.root, snapshot.branch, action, input.ref || input.name || input.remote].join('\n'),
      }, 'Выполнить')
      if (!yes) return 'Действие отменено'
      input.confirmed = true
    }
    const result = await provider.service.request('/api/v2/git/actions', { method: 'POST',
      timeoutMs: 125000, body: JSON.stringify(input) })
    provider.gitWorkbenchState = { ...result.snapshot, workspaceId: snapshot.workspaceId }
    return result.message
  }
  return { gitWorkbenchSnapshot, runGitWorkbenchAction }
}
module.exports = { createGitWorkbenchTools }
