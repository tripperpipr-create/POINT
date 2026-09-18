// Hub runtime: runs, quests, flows, change sets, approvals, and proposal decisions.
const vscode = require('vscode')
const { decisionResolvePath } = require('./extension-utils')

const QUEST_DECISION_TIMEOUT_MS = 90_000

async function handleHubRuntimeMessage(message) {
  switch (message.type) {
    case 'startRun': {
      this.service.hostLog('info', `[agent] start profile=${message.profileId || '-'} task_bytes=${String(message.task || '').length} context=${Array.isArray(message.contextItems) ? message.contextItems.length : 0}`)
      const run = await this.service.request('/api/runs', { method: 'POST', body: JSON.stringify({
        profileId: message.profileId,
        task: message.task,
        goal: message.goal || '',
        acceptanceCriteria: Array.isArray(message.acceptanceCriteria) ? message.acceptanceCriteria : [],
        constraints: Array.isArray(message.constraints) ? message.constraints : [],
        apiKey: message.apiKey || '',
        contextItems: Array.isArray(message.contextItems) ? message.contextItems : [],
        preflightFingerprint: message.preflightFingerprint || '',
      }) })
      this.activeRunId = run.id
      this.service.hostLog('info', `[agent] started run_id=${run.id} status=${run.status || '-'}`)
      this.details = { run, events: [], approvals: [], patches: [] }
      this.updateAgentBusy()
      this.post({ type: 'runStarted' })
      this.postState()
      this.startPolling()
      break
    }
    case 'startFastAgent': {
      this.service.hostLog('info', `[agent] fast-agent profile=${message.profileId || '-'} task_bytes=${String(message.task || '').length}`)
      const agent = (this.boot?.projectAgents || []).find(item => item.id === message.profileId)
        || (this.boot?.profiles || []).find(item => item.id === message.profileId)
      const apiKey = agent
        ? await this.credentialFor(agent, `агента «${agent.name || agent.id}»`, message.apiKey || '')
        : (message.apiKey || '')
      const run = await this.service.request('/api/runs/fast-agent', { method: 'POST', body: JSON.stringify({
        profileId: message.profileId,
        task: message.task,
        apiKey,
        contextItems: Array.isArray(message.contextItems) ? message.contextItems : [],
        preflightFingerprint: message.preflightFingerprint || '',
      }) })
      this.activeRunId = run.id
      this.service.hostLog('info', `[agent] fast-agent run_id=${run.id} status=${run.status || '-'}`)
      this.details = { run, events: [], approvals: [], patches: [] }
      this.updateAgentBusy()
      this.post({ type: 'runStarted', fastAgent: true })
      this.postState()
      this.startPolling()
      break
    }
    case 'undoRunPatches': {
      const result = await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/undo`, {
        method: 'POST',
        body: JSON.stringify({ patchIds: Array.isArray(message.patchIds) ? message.patchIds : [] }),
      })
      this.post({ type: 'runUndoResult', result })
      if (this.activeRunId === message.runId) await this.loadRun(message.runId, false)
      this.postState()
      break
    }
    case 'previewRun': {
      try {
        const preview = await this.service.request('/api/runs/preview', { method: 'POST', body: JSON.stringify({
          profileId: message.profileId,
          task: message.task,
          goal: message.goal || '',
          acceptanceCriteria: Array.isArray(message.acceptanceCriteria) ? message.acceptanceCriteria : [],
          constraints: Array.isArray(message.constraints) ? message.constraints : [],
          contextItems: Array.isArray(message.contextItems) ? message.contextItems : [],
        }) })
        this.post({ type: 'agentRunPreview', preview })
      } catch (error) {
        this.post({ type: 'agentRunPreviewError', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'cancelRun':
      await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/cancel`, { method: 'POST', body: '{}' })
      await this.loadRun(message.runId)
      break
    case 'pauseRun':
      this.upsertBootItem('runs', await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/pause`, { method: 'POST', body: '{}' }))
      if (this.activeRunId === message.runId) await this.loadRun(message.runId, false)
      this.postState()
      break
    case 'resumeRun':
      this.upsertBootItem('runs', await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/resume`, {
        method: 'POST',
        body: JSON.stringify({ apiKey: message.apiKey || '' }),
      }))
      if (this.activeRunId === message.runId) await this.loadRun(message.runId, false)
      this.postState()
      break
    case 'extendActiveTime':
      this.upsertBootItem('runs', await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/extend-active-time`, {
        method: 'POST',
        body: JSON.stringify({ apiKey: message.apiKey || '' }),
      }))
      if (this.activeRunId === message.runId) await this.loadRun(message.runId, false)
      this.postState()
      break
    case 'messageRun':
      await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/message`, {
        method: 'POST',
        body: JSON.stringify({ message: String(message.message || ''), learningIntent: String(message.learningIntent || '') }),
      })
      if (this.activeRunId === message.runId) await this.loadRun(message.runId, false)
      this.postState()
      break
    case 'forbidFile':
      await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/forbid-file`, {
        method: 'POST',
        body: JSON.stringify({ path: String(message.path || '') }),
      })
      if (this.activeRunId === message.runId) await this.loadRun(message.runId, false)
      this.postState()
      break
    case 'loadContextInspector': {
      const inspector = await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/context-inspector`)
      this.post({ type: 'contextInspector', runId: message.runId, inspector })
      break
    }
    case 'contextAmend': {
      await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/context-amend`, {
        method: 'POST',
        body: JSON.stringify({ action: message.action, itemId: message.itemId }),
      })
      const inspector = await this.service.request(`/api/runs/${encodeURIComponent(message.runId)}/context-inspector`)
      this.post({ type: 'contextInspector', runId: message.runId, inspector })
      break
    }
    case 'addRunContextFiles':
      await this.addRunContextFiles(message.runId); break
    case 'addRunContextSelection':
      await this.addRunContextSelection(message.runId); break
    case 'saveFlow': {
      const saved = await this.service.request('/api/flows', { method: 'POST', body: JSON.stringify(message.flow) })
      this.upsertBootItem('flows', saved)
      this.focusTab('flows')
      this.post({ type: 'flowSaved', flowId: saved.id })
      this.postState()
      break
    }
    case 'startFlowRun': {
      try {
        const flowRun = await this.service.request('/api/flow-runs', {
          method: 'POST',
          body: JSON.stringify({ flowId: String(message.flowId || ''), input: message.input || {} }),
        })
        await this.refreshRuntimeState()
        await this.launchPendingHubExecutions('', flowRun?.id || '')
        const execution = (this.boot?.executions || []).find(item =>
          item.flowRunId === flowRun?.id && item.runId && item.status === 'running'
        )
        if (execution?.runId) await this.loadRun(execution.runId, false)
        this.focusTab('overview')
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'loadQuestOutcome': {
      const id = String(message.questId || '').trim()
      if (!id) break
      const outcome = await this.service.request('/api/quests/' + encodeURIComponent(id) + '/outcome')
      this.post({ type: 'questOutcome', outcome })
      break
    }
    case 'createIntake': {
      try {
        this.post({ type: 'intakeBusy', busy: true })
        const session = await this.service.request('/api/intakes', {
          method: 'POST',
          body: JSON.stringify({ url: String(message.url || '').trim() }),
        })
        this.post({ type: 'intakeUpdated', session })
        await this.refreshRuntimeState({ companion: true })
        this.postState()
      } catch (error) {
        this.post({ type: 'intakeError', message: error instanceof Error ? error.message : String(error) })
      } finally {
        this.post({ type: 'intakeBusy', busy: false })
      }
      break
    }
    case 'approveIntake': {
      try {
        this.post({ type: 'intakeBusy', busy: true })
        const session = await this.service.request('/api/intakes/' + encodeURIComponent(message.id) + '/approve', {
          method: 'POST',
          body: JSON.stringify({ expectedVersion: Number(message.expectedVersion || 0) }),
        })
        this.post({ type: 'intakeUpdated', session })
        await this.refreshRuntimeState({ companion: true })
        this.postState()
      } catch (error) {
        this.post({ type: 'intakeError', message: error instanceof Error ? error.message : String(error) })
      } finally {
        this.post({ type: 'intakeBusy', busy: false })
      }
      break
    }
    case 'expandIntake': {
      try {
        this.post({ type: 'intakeBusy', busy: true })
        const session = await this.service.request('/api/intakes/' + encodeURIComponent(message.id) + '/expand', {
          method: 'POST',
          body: JSON.stringify({
            expectedVersion: Number(message.expectedVersion || 0),
            networkHosts: Array.isArray(message.networkHosts) ? message.networkHosts : [],
          }),
        })
        this.post({ type: 'intakeUpdated', session })
        await this.refreshRuntimeState({ companion: true })
        this.postState()
      } catch (error) {
        this.post({ type: 'intakeError', message: error instanceof Error ? error.message : String(error) })
      } finally {
        this.post({ type: 'intakeBusy', busy: false })
      }
      break
    }
    case 'selectIntake': {
      this.post({ type: 'intakeSelected', id: String(message.id || '') })
      break
    }
    case 'loadQuestReplans': {
      const id = String(message.questId || '').trim()
      if (!id) break
      const replans = await this.service.request('/api/quests/' + encodeURIComponent(id) + '/replans')
      this.post({ type: 'questReplans', replans: Array.isArray(replans) ? replans : [] })
      break
    }
    case 'replanQuest': {
      const id = String(message.questId || '').trim()
      if (!id) break
      try {
        const result = await this.service.request('/api/quests/' + encodeURIComponent(id) + '/replan', {
          method: 'POST',
          body: JSON.stringify({
            reason: message.reason || '',
            criterionIds: message.criterionIds || [],
            stages: message.stages || [],
          }),
        })
        this.post({ type: 'questReplanDone', result })
        await this.refreshRuntimeState({ companion: true })
        this.postState()
      } catch (error) {
        this.post({ type: 'questReplanDone', error: String(error?.message || error) })
      }
      break
    }
    case 'reviseQuestBrief': {
      const id = String(message.questId || '').trim()
      if (!id) break
      try {
        const quest = await this.service.request('/api/quests/' + encodeURIComponent(id) + '/revise-brief', {
          method: 'POST',
          body: JSON.stringify({
            brief: message.brief || {},
            expectedVersion: message.expectedVersion,
            approveVersion: message.approveVersion,
          }),
        })
        this.post({ type: 'questReviseDone', quest })
        await this.refreshRuntimeState({ companion: true })
        this.postState()
      } catch (error) {
        this.post({ type: 'questReviseDone', error: String(error?.message || error) })
      }
      break
    }
    case 'loadHandoffs': {
      const id = String(message.flowRunId || '').trim()
      if (!id) break
      const handoffs = await this.service.request(`/api/flow-runs/${encodeURIComponent(id)}/handoffs`)
      this.post({ type: 'handoffs', handoffs })
      break
    }
    case 'loadDecisions': {
      const decisions = await this.service.request('/api/decisions')
      this.post({ type: 'decisions', decisions })
      break
    }
    case 'resolveDecision': {
      // Путь приходит из webview, поэтому он проверяется здесь, а не
      // принимается на веру: иначе скомпрометированный webview мог бы
      // заставить расширение постучаться в любой маршрут ядра.
      const path = decisionResolvePath(message.path)
      // Не `notify` + `break`: это выход внутри try, и webview остаётся в
      // «загрузке» навсегда. Бросаем — перехват и покажет, и разморозит.
      if (!path) throw new Error('Отклонён неизвестный путь решения.')
      // Тело собирается по описанию самого маршрута, а не по одному правилу
      // на всех. Прежнее правило — «поле решения плюс id» — не подходило ни
      // одному из них: ядро отвергает чужие поля, и очередь получала 400 на
      // каждое нажатие. Идентификатор кладётся только туда, где маршрут его
      // назвал: у половины он уже в пути, и в теле он там лишний.
      //
      // Имена полей приходят из webview, поэтому проверяются здесь, как и
      // путь: ключом тела может быть только простой идентификатор.
      const key = value => (/^[a-zA-Z][a-zA-Z0-9_]{0,40}$/.test(String(value || '')) ? String(value) : '')
      const body = {}
      const field = key(message.field)
      const idField = key(message.idField)
      if (field) body[field] = message.value
      if (idField && message.id) body[idField] = String(message.id)
      await this.service.request(path, {
        method: 'POST', body: JSON.stringify(body),
        timeoutMs: path === '/api/quest-proposals/decide' ? QUEST_DECISION_TIMEOUT_MS : undefined,
      })
      const [decisions, runtime] = await Promise.all([
        this.service.request('/api/decisions'),
        this.service.request('/api/state/runtime'),
      ])
      this.post({ type: 'decisions', decisions })
      this.patchBoot(runtime)
      await this.refreshLiveCompanionInterventions()
      this.postState()
      break
    }
    case 'resolveChangeSet':
      await this.service.request(`/api/change-sets/${encodeURIComponent(message.id)}/resolve`, {
        method: 'POST',
        body: JSON.stringify({ strategy: message.strategy, path: message.path, content: message.content }),
      })
      await this.refreshRuntimeState({ companion: true })
      this.postState()
      break
    case 'resolveApproval':
      await this.service.request(`/api/approvals/${encodeURIComponent(message.id)}/resolve`, { method: 'POST', body: JSON.stringify({ allow: Boolean(message.allow) }) })
      if (this.activeRunId) await this.loadRun(this.activeRunId)
      break
    case 'loadRun':
      await this.loadRun(message.id); this.focusTab('quests'); this.postState(); break
    case 'decideQuestProposal': {
      try {
        const orchestratorApiKey = message.action === 'start'
          ? await this.credentialForOrchestrator()
          : ''
        const decision = await this.service.request('/api/quest-proposals/decide', {
          method: 'POST',
          timeoutMs: QUEST_DECISION_TIMEOUT_MS,
          body: JSON.stringify({
            proposalId: String(message.proposalId || ''),
            brief: message.brief || undefined,
            expectedVersion: message.expectedVersion,
            approveVersion: message.approveVersion,
            action: String(message.action || ''),
            title: message.title || undefined,
            objectives: Array.isArray(message.objectives) ? message.objectives : undefined,
            constraints: Array.isArray(message.constraints) ? message.constraints : undefined,
            definitionOfDone: Array.isArray(message.definitionOfDone) ? message.definitionOfDone : undefined,
            teamAgentIds: Array.isArray(message.teamAgentIds) ? message.teamAgentIds : undefined,
            flowId: message.flowId || undefined,
            importance: message.importance || undefined,
            startFlow: message.action === 'start' ? true : Boolean(message.startFlow),
            orchestratorApiKey,
          }),
        })
        await this.refreshRuntimeState({ companion: true })
        if (message.action === 'start') {
          const fromMaster = message.origin === 'master'
          await this.launchPendingHubExecutions(decision?.quest?.id)
          const execution = (this.boot?.executions || []).find(item =>
            item.questId === decision?.quest?.id && item.runId
            && (item.status === 'running' || item.status === 'waiting_approval' || item.status === 'paused')
          ) || (this.boot?.executions || []).find(item =>
            item.questId === decision?.quest?.id && item.runId
          )
          if (execution?.runId) await this.loadRun(execution.runId, false)
          if (decision?.orchestratorNote && typeof vscode.window.showInformationMessage === 'function') {
            const source = decision.orchestratorMode === 'model'
              ? `AI-план ${decision.orchestratorModel || ''}`.trim()
              : decision.orchestratorMode === 'model-fallback'
                ? 'Резервный план Point'
                : 'План оркестратора'
            void vscode.window.showInformationMessage(`${source}: ${decision.orchestratorNote}`)
          }
          if (decision?.plannerFallback) {
            this.service.hostLog('warn', `[orchestrator] model fallback: ${String(decision.plannerFallback).slice(0, 500)}`)
            this.post({
              type: 'plannerFallbackNotice',
              questId: String(decision?.quest?.id || ''),
              message: String(decision.plannerFallback),
              mode: String(decision.orchestratorMode || 'model-fallback'),
            })
          }
          this.post({
            type: 'questProposalStarted',
            proposalId: String(message.proposalId || ''),
            questId: String(decision?.quest?.id || ''),
            runId: String(execution?.runId || this.activeRunId || ''),
            stayInMaster: fromMaster,
          })
          // Старт с Мастера остаётся в переписке: там же смотрят работу агентов.
          // С обзора/решений по-прежнему уходим на overview к запущенному квесту.
          if (!fromMaster) this.focusTab('overview')
        } else if (message.action === 'modify') {
          this.post({ type: 'questProposalModified', proposalId: String(message.proposalId || '') })
        }
        // Уводит с места только запуск, остальное оставляет где был.
        //
        // Карточка предложения живёт не только в обзоре: тот же квест
        // отклоняют прямо в разговоре с Мастером, где он и был предложен.
        // Уводить оттуда в обзор значило выбрасывать человека из переписки
        // за «нет, не это» — ровно тогда, когда он собирался уточнить задачу
        // следующей репликой. Отклонённое предложение он и так видит: в
        // переписке карточка становится строкой «предложение отклонено».
        this.postState()
      } catch (error) {
        this.post({ type: 'error', request: '/api/quest-proposals/decide', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'decideCompanionAction': {
      try {
        const fromMaster = message.origin === 'master'
        const result = await this.service.request('/api/companion/actions/decide', {
          method: 'POST',
          body: JSON.stringify({
            proposalId: String(message.proposalId || ''),
            brief: message.brief || undefined,
            expectedVersion: message.expectedVersion,
            approveVersion: message.approveVersion,
            action: String(message.action || ''),
            name: message.name || undefined,
            description: message.description || undefined,
            roleDescription: message.roleDescription || undefined,
            mission: message.mission || undefined,
            agentIds: Array.isArray(message.agentIds) ? message.agentIds : undefined,
            instructions: message.instructions || undefined,
            requiredTools: Array.isArray(message.requiredTools) ? message.requiredTools : undefined,
            // Настройки исполнителя из карточки ленты. Ядро принимало только имя,
            // роль и миссию — модель, умения и пределы, показанные в карточке,
            // терялись по дороге, и агент заводился не тем, кого согласовали.
            allowedTools: Array.isArray(message.allowedTools) ? message.allowedTools : undefined,
            toolPolicies: message.toolPolicies && typeof message.toolPolicies === 'object' ? message.toolPolicies : undefined,
            connectionId: message.connectionId || undefined,
            primaryModel: message.primaryModel || undefined,
            reasoningEffort: message.reasoningEffort || undefined,
            approvalMode: message.approvalMode || undefined,
            maxSteps: Number(message.maxSteps) > 0 ? Number(message.maxSteps) : undefined,
            maxDurationSeconds: Number(message.maxDurationSeconds) > 0 ? Number(message.maxDurationSeconds) : undefined,
            maxOutputTokens: Number(message.maxOutputTokens) > 0 ? Number(message.maxOutputTokens) : undefined,
            temperature: Number.isFinite(message.temperature) ? Number(message.temperature) : undefined,
          }),
        })
        await this.refreshRuntimeAndGuildState({ companion: true })
        if (message.action === 'apply') {
          const hubFocused = Boolean(this.panel && this.panel.visible !== false)
          let guildTab = ''
          if (result?.flow?.id) guildTab = 'flows'
          else if (result?.agent?.id) guildTab = 'agents'
          else if (result?.team?.id) guildTab = 'teams'
          else if (result?.skill?.id) guildTab = 'skills'
          if (hubFocused && guildTab && !fromMaster) this.focusTab(guildTab)
          this.post({
            type: 'companionActionApplied',
            proposalId: String(message.proposalId || ''),
            brief: message.brief || undefined,
            expectedVersion: message.expectedVersion,
            approveVersion: message.approveVersion,
            flowId: result?.flow?.id,
            agentId: result?.agent?.id,
            teamId: result?.team?.id,
            skillId: result?.skill?.id,
            guildTab,
            stayInCompanion: !hubFocused,
            stayInMaster: fromMaster,
          })
        } else if (message.action === 'modify') {
          this.post({ type: 'companionActionModified', proposalId: String(message.proposalId || '') })
        }
        this.postState()
      } catch (error) {
        this.post({ type: 'error', request: '/api/companion/actions/decide', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'previewEquipSkill': {
      try {
        const preview = await this.service.request('/api/project-skills/preview', {
          method: 'POST',
          body: JSON.stringify({ skillId: String(message.skillId || '') }),
        })
        this.post({ type: 'skillEquipPreview', preview, skillId: message.skillId })
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'equipSkill': {
      try {
        const saved = await this.service.request('/api/project-skills/equip', {
          method: 'POST',
          body: JSON.stringify({ skillId: String(message.skillId || '') }),
        })
        this.upsertBootItem('projectSkills', saved)
        this.post({ type: 'skillEquipped', skillId: message.skillId })
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'revertExecution': {
      try {
        await this.service.request(`/api/executions/${encodeURIComponent(message.id)}/revert`, { method: 'POST', body: '{}' })
        await this.refreshRuntimeState({ companion: true })
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'revertQuest': {
      try {
        await this.service.request(`/api/quests/${encodeURIComponent(message.id)}/revert`, { method: 'POST', body: '{}' })
        await this.refreshRuntimeState({ companion: true })
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'revertFlowNode': {
      try {
        await this.service.request(`/api/flow-runs/${encodeURIComponent(message.flowRunId)}/nodes/${encodeURIComponent(message.nodeId)}/revert`, { method: 'POST', body: '{}' })
        await this.refreshRuntimeState({ companion: true })
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'launchExecution': {
      try {
        const run = await this.launchHubExecution(message.id, String(message.apiKey || ''))
        await this.refreshRuntimeState({ companion: true })
        if (run?.id) await this.loadRun(run.id)
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'resolveFlowNode': {
      try {
        await this.service.request(`/api/flow-runs/${encodeURIComponent(message.flowRunId)}/nodes/${encodeURIComponent(message.nodeId)}/resolve`, {
          method: 'POST',
          body: JSON.stringify({ approved: Boolean(message.approved) }),
        })
        await this.refreshRuntimeState({ companion: true })
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'resolveFlowMerge': {
      try {
        await this.service.request(`/api/flow-runs/${encodeURIComponent(message.flowRunId)}/nodes/${encodeURIComponent(message.nodeId)}/merge/resolve`, {
          method: 'POST',
          body: JSON.stringify(message.resolution || {}),
        })
        await this.refreshRuntimeState({ companion: true })
        this.focusTab('flows')
        this.postState()
      } catch (error) {
        this.post({ type: 'error', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'applyChangeSetChain': {
      const byId = new Map((this.boot?.changeSets || []).map(item => [item.id, item]))
      const ordered = []
      const visited = new Set()
      const visiting = new Set()
      const visit = id => {
        if (visited.has(id)) return
        if (visiting.has(id)) throw new Error(`Цепочка Change Set содержит цикл у ${id}`)
        const set = byId.get(id)
        if (!set) throw new Error(`Связанный Change Set ${id} не найден`)
        visiting.add(id)
        for (const dependencyId of set.dependsOn || []) visit(dependencyId)
        visiting.delete(id)
        visited.add(id)
        ordered.push(set)
      }
      visit(String(message.id || ''))
      for (const set of ordered) {
        if (set.status === 'applied') continue
        if (!['pending', 'approved', 'conflict'].includes(set.status)) {
          throw new Error(`Change Set ${set.id} нельзя применить из состояния ${set.status}`)
        }
        await this.service.request(`/api/change-sets/${encodeURIComponent(set.id)}/apply`, { method: 'POST', body: '{}' })
      }
      await this.refreshRuntimeState({ companion: true })
      this.postState()
      break
    }
    case 'applyChangeSet':
      await this.service.request(`/api/change-sets/${encodeURIComponent(message.id)}/apply`, { method: 'POST', body: '{}' })
      await this.refreshRuntimeState({ companion: true })
      this.postState()
      break
    case 'rejectChangeSet':
      await this.service.request(`/api/change-sets/${encodeURIComponent(message.id)}/reject`, { method: 'POST', body: '{}' })
      await this.refreshRuntimeState({ companion: true })
      this.postState()
      break
    case 'revertChangeSet':
      await this.service.request(`/api/change-sets/${encodeURIComponent(message.id)}/revert`, { method: 'POST', body: '{}' })
      await this.refreshRuntimeState({ companion: true })
      this.postState()
      break
  }
}

module.exports = { handleHubRuntimeMessage, QUEST_DECISION_TIMEOUT_MS }
