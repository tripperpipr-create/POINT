#!/usr/bin/env node
// MVP acceptance for the v2 contract: one production-shaped application,
// delivered and proven by its own approved completion profile.
// Rules run always; the live scenario is opt-in via POINT_V2_MVP_LIVE=1.
// See docs/AGENT-HUB-V2-MVP.md.

import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const root = path.resolve(import.meta.dirname, '..')
const live = process.env.POINT_V2_MVP_LIVE === '1'
const model = process.env.POINT_V2_MVP_MODEL || process.env.POINT_ACCEPTANCE_MODEL || 'unconfigured'
const run = Number((process.argv.find(argument => argument.startsWith('--run=')) || '--run=1').split('=')[1] || 1)
const outDir = path.join(root, '.tmp')
fs.mkdirSync(outDir, { recursive: true })

function go(args, env = process.env) {
  execFileSync('go', args, { cwd: root, stdio: 'inherit', env })
}

console.log(`v2-mvp acceptance run=${run} live=${live} model=${model}`)

// The rules that decide the verdict are checked without a model or Docker, so a
// broken acceptance cannot pass by never being executed.
go(['test', './internal/acceptance', './internal/domain', './internal/app', '-count=1', '-run',
  'TestWorkOrderMVPVerdictRejectsFalseCompletion|TestWorkOrderEvidenceStatus|TestCompletionProfile|TestCompletionCheck|TestDeliveredResult|TestMasterProposes|TestShellRunner|TestInterruptedQuest'])

if (!live) {
  console.log('rules verified; set POINT_V2_MVP_LIVE=1 with POINT_V2_MVP_MODEL, a base URL, an API key')
  console.log('and POINT_SANDBOX_BACKEND=docker to run the live scenario. MVP stays open until it passes twice.')
  console.log('On a local runtime: POINT_V2_MVP_PROVIDER=ollama with POINT_OLLAMA_BASE_URL; no API key is needed there.')
  process.exit(0)
}

if (model === 'unconfigured') {
  console.error('POINT_V2_MVP_LIVE=1 requires POINT_V2_MVP_MODEL or POINT_ACCEPTANCE_MODEL')
  process.exit(2)
}

const ledgerPath = path.join(outDir, `v2-mvp-${model.replace(/[^\w.-]+/g, '_')}-run${run}.jsonl`)
fs.rmSync(ledgerPath, { force: true })
let failed = false
try {
  go(['test', './internal/acceptance', '-count=1', '-timeout', '180m', '-run', 'TestWorkOrderMVPLiveScenario', '-v'],
    { ...process.env, POINT_V2_MVP_LIVE: '1', POINT_V2_MVP_MODEL: model, POINT_V2_MVP_RUN: String(run) })
} catch {
  failed = true
}

if (!fs.existsSync(ledgerPath)) {
  console.error(`live run wrote no ledger: ${ledgerPath}`)
  process.exit(1)
}
const row = JSON.parse(fs.readFileSync(ledgerPath, 'utf8').trim().split(/\r?\n/).filter(Boolean).pop())
console.log(`status=${row.status} seconds=${Math.round(row.seconds)} interventions=${row.interventions} checks=${row.profileChecks}`)
if (row.falseComplete) {
  console.error('FALSE COMPLETION: quest reported completed while the acceptance found violations')
}
for (const violation of row.violations || []) console.error(`  · ${violation}`)
if (failed || (row.violations || []).length > 0) {
  console.error(`MVP run ${run} failed. Ledger: ${ledgerPath}`)
  process.exit(1)
}
console.log(`MVP run ${run} passed: ${row.deliveredUrl || 'no URL'} servicesUp=${row.servicesUp}`)
console.log('MVP closes after two consecutive passing runs with one approval each.')
