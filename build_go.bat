@echo off
chcp 65001 >nul
cd /d "%~dp0"

echo ========================================
echo   QuotaBall - Go 构建工具
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
go build -tags production -trimpath -ldflags "-H=windowsgui -s -w" -o dist\QuotaBall.exe .\cmd\quotaball
if %errorlevel% neq 0 (
    echo [ERROR] 构建失败
    pause
    exit /b 1
)

copy /Y "dist\QuotaBall.exe" "%USERPROFILE%\Desktop\QuotaBall.exe" >nul 2>&1

echo.
echo ========================================
echo   构建完成
echo   EXE: dist\QuotaBall.exe
echo   桌面副本: QuotaBall.exe
echo ========================================
echo.
pause
