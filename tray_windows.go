//go:build windows

package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"log"
	goruntime "runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

//go:embed assets/tray_icon.png
var windowsTrayIconBytes []byte

const (
	wmNull          = 0x0000
	wmClose         = 0x0010
	wmDestroy       = 0x0002
	wmCommand       = 0x0111
	wmCancelMode    = 0x001F
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B
	wmApp           = 0x8000

	trayCallbackMessage = wmApp + 1

	// NOTIFYICON_VERSION_4 notification events delivered in LOWORD(lParam).
	// NIN_SELECT is a plain select (mouse or keyboard Enter); NIN_KEYSELECT
	// is the keyboard context-menu activation (Shift+F10 / menu key).
	// WM_USER = 0x0400; balloons (0x0402+) are distinct and unused here.
	ninSelect     = 0x0400
	ninKeySelect  = 0x0401

	nimAdd        = 0x00000000
	nimDelete     = 0x00000002
	nimSetFocus   = 0x00000003
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	notifyIconVersion4 = 4

	mfString    = 0x00000000
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmNonotify    = 0x0080
	tpmReturnCmd   = 0x0100

	idTrayIcon = 1
	idShow     = 1001
	idHide     = 1002
	idQuit     = 1003

	idiApplication = 32512

	smCxSmIcon = 49
	smCySmIcon = 50

	biRGB         = 0
	dibRGBColors  = 0
	imageIcon     = 1
	lrDefaultSize = 0x00000040
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procRegisterWindowMsgW  = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procCreateIconIndirect  = user32.NewProc("CreateIconIndirect")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procCreateDIBSection    = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap        = gdi32.NewProc("CreateBitmap")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
)

type windowsTray struct {
	app            *App
	hwnd           windows.Handle
	icon           windows.Handle
	taskbarCreated uint32
	done           chan struct{}
	stopping       bool
	mu             sync.Mutex
}

var (
	windowsTrayInstance *windowsTray
	windowsTrayMu       sync.Mutex
)

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type point struct {
	X int32
	Y int32
}

type msg struct {
	Hwnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             windows.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  windows.Handle
	HbmColor windows.Handle
}

func startTray(app *App) {
	windowsTrayMu.Lock()
	defer windowsTrayMu.Unlock()
	if windowsTrayInstance != nil {
		return
	}

	tray := &windowsTray{
		app:  app,
		done: make(chan struct{}),
	}
	windowsTrayInstance = tray
	go tray.run()
}

func stopTray() {
	windowsTrayMu.Lock()
	tray := windowsTrayInstance
	if tray == nil {
		windowsTrayMu.Unlock()
		return
	}

	// Mark stopping first so in-flight callbacks and the TaskbarCreated
	// handler bail out instead of touching a window we are about to destroy.
	tray.mu.Lock()
	tray.stopping = true
	hwnd := tray.hwnd
	tray.mu.Unlock()

	// Remove the notification-area icon while the instance still resolves
	// to this tray, so WM_DESTROY's nil-guard does not skip cleanup and the
	// shell does not orphan the icon when the window is torn down.
	_ = tray.deleteTrayIcon()

	// Clear the instance last so a WM_CLOSE/WM_DESTROY dispatched from the
	// menu's modal loop still finds the tray (and DefWindowProc fallback).
	windowsTrayInstance = nil
	windowsTrayMu.Unlock()

	if hwnd != 0 {
		// WM_CANCELMODE breaks an active TrackPopupMenu modal loop cleanly
		// (its owner is this hwnd), avoiding the owner-destroyed-while-
		// tracking race that can stall the 2s shutdown wait.
		procPostMessageW.Call(uintptr(hwnd), wmCancelMode, 0, 0)
		procPostMessageW.Call(uintptr(hwnd), wmClose, 0, 0)
	}

	select {
	case <-tray.done:
	case <-time.After(2 * time.Second):
		log.Printf("[tray] timed out waiting for Windows tray shutdown")
	}
}

