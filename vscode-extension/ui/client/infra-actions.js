// Клик-ветки инфраструктуры: Docker, серверы, связи, базы и системный бэкап.
//
// Третья часть одной темы. Отрисовка давно живёт в `infrastructure-views.js`,
// хост — в `vscode-extension/infra-controller.js`, а нажатия оставались в общем
// обработчике `main.js` вперемешку с Мастером, Гильдией и прогонами. Тридцать
// веток об одном лежали в шести местах файла.
//
// Здесь только доставка намерения: ветка меняет ячейку интерфейса и просит
// ядро. Ни одна из них не решает за ядро — ни прав, ни изоляции.

export function handleInfraClickAction({ action, target, ui, root, vscode, render, saveConnectionFromFields }) {
  if (action === 'reload-docker') {
    ui.dockerStatus = 'loading'
    ui.dockerLogs = undefined
    render()
    vscode.postMessage({ type: 'loadDocker' })
    return true
  }
  if (action === 'docker-control') {
    vscode.postMessage({ type: 'dockerContainerAction', action: target.dataset.actionKind, container: target.dataset.container })
    return true
  }
  if (action === 'docker-logs') {
    ui.dockerLogsContainer = target.dataset.container || ''
    vscode.postMessage({ type: 'dockerLogs', container: ui.dockerLogsContainer })
    return true
  }
  if (action === 'docker-clear-logs') {
    ui.dockerLogs = undefined
    ui.dockerLogsContainer = ''
    render()
    return true
  }
  if (action === 'docker-terminal-logs') {
    vscode.postMessage({ type: 'dockerOpenTerminal', mode: 'logs', container: target.dataset.container })
    return true
  }
  if (action === 'docker-terminal-ps') {
    vscode.postMessage({ type: 'dockerOpenTerminal', mode: 'ps' })
    return true
  }
  if (action === 'docker-terminal-shell') {
    vscode.postMessage({ type: 'dockerOpenTerminal', mode: 'shell', container: target.dataset.container })
    return true
  }
  if (action === 'enable-docker-sandbox') {
    // Настройку и перезапуск ядра делает расширение: у вебвью нет доступа ни к
    // конфигурации, ни к процессу. Ответ придёт обычным обновлением состояния.
    vscode.postMessage({type:'enableDockerSandbox'})
    ui.masterComposeNote='Включаем Docker sandbox и перезапускаем ядро…'
    render()
    return true
  }
  if (action === 'probe-server') {
    vscode.postMessage({ type: 'probeServerProfile', id: target.dataset.id })
    return true
  }
  if (action === 'edit-server') {
    ui.serverEditingId = target.dataset.id || ''
    render()
    requestAnimationFrame(() => {
      root.querySelector('#server-form')?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
      root.querySelector('#server-name')?.focus?.()
    })
    return true
  }
  if (action === 'delete-server') {
    if (ui.serverEditingId === target.dataset.id) ui.serverEditingId = ''
    vscode.postMessage({ type: 'deleteServerProfile', id: target.dataset.id })
    return true
  }
  if (action === 'cancel-edit-server') {
    ui.serverEditingId = ''
    render()
    return true
  }
  if (action === 'open-server-terminal') {
    vscode.postMessage({ type: 'openServerTerminal', id: target.dataset.id })
    return true
  }
  if (action === 'list-server-path') {
    vscode.postMessage({ type: 'listServerRemote', id: target.dataset.id, path: target.dataset.path || '~' })
    return true
  }
  if (action === 'edit-connection') {
    ui.connectionEditingId = target.dataset.id || ''
    render()
    root.querySelector('#connection-form')?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
    return true
  }
  if (action === 'save-connection') { saveConnectionFromFields(); return true }
  if (action === 'cancel-edit-connection') {
    ui.connectionEditingId = ''
    render()
    return true
  }
  if (action === 'delete-connection') {
    vscode.postMessage({ type: 'deleteConnection', id: target.dataset.id })
    return true
  }
  if (action === 'default-connection') {
    vscode.postMessage({ type: 'defaultConnection', id: target.dataset.id })
    return true
  }
  if (action === 'probe-connection') {
    vscode.postMessage({ type: 'probeConnection', id: target.dataset.id })
    return true
  }
  if (action === 'select-db') {
    ui.dbSelectedId = target.dataset.id || ''
    ui.dbQueryResult = undefined
    ui.dbSchemaResult = undefined
    ui.dbWritePending = null
    render()
    return true
  }
  if (action === 'test-db') {
    vscode.postMessage({ type: 'testDBConnection', id: target.dataset.id })
    return true
  }
  if (action === 'schema-db') {
    vscode.postMessage({ type: 'schemaDBConnection', id: target.dataset.id || ui.dbSelectedId })
    return true
  }
  if (action === 'edit-db') {
    ui.dbEditingId = target.dataset.id || ''
    ui.dbSelectedId = ui.dbEditingId
    render()
    requestAnimationFrame(() => {
      root.querySelector('#db-connection-form')?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
      root.querySelector('#db-name')?.focus?.()
    })
    return true
  }
  if (action === 'cancel-edit-db') {
    ui.dbEditingId = ''
    render()
    return true
  }
  if (action === 'delete-db') {
    if (ui.dbEditingId === target.dataset.id) ui.dbEditingId = ''
    vscode.postMessage({ type: 'deleteDBConnection', id: target.dataset.id })
    return true
  }
  if (action === 'apply-db-write') {
    if (!ui.dbWritePending?.connectionId || !ui.dbWritePending?.sql) return true
    ui.dbQueryStatus = 'loading'
    const pending = ui.dbWritePending
    ui.dbWritePending = null
    render()
    vscode.postMessage({ type: 'queryDBConnection', connectionId: pending.connectionId, sql: pending.sql, allowWrite: true, approved: true })
    return true
  }
  if (action === 'ignore-db-write') {
    ui.dbWritePending = null
    render()
    return true
  }
  if (action === 'create-system-backup') {
    ui.statisticsStatus = 'loading'
    render()
    vscode.postMessage({ type: 'createSystemBackup' })
    return true
  }
  if (action === 'restore-system-backup') {
    vscode.postMessage({ type: 'restoreSystemBackup' })
    return true
  }
  return false
}
