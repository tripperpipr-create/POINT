import { masterToolNameNow } from './master-tool-names.js'
import { masterTraceAccept, masterTraceHtml } from './master-live-trace.js'

export function createMasterChatState(saved = {}) {
  return {
    historyHidden:!!saved.historyHidden,historyOpen:false,active: saved.active || '', drafts: saved.drafts || {}, scroll: saved.scroll || {},
    attachments: saved.attachments || {}, turns: saved.turns || {}, questionDrafts:saved.questionDrafts || {}, pages: {}, query: '',
    // Место в пакете уточнений: вопросы показываются по одному, и закладка
    // обязана пережить перерисовку слота на каждом нажатии клавиши.
    questionCursor: saved.questionCursor || {},
    // Панель задания открыта по разговору, а не на весь раздел: задание своё
    // у каждого чата, и открытая панель соседнего чата — чужая раскрытая дверь.
    briefPanel: saved.briefPanel || {},
    // Раскрытые подробности карточек ленты — по разговору и по ключу карточки.
    // Значение трёхзначное: отсутствие ключа значит «не трогали», и тогда
    // работает умолчание вида (master-card-open.js).
    cardOpen: saved.cardOpen || {},
    // Раскрытые строки живого следа. Набор живёт при разговоре, а не в
    // разметке: лента перерисовывается на каждое событие хода, и раскрытие,
    // оставленное в DOM, схлопывалось бы прямо под читающим.
    openLive: new Set(),
    remember(id, draft, scroll) {if (!id) return;this.drafts[id]=draft;if (Number.isFinite(scroll)) this.scroll[id]=scroll},
    acceptTurn(turn) {this.turns[turn.conversationId]=turn},
    acceptEvent(event) {
      const turn=this.turns[event.conversationId] ||= {id:event.turnId,conversationId:event.conversationId,reply:''}
      if (turn.id!==event.turnId) return
      // След хода собирается всегда, в том числе для событий, меняющих статус:
      // строка ожидания говорит, что идёт, а след — что уже случилось.
      masterTraceAccept(turn,event)
      if (event.type==='reply') {turn.reply=event.text;turn.status='streaming'}
      else if (event.type==='done') turn.status=event.text
      // Размышление не меняет состояния хода: оно идёт и в ожидании модели, и
      // между обращениями к инструментам. Подменив статус, оно стирало бы со
      // строки ожидания имя инструмента, который как раз работает.
      else if (event.type==='reasoning') turn.thinking=true
      else if (event.type==='tool_result') turn.status='tools'
      // Повтор идёт столько же, сколько первая попытка: строка ожидания
      // обязана назвать его, иначе второй круг неотличим от зависшей модели.
      else if (event.type==='retry') {turn.status='waiting';turn.progress=event.text}
      else {turn.status=event.type==='tools'?'tools':'waiting';turn.progress=event.text}
    },
    running(id=this.active) {return ['preparing','waiting','streaming','tools'].includes(this.turns[id]?.status)},
    snapshot() {
      const durable=values=>Object.fromEntries(Object.entries(values).filter(([id])=>!id.startsWith('temporary_')&&!id.startsWith('temporary-')))
      // След хода в снимок не идёт: он про происходящее сейчас, а к
      // следующему открытию панели ход уже закончится своей репликой — с
      // теми же шагами и рассуждением, сохранёнными ядром.
      const lean=values=>Object.fromEntries(Object.entries(durable(values)).map(([id,turn])=>[id,{...turn,trace:undefined}]))
      return {historyHidden:this.historyHidden,active:this.active.startsWith('temporary')?'':this.active,drafts:durable(this.drafts),scroll:durable(this.scroll),attachments:durable(this.attachments),turns:lean(this.turns),questionDrafts:durable(this.questionDrafts),questionCursor:durable(this.questionCursor),briefPanel:durable(this.briefPanel),cardOpen:durable(this.cardOpen)}
    },
  }
}
// Строка ожидания. Ядро шлёт в событии `tools` сырое имя инструмента (call.Name),
// и оно уезжало на экран как есть: посреди русского разговора висело «read_file».
// Незнакомое имя не выдаём за знакомое — берём общую подпись состояния.
export function masterStreamHtml(turn, esc, open) {
  const labels={preparing:'Подготавливаю контекст…',waiting:'Ожидаю модель…',streaming:'Отвечаю…',tools:'Изучаю проект…'}
  const progress=turn?.status==='tools' ? masterToolNameNow(turn?.progress) : String(turn?.progress || '')
  const status=progress ? `${progress}…` : labels[turn?.status] || 'Ожидаю модель…'
  const trace=masterTraceHtml(turn,esc,open)
  return `<div class="hall-stream" data-master-stream>${trace}<small role="status">${esc(status)}</small><div class="hall-stream-text">${esc(turn?.reply || '')}</div></div>`
}
