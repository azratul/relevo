package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"relevo/api"
)

const schemaBase = "file:///api/schemas/"

var printer = message.NewPrinter(language.English)

// Validator validates documents against the embedded canonical schemas.
type Validator struct {
	schemas map[string]*jsonschema.Schema
}

var (
	defaultValidator     *Validator
	defaultValidatorErr  error
	defaultValidatorOnce sync.Once
)

// DefaultValidator compiles the embedded schemas once.
func DefaultValidator() (*Validator, error) {
	defaultValidatorOnce.Do(func() { defaultValidator, defaultValidatorErr = newValidator(api.FS) })
	return defaultValidator, defaultValidatorErr
}

type denyLoader struct{}

func (denyLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema reference not allowed: %s", url)
}

func newValidator(fsys fs.FS) (*Validator, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(denyLoader{})
	var names []string
	err := fs.WalkDir(fsys, "schemas", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if err := c.AddResource("file:///api/"+p, doc); err != nil {
			return err
		}
		names = append(names, strings.TrimPrefix(p, "schemas/"))
		return nil
	})
	if err != nil {
		return nil, err
	}
	v := &Validator{schemas: map[string]*jsonschema.Schema{}}
	for _, n := range names {
		s, err := c.Compile(schemaBase + n)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", n, err)
		}
		v.schemas[strings.TrimSuffix(n, ".json")] = s
	}
	return v, nil
}

// Has reports whether a schema exists, e.g. "ws/command.request".
func (v *Validator) Has(name string) bool { _, ok := v.schemas[name]; return ok }

// Validate checks raw JSON against schema name (e.g. "ws/command.request", "http/JoinRequest").
func (v *Validator) Validate(name string, raw []byte) error {
	s, ok := v.schemas[name]
	if !ok {
		return fmt.Errorf("unknown schema %q", name)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := s.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return errors.New(firstLeaf(ve))
		}
		return err
	}
	return nil
}

// firstLeaf returns a short, safe description of the deepest validation error.
func firstLeaf(ve *jsonschema.ValidationError) string {
	for len(ve.Causes) > 0 {
		ve = ve.Causes[0]
	}
	loc := "/" + strings.Join(ve.InstanceLocation, "/")
	msg := ve.ErrorKind.LocalizedString(printer)
	if len(msg) > 160 {
		msg = msg[:160]
	}
	return fmt.Sprintf("at %s: %s", loc, msg)
}

// InboundSchema returns the schema name for a message type sent TO the relay
// by the given role ("controller" or "host"). ok=false if the role may not send it.
func InboundSchema(role, typ string) (string, bool) {
	switch typ {
	case TypeAuthHello, TypePing:
		return "ws/" + typ, true
	case TypeCommandRequest, TypeCommandStatusRequest:
		return "ws/" + typ, role == "controller"
	case TypeHostRegister, TypeHostUpdate:
		return "ws/" + typ, role == "host"
	case TypeCommandResult:
		return "ws/command.result.host", role == "host"
	}
	return "", false
}

// OutboundSchema returns the schema for a message type sent BY the relay to role.
func OutboundSchema(role, typ string) string {
	if typ == TypeCommandResult {
		if role == "host" {
			return "" // relay never sends command.result to hosts
		}
		return "ws/command.result.controller"
	}
	return "ws/" + typ
}

// DecodeEnvelope parses raw bytes into an envelope without schema validation.
func DecodeEnvelope(raw []byte) (*Envelope, error) {
	var e Envelope
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := d.Decode(&e); err != nil {
		return nil, err
	}
	if d.More() {
		return nil, errors.New("trailing data")
	}
	return &e, nil
}

// SchemaNameFromExample maps an example path like "valid/ws/command.request--x.json" to "ws/command.request".
func SchemaNameFromExample(p string) string {
	dir := path.Base(path.Dir(p))
	base := strings.TrimSuffix(path.Base(p), ".json")
	base, _, _ = strings.Cut(base, "--")
	return dir + "/" + base
}
