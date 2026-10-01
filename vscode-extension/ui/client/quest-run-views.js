// Прогон квеста в ленте Мастера: идущий, остановленный и законченный.
//
// Прежняя строка прогона называла цель и слово состояния, а всё остальное —
// перечень этапов, хроника рамками, форма управления, сетка из шести ячеек
// «EvidenceBundle · acceptance · exit 0 · $0.00 · sha256…» — лежало одним
// столбцом под ней. Чтобы понять, где работа сейчас, чем кончилась и что с ней
// делать, приходилось читать всё сверху вниз.
//
// Здесь у каждого вопроса своё место:
// — идёт ли работа и сколько — строка: точка состояния, цель, слово, время;
// — где она — сегментная полоса этапов и строка «этап · агент · модель»;
// — что агент делает — журнал этапа (quest-journal-views.js);
// — чем кончилось — полоса вердикта и действия, затем условия с тем, чем
//   каждое доказано; остальное (этапы, расход, технические детали) свёрнуто.
//
// Слово состояния окрашено по канону docs/RPG-DESIGN-SYSTEM.md: идёт — тоном
// ссылки темы, ждёт человека — --caution, готово — --vital, провал — --wound.
// Всё, что пришло из ядра по-английски и что можно назвать без участия модели,
// названо по-русски; английское остаётся в подсказке по наведению.

import { masterPlanState } from './master-plan-views.js'
import { countOf, formatCompactCount, formatDuration, formatElapsed, list, plural } from './format-units.js'
import { masterCardMoreAttrs } from './master-card-open.js'
import { icon } from './ui-icons.js'
import { questChecklistHtml } from './master-quest-views.js'
import { stageFlowNode, stageLabel, stageLabelText } from './stage-labels.js'
import { activeStage, requireEsc, stageSpan, workOrderExecutionParts } from './work-order-execution-views.js'
import { diffCountHtml, diffStats } from './diff-view.js'
import { questApplicationHtml, questReportHtml } from './quest-app-views.js'
import { preparedFilesHtml } from './quest-prepared-views.js'
import { questRetrospectiveHtml } from './quest-retrospective-views.js'
import { preAcceptNoteHtml } from './stage-failure-views.js'

const LIVE = new Set(['preflight', 'running', 'verifying', 'applying'])
// Работающий или остановленный квест остаётся частью ленты разговора: этапы,
// причины ожидания и журнал видны без раскрытия. Законченный раскрывают из
// истории.
const IN_FEED = new Set([...LIVE, 'paused', 'awaiting_user', 'blocked'])

// Проверки профиля завершения — закрытый список ядра
// (supportedCompletionCheckV2, internal/domain/work_order_v2.go).
const CHECK_KIND = {
  acceptance: 'Условие готовности', build: 'Сборка', migrations: 'Миграции', automated_tests: 'Автотесты',
  service_start: 'Запуск сервисов', health: 'Проверка здоровья', http_smoke: 'Проверка HTTP',
  browser_journey: 'Сценарий в браузере', browser_errors: 'Ошибки браузера', accessibility: 'Доступность',
  screenshots: 'Снимки экрана', fixtures: 'Тестовые данные', live_smoke: 'Живая проверка',
  dependency_audit: 'Аудит зависимостей', secret_scan: 'Поиск секретов', container_config: 'Настройка контейнеров',
  performance: 'Производительность', readme: 'Документация запуска', cli_smoke: 'Проверка командной строки',
}

// Вид проверки завершения — по-русски; незнакомый вид остаётся как есть,
// чтобы новое значение ядра было видно, а не пряталось за общим словом.
export function completionCheckName(kind) {
  return CHECK_KIND[String(kind || '')] || String(kind || 'Проверка')
}

function stamp(value) {
  const time = Date.parse(value || '')
  return Number.isNaN(time) ? null : time
}

