// Какой запрос держит общий замок конкретной формы.
//
// Ошибка другого фонового запроса не подтверждает и не отменяет текущую
// отправку: если отпустить замок на отказе Docker во время saveTeam, второй
// клик создаст второй отряд, пока первый запрос всё ещё выполняется.
const FORM_REQUESTS = {
  'git-commit-form': ['gitAction'],
  'agent-form': ['startRun'],
  'settings-form': ['saveProfile', 'saveProjectAgent'],
  'constructor-form': ['saveProfile', 'saveProjectAgent'],
  'custom-tool-form': ['saveCustomTool'],
  'workflow-form': ['saveWorkflow'],
  'workflow-run-form': ['startWorkflow'],
  'companion-setup-form': ['saveCompanionConfig'],
  'budget-form': ['saveBudget'],
  'team-form': ['saveTeam'],
  'skill-form': ['saveSkill'],
  'connection-form': ['saveConnection'],
  'server-form': ['saveServerProfile'],
  'db-connection-form': ['saveDBConnection'],
  'db-query-form': ['queryDBConnection'],
  'flow-form': ['saveFlow'],
  'experience-search-form': ['searchExperience'],
  'manual-learning-form': ['previewManualLearning'],
  'memory-form': ['saveMemory'],
}

const FORM_ACKNOWLEDGEMENTS = {
  runStarted: ['agent-form'],
  profileSaved: ['settings-form', 'constructor-form'],
  projectAgentSaved: ['settings-form', 'constructor-form'],
  customToolSaved: ['custom-tool-form'],
  workflowSaved: ['workflow-form'],
  workflowRunStarted: ['workflow-run-form'],
  companionConfigSaved: ['companion-setup-form'],
  budgetSaved: ['budget-form'],
  teamSaved: ['team-form'],
  skillSaved: ['skill-form'],
  connectionSaved: ['connection-form'],
  serverProfileSaved: ['server-form'],
  dbConnectionSaved: ['db-connection-form'],
  dbQueryResult: ['db-query-form'],
  dbWriteRequired: ['db-query-form'],
  flowSaved: ['flow-form'],
  experienceSearch: ['experience-search-form'],
  manualLearningPreview: ['manual-learning-form'],
  memorySaved: ['memory-form'],
}

// Пустой/неизвестный request остаётся аварийным общим выходом для старых
// хостов и новых, ещё не описанных форм.
export function failedRequestOwnsForm(formId, request) {
  if (!formId) return false
  if (!request) return true
  const requests = FORM_REQUESTS[formId]
  return !requests || requests.includes(request)
}

export function acknowledgesForm(formId, message) {
  if (!formId || !message?.type) return false
  // startFastAgent отвечает тем же runStarted, но к форме запуска квеста
  // отношения не имеет и не должен снимать её guard.
  if (message.type === 'runStarted' && message.fastAgent) return false
  return (FORM_ACKNOWLEDGEMENTS[message.type] || []).includes(formId)
}
