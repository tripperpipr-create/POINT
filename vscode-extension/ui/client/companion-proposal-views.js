// Предложения Компаньона читают текущие черновики и занятость из main.js.
export function createCompanionProposalViews({ root, esc, getState, live, agentById, hubAgents, agentClass }) {
  function companionActionDecisionPayload(id) {
    const name = root.querySelector(`[data-companion-action-field="name"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
    const description = root.querySelector(`[data-companion-action-field="description"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
    const roleDescription = root.querySelector(`[data-companion-action-field="roleDescription"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
    const mission = root.querySelector(`[data-companion-action-field="mission"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
    const agentIds = [...root.querySelectorAll(`[data-companion-action-field="agentId"][data-id="${CSS.escape(id)}"]:checked`)].map(input => input.value)
    const instructions = root.querySelector(`[data-companion-action-field="instructions"][data-id="${CSS.escape(id)}"]`)?.value?.trim()
    const toolChecks = [...root.querySelectorAll(`[data-companion-action-field="requiredTool"][data-id="${CSS.escape(id)}"]`)]
    const requiredToolsInput = root.querySelector(`[data-companion-action-field="requiredTools"][data-id="${CSS.escape(id)}"]`)
    let requiredTools
    if (toolChecks.length) requiredTools = toolChecks.filter(item => item.checked).map(item => item.value)
    else if (requiredToolsInput) requiredTools = requiredToolsInput.value.split(/[\r\n,]+/).map(value => value.trim()).filter(Boolean)
    return { name, description, roleDescription, mission, agentIds, instructions, requiredTools }
  }
  function companionActionFooter(item, applyLabel) {
    const editing = live.companionActionEditId === item.id
    const applying = live.companionActionApplying.has(item.id)
    const modifying = live.companionActionModifying.has(item.id)
    const busy = applying || modifying
    return `<footer><button type="button" class="primary" data-action="companion-action-apply" data-id="${esc(item.id)}" ${busy ? 'disabled' : ''}>${applying ? 'Создаём…' : esc(applyLabel)}</button><button type="button" class="secondary" data-action="companion-action-modify" data-id="${esc(item.id)}" ${busy ? 'disabled' : ''}>${modifying ? 'Сохраняем…' : editing ? 'Сохранить черновик' : 'Изменить'}</button><button type="button" class="secondary" data-action="companion-action-ignore" data-id="${esc(item.id)}" ${busy ? 'disabled' : ''}>Игнорировать</button></footer>`
  }
  // Карточки предложений компаньона: панель Хаба, его же классы и его регистр.
  //
  // В ленте Мастера создание агента рисуется не отсюда, а своей карточкой
  // (ui/client/master-agent-card.js): там другой регистр, и здешняя разметка
  // стояла в разговоре без меры колонки и без геометрии Чертога. Ветка
  // create_agent осталась ради панели компаньона, где предложение приходит тем
  // же маршрутом, но живёт среди своих.
  function companionActionProposalHtml(item) {
    if (item.kind === 'create_agent') {
      const agent = item.agent || {}
      const editing = live.companionActionEditId === item.id
      const pending = live.companionActionEditDrafts.get(item.id)
      const shownAgent = pending ? {
        ...agent,
        name: pending.name !== undefined ? pending.name : agent.name,
        roleDescription: pending.roleDescription !== undefined ? pending.roleDescription : agent.roleDescription,
        mission: pending.mission !== undefined ? pending.mission : agent.mission,
      } : agent
      const editor = editing ? `<div class="companion-action-editor"><label>Имя агента<input data-companion-action-field="name" data-id="${esc(item.id)}" maxlength="120" value="${esc(shownAgent.name || '')}"></label><label>Роль<textarea data-companion-action-field="roleDescription" data-id="${esc(item.id)}" rows="2" maxlength="1000">${esc(shownAgent.roleDescription || '')}</textarea></label><label>Миссия<textarea data-companion-action-field="mission" data-id="${esc(item.id)}" rows="3" maxlength="4096">${esc(shownAgent.mission || '')}</textarea></label></div>` : ''
      return `<article class="quest-proposal-card companion-action-card"><header><strong>${esc(item.title)}</strong><em>Черновик агента</em></header><p>${esc(item.rationale || '')}</p><div class="companion-flow-preview"><span><small>Шаблон</small><b>${esc(agent.blueprintId || '—')}</b></span><span><small>Модель</small><b>${esc(agent.primaryModel || 'auto')}</b></span><span><small>Инструменты</small><b>${(agent.allowedTools || []).length}</b></span></div><div class="companion-agent-summary"><strong>${esc(agent.roleDescription || 'Роль не указана')}</strong><small>${esc(agent.mission || '')}</small><p>${(agent.allowedTools || []).map(tool => `<code>${esc(tool)}</code>`).join(' ') || 'Без инструментов'}</p></div>${editor}${companionActionFooter(item, 'Создать агента')}</article>`
    }
    if (item.kind === 'create_team') {
      const team = item.team || {}
      const editing = live.companionActionEditId === item.id
      const pending = live.companionActionEditDrafts.get(item.id)
      const shownTeam = pending ? {
        ...team,
        name: pending.name !== undefined ? pending.name : team.name,
        description: pending.description !== undefined ? pending.description : team.description,
        agentIds: Array.isArray(pending.agentIds) ? pending.agentIds : team.agentIds,
      } : team
      const selected = new Set(shownTeam.agentIds || [])
      const members = (shownTeam.agentIds || []).map(id => agentById(id)).filter(Boolean)
      const editor = editing ? `<div class="companion-action-editor"><label>Название отряда<input data-companion-action-field="name" data-id="${esc(item.id)}" maxlength="120" value="${esc(shownTeam.name || '')}"></label><label>Описание<textarea data-companion-action-field="description" data-id="${esc(item.id)}" rows="3" maxlength="4096">${esc(shownTeam.description || '')}</textarea></label><fieldset><legend>Состав</legend><small>Выберите от 1 до 8 исполнителей.</small><div class="check-grid">${hubAgents().map(agent => `<label><input type="checkbox" data-companion-action-field="agentId" data-id="${esc(item.id)}" value="${esc(agent.id)}" ${selected.has(agent.id) ? 'checked' : ''}> ${esc(agent.name)}</label>`).join('')}</div></fieldset></div>` : ''
      return `<article class="quest-proposal-card companion-action-card"><header><strong>${esc(item.title)}</strong><em>Черновик отряда</em></header><p>${esc(item.rationale || '')}</p><div class="companion-team-members">${members.map(agent => `<span><b>${esc(agent.name)}</b><small>${esc(agent.roleDescription || agentClass(agent))}</small></span>`).join('') || '<small>Состав не выбран</small>'}</div>${editor}${companionActionFooter(item, 'Создать отряд')}</article>`
    }
    if (item.kind === 'create_skill') {
      const skill = item.skill || {}
      const editing = live.companionActionEditId === item.id
      const pending = live.companionActionEditDrafts.get(item.id)
      const shownSkill = pending ? {
        ...skill,
        name: pending.name !== undefined ? pending.name : skill.name,
        description: pending.description !== undefined ? pending.description : skill.description,
        instructions: pending.instructions !== undefined ? pending.instructions : skill.instructions,
        requiredTools: Array.isArray(pending.requiredTools) ? pending.requiredTools : skill.requiredTools,
      } : skill
      const requiredTools = Array.isArray(shownSkill.requiredTools) ? shownSkill.requiredTools : []
      const selectedTools = new Set(requiredTools)
      const permissions = Object.entries(skill.permissionDelta || {})
      const catalog = (getState().boot?.toolCatalog || []).slice(0, 24)
      const toolEditor = catalog.length
        ? `<fieldset><legend>Требуемые инструменты</legend><div class="check-grid skill-tool-grid">${catalog.map(tool => `<label><input type="checkbox" data-companion-action-field="requiredTool" data-id="${esc(item.id)}" value="${esc(tool.name)}" ${selectedTools.has(tool.name) ? 'checked' : ''}> ${esc(tool.displayName || tool.name)}</label>`).join('')}</div><small>Skill не расширяет permissions агента — tools должны уже быть в allowlist.</small></fieldset>`
        : `<label>Требуемые инструменты — по одному на строку<textarea data-companion-action-field="requiredTools" data-id="${esc(item.id)}" rows="4">${esc(requiredTools.join('\n'))}</textarea></label>`
      const editor = editing ? `<div class="companion-action-editor"><label>Название Skill<input data-companion-action-field="name" data-id="${esc(item.id)}" maxlength="120" value="${esc(shownSkill.name || '')}"></label><label>Описание<textarea data-companion-action-field="description" data-id="${esc(item.id)}" rows="2" maxlength="4096">${esc(shownSkill.description || '')}</textarea></label><label>Инструкции<textarea data-companion-action-field="instructions" data-id="${esc(item.id)}" rows="7" maxlength="32768">${esc(shownSkill.instructions || '')}</textarea></label>${toolEditor}</div>` : ''
      return `<article class="quest-proposal-card companion-action-card"><header><strong>${esc(item.title)}</strong><em>SKILL DRAFT</em></header><p>${esc(item.rationale || '')}</p><div class="companion-flow-preview"><span><small>TOOLS</small><b>${requiredTools.length}</b></span><span><small>SCRIPTS</small><b>${(skill.scripts || []).length}</b></span><span><small>PERMISSIONS</small><b>${permissions.length ? permissions.length : 'Не расширяет'}</b></span></div><div class="companion-agent-summary"><strong>${esc(skill.description || 'Описание не указано')}</strong><small class="companion-skill-instructions">${esc(skill.instructions || '')}</small><p>${requiredTools.map(tool => `<code>${esc(tool)}</code>`).join(' ') || 'Без обязательных tools'}</p>${permissions.length ? `<ul>${permissions.map(([key, value]) => `<li>${esc(key)} = ${esc(value)}</li>`).join('')}</ul>` : '<p>Скрытых требований доступа нет. Tool grants агента не изменяются.</p>'}<p class="muted">После Apply definition попадёт в каталог и будет подключён к проекту. Чтобы агент использовал Skill — отметьте его в конструкторе.</p></div>${editor}${companionActionFooter(item, 'Создать и подключить Skill')}</article>`
    }
    const flow = item.flow || {}
    const editing = live.companionActionEditId === item.id
    const pending = live.companionActionEditDrafts.get(item.id)
    const shownFlow = pending ? {
      ...flow,
      name: pending.name !== undefined ? pending.name : flow.name,
      description: pending.description !== undefined ? pending.description : flow.description,
    } : flow
    const nodes = Array.isArray(flow.nodes) ? flow.nodes : []
    const edges = Array.isArray(flow.edges) ? flow.edges : []
    const nodePreview = nodes.map(node => {
      const agent = node.agentId ? agentById(node.agentId) : undefined
      return `<li><b>${esc(node.name || node.kind)}</b><small>${esc(node.kind)}${agent ? ` · ${esc(agent.name)}` : ''}</small></li>`
    }).join('')
    const editor = editing ? `<div class="companion-action-editor"><label>Название Flow<input data-companion-action-field="name" data-id="${esc(item.id)}" maxlength="200" value="${esc(shownFlow.name || '')}"></label><label>Описание<textarea data-companion-action-field="description" data-id="${esc(item.id)}" rows="3" maxlength="4096">${esc(shownFlow.description || '')}</textarea></label></div>` : ''
    return `<article class="quest-proposal-card companion-action-card"><header><strong>${esc(item.title)}</strong><em>FLOW DRAFT</em></header><p>${esc(item.rationale || '')}</p><div class="companion-flow-preview"><span><small>Узлы</small><b>${nodes.length}</b></span><span><small>Связи</small><b>${edges.length}</b></span><span><small>Состояние</small><b>${esc(item.status || 'pending')}</b></span></div><ol>${nodePreview}</ol>${editor}${companionActionFooter(item, 'Создать Flow')}</article>`
  }
  return { companionActionDecisionPayload, companionActionProposalHtml }
}