// Сколько шла работа квеста: от запуска до конца последнего этапа, у идущего —
// до сейчас.
function questSpan(runtime, live, now = Date.now()) {
  const stages = list(runtime.stages)
  const starts = [stamp(runtime.launchStartedAt), ...stages.map(stage => stamp(stage.startedAt))].filter(value => value != null)
  if (!starts.length) return null
  const ends = stages.map(stage => stamp(stage.finishedAt)).filter(value => value != null)
  const end = live ? now : (ends.length ? Math.max(...ends) : stamp(runtime.updatedAt))
  return end == null ? null : Math.max(0, end - Math.min(...starts))
}

// Имя исполнителя: ростер проекта, затем состав наряда. Этап без агента у наряда
// с одним исполнителем принадлежит ему.
function agentName(order, ui, id) {
  const roster = [...list(order.roster?.permanent), ...list(order.roster?.temporary)]
  return (id && (list(ui?.state?.boot?.projectAgents).find(item => item.id === id)?.name || roster.find(item => item?.id === id)?.name))
    || (roster.length === 1 ? String(roster[0]?.name || '') : '')
}

// Модель этапа: идущий прогон знает её точно, иначе — привязка узла Flow.
function stageModel(order, ui, stage, node) {
  const details = ui?.state?.details
  if (details?.run?.id && details.run.id === stage?.runId && details.run.model) return details.run.model
  const run = list(ui?.state?.boot?.runs).find(item => item.id === stage?.runId)
  return run?.model || node?.config?.modelBinding?.model || order.routing?.fixedModel || ''
}

function stageTone(stage) {
  if (['failed', 'cancelled'].includes(stage.status) || ['start_failed', 'stage_failed'].includes(stage.waitReason)) return 'fail'
  return masterPlanState(stage.status)
}

// Сегментная полоса: по сегменту на этап, тоном его состояния. Ширина сегментов
// — сеткой слоя, а не стилем: CSP вебвью выбрасывает style="".
function trackHtml(order, ui, deps, stages, esc) {
  if (!stages.length) return ''
  const done = stages.filter(stage => masterPlanState(stage.status) === 'done').length
  const cells = stages.map(stage => {
    const name = stageLabelText(stage, { node: stageFlowNode(ui?.state?.boot, order.runtime, stage), kindLabels: deps.flowNodeKindLabels || {} })
    return `<i class="is-${stageTone(stage)}" title="${esc(name)}"></i>`
  }).join('')
  return `<span class="quest-track" role="img" aria-label="Этапы: ${done} из ${stages.length}">${cells}</span>`
}

// «Этап 2 из 7 · Реализация · Разработчик · Qwen3.8-27B».
function nowLineHtml(order, ui, deps, stages, esc) {
  const stage = activeStage(stages)
  if (!stage) return ''
  const node = stageFlowNode(ui?.state?.boot, order.runtime, stage)
  const { label, detail } = stageLabel(stage, { node, kindLabels: deps.flowNodeKindLabels || {} })
  const who = [agentName(order, ui, stage.agentId), stageModel(order, ui, stage, node)].filter(Boolean).join(' · ')
  return `<div class="quest-now-line"><span>Этап ${stages.indexOf(stage) + 1} из ${stages.length}</span><b>${esc(label)}</b>${detail ? `<span class="quest-now-detail" title="${esc(detail)}">${esc(detail)}</span>` : ''}${who ? `<span class="quest-now-who">${esc(who)}</span>` : ''}</div>`
}

function controlAttrs(order, control, esc) {
  return `data-action="control-master-work-order-v2" data-control="${control}" data-id="${esc(order.id)}" data-quest-id="${esc(order.runtime?.questId || '')}"`
}

// Пауза и отмена — значками в строке идущего квеста: они меняют ход работы, и
// прятать их нельзя, но и форма из двух кнопок над журналом не нужна.
function headControlsHtml(order, controls, esc) {
  if (!order.runtime?.questId) return ''
  const disabled = controls.busy ? ' disabled' : ''
  const buttons = [
    controls.pausable ? `<button type="button" class="quest-icon-btn" ${controlAttrs(order, 'pause', esc)} aria-label="Пауза" title="Пауза"${disabled}>${icon('pause')}</button>` : '',
    controls.cancellable ? `<button type="button" class="quest-icon-btn" ${controlAttrs(order, 'cancel', esc)} aria-label="Отменить квест" title="Отменить квест"${disabled}>${icon('stop')}</button>` : '',
  ].join('')
  return buttons ? `<span class="quest-run-controls">${buttons}</span>` : ''
}

