import { ServerStateSnapshot, WebSocketMessage } from '../types';

type MessageHandler = (msg: WebSocketMessage) => void;
type ConnectionHandler = (connected: boolean) => void;

class WebSocketService {
  private ws: WebSocket | null = null;
  private serverUrl: string = '';
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
  }

  public getServerUrl(): string {
    return this.serverUrl;
  }

  public setServerUrl(url: string) {
    url = url.trim().replace(/\/$/, '');
    if (!url.startsWith('http://') && !url.startsWith('https://')) {
      url = 'http://' + url;
    }
    this.serverUrl = url;
    try {
      localStorage.setItem('claude_server_url', url);
    } catch {
      // Storage safe
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
      this.ws = new WebSocket(wsUrl);

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
      const res = await fetch(`${this.serverUrl}/api/status`);
      if (!res.ok) throw new Error('Status request failed');
      return await res.json();
    } catch (e) {
      console.error('Failed to fetch REST status', e);
      return null;
    }
  }
}

export const wsService = new WebSocketService();
