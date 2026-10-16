//go:build darwin

// Package macui is the station window on macOS: the Mac counterpart of the
// Win32 window in internal/winsetup, drawing the same StationView.
//
// It is AppKit called through purego's Objective-C runtime bindings, so it
// needs no cgo — CGO_ENABLED=0 is a project decision (see the Makefile), and
// cgo would end cross-compiling the other four binaries from one machine.
//
// AppKit may only be touched from the main thread. Everything that happens
// elsewhere — the station stopping, a check finishing, a message to show — is
// left for a timer on the main run loop to pick up, a quarter-second later.
package macui

import (
	"fmt"
	"os"
	"os/exec"
	"structs"
	"sync"
	"sync/atomic"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

type nsPoint struct {
	_    structs.HostLayout
	X, Y float64
}

type nsSize struct {
	_             structs.HostLayout
	Width, Height float64
}

type nsRect struct {
	_      structs.HostLayout
	Origin nsPoint
	Size   nsSize
}

func rect(x, y, w, h float64) nsRect {
	return nsRect{Origin: nsPoint{X: x, Y: y}, Size: nsSize{Width: w, Height: h}}
}

const (
	styleTitled         = 1 << 0
	styleClosable       = 1 << 1
	styleMiniaturizable = 1 << 2
	styleResizable      = 1 << 3
	backingBuffered     = 2
	activationRegular   = 0
	truncatingTail      = 4

	terminateCancel = 0
	terminateNow    = 1
	terminateLater  = 2

	// The window's size, in points. AppKit counts y from the bottom; place
	// turns a distance from the top into that.
	winWidth  = 520
	winHeight = 330
	margin    = 20
	labelW    = 130
	rowH      = 22
	rowsTop   = 128
	rowCount  = 6
)

var sel = objc.RegisterName

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func nsString(s string) objc.ID {
	return class("NSString").Send(sel("stringWithUTF8String:"), s)
}

// window is the one station window of this process.
type window struct {
	hooks winsetup.StationHooks

	app, win, delegate objc.ID
	headline, detail   objc.ID
	labels, values     [rowCount]objc.ID
	open, check, stop  objc.ID
	reportWin, report  objc.ID

	last     winsetup.StationView
	stopping bool
	checking bool
	// pendingQuit is a Cmd-Q or a logout that was told to wait until the
	// station has stopped.
	pendingQuit bool
	inModal     bool
	ticks       int

	stopped    atomic.Bool
	stopAsked  atomic.Bool
	reportDone atomic.Bool

	mu         sync.Mutex
	reportText string
	messages   []message
}

type message struct {
	title, text string
	isError     bool
}

var (
	current  *window
	stopOnce sync.Once
)

// OpenInBrowser opens address in the person's own browser.
func OpenInBrowser(address string) error { return exec.Command("open", address).Run() }

// ShowMessage queues an alert for the main thread. It may be called from any
// goroutine; before the window exists it does nothing.
func ShowMessage(title, text string, isError bool) {
	if w := current; w != nil {
		w.mu.Lock()
		w.messages = append(w.messages, message{title, text, isError})
		w.mu.Unlock()
	}
}

// Run shows the station window and returns once it has closed — which is only
// ever after hooks.Stop has returned. It must be called on the main thread:
// the caller locks the main goroutine to it before main runs.
func Run(hooks winsetup.StationHooks) error {
	if _, err := purego.Dlopen("/System/Library/Frameworks/Cocoa.framework/Cocoa",
		purego.RTLD_GLOBAL|purego.RTLD_LAZY); err != nil {
		return fmt.Errorf("load Cocoa: %w", err)
	}
	w := &window{hooks: hooks}
	current = w

	delegateClass, err := registerDelegate()
	if err != nil {
		return err
	}
	w.delegate = objc.ID(delegateClass).Send(sel("alloc")).Send(sel("init"))

	w.app = class("NSApplication").Send(sel("sharedApplication"))
	w.app.Send(sel("setActivationPolicy:"), activationRegular)
	w.app.Send(sel("setDelegate:"), w.delegate)
	w.buildMenu()
	w.build()
	w.refresh()

	if hooks.StopWanted != nil {
		go func() {
			<-hooks.StopWanted
			w.stopAsked.Store(true)
		}()
	}

	// Common modes, not the default one: while a quit is waiting for the
	// station to stop, the run loop is in a modal mode, and a timer only in
	// the default mode would never fire to finish it.
	timer := class("NSTimer").Send(sel("timerWithTimeInterval:target:selector:userInfo:repeats:"),
		0.25, w.delegate, sel("tick:"), objc.ID(0), true)
	class("NSRunLoop").Send(sel("mainRunLoop")).Send(sel("addTimer:forMode:"), timer, nsString("kCFRunLoopCommonModes"))

	w.win.Send(sel("center"))
	w.win.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
	w.app.Send(sel("activateIgnoringOtherApps:"), true)
	w.app.Send(sel("run"))
	return nil
}

func registerDelegate() (objc.Class, error) {
	if c := objc.GetClass("BokkieStationDelegate"); c != 0 {
		return c, nil
	}
	return objc.RegisterClass("BokkieStationDelegate", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: sel("tick:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) { current.tick() }},
		{Cmd: sel("openClicked:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) { go current.hooks.Open() }},
		{Cmd: sel("checkClicked:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) { current.beginCheck() }},
		{Cmd: sel("stopClicked:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) { current.beginStop() }},
		{Cmd: sel("windowShouldClose:"), Fn: func(self objc.ID, _ objc.SEL, sender objc.ID) bool {
			if sender == current.win {
				current.beginStop()
				return false
			}
			return true // the check report closes like any other window
		}},
		{Cmd: sel("applicationShouldTerminateAfterLastWindowClosed:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) bool {
			return false
		}},
		{Cmd: sel("applicationShouldHandleReopen:hasVisibleWindows:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID, _ bool) bool {
			current.win.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
			return true
		}},
		// Cmd-Q, the Dock's Quit, and logging out all arrive here. Each has
		// to wait for the closing backup, and "later" rather than "cancel" is
		// what keeps a logout from being refused outright.
		{Cmd: sel("applicationShouldTerminate:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) uint {
			if current.stopped.Load() {
				return terminateNow
			}
			current.pendingQuit = true
			current.beginStop()
			return terminateLater
		}},
	})
}

// place turns a frame measured from the top-left into AppKit's bottom-left.
func place(x, top, w, h float64) nsRect { return rect(x, winHeight-top-h, w, h) }

func (w *window) build() {
	w.win = class("NSWindow").Send(sel("alloc")).Send(sel("initWithContentRect:styleMask:backing:defer:"),
		rect(0, 0, winWidth, winHeight), styleTitled|styleClosable|styleMiniaturizable, backingBuffered, false)
	w.win.Send(sel("setTitle:"), nsString(w.hooks.Title))
	w.win.Send(sel("setReleasedWhenClosed:"), false)
	w.win.Send(sel("setDelegate:"), w.delegate)
	content := w.win.Send(sel("contentView"))

	inner := float64(winWidth - 2*margin)
	w.headline = label(content, place(margin, 16, inner, 30), boldFont(20), false)
	w.detail = class("NSTextField").Send(sel("wrappingLabelWithString:"), nsString(""))
	w.detail.Send(sel("setFrame:"), place(margin, 52, inner, 68))
	w.detail.Send(sel("setFont:"), font(13))
	content.Send(sel("addSubview:"), w.detail)

	for i := range rowCount {
		top := float64(rowsTop + i*rowH)
		w.labels[i] = label(content, place(margin, top, labelW, rowH), font(13), false)
		w.labels[i].Send(sel("setTextColor:"), class("NSColor").Send(sel("secondaryLabelColor")))
		// Selectable, so an address can be copied into a message.
		w.values[i] = label(content, place(margin+labelW, top, inner-labelW, rowH), font(13), true)
	}

	top := float64(rowsTop + rowCount*rowH + 14)
	w.open = button(content, "Open Bokkie in browser", "openClicked:", w.delegate, place(margin-6, top, 200, 32))
	w.open.Send(sel("setKeyEquivalent:"), nsString("\r"))
	w.check = button(content, "Run a check", "checkClicked:", w.delegate, place(margin+200, top, 130, 32))
	w.stop = button(content, "Stop Bokkie", "stopClicked:", w.delegate, place(winWidth-margin-124, top, 130, 32))
}

func label(parent objc.ID, frame nsRect, f objc.ID, selectable bool) objc.ID {
	l := class("NSTextField").Send(sel("labelWithString:"), nsString(""))
	l.Send(sel("setFrame:"), frame)
	l.Send(sel("setFont:"), f)
	l.Send(sel("setLineBreakMode:"), truncatingTail)
	l.Send(sel("setSelectable:"), selectable)
	parent.Send(sel("addSubview:"), l)
	return l
}

func button(parent objc.ID, title, action string, target objc.ID, frame nsRect) objc.ID {
	b := class("NSButton").Send(sel("buttonWithTitle:target:action:"), nsString(title), target, sel(action))
	b.Send(sel("setFrame:"), frame)
	parent.Send(sel("addSubview:"), b)
	return b
}

func font(size float64) objc.ID { return class("NSFont").Send(sel("systemFontOfSize:"), size) }

func boldFont(size float64) objc.ID {
	return class("NSFont").Send(sel("boldSystemFontOfSize:"), size)
}

// buildMenu gives the app the menus every Mac app has: Quit, Close, and the
// Edit commands that make copying an address or the check report work.
func (w *window) buildMenu() {
	bar := class("NSMenu").Send(sel("alloc")).Send(sel("init"))

	appMenu := class("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsString("Bokkie"))
	addItem(appMenu, "Quit Bokkie", "terminate:", "q")
	attach(bar, appMenu)

	edit := class("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsString("Edit"))
	addItem(edit, "Copy", "copy:", "c")
	addItem(edit, "Select All", "selectAll:", "a")
	attach(bar, edit)

	win := class("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsString("Window"))
	addItem(win, "Close", "performClose:", "w")
	addItem(win, "Minimize", "performMiniaturize:", "m")
	attach(bar, win)

	w.app.Send(sel("setMainMenu:"), bar)
}

func addItem(menu objc.ID, title, action, key string) {
	item := class("NSMenuItem").Send(sel("alloc")).Send(sel("initWithTitle:action:keyEquivalent:"),
		nsString(title), sel(action), nsString(key))
	menu.Send(sel("addItem:"), item)
}

func attach(bar, menu objc.ID) {
	item := class("NSMenuItem").Send(sel("alloc")).Send(sel("init"))
	item.Send(sel("setSubmenu:"), menu)
	bar.Send(sel("addItem:"), item)
}

// tick is the main thread's one look at everything that happened elsewhere.
func (w *window) tick() {
	if w.inModal {
		return
	}
	if w.stopAsked.Load() {
		w.stopAsked.Store(false)
		w.beginStop()
	}
	if w.reportDone.Load() {
		w.reportDone.Store(false)
		w.checking = false
		w.check.Send(sel("setTitle:"), nsString("Run a check"))
		w.mu.Lock()
		text := w.reportText
		w.mu.Unlock()
		w.showReport(text)
	}
	if w.stopped.Load() {
		if w.pendingQuit {
			w.pendingQuit = false
			w.app.Send(sel("replyToApplicationShouldTerminate:"), true)
		} else {
			w.app.Send(sel("terminate:"), objc.ID(0))
		}
		return
	}
	w.refresh()
	w.ticks++
	if path := os.Getenv("BOKKIE_WINDOW_SNAPSHOT"); path != "" && w.ticks == 12 {
		w.snapshot(path)
	}

	w.mu.Lock()
	pending := w.messages
	w.messages = nil
	w.mu.Unlock()
	for _, m := range pending {
		w.alert(m)
	}
}

func (w *window) refresh() {
	v := w.hooks.Snapshot()
	if w.stopping {
		v = winsetup.DescribeStation(winsetup.StationFacts{State: winsetup.StationStopping})
	}

	if v.Headline != w.last.Headline || v.Tone != w.last.Tone {
		w.headline.Send(sel("setStringValue:"), nsString(v.Headline))
		w.headline.Send(sel("setTextColor:"), toneColour(v.Tone))
	}
	if v.Detail != w.last.Detail {
		w.detail.Send(sel("setStringValue:"), nsString(v.Detail))
	}
	for i := range rowCount {
		var row, was winsetup.Row
		if i < len(v.Rows) {
			row = v.Rows[i]
		}
		if i < len(w.last.Rows) {
			was = w.last.Rows[i]
		}
		if row != was {
			w.labels[i].Send(sel("setStringValue:"), nsString(row.Label))
			w.values[i].Send(sel("setStringValue:"), nsString(row.Value))
		}
	}
	w.open.Send(sel("setEnabled:"), v.CanOpen && !v.Busy)
	w.check.Send(sel("setEnabled:"), !v.Busy && !w.checking)
	w.stop.Send(sel("setEnabled:"), !v.Busy)
	w.last = v
}

func toneColour(t winsetup.Tone) objc.ID {
	colours := class("NSColor")
	switch t {
	case winsetup.ToneGood:
		return colours.Send(sel("systemGreenColor"))
	case winsetup.ToneBad:
		return colours.Send(sel("systemRedColor"))
	default:
		return colours.Send(sel("labelColor"))
	}
}

func (w *window) beginStop() {
	if w.stopping {
		return
	}
	w.stopping = true
	w.refresh()
	go func() {
		stopOnce.Do(w.hooks.Stop)
		w.stopped.Store(true)
	}()
}

func (w *window) beginCheck() {
	if w.checking {
		return
	}
	w.checking = true
	w.check.Send(sel("setTitle:"), nsString("Checking…"))
	w.refresh()
	go func() {
		text := w.hooks.Check()
		w.mu.Lock()
		w.reportText = text
		w.mu.Unlock()
		w.reportDone.Store(true)
	}()
}

// showReport puts the self-check in a window of its own, in a fixed-width font
// that keeps its columns, where it can be selected and copied.
func (w *window) showReport(text string) {
	if w.reportWin == 0 {
		w.reportWin = class("NSWindow").Send(sel("alloc")).Send(sel("initWithContentRect:styleMask:backing:defer:"),
			rect(0, 0, 720, 480), styleTitled|styleClosable|styleMiniaturizable|styleResizable, backingBuffered, false)
		w.reportWin.Send(sel("setTitle:"), nsString("Bokkie — check"))
		w.reportWin.Send(sel("setReleasedWhenClosed:"), false)
		scroll := class("NSTextView").Send(sel("scrollableTextView"))
		w.report = scroll.Send(sel("documentView"))
		w.report.Send(sel("setEditable:"), false)
		w.report.Send(sel("setFont:"), class("NSFont").Send(sel("monospacedSystemFontOfSize:weight:"), 12.0, 0.0))
		w.reportWin.Send(sel("setContentView:"), scroll)
		w.reportWin.Send(sel("center"))
	}
	w.report.Send(sel("setString:"), nsString(text))
	w.reportWin.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
}

func (w *window) alert(m message) {
	a := class("NSAlert").Send(sel("alloc")).Send(sel("init"))
	a.Send(sel("setMessageText:"), nsString(m.title))
	a.Send(sel("setInformativeText:"), nsString(m.text))
	if m.isError {
		a.Send(sel("setAlertStyle:"), 2) // critical
	}
	w.inModal = true
	a.Send(sel("runModal"))
	w.inModal = false
}

// snapshot writes the window's contents to a PNG. It exists so the window can
// be looked at by a developer, or a check, on a machine that will not let a
// terminal capture the screen; BOKKIE_WINDOW_SNAPSHOT names the file, and
// nothing else ever sets it.
func (w *window) snapshot(path string) {
	view := w.win.Send(sel("contentView"))
	bounds := rect(0, 0, winWidth, winHeight)
	rep := view.Send(sel("bitmapImageRepForCachingDisplayInRect:"), bounds)
	view.Send(sel("cacheDisplayInRect:toBitmapImageRep:"), bounds, rep)
	png := rep.Send(sel("representationUsingType:properties:"), 4, class("NSDictionary").Send(sel("dictionary")))
	png.Send(sel("writeToFile:atomically:"), nsString(path), true)
}
