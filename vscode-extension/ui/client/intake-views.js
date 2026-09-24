// URL intake card: paste a source URL, review the draft contract, approve once.

export function intakePanelHtml({ boot, esc, selectedIntakeId, intakeBusy, intakeError, intakeURL }) {
  const sessions = Array.isArray(boot?.intakeSessions) ? boot.intakeSessions : []
  const selected = sessions.find((item) => item.id === selectedIntakeId) || sessions[0]
  const list = sessions.slice(0, 12).map((item) => {
    const active = selected && selected.id === item.id ? ' is-active' : ''
    const title = esc(item.brief?.goal || item.source?.artifacts?.[0]?.title || item.url || item.id)
    return `<button type="button" class="intake-list-item${active}" data-action="select-intake" data-id="${esc(item.id)}">
      <span class="intake-list-status">${esc(item.status || '')}</span>
      <span class="intake-list-title">${title}</span>
    </button>`
  }).join('')

  const urlValue = esc(intakeURL || '')
  const form = `<div class="intake-create">
    <label class="intake-url-label">Ссылка на репозиторий, страницу или документ
      <input type="url" id="intake-url-input" value="${urlValue}" placeholder="https://github.com/org/repo" ${intakeBusy ? 'disabled' : ''} />
    </label>
    <button type="button" class="primary" data-action="create-intake" ${intakeBusy ? 'disabled' : ''}>Разобрать ссылку</button>
  </div>`

  let detail = '<p class="muted">Вставьте HTTPS-ссылку — Hub сохранит снимок источника, план среды и черновик задания.</p>'
  if (selected) {
    const brief = selected.brief || {}
    const env = selected.environment || {}
    const delivery = selected.delivery || {}
    const coverage = selected.coverage || {}
    const criteria = (brief.criteria || []).map((c) => `<li>${esc(c.kind || '')}: ${esc(c.text || '')}</li>`).join('')
    const hosts = (brief.permissions?.networkHosts || env.networkHosts || []).map((h) => `<code>${esc(h)}</code>`).join(' ')
    const blockers = (selected.blockers || []).map((b) => `<li>${esc(b)}</li>`).join('')
    const evidence = selected.evidence
      ? `<p>Доказательства: ${esc(selected.evidence.id || '')} · критерии ${esc(String((selected.evidence.criteria || []).length))}</p>`
      : ''
    const approve = selected.status === 'awaiting_approval' && brief.version
      ? `<button type="button" class="primary" data-action="approve-intake" data-id="${esc(selected.id)}" data-version="${esc(String(brief.version))}" ${intakeBusy ? 'disabled' : ''}>Утвердить и выполнить</button>`
      : ''
    const expand = selected.id
      ? `<button type="button" data-action="expand-intake-network" data-id="${esc(selected.id)}" data-version="${esc(String(brief.version || 0))}" ${intakeBusy ? 'disabled' : ''}>Расширить сеть…</button>`
      : ''
    detail = `<article class="intake-card">
      <header>
        <h3>${esc(brief.goal || selected.url)}</h3>
        <p class="muted">${esc(selected.status)} · ${esc(selected.url)}</p>
      </header>
      <p>Среда: <strong>${esc(env.strategy || '')}</strong> · образ <code>${esc(env.runtime?.image || '')}</code> · отпечаток <code>${esc((env.digest || '').slice(0, 12))}</code></p>
      <p>Покрытие: ${coverage.ready ? 'готово' : 'есть пробелы'} · ветка <code>${esc(delivery.branch || '')}</code></p>
      <p>Сеть: ${hosts || '—'}</p>
      ${blockers ? `<ul class="intake-blockers">${blockers}</ul>` : ''}
      <ol class="intake-criteria">${criteria}</ol>
      ${evidence}
      ${selected.error ? `<p class="error">${esc(selected.error)}</p>` : ''}
      <div class="intake-actions">${approve}${expand}</div>
    </article>`
  }

  return `<section class="hub-current-quest accent-mana">
    <header class="section-title"><span>Задача по ссылке</span></header>
    ${form}
    ${intakeError ? `<p class="muted">${esc(intakeError)}</p>` : ''}
    <div class="hub-overview-secondary">
      <div>${list || '<p class="muted">Пока нет разобранных ссылок</p>'}</div>
      <div>${detail}</div>
    </div>
  </section>`
}
