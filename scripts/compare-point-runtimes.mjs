import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const projects = ['node-typescript', 'go', 'php']
const regimes = ['cold', 'warm']
const metrics = ['cpuTimeMs', 'peakMemoryBytes', 'idleMemoryBytes', 'diskReadBytes', 'diskWriteBytes', 'storageBytes', 'networkBytes']
const quality = ['runtimeSecurity', 'mandatoryTests', 'independentBehavior', 'changeScope', 'completeAudit', 'correctVerdicts', 'deliveryVerified', 'manualCriteriaPreserved']
const pins = ['sourceDigest', 'criteriaDigest', 'commandsDigest', 'dependencyPlanDigest', 'imageDigest', 'environmentDigest', 'filePolicyDigest', 'networkPolicyDigest', 'resourceLimitsDigest', 'securityProfile']
const median = values => {
  const ordered = [...values].sort((a, b) => a - b)
  const index = Math.floor(ordered.length / 2)
  return ordered.length % 2 ? ordered[index] : (ordered[index - 1] + ordered[index]) / 2
}
const geometricMean = values => Math.exp(values.reduce((sum, value) => sum + Math.log(value), 0) / values.length)

function summarize(samples) {
  const result = { count: samples.length }
  for (const name of ['taskMs', ...metrics]) {
    const values = samples.map(sample => sample[name])
    result[name] = { median: median(values), min: Math.min(...values), max: Math.max(...values) }
  }
  return result
}

