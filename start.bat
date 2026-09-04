@echo off
REM Ein Doppelklick - SpeedNAS laeuft.
REM Alle Befehle:  start.bat status   /   start.bat stop   /   start.bat logs
setlocal
cd /d "%~dp0"
where pwsh >nul 2>&1
if %errorlevel%==0 (
    pwsh -NoProfile -ExecutionPolicy Bypass -File "scripts\speednas.ps1" %*
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -File "scripts\speednas.ps1" %*
)
if errorlevel 1 (
    echo.
    echo Es ist ein Fehler aufgetreten. Fenster bleibt offen.
    pause
)
