# ==============================================================================
# SUPPORTFLAST.DEV - CLOUDFLARE TUNNEL AUTO MANAGER
# Quan ly tu dong hoa ket noi Internet toan cau cho ten mien supportflastdev.io.vn
# Tu dong hoa: Khoi dong cung Web, Cai dat Windows Service 24/7, Tu phuc hoi ket noi
# File: tools\cloudflare_auto_manager.ps1
# ==============================================================================

[CmdletBinding()]
param (
    [Parameter(Position = 0)]
    [ValidateSet("Status", "Start", "Stop", "InstallService", "UninstallService", "QuickTunnel")]
    [string]$Action = "Status",

    [Parameter(Position = 1)]
    [string]$Token = ""
)

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8

$rootDir = Split-Path -Parent $PSScriptRoot
$cloudflaredBin = "$rootDir\tools\cloudflared.exe"
$configFile = "$rootDir\cloudflared_config.yml"
$tokenFile = "$rootDir\tools\cloudflare_token.txt"

# 1. Kiem tra Binary cloudflared.exe
if (-not (Test-Path $cloudflaredBin)) {
    Write-Host "[!] Dang tai cong cu cloudflared.exe chinh thuc tu Cloudflare..." -ForegroundColor Cyan
    try {
        $downloadUrl = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"
        Invoke-WebRequest -Uri $downloadUrl -OutFile $cloudflaredBin -UseBasicParsing
        Write-Host "[OK] Da tai thanh cong cloudflared.exe!" -ForegroundColor Green
    } catch {
        Write-Host "[ERROR] Khong the tai cloudflared.exe: $_" -ForegroundColor Red
        return
    }
}

