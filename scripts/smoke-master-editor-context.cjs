const fs = require('node:fs')
const vm = require('node:vm')
const assert = require('node:assert/strict')
const posted = []
let body
const editor = {selection:{isEmpty:false,start:{line:4}},document:{uri:{},getText:()=>'func main() {}'}}
const sandbox = {module:{exports:{}},require:name=>{if(name==='./master-context-controller')return {};if(name==='./master-turn-stream')return {followMasterTurn:async()=>{}};assert.equal(name,'vscode');return {window:{activeTextEditor:editor},workspace:{getWorkspaceFolder:()=>({}),asRelativePath:()=> 'main.go'}}},Buffer,AbortController}
vm.runInNewContext(fs.readFileSync('vscode-extension/master-chat-controller.js','utf8'),sandbox)
const host = {post:value=>posted.push(value),credentialForOrchestrator:async()=>'',service:{request:async(_path,options)=>{body=JSON.parse(options.body);return {history:[]}}}}
;(async()=>{
 const handle=sandbox.module.exports.handleMasterMessage
 await handle.call(host,{type:'attachMasterContext',conversationId:'chat-a'})
 assert.equal(posted[0].context.name,'main.go:5')
 assert.equal(posted[0].conversationId,'chat-a')
 await handle.call(host,{type:'masterChat',message:'Объясни код',conversationId:'chat-a',context:posted[0].context})
 assert.equal(body.conversationId,'chat-a')
 assert.equal(body.message,'Объясни код')
 assert.match(body.attachments[0].content,/func main/)
 editor.document.getText=()=> 'x'.repeat(16001)
 await assert.rejects(handle.call(host,{type:'attachMasterContext'}),/16 КБ/)
 console.log('Master editor context: selection, conversation binding, model payload, size limit: PASS')
})().catch(error=>{console.error(error);process.exitCode=1})
