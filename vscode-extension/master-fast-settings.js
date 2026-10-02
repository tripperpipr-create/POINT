const vscode=require('vscode')

async function editFastAgentSettings(host) {
  const config=await host.service.request('/api/system/fast-agent')
  const section=await vscode.window.showQuickPick([
    {label:'Модель',value:'model'}, {label:'Бюджет',value:'budget'},
    {label:'Обязательные навыки',value:'skills'}, {label:'Инструменты',value:'tools'},
    {label:'Правила и запреты',value:'rules'},
  ],{title:'Общие настройки Fast Agent',placeHolder:'Настройки действуют для всех проектов и чатов POINT'})
  if(!section)return
  if(section.value==='model') {
    const connections=host.boot?.connections || []
    const connection=await vscode.window.showQuickPick(connections.map(c=>({label:c.name || c.id,description:c.defaultModel,id:c.id,model:c.defaultModel})),{title:'Подключение Fast Agent'})
    if(!connection)return
    const model=await vscode.window.showInputBox({title:'Модель Fast Agent',value:connection.model || config.profile.model || '',validateInput:v=>v.trim()?'':'Укажите модель'})
    if(model===undefined)return
    config.profile.connectionId=connection.id;config.profile.model=model.trim()
  }else if(section.value==='budget') {
    const value=await vscode.window.showInputBox({title:'Токены, активные секунды, шаги',value:[config.tokens,config.activeSeconds,config.profile.maxSteps].join(', '),validateInput:v=>/^\s*\d+\s*,\s*\d+\s*,\s*\d+\s*$/.test(v)?'':'Введите три целых числа через запятую'})
    if(value===undefined)return
    ;[config.tokens,config.activeSeconds,config.profile.maxSteps]=value.split(',').map(Number)
  }else if(section.value==='skills') {
    const items=await vscode.window.showQuickPick((host.boot?.skills || []).map(s=>({label:s.name,description:s.description,id:s.id,picked:(config.skillIds || []).includes(s.id)})),{title:'Обязательные навыки Fast Agent',canPickMany:true})
    if(!items)return;config.skillIds=items.map(s=>s.id)
  }else if(section.value==='tools') {
    const names=['project_map','search_code','list_files','read_file','search_text','propose_patch','run_command','git_diff','validate_syntax','read_skill','search_skills']
    const items=await vscode.window.showQuickPick(names.map(n=>({label:n,picked:config.profile.allowedTools.includes(n)})),{title:'Инструменты Fast Agent',canPickMany:true})
    if(!items)return;config.profile.allowedTools=items.map(i=>i.label)
  }else {
    const value=await vscode.window.showInputBox({title:'Политики инструментов (JSON: ALLOW, ASK, DENY)',value:JSON.stringify(config.profile.toolPolicies || {}),validateInput:v=>{try{const p=JSON.parse(v);return p && !Array.isArray(p) && typeof p==='object' && Object.values(p).every(x=>['ALLOW','ASK','DENY'].includes(x))?'':'Ожидается объект политик'}catch{return 'Некорректный JSON'}}})
    if(value===undefined)return;config.profile.toolPolicies=JSON.parse(value)
  }
  const saved=await host.service.request('/api/system/fast-agent',{method:'PUT',body:JSON.stringify(config)})
  host.boot.fastAgent=saved;host.postState()
  await vscode.window.showInformationMessage('Общие настройки Fast Agent сохранены.')
}
module.exports={editFastAgentSettings}
