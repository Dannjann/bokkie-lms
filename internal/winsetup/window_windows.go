//go:build windows

package winsetup

import (
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The station window is plain Win32, called through syscalls.
//
// Not a toolkit: every Go GUI library either needs cgo, which this project has
// ruled out (see the Makefile), or wraps these same calls in several thousand
// lines nobody here has read. The window has a headline, six rows and three
// buttons; the calls below are all of it.

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procIsDialogMessageW = user32.NewProc("IsDialogMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procSendMessageW     = user32.NewProc("SendMessageW")
	procSetWindowTextW   = user32.NewProc("SetWindowTextW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procSetTimer         = user32.NewProc("SetTimer")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procEnableWindow     = user32.NewProc("EnableWindow")
	procFindWindowW      = user32.NewProc("FindWindowW")
	procSetForegroundWnd = user32.NewProc("SetForegroundWindow")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procAdjustWindowRect = user32.NewProc("AdjustWindowRectEx")
	procGetDC            = user32.NewProc("GetDC")
	procReleaseDC        = user32.NewProc("ReleaseDC")
	procInvalidateRect   = user32.NewProc("InvalidateRect")

	procCreateFontW    = gdi32.NewProc("CreateFontW")
	procGetDeviceCaps  = gdi32.NewProc("GetDeviceCaps")
	procSetTextColor   = gdi32.NewProc("SetTextColor")
	procSetBkMode      = gdi32.NewProc("SetBkMode")
	procGetStockObject = gdi32.NewProc("GetStockObject")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procFreeConsole      = kernel32.NewProc("FreeConsole")
)

const (
	wsOverlapped     = 0x00000000
	wsCaption        = 0x00C00000
	wsSysMenu        = 0x00080000
	wsMinimizeBox    = 0x00020000
	wsThickFrame     = 0x00040000
	wsMaximizeBox    = 0x00010000
	wsVisible        = 0x10000000
	wsChild          = 0x40000000
	wsTabStop        = 0x00010000
	wsVScroll        = 0x00200000
	wsExControlPar   = 0x00010000
	wsExClientEdge   = 0x00000200
	bsPushButton     = 0x0
	bsDefPushButton  = 0x1
	ssNoPrefix       = 0x80
	ssEndEllipsis    = 0x4000
	esMultiline      = 0x4
	esAutoVScroll    = 0x40
	esReadOnly       = 0x800
	wmDestroy        = 0x0002
	wmSize           = 0x0005
	wmClose          = 0x0010
	wmQueryEndSess   = 0x0011
	wmEndSession     = 0x0016
	wmSetFont        = 0x0030
	wmCommand        = 0x0111
	wmTimer          = 0x0113
	wmCtlColorStatic = 0x0138
	wmApp            = 0x8000
	emSetSel         = 0x00B1
	swShow           = 5
	swRestore        = 9
	cwUseDefault     = 0x80000000
	idcArrow         = 32512
	logPixelsY       = 90
	transparent      = 1
	whiteBrush       = 0
	smCxScreen       = 0
	smCyScreen       = 1
	imageIcon        = 1
	lrDefaultSize    = 0x40
	lrShared         = 0x8000
	bnClicked        = 0

	// iconID is where the icon group lands in the resources the Makefile
	// embeds: rsrc numbers the manifest 1 and the icon group 2.
	iconID = 2

	stationClass = "BokkieStationWindow"
	reportClass  = "BokkieReportWindow"
)

// Messages the window posts to itself from other goroutines. Win32 windows
// may only be touched from the thread that made them, so anything learned
// elsewhere arrives as one of these.
const (
	msgStopped = wmApp + 1 // the station has finished stopping
	msgReport  = wmApp + 2 // a check finished; its text is in reportText
)

// Control ids, for WM_COMMAND.
const (
	btnOpen  = 101
	btnCheck = 102
	btnStop  = 103
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type point struct{ X, Y int32 }

type winMsg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type rect struct{ Left, Top, Right, Bottom int32 }

// stationWindow is the one window of this process. A package variable rather
// than a lookup from the handle because there is exactly one, and the window
// procedure is a C callback with nowhere to carry a Go pointer.
type stationWindow struct {
	hooks StationHooks
	dpi   int

	hwnd               windows.HWND
	headline, detail   windows.HWND
	labels, values     [6]windows.HWND
	open, check, stop  windows.HWND
	fontNormal, fontHl uintptr
	fontMono           uintptr
	labelBrush         uintptr

	last     StationView
	tone     Tone
	stopping bool
	checking bool

	mu         sync.Mutex
	reportText string
}

var (
	current   *stationWindow
	stopOnce  sync.Once
	callbacks struct {
		station, report uintptr
		once            sync.Once
	}
)

// FocusRunningStation brings an already-open station window to the front and
// reports whether there was one. Two windows would be two stations fighting
// over one port and one ledger, and the second would only ever lose.
func FocusRunningStation() bool {
	hwnd := findWindow(stationClass)
	if hwnd == 0 {
		return false
	}
	procShowWindow.Call(uintptr(hwnd), swRestore)
	procSetForegroundWnd.Call(uintptr(hwnd))
	return true
}

// HideConsole detaches from the console Windows made for this process, so the
// station is one window rather than a window and a black box behind it.
func HideConsole() { procFreeConsole.Call() }

// OpenInBrowser opens address in whatever browser this computer uses for web
// pages — a normal window, not the station's own.
func OpenInBrowser(address string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, err := syscall.UTF16PtrFromString(address)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

// ShowMessage is a modal message box owned by the station window, when there
// is one.
func ShowMessage(title, message string, isError bool) {
	var owner windows.HWND
	if current != nil {
		owner = current.hwnd
	}
	flags := uint32(windows.MB_OK)
	if isError {
		flags |= windows.MB_ICONERROR
	} else {
		flags |= windows.MB_ICONINFORMATION
	}
	windows.MessageBox(owner, utf16(message), utf16(title), flags)
}

// AskYesNo is a modal question owned by the station window.
func AskYesNo(title, question string) bool {
	var owner windows.HWND
	if current != nil {
		owner = current.hwnd
	}
	answer, _ := windows.MessageBox(owner, utf16(question), utf16(title),
		windows.MB_YESNO|windows.MB_ICONQUESTION)
	return answer == 6 // IDYES
}

// RunStationWindow shows the station window and returns once it has closed —
// which is only ever after hooks.Stop has returned.
func RunStationWindow(hooks StationHooks) error {
	// A window belongs to the thread that made it, and Go moves goroutines
	// between threads freely. Without this the message loop can wake up on a
	// thread that has no windows and wait there forever.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := &stationWindow{hooks: hooks}
	current = w

	instance, _, _ := procGetModuleHandleW.Call(0)
	if err := registerClasses(windows.Handle(instance)); err != nil {
		return err
	}

	w.dpi = systemDPI()
	w.fontNormal = makeFont("Segoe UI", 9, 400, w.dpi)
	w.fontHl = makeFont("Segoe UI Semibold", 15, 600, w.dpi)
	w.fontMono = makeFont("Consolas", 9, 400, w.dpi)

	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox)
	width, height := w.outerSize(style)
	x, y := centred(width, height)

	hwnd, _, err := procCreateWindowExW.Call(
		wsExControlPar,
		uintptr(unsafe.Pointer(utf16(stationClass))),
		uintptr(unsafe.Pointer(utf16(hooks.Title))),
		style,
		uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		0, 0, instance, 0)
	if hwnd == 0 {
		return err
	}
	w.hwnd = windows.HWND(hwnd)
	w.build(windows.Handle(instance))
	w.refresh()

	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	procSetForegroundWnd.Call(hwnd)
	procSetTimer.Call(hwnd, 1, 1000, 0)

	if hooks.StopWanted != nil {
		go func() {
			<-hooks.StopWanted
			procPostMessageW.Call(hwnd, wmClose, 0, 0)
		}()
	}

	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		// IsDialogMessage is what makes Tab move between the buttons and
		// Enter press the default one, as in every other Windows window.
		if handled, _, _ := procIsDialogMessageW.Call(hwnd, uintptr(unsafe.Pointer(&m))); handled != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func registerClasses(instance windows.Handle) error {
	callbacks.once.Do(func() {
		callbacks.station = syscall.NewCallback(stationProc)
		callbacks.report = syscall.NewCallback(reportProc)
	})
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	icon, _, _ := procLoadImageW.Call(uintptr(instance), iconID, imageIcon, 0, 0, lrDefaultSize|lrShared)
	white, _, _ := procGetStockObject.Call(whiteBrush)

	for _, c := range []struct {
		name string
		proc uintptr
	}{{stationClass, callbacks.station}, {reportClass, callbacks.report}} {
		wc := wndClassEx{
			WndProc:    c.proc,
			Instance:   instance,
			Icon:       windows.Handle(icon),
			IconSm:     windows.Handle(icon),
			Cursor:     windows.Handle(cursor),
			Background: windows.Handle(white),
			ClassName:  utf16(c.name),
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			return err
		}
	}
	return nil
}

// Layout, in pixels at 96 DPI. scale turns them into this display's pixels.
const (
	layoutWidth   = 500
	layoutMargin  = 20
	layoutLabelW  = 120
	layoutRowH    = 22
	layoutRowsTop = 132
	layoutButtonH = 30
)

func (w *stationWindow) scale(n int) int { return n * w.dpi / 96 }

func (w *stationWindow) clientHeight() int {
	return layoutRowsTop + len(w.values)*layoutRowH + 16 + layoutButtonH + layoutMargin
}

func (w *stationWindow) outerSize(style uintptr) (int, int) {
	r := rect{Right: int32(w.scale(layoutWidth)), Bottom: int32(w.scale(w.clientHeight()))}
	procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), style, 0, wsExControlPar)
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

func (w *stationWindow) build(instance windows.Handle) {
	m, inner := layoutMargin, layoutWidth-2*layoutMargin
	w.headline = w.child(instance, "STATIC", "", ssNoPrefix, m, 16, inner, 34, w.fontHl)
	w.detail = w.child(instance, "STATIC", "", ssNoPrefix, m, 54, inner, 70, w.fontNormal)
	for i := range w.values {
		y := layoutRowsTop + i*layoutRowH
		w.labels[i] = w.child(instance, "STATIC", "", ssNoPrefix, m, y, layoutLabelW, layoutRowH, w.fontNormal)
		w.values[i] = w.child(instance, "STATIC", "", ssNoPrefix|ssEndEllipsis,
			m+layoutLabelW, y, inner-layoutLabelW, layoutRowH, w.fontNormal)
	}

	y := layoutRowsTop + len(w.values)*layoutRowH + 16
	w.open = w.button(instance, btnOpen, "&Open Bokkie in browser", bsDefPushButton, m, y, 190)
	w.check = w.button(instance, btnCheck, "Run a &check", bsPushButton, m+200, y, 120)
	w.stop = w.button(instance, btnStop, "&Stop Bokkie", bsPushButton, layoutWidth-m-120, y, 120)

	w.labelBrush, _, _ = procGetStockObject.Call(whiteBrush)
}

func (w *stationWindow) child(instance windows.Handle, class, text string, style, x, y, cx, cy int, font uintptr) windows.HWND {
	hwnd, _, _ := procCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(utf16(class))),
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(wsChild|wsVisible|style),
		uintptr(w.scale(x)), uintptr(w.scale(y)), uintptr(w.scale(cx)), uintptr(w.scale(cy)),
		uintptr(w.hwnd), 0, uintptr(instance), 0)
	procSendMessageW.Call(hwnd, wmSetFont, font, 1)
	return windows.HWND(hwnd)
}

func (w *stationWindow) button(instance windows.Handle, id int, text string, style, x, y, cx int) windows.HWND {
	hwnd, _, _ := procCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(utf16("BUTTON"))),
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(wsChild|wsVisible|wsTabStop|style),
		uintptr(w.scale(x)), uintptr(w.scale(y)), uintptr(w.scale(cx)), uintptr(w.scale(layoutButtonH)),
		uintptr(w.hwnd), uintptr(id), uintptr(instance), 0)
	procSendMessageW.Call(hwnd, wmSetFont, w.fontNormal, 1)
	return windows.HWND(hwnd)
}

