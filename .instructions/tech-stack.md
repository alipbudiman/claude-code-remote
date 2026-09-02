# Tech Stack & System Architecture — Claude Code Remote Monitor

> **Project**: Claude Code Remote Session & Sub-Agent Monitor  
> **Repository**: `claude-status-apk` (`alipbudiman/claude-code-remote`)  
> **Target Platforms**: Windows/Linux/macOS (Go Desktop Server) & Android 7.0+ (Native APK / PWA)  
> **Network Mode**: 100% Local LAN / Wi-Fi (`0.0.0.0:9280`), Zero Internet Required  

---

## 1. System Overview & Architecture

Claude Code Remote Monitor is a real-time local monitoring and alerting system for [Claude Code CLI](https://github.com/anthropics/claude-code). It allows developers to monitor long-running tasks, tool invocations, active sub-agents, permission requests, and plan approvals (`ExitPlanMode`) directly on an Android device over the local Wi-Fi network.

### High-Level Data Flow

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           HOST MACHINE (PC)                             │
│                                                                         │
│  ┌───────────────────────┐                                              │
│  │    Claude Code CLI    │                                              │
│  │ (Agent executing task)│                                              │
│  └───────────┬───────────┘                                              │
│              │ hook trigger (stdin JSON)                                │
│              ▼                                                          │
│  ┌───────────────────────┐         ┌─────────────────────────────────┐  │
│  │ ~/.claude/            │         │ ~/.claude/projects/*.jsonl      │  │
│  │ claude-remote-hook.js │         │ (Transcript files - redundancy) │  │
│  └───────────┬───────────┘         └────────────────┬────────────────┘  │
│              │ POST /api/hook (HTTP)                │ poll (2s)         │
│              ▼                                      ▼                   │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │            Go Desktop Server (claude-remote-server.exe)           │  │
│  │                                                                   │  │
│  │  • Router: net/http ServeMux (0.0.0.0:9280)                       │  │
│  │  • State Store: In-memory RWMutex synchronized state              │  │
│  │  • Liveness Engine: 1s fallback ticker; event-driven turn machine  │  │
│  │  • Auto-Hook Installer: Registers 16 events in settings.json       │  │
│  │  • QR Generator: skip2/go-qrcode (Terminal ASCII + PNG /api/qr)   │  │
│  │  • Web UI: Embedded HTML via go:embed                             │  │
│  │  • WebSocket Hub: gorilla/websocket broadcast channel             │  │
│  └───────────────────────────────────┬───────────────────────────────┘  │
└──────────────────────────────────────┼──────────────────────────────────┘
                                       │
                      Local LAN / Wi-Fi (WebSocket + HTTP)
                      Port: 9280 (No Cloud / No Internet)
                                       │
┌──────────────────────────────────────┼──────────────────────────────────┐
│                                      ▼                                  │
│                       ANDROID DEVICE (Native APK / PWA)                 │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │ Android Native Container (MainActivity.kt + Kotlin)               │  │
│  │                                                                   │  │
│  │  • WebView with WebViewAssetLoader (appassets.androidplatform.net)│  │
│  │  • AndroidBridge (@JavascriptInterface)                           │  │
│  │  • NotificationHelper (Ongoing progress + Heads-up alerts)        │  │
│  │  • BatteryOptimizationHelper (Unrestricted background guide)      │  │
│  │  • Chrome Remote Desktop quick-intent launcher                    │  │
│  │                                                                   │  │
│  │  ┌─────────────────────────────────────────────────────────────┐  │  │
│  │  │ React 18 + TypeScript + Vite + TailwindCSS Frontend         │  │  │
│  │  │                                                             │  │  │
│  │  │  • StatusHero (Dynamic SVG: claudecode-color vs mono)       │  │  │
│  │  │  • QuestionPromptBanner (Golden callout for user questions) │  │  │
│  │  │  • LiveStreamBar (Floating mini player / drawer)            │  │  │
│  │  │  • SubagentInspector (Live subagent list + history)         │  │  │
│  │  │  • ChromeRemoteButton (Direct access to PC desktop)         │  │  │
│  │  │  • ActivityLogs (Live chronological execution feed)         │  │  │
│  │  │  • Audio Chimes (Web Audio API) & Haptic Vibration          │  │  │
│  │  └─────────────────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Technology Stack Matrix

| Layer | Technology | Version | Purpose / Responsibilities |
|---|---|---|---|
| **Server Runtime** | Go | `1.25.0+` | Core desktop executable server (`claude-remote-server.exe`), fast startup, zero runtime dependencies |
| **WebSocket** | `github.com/gorilla/websocket` | `v1.5.3` | Low-latency duplex stream between Go server and Android APK/web clients |
| **QR Code Engine** | `github.com/skip2/go-qrcode` | `v0.0.0-20200617` | Terminal ASCII QR code rendering and PNG endpoint (`/api/qr`) |
| **Mobile Frontend** | React | `^18.3.1` | Single-page UI for Android WebView & responsive mobile browsers |
| **Language (Web)** | TypeScript | `^5.5.3` | Type safety and schema synchronization with Go models |
| **Bundler / Build** | Vite | `^5.4.2` | Ultra-fast client bundler and asset generation |
| **Styling** | TailwindCSS | `^3.4.10` | Utility-first dark-themed mobile styling (`#0d0e12` palette) |
| **Icons** | Lucide React | `^0.475.0` | UI iconography for battery, wifi, settings, terminal, etc. |
| **Custom Icons** | SVG Assets | Custom | `claudecode-color.svg` (Active/Glow) & `claudecode.svg` (Idle) |
| **Mobile OS Wrapper** | Android Native (Kotlin) | `1.9.24` / AGP `8.4.2` | Host Android container, background notifications, battery management |
| **Target Android SDK** | Android SDK | `compileSdk 34`, `minSdk 24` | Compatible with Android 7.0 (Nougat) up to Android 14+ |
| **Build System** | Gradle | `8.6` (pinned wrapper) | Android APK compilation (`gradle assembleDebug`) |
| **CI/CD** | GitHub Actions | `ubuntu-latest` | Automated APK release builds and multi-platform Go binary compilation |

---

## 3. Backend Architecture (Go Server)

### Directory & Package Structure

```
cmd/server/
└── main.go                    # Server entry point: flag parsing, lifecycle management, banner & QR output
internal/
├── api/
│   └── server.go              # HTTP routes, CORS handling, WebSocket hub, REST endpoints, static embed
├── hooks/
│   ├── installer.go           # Automatic installer for ~/.claude/settings.json and hook bridge script
│   └── parser.go              # Tool mapper (transforms raw tool input into human-readable action summaries)
├── models/
│   └── models.go              # Core data models: Session, Subagent, HookPayload, Notification, Snapshot
├── network/
│   └── lan.go                 # Local non-loopback IP discovery and QR code generators (ASCII & PNG)
├── state/
│   └── store.go               # Thread-safe in-memory store, event dispatcher, 1s liveness & auto-idle engine
└── watcher/
    └── filewatcher.go         # JSONL transcript file watcher (redundancy source for ~/.claude/projects)
web/
├── index.html                 # Fallback web dashboard embedded into Go binary
└── web.go                     # go:embed index.html wrapper
```

### Key Subsystems

#### 1. State Store & Liveness Engine (`internal/state/store.go`)
- **Concurrency**: Protected by `sync.RWMutex`.
- **Session Tracking**: Sessions are indexed by `session_id`. Each session tracks status (`active`, `idle`, `waiting_permission`, `subagent_running`, `completed`), `current_tool_status`, `turn_active` (true from `UserPromptSubmit`/`SessionStart`/any `PreToolUse` until `Stop`), `active_tool_ids` (in-flight tool_use ids), `active_subagents` map, `subagent_history` slice, and a circular buffer of up to 50 recent activity logs.
- **Turn State Machine (event-driven, primary)**: `UserPromptSubmit` opens a turn (`active`, "Working on your prompt…", no notification). `PreToolUse` registers the `tool_use_id` in `ActiveToolIDs`; `PostToolUse`/`PostToolUseFailure` retire it, clear a matching `PendingQuestion` (identified by `ToolUseID`), and — with the turn still open — restore the session to `active` / "Processing results". `Stop` closes the turn (`turn_active=false`, tools cleared, all subagents completed, `idle`, one `task_done` notification). Subagents are keyed by `agent_id` (fallback `tool_use_id`); `SubagentStop` completes exactly the matched agent, never all of them.
- **Liveness Engine (`StartLivenessWatcher`)**: Runs a `time.Ticker(1 * time.Second)` background goroutine. It is a **fallback only**, configurable via `-idle-timeout` (default `300s`, `time.ParseDuration`). A working session is marked stalled ONLY when it has **no in-flight tools** (`ActiveToolIDs` empty — in-flight tools are never auto-idled), **no pending question**, and has been silent for the whole idle timeout:
  - All active sub-agents are marked `completed`.
  - The session status transitions to `idle` ("Idling (Ready for prompt)") — the log says "No events for Nm — fell back to idle".
  - Broadcasts a `session_update` WebSocket message.
  - Generates ONE `stalled` notification ("⚠️ No Events for Nm") per idle episode (anti-flap: re-armed only when new activity arrives). It is NEVER a `task_done` / "Task Completed" — long tools no longer produce false completions.
- **Watcher redundancy**: JSONL-watcher payloads carry `Source: "watcher"`; replayed `tool_use_id`s (per-session LRU, cap 500) are dropped, and watcher events never raise notifications — state backfill only. The watcher skips first-seen transcripts older than 10 minutes (modtime) and re-reads from offset 0 on truncation.
- **Session hygiene**: questions unanswered for 30 min are downgraded (`idle` + `pending_question.stale=true`); sessions silent for 24h are evicted from memory and the snapshot; the snapshot lists at most the 20 most recent sessions (most-recent-activity first) and `ActiveSession` falls back deterministically to the most recent activity.

#### 2. Auto-Hook Installer (`internal/hooks/installer.go`)
- Creates `~/.claude/claude-remote-hook.js` (Node.js bridge script; feed mode = 1.5s timeout + silent spool failover, `--decide` mode = 110s long-poll that forwards decision JSON on stdout).
- Modifies `~/.claude/settings.json` to register hook handlers for **16 Claude Code events** (stale copies of our own bridge are removed on upgrade; foreign hooks preserved):
  1. `SessionStart`
  2. `SessionEnd`
  3. `UserPromptSubmit` (turn start)
  4. `Stop` *(decide, timeout 30 — delivers queued prompts via `{"decision":"block","reason":…}`, guarded by `stop_hook_active`)*
  5. `StopFailure`
  6. `PermissionRequest` *(decide, timeout 120 — parks a remote approval)*
  7. `PermissionDenied`
  8. `Notification`
  9. `PreToolUse` *(decide, timeout 120 — answers AskUserQuestion/ExitPlanMode)*
  10. `PostToolUse`
  11. `PostToolUseFailure`
  12. `SubagentStart`
  13. `SubagentStop`
  14. `TeammateIdle`
  15. `TaskCompleted`
  16. `PreCompact`

**Hook decision contracts** (see `internal/api/decide.go`): PermissionRequest → `hookSpecificOutput.decision{behavior:allow|deny[, updatedPermissions]}`; AskUserQuestion → `permissionDecision:"allow"` + `updatedInput{questions:<echo>, answers:{q→a}}`; ExitPlanMode → plain allow / deny+reason; Stop → top-level `{"decision":"block","reason":<prompt>}`. Timeout layering: server wait (15–110s, default 60) < bridge 110s < settings.json entry 120s — a lost phone always degrades to the normal terminal flow.

#### 3. Tool Status Parser (`internal/hooks/parser.go`)
Translates tool names and parameters into short, meaningful status strings (max 60 characters):
- `Read` / `ReadFile` $\rightarrow$ `"Reading <filename>"`
- `Edit` / `replace_file_content` $\rightarrow$ `"Editing <filename>"`
- `Write` / `write_to_file` $\rightarrow$ `"Writing <filename>"`
- `Bash` / `run_command` $\rightarrow$ `"Running: <cmd>"`
- `Glob` $\rightarrow$ `"Finding files: <pattern>"`
- `Grep` / `grep_search` $\rightarrow$ `"Searching code: <query>"`
- `WebFetch` / `WebSearch` $\rightarrow$ `"Fetching/Searching <query>"`
- `Task` / `Agent` / `invoke_subagent` $\rightarrow$ `"Subtask: <description>"`
- `AskUserQuestion` / `ask_question` $\rightarrow$ `"Waiting for user input"`
- `EnterPlanMode` $\rightarrow$ `"Planning task steps"`
- `ExitPlanMode` $\rightarrow$ `"Plan ready, waiting for review (ExitPlanMode)"`

---

## 4. API Specification & Protocols

### HTTP REST Endpoints

| Method | Path | Description | Response / Body |
|---|---|---|---|
| `POST` | `/api/hook` | Hook ingestion endpoint from `claude-remote-hook.js`. With `?decide=1` (bridge `--decide` entries): decision events LONG-POLL — the response body is the Claude Code hook JSON (`hookSpecificOutput` / `decision`) once the phone answers or the approval wait expires (then `{"status":"ok"}`) | JSON `HookPayload` → `{"status":"ok"}` or hook decision JSON |
| `GET` | `/ws` | WebSocket real-time connection (now bidirectional — see client commands) | Upgrades to WebSocket connection |
| `GET` | `/api/status` | Full system state snapshot (now incl. `pending_decisions`, `recent_process_events`, `app_settings`) | `ServerStateSnapshot` JSON |
| `GET` | `/api/sessions` | List of all recorded sessions | Array of `Session` JSON |
| `GET` | `/api/subagents` | List of all active and historical subagents | Array of `Subagent` JSON |
| `GET` | `/api/process?session_id=&after=` | Live process feed events (ID > after, max 200) | `{"events":[ProcessEvent]}` |
| `POST` | `/api/decision` | Resolve a pending decision `{decision_id, action, answer?, notes?, suggestion_index?}` | `{"status":"ok"}` / 404 |
| `POST` | `/api/prompt` | Queue a mid-task prompt `{session_id, text}` | `{"status":"ok"}` |
| `POST` | `/api/logs/clear` | Manual clear of notifications + logs + process events | `{"status":"ok"}` |
| `GET`/`POST` | `/api/settings` | App settings (`approval_wait_s` 15–110, `log_autoclear_min` 0/5/15/30), persisted in `~/.claude/claude-remote-settings.json` | `AppSettings` JSON |
| `GET`/`POST` | `/api/permissions` | Read/write the `permissions` section of `~/.claude/settings.json` (preserves all other keys; validates `defaultMode` + array fields) | `{"permissions":{...}}` |
| `GET` | `/api/qr` | QR Code image for current primary LAN address | `image/png` (256x256) |
| `POST` | `/api/install-hooks`| Re-install / repair hook configuration | `{"success":true,"message":"..."}` |
| `GET` | `/` | Static embedded web UI dashboard | `text/html; charset=utf-8` |

### WebSocket Protocol (`/ws`)

Upon connection, the server immediately sends an `initial_state` message containing `ServerStateSnapshot`. Subsequent updates are pushed in real-time.

```json
{
  "type": "initial_state | session_update | subagent_update | notification | stats | process_event | process_sync | decision_pending | decision_resolved | prompt_queued | logs_cleared | app_settings | permissions",
  "data": { ... },
  "timestamp": "2026-08-31T12:00:00Z"
}
```

#### Message Types:
- `initial_state`: Complete state snapshot on connection.
- `session_update`: Pushed whenever session state, current tool, or status changes.
- `subagent_update`: Pushed when a sub-agent's internal tool changes.
- `notification`: Triggered for user questions, permissions, sub-agents, or completion.
- `process_event`: One live-feed entry (kind ∈ user_prompt/thinking/text/tool_use/tool_result/tool_error/turn_end, Detail capped at 8 KB, ring 200/session).
- `process_sync`: Bulk event tail (reply to the `process_sync` client command).
- `decision_pending` / `decision_resolved`: A remote decision was parked / answered (or expired).
- `prompt_queued`: A phone prompt entered the queue (depth included).
- `logs_cleared` / `app_settings` / `permissions`: state changes from the commands below.

#### Client→Server Commands (bidirectional /ws):

```json
{"type":"client_command","data":{"op":"…"},"timestamp":"…"}
```
Ops: `decision` · `prompt` · `clear_logs` · `app_settings` · `permissions_get` · `permissions_set` · `process_sync`. In relay mode this is the ONLY command path — the relay forwards frames verbatim (`relayclient.OnClientFrame` → the same dispatcher as the `/ws` read loop).

---

## 5. Mobile Application (React + Android Native)

### Component Hierarchy (`mobile/src/`)

```
App.tsx (Root State Orchestrator & Notification Coordinator)
├── Header.tsx (WiFi Indicator, IP display, Notification Bell, Settings trigger)
├── BatteryBanner.tsx (Unrestricted background warning & intent trigger)
├── QuestionPromptBanner.tsx (Golden banner for pending question / permission / plan approval)
├── StatusHero.tsx (Dynamic SVG: claudecode-color.svg vs claudecode.svg + Glow/Pulse + Badge)
├── ChromeRemoteButton.tsx (One-tap launcher for Chrome Remote Desktop app / web)
├── SubagentInspector.tsx (Collapsible live sub-agent monitor + completed history)
├── ActivityLogs.tsx (Scrollable feed of recent events and tool actions)
├── LiveStreamBar.tsx (Bottom floating mini-player bar + expand drawer)
└── ConnectionModal.tsx (Server IP selector & custom URL configurator)
```

### Dynamic Visual Status Rules

- **Active / Working**: Status is `active`, `subagent_running`, or executing tools.
  - Icon: `claudecode-color.svg` (Full color with animated bouncing glow).
  - Badge: Emerald / Cyan pulse indicator.
- **Idling**: Status is `idle` or `completed`.
  - Icon: `claudecode.svg` (Monochrome slate).
  - Badge: Slate badge "Ready for prompt".
- **Waiting Input / Permission**: Status is `waiting_permission` or `pending_question` is non-null.
  - Banner: Bright gold border and callout.
  - Audio: Urgent 2-tone prompt chime (A4 $\rightarrow$ E5).
  - Vibration: Pattern `[200, 100, 200, 100, 400]` ms.

### Android Native Bridge (`MainActivity.kt` $\leftrightarrow$ `AndroidBridge`)

| JavaScript Call (`window.AndroidBridge.*`) | Native Kotlin Implementation | Purpose |
|---|---|---|
| `showNotification(title, msg, type)` | `NotificationHelper.showPopupAlertNotification` | Dispatches high-priority heads-up notification with sound & vibration |
| `updateOngoingNotification(...)` | `NotificationHelper.updateOngoingProgressNotification` | Updates persistent notification in Android notification shade |
| `openChromeRemoteDesktop()` | `NotificationHelper.launchChromeRemoteDesktop` | Launches `com.google.chromeremotedesktop` package or opens browser fallback |
| `isNativeAndroid()` | Returns `true` | Tells frontend it is running inside native Android APK |
| `isBatteryUnrestricted()` | `BatteryOptimizationHelper.isIgnoringBatteryOptimizations` | Checks Android `PowerManager` exemption |
| `openBatterySettings()` | `BatteryOptimizationHelper.openBatteryOptimizationSettings` | Opens OS settings to whitelist app from Doze mode |

---

## 6. Security, Networking & Offline Execution

1. **Local LAN Isolation**:
   - Server listens on `0.0.0.0:9280`.
   - Accessible only by devices on the same local subnet / Wi-Fi network.
   - Zero outbound cloud connectivity required for telemetry or monitoring.
2. **Cleartext Traffic Policy**:
   - Android `network_security_config.xml` explicitly permits cleartext HTTP (`http://*`) and WebSocket (`ws://*`) communication on local private IP ranges (`192.168.x.x`, `10.x.x.x`, `172.16-31.x.x`, `127.0.0.1`).
3. **CORS Configuration**:
   - `Access-Control-Allow-Origin: *` configured on all `/api/*` endpoints to allow local development, APK WebView, and browser access.
4. **Hook Non-Blocking Safety**:
   - `claude-remote-hook.js` uses a strict 1500ms timeout and catches all socket errors to ensure Claude Code CLI execution is **never blocked or crashed** even if the Go server is stopped.

---

## 7. Build, Test & Development Workflow

### Quick Commands

#### 1. Compile Go Server (Windows EXE)
```cmd
scripts\build-go.bat
```
Or directly with Go CLI:
```bash
go build -ldflags="-s -w" -o bin/claude-remote-server.exe ./cmd/server
```

#### 2. Run Go Server
```cmd
scripts\run-server.bat
```

#### 3. Build Mobile Frontend (Vite)
```bash
cd mobile
npm install
npm run build
```

#### 4. Build Android APK (Local Gradle)
```bash
# Copy web dist to Android assets
mkdir -p mobile/android/app/src/main/assets
cp -r mobile/dist/* mobile/android/app/src/main/assets/

# Build APK
cd mobile/android
gradle assembleDebug
```
Output: `mobile/android/app/build/outputs/apk/debug/app-debug.apk`

#### 5. Simulate Mock Claude Code Events (PowerShell)
```powershell
powershell -ExecutionPolicy Bypass -File scripts\test-mock-hook.ps1
```
Tests the full lifecycle: `SessionStart` $\rightarrow$ `PreToolUse` (Edit/Bash) $\rightarrow$ `AskUserQuestion` $\rightarrow$ `SubagentStart` $\rightarrow$ `SubagentStop` $\rightarrow$ `Stop` (Task Completed).

---

## 8. Conventions & Coding Rules

1. **Go Codebase**:
   - Standard Go project layout (`cmd/`, `internal/`, `web/`).
   - Use standard library wherever feasible (`net/http`, `sync`, `os`, `encoding/json`).
   - Always guard shared state access with `sync.RWMutex`.
   - Never introduce heavy external frameworks (e.g. Gin, Fiber, Echo) unless explicitly approved.
2. **React / Mobile Frontend**:
   - Functional React components with TypeScript strict types.
   - Clean separation of UI components (`mobile/src/components/`) and transport services (`mobile/src/services/`).
   - Keep styles consistent with the dark theme palette (`#0d0e12`, `#161922`, Anthropic Coral `#D97757`).
3. **Android Native**:
   - Keep WebView container lightweight and clean.
   - Maintain backwards compatibility down to Android 7.0 (`minSdk 24`).
   - Use `WebViewAssetLoader` for local asset loading to avoid CORS/origin issues.
