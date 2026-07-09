//go:build windows

package main

import "testing"

func TestTrayCallbackEvent(t *testing.T) {
	t.Run("notify icon version 4 layout", func(t *testing.T) {
		event, ok := trayCallbackEvent(0, uintptr(idTrayIcon<<16)|uintptr(wmContextMenu))
		if !ok {
			t.Fatalf("expected callback for tray icon")
		}
		if event != wmContextMenu {
			t.Fatalf("event = %#x, want %#x", event, wmContextMenu)
		}
	})

	t.Run("legacy layout", func(t *testing.T) {
		event, ok := trayCallbackEvent(idTrayIcon, wmRButtonUp)
		if !ok {
			t.Fatalf("expected callback for tray icon")
		}
		if event != wmRButtonUp {
			t.Fatalf("event = %#x, want %#x", event, wmRButtonUp)
		}
	})

	t.Run("different icon id", func(t *testing.T) {
		if _, ok := trayCallbackEvent(0, uintptr(2<<16)|uintptr(wmContextMenu)); ok {
			t.Fatalf("expected callback for other icon id to be ignored")
		}
	})
}
