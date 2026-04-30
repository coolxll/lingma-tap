import { useState, useMemo } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { SSEEvent } from '@/lib/types';
import { JsonViewer } from './JsonViewer';

interface SseEventListProps {
  events: SSEEvent[];
}

type ViewMode = 'events' | 'complete';

/** Extract delta.content from SSE events and merge into full text. */
function mergeSSEContent(events: SSEEvent[]): { text: string; toolCalls: string[] } {
  let text = '';
  const toolCalls: string[] = [];

  for (const evt of events) {
    if (evt.event_type !== 'data' || !evt.body) continue;
    if (evt.body === '[DONE]') continue;

    try {
      const parsed = JSON.parse(evt.body);
      // OpenAI streaming: choices[0].delta.content
      const delta = parsed?.choices?.[0]?.delta;
      if (delta?.content) {
        text += delta.content;
      }
      // OpenAI streaming: choices[0].delta.tool_calls
      if (delta?.tool_calls) {
        for (const tc of delta.tool_calls) {
          const fn = tc.function;
          if (fn?.name) {
            toolCalls.push(`${fn.name}(${fn.arguments || ''})`);
          } else if (fn?.arguments) {
            // Append arguments to last tool call
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
  const [expandedIdx, setExpandedIdx] = useState<number | null>(null);
  const [viewMode, setViewMode] = useState<ViewMode>('events');

  const merged = useMemo(() => mergeSSEContent(events), [events]);

  if (!events || events.length === 0) {
    return <p className="text-zinc-500 text-xs">No SSE events</p>;
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
          Events
        </button>
        <button
          className={`px-2 py-1 text-[10px] font-medium rounded transition-colors ${
            viewMode === 'complete'
              ? 'bg-zinc-700 text-zinc-100'
              : 'text-zinc-500 hover:text-zinc-300 hover:bg-zinc-800'
          }`}
          onClick={() => setViewMode('complete')}
        >
          Complete
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
            <p className="text-zinc-500 text-xs">No text content in events</p>
          )}
          {merged.toolCalls.length > 0 && (
            <div className="mt-2">
              <h4 className="text-[10px] font-medium text-zinc-400 mb-1">Tool Calls</h4>
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
