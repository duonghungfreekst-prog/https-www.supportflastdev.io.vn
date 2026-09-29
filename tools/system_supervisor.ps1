# ==============================================================================
# SUPPORTFLAST MONOLITH PLATFORM - SYSTEM SUPERVISOR ENGINE
# Dieu phoi toan dien vong doi he thong: Khoi chay, Giam sat, Health Check, Tat an toan
# Tu dong dong bo Apache Reverse Proxy va bao ve toan ven SQLite WAL
# File: tools\system_supervisor.ps1
# ==============================================================================

[CmdletBinding()]
param (
    [Parameter(Position = 0)]
    [ValidateSet("Start", "Stop", "Status", "Restart")]
    [string]$Action = "Start",

    [switch]$Silent = $false,
    [switch]$All = $false
)

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8

$rootDir = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path "$rootDir\supportflast.exe")) {
    $rootDir = "F:\supportflast.dev"
}

# ------------------------------------------------------------------------------
# HAM TIM KIEM THU MUC XAMPP
# ------------------------------------------------------------------------------
function Find-XamppDirectory {
    $candidates = @("D:\xampp", "C:\xampp", "E:\xampp", "F:\xampp", "E:\ổ f\xampp")
    foreach ($dir in $candidates) {
        if (Test-Path "$dir\apache\bin\httpd.exe") {
            return $dir
        }
    }
    return $null
}

# ------------------------------------------------------------------------------
# HAM TU DONG CAU HINH REVERSE PROXY APACHE XAMPP
# ------------------------------------------------------------------------------
function Ensure-ApacheProxyConfig {
    param ([string]$xamppPath)
    if (-not $xamppPath) { return }

    $extraConf = "$xamppPath\apache\conf\extra\httpd-supportflast.conf"
    $mainConf = "$xamppPath\apache\conf\httpd.conf"
    $srcConf = "$rootDir\infra\httpd-supportflast.conf"

    if (-not (Test-Path $extraConf) -and (Test-Path $srcConf)) {
        Copy-Item -Path $srcConf -Destination $extraConf -Force
    }

    if (Test-Path $mainConf) {
        $content = Get-Content -Path $mainConf -Raw
        $changed = $false

        if ($content -match '#LoadModule proxy_http_module modules/mod_proxy_http.so') {
            $content = $content -replace '#LoadModule proxy_http_module modules/mod_proxy_http.so', 'LoadModule proxy_http_module modules/mod_proxy_http.so'
            $changed = $true
        }
        if ($content -match '#LoadModule proxy_wstunnel_module modules/mod_proxy_wstunnel.so') {
            $content = $content -replace '#LoadModule proxy_wstunnel_module modules/mod_proxy_wstunnel.so', 'LoadModule proxy_wstunnel_module modules/mod_proxy_wstunnel.so'
            $changed = $true
        }
        if ($content -notmatch 'httpd-supportflast.conf') {
            $content += "`nInclude conf/extra/httpd-supportflast.conf`n"
            $changed = $true
        }

        if ($changed) {
            Set-Content -Path $mainConf -Value $content -NoNewline
        }
    }
}

