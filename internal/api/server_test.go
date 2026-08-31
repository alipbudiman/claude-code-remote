package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