// Возобновление, починка песочницы и уточнение агенту — под журналом: сперва
// читают, что делает агент, потом решают, вмешиваться ли.
function bodyControlsHtml(order, controls, esc, { withCancel = false } = {}) {
  if (!order.runtime?.questId) return ''
  const disabled = controls.busy ? ' disabled' : ''
  const buttons = [
    controls.resumable ? `<button type="button" class="hall-btn is-primary" ${controlAttrs(order, 'resume', esc)}${disabled}>${esc(controls.resumeLabel)}</button>` : '',
    controls.sandboxFix ? '<button type="button" class="hall-btn" data-action="enable-docker-sandbox">Включить Docker sandbox</button>' : '',
    withCancel && controls.cancellable ? `<button type="button" class="hall-btn" ${controlAttrs(order, 'cancel', esc)}${disabled}>Отменить квест</button>` : '',
  ].join('')
  const message = controls.messageable ? `<label class="quest-message"><span class="hall-sr">Сообщение активному квесту</span><input data-work-order-message maxlength="32768" placeholder="Уточнение агенту — без изменения задачи"><button type="button" class="quest-icon-btn" ${controlAttrs(order, 'message', esc)} aria-label="Отправить агенту" title="Отправить агенту"${disabled}>${icon('send')}</button></label>` : ''
  return buttons || message ? `<div class="quest-actions">${buttons ? `<div>${buttons}</div>` : ''}${message}</div>` : ''
}

// Причина шлюза доказательств приходит английской машинной строкой
// («work is not proven: criterion:health-ok»). Имена условий в ней — ключи,
// а не текст; называем их так, как их видит человек. Сырая строка — в подсказке.
function limitationText(raw, criteria) {
  const names = new Map(criteria.map(row => [String(row.id), row.text]))
  return String(raw || '')
    .replace(/work is not proven:?/i, 'не доказано:')
    .replace(/criterion:([\w.-]+)/g, (_, id) => names.has(id) ? `«${names.get(id)}»` : `условие ${id}`)
    .replace(/\bdelivery_verified\b/g, 'доставка не подтверждена')
}

// Полоса вердикта: чем кончилось, куда перенесено, что осталось.
function verdictHtml(order, criteria, tone, esc) {
  const runtime = order.runtime || {}
  const evidence = runtime.evidence || {}
  const receipt = runtime.deliveryReceipt || {}
  const target = String(evidence.deliveryTarget || receipt.target || '')
  const done = criteria.filter(row => row.done).length
  const lines = [
    runtime.message ? `<p>${esc(runtime.message)}</p>` : '',
    target === 'isolated_review' ? '<p>Результат оставлен в изоляции для ручной приёмки.</p>'
      : receipt.id && target ? `<p>Перенесено в <code>${esc(target)}</code>.</p>` : '',
    evidence.id && criteria.length ? `<p>Подтверждено ${done} из ${countOf(criteria.length, 'условия', 'условий', 'условий')}.</p>` : '',
  ].filter(Boolean)
  const limits = list(evidence.knownLimitations)
  const limitsHtml = limits.length ? `<ul>${limits.map(item => `<li title="${esc(item)}">${esc(limitationText(item, criteria))}</li>`).join('')}</ul>` : ''
  if (!lines.length && !limitsHtml) return ''
  return `<div class="quest-verdict ${esc(tone)}">${lines.join('')}${limitsHtml}</div>`
}

// Отмена ручной приёмки — единственное действие ждущего человека квеста.
function finishedActionsHtml(order, controls, esc) {
  const runtime = order.runtime || {}
  if (!(runtime.status === 'needs_review' && controls.cancellable && runtime.questId)) return ''
  return `<div class="quest-actions"><div><button type="button" class="hall-btn" ${controlAttrs(order, 'cancel', esc)}${controls.busy ? ' disabled' : ''}>Отменить квест</button></div></div>`
}

