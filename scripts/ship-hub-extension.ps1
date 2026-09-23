$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$portable = [IO.Path]::GetFullPath((Join-Path $repo '.cache/VSCode-win32-x64'))
$installed = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'Programs/Point'))
$recordRoot = Join-Path $repo ('.tmp/hub-ship-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
New-Item -ItemType Directory -Path $recordRoot | Out-Null

$running = @(Get-CimInstance Win32_Process -Filter "Name = 'Point.exe' OR Name = 'point-core.exe'" -ErrorAction SilentlyContinue)
if ($running.Count) {
  throw "Close Point before updating (PIDs: $($running.ProcessId -join ', '))"
}
foreach ($root in @($portable, $installed)) {
  if (-not (Test-Path -LiteralPath (Join-Path $root 'Point.exe'))) {
    throw "Point root missing: $root"
  }
}

Add-Type -AssemblyName System.IO.Compression.FileSystem
$vsix = Join-Path $repo 'build/vsix/point-ide-1.2.3.vsix'
$unpacked = Join-Path $recordRoot 'vsix'
[IO.Compression.ZipFile]::ExtractToDirectory($vsix, $unpacked)
$extension = Join-Path $unpacked 'extension'
$portableExtension = Join-Path $portable 'resources/app/extensions/local-agent-workbench'

& node (Join-Path $repo 'scripts/stage-point-extension-manifest.mjs') (Join-Path $extension 'package.json') (Join-Path $portableExtension 'package.json')
if ($LASTEXITCODE -ne 0) { throw 'native manifest staging failed' }

function Sync-VerifiedFiles([string]$Source, [string]$Target, [string]$Label) {
  $src = [IO.Path]::GetFullPath($Source).TrimEnd('\')
  $dst = [IO.Path]::GetFullPath($Target).TrimEnd('\')
  if (-not ($dst.StartsWith($portable, [StringComparison]::OrdinalIgnoreCase) -or $dst -eq $installed -or $dst.StartsWith($installed + '\', [StringComparison]::OrdinalIgnoreCase))) {
    throw "Unexpected destination: $dst"
  }
  $changes = @()
  foreach ($file in Get-ChildItem -LiteralPath $src -Recurse -File) {
    $rel = $file.FullName.Substring($src.Length + 1)
    if ($rel -match '(^|[\\/])data[\\/]|\.db($|-)|api-token$') { continue }
    if ($file.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Unexpected link: $rel" }
    $to = [IO.Path]::GetFullPath((Join-Path $dst $rel))
    if (-not $to.StartsWith($dst + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Path escaped destination' }
    $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
    $exists = Test-Path -LiteralPath $to -PathType Leaf
    if ($exists -and (Get-FileHash -LiteralPath $to -Algorithm SHA256).Hash -eq $hash) { continue }
    $changes += [pscustomobject]@{ source = $file.FullName; destination = $to; relative = $rel; sha256 = $hash; existed = $exists }
  }
  $backup = Join-Path $recordRoot $Label
  foreach ($change in $changes) {
    if ($change.existed) {
      $bak = Join-Path $backup $change.relative
      New-Item -ItemType Directory -Force -Path (Split-Path -Parent $bak) | Out-Null
      Copy-Item -LiteralPath $change.destination -Destination $bak
    }
  }
  $changes | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $recordRoot ($Label + '.json')) -Encoding utf8
  foreach ($change in $changes) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $change.destination) | Out-Null
    Copy-Item -LiteralPath $change.source -Destination $change.destination -Force
    if ((Get-FileHash -LiteralPath $change.destination -Algorithm SHA256).Hash -ne $change.sha256) {
      throw "Copy verification failed: $($change.relative)"
    }
  }
  [pscustomobject]@{ target = $dst; updatedFiles = $changes.Count; backup = $backup }
}

$portableResult = Sync-VerifiedFiles $extension $portableExtension 'portable-backup'
$installedResult = Sync-VerifiedFiles $portableExtension (Join-Path $installed 'resources/app/extensions/local-agent-workbench') 'installed-backup'

$meta = @{
  backupRoot = $recordRoot
  updatedAt = (Get-Date).ToString('o')
  portable = $portable
  installed = $installed
  portableUpdated = $portableResult.updatedFiles
  installedUpdated = $installedResult.updatedFiles
  ledgers = @(
    (Join-Path $repo '.tmp/hub-acceptance-qwen3.5_9b-pass1.jsonl'),
    (Join-Path $repo '.tmp/hub-acceptance-qwen3.5_9b-pass2.jsonl')
  )
}
$meta | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $repo 'build/local-update-hub-ship.json') -Encoding utf8
$meta | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $recordRoot 'ship.json') -Encoding utf8
Write-Host ("Ship OK portable={0} installed={1} backup={2}" -f $portableResult.updatedFiles, $installedResult.updatedFiles, $recordRoot)
