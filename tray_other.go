//go:build !darwin && !windows

package main

func startTray(app *App) {
	// Stub for platforms without a system tray implementation.
}

func stopTray() {
	// Stub for platforms without a system tray implementation.
}
