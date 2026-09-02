package api

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"claude-remote-server/internal/hooks"
	"claude-remote-server/internal/models"
	"claude-remote-server/internal/network"
	"claude-remote-server/internal/relayclient"
	"claude-remote-server/internal/state"
)

// ServerVersion identifies this server build. The single-instance guard and
// /api/health use it to tell our server apart from whatever else may be
// listening on the port.
const ServerVersion = "1.0.0"

// androidWebViewOrigin is the Origin the Android APK WebView sends for every
// request (it serves the bundled dashboard from this virtual https origin).
const androidWebViewOrigin = "https://appassets.androidplatform.net"

// defaultPingInterval is the /ws keepalive cadence: every connection is
// pinged this often, and a client that goes three intervals without a pong
// (or any traffic) is torn down instead of pinning at "connected".
const defaultPingInterval = 20 * time.Second

// defaultStatsInterval is the app-level heartbeat cadence: every subscriber
// is broadcast a lightweight `stats` frame this often (M4b). Control-frame
// pongs never reach listeners like OkHttp's, so app clients need real data
// traffic to distinguish "quiet" from "dead" — without it they force periodic
// reconnects whose dark windows swallow live notification frames.
const defaultStatsInterval = 20 * time.Second

// wsWriteTimeout bounds every server-to-client write so a slow or wedged
// client cannot block a broadcast or ping goroutine indefinitely.
const wsWriteTimeout = 10 * time.Second

// Server encapsulates the HTTP and WebSocket API router
type Server struct {
	port       int
	store      *state.Store
	embeddedFS embed.FS
	mux        *http.ServeMux
	hostIPs    []string
	token      string
	upgrader   websocket.Upgrader

	// pingInterval is how often /ws connections are pinged (see
	// defaultPingInterval). Tests shrink it to keep the suite fast.
	pingInterval time.Duration

	// statsInterval is how often the `stats` heartbeat is broadcast to all
	// subscribers (see defaultStatsInterval). Tests shrink it too.
	statsInterval time.Duration

	// statsStop closes to stop the stats heartbeat goroutine; start/stop are
	// each guarded by a sync.Once so repeated Start()/Shutdown() calls stay
	// safe.
	statsStop      chan struct{}
	statsStartOnce sync.Once
	statsStopOnce  sync.Once

	// httpServer is created by Start() so Shutdown() can drain it.
	httpServer *http.Server
	// uptimeStart anchors /api/health's uptime_s.
	uptimeStart time.Time

	// approvalWaitOverride shrinks the decision long-poll window in tests
	// (0 = use the app-settings window; see decide.go).
	approvalWaitOverride time.Duration

	// settingsMu serializes every read-modify-write of ~/.claude/settings.json
	// (permissions editor, hook re-install): concurrent client_command frames
	// each dispatch on their own goroutine-turned-synchronous path and would
	// otherwise interleave file writes.
	settingsMu sync.Mutex

	// relayMu guards relayClient/relayURL — the runtime-swappable relay
	// connection (M11). The dashboard's POST /api/relay stops the current
	// client and starts a fresh one under this mutex; graceful shutdown
	// stops whichever client is current via StopRelay().
	relayMu     sync.Mutex
	relayClient *relayclient.Client
	relayURL    string // effective URL; "" = relay disabled
}

// NewServer initializes an API server with all routes configured. The token
// is the shared secret required by every /api/* endpoint and the /ws upgrade.
func NewServer(port int, store *state.Store, embeddedFS embed.FS, hostIPs []string, token string) *Server {
	s := &Server{
		port:          port,
		store:         store,
		embeddedFS:    embeddedFS,
		mux:           http.NewServeMux(),
		hostIPs:       hostIPs,
		token:         token,
		pingInterval:  defaultPingInterval,
		statsInterval: defaultStatsInterval,
		statsStop:     make(chan struct{}),
		uptimeStart:   time.Now(),
	}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		// Offer the authenticated subprotocol so gorilla/websocket echoes
		// it back to browser/WebView clients, which cannot set handshake
		// headers (this is what makes their /ws connection succeed).
		Subprotocols: []string{"claude-remote." + token},
		CheckOrigin:  s.originAllowed,
	}

	s.setupRoutes()
	return s
}

