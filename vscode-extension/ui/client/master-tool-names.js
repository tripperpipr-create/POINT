// Русские имена читающих инструментов Мастера.
//
// Сырое «search_code» посреди русской фразы — не только чужое слово на экране,
// но и требование к человеку знать внутренние имена. Полное имя остаётся в
// подсказке по наведению: разбирающемуся оно нужно, остальным мешает.
//
// Список закрыт ядром и ничем больше: companionReadTools собирает девять
// читающих инструментов (internal/app/companion_tools.go), masterReadTools
// добавляет к ним три чтения сущностей (internal/app/master_tools.go) и чтение
// ростера (internal/app/master_roster_tool.go). Пока
// словарь держался на памяти, в нём жил несуществующий `index_search`, а трёх
// настоящих не было — и разговор писал «обратился к инструменту» там, где мог
// сказать, к какому. Совпадение сторожит сверка договорённостей.
//
// Времён два, потому что мест два: в готовом ходе шаг уже случился, а строка
// ожидания рассказывает о происходящем прямо сейчас.
export const MASTER_TOOL_NAMES = {
  project_map: 'построил карту проекта',
  read_file: 'прочитал файл',
  list_files: 'посмотрел каталог',
  search_text: 'искал по тексту',
  search_code: 'искал по коду',
  git_diff: 'посмотрел изменения',
  git_log: 'посмотрел историю',
  git_branches: 'посмотрел ветки',
  git_tags: 'посмотрел метки',
  read_execution: 'прочитал запуск',
  read_changeset: 'прочитал набор правок',
  read_quest: 'прочитал квест',
  read_roster: 'посмотрел ростер',
}

export const MASTER_TOOL_NAMES_NOW = {
  project_map: 'строю карту проекта',
  read_file: 'читаю файл',
  list_files: 'смотрю каталог',
  search_text: 'ищу по тексту',
  search_code: 'ищу по коду',
  git_diff: 'смотрю изменения',
  git_log: 'смотрю историю',
  git_branches: 'смотрю ветки',
  git_tags: 'смотрю метки',
  read_execution: 'читаю запуск',
  read_changeset: 'читаю набор правок',
  read_quest: 'читаю квест',
  read_roster: 'смотрю ростер',
}

export function masterToolName(tool) {
  return MASTER_TOOL_NAMES[String(tool || '')] || 'обратился к инструменту'
}

// Строка ожидания получает от ядра сырое имя инструмента: событие `tools`
// несёт call.Name и ничего больше. Незнакомое имя не выдаём за знакомое.
export function masterToolNameNow(tool) {
  return MASTER_TOOL_NAMES_NOW[String(tool || '')] || ''
}
