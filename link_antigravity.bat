@echo off
setlocal
chcp 65001 >nul
title SupportFlast - Dinh Tuyen Antigravity IDE Den Web Gateway

set "KEY=%~1"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0link_antigravity.ps1" "%KEY%"

if "%~1"=="" pause
exit /b 0