// Условия готовности вместе с тем, чем каждое доказано: команда, код, время.
// Хвост вывода — в подсказке.
const NOT_PROVEN = { not_run: 'не запускалась', unavailable: 'проверяется после доставки' }
function criteriaRowsWithProof(order, rows) {
  const evidence = order.runtime?.evidence
  const proofs = new Map(list(evidence?.criteria).map(item => [String(item.criterionId), item]))
  return rows.map(row => {
    const proof = proofs.get(String(row.id))
    if (!proof) return row
    // Ядро называет исход поштучно; «не запускалась» — не провал. Старые пакеты без status — по-прежнему.
    const status = String(proof.status || '')
    const meta = [proof.command, proof.command && proof.exitCode != null ? `код ${Number(proof.exitCode)}` : '', proof.durationMs ? formatDuration(proof.durationMs) : '', NOT_PROVEN[status] || ''].filter(Boolean).join(' · ')
    return { ...row, meta, title: String(proof.summary || ''), failed: status ? status === 'failed' : Boolean(evidence?.id) && !row.done && row.kind !== 'manual' }
  })
}

// Проверки профиля завершения: сборка, тесты, запуск сервисов. Проверки
// условий здесь не повторяются — они уже стоят у своих условий.
function completionChecksHtml(order, esc) {
  const checks = list(order.runtime?.evidence?.verificationChecks).filter(item => item.kind !== 'acceptance')
  if (!checks.length) return ''
  return `<ul class="quest-checks">${checks.map(item => {
    const meta = [item.command ? `<code>${esc(item.command)}</code>` : '', item.exitCode != null ? `код ${Number(item.exitCode)}` : '', item.durationMs ? esc(formatDuration(item.durationMs)) : ''].filter(Boolean).join(' · ')
    return `<li class="${item.satisfied ? 'is-done' : 'is-failed'}"${item.summary ? ` title="${esc(item.summary)}"` : ''}><span class="hall-step-icon">${icon(item.satisfied ? 'check' : 'x')}</span><b>${esc(CHECK_KIND[item.kind] || 'Проверка')}</b><span>${meta}</span></li>`
  }).join('')}</ul>`
}

// Что изменилось: пути из пакета доказательств. Счёт строк — там, где diff
// известен (журнал загруженного этапа), иначе без него.
function changesHtml(order, ui, esc) {
  const runtime = order.runtime || {}
  const evidence = runtime.evidence
  const files = list(evidence?.changedFiles)
  // Подготовленное, но не доставленное — не «нет изменений» и не доставка:
  // diff, причина и выход — в quest-prepared-views.js.
  const preparedHtml = preparedFilesHtml(order, ui, esc)
  if (!files.length) return preparedHtml
  const target = String(evidence.deliveryTarget || runtime.deliveryReceipt?.target || '')
  const delivered = Boolean(runtime.deliveryReceipt?.id) && target !== 'isolated_review'
  const details = ui?.state?.details
  const diffs = new Map()
  if (details?.run?.questId && details.run.questId === runtime.questId) {
    for (const patch of list(details.patches)) if (patch?.path) diffs.set(String(patch.path), patch.diff)
  }
  const shown = files.slice(0, 12)
  const rows = shown.map(path => {
    const name = delivered
      ? `<button type="button" class="quest-file-link" data-action="open-file" data-path="${esc(path)}" title="Открыть в редакторе">${esc(path)}</button>`
      : `<span>${esc(path)}</span>`
    return `<li><span class="hall-step-icon">${icon('file')}</span>${name}${diffCountHtml(diffStats(diffs.get(String(path))))}</li>`
  }).join('')
  const rest = files.length - shown.length
  return `<div class="quest-section"><h4>Изменения · ${countOf(files.length, 'файл', 'файла', 'файлов')}</h4><ul class="quest-files">${rows}</ul>${rest > 0 ? `<small class="quest-more">и ещё ${countOf(rest, 'файл', 'файла', 'файлов')}</small>` : ''}</div>${preparedHtml}`
}

