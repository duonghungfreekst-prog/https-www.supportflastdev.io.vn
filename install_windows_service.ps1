# ==============================================================================
# SupportFlast Unified Core Platform - Windows Service Management Script
# Domain: supportflastdev.io.vn
# File: install_windows_service.ps1
# ==============================================================================
# Script quan tri vien cai dat, go bo, khoi dong, dung va kiem tra trang thai
# cua SupportFlast Engine duoi dang mot Windows Service tu dong khoi dong cung OS.
# Ho tro ca Native Windows Service (sc.exe / SCM) va NSSM (Non-Sucking Service Manager).
# ==============================================================================

[CmdletBinding()]
param (
    [Parameter(Position = 0)]
    [ValidateSet("Install", "Uninstall", "Start", "Stop", "Restart", "Status")]
    [string]$Action = "Install",

    [Parameter()]
    [ValidateSet("Auto", "Native", "NSSM")]
    [string]$Mode = "Auto",

    [Parameter()]
    [string]$ServiceName = "SupportFlastEngine",

    [Parameter()]
    [string]$DisplayName = "SupportFlast Unified Core Engine",

    [Parameter()]
    [string]$Description = "SupportFlast Unified Web UI, REST API Gateway, Cloud Storage & AI Subagents Hub for supportflastdev.io.vn",

    [Parameter()]
    [string]$BinaryPath = "F:\supportflast.dev\supportflast.exe",

    [Parameter()]
    [string]$WorkingDirectory = "F:\supportflast.dev",

    [Parameter()]
    [int]$Port = 8080,

    [Parameter()]
    [switch]$SkipFirewall = $false,

    [Parameter()]
    [switch]$NoStart = $false
)

# Thiet lap bang ma UTF-8
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8

function Write-Header {
    param ([string]$Title)
    Write-Host ""
    Write-Host "==============================================================================" -ForegroundColor Cyan
    Write-Host "  SUPPORTFLAST - QUAN LY WINDOWS SERVICE (supportflastdev.io.vn)" -ForegroundColor Yellow
    Write-Host "  $Title" -ForegroundColor Green
    Write-Host "==============================================================================" -ForegroundColor Cyan
    Write-Host ""
}

function Test-AdminPrivileges {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

# Kiem tra quyen Administrator
if (-not (Test-AdminPrivileges)) {
    Write-Host ""
    Write-Host "[LOI BAO MAT] Script yeu cau quyen Quan tri vien (Administrator) de quan ly Windows Service!" -ForegroundColor Red
    Write-Host "Vui long mo PowerShell bang cach click chuot phai -> 'Run as Administrator', sau do chay lai script." -ForegroundColor Yellow
    Write-Host "Lenh vi du: powershell -ExecutionPolicy Bypass -File .\install_windows_service.ps1 -Action $Action" -ForegroundColor Gray
    Write-Host ""
    exit 1
}

# Chuan hoa duong dan
if (-not [System.IO.Path]::IsPathRooted($BinaryPath)) {
    $BinaryPath = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot $BinaryPath))
}
if (-not [System.IO.Path]::IsPathRooted($WorkingDirectory)) {
    $WorkingDirectory = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot $WorkingDirectory))
}

$LogsDirectory = Join-Path $WorkingDirectory "logs"
$DataDirectory = Join-Path $WorkingDirectory "data"

# Tim kiem binary NSSM neu co
function Find-NSSM {
    $candidates = @(
        (Join-Path $WorkingDirectory "tools\nssm.exe"),
        (Join-Path $WorkingDirectory "nssm.exe"),
        "C:\nssm\win64\nssm.exe",
        "C:\tools\nssm.exe"
    )
    foreach ($cand in $candidates) {
        if (Test-Path $cand) {
            return $cand
        }
    }
    $cmd = Get-Command nssm -ErrorAction SilentlyContinue
    if ($cmd) {
        return $cmd.Source
    }
    return $null
}

# Quan ly Windows Firewall
function Configure-FirewallRule {
    param([int]$ListenPort)
    if ($SkipFirewall) { return }

    $ruleName = "SupportFlast Web Gateway (Port $ListenPort)"
    $existing = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
    if (-not $existing) {
        Write-Host "[-] Cau hinh Windows Firewall: Cho phep ket noi Inbound TCP cong $ListenPort..." -ForegroundColor Cyan
        try {
            New-NetFirewallRule -DisplayName $ruleName `
                                -Direction Inbound `
                                -Protocol TCP `
                                -LocalPort $ListenPort `
                                -Action Allow `
                                -Profile Any `
                                -Description "Cho phep luu luong HTTP/HTTPS cho SupportFlast Engine" | Out-Null
            Write-Host "[+] Da tao thanh cong quy tac Firewall: '$ruleName'" -ForegroundColor Green
        }
        catch {
            Write-Host "[!] Canh bao: Khong the tao Firewall Rule: $($_.Exception.Message)" -ForegroundColor Yellow
        }
    } else {
        Write-Host "[i] Quy tac Firewall '$ruleName' da ton tai." -ForegroundColor Gray
    }
}

