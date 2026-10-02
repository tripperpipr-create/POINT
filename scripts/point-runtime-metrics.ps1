param([Parameter(Mandatory)][string]$PidFile)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
# Raw counters retain CPU time for the protected vmmemWSL process, unlike
# Get-Process.CPU. Never emit command lines, environments or unrelated PIDs.
while ($true) {
    $ids = @(Get-Content -LiteralPath $PidFile -Raw | ConvertFrom-Json)
    $selected = @(Get-CimInstance Win32_PerfRawData_PerfProc_Process | Where-Object {
        $ids -contains [int]$_.IDProcess -or $_.Name -match '^(vmmemWSL|wslservice|wslhost|wslrelay|point-runtime|com\.docker\.backend|Docker Desktop)(#\d+)?$'
    } | ForEach-Object {
        [ordered]@{ pid=[int]$_.IDProcess; name=$_.Name;
            cpu100ns=[string]$_.PercentProcessorTime;
            workingSet=[string]$_.WorkingSet; privateBytes=[string]$_.PrivateBytes }
    })
    [ordered]@{ utc=[DateTime]::UtcNow.ToString('o'); processes=$selected } | ConvertTo-Json -Depth 4 -Compress
    Start-Sleep -Milliseconds 750
}