// setupRoutes registers all HTTP handlers, WebSocket endpoints, and static routes
func (s *Server) setupRoutes() {
	// Auth + CORS gate for /api/* endpoints. CORS wraps the token check so
	// OPTIONS preflights (which cannot carry credentials) are answered
	// before authentication.
	apiGate := func(h http.HandlerFunc) http.Handler {
		return s.withCORS(s.requireToken(h))
	}

	// 1. Claude Code Hook Receiver Endpoint
	s.mux.Handle("/api/hook", apiGate(s.handleHookPost))

	// 2. WebSocket Real-Time Stream Endpoint
	s.mux.Handle("/ws", s.requireToken(http.HandlerFunc(s.handleWebSocket)))

	// 3. REST Endpoints
	s.mux.Handle("/api/status", apiGate(s.handleStatus))
	s.mux.Handle("/api/sessions", apiGate(s.handleSessions))
	s.mux.Handle("/api/subagents", apiGate(s.handleSubagents))
	s.mux.Handle("/api/qr", apiGate(s.handleQRCode))
	s.mux.Handle("/api/install-hooks", apiGate(s.handleInstallHooks))
	s.mux.Handle("/api/health", apiGate(s.handleHealth))
	s.mux.Handle("/api/relay", apiGate(s.handleRelay))
	// Remote-interaction command mirrors (LAN/tests; phones use /ws frames).
	s.mux.Handle("/api/decision", apiGate(s.handleDecisionPost))
	s.mux.Handle("/api/prompt", apiGate(s.handlePromptPost))
	s.mux.Handle("/api/process", apiGate(s.handleProcessGet))
	s.mux.Handle("/api/logs/clear", apiGate(s.handleClearLogsPost))
	s.mux.Handle("/api/settings", apiGate(s.handleSettings))
	s.mux.Handle("/api/permissions", apiGate(s.handlePermissions))

	// 4. Embedded Web UI Dashboard (static HTML only, no data — ungated)
	s.mux.HandleFunc("/", s.handleStaticWeb)
}

// requireToken gates a handler behind the shared-secret token. Accepted in
// this order: "Authorization: Bearer <tok>" header, "?token=<tok>" query
// parameter, or the "Sec-WebSocket-Protocol: claude-remote.<tok>" handshake
// header (the only option available to browser/WebView JS).
func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if tok == "" {
			for _, p := range websocket.Subprotocols(r) {
				if strings.HasPrefix(p, "claude-remote.") {
					tok = strings.TrimPrefix(p, "claude-remote.")
				}
			}
		}
		// Fail closed: an empty configured token must never authenticate.
		if s.token == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed is the shared Origin allowlist used by both the WebSocket
// CheckOrigin hook and the CORS middleware. It allows:
//
//	(a) requests with no Origin header (non-browser clients: the Node hook
//	    bridge, curl, PowerShell),
//	(b) the Android APK WebView origin,
//	(c) same-origin requests: any http://<host>:<port> whose host equals
//	    the request's Host header.
//
// Everything else is rejected.
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser client
	}
	if origin == androidWebViewOrigin {
		return true // Android APK WebView
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host) // same-origin
}

