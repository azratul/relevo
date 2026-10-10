package protocol

import (
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"relevo/api"
)

func TestExamplesConformToSchemas(t *testing.T) {
	v, err := DefaultValidator()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = fs.WalkDir(api.FS, "examples", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		n++
		raw, _ := fs.ReadFile(api.FS, p)
		name := SchemaNameFromExample(p)
		if !v.Has(name) {
			t.Errorf("%s: no schema %s", p, name)
			return nil
		}
		verr := v.Validate(name, raw)
		valid := strings.HasPrefix(p, "examples/valid/")
		if valid && verr != nil {
			t.Errorf("%s should be valid: %v", p, verr)
		}
		if !valid && verr == nil {
			t.Errorf("%s should be invalid but validated", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 40 {
		t.Fatalf("only %d examples found", n)
	}
}

// The Go error catalog and the schema enum must be identical.
func TestErrorCodesMatchSchema(t *testing.T) {
	raw, _ := fs.ReadFile(api.FS, "schemas/common.json")
	var doc struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	schema := doc.Defs["ErrorCode"].Enum
	var gocodes []string
	for _, c := range Codes() {
		gocodes = append(gocodes, string(c))
	}
	slices.Sort(schema)
	slices.Sort(gocodes)
	if !slices.Equal(schema, gocodes) {
		t.Fatalf("schema enum %v != go codes %v", schema, gocodes)
	}
}

// Every valid ws example must decode into its Go type and re-encode to a document that is still valid
// (guards against Go types silently dropping or renaming fields).
func TestGoTypesRoundTrip(t *testing.T) {
	v, _ := DefaultValidator()
	types := map[string]func() any{
		"ws/auth.hello": func() any { return &AuthHello{} }, "ws/auth.ok": func() any { return &AuthOK{} },
		"ws/host.register": func() any { return &HostRegister{} }, "ws/host.update": func() any { return &HostUpdate{} },
		"ws/session.snapshot": func() any { return &SessionView{} }, "ws/session.updated": func() any { return &SessionView{} },
		"ws/participant.updated": func() any { return &ParticipantUpdated{} }, "ws/command.request": func() any { return &CommandRequest{} },
		"ws/command.dispatch": func() any { return &CommandDispatch{} }, "ws/command.result.host": func() any { return &HostCommandResult{} },
		"ws/command.result.controller": func() any { return &CommandResult{} }, "ws/command.status": func() any { return &CommandStatus{} },
		"ws/command.accepted": func() any { return &CommandAccepted{} }, "ws/command.rejected": func() any { return &CommandRejected{} },
	}
	_ = fs.WalkDir(api.FS, "examples/valid/ws", func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}
		name := SchemaNameFromExample(p)
		mk, ok := types[name]
		if !ok {
			return nil
		}
		raw, _ := fs.ReadFile(api.FS, p)
		env, err := DecodeEnvelope(raw)
		if err != nil {
			t.Fatal(err)
		}
		obj := mk()
		if err := json.Unmarshal(env.Payload, obj); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out, _ := Encode(env.Type, env.MessageID, env.SessionID, env.CorrelationID, obj)
		if err := v.Validate(name, out); err != nil {
			t.Errorf("%s: re-encoded invalid: %v\n%s", p, err, out)
		}
		var a, b any
		_ = json.Unmarshal(raw, &a)
		_ = json.Unmarshal(out, &b)
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Errorf("%s: round trip changed document\n%s\n%s", p, ja, jb)
		}
		return nil
	})
}
