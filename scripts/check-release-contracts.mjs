import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import { validateQualityGate } from './check-quality-gate.mjs'
import { validateLegacyFieldValidation } from './check-legacy-field-validation.mjs'
import { validateDogfoodReport } from './check-dogfood-report.mjs'

const root = path.resolve(import.meta.dirname, '..')
const errors = []
const read = relative => fs.readFileSync(path.join(root, relative), 'utf8')
const requireFile = relative => {
  if (!fs.existsSync(path.join(root, relative))) errors.push(`missing release file ${relative}`)
}
const requireText = (source, token, label) => {
  if (!source.includes(token)) errors.push(`${label}: missing ${token}`)
}
const lineCount = source => source.split(/\r?\n/).length
// Go-исходник читается пакетом, а не файлом: объявление переезжает в соседний
// файл того же пакета, а проверка по имени файла перестаёт что-либо находить и
// молча проходит вхолостую.
const readGoPackage = directory => fs.readdirSync(path.join(root, directory), { withFileTypes: true })
  .filter(entry => entry.isFile() && entry.name.endsWith('.go') && !entry.name.endsWith('_test.go'))
  .map(entry => entry.name)
  .sort()
  .map(name => fs.readFileSync(path.join(root, directory, name), 'utf8'))
  .join('\n')

const requiredFiles = [
  '.github/workflows/ci.yml',
  '.github/workflows/production-release.yml',
  'distribution/performance-slo.json',
  'distribution/soak-profile.json',
  'distribution/quality-gate.json',
  'distribution/legacy-field-validation.json',
  'distribution/legacy-field-report.example.json',
  'distribution/legacy-field-metadata.example.json',
  'distribution/dogfood-gate.json',
  'distribution/dogfood-report.example.json',
  'distribution/sandbox-image-security.json',
  'distribution/sandbox-trivy.json',
  'distribution/sandbox-sbom.cdx.json',
  'distribution/sandbox-packages.json',
  'distribution/sandbox-build-provenance.json',
  'distribution/publish-release.ps1',
  'scripts/check-quality-gate.mjs',
  'scripts/test-point-performance-slo.ps1',
  'scripts/measure-point-hub-cycles.mjs',
  'scripts/test-point-soak.ps1',
  'scripts/check-soak-report.mjs',
  'scripts/create-legacy-field-report.mjs',
  'scripts/check-legacy-field-validation.mjs',
  'scripts/check-dogfood-report.mjs',
  'scripts/generate-installer-sbom.mjs',
  'scripts/check-installer-sbom.mjs',
  'scripts/measure-point-performance.ps1',
  'scripts/measure-point-startup.mjs',
  'scripts/test-point-installer-lifecycle.ps1',
  'scripts/test-point-database-dr.ps1',
  'scripts/test-patch-point-chat-hub.js',
  'scripts/patch-point-chat-hub.js',
  'scripts/run-hub-smokes.mjs',
  'scripts/smoke-code-oss-renderer.ps1',
  'cmd/point-db/main.go',
  'cmd/point-performance-probe/main.go',
  'cmd/point-soak/main.go',
  'cmd/point-soak/main_test.go',
  'docs/operations.md',
  'docs/threat-model.md',
  'internal/app/custom_tools.go',
  'internal/app/legacy_workflows.go',
  'internal/app/workspace_surface.go',
  'internal/app/profiles.go',
  'internal/app/memory.go',
  'internal/app/model_capability_probe.go',
  'internal/app/model_capability_probe_test.go',
  'internal/app/usage_statistics.go',
  'internal/app/run_execution.go',
  'internal/app/run_views.go',
  'internal/sandbox/container_integration_test.go',
  'internal/companion/interventions.go',
  'internal/companion/focus.go',
  'internal/companion/model_reply.go',
  'internal/companion/deterministic_chat.go',
  'internal/companion/deterministic_reply.go',
  'internal/companion/model_chat.go',
  'internal/companion/proposal_parsing.go',
  'internal/companion/quest_proposal.go',
  'internal/companion/usage_analysis.go',
  'internal/storage/hub_agents.go',
  'internal/storage/hub_changesets.go',
  'internal/storage/hub_companion.go',
  'internal/storage/hub_flow_runs.go',
  'internal/storage/hub_orchestration.go',
  'internal/storage/hub_servers.go',
  'internal/storage/hub_state.go',
  'vscode-extension/extension-utils.js',
  'vscode-extension/run-config-utils.js',
  'vscode-extension/ide-navigation-utils.js',
  'vscode-extension/companion-controller.js',
  'vscode-extension/ide-action-controller.js',
  'vscode-extension/ide-navigation-controller.js',
  'vscode-extension/connection-controller.js',
  'vscode-extension/ssh-utils.js',
  'vscode-extension/media/chronicle.css',
  'vscode-extension/ui/client/companion-markdown.js',
  'vscode-extension/ui/client/git-views.js',
  'vscode-extension/ui/client/quest-overview-views.js',
  'vscode-extension/ui/client/quest-runtime-views.js',
  'vscode-extension/ui/client/hall-onboarding-views.js',
  'vscode-extension/ui/client/agent-workflow-editors.js',
  'vscode-extension/ui/client/view-runtime.js',
]
for (const file of requiredFiles) requireFile(file)

if (errors.length) {
  console.error(errors.join('\n'))
  process.exit(1)
}

