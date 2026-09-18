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
