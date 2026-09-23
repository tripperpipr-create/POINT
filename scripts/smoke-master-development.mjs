import assert from 'node:assert/strict'
import { masterDevelopmentHtml } from '../vscode-extension/ui/client/master-development.js'
import { handleMasterClickAction } from '../vscode-extension/ui/client/master-actions.js'
import { createMasterInbox } from '../vscode-extension/ui/client/master-inbox.js'
import { masterTraceAccept, masterTraceHtml } from '../vscode-extension/ui/client/master-live-trace.js'
import { esc } from '../vscode-extension/ui/client/html-escape.js'

const skill = { id:'master-intake',name:'Постановка задания',description:'Назначение',instructions:'<script>не исполнять</script>',configuration:{revision:2} }
const data = { config:{enabled:true},budget:{mainTokens:100000,limitTokens:10000,spentTokens:400,reservedTokens:100},skills:[skill],revisions:[{id:'revision-2',skill,digest:'digest-2',status:'canary'}],history:[{skillId:skill.id,status:'canary',reason:'Три применения',evaluations:[{scenario:'replay-1',passed:true,baselineScore:4,candidateScore:5,baselineTokens:100,candidateTokens:80}]}] }
const ui = {projectKey:'project-a',masterDevelopment:data}
let html = masterDevelopmentHtml(ui,esc)
for (const text of ['Навыки и развитие','Пробное применение','История проверок','replay-1','Откатить','digest-2','10%']) assert.ok(html.includes(text),text)
assert.ok(!html.includes('<script>'))
const posted = []
const ctx = {ui,render(){},vscode:{postMessage(message){posted.push(message)}},target:{dataset:{id:'revision-2'}}}
assert.equal(handleMasterClickAction({...ctx,action:'master-development-rollback'}),true)
assert.deepEqual(posted[0],{type:'rollbackMasterSkill',id:'revision-2',projectKey:'project-a'})
handleMasterClickAction({...ctx,action:'master-development-rollback'})
assert.equal(posted.length,1,'double submission')
const accept = createMasterInbox({ui,render(){}})
accept({type:'masterDevelopment',projectKey:'other-project',development:{config:{enabled:false}}})
assert.equal(ui.masterDevelopment.config.enabled,true,'foreign response changed project')
accept({type:'masterDevelopmentError',projectKey:'project-a',error:'Нет связи'})
assert.equal(ui.masterDevelopmentBusy,false)
assert.ok(masterDevelopmentHtml(ui,esc).includes('Нет связи'))
accept({type:'masterDevelopment',projectKey:'project-a',development:{...data,history:[{status:'rolled_back',skillId:skill.id,reason:'Нарушение контракта'}]}})
assert.ok(masterDevelopmentHtml(ui,esc).includes('Нарушение контракта'))
const turn = {id:'turn-1'}
masterTraceAccept(turn,{type:'skill',text:'Загружен навык',detail:JSON.stringify({revision:2,digest:'digest-2'})})
assert.ok(masterTraceHtml(turn,esc).includes('digest-2'))
console.log(JSON.stringify({masterDevelopment:'ok',history:true,rollback:true,isolation:true,escaping:true,progress:true}))
