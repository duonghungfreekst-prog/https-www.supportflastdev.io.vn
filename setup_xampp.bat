@echo off
chcp 65001 >nul
title SUPPORTFLAST.DEV - TRÌNH THIẾT LẬP TÍCH HỢP XAMPP TỰ ĐỘNG
color 0A

echo ==========================================================================
echo        🚀 SUPPORTFLAST MONOLITH - TRÌNH TÍCH HỢP XAMPP 1-CLICK
echo ==========================================================================
echo.

setlocal enabledelayedexpansion

:: 1. Tự động tìm kiếm thư mục cài đặt XAMPP
set "XAMPP_DIR="
for %%D in (D:\xampp C:\xampp E:\xampp F:\xampp "E:\ổ f\xampp") do (
    if exist "%%~D\apache\bin\httpd.exe" (
        set "XAMPP_DIR=%%~D"
        goto :found_xampp
    )
)

:manual_xampp
echo [CHÚ Ý] Không tìm thấy XAMPP tại các ổ đĩa mặc định (C, D, E, F).
set /p XAMPP_DIR="Nhập đường dẫn cài đặt XAMPP của bạn (ví dụ: C:\xampp): "
if not exist "%XAMPP_DIR%\apache\bin\httpd.exe" (
    echo [LỖI] Không tìm thấy file '%XAMPP_DIR%\apache\bin\httpd.exe'!
    pause
    exit /b 1
)

:found_xampp
echo [+] Đã phát hiện XAMPP tại: %XAMPP_DIR%
echo.

set "HTTPD_CONF=%XAMPP_DIR%\apache\conf\httpd.conf"
set "VHOSTS_CONF=%XAMPP_DIR%\apache\conf\extra\httpd-vhosts.conf"
set "APP_CONF=%XAMPP_DIR%\apache\conf\extra\httpd-supportflast.conf"

:: 2. Sao chép file cấu hình chuyên dụng & PHP Gateway Bridge
echo [+] Đang tích hợp cấu hình Apache Reverse Proxy vào XAMPP...
copy /Y "%~dp0infra\httpd-supportflast.conf" "%APP_CONF%" >nul
if %ERRORLEVEL% NEQ 0 (
    echo [LỖI] Không thể sao chép file cấu hình vào '%APP_CONF%'.
    pause
    exit /b 1
)

if not exist "%XAMPP_DIR%\htdocs\supportflast" mkdir "%XAMPP_DIR%\htdocs\supportflast"
copy /Y "%~dp0tools\xampp_htdocs_bridge\index.php" "%XAMPP_DIR%\htdocs\supportflast\index.php" >nul
echo [+] Đã cài đặt PHP Gateway Bridge tại: %XAMPP_DIR%\htdocs\supportflast\index.php

:: 3. Kích hoạt Include trong httpd.conf nếu chưa có
powershell -NoProfile -Command ^
    "$conf = Get-Content '%HTTPD_CONF%' -Raw;" ^
    "$conf = $conf -replace '#LoadModule proxy_http_module modules/mod_proxy_http.so', 'LoadModule proxy_http_module modules/mod_proxy_http.so';" ^
    "$conf = $conf -replace '#LoadModule proxy_wstunnel_module modules/mod_proxy_wstunnel.so', 'LoadModule proxy_wstunnel_module modules/mod_proxy_wstunnel.so';" ^
    "if ($conf -notmatch 'httpd-supportflast.conf') {" ^
    "    $conf += \"`nInclude conf/extra/httpd-supportflast.conf`n\";" ^
    "}" ^
    "Set-Content '%HTTPD_CONF%' -Value $conf -NoNewline;"

