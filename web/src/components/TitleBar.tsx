import { Shield, ShieldOff, Pause, Play, Trash2, Sun, Moon, Globe, ArrowDownToLine } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import i18n from '@/i18n';

export type TabId = 'proxy' | 'gateway' | 'settings';

interface TitleBarProps {
  activeTab: TabId;
  proxyRunning: boolean;
  isPaused: boolean;
  liveTail: boolean;
  theme: 'dark' | 'light';
  onTabChange: (tab: TabId) => void;
  onToggleProxy: () => void;
  onTogglePause: () => void;
  onToggleLiveTail: () => void;
  onClear: () => void;
  onToggleTheme: () => void;
}

export function TitleBar({
  activeTab,
  proxyRunning,
  isPaused,
  liveTail,
  theme,
  onTabChange,
  onToggleProxy,
  onTogglePause,
  onToggleLiveTail,
  onClear,
  onToggleTheme,
}: TitleBarProps) {
  const { t } = useTranslation();

  const toggleLanguage = () => {
    const nextLang = i18n.language === 'zh' ? 'en' : 'zh';
    i18n.changeLanguage(nextLang);
  };

  return (
    <div className="flex items-center justify-between px-4 pt-7 pb-3 shrink-0 bg-zinc-950 border-b border-zinc-900 select-none drag">
      {/* Left: Title - Using flex-1 to help center the tabs */}
      <div className="flex items-center flex-1">
        <span className="text-sm font-bold tracking-tight text-zinc-100 no-drag ml-2">Lingma Tap</span>
      </div>

      {/* Center: Tabs in a pill - No flex-1 to keep it centered based on siblings */}
      <div className="flex items-center bg-zinc-900 rounded-full p-1 gap-1 shadow-inner no-drag">
        <button
          onClick={() => onTabChange('proxy')}
          className={`px-4 py-1.5 text-xs font-medium rounded-full transition-all ${
            activeTab === 'proxy'
              ? 'bg-zinc-800 text-zinc-100 shadow-sm'
              : 'text-zinc-500 hover:text-zinc-300'
          }`}
        >
          {t('common.proxy_tab')}
        </button>
        <button
          onClick={() => onTabChange('gateway')}
          className={`px-4 py-1.5 text-xs font-medium rounded-full transition-all ${
            activeTab === 'gateway'
              ? 'bg-zinc-800 text-zinc-100 shadow-sm'
              : 'text-zinc-500 hover:text-zinc-300'
          }`}
        >
          {t('common.gateway_tab')}
        </button>
        <button
          onClick={() => onTabChange('settings')}
          className={`px-4 py-1.5 text-xs font-medium rounded-full transition-all ${
            activeTab === 'settings'
              ? 'bg-zinc-800 text-zinc-100 shadow-sm'
              : 'text-zinc-500 hover:text-zinc-300'
          }`}
        >
          {t('common.settings_tab')}
        </button>
      </div>

      {/* Right: Actions */}
      <div className="flex items-center justify-end gap-1.5 flex-1 no-drag">
        {/* Only show these controls on proxy tab */}
        {activeTab === 'proxy' && (
          <div className="flex items-center gap-1.5 mr-2">
            <button
              onClick={onToggleProxy}
              className={`flex items-center gap-1.5 px-2.5 py-1.5 rounded-full text-xs font-medium transition-colors ${
                proxyRunning
                  ? 'bg-green-900/40 text-green-400 hover:bg-green-900/60'
                  : 'bg-zinc-800/80 text-zinc-400 hover:bg-zinc-700'
              }`}
              title={proxyRunning ? t('titlebar.stop_proxy') : t('titlebar.start_proxy')}
            >
              {proxyRunning ? <Shield className="w-3 h-3" /> : <ShieldOff className="w-3 h-3" />}
              {proxyRunning ? t('titlebar.proxy_on') : t('titlebar.proxy_off')}
            </button>

            <button
              onClick={onTogglePause}
              className={`flex items-center gap-1.5 px-2.5 py-1.5 rounded-full text-xs transition-colors ${
                isPaused ? 'bg-zinc-800 text-zinc-500 hover:bg-zinc-700' : 'bg-amber-900/40 text-amber-400 hover:bg-amber-900/60'
              }`}
              title={isPaused ? t('titlebar.resume') : t('titlebar.pause')}
            >
              {isPaused ? <Play className="w-3 h-3" /> : <Pause className="w-3 h-3" />}
              {isPaused ? t('titlebar.resume') : t('titlebar.pause')}
            </button>

            <button
              onClick={onToggleLiveTail}
              className={`p-1.5 rounded-full transition-colors ${
                liveTail ? 'bg-blue-900/40 text-blue-400' : 'bg-zinc-900 text-zinc-500 hover:bg-zinc-800'
              }`}
              title={liveTail ? t('titlebar.disable_auto_scroll') : t('titlebar.enable_auto_scroll')}
            >
              <ArrowDownToLine className="w-4 h-4" />
            </button>

            <button
              onClick={onClear}
              className="p-1.5 rounded-full bg-zinc-900 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors"
              title={t('titlebar.clear_records')}
            >
              <Trash2 className="w-3.5 h-3.5" />
            </button>
            <div className="w-px h-5 bg-zinc-800 mx-1" />
          </div>
        )}

        <button
          onClick={onToggleTheme}
          className="p-1.5 rounded-full bg-zinc-900 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors"
          title={t('titlebar.toggle_theme')}
        >
          {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
        </button>
        <button
          onClick={toggleLanguage}
          className="px-2 py-1.5 rounded-full bg-zinc-900 text-xs font-medium text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors flex items-center gap-1"
          title="Toggle Language"
        >
          <Globe className="w-3.5 h-3.5" />
          <span>{t('titlebar.language')}</span>
        </button>
      </div>
    </div>
  );
}
