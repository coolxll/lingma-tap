function parseJSONMaybe(value: string): unknown {
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}

function stringifyContent(value: unknown): string {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) {
    return value.map(stringifyContent).filter(Boolean).join('\n');
  }
  if (typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    if (typeof obj.text === 'string') return obj.text;
    if (typeof obj.content === 'string') return obj.content;
    if (Array.isArray(obj.content)) return stringifyContent(obj.content);
    if (typeof obj.input === 'string') return obj.input;
    return JSON.stringify(value);
  }
  return String(value);
}

function extractMarkdownSection(text: string, titles: string[]): string {
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  const normalizedTitles = titles.map((title) => title.toLowerCase());
  let start = -1;

  for (let i = 0; i < lines.length; i += 1) {
    const match = lines[i].match(/^#{2,6}\s*(.+?)\s*$/);
    const title = match?.[1].trim().toLowerCase();
    if (title && normalizedTitles.some((expected) => title === expected || title.startsWith(expected))) {
      start = i + 1;
      break;
    }
  }

  if (start < 0) return '';

  const section: string[] = [];
  for (let i = start; i < lines.length; i += 1) {
    if (/^#{2,6}\s+\S/.test(lines[i])) break;
    section.push(lines[i]);
  }

  return section.join('\n').trim();
}

function unwrapTaggedUserText(text: string): string {
  let result = text.trim();

  const userQueryMatch = result.match(/<user_query>\s*([\s\S]*?)\s*<\/user_query>/i);
  if (userQueryMatch) {
    result = userQueryMatch[1].trim();
  }

  result = result
    .replace(/^\s*\[(user|用户)\]\s*:?\s*/i, '')
    .replace(/^\s*<user_query>\s*/i, '')
    .replace(/\s*<\/user_query>\s*$/i, '')
    .trim();

  return result;
}

function simplifyPromptText(text: string): string {
  const trimmed = text.trim();
  if (!trimmed) return '';

  const currentQuestion = extractMarkdownSection(trimmed, [
    '当前提问',
    '当前问题',
    '用户当前提问',
    '用户当前问题',
    '用户当前消息',
    'current question',
    'current prompt',
    'current message',
  ]);
  if (currentQuestion) return unwrapTaggedUserText(currentQuestion);

  return unwrapTaggedUserText(trimmed);
}

function extractMessageText(message: unknown): string {
  if (!message || typeof message !== 'object') return stringifyContent(message);
  const obj = message as Record<string, unknown>;
  if (obj.content !== undefined) return stringifyContent(obj.content);
  if (obj.prompt !== undefined) return stringifyContent(obj.prompt);
  if (obj.text !== undefined) return stringifyContent(obj.text);
  return stringifyContent(message);
}

export function extractReadablePromptText(requestBody: string): string {
  if (!requestBody) return '';

  const parsed = parseJSONMaybe(requestBody);
  if (!parsed || typeof parsed !== 'object') {
    return simplifyPromptText(requestBody);
  }

  const obj = parsed as Record<string, any>;
  if (Array.isArray(obj.messages) && obj.messages.length > 0) {
    const userMessages = obj.messages.filter((message: any) => message?.role === 'user');
    const message = userMessages[userMessages.length - 1] || obj.messages[obj.messages.length - 1];
    return simplifyPromptText(extractMessageText(message));
  }

  for (const key of ['prompt', 'input', 'query', 'question']) {
    if (obj[key] !== undefined) {
      return simplifyPromptText(stringifyContent(obj[key]));
    }
  }

  return simplifyPromptText(JSON.stringify(parsed, null, 2));
}
