@echo off
rem Puts a Bokkie shortcut on this computer's desktop. Double-click it.
rem
rem Run this on each counter PC. It needs no administrator rights: it only
rem writes a shortcut to the desktop of whoever runs it.
rem
rem The shortcut opens Edge in application mode — no address bar, no tabs — so
rem staff get something that looks and behaves like a desktop program rather
rem than a web page they might navigate away from.
title Create Bokkie Shortcut
setlocal

set "EDGE=%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe"
if not exist "%EDGE%" set "EDGE=%ProgramFiles%\Microsoft\Edge\Application\msedge.exe"
if not exist "%EDGE%" (
    echo.
    echo Could not find Microsoft Edge in the usual place.
    echo Send the developer this message.
    echo.
    pause
    exit /b 1
)

set "ADDRESS=http://bokkie.local:8080"
if not "%~1"=="" set "ADDRESS=%~1"

powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$s = (New-Object -ComObject WScript.Shell).CreateShortcut([Environment]::GetFolderPath('Desktop') + '\Bokkie.lnk');" ^
  "$s.TargetPath = '%EDGE%';" ^
  "$s.Arguments = '--app=%ADDRESS%';" ^
  "$s.Description = 'TLS Palay and Corn lending system';" ^
  "$s.Save()"

if errorlevel 1 (
    echo.
    echo The shortcut could not be created.
    echo.
    pause
    exit /b 1
)

echo.
echo A "Bokkie" shortcut is now on this computer's desktop.
echo.
echo If opening it shows "cannot reach this page", the host computer is either
echo switched off or the name bokkie.local is not resolving. Ask the developer
echo for the host's address and run this file again with it, for example:
echo     "Create Bokkie Shortcut.cmd" http://192.168.1.50:8080
echo.
pause