function tokensText(tokens) {
  const value = Math.max(0, Number(tokens) || 0)
  return value >= 10_000 ? `${formatCompactCount(value)} токенов` : `${value.toLocaleString('ru-RU')} ${plural(value, 'токен', 'токена', 'токенов')}`
}

function costText(calls) {
  if (!calls.length) return ''
  const unknown = calls.filter(item => !item.costKnown).length
  const cents = calls.filter(item => item.costKnown).reduce((sum, item) => sum + Number(item.costCents || 0), 0)
  if (unknown === calls.length) return 'стоимость неизвестна'
  if (!cents && !unknown) return 'бесплатно'
  return `$${(cents / 100).toFixed(2)}${unknown ? ` · ${countOf(unknown, 'вызов', 'вызова', 'вызовов')} без цены` : ''}`
}

// Расход: модели, вызовы, токены, время модели, стоимость.
function spendHtml(order, esc) {
  const calls = list(order.runtime?.evidence?.modelCalls)
  if (!calls.length) return ''
  const input = calls.reduce((sum, item) => sum + Number(item.inputTokens || 0), 0)
  const output = calls.reduce((sum, item) => sum + Number(item.outputTokens || 0), 0)
  const active = calls.reduce((sum, item) => sum + Number(item.activeMillis || 0), 0)
  const models = new Map()
  for (const item of calls) if (item.model) models.set(item.model, (models.get(item.model) || 0) + 1)
  const total = input + output
  const rows = [
    models.size ? ['Модели', [...models].map(([model, count]) => `${model} · ${countOf(count, 'вызов', 'вызова', 'вызовов')}`).join(', ')] : ['Вызовы', countOf(calls.length, 'вызов', 'вызова', 'вызовов')],
    ['Токены', `${total.toLocaleString('ru-RU')} ${plural(total, 'токен', 'токена', 'токенов')} · ввод ${input.toLocaleString('ru-RU')} · вывод ${output.toLocaleString('ru-RU')}`],
    active ? ['Время модели', formatElapsed(active, { seconds: true })] : null,
    ['Стоимость', costText(calls)],
  ].filter(Boolean)
  return `<details class="quest-fold"${masterCardMoreAttrs(`run-spend:${order.id}`, { esc })}><summary>Расход · ${countOf(calls.length, 'вызов', 'вызова', 'вызовов')} · ${tokensText(total)}</summary>
    <dl class="quest-facts">${rows.map(([name, value]) => `<dt>${esc(name)}</dt><dd>${esc(value)}</dd>`).join('')}</dl>
  </details>`
}

// Технические детали: всё, что нужно для сверки, но не для решения.
function technicalHtml(order, esc) {
  const runtime = order.runtime || {}
  const evidence = runtime.evidence
  if (!evidence?.id) return ''
  const receipt = runtime.deliveryReceipt || {}
  const revision = String(evidence.workspaceRevision || receipt.workspaceRevision || '')
  const commit = list(evidence.commitIds).at(-1) || receipt.commitId || ''
  const target = String(evidence.deliveryTarget || receipt.target || '')
  const rows = [
    ['Пакет доказательств', `<code>${esc(evidence.id)}</code> · версия ${Number(evidence.version) || 0}`],
    revision ? ['Ревизия', `<code title="${esc(revision)}">${esc(revision.replace(/^sha256:/, '').slice(0, 12))}</code>`] : null,
    ['Коммит', commit ? `<code>${esc(commit)}</code>` : 'без итогового коммита'],
    target ? ['Доставка', target === 'isolated_review' ? 'изоляция для ручной приёмки' : `<code>${esc(target)}</code>`] : null,
  ].filter(Boolean)
  return `<details class="quest-fold"${masterCardMoreAttrs(`run-tech:${order.id}`, { esc })}><summary>Технические детали</summary>
    <dl class="quest-facts">${rows.map(([name, value]) => `<dt>${esc(name)}</dt><dd>${value}</dd>`).join('')}</dl>
  </details>`
}

