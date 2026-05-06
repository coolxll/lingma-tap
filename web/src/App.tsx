import React, {
  useState,
  useEffect,
  useCallback,
  useRef,
  useMemo,
} from "react";
import { TrafficRecord, StorageStats, mapGatewayLogToRecord } from "@/lib/types";
import { WSClient } from "@/lib/ws-client";
import { useRecords } from "@/hooks/useRecords";
import { TitleBar, TabId } from "@/components/TitleBar";
import { RecordList } from "@/components/RecordList";
import { DetailPanel } from "@/components/DetailPanel";
import { ResizablePanels } from "@/components/ResizablePanels";
import { BottomDock } from "@/components/BottomDock";
import { SettingsPanel } from "@/components/SettingsPanel";
import { GatewayMonitor } from "@/components/GatewayMonitor";

// Wails window type
interface WailsWindow extends Window {
  go?: {
    main?: {
      App?: {
        StartProxy: (port: number) => Promise<void>;
        StopProxy: () => Promise<void>;
        StartGateway: (port: number) => Promise<void>;
        StopGateway: () => Promise<void>;
        GetRecords: (limit: number, offset: number) => Promise<TrafficRecord[]>;
        GetGatewayLogs: (limit: number, offset: number) => Promise<any[]>;
        LogError: (message: string) => Promise<void>;
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

class GlobalErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { hasError: boolean; error: any }
> {
  constructor(props: any) {
    super(props);
    this.state = { hasError: false, error: null };
  }
  static getDerivedStateFromError(error: any) {
    return { hasError: true, error };
  }
  componentDidCatch(error: any, errorInfo: any) {
    console.error("Global Error:", error, errorInfo);
    const msg = `Global Error: ${error} ${JSON.stringify(errorInfo)}`;
    (window as any).go?.main?.App?.LogError(msg);
  }
  render() {
    if (this.state.hasError) {
      return (
        <div className="p-10 bg-zinc-950 text-red-400 h-screen overflow-auto font-mono">
          <h1 className="text-2xl font-bold mb-4 text-red-500 flex items-center gap-2">
            ⚠️ UI CRASH DETECTED
          </h1>
          <div className="bg-red-500/10 border border-red-500/20 rounded-lg p-6 mb-6">
            <p className="text-zinc-300 mb-4">
              The application encountered a fatal rendering error.
            </p>
            <pre className="text-xs bg-black/50 p-4 rounded border border-zinc-800 whitespace-pre-wrap break-all">
              {this.state.error?.toString()}
            </pre>
          </div>
          <button
            onClick={() => window.location.reload()}
            className="px-6 py-2 bg-red-600 hover:bg-red-500 text-white font-bold rounded-lg transition-colors"
          >
            RELOAD APPLICATION
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

export default function AppWrapper() {
  return (
    <GlobalErrorBoundary>
      <App />
    </GlobalErrorBoundary>
  );
}

function App() {
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
    appendRecords,
  } = useRecords();

  const [activeTab, setActiveTab] = useState<TabId>("proxy");
  const [connected, setConnected] = useState(false);
  const [proxyRunning, setProxyRunning] = useState(false);
  const [proxyPort, setProxyPort] = useState(PROXY_PORT);
  const [gatewayRunning, setGatewayRunning] = useState(false);
  const [gatewayPort, setGatewayPort] = useState(DEFAULT_GATEWAY_PORT);
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const [stats, setStats] = useState<StorageStats | null>(null);
  const [caCertPath, setCaCertPath] = useState("");
  const [gatewayLoggingEnabled, setGatewayLoggingEnabled] = useState(true);
  const [displayCount, setDisplayCount] = useState(200);
  const [canLoadMore, setCanLoadMore] = useState(true);
  const [proxyTypeFilter, setProxyTypeFilter] = useState<
    "all" | "chat" | "embedding" | "other"
  >("all");
  const liveTailRef = useRef(liveTail);
  const selectedRef = useRef(selectedRecord);
  const recordsRef = useRef(records);

  // Computed records for active tab
  const displayedRecords = useMemo(() => {
    let result: TrafficRecord[] = [];
    try {
      if (activeTab === "proxy") {
        result = records.filter((r) => {
          if (!r || r.source !== "proxy") return false;
          if (proxyTypeFilter === "all") return true;
          if (proxyTypeFilter === "chat")
            return r.endpoint_type === "chat" || r.endpoint_type === "finish";
          if (proxyTypeFilter === "embedding")
            return r.endpoint_type === "embedding";
          if (proxyTypeFilter === "other")
            return (
              r.endpoint_type === "other" || r.endpoint_type === "tracking"
            );
          return true;
        });
      } else if (activeTab === "gateway") {
        result = records.filter((r) => r && (r as any).source === "gateway");
      } else {
        result = records || [];
      }
      return result.slice(0, displayCount);
    } catch (err) {
      console.error("Error calculating displayedRecords:", err);
      return [];
    }
  }, [records, activeTab, displayCount, proxyTypeFilter]);

  useEffect(() => {
    liveTailRef.current = liveTail;
  }, [liveTail]);
  useEffect(() => {
    selectedRef.current = selectedRecord;
  }, [selectedRecord]);
  useEffect(() => {
    recordsRef.current = records;
  }, [records]);

  // Wails bindings
  const wails = (window as unknown as WailsWindow).go?.main?.App;

  const handleLoadMore = useCallback(async () => {
    if (!wails || !canLoadMore) return;
    try {
      const proxyOffset = recordsRef.current.filter((r) => r.source === "proxy").length;
      const gatewayOffset = recordsRef.current.filter((r) => r.source === "gateway").length;

      const [newProxyRecs, newGatewayLogs] = await Promise.all([
        wails.GetRecords(200, proxyOffset),
        wails.GetGatewayLogs ? wails.GetGatewayLogs(200, gatewayOffset) : Promise.resolve([])
      ]);

      const newRecords: TrafficRecord[] = [...(newProxyRecs || [])];
      if (newGatewayLogs && newGatewayLogs.length > 0) {
        newRecords.push(...newGatewayLogs.map(mapGatewayLogToRecord));
      }

      if (newRecords.length > 0) {
        appendRecords(newRecords);
        setDisplayCount((prev) => prev + 200);
      }
      
      if ((!newProxyRecs || newProxyRecs.length < 200) && (!newGatewayLogs || newGatewayLogs.length < 200)) {
        setCanLoadMore(false);
      }
    } catch (err) {
      console.error("Failed to load more records:", err);
      wails?.LogError(`Failed to load more records: ${err}`);
    }
  }, [wails, canLoadMore, appendRecords]);

  // Find response record for the selected request
  const responseRecord = useMemo(() => {
    if (!selectedRecord || selectedRecord.direction === "S2C") return null;
    // Don't assume the response is the immediate next record (interleaving possible)
    return (
      records.find(
        (r) => r.session === selectedRecord.session && r.direction === "S2C",
      ) || null
    );
  }, [selectedRecord, records]);

  // Apply theme
  useEffect(() => {
    if (theme === "light") {
      document.documentElement.classList.remove("dark");
      document.documentElement.classList.add("light");
    } else {
      document.documentElement.classList.remove("light");
      document.documentElement.classList.add("dark");
    }
  }, [theme]);

  // Initialize: load existing records
  useEffect(() => {
    if (!wails) return;

    Promise.all([
      wails.GetRecords(200, 0),
      wails.GetGatewayLogs ? wails.GetGatewayLogs(200, 0) : Promise.resolve([]),
    ]).then(([proxyRecs, gatewayLogs]) => {
      const allRecords: TrafficRecord[] = [...(proxyRecs || [])];

      // Convert gateway logs to TrafficRecord format
      if (gatewayLogs && gatewayLogs.length > 0) {
        allRecords.push(...gatewayLogs.map(mapGatewayLogToRecord));
      }

      // Sort by timestamp (newest first)
      allRecords.sort(
        (a, b) => new Date(b.ts).getTime() - new Date(a.ts).getTime(),
      );

      if (allRecords.length > 0) {
        updateRecords(allRecords);
        setSelectedRecord(allRecords[0]);
      }
    });

    wails.GetCACertPath().then(setCaCertPath);
    wails.GetStatus().then((s) => {
      const st = s?.stats as StorageStats | null;
      if (st) setStats(st);
      if (s?.proxy_running !== undefined)
        setProxyRunning(s.proxy_running as boolean);
      if (s?.gateway_running !== undefined)
        setGatewayRunning(s.gateway_running as boolean);
      if (s?.gateway_logging !== undefined)
        setGatewayLoggingEnabled(s.gateway_logging as boolean);
    });
  }, [wails, updateRecords, setSelectedRecord]);

  // WebSocket connection
  useEffect(() => {
    const wsUrl = `ws://localhost:${WS_PORT}/ws/records`;
    const client = new WSClient(
      wsUrl,
      (record) => {
        try {
          if (!record) return;
          const rec = record as unknown as TrafficRecord;
          appendRecord(rec);
          if (liveTailRef.current) {
            setSelectedRecord(rec);
          }
        } catch (err) {
          const msg = `Failed to append record from WS: ${err}`;
          console.error(msg);
          wails?.LogError(msg);
        }
      },
      setConnected,
      () => {
        // On reconnect, fetch latest records including gateway logs
        if (!wails) return;
        Promise.all([
          wails.GetRecords(200, 0),
          wails.GetGatewayLogs
            ? wails.GetGatewayLogs(200, 0)
            : Promise.resolve([]),
        ]).then(([proxyRecs, gatewayLogs]) => {
          const allRecords: TrafficRecord[] = [...(proxyRecs || [])];
          if (gatewayLogs && gatewayLogs.length > 0) {
            allRecords.push(...gatewayLogs.map(mapGatewayLogToRecord));
          }
          allRecords.sort(
            (a, b) => new Date(b.ts).getTime() - new Date(a.ts).getTime(),
          );
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
        if (s?.proxy_running !== undefined)
          setProxyRunning(s.proxy_running as boolean);
        if (s?.gateway_running !== undefined)
          setGatewayRunning(s.gateway_running as boolean);
        if (s?.gateway_logging !== undefined)
          setGatewayLoggingEnabled(s.gateway_logging as boolean);
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
        console.error("Failed to start proxy:", err);
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
        console.error("Failed to start gateway:", err);
      }
    }
  }, [wails, gatewayRunning]);

  const handleToggleGatewayLogging = useCallback(async () => {
    const newState = !gatewayLoggingEnabled;
    setGatewayLoggingEnabled(newState);
    if (wails?.SetLogging) {
      try {
        await wails.SetLogging(newState);
      } catch (err) {
        console.error("Failed to toggle gateway logging:", err);
        wails?.LogError(`Failed to toggle gateway logging: ${err}`);
      }
    }
  }, [wails, gatewayLoggingEnabled]);

  const handleClear = useCallback(async () => {
    if (wails) {
      try {
        await wails.ClearRecords();
      } catch (err) {
        console.error("Failed to clear records:", err);
        wails?.LogError(`Failed to clear records: ${err}`);
      }
    }
    clearRecords();
  }, [wails, clearRecords]);

  const handleToggleTheme = useCallback(() => {
    setTheme((prev) => (prev === "dark" ? "light" : "dark"));
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
        {activeTab === "proxy" ? (
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
        ) : activeTab === "gateway" ? (
          <GatewayMonitor
            records={displayedRecords}
            onClear={handleClear}
            loggingEnabled={gatewayLoggingEnabled}
            onToggleLogging={handleToggleGatewayLogging}
            onLoadMore={handleLoadMore}
            canLoadMore={canLoadMore}
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
            onClearBefore={(days) =>
              wails?.ClearRecordsBefore?.(days) || Promise.resolve(0)
            }
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
      {caCertPath && <div className="hidden">{caCertPath}</div>}
    </div>
  );
}
