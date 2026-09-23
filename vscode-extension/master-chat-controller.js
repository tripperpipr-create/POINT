const { pickMasterContext, previewMasterContext, searchMasterContext, attachMasterContextPath } = require('./master-context-controller')
const { followMasterTurn } = require('./master-turn-stream')
const { watchMasterWorkOrder, watchMasterWorkOrders } = require('./master-work-order-watch')
const vscode = require('vscode')
let lastEditor
function rememberMasterEditor(editor) { if (editor?.document && !editor.document.isClosed) lastEditor = editor }

async function snapshotMasterContexts(host, contexts) {
  const sources=[]
  for(const value of contexts || []) {
    const label=String(value?.name || 'Контекст Мастера').slice(0,300)
    let request
    if(value?.path && (value.kind==='file' || value.kind==='image')) {
      request={kind:'workspace_file',label,path:value.path}
    } else if(value?.kind==='image') {
      request={kind:'image',label,content:String(value.content || ''),mediaType:String(value.mime || '')}
    } else {
      request={kind:'text',label,content:String(value?.content || '')}
    }
    const snapshot=await host.service.request('/api/v2/sources/preview',{method:'POST',body:JSON.stringify(request)})
    sources.push({id:snapshot.id,kind:snapshot.kind,label:snapshot.label,locator:snapshot.canonicalUrl || snapshot.locator || '',digest:snapshot.digest,mediaType:snapshot.mediaType || ''})
  }
  return sources
}

