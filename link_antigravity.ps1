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
$ConfigPath = Join-Path $HOME ".gemini\config\config.json"

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
    if (Test-Path $ConfigPath) {
        $c = Get-Content $ConfigPath -Raw | ConvertFrom-Json
        $c.PSObject.Properties.Remove('apiEndpoint')
        $c.PSObject.Properties.Remove('apiKey')
        $c | ConvertTo-Json -Depth 20 | Set-Content $ConfigPath -Encoding UTF8
        Write-Host "  [OK] Đã gỡ bỏ cấu hình Web Gateway, khôi phục Antigravity về mặc định Google." -ForegroundColor Yellow
    }
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

# 1. Ghi đè vào config.json của Antigravity IDE
try {
    $configDir = Split-Path $ConfigPath
    if (!(Test-Path $configDir)) {
        New-Item -ItemType Directory -Path $configDir -Force | Out-Null
    }

    if (Test-Path $ConfigPath) {
        $c = Get-Content $ConfigPath -Raw | ConvertFrom-Json
    } else {
        $c = [PSCustomObject]@{}
    }

    $c | Add-Member -MemberType NoteProperty -Name "apiEndpoint" -Value $WebGateway -Force
    $c | Add-Member -MemberType NoteProperty -Name "apiKey" -Value $ApiKey -Force

    $c | ConvertTo-Json -Depth 20 | Set-Content $ConfigPath -Encoding UTF8
    Write-Host "  [OK] Đã ghi đè apiEndpoint và apiKey vào config.json của Antigravity IDE!" -ForegroundColor Green
} catch {
    Write-Host "  [X] LỖI ghi cấu hình config.json: $_" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "==============================================================================" -ForegroundColor Cyan
Write-Host "  [*] ĐANG ĐO KIỂM KẾT NỐI VÀ LẤY TOKEN THỰC TẾ QUA WEB GATEWAY..." -ForegroundColor White
Write-Host "==============================================================================" -ForegroundColor Cyan

# 2. Đo kiểm tra danh sách Model chính
try {
    Write-Host "  [1/3] Kiểm tra danh mục Model chính..." -ForegroundColor Cyan -NoNewline
    $modelsRes = Invoke-RestMethod -Uri "$WebGateway/models" -Headers @{ "Authorization" = "Bearer $ApiKey" } -TimeoutSec 10 -Method Get
    $count = $modelsRes.data.Count
    $modelList = ($modelsRes.data | ForEach-Object { $_.id }) -join ", "
    Write-Host " OK ($count models: $modelList)" -ForegroundColor Green
} catch {
    Write-Host " CẢNH BÁO: $($_.Exception.Message)" -ForegroundColor Yellow
}

# 3. Tra cứu Quota trên Web
try {
    Write-Host "  [2/3] Tra cứu hạn mức Quota của Key trên Web..." -ForegroundColor Cyan -NoNewline
    $subRes = Invoke-RestMethod -Uri "$WebGateway/dashboard/billing/subscription" -Headers @{ "Authorization" = "Bearer $ApiKey" } -TimeoutSec 10 -Method Get
    $planName = $subRes.plan.title
    $usedReq = $subRes.plan.used_requests
    $limitReq = $subRes.plan.quota_limit
    Write-Host " OK ($planName | Đã dùng: $usedReq / $limitReq requests)" -ForegroundColor Green
} catch {
    Write-Host " BỎ QUA: $($_.Exception.Message)" -ForegroundColor Gray
}

# 4. Gửi test prompt lấy Token thật và kiểm tra trừ Quota
try {
    Write-Host "  [3/3] Gửi test prompt và lấy Token thật từ Web Gateway..." -ForegroundColor Cyan -NoNewline
    $body = @{
        model = "gemini-3.8-flash-high"
        messages = @(
            @{ role = "user"; content = "Phản hồi đúng 1 từ: SẴN SÀNG" }
        )
        stream = $false
    } | ConvertTo-Json -Compress

    $chatRes = Invoke-RestMethod -Uri "$WebGateway/chat/completions" -Headers @{
        "Authorization" = "Bearer $ApiKey"
        "Content-Type" = "application/json"
    } -Method Post -Body ([System.Text.Encoding]::UTF8.GetBytes($body)) -TimeoutSec 25

    $reply = $chatRes.choices[0].message.content.Trim()
    $totalTok = $chatRes.usage.total_tokens
    Write-Host " THÀNH CÔNG!" -ForegroundColor Green
    Write-Host "        > Phản hồi từ AI: $reply" -ForegroundColor Yellow
    Write-Host "        > Tokens tiêu thụ: $totalTok tokens (Đã tự động trừ Quota trên Web)" -ForegroundColor Yellow
} catch {
    Write-Host " THẤT BẠI: $($_.Exception.Message)" -ForegroundColor Red
}

Write-Host ""
Write-Host "==============================================================================" -ForegroundColor Green
Write-Host "  HOÀN TẤT: Antigravity IDE đã được kết nối hoàn chỉnh tới Web Gateway!" -ForegroundColor Green
Write-Host "  Bây giờ bạn chỉ cần mở Antigravity IDE và chat/code bình thường." -ForegroundColor White
Write-Host "  Mọi hoạt động sẽ tự động trừ Quota trực tiếp trên web supportflastdev.io.vn." -ForegroundColor White
Write-Host "==============================================================================" -ForegroundColor Green
Write-Host ""