// refresh redraws whatever changed since the last snapshot. Setting text that
// has not changed makes static controls flicker, so it is compared first.
func (w *stationWindow) refresh() {
	v := w.hooks.Snapshot()
	if w.stopping {
		// Once it is going down it stays going down, whatever the station
		// reports while it does.
		v = DescribeStation(StationFacts{State: StationStopping})
	}

	if v.Headline != w.last.Headline || v.Tone != w.tone {
		w.tone = v.Tone
		setText(w.headline, v.Headline)
		procInvalidateRect.Call(uintptr(w.headline), 0, 1)
	}
	if v.Detail != w.last.Detail {
		setText(w.detail, crlf(v.Detail))
	}
	for i := range w.values {
		var row Row
		if i < len(v.Rows) {
			row = v.Rows[i]
		}
		var was Row
		if i < len(w.last.Rows) {
			was = w.last.Rows[i]
		}
		if row != was {
			setText(w.labels[i], row.Label)
			setText(w.values[i], row.Value)
		}
	}

	enable(w.open, v.CanOpen && !v.Busy)
	enable(w.check, !v.Busy && !w.checking)
	enable(w.stop, !v.Busy)
	w.last = v
}

// beginStop is the one way the station goes down from this window: the X, the
// Stop button, Alt+F4, and the web screen's Stop all arrive here.
func (w *stationWindow) beginStop() {
	if w.stopping {
		return
	}
	w.stopping = true
	w.refresh()
	go func() {
		stopOnce.Do(w.hooks.Stop)
		procPostMessageW.Call(uintptr(w.hwnd), msgStopped, 0, 0)
	}()
}

