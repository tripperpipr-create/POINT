// Что Мастер делает прямо сейчас — строками в ленте, пока идёт ход.
//
// До этого всё ожидание описывалось одним словом: «Изучаю проект…». Полторы
// минуты такой строки неотличимы от зависшей модели, и человек не знал ни что
// читается, ни сколько кругов уже прошло. Ядро знало это с самого начала —
// рассуждение и обращения к инструментам шли через поток, — но рассказывало о
// них задним числом, уже готовой репликой.
//
// Свёрнуто — одна тихая строка на событие: «Размышление», «Читаю файл». Всё
// остальное за нажатием: раскрытая строка показывает мысль целиком, аргумент
// обращения и то, чем оно кончилось. Вид повторяет готовый ход (hall-reason и
// hall-step), чтобы при завершении лента не прыгала.

import { masterToolIcon, masterToolNameNow } from './master-tool-names.js'
import { countOf } from './format-units.js'
import { icon } from './ui-icons.js'

// Сколько строк следа видно, пока идёт ход. Длинный ход с двадцатью
// обращениями вытягивал список на полэкрана, и то, что происходит сейчас,
// приходилось искать внизу; ранние строки уходят под «ещё N».
const MASTER_TRACE_VISIBLE = 3

// Сколько строк живёт в трассе. Длинный ход с десятками обращений не должен
// вытеснять из памяти сам разговор; ранние строки всё равно вернутся готовым
// ходом, где у них своя раскрывашка.
const MASTER_TRACE_LIMIT = 200

