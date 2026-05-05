import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { TrafficRecord, StorageStats } from '@/lib/types';
import { WSClient } from '@/lib/ws-client';
import { useRecords } from '@/hooks/useRecords';
import { TitleBar, TabId } from '@/components/TitleBar';
import { RecordList } from '@/components/RecordList';
import { DetailPanel } from '@/components/DetailPanel';
import { ResizablePanels } from '@/components/ResizablePanels';
import { BottomDock } from '@/components/BottomDock';
import { SettingsPanel } from '@/components/SettingsPanel';
import { GatewayMonitor } from '@/components/GatewayMonitor';

// Wails window type
interface WailsWindow extends Window {
  go?: {
    main?: {
      App?: {
        StartProxy: (port: number) => Promise<void>;
        StopProxy: () => Promise<void>;
        StartGateway: (port: number) => Promise<void>;
        StopGateway: () => Promise<void>;
        GetRecords: (limit: number, offset?: number) => Promise<TrafficRecord[]>;
        GetGatewayLogs: (limit: number) => Promise<any[]>;
        ClearRecords: () => Promise<void>;
        ClearRecordsBefore: (days: number) => Promise<number>;
        GetCACertPath: () => Promise<string>;
        GetStatus: () => Promise<Record<string, unknown>>;
        SetLogging: (enabled: boolean) => Promise<void>;
        GetModels: () => Promise<ModelInfo[]>;
      };
    };
  };
}

interface ModelInfo {
  key: string;
  display_name?: string;
  object: string;
  owned_by: string;
}

const WS_PORT = 9091;
const PROXY_PORT = 9528;
const DEFAULT_GATEWAY_PORT = 9090;

