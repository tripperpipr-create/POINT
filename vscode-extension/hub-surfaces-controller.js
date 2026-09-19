// Поверхности Хаба: какое окно открыть и куда вернуть человека.
//
// Двадцать семь методов об одном: Чертог, галерея миров, статистика, Docker,
// связи и четыре обличья компаньона — док, боковая панель, всплывающее окно и
// подглядывание. Они решают, где показать экран, и ничего не знают о том, что
// на нём.
//
// Граница держится на правиле продукта: диалог компаньона живёт в IDE, а
// Гильдия принадлежит Мастеру. Поэтому `showCompanionDock` и соседи открывают
// панели редактора, а не поверхности Хаба, — и смешивать их нельзя.

function createHubSurfaces({ collectExtensionGarbage, createChatDocuments, cursorRuntime, escapeHtml, normalizedWorkspaceRoot, vscode }) {
  function post(provider, message) {
    const companionTraffic = provider.companionWebviewTraffic(message?.type)
    if (!provider.hubVisible() && message?.type !== 'state' && !companionTraffic) return
    if (provider.companionPopup && (provider.companionPopup.visible !== false || companionTraffic)) void provider.companionPopup.webview.postMessage(message)
    if (provider.companionSidebar && (provider.companionSidebar.visible !== false || companionTraffic)) void provider.companionSidebar.webview.postMessage(message)
    if (provider.view && (provider.view.visible !== false || companionTraffic)) void provider.view.webview.postMessage(message)
    if (provider.panel && (provider.panel.visible !== false || companionTraffic)) void provider.panel.webview.postMessage(message)
    if (provider.connectionsPanel && provider.connectionsPanel.visible !== false && !companionTraffic) void provider.connectionsPanel.webview.postMessage(message)
    if (provider.statisticsPanel && provider.statisticsPanel.visible !== false && !companionTraffic) void provider.statisticsPanel.webview.postMessage(message)
    if (provider.dockerPanel && provider.dockerPanel.visible !== false && !companionTraffic) void provider.dockerPanel.webview.postMessage(message)
    if (!companionTraffic) {
      for (const view of provider.toolWindows.values()) {
        if (view && view.visible !== false) void view.webview.postMessage(message)
      }
    }
  }
  function postCursorRuntime(provider) {
    provider.post({ type: 'cursorRuntime', ...provider.cursorRuntimeState })
  }
  async function refreshCursorRuntime(provider) {
    provider.cursorRuntimeState = await cursorRuntime.status()
    provider.postCursorRuntime()
    return provider.cursorRuntimeState
  }
  async function show(provider, tab = 'overview') {
    provider.showWide(tab)
  }
  function showWide(provider, tab = 'overview') {
    if (!provider.agentsWindowMode) {
      void provider.openAgentsWindow(tab)
      return
    }
    provider.showWideHere(tab)
  }
  function normalizedAgentImprovementFocus(provider, agentId, constructorStep = 'review') {
    const normalizedAgentId = String(agentId || '').trim().slice(0, 160)
    if (!normalizedAgentId) return undefined
    const allowedSteps = new Set(['identity', 'role', 'mission', 'rules', 'brain', 'skills', 'tools', 'memory', 'permissions', 'review'])
    const step = allowedSteps.has(constructorStep) ? constructorStep : 'review'
    provider.agentImprovementFocusSequence += 1
    return {
      requestId: `agent-improvement-${Date.now()}-${provider.agentImprovementFocusSequence}`,
      agentId: normalizedAgentId,
      constructorStep: step,
    }
  }
  function focusAgentImprovement(provider, agentId, constructorStep = 'review') {
    const focus = provider.normalizedAgentImprovementFocus(agentId, constructorStep)
    if (!focus) return
    if (!provider.agentsWindowMode) {
      void provider.openAgentsWindow('agents', focus)
      return
    }
    provider.agentImprovementFocus = focus
    provider.showWideHere('agents')
  }
  async function openAgentsWindow(provider, tab = 'master', focus = undefined) {
    // Production uses a real Agents window. Moving an editor tab after a fixed
    // timeout was a race against the active editor: if focus changed during
    // those 100 ms, Point moved or focused the IDE editor instead of the Hub.
    // An auxiliary editor also belongs to the IDE window lifecycle, so it
    // cannot remain an independently usable Agent Hub. Keep that route only
    // as an explicit diagnostic fallback.
    if (process.env.POINT_AUXILIARY_HUB === '1') {
      provider.agentsWindowMode = true
      provider.auxiliaryHubMode = true
      if (focus?.agentId) provider.agentImprovementFocus = provider.normalizedAgentImprovementFocus(focus.agentId, focus.constructorStep)
      provider.showWideHere(tab || 'master')
      await new Promise(resolve => setTimeout(resolve, 100))
      await vscode.commands.executeCommand('workbench.action.moveEditorToNewWindow')
      return
    }
    const folder = provider.workspaceFolder()
    const payload = { tab: tab || 'master' }
    if (focus?.agentId) {
      payload.agentId = focus.agentId
      payload.constructorStep = focus.constructorStep
    }
    const initialQuery = `point-hub:${encodeURIComponent(JSON.stringify(payload))}`
    await vscode.commands.executeCommand('workbench.action.openAgentsWindow', {
      ...(folder ? { folderUri: folder.uri.toJSON() } : {}),
      initialQuery,
    })
  }
  async function postChatDirectory(provider) {
    if (!provider.hubPanelReady && !provider.panel) return
    if (provider.service.state !== 'running') return
    try {
      const directory = await provider.service.request('/api/master/directory', { allowStart: false })
      provider.lastChatDirectory = directory
      provider.post({ type: 'chatDirectory', directory })
    } catch (error) {
      provider.service.hostLog('warn', `[chat] каталог не собрался: ${error?.message || error}`)
      provider.post({ type: 'chatDirectory', directory: { currentWorkspaceId: '', worlds: [] }, failed: true })
    }
  }
  function takePendingMasterConversation(provider) {
    const pending = provider.pendingMasterConversation
    if (!pending?.id && !pending?.create) return undefined
    const folder = provider.workspaceFolder()
    if (!folder || normalizedWorkspaceRoot(folder.uri.fsPath) !== normalizedWorkspaceRoot(pending.path)) return undefined
    provider.pendingMasterConversation = undefined
    return pending
  }
  async function confirmLeavingBusyWorld(provider) {
    const busy = provider.masterTurnStreams?.size > 0
    if (!busy) return true
    const folder = provider.workspaceFolder()
    const answer = await vscode.window.showWarningMessage(
      `Мастер ещё отвечает в проекте «${folder?.name || 'текущем'}». Переключиться?`,
      { modal: true, detail: 'Ответ допишется и будет ждать вас в этом чате.' },
      'Переключиться',
    )
    return answer === 'Переключиться'
  }
  function knownProject(provider, value) {
    const wanted = normalizedWorkspaceRoot(String(value || ''))
    if (!wanted) return undefined
    return (provider.lastProjectList || []).find(item => normalizedWorkspaceRoot(item.path) === wanted)?.path
  }
  function openProjectGallery(provider) {
    provider.post({ type: 'projectGallery', open: true })
    void provider.postProjects?.()
  }
  function enterAgentsWindow(provider, tab = 'master', focus = undefined) {
    provider.agentsWindowMode = true
    provider.auxiliaryHubMode = false
    if (focus?.agentId) provider.agentImprovementFocus = provider.normalizedAgentImprovementFocus(focus.agentId, focus.constructorStep)
    void vscode.commands.executeCommand('setContext', 'point.agentsWindow', true)
    provider.showWideHere(tab || 'master')
  }
  function focusTab(provider, tab) {
    if (provider.selectedTab === 'onboarding' && tab !== 'onboarding') return
    provider.selectedTab = tab
  }
  function showWideHere(provider, tab = 'master') {
    if (provider.hubGarbageTimer) clearTimeout(provider.hubGarbageTimer)
    provider.hubGarbageTimer = undefined
    provider.selectedTab = tab
    if (provider.panel) {
      if (provider.auxiliaryHubMode) void vscode.commands.executeCommand('point.focusAgentHubWindow')
      else provider.panel.reveal(vscode.ViewColumn.One)
      provider.postState()
      provider.flushCompanionFocus()
      return
    }
    provider.panel = vscode.window.createWebviewPanel(
      'point.agentHub',
      'Агенты Point',
      vscode.ViewColumn.One,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(provider.context.extensionUri, 'media')],
      },
    )
    provider.panelStateSignature = ''
    provider.panel.iconPath = vscode.Uri.joinPath(provider.context.extensionUri, 'media', 'agent.svg')
    provider.panel.webview.html = provider.html(provider.panel.webview, 'wide')
    const panel = provider.panel
    const panelListeners = [
      panel.webview.onDidReceiveMessage(message => provider.handleMessage(message)),
      panel.onDidChangeViewState(event => { provider.onHubVisibility(Boolean(event.webviewPanel.visible)) }),
    ]
    panel.onDidDispose(() => {
      for (const listener of panelListeners.splice(0)) listener.dispose()
      if (provider.panel === panel) provider.panel = undefined
      if (provider.auxiliaryHubMode) {
        provider.agentsWindowMode = false
        provider.auxiliaryHubMode = false
      }
      provider.panelStateSignature = ''
      provider.hubPanelReady = false
      provider.scheduleHubGarbageCollection()
      if (!provider.hubVisible()) provider.onHubVisibility(false)
    })
    provider.postState()
  }
  function showStatistics(provider) {
    if (!provider.agentsWindowMode) {
      void provider.openAgentsWindow('statistics')
      return
    }
    provider.showWideHere('statistics')
    provider.post({ type: 'loadStatistics' })
  }
  function scheduleHubGarbageCollection(provider) {
    if (provider.hubGarbageTimer) clearTimeout(provider.hubGarbageTimer)
    provider.hubGarbageTimer = setTimeout(() => {
      provider.hubGarbageTimer = undefined
      if (provider.panel) return
      provider.collectWebviewGarbage()
      collectExtensionGarbage()
      void vscode.commands.executeCommand('point.collectHubGarbage').then(() => {}, () => {})
    }, 750)
  }
  function collectWebviewGarbage(provider) {
    const surfaces = [provider.view, provider.companionSidebar, provider.companionPopup, provider.connectionsPanel, provider.statisticsPanel, provider.dockerPanel, ...provider.toolWindows.values()]
    for (const surface of surfaces) {
      if (!surface?.webview) continue
      void Promise.resolve(surface.webview.postMessage({ type: 'collectGarbage' })).catch(() => {})
    }
  }
  function showDocker(provider) {
    if (!provider.agentsWindowMode) {
      void provider.openAgentsWindow('docker')
      return
    }
    provider.showWideHere('docker')
    provider.post({ type: 'dockerNeedRefresh' })
  }
  function showCompanionPeek(provider) {
    provider.companionFocusTarget = 'peek'
    if (provider.companionPopup) {
      provider.companionPopup.reveal(vscode.ViewColumn.Beside, true)
      provider.flushCompanionFocus()
      provider.pushCompanionThreadSync('peek')
      provider.postState(true)
      return 'peek'
    }
    provider.companionPopupReady = false
    provider.companionPopup = vscode.window.createWebviewPanel(
      'point.companionPeek',
      'Компаньон',
      { viewColumn: vscode.ViewColumn.Beside, preserveFocus: true },
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(provider.context.extensionUri, 'media')],
      },
    )
    provider.companionPopupStateSignature = ''
    provider.companionPopup.iconPath = vscode.Uri.joinPath(provider.context.extensionUri, 'media', 'agent.svg')
    provider.companionPopup.webview.html = provider.html(provider.companionPopup.webview, 'companion-peek')
    provider.companionPopup.webview.onDidReceiveMessage(message => provider.handleMessage(message), undefined, provider.context.subscriptions)
    provider.companionPopup.onDidChangeViewState(() => { provider.onHubVisibility(provider.hubVisible()) }, undefined, provider.context.subscriptions)
    provider.companionPopup.onDidDispose(() => {
      provider.companionPopup = undefined
      provider.companionPopupReady = false
      provider.companionPopupStateSignature = ''
      if (!provider.hubVisible()) provider.onHubVisibility(false)
    }, undefined, provider.context.subscriptions)
    provider.postState(true)
    provider.pushCompanionThreadSync('peek')
    return 'peek'
  }
  function showCompanionPopup(provider) {
    return provider.showCompanionPeek()
  }
  function showConnections(provider) {
    if (provider.connectionsPanel) {
      provider.connectionsPanel.reveal(vscode.ViewColumn.Active, false)
      provider.postState(true)
      return
    }
    provider.connectionsPanel = vscode.window.createWebviewPanel(
      'point.connections',
      'Подключения Point',
      vscode.ViewColumn.Active,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(provider.context.extensionUri, 'media')],
      },
    )
    provider.connectionsStateSignature = ''
    provider.connectionsPanel.iconPath = vscode.Uri.joinPath(provider.context.extensionUri, 'media', 'agent.svg')
    provider.connectionsPanel.webview.html = provider.html(provider.connectionsPanel.webview, 'connections')
    provider.connectionsPanel.webview.onDidReceiveMessage(message => provider.handleMessage(message), undefined, provider.context.subscriptions)
    provider.connectionsPanel.onDidChangeViewState(() => { provider.onHubVisibility(provider.hubVisible()) }, undefined, provider.context.subscriptions)
    provider.connectionsPanel.onDidDispose(() => {
      provider.connectionsPanel = undefined
      provider.connectionsStateSignature = ''
      if (!provider.hubVisible()) provider.onHubVisibility(false)
    }, undefined, provider.context.subscriptions)
    provider.postState(true)
  }
  async function waitForCompanionSurface(provider, surface, timeoutMs = 1600) {
    const ready = () => {
      if (surface === 'sidebar') return Boolean(provider.companionSidebar && provider.companionSidebar.visible !== false)
      if (surface === 'dock') return Boolean(provider.view && provider.view.visible !== false)
      return Boolean(provider.companionPopup && provider.companionPopup.visible !== false)
    }
    const deadline = Date.now() + timeoutMs
    while (!ready() && Date.now() < deadline) {
      await new Promise(resolve => setTimeout(resolve, 80))
    }
    return ready()
  }
  async function showCompanionDock(provider) {
    return provider.showCompanionSidebar()
  }
  async function showCompanionSidebar(provider) {
    provider.companionFocusTarget = 'sidebar'
    const closeLeftAssistant = Boolean(provider.view && provider.view.visible !== false)
    try {
      await vscode.commands.executeCommand('workbench.view.extension.pointCompanion')
    } catch { /* the view may already be visible */ }
    try {
      await vscode.commands.executeCommand('localAgent.companionChat.focus')
    } catch { /* opening the container above is sufficient on older hosts */ }
    try {
      await vscode.commands.executeCommand('workbench.action.focusAuxiliaryBar')
    } catch { /* older hosts may not expose the command */ }
    if (!await provider.waitForCompanionSurface('sidebar')) {
      provider.service.hostLog('warn', '[ui] assistant right sidebar did not become visible; opening editor chat fallback')
      return provider.showCompanionPeek()
    }
    if (closeLeftAssistant) {
      try {
        await vscode.commands.executeCommand('workbench.action.closeSidebar')
      } catch { /* the primary sidebar may already be closed */ }
    }
    provider.flushCompanionFocus()
    provider.pushCompanionThreadSync('sidebar')
    provider.postState(true)
    return 'sidebar'
  }
  function closeCompanionPopup(provider) {
    if (!provider.companionPopup) return
    provider.companionPopup.dispose()
  }

  return {
    post, postCursorRuntime, refreshCursorRuntime, show,
    showWide, normalizedAgentImprovementFocus, focusAgentImprovement, openAgentsWindow,
    postChatDirectory, takePendingMasterConversation, confirmLeavingBusyWorld, knownProject,
    openProjectGallery, enterAgentsWindow, focusTab, showWideHere,
    showStatistics, scheduleHubGarbageCollection, collectWebviewGarbage, showDocker,
    showCompanionPeek, showCompanionPopup, showConnections, waitForCompanionSurface,
    showCompanionDock, showCompanionSidebar, closeCompanionPopup,
  }
}

module.exports = { createHubSurfaces }
