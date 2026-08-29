@echo off
echo ===================================================
echo   COMPILING CLAUDE REMOTE SERVER (GO EXE)
echo ===================================================

if not exist bin mkdir bin

echo Compiling ./cmd/server to bin/claude-remote-server.exe...
go build -ldflags="-s -w" -o bin/claude-remote-server.exe ./cmd/server

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Compilation failed!
    exit /b %ERRORLEVEL%
)

echo [SUCCESS] Binary created at bin\claude-remote-server.exe
echo.
pause
