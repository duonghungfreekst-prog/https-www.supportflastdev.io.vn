@echo off
chcp 65001 >nul
title SUPPORTFLAST - THIẾT LẬP TỰ ĐỘNG CHẠY TÊN MIỀN CLOUDFLARE 24/7
color 0A

echo ==============================================================================
echo    🌐 TỰ ĐỘNG HÓA TÊN MIỀN SUPPORTFLASTDEV.IO.VN - CLOUDFLARE AUTO RUNNER
echo ==============================================================================
echo.
echo  Chọn chế độ bạn muốn thực hiện:
echo.
echo  [1] Kiểm tra trạng thái kết nối Cloudflare Tunnel hiện tại
echo  [2] Cài đặt Cloudflare Tunnel làm Windows Service (TỰ ĐỘNG CHẠY 24/7 VỚI WEB)
echo  [3] Chạy lệnh: cloudflared tunnel run supportflast-tunnel (Theo cloudflared_config.yml)
echo  [4] Kích hoạt Quick Tunnel dùng thử ngay lập tức (Không cần tạo Token)
echo  [5] Dừng toàn bộ Cloudflare Tunnel
echo  [6] Gỡ bỏ Windows Service Cloudflared
echo  [7] Thoát
echo.
set /p opt="Nhập lựa chọn của bạn (1-7): "

if "%opt%"=="1" (
    powershell -ExecutionPolicy Bypass -File "%~dp0tools\cloudflare_auto_manager.ps1" Status
    echo.
    pause
    goto :eof
)
if "%opt%"=="2" (
    echo.
    echo Vui lòng dán Cloudflare Tunnel Token của bạn vào đây:
    echo (Lấy tại: Cloudflare Zero Trust -^> Networks -^> Tunnels -^> Install connector)
    set /p token="Token: "
    if defined token (
        powershell -ExecutionPolicy Bypass -File "%~dp0tools\cloudflare_auto_manager.ps1" InstallService "%token%"
    ) else (
        echo [!] Bạn chưa nhập Token!
    )
    echo.
    pause
    goto :eof
)
if "%opt%"=="3" (
    echo.
    "%~dp0tools\cloudflared.exe" tunnel --config "%~dp0cloudflared_config.yml" run supportflast-tunnel
    goto :eof
)
if "%opt%"=="4" (
    echo.
    echo [+] Đang bật Quick Tunnel... Nhấn Ctrl+C để dừng khi không dùng nữa.
    powershell -ExecutionPolicy Bypass -File "%~dp0tools\cloudflare_auto_manager.ps1" QuickTunnel
    goto :eof
)
if "%opt%"=="5" (
    powershell -ExecutionPolicy Bypass -File "%~dp0tools\cloudflare_auto_manager.ps1" Stop
    echo.
    pause
    goto :eof
)
if "%opt%"=="6" (
    powershell -ExecutionPolicy Bypass -File "%~dp0tools\cloudflare_auto_manager.ps1" UninstallService
    echo.
    pause
    goto :eof
)