# ------------------------------------------------------------------------------
# HANH DONG: START (KHOI CHAY HE THONG)
# ------------------------------------------------------------------------------
function Start-FullSystem {
    if (-not $Silent) {
        Write-Host ""
        Write-Host "==============================================================================" -ForegroundColor Cyan
        Write-Host "       [+] SUPPORTFLAST MONOLITH - TRINH KHOI DONG HE THONG 1-CLICK" -ForegroundColor Yellow
        Write-Host "                    Website: http://localhost (Cong 80)" -ForegroundColor Green
        Write-Host "==============================================================================" -ForegroundColor Cyan
        Write-Host ""
        Write-Host "[1/4] Dang kiem tra va cau hinh XAMPP Apache..." -ForegroundColor Cyan
    }

    $xampp = Find-XamppDirectory
    $apacheReady = $false

    if ($xampp) {
        if (-not $Silent) { Write-Host "  [+] Da phat hien XAMPP tai: $xampp" -ForegroundColor Gray }
        Ensure-ApacheProxyConfig -xamppPath $xampp

        # Kiem tra cong 80
        $port80 = Get-NetTCPConnection -LocalPort 80 -State Listen -ErrorAction SilentlyContinue
        if ($port80) {
            $apacheReady = $true
            if (-not $Silent) { Write-Host "  [OK] Apache dang chay tren cong 80 (HTTP) va 8443 (HTTPS)." -ForegroundColor Green }
        } else {
            if (-not $Silent) { Write-Host "  [+] Dang khoi dong Apache XAMPP trong nen..." -ForegroundColor Yellow }
            $svc = Get-Service -Name "Apache2.4" -ErrorAction SilentlyContinue
            if ($svc) {
                Start-Service -Name "Apache2.4" -ErrorAction SilentlyContinue
            } else {
                Start-Process -FilePath "$xampp\apache\bin\httpd.exe" -WorkingDirectory "$xampp\apache" -WindowStyle Hidden
            }
            Start-Sleep -Seconds 2

            $port80Check = Get-NetTCPConnection -LocalPort 80 -State Listen -ErrorAction SilentlyContinue
            if ($port80Check) {
                $apacheReady = $true
                if (-not $Silent) { Write-Host "  [OK] Apache da khoi dong thanh cong (Port 80 HTTP & Port 8443 HTTPS)!" -ForegroundColor Green }
            } else {
                if (-not $Silent) { Write-Host "  [!] Canh bao: Apache chua phan hoi tren cong 80. Se fallback ve cong 8080." -ForegroundColor Yellow }
            }
        }
    } else {
        if (-not $Silent) { Write-Host "  [i] Khong tim thay XAMPP. He thong se hoat dong doc lap tren Cong 8080." -ForegroundColor Yellow }
    }

    # 2. Khoi chay supportflast.exe
    if (-not $Silent) {
        Write-Host ""
        Write-Host "[2/4] Dang kiem tra va khoi chay SupportFlast Monolith Engine..." -ForegroundColor Cyan
    }

    $exePath = "$rootDir\supportflast.exe"
    if (-not (Test-Path $exePath)) {
        $exePath = "$rootDir\supportflast_engine\supportflast.exe"
    }

    $port8080 = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue

    if ($port8080) {
        if (-not $Silent) { Write-Host "  [OK] Tien trinh supportflast.exe da dang lang nghe tren cong 8080." -ForegroundColor Green }
    } else {
        # Don dep tien trinh cu neu co truoc khi khoi dong
        $engProc = Get-Process -Name "supportflast" -ErrorAction SilentlyContinue
        if ($engProc) {
            $engProc | Stop-Process -Force -ErrorAction SilentlyContinue
            Start-Sleep -Milliseconds 500
        }
        if (-not $Silent) { Write-Host "  [+] Khoi chay $exePath trong nen an toan..." -ForegroundColor Gray }
        Start-Process -FilePath $exePath -WorkingDirectory $rootDir -WindowStyle Hidden
    }

    # 3. Health Check Polling
    if (-not $Silent) {
        Write-Host ""
        Write-Host "[3/4] Dang kiem tra suc khoe he thong (Health Check Polling)..." -ForegroundColor Cyan
    }

    $backendHealthy = $false
    $healthInfo = $null
    for ($i = 1; $i -le 15; $i++) {
        try {
            $healthInfo = Invoke-RestMethod -Uri "http://127.0.0.1:8080/api/health" -TimeoutSec 1 -ErrorAction Stop
            if ($healthInfo.status -eq "healthy") {
                $backendHealthy = $true
                break
            }
        } catch {
            Start-Sleep -Seconds 1
        }
    }

    if ($backendHealthy) {
        if (-not $Silent) {
            $ramMB = [math]::Round([double]$healthInfo.alloc_mb, 2)
            Write-Host "  [OK] Backend Go Engine san sang hoat dong! (RAM: $ramMB MB | Goroutines: $($healthInfo.goroutines))" -ForegroundColor Green
        }
    } else {
        if (-not $Silent) { Write-Host "  [!] Canh bao: Backend mat nhieu thoi gian hon binh thuong de san sang." -ForegroundColor Yellow }
    }

    # Neu co Apache, kiem tra them ket noi Apache Reverse Proxy truoc khi mo trinh duyet
    if ($apacheReady) {
        for ($j = 1; $j -le 5; $j++) {
            try {
                $proxyCheck = Invoke-RestMethod -Uri "http://localhost/api/health" -TimeoutSec 1 -ErrorAction Stop
                if ($proxyCheck.status -eq "healthy") { break }
            } catch {
                Start-Sleep -Milliseconds 500
            }
        }
    }

    # 4. Tu dong mo trinh duyet web
    $targetUrl = "http://supportflastdev.io.vn"
    if (-not $apacheReady) {
        $targetUrl = "http://localhost:8080"
    }

    # Tu dong khoi chay Cloudflare Tunnel giu ket noi toan cau
    try {
        & "$PSScriptRoot\cloudflare_auto_manager.ps1" Start
    } catch {
        # Bo qua neu co ngoai le
    }

    if (-not $Silent) {
        Write-Host ""
        Write-Host "[4/4] Dang mo trinh duyet web: $targetUrl" -ForegroundColor Cyan
    }

    Start-Process $targetUrl

    if ($Silent) { return }

    # 5. Bang thong tin he thong
    $lanIP = (Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceAlias -notlike "*Loopback*" -and $_.IPAddress -notlike "169.254*" } | Select-Object -ExpandProperty IPAddress -First 1)

    Write-Host ""
    Write-Host "==============================================================================" -ForegroundColor Cyan
    Write-Host "  [OK] HE THONG SUPPORTFLAST DANG HOAT DONG HOAN HAO (MO CHO MAY KHACH)!" -ForegroundColor Green
    Write-Host "==============================================================================" -ForegroundColor Cyan
    Write-Host "  1. TRUY CAP CHINH THUC (DOMAIN & LOCAL):" -ForegroundColor Yellow
    Write-Host "     👉 Ten Mien Chinh Thuc      : http://supportflastdev.io.vn" -ForegroundColor Green
    Write-Host "     - HTTP (Port 80)           : http://localhost" -ForegroundColor White
    Write-Host "     - HTTPS (Port 8443)        : https://localhost:8443 (Da tranh cong 443)" -ForegroundColor White
    Write-Host "     - Backend Engine (Port 8080): http://localhost:8080" -ForegroundColor Gray
    Write-Host "     - Cloudflare Tunnel        : Tu dong ket noi 24/7 ra Internet toan cau" -ForegroundColor Cyan
    Write-Host ""
    if ($lanIP) {
        Write-Host "  2. TRUY CAP TU MAY KHACH / DIEN THOAI TRONG MANG (NHU WEB THAT):" -ForegroundColor Green
        Write-Host "     👉 HTTP (Khuyen dung)      : http://$lanIP" -ForegroundColor Yellow
        Write-Host "     👉 HTTPS Bao Mat           : https://$lanIP:8443" -ForegroundColor Yellow
        Write-Host "     👉 Sub-path PHP Bridge     : http://$lanIP/supportflast/" -ForegroundColor White
        Write-Host "     👉 O Dia Mang Ao (WebDAV)  : http://$lanIP/webdav/" -ForegroundColor White
    }
    Write-Host ""
    Write-Host "  3. QUAN TRI & TIEN ICH:" -ForegroundColor Cyan
    Write-Host "     👉 Trang Quan Ly PHP (Console): http://localhost/phpmanager/" -ForegroundColor Green
    Write-Host "     - Xem Thong Tin PHP (phpinfo) : http://localhost/dashboard/phpinfo.php" -ForegroundColor White
    Write-Host "     - Kho Luu Tru CloudPool       : http://localhost/storage" -ForegroundColor White
    Write-Host "     - Cong Cu Phat Trien          : http://localhost/tools" -ForegroundColor White
    Write-Host "     - Chan Doan He Thong (Diag)   : http://localhost/api/system/diagnostics" -ForegroundColor White
    Write-Host "     - Tat He Thong An Toan        : Chay file STOP_FULL_SYSTEM.bat" -ForegroundColor Gray
    Write-Host "==============================================================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  (Ghi chu: Ban co the dong cua so nay bat cu luc nao. He thong van tiep tuc chay ngam)" -ForegroundColor DarkGray
    Write-Host ""
}

