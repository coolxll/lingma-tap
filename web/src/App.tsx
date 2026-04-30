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
        GetRecords: (limit: number) => Promise<TrafficRecord[]>;
        ClearRecords: () => Promise<void>;
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

const WS_PORT = 9090;
const PROXY_PORT = 9528;
const DEFAULT_GATEWAY_PORT = 8080;

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
  } = useRecords();

  const [activeTab, setActiveTab] = useState<TabId>('proxy');
  const [connected, setConnected] = useState(false);
  const [proxyRunning, setProxyRunning] = useState(false);
  const [gatewayRunning, setGatewayRunning] = useState(false);
  const [theme, setTheme] = useState<'dark' | 'light'>('dark');
  const [stats, setStats] = useState<StorageStats | null>(null);
  const [caCertPath, setCaCertPath] = useState('');
  const [gatewayLoggingEnabled, setGatewayLoggingEnabled] = useState(true);
  const liveTailRef = useRef(liveTail);
  const selectedRef = useRef(selectedRecord);
  const recordsRef = useRef(records);

  // Computed records for active tab
  const displayedRecords = useMemo(() => {
    if (activeTab === 'proxy') {
      // Proxy captures Lingma traffic (api only, exclude tracking and other)
      return records.filter(r => 
        r.endpoint_type === 'chat' || 
        r.endpoint_type === 'embedding' || 
        r.endpoint_type === 'finish'
      );
    } else if (activeTab === 'gateway') {
      // Gateway traffic observability
      // TODO: Filter gateway specific traffic when implemented in backend
      // For now, it shows nothing or we could show all traffic
      return records.filter(r => (r as any).source === 'gateway');
    }
    return records;
  }, [records, activeTab]);

  useEffect(() => { liveTailRef.current = liveTail; }, [liveTail]);
  useEffect(() => { selectedRef.current = selectedRecord; }, [selectedRecord]);
  useEffect(() => { recordsRef.current = records; }, [records]);

  // Wails bindings
  const wails = (window as unknown as WailsWindow).go?.main?.App;

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
    wails.GetRecords(200).then((recs) => {
      if (recs && recs.length > 0) {
        updateRecords(recs);
        setSelectedRecord(recs[recs.length - 1]);
      }
    });
    wails.GetCACertPath().then(setCaCertPath);
    wails.GetStatus().then((s) => {
      const st = s?.stats as StorageStats | undefined;
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
        // On reconnect, fetch latest records
        wails?.GetRecords(200).then((recs) => {
          if (recs) updateRecords(recs);
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
        const st = s?.stats as StorageStats | undefined;
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
        await wails.StartProxy(PROXY_PORT);
        setProxyRunning(true);
      } catch (err) {
        console.error('Failed to start proxy:', err);
      }
    }
  }, [wails, proxyRunning]);

  const handleToggleGateway = useCallback(async () => {
    if (!wails) return;
    if (gatewayRunning) {
      if (wails.StopGateway) await wails.StopGateway();
      setGatewayRunning(false);
    } else {
      try {
        if (wails.StartGateway) await wails.StartGateway(DEFAULT_GATEWAY_PORT);
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
            />
            <DetailPanel request={selectedRecord} response={responseRecord} />
          </ResizablePanels>
        ) : activeTab === 'gateway' ? (
          <GatewayMonitor
            records={displayedRecords}
            allRecords={records}
            onClear={handleClear}
            loggingEnabled={gatewayLoggingEnabled}
            onToggleLogging={handleToggleGatewayLogging}
          />
        ) : (
          <SettingsPanel
            proxyRunning={proxyRunning}
            proxyPort={PROXY_PORT}
            onToggleProxy={handleToggleProxy}
            gatewayRunning={gatewayRunning}
            gatewayPort={DEFAULT_GATEWAY_PORT}
            onToggleGateway={handleToggleGateway}
            gatewayLoggingEnabled={gatewayLoggingEnabled}
            onToggleGatewayLogging={handleToggleGatewayLogging}
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
