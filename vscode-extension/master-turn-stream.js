const { watchMasterWorkOrder, isTransientWorkOrder } = require('./master-work-order-watch')
const terminal = status => ['completed', 'cancelled', 'failed', 'interrupted'].includes(status)

async function followMasterTurn(host, turn) {
  host.masterTurnStreams ||= new Map()
  if (host.masterTurnStreams.has(turn.id)) return host.masterTurnStreams.get(turn.id)
  const promise = (async () => {
    let after = 0
    let failures = 0
    host.post({type:'masterTurn', turn})
    while (true) {
      try {
        const response = await fetch(host.service.apiUrl(`/api/v2/master/turns/${encodeURIComponent(turn.id)}/events?after=${after}`), { headers: host.service.authHeaders({ Accept: 'text/event-stream' }) })
        if (!response.ok) throw new Error(`HTTP ${response.status}`)
        const reader = response.body.getReader(), decoder = new TextDecoder()
        let buffer = ''
        while (true) {
          const {done,value} = await reader.read()
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
            host.post({type:'masterEvent',event})
          }
        }
        turn = await host.service.request(`/api/v2/master/turns/${encodeURIComponent(turn.id)}`)
        if (terminal(turn.status)) break
        failures=0
      } catch(error) {
        failures++
        host.post({type:'masterEvent',event:{turnId:turn.id,conversationId:turn.conversationId,type:'connection',text:'Соединение прервано. Восстанавливаем ответ…'}})
        if (failures>=5) throw error
      }
      await new Promise(resolve=>setTimeout(resolve,Math.min(5000,500*(failures+1))))
    }
    if(turn.workOrderId) {
      const [workOrder,guild]=await Promise.all([
        host.service.request(`/api/v2/work-orders/${encodeURIComponent(turn.workOrderId)}`),
        host.service.request('/api/state/guild'),
      ])
      host.patchBoot(guild);host.postState()
      host.post({type:'masterWorkOrder',turnId:turn.id,conversationId:turn.conversationId,workOrder})
      if(isTransientWorkOrder(workOrder)) void watchMasterWorkOrder(host,turn.workOrderId,turn.conversationId)
    }
    const master=await host.service.request(`/api/master/history?conversationId=${encodeURIComponent(turn.conversationId)}`)
    host.post({type:'master',master,conversationId:turn.conversationId,turnFinished:true,turn})
    const [decisions,runtime]=await Promise.all([host.service.request('/api/decisions'),host.service.request('/api/state/runtime')])
    host.post({type:'decisions',decisions});host.patchBoot(runtime);host.postState()
  })().catch(error=>host.post({type:'masterStreamError',conversationId:turn.conversationId,turnId:turn.id,message:error.message})).finally(()=>host.masterTurnStreams.delete(turn.id))
  host.masterTurnStreams.set(turn.id,promise)
  return promise
}
module.exports={followMasterTurn}
