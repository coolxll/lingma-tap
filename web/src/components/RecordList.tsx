import { useState, useMemo, useRef, useEffect, memo, useCallback } from 'react';
import { TrafficRecord, recordKey, formatTimestamp, getEndpointLabel, getStatusColor } from '@/lib/types';
import { Search, Lock, ChevronRight, ChevronDown } from 'lucide-react';
import { useTranslation } from 'react-i18next';

interface RecordListProps {
  records: TrafficRecord[];
  selectedRecord: TrafficRecord | null;
  onSelectRecord: (record: TrafficRecord) => void;
  onLoadMore?: () => void;
  canLoadMore?: boolean;
  liveTail?: boolean;
  typeFilter?: 'all' | 'chat' | 'embedding' | 'other';
  onTypeFilterChange?: (filter: 'all' | 'chat' | 'embedding' | 'other') => void;
}

interface RequestPair {
  req: TrafficRecord;
  resp?: TrafficRecord;
}

interface SessionGroup {
  session: string;
  pairs: RequestPair[];
}

/** Group records by session, pair each request with its response. */
function groupBySession(records: TrafficRecord[]): SessionGroup[] {
  const sessionMap = new Map<string, TrafficRecord[]>();
  for (const rec of records) {
    const list = sessionMap.get(rec.session) || [];
    list.push(rec);
    sessionMap.set(rec.session, list);
  }

  const result: SessionGroup[] = [];
  for (const [session, recs] of sessionMap) {
    const pairs: RequestPair[] = [];
    // Sort by index ascending to ensure C2S comes before S2C
    const sortedRecs = [...recs].sort((a, b) => a.index - b.index);
    for (const rec of sortedRecs) {
      if (rec.direction === 'C2S') {
        pairs.push({ req: rec });
      } else if (pairs.length > 0) {
        pairs[pairs.length - 1].resp = rec;
      } else {
        pairs.push({ req: rec });
      }
    }
    result.push({ session, pairs });
  }
  return result;
}

/** Key for a pair (uses request's key). */
function pairKey(p: RequestPair): string {
  return recordKey(p.req);
}

