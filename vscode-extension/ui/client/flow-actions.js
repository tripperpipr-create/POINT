// Клик-ветки flow и legacy-workflow.
//
// Два контура рядом: визуальный flow и сохранённый для
// совместимости редактор workflow. Они вынесены вместе
// намеренно: переключатель `flow-legacy-mode` водит человека между
// ними на одном экране, и развести их по двум модулям значило бы
// разорвать одну работу пополам.
//
// Отрисовка редакторов живёт в `agent-workflow-editors.js`: там разметка,
// здесь нажатия. Граница та же, что у всех `*-actions.js` рядом.

export function handleFlowClickAction({
  action, target, ui, persistDraft, render, vscode,
  captureFlowForm, currentWorkflowForm, newFlowNode, newWorkflow, newWorkflowStep, workflowFromTemplate,
}) {
  if (action === 'flow-legacy-mode') {
    ui.flowLegacyMode = true
    persistDraft()
    render()
    return true
  }
  if (action === 'flow-visual-mode') {
    ui.flowLegacyMode = false
    persistDraft()
    render()
    return true
  }
  if (action === 'start-flow') {
    vscode.postMessage({
      type: 'startFlowRun',
      flowId: target.dataset.flowId || '',
      input: { title: (ui.flowDraft?.name || 'Flow run') },
    })
    return true
  }
  if (action === 'new-flow') {
    ui.flowDraft = { id: '', name: 'Новый флоу', description: '', nodes: [newFlowNode('input'), newFlowNode('agent'), newFlowNode('output')], edges: [] }
    ui.flowDraft.edges = [
      { id: 'edge-0', from: ui.flowDraft.nodes[0].id, to: ui.flowDraft.nodes[1].id },
      { id: 'edge-1', from: ui.flowDraft.nodes[1].id, to: ui.flowDraft.nodes[2].id },
    ]
    ui.selectedFlowId = ''
    ui.selectedFlowNodeId = ui.flowDraft.nodes[1].id
    ui.flowLegacyMode = false
    persistDraft()
    render()
    return true
  }
  if (action === 'add-flow-node') {
    const draft = captureFlowForm()
    const node = newFlowNode('agent')
    node.positionX = 24 + ((draft.nodes?.length || 0) % 3) * 150
    node.positionY = 24 + Math.floor((draft.nodes?.length || 0) / 3) * 96
    draft.nodes = [...(draft.nodes || []), node]
    ui.flowDraft = draft
    ui.selectedFlowNodeId = node.id
    render()
    return true
  }
  if (action === 'add-flow-edge') {
    const draft = captureFlowForm()
    const nodes = draft.nodes || []
    if (nodes.length < 2) return true
    draft.edges = [...(draft.edges || []), { id: `edge-${Date.now()}`, from: nodes[0].id, to: nodes[1].id }]
    ui.flowDraft = draft
    render()
    return true
  }
  if (action === 'select-flow-node') {
    const draft = captureFlowForm()
    ui.flowDraft = draft
    ui.selectedFlowNodeId = target.dataset.nodeId || ''
    render()
    return true
  }
  if (action === 'remove-flow-node') {
    const draft = captureFlowForm()
    const nodeId = target.dataset.nodeId
    draft.nodes = (draft.nodes || []).filter(item => item.id !== nodeId)
    draft.edges = (draft.edges || []).filter(edge => edge.from !== nodeId && edge.to !== nodeId)
    ui.flowDraft = draft
    if (ui.selectedFlowNodeId === nodeId) ui.selectedFlowNodeId = draft.nodes[0]?.id || ''
    render()
    return true
  }
  if (action === 'revert-flow-node') {
    vscode.postMessage({ type: 'revertFlowNode', flowRunId: target.dataset.flowRunId, nodeId: target.dataset.nodeId })
    return true
  }
  if (action === 'resolve-flow-node') {
    vscode.postMessage({
      type: 'resolveFlowNode',
      flowRunId: target.dataset.flowRunId,
      nodeId: target.dataset.nodeId,
      approved: target.dataset.approved === 'true',
    })
    return true
  }
  if (action === 'resolve-flow-merge') {
    vscode.postMessage({
      type: 'resolveFlowMerge',
      flowRunId: target.dataset.flowRunId,
      nodeId: target.dataset.nodeId,
      resolution: { path: target.dataset.path, strategy: 'use_parent', executionId: target.dataset.executionId },
    })
    return true
  }
  if (action === 'new-workflow') { ui.workflowDraft=newWorkflow();ui.selectedWorkflowId='';render(); return true }
  if (action === 'use-workflow-template') { ui.workflowDraft=workflowFromTemplate(target.dataset.template);ui.selectedWorkflowId='';render(); return true }
  if (action === 'duplicate-workflow') {
    const source=currentWorkflowForm()
    if(source){ui.workflowDraft={...source,id:'',name:`${source.name} — копия`,steps:source.steps.map(step=>({...step,id:''})),createdAt:undefined,updatedAt:undefined};ui.selectedWorkflowId='';render()}
    return true
  }
  if (action === 'cancel-workflow-edit') { ui.workflowDraft=undefined;ui.selectedWorkflowId=ui.state.boot?.workflows?.[0]?.id||'';render(); return true }
  if (action === 'add-workflow-step') { const value=currentWorkflowForm();if(value&&value.steps.length<12){value.steps.push(newWorkflowStep(value.steps.length));ui.workflowDraft=value;render()}; return true }
  if (action === 'remove-workflow-step') { const value=currentWorkflowForm();const index=Number(target.dataset.index);if(value&&value.steps.length>1){value.steps.splice(index,1);ui.workflowDraft=value;render()}; return true }
  if (action === 'move-workflow-step') { const value=currentWorkflowForm();const index=Number(target.dataset.index);const next=index+Number(target.dataset.direction);if(value&&next>=0&&next<value.steps.length){[value.steps[index],value.steps[next]]=[value.steps[next],value.steps[index]];ui.workflowDraft=value;render()}; return true }
  if (action === 'delete-workflow') vscode.postMessage({type:'deleteWorkflow',id:target.dataset.id; return true }
  if (action === 'load-workflow-run') vscode.postMessage({type:'loadWorkflowRun',id:target.dataset.id; return true }
  if (action === 'cancel-workflow') vscode.postMessage({type:'cancelWorkflow',id:target.dataset.id; return true }
  return false
}
