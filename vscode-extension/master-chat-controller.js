const {masterWorkspaceId,masterPath,setMasterScope,followFastRun} = require('./master-scope')
const {editFastAgentSettings}=require('./master-fast-settings')
const path=require('path')
const { pickMasterContext, previewMasterContext, searchMasterContext, attachMasterContextPath } = require('./master-context-controller')
const { followMasterTurn } = require('./master-turn-stream')
const { watchMasterWorkOrder, watchMasterWorkOrders, askMasterAboutStageFailure, projectScope } = require('./master-work-order-watch')
const { offerMasterChatBranch } = require('./master-chat-branch')
const vscode = require('vscode')
const { spawn } = require('child_process')
let lastEditor
function rememberMasterEditor(editor) { if (editor?.document && !editor.document.isClosed) lastEditor = editor }

// Доставленное приложение открывается тем, чем оно является: веб — в
// браузере, консольное и настольное — командой в терминале, остальное — папкой.
// Адрес пишет договор наряда, поэтому браузер открывается только для адреса на
// этой машине: чужой сайт из договора сам не откроется.
function loopbackUrl(value) {
  try {
    const url = new URL(String(value || ''))
    const host = url.hostname.replace(/^\[|\]$/g, '').toLowerCase()
    return ['http:', 'https:'].includes(url.protocol) && (host === 'localhost' || host.endsWith('.localhost') || host === '::1' || /^127\./.test(host))
  } catch { return false }
}
async function openDeliveredApplication(state, mode) {
  if (state?.launch === 'compose' && loopbackUrl(state.url)) {
    if (mode === 'auto' && !state.ready) return ''
    await vscode.env.openExternal(vscode.Uri.parse(String(state.url)))
    return 'browser'
  }
  if (state?.launch === 'terminal' && state.command && state.target) {
    const terminal = vscode.window.createTerminal({ name: 'Point · приложение', cwd: state.target })
    terminal.show()
    terminal.sendText(String(state.command), true)
    return 'terminal'
  }
  if (mode !== 'auto' && state?.target) {
    await vscode.commands.executeCommand('revealFileInOS', vscode.Uri.file(state.target))
    return 'folder'
  }
  return ''
}

