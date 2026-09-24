import { formatDateTime } from './format-units.js'
// Ярлык группы доступа. Цепочка тернарников знала три группы из девяти, и всё
// остальное — docker, ssh, базы, а теперь и git — подписывалось как «ЗАПУСК»,
// хотя git-инструменты только читают.
const TOOL_GROUP_LABELS = {
  read: 'Чтение', index: 'Индекс', git: 'GIT', skill: 'Навык', write: 'Запись',
  execute: 'Запуск', docker: 'DOCKER', network: 'Сеть', database: 'БД',
}

export function createAgentWorkflowEditors(dependencies) {
  const {
    TOOL_PRESETS,
    activeToolPresetId,
    agentById,
    agentCapabilityHtml,
    agentCharacterCard,
    agentClass,
    countOf,
    esc,
    flowStages,
    hireLiveChecklist,
    hireSummaryPanel,
    hubAgents,
    isWide,
    lines,
    modelCapabilityProbeHtml,
    modelChoiceHtml,
    profileReadiness,
    profileStepFooter,
    profileStepNav,
    providerCatalog,
    providerPreset,
    readinessBanner,
    requiresApiKey,
    root,
    shell,
    status,
    statusLabels,
    templatePickerHtml,
    toolProvidesVerification,
    ui,
  } = dependencies

  function profileEditor() {
    const profiles = ui.state.boot?.profiles || []
    const templates = ui.state.boot?.profileTemplates || []
    const tools = ui.state.boot?.toolCatalog || []
    if (!ui.selectedProfileId && !ui.profileDraft && profiles[0]) ui.selectedProfileId = profiles[0].id
    const stored = profiles.find(item=>item.id===ui.selectedProfileId) || profiles[0]
    const profile = ui.profileDraft || stored
    if (!profile) return shell('<main class="offline"><p>Профили ещё не загружены.</p></main>')
    const creating = !profile.id
    const stages = flowStages(creating)
    const step = stages.some(item => item.id === ui.profileEditorStep) ? ui.profileEditorStep : (creating ? 'class' : 'identity')
    const enabled = new Set(profile.allowedTools || [])
    const providers = providerCatalog()
    const preset = providerPreset(profile) || providers[0] || {id:'custom',kind:'openai-compatible',name:'Свой endpoint'}
    const readiness = profileReadiness(profile)
    const activePreset = activeToolPresetId(profile)
    const capabilityReport = modelCapabilityProbeHtml(ui.modelCapabilityProbe)
    const classStep = `<section class="profile-config-block profile-step create-class-step" data-step-panel="class">${templatePickerHtml(templates, { interactive: true })}</section>`
    const identityStep = `<section class="profile-config-block profile-step" data-step-panel="identity"><header><span>01</span><div><strong>Личность агента</strong><small>Имя, роль и правила — с чем агент начнёт работу</small></div></header>
        <p class="create-step-why">Имя шаблона видно в списке; созданный агент получит его как стартовое. Промпт задаёт стиль рассуждения, правила — жёсткие ограничения на каждый квест.</p>
        <label>Агент<select id="settings-profile" ${creating?'disabled':''}>${creating?'<option>Новый шаблон</option>':''}${profiles.map(item=>`<option value="${esc(item.id)}" ${item.id===profile.id?'selected':''}>${esc(item.name)} · ${esc(agentClass(item))}</option>`).join('')}</select></label>
        <label>Имя<input id="profile-name" maxlength="200" value="${esc(profile.name)}" placeholder="Например, SAGE-7" required></label>
        <label>Роль и особенности<textarea id="role-description" rows="3" maxlength="4096" placeholder="В чём агент эксперт и какой результат от него нужен">${esc(profile.roleDescription)}</textarea></label>
        <label>Цели агента<textarea id="profile-goals" rows="4" maxlength="16384" placeholder="Одна постоянная цель на строку">${esc((profile.goals||[]).join('\n'))}</textarea></label>
        <label>Системный промпт<textarea id="system-prompt" rows="7" maxlength="65536" placeholder="Роль, стиль рассуждения, формат результата">${esc(profile.systemPrompt)}</textarea></label>
        <label>Обязательные правила<textarea id="profile-rules" rows="5" maxlength="32768" placeholder="Одно правило на строку. Например: не менять миграции без отдельного подтверждения">${esc((profile.rules||[]).join('\n'))}</textarea></label>
      </section>`
    // Один и тот же выбор «подключение → модель», что и на других экранах.
    // Раньше здесь была своя сетка провайдеров, своя проверка связи и свои поля
    // адреса и ключа — четвёртая версия одного экрана, расходившаяся с прочими.
    // Адрес и ключ теперь принадлежат подключению; здесь остаётся то, что
    // действительно относится к агенту, плюс проверка пригодности модели.
    const modelStep = `<section class="profile-config-block profile-step" data-step-panel="model"><header><span>02</span><div><strong>Модель и подключение</strong><small>Выберите подключение и модель — адрес и ключ придут из подключения</small></div></header>
        <p class="create-step-why">Подключения заводятся один раз в разделе «Связи». Здесь агент только выбирает, каким из них ходить и какой моделью.</p>
        ${modelChoiceHtml({
              connectionId: profile.connectionId || '',
              model: profile.model,
              contextWindowTokens: profile.contextWindowTokens,
              temperature: profile.temperature,
              maxOutputTokens: profile.maxOutputTokens,
              reasoningEffort: profile.reasoningEffort,
            })}
            <div class="provider-probe"><button type="button" class="secondary" data-action="probe-model-capability" ${ui.modelCapabilityProbe?.loading || !profile.model ? 'disabled' : ''}>${ui.modelCapabilityProbe?.loading ? 'Проверяем роль…' : ui.modelCapabilityProbe ? 'Повторить проверку модели' : 'Проверить пригодность модели'}</button><button type="button" class="secondary" data-action="tab" data-tab="connections" title="Открыть раздел «Связи»">Управлять подключениями →</button></div>
            ${capabilityReport}
      </section>`
    const toolsStep = `<section class="tool-section profile-config-block profile-step" data-step-panel="tools"><header><span>03</span><div><strong>Инструменты и разрешения</strong><small>Пресет — быстрый старт; бейдж «доказательство» помечает верификатор</small></div><b>${enabled.size} / ${tools.length}</b></header>
        <p class="create-step-why">Если разрешены правки файлов, включите инструмент с доказательством — иначе квест с критериями готовности не стартует.</p>
        <div class="permission-presets">${TOOL_PRESETS.map(item=>`<button type="button" class="${activePreset===item.id?'on':''}" data-action="tool-preset" data-preset="${esc(item.id)}"><b>${esc(item.label)}</b><small>${esc(item.hint)}</small></button>`).join('')}</div>
        <div class="permission-legend"><span><i class="safe"></i>Чтение и локальный индекс — автоматически</span><span><i class="review"></i>Изменение — только через diff</span><span><i class="approval"></i>Команды и свои инструменты — с подтверждением</span><span><i class="verify"></i>Доказательство готовности — успешный тест/сборка</span></div>
        <div class="tool-grid">${tools.map(tool=>{const verifies=toolProvidesVerification(tool);return `<label class="tool-toggle risk-${esc(tool.risk)} ${verifies?'has-verification':''} ${enabled.has(tool.name)?'is-on':''}"><input type="checkbox" name="allowed-tool" value="${esc(tool.name)}" ${enabled.has(tool.name)?'checked':''}><span><strong>${esc(tool.displayName)}${tool.requiresApproval?'<em>подтверждение</em>':''}${verifies?'<em class="verify-mark">доказательство</em>':''}</strong><small>${esc(tool.description)}</small><b>${esc(TOOL_GROUP_LABELS[tool.category] || 'Запуск')}</b></span></label>`}).join('')}</div>
      </section>`
    const limitsStep = `<section class="profile-config-block profile-step" data-step-panel="limits"><header><span>04</span><div><strong>Лимиты и подтверждения</strong><small>Рекомендуемый лимит ходов для проверяемых квестов — 30</small></div></header>
        <p class="create-step-why">Лимит ходов останавливает зацикливание. Политика подтверждений решает, когда Point спросит вас перед действием.</p>
        <div class="settings-grid"><label>Максимум шагов<input id="max-steps" type="number" min="1" max="100" value="${Number(profile.maxSteps||30)}"><small>Остановка по лимиту ходов · по умолчанию 30</small></label><label>Тайм-аут, сек<input id="timeout" type="number" min="1" max="3600" value="${Number(profile.maxDurationSeconds||600)}"><small>Общий потолок длительности</small></label></div>
        <label>Политика подтверждений<select id="approval-mode"><option value="safe" ${profile.approvalMode==='safe'?'selected':''}>Опасные действия: diff и команды</option><option value="always" ${profile.approvalMode==='always'?'selected':''}>Подтверждать каждый инструмент</option></select></label>
        ${creating ? hireSummaryPanel(profile, readiness) : ''}
        ${!creating?`<div class="profile-danger-row"><button type="button" class="secondary" data-action="export-profile">Экспорт шаблона</button></div>`:''}
      </section>`
    const formPanels = creating
      ? `${classStep.replace('class="profile-config-block profile-step create-class-step"', `class="profile-config-block profile-step create-class-step${step==='class'?'':' is-hidden'}"`)}
        ${identityStep.replace('class="profile-config-block profile-step"', `class="profile-config-block profile-step${step==='identity'?'':' is-hidden'}"`)}
        ${modelStep.replace('class="profile-config-block profile-step"', `class="profile-config-block profile-step${step==='model'?'':' is-hidden'}"`)}
        ${toolsStep.replace('class="tool-section profile-config-block profile-step"', `class="tool-section profile-config-block profile-step${step==='tools'?'':' is-hidden'}"`)}
        ${limitsStep.replace('class="profile-config-block profile-step"', `class="profile-config-block profile-step${step==='limits'?'':' is-hidden'}"`)}`
      : `${identityStep.replace('class="profile-config-block profile-step"', `class="profile-config-block profile-step${step==='identity'?'':' is-hidden'}"`)}
        ${modelStep.replace('class="profile-config-block profile-step"', `class="profile-config-block profile-step${step==='model'?'':' is-hidden'}"`)}
        ${toolsStep.replace('class="tool-section profile-config-block profile-step"', `class="tool-section profile-config-block profile-step${step==='tools'?'':' is-hidden'}"`)}
        ${limitsStep.replace('class="profile-config-block profile-step"', `class="profile-config-block profile-step${step==='limits'?'':' is-hidden'}"`)}`
    return shell(`<main class="settings agent-studio profile-wizard ${creating ? 'create-flow' : ''}">
      <div class="section-title"><span>${creating?'Новый агент':'Карточка агента'}</span><em>${countOf(profiles.length, 'агент', 'агента', 'агентов')}</em></div>
      ${isWide?'<button class="secondary roster-back" type="button" data-action="close-profile-editor">← К агентам</button>':'<button class="secondary roster-expand" type="button" data-action="open-roster">Развернуть список →</button>'}
      ${creating ? '' : `<div class="studio-actions"><button class="secondary" data-action="new-profile">Новый агент</button><button class="secondary" data-action="duplicate-profile">⧉ Двойник</button><button class="secondary" data-action="import-profile">⇧ Призвать</button></div>`}
      ${creating ? `<header class="create-flow-hero"><div><span class="quest-label">Новый агент</span><h1>Соберите агента по шагам</h1><p>Шаблон даёт роль и инструменты. Дальше — личность, проверка модели, разрешения и лимиты. В конце — сводка и старт квеста.</p></div>${step === 'class' ? hireLiveChecklist(profile) : ''}</header>` : `${agentCharacterCard(profile)}${readinessBanner(profile, { showFixes: true })}`}
      ${creating ? readinessBanner(profile, { showFixes: true }) : ''}
      ${agentCapabilityHtml(profile)}
      ${profileStepNav(step, creating)}
      <div class="create-flow-body ${creating && step !== 'class' ? 'with-dossier' : ''}">
        <form id="settings-form" class="profile-wizard-form create-flow-form" data-active-step="${esc(step)}" novalidate>
          ${formPanels}
          ${profileStepFooter(step, creating, readiness)}
        </form>
        ${creating && step !== 'class' ? `<aside class="create-dossier">${agentCharacterCard(profile)}${hireLiveChecklist(profile)}</aside>` : ''}
      </div>
      <button class="secondary restart" data-action="restart-server">Перезапустить локальный сервис</button>
    </main>`)
  }
  
  function newProfile(template) {
    const profiles = ui.state.boot?.profiles || []
    const source = template || ui.state.boot?.profileTemplates?.[0] || {}
    const selected = profiles.find(item=>item.id===ui.selectedProfileId)
    // Built-in role templates intentionally carry no credentials/provider.
    // A provider-bearing blueprint remains authoritative; otherwise inherit the
    // currently selected runtime or the first available profile.
    const runtimeBase = source.provider
      ? source
      : selected || profiles[0] || {}
    return {
      ...runtimeBase,
      ...source,
      id: '', createdAt: undefined, updatedAt: undefined,
      name: source.name || 'Новый агент',
      roleDescription: source.roleDescription || '',
      personality: source.personality || runtimeBase.personality || '',
      mission: source.mission || runtimeBase.mission || '',
      systemPrompt: source.systemPrompt || '',
      goals: [...(source.goals || runtimeBase.goals || [])],
      rules: [...(source.rules || runtimeBase.rules || [])],
      constraints: [...(source.constraints || runtimeBase.constraints || [])],
      projectRules: [...(source.projectRules || [])],
      skillIds: [...(source.skillIds || runtimeBase.skillIds || [])],
      allowedTools: [...(source.allowedTools || runtimeBase.allowedTools || [])],
      toolPolicies: { ...(source.toolPolicies || runtimeBase.toolPolicies || {}) },
      connectionId: source.connectionId || runtimeBase.connectionId || '',
      provider: source.provider || runtimeBase.provider || '',
      providerPreset: source.providerPreset || runtimeBase.providerPreset || '',
      baseUrl: source.baseUrl || runtimeBase.baseUrl || '',
      primaryModel: source.primaryModel || source.model || runtimeBase.primaryModel || runtimeBase.model || '',
      model: source.primaryModel || source.model || runtimeBase.primaryModel || runtimeBase.model || '',
      fallbackModels: [...(source.fallbackModels || runtimeBase.fallbackModels || [])],
      maxSteps: source.maxSteps || runtimeBase.maxSteps || 30,
      maxDurationSeconds: source.maxDurationSeconds || runtimeBase.maxDurationSeconds || 600,
      temperature: Number(source.temperature ?? runtimeBase.temperature ?? 0.2),
      maxOutputTokens: Number(source.maxOutputTokens || runtimeBase.maxOutputTokens || 4096),
      contextWindowTokens: Number(source.contextWindowTokens || runtimeBase.contextWindowTokens || 32768),
      reasoningEffort: source.reasoningEffort || runtimeBase.reasoningEffort || 'medium',
      approvalMode: source.approvalMode || runtimeBase.approvalMode || 'safe',
    }
  }
  
  function currentFormProfile() {
    const source = ui.profileDraft || (ui.state.boot?.profiles || []).find(item=>item.id===ui.selectedProfileId)
    if (!source) return undefined
    // Провайдер и адрес больше не выбираются в форме агента: их несёт
    // подключение. Радиокнопки пресетов остались только у Cursor-профилей,
    // поэтому читаем сначала подключение, а пресет — как запасной путь для
    // строк, которым связь ещё не досталась.
    const connectionId = root.querySelector('#connection-id')?.value ?? source.connectionId ?? ''
    const connection = (ui.state.boot?.connections || []).find(item => item.id === connectionId)
    const presetId = connection?.presetId || source.providerPreset || source.provider
    const preset = providerCatalog().find(item=>item.id===presetId)
    const maxStepsValue = Number(root.querySelector('#max-steps')?.value)
    const timeoutValue = Number(root.querySelector('#timeout')?.value)
    const nameField = root.querySelector('#profile-name')
    const toolInputs = root.querySelectorAll('input[name="allowed-tool"]')
    return {
      ...source,
      name: nameField ? nameField.value.trim() : (source.name || ''),
      roleDescription: root.querySelector('#role-description')?.value.trim() ?? source.roleDescription ?? '',
      connectionId,
      provider: connection?.provider || preset?.kind || source.provider,
      providerPreset: preset?.id || source.providerPreset || source.provider,
      baseUrl: connection?.baseUrl || root.querySelector('#base-url')?.value.trim() || preset?.baseUrl || source.baseUrl || '',
      model: root.querySelector('#model')?.value.trim() ?? source.model ?? '',
      temperature: Number(root.querySelector('#temperature')?.value ?? source.temperature ?? 0.2),
      maxOutputTokens: Number(root.querySelector('#max-output-tokens')?.value || source.maxOutputTokens || 4096),
      contextWindowTokens: Number(root.querySelector('#context-window-tokens')?.value || source.contextWindowTokens || 32768),
      reasoningEffort: root.querySelector('#reasoning-effort')?.value || source.reasoningEffort || 'none',
      systemPrompt: root.querySelector('#system-prompt')?.value ?? source.systemPrompt ?? '',
      goals: root.querySelector('#profile-goals') ? lines(root.querySelector('#profile-goals')?.value) : [...(source.goals || [])],
      rules: root.querySelector('#profile-rules') ? lines(root.querySelector('#profile-rules')?.value) : [...(source.rules || [])],
      allowedTools: toolInputs.length ? [...toolInputs].filter(item => item.checked).map(item => item.value) : [...(source.allowedTools || [])],
      maxSteps: Number.isFinite(maxStepsValue) && maxStepsValue > 0 ? maxStepsValue : (source.maxSteps || 30),
      maxDurationSeconds: Number.isFinite(timeoutValue) && timeoutValue > 0 ? timeoutValue : (source.maxDurationSeconds || 600),
      approvalMode: root.querySelector('#approval-mode')?.value || source.approvalMode || 'safe',
    }
  }
  
  function cloneCustomTool(source) {
    return {
      ...source,
      id: '', createdAt: undefined, updatedAt: undefined,
      arguments: [...(source.arguments || [])],
      parameters: (source.parameters || []).map(parameter=>({...parameter,enumValues:[...(parameter.enumValues||[])]})),
    }
  }
  
  function currentCustomToolForm() {
    const source=ui.customToolDraft || (ui.state.boot?.customTools||[]).find(item=>item.id===ui.selectedCustomToolId)
    if(!source)return undefined
    const kind=root.querySelector('#custom-tool-kind')?.value||source.kind||'process'
    const cards=[...root.querySelectorAll('.tool-parameter')]
    const parameters=cards.length?cards.map(card=>({
      name:card.querySelector('[data-field="parameter-name"]')?.value.trim()||'',
      displayName:card.querySelector('[data-field="parameter-display-name"]')?.value.trim()||'',
      description:card.querySelector('[data-field="parameter-description"]')?.value.trim()||'',
      type:card.querySelector('[data-field="parameter-type"]')?.value||'string',
      required:Boolean(card.querySelector('[data-field="parameter-required"]')?.checked),
      enumValues:(card.querySelector('[data-field="parameter-enum"]')?.value||'').split(',').map(value=>value.trim()).filter(Boolean),
      maxLength:Number(card.querySelector('[data-field="parameter-max-length"]')?.value)||1024,
    })):source.parameters||[]
    return {
      ...source, kind,
      displayName:root.querySelector('#custom-tool-name')?.value.trim()||'',
      description:root.querySelector('#custom-tool-description')?.value.trim()||'',
      command:root.querySelector('#custom-tool-command')?.value.trim()??source.command??'',
      program:root.querySelector('#custom-tool-program')?.value.trim()??source.program??'',
      arguments:root.querySelector('#custom-tool-arguments')?(root.querySelector('#custom-tool-arguments').value||'').split(/\r?\n/).map(value=>value.trim()).filter(Boolean):[...(source.arguments||[])],
      parameters:cards.length?parameters:(source.parameters||[]).map(parameter=>({...parameter,enumValues:[...(parameter.enumValues||[])]})),
      providesVerification:Boolean(root.querySelector('#custom-tool-verification')?.checked??source.providesVerification),
      cwd:root.querySelector('#custom-tool-cwd')?.value.trim()||'.',
      timeoutSeconds:Number(root.querySelector('#custom-tool-timeout')?.value)||120,
    }
  }
  
  function resetCustomToolPreview(clearArguments = true) {
    ui.customToolPreview=undefined
    ui.customToolPreviewStatus='idle'
    ui.customToolPreviewError=''
    if(clearArguments)ui.customToolPreviewArguments={}
  }
  
  function invalidateCustomToolPreview() {
    ui.customToolPreview=undefined
    ui.customToolPreviewStatus='idle'
    ui.customToolPreviewError=''
    root.querySelector('.tool-sandbox-result')?.remove()
    root.querySelector('.tool-sandbox-error')?.remove()
  }
  
  // Врезка с пояснением — тот же примитив, что у «Ключи и пароли только в
  // SecretStorage» на Связях. Своя разметка была залита акцентом во всю ширину и
  // читалась как тревога, хотя ничего не случилось: это справка, а не состояние.
  function toolNetworkPolicyCallout() {
    return `<aside class="connection-secret-note" role="note"><span>i</span><div><strong>Сеть · запрещена по умолчанию</strong><small>Наружу инструмент не ходит.</small><details><summary>Как открыть доступ</summary><small>Свой инструмент наследует неизменяемую сетевую политику запуска. Доступ открывают в конструкторе агента — точным именем хоста и портом, только по TLS и только через изолированный шлюз. Маски, числовые адреса и незащищённые соединения не принимаются.</small></details></div></aside>`
  }
  
  function customToolFormIssue(tool) {
    if (!tool) return 'Заполните форму инструмента'
    if (!(tool.displayName || '').trim()) return 'Укажите название инструмента'
    const kind = tool.kind || 'process'
    if (kind === 'process') {
      if (!(tool.program || '').trim()) return 'Укажите программу (PATH или относительный путь)'
      for (const parameter of tool.parameters || []) {
        if (!/^[a-z][a-z0-9_]{0,63}$/.test(parameter.name || '')) return `Некорректное имя параметра «${parameter.name || '—'}»`
        if (!(parameter.displayName || '').trim() || !(parameter.description || '').trim()) return `Заполните название и описание параметра «${parameter.name || '—'}»`
        if (parameter.type === 'enum' && !(parameter.enumValues || []).length) return `Добавьте значения для списка «${parameter.name}»`
      }
    } else if (!(tool.command || '').trim()) {
      return 'Укажите фиксированную команду оболочки'
    }
    const timeout = Number(tool.timeoutSeconds)
    if (!Number.isFinite(timeout) || timeout < 1 || timeout > 600) return 'Тайм-аут должен быть от 1 до 600 секунд'
    return ''
  }
  
  function toolSandbox(tool) {
    const parameters=tool.parameters||[]
    const fields=parameters.map(parameter=>{
      const value=ui.customToolPreviewArguments[parameter.name]??''
      const control=parameter.type==='enum'
        ? `<select data-preview-param="${esc(parameter.name)}"><option value="">— выбрать —</option>${(parameter.enumValues||[]).map(option=>`<option value="${esc(option)}" ${String(value)===option?'selected':''}>${esc(option)}</option>`).join('')}</select>`
        : `<input data-preview-param="${esc(parameter.name)}" type="${parameter.type==='integer'?'number':'text'}" value="${esc(value)}" placeholder="${parameter.type==='workspace_path'?'например, scripts/check.py':'тестовое значение'}">`
      return `<label><span>${esc(parameter.displayName||parameter.name)} <small>${parameter.required?'обязательный':'необязательный'} · ${esc(parameter.type)}</small></span>${control}</label>`
    }).join('')
    const result=ui.customToolPreview
      ? `<div class="tool-sandbox-result"><header><strong>✓ Конфигурация корректна</strong><small>Процесс не запускался</small></header><span>Программа</span><code>${esc(ui.customToolPreview.program)}</code><span>ARGV · ${(ui.customToolPreview.arguments||[]).length}</span><ol>${(ui.customToolPreview.arguments||[]).map((argument,index)=>`<li><b>${index}</b><code>${esc(JSON.stringify(argument))}</code></li>`).join('')||'<li><em>без аргументов</em></li>'}</ol><span>Рабочая папка</span><code title="${esc(ui.customToolPreview.resolvedCwd)}">${esc(ui.customToolPreview.cwd)}</code><details><summary>JSON-схема для модели</summary><pre>${esc(JSON.stringify(ui.customToolPreview.definition?.inputSchema||{},null,2))}</pre></details></div>`
      : ui.customToolPreviewError
        ? `<div class="tool-sandbox-error"><strong>Конфигурация не прошла проверку</strong><span>${esc(ui.customToolPreviewError)}</span></div>`
        : ''
    return `<section class="tool-sandbox"><header class="builder-stage"><span>05</span><div><strong>Тест</strong><small>Сначала проверяет параметры без выполнения процесса</small></div><button type="button" class="secondary" data-action="preview-custom-tool" ${ui.customToolPreviewStatus==='loading'?'disabled':''}>${ui.customToolPreviewStatus==='loading'?'Проверяем…':'Предпросмотр'}</button></header>${fields?`<div class="tool-sandbox-fields">${fields}</div>`:'<p>У инструмента нет параметров модели: будет проверен фиксированный argv.</p>'}${result}</section>`
  }
  
  function toolBuilder() {
    const customTools=ui.state.boot?.customTools||[]
    const templates=ui.state.boot?.customToolTemplates||[]
    if(!ui.selectedCustomToolId&&!ui.customToolDraft&&customTools[0])ui.selectedCustomToolId=customTools[0].id
    const stored=customTools.find(item=>item.id===ui.selectedCustomToolId)||customTools[0]
    // Пустой объект, а не undefined: на новом проекте нет ни своих инструментов,
    // ни черновика, и обращение к tool.program роняло отрисовку всего вебвью —
    // Арсенал открывался чёрным экраном ровно там, где его открывают впервые.
    const tool=ui.customToolDraft||stored||{}
    const creating=!tool?.id
    const kind=tool?.kind||'process'
    const processFields=kind==='process'?`<section class="process-tool-fields"><label>Программа<input id="custom-tool-program" maxlength="4096" value="${esc(tool.program||'')}" placeholder="go, git, npm или ./scripts/check" required><small>Имя из PATH или относительный путь внутри проекта. Абсолютные пути запрещены.</small></label><label>Аргументы · по одному на строку<textarea id="custom-tool-arguments" rows="6" maxlength="32768" placeholder="test&#10;./...&#10;--format={{format}}">${esc((tool.arguments||[]).join('\n'))}</textarea><small>Подстановка <code>{{имя}}</code> остаётся одним элементом argv и не проходит через оболочку. Аргумент с отсутствующим необязательным параметром пропускается.</small></label><section class="tool-parameters"><header class="builder-stage"><span>04</span><div><strong>Параметры модели</strong><small>До 16 типизированных значений</small></div><button type="button" class="secondary" data-action="add-tool-parameter">＋ Параметр</button></header>${(tool.parameters||[]).map((parameter,index)=>`<article class="tool-parameter" data-index="${index}"><header><span>${index+1}</span><strong>${esc(parameter.displayName||parameter.name||`Параметр ${index+1}`)}</strong><button type="button" data-action="remove-tool-parameter" data-index="${index}">×</button></header><div class="settings-grid"><label>Имя в схеме<input data-field="parameter-name" maxlength="64" pattern="[a-z][a-z0-9_]*" value="${esc(parameter.name)}" placeholder="target_path" required></label><label>Тип<select data-field="parameter-type"><option value="string" ${parameter.type==='string'?'selected':''}>Строка</option><option value="integer" ${parameter.type==='integer'?'selected':''}>Целое число</option><option value="enum" ${parameter.type==='enum'?'selected':''}>Список значений</option><option value="workspace_path" ${parameter.type==='workspace_path'?'selected':''}>Путь внутри проекта</option></select></label></div><label>Название<input data-field="parameter-display-name" maxlength="120" value="${esc(parameter.displayName)}" placeholder="Целевой файл" required></label><label>Описание для модели<textarea data-field="parameter-description" rows="2" maxlength="1024" required>${esc(parameter.description)}</textarea></label><label class="parameter-enum ${parameter.type==='enum'?'':'is-hidden'}">Разрешённые значения · через запятую<input data-field="parameter-enum" value="${esc((parameter.enumValues||[]).join(', '))}" placeholder="short, full"></label><div class="settings-grid"><label>Макс. длина<input data-field="parameter-max-length" type="number" min="1" max="16384" value="${parameter.maxLength||1024}"></label><label class="check-row"><input data-field="parameter-required" type="checkbox" ${parameter.required?'checked':''}> Обязательный параметр</label></div></article>`).join('')||'<div class="empty-parameters">Параметров нет: инструмент запускает один фиксированный набор аргументов.</div>'}</section><div class="argv-preview"><strong>Шаблон argv</strong><code>${esc([tool.program||'program',...(tool.arguments||[])].map(value=>JSON.stringify(value)).join(' '))}</code></div>${toolSandbox(tool)}</section>`:''
    const commandFields=kind==='command'?`<label>Фиксированная команда оболочки<textarea id="custom-tool-command" rows="5" maxlength="32768" placeholder="go test ./..." required>${esc(tool.command||'')}</textarea><small>Совместимый режим для составных команд. Модель не может подставлять параметры в строку. Сеть тоже наследует запрет агента.</small></label>`:''
    return shell(`<main class="settings tool-builder tool-arsenal"><header class="hub-page-head"><div><h1>Свои инструменты</h1><p>Пять стадий ниже: от шаблона до теста. После сохранения включите инструмент в список разрешённых у агента.</p></div><em>${customTools.length}</em></header>
      ${toolNetworkPolicyCallout()}
      <div class="studio-actions"><button class="secondary" data-action="new-custom-tool">＋ Выковать</button><button class="secondary" data-action="duplicate-custom-tool" ${!tool||creating?'disabled':''}>⧉ Копия</button><button class="secondary" data-action="import-custom-tool">⇧ Импорт</button></div>
      <section class="custom-tool-presets"><header class="builder-stage"><span>01</span><div><strong>Шаблон</strong><small>Готовая заготовка или чистый лист</small></div></header><div>${templates.map(template=>`<button type="button" data-action="use-custom-tool-template" data-template="${esc(template.id)}"><b>${esc(template.name)}</b><small>${esc(template.description)}</small></button>`).join('')}</div></section>
      ${tool?`<form id="custom-tool-form" class="tool-form" novalidate><header class="builder-stage"><span>02</span><div><strong>Имя</strong><small>Как инструмент видят человек и модель</small></div></header><label>Инструмент<select id="custom-tool-select" ${creating?'disabled':''}>${creating?'<option>Новый инструмент</option>':''}${customTools.map(item=>`<option value="${esc(item.id)}" ${item.id===tool.id?'selected':''}>${esc(item.displayName)}</option>`).join('')}</select></label><div class="settings-grid"><label>Название<input id="custom-tool-name" maxlength="120" value="${esc(tool.displayName)}" placeholder="Например, Проверить API" required></label><label>Режим<select id="custom-tool-kind"><option value="process" ${kind==='process'?'selected':''}>Процесс · argv</option><option value="command" ${kind==='command'?'selected':''}>Фиксированная команда</option></select></label></div><label>Описание для модели<textarea id="custom-tool-description" rows="2" maxlength="4096" placeholder="Когда и зачем агенту использовать этот инструмент">${esc(tool.description||'')}</textarea><small>Если пусто — подставится из названия при сохранении.</small></label><header class="builder-stage"><span>03</span><div><strong>Процесс</strong><small>Что и с какими аргументами запускается</small></div></header>${processFields}${commandFields}<details class="tool-advanced"><summary>Расширенные поля</summary><div class="settings-grid"><label>Рабочая папка<input id="custom-tool-cwd" value="${esc(tool.cwd||'.')}" placeholder="."></label><label>Тайм-аут, сек<input id="custom-tool-timeout" type="number" min="1" max="600" value="${tool.timeoutSeconds||120}"></label></div><label class="check-row verification-evidence"><input id="custom-tool-verification" type="checkbox" ${tool.providesVerification?'checked':''}> <span><strong>Доказательство готовности</strong><small>Только успешный, действительно проверочный процесс подтверждает завершение.</small></span></label></details><div class="profile-footer">${creating?'<button type="button" class="secondary" data-action="cancel-custom-tool">Отмена</button>':`<div class="tool-record-actions"><button type="button" class="danger-button" data-action="delete-custom-tool" data-id="${esc(tool.id)}">Удалить</button></div>`}<button class="primary save" type="submit">${creating?'Сохранить и экипировать':'Сохранить'}</button></div></form>`:`<div class="empty compact"><h3>Своих инструментов пока нет</h3><p>Выберите шаблон или создайте безопасный процесс.</p></div>`}
      <div class="builder-footnote">После сохранения инструмент появится в каталоге Студии агентов. Удалить используемый инструмент можно только после отключения во всех агентах, чертежах и профилях.</div></main>`)
  }
  
  function newWorkflowStep(index = 0) {
    const profileId=(ui.state.boot?.profiles||[])[0]?.id||''
    return {id:'',name:index===0?'Анализ':'Этап '+(index+1),kind:'agent',profileId,instruction:'',condition:undefined,onFailure:'stop',includeOriginalContext:index===0,includePreviousResult:index>0}
  }
  
  function newWorkflow() {
    return {id:'',name:'Новый сценарий',description:'',steps:[newWorkflowStep(0),newWorkflowStep(1)],createdAt:undefined,updatedAt:undefined}
  }
  
  const workflowTemplates = [
    {id:'feature',name:'Фича под ключ',description:'Анализ → реализация → ревью',stages:[['Анализ','Изучи задачу и архитектуру. Дай план, риски и затрагиваемые файлы.'],['Реализация','Реализуй согласованный план, проверь изменения релевантными тестами.'],['Ревью','Проверь результат прошлого этапа, diff, риски и критерии готовности.']]},
    {id:'bug',name:'Охота на ошибку',description:'Воспроизведение → исправление → регрессия',stages:[['Диагностика','Воспроизведи проблему и найди доказуемую первопричину.'],['Исправление','Внеси минимальное исправление первопричины.'],['Регрессия','Проверь исправление и наличие теста, закрывающего исходный сценарий.']]},
    {id:'review',name:'Совет хранителей',description:'Архитектура → качество → финальный вывод',stages:[['Архитектура','Оцени границы, зависимости и системные риски.'],['Код-ревью','Проверь корректность, безопасность и тестируемость.'],['Вердикт','Собери выводы, устрани дубли и расставь приоритеты.']]},
  ]
  
  function workflowFromTemplate(id) {
    const template=workflowTemplates.find(item=>item.id===id)||workflowTemplates[0]
    const profiles=(ui.state.boot?.profiles||[])
    return {id:'',name:template.name,description:template.description,steps:template.stages.map((stage,index)=>({id:'',name:stage[0],profileId:profiles[index%Math.max(1,profiles.length)]?.id||'',instruction:stage[1],includeOriginalContext:index===0,includePreviousResult:index>0})),createdAt:undefined,updatedAt:undefined}
  }
  
  function currentWorkflowForm() {
    const source=ui.workflowDraft || (ui.state.boot?.workflows||[]).find(item=>item.id===ui.selectedWorkflowId)
    if(!source)return undefined
    const cards=[...root.querySelectorAll('.workflow-step')]
    const steps=cards.length?cards.map(card=>({
      id:card.dataset.stepId||'',
      name:card.querySelector('[data-field="step-name"]')?.value.trim()||'',
      profileId:card.querySelector('[data-field="step-profile"]')?.value||'',
      instruction:card.querySelector('[data-field="step-instruction"]')?.value.trim()||'',
      includeOriginalContext:Boolean(card.querySelector('[data-field="step-original"]')?.checked),
      includePreviousResult:Boolean(card.querySelector('[data-field="step-previous"]')?.checked),
      kind:card.querySelector('[data-field="step-kind"]')?.value||'agent',
      condition:(() => {
        const type = card.querySelector('[data-field="step-condition-type"]')?.value.trim() || ''
        if (!type) return undefined
        const value = card.querySelector('[data-field="step-condition-value"]')?.value.trim() || ''
        return type === 'previous_status' ? { type, value } : { type }
      })(),
      onFailure:card.querySelector('[data-field="step-on-failure"]')?.value||'stop',
    })):source.steps
    return {...source,name:root.querySelector('#workflow-name')?.value.trim()||source.name,description:root.querySelector('#workflow-description')?.value.trim()||'',steps}
  }
  
  const FLOW_NODE_KINDS = [
    ['input', 'Вход'], ['agent', 'Агент'], ['tool', 'Инструмент'], ['condition', 'Условие'],
    ['parallel', 'Параллель'], ['join', 'Слияние'], ['loop', 'Цикл'], ['verifier', 'Верификатор'],
    ['approval', 'Подтверждение'], ['output', 'Выход'],
  ]
  const flowNodeKindLabels = Object.fromEntries(FLOW_NODE_KINDS)
  function newFlowNode(kind = 'agent') {
    const id = `node-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`
    return { id, kind, name: flowNodeKindLabels[kind] || kind, config: {}, agentId: '', toolName: '', positionX: 0, positionY: 0 }
  }
  // Место узла на холсте. Ось, которую не прочитать числом, берётся из сетки:
  // координата уезжает в атрибут разметки, и `NaN` там был бы не «неизвестно»,
  // а испорченный атрибут, который расстановка молча пропустит.
  function flowNodePosition(node, index) {
    const col = index % 3
    const row = Math.floor(index / 3)
    const grid = { x: 24 + col * 150, y: 24 + row * 96 }
    if (!node.positionX && !node.positionY) return grid
    const x = Number(node.positionX)
    const y = Number(node.positionY)
    return { x: Number.isFinite(x) ? x : grid.x, y: Number.isFinite(y) ? y : grid.y }
  }
  function activeFlowRun(flowId) {
    const active = ['running', 'waiting', 'waiting_approval', 'paused', 'pending']
    return (ui.state.boot?.flowRuns || []).find(item => item.flowId === flowId && active.includes(item.status))
  }
  function mergeCandidateLabel(candidate) {
    const execution = (ui.state.boot?.executions || []).find(item => item.id === candidate.executionId)
    const agent = execution ? agentById(execution.projectAgentId) : undefined
    return agent?.name || execution?.task || candidate.executionId || 'ветка'
  }
  function flowMergeConflictPanelHtml(run) {
    if (!run?.nodeStates) return ''
    const conflictNodes = Object.entries(run.nodeStates).filter(([, nodeState]) => nodeState?.output?.waitReason === 'sandbox_merge_conflict')
    if (!conflictNodes.length) return ''
    const cards = conflictNodes.map(([nodeId, nodeState]) => {
      const conflicts = Array.isArray(nodeState.output?.mergeConflicts) ? nodeState.output.mergeConflicts : []
      const resolutions = Array.isArray(nodeState.output?.mergeResolutions) ? nodeState.output.mergeResolutions : []
      const resolvedByPath = new Map(resolutions.map(item => [item.path, item]))
      const rows = conflicts.map(conflict => {
        const resolution = resolvedByPath.get(conflict.path)
        const candidates = (conflict.candidates || []).map(candidate => {
          const selected = resolution?.strategy === 'use_parent' && resolution.executionId === candidate.executionId
          return `<button type="button" class="secondary ${selected ? 'selected' : ''}" data-action="resolve-flow-merge" data-flow-run-id="${esc(run.id)}" data-node-id="${esc(nodeId)}" data-path="${esc(conflict.path)}" data-execution-id="${esc(candidate.executionId)}">${selected ? '✓ ' : ''}${esc(mergeCandidateLabel(candidate))}</button>`
        }).join('')
        const manualSelected = resolution?.strategy === 'manual'
        return `<article class="flow-merge-conflict-row"><header><strong>${esc(conflict.path)}</strong><small>${resolution ? 'решение сохранено' : 'выберите итог'}</small></header><div class="flow-merge-candidates">${candidates}</div><details ${manualSelected ? 'open' : ''}><summary>Собрать вручную</summary><textarea rows="5" class="flow-merge-manual" placeholder="Итоговое содержимое файла">${manualSelected && resolution.content != null ? esc(resolution.content) : ''}</textarea><div><button type="button" class="secondary" data-action="resolve-flow-merge-manual" data-flow-run-id="${esc(run.id)}" data-node-id="${esc(nodeId)}" data-path="${esc(conflict.path)}">Сохранить текст</button><button type="button" class="secondary" data-action="resolve-flow-merge-delete" data-flow-run-id="${esc(run.id)}" data-node-id="${esc(nodeId)}" data-path="${esc(conflict.path)}">Удалить файл</button></div></details></article>`
      }).join('')
      return `<section class="flow-merge-conflict-card"><header><div><span>Слияние веток</span><strong>Нужно выбрать итог файлов</strong></div><em>${conflicts.length}</em></header><p>Point остановил следующий узел и ничего не потерял. Независимые файлы уже объединены; решите только пересечения.</p>${rows}</section>`
    }).join('')
    return `<div class="flow-merge-conflicts">${cards}</div>`
  }
  function flowRuntimeSummaryHtml(run, flow) {
    if (!run?.nodeStates) return ''
    const entries = Object.entries(run.nodeStates)
    const completed = entries.filter(([, item]) => item.status === 'completed' || item.status === 'skipped').length
    const mergeSets = entries.map(([, item]) => item.output?.mergeChangeSetId).filter(Boolean)
    return `<section class="flow-runtime-summary"><header><strong>Текущий запуск</strong>${status(run.status)}</header><div><span>${completed} / ${countOf(entries.length, 'узел', 'узла', 'узлов')}</span>${mergeSets.length ? `<span>${mergeSets.length} слияние зафиксировано</span>` : ''}<span>${esc(flow?.name || run.flowId)}</span></div></section>${flowMergeConflictPanelHtml(run)}`
  }
  function captureFlowForm() {
    const flows = ui.state.boot?.flows || []
    const stored = flows.find(item => item.id === ui.selectedFlowId) || flows[0]
    const source = ui.flowDraft || stored || { id: '', name: 'Новый флоу', description: '', nodes: [], edges: [] }
    const nodes = [...(source.nodes || [])]
    for (const card of root.querySelectorAll('[data-flow-node-id]')) {
      const id = card.dataset.flowNodeId
      const node = nodes.find(item => item.id === id)
      if (!node) continue
      node.name = card.querySelector('[data-field="node-name"]')?.value.trim() || node.name
      node.kind = card.querySelector('[data-field="node-kind"]')?.value || node.kind
      node.agentId = card.querySelector('[data-field="node-agent"]')?.value || ''
      node.toolName = card.querySelector('[data-field="node-tool"]')?.value || ''
      const configRaw = card.querySelector('[data-field="node-config"]')?.value.trim()
      if (configRaw) {
        try {
          const parsed = JSON.parse(configRaw)
          if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
            node.config = parsed
          }
        } catch { /* keep previous */ }
      }
  		if (node.kind === 'loop' && !Number.isInteger(Number(node.config?.maxIterations))) {
  			node.config = { ...(node.config || {}), maxIterations: 3 }
  		}
    }
    const edges = []
    const edgeCount = Number(root.querySelector('#flow-edge-count')?.value || 0)
    for (let index = 0; index < edgeCount; index++) {
      const from = root.querySelector(`#flow-edge-from-${index}`)?.value
      const to = root.querySelector(`#flow-edge-to-${index}`)?.value
      const condition = root.querySelector(`#flow-edge-condition-${index}`)?.value.trim() || ''
      if (from && to) edges.push({ id: `edge-${index}`, from, to, condition })
    }
    return {
      ...source,
      name: root.querySelector('#flow-name')?.value.trim() || source.name || 'Новый флоу',
      description: root.querySelector('#flow-description')?.value.trim() || '',
      nodes,
      edges,
    }
  }
  function flowEdgeOptions(nodes, selectedId) {
    return (nodes || []).map(node => `<option value="${esc(node.id)}" ${node.id === selectedId ? 'selected' : ''}>${esc(node.name || node.id)}</option>`).join('')
  }
  function flowCanvasHtml(flow, locked, selectedNodeId, flowRun) {
    const nodes = flow.nodes || []
    const edges = flow.edges || []
    const maxX = Math.max(320, ...nodes.map((node, index) => flowNodePosition(node, index).x + 130))
    const maxY = Math.max(240, ...nodes.map((node, index) => flowNodePosition(node, index).y + 72))
    const nodeHtml = nodes.map((node, index) => {
      const pos = flowNodePosition(node, index)
      const runtimeState = flowRun?.nodeStates?.[node.id]
      const runtimeClass = runtimeState?.output?.waitReason === 'sandbox_merge_conflict' ? 'merge-conflict' : (runtimeState?.status ? `runtime-${runtimeState.status}` : '')
      const binding = node.config?.modelBinding || {}
      const assignment = binding.model
        ? ` · ${[binding.runtime || binding.provider, binding.model, binding.estimatedCostCents != null && binding.estimatedCostCents !== '' ? `${binding.estimatedCostCents}¢` : ''].filter(Boolean).join(' · ')}`
        : ''
      return `<button type="button" class="flow-node kind-${esc(node.kind)} ${runtimeClass} ${selectedNodeId === node.id ? 'selected' : ''}" data-action="select-flow-node" data-node-id="${esc(node.id)}" data-x="${Math.round(pos.x)}" data-y="${Math.round(pos.y)}" ${locked ? 'disabled' : ''}><span>${esc(flowNodeKindLabels[node.kind] || node.kind)}${runtimeState?.status ? ` · ${esc(statusLabels[runtimeState.status] || runtimeState.status)}` : ''}${esc(assignment)}</span><strong>${esc(node.name || node.id)}</strong></button>`
    }).join('')
    const edgeSvg = edges.map(edge => {
      const fromNode = nodes.find(item => item.id === edge.from)
      const toNode = nodes.find(item => item.id === edge.to)
      if (!fromNode || !toNode) return ''
      const fromIndex = nodes.indexOf(fromNode)
      const toIndex = nodes.indexOf(toNode)
      const fromPos = flowNodePosition(fromNode, fromIndex)
      const toPos = flowNodePosition(toNode, toIndex)
      return `<line x1="${fromPos.x + 60}" y1="${fromPos.y + 28}" x2="${toPos.x + 10}" y2="${toPos.y + 28}" />`
    }).join('')
    return `<div class="flow-canvas-wrap"><svg class="flow-edges" width="${maxX}" height="${maxY}" aria-hidden="true">${edgeSvg}</svg><div class="flow-canvas" data-width="${Math.round(maxX)}" data-height="${Math.round(maxY)}">${nodeHtml || '<p class="muted flow-canvas-empty">Добавьте узлы</p>'}</div></div>`
  }
  // Место узла на холсте — атрибутом и CSSOM, а не инлайновым стилем.
  //
  // CSP вебвью (extension.js, style-src без 'unsafe-inline') выбрасывает
  // атрибут style="" целиком. Холст задавал им ровно то, ради чего он холст:
  // style="left:210px;top:140px" у каждого узла. Стиль отбрасывался, у правила
  // .flow-node оставалось одно position: absolute без смещений, и все узлы
  // садились в один угол друг на друга. Связи при этом рисовались по местам:
  // у <line> координаты — презентационные атрибуты x1/y1, а не стиль, и CSP их
  // не трогает. Получалась схема, где стрелки расходятся по пустому полю, а
  // узлы лежат стопкой в начале координат.
  //
  // Приём соседней полосы прогресса (data-fill + ui/layers/11-progress-fill.css)
  // сюда не переносится: там двадцать одна ступень по пять процентов, а здесь
  // произвольный пиксель по двум осям — правил понадобилось бы столько, сколько
  // точек на холсте. Остаётся второй здешний способ обойти CSP: писать свойство
  // из скрипта. Программная правка style — не инлайновый стиль в разметке, её
  // style-src не касается; так же меряет запас ленты applyMasterComposeReserve
  // в ui/client/master-feed.js.
  //
  // Число едет в data-x/data-y, потому что атрибут переживает и CSP, и
  // повторную сборку разметки: холст пересобирается на каждой отрисовке, и
  // расстановка обязана уметь начать с нуля, имея на руках только разметку.
  function flowNodePixels(element, property, raw) {
    const value = Number(raw)
    if (!Number.isFinite(value) || typeof element?.style?.setProperty !== 'function') return false
    element.style.setProperty(property, `${Math.round(value)}px`)
    return true
  }
  function applyFlowNodePlacement(scope) {
    const host = scope || root
    if (typeof host?.querySelector !== 'function') return 0
    const wrap = host.querySelector('.flow-canvas-wrap')
    const canvas = typeof wrap?.querySelector === 'function' ? wrap.querySelector('.flow-canvas') : null
    if (!canvas || typeof canvas.querySelectorAll !== 'function') return 0
    const sized = flowNodePixels(canvas, 'width', canvas.dataset?.width) && flowNodePixels(canvas, 'height', canvas.dataset?.height)
    let placed = 0
    let total = 0
    for (const node of canvas.querySelectorAll('.flow-node')) {
      total += 1
      const left = flowNodePixels(node, 'left', node.dataset?.x)
      const top = flowNodePixels(node, 'top', node.dataset?.y)
      if (left && top) placed += 1
    }
    // Признак ставится, только когда расставлены все до одного. Половина узлов
    // на местах, половина в углу — картинка хуже запасной: запасная честно
    // говорит «мест нет», а такая врёт, что места именно эти. Запасную даёт
    // ui/layers/60-suggestions.css: без признака холст раскладывает узлы рядом,
    // а связи прячет — рисовать их не по чему.
    if (sized && placed === total) wrap.classList?.add('is-placed')
    else wrap.classList?.remove('is-placed')
    return placed
  }
  function flowInspectorHtml(flow, locked, selectedNodeId) {
    const selected = (flow.nodes || []).find(item => item.id === selectedNodeId) || (flow.nodes || [])[0]
    if (!selected) {
      return `<aside class="flow-inspector empty"><strong>Инспектор узла</strong><p class="muted">Выберите узел на холсте.</p></aside>`
    }
    const agents = hubAgents()
    const tools = ui.state.boot?.toolCatalog || []
    const configJson = JSON.stringify(selected.config || {}, null, 2)
  	const loopHelp = selected.kind === 'loop' ? '<small>Loop требует maxIterations 1–20, одну связь continue к телу, одну done к выходу и обратную связь тела без условия.</small>' : ''
    return `<aside class="flow-inspector" data-flow-node-id="${esc(selected.id)}"><header><strong>${esc(selected.name)}</strong><small>${esc(flowNodeKindLabels[selected.kind] || selected.kind)}</small></header><label>Название<input data-field="node-name" value="${esc(selected.name)}" ${locked ? 'disabled' : ''}></label><label>Тип<select data-field="node-kind" ${locked ? 'disabled' : ''}>${FLOW_NODE_KINDS.map(([value, label]) => `<option value="${esc(value)}" ${selected.kind === value ? 'selected' : ''}>${esc(label)}</option>`).join('')}</select></label><label>Агент<select data-field="node-agent" ${locked ? 'disabled' : ''}><option value="">—</option>${agents.map(agent => `<option value="${esc(agent.id)}" ${agent.id === selected.agentId ? 'selected' : ''}>${esc(agent.name)}</option>`).join('')}</select></label><label>Инструмент<select data-field="node-tool" ${locked ? 'disabled' : ''}><option value="">—</option>${tools.map(tool => `<option value="${esc(tool.name)}" ${tool.name === selected.toolName ? 'selected' : ''}>${esc(tool.displayName || tool.name)}</option>`).join('')}</select></label><label>Параметры узла (JSON)<textarea data-field="node-config" rows="6" ${locked ? 'disabled' : ''}>${esc(configJson)}</textarea>${loopHelp}</label>${locked ? '' : `<button type="button" class="danger-button" data-action="remove-flow-node" data-node-id="${esc(selected.id)}">Удалить узел</button>`}</aside>`
  }
  function visualFlowBuilder() {
    const flows = ui.state.boot?.flows || []
    if (!ui.selectedFlowId && !ui.flowDraft && flows[0]) ui.selectedFlowId = flows[0].id
    const stored = flows.find(item => item.id === ui.selectedFlowId) || flows[0]
    const flow = ui.flowDraft || stored || { id: '', name: 'Новый флоу', description: '', nodes: [], edges: [] }
    const creating = !flow?.id
    const lockedRun = flow.id ? activeFlowRun(flow.id) : null
    const locked = Boolean(lockedRun)
    if (!flow.nodes?.length && creating) {
      const draft = {
        ...flow,
        nodes: [newFlowNode('input'), newFlowNode('agent'), newFlowNode('output')],
        edges: [],
      }
      draft.edges = [
        { id: 'edge-0', from: draft.nodes[0].id, to: draft.nodes[1].id },
        { id: 'edge-1', from: draft.nodes[1].id, to: draft.nodes[2].id },
      ]
      if (!ui.flowDraft) ui.flowDraft = draft
    }
    const working = ui.flowDraft || flow
    // Связь соединяет два узла: при одном кнопка была живой, а клик уходил в
    // пустоту — «нажал, ничего не произошло» читается как поломка, а не как
    // «пока нельзя». Считается по working: новому флоу узлы подставляются выше,
    // и признак, взятый до подстановки, врал бы ровно в этом случае.
    const canLink = (working.nodes || []).length >= 2
    if (!ui.selectedFlowNodeId && working.nodes?.[0]) ui.selectedFlowNodeId = working.nodes[0].id
    const edgeRows = (working.edges || []).map((edge, index) => `<div class="flow-edge-row"><label>Из<select id="flow-edge-from-${index}" ${locked ? 'disabled' : ''}><option value="">—</option>${flowEdgeOptions(working.nodes, edge.from)}</select></label><label>→ В<select id="flow-edge-to-${index}" ${locked ? 'disabled' : ''}><option value="">—</option>${flowEdgeOptions(working.nodes, edge.to)}</select></label><label>Условие<input id="flow-edge-condition-${index}" value="${esc(edge.condition || '')}" placeholder="true / false / continue / done" ${locked ? 'disabled' : ''}></label></div>`).join('')
    return shell(`<main class="settings flow-visual-builder"><div class="section-title"><span>Граф флоу</span><em>${esc(countOf(flows.length, 'сценарий', 'сценария', 'сценариев'))}</em></div>${locked ? `<aside class="flow-lock-banner">⚠ Активный запуск флоу — структура заблокирована (${esc(statusLabels[lockedRun.status] || lockedRun.status)})</aside>` : ''}${flowRuntimeSummaryHtml(lockedRun, working)}<div class="studio-actions"><button class="secondary" data-action="new-flow" ${locked ? 'disabled' : ''}>＋ Пустой флоу</button><button class="secondary" data-action="add-flow-node" ${locked ? 'disabled' : ''}>＋ Узел</button><button class="secondary" data-action="add-flow-edge" ${locked || !canLink ? 'disabled' : ''}${!locked && !canLink ? ' title="Связь соединяет два узла — добавьте второй"' : ''}>＋ Связь</button><button class="secondary" data-action="flow-legacy-mode">Прежний конструктор →</button></div><form id="flow-form"><label>Сценарий<select id="flow-select" ${creating ? 'disabled' : ''}>${creating ? '<option>Новый флоу</option>' : ''}${flows.map(item => `<option value="${esc(item.id)}" ${item.id === working.id ? 'selected' : ''}>${esc(item.name)}</option>`).join('')}</select></label><label>Название<input id="flow-name" value="${esc(working.name)}" required ${locked ? 'disabled' : ''}></label><label>Описание<textarea id="flow-description" rows="2" ${locked ? 'disabled' : ''}>${esc(working.description || '')}</textarea></label><div class="flow-split">${flowCanvasHtml(working, locked, ui.selectedFlowNodeId, lockedRun)}${flowInspectorHtml(working, locked, ui.selectedFlowNodeId)}</div><section class="flow-edges-editor"><header><strong>Связи</strong><small>from → to</small></header><input type="hidden" id="flow-edge-count" value="${(working.edges || []).length}">${edgeRows || '<p class="muted">Связей пока нет</p>'}</section><div class="profile-footer">${!creating && !locked ? `<button class="secondary" type="button" data-action="start-flow" data-flow-id="${esc(working.id)}">▶ Запустить флоу</button>` : ''}<button class="primary save" type="submit" ${locked ? 'disabled' : ''}>${creating ? 'Создать флоу' : 'Сохранить флоу'}</button></div></form></main>`)
  }
  function flowsView() {
    if (ui.flowLegacyMode || ui.state.boot?.flows === undefined) return workflowBuilder()
    return visualFlowBuilder()
  }
  
  function workflowTimeline(run) {
    if(!run)return ''
    return `<section class="workflow-run-panel"><header>${status(run.status)}<div><strong>${esc(run.snapshot?.workflow?.name||run.workflowId)}</strong><small>этап ${esc(run.currentStep)} / ${run.stepRuns?.length||0}</small></div>${['running','waiting_approval'].includes(run.status)?`<button class="danger-button" data-action="cancel-workflow" data-id="${esc(run.id)}">Остановить</button>`:''}</header><p>${esc(run.task)}</p><div class="workflow-timeline">${(run.stepRuns||[]).map((step,index)=>`<article class="workflow-stage stage-${esc(step.status)}"><span>${index+1}</span><div><strong>${esc(step.stepName)}</strong><small>${esc(step.profileName)} · ${esc(statusLabels[step.status]||step.status)}</small>${step.error?`<em>${esc(step.error)}</em>`:''}</div>${step.runId?`<button class="secondary" data-action="load-run" data-id="${esc(step.runId)}">Открыть</button>`:''}</article>`).join('')}</div>${run.result?`<details class="workflow-result"><summary>Финальный результат</summary><p>${esc(run.result)}</p></details>`:''}</section>`
  }
  
  function workflowBuilder() {
    const workflows=ui.state.boot?.workflows||[]
    const profiles=ui.state.boot?.profiles||[]
    const workflowRuns=ui.state.boot?.workflowRuns||[]
    if(!ui.selectedWorkflowId&&!ui.workflowDraft&&workflows[0])ui.selectedWorkflowId=workflows[0].id
    const stored=workflows.find(item=>item.id===ui.selectedWorkflowId)||workflows[0]
    const workflow=ui.workflowDraft||stored
    const creating=!workflow?.id
    const active=ui.state.workflowDetails
    if(!workflow)return shell(`<main class="settings workflow-builder"><div class="section-title"><span>Свои flow</span><em>0</em></div><div class="empty compact"><h3>Соберите первый flow</h3><p>Каждый этап выбирает агента, инструкцию и передаваемый контекст. Начните с чистого flow или рабочего шаблона.</p><button class="primary" data-action="new-workflow">＋ Пустой flow</button><div class="workflow-template-grid">${workflowTemplates.map(item=>`<button data-action="use-workflow-template" data-template="${esc(item.id)}"><strong>${esc(item.name)}</strong><small>${esc(item.description)}</small></button>`).join('')}</div></div></main>`)
    const usedProfileIds=[...new Set((workflow.steps||[]).map(step=>step.profileId))]
    const keyProfiles=usedProfileIds.map(id=>profiles.find(profile=>profile.id===id)).filter(profile=>requiresApiKey(profile))
    const invalidSteps = (workflow.steps||[]).filter(step => step.kind !== 'manual' && !step.profileId)
    return shell(`<main class="settings workflow-builder hybrid-flows">
      <div class="section-title"><span>Редактор схем</span><em>${workflows.length} flow</em></div>
      
      <div data-workflow-run-host>${workflowTimeline(active)}</div>
      <div class="studio-actions"><button class="secondary" data-action="new-workflow">＋ Пустой flow</button>${workflowTemplates.map(item=>`<button class="secondary" data-action="use-workflow-template" data-template="${esc(item.id)}">${esc(item.name)}</button>`).join('')}<button class="secondary" data-action="duplicate-workflow" ${creating?'disabled':''}>⧉ Копия</button><button class="secondary" data-action="add-workflow-step" ${(workflow.steps?.length||0)>=12?'disabled':''}>＋ Этап</button></div>
      <form id="workflow-form">
        <label>Сценарий<select id="workflow-select" ${creating?'disabled':''}>${creating?'<option>Новый сценарий</option>':''}${workflows.map(item=>`<option value="${esc(item.id)}" ${item.id===workflow.id?'selected':''}>${esc(item.name)}</option>`).join('')}</select></label>
        <label>Название<input id="workflow-name" maxlength="200" value="${esc(workflow.name)}" required></label>
        <label>Описание<textarea id="workflow-description" rows="3" maxlength="4096" placeholder="Когда использовать этот поток и какой результат он выдаёт">${esc(workflow.description)}</textarea></label>
        <div class="workflow-split"><section class="workflow-steps"><header><strong>Этапы</strong><small>Перетаскивайте или используйте стрелки</small></header>${(workflow.steps||[]).map((step,index)=>`<article class="workflow-step" draggable="true" data-index="${index}" data-step-id="${esc(step.id)}"><header><span>${index+1}</span><strong>${esc(step.name||`Этап ${index+1}`)}</strong><div><button type="button" data-action="move-workflow-step" data-index="${index}" data-direction="-1" ${index===0?'disabled':''}>↑</button><button type="button" data-action="move-workflow-step" data-index="${index}" data-direction="1" ${index===workflow.steps.length-1?'disabled':''}>↓</button><button type="button" data-action="remove-workflow-step" data-index="${index}" ${workflow.steps.length<=1?'disabled':''}>×</button></div></header><label>Название этапа<input data-field="step-name" maxlength="200" value="${esc(step.name)}" required></label><div class="settings-grid"><label>Вид<select data-field="step-kind"><option value="agent" ${step.kind==='agent'||step.kind==='cursor'?'selected':''}>Агент Point</option><option value="manual" ${step.kind==='manual'?'selected':''}>Ручной</option></select></label><label>Агент<select data-field="step-profile"><option value="">— выбрать —</option>${profiles.map(profile=>`<option value="${esc(profile.id)}" ${profile.id===step.profileId?'selected':''}>${esc(profile.name)} · ${esc(profile.model)}</option>`).join('')}</select></label></div><label>Инструкция этапа<textarea data-field="step-instruction" rows="3" maxlength="16384" placeholder="Что должен сделать этап">${esc(step.instruction)}</textarea></label><div class="workflow-context-options"><label><input data-field="step-original" type="checkbox" ${step.includeOriginalContext?'checked':''}> Исходный контекст</label><label><input data-field="step-previous" type="checkbox" ${step.includePreviousResult?'checked':''}> Результат прошлого</label></div><details><summary>Условия и сбой</summary><label>Тип условия<select data-field="step-condition-type"><option value="" ${!step.condition?.type?'selected':''}>Нет</option><option value="always" ${step.condition?.type==='always'?'selected':''}>Всегда</option><option value="previous_status" ${step.condition?.type==='previous_status'?'selected':''}>Статус предыдущего</option></select></label><label>Значение<input data-field="step-condition-value" value="${esc(step.condition?.value||'')}" placeholder="completed / failed"></label><label>При сбое<select data-field="step-on-failure"><option value="stop" ${(step.onFailure||'stop')==='stop'?'selected':''}>Остановить кампанию</option><option value="skip" ${step.onFailure==='skip'?'selected':''}>Пропустить и продолжить</option></select></label></details></article>`).join('')}</section><aside class="workflow-visual"><strong>Живая линия</strong>${workflowTimeline({status:'pending',currentStep:1,stepRuns:(workflow.steps||[]).map((step,index)=>({stepName:step.name||`Этап ${index+1}`,profileName:step.kind==='manual'?'вручную':profiles.find(p=>p.id===step.profileId)?.name||'не выбран',status:index===0?'running':'pending'}))})}</aside></div>
        <div class="profile-footer">${creating?'<button type="button" class="secondary" data-action="cancel-workflow-edit">Отмена</button>':`<button type="button" class="danger-button" data-action="delete-workflow" data-id="${esc(workflow.id)}">Удалить</button>`}<button class="primary save" type="submit">${creating?'Создать сценарий':'Сохранить сценарий'}</button></div>
      </form>
      ${!creating?`<section class="workflow-launch"><header><strong>Начать кампанию</strong><small>${invalidSteps.length?`Нужно назначить агентов: ${invalidSteps.map(step=>step.name).join(', ')}`:'Проверка готова: контекст и этапы будут передаваться по правилам.'}</small></header>${keyProfiles.map(profile=>`<label>Ключ API · ${esc(profile.name)}<input class="workflow-api-key" data-profile-id="${esc(profile.id)}" type="password" autocomplete="off" placeholder="Только в памяти"></label>`).join('')}<form id="workflow-run-form"><textarea id="workflow-task" rows="4" placeholder="Общая цель кампании" ${active&&['running','waiting_approval'].includes(active.status)?'disabled':''}></textarea><button class="primary" type="submit" ${invalidSteps.length||active&&['running','waiting_approval'].includes(active.status)?'disabled':''}>Начать · ${workflow.steps.length} этапа</button></form></section>`:''}
      ${workflowRuns.length?`<section class="workflow-history"><header><strong>Последние запуски</strong></header>${workflowRuns.slice(0,8).map(run=>`<button data-action="load-workflow-run" data-id="${esc(run.id)}"><span>${status(run.status)}</span><strong>${esc(run.snapshot?.workflow?.name||run.workflowId)}</strong><small>${formatDateTime(run.startedAt)}</small></button>`).join('')}</section>`:''}
      
    </main>`)
  }

  return {
    profileEditor,
    newProfile,
    currentFormProfile,
    cloneCustomTool,
    currentCustomToolForm,
    resetCustomToolPreview,
    invalidateCustomToolPreview,
    toolNetworkPolicyCallout,
    customToolFormIssue,
    toolSandbox,
    toolBuilder,
    newWorkflowStep,
    newWorkflow,
    workflowTemplates,
    workflowFromTemplate,
    currentWorkflowForm,
    FLOW_NODE_KINDS,
    flowNodeKindLabels,
    newFlowNode,
    flowNodePosition,
    activeFlowRun,
    mergeCandidateLabel,
    flowMergeConflictPanelHtml,
    flowRuntimeSummaryHtml,
    captureFlowForm,
    flowEdgeOptions,
    flowCanvasHtml,
    applyFlowNodePlacement,
    flowInspectorHtml,
    visualFlowBuilder,
    flowsView,
    workflowTimeline,
    workflowBuilder,
  }
}
