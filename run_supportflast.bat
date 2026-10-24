@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

title SupportFlast.dev - Trình Khởi Động Máy Chủ An Toàn (Local & Cloud)
color 0B

set "ROOT_DIR=%~dp0"
set "ENGINE_EXE=%ROOT_DIR%supportflast.exe"
set "CLOUDFLARED_BIN=%ROOT_DIR%tools\cloudflared.exe"
set "TOKEN_FILE=%ROOT_DIR%tools\cloudflare_token.txt"

echo ==============================================================================
echo       SUPPORTFLAST.DEV - TRÌNH KHỞI ĐỘNG HỆ THỐNG AN TOÀN & MINH BẠCH
echo       Kiến trúc: Go Engine + Python AI + Web UI
echo ==============================================================================
echo.

:: 1. Kiểm tra tập tin thực thi Go Web Engine
if not exist "%ENGINE_EXE%" (
    echo [!] Không tìm thấy tập tin %ENGINE_EXE%
    echo [*] Vui lòng chạy 'go build -o supportflast.exe ./supportflast_engine' hoặc kiểm tra mã nguồn.
    pause
    exit /b 1
)

:: 1. Khởi chạy Go Web Engine để nạp cấu hình và tạo khóa nội bộ dùng chung
echo [*] [1/3] Đang khởi động Go Web Engine (Cổng 8080)...
netstat -ano | findstr /r ":8080[ ]" | findstr "LISTENING" >nul
if %errorLevel% equ 0 (
    echo     [OK] Cổng 8080 đang có tiến trình hoạt động.
) else (
    echo     [+] Khởi chạy SupportFlast Engine tại http://127.0.0.1:8080...
    start "SupportFlast Go Engine" /d "%ROOT_DIR%" "%ENGINE_EXE%"
    timeout /t 2 /nobreak >nul
)

:: 2. Khởi chạy Python AI để Go Engine có thể chuyển tiếp các yêu cầu Subagent
echo [*] [2/3] Đang kiểm tra Python AI (Cổng 8000)...
set "AI_DIR=%ROOT_DIR%supportflast_ai"
set "AI_PYTHON="
if exist "%AI_DIR%\venv\Scripts\python.exe" set "AI_PYTHON=%AI_DIR%\venv\Scripts\python.exe"
if not defined AI_PYTHON if exist "%AI_DIR%\.venv\Scripts\python.exe" set "AI_PYTHON=%AI_DIR%\.venv\Scripts\python.exe"
set "AI_PYTHON_AVAILABLE=0"
if defined AI_PYTHON set "AI_PYTHON_AVAILABLE=1"
if not defined AI_PYTHON (
    set "AI_PYTHON=python"
    where python >nul 2>nul
    if not errorlevel 1 set "AI_PYTHON_AVAILABLE=1"
)

set "AI_READY=0"
curl.exe -fsS --max-time 2 http://127.0.0.1:8000/api/health >nul 2>&1
if not errorlevel 1 (
    set "AI_READY=1"
    echo     [OK] Python AI đang phản hồi tại http://127.0.0.1:8000.
) else (
    if "!AI_PYTHON_AVAILABLE!"=="0" (
        echo     [!] Không tìm thấy Python. Cài Python hoặc tạo môi trường tại "%AI_DIR%\venv".
    ) else (
        echo     [+] Khởi chạy Python AI...
        start "SupportFlast Python AI" /d "%AI_DIR%" "%AI_PYTHON%" -m uvicorn app:app --host 127.0.0.1 --port 8000 --workers 2
        for /l %%i in (1,1,15) do (
            if "!AI_READY!"=="0" (
                curl.exe -fsS --max-time 1 http://127.0.0.1:8000/api/health >nul 2>&1
                if not errorlevel 1 (
                    set "AI_READY=1"
                ) else (
                    timeout /t 1 /nobreak >nul
                )
            )
        )
        if "!AI_READY!"=="1" (
            echo     [OK] Python AI đã sẵn sàng.
        ) else (
            echo     [!] Python AI chưa phản hồi sau 30 giây.
            echo         Kiểm tra cửa sổ Python AI và cài dependencies trong supportflast_ai\requirements.txt.
            echo         Go Engine vẫn chạy ở chế độ fallback; API /api/agents sẽ báo trạng thái degraded.
        )
    )
)

:: 3. Kiểm tra cấu hình Cloudflare Tunnel (Tùy chọn cho người dùng muốn mở domain riêng)
echo [*] [3/3] Kiểm tra kết nối Cloudflare Zero Trust Tunnel...
set "CF_TOKEN="

:: Đọc token từ biến môi trường hoặc file cấu hình bí mật cục bộ
if defined CLOUDFLARE_TUNNEL_TOKEN (
    set "CF_TOKEN=%CLOUDFLARE_TUNNEL_TOKEN%"
) else if exist "%TOKEN_FILE%" (
    set /p CF_TOKEN=<"%TOKEN_FILE%"
)

if not "%CF_TOKEN%"=="" (
    if exist "%CLOUDFLARED_BIN%" (
        echo     [+] Đã phát hiện Cloudflare Tunnel Token hợp lệ.
        echo     [+] Đang khởi chạy Cloudflare Tunnel an toàn...
        start "Cloudflare Tunnel Client" /d "%ROOT_DIR%" "%CLOUDFLARED_BIN%" tunnel run --token %CF_TOKEN%
    ) else (
        echo     [!] Không tìm thấy công cụ %CLOUDFLARED_BIN% để chạy Tunnel.
    )
) else (
    echo     [INFO] Không có Cloudflare Token được cấu hình.
    echo            Máy chủ đang chạy ở chế độ Cục Bộ Nội Bộ (Local Mode):
    echo            - Truy cập Giao Diện: http://127.0.0.1:8080
    echo            - Cổng Định Tuyến AI: http://127.0.0.1:8080/router/
    echo            (Nếu muốn kết nối Internet qua domain riêng, hãy đặt token vào file 'tools\cloudflare_token.txt')
)

echo.
echo ==============================================================================
echo   [HOÀN TẤT] HỆ THỐNG ĐÃ SẴN SÀNG SỬ DỤNG
echo   - Địa chỉ máy chủ: http://127.0.0.1:8080
echo   - Dashboard AI:    http://127.0.0.1:8080/router/
echo ==============================================================================
echo.
echo [*] Đang mở trình duyệt vào giao diện hệ thống...
start "" "http://127.0.0.1:8080/router/"

echo [*] Nhấn phím bất kỳ để đóng cửa sổ trợ lý khởi động này (Máy chủ vẫn chạy độc lập)...
pause >nul
exit /b 0
