package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azratul/relevo/internal/protocol"
)

// hyprland is a TARGETED backend: it uses Hyprland's IPC to deliver the key
// to a specific window (send_shortcut / sendshortcut), so the presentation
// does not need focus. Requires a running Hyprland session; no special
// permissions (the IPC socket belongs to the user).
type hyprland struct {
	sock   string
	syntax string // "lua" (0.56+ config API) or "legacy"; detected lazily
}

func init() {
	Register(BackendInfo{Name: "hyprland", Description: "Hyprland IPC: sends keys to the chosen window (no focus needed)", Status: "tested"},
		func() (KeyBackend, error) { return newHyprland() })
}

func hyprSocket() (string, error) {
	sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	if sig == "" {
		return "", errors.New("HYPRLAND_INSTANCE_SIGNATURE not set: not a Hyprland session")
	}
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		rt = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(rt, "hypr", sig, ".socket.sock"), nil
}

func newHyprland() (*hyprland, error) {
	s, err := hyprSocket()
	if err != nil {
		return nil, err
	}
	return &hyprland{sock: s}, nil
}

func (h *hyprland) request(ctx context.Context, cmd string) ([]byte, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", h.sock)
	if err != nil {
		return nil, fmt.Errorf("hyprland IPC: %w", err)
	}
	defer c.Close()
	dl, ok := ctx.Deadline()
	if !ok {
		dl = time.Now().Add(2 * time.Second)
	}
	_ = c.SetDeadline(dl)
	if _, err := c.Write([]byte(cmd)); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(c, 4<<20))
}

func (h *hyprland) Name() string          { return "hyprland" }
func (h *hyprland) Platform() string      { return "linux/wayland-hyprland" }
func (h *hyprland) FocusCheck() string    { return protocol.FocusTargeted }
func (h *hyprland) Confirmation() string  { return protocol.ConfirmInputInjected }
func (h *hyprland) Permissions() []string { return nil }

func (h *hyprland) Check(ctx context.Context) error {
	if _, err := h.request(ctx, "j/version"); err != nil {
		return &ErrNotReady{Code: "backend_unavailable", Reason: "Cannot talk to Hyprland over IPC: " + err.Error()}
	}
	return nil
}

type hyprClient struct {
	Address string `json:"address"`
	Class   string `json:"class"`
	Title   string `json:"title"`
	Mapped  bool   `json:"mapped"`
	Hidden  bool   `json:"hidden"`
}

func (h *hyprland) Windows(ctx context.Context) ([]Window, error) {
	b, err := h.request(ctx, "j/clients")
	if err != nil {
		return nil, err
	}
	var cs []hyprClient
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("hyprland clients: %w", err)
	}
	var out []Window
	for _, c := range cs {
		if c.Mapped && c.Address != "" {
			out = append(out, Window{ID: c.Address, Class: c.Class, Title: c.Title})
		}
	}
	return out, nil
}

func (h *hyprland) Active(ctx context.Context) (Window, error) {
	b, err := h.request(ctx, "j/activewindow")
	if err != nil {
		return Window{}, err
	}
	var c hyprClient
	if err := json.Unmarshal(b, &c); err != nil || c.Address == "" {
		return Window{}, errors.New("no active window")
	}
	return Window{ID: c.Address, Class: c.Class, Title: c.Title}, nil
}

var hyprMods = map[string]string{"ctrl": "CTRL", "alt": "ALT", "shift": "SHIFT", "super": "SUPER"}

// luaQuote quotes s as a Lua string literal. Inputs are whitelisted key
// names and Hyprland window addresses, but we still escape defensively.
func luaQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

func validAddress(a string) bool {
	if !strings.HasPrefix(a, "0x") || len(a) > 20 {
		return false
	}
	for _, c := range a[2:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func (h *hyprland) Send(ctx context.Context, w Window, c Chord) error {
	if !validAddress(w.ID) {
		return Reject(protocol.TargetNotActive, "invalid window address")
	}
	var mods []string
	for _, m := range c.Mods {
		mods = append(mods, hyprMods[m])
	}
	lua := fmt.Sprintf("dispatch hl.dsp.send_shortcut({ mods = %s, key = %s, window = %s })",
		luaQuote(strings.Join(mods, " ")), luaQuote(c.Key), luaQuote("address:"+w.ID))
	legacy := fmt.Sprintf("dispatch sendshortcut %s, %s, address:%s", strings.Join(mods, " "), c.Key, w.ID)
	try := func(cmd string) (string, error) {
		b, err := h.request(ctx, cmd)
		return strings.TrimSpace(string(b)), err
	}
	if h.syntax != "legacy" {
		out, err := try(lua)
		if err != nil {
			return err
		}
		if out == "ok" {
			h.syntax = "lua"
			return nil
		}
		if h.syntax == "lua" {
			return fmt.Errorf("hyprland: %s", out)
		}
	}
	out, err := try(legacy)
	if err != nil {
		return err
	}
	if out != "ok" {
		return fmt.Errorf("hyprland: %s", out)
	}
	h.syntax = "legacy"
	return nil
}
