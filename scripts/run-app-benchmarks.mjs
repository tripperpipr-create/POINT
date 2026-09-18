#!/usr/bin/env node
// Records app-level Agent Hub acceptance without converting unavailable live
// runtimes into a PASS. Actual runs are launched from Command Center; this
// utility validates and summarizes their exported ledgers.

import fs from 'node:fs'
import path from 'node:path'

const root = path.resolve(import.meta.dirname, '..')
const spec = JSON.parse(fs.readFileSync(path.join(root, 'acceptance', 'app-benchmarks.json'), 'utf8'))
const input = process.argv.find(arg => arg.startsWith('--ledger='))?.slice('--ledger='.length)
const runtime = process.argv.find(arg => arg.startsWith('--runtime='))?.slice('--runtime='.length) || 'unconfigured'
const model = process.argv.find(arg => arg.startsWith('--model='))?.slice('--model='.length) || 'unconfigured'
const pass = Number(process.argv.find(arg => arg.startsWith('--pass='))?.slice('--pass='.length) || 1)

if (!Array.isArray(spec.cases) || spec.cases.length < 2) {
  throw new Error('app benchmark specification must contain at least two cases')
}

if (!input) {
  const outDir = path.join(root, '.tmp')
  fs.mkdirSync(outDir, { recursive: true })
  const safe = value => value.replace(/[^\w.-]+/g, '_')
  const target = path.join(outDir, `app-benchmark-${safe(runtime)}-${safe(model)}-pass${pass}.json`)
  const report = {
    schemaVersion: 1,
    runtime,
    model,
    pass,
    status: 'not_tested',
    reason: 'No exported live Command Center ledger was supplied.',
    cases: spec.cases.map(item => ({ id: item.id, status: 'not_tested' })),
  }
  fs.writeFileSync(target, JSON.stringify(report, null, 2) + '\n')
  console.log(`NOT TESTED: wrote ${target}`)
  process.exit(2)
}

const report = JSON.parse(fs.readFileSync(path.resolve(input), 'utf8'))
const expected = new Set(spec.cases.map(item => item.id))
for (const item of report.cases || []) expected.delete(item.id)
if (expected.size) throw new Error(`ledger is missing cases: ${[...expected].join(', ')}`)

const falseCompletions = (report.cases || []).filter(item => item.falseComplete).length
const boundaryViolations = (report.cases || []).reduce((sum, item) => sum + Number(item.boundaryViolations || 0), 0)
const passed = (report.cases || []).filter(item => item.status === 'passed').length
const rate = passed / spec.cases.length
console.log(JSON.stringify({ runtime: report.runtime, model: report.model, pass: report.pass, passed, total: spec.cases.length, rate, falseCompletions, boundaryViolations }))
if (rate < spec.shipGate.minimumCompletionRate || falseCompletions > 0 || boundaryViolations > 0) process.exit(1)
