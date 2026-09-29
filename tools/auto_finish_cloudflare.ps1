# ==============================================================================
# SUPPORTFLAST.DEV - AUTO FINISH CLOUDFLARE SETUP
# Tu dong tao Tunnel, tro DNS va cai Windows Service ngay khi co cert.pem
# ==============================================================================

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8

$rootDir = "f:\supportflast.dev"
$cloudflared = "$rootDir\tools\cloudflared.exe"
$certPath = "$env:USERPROFILE\.cloudflared\cert.pem"
$configFile = "$rootDir\cloudflared_config.yml"

Write-Host "==============================================================================" -ForegroundColor Cyan
Write-Host "       SUPPORTFLAST - TỰ ĐỘNG THIẾT LẬP TOÀN BỘ CLOUDFLARE TUNNEL            " -ForegroundColor Yellow
Write-Host "==============================================================================" -ForegroundColor Cyan

if (-not (Test-Path $certPath)) {
    Write-Host "[!] Chưa phát hiện chứng chỉ cert.pem." -ForegroundColor Yellow
    exit 1
}

Write-Host "[+] Bước 1/4: Đã có cert.pem. Đang khởi tạo Tunnel 'supportflast-tunnel'..." -ForegroundColor Cyan
$createOut = & $cloudflared tunnel create supportflast-tunnel 2>&1
Write-Host $createOut

# Đồng bộ file credentials JSON
$jsonFile = Get-ChildItem "$env:USERPROFILE\.cloudflared\*.json" | Where-Object { $_.Name -ne "supportflast-tunnel.json" } | Sort-Object LastWriteTime -Descending | Select-Object -First 1
if ($jsonFile) {
    Copy-Item -Path $jsonFile.FullName -Destination "$env:USERPROFILE\.cloudflared\supportflast-tunnel.json" -Force
    Write-Host "[+] Bước 2/4: Đã đồng bộ file chứng thực: $($jsonFile.Name)" -ForegroundColor Green
}

Write-Host "[+] Bước 3/4: Đang tự động trỏ DNS tên miền trên Cloudflare..." -ForegroundColor Cyan
& $cloudflared tunnel route dns -f supportflast-tunnel supportflastdev.io.vn 2>&1 | Write-Host
& $cloudflared tunnel route dns -f supportflast-tunnel www.supportflastdev.io.vn 2>&1 | Write-Host

Write-Host "[+] Bước 4/4: Đang cấu hình và khởi chạy Cloudflare Tunnel 24/7..." -ForegroundColor Cyan
Copy-Item -Path $configFile -Destination "$env:USERPROFILE\.cloudflared\config.yml" -Force

# Đồng bộ sang SystemProfile để Windows Service có thể đọc được
$sysDir = "C:\Windows\System32\config\systemprofile\.cloudflared"
try {
    if (-not (Test-Path $sysDir)) { New-Item -ItemType Directory -Path $sysDir -Force | Out-Null }
    Copy-Item -Path "$env:USERPROFILE\.cloudflared\*" -Destination $sysDir -Force -Recurse -ErrorAction SilentlyContinue
} catch {}

# Cài đặt Windows Service nếu chạy quyền Admin
& $cloudflared service install 2>&1 | Out-Null
Set-Service -Name "Cloudflared" -StartupType Automatic -ErrorAction SilentlyContinue
Start-Service -Name "Cloudflared" -ErrorAction SilentlyContinue

$svc = Get-Service -Name "Cloudflared" -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -eq "Running") {
    Write-Host "==============================================================================" -ForegroundColor Green
    Write-Host "  🎉 Windows Service 'Cloudflared' đang chạy tự động 24/7!" -ForegroundColor Green
    Write-Host "==============================================================================" -ForegroundColor Green
} else {
    # Nếu Service chưa chạy được, khởi động tiến trình ngầm vĩnh viễn
    $runningProc = Get-Process -Name "cloudflared" -ErrorAction SilentlyContinue
    if (-not $runningProc) {
        Start-Process -FilePath $cloudflared -ArgumentList "tunnel --config `"$configFile`" run supportflast-tunnel" -WindowStyle Hidden
        Start-Sleep -Seconds 2
    }
    Write-Host "[OK] Đã kích hoạt tiến trình Cloudflare Tunnel chạy ngầm thành công!" -ForegroundColor Green
}

Write-Host "==============================================================================" -ForegroundColor Green
Write-Host "  🌐 TÊN MIỀN SUPPORTFLASTDEV.IO.VN ĐÃ ĐƯỢC KẾT NỐI VỚI CLOUDFLARE THÀNH CÔNG! " -ForegroundColor Green
Write-Host "==============================================================================" -ForegroundColor Green
