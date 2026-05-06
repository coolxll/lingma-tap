import { useState, useMemo, useEffect } from 'react';
import { Search, X, Activity, CheckCircle, Trash2, ChevronLeft, ChevronRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { TrafficRecord, formatTimestamp, formatFriendlyMessage } from '@/lib/types';
import { JsonViewer } from './JsonViewer';

interface GatewayMonitorProps {
  records: TrafficRecord[];
  onClear: () => void;
  loggingEnabled: boolean;
  onToggleLogging: () => void;
}

export function GatewayMonitor({ 
  records, 
  onClear, 
  loggingEnabled, 
  onToggleLogging 
}: GatewayMonitorProps) {
  const { t } = useTranslation();
  const [filter, setFilter] = useState('');
  const [selectedRow, setSelectedRow] = useState<{ req: TrafficRecord; resp: TrafficRecord | null } | null>(null);
  const [currentPage, setCurrentPage] = useState(1);
  const [requestViewMode, setRequestViewMode] = useState<'friendly' | 'raw'>('friendly');
  const [responseViewMode, setResponseViewMode] = useState<'friendly' | 'raw'>('friendly');
  const PAGE_SIZE = 50;

  // Filter and Limit to 500
  const processedRows = useMemo(() => {
    if (!records) return [];
    const filtered = records
      .filter(r => r && r.source === 'gateway')
      .map(row => ({ 
        req: row, 
        resp: row,
        details: {
          model: row.model || 'Unknown',
          inputTokens: row.input_tokens || 0,
          outputTokens: row.output_tokens || 0,
          latency: Number(row.latency) || 0
        }
      }))
      .filter(row => {
        if (!filter) return true;
        const search = filter.toLowerCase();
        return (
          (row.details.model || '').toLowerCase().includes(search) ||
          (row.req.session && row.req.session.toLowerCase().includes(search)) ||
          (row.req.request_body && row.req.request_body.toLowerCase().includes(search))
        );
      });
    
    // Hard limit to 500 most recent
    return filtered.slice(0, 500);
  }, [records, filter]);

  // Statistics
  const stats = useMemo(() => {
    const total = processedRows.length;
    const ok = processedRows.filter(r => r.resp && r.resp.status >= 200 && r.resp.status < 300).length;
    const err = processedRows.filter(r => r.resp && r.resp.status >= 400).length;
    const inputTokens = processedRows.reduce((sum, r) => sum + (r.req.input_tokens || 0), 0);
    const outputTokens = processedRows.reduce((sum, r) => sum + (r.req.output_tokens || 0), 0);
    return { total, ok, err, inputTokens, outputTokens };
  }, [processedRows]);

  // Pagination logic
  const totalPages = Math.ceil(processedRows.length / PAGE_SIZE);
  const paginatedRows = useMemo(() => {
    const start = (currentPage - 1) * PAGE_SIZE;
    return processedRows.slice(start, start + PAGE_SIZE);
  }, [processedRows, currentPage]);

  // Reset page on filter change
  useEffect(() => {
    setCurrentPage(1);
  }, [filter]);

  return (
    <div className="h-full flex flex-col bg-zinc-950">
      {/* Toolbar */}
      <div className="flex items-center gap-4 px-6 py-4 bg-zinc-950 border-b border-zinc-900">
        <button
          onClick={onToggleLogging}
          className={`flex items-center gap-2 px-3 py-1.5 rounded-full text-[10px] font-bold uppercase transition-all border ${
            loggingEnabled
              ? 'bg-green-500/10 text-green-400 border-green-500/20'
              : 'bg-zinc-900 text-zinc-500 border-zinc-800'
          }`}
        >
          <div className={`w-1.5 h-1.5 rounded-full ${loggingEnabled ? 'bg-green-500 animate-pulse' : 'bg-zinc-600'}`} />
          {loggingEnabled ? t('monitor.logging_status.active') : t('monitor.logging_status.paused')}
        </button>

        <div className="relative flex-1 max-w-md">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-zinc-600" />
          <input
            type="text"
            placeholder={t('monitor.search_placeholder')}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-full bg-zinc-900 border border-zinc-800 rounded-xl pl-10 pr-4 py-2 text-sm text-zinc-300 focus:outline-none focus:border-zinc-700 transition-all shadow-inner"
          />
        </div>
        
        <div className="flex gap-4 ml-auto items-center">
           <div className="flex items-center gap-1.5 px-3 py-1.5 bg-blue-500/10 border border-blue-500/20 rounded-full">
              <span className="text-[10px] font-bold text-blue-400 uppercase tracking-wider">{t('monitor.stats.tokens')}</span>
              <span className="text-xs font-mono text-blue-200">{(stats.inputTokens + stats.outputTokens).toLocaleString()}</span>
           </div>
           <div className="flex items-center gap-1.5 px-3 py-1.5 bg-green-500/10 border border-green-500/20 rounded-full">
              <span className="text-[10px] font-bold text-green-400 uppercase tracking-wider">{t('monitor.stats.recent')}</span>
              <span className="text-xs font-mono text-green-200">{stats.total}</span>
           </div>
        </div>

        <div className="w-px h-6 bg-zinc-800" />
        <button 
          onClick={onClear}
          className="p-2 text-zinc-500 hover:text-red-400 hover:bg-red-400/10 rounded-full transition-all"
          title={t('common.clear')}
        >
          <Trash2 className="w-5 h-5" />
        </button>
      </div>

      {/* Main Table Content */}
      <div className="flex-1 overflow-auto">
        <table className="w-full border-collapse text-left">
          <thead className="sticky top-0 z-10 bg-zinc-950/80 backdrop-blur-md">
            <tr className="border-b border-zinc-900">
              <th className="px-6 py-4 text-[10px] font-bold text-zinc-600 uppercase tracking-widest">{t('monitor.table.model')}</th>
              <th className="px-4 py-4 text-[10px] font-bold text-zinc-600 uppercase tracking-widest">{t('monitor.table.preview')}</th>
              <th className="px-4 py-4 text-[10px] font-bold text-zinc-600 uppercase tracking-widest text-center">{t('monitor.table.tokens')}</th>
              <th className="px-4 py-4 text-[10px] font-bold text-zinc-600 uppercase tracking-widest text-right">{t('monitor.table.latency')}</th>
              <th className="px-6 py-4 text-[10px] font-bold text-zinc-600 uppercase tracking-widest text-right">{t('monitor.table.time')}</th>
            </tr>
          </thead>
          <tbody>
            {paginatedRows.map((row, idx) => (
              <tr 
                key={row.req.id || idx}
                onClick={() => setSelectedRow(row)}
                className="group border-b border-zinc-900/50 hover:bg-zinc-900/30 cursor-pointer transition-colors"
              >
                <td className="px-6 py-4">
                  <div className="flex flex-col">
                    <span className="text-sm font-bold text-zinc-200 group-hover:text-blue-400 transition-colors">{row.details.model}</span>
                    <span className="text-[10px] text-zinc-600 font-mono">{(row.req.session || '').slice(0, 8)}...</span>
                  </div>
                </td>
                <td className="px-4 py-4">
                  <div className="text-xs text-zinc-400 truncate max-w-md bg-zinc-900/50 px-3 py-1.5 rounded-lg border border-zinc-800/50">
                    {(() => {
                      try {
                        const body = JSON.parse(row.req.request_body || '{}');
                        const lastMsg = body.messages?.slice(-1)[0];
                        if (lastMsg) {
                          return formatFriendlyMessage(lastMsg);
                        }
                        return '-';
                      } catch {
                        return '-';
                      }
                    })()}
                  </div>
                </td>
                <td className="px-4 py-4 text-center">
                  <div className="flex items-center justify-center gap-1.5">
                    <span className="text-[10px] font-bold text-zinc-500">{row.details.inputTokens}</span>
                    <div className="w-1 h-1 rounded-full bg-zinc-800" />
                    <span className="text-[10px] font-bold text-blue-400">{row.details.outputTokens}</span>
                  </div>
                </td>
                <td className="px-4 py-4 text-right">
                  <div className="flex flex-col items-end">
                    <span className={`text-xs font-mono font-bold ${row.details.latency > 3000 ? 'text-amber-500' : 'text-zinc-300'}`}>
                      {row.details.latency}ms
                    </span>
                  </div>
                </td>
                <td className="px-6 py-4 text-right">
                  <span className="text-[10px] text-zinc-500 font-medium">{formatTimestamp(row.req.ts)}</span>
                </td>
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

      {/* Pagination Footer */}
      {totalPages > 1 && (
        <div className="px-6 py-3 bg-zinc-950 border-t border-zinc-900 flex items-center justify-between">
          <div className="text-[10px] text-zinc-500 font-bold uppercase tracking-widest">
            Showing {((currentPage - 1) * PAGE_SIZE) + 1} to {Math.min(currentPage * PAGE_SIZE, processedRows.length)} of {processedRows.length} (Max 500)
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setCurrentPage(p => Math.max(1, p - 1))}
              disabled={currentPage === 1}
              className="p-1.5 bg-zinc-900 border border-zinc-800 rounded-lg text-zinc-400 hover:text-zinc-200 disabled:opacity-30 disabled:cursor-not-allowed transition-all"
            >
              <ChevronLeft className="w-4 h-4" />
            </button>
            <div className="px-3 py-1 bg-zinc-900/50 border border-zinc-800 rounded-lg text-xs font-mono text-blue-400 font-bold">
              {currentPage} / {totalPages}
            </div>
            <button
              onClick={() => setCurrentPage(p => Math.min(totalPages, p + 1))}
              disabled={currentPage === totalPages}
              className="p-1.5 bg-zinc-900 border border-zinc-800 rounded-lg text-zinc-400 hover:text-zinc-200 disabled:opacity-30 disabled:cursor-not-allowed transition-all"
            >
              <ChevronRight className="w-4 h-4" />
            </button>
          </div>
        </div>
      )}

      {/* Detail Modal */}
      {selectedRow && (
        <div 
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-6"
          onClick={() => setSelectedRow(null)}
        >
          <div 
            className="bg-zinc-950 border border-zinc-800 rounded-2xl shadow-2xl w-full max-w-5xl max-h-[90vh] flex flex-col overflow-hidden"
            onClick={e => e.stopPropagation()}
          >
            {/* Modal Header */}
            <div className="px-6 py-4 border-b border-zinc-900 flex items-center justify-between bg-zinc-900/30">
              <div className="flex items-center gap-4">
                <div className="p-2 bg-blue-500/10 rounded-lg">
                  <Activity className="w-5 h-5 text-blue-400" />
                </div>
                <div>
                  <h3 className="text-sm font-bold text-zinc-100">{t('monitor.details.title')}</h3>
                  <div className="flex items-center gap-2 mt-1">
                    <span className="text-[10px] text-zinc-500 font-mono bg-zinc-900 px-1.5 py-0.5 rounded border border-zinc-800">{selectedRow.req.session}</span>
                    <span className="text-[10px] text-blue-400 font-mono bg-blue-900/20 px-1.5 py-0.5 rounded border border-blue-500/20">{selectedRow.req.path}</span>
                  </div>
                </div>
              </div>
              <button 
                onClick={() => setSelectedRow(null)}
                className="p-2 hover:bg-zinc-900 rounded-full transition-colors text-zinc-500 hover:text-zinc-200"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            <div className="flex-1 overflow-auto p-6 space-y-6">
              {/* Metric Cards */}
              <div className="grid grid-cols-4 gap-4">
                <MetricCard 
                  label="Provider Latency" 
                  value={`${selectedRow.req.latency || 0}ms`} 
                  subValue="Total turnaround time"
                  icon={<Activity className="w-4 h-4" />}
                  color="blue"
                />
                <MetricCard 
                  label="Throughput" 
                  value={`${((selectedRow.req.output_tokens || 0) / ((selectedRow.req.latency || 1) / 1000)).toFixed(1)}`} 
                  unit="tok/s"
                  subValue="Generation speed"
                  icon={<Activity className="w-4 h-4" />}
                  color="green"
                />
                <MetricCard 
                  label="Tokens" 
                  value={`${(selectedRow.req.input_tokens || 0) + (selectedRow.req.output_tokens || 0)}`} 
                  subValue={`${selectedRow.req.input_tokens || 0} → ${selectedRow.req.output_tokens || 0}`}
                  icon={<Activity className="w-4 h-4" />}
                  color="purple"
                />
                <MetricCard 
                  label="Finish Reason" 
                  value={selectedRow.req.finish_reason || 'stop'} 
                  subValue={selectedRow.req.is_sse ? 'Streaming' : 'Non-streaming'}
                  icon={<CheckCircle className="w-4 h-4" />}
                  color="amber"
                />
              </div>

              {/* Visual Timeline Bar */}
              <div className="bg-zinc-900/50 p-4 rounded-xl border border-zinc-800/50">
                 <div className="flex justify-between items-center mb-2 text-[10px] font-bold text-zinc-500 uppercase tracking-widest">
                    <span>Latency Timeline</span>
                    <span>Total: {selectedRow.req.latency}ms</span>
                 </div>
                 <div className="h-2 w-full bg-zinc-800 rounded-full overflow-hidden flex">
                    <div 
                      className="h-full bg-blue-500/80 transition-all duration-500" 
                      style={{ width: '40%' }} 
                      title="TTFT"
                    />
                    <div 
                      className="h-full bg-blue-400/40 transition-all duration-500" 
                      style={{ width: '60%' }} 
                      title="Generation"
                    />
                 </div>
              </div>

              {/* Request & Response Sections */}
              <div className="grid grid-cols-2 gap-6">
                {/* Prompt Section */}
                <div className="flex flex-col gap-3">
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-bold text-zinc-400 uppercase tracking-widest flex items-center gap-2">
                      <div className="w-1 h-3 bg-blue-500 rounded-full" />
                      Prompt (Last Message)
                    </span>
                    <div className="flex bg-zinc-900/50 rounded-lg p-0.5 border border-zinc-800">
                      <button 
                        onClick={() => setRequestViewMode('friendly')}
                        className={`px-3 py-1 text-[10px] font-bold rounded-md transition-all ${
                          requestViewMode === 'friendly' 
                            ? 'bg-zinc-800 text-blue-400 shadow-sm' 
                            : 'text-zinc-500 hover:text-zinc-400'
                        }`}
                      >
                        FRIENDLY
                      </button>
                      <button 
                        onClick={() => setRequestViewMode('raw')}
                        className={`px-3 py-1 text-[10px] font-bold rounded-md transition-all ${
                          requestViewMode === 'raw' 
                            ? 'bg-zinc-800 text-purple-400 shadow-sm' 
                            : 'text-zinc-500 hover:text-zinc-400'
                        }`}
                      >
                        RAW
                      </button>
                    </div>
                  </div>
                  <div className="bg-zinc-900/30 border border-zinc-800/50 rounded-xl p-4 text-sm text-zinc-300 min-h-[200px] font-sans leading-relaxed overflow-auto">
                    {requestViewMode === 'friendly' ? (
                      (() => {
                        try {
                          const body = JSON.parse(selectedRow.req.request_body || '{}');
                          const lastMsg = body.messages?.slice(-1)[0];
                          if (!lastMsg) return 'No prompt content';
                          return formatFriendlyMessage(lastMsg);
                        } catch {
                          return selectedRow.req.request_body;
                        }
                      })()
                    ) : (
                      <JsonViewer data={selectedRow.req.request_body || '{}'} />
                    )}
                  </div>
                </div>

                {/* Assistant Section */}
                <div className="flex flex-col gap-3">
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-bold text-zinc-400 uppercase tracking-widest flex items-center gap-2">
                      <div className="w-1 h-3 bg-green-500 rounded-full" />
                      Assistant Response
                    </span>
                    <div className="flex bg-zinc-900/50 rounded-lg p-0.5 border border-zinc-800">
                      <button 
                        onClick={() => setResponseViewMode('friendly')}
                        className={`px-3 py-1 text-[10px] font-bold rounded-md transition-all ${
                          responseViewMode === 'friendly' 
                            ? 'bg-zinc-800 text-green-400 shadow-sm' 
                            : 'text-zinc-500 hover:text-zinc-400'
                        }`}
                      >
                        FRIENDLY
                      </button>
                      <button 
                        onClick={() => setResponseViewMode('raw')}
                        className={`px-3 py-1 text-[10px] font-bold rounded-md transition-all ${
                          responseViewMode === 'raw' 
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
                      (() => {
                        try {
                          const body = JSON.parse(selectedRow.req.response_body || '{}');
                          const message = body.choices?.[0]?.message || body.choices?.[0]?.delta;
                          if (message) {
                            return formatFriendlyMessage(message) || 'Waiting for response...';
                          }
                          const content = body.choices?.[0]?.text || selectedRow.req.response_body;
                          return typeof content === 'string' ? content : JSON.stringify(content || 'Waiting for response...');
                        } catch {
                          return selectedRow.req.response_body || 'Waiting for response...';
                        }
                      })()
                    ) : (
                      <JsonViewer data={selectedRow.req.response_body || '{}'} />
                    )}
                  </div>
                </div>
              </div>

              {/* Raw JSON */}
              <details className="group border border-zinc-800/50 rounded-xl overflow-hidden">
                <summary className="flex items-center justify-between px-4 py-3 bg-zinc-900/20 cursor-pointer hover:bg-zinc-900/40 transition-colors">
                  <span className="text-xs font-bold text-zinc-500 uppercase tracking-widest">Metadata & Raw JSON</span>
                  <div className="text-zinc-500 group-open:rotate-180 transition-transform text-xs">▼</div>
                </summary>
                <div className="p-4 bg-zinc-950">
                  <JsonViewer data={JSON.stringify({
                    model: selectedRow.req.model,
                    latency: selectedRow.req.latency,
                    usage: {
                      input: selectedRow.req.input_tokens,
                      output: selectedRow.req.output_tokens
                    },
                    finish_reason: selectedRow.req.finish_reason,
                    raw_request: JSON.parse(selectedRow.req.request_body || '{}'),
                    raw_response: selectedRow.req.response_body
                  }, null, 2)} />
                </div>
              </details>
            </div>
          </div>
        </div>
      )}
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
