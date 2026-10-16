//go:build linux || freebsd || openbsd || netbsd

package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"

	"github.com/azratul/relevo/internal/protocol"
)

// x11 injects with the XTEST extension and checks focus with EWMH
// (_NET_ACTIVE_WINDOW). It does not work for native Wayland windows.
type x11 struct {
	mu   sync.Mutex
	conn *xgb.Conn
	root xproto.Window
	err  error
}

func init() {
	Register(BackendInfo{Name: "x11", Description: "X11 + XTEST, checks focus (_NET_ACTIVE_WINDOW)", Status: "tested"},
		func() (KeyBackend, error) { return &x11{}, nil })
}

func (x *x11) connect() (*xgb.Conn, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.conn != nil {
		return x.conn, nil
	}
	if os.Getenv("DISPLAY") == "" {
		return nil, errors.New("DISPLAY not set")
	}
	c, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	if err := xtest.Init(c); err != nil {
		c.Close()
		return nil, fmt.Errorf("XTEST extension not available: %w", err)
	}
	x.conn, x.root = c, xproto.Setup(c).DefaultScreen(c).Root
	return c, nil
}

func (x *x11) reset() {
	x.mu.Lock()
	if x.conn != nil {
		x.conn.Close()
		x.conn = nil
	}
	x.mu.Unlock()
}

func (x *x11) Name() string     { return "x11" }
func (x *x11) Platform() string { return "unix/x11" }
func (x *x11) FocusCheck() string {
	return protocol.FocusVerified
}
func (x *x11) Confirmation() string  { return protocol.ConfirmInputInjected }
func (x *x11) Permissions() []string { return nil }

func (x *x11) Check(ctx context.Context) error {
	c, err := x.connect()
	if err != nil {
		return &ErrNotReady{Code: "backend_unavailable", Reason: "X11: " + err.Error()}
	}
	if _, err := x.prop(c, x.root, "_NET_ACTIVE_WINDOW"); err != nil {
		x.reset()
		return &ErrNotReady{Code: "no_focus_info", Reason: "The window manager does not publish _NET_ACTIVE_WINDOW (EWMH): focus cannot be checked"}
	}
	return nil
}

func (x *x11) atom(c *xgb.Conn, name string) (xproto.Atom, error) {
	r, err := xproto.InternAtom(c, true, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return r.Atom, nil
}

func (x *x11) prop(c *xgb.Conn, w xproto.Window, name string) (*xproto.GetPropertyReply, error) {
	a, err := x.atom(c, name)
	if err != nil || a == 0 {
		return nil, fmt.Errorf("atom %s missing", name)
	}
	r, err := xproto.GetProperty(c, false, w, a, xproto.GetPropertyTypeAny, 0, 1<<16).Reply()
	if err != nil {
		return nil, err
	}
	if r.Format == 0 {
		return nil, fmt.Errorf("property %s not set", name)
	}
	return r, nil
}

func (x *x11) describe(c *xgb.Conn, w xproto.Window) Window {
	out := Window{ID: fmt.Sprintf("0x%x", uint32(w))}
	if r, err := x.prop(c, w, "WM_CLASS"); err == nil {
		parts := strings.Split(strings.TrimRight(string(r.Value), "\x00"), "\x00")
		out.Class = parts[len(parts)-1]
	}
	if r, err := x.prop(c, w, "_NET_WM_NAME"); err == nil {
		out.Title = string(r.Value)
	} else if r, err := x.prop(c, w, "WM_NAME"); err == nil {
		out.Title = string(r.Value)
	}
	return out
}

func (x *x11) Windows(ctx context.Context) ([]Window, error) {
	c, err := x.connect()
	if err != nil {
		return nil, err
	}
	r, err := x.prop(c, x.root, "_NET_CLIENT_LIST")
	if err != nil {
		return nil, err
	}
	var out []Window
	for i := 0; i+4 <= len(r.Value); i += 4 {
		w := xproto.Window(xgb.Get32(r.Value[i:]))
		out = append(out, x.describe(c, w))
	}
	return out, nil
}

func (x *x11) Active(ctx context.Context) (Window, error) {
	c, err := x.connect()
	if err != nil {
		return Window{}, err
	}
	r, err := x.prop(c, x.root, "_NET_ACTIVE_WINDOW")
	if err != nil || len(r.Value) < 4 {
		return Window{}, errors.New("no active window")
	}
	w := xproto.Window(xgb.Get32(r.Value))
	if w == 0 {
		return Window{}, errors.New("no active window")
	}
	return x.describe(c, w), nil
}

// keycode finds the keycode producing keysym; shift=true if it needs level 2.
func (x *x11) keycode(c *xgb.Conn, sym uint32) (xproto.Keycode, bool, error) {
	setup := xproto.Setup(c)
	min, max := setup.MinKeycode, setup.MaxKeycode
	m, err := xproto.GetKeyboardMapping(c, min, byte(max-min+1)).Reply()
	if err != nil {
		return 0, false, err
	}
	per := int(m.KeysymsPerKeycode)
	for col := 0; col < min2(per, 2); col++ {
		for i := 0; i <= int(max-min); i++ {
			if uint32(m.Keysyms[i*per+col]) == sym {
				return xproto.Keycode(int(min) + i), col == 1, nil
			}
		}
	}
	return 0, false, fmt.Errorf("keysym 0x%x not in the current keyboard map", sym)
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (x *x11) Send(ctx context.Context, _ Window, ch Chord) error {
	c, err := x.connect()
	if err != nil {
		return err
	}
	sym, _ := Keysym(ch.Key)
	kc, needShift, err := x.keycode(c, sym)
	if err != nil {
		return Reject(protocol.HostNotReady, "%v", err)
	}
	var mods []xproto.Keycode
	modNames := ch.Mods
	if needShift && !containsStr(modNames, "shift") {
		modNames = append(append([]string{}, modNames...), "shift")
	}
	for _, m := range modNames {
		mk, _, err := x.keycode(c, ModifierKeysyms[m])
		if err != nil {
			return Reject(protocol.HostNotReady, "modifier %s: %v", m, err)
		}
		mods = append(mods, mk)
	}
	pressed := []xproto.Keycode{}
	fake := func(typ byte, k xproto.Keycode) error {
		return xtest.FakeInputChecked(c, typ, byte(k), 0, x.root, 0, 0, 0).Check()
	}
	// Always release what we pressed, even on error (no stuck modifiers).
	defer func() {
		for i := len(pressed) - 1; i >= 0; i-- {
			_ = fake(xproto.KeyRelease, pressed[i])
		}
		_, _ = xproto.GetInputFocus(c).Reply() // round trip: flush
	}()
	for _, m := range mods {
		if err := fake(xproto.KeyPress, m); err != nil {
			x.reset()
			if len(pressed) == 0 {
				return err
			}
			return fmt.Errorf("%w: %v", ErrUncertain, err)
		}
		pressed = append(pressed, m)
	}
	if err := fake(xproto.KeyPress, kc); err != nil {
		x.reset()
		if len(pressed) == 0 {
			return err
		}
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	pressed = append(pressed, kc)
	if err := fake(xproto.KeyRelease, kc); err != nil {
		x.reset()
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	pressed = pressed[:len(pressed)-1]
	return nil
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
