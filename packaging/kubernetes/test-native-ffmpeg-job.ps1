# Credential-free test: exact owned launcher -> branch -> pinned NVENC FFmpeg.
# Native execution belongs to the coordinator after source/hash review.
param([ValidateSet('C:\ether-gpu-lab','D:\ether-gpu-lab')][string]$Lab = 'C:\ether-gpu-lab')
$ErrorActionPreference = 'Stop'
$LauncherPath = Join-Path $Lab 'ether-lab-launcher.exe'
$FFmpegPath = Join-Path $Lab 'ffmpeg-8.1.3-1\ffmpeg.exe'
$BranchFile = Join-Path $Lab 'specialist.child.pid'
$FFmpegFile = Join-Path $Lab 'probe.ffmpeg.pid'
foreach ($Path in @($LauncherPath,$FFmpegPath)) {
    if (-not (Test-Path $Path -PathType Leaf)) { throw 'Required owned test executable missing' }
}
foreach ($Path in @($BranchFile,$FFmpegFile,(Join-Path $Lab 'specialist.pid'))) {
    if (Test-Path $Path) { throw 'Existing owned PID record requires review before this test' }
}
$Launcher = $null
$Branch = $null
$Encoder = $null
$AncestryVerified = $false
$OutputLines = New-Object 'Collections.Generic.List[string]'
try {
    $Info = New-Object Diagnostics.ProcessStartInfo
    $Info.FileName = $LauncherPath
    $Info.Arguments = '--self-test-ffmpeg'
    $Info.WorkingDirectory = $Lab
    $Info.UseShellExecute = $false
    $Info.CreateNoWindow = $true
    $Info.RedirectStandardOutput = $true
    $Info.RedirectStandardError = $true
    $Launcher = New-Object Diagnostics.Process
    $Launcher.StartInfo = $Info
    if (-not $Launcher.Start()) { throw 'Owned launcher did not start' }
    $LauncherHandle = $Launcher.Handle # Cache before any detached process exits.
    $Started = $Launcher.StartTime.ToUniversalTime()
    $Errors = $Launcher.StandardError.ReadToEndAsync()
    $Deadline = [DateTime]::UtcNow.AddSeconds(10)
    while ((-not (Test-Path $BranchFile)) -or (-not (Test-Path $FFmpegFile))) {
        if ($Launcher.HasExited -or [DateTime]::UtcNow -ge $Deadline) { throw 'Owned descendants did not appear within deadline' }
        Start-Sleep -Milliseconds 100
    }
    $BranchId = [int]([IO.File]::ReadAllText($BranchFile).Trim())
    $EncoderId = [int]([IO.File]::ReadAllText($FFmpegFile).Trim())
    $Branch = [Diagnostics.Process]::GetProcessById($BranchId)
    $BranchHandle = $Branch.Handle
    $Encoder = [Diagnostics.Process]::GetProcessById($EncoderId)
    $EncoderHandle = $Encoder.Handle
    if ($Branch.StartTime.ToUniversalTime() -lt $Started -or $Encoder.StartTime.ToUniversalTime() -lt $Started) { throw 'Captured process predates this test' }
    if ($Branch.MainModule.FileName -ne $LauncherPath -or $Encoder.MainModule.FileName -ne $FFmpegPath) { throw 'Unexpected owned process executable' }
    $BranchRow = Get-CimInstance Win32_Process -Filter "ProcessId=$BranchId"
    $EncoderRow = Get-CimInstance Win32_Process -Filter "ProcessId=$EncoderId"
    if ($BranchRow.ParentProcessId -ne $Launcher.Id -or $EncoderRow.ParentProcessId -ne $Branch.Id) { throw 'Exact ancestry mismatch' }
    $AncestryVerified = $true
    $SawFrames = $false
    $Line = $Launcher.StandardOutput.ReadLineAsync()
    while (-not $SawFrames -and [DateTime]::UtcNow -lt $Deadline) {
        if ($Line.Wait(200)) {
            if ($null -eq $Line.Result) { break }
            $OutputLines.Add($Line.Result)
            if ($Line.Result -match '^frame=(\d+)$' -and [int]$Matches[1] -gt 0) { $SawFrames = $true; break }
            $Line = $Launcher.StandardOutput.ReadLineAsync()
        }
    }
    if (-not $SawFrames) { throw 'No actual NVENC frame progress observed before containment kill' }
    # Kill the exact retained launcher object, never a name or a re-looked-up PID.
    $Launcher.Kill()
    if (-not $Launcher.WaitForExit(10000) -or -not $Branch.WaitForExit(10000) -or -not $Encoder.WaitForExit(10000)) {
        throw 'An exact captured job descendant remained alive after launcher death'
    }
    if (-not $Errors.Wait(3000)) { throw 'Owned log pipe did not close after job termination' }
    $Evidence = @{
        nativeFFmpegContainmentVerified=$true
        launcherPID=$Launcher.Id; branchPID=$Branch.Id; ffmpegPID=$Encoder.Id
        launcherCreatedUTC=$Started.ToString('o')
        branchCreatedUTC=$Branch.StartTime.ToUniversalTime().ToString('o')
        ffmpegCreatedUTC=$Encoder.StartTime.ToUniversalTime().ToString('o')
        nonzeroFrameProgress=$SawFrames
        launcherExited=$Launcher.HasExited; branchExited=$Branch.HasExited; ffmpegExited=$Encoder.HasExited
        stderrBytes=[Text.Encoding]::UTF8.GetByteCount($Errors.Result)
        distributedGPUVerified=$false
    }
    $Evidence | ConvertTo-Json | Write-Output
}
finally {
    try {
        if ($null -ne $Launcher -and -not $Launcher.HasExited) {
            $Launcher.Kill()
            if (-not $Launcher.WaitForExit(10000)) { throw 'Owned launcher did not exit during cleanup' }
        }
        # Remove only this test's PID records after captured descendants are dead.
        if ($AncestryVerified -and $Branch.HasExited -and $Encoder.HasExited) {
            if ((Test-Path $BranchFile) -and [IO.File]::ReadAllText($BranchFile).Trim() -eq [string]$Branch.Id) { Remove-Item -LiteralPath $BranchFile }
            if ((Test-Path $FFmpegFile) -and [IO.File]::ReadAllText($FFmpegFile).Trim() -eq [string]$Encoder.Id) { Remove-Item -LiteralPath $FFmpegFile }
        }
    }
    finally {
        if ($null -ne $Launcher) { $Launcher.Dispose() }
        if ($null -ne $Branch) { $Branch.Dispose() }
        if ($null -ne $Encoder) { $Encoder.Dispose() }
    }
}
