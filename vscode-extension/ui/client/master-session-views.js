import { masterMemoryEntryHtml } from './master-memory-ui.js'
// Формы ответа — те же пять, что различает ядро: internal/app/master_sessions.go
// принимает auto|brief|detailed|plan|questions, и каждая разворачивается в свою
// инструкцию модели (internal/orchestrator/chat_model.go). Меню знало три, и
// разговор, сохранённый в режиме «Сначала вопросы», показывался как «Авто» —
// интерфейс врал о сохранённом состоянии, а выйти из режима было нечем.
// Сверку списка с перечислением ядра держит договорённость в ui/contracts.mjs.
const modes=[['auto','Авто'],['brief','Кратко'],['detailed','Подробно'],['plan','План'],['questions','Сначала вопросы']]
export function masterSessionHtml(sessions,esc){
 if(!sessions)return ''
 const current=sessions.items.find(v=>v.id===sessions.active)
 // Удалить разговор можно было только текущий: кнопки панели «•••» несут id
 // активной сессии. Чтобы убрать старый чат, приходилось сперва в него зайти —
 // то есть загрузить то, что собираешься выбросить. Знак снятия теперь у каждой
 // строки; ядро всё равно переспрашивает модальным окном, так что промах
 // обратим. Кнопка в кнопку не вкладывается, поэтому строка стала рядом.
 const row=v=>`<div class="hall-conversation-row"><button type="button" data-action="master-session-select" data-id="${esc(v.id)}" aria-current="${v.id===sessions.active}"><span>${esc(v.title)}</span></button><button type="button" class="hall-conversation-drop" data-action="master-session-delete" data-id="${esc(v.id)}" aria-label="Удалить разговор «${esc(v.title)}»" title="Удалить разговор">×</button></div>`
 const items=[...sessions.items].sort((a,b)=>Number(!!b.pinned)-Number(!!a.pinned)||String(b.updatedAt || '').localeCompare(a.updatedAt || ''))
 return `<div class="hall-sessions">
  ${/* Список разговоров уехал в master-chat-directory.js: он стал
       кросс-проектным и живёт левой колонкой экрана, а не внутри разговора.
       Полоса разошлась по шапке чата — там же теперь и вкладка задания.
       Панели «•••» и «Память» остались здесь: их ищут по root, и место
       в дереве им безразлично. */''}
  <section class="hall-session-panel" data-session-panel="history" hidden aria-label="Действия с разговором">
   <div class="hall-session-menu"><button type="button" class="hall-chip" data-action="master-session-new">Новый чат</button><button type="button" class="hall-chip" data-action="master-session-toggle" data-panel="memory">Память${sessions.memoryEntries?.some(v=>v.status==='proposed')?' •':''}</button>${masterAnswerStyleHtml(sessions,esc)}</div>
   <label>Название<input data-master-session-title maxlength="100" value="${esc(current?.title || '')}" /></label><button type="button" class="hall-chip" data-action="master-session-rename" data-id="${esc(sessions.active)}">Переименовать</button><button type="button" class="hall-chip" data-action="master-session-pin" data-id="${esc(sessions.active)}">${current?.pinned?'Открепить':'Закрепить'}</button><button type="button" class="hall-chip" data-action="master-session-archive" data-id="${esc(sessions.active)}">${current?.archived?'Вернуть из архива':'В архив'}</button><button type="button" class="hall-chip" data-action="master-session-export" data-id="${esc(sessions.active)}">Экспорт Markdown</button><button type="button" class="hall-chip" data-action="master-session-delete" data-id="${esc(sessions.active)}">Удалить разговор</button>
   <details><summary>Автозапуск в проекте: ${sessions.autoRunReadOnly?'включён для чтения':'выключен'}</summary><p>Только в режиме «Выполнить»: один агент, чтение проекта без записи, команд и сети. Лимиты: 20 000 токенов, 120 секунд, одна попытка и одна ревизия плана. Инструменты: read_file, list_files, search_text, project_map, search_code. Задания вне этих пределов требуют подтверждения.</p><button type="button" class="hall-chip" data-action="master-session-auto-read-only" data-value="${!sessions.autoRunReadOnly}">${sessions.autoRunReadOnly?'Выключить автозапуск':'Разрешить автозапуск в этих пределах'}</button></details>
   ${current?.summary?`<details><summary>Резюме беседы</summary><p>${esc(current.summary)}</p></details>`:''}
  </section>
  <section class="hall-session-panel" data-session-panel="memory" hidden aria-label="Память проекта"><p>Подтверждённые записи используются в других чатах проекта.</p>
   ${(sessions.memoryEntries || []).map(v=>masterMemoryEntryHtml(v,sessions.memoryEntries,esc)).join('')}
   <label>Добавить запись<textarea data-master-memory rows="2" maxlength="4000" placeholder="Решение или предпочтение проекта…"></textarea></label><button type="button" class="hall-chip" data-action="master-session-memory-save">Сохранить память</button>
  </section>
 </div>`
}
// Подробность ответа переехала в меню «•••» полосы разговора: её меняют раз в
// месяц, а место в ряду управления она занимала всегда. В ряду остаётся то, что
// выбирают перед каждой репликой, — режим работы и модель.
function masterAnswerStyleHtml(sessions,esc){
 if(!sessions)return ''
 // То, что названо в свёрнутом меню, и отмечено в раскрытом. Отметку считаем от
 // той же ступени, что и надпись, иначе меню показывает «Авто» и не отмечает
 // ничего — у сессии в этом поле бывает и чужое значение.
 const modeId=modes.find(v=>v[0]===sessions.mode)?.[0] || 'auto'
 const modeLabel=modes.find(v=>v[0]===modeId)[1]
 return `<details class="hall-answer-style"><summary>Ответ: ${esc(modeLabel)}</summary><div role="group" aria-label="Подробность ответа">${modes.map(([id,label])=>`<button type="button" class="hall-chip" data-action="master-session-mode" data-value="${id}" aria-pressed="${modeId===id}">${label}</button>`).join('')}</div></details>`
}

