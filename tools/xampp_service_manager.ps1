<#
.SYNOPSIS
    SupportFlast - XAMPP Apache Windows Service Manager
.DESCRIPTION
    Kịch bản PowerShell chuyên dụng quản trị Apache Windows Service cho SupportFlast:
    - Cài đặt Apache làm Windows Service tự khởi động cùng máy tính (httpd.exe -k install -n Apache2.4).
    - Cấu hình StartupType = Automatic.
    - Gỡ bỏ Windows Service sạch sẽ (httpd.exe -k uninstall -n Apache2.4).
    - Khởi động, dừng, khởi động lại, kiểm tra trạng thái và kiểm tra cú pháp cấu hình.
.PARAMETER Action
    Hành động cần thực thi: install | uninstall | start | stop | restart | status | test
.PARAMETER ServiceName
    Tên Windows Service (Mặc định: Apache2.4)
.PARAMETER HttpdPath
    Đường dẫn tới file thực thi httpd.exe (Mặc định: C:\xampp\apache\bin\httpd.exe)
#>

[CmdletBinding()]
param (
    [Parameter(Position = 0)]
    [ValidateSet("install", "uninstall", "remove", "start", "stop", "restart", "status", "test", "help")]
    [string]$Action = "",

    [string]$ServiceName = "Apache2.4",
    [string]$HttpdPath = "C:\xampp\apache\bin\httpd.exe"
)

# Thiết lập encoding UTF-8 cho console
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8

