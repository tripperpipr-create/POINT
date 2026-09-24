// Ход Мастера, пока он идёт, — тем видом, каким он останется в ленте.
//
// Раньше у хода было два облика. Пока он шёл — открытый список действий и
// сырой текст без разметки; когда кончался — строка сводки и оформленный
// ответ. На финише лента прыгала: восемь строк схлопывались в одну, звёздочки
// становились жирным, и ответ уезжал из-под глаз. Хуже того, между событием
// `done` и приходом истории ход пропадал вовсе — вместе с репликой человека:
// оба держались на признаке «идёт ход», а он гас раньше, чем приходила запись.
//
// Теперь у хода фазы:
//   wait     — ничего ещё не пришло: «Думаю…» со счётчиком секунд;
//   trace    — Мастер смотрит проект: последние три строки следа и «ещё N»;
//   text     — пошёл ответ: сводка следа, как у готового хода, и живой markdown
//              с курсором;
//   settling — ход кончился, история ещё не пришла: тот же вид без курсора;
//   failed   — поток оборвался: написанное остаётся, под ним строка сбоя.
// Фаза пустая, когда ход уже лежит в истории (реплика с его `turnId`) или
// разговор его «осадил» по приходу истории.
//
// Обновление точечное. События хода приходят десятками в секунду, и прежде
// каждое, кроме текста и мысли, пересобирало всю ленту: на разговоре в
// восемьдесят реплик это 56 мс на событие. Теперь меняется один блок
// `[data-master-stream]`, а текст ответа — только тело реплики, не чаще кадра.

import { masterTraceHtml } from './master-live-trace.js'
import { createMasterTrail, trailModelFromTrace } from './master-trail.js'
import { masterToolNameNow } from './master-tool-names.js'
import { threadNearBottom } from './master-feed.js'
import { icon } from './ui-icons.js'

const RUNNING = new Set(['preparing', 'waiting', 'streaming', 'tools'])
const BODY = 1
const BLOCK = 2
const FULL = 4
// Текст ответа перерисовывается не чаще тридцати раз в секунду: на длинном
// ответе разбор markdown стоит миллисекунды, а глаз разницы не заметит.
const BODY_GAP_MS = 33

export function masterStreamPhase (turn, { running = false, history = [] } = {}) {
  const saved = Boolean(turn?.id) && history.some(item => item?.role === 'assistant' && item.turnId === turn.id)
  const over = !turn || turn.settled || saved
  // Новый ход уже отправлен, а ядро о нём ещё не сообщило (или это прогон
  // агента, у которого хода Мастера нет вовсе): прежний, осевший ход не
  // должен показаться заново.
  if (running && over) return 'wait'
  if (over) return ''
  if (turn.streamError) return 'failed'
  const text = Boolean(String(turn.reply || '').trim())
  const trace = Array.isArray(turn.trace) && turn.trace.length > 0
  if (running) return text ? 'text' : trace ? 'trace' : 'wait'
  return text || trace ? 'settling' : ''
}

export function masterStreamRunning (turn) {
  return RUNNING.has(turn?.status)
}

// Что заставляет пересобрать блок целиком, а не только текст: число строк
// следа, число идущих и упавших. Рост мысли и текста ключ не меняет.
function rowsKey (turn) {
  const trace = Array.isArray(turn?.trace) ? turn.trace : []
  return `${trace.length}:${trace.filter(item => item.running).length}:${trace.filter(item => item.failed).length}`
}

