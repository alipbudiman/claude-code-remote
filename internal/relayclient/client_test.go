package relayclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"claude-remote-server/internal/models"
	"claude-remote-server/internal/state"
)

// testToken is a valid 64-char hex (32-byte) shared secret.
const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeRelay accepts every /ws upgrade and records every inbound frame; tests
// push frames back down the socket with push(). pingInterval > 0 makes it
// ping its member like the real relay (0 = silent relay); every dial is
// counted so tests can observe reconnects.
type fakeRelay struct {
	srv *httptest.Server

	pingInterval time.Duration

	mu     sync.Mutex
	conns  int // completed dials (reconnect counter)
	connCh chan *websocket.Conn

	frames chan string
}

func newFakeRelay(t *testing.T, pingInterval time.Duration) *fakeRelay {
	t.Helper()
	f := &fakeRelay{
		pingInterval: pingInterval,
		connCh:       make(chan *websocket.Conn, 4),
		frames:       make(chan string, 32),
	}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns++
		f.mu.Unlock()
		f.connCh <- conn
		defer conn.Close()

		if f.pingInterval > 0 {
			// Live-relay keepalive: ping the member, exactly like cmd/relay.
			done := make(chan struct{})
			defer close(done)
			go func() {
				ticker := time.NewTicker(f.pingInterval)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						_ = conn.WriteControl(websocket.PingMessage, []byte("ka"), time.Now().Add(2*time.Second))
					}
				}
			}()
		}

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			select {
			case f.frames <- string(data):
			default:
			}
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// connCount reports how many dials the relay has served.
func (f *fakeRelay) connCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns
}

// liveConn returns the most recent member connection (waiting briefly).
func (f *fakeRelay) liveConn(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case c := <-f.connCh:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("fake relay: no connection arrived")
		return nil
	}
}

// push sends a frame to the live member (the desktop client under test).
func (f *fakeRelay) push(t *testing.T, conn *websocket.Conn, v interface{}) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("fake relay: set write deadline: %v", err)
	}
	if err := conn.WriteJSON(v); err != nil {
		t.Fatalf("fake relay: push: %v", err)
	}
}

// nextFrame reads the next frame the client sent to the relay.
func nextFrame(t *testing.T, f *fakeRelay) map[string]interface{} {
	t.Helper()
	select {
	case raw := <-f.frames:
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("frame not JSON: %q: %v", raw, err)
		}
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a frame at the relay")
		return nil
	}
}

