// Снимок интерфейса до перерисовки и возврат после неё.
//
// render() пересобирает разметку целиком, и без снимка каждая перерисовка
// роняла бы фокус, каретку и прокрутку: поле теряло бы набранное место, лента
// Мастера прыгала к первой реплике, перебор списка работал бы ровно один раз.
// Снимок берёт то, что человек видит и держит под рукой, возврат ставит это
// обратно в новую разметку.
//
// Состояние живёт в main.js и приходит мешком ui; модулю нужны только корень,
// клиент Мастера и отрисовщики, которые меряют ленту после вставки разметки.

import { threadNearBottom } from './master-feed.js'

export function createUiSnapshot({ root, ui, masterClient, applyMasterComposeReserve, applyFlowNodePlacement, updateCompanionScrollCue, afterMasterFeedPaint }) {
  function captureUi() {
    const active = document.activeElement
    const focus = active && root.contains(active) && active.id
      ? { id: active.id, start: active.selectionStart, end: active.selectionEnd }
      : undefined
    // У кнопок списка нет id, и по снимку выше они не восстанавливаются. Список
    // опознаётся своей подписью, элемент — позицией: после перерисовки вернуть
    // нужно тот же по счёту, а если разметка сменилась — текущий выбранный.
    const activeList = !focus && active && root.contains(active) ? active.closest?.('[data-keynav]') : undefined
    const keynav = activeList ? {
      label: activeList.getAttribute('aria-label') || '',
      at: [...activeList.querySelectorAll('button')].indexOf(active.closest('button')),
    } : undefined
    const companionThread = root.querySelector('#companion-thread')
    const masterThread = root.querySelector('#master-thread')
    return {
      focus,
      keynav,
      chatMain: root.querySelector('.chat-main')?.scrollTop ?? 0,
      conversation: root.querySelector('.conversation')?.scrollTop ?? 0,
      commandCenter: root.querySelector('.command-center')?.scrollTop ?? 0,
      setupContent: root.querySelector('.companion-setup-content')?.scrollTop ?? 0,
      onboarding: root.querySelector('.onboarding')?.scrollTop ?? 0,
      // Дерево изменений перерисовывается от каждой правки в редакторе: без
      // переноса прокрутки список прыгал бы к началу под рукой.
      gitWorkspace: root.querySelector('.git-workspace')?.scrollTop ?? 0,
      gitWorkspaceRepo: root.querySelector('.git-workspace')?.dataset.gwRepo,
      gitTree: root.querySelector('.point-git-tree')?.scrollTop ?? 0,
      companionThread: companionThread ? {
        top: companionThread.scrollTop,
        follow: ui.companionAutoFollow || threadNearBottom(companionThread),
      } : undefined,
      masterThread: masterThread ? {
        top: masterThread.scrollTop,
        follow: ui.masterAutoFollow || threadNearBottom(masterThread),
      } : undefined,
    }
  }

  function restoreUi(snapshot) {
    // Запас ленты под плавающей карточкой меряется здесь, а не только на нажатии
    // клавиши: на первой отрисовке раздела нажатий ещё не было, и лента осталась
    // бы с запасным числом, а карточка с уточнениями закрыла бы хвост разговора.
    // Стоит до выхода по пустому снимку — снимка нет как раз при первом открытии.
    applyMasterComposeReserve()
    // Места узлов графа флоу — оттуда же и по той же причине: холст пересобирает
    // разметку на каждой отрисовке, а координаты в ней лежат атрибутами, потому
    // что CSP вебвью не пропускает инлайновый стиль. Разделов без холста это
    // стоит одного querySelector.
    applyFlowNodePlacement()
    if (!snapshot) return
    const chatScreen=root.querySelector('.is-chat')
    if(chatScreen){chatScreen.classList.toggle('is-chats-hidden',!!masterClient.historyHidden);chatScreen.classList.toggle('is-chats-open',!!masterClient.historyOpen)}
    const dialogue=root.querySelector('.hall-dialogue')
    // Панель задания: класс раздела повторяется после отрисовки по той же
    // причине, что и рейка разговоров, — разметку собирает не она одна.
    if(dialogue)dialogue.classList.toggle('is-brief-open',ui.masterBriefPanelOpen&&!!root.querySelector('#master-brief-panel'))
    const chatMain = root.querySelector('.chat-main')
    if (chatMain && snapshot.chatMain != null) chatMain.scrollTop = snapshot.chatMain
    const conversation = root.querySelector('.conversation')
    if (conversation && snapshot.conversation != null) conversation.scrollTop = snapshot.conversation
    const commandCenter = root.querySelector('.command-center')
    if (commandCenter && snapshot.commandCenter != null) commandCenter.scrollTop = snapshot.commandCenter
    const setupContent = root.querySelector('.companion-setup-content')
    if (setupContent && snapshot.setupContent != null) setupContent.scrollTop = snapshot.setupContent
    const onboarding = root.querySelector('.onboarding')
    if (onboarding && snapshot.onboarding != null) onboarding.scrollTop = snapshot.onboarding
    const gitWorkspace = root.querySelector('.git-workspace')
    if (gitWorkspace) gitWorkspace.scrollTop = snapshot.gitWorkspaceRepo===gitWorkspace.dataset.gwRepo ? snapshot.gitWorkspace : Number(gitWorkspace.dataset.gwScroll)||0
    const gitTree = root.querySelector('.point-git-tree')
    if (gitTree && snapshot.gitTree != null) gitTree.scrollTop = snapshot.gitTree
    const companionThread = root.querySelector('#companion-thread')
    if (companionThread && snapshot.companionThread) {
      ui.companionAutoFollow = Boolean(snapshot.companionThread.follow)
      companionThread.scrollTop = ui.companionAutoFollow ? companionThread.scrollHeight : snapshot.companionThread.top
      updateCompanionScrollCue()
    }
    // Лента Мастера пересобирается целиком на каждой отрисовке, а отрисовку
    // вызывает и чужое состояние — обновление очереди решений, ответ ядра.
    // Без переноса прокрутки разговор после каждой такой перерисовки прыгал к
    // самой первой реплике: свежий ответ и «Думает…» оказывались за экраном.
    const masterThread = root.querySelector('#master-thread')
    if (masterThread) {
      if(masterClient.restoreScroll!=null){snapshot.masterThread={top:masterClient.restoreScroll,follow:!Number.isFinite(masterClient.restoreScroll)};delete masterClient.restoreScroll}
      // Снимка нет — раздел только что открыли. Разговор показывается с конца,
      // как его и оставили, а не с начала переписки.
      const follow = snapshot.masterThread ? Boolean(snapshot.masterThread.follow) : true
      ui.masterAutoFollow = follow
      masterThread.scrollTop = follow ? masterThread.scrollHeight : snapshot.masterThread.top
      afterMasterFeedPaint()
    }
    if (snapshot.focus?.id) {
      const el = root.querySelector(`#${CSS.escape(snapshot.focus.id)}`)
      if (el && typeof el.focus === 'function') {
        // Возврат фокуса после отрисовки возвращает каретку, а не показывает
        // элемент: он и так был на экране. Обычный focus() при этом прокручивал
        // ближайшего предка с `overflow: hidden`, и в боковой панели помощника
        // разговор уезжал вверх на каждой перерисовке — то есть на каждой реплике.
        el.focus({ preventScroll: true })
        if (typeof snapshot.focus.start === 'number' && typeof el.setSelectionRange === 'function') {
          try { el.setSelectionRange(snapshot.focus.start, snapshot.focus.end ?? snapshot.focus.start) } catch {}
        }
      }
    } else if (snapshot.keynav) {
      // Без этого перебор списка работал ровно один раз. Клавиша меняет выбор,
      // выбор вызывает полную отрисовку, отрисовка уничтожает элемент с фокусом —
      // и следующая клавиша приходит в body мимо обработчика на root. Проверено
      // в браузере: так же были сломаны J и K, обещанные подсказкой на экране.
      const list = [...root.querySelectorAll('[data-keynav]')]
        .find(node => (node.getAttribute('aria-label') || '') === snapshot.keynav.label)
      if (list) {
        const items = [...list.querySelectorAll('button')]
        const target = list.querySelector('[aria-current="true"]')
          || items[Math.min(Math.max(snapshot.keynav.at, 0), items.length - 1)]
        if (target && typeof target.focus === 'function') target.focus()
      }
    }
    // Последним — иначе восстановление курсора по снимку вернуло бы каретку туда,
    // где она стояла в прежнем, ещё пустом поле.
    if (ui.masterCaretToEnd) {
      ui.masterCaretToEnd = false
      const field = root.querySelector('#master-input')
      if (field) {
        // Та же причина, что у поля помощника: фокус не должен двигать ленту.
        field.focus({ preventScroll: true })
        try { field.setSelectionRange(field.value.length, field.value.length) } catch {}
      }
    }
  }

  return { captureUi, restoreUi }
}
