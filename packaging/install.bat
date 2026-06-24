@echo off
setlocal enabledelayedexpansion

:: Get the directory of the script
set "SCRIPT_DIR=%~dp0"
:: Remove trailing backslash
set "SCRIPT_DIR=%SCRIPT_DIR:~0,-1%"

:: Define target paths
set "TARGET_DIR=%LOCALAPPDATA%\exteditor-nmh"
set "TARGET_JSON=%TARGET_DIR%\exteditor.json"
set "TARGET_EXE=%TARGET_DIR%\exteditor.exe"

:: Check if already installed
if exist "%TARGET_JSON%" (
    echo Error: exteditor.json already exists at %TARGET_JSON%
    echo Please run uninstall.bat first, and then run install.bat again.
    exit /b 1
)
if exist "%TARGET_EXE%" (
    echo Error: exteditor.exe already exists at %TARGET_EXE%
    echo Please run uninstall.bat first, and then run install.bat again.
    exit /b 1
)

:: Check if the binary file exists
set "SRC_EXE=.\exteditor.exe"
if not exist "%SRC_EXE%" (
    echo Error: Native messaging host binary not found at %SRC_EXE%
    echo Please ensure the project is built first.
    exit /b 1
)

:: Create directories if they do not exist
if not exist "%TARGET_DIR%" (
    mkdir "%TARGET_DIR%"
)

copy "%SRC_EXE%" "%TARGET_EXE%" > nul

:: Replace @@NATIVE_PATH@@ in json.in template
:: Uses PowerShell to escape backslashes correctly for JSON layout
set "SRC_JSON_IN=.\exteditor.json.in"
powershell -NoProfile -Command ^
    "$path = '%TARGET_EXE%'.Replace('\', '\\'); ^
     (Get-Content '%SRC_JSON_IN%') -replace '@@NATIVE_PATH@@', $path | Set-Content -Path '%TARGET_JSON%' -Encoding utf8"

:: Register in Registry
reg add "HKCU\Software\Mozilla\NativeMessagingHosts\exteditor" /ve /t REG_SZ /d "%TARGET_JSON%" /f > nul
if %errorlevel% neq 0 (
    echo Error: Failed to add registry key.
    exit /b 1
)

echo Installation completed successfully.
echo Config: %TARGET_JSON%
echo Binary: %TARGET_EXE%
endlocal
