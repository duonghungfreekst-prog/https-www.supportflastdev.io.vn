@echo off
chcp 65001 >nul
title SUPPORTFLAST.DEV - CLOUDFLARE QUICK TUNNEL (HTTPS TU DONG)
color 0A

echo ==============================================================================
echo       SUPPORTFLAST.DEV - PHAT SONG WEB RA INTERNET QUA CLOUDFLARE
echo       Tranh hoan toan cong cua 9router.com ^| Khong can mo port
echo ==============================================================================
echo.
echo [*] Dang kiem tra Go Engine (cong 8080)...
netstat -ano | findstr "8080" | findstr "LISTENING" >nul
if %errorLevel% neq 0 (
    echo [!] May chu supportflast.exe chua chay o cong 8080!
    echo [*] Dang khoi dong supportflast.exe...
    start /min "" "%~dp0supportflast.exe"
    timeout /t 3 /nobreak >nul
)

echo [OK] May chu noi bo 8080 dang san sang!
echo.
echo [*] Dang khoi tao Cloudflare Quick Tunnel ra Internet...
echo ==============================================================================
echo   DUONG LINK TRUY CAP TRUC TIEP TU NGOAI INTERNET / 4G SE HIEN THI DUOI DAY:
echo   (Dinh dang: https://xxxx-xxxx.trycloudflare.com)
echo ==============================================================================
echo.

"%~dp0tools\cloudflared.exe" tunnel --url http://127.0.0.1:8080

pause
