export function acceptMasterFastRun(message,ui,active,render) {
 if(message.type!=='masterFastRun')return false
 if(message.conversationId===active&&message.run?.workspaceId===ui.masterData?.sessions?.workspaceId){
  ui.masterData={...ui.masterData,fastRun:message.run}
  render()
 }
 return true
}

export function masterFastRunHtml(run,esc,icon) {
 if(!run)return ''
 const active=['pending','running','waiting','paused'].includes(run.status)
 return `<div class="master-notice"><span>Fast Agent · ${esc(run.status)} · шаг ${Number(run.step)||0}</span>${active?`<button type="button" class="btn secondary" data-action="cancel" data-id="${esc(run.id)}">${icon('stop')}Остановить</button>`:''}</div>`
}
