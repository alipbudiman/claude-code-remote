package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"claude-remote-server/internal/api"
	"claude-remote-server/internal/auth"
	"claude-remote-server/internal/hooks"
	"claude-remote-server/internal/network"
	"claude-remote-server/internal/state"
	"claude-remote-server/internal/watcher"
	"claude-remote-server/web"
)

const banner = `
========================================================================
   ____ _                 _         ____          _
  / ___| | __ _ _   _  __| | ___   / ___|___   __| | ___
 | |   | |/ _` + "`" + ` | | | |/ _` + "`" + ` |/ _ \ | |   / _ \ / _` + "`" + ` |/ _ \
 | |___| | (_| | |_| | (_| |  __/ | |__| (_) | (_| |  __/
  \____|_|\__,_|\__,_|\__,_|\___|  \____\___/ \__,_|\___/

          REMOTE SESSION & SUB-AGENT MONITOR (GO EXE)
========================================================================`

// diagf prints a startup diagnostic line. It defaults to plain stdout; once
// file logging is enabled it is rerouted through log.Printf so diagnostics
// also land in the log file. The QR banner and the connectivity block are
// display output, not diagnostics, and stay on stdout only.
var diagf = func(format string, a ...interface{}) {
	fmt.Printf(format+"\n", a...)
}

