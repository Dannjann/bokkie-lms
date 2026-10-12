@echo off
rem Updates Bokkie on this computer to the version in this folder.
rem Double-click it.
rem
rem bokkie.exe does the work: it checks the signature, backs the database up,
rem stops the service, swaps the binary, starts it again, and puts the old one
rem back if the new one does not answer.
title Update Bokkie
setlocal

net session >nul 2>&1
if errorlevel 1 (
    echo Asking Windows for administrator rights...
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
    exit /b
)

cd /d "%~dp0"
if not exist "latest.json" (
    echo.
    echo This folder does not look like a Bokkie release: latest.json is missing.
    echo.
    pause
    exit /b 1
)

echo.
echo Checking what is in this folder...
echo.
bokkie-win-amd64.exe update -check "%~dp0"
if errorlevel 1 (
    echo.
    echo This folder is not a genuine Bokkie release, or it is damaged.
    echo Nothing on this computer has been changed.
    echo.
    pause
    exit /b 1
)

echo.
set /p confirm="Install this version? The database is backed up first. [y/N] "
if /i not "%confirm%"=="y" (
    echo Nothing was changed.
    pause
    exit /b 0
)

echo.
bokkie-win-amd64.exe update "%~dp0"
if errorlevel 1 (
    echo.
    echo The update did not finish. Read the message above: if it says the
    echo previous version was put back, the station is working normally and
    echo this can wait for the developer.
    echo.
    pause
    exit /b 1
)

echo.
pause
