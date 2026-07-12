import { useState, useEffect, useCallback, useRef } from 'react';
import { RefreshCw, Copy, Check, Shield, ShieldOff, Server, ServerOff, Trash2, FolderOpen, FileKey, ExternalLink, LogIn, CircleCheck, LoaderCircle } from 'lucide-react';
import { useTranslation } from 'react-i18next';

const GITHUB_OWNER = 'coolxll';
const GITHUB_REPO = 'lingma-tap';
const RELEASES_URL = `https://github.com/${GITHUB_OWNER}/${GITHUB_REPO}/releases`;

// Wails window type
interface WailsWindow extends Window {
  go?: {
    main?: {
      App?: {
        GetModels: () => Promise<any[]>;
        GetNetworkInterfaces: () => Promise<Array<{name: string; addr: string; type: string}>>;
        ClearRecords: () => Promise<void>;
        ClearRecordsBefore: (days: number) => Promise<number>;
        GetAnthropicMapping: () => Promise<any>;
        SaveAnthropicMapping: (mapping: Record<string, string>, defaultModel: string) => Promise<void>;
        OpenExternal: (url: string) => Promise<void>;
        GetVersion: () => Promise<string>;
      };
    };
  };
}

interface ModelInfo {
  id: string;         // key from Lingma (e.g. "gm51model")
  object: string;
  display_name?: string;  // friendly name (e.g. "Qwen3-Coder")
  owned_by: string;
}

interface StorageStats {
  records: number;
  sessions: number;
  oldest_ts?: string;
  newest_ts?: string;
}

interface SettingsPanelProps {
  proxyRunning: boolean;
  proxyPort: number;
  onToggleProxy: () => void;
  onProxyPortChange?: (port: number) => void;
  gatewayRunning?: boolean;
  gatewayPort?: number;
  gatewayListenAddr?: string;
  onToggleGateway?: () => void;
  onGatewayPortChange?: (port: number) => void;
  onGatewayListenAddrChange?: (addr: string) => void;
  loggingEnabled?: boolean;
  onToggleLogging?: () => void;
  proxyLoggingEnabled?: boolean;
  onToggleProxyLogging?: () => void;
  lingmaHTTP2Enabled?: boolean;
  onToggleLingmaHTTP2?: () => void;
  authenticated?: boolean;
  authUser?: string;
  authExpireTime?: number;
  oauthInProgress?: boolean;
  oauthError?: string;
  onStartOAuthLogin?: () => Promise<void>;
  stats?: StorageStats | null;
  onClearAll?: () => void;
  onClearBefore?: (days: number) => Promise<number>;
  caCertPath?: string;
  onRevealCACert?: () => Promise<void>;
}

