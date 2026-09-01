package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"claude-remote-server/internal/state"
	"claude-remote-server/web"
)

// testToken is a valid 64-char hex (32-byte) shared secret for these tests.
const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// newTestAPIServer builds the real server + mux exactly as main.go does
// (real store, embedded FS) with a known token.
func newTestAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := state.NewStore(0, nil)
	srv := NewServer(0, store, web.EmbeddedFS, nil, testToken)
	ts := httptest.NewServer(srv.mux)
	t.Cleanup(ts.Close)
	return ts
}

func wsURL(ts *httptest.Server) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
}

// (a) GET /api/status without a token -> 401
func TestAPIStatusWithoutTokenReturns401(t *testing.T) {
	ts := newTestAPIServer(t)

	resp, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != `{"error":"unauthorized"}` {
		t.Fatalf("401 body = %q, want unauthorized JSON", string(body))
	}
}

// A wrong token must also be rejected.
func TestAPIStatusWithWrongTokenReturns401(t *testing.T) {
	ts := newTestAPIServer(t)

	req, _ := http.NewRequest("GET", ts.URL+"/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("ff", 32))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status with wrong token = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// (b) GET /api/status with Authorization: Bearer <tok> -> 200
func TestAPIStatusWithBearerTokenReturns200(t *testing.T) {
	ts := newTestAPIServer(t)

	req, _ := http.NewRequest("GET", ts.URL+"/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status with bearer token = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// (c) GET /api/status with ?token=<tok> -> 200
func TestAPIStatusWithQueryTokenReturns200(t *testing.T) {
	ts := newTestAPIServer(t)

	resp, err := http.Get(ts.URL + "/api/status?token=" + testToken)
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status with query token = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// (d) /ws upgrade WITHOUT the subprotocol token -> handshake fails with 401
func TestWSWithoutTokenFails401(t *testing.T) {
	ts := newTestAPIServer(t)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(ts), nil)
	if err == nil {
		conn.Close()
		t.Fatal("dial without token succeeded, want failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dial without token: resp = %v, want status 401", resp)
	}
}

// (e) /ws upgrade WITH Sec-WebSocket-Protocol: claude-remote.<tok> succeeds
// and the server echoes the negotiated subprotocol back.
func TestWSWithSubprotocolTokenSucceeds(t *testing.T) {
	ts := newTestAPIServer(t)

	dialer := &websocket.Dialer{Subprotocols: []string{"claude-remote." + testToken}}
	conn, resp, err := dialer.Dial(wsURL(ts), nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial with subprotocol token failed (status %d): %v", status, err)
	}
	defer conn.Close()

	if got := conn.Subprotocol(); got != "claude-remote."+testToken {
		t.Fatalf("negotiated subprotocol = %q, want %q", got, "claude-remote."+testToken)
	}

	// The server must send the initial state snapshot over the socket.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]interface{}
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("reading initial snapshot: %v", err)
	}
	if msg["type"] != "initial_state" {
		t.Fatalf("first message type = %v, want initial_state", msg["type"])
	}
}

// A wrong subprotocol token must be rejected by the auth gate.
func TestWSWithWrongSubprotocolTokenFails(t *testing.T) {
	ts := newTestAPIServer(t)

	dialer := &websocket.Dialer{Subprotocols: []string{"claude-remote." + strings.Repeat("ee", 32)}}
	conn, resp, err := dialer.Dial(wsURL(ts), nil)
	if err == nil {
		conn.Close()
		t.Fatal("dial with wrong subprotocol token succeeded, want failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dial with wrong token: resp = %v, want status 401", resp)
	}
}

// R4: a disallowed browser Origin is rejected at the WebSocket upgrade.
func TestWSWithDisallowedOriginRejected(t *testing.T) {
	ts := newTestAPIServer(t)

	dialer := &websocket.Dialer{Subprotocols: []string{"claude-remote." + testToken}}
	conn, resp, err := dialer.Dial(wsURL(ts), http.Header{"Origin": {"https://evil.example.com"}})
	if err == nil {
		conn.Close()
		t.Fatal("dial with disallowed origin succeeded, want failure")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("dial with disallowed origin: resp = %v, want status 403", resp)
	}
}

// R4: the Android WebView origin is allowed at the WebSocket upgrade.
func TestWSWithAndroidWebViewOriginAllowed(t *testing.T) {
	ts := newTestAPIServer(t)

	dialer := &websocket.Dialer{Subprotocols: []string{"claude-remote." + testToken}}
	conn, _, err := dialer.Dial(wsURL(ts), http.Header{"Origin": {"https://appassets.androidplatform.net"}})
	if err != nil {
		t.Fatalf("dial with Android WebView origin failed: %v", err)
	}
	conn.Close()
}

// R5: CORS emits the request Origin only when it is allowlisted.
func TestCORSHeaderOnlyForAllowedOrigins(t *testing.T) {
	ts := newTestAPIServer(t)

	get := func(origin string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+"/api/status?token="+testToken, nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/status (origin %q): %v", origin, err)
		}
		return resp
	}

	// Android WebView origin: allowed, echoed back.
	resp := get("https://appassets.androidplatform.net")
	if ac := resp.Header.Get("Access-Control-Allow-Origin"); ac != "https://appassets.androidplatform.net" {
		t.Fatalf("ACAO for WebView origin = %q, want it echoed", ac)
	}
	resp.Body.Close()

	// Unknown origin: response served but NO CORS header emitted.
	resp = get("https://evil.example.com")
	if ac := resp.Header.Get("Access-Control-Allow-Origin"); ac != "" {
		t.Fatalf("ACAO for disallowed origin = %q, want empty", ac)
	}
	resp.Body.Close()
}

// R5: OPTIONS preflight is answered by the CORS layer without a token
// (preflights cannot carry credentials).
func TestCORSPreflightWithoutTokenReturns200(t *testing.T) {
	ts := newTestAPIServer(t)

	req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/status", nil)
	req.Header.Set("Origin", "https://appassets.androidplatform.net")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /api/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preflight status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ac := resp.Header.Get("Access-Control-Allow-Origin"); ac != "https://appassets.androidplatform.net" {
		t.Fatalf("preflight ACAO = %q, want echoed origin", ac)
	}
}

// R2: the static dashboard HTML stays ungated.
func TestStaticDashboardUngated(t *testing.T) {
	ts := newTestAPIServer(t)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// R6/R8 path: POST /api/hook with a valid bearer token is accepted.
func TestHookPostWithBearerTokenReturns200(t *testing.T) {
	ts := newTestAPIServer(t)

	req, _ := http.NewRequest("POST", ts.URL+"/api/hook", strings.NewReader(
		`{"hook_event_name":"SessionStart","session_id":"test-session-1","cwd":"d:\\CODING\\claude-status-apk"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/hook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/hook status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// --- M3 3.5: GET /api/health (token-gated) ---

// healthResponse mirrors the documented /api/health JSON contract.
type healthResponse struct {
	Status      string     `json:"status"`
	Version     string     `json:"version"`
	UptimeS     int64      `json:"uptime_s"`
	LastEventAt *time.Time `json:"last_event_at"`
}

// M3: /api/health without a token -> 401 (same auth gate as the other APIs).
func TestHealthWithoutTokenReturns401(t *testing.T) {
	ts := newTestAPIServer(t)

	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/health without token = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// M3: /api/health with a token -> 200 and the documented fields; last_event_at
// is null before any hook event has been accepted.
func TestHealthWithTokenReturnsFields(t *testing.T) {
	ts := newTestAPIServer(t)

	req, _ := http.NewRequest("GET", ts.URL+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var h healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatalf("decode /api/health body: %v", err)
	}
	if h.Status != "ok" {
		t.Fatalf("status = %q, want %q", h.Status, "ok")
	}
	if h.Version != "1.0.0" {
		t.Fatalf("version = %q, want %q", h.Version, "1.0.0")
	}
	if h.UptimeS < 0 {
		t.Fatalf("uptime_s = %d, want >= 0", h.UptimeS)
	}
	if h.LastEventAt != nil {
		t.Fatalf("last_event_at before any event = %v, want null", *h.LastEventAt)
	}
}

// M3: after a hook event is accepted, last_event_at becomes a real timestamp.
func TestHealthLastEventAtAfterHookEvent(t *testing.T) {
	ts := newTestAPIServer(t)

	postEvent(t, ts.URL, `{"hook_event_name":"SessionStart","session_id":"health-1","cwd":"d:\\x"}`)

	req, _ := http.NewRequest("GET", ts.URL+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()

	var h healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatalf("decode /api/health body: %v", err)
	}
	if h.LastEventAt == nil {
		t.Fatal("last_event_at after an accepted event = null, want timestamp")
	}
}

// postEvent sends one hook event with the bearer token and asserts 200.
func postEvent(t *testing.T, baseURL, body string) {
	t.Helper()
	req, _ := http.NewRequest("POST", baseURL+"/api/hook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/hook: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/hook status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// --- M3 3.6: handleHookPost must zero payload.Source ---

// M3 hardening: a token-holding HTTP caller posting source:"replay" must NOT
// be able to impersonate the boot replay channel (which would silently bypass
// the durable event log and heads-up notifications). The handler forces
// Source to "" so every HTTP delivery is treated as a live event.
func TestHookPostZeroesSpoofedSource(t *testing.T) {
	store := state.NewStore(0, nil)
	srv := NewServer(0, store, web.EmbeddedFS, nil, testToken)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	postEvent(t, ts.URL, `{"hook_event_name":"SessionStart","session_id":"spoof-1","cwd":"d:\\x","source":"replay"}`)

	// Live SessionStart events raise a notification; a real source:"replay"
	// would not. Zeroing the field must make this delivery behave as live.
	snap := store.GetSnapshot()
	if len(snap.Notifications) != 1 {
		t.Fatalf("notifications after spoofed source:\"replay\" = %d, want 1 (must be treated as a live event)", len(snap.Notifications))
	}
	if store.LastEventAt().IsZero() {
		t.Fatal("LastEventAt not updated for an HTTP-delivered event")
	}
}

// --- M3 3.3: Start/Shutdown round-trip ---

// M3: Shutdown drains the underlying http.Server; Start then returns
// http.ErrServerClosed instead of an arbitrary error.
func TestStartShutdownRoundTrip(t *testing.T) {
	srv := NewServer(0, state.NewStore(0, nil), web.EmbeddedFS, nil, testToken)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Let the listener bind (port 0 picks an ephemeral port we cannot learn).
	time.Sleep(250 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Start returned %v, want http.ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Shutdown")
	}
}

// M3: Shutdown is a no-op (nil error) when the server was never started.
func TestShutdownWithoutStartIsNoop(t *testing.T) {
	srv := NewServer(0, state.NewStore(0, nil), web.EmbeddedFS, nil, testToken)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown before Start = %v, want nil", err)
	}
}

// --- M4a: /ws keepalive (server pings + read-deadline reaping) ---

// newKeepaliveTestServer builds a real server whose ping interval is shrunk
// for a fast test run (production default is 20s — far too slow for CI).
func newKeepaliveTestServer(t *testing.T, pingInterval time.Duration) *httptest.Server {
	t.Helper()
	srv := NewServer(0, state.NewStore(0, nil), web.EmbeddedFS, nil, testToken)
	srv.pingInterval = pingInterval
	ts := httptest.NewServer(srv.mux)
	t.Cleanup(ts.Close)
	return ts
}

// dialKeepaliveClient dials /ws with a valid subprotocol token and keeps a
// reader goroutine alive so incoming control frames are actually processed
// (gorilla only dispatches ping handlers from a read loop). The installed
// ping handler records pings and deliberately never answers them, which is
// exactly the "client that stopped responding" case the keepalive exists for.
// The returned channel receives the reader's terminal error (buffered, so the
// goroutine never leaks) — the reader is the connection's ONLY reader because
// gorilla/websocket permits a single concurrent ReadMessage caller.
func dialKeepaliveClient(t *testing.T, ts *httptest.Server, onPing func()) (*websocket.Conn, <-chan error) {
	t.Helper()
	dialer := &websocket.Dialer{Subprotocols: []string{"claude-remote." + testToken}}
	conn, _, err := dialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial /ws with subprotocol token: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	conn.SetPingHandler(func(string) error {
		if onPing != nil {
			onPing()
		}
		return nil // swallow the ping: never send a pong
	})
	closed := make(chan error, 1)
	go func() {
		for {
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, _, err := conn.ReadMessage(); err != nil {
				closed <- err
				return
			}
		}
	}()
	return conn, closed
}

// M4a: the server must send WebSocket ping control frames on every /ws
// connection, so half-open connections become detectable.
func TestWSServerPingsClient(t *testing.T) {
	ts := newKeepaliveTestServer(t, 50*time.Millisecond)

	pings := make(chan struct{}, 8)
	dialKeepaliveClient(t, ts, func() {
		select {
		case pings <- struct{}{}:
		default:
		}
	})

	select {
	case <-pings:
		// First ping arrived — keepalive is live.
	case <-time.After(3 * time.Second):
		t.Fatal("no ping control frame received within 3s of connecting")
	}
}

// M4a: a client that stops responding (no pongs, no data — e.g. a phone that
// vanished off the Wi-Fi) must be reaped by the server's read deadline
// instead of pinning the connection at "connected" forever.
func TestWSUnresponsiveClientReapedByReadDeadline(t *testing.T) {
	// 40ms ping interval => 120ms read window (3x the interval).
	ts := newKeepaliveTestServer(t, 40*time.Millisecond)

	// The helper's own reader goroutine is the single reader; its terminal
	// error tells us the server tore the connection down.
	_, closed := dialKeepaliveClient(t, ts, nil)

	select {
	case <-closed:
		// Server tore the connection down after the read deadline expired
		// without a single pong. Success.
	case <-time.After(3 * time.Second):
		t.Fatal("unresponsive connection was not torn down within 3s; read deadline is not enforced")
	}
}

// M4a: a client that DOES answer pings must stay connected across many read
// windows (the pong handler extends the deadline).
func TestWSResponsiveClientSurvivesReadDeadline(t *testing.T) {
	// 30ms ping interval => 90ms read window; survive for >10 windows.
	ts := newKeepaliveTestServer(t, 30*time.Millisecond)

	dialer := &websocket.Dialer{Subprotocols: []string{"claude-remote." + testToken}}
	conn, _, err := dialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial /ws: %v", err)
	}
	defer conn.Close()

	// Default gorilla ping handler answers pongs automatically while reading.
	go func() {
		for {
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Still alive after 1s means at least 11 read windows came and went with
	// the deadline extended by pongs each time.
	time.Sleep(1 * time.Second)
	if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("connection died while responding to pings: %v", err)
	}
}

// --- M4b: periodic stats heartbeat ---

// M4b: every /ws subscriber must periodically receive a lightweight `stats`
// frame (pure liveness + SystemSummary — no session mutation, no heads-up).
// App-level watchdogs need it because OkHttp never surfaces pong control
// frames, so without data traffic a quiet-but-healthy link is indistinguishable
// from a dead one and native clients force-reconnect, dropping live
// notification frames into the dark window.
func TestWSClientReceivesStatsHeartbeat(t *testing.T) {
	srv := NewServer(0, state.NewStore(0, nil), web.EmbeddedFS, nil, testToken)
	srv.statsInterval = 100 * time.Millisecond
	srv.startStatsHeartbeat()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	dialer := &websocket.Dialer{Subprotocols: []string{"claude-remote." + testToken}}
	conn, _, err := dialer.Dial(wsURL(ts), nil)
	if err != nil {
		t.Fatalf("dial /ws: %v", err)
	}
	defer conn.Close()

	// The first frame is initial_state; keep reading until a stats frame
	// lands (at 100ms cadence that is well inside the ~1s budget).
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 16; i++ {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("reading frame %d: %v", i, err)
		}
		if msg["type"] != "stats" {
			continue
		}
		tsStr, ok := msg["timestamp"].(string)
		if !ok || tsStr == "" {
			t.Fatalf("stats frame timestamp = %v, want non-empty string", msg["timestamp"])
		}
		data, ok := msg["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("stats frame data = %T, want object", msg["data"])
		}
		if _, ok := data["total_sessions"]; !ok {
			t.Fatalf("stats frame data missing total_sessions: %v", data)
		}
		return // first stats frame received — heartbeat is live
	}
	t.Fatal("no stats frame received within 2s of connecting; heartbeat is not running")
}

// --- M11: runtime relay configuration (GET/POST /api/relay) ---

// relayStateResponse mirrors the documented /api/relay JSON contract.
type relayStateResponse struct {
	URL       string `json:"url"`
	Active    bool   `json:"active"`
	Connected bool   `json:"connected"`
}

// withRelayFile isolates the persisted relay-URL file to a temp path for one
// test (production default: ~/.claude/claude-remote-relay.url). Returns the
// isolated path.
func withRelayFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-remote-relay.url")
	old := relayURLFilePath
	relayURLFilePath = path
	t.Cleanup(func() { relayURLFilePath = old })
	return path
}

// relayGet fetches /api/relay with the bearer token and decodes the state.
func relayGet(t *testing.T, baseURL string) (relayStateResponse, int) {
	t.Helper()
	req, _ := http.NewRequest("GET", baseURL+"/api/relay", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/relay: %v", err)
	}
	defer resp.Body.Close()
	var st relayStateResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
			t.Fatalf("decode /api/relay body: %v", err)
		}
	}
	return st, resp.StatusCode
}

// relayPost applies a relay URL via POST /api/relay with the bearer token.
func relayPost(t *testing.T, baseURL, body string) (relayStateResponse, int) {
	t.Helper()
	req, _ := http.NewRequest("POST", baseURL+"/api/relay", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/relay: %v", err)
	}
	defer resp.Body.Close()
	var st relayStateResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
			t.Fatalf("decode POST /api/relay body: %v", err)
		}
	}
	return st, resp.StatusCode
}

// M11: GET /api/relay without a token -> 401 (same auth gate as every API).
func TestRelayGetWithoutTokenReturns401(t *testing.T) {
	ts := newTestAPIServer(t)

	resp, err := http.Get(ts.URL + "/api/relay")
	if err != nil {
		t.Fatalf("GET /api/relay: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/relay without token = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// M11: POST /api/relay without a token -> 401.
func TestRelayPostWithoutTokenReturns401(t *testing.T) {
	ts := newTestAPIServer(t)

	req, _ := http.NewRequest("POST", ts.URL+"/api/relay", strings.NewReader(`{"url":"wss://x.example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/relay: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /api/relay without token = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// M11: with no relay configured (no flag/env/file), GET /api/relay reports
// the documented disabled state.
func TestRelayGetDisabledByDefault(t *testing.T) {
	ts := newTestAPIServer(t)

	st, code := relayGet(t, ts.URL)
	if code != http.StatusOK {
		t.Fatalf("GET /api/relay status = %d, want %d", code, http.StatusOK)
	}
	if st.URL != "" || st.Active || st.Connected {
		t.Fatalf("default relay state = %+v, want {url:\"\" active:false connected:false}", st)
	}
}

// M11: non-wss/https schemes (and hostless URLs) are rejected with 400 and
// leave the relay state untouched.
func TestRelayPostRejectsInvalidURLs(t *testing.T) {
	ts := newTestAPIServer(t)

	for _, bad := range []string{
		"ws://127.0.0.1:9280",      // plaintext ws is not allowed off-LAN
		"http://relay.example.com", // plaintext http is not allowed either
		"ftp://relay.example.com",  // not a relay scheme at all
		"wss://",                   // scheme without a host
	} {
		st, code := relayPost(t, ts.URL, `{"url":"`+bad+`"}`)
		if code != http.StatusBadRequest {
			t.Fatalf("POST /api/relay url=%q = %d, want %d", bad, code, http.StatusBadRequest)
		}
		if st != (relayStateResponse{}) {
			t.Fatalf("400 response body for %q = %+v, want empty state", bad, st)
		}
	}

	// State is unchanged after the rejections.
	st, code := relayGet(t, ts.URL)
	if code != http.StatusOK || st.Active {
		t.Fatalf("state after rejected posts = %+v (code %d), want still disabled", st, code)
	}
}

// M11: a malformed JSON body is a 400, not a 500.
func TestRelayPostInvalidJSONReturns400(t *testing.T) {
	ts := newTestAPIServer(t)

	req, _ := http.NewRequest("POST", ts.URL+"/api/relay", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/relay: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /api/relay invalid JSON = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// M11: methods other than GET/POST are answered with 405.
func TestRelayMethodNotAllowed(t *testing.T) {
	ts := newTestAPIServer(t)

	req, _ := http.NewRequest("PUT", ts.URL+"/api/relay", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/relay: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /api/relay = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

// M11: the enable → replace → disable drill against a server whose relay was
// never running (apply must not panic). Uses a loopback URL the client cannot
// reach, so `connected` stays false; the point is the state machine, the
// persisted file, and clean client swapping.
func TestRelayPostApplyReplaceDisableRoundTrip(t *testing.T) {
	ts := newTestAPIServer(t)
	file := withRelayFile(t)

	// Enable: applied immediately and persisted.
	st, code := relayPost(t, ts.URL, `{"url":"wss://127.0.0.1:1"}`)
	if code != http.StatusOK {
		t.Fatalf("POST enable = %d, want %d", code, http.StatusOK)
	}
	if st.URL != "wss://127.0.0.1:1" || !st.Active || st.Connected {
		t.Fatalf("state after enable = %+v, want url set + active + not connected", st)
	}
	if got := LoadPersistedRelayURL(); got != "wss://127.0.0.1:1" {
		t.Fatalf("persisted relay URL = %q, want the applied URL", got)
	}

	// GET agrees with the just-applied state.
	st, code = relayGet(t, ts.URL)
	if code != http.StatusOK || st.URL != "wss://127.0.0.1:1" || !st.Active {
		t.Fatalf("GET after enable = %+v (code %d), want the applied URL + active", st, code)
	}

	// Replace: a second URL swaps the client without error. (Also loopback:
	// unit tests must not dial the real internet.)
	st, code = relayPost(t, ts.URL, `{"url":"https://127.0.0.1:2"}`)
	if code != http.StatusOK {
		t.Fatalf("POST replace = %d, want %d", code, http.StatusOK)
	}
	if st.URL != "https://127.0.0.1:2" || !st.Active {
		t.Fatalf("state after replace = %+v, want the new URL + active", st)
	}

	// Disable: empty URL stops the client and clears the persisted setting.
	st, code = relayPost(t, ts.URL, `{"url":""}`)
	if code != http.StatusOK {
		t.Fatalf("POST disable = %d, want %d", code, http.StatusOK)
	}
	if st.URL != "" || st.Active || st.Connected {
		t.Fatalf("state after disable = %+v, want fully disabled", st)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("relay URL file after disable: stat err = %v, want removed", err)
	}
}

// M11: startup precedence — flag beats env beats persisted file.
func TestResolveRelayURLPrecedence(t *testing.T) {
	cases := []struct {
		flagVal, envVal, fileVal, want string
	}{
		{"wss://flag.example.com", "wss://env.example.com", "wss://file.example.com", "wss://flag.example.com"},
		{"", "wss://env.example.com", "wss://file.example.com", "wss://env.example.com"},
		{"", "", "wss://file.example.com", "wss://file.example.com"},
		{"", "", "", ""},
		{"   ", "\twss://env.example.com\n", "  ", "wss://env.example.com"}, // whitespace-only loses
	}
	for _, tc := range cases {
		if got := ResolveRelayURL(tc.flagVal, tc.envVal, tc.fileVal); got != tc.want {
			t.Fatalf("ResolveRelayURL(%q, %q, %q) = %q, want %q", tc.flagVal, tc.envVal, tc.fileVal, got, tc.want)
		}
	}
}

// M11: persistence round trip — a saved URL loads back verbatim, and an empty
// save removes the file entirely (absent = disabled).
func TestPersistAndLoadRelayURLRoundTrip(t *testing.T) {
	path := withRelayFile(t)

	if err := persistRelayURL("wss://relay.example.com"); err != nil {
		t.Fatalf("persistRelayURL: %v", err)
	}
	if got := LoadPersistedRelayURL(); got != "wss://relay.example.com" {
		t.Fatalf("LoadPersistedRelayURL = %q, want the persisted URL", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persisted relay URL file missing: %v", err)
	}

	if err := persistRelayURL(""); err != nil {
		t.Fatalf("persistRelayURL(disable): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("relay URL file after disable: stat err = %v, want removed", err)
	}
	if got := LoadPersistedRelayURL(); got != "" {
		t.Fatalf("LoadPersistedRelayURL after disable = %q, want empty", got)
	}
}
