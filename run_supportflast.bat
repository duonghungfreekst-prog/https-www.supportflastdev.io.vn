@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

title SupportFlast.dev - Trình Khởi Động Máy Chủ An Toàn (Local & Cloud)
color 0B

set "ROOT_DIR=%~dp0"
set "ENGINE_EXE=%ROOT_DIR%supportflast.exe"
set "CLOUDFLARED_BIN=%ROOT_DIR%tools\cloudflared.exe"
set "TOKEN_FILE=%ROOT_DIR%tools\cloudflare_token.txt"

echo ==============================================================================
echo       SUPPORTFLAST.DEV - TRÌNH KHỞI ĐỘNG HỆ THỐNG AN TOÀN & MINH BẠCH
echo       Kiến trúc: Antigravity Polyglot (Go Engine + AI + Core Crypto)
echo ==============================================================================
echo.

:: 1. Kiểm tra tập tin thực thi Go Web Engine
if not exist "%ENGINE_EXE%" (
    echo [!] Không tìm thấy tập tin %ENGINE_EXE%
    echo [*] Vui lòng chạy 'go build -o supportflast.exe ./supportflast_engine' hoặc kiểm tra mã nguồn.
    pause
    exit /b 1
)

:: 2. Khởi chạy Go Web Engine trực tiếp (Minh bạch, hiển thị log, không chạy ẩn ngầm)
echo [*] [1/2] Đang khởi động Go Web Engine (Cổng 8080)...
netstat -ano | findstr /r ":8080[ ]" | findstr "LISTENING" >nul
if %errorLevel% equ 0 (
    echo     [OK] Cổng 8080 đang có tiến trình hoạt động.
) else (
    echo     [+] Khởi chạy SupportFlast Engine tại http://127.0.0.1:8080...
    start "SupportFlast Go Engine" /d "%ROOT_DIR%" "%ENGINE_EXE%"
    timeout /t 2 /nobreak >nul
)

:: 3. Kiểm tra cấu hình Cloudflare Tunnel (Tùy chọn cho người dùng muốn mở domain riêng)
echo [*] [2/2] Kiểm tra kết nối Cloudflare Zero Trust Tunnel...
set "CF_TOKEN="

:: Đọc token từ biến môi trường hoặc file cấu hình bí mật cục bộ
if defined CLOUDFLARE_TUNNEL_TOKEN (
    set "CF_TOKEN=%CLOUDFLARE_TUNNEL_TOKEN%"
) else if exist "%TOKEN_FILE%" (
    set /p CF_TOKEN=<"%TOKEN_FILE%"
)

if not "%CF_TOKEN%"=="" (
    if exist "%CLOUDFLARED_BIN%" (
        echo     [+] Đã phát hiện Cloudflare Tunnel Token hợp lệ.
        echo     [+] Đang khởi chạy Cloudflare Tunnel an toàn...
        start "Cloudflare Tunnel Client" /d "%ROOT_DIR%" "%CLOUDFLARED_BIN%" tunnel run --token %CF_TOKEN%
    ) else (
        echo     [!] Không tìm thấy công cụ %CLOUDFLARED_BIN% để chạy Tunnel.
    )
) else (
    echo     [INFO] Không có Cloudflare Token được cấu hình.
    echo            Máy chủ đang chạy ở chế độ Cục Bộ Nội Bộ (Local Mode):
    echo            - Truy cập Giao Diện: http://127.0.0.1:8080
    echo            - Cổng Định Tuyến AI: http://127.0.0.1:8080/router/
    echo            (Nếu muốn kết nối Internet qua domain riêng, hãy đặt token vào file 'tools\cloudflare_token.txt')
)

echo.
echo ==============================================================================
echo   [HOÀN TẤT] HỆ THỐNG ĐÃ SẴN SÀNG SỬ DỤNG
echo   - Địa chỉ máy chủ: http://127.0.0.1:8080
echo   - Dashboard AI:    http://127.0.0.1:8080/router/
echo ==============================================================================
echo.
echo [*] Đang mở trình duyệt vào giao diện hệ thống...
start "" "http://127.0.0.1:8080/router/"

echo [*] Nhấn phím bất kỳ để đóng cửa sổ trợ lý khởi động này (Máy chủ vẫn chạy độc lập)...
pause >nul
exit /b 0
