param(
  [string]$VsixPath = (Join-Path $PSScriptRoot "..\build\vsix\point-ide-1.2.3.vsix")
)

$ErrorActionPreference = "Stop"
$code = Get-Command code -ErrorAction SilentlyContinue
if (-not $code) {
  throw "Команда 'code' не найдена. В VS Code выполните: Shell Command: Install 'code' command in PATH, либо установите VSIX через Extensions: Install from VSIX."
}
if (-not (Test-Path -LiteralPath $VsixPath)) {
  throw "VSIX не найден: $VsixPath"
}
& $code.Source --install-extension $VsixPath --force
