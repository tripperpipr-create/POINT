const fs = require('node:fs')
const vm = require('node:vm')
const assert = require('node:assert/strict')
const posted = []
let body
const editor = {selection:{isEmpty:false,start:{line:4}},document:{uri:{},getText:()=>'func main() {}'}}
const sandbox = {module:{exports:{}},require:name=>{if(name==='path')return require('node:path');if(name==='./master-scope')return require('../vscode-extension/master-scope.js');if(name==='./master-fast-settings')return {};if(name==='./master-context-controller')return {};if(name==='./master-turn-stream')return {followMasterTurn:async()=>{}};if(name==='./master-work-order-watch')return {watchMasterWorkOrder:async()=>{},watchMasterWorkOrders:async()=>{},projectScope:host=>({current:()=>true,post:value=>host.post(value)})};if(name==='./master-chat-branch')return {offerMasterChatBranch:async()=>false};if(name==='child_process')return {spawn:()=>{throw new Error('spawn is not expected in the editor-context smoke')}};assert.equal(name,'vscode');return {window:{activeTextEditor:editor},workspace:{getWorkspaceFolder:()=>({}),asRelativePath:()=> 'main.go'}}},Buffer,AbortController}
vm.runInNewContext(fs.readFileSync('vscode-extension/master-chat-controller.js','utf8'),sandbox)
const host = {post:value=>posted.push(value),boot:{currentWorkspace:{id:'ws-a'}},credentialForOrchestrator:async()=>'',service:{request:async(path,options)=>{if(!options)return {sessions:{items:[]}};const payload=JSON.parse(options.body);if(path.startsWith('/api/v2/sources/preview'))return {id:'src-1',kind:payload.kind,label:payload.label,canonicalUrl:'point://text/src-1',digest:'d1',mediaType:'text/plain',content:payload.content};body=payload;return {history:[]}}}}
const contents = []
;(async()=>{
 const handle=sandbox.module.exports.handleMasterMessage
 await handle.call(host,{type:'attachMasterContext',conversationId:'chat-a'})
 assert.equal(posted[0].context.name,'main.go:5')
 assert.equal(posted[0].conversationId,'chat-a')
 await handle.call(host,{type:'masterChat',message:'Объясни код',conversationId:'chat-a',context:posted[0].context})
 assert.equal(body.conversationId,'chat-a')
 // Мир реплики уходит вместе с ней: ядро другого проекта её отвергнет.
 assert.equal(body.workspaceId,'ws-a')
 assert.equal(body.message,'Объясни код')
 assert.equal(body.sources.length,1)
 assert.equal(body.sources[0].kind,'text')
 assert.equal(body.sources[0].label,'main.go:5')
 assert.equal(body.sources[0].locator,'point://text/src-1')
 editor.document.getText=()=> 'x'.repeat(16001)
 await assert.rejects(handle.call(host,{type:'attachMasterContext'}),/16 КБ/)
 console.log('Master editor context: selection, conversation binding, source snapshot, size limit: PASS')
})().catch(error=>{console.error(error);process.exitCode=1})
