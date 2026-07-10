package bridge

import (
	"sync"
	"time"
)

// oneShotTTLSet stores bounded expiring keys that are removed on first use.
type oneShotTTLSet struct {
	mu         sync.Mutex
	entries    map[string]time.Time
	maxEntries int
}

func newOneShotTTLSet(maxEntries int) *oneShotTTLSet {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	return &oneShotTTLSet{
		entries:    make(map[string]time.Time),
		maxEntries: maxEntries,
	}
}

func (s *oneShotTTLSet) mark(key string, ttl time.Duration) bool {
	if s == nil || key == "" || ttl <= 0 {
		return false
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.deleteExpiredLocked(now)
	if _, exists := s.entries[key]; !exists && len(s.entries) >= s.maxEntries {
		s.deleteOldestLocked()
	}
	s.entries[key] = now.Add(ttl)
	return true
}

func (s *oneShotTTLSet) consume(key string) bool {
	if s == nil || key == "" {
		return false
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	expiresAt, exists := s.entries[key]
	if !exists {
		return false
	}
	delete(s.entries, key)
	return expiresAt.After(now)
}

func (s *oneShotTTLSet) deleteExpiredLocked(now time.Time) {
	for key, expiresAt := range s.entries {
		if !expiresAt.After(now) {
			delete(s.entries, key)
		}
	}
}

func (s *oneShotTTLSet) deleteOldestLocked() {
	oldestKey := ""
	var oldestExpiry time.Time
	for key, expiresAt := range s.entries {
		if oldestKey == "" || expiresAt.Before(oldestExpiry) {
			oldestKey = key
			oldestExpiry = expiresAt
		}
	}
	if oldestKey != "" {
		delete(s.entries, oldestKey)
	}
}
