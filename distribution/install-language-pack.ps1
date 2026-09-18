$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$version = Get-Content -Raw (Join-Path $PSScriptRoot 'version.json') | ConvertFrom-Json
$packVersion = $version.russianLanguagePack.version
$expectedHash = $version.russianLanguagePack.sha256.ToUpperInvariant()
$cacheRoot = Join-Path $projectRoot '.cache\language-packs'
$vsix = Join-Path $cacheRoot "MS-CEINTL.vscode-language-pack-ru-$packVersion.vsix"
$zip = Join-Path $cacheRoot "MS-CEINTL.vscode-language-pack-ru-$packVersion.zip"
$expanded = Join-Path $cacheRoot "vscode-language-pack-ru-$packVersion"
$manifest = Join-Path $expanded 'extension\package.json'
$url = "https://open-vsx.org/api/MS-CEINTL/vscode-language-pack-ru/$packVersion/file/MS-CEINTL.vscode-language-pack-ru-$packVersion.vsix"

New-Item -ItemType Directory -Force -Path $cacheRoot | Out-Null
if (-not (Test-Path -LiteralPath $vsix)) {
  Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $vsix
}

$actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $vsix).Hash
if ($actualHash -ne $expectedHash) {
  throw "Russian language pack checksum mismatch: expected $expectedHash, found $actualHash."
}

if (-not (Test-Path -LiteralPath $manifest)) {
  Copy-Item -LiteralPath $vsix -Destination $zip -Force
  Expand-Archive -LiteralPath $zip -DestinationPath $expanded -Force
}

if (-not (Test-Path -LiteralPath $manifest)) {
  throw "Russian language pack manifest is missing after extraction: $manifest"
}

Write-Host "Russian language pack: $manifest"
