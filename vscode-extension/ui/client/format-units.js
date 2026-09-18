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
