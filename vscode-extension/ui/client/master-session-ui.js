import { masterSessionHtml as masterSessionViewHtml } from './master-session-views.js'
export { masterComposerHtml } from './master-session-views.js'

// Какое меню разговора открыто. Полная отрисовка Чертога (опрос состояния
// во время работы агентов) пересобирает шапку, и меню, открытое только
// в DOM, закрывалось бы под рукой посреди выбора.
let openSessionPanel = ''
export function masterSessionHtml(sessions, esc, developmentHtml = '') {
  return masterSessionViewHtml(sessions, esc, openSessionPanel, developmentHtml)
}

// Поповер встаёт под кнопкой «•••»: её место в шапке зависит от жетонов
// справа, поэтому отступ меряется при открытии и отдаётся переменной через
// CSSOM (атрибут style вырезает CSP вебвью).
function placeSessionPanel(root, panel) {
  const dialogue = root.querySelector('.hall-dialogue')
  const button = root.querySelector('[data-action="master-session-toggle"][data-panel="history"]')
  if (!dialogue || !button || typeof button.getBoundingClientRect !== 'function') return
  const right = Math.round(dialogue.getBoundingClientRect().right - button.getBoundingClientRect().right)
  panel.style?.setProperty?.('--hall-pop-right', Math.max(8, right) + 'px')
}

function closeSessionPanels(root) {
  openSessionPanel = ''
  root.querySelectorAll('[data-session-panel]').forEach(panel => { panel.hidden = true })
  root.querySelector('[data-action="master-session-toggle"][data-panel="history"]')?.setAttribute('aria-expanded', 'false')
}

// Меню закрывается как меню: щелчок мимо него и Esc. Enter в поле названия
// сохраняет название — кнопка-карандаш остаётся для мыши.
let dismissBound = false
function bindSessionDismiss(root) {
  if (dismissBound || typeof document === 'undefined') return
  dismissBound = true
  document.addEventListener('pointerdown', event => {
    if (!openSessionPanel) return
    const inside = event.target?.closest?.('[data-session-panel], [data-action="master-session-toggle"]')
    if (!inside) closeSessionPanels(root)
  }, true)
  document.addEventListener('keydown', event => {
    if (!openSessionPanel) return
    if (event.key === 'Escape') {
      closeSessionPanels(root)
      root.querySelector('[data-action="master-session-toggle"][data-panel="history"]')?.focus?.()
    } else if (event.key === 'Enter' && event.target?.matches?.('[data-master-session-title]')) {
      event.preventDefault()
      root.querySelector('[data-action="master-session-rename"]')?.click?.()
    }
  }, true)
}
import { masterAnswerNote, masterAnswerProgress } from './master-questions-views.js'

function flushMasterQuestionDraft(question, drafts) {
  if (!question || !drafts) return
  const key = question.dataset.questionKey
  if (!key) return
  drafts[key] = {
    text: question.querySelector('.hall-question-extra')?.value || '',
    selected: [...question.querySelectorAll('.hall-option.is-on')].map(btn => btn.dataset.option),
  }
}

// Счётчик у кнопки переписывается на каждом нажатии клавиши, а не отрисовкой:
// замена разметки слота стоила бы каретки в поле ответа.
export function patchMasterAnswerNote(root, drafts) {
  const group = root.querySelector('.hall-compose .hall-questions.is-inline')
  if (!group) return
  group.querySelectorAll('.hall-question').forEach(question => flushMasterQuestionDraft(question, drafts))
  let pack = []
  try { pack = JSON.parse(group.dataset.pack || '[]') } catch { pack = [] }
  const progress = masterAnswerProgress(pack, group.dataset.owner || 'live', drafts)
  const note = group.querySelector('#master-answer-note')
  if (note) note.textContent = masterAnswerNote(progress)
  // Кнопку здесь трогать нечего: `aria-disabled` с неё снят — заперта она не
  // бывает ни в одном состоянии пакета, и на каждом знаке переписывается только
  // причина под рядом.
}

