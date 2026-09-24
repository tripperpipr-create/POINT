import { countOf, plural } from './format-units.js'
import { esc } from './html-escape.js'

export function createViewRuntime({ getState, createCompanionMarkdownFormatter }) {
  const statusLabels = { pending:'Ожидает', running:'Выполняется', waiting:'Нужно решение', waiting_approval:'Нужно решение', paused:'Пауза', completed:'Завершён', failed:'Провален', cancelled:'Отменён', interrupted:'Прерван' }
  const toolLabels = { project_map:'Карта проекта', search_code:'Умный поиск кода', list_files:'Список файлов', read_file:'Чтение файла', search_text:'Поиск по проекту', propose_patch:'Изменение файла', run_command:'Запуск команды', git_diff:'Git diff', read_skill:'Чтение навыка', docker_inspect:'Docker: обзор', docker_control:'Docker: start/stop', ssh_test_connection:'Проверка SSH', ssh_list_remote:'Список на сервере', ssh_exec_remote:'Команда на сервере', db_list_connections:'Список БД', db_schema:'Схема БД', db_query:'SQL-запрос', db_exec:'SQL-запись' }
  const eventLabels = { 'run.started':'Запуск начат', 'run.completed':'Задача завершена', 'run.failed':'Ошибка запуска', 'run.cancelled':'Запуск отменён', 'run.message_injected':'Уточнение пользователя', 'model.requested':'Запрос к модели', 'model.usage':'Использование токенов', 'model.responded':'Ответ модели', 'tool.requested':'Вызов инструмента', 'tool.started':'Инструмент запущен', 'tool.finished':'Инструмент завершён', 'approval.requested':'Нужно подтверждение', 'approval.resolved':'Решение принято', 'patch.proposed':'Предложено изменение', 'patch.applied':'Изменение применено', 'patch.rejected':'Изменение отклонено', 'patch.reverted':'Изменение откачено' }
  const healthLabels = { active:'Выполняется', healthy:'Без сбоев', attention:'Нужно внимание', failed:'Завершён с ошибкой' }
  const stopReasonLabels = { active:'запуск активен', completed:'модель завершила задачу', cancelled_by_user:'остановлено пользователем', interrupted:'процесс был прерван', timeout:'превышен тайм-аут', step_limit:'достигнут лимит шагов', provider_error:'ошибка модели или провайдера', failed:'ошибка выполнения' }
  eventLabels['model.retrying'] = 'Повтор запроса к модели'
  eventLabels['agent.guardrail'] = 'Защита агента'
  eventLabels['completion.checked'] = 'Проверка финала'
  eventLabels['workspace.changed'] = 'Изменения команды зафиксированы'
  stopReasonLabels.agent_stalled = 'агент остановлен из-за повторяющихся действий'
  stopReasonLabels.completion_evidence_missing = 'не хватает проверяемых доказательств готовности'

  const formatCompanionMarkdown = createCompanionMarkdownFormatter(esc)
  function data(value) { return value && typeof value === 'object' ? value : {} }
  function toolName(value) { return getState().boot?.toolCatalog?.find(item=>item.name===value)?.displayName || toolLabels[value] || value }
  function providerCatalog() { return getState().boot?.providerCatalog || [] }
  function providerPreset(profile) { return providerCatalog().find(item=>item.id===(profile?.providerPreset||profile?.provider)) || providerCatalog().find(item=>item.kind===profile?.provider) }
  function requiresApiKey(profile) { return Boolean(providerPreset(profile)?.requiresApiKey) }
  function lines(value) { return String(value || '').split(/\r?\n/).map(item=>item.trim()).filter(Boolean) }
  function toolProvidesVerification(tool) { return Boolean(tool?.providesVerification) || tool?.name === 'run_command' }
  function modelCapabilityProbeHtml(result) {
    if (!result) return '<p class="model-capability-empty">После проверки связи Point запустит короткую проверку выбранной модели под эту роль.</p>'
    if (result.loading) return '<section class="model-capability-report is-loading"><header><strong>Проверяем поведение модели…</strong><small>In-memory fixture · проект не изменяется</small></header></section>'
    if (result.error) return `<section class="model-capability-report"><header><strong>Capability probe не выполнен</strong><small>${esc(result.error)}</small></header></section>`
    const checks = [
      ['Tool calls', result.toolCalls],
      ['JSON / schema', result.jsonContract],
      ['Inspection → edit', result.inspectionBeforeEdit],
      ['Verification evidence', result.verificationEvidence],
      ['Context / time', result.withinLimits],
    ]
    const rows = checks.map(([label, check]) => {
      const statusValue = String(check?.status || 'FAIL')
      const icon = statusValue === 'PASS' ? '✓' : statusValue === 'NOT_APPLICABLE' ? '—' : '✕'
      return `<li class="probe-${statusValue.toLowerCase()}"><b>${icon}</b><span><strong>${esc(label)}</strong><small>${esc(check?.detail || 'нет результата')}</small></span></li>`
    }).join('')
    const limitations = (result.limitations || []).map(item => `<li>${esc(item)}</li>`).join('')
    const suggestions = (result.suggestions || []).map(item => `<li>${esc(item)}</li>`).join('')
    return `<section class="model-capability-report"><header><div><strong>Role-specific capability probe</strong><small>${esc(result.model || '')} · ${esc(result.role || '')}</small></div><em>без общего балла</em></header><ul class="model-capability-checks">${rows}</ul><footer><span>tool failures: <b>${Number(result.toolFailures || 0)}</b></span><span>${Number(result.durationMs || 0).toLocaleString('ru-RU')} мс</span><span>context ≤ ${Number(result.maxContextTokens || 0).toLocaleString('ru-RU')} / ${Number(result.contextLimitTokens || 0).toLocaleString('ru-RU')} ток.</span></footer>${limitations ? `<details><summary>Ограничения</summary><ul>${limitations}</ul></details>` : ''}${suggestions ? `<details><summary>Проверенные варианты настройки</summary><ul>${suggestions}</ul></details>` : ''}</section>`
  }
  return {
    statusLabels, toolLabels, eventLabels, healthLabels, stopReasonLabels,
    plural, countOf, esc, formatCompanionMarkdown, data, toolName,
    providerCatalog, providerPreset, requiresApiKey, lines, toolProvidesVerification, modelCapabilityProbeHtml,
  }
}
