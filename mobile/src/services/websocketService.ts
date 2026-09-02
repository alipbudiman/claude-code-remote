import { DecisionRespondInput, ServerStateSnapshot, WebSocketMessage } from '../types';

type MessageHandler = (msg: WebSocketMessage) => void;
type ConnectionHandler = (connected: boolean) => void;

/** M4b: consecutive failed dials on one base URL before trying the next host. */
const FAILOVER_AFTER_FAILURES = 5;

class WebSocketService {
  private ws: WebSocket | null = null;
  private serverUrl: string = '';
  private token: string = '';
  private messageHandlers: Set<MessageHandler> = new Set();
  private connectionHandlers: Set<ConnectionHandler> = new Set();
  private reconnectTimer: number | null = null;
  private isConnecting: boolean = false;

  // --- M4b: reconnect-on-wake + host_ips auto-failover state ---
  /** Alternate server hosts (the snapshot's host_ips), probed round-robin. */
  private failoverCandidates: string[] = [];
  /** Consecutive failed dials against the current base URL. */
  private consecutiveFailures = 0;
  /** Round-robin cursor into failoverCandidates; -1 means "not probing yet". */
  private candidateIndex = -1;
  /** Candidate base URL currently being tried; null = use the saved URL. */
  private activeCandidateUrl: string | null = null;

