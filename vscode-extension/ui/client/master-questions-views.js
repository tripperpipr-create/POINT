// Уточнения Мастера. Спрашивают в композере, запись обмена остаётся в ленте.
//
// Обе половины стоят там, где нужны. Неотвеченный пакет живёт в карточке ввода:
// в ленте он уезжал вверх с каждым следующим ходом, и человек отвечал не на то,
// что видел, а на то, что сумел найти прокруткой. Отвеченный обмен живёт в
// ленте при том ходе, который его задал: разговор обязан читаться сверху вниз,
// а не начинаться ответами неизвестно на что.
//
// Текст вопроса не показан дважды: лента помечает, что спросили, композер
// спрашивает. Из пометки есть путь к форме — кнопкой, а не просьбой поискать.
//
// Прежде слот композера уже пробовали и убрали: он поднимал карточку ввода с
// восьмидесяти пикселей до двухсот семидесяти. С тех пор появился замер
// карточки с переносом запаса в ленту (applyMasterComposeReserve в
// ui/client/master-feed.js) — он написан ровно под этот случай, — а слот сверх
// того получил жёсткий потолок высоты с прокруткой, как у самого поля ввода.
// Эталон разметки — ячейка request_user_input в интерфейсе Codex: заголовок со
// счётчиком, вопросы перечнем с жёлобом, без рамок внутри рамки.

import { masterAnswerRows } from './master-compose.js'

// Формат реплики-ответа задан отправкой (masterQuestionAnswerLine): блоки
// «Вопрос: …\nМой ответ: a; b», разделённые пустой строкой.
const ANSWER_BLOCK = /^Вопрос:\s*([\s\S]*?)\nМой ответ:\s*([\s\S]*)$/

// Разбор реплики человека обратно в пары «вопрос — ответ».
//
// Разбирается либо всё, либо ничего: половина разбора хуже отсутствия разбора.
// Реплика, у которой часть блоков не разошлась, — обычное сообщение, в котором
// просто встретились те же слова; показав половину, мы потеряли бы остаток
// молча, а реплику при этом убрали бы из ленты как «уже показанную».
export function masterParseAnswers(content) {
  const text = String(content || '').replace(/\r\n?/g, '\n').trim()
  if (!/^Вопрос:\s/m.test(text) || !/\nМой ответ:\s/m.test(text)) return []
  const blocks = text.split(/\n{2,}/).map(block => block.trim()).filter(Boolean)
  const rows = []
  for (const block of blocks) {
    const match = block.match(ANSWER_BLOCK)
    if (!match) return []
    rows.push({
      text: match[1].trim(),
      answers: String(match[2]).split(/\s*;\s*/).map(value => value.trim()).filter(Boolean),
    })
  }
  return rows
}

// Строка ответа в формате отправки. Сборка живёт рядом с разбором намеренно:
// формат читает masterParseAnswers выше, и разъехавшись, эти двое молча
// превратили бы каждый ответ в обычную реплику.
function masterQuestionAnswerLine(text, draft) {
  const values = [...(draft?.selected || [])]
  const free = String(draft?.text || '').trim()
  if (free) values.push(free)
  if (!values.length) return ''
  return `Вопрос: ${text}\nМой ответ: ${values.join('; ')}`
}

// Сколько из пакета отвечено и что из этого уйдёт в ядро.
export function masterAnswerProgress(pack, owner, drafts) {
  const list = Array.isArray(pack) ? pack : []
  const lines = list.map((item, index) => masterQuestionAnswerLine(item.text, drafts?.[`${owner}:${index}`]))
  return {
    lines,
    total: list.length,
    answered: lines.filter(Boolean).length,
    missing: lines.findIndex(line => !line),
  }
}

// Что будет с неотвеченным. Отвечать на всё разом человек не обязан: ядро всё
// равно вернёт неотвеченное в «Нужно уточнить», и оттуда оно продолжит держать
// запуск. Молчащая кнопка, которая просто переставляла курсор, читалась как
// поломка — теперь причина написана рядом с ней.
export function masterAnswerNote({ answered, total }) {
  if (!total) return ''
  if (!answered) return 'Выберите вариант или напишите ответ — иначе отправлять нечего'
  if (answered < total) return `Отвечено ${answered} из ${total} · остальные останутся в задании нерешёнными и задержат запуск`
  return ''
}

