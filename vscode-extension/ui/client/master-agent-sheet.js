// Лист персонажа: портрет, класс, характеристики, снаряжение.
//
// Форма взята из макета «Квесты в чате» (вариант 3a). Условие макета здесь
// главное и оно же держит весь модуль: характеристики персонажа — это реальные
// настройки агента, а не украшение. Ни одно число на листе не выдумано; каждая
// полоса считается из поля, которое правят в той же карточке под раскрывашкой
// «Права и пределы», и нажатие на полосу туда и ведёт.
//
// Отдельным модулем — потому что карточка исполнителя стоит у своей границы в
// 450 строк (check-release-contracts.mjs), а лист это связный кусок: словарь
// классов, шкалы характеристик и три яруса разметки при них.
import { countOf, fillAttribute, list, shortLabel } from './format-units.js'

const text = value => String(value ?? '').trim()

// Усилие рассуждения — часть решения о модели, а не тонкая настройка «на
// потом»: у рассуждающих моделей оно меняет и цену хода, и время ответа.
// Словарь живёт здесь же, где шкала: разъехавшись, они назвали бы одно и то же
// усилие по-разному в полосе и в поле.
export const EFFORT_OPTIONS = [['none', 'обычное'], ['minimal', 'минимальное'], ['low', 'низкое'], ['medium', 'среднее'], ['high', 'высокое']]
const EFFORT_SCORE = { none: 1, minimal: 2, low: 3, medium: 4, high: 5 }

// Семейства ролей — словарь ядра, а не наш.
//
// Комплектовщик умеет ровно эти шесть и любое седьмое отвергает
// (roleFamilyTemplates в internal/app/agent_selector.go). Класс персонажа —
// это оно и есть; выдумывать классы поверх списка ядра нельзя: карточка
// назвала бы исполнителя тем, кем его никто не заводил. Пусто — строки нет.
const ROLE_FAMILY = {
  developer: 'разработчик',
  tester: 'тестировщик',
  designer: 'дизайнер',
  analyst: 'аналитик',
  devops: 'devops',
  security_auditor: 'аудитор безопасности',
}

// Монограмма — из имени, которое человек и набирает. Пока имени нет, портрет
// пустой и это видно: пустой слот в макете зовёт его заполнить, а не
// притворяется заполненным. Портрета здесь однажды не было ровно из-за
// обратного опасения — что пустая плашка прочтётся незагрузившейся картинкой;
// монограмма имени картинкой не притворяется.
function monogram(name) {
  const words = text(name).split(/\s+/).filter(Boolean)
  if (!words.length) return ''
  // Имя из одного слова даёт два знака, а не один: «К» в плитке 72×72 читается
  // не монограммой, а обрубком. Два знака — та же мера, что и у имени из двух
  // слов, и плитка выглядит одинаково у всех.
  if (words.length === 1) return [...words[0]].slice(0, 2).join('').toUpperCase()
  return words.slice(0, 2).map(word => [...word][0].toUpperCase()).join('')
}

function stepScore(value, edges) {
  const number = Number(value) || 0
  let score = 1
  for (const edge of edges) if (number > edge) score += 1
  return Math.max(1, Math.min(5, score))
}

// Автономность — как далеко исполнитель уходит без вопросов. Складывается из
// того, что он спрашивает (approvalMode), и из того, сколько умений ему
// разрешено пускать в ход без спроса.
function autonomyScore(value) {
  const tools = list(value.allowedTools)
  const free = tools.filter(name => String(value.toolPolicies?.[name] || '').toUpperCase() === 'ALLOW').length
  const share = tools.length ? free / tools.length : 0
  const base = value.approvalMode === 'always' ? 1 : 3
  return Math.min(5, base + (share >= 0.5 ? 1 : 0) + (share >= 0.9 ? 1 : 0))
}

