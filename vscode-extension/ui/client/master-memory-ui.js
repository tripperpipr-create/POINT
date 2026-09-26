export function masterMemoryEntryHtml(entry, entries, esc) {
 const proposed=entry.status==='proposed'
 const targets=entries.filter(value=>value.status==='accepted' && value.id!==entry.id)
 const date=entry.updatedAt?new Date(entry.updatedAt).toLocaleDateString('ru-RU'):''
 return `<article class="hall-memory-entry"><small>${proposed?'Предлагаю запомнить':'В памяти'}${date?' · '+esc(date):''}</small><textarea data-memory-id="${esc(entry.id)}" rows="2" maxlength="4000">${esc(entry.content)}</textarea><div><button type="button" class="hall-chip" data-action="master-session-memory-save" data-id="${esc(entry.id)}">${proposed?'Подтвердить':'Сохранить правку'}</button><button type="button" class="hall-chip" data-action="master-session-memory-delete" data-id="${esc(entry.id)}">${proposed?'Не запоминать':'Удалить'}</button></div>${entry.sourceId?`<details><summary>Источник</summary><code>${esc(entry.sourceId)}</code></details>`:''}${proposed&&targets.length?`<details><summary>Заменить существующее решение</summary><label>Запись для замены<select aria-label="Запись памяти для замены">${targets.map(value=>`<option value="${esc(value.id)}">${esc(value.content)}</option>`).join('')}</select></label><button type="button" class="hall-chip" data-action="master-session-memory-replace" data-id="${esc(entry.id)}">Подтвердить замену выбранной записи</button></details>`:''}</article>`
}

// Раскрытие помнится по реплике: лента пересобирается на каждое событие хода,
// и блок, оставленный открытым только в DOM, схлопывался бы под читающим.
// Ключ с приставкой `memory:` — набор общий с рассуждениями, и метки не должны
// совпасть с идентификатором хода.
export function masterUsedMemoryHtml(ids, entries, esc, messageId = '', open = false) {
 if(!ids?.length)return ''
 const remember=messageId?` data-master-open="memory" data-id="memory:${esc(messageId)}"`:''
 return `<details class="hall-message-context"${remember}${open?' open':''}><summary>Память в контексте ответа · ${ids.length}</summary><ul>${ids.map(id=>`<li>${esc(entries?.find(entry=>entry.id===id)?.content || 'Запись удалена: '+id)}</li>`).join('')}</ul></details>`
}

// Предложение запомнить стоит под ответом, который его сделал. Спрятанным в
// меню «•••» его никто не находил: 29 записей остались «proposed», а в промпт
// попадают только подтверждённые — поправка человека не доживала до
// следующего задания. Кнопки те же, что в панели памяти, и маршрут тот же.
export function masterProposedMemoryHtml(turnId, entries, esc) {
 const proposed=turnId?(entries || []).filter(entry=>entry.status==='proposed' && entry.sourceId===turnId):[]
 if(!proposed.length)return ''
 return `<div class="hall-memory-inline" role="group" aria-label="Предложение запомнить">${proposed.map(entry=>masterMemoryEntryHtml(entry,entries,esc)).join('')}</div>`
}
