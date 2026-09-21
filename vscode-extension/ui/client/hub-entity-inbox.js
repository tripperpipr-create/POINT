// Ядро сохранило или удалило сущность Гильдии — здесь интерфейс закрывает
// редактор и выбирает следующий экран для этого типа ответа.
// Подключения и серверы стоят в том же списке, потому что приходят тем же
// ответом на ту же форму — их запрос и схема разделяют экран «Подключения» с
// сохранением профиля.
//
// Состояние приходит общим мешком `ui`, как в `companion-transport.js`.

import { newConstructorDraft } from './agent-constructor.js'

const HUB_ENTITY_MESSAGES = new Set([
  'dbConnectionSaved', 'serverProfileSaved', 'dbQueryResult',
  'dbWriteRequired', 'dbSchemaResult', 'profileSaved',
  'profileDeleted', 'projectAgentSaved', 'projectAgentDeleted',
  'projectAgentDraftActivated', 'projectAgentDraftRejected',
  'blueprintSaved', 'blueprintSyncPreview', 'questDeleted',
  'memoryDeleted',
])

export function createHubEntityInbox({
  ui,
  vscode,
  render,
  persistDraft,
  closeQuestIfOpen,
  resetQuestReplansCache,
  releaseMasterAgentCards,
  reviseWorkOrderRosterV2,
}) {
  return function applyHubEntityMessage(message) {
    if (!HUB_ENTITY_MESSAGES.has(message.type)) return false
      if (message.type === 'dbConnectionSaved') {
        if (message.id) ui.dbSelectedId = message.id
        ui.dbEditingId = ''
        ui.dbQueryResult = undefined
        ui.dbSchemaResult = undefined
        render()
      }
      if (message.type === 'serverProfileSaved') {
        ui.serverEditingId = ''
        render()
      }
      if (message.type === 'dbQueryResult') {
        if (ui.submittingForm === 'db-query-form') ui.submittingForm = ''
        ui.dbQueryStatus = 'idle'
        if (message.error) {
          ui.transientError = message.error
          ui.dbQueryResult = undefined
        } else {
          ui.dbQueryResult = message.result
          ui.transientError = ''
        }
        render()
      }
      if (message.type === 'dbWriteRequired') {
        if (ui.submittingForm === 'db-query-form') ui.submittingForm = ''
        ui.dbQueryStatus = 'idle'
        ui.dbWritePending = { connectionId: message.connectionId, sql: message.sql }
        ui.transientError = ''
        render()
      }
      if (message.type === 'dbSchemaResult') {
        ui.dbSchemaResult = message.result
        render()
      }
      if (message.type === 'profileSaved' || message.type === 'profileDeleted') {
        const intent = ui.hireAfterSave
        ui.hireAfterSave = ''
        ui.createStepError = ''
        ui.hirePreviewTemplateId = ''
        ui.profileDraft = undefined
        ui.profileEditorOpen = false
        ui.profileEditorStep = 'identity'
        if (ui.agentConstructorOpen) {
          ui.agentConstructorOpen = false
          ui.constructorDraft = undefined
        }
        ui.selectedProfileId = message.profileId || ''
        if (ui.state.selectedTab === 'onboarding' && ui.onboardingStep === 'first-agent') {
          ui.onboardingStep = 'model-connection'
          persistDraft()
        }
        persistDraft()
        if (message.type === 'profileSaved' && intent === 'quest') {
          vscode.postMessage({ type: 'selectTab', tab: 'quests' })
        }
      }
      if (message.type === 'projectAgentSaved') {
        const intent = ui.hireAfterSave
        ui.hireAfterSave = ''
        ui.createStepError = ''
        ui.blueprintSyncPreview = undefined
        ui.blueprintSyncDirection = ''
        ui.constructorDraft = undefined
        ui.agentConstructorOpen = false
        ui.selectedProfileId = message.agentId || ui.selectedProfileId
        if (ui.state.selectedTab === 'onboarding' && ui.onboardingStep === 'first-agent') {
          ui.onboardingStep = 'model-connection'
        }
        persistDraft()
        render()
        if (intent === 'quest') vscode.postMessage({ type: 'selectTab', tab: 'quests' })
        if (intent.startsWith('work-order:') && message.agentId) {
          // Наряд и черновик, ради которого нанимали. Идентификатор черновика
          // приписан к намерению через «|»: без него подстановка не знает, кого
          // именно заменил новый исполнитель.
          const [orderId, draftId] = intent.slice('work-order:'.length).split('|')
          const order = (Array.isArray(ui.masterData?.workOrders) ? ui.masterData.workOrders : []).find(item => item.id === orderId)
          const agent = (ui.state.boot?.projectAgents || []).find(item => item.id === message.agentId)
          if (order && agent) {
            const hired = {
              id: agent.id, blueprintId: agent.blueprintId || '', existing: true, name: agent.name,
              role: agent.roleDescription || '', mission: agent.mission || agent.roleDescription || '',
              requiredTools: Array.isArray(agent.allowedTools) ? agent.allowedTools : [],
            }
            reviseWorkOrderRosterV2(order.id, roster => {
              // Прежде сюда вставлялся весь состав одним человеком: наряд из трёх
              // исполнителей после найма четвёртого оставался с одним. Заменяем
              // только тот черновик, ради которого шли в мастерскую, а если он уже
              // исчез — первый незаведённый; остальных не трогаем.
              const permanent = Array.isArray(roster.permanent) ? [...roster.permanent] : []
              if (permanent.some(draft => draft.id === agent.id)) return roster
              const index = permanent.findIndex(draft => draft.id === draftId && !draft.existing)
              const fallback = permanent.findIndex(draft => !draft.existing)
              const at = index >= 0 ? index : fallback
              if (at >= 0) permanent[at] = hired
              else permanent.push(hired)
              return { ...roster, permanent }
            })
          }
        }
        releaseMasterAgentCards()
      }
      if (message.type === 'projectAgentDraftActivated') {
        const activated = message.agent
        if (activated?.id && Array.isArray(ui.state.boot?.projectAgents)) {
          ui.state.boot.projectAgents = [activated, ...ui.state.boot.projectAgents.filter(item => item.id !== activated.id)]
        }
        const orders = Array.isArray(ui.masterData?.workOrders) ? ui.masterData.workOrders : []
        const owner = orders.find(order => {
          const ids = Array.isArray(order.roster?.agentIds) ? order.roster.agentIds : (order.roster?.permanent || []).map(item => item.id)
          return ids.includes(message.agentId)
        })
        const ids = Array.isArray(owner?.roster?.agentIds) ? owner.roster.agentIds : (owner?.roster?.permanent || []).map(item => item.id)
        const next = ids.map(id => (ui.state.boot?.projectAgents || []).find(item => item.id === id)).find(item => item?.status === 'draft')
        if (next) {
          ui.selectedProfileId = next.id
          ui.constructorDraft = newConstructorDraft(next)
          ui.constructorStep = 'review'
          ui.agentConstructorOpen = true
          vscode.postMessage({ type: 'selectTab', tab: 'agents' })
        } else {
          ui.constructorDraft = undefined
          ui.agentConstructorOpen = false
          ui.masterComposeNote = owner ? 'Все агенты состава активированы — квест разблокирован.' : ui.masterComposeNote
        }
        persistDraft()
        render()
      }
      if (message.type === 'projectAgentDraftRejected') {
        const replacementIds = Array.isArray(message.result?.replacementAgentIds) ? message.result.replacementAgentIds : []
        const replacement = replacementIds.map(id => (ui.state.boot?.projectAgents || []).find(item => item.id === id)).find(item => item?.status === 'draft')
        ui.constructorDraft = replacement ? newConstructorDraft(replacement) : undefined
        ui.agentConstructorOpen = Boolean(replacement)
        if (replacement) {
          ui.selectedProfileId = replacement.id
          ui.constructorStep = 'review'
          vscode.postMessage({ type: 'selectTab', tab: 'agents' })
        } else {
          ui.masterComposeNote = 'Черновик отклонён. Если безопасной замены нет, выберите исполнителя вручную.'
          vscode.postMessage({ type: 'loadMaster' })
        }
        persistDraft()
        render()
      }
      if (message.type === 'projectAgentDeleted') {
        // Конструктор закрываем: карточки, которую он правил, больше нет, и
        // оставленный открытым он сохранил бы распущенного персонажа заново.
        ui.createStepError = ''
        ui.blueprintSyncPreview = undefined
        ui.blueprintSyncDirection = ''
        ui.constructorDraft = undefined
        ui.agentConstructorOpen = false
        if (ui.selectedProfileId === message.agentId) ui.selectedProfileId = ''
        persistDraft()
        render()
      }
      if (message.type === 'blueprintSaved') {
        ui.createStepError = ''
        ui.blueprintSyncPreview = undefined
        ui.blueprintSyncDirection = ''
        persistDraft()
        render()
      }
      if (message.type === 'blueprintSyncPreview') {
        ui.blueprintSyncDirection = message.direction || ui.blueprintSyncDirection
        ui.blueprintSyncPreview = message.preview
        render()
      }
      if (message.type === 'questDeleted') {
        // Раскрытой остаётся строка, которой больше нет: без сброса разбор
        // готовности продолжал бы запрашиваться по удалённому идентификатору.
        closeQuestIfOpen(message.questId)
        resetQuestReplansCache()
        ui.transientError = ''
        render()
      }
      if (message.type === 'memoryDeleted') {
        if (ui.memoryEditId === message.memoryId) {
          ui.memoryEditId = ''
          ui.memoryDraft = undefined
        }
        ui.transientError = ''
        render()
      }
    return true
  }
}
