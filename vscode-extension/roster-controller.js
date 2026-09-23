// Гильдия: чертежи, проектные агенты, отряды, навыки и их удаление.
//
// Самая крупная однородная группа, оставшаяся в handleMessage: восемнадцать
// веток о том, кто есть в проекте и что ему разрешено. Они лежали тремя
// кусками в разных концах switch на восемьсот строк, между ними — докер,
// подключения и вкладки.
//
// Зависимостей нет: обработчик вызывается через .call(this, message), поэтому
// все методы и поля провайдера доступны как прежде. Так же устроены
// hub-runtime-controller, infra-controller и master-chat-controller.

const vscode = require('vscode')

async function handleRosterMessage(message) {
  switch (message.type) {
    case 'saveProfile':
      {
        const saved = await this.service.request('/api/profiles', { method: 'POST', body: JSON.stringify(message.profile) })
        this.upsertBootItem('profiles', saved)
        this.focusTab('settings')
        this.post({ type: 'profileSaved', profileId: saved.id })
        this.postState()
      }
      break
    case 'deleteProfile':
      await this.deleteProfile(message.id); break
    case 'deleteProjectAgent':
      await this.deleteProjectAgent(message.id); break
    case 'activateProjectAgentDraft': {
      if (message.agent) {
        const draft = await this.service.request('/api/project-agents', { method: 'POST', body: JSON.stringify(message.agent) })
        this.upsertBootItem('projectAgents', draft)
      }
      const saved = await this.service.request(`/api/project-agents/${encodeURIComponent(message.id)}/activate-draft`, { method: 'POST', body: '{}' })
      this.upsertBootItem('projectAgents', saved)
      this.postState()
      this.post({ type: 'projectAgentDraftActivated', agent: saved, agentId: saved.id })
      break
    }
    case 'rejectProjectAgentDraft': {
      const answer = await vscode.window.showWarningMessage(
        'Отклонить черновик агента? Он и его временные субагенты будут удалены, а комплектовщик попробует подобрать замену без повторения этого семейства.',
        { modal: true },
        'Отклонить черновик',
      )
      if (answer !== 'Отклонить черновик') break
      const result = await this.service.request(`/api/project-agents/${encodeURIComponent(message.id)}/reject-draft`, { method: 'POST', body: '{}' })
      await this.refreshGuildState()
      this.postState()
      this.post({ type: 'projectAgentDraftRejected', result, agentId: message.id })
      break
    }
    case 'deleteQuest':
      await this.deleteQuest(message.id); break
    case 'purgeQuest':
      await this.purgeQuest(message.id); break
    case 'deleteTeam':
      await this.deleteTeam(message.id); break
    case 'deleteFlow':
      await this.deleteFlow(message.id); break
    case 'deleteWorkOrderV2':
      await this.deleteWorkOrderV2(message.id); break
    case 'deleteBlueprint':
      await this.deleteBlueprint(message.id); break
    case 'exportProfile':
      await this.exportProfile(message.profile); break
    case 'importProfile':
      await this.importProfile(); break
    case 'saveTeam': {
      try {
        const saved = await this.service.request('/api/teams', {
          method: 'POST',
          body: JSON.stringify(message.team || {}),
        })
        this.upsertBootItem('teams', saved)
        this.post({ type: 'teamSaved', teamId: saved?.id || '' })
        this.postState()
      } catch (error) {
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'saveSkill': {
      try {
        const saved = await this.service.request('/api/skills', {
          method: 'POST',
          body: JSON.stringify(message.skill || {}),
        })
        this.upsertBootItem('skills', saved)
        this.focusTab('skills')
        this.post({
          type: 'skillSaved',
          skillId: saved?.id,
          equipAfterSave: Boolean(message.equipAfterSave),
        })
        this.postState()
      } catch (error) {
        this.post({ type: 'error', request: message.type, message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'saveBlueprint': {
      const saved = await this.service.request('/api/blueprints', { method: 'POST', body: JSON.stringify(message.blueprint) })
      this.upsertBootItem('blueprints', saved)
      this.post({ type: 'blueprintSaved', blueprintId: saved.id })
      this.postState()
      break
    }
    case 'previewCompiledPrompt': {
      try {
        const preview = await this.service.request('/api/project-agents/preview-prompt', {
          method: 'POST',
          body: JSON.stringify(message.agent || {}),
        })
        this.post({ type: 'compiledPromptPreview', preview })
      } catch (error) {
        this.post({ type: 'compiledPromptPreviewError', message: error instanceof Error ? error.message : String(error) })
      }
      break
    }
    case 'saveProjectAgent': {
      let agent = message.agent
      // Selector drafts deliberately have no global Blueprint. Editing and
      // saving their card must keep it that way until a separate activation.
      if (!agent?.blueprintId && agent?.status !== 'draft') {
        const blueprint = await this.service.request('/api/blueprints', {
          method: 'POST',
          body: JSON.stringify({
            id: '',
            name: agent.name,
            roleDescription: agent.roleDescription,
            personality: agent.personality,
            mission: agent.mission,
            systemPrompt: agent.systemPrompt,
            goals: agent.goals || [],
            rules: agent.rules || [],
            constraints: agent.constraints || [],
            skillIds: agent.skillIds || [],
            allowedTools: agent.allowedTools || [],
            toolPolicies: agent.toolPolicies || {},
            connectionId: agent.connectionId || '',
            provider: agent.provider,
            providerPreset: agent.providerPreset,
            baseUrl: agent.baseUrl,
            primaryModel: agent.primaryModel,
            fallbackModels: agent.fallbackModels || [],
            temperature: agent.temperature,
            maxOutputTokens: agent.maxOutputTokens,
            contextWindowTokens: agent.contextWindowTokens,
            reasoningEffort: agent.reasoningEffort,
            maxSteps: agent.maxSteps,
            maxDurationSeconds: agent.maxDurationSeconds,
            approvalMode: agent.approvalMode,
          }),
        })
        this.upsertBootItem('blueprints', blueprint)
        agent = { ...agent, blueprintId: blueprint.id }
      }
      const saved = await this.service.request('/api/project-agents', { method: 'POST', body: JSON.stringify(agent) })
      this.upsertBootItem('projectAgents', saved)
      // Мастерская агента живёт на вкладке «Агенты», и сохранение из неё
      // законно туда и возвращает. Но исполнителя заводят и прямо из ленты
      // Мастера — там переброс вкладки выкидывал человека из разговора,
      // ради которого агент и понадобился.
      if (!message.stayOnTab) this.focusTab('agents')
      // Подтверждение идёт раньше полного state. Карточке Мастера нужен сам
      // агент уже здесь, чтобы заменить точный черновик ростера, не пытаясь
      // найти новую запись в ещё старом снимке webview.
      this.post({ type: 'projectAgentSaved', agentId: saved.id, agent: saved })
      this.postState()
      break
    }
    case 'applyBlueprintToAgent': {
      const saved = await this.service.request(`/api/project-agents/${encodeURIComponent(message.agentId)}/apply-blueprint`, { method: 'POST', body: '{}' })
      this.upsertBootItem('projectAgents', saved)
      this.post({ type: 'projectAgentSaved', agentId: saved.id || message.agentId, agent: saved })
      this.postState()
      break
    }
    case 'previewBlueprintSync': {
      const diff = await this.service.request(`/api/project-agents/${encodeURIComponent(message.agentId)}/diff`)
      this.post({ type: 'blueprintSyncPreview', direction: message.direction, preview: diff })
      break
    }
    case 'updateBlueprintFromAgent': {
      const saved = await this.service.request(`/api/project-agents/${encodeURIComponent(message.agentId)}/update-blueprint`, { method: 'POST', body: '{}' })
      this.upsertBootItem('blueprints', saved)
      this.post({ type: 'blueprintSaved', blueprintId: saved?.id || saved?.blueprintId })
      this.postState()
      break
    }
  }
}

module.exports = { handleRosterMessage }
