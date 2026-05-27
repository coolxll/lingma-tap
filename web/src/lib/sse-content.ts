import { SSEEvent } from './types';

export interface MergedSSEContent {
  text: string;
  toolCalls: string[];
}

function parseJSONMaybe(value: unknown): unknown {
  if (typeof value !== 'string') return value;
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}

function unwrapSSEPayload(rawContent: unknown): unknown {
  const parsed = parseJSONMaybe(rawContent);
  if (parsed && typeof parsed === 'object' && 'body' in parsed) {
    const body = (parsed as { body?: unknown }).body;
    if (body !== undefined && body !== null) {
      return parseJSONMaybe(body);
    }
  }
  return parsed;
}

function buildSSEEvent(data: string, eventType = ''): SSEEvent {
  const evt: SSEEvent = {
    event_type: eventType,
    data,
  };
  const parsed = parseJSONMaybe(data);
  if (parsed && typeof parsed === 'object' && 'body' in parsed) {
    const body = (parsed as { body?: unknown }).body;
    if (typeof body === 'string') {
      evt.body = body;
    } else if (body !== undefined && body !== null) {
      evt.body = JSON.stringify(body);
    }
  }
  return evt;
}

function appendJSONEvent(events: SSEEvent[], data: string, eventType = '') {
  const parsed = parseJSONMaybe(data);
  if (Array.isArray(parsed)) {
    for (const item of parsed) {
      events.push(buildSSEEvent(JSON.stringify(item), eventType));
    }
    return;
  }
  if (parsed && typeof parsed === 'object') {
    events.push(buildSSEEvent(data, eventType));
  }
}

function isStandaloneSSEData(value: string): boolean {
  if (value === '[DONE]') return true;
  const parsed = parseJSONMaybe(value);
  return !!(parsed && typeof parsed === 'object');
}

function extractJSONValues(text: string): string[] {
  const values: string[] = [];
  let start = -1;
  let depth = 0;
  let inString = false;
  let escaped = false;

  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i];

    if (inString) {
      if (escaped) {
        escaped = false;
      } else if (ch === '\\') {
        escaped = true;
      } else if (ch === '"') {
        inString = false;
      }
      continue;
    }

    if (ch === '"') {
      inString = true;
      continue;
    }

    if (ch === '{' || ch === '[') {
      if (depth === 0) start = i;
      depth += 1;
      continue;
    }

    if ((ch === '}' || ch === ']') && depth > 0) {
      depth -= 1;
      if (depth === 0 && start >= 0) {
        values.push(text.slice(start, i + 1));
        start = -1;
      }
    }
  }

  return values;
}

export function parseSSEEventsFromText(text: string): SSEEvent[] {
  const events: SSEEvent[] = [];
  let eventType = '';
  let dataLines: string[] = [];

  const flushSSEEvent = () => {
    if (dataLines.length === 0) return;
    events.push(buildSSEEvent(dataLines.join('\n'), eventType));
    eventType = '';
    dataLines = [];
  };

  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed) {
      flushSSEEvent();
      continue;
    }

    if (trimmed.startsWith('event:')) {
      eventType = trimmed.slice(6).trim();
      continue;
    }

    if (trimmed.startsWith('data:')) {
      const data = trimmed.slice(5).trim();
      if (dataLines.length > 0 && isStandaloneSSEData(data) && dataLines.every(isStandaloneSSEData)) {
        flushSSEEvent();
      }
      dataLines.push(data);
      continue;
    }

    flushSSEEvent();
    const before = events.length;
    appendJSONEvent(events, trimmed, eventType);
    if (events.length > before) {
      eventType = '';
    }
  }

  flushSSEEvent();

  if (events.length === 0) {
    for (const value of extractJSONValues(text)) {
      appendJSONEvent(events, value);
    }
  }

  return events;
}

function appendStringPart(parts: string[], value: unknown) {
  if (typeof value === 'string' && value.length > 0) {
    parts.push(value);
  }
}

function appendContentArray(parts: string[], content: unknown) {
  if (!Array.isArray(content)) return;
  for (const block of content) {
    if (typeof block === 'string') {
      appendStringPart(parts, block);
    } else if (block && typeof block === 'object') {
      const item = block as Record<string, unknown>;
      appendStringPart(parts, item.text);
      appendStringPart(parts, item.thinking);
      appendStringPart(parts, item.output_text);
    }
  }
}

function extractOutputText(parts: string[], output: unknown) {
  if (!Array.isArray(output)) return;
  for (const item of output) {
    if (!item || typeof item !== 'object') continue;
    const obj = item as Record<string, unknown>;
    appendStringPart(parts, obj.text);
    appendStringPart(parts, obj.output_text);
    appendContentArray(parts, obj.content);
  }
}

