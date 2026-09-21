import { workOrderRunHtml } from './work-order-execution-views.js'
import { masterAgentConsent } from './master-agent-card.js'
import { countOf, list } from './format-units.js'
import { masterCardMoreAttrs } from './master-card-open.js'
import { DEFAULT_CRITERION_KIND, questChecklistHtml, questMenuHtml } from './master-quest-views.js'

const labels = {
  discussion: 'Нужно уточнение', ready: 'Готов к запуску', approved: 'Утверждён',
}

// Провал — такое же состояние квеста, как остальные, и без него карточка
// рисовала «✓ failed · <текст ошибки>» зелёной галочкой успеха: ключа не было
// ни в подписях, ни в знаках, ни в тонах, и все три словаря отдавали запасное
// значение «готово».
const runtimeLabels = {
  preflight:'Проверяем окружение', running:'Квест выполняется', awaiting_user:'Нужны данные пользователя',
  verifying:'Проверяем результат', applying:'Переносим в проект', completed:'Готово', needs_review:'Нужна ручная приёмка',
  blocked:'Заблокирован', failed:'Провален', paused:'На паузе', cancelled:'Отменён',
}

// Знак и тон состояния. Строка утверждённого наряда всегда начиналась зелёной
// галочкой — и «✓ Заблокирован» получалось зелёным успехом, хотя квест стоит, а
// причина написана тут же. Галочка принадлежит только исходу «готово»:
// остановка помечается знаком внимания, отмена — крестом, пауза — паузой, а
// работа в ходу — точкой.
const runtimeMarks = {
  completed:'✓', needs_review:'!', blocked:'!', failed:'✕', awaiting_user:'?', cancelled:'✕', paused:'‖',
  preflight:'·', running:'·', verifying:'·', applying:'·',
}
const runtimeTones = {
  completed:'is-done', needs_review:'is-attention', blocked:'is-attention', failed:'is-attention', awaiting_user:'is-attention',
  cancelled:'is-quiet', paused:'is-quiet',
  preflight:'is-active', running:'is-active', verifying:'is-active', applying:'is-active',
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
// в предложении квеста, и расходиться им незачем.
function criteriaChecklistHtml(order, esc) {
  const done = criteriaDoneIds(order)
  return questChecklistHtml('Условия готовности', list(order.criteria).map(item => ({
    text: item.text || item.id,
    kind: item.kind || DEFAULT_CRITERION_KIND,
    done: done.has(String(item.id)),
  })), esc, { empty: 'Условия готовности не заданы' })
}

function rows(values, esc) {
  return list(values).length ? `<ul>${list(values).map(value=>`<li>${esc(value)}</li>`).join('')}</ul>` : '<span class="master-v2-empty">Нет</span>'
}

function jsonValue(value, esc) {
  return esc(JSON.stringify(value ?? {}, null, 2))
}

export function masterWorkOrderCardsHtml(orders, esc, busyIds = new Set(), deps = {}) {
  return list(orders).map(order => {
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
	// Утверждение создаёт исполнителей из ростера. Пока карточка знала только
	// обещание «будет создан», человек искал создание агента, которого ядро уже
	// создало.
	const createdAgents=new Set(list(runtime?.agentIds))
	const receipt=runtime?.deliveryReceipt
	const evidence=runtime?.evidence
	const checks=list(evidence?.verificationChecks)
	const calls=list(evidence?.modelCalls)
	const knownCost=calls.filter(item=>item.costKnown).reduce((sum,item)=>sum+Number(item.costCents||0),0)
	const unknownCost=calls.filter(item=>!item.costKnown).length
	const tokens=calls.reduce((sum,item)=>sum+Number(item.inputTokens||0)+Number(item.outputTokens||0),0)
	const passedChecks=checks.filter(item=>item.satisfied).length
	const finalCommit=list(evidence?.commitIds).at(-1) || receipt?.commitId || ''
	const evidenceSummary=evidence?.id ? `<details class="master-v2-evidence"${masterCardMoreAttrs(`run-evidence:${order.id}`,{esc,open:runtime?.status==='completed'})}>
		<summary>Доказательства результата · ${passedChecks}/${countOf(checks.length,'проверка','проверки','проверок')}</summary>
        <div class="master-v2-grid">
          <section><b>EvidenceBundle</b><strong>v${Number(evidence.version)||0}</strong><code>${esc(evidence.id)}</code></section>
          <section><b>Проверки</b>${checks.length?checks.map(item=>`<span>${item.satisfied?'✓':'!'} ${esc(item.kind || item.id)}${item.command?` · <code>${esc(item.command)}</code>`:''}${item.exitCode!=null?` · exit ${Number(item.exitCode)}`:''}</span>`).join(''):'<span>Машинные проверки не записаны</span>'}</section>
          <section><b>Изменения</b><strong>${countOf(list(evidence.changedFiles).length,'файл','файла','файлов')}</strong>${finalCommit?`<code>${esc(finalCommit)}</code>`:'<span>Без итогового commit</span>'}</section>
          <section><b>Модели и расход</b><strong>${tokens.toLocaleString('ru-RU')} токенов</strong><span>${countOf(calls.length, 'вызов', 'вызова', 'вызовов')} · известная стоимость $${(knownCost/100).toFixed(2)}${unknownCost?` · ${unknownCost} без цены`:''}</span></section>
          <section><b>Ограничения</b>${rows(evidence.knownLimitations,esc)}</section>
          <section><b>Ревизия доставки</b><code>${esc(evidence.workspaceRevision || receipt?.workspaceRevision || '')}</code><span>${esc(evidence.deliveryTarget || receipt?.target || '')}</span></section>
        </div>
      </details>` : ''
	const editor=order.state!=='approved' ? `<details class="master-v2-editor"${masterCardMoreAttrs(`order-edit:${order.id}`,{esc})}>
        <summary>Редактировать карточку без запроса к модели</summary>
        <div class="master-v2-editor-simple">
          <label><span>Цель</span><input data-work-order-field="goal" maxlength="4096" value="${esc(order.goal || '')}"></label>
          <label><span>Что будет сделано · один пункт на строку</span><textarea data-work-order-field="scope" rows="4">${esc(list(order.scope).join('\n'))}</textarea></label>
          <label><span>Предположения · один пункт на строку</span><textarea data-work-order-field="assumptions" rows="3">${esc(list(order.assumptions).join('\n'))}</textarea></label>
          <label><span>Вне scope · один пункт на строку</span><textarea data-work-order-field="outOfScope" rows="3">${esc(list(order.outOfScope).join('\n'))}</textarea></label>
        </div>
        <details class="master-v2-editor-advanced"${masterCardMoreAttrs(`order-edit-json:${order.id}`,{esc})}><summary>Профессиональные настройки</summary>
          <p>JSON редактирует точный контракт. Сервер проверит версии, права, секреты, сеть и критерии до создания новой immutable-версии.</p>
          ${['criteria','milestones','completion','workspace','stack','roster','routing','network','secrets','budget','delivery'].map(field=>`<label><span>${field}</span><textarea data-work-order-json="${field}" rows="${field==='criteria'||field==='milestones'?8:5}">${jsonValue(order[field],esc)}</textarea></label>`).join('')}
        </details>
        <div class="master-v2-editor-actions"><button type="button" class="hall-btn is-primary" data-action="save-master-work-order-v2" data-id="${esc(order.id)}" ${busy?'disabled':''}>${busy?'Сохраняем…':'Сохранить новую версию'}</button></div>
      </details>` : ''
	// Утверждённый наряд без рантайма — договор, по которому работа так и не
	// пошла: квест не создан или уже удалён. Прежняя подпись обещала слежение за
	// тем, чего нет, и карточка читалась как незакрытое дело.
	const approvedText=runtime ? `${runtimeLabels[runtime.status] || runtime.status}${runtime.message?` · ${runtime.message}`:''}` : 'Квест не создан — работа по этому наряду не идёт'
	const pausable=runtime && ['preflight','running','verifying','applying','awaiting_user'].includes(runtime.status)
	// Пауза — решение человека, блокировка — состояние среды. Второе тоже
	// возобновляемо: причину чинят и просят повторить проверку окружения. Пока
	// этой кнопки не было, у заблокированного наряда оставалась одна дорога —
	// отмена, то есть заново весь разговор с Мастером.
	// Ожидание человека возобновляемо так же, как пауза и блокировка: работа
	// стоит на нём, он отдал ключ или авторизовал CLI и просит продолжить.
	// Пока этого состояния тут не было, у квеста, ждущего ключ, не оставалось
	// ни одной кнопки — только отмена.
	const resumable=['paused','blocked','failed','awaiting_user'].includes(runtime?.status)
	const resumeLabel=runtime?.status==='paused' ? 'Продолжить' : 'Повторить запуск'
	// Песочница выключена по умолчанию, и ядро честно отказывается запускать
	// автономный проект. Отказ без выхода читается как поломка, поэтому рядом
	// стоит само действие: настройка плюс перезапуск ядра.
	const sandboxFix=runtime?.status==='blocked' && /docker\s*sandbox/i.test(String(runtime.message || ''))
		? `<button type="button" class="hall-btn" data-action="enable-docker-sandbox">Включить Docker sandbox</button>` : ''
	const cancellable=runtime && !['completed','cancelled'].includes(runtime.status)
	const runtimeControls=order.state==='approved' && runtime?.questId && cancellable ? `<div class="master-v2-runtime-controls">
        <div>${pausable?`<button type="button" class="hall-btn" data-action="control-master-work-order-v2" data-control="pause" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId)}" ${busy?'disabled':''}>Пауза</button>`:''}${sandboxFix}${resumable?`<button type="button" class="hall-btn is-primary" data-action="control-master-work-order-v2" data-control="resume" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId)}" ${busy?'disabled':''}>${resumeLabel}</button>`:''}<button type="button" class="hall-btn" data-action="control-master-work-order-v2" data-control="cancel" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId)}" ${busy?'disabled':''}>Отменить</button></div>
        <label><span>Сообщение активному квесту</span><input data-work-order-message maxlength="32768" placeholder="Уточнение без изменения scope"><button type="button" class="hall-btn" data-action="control-master-work-order-v2" data-control="message" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId)}" ${busy?'disabled':''}>Отправить</button></label>
      </div>` : ''
	const applicationControls=runtime?.status==='completed' && receipt?.id ? `<div class="master-v2-runtime-controls master-v2-application-controls">
        <div><strong>Приложение готово</strong>${receipt.url?`<a href="${esc(receipt.url)}" title="Открыть приложение">${esc(receipt.url)}</a>`:'<span>Локальный URL не указан</span>'}</div>
        <div><button type="button" class="hall-btn is-primary" data-action="control-master-application-v2" data-control="start" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId)}" data-version="${Number(order.version)||1}" data-digest="${esc(order.digest || '')}" data-receipt-id="${esc(receipt.id)}" ${busy?'disabled':''}>Запустить</button><button type="button" class="hall-btn" data-action="control-master-application-v2" data-control="stop" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId)}" data-version="${Number(order.version)||1}" data-digest="${esc(order.digest || '')}" data-receipt-id="${esc(receipt.id)}" ${busy?'disabled':''}>Остановить</button></div>
      </div>` : ''
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
          <section><b>Workspace</b><code>${esc(order.workspace?.path || '')}</code><span>${esc(order.workspace?.mode || '')} · ${esc(order.workspace?.isolation || '')}${order.workspace?.initializeGit?' · Git':''}</span></section>
          <section><b>Стек</b><strong>${esc(order.stack?.id || 'recommended')}</strong><span>${esc(order.stack?.category || '')} · preset ${esc(order.stack?.version || '')}</span></section>
          <section><b>Модели</b><strong>${esc(order.routing?.fixedModel || order.routing?.routerModel || '')}</strong><span>${esc(order.routing?.mode || 'fixed')} · ${esc(order.routing?.certification || 'experimental')}${order.routing?.costKnown?'':' · стоимость неизвестна'}</span></section>
          <section><b>Бюджет</b><strong>${Number(order.budget?.tokens || 0).toLocaleString('ru-RU')} токенов</strong><span>${Number(order.budget?.activeSeconds || 0)} сек · ${countOf(Number(order.budget?.maxSteps || 0), 'шаг', 'шага', 'шагов')} · параллельно ${Number(order.budget?.maxParallel || 1)}</span></section>
          <section><b>Агенты</b>${agents.length?agents.map(agent=>{const created=agent.existing || createdAgents.has(agent.id);return `<span>${created?'✓':'＋'} ${esc(agent.name)} · ${esc(agent.role)}${agent.requiresConsent?(created?' · создан':' · будет создан'):''}</span>`}).join(''):'<span>Постоянные агенты не нужны</span>'}${temporary.map(agent=>`<span>↳ ${esc(agent.role)} · временный</span>`).join('')}</section>
          <section><b>Источники</b><strong>${sourceCount}</strong><span>Каждый привязан к неизменяемому digest</span></section>
          <section><b>Сеть</b>${network.length?network.map(item=>`<span><code>${esc(item.host)}</code> — ${esc(item.purpose)}</span>`).join(''):'<span>Исходящая сеть не требуется</span>'}</section>
          <section><b>Секреты</b>${secrets.length?secrets.map(item=>`<span>${item.satisfied?'✓':'!'} ${esc(item.name)} — ${esc(item.purpose)}</span>`).join(''):'<span>Не требуются</span>'}</section>
          <section><b>Проверки завершения</b>${completionChecks.length?completionChecks.map(item=>`<span>${esc(item.kind)}${item.command?` · <code>${esc(item.command)}</code>`:' · по критериям приёмки'}</span>`).join(''):'<span>Профиль не задан</span>'}</section>
          <section><b>Доставка</b><strong>${order.delivery?.applyMode==='manual'?'Ручная приёмка':'Автоматически после проверок'}</strong><span>${esc(order.delivery?.commitMode || 'squash')} · частичный результат ${countOf(Number(order.delivery?.keepPartialDays || 30), 'день', 'дня', 'дней')}</span></section>
          <section><b>Предположения</b>${rows(order.assumptions,esc)}</section>
          <section><b>Вне scope</b>${rows(order.outOfScope,esc)}</section>
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
	if (executing) return workOrderRunHtml(order, deps.ui, {
		...deps, esc,
		statusText: approvedText,
		tone: runtimeTones[runtime.status] || 'is-done',
		mark: runtimeMarks[runtime.status] || '✓',
		resumable, resumeLabel,
		controlsHtml: runtimeControls,
		compositionHtml, createdHtml,
		checklistHtml: criteriaChecklistHtml(order, esc),
		applicationHtml: applicationControls,
		evidenceHtml: evidenceSummary,
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
      ${criteriaChecklistHtml(order, esc)}
      ${compositionHtml}
      ${lifecycleNote}
      ${runtimeControls}
      ${applicationControls}
	  ${evidenceSummary}
	  ${editor}
      <footer>
        ${/* Решение одно, остальное — в меню. Четыре кнопки в ряд не говорили,
             какая из них главная: «Обсудить», «Подтвердить», «Убрать» и
             подпись стояли одним весом, и глаз выбирал крайнюю левую. */''}
        ${order.state==='approved'
          ? `<span class="master-v2-approved ${runtime?(runtimeTones[runtime.status] || 'is-done'):'is-quiet'}">${runtime?(runtimeMarks[runtime.status] || '✓'):'·'} ${esc(approvedText)}</span>${runtime?'':questMenuHtml([{action:'delete-work-order-v2',id:order.id,label:'Убрать наряд',busy}],esc)}`
          : `<div class="hall-quest-acts">
              <button type="button" class="hall-btn is-primary" data-action="approve-master-work-order-v2" data-id="${esc(order.id)}" data-version="${Number(order.version)||1}" data-digest="${esc(order.digest || '')}" ${ready&&consented&&!busy?'':'disabled'}>${busy?'Запускаем…':esc(approveLabel)}</button>
              ${questMenuHtml([{action:'revise-master-work-order-v2',id:order.id,label:'Обсудить с Мастером'}],esc)}
            </div>${consentNote}`}
      </footer>
    </section>`
  }).join('')
}