func (t *windowsTray) run() {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()
	defer close(t.done)

	className, _ := windows.UTF16PtrFromString("LingmaTapTrayWindow")
	hInstancePtr, _, err := procGetModuleHandleW.Call(0)
	if hInstancePtr == 0 {
		log.Printf("[tray] Windows GetModuleHandleW error: %v", err)
		return
	}
	hInstance := windows.Handle(hInstancePtr)

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   windows.NewCallback(windowsTrayWndProc),
		HInstance:     hInstance,
		LpszClassName: className,
	}
	if atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		log.Printf("[tray] Windows RegisterClassExW error: %v", callErr)
		return
	}

	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0,
		0, 0, 0, 0,
		0,
		0,
		uintptr(hInstance),
		0,
	)
	if hwnd == 0 {
		log.Printf("[tray] Windows CreateWindowExW error: %v", callErr)
		return
	}

	t.mu.Lock()
	t.hwnd = windows.Handle(hwnd)
	t.taskbarCreated = registerWindowMessage("TaskbarCreated")
	stopping := t.stopping
	t.mu.Unlock()
	if stopping {
		procDestroyWindow.Call(hwnd)
		return
	}

	icon, err := createTrayIconFromPNG()
	iconOwned := true
	if err != nil {
		log.Printf("[tray] Windows tray PNG icon error: %v; using fallback icon", err)
		icon = loadFallbackIcon()
		iconOwned = false
	}
	t.mu.Lock()
	t.icon = icon
	stopping = t.stopping
	t.mu.Unlock()
	if stopping {
		if icon != 0 && iconOwned {
			procDestroyIcon.Call(uintptr(icon))
		}
		procDestroyWindow.Call(hwnd)
		return
	}

	if err := t.addTrayIcon(); err != nil {
		log.Printf("[tray] Windows Shell_NotifyIcon add error: %v", err)
	} else {
		log.Printf("[tray] initialized Windows system tray")
	}

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	_ = t.deleteTrayIcon()
	if icon != 0 && iconOwned {
		procDestroyIcon.Call(uintptr(icon))
	}
}

func windowsTrayWndProc(hwnd uintptr, message uint32, wParam uintptr, lParam uintptr) uintptr {
	windowsTrayMu.Lock()
	tray := windowsTrayInstance
	windowsTrayMu.Unlock()
	if tray != nil && tray.taskbarCreated != 0 && message == tray.taskbarCreated {
		// Ignore Explorer-restart re-add races once shutdown is underway,
		// otherwise Shell_NotifyIcon(NIM_ADD) targets a doomed window.
		tray.mu.Lock()
		stopping := tray.stopping
		tray.mu.Unlock()
		if stopping {
			return 0
		}
		if err := tray.addTrayIcon(); err != nil {
			log.Printf("[tray] Windows re-add tray icon error: %v", err)
		}
		return 0
	}

	switch message {
	case trayCallbackMessage:
		if tray == nil {
			break
		}
		event, ok := trayCallbackEvent(wParam, lParam)
		if !ok {
			break
		}
		switch event {
		case wmLButtonUp, wmLButtonDblClk, ninSelect:
			go tray.showWindow()
			return 0
		case wmRButtonUp, wmContextMenu, ninKeySelect:
			tray.showMenu()
			return 0
		}
	case wmCommand:
		if tray != nil {
			go tray.handleCommand(uint16(wParam & 0xffff))
			return 0
		}
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if tray != nil {
			_ = tray.deleteTrayIcon()
		}
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func (t *windowsTray) addTrayIcon() error {
	t.mu.Lock()
	hwnd := t.hwnd
	icon := t.icon
	t.mu.Unlock()
	if hwnd == 0 {
		return fmt.Errorf("tray window is not initialized")
	}

	nid := newNotifyIconData(hwnd, icon)
	ok, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if ok == 0 {
		return err
	}

	nid.UVersion = notifyIconVersion4
	ok, _, err = procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&nid)))
	if ok == 0 {
		return err
	}
	return nil
}

