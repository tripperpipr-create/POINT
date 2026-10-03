// Прогон Быстрого агента в разговоре Мастера.
//
// Была одна строка «Fast Agent · running · шаг 16»: английский статус и ни
// слова о том, что агент делает. Хозяин следит за прогоном опросом
// /api/runs/{id} (master-scope.js) и присылает всю хронику — карточка рисует
// её тем же видом, что и запущенный квест: статус, кнопка, решения, хроника.

const ACTIVE = ['pending','running','waiting_approval','paused']

export function acceptMasterFastRun(message,ui,active,render) {
 if(message.type!=='masterFastRun')return false
 if(message.conversationId===active&&message.run?.workspaceId===ui.masterData?.sessions?.workspaceId){
  ui.masterData={...ui.masterData,fastRun:message.run,fastRunDetails:message.details||ui.masterData?.fastRunDetails}
  render()
 }
 return true
}

export function masterFastRunActive(run) {
 return ACTIVE.includes(String(run?.status||''))
}

function taskTitle(task) {
 const line=String(task||'').split('\n').map(item=>item.trim()).find(Boolean)||''
 return line.length>140?line.slice(0,139)+'…':line
}

export function masterFastRunHtml(run,details,deps) {
 if(!run)return ''
 const {esc,icon,statusLabels,agentWorkTranscriptHtml,pendingDecisionsHtml}=deps
 const own=details?.run?.id===run.id?details:null
 const active=masterFastRunActive(run)
 const status=statusLabels?.[run.status]||run.status
 const step=Number(run.step)||0
 const transcript=own
  ? agentWorkTranscriptHtml(own,{limit:80,compact:true,withoutPending:true})
  : '<div class="agent-work-empty"><span>Загружаем хронику работы агента…</span></div>'
 return `<section class="hall-work hall-fast-run" data-run-id="${esc(run.id)}">
  <header class="hall-work-head">
   <div><b>Быстрый агент</b><strong>${esc(taskTitle(run.task))}</strong></div>
   <span class="hall-work-status">${esc(status)}${step?` · ход ${step}`:''}</span>
  </header>
  ${active?`<div class="hall-work-controls"><button type="button" class="hall-btn is-sm" data-action="cancel" data-id="${esc(run.id)}">${icon('stop')}Остановить</button></div>`:''}
  ${own?pendingDecisionsHtml(own):''}
  <details class="hall-work-log"${active?' open':''}>
   <summary>Хроника</summary>
   ${transcript}
  </details>
 </section>`
}
