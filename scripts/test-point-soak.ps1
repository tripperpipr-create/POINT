param(
  [ValidateSet('quick', 'eight-hour', 'twenty-four-hour')]
  [string]$Profile = 'quick',
  [string]$ConfigPath = '',
  [string]$ReportPath = '',
  [string]$Executable = '',
  [string]$Workspace = '',
  [string]$DockerImage = 'point-agent-sandbox:release',
  [switch]$SkipCodeOSSFault,
  [switch]$SkipDockerFault
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $ConfigPath) { $ConfigPath = Join-Path $projectRoot 'distribution\soak-profile.json' }
if (-not $ReportPath) { $ReportPath = Join-Path $buildRoot "soak-$Profile-report.json" }
if (-not $Executable) { $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe' }
if (-not $Workspace) { $Workspace = Join-Path $projectRoot 'examples\go-health' }
$ConfigPath = [System.IO.Path]::GetFullPath($ConfigPath)
$ReportPath = [System.IO.Path]::GetFullPath($ReportPath)
$Executable = [System.IO.Path]::GetFullPath($Executable)
$Workspace = [System.IO.Path]::GetFullPath($Workspace)
foreach ($required in @($ConfigPath, $Executable, $Workspace)) {
  if (-not (Test-Path -LiteralPath $required)) { throw "Soak input is missing: $required" }
}

function Measure-SoakDesktopProbe([int]$Sequence) {
  $sampleJson = & (Join-Path $PSScriptRoot 'measure-point-performance.ps1') `
    -Executable $Executable -Workspace $Workspace -Mode agent `
    -HubCycles ([int]$selected.hubCyclesPerProbe) -HubCycleSettleSeconds 1
  if ($LASTEXITCODE -ne 0) { throw "isolated Code-OSS Hub probe $Sequence failed" }
  $measured = (($sampleJson | Out-String) | ConvertFrom-Json)
  return [pscustomobject]@{
    sequence = $Sequence
    observedAt = [DateTimeOffset]::UtcNow.ToString('o')
    sample = [ordered]@{
      timing = [ordered]@{
        workbenchVisibleMs = [double]$measured.timing.workbenchVisibleMs
        agentUsableAfterClickMs = [double]$measured.timing.agentUsableAfterClickMs
        twoFrameLatencyMs = [double]$measured.timing.twoFrameLatencyMs
      }
      processCount = [int]$measured.processCount
      extensionHostCount = [int]$measured.extensionHostCount
      pointCoreCount = [int]$measured.pointCoreCount
      totalPrivateMB = [double]$measured.totalPrivateMB
      hubOpenClose = [ordered]@{
        requestedCycles = [int]$measured.hubOpenClose.requestedCycles
        completedCycles = [int]$measured.hubOpenClose.completedCycles
        baselinePrivateMB = [double]$measured.hubOpenClose.baselinePrivateMB
        finalPrivateMB = [double]$measured.hubOpenClose.finalPrivateMB
        privateGrowthPercent = [double]$measured.hubOpenClose.privateGrowthPercent
        maxOpenMs = [double]$measured.hubOpenClose.controller.maxOpenMs
        maxCloseMs = [double]$measured.hubOpenClose.controller.maxCloseMs
      }
    }
  }
}
$config = Get-Content -Raw -LiteralPath $ConfigPath | ConvertFrom-Json
$selected = $config.profiles.$Profile
if ($config.schemaVersion -ne 1 -or $null -eq $selected) { throw 'Invalid soak profile configuration.' }
if ($selected.releaseQualifying -and ($SkipCodeOSSFault -or $SkipDockerFault)) {
  throw 'A release-qualifying soak cannot skip Code-OSS or Docker fault injection.'
}

$runId = [guid]::NewGuid().ToString('N')
$runRoot = Join-Path $buildRoot "soak-controller-$runId"
$binary = Join-Path $runRoot 'point-soak.exe'
$stdoutPath = Join-Path $runRoot 'core-stdout.json'
$stderrPath = Join-Path $runRoot 'core-stderr.log'
$coreReportPath = Join-Path $runRoot 'core-report.json'
[System.IO.Directory]::CreateDirectory($runRoot) | Out-Null

Push-Location $projectRoot
try {
  & go build -trimpath -o $binary ./cmd/point-soak
  if ($LASTEXITCODE -ne 0) { throw 'Could not build point-soak.' }
} finally {
  Pop-Location
}

$core = $null
$coreExitCode = -1
$containerName = "point-soak-fault-$runId"
$codeOSSRestarts = 0
$dockerRestarts = 0
$dockerStopObserved = $false
$orphanSandboxContainers = -1
$controllerFailures = [System.Collections.Generic.List[string]]::new()
$desktopEvidence = [System.Collections.Generic.List[object]]::new()
$dockerEvidence = [System.Collections.Generic.List[object]]::new()
try {
  $arguments = @('-profile', $Profile, '-config', $ConfigPath, '-output', $coreReportPath)
  $core = Start-Process -FilePath $binary -ArgumentList $arguments -WindowStyle Hidden -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath -PassThru

  if (-not $SkipDockerFault) {
    try {
      $created = & docker create --name $containerName --network none --read-only --cap-drop ALL --security-opt no-new-privileges --entrypoint sh $DockerImage -lc 'while true; do sleep 60; done'
      if ($LASTEXITCODE -ne 0 -or -not $created) { throw 'docker create failed' }
      & docker start $containerName | Out-Null
      if ($LASTEXITCODE -ne 0) { throw 'docker start failed' }
      & docker stop --time 2 $containerName | Out-Null
      if ($LASTEXITCODE -ne 0) { throw 'docker stop fault failed' }
      $stopped = (& docker inspect --format '{{.State.Running}}' $containerName | Out-String).Trim()
      if ($LASTEXITCODE -ne 0 -or $stopped -ne 'false') { throw 'dedicated Docker workload did not stop' }
      $dockerStopObserved = $true
      & docker start $containerName | Out-Null
      if ($LASTEXITCODE -ne 0) { throw 'docker recovery start failed' }
      $running = (& docker inspect --format '{{.State.Running}}' $containerName | Out-String).Trim()
      if ($LASTEXITCODE -ne 0 -or $running -ne 'true') { throw 'dedicated fault container did not recover' }
      $dockerRestarts = 1
      [void]$dockerEvidence.Add([ordered]@{ container = $containerName; image = $DockerImage; network = 'none'; readOnly = $true; stopObserved = $dockerStopObserved; recovered = $true })
    } catch {
      [void]$controllerFailures.Add("Docker fault: $($_.Exception.Message)")
    }
  }

  if (-not $SkipCodeOSSFault) {
    try {
      [void]$desktopEvidence.Add((Measure-SoakDesktopProbe 1))
      $codeOSSRestarts = 1
      $nextDesktopProbe = [DateTimeOffset]::UtcNow.AddSeconds([int]$selected.desktopProbeEverySeconds)
      while (-not $core.WaitForExit(1000)) {
        if ([DateTimeOffset]::UtcNow -lt $nextDesktopProbe) { continue }
        [void]$desktopEvidence.Add((Measure-SoakDesktopProbe ($codeOSSRestarts + 1)))
        $codeOSSRestarts++
        $nextDesktopProbe = [DateTimeOffset]::UtcNow.AddSeconds([int]$selected.desktopProbeEverySeconds)
      }
      while ($codeOSSRestarts -lt 2) {
        [void]$desktopEvidence.Add((Measure-SoakDesktopProbe ($codeOSSRestarts + 1)))
        $codeOSSRestarts++
      }
    } catch {
      [void]$controllerFailures.Add("Code-OSS/Hub fault probe: $($_.Exception.Message)")
    }
  } else {
    $core.WaitForExit()
  }

  if ($core.HasExited) {
    $core.Refresh()
    $coreExitCode = [int]$core.ExitCode
  }
} finally {
  if ($core -and -not $core.HasExited) { Stop-Process -Id $core.Id -Force -ErrorAction SilentlyContinue }
  if (-not $SkipDockerFault) {
    & docker rm -f $containerName 2>$null | Out-Null
    $remaining = (& docker ps -a --filter "name=^/$containerName$" --format '{{.Names}}' 2>$null | Out-String).Trim()
    $orphanSandboxContainers = if ($remaining) { 1 } else { 0 }
    if ($remaining) { [void]$controllerFailures.Add("Disposable Docker fault container remained after cleanup: $remaining") }
  }
}

if (-not (Test-Path -LiteralPath $coreReportPath)) {
  $detail = if (Test-Path -LiteralPath $stderrPath) { Get-Content -Raw -LiteralPath $stderrPath } else { 'no stderr' }
  throw "Core soak report is missing. $detail"
}
$report = Get-Content -Raw -LiteralPath $coreReportPath | ConvertFrom-Json
$report.faults.codeOSSRestarts = $codeOSSRestarts
$report.faults.dockerRestarts = $dockerRestarts
$expectedDesktopProbes = if ($selected.releaseQualifying) {
  [math]::Max(2, [math]::Floor([double]$selected.durationSeconds / [double]$selected.desktopProbeEverySeconds))
} else { 2 }
$hubCycles = 0
$uiInteractive = $desktopEvidence.Count -ge $expectedDesktopProbes
foreach ($entry in $desktopEvidence) {
  $sample = $entry.sample
  $cycles = $sample.hubOpenClose
  $hubCycles += [int]$cycles.completedCycles
  if ($null -eq $sample.timing -or [double]$sample.timing.agentUsableAfterClickMs -gt 6500 -or
      [double]$sample.timing.twoFrameLatencyMs -gt 60 -or $sample.extensionHostCount -ne 1 -or
      $sample.pointCoreCount -ne 1 -or $null -eq $cycles -or
      [int]$cycles.completedCycles -ne [int]$selected.hubCyclesPerProbe -or
      [double]$cycles.privateGrowthPercent -gt 10) {
    $uiInteractive = $false
  }
}
$externalVerified = $codeOSSRestarts -ge 2 -and $dockerRestarts -ge 1 -and $dockerStopObserved -and
  $orphanSandboxContainers -eq 0 -and $uiInteractive
$report.externalFaultsVerified = $externalVerified
$report.releaseQualified = [bool]($report.releaseProfile -and $report.coreCriteriaPassed -and $externalVerified -and $controllerFailures.Count -eq 0)
$report | Add-Member -Force -NotePropertyName controller -NotePropertyValue ([ordered]@{
  schemaVersion = 1
  desktopProbeEverySeconds = [int]$selected.desktopProbeEverySeconds
  expectedDesktopProbes = [int]$expectedDesktopProbes
  hubCycles = $hubCycles
  uiInteractive = $uiInteractive
  dockerStopObserved = $dockerStopObserved
  orphanSandboxContainers = $orphanSandboxContainers
  codeOSSEvidence = @($desktopEvidence)
  dockerEvidence = @($dockerEvidence)
  failures = @($controllerFailures)
  coreExitCode = $coreExitCode
  stderrPath = $stderrPath
})
$allFailures = @($report.failures) + @($controllerFailures)
$report.failures = @($allFailures)

$parent = [System.IO.Path]::GetDirectoryName($ReportPath)
[System.IO.Directory]::CreateDirectory($parent) | Out-Null
[System.IO.File]::WriteAllText($ReportPath, (($report | ConvertTo-Json -Depth 16) + [Environment]::NewLine), [System.Text.UTF8Encoding]::new($false))
$report | ConvertTo-Json -Depth 16
if ($coreExitCode -ne 0 -or $controllerFailures.Count -gt 0 -or -not $report.coreCriteriaPassed) {
  throw "Point soak failed: $(@($report.failures) -join '; ')"
}
if ($report.releaseProfile -and -not $report.releaseQualified) { throw 'Release soak did not collect every required external fault.' }
