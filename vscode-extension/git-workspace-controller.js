const vscode = require('vscode')
const { createReviewEditors } = require('./git-review-editors')
const writes = new Set(['create','update','comment','reply','resolve','approve','unapprove','merge','retryJob','retryPipeline'])
function createGitWorkspace(provider) {
  let panel
  const request = (route, body, method='POST') => provider.service.request(route, {
    method, timeoutMs: 125000, ...(body === undefined ? {} : {body:JSON.stringify(body)}),
  })
  async function connections() {
    const data = (await request('/api/v2/forge/connections', undefined, 'GET')) || []
    const values = {}
    for (const c of data) {
      const token = await provider.context.secrets.get(c.secretRef)
      if (token) values[c.secretRef] = token
    }
    if (Object.keys(values).length) await request('/api/v2/forge/secrets/unlock', {values})
    return data
  }
  async function forge(input) {
    await connections()
    const result = await request('/api/v2/forge/request', input)
    if (result.state !== 'ok') {
      const e = new Error(result.problem || 'Сервис не ответил')
      e.uncertain = result.uncertain
      throw e
    }
    return result.response
  }
  const editors = createReviewEditors(provider, forge, request)
  function open(screen) {
    if (panel) {
      panel.reveal(vscode.ViewColumn.Active)
      if (screen) void panel.webview.postMessage({type:'gitWorkspaceScreen',screen})
      return
    }
    panel = vscode.window.createWebviewPanel('point.gitWorkspace','Git',vscode.ViewColumn.Active,{
      enableScripts:true,retainContextWhenHidden:true,
      localResourceRoots:[vscode.Uri.joinPath(provider.context.extensionUri,'media')],
    })
    panel.webview.html = provider.html(panel.webview,'tool-git-workspace')
      .replace('data-layout="tool-git-workspace"', 'data-layout="tool-git-workspace" data-git-screen="'+(screen || '')+'"')
    provider.toolWindows.set('git-workspace',panel)
    panel.webview.onDidReceiveMessage(message=>provider.handleMessage(message),undefined,provider.context.subscriptions)
    panel.onDidChangeViewState(()=>void panel?.webview.postMessage({type:'gitWorkspaceVisibility',visible:panel.visible}),undefined,provider.context.subscriptions)
    panel.onDidDispose(()=>{provider.toolWindows.delete('git-workspace');panel=undefined},undefined,provider.context.subscriptions)
  }
  async function handle(message) {
    if (message.op === 'open') return open(message.screen)
    if (!panel) return
    const id = message.id
    try {
      let data
      switch(message.op) {
        case 'inventory':
          data = await request('/api/v2/git/repositories',undefined,'GET');break
        case 'status':
          data = await request('/api/v2/git/status?'+new URLSearchParams(message.target),undefined,'GET');break
        case 'read':
          data = await request('/api/v2/git/read',{...message.target,...message.input});break
        case 'git':
          data = await provider.handleGitAction({...message.input,...message.target})
          await provider.refreshToolWindowSnapshot('git');break
        case 'setup': {
          const name=await vscode.window.showInputBox({title:message.input.action==='clone'?'Clone — каталог внутри проекта':'Init — каталог внутри проекта',value:message.input.action==='clone'?'repo':'.'})
          if(name===undefined)throw new Error('Операция отменена')
          let url=''
          if(message.input.action==='clone'){url=await vscode.window.showInputBox({title:'Git — URL репозитория'});if(!url)throw new Error('Clone отменён')}
          data=await request('/api/v2/git/setup',{workspaceId:message.target.workspaceId,action:message.input.action,name,url});break
        }
        case 'native':
          if(message.input.command!=='git.init') throw new Error('Неизвестная команда');
          await vscode.commands.executeCommand('git.init');data={opened:true};break
        case 'connections':data=await connections();break
        case 'connection':
          data = await saveConnection(message.input);break
        case 'toggleConnection':
          data = await request('/api/v2/forge/connections',{...message.input,enabled:!message.input.enabled},'PUT');break
        case 'bindings':
          data=await request('/api/v2/forge/bindings?'+new URLSearchParams(message.target),undefined,'GET');break
        case 'bind':
          data=await bind(message.target);break
        case 'forge': {
          const input = {...message.input}
          if(writes.has(input.action) && input.action==='merge') {
            const yes=await vscode.window.showWarningMessage('Слить MR !'+input.iid+'?',{
              modal:true,detail:[input.connectionId,input.project,input.expectedSha].join('\n'),
            },'Слить')
            if(!yes) throw new Error('Слияние отменено')
            input.confirmed=true
          }
          data=await forge(input);break
        }
        case 'diff':data=await editors.openDiff(message.input);break
        case 'localDocument':data=await editors.localDocument({...message.target,...message.input});break
        case 'logs':data=await editors.logs(message.input);break
        case 'reviewers': {
          let page=1;const users=[]
          do {const v=await forge({...message.input,action:'users',page});users.push(...(v.data||[]));page=v.nextPage||0}while(page)
          const chosen=await vscode.window.showQuickPick(users.map(u=>({label:u.name,description:'@'+u.username,user:u})),{title:'Ревьюеры MR',canPickMany:true})
          if(!chosen)throw new Error('Выбор отменён')
          data=chosen.map(c=>c.user);break
        }
        case 'suggest': {
          const config=await request('/api/v2/git/assistance?'+new URLSearchParams(message.target),undefined,'GET')
          const apiKey=await provider.credentialFor(config,'Git-помощника')
          data=await request('/api/v2/git/suggest',{...message.input,...message.target,apiKey});break
        }
        case 'assist':
          await vscode.commands.executeCommand('localAgent.askCompanionAbout',{
            message:message.input.prompt+"\nРевизия: "+message.input.revision+"\n"+String(message.input.text||"").slice(0,64000),
            context:{kind:'git',...message.target,revision:message.input.revision,text:message.input.text},
          });data={opened:true};break
        default:throw new Error('Неизвестное действие пространства Git')
      }
      void panel?.webview.postMessage({type:'gitWorkspaceResult',id,ok:true,data})
    } catch(error) {
      void panel?.webview.postMessage({type:'gitWorkspaceResult',id,ok:false,error:String(error.message||error),uncertain:!!error.uncertain})
    }
  }
  async function saveConnection(input={}) {
    const name = await vscode.window.showInputBox({title:'GitLab — подключение',prompt:'Название сервера и аккаунта',value:input.name||''})
    if (!name) throw new Error('Подключение отменено')
    const url = await vscode.window.showInputBox({title:'GitLab — адрес сервера',value:input.url||'https://gitlab.com'})
    if (!url) throw new Error('Подключение отменено')
    const caPath=await vscode.window.showInputBox({title:'GitLab — файл CA (необязательно)',value:input.caPath||''})
    if(caPath===undefined) throw new Error('Подключение отменено')
    const token=await vscode.window.showInputBox({title:'GitLab — токен',password:true,prompt:'Нужен scope api. Пустое поле сохраняет текущий токен.'})
    if(token===undefined) throw new Error('Подключение отменено')
    const value=await request('/api/v2/forge/connections',{...input,provider:'gitlab',name,url,caPath,enabled:true,token},'PUT')
    if(token) await provider.context.secrets.store(value.secretRef,token)
    return value
  }
  async function bind(target) {
    const data=await request('/api/v2/forge/bindings?'+new URLSearchParams(target),undefined,'GET')
    const cs=await connections()
    const options=data.candidates.map(item=>({label:(cs.find(c=>c.id===item.connectionId)?.name||item.connectionId)+' · '+item.remote,
      description:item.project,binding:item}))
    options.push({label:'Задать вручную',manual:true},{label:'Отключить связь',off:true})
    const selected=await vscode.window.showQuickPick(options,{title:'Git — связь с сервисом'})
    if(!selected) throw new Error('Выбор отменён')
    let b=selected.binding
    if(!b) {
      const snap=await request('/api/v2/git/status?'+new URLSearchParams(target),undefined,'GET')
      const remote=await vscode.window.showQuickPick(snap.git.remotes.map(r=>r.name),{title:'Remote'})
      if(!remote) throw new Error('Выбор отменён')
      b={...target,remote,mode:selected.off?'off':'manual'}
      if(!selected.off) {
        const c=await vscode.window.showQuickPick(cs.filter(c=>c.enabled).map(c=>({label:c.name,c})),{title:'GitLab — аккаунт'})
        if(!c) throw new Error('Выбор отменён')
        b.connectionId=c.c.id
        b.project=await vscode.window.showInputBox({title:'Проект GitLab',prompt:'group/project'})
        if(!b.project) throw new Error('Выбор отменён')
      }
    }
    return request('/api/v2/forge/bindings',{...b,mode:selected.off?'off':'manual'},'PUT')
  }
  return {open,handle}
}
module.exports={createGitWorkspace}
