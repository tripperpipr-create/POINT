const { watchMasterWorkOrder, isTransientWorkOrder, projectScope } = require('./master-work-order-watch')
const {masterPath,masterScope,followFastRun} = require('./master-scope')
const terminal = status => ['completed', 'cancelled', 'failed', 'interrupted'].includes(status)

async function followMasterTurn(host, turn) {
  host.masterTurnStreams ||= new Map()
  if (host.masterTurnStreams.has(turn.id)) return host.masterTurnStreams.get(turn.id)
  // Поток принадлежит миру, в котором начался. После смены проекта он молчит и
  // выходит: его текст, сбой и карточка иначе рисовались в первом чате нового
  // проекта — у обоих он зовётся `legacy`.
  const workspaceId=String(turn.workspaceId || '')
  const scope = masterScope(host,workspaceId)
  const request=(route,options)=>host.service.request(masterPath(route,workspaceId),options)
  const promise = (async () => {
    let after = 0
    let failures = 0
    scope.post({type:'masterTurn', turn})
    while (scope.current()) {
      try {
        const response = await fetch(host.service.apiUrl(masterPath(`/api/v2/master/turns/${encodeURIComponent(turn.id)}/events?after=${after}`,workspaceId)), { headers: host.service.authHeaders({ Accept: 'text/event-stream' }) })
        if (!response.ok) throw new Error(`HTTP ${response.status}`)
        const reader = response.body.getReader(), decoder = new TextDecoder()
        let buffer = ''
        while (true) {
          const {done,value} = await reader.read()
          if (!scope.current()) { void reader.cancel().catch(() => {}); return }
          if (done) break
          buffer += decoder.decode(value,{stream:true})
          let end
          while ((end=buffer.indexOf('\n\n'))>=0) {
            const frame=buffer.slice(0,end);buffer=buffer.slice(end+2)
            const data=frame.split('\n').find(line=>line.startsWith('data: '))
            if (!data) continue
            const event=JSON.parse(data.slice(6))
            if (event.sequence<=after) continue
            after=event.sequence
            if(event.type==='run_started') void followFastRun(host,event.text,workspaceId,turn.conversationId)
            scope.post({type:'masterEvent',event})
          }
        }
        turn = await request(`/api/v2/master/turns/${encodeURIComponent(turn.id)}`)
        if (terminal(turn.status)) break
        failures=0
      } catch(error) {
        failures++
        scope.post({type:'masterEvent',event:{turnId:turn.id,conversationId:turn.conversationId,type:'connection',text:'Соединение прервано. Восстанавливаем ответ…'}})
        if (failures>=5) throw error
      }
      await new Promise(resolve=>setTimeout(resolve,Math.min(5000,500*(failures+1))))
    }
    if (!scope.current()) return
    if(turn.workOrderId) {
      const [workOrder,guild]=await Promise.all([
        request(`/api/v2/work-orders/${encodeURIComponent(turn.workOrderId)}`),
        request('/api/state/guild'),
      ])
      if (!scope.current()) return
      host.patchBoot(guild);host.postState()
      scope.post({type:'masterWorkOrder',turnId:turn.id,conversationId:turn.conversationId,workOrder})
      if(isTransientWorkOrder(workOrder)) void watchMasterWorkOrder(host,turn.workOrderId,turn.conversationId)
    }
    const master=await request(`/api/master/history?conversationId=${encodeURIComponent(turn.conversationId)}`)
    scope.post({type:'master',master,conversationId:turn.conversationId,turnFinished:true,turn})
    const [decisions,runtime]=await Promise.all([request('/api/decisions'),request('/api/state/runtime')])
    if (!scope.current()) return
    scope.post({type:'decisions',decisions});host.patchBoot(runtime);host.postState()
  })().catch(error=>scope.post({type:'masterStreamError',conversationId:turn.conversationId,turnId:turn.id,message:error.message})).finally(()=>{ if (host.masterTurnStreams.get(turn.id)===promise) host.masterTurnStreams.delete(turn.id) })
  host.masterTurnStreams.set(turn.id,promise)
  return promise
}

// Смена проекта отпускает потоки и наблюдателей прежнего мира: они сами выйдут
// на следующем шаге (projectScope), а их места освобождаются сразу — вернувшись
// в тот мир, человек снова подхватит тот же ход.
function forgetProjectFollowers(host) {
  host.projectEpoch = (host.projectEpoch || 0) + 1
  host.masterTurnStreams?.clear()
  host.masterWorkOrderWatchers?.clear()
}
module.exports={followMasterTurn,forgetProjectFollowers}
