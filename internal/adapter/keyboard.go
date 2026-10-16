package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/azratul/relevo/internal/protocol"
)

// Window identifies a top-level window or application.
type Window struct {
	ID    string `json:"id"`
	Class string `json:"class"` // X11 WM_CLASS, Wayland app_id, Windows exe name, macOS bundle id
	Title string `json:"title"`
}

// ErrNotReady is returned by KeyBackend.Check with a user-facing reason.
type ErrNotReady struct {
	Code   string // stable, for translation: backend_unavailable, accessibility_permission, no_focus_info, …
	Reason string // English detail
}

func (e *ErrNotReady) Error() string { return e.Reason }

// KeyBackend injects key chords on one platform/backend.
type KeyBackend interface {
	Name() string
	Platform() string // e.g. linux/x11
	// FocusCheck: protocol.FocusTargeted (Send addresses the window),
	// FocusVerified (Active works), FocusUnverified (neither), FocusSimulated.
	FocusCheck() string
	Confirmation() string
	Permissions() []string
	Check(ctx context.Context) error
	Windows(ctx context.Context) ([]Window, error)
	Active(ctx context.Context) (Window, error)
	// Send presses and releases chord. Targeted backends deliver to w;
	// others deliver to the focused window. Implementations must release any
	// pressed modifier on error.
	Send(ctx context.Context, w Window, c Chord) error
}

// Target selects the window that may receive input.
type Target struct {
	Class         string `json:"class,omitempty"`         // exact, case-insensitive
	TitleContains string `json:"titleContains,omitempty"` // case-insensitive substring
	// AllowUnverifiedActiveWindow lets backends that cannot check focus send
	// to whatever window is active. Explicit opt-in (ADR-0007).
	AllowUnverifiedActiveWindow bool `json:"allowUnverifiedActiveWindow,omitempty"`
}

func (t Target) Empty() bool { return t.Class == "" && t.TitleContains == "" }

func (t Target) Matches(w Window) bool {
	if t.Empty() {
		return false
	}
	if t.Class != "" && !strings.EqualFold(t.Class, w.Class) {
		return false
	}
	if t.TitleContains != "" && !strings.Contains(strings.ToLower(w.Title), strings.ToLower(t.TitleContains)) {
		return false
	}
	return true
}

func (t Target) Label() string {
	switch {
	case t.Class != "" && t.TitleContains != "":
		return fmt.Sprintf("%s «%s»", t.Class, t.TitleContains)
	case t.Class != "":
		return t.Class
	case t.TitleContains != "":
		return "«" + t.TitleContains + "»"
	}
	return "active window"
}

// Keyboard is the generic adapter: semantic action -> chords -> backend.
type Keyboard struct {
	mu      sync.Mutex
	backend KeyBackend
	actions *Resolved
	target  Target
}

func NewKeyboard(b KeyBackend, actions *Resolved, target Target) *Keyboard {
	return &Keyboard{backend: b, actions: actions, target: target}
}

func (k *Keyboard) Backend() KeyBackend { return k.backend }

// Reconfigure swaps actions/target atomically (host settings changed).
func (k *Keyboard) Reconfigure(actions *Resolved, target Target) {
	k.mu.Lock()
	k.actions, k.target = actions, target
	k.mu.Unlock()
}

func (k *Keyboard) Manifest() Manifest {
	k.mu.Lock()
	defer k.mu.Unlock()
	m := Manifest{
		ID: "relevo.keyboard." + k.backend.Name(), Version: "0.1.0", InterfaceVersions: []int{InterfaceVersion},
		ProtocolVersions: []string{protocol.Version}, Platforms: []string{k.backend.Platform()},
		Permissions: append([]string{}, k.backend.Permissions()...), Confirmation: k.backend.Confirmation(), FocusCheck: k.backend.FocusCheck(),
		Actions: []ManifestAction{},
	}
	for _, d := range k.actions.Descriptors(m.Confirmation) {
		m.Actions = append(m.Actions, ManifestAction{ID: d.ID, Capability: d.Capability, Parameters: d.Parameters})
	}
	return m
}

