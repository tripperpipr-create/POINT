import { masterTraceAccept } from './master-live-trace.js'

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
    // Повторный приход того же хода (переподключение потока, ответ на
    // отправку) не стирает собранный след и отметку «осел»: без них ход,
    // уже лежащий в истории, показался бы в ленте второй раз.
    acceptTurn(turn) {const prev=this.turns[turn.conversationId];this.turns[turn.conversationId]=prev&&prev.id===turn.id?{...turn,trace:turn.trace||prev.trace,settled:prev.settled||turn.settled,streamError:turn.streamError||prev.streamError,ask:prev.ask}:turn},
    // История пришла — ход больше не живёт в ленте своим блоком.
    settle(id=this.active) {const turn=this.turns[id];if(turn)turn.settled=true},
    // Поток оборвался. Ход перестаёт считаться идущим — иначе следующий ответ
    // ядра снова запер бы отправку, — а причина и вопрос остаются при нём:
    // строка сбоя показывает первое и повторяет второе.
    fail(id,message,ask) {const turn=this.turns[id];if(!turn)return;turn.status='failed';turn.streamError=String(message||'');turn.ask=String(ask||'');for(const item of turn.trace||[])item.running=false},
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
      // Не идущий ход в снимке помечен осевшим: после перезапуска панели его
      // текст придёт историей, и блок потока показал бы его дважды.
      const lean=values=>Object.fromEntries(Object.entries(durable(values)).map(([id,turn])=>[id,{...turn,trace:undefined,settled:turn.settled||!['preparing','waiting','streaming','tools'].includes(turn.status)}]))
      return {historyHidden:this.historyHidden,active:this.active.startsWith('temporary')?'':this.active,drafts:durable(this.drafts),scroll:durable(this.scroll),attachments:durable(this.attachments),turns:lean(this.turns),questionDrafts:durable(this.questionDrafts),questionCursor:durable(this.questionCursor),briefPanel:durable(this.briefPanel),cardOpen:durable(this.cardOpen)}
    },
  }
}
