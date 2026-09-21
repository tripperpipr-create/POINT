// В политиках инструментов остаётся только выбор человека.
//
// Карту `toolPolicies` ядро читает не как справочник, а как список стоячих
// указаний: запись в ней отменяет авто-подтверждение внутри утверждённого
// наряда (`taskAutoApproved` в internal/agent/task_authority.go). Конструктор
// же записывал политику каждого инструмента — включая ту, которую человек не
// трогал: значение по риску. Машинное умолчание попадало в ту же карту и
// глушило авто-режим, которого никто не выключал: 21 сентября 2026 на квесте с
// утверждённым нарядом и `approvalMode: safe` каждый патч и каждая команда
// спрашивали разрешения.
//
// Отсутствие записи означает для ядра ровно то же самое значение по риску
// (`PolicyForTool`), поэтому убрать умолчание безопасно. Кроме инструментов,
// запрещённых по умолчанию: у них пустота означает DENY, и снятие записи
// ужесточило бы доступ молча.
//
//   node scripts/smoke-agent-tool-policy-defaults.mjs

import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const clientDir = path.join(root, 'vscode-extension', 'ui', 'client')
const { createAgentConstructor, defaultToolPolicyForRisk } = await import(
  pathToFileURL(path.join(clientDir, 'agent-constructor.js')).href
)

if (defaultToolPolicyForRisk('LOW') !== 'ALLOW' || defaultToolPolicyForRisk('HIGH') !== 'ASK') {
  throw new Error('умолчание по риску изменилось — проверка ниже считает по нему')
}

let selects = []
const { readConstructorToolPolicies } = createAgentConstructor({
  ui: {},
  root: { querySelectorAll: selector => (selector === 'select[name="constructor-tool-policy"]' ? selects : []), querySelector: () => null },
  vscode: { postMessage() {} },
  esc: value => String(value ?? ''),
  lines: () => [],
  providerCatalog: () => [],
  agentById: () => undefined,
  hubModeAvailable: () => true,
  persistDraft: () => {},
  getState: () => ({ boot: {} }),
  getIgnoredCompanionSuggestions: () => new Set(),
})

const select = (tool, risk, value, denyDefault) => ({
  value,
  dataset: denyDefault ? { tool, risk, denyDefault: 'true' } : { tool, risk },
})

// 1. Нетронутые умолчания в карту не попадают.
selects = [
  select('read_file', 'LOW', 'ALLOW'),
  select('propose_patch', 'HIGH', 'ASK'),
  select('run_command', 'HIGH', 'ASK'),
]
const untouched = readConstructorToolPolicies({}, {})
if (Object.keys(untouched).length) {
  throw new Error(`умолчания сохранились как решение человека: ${JSON.stringify(untouched)}`)
}

// 2. Отклонение от умолчания — решение человека, и оно записывается.
selects = [
  select('read_file', 'LOW', 'ASK'),
  select('propose_patch', 'HIGH', 'ALLOW'),
  select('run_command', 'HIGH', 'DENY'),
]
const chosen = readConstructorToolPolicies({}, {})
for (const [tool, value] of [['read_file', 'ASK'], ['propose_patch', 'ALLOW'], ['run_command', 'DENY']]) {
  if (chosen[tool] !== value) throw new Error(`выбор человека потерян: ${tool} = ${chosen[tool]}`)
}

// 3. Прежняя запись, совпавшая с умолчанием, снимается: иначе агент, однажды
//    созданный со старым поведением, навсегда остался бы без авто-режима.
selects = [select('propose_patch', 'HIGH', 'ASK')]
const normalized = readConstructorToolPolicies({}, { propose_patch: 'ASK', 'network': 'DENY' })
if ('propose_patch' in normalized) {
  throw new Error('прежнее умолчание осталось в карте и продолжит глушить авто-режим')
}
// Ключи не-инструментов карту не покидают: `network` — политика исходящей сети,
// её считает egress, а не затвор подтверждений.
if (normalized.network !== 'DENY') {
  throw new Error('политика сети пропала вместе с умолчаниями инструментов')
}

// 4. Запрещённый по умолчанию инструмент записывается всегда: без записи он
//    DENY, и снятие ASK отняло бы доступ, который человек оставил.
selects = [select('ssh_exec_remote', 'CRITICAL', 'ASK', true), select('db_query', 'MEDIUM', 'ASK', true)]
const denyDefaults = readConstructorToolPolicies({}, {})
if (denyDefaults.ssh_exec_remote !== 'ASK' || denyDefaults.db_query !== 'ASK') {
  throw new Error(`запрещённый по умолчанию инструмент потерял разрешение: ${JSON.stringify(denyDefaults)}`)
}

process.stdout.write(JSON.stringify({ toolPolicies: 'only-human-choices', cases: 4 }))
