import { useState, useMemo, useEffect, memo } from 'react';
import { Search, X, Activity, CheckCircle, Trash2, ChevronLeft, ChevronRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { TrafficRecord, formatTimestamp, getStatusColor } from '@/lib/types';
import { parseJSONMaybe } from '@/lib/utils';
import { JsonViewer } from './JsonViewer';
import { extractReadableResponseText } from '@/lib/sse-content';
import { extractReadablePromptText } from '@/lib/prompt-content';

interface GatewayMonitorProps {
  records: TrafficRecord[];
  onClear: () => void;
  loggingEnabled: boolean;
  onToggleLogging: () => void;
  onLoadMore?: () => void;
  canLoadMore?: boolean;
  getStats?: (timeRange: TimeRange, filter: string) => Promise<GatewayStats>;
}

interface GatewayStats {
  total: number;
  input_tokens?: number;
  output_tokens?: number;
  cached_tokens?: number;
  reasoning_tokens?: number;
  total_tokens?: number;
}

interface ProcessedRow {
  req: TrafficRecord;
  resp: TrafficRecord | null;
  timestampValid: boolean;
  details: {
    model: string;
    path: string;
    status: number;
    error: string;
    finishReason: string;
    inputTokens: number;
    outputTokens: number;
    cachedTokens: number;
    reasoningTokens: number;
    totalTokens: number;
    ttft: number;
    latency: number;
    speed: number;
  };
}

type ColumnKey =
  | 'time'
  | 'model'
  | 'endpoint'
  | 'result'
  | 'ttft'
  | 'latency'
  | 'speed'
  | 'input'
  | 'output'
  | 'reasoning'
  | 'cached'
  | 'totalTokens';

type TimeRange = 'all' | '1h' | 'today' | '7d' | '30d';

const DEFAULT_COLUMNS: ColumnKey[] = ['time', 'model', 'result', 'ttft', 'latency', 'speed', 'input', 'output', 'totalTokens'];
const ALL_COLUMNS: ColumnKey[] = ['time', 'model', 'endpoint', 'result', 'ttft', 'latency', 'speed', 'input', 'output', 'reasoning', 'cached', 'totalTokens'];

function formatMs(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '-';
  return `${Math.round(value)}ms`;
}

function formatTokens(value: number): string {
  return Number.isFinite(value) ? Math.round(value).toLocaleString() : '0';
}

function resultColor(status: number): string {
  const baseColor = getStatusColor(status);
  if (status >= 200 && status < 300) return `${baseColor} bg-green-500/10 border-green-500/20`;
  if (status >= 400) return `${baseColor} bg-red-500/10 border-red-500/20`;
  if (status > 0) return `${baseColor} bg-amber-500/10 border-amber-500/20`;
  return `${baseColor} bg-zinc-900 border-zinc-800`;
}

function shortenEndpoint(path: string): string {
  if (!path) return '-';
  if (path.includes('/v1/chat/completions')) return '/v1/chat/completions';
  if (path.includes('/v1/responses')) return '/v1/responses';
  if (path.includes('/v1/messages')) return '/v1/messages';
  return path.length > 28 ? `…${path.slice(-27)}` : path;
}

export const GatewayMonitor = memo(function GatewayMonitor({
  records,
  onClear,
  loggingEnabled,
  onToggleLogging,
  onLoadMore,
  canLoadMore,
  getStats
}: GatewayMonitorProps) {
  const { t } = useTranslation();
  const [filter, setFilter] = useState('');
  const [selectedRow, setSelectedRow] = useState<ProcessedRow | null>(null);
  const [currentPage, setCurrentPage] = useState(1);
  const [requestViewMode, setRequestViewMode] = useState<'friendly' | 'raw'>('friendly');
  const [responseViewMode, setResponseViewMode] = useState<'friendly' | 'raw'>('friendly');
  const [visibleColumns, setVisibleColumns] = useState<Set<ColumnKey>>(() => new Set(DEFAULT_COLUMNS));
  const [timeRange, setTimeRange] = useState<TimeRange>('all');
  const [aggregateStats, setAggregateStats] = useState<GatewayStats | null>(null);
  const PAGE_SIZE = 50;

  const processedRows = useMemo(() => {
    if (!records) return [];

    // 计算时间范围
    const now = Date.now();
    const startOfToday = new Date();
    startOfToday.setHours(0, 0, 0, 0);
    const timeLimits: Record<Exclude<typeof timeRange, 'all' | 'today'>, number> = {
      '1h': 3600_000,
      '7d': 7 * 24 * 3600_000,
      '30d': 30 * 24 * 3600_000,
    };

    const filtered = records
      .filter(r => r && r.source === 'gateway')
      .filter(row => {
        // 时间过滤：保留坏时间戳记录，但给它们打标，不参与范围计算
        if (timeRange !== 'all' && row.ts) {
          const rowTime = new Date(row.ts).getTime();
          if (!Number.isNaN(rowTime)) {
            if (timeRange === 'today') return rowTime >= startOfToday.getTime();
            const limit = timeLimits[timeRange];
            if (now - rowTime > limit) return false;
          }
        }
        return true;
      })
      .map(row => {
        const rowTime = new Date(row.ts).getTime();
        const timestampValid = !Number.isNaN(rowTime);
        const inputTokens = row.input_tokens || 0;
        const outputTokens = row.output_tokens || 0;
        const totalTokens = row.total_tokens || inputTokens + outputTokens;
        const latency = Number(row.latency) || 0;
        const ttft = Number(row.ttft) || 0;
        const generationMs = Math.max(latency - ttft, 1);
        const speed = outputTokens / Math.max(generationMs / 1000, 0.001);
        return {
          req: row,
          resp: row,
          timestampValid,
          details: {
            model: row.model || 'Unknown',
            path: row.path || '',
            status: row.status || 0,
            error: row.error || '',
            finishReason: row.finish_reason || '',
            inputTokens,
            outputTokens,
            cachedTokens: row.cached_tokens || 0,
            reasoningTokens: row.reasoning_tokens || 0,
            totalTokens,
            ttft,
            latency,
            speed
          }
        };
      })
      .filter(row => {
        if (!filter) return true;
        const search = filter.toLowerCase();
        return (
          (row.details.model || '').toLowerCase().includes(search) ||
          (row.details.path || '').toLowerCase().includes(search) ||
          (row.req.session && row.req.session.toLowerCase().includes(search)) ||
          (row.req.request_body && row.req.request_body.toLowerCase().includes(search))
        );
      });

    return filtered;
  }, [records, filter, timeRange]);

  const loadedStats = useMemo(() => {
    const total = processedRows.length;
    const inputTokens = processedRows.reduce((sum, r) => sum + r.details.inputTokens, 0);
    const outputTokens = processedRows.reduce((sum, r) => sum + r.details.outputTokens, 0);
    const cachedTokens = processedRows.reduce((sum, r) => sum + r.details.cachedTokens, 0);
    const totalTokens = processedRows.reduce((sum, r) => sum + r.details.totalTokens, 0);
    return { total, inputTokens, outputTokens, cachedTokens, totalTokens };
  }, [processedRows]);

  useEffect(() => {
    if (!getStats) {
      setAggregateStats(null);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(() => {
      getStats(timeRange, filter)
        .then(stats => {
          if (!cancelled) setAggregateStats(stats || null);
        })
        .catch(err => {
          console.error('Failed to load gateway stats:', err);
          if (!cancelled) setAggregateStats(null);
        });
    }, 1000);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [getStats, timeRange, filter, records.length]);

  const stats = useMemo(() => {
    if (!aggregateStats) return loadedStats;
    return {
      total: Number(aggregateStats.total) || 0,
      inputTokens: Number(aggregateStats.input_tokens) || 0,
      outputTokens: Number(aggregateStats.output_tokens) || 0,
      cachedTokens: Number(aggregateStats.cached_tokens) || 0,
      totalTokens: Number(aggregateStats.total_tokens) || 0,
    };
  }, [aggregateStats, loadedStats]);

  const totalPages = Math.ceil(processedRows.length / PAGE_SIZE);
  const paginatedRows = useMemo(() => {
    const start = (currentPage - 1) * PAGE_SIZE;
    return processedRows.slice(start, start + PAGE_SIZE);
  }, [processedRows, currentPage]);

  const visibleColumnList = useMemo(() => ALL_COLUMNS.filter(col => visibleColumns.has(col)), [visibleColumns]);

  useEffect(() => {
    setCurrentPage(1);
  }, [filter, timeRange]);

  const toggleColumn = (column: ColumnKey) => {
    setVisibleColumns(prev => {
      const next = new Set(prev);
      if (next.has(column)) {
        if (next.size > 1) next.delete(column);
      } else {
        next.add(column);
      }
      return next;
    });
  };

  const columnLabel = (column: ColumnKey): string => {
    const labels: Record<ColumnKey, string> = {
      time: t('monitor.table.time'),
      model: t('monitor.table.model'),
      endpoint: t('monitor.table.endpoint'),
      result: t('monitor.table.result'),
      ttft: t('monitor.table.ttft'),
      latency: t('monitor.table.total_latency'),
      speed: t('monitor.table.speed'),
      input: t('monitor.table.input'),
      output: t('monitor.table.output'),
      reasoning: t('monitor.table.reasoning'),
      cached: t('monitor.table.cached'),
      totalTokens: t('monitor.table.total_tokens')
    };
    return labels[column];
  };

  const renderCell = (column: ColumnKey, row: ProcessedRow) => {
    switch (column) {
      case 'time':
        return (
          <div className="flex items-center gap-2">
            <span className="text-[10px] text-zinc-500 font-medium whitespace-nowrap">
              {row.timestampValid ? formatTimestamp(row.req.ts) : (row.req.ts || '-')}
            </span>
            {!row.timestampValid && (
              <span className="inline-flex items-center rounded border border-red-500/25 bg-red-500/10 px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide text-red-300">
                invalid
              </span>
            )}
          </div>
        );
      case 'model':
        return (
          <div className="flex flex-col min-w-36">
            <span className="text-sm font-bold text-zinc-200 group-hover:text-blue-400 transition-colors truncate">{row.details.model}</span>
            <span className="text-[10px] text-zinc-600 font-mono">{(row.req.session || '').slice(0, 8)}...</span>
          </div>
        );
      case 'endpoint':
        return <span className="font-mono text-[11px] text-zinc-400 whitespace-nowrap">{shortenEndpoint(row.details.path)}</span>;
      case 'result': {
        const label = row.details.status > 0 ? String(row.details.status) : row.details.error ? 'ERR' : '-';
        return (
          <div className="flex flex-col gap-1 min-w-24">
            <span className={`w-fit px-2 py-1 rounded-lg border text-[10px] font-bold ${resultColor(row.details.status)}`}>{label}</span>
            <span className="text-[10px] text-zinc-600 truncate max-w-28" title={row.details.error || row.details.finishReason}>
              {row.details.error || row.details.finishReason || '-'}
            </span>
          </div>
        );
      }
      case 'ttft':
        return <span className="font-mono text-xs text-zinc-300 whitespace-nowrap">{formatMs(row.details.ttft)}</span>;
      case 'latency':
        return <span className={`font-mono text-xs font-bold whitespace-nowrap ${row.details.latency > 3000 ? 'text-amber-500' : 'text-zinc-300'}`}>{formatMs(row.details.latency)}</span>;
      case 'speed':
        return <span className="font-mono text-xs text-green-400 whitespace-nowrap">{row.details.outputTokens > 0 ? `${row.details.speed.toFixed(1)} tok/s` : '-'}</span>;
      case 'input':
        return <span className="font-mono text-xs text-zinc-400">{formatTokens(row.details.inputTokens)}</span>;
      case 'output':
        return <span className="font-mono text-xs font-bold text-blue-400">{formatTokens(row.details.outputTokens)}</span>;
      case 'reasoning':
        return <span className="font-mono text-xs text-purple-400">{formatTokens(row.details.reasoningTokens)}</span>;
      case 'cached':
        return <span className="font-mono text-xs text-emerald-400">{formatTokens(row.details.cachedTokens)}</span>;
      case 'totalTokens':
        return <span className="font-mono text-xs font-bold text-zinc-200">{formatTokens(row.details.totalTokens)}</span>;
      default:
        return null;
    }
  };

  const selectedDetails = selectedRow?.details;
  const selectedSpeed = selectedDetails ? selectedDetails.speed.toFixed(1) : '0.0';
  const selectedTotalTokens = selectedDetails?.totalTokens || 0;
  const selectedLatency = selectedDetails?.latency || 0;
  const selectedTTFT = selectedDetails?.ttft || 0;
  const ttftWidth = selectedLatency > 0 ? Math.min(100, Math.max(0, (selectedTTFT / selectedLatency) * 100)) : 0;

  return (
    <div className="h-full flex flex-col bg-zinc-950">
      {/* Toolbar */}
      <div className="flex items-center gap-4 px-6 py-3 bg-zinc-950 border-b border-zinc-900">
        <button
          onClick={onToggleLogging}
          className={`flex items-center gap-2 px-3 py-1.5 rounded-full text-[10px] font-bold uppercase transition-colors border shrink-0 ${loggingEnabled
            ? 'bg-green-500/10 text-green-400 border-green-500/20'
            : 'bg-zinc-900 text-zinc-500 border-zinc-800'
            }`}
        >
          <div className={`w-1.5 h-1.5 rounded-full ${loggingEnabled ? 'bg-green-500 animate-pulse' : 'bg-zinc-600'}`} />
          {loggingEnabled ? t('monitor.logging_status.active') : t('monitor.logging_status.paused')}
        </button>

        <div className="relative flex-1 min-w-48 max-w-md">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-zinc-600" />
          <input
            type="text"
            placeholder={t('monitor.search_placeholder')}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-full bg-zinc-900 border border-zinc-800 rounded-xl pl-10 pr-4 py-2 text-sm text-zinc-300 focus:outline-none focus:border-zinc-700 transition-colors shadow-inner"
          />
        </div>

        <div className="flex gap-1 items-center shrink-0">
          {(['all', '1h', 'today', '7d', '30d'] as const).map((range) => (
            <button
              key={range}
              onClick={() => setTimeRange(range)}
              className={`px-3 py-1.5 rounded-lg text-[10px] font-bold transition-colors ${
                timeRange === range
                  ? 'bg-blue-500/20 text-blue-300 border border-blue-500/30'
                  : 'bg-zinc-900/50 text-zinc-500 border border-zinc-800 hover:text-zinc-300'
              }`}
            >
              {t(`monitor.time_filter.${range}`)}
            </button>
          ))}
        </div>

        <div className="w-px h-6 bg-zinc-800" />
        <button
          onClick={onClear}
          className="p-2 text-zinc-500 hover:text-red-400 hover:bg-red-400/10 rounded-full transition-colors shrink-0"
          title={t('common.clear')}
        >
          <Trash2 className="w-5 h-5" />
        </button>
      </div>

      {/* Stats Overview */}
      <div className="flex flex-wrap items-center gap-3 px-6 py-3 bg-zinc-950 border-b border-zinc-900">
        <StatCard
          label={t('monitor.stats.tokens')}
          value={stats.totalTokens.toLocaleString()}
          color="zinc"
        />
        <StatCard
          label={t('monitor.table.input')}
          value={stats.inputTokens.toLocaleString()}
          color="blue"
        />
        <StatCard
          label={t('monitor.table.output')}
          value={stats.outputTokens.toLocaleString()}
          color="purple"
        />
        <StatCard
          label={t('monitor.table.cached')}
          value={stats.cachedTokens.toLocaleString()}
          color="emerald"
        />
        <StatCard
          label={t('monitor.stats.recent')}
          value={String(stats.total)}
          color="green"
        />
      </div>

      {/* Column Selector */}
      <div className="flex flex-wrap items-center gap-2 px-6 py-2.5 bg-zinc-950 border-b border-zinc-900">
        <span className="text-[10px] font-bold text-zinc-600 uppercase tracking-widest mr-1">{t('monitor.table.columns')}</span>
        {ALL_COLUMNS.map(column => (
          <button
            key={column}
            type="button"
            onClick={() => toggleColumn(column)}
            className={`px-2.5 py-1 rounded-lg border text-[10px] font-bold transition-colors ${visibleColumns.has(column)
              ? 'bg-blue-500/10 text-blue-300 border-blue-500/30'
              : 'bg-zinc-900/50 text-zinc-500 border-zinc-800 hover:text-zinc-300'
              }`}
          >
            {columnLabel(column)}
          </button>
        ))}
      </div>

      <div className="flex-1 overflow-auto">
        <table className="min-w-[1120px] w-full border-collapse text-left">
          <thead className="sticky top-0 z-10 bg-zinc-950">
            <tr className="border-b border-zinc-900">
              {visibleColumnList.map(column => (
                <th
                  key={column}
                  className={`px-4 py-4 text-[10px] font-bold text-zinc-600 uppercase tracking-widest ${['ttft', 'latency', 'speed', 'input', 'output', 'reasoning', 'cached', 'totalTokens'].includes(column) ? 'text-right' : ''}`}
                >
                  {columnLabel(column)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {paginatedRows.map((row, idx) => (
              <tr
                key={row.req.id || idx}
                onClick={() => setSelectedRow(row)}
                className="group border-b border-zinc-900/50 hover:bg-zinc-900/30 cursor-pointer transition-colors"
              >
                {visibleColumnList.map(column => (
                  <td
                    key={column}
                    className={`px-4 py-4 ${['ttft', 'latency', 'speed', 'input', 'output', 'reasoning', 'cached', 'totalTokens'].includes(column) ? 'text-right' : ''}`}
                  >
                    {renderCell(column, row)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>

        {processedRows.length === 0 && (
          <div className="h-64 flex flex-col items-center justify-center text-zinc-600 gap-4">
            <div className="w-12 h-12 bg-zinc-900 rounded-full flex items-center justify-center opacity-20">
              <Activity className="w-6 h-6" />
            </div>
            <span className="text-sm italic">{t('recordlist.no_records')}</span>
          </div>
        )}
      </div>

      {totalPages > 1 && (
        <div className="px-6 py-3 bg-zinc-950 border-t border-zinc-900 flex items-center justify-between">
          <div className="text-[10px] text-zinc-500 font-bold uppercase tracking-widest">
            Showing {((currentPage - 1) * PAGE_SIZE) + 1} to {Math.min(currentPage * PAGE_SIZE, processedRows.length)} of {processedRows.length}
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setCurrentPage(p => Math.max(1, p - 1))}
              disabled={currentPage === 1}
              className="p-1.5 bg-zinc-900 border border-zinc-800 rounded-lg text-zinc-400 hover:text-zinc-200 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
            >
              <ChevronLeft className="w-4 h-4" />
            </button>
            <div className="px-3 py-1 bg-zinc-900/50 border border-zinc-800 rounded-lg text-xs font-mono text-blue-400 font-bold">
              {currentPage} / {totalPages}
            </div>
            <button
              onClick={() => setCurrentPage(p => Math.min(totalPages, p + 1))}
              disabled={currentPage === totalPages}
              className="p-1.5 bg-zinc-900 border border-zinc-800 rounded-lg text-zinc-400 hover:text-zinc-200 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
            >
              <ChevronRight className="w-4 h-4" />
            </button>
            {canLoadMore && onLoadMore && (
              <button
                onClick={onLoadMore}
                className="ml-4 px-3 py-1.5 bg-blue-500/10 text-blue-400 border border-blue-500/20 rounded-lg text-xs font-bold hover:bg-blue-500/20 transition-colors"
              >
                {t('recordlist.load_more')}
              </button>
            )}
          </div>
        </div>
      )}

      {selectedRow && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-6 animate-in fade-in duration-200">
          <div className="bg-zinc-950 border border-zinc-800 rounded-3xl shadow-2xl w-full max-w-6xl max-h-[90vh] flex flex-col overflow-hidden">
            <div className="px-6 py-4 border-b border-zinc-900 flex items-center justify-between bg-zinc-900/30">
              <div className="flex flex-col gap-1">
                <div className="flex items-center gap-3">
                  <span className="px-2 py-0.5 rounded bg-blue-500/10 text-blue-400 text-[10px] font-bold uppercase tracking-widest border border-blue-500/20">
                    {selectedRow.req.model || 'Unknown Model'}
                  </span>
                  <span className="text-xs text-zinc-500 font-mono">{selectedRow.req.session}</span>
                </div>
              <h2 className="text-lg font-bold text-zinc-100">
                {shortenEndpoint(selectedRow.req.path)} · {selectedRow.timestampValid ? formatTimestamp(selectedRow.req.ts) : (selectedRow.req.ts || '-')}
              </h2>
              {!selectedRow.timestampValid && (
                <span className="inline-flex items-center rounded border border-red-500/25 bg-red-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-red-300">
                  invalid timestamp
                </span>
              )}
            </div>
              <button
                onClick={() => setSelectedRow(null)}
                className="p-2 rounded-full hover:bg-zinc-800 text-zinc-500 hover:text-zinc-200 transition-colors"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            <div className="flex-1 overflow-auto p-6 space-y-6">
              <div className="grid grid-cols-4 gap-4">
                <MetricCard
                  label="Provider Latency"
                  value={formatMs(selectedRow.req.latency || 0)}
                  subValue={`TTFT ${formatMs(selectedRow.req.ttft || 0)}`}
                  icon={<Activity className="w-4 h-4" />}
                  color="blue"
                />
                <MetricCard
                  label="Throughput"
                  value={selectedSpeed}
                  unit="tok/s"
                  subValue="Generation speed"
                  icon={<Activity className="w-4 h-4" />}
                  color="green"
                />
                <MetricCard
                  label="Tokens"
                  value={`${selectedTotalTokens}`}
                  subValue={`${selectedRow.req.input_tokens || 0} → ${selectedRow.req.output_tokens || 0} · cache ${selectedRow.req.cached_tokens || 0} · reasoning ${selectedRow.req.reasoning_tokens || 0}`}
                  icon={<Activity className="w-4 h-4" />}
                  color="purple"
                />
                <MetricCard
                  label="Finish Reason"
                  value={selectedRow.req.finish_reason || (selectedRow.req.status >= 400 ? 'error' : 'stop')}
                  subValue={selectedRow.req.is_sse ? 'Streaming' : 'Non-streaming'}
                  icon={<CheckCircle className="w-4 h-4" />}
                  color="amber"
                />
              </div>

              <div className="bg-zinc-900/50 p-4 rounded-xl border border-zinc-800/50">
                <div className="flex justify-between items-center mb-2 text-[10px] font-bold text-zinc-500 uppercase tracking-widest">
                  <span>Latency Timeline</span>
                  <span>Total: {formatMs(selectedLatency)}</span>
                </div>
                <div className="h-2 w-full bg-zinc-800 rounded-full overflow-hidden flex">
                  <div
                    className="h-full bg-blue-500/80 transition-[width] duration-500"
                    style={{ width: `${ttftWidth}%` }}
                    title="TTFT"
                  />
                  <div
                    className="h-full bg-blue-400/40 transition-[width] duration-500"
                    style={{ width: `${Math.max(0, 100 - ttftWidth)}%` }}
                    title="Generation"
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 gap-6">
                <div className="flex flex-col gap-3">
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-bold text-zinc-400 uppercase tracking-widest flex items-center gap-2">
                      <div className="w-1 h-3 bg-blue-500 rounded-full" />
                      Prompt (Last Message)
                    </span>
                    <div className="flex bg-zinc-900/50 rounded-lg p-0.5 border border-zinc-800">
                      <button
                        onClick={() => setRequestViewMode('friendly')}
                        className={`px-3 py-1 text-[10px] font-bold rounded-md transition-colors ${requestViewMode === 'friendly'
                          ? 'bg-zinc-800 text-blue-400 shadow-sm'
                          : 'text-zinc-500 hover:text-zinc-400'
                          }`}
                      >
                        FRIENDLY
                      </button>
                      <button
                        onClick={() => setRequestViewMode('raw')}
                        className={`px-3 py-1 text-[10px] font-bold rounded-md transition-colors ${requestViewMode === 'raw'
                          ? 'bg-zinc-800 text-blue-400 shadow-sm'
                          : 'text-zinc-500 hover:text-zinc-400'
                          }`}
                      >
                        RAW
                      </button>
                    </div>
                  </div>
                  <div className="bg-zinc-900/30 border border-zinc-800/50 rounded-xl p-4 text-sm text-zinc-300 min-h-[200px] font-sans leading-relaxed overflow-auto">
                    {requestViewMode === 'friendly' ? (
                      extractReadablePromptText(selectedRow.req.request_body) || 'No prompt content'
                    ) : (
                      <JsonViewer data={selectedRow.req.request_body || '{}'} />
                    )}
                  </div>
                </div>

                <div className="flex flex-col gap-3">
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-bold text-zinc-400 uppercase tracking-widest flex items-center gap-2">
                      <div className="w-1 h-3 bg-green-500 rounded-full" />
                      Assistant Response
                    </span>
                    <div className="flex bg-zinc-900/50 rounded-lg p-0.5 border border-zinc-800">
                      <button
                        onClick={() => setResponseViewMode('friendly')}
                        className={`px-3 py-1 text-[10px] font-bold rounded-md transition-colors ${responseViewMode === 'friendly'
                          ? 'bg-zinc-800 text-green-400 shadow-sm'
                          : 'text-zinc-500 hover:text-zinc-400'
                          }`}
                      >
                        FRIENDLY
                      </button>
                      <button
                        onClick={() => setResponseViewMode('raw')}
                        className={`px-3 py-1 text-[10px] font-bold rounded-md transition-colors ${responseViewMode === 'raw'
                          ? 'bg-zinc-800 text-blue-400 shadow-sm'
                          : 'text-zinc-500 hover:text-zinc-400'
                          }`}
                      >
                        RAW
                      </button>
                    </div>
                  </div>
                  <div className="bg-zinc-900/30 border border-zinc-800/50 rounded-xl p-4 text-sm text-zinc-200 min-h-[200px] font-sans leading-relaxed overflow-auto">
                    {responseViewMode === 'friendly' ? (
                      extractReadableResponseText(selectedRow.req.response_body) || 'Waiting for response...'
                    ) : (
                      <JsonViewer data={selectedRow.req.response_body || '{}'} />
                    )}
                  </div>
                </div>
              </div>

              <details className="group border border-zinc-800/50 rounded-xl overflow-hidden">
                <summary className="flex items-center justify-between px-4 py-3 bg-zinc-900/20 cursor-pointer hover:bg-zinc-900/40 transition-colors">
                  <span className="text-xs font-bold text-zinc-500 uppercase tracking-widest">Metadata & Raw JSON</span>
                  <div className="text-zinc-500 group-open:rotate-180 transition-transform text-xs">▼</div>
                </summary>
                <div className="p-4 bg-zinc-950">
                  <JsonViewer data={JSON.stringify({
                    model: selectedRow.req.model,
                    endpoint: selectedRow.req.path,
                    status: selectedRow.req.status,
                    latency: selectedRow.req.latency,
                    ttft: selectedRow.req.ttft,
                    usage: {
                      input: selectedRow.req.input_tokens,
                      output: selectedRow.req.output_tokens,
                      cached: selectedRow.req.cached_tokens,
                      reasoning: selectedRow.req.reasoning_tokens,
                      total: selectedTotalTokens
                    },
                    finish_reason: selectedRow.req.finish_reason,
                    raw_request: parseJSONMaybe(selectedRow.req.request_body || ''),
                    raw_response: parseJSONMaybe(selectedRow.req.response_body || '')
                  }, null, 2)} />
                </div>
              </details>
            </div>
          </div>
        </div>
      )}
    </div>
  );
});

function StatCard({ label, value, color }: {
  label: string;
  value: string;
  color: 'zinc' | 'blue' | 'purple' | 'emerald' | 'green';
}) {
  const colors = {
    zinc: { border: 'border-zinc-700/50', labelText: 'text-zinc-500', valueText: 'text-zinc-200' },
    blue: { border: 'border-blue-500/20', labelText: 'text-blue-400', valueText: 'text-blue-200' },
    purple: { border: 'border-purple-500/20', labelText: 'text-purple-400', valueText: 'text-purple-200' },
    emerald: { border: 'border-emerald-500/20', labelText: 'text-emerald-400', valueText: 'text-emerald-200' },
    green: { border: 'border-green-500/20', labelText: 'text-green-400', valueText: 'text-green-200' },
  };
  const c = colors[color];
  return (
    <div className={`flex items-center gap-2 px-3 py-1.5 bg-zinc-900/30 border ${c.border} rounded-lg`}>
      <span className={`text-[10px] font-bold uppercase tracking-wider ${c.labelText}`}>{label}</span>
      <span className={`text-xs font-mono font-bold ${c.valueText}`}>{value}</span>
    </div>
  );
}

function MetricCard({ label, value, unit, subValue, icon, color }: {
  label: string;
  value: string;
  unit?: string;
  subValue: string;
  icon: React.ReactNode;
  color: 'blue' | 'green' | 'purple' | 'amber';
}) {
  const colors = {
    blue: 'text-blue-400 bg-blue-400/10 border-blue-400/20',
    green: 'text-green-400 bg-green-400/10 border-green-400/20',
    purple: 'text-purple-400 bg-purple-400/10 border-purple-400/20',
    amber: 'text-amber-400 bg-amber-400/10 border-amber-400/20',
  };

  return (
    <div className={`p-4 rounded-2xl border ${colors[color]} bg-zinc-900/20 flex flex-col gap-1`}>
      <div className="flex items-center justify-between mb-1">
        <span className="text-[10px] font-bold uppercase tracking-wider opacity-60">{label}</span>
        <div className="opacity-40">{icon}</div>
      </div>
      <div className="flex items-baseline gap-1">
        <span className="text-xl font-bold tracking-tight">{value}</span>
        {unit && <span className="text-xs opacity-60 font-medium">{unit}</span>}
      </div>
      <span className="text-[10px] opacity-40 font-medium truncate">{subValue}</span>
    </div>
  );
}