// Master chat transport: credentials and cancellation stay in the extension.
async function handleMasterMessage(message) {
 switch(message.type) {
	case 'loadMasterDevelopment':
	case 'setMasterLearning':
	case 'rollbackMasterSkill': {
	  try {
	    let development
	    if (message.type === 'setMasterLearning') development = await this.service.request('/api/master/learning',{method:'POST',body:JSON.stringify({enabled:message.enabled === true})})
	    else if (message.type === 'rollbackMasterSkill') development = await this.service.request('/api/master/skills/'+encodeURIComponent(String(message.id || ''))+'/rollback',{method:'POST',body:'{}'})
	    else development = await this.service.request('/api/master/skills')
	    this.post({type:'masterDevelopment',development,projectKey:message.projectKey})
	  } catch (error) {
	    this.post({type:'masterDevelopmentError',error:String(error?.message || error),projectKey:message.projectKey})
	  }
	  break
	}
 case 'forkMasterConversation': {
   const master=await this.service.request('/api/master/conversations/'+encodeURIComponent(message.conversationId)+'/fork',{method:'POST',body:JSON.stringify({messageId:message.messageId})})
   this.post({type:'master',master,viewId:message.viewId,sessionChanged:true,draft:message.draft,regenerate:message.regenerate})
   break
 }
 case 'deleteMasterConversation': {
   const answer=await vscode.window.showWarningMessage('Удалить разговор и его историю? Задачи и изменения проекта сохранятся.',{modal:true},'Удалить')
   if(answer!=='Удалить')break
   const master=await this.service.request('/api/master/sessions',{method:'POST',body:JSON.stringify({action:'delete',id:message.conversationId})})
   this.post({type:'master',master,viewId:message.viewId,sessionChanged:true});break
 }
 case 'exportMasterConversation': {
   const result=await this.service.request('/api/master/conversations/'+encodeURIComponent(message.conversationId)+'/export')
   const uri=await vscode.window.showSaveDialog({saveLabel:'Экспортировать разговор',filters:{Markdown:['md']}})
   if(uri)await vscode.workspace.fs.writeFile(uri,Buffer.from(result.markdown,'utf8'));break
 }
 case 'generateReport': {
   const prompt=await vscode.window.showInputBox({title:'Архивариус · новый отчёт',prompt:'Проверьте цель, аудиторию и факты. В модель уйдёт только этот текст.',value:String(message.prompt || '').slice(0,12000),placeHolder:'Например: отчёт для команды о рисках релиза, с итогом и таблицей приоритетов',ignoreFocusOut:true,validateInput:value=>value.trim()?'':'Опишите, какой отчёт нужен'})
   if(!prompt?.trim())break
   const format=await vscode.window.showQuickPick([
     {label:'Markdown (.md)',description:'Удобно читать в репозитории и рецензировать',value:'md'},
     {label:'HTML (.html)',description:'Готовая адаптивная страница для браузера и печати',value:'html'},
     {label:'Excel (.xlsx)',description:'Настоящая книга с переносами, заголовками и таблицами',value:'xlsx'},
   ],{title:'Формат отчёта',placeHolder:'Выберите файл, который соберёт Архивариус',ignoreFocusOut:true})
   if(!format)break
   const apiKey=await this.credentialForOrchestrator()
   await vscode.window.withProgress({location:vscode.ProgressLocation.Notification,title:'Архивариус собирает отчёт…',cancellable:false},async()=>{
     const result=await this.service.request('/api/reports',{method:'POST',body:JSON.stringify({prompt:prompt.trim(),format:format.value,apiKey})})
     const bytes=Buffer.from(String(result.contentBase64 || ''),'base64')
     if(!bytes.length||bytes.length>16*1024*1024)throw new Error('Агент отчётов вернул файл недопустимого размера.')
     const folder=this.workspaceFolder()
     const defaultUri=folder?vscode.Uri.joinPath(folder.uri,String(result.suggestedName || `report.${format.value}`)):undefined
     const filters=format.value==='md'?{Markdown:['md']}:format.value==='html'?{HTML:['html']}:{Excel:['xlsx']}
     const uri=await vscode.window.showSaveDialog({saveLabel:'Сохранить отчёт',defaultUri,filters})
     if(!uri)return
     await vscode.workspace.fs.writeFile(uri,bytes)
     const choice=await vscode.window.showInformationMessage(`Отчёт готов: ${vscode.workspace.asRelativePath(uri,false)}`,'Открыть','Показать в папке')
     if(choice==='Открыть'){
       if(format.value==='md')await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(uri))
       else await vscode.env.openExternal(uri)
     }else if(choice==='Показать в папке')await vscode.commands.executeCommand('revealFileInOS',uri)
   })
   break
 }
 case 'pickMasterModel': {
   const current=await this.service.request('/api/master/history?conversationId='+encodeURIComponent(message.conversationId))
   const conn=(this.boot?.connections || []).find(v=>v.id===current.config?.connectionId)
   const options=(conn?.models || []).map(v=>({label:v.id,description:(v.capabilities || []).join(' · ')}))
   let model
   if(options.length)model=(await vscode.window.showQuickPick(options,{title:'Модель разговора',placeHolder:'Модели текущего подключения'}))?.label
   else model=await vscode.window.showInputBox({title:'Модель текущего подключения',value:current.sessions?.model || current.config?.model || '',prompt:'Название модели из каталога вашего подключения'})
   if(!model?.trim())break
   await this.service.request('/api/master/sessions',{method:'POST',body:JSON.stringify({action:'model',id:message.conversationId,value:model.trim()})})
   const master=await this.service.request('/api/master/history?conversationId='+encodeURIComponent(message.conversationId))
   this.post({type:'master',master,viewId:message.viewId,sessionChanged:true});break
 }

 case 'pickMasterContext': await pickMasterContext(this,message,vscode.window.activeTextEditor || (lastEditor?.document.isClosed ? undefined : lastEditor));break
 case 'previewMasterContext': await previewMasterContext(message.context);break
 case 'searchMasterContext': await searchMasterContext(this,message);break
 case 'attachMasterContextPath': await attachMasterContextPath(this,message);break
 case 'attachMasterContext': {
   const editor = vscode.window.activeTextEditor || (lastEditor?.document.isClosed ? undefined : lastEditor)
   if (!editor) throw new Error('Откройте файл в редакторе и добавьте его повторно.')
   if (!vscode.workspace.getWorkspaceFolder(editor.document.uri)) throw new Error('Открытый файл должен относиться к текущему проекту.')
   const selected = !editor.selection.isEmpty
   const content = selected ? editor.document.getText(editor.selection) : editor.document.getText()
   if (Buffer.byteLength(content, 'utf8') > 16000) throw new Error('Контекст больше 16 КБ. Выделите нужный фрагмент кода.')
   this.post({type:'masterContext',viewId:message.viewId,conversationId:message.conversationId,context:{name:vscode.workspace.asRelativePath(editor.document.uri)+(selected ? ':'+(editor.selection.start.line+1) : ''),content}})
   break
 }

        case 'masterSession': {
          const master = await this.service.request('/api/master/sessions', {method:'POST', body:JSON.stringify({action:message.action,id:message.id || (message.action.startsWith('memory') ? '' : message.conversationId),value:message.value,sourceId:message.sourceId})})
          const selected=['new','temporary'].includes(message.action) ? master : await this.service.request('/api/master/history?conversationId='+encodeURIComponent(message.action==='select'?message.id:message.conversationId || master.sessions.active))
          this.post({type:'master',master:selected,sessionChanged:true,viewId:message.viewId,requestId:message.requestId})
          watchMasterWorkOrders(this,selected)
          break
        }
        case 'loadChatDirectory': {
          const directory=await this.service.request('/api/master/directory')
          this.post({type:'chatDirectory',directory,viewId:message.viewId,requestId:message.requestId})
          break
        }
        // Чат чужого мира открывается в два приёма: сначала переключаем проект,
        // потом выбираем разговор. Путь из вебвью здесь не путь, а ключ поиска
        // по реестру: открываем только то, что реестр уже знает, — ядро отдаёт
        // чужие миры без путей именно ради этого.
        case 'openProjectChat': {
          const known=this.knownProject(message.path)
          if(!known) throw new Error('Проект не найден в списке Point. Откройте папку заново.')
          if(!(await this.confirmLeavingBusyWorld())) break
          this.pendingMasterConversation={path:known,id:String(message.conversationId || ''),create:message.newChat===true}
          await this.switchToProject?.(known)
          break
        }
        case 'loadMaster': {
          // full=1 просит у ядра весь хвост, какой оно хранит: 200 реплик вместо 60.
          // Вебвью его просило, а строка адреса теряла — «Показать раньше»
          // перезапрашивало те же шестьдесят и оставалось на месте.
          //
          // Ожидающий разговор чужого мира выбирается здесь, а не сразу после
          // переключения: там ядро нового мира ещё не поднято, и запрос упал бы
          // гарантированно. К этому месту оно уже отвечает.
          const pending=this.takePendingMasterConversation()
          if(pending) {
            try {
              // Новый чат в чужом мире создаётся здесь же: ядро того мира до этой
              // точки ещё не поднято, и раньше запрос гарантированно бы упал.
              const master=pending.create
                ? await this.service.request('/api/master/sessions',{method:'POST',body:JSON.stringify({action:'new'})})
                : await this.service.request('/api/master/sessions',{method:'POST',body:JSON.stringify({action:'select',id:pending.id})})
              const wanted=pending.create ? String(master?.sessions?.active || '') : pending.id
              const selected=await this.service.request('/api/master/history?conversationId='+encodeURIComponent(wanted))
              this.post({type:'master',master:selected,viewId:message.viewId,requestId:message.requestId,loaded:true,sessionChanged:true})
              for(const turn of selected.activeTurns || []) void followMasterTurn(this,turn)
              watchMasterWorkOrders(this,selected)
              void this.postChatDirectory?.()
              break
            } catch (error) {
              // Разговор мог быть удалён, пока мы переключались. Это не повод
              // оставить человека на пустом экране: отдаём обычную историю мира.
              this.service.hostLog('warn', `[chat] ожидаемый разговор не открылся: ${error?.message || error}`)
            }
          }
          const master=await this.service.request('/api/master/history?conversationId='+encodeURIComponent(message.conversationId || '')+(message.full ? '&full=1' : ''))
          this.post({type:'master',master,viewId:message.viewId,requestId:message.requestId,loaded:true})
          for(const turn of master.activeTurns || []) void followMasterTurn(this,turn)
          watchMasterWorkOrders(this,master)
          void this.postChatDirectory?.()
          break
        }
        case 'masterPage': {
          const page=await this.service.request('/api/master/conversations/'+encodeURIComponent(message.conversationId)+'/messages?before='+(message.before || '')+'&q='+encodeURIComponent(message.query || ''))
          this.post({type:'masterPage',page,conversationId:message.conversationId,viewId:message.viewId,query:message.query || ''})
          break
        }
        case 'masterChat': {
          const apiKey = await this.credentialForOrchestrator()
          const contexts=message.attachments || (message.context ? [message.context] : [])
          const sources=await snapshotMasterContexts(this,contexts)
          const turn = await this.service.request('/api/v2/master/turns',{method:'POST',body:JSON.stringify({message:message.message,conversationId:message.conversationId,turnId:message.turnId,sources,model:message.model,taskIntake:true,proposalId:message.proposalId,previousAnswerRejected:!!message.retry,apiKey})})
          void followMasterTurn(this,turn)
          break
        }
        case 'copyMasterText': {
          const text = String(message.text || '').slice(0, 1024 * 1024)
          if (!text) break
          await vscode.env.clipboard.writeText(text)
          vscode.window.setStatusBarMessage('Скопировано из разговора с Мастером', 1800)
          break
        }
        case 'openMasterMessageDetails': {
		  const item = {...message.item}
		  if (item.turnId) {
		    const turn = await this.service.request('/api/master/turns/'+encodeURIComponent(item.turnId))
		    item.skills = turn.skills || []
		  }
          this.chatDocuments.showMasterMessageDetails(item, message.request)
          break
		}
        case 'masterFeedback': {
          // Оценка Мастера живёт в ядре, а не в состоянии рабочей области, как у
          // компаньона: по ней видно, какие постановки задач человек принимает, а
          // какие переделывает, и переезд IDE не должен стирать этот след.
          const id = String(message.messageId || '')
          if (!id) break
          const value = message.value === 'down' ? 'down' : message.value === 'up' ? 'up' : ''
          await this.service.request(`/api/master/messages/${encodeURIComponent(id)}/feedback`, {
            method: 'POST', body: JSON.stringify({ value }),
          })
          this.post({ type: 'master', master: await this.service.request('/api/master/history?conversationId='+encodeURIComponent(message.conversationId)),viewId:message.viewId,loaded:true })
          break
        }
        case 'stopMasterChat': {
          // Пустое тело обязательно: ядро отклоняет любой не-GET запрос без
          // Content-Type: application/json (middleware.go), а служба ставит этот
          // заголовок только там, где тело есть. Без него остановка хода падала
          // ошибкой «Content-Type must be application/json» вместо отмены.
          if (message.turnId) await this.service.request('/api/v2/master/turns/'+encodeURIComponent(message.turnId)+'/cancel',{method:'POST',body:'{}'})
          break
        }
        case 'approveMasterWorkOrderV2': {
          const id=String(message.workOrderId || '')
		  const reviewed=await this.service.request('/api/v2/work-orders/'+encodeURIComponent(id))
		  const routing=reviewed?.routing || {}
		  const connectionId=routing.mode==='auto' ? routing.routerConnectionId : routing.fixedConnectionId
		  const apiKey=connectionId ? await this.credentialFor({connectionId},'утверждённого маршрута WorkOrder') : await this.credentialForOrchestrator()
          const approval=await this.service.request('/api/v2/work-orders/'+encodeURIComponent(id)+'/approve',{
			method:'POST',body:JSON.stringify({version:Number(message.version),digest:String(message.digest || ''),idempotencyKey:String(message.idempotencyKey || ''),apiKey,rosterConsent:Array.isArray(message.rosterConsent)?message.rosterConsent.map(String):[]})
          })
          this.post({type:'masterWorkOrderApproved',approval,turnId:message.turnId,viewId:message.viewId})
          // Утверждение только начинает запуск: план и первый шаг идут минутами.
          // Дальше карточку ведёт наблюдение, иначе она замрёт на «Проверяем окружение».
          void watchMasterWorkOrder(this,id,message.conversationId)
          const [runtime,guild]=await Promise.all([this.service.request('/api/state/runtime'),this.service.request('/api/state/guild')])
          this.patchBoot(runtime);this.patchBoot(guild);this.postState()
          // Путь v2 не доводил pending-executions до запуска и не звал loadRun:
          // activeRunId оставался пустым, runDelta в вебвью не приходил никогда,
          // и экран выполнения было нечем наполнить.
          try { await this.coordinateActiveFlows() } catch (error) {
            this.service.hostLog('warn', `[chat] запуск Flow утверждённого наряда: ${String(error?.message || error).slice(0, 200)}`)
          }
          break
        }
		case 'reviseMasterWorkOrderV2': {
		  const id=String(message.workOrderId || '')
		  const workOrder=await this.service.request('/api/v2/work-orders/'+encodeURIComponent(id)+'/revise',{
			method:'POST',body:JSON.stringify({expectedVersion:Number(message.expectedVersion),expectedDigest:String(message.expectedDigest || ''),idempotencyKey:String(message.idempotencyKey || ''),workOrder:message.workOrder})
		  })
		  this.post({type:'masterWorkOrderRevised',workOrder,viewId:message.viewId})
		  break
		}
        case 'controlMasterWorkOrderQuestV2': {
          const questId=String(message.questId || '')
          const workOrderId=String(message.workOrderId || '')
          const action=String(message.action || '')
		  let apiKey=''
		  if(action==='resume'){
			const reviewed=await this.service.request('/api/v2/work-orders/'+encodeURIComponent(workOrderId))
			const routing=reviewed?.routing || {}
			const connectionId=routing.mode==='auto' ? routing.routerConnectionId : routing.fixedConnectionId
			apiKey=connectionId ? await this.credentialFor({connectionId},'утверждённого маршрута WorkOrder') : await this.credentialForOrchestrator()
		  }
          const result=await this.service.request('/api/v2/master/quests/'+encodeURIComponent(questId)+'/'+encodeURIComponent(action),{
            method:'POST',body:JSON.stringify({message:String(message.message || ''),apiKey})
          })
          const workOrder=await this.service.request('/api/v2/work-orders/'+encodeURIComponent(workOrderId))
          this.post({type:'masterWorkOrderControlled',result,workOrder,viewId:message.viewId})
          void watchMasterWorkOrder(this,workOrderId,message.conversationId)
          const runtime=await this.service.request('/api/state/runtime')
          this.patchBoot(runtime);this.postState()
          break
        }
        case 'controlMasterApplicationV2': {
          const questId=String(message.questId || '')
          const workOrderId=String(message.workOrderId || '')
          const action=String(message.action || '')
          const result=await this.service.request('/api/v2/master/quests/'+encodeURIComponent(questId)+'/application/'+encodeURIComponent(action),{
            method:'POST',body:JSON.stringify({version:Number(message.version),workOrderDigest:String(message.digest || ''),deliveryReceiptId:String(message.deliveryReceiptId || ''),idempotencyKey:String(message.idempotencyKey || '')})
          })
          const workOrder=await this.service.request('/api/v2/work-orders/'+encodeURIComponent(workOrderId))
          this.post({type:'masterApplicationControlled',result,workOrder,viewId:message.viewId})
          break
        }
 }
}
module.exports = { handleMasterMessage, rememberMasterEditor }
