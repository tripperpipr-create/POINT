param(
  [string]$Executable = '',
  [int]$WaitSeconds = 7,
  [string]$Screenshot = ''
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) {
  $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe'
}
$Executable = [System.IO.Path]::GetFullPath($Executable)
if (-not (Test-Path -LiteralPath $Executable)) {
  throw "Point executable is missing: $Executable"
}

$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
$listener.Stop()

$testRoot = Join-Path $buildRoot ("terminal-lazy-$([guid]::NewGuid().ToString('N'))")
$userData = Join-Path $testRoot 'user-data'
$extensions = Join-Path $testRoot 'extensions'
$chromiumLog = Join-Path $testRoot 'chromium.log'
New-Item -ItemType Directory -Force -Path $userData, $extensions | Out-Null
$process = $null
$ownedIds = @()

function Get-ExtensionHostStarts([string]$LogRoot) {
  if (-not (Test-Path -LiteralPath $LogRoot)) { return @() }
  return @(Get-ChildItem -LiteralPath $LogRoot -File -Recurse -ErrorAction SilentlyContinue |
    Select-String -Pattern 'Started local extension host with pid|Extension host with pid .* started' -CaseSensitive:$false |
    ForEach-Object { "$($_.Path):$($_.LineNumber):$($_.Line)" })
}

function Get-ExtensionHostPids([object[]]$LogLines) {
  $pids = [System.Collections.Generic.HashSet[int]]::new()
  foreach ($line in $LogLines) {
    if ([string]$line -match 'pid\D+(\d+)') { [void]$pids.Add([int]$Matches[1]) }
  }
  return @($pids)
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

try {
  $arguments = @(
    '--user-data-dir', $userData,
    '--extensions-dir', $extensions,
    "--remote-debugging-port=$port",
    '--enable-logging', "--log-file=$chromiumLog", '--v=1',
    '--disable-updates', '--skip-welcome', '--skip-release-notes',
    (Join-Path $projectRoot 'examples\go-health')
  )
  $process = Start-Process -FilePath $Executable -ArgumentList $arguments -WindowStyle Hidden -PassThru
  Start-Sleep -Seconds $WaitSeconds

  $logRoot = Join-Path $userData 'logs'
  $beforeStarts = @(Get-ExtensionHostStarts $logRoot)
  if ($beforeStarts.Count -ne 0) {
    throw "Point started the extension host before terminal activation:`n$($beforeStarts -join [Environment]::NewLine)"
  }

  $endpoint = "http://127.0.0.1:$port"
  $nodeArguments = @((Join-Path $PSScriptRoot 'verify-point-terminal-lazy.mjs'), $endpoint)
  if ($Screenshot) { $nodeArguments += [System.IO.Path]::GetFullPath($Screenshot) }
  & node @nodeArguments
  if ($LASTEXITCODE -ne 0) { throw 'Point terminal UI verification failed.' }
  Start-Sleep -Seconds 5

  $afterStarts = @(Get-ExtensionHostStarts $logRoot)
  $afterHostPids = @(Get-ExtensionHostPids $afterStarts)
  $debugActivationLines = @()
  if (Test-Path -LiteralPath $logRoot) {
    $debugActivationLines = @(Get-ChildItem -LiteralPath $logRoot -File -Recurse -ErrorAction SilentlyContinue |
      Select-String -Pattern 'debug-auto-launch' -CaseSensitive:$false |
      ForEach-Object { "$($_.Path):$($_.LineNumber):$($_.Line)" })
  }
  $owned = @(Get-OwnedProcesses $process.Id)
  $ownedIds = @($owned | ForEach-Object { [int]$_.ProcessId })
  $ownedIdSet = [System.Collections.Generic.HashSet[int]]::new()
  foreach ($ownedId in $ownedIds) { [void]$ownedIdSet.Add($ownedId) }
  $runningExtensionHostPids = @($afterHostPids | Where-Object { $ownedIdSet.Contains([int]$_) })
  $coreProcesses = @($owned | Where-Object { $_.Name -match '^point-core(?:\.exe)?$' -or $_.CommandLine -match 'point-core(?:\.exe)?' })
  $result = [ordered]@{
    idleExtensionHostStarts = $beforeStarts.Count
    terminalExtensionHostLogLines = $afterStarts.Count
    terminalExtensionHostPids = $afterHostPids
    runningExtensionHostProcesses = $runningExtensionHostPids.Count
    debugAutoLaunchLogMatches = $debugActivationLines.Count
    localCoreProcesses = $coreProcesses.Count
    processCount = $owned.Count
  }
  if ($afterHostPids.Count -ne 1 -or $runningExtensionHostPids.Count -ne 1 -or $debugActivationLines.Count -lt 1 -or $coreProcesses.Count -gt 0) {
    throw "Point lazy terminal verification failed: $($result | ConvertTo-Json -Compress)"
  }
  Write-Output ''
  $result | ConvertTo-Json
} finally {
  if ($process) {
    if ($ownedIds.Count -eq 0) {
      $ownedIds = @((Get-OwnedProcesses $process.Id) | ForEach-Object { [int]$_.ProcessId })
    }
    if ($ownedIds.Count -gt 0) {
      Stop-Process -Id ($ownedIds | Sort-Object -Descending) -Force -ErrorAction SilentlyContinue
      Start-Sleep -Seconds 1
    }
  }
  $resolvedTest = [System.IO.Path]::GetFullPath($testRoot)
  $buildPrefix = $buildRoot + [System.IO.Path]::DirectorySeparatorChar
  if ($resolvedTest.StartsWith($buildPrefix, [System.StringComparison]::OrdinalIgnoreCase) -and (Test-Path -LiteralPath $resolvedTest)) {
    Remove-Item -LiteralPath $resolvedTest -Recurse -Force -ErrorAction SilentlyContinue
  }
}
