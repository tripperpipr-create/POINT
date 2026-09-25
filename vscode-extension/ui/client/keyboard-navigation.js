// Перебор списков и полос вкладок с клавиатуры.
//
// До этого модуля клавиатурного маршрута по Хабу не было ничем гарантировано:
// на весь интерфейс приходился один обработчик keydown, ни одного tabindex, а
// очереди объявляли себя списками — роль listbox на контейнере и option на
// обычных кнопках. Скринридеру обещали список, вело себя это как набор кнопок,
// а стрелки не делали ничего. Ложное объявление снято — кнопки остались
// кнопками и живут в естественном порядке табуляции, — а стрелки добавлены
// здесь. Возврат объявления ловит ui/contracts.mjs по точному тексту атрибута,
// поэтому писать его здесь нельзя даже в комментарии.
//
// Контейнер помечается атрибутом data-keynav: "column" — вертикальный список
// (стрелки вверх/вниз), "row" — полоса (влево/вправо). Home и End прыгают к
// краям в обоих случаях.
//
// Фокус только переносится, но ничего не нажимает. Это осознанно: активация
// перерисовывает раздел целиком, а полная отрисовка сохраняет фокус лишь у
// элементов с id — то есть автоактивация стрелкой выбрасывала бы человека из
// списка на каждом шаге. Нажатие остаётся за Enter и Space, как у любой кнопки.
export function createKeyboardNavigation({ root }) {
  const typingTags = new Set(['input', 'textarea', 'select'])

  // Набор текста важнее навигации — то же правило и та же причина, что у
  // горячих клавиш очереди решений: стрелка внутри поля двигает каретку.
  function isTyping(target) {
    if (!target) return false
    if (target.isContentEditable) return true
    return typingTags.has(String(target.tagName || '').toLowerCase())
  }

  // Вложенные списки существуют: в детали решения свой перебор. Берём только
  // кнопки своего контейнера, иначе стрелка уводила бы в чужой список.
  //
  // Второстепенная кнопка строки (удалить чат) помечена data-keynav-skip:
  // стрелка идёт по строкам, а не по всем кнопкам подряд — иначе каждый второй
  // шаг вниз по списку чатов вставал на «×». До неё доходят Tab'ом.
  function itemsOf(container) {
    const all = container.querySelectorAll ? [...container.querySelectorAll('button:not([disabled])')] : []
    return all.filter(item => (item.closest ? item.closest('[data-keynav]') : container) === container
      && item.dataset?.keynavSkip === undefined)
  }

  function handleListKeydown(event) {
    if (event.ctrlKey || event.metaKey || event.altKey) return false
    const target = event.target
    if (isTyping(target)) return false
    const container = target?.closest?.('[data-keynav]')
    if (!container) return false
    const row = String(container.dataset?.keynav || '') === 'row'
    const forward = row ? 'ArrowRight' : 'ArrowDown'
    const backward = row ? 'ArrowLeft' : 'ArrowUp'
    const step = event.key === forward ? 1 : event.key === backward ? -1 : 0
    const edge = event.key === 'Home' ? 0 : event.key === 'End' ? -1 : undefined
    if (!step && edge === undefined) return false

    const items = itemsOf(container)
    if (items.length < 2) return false
    const current = target?.closest?.('button')
    const at = items.indexOf(current)

    let next
    if (edge !== undefined) {
      next = edge === 0 ? items[0] : items[items.length - 1]
    } else if (at < 0) {
      // Фокус на самом контейнере — начинаем с края, к которому идём.
      next = step > 0 ? items[0] : items[items.length - 1]
    } else {
      next = items[(at + step + items.length) % items.length]
    }
    if (!next || next === current) return false
    if (typeof next.focus === 'function') next.focus()
    // Полоса вкладок остаётся одним пунктом табуляции: остановка переезжает
    // вместе с фокусом, иначе Tab снова начал бы обходить все вкладки.
    if (row) {
      for (const item of items) {
        if (item.setAttribute) item.setAttribute('tabindex', item === next ? '0' : '-1')
      }
    }
    event.preventDefault?.()
    return true
  }

  return { handleListKeydown }
}