// Итоговый отчёт по квесту — одним нажатием. Прежде кнопка открывала три
// диалога подряд (текст запроса, формат, место сохранения) и ждала модель
// двадцать секунд — дольше отчёт не собирался никогда. Здесь формат HTML, файл
// ложится в .point/reports проекта и сразу открывается в браузере, а карточка
// видит каждую фазу.
// Файл отчёта открывается программой по умолчанию по пути, а не через
// env.openExternal. Тот отдаёт file:-адрес в ShellExecute закодированным, и
// Windows не находит файл с кириллицей в имени («итоговый-отчёт-…html»): Code-OSS
// показывает окно «При открытии внешней программы произошла ошибка», а
// openExternal всё равно сообщает об успехе.
//
// На Windows путь уходит в Start-Process — это тот же ShellExecute, но с путём,
// а не с адресом. Проверено на живом Chrome 27.09.2026, и проверены обе ловушки:
// explorer.exe с таким путём молча ничего не открывает, а запуск с `detached`
// создаёт PowerShell без консоли, и он тоже ничего не открывает. Путь едет в
// -EncodedCommand (UTF-16), чтобы кириллица и кавычки дошли как есть.
function openLocalFile(fsPath) {
  const onWindows = process.platform === 'win32'
  const script = `Start-Process -FilePath '${String(fsPath).replace(/'/g, "''")}'`
  const [command, args] = onWindows
    ? ['powershell.exe', ['-NoProfile', '-NonInteractive', '-EncodedCommand', Buffer.from(script, 'utf16le').toString('base64')]]
    : [process.platform === 'darwin' ? 'open' : 'xdg-open', [fsPath]]
  const child = spawn(command, args, { detached: !onWindows, stdio: 'ignore', windowsHide: true })
  child.on('error', () => {})
  child.unref()
}
function reportFileName(suggested) {
  const base = String(suggested || 'report').split(/[\\/]/).pop().replace(/\.[a-z0-9]+$/i, '').replace(/[^\p{L}\p{N}._-]+/gu, '-').slice(0, 80) || 'report'
  const now = new Date()
  const stamp = [now.getFullYear(), now.getMonth() + 1, now.getDate(), now.getHours(), now.getMinutes()].map((part, index) => String(part).padStart(index ? 2 : 4, '0')).join('')
  return `${stamp}-${base}.html`
}
async function generateQuickReport(host, message) {
  const workOrderId = String(message.workOrderId || '')
  const post = (phase, extra = {}) => host.post({ type: 'masterReportState', workOrderId, phase, ...extra, viewId: message.viewId })
  post('working')
  try {
    const apiKey = await host.credentialForOrchestrator()
    const result = await host.service.request('/api/reports', { method: 'POST', timeoutMs: 10 * 60_000, body: JSON.stringify({ prompt: String(message.prompt || '').slice(0, 12000), format: 'html', apiKey }) })
    const bytes = Buffer.from(String(result.contentBase64 || ''), 'base64')
    if (!bytes.length || bytes.length > 16 * 1024 * 1024) throw new Error('Агент отчётов вернул файл недопустимого размера.')
    const folder = host.workspaceFolder()
    const dir = folder ? vscode.Uri.joinPath(folder.uri, '.point', 'reports') : vscode.Uri.joinPath(host.context.globalStorageUri, 'reports')
    await vscode.workspace.fs.createDirectory(dir)
    const uri = vscode.Uri.joinPath(dir, reportFileName(result.suggestedName))
    await vscode.workspace.fs.writeFile(uri, bytes)
    openLocalFile(uri.fsPath)
    post('ready', { path: folder ? vscode.workspace.asRelativePath(uri, false) : uri.fsPath, uri: uri.toString() })
  } catch (error) {
    host.service?.hostLog?.('error', `[report] ${String(error?.message || error)}`)
    post('failed', { error: String(error?.message || error) })
  }
}

async function snapshotMasterContexts(host, contexts,workspaceId) {
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
    const snapshot=await host.service.request(masterPath('/api/v2/sources/preview',workspaceId),{method:'POST',body:JSON.stringify({...request,workspaceId})})
    sources.push({id:snapshot.id,kind:snapshot.kind,label:snapshot.label,locator:snapshot.canonicalUrl || snapshot.locator || '',digest:snapshot.digest,mediaType:snapshot.mediaType || ''})
  }
  return sources
}

