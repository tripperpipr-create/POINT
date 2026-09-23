// Карточка нового исполнителя — отдельная запись ленты Мастера.
//
// Создание агента было разорвано на три куска, и ни один не был карточкой
// ленты. Предложение ядра рисовалось классами чужого регистра
// (`quest-proposal-card companion-action-card`), для которых в `.hall-dialogue`
// нет ни одного правила: ни меры колонки, ни геометрии Чертога, ни кнопок
// Чертога — вместо настроек три плашки с сырым blueprintId и числом умений.
// Согласие на создание сидело ярусом внутри карточки запуска, а карточка найма
// показывала три поля и кнопку «Полная форма», уводившую на другую вкладку.
//
// Здесь всё это сходится в один вид: одна карточка, один набор действий, один
// разбор формы. Источников по-прежнему три, но форма карточки у них общая —
// человеку безразлично, из ответа хода пришёл черновик или из ростера наряда.
//
// Состояние живёт здесь же, как у карточки найма и карточки запуска: main.js
// стоит у своей границы в 6900 строк, и вид владеет своим раскрытием сам.

import { list, shortLabel } from './format-units.js'
import { masterCardMoreAttrs } from './master-card-open.js'
import { questMenuHtml } from './master-quest-views.js'
import { EFFORT_OPTIONS, agentGearHtml, agentPortraitHtml, agentStatsHtml } from './master-agent-sheet.js'
import { masterAgentBusy, masterAgentConsent, masterAgentDrafts, masterAgentErrors } from './master-agent-card-state.js'
export { masterAgentBusy, masterAgentConsent, masterAgentDrafts, masterAgentErrors, releaseMasterAgentCards } from './master-agent-card-state.js'

const text = value => String(value ?? '').trim()

// Набор умений по умолчанию совпадает с конструктором (agent-constructor.js):
// исполнитель, заведённый из ленты, не должен отличаться от заведённого в
// мастерской — иначе один и тот же человек получает двух разных агентов в
// зависимости от того, каким путём шёл.
const DEFAULT_TOOLS = ['project_map', 'search_code', 'list_files', 'read_file', 'search_text', 'git_diff']
const POLICY_OPTIONS = [['ALLOW', 'можно'], ['ASK', 'спросить'], ['DENY', 'нельзя']]
const APPROVAL_OPTIONS = [['safe', 'спрашивать про опасное'], ['always', 'спрашивать про всё']]

function normalizeRisk(value) {
  const risk = String(value || '').toUpperCase()
  return ['LOW', 'MEDIUM', 'HIGH', 'CRITICAL'].includes(risk) ? risk : 'MEDIUM'
}

function defaultPolicy(risk) {
  return normalizeRisk(risk) === 'LOW' ? 'ALLOW' : 'ASK'
}

function baseFromAgent(agent, fallbackModel) {
  const tools = list(agent?.allowedTools)
  return {
    name: text(agent?.name),
    role: text(agent?.roleDescription || agent?.role),
    mission: text(agent?.mission),
    blueprintId: text(agent?.blueprintId),
    // Класс и послужной список ведёт ядро: roleFamily ставит комплектовщик,
    // уровень и опыт — завершение прогона (flow_completion.go). Здесь они
    // только читаются; у черновика наряда их нет, и лист про них молчит.
    roleFamily: text(agent?.roleFamily),
    level: Number(agent?.level) || 0,
    experience: Number(agent?.experience) || 0,
    tasksCompleted: Number(agent?.tasksCompleted) || 0,
    successCount: Number(agent?.successCount) || 0,
    allowedTools: tools.length ? tools : [...DEFAULT_TOOLS],
    toolPolicies: { ...(agent?.toolPolicies || {}) },
    connectionId: text(agent?.connectionId),
    model: text(agent?.primaryModel || agent?.model || fallbackModel),
    reasoningEffort: text(agent?.reasoningEffort) || 'none',
    approvalMode: text(agent?.approvalMode) || 'safe',
    maxSteps: Number(agent?.maxSteps) || 30,
    maxDurationSeconds: Number(agent?.maxDurationSeconds) || 600,
  }
}

