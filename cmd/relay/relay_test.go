package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Two valid 64-hex tokens (different rooms) for these tests.
const (
	tokenA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tokenB = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

// newTestRelay starts the real relay handlers on an httptest server with the
// ping interval shrunk to 100ms so keepalive paths exercise quickly.
func newTestRelay(t *testing.T) (*relayServer, *httptest.Server) {
	t.Helper()
	rs := newRelayServer(100 * time.Millisecond)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", rs.handleHealth)
	mux.HandleFunc("/ws", rs.handleWS)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return rs, ts
}

// wsDial connects one relay member. useSubprotocol selects the browser-style
// handshake (Sec-WebSocket-Protocol: claude-remote.<tok>); otherwise the token
// rides the ?token= query parameter.
func wsDial(t *testing.T, ts *httptest.Server, token string, useSubprotocol bool) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	dialer := websocket.DefaultDialer
	if useSubprotocol {
		dialer = &websocket.Dialer{Subprotocols: []string{"claude-remote." + token}}
	} else {
		url += "?token=" + token
	}
	conn, resp, err := dialer.Dial(url, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial relay (subprotocol=%v): status=%d err=%v", useSubprotocol, status, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// readMsg reads one data frame with a test deadline.
func readMsg(t *testing.T, conn *websocket.Conn) (int, []byte) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	mt, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return mt, data
}

// waitFor polls cond until it holds or the 2s bound expires.
func waitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within 2s: %s", desc)
}

// 1. Two members of the SAME token room (one via query token, one via
// subprotocol) receive each other's frames verbatim — text and binary.
func TestRelayForwardsFramesWithinRoom(t *testing.T) {
	_, ts := newTestRelay(t)

	a := wsDial(t, ts, tokenA, false) // token via ?token=
	b := wsDial(t, ts, tokenA, true)  // token via subprotocol

	// The subprotocol member must have the handshake echoed back (browser
	// clients fail the upgrade without it).
	if got := b.Subprotocol(); got != "claude-remote."+tokenA {
		t.Fatalf("negotiated subprotocol = %q, want %q", got, "claude-remote."+tokenA)
	}
	if got := a.Subprotocol(); got != "" {
		t.Fatalf("query-token member negotiated subprotocol %q, want none", got)
	}

	// B's join announced a peer to A: consume it first.
	_, data := readMsg(t, a)
	var join map[string]interface{}
	if err := json.Unmarshal(data, &join); err != nil || join["type"] != "peer_joined" {
		t.Fatalf("A's first frame after B joins = %q, want peer_joined", data)
	}

	// Text frame A -> B, verbatim.
	if err := a.WriteMessage(websocket.TextMessage, []byte(`{"type":"session_update","data":{"x":1}}`)); err != nil {
		t.Fatalf("A write: %v", err)
	}
	mt, data := readMsg(t, b)
	if mt != websocket.TextMessage || string(data) != `{"type":"session_update","data":{"x":1}}` {
		t.Fatalf("B received (%d) %q, want verbatim text frame", mt, data)
	}

	// Binary frame B -> A, verbatim pass-through.
	if err := b.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02, 0xfe}); err != nil {
		t.Fatalf("B write: %v", err)
	}
	mt, data = readMsg(t, a)
	if mt != websocket.BinaryMessage || string(data) != string([]byte{0x01, 0x02, 0xfe}) {
		t.Fatalf("A received (%d) %v, want verbatim binary frame", mt, data)
	}
}

// 2. Different tokens are isolated rooms: B never sees A's frames.
func TestRelayIsolatesDifferentTokenRooms(t *testing.T) {
	rs, ts := newTestRelay(t)

	a := wsDial(t, ts, tokenA, false)
	b := wsDial(t, ts, tokenB, true)

	// Both rooms exist right after the joins. (Asserted BEFORE the quiet
	// window below: the relay pings at 100ms and correctly reaps a member
	// that goes 3 intervals without reading, so an idle member has a ~300ms
	// lifetime under this test's shrunken interval.)
	waitFor(t, "two rooms registered", func() bool { return rs.roomCount() == 2 })

	if err := a.WriteMessage(websocket.TextMessage, []byte("room-a-secret")); err != nil {
		t.Fatalf("A write: %v", err)
	}

	// B must receive no data frame within a generous quiet window (the relay
	// pings at 100ms, but control frames never surface from ReadMessage).
	if err := b.SetReadDeadline(time.Now().Add(400 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, _, err := b.ReadMessage(); err == nil {
		t.Fatal("B received a frame from A's room, want isolation")
	} else if !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("B read error = %v, want deadline timeout", err)
	}
}

// 3. When B joins A's room, A is told via a peer_joined frame (the desktop
// uses this to push a fresh snapshot).
func TestRelayPeerJoinedAnnouncement(t *testing.T) {
	_, ts := newTestRelay(t)

	a := wsDial(t, ts, tokenA, false)
	_ = wsDial(t, ts, tokenA, true) // B joins; the announcement lands on A

	_, data := readMsg(t, a)
	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("peer_joined frame not JSON: %q: %v", data, err)
	}
	if msg["type"] != "peer_joined" {
		t.Fatalf("frame type = %v, want peer_joined", msg["type"])
	}
	if ts, ok := msg["timestamp"]; !ok || ts == nil {
		t.Fatalf("peer_joined frame has no timestamp: %q", data)
	}
	// The control frame carries exactly type+timestamp — no data field.
	if _, hasData := msg["data"]; hasData {
		t.Fatalf("peer_joined frame must not carry a data field: %q", data)
	}
}

// 4. /health is open (Railway health check); /ws demands a valid 64-hex token.
func TestRelayHealthOpenAndWSTokenGated(t *testing.T) {
	_, ts := newTestRelay(t)

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health status = %d, want 200", resp.StatusCode)
	}
	var h map[string]string
	if err := json.Unmarshal(body, &h); err != nil || h["status"] != "ok" || h["service"] != "claude-remote-relay" {
		t.Fatalf("/health body = %q, want status ok + service claude-remote-relay", body)
	}

	// No token at all -> 401 before the upgrade.
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	if _, resp, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
		t.Fatal("dial without token succeeded, want 401")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dial without token: resp=%v, want 401", resp)
	}

	// Malformed token (not 64-hex) -> 401 as well.
	if _, resp, err := websocket.DefaultDialer.Dial(url+"?token=short", nil); err == nil {
		t.Fatal("dial with malformed token succeeded, want 401")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dial with malformed token: resp=%v, want 401", resp)
	}
}

// 5. Disconnect cleanup: a closed member leaves its room; an emptied room is
// deleted from the hub.
func TestRelayDisconnectCleanup(t *testing.T) {
	rs, ts := newTestRelay(t)

	a := wsDial(t, ts, tokenA, false)
	b := wsDial(t, ts, tokenA, true)

	waitFor(t, "both members registered", func() bool { return rs.memberCount(tokenA) == 2 })
	if rs.roomCount() != 1 {
		t.Fatalf("room count = %d, want 1", rs.roomCount())
	}

	if err := a.Close(); err != nil {
		t.Fatalf("close A: %v", err)
	}
	waitFor(t, "A removed from room", func() bool { return rs.memberCount(tokenA) == 1 })
	if rs.roomCount() != 1 {
		t.Fatalf("room count after A leaves = %d, want 1", rs.roomCount())
	}

	if err := b.Close(); err != nil {
		t.Fatalf("close B: %v", err)
	}
	waitFor(t, "empty room deleted", func() bool { return rs.roomCount() == 0 && rs.memberCount(tokenA) == 0 })
}
