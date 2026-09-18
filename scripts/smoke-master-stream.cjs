const fs=require('node:fs')
const vm=require('node:vm')
const assert=require('node:assert/strict')
const posted=[],requests=[],attempts=new Map()
const frame=(id,sequence,type,text)=>`id: ${sequence}\nevent: master\ndata: ${JSON.stringify({turnId:id,conversationId:'chat-'+id,sequence,type,text})}\n\n`
const sandbox={module:{exports:{}},TextDecoder,setTimeout:fn=>setImmediate(fn),require:name=>{if(name==='./master-work-order-watch')return {watchMasterWorkOrder:async()=>{},isTransientWorkOrder:()=>false};throw new Error('поток хода Мастера подключил неизвестный модуль: '+name)},fetch:async url=>{
 requests.push(url)
 const id=new URL(url).pathname.split('/')[5]
 const attempt=(attempts.get(id)||0)+1;attempts.set(id,attempt)
 let read=0
 return {ok:true,body:{getReader:()=>({read:async()=>{
  if(attempt===1){if(read++===0)return {value:Buffer.from(frame(id,1,'reply','First fragment')),done:false};throw Error('connection lost')}
  if(read++===0)return {value:Buffer.from(frame(id,1,'reply','First fragment')+frame(id,2,'reply','Complete answer')+frame(id,3,'done','completed')),done:false}
  return {done:true}
 }})}}
}}
vm.runInNewContext(fs.readFileSync('vscode-extension/master-turn-stream.js','utf8'),sandbox)
const host={post:value=>posted.push(value),patchBoot(){},postState(){},service:{baseUrl:'http://localhost:9999',apiToken:'test',apiUrl(route){return this.baseUrl+route},authHeaders(extra={}){return this.apiToken ? {...extra,Authorization:'Bearer '+this.apiToken} : {...extra}},request:async url=>{
 if(url.startsWith('/api/v2/master/turns/')||url.startsWith('/api/master/turns/')){const id=url.split('/').at(-1);return {id,conversationId:'chat-'+id,status:'completed'}}
 if(url.startsWith('/api/master/history'))return {sessions:{active:new URL('http://test'+url).searchParams.get('conversationId')}}
 return {}
}}}
;(async()=>{
 const follow=sandbox.module.exports.followMasterTurn
 const turn=id=>({id,conversationId:'chat-'+id,status:'waiting'})
 await Promise.all([follow(host,turn('a')),follow(host,turn('a')),follow(host,turn('b'))])
 for(const id of ['a','b']){
  assert.equal(attempts.get(id),2)
  assert(requests.some(url=>url.includes('/'+id+'/events?after=1')))
  const events=posted.filter(value=>value.type==='masterEvent'&&value.event.turnId===id&&value.event.sequence)
  assert.deepEqual(events.map(value=>value.event.sequence),[1,2,3])
  assert(events.every(value=>value.event.conversationId==='chat-'+id))
  assert.equal(posted.filter(value=>value.type==='master'&&value.turn?.id===id).length,1)
 }
 assert.equal(host.masterTurnStreams.size,0)
 console.log('Master SSE: reconnect, replay deduplication, two independent conversations: PASS')
})().catch(error=>{console.error(error);process.exitCode=1})
