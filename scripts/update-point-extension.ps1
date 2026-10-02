[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$PointRoot)
$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$targetRoot = (Resolve-Path -LiteralPath $PointRoot).Path.TrimEnd('\')
$app = Join-Path $targetRoot 'resources/app'
$target = Join-Path $app 'extensions/local-agent-workbench'
$source = Join-Path $repo 'vscode-extension'
if (-not (Test-Path -LiteralPath (Join-Path $targetRoot 'Point.exe'))) { throw 'Point executable missing' }
$product = Get-Content -LiteralPath (Join-Path $app 'product.json') -Raw | ConvertFrom-Json
if ($product.applicationName -ne 'point') { throw 'Only a Point installation can be updated' }
$running = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($targetRoot + '\', [StringComparison]::OrdinalIgnoreCase) })
if ($running.Count) { throw 'Close this Point installation first; active processes are preserved' }
foreach ($name in @('point-core.exe', 'point-db.exe', 'point-runtime.exe', 'point-sandboxd-linux-amd64')) {
    if (-not (Test-Path -LiteralPath (Join-Path $source "bin/$name"))) { throw "Build first; missing $name" }
}
$record = Join-Path $repo ('build/point-moby-update-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $record | Out-Null
$stage = Join-Path $record 'package.json'
Copy-Item -LiteralPath (Join-Path $source 'package.json') -Destination $stage
& node (Join-Path $repo 'scripts/stage-point-extension-manifest.mjs') $stage (Join-Path $target 'package.json')
if ($LASTEXITCODE -ne 0) { throw 'Native manifest staging failed' }
$files = @(Get-ChildItem -LiteralPath $source -File | Where-Object { $_.Extension -eq '.js' -or $_.Name -in @('package-lock.json', 'README.md', 'CHANGELOG.md', 'LICENSE', '.vscodeignore') })
foreach ($directory in @('bin', 'media', 'dist', 'themes', 'walkthrough')) {
    $files += @(Get-ChildItem -LiteralPath (Join-Path $source $directory) -Recurse -File)
}
$entries = @()
foreach ($file in $files) {
    $relative = $file.FullName.Substring($source.Length + 1)
    if ($relative -match '(^|[\\/])data[\\/]|\.db($|-)|api-token$') { continue }
    if ($file.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Source link rejected: $relative" }
    $entries += [pscustomobject]@{ source = $file.FullName; relative = $relative }
}
$entries += [pscustomobject]@{ source = $stage; relative = 'package.json' }
$verified = @()
foreach ($entry in $entries) {
    $destination = [IO.Path]::GetFullPath((Join-Path $target $entry.relative))
    if (-not $destination.StartsWith($target + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Destination escaped Point extension' }
    $hash = (Get-FileHash -LiteralPath $entry.source -Algorithm SHA256).Hash
    $changed = -not (Test-Path -LiteralPath $destination) -or (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -ne $hash
    if ($changed) {
        if (Test-Path -LiteralPath $destination) {
            $backup = Join-Path $record ('backup/' + $entry.relative)
            New-Item -ItemType Directory -Path (Split-Path -Parent $backup) -Force | Out-Null
            Copy-Item -LiteralPath $destination -Destination $backup
        }
        New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
        Copy-Item -LiteralPath $entry.source -Destination $destination -Force
    }
    if ((Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -ne $hash) { throw "Verification failed: $($entry.relative)" }
    $verified += [pscustomobject]@{ relative = $entry.relative; sha256 = $hash; changed = $changed }
}
$verified | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $record 'files.json') -Encoding utf8
$result = [pscustomobject]@{ target = $targetRoot; updatedFiles = @($verified | Where-Object changed).Count; verifiedFiles = $verified.Count; backup = $record; restarted = $false }
$result | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $record 'result.json') -Encoding utf8
$result | ConvertTo-Json
