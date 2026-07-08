//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
void initTray(const unsigned char* iconData, int iconLength);
*/
import "C"
import (
	_ "embed"
	"log"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed assets/tray_icon.png
var trayIconBytes []byte

var appInstance *App

func startTray(app *App) {
	appInstance = app
	if len(trayIconBytes) == 0 {
		log.Printf("[tray] macOS tray icon asset is empty")
		return
	}

	log.Printf("[tray] initializing macOS status item with %d-byte icon", len(trayIconBytes))
	C.initTray((*C.uchar)(unsafe.Pointer(&trayIconBytes[0])), C.int(len(trayIconBytes)))
}

//export goShowWindow
func goShowWindow() {
	if appInstance != nil && appInstance.ctx != nil {
		runtime.Show(appInstance.ctx)
		runtime.WindowShow(appInstance.ctx)
	}
}

//export goHideWindow
func goHideWindow() {
	if appInstance != nil && appInstance.ctx != nil {
		runtime.Hide(appInstance.ctx)
	}
}

func stopTray() {
	// The macOS status item is owned by NSApp and is cleaned up on process exit.
}
