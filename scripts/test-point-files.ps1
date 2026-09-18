param(
  [string]$Executable = '',
  [int]$WaitSeconds = 7,
  [string]$Screenshot = '',
  [ValidateSet('files', 'versions', 'projects', 'multi-project')]
  [string]$Section = 'files',
  [switch]$GitFixture
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
$testRoot = Join-Path $buildRoot ("files-$([guid]::NewGuid().ToString('N'))")
$userData = Join-Path $testRoot 'user-data'
$extensions = Join-Path $testRoot 'extensions'
New-Item -ItemType Directory -Force -Path $userData, $extensions | Out-Null
$workspace = Join-Path $projectRoot 'examples\go-health'
if ($Section -eq 'multi-project') {
  $workspaceFile = Join-Path $testRoot 'point-multi-root.code-workspace'
  $workspaceDefinition = [ordered]@{
    folders = @(
      [ordered]@{ name = 'go-health'; path = (Join-Path $projectRoot 'examples\go-health') },
      [ordered]@{ name = 'frontend'; path = (Join-Path $projectRoot 'frontend') }
    )
    settings = [ordered]@{}
  }
  [System.IO.File]::WriteAllText(
    $workspaceFile,
    (($workspaceDefinition | ConvertTo-Json -Depth 8) + [Environment]::NewLine),
    [System.Text.UTF8Encoding]::new($false)
  )
  $workspace = $workspaceFile
}
if ($GitFixture) {
  $workspace = Join-Path $testRoot 'go-health-git'
  New-Item -ItemType Directory -Force -Path $workspace | Out-Null
  Copy-Item -Path (Join-Path $projectRoot 'examples\go-health\*') -Destination $workspace -Recurse -Force
  & git -C $workspace init --quiet
  if ($LASTEXITCODE -ne 0) { throw 'Could not initialize the temporary Git fixture.' }
  $nestedVersionFolder = Join-Path $workspace 'internal\service'
  New-Item -ItemType Directory -Force -Path $nestedVersionFolder | Out-Null
  [System.IO.File]::WriteAllText(
    (Join-Path $nestedVersionFolder 'worker.go'),
    "package service`n`nfunc Work() {}`n",
    [System.Text.UTF8Encoding]::new($false)
  )
}
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
  if ($Section -eq 'projects') {
    $seedArguments = @(
      '--user-data-dir', $userData,
      '--extensions-dir', $extensions,
      '--disable-updates', '--skip-welcome', '--skip-release-notes',
      (Join-Path $projectRoot 'frontend')
    )
    $seedProcess = Start-Process -FilePath $Executable -ArgumentList $seedArguments -WindowStyle Hidden -PassThru
    Start-Sleep -Seconds 5
    $seedIds = @((Get-OwnedProcesses $seedProcess.Id) | ForEach-Object { [int]$_.ProcessId })
    if ($seedIds.Count -gt 0) { Stop-Process -Id ($seedIds | Sort-Object -Descending) -Force -ErrorAction SilentlyContinue }
    Start-Sleep -Seconds 1
  }
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
  if ($Section -eq 'versions' -or $Section -eq 'multi-project') {
    & node (Join-Path $PSScriptRoot 'verify-point-trust-dialog.mjs') $endpoint 'accept-if-present'
    if ($LASTEXITCODE -ne 0) { throw 'Point trust verification failed.' }
  }
  $screenshotPath = if ($Screenshot) { [System.IO.Path]::GetFullPath((Join-Path $projectRoot $Screenshot)) } else { '' }
  if ($Section -eq 'multi-project') {
    & node (Join-Path $PSScriptRoot 'verify-point-multi-project.mjs') $endpoint $screenshotPath
    if ($LASTEXITCODE -ne 0) { throw 'Point multi-project verification failed.' }
  } elseif ($Section -eq 'projects') {
    & node (Join-Path $PSScriptRoot 'verify-point-project-switcher.mjs') $endpoint $screenshotPath
    if ($LASTEXITCODE -ne 0) { throw 'Point project switcher verification failed.' }
  } else {
    & node (Join-Path $PSScriptRoot 'verify-point-sidebar.mjs') $endpoint $Section
    if ($LASTEXITCODE -ne 0) { throw "Point $Section sidebar verification failed." }
  }
  if ($Section -eq 'files') {
    & node (Join-Path $PSScriptRoot 'open-point-file.mjs') $endpoint 'main.go' $screenshotPath
    if ($LASTEXITCODE -ne 0) { throw 'Point file opening verification failed.' }
  } elseif ($Section -eq 'versions' -and $screenshotPath) {
    & node (Join-Path $PSScriptRoot 'capture-cdp-page.mjs') $endpoint $screenshotPath
    if ($LASTEXITCODE -ne 0) { throw 'Point versions screenshot failed.' }
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
    localCoreProcesses = $coreProcesses.Count
    authenticationPrompts = $authPrompts.Count
    screenshot = $screenshotPath
  }
  # The permanent right Assistant intentionally owns one extension host and one
  # shared point-core in every workspace. Keep this E2E aligned with the renderer
  # smoke and fail both missing-runtime regressions and duplicate process leaks.
  $expectedHostCount = 1
  if ($hostPids.Count -ne $expectedHostCount -or $coreProcesses.Count -ne 1 -or $authPrompts.Count -gt 0) {
    throw "Point files process verification failed: $($result | ConvertTo-Json -Compress)"
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
