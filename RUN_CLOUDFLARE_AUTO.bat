@echo off
chcp 65001 >nul
title SUPPORTFLAST - CLOUDFLARE 1-CLICK AUTO CONNECT
color 0A

echo ==============================================================================
echo    🌐 SUPPORTFLAST - TỰ ĐỘNG KẾT NỐI TÊN MIỀN CLOUDFLARE
echo    Tên miền: supportflastdev.io.vn
echo ==============================================================================
echo.

set "CERT_FILE=%USERPROFILE%\.cloudflared\cert.pem"

if not exist "%CERT_FILE%" (
    echo [+] Đang mở trang ủy quyền Cloudflare trên trình duyệt của bạn...
    echo [*] Vui lòng bấm chọn domain 'supportflastdev.io.vn' và bấm nút 'Authorize'!
    echo.
    "%~dp0tools\cloudflared.exe" tunnel login
)

if exist "%CERT_FILE%" (
    echo.
    echo [+] Đã phát hiện chứng chỉ xác thực cert.pem thành công!
    echo [+] Đang tự động hoàn tất toàn bộ cấu hình còn lại...
    echo.
    powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0tools\auto_finish_cloudflare.ps1"
) else (
    echo.
    echo [!] Chưa nhận được ủy quyền từ Cloudflare. Vui lòng mở lại file này để thử lại.
)

echo.
echo Nhấn phím bất kỳ để đóng cửa sổ này...
pause >nul