// Ряд управления композера: чем занят Мастер и какой моделью отвечает.
//
// Модель вернулась сюда из шапки: у эталона её выбирают там же, где пишут, и
// два места для одного имени — это два места, куда за ним идти. Чип режима
// залит, модель набрана плоским текстом: громкость в ряду одна.
export function masterComposerHtml(sessions,model,esc,sending,modelChip){
 if(!sessions)return ''
 // Режим работы: отметка считается от ступени, а не от сырого поля.
 const works=[['discuss','Обсудить'],['plan','Спланировать'],['execute','Выполнить'],['agent','Агент']]
 const workId=works.find(v=>v[0]===sessions.workMode)?.[0] || 'discuss'
 const workLabel=works.find(v=>v[0]===workId)[1]
 // Модель этого разговора, если её переопределили: ядро берёт её вместо общей
 // (internal/app/master_turns.go). Общая названа чипом рядом, и повторять её
 // здесь незачем — говорим только о расхождении.
 const sessionModel=String(sessions.model || '').trim()
 const commonModel=String(model || '').trim()
 const modelChoice=sessionModel && sessionModel !== commonModel
  ? `<button type="button" class="hall-chip hall-model-choice" data-action="master-session-model" data-id="${esc(sessions.active || '')}" title="Этот разговор закреплён за другой моделью" ${sending?'disabled':''}>только здесь: ${esc(sessionModel)}</button>`
  : ''
 return `<div class="hall-composer-controls">
  <details class="hall-work-menu"><summary>${esc(workLabel)}</summary><div class="hall-work-modes" role="group" aria-label="Режим работы">${works.map(([id,label])=>`<button type="button" class="hall-chip" data-action="master-session-workMode" data-value="${id}" aria-pressed="${workId===id}">${label}</button>`).join('')}</div></details>
  ${modelChip || ''}
  ${modelChoice}
 </div>`
}
