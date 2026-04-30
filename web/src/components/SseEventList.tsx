import { useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { SSEEvent } from '@/lib/types';
import { JsonViewer } from './JsonViewer';

interface SseEventListProps {
  events: SSEEvent[];
}

export function SseEventList({ events }: SseEventListProps) {
  const [expandedIdx, setExpandedIdx] = useState<number | null>(null);

  if (!events || events.length === 0) {
    return <p className="text-zinc-500 text-xs">No SSE events</p>;
  }

  return (
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
  );
}
