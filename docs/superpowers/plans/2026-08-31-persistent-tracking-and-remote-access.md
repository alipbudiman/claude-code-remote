# Persistent Background Tracking & Remote Access — Investigation and Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Status (2026-08-31): ANALYSIS + PLAN ONLY.** Nothing has been implemented yet, per owner instruction. This document is the deliverable of a 15-agent diagnostic workflow: 5 subsystem diagnoses (each adversarially verified against the actual code — **26 claims CONFIRMED, 1 REFUTED and excluded**), 4 web-research tracks (facts dated Aug 2026, sources cited), and a completeness critic whose 17 gaps are addressed below.

**Goal:** (1) Claude Code run-tracking keeps running and stays truthful regardless of whether the Android app or the server console window is open; (2) the APK can monitor (and later control) the desktop server from anywhere, not just the home LAN.

**Architecture (as-is):** Claude Code → hook bridge (`~/.claude/claude-remote-hook.js`, fire-and-forget POST) → Go server (`cmd/server`, all state in-memory, gorilla/websocket broadcast) → Android APK (Kotlin WebView hosting React app; **the WebSocket client and all notification-triggering logic live in the WebView's JavaScript**).

**Architecture (target):** Go server becomes a durable, authenticated, autostarted event hub (raw-event log + spool replay + event-driven state machine); the Android app gains a native Foreground Service that owns the WebSocket connection and notifications; remote access via a relay the desktop dials out to (token rooms) + FCM high-priority push for alerts, with Tailscale as a zero-code interim and Cloudflare Tunnel as the documented alternative.

**Tech Stack:** unchanged per `.instructions/tech-stack.md` (Go 1.25 + gorilla/websocket, React 18 + Vite + TS + Tailwind, Kotlin WebView, no external databases — persistence via JSONL files). Two proposed new dependencies are flagged in Global Constraints.

---

## Global Constraints

- One milestone per session; CLAUDE.md completion gate (verified evidence, baseline still green) applies to each.
- No external DB / message broker in the desktop server — persistence is plain JSONL under `~/.claude/`.
- **New dependencies requiring owner sign-off before use** (tech-stack deviation rule):
  - Android: `com.squareup.okhttp3:okhttp:4.12.0` (native WebSocket client for the Foreground Service — Android has no built-in WS client).
  - Go: `golang.org/x/sys/windows` (console-control handler; quasi-stdlib). A zero-dependency fallback is specified in M3 so this one is optional.
- Runtime facts that drift (hosting prices, tunnel timeouts, Claude Code hook names) must be re-verified at implementation time; each task below lists what to re-check.
- The APK's `network_security_config.xml` currently permits cleartext to **all** hosts (`base-config cleartextTrafficPermitted="true"` + manifest `usesCleartextTraffic="true"`); its RFC1918 "domain" entries use CIDR notation, which is **invalid** in a domain-config (Android matches hostname literals only) — they are inert. Treat "cleartext is restricted to LAN" as NOT currently true.

---

## Part A — Root Cause Analysis: why tracking stops

### A.0 The causal chain in one paragraph

The Go server tracks sessions server-side and does not need a connected client to record state — but **nothing the phone shows survives the phone app closing** (all phone-side tracking lives in WebView JS inside an app that has no Service of any kind), **and nothing at all survives the server's console window closing** (console-attached process, no supervisor, silent-drop hooks, in-memory-only state). Independently of both, the server's 4-second liveness heuristic **falsely declares runs finished while they are still in progress**, which is the "sometimes stops" flavor: the UI freezes on "Task Completed"/"idle" mid-run because the server's own state machine said so, and no later event corrects it.

### A.1 Failure surface 1 — Android app closed → phone-side tracking dies

All verified in code (verdicts: CONFIRMED ×5).

| # | Root cause | Decisive evidence | User-visible symptom |
|---|---|---|---|
| RC-A1 | **No Service / receiver / WorkManager / AlarmManager exists anywhere.** Tracking lives and dies with `MainActivity`'s process. | [AndroidManifest.xml:22-31](../../../mobile/android/app/src/main/AndroidManifest.xml) — `<application>` contains only `.MainActivity`; Glob of `mobile/android/**/*.kt` = exactly 3 files (MainActivity, NotificationHelper, BatteryOptimizationHelper); grep for `Service\|WorkManager\|JobScheduler\|AlarmManager\|startForeground` finds only `Context.NOTIFICATION_SERVICE` / `POWER_SERVICE` lookups | Home / swipe-from-recents → process cached then frozen/killed (Doze freezer, LMK, OEM killers) → no heads-up when Claude asks a question or finishes. Nondeterministic timing per OEM = "SOMETIMES stops" |
| RC-A2 | **Notifications are driven exclusively by WebView JS via the bridge.** Ongoing notification is a plain `notify()` with `setOngoing(true)` while working, never `startForeground()`; no native cancel path for id 1001. | [MainActivity.kt:178-193](../../../mobile/android/app/src/main/java/com/claudecode/remote/MainActivity.kt) (only entry point); [notificationService.ts:118-138](../../../mobile/src/services/notificationService.ts) (only caller); [App.tsx:101-110](../../../mobile/src/App.tsx); [NotificationHelper.kt:108,124](../../../mobile/android/app/src/main/java/com/claudecode/remote/NotificationHelper.kt) | After app death the shade keeps a **pinned, non-dismissable, lying "⚡ Claude Working"** notification (only clearable by force-stop or cold start) |
| RC-A3 | **Manifest lacks every background permission**: no `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_DATA_SYNC` (required for typed FGS at targetSdk 34), `RECEIVE_BOOT_COMPLETED`, `WAKE_LOCK`. | [AndroidManifest.xml:5-10](../../../mobile/android/app/src/main/AndroidManifest.xml) (complete permission list is INTERNET, ACCESS_NETWORK_STATE, ACCESS_WIFI_STATE, VIBRATE, POST_NOTIFICATIONS, REQUEST_IGNORE_BATTERY_OPTIMIZATIONS); [build.gradle:12-13](../../../mobile/android/app/build.gradle) minSdk 24 / targetSdk 34 | After reboot the app never relaunches; a service patched in without the manifest would crash (`MissingForegroundServiceTypeException` on Android 14) |
| RC-A4 | **Battery helper is advisory only** — queries the Doze whitelist, shows a dialog, deep-links settings; no wake lock anywhere. Doze whitelist does NOT exempt a service-less cached process from the Android 11+ cached-app freezer or OEM task killers. | [BatteryOptimizationHelper.kt:48-54](../../../mobile/android/app/src/main/java/com/claudecode/remote/BatteryOptimizationHelper.kt) (only PowerManager use is a query); grep `newWakeLock\|WakeLock` = zero hits | Owner grants "Unrestricted", closes app, assumes background monitoring is guaranteed — it still freezes |
| RC-A5 | **Missed events are never re-alerted after reconnect** — only live WS `notification` frames call `notify()`; the snapshot handler just setStates. | [App.tsx:79-85](../../../mobile/src/App.tsx) (only notify() for real events) vs [App.tsx:54-62](../../../mobile/src/App.tsx) (handleSnapshot: no diff); server sends exactly one `initial_state` then live broadcast ([server.go:121-143](../../../internal/api/server.go)) | Phone offline for 2 min exactly when Claude finished/asked → event appears buried in logs, **no heads-up, no chime** ever fires |
| RC-A6 | **No staleness detection on either end** — no ping/pong, no read deadline, no last-message watchdog; a half-open socket (Wi-Fi roam, NAT idle) pins `isConnected=true` with a frozen "Working" UI. (The `onerror`-doesn't-reconnect smell is latent only — browsers always pair error with close.) | [websocketService.ts:75-105](../../../mobile/src/services/websocketService.ts) (no ping/watchdog); [server.go:145-151](../../../internal/api/server.go) (`ReadMessage` blocks forever; "Keep alive" comment is misleading) | Phone shows "⚡ Claude Working / Executing tools…" indefinitely after the run actually finished |

Additional client-side weaknesses (confirmed, lower severity): fixed 2.5 s reconnect with no backoff/visibility hook ([websocketService.ts:114-120](../../../mobile/src/services/websocketService.ts)); hardcoded fallback IP `http://192.168.100.48:9280` reached on every fresh APK install, with no auto-failover across `host_ips` ([websocketService.ts:36](../../../mobile/src/services/websocketService.ts)).

### A.2 Failure surface 2 — server console window closed → ALL tracking dies

Verdicts: CONFIRMED ×5.

| # | Root cause | Decisive evidence | User-visible symptom |
|---|---|---|---|
| RC-S1 | **Server is a console-attached child process with no supervisor** — no service, scheduled task, autostart, watchdog, or single-instance guard anywhere in the repo. Closing the console (CTRL_CLOSE_EVENT — which Go's `signal.Notify` never receives) kills it. | [run-server.bat:8-9](../../../scripts/run-server.bat) (foreground exe + pause); [main.go:31-33](../../../cmd/server/main.go) (only `-port/-no-hooks/-no-watch`); repo-wide grep for `schtasks\|NSSM\|Start-Process\|watchdog` = doc line only | Owner closes the window mid-run → every hook POST fails silently → app freezes on last status, "Task Completed" never fires, nothing relaunches at next login |
| RC-S2 | **Hook bridge is fire-and-forget with permanent silent drop**: 1500 ms timeout, `process.exit(0)` on error/timeout, no spool/retry. | Deployed `~/.claude/claude-remote-hook.js:33,39-47`; regenerated identically by [installer.go:97,103-111](../../../internal/hooks/installer.go) | Events emitted during any downtime are lost forever — Stop/SubagentStop/SessionEnd/PermissionRequest can never be reconstructed (the watcher synthesizes only PreToolUse) |
| RC-S3 | **Zero state persistence + watcher cannot rebuild**: only disk writes in the whole tree are the hook script and settings.json; watcher replays only synthetic PreToolUse, skips to last 25 KB for files >50 KB, keeps offsets in an in-memory map, and creates every replayed session as `StatusActive`. | [store.go:13-34](../../../internal/state/store.go); [filewatcher.go:22,38,118-120,160,173-186](../../../internal/watcher/filewatcher.go) | After a restart mid-run: pending questions/subagent state gone; ended sessions resurrect as active; phone erupts with stale notifications then gets a false "✅ Task Completed" per session ~4 s later |
| RC-S4 | **Graceful shutdown is decorative**: handler catches only SIGINT/SIGTERM and calls `os.Exit(0)` (bypassing `defer fw.Stop()`); console-X / logoff never reach it; nothing would be flushed anyway. | [main.go:59,90-97,100-102](../../../cmd/server/main.go); no `Shutdown`/save anywhere | X-kill = abrupt TCP reset for WS clients; no state written |
| RC-S5 | **The documented "background" option (`Start-Process -WindowStyle Hidden`) survives nothing** (logoff/reboot/update), isn't autostarted, has no single-instance guard (2nd copy dies on bind via `log.Fatalf`), and hides all output (stdout-only logging). | [hand-off.md:795-804](../../../hand-off.md); [server.go:217-219](../../../internal/api/server.go) bare `ListenAndServe` | "Tracking randomly stops" impression after any reboot; duplicate-launch confusion |

### A.3 Failure surface 3 — the state machine lies while a run is in progress ("sometimes stops")

Verdicts: CONFIRMED (this is the dominant *correctness* bug).

| # | Root cause | Decisive evidence | User-visible symptom |
|---|---|---|---|
| RC-M1 | **4-second liveness auto-idle force-completes any tool/subagent that runs >4 s without a parent-session hook event.** `LastActivity` is refreshed only inside `HandleHookEvent`; `UpdateSubagentActivity` (the only subagent-side refresher) is **dead code — zero callers**. Owner's `settings.json` sets `API_TIMEOUT_MS=3000000`, so model thinking alone dwarfs 4 s. | [store.go:558-571](../../../internal/state/store.go) (≥4 s → wipe tools/subagents, `Status=Idle`), [store.go:584-597](../../../internal/state/store.go) (`✅ Task Completed` push); grep `UpdateSubagentActivity` = definition only | Long Bash/npm/go/subagent run → 4 s later the phone buzzes "✅ Task Completed", session tile flips idle **while the run is still going**. Worse: every inter-tool thinking gap >4 s repeats the false alert |
| RC-M2 | **`PostToolUse` never restores `Status` from idle and never clears `PendingQuestion`** (its only Status write is SubagentRunning→Active at [store.go:290-292](../../../internal/state/store.go)); question cleared only at SessionStart / next non-question PreToolUse / Stop / SessionEnd. | [store.go:269-292](../../../internal/state/store.go) | (1) After premature idle, the whole post-tool thinking gap shows idle. (2) User answers a question at the PC → phone keeps showing "Claude needs your input" until the next tool starts — forever if the terminal dies in the gap |
| RC-M3 | **`waiting_permission` liveness exemption is dead code** (outer gate at :551 already excludes that status), **no TTL/eviction exists** (`delete(s.sessions,…)` appears nowhere), and with all sessions idle the displayed "active session" is random map-iteration order. | [store.go:551-553,468-470](../../../internal/state/store.go) | Terminal killed while a permission prompt is on screen → phantom "working/waiting" banner for days; session list grows unbounded |
| RC-M4 | **Hook registration is valid but incomplete, and one payload contract is stale.** All 12 registered events exist in current Claude Code docs, but `UserPromptSubmit` (and `PreCompact`, `PermissionDenied`, `StopFailure`) are not registered — so "thinking after a prompt" is indistinguishable from idle, which is what forces the 4 s heuristic. The `Notification` handler matches `waiting_input`, which no longer exists in the documented type list (`permission_prompt` still matches; `agent_needs_input`-style waits fall through to generic info). | [installer.go:14-27](../../../internal/hooks/installer.go); deployed `~/.claude/settings.json` (same 12); [store.go:422](../../../internal/state/store.go); verified against the official hooks reference (re-verify against installed `claude --version` at implementation time) | Prompt submitted → 30 s of thinking → false "Task Completed". Subagent input-waits alert as generic info, not "⚠️ User Input Required" |
| RC-M5 | **`HookPayload` does not parse `agent_id`** — SubagentStart/SubagentStop payloads carry `agent_id` (not `tool_use_id`), so SubagentStart invents `subagent-<UnixNano>` IDs that no stop event can match, and SubagentStop falls into the "complete ALL subagents" branch, clobbering still-running parallel Task subagents. | [models.go:60-75](../../../internal/models/models.go) (no agent_id; json.Unmarshal drops it); [store.go:324-327,357-381](../../../internal/state/store.go) | Two parallel subagents → the first one's stop marks BOTH completed; one disappears from tracking |
| RC-M6 | **JSONL watcher double-ingests with no dedup** — it synthesizes PreToolUse for every tool_use block the hook already delivered (`Source` field exists but is never set or read), can replay after the matching PostToolUse (resurrecting "completed" subagents), and each ingestion fires `AddNotification` unconditionally. | [filewatcher.go:49,173-186](../../../internal/watcher/filewatcher.go); [store.go:249-263,441-444](../../../internal/state/store.go); [models.go:73](../../../internal/models/models.go) | Every subagent launch buzzes twice; ghost "running" subagent cards until the next sweep — reads as "tracking is flaky" |

### A.4 Refuted hypothesis — do NOT fix this

The diagnosis agent claimed lossy broadcast fan-out + snapshot-before-subscribe (store.go:56-61 / server.go:122-133) permanently freezes a reopened client. **Refuted by the verifier:** every (re)connection begins with a full `initial_state` snapshot and the client auto-reconnects every 2.5 s, so a reopened app always resyncs. The "frozen on Task Completed" symptom is real but is caused by RC-M1/RC-M2 (the server's state itself is wrong); the snapshot faithfully reproduces it. Fixing the WS handler ordering is unnecessary — **fix the state machine instead.**

---

## Part B — Fix Design: persistent, truthful tracking (Milestones M0–M4)

Build order matters (critic-verified dependencies): **M0 auth → M1 state machine → M2 durability → M3 Windows lifecycle → M4 Android FGS → (Part C) remote transport.** An implementer who starts with the Android FGS builds against a server that still lies and loses state — guaranteed rework.

### M0 — Server token auth + origin hardening (prerequisite for ANY network exposure)

**Files:** Modify [internal/api/server.go](../../../internal/api/server.go), [internal/hooks/installer.go](../../../internal/hooks/installer.go), [cmd/server/main.go](../../../cmd/server/main.go). Create `internal/auth/token.go`.
**Interfaces:** `auth.LoadOrCreateToken(dir string) (string, error)`; `Server.token string`; middleware `requireToken`.

Why first: today `CheckOrigin` returns true for every origin ([server.go:22-24](../../../internal/api/server.go)), CORS is `*`, `/api/hook` accepts POSTs from anyone, and `/api/install-hooks` **remotely mutates `~/.claude/settings.json`**. On LAN this is a leak (any webpage can open a WebSocket to a guessable LAN IP and read project names, file paths, shell command lines — cross-site WebSocket hijacking); on any public hostname it becomes an internet-wide leak. A tunnel terminates TLS; it does NOT authenticate the client to the app.

- [ ] **Step 1: Token creation.** On first run, generate 32 random bytes → hex, persist to `~/.claude/claude-remote-token` (0600), print it and include it in the QR URL (`http://<ip>:9280/?token=<tok>`). `crypto/rand` + `os.WriteFile` — no new deps.

```go
// internal/auth/token.go
func LoadOrCreateToken() (string, error) {
    p := filepath.Join(homeDotClaude(), "claude-remote-token")
    if b, err := os.ReadFile(p); err == nil && len(bytes.TrimSpace(b)) == 64 {
        return string(bytes.TrimSpace(b)), nil
    }
    raw := make([]byte, 32)
    if _, err := rand.Read(raw); err != nil { return "", err }
    tok := hex.EncodeToString(raw)
    return tok, os.WriteFile(p, []byte(tok), 0o600)
}
```

- [ ] **Step 2: Middleware** (constant-time compare). Accept the token via `Authorization: Bearer`, `?token=`, **or** `Sec-WebSocket-Protocol: claude-remote.<token>` — browser/WebView JS cannot set headers on a WS handshake, so the subprotocol (the trick Omnara uses) or query param is mandatory for `/ws`:

```go
func (s *Server) requireToken(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
        if tok == "" { tok = r.URL.Query().Get("token") }
        if tok == "" {
            for _, p := range websocket.Subprotocols(r.Header) {
                if strings.HasPrefix(p, "claude-remote.") { tok = strings.TrimPrefix(p, "claude-remote.") }
            }
        }
        if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
            http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized); return
        }
        next.ServeHTTP(w, r)
    })
}
```
For `/ws`, set `Upgrader.Subprotocols = []string{"claude-remote." + s.token}` so gorilla echoes the selected protocol back (this is what makes the browser handshake succeed).

- [ ] **Step 3: `CheckOrigin`** — allow only: no-Origin clients (hook bridge, curl), `https://appassets.androidplatform.net` (APK WebView), and the server's own origins. **Step 4: CORS** — replace `*` with the same origin list. **Step 5: Hook bridge** — generated `claude-remote-hook.js` reads `~/.claude/claude-remote-token` synchronously (tiny file; well inside the 1.5 s budget) and sends `Authorization: Bearer`.
- [ ] **Step 6: Keep the embedded dashboard usable** — the QR URL already carries `?token=`; the dashboard JS stores it in `localStorage` and appends it to WS/API calls.

**Verify:** `curl http://127.0.0.1:9280/api/status` → 401; with `-H "Authorization: Bearer <tok>"` → 200; `scripts\test-mock-hook.ps1` still passes (bridge sends token); browser console `new WebSocket('ws://127.0.0.1:9280/ws')` fails, `new WebSocket('ws://127.0.0.1:9280/ws', 'claude-remote.<tok>')` succeeds; `go build ./...` clean.
**Acceptance:** no endpoint answers without the token; hook simulation green; existing APK requires entering the token once in ConnectionModal (add a token field).

### M1 — Event-driven session state machine (replaces the 4-second heuristic)

**Files:** Modify [internal/state/store.go](../../../internal/state/store.go), [internal/hooks/installer.go](../../../internal/hooks/installer.go), [internal/models/models.go](../../../internal/models/models.go), [internal/watcher/filewatcher.go](../../../internal/watcher/filewatcher.go); `cmd/server/main.go` (new `-idle-timeout` flag, default `300s`).

Signal basis (critic-specified): **`UserPromptSubmit` = turn start · `PreToolUse`/`PostToolUse` keyed by `tool_use_id` = tool start/end · `Stop` = turn end (the ONLY "✅ Task Completed") · auto-idle demoted to a minutes-long fallback that never fires while a tool is in flight.**

- [ ] **1.1 Register the missing turn-lifecycle hooks** in `hookEvents`: add `UserPromptSubmit`; add `PermissionDenied`, `StopFailure`, `PreCompact` (verify each against the installed `claude --version` hooks docs at implementation time — keep only what fires). New `UserPromptSubmit` case: `Status=active`, clear `PendingQuestion`, `CurrentToolStatus="Working on your prompt…"`, **no notification** (anti-noise).
- [ ] **1.2 Kill the 4 s misfire** in `checkLivenessAndAutoIdle`:

```go
if len(sess.ActiveToolIDs) > 0 { continue }              // tool in flight — the ONLY remover is its PostToolUse/Stop
if sess.PendingQuestion != nil { continue }
if time.Since(sess.LastActivity) < s.idleTimeout { continue } // default 5m, flag-configurable
// then: emit type "stalled" ("⚠️ No events for 5m — tracking may have stopped"), NOT "Task Completed";
// at most one stalled notification per session per turn (anti-flap).
```
Also delete the dead `Status == StatusWaitingPermission` disjunct at [store.go:553](../../../internal/state/store.go) (unreachable given the :551 gate) and remove or wire the dead `UpdateSubagentActivity`.

- [ ] **1.3 `PostToolUse`/`PostToolUseFailure` recovery semantics:** after deleting the tool id — if the completing `tool_use_id` (or `tool_name`) matches the pending question tool, set `PendingQuestion = nil`; if `ActiveToolIDs` is empty and no `Stop` has fired this turn, set `Status = StatusActive` with `"Processing results"` (restores from a previous idle).
- [ ] **1.4 Subagent identity:** add `AgentID string \`json:"agent_id"\`` to `HookPayload` ([models.go:60-75](../../../internal/models/models.go)); key `ActiveSubagents` by `agent_id` when present; `SubagentStop` completes exactly that agent — remove the complete-all fallback (keep it only under `Stop`).
- [ ] **1.5 Notification type drift:** replace `notification_type == "waiting_input" || "permission_prompt"` ([store.go:422](../../../internal/state/store.go)) with the documented current set (`permission_prompt`, `agent_needs_input`) **plus** message-string matching (e.g. contains "permission"/"needs your input"). Confirm with a one-day raw-payload log pass in `handleHookPost` before finalizing.
- [ ] **1.6 Watcher dedup & hygiene:** set `Source:"watcher"` on synthesized events; keep a per-session seen-`tool_use_id` set (LRU 500) in the store — watcher PreToolUse for a seen id updates nothing and **never calls `AddNotification`**; handle truncation (`stat.Size() < offset` → reset 0); on first sight, only create sessions for transcripts modified in the last 10 minutes (kills the boot-time resurrection storm from RC-S3/RC-M6).
- [ ] **1.7 Session hygiene:** evict sessions with no activity for 24 h (keep newest 20 in the snapshot list); downgrade `waiting_permission` to idle + `PendingQuestion.Stale=true` after 30 min of inactivity (fixes RC-M3's phantom banner); make the "no working session" fallback deterministic (most-recent `LastActivity` instead of `sessionList[0]`).

**Verify (run and capture output):** `scripts\test-mock-hook.ps1` full lifecycle; the long-tool race — POST a `PreToolUse` (Bash, tool_use_id t1), wait 30 s, `GET /api/status` must still show `active` with t1 in flight; then `PostToolUse` t1 → `active`/“Processing results”; `Stop` → idle + exactly one task_done. Two parallel subagents (PreToolUse Task ×2, SubagentStop with the first `agent_id`) → only that one completed. Watcher dedup: with hooks working, a Task launch buzzes once (inspect `notifications` in `/api/status`). `go build ./...` + `go test ./...` clean; baseline `npm --prefix mobile run build` unaffected.
**Acceptance:** zero false "Task Completed" during a real ≥2-minute tool run; answered questions clear within one event; parallel subagents tracked independently.

### M2 — Durable ingestion: spool + append-only event log + replay

**Files:** Modify [internal/hooks/installer.go](../../../internal/hooks/installer.go) (bridge template), [internal/state/store.go](../../../internal/state/store.go), [cmd/server/main.go](../../../cmd/server/main.go), [internal/watcher/filewatcher.go](../../../internal/watcher/filewatcher.go).

Design choice (critic): **append-only raw-event log replayed at boot** — NOT snapshots of derived state (a snapshot bakes the watcher's wrong inferences in permanently).

- [ ] **2.1 Bridge spool:** on request error/timeout, append the serialized payload as one line to `~/.claude/claude-remote-hook.spool.jsonl` (bound: keep last 1000 lines — read+trim if larger), then `exit 0`. Must stay well under the 1.5 s script budget: synchronous append, no fsync.
- [ ] **2.2 Server drain:** on startup and every 10 s, if the spool exists: read line-by-line → `HandleHookEvent` → truncate. Dedup via the M1 seen-set keyed by `(session_id, event, tool_use_id/agent_id)` so a spooled event replayed after the watcher already saw the transcript is a no-op.
- [ ] **2.3 Raw-event log:** `Store.HandleHookEvent` appends every accepted payload to `~/.claude/claude-remote-events.jsonl`. On startup, if the log exists, replay the last 24 h of events to rebuild sessions/subagents/notifications (bounded: 10k events cap), then start live ingestion. This replaces the watcher as the recovery mechanism; the watcher remains a live-tail redundancy for hook failure only.
- [ ] **2.4 Persist watcher offsets** to `~/.claude/claude-remote-offsets.json` (save every 30 s) so restarts don't re-tail history.

**Verify:** start server → kill it (Ctrl+C) → send 3 hook events via a direct `Invoke-RestMethod` to a spool-testing variant or by running the bridge script manually with stdin JSON while the server is down → confirm spool file has 3 lines → restart server → `/api/status` shows the events replayed in order, spool truncated, no duplicate notifications. Kill the server mid-run, restart, and confirm the in-flight tool and pending question survive (event-log replay). `go build ./...` clean.
**Acceptance:** no hook event is ever permanently lost to a server restart; state after restart matches pre-restart for live sessions.

### M3 — Windows server lifecycle (survives window-close, logoff, reboot)

**Files:** Modify [cmd/server/main.go](../../../cmd/server/main.go), [internal/api/server.go](../../../internal/api/server.go); docs [README.md](../../../README.md) / [hand-off.md](../../../hand-off.md).

Lifetime ceiling (stated explicitly, critic): tracking can only exist while Claude Code itself runs, i.e. in the user session. A SYSTEM service buys nothing over a **logon-triggered autostart**; "survives reboot" means "starts at logon after reboot". When the PC sleeps, there is nothing to track — the design does not promise otherwise.

- [ ] **3.1 `-install` / `-uninstall` flags** → register a logon Scheduled Task (zero new deps):
  `schtasks /Create /TN "ClaudeRemoteServer" /TR "\"<abs-exe>\" -port 9280 -log-file \"%USERPROFILE%\.claude\claude-remote-server.log\"" /SC ONLOGON /RL LIMITED /F`
  Document the WinSW/NSSM alternative for owners who want a true service with auto-restart-on-crash (do not build it).
- [ ] **3.2 Single-instance guard (zero-dep):** on `ListenAndServe` bind failure, probe `http://127.0.0.1:<port>/api/health`; if it answers with our `ServerVersion`, log "already running" and `exit 0` — never `log.Fatalf` (fixes the duplicate-launch confusion, RC-S5).
- [ ] **3.3 Console-close handling:** add `golang.org/x/sys/windows.SetConsoleCtrlHandler` (flag to owner — or accept the zero-dep fallback): on CTRL_CLOSE_EVENT/CTRL_LOGOFF_EVENT run a bounded (≤3 s) shutdown — `srv.Shutdown(ctx)`, stop watcher. Since M2 appends per-event, an abrupt X-kill loses nothing; this is polish, not correctness. Replace the current `os.Exit(0)` goroutine ([main.go:90-97](../../../cmd/server/main.go)) with a real shutdown path either way.
- [ ] **3.4 File logging:** `-log-file` flag → `log.SetOutput(io.MultiWriter(os.Stdout, file))`, size-capped (5 MB, keep one rotation). All startup `fmt.Println` diagnostics go through the logger.
- [ ] **3.5 `/api/health`** endpoint: `{status, version, uptime_s, last_event_at}` — consumed by the APK staleness watchdog (M4) and useful for the guard above.
- [ ] **3.6 Docs:** replace hand-off.md "Option 3: Start-Process -WindowStyle Hidden" with `-install` instructions; state the lifetime ceiling.

**Verify:** `claude-remote-server.exe -install` → `schtasks /Query /TN ClaudeRemoteServer` shows the task; log off/on → server running (`tasklist`, `/api/health` OK); close the console window mid-run → with 3.3 the shutdown line logs; relaunch the exe while running → exits 0 with "already running". Baselines still green.
**Acceptance:** closing any window or rebooting never ends tracking past the next logon; a second launch never produces a fatal-looking error.

### M4 — Android Foreground Service with native WebSocket + notification ownership

**Files:** Create `MonitoringService.kt`, `BootCompletedReceiver.kt`; Modify [MainActivity.kt](../../../mobile/android/app/src/main/java/com/claudecode/remote/MainActivity.kt), [NotificationHelper.kt](../../../mobile/android/app/src/main/java/com/claudecode/remote/NotificationHelper.kt), [AndroidManifest.xml](../../../mobile/android/app/src/main/AndroidManifest.xml), `app/build.gradle` (okhttp dep — flagged), [network_security_config.xml](../../../mobile/android/app/src/main/res/xml/network_security_config.xml) (cleanup), [mobile/src/App.tsx](../../../mobile/src/App.tsx) + [websocketService.ts](../../../mobile/src/services/websocketService.ts) (token plumbing, URL handling), [ConnectionModal.tsx](../../../mobile/src/components/ConnectionModal.tsx) (token field).

**The architectural decision (critic gap 2): the WebSocket client and notification-trigger logic MOVE OUT of the WebView into native Kotlin inside the Foreground Service.** A WebView inside an FGS is still throttled when not resumed — keeping the client in JS reproduces the exact bug. The WebView keeps its own JS connection for UI **while the app is foregrounded** (the server already handles multiple subscribers; smallest diff, no binder/injection layer needed).

FGS type decision (critic gap 1): **`dataSync`** (fits "monitoring stream", sideload-friendly). Documented constraints: Android 14 requires the type + `FOREGROUND_SERVICE_DATA_SYNC` permission; **Android 15 caps cumulative dataSync FGS at ~6 h** — handle `onTimeout()` (API 35+) by posting a "Tracking paused after 6 h — tap to resume" notification and stopping foreground; `MainActivity` restarts it. dataSync-from-BOOT_COMPLETED is restricted on newer releases: the boot receiver is best-effort (try/catch), documented.

- [ ] **4.1 Manifest:** permissions `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_DATA_SYNC`, `RECEIVE_BOOT_COMPLETED`; `<service android:name=".MonitoringService" android:foregroundServiceType="dataSync"/>`; `<receiver>` with `BOOT_COMPLETED` (gated on a user "start at boot" pref, default off).
- [ ] **4.2 `MonitoringService`:** `START_STICKY`; `startForeground(NOTIFICATION_PROGRESS_ID, …, FOREGROUND_SERVICE_TYPE_DATA_SYNC)` — the existing ongoing notification becomes the FGS notification (one notification, not two). Native OkHttp WebSocket with: token auth (subprotocol from M0), exponential backoff reconnect (1 s→60 s, jitter), **ping watchdog** (server pings every 20 s — add the server side: ping ticker + `SetReadDeadline` + `SetPongHandler` in [server.go:145-151](../../../internal/api/server.go), also fixes RC-A6 on LAN), staleness marking on the notification (`"Last update HH:MM — reconnecting"`, `setOngoing(false)` while disconnected), and cancel/downgrade in `onDestroy` + `onTaskRemoved` keeps running.
- [ ] **4.3 Notification ownership goes native:** `NotificationHelper` gains `markTrackingInterrupted()`; the service parses `initial_state`/`session_update`/`notification` frames itself (small JSON data classes mirroring `types.ts`) and posts heads-ups + ongoing updates. JS `notificationService` remains for foreground chimes/haptics only.
- [ ] **4.4 Missed-alert replay (fixes RC-A5):** persist `lastSeenNotificationId` (SharedPreferences; notification IDs are `notif-<UnixNano>` — comparable). On every `initial_state`, diff `notifications` newer than the watermark → fire each missed alert natively, then advance the watermark. Server stamps snapshots with server time for "as of" display.
- [ ] **4.5 Doze/OEM survival matrix (document in-app + README):** FGS keeps the process alive; the **battery-optimization exemption** (already requested) grants network + wake locks in Doze; OEM autostart managers (Xiaomi/Oppo/Vivo) may still kill — the app surfaces a one-time "allow autostart" hint per OEM. No permanent wake lock (anti-pattern).
- [ ] **4.6 Client cleanups:** remove the hardcoded `192.168.100.48` fallback (default to empty → ConnectionModal on first launch); add `visibilitychange`/`online` handlers calling an `ensureConnected()`; auto-failover across `host_ips` after N failed attempts; add the token field; fix `network_security_config.xml` (remove the inert CIDR entries; keep global cleartext only while LAN-ws support is needed, tighten to `wss`-only when Part C lands).

**Verify (on device, capture screenshots/logcat):** with a long mock tool running (mock hook script), `adb shell dumpsys deviceidle force-idle` then `adb shell am kill com.claudecode.remote` → ongoing notification keeps updating and heads-ups still arrive (FCM not required for this path); force-stop → notification cleared or honestly marked stale; back-press → tracking continues (service alive); reboot → boot receiver restores (pre-Android-15) or documented pause; `gradle assembleDebug` + `npm --prefix mobile run build` green.
**Acceptance:** closing/swiping/Dozing the app never stops notification delivery while the phone has network; the shade never shows a pinned stale "Working" state; alerts missed while disconnected fire on reconnect.

---

## Part C — Remote access: forward-tunnel vs relay/broker (with VPN-overlay interim)

### C.1 Hard prerequisite

**No option below may be deployed before M0 (token auth + CheckOrigin + CORS).** Every mechanism "works" today only because the server trusts everyone; publishing it turns a LAN leak into an internet leak (anyone who learns the URL can read project names, file paths, shell command lines, and POST fake state / mutate hooks). Also mandatory once a public hostname is used: switch the APK to `wss://`/`https://` (all relay/tunnel options below terminate TLS at their edge) and add the server-side WS ping (M4.2) — intermediaries reap idle sockets (Cloudflare ≈100 s; Tailscale `serve` has a documented WS idle bug #18827; Render spins down after 15 min).

### C.2 Comparison matrix (facts as of Aug 2026 — re-verify pricing/limits at implementation)

| Option | Mechanism & stable endpoint | Latency | Security / auth | Complexity | Reliability | Cost (Aug 2026) | WS behavior | Android impact | Extends to control? |
|---|---|---|---|---|---|---|---|---|---|
| **Relay — custom Go WSS broker** (desktop dials OUT; rooms keyed by token; phone joins same room) on Fly.io / Railway / VPS | PC opens outbound WSS; relay routes frames desktop↔phone. Survives CGNAT/IP changes; no PC ports | +1 hop; ~10–60 ms if relay near phone (estimate) | Token per room; TLS at platform; relay sees plaintext unless E2E added (Happy uses tweetnacl) | ~300–600 LOC Go, reuses gorilla/websocket; single Dockerfile/bin | Always-on machine = high; single-region SPOF acceptable | Fly ~$2–5/mo; Railway Hobby **$5/mo** (no usable free tier for always-on); VPS $0–6/mo | Core product; keep ~30 s pings | wss URL + token only | **Excellent** — phone publishes to the room, desktop's outbound conn receives it (Omnara proves the pattern) |
| **Relay — managed MQTT** (EMQX / HiveMQ Cloud Serverless) | PC = MQTT client over WSS (8084/8884), publishes retained state to `u/<token>/state`; phone subscribes | Comparable to custom relay; retained message = instant last-known-state on connect | Per-client credentials + TLS; topic token = the secret (ACL granularity limited) | **Lowest server-side effort** — zero broker code | Managed cluster; no SLA on free tier; quota stops at spend-limit 0 | **$0** (EMQX: 1M session-min + 1 GB/mo; HiveMQ: 100 conns + 10 GB/mo) | First-class WSS; keepalive required | MQTT.js in WebView (or native in service) | Yes — phone publishes to `u/<token>/cmd` |
| **FCM high-priority data messages + on-demand WS** (add-on to any relay; or desktop-only) | Google push rides Play-services connection — **delivered in all app states incl. killed/Doze** | Sub-second when awake | OAuth2 service account on PC; payload transits Google (no secrets in data fields) | Medium: Firebase project, `FirebaseMessagingService` in Kotlin, HTTP v1 sender from Go | The only path that reliably defeats Doze **and** OEM killers; caveats: auto-deprioritization if no visible notification (ours always posts one), OEM blockers, 100-msg collapse | **$0** | N/A (not a transport) | Largest one-time change: google-services.json + Kotlin service (also FIXES the alert half of Part A independent of FGS!) | Yes (tap → open live view) |
| **Tailscale tailnet** (VPN overlay; interim, zero-code) | PC + phone join tailnet; phone hits `http://100.x.y.z:9280` like LAN | P2P: raw-path RTT (+1–2 ms); DERP fallback ~100–200 ms | **Nothing exposed publicly**; WireGuard in transit; SSO-gated | **Very low** — two app installs, same identity | Very high; DERP guarantees connectivity | **$0** (personal: 6 users, unlimited devices) | Transparent L3; no idle issue on direct path | **APK unchanged** (enter tailnet IP) | Yes (same as LAN) |
| **Cloudflare Tunnel (named, cloudflared + own domain)** | PC's cloudflared dials out to CF edge; `wss://status.<domain>` → localhost:9280 | Edge POP near both ends; ~10–60 ms reported | TLS at edge; **Cloudflare Access service tokens free** (50) as an auth layer in front; WebView seeds the CF cookie via a headers-fetch, then WS | Medium: cloudflared service + config.yml + domain DNS + (optional) Access app | Production-grade; 4 redundant edge conns; no traffic caps | **$0** + domain ~$10/yr | **Idle WS reaped ≈100 s** (non-Enterprise) → pings mandatory | wss URL (+Access cookie bootstrap) | Yes (same WS both ways) |
| **ngrok** (free static dev domain / $10 Hobbyist) | Agent dials out; `wss://<name>.ngrok-free.app` | Tens of ms (POP); spikes reported under load on free | TLS; free traffic-policy basic-auth — **but basic-auth can't ride a WS handshake**; app-level token still needed | Low (agent as Windows service) | Managed POPs; free endpoints no longer time out | **$0** (1 GB / 20k req / mo) or $10/mo | Native WS | **Risk: free-tier interstitial may break WebView WS handshake** (JS cannot set the skip header on WS) — test first | Yes |
| **Self-hosted tunnel: frp (frps on VPS + frpc on PC) + Caddy** | frpc dials out; Caddy terminates TLS on VPS; `proxyBindAddr=127.0.0.1` keeps the app port off the internet | 2 legs: ~20–90 ms same-region (est.) | frp token + TLS control channel + Caddy TLS + app token (layered) | Medium-high: VPS + 2 binaries + configs (~1–2 h) | Best of self-hosted: auto-reconnect, health checks, no stale-port mode | VPS: $0 (Oracle Always Free — capacity-constrained, halved to 2 OCPU/12 GB in 2026) / ~$1/mo RackNerd / ~€6 Hetzner + domain | Transparent pipe; no idle reap by frp/Caddy | wss URL + token | Yes |
| **Self-hosted tunnel: reverse SSH (`ssh -R`) + Caddy** | Windows built-in ssh.exe dials out; Caddy on VPS | Same 2 legs as frp | Same layered story | Lowest tooling (built-in ssh) **but** NSSM/Task-Scheduler babysitting, ~90 s dead-tunnel detection, stale-forward failure mode | Weakest of the self-hosted set | Same VPS economics | Raw TCP relay — WS passes untouched | wss URL + token | Yes |
| **Ruled out** | trycloudflare quick tunnels (random URL each run), localhost.run free (rotating domain; $9/mo for stability), boringproxy (project in maintenance mode; site DNS failing Aug 2026), plain WireGuard-to-home (needs reachable endpoint — non-viable behind CGNAT), Telegram-bot-as-relay (free & CGNAT-proof but bypasses the APK product goal) | — | — | — | — | — | — | — | — |

### C.3 Reading the matrix

- **Forward-tunnel family** (Cloudflare, ngrok, frp, ssh-R) exposes *the PC's server* through an edge. Strengths: no new application code (the APK just changes URL), mature tooling, low build cost. Weaknesses: the endpoint's uptime is chained to the PC process and the tunnel daemon; every option still requires app-level token auth; managed tunnels impose idle-WS reaping (keepalive becomes load-bearing); self-hosted variants add a VPS you maintain for roughly the same money as a relay host.
- **Relay/broker family** inverts it: the desktop *dials out* to a small broker and the phone joins the same room. Strengths: CGNAT/IP-change-proof by construction; one stable endpoint owned by the product; per-token rooms give clean multi-device auth; **it is the natural substrate for the roadmap's "answer questions from the phone"** (the phone publishes a frame to the room — no new PC-side attack surface); a relay can buffer while one end is offline; managed-MQTT variants give last-known-state (retained messages) and offline detection (LWT) for free at $0. Weaknesses: you build/operate a second component (or accept a managed broker's quotas); +1 hop latency (irrelevant at sub-Hz status traffic); a custom relay sees plaintext unless E2E is added (Happy's tweetnacl pattern) — acceptable for this data, and the token gates the room.
- **VPN overlay (Tailscale)** is the honest zero-code interim: the APK works **unchanged** today against the tailnet IP (the server binds 0.0.0.0; `GetLocalIPs()` even auto-advertises the 100.x address), WireGuard encrypts the cleartext WS in transit, and nothing is publicly exposed. Real costs: the phone must run the Tailscale app **permanently, occupying Android's single VpnService slot** (disqualifying if the owner uses another VPN daily), a standing ~2–3%/day battery cost, and it is inherently single-user — it cannot become the product's remote architecture.
- **FCM is orthogonal and additive** — pair it with whichever transport wins. It is the only mechanism that delivers alerts when the app process is dead (Doze/OEM-proof), and it single-handedly fixes the "alert half" of Part A even before the FGS lands.

### C.4 Recommendation

> **DECISION (owner, 2026-08-31): Relay/Broker (desktop dial-out) adopted. Deployment and testing target: Railway, operated via the `/railway:use-railway` skill.** Tailscale interim (M5.0) deferred; Cloudflare alternative (M5c) rejected. Execution order remains M0 → M1 → M2 → M3 → M4 → M5.1/M5.2.

**Adopt the relay architecture, staged, with Tailscale as the immediate interim:**

1. **Today (zero code): Tailscale** — install on PC + phone, one identity; enter the tailnet IP in the APK. This unblocks remote monitoring immediately and stays useful as a fallback. (Skip if the phone's VPN slot is spoken for.)
2. **Strategic target: a relay the desktop dials out to + FCM for alerts.** Concretely, in priority order:
   - **M5.1 — Go WSS relay** (`cmd/relay/main.go`, ~300–600 LOC on gorilla/websocket): rooms keyed by the M0 token; the desktop server gains `internal/relay/client.go` (outbound WSS with token; registers as the room's desktop; forwards its broadcasts; later receives control frames). The APK adds "remote mode": connect to `wss://<relay>/ws` with the token subprotocol. Deploy on **Railway Hobby ($5/mo — the owner already has Railway tooling configured in this workspace)** or Fly.io (~$2–5/mo); a VPS works too.
   - **M5.2 — FCM hybrid**: `FirebaseMessagingService` in the APK + HTTP-v1 sender in the Go server for `permission`/`task_done`/`stalled` events (high-priority data messages). Alert delivery then no longer depends on any socket, any service, or Doze. Live UI connects on demand.
   - Optional **M5.3 — E2E encryption** of relay frames (Happy's tweetnacl pattern) if the relay will ever be operated by someone other than the owner.
3. **If the owner prefers not to build/operate a relay:** **Cloudflare Tunnel + Cloudflare Access** is the best tunnel alternative ($0 + ~$10/yr domain, free service-token auth at the edge, production-grade) — accepting the ~100 s idle-WS reaping (mitigated by the M4 ping) and that control features will ride the same exposed endpoint.
4. **Rejected:** ngrok free (interstitial risk against a WebView WS handshake is a coin-flip; $10/mo to fix), trycloudflare/localhost.run (unstable hostnames), plain WireGuard-to-home (CGNAT), boringproxy (maintenance mode).

**Why relay over tunnel, in one line each:** latency — both add one hop (tie, irrelevant at this traffic); security — relay centralizes token auth with no public PC endpoint, tunnels still need app auth added and expose the PC; complexity — tunnel is cheaper to *adopt*, relay is cheaper to *extend* (control, buffering, multi-device); reliability — relay decouples endpoint from PC/tunnel uptime and survives IP churn by construction; cost — $0 (managed MQTT) to $5/mo (managed PaaS) either way.

### C.5 Milestone M5 (remote access) task sketch

- [ ] **M5.0 Tailscale interim (docs only):** README section — install, sign in both devices, firewall note (allow the exe on the Tailscale adapter), enter `http://100.x.y.z:9280` + token in ConnectionModal. Verify: monitor works over cellular with Wi-Fi off.
- [ ] **M5.1 Relay:** implement `cmd/relay` (hub, token rooms, ping/pong, graceful reconnect) + `internal/relay/client.go` in the desktop server (flag `--relay wss://…`); APK "remote mode" URL; deploy; verify: kill and restart the desktop server — phone reconnects and replays missed alerts (M4.4 watermark); mock-hook lifecycle green end-to-end over the relay; latency spot-check.
- [ ] **M5.2 FCM:** Firebase project + `google-services.json`; Kotlin `FirebaseMessagingService` posting heads-ups; Go HTTP-v1 sender for `permission|task_done|stalled`; verify: force-stop the app → send a permission mock event → notification arrives.
- [ ] **M5.3 (optional) E2E frames** per Happy's pattern.

---

## Verification plan (overall) and proposed rubric additions

Per-milestone acceptance criteria are stated above; additionally propose adding to `.instructions/evaluator-rubric.md` (human-maintained — owner to approve):

- *Persistent tracking:* with the app closed/Dozed (`adb shell dumpsys deviceidle force-idle`, `adb shell am kill`) and a ≥2-min mock tool running, every state transition still reaches the notification shade; zero false "Task Completed" notifications across a full mock lifecycle; killing and restarting the server mid-run loses no events (spool/event-log evidence in logs).
- *Remote access:* monitoring works over cellular (Tailscale interim; then relay) with Wi-Fi off; no endpoint answers without a token; `wss://` only for public hostnames.

**Facts to re-verify at implementation time** (fast-drifting, all Aug-2026-dated): Railway/Fly/Render pricing & spin-down; ngrok plan names/quotas; Cloudflare WS idle timeout (~100 s non-Enterprise); EMQX/HiveMQ serverless quotas; FCM auto-deprioritization policy; Claude Code hook event list vs installed version; Android 15 dataSync 6-hour cap behavior on the target device; Tailscale free-tier device limits.

---

## Appendix — Evidence & sources

- Diagnosis workflow `wf_5bdfc6d3-309` (15 agents, 2026-08-31): journal at `C:\Users\alipb\.claude\projects\d--CODING-claude-status-apk\2070565b-97a1-441e-aef6-793342158f09\subagents\workflows\wf_5bdfc6d3-309\journal.jsonl`. 26 claims CONFIRMED with file:line evidence (key files re-read first-hand: `store.go`, `MainActivity.kt`, `websocketService.ts`, `main.go`); 1 REFUTED (§A.4).
- Deployed artifacts inspected: `~/.claude/settings.json` (12 hook events; `API_TIMEOUT_MS=3000000`), `~/.claude/claude-remote-hook.js` (1500 ms, exit-0 drop).
- Research sources (primary ones): ngrok free-plan limits & domains docs; Cloudflare Tunnel / Access service-token / WebSocket-timeout docs & community; Tailscale pricing (v4), connection-types, DERP, Android battery & VPN-coexistence pages; frp config docs (proxyBindAddr, reconnect issue #5341); Caddy reverse_proxy docs; Render free-tier docs; Railway pricing; EMQX/HiveMQ serverless docs; Firebase FCM priority/throttling docs; reference implementations: `omnara-ai/omnara` (agent-dials-out WS relay, subprotocol auth, FCM) and `slopus/happy` (E2E relay, QR pairing, FCM).
