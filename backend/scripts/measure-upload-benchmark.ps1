[CmdletBinding()]
param(
    [string]$BenchmarkPattern = '^BenchmarkObjectUploadCurrentPath$/^history_100$/^batch_1000$',
    [ValidateRange(1, 100)]
    [int]$Count = 1,
    [ValidateRange(25, 5000)]
    [int]$SampleIntervalMilliseconds = 100
)

$ErrorActionPreference = 'Stop'

$originalTemp = $env:TEMP
$originalTmp = $env:TMP
$systemTemp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$artifactRoot = Join-Path $systemTemp ('light-oss-perf-artifacts-' + [guid]::NewGuid().ToString('N'))
$runRoot = Join-Path $systemTemp ('light-oss-perf-run-' + [guid]::NewGuid().ToString('N'))
$process = $null
$exitCode = 1

function Remove-TaskTempDirectory {
    param([Parameter(Mandatory)][string]$Path)

    $resolved = [IO.Path]::GetFullPath($Path)
    $systemTempPrefix = $systemTemp.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if (-not $resolved.StartsWith($systemTempPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove a path outside the system temporary directory: $resolved"
    }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}

try {
    New-Item -ItemType Directory -Path $artifactRoot, $runRoot | Out-Null
    $binary = Join-Path $artifactRoot 'service.test.exe'
    & go test -c -o $binary ./internal/service
    if ($LASTEXITCODE -ne 0) {
        throw "go test -c failed with exit code $LASTEXITCODE"
    }

    $stdout = Join-Path $artifactRoot 'stdout.txt'
    $stderr = Join-Path $artifactRoot 'stderr.txt'
    $env:TEMP = $runRoot
    $env:TMP = $runRoot
    $arguments = @(
        '-test.run=^$',
        "-test.bench=$BenchmarkPattern",
        '-test.benchtime=1x',
        "-test.count=$Count",
        '-test.benchmem'
    )

    $process = Start-Process `
        -FilePath $binary `
        -ArgumentList $arguments `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput $stdout `
        -RedirectStandardError $stderr
    # Keep the process handle open so Windows PowerShell 5.1 can read ExitCode
    # even when the benchmark exits between two sampling ticks.
    $null = $process.Handle

    $logicalProcessors = [Environment]::ProcessorCount
    $peakCPUPercent = 0.0
    $peakWorkingSetBytes = 0L
    $peakTempBytes = 0L
    $lastCPUSeconds = 0.0
    $lastSampleAt = Get-Date

    while (-not $process.HasExited) {
        Start-Sleep -Milliseconds $SampleIntervalMilliseconds
        try {
            $process.Refresh()
            $sampledAt = Get-Date
            $cpuSeconds = $process.TotalProcessorTime.TotalSeconds
            $elapsedSeconds = ($sampledAt - $lastSampleAt).TotalSeconds
            if ($elapsedSeconds -gt 0) {
                $cpuPercent = (($cpuSeconds - $lastCPUSeconds) / $elapsedSeconds) * 100 / $logicalProcessors
                $peakCPUPercent = [Math]::Max($peakCPUPercent, $cpuPercent)
            }
            $lastCPUSeconds = $cpuSeconds
            $lastSampleAt = $sampledAt
            $peakWorkingSetBytes = [Math]::Max($peakWorkingSetBytes, $process.WorkingSet64)
        }
        catch [System.InvalidOperationException] {
            # The process can exit between HasExited and Refresh.
        }

        $tempBytes = (Get-ChildItem -LiteralPath $runRoot -Recurse -File -ErrorAction SilentlyContinue |
                Measure-Object -Property Length -Sum).Sum
        if ($null -eq $tempBytes) {
            $tempBytes = 0
        }
        $peakTempBytes = [Math]::Max($peakTempBytes, [int64]$tempBytes)
    }

    $process.WaitForExit()
    $process.Refresh()
    $exitCode = $process.ExitCode
    Get-Content -LiteralPath $stdout
    if ((Get-Item -LiteralPath $stderr).Length -gt 0) {
        Get-Content -LiteralPath $stderr
    }

    [pscustomobject]@{
        benchmark_pattern        = $BenchmarkPattern
        count                    = $Count
        peak_cpu_percent         = [Math]::Round($peakCPUPercent, 2)
        peak_working_set_bytes   = $peakWorkingSetBytes
        db_wait_count            = 'see db_waits/op in benchmark output'
        db_wait_duration         = 'see db_wait_ns/op in benchmark output'
        peak_temporary_disk_bytes = $peakTempBytes
        sample_interval_ms       = $SampleIntervalMilliseconds
        exit_code                = $exitCode
    } | ConvertTo-Json -Compress
}
finally {
    $env:TEMP = $originalTemp
    $env:TMP = $originalTmp
    if (Test-Path -LiteralPath $artifactRoot) {
        Remove-TaskTempDirectory -Path $artifactRoot
    }
    if (Test-Path -LiteralPath $runRoot) {
        Remove-TaskTempDirectory -Path $runRoot
    }
}

exit $exitCode
