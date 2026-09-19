// Опрос: пока квест жив, состояние надо спрашивать.
//
// Четырнадцать методов об одном таймере, вернее о трёх: прогон, workflow и
// координатор flow. Они начинают опрос, переживают скрытие панели и
// останавливаются — по-отдельности и все разом при закрытии.
//
// Правило, ради которого семейство и стоит читать целиком: опрос прекращается,
// когда Хаб не виден, и возобновляется, когда виден снова. Раньше это условие
// было размазано по четырём методам в разных концах класса.

function createHubPolling({ cursorRuntime, describeCoreFailure, vscode }) {
  function startPolling(provider) {
    if (!provider.shouldPollRun()) {
      provider.stopRunPolling(false)
      return
    }
    if (provider.pollTimer) return
    if (provider.pollInFlight) return
    const tick = async () => {
      provider.pollTimer = undefined
      if (!provider.shouldPollRun()) return
      provider.pollInFlight = true
      let keepPolling = false
      try {
        if (!provider.activeRunId || provider.service.state !== 'running') return
        const visible = provider.hubVisible()
        await provider.loadRun(provider.activeRunId, visible, true)
        keepPolling = provider.shouldPollRun()
        if (keepPolling) {
          provider.companionPollTick += 1
          if (visible && provider.companionPollTick % 4 === 0) await provider.refreshLiveCompanionInterventions()
        }
        if (!keepPolling) {
          await provider.refreshRuntimeState({ companion: true })
          await provider.coordinateActiveFlows(false)
          keepPolling = provider.shouldPollRun()
          if (visible) provider.postState()
        }
      } catch (error) { provider.notify(error) }
      finally {
        provider.pollInFlight = false
        if (keepPolling && !provider.pollTimer) provider.pollTimer = setTimeout(tick, provider.hubVisible() ? 1500 : 2500)
      }
    }
    provider.pollTimer = setTimeout(tick, 600)
  }
  function shouldPollRun(provider) {
    const status = provider.details?.run?.status
    return Boolean(provider.activeRunId) && (status === 'running' || (provider.hubVisible() && status === 'waiting_approval'))
  }
  async function loadWorkflowRun(provider, id, post = true, asDelta = false) {
    provider.workflowDetails = await provider.service.request(`/api/workflow-runs/${encodeURIComponent(id)}`)
    provider.activeWorkflowRunId = id
    void provider.maybeRunCursorWorkflowStep()
    provider.updateAgentBusy()
    if (post) {
      if (asDelta) provider.postRunDelta()
      else provider.postState()
    }
    if (['running', 'waiting_approval'].includes(provider.workflowDetails.status)) provider.startWorkflowPolling()
  }
  async function maybeRunCursorWorkflowStep(provider) {
    const run = provider.workflowDetails
    if (provider.cursorRun || provider.cursorWorkflowStepBusy || !run || !['running', 'waiting_approval'].includes(run.status)) return
    const step = (run.stepRuns || []).find(item => item.status === 'waiting_approval' && item.kind === 'cursor')
    if (!step?.stepId) return
    const definition = (run.snapshot?.workflow?.steps || []).find(item => item.id === step.stepId)
    const profile = provider.boot?.profiles?.find(item => item.id === (step.profileId || definition?.profileId))
    if (!profile || profile.provider !== 'cursor-cli') return
    provider.cursorWorkflowStepBusy = true
    let heartbeat
    let claimToken = ''
    try {
      const claimed = await provider.service.request(`/api/workflow-runs/${encodeURIComponent(run.id)}/steps/${encodeURIComponent(step.stepId)}/claim`, { method: 'POST', body: '{}' })
      claimToken = claimed?.claimToken || ''
      if (!claimToken) throw new Error('Cursor step claim token was not issued.')
      const cwd = provider.workspaceFolder()?.uri?.fsPath
      const runtime = await provider.refreshCursorRuntime()
      if (!cwd || !runtime.available || !runtime.authenticated) throw new Error(runtime.error || 'Cursor Agent недоступен для этапа.')
      const task = [definition?.instruction, run.task].filter(Boolean).join('\n\n') || run.task
      heartbeat = setInterval(() => {
        void provider.service.request(`/api/workflow-runs/${encodeURIComponent(run.id)}/steps/${encodeURIComponent(step.stepId)}/heartbeat`, {
          method: 'POST',
          body: JSON.stringify({ claimToken }),
        }).catch(() => {})
      }, 15_000)
      provider.cursorRun = cursorRuntime.startRun({
        profile,
        cwd,
        task,
        onEvent: event => provider.post({ type: 'cursorRunEvent', event, workflowRunId: run.id, stepId: step.stepId }),
      })
      provider.updateAgentBusy()
      const result = await provider.cursorRun.done
      clearInterval(heartbeat)
      heartbeat = undefined
      await provider.service.request(`/api/workflow-runs/${encodeURIComponent(run.id)}/steps/${encodeURIComponent(step.stepId)}/complete`, {
        method: 'POST',
        body: JSON.stringify({
          claimToken,
          status: result?.status === 'error' || result?.status === 'cancelled' ? 'failed' : 'completed',
          result: result?.result || result?.summary || '',
          error: result?.status === 'error' ? (result?.result || 'Cursor run failed') : '',
        }),
      })
      await provider.loadWorkflowRun(run.id)
    } catch (error) {
      if (claimToken) {
        await provider.service.request(`/api/workflow-runs/${encodeURIComponent(run.id)}/steps/${encodeURIComponent(step.stepId)}/complete`, {
          method: 'POST',
          body: JSON.stringify({ claimToken, status: 'failed', error: describeCoreFailure(error) }),
        }).catch(() => {})
      }
      provider.notify(error)
    } finally {
      if (heartbeat) clearInterval(heartbeat)
      provider.cursorRun = undefined
      provider.cursorWorkflowStepBusy = false
      provider.updateAgentBusy()
    }
  }
  function startWorkflowPolling(provider) {
    if (!provider.hubVisible()) {
      provider.stopPollingTimers(false)
      return
    }
    if (provider.workflowPollTimer) return
    if (provider.workflowPollInFlight) {
      provider.workflowPollTimer = setTimeout(() => {
        provider.workflowPollTimer = undefined
        provider.startWorkflowPolling()
      }, 150)
      return
    }
    const tick = async () => {
      provider.workflowPollTimer = undefined
      if (!provider.hubVisible()) return
      provider.workflowPollInFlight = true
      let keepPolling = false
      try {
        if (!provider.activeWorkflowRunId || provider.service.state !== 'running') return
        await provider.loadWorkflowRun(provider.activeWorkflowRunId, true, true)
        keepPolling = ['running', 'waiting_approval'].includes(provider.workflowDetails?.status)
        if (!keepPolling) {
          await provider.refreshRuntimeState()
          provider.postState()
        }
      } catch (error) { provider.notify(error) }
      finally {
        provider.workflowPollInFlight = false
        if (keepPolling && provider.hubVisible() && !provider.workflowPollTimer) provider.workflowPollTimer = setTimeout(tick, 1500)
      }
    }
    provider.workflowPollTimer = setTimeout(tick, 600)
  }
  function hubVisible(provider) {
    return Boolean(
      (provider.panel && provider.panel.visible)
      || (provider.view && provider.view.visible)
      || (provider.companionSidebar && provider.companionSidebar.visible)
      || (provider.companionPopup && provider.companionPopup.visible)
      || (provider.connectionsPanel && provider.connectionsPanel.visible)
      || (provider.statisticsPanel && provider.statisticsPanel.visible)
      || (provider.dockerPanel && provider.dockerPanel.visible)
      || [...provider.toolWindows.values()].some(view => view && view.visible),
    )
  }
  function stopPollingTimers(provider, clearInFlight = true) {
    provider.stopRunPolling(clearInFlight)
    provider.stopWorkflowPolling(clearInFlight)
    provider.stopFlowCoordinator(clearInFlight)
  }
  function stopRunPolling(provider, clearInFlight = true) {
    if (provider.pollTimer) clearTimeout(provider.pollTimer)
    provider.pollTimer = undefined
    if (clearInFlight) provider.pollInFlight = false
  }
  function stopWorkflowPolling(provider, clearInFlight = true) {
    if (provider.workflowPollTimer) clearTimeout(provider.workflowPollTimer)
    provider.workflowPollTimer = undefined
    if (clearInFlight) provider.workflowPollInFlight = false
  }
  function stopFlowCoordinator(provider, clearInFlight = true) {
    if (provider.flowCoordinatorTimer) clearTimeout(provider.flowCoordinatorTimer)
    provider.flowCoordinatorTimer = undefined
    if (clearInFlight) {
      provider.flowCoordinatorInFlight = false
      provider.pendingExecutionLaunchInFlight = false
    }
  }
  function resumePollingIfNeeded(provider) {
    if (!provider.hubVisible()) return
    if (provider.activeRunId && ['running', 'waiting_approval'].includes(provider.details?.run?.status)) provider.startPolling()
    if (provider.activeWorkflowRunId && ['running', 'waiting_approval'].includes(provider.workflowDetails?.status)) provider.startWorkflowPolling()
  }
  function updateAgentBusy(provider) {
    const active = status => ['running', 'waiting_approval'].includes(status)
    provider.onAgentBusy(Boolean(provider.cursorRun) || active(provider.details?.run?.status) || active(provider.workflowDetails?.status))
  }
  function onHubVisibility(provider, visible) {
    if (visible) {
      provider.postState(true)
      if (vscode.workspace.isTrusted && vscode.workspace.getConfiguration('localAgent').get('autoStart', true)) {
        provider.scheduleAutoStart()
      }
      provider.resumePollingIfNeeded()
    } else {
      if (provider.details?.run?.status === 'running') provider.startPolling()
      else provider.stopRunPolling(false)
      provider.stopWorkflowPolling(false)
    }
  }
  function onServiceStatus(provider, value) {
    if (value?.state === 'running') provider.updateAgentBusy()
    else provider.onAgentBusy(false)
    provider.postState()
  }

  return {
    startPolling, shouldPollRun, loadWorkflowRun, maybeRunCursorWorkflowStep,
    startWorkflowPolling, hubVisible, stopPollingTimers, stopRunPolling,
    stopWorkflowPolling, stopFlowCoordinator, resumePollingIfNeeded, updateAgentBusy,
    onHubVisibility, onServiceStatus,
  }
}

module.exports = { createHubPolling }
