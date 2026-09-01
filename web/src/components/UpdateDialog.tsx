import { Download, ExternalLink, RefreshCw, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { UpdateInfo, UpdateProgress } from '@/lib/update';

interface UpdateDialogProps {
  info: UpdateInfo;
  progress: UpdateProgress | null;
  onInstall: () => void;
  onDismiss: () => void;
  onOpenRelease: () => void;
}

export function UpdateDialog({ info, progress, onInstall, onDismiss, onOpenRelease }: UpdateDialogProps) {
  const { t } = useTranslation();
  const phase = progress?.phase || 'idle';
  const busy = phase === 'downloading' || phase === 'verifying' || phase === 'staging' || phase === 'restarting';
  const percentage = progress?.total_bytes
    ? Math.min(100, Math.round(((progress.downloaded_bytes || 0) / progress.total_bytes) * 100))
    : 0;

  const phaseLabel = phase === 'downloading'
    ? t('update.downloading', { percent: percentage })
    : phase === 'verifying'
      ? t('update.verifying')
      : phase === 'staging'
        ? t('update.staging')
        : phase === 'restarting'
          ? t('update.restarting')
          : '';

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm no-drag">
      <div className="w-[440px] max-w-[calc(100vw-2rem)] rounded-2xl border border-zinc-700 bg-zinc-900 p-6 shadow-2xl">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="text-base font-semibold text-zinc-100">{t('update.title')}</h2>
            <p className="mt-1 text-xs text-zinc-400">
              {t('update.description', { version: info.latest_version })}
            </p>
          </div>
          {!busy && (
            <button onClick={onDismiss} className="rounded-lg p-1 text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200" aria-label={t('update.later')}>
              <X className="h-4 w-4" />
            </button>
          )}
        </div>

        {busy && (
          <div className="mt-5">
            <div className="mb-2 flex items-center gap-2 text-xs text-blue-300">
              <RefreshCw className="h-3.5 w-3.5 animate-spin" />
              {phaseLabel}
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-zinc-800">
              <div
                className={`h-full rounded-full bg-blue-500 transition-all ${phase === 'downloading' ? '' : 'animate-pulse'}`}
                style={{ width: phase === 'downloading' ? `${percentage}%` : '100%' }}
              />
            </div>
          </div>
        )}

        {phase === 'error' && (
          <div className="mt-5 rounded-xl border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300">
            {progress?.manual_required ? t('update.manual_required') : progress?.error || t('update.failed')}
          </div>
        )}

        {!busy && (
          <div className="mt-6 flex items-center justify-end gap-2">
            <button onClick={onDismiss} className="rounded-lg px-3 py-2 text-xs font-medium text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200">
              {t('update.later')}
            </button>
            {(phase === 'error' || progress?.manual_required) && (
              <button onClick={onOpenRelease} className="flex items-center gap-1.5 rounded-lg bg-zinc-800 px-3 py-2 text-xs font-medium text-zinc-200 hover:bg-zinc-700">
                <ExternalLink className="h-3.5 w-3.5" />
                {t('update.open_release')}
              </button>
            )}
            {phase !== 'error' && (
              <button onClick={onInstall} className="flex items-center gap-1.5 rounded-lg bg-blue-600 px-3 py-2 text-xs font-semibold text-white hover:bg-blue-500">
                <Download className="h-3.5 w-3.5" />
                {t('update.install_restart')}
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
