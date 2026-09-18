const Module = require('module')
const path = require('path')

const commands = []
const commandCalls = []
const posted = []
const workspaceUri = { scheme: 'file', fsPath: process.cwd(), toJSON: () => ({ scheme: 'file', path: process.cwd().replace(/\\/g, '/') }) }
const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      workspace: {
        isTrusted: true,
        workspaceFolders: [{ name: 'smoke', uri: workspaceUri }],
        getConfiguration: () => ({ get: (key, fallback) => key === 'autoStart' ? false : fallback }),
        asRelativePath: (uri, _includeWorkspace) => {
          const value = String(uri?.fsPath || uri?.path || uri || '').replace(/\\/g, '/')
          if (value.includes('/outside/')) return '../outside/secret.go'
          return path.basename(value)
        },
        onDidChangeWorkspaceFolders: () => ({ dispose() {} }),
        onDidChangeConfiguration: () => ({ dispose() {} }),
      },
      Uri: {
        joinPath: (...parts) => ({ fsPath: parts.join('/'), scheme: 'file', toString: () => parts.join('/') }),
        parse: value => ({ fsPath: String(value), scheme: 'file', toString: () => String(value) }),
        file: fsPath => ({ fsPath: String(fsPath), scheme: 'file', toString: () => String(fsPath) }),
      },
      window: {
        createWebviewPanel: () => {
          const panel = {
            visible: true,
            webview: {
              html: '',
              cspSource: 'vscode',
              asWebviewUri: uri => uri,
              postMessage: message => posted.push(message),
              onDidReceiveMessage: () => ({ dispose() {} }),
            },
            reveal() { panel.revealed = true },
            onDidChangeViewState: () => ({ dispose() {} }),
            onDidDispose: () => ({ dispose() {} }),
            dispose() {
              panel.visible = false
              if (typeof panel._onDispose === 'function') panel._onDispose()
            },
          }
          return panel
        },
        showInformationMessage: async () => undefined,
        showErrorMessage: async () => undefined,
        showWarningMessage: async () => undefined,
      },
      ViewColumn: { One: 1, Beside: 2, Active: -1 },
      commands: {
		executeCommand: async (name, ...args) => { commands.push(name); commandCalls.push({ name, args }) },
      },
      StatusBarAlignment: { Left: 1 },
      ThemeColor: class { constructor(id) { this.id = id } },
    }
  }
  return originalLoad.call(this, request, parent, isMain)
}

const extension = require(path.resolve(__dirname, '..', 'vscode-extension', 'extension.js'))
Module._load = originalLoad
const { AgentViewProvider, openCompanionChat, companionWorkspaceRelativePath, workspaceFileUri, formatCompanionFailure, formatCompanionChatError } = extension.__test

if (companionWorkspaceRelativePath({ fsPath: 'C:/proj/main.go' }) !== 'main.go') {
  throw new Error('companionWorkspaceRelativePath did not keep an in-workspace file')
}
if (companionWorkspaceRelativePath({ fsPath: 'C:/outside/secret.go' }) !== '') {
  throw new Error('companionWorkspaceRelativePath leaked a parent-relative path')
}
if (!formatCompanionFailure({ command: 'go test', detail: 'FAIL\nundefined: handler' }).includes('go test')) {
  throw new Error('formatCompanionFailure dropped the failed command')
}
if (!formatCompanionChatError(new Error('connect ECONNREFUSED 127.0.0.1:11434')).includes('Нет связи с моделью')) {
  throw new Error('formatCompanionChatError did not translate a connection failure')
}
if (!workspaceFileUri('main.go')?.fsPath) {
  throw new Error('workspaceFileUri dropped an in-workspace file')
}
if (workspaceFileUri('../secret.go') || workspaceFileUri('..\\secret.go')) {
  throw new Error('workspaceFileUri leaked a parent-relative path')
}

