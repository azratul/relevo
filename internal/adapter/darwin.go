//go:build darwin

package adapter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/azratul/relevo/internal/protocol"
)

// mac injects with CGEventPost (requires the Accessibility permission) and
// verifies focus with NSWorkspace.frontmostApplication. Uses purego, so it
// cross-compiles without cgo. Key codes are ANSI positions (kVK_*): navigation
// keys are layout independent, letters/digits follow the physical ANSI layout.
type mac struct {
	once sync.Once
	err  error

	cgEventCreateKeyboardEvent func(src uintptr, key uint16, down bool) uintptr
	cgEventSetFlags            func(ev uintptr, flags uint64)
	cgEventPost                func(tap uint32, ev uintptr)
	cfRelease                  func(uintptr)
	axIsProcessTrusted         func() bool
}

func init() {
	Register(BackendInfo{Name: "darwin", Description: "macOS CGEvent, checks the frontmost app", Status: "compiles"},
		func() (KeyBackend, error) { return &mac{}, nil })
}

func (m *mac) load() error {
	m.once.Do(func() {
		as, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			m.err = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			m.err = err
			return
		}
		if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			m.err = err
			return
		}
		purego.RegisterLibFunc(&m.cgEventCreateKeyboardEvent, as, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&m.cgEventSetFlags, as, "CGEventSetFlags")
		purego.RegisterLibFunc(&m.cgEventPost, as, "CGEventPost")
		purego.RegisterLibFunc(&m.axIsProcessTrusted, as, "AXIsProcessTrusted")
		purego.RegisterLibFunc(&m.cfRelease, cf, "CFRelease")
	})
	return m.err
}

func (m *mac) Name() string         { return "darwin" }
func (m *mac) Platform() string     { return "darwin" }
func (m *mac) FocusCheck() string   { return protocol.FocusVerified }
func (m *mac) Confirmation() string { return protocol.ConfirmInputInjected }
func (m *mac) Permissions() []string {
	return []string{"Accessibility (System Settings → Privacy & Security → Accessibility)"}
}

func (m *mac) Check(context.Context) error {
	if err := m.load(); err != nil {
		return &ErrNotReady{Code: "backend_unavailable", Reason: "Cannot load the macOS frameworks: " + err.Error()}
	}
	if !m.axIsProcessTrusted() {
		return &ErrNotReady{Code: "accessibility_permission", Reason: "relevo-host needs the Accessibility permission (System Settings → Privacy & Security → Accessibility)"}
	}
	return nil
}

var kvk = map[string]uint16{
	"Left": 0x7B, "Right": 0x7C, "Down": 0x7D, "Up": 0x7E, "Page_Up": 0x74, "Page_Down": 0x79, "Home": 0x73, "End": 0x77,
	"space": 0x31, "Return": 0x24, "Escape": 0x35, "BackSpace": 0x33, "Tab": 0x30, "period": 0x2F,
	"F1": 0x7A, "F2": 0x78, "F3": 0x63, "F4": 0x76, "F5": 0x60, "F6": 0x61, "F7": 0x62, "F8": 0x64, "F9": 0x65, "F10": 0x6D, "F11": 0x67, "F12": 0x6F,
	"a": 0x00, "s": 0x01, "d": 0x02, "f": 0x03, "h": 0x04, "g": 0x05, "z": 0x06, "x": 0x07, "c": 0x08, "v": 0x09, "b": 0x0B, "q": 0x0C,
	"w": 0x0D, "e": 0x0E, "r": 0x0F, "y": 0x10, "t": 0x11, "o": 0x1F, "u": 0x20, "i": 0x22, "p": 0x23, "l": 0x25, "j": 0x26, "k": 0x28,
	"n": 0x2D, "m": 0x2E, "1": 0x12, "2": 0x13, "3": 0x14, "4": 0x15, "6": 0x16, "5": 0x17, "9": 0x19, "7": 0x1A, "8": 0x1C, "0": 0x1D,
}

var macMods = map[string]struct {
	code uint16
	flag uint64
}{"shift": {0x38, 0x20000}, "ctrl": {0x3B, 0x40000}, "alt": {0x3A, 0x80000}, "super": {0x37, 0x100000}}

const kCGHIDEventTap = 0

func (m *mac) post(code uint16, down bool, flags uint64) error {
	ev := m.cgEventCreateKeyboardEvent(0, code, down)
	if ev == 0 {
		return errors.New("CGEventCreateKeyboardEvent failed")
	}
	m.cgEventSetFlags(ev, flags)
	m.cgEventPost(kCGHIDEventTap, ev)
	m.cfRelease(ev)
	return nil
}

func (m *mac) Send(ctx context.Context, _ Window, c Chord) error {
	if err := m.load(); err != nil {
		return err
	}
	code, ok := kvk[c.Key]
	if !ok {
		return Reject(protocol.HostNotReady, "key %s not mapped on macOS", c.Key)
	}
	var flags uint64
	var pressed []uint16
	defer func() {
		for i := len(pressed) - 1; i >= 0; i-- {
			_ = m.post(pressed[i], false, 0)
		}
	}()
	for _, mod := range c.Mods {
		mm := macMods[mod]
		flags |= mm.flag
		if err := m.post(mm.code, true, flags); err != nil {
			return err
		}
		pressed = append(pressed, mm.code)
	}
	if err := m.post(code, true, flags); err != nil {
		return err
	}
	if err := m.post(code, false, flags); err != nil {
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	return nil
}

func nsString(id objc.ID) string {
	if id == 0 {
		return ""
	}
	p := objc.Send[*byte](id, objc.RegisterName("UTF8String"))
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 && n < 4096 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

func withPool(f func()) {
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	defer pool.Send(objc.RegisterName("drain"))
	f()
}

func appInfo(app objc.ID) Window {
	return Window{
		ID:    fmt.Sprint(objc.Send[int32](app, objc.RegisterName("processIdentifier"))),
		Class: nsString(app.Send(objc.RegisterName("bundleIdentifier"))),
		Title: nsString(app.Send(objc.RegisterName("localizedName"))),
	}
}

func (m *mac) Active(context.Context) (Window, error) {
	if err := m.load(); err != nil {
		return Window{}, err
	}
	var w Window
	withPool(func() {
		ws := objc.ID(objc.GetClass("NSWorkspace")).Send(objc.RegisterName("sharedWorkspace"))
		app := ws.Send(objc.RegisterName("frontmostApplication"))
		if app != 0 {
			w = appInfo(app)
		}
	})
	if w.Class == "" {
		return w, errors.New("no frontmost application")
	}
	return w, nil
}

func (m *mac) Windows(context.Context) ([]Window, error) {
	if err := m.load(); err != nil {
		return nil, err
	}
	var out []Window
	withPool(func() {
		ws := objc.ID(objc.GetClass("NSWorkspace")).Send(objc.RegisterName("sharedWorkspace"))
		apps := ws.Send(objc.RegisterName("runningApplications"))
		n := objc.Send[uint](apps, objc.RegisterName("count"))
		for i := uint(0); i < n; i++ {
			app := apps.Send(objc.RegisterName("objectAtIndex:"), i)
			if objc.Send[int](app, objc.RegisterName("activationPolicy")) == 0 { // regular apps
				out = append(out, appInfo(app))
			}
		}
	})
	return out, nil
}