function detailOf (event) {
  if (!event || !event.detail) return {}
  try {
    const parsed = JSON.parse(event.detail)
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

// Строка события в трассе хода. Мысль обновляется на месте: она приходит
// накопленной, а не кусками, и новая строка на каждую дельту превратила бы
// ленту в бегущий столбец.
export function masterTraceAccept (turn, event, now = Date.now()) {
  if (!turn || !event) return turn
  const trace = Array.isArray(turn.trace) ? turn.trace : (turn.trace = [])
  const detail = detailOf(event)
  const last = trace[trace.length - 1]
  if (event.type === 'skill') {
	trace.push({kind:'skill',argument:event.text,result:`v${detail.revision || 1} · ${detail.digest || ''}`,running:false,startedAt:now,at:now})
  } else if (event.type === 'reasoning') {
    // Мысль приходит приростом: ядро шлёт только то, что появилось с прошлой
    // отправки, иначе журнал одного хода вырастал бы на мегабайты одного и
    // того же текста.
    const delta = String(detail.delta || detail.text || '')
    if (!delta.trim()) return turn
    if (last && last.kind === 'mind' && last.running) {
      last.text = (last.text || '') + delta
      last.at = now
    } else {
      trace.push({ kind: 'mind', text: delta, round: detail.round || 0, running: true, startedAt: now, at: now })
    }
  } else if (event.type === 'tools') {
    // Обращение начинается — мысль, что к нему привела, закончена.
    if (last && last.running) last.running = false
    const tool = String(detail.tool || event.text || '').trim()
    if (!tool) return turn
    trace.push({
      kind: 'tool', tool, argument: String(detail.argument || ''),
      round: detail.round || 0, running: true, startedAt: now, at: now,
    })
  } else if (event.type === 'retry') {
    // Повтор хода. Идёт он столько же, сколько первая попытка, и без строки в
    // ленте выглядит как зависшая модель: тот же «Ожидаю модель…», только
    // теперь второй раз подряд.
    if (last && last.running) last.running = false
    trace.push({
      kind: 'retry', what: String(detail.what || event.text || '').trim(),
      reason: String(detail.reason || ''), from: Number(detail.from || 0), to: Number(detail.to || 0),
      round: detail.round || 0, running: true, startedAt: now, at: now,
    })
  } else if (event.type === 'tool_result') {
    const tool = String(detail.tool || '').trim()
    for (let i = trace.length - 1; i >= 0; i--) {
      const item = trace[i]
      if (item.kind !== 'tool' || (tool && item.tool !== tool) || !item.running) continue
      item.running = false
      item.result = String(detail.result || '')
      item.failed = Boolean(detail.failed)
      item.truncated = Boolean(detail.truncated)
      item.at = now
      break
    }
  } else if (event.type === 'done' || event.type === 'reply') {
    for (const item of trace) item.running = false
  }
  if (trace.length > MASTER_TRACE_LIMIT) trace.splice(0, trace.length - MASTER_TRACE_LIMIT)
  return turn
}

// Подпись свёрнутой строки. Незнакомое имя инструмента не выдаём за знакомое:
// словарь закрыт ядром, и «обратился к инструменту» честнее выдумки.
export function masterTraceTitle (item) {
  if (!item) return ''
  if (item.kind === 'mind') return 'Размышление'
  if (item.kind === 'retry') return 'Вторая попытка'
	if (item.kind === 'skill') return 'Рабочий навык'
  const name = masterToolNameNow(item.tool)
  if (!name) return 'Вызов инструмента'
  return name.charAt(0).toUpperCase() + name.slice(1)
}

// Сколько шла строка. Секунды показываются от двух: «1 с» у мгновенного
// чтения — шум, а не сведения.
export function masterTraceDuration (item, now = Date.now()) {
  if (!item || !item.startedAt) return ''
  const end = item.running ? now : (item.at || item.startedAt)
  const seconds = Math.round((end - item.startedAt) / 1000)
  if (seconds < 2) return ''
  if (seconds < 60) return `${seconds} с`
  return `${Math.floor(seconds / 60)} мин ${seconds % 60} с`
}

// Последняя строка мысли — единственное, что видно в свёрнутом виде. Хвост, а
// не начало: свежая часть объясняет происходящее сейчас.
function masterTraceTail (text, limit = 120) {
  const lines = String(text || '').replace(/\r\n?/g, '\n').split('\n').map(line => line.trim()).filter(Boolean)
  const tail = lines[lines.length - 1] || ''
  const runes = [...tail]
  return runes.length > limit ? '…' + runes.slice(runes.length - limit).join('') : tail
}

// Почему ход пошёл на второй круг. Первая попытка кончилась ничем — весь предел
// вывода ушёл в размышление, — и человеку важно знать не «повтор», а что
// изменилось во второй попытке: иначе он видит просто удвоенное ожидание.
function masterRetryBody (item) {
  const lines = ['Первая попытка кончилась без ответа: весь предел вывода ушёл в размышление.']
  if (item.reason === 'reasoning_budget' && item.to) {
    lines.push(`Повтор с бо́льшим пределом вывода: ${item.from || '—'} → ${item.to} токенов.`)
  } else if (item.reason === 'disable_thinking') {
    lines.push(`Расти пределу вывода больше некуда (${item.from || item.to || '—'}), повтор без размышления.`)
  } else if (item.what) {
    lines.push(item.what)
  }
  return lines.join('\n\n')
}

// Значок строки следа — тот же, что у готового хода: живой след и сводка
// после ответа описывают одни и те же обращения и не должны выглядеть по-разному.
function masterTraceIcon (item) {
  if (item.failed) return 'warning'
  if (item.kind === 'mind') return 'think'
  if (item.kind === 'retry') return 'retry'
  if (item.kind === 'skill') return 'memory'
  return masterToolIcon(item.tool)
}

// Разметка живой трассы. Раскрытие живёт снаружи, в наборе открытых ключей:
// лента перерисовывается на каждое событие, и состояние, оставленное в DOM,
// схлопывалось бы прямо под читающим.
export function masterTraceHtml (turn, esc, open, now = Date.now()) {
  const trace = Array.isArray(turn?.trace) ? turn.trace : []
  if (!trace.length) return ''
  // Ключи строк — сквозные номера по всему следу: раскрытое помнится по ним, и
  // masterTraceMindPatch находит последнюю строку тем же ключом.
  const rows = trace.map((item, index) => {
    const key = `${turn.id || 'turn'}:${index}`
    const expanded = open?.has?.(key) ? ' open' : ''
    const duration = masterTraceDuration(item, now)
    const hint = item.kind === 'mind' ? masterTraceTail(item.text)
      : item.kind === 'retry' ? item.what
        : [item.argument, item.running ? '' : item.result].filter(Boolean).join(' · ')
    const body = item.kind === 'mind' ? item.text
      : item.kind === 'retry' ? masterRetryBody(item)
        : [item.argument && `→ ${item.argument}`, item.running ? 'идёт…' : (item.result || 'пусто')].filter(Boolean).join('\n\n')
    const state = [
      'hall-live-row',
      item.kind === 'mind' ? 'is-mind' : item.kind === 'retry' ? 'is-retry' : 'is-tool',
      item.running ? 'is-running' : 'is-done',
      item.failed ? 'is-failed' : '',
    ].filter(Boolean).join(' ')
    return `<li class="${state}">
      <details class="hall-live-item" data-master-open="live" data-id="${esc(key)}"${expanded}>
        <summary>
          <span class="hall-step-icon">${icon(masterTraceIcon(item))}</span>
          <b>${esc(masterTraceTitle(item))}</b>
          ${duration ? `<em>${esc(duration)}</em>` : ''}
          ${hint ? `<span>${esc(hint)}</span>` : ''}
        </summary>
        <pre class="hall-live-body">${esc(body)}${item.truncated ? '\n\n[обрезано ядром]' : ''}</pre>
      </details>
    </li>`
  })
  if (rows.length <= MASTER_TRACE_VISIBLE) return `<ol class="hall-live" data-master-live>${rows.join('')}</ol>`
  const earlier = rows.slice(0, -MASTER_TRACE_VISIBLE)
  const key = `${turn.id || 'turn'}:earlier`
  const expanded = open?.has?.(key) ? ' open' : ''
  return `<details class="hall-live-earlier" data-master-open="live" data-id="${esc(key)}"${expanded}><summary>ещё ${esc(countOf(earlier.length, 'шаг', 'шага', 'шагов'))}</summary><ol class="hall-live">${earlier.join('')}</ol></details><ol class="hall-live" data-master-live>${rows.slice(-MASTER_TRACE_VISIBLE).join('')}</ol>`
}

// Точечное обновление живой мысли: она растёт четыре раза в секунду и меняет
// ровно одну строку следа. Перерисовывать ради неё всю ленту значило бы дёргать
// разговор и закрывать раскрытое; здесь меняется только текст.
//
// Возвращает false, когда обновлять нечего — тогда лента перерисуется целиком.
export function masterTraceMindPatch (root, turn, follow) {
  const trace = turn?.trace || []
  const index = trace.length - 1
  const item = trace[index]
  if (!item || item.kind !== 'mind') return false
  const row = root?.querySelector?.(`.hall-live-item[data-id="${turn.id}:${index}"]`)
  if (!row) return false
  const body = row.querySelector('.hall-live-body')
  const hint = row.querySelector('summary span')
  if (body) body.textContent = item.text
  if (hint) hint.textContent = masterTraceTail(item.text)
  const thread = root.querySelector('#master-thread')
  if (thread && follow) thread.scrollTop = thread.scrollHeight
  return true
}
