package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// main runs the relay hub: PORT (default 8080, Railway injects it) and
// RELAY_PING_INTERVAL (default 30s, parseable as a Go duration; injectable
// so tests can shrink it). SIGINT/SIGTERM close every member connection and
// drain the listener.
func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if _, err := strconv.Atoi(port); err != nil {
		log.Fatalf("Fatal: invalid PORT %q: %v", port, err)
	}

	pingInterval := defaultPingInterval
	if v := os.Getenv("RELAY_PING_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			log.Fatalf("Fatal: invalid RELAY_PING_INTERVAL %q (use values like 30s or 1m): %v", v, err)
		}
		pingInterval = d
	}

	rs := newRelayServer(pingInterval)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", rs.handleHealth)
	mux.HandleFunc("/ws", rs.handleWS)

	srv := &http.Server{Addr: ":" + port, Handler: mux}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Printf("relay: shutting down, closing all member connections")
		rs.closeAll()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("relay: shutdown did not complete in time: %v", err)
		}
	}()

	log.Printf("relay: claude-remote-relay listening on :%s (ping every %s)", port, pingInterval)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Fatal: relay server error: %v", err)
	}
}
