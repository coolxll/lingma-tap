import { useState, useEffect, useCallback } from 'react';
import { RefreshCw, Copy, Check, Shield, ShieldOff, Server, ServerOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';

// Wails window type
interface WailsWindow extends Window {
  go?: {
    main?: {
      App?: {
        GetModels: () => Promise<any[]>;
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
  onToggleLogging
}: SettingsPanelProps) {
  const { t } = useTranslation();
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState('');
  const [copied, setCopied] = useState<string | null>(null);

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

  useEffect(() => {
    fetchModels();
  }, [fetchModels]);

  const copyToClipboard = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(text);
      setTimeout(() => setCopied(null), 1500);
    } catch {
      // ignore
    }
  };

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

      </div>
    </div>
  );
}
