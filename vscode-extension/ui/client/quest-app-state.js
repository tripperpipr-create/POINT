// Живое состояние доставленных приложений и итоговых отчётов квестов.
//
// Карточка прогона рисует блок приложения и кнопку отчёта по этим картам, а
// заполняют их ответы хоста: `masterApplicationState` приходит раз в 0,7 с,
// пока ядро поднимает контейнеры, и `masterReportState` — на каждой фазе
// сборки отчёта. Карты модульные, как согласие на исполнителя
// (master-agent-card.js): main.js стоит у потолка строк, а переживать
// перезапуск панели этому состоянию не нужно — его заново скажет ядро.

import { list } from './format-units.js'

// questId → { state, pending, opened, error, requested }
export const questApps = new Map()
// workOrderId → { phase, path, uri, error }
export const questReports = new Map()

export function questAppOf(questId) {
  return questApps.get(String(questId || '')) || null
}

// Нажатие: действие идёт с этой секунды, ещё до первого ответа ядра. Вывод
// прошлого действия снимается — иначе строки прошлой остановки читались бы
// как начало нового запуска, пока не придёт первый опрос.
export function markQuestAppPending(questId, action) {
  const id = String(questId || '')
  if (!id) return
  const current = questApps.get(id) || {}
  const state = current.state ? { ...current.state, lines: [], startedAt: '' } : null
  questApps.set(id, { ...current, state, pending: action, since: Date.now(), error: '', opened: '' })
}

export function acceptQuestAppState(message) {
  const id = String(message?.questId || '')
  if (!id) return false
  const current = questApps.get(id) || {}
  // Опрос до того, как ядро записало начало действия, отдаёт прошлое состояние:
  // пока идёт нажатое действие, такой снимок не заменяет свежий.
  const stale = current.pending && !message.final && message.state && !message.state.inFlight
  const state = stale ? current.state : message.state || current.state || null
  // Идущее действие снимает «ожидание» только финальным ответом: промежуточный
  // опрос может прийти раньше, чем ядро записало начало действия.
  const pending = message.final ? '' : current.pending || (state?.inFlight ? state.action : '')
  questApps.set(id, {
    ...current, state, pending, requested: true,
    // Что отдаёт приложение — страницу или JSON — известно после запуска;
    // остановка ответа по адресу не даёт, и вид не должен от этого меняться.
    contentType: String(state?.contentType || current.contentType || ''),
    opened: message.opened || current.opened || '',
    error: message.error ? String(message.error) : message.final ? '' : current.error || '',
  })
  return true
}

export function questReportOf(workOrderId) {
  return questReports.get(String(workOrderId || '')) || null
}

export function acceptQuestReportState(message) {
  const id = String(message?.workOrderId || '')
  if (!id) return false
  questReports.set(id, { phase: String(message.phase || ''), path: String(message.path || ''), uri: String(message.uri || ''), error: String(message.error || '') })
  return true
}

// Какие приложения спросить у ядра: законченные с квитанцией доставки, о
// которых ещё не спрашивали. Один запрос на квест за жизнь панели.
export function questAppsToProbe(workOrders) {
  const due = []
  for (const order of list(workOrders)) {
    const runtime = order?.runtime
    const id = String(runtime?.questId || '')
    if (!id || runtime?.status !== 'completed' || !runtime?.deliveryReceipt?.id) continue
    const current = questApps.get(id) || {}
    if (current.requested) continue
    questApps.set(id, { ...current, requested: true })
    due.push({ questId: id, workOrderId: String(order.id || '') })
  }
  return due
}
