package api

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"claude-remote-server/internal/hooks"
	"claude-remote-server/internal/models"
	"claude-remote-server/internal/network"
	"claude-remote-server/internal/state"
)

// androidWebViewOrigin is the Origin the Android APK WebView sends for every
// request (it serves the bundled dashboard from this virtual https origin).
const androidWebViewOrigin = "https://appassets.androidplatform.net"

// Server encapsulates the HTTP and WebSocket API router
type Server struct {
	port       int
	store      *state.Store
	embeddedFS embed.FS
	mux        *http.ServeMux
	hostIPs    []string
	token      string
	upgrader   websocket.Upgrader
}

// NewServer initializes an API server with all routes configured. The token
// is the shared secret required by every /api/* endpoint and the /ws upgrade.
func NewServer(port int, store *state.Store, embeddedFS embed.FS, hostIPs []string, token string) *Server {
	s := &Server{
		port:       port,
		store:      store,
		embeddedFS: embeddedFS,
		mux:        http.NewServeMux(),
		hostIPs:    hostIPs,
		token:      token,
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

	s.store.HandleHookEvent(payload)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Send initial full state snapshot
	snapshot := s.store.GetSnapshot()
	initMsg := models.WebSocketMessage{
		Type:      "initial_state",
		Data:      snapshot,
		Timestamp: time.Now(),
	}
	if err := conn.WriteJSON(initMsg); err != nil {
		return
	}

	// Subscribe to live broadcast channel
	subCh := s.store.Subscribe()
	defer s.store.Unsubscribe(subCh)

	// Pump incoming messages from store to WebSocket client
	go func() {
		for msg := range subCh {
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}()

	// Keep alive & read dummy messages
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
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
	err := hooks.InstallClaudeHooks(s.port)
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

// Start runs the HTTP server on 0.0.0.0:<port>
func (s *Server) Start() error {
	addr := fmt.Sprintf("0.0.0.0:%d", s.port)
	return http.ListenAndServe(addr, s.mux)
}
