param(
    [int]$Port = 9280
)

# PowerShell script to test sending mock hook events to local server

# All /api/* endpoints require the shared-secret token (see internal/auth).
$tokenFile = Join-Path $env:USERPROFILE ".claude\claude-remote-token"
$token = ""
try { $token = (Get-Content $tokenFile -Raw).Trim() } catch { $token = "" }
if (-not $token) {
    Write-Warning "Token file not found or empty at $tokenFile - requests will get 401 Unauthorized. Start the server once to generate it."
}
$headers = @{ Authorization = "Bearer $token" }

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

Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/hook" -Method Post -Body $payload1 -ContentType "application/json" -Headers $headers
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

Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/hook" -Method Post -Body $payload2 -ContentType "application/json" -Headers $headers
Start-Sleep -Seconds 3

Write-Host "3. Testing Turn Stop / Idling Event..." -ForegroundColor Green
$payload3 = @{
    hook_event_name = "Stop"
    session_id = "test-session-1"
    cwd = "d:\CODING\claude-status-apk"
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/hook" -Method Post -Body $payload3 -ContentType "application/json" -Headers $headers

# ---------------------------------------------------------------------------
# Remote-interaction flows (2026-09-02): decision long-poll + prompt queue.
# These mirror exactly what the --decide bridge entries send, then answer from
# the "phone" side while the hook request is parked.
# ---------------------------------------------------------------------------
$base = "http://127.0.0.1:$Port"

Write-Host "`n4. Decision flow: remote permission approval..." -ForegroundColor Cyan
# Resolve the parked decision id once it exists, then approve it as the phone.
$decJob = Start-Job -ScriptBlock {
    param($base, $token)
    for ($i = 0; $i -lt 100; $i++) {
        Start-Sleep -Milliseconds 200
        try {
            $snap = Invoke-RestMethod -Uri "$base/api/status?token=$token" -Method Get
            if ($snap.pending_decisions -and $snap.pending_decisions.Count -gt 0) {
                $id = $snap.pending_decisions[0].id
                Invoke-RestMethod -Uri "$base/api/decision?token=$token" -Method Post `
                    -ContentType "application/json" `
                    -Body (@{ decision_id = $id; action = "allow" } | ConvertTo-Json) | Out-Null
                return $id
            }
        } catch { }
    }
    return $null
} -ArgumentList $base, $token

$permBody = @{
    hook_event_name = "PermissionRequest"
    session_id = "test-session-1"
    permission_mode = "default"
    tool_name = "Bash"
    tool_input = @{ command = "npm run build" }
} | ConvertTo-Json
$resp = Invoke-RestMethod -Uri "$base/api/hook?decide=1&token=$token" -Method Post `
    -Body $permBody -ContentType "application/json" -Headers $headers -TimeoutSec 120
$respJson = $resp | ConvertTo-Json -Depth 10 -Compress
Write-Host "Hook decision response: $respJson"
if ($respJson -match 'hookSpecificOutput|"decision"') {
    Write-Host "PASS: decision JSON returned from long-poll" -ForegroundColor Green
} else {
    Write-Host "FAIL: no decision JSON from long-poll" -ForegroundColor Red
}
$decId = Receive-Job -Job $decJob -Wait
Remove-Job $decJob -Force
Write-Host "Phone side resolved decision: $decId"

Write-Host "`n5. Prompt injection: queue + Stop delivery..." -ForegroundColor Cyan
Invoke-RestMethod -Uri "$base/api/prompt?token=$token" -Method Post `
    -ContentType "application/json" `
    -Body (@{ session_id = "test-session-1"; text = "now run the tests" } | ConvertTo-Json) | Out-Null
$stopBody = @{
    hook_event_name = "Stop"
    session_id = "test-session-1"
    stop_hook_active = $false
} | ConvertTo-Json
$resp2 = Invoke-RestMethod -Uri "$base/api/hook?decide=1&token=$token" -Method Post `
    -Body $stopBody -ContentType "application/json" -Headers $headers -TimeoutSec 30
$resp2Json = $resp2 | ConvertTo-Json -Depth 10 -Compress
if ($resp2Json -match '"decision":"block"' -and $resp2Json -match 'now run the tests') {
    Write-Host "PASS: queued prompt delivered via Stop block" -ForegroundColor Green
} else {
    Write-Host "FAIL: expected block+reason, got: $resp2Json" -ForegroundColor Red
}

Write-Host "`n6. Process feed via /api/process..." -ForegroundColor Cyan
$events = Invoke-RestMethod -Uri "$base/api/process?token=$token&session_id=test-session-1" -Method Get
$kinds = ($events.events | ForEach-Object { $_.kind }) -join ","
Write-Host "Event kinds: $kinds"
if ($kinds -match 'tool_use') {
    Write-Host "PASS: process feed populated" -ForegroundColor Green
} else {
    Write-Host "FAIL: no tool_use events in feed" -ForegroundColor Red
}

Write-Host "`nAll test events dispatched successfully! Check mobile/web dashboard." -ForegroundColor Green
