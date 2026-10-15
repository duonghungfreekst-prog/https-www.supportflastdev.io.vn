@echo off
title SupportFlast - Tu Dong Kich Hoat Antigravity IDE (MITM Gateway)

set "HOSTS_PATH=%SystemRoot%\System32\drivers\etc\hosts"
set "CONFIG_PATH=%USERPROFILE%\.gemini\config\config.json"
set "LOCAL_GATEWAY=http://localhost:20128/v1"
set "LOCAL_KEY=9router"

REM Xu ly lenh theo tham so
set "ACTION=%~1"

if /i "%ACTION%"=="--status" goto do_status
if /i "%ACTION%"=="status" goto do_status
if /i "%ACTION%"=="-s" goto do_status

if /i "%ACTION%"=="--reset" goto do_reset
if /i "%ACTION%"=="reset" goto do_reset
if /i "%ACTION%"=="restore" goto do_reset

if /i "%ACTION%"=="--local" goto do_local
if /i "%ACTION%"=="local" goto do_local
if /i "%ACTION%"=="loco" goto do_local
if /i "%ACTION%"=="--loco" goto do_local

REM Mac dinh: Kich hoat Gateway (hosts file MITM)
goto do_activate

:do_activate
echo.
echo ==============================================================================
echo       SUPPORTFLAST.DEV - KICH HOAT ANTIGRAVITY IDE QUA GATEWAY
echo ==============================================================================
echo   Co che: Hosts file MITM - redirect DNS Google ve OmniRouter local
echo   IDE gui request den daily-cloudcode-pa.googleapis.com
echo   Hosts file chuyen ve 127.0.0.1 - OmniRouter proxy qua IP that
echo ==============================================================================
echo.

REM Kiem tra quyen Administrator
net session >nul 2>&1
if errorlevel 1 (
    echo   [!] CAN QUYEN ADMINISTRATOR de chinh sua hosts file.
    echo   [!] Hay click chuot phai -^> "Run as Administrator"
    echo.
    echo   CACH LAM: Click phai file nay -^> "Run as Administrator"
    echo.
    goto do_end
)

REM Kiem tra xem hosts file da co entry chua
findstr /C:"daily-cloudcode-pa.googleapis.com" "%HOSTS_PATH%" >nul 2>&1
if %errorlevel%==0 (
    echo   [OK] Hosts file DA CO entry redirect - Antigravity IDE da duoc kich hoat!
    echo.
    goto do_check_router
)

REM Them entry vao hosts file
echo.>> "%HOSTS_PATH%"
echo 127.0.0.1 daily-cloudcode-pa.googleapis.com>> "%HOSTS_PATH%"
echo 127.0.0.1 cloudcode-pa.googleapis.com>> "%HOSTS_PATH%"
echo.
echo   [OK] Da them entry redirect vao hosts file thanh cong!
echo.

:do_check_router
REM Kiem tra OmniRouter co dang chay khong
echo   [*] Kiem tra OmniRouter...
set "ROUTER_RUNNING=0"
netstat -anp tcp 2>nul | findstr "0.0.0.0:443.*LISTENING" >nul 2>&1
if %errorlevel%==0 set "ROUTER_RUNNING=1"

if "%ROUTER_RUNNING%"=="0" (
    echo   [*] OmniRouter chua chay. Dang tu dong khoi dong Engine tren cong 443 va 20128...
    if exist "F:\9router-stt-upgraded\dist\OmniRouter\omnirouter_engine.exe" (
        powershell -NoProfile -ExecutionPolicy Bypass -Command "Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{CommandLine = 'F:\9router-stt-upgraded\dist\OmniRouter\omnirouter_engine.exe --port 20128 --dir F:\9router-stt-upgraded'}; Start-Sleep -Seconds 2" >nul 2>&1
    ) else (
        echo   [!] Khong tim thay binary omnirouter_engine.exe
    )
)

netstat -anp tcp 2>nul | findstr "0.0.0.0:443.*LISTENING" >nul 2>&1
if %errorlevel%==0 (
    echo   [OK] OmniRouter MITM dang lang nghe tren port 443 - HOAN HAO!
) else (
    echo   [!] CANH BAO: Port 443 chua san sang. Hay click phai link_antigravity.bat chon Run as Administrator!
)

netstat -anp tcp 2>nul | findstr "0.0.0.0:20128.*LISTENING" >nul 2>&1
if %errorlevel%==0 (
    echo   [OK] OmniRouter Core dang lang nghe tren port 20128 - HOAN HAO!
) else (
    echo   [!] CANH BAO: Port 20128 chua san sang.
)

echo.
echo   ==========================================================
echo   THANH CONG! Antigravity IDE da duoc kich hoat qua Gateway.
echo   Moi request tu IDE se duoc proxy qua tai khoan AI pool.
echo   ==========================================================
echo.

REM Xoa DNS cache de hosts file co hieu luc ngay
ipconfig /flushdns >nul 2>&1
echo   [OK] Da xoa DNS cache de ap dung ngay lap tuc.
echo.
goto do_end

:do_local
echo.
echo ==============================================================================
echo       SUPPORTFLAST.DEV - TRO ANTIGRAVITY VE CONFIG CUC BO (Run Loco)
echo ==============================================================================
echo.

