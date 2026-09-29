@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

title SUPPORTFLAST - KHOI DONG TOAN BO HE THONG 1-CLICK (24/7 ONLINE)
color 0A

:: 1. Tu dong kiem tra va nang quyen Administrator
net session >nul 2>&1
if %errorLevel% neq 0 (
    echo [!] Dang kich hoat quyen Administrator...
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process cmd -ArgumentList '/c \"\"%~f0\" %*\"' -Verb RunAs"
    exit /b
)

set "ROOT_DIR=%~dp0"
set "ENGINE_EXE=%ROOT_DIR%supportflast.exe"
set "CLOUDFLARED_BIN=%ROOT_DIR%tools\cloudflared.exe"
set "TOKEN_FILE=%ROOT_DIR%tools\cloudflare_token.txt"
set "DEFAULT_TOKEN=eyJhIjoiNTVkNzc2NTNkMDIxYzJmYmRiODBjYWI4ZTRmYTJjNzAiLCJ0IjoiOWVjMjliMWYtMGRlNy00MjBlLWFjZDEtZWQ2Y2E4Yzc0NWU1IiwicyI6IldsNlRPRlQwcENHT0NVY1l1SGhYOC9jSmtDQWd5RHNuY1FIM05SVzdWTnM9In0="

echo ==============================================================================
echo       SUPPORTFLAST.DEV - TRINH KHOI DONG TOAN BO HE THONG 1-CLICK
echo       Ten mien: supportflastdev.io.vn  ^|  Cloudflare Tunnel 24/7
echo ==============================================================================
echo.

:: [1/4] Kiem tra va khoi dong Windows Service SupportFlastEngine (chay ngam 100%)
echo [*] [1/4] Dang kiem tra Go Web Engine (cong 8080)...
sc.exe query SupportFlastEngine | findstr /i "RUNNING" >nul
if %errorLevel% equ 0 (
    echo     [OK] Windows Service SupportFlastEngine dang chay ngam on dinh.
) else (
    netstat -ano | findstr /r ":8080[ ]" | findstr "LISTENING" >nul
    if %errorLevel% neq 0 (
        echo     [+] May chu chua chay. Dang khoi dong service SupportFlastEngine...
        net start SupportFlastEngine >nul 2>&1
        if %errorLevel% neq 0 (
            powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process '%ENGINE_EXE%' -WorkingDirectory '%ROOT_DIR%' -WindowStyle Hidden"
        )
        timeout /t 3 /nobreak >nul
    ) else (
        echo     [OK] Go Web Engine dang hoat dong tot tren cong 8080.
    )
)

:: [2/4] Dong bo file cau hinh Cloudflare Tunnel vao he thong
echo [*] [2/4] Dang dong bo cau hinh Cloudflare Tunnel...
if not exist "C:\Windows\System32\config\systemprofile\.cloudflared" (
    mkdir "C:\Windows\System32\config\systemprofile\.cloudflared" >nul 2>&1
)
copy /y "%ROOT_DIR%cloudflared_config.yml" "C:\Windows\System32\config\systemprofile\.cloudflared\config.yml" >nul 2>&1

:: [3/4] Kiem tra va khoi dong Windows Service Cloudflared
echo [*] [3/4] Dang kiem tra dich vu Cloudflare Tunnel 24/7...
set "CF_TOKEN="
if exist "%TOKEN_FILE%" (
    set /p CF_TOKEN=<"%TOKEN_FILE%"
)
if "%CF_TOKEN%"=="" set "CF_TOKEN=%DEFAULT_TOKEN%"

sc.exe query Cloudflared | findstr /i "RUNNING" >nul
if %errorLevel% neq 0 (
    echo     [+] Dang cai dat / khoi dong lai Cloudflare Service...
    sc.exe stop Cloudflared >nul 2>&1
    "%CLOUDFLARED_BIN%" service uninstall >nul 2>&1
    timeout /t 1 /nobreak >nul
    "%CLOUDFLARED_BIN%" service install %CF_TOKEN% >nul 2>&1
    sc.exe config Cloudflared start= auto >nul 2>&1
    sc.exe failure Cloudflared reset= 86400 actions= restart/5000/restart/10000/restart/60000 >nul 2>&1
    net start Cloudflared >nul 2>&1
    timeout /t 3 /nobreak >nul
) else (
    echo     [OK] Windows Service Cloudflared dang chay ngam on dinh.
)

:: [4/4] Kiem tra ket noi va thong bao ket qua
echo [*] [4/4] Kiem tra ket noi truc tuyen...
sc.exe query Cloudflared | findstr /i "RUNNING" >nul
if %errorLevel% equ 0 (
    echo.
    echo ==============================================================================
    echo   [THANH CONG RUC RO] TOAN BO HE THONG DA ONLINE 100%!
    echo ==============================================================================
    echo   - May chu noi bo:      http://127.0.0.1:8080 (Go Engine)
    echo   - Duong ham bao mat:   Cloudflare Zero Trust Tunnel 24/7 (Zero Port Opened)
    echo   - Ten mien toan cau:   https://supportflastdev.io.vn
    echo                          https://www.supportflastdev.io.vn
    echo ==============================================================================
    echo.
    echo [*] Dang mo trinh duyet truy cap trang web...
    start "" "https://www.supportflastdev.io.vn"
) else (
    echo [!] Co loi xay ra khi khoi dong service.
)

echo [*] Cua so nay se tu dong dong sau 3 giay (He thong chay ngam 24/7 khong can giu cua so nay)...
timeout /t 3 /nobreak >nul
exit /b 0
