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

// Wails window type
interface WailsWindow extends Window {
  go?: {
    main?: {
      App?: {
        StartProxy: (port: number) => Promise<void>;
        StopProxy: () => Promise<void>;
        GetRecords: (limit: number) => Promise<TrafficRecord[]>;
        ClearRecords: () => Promise<void>;
        GetCACertPath: () => Promise<string>;
        GetStatus: () => Promise<Record<string, unknown>>;
      };
    };
  };
}

const WS_PORT = 9090;
const PROXY_PORT = 9528;

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

  const [activeTab, setActiveTab] = useState<TabId>('monitor');
  const [connected, setConnected] = useState(false);
  const [proxyRunning, setProxyRunning] = useState(false);
  const [theme] = useState<'dark' | 'light'>('dark');
  const [stats, setStats] = useState<StorageStats | null>(null);
  const [caCertPath, setCaCertPath] = useState('');
  const liveTailRef = useRef(liveTail);
  const selectedRef = useRef(selectedRecord);
  const recordsRef = useRef(records);

  useEffect(() => { liveTailRef.current = liveTail; }, [liveTail]);
  useEffect(() => { selectedRef.current = selectedRecord; }, [selectedRecord]);
  useEffect(() => { recordsRef.current = records; }, [records]);

  // Wails bindings
  const wails = (window as unknown as WailsWindow).go?.main?.App;

  // Find response record for the selected request
  const responseRecord = useMemo(() => {
    if (!selectedRecord || selectedRecord.direction === 'S2C') return null;
    const idx = records.indexOf(selectedRecord);
    if (idx < 0) return null;
    const next = records[idx + 1];
    if (next && next.session === selectedRecord.session && next.direction === 'S2C') {
      return next;
    }
    return null;
  }, [selectedRecord, records]);

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

  const handleClear = useCallback(async () => {
    if (wails) {
      await wails.ClearRecords();
    }
    clearRecords();
  }, [wails, clearRecords]);

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
        onToggleTheme={() => {}}
      />

      <div className="flex-1 overflow-hidden">
        {activeTab === 'monitor' ? (
          <ResizablePanels defaultSizes={[35, 65]} minSizes={[250, 300]}>
            <RecordList
              records={records}
              selectedRecord={selectedRecord}
              onSelectRecord={setSelectedRecord}
            />
            <DetailPanel request={selectedRecord} response={responseRecord} />
          </ResizablePanels>
        ) : (
          <SettingsPanel
            proxyRunning={proxyRunning}
            proxyPort={PROXY_PORT}
            onToggleProxy={handleToggleProxy}
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
