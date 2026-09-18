import type { RunStatus } from './types'

export const statusLabels: Record<RunStatus, string> = {
  pending: 'Ожидает запуска',
  running: 'Выполняется',
  waiting_approval: 'Ждёт подтверждения',
  completed: 'Завершено',
  failed: 'Ошибка',
  cancelled: 'Отменено',
  interrupted: 'Прервано',
}

export const approvalLabels: Record<string, string> = {
  pending: 'Ожидает решения',
  allowed: 'Разрешено',
  denied: 'Отклонено',
}

export const patchLabels: Record<string, string> = {
  pending: 'Ожидает решения',
  applied: 'Применено',
  rejected: 'Отклонено',
  conflict: 'Конфликт',
}

export const eventLabels: Record<string, string> = {
  'run.started': 'Запуск начат',
  'run.completed': 'Запуск завершён',
  'run.failed': 'Ошибка запуска',
  'run.cancelled': 'Запуск отменён',
  'run.interrupted': 'Запуск прерван',
  'model.requested': 'Запрос к модели',
  'model.streamed': 'Ответ модели',
  'model.responded': 'Модель ответила',
  'tool.requested': 'Инструмент запрошен',
  'tool.completed': 'Инструмент завершён',
  'tool.failed': 'Ошибка инструмента',
  'approval.requested': 'Нужно подтверждение',
  'approval.resolved': 'Подтверждение обработано',
  'patch.proposed': 'Предложено изменение',
  'patch.applied': 'Изменение применено',
  'patch.rejected': 'Изменение отклонено',
}

export const actorLabels: Record<string, string> = {
  user: 'Пользователь',
  agent: 'Агент',
  model: 'Модель',
  tool: 'Инструмент',
  system: 'Система',
}

export function formatTime(value?: string) {
  return value
    ? new Date(value).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    : '—'
}

export function formatDateTime(value: string) {
  return new Date(value).toLocaleString('ru-RU')
}

export function formatDuration(ms = 0) {
  if (ms < 1000) return `${ms} мс`
  const seconds = Math.floor(ms / 1000)
  if (seconds < 60) return `${seconds} с`
  return `${Math.floor(seconds / 60)} мин ${seconds % 60} с`
}

export function formatBytes(bytes: number) {
  return new Intl.NumberFormat('ru-RU').format(bytes)
}

export function humanError(error: unknown) {
  const message = String(error).replace(/^Error:\s*/i, '')
  const known: Array<[RegExp, string]> = [
    [/no workspace is open/i, 'Рабочая папка не открыта.'],
    [/workspace path is required/i, 'Укажите путь к рабочей папке.'],
    [/profile not found/i, 'Профиль агента не найден.'],
    [/run not found/i, 'Запуск не найден.'],
    [/approval not found/i, 'Запрос подтверждения не найден.'],
    [/connection refused/i, 'Не удалось подключиться к модели. Проверьте адрес и убедитесь, что сервис запущен.'],
    [/failed to fetch/i, 'Сервис приложения недоступен. Проверьте, что backend запущен.'],
    [/cancelled/i, 'Операция отменена.'],
  ]
  return known.find(([pattern]) => pattern.test(message))?.[1] ?? message
}
