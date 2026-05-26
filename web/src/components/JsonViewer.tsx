import { useEffect, useMemo, useRef, useState } from 'react';
import { Copy, Check } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { createHighlighterCore, type HighlighterCore } from 'shiki/core';
import { createJavaScriptRegexEngine } from 'shiki/engine/javascript';

interface JsonViewerProps {
  data: string;
  maxHeight?: string;
}

const SHIKI_CHAR_LIMIT = 120_000;
const CACHE_LIMIT = 24;
const shikiCache = new Map<string, string>();

let highlighterPromise: Promise<HighlighterCore> | null = null;
function getHighlighter(): Promise<HighlighterCore> {
  if (!highlighterPromise) {
    highlighterPromise = createHighlighterCore({
      themes: [
        import('@shikijs/themes/github-light'),
        import('@shikijs/themes/github-dark'),
      ],
      langs: [import('@shikijs/langs/json')],
      engine: createJavaScriptRegexEngine(),
    });
  }
  return highlighterPromise;
}

async function highlightJson(formatted: string): Promise<string> {
  const highlighter = await getHighlighter();
  return highlighter.codeToHtml(formatted, {
    lang: 'json',
    themes: { light: 'github-light', dark: 'github-dark' },
  });
}

function rememberHighlighted(key: string, html: string): void {
  if (shikiCache.has(key)) shikiCache.delete(key);
  shikiCache.set(key, html);
  while (shikiCache.size > CACHE_LIMIT) {
    const oldest = shikiCache.keys().next().value;
    if (oldest === undefined) break;
    shikiCache.delete(oldest);
  }
}

function regexHighlight(formatted: string): string {
  return (formatted || '')
    .replace(/"([^"]+)":/g, '<span class="text-purple-400">"$1"</span>:')
    .replace(/: "([^"]*)"/g, ': <span class="text-green-400">"$1"</span>')
    .replace(/: (-?\d+\.?\d*)/g, ': <span class="text-blue-400">$1</span>')
    .replace(/: (true|false)/g, ': <span class="text-orange-400">$1</span>')
    .replace(/: (null)/g, ': <span class="text-zinc-500">$1</span>');
}

export function JsonViewer({ data, maxHeight = '400px' }: JsonViewerProps) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const formatted = useMemo(() => {
    try {
      return JSON.stringify(JSON.parse(data), null, 2);
    } catch {
      return data;
    }
  }, [data]);

  const cacheKey = `json:${formatted}`;
  const tooLarge = formatted.length > SHIKI_CHAR_LIMIT;
  const fallback = useMemo(() => regexHighlight(formatted), [formatted]);

  const [shikiHtml, setShikiHtml] = useState<string | null>(() =>
    tooLarge ? null : shikiCache.get(cacheKey) ?? null,
  );
  const lastKeyRef = useRef(cacheKey);

  useEffect(() => {
    if (tooLarge) {
      setShikiHtml(null);
      return;
    }
    const cached = shikiCache.get(cacheKey);
    if (cached) {
      setShikiHtml(cached);
      return;
    }
    let cancelled = false;
    lastKeyRef.current = cacheKey;
    highlightJson(formatted)
      .then((html) => {
        if (cancelled || lastKeyRef.current !== cacheKey) return;
        rememberHighlighted(cacheKey, html);
        setShikiHtml(html);
      })
      .catch(() => {
        // Swallow; fallback rendering still works.
      });
    return () => {
      cancelled = true;
    };
  }, [cacheKey, formatted, tooLarge]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(formatted);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // ignore
    }
  };

  const useShiki = !tooLarge && shikiHtml !== null;

  return (
    <div className="relative group w-full min-w-0">
      <button
        className="absolute top-2 right-2 p-1 rounded opacity-0 group-hover:opacity-100 transition-opacity z-10 hover:bg-zinc-700"
        onClick={handleCopy}
        title={t('common.copy')}
      >
        {copied ? (
          <Check className="w-3.5 h-3.5 text-green-400" />
        ) : (
          <Copy className="w-3.5 h-3.5 text-zinc-400" />
        )}
      </button>
      <div className="w-full overflow-x-auto">
        {useShiki ? (
          <div
            className="json-shiki text-xs"
            style={{ maxHeight, minWidth: 'min-content' }}
            dangerouslySetInnerHTML={{ __html: shikiHtml as string }}
          />
        ) : (
          <pre
            className="p-3 bg-zinc-900 rounded text-xs font-mono whitespace-pre"
            style={{ maxHeight, minWidth: 'min-content' }}
            dangerouslySetInnerHTML={{ __html: fallback }}
          />
        )}
      </div>
    </div>
  );
}
