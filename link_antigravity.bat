@echo off
title SupportFlast - Tu Dong Dinh Tuyen Antigravity IDE (1-Click)

set "CONFIG_PATH=%USERPROFILE%\.gemini\config\config.json"
set "DEFAULT_GATEWAY=https://supportflastdev.io.vn/v1"
set "MASTER_KEY=sk-supportflastdev.io.vn-master-live"
set "LOCAL_GATEWAY=http://localhost:20128/v1"
set "LOCAL_KEY=9router"

REM Kiem tra file config Antigravity
if not exist "%CONFIG_PATH%" (
    echo.
    echo ==============================================================================
    echo   [!] KHONG TIM THAY FILE CAU HINH ANTIGRAVITY
    echo   Duong dan kiem tra: %CONFIG_PATH%
    echo   Vui long khoi dong Antigravity IDE it nhat 1 lan truoc khi chay lenh nay.
    echo ==============================================================================
    echo.
    goto do_end
)

REM Xu ly lenh theo tham so
set "ACTION=%~1"
set "PARAM_KEY=%~2"

if /i "%ACTION%"=="--status" goto do_status
if /i "%ACTION%"=="status" goto do_status
if /i "%ACTION%"=="-s" goto do_status

if /i "%ACTION%"=="--local" goto do_local
if /i "%ACTION%"=="local" goto do_local
if /i "%ACTION%"=="loco" goto do_local
if /i "%ACTION%"=="--loco" goto do_local

if /i "%ACTION%"=="--reset" goto do_reset
if /i "%ACTION%"=="reset" goto do_reset
if /i "%ACTION%"=="restore" goto do_reset

REM Mac dinh: Tro ve Web Gateway SupportFlast
goto do_web

:do_web
set "TARGET_KEY=%ACTION%"
if "%TARGET_KEY%"=="" set "TARGET_KEY=%MASTER_KEY%"
if /i "%TARGET_KEY%"=="web" (
    if not "%PARAM_KEY%"=="" (
        set "TARGET_KEY=%PARAM_KEY%"
    ) else (
        set "TARGET_KEY=%MASTER_KEY%"
    )
)

echo.
echo ==============================================================================
echo       SUPPORTFLAST.DEV - TU DONG TRO ANTIGRAVITY VE WEB GATEWAY
echo ==============================================================================
echo   [*] May chu Gateway:  %DEFAULT_GATEWAY%
echo   [*] Khoa API Key:     %TARGET_KEY%
echo   [*] Tep cau hinh:     %CONFIG_PATH%
echo ==============================================================================
echo.

powershell -NoProfile -ExecutionPolicy Bypass -Command "$p = Join-Path $HOME '.gemini\config\config.json'; if (Test-Path $p) { $c = Get-Content $p -Raw | ConvertFrom-Json; $c.apiEndpoint = '%DEFAULT_GATEWAY%'; $c.apiKey = '%TARGET_KEY%'; $c | ConvertTo-Json -Depth 20 | Set-Content $p -Encoding UTF8; Write-Host '  [OK] Da ghi de apiEndpoint va apiKey vao config.json thanh cong!' -ForegroundColor Green; exit 0 } else { exit 1 }"
if errorlevel 1 (
    echo.
    echo   [LOI] Co loi xay ra khi cap nhat cau hinh.
    echo.
) else (
    echo.
    echo   [OK] THANH CONG: Google Antigravity da tu dong tro den Web Gateway.
    echo   [OK] Moi request tu Antigravity se duoc tinh Quota truc tiep tren Web.
    echo   [OK] Khong can phai cau hinh bang tay tren giao dien Antigravity IDE.
    echo.
)
goto do_end

:do_local
echo.
echo ==============================================================================
echo       SUPPORTFLAST.DEV - TU DONG TRO ANTIGRAVITY VE LOCAL ROUTER - RUN LOCO
echo ==============================================================================
echo   [*] May chu Local:    %LOCAL_GATEWAY%
echo   [*] Khoa API Key:     %LOCAL_KEY%
echo   [*] Tep cau hinh:     %CONFIG_PATH%
echo ==============================================================================
echo.

powershell -NoProfile -ExecutionPolicy Bypass -Command "$p = Join-Path $HOME '.gemini\config\config.json'; if (Test-Path $p) { $c = Get-Content $p -Raw | ConvertFrom-Json; $c.apiEndpoint = '%LOCAL_GATEWAY%'; $c.apiKey = '%LOCAL_KEY%'; $c | ConvertTo-Json -Depth 20 | Set-Content $p -Encoding UTF8; Write-Host '  [OK] Da tro ve Local Router port 20128 thanh cong.' -ForegroundColor Green; exit 0 } else { exit 1 }"
if errorlevel 1 (
    echo.
    echo   [LOI] Co loi xay ra khi cap nhat cau hinh.
    echo.
) else (
    echo.
    echo   [OK] THANH CONG: Antigravity da tro ve che do chay Cuc Bo - Run Loco.
    echo.
)
goto do_end

:do_reset
echo.
echo ==============================================================================
echo       SUPPORTFLAST.DEV - KHOI PHUC ANTIGRAVITY VE MAC DINH CUA GOOGLE
echo ==============================================================================
echo.

powershell -NoProfile -Command "$p = Join-Path $HOME '.gemini\config\config.json'; if (Test-Path $p) { $c = Get-Content $p -Raw | ConvertFrom-Json; $c.PSObject.Properties.Remove('apiEndpoint'); $c.PSObject.Properties.Remove('apiKey'); $c | ConvertTo-Json -Depth 20 | Set-Content $p -Encoding UTF8; Write-Host '  [OK] Da go bo cau hinh Gateway, tro ve mac dinh Google.' -ForegroundColor Yellow; exit 0 } else { exit 1 }"
if errorlevel 1 (
    echo.
    echo   [LOI] Co loi xay ra khi cap nhat cau hinh.
    echo.
) else (
    echo.
    echo   [OK] THANH CONG: Antigravity da duoc khoi phuc ve mac dinh goc.
    echo.
)
goto do_end

:do_status
echo.
echo ==============================================================================
echo       KIEM TRA TRANG THAI CAU HINH ANTIGRAVITY IDE HIEN TAI
echo ==============================================================================
powershell -NoProfile -ExecutionPolicy Bypass -Command "$p = Join-Path $HOME '.gemini\config\config.json'; if (Test-Path $p) { $c = Get-Content $p -Raw | ConvertFrom-Json; Write-Host ('  - Tep cau hinh: ' + $p); Write-Host ('  - apiEndpoint:  ' + $c.apiEndpoint) -ForegroundColor Cyan; Write-Host ('  - apiKey:       ' + $c.apiKey) -ForegroundColor Yellow; if ($c.apiEndpoint -like '*supportflastdev.io.vn*') { Write-Host '  => Trang thai: DANG KET NOI VOI WEB GATEWAY SUPPORTFLAST' -ForegroundColor Green } elseif ($c.apiEndpoint -like '*localhost*' -or $c.apiEndpoint -like '*127.0.0.1*') { Write-Host '  => Trang thai: DANG CHAY CUC BO - RUN LOCAL / LOCO' -ForegroundColor Green } else { Write-Host '  => Trang thai: MAC DINH HOAC CHUA TRO' -ForegroundColor Gray } } else { Write-Host '  [!] Khong tim thay tep cau hinh' }"
echo ==============================================================================
echo.
goto do_end

:do_end
exit /b 0
