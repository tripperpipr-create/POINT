// Появление нового в ленте Мастера — одно тихое движение, и только у нового.
//
// Лента пересобирается целиком почти на каждое событие хода (innerHTML), и
// анимация «при появлении узла» проигрывалась бы у всех реплик на каждом
// кадре. Поэтому появление решает не CSS, а этот модуль: он помнит, какие
// узлы человек уже видел, и помечает `is-entering` только те, что пришли
// впервые и встали в конец ленты.
//
// Молча, без движения, проходят:
//   — первая отрисовка и смена разговора: всё уже «было»;
//   — «Показать раньше»: ранние реплики встают выше виденного, а не в хвост;
//   — пачка больше трёх узлов разом: это загрузка, а не разговор;
//   — готовый ответ, сменивший блок идущего хода: он уже был на экране, и
//     появиться второй раз ему нечем.
// Если пересборка случилась посреди появления, оно продолжается с того же
// места: задержка ставится отрицательной, через CSSOM (CSP не пускает style).

const ENTER_MS = 200
const BULK = 3

// Ключ узла ленты. Ответ Мастера и блок хода несут `data-feed-key` с номером
// хода. Реплике человека ключ даёт её текст и порядковый номер среди реплик с
// тем же текстом: ожидающая реплика и сохранённая ядром — один и тот же узел,
// хотя идентификаторы у них разные.
function hash (text) {
  let value = 2166136261
  for (let i = 0; i < text.length; i++) value = Math.imul(value ^ text.charCodeAt(i), 16777619)
  return (value >>> 0).toString(36)
}

export function masterFeedKeys (nodes) {
  const seenTexts = new Map()
  return nodes.map(node => {
    const own = node.dataset?.feedKey
    if (own) return own
    if (node.classList?.contains('is-user-turn')) {
      const text = String(node.querySelector?.('.hall-msg .body')?.textContent || '').trim()
      const digest = hash(text)
      const count = (seenTexts.get(digest) || 0) + 1
      seenTexts.set(digest, count)
      return `u:${digest}:${count}`
    }
    const card = node.dataset?.workOrderId || node.dataset?.agentCard || node.dataset?.hiringCard
    return card ? `c:${card}` : ''
  })
}

// Какие ключи входят с движением. Чистая функция: её проверяет смоук.
export function masterFeedEnterPlan (seen, keys, { fresh = false, streamGone = false, masters = new Set() } = {}) {
  const known = keys.filter(Boolean)
  if (fresh || !seen.size) return []
  let lastSeen = -1
  known.forEach((key, index) => { if (seen.has(key)) lastSeen = index })
  let tail = known.slice(lastSeen + 1).filter(key => !seen.has(key))
  // Блок хода ушёл, а на его месте встал ответ без номера хода (старая запись
  // ядра): последний новый ответ Мастера — его преемник, и входить ему нечем.
  if (streamGone) {
    const successor = [...tail].reverse().find(key => masters.has(key))
    if (successor) tail = tail.filter(key => key !== successor)
  }
  return tail.length > BULK ? [] : tail
}

export function createMasterFeedMotion ({ now = () => Date.now() } = {}) {
  let seen = new Set()
  let conversation = null
  let streamKey = ''
  const entered = new Map()

  // Пометить узлы ленты. `scope` — лента целиком или один блок хода после
  // точечной правки; во втором случае ключи остальной ленты не пересчитываются.
  function mark (thread, { conversationId = '', scope = thread } = {}) {
    if (typeof thread?.querySelectorAll !== 'function') return
    const nodes = [...thread.children].filter(node => node.classList?.contains('hall-turn') || node.dataset?.workOrderId || node.dataset?.agentCard || node.dataset?.hiringCard)
    const keys = masterFeedKeys(nodes)
    const fresh = conversation !== conversationId
    conversation = conversationId
    const stream = thread.querySelector('[data-master-stream]')?.dataset?.feedKey || ''
    const masters = new Set(keys.filter((key, index) => key && nodes[index].classList?.contains('is-master-turn')))
    const enter = new Set(masterFeedEnterPlan(seen, keys, { fresh, streamGone: Boolean(streamKey) && !stream, masters }))
    streamKey = stream
    const time = now()
    nodes.forEach((node, index) => {
      const key = keys[index]
      if (!key || (scope !== thread && !scope.contains?.(node) && scope !== node)) return
      const started = entered.get(key)
      if (enter.has(key)) entered.set(key, time)
      const since = enter.has(key) ? 0 : started != null ? time - started : Infinity
      if (since >= ENTER_MS) return
      node.classList.add('is-entering')
      if (since > 0) node.style?.setProperty?.('animation-delay', `-${since}ms`)
    })
    seen = new Set(keys.filter(Boolean))
    for (const [key, started] of entered) if (time - started >= ENTER_MS) entered.delete(key)
  }

  return { mark }
}