// Raw full-task measurements are required. Missing guest/engine/host overhead
// cannot silently become zero, and microbenchmarks cannot select a winner.
export function compareSamples(input) {
  if (input?.schema !== 1 || !Array.isArray(input.samples) || !input.baseline) throw new Error('Expected schema 1, baseline and raw samples')
  const configurations = [...new Set(input.samples.map(sample => sample.configuration))].sort()
  if (!configurations.includes(input.baseline)) throw new Error('Baseline samples are absent')
  configurations.splice(configurations.indexOf(input.baseline), 1)
  configurations.unshift(input.baseline)
  const summaries = {}
  const failures = {}
  const projectPins = new Map()
  for (const configuration of configurations) {
    summaries[configuration] = {}
    failures[configuration] = []
    const fail = message => { if (!failures[configuration].includes(message)) failures[configuration].push(message) }
    const samples = input.samples.filter(sample => sample.configuration === configuration)
    const identities = new Set()
    const engines = new Set(samples.map(sample => `${sample.engine}|${sample.engineVersion}|${sample.runtimeDigest}`))
    if (engines.size !== 1) fail('Engine/runtime changed within configuration')
    for (let sample of samples) {
      if (!sample.id || identities.has(sample.id)) fail('Trial identifiers must be unique')
      identities.add(sample.id)
      if (!projects.includes(sample.project) || !regimes.includes(sample.regime)) fail('Unknown project or regime')
      if (sample.kind !== 'full_quest' || sample.resourceCoverage !== 'whole_environment') fail('Whole-environment full-quest measurements required')
      if (sample.model !== 'Qwen3.8-27B' || !sample.route || sample.fallback !== false) fail('Model/route must be pinned without fallback')
      if (!sample.engine || !sample.engineVersion || !sample.runtimeDigest) fail('Runtime provenance missing')
      if (!sample.evidence || !sample.receiptId) fail('Raw evidence/receipt missing')
      for (const name of quality) if (sample.quality?.[name] !== true) fail(`Quality failed or unknown: ${name}`)
      for (const name of metrics) if (!Number.isFinite(sample[name]) || sample[name] < 0) fail(`Resource metric missing or invalid: ${name}`)
      if (!(sample.cpuTimeMs > 0) || !(sample.peakMemoryBytes > 0)) fail('CPU-time and peak memory must be measured and positive')
      const approved = Date.parse(sample.approvedAt)
      const delivered = Date.parse(sample.deliveredAt)
      if (!Number.isFinite(approved) || !Number.isFinite(delivered) || delivered <= approved || !Number.isFinite(sample.humanWaitMs) || sample.humanWaitMs < 0 || sample.humanWaitMs >= delivered - approved) {
        fail('Approval-to-receipt bounds/human wait missing or invalid')
      } else {
        sample = { ...sample, taskMs: delivered - approved - sample.humanWaitMs }
      }
      // Installation and user wait are separate raw measurements, not task cost.
      if (!Number.isFinite(sample.installationMs) || sample.installationMs < 0) fail('Installation timing missing')
      const signature = JSON.stringify([sample.model, sample.route, ...pins.map(name => sample.pins?.[name])])
      if (pins.some(name => !sample.pins?.[name])) fail('One or more comparison inputs are unpinned')
      const old = projectPins.get(sample.project)
      if (old && old !== signature) fail(`Comparison inputs differ: ${sample.project}`)
      else projectPins.set(sample.project, signature)
      const cell = `${sample.project}/${sample.regime}`
      const bucket = summaries[configuration][cell] ||= []
      bucket.push(sample)
    }
    for (const project of projects) for (const regime of regimes) {
      const cell = `${project}/${regime}`
      const bucket = summaries[configuration][cell] || []
      if (bucket.length < 3) fail(`At least three full trials required: ${cell}`)
      summaries[configuration][cell] = bucket.length ? summarize(bucket) : null
    }
  }
  if (failures[input.baseline].length) return { status: 'blocked', winner: null, failures, summaries, reason: 'Baseline incomplete or invalid' }
  const baselineSamples = input.samples.filter(sample => sample.configuration === input.baseline)
  if (baselineSamples.some(sample => sample.engine !== 'docker' || sample.storageMode !== 'bind' || sample.verifyMode !== 'shadow')) return { status: 'blocked', winner: null, failures, summaries, reason: 'Baseline must be Docker bind/shadow' }
  const candidates = []
  for (const configuration of configurations.filter(name => name !== input.baseline)) {
    const result = { configuration, failures: failures[configuration] }
    if (!result.failures.length) {
      const ratios = []
      const memory = []
      const cpu = []
      for (const project of projects) for (const regime of regimes) {
        const cell = `${project}/${regime}`
        const trial = summaries[configuration][cell]
        const baseline = summaries[input.baseline][cell]
        const ratio = trial.taskMs.median / baseline.taskMs.median
        if (ratio > 1.10) result.failures.push(`Task regression exceeds 10%: ${cell}`)
        if (trial.peakMemoryBytes.max > baseline.peakMemoryBytes.max * 1.10) result.failures.push(`Peak memory growth exceeds 10%: ${cell}`)
        if (trial.cpuTimeMs.median > baseline.cpuTimeMs.median * 1.10) result.failures.push(`CPU-time growth exceeds 10%: ${cell}`)
        if (regime === 'warm') {
          ratios.push(ratio)
          memory.push(trial.peakMemoryBytes.max / Math.max(1, baseline.peakMemoryBytes.max))
          cpu.push(trial.cpuTimeMs.median / Math.max(1, baseline.cpuTimeMs.median))
        }
      }
      result.relativeWarmTime = geometricMean(ratios)
      result.warmImprovement = 1 - result.relativeWarmTime
      result.relativePeakMemory = geometricMean(memory)
      result.relativeCPUTime = geometricMean(cpu)
      if (result.warmImprovement + 1e-12 < .30) result.failures.push('Warm full-task speedup is below 30%')
    }
    result.eligible = result.failures.length === 0
    candidates.push(result)
  }
  const eligible = candidates.filter(candidate => candidate.eligible).sort((a, b) => a.relativeWarmTime - b.relativeWarmTime)
  const fastest = eligible[0]
  const finalists = fastest ? eligible.filter(candidate => candidate.relativeWarmTime / fastest.relativeWarmTime < 1.05) : []
  finalists.sort((a, b) => a.relativePeakMemory - b.relativePeakMemory || a.relativeCPUTime - b.relativeCPUTime || a.relativeWarmTime - b.relativeWarmTime)
  return { status: finalists.length ? 'qualified' : 'experimental', winner: finalists[0]?.configuration || null, candidates, failures, summaries, defaults: 'bind/shadow; opt-in only' }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const file = process.argv[2]
  if (!file) throw new Error('Usage: node scripts/compare-point-runtimes.mjs <raw-samples.json>')
  const input = JSON.parse(fs.readFileSync(file, 'utf8'))
  // Do not emit provenance pointing to nonexistent evidence files.
  for (const sample of input.samples || []) {
    if (!sample.evidence || !fs.statSync(path.resolve(path.dirname(file), sample.evidence)).isFile()) throw new Error('Raw evidence file unavailable')
    const proof = JSON.parse(fs.readFileSync(path.resolve(path.dirname(file), sample.evidence), 'utf8'))
    const bundle = proof.evidence || proof
    if (bundle.deliveryReceipt?.id !== sample.receiptId || bundle.deliveryVerified !== true) throw new Error('Sample receipt does not match raw delivery evidence')
    if (!bundle.modelCalls?.length || bundle.modelCalls.some(call => call.model !== sample.model)) throw new Error('Raw model-call provenance is missing or differs')
  }
  console.log(JSON.stringify(compareSamples(input), null, 2))
}
