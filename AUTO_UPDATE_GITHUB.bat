@echo off
chcp 65001 >nul
title SupportFlast Dev - Tự Động Đồng Bộ GitHub
echo ==========================================================
echo    SUPPORTFLAST DEV - TỰ ĐỘNG CẬP NHẬT LÊN GITHUB
echo ==========================================================
echo.
cd /d "F:\supportflast.dev"
powershell -NoProfile -ExecutionPolicy Bypass -File "F:\supportflast.dev\auto_git_sync.ps1"
echo.
echo ==========================================================
echo Hoàn thành! Nhấn phím bất kỳ để đóng cửa sổ...
pause >nul