export default function App() {
  const {
    records,
    selectedRecord,
    setSelectedRecord,
    isPaused,
    liveTail,
    appendRecord,
    updateRecords,
    clearRecords,
    togglePause,
    toggleLiveTail,
    appendRecords,  } = useRecords();

  const [activeTab, setActiveTab] = useState<TabId>('proxy');
  const [connected, setConnected] = useState(false);
  const [proxyRunning, setProxyRunning] = useState(false);
  const [proxyPort, setProxyPort] = useState(PROXY_PORT);
  const [gatewayRunning, setGatewayRunning] = useState(false);
  const [gatewayPort, setGatewayPort] = useState(DEFAULT_GATEWAY_PORT);
  const [theme, setTheme] = useState<'dark' | 'light'>('dark');
  const [stats, setStats] = useState<StorageStats | null>(null);
  const [caCertPath, setCaCertPath] = useState('');
  const [gatewayLoggingEnabled, setGatewayLoggingEnabled] = useState(true);
  const [displayCount, setDisplayCount] = useState(200);
  const [canLoadMore, setCanLoadMore] = useState(true);
  const [proxyTypeFilter, setProxyTypeFilter] = useState<'all' | 'chat' | 'embedding' | 'other'>('all');
  const liveTailRef = useRef(liveTail);
  const selectedRef = useRef(selectedRecord);
  const recordsRef = useRef(records);

  // Computed records for active tab
  const displayedRecords = useMemo(() => {
    let result: TrafficRecord[];
    if (activeTab === 'proxy') {
      result = records.filter(r => {
        if (r.source !== 'proxy') return false;
        if (proxyTypeFilter === 'all') return true;
        if (proxyTypeFilter === 'chat') return r.endpoint_type === 'chat' || r.endpoint_type === 'finish';
        if (proxyTypeFilter === 'embedding') return r.endpoint_type === 'embedding';
        if (proxyTypeFilter === 'other') return r.endpoint_type === 'other' || r.endpoint_type === 'tracking';
        return true;
      });
    } else if (activeTab === 'gateway') {
      result = records.filter(r => (r as any).source === 'gateway');
    } else {
      result = records;
    }
    return result.slice(0, displayCount);
  }, [records, activeTab, displayCount, proxyTypeFilter]);

  useEffect(() => { liveTailRef.current = liveTail; }, [liveTail]);
  useEffect(() => { selectedRef.current = selectedRecord; }, [selectedRecord]);
  useEffect(() => { recordsRef.current = records; }, [records]);

  // Wails bindings
  const wails = (window as unknown as WailsWindow).go?.main?.App;

  const handleLoadMore = useCallback(async () => {
    if (!wails || !canLoadMore) return;
    // Count only proxy records for the offset
    const offset = recordsRef.current.filter(r => r.source === 'proxy').length;
    const newRecords = await wails.GetRecords(200, offset);
    if (newRecords && newRecords.length > 0) {
      appendRecords(newRecords);
      setDisplayCount(prev => prev + 200);
      if (newRecords.length < 200) {
        setCanLoadMore(false);
      }
    } else {
      setCanLoadMore(false);
    }
  }, [wails, canLoadMore, appendRecords]);

  // Find response record for the selected request
   const responseRecord = useMemo(() => {
    if (!selectedRecord || selectedRecord.direction === 'S2C') return null;
    // Don't assume the response is the immediate next record (interleaving possible)
    return records.find(r => r.session === selectedRecord.session && r.direction === 'S2C') || null;
  }, [selectedRecord, records]);

  // Apply theme
  useEffect(() => {
    if (theme === 'light') {
      document.documentElement.classList.remove('dark');
      document.documentElement.classList.add('light');
    } else {
      document.documentElement.classList.remove('light');
      document.documentElement.classList.add('dark');
    }
  }, [theme]);

  // Initialize: load existing records
  useEffect(() => {
    if (!wails) return;

    Promise.all([
      wails.GetRecords(200),
      wails.GetGatewayLogs ? wails.GetGatewayLogs(200) : Promise.resolve([]),
    ]).then(([proxyRecs, gatewayLogs]) => {
      const allRecords: TrafficRecord[] = [...(proxyRecs || [])];

      // Convert gateway logs to TrafficRecord format
      if (gatewayLogs && gatewayLogs.length > 0) {
        const gatewayRecords: TrafficRecord[] = gatewayLogs.map((log: any) => ({
          ts: log.ts,
          id: log.id || 0,
          session: log.session,
          direction: 'C2S' as const,
          source: 'gateway',
          method: log.method,
          path: log.path,
          endpoint_type: 'chat' as const,
          request_body: log.request_body || '',
          response_body: log.response_body || '',
          status: log.status || 0,
          is_sse: log.is_sse || false,
          sse_events: log.sse_events || [],
          model: log.model || '',
          input_tokens: log.input_tokens || 0,
          output_tokens: log.output_tokens || 0,
          latency: log.latency || 0,
          error: log.error || '',
          finish_reason: log.finish_reason || '',
          // Defaults for required fields
          index: 0,
          url: '',
          host: '',
          is_encoded: false,
          request_headers: {},
          request_body_raw: '',
          request_mime: '',
          request_size: 0,
          status_text: '',
          response_headers: {},
          response_mime: '',
          response_size: 0,
        }));
        allRecords.push(...gatewayRecords);
      }

      // Sort by timestamp (newest first)
      allRecords.sort((a, b) => new Date(b.ts).getTime() - new Date(a.ts).getTime());

      if (allRecords.length > 0) {
        updateRecords(allRecords);
        setSelectedRecord(allRecords[0]);
      }
    });

    wails.GetCACertPath().then(setCaCertPath);
    wails.GetStatus().then((s) => {
      const st = s?.stats as StorageStats | null;
      if (st) setStats(st);
      if (s?.proxy_running !== undefined) setProxyRunning(s.proxy_running as boolean);
      if (s?.gateway_running !== undefined) setGatewayRunning(s.gateway_running as boolean);
      if (s?.gateway_logging !== undefined) setGatewayLoggingEnabled(s.gateway_logging as boolean);
    });
  }, [wails, updateRecords, setSelectedRecord]);

  // WebSocket connection
  useEffect(() => {
    const wsUrl = `ws://localhost:${WS_PORT}/ws/records`;
    const client = new WSClient(
      wsUrl,
      (record) => {
        const rec = record as unknown as TrafficRecord;
        appendRecord(rec);
        if (liveTailRef.current) {
          setSelectedRecord(rec);
        }
      },
      setConnected,
      () => {
        // On reconnect, fetch latest records including gateway logs
        if (!wails) return;
        Promise.all([
          wails.GetRecords(200),
          wails.GetGatewayLogs ? wails.GetGatewayLogs(200) : Promise.resolve([]),
        ]).then(([proxyRecs, gatewayLogs]) => {
          const allRecords: TrafficRecord[] = [...(proxyRecs || [])];
          if (gatewayLogs && gatewayLogs.length > 0) {
            const gatewayRecords: TrafficRecord[] = gatewayLogs.map((log: any) => ({
              ts: log.ts,
          id: log.id || 0,
              session: log.session,
              direction: 'C2S' as const,
              source: 'gateway',
              method: log.method,
              path: log.path,
              endpoint_type: 'chat' as const,
              request_body: log.request_body || '',
              response_body: log.response_body || '',
              status: log.status || 0,
              is_sse: log.is_sse || false,
              sse_events: log.sse_events || [],
              model: log.model || '',
              input_tokens: log.input_tokens || 0,
              output_tokens: log.output_tokens || 0,
              latency: log.latency || 0,
              error: log.error || '',
              finish_reason: log.finish_reason || '',
              index: 0,
              url: '',
              host: '',
              is_encoded: false,
              request_headers: {},
              request_body_raw: '',
              request_mime: '',
              request_size: 0,
              status_text: '',
              response_headers: {},
              response_mime: '',
              response_size: 0,
            }));
            allRecords.push(...gatewayRecords);
          }
          allRecords.sort((a, b) => new Date(b.ts).getTime() - new Date(a.ts).getTime());
          updateRecords(allRecords);
        });
      },
    );
    client.connect();
    return () => client.disconnect();
  }, [appendRecord, setSelectedRecord, updateRecords, wails]);

  // Poll status
  useEffect(() => {
    const interval = setInterval(() => {
      wails?.GetStatus().then((s) => {
        const st = s?.stats as StorageStats | null;
        if (st) setStats(st);
        if (s?.proxy_running !== undefined) setProxyRunning(s.proxy_running as boolean);
        if (s?.gateway_running !== undefined) setGatewayRunning(s.gateway_running as boolean);
        if (s?.gateway_logging !== undefined) setGatewayLoggingEnabled(s.gateway_logging as boolean);
      });
    }, 5000);
    return () => clearInterval(interval);
  }, [wails]);

  // Handlers
  const handleToggleProxy = useCallback(async () => {
    if (!wails) return;
    if (proxyRunning) {
      await wails.StopProxy();
      setProxyRunning(false);
    } else {
      try {
        await wails.StartProxy(proxyPort);
        setProxyRunning(true);
      } catch (err) {
        console.error('Failed to start proxy:', err);
      }
    }
  }, [wails, proxyRunning, proxyPort]);

  const handleToggleGateway = useCallback(async () => {
    if (!wails) return;
    if (gatewayRunning) {
      if (wails.StopGateway) await wails.StopGateway();
      setGatewayRunning(false);
    } else {
      try {
        if (wails.StartGateway) await wails.StartGateway(gatewayPort);
        setGatewayRunning(true);
      } catch (err) {
        console.error('Failed to start gateway:', err);
      }
    }
  }, [wails, gatewayRunning]);

  const handleToggleGatewayLogging = useCallback(async () => {
    const newState = !gatewayLoggingEnabled;
    setGatewayLoggingEnabled(newState);
    if (wails?.SetLogging) {
      await wails.SetLogging(newState);
    }
  }, [wails, gatewayLoggingEnabled]);

  const handleClear = useCallback(async () => {
    if (wails) {
      await wails.ClearRecords();
    }
    clearRecords();
  }, [wails, clearRecords]);

  const handleToggleTheme = useCallback(() => {
    setTheme(prev => (prev === 'dark' ? 'light' : 'dark'));
  }, []);

  return (
    <div className="h-dvh flex flex-col bg-zinc-950 text-zinc-100">
      <TitleBar
        activeTab={activeTab}
        proxyRunning={proxyRunning}
        isPaused={isPaused}
        liveTail={liveTail}
        theme={theme}
        onTabChange={setActiveTab}
        onToggleProxy={handleToggleProxy}
        onTogglePause={togglePause}
        onToggleLiveTail={toggleLiveTail}
        onClear={handleClear}
        onToggleTheme={handleToggleTheme}
      />

      <div className="flex-1 overflow-hidden">
        {activeTab === 'proxy' ? (
          <ResizablePanels defaultSizes={[35, 65]} minSizes={[250, 300]}>
            <RecordList
              records={displayedRecords}
              selectedRecord={selectedRecord}
              onSelectRecord={setSelectedRecord}
              onLoadMore={handleLoadMore}
              canLoadMore={canLoadMore}
              liveTail={liveTail}
              typeFilter={proxyTypeFilter}
              onTypeFilterChange={setProxyTypeFilter}
            />
            <DetailPanel request={selectedRecord} response={responseRecord} />
          </ResizablePanels>
        ) : activeTab === 'gateway' ? (
          <GatewayMonitor
            records={displayedRecords}
            onClear={handleClear}
            loggingEnabled={gatewayLoggingEnabled}
            onToggleLogging={handleToggleGatewayLogging}
/>
        ) : (
          <SettingsPanel 
            proxyRunning={proxyRunning}
            proxyPort={proxyPort}
            onToggleProxy={handleToggleProxy}
            onProxyPortChange={setProxyPort}
            gatewayRunning={gatewayRunning}
            gatewayPort={gatewayPort}
            onToggleGateway={handleToggleGateway}
            onGatewayPortChange={setGatewayPort}
            loggingEnabled={gatewayLoggingEnabled}
            onToggleLogging={handleToggleGatewayLogging}
            stats={stats || null}
            onClearAll={handleClear}
            onClearBefore={(days) => wails?.ClearRecordsBefore?.(days) || Promise.resolve(0)}

          />
        )}
      </div>

      <BottomDock
        connected={connected}
        recordCount={records.length}
        stats={stats}
        proxyPort={PROXY_PORT}
      />

      {/* CA cert path hint */}
      {caCertPath && (
        <div className="hidden">{caCertPath}</div>
      )}
    </div>
  );
}