# Hàm kiểm tra quyền Administrator
function Test-IsAdmin {
    $currentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $currentPrincipal = New-Object Security.Principal.WindowsPrincipal($currentIdentity)
    return $currentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

# Hàm yêu cầu quyền Administrator tự động
function Assert-AdminPrivileges {
    if (-not (Test-IsAdmin)) {
        Write-Host ""
        Write-Host "[!] CẢNH BÁO: Thao tác với Windows Service yêu cầu quyền Quản trị viên (Administrator)!" -ForegroundColor Yellow
        Write-Host "[*] Đang tự động kích hoạt phiên bản PowerShell nâng cao quyền (UAC)..." -ForegroundColor Cyan
        
        $scriptPath = $PSCommandPath
        $argList = "-NoProfile -ExecutionPolicy Bypass -File `"$scriptPath`""
        if ($Action) {
            $argList += " -Action $Action"
        }
        
        try {
            Start-Process powershell -Verb RunAs -ArgumentList $argList
            Exit 0
        } catch {
            Write-Host "[X] Người dùng đã từ chối yêu cầu quyền Administrator hoặc có lỗi xảy ra: $_" -ForegroundColor Red
            Exit 1
        }
    }
}

# Hàm xác thực đường dẫn httpd.exe
function Assert-HttpdExists {
    if (-not (Test-Path $HttpdPath)) {
        # Tìm kiếm fallback trong các thư mục thông dụng
        $fallbackPaths = @(
            "C:\xampp\apache\bin\httpd.exe",
            "D:\xampp\apache\bin\httpd.exe",
            "E:\xampp\apache\bin\httpd.exe",
            "F:\xampp\apache\bin\httpd.exe"
        )
        $found = $false
        foreach ($path in $fallbackPaths) {
            if (Test-Path $path) {
                $script:HttpdPath = $path
                $found = $true
                break
            }
        }
        if (-not $found) {
            Write-Host "[X] LỖI: Không tìm thấy file Apache httpd.exe tại: $HttpdPath" -ForegroundColor Red
            Write-Host "    Vui lòng kiểm tra lại đường dẫn cài đặt XAMPP Apache." -ForegroundColor Yellow
            Exit 1
        }
    }
}

# Hàm kiểm tra cú pháp cấu hình Apache
function Invoke-ConfigTest {
    Assert-HttpdExists
    Write-Host "==============================================================" -ForegroundColor Cyan
    Write-Host " KIỂM TRA CÚ PHÁP CẤU HÌNH APACHE (httpd.exe -t)" -ForegroundColor Cyan
    Write-Host "==============================================================" -ForegroundColor Cyan
    Write-Host "[*] Đang chạy: $HttpdPath -t" -ForegroundColor Gray
    
    $result = & $HttpdPath -t 2>&1
    $exitCode = $LASTEXITCODE
    
    foreach ($line in $result) {
        if ($line -match "Syntax OK") {
            Write-Host "  [OK] $line" -ForegroundColor Green
        } elseif ($line -match "warn|warning") {
            Write-Host "  [WARN] $line" -ForegroundColor Yellow
        } else {
            Write-Host "  $line" -ForegroundColor White
        }
    }
    
    Write-Host ""
    Write-Host "[*] Phân tích VirtualHost (httpd.exe -S):" -ForegroundColor Gray
    $vhosts = & $HttpdPath -S 2>&1
    foreach ($vhostLine in $vhosts) {
        Write-Host "  $vhostLine" -ForegroundColor DarkGray
    }
    
    Write-Host "--------------------------------------------------------------" -ForegroundColor Cyan
    if ($exitCode -eq 0) {
        Write-Host "[✓] Cấu hình Apache hoàn toàn hợp lệ (Syntax OK)!" -ForegroundColor Green
        return $true
    } else {
        Write-Host "[X] Cấu hình Apache có lỗi! Cần khắc phục trước khi khởi chạy." -ForegroundColor Red
        return $false
    }
}

# Hàm cài đặt Windows Service
function Install-ApacheService {
    Assert-AdminPrivileges
    Assert-HttpdExists

    Write-Host "==============================================================" -ForegroundColor Cyan
    Write-Host " CÀI ĐẶT APACHE LÀM WINDOWS SERVICE TỰ KHỞI ĐỘNG" -ForegroundColor Cyan
    Write-Host "==============================================================" -ForegroundColor Cyan

    # Kiểm tra cú pháp trước khi cài đặt
    Write-Host "[1/5] Kiểm tra cú pháp cấu hình..." -ForegroundColor Cyan
    $testResult = & $HttpdPath -t 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[X] LỖI CÚ PHÁP: Không thể cài đặt service vì file cấu hình bị lỗi:" -ForegroundColor Red
        $testResult | ForEach-Object { Write-Host "    $_" -ForegroundColor Yellow }
        return
    }
    Write-Host "  [OK] Cú pháp hợp lệ (Syntax OK)." -ForegroundColor Green

    # Kiểm tra xem service đã tồn tại chưa
    Write-Host "[2/5] Kiểm tra dịch vụ '$ServiceName' trên hệ thống..." -ForegroundColor Cyan
    $existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($existing) {
        Write-Host "[!] Cảnh báo: Windows Service '$ServiceName' đã tồn tại sẵn!" -ForegroundColor Yellow
        Write-Host "    Trạng thái hiện tại: $($existing.Status) | Khởi động: $($existing.StartType)" -ForegroundColor Gray
        
        # Đảm bảo cấu hình tự khởi động Automatic
        Write-Host "[3/5] Đặt chế độ tự khởi động cùng Windows (StartupType = Automatic)..." -ForegroundColor Cyan
        Set-Service -Name $ServiceName -StartupType Automatic
        Write-Host "  [OK] Đã thiết lập StartupType = Automatic thành công." -ForegroundColor Green
    } else {
        # Đăng ký Windows Service qua httpd.exe -k install
        Write-Host "[3/5] Đang đăng ký Windows Service qua lệnh:" -ForegroundColor Cyan
        Write-Host "      & `"$HttpdPath`" -k install -n `"$ServiceName`"" -ForegroundColor Gray
        
        $installResult = & $HttpdPath -k install -n $ServiceName 2>&1
        $installExit = $LASTEXITCODE
        
        $installResult | ForEach-Object { Write-Host "    $_" -ForegroundColor White }
        
        # Thiết lập chế độ Automatic cho Service
        Start-Sleep -Seconds 1
        $newService = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
        if ($newService) {
            Set-Service -Name $ServiceName -StartupType Automatic
            Write-Host "  [OK] Đã đăng ký Windows Service và đặt StartupType = Automatic thành công!" -ForegroundColor Green
        } else {
            Write-Host "[X] LỖI: Không thể đăng ký dịch vụ '$ServiceName' (Mã lỗi: $installExit)!" -ForegroundColor Red
            return
        }
    }

    # Khởi động dịch vụ
    Write-Host "[4/5] Khởi động dịch vụ '$ServiceName'..." -ForegroundColor Cyan
    try {
        Start-Service -Name $ServiceName -ErrorAction Stop
        Write-Host "  [OK] Dịch vụ '$ServiceName' đã được khởi động thành công!" -ForegroundColor Green
    } catch {
        Write-Host "[!] Không thể khởi động dịch vụ qua Start-Service: $_" -ForegroundColor Yellow
        Write-Host "    Đang thử kích hoạt qua: & `"$HttpdPath`" -k start -n `"$ServiceName`"..." -ForegroundColor Gray
        & $HttpdPath -k start -n $ServiceName 2>&1 | ForEach-Object { Write-Host "    $_" -ForegroundColor White }
    }

    # Xác nhận trạng thái cuối cùng
    Write-Host "[5/5] Xác thực trạng thái dịch vụ..." -ForegroundColor Cyan
    Start-Sleep -Seconds 2
    Get-ApacheStatus
    Write-Host "==============================================================" -ForegroundColor Green
    Write-Host " [✓] HOÀN TẤT: Apache Service đã sẵn sàng tự khởi động cùng máy!" -ForegroundColor Green
    Write-Host "==============================================================" -ForegroundColor Green
}

# Hàm gỡ bỏ Windows Service
function Uninstall-ApacheService {
    Assert-AdminPrivileges
    Assert-HttpdExists

    Write-Host "==============================================================" -ForegroundColor Yellow
    Write-Host " GỠ BỎ APACHE WINDOWS SERVICE" -ForegroundColor Yellow
    Write-Host "==============================================================" -ForegroundColor Yellow

    $service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $service) {
        Write-Host "[!] Thông báo: Dịch vụ '$ServiceName' không tồn tại trên hệ thống." -ForegroundColor Gray
        return
    }

    # Nếu đang chạy, dừng dịch vụ trước
    if ($service.Status -eq "Running") {
        Write-Host "[*] Dịch vụ đang chạy, tiến hành dừng dịch vụ..." -ForegroundColor Cyan
        try {
            Stop-Service -Name $ServiceName -Force -ErrorAction Stop
            Write-Host "  [OK] Đã dừng dịch vụ." -ForegroundColor Green
        } catch {
            Write-Host "[!] Thử dừng qua lệnh: & `"$HttpdPath`" -k stop -n `"$ServiceName`"..." -ForegroundColor Gray
            & $HttpdPath -k stop -n $ServiceName 2>&1 | Out-Null
        }
        Start-Sleep -Seconds 2
    }

    # Gỡ bỏ service qua httpd.exe -k uninstall -n Apache2.4
    Write-Host "[*] Tiến hành gỡ bỏ Windows Service:" -ForegroundColor Cyan
    Write-Host "    & `"$HttpdPath`" -k uninstall -n `"$ServiceName`"" -ForegroundColor Gray
    
    $uninstallResult = & $HttpdPath -k uninstall -n $ServiceName 2>&1
    $uninstallResult | ForEach-Object { Write-Host "    $_" -ForegroundColor White }

    Start-Sleep -Seconds 1
    $verify = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $verify) {
        Write-Host "  [OK] Đã gỡ bỏ hoàn toàn Windows Service '$ServiceName' khỏi hệ thống!" -ForegroundColor Green
    } else {
        Write-Host "[!] Cảnh báo: Service vẫn hiển thị trong bảng điều khiển. Có thể cần khởi động lại máy để làm sạch." -ForegroundColor Yellow
    }
}