// Предложение ядра. Черновик здесь полный — ядро собрало его из чертежа, — и
// карточке остаётся показать то, что в нём есть, а не пересказывать его тремя
// плашками.
export function masterAgentCardFromAction(item) {
  if (!item || item.kind !== 'create_agent') return undefined
  return {
    id: 'action:' + text(item.id),
    kind: 'action',
    proposalId: text(item.id),
    why: text(item.rationale),
    blueprints: [],
    base: baseFromAgent(item.agent, ''),
  }
}

// Черновики нарядов и пробелы ростера. Утверждённый наряд карточки не получает:
// состав там уже зафиксирован согласием, и звать человека заводить исполнителя
// после запуска — значит предлагать менять то, что уже работает.
// Наряд на уточнении — тоже: карточка с ролью, моделью и снаряжением вставала
// рядом с неотвеченным вопросом, под задание, которого ещё нет. От ответа
// меняется состав наряда, а с ним и то, кто нужен. `ready` — «всё собрано».
const orderCollected = order => order?.state === 'staffing'

export function masterAgentCardsFor(masterData) {
  const hiring = list(masterData?.hiring)
  const orders = list(masterData?.workOrders)
  const cards = []
  for (const order of orders) {
    if (!orderCollected(order)) continue
    const created = new Set(list(order.runtime?.agentIds))
    const card = hiring.find(item => item?.workOrderId === order.id)
    for (const draft of list(order.roster?.permanent)) {
      if (!draft || draft.existing || created.has(draft.id) || !draft.requiresConsent) continue
      cards.push({
        id: 'order:' + text(order.id) + ':' + text(draft.id),
        kind: 'work-order',
        workOrderId: text(order.id),
        draftId: text(draft.id),
        goal: text(order.goal),
        why: text(card?.reason),
        blueprints: list(card?.blueprints),
        maxAgents: Number(card?.maxAgents) || 0,
        base: baseFromAgent(
          { ...draft, roleDescription: draft.role, allowedTools: draft.requiredTools },
          order.routing?.fixedModel || order.routing?.routerModel,
        ),
      })
    }
  }
  // Пробел ростера, до наряда ещё не доехавший: наблюдатель уже сочинил
  // черновик, и заводить исполнителя можно, не дожидаясь правки наряда.
  // Собранность всё равно считается по наряду: черновик говорит, кого не
  // хватает, и молчит о том, решено ли задание.
  for (const item of hiring) {
    if (!item?.draft || cards.some(card => card.workOrderId === item.workOrderId)) continue
    if (!orderCollected(orders.find(order => text(order?.id) === text(item.workOrderId)))) continue
    cards.push({
      id: 'hiring:' + text(item.workOrderId),
      kind: 'work-order',
      workOrderId: text(item.workOrderId),
      draftId: text(item.draft.id),
      goal: text(item.goal),
      why: text(item.reason),
      blueprints: list(item.blueprints),
      maxAgents: Number(item.maxAgents) || 0,
      base: baseFromAgent({ ...item.draft, roleDescription: item.draft.role, allowedTools: item.draft.requiredTools }, ''),
    })
  }
  return cards
}

// Все карточки разом — для диспетчера действий: нажатие приходит в main.js с
// одним идентификатором карточки, и по нему надо найти саму карточку, где бы
// она ни рисовалась — в слоте ленты или при своей реплике.
export function masterAgentCardsAll(masterData, actionProposals) {
  const cards = masterAgentCardsFor(masterData)
  for (const item of list(actionProposals)) {
    if (item?.status && item.status !== 'pending' && item.status !== 'modified') continue
    const card = masterAgentCardFromAction(item)
    if (card && !cards.some(existing => existing.id === card.id)) cards.push(card)
  }
  return cards
}

