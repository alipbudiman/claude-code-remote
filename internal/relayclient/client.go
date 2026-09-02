// Package relayclient connects the desktop server to the remote relay hub
// (M5.1) by dialing OUT: the desktop keeps no inbound ports open and works
// behind CGNAT. Once connected, the client forwards every store broadcast to
// the relay (which fans frames out to the room's other members — the phone)
// and answers every peer_joined with a fresh initial_state snapshot, so a
// phone joining at any point immediately receives the full state.
package relayclient

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"claude-remote-server/internal/models"
	"claude-remote-server/internal/state"
)

const (
	// Reconnect backoff: 1s after a first failure, doubling per failed
	// attempt up to 60s, plus jitter; reset whenever a dial succeeds.
	minBackoff = 1 * time.Second
	maxBackoff = 60 * time.Second

	// writeTimeout bounds every frame write to the relay so a wedged link
	// cannot block the pump forever.
	writeTimeout = 10 * time.Second

	// handshakeTimeout bounds each dial attempt.
	handshakeTimeout = 10 * time.Second

	// defaultReadWindow is how long the client may hear NOTHING from the
	// relay before treating the link as dead. It mirrors the relay's default
	// keepalive contract (RELAY_PING_INTERVAL = 30s): every relay PING
	// refreshes this window (see serve's PingHandler), so a healthy link
	// never expires it, while a dead or wedged relay is noticed within one
	// window instead of pinning a half-open TCP connection.
	//
	// CONSTRAINT: the relay's RELAY_PING_INTERVAL must stay well under this
	// window (default margin 3x, matching the relay's own reap rule). If the
	// relay is redeployed with a longer interval, readWindow must grow with
	// it or every quiet link will cycle through reconnects.
	defaultReadWindow = 90 * time.Second

	// subprotocolPrefix mirrors internal/api: the token rides the
	// Sec-WebSocket-Protocol handshake as "claude-remote.<token>". Unlike a
	// browser, plain Go sets handshake headers directly — and the client
	// also sends ?token=, so both extraction paths on the relay work.
	subprotocolPrefix = "claude-remote."
)

// Client is the desktop-side dial-out relay connection. Construct with
// NewClient, Start it once, and Stop it on shutdown; it reconnects on its own
// for the process's lifetime.
type Client struct {
	relayURL string
	token    string
	store    *state.Store

	stopOnce sync.Once
	stopCh   chan struct{}
	cancel   context.CancelFunc

	// connected is set while a relay connection is open (M11): stored true
	// right after a dial succeeds, false again when the link drops or the
	// client stops. Exposed via Connected() for /api/relay's status line.
	connected atomic.Bool

	// ignoredMu guards ignored (log-once bookkeeping for inbound frames).
	ignoredMu sync.Mutex
	ignored   map[string]bool

	// readWindow is how long the client waits for relay traffic (pings
	// included) before declaring the link dead. Defaults to
	// defaultReadWindow; tests shrink it to make keepalive failures fast.
	readWindow time.Duration

	// OnClientFrame, when set, receives every phone-originated
	// client_command frame (raw bytes, relayed verbatim). The api package
	// registers the command dispatcher here — in relay mode this is the
	// only path commands from the phone can take.
	OnClientFrame func([]byte)
}

// NewClient builds a relay client for one relay address and token. relayURL
// accepts wss:// or https:// (mapped to wss) and ws:// or http:// (mapped to
// ws); token is the same 64-hex shared secret the local API uses.
func NewClient(relayURL, token string, store *state.Store) *Client {
	return &Client{
		relayURL:   relayURL,
		token:      token,
		store:      store,
		stopCh:     make(chan struct{}),
		ignored:    make(map[string]bool),
		readWindow: defaultReadWindow,
	}
}

// Start launches the connect/pump loop in the background. The ctx (usually
// context.Background() from main) bounds the client's lifetime together with
// Stop. Call once, before Stop.
func (c *Client) Start(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	go c.run(ctx)
}

// Stop tears the client down: it closes the current connection (if any) and
// unsubscribes from the store. Safe to call multiple times.
func (c *Client) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
		if c.cancel != nil {
			c.cancel()
		}
		c.connected.Store(false)
	})
}

// Connected reports whether the client currently has an open relay
// connection: set the moment a dial succeeds, cleared when the link drops,
// the client stops, or before the first dial. Callers that need to swap
// clients at runtime (POST /api/relay, M11) use it to distinguish
// "connecting" from "connected".
func (c *Client) Connected() bool {
	return c.connected.Load()
}

