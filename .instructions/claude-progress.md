# Progress Log

## 2026-08-31 — Session: Root-cause analysis + remote-access plan (NO code changes)

Scope: investigation and planning only, per owner instruction ("Do not implement anything yet — first analyze root cause and produce a plan/comparison"). No feature moved to in-progress; new Phase 5/6 entries added to TODO.md as not-started.

### Verification actually run (baseline health, all green)
- `go build -o /tmp/claude-remote-server-baseline.exe ./cmd/server` → `GO_BUILD_OK`
- `npm --prefix mobile run build` → `✓ built in 8.89s` (1605 modules; dist hash matches committed android assets `index-BvbSqJPW.js`)
- `git log --oneline -15` / `git status --porcelain` → main @ `16468aa`; tree clean except pre-existing `M .gitignore` (adds ignore for CLAUDE.md/.instructions/hand-off.md — not this session's change, left untouched)
- TODO-marker scan (`TODO:` across go/ts/tsx/kt/js) → no matches

### Investigation performed
- 15-agent diagnostic workflow `wf_5bdfc6d3-309` (5 subsystem diagnoses, each adversarially verified against actual code; 4 web-research topics; 1 completeness critic): 26 claims CONFIRMED, 1 REFUTED (broadcast-lossy/stale-client hypothesis — excluded from RCA). ~1.56M subagent tokens, 318 tool uses, 0 errors. Journal: `C:\Users\alipb\.claude\projects\d--CODING-claude-status-apk\2070565b-97a1-441e-aef6-793342158f09\subagents\workflows\wf_5bdfc6d3-309\journal.jsonl`
- First-hand re-read of pivotal files to cross-check agents: `internal/state/store.go`, `MainActivity.kt`, `websocketService.ts`, `cmd/server/main.go`
- Root causes (3 failure surfaces): (A) phone-side tracking lives in WebView JS of a service-less app → dies on app close, pinned stale "Working" notification, missed alerts never replayed; (S) server is a console-attached process with silent-drop hooks and in-memory-only state → dies on window close, nothing rebuilt; (M) 4s liveness auto-idle + PostToolUse-never-restores + missing UserPromptSubmit hook + unparsed agent_id → false "Task Completed" mid-run (the "sometimes" flavor).

### Artifacts produced
- `docs/superpowers/plans/2026-08-31-persistent-tracking-and-remote-access.md` — Part A RCA (evidence file:line), Part B fix design (M0–M4 with code sketches + acceptance criteria), Part C tunnel-vs-relay comparison matrix + recommendation (relay + FCM target; Tailscale interim; Cloudflare Tunnel alternative)
- `.instructions/TODO.md` — Phase 5 (M0–M4) and Phase 6 (M5.x) added

### Broken / incomplete / unverified
- Nothing introduced (no code touched; baselines green).
- Runtime-drifting facts flagged in the plan for re-verification at implementation time: hosting pricing/spin-down (Railway/Fly/Render), ngrok interstitial vs WebView WS handshake, Cloudflare ~100s WS idle, EMQX/HiveMQ quotas, FCM policies, Claude Code hook event list vs installed version, Android 15 dataSync 6h cap.

### Proposed commit message (for owner review; not committed per CLAUDE.md §7)
`docs(plans): add persistent-tracking root-cause analysis and remote-access tunnel-vs-relay plan`

---

## 2026-08-31 — Session (lanjutan): M0 implemented — token auth + origin hardening

Owner decisions this session: (1) remote-access path = **Relay/Broker (desktop dial-out)**; (2) relay deploy & testing via **Railway** (`/railway:use-railway`; auth verified — account alipbudiman). Execution mode: superpowers subagent-driven-development (implementer subagent + adversarial task reviewer + fix loop), adapted to repo rules: **no agent commits** (reviews use working-tree diffs vs 16468aa).

### Files changed (all uncommitted, working tree)
- NEW `internal/auth/token.go` + `token_test.go` — 64-hex token from crypto/rand, `~/.claude/claude-remote-token` (0600), idempotent load
- `internal/api/server.go` + NEW `server_test.go` — `requireToken` middleware (Bearer → `?token=` → `Sec-WebSocket-Protocol: claude-remote.<tok>`, constant-time, fail-closed on empty), gated all `/api/*` + `/ws` (static `/` open), `originAllowed` shared by CheckOrigin+CORS (no-Origin / appassets.androidplatform.net / same-origin), upgrader subprotocol echo
- `internal/hooks/installer.go` — bridge reads token file sync, sends Bearer; 1500ms + silent-exit preserved; no spooling (M2)
- `cmd/server/main.go` — token load at startup, `?token=` in printed/QR URL, NewServer signature +token
- `scripts/test-mock-hook.ps1` — Bearer header on all POSTs
- `mobile/src/services/websocketService.ts` + `ConnectionModal.tsx` — localStorage `claude_server_token`, WS subprotocol, `fetchStatus ?token=`, Token field; fix round: `setServerUrl` strips query/hash and seeds token from pasted `?token=` URL

### Verification actually run (by controller, this session)
- `go build ./... && go vet ./...` → clean; `go test ./... -count=1` → `ok claude-remote-server/internal/api 0.569s`, `ok claude-remote-server/internal/auth 0.280s`
- Live gate check (server `-port 9299 --no-hooks --no-watch`): `no-token:401 · wrong-token:401 · bearer:200 · query-param:200 · root-static:200` (token file exists, len 64)
- `powershell scripts/test-mock-hook.ps1` against live server on 9280 → all events `ok`
- `npm --prefix mobile run build` → `✓ built in 2.15s` (dist hash `index-fk1fF3EB.js`)
- Implementer also ran live checks (Node WS handshake w/ echoed subprotocol, e2e hook delivery) — in task-M0-report.md

### Review trail
Round 1: Spec ✅, Quality "Needs fixes" — 1 Important (pasting `?token=` URL corrupted serverUrl) + 4 Minor. Fix round (websocketService.ts only) → Round 2: Spec ✅, **Approved**. Minor findings (6) rolled up in `.superpowers/sdd/progress.md` for the final whole-branch review. Deviation flagged to owner: `setToken("")` is now a no-op (token can be replaced but not cleared from UI until M4 rework).

### Status
- M0 code-complete & verified; TODO.md marked awaiting owner acceptance → ✅.
- Next: M1 (event-driven state machine) after owner confirmation; then M2, M3, M4, M5.1 (relay on Railway), M5.2 (FCM).

### Proposed commit message for M0 (for owner review; not committed)
```
feat(security): add token auth gate, origin/CORS allowlist, and WS subprotocol handshake

M0 of the persistence plan (docs/superpowers/plans/2026-08-31-...): shared-secret
token required on all /api/* and /ws; CheckOrigin/CORS restricted to no-origin,
APK WebView origin, and same-origin; hook bridge and mock-hook script send Bearer;
QR/terminal URL carries ?token=; mobile client stores token, dials WS with the
claude-remote.<token> subprotocol, and sanitizes pasted URLs (seeds token from query).
```


## Session 2026-09-01 — M0→M5.1 full pipeline execution (owner directive: "kerjakan M1-M4, deploy Railway, jalankan exe, build APK dari GitHub")

Owner amended CLAUDE.md §10: **direct git commits now allowed** (message reported, no AI attribution, watch GH Actions after push). Process: subagent-driven-development per milestone (implementer + adversarial reviewer + fix loop); commits per milestone.

### Milestones completed this session (all reviews Approved; every fix round red→green)
- M0 token auth — `48023cb` (accepted by owner; committed with docs plan `ab23bcf` + gitignore chore `32a2aca`)
- M1 event-driven state machine — `2cb78f7` (long tools never auto-idled; `stalled` fallback; agent_id; watcher dedup; hygiene)
- M2 durability — `3c5544c` + fix `c113351` (bridge spool, 24h event-log replay, tool_result synthesis, persisted offsets)
- M3 Windows lifecycle — `ab72f22` (`-install` ONLOGON via schtasks, single-instance guard exit-0, console-close handler, `-log-file` rotation, `/api/health`; cross-compile linux/darwin OK; NOTE: owner must run `-install` once from an elevated console for autostart)
- M4a Android FGS — `a200d6b` (MonitoringService dataSync FGS, native OkHttp WS, notification ownership, boot receiver, server WS pings; Kotlin compile gate = CI — no local gradle/JDK)
- M4b — `7b19e57` (stats heartbeat 20s, missed-alert replay watermark ≤10, host_ips failover, clear-token, URL validation, browser fallback restored)
- M5.1a relay — `7d59fc3` + fix `d3cf3f0` (cmd/relay room hub + internal/relayclient dial-out + root Dockerfile; ping-deadline churn fixed red-first)

### Verification actually run (by controller, this session)
- `go build/vet/test ./...` after every milestone → all packages `ok` every time (api, auth, state, watcher, cmd/server, cmd/relay, relayclient)
- Live drills: auth gate 401/401/200/200/200; long-tool immunity past idle-timeout; abrupt taskkill mid-tool → restart → session reconstructed via event log replay; /api/health JSON; duplicate-launch exit-0; relay /health ok + /ws 401s; relay join stable (no churn) after ping fix; npm build green each milestone
- Railway: deployment `174fddcf` **SUCCESS** (docker build 6/6, relay listening :8080); domain `claude-remote-relay-production.up.railway.app` verified; desktop join `room=dc375a14 members=1` in Railway logs
- Production run: `/tmp/claude-remote-server.exe -port 9280 -log-file ~/.claude/claude-remote-server.log` with RELAY_URL=wss://… (PID 40720): replayed 1082 events, hooks linked, watcher active, relay active — RUNNING at session end
- GitHub: pushed `16468aa..d3cf3f0`; CI "Build Go Server Binaries" d3cf3f0 **success**; "Build & Release Android APK" d3cf3f0 in progress (Kotlin compile gate) — result recorded below when known

### Runtime/ops notes for the owner
- Phone setup after APK install: ConnectionModal → URL `https://claude-remote-relay-production.up.railway.app` + token from `~/.claude/claude-remote-token` (or paste the QR/terminal URL on LAN). Monitoring now works from ANY network via the relay.
- Server autostart: run `claude-remote-server.exe -install` once from an elevated console (argv proven; non-elevated ONLOGON is denied by Windows).
- Relay cost: Railway Hobby plan usage (~$5/mo incl. credits); relay logs show room joins only (8-char hash, no tokens).

### Not done / deferred
- M5.2 FCM alerts, M5.3 E2E relay encryption (optional), final whole-branch review (39 minor findings rolled up in `.superpowers/sdd/progress.md`), device-level Android verification (install APK from Actions artifact → FGS behavior on real phone), elevated `-install` acceptance run, `-race` CI pass.

### Follow-up session (2026-09-01): online-access proof + M6 (README deploy button + APK Railway URL input)
- ONLINE ACCESS PROVEN LIVE: `phone_sim.go` (kept at `.superpowers/sdd/phone_sim.go`) connected from outside via `wss://claude-remote-relay-production.up.railway.app/ws?token=…` and received `initial_state`, live `session_update` for the controller's own Claude Code session (project d--CODING-claude-status-apk), a stalled notification, and a locally-POSTed PreToolUse round-tripped through the relay — full internet path working.
- M6 commit `2fc872f` (review Approved): README "Akses Online via Railway (Relay)" section (deploy button markdown w/ YOUR-TEMPLATE-ID + publish-once note + 4-step manual deploy + desktop `--relay` + phone setup + token security/rotation) + ConnectionModal "Railway URL (Online)" input (https prefill; Railway wins when both filled; LAN field relabeled). npm build green.
- Pushed `b1312ec..2fc872f`: APK CI SUCCESS (new artifact with Railway input), Go binaries SUCCESS.
- Railway service connected to GitHub `alipbudiman/claude-code-remote@main` → auto-deploy verified: 2fc872f deployed automatically (deployment 9226a597 SUCCESS); relay /health ok; desktop exe re-joined room dc375a14 7s after redeploy (reconnect backoff works through relay restarts).
- Owner action pending: publish Railway template once to activate the one-click button (README note box has the steps).

### CI gate result (final)
First APK run (d3cf3f0) FAILED at `:app:compileDebugKotlin`: wrong import package `android.app.ServiceInfo` (correct `android.content.pm.ServiceInfo`) and `onTimeout` missing `override` (Service.onTimeout(int) exists since API 34 — M4a premise wrong). Fixed in `b1312ec` (pushed). **Re-run: Build & Release Android APK SUCCESS** — artifact `claude-remote-android-apk` (5.6 MB, expires 2026-11-29) at https://github.com/alipbudiman/claude-code-remote/actions. Build Go Server Binaries also SUCCESS at b1312ec.

### Commit messages (all pushed in 16468aa..b1312ec)
ab23bcf docs(plans) · 32a2aca chore(gitignore) · 48023cb feat(security) · 2cb78f7 feat(state) · 3c5544c feat(state) · c113351 fix(state) · ab72f22 feat(server) · a200d6b feat(android) · 7b19e57 feat(android) · 7d59fc3 feat(relay) · d3cf3f0 fix(relay) · b1312ec fix(android)

---

## Session 2026-09-01 — M3: Windows server lifecycle (SDD task, BASE c113351)

### Worked on
M3 (autostart, single-instance guard, console-close handling, file logging, /api/health) per `.superpowers/sdd/task-M3-brief.md`. End state: code-complete, fully tested, live-verified.

### Implementation (TDD: tests written first, red captured, then green)
- `internal/api/server.go`: `ServerVersion` const; token-gated `GET /api/health` (status/version/uptime_s/last_event_at null-or-RFC3339); `Start()` now wraps a stored `*http.Server`; new `Shutdown(ctx)`; `handleHookPost` forces `payload.Source=""` (anti-spoof, folded M2-review item).
- `internal/state/store.go`: minimal `lastEventAt` field + `LastEventAt()` getter; stamped in `HandleHookEvent` for every accepted event.
- `cmd/server/install.go`(+test): `installTaskArgs`/`uninstallTaskArgs` pure builders (exact argv asserted); `-install` via `os.Executable()` + schtasks ONLOGON/LIMITED; `-uninstall` tolerates not-found ("cannot find" / 0x80070002); "Access is denied" gets an explicit elevated-console hint.
- `cmd/server/singleinstance.go`(+test): `isOurInstance` probes `/api/health` w/ Bearer; on Start bind failure another-instance-of-us → log "another Claude Remote Server instance is already running on port <port>; exiting" + exit 0.
- `cmd/server/logging.go`(+test): `openLogFile` (parent dirs, >5MB rotate to `.1`); `-log-file` flag wires `log.SetOutput(io.MultiWriter(stdout,file))` and reroutes diagnostics through `log.Printf`; QR banner/connectivity block stay stdout-only.
- `cmd/server/console_windows.go` (`//go:build windows`, x/sys windows.NewCallback + kernel32 SetConsoleCtrlHandler for CTRL_CLOSE/LOGOFF/SHUTDOWN; note: x/sys v0.47.0 has no SetConsoleCtrlHandler wrapper, called directly) + `console_other.go` no-op stub.
- `cmd/server/main.go`: `-install`/`-uninstall`/`-log-file` flags handled before startup; unified bounded (~3s) graceful shutdown (fw.Stop + srv.Shutdown + exit 0) behind `sync.Once`, fed by both signal.Notify and the console handler.
- Docs: README.md new "Auto-Start saat Login Windows" subsection; hand-off.md §"Server Deployment on PC" rewritten (-install flow, elevation note, lifetime ceiling, WinSW/NSSM alternative).

### Verification (actual outputs in .superpowers/sdd/task-M3-report.md)
- `go build ./...` OK, `go vet ./...` OK, `go test ./... -count=1` all 5 packages ok; `GOOS=linux GOARCH=amd64` and `GOOS=darwin GOARCH=arm64` builds OK.
- Live: duplicate launch on 9299 → exact "already running" line, exit 0. `/api/health` w/ Bearer → {"status":"ok","version":"1.0.0","uptime_s":22,"last_event_at":...}; w/o token → 401. `-log-file` startup lines in file. `-uninstall` live-verified (register→remove→query not-found; second uninstall tolerated, exit 0).
- `taskkill` w/o /F: Windows refuses WM_CLOSE for console apps ("can only be terminated forcefully") — graceful console-close path covered by unit test of Shutdown + code inspection, as the brief anticipated.
- `-install` on this machine: schtasks rejects `/SC ONLOGON` non-elevated ("Access is denied", with/without /RL LIMITED) — OS policy, not an argv bug (identical command shape verified registering successfully with a DAILY trigger; task storage shows correct action incl. %USERPROFILE% expansion). Documented in README/hand-off/report; needs one elevated run by the owner to complete end-to-end proof.

### Cleanup
No scheduled task left registered; test instance stopped; probe scripts removed; working tree = only M3 files.

### Commit (per brief, committed this session)
`feat(server): windows lifecycle management (logon autostart, single-instance guard, graceful close)`

## Session 2026-09-01 — M7 (relay presence, LAN IP ranking, relay-aware autostart, GHCR image, docs)

### Feature: M7 per `.superpowers/sdd/task-M7-brief.md` — commit `8d32208` (parent 39e445c)
- LAN IP ranking: `rankLANIPs` tiers 192.168.* > 10.* > 172.16-31.* > rest; `GetLocalIPs` returns ranked list (QR/primary URL fixed: virtual adapters no longer win the first slot). New `internal/network/lan_test.go`.
- `-install` now persists `--relay <url>` (flag or RELAY_URL) into the Scheduled Task command; prints relay-preserved / LAN-only hint. Tests cover both argv shapes.
- Relay `room_status` frame: first joiner of an empty room immediately receives `{"type":"room_status","data":{"peers":0},...}` (local struct, not models.WebSocketMessage). Phone: `room_status` in TS type union; App.tsx amber "waiting for your PC" banner (boolean + 1 element, cleared by initial_state/session_update/notification, gated on isConnected); MonitoringService.kt liveness-only case. 3 pre-existing relay tests adapted honestly (consume the new own-join frame first).
- `publish-relay-image` job added to build-server.yml (push-only, job-level packages:write) → `ghcr.io/<owner>/claude-remote-relay:{latest,main|vX.Y.Z}` so the Railway template can use a public Docker image instead of repo source.
- README: `-install --relay` example + task-preserves-relay note; `### 5. Troubleshooting` (ID) in the Railway section (empty room / wrong token / QR+firewall netsh).

### Verification (full outputs in .superpowers/sdd/task-M7-report.md)
- TDD red→green on all three Go items. `go build ./...`, `go vet ./...`, `go test ./... -count=1` all ok (incl. new network tests); `npm --prefix mobile run build` green; live relay on :8085 — phone-style join got room_status peers 0, desktop join got silence, phone then got peer_joined (exact frames in report).
- One non-reproducible `cmd/relay` FAIL during one full-suite run (parallel-load timing; package has pre-existing 400ms/2s windows since M5.1); 4x full suite + count=3/10 + 2 verbose sweeps all green after. Noted as concern.
- `-install --relay` itself NOT live-registered (ONLOGON needs elevation on this machine — same OS policy as M3); argv verified by unit test only.

### Commit (per brief, committed this session)
`feat(relay,android): relay presence feedback, LAN IP ranking, relay-aware autostart, GHCR image` → `8d32208`, no push.

## Session 2026-09-01 — M9 (transcript-based turn-end detection + explicit task-completed display)

### Feature: M9 per `.superpowers/sdd/task-M9-brief.md` — commit `feat(state,android): transcript-based turn-end detection and explicit task-completed display` (parent 8fbbf00)
- Transcript shape verified FIRST against 3 projects / ~1350 assistant records: the brief's literal all-text-blocks rule misfires ~10:1 (288 mid-turn narration text records vs 31 finals — same `message.id` streams thinking→text→tool_use as separate records). Shipped rule: ≥1 text block, 0 tool_use blocks, `message.stop_reason=="end_turn"` (final-answer discriminator). Documented in `handleAssistantRecord` (filewatcher.go).
- Watcher synthesizes `Stop` (Source:"watcher") for watcher-tracked sessions (subagents / failed hook delivery) — they no longer hang on "last tool" until the stall fallback.
- Store: duplicate-Stop guard (Stop on idle/completed = full no-op → task_done exactly once per turn; NB brief's literal active||subagent_running list broke the pre-existing spool-drain test where Stop follows a pending question — guard keys on already-idle instead, same intent); Stop sets `"✅ Task completed"` (also drives FGS shade) + new `Session.LastCompletedAt`; watcher Stop is the ONE watcher event that notifies; UserPromptSubmit/PreToolUse clear LastCompletedAt; stall fallback untouched (a stall is not a completion).
- UI: `taskCompleted = idle && last_completed_at` (App.tsx); StatusHero emerald `TASK COMPLETED` badge (CheckCircle2, verified in lucide-react 0.475.0) + "Finished at HH:mm"; LiveStreamBar emerald tint + check icon + "Task completed" label (no emoji truncation); types.ts `last_completed_at?`.

### Verification (full outputs in .superpowers/sdd/task-M9-report.md)
- TDD red→green. `go build ./...`, `go vet ./...`, `go test ./... -count=1` all ok; gofmt clean; `npm --prefix mobile run build` green.
- Live 9291 `--no-hooks --no-watch -idle-timeout 5m` (boot replayed 2104 durable events): PreToolUse→Stop→duplicate Stop → `/api/status` = `"current_tool_status":"✅ Task completed"` + `last_completed_at` set, exactly ONE task_done; UserPromptSubmit live-cleared the marker.

## Session 2026-09-03 — Remote Interaction & Live Process View (plan: docs/superpowers/plans/2026-09-02-remote-interaction-and-live-process-view.md)

### Worked on
Turned the monitor-only APK into a remote control surface, per the approved 15-task plan: live process view, remote approvals, AskUserQuestion/ExitPlanMode answering, permission-rules editor, mid-task prompt injection (queue + Stop-hook delivery), log clear/auto-clear. All contracts verified against https://code.claude.com/docs/en/hooks BEFORE implementing.

### Implementation (14 commits, TDD red→green per task)
- `internal/models`: HookPayload + permission_mode/stop_hook_active/last_assistant_message/prompt/tool_response/permission_suggestions; ProcessEvent (+kinds), PendingDecision, QuestionSpec/Option, DecisionResolution, AppSettings; Session + prompt_queue_depth/process_events; Snapshot + pending_decisions/recent_process_events/app_settings.
- `internal/state/decisions.go`: pending-decision registry (create/wait/resolve with one-shot channels), per-session FIFO prompt queues, app-settings hold.
- `internal/state/process.go`: per-session event ring (cap 200, Detail cap 8 KB) + process_event broadcast; store.go emits user_prompt/tool_use/tool_result/tool_error/turn_end from hook events; snapshot embeds active-session tail (50).
- `internal/state/logs.go`: ClearLogs (manual) + purgeStaleLogs (age-based 5/15/30 min window, [HH:MM] same-day best-effort parse w/ midnight wrap) wired into the 1s liveness ticker.
- `internal/api/decide.go`: `?decide=1` long-poll — PermissionRequest→decision.behavior (+updatedPermissions echo for always-allow), AskUserQuestion→allow+updatedInput{questions,answers}, ExitPlanMode→allow/deny+reason, Stop→{"decision":"block","reason":<queued prompt>} with stop_hook_active guard; bypassPermissions fast-path; timeout→plain ok (terminal fallback). approvalWaitOverride for fast tests.
- `internal/hooks/installer.go`: per-event registrations (PreToolUse/PermissionRequest --decide t=120, Stop --decide t=30, feed t=5), stale own-entry removal on upgrade (foreign hooks preserved — verified live against the .pixel-agents hook), bridge script --decide mode (110s timeout, stdout gated on hookSpecificOutput/decision).
- `internal/api/commands.go` + `permissions.go` + `settings.go`: client_command dispatcher (decision/prompt/clear_logs/app_settings/permissions_get/set/process_sync) over /ws read loop AND relayclient.OnClientFrame (relay forwards verbatim); REST mirrors; settings.json permissions editor (validates mode+arrays, preserves other keys); app settings persisted in ~/.claude/claude-remote-settings.json.
- `internal/watcher`: thinking/text blocks + tool_result content (string or {type:text}[]) stream into the feed; final-answer text (stop_reason=end_turn) not duplicated (turn_end carries it).
- `mobile/`: types + wsService.send/sendCommand/sendDecision/sendPrompt (first-ever client sends); ProcessFeed (expandable rows, FIXED-HEIGHT h-40/h-48 scroll boxes — the spec constraint), DecisionBanner (permission/question/plan + countdown), PromptComposer (queue-aware), SettingsModal (mode + rule lists + wait slider + auto-clear + clear-now), ActivityLogs clear chip + auto-clear chip, Header sliders button, App.tsx full frame handling.

### Verification (actual outputs)
- `go build ./...` OK; `go vet ./...` OK; `go test ./... -count=1` → 9 packages ok (cmd/relay, cmd/server, api, auth, hooks, network, relayclient, state, watcher). New tests: decisions (4), process (3), FormatToolDetail (2), watcher extraction (3), decide endpoint (8), commands (4), permissions/app-settings (6), logs (4), relay command path (1).
- `npm --prefix mobile run build` → `✓ built` (tsc strict, 0 errors).
- Live mock (`scripts/test-mock-hook.ps1` extended, server on :9280): `PASS: decision relayed through bridge stdout` (PermissionRequest→phone allow→hookSpecificOutput JSON), `PASS: queued prompt delivered via Stop block`, `PASS: process feed populated` (tool_use,turn_end events via /api/process). Installer on real home: Stop entry = foreign pixel-agents hook preserved + `claude-remote-hook.js --decide` t=30; relay dial-out connected with new code.
- Bridge node smoke: `node --check` OK; decide-mode with server down → exit 0, no stdout, no hang.

### Known limitations (documented in README + plan)
1. Prompts queued while fully idle deliver on the NEXT turn's end (no hook carries an injection into a dead-idle session; official RC has the same mid-turn queue semantics).
2. dontAsk/auto modes editable but banner only reacts to PermissionRequest events.
3. Relay-mode commands are WS-only (relay forwards frames, not HTTP) — REST mirrors are LAN/test conveniences.
