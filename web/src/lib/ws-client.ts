export type WSRecord = Record<string, unknown>;

export class WSClient {
  private ws: WebSocket | null = null;
  private url: string;
  private onRecord: (record: WSRecord) => void;
  private onStatus: (connected: boolean) => void;
  private onReconnect: () => void;
  private reconnectDelay = 1000;
  private maxReconnectDelay = 30000;
  private wasConnected = false;
  private closed = false;

  constructor(
    url: string,
    onRecord: (record: WSRecord) => void,
    onStatus: (connected: boolean) => void,
    onReconnect: () => void,
  ) {
    this.url = url;
    this.onRecord = onRecord;
    this.onStatus = onStatus;
    this.onReconnect = onReconnect;
  }

  connect() {
    if (this.closed) return;

    const clientId = crypto.randomUUID();
    const ws = new WebSocket(`${this.url}?client_id=${clientId}`);
    this.ws = ws;

    ws.onopen = () => {
      this.reconnectDelay = 1000;
      this.onStatus(true);
      if (this.wasConnected) {
        this.onReconnect();
      }
      this.wasConnected = true;
    };

    ws.onmessage = (event) => {
      try {
        const record = JSON.parse(event.data as string);
        this.onRecord(record);
      } catch {
        // ignore parse errors
      }
    };

    ws.onclose = () => {
      this.onStatus(false);
      if (!this.closed) {
        setTimeout(() => this.connect(), this.reconnectDelay);
        this.reconnectDelay = Math.min(
          this.reconnectDelay * 2,
          this.maxReconnectDelay,
        );
      }
    };

    ws.onerror = () => {
      ws.close();
    };
  }

  disconnect() {
    this.closed = true;
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }
}