// run owns the reconnect loop and the store subscription for the client's
// whole lifetime.
func (c *Client) run(ctx context.Context) {
	sub := c.store.Subscribe()
	defer c.store.Unsubscribe(sub)

	backoff := minBackoff
	for {
		select {
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		conn, err := c.dial(ctx)
		if err != nil {
			wait := backoff + jitter(backoff)
			log.Printf("relayclient: relay %s unreachable: %v (retrying in %s)", c.relayURL, err, wait.Round(time.Millisecond))
			if !c.sleep(ctx, wait) {
				return
			}
			backoff = min(2*backoff, maxBackoff)
			continue
		}
		backoff = minBackoff // reset on open
		c.connected.Store(true)

		c.serve(ctx, conn, sub)
		c.connected.Store(false)
		_ = conn.Close()

		// Small pause before redialing so a relay that accepts and
		// immediately drops cannot turn into a hot connect loop.
		if !c.sleep(ctx, backoff+jitter(backoff)) {
			return
		}
	}
}

// wsURL derives the dial URL: scheme mapped to ws(s), path /ws appended when
// the configured address carries none, and the token attached as ?token=.
func (c *Client) wsURL() string {
	u, err := url.Parse(c.relayURL)
	if err != nil {
		return c.relayURL // let the dialer report the unusable address
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws"
	}
	q := u.Query()
	q.Set("token", c.token)
	u.RawQuery = q.Encode()
	return u.String()
}

// dial connects to the relay with BOTH ?token= and the
// "Sec-WebSocket-Protocol: claude-remote.<tok>" handshake header.
func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	header := http.Header{}
	header.Set("Sec-WebSocket-Protocol", subprotocolPrefix+c.token)
	dialer := &websocket.Dialer{HandshakeTimeout: handshakeTimeout}
	conn, _, err := dialer.DialContext(ctx, c.wsURL(), header)
	return conn, err
}

// serve runs one live relay connection until it drops or the client stops.
func (c *Client) serve(ctx context.Context, conn *websocket.Conn, sub <-chan models.WebSocketMessage) {
	var writeMu sync.Mutex
	writeJSON := func(v interface{}) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := conn.WriteJSON(v); err != nil {
			_ = conn.Close() // release the reader; serve returns to reconnect
			return false
		}
		return true
	}
	pushSnapshot := func() bool {
		return writeJSON(models.WebSocketMessage{
			Type:      "initial_state",
			Data:      c.store.GetSnapshot(),
			Timestamp: time.Now(),
		})
	}

	// peerJoined coalesces announcements: one snapshot push answers a whole
	// burst of joins.
	peerJoined := make(chan struct{}, 1)
	readerDone := make(chan struct{})

	go func() {
		defer close(readerDone)
		extend := func() error { return conn.SetReadDeadline(time.Now().Add(c.readWindow)) }
		if err := extend(); err != nil {
			return
		}
		conn.SetPongHandler(func(string) error { return extend() })

		// The relay's PING is this side's keepalive. gorilla consumes ping
		// control frames inside ReadMessage WITHOUT touching the absolute
		// read deadline — and relay→client DATA frames are rare (only
		// peer_joined), so without this handler even a busy system's link
		// is one-directional and every connection died at exactly readWindow
		// (a permanent ~readWindow reconnect churn; see M4b's stats
		// heartbeat for the mirror-image lesson). Replacing the default
		// ping handler also suppresses gorilla's automatic pong, so answer
		// manually — serialized with the pump through writeMu (the M4a
		// single-writer rule; WriteControl shares the mutex for the same
		// reason the relay's own ping ticker does).
		conn.SetPingHandler(func(appData string) error {
			if err := extend(); err != nil {
				return err
			}
			writeMu.Lock()
			defer writeMu.Unlock()
			return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(writeTimeout))
		})
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := extend(); err != nil {
				return
			}
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &head) != nil || head.Type == "" {
				continue // inbound frames are a future control channel
			}
			switch head.Type {
			case "peer_joined":
				select {
				case peerJoined <- struct{}{}:
				default:
				}
			case "client_command":
				// Phone-originated command frame relayed verbatim. Hand the
				// RAW bytes to the registered handler (the api package
				// parses + dispatches) synchronously so command ORDER is
				// preserved; handlers are fast and never block on the
				// network, so the keepalive window is safe.
				if c.OnClientFrame != nil {
					c.OnClientFrame(data)
				}
			default:
				c.logIgnored(head.Type)
			}
		}
	}()

	// Drop broadcasts that stacked up while the link was down, THEN
	// snapshot: everything those frames did to the store is already inside
	// GetSnapshot(), so replaying them would only regress the phone's view.
	drainBuffered(sub)

	log.Printf("relayclient: connected to relay %s", c.relayURL)
	if !pushSnapshot() {
		return
	}

	for {
		select {
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		case <-peerJoined:
			if !pushSnapshot() {
				return
			}
		case msg, ok := <-sub:
			if !ok {
				return
			}
			if !writeJSON(msg) {
				return
			}
		case <-readerDone:
			return
		}
	}
}

// drainBuffered discards whatever broadcasts are stacked up in the
// subscription channel right now (used on reconnect — see serve).
func drainBuffered(sub <-chan models.WebSocketMessage) {
	for {
		select {
		case <-sub:
		default:
			return
		}
	}
}

// logIgnored records one "ignoring inbound frame type X" line per type; the
// inbound channel is a future control plane, and a chatty log helps nobody.
func (c *Client) logIgnored(frameType string) {
	c.ignoredMu.Lock()
	already := c.ignored[frameType]
	c.ignored[frameType] = true
	c.ignoredMu.Unlock()
	if !already {
		log.Printf("relayclient: ignoring inbound frame type %q", frameType)
	}
}

// jitter returns a random duration in [0, d/2] to spread reconnect storms.
func jitter(d time.Duration) time.Duration {
	return time.Duration(rand.Int63n(int64(d/2) + 1))
}

// sleep waits for d, returning false as soon as the client is stopping.
func (c *Client) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-c.stopCh:
		return false
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
