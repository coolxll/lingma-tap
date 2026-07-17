package storage

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

const (
	defaultQueueCapacity = 4096
	maxPendingBodyBytes  = 64 * 1024 * 1024
)

// AsyncSink coalesces lifecycle updates by flow key. This prevents a headers
// record, body update and final response from occupying three large queue
// entries, while keeping proxy callbacks non-blocking when SQLite is busy.
type AsyncSink struct {
	db            *DB
	signal        chan string
	closed        bool
	mu            sync.Mutex
	pending       map[string]*proto.Record
	pendingBytes  int64
	droppedBodies atomic.Uint64
	onSaved       func(*proto.Record)
	wg            sync.WaitGroup
}

func NewAsyncSink(db *DB, capacity int) *AsyncSink {
	if capacity <= 0 {
		capacity = defaultQueueCapacity
	}
	s := &AsyncSink{
		db:      db,
		signal:  make(chan string, capacity),
		pending: make(map[string]*proto.Record),
	}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *AsyncSink) SetOnSaved(callback func(*proto.Record)) {
	s.mu.Lock()
	s.onSaved = callback
	s.mu.Unlock()
}

func recordQueueKey(rec *proto.Record) string {
	return fmt.Sprintf("%s/%d", rec.Session, rec.Index)
}

func recordBodyWeight(rec *proto.Record) int64 {
	return int64(len(rec.ReqBodyBlob) + len(rec.RespBodyBlob))
}

func (s *AsyncSink) SaveRecord(rec *proto.Record) {
	if rec == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		if err := s.db.SaveRecord(rec); err != nil {
			log.Printf("[async_sink] save after close error: %v", err)
		}
		return
	}
	key := recordQueueKey(rec)
	if old := s.pending[key]; old != nil {
		s.pendingBytes -= recordBodyWeight(old)
	}
	weight := recordBodyWeight(rec)
	if weight > 0 && s.pendingBytes+weight > maxPendingBodyBytes {
		// Keep metadata/final status available while preventing an overloaded
		// persistence queue from retaining unbounded image bytes.
		copyRec := *rec
		copyRec.ReqBodyBlob = nil
		copyRec.RespBodyBlob = nil
		copyRec.BodyTruncated = true
		copyRec.BodyComplete = false
		copyRec.Error = "body persistence backlog exceeded memory budget"
		rec = &copyRec
		weight = 0
		s.droppedBodies.Add(1)
	}
	s.pending[key] = rec
	s.pendingBytes += weight
	select {
	case s.signal <- key:
	default:
		// The periodic drain below will discover this key. Never synchronously
		// write SQLite from a proxy callback.
	}
	s.mu.Unlock()
}

func (s *AsyncSink) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.signal)
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *AsyncSink) run() {
	defer s.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case key, ok := <-s.signal:
			if !ok {
				s.drainPending()
				return
			}
			s.process(key)
			// Drain a bounded batch so a burst does not monopolize the worker.
			for drained := 0; drained < 127; drained++ {
				select {
				case key, ok := <-s.signal:
					if !ok {
						s.drainPending()
						return
					}
					s.process(key)
				default:
					drained = 127
				}
			}
		case <-ticker.C:
			// A full signal channel leaves pending records without a
			// notification. Periodically scan so they are persisted during
			// normal operation, not only when the sink shuts down.
			s.drainPending()
		}
	}
}

func (s *AsyncSink) drainPending() {
	for {
		s.mu.Lock()
		var key string
		for pendingKey := range s.pending {
			key = pendingKey
			break
		}
		s.mu.Unlock()
		if key == "" {
			return
		}
		s.process(key)
	}
}

func (s *AsyncSink) process(key string) {
	s.mu.Lock()
	rec := s.pending[key]
	if rec != nil {
		delete(s.pending, key)
		s.pendingBytes -= recordBodyWeight(rec)
	}
	s.mu.Unlock()
	if rec != nil {
		if err := s.db.SaveRecord(rec); err != nil {
			log.Printf("[async_sink] save error: %v", err)
			return
		}
		s.mu.Lock()
		callback := s.onSaved
		s.mu.Unlock()
		if callback != nil {
			callback(rec)
		}
	}
}

func (s *AsyncSink) DroppedBodies() uint64 {
	return s.droppedBodies.Load()
}
