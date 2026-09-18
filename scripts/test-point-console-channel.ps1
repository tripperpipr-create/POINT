param(
  [string]$Executable = '',
  [string]$Screenshot = ''
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) { $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe' }
if (-not $Screenshot) { $Screenshot = Join-Path $buildRoot 'code-oss\point-console-channel.png' }
$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start(); $port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port; $listener.Stop()
$testRoot = Join-Path $buildRoot ("console-channel-$([guid]::NewGuid().ToString('N'))")
$userData = Join-Path $testRoot 'user-data'; $extensions = Join-Path $testRoot 'extensions'
New-Item -ItemType Directory -Force -Path $userData, $extensions, (Split-Path -Parent $Screenshot) | Out-Null
$process = $null
function Get-OwnedProcesses([int]$RootId) {
  $all = @(Get-CimInstance Win32_Process)
  $ids = [System.Collections.Generic.HashSet[int]]::new(); [void]$ids.Add($RootId)
  do { $before = $ids.Count; foreach ($item in $all) { if ($ids.Contains([int]$item.ParentProcessId)) { [void]$ids.Add([int]$item.ProcessId) } } } while ($ids.Count -gt $before)
  return @($all | Where-Object { $ids.Contains([int]$_.ProcessId) })
}
try {
  $process = Start-Process -FilePath $Executable -ArgumentList @('--user-data-dir',$userData,'--extensions-dir',$extensions,"--remote-debugging-port=$port",'--disable-updates','--skip-welcome','--skip-release-notes',(Join-Path $projectRoot 'examples\go-health')) -WindowStyle Hidden -PassThru
  $result = & node (Join-Path $PSScriptRoot 'verify-point-console-channel.mjs') "http://127.0.0.1:$port" ([System.IO.Path]::GetFullPath($Screenshot))
  if ($LASTEXITCODE -ne 0) {
    $logRoot = Join-Path $userData 'logs'
    $evidence = if (Test-Path -LiteralPath $logRoot) {
      @(Get-ChildItem -LiteralPath $logRoot -File -Recurse -ErrorAction SilentlyContinue |
        Select-String -Pattern '\[activation\]|\[terminal\]|Started local extension host|Activating extension|Error activating extension' -CaseSensitive:$false |
        ForEach-Object { "$($_.Path):$($_.LineNumber):$($_.Line)" })
    } else { @() }
    if ($evidence.Count -gt 0) { Write-Output ($evidence -join [Environment]::NewLine) }
    throw 'Point console channel verification failed.'
  }
  $result
} finally {
  if ($process) {
    $ids = @((Get-OwnedProcesses $process.Id) | ForEach-Object { [int]$_.ProcessId })
    if ($ids.Count -gt 0) {
      Stop-Process -Id ($ids | Sort-Object -Descending) -Force -ErrorAction SilentlyContinue
      Start-Sleep -Milliseconds 500
    }
  }
  $resolved = [System.IO.Path]::GetFullPath($testRoot); $prefix = $buildRoot + [System.IO.Path]::DirectorySeparatorChar
  if ($resolved.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase) -and (Test-Path -LiteralPath $resolved)) { Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction SilentlyContinue }
}
