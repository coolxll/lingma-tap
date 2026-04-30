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
  gatewayRunning?: boolean;
  gatewayPort?: number;
  onToggleGateway?: () => void;
  gatewayLoggingEnabled?: boolean;
  onToggleGatewayLogging?: () => void;
}

export function SettingsPanel({ 
  proxyRunning, 
  proxyPort, 
  onToggleProxy,
  gatewayRunning = false,
  gatewayPort = 8080,
  onToggleGateway,
  gatewayLoggingEnabled = true,
  onToggleGatewayLogging
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
          <h2 className="text-sm font-semibold text-zinc-200 mb-4">{t('common.settings_tab')}</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {/* Proxy Settings */}
            <div className="bg-zinc-900/50 rounded-lg p-4 border border-zinc-800">
              <div className="flex items-center gap-4 mb-3">
                <h3 className="text-sm font-medium text-zinc-300">{t('common.proxy')}</h3>
                <div className="flex items-center gap-2 ml-auto">
                  <div className={`w-2 h-2 rounded-full ${proxyRunning ? 'bg-green-500' : 'bg-zinc-600'}`} />
                  <span className="text-xs text-zinc-400">{proxyRunning ? t('common.running') : t('common.stopped')}</span>
                </div>
              </div>
              <div className="flex items-center gap-4">
                <div className="flex items-center gap-2">
                  <span className="text-xs text-zinc-400">{t('common.port')}</span>
                  <span className="px-2 py-1 bg-zinc-800 rounded text-sm font-mono text-zinc-200">
                    {proxyPort}
                  </span>
                </div>
                <button
                  onClick={onToggleProxy}
                  className={`flex items-center gap-1.5 px-3 py-1.5 rounded text-xs font-medium transition-colors ml-auto ${
                    proxyRunning
                      ? 'bg-red-900/50 text-red-400 hover:bg-red-900/70'
                      : 'bg-green-900/50 text-green-400 hover:bg-green-900/70'
                  }`}
                >
                  {proxyRunning ? (
                    <><ShieldOff className="w-3.5 h-3.5" /> {t('common.stop')}</>
                  ) : (
                    <><Shield className="w-3.5 h-3.5" /> {t('common.start')}</>
                  )}
                </button>
              </div>
              <p className="mt-3 text-[10px] text-zinc-600">
                {t('settings.proxy_hint', { port: proxyPort })}
              </p>
            </div>

            {/* Gateway Settings */}
            <div className="bg-zinc-900/50 rounded-lg p-4 border border-zinc-800">
              <div className="flex items-center gap-4 mb-3">
                <h3 className="text-sm font-medium text-zinc-300">{t('settings.gateway')}</h3>
                <div className="flex items-center gap-2 ml-auto">
                  <div className={`w-2 h-2 rounded-full ${gatewayRunning ? 'bg-green-500' : 'bg-zinc-600'}`} />
                  <span className="text-xs text-zinc-400">{gatewayRunning ? t('common.running') : t('common.stopped')}</span>
                </div>
              </div>
              <div className="flex items-center gap-4">
                <div className="flex items-center gap-2">
                  <span className="text-xs text-zinc-400">{t('common.port')}</span>
                  <span className="px-2 py-1 bg-zinc-800 rounded text-sm font-mono text-zinc-200">
                    {gatewayPort}
                  </span>
                </div>
                <button
                  onClick={onToggleGateway}
                  className={`flex items-center gap-1.5 px-3 py-1.5 rounded text-xs font-medium transition-colors ml-auto ${
                    gatewayRunning
                      ? 'bg-red-900/50 text-red-400 hover:bg-red-900/70'
                      : 'bg-green-900/50 text-green-400 hover:bg-green-900/70'
                  }`}
                >
                  {gatewayRunning ? (
                    <><ServerOff className="w-3.5 h-3.5" /> {t('common.stop')}</>
                  ) : (
                    <><Server className="w-3.5 h-3.5" /> {t('common.start')}</>
                  )}
                </button>
              </div>
              <p className="mt-3 text-[10px] text-zinc-600">
                {gatewayRunning 
                  ? t('settings.gateway_running', { port: gatewayPort }) 
                  : t('settings.gateway_stopped')}
              </p>

              {/* Logging Toggle */}
              <div className="mt-4 pt-4 border-t border-zinc-800">
                <div className="flex items-center justify-between">
                  <div className="pr-4">
                    <span className="block text-xs font-medium text-zinc-300">{t('settings.gateway_logging')}</span>
                    <p className="text-[10px] text-zinc-600 mt-1">
                      {t('settings.gateway_logging_hint')}
                    </p>
                  </div>
                  <button
                    onClick={onToggleGatewayLogging}
                    className={`relative inline-flex h-5 w-10 shrink-0 cursor-pointer items-center rounded-full transition-colors duration-200 ease-in-out focus:outline-none ${
                      gatewayLoggingEnabled ? 'bg-blue-600' : 'bg-zinc-700'
                    }`}
                  >
                    <span
                      className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform duration-200 ease-in-out ${
                        gatewayLoggingEnabled ? 'translate-x-5.5' : 'translate-x-1'
                      }`}
                    />
                  </button>
                </div>
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
              const url = `http://127.0.0.1:9090${ep.path}`;
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
