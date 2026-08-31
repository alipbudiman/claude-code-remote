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
// push frames back down the socket with push().
type fakeRelay struct {
	srv *httptest.Server

	mu     sync.Mutex
	conn   *websocket.Conn
	connCh chan *websocket.Conn // closed-over live connection, per dial

	frames chan string
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	f := &fakeRelay{
		connCh: make(chan *websocket.Conn, 4),
		frames: make(chan string, 32),
	}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.connCh <- conn
		defer conn.Close()
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
	f := newFakeRelay(t)

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

	// (b) Store broadcast -> forwarded frame.
	store.HandleHookEvent(models.HookPayload{
		HookEventName: "UserPromptSubmit",
		SessionID:     "sess-1",
		Cwd:           "D:\\proj",
	})
	frame = nextFrame(t, f)
	if frame["type"] != "session_update" {
		t.Fatalf("frame after hook event = %v, want session_update", frame["type"])
	}
	sess := frame["data"].(map[string]interface{})
	if sess["id"] != "sess-1" {
		t.Fatalf("session_update session id = %v, want sess-1", sess["id"])
	}

	// (c) peer_joined -> fresh initial_state snapshot.
	f.push(t, conn, models.WebSocketMessage{Type: "peer_joined", Timestamp: time.Now()})
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