function extractTextParts(target: unknown): string[] {
  const parts: string[] = [];
  if (!target || typeof target !== 'object') return parts;

  const obj = target as Record<string, any>;
  const eventType = typeof obj.type === 'string' ? obj.type : '';

  // OpenAI Responses API stream events.
  if (eventType === 'response.output_text.delta' && typeof obj.delta === 'string') {
    appendStringPart(parts, obj.delta);
  }

  // Anthropic-compatible stream events.
  if (eventType === 'content_block_delta' && obj.delta && typeof obj.delta === 'object') {
    appendStringPart(parts, obj.delta.text);
    appendStringPart(parts, obj.delta.thinking);
  }

  // OpenAI chat-completion and DashScope-compatible chunks.
  const delta = obj.choices?.[0]?.delta || obj.output?.choices?.[0]?.delta;
  if (delta) {
    appendStringPart(parts, delta.content);
    appendStringPart(parts, delta.text);
  }

  appendStringPart(parts, obj.choices?.[0]?.text);
  appendStringPart(parts, obj.choices?.[0]?.message?.content);
  appendContentArray(parts, obj.choices?.[0]?.message?.content);

  // DashScope older style: output.text usually contains the full text so far.
  if (obj.output?.text !== undefined && obj.output?.text !== null) {
    return typeof obj.output.text === 'string' ? [obj.output.text] : parts;
  }

  appendStringPart(parts, obj.output_text);
  extractOutputText(parts, obj.output);
  appendStringPart(parts, obj.content);
  appendStringPart(parts, obj.text);
  appendContentArray(parts, obj.content);

  // Generic stream delta fallback for shapes not handled above.
  if (parts.length === 0) {
    if (typeof obj.delta === 'string' && !eventType.includes('reasoning') && !eventType.includes('function_call')) {
      appendStringPart(parts, obj.delta);
    } else if (obj.delta && typeof obj.delta === 'object') {
      appendStringPart(parts, obj.delta.content);
      appendStringPart(parts, obj.delta.text);
    }
  }

  return parts;
}

function splitMergedJSONEvent(rawContent: string, eventType = ''): SSEEvent[] {
  const lines = rawContent
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  if (lines.length <= 1 || !lines.every(isStandaloneSSEData)) return [];
  return lines.map((line) => buildSSEEvent(line, eventType));
}

function looksLikeStreamPayload(target: unknown): boolean {
  if (target === '[DONE]') return true;
  if (!target || typeof target !== 'object') return false;

  const obj = target as Record<string, any>;
  if (Array.isArray(obj.choices) || obj.output !== undefined) return true;
  if (typeof obj.type === 'string' && (obj.type.includes('response.') || obj.type.includes('content_block'))) {
    return true;
  }
  if (obj.body !== undefined && obj.body !== null) {
    return looksLikeStreamPayload(parseJSONMaybe(obj.body));
  }
  return false;
}

export function hasReadableSSEPayload(events: SSEEvent[]): boolean {
  for (const evt of events) {
    const rawContent = evt.body ?? evt.data;
    if (!rawContent) continue;
    const target = unwrapSSEPayload(rawContent);
    if (looksLikeStreamPayload(target) || extractTextParts(target).length > 0) {
      return true;
    }
  }
  return false;
}

export function mergeSSEContent(events: SSEEvent[]): MergedSSEContent {
  let text = '';
  const toolCalls: string[] = [];

  for (const evt of events) {
    const rawContent = evt.body ?? evt.data;
    if (!rawContent || rawContent === '[DONE]') continue;

    if (!evt.body) {
      const splitEvents = splitMergedJSONEvent(rawContent, evt.event_type);
      if (splitEvents.length > 1) {
        const merged = mergeSSEContent(splitEvents);
        text += merged.text;
        toolCalls.push(...merged.toolCalls);
        continue;
      }
    }

    try {
      const target = unwrapSSEPayload(rawContent);
      if (target === '[DONE]') continue;
      if (typeof target === 'string') {
        if (!rawContent.includes('"body"') && !rawContent.includes('"choices"')) {
          text += target;
        }
        continue;
      }

      const textParts = extractTextParts(target);
      if (textParts.length > 0) {
        if (target && typeof target === 'object' && (target as Record<string, any>).output?.text !== undefined) {
          text = textParts.join('');
        } else {
          text += textParts.join('');
        }
      }

      const obj = target as Record<string, any>;
      const delta = obj?.choices?.[0]?.delta || obj?.output?.choices?.[0]?.delta;
      const toolCallsDelta = delta?.tool_calls || obj?.choices?.[0]?.message?.tool_calls;
      if (toolCallsDelta) {
        for (const tc of toolCallsDelta) {
          const fn = tc.function;
          if (fn?.name) {
            toolCalls.push(`${fn.name}(${fn.arguments || ''})`);
          } else if (fn?.arguments && toolCalls.length > 0) {
            toolCalls[toolCalls.length - 1] += fn.arguments;
          }
        }
      }
      if (obj?.type === 'response.function_call_arguments.delta' && typeof obj.delta === 'string') {
        if (toolCalls.length > 0) {
          toolCalls[toolCalls.length - 1] += obj.delta;
        } else {
          toolCalls.push(obj.delta);
        }
      }
      if (obj?.type === 'content_block_delta' && typeof obj.delta?.partial_json === 'string') {
        if (toolCalls.length > 0) {
          toolCalls[toolCalls.length - 1] += obj.delta.partial_json;
        } else {
          toolCalls.push(obj.delta.partial_json);
        }
      }
    } catch {
      // Skip malformed packets.
    }
  }

  return { text, toolCalls };
}

export function extractReadableResponseText(responseBody: string): string {
  if (!responseBody) return '';

  const events = parseSSEEventsFromText(responseBody);
  if (events.length > 0) {
    const merged = mergeSSEContent(events);
    if (merged.text) return merged.text;
  }

  const parsed = parseJSONMaybe(responseBody);
  if (typeof parsed === 'string') return parsed;

  const target = unwrapSSEPayload(parsed);
  if (target === '[DONE]') return '';
  if (typeof target === 'string') return target;

  const parts = extractTextParts(target);
  return parts.join('');
}
