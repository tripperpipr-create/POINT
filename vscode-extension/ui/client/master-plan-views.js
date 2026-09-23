// План работы в ленте разговора.
//
// Плана в чате не было вовсе: до запуска карточка задания обещала, что «после
// утверждения Мастер назначит каждому этапу агента, runtime, модель и
// стоимость», а после запуска состояния этапов жили в другом разделе — в обзоре
// квеста и в хронике. Человек, который только что согласовал работу, не видел
// ни того, из чего она состоит, ни того, где она сейчас.
//
// Эталон — ячейка PlanUpdateCell в интерфейсе Codex: перечень с тремя
// состояниями, где сделанное перечёркнуто и потушено, идущее набрано весом и
// единственным цветным знаком, а ждущее просто тише.
//
// Одна форма на две сущности, но не одно имя: до старта состояний не существует
// (планировщик работает при запуске квеста), и перечень условий готовности
// называется условиями готовности. Назвать его планом значило бы обещать
// прогресс там, где его неоткуда взять.

const PLAN_MARK = { done: '✔', now: '□', wait: '□' }

// Состояние узла прогона → состояние пункта. Ожидание решения — это «идёт»:
// работа стоит на человеке, а не на очереди.
export function masterPlanState(status) {
  const value = String(status || '')
  if (value === 'completed' || value === 'skipped') return 'done'
  if (value === 'running' || value === 'waiting' || value === 'waiting_approval') return 'now'
  return 'wait'
}

// Перечень с тремя состояниями. `rows` — { text, state, note }.
export function masterPlanHtml(title, rows, esc, { limit = 8 } = {}) {
  const list = Array.isArray(rows) ? rows.filter(row => row && String(row.text || '').trim()) : []
  if (!list.length) return ''
  const done = list.filter(row => row.state === 'done').length
  // Длинный перечень режется по хвосту, а не по началу: обрезанный хвост назван
  // числом, и счётчик в заголовке продолжает считать всё, а не показанное.
  const shown = list.slice(0, limit)
  const rest = list.length - shown.length
  const lines = shown.map(row => `<div class="hall-plan-row is-${row.state || 'wait'}">
    <i aria-hidden="true">${PLAN_MARK[row.state] || PLAN_MARK.wait}</i>
    <span>${esc(row.text)}</span>
    ${row.note ? `<small>${esc(row.note)}</small>` : ''}
  </div>`).join('')
  return `<div class="hall-plan">
    <small class="hall-plan-progress">${esc(title)} · ${done} из ${list.length}</small>
    ${lines}
    ${rest > 0 ? `<small class="hall-plan-rest">и ещё ${rest}</small>` : ''}
  </div>`
}

// Пометка при пункте: почему он стоит или чем кончился.
const STAGE_NOTE = {
  waiting: 'нужно решение',
  waiting_approval: 'нужно решение',
  failed: 'ошибка',
  cancelled: 'отменён',
  skipped: 'пропущен',
}

// Ряды плана запущенного квеста — из состояний узлов прогона.
//
// Считает их два места сразу: лента (карточка запущенной работы) и правая
// панель разговора, где план и живёт после старта. Второй такой же перебор
// разошёлся бы с первым на первой же правке — этапы читались бы в панели
// иначе, чем в ленте, и оба вида были бы «правдой».
//
// Читаем на отрисовку и ничего не храним: правда о прогоне остаётся у ядра.
export function questPlanRows(boot, questId, nodeKindLabels = {}) {
  const id = String(questId || '')
  if (!id) return []
  const run = (boot?.flowRuns || []).find(item => item.questId === id && item.nodeStates)
  if (!run?.nodeStates) return []
  const flow = (boot?.flows || []).find(item => item.id === run.flowId)
  // Порядок задаёт сам Flow: перебор объекта состояний отдал бы этапы в
  // порядке вставки ядром, а он не порядок работы.
  const nodes = Array.isArray(flow?.nodes) && flow.nodes.length
    ? flow.nodes
    : Object.keys(run.nodeStates).map(id => ({ id }))
  return nodes.map(node => {
    const status = String(run.nodeStates[node.id]?.status || 'pending')
    return {
      text: node.name || nodeKindLabels?.[node.kind] || node.id,
      state: masterPlanState(status),
      note: STAGE_NOTE[status] || '',
    }
  })
}

// Сколько этапов позади. Число живёт на вкладке задания, где места на перечень
// нет: «3 из 7» — это весь прогресс, который помещается в шапку разговора.
export function questPlanProgress(rows) {
  const list = Array.isArray(rows) ? rows : []
  return { done: list.filter(row => row.state === 'done').length, total: list.length }
}
