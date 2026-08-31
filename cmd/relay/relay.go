// Command relay is the stateless room hub deployed on Railway (M5.1). The
// desktop server dials OUT to it (surviving CGNAT and firewalls — no inbound
// ports), the phone joins the SAME token-keyed room, and this process forwards
// every WebSocket frame from one room member to the others. The room key is
// the SAME 64-hex shared secret the desktop already uses
// (~/.claude/claude-remote-token), so the phone's MonitoringService works
// against the relay URL with zero protocol changes: the frames crossing the
// relay are the existing message types plus one relay-control frame,
// "peer_joined", which tells the desktop to push a fresh snapshot.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"claude-remote-server/internal/models"
)

const (
	// defaultPingInterval is the keepalive cadence: every member is pinged
	// this often, and a member that goes three intervals without a pong (or
	// any traffic) is reaped instead of pinning at "connected".
	defaultPingInterval = 30 * time.Second

	// wsWriteTimeout bounds every relay write so a wedged peer cannot stall
	// a forward or a ping forever.
	wsWriteTimeout = 10 * time.Second
)

// validTokenRE matches exactly 64 hex characters — the same token shape the
// desktop's auth package generates and validates.
var validTokenRE = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// relayServer is the room hub: room key (lowercased token) -> members.
// It holds no other state — any valid 64-hex token creates a room, and frames
// only ever flow between holders of the SAME token.
type relayServer struct {
	pingInterval time.Duration
	upgrader     websocket.Upgrader

	mu    sync.Mutex
	rooms map[string]map[*relayPeer]bool
}

// newRelayServer builds a hub with the given keepalive interval (tests shrink
// it; anything <= 0 falls back to the default).
func newRelayServer(pingInterval time.Duration) *relayServer {
	if pingInterval <= 0 {
		pingInterval = defaultPingInterval
	}
	return &relayServer{
		pingInterval: pingInterval,
		rooms:        make(map[string]map[*relayPeer]bool),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// Any origin may open a relay socket: the 64-hex room token is
			// the only gate, and phone WebViews join from origins the relay
			// cannot know in advance.
			CheckOrigin: func(*http.Request) bool { return true },
			// Subprotocols stays nil on purpose: room tokens are dynamic, so
			// the negotiated subprotocol is echoed per-request via the
			// Upgrade responseHeader instead of a fixed list (see handleWS).
		},
	}
}

// handleHealth serves the unauthenticated health check Railway probes.
func (s *relayServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "claude-remote-relay",
	})
}

// extractToken pulls the room token from "?token=" or, failing that, from the
// "Sec-WebSocket-Protocol: claude-remote.<tok>" handshake (the only option
// browser/WebView JS has). It returns the token plus the subprotocol string
// the client offered ("" when none), so the upgrade can echo it back.
func (s *relayServer) extractToken(r *http.Request) (tok, offeredSubprotocol string) {
	tok = r.URL.Query().Get("token")
	for _, p := range websocket.Subprotocols(r) {
		if strings.HasPrefix(p, "claude-remote.") {
			offeredSubprotocol = p
			if tok == "" {
				tok = strings.TrimPrefix(p, "claude-remote.")
			}
		}
	}
	return tok, offeredSubprotocol
}

