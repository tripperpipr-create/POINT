param(
  [string]$Executable = '',
  [int]$WaitSeconds = 12,
  [string]$Screenshot = '',
  # Прогнать вместо аудита другой CDP-зонд: разбор найденного обычно требует
  # уточняющего замера на том же окне, а порт отладки здесь каждый раз новый.
  [string]$Probe = '',
  # Влить свежий CSS в живой рендерер перед замером. Оверлей вшивается в
  # workbench при сборке Code-OSS, а она сносит .cache и идёт минутами — для
  # проверки правки в distribution/resources это несоразмерно. Инъекция даёт
  # тот же ответ за секунды; в сборку правка попадает обычным путём.
  [string[]]$InjectCss = @(),
  # Состояние окна перед замером: idle | file | panel | palette. Пустое окно
  # показывает малую часть хрома — вкладки, крошки, терминал и палитра
  # существуют только после действия пользователя.
  [string]$Scenario = 'idle',
  # CSS расширения для подмены внутри вебвью. Вебвью показывает то, что
  # выложено в приложение, а не то, что лежит в репозитории — без подмены замер
  # говорит о прошлом состоянии продукта.
  [string]$InjectWebviewCss = '',
  # Папка, которую открывает стенд. По умолчанию examples\go-health — плоская,
  # четыре файла в корне. Вложенность там негде взять, а часть замеров верхней
  # части редактора без неё бессмысленна: крошка на файле из корня показывает
  # то же, что вкладка, и повтор нельзя отличить от совпадения.
  [string]$Workspace = ''
)

# Поднимает настоящую оболочку Point со свободным портом отладки и меряет её
# оформление зондом audit-point-workbench.mjs. Хаб меряется на статических
# страницах, а оболочка правит чужую разметку Code-OSS — подделать её страницей
# нельзя, поэтому здесь настоящее окно.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-point-workbench-design.ps1

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $projectRoot 'build'))
if (-not $Executable) { $Executable = Join-Path $projectRoot '.cache\VSCode-win32-x64\Point.exe' }
$Executable = [System.IO.Path]::GetFullPath($Executable)
if (-not (Test-Path -LiteralPath $Executable)) { throw "Point executable is missing: $Executable" }
# `-InjectCss a.css b.css` без запятой отдаёт второй файл позиционно сюда, и
# стенд пытается запустить таблицу стилей как программу. Ошибка Start-Process
# при этом ничего не объясняет, поэтому ловим раньше.
if ([System.IO.Path]::GetExtension($Executable) -ne '.exe') {
  $hint = 'Похоже, второй файл -InjectCss попал сюда позиционно'
  throw ("Executable должен быть .exe, получено: {0}. {1}" -f $Executable, $hint)
}

# Список файлов приходит по-разному: из PowerShell — массивом, из bash или cmd —
# одной строкой через запятую, потому что запятую там не разбирает никто.
# Принимаем обе формы, иначе Node получает путь вида "a.css,b.css" и падает.
$InjectCss = @($InjectCss | Where-Object { $_ } | ForEach-Object { $_ -split ',' } | Where-Object { $_.Trim() })

# Уборка не только после прогона, но и перед ним. Уборка в `finally` не
# срабатывает, если прогон прервали снаружи, и тогда окна той же сборки живут
# дальше: они держат `.cache\VSCode-win32-x64`, следующий gulp падает на
# `EBUSY: rmdir`, а причина к тому времени уже в прошлом дне. Здесь добиваются
# ровно процессы той сборки, которую стенд и так поднимает и гасит сам.
$ownRoot = [System.IO.Path]::GetDirectoryName($Executable)
foreach ($item in @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
  Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($ownRoot, [System.StringComparison]::OrdinalIgnoreCase) })) {
  try { Stop-Process -Id ([int]$item.ProcessId) -Force -ErrorAction Stop } catch {}
}

$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
$listener.Stop()

