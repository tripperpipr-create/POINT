import { completionCheckName, questRunHtml } from './quest-run-views.js'
import { masterAgentConsent } from './master-agent-card.js'
import { countOf, list } from './format-units.js'
import { masterCardMoreAttrs } from './master-card-open.js'
import { manualReviewHtml } from './master-manual-review.js'
import { DEFAULT_CRITERION_KIND, questChecklistHtml, questMenuHtml } from './master-quest-views.js'
import { runtimePresentation } from './quest-status.js'

const labels = {
  discussion: 'Нужно уточнение', staffing: 'Собираем состав', ready: 'Готов к запуску', approved: 'Утверждён',
}

// Какие условия закрыты — поимённо.
//
// Закрытость приходит из `EvidenceBundle.criteria`: ядро кладёт туда по записи
// на каждое условие наряда, с его же идентификатором в `criterionId`. До
// запуска записей нет, и все условия ждут — это честно, потому что закрывает
// их прогон, а не план.
//
// Считать по `verificationChecks` нельзя, хотя соблазн велик: список лежит
// рядом и у него есть `satisfied`. Он отвечает на другой вопрос. Ручных
// условий в нём нет вовсе, а проверки профиля завершения (`completion:health`
// и такие же) есть, и их идентификаторы условиям не принадлежат. Наряд с двумя
// проваленными условиями и четырьмя зелёными проверками профиля показывал
// «2 / 2 закрыто».
//
// Сопоставление по номеру в списке не годится тем же: закрытым помечалось
// первое условие, а не то, которое прошло.
function criteriaDoneIds(order) {
  const done = new Set()
  for (const item of list(order.runtime?.evidence?.criteria)) {
    if (item?.satisfied && item.criterionId) done.add(String(item.criterionId))
  }
  return done
}

// Чек-лист условий готовности — главный герой карточки квеста. Рисует его
// общая ячейка (master-quest-views.js): та же форма стоит в составе задания и
// в предложении квеста, и расходиться им незачем. Прогон добавляет к рядам то,
// чем каждое условие доказано (quest-run-views.js), поэтому ряды отдельно.
function criteriaRows(order) {
  const done = criteriaDoneIds(order)
  return list(order.criteria).map(item => ({
    id: String(item.id || ''),
    text: item.text || item.id,
    kind: item.kind || DEFAULT_CRITERION_KIND,
    done: done.has(String(item.id)),
  }))
}

// Значения договора — закрытые списки ядра (internal/domain/work_order_v2.go):
// режим папки, изоляция, маршрут моделей, сертификация, коммит.
const WORD = {
  existing: 'существующая', managed: 'управляемая Point', snapshot: 'снимок', git_worktree: 'отдельная копия Git', live_write: 'запись напрямую',
  fixed: 'одна модель', auto: 'маршрутизатор', certified: 'проверенная', experimental: 'экспериментальная',
  squash: 'один коммит', staged: 'изменения в индексе', none: 'без коммита',
}

function rows(values, esc) {
  return list(values).length ? `<ul>${list(values).map(value=>`<li>${esc(value)}</li>`).join('')}</ul>` : '<span class="master-v2-empty">Нет</span>'
}

function jsonValue(value, esc) {
  return esc(JSON.stringify(value ?? {}, null, 2))
}

