// Package adapter defines the host-side action adapter interface (ADR-0008)
// and a generic keyboard adapter built on platform key backends.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"relevo/internal/protocol"
)

// InterfaceVersion of the Go adapter interface. Bump on breaking changes.
const InterfaceVersion = 1

// Manifest describes an adapter (api/schemas/catalog/adapter-manifest.json).
type Manifest struct {
	ID                string           `json:"id"`
	Version           string           `json:"version"`
	InterfaceVersions []int            `json:"interfaceVersions"`
	ProtocolVersions  []string         `json:"protocolVersions"`
	Platforms         []string         `json:"platforms"`
	Actions           []ManifestAction `json:"actions"`
	Permissions       []string         `json:"permissions"`
	Confirmation      string           `json:"confirmation"`
	FocusCheck        string           `json:"focusCheck,omitempty"`
}

type ManifestAction struct {
	ID         string          `json:"id"`
	Capability string          `json:"capability"`
	Parameters json.RawMessage `json:"parameters"`
}

// Readiness is the adapter's current ability to execute.
type Readiness struct {
	Ready  bool
	Code   string // stable reason code (see ErrNotReady), empty when ready
	Reason string // why not ready, in English (missing permission, target not found...)
	Target protocol.Target
}

// Adapter executes semantic actions locally. Implementations must be safe for
// sequential use; the host never calls Execute concurrently.
type Adapter interface {
	Manifest() Manifest
	// Catalog returns the actions currently offered (subset of the manifest).
	Catalog() []protocol.ActionDescriptor
	Readiness(ctx context.Context) Readiness
	// Execute performs the action. Errors: *Rejection (nothing was done),
	// ErrUncertain (something may have been done), others (failed, nothing done).
	Execute(ctx context.Context, action string, params json.RawMessage) (confirmation string, err error)
}

// Rejection means the adapter refused before doing anything.
type Rejection struct {
	Code    protocol.Code
	Message string
}

func (r *Rejection) Error() string { return string(r.Code) + ": " + r.Message }

func Reject(code protocol.Code, format string, a ...any) error {
	return &Rejection{Code: code, Message: fmt.Sprintf(format, a...)}
}

// ErrUncertain wraps failures after input may already have reached the OS.
var ErrUncertain = errors.New("action may have been partially executed")