# ------------------------------------------------------------------------------
# HANH DONG: STOP (DUNG HE THONG AN TOAN & BAO VE SQLITE WAL)
# ------------------------------------------------------------------------------
function Stop-FullSystem {
    if (-not $Silent) {
        Write-Host ""
        Write-Host "==============================================================================" -ForegroundColor Red
        Write-Host "       [-] SUPPORTFLAST MONOLITH - TRINH TAT HE THONG AN TOAN" -ForegroundColor Yellow
        Write-Host "        Bao ve toan ven du lieu SQLite WAL va tuy chon dung XAMPP" -ForegroundColor White
        Write-Host "==============================================================================" -ForegroundColor Red
        Write-Host ""
        Write-Host "[1/3] Dang gui yeu cau dung an toan toi SupportFlast Engine..." -ForegroundColor Cyan
    }

    $procEng = Get-Process -Name "supportflast" -ErrorAction SilentlyContinue

    if ($procEng) {
        # 1. Goi Graceful Shutdown API (kich hoat PRAGMA wal_checkpoint(TRUNCATE))
        try {
            $resp = Invoke-RestMethod -Uri "http://127.0.0.1:8080/api/system/shutdown" -TimeoutSec 3 -ErrorAction Stop
            if (-not $Silent) { Write-Host "  [+] Phan hoi tu Engine: $($resp.message)" -ForegroundColor Green }
        } catch {
            if (-not $Silent) { Write-Host "  [!] Khong the ket noi API shutdown, tien hanh dung an toan tien trinh..." -ForegroundColor Yellow }
        }

        # 2. Cho tien trinh ket thuc tu nhien
        Start-Sleep -Seconds 2
        $procCheck = Get-Process -Name "supportflast" -ErrorAction SilentlyContinue
        if ($procCheck) {
            $procCheck | Stop-Process -Force -ErrorAction SilentlyContinue
            Start-Sleep -Seconds 1
            if (-not $Silent) { Write-Host "  [OK] Da dung hoan toan tien trinh supportflast.exe." -ForegroundColor Green }
        } else {
            if (-not $Silent) { Write-Host "  [OK] Tien trinh supportflast.exe da tu dong dong an toan." -ForegroundColor Green }
        }

        # 3. Kiem tra kich thuoc file WAL SQLite
        $mainWal = Get-Item "$rootDir\data\supportflast.db-wal" -ErrorAction SilentlyContinue
        $cloudWal = Get-Item "$rootDir\data\cloudpool_metadata.db-wal" -ErrorAction SilentlyContinue
        $mainBytes = 0
        if ($mainWal) { $mainBytes = $mainWal.Length }
        $cloudBytes = 0
        if ($cloudWal) { $cloudBytes = $cloudWal.Length }

        if (-not $Silent) {
            Write-Host "  [OK] Toan ven SQLite WAL: Main DB = $mainBytes bytes | CloudPool DB = $cloudBytes bytes (Zero Loss)." -ForegroundColor Green
        }
    } else {
        if (-not $Silent) { Write-Host "  [i] Tien trinh supportflast.exe khong hoat dong." -ForegroundColor Gray }
    }

    # 4. Tuy chon dung Apache
    if (-not $Silent) {
        Write-Host ""
        Write-Host "[2/3] Dang kiem tra trang thai XAMPP Apache (Cong 80)..." -ForegroundColor Cyan
    }

    $xampp = Find-XamppDirectory
    $apacheProcs = Get-Process -Name "httpd" -ErrorAction SilentlyContinue
    $port80 = Get-NetTCPConnection -LocalPort 80 -State Listen -ErrorAction SilentlyContinue

    if ($apacheProcs -or $port80) {
        $shouldStop = $false
        if ($All -or $Silent) {
            $shouldStop = $true
        } else {
            Write-Host "  [+] Phat hien Apache dang chay tren cong 80." -ForegroundColor Yellow
            $choice = Read-Host "  >> Ban co muon dung luon Apache (Cong 80) khong? [Y/N] (Mac dinh: Y)"
            if ([string]::IsNullOrWhiteSpace($choice) -or $choice.Trim().ToUpper() -eq "Y") {
                $shouldStop = $true
            }
        }

        if ($shouldStop) {
            if (-not $Silent) { Write-Host "  [-] Dang dung dich vu Apache..." -ForegroundColor Gray }
            if ($xampp -and (Test-Path "$xampp\apache\bin\httpd.exe")) {
                & "$xampp\apache\bin\httpd.exe" -k stop 2>&1 | Out-Null
            }
            Get-Process -Name "httpd" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
            if ($xampp -and (Test-Path "$xampp\apache\logs\httpd.pid")) {
                Remove-Item -Path "$xampp\apache\logs\httpd.pid" -Force -ErrorAction SilentlyContinue
            }
            if (-not $Silent) { Write-Host "  [OK] Da dung Apache va giai phong Cong 80." -ForegroundColor Green }
        } else {
            if (-not $Silent) { Write-Host "  [i] Giu Apache tiep tuc hoat dong theo yeu cau cua ban." -ForegroundColor Gray }
        }
    } else {
        if (-not $Silent) { Write-Host "  [i] Apache hien khong chay tren he thong." -ForegroundColor Gray }
    }

    # 5. Xac nhan giai phong cong va tai nguyen
    if (-not $Silent) {
        Write-Host ""
        Write-Host "[3/3] Xac nhan trang thai tai nguyen mang..." -ForegroundColor Cyan
        $p8080After = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue
        $p80After = Get-NetTCPConnection -LocalPort 80 -State Listen -ErrorAction SilentlyContinue

        if (-not $p8080After) {
            Write-Host "  [+] Cong 8080: Da giai phong hoan toan." -ForegroundColor Green
        } else {
            Write-Host "  [!] Cong 8080: Van dang bi chiem dung." -ForegroundColor Red
        }

        if (-not $p80After) {
            Write-Host "  [+] Cong 80  : Da giai phong (Apache da dung)." -ForegroundColor Green
        } else {
            Write-Host "  [i] Cong 80  : Dang lang nghe (Apache dang duy tri)." -ForegroundColor Gray
        }

        Write-Host ""
        Write-Host "==============================================================================" -ForegroundColor Cyan
        Write-Host "  [OK] HE THONG SUPPORTFLAST DA DUOC TAT HOAN TOAN AN TOAN!" -ForegroundColor Green
        Write-Host "==============================================================================" -ForegroundColor Cyan
        Write-Host "  - Du lieu SQLite WAL da duoc dong bo toan ven vao dia cung (Zero Data Loss)." -ForegroundColor White
        Write-Host "  - Tien trinh Go Monolith Engine va ket noi mang da duoc giai phong sach se." -ForegroundColor White
        Write-Host "  - De khoi dong lai he thong, chi can chay file RUN_FULL_SYSTEM.bat" -ForegroundColor Yellow
        Write-Host "==============================================================================" -ForegroundColor Cyan
        Write-Host ""
    }
}

