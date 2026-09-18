param(
  [string]$Executable = '',
  [string]$Screenshot = ''
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) { $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe' }
if (-not $Screenshot) { $Screenshot = Join-Path $buildRoot 'code-oss\point-chronicle.png' }
$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start(); $port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port; $listener.Stop()
$testRoot = Join-Path $buildRoot ("chronicle-$([guid]::NewGuid().ToString('N'))")
$world = Join-Path $testRoot 'world'
$userData = Join-Path $testRoot 'user-data'
$extensions = Join-Path $testRoot 'extensions'
New-Item -ItemType Directory -Force -Path $world, $userData, $extensions, (Split-Path -Parent $Screenshot) | Out-Null
$process = $null

function Invoke-Git([string[]]$Arguments) {
  & git -C $world @Arguments | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Git fixture command failed: $($Arguments -join ' ')" }
}
function Get-OwnedProcesses([int]$RootId) {
  $all = @(Get-CimInstance Win32_Process)
  $ids = [System.Collections.Generic.HashSet[int]]::new(); [void]$ids.Add($RootId)
  do { $before = $ids.Count; foreach ($item in $all) { if ($ids.Contains([int]$item.ParentProcessId)) { [void]$ids.Add([int]$item.ProcessId) } } } while ($ids.Count -gt $before)
  return @($all | Where-Object { $ids.Contains([int]$_.ProcessId) })
}

try {
  Invoke-Git @('init','-b','main')
  Invoke-Git @('config','user.email','point@local.test')
  Invoke-Git @('config','user.name','SAGE-7')
  Copy-Item -LiteralPath (Join-Path $projectRoot 'examples\go-health\go.mod') -Destination $world
  Invoke-Git @('add','go.mod'); Invoke-Git @('commit','-m','init: open a new world')
  Copy-Item -LiteralPath (Join-Path $projectRoot 'examples\go-health\main.go') -Destination $world
  Invoke-Git @('add','main.go'); Invoke-Git @('commit','-m','feat: add health check')
  Copy-Item -LiteralPath (Join-Path $projectRoot 'examples\go-health\main_test.go') -Destination $world
  Copy-Item -LiteralPath (Join-Path $projectRoot 'examples\go-health\README.md') -Destination $world
  Invoke-Git @('add','main_test.go')

  $process = Start-Process -FilePath $Executable -ArgumentList @('--user-data-dir',$userData,'--extensions-dir',$extensions,"--remote-debugging-port=$port",'--disable-updates','--skip-welcome','--skip-release-notes',$world) -WindowStyle Hidden -PassThru
  $result = & node (Join-Path $PSScriptRoot 'verify-point-chronicle.mjs') "http://127.0.0.1:$port" ([System.IO.Path]::GetFullPath($Screenshot))
  if ($LASTEXITCODE -ne 0) { throw 'Point Chronicle verification failed.' }
  $result
} finally {
  if ($process) {
    $ids = @((Get-OwnedProcesses $process.Id) | ForEach-Object { [int]$_.ProcessId })
    if ($ids.Count -gt 0) { Stop-Process -Id ($ids | Sort-Object -Descending) -Force -ErrorAction SilentlyContinue }
  }
  $resolved = [System.IO.Path]::GetFullPath($testRoot); $prefix = $buildRoot + [System.IO.Path]::DirectorySeparatorChar
  if ($resolved.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase) -and (Test-Path -LiteralPath $resolved)) { Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction SilentlyContinue }
}
