// Карточка найма: кем делать работу, которую описывает карточка запуска.
//
// Раньше в ленте жила карточка «Кого нанять»: один чертёж, выбранный по
// совпадению слов, без готовности, без связи с нарядом и без жизни после
// перезагрузки — ответ хода её не хранил. Наблюдатель ростера считает картину
// целиком, поэтому и показывать её надо целиком: кто уже подходит, кому не
// хватает настройки, из какого чертежа завести исполнителя и чем закрыть роль,
// которой нет.
//
// Создание исполнителя отсюда ушло в свою карточку ленты
// (ui/client/master-agent-card.js): три поля и кнопка «Полная форма», уводившая
// на другую вкладку, не были ни настройкой агента, ни его созданием. Здесь
// осталось то, чем карточка называется: кто уже в наряде, кого рассмотрели и
// какой узкой роли не хватает.
//
// Состояние раскрытия живёт здесь же, как и у карточки запуска: main.js стоит у
// своей границы в 6900 строк, и вид владеет своим раскрытием сам.
import { list, shortLabel } from './format-units.js'

export const masterHiringOpen = new Set()


const READINESS = { READY: 'готов', DEGRADED: 'с оговорками', BLOCKED: 'не готов' }
const STATE_LABELS = {
  ready: 'Состав собран',
  candidate: 'Есть кем заменить',
  blocked: 'Исполнитель не готов',
  blueprint: 'Есть подходящий чертёж',
  create: 'Исполнителя нет',
  subagent: 'Не хватает узкой роли',
}

function toolChips(tools, esc) {
  const shown = list(tools).slice(0, 6)
  if (!shown.length) return ''
  return `<div class="hall-hire-tools">${shown.map(tool => `<span>${esc(tool)}</span>`).join('')}</div>`
}

const shortName = name => shortLabel(name, 28)

function candidateHtml(candidate, esc, workOrderId, { takeable } = {}) {
  const readiness = READINESS[candidate.readiness] || 'готов'
  const matched = list(candidate.matched).slice(0, 4)
  const blocking = list(candidate.blocking).slice(0, 3)
  const action = candidate.readiness === 'BLOCKED'
    ? `<button type="button" class="hall-btn is-sm" data-action="open-agent-constructor-edit" data-id="${esc(candidate.agentId)}">Открыть мастерскую</button>`
    : takeable
      ? `<button type="button" class="hall-btn is-sm" data-action="hire-into-work-order" data-work-order-id="${esc(workOrderId)}" data-agent-id="${esc(candidate.agentId)}" title="Взять «${esc(candidate.name)}» в наряд">Взять в наряд</button>`
      : ''
  return `<article class="hall-hire-candidate is-${esc(String(candidate.readiness || 'ready').toLowerCase())}">
    <div class="hall-hire-who"><b title="${esc(candidate.name)}">${esc(shortName(candidate.name))}</b>${candidate.role ? `<span>${esc(candidate.role)}</span>` : ''}<i>${esc(readiness)}</i></div>
    ${matched.length ? `<p class="hall-hire-why">совпало с задачей: ${esc(matched.join(', '))}</p>` : ''}
    ${candidate.whyNot ? `<p class="hall-hire-why">${esc(candidate.whyNot)}</p>` : ''}
    ${blocking.length ? `<ul class="hall-hire-blocking">${blocking.map(text => `<li>${esc(text)}</li>`).join('')}</ul>` : ''}
    ${action}
  </article>`
}

function subagentHtml(card, esc) {
  if (!card.subagent) return ''
  const disabled = card.allowSubagents ? '' : ' disabled'
  const note = card.allowSubagents
    ? 'Временный помощник работает под своим исполнителем и получает только его права.'
    : 'Создание исполнителей не разрешено в задании — включите его в правах, чтобы добавить помощника.'
  return `<div class="hall-hire-subagent">
    <div class="hall-hire-who"><b>Помощник: ${esc(card.subagent.role || '')}</b>${card.parentName ? `<span>под «${esc(card.parentName)}»</span>` : ''}</div>
    ${card.subagent.mission ? `<p class="hall-hire-why">${esc(card.subagent.mission)}</p>` : ''}
    ${toolChips(card.subagent.requiredTools, esc)}
    <div class="hall-hire-actions"><button type="button" class="hall-btn is-sm" data-action="hire-add-subagent" data-work-order-id="${esc(card.workOrderId)}"${disabled}>Добавить помощника</button></div>
    <small class="hall-hire-note">${esc(note)}</small>
  </div>`
}

