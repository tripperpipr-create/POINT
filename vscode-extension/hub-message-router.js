// Внешний разбор сообщений вебвью Хаба: одна ветка на тип сообщения.
//
// handleMessage в extension.js проверяет доверие к папке и пишет журнал, а сам
// разбор живёт здесь. Он зовётся через .call(provider, message), поэтому this
// в ветках — провайдер Хаба, ровно как в те времена, когда switch стоял внутри
// метода. Новое сообщение вебвью требует ветки здесь и в своём контроллере:
// затвор scripts/check-webview-message-routes.mjs читает этот файл.

const vscode = require('vscode')
const { handleMasterMessage } = require('./master-chat-controller')
const { handleCompanionChatMessage } = require('./companion-chat-controller')
const { handleHubRuntimeMessage } = require('./hub-runtime-controller')
const { handleInfraMessage } = require('./infra-controller')
const { handleRosterMessage } = require('./roster-controller')
const { handleLearningMessage } = require('./learning-controller')
const { handleToolingMessage } = require('./tooling-controller')
const { handleCursorMessage } = require('./cursor-controller')
const { TOOL_WINDOW_COMMANDS } = require('./extension-utils')

function orchestratorConfigPayload(config = {}) {
  const payload = {
    id: String(config.id || ''),
    workspaceId: String(config.workspaceId || ''),
    preset: String(config.preset || ''),
    connectionId: String(config.connectionId || ''),
    provider: config.provider || '',
    providerPreset: String(config.providerPreset || ''),
    baseUrl: String(config.baseUrl || ''),
    model: String(config.model || ''),
    projectModelOverride: Boolean(config.projectModelOverride),
    temperature: Number(config.temperature ?? 0),
    maxOutputTokens: Number(config.maxOutputTokens || 0),
    planningDepth: Number(config.planningDepth ?? 50),
    parallelism: Number(config.parallelism ?? 50),
    approvalStrictness: Number(config.approvalStrictness ?? 50),
    teamPreference: Number(config.teamPreference ?? 50),
  }
  if (config.createdAt) payload.createdAt = config.createdAt
  return payload
}

