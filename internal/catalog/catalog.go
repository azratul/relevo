// Package catalog validates action catalogs and their parameter schemas.
// It is shared by the relay and the host so both enforce the same rules.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/azratul/relevo/internal/protocol"
)

const (
	MaxActions         = 32
	MaxParamSchemaSize = 4096
)

type Action struct {
	protocol.ActionDescriptor
	schema *jsonschema.Schema
}

// Compiled is a validated catalog ready to check parameters.
type Compiled struct {
	Revision int64
	Actions  map[string]*Action
	Raw      protocol.Catalog
}

// forbidden keywords: remote/dynamic references and identifiers that could
// trigger resolution, plus keywords outside the common dialect subset (ADR-0012).
var forbidden = map[string]bool{
	"$id": true, "$schema": true, "$dynamicRef": true, "$dynamicAnchor": true, "$recursiveRef": true,
	"$recursiveAnchor": true, "$anchor": true, "$vocabulary": true, "prefixItems": true,
	"unevaluatedProperties": true, "unevaluatedItems": true, "dependentRequired": true, "dependentSchemas": true,
	"contentSchema": true,
}

func checkKeywords(v any, path string) error {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			if forbidden[k] {
				return fmt.Errorf("%s: keyword %q not allowed", path, k)
			}
			if k == "$ref" {
				s, _ := sub.(string)
				if !strings.HasPrefix(s, "#") {
					return fmt.Errorf("%s: only local $ref allowed", path)
				}
			}
			if k == "items" {
				if _, isArr := sub.([]any); isArr {
					return fmt.Errorf("%s: array form of items not allowed", path)
				}
			}
			if err := checkKeywords(sub, path+"/"+k); err != nil {
				return err
			}
		}
	case []any:
		for i, sub := range t {
			if err := checkKeywords(sub, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

type denyLoader struct{}

func (denyLoader) Load(url string) (any, error) {
	return nil, errors.New("remote references are not allowed")
}

// CompileParams compiles one parameter schema under the restrictions above.
func CompileParams(raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(raw) > MaxParamSchemaSize {
		return nil, errors.New("parameter schema too large")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	m, ok := doc.(map[string]any)
	if !ok || m["type"] != "object" {
		return nil, errors.New(`parameter schema must be {"type":"object",...}`)
	}
	if err := checkKeywords(doc, "#"); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(denyLoader{})
	if err := c.AddResource("mem:///params.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("mem:///params.json")
}

// Compile validates a catalog (structure must already conform to the schema).
func Compile(c protocol.Catalog) (*Compiled, error) {
	if c.Revision < 1 {
		return nil, errors.New("catalog revision must be >= 1")
	}
	if len(c.Actions) > MaxActions {
		return nil, errors.New("too many actions")
	}
	out := &Compiled{Revision: c.Revision, Actions: map[string]*Action{}, Raw: c}
	for _, a := range c.Actions {
		if _, dup := out.Actions[a.ID]; dup {
			return nil, fmt.Errorf("duplicate action %s", a.ID)
		}
		s, err := CompileParams(a.Parameters)
		if err != nil {
			return nil, fmt.Errorf("action %s: %w", a.ID, err)
		}
		out.Actions[a.ID] = &Action{ActionDescriptor: a, schema: s}
	}
	return out, nil
}

// ValidateParams checks params against the action schema.
func (a *Action) ValidateParams(params json.RawMessage) error {
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(params))
	if err != nil {
		return err
	}
	if err := a.schema.Validate(inst); err != nil {
		return errors.New("parameters do not match the action schema")
	}
	return nil
}
