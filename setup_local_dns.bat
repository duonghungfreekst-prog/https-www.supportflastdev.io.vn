@echo off
chcp 65001 >nul
echo ==============================================================================
echo   ĐÍNH TÊN MIỀN CỤC BỘ: supportflastdev.io.vn -> 127.0.0.1
echo ==============================================================================
echo.

net session >nul 2>&1
if %errorLevel% neq 0 (
    echo [!] Vui lòng bấm chuột phải vào file này và chọn "Run as administrator" (Chạy với quyền Quản trị viên).
    echo     File hosts hệ thống yêu cầu quyền Administrator để chỉnh sửa.
    echo.
    pause
    exit /b 1
)

set HOSTS_FILE=%SystemRoot%\System32\drivers\etc\hosts

findstr /i "supportflastdev.io.vn" "%HOSTS_FILE%" >nul
if %errorLevel% equ 0 (
    echo [OK] Tên miền supportflastdev.io.vn ĐÃ CÓ trong file hosts!
) else (
    echo. >> "%HOSTS_FILE%"
    echo # Domain SupportFlast App Hub >> "%HOSTS_FILE%"
    echo 127.0.0.1 supportflastdev.io.vn >> "%HOSTS_FILE%"
    echo 127.0.0.1 www.supportflastdev.io.vn >> "%HOSTS_FILE%"
    echo [OK] Đã thêm thành công:
    echo      127.0.0.1 supportflastdev.io.vn
    echo      127.0.0.1 www.supportflastdev.io.vn
    echo      vào file %HOSTS_FILE%
)

ipconfig /flushdns >nul
echo [OK] Đã làm mới DNS cache (ipconfig /flushdns).
echo.
echo Bạn có thể mở trình duyệt và truy cập:
echo     http://supportflastdev.io.vn:8080
echo.
pause
