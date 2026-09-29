// Короткие заголовки квестов, команд и запусков для рабочих экранов.
//
// Названия из старых данных могут содержать всю разговорную команду. На
// рабочем экране человеку нужна тема задачи, а не повтор его сообщения.
export function compactQuestTitle(value) {
  let title = String(value || '').trim().split(/\r?\n/, 1)[0]
  title = title.replace(/^(?:создай|создать|подготовь|подготовить|поставь|поставить)\s+квест(?:\s+на)?\s*[:—-]?\s*/i, '')
  const sentenceEnd = title.search(/[.!?](?:\s|$)/)
  if (sentenceEnd >= 0) title = title.slice(0, sentenceEnd)
  title = title.trim().replace(/[.!?,;:\s]+$/g, '')
  if (!title) return 'Задача без названия'
  const limit = 72
  if ([...title].length > limit) {
    let short = [...title].slice(0, limit).join('')
    const wordEnd = short.lastIndexOf(' ')
    if (wordEnd >= Math.floor(limit / 2)) short = short.slice(0, wordEnd)
    title = short.trim().replace(/[.,;:!—-]+$/g, '') + '…'
  }
  return title.charAt(0).toUpperCase() + title.slice(1)
}
export function compactTeamName(team, quest) {
  const raw = String(team?.name || '').trim().replace(/^(?:party|отряд)\s*[·:—-]\s*/i, '')
  return compactQuestTitle(raw || quest?.title || 'Команда квеста')
}
export function compactQuestDescription(value, title) {
  let description = String(value || '').trim()
  if (!description) return ''
  description = description.replace(/^(?:создай|создать|подготовь|подготовить|поставь|поставить)\s+квест(?:\s+на)?\s*[:—-]?\s*/i, '')
  const firstEnd = description.search(/[.!?](?:\s|$)/)
  if (firstEnd >= 0 && compactQuestTitle(description.slice(0, firstEnd)) === compactQuestTitle(title)) {
    description = description.slice(firstEnd + 1).trim()
  }
  if (description === String(title || '').trim()) return ''
  const runes = [...description]
  return runes.length > 240 ? runes.slice(0, 239).join('').trim() + '…' : description
}
export function compactExecutionTask(item, quest) {
  const raw = String(item?.task || quest?.title || 'Запуск').replace(/^(?:primary|reviewer|planner|implementer)\s*:\s*/i, '')
  return compactQuestTitle(raw)
}
