// Правила ленты разговора: где кончается один день и что считать совпадением.
//
// Вынесено из разметки, потому что проверяется отдельно от неё: подпись дня
// зависит от «сегодня», а совпадение — от того, что человек набрал. И то, и
// другое ошибается молча, если считать его в шаблоне.

const MONTHS = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня',
  'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря']

// Метка дня для группировки. Считается по местному времени, а не по UTC:
// разговор в час ночи принадлежит той ночи, а не вчерашнему дню в Гринвиче.
export function masterDayKey (value) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return `${date.getFullYear()}-${date.getMonth() + 1}-${date.getDate()}`
}

// Подпись разделителя. «Сегодня» и «вчера» — то, как человек и думает о своём
// разговоре; дальше уже нужна дата, потому что «три дня назад» ничего не
// находит. Год добавляется только когда он не текущий: в разговоре этого года
// он был бы шумом в каждой строке. Дата набрана обычным регистром, как и
// «Сегодня» рядом: прописные были голосом прежнего Чертога.
export function masterDayLabel (value, now = new Date()) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const key = masterDayKey(date)
  if (key === masterDayKey(now)) return 'Сегодня'
  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  if (key === masterDayKey(yesterday)) return 'Вчера'
  const day = `${date.getDate()} ${MONTHS[date.getMonth()]}`
  return date.getFullYear() === now.getFullYear() ? day : `${day} ${date.getFullYear()}`
}

// Время реплики — часы и минуты. Дата живёт в разделителе: повторять её у
// каждой реплики значит забить колонку говорящего шумом.
export function masterTimeLabel (value) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
}

// Сколько ходов нашлось. Считаются реплики, а не вхождения: человек ищет место
// в разговоре, а не количество упоминаний слова.
export function masterFindSummary (total, current) {
  if (!total) return 'ничего не найдено'
  return `${current + 1} из ${total}`
}

// Работа с живой лентой: пометка найденного и якорь «к свежему».
//
// Обе правят DOM и потому живут не в разметке, а здесь: перерисовывать ленту на
// каждую набранную букву значило бы терять и прокрутку, и каретку в самом поле
// поиска. Состояние остаётся в main.js и приходит сюда общим ui-объектом — как
// у остальных вынесенных видов.
export function createMasterFeedRuntime ({ root, ui }) {
  // Сколько ленты отдано плавающему композеру.
  //
  // Числом это не задаётся. Карточка растёт от уточнений модели, вложений,
  // строки задания и набранного: в покое она 81 пиксель, с блоком уточнений —
  // 273. Пока запас был постоянным, хвост разговора уходил под непрозрачную
  // часть вуали и доскроллить до него было нельзя — лента просто кончалась
  // раньше карточки. Меряем узел и отдаём число в переменную, от которой
  // считают и отступ ленты, и высота вуали, и якорь «к новым».
  //
  // Пишем через CSSOM, а не атрибутом style: CSP вебвью запрещает инлайновые
  // стили в разметке, но программную правку свойства не трогает.
  let watched = null
  const sizes = typeof ResizeObserver === 'function' ? new ResizeObserver(() => applyMasterComposeReserve()) : null

  function applyMasterComposeReserve () {
    const dialogue = root.querySelector?.('.hall-dialogue')
    const form = root.querySelector?.('.hall-compose')
    if (!dialogue || !form || typeof form.getBoundingClientRect !== 'function') return
    if (sizes && watched !== form) {
      if (watched) sizes.unobserve(watched)
      sizes.observe(form)
      watched = form
    }
    const below = parseFloat(getComputedStyle(form).marginBottom) || 0
    const height = Math.round(form.getBoundingClientRect().height + below)
    if (height > 0) dialogue.style?.setProperty('--hall-compose-reserve', height + 'px')
  }

  // Якорь прячется классом, а не атрибутом hidden: кнопка липкая, и hidden
  // убрал бы её из потока вместе с местом, на котором она стоит.
  function updateMasterScrollCue () {
    const cue = root.querySelector('#master-scroll-cue')
    if (cue) cue.className = ui.masterAutoFollow ? 'hall-thread-cue is-hidden' : 'hall-thread-cue'
  }

  function applyMasterFind (scroll = false) {
    const thread = root.querySelector('#master-thread')
    // Лента бывает не настоящим узлом: смоуки подставляют объект с тремя
    // известными им свойствами. Поиск по такой ленте не нужен, а падение в ней
    // уронило бы проверки, которые про поиск ничего не знают.
    if (typeof thread?.querySelectorAll !== 'function') return
    const needle = String(ui.masterFindQuery || '').trim().toLowerCase()
    const hits = []
    for (const body of thread.querySelectorAll('.hall-msg .body')) {
      const turn = body.closest('.hall-turn') || body.closest('.hall-msg')
      if (!turn) continue
      const hit = Boolean(needle) && String(body.textContent || '').toLowerCase().includes(needle)
      turn.classList.toggle('is-hit', hit)
      turn.classList.remove('is-current-hit')
      if (hit) hits.push(turn)
    }
    if (!needle) {
      ui.masterFindSummary = ''
    } else {
      // Перебор идёт по кругу в обе стороны: дойдя до последнего совпадения,
      // человек ждёт возврата к первому, а не упора в край.
      ui.masterFindIndex = hits.length ? ((ui.masterFindIndex % hits.length) + hits.length) % hits.length : 0
      ui.masterFindSummary = masterFindSummary(hits.length, ui.masterFindIndex)
      const current = hits[ui.masterFindIndex]
      if (current) {
        current.classList.add('is-current-hit')
        if (scroll) current.scrollIntoView({ block: 'center' })
      }
    }
    const count = root.querySelector('.hall-find-count')
    if (count) count.textContent = ui.masterFindSummary
  }

  return { applyMasterFind, updateMasterScrollCue, applyMasterComposeReserve }
}
