import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const defaultPolicy = JSON.parse(fs.readFileSync(path.join(root, 'distribution', 'dogfood-gate.json'), 'utf8'))
const reportKeys = [
  'schemaVersion', 'candidateVersion', 'candidateCommit', 'startedAt', 'completedAt',
  'fourteenDayAttestation', 'operatorSignoff', 'totals', 'checkpoints',
]
const counterKeys = [
  'runsStarted', 'runsTerminal', 'p0', 'p1', 'dataLossIncidents',
  'databaseCorruptionIncidents', 'lostTerminalEvents',
]
const checkpointKeys = ['timestamp', 'activeUseAttestation', ...counterKeys]

const isObject = value => value !== null && typeof value === 'object' && !Array.isArray(value)

function exactKeys(value, expected, label, errors) {
  if (!isObject(value)) {
    errors.push(`${label} must be an object`)
    return
  }
  const actual = Object.keys(value)
  const unexpected = actual.filter(key => !expected.includes(key))
  const missing = expected.filter(key => !actual.includes(key))
  if (unexpected.length) errors.push(`${label} contains forbidden fields: ${unexpected.join(', ')}`)
  if (missing.length) errors.push(`${label} is missing fields: ${missing.join(', ')}`)
}

function parseTimestamp(value, label, errors) {
  if (typeof value !== 'string' || value.length > 40 || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) {
    errors.push(`${label} must be a bounded UTC RFC3339 timestamp`)
    return NaN
  }
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) errors.push(`${label} is invalid`)
  return parsed
}

function validatePolicy(policy) {
  const errors = []
  if (!isObject(policy) || policy.schemaVersion !== 1) errors.push('dogfood policy schemaVersion must be 1')
  for (const [field, minimum] of Object.entries({ minimumElapsedHours: 336, minimumCheckpointDays: 14, minimumRuns: 1 })) {
    if (!Number.isFinite(policy?.[field]) || policy[field] < minimum) errors.push(`dogfood policy ${field} must be at least ${minimum}`)
  }
  if (!Number.isFinite(policy?.maximumCheckpointGapHours) || policy.maximumCheckpointGapHours <= 0 || policy.maximumCheckpointGapHours > 30) {
    errors.push('dogfood policy maximumCheckpointGapHours must stay at or below 30')
  }
  return errors
}

