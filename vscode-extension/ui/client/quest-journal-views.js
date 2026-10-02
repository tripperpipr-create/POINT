// Журнал этапа квеста в карточке прогона.
//
// Прежняя хроника рисовала каждый вызов инструмента рамкой «↳ Карта проекта ·
// шаг 1»: без того, над чем работал агент, чем кончилось и сколько заняло.
// Двадцать таких рамок подряд были самым высоким и самым пустым местом ленты.
// Предупреждения шли оранжевыми коробками цвета главной кнопки, а внутри —
// английский текст ядра и сырое имя инструмента («сначала search_code»).
//
// Здесь журнал говорит тем же языком, что и след хода Мастера (master-trail.js,
// разметка hall-trail / hall-step из 07c): действия между репликами агента
// собраны в одну строку сводки «N действий · K предупреждений», раскрываемую по
// нажатию, а строка действия — значок, имя, над чем, итог и длительность.
// Предупреждения видны всегда, под сводкой, тоном --caution, и по-русски.
// Правка файла — строка файла со счётом «+N −M» и цветным diff.
//
// Реплики агента остаются на его языке: модели работают по-английски, и
// переучивать их ради экрана владелец не велел. Всё, что вокруг, — по-русски.
//
// Общий agent-work-transcript.js остаётся для раздела «Квесты» и старого пути
// hall-work: там свой регистр, и менять его этой правкой незачем.

import { countOf, formatElapsed, list } from './format-units.js'
import { icon } from './ui-icons.js'
import { masterCardMoreAttrs } from './master-card-open.js'
import { diffCountHtml, diffHtml, diffPathHtml, diffStats } from './diff-view.js'
import { completionStatus, pendingAcceptanceText, verificationReuseText } from './completion-verdict.js'

// Отказы, о которых ядро сообщает отдельным событием защиты: строка сбоя их
// повторила бы.
const SKIP_TOOL_FAIL = new Set(['inspection_required', 'inspection_scope_required', 'inspection_stale', 'duplicate_tool_call'])

// Правка файла показывается строкой файла, а не шагом: иначе одно действие
// читалось бы дважды.
const FILE_TOOLS = new Set(['propose_patch'])

// Значок отвечает на вопрос «чем работал». Имена — каталог ядра
// (internal/domain/catalog.go); незнакомое получает общий значок.
const STEP_ICONS = {
  project_map: 'map', list_files: 'folder', read_file: 'file', search_code: 'search', search_text: 'search',
  git_diff: 'git', git_log: 'git', git_branches: 'git', git_tags: 'git', read_skill: 'memory',
  team_inbox: 'team', team_publish: 'team', request_subagent: 'team', permission_prompt: 'info',
  run_command: 'terminal', docker_inspect: 'terminal', docker_control: 'terminal',
  ssh_test_connection: 'terminal', ssh_list_remote: 'folder', ssh_read_remote: 'file', ssh_exec_remote: 'terminal',
  db_list_connections: 'list', db_schema: 'list', db_query: 'search', db_exec: 'terminal',
}

// Коды отказов, которые общий toolFailureText не знает и отдавал английским
// текстом ядра («skill is not equipped for this agent»). Текст ядра адресован
// модели и остаётся в подсказке по наведению.
const FAILURE_TEXT = {
  skill_not_equipped: 'навык не выдан этому агенту',
  invalid_path: 'путь вне рабочей папки или записан неверно',
  sensitive_path: 'путь закрыт: чувствительный файл',
  not_found: 'не найдено',
  read_failed: 'файл не прочитан',
  binary_file: 'двоичный файл не читается',
  content_too_large: 'содержимое слишком большое',
  arguments_too_large: 'аргументы слишком большие',
  patch_conflict: 'правка разошлась с файлом',
  patch_rejected: 'правка отклонена',
  approval_denied: 'подтверждение отклонено',
  tool_denied: 'инструмент запрещён политикой',
  sandbox_unavailable: 'песочница недоступна',
  search_failed: 'поиск не выполнен',
  index_search_failed: 'поиск не выполнен',
  index_failed: 'индекс не построен',
  tool_output_limit: 'вывод превысил лимит',
  background_process_unsupported: 'фоновые процессы не поддерживаются',
  workspace_audit_failed: 'изменения не удалось зафиксировать',
}
// Коды, которые общий toolFailureText переводит сам (quest-runtime-views.js).
const SHARED_FAILURE_CODES = new Set(['edit_anchor_missing', 'edit_anchor_ambiguous', 'overlapping_edits', 'edit_target_missing', 'no_changes', 'command_denied', 'network_denied', 'duplicate_tool_call', 'invalid_input'])