// Что уйдёт в ядро: черновик карточки плюс правки человека. Политики умений
// сливаются по ключам, а не целиком: в черновике может стоять политика сети,
// которую карточка не показывает, и замена объекта молча снимала бы её.
export function masterAgentValue(card) {
  const patch = masterAgentDrafts.get(card?.id) || {}
  const base = card?.base || {}
  return {
    ...base,
    ...patch,
    toolPolicies: { ...(base.toolPolicies || {}), ...(patch.toolPolicies || {}) },
    allowedTools: list(patch.allowedTools || base.allowedTools),
  }
}

// Отказ должен быть назван словами и до отправки. Прежняя форма отправляла
// пустое в ядро и возвращала английскую ошибку валидации.
export function masterAgentIssue(value, connections) {
  if (!text(value.name)) return 'Дайте исполнителю имя — без него его не отличить в ростере.'
  if (!text(value.role)) return 'Назовите роль: по ней Мастер решает, что ему поручать.'
  if (!list(value.allowedTools).length) return 'Отметьте хотя бы одно умение — без них исполнитель ничего не сможет.'
  if (!text(value.model) && !list(connections).length) return 'Заведите подключение в разделе «Связи» — исполнителю нечем думать.'
  if (!text(value.model)) return 'Укажите модель — подключение без неё не знает, кого спрашивать.'
  return ''
}

function fieldsHtml(card, value, esc) {
  return `<div class="master-agent-identity">
      <label><span>Имя</span><input data-agent-field="name" maxlength="120" value="${esc(value.name)}" placeholder="Как звать исполнителя"></label>
      <label><span>Роль</span><input data-agent-field="role" maxlength="200" value="${esc(value.role)}" placeholder="В чём он эксперт"></label>
    </div>
    <label class="master-agent-mission"><span>За что отвечает</span><textarea data-agent-field="mission" rows="2" maxlength="4096" placeholder="Постоянная задача в каждом квесте">${esc(value.mission)}</textarea></label>`
}

function brainHtml(card, value, esc, deps) {
  const connections = list(deps.connections)
  const chosen = connections.find(item => item.id === value.connectionId)
    || connections.find(item => item.isDefault) || connections[0]
  const catalog = list(chosen?.models).slice(0, 200)
  const label = deps.connectionLabel || (item => item.displayName || item.presetId || item.provider)
  const options = connections.length
    ? connections.map(item => `<option value="${esc(item.id)}"${item.id === (chosen?.id || '') ? ' selected' : ''}>${esc(label(item))}${item.isDefault ? ' · по умолчанию' : ''}</option>`).join('')
    : '<option value="">Подключений ещё нет</option>'
  const listId = 'models-' + String(card.id).replace(/[^a-zA-Z0-9_-]/g, '-')
  return `<div class="master-agent-brain">
      <label><span>Подключение</span><select data-agent-field="connectionId"${connections.length ? '' : ' disabled'}>${options}</select></label>
      <label><span>Модель</span><input data-agent-field="model" list="${esc(listId)}" value="${esc(value.model)}" placeholder="${esc(chosen?.defaultModel || 'Точный model ID')}"><datalist id="${esc(listId)}">${catalog.map(item => `<option value="${esc(item.id)}"></option>`).join('')}</datalist></label>
    </div>`
}

