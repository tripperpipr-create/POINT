// Имя этапа квеста — по-русски, не трогая модель.
//
// Этапы называет ядро, и по-английски: шаблоны планировщика и конвейера
// («Input», «Verify result», «Implementation review») — закрытый список строк
// в internal/orchestrator и internal/flowruntime, а план модели приходит с
// её собственными названиями («Implement Go health service…»). Моделям
// английский удобнее, и переучивать их ради экрана владелец не велел.
//
// Поэтому перевод живёт здесь. Шаблонное имя заменяется целиком. Своё имя
// модели не выбрасывается — оно точнее любого общего слова, — но встаёт второй
// тихой строкой после русской роли этапа. Имя, написанное по-русски, остаётся
// как есть.

import { list } from './format-units.js'

const TEMPLATE_NAMES = {
  Input: 'Вход', Output: 'Выход', 'Verify result': 'Проверка результата', Verifier: 'Проверка',
  'User approval': 'Решение пользователя', 'User resolve': 'Решение пользователя',
  Bootstrap: 'Подготовка', Implement: 'Реализация', Integrate: 'Интеграция',
  'Implementation review': 'Ревью реализации', Accept: 'Приёмка',
  Primary: 'Основная работа', Reviewer: 'Ревью', Specialist: 'Специалист',
  'Model A': 'Ветка A', 'Model B': 'Ветка B', Join: 'Слияние', 'Independent branches': 'Параллельные ветки', Wave: 'Волна',
}

// Роль этапа конвейера (domain.StageRole*) — из настройки узла Flow.
const STAGE_ROLES = {
  bootstrap: 'Подготовка', implement: 'Реализация', integrate: 'Интеграция', impl_review: 'Ревью реализации', accept: 'Приёмка',
}

// Узел Flow, из которого вырос этап: у этапа наряда роли нет, она лежит в
// config узла. Ищем по flowId наряда, а без него — по любому Flow с таким узлом.
export function stageFlowNode(boot, runtime, stage) {
  const flows = list(boot?.flows)
  const own = flows.find(flow => flow.id === runtime?.flowId)
  const pool = own ? [own] : flows
  for (const flow of pool) {
    const node = list(flow.nodes).find(item => item.id === stage?.id)
    if (node) return node
  }
  return null
}

// { label, detail }: label — всегда по-русски, detail — английское имя модели,
// если оно было.
export function stageLabel(stage, { node = null, kindLabels = {} } = {}) {
  const name = String(stage?.name || '').trim()
  const kind = String(stage?.kind || node?.kind || '')
  const role = STAGE_ROLES[String(node?.config?.stageRole || '')] || ''
  if (TEMPLATE_NAMES[name]) return { label: TEMPLATE_NAMES[name], detail: '' }
  const phase = name.match(/^(Join )?[Pp]hase (\d+)$/)
  if (phase) return { label: phase[1] ? `Сведение фазы ${phase[2]}` : `Фаза ${phase[2]}`, detail: '' }
  if (!name) return { label: role || kindLabels[kind] || String(stage?.id || 'Этап'), detail: '' }
  if (/[а-яё]/i.test(name)) return { label: name, detail: '' }
  const general = role || (kind && kind !== 'agent' ? kindLabels[kind] : '') || 'Работа агента'
  return { label: general, detail: name }
}

// Одна строка для мест, где второй строки нет: «Реализация · Implement …».
export function stageLabelText(stage, options) {
  const { label, detail } = stageLabel(stage, options)
  return detail ? `${label} · ${detail}` : label
}
