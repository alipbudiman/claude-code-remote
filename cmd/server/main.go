package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
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
  \____|_|\__,_|\__,_|\__,_|\___|  \____\___/ \__,_|\___|
  
          REMOTE SESSION & SUB-AGENT MONITOR (GO EXE)
========================================================================`

func main() {
	portFlag := flag.Int("port", 9280, "Port for local server to listen on (binds to 0.0.0.0)")
	noHooksFlag := flag.Bool("no-hooks", false, "Disable auto-installation of Claude Code hooks")
	noWatchFlag := flag.Bool("no-watch", false, "Disable JSONL transcript file watcher")
	idleTimeoutFlag := flag.String("idle-timeout", "300s", "How long a session may receive no hook events before it is marked stalled (e.g. 300s, 2m30s)")
	flag.Parse()

	fmt.Println(banner)

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
		fmt.Printf("⚠️  Warning: event-log replay failed: %v\n", err)
	} else if replayed > 0 {
		fmt.Printf("✅ Replayed %d events from the last 24h of %s\n", replayed, eventLog.Path())
	}
	if drained, err := state.DrainSpool(durableDir, store.HandleHookEvent); err != nil {
		fmt.Printf("⚠️  Warning: spool drain failed: %v\n", err)
	} else if drained > 0 {
		fmt.Printf("✅ Drained %d spooled hook events (delivered as real notifications)\n", drained)
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := state.DrainSpool(durableDir, store.HandleHookEvent); err != nil {
				fmt.Printf("⚠️  Warning: spool drain failed: %v\n", err)
			}
		}
	}()

	// 1b. Liveness Fallback Engine
	store.StartLivenessWatcher()
	fmt.Printf("✅ Event-driven turn tracking active; liveness fallback auto-checks every 1s (stall after %s of silence)\n", idleTimeout)

	// 2. Load (or create) the shared-secret auth token guarding /api/* and /ws
	token, err := auth.LoadOrCreateToken()
	if err != nil {
		log.Fatalf("Fatal: could not load or create auth token: %v", err)
	}
	fmt.Println("✅ Auth token loaded from ~/.claude/claude-remote-token (required by all /api/* and /ws requests)")

	// 3. Install Claude Code Hooks automatically
	if !*noHooksFlag {
		if err := hooks.InstallClaudeHooks(port); err != nil {
			fmt.Printf("⚠️  Warning: Failed to install Claude hooks: %v\n", err)
		} else {
			fmt.Println("✅ Claude Code Hooks successfully linked to ~/.claude/settings.json")
		}
	}

	// 4. Start Transcript File Watcher (Redundancy; offsets persist across restarts)
	if !*noWatchFlag {
		fw := watcher.NewTranscriptWatcher(store)
		fw.Start()
		defer fw.Stop()
		fmt.Println("✅ JSONL Transcript file watcher active on ~/.claude/projects/")
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

	// Print Terminal ASCII QR Code
	fmt.Println("\n📱 Scan this QR Code from Android APK / Phone Camera to Connect:")
	qrString := network.GenerateTerminalQRCode(primaryURL)
	if qrString != "" {
		fmt.Print(qrString)
	}
	fmt.Printf("URL: %s\n\n", primaryURL)

	// 6. Initialize & Start API Server (after replay & spool drain)
	srv := api.NewServer(port, store, web.EmbeddedFS, hostIPs, token)

	// Graceful shutdown handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\n🛑 Shutting down Claude Code Remote Server...")
		os.Exit(0)
	}()

	fmt.Println("📡 Listening for Claude Code sessions & sub-agents... (Press Ctrl+C to stop)")
	if err := srv.Start(); err != nil {
		log.Fatalf("Fatal: Server error: %v", err)
	}
}
