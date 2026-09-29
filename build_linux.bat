@echo off
chcp 65001 >nul
title SupportFlast - Cross-Compilation Linux Binaries (supportflastdev.io.vn)
cls

echo =======================================================================
echo     SUPPORTFLAST - BIEN DICH CROSS-COMPILATION SANG LINUX
echo     Tu dong tao Linux Static Binary doc lap cho VPS / cPanel
echo     Ten mien: supportflastdev.io.vn
echo =======================================================================
echo.

:: 1. Kiem tra trinh bien dich Go
where go >nul 2>nul
if %ERRORLEVEL% neq 0 (
    echo [LOI] Khong tim thay Go trong he thong!
    echo Vui long cai dat Go tu https://go.dev/dl/ va them vao PATH.
    echo.
    pause
    exit /b 1
)

for /f "tokens=*" %%i in ('go version') do set GO_VER=%%i
echo [INFO] Trinh bien dich Go: %GO_VER%
echo.

:: Dinh nghia duong dan
set ROOT_DIR=%~dp0
set ENGINE_DIR=%ROOT_DIR%supportflast_engine
set OUTPUT_AMD64=%ENGINE_DIR%\supportflast-linux-amd64
set OUTPUT_ARM64=%ENGINE_DIR%\supportflast-linux-arm64

cd /d "%ENGINE_DIR%"

:: 2. Bien dich Linux x86_64 (amd64)
echo -----------------------------------------------------------------------
echo [1/2] Dang bien dich Linux x86_64 (amd64)...
echo       GOOS=linux, GOARCH=amd64, CGO_ENABLED=0
echo       File dich: supportflast_engine\supportflast-linux-amd64
echo -----------------------------------------------------------------------

set CGO_ENABLED=0
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0" -trimpath -o "%OUTPUT_AMD64%" .

if %ERRORLEVEL% neq 0 (
    echo [LOI] Bien dich Linux amd64 that bai!
    cd /d "%ROOT_DIR%"
    pause
    exit /b 1
)
echo [THANH CONG] Da tao file: supportflast-linux-amd64

:: 3. Bien dich Linux ARM64 (arm64)
echo.
echo -----------------------------------------------------------------------
echo [2/2] Dang bien dich Linux ARM64 (arm64)...
echo       GOOS=linux, GOARCH=arm64, CGO_ENABLED=0
echo       File dich: supportflast_engine\supportflast-linux-arm64
echo -----------------------------------------------------------------------

set CGO_ENABLED=0
set GOOS=linux
set GOARCH=arm64
go build -ldflags="-s -w -extldflags '-static' -X main.version=1.0.0" -trimpath -o "%OUTPUT_ARM64%" .

if %ERRORLEVEL% neq 0 (
    echo [LOI] Bien dich Linux arm64 that bai!
    cd /d "%ROOT_DIR%"
    pause
    exit /b 1
)
echo [THANH CONG] Da tao file: supportflast-linux-arm64

cd /d "%ROOT_DIR%"

:: 4. Hien thi thong tin file da tao
echo.
echo =======================================================================
echo           KET QUA BIEN DICH LINUX STATIC BINARIES THANH CONG!
echo =======================================================================
echo.
echo Danh sach file nhi phan Linux da san sang de upload len VPS / cPanel:
echo.

if exist "%OUTPUT_AMD64%" (
    for %%F in ("%OUTPUT_AMD64%") do (
        echo   [x86_64 / amd64]: supportflast_engine\supportflast-linux-amd64
        echo   Kich thuoc: %%~zF bytes
    )
)

if exist "%OUTPUT_ARM64%" (
    for %%F in ("%OUTPUT_ARM64%") do (
        echo   [ARM64 / aarch64]: supportflast_engine\supportflast-linux-arm64
        echo   Kich thuoc: %%~zF bytes
    )
)

echo.
echo =======================================================================
echo                 HUONG DAN SU DUNG TREN VPS / CPANEL
echo =======================================================================
echo 1. Upload file binary phu hop len thu muc VPS / Server.
echo 2. Cap quyen thuc thi tren Linux:
echo       chmod +x supportflast-linux-amd64
echo 3. Khoi chay truc tiep ma KHONG CAN CAI DAT GO tren VPS:
echo       ./supportflast-linux-amd64
echo.
echo Luu y: Binary la Static 100%%, doc lap hoan toan (khong can CGO / glibc).
echo =======================================================================
echo.
pause
