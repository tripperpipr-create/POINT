import crypto from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const defaultPolicyPath = path.join(root, 'distribution', 'legacy-field-validation.json')

const isObject = value => value !== null && typeof value === 'object' && !Array.isArray(value)
const integers = ['started', 'completed', 'failed', 'cancelled', 'interrupted']
const topLevelKeys = [
  'schemaVersion', 'optInAggregateOnly', 'realProjectAttestation', 'projectNonce',
  'stackFamily', 'releaseWindow', 'runs', 'lifecycle', 'blockingDefects',
  'compatibilityTotals', 'compatibilityUsage',
]
const windowKeys = [
  'id', 'sequence', 'startedAt', 'completedAt', 'fullWindowAttestation',
  'candidateVersion', 'candidateCommit',
]
const blockingKeys = ['p0', 'p1']
const usageKeys = ['feature', 'applicationVersion', 'legacyVersion', 'count', 'firstSeen', 'lastSeen']

function exactKeys(value, expected, label, errors) {
  if (!isObject(value)) {
    errors.push(`${label} must be an object`)
    return false
  }
  const actual = Object.keys(value).sort()
  const wanted = [...expected].sort()
  const unexpected = actual.filter(key => !wanted.includes(key))
  const missing = wanted.filter(key => !actual.includes(key))
  if (unexpected.length) errors.push(`${label} contains forbidden fields: ${unexpected.join(', ')}`)
  if (missing.length) errors.push(`${label} is missing fields: ${missing.join(', ')}`)
  return unexpected.length === 0 && missing.length === 0
}

function parseTimestamp(value, label, errors) {
  if (typeof value !== 'string' || value.length > 40 || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) {
    errors.push(`${label} must be a bounded UTC RFC3339 timestamp`)
    return NaN
  }
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) errors.push(`${label} is not a valid timestamp`)
  return parsed
}

function boundedString(value, pattern, label, errors) {
  if (typeof value !== 'string' || !pattern.test(value)) {
    errors.push(`${label} has an invalid value`)
    return false
  }
  return true
}

function validatePolicy(policy) {
  const errors = []
  if (!isObject(policy) || policy.schemaVersion !== 1) errors.push('policy schemaVersion must be 1')
  for (const [field, minimum] of Object.entries({
    minimumProjects: 5,
    minimumStackFamilies: 3,
    minimumRuns: 200,
    minimumReleaseWindows: 2,
  })) {
    if (!Number.isInteger(policy?.[field]) || policy[field] < minimum) {
      errors.push(`policy ${field} must be at least ${minimum}`)
    }
  }
  for (const field of ['allowedStackFamilies', 'requiredLifecycleChecks', 'requiredCompatibilityFeatures']) {
    if (!Array.isArray(policy?.[field]) || !policy[field].length || new Set(policy[field]).size !== policy[field].length) {
      errors.push(`policy ${field} must be a non-empty unique array`)
    }
  }
  return errors
}