func (k *Keyboard) Catalog() []protocol.ActionDescriptor {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.actions.Descriptors(k.backend.Confirmation())
}

func (k *Keyboard) focusMode() string {
	fc := k.backend.FocusCheck()
	if fc == protocol.FocusVerified && k.target.Empty() && k.target.AllowUnverifiedActiveWindow {
		return protocol.FocusUnverified
	}
	return fc
}

func (k *Keyboard) Readiness(ctx context.Context) Readiness {
	k.mu.Lock()
	t := k.target
	k.mu.Unlock()
	fc := k.focusMode()
	r := Readiness{Target: protocol.Target{Label: t.Label() + " · " + k.backend.Name(), FocusCheck: fc}}
	if err := k.backend.Check(ctx); err != nil {
		r.Code, r.Reason = "backend_unavailable", err.Error()
		var nr *ErrNotReady
		if errors.As(err, &nr) && nr.Code != "" {
			r.Code = nr.Code
		}
		return r
	}
	switch fc {
	case protocol.FocusUnverified:
		if !t.AllowUnverifiedActiveWindow {
			r.Code, r.Reason = "unverified_optin", "This backend cannot check which window has focus; explicitly enable \"send to the active window without checking\""
			return r
		}
	case protocol.FocusTargeted, protocol.FocusVerified:
		if t.Empty() {
			r.Code, r.Reason = "no_target", "Choose the target window"
			return r
		}
		if fc == protocol.FocusTargeted {
			if _, err := k.find(ctx, t); err != nil {
				r.Code, r.Reason = "target_not_found", err.Error()
				return r
			}
		}
	}
	r.Ready = true
	return r
}

var errNoTarget = errors.New("target window not found")

func (k *Keyboard) find(ctx context.Context, t Target) (Window, error) {
	ws, err := k.backend.Windows(ctx)
	if err != nil {
		return Window{}, err
	}
	for _, w := range ws {
		if t.Matches(w) {
			return w, nil
		}
	}
	return Window{}, errNoTarget
}

var safeLabel = regexp.MustCompile(`[^\pL\pN ._:-]`)

func (k *Keyboard) Execute(ctx context.Context, action string, params json.RawMessage) (string, error) {
	k.mu.Lock()
	actions, t := k.actions, k.target
	k.mu.Unlock()
	chords, err := actions.ChordsFor(action, params)
	if err != nil {
		return "", err
	}
	var w Window
	switch k.focusMode() {
	case protocol.FocusTargeted:
		if w, err = k.find(ctx, t); err != nil {
			return "", Reject(protocol.TargetNotActive, "%v", err)
		}
	case protocol.FocusVerified:
		if t.Empty() {
			return "", Reject(protocol.HostNotReady, "no target selected")
		}
		a, err := k.backend.Active(ctx)
		if err != nil {
			return "", Reject(protocol.TargetNotActive, "cannot determine the focused window: %v", err)
		}
		if !t.Matches(a) {
			return "", Reject(protocol.TargetNotActive, "the focused window is not the presentation (%s)", safeLabel.ReplaceAllString(a.Class, ""))
		}
		w = a
	case protocol.FocusUnverified:
		if !t.AllowUnverifiedActiveWindow {
			return "", Reject(protocol.HostNotReady, "unverified active-window mode not enabled")
		}
	}
	for i, c := range chords {
		if err := k.backend.Send(ctx, w, c); err != nil {
			if i == 0 && !errors.Is(err, ErrUncertain) {
				return "", fmt.Errorf("%s: %w", k.backend.Name(), err)
			}
			return "", fmt.Errorf("%w: %v", ErrUncertain, err)
		}
	}
	return k.backend.Confirmation(), nil
}
