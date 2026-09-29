@echo off
chcp 65001 >nul
title SUPPORTFLAST - QUẢN LÝ DATABASE BẢO MẬT CAO
cls

:MENU
cls
echo ==============================================================================
echo        SUPPORTFLAST ENTERPRISE - BỘ CÔNG CỤ QUẢN LÝ DATABASE CHUẨN
echo   Bảo Mật Cao - Chống Hack - Chống DDoS - Chống Mất Dữ Liệu (Zero Data Loss)
echo ==============================================================================
echo.
echo   [1] Kiểm toán an ninh CSDL toàn diện (Security Audit)
echo   [2] Sao lưu 3 tầng chống mất dữ liệu (Backup lên Google Drive có Timestamp)
echo   [3] Đồng bộ toàn diện 18 bảng lên TiDB Cloud Serverless (Sync TiDB)
echo   [4] Chạy toàn bộ quy trình chuẩn (Audit -^> Sync TiDB -^> Backup Google Drive)
echo   [5] Mở tài liệu Quy Trình Chuẩn (QUY_TRINH_QUAN_LY_DATABASE_BAO_MAT.md)
echo   [0] Thoát
echo.
echo ==============================================================================
set /p opt="Vui lòng chọn thao tác [0-5]: "

if "%opt%"=="1" goto AUDIT
if "%opt%"=="2" goto BACKUP
if "%opt%"=="3" goto SYNC
if "%opt%"=="4" goto ALL
if "%opt%"=="5" goto DOC
if "%opt%"=="0" goto EXIT
echo Lựa chọn không hợp lệ, vui lòng thử lại.
timeout /t 2 >nul
goto MENU

:AUDIT
cls
echo [INFO] Đang chạy kiểm toán an ninh CSDL...
python tools\db_security_manager.py audit
echo.
pause
goto MENU

:BACKUP
cls
echo [INFO] Đang kích hoạt quy trình sao lưu 3 tầng lên Google Drive...
python tools\db_security_manager.py backup
echo.
pause
goto MENU

:SYNC
cls
echo [INFO] Đang đồng bộ toàn diện dữ liệu lên TiDB Cloud...
python tools\db_security_manager.py sync
echo.
pause
goto MENU

:ALL
cls
echo [INFO] Đang thực thi toàn bộ quy trình bảo mật chuẩn...
python tools\db_security_manager.py all
echo.
pause
goto MENU

:DOC
cls
start notepad docs\QUY_TRINH_QUAN_LY_DATABASE_BAO_MAT.md
goto MENU

:EXIT
cls
exit /b 0