export function masterWorkOrderCardsHtml(orders, esc, busyIds = new Set(), deps = {}) {
  return list(orders).filter(order => order?.state === 'ready' || order?.state === 'approved').map(order => {
    let ready=order.state==='ready' && order.digest
    const busy=busyIds.has(order.id)
    const agents=list(order.roster?.permanent)
	const selectedAgentIds=list(order.roster?.agentIds).length ? list(order.roster?.agentIds) : agents.map(item=>item.id)
	const projectAgents=list(deps.ui?.state?.boot?.projectAgents)
	const selectedAgents=selectedAgentIds.map(id=>projectAgents.find(item=>item.id===id)).filter(Boolean)
	const draftAgents=selectedAgents.filter(item=>item.status==='draft')
	const unavailableAgents=selectedAgents.filter(item=>item.status && item.status!=='active' && item.status!=='draft')
	if(draftAgents.length || unavailableAgents.length) ready=false
    const temporary=list(order.roster?.temporary)
    const network=list(order.network)
    const secrets=list(order.secrets)
    const criteria=list(order.criteria)
    const completionChecks=list(order.completion?.checks)
    const sourceCount=list(order.sources).length
	const runtime=order.runtime
	const runtimeView=runtimePresentation(runtime)
	// Утверждение создаёт исполнителей из ростера. Пока карточка знала только
	// обещание «будет создан», человек искал создание агента, которого ядро уже
	// создало.
	const createdAgents=new Set(list(runtime?.agentIds))
	const sandbox=order.sandbox || {}
	const sandboxTools=sandbox.toolchains || {}
	const versionFields=['node','npm','php','composer','python','pip','go','rust','java','mvn','gradle','dotnet']
	const versionsHtml=`<section class="master-v2-editor-simple"><b>Версии песочницы</b><p>Point предложил версии из CI и проекта. Изменения сохраняются новой версией наряда.</p>${versionFields.map(tool=>`<label><span>${esc(tool)}${sandbox.versionSources?.[tool]?` · ${esc(sandbox.versionSources[tool])}`:''}</span><input data-work-order-toolchain="${tool}" value="${esc(sandboxTools[tool]||'')}" placeholder="Авто"></label>`).join('')}${list(sandbox.versionConflicts).map(conflict=>`<small>${esc(conflict)}</small>`).join('')}<small>Образ: ${esc(sandbox.image||'будет выбран Point')}</small></section>`
	const editor=(order.state!=='approved' || runtime?.status==='paused') ? `<details class="master-v2-editor"${masterCardMoreAttrs(`order-edit:${order.id}`,{esc})}>
        <summary>Редактировать карточку без запроса к модели</summary>
        <div class="master-v2-editor-simple">
          <label><span>Цель</span><input data-work-order-field="goal" maxlength="4096" value="${esc(order.goal || '')}"></label>
          <label><span>Что будет сделано · один пункт на строку</span><textarea data-work-order-field="scope" rows="4">${esc(list(order.scope).join('\n'))}</textarea></label>
          <label><span>Предположения · один пункт на строку</span><textarea data-work-order-field="assumptions" rows="3">${esc(list(order.assumptions).join('\n'))}</textarea></label>
          <label><span>Вне задачи · один пункт на строку</span><textarea data-work-order-field="outOfScope" rows="3">${esc(list(order.outOfScope).join('\n'))}</textarea></label>
        </div>
		${versionsHtml}
        <details class="master-v2-editor-advanced"${masterCardMoreAttrs(`order-edit-json:${order.id}`,{esc})}><summary>Профессиональные настройки</summary>
          <p>JSON редактирует точный контракт. Сервер проверит версии, права, секреты, сеть и критерии до создания новой immutable-версии.</p>
		  ${['criteria','milestones','completion','workspace','stack','roster','routing','network','secrets','budget','delivery'].map(field=>`<label><span>${field}</span><textarea data-work-order-json="${field}" rows="${field==='criteria'||field==='milestones'?8:5}">${jsonValue(order[field],esc)}</textarea></label>`).join('')}
        </details>
        <div class="master-v2-editor-actions"><button type="button" class="hall-btn is-primary" data-action="save-master-work-order-v2" data-id="${esc(order.id)}" ${busy?'disabled':''}>${busy?'Сохраняем…':'Сохранить новую версию'}</button></div>
      </details>` : ''
	// Утверждённый наряд без рантайма — договор, по которому работа так и не
	// пошла: квест не создан или уже удалён. Прежняя подпись обещала слежение за
	// тем, чего нет, и карточка читалась как незакрытое дело.
	const approvedText=runtime ? `${runtimeView.label}${runtime.message?` · ${runtime.message}`:''}` : 'Квест не создан — работа по этому наряду не идёт'
	const pausable=runtime && ['preflight','running','verifying','applying','awaiting_user'].includes(runtime.status)
	// Пауза — решение человека, блокировка — состояние среды. Второе тоже
	// возобновляемо: причину чинят и просят повторить проверку окружения. Пока
	// этой кнопки не было, у заблокированного наряда оставалась одна дорога —
	// отмена, то есть заново весь разговор с Мастером.
	// Ожидание человека возобновляемо так же, как пауза и блокировка: работа
	// стоит на нём, он отдал ключ или авторизовал CLI и просит продолжить.
	// Пока этого состояния тут не было, у квеста, ждущего ключ, не оставалось
	// ни одной кнопки — только отмена.
	// Вердикт шлюза окончателен: исправление идёт новой версией наряда через Мастера.
	const resumable=['paused','blocked','awaiting_user'].includes(runtime?.status) && runtime?.stall?.waitReason!=='stage_failed' && !(runtime?.status==='blocked' && runtime?.evidence?.id)
	const resumeLabel=runtime?.status==='paused' ? 'Продолжить' : 'Повторить запуск'
	// Песочница выключена по умолчанию, и ядро честно отказывается запускать
	// автономный проект. Отказ без выхода читается как поломка, поэтому рядом
	// стоит само действие: настройка плюс перезапуск ядра.
	const sandboxFix=runtime?.status==='blocked' && /docker\s*sandbox/i.test(String(runtime.message || ''))
	const cancellable=runtime && ['preflight','running','verifying','applying','paused','awaiting_user','needs_review','blocked'].includes(runtime.status)
	const messageable=runtime && ['running','verifying','applying'].includes(runtime.status)
		&& list(runtime.stages).some(stage => stage.runId && ['running','waiting_approval'].includes(stage.status))
	// Утверждённый наряд перестаёт быть предложением: решать в нём больше
	// нечего, а место нужно тому, что происходит сейчас. Состав уходит под один
	// раскрывающийся заголовок, на его месте — экран выполнения.
	const executing=order.state==='approved' && Boolean(runtime)
	// «Что будет сделано» уходит в подробности к шагам и отряду: по нему не
	// решают, а читают, когда решили читать. Наверху остаётся то, по чему квест
	// принимают, — условия готовности.
	const summaryHtml=`<div class="master-v2-summary">
        <section><b>Что будет сделано</b>${rows(order.scope,esc)}</section>
      </div>`
	// Вопрос Мастера — состояние квеста, а не примечание к нему: точка тем же
	// тоном, что и кикер, и счёт вопросов прямо в строке.
	const questionCount=list(order.openQuestions).length
	const questionsHtml=questionCount?`<div class="master-v2-warning">
        <div class="hall-quest-kick is-ask"><span class="hall-quest-dot" aria-hidden="true"></span><b>Мастер ждёт ${countOf(questionCount,'уточнение','уточнения','уточнений')}</b></div>
        ${rows(order.openQuestions,esc)}
      </div>`:''
	const detailsGridHtml=`<div class="master-v2-grid">
          <section><b>Рабочая папка</b><code>${esc(order.workspace?.path || '')}</code><span>${esc(WORD[order.workspace?.mode] || order.workspace?.mode || '')} · ${esc(WORD[order.workspace?.isolation] || order.workspace?.isolation || '')}${order.workspace?.initializeGit?' · Git':''}</span></section>
          <section><b>Стек</b><strong>${esc(order.stack?.id || 'рекомендованный')}</strong><span>${esc(order.stack?.category || '')} · версия набора ${esc(order.stack?.version || '')}</span></section>
          <section><b>Модели</b><strong>${esc(order.routing?.fixedModel || order.routing?.routerModel || '')}</strong><span>${esc(WORD[order.routing?.mode || 'fixed'] || order.routing?.mode)} · ${esc(WORD[order.routing?.certification || 'experimental'] || order.routing?.certification)}${order.routing?.costKnown?'':' · стоимость неизвестна'}</span></section>
          <section><b>Бюджет</b><strong>${Number(order.budget?.tokens || 0).toLocaleString('ru-RU')} токенов</strong><span>${Number(order.budget?.activeSeconds || 0)} сек · ${countOf(Number(order.budget?.maxSteps || 0), 'шаг', 'шага', 'шагов')} · параллельно ${Number(order.budget?.maxParallel || 1)}</span></section>
          <section><b>Агенты</b>${agents.length?agents.map(agent=>{const created=agent.existing || createdAgents.has(agent.id);return `<span>${created?'✓':'＋'} ${esc(agent.name)} · ${esc(agent.role)}${agent.requiresConsent?(created?' · создан':' · будет создан'):''}</span>`}).join(''):'<span>Постоянные агенты не нужны</span>'}${temporary.map(agent=>`<span>↳ ${esc(agent.role)} · временный</span>`).join('')}</section>
          <section><b>Источники</b><strong>${sourceCount}</strong><span>Каждый привязан к неизменяемому digest</span></section>
          <section><b>Сеть</b>${network.length?network.map(item=>`<span><code>${esc(item.host)}</code> — ${esc(item.purpose)}</span>`).join(''):'<span>Исходящая сеть не требуется</span>'}</section>
          <section><b>Секреты</b>${secrets.length?secrets.map(item=>`<span>${item.satisfied?'✓':'!'} ${esc(item.name)} — ${esc(item.purpose)}</span>`).join(''):'<span>Не требуются</span>'}</section>
          <section><b>Проверки завершения</b>${completionChecks.length?completionChecks.map(item=>`<span>${esc(completionCheckName(item.kind))}${item.command?` · <code>${esc(item.command)}</code>`:' · по критериям приёмки'}</span>`).join(''):'<span>Профиль не задан</span>'}</section>
          <section><b>Доставка</b><strong>${order.delivery?.applyMode==='manual'?'Ручная приёмка':'Автоматически после проверок'}</strong><span>${esc(WORD[order.delivery?.commitMode || 'squash'] || order.delivery?.commitMode)} · частичный результат ${countOf(Number(order.delivery?.keepPartialDays || 30), 'день', 'дня', 'дней')}</span></section>
          <section><b>Предположения</b>${rows(order.assumptions,esc)}</section>
          <section><b>Вне задачи</b>${rows(order.outOfScope,esc)}</section>
        </div>`
	const compositionHtml=executing
		? `<details class="master-v2-composition"${masterCardMoreAttrs(`run:${order.id}`,{esc})}><summary>Состав задания</summary>${summaryHtml}${questionsHtml}${detailsGridHtml}</details>`
		: `${summaryHtml}${questionsHtml}<details${masterCardMoreAttrs(`order:${order.id}`,{esc})}><summary>Подробности</summary>${detailsGridHtml}</details>`
	// Согласие на создание исполнителя живёт в своей карточке ленты.
	//
	// Раньше оно раскрывалось ярусом прямо здесь: черновик агента с именем,
	// ролью и моделью был вложен в чужой документ, и создание новой сущности
	// проекта читалось как подпункт запуска квеста. Карточке запуска остаётся
	// то, что и было её делом: сказать, что запускать пока нельзя, и назвать
	// причину. Само согласие и правку черновика ведёт master-agent-card.js.
	const consentDrafts=agents.filter(agent=>agent.requiresConsent && !agent.existing && !createdAgents.has(agent.id))
	const consented=consentDrafts.length===0 || masterAgentConsent.has(order.id)
	// Пока исполнителя нет, запускать нечем, и кнопка об этом говорит прямо, а
	// не молча блокируется: причина стоит рядом с ней и называет, где решение.
	const consentNote=consented ? '' : `<small class="master-v2-consent-note">Сначала заведите ${consentDrafts.length > 1 ? 'исполнителей' : 'исполнителя'} — карточка ниже</small>`
	const lifecycleNote=draftAgents.length
		? `<aside class="master-v2-warning"><b>Состав ждёт активации</b><p>${countOf(draftAgents.length,'черновик','черновика','черновиков')} блокирует запуск. Сохранение карточки не активирует агента.</p>${draftAgents.map(agent=>`<button type="button" class="hall-btn is-sm" data-action="open-agent-constructor-edit" data-id="${esc(agent.id)}">Открыть ${esc(agent.name || agent.roleFamily || 'черновик')}</button>`).join('')}</aside>`
		: (unavailableAgents.length ? `<aside class="master-v2-warning"><b>Исполнитель недоступен</b><p>Дождитесь завершения оценки или выберите активного агента.</p></aside>` : '')
	const approveLabel='Запустить квест'
	// Созданный исполнитель — событие, а не строка под свёрнутыми подробностями.
	// Утверждение создаёт агента в своей транзакции, и человек имеет право сразу
	// увидеть, кто появился, и уйти в его мастерскую.
	const createdDrafts=agents.filter(agent=>!agent.existing && createdAgents.has(agent.id))
	const createdHtml=executing && createdDrafts.length ? `<div class="master-v2-created">${createdDrafts.map(agent=>`<span>✓ Агент создан: <b>${esc(agent.name || agent.id)}</b></span><button type="button" class="hall-btn is-sm" data-action="open-agent-constructor-edit" data-id="${esc(agent.id)}">Открыть мастерскую</button>`).join('')}</div>` : ''
	// Запущенный квест уходит из ленты как карточка и возвращается как прогон.
	// Шапка прогона несёт цель и исход, поэтому экрану выполнения статус больше
	// не передаётся: второй раз то же слово читается как два разных состояния.
	// Чек-лист уходит в прогон вместе с квестом, а не пропадает на запуске.
	// Вариант 1b макета держится на том, что карточка одна на весь квест и
	// отметки в ней заполняются: до запуска — счёт условий, после — какие
	// именно закрыты. Пока чек-лист рисовала только незапущенная карточка,
	// заполняться было нечему, и «0 / 4» оставалось единственным его видом.
	if (executing) return questRunHtml(order, deps.ui, {
		...deps, esc,
		label: runtimeView.label,
		tone: runtimeView.tone,
		mark: runtimeView.mark,
		controls: { pausable, resumable, resumeLabel, sandboxFix, cancellable, messageable, busy },
		criteriaRows: criteriaRows(order),
		compositionHtml, createdHtml,
		manualReviewHtml: manualReviewHtml(order, esc),
	})
    // Шапка квеста: точка состояния, кикер и мета справа. Гриф «ЕДИНАЯ
    // КАРТОЧКА ЗАПУСКА» прописными ушёл — он называл документ, а не то, что с
    // ним делают, и был единственным капсом в тихом регистре разговора.
    const kick=order.state==='approved' ? 'Квест утверждён' : order.state==='ready' ? 'Квест ждёт решения' : 'Квест уточняется'
    return `<section class="hall-deck master-v2-order state-${esc(order.state || 'discussion')}" data-work-order-id="${esc(order.id)}">
      <header>
        <div>
          <span class="hall-quest-kick"><span class="hall-quest-dot" aria-hidden="true"></span>${esc(kick)}</span>
          <strong>${esc(order.goal || 'Задание')}</strong>
        </div>
        <span>${esc(labels[order.state] || order.state || 'Черновик')} · v${Number(order.version)||1}</span>
      </header>
      ${questChecklistHtml('Условия готовности', criteriaRows(order), esc, { empty: 'Условия готовности не заданы' })}
      ${compositionHtml}
      ${lifecycleNote}
	  ${manualReviewHtml(order, esc)}
	  ${editor}
      <footer>
        ${/* Решение одно, остальное — в меню. Четыре кнопки в ряд не говорили,
             какая из них главная: «Обсудить», «Подтвердить», «Убрать» и
             подпись стояли одним весом, и глаз выбирал крайнюю левую. */''}
        ${order.state==='approved'
          ? `<span class="master-v2-approved ${runtime?runtimeView.tone:'is-quiet'}">${runtime?runtimeView.mark:'·'} ${esc(approvedText)}</span>${runtime?'':questMenuHtml([{action:'delete-work-order-v2',id:order.id,label:'Убрать наряд',busy}],esc)}`
          : `<div class="hall-quest-acts">
              <button type="button" class="hall-btn is-primary" data-action="approve-master-work-order-v2" data-id="${esc(order.id)}" data-version="${Number(order.version)||1}" data-digest="${esc(order.digest || '')}" ${ready&&consented&&!busy?'':'disabled'}>${busy?'Запускаем…':esc(approveLabel)}</button>
              ${questMenuHtml([{action:'revise-master-work-order-v2',id:order.id,label:'Обсудить с Мастером'}],esc)}
            </div>${consentNote}`}
      </footer>
    </section>`
  }).join('')
}