$testRoot = Join-Path $buildRoot ("workbench-design-$([guid]::NewGuid().ToString('N'))")
$userData = Join-Path $testRoot 'user-data'
$extensions = Join-Path $testRoot 'extensions'
$workspace = if ($Workspace) { [System.IO.Path]::GetFullPath($Workspace) } else { Join-Path $projectRoot 'examples\go-health' }
New-Item -ItemType Directory -Force -Path $userData, $extensions | Out-Null

$process = $null

function Get-OwnedProcesses([int]$RootId) {
  $all = @(Get-CimInstance Win32_Process)
  $ids = [System.Collections.Generic.HashSet[int]]::new()
  [void]$ids.Add($RootId)
  do {
    $before = $ids.Count
    foreach ($item in $all) {
      if ($ids.Contains([int]$item.ParentProcessId)) { [void]$ids.Add([int]$item.ProcessId) }
    }
  } while ($ids.Count -gt $before)
  return @($all | Where-Object { $ids.Contains([int]$_.ProcessId) })
}

$exitCode = 0
try {
  # Нативный стартовый экран показывается только когда папка не открыта, и
  # только если его не подавили `--skip-welcome`. Остальные сценарии, наоборот,
  # требуют открытой папки и тишины при старте.
  if ($Scenario -eq 'welcome') {
    $arguments = @(
      '--user-data-dir', $userData,
      '--extensions-dir', $extensions,
      "--remote-debugging-port=$port",
      '--disable-updates', '--skip-release-notes'
    )
  } else {
    $arguments = @(
      '--user-data-dir', $userData,
      '--extensions-dir', $extensions,
      "--remote-debugging-port=$port",
      '--disable-updates', '--skip-welcome', '--skip-release-notes',
      $workspace
    )
  }
  $process = Start-Process -FilePath $Executable -ArgumentList $arguments -WindowStyle Hidden -PassThru
  Start-Sleep -Seconds $WaitSeconds
  $endpoint = "http://127.0.0.1:$port"

  Write-Output "оболочка поднята: $endpoint"
  if ($InjectCss.Count -gt 0) {
    & node (Join-Path $PSScriptRoot 'preview-point-css.mjs') $endpoint @InjectCss
    Start-Sleep -Milliseconds 400
  }
  # Сценарий готовит окно, и делает это тот, кого мы запустили. Зонд идёт
  # ВМЕСТО аудита, поэтому сценарий ему передаётся переменной окружения — иначе
  # зонд мерит окно в покое, какой бы -Scenario ни стоял в командной строке.
  $env:POINT_SCENARIO = $Scenario
  if ($Probe) { & node $Probe $endpoint } else { & node (Join-Path $PSScriptRoot 'audit-point-workbench.mjs') $endpoint $Scenario $InjectWebviewCss }
  $exitCode = $LASTEXITCODE

  if ($Screenshot) {
    & node (Join-Path $PSScriptRoot 'capture-cdp-page.mjs') $endpoint $Screenshot
  }
}
finally {
  if ($process) {
    $owned = Get-OwnedProcesses $process.Id
    foreach ($item in $owned) {
      try { Stop-Process -Id ([int]$item.ProcessId) -Force -ErrorAction Stop } catch {}
    }
  }
  # Уборка по дереву процессов ненадёжна: `point-core.exe` расширение запускает
  # само, и к моменту сбора дерева он либо ещё не появился, либо уже
  # переподчинён. После нескольких прогонов таких сирот накапливается десяток,
  # и они держат папку сборки — gulp падает на `EBUSY: rmdir`. Добиваем по пути
  # исполняемого файла: это ровно процессы той сборки, которую поднял стенд.
  $ownRoot = [System.IO.Path]::GetDirectoryName($Executable)
  $strays = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
    Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($ownRoot, [System.StringComparison]::OrdinalIgnoreCase) })
  foreach ($item in $strays) {
    try { Stop-Process -Id ([int]$item.ProcessId) -Force -ErrorAction Stop } catch {}
  }
}

exit $exitCode
