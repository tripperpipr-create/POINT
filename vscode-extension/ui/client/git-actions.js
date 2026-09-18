// Git panel click/change actions. State is mutated through the shared ui bag.

export function handleGitClickAction({
  action,
  target,
  root,
  ui,
  vscode,
  persistDraft,
  render,
  gitSelectFile,
  gitGroups,
  gitGroupPaths,
  submitGitCommit,
  toolWindowData,
}) {
  if (!String(action || '').startsWith('git-') && action !== 'dismiss-git-notice') return false

  if (action === 'git-menu') {
    const key = String(target.dataset.menu || '')
    ui.gitMenuFor = ui.gitMenuFor === key ? '' : key
    render()
    // Меню — слой над строкой, а строка живёт в прокручиваемом списке: у
    // нижних строк меню открывается за краем и его не видно вовсе. Доводим
    // список ровно настолько, чтобы меню поместилось целиком.
    root.querySelector('.nc-menu-body')?.scrollIntoView?.({ block: 'nearest' })
    return true
  }
  if (action === 'git-collapse') {
    const id = String(target.dataset.list || '')
    if (ui.gitCollapsed.has(id)) ui.gitCollapsed.delete(id)
    else ui.gitCollapsed.add(id)
    persistDraft()
    render()
    return true
  }
  if (action === 'git-select') {
    gitSelectFile(String(target.dataset.path || ''))
    return true
  }
  if (action === 'git-select-stash') {
    ui.gitSelectedStash = String(target.dataset.stash || '')
    render()
    return true
  }
  if (action === 'git-tab') {
    ui.gitTab = String(target.dataset.tab || 'changes')
    ui.gitMenuFor = ''
    persistDraft()
    render()
    return true
  }
  if (action === 'git-layout') {
    ui.gitFlat = target.dataset.flat === '1'
    persistDraft()
    render()
    return true
  }
  // Одна кнопка на два состояния: если что-то раскрыто — свернуть всё, иначе
  // раскрыть. Так же ведёт себя дерево в любой IDE.
  if (action === 'git-collapse-all') {
    const groupKeys = gitGroups().map(group => group.id)
    const dirKeys = [...ui.gitCollapsed].filter(key => String(key).startsWith('dir:'))
    const openDirs = [...root.querySelectorAll('.nc-dir-twist[aria-expanded="true"]')]
      .map(node => String(node.dataset.list || ''))
      .filter(Boolean)
    const keys = [...new Set([...groupKeys, ...dirKeys, ...openDirs])]
    const expanded = keys.filter(key => !ui.gitCollapsed.has(key))
    if (expanded.length) for (const key of keys) ui.gitCollapsed.add(key)
    else for (const key of keys) ui.gitCollapsed.delete(key)
    persistDraft()
    render()
    return true
  }
  if (action === 'git-history-toggle') {
    ui.gitHistoryOpen = !ui.gitHistoryOpen
    render()
    return true
  }
  if (action === 'dismiss-git-notice') {
    ui.gitNotice = undefined
    render()
    return true
  }
  // «Только эту» — короткий путь к тому, ради чего папки и заведены: собрать
  // коммит из одной работы, не снимая отметки с остальных по одной.
  if (action === 'git-select-only') {
    const id = String(target.dataset.list || '')
    ui.gitChecked = new Set(gitGroupPaths(id))
    persistDraft()
    render()
    return true
  }
  if (action === 'git-reuse-message') {
    ui.gitCommitDraft = String(target.dataset.message || '')
    ui.gitMenuFor = ''
    persistDraft()
    render()
    return true
  }
  if (action === 'git-insert-tag') {
    const tag = String(target.dataset.tag || '')
    if (!tag) return true
    const current = String(ui.gitCommitDraft || '')
    if (/^(feat|fix|refactor|docs|chore|test|perf|ci|build|style)(\(.+\))?!?:\s/i.test(current)) {
      ui.gitCommitDraft = current.replace(/^[^\s:]+:\s*/, tag)
    } else {
      ui.gitCommitDraft = tag + current
    }
    persistDraft()
    render()
    return true
  }
  if (action === 'git-commit-push') {
    submitGitCommit('commitAndPush')
    return true
  }
  if (action === 'git-action') {
    const gitAction = String(target.dataset.gitAction || '')
    if (!gitAction) return true
    if (gitAction === 'setPushTarget') ui.gitTarget = String(target.textContent || '').trim()
    const passive = gitAction === 'openChange' || gitAction === 'openFile' || gitAction === 'history'
    if (ui.gitPendingAction && !passive) return true
    ui.gitMenuFor = ''
    if (!passive) {
      ui.gitPendingAction = target.dataset.path ? `${gitAction}:${target.dataset.path}` : gitAction
      ui.gitNotice = undefined
    }
    render()
    const fromChecked = target.dataset.useChecked === '1'
    vscode.postMessage({
      type: 'gitAction',
      action: gitAction,
      path: target.dataset.path || '',
      list: target.dataset.list || '',
      stash: target.dataset.stash || '',
      target: gitAction === 'setPushTarget' ? ui.gitTarget : '',
      paths: fromChecked
        ? [...ui.gitChecked]
        : target.dataset.paths ? String(target.dataset.paths).split('\n').filter(Boolean) : [],
      repoRoot: toolWindowData.git?.root || '',
    })
    return true
  }
  return false
}

export function handleGitChangeAction({
  event,
  ui,
  persistDraft,
  render,
  gitGroupPaths,
  gitChanges,
  syncGitTree,
  syncGitCommitButtons,
  toolWindowData,
}) {
  const name = event.target?.name
  if (name === 'git-file') {
    const path = String(event.target.value || '')
    if (event.target.checked) ui.gitChecked.add(path)
    else ui.gitChecked.delete(path)
    ui.gitKnown.add(path)
    persistDraft()
    event.target.closest('.nc-file')?.classList.toggle('is-checked', event.target.checked)
    syncGitTree()
    syncGitCommitButtons()
    return true
  }
  if (name === 'git-amend') {
    ui.gitAmend = Boolean(event.target.checked)
    const last = (toolWindowData.git?.commits || [])[0]
    if (ui.gitAmend && !String(ui.gitCommitDraft || '').trim() && last?.message) {
      ui.gitCommitDraft = String(last.message)
      persistDraft()
    }
    render()
    return true
  }
  if (name === 'git-group') {
    const paths = gitGroupPaths(String(event.target.value || ''))
    for (const path of paths) {
      if (event.target.checked) ui.gitChecked.add(path)
      else ui.gitChecked.delete(path)
      ui.gitKnown.add(path)
    }
    persistDraft()
    render()
    return true
  }
  if (name === 'git-dir') {
    const dir = String(event.target.value || '')
    const paths = gitChanges()
      .map(item => String(item.path || ''))
      .filter(path => path === dir || path.startsWith(`${dir}/`))
    for (const path of paths) {
      if (event.target.checked) ui.gitChecked.add(path)
      else ui.gitChecked.delete(path)
      ui.gitKnown.add(path)
    }
    persistDraft()
    render()
    return true
  }
  return false
}
