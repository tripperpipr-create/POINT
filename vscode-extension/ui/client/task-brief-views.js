import { masterPlanHtml } from './master-plan-views.js'

function taskBriefEditorHtml(item, esc) {
  const b = item.brief
  const field = name => `data-brief-field="${name}" data-id="${esc(item.id)}"`
  const area = (name, label, value) => `<label>${label}<textarea rows="3" ${field(name)}>${esc(value || '')}</textarea></label>`
  const select = (name, label, value, options) => `<label>${label}<select ${field(name)}>${options.map(([key, text]) => `<option value="${key}" ${key === value ? 'selected' : ''}>${text}</option>`).join('')}</select></label>`
  return `<div class="proposal-editor">
    ${select('mode', 'Режим работы', b.mode, [['undecided', 'Нужно уточнить'], ['precise', 'Точное поручение'], ['project', 'Автономный проект']])}
    ${select('resultKind', 'Результат', b.resultKind, [['', 'Нужно уточнить'], ['code', 'Код в ответе'], ['report', 'Отчёт'], ['workspace_change', 'Изменения проекта'], ['hub_tool', 'Исходники инструмента']])}
    ${area('goal', 'Цель', b.goal)}
    ${area('scope', 'Входит в задание — по одному пункту на строку', (b.scope || []).join('\n'))}
    ${area('outOfScope', 'Не входит в задание', (b.outOfScope || []).join('\n'))}
    ${area('openQuestions', 'Нерешённые вопросы — блокируют запуск', (b.openQuestions || []).join('\n'))}
    ${(b.criteria || []).map((c, i) => `<label>Критерий ${i + 1} · ${c.kind === 'manual' ? 'оценка пользователя' : c.kind === 'reproduction' ? 'воспроизведение' : 'проверка'}<textarea rows="2" ${field(`criterion-${i}`)}>${esc(c.text)}</textarea></label>${c.tool === 'run_command' ? `<label>Команда проверки<input ${field(`command-${i}`)} value="${esc(c.arguments?.command || '')}"></label>` : ''}`).join('')}
    <label><input type="checkbox" ${field('writeFiles')} ${b.permissions?.writeFiles ? 'checked' : ''}>Разрешить изменение файлов в sandbox</label>
    <label><input type="checkbox" ${field('executeCommands')} ${b.permissions?.executeCommands ? 'checked' : ''}>Разрешить выполнение команд</label>
    <label><input type="checkbox" ${field('provisionProjectAgents')} ${b.permissions?.provisionProjectAgents ? 'checked' : ''}>Разрешить временных субагентов под выбранным агентом</label>
    ${area('networkHosts', 'Сетевые назначения — по одному хосту на строку', (b.permissions?.networkHosts || []).join('\n'))}
    <label>Предел токенов<input type="number" min="1" ${field('tokens')} value="${Number(b.budget?.tokens || 200000)}"></label>
    <label>Денежный бюджет, центы<input type="number" min="0" ${field('costCents')} value="${Number(b.budget?.costCents || 0)}"></label>
    <label>Предел активной работы, минут<input type="number" min="1" ${field('activeMinutes')} value="${Number(b.budget?.activeSeconds || 3600) / 60}"></label>
    <label>Параллельных исполнителей<input type="number" min="1" max="8" ${field('maxParallel')} value="${Number(b.budget?.maxParallel || 2)}"></label>
    <label>Попыток этапа<input type="number" min="1" ${field('maxAttempts')} value="${Number(b.budget?.maxAttempts || 3)}"></label>
    <label>Перепланирований<input type="number" min="0" ${field('maxReplans')} value="${Number(b.budget?.maxReplans || 6)}"></label>
    <label>Лимит project-local агентов<input type="number" min="0" max="8" ${field('maxProjectAgents')} value="${Number(b.budget?.maxProjectAgents || 0)}"></label>
    <small>Изменения сохраняются новой версией. После сохранения проверьте карточку перед запуском. Состав проверок можно уточнить в разговоре с Мастером.</small>
  </div>`
}