// Новая версия наряда из формы карточки: текстовые поля, профессиональный JSON
// и версии инструментов. Ошибка разбора JSON уходит вызывающему — он пишет её
// под полем ввода, а не молча теряет правку.
export function workOrderDraftFromCard(order, card) {
  const draft=JSON.parse(JSON.stringify(order))
  delete draft.digest;delete draft.runtime;delete draft.approvedVersion;delete draft.approvedDigest
  const lineValues=name=>String(card.querySelector(`[data-work-order-field="${name}"]`)?.value || '').split(/\r?\n/).map(value=>value.trim()).filter(Boolean)
  draft.goal=String(card.querySelector('[data-work-order-field="goal"]')?.value || '').trim()
  draft.scope=lineValues('scope')
  draft.assumptions=lineValues('assumptions')
  draft.outOfScope=lineValues('outOfScope')
  for (const input of card.querySelectorAll('[data-work-order-json]')) {
    const field=String(input.dataset.workOrderJson || '')
    if (field) draft[field]=JSON.parse(String(input.value || 'null'))
  }
  draft.sandbox = draft.sandbox || {}
  draft.sandbox.toolchains = { ...(draft.sandbox.toolchains || {}) }
  for (const input of card.querySelectorAll('[data-work-order-toolchain]')) {
    const tool = String(input.dataset.workOrderToolchain || '')
    const version = String(input.value || '').trim()
    if (tool && version) draft.sandbox.toolchains[tool] = version
    else if (tool) delete draft.sandbox.toolchains[tool]
  }
  return draft
}
