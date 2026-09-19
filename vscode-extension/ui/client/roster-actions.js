// Клик-ветки Гильдии: роли, найм, навыки и свои инструменты.
//
// Сорок три ветки об одном экране. Мастерская роли, карточка найма, редактор
// навыка и конструктор инструмента ведут одну работу — собрать исполнителя —
// и потому вынесены вместе, а не по одному модулю на форму.
//
// Роли, навыки, чертежи и свои инструменты у ядра общие: в `internal/storage`
// их списки идут без `workspaceID`. Правка здесь меняет общую сущность, а не
// копию проекта, — поэтому черновики этих форм и не сбрасываются при смене
// мира.

import { constructorStepForProfileStep, constructorToProjectAgent, newConstructorDraft } from './agent-constructor.js'

export function handleRosterClickAction({
  action, target, ui, root, vscode, render, persistDraft,
  CONSTRUCTOR_STEPS, EMPTY_TASK_REASON, TOOL_PRESETS,
  agentById, hubAgents, hubModeAvailable, requestModelCapabilityProbe,
  cloneCustomTool, composeQuestTask, currentCustomToolForm, currentFormProfile,
  firstUnreadinessStep, newProfile, prepareAgentConstructor, profileReadiness,
  providerCatalog, resetCustomToolPreview, stepValidationIssue,
}) {
  if (action === 'open-roster') vscode.postMessage({type:'openRoster'; return true }
  if (action === 'select-roster-profile') { ui.selectedProfileId=target.dataset.id||''; ui.profileDraft=undefined; persistDraft(); render(); return true }
  if (action === 'edit-roster-profile') {
    const selected=(ui.state.boot?.profiles||[]).find(item=>item.id===ui.selectedProfileId)||(ui.state.boot?.profiles||[])[0]
    if (hubModeAvailable()) {
      const hubSelected = agentById(ui.selectedProfileId) || hubAgents()[0]
      prepareAgentConstructor(hubSelected, constructorStepForProfileStep(firstUnreadinessStep(hubSelected)))
    } else {
      ui.profileEditorOpen=true
      ui.profileDraft=undefined
      ui.profileEditorStep=firstUnreadinessStep(selected)
    }
    render()
    return true
  }
  if (action === 'close-profile-editor') { ui.profileEditorOpen=false; ui.profileDraft=undefined; ui.profileEditorStep='identity'; ui.createStepError=''; ui.hireAfterSave=''; render(); return true }
  if (action === 'profile-step') {
    const profile=currentFormProfile()
    if(profile) ui.profileDraft=profile
    ui.createStepError=''
    ui.profileEditorStep=target.dataset.step||'identity'
    render()
    return true
  }
  if (action === 'advance-profile-step') {
    const profile=currentFormProfile()
    if(profile) ui.profileDraft=profile
    const dir=target.dataset.dir||'next'
    const nextStep=target.dataset.step||'identity'
    if(dir==='next'){
      const issue=stepValidationIssue(ui.profileEditorStep, profile||ui.profileDraft)
      if(issue){ui.createStepError=issue;render();return true}
    }
    ui.createStepError=''
    ui.profileEditorStep=nextStep
    render()
    return true
  }
  if (action === 'skip-class-step') {
    ui.providerProbe=undefined
    ui.createStepError=''
    ui.hirePreviewTemplateId=''
    ui.profileDraft=newProfile({
      name: 'Новый агент',
      roleDescription: '',
      systemPrompt: '',
      goals: [],
      rules: [],
      allowedTools: ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff'],
      maxSteps: 30,
      maxDurationSeconds: 600,
      approvalMode: 'safe',
    })
    ui.selectedProfileId=''
    ui.profileEditorOpen=true
    ui.profileEditorStep='identity'
    render()
    return true
  }
  if (action === 'preview-template') {
    ui.hirePreviewTemplateId=target.dataset.template||''
    render()
    return true
  }
  if (action === 'fix-profile-step') {
    const step=target.dataset.step||'identity'
    if (hubModeAvailable()) {
      const selected = agentById(ui.selectedProfileId) || hubAgents()[0]
      prepareAgentConstructor(selected, constructorStepForProfileStep(step))
    } else {
      ui.profileEditorStep=step
      ui.profileEditorOpen=true
      ui.createStepError=''
    }
    if(ui.state.selectedTab!=='agents') vscode.postMessage({type:'selectTab',tab:'agents'})
    else render()
    return true
  }
  if (action === 'rollback-agent-improvement') {
    vscode.postMessage({ type: 'rollbackAgentImprovement', id: target.dataset.id })
    return true
  }
  if (action === 'promote-agent-improvement') {
    vscode.postMessage({ type: 'promoteAgentImprovement', id: target.dataset.id })
    return true
  }
  if (action === 'improve-agent') {
    const agentId = target.dataset.id || ''
    const step = CONSTRUCTOR_STEPS.some(item => item.id === target.dataset.step) ? target.dataset.step : 'review'
    vscode.postMessage({ type: 'focusHub', tab: 'agents', agentId, constructorStep: step })
    return true
  }
  if (action === 'launch-cursor') {
    const profile=(ui.state.boot?.profiles||[]).find(item=>item.id===ui.selectedProfileId)
    // Тот же ответ, что и у обычного запуска: раньше эта ветка выходила молча,
    // и одна и та же ошибка на одном экране вела себя двумя разными способами.
    if (!ui.taskDraft.trim()) {
      ui.transientError = EMPTY_TASK_REASON
      render()
      return true
    }
    if (ui.state.cursorRuntime?.available && ui.state.cursorRuntime?.authenticated) {
      ui.cursorRunEvents=[]
      vscode.postMessage({type:'startCursorRun',profileId:ui.selectedProfileId,task:composeQuestTask()})
    } else {
      vscode.postMessage({type:'launchCursorAgent',task:composeQuestTask(),model:profile?.model||'auto'})
    }
    return true
  }
  if (action === 'new-profile') {
    ui.providerProbe=undefined; ui.createStepError=''; ui.hireAfterSave=''; ui.selectedProfileId=''
    if (hubModeAvailable()) {
      prepareAgentConstructor({ name: 'Новый агент', allowedTools: ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff'], maxSteps: 30, maxDurationSeconds: 600, approvalMode: 'safe' })
    } else {
      ui.hirePreviewTemplateId=ui.state.boot?.profileTemplates?.[0]?.id||''; ui.profileEditorOpen=true; ui.profileDraft=newProfile({ name: 'Новый агент', roleDescription: '', systemPrompt: '', goals: [], rules: [], allowedTools: ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff'], maxSteps: 30, maxDurationSeconds: 600, approvalMode: 'safe' }); ui.profileEditorStep='class'
    }
    render()
    return true
  }
  if (action === 'setup-provider') {
    const preset=providerCatalog().find(item=>item.id===target.dataset.preset)
    if(preset){ui.profileDraft={...newProfile(),provider:preset.kind,providerPreset:preset.id,baseUrl:preset.baseUrl||'',model:preset.defaultModel||'auto'};ui.selectedProfileId='';ui.profileEditorOpen=true;ui.profileEditorStep='model';vscode.postMessage({type:'selectTab',tab:'agents'})}
    return true
  }
  if (action === 'duplicate-profile') {
    const source=(ui.state.boot?.profiles||[]).find(item=>item.id===ui.selectedProfileId)
    if(source){ui.providerProbe=undefined;ui.profileDraft={...source,id:'',name:`${source.name} — копия`,allowedTools:[...(source.allowedTools||[])],createdAt:undefined,updatedAt:undefined};ui.selectedProfileId='';ui.profileEditorOpen=true;ui.profileEditorStep='identity';render()}
    return true
  }
  if (action === 'use-template') {
    const template=(ui.state.boot?.profileTemplates||[]).find(item=>item.id===target.dataset.template)
    if(template){
      ui.providerProbe=undefined;ui.createStepError='';ui.hirePreviewTemplateId=template.id;ui.selectedProfileId=''
      if (hubModeAvailable()) prepareAgentConstructor({ ...template, id: '', blueprintId: '' }, 'identity')
      else { ui.profileDraft=newProfile(template);ui.profileEditorOpen=true;ui.profileEditorStep='model' }
      render()
    }
    return true
  }
  if (action === 'hire-and-quest') {
    const profile=currentFormProfile()
    if(!profile) return true
    const issue=stepValidationIssue('limits', profile) || (!profileReadiness(profile).ready ? profileReadiness(profile).issues[0] : '')
    if(issue){ui.createStepError=issue;ui.profileDraft=profile;render();return true}
    ui.hireAfterSave='quest'
    ui.profileDraft=profile
    if (hubModeAvailable()) vscode.postMessage({ type: 'saveProjectAgent', agent: constructorToProjectAgent(newConstructorDraft(profile)) })
    else vscode.postMessage({type:'saveProfile',profile})
    return true
  }
  if (action === 'start-roster-quest') {
    const selected=(ui.state.boot?.profiles||[]).find(item=>item.id===ui.selectedProfileId)||(ui.state.boot?.profiles||[])[0]
    const readiness=profileReadiness(selected)
    if(selected && !readiness.ready){ui.profileEditorOpen=true;ui.profileDraft=undefined;ui.profileEditorStep=firstUnreadinessStep(selected);render();return true}
    ui.profileEditorOpen=false; vscode.postMessage({type:'selectTab',tab:'chat'})
    return true
  }
  if (action === 'cancel-profile') { ui.profileDraft=undefined; ui.profileEditorOpen=false; ui.profileEditorStep='identity'; ui.createStepError=''; ui.hireAfterSave=''; ui.selectedProfileId=ui.state.boot?.profiles?.[0]?.id||''; render(); return true }
  if (action === 'delete-profile') vscode.postMessage({type:'deleteProfile',id:target.dataset.id; return true }
  if (action === 'disband-agent') vscode.postMessage({ type: 'deleteProjectAgent', id: target.dataset.id; return true }
  if (action === 'delete-blueprint') vscode.postMessage({ type: 'deleteBlueprint', id: target.dataset.id; return true }
  if (action === 'export-profile') { const profile=currentFormProfile(); if(profile)vscode.postMessage({type:'exportProfile',profile}); return true }
  if (action === 'import-profile') vscode.postMessage({type:'importProfile'; return true }
  if (action === 'probe-provider') {
    const profile=currentFormProfile()
    if(profile){ui.profileDraft=profile;ui.providerProbe={loading:true,models:[]};render();vscode.postMessage({type:'probeProvider',provider:profile.provider,baseUrl:profile.baseUrl,ui.apiKey})}
    return true
  }
  if (action === 'probe-model-capability') {
    const profile = currentFormProfile()
    if (profile) {
      ui.profileDraft = profile
      requestModelCapabilityProbe(profile)
    }
    return true
  }
  if (action === 'tool-preset') {
    const preset=TOOL_PRESETS.find(item=>item.id===target.dataset.preset)
    const values=preset?.tools === null ? (ui.state.boot?.toolCatalog||[]).map(item=>item.name) : (preset?.tools || [])
    for(const input of root.querySelectorAll('input[name="allowed-tool"]'))input.checked=values.includes(input.value)
    const profile=currentFormProfile()
    if(profile){ui.profileDraft=profile;render()}
    return true
  }
  if (action === 'preview-equip-skill') {
    vscode.postMessage({ type: 'previewEquipSkill', skillId: target.dataset.id })
    return true
  }
  if (action === 'confirm-equip-skill') {
    vscode.postMessage({ type: 'equipSkill', skillId: target.dataset.id })
    ui.pendingSkillEquip = undefined
    return true
  }
  if (action === 'cancel-equip-skill') {
    ui.pendingSkillEquip = undefined
    render()
    return true
  }
  if (action === 'skill-edit') {
    ui.skillEditId = target.dataset.id || ''
    ui.skillDraft = (ui.state.boot?.skills || []).find(item => item.id === ui.skillEditId)
    ui.skillFormError = ''
    ui.skillEquipAfterSave = false
    render()
    return true
  }
  if (action === 'skill-cancel-edit') {
    ui.skillEditId = ''
    ui.skillDraft = undefined
    ui.skillFormError = ''
    ui.skillEquipAfterSave = true
    render()
    return true
  }
  if (action === 'new-custom-tool') {
    const source=ui.state.boot?.customToolTemplates?.[0]?.tool||{kind:'process',displayName:'Новый инструмент',description:'Запускает новый инструмент после подтверждения.',program:'',arguments:[],parameters:[],cwd:'.',timeoutSeconds:120}
    resetCustomToolPreview();ui.customToolDraft=cloneCustomTool(source);ui.selectedCustomToolId='';render()
    return true
  }
  if (action === 'duplicate-custom-tool') { const source=currentCustomToolForm();if(source){resetCustomToolPreview();ui.customToolDraft=cloneCustomTool({...source,displayName:`${source.displayName} — копия`});ui.selectedCustomToolId='';render()}; return true }
  if (action === 'use-custom-tool-template') {
    const template=(ui.state.boot?.customToolTemplates||[]).find(item=>item.id===target.dataset.template)
    if(template){resetCustomToolPreview();ui.customToolDraft=cloneCustomTool(template.tool);ui.selectedCustomToolId='';render()}
    return true
  }
  if (action === 'add-tool-parameter') {
    const value=currentCustomToolForm();if(value&&value.parameters.length<16){resetCustomToolPreview();const used=new Set(value.parameters.map(item=>item.name));let index=value.parameters.length+1;while(used.has(`input_${index}`))index++;value.parameters.push({name:`input_${index}`,displayName:`Параметр ${index}`,description:'Опишите допустимое значение для модели',type:'string',required:true,enumValues:[],maxLength:1024});ui.customToolDraft=value;render()}
    return true
  }
  if (action === 'remove-tool-parameter') { const value=currentCustomToolForm();const index=Number(target.dataset.index);if(value){resetCustomToolPreview();value.parameters.splice(index,1);ui.customToolDraft=value;render()}; return true }
  if (action === 'preview-custom-tool') {
    const tool=currentCustomToolForm()
    if(tool){const previewArguments={reason:'Проверка конфигурации в песочнице конструктора'};ui.customToolPreviewArguments={};for(const parameter of tool.parameters||[]){const control=root.querySelector(`[data-preview-param="${parameter.name}"]`);if(!control||control.value==='')continue;const value=parameter.type==='integer'?Number(control.value):control.value;previewArguments[parameter.name]=value;ui.customToolPreviewArguments[parameter.name]=value}ui.customToolDraft=tool;ui.customToolPreview=undefined;ui.customToolPreviewError='';ui.customToolPreviewStatus='loading';render();vscode.postMessage({type:'previewTool',tool,arguments:previewArguments})}
    return true
  }
  if (action === 'cancel-custom-tool') { resetCustomToolPreview();ui.customToolDraft=undefined;ui.selectedCustomToolId=ui.state.boot?.customTools?.[0]?.id||'';render(); return true }
  if (action === 'delete-custom-tool') vscode.postMessage({type:'deleteCustomTool',id:target.dataset.id; return true }
  if (action === 'export-custom-tool') { const tool=currentCustomToolForm();if(tool)vscode.postMessage({type:'exportCustomTool',tool}); return true }
  if (action === 'import-custom-tool') vscode.postMessage({type:'importCustomTool'; return true }
  return false
}
