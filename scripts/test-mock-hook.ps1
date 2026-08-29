# PowerShell script to test sending mock hook events to local server

Write-Host "1. Testing Task/Tool Start Event..." -ForegroundColor Cyan
$payload1 = @{
    hook_event_name = "PreToolUse"
    session_id = "test-session-1"
    cwd = "d:\CODING\claude-status-apk"
    tool_name = "Edit"
    tool_use_id = "tool-001"
    tool_input = @{
        file_path = "internal/api/server.go"
    }
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://127.0.0.1:9280/api/hook" -Method Post -Body $payload1 -ContentType "application/json"
Start-Sleep -Seconds 2

Write-Host "2. Testing Sub-Agent Spawn Event..." -ForegroundColor Yellow
$payload2 = @{
    hook_event_name = "SubagentStart"
    session_id = "test-session-1"
    cwd = "d:\CODING\claude-status-apk"
    agent_type = "Bug-Fixer-Agent"
    description = "Optimizing WebSocket memory footprint"
    tool_name = "Bash"
    tool_use_id = "subagent-99"
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://127.0.0.1:9280/api/hook" -Method Post -Body $payload2 -ContentType "application/json"
Start-Sleep -Seconds 3

Write-Host "3. Testing Turn Stop / Idling Event..." -ForegroundColor Green
$payload3 = @{
    hook_event_name = "Stop"
    session_id = "test-session-1"
    cwd = "d:\CODING\claude-status-apk"
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://127.0.0.1:9280/api/hook" -Method Post -Body $payload3 -ContentType "application/json"

Write-Host "`nAll test events dispatched successfully! Check mobile/web dashboard." -ForegroundColor Green
