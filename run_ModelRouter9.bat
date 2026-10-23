@echo off
chcp 65001 >nul
title Khởi động ModelRouter9 AI Gateway
cd /d "%~dp0storage\packages\APP-ROUTER_ModelRouter9_v2.9.0"
call "run_ModelRouter9.bat"
