@echo off
chcp 65001 >nul
echo =====================================================================
echo    BIÊN DỊCH 9ROUTER LOCAL PROXY (PURE GO - 0 CGO - 0 LIBSQL)
echo =====================================================================

set CGO_ENABLED=0

echo [1/2] Biên dịch cho Windows x64 (9router-client.exe)...
set GOOS=windows
set GOARCH=amd64
go build -ldflags "-s -w" -o 9router-client.exe main.go
if %ERRORLEVEL% EQU 0 (
    echo ✔ Biên dịch Windows thành công: 9router-client.exe
) else (
    echo ❌ Lỗi biên dịch Windows!
)

echo [2/2] Biên dịch cho Android Termux / Linux ARM64 (9router-client-arm64)...
set GOOS=linux
set GOARCH=arm64
go build -ldflags "-s -w" -o 9router-client-arm64 main.go
if %ERRORLEVEL% EQU 0 (
    echo ✔ Biên dịch Android Termux thành công: 9router-client-arm64
) else (
    echo ❌ Lỗi biên dịch Linux ARM64!
)

echo.
echo =====================================================================
echo Hoàn tất! Bạn có thể chạy 9router-client.exe ngay trên PC.
echo =====================================================================