if not exist "%CONFIG_PATH%" (
    echo   [!] KHONG TIM THAY file config.json Antigravity IDE
    echo   [!] Duong dan: %CONFIG_PATH%
    echo.
    goto do_end
)

powershell -NoProfile -ExecutionPolicy Bypass -Command "$p = Join-Path $HOME '.gemini\config\config.json'; if (Test-Path $p) { $c = Get-Content $p -Raw | ConvertFrom-Json; $c.apiEndpoint = '%LOCAL_GATEWAY%'; $c.apiKey = '%LOCAL_KEY%'; $c | ConvertTo-Json -Depth 20 | Set-Content $p -Encoding UTF8; Write-Host '  [OK] Da tro Antigravity ve Local Router port 20128.' -ForegroundColor Green; exit 0 } else { exit 1 }"
if errorlevel 1 (
    echo   [LOI] Co loi khi cap nhat config.json
) else (
    echo   [OK] THANH CONG: Antigravity da tro ve che do chay Cuc Bo - Run Loco.
)
echo.
goto do_end

:do_reset
echo.
echo ==============================================================================
echo       SUPPORTFLAST.DEV - VO HIEU HOA GATEWAY VA KHOI PHUC MAC DINH
echo ==============================================================================
echo.

REM Kiem tra quyen Administrator
net session >nul 2>&1
if errorlevel 1 (
    echo   [!] CAN QUYEN ADMINISTRATOR de chinh sua hosts file.
    echo   [!] Hay click chuot phai -^> "Run as Administrator"
    echo.
    goto do_end
)

REM Xoa entry khoi hosts file
powershell -NoProfile -ExecutionPolicy Bypass -Command "$h = Get-Content '%HOSTS_PATH%' -Raw; $lines = $h -split [Environment]::NewLine; $cleaned = $lines | Where-Object { $_ -notmatch 'daily-cloudcode-pa\.googleapis\.com' -and $_ -notmatch 'cloudcode-pa\.googleapis\.com' }; ($cleaned -join [Environment]::NewLine).TrimEnd() | Set-Content '%HOSTS_PATH%' -Encoding ASCII; Write-Host '  [OK] Da xoa entry redirect khoi hosts file.' -ForegroundColor Yellow; exit 0"

REM Xoa apiEndpoint khoi config.json (neu co)
if exist "%CONFIG_PATH%" (
    powershell -NoProfile -Command "$p = '%CONFIG_PATH%'; $c = Get-Content $p -Raw | ConvertFrom-Json; $c.PSObject.Properties.Remove('apiEndpoint'); $c.PSObject.Properties.Remove('apiKey'); $c | ConvertTo-Json -Depth 20 | Set-Content $p -Encoding UTF8; Write-Host '  [OK] Da go bo apiEndpoint/apiKey khoi config.json' -ForegroundColor Yellow; exit 0"
)

ipconfig /flushdns >nul 2>&1
echo.
echo   [OK] THANH CONG: Antigravity IDE da duoc khoi phuc ve mac dinh Google.
echo   [OK] DNS cache da duoc xoa sach.
echo.
goto do_end

:do_status
echo.
echo ==============================================================================
echo       KIEM TRA TRANG THAI ANTIGRAVITY IDE GATEWAY
echo ==============================================================================
echo.

echo   --- HOSTS FILE ---
findstr /C:"daily-cloudcode-pa.googleapis.com" "%HOSTS_PATH%" >nul 2>&1
if %errorlevel%==0 (
    echo   [ACTIVE] Hosts file DA redirect daily-cloudcode-pa.googleapis.com ve 127.0.0.1
    echo   =^> IDE dang duoc proxy qua OmniRouter Gateway
) else (
    echo   [INACTIVE] Hosts file KHONG co entry redirect
    echo   =^> IDE dang ket noi truc tiep den Google
)
echo.

echo   --- OMNIROUTER ---
netstat -anp tcp 2>nul | findstr "0.0.0.0:443.*LISTENING" >nul 2>&1
if %errorlevel%==0 (
    echo   [RUNNING] OmniRouter port 443: DANG CHAY
) else (
    echo   [STOPPED] OmniRouter port 443: KHONG CHAY
)

netstat -anp tcp 2>nul | findstr "0.0.0.0:20128.*LISTENING" >nul 2>&1
if %errorlevel%==0 (
    echo   [RUNNING] OmniRouter port 20128: DANG CHAY
) else (
    echo   [STOPPED] OmniRouter port 20128: KHONG CHAY
)
echo.

echo   --- CONFIG.JSON ---
if exist "%CONFIG_PATH%" (
    powershell -NoProfile -ExecutionPolicy Bypass -Command "$c = Get-Content '%CONFIG_PATH%' -Raw | ConvertFrom-Json; Write-Host ('  apiEndpoint: ' + $c.apiEndpoint) -ForegroundColor Cyan; Write-Host ('  apiKey:      ' + $c.apiKey) -ForegroundColor Yellow"
) else (
    echo   [!] File config.json khong ton tai
)

echo.
echo ==============================================================================
echo.
goto do_end

:do_end
exit /b 0
