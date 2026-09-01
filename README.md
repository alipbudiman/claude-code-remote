# Claude Code Remote

Real-time session monitoring for [Claude Code](https://github.com/anthropics/claude-code), from your phone.

[![Build APK](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-apk.yml/badge.svg)](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-apk.yml)
[![Build Server](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-server.yml/badge.svg)](https://github.com/alipbudiman/claude-code-remote/actions/workflows/build-server.yml)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

## What is this?

A small Go server runs on your PC and captures Claude Code hook events — tool calls, sub-agents, questions, permission requests, and completions. The Android app shows the live status and raises heads-up notifications; it runs as a foreground service, so tracking keeps working after you close the app or lock the phone. Everything works over local Wi-Fi with no internet required, and an optional relay (deployable on Railway) extends the same monitoring to any network. Sessions end in an explicit "✅ Task completed" state instead of a guess, and alerts you missed while offline are replayed on reconnect. Every connection is gated by a token that never leaves your machines.

## How it works

```
Claude Code CLI
   │  hook events (tools, sub-agents, questions, completions)
   ▼
Hook bridge — ~/.claude/claude-remote-hook.js
   │  HTTP to the server (spools to disk while the server is down)
   ▼
Desktop server — claude-remote-server.exe
   │  state store · durable event log · auth token · QR + web dashboard
   │
   ├──►  LAN WebSocket (same Wi-Fi, no internet needed)
   └──►  Railway relay (server dials out via --relay wss://…)
                  │
                  ▼
          Android app — foreground service + WebView UI,
          heads-up notifications
```

## Features

- Real-time session status and sub-agent tracking — role, current activity, duration
- Heads-up notifications for questions, permission requests, and completions
- Background tracking via an Android foreground service — survives app close, reconnects automatically
- Missed-alert replay — what happened while the phone was offline plays back on reconnect
- QR-scan setup — point the app at the terminal QR code, done
- Explicit "✅ Task completed" end state — no guessing whether a turn really finished
- Two connection modes: direct LAN (no internet) or anywhere via a self-hosted relay
- Durable event log — recent session history survives server and PC restarts
- Windows logon autostart via `-install`
- Token authentication on every API and WebSocket request

## Quick Start

### 1. Run the desktop server

Download the server binary for your OS from [Releases](https://github.com/alipbudiman/claude-code-remote/releases) — `claude-remote-server-windows-amd64.exe` on Windows, with Linux and macOS builds alongside — and run it:

```cmd
claude-remote-server.exe
```

On first start the server installs the Claude Code hooks into `~/.claude/settings.json`, prints its LAN URL, and shows a QR code in the terminal. Opening the URL in a desktop browser gives you the same dashboard as the phone. To enable remote access later, open the dashboard in a browser and set the relay URL — no flags needed.

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

The token is the room key for the status stream — anyone holding it can read your session status, so keep it private. It is generated automatically on your PC at `~/.claude/claude-remote-token`. To rotate it: delete the token file, restart the server (a new token is created), and re-enter the token on the phone.

## Troubleshooting

- **The app connects to the relay but shows nothing.** The PC has not joined the room yet. Start the server with `--relay wss://<your-domain>` and confirm the log shows `🌐 Relay client active`. The app displays a "waiting for your PC" banner while the room is empty.
- **Connection silently rejected — wrong token.** The handshake is declined with a 401. Copy all 64 characters from `%USERPROFILE%\.claude\claude-remote-token`, with no extra spaces or line breaks.
- **The QR code does not connect.** Check that the IP in the QR matches one of the server's listed addresses — virtual adapters from WSL or a VPN are sometimes picked by mistake. Make sure the phone and the PC are on the same Wi-Fi, then open the port in Windows Firewall:

  ```
  netsh advfirewall firewall add rule name="Claude Remote Server" dir=in action=allow protocol=TCP localport=9280
  ```

## Development

```bash
go build ./cmd/server                                            # desktop server
go test ./...                                                    # Go tests
npm --prefix mobile run build                                    # Android web UI
powershell -ExecutionPolicy Bypass -File scripts\test-mock-hook.ps1   # simulate hook events
```

CI builds the APK and the Windows/Linux/macOS server binaries on every push and attaches them to Releases on tags; every workflow run also uploads them as downloadable artifacts.

## License

GPL-3.0 — see [LICENSE](LICENSE). The desktop server and the relay are licensed under the GNU General Public License v3.
