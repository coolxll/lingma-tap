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
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed assets/tray_icon.png
var trayIconBytes []byte

var appInstance *App

func startTray(app *App) {
	appInstance = app
	if len(trayIconBytes) > 0 {
		C.initTray((*C.uchar)(unsafe.Pointer(&trayIconBytes[0])), C.int(len(trayIconBytes)))
	}
}

//export goShowWindow
func goShowWindow() {
	if appInstance != nil && appInstance.ctx != nil {
		runtime.WindowShow(appInstance.ctx)
	}
}

//export goHideWindow
func goHideWindow() {
	if appInstance != nil && appInstance.ctx != nil {
		runtime.WindowHide(appInstance.ctx)
	}
}
