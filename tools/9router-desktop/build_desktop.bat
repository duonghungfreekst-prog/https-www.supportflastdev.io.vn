@echo off
chcp 65001 >nul
echo =====================================================================
echo    BIÊN DỊCH 9ROUTER DESKTOP APP (WINDOWS SYSTEM TRAY + 0 CGO)
echo =====================================================================

set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64

echo [1/2] Biên dịch bản Desktop Chạy Ẩn (Không hiện màn hình đen CMD)...
go build -ldflags "-s -w -H=windowsgui" -o 9router-desktop.exe .
if %ERRORLEVEL% EQU 0 (
    echo ✔ Biên dịch thành công: 9router-desktop.exe (Chạy nền System Tray)
) else (
    echo ❌ Lỗi biên dịch bản Windows GUI!
)

echo [2/2] Biên dịch bản Debug Console (Hiện log màn hình)...
go build -ldflags "-s -w" -o 9router-desktop-debug.exe .
if %ERRORLEVEL% EQU 0 (
    echo ✔ Biên dịch thành công: 9router-desktop-debug.exe
) else (
    echo ❌ Lỗi biên dịch bản Debug!
)

echo.
echo =====================================================================
echo Hoàn tất! Bạn có thể nhấp đúp vào 9router-desktop.exe để chạy.
echo App sẽ tự mở Bảng Điều Khiển và nằm gọn ở Khay Hệ Thống (System Tray).
echo =====================================================================
