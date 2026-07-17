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
  endpoint_type: 'chat' | 'finish' | 'embedding' | 'tracking' | 'image_upload' | 'image_resource' | 'other';
  request_headers: { [key: string]: string };
  request_body: string;
  request_body_raw: string;
  request_mime: string;
  request_size: number;
	body_phase?: 'headers' | 'complete' | 'error';
	body_complete?: boolean;
	body_truncated?: boolean;
	captured_size?: number;
	declared_size?: number;
	body_encoding?: 'empty' | 'text' | 'binary';
	content_encoding?: string;
	correlation_keys?: string[];
	artifact_ids?: number[];

  status: number;
  status_text: string;
  response_headers: { [key: string]: string };
  response_body: string;
  response_body_raw?: string;
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
  cached_tokens?: number;
  reasoning_tokens?: number;
  total_tokens?: number;
  ttft?: number;
  upstream_attempts?: number;
  recovery_applied?: boolean;
  upstream_error_class?: string;
  first_actionable_ms?: number;
  reasoning_only_bytes?: number;
  requested_profile?: string;
  effective_profile?: string;
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
  cached_tokens?: number;
  reasoning_tokens?: number;
  total_tokens?: number;
  ttft?: number;
  upstream_attempts?: number;
  recovery_applied?: boolean;
  upstream_error_class?: string;
  first_actionable_ms?: number;
  reasoning_only_bytes?: number;
  requested_profile?: string;
  effective_profile?: string;
  status: number;
  latency: number;
  error?: string;
  is_sse: boolean;
  sse_events?: SSEEvent[];
  finish_reason?: string;
}

export function mapGatewayLogToRecord(log: any): TrafficRecord {
  return {
    ts: log.ts,
    id: log.id || 0,
    session: log.session,
    direction: "C2S",
    source: "gateway",
    method: log.method,
    path: log.path,
    endpoint_type: "chat",
    request_body: log.request_body || "",
    response_body: log.response_body || "",
    status: log.status || 0,
    is_sse: log.is_sse || false,
    sse_events: log.sse_events || [],
    model: log.model || "",
    input_tokens: log.input_tokens || 0,
    output_tokens: log.output_tokens || 0,
    cached_tokens: log.cached_tokens || 0,
    reasoning_tokens: log.reasoning_tokens || 0,
    total_tokens: log.total_tokens || 0,
    ttft: log.ttft || 0,
    upstream_attempts: log.upstream_attempts || 0,
    recovery_applied: log.recovery_applied || false,
    upstream_error_class: log.upstream_error_class || "",
    first_actionable_ms: log.first_actionable_ms || 0,
    reasoning_only_bytes: log.reasoning_only_bytes || 0,
    requested_profile: log.requested_profile || "",
    effective_profile: log.effective_profile || "",
    latency: log.latency || 0,
    error: log.error || "",
    finish_reason: log.finish_reason || "",
    index: 0,
    url: "",
    host: "",
    is_encoded: false,
    request_headers: {},
    request_body_raw: "",
    request_mime: "",
    request_size: 0,
    status_text: "",
    response_headers: {},
    response_mime: "",
    response_size: 0,
  };
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

export function formatDurationMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const seconds = ms / 1000;
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 2 : 1)}s`;
  const minutes = Math.floor(seconds / 60);
  const remSeconds = Math.round(seconds - minutes * 60);
  if (minutes < 60) return `${minutes}m ${remSeconds}s`;
  const hours = Math.floor(minutes / 60);
  const remMinutes = minutes - hours * 60;
  return `${hours}h ${remMinutes}m`;
}

export function formatTimeSpan(fromTs?: string, toTs?: string): string {
  if (!fromTs || !toTs) return '';
  const from = new Date(fromTs).getTime();
  const to = new Date(toTs).getTime();
  if (!Number.isFinite(from) || !Number.isFinite(to) || to < from) return '';
  return formatDurationMs(to - from);
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
	case 'image_upload':
	  return 'text-pink-400';
	case 'image_resource':
	  return 'text-cyan-400';
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
	case 'image_upload':
	  return 'Image Upload';
	case 'image_resource':
	  return 'Image';
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

export function formatFriendlyMessage(message: any): string {
  if (!message) return "";
  if (typeof message === "string") return message;

  let result = "";

  // 1. Text content
  if (typeof message.content === "string") {
    result += message.content;
  } else if (Array.isArray(message.content)) {
    result += message.content.map(formatContentBlock).join("\n");
  }

  // 2. OpenAI Tool Calls
  if (message.tool_calls && Array.isArray(message.tool_calls)) {
    result += message.tool_calls.map(formatOpenAIToolCall).join("\n");
  }

  // Fallback for direct blocks
  if (!result && message.content === undefined && message.tool_calls === undefined) {
    if (message.type === "text") return message.text;
    if (message.type === "tool_use") return formatAnthropicToolUse(message);
    result = JSON.stringify(message);
  }

  return result.trim();
}

function formatContentBlock(c: any): string {
  if (typeof c === "string") return c;
  if (c.type === "text") return c.text || "";
  if (c.type === "tool_use") return formatAnthropicToolUse(c);
  if (c.type === "tool_result") {
    const resContent = typeof c.content === "string" ? c.content : JSON.stringify(c.content);
    return `\n\n[✅ Tool Result: ${c.tool_use_id}]\n${resContent}`;
  }
  return JSON.stringify(c);
}

function formatAnthropicToolUse(block: any): string {
  const input = typeof block.input === "object" ? JSON.stringify(block.input, null, 2) : block.input;
  return `\n\n[🛠️ Tool Call: ${block.name}]\nArguments: ${input}`;
}

function formatOpenAIToolCall(tc: any): string {
  if (tc.type === "function" && tc.function) {
    let args = tc.function.arguments;
    try {
      args = JSON.stringify(JSON.parse(args), null, 2);
    } catch {
      // Keep raw if not valid JSON
    }
    return `\n\n[🛠️ Tool Call: ${tc.function.name}]\nArguments: ${args}`;
  }
  return JSON.stringify(tc);
}
