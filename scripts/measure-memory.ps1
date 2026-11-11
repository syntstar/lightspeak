<#
.SYNOPSIS
  Measures RAM and CPU of an app including all child processes (e.g. Lightspeak + msedgewebview2, or Discord).

.EXAMPLE
  ./scripts/measure-memory.ps1 -Name Lightspeak -Seconds 30
  ./scripts/measure-memory.ps1 -Name Discord -Seconds 30 -Csv results.csv -Label "idle"
#>
param(
  [Parameter(Mandatory = $true)][string]$Name,   # process name without .exe (root process)
  [int]$Seconds = 20,                             # CPU sampling window
  [string]$Csv = "",                              # optional: append result to CSV
  [string]$Label = ""                             # optional: scenario label (idle, voice, stream...)
)

$ErrorActionPreference = 'Stop'

function Get-ProcessTree([string]$rootName) {
  $all = Get-CimInstance Win32_Process | Select-Object ProcessId, ParentProcessId, Name
  $roots = $all | Where-Object { $_.Name -ieq "$rootName.exe" }
  if (-not $roots) { throw "No process '$rootName.exe' found." }
  $ids = [System.Collections.Generic.HashSet[int]]::new()
  $queue = [System.Collections.Generic.Queue[int]]::new()
  foreach ($r in $roots) { [void]$ids.Add([int]$r.ProcessId); $queue.Enqueue([int]$r.ProcessId) }
  while ($queue.Count -gt 0) {
    $p = $queue.Dequeue()
    foreach ($c in ($all | Where-Object { $_.ParentProcessId -eq $p })) {
      if ($ids.Add([int]$c.ProcessId)) { $queue.Enqueue([int]$c.ProcessId) }
    }
  }
  return $ids
}

$ids = Get-ProcessTree $Name
$procs = foreach ($id in $ids) { Get-Process -Id $id -ErrorAction SilentlyContinue }

$cpuStartMs = ($procs | ForEach-Object { $_.TotalProcessorTime.TotalMilliseconds } | Measure-Object -Sum).Sum
Start-Sleep -Seconds $Seconds
$procs2 = foreach ($id in $ids) { Get-Process -Id $id -ErrorAction SilentlyContinue }
$cpuEndMs = ($procs2 | ForEach-Object { $_.TotalProcessorTime.TotalMilliseconds } | Measure-Object -Sum).Sum
$cores = [Environment]::ProcessorCount
$cpuPct = [math]::Round((($cpuEndMs - $cpuStartMs) / ($Seconds * 1000 * $cores)) * 100, 2)

$ws = [math]::Round((($procs2 | Measure-Object WorkingSet64 -Sum).Sum) / 1MB, 1)
$priv = [math]::Round((($procs2 | Measure-Object PrivateMemorySize64 -Sum).Sum) / 1MB, 1)

$result = [pscustomobject]@{
  Timestamp       = (Get-Date).ToString('s')
  App             = $Name
  Label           = $Label
  Processes       = @($procs2).Count
  WorkingSetMB    = $ws      # comparable to Task Manager "Memory" (incl. shared pages)
  PrivateBytesMB  = $priv    # memory exclusively owned by the app
  CpuPercentTotal = $cpuPct  # average across all logical cores during the window
  Seconds         = $Seconds
}

$result | Format-List
$procs2 | Group-Object ProcessName | ForEach-Object {
  [pscustomobject]@{ Process = $_.Name; Count = $_.Count; WorkingSetMB = [math]::Round((($_.Group | Measure-Object WorkingSet64 -Sum).Sum) / 1MB, 1) }
} | Sort-Object WorkingSetMB -Descending | Format-Table -AutoSize

if ($Csv) {
  $exists = Test-Path $Csv
  $result | Export-Csv -Path $Csv -Append -NoTypeInformation -Encoding UTF8
  if (-not $exists) { Write-Host "Created $Csv" }
}
