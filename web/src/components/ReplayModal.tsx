import { useEffect, useMemo, useState } from 'react';
import { Copy, Check, X, Terminal } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { TrafficRecord } from '@/lib/types';

interface ReplayModalProps {
  request: TrafficRecord;
  onClose: () => void;
}

function buildInitialBody(request: TrafficRecord): string {
  const raw = request.request_body || '';
  if (!raw) return '';
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function buildInitialHeaders(request: TrafficRecord): string {
  return JSON.stringify(request.request_headers || {}, null, 2);
}

function shellEscape(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

function buildCurl(method: string, url: string, headersJSON: string, bodyJSON: string): string {
  const parts: string[] = [];
  parts.push(`curl -X ${method.toUpperCase() || 'POST'} ${shellEscape(url)}`);

  let headers: Record<string, string> = {};
  try {
    const parsed = JSON.parse(headersJSON || '{}');
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      for (const [k, v] of Object.entries(parsed)) {
        headers[k] = typeof v === 'string' ? v : String(v);
      }
    }
  } catch {
    // headers JSON invalid; emit a comment and skip headers
    parts.push(`# WARNING: headers JSON is invalid, headers omitted`);
  }
  for (const [k, v] of Object.entries(headers)) {
    parts.push(`  -H ${shellEscape(`${k}: ${v}`)}`);
  }

  if (bodyJSON && bodyJSON.trim()) {
    parts.push(`  --data-raw ${shellEscape(bodyJSON)}`);
  }

  return parts.join(' \\\n');
}

export function ReplayModal({ request, onClose }: ReplayModalProps) {
  const { t } = useTranslation();
  const [headersText, setHeadersText] = useState(() => buildInitialHeaders(request));
  const [bodyText, setBodyText] = useState(() => buildInitialBody(request));
  const [copied, setCopied] = useState(false);

  const curl = useMemo(
    () => buildCurl(request.method, request.url, headersText, bodyText),
    [request.method, request.url, headersText, bodyText],
  );

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, [onClose]);

  const handleCopyCurl = async () => {
    try {
      await navigator.clipboard.writeText(curl);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // ignore
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onClick={onClose}
    >
      <div
        className="w-[920px] max-w-[95vw] max-h-[90vh] flex flex-col bg-zinc-950 border border-zinc-800 rounded-2xl shadow-2xl overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 py-3 border-b border-zinc-800">
          <div className="flex items-center gap-2 text-sm font-bold text-zinc-100">
            <Terminal className="w-4 h-4 text-blue-400" />
            {t('detailpanel.replay_title')}
          </div>
          <button
            onClick={onClose}
            className="p-1 rounded hover:bg-zinc-800 text-zinc-400 hover:text-zinc-200 transition-colors"
            aria-label={t('detailpanel.close')}
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <div className="px-5 py-3 border-b border-zinc-800 bg-zinc-900/30">
          <p className="text-[11px] text-zinc-400 leading-relaxed">
            {t('detailpanel.replay_hint')}
          </p>
          <div className="mt-2 flex items-center gap-2 text-[10px] font-mono">
            <span className="px-1.5 py-0.5 rounded bg-blue-900/40 text-blue-400 font-bold uppercase">{request.method}</span>
            <span className="text-zinc-400 truncate">{request.url}</span>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4 px-5 py-4 overflow-y-auto flex-1">
          <div className="flex flex-col gap-2 min-h-[260px]">
            <label className="text-[10px] font-bold uppercase tracking-widest text-zinc-500">
              {t('detailpanel.body_json')}
            </label>
            <textarea
              value={bodyText}
              onChange={(e) => setBodyText(e.target.value)}
              spellCheck={false}
              className="flex-1 min-h-[260px] bg-zinc-900 border border-zinc-800 rounded-lg p-3 text-xs font-mono text-zinc-200 focus:outline-none focus:ring-1 focus:ring-blue-500/50 resize-none"
            />
          </div>
          <div className="flex flex-col gap-2 min-h-[260px]">
            <label className="text-[10px] font-bold uppercase tracking-widest text-zinc-500">
              {t('detailpanel.headers_json')}
            </label>
            <textarea
              value={headersText}
              onChange={(e) => setHeadersText(e.target.value)}
              spellCheck={false}
              className="flex-1 min-h-[260px] bg-zinc-900 border border-zinc-800 rounded-lg p-3 text-xs font-mono text-zinc-200 focus:outline-none focus:ring-1 focus:ring-blue-500/50 resize-none"
            />
          </div>
        </div>

        <div className="px-5 py-3 border-t border-zinc-800 bg-zinc-900/30">
          <label className="text-[10px] font-bold uppercase tracking-widest text-zinc-500">
            {t('detailpanel.curl_preview')}
          </label>
          <pre className="mt-1 max-h-[140px] overflow-auto bg-zinc-900 border border-zinc-800 rounded-lg p-3 text-[11px] font-mono text-zinc-300 whitespace-pre-wrap break-all">
            {curl}
          </pre>
        </div>

        <div className="flex items-center justify-end gap-2 px-5 py-3 border-t border-zinc-800">
          <button
            onClick={onClose}
            className="px-3 py-1.5 rounded-lg text-xs font-bold bg-zinc-800 hover:bg-zinc-700 text-zinc-200 transition-colors"
          >
            {t('detailpanel.close')}
          </button>
          <button
            onClick={handleCopyCurl}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-bold bg-blue-600 hover:bg-blue-500 text-white transition-colors"
          >
            {copied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
            {t('detailpanel.copy_curl')}
          </button>
        </div>
      </div>
    </div>
  );
}
