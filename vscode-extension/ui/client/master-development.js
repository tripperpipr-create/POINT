const labels = { queued: 'В очереди', running: 'Проверка', deferred: 'Отложено', candidate: 'Кандидат', canary: 'Пробное применение', local: 'Подтверждено в проекте', shared: 'Общий навык', rejected: 'Отклонено', rolled_back: 'Откат', builtin: 'Встроенная версия' }
const list = value => Array.isArray(value) ? value : []
const count = value => Math.max(0, Number(value) || 0).toLocaleString('ru-RU')

export function masterDevelopmentHtml(ui, esc) {
  const data = ui.masterDevelopment
  const busy = ui.masterDevelopmentBusy ? 'disabled' : ''
  const refresh = `<button type="button" class="secondary" data-action="master-development-load" ${busy}>${ui.masterDevelopmentBusy ? 'Загрузка…' : 'Обновить'}</button>`
  const head = `<header><h2>Навыки и развитие</h2>${refresh}</header><p>Методики Мастера подключаются по этапу. Права, утверждение и проверки остаются правилами ядра.</p>`
  const error = ui.masterDevelopmentError ? `<p role="alert">${esc(ui.masterDevelopmentError)}</p>` : ''
  if (!data) return `<section class="onboarding-panel master-development">${head}${error}<p>Загрузите активные версии, бюджет обучения и историю проверок.</p></section>`
  const budget = data.budget || {}
  const active = list(data.skills).map(skill => {
    const revision = list(data.revisions).findLast(item => item.skill?.id === skill.id && item.skill?.instructions === skill.instructions)
    return `<details><summary>${esc(skill.name)} · v${esc(String(skill.configuration?.revision || 1))} · ${esc(labels[revision?.status] || 'Встроенная версия')}</summary><p>${esc(skill.description)}</p><pre>${esc(skill.instructions)}</pre><small>Digest: ${esc(revision?.digest || '—')}</small></details>`
  }).join('')
  const history = list(data.history).map(item => `<details><summary>${esc(item.skillId)} · ${esc(labels[item.status] || item.status)}</summary><p>${esc(item.reason || '')}</p><ul>${list(item.evaluations).map(check => `<li>${esc(check.scenario)}: ${check.passed ? 'пройдено' : 'не пройдено'} · качество ${count(check.baselineScore)} → ${count(check.candidateScore)} · токены ${count(check.baselineTokens)} → ${count(check.candidateTokens)}</li>`).join('')}</ul></details>`).join('')
  const revisions = list(data.revisions).filter(item => item.status !== 'builtin').map(item => `<div><span>${esc(item.skill?.name || item.id)} · v${esc(String(item.skill?.configuration?.revision || ''))} · ${esc(labels[item.status] || item.status)}</span>${['canary', 'local', 'shared'].includes(item.status) ? ` <button type="button" class="secondary" data-action="master-development-rollback" data-id="${esc(item.id)}" ${busy}>Откатить</button>` : ''}</div>`).join('')
  return `<section class="onboarding-panel master-development">${head}${error}
    <p>Самообучение: ${data.config?.enabled ? 'включено' : 'выключено'} <button type="button" class="secondary" data-action="master-development-toggle" ${busy}>${data.config?.enabled ? 'Выключить' : 'Включить'}</button></p>
    <p>За 30 дней: основная работа ${count(budget.mainTokens)} токенов; лимит обучения 10% — ${count(budget.limitTokens)}; потрачено ${count(budget.spentTokens)}, зарезервировано ${count(budget.reservedTokens)}.</p>
    <p>Кандидат проверяется на сохранённых входах и фиксированных сценариях без запуска команд. Затем — три применения в проекте. Для общего навыка нужны независимые подтверждения двух проектов. При нехватке бюджета или авторизации очередь ждёт следующего хода Мастера.</p>
    ${active}<h3>История проверок</h3>${history || '<p>Проверок пока нет: нужны три новых завершённых примера одного этапа.</p>'}
    <h3>Ревизии и откат</h3>${revisions || '<p>Используются встроенные версии.</p>'}
  </section>`
}