// Каждая характеристика называет поле, от которого считается: по нему карточка
// и открывает настройку. Второго редактора тех же значений мы не заводим — у
// задания это однажды стоило двойного чтения формы (master-brief-panel.js).
export function agentStats(value) {
  const effort = EFFORT_OPTIONS.find(([key]) => key === (value.reasoningEffort || 'none'))
  const minutes = Math.round((Number(value.maxDurationSeconds) || 0) / 60)
  return [
    {
      label: 'Автономность', field: 'approvalMode', score: autonomyScore(value),
      hint: value.approvalMode === 'always' ? 'спрашивает про всё' : 'спрашивает про опасное',
    },
    {
      label: 'Тщательность', field: 'reasoningEffort', score: EFFORT_SCORE[value.reasoningEffort || 'none'] || 1,
      hint: `рассуждение ${effort ? effort[1] : 'обычное'}`,
    },
    {
      label: 'Размах', field: 'maxSteps', score: stepScore(value.maxSteps, [10, 20, 40, 70]),
      hint: `до ${countOf(Number(value.maxSteps) || 0, 'хода', 'ходов', 'ходов')} за прогон`,
    },
    {
      label: 'Выдержка', field: 'maxDurationSeconds', score: stepScore(value.maxDurationSeconds, [300, 900, 1800, 3600]),
      hint: `до ${countOf(minutes, 'минуты', 'минут', 'минут')} работы`,
    },
  ]
}

export function agentStatsHtml(card, value, esc) {
  const rows = agentStats(value).map(stat => `<button type="button" class="master-agent-stat" data-action="agent-card-open-more" data-card="${esc(card.id)}" data-field="${esc(stat.field)}" title="${esc(stat.hint)}">
      <span class="master-agent-stat-name">${esc(stat.label)}</span>
      <span class="hall-quest-bar"><span ${fillAttribute(stat.score * 20)}></span></span>
      <span class="master-agent-stat-score">${stat.score}/5</span>
    </button>`).join('')
  return `<div class="master-agent-stats"><span class="master-agent-rubric">характеристики</span>${rows}</div>`
}

// Снаряжение — те же умения, что в сетке прав, но в лицо: по фишкам видно, чем
// исполнитель вооружён, не раскрывая подробностей. Пустой слот ведёт в сетку.
export function agentGearHtml(card, value, esc, deps) {
  const catalog = list(deps.toolCatalog)
  const label = name => catalog.find(item => item.name === name)?.displayName || name
  const tools = list(value.allowedTools)
  const shown = tools.slice(0, 8).map(name => `<span class="master-agent-slot">${esc(shortLabel(label(name), 22))}</span>`).join('')
  const rest = tools.length - 8
  return `<div class="master-agent-gear"><span class="master-agent-rubric">снаряжение</span>
      <div class="master-agent-slots">${shown}${rest > 0 ? `<span class="master-agent-slot">+${rest}</span>` : ''}<button type="button" class="master-agent-slot is-empty" data-action="agent-card-open-more" data-card="${esc(card.id)}" data-field="tool">+ умение</button></div>
    </div>`
}

// Портрет и то, что при нём: класс, уровень, послужной список. Ничего из этого
// не выдумывается — нет в данных, нет и строки. У черновика из ленты нет ни
// класса, ни уровня: их ставит ядро тому, кто уже работал (flow_completion.go).
export function agentPortraitHtml(card, value, esc) {
  const mark = monogram(value.name)
  const family = ROLE_FAMILY[value.roleFamily] || ''
  const level = Number(value.level) || 0
  const done = Number(value.tasksCompleted) || 0
  const maxAgents = Number(card.maxAgents) || 0
  return `<div class="master-agent-portrait">
      <span class="master-agent-mark${mark ? '' : ' is-empty'}" aria-hidden="true">${esc(mark || '+')}</span>
      ${family ? `<span class="master-agent-class">${esc(family)}</span>` : ''}
      ${level ? `<span class="master-agent-rank">уровень ${level}${value.experience ? ` · ${Number(value.experience)} опыта` : ''}</span>` : ''}
      ${done ? `<span class="master-agent-rank">${esc(countOf(done, 'квест', 'квеста', 'квестов'))} · ${Number(value.successCount) || 0} успешно</span>` : ''}
      ${maxAgents ? `<span class="master-agent-rank">в отряде до ${maxAgents}</span>` : ''}
    </div>`
}