// openWorkspaceFile приходит из фабрики расширения: он знает, в каком окне
// открывать файл — в Чертоге или в IDE.
function createHubMessageRouter({ openWorkspaceFile }) {
  return async function routeHubMessage(message) {
    switch (message.type) {
      case 'ready':
        if (message.surface === 'wide' && this.panel) this.hubPanelReady = true
        if (message.surface === 'wide') void this.postProjects?.()
        if (message.surface === 'companion' && this.view) this.companionDockReady = true
        if ((message.surface === 'companion-popup' || message.surface === 'companion-peek') && this.companionPopup) this.companionPopupReady = true
        if (message.surface === 'companion-sidebar' && this.companionSidebar) this.companionSidebarReady = true
        if (!vscode.workspace.isTrusted) {
          this.postState(true)
        } else {
          // Paint the Guild immediately. Starting the local core and loading
          // its bootstrap data happen after the webview gets its first frame.
          this.postState(true)
          void this.refreshCursorRuntime()
          // A contributed webview can finish loading while its sidebar is
          // hidden. Editor/navigation commands must not start point-core as
          // a side effect of merely activating this extension.
          if (this.hubVisible() && this.workspaceFolder()) {
            if (vscode.workspace.getConfiguration('localAgent').get('autoStart', true)) this.scheduleAutoStart()
            else await this.refresh()
          }
        }
        this.flushCompanionFocus()
        break
      case 'openHub':
        this.showWide(this.onboardingComplete ? 'master' : 'onboarding'); break
      case 'chooseProject':
        await vscode.commands.executeCommand('localAgent.switchProject'); break
      // Путь из вебвью — не путь, а ключ поиска по реестру. Открываем только
      // то, что реестр уже знает; всё остальное идёт через диалог оболочки.
      case 'openProject': {
        const known = this.knownProject(message.path)
        if (known) await this.switchToProject?.(known)
        break
      }
      case 'openProjectInIde':
        await vscode.commands.executeCommand('localAgent.openProjectInIde', this.knownProject(message.path))
        break
      case 'chooseProjectFolder': {
        const picked = await vscode.window.showOpenDialog({
          canSelectFiles: false,
          canSelectFolders: true,
          canSelectMany: false,
          openLabel: this.agentsWindowMode ? 'Открыть в Чертоге' : 'Открыть в Point',
          title: 'Point — выбрать проект',
        })
        const uri = picked?.[0]
        if (!uri) break
        if (this.agentsWindowMode) await this.switchToProject?.(uri.fsPath)
        else await vscode.commands.executeCommand('vscode.openFolder', uri, false)
        break
      }
      case 'pinProject': {
        const known = this.knownProject(message.path)
        if (known) await this.projects?.setPinned?.(known, message.pinned === true)
        void this.postProjects?.()
        break
      }
      case 'forgetProject': {
        const known = this.knownProject(message.path)
        if (known) await this.projects?.forget?.(known)
        void this.postProjects?.()
        break
      }
      case 'refreshProjects':
        void this.postProjects?.()
        break
      case 'cloneProject':
        await vscode.commands.executeCommand('localAgent.gitClone'); break
      case 'openConnections':
        this.showConnections(); break
      case 'openStatistics':
        this.showStatistics(); break
      case 'openDocker':
        this.showDocker(); break
      case 'toolCommand': {
        const command = String(message.command || '')
        if (!TOOL_WINDOW_COMMANDS.has(command)) throw new Error('Недоступное действие окна инструментов')
        await vscode.commands.executeCommand(command)
        break
      }
      case 'mcpAction': case 'gitlabAction':
        await this.integrations().handle(message); break
      case 'gitAction':
      case 'loadToolWindowState':
      case 'loadDocker':
      case 'dockerContainerAction':
      case 'dockerLogs':
      case 'dockerOpenTerminal':
      case 'saveServerProfile':
      case 'probeServerProfile':
      case 'listServerRemote':
      case 'openServerTerminal':
      case 'deleteServerProfile':
      case 'saveDBConnection':
      case 'deleteDBConnection':
      case 'testDBConnection':
      case 'schemaDBConnection':
      case 'queryDBConnection':
        await handleInfraMessage.call(this, message)
        break
      case 'openCompanionSidebar':
        await this.showCompanionSidebar()
        this.pushCompanionThreadSync('sidebar')
        break
      case 'openCompanionPopup':
        this.showCompanionPeek()
        this.pushCompanionThreadSync('peek')
        if (typeof message.message === 'string' && message.message.trim()) {
          this.queueCompanionFocus({
            message: message.message,
            send: Boolean(message.send),
            surface: 'peek',
          })
        }
        break
      case 'companionThreadUpdate':
      case 'copyCompanionText':
      case 'openCompanionMessageDetails':
      case 'companionFeedback':
      case 'newCompanionThread':
      case 'showCompanionArchives':
      case 'stopCompanionChat':
      case 'companionChat':
      case 'clearCompanionHistory':
      case 'dismissCompanionIntervention':
      case 'restoreCompanionInterventions':
      case 'probeCompanionConnection':
      case 'saveCompanionConfig':
      case 'saveCompanionConfigAndChat':
        await handleCompanionChatMessage.call(this, message)
        break
      case 'closeCompanionPopup':
        this.closeCompanionPopup(); break
      case 'focusHub':
        if (message.agentId) this.focusAgentImprovement(message.agentId, message.constructorStep)
        else this.showWide(message.tab || 'overview')
        break
      case 'agentImprovementFocused':
        if (this.agentImprovementFocus?.requestId === message.requestId) {
          this.agentImprovementFocus = undefined
          this.postState()
        }
        break
      case 'manageTrust':
        await vscode.commands.executeCommand('workbench.trust.manage'); break
      case 'startServer':
        if (!this.workspaceFolder()) {
          await vscode.commands.executeCommand('localAgent.switchProject')
          break
        }
        await this.service.start(); await this.refresh(); break
      case 'restartServer':
        await this.service.stop(); await this.service.start(); await this.refresh(); break
      case 'startRun':
      case 'startFastAgent':
      case 'undoRunPatches':
      case 'previewRun':
      case 'cancelRun':
      case 'pauseRun':
      case 'resumeRun':
      case 'extendActiveTime':
      case 'messageRun':
      case 'forbidFile':
      case 'loadContextInspector':
      case 'contextAmend':
      case 'addRunContextFiles':
      case 'addRunContextSelection':
      case 'saveFlow':
      case 'startFlowRun':
      case 'loadQuestOutcome':
      case 'loadQuestReplans':
      case 'replanQuest':
      case 'reviseQuestBrief':
      case 'loadHandoffs':
      case 'loadDecisions':
      case 'resolveDecision':
      case 'resolveChangeSet':
      case 'resolveApproval':
      case 'loadRun':
      case 'decideQuestProposal':
      case 'decideCompanionAction':
      case 'previewEquipSkill':
      case 'equipSkill':
      case 'revertExecution':
      case 'revertQuest':
      case 'revertFlowNode':
      case 'launchExecution':
      case 'resolveFlowNode':
      case 'resolveFlowMerge':
      case 'applyChangeSetChain':
      case 'applyChangeSet':
      case 'rejectChangeSet':
      case 'revertChangeSet':
      // То же самое у приёмки задачи по ссылке: обработчики в
      // hub-runtime-controller.js полны, а сообщения до них не доходили.
      case 'createIntake':
      case 'approveIntake':
      case 'expandIntake':
      case 'selectIntake':
        await handleHubRuntimeMessage.call(this, message)
        break
      case 'saveMemory': {
        const saved = await this.service.request('/api/memories', { method: 'POST', body: JSON.stringify(message.memory) })
        this.upsertBootItem('memories', saved)
        this.post({ type: 'memorySaved', memoryId: saved?.id || '' })
        this.postState()
        break
      }
      case 'deleteMemory': {
        const answer = await vscode.window.showWarningMessage(
          'Удалить эту запись памяти текущего проекта? Действие нельзя отменить.',
          { modal: true },
          'Удалить',
        )
        if (answer !== 'Удалить') break
        await this.service.request(`/api/memories/${encodeURIComponent(message.memoryId)}`, { method: 'DELETE' })
        this.removeBootItem('memories', message.memoryId)
        this.post({ type: 'memoryDeleted', memoryId: message.memoryId })
        this.postState()
        break
      }
      case 'loadFileHistory': {
        const path = String(message.path || '').trim()
        if (!path) break
        // Путь не чистим здесь: границу рабочей папки проверяет ядро, и
        // вторая, отличающаяся проверка в расширении только создала бы
        // расхождение между тем, что разрешено, и тем, что показано.
        const history = await this.service.request(`/api/files/history?path=${encodeURIComponent(path)}`)
        this.post({ type: 'fileHistory', history })
        break
      }
      case 'capabilityDelta': {
        // Что изменится от навыка или умения — считает ядро тем же кодом,
        // что и саму годность.
        const delta = await this.service.request('/api/project-agents/capability-delta', {
          method: 'POST', body: JSON.stringify({
            profile: message.profile || {},
            addTools: message.addTools || [],
            removeTools: message.removeTools || [],
          }),
        })
        this.post({ type: 'capabilityDelta', delta, key: message.key || '' })
        break
      }
      case 'agentCapability': {
        // Что агент сможет — считает ядро тем же кодом, который это разрешает.
        const capability = await this.service.request('/api/project-agents/capability', {
          method: 'POST', body: JSON.stringify(message.profile || {}),
        })
        // Ключ возвращается вместе с ответом: у веб-вью несколько агентов на
        // экране, и без ключа ответ невозможно отнести к нужному.
        this.post({ type: 'agentCapability', capability, key: String(message.key || '') })
        break
      }
      case 'orchestratorPolicy': {
        // Политику считает ядро — тем же кодом, который её исполняет.
        const policy = await this.service.request('/api/orchestrator/policy', {
          method: 'POST', body: JSON.stringify(message.draft || {}),
        })
        // Ключ возвращается с ответом: на экране может быть два разных
        // черновика (форма настройки и карточка обзора), и без ключа ответ
        // невозможно отнести к нужному.
        this.post({ type: 'orchestratorPolicy', policy, key: String(message.key || '') })
        break
      }
      case 'forkMasterConversation':
      case 'deleteMasterConversation':
      case 'exportMasterConversation':
      case 'generateReport':
      case 'pickMasterModel':
      case 'pickMasterContext':
      case 'previewMasterContext':
      case 'masterPage':
      case 'attachMasterContext':
      case 'masterSession':
      case 'loadMaster':
		case 'loadMasterDevelopment':
		case 'setMasterLearning':
		case 'rollbackMasterSkill':
      case 'masterChat':
      case 'offerMasterChatBranch':
      case 'copyMasterText':
      case 'openMasterMessageDetails':
      case 'masterFeedback':
      case 'stopMasterChat':
      case 'approveMasterWorkOrderV2':
		case 'reviseMasterWorkOrderV2':
      case 'hireMasterWorkOrderAgentV2':
      case 'controlMasterWorkOrderQuestV2':
      case 'reviewMasterManualCriterionV2':
      case 'controlMasterApplicationV2':
      // Четыре ветки ниже написаны в master-chat-controller.js давно, но во
      // внешнем разборе их не было: поиск по контексту, вложение по пути,
      // список чатов и чат чужого мира нажимались вхолостую. Нашёл затвор
      // scripts/check-webview-message-routes.mjs — тем же способом, каким
      // нашёл мёртвый `purgeQuest`.
      case 'searchMasterContext':
      case 'attachMasterContextPath':
      case 'loadChatDirectory':
      case 'openProjectChat':
        await handleMasterMessage.call(this, message)
        break
      case 'loadStatistics':
      case 'createSystemBackup':
      case 'restoreSystemBackup':
      case 'searchExperience':
      case 'previewManualLearning':
      case 'applyManualLearning':
      case 'rollbackAgentImprovement':
      case 'promoteAgentImprovement':
      case 'rejectAgentImprovement':
      case 'loadQuestRetrospective':
      case 'saveBudget':
      case 'saveGlobalBudget':
        await handleLearningMessage.call(this, message)
        break
      case 'saveProfile':
      case 'deleteProfile':
      case 'deleteProjectAgent':
      case 'activateProjectAgentDraft':
      case 'rejectProjectAgentDraft':
      case 'deleteQuest':
      case 'purgeQuest':
      case 'deleteTeam':
      case 'deleteFlow':
      case 'deleteWorkOrderV2':
      case 'deleteBlueprint':
      case 'exportProfile':
      case 'importProfile':
        await handleRosterMessage.call(this, message)
        break
      case 'attachFiles':
        await this.attachFiles(); break
      case 'attachSelection':
        await this.attachSelection(); break
      case 'previewContext': {
        const contextItems = Array.isArray(message.contextItems) ? message.contextItems : []
        try {
          const preview = await this.service.request('/api/context/preview', { method: 'POST', body: JSON.stringify({ contextItems }) })
          this.post({ type: 'contextPreview', preview })
        } catch (error) {
          this.post({ type: 'contextPreviewError', message: error instanceof Error ? error.message : String(error) })
        }
        break
      }
      case 'saveCustomTool':
      case 'previewCustomTool':
      case 'previewTool':
      case 'requestToolExecutionApproval':
      case 'resolveToolExecutionApproval':
      case 'executeTool':
      case 'claimWorkflowStep':
      case 'heartbeatWorkflowStep':
      case 'completeWorkflowStep':
      case 'deleteCustomTool':
      case 'exportCustomTool':
      case 'importCustomTool':
      case 'probeProvider':
      case 'probeModelCapability':
        await handleToolingMessage.call(this, message)
        break
      case 'rebuildIndex': {
        this.service.hostLog('info', '[index] rebuild requested')
        if (this.indexController) {
          await this.indexController.rebuildNow({ notify: false })
        } else {
          const indexStatus = await this.service.request('/api/index/rebuild', { method: 'POST', body: '{}', timeoutMs: 120_000 })
          this.patchBoot({ indexStatus })
        }
        this.post({ type: 'indexRebuilt' })
        this.postState()
        break
      }
      case 'saveQuickChatSettings': {
        const cfg = vscode.workspace.getConfiguration('localAgent')
        if (typeof message.defaultProfileId === 'string') {
          await cfg.update('quickChatDefaultProfileId', message.defaultProfileId, vscode.ConfigurationTarget.Global)
        }
        this.postState(true)
        break
      }
      case 'openQuickChat':
        await vscode.commands.executeCommand('localAgent.openAgentComposer')
        break
      case 'enableDockerSandbox': {
        // Ядро читает бэкенд песочницы один раз — при запуске, из окружения.
        // Поэтому настройки мало: без перезапуска ядро осталось бы в
        // filtered-copy, а карточка показывала бы ту же блокировку, хотя
        // переключатель уже стоит в docker.
        const cfg = vscode.workspace.getConfiguration('localAgent')
        await cfg.update('sandboxBackend', 'docker', vscode.ConfigurationTarget.Global)
        await vscode.commands.executeCommand('localAgent.restartServer')
        this.postState(true)
        break
      }
      case 'revertPatch':
        await this.revertPatch(message.id); break
      case 'launchCursorAgent':
      case 'cursorRefresh':
      case 'cursorLogin':
      case 'cursorLogout':
      case 'startCursorRun':
      case 'cancelCursorRun':
      case 'cancelCursorExecution':
        await handleCursorMessage.call(this, message)
        break
      case 'completeOnboarding':
        this.onboardingComplete = true
        await this.context.globalState?.update?.('point.agentHubV2.onboardingComplete', true)
        this.selectedTab = 'master'
        this.postState(true)
        break
      case 'restartOnboarding':
        this.onboardingComplete = false
        await this.context.globalState?.update?.('point.agentHubV2.onboardingComplete', false)
        this.selectedTab = 'onboarding'
        this.postState(true)
        break
      case 'saveWorkflow':
      case 'deleteWorkflow':
      case 'startWorkflow':
      case 'cancelWorkflow':
      case 'loadWorkflowRun':
        await handleToolingMessage.call(this, message)
        break
      case 'openFile':
        await openWorkspaceFile(message.path, message.line, this.auxiliaryHubMode ? 'main' : this.agentsWindowMode); break
      case 'showOutput':
        this.output.show(true); break
      case 'saveTeam':
      case 'saveSkill':
        await handleRosterMessage.call(this, message)
        break
      case 'saveConnection':
      case 'probeConnection':
      case 'defaultConnection':
      case 'deleteConnection': {
        await this.connections().handle(message)
        if (message.type === 'saveConnection') this.post({ type: 'connectionSaved' })
        this.postState()
        break
      }
      case 'saveModelRouting': {
        const saved = await this.service.request('/api/workspace/model-routing', {
          method: 'PUT',
          body: JSON.stringify(message.routing || {}),
        })
        this.patchBoot({ modelRouting: saved })
        this.postState()
        break
      }
      case 'saveOrchestratorConfig': {
        const config = orchestratorConfigPayload(message.config)
        const saved = await this.service.request('/api/orchestrator/config', { method: 'POST', body: JSON.stringify(config) })
        if (!config.projectModelOverride) {
          const defaults = await this.service.request('/api/global-models')
          defaults.master = { connectionId: config.connectionId, model: config.model }
          await this.service.request('/api/global-models', { method: 'POST', body: JSON.stringify(defaults) })
        }
        this.patchBoot({ orchestrator: saved })
        if (!config.projectModelOverride) await this.refresh()
        this.post({ type: 'orchestratorConfigSaved', configId: saved.id })
        this.postState()
        break
      }
      case 'loadGlobalModels': {
        const defaults = await this.service.request('/api/global-models')
        this.post({ type: 'globalModelsLoaded', defaults })
        break
      }
      case 'saveGlobalModels': {
        const defaults = await this.service.request('/api/global-models', { method: 'POST', body: JSON.stringify(message.defaults || {}) })
        this.post({ type: 'globalModelsLoaded', defaults, saved: true })
        await this.refresh()
        break
      }
      case 'saveBlueprint':
      case 'previewCompiledPrompt':
      case 'saveProjectAgent':
      case 'applyBlueprintToAgent':
      case 'previewBlueprintSync':
      case 'updateBlueprintFromAgent':
        await handleRosterMessage.call(this, message)
        break
      case 'selectTab':
        if (message.tab === 'statistics') {
          this.showStatistics()
          break
        }
        if (message.tab === 'docker') {
          this.showDocker()
          break
        }
        if (!this.onboardingComplete && message.tab && message.tab !== 'onboarding') {
          this.selectedTab = 'onboarding'
          this.postState()
          break
        }
        this.selectedTab = message.tab
        // Вкладка запоминается на мир: возврат в проект должен возвращать и
        // место, на котором его оставили.
        void this.projects?.rememberTab?.(this.workspaceFolder()?.uri?.fsPath || '', message.tab)
        this.postState()
        break
      case 'openRoster':
        this.showWide('settings'); break
      default:
        // Сообщение без маршрута молча исчезало, и кнопка, которая его
        // шлёт, выглядела нажатой-без-последствий: ни ошибки, ни записи.
        // Так «СНЕСТИ КВЕСТ» доехал до выложенного приложения мёртвым —
        // вебвью слал `purgeQuest`, а разбор о таком типе не знал.
        // Затвор scripts/check-webview-message-routes.mjs ловит это до
        // сборки; строка ниже — на случай, когда затвор обошли.
        console.warn(`[point] сообщение вебвью без маршрута: ${String(message?.type || '(без типа)')}`)
        break
    }
  }
}

module.exports = { createHubMessageRouter }
