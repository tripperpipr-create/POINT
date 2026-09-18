param()

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$version = Get-Content -Raw (Join-Path $PSScriptRoot 'version.json') | ConvertFrom-Json
$destination = Join-Path $projectRoot '.cache\code-oss'
if (Test-Path -LiteralPath (Join-Path $destination 'product.json')) {
  Write-Host "Code-OSS is already synchronized: $destination"
  exit 0
}
New-Item -ItemType Directory -Path (Split-Path $destination) -Force | Out-Null
git clone --depth 1 --branch $version.ref --single-branch --filter=blob:none $version.repository $destination
if ($LASTEXITCODE -ne 0 -and -not (Test-Path -LiteralPath (Join-Path $destination 'product.json'))) {
  throw 'Unable to download Code-OSS.'
}
$actualCommit = git -C $destination rev-parse HEAD
if ($LASTEXITCODE -ne 0 -or $actualCommit.Trim() -ne $version.commit) {
  throw "Code-OSS commit mismatch: expected $($version.commit), found $actualCommit."
}