func (t *windowsTray) deleteTrayIcon() error {
	t.mu.Lock()
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd == 0 {
		return nil
	}

	nid := newNotifyIconData(hwnd, 0)
	ok, _, err := procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	if ok == 0 {
		return err
	}
	return nil
}

func (t *windowsTray) setTrayFocus() {
	t.mu.Lock()
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd == 0 {
		return
	}

	nid := newNotifyIconData(hwnd, 0)
	if ok, _, err := procShellNotifyIconW.Call(nimSetFocus, uintptr(unsafe.Pointer(&nid))); ok == 0 {
		log.Printf("[tray] Windows Shell_NotifyIcon set focus error: %v", err)
	}
}

func newNotifyIconData(hwnd windows.Handle, icon windows.Handle) notifyIconData {
	nid := notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:             hwnd,
		UID:              idTrayIcon,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: trayCallbackMessage,
		HIcon:            icon,
	}
	copy(nid.SzTip[:], windows.StringToUTF16("Lingma Tap"))
	return nid
}

func (t *windowsTray) showMenu() {
	if t.isStopping() {
		return
	}
	hMenu, _, err := procCreatePopupMenu.Call()
	if hMenu == 0 {
		log.Printf("[tray] Windows CreatePopupMenu error: %v", err)
		return
	}
	defer procDestroyMenu.Call(hMenu)

	appendMenu(hMenu, mfString, idShow, "Show Window")
	appendMenu(hMenu, mfString, idHide, "Hide Window")
	appendMenu(hMenu, mfSeparator, 0, "")
	appendMenu(hMenu, mfString, idQuit, "Quit Lingma Tap")

	var pt point
	if ok, _, callErr := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); ok == 0 {
		log.Printf("[tray] Windows GetCursorPos error: %v", callErr)
		return
	}

	t.mu.Lock()
	hwnd := t.hwnd
	stopping := t.stopping
	t.mu.Unlock()
	if stopping || hwnd == 0 {
		return
	}
	procSetForegroundWindow.Call(uintptr(hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(
		hMenu,
		tpmRightButton|tpmReturnCmd|tpmNonotify,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		uintptr(hwnd),
		0,
	)
	// TrackPopupMenu has returned (either the user picked an item, dismissed
	// the menu, or stopTray's WM_CANCELMODE broke the modal loop). Re-check
	// before posting the follow-up WM_NULL or dispatching a command, since
	// shutdown may have destroyed hwnd while the menu was tracking.
	t.mu.Lock()
	hwnd = t.hwnd
	stopping = t.stopping
	t.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(uintptr(hwnd), wmNull, 0, 0)
		t.setTrayFocus()
	}
	if cmd != 0 && !stopping {
		go t.handleCommand(uint16(cmd))
	}
}

func trayCallbackEvent(wParam uintptr, lParam uintptr) (uint32, bool) {
	event := uint32(lParam & 0xffff)
	iconID := uint32((lParam >> 16) & 0xffff)
	if iconID == 0 {
		iconID = uint32(wParam & 0xffff)
		event = uint32(lParam)
	}
	if iconID != idTrayIcon {
		return 0, false
	}
	return event, true
}

func appendMenu(menu uintptr, flags uintptr, id uintptr, label string) {
	var labelPtr uintptr
	if label != "" {
		labelPtr = uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(label)))
	}
	if ok, _, err := procAppendMenuW.Call(menu, flags, id, labelPtr); ok == 0 {
		log.Printf("[tray] Windows AppendMenuW(%q) error: %v", label, err)
	}
}

func (t *windowsTray) handleCommand(command uint16) {
	switch command {
	case idShow:
		t.showWindow()
	case idHide:
		t.hideWindow()
	case idQuit:
		t.quit()
	}
}

