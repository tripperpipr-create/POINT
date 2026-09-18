param(
  [string]$Executable = '',
  [int]$WaitSeconds = 8,
  [string]$ScreenshotPrefix = ''
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
$testRoot = Join-Path $buildRoot ("navigation-$([guid]::NewGuid().ToString('N'))")
$userData = Join-Path $testRoot 'user-data'
$extensions = Join-Path $testRoot 'extensions'
$workspace = Join-Path $projectRoot 'examples\polyglot-navigation'
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
    '--disable-updates', '--skip-welcome', '--skip-release-notes',
    $workspace
  )
  $process = Start-Process -FilePath $Executable -ArgumentList $arguments -WindowStyle Hidden -PassThru
  Start-Sleep -Seconds $WaitSeconds
  $endpoint = "http://127.0.0.1:$port"
  $screenshotPath = if ($ScreenshotPrefix) { [System.IO.Path]::GetFullPath((Join-Path $projectRoot $ScreenshotPrefix)) } else { '' }
  & node (Join-Path $PSScriptRoot 'verify-point-navigation.mjs') $endpoint $screenshotPath
  if ($LASTEXITCODE -ne 0) { throw 'Point navigation UI verification failed.' }

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
  $runtimeErrors = @(Get-ChildItem -LiteralPath $logRoot -File -Recurse -ErrorAction SilentlyContinue |
    Select-String -Pattern 'Activating extension .* failed|Cannot find module|No default agent contributed' -CaseSensitive:$false)
  $result = [ordered]@{
    processCount = $owned.Count
    extensionHostPids = @($hostPids)
    localCoreProcesses = $coreProcesses.Count
    authenticationPrompts = $authPrompts.Count
    runtimeErrors = $runtimeErrors.Count
    screenshotPrefix = $screenshotPath
  }
  # Navigation shares the permanent Assistant runtime; it must reuse the one
  # workspace core instead of suppressing it or starting another process.
  if ($hostPids.Count -ne 1 -or $coreProcesses.Count -ne 1 -or $authPrompts.Count -gt 0 -or $runtimeErrors.Count -gt 0) {
    throw "Point navigation process verification failed: $($result | ConvertTo-Json -Compress)"
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