function validateEntry(entry, source, policy) {
  const errors = []
  exactKeys(entry, topLevelKeys, source, errors)
  if (entry?.schemaVersion !== 1) errors.push(`${source}.schemaVersion must be 1`)
  if (entry?.optInAggregateOnly !== true) errors.push(`${source} does not contain explicit aggregate-only opt-in`)
  if (entry?.realProjectAttestation !== true) errors.push(`${source} is not attested as a real project`)
  boundedString(entry?.projectNonce, /^[a-f0-9]{64}$/, `${source}.projectNonce`, errors)
  if (!policy.allowedStackFamilies.includes(entry?.stackFamily)) {
    errors.push(`${source}.stackFamily must be one of ${policy.allowedStackFamilies.join(', ')}`)
  }

  exactKeys(entry?.releaseWindow, windowKeys, `${source}.releaseWindow`, errors)
  const window = entry?.releaseWindow || {}
  boundedString(window.id, /^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$/, `${source}.releaseWindow.id`, errors)
  if (!Number.isInteger(window.sequence) || window.sequence < 1 || window.sequence > 1000) {
    errors.push(`${source}.releaseWindow.sequence must be an integer from 1 to 1000`)
  }
  const startedAt = parseTimestamp(window.startedAt, `${source}.releaseWindow.startedAt`, errors)
  const completedAt = parseTimestamp(window.completedAt, `${source}.releaseWindow.completedAt`, errors)
  if (Number.isFinite(startedAt) && Number.isFinite(completedAt) && completedAt <= startedAt) {
    errors.push(`${source}.releaseWindow must end after it starts`)
  }
  if (window.fullWindowAttestation !== true) errors.push(`${source} is not attested as a complete release window`)
  boundedString(window.candidateVersion, /^[A-Za-z0-9][A-Za-z0-9.+_-]{0,49}$/, `${source}.releaseWindow.candidateVersion`, errors)
  boundedString(window.candidateCommit, /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/, `${source}.releaseWindow.candidateCommit`, errors)

  exactKeys(entry?.runs, integers, `${source}.runs`, errors)
  for (const field of integers) {
    if (!Number.isSafeInteger(entry?.runs?.[field]) || entry.runs[field] < 0) {
      errors.push(`${source}.runs.${field} must be a non-negative safe integer`)
    }
  }
  if (integers.every(field => Number.isSafeInteger(entry?.runs?.[field]))) {
    const terminal = entry.runs.completed + entry.runs.failed + entry.runs.cancelled + entry.runs.interrupted
    if (entry.runs.started < 1) errors.push(`${source} must contain at least one real Run`)
    if (terminal !== entry.runs.started) errors.push(`${source} has ${entry.runs.started - terminal} non-terminal or double-counted Runs`)
  }

  exactKeys(entry?.lifecycle, policy.requiredLifecycleChecks, `${source}.lifecycle`, errors)
  for (const check of policy.requiredLifecycleChecks) {
    if (entry?.lifecycle?.[check] !== true) errors.push(`${source}.lifecycle.${check} must be true`)
  }
  exactKeys(entry?.blockingDefects, blockingKeys, `${source}.blockingDefects`, errors)
  for (const severity of blockingKeys) {
    if (!Number.isSafeInteger(entry?.blockingDefects?.[severity]) || entry.blockingDefects[severity] !== 0) {
      errors.push(`${source}.blockingDefects.${severity} must be exactly 0`)
    }
  }

  exactKeys(entry?.compatibilityTotals, policy.requiredCompatibilityFeatures, `${source}.compatibilityTotals`, errors)
  for (const feature of policy.requiredCompatibilityFeatures) {
    if (!Number.isSafeInteger(entry?.compatibilityTotals?.[feature]) || entry.compatibilityTotals[feature] < 0) {
      errors.push(`${source}.compatibilityTotals.${feature} must be a non-negative safe integer`)
    }
  }
  if (!Array.isArray(entry?.compatibilityUsage)) {
    errors.push(`${source}.compatibilityUsage must be an array`)
  } else if (entry.compatibilityUsage.length > 200) {
    errors.push(`${source}.compatibilityUsage exceeds 200 exact-version rows`)
  } else {
    const sums = Object.fromEntries(policy.requiredCompatibilityFeatures.map(feature => [feature, 0]))
    const seen = new Set()
    for (const [index, row] of entry.compatibilityUsage.entries()) {
      const label = `${source}.compatibilityUsage[${index}]`
      exactKeys(row, usageKeys, label, errors)
      if (!policy.requiredCompatibilityFeatures.includes(row?.feature)) errors.push(`${label}.feature is unknown`)
      boundedString(row?.applicationVersion, /^[A-Za-z0-9][A-Za-z0-9.+_-]{0,49}$/, `${label}.applicationVersion`, errors)
      boundedString(row?.legacyVersion, /^[A-Za-z0-9][A-Za-z0-9.+_-]{0,99}$/, `${label}.legacyVersion`, errors)
      if (!Number.isSafeInteger(row?.count) || row.count <= 0) errors.push(`${label}.count must be a positive safe integer`)
      const firstSeen = parseTimestamp(row?.firstSeen, `${label}.firstSeen`, errors)
      const lastSeen = parseTimestamp(row?.lastSeen, `${label}.lastSeen`, errors)
      if (Number.isFinite(firstSeen) && Number.isFinite(lastSeen) && lastSeen < firstSeen) errors.push(`${label} has inverted time bounds`)
      const key = `${row?.feature}\u0000${row?.applicationVersion}\u0000${row?.legacyVersion}`
      if (seen.has(key)) errors.push(`${label} duplicates an exact-version row`)
      seen.add(key)
      if (Object.hasOwn(sums, row?.feature) && Number.isSafeInteger(row?.count) && row.count > 0) sums[row.feature] += row.count
    }
    for (const feature of policy.requiredCompatibilityFeatures) {
      if (Number.isSafeInteger(entry?.compatibilityTotals?.[feature]) && sums[feature] !== entry.compatibilityTotals[feature]) {
        errors.push(`${source}.compatibilityTotals.${feature} does not equal exact-version row sum ${sums[feature]}`)
      }
    }
  }
  return errors
}