function parsed(value) {
  if (typeof value !== 'string') return value
  try { return JSON.parse(value) } catch { return value }
}

function clip(value, limit) {
  const text = String(value || '').replace(/\s+/g, ' ').trim()
  return text.length > limit ? `${[...text].slice(0, limit - 1).join('')}…` : text
}

// Над чем работал инструмент — из его аргументов. Путь, команда, запрос, имя.
function stepArgument(raw) {
  const args = parsed(raw)
  if (!args || typeof args !== 'object') return String(args || '')
  const program = args.program ? [args.program, ...list(args.arguments ?? args.args).map(String)].join(' ') : ''
  const paths = Array.isArray(args.paths) ? args.paths.join(', ') : ''
  return String(args.path || args.file || paths || args.command || program || args.query || args.pattern
    || args.name || args.skill || args.url || args.host || args.sql || args.message || '').trim()
}

// Чем кончилось — коротко, по форме вывода. Неизвестная форма молчит, а не
// выдумывает итог.
function stepResult(result) {
  const output = parsed(result?.output)
  if (Array.isArray(output)) return output.length ? countOf(output.length, 'запись', 'записи', 'записей') : 'пусто'
  if (!output || typeof output !== 'object') return ''
  if (output.exitCode != null) return `код ${Number(output.exitCode)}${output.timedOut ? ' · время вышло' : ''}`
  if (typeof output.count === 'number') return output.count ? countOf(output.count, 'совпадение', 'совпадения', 'совпадений') : 'ничего не найдено'
  if (Array.isArray(output.chunks)) return output.chunks.length ? countOf(output.chunks.length, 'фрагмент', 'фрагмента', 'фрагментов') : 'ничего не найдено'
  for (const key of ['entries', 'files', 'items', 'matches', 'results']) {
    if (Array.isArray(output[key])) return output[key].length ? countOf(output[key].length, 'запись', 'записи', 'записей') : 'пусто'
  }
  if (typeof output.content === 'string') return countOf(output.content.split('\n').length, 'строка', 'строки', 'строк')
  return ''
}