# Kiem tra HTTP Health Endpoint
function Test-ServiceHealth {
    $healthUrl = "http://127.0.0.1:$Port/api/health"
    Write-Host "[-] Dang kiem tra phan hoi HTTP Gateway: $healthUrl..." -ForegroundColor Cyan
    try {
        $response = Invoke-RestMethod -Uri $healthUrl -Method Get -TimeoutSec 5 -ErrorAction Stop
        Write-Host "[+] KET NOI THANH CONG TOI GATEWAY!" -ForegroundColor Green
        Write-Host "    - Trang thai HTTP:   $($response.status)" -ForegroundColor Green
        Write-Host "    - Ten mien:          $($response.domain)" -ForegroundColor Green
        Write-Host "    - So Goroutines:     $($response.goroutines)" -ForegroundColor Gray
        Write-Host "    - RAM Alloc:         $([math]::Round($response.alloc_mb, 2)) MB" -ForegroundColor Gray
        Write-Host "    - So luong AI Agent: $($response.subagents)" -ForegroundColor Gray
        Write-Host ""
        Write-Host "==============================================================================" -ForegroundColor Cyan
        Write-Host "  HE THONG SAN SANG PHUC VU TAI: http://localhost:$Port" -ForegroundColor Green
        Write-Host "  NEU DUNG IIS REVERSE PROXY: Cau hinh ARR chuyen tiep ve port $Port" -ForegroundColor Yellow
        Write-Host "==============================================================================" -ForegroundColor Cyan
    }
    catch {
        Write-Host "[!] Chua nhan duoc phan hoi HTTP tu $healthUrl ($($_.Exception.Message))" -ForegroundColor Yellow
        Write-Host "    Neu dich vu vua khoi dong, vui long doi vai giay va kiem tra lai qua: -Action Status" -ForegroundColor Gray
    }
}

# ------------------------------------------------------------------------------
# ACTION: STOP
# ------------------------------------------------------------------------------
function Stop-SupportFlastService {
    Write-Header "DUNG WINDOWS SERVICE: $ServiceName"

    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $svc) {
        Write-Host "[LOI] Dich vu '$ServiceName' chua duoc cai dat!" -ForegroundColor Red
        return
    }

    if ($svc.Status -eq "Stopped") {
        Write-Host "[i] Dich vu '$ServiceName' hien da dung." -ForegroundColor Gray
    } else {
        Write-Host "[-] Dang gui tin hieu dung toi dich vu '$ServiceName'..." -ForegroundColor Cyan
        try {
            Stop-Service -Name $ServiceName -Force
            $svc.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(15))
            Write-Host "[+] Dich vu '$ServiceName' da dung an toan." -ForegroundColor Green
        }
        catch {
            Write-Host "[LOI] Khong the dung dich vu: $($_.Exception.Message)" -ForegroundColor Red
        }
    }
}

# ------------------------------------------------------------------------------
# ACTION: UNINSTALL
# ------------------------------------------------------------------------------
function Uninstall-SupportFlastService {
    param([switch]$Quiet = $false)
    if (-not $Quiet) {
        Write-Header "GO CAI DAT WINDOWS SERVICE: $ServiceName"
    }

    $existingService = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $existingService) {
        if (-not $Quiet) {
            Write-Host "[i] Khong tim thay Windows Service '$ServiceName' tren may chu." -ForegroundColor Yellow
        }
        return
    }

    # Dung dich vu neu dang chay
    if ($existingService.Status -eq "Running") {
        Write-Host "[-] Dang dung dich vu '$ServiceName'..." -ForegroundColor Cyan
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 2
    }

    # Kiem tra NSSM hay Native de go
    $nssmPath = Find-NSSM
    if ($nssmPath) {
        & $nssmPath remove $ServiceName confirm 2>&1 | Out-Null
    }

    # Du phong go bang sc.exe delete
    $delOutput = sc.exe delete $ServiceName
    Start-Sleep -Seconds 1

    $verify = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $verify) {
        Write-Host "[+] Da go bo hoan toan Windows Service '$ServiceName' khoi he thong!" -ForegroundColor Green
    } else {
        Write-Host "[!] Luu y: Dich vu da duoc danh dau de xoa khi dong Services.msc hoac khoi dong lai OS." -ForegroundColor Yellow
    }
}

