import { StorageStats } from '@/lib/types';

interface BottomDockProps {
  connected: boolean;
  recordCount: number;
  stats: StorageStats | null;
  proxyPort: number;
}

export function BottomDock({ connected, recordCount, stats, proxyPort }: BottomDockProps) {
  return (
    <div className="h-7 flex items-center px-3 gap-4 border-t border-zinc-800 bg-zinc-950 text-[10px] text-zinc-500 shrink-0">
      {/* Connection status */}
      <div className="flex items-center gap-1.5">
        <div className={`w-1.5 h-1.5 rounded-full ${connected ? 'bg-green-500' : 'bg-red-500'}`} />
        <span>{connected ? 'WS Connected' : 'WS Disconnected'}</span>
      </div>

      <span className="text-zinc-700">|</span>

      {/* Record count */}
      <span>{recordCount} records loaded</span>

      {stats && (
        <>
          <span className="text-zinc-700">|</span>
          <span>{stats.sessions} sessions</span>
          <span className="text-zinc-700">|</span>
          <span>{stats.records} total in DB</span>
        </>
      )}

      <div className="flex-1" />

      {/* Proxy port */}
      <span>Proxy: 127.0.0.1:{proxyPort}</span>
    </div>
  );
}
