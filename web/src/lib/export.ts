// File-export helpers used by DetailPanel to dump selected records as JSONL.
// Mirrors cursor-tap's web/src/app/page.tsx (safeFilename / recordsToJSONL /
// downloadTextFile).

export function safeFilename(value: string, fallback: string): string {
  const clean = value.trim().replace(/[^a-z0-9_-]+/gi, '-').replace(/^-+|-+$/g, '');
  return clean || fallback;
}

export function recordsToJSONL(value: unknown[]): string {
  if (value.length === 0) return '';
  return `${value.map((record) => JSON.stringify(record)).join('\n')}\n`;
}

export function downloadTextFile(filename: string, value: string, type: string): void {
  const blob = new Blob([value], { type });
  const href = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = href;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(href);
}
