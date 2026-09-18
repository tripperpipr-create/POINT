// Единицы измерения, показанные человеку.
//
// Формат объёма был написан дважды — в hub-runtime-ui и в quest-runtime-views,
// байт в байт. Один и тот же размер контекста человек видит на двух экранах
// подряд: в инспекторе и в предпросмотре. Разойдись округление — и два числа
// об одном и том же станут спорить друг с другом.

export function formatBytes(value) {
  const bytes = Number(value || 0)
  if (bytes < 1024) return `${bytes} Б`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} КиБ`
  return `${(bytes / 1024 / 1024).toFixed(1)} МиБ`
}

// Дата, показанная человеку.
//
// `new Date(x).toLocaleString('ru-RU')` звался в семи местах без защиты, и на
// неполных данных давал две беды. Пропущенная дата превращалась в «Invalid
// Date» — видно, что сломано, но непонятно что. Пустая (`null`) — в
// «01.01.1970, 07:00:00», и это хуже: правдоподобная ложь, которую человек
// принимает за настоящее время. Хаб обязан держаться на неполных данных, а
// прочерк честнее и того и другого.
export function formatDateTime(value) {
  if (value === null || value === undefined || value === '') return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('ru-RU')
}
