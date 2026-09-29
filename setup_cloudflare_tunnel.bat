@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

title SUPPORTFLAST - QUAN LY VA CAI DAT CLOUDFLARE TUNNEL 24/7
color 0A

:: 1. Tu dong kiem tra va nang quyen Administrator (Self-Elevation)
net session >nul 2>&1
if %errorLevel% neq 0 (
    echo [!] Dang kich hoat quyen Administrator...
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process cmd -ArgumentList '/c \"\"%~f0\" %*\"' -Verb RunAs"
    exit /b
)

set "ROOT_DIR=%~dp0"
set "CLOUDFLARED_BIN=%ROOT_DIR%tools\cloudflared.exe"
set "TOKEN_FILE=%ROOT_DIR%tools\cloudflare_token.txt"

:: Kiem tra file thuc thi cloudflared.exe
if not exist "%CLOUDFLARED_BIN%" (
    echo [LOI] Khong tim thay cong cu cloudflared.exe tai:
    echo       %CLOUDFLARED_BIN%
    pause
    exit /b 1
)

:: Xu ly tham so dong lenh truc tiep (neu co)
if /i "%~1"=="install" (
    set "CF_TOKEN=%~2"
    goto :DoInstall
)
if /i "%~1"=="uninstall" goto :DoUninstall
if /i "%~1"=="status" goto :DoStatus
if /i "%~1"=="stop" goto :DoStop

:Menu
cls
echo ==============================================================================
echo       SUPPORTFLAST.DEV - TRINH DIEU HANH CLOUDFLARE TUNNEL WINDOWS SERVICE
echo       Ten mien: supportflastdev.io.vn  ^|  Cong noi bo: 127.0.0.1:8080
echo ==============================================================================
echo.
echo   [1] Cai dat / Khoi dong lai Cloudflare Service (Tu dong chay 24/7)
echo   [2] Kiem tra trang thai hoat dong (Service, Tien trinh, Cong mang)
echo   [3] Tam dung ket noi Cloudflare Tunnel
echo   [4] Go bo hoan toan Windows Service (Rollback / Uninstall)
echo   [5] Thoat
echo.
echo ==============================================================================
set /p "CHOICE=Nhap lua chon cua ban (1-5) [Mac dinh: 1]: "
if "%CHOICE%"=="" set "CHOICE=1"

if "%CHOICE%"=="1" goto :PromptInstall
if "%CHOICE%"=="2" goto :DoStatus
if "%CHOICE%"=="3" goto :DoStop
if "%CHOICE%"=="4" goto :DoUninstall
if "%CHOICE%"=="5" exit /b 0
goto :Menu

:PromptInstall
echo.
echo ==============================================================================
echo                      CAU HINH CLOUDFLARE TUNNEL TOKEN
echo ==============================================================================
set "SAVED_TOKEN="
if exist "%TOKEN_FILE%" (
    set /p SAVED_TOKEN=<"%TOKEN_FILE%"
)

if defined SAVED_TOKEN (
    echo [*] Da phat hien Token da luu truoc do:
    echo     !SAVED_TOKEN:~0,18!... (da an bot de bao mat)
    echo.
    echo Nhan [Enter] de su dung ngay Token da luu, hoac dan Token moi:
) else (
    echo [Huong dan lay Token]:
    echo   1. Truy cap Cloudflare Zero Trust: https://one.dash.cloudflare.com/
    echo   2. Vao: Networks ^> Tunnels ^> Add a tunnel ^> Cloudflared
    echo   3. Dat ten: supportflast-tunnel
    echo   4. Sao chep chuoi Token dai bat dau bang eyJ...
    echo.
)

set "INPUT_TOKEN="
set /p "INPUT_TOKEN=Dan TOKEN vao day (hoac bam Enter neu dung token cu): "

if defined INPUT_TOKEN (
    set "CF_TOKEN=!INPUT_TOKEN!"
) else (
    set "CF_TOKEN=!SAVED_TOKEN!"
)

if "%CF_TOKEN%"=="" (
    echo [LOI] Khong co Token nao duoc cung cap! Vui long thu lai.
    pause
    goto :Menu
)

:DoInstall
echo.
echo [1/5] Dang luu cau hinh Token an toan...
<nul set /p "=%CF_TOKEN%" > "%TOKEN_FILE%"

echo [2/5] Dang don dep cac service cu (neu co)...
sc.exe stop Cloudflared >nul 2>&1
"%CLOUDFLARED_BIN%" service uninstall >nul 2>&1
timeout /t 1 /nobreak >nul