export function validateLegacyFieldReport(report, policy, source = '<report>') {
  return validatePolicy(policy).concat(validateEntry(report, source, policy))
}

export function validateLegacyFieldValidation(entries, policy) {
  const errors = validatePolicy(policy)
  const normalized = Array.isArray(entries) ? entries : []
  if (!Array.isArray(entries)) errors.push('field evidence must be an array of parsed reports')
  const projects = new Map()
  const windows = new Map()
  const seenProjectWindow = new Set()
  let totalRuns = 0

  for (const item of normalized) {
    const source = item?.source || '<unknown>'
    const entry = item?.report
    errors.push(...validateEntry(entry, source, policy))
    if (!isObject(entry)) continue
    const projectNonce = entry.projectNonce
    const window = entry.releaseWindow
    if (/^[a-f0-9]{64}$/.test(projectNonce || '') && isObject(window) && typeof window.id === 'string') {
      const projectWindowKey = `${projectNonce}\u0000${window.id}`
      if (seenProjectWindow.has(projectWindowKey)) errors.push(`${source} duplicates project/window evidence`)
      seenProjectWindow.add(projectWindowKey)
      if (projects.has(projectNonce) && projects.get(projectNonce) !== entry.stackFamily) {
        errors.push(`${source} changes stackFamily for one project nonce`)
      }
      projects.set(projectNonce, entry.stackFamily)

      const canonicalWindow = JSON.stringify(window)
      if (windows.has(window.id) && windows.get(window.id).canonical !== canonicalWindow) {
        errors.push(`${source} disagrees with another report about release window ${window.id}`)
      } else if (!windows.has(window.id)) {
        windows.set(window.id, { canonical: canonicalWindow, ...window })
      }
    }
    if (Number.isSafeInteger(entry.runs?.started) && entry.runs.started >= 0) totalRuns += entry.runs.started
  }

  const stackFamilies = new Set(projects.values())
  if (projects.size < policy.minimumProjects) errors.push(`field evidence has ${projects.size} projects; at least ${policy.minimumProjects} are required`)
  if (stackFamilies.size < policy.minimumStackFamilies) errors.push(`field evidence has ${stackFamilies.size} stack families; at least ${policy.minimumStackFamilies} are required`)
  if (windows.size < policy.minimumReleaseWindows) errors.push(`field evidence has ${windows.size} release windows; at least ${policy.minimumReleaseWindows} are required`)
  if (windows.size > policy.minimumReleaseWindows) errors.push(`field evidence must contain exactly the ${policy.minimumReleaseWindows} consecutive release windows under review`)
  if (totalRuns < policy.minimumRuns) errors.push(`field evidence has ${totalRuns} Runs; at least ${policy.minimumRuns} are required`)

  const orderedWindows = [...windows.values()].sort((a, b) => a.sequence - b.sequence)
  if (orderedWindows.length) {
    for (let index = 0; index < orderedWindows.length; index += 1) {
      if (orderedWindows[index].sequence !== index + 1) errors.push('release window sequence must be exactly 1, 2 with no gap')
      if (index > 0 && Date.parse(orderedWindows[index].startedAt) < Date.parse(orderedWindows[index - 1].completedAt)) {
        errors.push(`release windows ${orderedWindows[index - 1].id} and ${orderedWindows[index].id} overlap`)
      }
    }
  }
  for (const projectNonce of projects.keys()) {
    for (const window of orderedWindows) {
      if (!seenProjectWindow.has(`${projectNonce}\u0000${window.id}`)) {
        errors.push(`one attested project is missing release window ${window.id}`)
      }
    }
  }

  const compatibilityByWindow = Object.fromEntries(orderedWindows.map(window => [
    window.id,
    Object.fromEntries(policy.requiredCompatibilityFeatures.map(feature => [feature, 0])),
  ]))
  for (const { report } of normalized) {
    if (!compatibilityByWindow[report?.releaseWindow?.id]) continue
    for (const feature of policy.requiredCompatibilityFeatures) {
      const count = report?.compatibilityTotals?.[feature]
      if (Number.isSafeInteger(count) && count >= 0) compatibilityByWindow[report.releaseWindow.id][feature] += count
    }
  }
  const removalEligibleFeatures = policy.requiredCompatibilityFeatures.filter(feature => orderedWindows.length === policy.minimumReleaseWindows && orderedWindows.every(window => compatibilityByWindow[window.id][feature] === 0))
  const reportDigest = crypto.createHash('sha256').update(JSON.stringify(normalized.map(item => item.report))).digest('hex')
  return {
    errors,
    summary: {
      schemaVersion: 1,
      passed: errors.length === 0,
      aggregateOnly: true,
      projectCount: projects.size,
      stackFamilies: [...stackFamilies].sort(),
      releaseWindows: orderedWindows.map(window => ({ id: window.id, sequence: window.sequence, startedAt: window.startedAt, completedAt: window.completedAt, candidateVersion: window.candidateVersion, candidateCommit: window.candidateCommit })),
      runCount: totalRuns,
      compatibilityByWindow,
      removalEligibleFeatures,
      inputDigest: reportDigest,
    },
  }
}

