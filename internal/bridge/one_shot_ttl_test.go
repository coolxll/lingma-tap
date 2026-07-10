package bridge

import (
	"testing"
	"time"
)

func TestOneShotTTLSetConsumesOnce(t *testing.T) {
	set := newOneShotTTLSet(2)
	if !set.mark("request", time.Minute) {
		t.Fatal("mark returned false")
	}
	if !set.consume("request") {
		t.Fatal("first consume returned false")
	}
	if set.consume("request") {
		t.Fatal("second consume returned true")
	}
}

func TestOneShotTTLSetExpiresAndEvicts(t *testing.T) {
	set := newOneShotTTLSet(1)
	set.mark("expired", time.Nanosecond)
	time.Sleep(time.Millisecond)
	if set.consume("expired") {
		t.Fatal("expired key was consumed")
	}

	set.mark("first", time.Minute)
	set.mark("second", time.Hour)
	if set.consume("first") {
		t.Fatal("oldest key was not evicted")
	}
	if !set.consume("second") {
		t.Fatal("newest key was evicted")
	}
}
