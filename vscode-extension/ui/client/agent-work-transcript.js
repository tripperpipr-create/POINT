// Cursor-подобная лента работы агента: ответы, tools, diffs, approvals.
// Общий рендер для хроники квеста и встроенного блока в чате Мастера.

const SKIP_TOOL_FAIL = new Set([
  'inspection_required',
  'inspection_scope_required',
  'inspection_stale',
  'duplicate_tool_call',
])

export function createAgentWorkTranscript(dependencies) {
  const {
    esc,
    data,
    toolName,
    formatDuration,
    guardrailEventText,
    toolFailureText,
    coreFailureText,
    patchStatusLabel,
    questLevelUpBadge,
    formatMarkdown,
  } = dependencies

  function bodyHtml(text) {
    const raw = String(text || '')
    if (!raw) return ''
    return typeof formatMarkdown === 'function' ? formatMarkdown(raw) : `<p>${esc(raw)}</p>`
  }

  function approvalCard(approval, anchor = false) {
    const args = data(approval.arguments)
    const pending = approval.status === 'pending'
    const title = args.displayName
      ? `Инструмент «${args.displayName}» требует подтверждения`
      : 'Команда требует подтверждения'
    const operation = args.kind === 'process' && Array.isArray(args.arguments)
      ? `<div class="process-approval"><span>ПРОГРАММА</span><code>${esc(args.program)}</code><span>ARGV · ${args.arguments.length}</span><ol>${args.arguments.map((argument, index) => `<li><b>${index}</b><code>${esc(JSON.stringify(argument))}</code></li>`).join('') || '<li><em>без аргументов</em></li>'}</ol></div>`
      : `<pre>${esc(args.command || JSON.stringify(args, null, 2))}</pre>`
    return `<section class="approval agent-work-card ${pending ? 'is-pending' : ''} ${!pending && approval.status === 'denied' ? 'was-denied' : ''}" ${anchor ? 'id="pending-decision"' : ''}><header><span>!</span><div><strong>${esc(title)}</strong><p>${esc(args.reason || approval.reason)}</p>${pending ? '<em class="decision-mark">Ожидает решения</em>' : ''}</div></header>${operation}${args.cwd ? `<small>${esc(args.cwd)}${args.timeoutSeconds ? ` · тайм-аут ${esc(args.timeoutSeconds)} сек` : ''}</small>` : ''}${pending ? `<footer><button class="danger-button" data-action="resolve" data-id="${esc(approval.id)}" data-allow="false">Отклонить</button><button class="primary small-button" data-action="resolve" data-id="${esc(approval.id)}" data-allow="true">Разрешить один раз</button></footer>` : `<div class="resolved ${approval.status === 'denied' ? 'denied' : ''}">${approval.status === 'allowed' ? 'Разрешено' : 'Отклонено · агент продолжит без этого умения'}</div>`}</section>`
  }

  function patchCard(patch, approval, anchor = false) {
    const pending = approval?.status === 'pending'
    const diff = String(patch.diff || '').trim()
    const diffBlock = diff
      ? `<details class="agent-work-diff" ${pending ? 'open' : ''}><summary>Показать diff</summary><pre>${esc(diff)}</pre></details>`
      : ''
    return `<section class="patch agent-work-card ${pending ? 'is-pending' : ''}" ${anchor ? 'id="pending-decision"' : ''}><header><button type="button" data-action="open-file" data-path="${esc(patch.path)}">${esc(patch.path)}</button><span>${esc(patchStatusLabel(pending ? 'proposed' : patch.status))}</span></header>${pending ? '<em class="decision-mark">Diff ждёт подтверждения</em>' : ''}${diffBlock}${pending ? `<footer><button class="danger-button" data-action="resolve" data-id="${esc(approval.id)}" data-allow="false">Отклонить</button><button class="primary small-button" data-action="resolve" data-id="${esc(approval.id)}" data-allow="true">Применить</button></footer>` : ''}</section>`
  }

  // Ждёт ли решения набор правок: у патча своё подтверждение, и лежит оно в
  // общем перечне подтверждений прогона.
  const patchIsPending = (approvals, patch) => approvals.get(patch.approvalId)?.status === 'pending'

  function agentWorkTranscriptHtml(details, options = {}) {
    if (!details?.run) return ''
    const limit = Number(options.limit) > 0 ? Number(options.limit) : 240
    const compact = Boolean(options.compact)
    // Ждущее решение можно поднять из хроники наверх карточки — тогда здесь его
    // рисовать второй раз нельзя: две одинаковые карточки с одной парой кнопок
    // и одним якорем «pending-decision» — это не подсказка, а развилка-двойник.
    const withoutPending = Boolean(options.withoutPending)
    const approvals = new Map((details.approvals || []).map(item => [item.id, item]))
    const patches = new Map((details.patches || []).map(item => [item.id, item]))
    const allEvents = details.events || []
    const visibleEvents = allEvents.slice(-limit)
    const items = []
    if (allEvents.length > visibleEvents.length) {
      items.push(`<div class="notice agent-work-notice">Показаны последние ${visibleEvents.length} событий из ${allEvents.length}.</div>`)
    }
    let anchoredPending = false
    for (const event of visibleEvents) {
      const payload = data(event.data)
      if (event.type === 'run.message_injected' && payload.content) {
        const learningBadge = payload.learningIntent === 'correction'
          ? '<div class="run-context"><span title="Это уточнение явно разрешено использовать как проверяемый обучающий сигнал">◎ разрешено как урок</span></div>'
          : ''
        items.push(`<article class="message user agent-work-msg"><small>УТОЧНЕНИЕ · ХОД ${event.step}</small><div class="agent-work-body">${bodyHtml(payload.content)}</div>${learningBadge}</article>`)
      }
      if (event.type === 'model.responded' && payload.content) {
        items.push(`<article class="message agent agent-work-msg"><div class="avatar" aria-hidden="true">✦</div><div><small>АГЕНТ · ХОД ${event.step}</small><div class="agent-work-body">${bodyHtml(payload.content)}</div></div></article>`)
      }
      if (event.type === 'model.retrying') {
        items.push(`<div class="notice warning agent-work-notice">↻ Провайдер временно недоступен · попытка ${esc(payload.attempt || '?')}${payload.delayMs ? ` через ${formatDuration(payload.delayMs)}` : ''}</div>`)
      }
      if (event.type === 'context.compacted') {
        items.push(`<div class="notice agent-work-notice">◇ Память уплотнена · ${esc(payload.beforeTokens || 0)} → ${esc(payload.afterTokens || 0)} токенов</div>`)
      }
      if (event.type === 'completion.checked' && payload.status === 'revision_required') {
        items.push('<div class="notice warning agent-work-notice">◇ Финал на доработку · нет проверяемого результата</div>')
      }
      if (event.type === 'completion.checked' && payload.status === 'accepted_after_revision') {
        items.push('<div class="notice success agent-work-notice">✓ Финал принят после подтверждения инструментом</div>')
      }
      if (event.type === 'completion.checked' && payload.status === 'rejected') {
        items.push('<div class="notice danger agent-work-notice">! Финал отклонён · доказательство готовности не получено</div>')
      }
      if (event.type === 'agent.guardrail') {
        items.push(`<div class="notice warning agent-work-notice">△ Защита · ${esc(guardrailEventText(payload))}</div>`)
      }
      if (event.type === 'tool.finished' && payload.result?.ok === false && !SKIP_TOOL_FAIL.has(payload.result?.error?.code)) {
        items.push(`<div class="notice warning agent-work-notice">△ ${esc(toolFailureText(payload.tool, payload.result.error))}</div>`)
      }
      if (event.type === 'workspace.changed') {
        const source = toolName(payload.tool || 'команда')
        const warnings = [
          payload.nonRevertibleChanges ? `${payload.nonRevertibleChanges} без точного отката` : '',
          payload.omittedRevertibleChanges ? `${payload.omittedRevertibleChanges} сверх лимита истории` : '',
          payload.snapshotComplete === false ? 'снимок неполный' : '',
        ].filter(Boolean)
        items.push(`<div class="notice ${warnings.length ? 'warning' : 'success'} agent-work-notice">${warnings.length ? '△' : '✓'} ${esc(source)} · изменений: ${esc(payload.totalChanges || 0)} · записано: ${esc(payload.recordedChanges || 0)}${warnings.length ? ` · ${esc(warnings.join(' · '))}` : ''}</div>`)
      }
      if (event.type === 'tool.requested') {
        items.push(`<div class="tool agent-work-tool"><span aria-hidden="true">↳</span><div><strong>${esc(toolName(payload.tool))}</strong><small>шаг ${event.step}</small></div></div>`)
      }
      if (event.type === 'approval.requested') {
        const approval = approvals.get(String(payload.id || ''))
        if (approval && approval.toolName !== 'propose_patch' && !(withoutPending && approval.status === 'pending')) {
          const anchor = !anchoredPending && approval.status === 'pending'
          if (anchor) anchoredPending = true
          items.push(approvalCard(approval, anchor))
        }
      }
      if (event.type === 'patch.proposed') {
        const patch = patches.get(String(payload.id || ''))
        if (patch && !(withoutPending && patchIsPending(approvals, patch))) {
          const approval = approvals.get(patch.approvalId)
          const anchor = !anchoredPending && approval?.status === 'pending'
          if (anchor) anchoredPending = true
          items.push(patchCard(patch, approval, anchor))
        }
      }
      if (event.type === 'approval.resolved' && payload.status === 'denied') {
        items.push('<div class="notice warning agent-work-notice">△ Подтверждение отклонено</div>')
      }
      if (event.type === 'run.completed') {
        items.push(`<div class="notice success completion-pulse agent-work-notice">✓ Квест завершён · запросов: ${details.run.requestCount} · артефактов: ${details.run.changedFiles?.length || 0}</div>${questLevelUpBadge ? questLevelUpBadge(details.run) : ''}`)
      }
      if (event.type === 'run.failed' || event.type === 'run.cancelled') {
        items.push(`<div class="notice danger agent-work-notice">! ${esc(coreFailureText(payload.error || payload.reason || details.run.error) || 'Запуск остановлен')}</div>`)
      }
    }
    const runStatus = String(details.run.status || '')
    if (['running', 'waiting_approval', 'paused'].includes(runStatus)) {
      const label = runStatus === 'waiting_approval'
        ? 'Ожидаю вашего решения'
        : runStatus === 'paused'
          ? 'Пауза'
          : 'Агент работает…'
      items.push(`<div class="thinking agent-work-thinking"><i></i><span>${esc(label)}</span></div>`)
    }
    if (!items.length) {
      return compact
        ? '<div class="agent-work-empty"><span>Ожидаем первые шаги агента…</span></div>'
        : ''
    }
    return `<div class="agent-work-transcript${compact ? ' is-compact' : ''}">${items.join('')}</div>`
  }

  return { agentWorkTranscriptHtml, approvalCard, patchCard }
}