export function loadLegacyFieldEvidence(evidenceDirectory, policyPath = defaultPolicyPath) {
  const policy = JSON.parse(fs.readFileSync(policyPath, 'utf8'))
  const absolute = path.resolve(evidenceDirectory)
  if (!fs.existsSync(absolute) || !fs.statSync(absolute).isDirectory()) {
    throw new Error(`legacy field evidence directory is missing: ${absolute}`)
  }
  const files = fs.readdirSync(absolute, { withFileTypes: true })
    .filter(entry => entry.isFile() && entry.name.endsWith('.json'))
    .map(entry => entry.name)
    .sort()
  if (!files.length) throw new Error(`legacy field evidence directory contains no JSON reports: ${absolute}`)
  const entries = files.map(file => ({
    source: file,
    report: JSON.parse(fs.readFileSync(path.join(absolute, file), 'utf8')),
  }))
  return { entries, policy, absolute }
}

function main() {
  const evidenceDirectory = process.argv[2]
  if (!evidenceDirectory || process.argv.length > 3) {
    console.error('Usage: node scripts/check-legacy-field-validation.mjs <evidence-directory>')
    process.exit(2)
  }
  try {
    const { entries, policy } = loadLegacyFieldEvidence(evidenceDirectory)
    const result = validateLegacyFieldValidation(entries, policy)
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
