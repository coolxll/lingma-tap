package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

func TestAsyncSinkRecoversKeysDroppedWhenSignalQueueIsFull(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "async_sink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Hold the DB write mutex so the worker consumes one notification and
	// blocks in SaveRecord while subsequent distinct keys fill the signal
	// channel. The third key has no notification and must be found by the
	// periodic pending scan.
	db.writeMu.Lock()
	sink := NewAsyncSink(db, 1)
	defer sink.Close()
	for i := 0; i < 3; i++ {
		sink.SaveRecord(&proto.Record{
			Ts: time.Now().UTC().Format(time.RFC3339Nano), Session: "burst", Index: i,
			Direction: "C2S", Method: "POST", Path: "/v1/chat", Source: "proxy",
		})
	}
	db.writeMu.Unlock()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && db.RecordCount() != 3 {
		time.Sleep(20 * time.Millisecond)
	}
	sink.Close()
	if got := db.RecordCount(); got != 3 {
		t.Fatalf("record count=%d, want 3 after signal overflow recovery", got)
	}
}
