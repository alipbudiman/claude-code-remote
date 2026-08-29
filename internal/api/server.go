package api

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"claude-remote-server/internal/hooks"
	"claude-remote-server/internal/models"
	"claude-remote-server/internal/network"
	"claude-remote-server/internal/state"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow LAN connections from Android APK and web clients
	},
}

// Server encapsulates the HTTP and WebSocket API router
type Server struct {
	port       int
	store      *state.Store
	embeddedFS embed.FS
	mux        *http.ServeMux
	hostIPs    []string
}

// NewServer initializes an API server with all routes configured
func NewServer(port int, store *state.Store, embeddedFS embed.FS, hostIPs []string) *Server {
	s := &Server{
		port:       port,
		store:      store,
		embeddedFS: embeddedFS,
		mux:        http.NewServeMux(),
		hostIPs:    hostIPs,
	}

	s.setupRoutes()
	return s
}

// setupRoutes registers all HTTP handlers, WebSocket endpoints, and static routes
func (s *Server) setupRoutes() {
	// CORS Middleware wrapper
	withCORS := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next(w, r)
		}
	}

	// 1. Claude Code Hook Receiver Endpoint
	s.mux.HandleFunc("/api/hook", withCORS(s.handleHookPost))

	// 2. WebSocket Real-Time Stream Endpoint
	s.mux.HandleFunc("/ws", s.handleWebSocket)

	// 3. REST Endpoints
	s.mux.HandleFunc("/api/status", withCORS(s.handleStatus))
	s.mux.HandleFunc("/api/sessions", withCORS(s.handleSessions))
	s.mux.HandleFunc("/api/subagents", withCORS(s.handleSubagents))
	s.mux.HandleFunc("/api/qr", withCORS(s.handleQRCode))
	s.mux.HandleFunc("/api/install-hooks", withCORS(s.handleInstallHooks))

	// 4. Embedded Web UI Dashboard
	s.mux.HandleFunc("/", s.handleStaticWeb)
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
	conn, err := upgrader.Upgrade(w, r, nil)
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
