@echo off
setlocal EnableDelayedExpansion

echo ============================================
echo   qwen2api Build Script (Windows)
echo ============================================
echo.

REM Check if Go is installed
where go >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Go is not installed or not in PATH.
    echo Download from: https://go.dev/dl/
    pause
    exit /b 1
)

for /f "tokens=3" %%i in ('go version') do set GO_VER=%%i
echo [OK] Go found: %GO_VER%
echo.

REM Navigate to project root
cd /d "%~dp0"

REM Download dependencies
echo [1/3] Downloading dependencies...
go mod tidy
if %ERRORLEVEL% neq 0 (
    echo [ERROR] go mod tidy failed
    pause
    exit /b 1
)
echo [OK] Dependencies downloaded
echo.

REM Build
echo [2/3] Building qwen2api.exe...
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=1
go build -ldflags="-s -w" -o qwen2api.exe ./cmd/qwen2api/
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Build failed
    pause
    exit /b 1
)
echo [OK] Build successful: qwen2api.exe
echo.

REM Optional: run
echo [3/3] Done!
echo.
echo To run:  .\qwen2api.exe
echo To test: curl http://localhost:5001/healthz
echo.

set /p RUN_NOW="Run now? (y/N): "
if /i "%RUN_NOW%"=="y" (
    echo Starting qwen2api...
    .\qwen2api.exe
)

pause
