const { projectScope } = require('./master-work-order-watch')

function masterWorkspaceId(host, explicit) {
  if(explicit)return String(explicit)
  const id=String(host.masterWorkspaceID || host.context?.workspaceState?.get('point.masterWorkspaceId','') || '')
  return id.startsWith('point-chat-') || id===host.boot?.currentWorkspace?.id ? id : String(host.boot?.currentWorkspace?.id || '')
}
function masterPath(route, workspaceId) {
  return workspaceId ? route + (route.includes('?') ? '&' : '?') + 'workspaceId=' + encodeURIComponent(workspaceId) : route
}
async function setMasterScope(host, id) {
  host.masterWorkspaceID = String(id || '')
  await host.context?.workspaceState?.update('point.masterWorkspaceId',host.masterWorkspaceID)
}
function masterScope(host, id) {
  const project = projectScope(host)
  const current = () => project.current() && masterWorkspaceId(host) === String(id || '')
  return {current,post:message=>{if(current())host.post(message)}}
}
async function followFastRun(host, runId, workspaceId, conversationId) {
  const scope = masterScope(host, workspaceId)
  while(scope.current()) {
    try {
      const details = await host.service.request('/api/runs/'+encodeURIComponent(runId))
      if(!scope.current())return
      // Хроника для карточки — без потока токенов: его сотни событий на прогон,
      // а карточке нужны ответы, команды и решения.
      const events=(details.events || []).filter(event => event.type!=='model.streamed')
      scope.post({type:'masterFastRun',run:details.run,details:{...details,events},conversationId})
      if(['completed','failed','cancelled','interrupted'].includes(details.run.status)) {
        const master=await host.service.request(masterPath('/api/master/history?conversationId='+encodeURIComponent(conversationId),workspaceId))
        scope.post({type:'master',master,conversationId,loaded:true,completionRefresh:true})
        return
      }
    }catch(error){scope.post({type:'masterStreamError',conversationId,message:error.message});return}
    await new Promise(resolve=>setTimeout(resolve,1200))
  }
}
module.exports={masterWorkspaceId,masterPath,setMasterScope,masterScope,followFastRun}