export function readTaskBriefEditor(root, item) {
  const b = structuredClone(item.brief)
  const input = name => root.querySelector(`[data-brief-field="${name}"][data-id="${CSS.escape(item.id)}"]`)
  const value = name => input(name)?.value?.trim() || ''
  const lines = name => value(name).split(/\r?\n/).filter(Boolean)
  b.mode = value('mode'); b.resultKind = value('resultKind'); b.goal = value('goal')
  b.scope = lines('scope'); b.outOfScope = lines('outOfScope'); b.openQuestions = lines('openQuestions')
  b.criteria = (b.criteria || []).map((c, i) => ({ ...c, text: value(`criterion-${i}`), ...(input(`command-${i}`) ? { arguments: { ...c.arguments, command: value(`command-${i}`) } } : {}) }))
  b.permissions = {
    ...b.permissions,
    writeFiles: Boolean(input('writeFiles')?.checked),
    executeCommands: Boolean(input('executeCommands')?.checked),
    provisionProjectAgents: Boolean(input('provisionProjectAgents')?.checked),
    networkHosts: lines('networkHosts'),
  }
  b.budget = {
    ...b.budget,
    tokens: Number(value('tokens')),
    costCents: Number(value('costCents') || b.budget?.costCents || 0),
    activeSeconds: Math.round(Number(value('activeMinutes')) * 60),
    maxParallel: Number(value('maxParallel') || b.budget?.maxParallel || 2),
    maxAttempts: Number(value('maxAttempts') || b.budget?.maxAttempts || 3),
    maxProjectAgents: Number(value('maxProjectAgents') || b.budget?.maxProjectAgents || 0),
    maxReplans: Number(value('maxReplans') || b.budget?.maxReplans || 0),
  }
  b.state = b.openQuestions.length || b.mode === 'undecided' ? 'discussion' : 'ready'
  delete b.approvedVersion; delete b.approvedDigest
  return { brief: b, expectedVersion: item.brief.version }
}

// Условия готовности как перечень. Состояний до старта нет — планировщик
// работает при запуске квеста, — поэтому все пункты ждут, и назван перечень
// условиями, а не планом: обещать прогресс там, где его неоткуда взять, нельзя.
const CRITERION_KIND = { manual: 'оценивает пользователь', reproduction: 'воспроизведение' }

function briefCriteriaPlanHtml(brief, esc) {
  const criteria = Array.isArray(brief?.criteria) ? brief.criteria : []
  return masterPlanHtml('Условия готовности', criteria.map(criterion => ({
    text: criterion.text,
    state: 'wait',
    note: CRITERION_KIND[criterion.kind] || 'машинная проверка',
  })), esc)
}

function boundedPlanHtml(item, esc) {
  const stages = item?.planPreview || []
  const breakdown = item?.selectionBreakdown || []
  const stageLines = stages.map(stage => {
    const assignment = [stage.runtime, stage.model].filter(Boolean).join(' · ') || 'модель на старте'
    const cost = stage.estimatedCostCents > 0 ? `${stage.estimatedCostCents}¢` : (stage.pricingKnown ? '0¢' : 'цена не нулевая — нужен резерв')
    const scope = (stage.ownedPaths || []).join(', ')
    return `<li><b>${esc(stage.name || 'этап')}</b> · ${esc(assignment)} · ${esc(cost)}${scope ? ` · ${esc(scope)}` : ''}</li>`
  }).join('')
  const why = breakdown.map(row => `<li>${esc(row.agentId || '')} · score ${Number(row.total || 0)}${(row.matched || []).length ? ` · ${esc(row.matched.join(', '))}` : ''}</li>`).join('')
  // Пусто — значит пусто. Прежняя заглушка обещала, что «после утверждения
  // Мастер назначит каждому этапу агента, runtime, модель и стоимость», и
  // занимала место там, где теперь стоит перечень условий готовности: обещание
  // не рассказывает об этом задании ничего.
  return `${stageLines ? `<div class="hall-dod"><b>Этапы</b><ul>${stageLines}</ul></div>` : ''}${why ? `<div class="hall-dod"><b>Почему эти агенты</b><ul>${why}</ul></div>` : ''}`
}

// Готовность задания считают в одном месте. Её читают четверо: шапка карточки,
// шлюз ленты, признак на вкладке разговора и кнопка запуска. Три независимые
// копии предиката расходятся при первой же правке — и тогда вкладка говорит
// «обсуждение» над карточкой с рабочей кнопкой «Утвердить и запустить».
export function taskBriefReady(brief) {
  return ['ready', 'approved'].includes(brief?.state) && brief?.mode !== 'undecided' && !(brief?.openQuestions || []).length
}

