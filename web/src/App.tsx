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
        GetRecordsByType?: (limit: number, offset: number, recordType: string) => Promise<TrafficRecord[]>;
        GetGatewayLogs: (limit: number, offset: number) => Promise<any[]>;
        LogError: (message: string) => Promise<void>;
        ClearRecords: () => Promise<void>;
        ClearProxyRecords: () => Promise<void>;
        ClearGatewayLogs: () => Promise<void>;
        ClearRecordsBefore: (days: number) => Promise<number>;
        GetCACertPath: () => Promise<string>;
        RevealCACert: () => Promise<void>;
        OpenExternal: (url: string) => Promise<void>;
        GetStatus: () => Promise<Record<string, unknown>>;
        SetLogging: (enabled: boolean) => Promise<void>;
        SetProxyLogging: (enabled: boolean) => Promise<void>;
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
const PROXY_PAGE_SIZE = 500;
const GATEWAY_PAGE_SIZE = 200;
type ProxyTypeFilter = "all" | "chat" | "embedding" | "other";

function matchesProxyTypeFilter(record: TrafficRecord, filter: ProxyTypeFilter): boolean {
  if (filter === "all") return true;
  if (filter === "chat") return record.endpoint_type === "chat";
  if (filter === "embedding") return record.endpoint_type === "embedding";
  return (
    record.endpoint_type === "other" ||
    record.endpoint_type === "tracking" ||
    record.endpoint_type === "finish"
  );
}

