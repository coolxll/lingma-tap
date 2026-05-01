import { useState, useCallback, useRef } from 'react';
import { TrafficRecord, recordKey } from '@/lib/types';

const MAX_RECORDS = 2000;

export function useRecords() {
  const [records, setRecords] = useState<TrafficRecord[]>([]);
  const [selectedRecord, setSelectedRecord] = useState<TrafficRecord | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [isPaused, setIsPaused] = useState(false);
  const [liveTail, setLiveTail] = useState(true);
  const isPausedRef = useRef(false);

  const appendRecord = useCallback((record: TrafficRecord) => {
    if (isPausedRef.current) return;

    setRecords((prev) => {
      const key = recordKey(record);
      const index = prev.findIndex((r) => recordKey(r) === key);
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

  const clearRecords = useCallback(() => {
    setRecords([]);
    setSelectedRecord(null);
  }, []);

  const togglePause = useCallback(() => {
    setIsPaused((p) => {
      isPausedRef.current = !p;
      return !p;
    });
  }, []);

  const toggleLiveTail = useCallback(() => {
    setLiveTail((p) => !p);
  }, []);

  return {
    records,
    selectedRecord,
    setSelectedRecord,
    searchQuery,
    setSearchQuery,
    isPaused,
    liveTail,
    appendRecord,
    updateRecords,
    clearRecords,
    togglePause,
    toggleLiveTail,
    appendRecords,
  };
}
