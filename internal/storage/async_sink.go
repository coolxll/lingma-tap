package storage

import (
	"log"
	"sync"
	"sync/atomic"

	"github.com/coolxll/lingma-tap/internal/proto"
)

const defaultQueueCapacity = 10000

type AsyncSink struct {
	db     *DB
	queue  chan *proto.Record
	closed atomic.Bool
	mu     sync.RWMutex
	wg     sync.WaitGroup
}

func NewAsyncSink(db *DB, capacity int) *AsyncSink {
	if capacity <= 0 {
		capacity = defaultQueueCapacity
	}
	s := &AsyncSink{
		db:    db,
		queue: make(chan *proto.Record, capacity),
	}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *AsyncSink) SaveRecord(rec *proto.Record) {
	if s.closed.Load() {
		s.db.SaveRecord(rec)
		return
	}

	select {
	case s.queue <- rec:
	default:
		// Backpressure: fall back to synchronous write
		s.db.SaveRecord(rec)
	}
}

func (s *AsyncSink) Close() {
	if s.closed.CompareAndSwap(false, true) {
		close(s.queue)
		s.wg.Wait()
	}
}

func (s *AsyncSink) run() {
	defer s.wg.Done()
	for rec := range s.queue {
		if err := s.db.SaveRecord(rec); err != nil {
			log.Printf("[async_sink] save error: %v", err)
		}
	}
}