func (t *windowsTray) showWindow() {
	if t.isStopping() {
		return
	}
	if t.app != nil && t.app.ctx != nil {
		runtime.Show(t.app.ctx)
		runtime.WindowShow(t.app.ctx)
	}
}

func (t *windowsTray) hideWindow() {
	if t.isStopping() {
		return
	}
	if t.app != nil && t.app.ctx != nil {
		runtime.Hide(t.app.ctx)
	}
}

func (t *windowsTray) quit() {
	if t.isStopping() {
		return
	}
	if t.app != nil && t.app.ctx != nil {
		runtime.Quit(t.app.ctx)
	}
}

// isStopping reports whether shutdown is in progress. Goroutines spawned for
// tray callbacks consult this before touching the Wails ctx, so a callback
// queued against a window that is being torn down does not race the runtime.
func (t *windowsTray) isStopping() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopping
}

func registerWindowMessage(name string) uint32 {
	ptr, _ := windows.UTF16PtrFromString(name)
	ret, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(ptr)))
	return uint32(ret)
}

func createTrayIconFromPNG() (windows.Handle, error) {
	if len(windowsTrayIconBytes) == 0 {
		return 0, fmt.Errorf("embedded tray icon is empty")
	}
	img, _, err := image.Decode(bytes.NewReader(windowsTrayIconBytes))
	if err != nil {
		return 0, err
	}

	width := systemMetric(smCxSmIcon, 32)
	height := systemMetric(smCySmIcon, 32)
	pixels := resizeToBGRA(img, width, height)

	bmi := bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(width),
		Height:      -int32(height),
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
		SizeImage:   uint32(len(pixels)),
	}

	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return 0, fmt.Errorf("GetDC returned 0")
	}
	defer procReleaseDC.Call(0, hdc)

	var bits uintptr
	hColor, _, err := procCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bmi)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hColor == 0 {
		return 0, fmt.Errorf("CreateDIBSection: %w", err)
	}
	defer procDeleteObject.Call(hColor)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(bits)), len(pixels)), pixels)

	hMask, _, err := procCreateBitmap.Call(uintptr(width), uintptr(height), 1, 1, 0)
	if hMask == 0 {
		return 0, fmt.Errorf("CreateBitmap mask: %w", err)
	}
	defer procDeleteObject.Call(hMask)

	info := iconInfo{
		FIcon:    1,
		HbmMask:  windows.Handle(hMask),
		HbmColor: windows.Handle(hColor),
	}
	hIcon, _, err := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	if hIcon == 0 {
		return 0, fmt.Errorf("CreateIconIndirect: %w", err)
	}
	return windows.Handle(hIcon), nil
}

func resizeToBGRA(img image.Image, width int, height int) []byte {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	pixels := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		sy := bounds.Min.Y + y*srcH/height
		for x := 0; x < width; x++ {
			sx := bounds.Min.X + x*srcW/width
			c := color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
			r := c.R
			g := c.G
			b := c.B
			a := c.A
			if a != 0xff {
				r = uint8(uint16(r) * uint16(a) / 255)
				g = uint8(uint16(g) * uint16(a) / 255)
				b = uint8(uint16(b) * uint16(a) / 255)
			}
			i := (y*width + x) * 4
			pixels[i+0] = b
			pixels[i+1] = g
			pixels[i+2] = r
			pixels[i+3] = a
		}
	}
	return pixels
}

func systemMetric(index int32, fallback int) int {
	ret, _, _ := procGetSystemMetrics.Call(uintptr(index))
	if ret == 0 {
		return fallback
	}
	return int(ret)
}

func loadFallbackIcon() windows.Handle {
	hIcon, _, _ := procLoadImageW.Call(0, idiApplication, imageIcon, 0, 0, lrDefaultSize)
	if hIcon != 0 {
		return windows.Handle(hIcon)
	}
	hIcon, _, _ = procLoadIconW.Call(0, idiApplication)
	return windows.Handle(hIcon)
}
