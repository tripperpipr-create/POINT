// Перетаскивание в вебвью: файл между папками изменений Git и шаг workflow на
// новое место. Слушатели висят на корне, как все остальные: разметку
// перерисовывает render(), и слушатель на самом элементе умер бы с ним.
//
// Состояние живёт в main.js и приходит мешком ui; своего у модуля только номер
// перетаскиваемого шага — между dragstart и drop его больше никто не читает.

export function bindDragAndDrop({ root, vscode, ui, render, gitChanges, gitGroupOf, currentWorkflowForm }) {
  let draggedWorkflowStep = -1

  // Перетаскивание файла между папками изменений. Целями служат только папки
  // самого человека: конфликты и файлы вне репозитория никуда не переносятся.
  function gitDropTarget(node) {
    const group = node?.closest?.('.nc-group.tone-change')
    return group || undefined
  }
  function markGitDrop(group) {
    for (const node of root.querySelectorAll('.nc-group.is-drop')) {
      if (node !== group) node.classList.remove('is-drop')
    }
    if (group) group.classList.add('is-drop')
  }
  function endGitDrag() {
    ui.gitDragPath = ''
    markGitDrop(undefined)
    for (const node of root.querySelectorAll('.nc-file.is-dragging')) node.classList.remove('is-dragging')
  }
  root.addEventListener('dragstart', event => {
    const file = event.target.closest?.('.nc-file[draggable="true"]')
    if (file) {
      ui.gitDragPath = String(file.dataset.path || '')
      event.dataTransfer.effectAllowed = 'move'
      try { event.dataTransfer.setData('text/plain', ui.gitDragPath) } catch { /* не все среды дают буфер */ }
      file.classList.add('is-dragging')
      return
    }
    const card = event.target.closest?.('.workflow-step')
    if (!card) return
    draggedWorkflowStep = Number(card.dataset.index)
    event.dataTransfer.effectAllowed = 'move'
  })
  root.addEventListener('dragover', event => {
    if (ui.gitDragPath) {
      const group = gitDropTarget(event.target)
      markGitDrop(group)
      if (group) {
        event.preventDefault()
        if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
      }
      return
    }
    if (event.target.closest?.('.workflow-step')) event.preventDefault()
  })
  root.addEventListener('dragend', () => { if (ui.gitDragPath) endGitDrag() })
  root.addEventListener('drop', event => {
    if (ui.gitDragPath) {
      const group = gitDropTarget(event.target)
      const list = String(group?.dataset.list || '')
      const path = ui.gitDragPath
      const from = gitChanges().find(item => String(item.path) === path)
      endGitDrag()
      if (!group) return
      event.preventDefault()
      if (!list || !from || gitGroupOf(from) === list) return
      ui.gitPendingAction = `moveToList:${path}`
      ui.gitNotice = undefined
      render()
      vscode.postMessage({ type: 'gitAction', action: 'moveToList', path, list, paths: [], repoRoot: ui.toolWindowData.git?.root || '' })
      return
    }
    const card = event.target.closest?.('.workflow-step')
    const targetIndex = Number(card?.dataset.index)
    if (!card || draggedWorkflowStep < 0 || targetIndex === draggedWorkflowStep) return
    event.preventDefault()
    const workflow = currentWorkflowForm()
    if (workflow) {
      const [step] = workflow.steps.splice(draggedWorkflowStep, 1)
      workflow.steps.splice(targetIndex, 0, step)
      ui.workflowDraft = workflow
      render()
    }
    draggedWorkflowStep = -1
  })
}
