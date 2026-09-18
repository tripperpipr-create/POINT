param(
  [string]$Executable = '',
  [int]$WaitSeconds = 14,
  [string]$IdeScreenshot = '',
  [string]$AgentsScreenshot = '',
  [ValidateSet('auxiliary', 'sessions')]
  [string]$WindowMode = 'sessions',
  [switch]$HubOnly,
  [switch]$KeepArtifacts
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) { $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe' }
$Executable = [System.IO.Path]::GetFullPath($Executable)
if (-not (Test-Path -LiteralPath $Executable)) { throw "Point executable is missing: $Executable" }
if (-not $IdeScreenshot) { $IdeScreenshot = Join-Path $buildRoot 'code-oss\point-split-ide.png' }
if (-not $AgentsScreenshot) { $AgentsScreenshot = Join-Path $buildRoot 'code-oss\point-split-agents.png' }
$IdeScreenshot = [System.IO.Path]::GetFullPath($IdeScreenshot)
$AgentsScreenshot = [System.IO.Path]::GetFullPath($AgentsScreenshot)

$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
$listener.Stop()
$testRoot = Join-Path $buildRoot ("split-windows-$([guid]::NewGuid().ToString('N'))")
$userData = Join-Path $testRoot 'user-data'
$extensions = Join-Path $testRoot 'extensions'
$chromiumLog = Join-Path $testRoot 'chromium.log'
New-Item -ItemType Directory -Force -Path $userData, $extensions | Out-Null
$fixtureLogDir = Join-Path $userData 'User\globalStorage\local-agent.local-agent-workbench\logs'
New-Item -ItemType Directory -Force -Path $fixtureLogDir | Out-Null
$fixtureLogPath = Join-Path $fixtureLogDir 'point-core.log'
$fixtureWriter = [System.IO.StreamWriter]::new($fixtureLogPath, $false, [System.Text.UTF8Encoding]::new($false))
try {
  for ($index = 0; $index -lt 90000; $index++) {
    $fixtureWriter.WriteLine('oversized legacy debug line used to verify bounded migration 0123456789')
  }
} finally {
  $fixtureWriter.Dispose()
}
$process = $null
$ownedIds = @()
$originalAuxiliaryHub = $env:POINT_AUXILIARY_HUB

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
  if ($WindowMode -eq 'auxiliary') { $env:POINT_AUXILIARY_HUB = '1' } else { Remove-Item Env:POINT_AUXILIARY_HUB -ErrorAction SilentlyContinue }
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
  $verifier = if ($HubOnly) { 'verify-point-agents-window.mjs' } else { 'verify-point-split-windows.mjs' }
  $verification = & node (Join-Path $PSScriptRoot $verifier) $endpoint $IdeScreenshot $AgentsScreenshot $WindowMode
  if ($LASTEXITCODE -ne 0) { throw 'Point split-window browser verification failed.' }

  Start-Sleep -Seconds 3
  $owned = @(Get-OwnedProcesses $process.Id)
  $ownedIds = @($owned | ForEach-Object { [int]$_.ProcessId })
  $coreProcesses = @($owned | Where-Object { $_.Name -match '^point-core(?:\.exe)?$' -or $_.CommandLine -match '[\\/]point-core(?:\.exe)?(?:\s|$)' })
  $sharedStorage = Join-Path $userData 'User\globalStorage\local-agent.local-agent-workbench'
  $runtimeDir = Join-Path $sharedStorage 'runtime'
  $runtimeDescriptors = @(Get-ChildItem -LiteralPath $runtimeDir -Filter 'core-*.json' -File -ErrorAction SilentlyContinue)
  $leases = @(Get-ChildItem -LiteralPath $runtimeDir -Filter 'lease-*.json' -File -ErrorAction SilentlyContinue)
  $descriptor = if ($runtimeDescriptors.Count -eq 1) { Get-Content -LiteralPath $runtimeDescriptors[0].FullName -Raw | ConvertFrom-Json } else { $null }
  $logRoot = Join-Path $userData 'logs'
  $activationErrors = @(Get-ChildItem -LiteralPath $logRoot -File -Recurse -ErrorAction SilentlyContinue |
    Select-String -Pattern 'Activating extension local-agent\.local-agent-workbench failed|Error activating extension local-agent\.local-agent-workbench|Cannot activate.*local-agent|No bundle location found for extension local-agent\.local-agent-workbench|local-agent\.local-agent-workbench.*(?:property|\p{L}+).*id|(?:point\.companion|pointCompanion).*(?:does not exist|\p{L}+)' -CaseSensitive:$false)
  $extensionHostIds = [System.Collections.Generic.HashSet[int]]::new()
  Get-ChildItem -LiteralPath $logRoot -File -Recurse -ErrorAction SilentlyContinue |
    Select-String -Pattern 'Started local extension host with pid|Extension host with pid .* started' -CaseSensitive:$false |
    ForEach-Object { if ($_.Line -match '(?:pid\s+)(\d+)') { [void]$extensionHostIds.Add([int]$Matches[1]) } }
  $extensionHostCount = @($owned | Where-Object { $extensionHostIds.Contains([int]$_.ProcessId) -or $_.CommandLine -match 'extensionHost' }).Count
  $rotatedLogs = @(Get-ChildItem -LiteralPath $fixtureLogDir -Filter 'point-core.log*' -File -ErrorAction SilentlyContinue)
  $oversizedLogs = @($rotatedLogs | Where-Object { $_.Length -gt (4 * 1024 * 1024) })
  $activeLogHead = if (Test-Path -LiteralPath $fixtureLogPath) { @(Get-Content -LiteralPath $fixtureLogPath -TotalCount 80) } else { @() }
  $usesInfoByDefault = [bool]($activeLogHead -match '\[log\] level=info\b')

  $result = [ordered]@{
    browser = $verification | ConvertFrom-Json
    windowMode = $WindowMode
    processCount = $owned.Count
    extensionHostProcesses = $extensionHostCount
    localCoreProcesses = $coreProcesses.Count
    runtimeDescriptors = $runtimeDescriptors.Count
    liveWindowLeases = $leases.Count
    sharedCorePid = $descriptor.pid
    sharedCoreUrl = $descriptor.baseUrl
    activationErrors = $activationErrors.Count
    logFiles = @($rotatedLogs | Sort-Object Name | ForEach-Object { [ordered]@{ name = $_.Name; bytes = $_.Length } })
    defaultLogLevel = if ($usesInfoByDefault) { 'info' } else { 'unknown' }
    installer = Join-Path $buildRoot 'code-oss\PointSetup-x64-1.124.2.exe'
  }
  $expectedLeaseCount = if ($WindowMode -eq 'auxiliary') { 1 } else { 2 }
  $expectedExtensionHosts = if ($WindowMode -eq 'auxiliary') { 1 } else { 2 }
  if ($coreProcesses.Count -ne 1 -or $runtimeDescriptors.Count -ne 1 -or $leases.Count -ne $expectedLeaseCount -or $extensionHostCount -ne $expectedExtensionHosts -or -not $descriptor.baseUrl -or $activationErrors.Count -gt 0 -or $rotatedLogs.Count -gt 4 -or $oversizedLogs.Count -gt 0 -or -not $usesInfoByDefault) {
    throw "Point split-window process verification failed: $($result | ConvertTo-Json -Depth 6 -Compress)"
  }
  Write-Output ''
  $result | ConvertTo-Json -Depth 6
} finally {
  if ($null -eq $originalAuxiliaryHub) { Remove-Item Env:POINT_AUXILIARY_HUB -ErrorAction SilentlyContinue } else { $env:POINT_AUXILIARY_HUB = $originalAuxiliaryHub }
  if ($process) {
    if ($ownedIds.Count -eq 0) { $ownedIds = @((Get-OwnedProcesses $process.Id) | ForEach-Object { [int]$_.ProcessId }) }
    if ($ownedIds.Count -gt 0) {
      Stop-Process -Id ($ownedIds | Sort-Object -Descending) -Force -ErrorAction SilentlyContinue
      Start-Sleep -Seconds 1
    }
  }
  $resolvedTest = [System.IO.Path]::GetFullPath($testRoot)
  $buildPrefix = $buildRoot + [System.IO.Path]::DirectorySeparatorChar
  if (-not $KeepArtifacts -and $resolvedTest.StartsWith($buildPrefix, [System.StringComparison]::OrdinalIgnoreCase) -and (Test-Path -LiteralPath $resolvedTest)) {
    Remove-Item -LiteralPath $resolvedTest -Recurse -Force -ErrorAction SilentlyContinue
  } elseif ($KeepArtifacts) {
    Write-Host "Kept split-window artifacts: $resolvedTest"
  }
}
