// Early hub / tool-window click actions that do not belong to Master or Git.

export function handleHubClickAction({
  action,
  target,
  vscode,
  persistDraft,
  render,
  toolWindowKind,
  getToolWindowData,
  setToolWindowData,
  getToolLogFilter,
  setToolLogFilter,
  setTransientError,
  setPlannerFallbackNotice,
  handleModelChipAction,
}) {
  if (action === 'tool-command') {
    vscode.postMessage({ type: 'toolCommand', command: target.dataset.command || '' })
    setTimeout(() => vscode.postMessage({ type: 'loadToolWindowState', kind: toolWindowKind() }), 500)
    return true
  }
  if (action === 'refresh-tool-window') {
    const kind = toolWindowKind()
    const toolWindowData = getToolWindowData()
    setToolWindowData({ ...toolWindowData, [kind]: { ...(toolWindowData[kind] || {}), loaded: false } })
    render()
    vscode.postMessage({ type: 'loadToolWindowState', kind })
    return true
  }
  if (action === 'set-log-filter') {
    setToolLogFilter(target.dataset.filter || 'all')
    persistDraft()
    render()
    return true
  }
  // Схему Мастер собирает под квест, и она переживает его, держа исполнителей в
  // своих узлах: пока схему нельзя было удалить, персонаж из ростера не
  // удалялся никогда — отказ отправлял «заменить его в схеме», а редактор схем
  // скрыт.
  // Наряд без квеста — договор, по которому работа не пошла. Он принадлежит
  // разговору и уходит вместе с ним, но повода держать его до удаления всей
  // переписки нет.
  if (action === 'delete-work-order-v2') {
    vscode.postMessage({ type: 'deleteWorkOrderV2', id: target.dataset.id || '' })
    return true
  }
  if (action === 'delete-flow') {
    vscode.postMessage({ type: 'deleteFlow', id: target.dataset.id || '' })
    return true
  }
  if (action === 'choose-project') {
    vscode.postMessage({type:'chooseProject'})
    return true
  }
  if (action === 'start-server') {
    vscode.postMessage({type:'startServer'})
    return true
  }
  if (action === 'manage-trust') {
    vscode.postMessage({type:'manageTrust'})
    return true
  }
  if (action === 'restart-server') {
    vscode.postMessage({type:'restartServer'})
    return true
  }
  if (action === 'show-output') {
    vscode.postMessage({type:'showOutput'})
    return true
  }
  if (handleModelChipAction({ action, target, vscode })) {
    render()
    return true
  }
  if (action === 'dismiss-error') {
    setTransientError('')
    render()
    return true
  }
  if (action === 'dismiss-planner-fallback') {
    setPlannerFallbackNotice(null)
    render()
    return true
  }
  if (action === 'create-intake') {
    const url = String(document.querySelector('#intake-url-input')?.value || '').trim()
    if (!url) {
      setTransientError('Укажите HTTPS URL источника')
      render()
      return true
    }
    vscode.postMessage({ type: 'createIntake', url })
    return true
  }
  if (action === 'select-intake') {
    vscode.postMessage({ type: 'selectIntake', id: target.dataset.id || '' })
    return true
  }
  if (action === 'approve-intake') {
    vscode.postMessage({
      type: 'approveIntake',
      id: target.dataset.id || '',
      expectedVersion: Number(target.dataset.version || 0),
    })
    return true
  }
  if (action === 'expand-intake-network') {
    const host = window.prompt('Добавить сетевой хост (FQDN без схемы):', '')
    if (!host) return true
    vscode.postMessage({
      type: 'expandIntake',
      id: target.dataset.id || '',
      expectedVersion: Number(target.dataset.version || 0),
      networkHosts: [host],
    })
    return true
  }
  return false
}
