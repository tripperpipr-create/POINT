// Ячейки квеста: чек-лист условий и меню редких действий.
//
// Квест показывают в трёх местах ленты — предложением, составом задания и
// карточкой запуска, — и до этого модуля каждое место рисовало условия по-своему:
// у наряда колонкой рядом с «Что будет сделано», у задания перечнем плана, у
// предложения простым списком. Одно и то же условие называлось в ленте «машинной
// проверкой», а в карточке «авто», и человек читал это как два разных вида.
//
// Форма взята из макета «Квесты в чате» (вариант 1b): по квесту принимают
// решение не по описанию работы, а по условиям, на которых его примут. Цвета при
// переносе не брались ни одного — в макете система моно-акцентная и заводит свои
// статусные оттенки, а у продукта они уже названы: --ember, --caution, --vital.

import { fillAttribute } from './format-units.js'

// Вид проверки — одним словом. Имена берутся у ядра как есть: договор
// `AcceptanceCriterion.Kind` знает ровно три значения — `verification`,
// `reproduction`, `manual`, — и любое четвёртое его же проверка отвергает
// как `invalid_criterion_kind`.
//
// Первая редакция словаря звала машинную проверку `automatic`. Такого вида
// нет ни в одном наряде, поэтому ветка не срабатывала никогда, а на экран
// уходило запасное значение — английское слово «verification» посреди
// русской карточки. Стенд этого не показал: его образцы были написаны под
// словарь, а не под ядро, то есть проверяли сами себя.
export const CRITERION_KIND = { verification: 'авто', reproduction: 'повтор', manual: 'вы' }

// Вид по умолчанию. Условие без вида — машинное: ручное ядро помечает явно,
// потому что от этой пометки зависит, кто его закрывает.
export const DEFAULT_CRITERION_KIND = 'verification'

// Условия готовности — чек-лист со счётчиком и полосой.
//
// `rows` — { text, kind, done }. Закрытость приходит извне: до запуска её
// неоткуда взять, после — её знает только прогон по своим проверкам. Обещать
// прогресс там, где его неоткуда взять, нельзя, поэтому счётчик считает всегда,
// а полоса до первого закрытого условия показывает засечку, а не пустоту.
export function questChecklistHtml(title, rows, esc, { empty = '' } = {}) {
  const items = Array.isArray(rows) ? rows.filter(row => row && String(row.text || '').trim()) : []
  if (!items.length) return empty ? `<div class="hall-quest-check"><span class="master-v2-empty">${esc(empty)}</span></div>` : ''
  const done = items.filter(row => row.done).length
  const share = Math.round((done / items.length) * 100)
  return `<div class="hall-quest-check">
    <div class="hall-quest-check-head"><span>${esc(title)}</span><b>${done} / ${items.length}</b></div>
    <div class="hall-quest-bar"><span ${fillAttribute(share)}></span></div>
    <ul>${items.map(row => `<li${row.done ? ' class="is-done"' : ''}><i aria-hidden="true">${row.done ? '✓' : ''}</i><span>${esc(row.text)}</span>${row.kind ? `<em>${esc(CRITERION_KIND[row.kind] || row.kind)}</em>` : ''}</li>`).join('')}</ul>
  </div>`
}

// Меню «···» — то, что с квестом делают нечасто: изменить, продолжить
// обсуждение, отклонить, убрать наряд.
//
// Пока все действия стояли в ряд одним весом, ряд не говорил, какое из них
// главное: глаз выбирал крайнее левое. В ряду остаётся одно решение, остальное
// живёт здесь. Механика взята у меню режимов в карточке ввода — `<details>` со
// списком: заводить вторую ради трёх пунктов дороже, чем переиспользовать первую.
export function questMenuHtml(items, esc) {
  const rowsHtml = (Array.isArray(items) ? items : []).filter(Boolean).map(item => {
    const attrs = [
      `data-action="${esc(item.action)}"`,
      item.id ? `data-id="${esc(item.id)}"` : '',
      item.questId ? `data-quest-id="${esc(item.questId)}"` : '',
      item.busy ? 'disabled' : '',
    ].filter(Boolean).join(' ')
    return `<button type="button" class="hall-btn" ${attrs}>${esc(item.label)}</button>`
  }).join('')
  if (!rowsHtml) return ''
  return `<details class="hall-deck-menu"><summary title="Ещё действия" aria-label="Ещё действия">···</summary><div>${rowsHtml}</div></details>`
}
