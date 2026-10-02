import assert from 'node:assert/strict'
import { compareSamples } from './compare-point-runtimes.mjs'

function fixture() {
  const samples = []
  for (const configuration of ['docker-bind', 'moby', 'podman']) for (const project of ['node-typescript', 'go', 'php']) for (const regime of ['cold', 'warm']) for (let trial = 1; trial <= 3; trial++) {
    const duration = configuration === 'docker-bind' || regime === 'cold' ? 10000 : configuration === 'moby' ? 6500 : 6700
    samples.push({
      id: `${configuration}/${project}/${regime}/${trial}`, configuration, project, regime,
      kind: 'full_quest', resourceCoverage: 'whole_environment', engine: configuration === 'docker-bind' ? 'docker' : configuration,
      engineVersion: '1.0.0', runtimeDigest: 'test-pin', storageMode: configuration === 'docker-bind' ? 'bind' : 'volume', verifyMode: 'shadow',
      model: 'Qwen3.8-27B', route: 'fixed-route', fallback: false, receiptId: 'fixture-receipt', evidence: 'fixture-only.json',
      approvedAt: '2026-10-02T00:00:00.000Z', deliveredAt: new Date(Date.parse('2026-10-02T00:00:00.000Z') + duration).toISOString(), humanWaitMs: 0, installationMs: 100,
      cpuTimeMs: 1000, peakMemoryBytes: configuration === 'podman' ? 90 : 100, idleMemoryBytes: 20, diskReadBytes: 30, diskWriteBytes: 10, storageBytes: 100, networkBytes: 0,
      quality: Object.fromEntries(['runtimeSecurity', 'mandatoryTests', 'independentBehavior', 'changeScope', 'completeAudit', 'correctVerdicts', 'deliveryVerified', 'manualCriteriaPreserved'].map(name => [name, true])),
      pins: Object.fromEntries(['sourceDigest', 'criteriaDigest', 'commandsDigest', 'dependencyPlanDigest', 'imageDigest', 'environmentDigest', 'filePolicyDigest', 'networkPolicyDigest', 'resourceLimitsDigest', 'securityProfile'].map(name => [name, 'same-pin'])),
    })
  }
  return { schema: 1, baseline: 'docker-bind', samples }
}

let input = fixture()
const unchanged = structuredClone(input)
assert.equal(compareSamples(input).winner, 'podman', 'Within 5% prefer lower memory')
assert.deepEqual(input, unchanged, 'Raw measurements must remain immutable')
input = fixture()
delete input.samples[0].idleMemoryBytes
assert.equal(compareSamples(input).status, 'blocked', 'Missing whole-environment data must block selection')
input = fixture()
input.samples = input.samples.filter(sample => sample.configuration !== 'podman' && !sample.id.endsWith('/3'))
assert.equal(compareSamples(input).winner, null, 'Insufficient repeats must not qualify')
input = fixture()
for (const sample of input.samples.filter(sample => sample.configuration === 'podman')) sample.quality.completeAudit = false
assert.equal(compareSamples(input).winner, 'moby', 'Failed audit must exclude faster candidates')
input = fixture()
input.samples.find(sample => sample.configuration === 'podman').pins.networkPolicyDigest = 'changed'
assert.equal(compareSamples(input).winner, 'moby', 'Changed inputs must exclude a configuration')
input = fixture()
for (const sample of input.samples.filter(sample => sample.configuration !== 'docker-bind')) sample.cpuTimeMs = 1101
assert.equal(compareSamples(input).winner, null, 'CPU growth must prevent qualification')
console.log('runtime comparison smoke passed (synthetic fixtures; no performance claim)')
