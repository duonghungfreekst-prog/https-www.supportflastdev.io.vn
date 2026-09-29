@echo off
chcp 65001 >nul
title SUPPORTFLAST.DEV - UNIVERSAL RELEASE PACKAGER

echo ==========================================================================
echo        🚀 SUPPORTFLAST.DEV - TRÌNH ĐÓNG GÓI PHÂN PHỐI TỰ ĐỘNG
echo ==========================================================================
echo.

:: Kiểm tra Python đã được cài đặt chưa
where python >nul 2>nul
if %ERRORLEVEL% NEQ 0 (
    echo [LỖI] Không tìm thấy Python trong hệ thống!
    echo Vui lòng cài đặt Python 3.8+ và đảm bảo đã tích chọn 'Add Python to PATH'.
    echo.
    pause
    exit /b 1
)

:: Chạy kịch bản đóng gói Python
python "%~dp0package_release.py"

if %ERRORLEVEL% EQU 0 (
    echo.
    echo [THÀNH CÔNG] Đã tạo gói phát hành 'supportflast-hosting-package.zip'.
) else (
    echo.
    echo [LỖI] Quá trình đóng gói gặp sự cố. Mã lỗi: %ERRORLEVEL%
)

echo.
pause
