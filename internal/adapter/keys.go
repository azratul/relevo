package adapter

import (
	"fmt"
	"slices"
	"strings"
)

// Logical key names are X keysym names (layout independent where the platform
// allows it). Only this whitelist can ever be injected (ADR-0005).
var keysyms = map[string]uint32{
	"Left": 0xff51, "Up": 0xff52, "Right": 0xff53, "Down": 0xff54,
	"Page_Up": 0xff55, "Page_Down": 0xff56, "Home": 0xff50, "End": 0xff57,
	"space": 0x20, "Return": 0xff0d, "Escape": 0xff1b, "BackSpace": 0xff08, "Tab": 0xff09,
	"period": 0x2e,
}

func init() {
	for i := 0; i < 12; i++ {
		keysyms[fmt.Sprintf("F%d", i+1)] = 0xffbe + uint32(i)
	}
	for c := 'a'; c <= 'z'; c++ {
		keysyms[string(c)] = uint32(c)
	}
	for c := '0'; c <= '9'; c++ {
		keysyms[string(c)] = uint32(c)
	}
}

// Modifiers, in press order.
var Modifiers = []string{"ctrl", "alt", "shift", "super"}

// ModifierKeysyms are the left-hand modifier keysyms.
var ModifierKeysyms = map[string]uint32{"shift": 0xffe1, "ctrl": 0xffe3, "alt": 0xffe9, "super": 0xffeb}

// Keysym returns the X keysym of a logical key name.
func Keysym(name string) (uint32, bool) { k, ok := keysyms[name]; return k, ok }

// KeyNames returns the whitelist (sorted).
func KeyNames() []string {
	out := make([]string, 0, len(keysyms))
	for k := range keysyms {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Chord is one key with modifiers, e.g. ctrl+shift+Page_Down.
type Chord struct {
	Mods []string
	Key  string
}

func (c Chord) String() string { return strings.Join(append(slices.Clone(c.Mods), c.Key), "+") }

// ParseChord parses "mod+mod+Key" against the whitelist.
func ParseChord(s string) (Chord, error) {
	parts := strings.Split(s, "+")
	key := parts[len(parts)-1]
	if _, ok := keysyms[key]; !ok {
		return Chord{}, fmt.Errorf("key %q not allowed (see docs/platform-support.md#teclas)", key)
	}
	var c Chord
	for _, m := range parts[:len(parts)-1] {
		m = strings.ToLower(m)
		if !slices.Contains(Modifiers, m) {
			return Chord{}, fmt.Errorf("modifier %q not allowed", m)
		}
		if !slices.Contains(c.Mods, m) {
			c.Mods = append(c.Mods, m)
		}
	}
	// canonical order
	slices.SortFunc(c.Mods, func(a, b string) int { return slices.Index(Modifiers, a) - slices.Index(Modifiers, b) })
	c.Key = key
	return c, nil
}
