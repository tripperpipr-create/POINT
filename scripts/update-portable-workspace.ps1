$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$portable = [IO.Path]::GetFullPath((Join-Path $repo '.cache/VSCode-win32-x64'))
$appRoot = Join-Path $portable 'resources/app'
$bundle = Join-Path $repo '.cache/code-oss/out-point-review'
$extension = Join-Path $repo '.cache/code-oss/extensions/local-agent-workbench'
$record = Join-Path $repo ('build/portable-update-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
if (-not (Test-Path -LiteralPath (Join-Path $portable 'Point.exe'))) { throw 'Portable IDE is missing' }
if (-not (Test-Path -LiteralPath (Join-Path $bundle 'vs/workbench/workbench.desktop.main.js'))) { throw 'Desktop bundle is missing' }
$running = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($portable + '\', [StringComparison]::OrdinalIgnoreCase) })
if ($running.Count) { throw "Portable IDE is running: $($running.ProcessId -join ', ')" }
New-Item -ItemType Directory -Force -Path $record | Out-Null
function Copy-Verified([string]$Source, [string]$Destination, [string]$Label) {
  $sourceRoot = [IO.Path]::GetFullPath($Source).TrimEnd('\')
  $targetRoot = [IO.Path]::GetFullPath($Destination).TrimEnd('\')
  if (-not $targetRoot.StartsWith($appRoot + '\', [StringComparison]::OrdinalIgnoreCase)) { throw "Unexpected target: $targetRoot" }
  $count = 0
  foreach ($file in Get-ChildItem -LiteralPath $sourceRoot -File -Recurse) {
    $relative = $file.FullName.Substring($sourceRoot.Length + 1)
    if ($relative -match '(^|[\\/])data[\\/]|\.db($|-)|api-token$') { continue }
    if ($file.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Unexpected link: $relative" }
    $target = [IO.Path]::GetFullPath((Join-Path $targetRoot $relative))
    if (-not $target.StartsWith($targetRoot + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Path escaped target' }
    $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
    if (Test-Path -LiteralPath $target) {
      if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash -eq $hash) { continue }
      $backup = Join-Path (Join-Path $record $Label) $relative
      New-Item -ItemType Directory -Force -Path (Split-Path $backup) | Out-Null
      Copy-Item -LiteralPath $target -Destination $backup
    }
    New-Item -ItemType Directory -Force -Path (Split-Path $target) | Out-Null
    Copy-Item -LiteralPath $file.FullName -Destination $target -Force
    if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash -ne $hash) { throw "Hash mismatch: $relative" }
    $count++
  }
  Write-Output "$Label updated: $count files"
}
# Generated webview assets may have been rebuilt while the desktop bundle ran.
Copy-Item -LiteralPath (Join-Path $repo 'vscode-extension/media/main.js') -Destination (Join-Path $extension 'media/main.js') -Force
Copy-Item -LiteralPath (Join-Path $repo 'vscode-extension/media/style.css') -Destination (Join-Path $extension 'media/style.css') -Force
Copy-Verified $bundle (Join-Path $appRoot 'out') 'desktop'
Copy-Verified $extension (Join-Path $appRoot 'extensions/local-agent-workbench') 'extension'
Copy-Item -LiteralPath (Join-Path $appRoot 'product.json') -Destination (Join-Path $record 'product.json')
& node (Join-Path $repo 'scripts/patch-point-chat-hub.js')
if ($LASTEXITCODE -ne 0) { throw 'Chat routing patch failed' }
& node (Join-Path $repo 'distribution/refresh-product-checksums.mjs') $appRoot
if ($LASTEXITCODE -ne 0) { throw 'Product checksums failed' }
Write-Output "Portable updated: $portable; backup: $record"
