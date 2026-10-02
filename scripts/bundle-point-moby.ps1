[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$PointRoot,
    [Parameter(Mandatory = $true)][string]$PackPath
)
# Moby ships inside Point: the pinned pack lies beside point-runtime.exe in the
# extension's bin/moby, so the extension finds it without any setting and the
# pack goes away with the installation. On the same volume files are hard
# links (no extra 2 GiB); otherwise they are copied. Every asset is verified.
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath $PointRoot).Path.TrimEnd('\')
$app = Join-Path $root 'resources/app'
$product = Get-Content -LiteralPath (Join-Path $app 'product.json') -Raw | ConvertFrom-Json
if ($product.applicationName -ne 'point') { throw 'Only a Point installation can carry the Moby pack' }
$bin = Join-Path $app 'extensions/local-agent-workbench/bin'
if (-not (Test-Path -LiteralPath (Join-Path $bin 'point-runtime.exe'))) { throw 'point-runtime.exe missing; deploy the extension first' }
$running = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($root + '\', [StringComparison]::OrdinalIgnoreCase) })
if ($running.Count) { throw 'Close this Point installation first' }

$source = (Resolve-Path -LiteralPath $PackPath).Path
if ((Get-Item -LiteralPath $source).PSIsContainer) { $source = Join-Path $source 'runtime.json' }
$packDir = Split-Path -Parent $source
$manifest = Get-Content -LiteralPath $source -Raw | ConvertFrom-Json
if ($manifest.engine -ne 'moby') { throw 'Point ships Moby only; this pack is ' + $manifest.engine }
$assets = @(@{ file = 'runtime.json'; sha256 = $null }, @{ file = $manifest.rootfs.file; sha256 = $manifest.rootfs.sha256 }, @{ file = $manifest.image.file; sha256 = $manifest.image.sha256 })

$target = Join-Path $bin 'moby'
New-Item -ItemType Directory -Path $target -Force | Out-Null
$sameVolume = [IO.Path]::GetPathRoot($packDir) -eq [IO.Path]::GetPathRoot($target)
$placed = @()
foreach ($asset in $assets) {
    if ($asset.file -notmatch '^[A-Za-z0-9._-]+$') { throw "Unsafe pack file name: $($asset.file)" }
    $from = Join-Path $packDir $asset.file
    $to = Join-Path $target $asset.file
    $hash = 'sha256:' + (Get-FileHash -LiteralPath $from -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($asset.sha256 -and $hash -ne $asset.sha256) { throw "Pack asset does not match its manifest: $($asset.file)" }
    $current = if (Test-Path -LiteralPath $to) { 'sha256:' + (Get-FileHash -LiteralPath $to -Algorithm SHA256).Hash.ToLowerInvariant() } else { '' }
    if ($current -ne $hash) {
        if ($current) { Remove-Item -LiteralPath $to -Force }
        if ($sameVolume) { New-Item -ItemType HardLink -Path $to -Target $from | Out-Null } else { Copy-Item -LiteralPath $from -Destination $to }
    }
    if (('sha256:' + (Get-FileHash -LiteralPath $to -Algorithm SHA256).Hash.ToLowerInvariant()) -ne $hash) { throw "Verification failed: $($asset.file)" }
    $placed += [pscustomobject]@{ file = $asset.file; sha256 = $hash; changed = ($current -ne $hash) }
}
# Leftovers of an earlier pack would be shipped and never verified.
Get-ChildItem -LiteralPath $target -File | Where-Object { $_.Name -notin $placed.file } | Remove-Item -Force
$digest = 'sha256:' + (Get-FileHash -LiteralPath (Join-Path $target 'runtime.json') -Algorithm SHA256).Hash.ToLowerInvariant()
[pscustomobject]@{ target = $target; engine = 'moby'; engineVersion = $manifest.engineVersion; manifestDigest = $digest; hardLinks = $sameVolume; files = $placed } | ConvertTo-Json -Depth 4
