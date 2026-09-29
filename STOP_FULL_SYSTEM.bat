@echo off
chcp 65001 >nul
title SUPPORTFLAST - DUNG TOAN BO HE THONG
color 0C

echo ==============================================================================
echo       SUPPORTFLAST.DEV - DUNG TOAN BO HE THONG
echo ==============================================================================
echo.

echo [-] Dang dung dich vu Cloudflare Tunnel...
sc.exe stop Cloudflared >nul 2>&1
taskkill /f /im cloudflared.exe >nul 2>&1

echo [-] Dang dung Go Web Engine (supportflast.exe)...
taskkill /f /im supportflast.exe >nul 2>&1

echo [OK] Da tat toan bo may chu va duong ham bao mat thanh cong.
echo.
pause
