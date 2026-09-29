// Подпись состояния Хаба: postState отправляет вебвью новое состояние только
// тогда, когда подпись изменилась. Поле, которого в подписи нет, для вкладок
// невидимо — его изменение до них не доедет, пока не сменится что-то другое.
// Поэтому каждое поле, от которого зависит показ, обязано быть здесь.

function cheapStateSignature(message) {
  const boot = message.boot
  const details = message.details
  const workflow = message.workflowDetails
  return JSON.stringify({
    s: message.service,
    t: message.workspaceTrusted,
    w: message.workspace,
    tab: message.selectedTab,
    on: message.onboarding,
    imp: message.agentImprovementFocus,
    idx: boot?.indexStatus,
    ws: boot?.currentWorkspace?.id,
    pr: boot?.profiles?.map(p => [p.id, p.name, p.model]),
    runs: boot?.runs?.map(r => [r.id, r.status, r.updatedAt || r.finishedAt || r.startedAt]),
    wf: boot?.workflows?.map(item => item.id),
    wfr: boot?.workflowRuns?.map(r => [r.id, r.status]),
    tools: boot?.customTools?.map(item => item.id),
    connections: boot?.connections?.map(item => [item.id, item.status, item.lastError, item.updatedAt]),
    servers: boot?.serverProfiles?.map(item => [item.id, item.status, item.lastError, item.lastProbeAt, item.updatedAt]),
    databases: boot?.dbConnections?.map(item => [item.id, item.status, item.lastError, item.lastProbeAt, item.updatedAt]),
    ch: boot?.changes?.length,
    hubAgents: boot?.projectAgents?.map(item => [item.id, item.updatedAt]),
    hubTeams: boot?.teams?.map(item => [item.id, item.updatedAt, item.agentIds?.length]),
    hubExec: boot?.executions?.map(item => [item.id, item.status, item.durationMs]),
    hubSets: boot?.changeSets?.map(item => [item.id, item.status, item.items?.length]),
    companionHistory: boot?.companionMessages?.at?.(-1)?.id,
    companionInterventions: boot?.companionInterventions?.map(item => [item.id, item.level]),
    companionActions: boot?.companionActionProposals?.map(item => [item.id, item.status]),
    // Задание меняется внутри одного предложения: id и статус остаются прежними,
    // а обсуждение превращается в готовое к запуску. По двум полям подпись этого
    // не видела, состояние в вебвью не уезжало — и человек читал «задание готово
    // к запуску» над карточкой, которой в ленте нет, потому что там всё ещё
    // лежит прежняя, обсуждаемая версия. Версия задания растёт при каждом
    // изменении содержания, поэтому её и состояния достаточно.
    questProposals: boot?.questProposals?.map(item => [item.id, item.status, item.brief?.version, item.brief?.state]),
    // Статус квеста — то, по чему вкладки делят работу на идущую и историю.
    // Без него смена «проверяется → готово» не доезжала ни до одной вкладки:
    // настройки проекта показывали завершённый квест активным, пока не
    // сменится что-нибудь другое.
    quests: boot?.quests?.map(item => [item.id, item.status, item.updatedAt]),
    flowRuns: boot?.flowRuns?.map(item => [item.id, item.status]),
    workOrders: boot?.workOrders?.map(item => [item.id, item.runtime?.status, item.runtime?.updatedAt]),
    d: details && {
      id: details.run?.id,
      st: details.run?.status,
      ev: details.events?.length,
      ap: details.approvals?.length,
      pa: details.patches?.length,
    },
    wd: workflow && {
      id: workflow.id,
      st: workflow.status,
      ev: workflow.events?.length,
    },
  })
}

module.exports = { cheapStateSignature }
