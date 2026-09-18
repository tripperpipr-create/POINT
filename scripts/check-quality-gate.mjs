import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const requiredAreas = [
  'sandbox', 'controlled-egress', 'supply-chain-evidence',
  'agent-skill-evaluation', 'module-boundaries', 'legacy-lifecycle',
  'release-recovery', 'client-surfaces', 'performance-slo', 'security-privacy',
  'documentation-contracts',
]
const requiredThreats = Array.from({ length: 15 }, (_, index) => `T${String(index + 1).padStart(2, '0')}`)
// Принятых остаточных рисков шесть. R6 жил в docs/threat-model.md, но в
// реестр попасть не мог: затвор требовал ровно R1-R5 и падал на любой
// попытке его внести. Документ и реестр расходились там, где реестр и
// заведён для того, чтобы они сходились.
const requiredRisks = Array.from({ length: 6 }, (_, index) => `R${index + 1}`)
const severities = new Set(['P0', 'P1', 'P2', 'P3'])
const statuses = new Set(['open', 'mitigating', 'accepted', 'closed'])

function sameMembers(actual, required) {
  return Array.isArray(actual)
    && actual.length === required.length
    && required.every(value => actual.includes(value))
}

export function validateQualityGate(registryOverride) {
  const errors = []
  const registryPath = path.join(root, 'distribution/quality-gate.json')
  let registry
  if (registryOverride === undefined) {
    try {
      registry = JSON.parse(fs.readFileSync(registryPath, 'utf8'))
    } catch (error) {
      return { errors: [`quality gate registry is unreadable: ${error.message}`] }
    }
  } else {
    registry = registryOverride
  }

  const appSource = fs.readFileSync(path.join(root, 'internal/app/app.go'), 'utf8')
  const appVersion = appSource.match(/const Version = "([^"]+)"/)?.[1]
  const frontendVersion = JSON.parse(fs.readFileSync(path.join(root, 'frontend/package.json'), 'utf8')).version
  const extensionVersion = JSON.parse(fs.readFileSync(path.join(root, 'vscode-extension/package.json'), 'utf8')).version
  if (registry.schemaVersion !== 1) errors.push('quality gate schemaVersion must be 1')
  if (!appVersion || registry.productVersion !== appVersion || appVersion !== frontendVersion || appVersion !== extensionVersion) {
    errors.push(`quality gate version drift: registry=${registry.productVersion}, core=${appVersion}, frontend=${frontendVersion}, extension=${extensionVersion}`)
  }
  if (!/^\d{4}-\d{2}-\d{2}$/.test(registry.reviewedAt || '') || Number.isNaN(Date.parse(`${registry.reviewedAt}T00:00:00Z`))) {
    errors.push('quality gate reviewedAt must be a valid ISO date')
  }
  for (const severity of severities) {
    if (!String(registry.severityPolicy?.[severity] || '').trim()) errors.push(`quality gate severityPolicy.${severity} is missing`)
  }
  if (!sameMembers(registry.reviewedAreas, requiredAreas)) errors.push('quality gate reviewedAreas does not cover the full production objective')
  if (!sameMembers(registry.reviewedThreats, requiredThreats)) errors.push('quality gate reviewedThreats must contain T01-T15 exactly')
  if (!sameMembers(registry.acceptedResidualRisks, requiredRisks)) errors.push('quality gate acceptedResidualRisks must contain R1-R6 exactly')

  const threatModel = fs.readFileSync(path.join(root, 'docs/threat-model.md'), 'utf8')
  for (const id of [...requiredThreats, ...requiredRisks]) {
    if (!new RegExp(`\\b${id}\\b`).test(threatModel)) errors.push(`quality gate references missing threat-model item ${id}`)
  }

  const defects = Array.isArray(registry.defects) ? registry.defects : []
  if (!Array.isArray(registry.defects)) errors.push('quality gate defects must be an array')
  const ids = new Set()
  for (const [index, defect] of defects.entries()) {
    const label = `quality gate defect ${index + 1}`
    if (!/^[A-Z][A-Z0-9_-]+$/.test(defect?.id || '')) errors.push(`${label}: stable id is missing`)
    else if (ids.has(defect.id)) errors.push(`${label}: duplicate id ${defect.id}`)
    else ids.add(defect.id)
    if (!severities.has(defect?.severity)) errors.push(`${label}: invalid severity ${defect?.severity}`)
    if (!statuses.has(defect?.status)) errors.push(`${label}: invalid status ${defect?.status}`)
    if (!String(defect?.title || '').trim()) errors.push(`${label}: title is missing`)
    if (!String(defect?.evidence || '').trim()) errors.push(`${label}: evidence is missing`)
    if (defect?.status !== 'closed' && (defect?.severity === 'P0' || defect?.severity === 'P1')) {
      errors.push(`${label}: release-blocking ${defect.severity} ${defect.id} is not closed`)
    }
    if (defect?.status === 'accepted' && !String(defect?.rationale || '').trim()) {
      errors.push(`${label}: accepted defect requires a rationale`)
    }
  }

  const evidence = Array.isArray(registry.evidence) ? registry.evidence : []
  if (!evidence.length) errors.push('quality gate evidence is empty')
  for (const referenced of evidence) {
    if (typeof referenced !== 'string' || path.isAbsolute(referenced) || referenced.includes('..')) {
      errors.push(`quality gate evidence path is unsafe: ${referenced}`)
    } else if (!fs.existsSync(path.join(root, referenced))) {
      errors.push(`quality gate evidence path is missing: ${referenced}`)
    }
  }

  return {
    errors,
    report: {
      qualityGate: errors.length ? 'failed' : 'ok',
      productVersion: registry.productVersion,
      reviewedAt: registry.reviewedAt,
      reviewedAreas: registry.reviewedAreas?.length || 0,
      reviewedThreats: registry.reviewedThreats?.length || 0,
      openP0P1: defects.filter(item => item?.status !== 'closed' && (item?.severity === 'P0' || item?.severity === 'P1')).length,
      trackedDefects: defects.length,
    },
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const result = validateQualityGate()
  if (result.errors.length) {
    console.error(`Quality gate failed (${result.errors.length}):`)
    for (const error of result.errors) console.error(`- ${error}`)
    process.exit(1)
  }
  console.log(JSON.stringify(result.report))
}