echo [3/5] Dang cai dat Cloudflare Tunnel lam Windows Service chay 24/7...
"%CLOUDFLARED_BIN%" service install %CF_TOKEN%

echo [4/5] Dang cau hinh che do Tu khoi dong cung Windows va Tu phuc hoi khi loi...
sc.exe config Cloudflared start= auto >nul 2>&1
sc.exe failure Cloudflared reset= 86400 actions= restart/5000/restart/10000/restart/60000 >nul 2>&1

echo [5/5] Dang khoi dong Windows Service 'Cloudflared'...
net start Cloudflared >nul 2>&1

timeout /t 3 /nobreak >nul
goto :CheckResult

:CheckResult
echo.
sc.exe query Cloudflared | findstr /i "RUNNING" >nul
if %errorLevel% equ 0 (
    echo ==============================================================================
    echo   [THANH CONG] Cloudflare Tunnel da chay tu dong 24/7!
    echo ==============================================================================
    echo   - Trang thai Windows Service: DANG HOAT DONG (RUNNING)
    echo   - Kieu khoi dong: TU DONG (Automatic - Khoi dong cung Windows)
    echo   - Co che tu phuc hoi (Auto-Recovery): Tu khoi dong lai neu bi loi
    echo   - Ten mien ket noi toan cau qua Cloudflare Zero Trust
    echo ==============================================================================
) else (
    echo [CANH BAO] Service chua chuyen sang trang thai RUNNING.
    echo Dang thu kich hoat tien trinh chay nen du phong...
    start "" /b "%CLOUDFLARED_BIN%" tunnel run --token %CF_TOKEN%
    echo [OK] Da kich hoat tien trinh du phong.
)
echo.
pause
goto :Menu

:DoStatus
cls
echo ==============================================================================
echo             KIEM TRA TRANG THAI HE THONG CLOUDFLARE TUNNEL
echo ==============================================================================
echo.
echo 1. Trang thai Windows Service 'Cloudflared':
sc.exe query Cloudflared 2>&1 | findstr /i "STATE SERVICE_NAME FAILED"
echo.
echo 2. Trang thai tien trinh cloudflared.exe trong Task Manager:
powershell -NoProfile -Command "$p = Get-Process -Name 'cloudflared' -ErrorAction SilentlyContinue; if ($p) { Write-Host \"   [+] Dang chay voi PID: $($p.Id)\" -ForegroundColor Green } else { Write-Host \"   [-] Khong co tien trinh cloudflared nao.\" -ForegroundColor Yellow }"
echo.
echo 3. Trang thai Cong Web noi bo chuyen tiep:
powershell -NoProfile -Command "$p8080 = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue; if ($p8080) { Write-Host \"   [+] Cong 8080 (Go Engine - supportflast.exe): DANG LANG NGHE [OK]\" -ForegroundColor Green } else { Write-Host \"   [!] Cong 8080 (Go Engine): CHUA BAT [!] Vui long khoi dong backend.\" -ForegroundColor Red }"
echo.
echo ==============================================================================
pause
goto :Menu

:DoStop
echo.
echo [-] Dang dung ket noi Cloudflare Tunnel...
sc.exe stop Cloudflared >nul 2>&1
powershell -NoProfile -Command "Stop-Process -Name 'cloudflared' -Force -ErrorAction SilentlyContinue" >nul 2>&1
echo [OK] Da dung toan bo dich vu va tien trinh Cloudflare Tunnel.
pause
goto :Menu

:DoUninstall
echo.
echo ==============================================================================
echo                    GO BO (ROLLBACK) WINDOWS SERVICE CLOUDFLARED
echo ==============================================================================
echo [-] Dang dung Windows Service...
sc.exe stop Cloudflared >nul 2>&1
timeout /t 1 /nobreak >nul

echo [-] Dang thuc thi go bo service khoi he dieu hanh...
"%CLOUDFLARED_BIN%" service uninstall >nul 2>&1
sc.exe delete Cloudflared >nul 2>&1

echo [-] Don dep tien trinh ngam...
powershell -NoProfile -Command "Stop-Process -Name 'cloudflared' -Force -ErrorAction SilentlyContinue" >nul 2>&1

echo.
echo [THANH CONG] Da go bo hoan toan Windows Service 'Cloudflared'. He thong da sach se.
echo.
pause
goto :Menu
