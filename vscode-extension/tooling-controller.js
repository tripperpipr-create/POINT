// Свои инструменты и linear workflows.
//
// Девятнадцать веток: сохранение и предпросмотр своего инструмента, запрос и
// выдача разрешения на запуск, сам запуск, шаги workflow и пробы провайдера.
// Всё это — совместимый контур рядом с потоками, и держать его вперемешку с
// Мастером и Гильдией в одном switch незачем.
//
// Зависимостей нет: обработчик вызывается через .call(this, message).


async function handleToolingMessage(message) {
  switch (message.type) {
    case 'saveCustomTool':
      {
        const saved = await this.service.request('/api/custom-tools', { method: 'POST', body: JSON.stringify(message.tool) })
        this.syncCustomTool(saved)
        this.focusTab(message.equip ? 'agents' : 'tools')
        this.post({ type: 'customToolSaved', toolId: saved.id, equip: Boolean(message.equip) })
        this.postState()
      }
      break
    case 'previewCustomTool':
    case 'previewTool': {
      try {
        const preview = await this.service.request('/api/tools/preview', {
          method: 'POST',
          body: JSON.stringify({
            tool: message.tool,
            toolName: message.toolName || message.tool?.id || '',
            arguments: message.arguments || {},
            mode: message.mode || 'validate',
          }),
        })
        this.post({ type: 'customToolPreview', preview })
      } catch (error) {
        this.post({ type: 'customToolPreviewError', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'requestToolExecutionApproval': {
      const preview = await this.service.request('/api/tools/execution-approvals', {
        method: 'POST',
        body: JSON.stringify({ toolName: message.toolName || '', arguments: message.arguments || {} }),
      })
      this.post({ type: 'toolExecutionApprovalRequested', ...preview })
      break
    }
    case 'resolveToolExecutionApproval': {
      const approval = await this.service.request(`/api/tools/execution-approvals/${encodeURIComponent(message.approvalId || '')}/resolve`, {
        method: 'POST',
        body: JSON.stringify({ allow: message.allow === true }),
      })
      this.post({ type: 'toolExecutionApprovalResolved', approval })
      break
    }
    case 'executeTool': {
      const result = await this.service.request('/api/tools/execute', {
        method: 'POST',
        body: JSON.stringify({
          tool: message.tool,
          toolName: message.toolName || message.tool?.id || '',
          arguments: message.arguments || {},
          mode: message.mode || 'execute_readonly',
          approvalId: message.approvalId || '',
        }),
      })
      this.post({ type: 'toolExecuted', result })
      break
    }
    case 'claimWorkflowStep': {
      const claimed = await this.service.request(`/api/workflow-runs/${encodeURIComponent(message.runId)}/steps/${encodeURIComponent(message.stepId)}/claim`, { method: 'POST', body: '{}' })
      this.post({ type: 'workflowStepClaimed', runId: message.runId, stepId: message.stepId, claimToken: claimed?.claimToken })
      await this.loadWorkflowRun(message.runId)
      break
    }
    case 'heartbeatWorkflowStep':
      await this.service.request(`/api/workflow-runs/${encodeURIComponent(message.runId)}/steps/${encodeURIComponent(message.stepId)}/heartbeat`, {
        method: 'POST',
        body: JSON.stringify({ claimToken: message.claimToken || '' }),
      })
      break
    case 'completeWorkflowStep':
      await this.service.request(`/api/workflow-runs/${encodeURIComponent(message.runId)}/steps/${encodeURIComponent(message.stepId)}/complete`, {
        method: 'POST',
        body: JSON.stringify({
          claimToken: message.claimToken || '',
          status: message.status || (message.error ? 'failed' : 'completed'),
          result: message.result || '',
          error: message.error || '',
        }),
      })
      await this.loadWorkflowRun(message.runId)
      break
    case 'deleteCustomTool':
      await this.deleteCustomTool(message.id); break
    case 'exportCustomTool':
      await this.exportCustomTool(message.tool); break
    case 'importCustomTool':
      await this.importCustomTool(); break
    case 'probeProvider': {
      const result = await this.service.request('/api/providers/probe', { method: 'POST', body: JSON.stringify({ provider: message.provider, baseUrl: message.baseUrl, apiKey: message.apiKey || '' }) })
      this.post({ type: 'providerProbeResult', result })
      break
    }
    case 'probeModelCapability': {
      const profile = { ...(message.profile || {}) }
      profile.model = String(profile.model || profile.primaryModel || '')
      const result = await this.service.request('/api/providers/capability-probe', {
        method: 'POST',
        body: JSON.stringify({ profile, apiKey: String(message.apiKey || '') }),
        timeoutMs: 30_000,
      })
      this.post({ type: 'modelCapabilityProbeResult', result })
      break
    }
    case 'saveWorkflow': {
      const saved = await this.service.request('/api/workflows', { method: 'POST', body: JSON.stringify(message.workflow) })
      this.upsertBootItem('workflows', saved)
      this.focusTab('workflows')
      this.post({ type: 'workflowSaved', workflowId: saved.id })
      this.postState()
      break
    }
    case 'deleteWorkflow':
      await this.deleteWorkflow(message.id); break
    case 'startWorkflow': {
      const run = await this.service.request('/api/workflow-runs', { method: 'POST', body: JSON.stringify({ workflowId: message.workflowId, task: message.task, apiKeys: message.apiKeys || {}, contextItems: Array.isArray(message.contextItems) ? message.contextItems : [] }) })
      this.activeWorkflowRunId = run.id
      this.workflowDetails = run
      this.updateAgentBusy()
      this.focusTab('workflows')
      this.post({ type: 'workflowRunStarted' })
      this.postState()
      this.startWorkflowPolling()
      break
    }
    case 'cancelWorkflow':
      await this.service.request(`/api/workflow-runs/${encodeURIComponent(message.id)}/cancel`, { method: 'POST', body: '{}' })
      await this.loadWorkflowRun(message.id)
      break
    case 'loadWorkflowRun':
      await this.loadWorkflowRun(message.id); this.focusTab('workflows'); this.postState(); break
  }
}

module.exports = { handleToolingMessage }
