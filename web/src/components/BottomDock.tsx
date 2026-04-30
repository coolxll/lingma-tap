import { StorageStats } from '@/lib/types';
import { useTranslation } from 'react-i18next';

interface BottomDockProps {
  connected: boolean;
  recordCount: number;
  stats: StorageStats | null;
  proxyPort: number;
}

export function BottomDock({ connected, recordCount, stats, proxyPort }: BottomDockProps) {
  const { t } = useTranslation();

  return (
    <div className="h-7 flex items-center px-3 gap-4 border-t border-zinc-800 bg-zinc-950 text-[10px] text-zinc-500 shrink-0">
      {/* Connection status */}
      <div className="flex items-center gap-1.5">
        <div className={`w-1.5 h-1.5 rounded-full ${connected ? 'bg-green-500' : 'bg-red-500'}`} />
        <span>{connected ? t('bottomdock.ws_connected') : t('bottomdock.ws_disconnected')}</span>
      </div>

      <span className="text-zinc-700">|</span>

      {/* Record count */}
      <span>{recordCount} {t('bottomdock.records_loaded')}</span>

      {stats && (
        <>
          <span className="text-zinc-700">|</span>
          <span>{stats.sessions} {t('bottomdock.sessions')}</span>
          <span className="text-zinc-700">|</span>
          <span>{stats.records} {t('bottomdock.total_in_db')}</span>
        </>
      )}

      <div className="flex-1" />

      {/* Proxy port */}
      <span>{t('common.proxy')}: 127.0.0.1:{proxyPort}</span>
    </div>
  );
}
