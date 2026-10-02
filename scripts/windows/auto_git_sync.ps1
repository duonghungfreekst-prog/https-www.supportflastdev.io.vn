# SupportFlast Dev - Auto Git Sync to GitHub Script
param(
    [string]$CommitMessage = "",
    [switch]$ForcePush = $false
)

$WorkspaceDir = "F:\supportflast.dev"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "   SUPPORTFLAST DEV - TU DONG DONG BO GITHUB              " -ForegroundColor Yellow
Write-Host "==========================================================" -ForegroundColor Cyan

if (-not (Test-Path $WorkspaceDir)) {
    Write-Host "[LOI] Khong tim thay thu muc: $WorkspaceDir" -ForegroundColor Red
    exit 1
}

Set-Location $WorkspaceDir

# 1. Kiem tra trang thai Git
Write-Host "[1/4] Kiem tra trang thai Git..." -ForegroundColor Cyan
$gitStatus = git status --porcelain
if ([string]::IsNullOrWhiteSpace($gitStatus)) {
    Write-Host "[OK] Khong co thay doi moi. Kho chua da cap nhat moi nhat!" -ForegroundColor Green
    exit 0
}

Write-Host "Phat hien cac tap tin thay doi:" -ForegroundColor Yellow
Write-Host $gitStatus -ForegroundColor Gray

# 2. Stage tat ca tep tin
Write-Host "[2/4] Dang stage tap tin (git add .)..." -ForegroundColor Cyan
git add .
if ($LASTEXITCODE -ne 0) {
    Write-Host "[LOI] git add that bai!" -ForegroundColor Red
    exit $LASTEXITCODE
}

# 3. Tao commit
$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
if ([string]::IsNullOrWhiteSpace($CommitMessage)) {
    $CommitMessage = "auto-update: Sync project at $timestamp"
} else {
    $CommitMessage = "$CommitMessage ($timestamp)"
}

Write-Host "[3/4] Commit: '$CommitMessage'..." -ForegroundColor Cyan
git commit -m $CommitMessage
if ($LASTEXITCODE -ne 0) {
    Write-Host "[LOI] git commit that bai!" -ForegroundColor Red
    exit $LASTEXITCODE
}

# 4. Day len GitHub
Write-Host "[4/4] Dang day (push) len GitHub main..." -ForegroundColor Cyan

# Keo rebase neu co thay doi tu remote
git pull --rebase origin main

if ($ForcePush) {
    git push -u origin main --force
} else {
    git push -u origin main
}

if ($LASTEXITCODE -eq 0) {
    Write-Host ""
    Write-Host ">>> [THANH CONG] Da dong bo toan bo du an len GitHub thanh cong luc $timestamp! <<<" -ForegroundColor Green
    Write-Host "URL: https://github.com/duonghungfreekst-prog/https-www.supportflastdev.io.vn" -ForegroundColor Cyan
} else {
    Write-Host ""
    Write-Host "[LOI] Day len GitHub that bai (Ma loi: $LASTEXITCODE)!" -ForegroundColor Red
    exit $LASTEXITCODE
}