// Нужна ли подготовка исполнителя перед запуском.
//
// Ядро добавляет решение «Подготовка исполнителя», когда на момент постановки в
// ростере нет готового агента. Но задание — снимок, а ростер живёт дальше: агента
// распускают, он теряет готовность, и сохранённая карточка продолжает обещать
// «готово к запуску», называя исполнителем того, кого уже нет. Поэтому решает
// не только снимок, но и ростер на момент показа — как и статус предложения,
// который карточка тоже берёт из хранилища, а не из ответа хода.
function briefNeedsPreparation(brief, rosterReady) {
  if (rosterReady === false) return true
  return (brief?.decisions || []).some(d => d.topic === 'Подготовка исполнителя')
}

export function taskBriefStateLabel(brief, rosterReady) {
  if (!taskBriefReady(brief)) return 'обсуждение'
  return briefNeedsPreparation(brief, rosterReady)
    ? 'готово после подготовки исполнителя'
    : 'готово к запуску'
}

// Состав задания отделён от действий над ним: то же самое показывает правая
// панель разговора, где кнопки запуска нет — пока задание обсуждают, принимать
// нечего. Второй разметкой это быть не может: две копии одного состава
// однажды разойдутся, и человек прочтёт разное в панели и в ленте.
export function taskBriefBodyHtml(item, { esc, countOf, editing, editor, rosterReady }) {
  const b = item.brief
  const list = (title, values) => values?.length ? `<div class="hall-dod"><b>${title}</b><ul>${values.map(v => `<li>${esc(v)}</li>`).join('')}</ul></div>` : ''
  const preparation = (b.decisions || []).filter(d => d.topic === 'Подготовка исполнителя').map(d => d.decision)
  return `<header><b>${esc(({precise: 'Точное поручение', project: 'Автономный проект', undecided: 'Уточнение режима'})[b.mode] || b.mode)}</b><small>Версия ${Number(b.version)} · ${taskBriefStateLabel(b, rosterReady)}</small></header>
    <div class="hall-panel-row is-stack"><span class="hall-lead">${esc(b.goal)}</span>
    <small>Результат: ${esc(({code:'код в ответе', report:'отчёт', workspace_change:'изменения проекта', hub_tool:'исходники инструмента'})[b.resultKind] || 'уточняется')}</small>
    ${/* Из чего состоит работа и по чему её примут — в теле карточки, а не за
         раскрывашкой. Одно свёрнутое «Состав задания» прятало всё сразу: и
         границы задания, и условия готовности, и нерешённые вопросы, которые
         запуск блокируют. Под раскрывашкой остаются рамки — разрешения, бюджет
         и разбор подбора: это про то, как работа пойдёт, а не про то, что в ней. */''}
    ${briefCriteriaPlanHtml(b, esc)}
    ${list('Перед выполнением', preparation)}
    ${list('Входит в задание', b.scope)}${list('Не входит в задание', b.outOfScope)}
    ${list('Нужно уточнить', b.openQuestions)}
    <details class="hall-proposal-more"><summary>Рамки и подбор</summary>
    ${list('Согласованные решения', (b.decisions || []).map(d => `${d.topic}: ${d.decision}`))}
    <small>Разрешения: файлы ${b.permissions?.writeFiles ? 'можно изменять в sandbox' : 'только чтение'}; команды ${b.permissions?.executeCommands ? 'разрешены в рамках профиля' : 'запрещены'}${b.permissions?.provisionProjectAgents ? '; разрешён временный субагент под выбранным агентом' : ''}. Сеть: ${esc((b.permissions?.networkHosts || []).join(', ') || 'не разрешена')}.</small>
    <small>Бюджет: ${Number(b.budget?.tokens || 0).toLocaleString('ru-RU')} токенов${b.budget?.costCents ? ` · ${Number(b.budget.costCents)}¢` : ''} · ${Number(b.budget?.activeSeconds || 0) / 60} минут активной работы · до ${Number(b.budget?.maxParallel || 1)} параллельно · до ${countOf(Number(b.budget?.maxAttempts || 1), 'попытки', 'попыток', 'попыток')} · до ${countOf(Number(b.budget?.maxReplans || 0), 'перепланирования', 'перепланирований', 'перепланирований')}.</small>
    ${boundedPlanHtml(item, esc)}
    </details>
    ${editing ? editor : ''}</div>`
}