export function handleMasterSessionAction({action, target, root, vscode, sending, send, render, persist, drafts, cursor}) {
  if (action === 'master-question-next' || action === 'master-question-prev') {
    if (sending) return true
    const group = target.closest('.hall-questions')
    if (!group) return true
    const answers = drafts?.() || {}
    flushMasterQuestionDraft(group.querySelector('.hall-question'), answers)
    const owner = group.dataset.owner || 'live'
    const total = Number(group.dataset.total || 1)
    const at = Number(group.dataset.cursor || 0)
    const step = action === 'master-question-next' ? 1 : -1
    const places = cursor?.()
    if (places) places[owner] = Math.max(0, Math.min(total - 1, at + step))
    persist?.()
    render()
    // Отрисовка забирает фокус вместе со старой кнопкой. Без возврата
    // перелистывание с клавиатуры высаживает курсор в начало карточки.
    const shown = root.querySelector('.hall-compose .hall-questions.is-inline .hall-question')
    ;(shown?.querySelector('.hall-option') || shown?.querySelector('.hall-question-extra'))?.focus()
    return true
  }
  if (action === 'master-pick-option') {
    if (sending) return true
    const question = target.closest('.hall-question')
    if (!question) return true
    const multiple = question.dataset.multiple === '1'
    if (!multiple) {
      question.querySelectorAll('.hall-option').forEach(btn => {
        const on = btn === target
        btn.classList.toggle('is-on', on)
        btn.setAttribute('aria-pressed', on ? 'true' : 'false')
      })
    } else {
      const on = target.getAttribute('aria-pressed') !== 'true'
      target.classList.toggle('is-on', on)
      target.setAttribute('aria-pressed', on ? 'true' : 'false')
    }
    question.dispatchEvent(new Event('input', { bubbles: true }))
    return true
  }
  if (action === 'master-answer-question') {
    if (!sending) {
      const group = target.closest('.hall-questions')
      if (!group) return true
      const answers = drafts?.() || {}
      // На экране один вопрос пакета; ответы на остальные уже сняты при
      // перелистывании и лежат в черновиках. Снимаем видимый — обработчик
      // ввода доходит до него не всегда.
      flushMasterQuestionDraft(group.querySelector('.hall-question'), answers)
      let pack = []
      try { pack = JSON.parse(group.dataset.pack || '[]') } catch { pack = [] }
      if (!pack.length) return true
      const { lines, answered, missing } = masterAnswerProgress(pack, group.dataset.owner || 'live', answers)
      if (!answered) {
        // Отправлять нечего. Идём к первому незаполненному вопросу — а он в
        // пакете по одному может быть и не тем, что сейчас на экране, поэтому
        // сначала переставляем закладку. Причина уже написана рядом с кнопкой.
        const owner = group.dataset.owner || 'live'
        const at = Number(group.dataset.cursor || 0)
        const want = Math.max(0, missing)
        if (want !== at) {
          const places = cursor?.()
          if (places) places[owner] = want
          render()
          return true
        }
        const question = group.querySelector('.hall-question')
        ;(question?.querySelector('.hall-option') || question?.querySelector('.hall-question-extra'))?.focus()
        return true
      }
      // Пустые блоки выброшены, а не отправлены пустыми: masterParseAnswers
      // отказывается от реплики целиком, если хоть один блок не разошёлся, —
      // и один пустой блок увёл бы весь обмен из ленты в сырой текст отправки.
      // Неотвеченное вернётся из ядра в «Нужно уточнить» и задержит запуск.
      persist?.()
      send(lines.filter(Boolean).join('\n\n'))
    }
    return true
  }
  if (!action?.startsWith('master-session-')) return false
  // Пока идёт ход, действия над разговором молчат — но выбор режима и
  // подробности ответа относится к следующей реплике, а не к текущей.
  // Запертым он делал меню, которое открывается и показывает четыре мёртвых
  // пункта: хуже и запрета, и разрешения.
  if (sending && !['master-session-select','master-session-new','master-session-toggle','master-session-sidebar','master-session-workMode','master-session-mode'].includes(action)) return true
  const kind = action.slice('master-session-'.length)
  // Действие из меню его закрывает: переименование, закрепление, экспорт
  // отвечают новым состоянием, и открытое меню поверх него только мешает.
  // Подробность ответа, автозапуск и память меняют само меню — оно остаётся.
  if (kind !== 'toggle' && !['mode', 'auto-read-only', 'memory-save', 'memory-delete', 'memory-replace'].includes(kind)) closeSessionPanels(root)
  if(kind==='memory-replace'){vscode.postMessage({type:'masterSession',action:kind,id:target.dataset.id,value:target.closest('.hall-memory-entry').querySelector('select').value});return true}
  if(kind==='branch'){vscode.postMessage({type:'offerMasterChatBranch',conversationId:target.dataset.id});return true}
  if(kind==='sidebar'){const screen=root.querySelector('.is-chat');if(!screen)return true;screen.classList.toggle('is-chats-hidden');screen.classList.toggle('is-chats-open');vscode.postMessage({type:'masterViewPreferences',hidden:screen.classList.contains('is-chats-hidden'),open:screen.classList.contains('is-chats-open')});return true}
  if(kind==='export'||kind==='delete'||kind==='model'){vscode.postMessage({type:kind==='export'?'exportMasterConversation':kind==='delete'?'deleteMasterConversation':'pickMasterModel',conversationId:target.dataset.id});return true}
  if(kind==='memory-save'){const area=target.closest('.hall-memory-entry')?.querySelector('textarea') || root.querySelector('[data-master-memory]');vscode.postMessage({type:'masterSession',action:'memory-save',id:target.dataset.id || '',value:area.value});return true}

  if (kind === 'toggle') {
    const panel = root.querySelector(`[data-session-panel="${target.dataset.panel}"]`)
    if (!panel) return true
    // Панели больше не открываются вдвоём: «Память» открывается из меню «•••»
    // на его месте, а «назад» возвращает к меню.
    const opening = panel.hidden
    root.querySelectorAll('[data-session-panel]').forEach(other => { other.hidden = true })
    panel.hidden = !opening
    openSessionPanel = opening ? target.dataset.panel : ''
    root.querySelector('[data-action="master-session-toggle"][data-panel="history"]')?.setAttribute('aria-expanded', String(Boolean(openSessionPanel)))
    if (opening) {
      placeSessionPanel(root, panel)
      bindSessionDismiss(root)
      panel.querySelector(target.dataset.panel === 'memory' ? 'textarea' : 'input')?.focus?.()
    }
    return true
  }
  const value = kind === 'memory' ? root.querySelector('[data-master-memory]').value : kind === 'rename' ? root.querySelector('[data-master-session-title]').value : target.dataset.value
  vscode.postMessage({type: 'masterSession', action: kind, id: target.dataset.id, value})
  return true
}