export function validateDogfoodReport(report, policy = defaultPolicy) {
  const errors = validatePolicy(policy)
  exactKeys(report, reportKeys, 'dogfood report', errors)
  if (report?.schemaVersion !== 1) errors.push('dogfood report schemaVersion must be 1')
  if (typeof report?.candidateVersion !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9.+_-]{0,49}$/.test(report.candidateVersion)) errors.push('candidateVersion is invalid')
  if (typeof report?.candidateCommit !== 'string' || !/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(report.candidateCommit)) errors.push('candidateCommit must be a full Git or artifact digest')
  const startedAt = parseTimestamp(report?.startedAt, 'startedAt', errors)
  const completedAt = parseTimestamp(report?.completedAt, 'completedAt', errors)
  const elapsedHours = Number.isFinite(startedAt) && Number.isFinite(completedAt) ? (completedAt - startedAt) / 3_600_000 : 0
  if (elapsedHours < policy.minimumElapsedHours) errors.push(`dogfood elapsed ${elapsedHours.toFixed(2)} hours; at least ${policy.minimumElapsedHours} are required`)
  if (report?.fourteenDayAttestation !== true) errors.push('fourteenDayAttestation must be true')
  if (report?.operatorSignoff !== true) errors.push('operatorSignoff must be true')

  exactKeys(report?.totals, counterKeys, 'dogfood report totals', errors)
  for (const key of counterKeys) {
    if (!Number.isSafeInteger(report?.totals?.[key]) || report.totals[key] < 0) errors.push(`totals.${key} must be a non-negative safe integer`)
  }
  if (Number.isSafeInteger(report?.totals?.runsStarted) && Number.isSafeInteger(report?.totals?.runsTerminal) && report.totals.runsTerminal !== report.totals.runsStarted) {
    errors.push('dogfood totals contain non-terminal or double-counted Runs')
  }
  for (const key of ['p0', 'p1', 'dataLossIncidents', 'databaseCorruptionIncidents', 'lostTerminalEvents']) {
    if (report?.totals?.[key] !== 0) errors.push(`totals.${key} must be exactly 0`)
  }
  if (Number.isSafeInteger(report?.totals?.runsStarted) && report.totals.runsStarted < policy.minimumRuns) {
    errors.push(`dogfood has ${report.totals.runsStarted} Runs; at least ${policy.minimumRuns} are required`)
  }

  const sums = Object.fromEntries(counterKeys.map(key => [key, 0]))
  const checkpointTimes = []
  const days = new Set()
  if (!Array.isArray(report?.checkpoints)) {
    errors.push('dogfood report checkpoints must be an array')
  } else if (report.checkpoints.length > 1000) {
    errors.push('dogfood report checkpoints exceed 1000 rows')
  } else {
    for (const [index, checkpoint] of report.checkpoints.entries()) {
      const label = `checkpoints[${index}]`
      exactKeys(checkpoint, checkpointKeys, label, errors)
      const timestamp = parseTimestamp(checkpoint?.timestamp, `${label}.timestamp`, errors)
      if (Number.isFinite(timestamp)) {
        checkpointTimes.push(timestamp)
        days.add(new Date(timestamp).toISOString().slice(0, 10))
        if (Number.isFinite(startedAt) && timestamp < startedAt) errors.push(`${label} is before dogfood start`)
        if (Number.isFinite(completedAt) && timestamp > completedAt) errors.push(`${label} is after dogfood completion`)
      }
      if (checkpoint?.activeUseAttestation !== true) errors.push(`${label}.activeUseAttestation must be true`)
      for (const key of counterKeys) {
        if (!Number.isSafeInteger(checkpoint?.[key]) || checkpoint[key] < 0) errors.push(`${label}.${key} must be a non-negative safe integer`)
        else sums[key] += checkpoint[key]
      }
      if (Number.isSafeInteger(checkpoint?.runsStarted) && Number.isSafeInteger(checkpoint?.runsTerminal) && checkpoint.runsStarted !== checkpoint.runsTerminal) {
        errors.push(`${label} contains non-terminal or double-counted Runs`)
      }
      for (const key of ['p0', 'p1', 'dataLossIncidents', 'databaseCorruptionIncidents', 'lostTerminalEvents']) {
        if (checkpoint?.[key] !== 0) errors.push(`${label}.${key} must be exactly 0`)
      }
    }
  }
  checkpointTimes.sort((a, b) => a - b)
  if (days.size < policy.minimumCheckpointDays) errors.push(`dogfood has checkpoints on ${days.size} UTC days; at least ${policy.minimumCheckpointDays} are required`)
  const maximumGapMs = policy.maximumCheckpointGapHours * 3_600_000
  if (checkpointTimes.length) {
    if (Number.isFinite(startedAt) && checkpointTimes[0] - startedAt > maximumGapMs) errors.push('first checkpoint is too far after dogfood start')
    if (Number.isFinite(completedAt) && completedAt - checkpointTimes.at(-1) > maximumGapMs) errors.push('last checkpoint is too far before dogfood completion')
    for (let index = 1; index < checkpointTimes.length; index += 1) {
      if (checkpointTimes[index] - checkpointTimes[index - 1] > maximumGapMs) errors.push(`checkpoint gap exceeds ${policy.maximumCheckpointGapHours} hours`)
    }
  }
  for (const key of counterKeys) {
    if (Number.isSafeInteger(report?.totals?.[key]) && sums[key] !== report.totals[key]) errors.push(`totals.${key} does not equal checkpoint sum ${sums[key]}`)
  }
  return {
    errors,
    summary: {
      schemaVersion: 1,
      passed: errors.length === 0,
      candidateVersion: report?.candidateVersion,
      candidateCommit: report?.candidateCommit,
      elapsedHours,
      checkpointDays: days.size,
      runCount: Number.isSafeInteger(report?.totals?.runsStarted) ? report.totals.runsStarted : 0,
    },
  }
}

function main() {
  const reportPath = process.argv[2]
  if (!reportPath || process.argv.length > 3) {
    console.error('Usage: node scripts/check-dogfood-report.mjs <dogfood-report.json>')
    process.exit(2)
  }
  try {
    const report = JSON.parse(fs.readFileSync(path.resolve(reportPath), 'utf8'))
    const result = validateDogfoodReport(report)
    process.stdout.write(`${JSON.stringify(result.summary, null, 2)}\n`)
    if (result.errors.length) {
      console.error(result.errors.join('\n'))
      process.exit(1)
    }
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error))
    process.exit(1)
  }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) main()