function toolsHtml(value, esc, deps) {
  const catalog = list(deps.toolCatalog)
  const allowed = new Set(list(value.allowedTools))
  // Умения, которых нет в каталоге, всё равно показываются: ядро могло назвать
  // в черновике пользовательский инструмент, и молча снять его с исполнителя
  // карточка не вправе.
  const known = new Set(catalog.map(item => item.name))
  const extra = [...allowed].filter(name => !known.has(name)).map(name => ({ name, displayName: name, risk: 'MEDIUM' }))
  const items = catalog.concat(extra)
  if (!items.length) return ''
  const row = tool => {
    const on = allowed.has(tool.name)
    const policy = value.toolPolicies?.[tool.name] || defaultPolicy(tool.risk)
    const title = tool.displayName || tool.name
    return `<label class="master-agent-tool${on ? ' is-on' : ''}">
      <input type="checkbox" data-agent-field="tool" value="${esc(tool.name)}"${on ? ' checked' : ''}>
      <b>${esc(title)}</b>
      <select data-agent-field="policy" data-tool="${esc(tool.name)}" aria-label="Права на «${esc(title)}»"${on ? '' : ' disabled'}>${POLICY_OPTIONS.map(([key, label]) => `<option value="${key}"${key === policy ? ' selected' : ''}>${label}</option>`).join('')}</select>
    </label>`
  }
  return `<div class="master-agent-grants">${items.map(row).join('')}</div>`
}

function limitsHtml(value, esc) {
  return `<div class="master-agent-limits">
      <label><span>Подтверждения</span><select data-agent-field="approvalMode">${APPROVAL_OPTIONS.map(([key, label]) => `<option value="${key}"${key === value.approvalMode ? ' selected' : ''}>${label}</option>`).join('')}</select></label>
      <label><span>Рассуждение</span><select data-agent-field="reasoningEffort">${EFFORT_OPTIONS.map(([key, label]) => `<option value="${key}"${key === (value.reasoningEffort || 'none') ? ' selected' : ''}>${label}</option>`).join('')}</select></label>
      <label><span>Предел ходов</span><input type="number" min="1" max="100" data-agent-field="maxSteps" value="${Number(value.maxSteps) || 30}"></label>
      <label><span>Предел работы, секунд</span><input type="number" min="60" max="86400" step="60" data-agent-field="maxDurationSeconds" value="${Number(value.maxDurationSeconds) || 600}"></label>
    </div>`
}

const shortName = name => shortLabel(name, 24)

// Чертёж и есть класс: с него снимают роль, умения и модель разом. Отмечаем
// выбранный — иначе ряд читается списком предложений, а не выбором из них.
function blueprintsHtml(card, value, esc) {
  const blueprints = list(card.blueprints).slice(0, 3)
  if (!blueprints.length) return ''
  const buttons = blueprints.map(item => {
    const on = Boolean(value.blueprintId) && value.blueprintId === item.blueprintId
    return `<button type="button" class="hall-btn is-sm" data-action="agent-card-blueprint" data-card="${esc(card.id)}" data-template="${esc(item.blueprintId)}" aria-pressed="${on ? 'true' : 'false'}" title="${esc(item.why || item.name)}">${esc(shortName(item.name))}</button>`
  }).join('')
  return `<div class="master-agent-blueprints"><span class="master-agent-rubric">класс — чертёж, с которого его снимают</span><div class="master-agent-slots">${buttons}</div></div>`
}

