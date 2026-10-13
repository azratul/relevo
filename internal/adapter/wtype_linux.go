package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"relevo/internal/protocol"
)

// wtype injects through the wlroots virtual-keyboard protocol (wlroots
// compositors, Hyprland; NOT GNOME/KDE). It has no focus information of its
// own; on Hyprland we verify focus through IPC, elsewhere it is unverified.
type wtype struct {
	bin  string
	hypr *hyprland // nil outside Hyprland
}

func init() {
	Register(BackendInfo{Name: "wtype", Description: "wtype (wlroots virtual keyboard): active window; focus checked only on Hyprland", Status: "tested"},
		func() (KeyBackend, error) {
			bin, err := exec.LookPath("wtype")
			if err != nil {
				return nil, errors.New("wtype not installed")
			}
			w := &wtype{bin: bin}
			if h, err := newHyprland(); err == nil {
				w.hypr = h
			}
			return w, nil
		})
}

func (w *wtype) Name() string     { return "wtype" }
func (w *wtype) Platform() string { return "linux/wayland-wlroots" }
func (w *wtype) FocusCheck() string {
	if w.hypr != nil {
		return protocol.FocusVerified
	}
	return protocol.FocusUnverified
}
func (w *wtype) Confirmation() string  { return protocol.ConfirmInputInjected }
func (w *wtype) Permissions() []string { return nil }

func (w *wtype) Check(ctx context.Context) error {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return &ErrNotReady{Code: "backend_unavailable", Reason: "WAYLAND_DISPLAY is not set: wtype needs a Wayland session"}
	}
	if w.hypr != nil {
		return w.hypr.Check(ctx)
	}
	return nil
}

func (w *wtype) Windows(ctx context.Context) ([]Window, error) {
	if w.hypr != nil {
		return w.hypr.Windows(ctx)
	}
	return nil, errors.New("this compositor offers no window list; enable unverified active-window mode")
}

func (w *wtype) Active(ctx context.Context) (Window, error) {
	if w.hypr != nil {
		return w.hypr.Active(ctx)
	}
	return Window{}, errors.New("focus cannot be checked on this compositor")
}

func (w *wtype) Send(ctx context.Context, _ Window, c Chord) error {
	var args []string
	for _, m := range c.Mods {
		args = append(args, "-M", m)
	}
	args = append(args, "-k", c.Key)
	for i := len(c.Mods) - 1; i >= 0; i-- {
		args = append(args, "-m", c.Mods[i])
	}
	cmd := exec.CommandContext(ctx, w.bin, args...)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// The process ran: some events may have been sent.
			return fmt.Errorf("%w: wtype: %s", ErrUncertain, string(out))
		}
		return fmt.Errorf("wtype: %w", err)
	}
	return nil
}