func main() {
	portFlag := flag.Int("port", 9280, "Port for local server to listen on (binds to 0.0.0.0)")
	installFlag := flag.Bool("install", false, "Register a logon Scheduled Task (auto-start at sign-in), then exit")
	uninstallFlag := flag.Bool("uninstall", false, "Remove the logon Scheduled Task, then exit")
	noHooksFlag := flag.Bool("no-hooks", false, "Disable auto-installation of Claude Code hooks")
	noWatchFlag := flag.Bool("no-watch", false, "Disable JSONL transcript file watcher")
	idleTimeoutFlag := flag.String("idle-timeout", "300s", "How long a session may receive no hook events before it is marked stalled (e.g. 300s, 2m30s)")
	logFileFlag := flag.String("log-file", "", "Also write log output to this file (rotates to <path>.1 past 5MB; default: stdout only)")
	relayFlag := flag.String("relay", os.Getenv("RELAY_URL"), "Remote relay URL to dial out to so off-LAN phones can connect, e.g. wss://relay.example.com (env RELAY_URL; empty = disabled)")
	flag.Parse()

	// Lifecycle-management flags never run the server.
	if *installFlag {
		// *relayFlag already folds in RELAY_URL (flag default), so the
		// registered task keeps the relay connection across reboots (M7).
		if err := installScheduledTask(*portFlag, *relayFlag); err != nil {
			log.Fatalf("Fatal: -install failed: %v", err)
		}
		return // exit 0
	}
	if *uninstallFlag {
		uninstallScheduledTask()
		return // exit 0
	}

	fmt.Println(banner)

	// 0. Optional file logging: stdout stays as-is (MultiWriter), and the
	// diagnostics below switch from fmt.Printf to log.Printf so they are
	// captured in the file. An oversized existing file rotates to <path>.1.
	if *logFileFlag != "" {
		f, err := openLogFile(*logFileFlag, maxLogFileSize)
		if err != nil {
			log.Fatalf("Fatal: could not open log file %s: %v", *logFileFlag, err)
		}
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stdout, f))
		diagf = log.Printf
		diagf("✅ Logging to %s (rotates to %s.1 past 5MB)", *logFileFlag, *logFileFlag)
	}

	idleTimeout, err := time.ParseDuration(*idleTimeoutFlag)
	if err != nil {
		log.Fatalf("Fatal: invalid -idle-timeout %q: %v (use values like 300s or 2m30s)", *idleTimeoutFlag, err)
	}
	if idleTimeout < 0 {
		log.Fatalf("Fatal: -idle-timeout must not be negative, got %q", *idleTimeoutFlag)
	}

	port := *portFlag
	hostIPs := network.GetLocalIPs()

	// 1. Initialize Thread-safe State Store
	store := state.NewStoreWithIdleTimeout(port, hostIPs, idleTimeout)

	// 1b. Remote-interaction settings (approval wait, log auto-clear) persist
	// in ~/.claude/claude-remote-settings.json; defaults 60s / off.
	store.SetAppSettings(api.LoadPersistedAppSettings())

	// 1a. Durable ingestion: replay the raw event log (rebuilds sessions from
	// the last 24h of accepted events), then drain any hook events the bridge
	// spooled while the server was down. Both run BEFORE the HTTP server
	// starts listening, so replayed history never buzzes (Source-gated
	// notifications) while spooled REAL events do notify on drain.
	durableDir := hooks.GetClaudeDir()
	if durableDir == "" {
		log.Fatalf("Fatal: could not resolve home directory for durable state")
	}
	eventLog := state.NewEventLog(durableDir)
	store.SetEventLog(eventLog)
	if replayed, err := eventLog.Replay(store.HandleHookEvent); err != nil {
		diagf("⚠️  Warning: event-log replay failed: %v", err)
	} else if replayed > 0 {
		diagf("✅ Replayed %d events from the last 24h of %s", replayed, eventLog.Path())
	}
	if drained, err := state.DrainSpool(durableDir, store.HandleHookEvent); err != nil {
		diagf("⚠️  Warning: spool drain failed: %v", err)
	} else if drained > 0 {
		diagf("✅ Drained %d spooled hook events (delivered as real notifications)", drained)
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := state.DrainSpool(durableDir, store.HandleHookEvent); err != nil {
				diagf("⚠️  Warning: spool drain failed: %v", err)
			}
		}
	}()

	// 1b. Liveness Fallback Engine
	store.StartLivenessWatcher()
	diagf("✅ Event-driven turn tracking active; liveness fallback auto-checks every 1s (stall after %s of silence)", idleTimeout)

	// 2. Load (or create) the shared-secret auth token guarding /api/* and /ws
	token, err := auth.LoadOrCreateToken()
	if err != nil {
		log.Fatalf("Fatal: could not load or create auth token: %v", err)
	}
	diagf("✅ Auth token loaded from ~/.claude/claude-remote-token (required by all /api/* and /ws requests)")

	// 3. Install Claude Code Hooks automatically
	if !*noHooksFlag {
		if err := hooks.InstallClaudeHooks(port); err != nil {
			diagf("⚠️  Warning: Failed to install Claude hooks: %v", err)
		} else {
			diagf("✅ Claude Code Hooks successfully linked to ~/.claude/settings.json")
		}
	}

	// 4. Start Transcript File Watcher (Redundancy; offsets persist across restarts)
	var fw *watcher.TranscriptWatcher
	if !*noWatchFlag {
		fw = watcher.NewTranscriptWatcher(store)
		fw.Start()
		defer fw.Stop() // best effort; the shutdown paths below stop it explicitly
		diagf("✅ JSONL Transcript file watcher active on ~/.claude/projects/")
	}

	// 5. Print Connectivity & QR Code (URL carries the auth token so a
	// scanned client can authenticate immediately)
	primaryURL := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", port, token)
	if len(hostIPs) > 0 {
		primaryURL = fmt.Sprintf("http://%s:%d/?token=%s", hostIPs[0], port, token)
	}

	fmt.Println("\n------------------------------------------------------------------------")
	fmt.Printf("🚀 Server running on 0.0.0.0:%d (Local LAN / Offline Wi-Fi)\n", port)
	fmt.Println("   Available Network Addresses:")
	for _, ip := range hostIPs {
		fmt.Printf("   • http://%s:%d\n", ip, port)
	}
	fmt.Printf("   • http://localhost:%d (Local Desktop Browser)\n", port)
	fmt.Println("------------------------------------------------------------------------")

	// 5a. Resolve the relay setting (M11): --relay flag > RELAY_URL env > the
	// URL persisted from the web dashboard. A flag/env value wins for THIS
	// run only; the file is rewritten exclusively by POST /api/relay.
	relayURL := api.ResolveRelayURL(*relayFlag, os.Getenv("RELAY_URL"), api.LoadPersistedRelayURL())
	if relayURL == "" {
		fmt.Println("💡 Tip: enable remote access (relay) from the web dashboard — open the URL above, Connection → Relay URL.")
	}

	// Print Terminal ASCII QR Code
	fmt.Println("\n📱 Scan this QR Code from Android APK / Phone Camera to Connect:")
	qrString := network.GenerateTerminalQRCode(primaryURL)
	if qrString != "" {
		fmt.Print(qrString)
	}
	fmt.Printf("URL: %s\n\n", primaryURL)

	// 6. Initialize & Start API Server (after replay & spool drain)
	srv := api.NewServer(port, store, web.EmbeddedFS, hostIPs, token)

	// 6a. Optional remote relay (M5.1): dial OUT to the relay hub so phones
	// off the LAN can join the same token-keyed room. Additive — the LAN
	// URLs above stay the primary path, and the relay client runs entirely
	// in the background with its own reconnect loop. Since M11 the URL can
	// also be set or changed at runtime from the web dashboard: the api
	// server owns the current client (StartRelay/StopRelay) so POST
	// /api/relay can swap it live; this boot-time call just seeds it.
	if relayURL != "" {
		srv.StartRelay(relayURL)
		diagf("🌐 Relay client active → %s", relayURL)
	}

	// 7. Graceful shutdown: Ctrl+C / SIGTERM (signal.Notify) and the Windows
	// console-close events (installConsoleHandler) all funnel into ONE
	// bounded path — persist watcher offsets, drain in-flight HTTP (~3s),
	// exit — so a console window close or logoff no longer loses up to 30s
	// of transcript offsets. M2's per-event durable log remains the real
	// durability story; this path only saves offsets and closes cleanly.
	var shutdownOnce sync.Once
	gracefulShutdown := func() {
		shutdownOnce.Do(func() {
			diagf("🛑 Shutting down Claude Remote Server (graceful; bounded to ~3s)...")
			// os.Exit skips main's defers, so the watcher must be stopped
			// HERE. Stop() persists offsets; sync.Once makes the deferred
			// double-call a no-op.
			if fw != nil {
				fw.Stop()
			}
			// Stops whichever relay client is current — the boot-time one
			// or the last one applied from the web dashboard (M11).
			srv.StopRelay()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := srv.Shutdown(ctx); err != nil {
				log.Printf("graceful shutdown did not complete in time: %v", err)
			}
			os.Exit(0)
		})
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		gracefulShutdown()
	}()

	// Windows console-close (X button), logoff and shutdown arrive as
	// CTRL_CLOSE/LOGOFF/SHUTDOWN events signal.Notify never sees. No-op on
	// other platforms.
	if err := installConsoleHandler(gracefulShutdown); err != nil {
		diagf("⚠️  Warning: could not install console-close handler: %v", err)
	}

	diagf("📡 Listening for Claude Code sessions & sub-agents... (Press Ctrl+C to stop)")
	if err := srv.Start(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			// Shutdown() already drained the listener; a clean stop, not an
			// error. (gracefulShutdown normally exits before we get here.)
			os.Exit(0)
		}
		// Bind failure: if the port owner answers /api/health with our
		// ServerVersion it is another instance of this server — a duplicate
		// launch that must exit cleanly instead of crashing.
		if isOurInstance(fmt.Sprintf("http://127.0.0.1:%d", port), token) {
			log.Printf("another Claude Remote Server instance is already running on port %d; exiting", port)
			os.Exit(0)
		}
		log.Fatalf("Fatal: Server error: %v", err)
	}
}
