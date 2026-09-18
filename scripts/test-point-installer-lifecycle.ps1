param(
  [Parameter(Mandatory = $true)]
  [string]$CurrentInstaller,
  [string]$PreviousInstaller = '',
  [string]$Workspace = '',
  [string]$ExpectedVersion = ''
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
$version = Get-Content -Raw (Join-Path $projectRoot 'distribution\version.json') | ConvertFrom-Json
if (-not $ExpectedVersion) { $ExpectedVersion = [string]$version.ref }
if (-not $Workspace) { $Workspace = Join-Path $projectRoot 'examples\go-health' }
$CurrentInstaller = [System.IO.Path]::GetFullPath($CurrentInstaller)
$Workspace = [System.IO.Path]::GetFullPath($Workspace)
if ($PreviousInstaller) { $PreviousInstaller = [System.IO.Path]::GetFullPath($PreviousInstaller) }

foreach ($path in @($CurrentInstaller, $Workspace)) {
  if (-not (Test-Path -LiteralPath $path)) { throw "Required installer test input is missing: $path" }
}
if ($PreviousInstaller -and -not (Test-Path -LiteralPath $PreviousInstaller)) {
  throw "Previous installer is missing: $PreviousInstaller"
}

$testRoot = Join-Path $buildRoot ("installer-e2e-$([guid]::NewGuid().ToString('N'))")
$installRoot = Join-Path $testRoot 'Point'
$stateRoot = Join-Path $testRoot 'user-state'
$marker = Join-Path $stateRoot 'upgrade-marker.txt'
$phases = [System.Collections.Generic.List[string]]::new()
$rendererSmokes = [System.Collections.Generic.List[object]]::new()

function Assert-TestPath([string]$Path) {
  $resolved = [System.IO.Path]::GetFullPath($Path)
  $prefix = $buildRoot + [System.IO.Path]::DirectorySeparatorChar
  if (-not $resolved.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Installer test path escaped build root: $resolved"
  }
  return $resolved
}

function Invoke-Installer([string]$Installer, [string]$Phase) {
  $log = Join-Path $testRoot "$Phase-install.log"
  # VS Code's Inno template enables the `runcode` task for silent installs.
  # Disable it explicitly: an auto-launched default-profile instance would
  # steal the renderer smoke through Electron's single-instance lock.
  $arguments = @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/SP-', '/MERGETASKS=!runcode', "/DIR=$installRoot", "/LOG=$log")
  $process = Start-Process -FilePath $Installer -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru
  if ($process.ExitCode -ne 0) { throw "$Phase installer exited with code $($process.ExitCode). See $log" }
  [void]$phases.Add("$Phase-install")
}

function Get-InstalledReleaseFingerprint([string]$Phase) {
  $artifacts = [ordered]@{
    point = 'Point.exe'
    extension = 'resources\app\extensions\local-agent-workbench\extension.js'
    core = 'resources\app\extensions\local-agent-workbench\bin\point-core.exe'
  }
  $fingerprint = [ordered]@{}
  foreach ($entry in $artifacts.GetEnumerator()) {
    $path = Join-Path $installRoot $entry.Value
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
      throw "$Phase is missing release artifact $($entry.Value)"
    }
    $fingerprint[$entry.Key] = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash
  }
  return $fingerprint
}

function Test-SameReleaseFingerprint($Actual, $Expected, [string]$Phase) {
  foreach ($name in @('point', 'extension', 'core')) {
    if ([string]$Actual[$name] -ne [string]$Expected[$name]) {
      throw "$Phase artifact $name was not restored exactly"
    }
  }
}

