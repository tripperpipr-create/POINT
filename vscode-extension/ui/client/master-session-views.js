import { masterMemoryEntryHtml } from './master-memory-ui.js'
import { icon } from './ui-icons.js'
// Формы ответа — те же пять, что различает ядро: internal/app/master_sessions.go
// принимает auto|brief|detailed|plan|questions, и каждая разворачивается в свою
// инструкцию модели (internal/orchestrator/chat_model.go). Меню знало три, и
// разговор, сохранённый в режиме «Сначала вопросы», показывался как «Авто» —
// интерфейс врал о сохранённом состоянии, а выйти из режима было нечем.
// Сверку списка с перечислением ядра держит договорённость в ui/contracts.mjs.
const modes=[['auto','Авто'],['brief','Кратко'],['detailed','Подробно'],['plan','План'],['questions','Сначала вопросы','Вопросы']]
// developmentHtml — готовая панель «Навыки и развитие» (master-development.js):
// её состояние живёт в ui, а не в сессиях, и собирает её вызывающий.
export function masterSessionHtml(sessions,esc,openPanel='',developmentHtml=''){
 if(!sessions)return ''
 const current=sessions.items.find(v=>v.id===sessions.active)
 const proposed=(sessions.memoryEntries || []).filter(v=>v.status==='proposed').length
 return `<div class="hall-sessions">
  ${/* Список разговоров уехал в master-chat-directory.js: он стал
       кросс-проектным и живёт левой колонкой экрана, а не внутри разговора.
       Полоса разошлась по шапке чата — там же теперь и вкладка задания.
       Панели «•••» и «Память» остались здесь: их ищут по root, и место
       в дереве им безразлично. */''}
  ${/* Меню «•••» — поповер у самой кнопки, а не полоса под шапкой: полоса
       сдвигала ленту и держала два десятка одинаковых чипов вперемешку с
       полем и свёртками («Удалить» стояло рядом с «Экспорт»). Облик выбрал
       владелец по снимкам стенда 29 сентября 2026 (вариант C): название
       полем, частые действия плитками, подробность ответа — сегментами,
       удаление отдельно и красным. «Новый чат» ушёл: он есть в рейке. */''}
  <section class="hall-session-panel hall-pop" data-session-panel="history"${openPanel==='history'?'':' hidden'} aria-label="Действия с разговором">
   <div class="hall-pop-title"><input id="master-session-title" data-master-session-title maxlength="100" value="${esc(current?.title || '')}" aria-label="Название разговора" /><button type="button" class="hall-pop-icon" data-action="master-session-rename" data-id="${esc(sessions.active)}" aria-label="Переименовать" title="Сохранить название (Enter)">${icon('edit')}</button></div>
   <div class="hall-pop-tiles">
    ${tile('pin', current?.pinned?'Открепить':'Закрепить', 'master-session-pin', sessions.active, esc)}
    ${tile('download', 'Экспорт', 'master-session-export', sessions.active, esc, 'Экспорт в Markdown')}
    ${tile('archive', current?.archived?'Вернуть':'В архив', 'master-session-archive', sessions.active, esc, current?.archived?'Вернуть из архива':'Убрать в архив')}
    ${current?.branchOffer === 'skipped' ? tile('git', 'Ветка', 'master-session-branch', current.id, esc, 'Создать ветку для плана') : current?.branchOffer === 'bound' ? `<span class="hall-pop-tile is-static" title="Ветка: ${esc(current.branchName)}">${icon('git')}<span>${esc(current.branchName)}</span></span>` : ''}
   </div>
   <div class="hall-pop-sep"></div>
   ${masterAnswerStyleHtml(sessions,esc)}
   <button type="button" class="hall-pop-row" role="switch" aria-checked="${Boolean(sessions.autoRunReadOnly)}" data-action="master-session-auto-read-only" data-value="${!sessions.autoRunReadOnly}" title="Инструменты: read_file, list_files, search_text, project_map, search_code. Задания вне этих пределов требуют подтверждения.">${icon('retry')}<span>Автозапуск на чтение</span><span class="hall-pop-switch" aria-hidden="true"></span></button>
   <small class="hall-pop-note">В режиме «Выполнить» агент читает проект без записи, команд и сети: до 20 000 токенов и 2 минут.</small>
   <button type="button" class="hall-pop-row" data-action="master-session-toggle" data-panel="memory">${icon('memory')}<span>Память проекта</span><small>${proposed?`<i class="hall-pop-dot"></i>${proposed} ${proposed===1?'новая':'новых'}`:''}${icon('chevron-right')}</small></button>
   ${developmentHtml?`<button type="button" class="hall-pop-row" data-action="master-session-toggle" data-panel="development">${icon('bolt')}<span>Навыки и развитие</span><small>${icon('chevron-right')}</small></button>`:''}
   ${current?.summary?`<details class="hall-pop-more"><summary class="hall-pop-row">${icon('text')}<span>Резюме беседы</span><small>${icon('chevron-right')}</small></summary><p>${esc(current.summary)}</p></details>`:''}
   <div class="hall-pop-sep"></div>
   <button type="button" class="hall-pop-row is-danger" data-action="master-session-delete" data-id="${esc(sessions.active)}">${icon('trash')}<span>Удалить разговор</span></button>
  </section>
  <section class="hall-session-panel hall-pop is-memory" data-session-panel="memory"${openPanel==='memory'?'':' hidden'} aria-label="Память проекта">
   <button type="button" class="hall-pop-back" data-action="master-session-toggle" data-panel="history" aria-label="Назад к действиям с разговором">${icon('chevron-right')}<span>Память проекта</span></button>
   <small class="hall-pop-note">Подтверждённые записи используются в других чатах проекта.</small>
   ${(sessions.memoryEntries || []).map(v=>masterMemoryEntryHtml(v,sessions.memoryEntries,esc)).join('')}
   <label class="hall-pop-add">Добавить запись<textarea data-master-memory rows="2" maxlength="4000" placeholder="Решение или предпочтение проекта…"></textarea></label><button type="button" class="hall-chip" data-action="master-session-memory-save">Сохранить память</button>
  </section>
  ${/* «Навыки и развитие» открывались только с шагов настройки Мастера в
       онбординге: найти, чему Мастер научился, и откатить это было почти
       негде. Панель живёт в том же меню, что и память проекта. */''}
  ${developmentHtml?`<section class="hall-session-panel hall-pop is-development" data-session-panel="development"${openPanel==='development'?'':' hidden'} aria-label="Навыки и развитие">
   <button type="button" class="hall-pop-back" data-action="master-session-toggle" data-panel="history" aria-label="Назад к действиям с разговором">${icon('chevron-right')}<span>Навыки и развитие</span></button>
   ${developmentHtml}
  </section>`:''}
 </div>`
}
// Подробность ответа переехала в меню «•••» полосы разговора: её меняют раз в
// месяц, а место в ряду управления она занимала всегда. В ряду остаётся то, что
// выбирают перед каждой репликой, — режим работы и модель.
function masterAnswerStyleHtml(sessions,esc){
 if(!sessions)return ''
 // Отметку считаем от той же ступени, что и надпись: у сессии в этом поле
 // бывает и чужое значение, и тогда отмечается «Авто».
 const modeId=modes.find(v=>v[0]===sessions.mode)?.[0] || 'auto'
 return `<div class="hall-pop-label" id="master-answer-style-label">Подробность ответа</div><div class="hall-pop-seg" role="group" aria-labelledby="master-answer-style-label">${modes.map(([id,label,short])=>`<button type="button" data-action="master-session-mode" data-value="${id}" aria-pressed="${modeId===id}" title="${label}">${short || label}</button>`).join('')}</div>`
}
// Плитка частого действия: значок над подписью, полное имя — в подсказке.
function tile(glyph,label,action,id,esc,title=label){
 return `<button type="button" class="hall-pop-tile" data-action="${action}" data-id="${esc(id || '')}" title="${esc(title)}">${icon(glyph)}<span>${esc(label)}</span></button>`
}

