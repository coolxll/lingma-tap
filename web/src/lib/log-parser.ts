import { TrafficRecord } from './types';

export interface LogDetails {
  model: string;
  inputTokens: number;
  outputTokens: number;
}

/**
 * Extracts model name and token usage from a request/response pair.
 */
export function parseLogDetails(record: TrafficRecord, response?: TrafficRecord | null): LogDetails {
  let model = '-';
  let inputTokens = 0;
  let outputTokens = 0;

  // 1. Parse Request Body for Model
  if (record.request_body) {
    try {
      const body = JSON.parse(record.request_body);
      model = body.model || body.engine || '-';
      
      // Some APIs might have usage info in the request for certain types, but rare
    } catch (e) {
      // Not JSON or malformed
    }
  }

  // 2. Parse Response Body for Usage
  if (response && response.response_body) {
    try {
      const body = JSON.parse(response.response_body);
      
      // Support OpenAI and DashScope/Lingma formats
      const usage = body.usage || body.output?.usage;
      if (usage) {
        inputTokens = usage.prompt_tokens || usage.input_tokens || 0;
        outputTokens = usage.completion_tokens || usage.output_tokens || 0;
      }
    } catch (e) {
      // Not JSON
    }
  }

  // 3. Parse SSE Events for Usage (often at the end of the stream)
  if (response && response.is_sse && response.sse_events) {
    for (const evt of response.sse_events) {
      const raw = evt.body || evt.data;
      if (!raw || raw === '[DONE]') continue;
      try {
        const parsed = JSON.parse(raw);
        const usage = parsed.usage || parsed.output?.usage;
        if (usage) {
          inputTokens = usage.prompt_tokens || usage.input_tokens || inputTokens;
          outputTokens = usage.completion_tokens || usage.output_tokens || outputTokens;
        }
      } catch (e) {
        // Skip malformed packets
      }
    }
  }

  return { model, inputTokens, outputTokens };
}
