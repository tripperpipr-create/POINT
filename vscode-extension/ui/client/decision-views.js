// Очередь решений — единственный экран, где агент физически стоит и ждёт
// человека.
//
// Порядок и способ разрешения приходят с сервера: клиент не знает про типы
// решений ничего, кроме того, как их показать. Путь разрешения тоже серверный
// — интерфейс пересылает то, что ему дали, и не строит маршрутов сам.

import { sentenceLabel } from './format-units.js'

export function createDecisionViews({ ui, vscode, render, esc, data, state, shell, masterProposalHtml, taskProposalById }) {
  // ── Экран «Решения» ────────────────────────────────────────────────────────
  // Единственное место, где агент физически стоит и ждёт человека. Порядок и
  // способ разрешения приходят с сервера: клиент не знает про типы решений
  // ничего, кроме того, как их показать.

  function decisionWaitLabel(ms) {
    const total = Math.max(0, Math.round(Number(ms || 0) / 1000))
    const minutes = Math.floor(total / 60)
    const seconds = total % 60
    if (minutes >= 60) return `${Math.floor(minutes / 60)}ч ${String(minutes % 60).padStart(2, '0')}м`
    return `${String(minutes).padStart(2, '0')}м ${String(seconds).padStart(2, '0')}с`
  }

  // Подпись кнопки решения — из того значения, которое уходит в ядро.
  //
  // «ПРИНЯТЬ» означало три разных поступка: применить правку в рабочую копию,
  // разрешить агенту запустить команду и стартовать квест. Слово одно, а
  // последствия несопоставимы — и понять, что именно произойдёт, можно было
  // только прочитав карточку целиком.
  const DECISION_VERBS = {
    apply: 'Применить', approve: 'Разрешить', start: 'Запустить',
    deny: 'Запретить', reject: 'Отклонить', ignore: 'Пропустить',
    // Конфликт слияния не «применяют» — его разбирают, выбирая итоговые файлы.
    resolve: 'Разобрать',
  }
  function decisionVerb(value, fallback) {
    return DECISION_VERBS[String(value || '').trim().toLowerCase()] || fallback
  }

  // Что можно будет отменить, а что нет — по виду решения.
  //
  // Панель обещала: «обратимо, кроме помеченного «необратимо»». Метки «необратимо»
  // на этом экране не бывает вовсе — у решения нет такого поля, — и человек читал
  // обещание как «всё, что здесь, откатывается». Выполненную команду откатить
  // нельзя: она уже выполнилась.
  function decisionReversibilityNote(kind) {
    switch (kind) {
      // flow-gate — тот же approval, только внутри флоу: разрешение пускает узел
      // дальше, и выполненное им уже не отменить.
      case 'approval':
      case 'flow-gate':
        return 'Разрешённое действие выполнится сразу — отменить само выполнение нельзя. Решение попадёт в журнал изменений.'
      case 'change-set':
      case 'conflict':
        return 'Применение запишет файлы в рабочую копию. Набор можно откатить из журнала изменений.'
      case 'quest':
        return 'Запуск можно остановить, а квест — откатить из журнала изменений.'
      case 'action':
        // Предложение компаньона создаёт сущность — агента, отряд, навык или flow.
        // Файлов оно не трогает, поэтому и откат тут другой: удалить в Гильдии.
        return 'Согласие создаст запись в Гильдии — её можно удалить там же. Файлы не меняются.'
      case 'egress':
        return 'Разрешение откроет указанный сетевой адрес для этого квеста. Уже выполненный внешний запрос отменить нельзя.'
      case 'supervision':
        return 'Продолжение возобновит работу с текущего checkpoint; остановка сохранит доступный частичный результат изолированно.'
      default:
        return 'Решение записывается в журнал изменений.'
    }
  }

  function decisionRiskLabel(risk) {
    return { LOW: 'низкий', MEDIUM: 'средний', HIGH: 'высокий', CRITICAL: 'критический' }[risk] || String(risk || '').toLowerCase()
  }

  function decisionIsHot(item) {
    return item.risk === 'HIGH' || item.risk === 'CRITICAL'
  }

  function currentDecision() {
    const items = ui.decisionsData?.items || []
    if (items.length === 0) return undefined
    return items.find(item => item.id === ui.decisionPick) || items[0]
  }

  function decisionQueueItemHtml(item, active) {
    return `<button class="hall-queue-item ${active ? 'is-active' : ''}" data-action="pick-decision" data-id="${esc(item.id)}"${active ? ' aria-current="true"' : ''}>
      <div class="hall-spread">
        <span class="hall-chip ${decisionIsHot(item) ? 'is-hot' : ''}">${esc(sentenceLabel(item.label))}</span>
        <span class="hall-wait ${item.blocking ? 'is-blocking' : ''}">${decisionWaitLabel(item.waitingMs)}</span>
      </div>
      <span class="title">${esc(item.title || item.detail || item.id)}</span>
      <div class="hall-spread">
        <span class="who">${esc(item.who || item.runId || '')}</span>
        <span class="hall-chip ${decisionIsHot(item) ? 'is-hot' : 'is-dim'}">${esc(decisionRiskLabel(item.risk))}</span>
      </div>
    </button>`
  }

  // Что уйдёт в ядро при согласии и при отказе.
  //
  // Для наборов изменений отклонение — отдельный путь, для остальных — значение
  // того же поля. Ядро уже сказало, что именно; клиент только передаёт дальше.
  // Кнопки и клавиши берут это из одного места намеренно: две копии одного
  // правила однажды разойдутся, и «A» сделает не то, что кнопка «ПРИНЯТЬ».
  // Как решать элемент очереди: куда слать, чем назвать поля и что в них класть.
  //
  // Код действия и значение поля разведены намеренно. Кодом («approve», «start»)
  // интерфейс подписывает кнопку, а маршрут ждёт в теле своё: одному нужно
  // булево, другому — тот же код. Пока это было одним значением, тело собиралось
  // наугад и ядро отвергало его целиком.
  function decisionIntents(item) {
    const resolve = item?.resolve || {}
    const path = resolve.path || ''
    const field = resolve.field || ''
    const idField = resolve.idField || ''
    const reject = resolve.reject || ''
    const rejectIsPath = Boolean(reject) && reject.startsWith('/api/')
    const acceptValue = resolve.acceptValue === undefined ? (resolve.accept || '') : resolve.acceptValue
    const rejectValue = resolve.rejectValue === undefined ? reject : resolve.rejectValue
    return {
      accept: { path, field, idField, code: resolve.accept || '', value: acceptValue },
      reject: rejectIsPath
        // Отказ отдельным маршрутом: тело ему не нужно вовсе.
        ? { path: reject, field: '', idField: '', code: 'reject', value: '' }
        : { path, field, idField, code: reject, value: rejectValue },
    }
  }

  // Отправка решения в ядро. Без пути отправлять нечего: ядро не назвало способ,
  // и кнопка в таком случае нарисована выключенной.
  function sendDecisionResolve(id, intent) {
    if (!intent?.path) return false
    // Пока предыдущее решение в пути, второе не отправляем. Это и двойной щелчок
    // по кнопке, и очередь повторов от удержанной клавиши: ответ может прийти
    // посреди серии, список сместится, и оставшиеся нажатия разрешат уже другое
    // решение — то самое действие, которое выполняется сразу и не отменяется.
    if (ui.decisionsStatus === 'loading') return false
    const proposal = taskProposalById(id)
    if (intent.path === '/api/quest-proposals/decide' && intent.value === 'start' && proposal?.brief) {
      if (ui.proposalEditId === id || ui.proposalStarting.has(id) || ui.proposalModifying.has(id) || !['ready', 'approved'].includes(proposal.brief.state) || proposal.brief.openQuestions?.length) return false
      ui.proposalStarting.add(id)
      vscode.postMessage({
        type: 'decideQuestProposal',
        proposalId: id,
        action: 'start',
        startFlow: true,
        origin: ui.state.selectedTab === 'master' ? 'master' : '',
        expectedVersion: proposal.brief.version,
        approveVersion: proposal.brief.mode === 'project' ? proposal.brief.version : undefined,
      })
      render()
      return true
    }
    ui.decisionsStatus = 'loading'
    vscode.postMessage({
      type: 'resolveDecision', id: String(id || ''),
      path: intent.path, field: intent.field || '', idField: intent.idField || '',
      // Значение уходит как есть: часть маршрутов ждёт булево, и строка «true»
      // для них — чужой тип, а не согласие.
      value: intent.value,
    })
    render()
    return true
  }

  function decisionDetailHtml(item) {
    if (!item) {
      // «Пусто» и «не удалось загрузить» — разные вещи, и путать их нельзя:
      // после отказа панель уверяла, что никто не ждёт решения, и тут же
      // добавляла, что это не отсутствие данных, — рядом с сообщением об отказе.
      if (ui.decisionsStatus === 'error') {
        return `<div class="hall-page"><div class="hall-title"><span class="kicker">Очередь не загружена</span><h1>Неизвестно, кто ждёт решения</h1></div>
        <p class="hall-note">Запрос к ядру не удался: ${esc(ui.decisionsError)}. Здесь не пусто — здесь неизвестно. Нажмите «Повторить», чтобы спросить ядро снова.</p></div>`
      }
      // Тот же водораздел, что и у отказа, но для ещё не пришедшего ответа: пока
      // очередь грузится, ответа нет ни «пусто», ни «занято». Шапка списка честно
      // писала «загрузка…», а панель рядом в ту же секунду уверяла, что никто не
      // ждёт решения, — и это было первое, что видел человек, пришедший сюда по
      // тревоге «2 ждёт вас» с Обзора.
      if (ui.decisionsStatus === 'loading' || ui.decisionsStatus === 'idle') {
        return `<div class="hall-page"><div class="hall-title"><span class="kicker">Очередь загружается</span><h1>Спрашиваю ядро</h1></div>
        <p class="hall-note">Пока ответ не пришёл, неизвестно, кто ждёт решения. Список появится здесь сам.</p></div>`
      }
      return `<div class="hall-page"><div class="hall-title"><span class="kicker">Очередь пуста</span><h1>Никто не ждёт решения</h1></div>
        <p class="hall-note">Пока агенты не упираются в подтверждение, здесь пусто. Это нормальное состояние, а не отсутствие данных.</p></div>`
    }
    if (item.brief) return masterProposalHtml(taskProposalById(item.id) || item)
    const intents = decisionIntents(item)
    return `<div class="hall-detail">
      <div class="hall-detail-body">
        <div class="hall-title">
          <div class="hall-item">
            <span class="hall-chip ${decisionIsHot(item) ? 'is-hot' : ''}">${esc(sentenceLabel(item.label))}</span>
            <span class="hall-wait ${item.blocking ? 'is-blocking' : ''}">ждёт ${decisionWaitLabel(item.waitingMs)}${item.blocking ? ' · агент простаивает' : ''}</span>
          </div>
          <h1>${esc(item.title || item.id)}</h1>
        </div>
        <div class="hall-triad">
          <dl class="hall-facet"><dt>Что просят</dt><dd>${esc(item.detail || item.title || '—')}</dd></dl>
          <dl class="hall-facet is-risk"><dt>Чем рискует</dt><dd>${esc(decisionRiskLabel(item.risk))}${item.blocking ? ' · пока решение не принято, работа стоит' : ''}</dd></dl>
          <dl class="hall-facet"><dt>Откуда</dt><dd>${esc(item.who || item.runId || item.flowRunId || '—')}</dd></dl>
        </div>
      </div>
      <div class="hall-verdict">
        ${!item.resolve?.path ? `<div class="hall-verdict-dead">Это решение нельзя принять отсюда: ядро не назвало способ. Откройте раздел, к которому оно относится.</div>` : ''}
        <button class="hall-btn is-primary" ${intents.accept.path ? '' : 'disabled'} data-action="resolve-decision" data-id="${esc(item.id)}" data-intent="accept">${esc(decisionVerb(intents.accept.code, 'Принять'))} · A</button>
        <button class="hall-btn" ${intents.reject.path ? '' : 'disabled'} data-action="resolve-decision" data-id="${esc(item.id)}" data-intent="reject">${esc(decisionVerb(intents.reject.code, 'Отклонить'))} · R</button>
        <small>${esc(decisionReversibilityNote(item.kind))}</small>
        <span class="hall-verdict-keys"><kbd>J</kbd><kbd>K</kbd><kbd>↑</kbd><kbd>↓</kbd><em>перебор</em></span>
      </div>
    </div>`
  }

  function decisionsView() {
    if (ui.decisionsStatus === 'idle') {
      ui.decisionsStatus = 'loading'
      setTimeout(() => vscode.postMessage({ type: 'loadDecisions' }), 0)
    }
    const items = ui.decisionsData?.items || []
    const active = currentDecision()
    return shell(`<div class="hall-split">
      <div class="hall-queue">
        <header>
          <b>Очередь · ${items.length}</b>
          <small>${ui.decisionsStatus === 'loading' ? 'загрузка…' : ui.decisionsStatus === 'error' ? 'не удалось' : 'по времени ожидания'}</small>
        </header>
        ${ui.decisionsStatus === 'error' ? `<div class="hall-strip">
          <span>Очередь не обновилась: ${esc(ui.decisionsError)}</span>
          <button class="hall-btn" data-action="retry-decisions">Повторить</button>
        </div>` : ''}
        <div class="hall-queue-list" id="decision-queue" data-keynav="column" aria-label="Очередь решений">
          ${items.map(item => decisionQueueItemHtml(item, active && item.id === active.id)).join('')}
        </div>
      </div>
      ${decisionDetailHtml(active)}
    </div>`)
  }

  // Клавиши очереди решений: J K перебор, A принять, R отклонить.
  //
  // Эта подсказка висела над очередью, а кнопки были подписаны «· A» и «· R» —
  // но ни одна из клавиш не была подключена: обещание печаталось трижды и не
  // выполнялось ни разу.
  //
  // Смотрим на event.code, а не на event.key: на кириллической раскладке та же
  // физическая клавиша даёт «о», «л», «ф», «к», и по букве подсказка не работала
  // бы ровно у тех, для кого написан интерфейс.
  function decisionHotkey(event) {
    if (ui.state.selectedTab !== 'decisions') return ''
    if (event.ctrlKey || event.metaKey || event.altKey) return ''
    // Набор текста важнее горячих клавиш: случайное «A» в поле ввода означало бы
    // разрешение, а разрешённое действие выполняется сразу и не отменяется.
    const target = event.target
    const tag = String(target?.tagName || '').toLowerCase()
    if (tag === 'input' || tag === 'textarea' || tag === 'select' || target?.isContentEditable) return ''
    // Стрелки делают то же, что J и K: на этом экране перебор — одно действие, и
    // два разных поведения на одном списке читались бы как поломка.
    if (event.key === 'ArrowDown') return 'next'
    if (event.key === 'ArrowUp') return 'prev'
    return ({ KeyJ: 'next', KeyK: 'prev', KeyA: 'accept', KeyR: 'reject' })[String(event.code || '')] || ''
  }

  return { decisionsView, decisionQueueItemHtml, decisionDetailHtml, decisionIntents, sendDecisionResolve, decisionHotkey }
}