func (w *stationWindow) beginCheck() {
	if w.checking {
		return
	}
	w.checking = true
	setText(w.check, "Checking…")
	w.refresh()
	go func() {
		report := w.hooks.Check()
		w.mu.Lock()
		w.reportText = report
		w.mu.Unlock()
		procPostMessageW.Call(uintptr(w.hwnd), msgReport, 0, 0)
	}()
}

func stationProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	w := current
	if w == nil || w.hwnd == 0 {
		return defProc(hwnd, msg, wparam, lparam)
	}

	switch msg {
	case wmTimer:
		w.refresh()
		return 0

	case wmCommand:
		if (wparam>>16)&0xffff != bnClicked {
			break
		}
		switch wparam & 0xffff {
		case btnOpen:
			go w.hooks.Open()
		case btnCheck:
			w.beginCheck()
		case btnStop:
			w.beginStop()
		}
		return 0

	case msgReport:
		w.checking = false
		setText(w.check, "Run a &check")
		w.refresh()
		w.mu.Lock()
		text := w.reportText
		w.mu.Unlock()
		showReport(w, text)
		return 0

	case wmCtlColorStatic:
		hdc, control := wparam, lparam
		procSetBkMode.Call(hdc, transparent)
		switch windows.HWND(control) {
		case w.headline:
			procSetTextColor.Call(hdc, toneColour(w.tone))
		case w.labels[0], w.labels[1], w.labels[2], w.labels[3], w.labels[4], w.labels[5]:
			procSetTextColor.Call(hdc, rgb(0x5f, 0x6b, 0x7a))
		default:
			procSetTextColor.Call(hdc, rgb(0x1f, 0x29, 0x37))
		}
		return w.labelBrush

	case wmClose:
		w.beginStop()
		return 0

	case msgStopped:
		procDestroyWindow.Call(hwnd)
		return 0

	case wmQueryEndSess:
		return 1

	case wmEndSession:
		// Windows is signing out or shutting down, and will end this process
		// as soon as this returns. The station is stopped here, on this
		// thread, so the closing backup is taken before that happens rather
		// than raced against it.
		if wparam != 0 {
			w.stopping = true
			stopOnce.Do(w.hooks.Stop)
		}
		return 0

	case wmDestroy:
		current = nil
		procPostQuitMessage.Call(0)
		return 0
	}
	return defProc(hwnd, msg, wparam, lparam)
}

