[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ManifestPath,
    [switch]$EnableWSL
)
$ErrorActionPreference = 'Stop'
$resolvedManifest = (Resolve-Path -LiteralPath $ManifestPath).Path
$runtimeBinary = Join-Path $PSScriptRoot '../vscode-extension/bin/point-runtime.exe'
if (-not (Test-Path -LiteralPath $runtimeBinary)) { throw 'Build the core bundle first: node scripts/build-core.mjs' }
if ($EnableWSL) {
    # Explicit installation action; never stop or reconfigure other distributions.
    & wsl.exe --install --no-distribution
    if ($LASTEXITCODE -ne 0) { throw 'WSL setup needs administrator rights or a restart. Retry this command after completing setup.' }
}
& $runtimeBinary install -manifest $resolvedManifest
if ($LASTEXITCODE -ne 0) { throw 'Runtime provisioning incomplete. Keep the pack and rerun after resolving the reported cause.' }
& $runtimeBinary status -manifest $resolvedManifest
if ($LASTEXITCODE -ne 0) { throw 'Runtime isolation is not verified; embedded execution remains unavailable.' }
Write-Output 'Embedded runtime verified. Opt in with POINT_SANDBOX_BACKEND=embedded, POINT_SANDBOX_WORKSPACE=volume and POINT_EMBEDDED_RUNTIME set to this manifest. Restart Point; active executions keep their original runtime.'
