param(
    [Parameter(Mandatory = $true)]
    [string]$VsixPath
)

$resolvedVsix = (Resolve-Path -LiteralPath $VsixPath).Path
$temporaryRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$extractPath = Join-Path $temporaryRoot ("point-vsix-smoke-" + [guid]::NewGuid().ToString("N"))

try {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [System.IO.Compression.ZipFile]::ExtractToDirectory($resolvedVsix, $extractPath)
    $runtimePath = Join-Path $extractPath "extension\cursor-runtime.js"
    if (-not (Test-Path -LiteralPath $runtimePath)) {
        throw "Packaged Cursor runtime is missing: $runtimePath"
    }

    & node -e "const fs=require('fs'),path=require('path');const runtimePath=process.argv[1],root=path.dirname(runtimePath),entry=path.join(root,'dist','cursor-sdk','index.js');const runtime=require(runtimePath),before=Boolean(require.cache[entry]);runtime.status({includeModels:false}).then(state=>{const after=Boolean(require.cache[entry]),vendorExists=fs.existsSync(process.env.CURSOR_TREE_SITTER_VENDOR_DIR||''),sourceSdkShipped=fs.existsSync(path.join(root,'node_modules','@cursor','sdk'));console.log(JSON.stringify({before,after,available:state.available,authenticated:state.authenticated,vendorExists,sourceSdkShipped,error:state.error}));if(before||!after||!state.available||!vendorExists||sourceSdkShipped)process.exit(1)}).catch(error=>{console.error(error);process.exit(1)})" $runtimePath
    if ($LASTEXITCODE -ne 0) {
        throw "Packaged Cursor runtime smoke failed with exit code $LASTEXITCODE"
    }
}
finally {
    $resolvedExtract = [System.IO.Path]::GetFullPath($extractPath)
    $leaf = Split-Path -Leaf $resolvedExtract
    if ($resolvedExtract.StartsWith($temporaryRoot, [System.StringComparison]::OrdinalIgnoreCase) -and $leaf.StartsWith("point-vsix-smoke-")) {
        Remove-Item -LiteralPath $resolvedExtract -Recurse -Force -ErrorAction SilentlyContinue
    }
}
