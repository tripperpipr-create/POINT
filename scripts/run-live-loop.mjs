#!/usr/bin/env node
// Лёгкий живой прогон: одна правка в настоящем проекте, от реплики до зелёных
// тестов. Полигон для повседневной доводки — круг стоит минуты, а не часы,
// как приёмка MVP (scripts/run-v2-mvp-acceptance.mjs).
//
// Правила вердикта проверяются всегда; живой прогон включается POINT_LIVE_LOOP=1.
// Docker по умолчанию не используется: команды исполняет процесс Point, и это
// экономит 20–40 секунд на круге. Проверять полигон в песочнице — отдельный
// заход с --docker, раз в день.

import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const root = path.resolve(import.meta.dirname, '..')

// Ключ провайдера живёт в файле, а не в строке запуска: команда с ключом
// остаётся в истории оболочки и в журнале сессии, а файл .env.local закрыт
// правилом .env.* в .gitignore. Значения отсюда никуда не печатаются — в
// вывод идут только имена подхваченных переменных.
//
// Окружение старше файла: заданная снаружи переменная не перекрывается, иначе
// разовый запуск с другой моделью молча возвращался бы к записанной.
function loadLocalEnv(...names) {
  const loaded = []
  for (const name of names) {
    const file = path.join(root, name)
    if (!fs.existsSync(file)) continue
    for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
      const trimmed = line.trim()
      if (!trimmed || trimmed.startsWith('#')) continue
      const separator = trimmed.indexOf('=')
      if (separator < 1) continue
      const key = trimmed.slice(0, separator).trim()
      if (process.env[key] !== undefined && process.env[key] !== '') continue
      process.env[key] = trimmed.slice(separator + 1).trim().replace(/^["']|["']$/g, '')
      loaded.push(`${key} (${name})`)
    }
  }
  return loaded
}

const loadedEnv = loadLocalEnv('.env.local', '.env')
const live = process.env.POINT_LIVE_LOOP === '1'
const docker = process.argv.includes('--docker')
// Умолчание то же, что в internal/acceptance/live_loop_test.go. Две копии
// одного имени разошлись бы молча: обёртка сложила бы ledger под одним именем,
// а прогон записал бы его под другим, и вердикт читался бы из пустоты.
const model = process.env.POINT_LIVE_LOOP_MODEL || process.env.POINT_V2_MVP_MODEL || process.env.POINT_ACCEPTANCE_MODEL || 'Qwen3.6-35B-A3B'
const run = Number((process.argv.find(argument => argument.startsWith('--run=')) || '--run=1').split('=')[1] || 1)
const outDir = path.join(root, '.tmp')
fs.mkdirSync(outDir, { recursive: true })

function go(args, env = process.env) {
  execFileSync('go', args, { cwd: root, stdio: 'inherit', env })
}

console.log(`live-loop run=${run} live=${live} docker=${docker} model=${model}`)
if (loadedEnv.length > 0) console.log(`подхвачено из файла: ${loadedEnv.join(', ')}`)

// Вердикт проверяется без модели и без Docker: приёмка, которую никто не
// исполняет, пропускает ровно то, ради чего написана.
go(['test', './internal/acceptance', '-count=1', '-run', 'TestLiveLoopVerdictRejectsFalseCompletion'])

if (!live) {
  console.log('правила проверены. Живой прогон включается файлом .env.local в корне (он закрыт .gitignore):')
  console.log('')
  console.log('  POINT_LIVE_LOOP=1')
  console.log('  POINT_LLMUX_API_KEY=<ключ>')
  console.log('')
  console.log('Адрес шлюза и модель уже известны коду (llmux, Qwen3.6-35B-A3B); чтобы взять другие,')
  console.log('добавьте POINT_LLMUX_BASE_URL и POINT_LIVE_LOOP_MODEL туда же.')
  console.log('')
  console.log('Добавьте --docker, чтобы команды шли в песочницу. На локальном рантайме:')
  console.log('POINT_LIVE_LOOP_PROVIDER=ollama с POINT_OLLAMA_BASE_URL, ключ там не нужен.')
  process.exit(0)
}

const ledgerPath = path.join(outDir, `live-loop-${model.replace(/[^\w.-]+/g, '_')}-run${run}.jsonl`)
fs.rmSync(ledgerPath, { force: true })
const env = { ...process.env, POINT_LIVE_LOOP: '1', POINT_LIVE_LOOP_MODEL: model, POINT_LIVE_LOOP_RUN: String(run) }
if (docker) env.POINT_SANDBOX_BACKEND = 'docker'
let failed = false
try {
  go(['test', './internal/acceptance', '-count=1', '-timeout', '20m', '-run', 'TestLiveLoopScenario', '-v'], env)
} catch {
  failed = true
}

if (!fs.existsSync(ledgerPath)) {
  console.error(`live run wrote no ledger: ${ledgerPath}`)
  process.exit(1)
}
const row = JSON.parse(fs.readFileSync(ledgerPath, 'utf8').trim().split(/\r?\n/).filter(Boolean).pop())
console.log('')
console.log(`status=${row.status || 'not started'} seconds=${Math.round(row.seconds)} steps=${row.steps} requests=${row.modelRequests} interventions=${row.interventions}`)
console.log(`endpoint=${row.endpointPresent} test=${row.testPresent} testsGreen=${row.testsGreen} files=${(row.changedFiles || []).length}`)
if (row.toolsUsed?.length) console.log(`tools: ${row.toolsUsed.join(', ')}`)
if (row.firstFailure) console.error(`first failure: ${row.firstFailure}`)
if (row.testsGreen === false && row.testOutput) {
  console.error('--- go test ---')
  console.error(row.testOutput)
}
for (const violation of row.violations || []) console.error(`  · ${violation}`)

if (failed || (row.violations || []).length > 0) {
  console.error(`live loop run ${run} failed. Ledger: ${ledgerPath}`)
  process.exit(1)
}
console.log(`live loop run ${run} passed in ${Math.round(row.seconds)}s over ${row.steps} steps.`)
console.log('Полигон закрыт, когда два прогона подряд дают один и тот же вердикт — пусть даже отказ.')
