# Project Roadmap & Feature Status

> **Pattern**:
> - `[✅]` Completed / Passing
> - `[🔄]` In Progress
> - `[⛔]` Blocked
> - `[ ]` Need to Implement

---

## Phase 1: Core Go Server Backend
- [✅] Go HTTP Server with `0.0.0.0:9280` local LAN listener
- [✅] In-memory Thread-Safe State Store with `sync.RWMutex`
- [✅] Real-time WebSocket broadcasting (`/ws`) via `gorilla/websocket`
- [✅] Liveness auto-checking & 4s auto-idle task completion engine
- [✅] Local IPv4 discovery and ASCII / PNG QR Code generator (`/api/qr`)
- [✅] Automatic Claude Code Hook installer for `~/.claude/settings.json`
- [✅] Redundancy JSONL transcript file watcher (`~/.claude/projects/`)
- [✅] Embedded fallback Web Dashboard (`web/index.html`)

## Phase 2: React Mobile UI & State Management
- [✅] Responsive Dark Theme UI with TailwindCSS (`#0d0e12` palette)
- [✅] Dynamic Status Hero (Color glowing bounce for Active vs Monochrome for Idle)
- [✅] Sub-Agent Activity Inspector with collapsible history and role badges
- [✅] Golden Question & Permission Request Banner (`AskUserQuestion` & `ExitPlanMode`)
- [✅] Floating Live Stream Mini-Player Bar & Expandable Drawer
- [✅] Battery Optimization warning banner with direct OS settings intent
- [✅] Real-time Activity Logs viewer with circular buffer
- [✅] Web Audio API procedural chimes & Haptic vibration feedback

## Phase 3: Android Native Integration (Kotlin)
- [✅] Android WebView host with `WebViewAssetLoader`
- [✅] JavaScript-to-Kotlin `AndroidBridge` interface
- [✅] Ongoing persistent notification bar in Android notification shade (`claude_tasks_ongoing`)
- [✅] Heads-up alert popup notifications for permissions and task finish (`claude_alerts_heads_up`)
- [✅] Direct Chrome Remote Desktop app launcher intent
- [✅] Battery optimization whitelist prompt (`ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`)

## Phase 4: CI/CD & Build Pipeline
- [✅] GitHub Actions APK automated build workflow (`.github/workflows/build-apk.yml`)
- [✅] GitHub Actions multi-platform Go binary matrix (`.github/workflows/build-server.yml`)
- [✅] Windows batch build and run helper scripts (`scripts/build-go.bat`, `scripts/run-server.bat`)
- [✅] PowerShell mock hook simulation testing harness (`scripts/test-mock-hook.ps1`)

## Phase 5: Persistent Background Tracking (planned 2026-08-31 — full design in `docs/superpowers/plans/2026-08-31-persistent-tracking-and-remote-access.md`)
> Build order is a dependency chain: M0 → M1 → M2 → M3 → M4. Root causes verified 2026-08-31 (26 claims confirmed against code).
- [✅] M0: Server token auth + `CheckOrigin`/CORS hardening — accepted by owner 2026-08-31 (verification evidence in claude-progress.md; review Approved after 1 fix round)
- [✅] M1: Event-driven state machine — implemented & verified 2026-08-31 (commit 2cb78f7; review Approved; live: long-tool immune to auto-idle, stall fallback emits `stalled` never false task_done)
- [✅] M2: Durable ingestion — implemented & verified 2026-08-31 (commits 3c5544c + c113351; review Approved after fix round; live: abrupt kill mid-tool → restart → state reconstructed via event-log replay)
- [✅] M3: Windows lifecycle — implemented & verified 2026-09-01 (commit ab72f22; review Approved; live: /api/health, duplicate-launch exit-0, -log-file rotation; NOTE: owner runs one elevated `-install` for ONLOGON acceptance)
- [✅] M4: Android Foreground Service — DONE 2026-09-01 via M4a (a200d6b) + M4b (7b19e57) + compile fix (b1312ec). Reviews Approved. **CI APK build SUCCESS** — artifact `claude-remote-android-apk` (5.6 MB)

