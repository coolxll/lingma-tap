import { useState, useEffect, useCallback } from 'react';
import { RefreshCw, Copy, Check, Shield, ShieldOff, Server, ServerOff, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';

// Wails window type
interface WailsWindow extends Window {
  go?: {
    main?: {
      App?: {
        GetModels: () => Promise<any[]>;
        ClearRecords: () => Promise<void>;
        ClearRecordsBefore: (days: number) => Promise<number>;
        GetAnthropicMapping: () => Promise<any>;
        SaveAnthropicMapping: (mapping: Record<string, string>, defaultModel: string) => Promise<void>;
      };
    };
  };
}

interface ModelInfo {
  id: string;         // key from Lingma (e.g. "dashscope_qwen3_coder")
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
  onToggleGateway?: () => void;
  onGatewayPortChange?: (port: number) => void;
  loggingEnabled?: boolean;
  onToggleLogging?: () => void;
  stats?: StorageStats | null;
  onClearAll?: () => void;
  onClearBefore?: (days: number) => Promise<number>;
}

export function SettingsPanel({
  proxyRunning,
  proxyPort,
  onToggleProxy,
  onProxyPortChange,
  gatewayRunning = false,
  gatewayPort = 8080,
  onToggleGateway,
  onGatewayPortChange,
  loggingEnabled,
  onToggleLogging,
  stats,
  onClearAll,
  onClearBefore,
}: SettingsPanelProps) {
  const { t } = useTranslation();
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState('');
  const [copied, setCopied] = useState<string | null>(null);
  const [clearDays, setClearDays] = useState(30);
  const [clearMsg, setClearMsg] = useState('');
  const [clearLoading, setClearLoading] = useState(false);
  const [anthropicMapping, setAnthropicMapping] = useState<Record<string, string>>({});
  const [anthropicDefault, setAnthropicDefault] = useState('dashscope_qmodel');
  const [savingMapping, setSavingMapping] = useState(false);

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
    fetchModels();
    fetchAnthropicMapping();
  }, [fetchModels, fetchAnthropicMapping]);

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
    if (!window.confirm('确定要清空所有流量记录吗？此操作不可恢复。')) return;
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
    if (!window.confirm(`确定要删除 ${clearDays} 天前的所有记录吗？此操作不可恢复。`)) return;
    setClearLoading(true);
    setClearMsg('');
    try {
      const deleted = await onClearBefore(clearDays);
      setClearMsg(`已删除 ${deleted} 条记录`);
      setTimeout(() => setClearMsg(''), 3000);
    } catch (err) {
      setClearMsg(`错误: ${err instanceof Error ? err.message : '未知错误'}`);
    } finally {
      setClearLoading(false);
    }
  }, [onClearBefore, clearDays]);

  return (
    <div className="h-full overflow-y-auto bg-zinc-950 p-6">
      <div className="max-w-2xl space-y-8">

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

                <div className="flex items-center gap-4">
                  <div className="flex flex-col gap-1">
                    <span className="text-[10px] font-bold text-zinc-500 uppercase tracking-tighter">{t('common.port')}</span>
                    <input
                      type="number"
                      value={gatewayPort}
                      onChange={(e) => onGatewayPortChange?.(parseInt(e.target.value) || 0)}
                      disabled={gatewayRunning}
                      className="w-20 bg-zinc-900 border border-zinc-800 rounded-lg px-2 py-1 text-sm font-mono text-zinc-200 focus:outline-none focus:ring-1 focus:ring-purple-500/50 disabled:opacity-50 transition-all"
                    />
                  </div>
                  <button
                    onClick={onToggleGateway}
                    className={`flex items-center gap-2 px-4 py-2 rounded-xl text-xs font-bold transition-all ml-auto ${
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
            </div>
          </div>
        </section>

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
            <div className="flex items-center justify-between">
              <div>
                <div className="text-xs font-bold text-zinc-200">{t('settings.clear_all')}</div>
                <div className="text-[10px] text-zinc-500">{t('settings.clear_all_hint')}</div>
              </div>
              <button
                onClick={handleClearAll}
                disabled={clearLoading}
                className="flex items-center gap-2 px-4 py-2 rounded-xl text-xs font-bold bg-red-500/10 text-red-400 border border-red-500/20 hover:bg-red-500/20 transition-all disabled:opacity-50"
              >
                <Trash2 className="w-3.5 h-3.5" />
                {t('common.clear')}
              </button>
            </div>

            {/* Clear Before */}
            <div className="flex items-center justify-between pt-3 border-t border-zinc-800/50">
              <div>
                <div className="text-xs font-bold text-zinc-200">{t('settings.clear_before')}</div>
                <div className="text-[10px] text-zinc-500">{t('settings.clear_before_hint')}</div>
              </div>
              <div className="flex items-center gap-2">
                <input
                  type="number"
                  value={clearDays}
                  onChange={(e) => setClearDays(parseInt(e.target.value) || 30)}
                  min={1}
                  max={365}
                  className="w-16 bg-zinc-900 border border-zinc-800 rounded-lg px-2 py-1 text-xs font-mono text-zinc-200 focus:outline-none focus:ring-1 focus:ring-amber-500/50"
                />
                <span className="text-xs text-zinc-400">{t('settings.days')}</span>
                <button
                  onClick={handleClearBefore}
                  disabled={clearLoading}
                  className="flex items-center gap-2 px-4 py-2 rounded-xl text-xs font-bold bg-amber-500/10 text-amber-400 border border-amber-500/20 hover:bg-amber-500/20 transition-all disabled:opacity-50"
                >
                  <Trash2 className="w-3.5 h-3.5" />
                  {t('common.clear')}
                </button>
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


      </div>
    </div>
  );
}