# Hàm khởi động dịch vụ
function Start-ApacheService {
    Assert-AdminPrivileges
    Assert-HttpdExists
    
    Write-Host "[*] Kiểm tra cú pháp Apache trước khi khởi động..." -ForegroundColor Cyan
    $testResult = & $HttpdPath -t 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[X] LỖI CÚ PHÁP: Không thể khởi động Apache:" -ForegroundColor Red
        $testResult | ForEach-Object { Write-Host "    $_" -ForegroundColor Yellow }
        return
    }

    $service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $service) {
        Write-Host "[X] LỖI: Dịch vụ '$ServiceName' chưa được cài đặt!" -ForegroundColor Red
        Write-Host "    Vui lòng chạy lệnh cài đặt trước (-Action install)." -ForegroundColor Yellow
        return
    }

    if ($service.Status -eq "Running") {
        Write-Host "[!] Dịch vụ '$ServiceName' hiện đã đang chạy." -ForegroundColor Yellow
        return
    }

    Write-Host "[*] Đang khởi động dịch vụ '$ServiceName'..." -ForegroundColor Cyan
    try {
        Start-Service -Name $ServiceName -ErrorAction Stop
        Write-Host "  [OK] Dịch vụ '$ServiceName' đã khởi động thành công!" -ForegroundColor Green
    } catch {
        Write-Host "[X] Không thể khởi động: $_" -ForegroundColor Red
        & $HttpdPath -k start -n $ServiceName 2>&1 | ForEach-Object { Write-Host "    $_" -ForegroundColor White }
    }
    Get-ApacheStatus
}