  constructor() {
    let savedUrl: string | null = null;
    try {
      savedUrl = localStorage.getItem('claude_server_url');
    } catch {
      // Storage access exception safe
    }

    if (savedUrl) {
      this.serverUrl = savedUrl;
    } else {
      const origin = window.location.origin;
      if (
        origin &&
        !origin.includes('localhost:3000') &&
        !origin.includes('127.0.0.1:3000') &&
        !origin.includes('appassets.androidplatform.net') &&
        !origin.includes('file://') &&
        (origin.startsWith('http://') || origin.startsWith('https://'))
      ) {
        this.serverUrl = origin;
      }
      // M4b: no hardcoded fallback anymore. With no saved URL and no usable
      // origin, serverUrl stays '' — App opens the ConnectionModal on first
      // launch instead of dialing an address that is almost never right.
    }

    // Load the saved auth token (server requires it on /ws and /api/*)
    try {
      this.token = localStorage.getItem('claude_server_token') || '';
    } catch {
      this.token = '';
    }

    // M4b: mobile browsers park background timers/sockets aggressively, so
    // force an immediate dial when the page becomes visible or the network
    // returns — never wait out the 2.5s reconnect timer on a wake-up.
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') this.ensureConnected();
    });
    window.addEventListener('online', () => this.ensureConnected());
  }

  public getServerUrl(): string {
    return this.serverUrl;
  }

  public getToken(): string {
    return this.token;
  }

  public setToken(token: string) {
    const next = token.trim();
    if (!next) {
      // Empty means "nothing entered" (e.g. the token field was left blank
      // while pasting a token-bearing URL). Never destroy a working stored
      // token; just reconnect with the current values.
      this.reconnect();
      return;
    }
    this.token = next;
    try {
      localStorage.setItem('claude_server_token', next);
    } catch {
      // Storage safe
    }
    this.reconnect();
  }

  /** M4b: forget the stored token entirely and reconnect without one. */
  public clearToken() {
    this.token = '';
    try {
      localStorage.removeItem('claude_server_token');
    } catch {
      // Storage safe
    }
    this.reconnect();
  }

  /**
   * M4b: alternate server hosts (the snapshot's host_ips). After
   * FAILOVER_AFTER_FAILURES consecutive failed dials these are probed
   * round-robin (same port as the saved URL, default 9280); the first one
   * that connects is promoted to the saved URL.
   */
  public setFailoverCandidates(hosts: string[]) {
    this.failoverCandidates = hosts.filter((h) => !!h);
    if (this.candidateIndex >= this.failoverCandidates.length) {
      this.candidateIndex = -1;
      this.activeCandidateUrl = null;
    }
  }

  public setServerUrl(url: string) {
    url = url.trim();
    if (!url.startsWith('http://') && !url.startsWith('https://')) {
      url = 'http://' + url;
    }

    // The server's canonical/QR URL ends with /?token=<64hex>. Extract an
    // inline token (seeded via setToken below) and keep only the
    // scheme/host/port/path so later URL building stays correct.
    let inlineToken = '';
    try {
      const parsed = new URL(url);
      inlineToken = (parsed.searchParams.get('token') || '').trim();
      url = parsed.origin + parsed.pathname;
    } catch {
      // Unparseable input: strip query/hash manually.
      url = url.split('#')[0].split('?')[0];
    }
    url = url.replace(/\/+$/, '');

    this.serverUrl = url;
    // Manual re-pointing resets any in-flight failover rotation.
    this.activeCandidateUrl = null;
    this.candidateIndex = -1;
    this.consecutiveFailures = 0;
    try {
      localStorage.setItem('claude_server_url', url);
    } catch {
      // Storage safe
    }

    if (inlineToken) {
      // Seed the token through setToken(): persists it to localStorage and
      // reconnects once with both the cleaned URL and the token applied.
      this.setToken(inlineToken);
      return;
    }
    this.reconnect();
  }

  /**
   * M4b: immediate (re)dial when the page becomes visible / the network
   * returns and the socket is not already open. Safe to call at any time.
   */
  private ensureConnected() {
    if (!this.serverUrl) return; // nothing configured yet
    if (this.ws && this.ws.readyState === WebSocket.OPEN) return;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.isConnecting = false;
    this.connect();
  }

  public connect() {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return;
    }

    if (this.isConnecting) return;

    // No base URL configured (first launch before the user saves one).
    const base = this.activeCandidateUrl || this.serverUrl;
    if (!base) return;
    this.isConnecting = true;

    try {
      const httpUrl = new URL(base);
      const wsProtocol = httpUrl.protocol === 'https:' ? 'wss:' : 'ws:';
      const wsUrl = `${wsProtocol}//${httpUrl.host}/ws`;

      console.log(`[WebSocket] Connecting to ${wsUrl}...`);
      // Browser JS cannot set headers on a WS handshake, so the token rides
      // the Sec-WebSocket-Protocol subprotocol instead.
      this.ws = new WebSocket(wsUrl, this.token ? ['claude-remote.' + this.token] : undefined);
      // Handlers are bound to THIS socket: a superseded one (reconnect() or
      // failover dialed a replacement) must not corrupt the new attempt's
      // state — notably its onclose must not count as a connect failure.
      const sock = this.ws;

      sock.onopen = () => {
        if (this.ws !== sock) return;
        console.log('[WebSocket] Connected successfully');
        this.isConnecting = false;
        if (this.activeCandidateUrl) {
          // Failover candidate answered: promote it to THE saved URL.
          const promoted = this.activeCandidateUrl;
          this.serverUrl = promoted;
          this.activeCandidateUrl = null;
          this.candidateIndex = -1;
          try {
            localStorage.setItem('claude_server_url', promoted);
          } catch {
            // Storage safe
          }
          console.log(`[WebSocket] Failover connected; saved ${promoted}`);
        }
        this.consecutiveFailures = 0;
        this.notifyConnection(true);
        if (this.reconnectTimer) {
          clearTimeout(this.reconnectTimer);
          this.reconnectTimer = null;
        }
      };

      sock.onmessage = (event) => {
        if (this.ws !== sock) return;
        try {
          const msg: WebSocketMessage = JSON.parse(event.data);
          this.notifyMessage(msg);
        } catch (e) {
          console.error('[WebSocket] Failed to parse message', e);
        }
      };

      sock.onclose = () => {
        if (this.ws !== sock) return; // superseded: not a failure of the current attempt
        console.log('[WebSocket] Disconnected. Scheduling reconnect...');
        this.isConnecting = false;
        this.notifyConnection(false);
        this.consecutiveFailures++;
        if (this.consecutiveFailures >= FAILOVER_AFTER_FAILURES) {
          this.rotateFailoverCandidate();
        }
        this.scheduleReconnect();
      };

      sock.onerror = (err) => {
        if (this.ws !== sock) return;
        console.warn('[WebSocket] Error encountered', err);
        this.isConnecting = false;
        this.notifyConnection(false);
      };
    } catch (e) {
      console.error('[WebSocket] Connection initialization failed', e);
      this.isConnecting = false;
      this.notifyConnection(false);
      this.scheduleReconnect();
    }
  }

  /**
   * M4b: after FAILOVER_AFTER_FAILURES consecutive failures on the current
   * base URL, move to the next known host. Simple round-robin, no mDNS.
   */
  private rotateFailoverCandidate() {
    const candidates = this.failoverUrls();
    if (candidates.length === 0) return; // nowhere to fail over to
    this.candidateIndex = (this.candidateIndex + 1) % candidates.length;
    this.activeCandidateUrl = candidates[this.candidateIndex] || null;
    this.consecutiveFailures = 0;
    if (this.activeCandidateUrl) {
      console.warn(
        `[WebSocket] ${FAILOVER_AFTER_FAILURES} failed attempts — trying ${this.activeCandidateUrl}`
      );
    }
  }

  /** Builds http://<ip>:<port> candidates from host_ips + the saved URL's port. */
  private failoverUrls(): string[] {
    if (this.failoverCandidates.length === 0 || !this.serverUrl) return [];
    let port = '9280';
    try {
      const parsed = new URL(this.serverUrl);
      if (parsed.port) port = parsed.port;
    } catch {
      // Saved URL unparseable: default port.
    }
    return this.failoverCandidates.map((ip) => `http://${ip}:${port}`);
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, 2500);
  }

  public reconnect() {
    // Clear the connecting flag: we are deliberately aborting any in-flight
    // attempt so back-to-back setServerUrl()/setToken() calls both dial.
    this.isConnecting = false;
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.connect();
  }

  public onMessage(handler: MessageHandler) {
    this.messageHandlers.add(handler);
    return () => this.messageHandlers.delete(handler);
  }

  public onConnectionChange(handler: ConnectionHandler) {
    this.connectionHandlers.add(handler);
    return () => this.connectionHandlers.delete(handler);
  }

  // --- Client→server commands (2026-09-02) ---------------------------------
  // The socket has been receive-only until now; these are the first sends.
  // Commands ride /ws in BOTH LAN and relay mode — the relay forwards
  // frames verbatim in both directions, while HTTP only reaches the
  // desktop on LAN.

  /** Send a raw frame if the socket is open. Returns false when closed. */
  public send(frame: object): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    this.ws.send(JSON.stringify(frame));
    return true;
  }

  /** Send a client_command frame ({type, data:{op, ...extra}}). */
  public sendCommand(op: string, extra: Record<string, unknown> = {}): boolean {
    return this.send({ type: 'client_command', data: { op, ...extra }, timestamp: new Date().toISOString() });
  }

  /** Answer a pending decision (allow / deny / always_allow / answer / dismiss). */
  public sendDecision(input: DecisionRespondInput): boolean {
    return this.sendCommand('decision', input as unknown as Record<string, unknown>);
  }

  /** Queue a prompt for a session (delivered at the current turn's end). */
  public sendPrompt(sessionId: string, text: string): boolean {
    return this.sendCommand('prompt', { session_id: sessionId, text });
  }

  private notifyMessage(msg: WebSocketMessage) {
    this.messageHandlers.forEach((h) => h(msg));
  }

  private notifyConnection(connected: boolean) {
    this.connectionHandlers.forEach((h) => h(connected));
  }

  public async fetchStatus(): Promise<ServerStateSnapshot | null> {
    try {
      const url = this.token
        ? `${this.serverUrl}/api/status?token=${encodeURIComponent(this.token)}`
        : `${this.serverUrl}/api/status`;
      const res = await fetch(url);
      if (!res.ok) throw new Error('Status request failed');
      return await res.json();
    } catch (e) {
      console.error('Failed to fetch REST status', e);
      return null;
    }
  }
}

export const wsService = new WebSocketService();
