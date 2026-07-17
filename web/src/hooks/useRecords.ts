import { useState, useCallback, useRef, useEffect } from 'react';
import { TrafficRecord, recordKey } from '@/lib/types';

const MAX_RECORDS = 2000;

export function useRecords(externalPaused?: boolean) {
  const [records, setRecords] = useState<TrafficRecord[]>([]);
  const [selectedRecord, setSelectedRecord] = useState<TrafficRecord | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const isPausedRef = useRef(externalPaused ?? false);

  // Sync external paused state into ref for appendRecord check
  useEffect(() => {
    isPausedRef.current = externalPaused ?? false;
  }, [externalPaused]);

  const appendRecord = useCallback((record: TrafficRecord) => {
    if (isPausedRef.current || !record) return;

    try {
      setRecords((prev) => {
        const key = recordKey(record);
        if (!key) return prev;

        const index = prev.findIndex((r) => r && recordKey(r) === key);
        if (index >= 0) {
          const next = [...prev];
          next[index] = record;
          return next;
        }
        const next = [record, ...prev];
        if (next.length > MAX_RECORDS) {
          return next.slice(0, MAX_RECORDS);
        }
        return next;
      });
    } catch (err) {
      const msg = `Error in appendRecord: ${err}`;
      console.error(msg);
      (window as any).go?.main?.App?.LogError(msg);
    }
  }, []);

  const updateRecords = useCallback((newRecords: TrafficRecord[]) => {
    setRecords(newRecords);
  }, []);

  const appendRecords = useCallback((newRecords: TrafficRecord[]) => {
    setRecords(prev => {
      const existingKeys = new Set(prev.map(r => recordKey(r)));
      const filtered = newRecords.filter(r => !existingKeys.has(recordKey(r)));
      // New records from pagination are older, should be appended to the end
      return [...prev, ...filtered];
    });
  }, []);

  // Apply a burst of lifecycle updates in one React commit. Proxy bodies are
  // deliberately absent from these records, so this remains cheap even when
  // the tap is receiving many SSE/request updates.
  const upsertRecords = useCallback((updates: TrafficRecord[]) => {
    if (isPausedRef.current || updates.length === 0) return;
    setRecords((prev) => {
      const next = [...prev];
      const indexes = new Map(next.map((record, index) => [recordKey(record), index]));
      const added: TrafficRecord[] = [];
      for (const record of updates) {
        const key = recordKey(record);
        if (!key) continue;
        const existing = indexes.get(key);
        if (existing !== undefined) {
          next[existing] = record;
        } else {
          added.push(record);
        }
      }
      const combined = [...added.reverse(), ...next];
      return combined.length > MAX_RECORDS ? combined.slice(0, MAX_RECORDS) : combined;
    });
  }, []);

  const clearRecords = useCallback(() => {
    setRecords([]);
    setSelectedRecord(null);
  }, []);

  const clearProxyRecords = useCallback(() => {
    setRecords((prev) => {
      const filtered = prev.filter((r) => r && r.source !== 'proxy');
      return filtered;
    });
    setSelectedRecord((prev) => (prev && prev.source === 'proxy' ? null : prev));
  }, []);

  const clearGatewayRecords = useCallback(() => {
    setRecords((prev) => {
      const filtered = prev.filter((r) => r && r.source !== 'gateway');
      return filtered;
    });
    setSelectedRecord((prev) => (prev && prev.source === 'gateway' ? null : prev));
  }, []);

  return {
    records,
    selectedRecord,
    setSelectedRecord,
    searchQuery,
    setSearchQuery,
    appendRecord,
    updateRecords,
    clearRecords,
    clearProxyRecords,
    clearGatewayRecords,
    appendRecords,
    upsertRecords,
  };
}
