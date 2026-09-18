param(
  [switch]$SkipInstall,
  [switch]$InstallerOnly,
  [switch]$SkipInstaller,
  [switch]$Minified,
  [string]$PythonPath
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$version = Get-Content -Raw (Join-Path $PSScriptRoot 'version.json') | ConvertFrom-Json
$sourceRoot = Join-Path $projectRoot '.cache\code-oss'
$nodeRoot = Join-Path $projectRoot ".cache\node-v$($version.node)-win-x64"
$node = Join-Path $nodeRoot 'node.exe'
$npm = Join-Path $nodeRoot 'npm.cmd'
$goCommand = Get-Command go.exe -ErrorAction SilentlyContinue
$python = $PythonPath
if (-not $python) {
  $pythonCommand = Get-Command python.exe -ErrorAction SilentlyContinue
  if ($pythonCommand) { $python = $pythonCommand.Source }
}
$windowsSdkBin = 'C:\Program Files (x86)\Windows Kits\10\bin\10.0.26100.0\x64'
$vcRedistRoot = 'C:\Program Files\Microsoft Visual Studio\2022\Community\VC\Redist\MSVC'

if (-not (Test-Path -LiteralPath $node) -or -not (Test-Path -LiteralPath $npm)) {
  throw "Node 24.15.0 is missing. Run distribution\install-build-runtime.ps1 first."
}
if (-not $python -or -not (Test-Path -LiteralPath $python)) {
  throw 'Python 3 is missing. Install it or pass -PythonPath C:\path\to\python.exe.'
}
if (-not $goCommand) {
  throw 'Go is missing. Install Go 1.25 or newer to build the Point local core.'
}
if (-not (Test-Path -LiteralPath (Join-Path $windowsSdkBin 'signtool.exe'))) {
  throw "Windows SDK signing tools are missing at $windowsSdkBin."
}
$vcruntime = Get-ChildItem -LiteralPath $vcRedistRoot -Filter 'vcruntime140.dll' -Recurse |
  Where-Object { $_.FullName -match '\\x64\\Microsoft\.VC143\.CRT\\vcruntime140\.dll$' -and $_.FullName -notmatch '\\onecore\\' } |
  Sort-Object FullName -Descending |
  Select-Object -First 1
if (-not $vcruntime) {
  throw "Visual C++ x64 runtime is missing below $vcRedistRoot."
}
if (-not (Test-Path -LiteralPath (Join-Path $sourceRoot 'product.json'))) {
  throw "Code-OSS is missing at $sourceRoot. Run distribution\sync-code-oss.ps1 first."
}

function Invoke-Node {
  param([Parameter(ValueFromRemainingArguments = $true)][string[]]$NodeArgs)
  $previous = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  $output = & $node @NodeArgs 2>&1
  $code = $LASTEXITCODE
  $ErrorActionPreference = $previous
  foreach ($line in @($output)) {
    Write-Host ([string]$line)
  }
  return $code
}

$env:GOCACHE = Join-Path $projectRoot '.cache\go-build'
Push-Location $projectRoot
try {
  & $goCommand.Source build -trimpath -ldflags '-s -w' -o (Join-Path $projectRoot 'vscode-extension\bin\point-core.exe') '.\cmd\server'
  if ($LASTEXITCODE -ne 0) { throw 'Building the Point local core failed.' }
  & $goCommand.Source build -trimpath -ldflags '-s -w' -o (Join-Path $projectRoot 'vscode-extension\bin\point-db.exe') '.\cmd\point-db'
  if ($LASTEXITCODE -ne 0) { throw 'Building the Point database recovery utility failed.' }
} finally {
  Pop-Location
}
if ((Invoke-Node (Join-Path $PSScriptRoot 'generate-icons.mjs')) -ne 0) { throw 'Icon generation failed.' }
if ((Invoke-Node (Join-Path $projectRoot 'vscode-extension\runtime\build.mjs')) -ne 0) { throw 'Extension runtime bundling failed.' }
& (Join-Path $PSScriptRoot 'install-language-pack.ps1')
if ($LASTEXITCODE -ne 0) { throw 'Russian language pack preparation failed.' }
if ((Invoke-Node (Join-Path $PSScriptRoot 'apply-overlay.mjs') $sourceRoot) -ne 0) { throw 'Applying the Point overlay failed.' }
if ((Invoke-Node (Join-Path $PSScriptRoot 'prepare-node-gyp.mjs') $nodeRoot) -ne 0) { throw 'Preparing the local node-gyp fallback failed.' }
Copy-Item -LiteralPath $vcruntime.FullName -Destination (Join-Path $sourceRoot 'build\win32\vcruntime140.dll') -Force

$previousPath = $env:PATH
$env:PATH = "$nodeRoot;$(Split-Path -Parent $python);$windowsSdkBin;$env:PATH"
$env:PYTHON = $python
$env:npm_config_python = $python
$env:npm_config_msvs_version = '2022'
$env:vs2022_install = 'C:\Program Files\Microsoft Visual Studio\2022\Community'
$env:LOCAL_AGENT_USE_STANDARD_CRT = '1'
$env:CL = '/Qspectre'

# npm writes warnings to stderr; PowerShell turns them into NativeCommandError and
# aborts under $ErrorActionPreference=Stop. Capture and re-print as plain text.
function Invoke-Npm {
  param([Parameter(ValueFromRemainingArguments = $true)][string[]]$NpmArgs)
  $quoted = foreach ($arg in $NpmArgs) {
    if ($null -eq $arg) { continue }
    $text = [string]$arg
    if ($text -match '[\s"]') { '"' + ($text -replace '"', '\"') + '"' } else { $text }
  }
  $previous = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  $output = & cmd.exe /c "`"$npm`" $($quoted -join ' ')" 2>&1
  $code = $LASTEXITCODE
  $ErrorActionPreference = $previous
  foreach ($line in @($output)) {
    Write-Host ([string]$line)
  }
  return $code
}

$extensionTarget = Join-Path $sourceRoot 'extensions\local-agent-workbench'
foreach ($requiredBinary in @('point-core.exe', 'point-db.exe')) {
  if (-not (Test-Path -LiteralPath (Join-Path $extensionTarget "bin\$requiredBinary"))) {
    throw "Built-in extension is missing required binary: $requiredBinary"
  }
}
if ((Invoke-Npm --prefix $extensionTarget install --omit=dev --no-audit --no-fund) -ne 0) {
  throw 'Built-in extension dependency installation failed.'
}
if (-not (Test-Path -LiteralPath (Join-Path $extensionTarget 'node_modules\@cursor\sdk-win32-x64\package.json'))) {
  throw 'Built-in extension is missing the Cursor Windows runtime after install.'
}
if (-not (Test-Path -LiteralPath (Join-Path $extensionTarget 'cursor-runtime.js'))) {
  throw 'Built-in extension is missing cursor-runtime.js.'
}
if (-not (Test-Path -LiteralPath (Join-Path $extensionTarget 'dist\cursor-sdk\index.js'))) {
  throw 'Built-in extension is missing the bundled Cursor SDK runtime.'
}

try {
  Push-Location $sourceRoot
  try {
    if (-not $SkipInstall -or -not (Test-Path -LiteralPath (Join-Path $sourceRoot 'node_modules'))) {
      if ((Invoke-Npm install --no-audit --no-fund) -ne 0) { throw 'Code-OSS dependency installation failed.' }
    }
    $requiredNativePackages = @('@vscode\sqlite3', 'kerberos', 'native-is-elevated', 'native-keymap', 'node-pty', 'windows-foreground-love')
    $missingNativePackages = @($requiredNativePackages | Where-Object {
      $packagePath = Join-Path (Join-Path $sourceRoot 'node_modules') $_
      -not (Get-ChildItem -LiteralPath $packagePath -Filter '*.node' -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1)
    })
    if ($missingNativePackages.Count -gt 0) {
      if ((Invoke-Npm rebuild '@vscode/sqlite3' kerberos native-is-elevated native-keymap node-pty ssh2 windows-foreground-love --no-audit --no-fund) -ne 0) {
        throw 'Code-OSS native dependency rebuild failed.'
      }
    }
    foreach ($package in $requiredNativePackages) {
      $packagePath = Join-Path (Join-Path $sourceRoot 'node_modules') $package
      if (-not (Get-ChildItem -LiteralPath $packagePath -Filter '*.node' -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1)) {
        throw "Native bindings are missing for $package."
      }
    }
    if (-not $InstallerOnly) {
      if ((Invoke-Npm --prefix build run typecheck) -ne 0) { throw 'Build script type checking failed.' }
      $task = if ($Minified) { 'vscode-win32-x64-min' } else { 'vscode-win32-x64' }
      if ((Invoke-Npm run gulp $task) -ne 0) { throw "$task failed." }
    } else {
      # InstallerOnly must package the already tested portable tree verbatim.
      # Re-running a *-ci task here would recreate it from an older out-vscode
      # cache and could silently roll back minified Point workbench changes.
      # Product identity (URLs, names) is overlay-only JSON: sync without a full gulp.
      $sourceProduct = Join-Path $sourceRoot 'product.json'
      $portableProduct = Join-Path $projectRoot '.cache\VSCode-win32-x64\resources\app\product.json'
      if (-not (Test-Path -LiteralPath $sourceProduct) -or -not (Test-Path -LiteralPath $portableProduct)) {
        throw 'Point product.json is missing from Code-OSS source or portable app.'
      }
      $sourceProductJson = Get-Content -LiteralPath $sourceProduct -Raw | ConvertFrom-Json
      $portableProductJson = Get-Content -LiteralPath $portableProduct -Raw | ConvertFrom-Json
      foreach ($propertyName in @(
        'nameShort', 'nameLong', 'applicationName', 'dataFolderName', 'sharedDataFolderName',
        'licenseName', 'licenseUrl', 'serverLicenseUrl', 'documentationUrl', 'serverDocumentationUrl',
        'privacyStatementUrl', 'reportIssueUrl', 'urlProtocol', 'defaultChatAgent', 'extensionsGallery',
        'builtInExtensionsEnabledWithAutoUpdates'
      )) {
        if ($sourceProductJson.PSObject.Properties.Name -contains $propertyName) {
          $portableProductJson | Add-Member -NotePropertyName $propertyName -NotePropertyValue $sourceProductJson.$propertyName -Force
        }
      }
      [System.IO.File]::WriteAllText($portableProduct, (($portableProductJson | ConvertTo-Json -Depth 100) + [Environment]::NewLine), [System.Text.UTF8Encoding]::new($false))
      $portableExtensions = Join-Path $projectRoot '.cache\VSCode-win32-x64\resources\app\extensions'
      foreach ($extensionName in @('local-agent-workbench', 'vscode-language-pack-ru')) {
        $packagedExtension = Join-Path $portableExtensions $extensionName
        $sourceExtension = Join-Path $sourceRoot "extensions\$extensionName"
        if (-not (Test-Path -LiteralPath $sourceExtension)) {
          throw "Built-in extension is missing: $sourceExtension"
        }
        if (Test-Path -LiteralPath $packagedExtension) {
          Remove-Item -LiteralPath $packagedExtension -Recurse -Force
        }
        Copy-Item -LiteralPath $sourceExtension -Destination $packagedExtension -Recurse -Force
      }
      # Git and GitHub are compiled upstream extensions. Preserve the generated
      # dist entry points and refresh only Point-owned activation/welcome metadata.
      foreach ($extensionName in @('git', 'github', 'git-base', 'emmet')) {
        $packagedExtension = Join-Path $portableExtensions $extensionName
        $sourceExtension = Join-Path $sourceRoot "extensions\$extensionName"
        $sourcePackagePath = Join-Path $sourceExtension 'package.json'
        $packagedPackagePath = Join-Path $packagedExtension 'package.json'
        $sourcePackage = Get-Content -LiteralPath $sourcePackagePath -Raw | ConvertFrom-Json
        $packagedPackage = Get-Content -LiteralPath $packagedPackagePath -Raw | ConvertFrom-Json
        $packagedPackage.activationEvents = [object[]]@($sourcePackage.activationEvents)
        if ($sourcePackage.contributes.PSObject.Properties.Name -contains 'viewsWelcome') {
          $packagedPackage.contributes.viewsWelcome = [object[]]@($sourcePackage.contributes.viewsWelcome)
        }
        [System.IO.File]::WriteAllText($packagedPackagePath, (($packagedPackage | ConvertTo-Json -Depth 100) + [Environment]::NewLine), [System.Text.UTF8Encoding]::new($false))
        $sourceNls = Join-Path $sourceExtension 'package.nls.json'
        if (Test-Path -LiteralPath $sourceNls) {
          Copy-Item -LiteralPath $sourceNls -Destination (Join-Path $packagedExtension 'package.nls.json') -Force
        }
      }
      $titlebarIcon = Join-Path $PSScriptRoot 'resources\point-titlebar.svg'
      $portableTitlebarIcon = Join-Path $projectRoot '.cache\VSCode-win32-x64\resources\app\out\media\code-icon.svg'
      if (-not (Test-Path -LiteralPath $titlebarIcon) -or -not (Test-Path -LiteralPath (Split-Path -Parent $portableTitlebarIcon))) {
        throw 'Point titlebar icon or portable media directory is missing.'
      }
      Copy-Item -LiteralPath $titlebarIcon -Destination $portableTitlebarIcon -Force
    }
    # InstallerOnly does not recompile workbench; keep Point Chat-to-Hub guards in the
    # already packaged renderer until the next full gulp compile from overlay TS.
    if ((Invoke-Node (Join-Path $projectRoot 'scripts\patch-point-chat-hub.js')) -ne 0) {
      throw 'Patching packaged Chat-to-Hub redirects failed.'
    }
    if (-not (Test-Path -LiteralPath (Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe'))) {
      throw 'Portable Point build is missing; run without -InstallerOnly first.'
    }
    # Overlay / InstallerOnly may patch out/ assets after gulp wrote product.checksums.
    # Refresh so integrityService does not claim the install is corrupt.
    $portableApp = Join-Path $projectRoot '.cache\VSCode-win32-x64\resources\app'
    if ((Invoke-Node (Join-Path $PSScriptRoot 'refresh-product-checksums.mjs') $portableApp) -ne 0) {
      throw 'Refreshing portable product checksums failed.'
    }
    & (Join-Path $projectRoot 'scripts\smoke-code-oss-renderer.ps1') -Executable (Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe')
    if ($LASTEXITCODE -ne 0) { throw 'Portable Code-OSS renderer smoke failed.' }
    if (-not $SkipInstaller) {
      if ((Invoke-Npm run gulp vscode-win32-x64-inno-updater) -ne 0) { throw 'Inno Setup updater preparation failed.' }
      if ((Invoke-Npm run gulp vscode-win32-x64-user-setup) -ne 0) { throw 'Installer build failed.' }
    }
  } finally {
    Pop-Location
  }
} finally {
  $env:PATH = $previousPath
}

if (-not $SkipInstaller) {
  & (Join-Path $PSScriptRoot 'publish-release.ps1')
  if ($LASTEXITCODE -ne 0) { throw 'Release publication failed.' }
}