// withCORS emits CORS headers for /api/* responses, echoing the request's
// Origin only when it passes the originAllowed allowlist.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && s.originAllowed(r) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHookPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	var payload models.HookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Source is an INTERNAL trust marker ("" = live hook, "watcher" = JSONL
	// watcher, "replay" = boot replay) that decides durable-log appends and
	// whether heads-up notifications fire. A token-holding HTTP caller must
	// never set it — a spoofed source:"replay" would silently bypass the
	// durable event log and notifications. Force it to "" so every HTTP
	// delivery is treated as a live event.
	payload.Source = ""

	// Decision mode (?decide=1, sent by the bridge's --decide entries):
	// park a pending decision for permission/question/plan events and
	// long-poll for the phone's answer; Stop checks the prompt queue. The
	// response body is the Claude Code hook JSON the bridge forwards on
	// stdout. Feed events (default mode) keep the plain ok response.
	decide := r.URL.Query().Get("decide") == "1"

	// A Stop that will deliver a queued prompt must NOT apply turn-end
	// state first: blocking the stop means the turn CONTINUES, so marking
	// the session idle/completed (and firing task_done) would be a false
	// end. Peek before feeding; skip the state application entirely when a
	// prompt is about to be delivered.
	if decide && payload.HookEventName == "Stop" &&
		!(payload.StopHookActive != nil && *payload.StopHookActive) {
		if _, has := s.store.PeekNextPrompt(payload.SessionID); has {
			prompt, _ := s.store.DrainNextPrompt(payload.SessionID)
			s.store.Publish(models.WebSocketMessage{
				Type:      "prompt_queued",
				Data:      map[string]interface{}{"session_id": payload.SessionID, "depth": s.store.PromptQueueDepth(payload.SessionID)},
				Timestamp: time.Now(),
			})
			s.store.AddNotification(payload.SessionID, "📲 Prompt delivered", prompt, "info")
			writeJSON(w, map[string]interface{}{"decision": "block", "reason": prompt})
			return
		}
	}

	s.store.HandleHookEvent(payload)

	if decide {
		s.decideHookEvent(w, payload)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleWebSocket serves a long-lived connection: it sends the initial state
// snapshot, streams live broadcasts, and keeps the link alive with a ping
// ticker (M4a). A client that stops responding — half-open socket, vanished
// phone, wedged WebView — is reaped by a read deadline instead of lingering
// as a zombie subscriber.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	interval := s.pingInterval
	if interval <= 0 {
		interval = defaultPingInterval
	}

	// gorilla/websocket permits one concurrent writer per connection: the
	// initial snapshot, the broadcast pump, and the ping ticker all funnel
	// through writeMu, each write bounded by wsWriteTimeout.
	var writeMu sync.Mutex
	sendJSON := func(v interface{}) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		if err := conn.WriteJSON(v); err != nil {
			// Hard-close so the blocked reader and this pump terminate now
			// rather than on the client's next disconnect.
			_ = conn.Close()
			return false
		}
		return true
	}

	// Send initial full state snapshot
	snapshot := s.store.GetSnapshot()
	initMsg := models.WebSocketMessage{
		Type:      "initial_state",
		Data:      snapshot,
		Timestamp: time.Now(),
	}
	if !sendJSON(initMsg) {
		return
	}

	// Subscribe to live broadcast channel
	subCh := s.store.Subscribe()
	defer s.store.Unsubscribe(subCh)

	// done is closed when the read loop exits; it stops the ping ticker.
	done := make(chan struct{})
	defer close(done)

	// Pump incoming messages from store to WebSocket client
	go func() {
		for msg := range subCh {
			if !sendJSON(msg) {
				return
			}
		}
	}()

	// Keepalive: ping the client every interval. WriteControl is documented
	// as safe to call alongside other writers, but it shares writeMu anyway
	// so there is exactly one writer on this connection at any instant.
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				writeMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout))
				writeMu.Unlock()
				if err != nil {
					_ = conn.Close() // release the read loop
					return
				}
			}
		}
	}()

	// Read loop with a liveness deadline. Every WebSocket stack answers pings
	// with pongs automatically, so a healthy connection keeps the deadline
	// extended forever; anything else expires it and breaks the loop, after
	// which the deferred Close/Unsubscribe tear the connection down.
	readWindow := 3 * interval
	extendReadDeadline := func() error {
		return conn.SetReadDeadline(time.Now().Add(readWindow))
	}
	if err := extendReadDeadline(); err != nil {
		return
	}
	conn.SetPongHandler(func(string) error { return extendReadDeadline() })

	// Read loop: dispatch client_command frames from the phone/web client
	// (decisions, prompts, settings, log clears) SYNCHRONOUSLY so command
	// order is preserved (handlers are fast; none block on the network).
	// Replies ride the normal broadcast pump, so no per-connection writes
	// happen here. Malformed or non-command frames are ignored.
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := extendReadDeadline(); err != nil {
			return
		}
		if len(data) == 0 {
			continue
		}
		s.handleClientFrameRaw(data)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snapshot := s.store.GetSnapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshot)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	snapshot := s.store.GetSnapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshot.Sessions)
}

func (s *Server) handleSubagents(w http.ResponseWriter, r *http.Request) {
	subagents := s.store.GetAllSubagents()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(subagents)
}

func (s *Server) handleQRCode(w http.ResponseWriter, r *http.Request) {
	primaryIP := "127.0.0.1"
	if len(s.hostIPs) > 0 {
		primaryIP = s.hostIPs[0]
	}
	url := fmt.Sprintf("http://%s:%d", primaryIP, s.port)
	pngBytes, err := network.GenerateQRCodePNG(url, 256)
	if err != nil {
		http.Error(w, "Failed to generate QR Code", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(pngBytes)
}

func (s *Server) handleInstallHooks(w http.ResponseWriter, r *http.Request) {
	// Serialized with the permissions editor: both rewrite settings.json.
	s.settingsMu.Lock()
	err := hooks.InstallClaudeHooks(s.port)
	s.settingsMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Hooks successfully installed in ~/.claude/settings.json",
	})
}

