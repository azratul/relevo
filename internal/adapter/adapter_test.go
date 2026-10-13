package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"relevo/internal/protocol"
)

func TestParseChordWhitelist(t *testing.T) {
	c, err := ParseChord("shift+ctrl+Page_Down")
	if err != nil || c.String() != "ctrl+shift+Page_Down" {
		t.Fatal(c, err)
	}
	for _, bad := range []string{"ctrl+alt+Delete", "rm -rf", "hyper+Right", "Right+"} {
		if _, err := ParseChord(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestResolveAndExpand(t *testing.T) {
	r, err := Resolve("presentation-full", nil, map[string][]string{"presentation.next": {"Page_Down"}}, []CustomAction{{ID: "custom.laser", Title: "Laser", Keys: []string{"ctrl+l"}}})
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := r.ChordsFor("presentation.next", nil)
	if ch[0].Key != "Page_Down" {
		t.Fatal(ch)
	}
	ch, err = r.ChordsFor("presentation.goto", json.RawMessage(`{"slide":12}`))
	if err != nil || len(ch) != 3 || ch[0].Key != "1" || ch[2].Key != "Return" {
		t.Fatal(ch, err)
	}
	if _, err := Resolve("presentation", nil, map[string][]string{"presentation.next": {"ctrl+alt+Delete"}}, nil); err == nil {
		t.Fatal("non-whitelisted binding accepted")
	}
}

func TestKeyboardFocusCheck(t *testing.T) {
	sim := NewSim()
	r, _ := Resolve("presentation", nil, nil, nil)
	k := NewKeyboard(sim, r, Target{Class: "relevo-sim"})
	ctx := context.Background()
	if rd := k.Readiness(ctx); !rd.Ready {
		t.Fatal(rd)
	}
	if _, err := k.Execute(ctx, "presentation.next", nil); err != nil {
		t.Fatal(err)
	}
	sim.SetFocused(false)
	_, err := k.Execute(ctx, "presentation.next", nil)
	var rej *Rejection
	if !errors.As(err, &rej) || rej.Code != protocol.TargetNotActive {
		t.Fatalf("want TARGET_NOT_ACTIVE, got %v", err)
	}
	if !slices.Equal(sim.Sent(), []string{"Right"}) {
		t.Fatal(sim.Sent())
	}
	// no target configured -> not ready
	k.Reconfigure(r, Target{})
	if rd := k.Readiness(ctx); rd.Ready {
		t.Fatal("ready without target")
	}
}

// The Go manifest must conform to the public manifest schema.
func TestManifestConformsToSchema(t *testing.T) {
	v, err := protocol.DefaultValidator()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := Resolve("presentation-full", nil, nil, []CustomAction{{ID: "custom.laser", Title: "Laser", Keys: []string{"ctrl+l"}}})
	for _, name := range []string{"sim"} {
		b, _ := NewBackend(name)
		raw, _ := json.Marshal(NewKeyboard(b, r, Target{Class: "x"}).Manifest())
		if err := v.Validate("catalog/adapter-manifest", raw); err != nil {
			t.Fatalf("%s manifest: %v\n%s", name, err, raw)
		}
	}
}
