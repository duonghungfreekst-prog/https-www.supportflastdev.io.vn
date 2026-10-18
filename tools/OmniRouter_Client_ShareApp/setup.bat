@echo off
chcp 65001 >nul
title Cài Đặt OmniRouter Client Node (App Chia Sẻ Dữ Liệu)
cls
echo =====================================================================
echo       BỘ CÀI ĐẶT OMNIROUTER CLIENT NODE - APP CHIA SẺ & DNS
echo =====================================================================
echo.

:: 1. Kiểm tra quyền Quản trị viên (Administrator)
net session >nul 2>&1
if %ERRORLEVEL% NEQ 0 (
    echo [CẢNH BÁO] Cần quyền Administrator để cài chứng chỉ Root CA và cấu hình DNS.
    echo Đang tự động yêu cầu nâng quyền Administrator (UAC)...
    powershell -Command "Start-Process cmd -ArgumentList '/c \"\"%~dp0setup.bat\"\"' -Verb RunAs"
    exit /b
)

echo [1/3] Đang cài đặt Chứng Chỉ SSL Root CA vào Windows Trust Store...
if exist "%~dp0rootCA.crt" (
    certutil -addstore -f "ROOT" "%~dp0rootCA.crt" >nul 2>&1
    if %ERRORLEVEL% EQU 0 (
        echo   ✔ Đã cài đặt thành công Chứng chỉ OmniRouter Root CA!
    ) else (
        echo   ⚠️ Không thể nạp tự động, vui lòng nhấp đúp file rootCA.crt để cài thủ công.
    )
) else (
    echo   ⚠️ Không tìm thấy file rootCA.crt, bỏ qua bước này.
)

echo.
echo [2/3] Đang tạo Shortcut "OmniRouter Client Node" trên Màn hình Desktop...
set SCRIPT="%TEMP%\create_omni_shortcut.vbs"
echo Set oWS = WScript.CreateObject("WScript.Shell") >> %SCRIPT%
echo sLinkFile = oWS.SpecialFolders("Desktop") ^& "\OmniRouter Client Node.lnk" >> %SCRIPT%
echo Set oLink = oWS.CreateShortcut(sLinkFile) >> %SCRIPT%
echo oLink.TargetPath = "%~dp0run_client.bat" >> %SCRIPT%
echo oLink.WorkingDirectory = "%~dp0" >> %SCRIPT%
echo oLink.Description = "OmniRouter Client Node - App Chia Se Du Lieu & DNS" >> %SCRIPT%
echo oLink.Save >> %SCRIPT%
cscript /nologo %SCRIPT%
del %SCRIPT%
echo   ✔ Đã tạo Shortcut ngoài Desktop thành công!

echo.
echo [3/3] Khởi động ứng dụng lần đầu...
timeout /t 2 >nul
start "" "%~dp0run_client.bat"

echo.
echo =====================================================================
echo  ✔ CÀI ĐẶT HOÀN TẤT! BẠN CÓ THỂ MỞ APP TỪ SHORTCUT NGOÀI DESKTOP.
echo =====================================================================
pause
