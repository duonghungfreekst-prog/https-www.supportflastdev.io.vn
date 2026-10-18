@echo off
chcp 65001 >nul
title OmniRouter Client Node (App Chia Sẻ Dữ Liệu)
cls

echo =====================================================================
echo    KHỞI CHẠY OMNIROUTER CLIENT NODE - APP CHIA SẺ & DNS
echo =====================================================================

:: 1. Khởi động tiến trình đồng bộ nền 9router-client.exe nếu chưa chạy
tasklist /FI "IMAGENAME eq 9router-client.exe" 2>NUL | find /I /N "9router-client.exe">NUL
if "%ERRORLEVEL%"=="1" (
    if exist "%~dp09router-client.exe" (
        echo [1/2] Đang khởi động tiến trình đồng bộ ngầm (9router-client.exe)...
        start /B "" "%~dp09router-client.exe" -config "%~dp0config.json" >nul 2>&1
    )
) else (
    echo [1/2] Tiến trình đồng bộ 9router-client.exe đã sẵn sàng.
)

:: 2. Xác định đường dẫn file giao diện HTML
set "HTML_FILE=%~dp0index.html"
set "HTML_URL=file:///%HTML_FILE:\=/%"

echo [2/2] Đang mở giao diện App Chia Sẻ (Chế độ App Window)...

:: Tìm Microsoft Edge
if exist "%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe" (
    start "" "%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe" --app="%HTML_URL%" --window-size=1080,780
    exit /b
)
if exist "%ProgramFiles%\Microsoft\Edge\Application\msedge.exe" (
    start "" "%ProgramFiles%\Microsoft\Edge\Application\msedge.exe" --app="%HTML_URL%" --window-size=1080,780
    exit /b
)

:: Tìm Google Chrome
if exist "%ProgramFiles%\Google\Chrome\Application\chrome.exe" (
    start "" "%ProgramFiles%\Google\Chrome\Application\chrome.exe" --app="%HTML_URL%" --window-size=1080,780
    exit /b
)
if exist "%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe" (
    start "" "%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe" --app="%HTML_URL%" --window-size=1080,780
    exit /b
)

:: Tìm Cốc Cốc
if exist "%LocalAppData%\CocCoc\Browser\Application\browser.exe" (
    start "" "%LocalAppData%\CocCoc\Browser\Application\browser.exe" --app="%HTML_URL%" --window-size=1080,780
    exit /b
)

:: Mở bằng trình duyệt mặc định nếu không tìm thấy browser hỗ trợ --app
start "" "%HTML_FILE%"
exit /b