# Hàm dừng dịch vụ
function Stop-ApacheService {
    Assert-AdminPrivileges
    Assert-HttpdExists

    $service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $service) {
        Write-Host "[X] LỖI: Dịch vụ '$ServiceName' không tồn tại!" -ForegroundColor Red
        return
    }

    if ($service.Status -ne "Running") {
        Write-Host "[!] Dịch vụ '$ServiceName' hiện không chạy (Trạng thái: $($service.Status))." -ForegroundColor Gray
        return
    }

    Write-Host "[*] Đang dừng dịch vụ '$ServiceName'..." -ForegroundColor Cyan
    try {
        Stop-Service -Name $ServiceName -Force -ErrorAction Stop
        Write-Host "  [OK] Dịch vụ '$ServiceName' đã dừng thành công!" -ForegroundColor Green
    } catch {
        Write-Host "[X] Lỗi khi dừng dịch vụ: $_" -ForegroundColor Red
        & $HttpdPath -k stop -n $ServiceName 2>&1 | ForEach-Object { Write-Host "    $_" -ForegroundColor White }
    }
}

# Hàm khởi động lại dịch vụ
function Restart-ApacheService {
    Assert-AdminPrivileges
    Assert-HttpdExists

    Write-Host "[*] Kiểm tra cú pháp trước khi khởi động lại..." -ForegroundColor Cyan
    $testResult = & $HttpdPath -t 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[X] LỖI CÚ PHÁP: Huỷ khởi động lại do file cấu hình có lỗi:" -ForegroundColor Red
        $testResult | ForEach-Object { Write-Host "    $_" -ForegroundColor Yellow }
        return
    }

    Write-Host "[*] Đang khởi động lại dịch vụ '$ServiceName'..." -ForegroundColor Cyan
    try {
        Restart-Service -Name $ServiceName -Force -ErrorAction Stop
        Write-Host "  [OK] Khởi động lại dịch vụ '$ServiceName' thành công!" -ForegroundColor Green
    } catch {
        Write-Host "[!] Đang thử khởi động lại qua Apache binary..." -ForegroundColor Yellow
        & $HttpdPath -k restart -n $ServiceName 2>&1 | ForEach-Object { Write-Host "    $_" -ForegroundColor White }
    }
    Get-ApacheStatus
}

# Hàm kiểm tra trạng thái chi tiết (Status)
function Get-ApacheStatus {
    Write-Host "==============================================================" -ForegroundColor Cyan
    Write-Host " TRẠNG THÁI APACHE VÀ DỊCH VỤ HỆ THỐNG" -ForegroundColor Cyan
    Write-Host "==============================================================" -ForegroundColor Cyan

    # 1. Trạng thái Windows Service
    $service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($service) {
        $statusColor = if ($service.Status -eq "Running") { "Green" } else { "Red" }
        Write-Host "  • Windows Service    : " -NoNewline
        Write-Host "$($service.Name) ($($service.DisplayName))" -ForegroundColor White
        Write-Host "  • Trạng thái Service : " -NoNewline
        Write-Host "$($service.Status)" -ForegroundColor $statusColor
        Write-Host "  • Kiểu khởi động     : " -NoNewline
        Write-Host "$($service.StartType)" -ForegroundColor White
    } else {
        Write-Host "  • Windows Service    : " -NoNewline
        Write-Host "CHƯA CÀI ĐẶT" -ForegroundColor Yellow
    }

    # 2. Tiến trình httpd.exe đang chạy
    $processes = Get-Process -Name "httpd" -ErrorAction SilentlyContinue
    if ($processes) {
        Write-Host "  • Tiến trình httpd   : " -NoNewline
        Write-Host "$($processes.Count) process đang chạy" -ForegroundColor Green
        foreach ($proc in $processes) {
            $memMB = [math]::Round($proc.WorkingSet64 / 1MB, 2)
            Write-Host "    - PID: $($proc.Id) | RAM: ${memMB} MB" -ForegroundColor Gray
        }
    } else {
        Write-Host "  • Tiến trình httpd   : " -NoNewline
        Write-Host "KHÔNG CÓ TIẾN TRÌNH NÀO" -ForegroundColor Red
    }

    # 3. Trạng thái lắng nghe các Cổng (Port 80 HTTP, Port 8443 HTTPS - Hoàn toàn tránh Port 443)
    Write-Host "  • Cổng lắng nghe (Ports - Đã tránh cổng 443):" -ForegroundColor White
    $ports = @(80, 8443)
    foreach ($p in $ports) {
        $conns = Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue
        if ($conns) {
            $pids = ($conns | Select-Object -ExpandProperty OwningProcess -Unique) -join ", "
            Write-Host "    - Port $p : " -NoNewline
            Write-Host "LISTEN (PID: $pids)" -ForegroundColor Green
        } else {
            Write-Host "    - Port $p : " -NoNewline
            Write-Host "Chưa có tiến trình lắng nghe" -ForegroundColor Gray
        }
    }

    # 4. Kiểm tra Backend Go Monolith Engine (Port 8080)
    $engineConn = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue
    Write-Host "  • SupportFlast Engine (8080): " -NoNewline
    if ($engineConn) {
        $enginePid = ($engineConn | Select-Object -ExpandProperty OwningProcess -Unique) -join ", "
        Write-Host "ONLINE (PID: $enginePid)" -ForegroundColor Green
    } else {
        Write-Host "OFFLINE (Chưa chạy trên port 8080)" -ForegroundColor Yellow
    }

    Write-Host "==============================================================" -ForegroundColor Cyan
}

