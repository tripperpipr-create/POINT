// Лента компаньона: реплика, поток сообщений, баннер подсказки и сводка для
// боковой панели.
//
// Разметка и ничего кроме. Изменяемое состояние приходит объектом live и
// читается на каждый кадр: деструктуризация сняла бы снимок на момент сборки
// модуля, и лента застыла бы на первом сообщении. Так же устроена лента
// Мастера — держать их по разные стороны границы было бы случайностью.

import { companionSpendCaveats } from './companion-compose.js'

export function createCompanionThreadViews({ live, COMPANION_PRESETS, companionBubbleBodyHtml, companionGettingStartedHtml, companionIdeNowHtml, companionInterventionActionHtml, companionInterventionFileHtml, companionInterventionProbeHtml, companionQuestionsHtml, companionThinkingLabel, data, esc, root, scrollCompanionThread, threadNearBottom }) {
  function companionBubbleHtml(item, messageIndex = -1) {
    const role = item.role === 'user' ? 'user' : 'assistant'
    const streaming = Boolean(item.streaming || item.mode === 'streaming')
    const who = role === 'user' ? 'Вы' : 'Помощник Point'
    // Уровень ответа модель ставит сама, и «critical» до сих пор не значил ничего:
    // ни рамки, ни подписи — критичное предупреждение выглядело как обычный совет
    // и читалось по диагонали вместе с остальными.
    const critical = item.role === 'assistant' && item.level === 'critical'
    const extra = critical
      ? ' warning critical'
      : item.level === 'warning' || item.mode === 'error' || item.mode === 'cancelled' ? ' warning' : ''
    const levelMark = critical ? 'критично' : item.role === 'assistant' && item.level === 'warning' ? 'важно' : ''
    const streamClass = streaming ? ' streaming' : ''
    // Кнопки под отказом ведут туда, где чинят именно эту причину. Ядро,
    // остановленное или упавшее, настройкой модели не лечится: раньше человеку
    // предлагали открыть настройку и повторить, и оба действия были бесполезны.
    // Причина отказа отбирается отдельно от текста: в сохранённом куске ответа
    // слово «ядро» встречается запросто, и по всему пузырю человека уводило бы
    // запускать ядро там, где не ответила модель.
    const failure = item.mode === 'error' ? String(item.failure || item.content || '') : ''
    const locked = /разреши(те)? доступ|безопасн(ый|ом) режим/i.test(failure)
    const coreDown = !locked && /ядро/i.test(failure)
    // Закрытая папка не лечится ни настройкой модели, ни запуском ядра: пока
    // доступа нет, ядру нечего читать. Проверяется раньше ядра — иначе слово
    // «ядро» в тексте про безопасный режим увело бы человека не туда.
    const repair = locked
      ? `<button type="button" class="primary" data-action="manage-trust">Настроить доступ</button>`
      : coreDown
        ? `<button type="button" class="secondary" data-action="show-output">Журнал ядра</button><button type="button" class="primary" data-action="start-server">Запустить ядро</button>`
        : `<button type="button" class="secondary" data-action="open-companion-setup">Настроить модель</button><button type="button" class="primary" data-action="companion-retry-last">Повторить</button>`
    // Остановленный вопрос человек чаще всего хочет задать снова — но только если
    // остановил его сам. Отмена новым сообщением значит, что он уже спросил
    // другое, и звать его назад незачем.
    const stopped = item.mode === 'cancelled' && !item.superseded
    // Ответ не от модели бывает двух видов: вынужденный — модель не ответила, и
    // выбранный — модель не подключена вовсе. Метка одна на оба, потому что
    // человеку важно одно: слова не модельные. Причина есть только у первого, и
    // кнопки починки идут тоже только с ней: подключённый локальный режим чинить
    // нечего, он работает как задумано.
    const fellBack = role === 'assistant' && Boolean(String(item.fallbackReason || '').trim())
    const withoutModel = role === 'assistant' && item.mode === 'deterministic'
    // Оборванный ответ дописывается с места обрыва, а не задаётся заново: вопрос
    // тот же, разобранная половина уже в истории, и переспрашивать целиком значит
    // платить за неё второй раз.
    const cutReply = (item.factsUsed || []).some(fact => String(fact || '') === 'replyTruncated=true')
    const recovery = item.mode === 'error'
      ? `<footer class="companion-msg-recovery">${repair}</footer>`
      : stopped
        ? `<footer class="companion-msg-recovery"><button type="button" class="primary" data-action="companion-retry-last">Спросить снова</button></footer>`
        : fellBack && !streaming
          ? `<footer class="companion-msg-recovery"><button type="button" class="secondary" data-action="open-companion-setup">Настроить модель</button><button type="button" class="primary" data-action="companion-retry-last">Повторить</button></footer>`
          : cutReply && !streaming
            ? `<footer class="companion-msg-recovery"><button type="button" class="primary" data-action="companion-continue">Продолжить</button></footer>`
            : ''
    // Отказ инструмента виден в самом ответе, а не только в «Сведениях»: он
    // объясняет, почему ответ беднее обычного, и открывать ради этого отдельное
    // окно человек не станет.
    const factValue = key => {
      const found = (item.factsUsed || []).find(fact => String(fact || '').startsWith(`${key}=`))
      return found ? String(found).slice(key.length + 1).split(',').filter(Boolean).join(', ') : ''
    }
    const failedTools = factValue('toolFailures')
    // Обрезанная выдача — не отказ, но ответ по ней тоже неполон: у большого
    // файла или длинной истории модель видела только начало.
    const partialTools = factValue('toolsTruncated')
    // Предел зовётся числом: «оборван на пределе» без числа не говорит, где его
    // подвинуть, а поле стоит в настройке под «Дополнительно».
    const replyLimit = factValue('replyLimitTokens')
    // Пределы и цена показываются прямо под ответом, когда их сообщил сам
    // исполнитель: у подписки это единственное, что видно про её расход.
    const modelWindow = factValue('modelContextWindow')
    const modelCeiling = factValue('modelMaxOutput')
    const replyCost = factValue('replyCostMicroUsd')
    const cacheSplit = factValue('cacheReadTokens')
    // Ответ, обрезанный потолком длины, кончается на полуслове. Без оговорки
    // оборванная фраза читается как законченная мысль, и человек уходит с
    // половиной разбора, считая его целым.
    const caveats = [
      failedTools ? `Не удалось посмотреть: ${failedTools}` : '',
      partialTools ? `Прочитано частично: ${partialTools}` : '',
      cutReply ? `Ответ оборван на пределе длины${replyLimit ? ` (${replyLimit} токенов)` : ''}` : '',
      ...companionSpendCaveats({ modelWindow, modelCeiling, replyCost, cacheSplit }),
    ].filter(Boolean)
    const blindSpotHtml = role === 'assistant' && caveats.length
      ? `<small class="companion-msg-blindspot">${esc(caveats.join(' · '))}</small>`
      : ''
    const mark = live.companionFeedbackMarks.get(String(item.id || ''))
    const markHtml = [
      withoutModel || fellBack
        ? `<small class="companion-msg-mark" title="${esc(String(item.fallbackReason || 'Модель не подключена — отвечает встроенный разбор Point'))}">Ответил движок Point</small>`
        : '',
      levelMark ? `<small class="companion-msg-level">${esc(levelMark)}</small>` : '',
      mark ? `<small class="companion-msg-mark">${mark === 'down' ? 'Отмечено: не помогло' : 'Отмечено: полезно'}</small>` : '',
    ].join('')
    // «Ответить иначе» просит другой путь, а у встроенного разбора путь один:
    // без модели кнопка вернула бы тот же текст слово в слово.
    const otherWay = withoutModel
      ? ''
      : `<button type="button" class="secondary" data-action="regenerate-companion-message" data-message-index="${messageIndex}" title="Тот же вопрос, но другим путём">Ответить иначе</button>`
    const actions = role === 'assistant' && !streaming
      ? `<footer class="companion-msg-actions"><details class="companion-message-menu"><summary title="Действия с ответом" aria-label="Действия с ответом">•••</summary><div>${otherWay}<button type="button" class="secondary" data-action="copy-companion-message">Копировать</button><button type="button" class="secondary${mark === 'up' ? ' is-on' : ''}" data-action="feedback-companion-message" data-value="up" data-message-id="${esc(item.id || '')}" data-message-index="${messageIndex}" title="Полезный ответ">Полезно</button><button type="button" class="secondary${mark === 'down' ? ' is-on' : ''}" data-action="feedback-companion-message" data-value="down" data-message-id="${esc(item.id || '')}" data-message-index="${messageIndex}" title="Следующий ответ пойдёт другим путём">Не помогло</button><button type="button" class="primary" data-action="open-companion-message-details" data-message-index="${messageIndex}">Сведения об ответе</button></div></details></footer>`
      : ''
    const body = role === 'user'
      ? `<p>${esc(item.content)}</p>`
      : companionBubbleBodyHtml(item.content, { streaming })
    return `<article class="companion-msg ${role}${extra}${streamClass}" aria-label="${who}"${streaming ? ' data-companion-stream="1"' : ''}><header><span class="companion-presence" aria-hidden="true"></span><small>${who}</small>${markHtml}</header>${body}${blindSpotHtml}${companionQuestionsHtml(item)}${recovery}${actions}</article>`
  }
  function companionStreamingBubbleHtml() {
    if (!live.companionLoading) return ''
    if (live.companionStreamReply) {
      return companionBubbleHtml({ role: 'assistant', content: live.companionStreamReply, mode: 'streaming', streaming: true })
    }
    return `<article class="companion-msg assistant thinking" aria-label="Помощник думает" data-companion-thinking="1"><header><span class="companion-presence" aria-hidden="true"></span><small>Помощник Point</small></header><p>${companionThinkingLabel()}</p></article>`
  }
  function companionThreadHtml() {
    const bubbles = live.companionMessages.map((item, index) => item?.mode === 'streaming' ? '' : companionBubbleHtml(item, index)).join('')
    const thinking = companionStreamingBubbleHtml()
    const empty = !bubbles && !thinking
      ? `<div class="companion-empty" id="companion-empty"><span aria-hidden="true">✦</span><strong>Чем помочь?</strong><p>Пишите обычными словами. Я вижу открытый файл и контекст IDE, объясняю код и готовлю агентов, квесты и отряды. Создание и запуск — только после вашего подтверждения.</p>${companionGettingStartedHtml()}</div>`
      : ''
    return `${bubbles}${thinking}${empty}`
  }
  // Строка ожидания живёт в «думающем» пузыре, а не отдельной полосой: полосу
  // пробовали, от неё остался только этот патч.
  function patchCompanionThinkingLabel() {
    const thinking = root.querySelector('[data-companion-thinking] > p')
    if (thinking) thinking.textContent = companionThinkingLabel()
  }
  function patchCompanionStreamingBubble() {
    const thread = root.querySelector('#companion-thread')
    if (!thread || !live.companionLoading) return
    const follow = live.companionAutoFollow || threadNearBottom(thread)
    const html = companionStreamingBubbleHtml()
    if (!html) return
    const wrap = document.createElement('div')
    wrap.innerHTML = html
    const fresh = wrap.firstElementChild
    if (!fresh) return
    const current = thread.querySelector('[data-companion-stream], [data-companion-thinking]')
    if (current) current.replaceWith(fresh)
    else {
      thread.querySelector('#companion-empty')?.remove()
      thread.appendChild(fresh)
    }
    if (follow) scrollCompanionThread(true)
  }
  function companionSpeakInterventions() {
    return (live.state.boot?.companionInterventions || []).filter(item => item?.level === 'critical' || item?.level === 'warning')
  }
  function companionNudgeBannerHtml() {
    const item = companionSpeakInterventions()[0]
    if (!item) return ''
    const probeVisible = live.companionInterventionProbe?.interventionId === item.id && live.companionInterventionProbe?.connectionId === item.relatedId
    return `<details class="companion-nudge-banner intervention-${esc(item.level)}"${probeVisible ? ' open' : ''}><summary><span aria-hidden="true">${item.level === 'critical' ? '!' : '•'}</span><strong>${esc(item.title)}</strong><small>Подробнее</small></summary><div><p>${esc(item.detail)}</p>${companionInterventionProbeHtml(item)}<footer>${companionInterventionActionHtml(item)}${companionInterventionFileHtml(item)}<button type="button" class="secondary" data-action="dismiss-companion-intervention" data-id="${esc(item.id)}" data-occurrence="${esc(item.occurrenceKey)}">Скрыть</button></footer></div></details>`
  }
  function companionSidebarBriefHtml() {
    const companion = live.state.boot?.companion || {}
    const preset = companion.preset || 'balanced'
    const presetMeta = COMPANION_PRESETS.find(item => item.id === preset)
    const interventions = companionSpeakInterventions()
    const connected = Boolean(companion.provider && companion.model)
    const speak = interventions.length
      ? `<button type="button" class="companion-dock-speak-count intervention-${esc(interventions[0].level)}" data-action="open-companion-sidebar">${interventions.length} сигнал${interventions.length === 1 ? '' : 'а'} →</button>`
      : `<p class="muted companion-dock-quiet">Тихо наблюдает IDE. Подскажет, только когда есть смысл.</p>`
    return `<main class="companion-dock-brief"><header><span class="companion-presence" aria-hidden="true"></span><div><strong>Компаньон</strong><small>${connected ? esc(presetMeta?.label || 'Модель подключена') : 'Локальный режим'}</small></div></header>${companionIdeNowHtml()}${speak}<div class="empty-next"><button type="button" class="primary" data-action="open-companion-sidebar">Открыть чат</button><button type="button" class="secondary" data-action="open-companion-setup">Настроить</button></div></main>`
  }

  // Обе точечные правки ленты зовёт разговор помощника: 18 сентября лента
  // уехала сюда вместе с ними, а места вызова остались в `main.js` — и
  // каждое событие потока роняло разбор сообщения на `ReferenceError`.
  return {
    companionBubbleHtml, companionThreadHtml, companionNudgeBannerHtml, companionSidebarBriefHtml,
    patchCompanionThinkingLabel, patchCompanionStreamingBubble,
  }
}
