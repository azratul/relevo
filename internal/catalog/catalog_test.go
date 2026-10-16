package catalog

import (
	"encoding/json"
	"testing"

	"github.com/azratul/relevo/internal/protocol"
)

func TestCompileAndValidate(t *testing.T) {
	c, err := Compile(protocol.Catalog{Revision: 1, Actions: []protocol.ActionDescriptor{
		{ID: "presentation.goto", Title: "Go", Capability: "presentation.navigate", Confirmation: "simulated",
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["slide"],"properties":{"slide":{"type":"integer","minimum":1}}}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	a := c.Actions["presentation.goto"]
	if a.ValidateParams(json.RawMessage(`{"slide":3}`)) != nil {
		t.Fatal("valid params rejected")
	}
	for _, bad := range []string{`{}`, `{"slide":0}`, `{"slide":3,"keys":"ctrl+alt+del"}`, `{"slide":"3"}`} {
		if a.ValidateParams(json.RawMessage(bad)) == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestRejectsDangerousSchemas(t *testing.T) {
	for _, s := range []string{
		`{"type":"object","properties":{"x":{"$ref":"https://evil.example/s.json"}}}`,
		`{"type":"object","$id":"https://evil.example/"}`,
		`{"type":"array"}`,
		`{"type":"object","properties":{"x":{"type":"array","items":[{"type":"string"}]}}}`,
	} {
		if _, err := CompileParams(json.RawMessage(s)); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
