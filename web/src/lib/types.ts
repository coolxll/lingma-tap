export interface SSEEvent {
  event_type: string;
  data: string;
  body?: string;
}

export interface TrafficRecord {
  id: number;
  ts: string;
  session: string;
  index: number;
  direction: 'C2S' | 'S2C';

  method: string;
  url: string;
  host: string;
  path: string;
  is_encoded: boolean;
  endpoint_type: 'chat' | 'finish' | 'embedding' | 'tracking' | 'other';
  request_headers: { [key: string]: string };
  request_body: string;
  request_body_raw: string;
  request_mime: string;
  request_size: number;

  status: number;
  status_text: string;
  response_headers: { [key: string]: string };
  response_body: string;
  response_mime: string;
  response_size: number;
  is_sse: boolean;
  sse_events?: SSEEvent[];

  error?: string;
  source: string;

  // AI Metadata (for source === 'gateway')
  model?: string;
  input_tokens?: number;
  output_tokens?: number;
  latency?: number;
  finish_reason?: string;
}

export interface GatewayLog {
  id: number;
  ts: string;
  session: string;
  model: string;
  method: string;
  path: string;
  request_body: string;
  response_body: string;
  input_tokens: number;
  output_tokens: number;
  status: number;
  latency: number;
  error?: string;
  is_sse: boolean;
  sse_events?: SSEEvent[];
}

export interface StorageStats {
  records: number;
  sessions: number;
  oldest_ts?: string;
  newest_ts?: string;
}

export function formatTimestamp(ts: string): string {
  try {
    const date = new Date(ts);
    return date.toLocaleTimeString('en-US', {
      hour12: false,
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  } catch {
    return ts;
  }
}

export function getEndpointColor(endpoint: string): string {
  switch (endpoint) {
    case 'chat':
      return 'text-blue-400';
    case 'finish':
      return 'text-green-400';
    case 'embedding':
      return 'text-purple-400';
    case 'tracking':
      return 'text-yellow-400';
    default:
      return 'text-zinc-400';
  }
}

export function getEndpointLabel(endpoint: string): string {
  switch (endpoint) {
    case 'chat':
      return 'Chat';
    case 'finish':
      return 'Finish';
    case 'embedding':
      return 'Embed';
    case 'tracking':
      return 'Track';
    default:
      return 'Other';
  }
}

export function getStatusColor(status: number): string {
  if (status >= 200 && status < 300) return 'text-green-400';
  if (status >= 300 && status < 400) return 'text-yellow-400';
  if (status >= 400) return 'text-red-400';
  return 'text-zinc-400';
}

export function recordKey(r: TrafficRecord): string {
  return `${r.session}-${r.index}`;
}
