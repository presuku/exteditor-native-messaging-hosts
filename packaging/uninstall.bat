@echo off
setlocal

set "TARGET_DIR=%LOCALAPPDATA%\exteditor-nmh"

echo Uninstalling exteditor...

:: Remove Registry key
reg delete "HKCU\Software\Mozilla\NativeMessagingHosts\exteditor" /f > nul 2>&1
if %errorlevel% equ 0 (
    echo Removed registry key.
)

:: Remove files & directory
if exist "%TARGET_DIR%" (
    rd /s /q "%TARGET_DIR%"
    echo Removed directory %TARGET_DIR%.
)

echo Uninstallation completed successfully.
endlocal