// (unit) URL derivation: https→wss, http→ws, /ws appended when the address
// has no path, token always attached as ?token=.
func TestWSURLDerivation(t *testing.T) {
	cases := []struct{ in, want string }{
		{"wss://relay.example.com", "wss://relay.example.com/ws?token=" + testToken},
		{"https://relay.example.com", "wss://relay.example.com/ws?token=" + testToken},
		{"http://127.0.0.1:8081", "ws://127.0.0.1:8081/ws?token=" + testToken},
		{"ws://127.0.0.1:8081/", "ws://127.0.0.1:8081/ws?token=" + testToken},
		{"wss://relay.example.com/custom", "wss://relay.example.com/custom?token=" + testToken},
	}
	for _, tc := range cases {
		c := NewClient(tc.in, testToken, nil)
		if got := c.wsURL(); got != tc.want {
			t.Errorf("wsURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// 6. Against a fake relay: on open the client pushes an initial_state
// snapshot; every store broadcast is forwarded; peer_joined triggers a fresh
// snapshot push.
func TestClientSnapshotBroadcastAndPeerJoined(t *testing.T) {
	store := state.NewStore(0, nil)
	f := newFakeRelay(t, 0)

	relayAddr := "ws" + strings.TrimPrefix(f.srv.URL, "http")
	c := NewClient(relayAddr, testToken, store)
	c.Start(context.Background())
	defer c.Stop()

	conn := f.liveConn(t)

	// (a) On open: initial_state snapshot.
	frame := nextFrame(t, f)
	if frame["type"] != "initial_state" {
		t.Fatalf("first frame type = %v, want initial_state", frame["type"])
	}
	data, ok := frame["data"].(map[string]interface{})
	if !ok || data["server_version"] == nil {
		t.Fatalf("initial_state carries no snapshot: %v", frame["data"])
	}

	// (b) Store broadcast -> forwarded frame. A UserPromptSubmit now also
	// emits a process_event (live feed, 2026-09-02) BEFORE session_update,
	// so drain any process_event frames first.
	store.HandleHookEvent(models.HookPayload{
		HookEventName: "UserPromptSubmit",
		SessionID:     "sess-1",
		Cwd:           "D:\\proj",
	})
	for frame = nextFrame(t, f); frame["type"] == "process_event"; frame = nextFrame(t, f) {
	}
	if frame["type"] != "session_update" {
		t.Fatalf("frame after hook event = %v, want session_update", frame["type"])
	}
	sess := frame["data"].(map[string]interface{})
	if sess["id"] != "sess-1" {
		t.Fatalf("session_update session id = %v, want sess-1", sess["id"])
	}

	// (c) peer_joined -> fresh initial_state snapshot. (Sent in the real
	// relay's shape: a type+timestamp control frame with no data field.)
	f.push(t, conn, map[string]interface{}{"type": "peer_joined", "timestamp": time.Now()})
	frame = nextFrame(t, f)
	if frame["type"] != "initial_state" {
		t.Fatalf("frame after peer_joined = %v, want initial_state", frame["type"])
	}
	snap := frame["data"].(map[string]interface{})
	sessions := snap["sessions"].([]interface{})
	if len(sessions) != 1 {
		t.Fatalf("snapshot sessions = %d, want 1 (the hook-created session)", len(sessions))
	}

	c.Stop()
}

// (regression, fix round 1) A relay that PINGS on schedule must keep the
// client's read deadline alive even though the link carries no relay→client
// DATA frames: pings are consumed inside ReadMessage and never surface as
// messages, so only a deadline-refreshing PingHandler prevents a permanent
// read-timeout → reconnect cycle (the connection used to die at exactly
// readWindow on every quiet link).
func TestClientSurvivesRelayPingsPastReadWindow(t *testing.T) {
	store := state.NewStore(0, nil)
	f := newFakeRelay(t, 100*time.Millisecond) // relay pings every 100ms

	c := NewClient("ws"+strings.TrimPrefix(f.srv.URL, "http"), testToken, store)
	c.readWindow = 500 * time.Millisecond // 3 pings per window, like 30s/90s
	c.Start(context.Background())
	defer c.Stop()

	// On-open snapshot must arrive.
	if frame := nextFrame(t, f); frame["type"] != "initial_state" {
		t.Fatalf("first frame type = %v, want initial_state", frame["type"])
	}

	// Survive well past 3x the read window (1.5s) with ZERO reconnects.
	time.Sleep(2 * time.Second)
	if n := f.connCount(); n != 1 {
		t.Fatalf("relay served %d connections, want exactly 1 — the read deadline expired despite live pings", n)
	}
	// A reconnect would also have re-pushed a snapshot: nothing may follow.
	select {
	case raw := <-f.frames:
		t.Fatalf("unexpected extra frame after the on-open snapshot: %s", raw)
	default:
	}
}

// (fix round 1) The flip side: a SILENT relay (no pings, no frames — dead or
// wedged) must still trip the read window and trigger the reconnect path.
func TestClientReconnectsWhenRelayGoesSilent(t *testing.T) {
	store := state.NewStore(0, nil)
	f := newFakeRelay(t, 0) // never pings, never sends

	c := NewClient("ws"+strings.TrimPrefix(f.srv.URL, "http"), testToken, store)
	c.readWindow = 500 * time.Millisecond
	c.Start(context.Background())
	defer c.Stop()

	// First connection and its on-open snapshot...
	if frame := nextFrame(t, f); frame["type"] != "initial_state" {
		t.Fatalf("first frame type = %v, want initial_state", frame["type"])
	}

	// ...then the 500ms read window expires with no keepalive traffic and
	// the client must redial (window + reconnect backoff).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f.connCount() >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("relay served %d connections within 5s, want >= 2 — a silent relay must trigger reconnect", f.connCount())
}

// (M11) Connected() reports the live link state for /api/relay: false before
// any dial, true once a dial succeeds, false again after the link drops.
func TestConnectedReflectsRelayLink(t *testing.T) {
	store := state.NewStore(0, nil)
	f := newFakeRelay(t, 0)

	c := NewClient("ws"+strings.TrimPrefix(f.srv.URL, "http"), testToken, store)
	if c.Connected() {
		t.Fatal("Connected() before Start = true, want false")
	}
	c.Start(context.Background())
	defer c.Stop()

	// The relay serving our dial means the link is open.
	serverConn := f.liveConn(t)
	waitForConnected(t, c, true, "after a successful dial")

	// Dropping the RELAY SIDE of the link breaks the connection (killing the
	// whole httptest server would not: hijacked websocket conns are not
	// force-closed by Close). The client will redial after its backoff —
	// that is its job — but Connected() must report false while the link is
	// down, and the reconnect backoff (>=1s) leaves a wide window to see it.
	_ = serverConn.Close()
	waitForConnected(t, c, false, "after the relay link dropped")
}

// waitForConnected polls c.Connected() until it equals want, failing the test
// after 3s. The connect/disconnect transitions land on another goroutine, so
// the flag is observed with a small bounded poll instead of a fixed sleep.
func waitForConnected(t *testing.T, c *Client, want bool, when string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for c.Connected() != want {
		if time.Now().After(deadline) {
			t.Fatalf("Connected() never became %v %s", want, when)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
