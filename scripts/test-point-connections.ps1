param(
  [string]$Executable = '',
  [string]$Screenshot = ''
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) { $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe' }
$Executable = [System.IO.Path]::GetFullPath($Executable)
if (-not (Test-Path -LiteralPath $Executable)) { throw "Point executable is missing: $Executable" }

$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start(); $port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port; $listener.Stop()
$testRoot = Join-Path $buildRoot ("connections-e2e-$([guid]::NewGuid().ToString('N'))")
$workspace = Join-Path $testRoot 'workspace'
$userData = Join-Path $testRoot 'user-data'
$extensions = Join-Path $testRoot 'extensions'
$chromiumLog = Join-Path $testRoot 'chromium.log'
New-Item -ItemType Directory -Force -Path $workspace, $userData, $extensions | Out-Null
Copy-Item -LiteralPath (Join-Path $projectRoot 'examples\go-health\main.go') -Destination $workspace -Force
$process = $null
$ownedIds = @()

function Get-OwnedProcesses([int]$RootId) {
  $all = @(Get-CimInstance Win32_Process)
  $ids = [System.Collections.Generic.HashSet[int]]::new(); [void]$ids.Add($RootId)
  do {
    $before = $ids.Count
    foreach ($item in $all) { if ($ids.Contains([int]$item.ParentProcessId)) { [void]$ids.Add([int]$item.ProcessId) } }
  } while ($ids.Count -gt $before)
  return @($all | Where-Object { $ids.Contains([int]$_.ProcessId) })
}

try {
  $arguments = @(
    '--user-data-dir', $userData, '--extensions-dir', $extensions,
    "--remote-debugging-port=$port", '--enable-logging', "--log-file=$chromiumLog", '--v=1',
    '--disable-updates', '--skip-welcome', '--skip-release-notes', $workspace
  )
  $process = Start-Process -FilePath $Executable -ArgumentList $arguments -WindowStyle Hidden -PassThru
  Start-Sleep -Seconds 7
  $endpoint = "http://127.0.0.1:$port"
  & node (Join-Path $PSScriptRoot 'prepare-point-trusted-agent.mjs') $endpoint
  if ($LASTEXITCODE -ne 0) { throw 'Point Agent Hub preparation failed.' }
  $nodeArguments = @((Join-Path $PSScriptRoot 'verify-point-connections-e2e.mjs'), $endpoint)
  if ($Screenshot) { $nodeArguments += [System.IO.Path]::GetFullPath($Screenshot) }
  & node @nodeArguments
  if ($LASTEXITCODE -ne 0) { throw 'Point DB/SSH UI verification failed.' }
  if (-not (Test-Path -LiteralPath (Join-Path $workspace 'data\point-connections-e2e.db'))) {
    throw 'Point DB/SSH UI verification did not create the SQLite fixture.'
  }

  $owned = @(Get-OwnedProcesses $process.Id)
  $ownedIds = @($owned | ForEach-Object { [int]$_.ProcessId })
  $coreProcesses = @($owned | Where-Object { $_.Name -match '^point-core(?:\.exe)?$' -or $_.CommandLine -match 'point-core(?:\.exe)?' })
  $logRoot = Join-Path $userData 'logs'
  $authPrompts = @(Get-ChildItem -LiteralPath $logRoot -File -Recurse -ErrorAction SilentlyContinue |
    Select-String -Pattern "\[sessions welcome\] Showing sign-in dialog|Authentication is required to use Copilot|Timed out waiting for authentication provider" -CaseSensitive:$false)
  $runtimeErrors = @(Get-ChildItem -LiteralPath $logRoot -File -Recurse -ErrorAction SilentlyContinue |
    Select-String -Pattern 'Extension host.*terminated unexpectedly|renderer process gone|FATAL ERROR' -CaseSensitive:$false)
  $result = [ordered]@{
    processCount = $owned.Count
    localCoreProcesses = $coreProcesses.Count
    authenticationPrompts = $authPrompts.Count
    runtimeErrors = $runtimeErrors.Count
    sqliteCreated = $true
  }
  if ($coreProcesses.Count -ne 1 -or $authPrompts.Count -gt 0 -or $runtimeErrors.Count -gt 0) {
    throw "Point DB/SSH process verification failed: $($result | ConvertTo-Json -Compress)"
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
  $resolved = [System.IO.Path]::GetFullPath($testRoot)
  $prefix = $buildRoot + [System.IO.Path]::DirectorySeparatorChar
  if ($resolved.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase) -and (Test-Path -LiteralPath $resolved)) {
    Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction SilentlyContinue
  }
}
