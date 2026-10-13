package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"relevo/internal/protocol"
	"relevo/internal/secret"
)

// portal uses org.freedesktop.portal.RemoteDesktop (GNOME, KDE and other
// compositors whose portal backend implements it). The user grants consent in
// a system dialog. The portal exposes no focus information, so this backend
// is "unverified" and needs the explicit active-window opt-in (ADR-0007).
// Reference: https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.RemoteDesktop.html
type portal struct {
	mu      sync.Mutex
	conn    *dbus.Conn
	session dbus.ObjectPath
	denied  string
}

const (
	portalDest = "org.freedesktop.portal.Desktop"
	portalPath = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	rdIface    = "org.freedesktop.portal.RemoteDesktop"
)

func init() {
	Register(BackendInfo{Name: "portal", Description: "XDG RemoteDesktop portal (GNOME/KDE); no focus check", Status: "compiles"},
		func() (KeyBackend, error) { return &portal{}, nil })
}

func (p *portal) Name() string         { return "portal" }
func (p *portal) Platform() string     { return "linux/wayland-portal" }
func (p *portal) FocusCheck() string   { return protocol.FocusUnverified }
func (p *portal) Confirmation() string { return protocol.ConfirmInputInjected }
func (p *portal) Permissions() []string {
	return []string{"Consent to \"remote control\" in the portal dialog"}
}

func tokenFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "relevo", "portal-restore-token")
}

// request calls a portal method that answers through a Request object.
func (p *portal) request(ctx context.Context, method string, args ...any) (map[string]dbus.Variant, error) {
	token := "relevo_" + strings.ReplaceAll(secret.UUID()[:8], "-", "")
	sender := strings.ReplaceAll(strings.TrimPrefix(p.conn.Names()[0], ":"), ".", "_")
	reqPath := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/portal/desktop/request/%s/%s", sender, token))
	if err := p.conn.AddMatchSignal(dbus.WithMatchObjectPath(reqPath), dbus.WithMatchInterface("org.freedesktop.portal.Request"), dbus.WithMatchMember("Response")); err != nil {
		return nil, err
	}
	defer p.conn.RemoveMatchSignal(dbus.WithMatchObjectPath(reqPath), dbus.WithMatchInterface("org.freedesktop.portal.Request"), dbus.WithMatchMember("Response"))
	ch := make(chan *dbus.Signal, 4)
	p.conn.Signal(ch)
	defer p.conn.RemoveSignal(ch)
	opts := args[len(args)-1].(map[string]dbus.Variant)
	opts["handle_token"] = dbus.MakeVariant(token)
	if err := p.conn.Object(portalDest, portalPath).CallWithContext(ctx, rdIface+"."+method, 0, args...).Err; err != nil {
		return nil, err
	}
	for {
		select {
		case s := <-ch:
			if s.Path != reqPath || len(s.Body) < 2 {
				continue
			}
			code, _ := s.Body[0].(uint32)
			results, _ := s.Body[1].(map[string]dbus.Variant)
			if code != 0 {
				return nil, fmt.Errorf("portal %s: user cancelled or denied (code %d)", method, code)
			}
			return results, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (p *portal) start(ctx context.Context) error {
	if p.session != "" {
		return nil
	}
	if p.denied != "" {
		return errors.New(p.denied)
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	p.conn = conn
	res, err := p.request(ctx, "CreateSession", map[string]dbus.Variant{"session_handle_token": dbus.MakeVariant("relevo")})
	if err != nil {
		return err
	}
	sh, _ := res["session_handle"].Value().(string)
	session := dbus.ObjectPath(sh)
	sel := map[string]dbus.Variant{"types": dbus.MakeVariant(uint32(1)), "persist_mode": dbus.MakeVariant(uint32(2))}
	if f := tokenFile(); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			sel["restore_token"] = dbus.MakeVariant(strings.TrimSpace(string(b)))
		}
	}
	if _, err := p.request(ctx, "SelectDevices", session, sel); err != nil {
		return err
	}
	res, err = p.request(ctx, "Start", session, "", map[string]dbus.Variant{})
	if err != nil {
		p.denied = "Remote control was denied in the portal; restart relevo-host to ask again"
		return err
	}
	if tok, ok := res["restore_token"].Value().(string); ok && tok != "" {
		if f := tokenFile(); f != "" {
			_ = os.MkdirAll(filepath.Dir(f), 0o700)
			_ = os.WriteFile(f, []byte(tok), 0o600)
		}
	}
	p.session = session
	return nil
}

func (p *portal) Check(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// The first check opens the consent dialog; allow time for the user.
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := p.start(cctx); err != nil {
		return &ErrNotReady{Code: "backend_unavailable", Reason: "RemoteDesktop portal: " + err.Error()}
	}
	return nil
}

func (p *portal) Windows(context.Context) ([]Window, error) {
	return nil, errors.New("the portal cannot list windows; enable unverified active-window mode")
}

func (p *portal) Active(context.Context) (Window, error) {
	return Window{}, errors.New("the portal cannot report the focused window")
}

func (p *portal) key(ctx context.Context, sym uint32, down bool) error {
	state := uint32(0)
	if down {
		state = 1
	}
	return p.conn.Object(portalDest, portalPath).CallWithContext(ctx, rdIface+".NotifyKeyboardKeysym", 0,
		p.session, map[string]dbus.Variant{}, int32(sym), state).Err
}

func (p *portal) Send(ctx context.Context, _ Window, c Chord) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session == "" {
		return Reject(protocol.HostNotReady, "portal session not started")
	}
	var pressed []uint32
	defer func() {
		for i := len(pressed) - 1; i >= 0; i-- {
			_ = p.key(context.Background(), pressed[i], false)
		}
	}()
	press := func(sym uint32) error {
		if err := p.key(ctx, sym, true); err != nil {
			if len(pressed) == 0 {
				return err
			}
			return fmt.Errorf("%w: %v", ErrUncertain, err)
		}
		pressed = append(pressed, sym)
		return nil
	}
	for _, m := range c.Mods {
		if err := press(ModifierKeysyms[m]); err != nil {
			return err
		}
	}
	sym, _ := Keysym(c.Key)
	if err := press(sym); err != nil {
		return err
	}
	pressed = pressed[:len(pressed)-1]
	if err := p.key(ctx, sym, false); err != nil {
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	return nil
}