## Phase 6: Remote Access beyond LAN — DECISION 2026-08-31: Relay/Broker (desktop dial-out), deploy & test on Railway via `/railway:use-railway`
- [ ] M5.0 (optional interim): Tailscale setup documentation — deferred; relay path chosen
- [✅] M5.1: Go WSS relay — BUILD DONE (7d59fc3 + d3cf3f0, review Approved) + DEPLOYED LIVE on Railway 2026-09-01 (project claude-code-remote, service claude-remote-relay, https://claude-remote-relay-production.up.railway.app; desktop dial-out joined room; domain auth-gated). APK remote mode = enter relay URL + token in ConnectionModal
- [✅] M9: Task-completed mechanism — transcript-based turn-end (stop_reason=end_turn) + "✅ Task completed" state + green UI badge — DONE 2026-09-01 (4f0798b, review Approved, live-verified)
- [✅] M10: README professional English + LICENSE GPL-3.0 + untrack local docs — DONE 2026-09-01 (973c6ee + 08ec982)
- [✅] M11: Web dashboard rewrite (token auth, copy URL/token, Task-Completed state, Relay-URL runtime input) + proper camera permission + full English UI — DONE 2026-09-01 (619bdcf + 0befb96 + 854f5aa + e3a714d; review Approved; relay-config dogfooded live; CI SUCCESS). Relay outage 2026-09-01 (deployments removed on Railway) restored via redeploy.
- [ ] M5.2: FCM high-priority data-message alerts (Doze/OEM-proof alert path; orthogonal add-on)
- [ ] M5.3 (optional): E2E encryption of relay frames (Happy/tweetnacl pattern)
- [ ] M6.1 (owner, 1x): ganti sumber template Railway ke **Docker image publik** `ghcr.io/alipbudiman/claude-remote-relay:latest` (CI sudah mem-publish otomatis tiap push; pull anonim terverifikasi 200) → hilangkan gate "Trying to access GitHub repository" → tombol deploy one-click aktif untuk publik (ganti YOUR-TEMPLATE-ID di README)
- [✅] M7: relay presence feedback + LAN IP ranking + relay-aware autostart + GHCR image — DONE 2026-09-01 (commit 8d32208; report .superpowers/sdd/task-M7-report.md). room_status{peers:0} on empty-room join → APK amber "waiting for your PC" banner; GetLocalIPs ranks 192.168 > 10 > 172.16/12 (QR no longer picks WSL/Hyper-V IP); `-install` bakes `--relay` into the task; publish-relay-image CI job → ghcr.io/<owner>/claude-remote-relay (first push to main publishes :latest/:main — template can switch to image source for M6.1); README troubleshooting (ID) added. NOTE: relay on Railway must be redeployed to the new commit before phones get the banner.
- ~~M5c: Cloudflare Tunnel + Access~~ — not chosen (relay selected 2026-08-31)

## Phase 7: Remote Interaction & Live Process View (2026-09-03 — full plan in `docs/superpowers/plans/2026-09-02-remote-interaction-and-live-process-view.md`)
- [✅] Live process view: ProcessEvent ring (hooks tool_input/tool_response + watcher thinking/text/tool_result) → `process_event` frames → ProcessFeed UI with fixed-height scroll boxes
- [✅] Approval mechanism: PermissionRequest decide long-poll (Allow/Deny/Always-Allow + note), bypassPermissions fast-path, terminal-prompt timeout fallback
- [✅] Question/option selection: AskUserQuestion allow+updatedInput answers echo (multiSelect + notes); ExitPlanMode approve/reject with plan pane
- [✅] Permission settings: phone editor for settings.json defaultMode + allow/ask/deny rules (foreign keys preserved, live-applied by Claude Code)
- [✅] Mid-task prompt injection: phone-queued prompts delivered at turn end via Stop `decision:block` (stop_hook_active-guarded, matches Remote Control queue semantics)
- [✅] Log clearing + auto-clear: manual clear (banner/button) + age-based purge at Off/5/15/30 min
- [✅] Command path: client_command frames over /ws AND relay (relayclient.OnClientFrame) + REST mirrors; app settings persisted in ~/.claude/claude-remote-settings.json
- [✅] Verification: `go test ./... -count=1` 9/9 packages ok; `npm --prefix mobile run build` ✓; mock decision+prompt+feed flows PASS live on :9280; installer preserves foreign hooks, registers --decide entries (Stop=30s, PreToolUse/PermissionRequest=120s)
