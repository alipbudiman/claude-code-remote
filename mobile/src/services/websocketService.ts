import { ServerStateSnapshot, WebSocketMessage } from '../types';

type MessageHandler = (msg: WebSocketMessage) => void;
type ConnectionHandler = (connected: boolean) => void;

class WebSocketService {
  private ws: WebSocket | null = null;
  private serverUrl: string = '';
  private token: string = '';
  private messageHandlers: Set<MessageHandler> = new Set();
  private connectionHandlers: Set<ConnectionHandler> = new Set();
  private reconnectTimer: number | null = null;
  private isConnecting: boolean = false;

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
      } else {
        this.serverUrl = 'http://192.168.100.48:9280';
      }
    }

    // Load the saved auth token (server requires it on /ws and /api/*)
    try {
      this.token = localStorage.getItem('claude_server_token') || '';
    } catch {
      this.token = '';
    }
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

  public connect() {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return;
    }

    if (this.isConnecting) return;
    this.isConnecting = true;

    try {
      const httpUrl = new URL(this.serverUrl);
      const wsProtocol = httpUrl.protocol === 'https:' ? 'wss:' : 'ws:';
      const wsUrl = `${wsProtocol}//${httpUrl.host}/ws`;

      console.log(`[WebSocket] Connecting to ${wsUrl}...`);
      // Browser JS cannot set headers on a WS handshake, so the token rides
      // the Sec-WebSocket-Protocol subprotocol instead.
      this.ws = new WebSocket(wsUrl, this.token ? ['claude-remote.' + this.token] : undefined);

      this.ws.onopen = () => {
        console.log('[WebSocket] Connected successfully');
        this.isConnecting = false;
        this.notifyConnection(true);
        if (this.reconnectTimer) {
          clearTimeout(this.reconnectTimer);
          this.reconnectTimer = null;
        }
      };

      this.ws.onmessage = (event) => {
        try {
          const msg: WebSocketMessage = JSON.parse(event.data);
          this.notifyMessage(msg);
        } catch (e) {
          console.error('[WebSocket] Failed to parse message', e);
        }
      };

      this.ws.onclose = () => {
        console.log('[WebSocket] Disconnected. Scheduling reconnect...');
        this.isConnecting = false;
        this.notifyConnection(false);
        this.scheduleReconnect();
      };

      this.ws.onerror = (err) => {
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
