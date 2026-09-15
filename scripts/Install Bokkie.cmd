@echo off
rem Installs Bokkie on this computer. Double-click it.
rem
rem Everything careful happens inside bokkie.exe: this file exists only to save
rem the person standing at the station from opening a command prompt and typing
rem a path, which is the step that actually goes wrong on a support call.
title Install Bokkie
setlocal

rem Windows will have shown "Windows protected your PC" before this ran, because
rem the download is not signed by a certificate we have bought. More info ->
rem Run anyway is the way past it, and is expected.

net session >nul 2>&1
if errorlevel 1 (
    echo Asking Windows for administrator rights...
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
    exit /b
)

cd /d "%~dp0"
if not exist "bokkie-win-amd64.exe" (
    echo.
    echo Could not find bokkie-win-amd64.exe next to this file.
    echo.
    echo Copy the whole Bokkie folder from the USB stick onto this computer,
    echo then run this file from the copy.
    echo.
    pause
    exit /b 1
)

echo.
echo Installing Bokkie. This takes a few seconds.
echo.
bokkie-win-amd64.exe install
if errorlevel 1 (
    echo.
    echo The install did not finish. Nothing has been removed and the old
    echo system, if there was one, is untouched.
    echo.
    echo Send the developer everything printed above.
    echo.
    pause
    exit /b 1
)

echo.
echo Bokkie is installed and starts with this computer.
echo.
echo Open this address on any computer or phone at the station:
echo     http://bokkie.local:8080
echo.
pause
