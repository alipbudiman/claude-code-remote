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

	// readWindow mirrors the relay's default keepalive contract (ping every
	// 30s, reap after three silent intervals): any relay traffic — pings,
	// pongs, frames — keeps the deadline extended, so a dead relay is
	// noticed within ~90s instead of pinning a half-open TCP connection.
	readWindow = 90 * time.Second

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

	// ignoredMu guards ignored (log-once bookkeeping for inbound frames).
	ignoredMu sync.Mutex
	ignored   map[string]bool
}

// NewClient builds a relay client for one relay address and token. relayURL
// accepts wss:// or https:// (mapped to wss) and ws:// or http:// (mapped to
// ws); token is the same 64-hex shared secret the local API uses.
func NewClient(relayURL, token string, store *state.Store) *Client {
	return &Client{
		relayURL: relayURL,
		token:    token,
		store:    store,
		stopCh:   make(chan struct{}),
		ignored:  make(map[string]bool),
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
	})
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

		c.serve(ctx, conn, sub)
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
		extend := func() error { return conn.SetReadDeadline(time.Now().Add(readWindow)) }
		if err := extend(); err != nil {
			return
		}
		conn.SetPongHandler(func(string) error { return extend() })
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