export function SettingsPanel({
  proxyRunning,
  proxyPort,
  onToggleProxy,
  onProxyPortChange,
  gatewayRunning = false,
  gatewayPort = 8080,
  gatewayListenAddr = "127.0.0.1",
  onToggleGateway,
  onGatewayPortChange,
  onGatewayListenAddrChange,
  loggingEnabled,
  onToggleLogging,
  proxyLoggingEnabled = true,
  onToggleProxyLogging,
  lingmaHTTP2Enabled = false,
  onToggleLingmaHTTP2,
  authenticated = false,
  authUser = '',
  authExpireTime = 0,
  oauthInProgress = false,
  oauthError = '',
  onStartOAuthLogin,
  stats,
  onClearAll,
  onClearBefore,
  caCertPath,
  onRevealCACert,
}: SettingsPanelProps) {
  const { t } = useTranslation();
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState('');
  const [copied, setCopied] = useState<string | null>(null);
  const [clearDays, setClearDays] = useState<number | "">(30);
  const [clearMsg, setClearMsg] = useState('');
  const [clearLoading, setClearLoading] = useState(false);
  const [revealLoading, setRevealLoading] = useState(false);
  const [confirmAll, setConfirmAll] = useState(false);
  const [confirmBefore, setConfirmBefore] = useState(false);
  const [anthropicMapping, setAnthropicMapping] = useState<Record<string, string>>({});
  const [anthropicDefault, setAnthropicDefault] = useState('dashscope_qmodel');
  const [savingMapping, setSavingMapping] = useState(false);
  const [currentVersion, setCurrentVersion] = useState('');
  const currentVersionRef = useRef('');
  const [updateStatus, setUpdateStatus] = useState<'idle' | 'checking' | 'latest' | 'available' | 'error'>('idle');
  const [latestVersion, setLatestVersion] = useState('');
  const [networkInterfaces, setNetworkInterfaces] = useState<Array<{name: string; addr: string; type: string}>>([]);
  const [updateError, setUpdateError] = useState('');

  const getCutoffDate = (days: number) => {
    const date = new Date();
    date.setDate(date.getDate() - days);
    return date.toLocaleDateString(undefined, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit'
    });
  };

  const ENDPOINTS = [
    { method: 'GET', path: '/v1/models', desc: t('settings.models') },
    { method: 'POST', path: '/v1/chat/completions', desc: 'OpenAI Chat' },
    { method: 'POST', path: '/v1/responses', desc: 'OpenAI Responses' },
    { method: 'POST', path: '/v1/messages', desc: 'Anthropic Messages' },
  ];

  const fetchModels = useCallback(async () => {
    setModelsLoading(true);
    setModelsError('');
    try {
      // Use Wails binding (avoids CORS issues)
      const w = (window as unknown as WailsWindow).go;
      const result = await w?.main?.App?.GetModels();
      if (!result) throw new Error('Bridge not available');
      // Map Go ModelInfo (key/display_name) to our ModelInfo (id/display_name)
      const mapped: ModelInfo[] = (result as any[]).map(m => ({
        id: m.key || m.id,
        object: m.object || 'model',
        display_name: m.display_name || m.DisplayName || m.key || m.id,
        owned_by: m.owned_by || 'lingma',
      }));
      setModels(mapped);
    } catch (err) {
      setModelsError(err instanceof Error ? err.message : 'Failed to fetch models');
    } finally {
      setModelsLoading(false);
    }
  }, []);

  const fetchNetworkInterfaces = useCallback(async () => {
    try {
      const w = (window as unknown as WailsWindow).go;
      const result = await w?.main?.App?.GetNetworkInterfaces();
      if (result) {
        setNetworkInterfaces(result);
      }
    } catch (err) {
      console.error('Failed to fetch network interfaces:', err);
    }
  }, []);

  const fetchAnthropicMapping = useCallback(async () => {
    try {
      const w = (window as unknown as WailsWindow).go;
      const result = await w?.main?.App?.GetAnthropicMapping();
      if (result) {
        setAnthropicMapping(result.mapping || {});
        setAnthropicDefault(result.default_model || 'dashscope_qmodel');
      }
    } catch (err) {
      console.error('Failed to fetch Anthropic mapping', err);
    }
  }, []);

  useEffect(() => {
    if (authenticated) {
      fetchModels();
    }
    fetchAnthropicMapping();
    fetchNetworkInterfaces();
  }, [authenticated, fetchModels, fetchAnthropicMapping, fetchNetworkInterfaces]);

  // Fetch current version from Wails backend
  useEffect(() => {
    const fetchVersion = async () => {
      try {
        const w = (window as unknown as WailsWindow).go;
        const version = await w?.main?.App?.GetVersion();
        if (version) {
          const normalized = version.replace(/^v/, '');
          currentVersionRef.current = normalized;
          setCurrentVersion(normalized);
        }
      } catch (err) {
        console.error('Failed to fetch version', err);
      }
    };
    fetchVersion();
  }, []);

  const handleSaveMapping = async () => {
    setSavingMapping(true);
    try {
      const w = (window as unknown as WailsWindow).go;
      await w?.main?.App?.SaveAnthropicMapping(anthropicMapping, anthropicDefault);
      setClearMsg(t('settings.mapping_saved'));
      setTimeout(() => setClearMsg(''), 3000);
    } catch (err) {
      setClearMsg(`Error: ${err instanceof Error ? err.message : 'Unknown error'}`);
    } finally {
      setSavingMapping(false);
    }
  };

  const addMappingItem = () => {
    setAnthropicMapping({ ...anthropicMapping, [`new_keyword_${Object.keys(anthropicMapping).length}`]: 'dashscope_qmodel' });
  };

  const updateMappingKey = (oldKey: string, newKey: string) => {
    if (oldKey === newKey) return;
    const newMapping = { ...anthropicMapping };
    const value = newMapping[oldKey];
    delete newMapping[oldKey];
    newMapping[newKey] = value;
    setAnthropicMapping(newMapping);
  };

  const updateMappingValue = (key: string, newValue: string) => {
    setAnthropicMapping({ ...anthropicMapping, [key]: newValue });
  };

  const removeMappingItem = (key: string) => {
    const newMapping = { ...anthropicMapping };
    delete newMapping[key];
    setAnthropicMapping(newMapping);
  };

  const copyToClipboard = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(text);
      setTimeout(() => setCopied(null), 1500);
    } catch {
      // ignore
    }
  };

  const handleClearAll = useCallback(async () => {
    if (!onClearAll) return;
    setClearLoading(true);
    setClearMsg('');
    try {
      await onClearAll();
      setClearMsg('已清空所有记录');
      setTimeout(() => setClearMsg(''), 3000);
    } catch (err) {
      setClearMsg(`错误: ${err instanceof Error ? err.message : '未知错误'}`);
    } finally {
      setClearLoading(false);
    }
  }, [onClearAll]);

  const handleClearBefore = useCallback(async () => {
    if (!onClearBefore) return;
    const days = typeof clearDays === 'number' ? clearDays : 30;
    setClearLoading(true);
    setClearMsg('');
    try {
      const deleted = await onClearBefore(days);
      setClearMsg(`已删除 ${deleted} 条记录`);
      setTimeout(() => setClearMsg(''), 3000);
    } catch (err) {
      setClearMsg(`错误: ${err instanceof Error ? err.message : '未知错误'}`);
    } finally {
      setClearLoading(false);
    }
  }, [onClearBefore, clearDays]);

  const parseComparableVersion = (version: string): number[] => {
    const match = version.match(/^v?(\d+)\.(\d+)\.(\d+)/);
    if (match) {
      return match.slice(1).map(Number);
    }
    return [0, 0, 0];
  };

  const compareVersions = (a: string, b: string): number => {
    const aParts = parseComparableVersion(a);
    const bParts = parseComparableVersion(b);
    for (let i = 0; i < Math.max(aParts.length, bParts.length); i++) {
      const aNum = aParts[i] || 0;
      const bNum = bParts[i] || 0;
      if (aNum > bNum) return 1;
      if (aNum < bNum) return -1;
    }
    return 0;
  };

  const checkForUpdate = useCallback(async () => {
    setUpdateStatus('checking');
    setUpdateError('');
    try {
      const response = await fetch(`https://api.github.com/repos/${GITHUB_OWNER}/${GITHUB_REPO}/releases/latest`);
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      const data = await response.json();
      const remoteVersion = data.tag_name as string;
      setLatestVersion(remoteVersion);
      if (compareVersions(remoteVersion, currentVersionRef.current) > 0) {
        setUpdateStatus('available');
      } else {
        setUpdateStatus('latest');
      }
    } catch (err) {
      setUpdateStatus('error');
      setUpdateError(err instanceof Error ? err.message : 'Unknown error');
    }
  }, []);

  const openReleasePage = () => {
    const w = window as unknown as WailsWindow;
    if (w.go?.main?.App?.OpenExternal) {
      w.go.main.App.OpenExternal(RELEASES_URL);
    } else {
      window.open(RELEASES_URL, '_blank');
    }
  };

  const handleRevealCACert = useCallback(async () => {
    if (!onRevealCACert) return;
    setRevealLoading(true);
    setClearMsg('');
    try {
      await onRevealCACert();
    } catch (err) {
      setClearMsg(`错误: ${err instanceof Error ? err.message : '无法打开证书位置'}`);
    } finally {
      setRevealLoading(false);
    }
  }, [onRevealCACert]);

  const authExpiryLabel = authExpireTime > 0
    ? new Date(authExpireTime).toLocaleString()
    : '';

  return (
    <div className="h-full overflow-y-auto bg-zinc-950 p-6">
      <div className="max-w-2xl space-y-8">

        <section>
          <h2 className="text-sm font-semibold text-zinc-200 mb-4 uppercase tracking-widest opacity-60">{t('settings.authentication')}</h2>
          <div className="bg-zinc-900/30 rounded-2xl p-5 border border-zinc-800/50 flex items-center justify-between gap-4">
            <div className="min-w-0">
              <div className="flex items-center gap-2 text-sm font-bold text-zinc-100">
                {authenticated ? <CircleCheck className="w-4 h-4 text-emerald-400 shrink-0" /> : <Shield className="w-4 h-4 text-amber-400 shrink-0" />}
                <span>{authenticated ? t('settings.authenticated') : t('settings.not_authenticated')}</span>
              </div>
              <p className="mt-1 text-[11px] text-zinc-500">
                {oauthInProgress
                  ? t('settings.oauth_waiting')
                  : authenticated
                    ? authExpiryLabel
                      ? t('settings.authenticated_hint', { user: authUser || t('settings.unknown_user'), expires: authExpiryLabel })
                      : t('settings.authenticated_user_hint', { user: authUser || t('settings.unknown_user') })
                    : t('settings.oauth_hint')}
              </p>
              {!oauthInProgress && oauthError && <p className="mt-2 text-[11px] text-red-400">{oauthError}</p>}
            </div>
            <button
              onClick={() => void onStartOAuthLogin?.()}
              disabled={!onStartOAuthLogin || oauthInProgress}
              className="shrink-0 flex items-center gap-2 px-4 py-2 rounded-xl text-xs font-bold bg-blue-500/10 text-blue-300 border border-blue-500/20 hover:bg-blue-500/20 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {oauthInProgress ? <LoaderCircle className="w-3.5 h-3.5 animate-spin" /> : <LogIn className="w-3.5 h-3.5" />}
              {oauthInProgress ? t('settings.oauth_waiting_button') : authenticated ? t('settings.oauth_reauthenticate') : t('settings.oauth_login')}
            </button>
          </div>
        </section>

        {/* Network Settings */}
        <section>
          <h2 className="text-sm font-semibold text-zinc-200 mb-4 uppercase tracking-widest opacity-60">{t('common.settings_tab')}</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            
            {/* Proxy Settings */}
            <div className="bg-zinc-900/30 rounded-2xl p-5 border border-zinc-800/50 flex flex-col justify-between min-h-[160px]">
              <div>
                <div className="flex items-center justify-between mb-4">
                  <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
                    <Shield className="w-4 h-4 text-blue-400" />
                    {t('common.proxy')}
                  </h3>
                  <div className="flex items-center gap-2 px-2 py-0.5 rounded-full bg-zinc-950/50 border border-zinc-800/50">
                    <div className={`w-1.5 h-1.5 rounded-full ${proxyRunning ? 'bg-green-500 animate-pulse' : 'bg-zinc-600'}`} />
                    <span className="text-[10px] font-bold text-zinc-400 uppercase">{proxyRunning ? t('common.running') : t('common.stopped')}</span>
                  </div>
                </div>
                
                <div className="flex items-center gap-4">
                  <div className="flex flex-col gap-1">
                    <span className="text-[10px] font-bold text-zinc-500 uppercase tracking-tighter">{t('common.port')}</span>
                    <input
                      type="number"
                      value={proxyPort}
                      onChange={(e) => onProxyPortChange?.(parseInt(e.target.value) || 0)}
                      disabled={proxyRunning}
                      className="w-20 bg-zinc-900 border border-zinc-800 rounded-lg px-2 py-1 text-sm font-mono text-zinc-200 focus:outline-none focus:ring-1 focus:ring-blue-500/50 disabled:opacity-50 transition-all"
                    />
                  </div>
                  <button
                    onClick={onToggleProxy}
                    className={`flex items-center gap-2 px-4 py-2 rounded-xl text-xs font-bold transition-all ml-auto ${
                      proxyRunning
                        ? 'bg-red-500/10 text-red-400 border border-red-500/20 hover:bg-red-500/20'
                        : 'bg-green-500/10 text-green-400 border border-green-500/20 hover:bg-green-500/20'
                    }`}
                  >
                    {proxyRunning ? (
                      <><ShieldOff className="w-3.5 h-3.5" /> {t('common.stop')}</>
                    ) : (
                      <><Shield className="w-3.5 h-3.5" /> {t('common.start')}</>
                    )}
                  </button>
                </div>

                <div className="mt-4 flex items-center justify-between pt-3 border-t border-zinc-800/50">
                  <div className="flex flex-col">
                    <span className="text-[10px] font-bold text-zinc-400 uppercase">{t('settings.proxy_logging')}</span>
                    <span className="text-[9px] text-zinc-600 truncate max-w-[150px]">{t('settings.proxy_logging_hint')}</span>
                  </div>
                  <button
                    onClick={onToggleProxyLogging}
                    className={`relative inline-flex h-4 w-8 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none ${
                      proxyLoggingEnabled ? 'bg-green-500/80' : 'bg-zinc-700'
                    }`}
                  >
                    <span
                      className={`pointer-events-none inline-block h-3 w-3 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out ${
                        proxyLoggingEnabled ? 'translate-x-4' : 'translate-x-0'
                      }`}
                    />
                  </button>
                </div>
              </div>
              <p className="mt-4 text-[10px] text-zinc-500 leading-relaxed italic">
                {t('settings.proxy_hint', { port: proxyPort })}
              </p>
            </div>

            {/* Gateway Settings */}
            <div className="bg-zinc-900/30 rounded-2xl p-5 border border-zinc-800/50 flex flex-col justify-between min-h-[160px]">
              <div>
                <div className="flex items-center justify-between mb-4">
                  <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
                    <Server className="w-4 h-4 text-purple-400" />
                    {t('settings.gateway')}
                  </h3>
                  <div className="flex items-center gap-2 px-2 py-0.5 rounded-full bg-zinc-950/50 border border-zinc-800/50">
                    <div className={`w-1.5 h-1.5 rounded-full ${gatewayRunning ? 'bg-green-500 animate-pulse' : 'bg-zinc-600'}`} />
                    <span className="text-[10px] font-bold text-zinc-400 uppercase">{gatewayRunning ? t('common.running') : t('common.stopped')}</span>
                  </div>
                </div>

                <div className="flex items-center gap-3">
                  <div className="flex flex-col gap-1 min-w-0 flex-1">
                    <span className="text-[10px] font-bold text-zinc-500 uppercase tracking-tighter">{t('settings.listen_addr')}</span>
                    <select
                      value={gatewayListenAddr}
                      onChange={(e) => onGatewayListenAddrChange?.(e.target.value)}
                      disabled={gatewayRunning}
                      className="w-full bg-zinc-900 border border-zinc-800 rounded-lg px-2 py-1 text-xs font-mono text-zinc-200 focus:outline-none focus:ring-1 focus:ring-purple-500/50 disabled:opacity-50 transition-all truncate"
                    >
                      {networkInterfaces.length > 0 ? (
                        networkInterfaces.map((iface) => (
                          <option key={iface.addr} value={iface.addr}>
                            {iface.name} ({iface.addr})
                          </option>
                        ))
                      ) : (
                        <>
                          <option value="127.0.0.1">127.0.0.1 (Local)</option>
                          <option value="0.0.0.0">0.0.0.0 (All)</option>
                        </>
                      )}
                    </select>
                  </div>
                  <div className="flex flex-col gap-1 shrink-0">
                    <span className="text-[10px] font-bold text-zinc-500 uppercase tracking-tighter">{t('common.port')}</span>
                    <input
                      type="number"
                      value={gatewayPort}
                      onChange={(e) => onGatewayPortChange?.(parseInt(e.target.value) || 0)}
                      disabled={gatewayRunning}
                      className="w-16 bg-zinc-900 border border-zinc-800 rounded-lg px-2 py-1 text-xs font-mono text-zinc-200 focus:outline-none focus:ring-1 focus:ring-purple-500/50 disabled:opacity-50 transition-all"
                    />
                  </div>
                  <button
                    onClick={onToggleGateway}
                    className={`flex items-center gap-2 px-3 py-1.5 rounded-xl text-xs font-bold transition-all shrink-0 ${
                      gatewayRunning
                        ? 'bg-red-500/10 text-red-400 border border-red-500/20 hover:bg-red-500/20'
                        : 'bg-green-500/10 text-green-400 border border-green-500/20 hover:bg-green-500/20'
                    }`}
                  >
                    {gatewayRunning ? (
                      <><ServerOff className="w-3.5 h-3.5" /> {t('common.stop')}</>
                    ) : (
                      <><Server className="w-3.5 h-3.5" /> {t('common.start')}</>
                    )}
                  </button>
                </div>
                {gatewayListenAddr === '0.0.0.0' && (
                  <div className="mt-3 flex items-start gap-2 p-2 bg-amber-500/10 border border-amber-500/20 rounded-lg">
                    <span className="text-amber-400 text-xs">⚠️</span>
                    <span className="text-[10px] text-amber-300/90 leading-relaxed">{t('settings.listen_addr_warning')}</span>
                  </div>
                )}
              </div>
              
              <div className="mt-4 flex items-center justify-between pt-3 border-t border-zinc-800/50">
                <div className="flex flex-col">
                  <span className="text-[10px] font-bold text-zinc-400 uppercase">{t('settings.gateway_logging')}</span>
                  <span className="text-[9px] text-zinc-600 truncate max-w-[150px]">{t('settings.gateway_logging_hint')}</span>
                </div>
                <button
                  onClick={onToggleLogging}
                  className={`relative inline-flex h-4 w-8 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none ${
                    loggingEnabled ? 'bg-green-500/80' : 'bg-zinc-700'
                  }`}
                >
                  <span
                    className={`pointer-events-none inline-block h-3 w-3 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out ${
                      loggingEnabled ? 'translate-x-4' : 'translate-x-0'
                    }`}
                  />
                </button>
              </div>

              <div className="mt-3 flex items-center justify-between pt-3 border-t border-zinc-800/50">
                <div className="flex flex-col min-w-0 pr-3">
                  <span className="text-[10px] font-bold text-zinc-400 uppercase">{t('settings.lingma_http2')}</span>
                  <span className="text-[9px] text-zinc-600 leading-snug">{t('settings.lingma_http2_hint')}</span>
                </div>
                <button
                  onClick={onToggleLingmaHTTP2}
                  className={`relative inline-flex h-4 w-8 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none ${
                    lingmaHTTP2Enabled ? 'bg-green-500/80' : 'bg-zinc-700'
                  }`}
                >
                  <span
                    className={`pointer-events-none inline-block h-3 w-3 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out ${
                      lingmaHTTP2Enabled ? 'translate-x-4' : 'translate-x-0'
                    }`}
                  />
                </button>
              </div>

            </div>
          </div>
        </section>

        {/* CA Certificate */}
        {caCertPath && (
          <section>
            <h2 className="text-sm font-semibold text-zinc-200 mb-4 uppercase tracking-widest opacity-60">{t('settings.ca_section')}</h2>
            <div className="bg-zinc-900/30 rounded-2xl p-5 border border-zinc-800/50">
              <div className="flex items-center justify-between mb-3">
                <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
                  <FileKey className="w-4 h-4 text-amber-400" />
                  {t('settings.ca_title')}
                </h3>
              </div>
              <p className="text-[11px] text-zinc-400 leading-relaxed mb-3">
                {t('settings.ca_hint')}
              </p>
              <div className="flex items-center gap-2 bg-zinc-950/50 border border-zinc-800/50 rounded-lg px-3 py-2 mb-3">
                <span className="text-[11px] font-mono text-zinc-300 truncate flex-1" title={caCertPath}>{caCertPath}</span>
                <button
                  onClick={() => copyToClipboard(caCertPath)}
                  className="p-1 rounded text-zinc-500 hover:text-zinc-200 hover:bg-zinc-800 transition-colors"
                  title={t('common.copy')}
                >
                  {copied === caCertPath ? <Check className="w-3.5 h-3.5 text-green-400" /> : <Copy className="w-3.5 h-3.5" />}
                </button>
              </div>
              <div className="flex items-center gap-2">
                <button
                  onClick={handleRevealCACert}
                  disabled={revealLoading}
                  className="flex items-center gap-2 px-3 py-1.5 bg-zinc-800 hover:bg-zinc-700 text-zinc-200 rounded-lg text-[11px] font-bold transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  <FolderOpen className={`w-3.5 h-3.5 ${revealLoading ? 'animate-pulse' : ''}`} />
                  {t('settings.ca_reveal')}
                </button>
              </div>
              <p className="mt-3 text-[10px] text-zinc-500 leading-relaxed">
                {t('settings.ca_install_steps')}
              </p>
            </div>
          </section>
        )}

        {/* Models */}
        <section>
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-sm font-semibold text-zinc-200">{t('settings.models')}</h2>
            <button
              onClick={fetchModels}
              disabled={modelsLoading}
              className="p-1.5 rounded text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors disabled:opacity-50"
              title={t('settings.refresh_models')}
            >
              <RefreshCw className={`w-3.5 h-3.5 ${modelsLoading ? 'animate-spin' : ''}`} />
            </button>
          </div>

          {modelsError && (
            <p className="text-xs text-red-400 mb-2">{modelsError}</p>
          )}

          <div className="bg-zinc-900/50 rounded-lg border border-zinc-800 overflow-hidden">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-zinc-800">
                  <th className="text-left px-4 py-2 text-zinc-400 font-medium">{t('settings.friendly_name')}</th>
                  <th className="text-left px-4 py-2 text-zinc-400 font-medium">{t('settings.model_id')}</th>
                </tr>
              </thead>
              <tbody>
                {models.map((m) => (
                  <tr key={m.id} className="border-b border-zinc-800/50 hover:bg-zinc-800/30">
                    <td className="px-4 py-2 text-zinc-200">{m.display_name || m.id}</td>
                    <td className="px-4 py-2 text-zinc-500 font-mono">{m.id}</td>
                  </tr>
                ))}
                {models.length === 0 && !modelsLoading && (
                  <tr>
                    <td colSpan={2} className="px-4 py-3 text-zinc-600 text-center">{t('settings.no_models')}</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>

        {/* API Endpoints */}
        <section>
          <h2 className="text-sm font-semibold text-zinc-200 mb-4">{t('settings.api_endpoints')}</h2>
          <div className="space-y-2">
            {ENDPOINTS.map((ep) => {
              const url = `http://127.0.0.1:${gatewayPort}${ep.path}`;
              return (
                <div
                  key={ep.path}
                  className="flex items-center gap-3 bg-zinc-900/50 rounded-lg px-4 py-2.5 border border-zinc-800"
                >
                  <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${
                    ep.method === 'GET' ? 'bg-green-900/50 text-green-400' : 'bg-blue-900/50 text-blue-400'
                  }`}>
                    {ep.method}
                  </span>
                  <span className="text-xs font-mono text-zinc-300 flex-1">{ep.path}</span>
                  <span className="text-[10px] text-zinc-500">{ep.desc}</span>
                  <button
                    onClick={() => copyToClipboard(url)}
                    className="p-1 rounded text-zinc-500 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
                    title={t('common.copy')}
                  >
                    {copied === url ? (
                      <Check className="w-3 h-3 text-green-400" />
                    ) : (
                      <Copy className="w-3 h-3" />
                    )}
                  </button>
                </div>
              );
            })}
          </div>
        </section>

        {/* Anthropic Mapping */}
        <section>
          <div className="flex items-center justify-between mb-4">
            <div>
              <h2 className="text-sm font-semibold text-zinc-200">{t('settings.anthropic_mapping')}</h2>
              <p className="text-[10px] text-zinc-500">{t('settings.anthropic_mapping_hint')}</p>
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={addMappingItem}
                className="px-3 py-1.5 bg-zinc-800 hover:bg-zinc-700 text-zinc-200 rounded-lg text-[10px] font-bold transition-colors"
              >
                {t('settings.add_mapping')}
              </button>
              <button
                onClick={handleSaveMapping}
                disabled={savingMapping}
                className="px-3 py-1.5 bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white rounded-lg text-[10px] font-bold transition-colors"
              >
                {t('settings.save_mapping')}
              </button>
            </div>
          </div>

          <div className="bg-zinc-900/50 rounded-xl border border-zinc-800 overflow-hidden">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-zinc-800 bg-zinc-900/80">
                  <th className="text-left px-4 py-3 text-zinc-400 font-medium w-1/3">{t('settings.keyword')}</th>
                  <th className="text-left px-4 py-3 text-zinc-400 font-medium">{t('settings.target_model')}</th>
                  <th className="w-10"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800/50">
                {Object.entries(anthropicMapping).map(([keyword, target]) => (
                  <tr key={keyword} className="hover:bg-zinc-800/30 group">
                    <td className="px-4 py-2">
                      <input
                        type="text"
                        defaultValue={keyword}
                        onBlur={(e) => updateMappingKey(keyword, e.target.value)}
                        className="w-full bg-transparent border-none focus:ring-0 text-zinc-200 font-mono text-xs placeholder-zinc-700"
                        placeholder="e.g. sonnet"
                      />
                    </td>
                    <td className="px-4 py-2">
                      <select
                        value={target}
                        onChange={(e) => updateMappingValue(keyword, e.target.value)}
                        className="w-full bg-transparent border-none focus:ring-0 text-zinc-300 text-xs appearance-none cursor-pointer"
                      >
                        {models.map(m => (
                          <option key={m.id} value={m.id} className="bg-zinc-900 text-zinc-200">{m.display_name || m.id}</option>
                        ))}
                      </select>
                    </td>
                    <td className="px-2">
                      <button
                        onClick={() => removeMappingItem(keyword)}
                        className="p-1 text-zinc-600 hover:text-red-400 opacity-0 group-hover:opacity-100 transition-all"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </td>
                  </tr>
                ))}
                
                {/* Fallback Model */}
                <tr className="bg-zinc-900/30 border-t border-zinc-800">
                  <td className="px-4 py-3 italic text-zinc-500">
                    <div className="flex flex-col">
                      <span className="text-[10px] font-bold text-zinc-400">{t('settings.anthropic_fallback')}</span>
                      <span className="text-[9px] opacity-60">{t('settings.anthropic_fallback_hint')}</span>
                    </div>
                  </td>
                  <td className="px-4 py-3" colSpan={2}>
                    <select
                      value={anthropicDefault}
                      onChange={(e) => setAnthropicDefault(e.target.value)}
                      className="w-full bg-transparent border-none focus:ring-0 text-zinc-200 text-xs font-bold appearance-none cursor-pointer"
                    >
                      {models.map(m => (
                        <option key={m.id} value={m.id} className="bg-zinc-900 text-zinc-200">{m.display_name || m.id}</option>
                      ))}
                    </select>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        {/* Data Management */}
        <section>
          <h2 className="text-sm font-semibold text-zinc-200 mb-4">{t('settings.data_management')}</h2>
          <div className="bg-zinc-900/30 rounded-2xl p-5 border border-zinc-800/50 space-y-4">
            {/* Stats */}
            {stats && (
              <div className="grid grid-cols-2 gap-4 mb-4">
                <div className="bg-zinc-950/50 rounded-lg p-3">
                  <div className="text-[10px] text-zinc-500 uppercase">{t('settings.records_count')}</div>
                  <div className="text-lg font-bold text-zinc-100">{stats.records}</div>
                </div>
                <div className="bg-zinc-950/50 rounded-lg p-3">
                  <div className="text-[10px] text-zinc-500 uppercase">{t('settings.sessions_count')}</div>
                  <div className="text-lg font-bold text-zinc-100">{stats.sessions}</div>
                </div>
              </div>
            )}

            {/* Clear All */}
            {!confirmAll ? (
              <div className="flex items-center justify-between">
                <div>
                  <div className="text-xs font-bold text-zinc-200">{t('settings.clear_all')}</div>
                  <div className="text-[10px] text-zinc-500">{t('settings.clear_all_hint')}</div>
                </div>
                <button
                  onClick={() => setConfirmAll(true)}
                  disabled={clearLoading}
                  className="flex items-center gap-2 px-4 py-2 rounded-xl text-xs font-bold bg-red-500/10 text-red-400 border border-red-500/20 hover:bg-red-500/20 transition-all disabled:opacity-50"
                >
                  <Trash2 className="w-3.5 h-3.5" />
                  {t('common.clear')}
                </button>
              </div>
            ) : (
              <div className="flex items-center justify-between p-3 rounded-xl bg-red-500/5 border border-red-500/20 animate-fade-in">
                <div className="flex items-center gap-2 text-xs font-medium text-red-400">
                  <span className="text-sm">⚠️</span>
                  <span>确定要清空所有流量记录吗？此操作不可恢复。</span>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => setConfirmAll(false)}
                    className="px-3 py-1.5 rounded-lg text-xs font-medium bg-zinc-900 text-zinc-300 border border-zinc-800 hover:bg-zinc-800 transition-all"
                  >
                    取消
                  </button>
                  <button
                    onClick={async () => {
                      setConfirmAll(false);
                      await handleClearAll();
                    }}
                    disabled={clearLoading}
                    className="px-3 py-1.5 rounded-lg text-xs font-bold bg-red-500 text-white hover:bg-red-600 transition-all disabled:opacity-50"
                  >
                    确定清空
                  </button>
                </div>
              </div>
            )}

            {/* Clear Before */}
            <div className="flex flex-col gap-2 pt-3 border-t border-zinc-800/50">
              {!confirmBefore ? (
                <div className="flex items-center justify-between">
                  <div>
                    <div className="text-xs font-bold text-zinc-200">{t('settings.clear_before')}</div>
                    <div className="text-[10px] text-zinc-500">{t('settings.clear_before_hint')}</div>
                  </div>
                  <div className="flex items-center gap-2">
                    <input
                      type="number"
                      value={clearDays}
                      placeholder="30"
                      onChange={(e) => {
                        const val = e.target.value;
                        if (val === '') {
                          setClearDays('');
                        } else {
                          const parsed = parseInt(val, 10);
                          if (!isNaN(parsed)) {
                            setClearDays(parsed);
                          }
                        }
                      }}
                      min={1}
                      max={365}
                      className="w-16 bg-zinc-900 border border-zinc-800 rounded-lg px-2 py-1 text-xs font-mono text-zinc-200 focus:outline-none focus:ring-1 focus:ring-amber-500/50"
                    />
                    <span className="text-xs text-zinc-400">{t('settings.days')}</span>
                    <button
                      onClick={() => setConfirmBefore(true)}
                      disabled={clearLoading}
                      className="flex items-center gap-2 px-4 py-2 rounded-xl text-xs font-bold bg-amber-500/10 text-amber-400 border border-amber-500/20 hover:bg-amber-500/20 transition-all disabled:opacity-50"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                      {t('common.clear')}
                    </button>
                  </div>
                </div>
              ) : (
                <div className="flex items-center justify-between p-3 rounded-xl bg-amber-500/5 border border-amber-500/20 animate-fade-in">
                  <div className="flex items-center gap-2 text-xs font-medium text-amber-400">
                    <span className="text-sm">⚠️</span>
                    <span>确定要删除 {typeof clearDays === 'number' ? clearDays : 30} 天前的所有记录吗？</span>
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => setConfirmBefore(false)}
                      className="px-3 py-1.5 rounded-lg text-xs font-medium bg-zinc-900 text-zinc-300 border border-zinc-800 hover:bg-zinc-800 transition-all"
                    >
                      取消
                    </button>
                    <button
                      onClick={async () => {
                        setConfirmBefore(false);
                        await handleClearBefore();
                      }}
                      disabled={clearLoading}
                      className="px-3 py-1.5 rounded-lg text-xs font-bold bg-amber-500 text-zinc-950 hover:bg-amber-600 hover:text-white transition-all disabled:opacity-50"
                    >
                      确定删除
                    </button>
                  </div>
                </div>
              )}

              {/* Dynamic preview hint */}
              <div className="text-[10px] text-zinc-500 bg-zinc-950/30 rounded-lg p-2.5 border border-zinc-800/40 flex flex-col gap-1.5 leading-relaxed">
                <div>
                  <span className="text-amber-500/90 font-medium mr-1">⚠️ {t('settings.clear_range')}:</span>
                  {t('settings.clear_before_preview_delete', {
                    date: getCutoffDate(typeof clearDays === 'number' ? clearDays : 30),
                  })}
                </div>
                <div>
                  <span className="text-emerald-400/90 font-medium mr-1">✨ {t('settings.keep_range')}:</span>
                  {t('settings.clear_before_preview_keep', {
                    days: typeof clearDays === 'number' ? clearDays : 30,
                    date: getCutoffDate(typeof clearDays === 'number' ? clearDays : 30),
                  })}
                </div>
              </div>
            </div>

            {/* Message */}
            {clearMsg && (
              <div className={`text-xs p-2 rounded-lg ${
                clearMsg.includes('错误') || clearMsg.includes('Error')
                  ? 'bg-red-500/10 text-red-400'
                  : 'bg-green-500/10 text-green-400'
              }`}>
                {clearMsg}
              </div>
            )}
          </div>
        </section>


        {/* Version & Update */}
        <section>
          <h2 className="text-sm font-semibold text-zinc-200 mb-4 uppercase tracking-widest opacity-60">{t('settings.about')}</h2>
          <div className="bg-zinc-900/30 rounded-2xl p-5 border border-zinc-800/50">
            <div className="flex items-center justify-between">
              <div className="flex flex-col">
                <span className="text-xs font-bold text-zinc-200">{t('settings.current_version')}</span>
                <span className="text-[10px] text-zinc-500">{t('settings.version_hint')}</span>
              </div>
              <div className="flex items-center gap-3">
                <span className="text-sm font-mono text-zinc-300 bg-zinc-950/50 px-3 py-1 rounded-lg border border-zinc-800/50">
                  v{currentVersion || '...'}
                </span>
                <button
                  onClick={checkForUpdate}
                  disabled={updateStatus === 'checking'}
                  className="flex items-center gap-2 px-3 py-1.5 bg-zinc-800 hover:bg-zinc-700 text-zinc-200 rounded-lg text-[11px] font-bold transition-colors disabled:opacity-50"
                >
                  <RefreshCw className={`w-3.5 h-3.5 ${updateStatus === 'checking' ? 'animate-spin' : ''}`} />
                  {updateStatus === 'checking' ? t('settings.checking') : t('settings.check_update')}
                </button>
              </div>
            </div>

            {updateStatus === 'latest' && (
              <div className="mt-3 text-xs text-green-400 bg-green-500/10 rounded-lg px-3 py-2">
                {t('settings.already_latest')}
              </div>
            )}

            {updateStatus === 'available' && (
              <div className="mt-3 flex items-center justify-between bg-amber-500/10 border border-amber-500/20 rounded-lg px-3 py-2">
                <div className="flex items-center gap-2">
                  <span className="text-xs text-amber-400">
                    {t('settings.new_version_available', { version: latestVersion })}
                  </span>
                </div>
                <button
                  onClick={openReleasePage}
                  className="flex items-center gap-1.5 px-3 py-1 bg-amber-500/20 hover:bg-amber-500/30 text-amber-400 rounded-lg text-[11px] font-bold transition-colors"
                >
                  <ExternalLink className="w-3.5 h-3.5" />
                  {t('settings.go_to_releases')}
                </button>
              </div>
            )}

            {updateStatus === 'error' && (
              <div className="mt-3 text-xs text-red-400 bg-red-500/10 rounded-lg px-3 py-2">
                {t('settings.check_update_failed')}: {updateError}
              </div>
            )}
          </div>
        </section>

      </div>
    </div>
  );
}
