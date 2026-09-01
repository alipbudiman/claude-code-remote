# Deploy and Host claude-code-remote on Railway

claude-code-remote is a real-time monitoring system for Claude Code sessions. A lightweight Go server runs on your PC, captures Claude Code hook events (tools, sub-agents, questions, completions), and streams live status to an Android app. Deploying its relay on Railway extends monitoring beyond your home Wi-Fi — to anywhere your phone is.

## About Hosting claude-code-remote on Railway

This template deploys the **relay** — a tiny, stateless Go WebSocket hub (~10 MB RAM, single container, no database, no volume). Your desktop server (a single EXE/binary for Windows, macOS, or Linux) dials **out** to the relay over `wss://` using your secret token, so you need no port forwarding, public IP, or firewall changes. The Android APK connects to the same relay URL with the same token, and the relay forwards status frames between the two. Railway provides the public TLS domain and keeps the relay always-on; the Hobby tier is more than enough. Deploy, generate a domain, verify `/health` — done in about a minute.

## Common Use Cases

- Monitor long-running Claude Code tasks from your phone on any network (cellular, office, campus) — not just the same Wi-Fi as your PC.
- Get instant heads-up notifications when Claude asks a question, requests permission, or finishes a task — including missed-alert replay after the phone reconnects.
- Self-host the relay under your own Railway account so session status data never passes through a third-party service.

## Dependencies for claude-code-remote Hosting

**None on Railway.** The relay is fully self-contained — no database, cache, queue, or persistent volume is required.

Client-side components (not part of this deployment):

- **Desktop server** — `claude-remote-server` binary from [GitHub Releases](https://github.com/alipbudiman/claude-code-remote/releases), started with `--relay wss://<your-relay-domain>` (or `RELAY_URL` env). Generates and stores its own token at `~/.claude/claude-remote-token`.
- **Android APK** — `claude-remote-android-apk` artifact from [GitHub Actions](https://github.com/alipbudiman/claude-code-remote/actions); enter the relay URL + token in the app's connection settings.
- **Pairing token** — the 64-character hex token shared by desktop and phone; it is the room key. Keep it private (rotation is documented in the README).