:: 3.1. Tự động khắc phục xung đột cổng 443 & cài đặt cấu hình SSL SupportFlast (Port 8443)
set "SSL_CONF=%XAMPP_DIR%\apache\conf\extra\httpd-ssl.conf"
if exist "%SSL_CONF%" (
    echo [+] Đang tích hợp cấu hình SSL Reverse Proxy SupportFlast (Port 8443)...
    copy /Y "%~dp0infra\httpd-ssl.conf" "%SSL_CONF%" >nul
    echo     -^> Cấu hình SSL Port 8443 đã được tích hợp thành công!
)

:: 3.2. Tự động mở tường lửa Windows Firewall cho máy khách trong mạng LAN truy cập
echo [+] Đang kiểm tra và cấu hình Windows Defender Firewall (Mở Port 80 & 8443)...
powershell -NoProfile -Command ^
    "$r80 = Get-NetFirewallRule -DisplayName 'SupportFlast XAMPP HTTP (Port 80)' -ErrorAction SilentlyContinue;" ^
    "if (-not $r80) { New-NetFirewallRule -DisplayName 'SupportFlast XAMPP HTTP (Port 80)' -Direction Inbound -LocalPort 80 -Protocol TCP -Action Allow -Profile Any | Out-Null };" ^
    "$r8443 = Get-NetFirewallRule -DisplayName 'SupportFlast XAMPP HTTPS (Port 8443)' -ErrorAction SilentlyContinue;" ^
    "if (-not $r8443) { New-NetFirewallRule -DisplayName 'SupportFlast XAMPP HTTPS (Port 8443)' -Direction Inbound -LocalPort 8443 -Protocol TCP -Action Allow -Profile Any | Out-Null };" ^
    "Write-Host '    -> Windows Firewall đã mở quyền kết nối cho máy khách qua Port 80 & 8443!' -ForegroundColor Green;"

:: 4. Kiểm tra cú pháp cấu hình Apache
echo [+] Đang kiểm tra cú pháp Apache...
"%XAMPP_DIR%\apache\bin\httpd.exe" -t
if %ERRORLEVEL% NEQ 0 (
    echo.
    echo [CẢNH BÁO] Kiểm tra cú pháp Apache phát hiện cảnh báo hoặc lỗi.
) else (
    echo.
    echo [THÀNH CÔNG] Cú pháp Apache XAMPP hoàn toàn chính xác (Syntax OK)!
)

:: 5. Hướng dẫn khởi chạy & Lấy IP máy khách
for /f "tokens=*" %%I in ('powershell -NoProfile -Command "(Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.InterfaceAlias -notlike '*Loopback*' -and $_.IPAddress -notlike '169.254*' } | Select-Object -ExpandProperty IPAddress -First 1)"') do set "LAN_IP=%%I"

echo.
echo ==========================================================================
echo                      🎉 HOÀN TẤT THIẾT LẬP XAMPP!
echo ==========================================================================
echo  [1] TRUY CẬP TẠI MÁY CHỦ (LOCAL):
echo      👉 http://localhost (Toàn bộ SupportFlast Web UI + Virtual Storage)
echo      👉 https://localhost:8443 (HTTPS bảo mật cao - Đã tránh cổng 443)
echo.
if defined LAN_IP (
    echo  [2] TRUY CẬP TỪ MÁY KHÁCH / ĐIỆN THOẠI TRONG MẠNG LAN (NHƯ WEB THẬT):
    echo      👉 http://!LAN_IP! (Truy cập trực tiếp không cần cài đặt gì thêm)
    echo      👉 https://!LAN_IP!:8443 (HTTPS máy khách)
    echo      👉 http://!LAN_IP!/webdav/ (Ổ đĩa mạng ảo CloudPool WebDAV)
    echo.
)
echo  [3] CÁCH KHỞI CHẠY HỆ THỐNG:
echo      - Cách 1: Nhấp đúp 'RUN_FULL_SYSTEM.bat' (Tự động bật Apache + Go Engine)
echo      - Cách 2: Mở XAMPP Control Panel và Start Apache
echo ==========================================================================
echo.
pause
