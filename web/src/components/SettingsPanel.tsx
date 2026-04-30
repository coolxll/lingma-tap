import { useState, useEffect, useCallback } from 'react';
import { RefreshCw, Copy, Check, Shield, ShieldOff } from 'lucide-react';

interface ModelInfo {
  id: string;
  object: string;
  display_name?: string;
  owned_by: string;
}

interface SettingsPanelProps {
  proxyRunning: boolean;
  proxyPort: number;
  onToggleProxy: () => void;
}

const API_BASE = 'http://localhost:9090';

const ENDPOINTS = [
  { method: 'GET', path: '/v1/models', desc: 'Model list' },
  { method: 'POST', path: '/v1/chat/completions', desc: 'OpenAI Chat' },
  { method: 'POST', path: '/v1/responses', desc: 'OpenAI Responses' },
  { method: 'POST', path: '/v1/messages', desc: 'Anthropic Messages' },
];

export function SettingsPanel({ proxyRunning, proxyPort, onToggleProxy }: SettingsPanelProps) {
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState('');
  const [copied, setCopied] = useState<string | null>(null);

  const fetchModels = useCallback(async () => {
    setModelsLoading(true);
    setModelsError('');
    try {
      const resp = await fetch(`${API_BASE}/v1/models`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      setModels(data.data || []);
    } catch (err) {
      setModelsError(err instanceof Error ? err.message : 'Failed to fetch');
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

        {/* Proxy Settings */}
        <section>
          <h2 className="text-sm font-semibold text-zinc-200 mb-4">Proxy</h2>
          <div className="bg-zinc-900/50 rounded-lg p-4 border border-zinc-800">
            <div className="flex items-center gap-4">
              <div className="flex items-center gap-2">
                <span className="text-xs text-zinc-400">Port</span>
                <span className="px-2 py-1 bg-zinc-800 rounded text-sm font-mono text-zinc-200">
                  {proxyPort}
                </span>
              </div>
              <div className="flex items-center gap-2">
                <div className={`w-2 h-2 rounded-full ${proxyRunning ? 'bg-green-500' : 'bg-zinc-600'}`} />
                <span className="text-xs text-zinc-400">{proxyRunning ? 'Running' : 'Stopped'}</span>
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
                  <><ShieldOff className="w-3.5 h-3.5" /> Stop</>
                ) : (
                  <><Shield className="w-3.5 h-3.5" /> Start</>
                )}
              </button>
            </div>
            <p className="mt-3 text-[10px] text-zinc-600">
              Set system proxy to 127.0.0.1:{proxyPort} to capture Lingma traffic
            </p>
          </div>
        </section>

        {/* Models */}
        <section>
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-sm font-semibold text-zinc-200">Models</h2>
            <button
              onClick={fetchModels}
              disabled={modelsLoading}
              className="p-1.5 rounded text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors disabled:opacity-50"
              title="Refresh models"
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
                  <th className="text-left px-4 py-2 text-zinc-400 font-medium">Name</th>
                  <th className="text-left px-4 py-2 text-zinc-400 font-medium">ID</th>
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
                    <td colSpan={2} className="px-4 py-3 text-zinc-600 text-center">No models</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>

        {/* API Endpoints */}
        <section>
          <h2 className="text-sm font-semibold text-zinc-200 mb-4">API Endpoints</h2>
          <div className="space-y-2">
            {ENDPOINTS.map((ep) => {
              const url = `${API_BASE}${ep.path}`;
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
                    title="Copy URL"
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