// The report window: the self-check's text in a box that scrolls and can be
// selected and copied, which is what a person sending it to the developer does.

var report struct {
	hwnd, edit windows.HWND
}

func showReport(w *stationWindow, text string) {
	if report.hwnd != 0 {
		procDestroyWindow.Call(uintptr(report.hwnd))
	}
	instance, _, _ := procGetModuleHandleW.Call(0)
	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsThickFrame | wsMinimizeBox | wsMaximizeBox)
	width, height := w.scale(720), w.scale(520)
	x, y := centred(width, height)

	hwnd, _, _ := procCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(utf16(reportClass))),
		uintptr(unsafe.Pointer(utf16("Bokkie — check"))),
		style, uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		uintptr(w.hwnd), 0, instance, 0)
	if hwnd == 0 {
		ShowMessage("Bokkie — check", text, false)
		return
	}
	report.hwnd = windows.HWND(hwnd)

	edit, _, _ := procCreateWindowExW.Call(wsExClientEdge,
		uintptr(unsafe.Pointer(utf16("EDIT"))),
		uintptr(unsafe.Pointer(utf16(crlf(text)))),
		uintptr(wsChild|wsVisible|wsVScroll|esMultiline|esAutoVScroll|esReadOnly),
		0, 0, uintptr(width), uintptr(height),
		hwnd, 0, instance, 0)
	report.edit = windows.HWND(edit)
	procSendMessageW.Call(edit, wmSetFont, w.fontMono, 1)
	procSendMessageW.Call(edit, emSetSel, 0, 0)
	resizeReport(hwnd)

	procShowWindow.Call(hwnd, swShow)
	procSetForegroundWnd.Call(hwnd)
}