// Ряд управления композера: чем занят Мастер и какой моделью отвечает.
//
// Модель вернулась сюда из шапки: у эталона её выбирают там же, где пишут, и
// два места для одного имени — это два места, куда за ним идти. Ряд тихий
// целиком: у каждого элемента значок и слово, заливка — только под указателем.
export function masterComposerHtml(sessions,model,esc,sending,modelChip){
 if(!sessions)return ''
 // Режим работы: отметка считается от ступени, а не от сырого поля. Значок
 // режима стоит и в свёрнутом меню, и в строке списка: по нему режим узнаётся
 // в ряду рядом со скрепкой «Контекста» и точкой модели, не читая слова.
 const works=[['discuss','Обсудить','chat'],['plan','Спланировать','list'],['execute','Выполнить','play'],['agent','Агент','tool']]
 const work=works.find(v=>v[0]===sessions.workMode) || works[0]
 const [workId,workLabel,workIcon]=work
 // Модель этого разговора, если её переопределили: ядро берёт её вместо общей
 // (internal/app/master_turns.go). Общая названа чипом рядом, и повторять её
 // здесь незачем — говорим только о расхождении.
 const sessionModel=String(sessions.model || '').trim()
 const commonModel=String(model || '').trim()
 const modelChoice=sessionModel && sessionModel !== commonModel
  ? `<button type="button" class="hall-chip hall-model-choice" data-action="master-session-model" data-id="${esc(sessions.active || '')}" title="Этот разговор закреплён за другой моделью" ${sending?'disabled':''}>только здесь: ${esc(sessionModel)}</button>`
  : ''
 return `<div class="hall-composer-controls">
  <details class="hall-work-menu"><summary>${icon(workIcon)}${esc(workLabel)}</summary><div class="hall-work-modes" role="group" aria-label="Режим работы">${works.map(([id,label,glyph])=>`<button type="button" class="hall-chip" data-action="master-session-workMode" data-value="${id}" aria-pressed="${workId===id}">${icon(glyph)}${label}</button>`).join('')}</div></details>
  ${modelChip || ''}
  ${modelChoice}
 </div>`
}