# Menu tương tác dòng lệnh
function Show-InteractiveMenu {
    Clear-Host
    while ($true) {
        Write-Host ""
        Write-Host "==============================================================" -ForegroundColor Cyan
        Write-Host "    SUPPORTFLAST - XAMPP APACHE SERVICE SPECIALIST" -ForegroundColor White
        Write-Host "==============================================================" -ForegroundColor Cyan
        Write-Host " 1. Cài đặt Apache Windows Service (Tự khởi động cùng Windows)" -ForegroundColor Yellow
        Write-Host " 2. Gỡ bỏ Apache Windows Service" -ForegroundColor Yellow
        Write-Host " 3. Khởi động dịch vụ Apache (Start)" -ForegroundColor White
        Write-Host " 4. Dừng dịch vụ Apache (Stop)" -ForegroundColor White
        Write-Host " 5. Khởi động lại dịch vụ Apache (Restart)" -ForegroundColor White
        Write-Host " 6. Xem trạng thái dịch vụ, RAM, PID & Ports (Status)" -ForegroundColor Cyan
        Write-Host " 7. Kiểm tra cú pháp cấu hình (httpd.exe -t & -S)" -ForegroundColor Green
        Write-Host " 8. Thoát" -ForegroundColor Red
        Write-Host "==============================================================" -ForegroundColor Cyan
        Write-Host ""

        $choice = Read-Host "Nhập lựa chọn của bạn [1-8]"
        switch ($choice) {
            "1" { Install-ApacheService }
            "2" { Uninstall-ApacheService }
            "3" { Start-ApacheService }
            "4" { Stop-ApacheService }
            "5" { Restart-ApacheService }
            "6" { Get-ApacheStatus }
            "7" { Invoke-ConfigTest }
            "8" { Write-Host "[*] Tạm biệt!" -ForegroundColor Green; Exit 0 }
            default { Write-Host "[!] Lựa chọn không hợp lệ, vui lòng chọn lại [1-8]." -ForegroundColor Yellow }
        }
        Write-Host ""
        Write-Host "Nhấn Enter để tiếp tục..." -NoNewline
        [void][Console]::ReadLine()
    }
}

# ------------------------------------------------------------------------------
# ENTRY POINT
# ------------------------------------------------------------------------------
Assert-HttpdExists

switch ($Action.ToLower()) {
    "install"   { Install-ApacheService }
    "uninstall" { Uninstall-ApacheService }
    "remove"    { Uninstall-ApacheService }
    "start"     { Start-ApacheService }
    "stop"      { Stop-ApacheService }
    "restart"   { Restart-ApacheService }
    "status"    { Get-ApacheStatus }
    "test"      { [void](Invoke-ConfigTest) }
    "help"      {
        Get-Help $PSCommandPath -Detailed
    }
    default {
        Show-InteractiveMenu
    }
}
