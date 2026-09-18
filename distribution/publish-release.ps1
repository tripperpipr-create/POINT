param(
  [string]$SetupSource
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$version = Get-Content -Raw (Join-Path $PSScriptRoot 'version.json') | ConvertFrom-Json
$extensionManifest = Get-Content -Raw (Join-Path $projectRoot 'vscode-extension\package.json') | ConvertFrom-Json
$portable = Join-Path $projectRoot '.cache\VSCode-win32-x64'
if (-not $SetupSource) {
  $SetupSource = Join-Path $projectRoot '.cache\code-oss\.build\win32-x64\user-setup\VSCodeSetup.exe'
}

if (-not (Test-Path -LiteralPath $SetupSource)) {
  throw "Installer is missing: $SetupSource"
}
if (-not (Test-Path -LiteralPath (Join-Path $portable 'Point.exe'))) {
  throw "Portable application is missing: $portable"
}

$releaseRoot = Join-Path $projectRoot 'build\code-oss'
$releaseSetup = Join-Path $releaseRoot "PointSetup-x64-$($version.ref).exe"
$releaseSBOM = Join-Path $releaseRoot 'point-installer-sbom.cdx.json'
New-Item -ItemType Directory -Force -Path $releaseRoot | Out-Null
Copy-Item -LiteralPath $SetupSource -Destination $releaseSetup -Force
$releaseHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $releaseSetup).Hash
& node (Join-Path $projectRoot 'scripts\generate-installer-sbom.mjs') $releaseSetup $portable $releaseSBOM
if ($LASTEXITCODE -ne 0) { throw 'Generating installer SBOM failed.' }
& node (Join-Path $projectRoot 'scripts\check-installer-sbom.mjs') $releaseSetup $portable $releaseSBOM
if ($LASTEXITCODE -ne 0) { throw 'Installer SBOM verification failed.' }
$releaseSBOMHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $releaseSBOM).Hash
$releaseSBOMDocument = Get-Content -Raw -LiteralPath $releaseSBOM | ConvertFrom-Json
$releaseManifest = [ordered]@{
  product = 'Point IDE'
  version = $version.ref
  platform = $version.platform
  upstreamCommit = $version.commit
  extensionVersion = $extensionManifest.version
  russianLanguagePackVersion = $version.russianLanguagePack.version
  installer = (Split-Path -Leaf $releaseSetup)
  installerSha256 = $releaseHash
  installerSbom = (Split-Path -Leaf $releaseSBOM)
  installerSbomSha256 = $releaseSBOMHash
  installerSbomComponents = @($releaseSBOMDocument.components).Count
  portable = $portable
}
[System.IO.File]::WriteAllText((Join-Path $releaseRoot 'release.json'), ($releaseManifest | ConvertTo-Json) + [Environment]::NewLine)
Write-Host "Portable: $portable"
Write-Host "Installer: $releaseSetup"
Write-Host "SHA256: $releaseHash"
Write-Host "Installer SBOM: $releaseSBOM"
Write-Host "Installer SBOM SHA256: $releaseSBOMHash"