// Лист персонажа. Форма взята из макета «Квесты в чате» (вариант 3a): портрет
// и класс слева, характеристики полосками, снаряжение слотами. Цвета при
// переносе не брались ни одного — в макете система моно-акцентная и заводит
// свои статусные оттенки, а у продукта они уже названы.
//
// Прежде здесь стояла анкета: восемь подписанных полей подряд одним весом. По
// ней нельзя было за секунду понять, кого заводят, — а решение ровно об этом.
// Поля никуда не делись (без них создавать нечего), но перестали быть всем
// содержанием карточки: сверху виден исполнитель, а не бланк.
//
// Ряд решения — одна набранная кнопка и меню, как у карточки квеста: пока все
// четыре стояли в ряд одним весом, глаз выбирал крайнюю левую.
export function masterAgentCardHtml(card, esc, deps = {}) {
  if (!card) return ''
  const value = masterAgentValue(card)
  const busy = masterAgentBusy.has(card.id)
  const consented = card.kind === 'work-order' && masterAgentConsent.has(card.workOrderId)
  const issue = masterAgentErrors.get(card.id) || ''
  const why = card.why || (card.goal ? `для задания «${card.goal}»` : '')
  const grants = `умений: ${list(value.allowedTools).length} · ${value.approvalMode === 'always' ? 'спрашивает про всё' : 'спрашивает про опасное'}`
  const menu = questMenuHtml([
    { action: 'agent-card-workshop', card: card.id, label: 'Открыть мастерскую', busy },
    card.kind === 'action' ? { action: 'agent-card-dismiss', card: card.id, label: 'Не создавать', busy } : null,
  ], esc)
  return `<section class="hall-deck master-agent" data-agent-card="${esc(card.id)}">
    <header><span class="hall-quest-kick"><span class="hall-quest-dot" aria-hidden="true"></span>Новый исполнитель</span><small>${esc(why || 'заводите вы — Мастер только предлагает')}</small></header>
    <div class="hall-panel-row master-agent-sheet">
      ${agentPortraitHtml(card, value, esc)}
      <div class="master-agent-main">
        ${fieldsHtml(card, value, esc)}
        ${brainHtml(card, value, esc, deps)}
        ${agentStatsHtml(card, value, esc)}
        ${agentGearHtml(card, value, esc, deps)}
        ${blueprintsHtml(card, value, esc)}
        <details class="master-agent-more"${masterCardMoreAttrs(`agent:${card.id}`, { esc })}>
          <summary>Права и пределы · ${esc(grants)}</summary>
          ${toolsHtml(value, esc, deps)}
          ${limitsHtml(value, esc)}
        </details>
        <p class="master-agent-error${issue ? '' : ' is-hidden'}" role="alert">${esc(issue)}</p>
      </div>
    </div>
    <div class="hall-panel-row hall-actions">
      <div class="hall-quest-acts">
        <button type="button" class="hall-btn is-primary" data-action="agent-card-create" data-card="${esc(card.id)}"${busy ? ' disabled' : ''}>${busy ? 'Заводим…' : 'Создать исполнителя'}</button>
        ${menu}
      </div>
      <small class="hall-fineprint is-trailing">Исполнитель появится в ростере проекта и встанет в это задание.</small>
    </div>
  </section>`
}

export function masterAgentCardsHtml(cards, esc, deps = {}) {
  return list(cards).map(card => masterAgentCardHtml(card, esc, deps)).join('')
}

// Ввод снимается на каждом знаке, но полной перерисовки не вызывает: она
// отобрала бы каретку у поля. Объяснение отказа гасим на месте — тем же
// приёмом, что и счётчик у кнопки отправки ответов.
export function readMasterAgentCardInput(target) {
  const field = target?.dataset?.agentField
  const host = target?.closest?.('[data-agent-card]')
  if (!field || !host) return ''
  const id = String(host.dataset.agentCard || '')
  const patch = { ...(masterAgentDrafts.get(id) || {}) }
  if (field === 'tool') {
    patch.allowedTools = [...host.querySelectorAll('[data-agent-field="tool"]')].filter(item => item.checked).map(item => item.value)
  } else if (field === 'policy') {
    patch.toolPolicies = { ...(patch.toolPolicies || {}) }
    patch.toolPolicies[String(target.dataset.tool || '')] = target.value
  } else if (field === 'maxSteps' || field === 'maxDurationSeconds') {
    patch[field] = Number(target.value) || 0
  } else {
    patch[field] = target.value
  }
  masterAgentDrafts.set(id, patch)
  masterAgentErrors.delete(id)
  const note = host.querySelector('.master-agent-error')
  if (note) {
    note.textContent = ''
    note.classList.add('is-hidden')
  }
  return id
}

function blueprintPatch(blueprint) {
  const tools = list(blueprint?.allowedTools || blueprint?.tools)
  const model = text(blueprint?.primaryModel || blueprint?.model)
  return {
    name: text(blueprint?.name),
    role: text(blueprint?.roleDescription || blueprint?.role),
    mission: text(blueprint?.mission) || text(blueprint?.why),
    blueprintId: text(blueprint?.id || blueprint?.blueprintId),
    ...(tools.length ? { allowedTools: tools } : {}),
    ...(model ? { model } : {}),
  }
}

