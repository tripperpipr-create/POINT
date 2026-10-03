// Провал этапа в карточке выполнения: почему, что Point делает сам и что
// решает человек.
//
// Прежде здесь была одна строка ядра — «verify-pack: … (x Build failed in
// 17.43s)» — и две кнопки. Причина лежала в выводе, а повторить можно было
// только так же, как упало. Теперь ядро разбирает каждую проваленную проверку,
// сбой среды Point повторяет сам, безопасные правки Мастера применяются сразу,
// а правка проверки ждёт разрешения на конкретный diff.

function checkHtml(check, esc) {
  const name = check.criterionId ? `<b>${esc(check.criterionId)}</b> ` : ''
  const hint = check.hint ? `<small>${esc(check.hint)}</small>` : ''
  const command = check.command ? `<code>${esc(check.command)}</code>` : ''
  return `<li>${name}<span>${esc(check.cause || '')}</span>${hint}${command}</li>`
}

function diagnosisHtml(failure, esc) {
  const diagnosis = failure?.diagnosis
  const checks = Array.isArray(diagnosis?.checks) ? diagnosis.checks : []
  if (!checks.length) return failure?.error ? `<p>${esc(failure.error)}</p>` : ''
  const image = diagnosis.image ? `<small>Среда проверки: ${esc(diagnosis.image)}</small>` : ''
  return `<ul class="stage-failure-checks">${checks.map(check => checkHtml(check, esc)).join('')}</ul>${image}`
}

function autopilotHtml(failure, proposal, esc) {
  if (proposal?.autoApply && !proposal.needsApproval) {
    const what = proposal.instruction ? 'с указанием исполнителю' : ''
    return `<small class="stage-failure-auto">Мастер повторяет этап${what ? ' ' + esc(what) : ''}${proposal.diagnosis ? ': ' + esc(proposal.diagnosis) : ''}</small>`
  }
  if (failure?.autoRetry?.allowed) {
    return `<small class="stage-failure-auto">Сбой среды: Point повторит этап сам через ${Number(failure.autoRetry.delaySeconds) || 30} с (попытка ${Number(failure.autoRetry.attempt) || 1} из ${Number(failure.autoRetry.max) || 2})</small>`
  }
  return ''
}

function proposalHtml(order, runtime, proposal, esc) {
  if (!proposal?.needsApproval) return ''
  const dependencies = (proposal.dependencyPlan?.projects || []).map(project => `<li><b>${esc(project.manager)}: ${esc(project.cwd || '.')}</b>${(project.commands || []).map(command => `<code>${esc(command.command)}</code>`).join('')}<small>Манифесты: ${esc((project.manifestPaths || []).join(', '))}</small><small>Ожидаемые пути: ${esc((project.expectedPaths || []).join(', ') || '—')}</small></li>`).join('')
  const changes = (Array.isArray(proposal.criteria) ? proposal.criteria : []).map(change => `<li><b>${esc(change.criterionId)}</b>
      <span>было</span><code>${esc(change.previousCommand || '')}</code>
      <span>станет</span><code>${esc(change.command || '')}</code>${change.reason ? `<small>${esc(change.reason)}</small>` : ''}</li>`).join('')
  return `<div class="stage-retry-proposal">
      <b>Мастер предлагает поправку и повтор этапа</b>
      ${proposal.diagnosis ? `<p>${esc(proposal.diagnosis)}</p>` : ''}
      <ul>${dependencies}${changes}</ul>
      <div><button type="button" class="hall-btn is-primary" data-action="retry-work-order-stage" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId || '')}" data-proposal-digest="${esc(proposal.digest)}">Разрешить и повторить</button></div>
    </div>`
}

function controlButton(order, runtime, control, label, cls, esc) {
  return `<button type="button" class="${cls}" data-action="control-master-work-order-v2" data-control="${control}" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId || '')}">${label}</button>`
}

// Кнопки: повтор как есть, разбор Мастером, вердикт и новая версия наряда.
// Кнопок под отдельную причину здесь нет: были «Повторить в Node 20 / 22» под
// один случай с npm, и при провале по сроку они обещали лечение, которого не
// давали. Что менять в повторе, решает Мастер по разбору — для любой задачи.
function buttonsHtml(order, runtime, failure, esc) {
  const revise = `<button type="button" class="hall-btn" data-action="revise-master-work-order-v2" data-id="${esc(order.id)}">Обсудить новую версию</button>`
  if (runtime.status !== 'awaiting_user') return `<div>${revise}</div>`
  return `<div>${controlButton(order, runtime, 'retry', 'Повторить этап', 'hall-btn is-primary', esc)}<button type="button" class="hall-btn" data-action="analyze-stage-failure" data-id="${esc(order.id)}">Разобрать с Мастером</button>${controlButton(order, runtime, 'finalize', 'Завершить квест', 'hall-btn', esc)}${revise}</div>`
}

export function stageFailureHtml(order, runtime, esc) {
  const failure = runtime?.stageFailure
  const proposal = runtime?.stageRetryProposal
  return `${diagnosisHtml(failure, esc)}${autopilotHtml(failure, proposal, esc)}${proposalHtml(order, runtime, proposal, esc)}${buttonsHtml(order, runtime, failure, esc)}`
}

// Проверка на хосте упёрлась в среду (Q14): результат уже в проекте; проверить
// снова на той же ревизии или вынести вердикт по полученному итогу.
export function hostRecheckHtml(order, runtime, esc) {
  const held = runtime?.hostRecheck
  if (!held || runtime.status !== 'awaiting_user') return ''
  const actions = (Array.isArray(held.actions) ? held.actions : []).map(item => `<li>${esc(item)}</li>`).join('')
  return `<div class="work-order-exec-stall"><b>Проверка на хосте упёрлась в среду</b><small>Результат уже в проекте · остановка ${Number(held.count) || 1} из 3</small>${actions ? `<ul>${actions}</ul>` : ''}<div>${controlButton(order, runtime, 'recheck', 'Проверить снова', 'hall-btn is-primary', esc)}${controlButton(order, runtime, 'finalize', 'Завершить квест', 'hall-btn', esc)}</div></div>`
}