export function createQuestJournal(dependencies) {
  const { esc, data, toolName, formatDuration, guardrailEventText, toolFailureText, coreFailureText, formatMarkdown, approvalCard, patchCard, questLevelUpBadge } = dependencies

  function failureText(tool, error) {
    const code = String(error?.code || '')
    if (FAILURE_TEXT[code]) return FAILURE_TEXT[code]
    if (SHARED_FAILURE_CODES.has(code)) return toolFailureText(tool, error)
    return 'не выполнено'
  }

  // Защита называет нужный инструмент по-русски: «сначала «Умный поиск кода»».
  function guardText(payload) {
    const required = payload.requiredTool || (payload.code === 'inspection_scope_required' ? 'search_code' : '')
    return guardrailEventText(required ? { ...payload, requiredTool: `«${toolName(required)}»` } : payload)
  }

  function noteHtml(tone, iconName, label, text, title = '') {
    return `<div class="hall-step quest-note is-${tone}"${title ? ` title="${esc(title)}"` : ''}><span class="hall-step-icon">${icon(iconName)}</span><b>${esc(label)}</b><span class="quest-note-text">${esc(text)}</span></div>`
  }

  function stepHtml(step) {
    const tail = step.running ? 'выполняется…' : [step.result, step.durationMs != null ? formatDuration(step.durationMs) : ''].filter(Boolean).join(' · ')
    return `<div class="hall-step quest-step${step.softFail ? ' is-soft-fail' : ''}${step.running ? ' is-running' : ''}">
      <span class="hall-step-icon">${icon(STEP_ICONS[step.tool] || 'tool')}</span>
      <b title="${esc(step.tool)}">${esc(toolName(step.tool))}</b>
      <span class="hall-step-arg" title="${esc(step.argument)}">${esc(clip(step.argument, 96) || '—')}</span>
      <small>${esc(tail)}</small>
    </div>`
  }

  // Реплика агента — на его языке, тихим набором. Длинная сворачивается до
  // начала: это ход мысли, а не ответ человеку.
  function sayHtml(content, key) {
    const text = String(content || '').trim()
    if (text.length <= 320) return `<div class="quest-say"><div class="quest-say-body">${formatMarkdown(text)}</div></div>`
    return `<details class="quest-say is-long"${masterCardMoreAttrs(key, { esc })}>
      <summary><span class="quest-say-lead">${esc(clip(text, 220))}</span><span class="quest-say-more">целиком</span><span class="quest-say-less">свернуть</span></summary>
      <div class="quest-say-body">${formatMarkdown(text)}</div>
    </details>`
  }

  function fileHtml(patch, key, sandboxOnly) {
    const stats = diffStats(patch.diff)
    const verb = stats.created ? 'создан' : stats.deleted ? 'удалён' : 'изменён'
    const state = { proposed: 'ждёт решения', rejected: 'отклонён', reverted: 'откачен' }[patch.status] || `${verb}${sandboxOnly ? ' в песочнице' : ''}`
    const head = `<span class="hall-step-icon${stats.created ? ' is-new' : ''}">${icon(stats.created ? 'file-plus' : 'file-edit')}</span><span class="quest-file-path" title="${esc(patch.path)}">${diffPathHtml(patch.path, esc)}</span>${diffCountHtml(stats)}<small>${esc(state)}</small>`
    if (!stats.known) return `<div class="quest-file">${head}</div>`
    return `<details class="quest-file"${masterCardMoreAttrs(key, { esc })}><summary>${head}<span class="quest-file-chevron">${icon('chevron-right')}</span></summary>${diffHtml(patch.diff, esc)}</details>`
  }

  function groupHtml(group, key, open) {
    const alerts = group.alerts.length ? `<div class="hall-trail-failed quest-alerts">${group.alerts.join('')}</div>` : ''
    if (!group.rows.length) return alerts ? `<div class="hall-trail quest-trail">${alerts}</div>` : ''
    const kinds = [...new Set(group.steps.map(step => STEP_ICONS[step.tool] || 'tool'))].slice(0, 4)
    const label = group.steps.length ? countOf(group.steps.length, 'действие', 'действия', 'действий') : countOf(group.rows.length, 'событие', 'события', 'событий')
    const warn = group.alerts.length ? `<span class="quest-trail-warn">${icon('warning')}${esc(countOf(group.alerts.length, 'предупреждение', 'предупреждения', 'предупреждений'))}</span>` : ''
    const running = group.steps.some(step => step.running)
    return `<div class="hall-trail quest-trail${running ? ' is-running' : ''}">
      <details class="hall-trail-group"${masterCardMoreAttrs(key, { esc, open })}>
        <summary><span class="hall-trail-icons">${kinds.map(name => icon(name)).join('')}</span><span class="hall-trail-label">${esc(label)}</span>${warn}<span class="hall-trail-chevron">${icon('chevron-right')}</span></summary>
        <div class="hall-trail-list">${group.rows.join('')}</div>
      </details>
      ${alerts}
    </div>`
  }

  // Что агент делает прямо сейчас: незакрытый вызов инструмента или мысль.
  function nowHtml(details, openStep) {
    const status = String(details.run.status || '')
    if (!['running', 'waiting_approval', 'paused'].includes(status)) return ''
    if (status !== 'running') {
      return `<div class="quest-now is-waiting"><i aria-hidden="true"></i><span>${status === 'paused' ? 'Пауза' : 'Ждёт вашего решения'}</span></div>`
    }
    if (!openStep) return '<div class="quest-now"><i aria-hidden="true"></i><span>Агент думает…</span></div>'
    const since = Date.parse(openStep.createdAt || '')
    const elapsed = Number.isNaN(since) ? '' : ` · ${formatElapsed(Date.now() - since)}`
    return `<div class="quest-now"><i aria-hidden="true"></i><span>${esc(toolName(openStep.tool))}</span>${openStep.argument ? `<code title="${esc(openStep.argument)}">${esc(clip(openStep.argument, 72))}</code>` : ''}<small>${esc(elapsed.replace(/^ · /, ''))}</small></div>`
  }

  function questJournalHtml(details, options = {}) {
    if (!details?.run) return ''
    const runId = String(details.run.id || '')
    const runLive = ['running', 'waiting_approval', 'paused'].includes(String(details.run.status || ''))
    const sandboxOnly = Boolean(options.sandboxOnly)
    const withoutPending = Boolean(options.withoutPending)
    const approvals = new Map(list(details.approvals).map(item => [item.id, item]))
    const patches = new Map(list(details.patches).map(item => [item.id, item]))
    const all = list(details.events)
    const events = all.slice(-400)
    const finished = new Map()
    for (const event of events) {
      const payload = data(event.data)
      if (event.type === 'tool.finished' && payload.callId) finished.set(String(payload.callId), payload)
    }
    const blocks = []
    let group = null
    let openStep = null
    const current = () => {
      if (!group) { group = { kind: 'group', steps: [], rows: [], alerts: [] }; blocks.push(group) }
      return group
    }
    const close = () => { group = null }
    const alert = (label, error, tool) => current().alerts.push(noteHtml('warn', 'warning', label, failureText(tool, error), [error?.code, error?.message].filter(Boolean).join(': ')))
    let anchored = false
    for (const event of events) {
      const payload = { ...data(event.data) }
      switch (event.type) {
        case 'model.responded':
          if (payload.content) { close(); blocks.push({ kind: 'html', html: sayHtml(payload.content, `journal-say:${runId}:${event.step}:${blocks.length}`) }) }
          break
        case 'run.message_injected':
          if (payload.content) { close(); blocks.push({ kind: 'html', html: `<div class="quest-say is-mine"><small>Ваше уточнение</small><div class="quest-say-body">${formatMarkdown(String(payload.content))}</div></div>` }) }
          break
        case 'tool.requested': {
          const done = payload.callId ? finished.get(String(payload.callId)) : null
          // Вызов с негодными аргументами ядро отбивает до исполнения: события
          // завершения у него нет, и «выполняется…» висело бы вечно.
          const result = payload.error ? { ok: false, error: { code: 'invalid_input', message: String(payload.error) } } : done?.result
          const code = String(result?.error?.code || '')
          if (result && result.ok === false && !SKIP_TOOL_FAIL.has(code)) alert(toolName(payload.tool), result.error, payload.tool)
          if (FILE_TOOLS.has(payload.tool)) break
          const step = {
            tool: String(payload.tool || ''), argument: stepArgument(payload.arguments), createdAt: event.createdAt,
            running: !result && runLive, durationMs: done?.durationMs, result: result?.ok === false ? 'не выполнено' : stepResult(result),
          }
          step.softFail = result?.ok === false || /^код [1-9-]/.test(step.result)
          // Незакрытый вызов живёт в строке «сейчас» под журналом: в сводке
          // он встал бы вторым экземпляром той же строки.
          if (step.running) { openStep = step; break }
          const target = current()
          target.steps.push(step)
          target.rows.push(stepHtml(step))
          break
        }
        case 'tool.finished':
          if (!payload.callId && payload.result?.ok === false && !SKIP_TOOL_FAIL.has(String(payload.result?.error?.code || ''))) alert(toolName(payload.tool), payload.result.error, payload.tool)
          break
        case 'agent.guardrail':
          current().alerts.push(noteHtml('warn', 'warning', 'Защита', guardText(payload), payload.message || ''))
          break
        case 'workspace.changed': {
          const warnings = [
            payload.nonRevertibleChanges ? `${payload.nonRevertibleChanges} без точного отката` : '',
            payload.omittedRevertibleChanges ? `${payload.omittedRevertibleChanges} сверх лимита истории` : '',
            payload.snapshotComplete === false ? 'снимок неполный' : '',
          ].filter(Boolean)
          const text = `изменений: ${Number(payload.totalChanges || 0)} · записано: ${Number(payload.recordedChanges || 0)}${sandboxOnly ? ' · в песочнице' : ''}${warnings.length ? ` · ${warnings.join(' · ')}` : ''}`
          const row = noteHtml(warnings.length ? 'warn' : 'quiet', warnings.length ? 'warning' : 'check', toolName(payload.tool || 'run_command'), text)
          if (warnings.length) current().alerts.push(row)
          else current().rows.push(row)
          break
        }
        case 'model.retrying':
          current().rows.push(noteHtml('quiet', 'retry', 'Повтор запроса', `попытка ${Number(payload.attempt) || 1}${payload.delayMs ? ` через ${formatDuration(payload.delayMs)}` : ''}`))
          break
        case 'context.compacted':
          current().rows.push(noteHtml('quiet', 'memory', 'Память уплотнена', `${Number(payload.beforeTokens || 0).toLocaleString('ru-RU')} → ${Number(payload.afterTokens || 0).toLocaleString('ru-RU')}`))
          break
        case 'completion.checked': {
          payload.status = completionStatus(payload)
          if (payload.status === 'needs_review') {
            close(); blocks.push({ kind: 'html', html: `<div class="hall-trail quest-trail">${noteHtml('warn', 'warning', 'Приёмка', pendingAcceptanceText(payload))}</div>` })
          }
          const reuse = verificationReuseText(payload)
          if (reuse) current().rows.push(noteHtml('quiet', 'check', 'Проверки', reuse))
          const verdict = { implementation_ready: ['warn', 'warning', 'реализация сохранена; независимая приёмка не подтверждена'], preparation_failed: ['warn', 'warning', 'ошибка подготовки окружения; реализация сохранена'], revision_required: ['warn', 'warning', 'финал на доработку: нет проверяемого результата'], accepted_after_revision: ['ok', 'check', 'финал принят после проверки инструментом'], rejected: ['fail', 'x', 'финал отклонён: готовность не доказана'] }[payload.status]
          if (verdict) { close(); blocks.push({ kind: 'html', html: `<div class="hall-trail quest-trail">${noteHtml(verdict[0], verdict[1], 'Проверка финала', verdict[2])}</div>` }) }
          break
        }
        case 'approval.requested': {
          const approval = approvals.get(String(payload.id || ''))
          if (!approval || approval.toolName === 'propose_patch' || (withoutPending && approval.status === 'pending')) break
          const anchor = !anchored && approval.status === 'pending'
          if (anchor) anchored = true
          close()
          blocks.push({ kind: 'html', html: approvalCard(approval, anchor) })
          break
        }
        case 'patch.proposed': {
          const patch = patches.get(String(payload.id || ''))
          if (!patch) break
          const approval = approvals.get(patch.approvalId)
          const pending = approval?.status === 'pending'
          if (pending && withoutPending) break
          close()
          if (pending) {
            const anchor = !anchored
            anchored = true
            blocks.push({ kind: 'html', html: patchCard(patch, approval, anchor, sandboxOnly) })
          } else {
            blocks.push({ kind: 'html', html: fileHtml(patch, `journal-file:${runId}:${patch.id}`, sandboxOnly) })
          }
          break
        }
        case 'approval.resolved':
          if (payload.status === 'denied') current().alerts.push(noteHtml('warn', 'x', 'Подтверждение', 'отклонено'))
          break
        case 'run.completed':
          close()
          blocks.push({ kind: 'html', html: `<div class="hall-trail quest-trail">${noteHtml('ok', 'check', 'Этап агента завершён', `${countOf(Number(details.run.requestCount) || 0, 'запрос', 'запроса', 'запросов')} · ${countOf(list(details.run.changedFiles).length, 'файл', 'файла', 'файлов')} · проверка и доставка идут отдельно`)}</div>${questLevelUpBadge ? questLevelUpBadge(details.run) : ''}` })
          break
        case 'run.failed':
        case 'run.cancelled':
          close()
          blocks.push({ kind: 'html', html: `<div class="hall-trail quest-trail">${noteHtml('fail', 'x', event.type === 'run.cancelled' ? 'Этап остановлен' : 'Этап сорвался', coreFailureText(payload.error || payload.reason || details.run.error) || 'запуск остановлен')}</div>` })
          break
        default:
          break
      }
    }
    // Последняя группа идущего этапа раскрыта: это то, что происходит сейчас.
    const lastGroup = blocks.filter(block => block.kind === 'group').at(-1)
    const html = blocks.map((block, index) => block.kind === 'group'
      ? groupHtml(block, `journal:${runId}:${index}`, runLive && block === lastGroup)
      : block.html).join('')
    const cut = all.length > events.length
      ? `<div class="quest-journal-cut">Ранние события скрыты · показаны последние ${events.length} из ${all.length}</div>` : ''
    const now = nowHtml(details, openStep)
    if (!html && !now) return '<div class="quest-journal is-empty"><span>Ожидаем первые шаги агента…</span></div>'
    return `<div class="quest-journal">${cut}${html}${now}</div>`
  }

  return { questJournalHtml }
}