# ------------------------------------------------------------------------------
# ACTION: START
# ------------------------------------------------------------------------------
function Start-SupportFlastService {
    Write-Header "KHOI DONG WINDOWS SERVICE: $ServiceName"

    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $svc) {
        Write-Host "[LOI] Dich vu '$ServiceName' chua duoc cai dat!" -ForegroundColor Red
        Write-Host "Vui long chay: .\install_windows_service.ps1 -Action Install" -ForegroundColor Yellow
        return
    }

    if ($svc.Status -eq "Running") {
        Write-Host "[i] Dich vu '$ServiceName' hien da dang chay." -ForegroundColor Green
    } else {
        Write-Host "[-] Dang khoi dong dich vu '$ServiceName'..." -ForegroundColor Cyan
        try {
            Start-Service -Name $ServiceName
            $svc.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(15))
            Write-Host "[+] Dich vu '$ServiceName' da khoi dong thanh cong!" -ForegroundColor Green
        }
        catch {
            Write-Host "[LOI] Khong the khoi dong dich vu: $($_.Exception.Message)" -ForegroundColor Red
            Write-Host "Kiem tra log tai: $LogsDirectory" -ForegroundColor Yellow
            return
        }
    }

    # Kiem tra kiem toan HTTP Health
    Start-Sleep -Seconds 2
    Test-ServiceHealth
}

