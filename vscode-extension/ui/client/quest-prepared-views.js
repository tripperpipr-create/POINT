// Подготовленное, но не доставленное (TODO Q13).
//
// Пакет доказательств называет такие файлы только путями. Человек видел
// список и не видел ни того, что в них изменено, ни почему это не перенесено,
// ни что делать дальше. Diff уже лежит в снимке мира — наборы изменений
// дерева квеста; здесь каждый путь находит свой набор, рядом стоит причина,
// а выход — новая версия наряда через Мастера — одной кнопкой.
import { countOf, list } from './format-units.js'
import { icon } from './ui-icons.js'
import { diffCountHtml, diffHtml, diffStats } from './diff-view.js'

const SHOWN = 12
// Доставленное и заменённое новой версией — уже не «подготовлено».
const SETTLED = new Set(['applied', 'superseded'])

// Квест наряда и его потомки: наборы пишут этапы Flow, у каждого свой квест.
function questTree(rootId, quests) {
  const tree = new Set(rootId ? [rootId] : [])
  let grew = Boolean(rootId)
  while (grew) {
    grew = false
    for (const quest of quests) {
      if (quest?.id && quest.parentId && tree.has(quest.parentId) && !tree.has(quest.id)) {
        tree.add(quest.id)
        grew = true
      }
    }
  }
  return tree
}

// Путь → набор и его элемент. Новый набор раньше старого: у переписанного
// файла показывается последняя правка.
function preparedItems(runtime, boot) {
  const tree = questTree(String(runtime.questId || ''), list(boot?.quests))
  const executions = new Set(list(boot?.executions).filter(item => tree.has(item?.questId)).map(item => item.id))
  const sets = list(boot?.changeSets)
    .filter(set => !SETTLED.has(set?.status) && (tree.has(set?.questId) || executions.has(set?.executionId)))
    .sort((left, right) => String(right.updatedAt || right.createdAt || '').localeCompare(String(left.updatedAt || left.createdAt || '')))
  const found = new Map()
  for (const set of sets) {
    for (const item of list(set.items)) {
      const path = String(item?.path || '')
      if (path && !found.has(path)) found.set(path, { set, item })
    }
  }
  return found
}

// Почему не доставлено: конфликт набора, конфликт переноса, ручной перенос или
// первое ограничение пакета. Одна строка — подробности есть в полосе вердикта.
function preparedReason(runtime, found) {
  const evidence = runtime.evidence || {}
  const conflicted = [...found.values()].find(entry => entry.set.status === 'conflict')
  if (conflicted) {
    const paths = list(conflicted.set.resolutions).filter(item => item?.strategy === 'unresolved').map(item => item.path)
    return `Набор изменений в конфликте с проектом${paths.length ? `: ${paths.slice(0, 3).join(', ')}` : ''}.`
  }
  if (evidence.deliveryConflict) return 'Перенос остановлен конфликтом с текущим состоянием проекта.'
  if (String(evidence.deliveryTarget || '') === 'isolated_review') return 'Перенос ручной: результат ждёт решения в наборах изменений.'
  // Сообщение квеста — по-русски; строка шлюза — машинная и уже разобрана в
  // полосе вердикта, поэтому она последняя.
  const first = runtime.message || evidence.outcomeSummary || list(evidence.knownLimitations)[0]
  return first ? `Не перенесено: ${String(first)}` : 'Не перенесено: проверки не подтвердили результат.'
}

export function preparedFilesHtml(order, ui, esc) {
  const runtime = order?.runtime || {}
  const prepared = list(runtime.evidence?.preparedFiles)
  if (!prepared.length) return ''
  const found = preparedItems(runtime, ui?.state?.boot)
  const shown = prepared.slice(0, SHOWN)
  const rows = shown.map(path => {
    const entry = found.get(String(path))
    const head = `<span class="hall-step-icon">${icon('file')}</span><span>${esc(path)}</span>`
    if (!entry?.item?.diff) return `<li>${head}</li>`
    return `<li><details class="quest-prepared-file"><summary>${head}${diffCountHtml(diffStats(entry.item.diff))}</summary>${diffHtml(entry.item.diff, esc, { limit: 200 })}</details></li>`
  }).join('')
  const rest = prepared.length - shown.length
  const reason = preparedReason(runtime, found)
  // Закрытый наряд не продолжается: выход — новая версия через Мастера.
  const finished = ['blocked', 'failed', 'needs_review', 'completed', 'cancelled'].includes(String(runtime.status || ''))
  const question = `Подготовь новую версию наряда: подготовленные файлы не доставлены. ${reason}`
  const next = finished
    ? `<div class="quest-actions"><div><button type="button" class="hall-btn" data-action="master-ask" data-question="${esc(question)}">Новая версия наряда</button></div></div>`
    : ''
  return `<div class="quest-section quest-prepared"><h4>Подготовлено, не доставлено · ${countOf(prepared.length, 'файл', 'файла', 'файлов')}</h4><p class="quest-prepared-reason">${esc(reason)}</p><ul class="quest-files">${rows}</ul>${rest > 0 ? `<small class="quest-more">и ещё ${countOf(rest, 'файл', 'файла', 'файлов')}</small>` : ''}${next}</div>`
}
