package adapter

import (
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strconv"

	"github.com/azratul/relevo/internal/protocol"
)

// ActionDef is a semantic action and how it translates to keys locally.
type ActionDef struct {
	ID          string
	Title       string
	Description string
	Capability  string
	Parameters  json.RawMessage
	// Keys is the chord sequence for parameterless actions.
	Keys []string
	// Expand builds the chord sequence from parameters (e.g. goto slide N).
	Expand func(params json.RawMessage) ([]string, error)
}

var noParams = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)

// Builtin presentation actions. Keys follow what PowerPoint, Keynote,
// LibreOffice Impress, Google Slides and most PDF viewers accept in
// presentation mode; hosts can rebind them locally.
func BuiltinActions() []ActionDef {
	return []ActionDef{
		{ID: "presentation.next", Title: "Next", Description: "Next slide or animation", Capability: "presentation.navigate", Parameters: noParams, Keys: []string{"Right"}},
		{ID: "presentation.previous", Title: "Previous", Description: "Previous slide", Capability: "presentation.navigate", Parameters: noParams, Keys: []string{"Left"}},
		{ID: "presentation.first", Title: "First", Description: "First slide", Capability: "presentation.navigate", Parameters: noParams, Keys: []string{"Home"}},
		{ID: "presentation.last", Title: "Last", Description: "Last slide", Capability: "presentation.navigate", Parameters: noParams, Keys: []string{"End"}},
		{ID: "presentation.blank", Title: "Black screen", Description: "Toggle a black screen (B)", Capability: "presentation.navigate", Parameters: noParams, Keys: []string{"b"}},
		{ID: "presentation.goto", Title: "Go to slide", Description: "Types the number and Enter (PowerPoint, Impress)", Capability: "presentation.jump",
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["slide"],"properties":{"slide":{"type":"integer","minimum":1,"maximum":9999,"title":"Slide"}}}`),
			Expand: func(p json.RawMessage) ([]string, error) {
				var v struct {
					Slide int `json:"slide"`
				}
				if err := json.Unmarshal(p, &v); err != nil || v.Slide < 1 || v.Slide > 9999 {
					return nil, fmt.Errorf("invalid slide")
				}
				var keys []string
				for _, d := range strconv.Itoa(v.Slide) {
					keys = append(keys, string(d))
				}
				return append(keys, "Return"), nil
			}},
		{ID: "presentation.start", Title: "Start presentation", Description: "Start the slideshow", Capability: "presentation.control", Parameters: noParams, Keys: []string{"F5"}},
		{ID: "presentation.stop", Title: "Exit presentation", Description: "Leave the slideshow", Capability: "presentation.control", Parameters: noParams, Keys: []string{"Escape"}},
	}
}

// Profiles: named defaults (enabled actions and key overrides). Applications
// disagree mostly on how to start the show: F5 reloads the page in a browser,
// so web apps use other shortcuts. Darwin overrides apply on macOS (super = ⌘).
//
// Sources: Google Slides https://support.google.com/docs/answer/1696717,
// PowerPoint https://support.microsoft.com/office/1524ffce-bd2a-45f4-9a7f-f18b992b93a0
type Profile struct {
	Name     string
	Actions  []string
	Bindings map[string][]string
	Darwin   map[string][]string
}

var defaultActions = []string{"presentation.next", "presentation.previous"}

func Profiles() map[string]Profile {
	return map[string]Profile{
		"presentation":         {Name: "Generic (arrow keys, F5 to start)", Actions: defaultActions},
		"presentation-clicker": {Name: "Generic (Page Up/Down, like a clicker)", Actions: defaultActions, Bindings: map[string][]string{"presentation.next": {"Page_Down"}, "presentation.previous": {"Page_Up"}}},
		"presentation-full":    {Name: "Generic (all actions)", Actions: []string{"presentation.next", "presentation.previous", "presentation.first", "presentation.last", "presentation.blank", "presentation.goto"}},
		"google-slides": {Name: "Google Slides", Actions: defaultActions,
			Bindings: map[string][]string{"presentation.start": {"ctrl+F5"}},
			Darwin:   map[string][]string{"presentation.start": {"super+Return"}}},
		"powerpoint": {Name: "PowerPoint (desktop)", Actions: defaultActions,
			Bindings: map[string][]string{"presentation.start": {"F5"}},
			Darwin:   map[string][]string{"presentation.start": {"super+shift+Return"}}},
		"powerpoint-web": {Name: "PowerPoint for the web", Actions: defaultActions,
			Bindings: map[string][]string{"presentation.start": {"ctrl+F5"}},
			Darwin:   map[string][]string{"presentation.start": {"super+shift+Return"}}},
		"keynote": {Name: "Keynote (macOS)", Actions: defaultActions,
			Darwin: map[string][]string{"presentation.start": {"alt+super+p"}}},
		"libreoffice": {Name: "LibreOffice Impress", Actions: defaultActions,
			Bindings: map[string][]string{"presentation.start": {"F5"}}},
	}
}

// DefaultKeys returns the keys each parameterless builtin action gets from a
// profile on this OS, before the host's own bindings.
func DefaultKeys(profile string) map[string][]string { return defaultKeysFor(runtime.GOOS, profile) }

func defaultKeysFor(goos, profile string) map[string][]string {
	prof := Profiles()[profile]
	out := map[string][]string{}
	for _, d := range BuiltinActions() {
		if d.Expand != nil {
			continue
		}
		keys := d.Keys
		if b, ok := prof.Bindings[d.ID]; ok {
			keys = b
		}
		if b, ok := prof.Darwin[d.ID]; ok && goos == "darwin" {
			keys = b
		}
		out[d.ID] = keys
	}
	return out
}

// CustomAction is a host-defined action from the local config.
type CustomAction struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Capability  string   `json:"capability,omitempty"`
	Keys        []string `json:"keys"`
}

// Resolved is the effective set of actions and their chords.
type Resolved struct {
	Defs   map[string]ActionDef
	Order  []string
	Chords map[string][]Chord // for parameterless actions
}

// Resolve applies a profile, enabled list, bindings and custom actions.
// Every key is validated against the whitelist.
func Resolve(profile string, enabled []string, bindings map[string][]string, custom []CustomAction) (*Resolved, error) {
	return resolveFor(runtime.GOOS, profile, enabled, bindings, custom)
}

func resolveFor(goos, profile string, enabled []string, bindings map[string][]string, custom []CustomAction) (*Resolved, error) {
	prof, ok := Profiles()[profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", profile)
	}
	if enabled == nil {
		enabled = prof.Actions
	}
	all := map[string]ActionDef{}
	for _, d := range BuiltinActions() {
		all[d.ID] = d
	}
	for _, c := range custom {
		if _, dup := all[c.ID]; dup {
			return nil, fmt.Errorf("custom action %s collides with a builtin", c.ID)
		}
		capab := c.Capability
		if capab == "" {
			capab = "presentation.navigate"
		}
		all[c.ID] = ActionDef{ID: c.ID, Title: c.Title, Description: c.Description, Capability: capab, Parameters: noParams, Keys: c.Keys}
		if !slices.Contains(enabled, c.ID) {
			enabled = append(slices.Clone(enabled), c.ID)
		}
	}
	defaults := defaultKeysFor(goos, profile)
	r := &Resolved{Defs: map[string]ActionDef{}, Chords: map[string][]Chord{}}
	for _, id := range enabled {
		d, ok := all[id]
		if !ok {
			return nil, fmt.Errorf("unknown action %q", id)
		}
		keys := d.Keys
		if b, ok := defaults[id]; ok {
			keys = b
		}
		if b, ok := bindings[id]; ok {
			keys = b
		}
		if d.Expand == nil {
			if len(keys) == 0 || len(keys) > 8 {
				return nil, fmt.Errorf("action %s needs 1-8 keys", id)
			}
			for _, k := range keys {
				ch, err := ParseChord(k)
				if err != nil {
					return nil, fmt.Errorf("action %s: %w", id, err)
				}
				r.Chords[id] = append(r.Chords[id], ch)
			}
		}
		r.Defs[id] = d
		r.Order = append(r.Order, id)
	}
	return r, nil
}

// Descriptors converts to catalog entries.
func (r *Resolved) Descriptors(confirmation string) []protocol.ActionDescriptor {
	out := []protocol.ActionDescriptor{}
	for _, id := range r.Order {
		d := r.Defs[id]
		out = append(out, protocol.ActionDescriptor{ID: d.ID, Title: d.Title, Description: d.Description, Capability: d.Capability, Parameters: d.Parameters, Confirmation: confirmation})
	}
	return out
}

// ChordsFor returns the chord sequence for an action invocation.
func (r *Resolved) ChordsFor(action string, params json.RawMessage) ([]Chord, error) {
	d, ok := r.Defs[action]
	if !ok {
		return nil, Reject(protocol.UnsupportedAction, "action not enabled")
	}
	if d.Expand == nil {
		return r.Chords[action], nil
	}
	keys, err := d.Expand(params)
	if err != nil {
		return nil, Reject(protocol.InvalidMessage, "%v", err)
	}
	var out []Chord
	for _, k := range keys {
		ch, err := ParseChord(k)
		if err != nil {
			return nil, Reject(protocol.InvalidMessage, "%v", err)
		}
		out = append(out, ch)
	}
	return out, nil
}
