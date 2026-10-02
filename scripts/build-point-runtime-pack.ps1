[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('moby', 'podman')][string]$Engine,
    [Parameter(Mandatory = $true)][string]$BaseImage,
    [Parameter(Mandatory = $true)][string]$PackageVersion,
    [Parameter(Mandatory = $true)][string]$SandboxImage,
    [Parameter(Mandatory = $true)][string]$OutputDirectory
)
$ErrorActionPreference = 'Stop'
if ($BaseImage -notmatch '^[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}$') { throw 'BaseImage must be a digest-pinned Debian/Ubuntu image' }
if ($PackageVersion -notmatch '^[a-zA-Z0-9.+:~_-]+$') { throw 'PackageVersion must be an exact apt package version' }
if ($SandboxImage -notmatch '^[a-zA-Z0-9][a-zA-Z0-9./:@_-]+$') { throw 'Invalid SandboxImage reference' }
$outputRoot = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $outputRoot) { throw 'Use a new output directory for an isolated runtime pack' }
New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null
if (Test-Path -LiteralPath (Join-Path $outputRoot 'runtime.json')) { throw 'Use a new output directory; existing pinned packs are immutable' }
$package = if ($Engine -eq 'moby') { 'docker.io' } else { 'podman' }
$program = if ($Engine -eq 'moby') { 'docker' } else { 'podman' }
$service = if ($Engine -eq 'moby') { 'RUN systemctl enable docker' } else { '' }
$dockerfile = @"
FROM $BaseImage
RUN apt-get update && apt-get install -y --no-install-recommends systemd systemd-sysv ca-certificates iptables $package=$PackageVersion && rm -rf /var/lib/apt/lists/*
RUN printf '[boot]\nsystemd=true\n[automount]\nenabled=false\n[interop]\nenabled=false\nappendWindowsPath=false\n' > /etc/wsl.conf
$service
"@
$dockerfilePath = Join-Path $outputRoot 'Dockerfile'
[IO.File]::WriteAllText($dockerfilePath, $dockerfile, [Text.UTF8Encoding]::new($false))
$tag = 'point-runtime-pack:' + [guid]::NewGuid().ToString('N')
& docker.exe build --pull=false --tag $tag --file $dockerfilePath $outputRoot
if ($LASTEXITCODE -ne 0) { throw 'Pinned runtime build failed' }
$versionOutput = & docker.exe run --rm --network none $tag $program --version
if ($LASTEXITCODE -ne 0 -or "$versionOutput" -notmatch '(\d+\.\d+\.\d+)') { throw 'Runtime engine version probe failed' }
$engineVersion = $Matches[1]
$sbom = & docker.exe run --rm --network none $tag dpkg-query -W
if ($LASTEXITCODE -ne 0) { throw 'Runtime package inventory failed' }
[IO.File]::WriteAllLines((Join-Path $outputRoot 'packages.txt'), $sbom, [Text.UTF8Encoding]::new($false))
$container = 'point-runtime-export-' + [guid]::NewGuid().ToString('N')
& docker.exe create --name $container $tag /bin/true | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Runtime export container creation failed' }
try {
    & docker.exe export --output (Join-Path $outputRoot 'rootfs.tar') $container
    if ($LASTEXITCODE -ne 0) { throw 'Runtime rootfs export failed' }
} finally {
    & docker.exe rm $container | Out-Null
}
$imageDigest = & docker.exe image inspect $SandboxImage --format '{{.Id}}'
if ($LASTEXITCODE -ne 0 -or "$imageDigest" -notmatch '^sha256:[a-f0-9]{64}$') { throw 'Sandbox image digest unavailable' }
& docker.exe save --output (Join-Path $outputRoot 'sandbox-image.tar') $SandboxImage
if ($LASTEXITCODE -ne 0) { throw 'Sandbox image export failed' }
$manifest = [ordered]@{
    schema = 1; engine = $Engine; engineVersion = $engineVersion; license = 'Apache-2.0'
    rootfs = @{ file = 'rootfs.tar'; sha256 = 'sha256:' + (Get-FileHash -LiteralPath (Join-Path $outputRoot 'rootfs.tar') -Algorithm SHA256).Hash.ToLowerInvariant() }
    image = @{ file = 'sandbox-image.tar'; sha256 = 'sha256:' + (Get-FileHash -LiteralPath (Join-Path $outputRoot 'sandbox-image.tar') -Algorithm SHA256).Hash.ToLowerInvariant() }
    imageReference = $SandboxImage; imageDigest = "$imageDigest"
    provenance = @{ baseImage = $BaseImage; package = $package; packageVersion = $PackageVersion; engineRootfsImage = $tag }
}
[IO.File]::WriteAllText((Join-Path $outputRoot 'runtime.json'), ($manifest | ConvertTo-Json -Depth 6), [Text.UTF8Encoding]::new($false))
Write-Output "Runtime pack prepared: $outputRoot. Security probes and live acceptance remain mandatory."
