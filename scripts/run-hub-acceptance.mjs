#!/usr/bin/env node
// Structural + live harness for the 2×10 Agent Hub acceptance suite.
// Live model execution is opt-in via POINT_ACCEPTANCE_LIVE=1.

import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const root = path.resolve(import.meta.dirname, '..')
const pass = Number((process.argv.find(arg => arg.startsWith('--pass=')) || '--pass=1').split('=')[1] || 1)
const live = process.env.POINT_ACCEPTANCE_LIVE === '1'
const model = process.env.POINT_ACCEPTANCE_MODEL || process.env.POINT_INTAKE_TEST_MODEL || 'unconfigured'
const outDir = path.join(root, '.tmp')
fs.mkdirSync(outDir, { recursive: true })

const tasks = [
  { id: 1, kind: 'read', title: 'Summarize task_brief.go' },
  { id: 2, kind: 'bug', title: 'Fix fixture bug' },
  { id: 3, kind: 'test', title: 'WithEffectiveModel test' },
  { id: 4, kind: 'cross-file', title: 'Rename helper across packages' },
  { id: 5, kind: 'review', title: 'Secret leak review' },
  { id: 6, kind: 'recover', title: 'Pause and extend' },
  { id: 7, kind: 'sequential', title: 'Write then verify handoff' },
  { id: 8, kind: 'parallel', title: 'Parallel writers + merge' },
  { id: 9, kind: 'precise', title: 'Clamp function only' },
  { id: 10, kind: 'project', title: 'Broad app stays discussion' },
]

function run(cmd, args, cwd = root, env = process.env) {
  execFileSync(cmd, args, { cwd, stdio: 'inherit', env })
}

console.log(`hub-acceptance pass=${pass} live=${live} model=${model}`)
run('go', ['test', './internal/app', './internal/agent', './internal/flowruntime', './internal/orchestrator', './internal/domain', './internal/acceptance', '-count=1', '-run', 'TestReplanQuest|TestReviseActive|TestWithEffectiveModel|TestProjectTaskFreezes|TestRunSwitchesToFallback|TestVerifyInputsRequires|TestMasterRoster|TestVerificationFailure|TestHubAcceptanceFixturesPresent|TestQuestOutcomeRequiresMerged'])

const ledgerPath = path.join(outDir, `hub-acceptance-${model.replace(/[^\w.-]+/g, '_')}-pass${pass}.jsonl`)
if (!live) {
  const structural = tasks.map(task => ({
    task: task.id,
    kind: task.kind,
    title: task.title,
    ok: null,
    seconds: null,
    tokensIn: null,
    tokensOut: null,
    interventions: null,
    falseComplete: null,
    notes: 'structural gate only; set POINT_ACCEPTANCE_LIVE=1 and POINT_ACCEPTANCE_MODEL for model runs',
  }))
  fs.writeFileSync(ledgerPath, structural.map(row => JSON.stringify(row)).join('\n') + '\n')
  console.log(`wrote structural ledger ${ledgerPath}`)
  console.log('Ship blocked until live 2×10 passes (≥8/10 each). See docs/AGENT-HUB-ACCEPTANCE-2x10.md')
  process.exit(0)
}

if (!model || model === 'unconfigured') {
  console.error('POINT_ACCEPTANCE_LIVE=1 requires POINT_ACCEPTANCE_MODEL or POINT_INTAKE_TEST_MODEL')
  process.exit(2)
}

const env = {
  ...process.env,
  POINT_ACCEPTANCE_LIVE: '1',
  POINT_ACCEPTANCE_MODEL: model,
  POINT_ACCEPTANCE_PASS: String(pass),
  POINT_INTAKE_TEST_MODEL: process.env.POINT_INTAKE_TEST_MODEL || model,
}
console.log('running live suite via go test ./internal/acceptance -run TestHubAcceptanceLiveSuite')
try {
  run('go', ['test', './internal/acceptance', '-count=1', '-timeout', '45m', '-run', 'TestHubAcceptanceLiveSuite', '-v'], root, env)
} catch {
  console.error('live acceptance failed; inspect ledger under .tmp/')
  process.exit(1)
}

if (!fs.existsSync(ledgerPath)) {
  console.error(`expected ledger missing: ${ledgerPath}`)
  process.exit(1)
}
const lines = fs.readFileSync(ledgerPath, 'utf8').trim().split(/\r?\n/).filter(Boolean)
const parsed = lines.map(line => JSON.parse(line))
const okCount = parsed.filter(row => row.ok === true).length
console.log(`live ledger ${ledgerPath}: ${okCount}/${parsed.length} ok`)
if (okCount < 8) {
  console.error('Ship blocked: need ≥8/10')
  process.exit(1)
}
console.log(`live pass ${pass} threshold met (≥8/10)`)
