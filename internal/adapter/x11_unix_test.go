//go:build linux || freebsd || openbsd || netbsd

package adapter

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/azratul/relevo/internal/protocol"
)

// TestX11Injection needs a DISPOSABLE X server (Xvfb or a nested Xwayland),
// given as RELEVO_TEST_X11_DISPLAY. It plays a minimal EWMH window manager
// itself, so no real WM is needed, and checks that keys arrive only when the
// target window is active. Never point it at your desktop session.
func TestX11Injection(t *testing.T) {
	disp := os.Getenv("RELEVO_TEST_X11_DISPLAY")
	if disp == "" {
		t.Skip("set RELEVO_TEST_X11_DISPLAY to a disposable X server (e.g. Xvfb :99)")
	}
	t.Setenv("DISPLAY", disp)
	c, err := xgb.NewConnDisplay(disp)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	scr := xproto.Setup(c).DefaultScreen(c)
	mk := func(class string) xproto.Window {
		w, _ := xproto.NewWindowId(c)
		xproto.CreateWindow(c, scr.RootDepth, w, scr.Root, 0, 0, 200, 100, 0, xproto.WindowClassInputOutput, scr.RootVisual,
			xproto.CwEventMask, []uint32{xproto.EventMaskKeyPress | xproto.EventMaskKeyRelease})
		v := []byte(class + "\x00" + class + "\x00")
		xproto.ChangeProperty(c, xproto.PropModeReplace, w, xproto.AtomWmClass, xproto.AtomString, 8, uint32(len(v)), v)
		xproto.MapWindow(c, w)
		return w
	}
	target, other := mk("relevo-x11-target"), mk("relevo-x11-other")
	atom := func(n string) xproto.Atom {
		r, _ := xproto.InternAtom(c, false, uint16(len(n)), n).Reply()
		return r.Atom
	}
	activate := func(w xproto.Window) {
		b := make([]byte, 4)
		xgb.Put32(b, uint32(w))
		xproto.ChangeProperty(c, xproto.PropModeReplace, scr.Root, atom("_NET_ACTIVE_WINDOW"), xproto.AtomWindow, 32, 1, b)
		xproto.SetInputFocus(c, xproto.InputFocusPointerRoot, w, xproto.TimeCurrentTime)
		xproto.GetInputFocus(c).Reply()
	}
	list := make([]byte, 8)
	xgb.Put32(list, uint32(target))
	xgb.Put32(list[4:], uint32(other))
	xproto.ChangeProperty(c, xproto.PropModeReplace, scr.Root, atom("_NET_CLIENT_LIST"), xproto.AtomWindow, 32, 2, list)
	activate(target)

	events := make(chan xproto.KeyPressEvent, 32)
	go func() {
		for {
			ev, err := c.WaitForEvent()
			if ev == nil && err == nil {
				return
			}
			if kp, ok := ev.(xproto.KeyPressEvent); ok {
				events <- kp
			}
		}
	}()

	b := &x11{}
	res, _ := Resolve("presentation", nil, nil, nil)
	kb := NewKeyboard(b, res, Target{Class: "relevo-x11-target"})
	ctx := context.Background()
	if rd := kb.Readiness(ctx); !rd.Ready {
		t.Fatalf("not ready: %s", rd.Reason)
	}
	ws, err := b.Windows(ctx)
	if err != nil || len(ws) != 2 {
		t.Fatalf("windows %v %v", ws, err)
	}
	if _, err := kb.Execute(ctx, "presentation.next", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Event != target {
			t.Fatalf("key went to window %x", ev.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no KeyPress received by the target")
	}
	// focus moves away: rejected, and nothing reaches the other window
	activate(other)
	_, err = kb.Execute(ctx, "presentation.next", nil)
	var rej *Rejection
	if !errors.As(err, &rej) || rej.Code != protocol.TargetNotActive {
		t.Fatalf("want TARGET_NOT_ACTIVE, got %v", err)
	}
	select {
	case ev := <-events:
		t.Fatalf("unexpected key to %x", ev.Event)
	case <-time.After(300 * time.Millisecond):
	}
	// modifiers are pressed and released (ctrl+Right)
	activate(target)
	res2, _ := Resolve("presentation", nil, map[string][]string{"presentation.next": {"ctrl+Right"}}, nil)
	kb.Reconfigure(res2, Target{Class: "relevo-x11-target"})
	if _, err := kb.Execute(ctx, "presentation.next", nil); err != nil {
		t.Fatal(err)
	}
	var sawRight bool
	timeout := time.After(2 * time.Second)
	for !sawRight {
		select {
		case ev := <-events:
			if ev.State&xproto.ModMaskControl != 0 {
				sawRight = true
			}
		case <-timeout:
			t.Fatal("no ctrl+key received")
		}
	}
	qk, _ := xproto.QueryKeymap(c).Reply()
	for i, by := range qk.Keys {
		if by != 0 {
			t.Fatalf("key still pressed after chord (byte %d = %08b)", i, by)
		}
	}
}
