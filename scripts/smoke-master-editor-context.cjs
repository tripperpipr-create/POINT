const fs = require('node:fs')
const vm = require('node:vm')
const assert = require('node:assert/strict')
const posted = []
let body
const editor = {selection:{isEmpty:false,start:{line:4}},document:{uri:{},getText:()=>'func main() {}'}}
const sandbox = {module:{exports:{}},require:name=>{if(name==='./master-context-controller')return {};if(name==='./master-turn-stream')return {followMasterTurn:async()=>{}};if(name==='./master-work-order-watch')return {watchMasterWorkOrder:async()=>{},watchMasterWorkOrders:async()=>{}};assert.equal(name,'vscode');return {window:{activeTextEditor:editor},workspace:{getWorkspaceFolder:()=>({}),asRelativePath:()=> 'main.go'}}},Buffer,AbortController}
vm.runInNewContext(fs.readFileSync('vscode-extension/master-chat-controller.js','utf8'),sandbox)
const host = {post:value=>posted.push(value),credentialForOrchestrator:async()=>'',service:{request:async(path,options)=>{const payload=JSON.parse(options.body);if(path==='/api/v2/sources/preview')return {id:'src-1',kind:payload.kind,label:payload.label,canonicalUrl:'point://text/src-1',digest:'d1',mediaType:'text/plain',content:payload.content};body=payload;return {history:[]}}}}
const contents = []
;(async()=>{
 const handle=sandbox.module.exports.handleMasterMessage
 await handle.call(host,{type:'attachMasterContext',conversationId:'chat-a'})
 assert.equal(posted[0].context.name,'main.go:5')
 assert.equal(posted[0].conversationId,'chat-a')
 await handle.call(host,{type:'masterChat',message:'Объясни код',conversationId:'chat-a',context:posted[0].context})
 assert.equal(body.conversationId,'chat-a')
 assert.equal(body.message,'Объясни код')
 assert.equal(body.sources.length,1)
 assert.equal(body.sources[0].kind,'text')
 assert.equal(body.sources[0].label,'main.go:5')
 assert.equal(body.sources[0].locator,'point://text/src-1')
 editor.document.getText=()=> 'x'.repeat(16001)
 await assert.rejects(handle.call(host,{type:'attachMasterContext'}),/16 КБ/)
 console.log('Master editor context: selection, conversation binding, source snapshot, size limit: PASS')
})().catch(error=>{console.error(error);process.exitCode=1})