function shouldAutoSelectRecord(record: TrafficRecord, activeTab: TabId, proxyTypeFilter: ProxyTypeFilter): boolean {
  if (record.direction !== "C2S") return false;
  if (activeTab === "gateway") return record.source === "gateway";
  if (activeTab === "proxy") {
    return record.source === "proxy" && matchesProxyTypeFilter(record, proxyTypeFilter);
  }
  return false;
}

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
  const [activeTab, setActiveTab] = useState<TabId>("proxy");
  // Per-tab pause state
  const [proxyPaused, setProxyPaused] = useState(false);
  const [gatewayPaused, setGatewayPaused] = useState(false);
  // Per-tab liveTail state
  const [proxyLiveTail, setProxyLiveTail] = useState(true);
  const [gatewayLiveTail, setGatewayLiveTail] = useState(true);

  const isPaused = activeTab === "proxy" ? proxyPaused : gatewayPaused;

  const {
    records,
    selectedRecord,
    setSelectedRecord,
    appendRecord,
    updateRecords,
    clearRecords,
    clearProxyRecords,
    clearGatewayRecords,
    appendRecords,
  } = useRecords(isPaused);
  const [connected, setConnected] = useState(false);
  const [proxyRunning, setProxyRunning] = useState(false);
  const [proxyPort, setProxyPort] = useState(PROXY_PORT);
  const [gatewayRunning, setGatewayRunning] = useState(false);
  const [gatewayPort, setGatewayPort] = useState(DEFAULT_GATEWAY_PORT);
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const [stats, setStats] = useState<StorageStats | null>(null);
  const [caCertPath, setCaCertPath] = useState("");
  const [gatewayLoggingEnabled, setGatewayLoggingEnabled] = useState(true);
  const [proxyLoggingEnabled, setProxyLoggingEnabled] = useState(true);
  const [displayCount, setDisplayCount] = useState(PROXY_PAGE_SIZE);
  const [canLoadMore, setCanLoadMore] = useState(true);
  const [proxyTypeFilter, setProxyTypeFilter] = useState<ProxyTypeFilter>("chat");

  const liveTail = activeTab === "proxy" ? proxyLiveTail : gatewayLiveTail;
  const liveTailRef = useRef(liveTail);
  const selectedRef = useRef(selectedRecord);
  const recordsRef = useRef(records);
  const activeTabRef = useRef(activeTab);
  const proxyTypeFilterRef = useRef(proxyTypeFilter);
  const proxyLoadedByFilterRef = useRef<Record<ProxyTypeFilter, number>>({
    all: 0,
    chat: 0,
    embedding: 0,
    other: 0,
  });

  // Computed records for active tab
  const displayedRecords = useMemo(() => {
    let result: TrafficRecord[] = [];
    try {
      if (activeTab === "proxy") {
        result = records.filter((r) => {
          if (!r || r.source !== "proxy") return false;
          return matchesProxyTypeFilter(r, proxyTypeFilter);
        });
      } else if (activeTab === "gateway") {
        result = records.filter((r) => r && r.source === "gateway");
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
  useEffect(() => {
    activeTabRef.current = activeTab;
  }, [activeTab]);
  useEffect(() => {
    proxyTypeFilterRef.current = proxyTypeFilter;
  }, [proxyTypeFilter]);

  // Wails bindings
  const wails = (window as unknown as WailsWindow).go?.main?.App;

  const fetchProxyRecords = useCallback(async (filter: ProxyTypeFilter, offset: number) => {
    if (!wails) return [];
    const records =
      wails.GetRecordsByType
        ? await wails.GetRecordsByType(PROXY_PAGE_SIZE, offset, filter)
        : await wails.GetRecords(PROXY_PAGE_SIZE, offset);
    proxyLoadedByFilterRef.current[filter] = Math.max(
      proxyLoadedByFilterRef.current[filter],
      offset + (records?.length || 0),
    );
    return records || [];
  }, [wails]);

  const handleLoadMore = useCallback(async () => {
    if (!wails || !canLoadMore) return;
    try {
      let newRecords: TrafficRecord[] = [];
      let hasMore = true;

      if (activeTabRef.current === "proxy") {
        const filter = proxyTypeFilterRef.current;
        const proxyOffset = proxyLoadedByFilterRef.current[filter];
        const newProxyRecs = await fetchProxyRecords(filter, proxyOffset);
        newRecords = newProxyRecs;
        hasMore = newProxyRecs.length >= PROXY_PAGE_SIZE;
      } else if (activeTabRef.current === "gateway") {
        const gatewayOffset = recordsRef.current.filter((r) => r.source === "gateway").length;
        const newGatewayLogs = wails.GetGatewayLogs
          ? await wails.GetGatewayLogs(GATEWAY_PAGE_SIZE, gatewayOffset)
          : [];
        newRecords = (newGatewayLogs || []).map(mapGatewayLogToRecord);
        hasMore = (newGatewayLogs || []).length >= GATEWAY_PAGE_SIZE;
      }

      if (newRecords.length > 0) {
        appendRecords(newRecords);
        setDisplayCount((prev) => prev + PROXY_PAGE_SIZE);
      }

      setCanLoadMore(hasMore);
    } catch (err) {
      console.error("Failed to load more records:", err);
      wails?.LogError(`Failed to load more records: ${err}`);
    }
  }, [wails, canLoadMore, appendRecords, fetchProxyRecords]);

  const selectedRequestRecord = useMemo(() => {
    if (!selectedRecord) return null;
    if (selectedRecord.direction === "C2S") return selectedRecord;
    return (
      records.find(
        (r) =>
          r.session === selectedRecord.session &&
          r.direction === "C2S" &&
          r.source === selectedRecord.source,
      ) || null
    );
  }, [selectedRecord, records]);

  // Find response record for the selected request.
  const responseRecord = useMemo(() => {
    if (!selectedRequestRecord) return null;
    // Don't assume the response is the immediate next record (interleaving possible)
    return (
      records.find(
        (r) =>
          r.session === selectedRequestRecord.session &&
          r.direction === "S2C" &&
          r.source === selectedRequestRecord.source,
      ) || null
    );
  }, [selectedRequestRecord, records]);

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
      fetchProxyRecords(proxyTypeFilterRef.current, 0),
      wails.GetGatewayLogs ? wails.GetGatewayLogs(GATEWAY_PAGE_SIZE, 0) : Promise.resolve([]),
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
        setSelectedRecord(
          allRecords.find((record) => shouldAutoSelectRecord(record, activeTabRef.current, proxyTypeFilterRef.current)) ||
          allRecords.find((record) => record.direction === "C2S") ||
          allRecords[0],
        );
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
      if (s?.proxy_logging !== undefined)
        setProxyLoggingEnabled(s.proxy_logging as boolean);
    });
  }, [wails, updateRecords, setSelectedRecord, fetchProxyRecords]);

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
          if (liveTailRef.current && shouldAutoSelectRecord(rec, activeTabRef.current, proxyTypeFilterRef.current)) {
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
          fetchProxyRecords(proxyTypeFilterRef.current, 0),
          wails.GetGatewayLogs
            ? wails.GetGatewayLogs(GATEWAY_PAGE_SIZE, 0)
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
  }, [appendRecord, setSelectedRecord, updateRecords, wails, fetchProxyRecords]);

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
        if (s?.proxy_logging !== undefined)
          setProxyLoggingEnabled(s.proxy_logging as boolean);
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

  const handleToggleProxyLogging = useCallback(async () => {
    const newState = !proxyLoggingEnabled;
    setProxyLoggingEnabled(newState);
    if (wails?.SetProxyLogging) {
      try {
        await wails.SetProxyLogging(newState);
      } catch (err) {
        console.error("Failed to toggle proxy logging:", err);
        wails?.LogError(`Failed to toggle proxy logging: ${err}`);
      }
    }
  }, [wails, proxyLoggingEnabled]);

  const togglePause = useCallback(() => {
    if (activeTabRef.current === "proxy") {
      setProxyPaused((p) => {
        const next = !p;
        return next;
      });
    } else if (activeTabRef.current === "gateway") {
      setGatewayPaused((p) => {
        const next = !p;
        return next;
      });
    }
  }, []);

  const toggleLiveTail = useCallback(() => {
    if (activeTabRef.current === "proxy") {
      setProxyLiveTail((p) => !p);
    } else if (activeTabRef.current === "gateway") {
      setGatewayLiveTail((p) => !p);
    }
  }, []);

  const handleClear = useCallback(async () => {
    if (activeTabRef.current === "proxy") {
      clearProxyRecords();
      if (wails?.ClearProxyRecords) {
        try {
          await wails.ClearProxyRecords();
        } catch (err) {
          console.error("Failed to clear proxy records:", err);
          wails?.LogError(`Failed to clear proxy records: ${err}`);
        }
      }
    } else if (activeTabRef.current === "gateway") {
      clearGatewayRecords();
      if (wails?.ClearGatewayLogs) {
        try {
          await wails.ClearGatewayLogs();
        } catch (err) {
          console.error("Failed to clear gateway logs:", err);
          wails?.LogError(`Failed to clear gateway logs: ${err}`);
        }
      }
    } else {
      // Settings tab: clear all (backward compatible)
      clearRecords();
      if (wails) {
        try {
          await wails.ClearRecords();
        } catch (err) {
          console.error("Failed to clear records:", err);
          wails?.LogError(`Failed to clear records: ${err}`);
        }
      }
    }
  }, [wails, clearProxyRecords, clearGatewayRecords, clearRecords]);

  const handleProxyTypeFilterChange = useCallback((filter: ProxyTypeFilter) => {
    setProxyTypeFilter(filter);
    setDisplayCount(PROXY_PAGE_SIZE);
    setCanLoadMore(true);

    if (proxyLoadedByFilterRef.current[filter] > 0) {
      return;
    }

    fetchProxyRecords(filter, 0)
      .then((newRecords) => {
        if (newRecords.length > 0) {
          appendRecords(newRecords);
        }
        setCanLoadMore(newRecords.length >= PROXY_PAGE_SIZE);
      })
      .catch((err) => {
        console.error("Failed to load records for filter:", err);
        wails?.LogError(`Failed to load records for filter ${filter}: ${err}`);
      });
  }, [appendRecords, fetchProxyRecords, wails]);

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
              selectedRecord={selectedRequestRecord}
              onSelectRecord={setSelectedRecord}
              onLoadMore={handleLoadMore}
              canLoadMore={canLoadMore}
              liveTail={liveTail}
              typeFilter={proxyTypeFilter}
              onTypeFilterChange={handleProxyTypeFilterChange}
            />
            <DetailPanel request={selectedRequestRecord} response={responseRecord} />
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
            proxyLoggingEnabled={proxyLoggingEnabled}
            onToggleProxyLogging={handleToggleProxyLogging}
            stats={stats || null}
            onClearAll={handleClear}
            onClearBefore={async (days) => {
              let deleted = 0;
              if (wails?.ClearRecordsBefore) {
                deleted = await wails.ClearRecordsBefore(days);
                try {
                  const s = await wails.GetStatus();
                  const st = s?.stats as StorageStats | null;
                  if (st) setStats(st);
                } catch (err) {
                  console.error("Failed to refresh status:", err);
                }
              } else {
                const cutoff = new Date();
                cutoff.setDate(cutoff.getDate() - days);
                const cutoffTime = cutoff.getTime();
                deleted = records.filter((r) => new Date(r.ts).getTime() < cutoffTime).length;
              }

              const cutoff = new Date();
              cutoff.setDate(cutoff.getDate() - days);
              const cutoffTime = cutoff.getTime();
              const filtered = records.filter(
                (r) => new Date(r.ts).getTime() >= cutoffTime
              );
              updateRecords(filtered);

              if (selectedRecord && new Date(selectedRecord.ts).getTime() < cutoffTime) {
                setSelectedRecord(filtered[0] || null);
              }

              return deleted;
            }}
            caCertPath={caCertPath}
            onRevealCACert={async () => {
              if (!wails?.RevealCACert) {
                throw new Error('RevealCACert is not available');
              }
              await wails.RevealCACert();
            }}
          />
        )}
      </div>

      <BottomDock
        connected={connected}
        recordCount={records.length}
        stats={stats}
        proxyPort={PROXY_PORT}
      />
    </div>
  );
}
