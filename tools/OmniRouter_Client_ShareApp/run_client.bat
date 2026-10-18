@echo off
chcp 65001 >nul
title OmniRouter Client Node (Desktop App)

:: Khởi chạy trực tiếp file GUI thực thi OmniRouter_Client.exe
if exist "%~dp0OmniRouter_Client.exe" (
    start "" "%~dp0OmniRouter_Client.exe"
    exit /b
)

if exist "%~dp09router-client.exe" (
    start "" "%~dp09router-client.exe"
    exit /b
)

:: Fallback nếu chưa có exe: mở qua trình duyệt
set "HTML_FILE=%~dp0index.html"
set "HTML_URL=file:///%HTML_FILE:\=/%"

if exist "%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe" (
    start "" "%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe" --app="%HTML_URL%" --window-size=1260,860
    exit /b
)
if exist "%ProgramFiles%\Microsoft\Edge\Application\msedge.exe" (
    start "" "%ProgramFiles%\Microsoft\Edge\Application\msedge.exe" --app="%HTML_URL%" --window-size=1260,860
    exit /b
)

start "" "%HTML_FILE%"
exit /b
