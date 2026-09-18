import fs from 'node:fs'
import path from 'node:path'
import { validateLegacyFieldReport } from './check-legacy-field-validation.mjs'

const root = path.resolve(import.meta.dirname, '..')
const policy = JSON.parse(fs.readFileSync(path.join(root, 'distribution', 'legacy-field-validation.json'), 'utf8'))
const metadataKeys = [
  'schemaVersion', 'optInAggregateOnly', 'realProjectAttestation', 'projectNonce',
  'stackFamily', 'releaseWindow', 'runs', 'lifecycle', 'blockingDefects',
]
const statisticKeys = ['feature', 'applicationVersion', 'legacyVersion', 'count', 'firstSeen', 'lastSeen']

const isObject = value => value !== null && typeof value === 'object' && !Array.isArray(value)

function exactKeys(value, expected, label) {
  if (!isObject(value)) throw new Error(`${label} must be an object`)
  const actual = Object.keys(value)
  const unexpected = actual.filter(key => !expected.includes(key))
  const missing = expected.filter(key => !actual.includes(key))
  if (unexpected.length) throw new Error(`${label} contains forbidden fields: ${unexpected.join(', ')}`)
  if (missing.length) throw new Error(`${label} is missing fields: ${missing.join(', ')}`)
}

function compatibilityRows(snapshot, label) {
  const rows = Array.isArray(snapshot) ? snapshot : snapshot?.compatibilityUsage
  if (!Array.isArray(rows)) throw new Error(`${label} must be a Statistics response or compatibilityUsage array`)
  if (rows.length > 200) throw new Error(`${label} exceeds 200 exact-version rows`)
  const result = new Map()
  for (const [index, row] of rows.entries()) {
    if (!isObject(row)) throw new Error(`${label}[${index}] must be an object`)
    for (const key of statisticKeys) {
      if (!Object.hasOwn(row, key)) throw new Error(`${label}[${index}] is missing ${key}`)
    }
    if (!policy.requiredCompatibilityFeatures.includes(row.feature)) throw new Error(`${label}[${index}] has unknown feature ${row.feature}`)
    if (typeof row.applicationVersion !== 'string' || typeof row.legacyVersion !== 'string') throw new Error(`${label}[${index}] has invalid version attribution`)
    if (!Number.isSafeInteger(row.count) || row.count < 0) throw new Error(`${label}[${index}].count must be a non-negative safe integer`)
    const key = `${row.feature}\u0000${row.applicationVersion}\u0000${row.legacyVersion}`
    if (result.has(key)) throw new Error(`${label}[${index}] duplicates an exact-version row`)
    result.set(key, row)
  }
  return result
}

function buildReport(metadata, startSnapshot, endSnapshot) {
  exactKeys(metadata, metadataKeys, 'metadata')
  const start = compatibilityRows(startSnapshot, 'start compatibility snapshot')
  const end = compatibilityRows(endSnapshot, 'end compatibility snapshot')
  const compatibilityUsage = []
  const compatibilityTotals = Object.fromEntries(policy.requiredCompatibilityFeatures.map(feature => [feature, 0]))
  for (const [key, endRow] of end) {
    const startCount = start.get(key)?.count || 0
    if (endRow.count < startCount) throw new Error(`compatibility counter decreased for ${endRow.feature}/${endRow.applicationVersion}/${endRow.legacyVersion}`)
    const count = endRow.count - startCount
    if (!count) continue
    compatibilityTotals[endRow.feature] += count
    compatibilityUsage.push({
      feature: endRow.feature,
      applicationVersion: endRow.applicationVersion,
      legacyVersion: endRow.legacyVersion,
      count,
      firstSeen: endRow.firstSeen,
      lastSeen: endRow.lastSeen,
    })
  }
  for (const [key, startRow] of start) {
    if (!end.has(key) && startRow.count > 0) throw new Error(`compatibility row disappeared for ${startRow.feature}/${startRow.applicationVersion}/${startRow.legacyVersion}`)
  }
  compatibilityUsage.sort((left, right) => left.feature.localeCompare(right.feature) || left.applicationVersion.localeCompare(right.applicationVersion) || left.legacyVersion.localeCompare(right.legacyVersion))
  return { ...metadata, compatibilityTotals, compatibilityUsage }
}

function main() {
  const [metadataPath, startPath, endPath, outputPath] = process.argv.slice(2)
  if (!metadataPath || !startPath || !endPath || !outputPath || process.argv.length !== 6) {
    console.error('Usage: node scripts/create-legacy-field-report.mjs <metadata.json> <start-statistics.json> <end-statistics.json> <output.json>')
    process.exit(2)
  }
  try {
    const output = path.resolve(outputPath)
    if (fs.existsSync(output)) throw new Error(`refusing to overwrite existing field report: ${output}`)
    const metadata = JSON.parse(fs.readFileSync(path.resolve(metadataPath), 'utf8'))
    const startSnapshot = JSON.parse(fs.readFileSync(path.resolve(startPath), 'utf8'))
    const endSnapshot = JSON.parse(fs.readFileSync(path.resolve(endPath), 'utf8'))
    const report = buildReport(metadata, startSnapshot, endSnapshot)
    const errors = validateLegacyFieldReport(report, policy, path.basename(output))
    if (errors.length) throw new Error(errors.join('\n'))
    fs.mkdirSync(path.dirname(output), { recursive: true })
    fs.writeFileSync(output, `${JSON.stringify(report, null, 2)}\n`, { encoding: 'utf8', flag: 'wx', mode: 0o600 })
    process.stdout.write(`${JSON.stringify({ output, compatibilityTotals: report.compatibilityTotals }, null, 2)}\n`)
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error))
    process.exit(1)
  }
}

main()