// Итог в свёрнутой строке: условия, файлы, токены, цена.
function chipsHtml(order, criteria, esc) {
  const runtime = order.runtime || {}
  const evidence = runtime.evidence
  if (!evidence?.id) return ''
  const done = criteria.filter(row => row.done).length
  const files = list(evidence.changedFiles).length
  const calls = list(evidence.modelCalls)
  const tokens = calls.reduce((sum, item) => sum + Number(item.inputTokens || 0) + Number(item.outputTokens || 0), 0)
  const cost = costText(calls)
  const chips = [
    criteria.length ? [`${done}/${criteria.length} ${plural(criteria.length, 'условие', 'условия', 'условий')}`, done === criteria.length ? 'is-done' : 'is-short', ''] : null,
    files ? [countOf(files, 'файл', 'файла', 'файлов'), '', ''] : null,
    tokens ? [tokensText(tokens), '', `${tokens.toLocaleString('ru-RU')} ${plural(tokens, 'токен', 'токена', 'токенов')}`] : null,
    cost && cost !== 'стоимость неизвестна' ? [cost, '', ''] : null,
  ].filter(Boolean)
  if (!chips.length) return ''
  return `<div class="hall-quest-chips">${chips.map(([text, tone, title]) => `<span class="hall-quest-chip${tone ? ` ${tone}` : ''}"${title ? ` title="${esc(title)}"` : ''}>${esc(text)}</span>`).join('')}</div>`
}

