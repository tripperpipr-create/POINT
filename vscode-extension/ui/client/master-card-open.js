// Раскрытые подробности карточек ленты: одна память на все четыре регистра.
//
// Карточек в разговоре четыре рода — предложение квеста, состав задания, единая
// карточка запуска и новый исполнитель с наймом, — и у каждой были свои
// подробности под своей раскрывашкой. Три из них не помнили ничего: лента
// перерисовывается на каждый ход, а `<details>` живёт в разметке, и раскрытое
// схлопывалось прямо под читающим. Четвёртая помнила модульным набором, который
// не переживал перезапуск панели.
//
// Память здесь трёхзначная — `undefined | true | false`, а не «набор
// открытых». Разница нужна одной карточке: доказательства результата
// раскрываются сами, когда прогон завершён (master-work-order-v2.js), а
// наблюдатель наряда перерисовывает ленту каждые две с половиной секунды. При
// памяти «только открытые» закрытая человеком панель распахивалась бы ему в лицо
// на каждом опросе: закрытие ничем не отличалось бы от «ещё не трогали».
//
// DOM модуль не трогает вовсе. Разметку собирают виды, а состояние приходит
// функцией: смоуки исполняют собранный media/main.js в поддельном окружении, где
// вместо узлов стоят заглушки, и любое обращение к документу роняло бы их.

// Карта раскрытий текущего разговора. Ставит main.js — он единственный знает,
// какой разговор открыт и где лежит сохранённое состояние.
let access = () => null

export function useMasterCardOpen(accessor) {
  access = typeof accessor === 'function' ? accessor : () => null
}

// Набор с тремя значениями. Имена `add`/`delete` не случайны: общий перехватчик
// события toggle в main.js зовёт именно их, и карточки встают в него наравне с
// рассуждением, шагами и живым следом — без второй ветки разбора.
export const masterCardOpen = {
  get(key) {
    const map = access()
    return map ? map[key] : undefined
  },
  set(key, open) {
    const map = access()
    if (map && key) map[key] = Boolean(open)
  },
  add(key) { this.set(key, true) },
  delete(key) { this.set(key, false) },
  // Забыть решение — не то же самое, что закрыть: после этого снова работает
  // значение по умолчанию. Нужно смоукам и смене мира.
  forget(key) {
    const map = access()
    if (map && key in map) delete map[key]
  },
}

// Атрибуты раскрывашки для готового `<details>`: ключ, метка рода и решение
// человека поверх умолчания вида. Отдельно от `masterCardMoreHtml` потому, что
// половина раскрывашек несёт своё имя класса и своё тело с разметкой внутри
// `<summary>` — переписывать их целиком ради одной строки атрибутов было бы
// правкой ради правки.
export function masterCardMoreAttrs(key, { esc = String, open = false } = {}) {
  const decided = masterCardOpen.get(key)
  const shown = decided === undefined ? Boolean(open) : decided
  return ` data-master-open="card" data-id="${esc(key)}"${shown ? ' open' : ''}`
}

// Подробности карточки. Одна разметка на все регистры: имя класса общее, ключ
// несёт префикс рода карточки, а `open` — то самое умолчание, которое человек
// вправе перебить и в ту, и в другую сторону.
export function masterCardMoreHtml(key, summary, body, { esc = String, open = false, className = '' } = {}) {
  const decided = masterCardOpen.get(key)
  const shown = decided === undefined ? Boolean(open) : decided
  const classes = `hall-deck-more${className ? ` ${className}` : ''}`
  return `<details class="${classes}" data-master-open="card" data-id="${esc(key)}"${shown ? ' open' : ''}>
    <summary>${esc(summary)}</summary>
    ${body}
  </details>`
}
