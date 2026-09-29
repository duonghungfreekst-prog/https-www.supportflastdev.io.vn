@echo off
chcp 65001 >nul
setlocal EnableDelayedExpansion

echo ====================================================================
echo    SUPPORTFLAST APP HUB - 1-CLICK DEPLOY SCRIPT (WINDOWS)
echo    Day ung dung tu dong len SupportFlast Hub (supportflastdev.io.vn)
echo ====================================================================
echo.

:: Kiem tra Python
where python >nul 2>nul
if %errorlevel% neq 0 (
    echo [LOI] Khong tim thay Python tren he thong!
    echo Vui long cai dat Python 3 tu https://www.python.org/ hoac Microsoft Store.
    pause
    exit /b 1
)

:: Tim script uploader
set UPLOADER_SCRIPT=%~dp0supportflast_uploader.py

if not exist "%UPLOADER_SCRIPT%" (
    echo [CANH BAO] Khong tim thay supportflast_uploader.py trong thu muc hien tai!
    echo Dang thu tim trong f:\supportflast.dev\tools\...
    set UPLOADER_SCRIPT=f:\supportflast.dev\tools\supportflast_uploader.py
)

if not exist "%UPLOADER_SCRIPT%" (
    echo [LOI] Khong tim thay supportflast_uploader.py!
    pause
    exit /b 1
)

echo [*] Dang chay SupportFlast Uploader CLI...
echo.

python "%UPLOADER_SCRIPT%" %*

if %errorlevel% equ 0 (
    echo.
    echo ====================================================================
    echo [THANH CONG] Ung dung da duoc xuat ban len SupportFlast App Hub!
    echo ====================================================================
) else (
    echo.
    echo [THAT BAI] Qua trinh xuat ban gap loi. Vui long kiem tra lai thong tin!
)

echo.
pause
