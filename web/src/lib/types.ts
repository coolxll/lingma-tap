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