var procGetClientRect = user32.NewProc("GetClientRect")
var procMoveWindow = user32.NewProc("MoveWindow")

func resizeReport(hwnd uintptr) {
	if report.edit == 0 {
		return
	}
	var r rect
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	procMoveWindow.Call(uintptr(report.edit), 0, 0, uintptr(r.Right), uintptr(r.Bottom), 1)
}

func reportProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmSize:
		resizeReport(hwnd)
		return 0
	case wmCtlColorStatic:
		// A read-only edit asks with this message; white keeps it reading as
		// text to copy rather than a greyed-out control.
		procSetBkMode.Call(wparam, transparent)
		brush, _, _ := procGetStockObject.Call(whiteBrush)
		return brush
	case wmDestroy:
		if windows.HWND(hwnd) == report.hwnd {
			report.hwnd, report.edit = 0, 0
		}
		return 0
	}
	return defProc(hwnd, msg, wparam, lparam)
}

// Small helpers.

func defProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func findWindow(class string) windows.HWND {
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(utf16(class))), 0)
	return windows.HWND(hwnd)
}

func setText(hwnd windows.HWND, text string) {
	procSetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(utf16(text))))
}

func enable(hwnd windows.HWND, on bool) {
	var b uintptr
	if on {
		b = 1
	}
	procEnableWindow.Call(uintptr(hwnd), b)
}

func utf16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		// Only a NUL inside s fails, and none of this window's text has one.
		p, _ = syscall.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", ""))
	}
	return p
}

// crlf is for controls that draw a bare \n as nothing at all.
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

func systemDPI() int {
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return 96
	}
	defer procReleaseDC.Call(0, hdc)
	dpi, _, _ := procGetDeviceCaps.Call(hdc, logPixelsY)
	if dpi == 0 {
		return 96
	}
	return int(dpi)
}

func makeFont(face string, points, weight, dpi int) uintptr {
	height := -(points * dpi / 72)
	font, _, _ := procCreateFontW.Call(
		uintptr(int32(height)), 0, 0, 0, uintptr(weight),
		0, 0, 0, 1, // DEFAULT_CHARSET
		0, 0, 5, 0, // CLEARTYPE_QUALITY
		uintptr(unsafe.Pointer(utf16(face))))
	return font
}

func centred(width, height int) (int, int) {
	sw, _, _ := procGetSystemMetrics.Call(smCxScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCyScreen)
	x, y := (int(sw)-width)/2, (int(sh)-height)/3
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

func rgb(r, g, b byte) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }

func toneColour(t Tone) uintptr {
	switch t {
	case ToneGood:
		return rgb(0x1a, 0x7f, 0x37)
	case ToneBad:
		return rgb(0xc6, 0x28, 0x28)
	default:
		return rgb(0x1f, 0x29, 0x37)
	}
}
