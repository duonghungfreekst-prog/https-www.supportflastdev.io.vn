# Cai dat Windows Scheduled Task tu dong dong bo Git len GitHub
param(
    [int]$IntervalMinutes = 30
)

$TaskName = "SupportFlastAutoGitSync"
$ScriptPath = "F:\supportflast.dev\auto_git_sync.ps1"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "  CAI DAT TU DONG DONG BO GITHUB THEO LICH TRINH WINDOWS  " -ForegroundColor Yellow
Write-Host "==========================================================" -ForegroundColor Cyan

$command = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File $ScriptPath"

try {
    # Dang ky tac vu voi schtasks.exe
    $output = schtasks.exe /create /tn $TaskName /tr $command /sc minute /mo $IntervalMinutes /f
    Write-Host ""
    Write-Host ">>> [THANH CONG] Da dang ky tac vu '$TaskName' tu dong dong bo GitHub moi $IntervalMinutes phut! <<<" -ForegroundColor Green
    Write-Host "Co the kiem tra trong Task Scheduler cua Windows bat ky luc nao." -ForegroundColor Gray
} catch {
    Write-Host ""
    Write-Host "[LOI] Khong the dang ky Scheduled Task: $_" -ForegroundColor Red
}
