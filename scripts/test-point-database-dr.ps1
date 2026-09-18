[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string] $PointDB,

  [Parameter(Mandatory = $true)]
  [string] $DatabaseDirectory,

  [Parameter(Mandatory = $true)]
  [string] $EvidenceDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$tool = (Resolve-Path -LiteralPath $PointDB).Path
$databaseRoot = (Resolve-Path -LiteralPath $DatabaseDirectory).Path
$databases = @(Get-ChildItem -LiteralPath $databaseRoot -Filter '*.db' -File | Sort-Object Name)
if ($databases.Count -lt 2) {
  throw 'Database DR drill requires at least two migrated .db files'
}

$evidenceRoot = [System.IO.Path]::GetFullPath($EvidenceDirectory)
if (Test-Path -LiteralPath $evidenceRoot) {
  if (-not (Test-Path -LiteralPath $evidenceRoot -PathType Container)) {
    throw "DR evidence path is not a directory: $evidenceRoot"
  }
  if (Get-ChildItem -LiteralPath $evidenceRoot | Select-Object -First 1) {
    throw "DR evidence directory must be empty: $evidenceRoot"
  }
} else {
  New-Item -ItemType Directory -Path $evidenceRoot | Out-Null
}

function Invoke-PointDBJSON {
  param([Parameter(Mandatory = $true)][string[]] $Arguments)

  $lines = @(& $tool @Arguments)
  if ($LASTEXITCODE -ne 0) {
    throw "point-db failed ($LASTEXITCODE): $($Arguments -join ' ')"
  }
  try {
    return (($lines -join [Environment]::NewLine) | ConvertFrom-Json)
  } catch {
    throw "point-db returned invalid JSON for '$($Arguments -join ' ')': $($_.Exception.Message)"
  }
}

function Assert-HealthyDatabaseReport {
  param(
    [Parameter(Mandatory = $true)] $Report,
    [Parameter(Mandatory = $true)][string] $Label
  )

  if ($Report.integrity -ne 'ok' -or [int]$Report.foreignKeyViolations -ne 0 -or -not $Report.sha256) {
    throw "$Label is not a healthy verified SQLite database"
  }
}

$sourceFile = $databases[0]
$sourceReport = Invoke-PointDBJSON -Arguments @('verify', '--db', $sourceFile.FullName)
Assert-HealthyDatabaseReport $sourceReport 'DR source'

$targetFile = $null
$targetReport = $null
foreach ($candidate in $databases | Select-Object -Skip 1) {
  $candidateReport = Invoke-PointDBJSON -Arguments @('verify', '--db', $candidate.FullName)
  Assert-HealthyDatabaseReport $candidateReport "DR target candidate $($candidate.Name)"
  if ($candidateReport.sha256 -ne $sourceReport.sha256) {
    $targetFile = $candidate
    $targetReport = $candidateReport
    break
  }
}
if (-not $targetFile) {
  throw 'Database DR drill requires two content-distinct migrated databases'
}

$backupPath = Join-Path $evidenceRoot 'online-backup.db'
$restoreTarget = Join-Path $evidenceRoot 'restore-target.db'
Copy-Item -LiteralPath $targetFile.FullName -Destination $restoreTarget

$copiedTarget = Invoke-PointDBJSON -Arguments @('verify', '--db', $restoreTarget)
Assert-HealthyDatabaseReport $copiedTarget 'Copied restore target'
if ($copiedTarget.sha256 -ne $targetReport.sha256) {
  throw 'Preparing the restore target changed its SHA-256'
}

$backupReport = Invoke-PointDBJSON -Arguments @('backup', '--db', $sourceFile.FullName, '--out', $backupPath)
Assert-HealthyDatabaseReport $backupReport 'Online backup'

$restoreReport = Invoke-PointDBJSON -Arguments @('restore', '--backup', $backupPath, '--db', $restoreTarget, '--confirm-offline')
Assert-HealthyDatabaseReport $restoreReport.backup 'Verified restore source'
Assert-HealthyDatabaseReport $restoreReport.restored 'Restored target'
if ($restoreReport.backup.sha256 -ne $backupReport.sha256 -or $restoreReport.restored.sha256 -ne $backupReport.sha256) {
  throw 'Restored database SHA-256 does not exactly match the verified backup'
}
if (-not $restoreReport.previousPath -or -not (Test-Path -LiteralPath $restoreReport.previousPath -PathType Leaf)) {
  throw 'Offline restore did not preserve a pre-restore recovery point'
}

$previousReport = Invoke-PointDBJSON -Arguments @('verify', '--db', $restoreReport.previousPath)
Assert-HealthyDatabaseReport $previousReport 'Pre-restore recovery point'
if ($previousReport.sha256 -ne $targetReport.sha256) {
  throw 'Pre-restore recovery point does not exactly match the displaced target'
}

$sourceAfter = Invoke-PointDBJSON -Arguments @('verify', '--db', $sourceFile.FullName)
$targetAfter = Invoke-PointDBJSON -Arguments @('verify', '--db', $targetFile.FullName)
Assert-HealthyDatabaseReport $sourceAfter 'DR source after drill'
Assert-HealthyDatabaseReport $targetAfter 'DR target fixture after drill'
if ($sourceAfter.sha256 -ne $sourceReport.sha256) {
  throw 'Database DR drill changed its source database'
}
if ($targetAfter.sha256 -ne $targetReport.sha256) {
  throw 'Database DR drill changed its target fixture'
}

$evidence = [ordered]@{
  schemaVersion = 1
  source = $sourceReport
  displacedTarget = $targetReport
  backup = $backupReport
  restored = $restoreReport.restored
  recoveryPoint = $previousReport
  exactBackupRestore = $true
  exactRecoveryPoint = $true
  sourceUnchanged = $true
  targetFixtureUnchanged = $true
}
$reportPath = Join-Path $evidenceRoot 'dr-report.json'
$utf8 = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText($reportPath, (($evidence | ConvertTo-Json -Depth 8) + [Environment]::NewLine), $utf8)
$evidence | ConvertTo-Json -Depth 8
