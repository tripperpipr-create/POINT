#!/usr/bin/env node
// Live PHP URL-intake benchmark for Agent Hub. Opt-in via POINT_PHP_INTAKE_LIVE=1.
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const passArg = process.argv.find((a) => a.startsWith('--pass='))
const pass = process.env.POINT_PHP_INTAKE_PASS || passArg?.split('=')[1] || '1'
const live = process.env.POINT_PHP_INTAKE_LIVE === '1'

function run(args, env = {}) {
  return new Promise((resolve) => {
    const child = spawn('go', args, {
      cwd: root,
      env: { ...process.env, ...env },
      stdio: 'inherit',
      windowsHide: false,
    })
    child.on('error', (err) => {
      console.error(err.message)
      resolve(1)
    })
    child.on('close', (code) => resolve(code ?? 1))
  })
}

if (passArg && !live) {
  console.error('POINT_PHP_INTAKE_LIVE=1 is required with --pass= (structural-only mode is not a live PASS)')
  process.exit(2)
}

async function main() {
  if (!live) {
    const status = await run(['test', './internal/acceptance', '-count=1', '-run', 'TestPHPIntakeStructural|TestAppBenchmarkMatrixSpec'])
    console.log(status === 0
      ? 'Structural PHP intake/matrix checks passed. Ship blocked until POINT_PHP_INTAKE_LIVE=1 ×2 verified completed.'
      : 'Structural checks failed.')
    process.exit(status)
  }

  const model = process.env.POINT_PHP_INTAKE_MODEL || process.env.POINT_ACCEPTANCE_MODEL || 'Qwen3.6-35B-A3B'
  const apiKey = process.env.POINT_LLMUX_API_KEY || process.env.POINT_OPENAI_API_KEY || process.env.OPENAI_API_KEY || ''
  if (!apiKey) {
    console.error('Set POINT_LLMUX_API_KEY to the LLMux token already stored in Point SecretStorage')
    process.exit(2)
  }
  if (process.env.POINT_SANDBOX_BACKEND !== 'docker') {
    console.error('POINT_SANDBOX_BACKEND=docker is required')
    process.exit(2)
  }
  if (!process.env.POINT_SANDBOX_IMAGE) {
    process.env.POINT_SANDBOX_IMAGE = 'point-agent-sandbox-php:1.3.1'
  }
  if (!process.env.POINT_SANDBOX_REQUIRE_STRONG) {
    process.env.POINT_SANDBOX_REQUIRE_STRONG = 'true'
  }
  if (!process.env.POINT_PHP_INTAKE_BUDGET_TOKENS) {
    process.env.POINT_PHP_INTAKE_BUDGET_TOKENS = '5000000'
  }

  const status = await run(
    ['test', './internal/acceptance', '-count=1', '-v', '-run', 'TestPHPIntakeLiveBenchmark|TestPHPIntakeStructural', '-timeout', '2h30m'],
    {
      POINT_PHP_INTAKE_LIVE: '1',
      POINT_PHP_INTAKE_PASS: String(pass),
      POINT_PHP_INTAKE_MODEL: model,
      POINT_LLMUX_API_KEY: apiKey,
      POINT_SANDBOX_IMAGE: process.env.POINT_SANDBOX_IMAGE,
      POINT_SANDBOX_REQUIRE_STRONG: process.env.POINT_SANDBOX_REQUIRE_STRONG,
      POINT_PHP_INTAKE_BUDGET_TOKENS: process.env.POINT_PHP_INTAKE_BUDGET_TOKENS,
    },
  )
  const ledger = path.join(root, '.tmp', `php-intake-pass${pass}.json`)
  if (status !== 0) {
    process.exit(status)
  }
  if (!fs.existsSync(ledger)) {
    console.error(`missing ledger ${ledger}`)
    process.exit(1)
  }
  const body = JSON.parse(fs.readFileSync(ledger, 'utf8'))
  const required = ['runId', 'questId', 'intakeId', 'implementationDigest', 'fixtureURL', 'workspaceRevision', 'criteria', 'status']
  for (const key of required) {
    if (body[key] === undefined || body[key] === null || body[key] === '') {
      console.error(`ledger missing ${key}`)
      process.exit(1)
    }
  }
  if (body.status !== 'completed') {
    console.error(`ledger status ${body.status} is not a PASS`)
    process.exit(1)
  }
  if (String(pass) === '2') {
    const first = path.join(root, '.tmp', 'php-intake-pass1.json')
    if (!fs.existsSync(first)) {
      console.error('pass=2 requires a fresh pass1 ledger on the same implementationDigest')
      process.exit(1)
    }
    const prior = JSON.parse(fs.readFileSync(first, 'utf8'))
    if (prior.implementationDigest !== body.implementationDigest) {
      console.error('pass1 and pass2 implementationDigest differ; restart the series after a code change')
      process.exit(1)
    }
  }
  console.log(`Live PHP intake pass ${pass} ledger: ${ledger}`)
  process.exit(0)
}

main()
