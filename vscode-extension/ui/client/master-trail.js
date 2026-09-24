// След хода: что Мастер делал, прежде чем ответить.
//
// Готовый ход и ход, который ещё идёт, рисуются этим одним построителем. Пока
// их рисовали двое — список живого следа и строка сводки готового хода, — на
// конце хода лента прыгала: восемь строк действий схлопывались в одну, и ответ
// уезжал вверх прямо под читающим. Теперь, как только пошёл текст ответа, след
// потока уже стоит той же строкой сводки, что останется в ленте.
//
// След описывается моделью, а не репликой: `{ id, steps, reasoning, running }`.
// Ключ раскрытия — идентификатор хода (`turnId`), а не реплики: у хода, который
// ещё идёт, реплики нет, и раскрытое во время потока иначе схлопнулось бы на
// финише.

import { masterToolIcon, masterToolName } from './master-tool-names.js'
import { icon } from './ui-icons.js'

// Модель следа готовой реплики. Старые записи хода не знают — ключом тогда
// служит сама реплика, как и раньше.
export function trailModelFromItem(item) {
  return {
    id: String(item?.turnId || item?.id || ''),
    steps: Array.isArray(item?.steps) ? item.steps : [],
    reasoning: String(item?.reasoning || ''),
    running: false,
  }
}

// Модель следа живого хода: обращения к инструментам — шаги, мысли — одно
// рассуждение. Повтор хода и чтение навыка в сводку не входят: их нет и в
// сохранённой реплике, и строка сводки на финише разошлась бы со своей же
// строкой из потока.
export function trailModelFromTrace(turn) {
  const trace = Array.isArray(turn?.trace) ? turn.trace : []
  return {
    id: String(turn?.id || ''),
    steps: trace.filter(item => item.kind === 'tool').map(item => ({
      tool: item.tool, argument: item.argument, result: item.running ? '' : item.result,
      failed: Boolean(item.failed), truncated: Boolean(item.truncated),
    })),
    reasoning: trace.filter(item => item.kind === 'mind').map(item => String(item.text || '').trim()).filter(Boolean).join('\n\n'),
    running: trace.some(item => item.running),
  }
}

