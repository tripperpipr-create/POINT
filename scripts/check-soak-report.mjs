import fs from 'node:fs'
import path from 'node:path'

const inputs = [
  { name: 'eight-hour', minimumSeconds: 28_800, file: process.argv[2] },
  { name: 'twenty-four-hour', minimumSeconds: 86_400, file: process.argv[3] },
]
if (inputs.some(item => !item.file)) {
  throw new Error('Usage: node check-soak-report.mjs <eight-hour-report.json> <twenty-four-hour-report.json>')
}

const failures = []
for (const expected of inputs) {
  const absolute = path.resolve(expected.file)
  if (!fs.existsSync(absolute)) {
    failures.push(`${expected.name}: report is missing: ${absolute}`)
    continue
  }
  let report
  try {
    report = JSON.parse(fs.readFileSync(absolute, 'utf8'))
  } catch (error) {
    failures.push(`${expected.name}: invalid JSON: ${error.message}`)
    continue
  }
  const require = (condition, message) => {
    if (!condition) failures.push(`${expected.name}: ${message}`)
  }
  require(report.schemaVersion === 1, 'schemaVersion must be 1')
  require(report.profile === expected.name, `profile is ${report.profile || '(missing)'}`)
  require(report.releaseProfile === true, 'report is not release-profile evidence')
  require(Number(report.durationSeconds) >= expected.minimumSeconds * 0.99, `duration ${report.durationSeconds} is shorter than ${expected.minimumSeconds}s`)
  require(report.fixtureFiles >= 50_000 && report.indexFiles >= 50_000 && report.indexPartial === false, '50k non-partial project/index evidence is missing')
  require(report.runs >= 5_000 && report.events >= 100_000 && report.flowExecutions >= 2_000, 'workload cardinality is incomplete')
  require(report.fullRuns?.completed >= 4 && report.fullRuns?.maxConcurrent >= 4, 'four concurrent full Runs are not proven')
  require(report.fullRuns?.cancelled === 1 && report.fullRuns?.cancelTerminalEvents === 1 && report.faults?.runCancellation === true, 'Run cancellation and its single terminal event are not proven')
  require(report.fullRuns?.providerTimedOut === 1 && report.fullRuns?.providerTimeoutTerminalEvents === 1 && report.faults?.providerTimeout === true, 'provider timeout and its single failed terminal event are not proven')
  require(report.sqliteIntegrity === 'ok' && report.foreignKeyViolations === 0, 'SQLite integrity evidence failed')
  require(report.orphanExecutions === 0 && report.lostTerminalEvents === 0, 'orphans or lost terminal events were reported')
  require(report.historyReopenMs <= 5_000, 'history reopen exceeds five seconds')
  require(report.databaseBytes <= 128 * 1024 * 1024 && report.databaseBytesPerEvent <= 512, 'database growth exceeds its cap')
  require(report.memoryGrowthPercent <= 10, 'memory growth exceeds 10%')
  require(report.faults?.coreRestarts > 0 && report.faults?.codeOSSRestarts >= 2 && report.faults?.dockerRestarts > 0 && report.faults?.diskLowWriteRefused === true, 'required restart/disk-limit evidence is incomplete')
  require(report.faults?.temporaryDiskWriteDenied === true && report.faults?.diskWriteRecovered === true, 'temporary disk write loss/recovery is not proven')
  require(report.faults?.interruptedRunRecovery === true && report.interruptedRecovery?.runs === 1 && report.interruptedRecovery?.flowRuns === 1 && report.interruptedRecovery?.executions === 1, 'unfinished Run/Flow/execution recovery is not proven')
  const minimumDesktopProbes = expected.name === 'eight-hour' ? 8 : 24
  const desktopEvidence = Array.isArray(report.controller?.codeOSSEvidence) ? report.controller.codeOSSEvidence : []
  require(desktopEvidence.length >= minimumDesktopProbes && report.controller?.expectedDesktopProbes >= minimumDesktopProbes, `periodic Code-OSS evidence has ${desktopEvidence.length} samples; expected at least ${minimumDesktopProbes}`)
  require(report.controller?.uiInteractive === true, 'periodic Agent Hub UI did not remain interactive')
  require(report.controller?.hubCycles >= minimumDesktopProbes * 2, 'periodic Agent Hub open/close cycles are incomplete')
  require(desktopEvidence.every(entry => entry?.sample?.timing?.agentUsableAfterClickMs <= 6_500 && entry?.sample?.timing?.twoFrameLatencyMs <= 60 && entry?.sample?.hubOpenClose?.completedCycles >= 2 && entry?.sample?.hubOpenClose?.privateGrowthPercent <= 10 && entry?.sample?.extensionHostCount === 1 && entry?.sample?.pointCoreCount === 1), 'a periodic Code-OSS/Hub sample exceeded interactivity, lifecycle, or memory-growth limits')
  require(report.controller?.dockerStopObserved === true && report.controller?.orphanSandboxContainers === 0, 'Docker stop/recovery or orphan-container cleanup is not proven')
  require(report.controller?.coreExitCode === 0 && Array.isArray(report.controller?.failures) && report.controller.failures.length === 0, 'soak controller reported a failure')
  require(report.coreCriteriaPassed === true && report.externalFaultsVerified === true && report.releaseQualified === true, 'report did not pass the closed release gate')
  require(Array.isArray(report.failures) && report.failures.length === 0, `report contains failures: ${(report.failures || []).join('; ')}`)
}

if (failures.length) {
  console.error(failures.join('\n'))
  process.exit(1)
}
process.stdout.write(JSON.stringify({ soakReports: 'ok', profiles: inputs.map(item => item.name) }) + '\n')
