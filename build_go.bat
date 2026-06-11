@echo off
chcp 65001 >nul
cd /d "%~dp0"

echo ========================================
echo   Krill AI 额度监控 - Go 构建工具
echo ========================================
echo.

go version >nul 2>&1
if %errorlevel% neq 0 (
    echo [ERROR] 未找到 Go，请先安装 Go 1.25+
    pause
    exit /b 1
)

echo [1/2] 运行测试...
go test ./...
if %errorlevel% neq 0 (
    echo [ERROR] 测试失败
    pause
    exit /b 1
)

echo [2/2] 构建 Wails Windows GUI EXE...
if not exist dist mkdir dist
go build -tags production -trimpath -ldflags "-H=windowsgui -s -w" -o dist\Krill-Monitor-Go.exe .\cmd\krill-monitor
if %errorlevel% neq 0 (
    echo [ERROR] 构建失败
    pause
    exit /b 1
)

copy /Y "dist\Krill-Monitor-Go.exe" "%USERPROFILE%\Desktop\Krill-Monitor-Go.exe" >nul 2>&1

echo.
echo ========================================
echo   构建完成
echo   EXE: dist\Krill-Monitor-Go.exe
echo   桌面副本: Krill-Monitor-Go.exe
echo ========================================
echo.
pause