export const RecordList = memo(function RecordList({
  records,
  selectedRecord,
  onSelectRecord,
  onLoadMore,
  canLoadMore,
  liveTail,
  typeFilter = 'all',
  onTypeFilterChange,
}: RecordListProps) {
  const { t } = useTranslation();
  const [localSearch, setLocalSearch] = useState('');
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const scrollRef = useRef<HTMLDivElement>(null);
  const selectedRef = useRef<HTMLButtonElement>(null);

  const filteredRecords = useMemo(() => {
    if (!localSearch.trim()) return records;
    const q = localSearch.toLowerCase();
    return records.filter((r) => {
      const text = [r.path, r.host, r.request_body, r.response_body]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
      return text.includes(q);
    });
  }, [records, localSearch]);

  const groups = useMemo(() => groupBySession(filteredRecords), [filteredRecords]);

  const toggleCollapse = useCallback((session: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(session)) next.delete(session);
      else next.add(session);
      return next;
    });
  }, []);

  // Auto-scroll to selected
  useEffect(() => {
    selectedRef.current?.scrollIntoView({ block: 'nearest' });
  }, [selectedRecord]);

  // Auto-scroll to top on new records if live tail is on
  useEffect(() => {
    if (scrollRef.current && liveTail && !localSearch) {
      scrollRef.current.scrollTop = 0;
    }
  }, [records.length, liveTail, localSearch]);

  // Which pair is selected?
  const selectedSession = selectedRecord?.session;
  const selectedPairIdx = selectedRecord
    ? groups
        .find((g) => g.session === selectedRecord.session)
        ?.pairs.findIndex(
          (p) => recordKey(p.req) === recordKey(selectedRecord) || (p.resp && recordKey(p.resp) === recordKey(selectedRecord)),
        ) ?? -1
    : -1;

  return (
    <div className="h-full flex flex-col bg-zinc-950">
      {/* Search */}
      <div className="px-2 py-1.5 border-b border-zinc-800 shrink-0 space-y-2">
        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 w-3 h-3 text-zinc-500" />
          <input
            type="text"
            value={localSearch}
            onChange={(e) => setLocalSearch(e.target.value)}
            placeholder={t('recordlist.filter')}
            className="w-full pl-7 pr-2 py-1 bg-zinc-900 border border-zinc-800 rounded text-xs text-zinc-200 placeholder:text-zinc-600 focus:outline-none focus:border-zinc-600"
          />
        </div>

        {/* Type Filter */}
        <div className="flex gap-1">
          {(['all', 'chat', 'embedding', 'other'] as const).map((type) => (
            <button
              key={type}
              onClick={() => onTypeFilterChange?.(type)}
              className={`px-2 py-0.5 rounded text-[10px] font-medium transition-colors ${
                typeFilter === type
                  ? 'bg-blue-600 text-white'
                  : 'bg-zinc-900 text-zinc-500 hover:bg-zinc-800 hover:text-zinc-300'
              }`}
            >
              {type === 'all' ? t('common.all') : getEndpointLabel(type)}
            </button>
          ))}
        </div>
      </div>

      {/* List */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto">
        {groups.length === 0 ? (
          <div className="flex items-center justify-center h-full text-zinc-600 text-xs">
            {records.length === 0 ? t('recordlist.no_records') : t('recordlist.no_matches')}
          </div>
        ) : (
          groups.map((group) => {
            const isSessionCollapsed = collapsed.has(group.session);
            const hasMultiple = group.pairs.length > 1;
            const isSessionSelected = selectedSession === group.session;

            return (
              <div key={group.session}>
                {/* Session header (shown only when multiple pairs) */}
                {hasMultiple && (
                  <button
                    onClick={() => toggleCollapse(group.session)}
                    className={`w-full text-left px-2 py-1 text-[11px] border-b border-zinc-800 transition-colors ${
                      isSessionSelected
                        ? 'bg-blue-950/40 text-blue-300'
                        : 'bg-zinc-900/30 text-zinc-500 hover:bg-zinc-900/50'
                    }`}
                  >
                    <div className="flex items-center gap-1.5">
                      {isSessionCollapsed ? (
                        <ChevronRight className="w-3 h-3 shrink-0" />
                      ) : (
                        <ChevronDown className="w-3 h-3 shrink-0" />
                      )}
                      <span className="text-zinc-600">{t('recordlist.session')}</span>
                      <span className="text-zinc-500 font-mono">{(group.session || '').slice(0, 8)}</span>
                      <span className="text-zinc-600 ml-auto">{group.pairs.length} {t('recordlist.requests')}</span>
                    </div>
                  </button>
                )}

                {/* Pairs */}
                {!isSessionCollapsed &&
                  group.pairs.map((pair, i) => {
                    const key = pairKey(pair);
                    const isSelected =
                      selectedSession === group.session && selectedPairIdx === i;

                    return (
                      <button
                        key={key}
                        ref={isSelected ? selectedRef : undefined}
                        onClick={() => onSelectRecord(pair.req)}
                        className={`w-full text-left px-2 py-1.5 text-xs border-b border-zinc-900 transition-colors ${
                          hasMultiple ? 'pl-6' : ''
                        } ${
                          isSelected
                            ? 'bg-blue-900/30 border-l-2 border-l-blue-500'
                            : 'hover:bg-zinc-900/50 border-l-2 border-l-transparent'
                        }`}
                      >
                        <div className="flex items-center gap-1.5">
                          {/* Index */}
                          <span className="text-zinc-600 w-5 text-right shrink-0">
                            {i + 1}
                          </span>

                          {/* Method badge */}
                          <span className="px-1 py-0.5 rounded text-[10px] font-medium bg-blue-900/50 text-blue-400 shrink-0">
                            {pair.req.method || '—'}
                          </span>

                          {/* Endpoint type */}
                          <span className={`text-[10px] font-medium shrink-0 ${getEndpointColor(pair.req.endpoint_type)}`}>
                            {getEndpointLabel(pair.req.endpoint_type)}
                          </span>

                          {/* Path */}
                          <span className="text-zinc-300 truncate flex-1">
                            {shortPath(pair.req.path)}
                          </span>

                          {/* Encoded indicator */}
                          {pair.req.is_encoded && <Lock className="w-3 h-3 text-amber-400 shrink-0" />}

                          {/* SSE indicator */}
                          {pair.resp?.is_sse && (
                            <span className="px-1 py-0.5 rounded text-[10px] bg-purple-900/50 text-purple-400 shrink-0">
                              SSE
                            </span>
                          )}

                          {/* Status */}
                          {pair.resp && pair.resp.status > 0 && (
                            <span className={`text-[10px] font-mono shrink-0 ${getStatusColor(pair.resp.status)}`}>
                              {pair.resp.status}
                            </span>
                          )}

                          {/* Host */}
                          <span className="text-zinc-600 text-[10px] shrink-0 max-w-[180px] truncate">
                            {pair.req.host}
                          </span>

                          {/* Timestamp */}
                          <span className="text-zinc-600 text-[10px] shrink-0">
                            {formatTimestamp(pair.req.ts)}
                          </span>
                        </div>
                      </button>
                    );
                  })}
              </div>
            );
          })
        )}

        {/* Load More */}
        {onLoadMore && canLoadMore && (
          <div className="p-4 border-t border-zinc-900 flex justify-center">
            <button
              onClick={onLoadMore}
              className="px-4 py-1.5 bg-zinc-900 hover:bg-zinc-800 text-zinc-400 hover:text-zinc-200 text-xs rounded transition-colors"
            >
              {t('recordlist.load_more')}
            </button>
          </div>
        )}
      </div>

      {/* Count */}
      <div className="px-2 py-1 border-t border-zinc-800 text-[10px] text-zinc-600 shrink-0">
        {filteredRecords.length} / {records.length} {t('recordlist.records_count')}
      </div>
    </div>
  );
});

function shortPath(path: string): string {
  if (!path) return '—';
  const parts = path.split('/');
  if (parts.length <= 3) return path;
  return '.../' + parts.slice(-2).join('/');
}

function getEndpointColor(endpoint: string): string {
  switch (endpoint) {
    case 'chat':
      return 'text-blue-400';
    case 'finish':
      return 'text-green-400';
    case 'embedding':
      return 'text-purple-400';
    case 'tracking':
      return 'text-yellow-400';
    default:
      return 'text-zinc-500';
  }
}
