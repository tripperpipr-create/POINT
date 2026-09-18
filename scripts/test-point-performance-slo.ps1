param(
  [string]$Executable = '',
  [string]$Workspace = '',
  [string]$SloPath = '',
  [string]$ReportPath = ''
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) { $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe' }
if (-not $Workspace) { $Workspace = Join-Path $projectRoot 'examples\go-health' }
if (-not $SloPath) { $SloPath = Join-Path $projectRoot 'distribution\performance-slo.json' }
if (-not $ReportPath) { $ReportPath = Join-Path $buildRoot 'performance-slo-report.json' }
$Executable = [System.IO.Path]::GetFullPath($Executable)
$Workspace = [System.IO.Path]::GetFullPath($Workspace)
$SloPath = [System.IO.Path]::GetFullPath($SloPath)
$ReportPath = [System.IO.Path]::GetFullPath($ReportPath)

foreach ($required in @($Executable, $Workspace, $SloPath)) {
  if (-not (Test-Path -LiteralPath $required)) { throw "Performance input is missing: $required" }
}
$slo = Get-Content -Raw -LiteralPath $SloPath | ConvertFrom-Json
if ($slo.schemaVersion -ne 1 -or $slo.desktop.samples -lt 1) { throw 'Invalid performance SLO configuration.' }

Push-Location $projectRoot
try {
  $coreJson = & go run ./cmd/point-performance-probe -slo $SloPath
  if ($LASTEXITCODE -ne 0) { throw 'Core performance SLO probe failed.' }
} finally {
  Pop-Location
}
$core = ($coreJson | Out-String) | ConvertFrom-Json
if (@($core.failures).Count -gt 0) { throw "Core performance failures: $(@($core.failures) -join '; ')" }

$samples = [System.Collections.Generic.List[object]]::new()
for ($index = 1; $index -le [int]$slo.desktop.samples; $index++) {
  foreach ($mode in @('idle', 'agent')) {
    $hubCycles = if ($mode -eq 'agent' -and $index -eq 1) { [int]$slo.desktop.hubOpenCloseCycles } else { 0 }
    $sampleJson = & (Join-Path $PSScriptRoot 'measure-point-performance.ps1') -Executable $Executable -Workspace $Workspace -Mode $mode -HubCycles $hubCycles
    if ($LASTEXITCODE -ne 0) { throw "Desktop performance sample $index/$mode failed." }
    $sample = ($sampleJson | Out-String) | ConvertFrom-Json
    $sample | Add-Member -NotePropertyName sample -NotePropertyValue $index
    [void]$samples.Add($sample)
  }
}

$idle = @($samples | Where-Object { $_.timing.mode -eq 'idle' })
$agent = @($samples | Where-Object { $_.timing.mode -eq 'agent' })
$all = @($samples)
$cycleSamples = @($agent | Where-Object { $null -ne $_.hubOpenClose })
if ($idle.Count -ne [int]$slo.desktop.samples -or $agent.Count -ne [int]$slo.desktop.samples) {
  throw 'Desktop performance probe did not return every required sample.'
}

function Maximum([object[]]$Values, [string]$Property) {
  return [double](($Values | ForEach-Object { [double]($_.$Property) } | Measure-Object -Maximum).Maximum)
}

$aggregate = [ordered]@{
  workbenchVisibleMs = [math]::Round((Maximum @($all | ForEach-Object { $_.timing }) 'workbenchVisibleMs'), 1)
  agentUsableAfterClickMs = [math]::Round((Maximum @($agent | ForEach-Object { $_.timing }) 'agentUsableAfterClickMs'), 1)
  twoFrameLatencyMs = [math]::Round((Maximum @($all | ForEach-Object { $_.timing }) 'twoFrameLatencyMs'), 1)
  idlePrivateMB = [math]::Round((Maximum $idle 'totalPrivateMB'), 1)
  agentPrivateMB = [math]::Round((Maximum $agent 'totalPrivateMB'), 1)
  processCount = [int](Maximum $all 'processCount')
  hubCyclePrivateGrowthPercent = if ($cycleSamples.Count) { [math]::Round((Maximum @($cycleSamples | ForEach-Object { $_.hubOpenClose }) 'privateGrowthPercent'), 2) } else { 100 }
}

$failures = [System.Collections.Generic.List[string]]::new()
function Assert-Maximum([string]$Name, [double]$Actual, [double]$Limit) {
  if ($Actual -gt $Limit) { [void]$failures.Add("$Name $Actual exceeds $Limit") }
}
Assert-Maximum 'workbenchVisibleMs' $aggregate.workbenchVisibleMs ([double]$slo.desktop.workbenchVisibleMs)
Assert-Maximum 'agentUsableAfterClickMs' $aggregate.agentUsableAfterClickMs ([double]$slo.desktop.agentUsableAfterClickMs)
Assert-Maximum 'twoFrameLatencyMs' $aggregate.twoFrameLatencyMs ([double]$slo.desktop.twoFrameLatencyMs)
Assert-Maximum 'idlePrivateMB' $aggregate.idlePrivateMB ([double]$slo.desktop.idlePrivateMB)
Assert-Maximum 'agentPrivateMB' $aggregate.agentPrivateMB ([double]$slo.desktop.agentPrivateMB)
Assert-Maximum 'processCount' $aggregate.processCount ([double]$slo.desktop.processCount)
Assert-Maximum 'hubCyclePrivateGrowthPercent' $aggregate.hubCyclePrivateGrowthPercent ([double]$slo.desktop.hubCyclePrivateGrowthPercent)
if ($cycleSamples.Count -ne 1) { [void]$failures.Add("expected one Hub open/close cycle sample; got $($cycleSamples.Count)") }
elseif ([int]$cycleSamples[0].hubOpenClose.completedCycles -ne [int]$slo.desktop.hubOpenCloseCycles) {
  [void]$failures.Add("Hub open/close cycles $($cycleSamples[0].hubOpenClose.completedCycles); expected $($slo.desktop.hubOpenCloseCycles)")
}
foreach ($sample in $all) {
  if ([int]$sample.pointCoreCount -ne 1) { [void]$failures.Add("sample $($sample.sample)/$($sample.timing.mode) has $($sample.pointCoreCount) point-core processes") }
  $expectedExtensionHosts = if ($sample.timing.mode -eq 'agent') { [int]$slo.desktop.agentExtensionHosts } else { [int]$slo.desktop.idleExtensionHosts }
  if ([int]$sample.extensionHostCount -ne $expectedExtensionHosts) {
    [void]$failures.Add("sample $($sample.sample)/$($sample.timing.mode) has $($sample.extensionHostCount) extension hosts; expected $expectedExtensionHosts")
  }
}

$report = [ordered]@{
  schemaVersion = 1
  measuredAt = [DateTime]::UtcNow.ToString('o')
  executable = $Executable
  workspace = $Workspace
  core = $core
  desktop = [ordered]@{
    sampleCountPerMode = [int]$slo.desktop.samples
    aggregation = 'worst observed value; no composite score'
    thresholds = $slo.desktop
    aggregate = $aggregate
    samples = @($samples)
    failures = @($failures)
  }
}
$parent = [System.IO.Path]::GetDirectoryName($ReportPath)
[System.IO.Directory]::CreateDirectory($parent) | Out-Null
[System.IO.File]::WriteAllText($ReportPath, (($report | ConvertTo-Json -Depth 12) + [Environment]::NewLine), [System.Text.UTF8Encoding]::new($false))
$report | ConvertTo-Json -Depth 12
if ($failures.Count -gt 0) { throw "Desktop performance SLO failed: $($failures -join '; ')" }
