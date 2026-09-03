# Claude Code Remote

Monitor and control [Claude Code](https://github.com/anthropics/claude-code) from your phone — live process view, remote approvals, question answering, and queued prompts.

[![Build APK](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-apk.yml/badge.svg)](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-apk.yml)
[![Build Server](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-server.yml/badge.svg)](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-server.yml)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

## What is this?

A small Go server runs on your PC and captures Claude Code hook events — tool calls, sub-agents, questions, permission requests, and completions. The Android app streams the live status, shows every step Claude is executing (including its thinking), and raises heads-up notifications; it runs as a foreground service, so tracking keeps working after you close the app or lock the phone.

The app is not just a viewer. When Claude needs a decision — a permission approval, a multiple-choice question, a plan review — the request appears on your phone and your answer is delivered straight back into the Claude Code session on your PC. You can also queue a follow-up prompt while Claude is still working, and edit Claude Code's permission rules without touching the terminal.

Everything works over local Wi-Fi with no internet required, and an optional relay (deployable on Railway) extends the same monitoring and control to any network. Sessions end in an explicit "✅ Task completed" state instead of a guess, and alerts you missed while offline are replayed on reconnect. Every connection is gated by a token that never leaves your machines.

## How it works

```
Claude Code CLI
   │  hook events (tools, thinking, questions, completions)
   ▼
Hook bridge — ~/.claude/claude-remote-hook.js
   │  HTTP to the server (spools to disk while the server is down)
   │  decision events long-poll until your phone answers
   ▼
Desktop server — claude-remote-server.exe
   │  state store · durable event log · auth token · QR + web dashboard
   │
   ├──►  LAN WebSocket (same Wi-Fi, no internet needed)
   └──►  Railway relay (server dials out via --relay wss://…)
                  │
                  ▼
          Android app — foreground service + WebView UI,
          heads-up notifications, approvals & commands
```

Monitoring flows one way (PC → phone). Control flows the other: every tap on the phone — Allow, Deny, an option pick, a queued prompt, a settings change — travels the same authenticated WebSocket back to the server, which translates it into the official Claude Code hook-decision protocol. No terminal input is simulated, and nothing runs outside your machines.

## Features

### Watch

- **Live process view** — every agent step as it happens: your prompts, Claude's thinking, its replies, each tool call with full detail (commands, file edits), and every result. Long outputs scroll inside fixed-height boxes, so the view stays stable no matter how big an output gets.
- Real-time session status and sub-agent tracking — role, current activity, duration
- Heads-up notifications for questions, permission requests, and completions
- Background tracking via an Android foreground service — survives app close, reconnects automatically
- Missed-alert replay — what happened while the phone was offline plays back on reconnect
- Explicit "✅ Task completed" end state — no guessing whether a turn really finished
- Durable event log — recent session history survives server and PC restarts

### Control

- **Remote approvals** — when Claude Code wants to run something that needs permission, the request appears on your phone with Allow / Deny / "always allow" buttons and an optional note for Claude. If you don't answer in time, the request simply falls back to the normal prompt on your PC terminal — nothing is ever lost or stuck.
- **Remote question answering** — Claude's multiple-choice questions (`AskUserQuestion`) arrive as tappable options, multi-select included, with a free-text notes field. Plan reviews (`ExitPlanMode`) show the full plan in a scrollable pane with Approve / Reject.
- **Mid-task prompt injection** — type a follow-up while Claude is still working; it's queued and delivered the moment the current turn ends (the same queue-until-turn-end behavior as the official Remote Control). The composer shows how many prompts are queued.
- **Permission settings editor** — view and edit Claude Code's permission rules (`~/.claude/settings.json`) from the phone: the permission mode plus allow / ask / deny rule lists. Uses Claude Code's own rule syntax; changes apply from the next tool call.
- **Activity log controls** — clear the log view with one tap, or auto-drop entries older than 5, 15, or 30 minutes.

### Foundation

- Two connection modes: direct LAN (no internet) or anywhere via a self-hosted relay
- QR-scan setup — point the app at the terminal QR code, done
- Windows logon autostart via `-install`
- Token authentication on every API and WebSocket request

## Remote control from your phone

The interactive features use the same hooks as the monitoring — there is no second system to install or configure. When a decision is needed, the hook pauses briefly, your phone shows the request, and your answer is returned through Claude Code's official hook-decision protocol. Two guarantees worth knowing:

- **Never stuck.** Every wait has a timeout. If your phone is offline, unreachable, or you just don't answer, the request falls through to the normal prompt in your PC terminal — Claude Code keeps working exactly as before.
- **Never bypassed.** In `bypassPermissions` mode nothing is parked at all (Claude Code skips those prompts anyway), and every answer travels the same token-authenticated connection as the status stream.

What each piece looks like on the phone:

| Feature | What you see | What happens on the PC |
| --- | --- | --- |
| Permission request | Golden banner: command or file, Allow / Deny, optional note, "always allow" when offered | Your choice is applied to that tool call; "always allow" also saves the matching rule |
| Question | Tappable options (multi-select + notes) | Your selection is returned as the question's answer |
| Plan review | The plan in a scrollable pane, Approve / Reject | Approval lets Claude start executing; rejection sends your note back as the reason |
| Queued prompt | Composer with queue depth badge | Your text continues the conversation when the current turn ends |
| Permission rules | Mode buttons + rule lists with add/delete | Written to `~/.claude/settings.json`; unrelated settings are preserved |

Tuning: **Remote Settings → Remote approval wait** (15–105 s, default 60 s) controls how long a request waits for your phone before the PC terminal takes over.

### Updating from an older release

The control features need the decision-mode hook entries, which the server installs automatically. If you're upgrading:

1. Replace the server binary with the new build and restart it — the hooks in `~/.claude/settings.json` are upgraded on startup (hooks from your other tools are left untouched).
2. Install the new APK on your phone.
3. Start a **fresh** Claude Code session — already-running sessions keep the hooks they started with.

To verify, check that `~/.claude/settings.json` lists `claude-remote-hook.js --decide` for `PreToolUse`, `PermissionRequest`, and `Stop`, or simply run a command that needs permission and watch it appear on the phone.

## Quick Start

### 1. Run the desktop server

Download the server binary for your OS from [Releases](https://github.com/alipbudiman/claude-code-remote/releases) — `claude-remote-server-windows-amd64.exe` on Windows, with Linux and macOS builds alongside — and run it:

```cmd
claude-remote-server.exe
```

On first start the server installs the Claude Code hooks into `~/.claude/settings.json` — including the decision hooks that power remote approvals and question answering — prints its LAN URL, and shows a QR code in the terminal. Opening the URL in a desktop browser gives you the same dashboard as the phone. To enable remote access later, open the dashboard in a browser and set the relay URL — no flags needed.

Optional flags:

| Flag | Default | Description |
| --- | --- | --- |
| `-port` | `9280` | Port to listen on (binds to `0.0.0.0`) |
| `--relay <url>` | `RELAY_URL` env (disabled) | Dial out to a relay so phones off your LAN can connect, e.g. `wss://relay.example.com` — or set it at runtime from the web dashboard (Connection → Relay URL); the setting persists across restarts |
| `-idle-timeout <dur>` | `300s` | How long a session may stay silent before it is marked stalled, e.g. `2m30s` |
| `-log-file <path>` | stdout only | Also write logs to this file; rotates to `<path>.1` past 5 MB |
| `-install` / `-uninstall` | — | Register or remove the logon Scheduled Task (auto-start at sign-in). Run `-install` once from an elevated console; any active `--relay`/`RELAY_URL` is baked into the task so the relay survives reboots |
| `--no-hooks` | hooks installed | Skip hook auto-installation |
| `--no-watch` | watcher active | Skip the JSONL transcript watcher |

To build from source instead: `go build -ldflags="-s -w" -o claude-remote-server.exe ./cmd/server`.

### 2. Install the Android app

Download the APK — `claude-remote.apk` from [Releases](https://github.com/alipbudiman/claude-code-remote/releases), or the `claude-remote-android-apk` artifact from the latest [APK workflow run](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-apk.yml) — and install it. Android will ask you to allow installs from unknown sources; the app needs notification permission for alerts.

### 3. Connect

- **Fastest (LAN):** tap **Scan QR** in the app's settings and point the camera at the QR code in the server terminal — URL and token are filled in automatically.
- **From anywhere (relay):** enter the relay URL (`https://…up.railway.app`) and the token — the exact contents of `%USERPROFILE%\.claude\claude-remote-token` on Windows, `~/.claude/claude-remote-token` elsewhere.

## Anywhere access: deploy your own relay

Default mode is LAN-only. To monitor from any network — mobile data, office, school — deploy the relay hub to [Railway](https://railway.com). The desktop server dials out to it, so no port-forwarding or public IP is needed; phones then connect to the same relay URL with the same token.

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/deploy/claude-stat-remote?referralCode=jH5Z9g&utm_medium=integration&utm_source=template&utm_campaign=generic)


Manual alternative:

1. Sign in at [railway.com](https://railway.com) → **New Project → Deploy from GitHub repo** → pick your fork of `claude-code-remote`, or create the service from the Docker image `ghcr.io/alipbudiman/claude-remote-relay:latest`. Both paths build the root `Dockerfile`, which is the relay hub — not the desktop monitor.
2. Service → **Settings → Networking → Generate Domain**, and note the URL (e.g. `https://xxx.up.railway.app`).
3. Verify: `https://<your-domain>/health` must return `{"service":"claude-remote-relay","status":"ok"}`.
4. Run the desktop server with `claude-remote-server.exe --relay wss://<your-domain>` (or set `RELAY_URL`).
5. In the app, enter the relay URL `https://<your-domain>` plus the token.

The relay image is republished automatically on every push to `main`; redeploy the Railway service to pick up the latest build.

## Security

The token is the room key — anyone holding it can read your session status **and answer approvals or send prompts**, so keep it private. It is generated automatically on your PC at `~/.claude/claude-remote-token`. To rotate it: delete the token file, restart the server (a new token is created), and re-enter the token on the phone.

Design notes for the control path:

- Control commands ride the same token-authenticated WebSocket as the status stream — there is no unauthenticated endpoint, and in relay mode the transport must be `wss://` (encrypted) for the server to dial out.
- The server only answers permission prompts on your behalf; it never bypasses Claude Code's permission system, and `bypassPermissions` behavior is unchanged.
- Permission-rule edits are validated before they are written, merge with your existing `settings.json` (unknown keys and other tools' settings survive), and are serialized so concurrent edits cannot corrupt the file.

## Troubleshooting

- **The app connects to the relay but shows nothing.** The PC has not joined the room yet. Start the server with `--relay wss://<your-domain>` and confirm the log shows `🌐 Relay client active`. The app displays a "waiting for your PC" banner while the room is empty.
- **Connection silently rejected — wrong token.** The handshake is declined with a 401. Copy all 64 characters from `%USERPROFILE%\.claude\claude-remote-token`, with no extra spaces or line breaks.
- **The QR code does not connect.** Check that the IP in the QR matches one of the server's listed addresses — virtual adapters from WSL or a VPN are sometimes picked by mistake. Make sure the phone and the PC are on the same Wi-Fi, then open the port in Windows Firewall:

  ```
  netsh advfirewall firewall add rule name="Claude Remote Server" dir=in action=allow protocol=TCP localport=9280
  ```

- **Approvals and questions don't appear on the phone.** The Claude Code session is probably older than the upgrade — the decision hooks are picked up when a session starts. Also check `~/.claude/settings.json` lists `claude-remote-hook.js --decide` for `PreToolUse`, `PermissionRequest`, and `Stop`; if not, restart the server (it re-installs hooks on startup) and start a fresh session. Approvals are also skipped in `bypassPermissions` mode — that's expected.

- **A permission request went to the PC terminal instead of my phone.** The phone didn't answer within the approval wait (offline, disconnected, or you missed the banner), so the request fell back to the terminal — by design, so Claude Code is never blocked. Raise the wait in `Remote Settings → Remote approval wait` if this happens often.

## Development

```bash
go build ./cmd/server                                            # desktop server
go test ./...                                                    # Go tests
npm --prefix mobile run build                                    # Android web UI
powershell -ExecutionPolicy Bypass -File scripts\test-mock-hook.ps1   # simulate hook events
powershell -ExecutionPolicy Bypass -File scripts\test-mock-hook.ps1 -Port 9293   # …against a scratch server
```

The mock script exercises the full lifecycle — tool use, sub-agents, turn end — plus the remote-control round trips: a permission approval answered from the "phone" side while the hook long-polls, a queued prompt delivered through the Stop hook, and the live process feed via `/api/process`. Use `-Port` to target a test server without disturbing one already running on the default port.

CI builds the APK and the Windows/Linux/macOS server binaries on every push and attaches them to Releases on tags; every workflow run also uploads them as downloadable artifacts.

## License

GPL-3.0 — see [LICENSE](LICENSE). The desktop server and the relay are licensed under the GNU General Public License v3.
