param()

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$version = Get-Content -Raw (Join-Path $PSScriptRoot 'version.json') | ConvertFrom-Json
$archive = Join-Path $projectRoot ".cache\node-v$($version.node)-win-x64.zip"
$destination = Join-Path $projectRoot ".cache\node-v$($version.node)-win-x64"
$download = "https://nodejs.org/dist/v$($version.node)/node-v$($version.node)-win-x64.zip"
$expectedSha = [string]$version.nodeSha256

if (-not $expectedSha) {
  throw 'version.json is missing nodeSha256 for the pinned Node runtime.'
}

if (Test-Path -LiteralPath (Join-Path $destination 'node.exe')) {
  Write-Host "Node runtime is already installed: $destination"
  exit 0
}
New-Item -ItemType Directory -Path (Split-Path $archive) -Force | Out-Null
Invoke-WebRequest -Uri $download -OutFile $archive
$actualSha = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualSha -ne $expectedSha.ToLowerInvariant()) {
  Remove-Item -LiteralPath $archive -Force
  throw "Node archive SHA-256 mismatch. expected=$expectedSha actual=$actualSha"
}
Expand-Archive -LiteralPath $archive -DestinationPath (Split-Path $destination) -Force
if (-not (Test-Path -LiteralPath (Join-Path $destination 'node.exe'))) {
  throw 'The Node archive was not extracted correctly.'
}
Write-Host "Node runtime installed: $destination"
