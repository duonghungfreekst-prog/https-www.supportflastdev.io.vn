# ==============================================================================
# SUPPORTFLAST.DEV - TỰ ĐỘNG ĐỊNH TUYẾN ANTIGRAVITY IDE ĐẾN WEB GATEWAY
# ==============================================================================
param(
    [string]$ApiKey = ""
)

[System.Console]::OutputEncoding = [System.Text.Encoding]::UTF8
[System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12 -bor [System.Net.SecurityProtocolType]::Tls13

$WebGateway = "https://www.supportflastdev.io.vn/v1"
$DefaultKey = "sk-supportflastdev.io.vn-master-live"
$ConfigFiles = @((Join-Path $HOME ".gemini\config.json"), (Join-Path $HOME ".gemini\config\config.json"))

Write-Host ""
Write-Host "==============================================================================" -ForegroundColor Cyan
Write-Host "      SUPPORTFLAST.DEV - TỰ ĐỘNG ĐỊNH TUYẾN ANTIGRAVITY ĐẾN WEB GATEWAY" -ForegroundColor White
Write-Host "==============================================================================" -ForegroundColor Cyan
Write-Host "  [*] Máy Chủ Web Gateway: $WebGateway" -ForegroundColor Gray
Write-Host "  [*] Trạng thái:           Cloud Online 24/24 (Render + TiDB Cloud)" -ForegroundColor Gray
Write-Host "  [*] Quota & Token:        Tự động trừ trực tiếp trên Web supportflastdev.io.vn" -ForegroundColor Gray
Write-Host "==============================================================================" -ForegroundColor Cyan
Write-Host ""

# Xử lý lệnh đặc biệt: --reset
if ($ApiKey -eq "--reset" -or $ApiKey -eq "reset") {
    foreach ($p in $ConfigFiles) {
        if (Test-Path $p) {
            $c = Get-Content $p -Raw | ConvertFrom-Json
            $c.PSObject.Properties.Remove('apiEndpoint')
            $c.PSObject.Properties.Remove('apiKey')
            $c | ConvertTo-Json -Depth 20 | Set-Content $p -Encoding UTF8
        }
    }
    try {
        $hb = @{ event='disconnected'; client='Antigravity IDE'; hostname=$env:COMPUTERNAME } | ConvertTo-Json -Compress
        Invoke-RestMethod -Uri "https://www.supportflastdev.io.vn/api/v1/client/heartbeat" -Method Post -Body ([System.Text.Encoding]::UTF8.GetBytes($hb)) -ContentType 'application/json' -TimeoutSec 5 | Out-Null
    } catch {}
    Write-Host "  [OK] Đã gỡ bỏ cấu hình Web Gateway, khôi phục Antigravity về mặc định Google." -ForegroundColor Yellow
    Write-Host ""
    exit 0
}

# Nếu không có tham số, hỏi người dùng hoặc dùng Master Key
if ([string]::IsNullOrWhiteSpace($ApiKey)) {
    Write-Host "  [?] Nhập API Key bạn vừa tạo trên Web (hoặc nhấn ENTER để dùng Master Key):" -ForegroundColor Yellow
    $inputKey = Read-Host "  > "
    if (![string]::IsNullOrWhiteSpace($inputKey)) {
        $ApiKey = $inputKey.Trim()
    } else {
        $ApiKey = $DefaultKey
    }
}

$ApiKey = $ApiKey.Trim('"', "'", " ")

Write-Host "  [*] Đang cấu hình với API Key: $ApiKey" -ForegroundColor Cyan
Write-Host ""

# 1. Ghi đè vào config.json của Antigravity IDE (cả 2 đường dẫn gốc và thư mục con)
try {
    foreach ($p in $ConfigFiles) {
        $configDir = Split-Path $p
        if (!(Test-Path $configDir)) {
            New-Item -ItemType Directory -Path $configDir -Force | Out-Null
        }
        $c = if (Test-Path $p) { Get-Content $p -Raw | ConvertFrom-Json } else { [PSCustomObject]@{} }
        $c | Add-Member -MemberType NoteProperty -Name "apiEndpoint" -Value $WebGateway -Force
        $c | Add-Member -MemberType NoteProperty -Name "apiKey" -Value $ApiKey -Force
        $c | ConvertTo-Json -Depth 20 | Set-Content $p -Encoding UTF8
    }
    Write-Host "  [OK] Đã cấu hình Antigravity IDE trỏ về Web Gateway thành công!" -ForegroundColor Green
} catch {
    Write-Host "  [X] LỖI ghi cấu hình config.json: $_" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "==============================================================================" -ForegroundColor Green
Write-Host "  HOÀN TẤT: Antigravity IDE đã được kết nối hoàn chỉnh tới Web Gateway!" -ForegroundColor Green
Write-Host "  Bây giờ bạn chỉ cần mở Antigravity IDE và chat/code bình thường." -ForegroundColor White
Write-Host "  Mọi hoạt động sẽ tự động trừ Quota trực tiếp trên web supportflastdev.io.vn." -ForegroundColor White
Write-Host "==============================================================================" -ForegroundColor Green
Write-Host ""
