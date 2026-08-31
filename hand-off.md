# Claude Code Remote Monitor — Hand-Off Document

> **Version**: 1.0.0  
> **Repository**: [github.com/alipbudiman/claude-code-remote](https://github.com/alipbudiman/claude-code-remote)  
> **Last Updated**: 2026-08-30  
> **Author**: alipbudiman (built with AI-assisted development)

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Architecture Diagram](#2-architecture-diagram)
3. [Technology Stack](#3-technology-stack)
4. [Directory Structure](#4-directory-structure)
5. [Backend — Go Server (Desktop EXE)](#5-backend--go-server-desktop-exe)
6. [Frontend — React/Vite Mobile UI](#6-frontend--reactvite-mobile-ui)
7. [Android Native Layer (Kotlin)](#7-android-native-layer-kotlin)
8. [Communication Pipeline](#8-communication-pipeline)
9. [Claude Code Integration (Hooks System)](#9-claude-code-integration-hooks-system)
10. [Notification System](#10-notification-system)
11. [State Management & Liveness Detection](#11-state-management--liveness-detection)
12. [CI/CD Pipeline (GitHub Actions)](#12-cicd-pipeline-github-actions)
13. [How to Build, Check & Validate](#13-how-to-build-check--validate)
14. [How to Deploy](#14-how-to-deploy)
15. [Algorithm & System Verification Checklist](#15-algorithm--system-verification-checklist)
16. [Known Limitations & Improvement Areas](#16-known-limitations--improvement-areas)
17. [Quick Reference Commands](#17-quick-reference-commands)

---

## 1. Project Overview

**Claude Code Remote Monitor** adalah sistem monitoring real-time yang memungkinkan pengguna memantau aktivitas sesi [Claude Code](https://github.com/anthropics/claude-code) dari perangkat Android melalui koneksi jaringan lokal (LAN/Wi-Fi). Sistem ini terdiri dari:

1. **Go Desktop Server (EXE)** — Berjalan di PC yang menjalankan Claude Code, menerima hook events dan membagikan status secara real-time via WebSocket.
2. **Android APK** — Menampilkan status sesi, sub-agent activity, notifikasi, dan menyediakan akses cepat ke Chrome Remote Desktop.

### Fitur Utama

- ✅ Real-time session status monitoring (Working / Idling / Waiting Input)
- ✅ Sub-agent activity inspector (live spawning & auto-cleanup setelah selesai)
- ✅ Push notification untuk pertanyaan, permission request, ExitPlanMode, dan task completion
- ✅ Floating stream bar (seperti music player notification di Android)
- ✅ Icon status: `claudecode-color.svg` (active) / `claudecode.svg` (idle) dengan animasi bounce
- ✅ Koneksi offline via Wi-Fi LAN (0.0.0.0), tanpa memerlukan internet
- ✅ Battery optimization guidance (unrestricted background access)
- ✅ Chrome Remote Desktop quick-launch integration
- ✅ QR Code scanning untuk koneksi cepat dari HP

---

## 2. Architecture Diagram

```
┌─────────────────────────────────────────────────────────┐
│                   DESKTOP PC (Windows/Mac/Linux)        │
│                                                         │
│  ┌──────────────────┐     ┌──────────────────────────┐  │
│  │  Claude Code CLI │────▶│  ~/.claude/settings.json │  │
│  │  (Running Agent)  │     │  (Hook Entries)          │  │
│  └──────────────────┘     └──────────┬───────────────┘  │
│                                      │                  │
│               Hook Events (stdin → node.js bridge)      │
│                                      │                  │
│                                      ▼                  │
│  ┌──────────────────────────────────────────────────┐   │
│  │       Go Server (claude-remote-server.exe)       │   │
│  │                                                  │   │
│  │  ┌────────────────┐  ┌───────────────────────┐   │   │
│  │  │ POST /api/hook │  │ JSONL File Watcher    │   │   │
│  │  │ (Hook Receiver)│  │ (~/.claude/projects/) │   │   │
│  │  └───────┬────────┘  └──────────┬────────────┘   │   │
│  │          │                      │                │   │
│  │          ▼                      ▼                │   │
│  │  ┌──────────────────────────────────────────┐    │   │
│  │  │         State Store (in-memory)          │    │   │
│  │  │  Sessions, Subagents, Notifications      │    │   │
│  │  │  Liveness Watcher (1s polling)           │    │   │
│  │  └────────────────┬─────────────────────────┘    │   │
│  │                   │                              │   │
│  │          ┌────────┴────────┐                     │   │
│  │          ▼                 ▼                     │   │
│  │  ┌──────────────┐  ┌───────────────┐            │   │
│  │  │  WebSocket   │  │  REST API     │            │   │
│  │  │  /ws         │  │  /api/status  │            │   │
│  │  │  (Real-time) │  │  /api/sessions│            │   │
│  │  └──────┬───────┘  └───────────────┘            │   │
│  └─────────┼───────────────────────────────────────┘   │
│            │                                           │
└────────────┼───────────────────────────────────────────┘
             │  Wi-Fi LAN (0.0.0.0:9280)
             │  (No internet required)
             ▼
┌─────────────────────────────────────────────────────────┐
│                  ANDROID DEVICE (APK)                   │
│                                                         │
│  ┌──────────────────────────────────────────────────┐   │
│  │  MainActivity.kt (WebView Container)             │   │
│  │  ┌────────────────────────────────────────────┐  │   │
│  │  │  React/Vite App (assets/index.html)        │  │   │
│  │  │                                            │  │   │
│  │  │  ┌──────────────┐  ┌────────────────────┐  │  │   │
│  │  │  │ WebSocket    │  │ Components:        │  │  │   │
│  │  │  │ Service      │  │ - StatusHero       │  │  │   │
│  │  │  │ (ws://...)   │  │ - LiveStreamBar    │  │  │   │
│  │  │  └──────┬───────┘  │ - SubagentInspect  │  │  │   │
│  │  │         │          │ - QuestionPrompt   │  │  │   │
│  │  │         ▼          │ - BatteryBanner    │  │  │   │
│  │  │  ┌──────────────┐  │ - ActivityLogs     │  │  │   │
│  │  │  │ AndroidBridge│  │ - ChromeRemoteBtn  │  │  │   │
│  │  │  │ (JS↔Kotlin)  │  └────────────────────┘  │  │   │
│  │  │  └──────┬───────┘                          │  │   │
│  │  └─────────┼──────────────────────────────────┘  │   │
│  │            ▼                                     │   │
│  │  ┌──────────────────────────────────────────┐    │   │
│  │  │ NotificationHelper.kt                    │    │   │
│  │  │ - Ongoing Progress (notification shade)  │    │   │
│  │  │ - Heads-up Alerts (permission/question)  │    │   │
│  │  │                                          │    │   │
│  │  │ BatteryOptimizationHelper.kt             │    │   │
│  │  │ - First-launch dialog                    │    │   │
│  │  │ - Persistent warning notification        │    │   │
│  │  └──────────────────────────────────────────┘    │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

---

## 3. Technology Stack

| Layer                | Technology                     | Version/Notes                      |
|----------------------|--------------------------------|------------------------------------|
| Desktop Server       | Go                             | 1.25.x                            |
| WebSocket Library    | gorilla/websocket              | v1.5.3                             |
| QR Code Generator    | skip2/go-qrcode               | v0.0.0-20200617                    |
| Mobile Frontend      | React + TypeScript             | Vite 5.4.x                        |
| CSS Framework        | TailwindCSS                    | v3.4.x                            |
| Icon Library         | Lucide React                   | Latest                             |
| Android Native       | Kotlin + WebView               | AGP 8.4.2, Kotlin 1.9.24          |
| Build System         | Gradle                         | 8.6 (pinned)                       |
| CI/CD                | GitHub Actions                 | ubuntu-latest, JDK 17             |
| Target Android SDK   | 34 (compileSdk), 24 (minSdk)  | Android 7.0+                       |

---

## 4. Directory Structure

```
claude-status-apk/
├── .github/workflows/
│   ├── build-apk.yml              # CI: Build Android APK
│   └── build-server.yml           # CI: Cross-compile Go server (Win/Mac/Linux)
├── cmd/server/
│   └── main.go                    # Go server entrypoint (flags, startup, banner)
├── internal/
│   ├── api/
│   │   └── server.go              # HTTP routes, WebSocket handler, CORS, static files
│   ├── hooks/
│   │   ├── installer.go           # Auto-install hooks into ~/.claude/settings.json
│   │   └── parser.go              # Tool name → human-friendly status string mapper
│   ├── models/
│   │   └── models.go              # Data structures: Session, Subagent, HookPayload, etc.
│   ├── network/
│   │   └── lan.go                 # LAN IP discovery, QR code generation
│   ├── state/
│   │   └── store.go               # In-memory state store, hook event handler, liveness watcher
│   └── watcher/
│       └── filewatcher.go         # JSONL transcript file watcher (redundancy source)
├── web/
│   └── index.html                 # Embedded web UI placeholder (go:embed)
├── mobile/
│   ├── src/
│   │   ├── App.tsx                # Root React component (state orchestration)
│   │   ├── main.tsx               # React entry point
│   │   ├── types.ts               # TypeScript type definitions mirroring Go models
│   │   ├── assets/                # SVG icons (claudecode-color.svg, claudecode.svg)
│   │   ├── components/
│   │   │   ├── StatusHero.tsx     # Main status icon & badge display
│   │   │   ├── LiveStreamBar.tsx  # Floating mini-bar + drawer (music-player style)
│   │   │   ├── SubagentInspector.tsx  # Collapsible sub-agent activity list
│   │   │   ├── QuestionPromptBanner.tsx # Golden banner for pending questions
│   │   │   ├── BatteryBanner.tsx  # Battery optimization warning & guidance
│   │   │   ├── ChromeRemoteButton.tsx  # Quick-launch Chrome Remote Desktop
│   │   │   ├── Header.tsx         # Top bar with connection indicator
│   │   │   ├── ConnectionModal.tsx  # Server URL configuration modal
│   │   │   └── ActivityLogs.tsx   # Recent activity log viewer
│   │   └── services/
│   │       ├── websocketService.ts  # WebSocket client (connect, reconnect, subscribe)
│   │       └── notificationService.ts  # Bridge to Android native notifications + audio chimes
│   ├── android/
│   │   ├── app/
│   │   │   ├── build.gradle       # Android app module config
│   │   │   ├── proguard-rules.pro # ProGuard keep rules for JS interface
│   │   │   └── src/main/
│   │   │       ├── AndroidManifest.xml
│   │   │       ├── java/com/claudecode/remote/
│   │   │       │   ├── MainActivity.kt              # WebView host + JS bridge
│   │   │       │   ├── NotificationHelper.kt         # Android notification channels & push
│   │   │       │   └── BatteryOptimizationHelper.kt  # Battery unrestricted guidance
│   │   │       └── res/
│   │   │           ├── drawable/       # Adaptive icon vectors
│   │   │           ├── mipmap-anydpi-v26/  # Adaptive launcher icons
│   │   │           ├── values/         # strings.xml, styles.xml
│   │   │           └── xml/            # network_security_config.xml (cleartext)
│   │   ├── build.gradle           # Root Gradle config (AGP 8.4.2, Kotlin 1.9.24)
│   │   ├── settings.gradle        # Gradle dependency resolution management
│   │   ├── gradle.properties      # JVM args, AndroidX, nonTransitiveRClass
│   │   └── gradle/wrapper/
│   │       └── gradle-wrapper.properties  # Pinned to Gradle 8.6
│   ├── package.json               # Node.js dependencies
│   ├── vite.config.ts             # Vite build config
│   ├── tailwind.config.js         # TailwindCSS config
│   └── tsconfig.json              # TypeScript config
├── scripts/
│   ├── build-go.bat               # Windows batch: compile Go server
│   ├── run-server.bat             # Windows batch: run Go server
│   └── test-mock-hook.ps1         # PowerShell: send mock hook events for testing
├── icon/
│   ├── claudecode-color.svg       # Color icon (session active/working)
│   └── claudecode.svg             # Monochrome icon (idle)
├── bin/
│   └── claude-remote-server.exe   # Compiled Go binary (Windows)
├── go.mod                         # Go module definition
├── go.sum                         # Go dependency checksums
└── README.md                      # Project documentation
```

---

## 5. Backend — Go Server (Desktop EXE)

### Entrypoint: `cmd/server/main.go`

Server startup sequence:
1. Parse CLI flags: `--port` (default: 9280), `--no-hooks`, `--no-watch`
2. Discover LAN IPs via `network.GetLocalIPs()`
3. Initialize `state.Store` (in-memory state manager)
4. Start `Store.StartLivenessWatcher()` — 1-second heartbeat polling
5. Auto-install Claude Code hooks via `hooks.InstallClaudeHooks(port)`
6. Start JSONL transcript file watcher on `~/.claude/projects/`
7. Print ASCII banner, QR code, and all available network URLs
8. Start HTTP server on `0.0.0.0:<port>`

### Key Packages

#### `internal/api/server.go` — HTTP & WebSocket Router

| Endpoint             | Method | Description                                         |
|----------------------|--------|-----------------------------------------------------|
| `POST /api/hook`     | POST   | Receives JSON hook payloads from Claude Code         |
| `GET /ws`            | WS     | WebSocket stream (initial snapshot + live updates)   |
| `GET /api/status`    | GET    | Full server state snapshot (REST)                    |
| `GET /api/sessions`  | GET    | List all sessions                                   |
| `GET /api/subagents` | GET    | List all subagents (active + historical)            |
| `GET /api/qr`        | GET    | QR code as PNG image                                |
| `POST /api/install-hooks` | POST | Manually trigger hook installation              |
| `GET /`              | GET    | Embedded web UI dashboard                           |

**WebSocket Protocol**: On connect, server sends `initial_state` message with full `ServerStateSnapshot`. Subsequently, it pushes `session_update`, `notification`, and `subagent_update` messages in real-time.

#### `internal/state/store.go` — State Machine & Event Handler

The central brain of the system. Processes `HookPayload` events and maintains:

- **Sessions map** (`map[string]*Session`) — keyed by session ID
- **Subagents map** (`map[string]*Subagent`) — keyed by tool_use_id
- **Notifications list** — capped at 100
- **Broadcast subscribers** — WebSocket channels for real-time push

**Hook Event State Machine:**

```
SessionStart   → Status: active, clear pending question
PreToolUse     → (branches below)
  ├── AskUserQuestion/ask_question → Status: waiting_permission, set PendingQuestion
  ├── ExitPlanMode/exit_plan_mode  → Status: waiting_permission, set PendingQuestion (plan review)
  ├── Task/Agent/invoke_subagent   → Status: subagent_running, create Subagent entry
  └── (other tools)                → Status: active, clear pending question
PostToolUse    → Remove from ActiveToolIDs, complete subagent if matches tool_use_id
PostToolUseFailure → Same as PostToolUse
PermissionRequest  → Status: waiting_permission, set PendingQuestion
SubagentStart  → Create new Subagent, Status: subagent_running
SubagentStop   → Complete subagent, move to history
Stop           → Status: idle, clear all active tools/subagents, send task_done notification
SessionEnd     → Status: completed
Notification   → Passthrough notification with type detection
```

**Liveness Watcher** (`checkLivenessAndAutoIdle`):
- Runs every 1 second via `time.Ticker`
- Checks sessions in `active` or `subagent_running` status
- If `LastActivity` is >4 seconds old AND no `PendingQuestion` exists → auto-transitions to `idle`
- Cleans up stale subagents and sends `task_done` notification
- **Critical for UX**: Without this, sessions would stay "working" forever if Claude Code's `Stop` hook fails

#### `internal/hooks/installer.go` — Hook Auto-Installation

- Creates `~/.claude/claude-remote-hook.js` — a Node.js bridge script that:
  1. Reads JSON from stdin (Claude Code pipes hook payload)
  2. Posts it to `http://127.0.0.1:<port>/api/hook`
  3. Fails silently (never blocks Claude Code execution)
- Patches `~/.claude/settings.json` to register hooks for all 12 event types
- Idempotent: checks if hook already registered before adding duplicates

**Registered Hook Events:**
`SessionStart`, `SessionEnd`, `Stop`, `PermissionRequest`, `Notification`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `SubagentStart`, `SubagentStop`, `TeammateIdle`, `TaskCompleted`

#### `internal/hooks/parser.go` — Tool Name Formatter

Converts raw tool names (e.g., `replace_file_content`) into human-friendly descriptions (e.g., `Editing server.go`). Handles:
- File operations: Read, Edit, Write (extracts basename from `file_path`/`path`)
- Terminal: Bash, run_command (truncates command to 60 chars)
- Search: Grep, Glob (shows query/pattern)
- Web: WebFetch, WebSearch (shows URL/query)
- Agents: Task, Agent, invoke_subagent (shows task description)
- Questions: AskUserQuestion → "Waiting for user input"
- Planning: EnterPlanMode → "Planning task steps", ExitPlanMode → "Plan ready, waiting for review"

#### `internal/watcher/filewatcher.go` — JSONL Transcript Watcher

**Redundancy mechanism** — In case hook delivery fails, this watcher:
- Polls `~/.claude/projects/` every 2 seconds
- Reads new lines from `.jsonl` transcript files
- Parses `tool_use` blocks from `assistant` messages
- Synthesizes `PreToolUse` hook payloads and feeds them into the Store
- Maintains file offsets to avoid re-processing (starts near end for large files >50KB)

#### `internal/network/lan.go` — Network Utilities

- `GetLocalIPs()`: Discovers all non-loopback IPv4 addresses, filters out 169.254.x.x (APIPA)
- `GenerateTerminalQRCode()`: ASCII QR code for terminal display
- `GenerateQRCodePNG()`: PNG bytes for HTTP endpoint

---

## 6. Frontend — React/Vite Mobile UI

### Component Hierarchy

```
App.tsx (Root)
├── Header.tsx                 — Top bar: connection dot, settings gear, notification bell
├── BatteryBanner.tsx          — Expandable amber warning if battery not unrestricted
├── QuestionPromptBanner.tsx   — Golden alert when Claude is asking a question
├── StatusHero.tsx             — Large circular icon + status badge + project name
├── ChromeRemoteButton.tsx     — Quick-launch button for Chrome Remote Desktop
├── SubagentInspector.tsx      — Collapsible list of active/completed sub-agents
├── ActivityLogs.tsx           — Scrollable recent activity log
├── LiveStreamBar.tsx          — Floating mini-bar (docked bottom) + expandable drawer
└── ConnectionModal.tsx        — Server IP/URL configuration overlay
```

### State Flow

```
App.tsx (useState hooks)
  │
  ├── isConnected ←── wsService.onConnectionChange()
  ├── activeSession ←── WebSocket "session_update" / "initial_state"
  ├── notifications ←── WebSocket "notification"
  ├── logs ←── activeSession.recent_logs
  │
  └── Derived state:
      ├── isWorking = isConnected && (active || subagent_running || waiting_permission)
      └── isWaitingInput = waiting_permission || pending_question exists
```

### Services

**`websocketService.ts`** — WebSocket Client:
- Auto-connects on startup
- URL stored in `localStorage` key `claude_server_url`
- Default fallback: `http://192.168.100.48:9280`
- Auto-reconnect every 2.5 seconds on disconnect
- Pub/sub pattern: `onMessage()`, `onConnectionChange()`
- REST fallback: `fetchStatus()` hits `GET /api/status`

**`notificationService.ts`** — Notification Bridge:
- Detects native Android via `window.AndroidBridge`
- Calls native Kotlin methods: `showNotification()`, `updateOngoingNotification()`
- Haptic vibration patterns per notification type
- Audio chime synthesizer (Web Audio API):
  - Permission: 2-tone urgent (A4→E5)
  - Task done: 3-tone celebration (C5→E5→G5)
  - Working/subagent: Rising sweep
  - Info: Falling sweep
- Web Notification API fallback for non-APK usage

### Icons

- **Working/Active**: `claudecode-color.svg` with `animate-bounce` CSS
- **Idle**: `claudecode.svg` (monochrome, no animation)
- Imported as Vite static assets and referenced via `<img src={...}>`

---

## 7. Android Native Layer (Kotlin)

### `MainActivity.kt`

- **WebView container** hosting the React build output from `assets/index.html`
- Uses `WebViewAssetLoader` with virtual domain `appassets.androidplatform.net`
- Registers `AndroidBridge` JavaScript interface with these methods:

| JS Method                    | Kotlin Handler                          |
|------------------------------|-----------------------------------------|
| `showNotification(t,m,type)` | `NotificationHelper.showPopupAlertNotification()` |
| `updateOngoingNotification(...)` | `NotificationHelper.updateOngoingProgressNotification()` |
| `openChromeRemoteDesktop()`  | `NotificationHelper.launchChromeRemoteDesktop()` |
| `isNativeAndroid()`          | Returns `true`                          |
| `isBatteryUnrestricted()`    | `BatteryOptimizationHelper.isIgnoringBatteryOptimizations()` |
| `openBatterySettings()`      | `BatteryOptimizationHelper.openBatteryOptimizationSettings()` |

- `onResume()`: Checks battery status and pushes to WebView via `evaluateJavascript()`
- `onPageFinished()`: Pushes battery status once page loads

### `NotificationHelper.kt`

Two notification channels:

| Channel                   | ID                      | Importance | Purpose                                  |
|---------------------------|-------------------------|------------|------------------------------------------|
| Claude Task Progress      | `claude_tasks_ongoing`  | LOW        | Persistent task bar in notification shade |
| Claude Alerts & Questions | `claude_alerts_heads_up`| HIGH       | Heads-up popups, sounds, vibrations      |

- **Ongoing notification**: Always visible, shows project name + current tool status + sub-agent count
- **Alert notification**: High-priority heads-up with custom vibration pattern `[0, 250, 100, 250, 100, 400]`
- Both include action buttons: "🖥️ Chrome Remote" and "📱 Open Monitor"

### `BatteryOptimizationHelper.kt`

- **`isIgnoringBatteryOptimizations()`**: Checks via Android `PowerManager`
- **`showFirstLaunchDialogIfNeeded()`**: AlertDialog on first install (persisted in SharedPreferences)
- **`showBatteryWarningNotificationIfNeeded()`**: Persistent notification when restricted
- **`openBatteryOptimizationSettings()`**: Launches `ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` intent

### Android Permissions

```xml
INTERNET, ACCESS_NETWORK_STATE, ACCESS_WIFI_STATE, VIBRATE,
POST_NOTIFICATIONS, REQUEST_IGNORE_BATTERY_OPTIMIZATIONS
```

---

## 8. Communication Pipeline

```
Claude Code CLI
    │
    │  (stdout/stdin hook)
    ▼
claude-remote-hook.js (Node.js)
    │
    │  HTTP POST (JSON)
    ▼
Go Server /api/hook
    │
    │  HandleHookEvent()
    ▼
State Store
    │
    │  broadcast()
    ▼
WebSocket /ws
    │
    │  JSON message
    ▼
React App (websocketService.ts)
    │
    │  AndroidBridge.showNotification()
    ▼
Kotlin NotificationHelper
    │
    ▼
Android System Notifications
```

**Data flow is unidirectional**: Claude Code → Server → Android. The Android app is read-only; it does NOT send input back to Claude Code. Users must use Chrome Remote Desktop or the physical PC to interact with Claude Code.

---

## 9. Claude Code Integration (Hooks System)

### How Hooks Work

Claude Code supports user-defined hooks in `~/.claude/settings.json`. Each hook event triggers a command that receives a JSON payload via stdin.

Our Go server auto-installs a bridge script:

```
~/.claude/settings.json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "node \"~/.claude/claude-remote-hook.js\"",
            "timeout": 5
          }
        ]
      }
    ],
    // ... same for all 12 events
  }
}
```

The bridge script (`claude-remote-hook.js`) reads stdin, parses JSON, and POSTs to `http://127.0.0.1:9280/api/hook`. It fails silently so Claude Code is never blocked.

### Hook Payload Structure

```json
{
  "hook_event_name": "PreToolUse",
  "session_id": "abc-123",
  "transcript_path": "/home/user/.claude/projects/.../transcript.jsonl",
  "cwd": "/home/user/project",
  "tool_name": "Edit",
  "tool_use_id": "tool-xyz",
  "tool_input": { "file_path": "src/main.go" }
}
```

---

## 10. Notification System

### Notification Types & Triggers

| Type          | Trigger                          | Android Channel      | Icon | Sound |
|---------------|----------------------------------|----------------------|------|-------|
| `working`     | Session connected                | Progress (LOW)       | —    | ding  |
| `permission`  | AskUserQuestion, ExitPlanMode    | Alerts (HIGH)        | ⚠️   | 2-tone|
| `subagent`    | Sub-agent launched/completed     | Progress (LOW)       | 🤖   | sweep |
| `task_done`   | Stop event / liveness auto-idle  | Alerts (HIGH)        | ✅   | 3-tone|
| `idle`        | Session ended                    | Progress (LOW)       | —    | sweep |
| `info`        | Generic notifications            | Progress (LOW)       | —    | sweep |

### ExitPlanMode (Special Case)

When Claude Code calls `ExitPlanMode`:
1. Server sets session status to `waiting_permission`
2. Creates `PendingQuestion` with title "📋 Plan Ready for Review"
3. Sends push notification via HIGH channel (heads-up banner)
4. Frontend shows golden `QuestionPromptBanner` with plan review details

---

## 11. State Management & Liveness Detection

### Liveness Watcher Algorithm

```
Every 1 second:
  for each session in sessions:
    if session.status == active OR subagent_running:
      if session.pending_question != nil:
        SKIP (user interaction expected)
      if session.status == waiting_permission:
        SKIP (not stuck, waiting for user)
      if time.Since(session.last_activity) >= 4 seconds:
        → Mark all active subagents as completed
        → Clear active tool IDs
        → Set status = idle
        → Set tool status = "Idling (Ready for prompt)"
        → Broadcast session_update
        → Send "✅ Task Completed" notification
```

**Why 4 seconds?** Claude Code typically fires PreToolUse → PostToolUse rapidly. A 4-second gap strongly indicates the turn has ended. This catches cases where Claude's `Stop` hook fails to fire.

### Sub-agent Cleanup

- Sub-agents tracked by `tool_use_id` (from PreToolUse of Task/Agent/invoke_subagent)
- Completed when matching `PostToolUse`/`PostToolUseFailure` arrives
- Also cleaned by `SubagentStop`, `TaskCompleted`, `TeammateIdle` events
- Stale agents auto-cleaned by liveness watcher
- Completed agents moved from `ActiveSubagents` → `SubagentHistory` (prepended, most recent first)

---

## 12. CI/CD Pipeline (GitHub Actions)

Proses kompilasi APK Android dilakukan secara otomatis di cloud menggunakan **GitHub Actions**. Pengembang tidak perlu menginstal Android Studio, Android SDK, ataupun Gradle di komputer lokal untuk memproduksi file APK.

### Direct Links
- **Repository**: [https://github.com/alipbudiman/claude-code-remote](https://github.com/alipbudiman/claude-code-remote)
- **GitHub Actions (All Workflow Runs)**: [https://github.com/alipbudiman/claude-code-remote/actions](https://github.com/alipbudiman/claude-code-remote/actions)
- **APK Workflow History**: [https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-apk.yml](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-apk.yml)
- **GitHub Releases (Official Builds)**: [https://github.com/alipbudiman/claude-code-remote/releases](https://github.com/alipbudiman/claude-code-remote/releases)

---

### Workflow 1: `build-apk.yml` — Cloud Android APK Build
*File konfigurasi: [`.github/workflows/build-apk.yml`](file:///d:/CODING/claude-status-apk/.github/workflows/build-apk.yml)*

Setiap kali ada perubahan yang di-push ke branch `main`, GitHub Actions runner (`ubuntu-latest`) secara otomatis menjalankan pipeline berikut:

1. **Checkout Code**: Mengambil branch/tag terbaru dari repositori GitHub.
2. **Node.js 20 Setup**: Menginstal dependensi frontend Vite + React di folder `mobile/`.
3. **Build Frontend Bundle**: Menjalankan `npm run build` yang menghasilkan bundle SPA di `mobile/dist/`.
4. **Sync Web Assets ke Android**: Menyalin seluruh isi `mobile/dist/*` ke folder `mobile/android/app/src/main/assets/`.
5. **Java JDK 17 Setup**: Menyiapkan Temurin JDK 17 untuk lingkungan Gradle.
6. **Android SDK Setup**: Menyiapkan Android SDK 34 (Android 14) dan build tools AAPT2.
7. **Gradle 8.6 Setup (Pinned)**: Menggunakan Gradle versi 8.6 yang kompatibel dengan Android Gradle Plugin (AGP) 8.4.2.
8. **Compile APK**: Menjalankan `gradle assembleDebug --stacktrace --no-daemon` untuk memproduksi `claude-remote.apk`.
9. **Upload Workflow Artifact**: Mengunggah APK ke cloud GitHub dengan nama artifact **`claude-remote-android-apk`** (~4.7 MB).
10. **Auto GitHub Release (Jika Tag `v*` atau Manual Dispatch)**: Mempublikasikan file `.apk` langsung ke halaman Releases GitHub.

#### Cara Mengunduh APK dari GitHub Actions:
1. Buka [https://github.com/alipbudiman/claude-code-remote/actions](https://github.com/alipbudiman/claude-code-remote/actions).
2. Klik workflow run terbaru yang berstatus centang hijau (✅ **Success**).
3. Scroll ke bagian bawah halaman pada tabel **Artifacts**.
4. Klik **`claude-remote-android-apk`** untuk mengunduh file `.zip`.
5. Ekstrak file zip tersebut untuk mendapatkan file **`claude-remote.apk`** siap install di HP Android.

---

### Workflow 2: `build-server.yml` — Multi-Platform Go Server Binaries
*File konfigurasi: [`.github/workflows/build-server.yml`](file:///d:/CODING/claude-status-apk/.github/workflows/build-server.yml)*

Mengompilasi binary Go desktop server untuk 4 sistem operasi sekaligus:
- **Windows (amd64)**: `claude-remote-server-windows-amd64.exe`
- **Linux (amd64)**: `claude-remote-server-linux-amd64`
- **macOS Apple Silicon (arm64)**: `claude-remote-server-darwin-arm64`
- **macOS Intel (amd64)**: `claude-remote-server-darwin-amd64`

Semua binary server juga diunggah sebagai workflow artifact dan release assets.

---

## 13. How to Build, Check & Validate

### Prerequisites

| Tool           | Version     | Install                              |
|----------------|-------------|--------------------------------------|
| Go             | 1.25+       | https://go.dev/dl/                   |
| Node.js        | 20+         | https://nodejs.org/                  |
| Android Studio | Latest      | For local APK testing (optional)     |
| Git            | Latest      | Source control                       |

### A. Build Go Server (Local Windows)

```powershell
# From project root
go build -ldflags="-s -w" -o bin/claude-remote-server.exe ./cmd/server

# Or use the script:
scripts\build-go.bat
```

**Validation**: Run the binary and check output:
```powershell
bin\claude-remote-server.exe -port 9280

# Expected output:
# ✅ Task Completion & Liveness auto-checking active (1s polling)
# ✅ Claude Code Hooks successfully linked to ~/.claude/settings.json
# ✅ JSONL Transcript file watcher active on ~/.claude/projects/
# 🚀 Server running on 0.0.0.0:9280 (Local LAN / Offline Wi-Fi)
```

### B. Build React Frontend

```powershell
cd mobile
npm install           # First time only
npm run build         # Produces dist/ directory

# Copy to Android assets:
Copy-Item -Recurse -Force "dist\*" "android\app\src\main\assets\"
```

**Validation**: Check `dist/` contains `index.html` + `assets/` folder with `.js` and `.css`.

### C. Build Android APK (Local)

```powershell
cd mobile/android
gradle assembleDebug --stacktrace

# APK output: app/build/outputs/apk/debug/app-debug.apk
```

### D. Build Android APK (CI/CD — Recommended)

```powershell
# Simply push to main branch:
git add . && git commit -m "your changes" && git push origin main

# GitHub Actions will automatically:
# 1. Build frontend
# 2. Compile APK
# 3. Upload as downloadable artifact at:
#    https://github.com/alipbudiman/claude-code-remote/actions
```

### E. Test Mock Hook Events (Without Claude Code Running)

```powershell
# Start the Go server first, then:
scripts\test-mock-hook.ps1

# This sends 3 test events:
# 1. PreToolUse (Edit file) → should show "Editing server.go"
# 2. SubagentStart → should show sub-agent in inspector
# 3. Stop → should trigger task_done notification
```

### F. Test REST API

```powershell
# Full status snapshot:
Invoke-RestMethod -Uri "http://127.0.0.1:9280/api/status" | ConvertTo-Json -Depth 10

# All sessions:
Invoke-RestMethod -Uri "http://127.0.0.1:9280/api/sessions"

# All subagents:
Invoke-RestMethod -Uri "http://127.0.0.1:9280/api/subagents"
```

### G. Test WebSocket (Browser Console)

```javascript
const ws = new WebSocket('ws://127.0.0.1:9280/ws');
ws.onmessage = (e) => console.log(JSON.parse(e.data));
// Should immediately receive "initial_state" message
```

### H. Validate Android APK on Device

1. Download APK from GitHub Actions artifacts
2. Install on Android device (enable "Install from unknown sources")
3. Connect to same Wi-Fi as PC
4. Open APK → enter PC's IP address (shown in server console)
5. Run `scripts\test-mock-hook.ps1` and verify:
   - Status changes from idle → working → idle
   - Sub-agent appears and disappears
   - Push notification fires on task completion
   - LiveStreamBar at bottom updates in real-time

---

## 14. How to Deploy

### Production Deployment Steps

```powershell
# 1. Build Go server
go build -ldflags="-s -w" -o bin/claude-remote-server.exe ./cmd/server

# 2. Build React frontend
cd mobile
npm run build

# 3. Copy frontend to Android assets
Copy-Item -Recurse -Force "dist\*" "android\app\src\main\assets\"
cd ..

# 4. Commit and push (triggers GitHub Actions CI)
git add .
git commit -m "feat: description of changes"
git push origin main

# 5. Monitor build at:
#    https://github.com/alipbudiman/claude-code-remote/actions
#
# 6. Download APK artifact from the completed workflow run
#
# 7. Install APK on Android device via:
#    - USB file transfer
#    - ADB: adb install claude-remote.apk
#    - Direct download from GitHub Actions
```

### Creating a Tagged Release

```powershell
git tag v1.0.0
git push origin v1.0.0
# This triggers both workflows and creates a GitHub Release
# with APK + server binaries for all platforms attached
```

### Server Deployment on PC

```powershell
# Option 1: Run directly (foreground console; Ctrl+C or closing the console
# window triggers a bounded graceful shutdown)
bin\claude-remote-server.exe -port 9280

# Option 2: Use script
scripts\run-server.bat

# Option 3 (recommended): logon autostart via -install.
# NOTE: registering an ONLOGON task requires an ELEVATED (Administrator)
# console — a normal console gets "Access is denied" from schtasks.
bin\claude-remote-server.exe -install        # registers Scheduled Task "ClaudeRemoteServer"
schtasks /Query /TN ClaudeRemoteServer       # verify it is registered
bin\claude-remote-server.exe -uninstall      # remove the autostart (normal console is fine)
```

The registered task runs:
`"<exe-path>" -port 9280 -log-file "%USERPROFILE%\.claude\claude-remote-server.log"`

**Logon semantics & lifetime ceiling:** the task starts at the user's logon —
not as a boot-time SYSTEM service — because Claude Code (the event source)
runs in the user session, so a SYSTEM service would buy nothing over a
logon-triggered task. Tracking can only exist while Claude Code runs: PC
sleep or a logged-off session means there is nothing to track. A duplicate
launch on the same port is harmless — the second instance probes
`GET /api/health`, sees another instance of this server, and exits 0. Closing
the console window / logoff / shutdown runs a graceful path bounded to ~3s
(the durable event log, not this path, is the real durability story). Owners
wanting a true service with restart-on-crash can wrap the EXE with WinSW or
NSSM — a documented alternative only, not built in this repo.

---

## 15. Algorithm & System Verification Checklist

### ✅ Hook Installation Verification

```powershell
# Check if hooks are properly installed:
Get-Content "$env:USERPROFILE\.claude\settings.json" | ConvertFrom-Json | ConvertTo-Json -Depth 10

# Verify these 12 events exist under "hooks":
# SessionStart, SessionEnd, Stop, PermissionRequest, Notification,
# PreToolUse, PostToolUse, PostToolUseFailure, SubagentStart,
# SubagentStop, TeammateIdle, TaskCompleted

# Check bridge script exists:
Test-Path "$env:USERPROFILE\.claude\claude-remote-hook.js"
```

### ✅ State Machine Transition Verification

Test each transition path:

```powershell
# 1. Session Start → active
$body = '{"hook_event_name":"SessionStart","session_id":"test-1","cwd":"C:\\project"}'
Invoke-RestMethod -Uri http://127.0.0.1:9280/api/hook -Method POST -Body $body -ContentType "application/json"
# CHECK: /api/status → active_session.status == "active"

# 2. PreToolUse (normal tool) → active
$body = '{"hook_event_name":"PreToolUse","session_id":"test-1","tool_name":"Edit","tool_use_id":"t1","tool_input":{"file_path":"main.go"}}'
Invoke-RestMethod -Uri http://127.0.0.1:9280/api/hook -Method POST -Body $body -ContentType "application/json"
# CHECK: status == "active", current_tool_status == "Editing main.go"

# 3. PreToolUse (AskUserQuestion) → waiting_permission
$body = '{"hook_event_name":"PreToolUse","session_id":"test-1","tool_name":"AskUserQuestion","tool_use_id":"t2","tool_input":{"question":"Which option?","questions":[{"question":"Pick one","options":["A","B"]}]}}'
Invoke-RestMethod -Uri http://127.0.0.1:9280/api/hook -Method POST -Body $body -ContentType "application/json"
# CHECK: status == "waiting_permission", pending_question.question == "Pick one"

# 4. PreToolUse (ExitPlanMode) → waiting_permission + push notification
$body = '{"hook_event_name":"PreToolUse","session_id":"test-1","tool_name":"ExitPlanMode","tool_use_id":"t3"}'
Invoke-RestMethod -Uri http://127.0.0.1:9280/api/hook -Method POST -Body $body -ContentType "application/json"
# CHECK: status == "waiting_permission", pending_question.title contains "ExitPlanMode"

# 5. PreToolUse (invoke_subagent) → subagent_running
$body = '{"hook_event_name":"PreToolUse","session_id":"test-1","tool_name":"invoke_subagent","tool_use_id":"sub-1","tool_input":{"Task":"Fix the bug"}}'
Invoke-RestMethod -Uri http://127.0.0.1:9280/api/hook -Method POST -Body $body -ContentType "application/json"
# CHECK: status == "subagent_running", active_subagents has "sub-1"

# 6. PostToolUse (subagent complete) → active (if no more subagents)
$body = '{"hook_event_name":"PostToolUse","session_id":"test-1","tool_use_id":"sub-1"}'
Invoke-RestMethod -Uri http://127.0.0.1:9280/api/hook -Method POST -Body $body -ContentType "application/json"
# CHECK: active_subagents is empty, subagent_history[0].id == "sub-1"

# 7. Stop → idle + task_done notification
$body = '{"hook_event_name":"Stop","session_id":"test-1"}'
Invoke-RestMethod -Uri http://127.0.0.1:9280/api/hook -Method POST -Body $body -ContentType "application/json"
# CHECK: status == "idle", notifications[0].type == "task_done"

# 8. Liveness auto-idle (wait 5 seconds after PreToolUse without PostToolUse)
$body = '{"hook_event_name":"PreToolUse","session_id":"test-1","tool_name":"Bash","tool_use_id":"t99","tool_input":{"command":"echo test"}}'
Invoke-RestMethod -Uri http://127.0.0.1:9280/api/hook -Method POST -Body $body -ContentType "application/json"
Start-Sleep -Seconds 5
# CHECK: status == "idle" (auto-transitioned by liveness watcher)
```

### ✅ WebSocket Real-Time Delivery Verification

```javascript
// Open browser console at http://127.0.0.1:9280
const ws = new WebSocket('ws://127.0.0.1:9280/ws');
const messages = [];
ws.onmessage = (e) => {
  const msg = JSON.parse(e.data);
  messages.push(msg);
  console.log(`[${msg.type}]`, msg.data);
};

// Then send a hook event from PowerShell and verify:
// - "session_update" message received within 100ms
// - "notification" message follows immediately after
```

### ✅ Android Notification Verification

On the Android device:
1. Send a `PreToolUse` with `AskUserQuestion` → Should see **heads-up notification** with sound + vibration
2. Send a `Stop` event → Should see **"✅ Task Completed"** notification
3. Send `ExitPlanMode` → Should see **"📋 Plan Ready for Review"** notification
4. Verify ongoing notification updates tool status text in real-time

### ✅ Battery Optimization Verification

1. Fresh install APK → Should see first-launch AlertDialog about battery
2. Dismiss dialog → Should see persistent notification "⚠️ Background Access Restricted"
3. Set battery to Unrestricted → Notification should auto-dismiss
4. Set battery back to Optimized → Warning reappears on next app resume
5. In-app: BatteryBanner should appear with "Fix" button
6. Tap "Fix" → Should open system battery optimization dialog

### ✅ Sub-agent Lifecycle Verification

1. Send `SubagentStart` → Appears in SubagentInspector with "RUNNING" badge
2. Send another tool event with same `tool_use_id` → Sub-agent tool status updates
3. Send `SubagentStop` with same `tool_use_id` → Moves to "Completed Sub-Tasks" history
4. After 4 seconds of inactivity → Stale agents auto-cleaned by liveness watcher

---

## 16. Known Limitations & Improvement Areas

### Current Limitations

1. **Unidirectional communication**: User cannot respond to questions from the APK; must use Chrome Remote Desktop or physical PC
2. **Single active session display**: Frontend only shows one active session (most recently active)
3. **No authentication**: Server is open on LAN; anyone on the network can connect
4. **No encryption**: WebSocket uses `ws://` not `wss://` (acceptable for local LAN)
5. **Hardcoded fallback IP**: `websocketService.ts` defaults to `192.168.100.48:9280` — should be configurable per-user
6. **No data persistence**: All state is in-memory; restarting the server clears everything
7. **Liveness threshold**: 4-second auto-idle may be too aggressive for slow tool executions

### Potential Improvements

- [ ] Add user input forwarding (respond to questions from APK)
- [ ] Support multiple simultaneous session displays
- [ ] Add mTLS or API key authentication for security
- [ ] Add SQLite/file-based state persistence
- [ ] Make liveness threshold configurable via CLI flag
- [ ] Add Foreground Service for true background WebSocket persistence
- [ ] Add widget/lock screen widget for quick status glance
- [ ] Add dark/light theme toggle
- [ ] Support for Claude Code Teams/multi-agent orchestration events

---

## 17. Quick Reference Commands

```powershell
# ──────────────────────────────────────────────
# BUILD
# ──────────────────────────────────────────────

# Go server (Windows)
go build -ldflags="-s -w" -o bin/claude-remote-server.exe ./cmd/server

# React frontend
cd mobile && npm run build && cd ..

# Copy frontend to Android assets
Copy-Item -Recurse -Force "mobile\dist\*" "mobile\android\app\src\main\assets\"

# Android APK (local)
cd mobile/android && gradle assembleDebug && cd ../..

# ──────────────────────────────────────────────
# RUN
# ──────────────────────────────────────────────

# Start server
bin\claude-remote-server.exe -port 9280

# Start server (custom port)
bin\claude-remote-server.exe -port 8080

# Start without hooks auto-install
bin\claude-remote-server.exe -port 9280 --no-hooks

# Start without file watcher
bin\claude-remote-server.exe -port 9280 --no-watch

# ──────────────────────────────────────────────
# TEST
# ──────────────────────────────────────────────

# Send mock hook events
powershell scripts\test-mock-hook.ps1

# Check server status
Invoke-RestMethod http://127.0.0.1:9280/api/status | ConvertTo-Json

# ──────────────────────────────────────────────
# DEPLOY
# ──────────────────────────────────────────────

# Build everything and push to GitHub
npm run build --prefix mobile
Copy-Item -Recurse -Force "mobile\dist\*" "mobile\android\app\src\main\assets\"
go build -ldflags="-s -w" -o bin/claude-remote-server.exe ./cmd/server
git add . && git commit -m "feat: your changes" && git push origin main

# Create tagged release
git tag v1.x.x && git push origin v1.x.x

# Check CI status
# → https://github.com/alipbudiman/claude-code-remote/actions
```

---

> **End of Hand-Off Document**  
> This document should provide all context needed for another AI agent or developer to understand, validate, modify, and deploy this project independently.