export function taskBriefActionsHtml(item, { esc, busy, editing, withStart, rosterReady }) {
  const b = item.brief
  const ready = taskBriefReady(b)
  const needsPreparation = briefNeedsPreparation(b, rosterReady)
  const startLabel = needsPreparation ? 'Утвердить и подготовить исполнителя' : b.mode === 'project' ? 'Утвердить и запустить' : 'Выполнить поручение'
  return `<div class="hall-panel-row hall-actions">
    ${withStart ? `<button class="hall-btn is-primary" data-action="quest-proposal-start" data-id="${esc(item.id)}" ${busy || editing || !ready ? 'disabled' : ''}>${busy ? 'Обработка…' : startLabel}</button>` : ''}
    <button class="hall-btn" data-action="quest-proposal-modify" data-id="${esc(item.id)}" ${busy ? 'disabled' : ''}>${editing ? 'Сохранить' : 'Изменить'}</button>
    <button class="hall-btn" data-action="quest-proposal-discuss" data-id="${esc(item.id)}" ${busy ? 'disabled' : ''}>Продолжить обсуждение</button>
    <button class="hall-btn" data-action="quest-proposal-ignore" data-id="${esc(item.id)}" ${busy ? 'disabled' : ''}>Отклонить</button></div>`
}

export function taskBriefCardHtml(item, opts) {
  return `<section class="hall-panel hall-proposal">${taskBriefBodyHtml(item, opts)}${taskBriefActionsHtml(item, { ...opts, withStart: true })}</section>`
}

export function proposalEditorHtml(view, agents, flows, esc, agentClass) {
  if (view.brief) return taskBriefEditorHtml(view, esc)
  const agentOptions = agents.map(agent => `<label><input type="checkbox" data-proposal-field="team" data-id="${esc(view.id)}" value="${esc(agent.id)}" ${(view.teamAgentIds || []).includes(agent.id) ? 'checked' : ''}><span><b>${esc(agent.name)}</b><small>${esc(agent.roleDescription || agentClass(agent))}</small></span></label>`).join('')
  const flowOptions = flows.map(flow => `<option value="${esc(flow.id)}" ${flow.id === view.flowId ? 'selected' : ''}>${esc(flow.name)}</option>`).join('')
  return `<div class="proposal-editor">
    <label>Название<input data-proposal-field="title" data-id="${esc(view.id)}" maxlength="160" value="${esc(view.title)}"></label>
    <label>Важность<select data-proposal-field="importance" data-id="${esc(view.id)}"><option value="normal" ${view.importance === 'normal' ? 'selected' : ''}>Обычный</option><option value="important" ${view.importance === 'important' ? 'selected' : ''}>Важный</option><option value="critical" ${view.importance === 'critical' ? 'selected' : ''}>Критический</option></select></label>
    <label>Цели — по одной на строку<textarea rows="4" data-proposal-field="objectives" data-id="${esc(view.id)}">${esc((view.objectives || []).join('\n'))}</textarea></label>
    <label>Ограничения — по одному на строку<textarea rows="3" data-proposal-field="constraints" data-id="${esc(view.id)}">${esc((view.constraints || []).join('\n'))}</textarea></label>
    <label>Готово, когда — по одному на строку<textarea rows="4" data-proposal-field="definitionOfDone" data-id="${esc(view.id)}">${esc((view.definitionOfDone || []).join('\n'))}</textarea></label>
    <label>Flow<select data-proposal-field="flow" data-id="${esc(view.id)}"><option value="">Авто по важности</option>${flowOptions}</select></label>
    <fieldset><legend>Отряд</legend><small>Отметьте исполнителей вручную или снимите все флажки, чтобы Мастер подобрал состав при запуске.</small><div class="check-grid proposal-team-grid">${agentOptions || '<small>Нет доступных агентов</small>'}</div></fieldset>
  </div>`
}
export function readProposalDecisionEditor(root, id, item) {
  if (item?.brief) return readTaskBriefEditor(root, item)
  const selector = field => `[data-proposal-field="${field}"][data-id="${CSS.escape(id)}"]`
  const lines = field => (root.querySelector(selector(field))?.value || '').split(/\r?\n/).map(value => value.trim()).filter(Boolean)
  return {
    title: root.querySelector(selector('title'))?.value?.trim(),
    importance: root.querySelector(selector('importance'))?.value,
    objectives: lines('objectives'),
    constraints: lines('constraints'),
    definitionOfDone: lines('definitionOfDone'),
    teamAgentIds: [...root.querySelectorAll(`${selector('team')}:checked`)].map(input => input.value),
    flowId: root.querySelector(selector('flow'))?.value || '',
  }
}