export function handleMasterAgentCardAction(action, target, ctx) {
  if (!String(action || '').startsWith('agent-card-')) return false
  const id = String(target?.dataset?.card || '')
  const card = list(ctx.cards).find(item => item.id === id)
  if (!card) return true
  // Второе нажатие создаёт второго агента, а не повторяет первого: здесь
  // каждое нажатие порождает новую сущность, и запертой кнопки в разметке для
  // этого мало — ответа ядра ждём заметное время, и за него успевают нажать.
  if (masterAgentBusy.has(id) && (action === 'agent-card-create' || action === 'agent-card-dismiss')) return true
  if (action === 'agent-card-blueprint') {
    const template = String(target.dataset.template || '')
    const blueprint = ctx.blueprintById(template) || list(card.blueprints).find(item => item.blueprintId === template)
    if (blueprint) {
      masterAgentDrafts.set(id, { ...(masterAgentDrafts.get(id) || {}), ...blueprintPatch(blueprint) })
      masterAgentErrors.delete(id)
      ctx.render()
    }
    return true
  }
  // Полоса характеристики и пустой слот снаряжения — не вторые поля, а путь к
  // настоящим. Открываем «Права и пределы» и ставим курсор в то поле, от
  // которого полоса и считается: второй редактор тех же значений однажды уже
  // стоил двойного чтения формы (см. master-brief-panel.js).
  //
  // Без render(): полная отрисовка забрала бы и раскрытие, и курсор. Открытие
  // `details` само доедет до снимка состояния — его пишет обработчик toggle.
  if (action === 'agent-card-open-more') {
    const host = target.closest?.('[data-agent-card]')
    const more = host?.querySelector?.('.master-agent-more')
    if (more) more.open = true
    host?.querySelector?.(`[data-agent-field="${String(target.dataset.field || '')}"]`)?.focus?.()
    return true
  }
  if (action === 'agent-card-later') {
    // Согласие на создание транзакцией утверждения. Само по себе оно ничего не
    // создаёт: наряд остаётся черновиком, а исполнитель появится вместе с
    // запуском — и только после второго нажатия человека в карточке запуска.
    if (card.workOrderId) masterAgentConsent.add(card.workOrderId)
    ctx.render()
    return true
  }
  if (action === 'agent-card-workshop') {
    ctx.openWorkshop(card, masterAgentValue(card))
    return true
  }
  if (action === 'agent-card-dismiss') {
    masterAgentBusy.add(id)
    ctx.vscode.postMessage({ type: 'decideCompanionAction', proposalId: card.proposalId, action: 'ignore', origin: 'master' })
    ctx.render()
    return true
  }
  if (action === 'agent-card-create') {
    const value = masterAgentValue(card)
    const issue = masterAgentIssue(value, ctx.connections())
    if (issue) {
      masterAgentErrors.set(id, issue)
      ctx.render()
      return true
    }
    masterAgentErrors.delete(id)
    masterAgentBusy.add(id)
    if (card.kind === 'action') {
      ctx.vscode.postMessage({
        type: 'decideCompanionAction',
        proposalId: card.proposalId,
        action: 'apply',
        origin: 'master',
        name: value.name,
        roleDescription: value.role,
        mission: value.mission,
        allowedTools: list(value.allowedTools),
        toolPolicies: value.toolPolicies || {},
        connectionId: value.connectionId,
        primaryModel: value.model,
        reasoningEffort: value.reasoningEffort,
        approvalMode: value.approvalMode,
        maxSteps: Number(value.maxSteps) || 0,
        maxDurationSeconds: Number(value.maxDurationSeconds) || 0,
      })
    } else {
      ctx.createAgent(card, value)
    }
    ctx.render()
    return true
  }
  return true
}