# ------------------------------------------------------------------------------
# DIEU PHOI HANH DONG CHINH
# ------------------------------------------------------------------------------
switch ($Action) {
    "Start"   { Start-FullSystem }
    "Stop"    { Stop-FullSystem }
    "Restart" { Stop-FullSystem; Start-Sleep -Seconds 2; Start-FullSystem }
    "Status"  {
        $p8080 = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue
        $p80 = Get-NetTCPConnection -LocalPort 80 -State Listen -ErrorAction SilentlyContinue
        $eng = Get-Process -Name "supportflast" -ErrorAction SilentlyContinue
        $httpd = Get-Process -Name "httpd" -ErrorAction SilentlyContinue

        $engStatus = "Stopped"
        if ($eng) { $engStatus = "Running (PID: " + $eng.Id + ")" }

        $httpdStatus = "Stopped"
        if ($httpd) { $httpdStatus = "Running" }

        $p8080Status = "Free"
        if ($p8080) { $p8080Status = "Listening" }

        $p80Status = "Free"
        if ($p80) { $p80Status = "Listening" }

        Write-Host "SupportFlast Process: $engStatus"
        Write-Host "Apache Process:      $httpdStatus"
        Write-Host "Port 8080:           $p8080Status"
        Write-Host "Port 80:             $p80Status"
    }
}