export function masterHiringCardsHtml(cards, esc) {
  return list(cards).map(card => {
    const state = String(card.state || 'ready')
    const open = masterHiringOpen.has(card.workOrderId)
    const selected = list(card.selected)
    const considered = list(card.considered)
    const takeable = considered.filter(candidate => candidate.readiness !== 'BLOCKED')
    const body = []
    if (selected.length) {
      body.push(`<div class="hall-hire-selected"><b>В наряде</b>${selected.map(candidate => candidateHtml(candidate, esc, card.workOrderId, { takeable: false })).join('')}</div>`)
    }
    body.push(subagentHtml(card, esc))
    if (considered.length) {
      body.push(`<details class="hall-hire-considered"${open ? ' open' : ''}><summary data-action="hire-toggle" data-work-order-id="${esc(card.workOrderId)}">Рассмотрены ещё: ${considered.length}</summary>${considered.map(candidate => candidateHtml(candidate, esc, card.workOrderId, { takeable: takeable.includes(candidate) })).join('')}</details>`)
    }
    // Решать в этой карточке больше нечего: исполнителя нет, и заводят его в
    // своей карточке — она стоит прямо над этой. Пустая рамка «Кем делать ·
    // Исполнителя нет» повторяла бы её заголовок и ничего не добавляла.
    if (!body.filter(Boolean).length) return ''
    return `<section class="hall-panel hall-hire state-${esc(state)}" data-hiring-card="${esc(card.workOrderId)}">
      <header><b>Кем делать</b><small>${esc(STATE_LABELS[state] || 'Состав собран')}</small></header>
      <div class="hall-panel-row">
        ${card.reason ? `<p class="hall-hire-why">${esc(card.reason)}</p>` : ''}
        ${body.join('')}
      </div>
    </section>`
  }).join('')
}

// Действия карточки. Обработчики живут рядом с разметкой: main.js остаётся на
// одной строке диспетчеризации, а правка вида не растаскивается по двум файлам.
//
// Ни одно из них не создаёт агента само: «Взять в наряд» и «Добавить помощника»
// правят ростер уже существующим маршрутом ревизии, а найм из чертежа и полная
// форма уводят в мастерскую, где проверки профиля стоят на своём месте.
export function handleMasterHiringAction(action, target, ctx) {
  const card = () => list(ctx.hiring).find(item => item.workOrderId === String(target.dataset.workOrderId || ''))
  if (action === 'hire-toggle') {
    // Раскрытие переживает перерисовку: без своей памяти список рассмотренных
    // схлопывался на каждом ходе Мастера прямо под курсором.
    const id = String(target.dataset.workOrderId || '')
    if (masterHiringOpen.has(id)) masterHiringOpen.delete(id)
    else masterHiringOpen.add(id)
    ctx.render()
    return true
  }
  if (action === 'hire-into-work-order') {
    const current = card()
    const agentId = String(target.dataset.agentId || '')
    if (!current || !agentId) return true
    const candidate = list(current.considered).concat(list(current.selected)).find(item => item.agentId === agentId)
    if (!candidate) return true
    ctx.reviseRoster(current.workOrderId, roster => {
      const permanent = list(roster.permanent).filter(draft => draft.existing)
      if (permanent.some(draft => draft.id === agentId)) return roster
      permanent.push({
        id: agentId, existing: true, name: candidate.name, role: candidate.role,
        mission: candidate.mission || candidate.role, requiredTools: list(candidate.tools),
      })
      return { ...roster, permanent: permanent.slice(0, Math.max(1, Number(current.maxAgents) || 1)) }
    })
    return true
  }
  if (action === 'hire-add-subagent') {
    const current = card()
    if (!current?.subagent || !current.allowSubagents) return true
    ctx.reviseRoster(current.workOrderId, roster => {
      const permanent = list(roster.permanent)
      const parent = permanent.find(draft => draft.id === current.subagent.parentAgentId) || permanent[0]
      if (!parent) return roster
      const temporary = list(roster.temporary).filter(item => item.role !== current.subagent.role)
      temporary.push({ ...current.subagent, parentAgentId: parent.id })
      return { ...roster, temporary }
    })
    return true
  }
  return false
}
