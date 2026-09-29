# Cai dat Windows Scheduled Task tu dong dong bo Git len GitHub
param(
    [int]$IntervalMinutes = 30
)

$TaskName = "SupportFlastAutoGitSync"
$ScriptPath = "F:\supportflast.dev\auto_git_sync.ps1"
$Action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$ScriptPath`""
$Trigger = New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval (New-TimeSpan -Minutes $IntervalMinutes) -RepetitionDuration ([TimeSpan]::MaxValue)
$Settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RunOnlyIfNetworkAvailable

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "  CAI DAT TU DONG DONG BO GITHUB THEO LICH TRINH WINDOWS  " -ForegroundColor Yellow
Write-Host "==========================================================" -ForegroundColor Cyan

try {
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
    Register-ScheduledTask -TaskName $TaskName -Action $Action -Trigger $Trigger -Settings $Settings -Description "Tu dong dong bo SupportFlast Dev len GitHub moi $IntervalMinutes phut"
    Write-Host ""
    Write-Host ">>> [THANH CONG] Da dang ky tac vu '$TaskName' tu dong dong bo GitHub moi $IntervalMinutes phut! <<<" -ForegroundColor Green
    Write-Host "Co the kiem tra trong Task Scheduler cua Windows bat ky luc nao." -ForegroundColor Gray
} catch {
    Write-Host ""
    Write-Host "[LOI] Khong the dang ky Scheduled Task: $_" -ForegroundColor Red
}