// Master chat transport: credentials and cancellation stay in the extension.
async function handleMasterMessage(message) {
 // Ответ, пришедший после смены проекта, принадлежит прежнему миру: его лента
 // и карточки в окне нового проекта были бы чужими — первые чаты обоих миров
 // называются `legacy`. Эпоху двигает forgetProjectFollowers.
 let scope = projectScope(this)
 let workspaceId=masterWorkspaceId(this,message.workspaceId)
 const request=(route,options)=>this.service.request(masterPath(route,workspaceId),options)
 const post = reply => scope.post(reply)
 switch(message.type) {
 case 'editFastAgentSettings':await editFastAgentSettings(this);break
 case 'openMasterChatFolder': {
   const view=await request('/api/master/files')
   await vscode.commands.executeCommand('revealFileInOS',vscode.Uri.file(view.path));break
 }
 case 'showMasterChatFiles': {
   const view=await request('/api/master/files')
   const flatten=nodes=>(nodes || []).flatMap(n=>n.isDir ? flatten(n.children) : [{label:n.path}])
   const picked=await vscode.window.showQuickPick(flatten(view.files),{title:'Файлы разговора POINT'})
   if(picked){const root=path.resolve(view.path),file=path.resolve(root,picked.label);if(file.startsWith(root+path.sep))await vscode.window.showTextDocument(vscode.Uri.file(file))}
   break
 }
 case 'continueMasterInProject': {
   const choices=(this.boot?.workspaces || []).filter(w=>!String(w.id).startsWith('point-chat-')&&this.knownProject(w.path))
   const picked=await vscode.window.showQuickPick(choices.map(w=>({label:w.name,description:w.path,world:w})),{title:'Продолжить в выбранном проекте'})
   if(!picked)break
   if(!(await this.confirmLeavingBusyWorld()))break
   const source=workspaceId
   await this.switchToProject?.(picked.world.path)
   const master=await this.service.request(masterPath('/api/master/conversations/'+encodeURIComponent(message.conversationId)+'/continue-in-project',source),{method:'POST',body:JSON.stringify({workspaceId:this.boot?.currentWorkspace?.id})})
   await setMasterScope(this,master.sessions.workspaceId);this.post({type:'master',master,loaded:true,sessionChanged:true});break
 }
	case 'loadMasterDevelopment':
	case 'setMasterLearning':
	case 'rollbackMasterSkill': {
	  try {
	    let development
	    if (message.type === 'setMasterLearning') development = await request('/api/master/learning',{method:'POST',body:JSON.stringify({enabled:message.enabled === true})})
	    else if (message.type === 'rollbackMasterSkill') development = await request('/api/master/skills/'+encodeURIComponent(String(message.id || ''))+'/rollback',{method:'POST',body:'{}'})
	    else development = await request('/api/master/skills')
	    post({type:'masterDevelopment',development,projectKey:message.projectKey})
	  } catch (error) {
	    post({type:'masterDevelopmentError',error:String(error?.message || error),projectKey:message.projectKey})
	  }
	  break
	}
 case 'forkMasterConversation': {
   const master=await request('/api/master/conversations/'+encodeURIComponent(message.conversationId)+'/fork',{method:'POST',body:JSON.stringify({messageId:message.messageId})})
   await setMasterScope(this,master.sessions.workspaceId)
   post({type:'master',master,viewId:message.viewId,sessionChanged:true,draft:message.draft,regenerate:message.regenerate})
   break
 }
 case 'deleteMasterConversation': {
   const answer=await vscode.window.showWarningMessage('Удалить разговор и его историю? Задачи и изменения проекта сохранятся.',{modal:true},'Удалить')
   if(answer!=='Удалить')break
   const master=await request('/api/master/sessions',{method:'POST',body:JSON.stringify({action:'delete',id:message.conversationId})})
   await setMasterScope(this,master.sessions.workspaceId)
   post({type:'master',master,viewId:message.viewId,sessionChanged:true});break
 }
 case 'exportMasterConversation': {
   const result=await request('/api/master/conversations/'+encodeURIComponent(message.conversationId)+'/export')
   const uri=await vscode.window.showSaveDialog({saveLabel:'Экспортировать разговор',filters:{Markdown:['md']}})
   if(uri)await vscode.workspace.fs.writeFile(uri,Buffer.from(result.markdown,'utf8'));break
 }
 case 'generateReport': {
   // Повторное открытие готового отчёта: только файл из отчётов Point.
   if(message.openUri){const uri=vscode.Uri.parse(String(message.openUri));if(uri.scheme==='file'&&/[\\/]reports[\\/][^\\/]+\.html$/i.test(uri.fsPath))openLocalFile(uri.fsPath);break}
   if(message.quick){await generateQuickReport(this,message);break}
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
     const result=await request('/api/reports',{method:'POST',body:JSON.stringify({prompt:prompt.trim(),format:format.value,apiKey})})
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
       else openLocalFile(uri.fsPath)
     }else if(choice==='Показать в папке')await vscode.commands.executeCommand('revealFileInOS',uri)
   })
   break
 }
 case 'pickMasterModel': {
   const current=await request('/api/master/history?conversationId='+encodeURIComponent(message.conversationId))
   const conn=(this.boot?.connections || []).find(v=>v.id===current.config?.connectionId)
   const options=(conn?.models || []).map(v=>({label:v.id,description:(v.capabilities || []).join(' · ')}))
   let model
   if(options.length)model=(await vscode.window.showQuickPick(options,{title:'Модель разговора',placeHolder:'Модели текущего подключения'}))?.label
   else model=await vscode.window.showInputBox({title:'Модель текущего подключения',value:current.sessions?.model || current.config?.model || '',prompt:'Название модели из каталога вашего подключения'})
   if(!model?.trim())break
   await request('/api/master/sessions',{method:'POST',body:JSON.stringify({action:'model',id:message.conversationId,value:model.trim()})})
   const master=await request('/api/master/history?conversationId='+encodeURIComponent(message.conversationId))
   post({type:'master',master,viewId:message.viewId,sessionChanged:true});break
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
   post({type:'masterContext',viewId:message.viewId,conversationId:message.conversationId,context:{name:vscode.workspace.asRelativePath(editor.document.uri)+(selected ? ':'+(editor.selection.start.line+1) : ''),content}})
   break
 }

        case 'masterSession': {
          const master = await request('/api/master/sessions', {method:'POST', body:JSON.stringify({action:message.action,id:message.id || (message.action.startsWith('memory') ? '' : message.conversationId),value:message.value,sourceId:message.sourceId,workspaceId,scopeKind:message.scopeKind})})
          workspaceId=String(master.sessions?.workspaceId || workspaceId);await setMasterScope(this,workspaceId)
          const selected=['new','temporary'].includes(message.action) ? master : await request('/api/master/history?conversationId='+encodeURIComponent(message.action==='select'?message.id:message.conversationId || master.sessions.active))
          post({type:'master',master:selected,sessionChanged:true,viewId:message.viewId,requestId:message.requestId})
          watchMasterWorkOrders(this,selected)
          break
        }
        case 'loadChatDirectory': {
          const directory=await request('/api/master/directory')
          post({type:'chatDirectory',directory,viewId:message.viewId,requestId:message.requestId})
          break
        }
        // Чат чужого мира открывается в два приёма: сначала переключаем проект,
        // потом выбираем разговор. Путь из вебвью здесь не путь, а ключ поиска
        // по реестру: открываем только то, что реестр уже знает, — ядро отдаёт
        // чужие миры без путей именно ради этого.
        case 'openProjectChat': {
          if(String(message.workspaceId || '').startsWith('point-chat-')) {
            workspaceId=message.workspaceId;await setMasterScope(this,workspaceId)
            const master=await request('/api/master/history?conversationId='+encodeURIComponent(message.conversationId || ''))
            post({type:'master',master,sessionChanged:true,loaded:true,viewId:message.viewId});break
          }
          workspaceId='';await setMasterScope(this,'')
          const known=this.knownProject(message.path)
          if(!known) throw new Error('Проект не найден в списке Point. Откройте папку заново.')
          if(!(await this.confirmLeavingBusyWorld())) break
          // Переключились ради настроек проекта — открывается вкладка, а не чат:
          // ожидающего разговора нет, и после смены мира выбирается она.
          const tab=['overview'].includes(String(message.tab || '')) ? String(message.tab) : ''
          if(!tab) this.pendingMasterConversation={path:known,id:String(message.conversationId || ''),create:message.newChat===true}
          await this.switchToProject?.(known)
          if(tab){this.focusTab(tab);this.postState(true)}
          break
        }
        case 'loadMaster': {
          const requestedProject = this.workspaceFolder()?.uri?.fsPath || ''
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
                ? await request('/api/master/sessions',{method:'POST',body:JSON.stringify({action:'new'})})
                : await request('/api/master/sessions',{method:'POST',body:JSON.stringify({action:'select',id:pending.id})})
              const wanted=pending.create ? String(master?.sessions?.active || '') : pending.id
              const selected=await request('/api/master/history?conversationId='+encodeURIComponent(wanted))
              if ((this.workspaceFolder()?.uri?.fsPath || '') !== requestedProject) break
              post({type:'master',master:selected,viewId:message.viewId,requestId:message.requestId,loaded:true,sessionChanged:true})
              await setMasterScope(this,selected.sessions.workspaceId)
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
          const master=await request('/api/master/history?conversationId='+encodeURIComponent(message.conversationId || '')+(message.full ? '&full=1' : ''))
          if ((this.workspaceFolder()?.uri?.fsPath || '') !== requestedProject) break
          workspaceId=String(master.sessions?.workspaceId || workspaceId);await setMasterScope(this,workspaceId)
          post({type:'master',master,viewId:message.viewId,requestId:message.requestId,loaded:true})
          if(master.fastRun && ['pending','running','waiting','paused'].includes(master.fastRun.status))void followFastRun(this,master.fastRun.id,master.sessions.workspaceId,master.sessions.active)
          for(const turn of master.activeTurns || []) void followMasterTurn(this,turn)
          watchMasterWorkOrders(this,master)
          void this.postChatDirectory?.()
          break
        }
        case 'masterPage': {
          const page=await request('/api/master/conversations/'+encodeURIComponent(message.conversationId)+'/messages?before='+(message.before || '')+'&q='+encodeURIComponent(message.query || ''))
          post({type:'masterPage',page,conversationId:message.conversationId,viewId:message.viewId,query:message.query || ''})
          break
        }
        case 'masterChat': {
          const before=await request('/api/master/history?conversationId='+encodeURIComponent(String(message.conversationId || '')))
          const chat=before?.sessions?.items?.find(item=>item.id===message.conversationId)
          // Принятая ветка переносит беседу в свою рабочую копию и открывает её:
          // эта смена мира — часть отправки, и сообщение уходит в новый мир.
          if(chat?.branchOffer==='pending' && chat?.workMode==='plan' && await offerMasterChatBranch(this,{...chat,title:message.message || chat.title})) scope = projectScope(this)
          workspaceId=String(before?.sessions?.workspaceId || workspaceId || this.boot?.currentWorkspace?.id || '');await setMasterScope(this,workspaceId)
          const apiKey = await this.credentialForOrchestrator()
          const contexts=message.attachments || (message.context ? [message.context] : [])
          const sources=await snapshotMasterContexts(this,contexts,workspaceId)
          const fastConfig=await this.service.request('/api/system/fast-agent')
          const fastApiKey=(chat?.workMode || 'auto')==='auto' && fastConfig?.profile?.connectionId ? await this.credentialFor(fastConfig.profile,'Fast Agent') : ''
          // Пока спрашивали ключ и снимали вложения, человек мог открыть другой
          // проект. Реплика принадлежит прежнему: в новом она ушла бы в его
          // `legacy`. Ядро отвергает и чужой workspaceId.
          if (!scope.current()) break
          const turn = await request('/api/v2/master/turns',{method:'POST',body:JSON.stringify({message:message.message,conversationId:message.conversationId,turnId:message.turnId,sources,model:message.model,taskIntake:true,proposalId:message.proposalId,previousAnswerRejected:!!message.retry,apiKey,fastApiKey,workspaceId})})
          void followMasterTurn(this,turn)
          break
        }
        case 'offerMasterChatBranch': {
          const before=await request('/api/master/history?conversationId='+encodeURIComponent(String(message.conversationId || '')))
          const chat=before?.sessions?.items?.find(item=>item.id===message.conversationId)
          if(chat && chat.branchOffer!=='bound') await offerMasterChatBranch(this,chat,{manual:true})
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
		    const turn = await request('/api/master/turns/'+encodeURIComponent(item.turnId))
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
          await request(`/api/master/messages/${encodeURIComponent(id)}/feedback`, {
            method: 'POST', body: JSON.stringify({ value }),
          })
          post({ type: 'master', master: await request('/api/master/history?conversationId='+encodeURIComponent(message.conversationId)),viewId:message.viewId,loaded:true })
          break
        }
        case 'stopMasterChat': {
          // Пустое тело обязательно: ядро отклоняет любой не-GET запрос без
          // Content-Type: application/json (middleware.go), а служба ставит этот
          // заголовок только там, где тело есть. Без него остановка хода падала
          // ошибкой «Content-Type must be application/json» вместо отмены.
          if (message.turnId) await request('/api/v2/master/turns/'+encodeURIComponent(message.turnId)+'/cancel',{method:'POST',body:'{}'})
          break
        }
        case 'approveMasterWorkOrderV2': {
          const id=String(message.workOrderId || '')
		  const reviewed=await request('/api/v2/work-orders/'+encodeURIComponent(id))
		  const routing=reviewed?.routing || {}
		  const connectionId=routing.mode==='auto' ? routing.routerConnectionId : routing.fixedConnectionId
		  const apiKey=connectionId ? await this.credentialFor({connectionId},'утверждённого маршрута WorkOrder') : await this.credentialForOrchestrator()
          const approval=await request('/api/v2/work-orders/'+encodeURIComponent(id)+'/approve',{
			method:'POST',body:JSON.stringify({version:Number(message.version),digest:String(message.digest || ''),idempotencyKey:String(message.idempotencyKey || ''),apiKey,rosterConsent:Array.isArray(message.rosterConsent)?message.rosterConsent.map(String):[]})
          })
          post({type:'masterWorkOrderApproved',approval,turnId:message.turnId,viewId:message.viewId})
          // Утверждение только начинает запуск: план и первый шаг идут минутами.
          // Дальше карточку ведёт наблюдение, иначе она замрёт на «Проверяем окружение».
          void watchMasterWorkOrder(this,id,message.conversationId)
          const [runtime,guild]=await Promise.all([request('/api/state/runtime'),request('/api/state/guild')])
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
		  const workOrder=await request('/api/v2/work-orders/'+encodeURIComponent(id)+'/revise',{
			method:'POST',body:JSON.stringify({expectedVersion:Number(message.expectedVersion),expectedDigest:String(message.expectedDigest || ''),idempotencyKey:String(message.idempotencyKey || ''),workOrder:message.workOrder})
		  })
		  post({type:'masterWorkOrderRevised',workOrder,viewId:message.viewId})
		  break
		}
        case 'hireMasterWorkOrderAgentV2': {
          const id=String(message.workOrderId || '')
          const result=await request('/api/v2/work-orders/'+encodeURIComponent(id)+'/hire-agent',{
            method:'POST',body:JSON.stringify({draftId:message.draftId,expectedVersion:message.expectedVersion,expectedDigest:message.expectedDigest,idempotencyKey:message.idempotencyKey,agentId:message.agentId,agent:message.agent})
          })
          this.upsertBootItem('projectAgents',result.agent)
          post({type:'masterAgentHired',result,workOrderId:id,draftId:message.draftId,viewId:message.viewId})
          this.postState()
          break
        }
        case 'reviewMasterManualCriterionV2': {
          const workOrderId=String(message.workOrderId || '')
          await request('/api/v2/master/quests/'+encodeURIComponent(String(message.questId || ''))+'/criteria/'+encodeURIComponent(String(message.criterionId || ''))+'/review',{
            method:'POST',body:JSON.stringify({decision:String(message.decision || ''),note:String(message.note || '')})
          })
          const workOrder=await request('/api/v2/work-orders/'+encodeURIComponent(workOrderId))
          post({type:'masterWorkOrderControlled',workOrder,viewId:message.viewId})
          break
        }
        case 'analyzeStageFailureWithMaster': {
          // Ручной разбор провала: та же реплика, что шлёт наблюдатель сам.
          const workOrder=await request('/api/v2/work-orders/'+encodeURIComponent(String(message.workOrderId || '')))
          const asked=await askMasterAboutStageFailure(this,workOrder,message.conversationId,{manual:true})
          if(!asked) post({type:'masterBranchNotice',tone:'error',message:'Мастер не взялся за разбор: нет провала этапа или разговора наряда',viewId:message.viewId})
          break
        }
        case 'controlMasterWorkOrderQuestV2': {
          const questId=String(message.questId || '')
          const workOrderId=String(message.workOrderId || '')
          const action=String(message.action || '')
		  let apiKey=''
		  if(action==='resume'||action==='retry'){
			const reviewed=await request('/api/v2/work-orders/'+encodeURIComponent(workOrderId))
			const routing=reviewed?.routing || {}
			const connectionId=routing.mode==='auto' ? routing.routerConnectionId : routing.fixedConnectionId
			apiKey=connectionId ? await this.credentialFor({connectionId},'утверждённого маршрута WorkOrder') : await this.credentialForOrchestrator()
		  }
          // Повтор этапа может нести среду из списка или предложение Мастера,
          // которое человек разрешил; ядро перепроверяет и то и другое.
          const retry=action==='retry' ? {proposalDigest:String(message.proposalDigest || '')} : {}
          const result=await request('/api/v2/master/quests/'+encodeURIComponent(questId)+'/'+encodeURIComponent(action),{
            method:'POST',body:JSON.stringify({message:String(message.message || ''),apiKey,...retry})
          })
          const workOrder=await request('/api/v2/work-orders/'+encodeURIComponent(workOrderId))
          post({type:'masterWorkOrderControlled',result,workOrder,viewId:message.viewId})
          void watchMasterWorkOrder(this,workOrderId,message.conversationId)
          const runtime=await request('/api/state/runtime')
          this.patchBoot(runtime);this.postState()
          break
        }
        case 'controlMasterApplicationV2': {
          const questId=String(message.questId || '')
          const workOrderId=String(message.workOrderId || '')
          const action=String(message.action || '')
          const route='/api/v2/master/quests/'+encodeURIComponent(questId)+'/application'
          const postState=(state,extra={})=>post({type:'masterApplicationState',questId,workOrderId,state,...extra,viewId:message.viewId})
          // Состояние, открытие и терминал ядро не меняют: это чтение и
          // действие в самой IDE по тому, что ядро знает о приложении.
          if(action==='status'){postState(await request(route+'?probe=1'),{final:true});break}
          if(action==='open'||action==='terminal'){const state=await request(route);postState(state,{opened:await openDeliveredApplication(state,action),final:true});break}
          // Запуск идёт минутами — сборка образов, старт контейнеров, ожидание
          // ответа по адресу. Пока ядро держит запрос, карточка раз в 0,7 с
          // получает его живой вывод; двадцать секунд умолчания здесь мало.
          let polling=true
          const poll=(async()=>{while(polling){await new Promise(resolve=>setTimeout(resolve,700));if(!polling)break;try{postState(await request(route,{timeoutMs:5000}))}catch{}}})()
          let result
          try{
            result=await request(route+'/'+encodeURIComponent(action),{
              method:'POST',timeoutMs:20*60_000,body:JSON.stringify({version:Number(message.version),workOrderDigest:String(message.digest || ''),deliveryReceiptId:String(message.deliveryReceiptId || ''),idempotencyKey:String(message.idempotencyKey || '')})
            })
          }catch(error){
            polling=false;await poll
            try{postState(await request(route),{error:String(error?.message || error),final:true})}catch{post({type:'masterApplicationState',questId,workOrderId,error:String(error?.message || error),final:true,viewId:message.viewId})}
            throw error
          }
          polling=false;await poll
          const state=await request(route+'?probe=1')
          const opened=action==='start'&&result?.status==='running'?await openDeliveredApplication(state,'auto'):''
          postState(state,{opened,final:true})
          const workOrder=await request('/api/v2/work-orders/'+encodeURIComponent(workOrderId))
          post({type:'masterApplicationControlled',result,workOrder,viewId:message.viewId})
          break
        }
 }
}
module.exports = { handleMasterMessage, rememberMasterEditor }
