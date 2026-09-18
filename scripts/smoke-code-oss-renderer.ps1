param(
  [string]$Executable = '',
  [string]$Workspace = '',
  [int]$WaitSeconds = 12,
  [int]$MaxIdlePrivateMB = 1280
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) {
  $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe'
}
$Executable = [System.IO.Path]::GetFullPath($Executable)
if (-not (Test-Path -LiteralPath $Executable)) {
  throw "Point executable is missing: $Executable"
}
if (-not $Workspace) { $Workspace = Join-Path $projectRoot 'examples\go-health' }
$Workspace = [System.IO.Path]::GetFullPath($Workspace)
if (-not (Test-Path -LiteralPath $Workspace -PathType Container)) {
  throw "Point renderer smoke workspace is missing: $Workspace"
}

$smokeRoot = Join-Path $buildRoot ("renderer-smoke-$([guid]::NewGuid().ToString('N'))")
$userData = Join-Path $smokeRoot 'user-data'
$extensions = Join-Path $smokeRoot 'extensions'
$chromiumLog = Join-Path $smokeRoot 'chromium.log'
New-Item -ItemType Directory -Force -Path $userData, $extensions | Out-Null
$process = $null
$ownedIds = @()

try {
  $arguments = @(
    '--user-data-dir', $userData,
    '--extensions-dir', $extensions,
    '--enable-logging', "--log-file=$chromiumLog", '--v=1',
    '--disable-updates', '--skip-welcome', '--skip-release-notes',
    $Workspace
  )
  $process = Start-Process -FilePath $Executable -ArgumentList $arguments -WindowStyle Hidden -PassThru

  # Дерево процессов окна: собирается заново на каждой попытке, потому что
  # ядро появляется позже renderer'а.
  $collect = {
    $all = Get-CimInstance Win32_Process
    $ids = [System.Collections.Generic.HashSet[int]]::new()
    [void]$ids.Add($process.Id)
    do {
      $before = $ids.Count
      foreach ($item in $all) {
        if ($ids.Contains([int]$item.ParentProcessId)) { [void]$ids.Add([int]$item.ProcessId) }
      }
    } while ($ids.Count -gt $before)
    ,@($all | Where-Object { $ids.Contains([int]$_.ProcessId) })
  }

  # Ждём условие, а не время. Фиксированные $WaitSeconds — гонка: после холодной
  # пересборки ядро (20 МиБ) поднимается дольше двенадцати секунд, и сборка
  # падала на `localCoreProcesses: 0`, хотя через тридцать секунд их ровно один.
  # Требование не ослаблено: ниже по-прежнему проверяется «ровно одно ядро»,
  # просто замер снимается тогда, когда окну дали подняться.
  $deadline = (Get-Date).AddSeconds([math]::Max($WaitSeconds, 45))
  do {
    Start-Sleep -Seconds 2
    $owned = & $collect
    $coreUp = @($owned | Where-Object { $_.Name -match '^point-core(?:\.exe)?$' -or $_.CommandLine -match 'point-core(?:\.exe)?' }).Count -ge 1
  } while (-not $coreUp -and (Get-Date) -lt $deadline)
  # Ещё пауза после появления ядра: замер памяти должен видеть окно в покое,
  # а не в середине запуска.
  Start-Sleep -Seconds ([math]::Min($WaitSeconds, 8))
  $owned = & $collect
  $allProcesses = $owned
  $ownedIds = @($owned | ForEach-Object { [int]$_.ProcessId })
  $rendererCount = @($owned | Where-Object { $_.CommandLine -match '--type=renderer' }).Count
  $localCoreCount = @($owned | Where-Object { $_.Name -match '^point-core(?:\.exe)?$' -or $_.CommandLine -match 'point-core(?:\.exe)?' }).Count
  $workingSetBytes = ($owned | Measure-Object -Property WorkingSetSize -Sum).Sum
  $processMemory = @($owned | ForEach-Object {
    $nativeProcess = Get-Process -Id ([int]$_.ProcessId) -ErrorAction SilentlyContinue
    $kind = if ($_.CommandLine -match '--type=([^\s"]+)') { $Matches[1] } elseif ($_.CommandLine -match 'extensionHost') { 'extension-host' } else { 'main' }
    $subtype = if ($_.CommandLine -match '--utility-sub-type=([^\s"]+)') { $Matches[1] } else { '' }
    [pscustomobject]@{
      pid = [int]$_.ProcessId
      kind = $kind
      subtype = $subtype
      workingSetBytes = [double]$_.WorkingSetSize
      privateBytes = if ($nativeProcess) { [double]$nativeProcess.PrivateMemorySize64 } else { 0 }
    }
  })
  $privateBytes = ($processMemory | Measure-Object -Property privateBytes -Sum).Sum
  $processMemoryReport = @($processMemory | Sort-Object privateBytes -Descending | ForEach-Object {
    [ordered]@{
      pid = $_.pid
      kind = $_.kind
      subtype = $_.subtype
      workingSetMB = [math]::Round(($_.workingSetBytes / 1MB), 1)
      privateMB = [math]::Round(($_.privateBytes / 1MB), 1)
    }
  })
  $main = Get-Process -Id $process.Id -ErrorAction SilentlyContinue
  $fatalLines = @()
  if (Test-Path -LiteralPath $chromiumLog) {
    $fatalLines = @(Select-String -LiteralPath $chromiumLog -Pattern 'Onboarding requires a default chat agent|Uncaught \(in promise\)|\[uncaught exception\]|FATAL' -CaseSensitive:$false | ForEach-Object { $_.Line })
  }
  $authPromptLines = @()
  $runtimeErrorLines = @()
  $idleExtensionHostLines = @()
  $workbenchLogs = Join-Path $userData 'logs'
  if (Test-Path -LiteralPath $workbenchLogs) {
    $authPromptLines = @(Get-ChildItem -LiteralPath $workbenchLogs -File -Recurse -ErrorAction SilentlyContinue |
      Select-String -Pattern "\[sessions welcome\] Showing sign-in dialog|Authentication is required to use Copilot|Timed out waiting for authentication provider '(local-agent|point)'|Registering agent provider: copilotcli|builtInExtensionsEnabledWithAutoUpdates" -CaseSensitive:$false |
      ForEach-Object { "$($_.Path):$($_.LineNumber):$($_.Line)" })
    $runtimeErrorLines = @(Get-ChildItem -LiteralPath $workbenchLogs -File -Recurse -ErrorAction SilentlyContinue |
      Select-String -Pattern 'Activating extension .* failed|Cannot find module|No default agent contributed' -CaseSensitive:$false |
      ForEach-Object { "$($_.Path):$($_.LineNumber):$($_.Line)" })
    $idleExtensionHostLines = @(Get-ChildItem -LiteralPath $workbenchLogs -File -Recurse -ErrorAction SilentlyContinue |
      Select-String -Pattern 'Started local extension host with pid|Extension host with pid .* started' -CaseSensitive:$false |
      ForEach-Object { "$($_.Path):$($_.LineNumber):$($_.Line)" })
  }
  # One extension host and one shared point-core are intentional: the permanent
  # right Assistant opens on startup and observes IDE context in the background.
  # The same extension-host PID appears in renderer and exthost logs, so count
  # unique processes rather than log lines. More than one core is still a leak.
  $idleExtensionHostPids = @($idleExtensionHostLines | ForEach-Object {
    if ($_ -match '(?:pid\s+)(\d+)') { [int]$Matches[1] }
  } | Sort-Object -Unique)
  $privateMemoryMB = [math]::Round(([double]$privateBytes / 1MB), 1)
  $result = [ordered]@{
    executable = $Executable
    processCount = $owned.Count
    rendererCount = $rendererCount
    localCoreProcesses = $localCoreCount
    totalWorkingSetMB = [math]::Round(([double]$workingSetBytes / 1MB), 1)
    totalPrivateMB = $privateMemoryMB
    idlePrivateMemoryBudgetMB = $MaxIdlePrivateMB
    withinIdlePrivateMemoryBudget = $privateMemoryMB -le $MaxIdlePrivateMB
    idleExtensionHostStarts = $idleExtensionHostPids.Count
    processMemory = $processMemoryReport
    responding = [bool]($main -and $main.Responding)
    fatalRendererErrors = $fatalLines.Count
    authenticationPrompts = $authPromptLines.Count
    runtimeErrors = $runtimeErrorLines.Count
  }
  if (-not $result.responding -or $rendererCount -lt 1 -or $localCoreCount -ne 1 -or $fatalLines.Count -gt 0 -or $authPromptLines.Count -gt 0 -or $runtimeErrorLines.Count -gt 0 -or $idleExtensionHostPids.Count -gt 1 -or $privateMemoryMB -gt $MaxIdlePrivateMB) {
    $allFailures = @($fatalLines) + @($authPromptLines) + @($runtimeErrorLines) + @($idleExtensionHostLines)
    $detail = ($result | ConvertTo-Json -Depth 5 -Compress)
    if ($allFailures.Count) { $detail += [Environment]::NewLine + ($allFailures -join [Environment]::NewLine) }
    throw "Code-OSS renderer smoke failed:`n$detail"
  }
  $result | ConvertTo-Json
} finally {
  if ($ownedIds.Count -gt 0) {
    Stop-Process -Id ($ownedIds | Sort-Object -Descending) -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
  } elseif ($process) {
    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
  }
  # `point-core.exe` расширение запускает само и к моменту сбора дерева бывает
  # уже переподчинён — по дереву он не убирается и переживает смоук. Сироты
  # держат папку сборки, и следующий gulp падает на `EBUSY: rmdir`. Добиваем по
  # пути исполняемого файла: это процессы ровно той сборки, что мы поднимали.
  $ownRoot = [System.IO.Path]::GetDirectoryName($Executable)
  $strays = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
    Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($ownRoot, [System.StringComparison]::OrdinalIgnoreCase) })
  foreach ($item in $strays) {
    Stop-Process -Id ([int]$item.ProcessId) -Force -ErrorAction SilentlyContinue
  }
  if ($strays.Count -gt 0) { Start-Sleep -Seconds 1 }
  $resolvedSmoke = [System.IO.Path]::GetFullPath($smokeRoot)
  $buildPrefix = $buildRoot + [System.IO.Path]::DirectorySeparatorChar
  if ($resolvedSmoke.StartsWith($buildPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
    Remove-Item -LiteralPath $resolvedSmoke -Recurse -Force -ErrorAction SilentlyContinue
  }
}
