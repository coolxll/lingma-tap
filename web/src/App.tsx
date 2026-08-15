import React, {
  useState,
  useEffect,
  useCallback,
  useRef,
  useMemo,
} from "react";
import { TrafficRecord, StorageStats, mapGatewayLogToRecord, recordKey } from "@/lib/types";
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
        StartGateway: (port: number, listenAddr: string) => Promise<void>;
        StopGateway: () => Promise<void>;
        GetRecords: (limit: number, offset: number) => Promise<TrafficRecord[]>;
        GetRecordsByType?: (limit: number, offset: number, recordType: string) => Promise<TrafficRecord[]>;
        GetGatewayLogs: (limit: number, offset: number) => Promise<any[]>;
        GetGatewayStats?: (timeRange: string, filter: string) => Promise<any>;
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
        SetLingmaHTTP2: (enabled: boolean) => Promise<void>;
        GetModels: () => Promise<ModelInfo[]>;
        StartOAuthLogin: () => Promise<string>;
        CancelOAuthLogin: () => Promise<void>;
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
    updateRecords,
    clearRecords,
    clearProxyRecords,
    clearGatewayRecords,
    appendRecords,
    upsertRecords,
  } = useRecords(isPaused);
  const [connected, setConnected] = useState(false);
  const [proxyRunning, setProxyRunning] = useState(false);
  const [proxyPort, setProxyPort] = useState(PROXY_PORT);
  const [gatewayRunning, setGatewayRunning] = useState(false);
  const [gatewayPort, setGatewayPort] = useState(DEFAULT_GATEWAY_PORT);
  const [gatewayListenAddr, setGatewayListenAddr] = useState("127.0.0.1");
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const [stats, setStats] = useState<StorageStats | null>(null);
  const [caCertPath, setCaCertPath] = useState("");
  const [gatewayLoggingEnabled, setGatewayLoggingEnabled] = useState(true);
  const [proxyLoggingEnabled, setProxyLoggingEnabled] = useState(true);
  const [lingmaHTTP2Enabled, setLingmaHTTP2Enabled] = useState(false);
  const [authenticated, setAuthenticated] = useState(false);
  const [authUser, setAuthUser] = useState('');
  const [authExpireTime, setAuthExpireTime] = useState(0);
  const [oauthInProgress, setOAuthInProgress] = useState(false);
  const [oauthError, setOAuthError] = useState('');
  const [oauthLoginURL, setOAuthLoginURL] = useState('');
  const [displayCount, setDisplayCount] = useState(PROXY_PAGE_SIZE);
  const [canLoadMore, setCanLoadMore] = useState(true);
  const [proxyTypeFilter, setProxyTypeFilter] = useState<ProxyTypeFilter>("chat");
  const [isVisible, setIsVisible] = useState(true);

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
  const pendingWSRecordsRef = useRef(new Map<string, TrafficRecord>());
  const wsFlushFrameRef = useRef<number | null>(null);

  const isVisibleRef = useRef(isVisible);
  useEffect(() => {
    isVisibleRef.current = isVisible;
  }, [isVisible]);

  const enqueueWSRecord = useCallback((record: TrafficRecord) => {
    if (!record) return;
    const key = recordKey(record);
    if (!key) return;
    pendingWSRecordsRef.current.set(key, record);
    if (wsFlushFrameRef.current !== null) return;

    // Throttle WebSocket processing when page is hidden
    const flushDelay = isVisibleRef.current ? 0 : 2000;

    if (flushDelay === 0) {
      wsFlushFrameRef.current = window.requestAnimationFrame(() => {
        wsFlushFrameRef.current = null;
        const batch = [...pendingWSRecordsRef.current.values()];
        pendingWSRecordsRef.current.clear();
        upsertRecords(batch);
        if (liveTailRef.current) {
          const last = batch[batch.length - 1];
          if (last && shouldAutoSelectRecord(last, activeTabRef.current, proxyTypeFilterRef.current)) {
            setSelectedRecord(last);
          }
        }
      });
    } else {
      wsFlushFrameRef.current = window.setTimeout(() => {
        wsFlushFrameRef.current = null;
        const batch = [...pendingWSRecordsRef.current.values()];
        pendingWSRecordsRef.current.clear();
        upsertRecords(batch);
        if (liveTailRef.current) {
          const last = batch[batch.length - 1];
          if (last && shouldAutoSelectRecord(last, activeTabRef.current, proxyTypeFilterRef.current)) {
            setSelectedRecord(last);
          }
        }
      }, flushDelay) as unknown as number;
    }
  }, [setSelectedRecord, upsertRecords]);

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

  // Track page visibility to throttle work when hidden
  useEffect(() => {
    const handler = () => setIsVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", handler);
    return () => document.removeEventListener("visibilitychange", handler);
  }, []);

  // Wails bindings
  const wails = (window as unknown as WailsWindow).go?.main?.App;

  const applyStatus = useCallback((s: Record<string, unknown>) => {
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
    if (s?.lingma_http2 !== undefined)
      setLingmaHTTP2Enabled(s.lingma_http2 as boolean);
    if (s?.authenticated !== undefined)
      setAuthenticated(s.authenticated as boolean);
    if (typeof s?.auth_user === 'string')
      setAuthUser(s.auth_user);
    if (typeof s?.auth_expire_time === 'number')
      setAuthExpireTime(s.auth_expire_time);
    if (s?.oauth_in_progress !== undefined)
      setOAuthInProgress(s.oauth_in_progress as boolean);
    if (typeof s?.oauth_error === 'string')
      setOAuthError(s.oauth_error);
    if (typeof s?.oauth_login_url === 'string')
      setOAuthLoginURL(s.oauth_login_url);
    if (!(s?.oauth_in_progress as boolean))
      setOAuthLoginURL('');
    if (typeof s?.oauth_login_url === 'string')
      setOAuthLoginURL(s.oauth_login_url);
  }, []);

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
    wails.GetStatus().then(applyStatus);
  }, [wails, updateRecords, setSelectedRecord, fetchProxyRecords, applyStatus]);

  // WebSocket connection
  useEffect(() => {
    const wsUrl = `ws://localhost:${WS_PORT}/ws/records`;
    const client = new WSClient(
      wsUrl,
      (record) => {
        try {
          if (!record) return;
          const rec = record as unknown as TrafficRecord;
          enqueueWSRecord(rec);
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
    return () => {
      if (wsFlushFrameRef.current !== null) {
        // Could be either requestAnimationFrame or setTimeout depending on visibility
        window.cancelAnimationFrame(wsFlushFrameRef.current);
        window.clearTimeout(wsFlushFrameRef.current);
        wsFlushFrameRef.current = null;
      }
      pendingWSRecordsRef.current.clear();
      client.disconnect();
    };
  }, [enqueueWSRecord, updateRecords, wails, fetchProxyRecords]);

  // Poll status (pause when hidden)
  useEffect(() => {
    if (!isVisible) return;
    const interval = setInterval(() => {
      wails?.GetStatus().then((s) => {
        applyStatus(s);
      });
    }, 5000);
    return () => clearInterval(interval);
  }, [wails, applyStatus, isVisible]);

  // Refresh status immediately when becoming visible
  useEffect(() => {
    if (isVisible && wails) {
      wails.GetStatus().then(applyStatus);
    }
  }, [isVisible, wails, applyStatus]);

  useEffect(() => {
    if (!oauthInProgress || !wails) return;
    const interval = setInterval(() => {
      wails.GetStatus().then(applyStatus).catch((err) => {
        console.error('Failed to refresh OAuth status:', err);
      });
    }, 1000);
    return () => clearInterval(interval);
  }, [wails, oauthInProgress, applyStatus]);

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
        if (wails.StartGateway) await wails.StartGateway(gatewayPort, gatewayListenAddr);
        setGatewayRunning(true);
      } catch (err) {
        console.error("Failed to start gateway:", err);
      }
    }
  }, [wails, gatewayRunning, gatewayPort, gatewayListenAddr]);

  const handleStartOAuthLogin = useCallback(async () => {
    if (!wails?.StartOAuthLogin) return;
    try {
      const url = await wails.StartOAuthLogin();
      setOAuthLoginURL(url);
      const status = await wails.GetStatus();
      applyStatus(status);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to start OAuth login';
      setOAuthError(message);
      console.error('Failed to start OAuth login:', err);
    }
  }, [wails, applyStatus]);

  const handleCancelOAuthLogin = useCallback(async () => {
    if (!wails?.CancelOAuthLogin) return;
    try {
      await wails.CancelOAuthLogin();
      setOAuthLoginURL('');
      const status = await wails.GetStatus();
      applyStatus(status);
    } catch (err) {
      console.error('Failed to cancel OAuth login:', err);
    }
  }, [wails, applyStatus]);

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

  const handleToggleLingmaHTTP2 = useCallback(async () => {
    const newState = !lingmaHTTP2Enabled;
    setLingmaHTTP2Enabled(newState);
    if (wails?.SetLingmaHTTP2) {
      try {
        await wails.SetLingmaHTTP2(newState);
      } catch (err) {
        setLingmaHTTP2Enabled(!newState);
        console.error("Failed to toggle Lingma HTTP/2:", err);
        wails?.LogError(`Failed to toggle Lingma HTTP/2: ${err}`);
      }
    }
  }, [wails, lingmaHTTP2Enabled]);

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

  const handleClearBefore = useCallback(async (days: number) => {
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
  }, [wails, records, selectedRecord, updateRecords, setSelectedRecord]);

  const handleRevealCACert = useCallback(async () => {
    if (!wails?.RevealCACert) {
      throw new Error('RevealCACert is not available');
    }
    await wails.RevealCACert();
  }, [wails]);

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
            getStats={wails?.GetGatewayStats}
          />
        ) : (
          <SettingsPanel
            proxyRunning={proxyRunning}
            proxyPort={proxyPort}
            onToggleProxy={handleToggleProxy}
            onProxyPortChange={setProxyPort}
            gatewayRunning={gatewayRunning}
            gatewayPort={gatewayPort}
            gatewayListenAddr={gatewayListenAddr}
            onToggleGateway={handleToggleGateway}
            onGatewayPortChange={setGatewayPort}
            onGatewayListenAddrChange={setGatewayListenAddr}
            loggingEnabled={gatewayLoggingEnabled}
            onToggleLogging={handleToggleGatewayLogging}
            proxyLoggingEnabled={proxyLoggingEnabled}
            onToggleProxyLogging={handleToggleProxyLogging}
            lingmaHTTP2Enabled={lingmaHTTP2Enabled}
            onToggleLingmaHTTP2={handleToggleLingmaHTTP2}
            authenticated={authenticated}
            authUser={authUser}
            authExpireTime={authExpireTime}
            oauthInProgress={oauthInProgress}
            oauthError={oauthError}
            oauthLoginURL={oauthLoginURL}
            onStartOAuthLogin={handleStartOAuthLogin}
            onCancelOAuthLogin={handleCancelOAuthLogin}
            stats={stats || null}
            onClearAll={handleClear}
            onClearBefore={handleClearBefore}
            caCertPath={caCertPath}
            onRevealCACert={handleRevealCACert}
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
