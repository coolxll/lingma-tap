import { memo, useState, useMemo } from 'react';
import { TrafficRecord, formatTimestamp, getEndpointLabel, formatFriendlyMessage, getEndpointColor } from '@/lib/types';
import { JsonViewer } from './JsonViewer';
import { SseEventList } from './SseEventList';
import { useTranslation } from 'react-i18next';
import { MessageSquare, Code, User, Bot, Sparkles } from 'lucide-react';

interface DetailPanelProps {
  request: TrafficRecord | null;
  response: TrafficRecord | null;
}

export const DetailPanel = memo(function DetailPanel({ request, response }: DetailPanelProps) {
  const { t } = useTranslation();
  const [viewMode, setViewMode] = useState<'friendly' | 'standard'>('standard');

  const chatContent = useMemo(() => {
    try {
      if (!request || (request.endpoint_type !== 'chat' && request.endpoint_type !== 'finish')) return null;

      let prompt = '';
      let completion = '';

      // Extract prompt
      try {
        const reqBody = JSON.parse(request.request_body || '{}');
        if (reqBody.messages && Array.isArray(reqBody.messages) && reqBody.messages.length > 0) {
          const lastMsg = reqBody.messages[reqBody.messages.length - 1];
          prompt = formatFriendlyMessage(lastMsg);
        } else if (reqBody.prompt) {
          prompt = typeof reqBody.prompt === 'string' ? reqBody.prompt : JSON.stringify(reqBody.prompt);
        }
      } catch (e) {
        prompt = request.request_body;
      }

      // Extract completion
      if (response) {
        if (response?.is_sse && response?.sse_events) {
          completion = response.sse_events
            .map((e: any) => {
              try {
                if (!e.data || e.data === '[DONE]') return '';
                const data = JSON.parse(e.data);
                
                // Handle nested structure if 'body' exists
                let target = data;
                if (data.body && typeof data.body === 'string') {
                  try {
                    target = JSON.parse(data.body);
                  } catch {
                    // If body is not JSON, use it as is or fallback
                  }
                }

                const delta = target.choices?.[0]?.delta;
                if (delta) return formatFriendlyMessage(delta);
                const content = target.choices?.[0]?.text || '';
                return typeof content === 'string' ? content : JSON.stringify(content);
              } catch {
                return '';
              }
            })
            .join('');
        } else {
          try {
            const respBody = JSON.parse(response.response_body || '{}');
            const message = respBody.choices?.[0]?.message || respBody.choices?.[0]?.delta;
            if (message) {
              completion = formatFriendlyMessage(message);
            } else {
              const content = respBody.choices?.[0]?.text || response.response_body;
              completion = typeof content === 'string' ? content : JSON.stringify(content || '');
            }
          } catch {
            completion = typeof response.response_body === 'string' ? response.response_body : JSON.stringify(response.response_body || '');
          }
        }
      }

      return { prompt, completion };
    } catch (err) {
      console.error("Error in chatContent useMemo:", err);
      (window as any).go?.main?.App?.LogError(`chatContent error: ${err}`);
      return { prompt: 'Error extracting prompt', completion: 'Error extracting completion' };
    }
  }, [request, response]);

  if (!request) {
    return (
      <div className="h-full flex items-center justify-center text-zinc-600 text-sm">
        {t('detailpanel.select_record')}
      </div>
    );
  }

  return (
    <div className="h-full overflow-y-auto bg-zinc-950">
      {/* Summary */}
      <div className="px-4 py-3 border-b border-zinc-800 flex items-center justify-between bg-zinc-900/20">
        <div>
          <div className="flex items-center gap-2 mb-1">
            <span className="px-2 py-0.5 rounded text-[10px] font-bold uppercase bg-blue-900/50 text-blue-400 border border-blue-500/20">
              {request.method}
            </span>
            <span className="text-xs text-zinc-300 font-mono break-all">{request.url}</span>
          </div>
          <div className="flex items-center gap-3 text-[10px] text-zinc-500 font-medium">
            <span>{formatTimestamp(request.ts)}</span>
            <span className="w-1 h-1 rounded-full bg-zinc-800" />
            <span className={getEndpointColor(request.endpoint_type)}>{getEndpointLabel(request.endpoint_type)}</span>
            <span className="w-1 h-1 rounded-full bg-zinc-800" />
            <span className="text-zinc-400 font-mono bg-zinc-800/50 px-1.5 py-0.5 rounded">{request.path}</span>
            {request.is_encoded && (
              <>
                <span className="w-1 h-1 rounded-full bg-zinc-800" />
                <span className="text-amber-400">{t('detailpanel.encoded')}</span>
              </>
            )}
            {response && response.status > 0 && (
              <>
                <span className="w-1 h-1 rounded-full bg-zinc-800" />
                <span className={response.status < 400 ? 'text-green-400' : 'text-red-400'}>
                  {response.status_text || response.status}
                </span>
              </>
            )}
          </div>
        </div>

        <div className="flex bg-zinc-900/50 rounded-lg p-0.5 border border-zinc-800">
          <button
            onClick={() => setViewMode('friendly')}
            className={`flex items-center gap-1.5 px-3 py-1 text-[10px] font-bold rounded-md transition-all ${
              viewMode === 'friendly'
                ? 'bg-zinc-800 text-blue-400 shadow-sm'
                : 'text-zinc-500 hover:text-zinc-400'
            }`}
          >
            <MessageSquare className="w-3 h-3" />
            CHAT
          </button>
          <button
            onClick={() => setViewMode('standard')}
            className={`flex items-center gap-1.5 px-3 py-1 text-[10px] font-bold rounded-md transition-all ${
              viewMode === 'standard'
                ? 'bg-zinc-800 text-zinc-200 shadow-sm'
                : 'text-zinc-500 hover:text-zinc-400'
            }`}
          >
            <Code className="w-3 h-3" />
            STANDARD
          </button>
        </div>
      </div>

      {viewMode === 'friendly' && chatContent ? (
        <div className="p-4 space-y-4">
          {/* Prompt */}
          <div className="space-y-2">
            <div className="flex items-center gap-2 text-[10px] font-bold text-zinc-500 uppercase tracking-widest">
              <User className="w-3 h-3" />
              User Prompt
            </div>
            <div className="bg-zinc-900/30 border border-zinc-800/50 rounded-xl p-4 text-sm text-zinc-300 leading-relaxed font-sans shadow-inner whitespace-pre-wrap">
              {chatContent.prompt || 'No prompt content'}
            </div>
          </div>

          {/* Assistant */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 text-[10px] font-bold text-blue-400 uppercase tracking-widest">
                <Bot className="w-3 h-3" />
                Assistant Response
              </div>
              {response?.is_sse && (
                <div className="flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-purple-500/10 border border-purple-500/20 text-[9px] font-bold text-purple-400">
                  <Sparkles className="w-2.5 h-2.5" />
                  STREAMING
                </div>
              )}
            </div>
            <div className="bg-blue-500/5 border border-blue-500/10 rounded-xl p-4 text-sm text-zinc-200 leading-relaxed font-sans shadow-inner min-h-[100px] whitespace-pre-wrap">
              {chatContent.completion || (response ? 'Processing...' : 'Waiting for response...')}
            </div>
          </div>
        </div>
      ) : (
        <div className="divide-y divide-zinc-900">
          {/* Metadata/Headers */}
          {request.request_headers && Object.keys(request.request_headers || {}).length > 0 && (
            <Section title={t('detailpanel.request_headers')}>
              <HeadersTable headers={request.request_headers} />
            </Section>
          )}

          {/* Request Body */}
          <Section title={t('detailpanel.request_body') + (request.is_encoded ? ` (${t('detailpanel.request_body_decoded')})` : '')}>
            {request.request_body ? (
              <JsonViewer data={request.request_body} maxHeight="500px" />
            ) : (
              <p className="text-zinc-600 text-xs">{t('detailpanel.empty_body')}</p>
            )}
          </Section>

          {/* Raw Request Body (if different) */}
          {request.is_encoded && request.request_body_raw && request.request_body_raw !== request.request_body && (
            <Section title={t('detailpanel.request_body_raw')}>
              <JsonViewer data={request.request_body_raw} maxHeight="500px" />
            </Section>
          )}

          {/* Response section */}
          {response ? (
            <>
              {/* Response Headers */}
              {response.response_headers && Object.keys(response.response_headers || {}).length > 0 && (
                <Section title={t('detailpanel.response_headers')}>
                  <HeadersTable headers={response.response_headers} />
                </Section>
              )}

              {/* SSE Events */}
              {response?.is_sse && response?.sse_events && response.sse_events.length > 0 && (
                <Section title={t('detailpanel.response_body') + ' (SSE Events)'}>
                  <SseEventList events={response.sse_events} />
                </Section>
              )}

              {/* Response Body */}
              <Section title={t('detailpanel.response_body')}>
                {response.response_body ? (
                  <JsonViewer data={response.response_body} maxHeight="500px" />
                ) : (
                  <p className="text-zinc-600 text-xs">{t('detailpanel.empty_body')}</p>
                )}
              </Section>
            </>
          ) : (
            <Section title={t('detailpanel.response')}>
              <p className="text-zinc-600 text-xs">{t('detailpanel.waiting')}</p>
            </Section>
          )}
        </div>
      )}
    </div>
  );
});

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border-b border-zinc-800">
      <div className="px-4 py-2">
        <h3 className="text-xs font-medium text-zinc-400 mb-2">{title}</h3>
        {children}
      </div>
    </div>
  );
}

function HeadersTable({ headers }: { headers: Record<string, string> }) {
  const entries = Object.entries(headers || {});
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-[11px]">
        <tbody>
          {entries.map(([key, value]) => (
            <tr key={key} className="border-b border-zinc-900 last:border-0">
              <td className="py-1.5 pr-4 text-zinc-500 font-bold whitespace-nowrap align-top uppercase tracking-wider">{key}</td>
              <td className="py-1.5 text-zinc-300 font-mono break-all">{value}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