function Test-InstalledPoint([string]$Phase, [string]$Expected = '') {
  $executable = Join-Path $installRoot 'Point.exe'
  if (-not (Test-Path -LiteralPath $executable)) { throw "$Phase did not install Point.exe" }
  $fileVersion = [string](Get-Item -LiteralPath $executable).VersionInfo.FileVersion
  if ($Expected -and $fileVersion -and -not $fileVersion.StartsWith($Expected, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "$Phase installed file version $fileVersion; expected prefix $Expected"
  }
  # PowerShell scripts report failures by throwing; `$LASTEXITCODE` belongs to
  # native programs and can contain an unrelated stale value here.
  $smokeJSON = (& (Join-Path $PSScriptRoot 'smoke-code-oss-renderer.ps1') -Executable $executable -Workspace $Workspace | Out-String).Trim()
  $smoke = $smokeJSON | ConvertFrom-Json
  [void]$rendererSmokes.Add([ordered]@{
    phase = $Phase
    processCount = [int]$smoke.processCount
    rendererCount = [int]$smoke.rendererCount
    localCoreProcesses = [int]$smoke.localCoreProcesses
    totalPrivateMB = [double]$smoke.totalPrivateMB
    idlePrivateMemoryBudgetMB = [double]$smoke.idlePrivateMemoryBudgetMB
  })
  [void]$phases.Add("$Phase-renderer")
  return $fileVersion
}

$testRoot = Assert-TestPath $testRoot
New-Item -ItemType Directory -Force -Path $testRoot, $stateRoot | Out-Null
$currentHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $CurrentInstaller).Hash
$previousVersion = ''
$currentVersion = ''
$previousFingerprint = $null
$currentFingerprint = $null
$rolledBackFingerprint = $null
try {
  if ($PreviousInstaller) {
    Invoke-Installer $PreviousInstaller 'previous-clean'
    $previousVersion = Test-InstalledPoint 'previous-clean'
    $previousFingerprint = Get-InstalledReleaseFingerprint 'previous-clean'
    [System.IO.File]::WriteAllText($marker, 'preserve-across-installer-lifecycle', [System.Text.UTF8Encoding]::new($false))
  }

  Invoke-Installer $CurrentInstaller $(if ($PreviousInstaller) { 'upgrade-current' } else { 'current-clean' })
  $currentVersion = Test-InstalledPoint 'current' $ExpectedVersion
  $currentFingerprint = Get-InstalledReleaseFingerprint 'current'
  if ($PreviousInstaller -and (Get-Content -Raw -LiteralPath $marker) -ne 'preserve-across-installer-lifecycle') {
    throw 'Upgrade removed or changed state outside the installation directory'
  }
  if ($PreviousInstaller) {
    $changedArtifacts = @('point', 'extension', 'core') | Where-Object {
      [string]$currentFingerprint[$_] -ne [string]$previousFingerprint[$_]
    }
    if ($changedArtifacts.Count -eq 0) {
      throw 'Upgrade did not change any tracked release artifact; installer update was not proven'
    }
  }

  if ($PreviousInstaller) {
    Invoke-Installer $PreviousInstaller 'rollback-previous'
    $rolledBackVersion = Test-InstalledPoint 'rollback-previous' $previousVersion
    $rolledBackFingerprint = Get-InstalledReleaseFingerprint 'rollback-previous'
    if ((Get-Content -Raw -LiteralPath $marker) -ne 'preserve-across-installer-lifecycle') {
      throw 'Rollback removed or changed state outside the installation directory'
    }
    if ($rolledBackVersion -ne $previousVersion) {
      throw "Rollback version $rolledBackVersion differs from original previous version $previousVersion"
    }
    Test-SameReleaseFingerprint $rolledBackFingerprint $previousFingerprint 'rollback-previous'
  }

  [ordered]@{
    installerSha256 = $currentHash
    currentVersion = $currentVersion
    previousVersion = $previousVersion
    previousFingerprint = $previousFingerprint
    currentFingerprint = $currentFingerprint
    rolledBackFingerprint = $rolledBackFingerprint
    cleanInstall = $true
    update = [bool]$PreviousInstaller
    rollback = [bool]$PreviousInstaller
    phases = @($phases)
    rendererSmokes = @($rendererSmokes)
  } | ConvertTo-Json -Depth 5
} finally {
  $uninstaller = Join-Path $installRoot 'unins000.exe'
  if (Test-Path -LiteralPath $uninstaller) {
    $uninstall = Start-Process -FilePath $uninstaller -ArgumentList @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART') -WindowStyle Hidden -Wait -PassThru
    if ($uninstall.ExitCode -ne 0) { Write-Warning "Test uninstaller exited with code $($uninstall.ExitCode)" }
  }
  $resolved = Assert-TestPath $testRoot
  if (Test-Path -LiteralPath $resolved) {
    Remove-Item -LiteralPath $resolved -Recurse -Force
  }
}