export function createMasterQuestionsViews({ esc, countOf, ui }) {
  // Варианты ответа. Выдумывать их нельзя: предложенный выбор человек читает как
  // слова Мастера, а не как нашу догадку.
  //
  // Раньше вопрос без вариантов получал запасные «Да / Нет / Другое» — и на
  // «С какой стороны воспроизводится отказ?» предлагалось ответить «Да». В слоте
  // композера это терялось, в ленте стоит на виду. Разбор формулировки остаётся
  // только там, где выбор в ней действительно назван словом «или»: «Docker,
  // Kubernetes или вручную» — перечисление, а «Что считать готовым, и когда?» —
  // нет, и запятая в нём не вариант ответа.
  function masterQuestionOptions(question) {
    const given = Array.isArray(question?.options) ? question.options.map(v => String(v || '').trim()).filter(Boolean) : []
    if (given.length) return given.slice(0, 8)
    const text = String(question?.text || '')
    if (!/\s+или\s+/iu.test(text)) return []
    const after = text.includes(':') ? text.slice(text.indexOf(':') + 1) : text
    const cleaned = after.replace(/[?.!…]+$/u, '').trim()
    const parts = cleaned.split(/\s+или\s+|,\s*/iu).map(part => part.trim()).filter(part => part.length > 1 && part.length < 60)
    return parts.length >= 2 && parts.length <= 5 ? parts : []
  }

  function masterNormalizeQuestions(questions, clarifications) {
    if (clarifications?.length) questions = clarifications
    if (!Array.isArray(questions) || questions.length === 0) return []
    return questions.map(value => {
      const question = typeof value === 'string' ? { text: value, kind: 'text' } : { ...value }
      question.text = String(question.text || '').trim()
      question.options = masterQuestionOptions(question)
      if (question.options.length && question.kind !== 'multiple') question.kind = 'single'
      return question
    }).filter(question => question.text)
  }

  // Вопросы конкретной реплики Мастера. Хранилище кладёт их и в `questions`, и в
  // `clarifications` — разбор один и тот же.
  function masterQuestionsOf(item) {
    return masterNormalizeQuestions(item?.questions, item?.clarifications)
  }

  // Ответ к своему вопросу: сперва по тексту, потом по месту в перечне.
  //
  // По тексту — потому что порядок ответов задаёт отправка, а она собирает их из
  // того же пакета; по месту — потому что формулировку вопроса ядро могло
  // сохранить с другой правкой пробелов, и потерять из-за этого готовый ответ
  // было бы обиднее, чем показать его на строку выше.
  function masterPairAnswers(list, rows) {
    const rest = rows.slice()
    const pairs = list.map(question => {
      const found = rest.findIndex(row => row.text === question.text)
      if (found >= 0) return { text: question.text, answers: rest.splice(found, 1)[0].answers }
      return { text: question.text, answers: [] }
    })
    // Ответ, которому не нашлось вопроса, показывается сам: он всё равно часть
    // разговора, и молчать о нём нельзя.
    for (const row of rest) pairs.push(row)
    return pairs
  }

  // Решённый обмен. Служебный регистр: это запись разговора, а не развилка.
  function masterAnswersResolvedHtml(pairs) {
    if (!pairs.length) return ''
    const answered = pairs.filter(pair => pair.answers.length).length
    const rows = pairs.map(pair => `<div class="hall-question${pair.answers.length ? ' is-answered' : ' is-unanswered'}">
      <p class="hall-question-prompt">${esc(pair.text)}</p>
      <p class="hall-question-answer">${pair.answers.length
        ? `<span>ответ</span>${esc(pair.answers.join('; '))}`
        : '<span>нет ответа</span>'}</p>
    </div>`).join('')
    return `<div class="hall-questions is-resolved">
      <small class="hall-questions-progress">Уточнения · отвечено ${answered} из ${pairs.length}</small>
      ${rows}
    </div>`
  }

  // Живой обмен: по одному вопросу, с местом в пакете и возвратом назад.
  //
  // Пакетом сразу было хуже: два развёрнутых поля поднимали карточку ввода, и
  // человек отвечал на всё разом, не дочитав. Курсор здесь уже стоял раньше и
  // его сняли — но сняли за две беды, а не за сам курсор: он не говорил,
  // сколько ещё спросят, и не пускал обратно к отвеченному. Обе закрыты —
  // счётчик «вопрос N из M» в шапке и «Назад» рядом с «Далее».
  //
  // Ответы прошлых вопросов живут в черновиках пакета (masterQuestionDrafts),
  // а не в разметке: на экране один блок, и снимать со страницы больше нечего.
  // Поэтому «Продолжить» доступно с любого вопроса — отвеченное уйдёт, а
  // неотвеченное вернётся из ядра в «Нужно уточнить», как и прежде.
  function masterQuestionsAskHtml(owner, list) {
    const sending = Boolean(ui.masterSending)
    const drafts = ui.masterQuestionDrafts || {}
    const packAttr = esc(JSON.stringify(list.map(item => ({ text: item.text, options: item.options, kind: item.kind }))))
    const last = list.length - 1
    const saved = Number(ui.masterQuestionCursor?.[owner])
    const index = Math.max(0, Math.min(last, Number.isFinite(saved) ? saved : 0))
    const question = list[index]
    const key = `${owner}:${index}`
    const draft = drafts[key] || {}
    const multiple = question.kind === 'multiple'
    const options = question.options.map(option => {
      const on = draft.selected?.includes(option)
      return `<button type="button" class="hall-option${on ? ' is-on' : ''}" data-action="master-pick-option" data-option="${esc(option)}" aria-pressed="${on ? 'true' : 'false'}" ${sending ? 'disabled' : ''}>${esc(option)}</button>`
    }).join('')
    // Доступное имя называет место в пакете: на экране один вопрос, и без
    // номера озвучка не отличит его от вопроса, который был до него.
    const label = list.length > 1 ? `Свой ответ на вопрос ${index + 1} из ${list.length}` : 'Свой ответ'
    // Вопрос без вариантов отвечают словами, и слов бывает много: строка в
    // один ряд показывала из развёрнутого ответа последние сорок знаков.
    // Поле растёт по набранному, как поле реплики под ним. Там же, где
    // варианты названы, своё остаётся поправкой к выбору — и строки хватает.
    const free = question.options.length
      ? `<input type="text" class="hall-question-extra" aria-label="${esc(label)}" placeholder="Или свой вариант…" value="${esc(draft.text || '')}" ${sending ? 'disabled' : ''}>`
      : `<textarea class="hall-question-extra" aria-label="${esc(label)}" placeholder="Ответьте своими словами…" rows="${masterAnswerRows(draft.text)}" ${sending ? 'disabled' : ''}>${esc(draft.text || '')}</textarea>`
    const block = `<div class="hall-question" data-question-key="${esc(key)}" data-question="${esc(question.text)}" data-multiple="${multiple ? '1' : '0'}">
        <p class="hall-question-prompt">${esc(question.text)}</p>
        ${options ? `<div class="hall-question-options" role="${multiple ? 'group' : 'radiogroup'}">${options}</div>` : ''}
        ${free}
      </div>`
    const progress = masterAnswerProgress(list, owner, drafts)
    const note = masterAnswerNote(progress)
    // Счётчик пакета. У одного вопроса номер не пишем: «вопрос 1 из 1» —
    // это шум, который читается как обещание второго.
    const heading = list.length > 1
      ? `Уточнения · вопрос ${index + 1} из ${list.length}`
      : `Уточнения · ${countOf(list.length, 'вопрос', 'вопроса', 'вопросов')}`
    const back = index > 0
      ? `<button type="button" class="hall-btn hall-questions-step" data-action="master-question-prev" ${sending ? 'disabled' : ''}>Назад</button>`
      : ''
    const forward = index < last
      ? `<button type="button" class="hall-btn hall-questions-step" data-action="master-question-next" ${sending ? 'disabled' : ''}>Далее</button>`
      : ''
    // Кнопка не заперта пустотой: нажатие при пустом ответе ведёт к первому
    // незаполненному вопросу пакета и ставит в него курсор — это и есть ответ
    // на нажатие (handleMasterSessionAction в master-session-ui.js).
    //
    // Прежде она несла `aria-disabled`, и он врал дважды. Читалке — про то, что
    // кнопка недоступна, хотя она работает. Глазу — руками канона Чертога:
    // 99-hall-canon.css гасит заливку всякой кнопке с этим атрибутом, и
    // единственное решение пакета выглядело третьей подписью в ряду, рядом с
    // причиной. Причина осталась подписью, на неё указывает aria-describedby;
    // сама пустота видна по тому, что ни один вариант не выбран.
    //
    // Запертой кнопка бывает только на время хода — ровно как стрелка отправки
    // рядом (master-compose.js), и там это `disabled` по делу.
    //
    // Класса is-primary у неё нет намеренно: карточка ввода ищет свою стрелку
    // отправки по нему (syncMasterComposeState в main.js), и второй ярко
    // набранной кнопкой внутри той же карточки мы бы отобрали у настоящей
    // отправки запирание на время хода.
    return `<div class="hall-questions is-inline" data-owner="${esc(owner)}" data-total="${list.length}" data-cursor="${index}" data-pack="${packAttr}">
      <small class="hall-questions-progress">${esc(heading)}</small>
      ${block}
      <div class="hall-questions-foot">
        ${back}${forward}
        <button type="button" class="hall-btn hall-questions-send" data-action="master-answer-question" aria-describedby="master-answer-note" ${sending ? 'disabled' : ''}>${sending ? 'Отправляем…' : 'Продолжить'}</button>
        <small class="hall-questions-left" id="master-answer-note">${esc(note)}</small>
      </div>
    </div>`
  }

  // Пометка в ленте: ход спросил, ответа пока нет. Текст вопросов не повторён —
  // они стоят в карточке ввода, и два одинаковых перечня на одном экране
  // заставили бы сверять их между собой. Кнопка есть только у того пакета,
  // который сейчас в карточке: увести к форме, которой на экране нет, нельзя.
  function masterQuestionsMarkHtml(list, ownAsk) {
    if (!list.length) return ''
    return `<div class="hall-questions is-asked">
      <small class="hall-questions-progress">Уточнения · ${countOf(list.length, 'вопрос', 'вопроса', 'вопросов')}</small>
      ${ownAsk ? '<button type="button" class="hall-chip" data-action="master-focus-ask">Ответить в поле ниже</button>' : '<small>Остались без ответа</small>'}
    </div>`
  }

  // Пакет, на который ещё не ответили, — тот самый, что уходит в карточку ввода.
  //
  // Решённость считается ровно теми же правилами, которыми лента показывает
  // обмен решённым (masterThreadHtml в master-thread-views.js): разъехавшись,
  // форма спросила бы второй раз то, что в ленте уже стоит с ответами.
  function masterPendingQuestions() {
    if (ui.masterSending && masterParseAnswers(ui.masterSentText).length) return null
    const history = ui.masterData?.history || []
    const last = history[history.length - 1]
    if (last?.role === 'assistant') {
      const list = masterQuestionsOf(last)
      if (list.length) return { owner: String(last.id || 'live'), list }
    }
    if (last?.role === 'user' && masterParseAnswers(last.content).length) return null
    const response = ui.masterData?.response
    const list = masterNormalizeQuestions(response?.questions, response?.clarifications)
    return list.length ? { owner: 'live', list } : null
  }

  // То, что уходит в карточку ввода.
  function masterAskSlotHtml() {
    const pending = masterPendingQuestions()
    return pending ? masterQuestionsAskHtml(pending.owner, pending.list) : ''
  }

  // Блок уточнений одного хода. `answered` — разобранные ответы, если они уже
  // даны; без них ход несёт пометку, а спрашивает карточка ввода.
  function masterTurnQuestionsHtml(item, answered) {
    const list = masterQuestionsOf(item)
    if (!list.length) return ''
    if (answered?.length) return masterAnswersResolvedHtml(masterPairAnswers(list, answered))
    return masterQuestionsMarkHtml(list, masterPendingQuestions()?.owner === String(item?.id || 'live'))
  }

  // Вопросы текущего хода, которых нет ни у одной сохранённой реплики: ядро
  // прислало их в ответе, а история ещё не доехала.
  function masterLiveQuestionsHtml(response) {
    const list = masterNormalizeQuestions(response?.questions, response?.clarifications)
    if (!list.length) return ''
    return masterQuestionsMarkHtml(list, masterPendingQuestions()?.owner === 'live')
  }

  return {
    masterAnswersResolvedHtml,
    masterAskSlotHtml,
    masterLiveQuestionsHtml,
    masterPairAnswers,
    masterPendingQuestions,
    masterQuestionsMarkHtml,
    masterQuestionsOf,
    masterTurnQuestionsHtml,
  }
}
