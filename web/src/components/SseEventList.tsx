import { useState, useMemo } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { SSEEvent } from '@/lib/types';
import { JsonViewer } from './JsonViewer';
import { useTranslation } from 'react-i18next';

interface SseEventListProps {
  events: SSEEvent[];
}

type ViewMode = 'events' | 'complete';

/** Extract content from SSE events and merge into full text. */
function mergeSSEContent(events: SSEEvent[]): { text: string; toolCalls: string[] } {
  let text = '';
  const toolCalls: string[] = [];

  for (const evt of events) {
    // SSE event_type defaults to 'message' or 'data' if not specified
    const isDataEvent = !evt.event_type || evt.event_type === 'data' || evt.event_type === 'message';
    const rawContent = evt.body || evt.data;
    
    if (!isDataEvent || !rawContent) continue;
    if (rawContent === '[DONE]') continue;

    try {
      const parsed = JSON.parse(rawContent);
      
      // 1. OpenAI style: choices[0].delta.content
      // 2. DashScope style: output.choices[0].delta.content
      const delta = parsed?.choices?.[0]?.delta || parsed?.output?.choices?.[0]?.delta;
      
      // Use undefined check because content can be an empty string
      if (delta?.content !== undefined && delta?.content !== null) {
        text += delta.content;
      } else if (parsed?.output?.text !== undefined && parsed?.output?.text !== null) {
        // 3. DashScope older style: output.text (usually contains full text so far)
        text = parsed.output.text; 
      } else if (parsed?.content !== undefined && parsed?.content !== null) {
        // 4. Simple top-level content
        text += parsed.content;
      }

      // Tool calls extraction
      const tool_calls = delta?.tool_calls || parsed?.choices?.[0]?.message?.tool_calls;
      if (tool_calls) {
        for (const tc of tool_calls) {
          const fn = tc.function;
          if (fn?.name) {
            toolCalls.push(`${fn.name}(${fn.arguments || ''})`);
          } else if (fn?.arguments) {
            if (toolCalls.length > 0) {
              toolCalls[toolCalls.length - 1] += fn.arguments;
            }
          }
        }
      }
    } catch {
      // Not valid JSON, skip
    }
  }

  return { text, toolCalls };
}

export function SseEventList({ events }: SseEventListProps) {
  const { t } = useTranslation();
  const [expandedIdx, setExpandedIdx] = useState<number | null>(null);
  const [viewMode, setViewMode] = useState<ViewMode>('events');

  const merged = useMemo(() => mergeSSEContent(events), [events]);

  if (!events || events.length === 0) {
    return <p className="text-zinc-500 text-xs">{t('sse.no_events')}</p>;
  }

  return (
    <div>
      {/* View mode tabs */}
      <div className="flex gap-1 mb-2">
        <button
          className={`px-2 py-1 text-[10px] font-medium rounded transition-colors ${
            viewMode === 'events'
              ? 'bg-zinc-700 text-zinc-100'
              : 'text-zinc-500 hover:text-zinc-300 hover:bg-zinc-800'
          }`}
          onClick={() => setViewMode('events')}
        >
          {t('sse.events_tab')}
        </button>
        <button
          className={`px-2 py-1 text-[10px] font-medium rounded transition-colors ${
            viewMode === 'complete'
              ? 'bg-zinc-700 text-zinc-100'
              : 'text-zinc-500 hover:text-zinc-300 hover:bg-zinc-800'
          }`}
          onClick={() => setViewMode('complete')}
        >
          {t('sse.complete_tab')}
        </button>
      </div>

      {/* Events view */}
      {viewMode === 'events' && (
        <div className="space-y-1">
          {events.map((evt, idx) => {
            const isExpanded = expandedIdx === idx;
            const isFinish = evt.event_type === 'finish';
            const isDone = evt.body === '[DONE]';

            return (
              <div key={idx} className="border border-zinc-800 rounded">
                <button
                  className="w-full flex items-center gap-2 px-2 py-1.5 text-xs hover:bg-zinc-800/50 transition-colors"
                  onClick={() => setExpandedIdx(isExpanded ? null : idx)}
                >
                  {isExpanded ? (
                    <ChevronDown className="w-3 h-3 text-zinc-500 shrink-0" />
                  ) : (
                    <ChevronRight className="w-3 h-3 text-zinc-500 shrink-0" />
                  )}
                  <span
                    className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${
                      isFinish
                        ? 'bg-amber-900/50 text-amber-400'
                        : isDone
                          ? 'bg-zinc-800 text-zinc-500'
                          : 'bg-blue-900/50 text-blue-400'
                    }`}
                  >
                    {evt.event_type || 'data'}
                  </span>
                  <span className="text-zinc-500 truncate">
                    {isDone ? '[DONE]' : evt.body ? evt.body.slice(0, 80) + (evt.body.length > 80 ? '...' : '') : evt.data.slice(0, 80)}
                  </span>
                </button>
                {isExpanded && (
                  <div className="px-2 pb-2">
                    {evt.body ? (
                      <JsonViewer data={evt.body} maxHeight="300px" />
                    ) : (
                      <pre className="p-2 bg-zinc-900 rounded text-xs font-mono text-zinc-300 overflow-x-auto max-h-[300px]">
                        {evt.data}
                      </pre>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* Complete view */}
      {viewMode === 'complete' && (
        <div>
          {merged.text ? (
            <pre className="p-3 bg-zinc-900 rounded text-xs font-mono text-zinc-200 whitespace-pre-wrap break-words max-h-[500px] overflow-y-auto">
              {merged.text}
            </pre>
          ) : (
            <p className="text-zinc-500 text-xs">{t('sse.no_content')}</p>
          )}
          {merged.toolCalls.length > 0 && (
            <div className="mt-2">
              <h4 className="text-[10px] font-medium text-zinc-400 mb-1">{t('sse.tool_calls')}</h4>
              {merged.toolCalls.map((tc, i) => (
                <pre key={i} className="p-2 bg-zinc-900 rounded text-xs font-mono text-amber-300 whitespace-pre-wrap break-all mb-1">
                  {tc}
                </pre>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
