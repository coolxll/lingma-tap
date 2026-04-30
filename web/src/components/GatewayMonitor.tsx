import { useState, useMemo } from 'react';
import { Search, Trash2, X, Copy, CheckCircle, Activity } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { TrafficRecord, formatTimestamp, getStatusColor } from '@/lib/types';
import { parseLogDetails } from '@/lib/log-parser';
import { JsonViewer } from './JsonViewer';
import { SseEventList } from './SseEventList';

interface GatewayMonitorProps {
  records: TrafficRecord[]; // All records (will filter for C2S)
  allRecords: TrafficRecord[]; // Full list to find responses
  onClear: () => void;
  loggingEnabled: boolean;
  onToggleLogging: () => void;
}

export function GatewayMonitor({ 
  records, 
  allRecords, 
  onClear, 
  loggingEnabled, 
  onToggleLogging 
}: GatewayMonitorProps) {
  const { t } = useTranslation();
  const [filter, setFilter] = useState('');
  const [selectedRow, setSelectedRow] = useState<{ req: TrafficRecord; resp: TrafficRecord | null } | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);

  // Filter for C2S records (requests) and apply search filter
  const processedRows = useMemo(() => {
    return records
      .filter(r => r.direction === 'C2S')
      .map(req => {
        const resp = allRecords.find(r => r.session === req.session && r.direction === 'S2C') || null;
        const details = parseLogDetails(req, resp);
        return { req, resp, details };
      })
      .filter(row => {
        if (!filter) return true;
        const search = filter.toLowerCase();
        return (
          row.req.path.toLowerCase().includes(search) ||
          row.details.model.toLowerCase().includes(search) ||
          row.req.session.toLowerCase().includes(search)
        );
      });
  }, [records, allRecords, filter]);

  // Statistics
  const stats = useMemo(() => {
    const total = processedRows.length;
    const ok = processedRows.filter(r => r.resp && r.resp.status >= 200 && r.resp.status < 300).length;
    const err = processedRows.filter(r => r.resp && r.resp.status >= 400).length;
    return { total, ok, err };
  }, [processedRows]);

  const handleCopy = async (text: string, id: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedId(id);
      setTimeout(() => setCopiedId(null), 2000);
    } catch (err) {
      console.error('Failed to copy:', err);
    }
  };

  return (
    <div className="h-full flex flex-col bg-zinc-950">
      {/* Toolbar */}
      <div className="p-3 border-b border-zinc-900 flex items-center gap-4 bg-zinc-950/50">
        <button
          onClick={onToggleLogging}
          className={`flex items-center gap-2 px-3 py-1.5 rounded-full text-xs font-medium transition-all ${
            loggingEnabled
              ? 'bg-red-900/40 text-red-400 border border-red-800/50 animate-pulse'
              : 'bg-zinc-800 text-zinc-400 border border-zinc-700'
          }`}
        >
          <Activity className="w-3.5 h-3.5" />
          {loggingEnabled ? t('monitor.logging_status.active') : t('monitor.logging_status.paused')}
        </button>

        <div className="relative flex-1 max-w-md">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 text-zinc-500 w-3.5 h-3.5" />
          <input
            type="text"
            placeholder={t('monitor.filters.placeholder')}
            className="w-full bg-zinc-900 border border-zinc-800 rounded-lg pl-9 pr-4 py-1.5 text-sm text-zinc-200 focus:outline-none focus:ring-1 focus:ring-blue-500/50 transition-all"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>

        <div className="flex items-center gap-4 text-[11px] font-bold uppercase tracking-wider px-2">
          <span className="text-zinc-500">{t('monitor.stats.total')}: <span className="text-zinc-200">{stats.total}</span></span>
          <span className="text-zinc-500">{t('monitor.stats.ok')}: <span className="text-green-500">{stats.ok}</span></span>
          <span className="text-zinc-500">{t('monitor.stats.err')}: <span className="text-red-500">{stats.err}</span></span>
        </div>

        <div className="flex-1" />

        <button 
          onClick={onClear}
          className="p-2 text-zinc-500 hover:text-zinc-200 hover:bg-zinc-800 rounded-full transition-colors"
          title={t('common.clear')}
        >
          <Trash2 className="w-4 h-4" />
        </button>
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto">
        <table className="w-full border-collapse text-left text-xs">
          <thead className="sticky top-0 bg-zinc-900/90 backdrop-blur z-10 border-b border-zinc-800">
            <tr className="text-zinc-500 font-medium uppercase tracking-wider">
              <th className="px-4 py-3">{t('monitor.table.status')}</th>
              <th className="px-4 py-3">{t('monitor.table.method')}</th>
              <th className="px-4 py-3">{t('monitor.table.model')}</th>
              <th className="px-4 py-3">{t('monitor.table.path')}</th>
              <th className="px-4 py-3 text-right">{t('monitor.table.usage')}</th>
              <th className="px-4 py-3 text-right">{t('monitor.table.duration')}</th>
              <th className="px-4 py-3 text-right">{t('monitor.table.time')}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-zinc-900">
            {processedRows.map((row) => (
              <tr 
                key={row.req.id} 
                className="hover:bg-zinc-900/50 cursor-pointer transition-colors group"
                onClick={() => setSelectedRow({ req: row.req, resp: row.resp })}
              >
                <td className="px-4 py-3">
                  {row.resp ? (
                    <span className={`px-2 py-0.5 rounded text-[10px] font-bold border ${getStatusColor(row.resp.status).replace('text-', 'border-').replace('400', '500/20')} ${getStatusColor(row.resp.status)} bg-zinc-950`}>
                      {row.resp.status}
                    </span>
                  ) : (
                    <span className="text-zinc-600 italic">...</span>
                  )}
                </td>
                <td className="px-4 py-3 font-mono font-bold text-zinc-400">{row.req.method}</td>
                <td className="px-4 py-3">
                  <span className="text-blue-400 font-medium">{row.details.model}</span>
                </td>
                <td className="px-4 py-3 text-zinc-400 truncate max-w-xs" title={row.req.path}>
                  {row.req.path}
                </td>
                <td className="px-4 py-3 text-right font-mono">
                  {row.details.inputTokens > 0 || row.details.outputTokens > 0 ? (
                    <div className="flex flex-col text-[10px]">
                      <span className="text-zinc-500">I: <span className="text-zinc-300">{row.details.inputTokens}</span></span>
                      <span className="text-zinc-500">O: <span className="text-zinc-300">{row.details.outputTokens}</span></span>
                    </div>
                  ) : '-'}
                </td>
                <td className="px-4 py-3 text-right text-zinc-500 font-mono">
                  {row.resp ? `${row.resp.status_text}` : '-'}
                </td>
                <td className="px-4 py-3 text-right text-zinc-500 font-mono">
                  {formatTimestamp(row.req.ts)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {processedRows.length === 0 && (
          <div className="h-full flex items-center justify-center text-zinc-600 text-sm italic py-20">
            {t('recordlist.no_records')}
          </div>
        )}
      </div>

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
                <span className="text-sm font-bold text-zinc-100">{t('monitor.details.title')}</span>
                {selectedRow.resp && (
                  <span className={`px-2 py-0.5 rounded text-[10px] font-bold border ${getStatusColor(selectedRow.resp.status)} border-current/20 bg-zinc-950`}>
                    {selectedRow.resp.status} {selectedRow.resp.status_text}
                  </span>
                )}
                <span className="text-xs text-zinc-500 font-mono truncate max-w-md">{selectedRow.req.url}</span>
              </div>
              <button 
                onClick={() => setSelectedRow(null)}
                className="p-2 text-zinc-500 hover:text-zinc-200 hover:bg-zinc-800 rounded-full transition-colors"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Modal Body */}
            <div className="flex-1 overflow-y-auto p-6 space-y-8">
              {/* Metadata Grid */}
              <div className="grid grid-cols-1 md:grid-cols-3 gap-6 bg-zinc-900/20 p-6 rounded-2xl border border-zinc-900">
                <div className="space-y-1">
                  <span className="block text-[10px] font-black uppercase text-zinc-600 tracking-tighter">{t('monitor.details.time')}</span>
                  <span className="text-sm font-mono text-zinc-200">{new Date(selectedRow.req.ts).toLocaleString()}</span>
                </div>
                <div className="space-y-1">
                  <span className="block text-[10px] font-black uppercase text-zinc-600 tracking-tighter">{t('monitor.details.model')}</span>
                  <span className="text-sm font-bold text-blue-400">{parseLogDetails(selectedRow.req, selectedRow.resp).model}</span>
                </div>
                <div className="space-y-1">
                  <span className="block text-[10px] font-black uppercase text-zinc-600 tracking-tighter">{t('monitor.details.tokens')}</span>
                  <div className="flex gap-3 mt-1">
                    <span className="px-2 py-1 bg-blue-500/10 border border-blue-500/20 text-blue-400 rounded text-[10px] font-bold">
                      IN: {parseLogDetails(selectedRow.req, selectedRow.resp).inputTokens}
                    </span>
                    <span className="px-2 py-1 bg-green-500/10 border border-green-500/20 text-green-400 rounded text-[10px] font-bold">
                      OUT: {parseLogDetails(selectedRow.req, selectedRow.resp).outputTokens}
                    </span>
                  </div>
                </div>
              </div>

              {/* Payloads */}
              <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
                {/* Request */}
                <div className="flex flex-col h-full">
                  <div className="flex items-center justify-between mb-3 px-1">
                    <h3 className="text-[10px] font-black uppercase text-zinc-500 tracking-widest">{t('monitor.details.request_payload')}</h3>
                    <button 
                      onClick={() => handleCopy(selectedRow.req.request_body || '', 'req')}
                      className="text-[10px] text-zinc-500 hover:text-zinc-200 flex items-center gap-1.5 transition-colors"
                    >
                      {copiedId === 'req' ? <CheckCircle className="w-3 h-3 text-green-500" /> : <Copy className="w-3 h-3" />}
                      {copiedId === 'req' ? t('common.copied') : t('common.copy')}
                    </button>
                  </div>
                  <div className="flex-1 min-h-[300px] bg-zinc-900/50 rounded-xl border border-zinc-900 overflow-hidden">
                    <JsonViewer data={selectedRow.req.request_body} maxHeight="500px" />
                  </div>
                </div>

                {/* Response */}
                <div className="flex flex-col h-full">
                  <div className="flex items-center justify-between mb-3 px-1">
                    <h3 className="text-[10px] font-black uppercase text-zinc-500 tracking-widest">{t('monitor.details.response_payload')}</h3>
                    <button 
                      onClick={() => handleCopy(selectedRow.resp?.response_body || '', 'resp')}
                      className="text-[10px] text-zinc-500 hover:text-zinc-200 flex items-center gap-1.5 transition-colors"
                    >
                      {copiedId === 'resp' ? <CheckCircle className="w-3 h-3 text-green-500" /> : <Copy className="w-3 h-3" />}
                      {copiedId === 'resp' ? t('common.copied') : t('common.copy')}
                    </button>
                  </div>
                  <div className="flex-1 min-h-[300px] bg-zinc-900/50 rounded-xl border border-zinc-900 overflow-hidden">
                    {selectedRow.resp?.is_sse ? (
                      <div className="p-2">
                        <SseEventList events={selectedRow.resp.sse_events || []} />
                      </div>
                    ) : (
                      <JsonViewer data={selectedRow.resp?.response_body || ''} maxHeight="500px" />
                    )}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