# ------------------------------------------------------------------------------
# ACTION: INSTALL
# ------------------------------------------------------------------------------
function Install-SupportFlastService {
    Write-Header "CAI DAT WINDOWS SERVICE: $ServiceName"

    # 1. Kiem tra Binary nhi phan
    if (-not (Test-Path $BinaryPath)) {
        Write-Host "[LOI] Khong tim thay file nhi phan tai: $BinaryPath" -ForegroundColor Red
        Write-Host "Vui long build file binary truoc bang lenh: make build hoac go build" -ForegroundColor Yellow
        exit 1
    }

    # 2. Tao thu muc logs va data neu chua co
    if (-not (Test-Path $LogsDirectory)) {
        New-Item -ItemType Directory -Path $LogsDirectory -Force | Out-Null
        Write-Host "[+] Da tao thu muc logs tai: $LogsDirectory" -ForegroundColor Gray
    }
    if (-not (Test-Path $DataDirectory)) {
        New-Item -ItemType Directory -Path $DataDirectory -Force | Out-Null
        Write-Host "[+] Da tao thu muc data tai: $DataDirectory" -ForegroundColor Gray
    }

    # 3. Kiem tra service da ton tai hay chua
    $existingService = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($existingService) {
        Write-Host "[!] Windows Service '$ServiceName' da ton tai san (Trang thai: $($existingService.Status))." -ForegroundColor Yellow
        Write-Host "[-] Dang dung va go cai dat phien ban cu de cai moi..." -ForegroundColor Cyan
        Uninstall-SupportFlastService -Quiet
        Start-Sleep -Seconds 2
    }

    # 4. Xac dinh che do cai dat (Native vs NSSM)
    $resolvedMode = $Mode
    $nssmPath = Find-NSSM

    if ($resolvedMode -eq "Auto") {
        if ($nssmPath) {
            $resolvedMode = "NSSM"
        } else {
            $resolvedMode = "Native"
        }
    }

    Write-Host "[i] Che do dich vu: $resolvedMode" -ForegroundColor Cyan
    Write-Host "[i] Ten dich vu:    $ServiceName" -ForegroundColor Gray
    Write-Host "[i] Ten hien thi:   $DisplayName" -ForegroundColor Gray
    Write-Host "[i] File thuc thi:  $BinaryPath" -ForegroundColor Gray
    Write-Host "[i] Thu muc goc:    $WorkingDirectory" -ForegroundColor Gray
    Write-Host "[i] Cong cau hinh:  $Port" -ForegroundColor Gray

    # 5. Thuc hien cai dat theo che do
    if ($resolvedMode -eq "NSSM") {
        if (-not $nssmPath) {
            Write-Host "[LOI] Che do NSSM duoc chon nhung khong tim thay nssm.exe tren he thong!" -ForegroundColor Red
            Write-Host "Vui long dat nssm.exe vao thu muc '$WorkingDirectory\tools' hoac chuyen sang Mode Native." -ForegroundColor Yellow
            exit 1
        }
        Write-Host "[-] Dang cai dat bang NSSM ($nssmPath)..." -ForegroundColor Cyan
        & $nssmPath install $ServiceName "$BinaryPath" | Out-Null
        & $nssmPath set $ServiceName AppDirectory "$WorkingDirectory" | Out-Null
        & $nssmPath set $ServiceName DisplayName "$DisplayName" | Out-Null
        & $nssmPath set $ServiceName Description "$Description" | Out-Null
        & $nssmPath set $ServiceName Start SERVICE_AUTO_START | Out-Null
        & $nssmPath set $ServiceName AppStdout "$LogsDirectory\service_stdout.log" | Out-Null
        & $nssmPath set $ServiceName AppStderr "$LogsDirectory\service_stderr.log" | Out-Null
        $envBlock = "PORT=$Port`nHOST=0.0.0.0`nDATA_DIR=$DataDirectory`nUI_DIR=$WorkingDirectory\supportflast_ui"
        & $nssmPath set $ServiceName AppEnvironmentExtra $envBlock | Out-Null
        & $nssmPath set $ServiceName AppRestartDelay 5000 | Out-Null
        Write-Host "[+] NSSM da cau hinh xong dich vu $ServiceName thanh cong!" -ForegroundColor Green
    }
    else {
        # Native Windows Service qua sc.exe
        Write-Host "[-] Dang tao Native Windows Service bang sc.exe..." -ForegroundColor Cyan
        
        $binArg = "`"$BinaryPath`""
        $scOutput = sc.exe create $ServiceName binPath= $binArg start= auto DisplayName= "$DisplayName"
        if ($LASTEXITCODE -ne 0) {
            Write-Host "[LOI] sc.exe create that bai: $scOutput" -ForegroundColor Red
            exit 1
        }

        # Cap nhat mo ta dich vu
        sc.exe description $ServiceName "$Description" | Out-Null

        # Cau hinh tu dong phuc hoi khi loi (Restart sau 5s, 10s, 30s)
        sc.exe failure $ServiceName reset= 86400 actions= restart/5000/restart/10000/restart/30000 | Out-Null

        Write-Host "[+] Da tao Native Windows Service thanh cong!" -ForegroundColor Green
    }

    # 6. Mo cong Windows Firewall
    Configure-FirewallRule -ListenPort $Port

    # 7. Khoi dong dich vu neu khong co co -NoStart
    if (-not $NoStart) {
        Write-Host ""
        Start-SupportFlastService
    }
    else {
        Write-Host ""
        Write-Host "[i] Cai dat hoan tat. Dich vu chua khoi dong do co -NoStart duoc bat." -ForegroundColor Yellow
        Write-Host "    De khoi dong dich vu, chay: .\install_windows_service.ps1 -Action Start" -ForegroundColor Gray
    }
}

# ------------------------------------------------------------------------------
# ACTION: RESTART
# ------------------------------------------------------------------------------
function Restart-SupportFlastService {
    Write-Header "KHOI DONG LAI WINDOWS SERVICE: $ServiceName"
    Stop-SupportFlastService
    Start-Sleep -Seconds 2
    Start-SupportFlastService
}

# ------------------------------------------------------------------------------
# ACTION: STATUS
# ------------------------------------------------------------------------------
function Get-SupportFlastStatus {
    Write-Header "TRANG THAI WINDOWS SERVICE: $ServiceName"

    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $svc) {
        Write-Host "[i] Dich vu '$ServiceName' CHUA DUOC CAI DAT tren he thong." -ForegroundColor Yellow
        Write-Host "    De cai dat: .\install_windows_service.ps1 -Action Install" -ForegroundColor Gray
        return
    }

    $color = if ($svc.Status -eq "Running") { "Green" } else { "Red" }
    Write-Host "Ten dich vu:      $($svc.Name)" -ForegroundColor Cyan
    Write-Host "Ten hien thi:     $($svc.DisplayName)" -ForegroundColor Cyan
    Write-Host "Trang thai:       $($svc.Status)" -ForegroundColor $color
    Write-Host "Loai khoi dong:   $($svc.StartType)" -ForegroundColor Gray

    # Tim Process ID cua supportflast.exe
    $processes = Get-Process -Name "supportflast" -ErrorAction SilentlyContinue
    if ($processes) {
        Write-Host ""
        Write-Host "--- THONG TIN TIEN TRINH SUPPORTFLAST (PID) ---" -ForegroundColor Cyan
        foreach ($p in $processes) {
            $memMB = [math]::Round($p.WorkingSet64 / 1MB, 2)
            Write-Host "  PID:            $($p.Id)" -ForegroundColor Yellow
            Write-Host "  RAM Su dung:    $memMB MB" -ForegroundColor Yellow
            Write-Host "  Thoi gian chay: $($p.StartTime)" -ForegroundColor Gray
            Write-Host "  Duong dan:      $($p.Path)" -ForegroundColor Gray
        }
    }

    Write-Host ""
    Test-ServiceHealth
}

# ------------------------------------------------------------------------------
# DIEU PHOI HANH DONG
# ------------------------------------------------------------------------------
switch ($Action) {
    "Install"   { Install-SupportFlastService }
    "Uninstall" { Uninstall-SupportFlastService }
    "Start"     { Start-SupportFlastService }
    "Stop"      { Stop-SupportFlastService }
    "Restart"   { Restart-SupportFlastService }
    "Status"    { Get-SupportFlastStatus }
    Default     { Install-SupportFlastService }
}