// Строка прогона и тело — по состоянию квеста.
//
// `deps` приходят от карточки наряда (master-work-order-v2.js): esc, ui,
// журнал, имена видов узлов, представление состояния, флаги управления, ряды
// условий, созданные исполнители, ручная приёмка и состав задания.
export function questRunHtml(order, ui, deps = {}) {
  const esc = requireEsc(deps)
  const runtime = order?.runtime || {}
  const status = String(runtime.status || '')
  const tone = deps.tone || ''
  const live = LIVE.has(status)
  const inFeed = IN_FEED.has(status)
  const stages = list(runtime.stages)
  const controls = deps.controls || {}
  const criteria = criteriaRowsWithProof(order, list(deps.criteriaRows))
  const parts = workOrderExecutionParts(order, ui, deps) || {}
  const span = questSpan(runtime, live)
  const time = span != null ? `<span class="quest-run-time" title="Время работы: ${esc(formatElapsed(span, { seconds: true }))}">${esc(formatElapsed(span))}</span>` : ''
  const title = `
          <span class="hall-quest-row">
            <span class="hall-quest-dot" aria-hidden="true"></span>
            <strong class="hall-quest-name">${esc(order.goal || 'Задание')}</strong>
            <span class="master-v2-approved ${esc(tone)}">${live ? '' : esc(deps.mark || '·') + ' '}${esc(deps.label || status)}</span>
            ${time}
            ${inFeed ? headControlsHtml(order, controls, esc) : ''}
          </span>
          ${inFeed ? `${trackHtml(order, ui, deps, stages, esc)}${nowLineHtml(order, ui, deps, stages, esc)}` : chipsHtml(order, criteria, esc)}`
  const finished = !inFeed
  const hasEvidence = Boolean(runtime.evidence?.id)
  const checklist = questChecklistHtml(hasEvidence ? 'Условия и доказательства' : 'Условия готовности', criteria, esc, { empty: 'Условия готовности не заданы' })
  // Этапы у идущего квеста свёрнуты: где работа, говорит полоса. Раскрыты
  // там, где этап стоит и человеку нужно найти, какой.
  const stagesOpen = Boolean(runtime.stall) || tone === 'is-attention' || tone === 'is-failed'
  const stagesFold = stages.length || parts.plan?.includes('Запуск плана')
    ? `<details class="quest-fold quest-stages"${masterCardMoreAttrs(`run-stages:${order.id}`, { esc, open: stagesOpen && !finished })}><summary>Все этапы${stages.length ? ` · ${stages.filter(stage => masterPlanState(stage.status) === 'done').length} из ${stages.length}` : ''}</summary>${parts.plan || ''}</details>`
    : ''
  // Журнал последнего этапа у законченного квеста — под свёрткой: он о том,
  // как работа шла, а не о том, чем кончилась.
  const logFold = finished && parts.hasLog
    ? `<details class="quest-fold"${masterCardMoreAttrs(`run-log:${order.id}`, { esc })}><summary>Журнал последнего этапа</summary>${parts.log}</details>` : ''
  const body = finished ? `
        ${verdictHtml(order, criteria, tone, esc)}
        ${questApplicationHtml(order, controls, esc)}
        ${questReportHtml(order, esc)}
        ${finishedActionsHtml(order, controls, esc)}
        ${deps.manualReviewHtml || ''}
        ${questRetrospectiveHtml(order, ui, esc)}
        ${checklist}
        ${completionChecksHtml(order, esc)}
        ${changesHtml(order, ui, esc)}
        ${deps.createdHtml || ''}
        ${stagesFold}
        ${logFold}
        ${spendHtml(order, esc)}
        ${technicalHtml(order, esc)}
        ${deps.compositionHtml || ''}` : `
        ${parts.stall || ''}
        ${hasEvidence ? verdictHtml(order, criteria, tone, esc) : (!runtime.stall && !live && runtime.message ? `<div class="quest-verdict ${esc(tone)}"><p>${esc(runtime.message)}</p></div>` : '')}
        ${checklist}
        ${questRetrospectiveHtml(order, ui, esc)}
        ${deps.createdHtml || ''}
        ${parts.provisioning || ''}
        ${preAcceptNoteHtml(runtime, esc)}
        ${/* Остановленный вердиктом квест журнала не ждёт: «Загружаем журнал…»
             у него висело бы вечно. Причину затыка и пустой идущий этап пустота
             объясняет сама. */''}
        ${parts.hasLog || live || runtime.stall ? parts.log || '' : ''}
        ${parts.plannerNote || ''}
        ${bodyControlsHtml(order, controls, esc)}
        ${stagesFold}
        ${hasEvidence ? `${completionChecksHtml(order, esc)}${changesHtml(order, ui, esc)}${spendHtml(order, esc)}${technicalHtml(order, esc)}` : ''}
        ${deps.compositionHtml || ''}`
  const shell = `<section class="work-order-exec" data-work-order-execution="${esc(order.id)}" data-quest-id="${esc(runtime.questId || '')}">${body}</section>`
  // Ручная приёмка ждёт человека — раскрыта сразу, как и остановленный квест.
  const openByDefault = status === 'needs_review'
  return `<section class="master-v2-run quest-run ${esc(tone)}${live ? ' is-live' : ''}" data-work-order-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId || '')}">
    ${inFeed ? `<div class="hall-quest-run is-in-feed"><div class="hall-quest-run-title">${title}</div>${shell}</div>`
      : `<details class="hall-quest-run"${masterCardMoreAttrs(`run-live:${order.id}`, { esc, open: openByDefault })}><summary>${title}</summary>${shell}</details>`}
  </section>`
}

// Итоговое сообщение ядра о квесте (`mode: quest_completion`).
//
// Ядро пишет его текстом — «Готово / Изменено файлов: 5 / Файлы: … /
// Выполненные проверки: …», — и сразу под ним стоит карточка, которая говорит
// то же самое строкой, чипами и доказательствами. Стена текста над карточкой
// читалась как второй, другой отчёт. Остаются первая строка — вердикт — и
// строка «Нужно действие», если она есть; полный текст — под раскрытием.
export function questCompletionMessageHtml(item, esc, formatMarkdown) {
  const lines = String(item?.content || '').split(/\r?\n/).map(line => line.trim()).filter(Boolean)
  if (!lines.length) return ''
  const action = lines.find(line => /^Нужно действие:/i.test(line))
  const key = `quest-report:${item.id || ''}`
  return `<div class="quest-report">
    <p class="quest-report-head">${esc(lines[0])}</p>
    ${action ? `<p class="quest-report-action">${esc(action)}</p>` : ''}
    <details class="quest-fold"${masterCardMoreAttrs(key, { esc })}><summary>Полный отчёт ядра</summary><div class="quest-report-body">${formatMarkdown(lines.join('\n'))}</div></details>
  </div>`
}
