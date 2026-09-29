// Порядок снимков квестов и нарядов (TODO Q01).
//
// Ответы ядра приходят не в том порядке, в каком были запрошены: состояние
// запрашивают наблюдатель наряда, поток хода, контроллеры кнопок, и медленный
// ответ, снятый до отмены, приходил после быстрого, снятого после неё. Вкладки
// принимали его целиком, и отменённый квест снова показывался «в работе».
//
// Правило одно для хоста расширения и вебвью (оно собирает этот файл в свой
// бандл): элемент со временем обновления старше того, что уже есть, не
// заменяет его. Время берётся из того же столбца `quests.updated_at`: у
// квеста — `updatedAt`, у наряда — `runtime.updatedAt`. Без времени снимок
// принимается как есть, иначе состояние можно было бы заморозить.

function stamp(value) {
  const time = Date.parse(value || '')
  return Number.isFinite(time) ? time : 0
}

const questStamp = quest => stamp(quest?.updatedAt)
const workOrderStamp = order => stamp(order?.runtime?.updatedAt)

function isStale(current, incoming, stampOf) {
  const held = stampOf(current)
  const next = stampOf(incoming)
  return held > 0 && next > 0 && next < held
}

// Список из свежего ответа задаёт состав (удалённое уходит), но элемент,
// который старше уже показанного, остаётся прежним.
function mergeNewerById(previous, next, stampOf) {
  if (!Array.isArray(next) || !Array.isArray(previous) || !previous.length) return next
  const held = new Map(previous.filter(item => item?.id).map(item => [item.id, item]))
  return next.map(item => {
    const current = held.get(item?.id)
    return current && isStale(current, item, stampOf) ? current : item
  })
}

// Один элемент — вперёд списка, если он не старше того, что уже есть.
function upsertNewer(list, item, stampOf) {
  const items = Array.isArray(list) ? list : []
  const current = items.find(entry => entry?.id === item?.id)
  if (current && isStale(current, item, stampOf)) return items
  return [item, ...items.filter(entry => entry?.id !== item?.id)]
}

// Снимок мира целиком (вебвью получает его в каждом `state`): состав полей —
// из нового, квесты и наряды — не старше уже показанных.
function newerBoot(previous, next) {
  if (!next || !previous) return next
  const merged = { ...next }
  if (Array.isArray(next.quests)) merged.quests = mergeNewerById(previous.quests, next.quests, questStamp)
  if (Array.isArray(next.workOrders)) merged.workOrders = mergeNewerById(previous.workOrders, next.workOrders, workOrderStamp)
  return merged
}

// Частичное обновление снимка в хосте (`patchBoot`): поля ложатся поверх.
function mergeBootSnapshot(previous, fields) {
  return newerBoot(previous, { ...(previous || {}), ...(fields || {}) })
}

module.exports = { questStamp, workOrderStamp, isStale, mergeNewerById, upsertNewer, newerBoot, mergeBootSnapshot }