export function createMasterTrail({ esc, countOf, ui }) {
  // Модель часто рвёт рассуждение мягкими переносами («All\\nquestions»).
  // Собираем в читаемую прозу: одиночные переносы → пробел, абзацы и списки
  // оставляем. Иначе при white-space:pre-wrap лента выглядит столбиком слов.
  function masterReasoningProse(raw) {
    const lines = String(raw || '').replace(/\r\n?/g, '\n').split('\n')
    const out = []
    for (const line of lines) {
      const trimmed = line.replace(/[ \t]+/g, ' ').trimEnd()
      if (!trimmed.trim()) {
        if (out.length && out[out.length - 1] !== '') out.push('')
        continue
      }
      const body = trimmed.trimStart()
      const list = /^(?:[-*•]|\d+[.)])\s/.test(body)
      const prev = out[out.length - 1]
      if (prev === undefined || prev === '' || list) out.push(body)
      else out[out.length - 1] = `${prev} ${body}`
    }
    while (out.length && out[out.length - 1] === '') out.pop()
    return out.join('\n').replace(/\n{3,}/g, '\n\n')
  }

  // Как Мастер пришёл к ответу.
  //
  // Раскрыто по умолчанию рассуждение быть не должно: это черновик модели, и
  // он длиннее самого ответа. Прежнее правило открывало всё до четырёх тысяч
  // знаков — то есть почти всегда, — и ответ уезжал под простыню размышлений.
  // У эталона это одна тусклая строка «Thought 6s», раскрываемая по нажатию.
  // Текст экранирован: это недоверенный вывод модели.
  function masterReasoningHtml(model) {
    const raw = String(model.reasoning || '').trim()
    if (!raw) return ''
    const text = masterReasoningProse(raw)
    const open = ui.masterOpenReasoning.has(model.id) ? ' open' : ''
    return `<details class="hall-reason" data-master-open="reasoning" data-id="${esc(model.id)}"${open}>
      <summary><span class="hall-step-icon">${icon('think')}</span><span class="hall-reason-label">Ход мысли</span></summary>
      <div class="hall-reason-body">${esc(text)}</div>
    </details>`
  }

  // Что Мастер посмотрел в проекте, прежде чем ответить.
  //
  // Обрезанный результат назван обрезанным: ответ по первым 16 КБ файла читается
  // иначе, чем ответ по файлу целиком, и молчать об этом нельзя. Длинное можно
  // раскрыть кнопкой — title не заменяет чтение на месте.
  function masterStepClip(value, limit = 96) {
    const text = String(value || '').replace(/\s+/g, ' ').trim()
    if (text.length <= limit) return text
    return `${[...text].slice(0, limit - 1).join('')}…`
  }

  function masterStepPretty(value) {
    const raw = String(value || '').trim()
    if (!raw) return ''
    if (!(raw.startsWith('{') || raw.startsWith('['))) return raw
    try { return JSON.stringify(JSON.parse(raw), null, 2) } catch { return raw }
  }

  function masterStepResultLabel(step, { expanded = false } = {}) {
    const raw = String(step?.result || '').trim()
    if (expanded) return masterStepPretty(raw) || 'пусто'
    if (step?.failed) {
      const short = raw.replace(/^GetFileAttributesEx\s+/i, 'нет файла: ').replace(/:\s*The system.*$/i, '')
      return masterStepClip(short || 'инструмент не отработал', 120)
    }
    if (!raw) return 'пусто'
    if (raw === '[]' || raw === 'null') return 'ничего не найдено'
    if (raw.startsWith('{') || raw.startsWith('[')) {
      try {
        const parsed = JSON.parse(raw)
        if (Array.isArray(parsed)) return parsed.length ? countOf(parsed.length, 'запись', 'записи', 'записей') : 'ничего не найдено'
        if (parsed && typeof parsed === 'object') {
          if (parsed.count === 0 || parsed.candidateChunks === 0) return 'ничего не найдено'
          if (typeof parsed.count === 'number') return `${parsed.count} совпадений`
          if (Array.isArray(parsed.chunks) && parsed.chunks.length === 0) return 'ничего не найдено'
          if (Array.isArray(parsed.topDirectories) && parsed.topDirectories.length === 0 && Array.isArray(parsed.symbols) && parsed.symbols.length === 0) {
            return parsed.status?.state ? `карта · ${parsed.status.state}` : 'карта пуста'
          }
        }
      } catch { /* keep raw */ }
    }
    return masterStepClip(raw, 96)
  }

  function masterStepRowHtml(model, step, index) {
    const key = `${model.id}:${index}`
    const expanded = ui.masterExpandedSteps.has(key)
    const name = masterToolName(step.tool)
    const fullArg = String(step.argument || '').trim()
    const fullResult = String(step.result || '').trim()
    const clippedArg = masterStepClip(fullArg, 64)
    const shortResult = masterStepResultLabel(step)
    const canExpand = Boolean(step.truncated)
      || fullArg.length > 64
      || fullResult.length > 96
      || shortResult.endsWith('…')
    const more = canExpand
      ? `<button type="button" class="hall-step-more${expanded ? ' is-open' : ''}" data-action="master-step-expand" data-key="${esc(key)}" aria-label="${expanded ? 'Свернуть' : 'Показать полностью'}" title="${expanded ? 'Свернуть' : 'Показать полностью'}">${icon('chevron-down')}</button>`
      : ''
    // Полный вывод — в отдельном <pre>, не в span/small: иначе nowrap/line-clamp
    // переживают «раскрытие» и режут JSON многоточием прямо под кнопкой «Свернуть».
    const collapsed = expanded ? `<span class="hall-step-arg"></span>` : `<span class="hall-step-arg" title="${esc(fullArg)}">${esc(clippedArg || '—')}</span>
      <small title="${esc(fullResult)}">${esc(shortResult)}${step.truncated ? ' · обрезано' : ''}</small>`
    const payload = expanded
      ? `<pre class="hall-step-payload">${esc([fullArg && `→ ${fullArg}`, masterStepPretty(fullResult) || 'пусто'].filter(Boolean).join('\n\n'))}</pre>`
      : ''
    return `<div class="hall-step${step.failed ? ' is-failed' : ''}${expanded ? ' is-expanded' : ''}">
      <span class="hall-step-icon">${icon(step.failed ? 'warning' : masterToolIcon(step.tool))}</span>
      <b title="${esc(step.tool || '')}">${esc(name)}</b>
      ${collapsed}
      ${more}
      ${payload}
    </div>`
  }

  // Что Мастер делал, прежде чем ответить, — одна свёрнутая строка.
  //
  // Шаги стояли строками над ответом: последние три всегда, ранние за «ещё N»,
  // и ответ на простой вопрос начинался с пяти строк служебного вывода. Теперь
  // это одна строка сводки — какими средствами смотрел, сколько раз, была ли
  // ошибка, — а весь перечень с подробностями раскрывается по нажатию. Ход
  // мысли — первая строка того же перечня: это тоже путь к ответу, а не ответ.
  //
  // Неудачное обращение прятать нельзя: ответ, собранный с ошибкой инструмента,
  // читается иначе. Упавшие строки стоят под сводкой всегда и в перечне не
  // повторяются: одна строка на обращение, где бы её ни искали.
  const MASTER_ACTION_ICONS_MAX = 4

  function masterTrailHtml(model) {
    const steps = model.steps || []
    const reasoning = masterReasoningHtml(model)
    if (!steps.length && !reasoning) return ''
    const indexed = steps.map((step, index) => [step, index])
    const rows = indexed.filter(([step]) => !step.failed).map(([step, index]) => masterStepRowHtml(model, step, index)).join('')
    const failed = indexed.filter(([step]) => step.failed)
    const kinds = [...new Set(steps.map(step => masterToolIcon(step.tool)))].slice(0, MASTER_ACTION_ICONS_MAX)
    const icons = steps.length ? kinds.map(name => icon(name)).join('') : icon('think')
    const label = steps.length ? countOf(steps.length, 'действие', 'действия', 'действий') : 'Ход мысли'
    const fail = failed.length
      ? `<span class="hall-trail-fail">${icon('warning')}${esc(countOf(failed.length, 'ошибка', 'ошибки', 'ошибок'))}</span>`
      : ''
    const open = ui.masterOpenSteps.has(model.id) ? ' open' : ''
    const failedRows = failed.length
      ? `<div class="hall-trail-failed">${failed.map(([step, index]) => masterStepRowHtml(model, step, index)).join('')}</div>`
      : ''
    return `<div class="hall-trail${failed.length ? ' is-failed' : ''}${model.running ? ' is-running' : ''}">
      <details class="hall-trail-group" data-master-open="steps" data-id="${esc(model.id)}"${open}>
        <summary><span class="hall-trail-icons">${icons}</span><span class="hall-trail-label">${esc(label)}</span>${fail}<span class="hall-trail-chevron">${icon('chevron-right')}</span></summary>
        ${reasoning || rows ? `<div class="hall-trail-list">${reasoning}${rows}</div>` : ''}
      </details>
      ${failedRows}
    </div>`
  }

  return { masterTrailHtml }
}
