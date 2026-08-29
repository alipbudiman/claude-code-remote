@echo off
echo Starting Claude Code Remote Session Server...
if not exist bin\claude-remote-server.exe (
    echo Binary not found. Compiling first...
    go build -o bin\claude-remote-server.exe ./cmd/server
)

bin\claude-remote-server.exe -port 9280
pause