const workflow = read('.github/workflows/production-release.yml')
const ciWorkflow = read('.github/workflows/ci.yml')
const qualityGate = validateQualityGate()
for (const error of qualityGate.errors) errors.push(error)
const qualityFixture = JSON.parse(read('distribution/quality-gate.json'))
qualityFixture.defects = [{
  id: 'SECURITY_BOUNDARY_001', severity: 'P0', status: 'open',
  title: 'Contract fixture', evidence: 'in-memory negative test',
}]
const rejectedP0 = validateQualityGate(qualityFixture)
if (!rejectedP0.errors.some(error => error.includes('release-blocking P0'))) {
  errors.push('quality gate contract: an open P0 did not fail closed')
}
const narrowedFixture = JSON.parse(read('distribution/quality-gate.json'))
narrowedFixture.reviewedAreas = narrowedFixture.reviewedAreas.slice(1)
const rejectedScope = validateQualityGate(narrowedFixture)
if (!rejectedScope.errors.some(error => error.includes('full production objective'))) {
  errors.push('quality gate contract: narrowed review scope was accepted')
}
const documentationCheck = read('scripts/check-docs.mjs')
for (const token of ['evidenceRoots', 'missing referenced path pattern', 'API route claim is']) {
  requireText(documentationCheck, token, 'documentation implementation contract')
}
const workflowContracts = [
  'git status --porcelain=v1 --untracked-files=all',
  'node scripts/check-docs.mjs',
  'node scripts/check-quality-gate.mjs',
  'aquasecurity/trivy-action@a9c7b0f06e461e9d4b4d1711f154ee024b8d7ab8',
  "version: 'v0.74.0'",
  'severity: CRITICAL,HIGH',
  "exit-code: '1'",
  'needs: sandbox-security',
  'node scripts/check-release-contracts.mjs',
  'go mod verify',
  'go vet ./...',
  'go test ./... -count=1',
  'govulncheck@v1.7.0 ./...',
  'npm audit --omit=dev',
  'POINT_SANDBOX_DOCKER_TEST',
  'DockerSandboxIntegration',
  'migrate-corpus',
  'build-code-oss.ps1 -Minified',
  'Installer SBOM verification',
  'check-installer-sbom.mjs',
  'installerSbomSha256',
  'installerSbomComponents',
  'test-point-agent-onboarding.ps1',
  'test-point-files.ps1',
  'test-point-navigation.ps1',
  'test-point-chronicle.ps1',
  'test-point-connections.ps1',
  'test-point-split-windows.ps1',
  'test-point-agent-context.ps1',
  'test-point-guild-roster.ps1',
  'test-point-installer-lifecycle.ps1',
  'test-point-database-dr.ps1',
  'Get-AuthenticodeSignature',
  'test-point-performance-slo.ps1',
  'check-soak-report.mjs',
  'check-legacy-field-validation.mjs',
  'check-dogfood-report.mjs',
]
for (const contract of workflowContracts) requireText(workflow, contract, 'production release workflow')
const immutableActionPins = [
  'actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5',
  'actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020',
  'actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff',
]
for (const [source, label] of [[workflow, 'production release workflow'], [ciWorkflow, 'CI workflow']]) {
  for (const match of source.matchAll(/uses:\s+([^@\s]+)@([^\s#]+)/g)) {
    if (!/^[a-f0-9]{40}$/.test(match[2])) errors.push(`${label}: action ${match[1]} is not pinned to a full commit SHA`)
  }
  for (const pin of immutableActionPins) requireText(source, pin, label)
}
for (const contract of [
  'aquasecurity/trivy-action@a9c7b0f06e461e9d4b4d1711f154ee024b8d7ab8',
  "version: 'v0.74.0'", 'severity: CRITICAL,HIGH', "exit-code: '1'",
  'format: cyclonedx', 'output: sandbox-sbom.cdx.json',
  'format: json', 'output: sandbox-trivy.json',
]) {
  requireText(ciWorkflow, contract, 'CI sandbox image audit')
}
if ([...workflow.matchAll(/git status --porcelain=v1 --untracked-files=all/g)].length < 2) {
  errors.push('production release workflow: clean Git must be checked before and after the gate')
}
requireText(workflow, "head -ne '${{ github.sha }}'", 'production release final Git identity')
for (const requiredInput of [
  'previous_installer_path:', 'migration_corpus_path:', 'eight_hour_soak_report:',
  'twenty_four_hour_soak_report:', 'legacy_field_evidence_path:', 'dogfood_report:',
]) {
  const input = workflow.slice(workflow.indexOf(requiredInput), workflow.indexOf(requiredInput) + 260)
  if (!/required:\s+true/.test(input)) errors.push(`production release workflow: ${requiredInput} must be required`)
}

for (const match of workflow.matchAll(/\.\/(scripts|distribution)\/([A-Za-z0-9_.\/-]+)/g)) {
  const relative = `${match[1]}/${match[2]}`
  if (!fs.existsSync(path.join(root, relative))) errors.push(`production release workflow references missing ${relative}`)
}

const slo = JSON.parse(read('distribution/performance-slo.json'))
const positiveCore = [
  'fixtureFiles', 'indexBuildMs', 'indexSearchP95Ms', 'indexHeapDeltaMB',
  'sqliteEvents', 'sqliteMaxBytes', 'sqliteBytesPerEvent', 'flowRuns',
  'reopenAndFlowListMs', 'parallelExecutionWrites', 'parallelExecutionWriteMs',
]
const positiveDesktop = [
  'samples', 'workbenchVisibleMs', 'agentUsableAfterClickMs', 'twoFrameLatencyMs',
  'idlePrivateMB', 'agentPrivateMB', 'processCount', 'hubOpenCloseCycles',
  'hubCyclePrivateGrowthPercent', 'idleExtensionHosts', 'agentExtensionHosts',
  'projectSwitchWarmMs', 'projectSwitchColdMs',
]
if (slo.schemaVersion !== 1) errors.push('performance SLO: schemaVersion must be 1')
for (const name of positiveCore) {
  if (!Number.isFinite(slo.core?.[name]) || slo.core[name] <= 0) errors.push(`performance SLO: invalid core.${name}`)
}
for (const name of positiveDesktop) {
  if (!Number.isFinite(slo.desktop?.[name]) || slo.desktop[name] <= 0) errors.push(`performance SLO: invalid desktop.${name}`)
}
if (slo.core.fixtureFiles < 50_000) errors.push('performance SLO: fixture must cover at least 50,000 files')
if (slo.core.sqliteEvents < 100_000) errors.push('performance SLO: SQLite growth must cover at least 100,000 events')
if (slo.core.flowRuns < 2_000) errors.push('performance SLO: Flow recovery must cover at least 2,000 runs')
if (slo.core.sqliteMaxBytes > 128 * 1024 * 1024) errors.push('performance SLO: SQLite cap must stay at or below 128 MB')
if (slo.core.sqliteBytesPerEvent > 512) errors.push('performance SLO: SQLite density must stay at or below 512 bytes/event')
if (slo.core.reopenAndFlowListMs > 5_000) errors.push('performance SLO: history reopen must stay at or below 5 seconds')
if (slo.desktop.samples < 5) errors.push('performance SLO: desktop gate requires five clean samples per mode')
for (const [name, maximum] of Object.entries({
  workbenchVisibleMs: 3000, agentUsableAfterClickMs: 6500, twoFrameLatencyMs: 60,
  idlePrivateMB: 1100, agentPrivateMB: 1450, processCount: 18,
  hubCyclePrivateGrowthPercent: 5,
  // Переключение мира в Чертоге: тёплое ядро подхватывается одной
  // проверкой здоровья, холодное стартует процессом заново.
  projectSwitchWarmMs: 700, projectSwitchColdMs: 2500,
})) {
  if (slo.desktop[name] > maximum) errors.push(`performance SLO: desktop.${name} must stay at or below ${maximum}`)
}
if (slo.desktop.hubOpenCloseCycles < 50) errors.push('performance SLO: Hub memory gate requires at least 50 open/close cycles')

const soak = JSON.parse(read('distribution/soak-profile.json'))
if (soak.schemaVersion !== 1) errors.push('soak profile: schemaVersion must be 1')
for (const [name, seconds] of [['eight-hour', 28_800], ['twenty-four-hour', 86_400]]) {
  const profile = soak.profiles?.[name]
  if (!profile?.releaseQualifying) errors.push(`soak profile: ${name} must be release qualifying`)
  for (const [field, minimum] of Object.entries({ durationSeconds: seconds, fixtureFiles: 50_000, events: 100_000, runs: 5_000, flowExecutions: 2_000, concurrentFullRuns: 4 })) {
    if (!Number.isFinite(profile?.[field]) || profile[field] < minimum) errors.push(`soak profile: ${name}.${field} must be at least ${minimum}`)
  }
  for (const [field, maximum] of Object.entries({ memoryGrowthPercent: 10, historyReopenMs: 5_000, databaseMaxBytes: 128 * 1024 * 1024, databaseBytesPerEvent: 512 })) {
    if (!Number.isFinite(profile?.[field]) || profile[field] > maximum) errors.push(`soak profile: ${name}.${field} must stay at or below ${maximum}`)
  }
  if (!Number.isFinite(profile?.desktopProbeEverySeconds) || profile.desktopProbeEverySeconds > 3_600) errors.push(`soak profile: ${name}.desktopProbeEverySeconds must stay at or below 3600`)
  if (!Number.isInteger(profile?.hubCyclesPerProbe) || profile.hubCyclesPerProbe < 2) errors.push(`soak profile: ${name}.hubCyclesPerProbe must be at least 2`)
}
for (const fault of [
  'core-restart', 'code-oss-restart', 'agent-hub-open-close', 'run-cancellation',
  'provider-timeout', 'docker-stop-recovery', 'temporary-disk-write-loss',
  'disk-low-write-refusal', 'interrupted-run-recovery',
]) {
  if (!soak.requiredFaults?.includes(fault)) errors.push(`soak profile: missing required fault ${fault}`)
}
const soakHarness = read('cmd/point-soak/main.go')
for (const token of [
  'exerciseCancelledRun', 'exerciseProviderTimeout', 'exerciseTemporaryDiskWriteLoss',
  'exerciseNearDiskLimitRefusal', 'seedInterruptedWorkload', 'verifyInterruptedRecovery',
]) {
  requireText(soakHarness, token, 'soak Core fault profile')
}
const soakController = read('scripts/test-point-soak.ps1')
for (const token of [
  '-Mode agent', '-HubCycles', 'desktopProbeEverySeconds', 'docker stop',
  'dockerStopObserved', 'orphanSandboxContainers', 'uiInteractive',
]) {
  requireText(soakController, token, 'soak desktop/Docker fault profile')
}
const storageRecovery = read('internal/storage/sqlite.go')
for (const token of ['UPDATE flow_runs SET status=?', 'UPDATE executions SET status=?']) {
  requireText(storageRecovery, token, 'startup interrupted recovery')
}

const legacyFieldPolicy = JSON.parse(read('distribution/legacy-field-validation.json'))
const compatibilityFeatures = [
  'legacy_profile_save', 'legacy_profile_delete', 'legacy_profile_run_fallback',
  'legacy_workflow_save', 'legacy_workflow_delete', 'legacy_workflow_run',
  'legacy_custom_command_save', 'legacy_custom_command_execute', 'legacy_run_snapshot_read',
]
for (const [field, minimum] of Object.entries({ minimumProjects: 5, minimumStackFamilies: 3, minimumRuns: 200, minimumReleaseWindows: 2 })) {
  if (!Number.isInteger(legacyFieldPolicy[field]) || legacyFieldPolicy[field] < minimum) errors.push(`legacy field policy: ${field} must be at least ${minimum}`)
}
for (const feature of compatibilityFeatures) {
  if (!legacyFieldPolicy.requiredCompatibilityFeatures?.includes(feature)) errors.push(`legacy field policy: missing compatibility feature ${feature}`)
}
const contractWindows = [
  { id: 'contract-window-1', sequence: 1, startedAt: '2026-01-01T00:00:00Z', completedAt: '2026-01-08T00:00:00Z', fullWindowAttestation: true, candidateVersion: '1.2.3-rc.1', candidateCommit: '1'.repeat(40) },
  { id: 'contract-window-2', sequence: 2, startedAt: '2026-01-08T00:00:00Z', completedAt: '2026-01-15T00:00:00Z', fullWindowAttestation: true, candidateVersion: '1.2.3-rc.2', candidateCommit: '2'.repeat(40) },
]
const contractStacks = ['go', 'node', 'python', 'go', 'node']
const contractFieldEntries = []
for (let project = 0; project < 5; project += 1) {
  for (const window of contractWindows) {
    contractFieldEntries.push({
      source: `in-memory-project-${project + 1}-window-${window.sequence}.json`,
      report: {
        schemaVersion: 1,
        optInAggregateOnly: true,
        realProjectAttestation: true,
        projectNonce: crypto.createHash('sha256').update(`contract-project-${project}`).digest('hex'),
        stackFamily: contractStacks[project],
        releaseWindow: { ...window },
        runs: { started: 20, completed: 20, failed: 0, cancelled: 0, interrupted: 0 },
        lifecycle: Object.fromEntries(legacyFieldPolicy.requiredLifecycleChecks.map(check => [check, true])),
        blockingDefects: { p0: 0, p1: 0 },
        compatibilityTotals: Object.fromEntries(compatibilityFeatures.map(feature => [feature, 0])),
        compatibilityUsage: [],
      },
    })
  }
}
const acceptedFieldFixture = validateLegacyFieldValidation(contractFieldEntries, legacyFieldPolicy)
if (acceptedFieldFixture.errors.length || acceptedFieldFixture.summary.projectCount !== 5 || acceptedFieldFixture.summary.runCount !== 200) {
  errors.push(`legacy field contract: valid representative evidence was rejected: ${acceptedFieldFixture.errors.join('; ')}`)
}
const privateFieldFixture = structuredClone(contractFieldEntries)
privateFieldFixture[0].report.projectPath = 'C:\\private\\project'
const rejectedPrivateField = validateLegacyFieldValidation(privateFieldFixture, legacyFieldPolicy)
if (!rejectedPrivateField.errors.some(error => error.includes('forbidden fields'))) errors.push('legacy field contract: project path was not rejected')
const incompleteFieldFixture = contractFieldEntries.slice(1)
const rejectedIncompleteField = validateLegacyFieldValidation(incompleteFieldFixture, legacyFieldPolicy)
if (!rejectedIncompleteField.errors.some(error => error.includes('missing release window'))) errors.push('legacy field contract: project absent from one release window was accepted')
const relaxedFieldPolicy = { ...legacyFieldPolicy, minimumProjects: 4 }
const rejectedRelaxedFieldPolicy = validateLegacyFieldValidation(contractFieldEntries, relaxedFieldPolicy)
if (!rejectedRelaxedFieldPolicy.errors.some(error => error.includes('minimumProjects must be at least 5'))) errors.push('legacy field contract: weakened project threshold was accepted')

const dogfoodPolicy = JSON.parse(read('distribution/dogfood-gate.json'))
const dogfoodCheckpoints = Array.from({ length: 15 }, (_, index) => ({
  timestamp: new Date(Date.UTC(2026, 0, index + 1)).toISOString(),
  activeUseAttestation: true,
  runsStarted: 1,
  runsTerminal: 1,
  p0: 0,
  p1: 0,
  dataLossIncidents: 0,
  databaseCorruptionIncidents: 0,
  lostTerminalEvents: 0,
}))
const dogfoodFixture = {
  schemaVersion: 1,
  candidateVersion: '1.2.3-rc.2',
  candidateCommit: '3'.repeat(40),
  startedAt: '2026-01-01T00:00:00Z',
  completedAt: '2026-01-15T00:00:00Z',
  fourteenDayAttestation: true,
  operatorSignoff: true,
  totals: { runsStarted: 15, runsTerminal: 15, p0: 0, p1: 0, dataLossIncidents: 0, databaseCorruptionIncidents: 0, lostTerminalEvents: 0 },
  checkpoints: dogfoodCheckpoints,
}
const acceptedDogfood = validateDogfoodReport(dogfoodFixture, dogfoodPolicy)
if (acceptedDogfood.errors.length) errors.push(`dogfood contract: valid fourteen-day report was rejected: ${acceptedDogfood.errors.join('; ')}`)
const rejectedShortDogfood = validateDogfoodReport({ ...dogfoodFixture, completedAt: '2026-01-14T00:00:00Z', checkpoints: dogfoodCheckpoints.slice(0, 14), totals: { ...dogfoodFixture.totals, runsStarted: 14, runsTerminal: 14 } }, dogfoodPolicy)
if (!rejectedShortDogfood.errors.some(error => error.includes('at least 336'))) errors.push('dogfood contract: thirteen-day report was accepted')
const rejectedRelaxedDogfood = validateDogfoodReport(dogfoodFixture, { ...dogfoodPolicy, minimumElapsedHours: 300 })
if (!rejectedRelaxedDogfood.errors.some(error => error.includes('must be at least 336'))) errors.push('dogfood contract: weakened duration threshold was accepted')

const installer = read('scripts/test-point-installer-lifecycle.ps1')
for (const token of ['previous-clean', 'upgrade-current', 'rollback-previous', 'preserve-across-installer-lifecycle', 'smoke-code-oss-renderer.ps1']) {
  requireText(installer, token, 'installer lifecycle')
}
const build = read('distribution/build-code-oss.ps1')
for (const token of ["cmd\\server", "cmd\\point-db", "point-core.exe", "point-db.exe", 'vscode-win32-x64-user-setup', 'publish-release.ps1']) {
  requireText(build, token, 'Code-OSS build')
}
const installerSBOMGenerator = read('scripts/generate-installer-sbom.mjs')
for (const token of [
  "specVersion: '1.7'", "path.join(appRoot, 'node_modules')",
  "path.join(extensionRoot, 'node_modules')", 'local-agent-workbench/extension.js',
  'point-core.exe', 'point-db.exe', "alg: 'SHA-256'",
]) {
  requireText(installerSBOMGenerator, token, 'installer SBOM generator')
}
const installerSBOMCheck = read('scripts/check-installer-sbom.mjs')
for (const token of [
  'collectInstallerInventory', 'installer hash does not match',
  'component count', 'transitive packaged library inventory is empty',
  'installed paths do not match', 'key file hash does not match',
]) {
  requireText(installerSBOMCheck, token, 'installer SBOM verifier')
}
const releasePublisher = read('distribution/publish-release.ps1')
for (const token of [
  'generate-installer-sbom.mjs', 'check-installer-sbom.mjs',
  'installerSbomSha256', 'installerSbomComponents',
]) {
  requireText(releasePublisher, token, 'release publisher')
}
const databaseCLI = read('cmd/point-db/main.go')
for (const command of ['verify', 'backup', 'migrate-copy', 'migrate-corpus', 'restore']) {
  requireText(databaseCLI, `"${command}"`, 'point-db CLI')
}
const databaseDR = read('scripts/test-point-database-dr.ps1')
for (const token of [
  "@('backup'", "@('restore'", '-Arguments', '--confirm-offline', 'previousPath',
  'exactBackupRestore', 'exactRecoveryPoint', 'sourceUnchanged',
  'targetFixtureUnchanged', 'foreignKeyViolations',
]) {
  requireText(databaseDR, token, 'database DR drill')
}
for (const token of [
  "resources\\app\\extensions\\local-agent-workbench\\bin\\point-db.exe",
  'at least two .db copies', 'Get-FileHash -Algorithm SHA256',
  'Migration changed source corpus item',
]) {
  requireText(workflow, token, 'production database DR gate')
}

const chronicleCSS = read('vscode-extension/media/chronicle.css')
const chronicleDiffRule = chronicleCSS.match(/\.diff-scroll pre\s*\{([^}]*)\}/)?.[1] || ''
for (const declaration of ['width: 100%', 'min-width: 0', 'max-width: 100%', 'overflow: auto']) {
  requireText(chronicleDiffRule, declaration, 'Chronicle diff containment')
}
const chronicleVerifier = read('scripts/verify-point-chronicle.mjs')
for (const token of ['horizontalOverflow', 'overflowers', 'summary.horizontalOverflow > 1']) {
  requireText(chronicleVerifier, token, 'Chronicle Electron E2E')
}
try {
  const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor
  // The verifier is an ESM script with top-level await. Removing its single
  // static import lets AsyncFunction parse the rest without executing it or
  // spawning a child process (which read-only sandboxes may forbid).
  new AsyncFunction(chronicleVerifier.replace(/^import\s+[^\n]+\n/, ''))
} catch (error) {
  errors.push(`Chronicle Electron E2E: verifier syntax invalid: ${error.message}`)
}

const threatModel = read('docs/threat-model.md')
for (const token of [
  'T01', 'T02', 'T03', 'T04', 'T05', 'T06', 'T07', 'T08', 'T09', 'T10',
  'T11', 'T12', 'T13', 'T14', 'T15', 'T16', 'Privacy inventory', 'Принятые остаточные риски',
  'tools', 'approval', 'SecretStorage', 'Learning', 'Memory', 'Skill', 'SSH', 'DB', 'network', 'workspace',
]) {
  requireText(threatModel, token, 'threat model')
}
requireText(threatModel, 'internal/sandbox/container_integration_test.go', 'threat model sandbox evidence')
const threatRows = [...threatModel.matchAll(/^\| (T\d{2}) \|.*$/gm)]
if (threatRows.length !== 16) errors.push(`threat model: expected 16 threat rows, found ${threatRows.length}`)
for (const row of threatRows) {
  if (!/`(?:internal|cmd|scripts|distribution|\.github|vscode-extension|frontend|docs)[\\/][^`]+`/.test(row[0])) {
    errors.push(`threat model: ${row[1]} has no exact repository evidence path`)
  }
}
const manualToolSource = read('internal/app/custom_tools.go')
for (const token of ['ApprovalID', 'ConsumeToolExecutionApproval', 'approved=true is not an approval']) {
  requireText(manualToolSource, token, 'manual tool approval')
}
const manualToolMigration = readGoPackage('internal/storage')
for (const token of ['manual_tool_execution_approval_v1', 'tool_execution_approvals', "'consumed'"]) {
  requireText(manualToolMigration, token, 'manual tool approval migration')
}
const sandboxSource = read('internal/sandbox/container.go')
for (const token of [
  'network := "none"', '"--read-only"', '"--cap-drop", "ALL"',
  '"no-new-privileges=true"', '"--pids-limit"', '"--memory"', '"--cpus"',
  'prepareEgressGateway', '"--internal"', '"network", "connect", "bridge"',
  'HTTP_PROXY=http://point-egress-gateway:8080', 'POINT_EGRESS_POLICY_DIGEST=',
  'pinAllowlistHosts(ctx, policy)', '"--add-host", "point-egress-gateway:"',
  'args = append(args, "--add-host", fqdn+":"+ip.String())',
  'executionImage()', 'imageDigestPattern',
]) {
  requireText(sandboxSource, token, 'container sandbox')
}
const egressPolicySource = read('internal/egress/policy.go')
for (const token of ['ResolvePinned', 'DNS answer contains forbidden address', 'exact DNS FQDN is required', 'policyDigest']) {
  requireText(egressPolicySource, token, 'controlled egress policy')
}
const egressGatewaySource = read('internal/egress/gateway.go')
for (const token of ['http.MethodConnect', 'tls_sni_mismatch', 'connection_quota', 'byte_quota', 'policy_digest']) {
  requireText(egressGatewaySource, token, 'controlled egress gateway')
}
const sandboxIntegration = read('internal/sandbox/container_integration_test.go')
for (const token of [
  'POINT_HOST_SECRET', '/proc/mounts', 'CapEff:', 'NoNewPrivs:', '/proc/1/cmdline',
  '/sys/fs/cgroup/memory.max', '/sys/fs/cgroup/pids.max', '/sys/fs/cgroup/cpu.max',
  'registry.npmjs.org:443', 'unlisted fqdn', 'plain http', 'direct ip',
]) {
  requireText(sandboxIntegration, token, 'live container isolation evidence')
}
const sandboxImage = read('Dockerfile.sandbox')
for (const token of [
  'cgr.dev/chainguard/wolfi-base@sha256:03c6561658909fc4eadd0b2dc717375df40a22cc05455b8f82f1f1974e7e4427',
  'ca-certificates-bundle=20260611-r0', 'git=2.55.0-r5', 'build-base=1-r9',
  'python-3.13=3.13.15-r4', 'ripgrep=15.2.0-r2', 'go-1.26=1.26.7-r0',
  'nodejs-24=24.20.0-r1', 'npm=12.0.2-r0', 'apk add --no-cache',
  'adduser -D -u 10001', 'USER 10001:10001', 'WORKDIR /workspace',
]) {
  requireText(sandboxImage, token, 'sandbox image')
}
const sandboxSecurity = JSON.parse(read('distribution/sandbox-image-security.json'))
const sha256File = relative => crypto.createHash('sha256').update(fs.readFileSync(path.join(root, relative))).digest('hex')
if (sandboxSecurity.schemaVersion !== 2) errors.push('sandbox image security: schemaVersion must be 2')
if (sandboxSecurity.productVersion !== qualityFixture.productVersion) errors.push('sandbox image security: product version drift')
if (sandboxSecurity.method !== 'docker-save-archive-no-docker-socket') errors.push('sandbox image security: unsafe or unknown scan method')
if (sandboxSecurity.dockerfileSha256 !== sha256File('Dockerfile.sandbox')) errors.push('sandbox image security: Dockerfile changed after attestation')
if (sandboxSecurity.integrationTestSha256 !== sha256File('internal/sandbox/container_integration_test.go')) errors.push('sandbox image security: integration test changed after attestation')
if (!/^sha256:[a-f0-9]{64}$/.test(sandboxSecurity.image?.digest || '')) errors.push('sandbox image security: exact image digest is missing')
if (!Number.isInteger(sandboxSecurity.image?.sizeBytes) || sandboxSecurity.image.sizeBytes <= 0) errors.push('sandbox image security: image size is invalid')
if (!/^[a-f0-9]{64}$/.test(sandboxSecurity.image?.archiveSha256 || '')) errors.push('sandbox image security: archive hash is invalid')
requireText(sandboxImage, `${sandboxSecurity.base?.name}@${sandboxSecurity.base?.digest}`, 'sandbox image security base attribution')
if (!Array.isArray(sandboxSecurity.directPackages) || sandboxSecurity.directPackages.length !== 8) {
  errors.push('sandbox image security: direct package attribution must contain exactly eight packages')
} else {
  for (const packagePin of sandboxSecurity.directPackages) requireText(sandboxImage, packagePin, 'sandbox image security package attribution')
}
if (sandboxSecurity.scanner?.version !== '0.74.0') errors.push('sandbox image security: unexpected Trivy version')
if (!/^sha256:[a-f0-9]{64}$/.test(sandboxSecurity.scanner?.imageDigest || '')) errors.push('sandbox image security: scanner image digest is invalid')
for (const source of [workflow, ciWorkflow]) {
  requireText(source, `version: 'v${sandboxSecurity.scanner?.version}'`, 'sandbox image security scanner version')
  requireText(source, `aquasecurity/trivy-action@${sandboxSecurity.scanner?.actionCommit}`, 'sandbox image security action attribution')
}
if (sandboxSecurity.settings?.ignoreUnfixed !== false || sandboxSecurity.settings?.exitCodeOnFinding !== 1) {
  errors.push('sandbox image security: scan must fail on fixed and unfixed HIGH/CRITICAL findings')
}
if (!Array.isArray(sandboxSecurity.settings?.scanners) || sandboxSecurity.settings.scanners.join(',') !== 'vuln') errors.push('sandbox image security: scanner scope drift')
if (![sandboxSecurity.result?.high, sandboxSecurity.result?.critical, sandboxSecurity.result?.total, sandboxSecurity.result?.exitCode].every(value => value === 0)) {
  errors.push('sandbox image security: attested scan is not clean')
}
if (!/^[a-f0-9]{64}$/.test(sandboxSecurity.result?.reportSha256 || '')) errors.push('sandbox image security: report hash is invalid')
const sandboxArtifactContracts = [
  ['trivyReport', 'distribution/sandbox-trivy.json'],
  ['sbom', 'distribution/sandbox-sbom.cdx.json'],
  ['transitivePackageManifest', 'distribution/sandbox-packages.json'],
  ['provenance', 'distribution/sandbox-build-provenance.json'],
]
for (const [key, expectedPath] of sandboxArtifactContracts) {
  const artifact = sandboxSecurity.artifacts?.[key]
  if (artifact?.path !== expectedPath || artifact?.sha256 !== sha256File(expectedPath) || artifact?.bytes !== fs.statSync(path.join(root, expectedPath)).size) {
    errors.push(`sandbox image security: ${key} artifact attribution drift`)
  }
}
const sandboxTrivyReport = JSON.parse(read('distribution/sandbox-trivy.json'))
const sandboxVulnerabilities = (sandboxTrivyReport.Results || []).flatMap(result => result.Vulnerabilities || [])
if (sandboxVulnerabilities.some(item => ['HIGH', 'CRITICAL'].includes(item.Severity))) errors.push('sandbox image security: machine-readable Trivy report is not clean')
const sandboxSBOM = JSON.parse(read('distribution/sandbox-sbom.cdx.json'))
if (sandboxSBOM.bomFormat !== 'CycloneDX' || sandboxSBOM.specVersion !== sandboxSecurity.artifacts?.sbom?.specVersion || (sandboxSBOM.components || []).length !== sandboxSecurity.artifacts?.sbom?.components) {
  errors.push('sandbox image security: CycloneDX SBOM attribution drift')
}
const sandboxPackages = JSON.parse(read('distribution/sandbox-packages.json'))
if (sandboxPackages.image?.digest !== sandboxSecurity.image?.digest || sandboxPackages.packageCount !== (sandboxPackages.packages || []).length || sandboxPackages.packageCount !== sandboxSecurity.artifacts?.transitivePackageManifest?.packages) {
  errors.push('sandbox image security: transitive package manifest attribution drift')
}
const sandboxProvenance = JSON.parse(read('distribution/sandbox-build-provenance.json'))
if (sandboxProvenance.subject?.digest !== sandboxSecurity.image?.digest || sandboxProvenance.provenanceLevel !== sandboxSecurity.artifacts?.provenance?.level) {
  errors.push('sandbox image security: provenance subject attribution drift')
}
for (const material of sandboxProvenance.materials || []) {
  if (material.path && material.sha256 !== sha256File(material.path)) errors.push(`sandbox image security: provenance material drift ${material.path}`)
}
if (sandboxProvenance.sourceControl?.verified !== sandboxSecurity.artifacts?.provenance?.sourceControlVerified) errors.push('sandbox image security: provenance source-control state drift')
if (sandboxSecurity.result?.reportSha256 !== sandboxSecurity.artifacts?.trivyReport?.sha256) errors.push('sandbox image security: result/report digest mismatch')
if (sandboxSecurity.integration?.passed !== true || !sandboxSecurity.integration.verified?.includes('deny-all-egress') || !sandboxSecurity.integration.verified?.includes('exact-tls-egress-positive') || !sandboxSecurity.integration.verified?.includes('direct-ip-denied') || !sandboxSecurity.integration.verified?.includes('pinned-toolchain-login-shell')) {
  errors.push('sandbox image security: live isolation attestation is incomplete')
}
const authSource = read('internal/httpapi/auth.go')
for (const token of ['make([]byte, 32)', 'subtle.ConstantTimeCompare', '0o600']) {
  requireText(authSource, token, 'HTTP API authentication')
}
const sshSource = read('internal/servers/openssh.go')
requireText(sshSource, 'StrictHostKeyChecking=accept-new', 'SSH implementation')
requireText(threatModel, '`accept-new`', 'SSH residual-risk documentation')

const architecture = read('docs/architecture.md')
const readme = read('README.md')
if (!/Code-OSS Agent Hub is the canonical Agent Hub client/i.test(architecture)) errors.push('architecture: Code-OSS canonical client decision is missing')
if (!/Wails diagnostic client/i.test(readme)) errors.push('README: Wails diagnostic-client status is missing')

const extensionSource = read('vscode-extension/extension.js')
for (const token of [
  "require('./extension-utils')", "require('./run-config-utils')",
  "require('./ide-navigation-utils')", "require('./ssh-utils')",
  "require('./companion-controller')", "require('./ide-action-controller')",
  "require('./ide-navigation-controller')", "require('./connection-controller')",
  "require('./project-index-controller')", "require('./console-ssh-controller')",
  "require('./core-log')", "require('./core-lease')",
  "require('./git-tool-controller')", "require('./hub-surfaces-controller')",
  "require('./hub-polling-controller')", "require('./companion-thread-controller')",
]) {
  requireText(extensionSource, token, 'extension module boundary')
}
// Выбор подключения — ровно одно место.
//
// Он был написан трижды копипастой, для квеста, компаньона и Мастера, и каждая
// копия брала первое подключение с подходящим пресетом: при двух ключах одного
// провайдера запрос молча уходил с чужим. Проверка держит два условия — сам
// поиск существует в единственном экземпляре, и все трое им пользуются.
{
  const guesses = [...extensionSource.matchAll(/item\.presetId === \w+\.providerPreset/g)].length
  if (guesses > 1) {
    errors.push(`connection binding: подбор подключения по пресету встречается ${guesses} раза — вернулась копипаста, из-за которой брался чужой ключ`)
  }
  for (const owner of ['credentialForExecution', 'credentialForCompanion', 'credentialForOrchestrator']) {
    const body = extensionSource.match(new RegExp(`async ${owner}\\([^)]*\\) \\{[\\s\\S]*?\\n  \\}`))
    if (!body) errors.push(`connection binding: метод ${owner} не найден`)
    else if (!body[0].includes('this.credentialFor(')) {
      errors.push(`connection binding: ${owner} снова ищет подключение сам вместо общего credentialFor`)
    }
  }
}
const webviewSource = read('vscode-extension/ui/client/main.js')
for (const token of [
  "from './statistics-views.js'", "from './infrastructure-views.js'",
  "from './change-set-views.js'", "from './companion-markdown.js'",
  "from './git-views.js'",
  "from './quest-overview-views.js'", "from './quest-runtime-views.js'",
  "from './hall-onboarding-views.js'", "from './agent-workflow-editors.js'",
  "from './view-runtime.js'", "from './keyboard-navigation.js'",
  "from './model-picker.js'", "from './companion-studio-views.js'", "from './companion-setup-wizard.js'",
  "from './master-hiring-card.js'", "from './master-agent-card.js'",
  "from './decision-views.js'", "from './companion-thread-views.js'",
  "from './companion-actions.js'", "from './onboarding-actions.js'",
  "from './companion-transport.js'", "from './master-inbox.js'",
  "from './hub-entity-inbox.js'", "from './run-inbox.js'",
  "from './world-state-inbox.js'",
]) {
  requireText(webviewSource, token, 'webview module boundary')
}
// Бюджет строк — трещотка, а не цель.
//
// До 18 сентября файл попадал сюда только после того, как с ним уже
// помучились и разрезали. Поэтому самые крупные файлы дерева как раз и не
// были ограничены ничем: ни `domain/hub.go` с его fan-in в сотни файлов, ни
// `apply-overlay.mjs`, который кладёт заплаты в чужое дерево Code-OSS. Числа
// ниже — их сегодняшний размер: расти дальше затвор им не даст, а падать
// можно сколько угодно.
for (const [file, maximum] of Object.entries({
  'internal/storage/hub.go': 100,
  'internal/domain/hub.go': 1077,
  'internal/flowruntime/runtime.go': 987,
  'internal/storage/sqlite.go': 985,
  'internal/tools/workspace_tools.go': 960,
  'cmd/point-soak/main.go': 967,
  'distribution/apply-overlay.mjs': 5068,
  'vscode-extension/ui/layers/07-master-quiet.css': 2110,
  'vscode-extension/ui/layers/05-hall.css': 1841,
  // Три файла пишутся руками мимо `ui/build.mjs`: главная и Летопись
  // подключают только `rpg-tokens.css` и в общий бандл не входят. Ни бюджета,
  // ни шкал у них не было вовсе — теперь есть хотя бы трещотка по строкам.
  'vscode-extension/media/home.css': 69,
  'vscode-extension/media/chronicle.css': 79,
  'vscode-extension/media/chronicle.js': 59,
  'internal/app/app.go': 720,
  'internal/companion/service.go': 600,
  'vscode-extension/extension.js': 3750,
  'vscode-extension/git-tool-controller.js': 400,
  'vscode-extension/hub-surfaces-controller.js': 400,
  'vscode-extension/hub-polling-controller.js': 300,
  'vscode-extension/companion-thread-controller.js': 250,
  'vscode-extension/companion-controller.js': 1500,
  'vscode-extension/ide-action-controller.js': 1500,
  'vscode-extension/ide-navigation-controller.js': 1500,
  'vscode-extension/connection-controller.js': 1500,
  'vscode-extension/ui/client/main.js': 4400,
  'vscode-extension/ui/client/infra-actions.js': 220,
  'vscode-extension/ui/client/master-actions.js': 280,
  'vscode-extension/ui/client/run-actions.js': 280,
  'vscode-extension/ui/client/flow-actions.js': 180,
  'vscode-extension/ui/client/roster-actions.js': 330,
  'vscode-extension/ui/client/form-submit.js': 380,
  'vscode-extension/ui/client/quest-overview-views.js': 1500,
  'vscode-extension/ui/client/quest-runtime-views.js': 1500,
  'vscode-extension/ui/client/hall-onboarding-views.js': 1500,
  'vscode-extension/ui/client/agent-workflow-editors.js': 1500,
  'vscode-extension/ui/client/view-runtime.js': 1500,
  // Общие чистые помощники вебвью: экранирование и единицы. Растут только
  // тем, что в них съезжается очередная разошедшаяся копия.
  'vscode-extension/ui/client/html-escape.js': 40,
  'vscode-extension/ui/client/format-units.js': 120,
  'vscode-extension/ui/client/keyboard-navigation.js': 300,
  'vscode-extension/ui/client/model-picker.js': 400,
  'vscode-extension/ui/client/companion-studio-views.js': 400,
  'vscode-extension/ui/client/companion-setup-wizard.js': 300,
  'vscode-extension/ui/client/work-order-execution-views.js': 300,
  'vscode-extension/ui/client/master-work-order-v2.js': 300,
  'vscode-extension/ui/client/master-hiring-card.js': 300,
  'vscode-extension/ui/client/master-agent-card.js': 450,
  'vscode-extension/ui/client/decision-views.js': 400,
  'vscode-extension/ui/client/companion-thread-views.js': 300,
  'vscode-extension/ui/client/companion-actions.js': 400,
  'vscode-extension/ui/client/onboarding-actions.js': 450,
  'vscode-extension/ui/client/companion-transport.js': 450,
  'vscode-extension/ui/client/master-inbox.js': 200,
  'vscode-extension/ui/client/hub-entity-inbox.js': 220,
  'vscode-extension/ui/client/run-inbox.js': 240,
  'vscode-extension/ui/client/world-state-inbox.js': 180,
})) {
  const actual = lineCount(read(file))
  if (actual > maximum) errors.push(`module boundary: ${file} has ${actual} lines (budget ${maximum})`)
}
// Чужая программа запускается только через internal/osproc.
//
// Point поднимает ядро без консоли, а у процесса без консоли Windows выдаёт
// новую консоль каждому консольному потомку: git, docker, cmd, ssh и
// CLI-исполнители вспыхивали чёрными окнами поверх рабочего места. osproc
// ставит CREATE_NO_WINDOW, но дыра открывается заново с каждым новым прямым
// exec.Command — а увидеть её можно только глазами на живой Windows, потому
// что на сборке и в тестах она молчит.
const goSourceFiles = directory => {
  const found = []
  const walk = current => {
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const full = path.join(current, entry.name)
      if (entry.isDirectory()) { walk(full); continue }
      if (entry.name.endsWith('.go') && !entry.name.endsWith('_test.go')) found.push(full)
    }
  }
  walk(path.join(root, directory))
  return found
}
const osprocDirectory = path.join(root, 'internal', 'osproc')
for (const directory of ['internal', 'cmd']) {
  for (const file of goSourceFiles(directory)) {
    if (file.startsWith(osprocDirectory)) continue
    const code = fs.readFileSync(file, 'utf8')
    const relative = path.relative(root, file).split(path.sep).join('/')
    // completion_shell_windows.go собирает свой SysProcAttr с CmdLine и потому
    // зовёт osproc.Hide на готовой команде: конструктор стёр бы CmdLine.
    const allowed = relative === 'internal/app/completion_shell_windows.go' && code.includes('osproc.Hide(')
    if (allowed) continue
    if (/(?<![\w.])exec\.Command(Context)?\(/.test(code)) {
      errors.push(`process launch: ${relative} зовёт exec.Command напрямую — консольное окно всплывёт на Windows; используйте internal/osproc`)
    }
  }
}

// Список файлов расширения в apply-overlay.mjs — ручной, и он дважды отстал от
// исходников: `extension-utils.js` и `run-config-utils.js` выделили из
// `extension.js`, а сюда не вписали. Сборка при этом проходила, приложение
// собиралось, и только послесборочный смоук показывал «Cannot find module».
// Двенадцать минут сборки ради ошибки, которую видно чтением двух файлов.
const overlaySource = read('distribution/apply-overlay.mjs')
const stagingList = overlaySource.match(/for \(const name of \[([^\]]*)\]/)
if (!stagingList) errors.push('extension staging: не найден список файлов в apply-overlay.mjs')
else {
  const staged = new Set([...stagingList[1].matchAll(/'([^']+)'/g)].map(match => match[1]))
  for (const file of fs.readdirSync(path.join(root, 'vscode-extension')).filter(name => name.endsWith('.js'))) {
    const code = read(`vscode-extension/${file}`)
    for (const match of code.matchAll(/require\(\s*['"](\.\/[^'"]+)['"]\s*\)/g)) {
      const target = `${match[1].slice(2)}.js`
      if (!staged.has(target)) errors.push(`extension staging: ${file} требует ./${match[1].slice(2)}, но ${target} нет в списке apply-overlay.mjs`)
    }
  }
}

const extensionPackage = read('vscode-extension/package.json')
// Раньше здесь требовались шесть строк `node --check <файл>` поимённо.
// Цепочка в `npm run check` была ручной и отстала: 54 файла из 97, семь
// контроллеров хоста не проверял никто. Теперь проверка одна и обходит
// каталоги, поэтому затвору достаточно требовать её вызова.
for (const token of [
  'check-js-syntax.mjs', 'check-webview-exports.mjs',
  'smoke-chat-markup-escaping.js', 'smoke-git-workflow.js',
]) {
  requireText(extensionPackage, token, 'extension release check')
}

const checkScripts = JSON.parse(extensionPackage).scripts
if (checkScripts.prepackage !== 'npm run build:core') errors.push('extension packaging must build fresh core first')
if (checkScripts.precheck !== 'npm run build:core' || checkScripts['build:core'] !== 'node ../scripts/build-core.mjs') errors.push('extension checks must build fresh core first')
requireFile('scripts/build-core.mjs')
requireText(ciWorkflow.slice(ciWorkflow.indexOf('  extension:')), 'actions/setup-go@', 'extension integration toolchain')

// Линтер и его набор проверок — одно целое.
//
// Шаг без конфига включит набор по умолчанию и покраснеет на тридцати
// ложных срабатываниях ST1005; конфиг без шага — файл, который ничего не
// стережёт. Версия сверяется тоже: расхождение между CI и Makefile даёт
// два разных ответа на один коммит.
requireFile('staticcheck.conf')
const staticcheckPin = 'honnef.co/go/tools/cmd/staticcheck@v0.8.1'
requireText(ciWorkflow, staticcheckPin, 'CI staticcheck')
requireText(read('Makefile'), staticcheckPin, 'Makefile staticcheck')
requireText(read('staticcheck.conf'), 'checks = ["inherit", "-ST1005", "SA9003"]', 'staticcheck checks')

// Смоук обязан быть кем-то вызван.
//
// Список в run-hub-smokes.mjs намеренно ручной: автопоиск по маске подключал бы
// к затвору черновик и временное воспроизведение бага вместе с настоящей
// проверкой. Но обратной стороны у ручного списка не было, и файлы гнили молча:
// 2 сентября так нашлись 28 неподключённых смоуков, один из которых давно
// разошёлся с экраном, а 18 сентября — ещё два, отставших от контракта v2.
//
// Признак живого смоука — упоминание в том, что умеет его запустить: раннер,
// npm-скрипты, workflow, другой скрипт. Упоминание в документе не считается
// намеренно: именно так выглядели все найденные мертвецы — строка в CHANGELOG
// и ни одного вызывающего.
const invokerRoots = ['scripts', '.github', 'distribution', 'vscode-extension/package.json', 'Makefile']
const invokerSources = new Map()
const collectInvokers = relative => {
  const absolute = path.join(root, relative)
  if (!fs.existsSync(absolute)) return
  if (fs.statSync(absolute).isDirectory()) {
    for (const entry of fs.readdirSync(absolute, { withFileTypes: true })) {
      if (entry.name === 'node_modules') continue
      collectInvokers(path.posix.join(relative, entry.name))
    }
    return
  }
  if (/\.(md|png|jpg|jpeg|webp|ico|woff2|ttf|svg|exe|dll|zip)$/i.test(relative)) return
  invokerSources.set(relative, fs.readFileSync(absolute, 'utf8'))
}
for (const entry of invokerRoots) collectInvokers(entry)

// Ручные браузерные проверки: вызывает человек по записанному порядку.
// Каждая строка несёт причину, почему проверка не может жить в затворе.
const manualSmokes = new Map([
  ['smoke-master-session-controls.cjs',
    'требует Edge и Playwright из локальной сборки Code-OSS; порядок — docs/master-chat-sessions.md'],
  ['test-point-console-channel.ps1',
    'живой рабочий стол Windows и собранный Point.exe со снимками экрана; в отличие от одиннадцати соседних test-point-* не внесён в production-release.yml'],
  ['test-point-lazy-terminal.ps1',
    'живой рабочий стол Windows и собранный Point.exe; так же не внесён в production-release.yml'],
  ['verify-point-deploy.mjs',
    'сверяет выложенное приложение с репозиторием по SHA-256 — нужна выкладка, а не исходники'],
  ['verify-point-editor-watermark.mjs',
    'зонд по живому окну через CDP; порядок запуска — docs/README.md'],
  ['verify-point-safe-mode.mjs',
    'зонд по живому окну через CDP; порядок запуска — docs/README.md'],
])
// Фильтр шире одного `smoke-`.
//
// Правило затвора — «проверка обязана быть кем-то вызвана», а не «файлы с
// таким именем обязаны». Пока он смотрел только на `smoke-`,
// пятнадцать PowerShell-наборов `test-point-*` и десятки зондов
// `verify-point-*` оставались вне его поля зрения по одному только признаку —
// имени. Три из них оказались ровно в том состоянии, которое затвор и заведён
// запрещать: упоминание в документе есть, вызывающего нет.
const smokeFiles = fs.readdirSync(path.join(root, 'scripts'), { withFileTypes: true })
  .filter(entry => entry.isFile() && /^(?:smoke|test-point|verify-point)-.*\.(js|mjs|cjs|ps1)$/.test(entry.name))
  .map(entry => entry.name)
  .sort()
const orphanSmokes = []
let wiredSmokes = 0
for (const name of smokeFiles) {
  const own = `scripts/${name}`
  const invoked = [...invokerSources].some(([source, text]) => source !== own && text.includes(name))
  if (invoked || manualSmokes.has(name)) wiredSmokes += 1
  else orphanSmokes.push(name)
}
// Обратная сторона списка исключений: запись про удалённый файл остаётся
// лежать и делает вид, что что-то проверяется.
for (const [name] of manualSmokes) {
  if (!smokeFiles.includes(name)) errors.push(`smoke coverage: manual smoke scripts/${name} is listed but missing`)
}
for (const name of orphanSmokes) {
  errors.push(`smoke coverage: scripts/${name} is not invoked by any runner, npm script or workflow`)
}
// Сторож на случай, когда проверка ослепла сама: если «живых» смоуков вдруг
// почти нет, сломан обход, а не дерево.
if (smokeFiles.length && wiredSmokes < smokeFiles.length / 2) {
  errors.push(`smoke coverage: only ${wiredSmokes} of ${smokeFiles.length} smokes look invoked — the scan itself is broken`)
}

// Имя VSIX несёт версию, и это имя лежит в пяти местах.
//
// `check-quality-gate` сверяет версию в трёх источниках: ядро, frontend,
// расширение. Путь к VSIX — четвёртый, и его не сверял никто: `package.json`
// собирает `point-ide-1.2.2.vsix`, а `ship-hub-extension.ps1`,
// `install-vscode-extension.ps1`, `package:verify` и README ищут файл с тем же
// именем. Подъём версии в трёх местах оставил бы сборку писать один файл, а
// выкладку — искать другой, и разошлось бы это молча: сборка отработает, а
// выкладка скажет «нет файла».
//
// Обход идёт по тем же корням, что и проверка смоуков, плюс markdown в корне и
// в docs. Сторож на случай, когда ослепла сама проверка: находок должно быть
// не меньше трёх.
const vsixVersion = JSON.parse(extensionPackage).version
const vsixPattern = /point-ide-(\d+\.\d+\.\d+)\.vsix/g
const vsixSources = new Map(invokerSources)
for (const relative of ['README.md', 'CONTRIBUTING.md']) {
  if (fs.existsSync(path.join(root, relative))) vsixSources.set(relative, read(relative))
}
for (const entry of fs.readdirSync(path.join(root, 'docs'), { withFileTypes: true })) {
  if (entry.isFile() && entry.name.endsWith('.md')) {
    vsixSources.set(`docs/${entry.name}`, read(`docs/${entry.name}`))
  }
}
let vsixMentions = 0
for (const [source, text] of vsixSources) {
  for (const match of text.matchAll(vsixPattern)) {
    vsixMentions += 1
    if (match[1] !== vsixVersion) {
      errors.push(`vsix naming: ${source} names ${match[0]}, extension version is ${vsixVersion}`)
    }
  }
}
if (vsixMentions < 3) {
  errors.push(`vsix naming: only ${vsixMentions} mentions found — the scan itself is broken`)
}

if (errors.length) {
  console.error(`Release contract check failed (${errors.length}):`)
  for (const error of errors) console.error(`- ${error}`)
  process.exit(1)
}

console.log(JSON.stringify({
  releaseContracts: 'ok',
  workflowContracts: workflowContracts.length,
  coreSLOs: positiveCore.length,
  desktopSLOs: positiveDesktop.length,
  canonicalClient: 'Code-OSS',
  alternateClient: 'Wails diagnostic',
  smokes: smokeFiles.length,
  manualSmokes: [...manualSmokes.keys()],
  vsixMentions,
}))
