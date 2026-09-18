param(
  [string]$Executable = '',
  [ValidateSet('idle', 'agent')]
  [string]$Mode = 'idle',
  [string]$Workspace = '',
  [int]$SettleSeconds = 5,
  [ValidateRange(0, 200)]
  [int]$HubCycles = 0,
  [ValidateRange(1, 120)]
  [int]$HubCycleSettleSeconds = 15,
  [switch]$DisableGpu,
  [switch]$DisableCrashReporter,
  [switch]$DisableSoftwareRasterizer
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) { $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe' }
$Executable = [System.IO.Path]::GetFullPath($Executable)
if (-not (Test-Path -LiteralPath $Executable)) { throw "Point executable is missing: $Executable" }
if (-not $Workspace) { $Workspace = Join-Path $projectRoot 'examples\go-health' }
$Workspace = [System.IO.Path]::GetFullPath($Workspace)
if (-not (Test-Path -LiteralPath $Workspace -PathType Container)) { throw "Point test workspace is missing: $Workspace" }

function Get-FreePort {
  $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
  $listener.Start()
  $port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
  $listener.Stop()
  return $port
}

function Get-OwnedProcesses([int]$RootId) {
  $all = @(Get-CimInstance Win32_Process)
  $ids = [System.Collections.Generic.HashSet[int]]::new()
  [void]$ids.Add($RootId)
  do {
    $before = $ids.Count
    foreach ($item in $all) {
      if ($ids.Contains([int]$item.ParentProcessId)) { [void]$ids.Add([int]$item.ProcessId) }
    }
  } while ($ids.Count -gt $before)
  return @($all | Where-Object { $ids.Contains([int]$_.ProcessId) })
}

function Get-ProcessSnapshot([int]$RootId, [string]$LogsRoot) {
  $owned = @(Get-OwnedProcesses $RootId)
  # Recent Electron versions host extensions in a generic NodeService utility
  # process; workbench logs are the stable PID attribution contract.
  $extensionHostIds = [System.Collections.Generic.HashSet[int]]::new()
  if (Test-Path -LiteralPath $LogsRoot) {
    Get-ChildItem -LiteralPath $LogsRoot -File -Recurse -ErrorAction SilentlyContinue |
      Select-String -Pattern 'Started local extension host with pid|Extension host with pid .* started' -CaseSensitive:$false |
      ForEach-Object {
        if ($_.Line -match '(?:pid\s+)(\d+)') { [void]$extensionHostIds.Add([int]$Matches[1]) }
      }
  }
  $memory = @($owned | ForEach-Object {
    $native = Get-Process -Id ([int]$_.ProcessId) -ErrorAction SilentlyContinue
    $kind = if ($_.Name -match '^point-core(?:\.exe)?$' -or $_.CommandLine -match 'point-core(?:\.exe)?') {
      'point-core'
    } elseif ($extensionHostIds.Contains([int]$_.ProcessId) -or $_.CommandLine -match 'extensionHost') {
      'extension-host'
    } elseif ($_.CommandLine -match 'shared-process') {
      'shared-process'
    } elseif ($_.CommandLine -match 'ptyHost') {
      'pty-host'
    } elseif ($_.CommandLine -match 'fileWatcher') {
      'file-watcher'
    } elseif ($_.CommandLine -match '--type=([^\s"]+)') {
      $Matches[1]
    } else {
      'main'
    }
    [pscustomobject]@{
      pid = [int]$_.ProcessId
      kind = $kind
      privateMB = if ($native) { [math]::Round($native.PrivateMemorySize64 / 1MB, 1) } else { 0 }
      workingSetMB = [math]::Round([double]$_.WorkingSetSize / 1MB, 1)
      command = if ($_.CommandLine) { [string]$_.CommandLine } else { '' }
    }
  })
  return [pscustomobject]@{
    owned = $owned
    memory = $memory
    totalPrivateMB = [math]::Round(($memory | Measure-Object privateMB -Sum).Sum, 1)
    totalWorkingSetMB = [math]::Round(($memory | Measure-Object workingSetMB -Sum).Sum, 1)
  }
}

$port = Get-FreePort
$testRoot = Join-Path $buildRoot ("performance-$Mode-$([guid]::NewGuid().ToString('N'))")
$userData = Join-Path $testRoot 'user-data'
$extensions = Join-Path $testRoot 'extensions'
New-Item -ItemType Directory -Force -Path $userData, $extensions | Out-Null
$process = $null
$ownedIds = @()
try {
  $arguments = @(
    '--user-data-dir', $userData,
    '--extensions-dir', $extensions,
    "--remote-debugging-port=$port",
    '--disable-updates', '--skip-welcome', '--skip-release-notes',
    $Workspace
  )
  if ($DisableGpu) { $arguments = @('--disable-gpu') + $arguments }
  if ($DisableCrashReporter) { $arguments = @('--disable-crash-reporter') + $arguments }
  if ($DisableSoftwareRasterizer) { $arguments = @('--disable-software-rasterizer') + $arguments }
  $startedAt = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
  $process = Start-Process -FilePath $Executable -ArgumentList $arguments -WindowStyle Hidden -PassThru
  $timingJson = & node (Join-Path $PSScriptRoot 'measure-point-startup.mjs') "http://127.0.0.1:$port" $startedAt $Mode
  if ($LASTEXITCODE -ne 0) { throw 'Point timing probe failed.' }
  $timing = $timingJson | ConvertFrom-Json
  Start-Sleep -Seconds $SettleSeconds
  $logsRoot = Join-Path $userData 'logs'
  $snapshot = Get-ProcessSnapshot $process.Id $logsRoot
  $owned = @($snapshot.owned)
  $memory = @($snapshot.memory)
  $ownedIds = @($owned | ForEach-Object { [int]$_.ProcessId })
  $hubCycleEvidence = $null
  if ($Mode -eq 'agent' -and $HubCycles -gt 0) {
    $closeJson = & node (Join-Path $PSScriptRoot 'measure-point-hub-cycles.mjs') "http://127.0.0.1:$port" close 1 auto $timing.rendererTargetId
    if ($LASTEXITCODE -ne 0) { throw 'Could not close Agent Hub before the memory-growth cycle.' }
    $closeEvidence = $closeJson | ConvertFrom-Json
    if (-not $closeEvidence.workbenchTargetId) { throw 'Agent Hub cycle did not identify its owning workbench target.' }
    Start-Sleep -Seconds $HubCycleSettleSeconds
    $cycleBaseline = Get-ProcessSnapshot $process.Id $logsRoot
    $cycleJson = & node (Join-Path $PSScriptRoot 'measure-point-hub-cycles.mjs') "http://127.0.0.1:$port" cycle $HubCycles $closeEvidence.workbenchTargetId $timing.rendererTargetId $HubCycleSettleSeconds
    if ($LASTEXITCODE -ne 0) { throw 'Agent Hub open/close memory cycle failed.' }
    $cycleFinal = Get-ProcessSnapshot $process.Id $logsRoot
    $growth = if ($cycleBaseline.totalPrivateMB -gt 0) {
      [math]::Round((($cycleFinal.totalPrivateMB - $cycleBaseline.totalPrivateMB) / $cycleBaseline.totalPrivateMB) * 100, 2)
    } else { 100 }
    $hubCycleEvidence = [ordered]@{
      requestedCycles = $HubCycles
      completedCycles = [int](($cycleJson | ConvertFrom-Json).completedCycles)
      baselinePrivateMB = $cycleBaseline.totalPrivateMB
      finalPrivateMB = $cycleFinal.totalPrivateMB
      privateGrowthPercent = $growth
      baselineProcesses = @($cycleBaseline.memory | Sort-Object privateMB -Descending)
      finalProcesses = @($cycleFinal.memory | Sort-Object privateMB -Descending)
      controller = $cycleJson | ConvertFrom-Json
      initialClose = $closeEvidence
    }
  }
  $result = [ordered]@{
    timing = $timing
    processCount = $owned.Count
    extensionHostCount = @($memory | Where-Object kind -eq 'extension-host').Count
    pointCoreCount = @($memory | Where-Object kind -eq 'point-core').Count
    totalPrivateMB = $snapshot.totalPrivateMB
    totalWorkingSetMB = $snapshot.totalWorkingSetMB
    hubOpenClose = $hubCycleEvidence
    processes = @($memory | Sort-Object privateMB -Descending)
  }
  $result | ConvertTo-Json -Depth 8
} catch {
  $failure = $_
  $logsRoot = Join-Path $userData 'logs'
  if (Test-Path -LiteralPath $logsRoot) {
    $diagnosticFiles = @(Get-ChildItem -LiteralPath $logsRoot -File -Recurse -ErrorAction SilentlyContinue |
      Where-Object { $_.Name -match '^(main|renderer|exthost|window)\.log$' } |
      Sort-Object LastWriteTime -Descending |
      Select-Object -First 8)
    foreach ($logFile in $diagnosticFiles) {
      Write-Error ("Point performance failure log {0}:`n{1}" -f $logFile.FullName, ((Get-Content -LiteralPath $logFile.FullName -Tail 40 -ErrorAction SilentlyContinue) -join "`n")) -ErrorAction Continue
    }
  }
  throw $failure
} finally {
  if ($process) {
    $currentIds = @((Get-OwnedProcesses $process.Id) | ForEach-Object { [int]$_.ProcessId })
    $ownedIds = @($ownedIds + $currentIds | Sort-Object -Unique)
    if ($ownedIds.Count -gt 0) { Stop-Process -Id ($ownedIds | Sort-Object -Descending) -Force -ErrorAction SilentlyContinue }
  }
  $resolved = [System.IO.Path]::GetFullPath($testRoot)
  $prefix = $buildRoot + [System.IO.Path]::DirectorySeparatorChar
  if ($resolved.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase) -and (Test-Path -LiteralPath $resolved)) {
    Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction SilentlyContinue
  }
}