export function createMasterStreamView ({ root, ui, esc, countOf, formatStreaming, replaceThread, afterPatch = () => {} }) {
  const { masterTrailHtml: trailHtml } = createMasterTrail({ esc, countOf, ui })
  const history = () => ui.masterData?.history || []
  const phaseNow = () => masterStreamPhase(ui.masterTurn, { running: ui.masterSending, history: history() })

  // Чем Мастер занят прямо сейчас. Ядро шлёт имя инструмента событием,
  // и строка ожидания обязана называть его по-русски: «Смотрю проект…» верно,
  // но беднее, чем «Читаю файл…», а сырое имя на экране — чужое слово.
  function waitingLabel () {
    if (ui.masterTurn?.status !== 'tools') return 'Думаю…'
    const tool = masterToolNameNow(ui.masterTurn?.progress)
    return tool ? `${tool[0].toUpperCase()}${tool.slice(1)}…` : 'Смотрю проект…'
  }

  // Строка сбоя под оборванным ответом. Написанное остаётся на месте: это то,
  // что модель успела сказать, и прочитать его можно и без продолжения.
  // «Повторить» спрашивает тот же вопрос заново — не «ответить иначе»: ход не
  // был плохим, он не дошёл. «Обновить» забирает переписку у ядра: поток мог
  // оборваться у панели, а ход — дойти до конца и сохраниться.
  function errorHtml (turn) {
    const ask = String(turn.ask || '').trim()
    return `<div class="hall-turn-error" role="alert">${icon('warning')}<span><b>Ответ прервался.</b> ${esc(turn.streamError || 'Поток ответа оборвался.')}</span><span class="hall-turn-error-acts">${ask ? `<button type="button" class="hall-btn is-sm" data-action="master-retry-turn" data-message="${esc(ask)}">Повторить</button>` : ''}<button type="button" class="hall-btn is-sm" data-action="retry-master">Обновить</button></span></div>`
  }

  function statusLabel (turn) {
    const labels = { preparing: 'Подготавливаю контекст…', waiting: 'Ожидаю модель…', streaming: 'Отвечаю…', tools: 'Изучаю проект…' }
    // Пока идёт текст, строка говорит одно — «Отвечаю…»: в `progress` при этом
    // лежит имя последнего инструмента, и оно выходило на экран сырым.
    if (turn?.status === 'streaming') return labels.streaming
    // Ядро шлёт в событии `tools` сырое имя инструмента. Незнакомое имя не
    // выдаём за знакомое — берём общую подпись.
    const progress = turn?.status === 'tools' ? masterToolNameNow(turn?.progress) : String(turn?.progress || '')
    return progress ? `${progress}…` : labels[turn?.status] || 'Ожидаю модель…'
  }

  function articleHtml (body) {
    return `<article class="hall-msg"><span class="who hall-sr"><span class="hall-speaker-name">Мастер</span><time>сейчас</time></span><div class="body">${body}</div></article>`
  }

  function html () {
    const phase = phaseNow()
    if (!phase) return ''
    const turn = ui.masterTurn
    const attrs = `class="hall-turn is-master-turn is-stream-turn" data-master-stream data-phase="${phase}" data-rows="${esc(rowsKey(turn))}" data-feed-key="a:${esc(turn?.id || 'wait')}"`
    if (phase === 'wait') {
      return `<div ${attrs}><article class="hall-msg hall-msg-waiting"><span class="who hall-sr"><span class="hall-speaker-name">Мастер</span><time>сейчас</time></span><div class="body is-muted"><span class="agent-work-thinking"><i></i><span>${esc(waitingLabel())}</span></span></div></article></div>`
    }
    if (phase === 'trace') {
      return `<div ${attrs}>${masterTraceHtml(turn, esc, ui.masterOpenLive)}<div class="hall-turn-foot hall-stream-foot"><small class="hall-stream-status">${esc(statusLabel(turn))}</small></div></div>`
    }
    // Сводка следа — та же строка, что встанет у готового хода, поэтому на
    // финише ей некуда прыгать. Подвал держит место подвала готового хода.
    const caret = phase === 'text'
    const body = formatStreaming(turn.reply, { caret })
    const foot = phase === 'failed' ? errorHtml(turn) : `<div class="hall-turn-foot hall-stream-foot"><small class="hall-stream-status">${caret ? esc(statusLabel(turn)) : ''}</small></div>`
    return `<div ${attrs}>${trailHtml(trailModelFromTrace(turn))}${String(turn.reply || '').trim() ? articleHtml(body) : ''}${foot}</div>`
  }

  // ——— Планировщик ———
  // Кадр в скрытой панели не наступает, поэтому рядом с ним стоит таймер: кто
  // придёт первым, тот и рисует, второй ничего не найдёт.
  let pending = 0
  let frame = 0
  let timer = 0
  let lastBody = 0
  const now = () => (typeof performance === 'object' && performance?.now ? performance.now() : Date.now())

  function fire () {
    if (!pending) return
    const need = pending
    pending = 0
    if (frame && typeof cancelAnimationFrame === 'function') cancelAnimationFrame(frame)
    if (timer) clearTimeout(timer)
    frame = 0
    timer = 0
    flush(need)
  }

  function schedule (need) {
    const idle = !pending
    pending |= need
    if (!idle) return
    const wait = pending === BODY ? Math.max(0, BODY_GAP_MS - (now() - lastBody)) : 0
    if (wait > 0) { timer = setTimeout(fire, wait); return }
    if (typeof requestAnimationFrame === 'function') frame = requestAnimationFrame(fire)
    if (pending) timer = setTimeout(fire, 120)
  }

  function flush (need) {
    const thread = root.querySelector('#master-thread')
    if (!thread) return
    const block = typeof thread.querySelector === 'function' ? thread.querySelector('[data-master-stream]') : null
    const phase = phaseNow()
    // Появление и исчезновение блока — дело всей ленты: он встаёт между
    // репликой человека и карточками, и место ему знает только она.
    if ((need & FULL) || !block || !phase) { replaceThread(); return }
    const follow = ui.masterAutoFollow || threadNearBottom(thread)
    const turn = ui.masterTurn
    const structural = block.dataset.phase !== phase || block.dataset.rows !== rowsKey(turn)
    if ((need & BLOCK) || structural) {
      const active = typeof document === 'object' ? document.activeElement : null
      const focusedId = block.contains(active) ? active.closest?.('[data-id]')?.dataset?.id : ''
      block.insertAdjacentHTML('afterend', html())
      const next = block.nextElementSibling
      block.remove()
      if (focusedId && next) next.querySelector(`[data-id="${CSS.escape(focusedId)}"] > summary`)?.focus({ preventScroll: true })
      if (next) afterPatch(next)
    } else {
      const body = block.querySelector('.hall-msg .body')
      if (!body) { schedule(BLOCK); return }
      body.innerHTML = formatStreaming(turn.reply, { caret: phase === 'text' })
      const status = block.querySelector('.hall-stream-status')
      if (status) status.textContent = phase === 'text' ? statusLabel(turn) : ''
      lastBody = now()
    }
    if (follow) thread.scrollTop = thread.scrollHeight
  }

  // Событие хода: что именно поменялось, решает тип. Текст меняет только тело
  // ответа, мысль — одну строку следа (её правит masterTraceMindPatch), всё
  // остальное — блок целиком.
  function accept (type) {
    schedule(type === 'reply' ? BODY : BLOCK)
  }

  return { html, accept, errorHtml, phase: phaseNow, refresh: () => schedule(FULL) }
}
