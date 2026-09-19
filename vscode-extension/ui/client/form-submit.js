// Отправка форм Хаба — все двадцать в одном месте.
//
// Функция и раньше была одна на все поверхности, но лежала посреди `main.js`
// между отрисовкой и слушателями. Двадцать форм — от коммита Git до настройки
// помощника — читались вперемешку с тем, к чему отношения не имеют.
//
// Замок повторной отправки остался в слушателе: он общий для всех форм и
// зависит от того, ушло ли что-то в ядро, а не от того, какая это форма.
// Помощник из-под замка выведен намеренно — у него своя политика очереди.

import { constructorToProfile, constructorToProjectAgent, legacySaveWouldDrop, newConstructorDraft } from './agent-constructor.js'

export function handleFormSubmit({
  event, ui, root, vscode, render,
  EMPTY_TASK_REASON, companionConfigFromDraft, companionSetupValidation, currentCompanionSetupDraft,
  hubModeAvailable, saveConnectionFromFields, submitGitCommit,
  canAcceptQuest, captureFlowForm, currentConstructorForm, currentCustomToolForm, currentFormProfile,
  currentWorkflowForm, customToolFormIssue, questPayload, sendCompanionUserMessage, stepValidationIssue,
}) {
  event.preventDefault()
  if (event.target.id === 'git-commit-form') {
    submitGitCommit('commit')
    return
  }
  if (event.target.id === 'agent-form') {
    const quest = questPayload()
    const profile = (ui.state.boot?.profiles || []).find(item => item.id === ui.selectedProfileId) || (ui.state.boot?.profiles || [])[0]
    const gate = canAcceptQuest(profile)
    if (!quest.task || !gate.ok) {
      // Причина называется по существу. Пустая задача — это пустая задача, а не
      // «завершите разведку»: человек искал бы несуществующую проблему.
      ui.transientError = !quest.task
        ? EMPTY_TASK_REASON
        : (gate.reasons[0] || 'Сначала завершите разведку и готовность персонажа')
      render()
      return
    }
    // Форма отправляется и щелчком, и по Enter, а между отправкой и ответом
    // ядра проходит время: задача и отпечаток разведки всё ещё на месте, и
    // вторая отправка запускала второй прогон той же задачи. Это два агента в
    // одних файлах и двойной расход — в отличие от кнопок с собственным id,
    // здесь повтор порождает новую сущность, а не повторяет старую.
    if (ui.runStarting) return
    ui.runStarting = true
    vscode.postMessage({ type: 'startRun', profileId: ui.selectedProfileId, ...quest, apiKey: ui.apiKey, contextItems: ui.contextItems, preflightFingerprint: ui.agentRunPreview?.fingerprint || '' })
  }
  if (event.target.id === 'settings-form') {
    const profile=currentFormProfile()
    if (!profile) return
    const submitter=event.submitter
    ui.hireAfterSave=submitter?.dataset?.hireIntent || 'card'
    ui.createStepError=''
    const issue=stepValidationIssue(ui.profileEditorStep === 'class' ? 'identity' : ui.profileEditorStep, profile)
    if(issue && !profile.id){ui.createStepError=issue;ui.profileDraft=profile;render();return}
    ui.profileDraft=profile
    if (hubModeAvailable()) vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(newConstructorDraft(profile)) })
    else vscode.postMessage({type:'saveProfile',profile})
  }
  if (event.target.id === 'constructor-form') {
    event.preventDefault()
    const draft = currentConstructorForm()
    if (!draft) return
    ui.constructorDraft = draft
    if (ui.constructorStep !== 'review') return
    if (!(draft.name || '').trim()) { ui.createStepError = 'Укажите имя агента'; render(); return }
    const dropped = legacySaveWouldDrop(draft)
    if (dropped) { ui.createStepError = dropped; render(); return }
    // Кнопка «Сохранить» гасит прежнюю ошибку, а Enter — нет: имя исправили,
    // сохранение ушло, а под шагами так и висело «Укажите имя агента».
    ui.createStepError = ''
    if (hubModeAvailable()) {
      vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(draft) })
    } else {
      vscode.postMessage({ type: 'saveProfile', profile: constructorToProfile(draft) })
    }
  }
  if (event.target.id === 'custom-tool-form') {
    const tool=currentCustomToolForm()
    const issue=customToolFormIssue(tool)
    if(issue){ui.transientError=issue;render();return}
    const equip=!tool?.id
    ui.toolEquipAfterSave=equip
    ui.transientError=''
    if(tool)vscode.postMessage({type:'saveCustomTool',tool,equip})
  }
  if (event.target.id === 'workflow-form') {
    const workflow=currentWorkflowForm()
    if(workflow)vscode.postMessage({type:'saveWorkflow',workflow})
  }
  if (event.target.id === 'workflow-run-form') {
    const task=root.querySelector('#workflow-task')?.value.trim()
    const apiKeys={}
    for(const input of root.querySelectorAll('.workflow-api-key'))if(input.value)apiKeys[input.dataset.profileId]=input.value
    if(task&&ui.selectedWorkflowId)vscode.postMessage({type:'startWorkflow',workflowId:ui.selectedWorkflowId,task,apiKeys,contextItems: ui.contextItems})
  }
  if (event.target.id === 'companion-setup-form') {
    ui.companionSetupDraft = currentCompanionSetupDraft()
    const issue = companionSetupValidation('brain', ui.companionSetupDraft) || companionSetupValidation('boundaries', ui.companionSetupDraft) || companionSetupValidation('skills', ui.companionSetupDraft)
    if (issue) { ui.companionSetupStatus = issue; render(); return }
    ui.companionSetupPendingClose = true
    ui.companionSetupStatus = ''
    vscode.postMessage({ type: 'saveCompanionConfig', config: companionConfigFromDraft(ui.companionSetupDraft) })
  }
  if (event.target.id === 'budget-form') {
    const toCents = selector => {
      const raw = root.querySelector(selector)?.value.trim() || ''
      if (!raw) return 0
      const dollars = Number(raw)
      if (!Number.isFinite(dollars) || dollars < 0) throw new Error('Бюджет должен быть неотрицательным числом')
      return Math.round(dollars * 100)
    }
    try {
      ui.statisticsStatus = 'loading'
      ui.transientError = ''
      vscode.postMessage({
        type: 'saveBudget',
        budget: {
          dailyCents: toCents('#budget-daily'),
          monthlyCents: toCents('#budget-monthly'),
          hardStop: Boolean(root.querySelector('#budget-hard-stop')?.checked),
        },
      })
      render()
    } catch (error) {
      ui.transientError = error instanceof Error ? error.message : String(error)
      render()
    }
  }
  if (event.target.id === 'companion-form') {
    const message = root.querySelector('#companion-input')?.value.trim()
    sendCompanionUserMessage(message)
  }
  if (event.target.id === 'team-form') {
    const name = root.querySelector('#team-name')?.value.trim()
    const description = root.querySelector('#team-description')?.value.trim() || ''
    const agentIds = [...root.querySelectorAll('input[name="team-agent"]:checked')].map(item => item.value)
    if (!name) return
    vscode.postMessage({ type: 'saveTeam', team: { name, description, agentIds } })
  }
  if (event.target.id === 'skill-form') {
    const existing = ui.skillEditId ? (ui.state.boot?.skills || []).find(item => item.id === ui.skillEditId) : null
    const name = root.querySelector('#skill-name')?.value.trim() || ''
    const description = root.querySelector('#skill-description')?.value.trim() || ''
    const instructions = root.querySelector('#skill-instructions')?.value.trim() || ''
    const requiredTools = [...root.querySelectorAll('input[name="skill-tool"]:checked')].map(item => item.value)
	const configuration = { ...(existing?.configuration || {}) }
	if (root.querySelector('#skill-deprecated')?.checked) configuration.lifecycleStatus = 'deprecated'
	else delete configuration.lifecycleStatus
    ui.skillEquipAfterSave = Boolean(root.querySelector('#skill-equip-after-save')?.checked)
    ui.skillDraft = {
      ...(existing || {}),
      id: existing?.id || '',
      name,
      description,
      instructions,
      requiredTools,
      permissionDelta: existing?.permissionDelta || {},
	  configuration,
      references: existing?.references || [],
      scripts: existing?.scripts || [],
    }
    if (!name) { ui.skillFormError = 'Укажите название Skill'; render(); return }
    if (name.length > 120) { ui.skillFormError = 'Название не длиннее 120 символов'; render(); return }
    if (!instructions) { ui.skillFormError = 'Инструкции обязательны — опишите практику и проверки'; render(); return }
    if (!requiredTools.length) { ui.skillFormError = 'Выберите хотя бы один требуемый tool'; render(); return }
    ui.skillFormError = ''
    const alreadyEquipped = ui.skillDraft.id && (ui.state.boot?.projectSkills || []).some(item => item.skillId === ui.skillDraft.id && item.enabled)
    vscode.postMessage({
      type: 'saveSkill',
      skill: ui.skillDraft,
      equipAfterSave: ui.skillEquipAfterSave && !alreadyEquipped,
    })
  }
  if (event.target.id === 'connection-form') { saveConnectionFromFields() }
  if (event.target.id === 'server-form') {
    const existing = ui.serverEditingId
      ? (ui.state.boot?.serverProfiles || []).find(item => item.id === ui.serverEditingId)
      : null
    const displayName = root.querySelector('#server-name')?.value.trim() || ''
    const host = root.querySelector('#server-host')?.value.trim() || ''
    const port = Number(root.querySelector('#server-port')?.value || 22)
    const user = root.querySelector('#server-user')?.value.trim() || ''
    const authMethod = root.querySelector('#server-auth')?.value || 'agent'
    const privateKeyPath = root.querySelector('#server-key')?.value.trim() || ''
    const defaultRemotePath = root.querySelector('#server-remote-path')?.value.trim() || '~'
    const password = root.querySelector('#server-password')?.value || ''
    if (!host || !user) { ui.transientError = 'Укажите хост и пользователя SSH.'; render(); return }
    if (authMethod === 'key' && !privateKeyPath) { ui.transientError = 'Для входа по ключу укажите абсолютный путь к ключу.'; render(); return }
    vscode.postMessage({
      type: 'saveServerProfile',
      id: existing?.id || '',
      secretRef: existing?.secretRef || '',
      displayName,
      host,
      port,
      user,
      authMethod,
      privateKeyPath,
      defaultRemotePath,
      password,
    })
  }
  if (event.target.id === 'db-connection-form') {
    const existing = ui.dbEditingId
      ? (ui.state.boot?.dbConnections || []).find(item => item.id === ui.dbEditingId)
      : null
    const displayName = root.querySelector('#db-name')?.value.trim() || ''
    const driver = root.querySelector('#db-driver')?.value || 'sqlite'
    const host = root.querySelector('#db-host')?.value.trim() || ''
    const port = Number(root.querySelector('#db-port')?.value || 0)
    const database = root.querySelector('#db-database')?.value.trim() || ''
    const username = root.querySelector('#db-user')?.value.trim() || ''
    const sslMode = root.querySelector('#db-ssl')?.value.trim() || ''
    const password = root.querySelector('#db-password')?.value || ''
    if (!database) { ui.transientError = 'Укажите имя БД или путь к файлу SQLite.'; render(); return }
    vscode.postMessage({
      type: 'saveDBConnection',
      id: existing?.id || '',
      secretRef: existing?.secretRef || '',
      displayName,
      driver,
      host,
      port,
      database,
      username,
      sslMode,
      password,
      readOnlyDefault: existing?.readOnlyDefault ?? true,
    })
  }
  if (event.target.id === 'db-query-form') {
    const sql = root.querySelector('#db-sql')?.value || ''
    const connectionId = ui.dbSelectedId || (ui.state.boot?.dbConnections || [])[0]?.id || ''
    if (!connectionId) { ui.transientError = 'Сначала сохраните подключение к БД.'; render(); return }
    if (!sql.trim()) { ui.transientError = 'Введите SQL.'; render(); return }
    ui.dbQueryStatus = 'loading'
    ui.dbQueryResult = undefined
    ui.dbWritePending = null
    render()
    vscode.postMessage({ type: 'queryDBConnection', connectionId, sql, allowWrite: false, approved: false })
  }
  if (event.target.id === 'flow-form') {
    const flow = captureFlowForm()
    vscode.postMessage({ type: 'saveFlow', flow })
  }
  if (event.target.id === 'experience-search-form') {
    ui.experienceSearchQuery = root.querySelector('#experience-search-query')?.value.trim() || ''
    if (ui.experienceSearchQuery.length < 2) { ui.transientError = 'Введите минимум 2 символа для поиска по опыту'; render(); return }
    ui.experienceSearchStatus = 'loading'
    vscode.postMessage({ type: 'searchExperience', query: ui.experienceSearchQuery })
    render()
  }
  if (event.target.id === 'manual-learning-form') {
    ui.manualLearningDraft = {
      projectAgentId: root.querySelector('#manual-learning-agent')?.value || '',
      kind: root.querySelector('#manual-learning-kind')?.value || 'memory',
      scope: root.querySelector('#manual-learning-scope')?.value || 'project',
      content: root.querySelector('#manual-learning-content')?.value.trim() || '',
    }
    if (!ui.manualLearningDraft.content) { ui.transientError = 'Сформулируйте урок перед preview'; render(); return }
    ui.manualLearningStatus = 'loading'
    ui.manualLearningPreview = undefined
    vscode.postMessage({ type: 'previewManualLearning', request: ui.manualLearningDraft })
    render()
  }
  if (event.target.id === 'memory-form') {
    const existing = ui.memoryEditId ? (ui.state.boot?.memories || []).find(item => item.id === ui.memoryEditId) : null
    const kind = root.querySelector('#memory-kind')?.value || 'project'
    let ownerId = root.querySelector('#memory-owner')?.value || ''
    if (kind === 'project' || kind === 'companion') ownerId = ''
    if (kind === 'profile' && !(ui.state.boot?.blueprints || []).some(item => item.id === ownerId)) { ui.transientError = 'Для переносимой памяти выберите основной профиль'; render(); return }
    if (kind === 'agent' && !(ui.state.boot?.projectAgents || []).some(item => item.id === ownerId)) { ui.transientError = 'Для Agent Memory выберите агента-владельца'; render(); return }
    if (kind === 'quest' && !(ui.state.boot?.quests || []).some(item => item.id === ownerId)) { ui.transientError = 'Для Quest Memory выберите квест-владельца'; render(); return }
    const memory = {
      ...(existing || {}),
      id: existing?.id || '',
      kind,
      content: root.querySelector('#memory-content')?.value.trim() || '',
      source: root.querySelector('#memory-source')?.value.trim() || '',
      ownerId,
      confidence: Number(root.querySelector('#memory-confidence')?.value ?? existing?.confidence ?? 0.5),
      pinned: Boolean(root.querySelector('#memory-pinned')?.checked),
    }
    if (!memory.content) { ui.transientError = 'Содержание памяти не может быть пустым'; render(); return }
    vscode.postMessage({ type: 'saveMemory', memory })
    ui.memoryEditId = ''
    ui.memoryDraft = undefined
  }
  const execForm = event.target.closest?.('[data-exec-form]')
  if (execForm) {
    const runId = execForm.dataset.runId
    const input = execForm.querySelector('input')
    const value = input?.value.trim()
    if (!runId || !value) return
    if (execForm.dataset.execForm === 'message') {
      const learningIntent = execForm.querySelector('input[name="learn-from-message"]')?.checked ? 'correction' : ''
      vscode.postMessage({ type: 'messageRun', runId, message: value, learningIntent })
    } else if (execForm.dataset.execForm === 'forbid') {
      vscode.postMessage({ type: 'forbidFile', runId, path: value })
    }
    if (input) input.value = ''
  }
}