// handleWS upgrades one member into its token-keyed room and pumps frames
// until the connection dies.
func (s *relayServer) handleWS(w http.ResponseWriter, r *http.Request) {
	tok, subproto := s.extractToken(r)
	if !validTokenRE.MatchString(tok) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Echo the authenticated subprotocol back to browser/WebView clients,
	// which fail the handshake without it (mirrors internal/api/server.go,
	// where Upgrader.Subprotocols serves the same purpose for one fixed
	// token; here the token is dynamic so the echo rides responseHeader).
	var respHeader http.Header
	if subproto != "" {
		respHeader = http.Header{"Sec-WebSocket-Protocol": []string{subproto}}
	}
	conn, err := s.upgrader.Upgrade(w, r, respHeader)
	if err != nil {
		return
	}

	p := &relayPeer{conn: conn, room: strings.ToLower(tok)}
	s.join(p)
	defer s.leave(p)

	// done stops the ping ticker when the read loop below exits.
	done := make(chan struct{})
	defer close(done)

	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// WriteControl is documented as safe alongside other
				// writers, but it shares writeMu so exactly one writer
				// touches this connection at any instant (M4a pattern).
				p.writeMu.Lock()
				err := p.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout))
				p.writeMu.Unlock()
				if err != nil {
					_ = p.conn.Close() // release the read loop
					return
				}
			}
		}
	}()

	// Read loop with a liveness deadline: every WebSocket stack answers pings
	// with pongs automatically, so a healthy member keeps the deadline
	// extended forever; anything else expires and tears the member down.
	readWindow := 3 * s.pingInterval
	extend := func() error { return conn.SetReadDeadline(time.Now().Add(readWindow)) }
	if err := extend(); err != nil {
		return
	}
	conn.SetPongHandler(func(string) error { return extend() })

	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := extend(); err != nil {
			return
		}
		s.forward(p, msgType, data)
	}
}

// relayPeer is one authenticated member connection. All writes funnel through
// writeMu (gorilla allows one concurrent writer), each bounded by
// wsWriteTimeout.
type relayPeer struct {
	conn    *websocket.Conn
	room    string
	writeMu sync.Mutex
}

// send writes one frame; a wedged peer is hard-closed so its read loop and
// room membership are reaped immediately instead of stalling the forwarder.
func (p *relayPeer) send(msgType int, data []byte) bool {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	if err := p.conn.WriteMessage(msgType, data); err != nil {
		_ = p.conn.Close()
		return false
	}
	return true
}

// join registers the member and tells the room's other members a peer arrived
// (the desktop answers peer_joined with a fresh snapshot push).
func (s *relayServer) join(p *relayPeer) {
	s.mu.Lock()
	room, ok := s.rooms[p.room]
	if !ok {
		room = make(map[*relayPeer]bool)
		s.rooms[p.room] = room
	}
	room[p] = true
	others := make([]*relayPeer, 0, len(room)-1)
	for q := range room {
		if q != p {
			others = append(others, q)
		}
	}
	s.mu.Unlock()

	log.Printf("relay: join room=%s members=%d rooms=%d", roomTag(p.room), s.memberCount(p.room), s.roomCount())

	if len(others) > 0 {
		msg, _ := json.Marshal(models.WebSocketMessage{Type: "peer_joined", Timestamp: time.Now()})
		for _, q := range others {
			q.send(websocket.TextMessage, msg)
		}
	}
}

// leave removes the member and deletes the room when it empties.
func (s *relayServer) leave(p *relayPeer) {
	s.mu.Lock()
	room, ok := s.rooms[p.room]
	if ok {
		delete(room, p)
		if len(room) == 0 {
			delete(s.rooms, p.room)
		}
	}
	members, rooms := len(room), len(s.rooms)
	s.mu.Unlock()

	log.Printf("relay: leave room=%s members=%d rooms=%d", roomTag(p.room), members, rooms)
}

// forward relays a frame verbatim to every OTHER member of the sender's room.
func (s *relayServer) forward(from *relayPeer, msgType int, data []byte) {
	s.mu.Lock()
	room := s.rooms[from.room]
	others := make([]*relayPeer, 0, len(room))
	for q := range room {
		if q != from {
			others = append(others, q)
		}
	}
	s.mu.Unlock()

	for _, q := range others {
		q.send(msgType, data)
	}
}

// closeAll hard-closes every member connection (graceful shutdown); each
// member's read loop then fails and its deferred leave() runs.
func (s *relayServer) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, room := range s.rooms {
		for p := range room {
			_ = p.conn.Close()
		}
	}
}

// roomCount returns the number of live rooms (logging + tests).
func (s *relayServer) roomCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms)
}

// memberCount returns the number of members in one room (logging + tests).
func (s *relayServer) memberCount(roomKey string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms[roomKey])
}

// roomTag returns an 8-char hash prefix identifying a room in logs without
// leaking any part of the token itself.
func roomTag(roomKey string) string {
	sum := sha256.Sum256([]byte("claude-remote-relay:" + roomKey))
	return hex.EncodeToString(sum[:])[:8]
}
