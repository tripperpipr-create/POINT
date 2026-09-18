param(
  [string]$Executable = '',
  [int]$WaitSeconds = 7,
  [string]$Screenshot = '',
  [ValidateSet('composer', 'character')]
  [string]$View = 'composer'
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) { $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe' }
$Executable = [System.IO.Path]::GetFullPath($Executable)
if (-not (Test-Path -LiteralPath $Executable)) { throw "Point executable is missing: $Executable" }

$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
$listener.Stop()
$testRoot = Join-Path $buildRoot ("agent-context-$([guid]::NewGuid().ToString('N'))")
$userData = Join-Path $testRoot 'user-data'
$extensions = Join-Path $testRoot 'extensions'
$chromiumLog = Join-Path $testRoot 'chromium.log'
New-Item -ItemType Directory -Force -Path $userData, $extensions | Out-Null
$process = $null
$ownedIds = @()

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
  $endpoint = "http://127.0.0.1:$port"
  & node (Join-Path $PSScriptRoot 'prepare-point-trusted-agent.mjs') $endpoint
  if ($LASTEXITCODE -ne 0) { throw 'Point Agent context verification failed.' }
  & node (Join-Path $PSScriptRoot 'verify-point-agent-nav.mjs') $endpoint $View
  if ($LASTEXITCODE -ne 0) { throw "Point Agent $View verification failed." }
  if ($Screenshot) {
    Start-Sleep -Milliseconds 500
    & node (Join-Path $PSScriptRoot 'capture-cdp-page.mjs') $endpoint ([System.IO.Path]::GetFullPath($Screenshot))
    if ($LASTEXITCODE -ne 0) { throw 'Point Agent screenshot failed.' }
  }

  $owned = @(Get-OwnedProcesses $process.Id)
  $ownedIds = @($owned | ForEach-Object { [int]$_.ProcessId })
  $coreProcesses = @($owned | Where-Object { $_.Name -match '^point-core(?:\.exe)?$' -or $_.CommandLine -match 'point-core(?:\.exe)?' })
  $logRoot = Join-Path $userData 'logs'
  $hostLines = @(Get-ChildItem -LiteralPath $logRoot -File -Recurse -ErrorAction SilentlyContinue |
    Select-String -Pattern 'Started local extension host with pid|Extension host with pid .* started' -CaseSensitive:$false)
  $hostPids = [System.Collections.Generic.HashSet[int]]::new()
  foreach ($line in $hostLines) {
    if ([string]$line -match 'pid\D+(\d+)') { [void]$hostPids.Add([int]$Matches[1]) }
  }
  $authPrompts = @(Get-ChildItem -LiteralPath $logRoot -File -Recurse -ErrorAction SilentlyContinue |
    Select-String -Pattern "\[sessions welcome\] Showing sign-in dialog|Authentication is required to use Copilot|Timed out waiting for authentication provider" -CaseSensitive:$false)
  $result = [ordered]@{
    processCount = $owned.Count
    extensionHostPids = @($hostPids)
    extensionHostMode = if ($hostPids.Count -eq 1) { 'shared' } else { 'per-window' }
    localCoreProcesses = $coreProcesses.Count
    authenticationPrompts = $authPrompts.Count
    screenshot = $Screenshot
  }
  # The IDE and the dedicated Agent Hub are two workbench pages (proved above
  # through two live CDP page targets). Depending on the Code-OSS host model
  # they may share one extension host or own one each; both must still share
  # exactly one Point core process owned by this launch.
  if ($hostPids.Count -lt 1 -or $hostPids.Count -gt 2 -or $coreProcesses.Count -ne 1 -or $authPrompts.Count -gt 0) {
    throw "Point Agent process verification failed: $($result | ConvertTo-Json -Compress)"
  }
  Write-Output ''
  $result | ConvertTo-Json
} finally {
  if ($process) {
    if ($ownedIds.Count -eq 0) { $ownedIds = @((Get-OwnedProcesses $process.Id) | ForEach-Object { [int]$_.ProcessId }) }
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