func (s *Server) handleStaticWeb(w http.ResponseWriter, r *http.Request) {
	data, err := s.embeddedFS.ReadFile("index.html")
	if err != nil {
		http.Error(w, "Web UI not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// handleHealth serves the token-gated liveness endpoint used by watchdogs and
// the single-instance guard. last_event_at is null until the first hook event
// is accepted.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	lastEvent := s.store.LastEventAt()

	resp := struct {
		Status      string     `json:"status"`
		Version     string     `json:"version"`
		UptimeS     int64      `json:"uptime_s"`
		LastEventAt *time.Time `json:"last_event_at"`
	}{
		Status:  "ok",
		Version: ServerVersion,
		UptimeS: int64(time.Since(s.uptimeStart) / time.Second),
	}
	if !lastEvent.IsZero() {
		t := lastEvent
		resp.LastEventAt = &t
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- M11: runtime relay configuration (GET/POST /api/relay) ---

// relayURLFileName is the file under ~/.claude/ persisting the relay URL set
// from the web dashboard. Empty/absent = relay disabled.
const relayURLFileName = "claude-remote-relay.url"

// relayURLFilePath overrides the default relay-URL file location. Empty in
// production; set by tests for isolation (same pattern as auth.tokenFilePath).
var relayURLFilePath string

// relayStatus is the /api/relay response contract.
type relayStatus struct {
	URL       string `json:"url"`       // effective relay URL; "" = disabled
	Active    bool   `json:"active"`    // a relay URL is configured
	Connected bool   `json:"connected"` // the client currently has an open link
}

// ResolveRelayURL applies the M11 startup precedence: --relay flag >
// RELAY_URL env > URL persisted from the web dashboard. A flag/env value
// wins for THAT run only; the persisted file is rewritten exclusively by
// POST /api/relay. Whitespace-only values count as empty.
func ResolveRelayURL(flagVal, envVal, fileVal string) string {
	for _, v := range []string{flagVal, envVal, fileVal} {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// validRelayURL accepts the two dial-out-safe relay forms: wss:// and https://
// (relayclient maps https to wss) with a non-empty host. Plain ws:// and
// http:// are rejected — the relay URL leaves the LAN, so transport
// encryption is mandatory — as is anything that is not a URL at all.
func validRelayURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "wss" || u.Scheme == "https") && u.Host != ""
}

// relayURLPath resolves the persisted relay-URL file location, mirroring
// internal/auth's home-dir resolution (~/.claude/claude-remote-relay.url).
func relayURLPath() (string, error) {
	if relayURLFilePath != "" {
		return relayURLFilePath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("api: could not resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude", relayURLFileName), nil
}

// LoadPersistedRelayURL returns the relay URL saved from the web dashboard
// ("" when absent, unreadable, or blank — all mean "disabled"). main.go folds
// it into ResolveRelayURL at startup.
func LoadPersistedRelayURL() string {
	path, err := relayURLPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "" // absent file is the normal "never configured" case
	}
	return strings.TrimSpace(string(data))
}

// persistRelayURL stores the dashboard-set relay URL. An empty URL disables
// the relay and REMOVES the file (absent = disabled), so a stale URL can
// never resurrect itself on the next start.
func persistRelayURL(relayURL string) error {
	path, err := relayURLPath()
	if err != nil {
		return err
	}
	if relayURL == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("api: could not remove relay URL file %s: %w", path, err)
		}
		return nil
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("api: could not create relay URL directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(relayURL), 0600); err != nil {
		return fmt.Errorf("api: could not write relay URL file %s: %w", path, err)
	}
	return nil
}

// handleRelay serves GET (current relay state) and POST (validate, apply
// immediately, and persist a new relay URL). Both are token-gated like every
// other /api/* endpoint.
func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.RelayState())
	case http.MethodPost:
		s.handleRelayPost(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRelayPost applies a relay URL at runtime: stop the current client,
// start a fresh one for a non-empty URL, persist the setting, respond with
// the new state.
func (s *Server) handleRelayPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	relayURL := strings.TrimSpace(body.URL)
	if relayURL != "" && !validRelayURL(relayURL) {
		http.Error(w, `{"error":"relay URL must use wss:// or https:// (empty disables the relay)"}`, http.StatusBadRequest)
		return
	}

	s.StartRelay(relayURL)
	if err := persistRelayURL(relayURL); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"could not persist relay setting: %v"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.RelayState())
}

// RelayState returns the current relay configuration and link state (the GET
// /api/relay contract; also the POST response).
func (s *Server) RelayState() relayStatus {
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	return s.relayStateLocked()
}

// StartRelay makes relayURL the effective relay setting: it stops the current
// client (if any) and, for a non-empty URL, creates and starts a new one.
// main.go calls this once at boot with the ResolveRelayURL() winner; POST
// /api/relay calls it on every apply. The persisted file is NOT touched here
// — only the endpoint persists.
func (s *Server) StartRelay(relayURL string) {
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	s.stopRelayLocked()
	s.relayURL = strings.TrimSpace(relayURL)
	if s.relayURL == "" {
		return
	}
	// The relay link now carries phone-originated COMMANDS (approvals,
	// permission edits), so the transport must be encrypted whenever it
	// leaves the machine: wss/https only, with plain ws/http tolerated
	// solely for loopback test relays.
	if !validRelayURL(s.relayURL) && !loopbackRelayURL(s.relayURL) {
		log.Printf("refusing relay URL %q: command traffic requires wss:// or https:// off-loopback", s.relayURL)
		s.relayURL = ""
		return
	}
	// M5.1a caveat: Client.Start() is not itself guarded against double
	// invocation, so a client instance must be started exactly once. That
	// holds here because every apply builds a FRESH client and never calls
	// Start on an existing one.
	c := relayclient.NewClient(s.relayURL, s.token, s.store)
	// Phone-originated command frames ride the relay link verbatim; hand
	// them to the same dispatcher the /ws read loop uses.
	c.OnClientFrame = func(frame []byte) { _ = s.handleClientFrameRaw(frame) }
	c.Start(context.Background())
	s.relayClient = c
}

// loopbackRelayURL accepts ws:// or http:// ONLY on loopback hosts (tests
// run fake relays on 127.0.0.1).
func loopbackRelayURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "ws" && u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// StopRelay stops the current relay client and clears the setting (the
// graceful-shutdown path; the process is exiting, so persistence is not
// touched).
func (s *Server) StopRelay() {
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	s.stopRelayLocked()
}

// stopRelayLocked stops the current relay client (if any). Caller holds
// relayMu.
func (s *Server) stopRelayLocked() {
	if s.relayClient != nil {
		s.relayClient.Stop()
		s.relayClient = nil
	}
	s.relayURL = ""
}

// relayStateLocked snapshots the relay state. Caller holds relayMu.
func (s *Server) relayStateLocked() relayStatus {
	return relayStatus{
		URL:       s.relayURL,
		Active:    s.relayURL != "",
		Connected: s.relayClient != nil && s.relayClient.Connected(),
	}
}

// startStatsHeartbeat starts the periodic `stats` broadcast loop (M4b): a
// single server-wide ticker that fans a lightweight summary frame out to ALL
// subscribers via store.BroadcastStats. It never notifies and never touches
// sessions — pure liveness plus summary. Idempotent: Start() invokes it, and
// tests may invoke it directly against an httptest server. Shutdown stops it.
func (s *Server) startStatsHeartbeat() {
	s.statsStartOnce.Do(func() {
		interval := s.statsInterval
		if interval <= 0 {
			interval = defaultStatsInterval
		}
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-s.statsStop:
					return
				case <-ticker.C:
					s.store.BroadcastStats()
				}
			}
		}()
	})
}

// Start runs the HTTP server on 0.0.0.0:<port>. It returns http.ErrServerClosed
// after Shutdown() drains the server; any other error means the listener
// failed (e.g. the port is already bound).
func (s *Server) Start() error {
	s.startStatsHeartbeat()
	addr := fmt.Sprintf("0.0.0.0:%d", s.port)
	s.httpServer = &http.Server{Addr: addr, Handler: s.mux}
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully drains in-flight requests (bounded by ctx) and closes
// the listeners. It is a no-op when the server was never started (the stats
// heartbeat, if running, is stopped either way).
func (s *Server) Shutdown(ctx context.Context) error {
	s.statsStopOnce.Do(func() { close(s.statsStop) })
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}
