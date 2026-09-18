param(
  [Parameter(Mandatory = $true)]
  [string]$Source
)

$ErrorActionPreference = 'Stop'
$sourcePath = [System.IO.Path]::GetFullPath($Source)
$resourceRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'resources'))
if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
  throw "Point icon source is missing: $sourcePath"
}

Add-Type -AssemblyName System.Drawing
New-Item -ItemType Directory -Path $resourceRoot -Force | Out-Null
$savedSource = Join-Path $resourceRoot 'point-source.png'
Copy-Item -LiteralPath $sourcePath -Destination $savedSource -Force

$sourceImage = [System.Drawing.Image]::FromFile($savedSource)
try {
  foreach ($size in @(16, 24, 32, 48, 64, 70, 128, 150, 256)) {
    $bitmap = New-Object System.Drawing.Bitmap($size, $size, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    try {
      $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
      try {
        $graphics.CompositingMode = [System.Drawing.Drawing2D.CompositingMode]::SourceCopy
        $graphics.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
        $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
        $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        $graphics.DrawImage($sourceImage, 0, 0, $size, $size)
      } finally {
        $graphics.Dispose()
      }
      $bitmap.Save((Join-Path $resourceRoot "point-$size.png"), [System.Drawing.Imaging.ImageFormat]::Png)
    } finally {
      $bitmap.Dispose()
    }
  }
} finally {
  $sourceImage.Dispose()
}

$extensionMedia = Join-Path $PSScriptRoot '..\vscode-extension\media'
$frontendPublic = Join-Path $PSScriptRoot '..\frontend\public'
New-Item -ItemType Directory -Path $extensionMedia, $frontendPublic -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $resourceRoot 'point-256.png') -Destination (Join-Path $extensionMedia 'point-icon.png') -Force
Copy-Item -LiteralPath (Join-Path $resourceRoot 'point-256.png') -Destination (Join-Path $frontendPublic 'point-icon.png') -Force

Write-Host "Prepared Point icon assets in $resourceRoot"
