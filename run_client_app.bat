@echo off
chcp 65001 >nul
title Khởi Động OmniRouter Client Node (App Chia Sẻ)
cd /d "%~dp0tools\OmniRouter_Client_ShareApp"
call run_client.bat