async function main() {
  const serviceRequests = []
  const service = {
    state: 'running',
    workspaceFolder: () => ({ name: 'smoke', uri: workspaceUri }),
    hostLog() {},
    request: async (route, options = {}) => {
      serviceRequests.push({ route, options })
      if (route === '/api/quest-proposals/decide') {
        const request = JSON.parse(options.body || '{}')
        if (request.proposalId === 'proposal-fail') throw new Error('quest save failed')
        return { quest: { id: 'quest-model-plan' }, orchestratorMode: 'model', orchestratorModel: 'planner-model', orchestratorNote: 'Plan validated' }
      }
      if (route === '/api/companion/actions/decide') {
        const request = JSON.parse(options.body || '{}')
        if (request.proposalId === 'action-fail') throw new Error('action save failed')
      }
      return {}
    },
    requestNdjson: async (_route, options = {}) => {
      options.onProgress?.({ step: 'model', status: 'running' })
      options.onDelta?.({ reply: 'Единый поток' })
      return { reply: 'Единый ответ', mode: 'model' }
    },
  }
  const provider = new AgentViewProvider({
    subscriptions: [],
    extensionUri: { fsPath: path.join(process.cwd(), 'vscode-extension'), scheme: 'file' },
    globalState: { get: () => true },
    secrets: { get: async ref => ref === 'orchestrator-secret-ref' ? 'orchestrator-secret' : '' },
  }, service, { append() {}, appendLine() {} })

  provider.boot = {
    orchestrator: { provider: 'openai', providerPreset: 'openai', model: 'planner-model' },
    connections: [{ provider: 'openai', presetId: 'openai', secretRef: 'orchestrator-secret-ref' }],
    executions: [],
  }
  await provider.handleMessage({ type: 'decideQuestProposal', proposalId: 'proposal-model-plan', action: 'start' })
  const decisionRequest = serviceRequests.find(item => item.route === '/api/quest-proposals/decide')
  const decisionBody = JSON.parse(decisionRequest?.options?.body || '{}')
  if (decisionBody.orchestratorApiKey !== 'orchestrator-secret') {
    throw new Error('Quest Start did not forward the Orchestrator credential from SecretStorage')
  }
  if (decisionRequest?.options?.timeoutMs !== 90_000) {
    throw new Error(`Quest Start still uses the generic short timeout: ${decisionRequest?.options?.timeoutMs}`)
  }

  const beforeQueueDecision = serviceRequests.length
  await provider.handleMessage({
    type: 'resolveDecision', path: '/api/quest-proposals/decide', field: 'action', value: 'start',
    idField: 'proposalId', id: 'proposal-from-queue',
  })
  const queueDecision = serviceRequests.slice(beforeQueueDecision).find(item => item.route === '/api/quest-proposals/decide')
  if (queueDecision?.options?.timeoutMs !== 90_000) {
    throw new Error(`Queue Quest Start still uses the generic short timeout: ${queueDecision?.options?.timeoutMs}`)
  }

  const decisionPosts = []
  const forwardPost = provider.post.bind(provider)
  provider.post = message => {
    decisionPosts.push(message)
    forwardPost(message)
  }
  await provider.handleMessage({ type: 'decideQuestProposal', proposalId: 'proposal-edit', action: 'modify', title: 'Edited' })
  if (!decisionPosts.some(message => message.type === 'questProposalModified' && message.proposalId === 'proposal-edit')) {
    throw new Error('Quest Modify did not acknowledge the exact editor that can now close')
  }
  await provider.handleMessage({ type: 'decideCompanionAction', proposalId: 'action-edit', action: 'modify', name: 'Edited agent' })
  if (!decisionPosts.some(message => message.type === 'companionActionModified' && message.proposalId === 'action-edit')) {
    throw new Error('Companion action Modify did not acknowledge the exact editor that can now close')
  }
  await provider.handleMessage({ type: 'decideQuestProposal', proposalId: 'proposal-fail', action: 'modify' })
  await provider.handleMessage({ type: 'decideCompanionAction', proposalId: 'action-fail', action: 'modify' })
  if (!decisionPosts.some(message => message.type === 'error' && message.request === '/api/quest-proposals/decide')) {
    throw new Error('Quest decision error did not identify the editor request that must stay open')
  }
  if (!decisionPosts.some(message => message.type === 'error' && message.request === '/api/companion/actions/decide')) {
    throw new Error('Companion action error did not identify the editor request that must stay open')
  }

  await openCompanionChat(provider, () => {}, { message: 'Разбери main.go', send: true, surface: 'peek' })
  if (!provider.companionPopup) {
    throw new Error('Peek companion chat did not open the compact panel')
  }
  if (!String(provider.companionPopup.webview.html || '').includes('data-layout="companion-peek"')) {
    throw new Error('Peek surface did not use companion-peek layout')
  }
  if (String(provider.companionPopup.webview.html || '').includes('companion-popup-shell')) {
    throw new Error('Peek still rendered the old window-in-window shell')
  }
  if (provider.panel) throw new Error('Companion chat unexpectedly opened the Hub panel')
  if (!provider.pendingCompanionFocus || provider.pendingCompanionFocus.message !== 'Разбери main.go' || !provider.pendingCompanionFocus.send) {
    throw new Error('Companion focus was not queued until the peek is ready')
  }
  if (posted.some(message => message.type === 'focusCompanion')) {
    throw new Error('focusCompanion was posted before the companion peek could receive it')
  }

  const peekPosts = []
  provider.companionPopup.webview.postMessage = message => peekPosts.push(message)
  await provider.handleMessage({ type: 'ready', surface: 'companion-peek' })
  if (!peekPosts.some(message => message.type === 'state')) {
    throw new Error('Companion peek stayed on the loading spinner because it never received state')
  }
  const focus = peekPosts.find(message => message.type === 'focusCompanion')
  if (!focus || focus.message !== 'Разбери main.go' || focus.send !== true) {
    throw new Error('Ready companion peek did not receive focusCompanion with the IDE prompt')
  }
  if (provider.pendingCompanionFocus) {
    throw new Error('Queued companion focus was not flushed')
  }
  if (!provider.companionPopupReady) {
    throw new Error('companionPopupReady was not set after companion-peek ready')
  }

  provider.selectedTab = 'quests'
  provider.postState()
  if (!peekPosts.some(message => message.type === 'state' && message.selectedTab === 'quests')) {
    throw new Error('Companion peek did not receive a later state update while Hub was closed')
  }

  provider.post({ type: 'companionChatProgress', step: 'memory', status: 'running', phase: 'gather', requestId: 1 })
  if (!peekPosts.some(message => message.type === 'companionChatProgress' && message.step === 'memory')) {
    throw new Error('Companion progress steps were not delivered to the peek surface')
  }

  provider.post({ type: 'companionChatDelta', reply: 'Часть ответа', requestId: 1 })
  if (!peekPosts.some(message => message.type === 'companionChatDelta' && message.reply === 'Часть ответа')) {
    throw new Error('Companion streaming delta was not delivered to the peek')
  }

  provider.post({ type: 'companionChatResult', response: { reply: 'ok' } })
  if (!peekPosts.some(message => message.type === 'companionChatResult' && message.response?.reply === 'ok')) {
    throw new Error('Companion reply was not delivered to the peek')
  }

  // Right sidebar full chat surface.
  const sidebarPosts = []
  provider.companionSidebar = {
    visible: true,
    webview: { postMessage: message => sidebarPosts.push(message) },
  }
  provider.companionSidebarReady = true
  provider.boot = {
    companionActionProposals: [{ id: 'team-draft', status: 'pending' }],
    teams: [],
  }
  provider.postState(true)
  const sidebarStatesBeforeApply = sidebarPosts.filter(message => message.type === 'state').length
  provider.boot = {
    companionActionProposals: [{ id: 'team-draft', status: 'applied' }],
    teams: [{ id: 'team-qa', agentIds: ['agent-qa'] }],
  }
  provider.postState()
  if (sidebarPosts.filter(message => message.type === 'state').length !== sidebarStatesBeforeApply + 1) {
    throw new Error('Applying a Team proposal did not invalidate the sidebar state; the active card can be submitted twice')
  }
  provider.companionFocusTarget = 'sidebar'
  provider.queueCompanionFocus({ message: 'Полный диалог', send: false, surface: 'sidebar' })
  if (!sidebarPosts.some(message => message.type === 'focusCompanion' && message.message === 'Полный диалог')) {
    throw new Error('Sidebar companion did not receive focusCompanion')
  }

  const peekBeforeUnified = peekPosts.length
  const sidebarBeforeUnified = sidebarPosts.length
  await provider.handleMessage({ type: 'companionChat', message: 'Единый запрос', requestId: 900 })
  const peekUnified = peekPosts.slice(peekBeforeUnified)
  const sidebarUnified = sidebarPosts.slice(sidebarBeforeUnified)
  const peekStarted = peekUnified.find(message => message.type === 'companionChatStarted')
  const sidebarStarted = sidebarUnified.find(message => message.type === 'companionChatStarted')
  if (!peekStarted?.requestId || peekStarted.requestId !== sidebarStarted?.requestId) {
    throw new Error('Peek and sidebar did not receive one shared Companion request id')
  }
  if (!peekUnified.some(message => message.type === 'companionChatDelta' && message.requestId === peekStarted.requestId)
      || !sidebarUnified.some(message => message.type === 'companionChatResult' && message.requestId === peekStarted.requestId)) {
    throw new Error('Unified Companion stream/result did not reach both chat surfaces')
  }
  if (provider.companionThreadCache.loading || provider.companionThreadCache.requestId || provider.companionThreadCache.messages.at(-1)?.content !== 'Единый ответ') {
    throw new Error('Host Companion transcript did not finalize after the shared request')
  }

  provider.companionActiveChatRequestId = 77
  provider.companionChatAbort = new AbortController()
  provider.beginCompanionThread('Останови меня', 77)
  const beforeSharedStop = sidebarPosts.length
  await provider.handleMessage({ type: 'stopCompanionChat', requestId: 77 })
  if (!sidebarPosts.slice(beforeSharedStop).some(message => message.type === 'companionChatStopped' && message.requestId === 77)) {
    throw new Error('Stopping Companion was not mirrored to the sidebar')
  }
  if (provider.companionThreadCache.loading || provider.companionThreadCache.messages.at(-1)?.mode !== 'cancelled') {
    throw new Error('Host Companion transcript stayed busy after stop')
  }

  provider.rememberCompanionThread({
    messages: [{ role: 'user', content: 'Привет' }, { role: 'assistant', content: 'Часть', mode: 'streaming' }],
    draft: '',
    streamReply: 'Часть',
    loading: true,
  })
  provider.pushCompanionThreadSync('sidebar')
  if (!sidebarPosts.some(message => message.type === 'companionThreadSync' && message.streamReply === 'Часть')) {
    throw new Error('Sidebar did not receive companionThreadSync for an in-flight turn')
  }

  // Prefer the already-open sidebar instead of opening a new peek.
  await openCompanionChat(provider, () => {}, { message: 'Ещё вопрос', send: false, surface: 'peek' })
  if (provider.companionFocusTarget !== 'sidebar') {
    throw new Error('openCompanionChat did not prefer the live sidebar surface')
  }
  if (!sidebarPosts.some(message => message.type === 'focusCompanion' && message.message === 'Ещё вопрос')) {
    throw new Error('Preferred live sidebar did not receive the new focus message')
  }

  // Point exposes one assistant entity. Legacy callers that still request the
  // former left dock are routed into the canonical right tool window.
  await openCompanionChat(provider, () => {}, { surface: 'dock', forceSurface: true, message: 'Основной чат', send: false })
  if (provider.companionFocusTarget !== 'sidebar' || commands.includes('localAgent.chatView.focus')) {
    throw new Error('Legacy assistant dock did not route to the single right assistant')
  }
  if (!sidebarPosts.some(message => message.type === 'focusCompanion' && message.message === 'Основной чат')) {
    throw new Error('Canonical right assistant did not receive focusCompanion')
  }

  // Auxiliary mode remains an explicit diagnostic fallback. Production uses
  // the independent Sessions-based Agents window; this unit smoke exercises
  // the old route without needing a second mocked extension host.
  process.env.POINT_AUXILIARY_HUB = '1'
  provider.showWide('overview')
  await new Promise(resolve => setTimeout(resolve, 125))
  delete process.env.POINT_AUXILIARY_HUB
  if (!provider.panel || !provider.agentsWindowMode || !provider.auxiliaryHubMode) {
    throw new Error('Hub did not enter shared-host auxiliary window mode')
  }
  if (!commands.includes('workbench.action.moveEditorToNewWindow') || commands.includes('workbench.action.openAgentsWindow')) {
    throw new Error('Hub did not use the production auxiliary-window route')
  }
	await provider.handleMessage({ type: 'focusHub', tab: 'agents', agentId: 'agent-backend', constructorStep: 'skills' })
	const improvementState = posted.filter(message => message.type === 'state' && message.agentImprovementFocus).at(-1)
	if (improvementState?.selectedTab !== 'agents' || improvementState.agentImprovementFocus.agentId !== 'agent-backend' || improvementState.agentImprovementFocus.constructorStep !== 'skills') {
	  throw new Error('Auxiliary Hub lost the pending improvement focus')
	}
	await provider.handleMessage({ type: 'agentImprovementFocused', requestId: improvementState.agentImprovementFocus.requestId })
	if (provider.agentImprovementFocus) throw new Error('Acknowledged agent improvement focus can reopen the constructor')
  provider.post({ type: 'companionChatResult', response: { reply: 'hub-ok' } })
  if (!peekPosts.some(message => message.type === 'companionChatResult' && message.response?.reply === 'hub-ok')) {
    throw new Error('Companion reply stopped reaching the peek after Hub opened')
  }
  if (!sidebarPosts.some(message => message.type === 'companionChatResult' && message.response?.reply === 'hub-ok')) {
    throw new Error('Companion reply did not reach the right sidebar after Hub opened')
  }

  if (!commands.some(name => name === 'workbench.action.focusAuxiliaryBar' || name === 'localAgent.companionChat.focus' || name === 'workbench.view.extension.pointCompanion')) {
    // openCompanionChat(sidebar) is covered separately below
  }

  await openCompanionChat(provider, () => {}, { surface: 'sidebar', forceSurface: true })
  if (!commands.includes('workbench.action.focusAuxiliaryBar')) {
    throw new Error('Sidebar companion open did not focus the auxiliary bar')
  }
  if (commands.includes('workbench.action.closeSidebar')) {
    throw new Error('Opening the single right assistant unexpectedly closed the project sidebar')
  }

  // Change Set chains must be applied from the oldest prerequisite to the
  // selected result, otherwise a later agent can be merged without the state
  // it actually worked on.
  provider.boot = {
    ...(provider.boot || {}),
    changeSets: [
      { id: 'changes-parent', status: 'pending', dependsOn: [] },
      { id: 'changes-child', status: 'conflict', dependsOn: ['changes-parent'] },
    ],
  }
  const beforeChain = serviceRequests.length
  await provider.handleMessage({ type: 'applyChangeSetChain', id: 'changes-child' })
  const chainRoutes = serviceRequests
    .slice(beforeChain)
    .map(item => item.route)
    .filter(route => route.includes('/api/change-sets/') && route.endsWith('/apply'))
  if (chainRoutes.join(',') !== '/api/change-sets/changes-parent/apply,/api/change-sets/changes-child/apply') {
    throw new Error(`Change Set dependency chain applied in the wrong order: ${chainRoutes.join(',')}`)
  }

  const beforeMergeResolution = serviceRequests.length
  await provider.handleMessage({
    type: 'resolveFlowMerge',
    flowRunId: 'flow-run-merge',
    nodeId: 'integrator',
    resolution: { path: 'shared.txt', strategy: 'use_parent', executionId: 'branch-b' },
  })
  const mergeResolutionRequest = serviceRequests.slice(beforeMergeResolution).find(item => item.route.includes('/merge/resolve'))
  const mergeResolutionBody = JSON.parse(mergeResolutionRequest?.options?.body || '{}')
  if (mergeResolutionRequest?.route !== '/api/flow-runs/flow-run-merge/nodes/integrator/merge/resolve'
      || mergeResolutionBody.path !== 'shared.txt' || mergeResolutionBody.executionId !== 'branch-b') {
    throw new Error('Flow sandbox merge resolution was not routed to Point Core')
  }

  const fs = require('fs')
  const clientRoot = path.resolve(__dirname, '..', 'vscode-extension', 'ui', 'client')
  const clientFiles = []
  const visitClient = directory => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const target = path.join(directory, entry.name)
      if (entry.isDirectory()) visitClient(target)
      else if (entry.isFile() && entry.name.endsWith('.js')) clientFiles.push(target)
    }
  }
  visitClient(clientRoot)
  const mediaMain = clientFiles.sort().map(file => fs.readFileSync(file, 'utf8')).join('\n')
  const mediaCss = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'media', 'style.css'), 'utf8')
  const packageJson = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'package.json'), 'utf8')
  const overlaySource = fs.readFileSync(path.resolve(__dirname, '..', 'distribution', 'apply-overlay.mjs'), 'utf8')
  const activityCss = fs.readFileSync(path.resolve(__dirname, '..', 'distribution', 'resources', 'point-activitybar.css'), 'utf8')
  const workbenchCss = fs.readFileSync(path.resolve(__dirname, '..', 'distribution', 'resources', 'point-workbench.css'), 'utf8')
  // Ход работы показывает «думающий» пузырь: отдельная полоса активности была
  // мёртвым кодом, и сторож на её имя проверял след, а не поведение.
  if (!mediaMain.includes('data-companion-thinking') || !mediaMain.includes('companionStepLabel')) {
    throw new Error('Companion progress indicator helpers are missing from media/main.js')
  }
  if (!mediaMain.includes('formatCompanionMarkdown') || !mediaMain.includes('companionChatDelta') || !mediaMain.includes('patchCompanionStreamingBubble')) {
    throw new Error('Companion streaming / markdown helpers are missing from media/main.js')
  }
  if (!mediaMain.includes('copy-companion-message') || !mediaMain.includes('copy-companion-code') || !mediaMain.includes('companion-scroll-latest')) {
    throw new Error('Companion copy/new-message affordances are missing')
  }
  for (const action of ['regenerate-companion-message', 'feedback-companion-message', 'open-companion-message-details', 'new-companion-thread', 'show-companion-archives']) {
    if (!mediaMain.includes(action)) throw new Error(`Companion answer/chat action is missing: ${action}`)
  }
  if (!mediaCss.includes('companion-stream-cursor') || !mediaCss.includes('companion-file-link')) {
    throw new Error('Companion streaming/markdown styles are missing')
  }
  if (!mediaCss.includes('companion-code-actions') || !mediaCss.includes('companion-scroll-latest')) {
    throw new Error('Companion copy/new-message styles are missing')
  }
  if (!mediaMain.includes('companionComposeContextHtml') || !mediaMain.includes('companion-chat-menu') || !mediaMain.includes('companion-chat-notices')) {
    throw new Error('Minimal Companion chat structure is missing')
  }
  if (!mediaMain.includes('companionGettingStartedHtml') || !mediaMain.includes('companion-prefill') || !mediaMain.includes('Подобрать отряд')) {
    throw new Error('Primary assistant getting-started actions are missing')
  }
  if (!mediaCss.includes('Companion chat — minimal conversation surface.') || !mediaCss.includes('.companion-compose-context') || !mediaCss.includes('.companion-chat-menu')) {
    throw new Error('Minimal Companion chat styles are missing')
  }
  if (!mediaCss.includes('body[data-layout="companion"]') || !mediaCss.includes('.companion-starters')) {
    throw new Error('Primary Activity Bar assistant layout styles are missing')
  }
  if (mediaMain.includes('${chatHeader}${companionIdeNowHtml()}${companionActivityStripHtml()}')) {
    throw new Error('Companion chat regressed to stacked context/activity chrome')
  }
  if (mediaMain.includes('companion-popup-shell') || mediaCss.includes('companion-popup-shell')) {
    throw new Error('Peek/sidebar UI still references companion-popup-shell')
  }
  if (!mediaMain.includes('companionPeekContextChipHtml') || !mediaCss.includes('companion-peek-context')) {
    throw new Error('Peek context chip is missing')
  }
  if (!mediaMain.includes('changeSetDependencyChain') || !mediaMain.includes('apply-changeset-chain') || !mediaMain.includes('applyChangeSetChain')) {
    throw new Error('Change Set dependency-chain UI is missing')
  }
  if (!mediaMain.includes('flowMergeConflictPanelHtml') || !mediaMain.includes('resolve-flow-merge') || !mediaMain.includes('resolveFlowMerge')) {
    throw new Error('Parallel Flow merge-conflict UI is missing')
  }
  if (!mediaCss.includes('.flow-merge-conflict-card') || !mediaCss.includes('.flow-node.merge-conflict')) {
    throw new Error('Parallel Flow merge-conflict styles are missing')
  }
  if (!mediaCss.includes('companion-activity-chip') || !mediaCss.includes('body[data-layout="companion-peek"]')) {
    throw new Error('Companion peek/activity styles are missing')
  }
  // Ключ вторичной панели — "secondarySidebar". Оболочка принимает ровно три
  // ключа viewsContainers и объявляет additionalProperties: false, поэтому
  // "auxiliaryBar" молча отбрасывался: провайдер был зарегистрирован, а панели
  // справа не существовало. Эта проверка требовала сломанный ключ и тем самым
  // гарантировала, что дефект не починят.
  if (!packageJson.includes('"secondarySidebar"') || !packageJson.includes('localAgent.companionChat')) {
    throw new Error('package.json is missing the secondary-sidebar companion chat registration')
  }
  if (!packageJson.includes('"id": "pointCompanion"') || packageJson.includes('"id": "point.companion"')) {
    throw new Error('Secondary-sidebar container id must be manifest-safe (letters, digits, _ or - only)')
  }
  if (!packageJson.includes('"onView:localAgent.companionChat"')) {
    throw new Error('Directly opening the right assistant does not activate the extension')
  }
  if (!packageJson.includes('"title": "Помощник Point"') || !packageJson.includes('"name": "Диалог"')) {
    throw new Error('package.json does not expose the single Point assistant clearly')
  }
  if (packageJson.includes('localAgent.chatView') || packageJson.includes('"id": "localAgent"')) {
    throw new Error('The duplicate Activity Bar assistant is still contributed')
  }
  for (const view of ['localAgent.terminalTools', 'localAgent.databaseTools', 'localAgent.sshTools', 'localAgent.gitTools', 'localAgent.logTools']) {
    if (!packageJson.includes(view)) throw new Error(`Point right tool window is missing: ${view}`)
  }
  if (!packageJson.includes('localAgent.openLogChat') || !mediaMain.includes('Отдельный чат по логам')) {
    throw new Error('Structured logs do not expose their isolated assistant chat')
  }
  if (!mediaMain.includes('function projectRequired()') || !mediaMain.includes("data-action=\"choose-project\"") || !mediaMain.includes("type:'chooseProject'")) {
    throw new Error('Empty Hub does not expose the live project picker')
  }
  if (!mediaMain.includes('function replaceCompanionThreadHtml()') || !mediaMain.includes('const follow = companionAutoFollow || threadNearBottom(thread)') || !mediaMain.includes('replaceCompanionThreadHtml()')) {
    throw new Error('Companion DOM patch does not preserve bottom-follow scroll state')
  }
  if (!overlaySource.includes("args['agents'] && this.productService.applicationName !== 'point'") || !overlaySource.includes("executeCommand('localAgent.switchProject')")) {
    throw new Error('Point startup or titlebar still routes through the upstream Agents/recent-project path')
  }
  if (!overlaySource.includes("point.workbench.inventory.initialized") || !overlaySource.includes("pointNeedsInitialInventory") || !overlaySource.includes("? 'workbench.view.explorer'")) {
    throw new Error('A fresh Point workspace can restore SCM instead of Inventory')
  }
  if (!overlaySource.includes("hideIfEmpty: product.applicationName !== 'point'")) {
    throw new Error('Point Inventory can disappear before its asynchronous file views register')
  }
  if (!overlaySource.includes("product.applicationName !== 'point' &&\\n\\t\\t\\t\\tviewContainerToRestore")) {
    throw new Error('The upstream default-container correction can overwrite Point Inventory on a fresh profile')
  }
  if (!activityCss.includes('content: none') || !workbenchCss.includes('statusbar-item[id*="copilot" i]') || !workbenchCss.includes('statusbar-item[id*="chat" i]')) {
    throw new Error('Duplicate Point logo or upstream Chat/Copilot status chrome is still visible')
  }
  if (!workbenchCss.includes('.part.auxiliarybar') || !workbenchCss.includes('height: 100% !important')) {
    throw new Error('Point right tool rail can collapse its native view content to zero height')
  }
  if (!workbenchCss.includes('-webkit-app-region: no-drag') || /\n\s*app-region:\s*no-drag/.test(workbenchCss)) {
    throw new Error('Point titlebar buttons can be swallowed by the native window drag region')
  }
  if (!overlaySource.includes("setPartHidden(false, Parts.AUXILIARYBAR_PART)") || !overlaySource.includes("executeCommand('workbench.view.extension.pointCompanion')")) {
    throw new Error('Point right tool rail is not restored permanently at startup')
  }
  if (!packageJson.includes('editorHasSelection')) {
    throw new Error('Selection-gated companion menu when-clause is missing')
  }
  if (!mediaMain.includes("read_skill:'Чтение навыка'") && !mediaMain.includes('read_skill:\'Чтение навыка\'')) {
    throw new Error('read_skill tool label is missing')
  }

  const extensionSource = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'extension.js'), 'utf8')
  if (!extensionSource.includes('✦ Спросить компаньона') || !extensionSource.includes('createCompanionCodeLensProvider')) {
    throw new Error('Selection CodeLens ask companion affordance is missing')
  }
  if (!extensionSource.includes("case 'copyCompanionText'") || !extensionSource.includes('env.clipboard.writeText')) {
    throw new Error('Companion copy action is not handled by the IDE host')
  }
  for (const handler of ['openCompanionMessageDetails', 'showCompanionArchives', 'companionFeedback', 'newCompanionThread', 'showLogChat']) {
    if (!extensionSource.includes(handler)) throw new Error(`Companion host action is missing: ${handler}`)
  }
  if (!extensionSource.includes("registerCommand('localAgent.askCompanion', () => openCompanionChat(provider, revealInfra, { surface: 'sidebar', forceSurface: true }))")) {
    throw new Error('Primary assistant command does not open the Cursor-like right sidebar')
  }
  if (!extensionSource.includes('provider.companionFocus = () => liveCompanionFocus(() => ideObserver?.lastFailure(), () => runController?.lastStarted())')
      || extensionSource.includes('provider.companionFocus = () => liveCompanionFocus(() => ideObserver?.lastFailure(), () => runController?.current())')) {
    throw new Error('A merely selected launch target still leaks into every Companion turn')
  }
  if (!extensionSource.includes("const api = gitExt?.isActive ? gitExt.exports?.getAPI?.(1) : undefined")) {
    throw new Error('Passive Companion observations still activate Git and steal the Inventory sidebar')
  }

  const overlay = fs.readFileSync(path.resolve(__dirname, '..', 'distribution', 'apply-overlay.mjs'), 'utf8')
  if (!overlay.includes('Point hide upstream Chat view') || !overlay.includes('ContextKeyExpr.false()')) {
    throw new Error('Overlay does not hide upstream Chat view for Point')
  }
  if (!overlay.includes('Point quick chat redirect') || !overlay.includes('Point openQuickChat redirect')) {
    throw new Error('Overlay does not redirect Quick Chat to Agent Hub')
  }
  if (!overlay.includes("id: 'local-agent.local-agent-workbench'") || !overlay.includes('local-agent.local-agent-workbench.i18n.json')) {
    throw new Error('Overlay does not register the built-in Point extension in the Russian language pack')
  }
  if (!overlay.includes("executeCommand('localAgent.quickChat'")) {
    throw new Error('Overlay missing Companion quick-chat redirect')
  }
  if (!overlay.includes("product.applicationName === 'point' ? ActionsOrientation.VERTICAL : ActionsOrientation.HORIZONTAL")) {
    throw new Error('Point auxiliary bar does not use the native vertical action layout')
  }

  process.stdout.write('ok\n')
}

main().catch(error => {
  console.error(error)
  process.exit(1)
})