# 2. Xu ly cac hanh dong
switch ($Action) {
    "Status" {
        Write-Host "==============================================================================" -ForegroundColor Cyan
        Write-Host "        TRANG THAI CLOUDFLARE TUNNEL - TEN MIEN SUPPORTFLASTDEV.IO.VN         " -ForegroundColor Yellow
        Write-Host "==============================================================================" -ForegroundColor Cyan
        
        # Kiem tra Windows Service
        $service = Get-Service -Name "Cloudflared" -ErrorAction SilentlyContinue
        if ($service) {
            Write-Host "  [+] Dich vu Windows Service 'Cloudflared':" -NoNewline
            if ($service.Status -eq "Running") {
                Write-Host " [ DANG CHAY TU DONG 24/7 ]" -ForegroundColor Green
            } else {
                Write-Host " [ DA DUNG: $($service.Status) ]" -ForegroundColor Yellow
            }
            Write-Host "      Khoi dong cung Windows: Tu dong (Automatic)" -ForegroundColor Gray
        } else {
            Write-Host "  [-] Dich vu Windows Service 'Cloudflared': [ Chua cai dat service ]" -ForegroundColor Gray
        }

        # Kiem tra tien trinh Process
        $proc = Get-Process -Name "cloudflared" -ErrorAction SilentlyContinue
        if ($proc) {
            Write-Host "  [+] Tien trinh cloudflared.exe: [ DANG HOAT DONG (PID: $($proc.Id)) ]" -ForegroundColor Green
        } else {
            Write-Host "  [-] Tien trinh cloudflared.exe: [ Khong co tien trinh chay nen ]" -ForegroundColor Gray
        }

        # Kiem tra Token file
        if (Test-Path $tokenFile) {
            Write-Host "  [+] Da luu cau hinh Token tai: tools\cloudflare_token.txt [OK]" -ForegroundColor Green
        }

        # Kiem tra Cong Web cuc bo
        $port80 = Get-NetTCPConnection -LocalPort 80 -State Listen -ErrorAction SilentlyContinue
        $port8080 = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue
        Write-Host ""
        Write-Host "  [+] Cong Web noi bo chuyen tiep:" -ForegroundColor White
        Write-Host "      - Cong 80 (Apache Proxy) : $(if ($port80) { 'Lang nghe [OK]' } else { 'Chua bat [!] ' })" -ForegroundColor $(if ($port80) { 'Green' } else { 'Yellow' })
        Write-Host "      - Cong 8080 (Go Engine)  : $(if ($port8080) { 'Lang nghe [OK]' } else { 'Chua bat [!] ' })" -ForegroundColor $(if ($port8080) { 'Green' } else { 'Yellow' })
        Write-Host "==============================================================================" -ForegroundColor Cyan
    }

    "Start" {
        Write-Host "[+] Khoi dong Cloudflare Tunnel tu dong..." -ForegroundColor Cyan
        
        # 1. Neu Windows Service ton tai thi khoi dong Service
        $service = Get-Service -Name "Cloudflared" -ErrorAction SilentlyContinue
        if ($service) {
            if ($service.Status -ne "Running") {
                Start-Service -Name "Cloudflared" -ErrorAction SilentlyContinue
                Write-Host "[OK] Da khoi dong Windows Service 'Cloudflared' thanh cong." -ForegroundColor Green
            } else {
                Write-Host "[INFO] Windows Service 'Cloudflared' da dang chay tu dong." -ForegroundColor Yellow
            }
            return
        }

        # 2. Neu co Token file hoac Token param -> Chay ngam voi Token
        $effectiveToken = $Token
        if (-not $effectiveToken -and (Test-Path $tokenFile)) {
            $effectiveToken = (Get-Content $tokenFile -Raw).Trim()
        }

        if ($effectiveToken) {
            $existing = Get-Process -Name "cloudflared" -ErrorAction SilentlyContinue
            if (-not $existing) {
                Start-Process -FilePath $cloudflaredBin -ArgumentList "tunnel run --token $effectiveToken" -WindowStyle Hidden
                Start-Sleep -Seconds 1
                Write-Host "[OK] Da tu dong kich hoat Cloudflare Tunnel chay ngam voi Token cau hinh!" -ForegroundColor Green
            } else {
                Write-Host "[INFO] Tien trinh cloudflared.exe da dang hoat dong." -ForegroundColor Yellow
            }
            return
        }

        # 3. Neu co file cau hinh local config
        if (Test-Path $configFile) {
            $existing = Get-Process -Name "cloudflared" -ErrorAction SilentlyContinue
            if (-not $existing) {
                Start-Process -FilePath $cloudflaredBin -ArgumentList "tunnel --config `"$configFile`" run supportflast-tunnel" -WindowStyle Hidden
                Start-Sleep -Seconds 1
                Write-Host "[OK] Da khoi chay tien trinh Cloudflare Tunnel (supportflast-tunnel) ngam tu dong qua config." -ForegroundColor Green
            } else {
                Write-Host "[INFO] Tien trinh cloudflared.exe da dang chay." -ForegroundColor Yellow
            }
            return
        }

        Write-Host "[!] Chua cau hinh Cloudflare Tunnel Token. Nhap dup setup_cloudflare_auto.bat (chon 2) de cai dat tu dong 24/7." -ForegroundColor Yellow
    }

    "Stop" {
        Write-Host "[-] Dang dung Cloudflare Tunnel..." -ForegroundColor Yellow
        $service = Get-Service -Name "Cloudflared" -ErrorAction SilentlyContinue
        if ($service -and $service.Status -eq "Running") {
            Stop-Service -Name "Cloudflared" -Force -ErrorAction SilentlyContinue
        }
        Stop-Process -Name "cloudflared" -Force -ErrorAction SilentlyContinue
        Write-Host "[OK] Da dung toan bo ket noi Cloudflare Tunnel." -ForegroundColor Green
    }

    "InstallService" {
        $effectiveToken = $Token
        if (-not $effectiveToken -and (Test-Path $tokenFile)) {
            $effectiveToken = (Get-Content $tokenFile -Raw).Trim()
        }

        if (-not $effectiveToken) {
            Write-Host "[!] Vui long cung cap Cloudflare Tunnel Token:" -ForegroundColor Yellow
            Write-Host "    Cu phap: .\tools\cloudflare_auto_manager.ps1 InstallService <YOUR_TOKEN>" -ForegroundColor White
            Write-Host "    Lay Token tai: Cloudflare Zero Trust Dashboard -> Networks -> Tunnels" -ForegroundColor Gray
            return
        }

        Write-Host "[+] Dang cai dat Cloudflare Tunnel thanh Windows Service tu dong 24/7..." -ForegroundColor Cyan
        
        # Luu token lai de dung tai khoi dong du phong
        Set-Content -Path $tokenFile -Value $effectiveToken.Trim() -Encoding UTF8
        
        & $cloudflaredBin service install $effectiveToken
        Set-Service -Name "Cloudflared" -StartupType Automatic -ErrorAction SilentlyContinue
        Start-Service -Name "Cloudflared" -ErrorAction SilentlyContinue
        Write-Host "[THANH CONG] Da cai dat Cloudflare Tunnel lam Windows Service chay tu dong 24/7!" -ForegroundColor Green
        Write-Host "Bat cu khi nao may tinh hoac website hoat dong, ten mien supportflastdev.io.vn se tu dong online toan cau." -ForegroundColor Cyan
    }

    "UninstallService" {
        Write-Host "[-] Dang go bo Windows Service Cloudflared..." -ForegroundColor Yellow
        & $cloudflaredBin service uninstall
        Write-Host "[OK] Da go bo service thanh cong." -ForegroundColor Green
    }

    "QuickTunnel" {
        Write-Host "[+] Dang kich hoat Quick Tunnel tam thoi toi cong 80 (Apache Proxy)..." -ForegroundColor Cyan
        Write-Host "    Dang tao duong truyen an toan tu Cloudflare..." -ForegroundColor Gray
        & $cloudflaredBin tunnel --url http://127.0.0.1:80
    }
}
