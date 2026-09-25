// Русские имена читающих инструментов Мастера.
//
// Сырое «search_code» посреди русской фразы — не только чужое слово на экране,
// но и требование к человеку знать внутренние имена. Полное имя остаётся в
// подсказке по наведению: разбирающемуся оно нужно, остальным мешает.
//
// Список закрыт ядром и ничем больше: companionReadTools собирает девять
// читающих инструментов (internal/app/companion_tools.go), masterReadTools
// добавляет к ним три чтения сущностей (internal/app/master_tools.go), а
// инструменты разговора — задание, уточнения и память — объявлены в
// internal/orchestrator/master_actions.go. Пока словарь держался на памяти, в нём жил несуществующий `index_search`, а трёх
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
  propose_brief: 'оформил задание',
  ask_clarifications: 'задал уточнения',
  suggest_memory: 'предложил запомнить',
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
  propose_brief: 'оформляю задание',
  ask_clarifications: 'формулирую уточнения',
  suggest_memory: 'предлагаю запомнить',
}

export function masterToolName(tool) {
  return MASTER_TOOL_NAMES[String(tool || '')] || 'обратился к инструменту'
}

// Строка ожидания получает от ядра сырое имя инструмента: событие `tools`
// несёт call.Name и ничего больше. Незнакомое имя не выдаём за знакомое.
export function masterToolNameNow(tool) {
  return MASTER_TOOL_NAMES_NOW[String(tool || '')] || ''
}

// Род обращения — для значка строки. Значок отвечает на вопрос «чем смотрел»
// (файл, поиск, история), и одинаковый значок у девяти строк подряд ничего бы
// не сообщал. Незнакомое имя получает общий значок инструмента.
const MASTER_TOOL_ICONS = {
  project_map: 'map',
  read_file: 'file',
  list_files: 'folder',
  search_text: 'search',
  search_code: 'search',
  git_diff: 'git',
  git_log: 'git',
  git_branches: 'git',
  git_tags: 'git',
  read_execution: 'terminal',
  read_changeset: 'file-edit',
  read_quest: 'quest',
  propose_brief: 'quest',
  ask_clarifications: 'info',
  suggest_memory: 'memory',
}

export function masterToolIcon(tool) {
  return MASTER_TOOL_ICONS[String(tool || '')] || 'tool'
}
