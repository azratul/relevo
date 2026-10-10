// Package protocol defines the Relevo application protocol (transport
// independent). The canonical definition is api/schemas; these types mirror it.
package protocol

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	// Major/Minor of the protocol this build speaks. Independent from the app version.
	Major   = 1
	Minor   = 0
	Version = "1.0"
)

// Message types.
const (
	TypeAuthHello            = "auth.hello"
	TypeAuthOK               = "auth.ok"
	TypeAuthError            = "auth.error"
	TypeHostRegister         = "host.register"
	TypeHostRegistered       = "host.registered"
	TypeHostUpdate           = "host.update"
	TypeSessionSnapshot      = "session.snapshot"
	TypeSessionUpdated       = "session.updated"
	TypeSessionClosed        = "session.closed"
	TypeParticipantUpdated   = "participant.updated"
	TypeCommandRequest       = "command.request"
	TypeCommandAccepted      = "command.accepted"
	TypeCommandRejected      = "command.rejected"
	TypeCommandDispatch      = "command.dispatch"
	TypeCommandResult        = "command.result"
	TypeCommandStatusRequest = "command.status.request"
	TypeCommandStatus        = "command.status"
	TypePing                 = "ping"
	TypePong                 = "pong"
	TypeError                = "error"
)

// Envelope is the common wrapper of every message.
type Envelope struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Type            string          `json:"type"`
	MessageID       string          `json:"messageId"`
	SessionID       string          `json:"sessionId,omitempty"`
	CorrelationID   string          `json:"correlationId,omitempty"`
	Payload         json.RawMessage `json:"payload"`
}

// ParseVersion parses "MAJOR.MINOR".
func ParseVersion(v string) (major, minor int, err error) {
	a, b, ok := strings.Cut(v, ".")
	if !ok {
		return 0, 0, fmt.Errorf("bad version %q", v)
	}
	if major, err = strconv.Atoi(a); err != nil {
		return 0, 0, err
	}
	if minor, err = strconv.Atoi(b); err != nil {
		return 0, 0, err
	}
	return major, minor, nil
}

// Compatible reports whether a peer speaking v can talk to this build: same major.
func Compatible(v string) bool {
	m, _, err := ParseVersion(v)
	return err == nil && m == Major
}

type Error struct {
	Code         Code   `json:"code"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable"`
	RetryAfterMs int64  `json:"retryAfterMs,omitempty"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type AuthHello struct {
	Token  string      `json:"token"`
	Client *ClientInfo `json:"client,omitempty"`
}

type Heartbeat struct {
	IntervalMs int64 `json:"intervalMs"`
	TimeoutMs  int64 `json:"timeoutMs"`
}

type Limits struct {
	MaxMessageBytes      int64   `json:"maxMessageBytes"`
	MaxMessagesPerSecond float64 `json:"maxMessagesPerSecond"`
	CommandValidForMs    int64   `json:"commandValidForMs"`
	MaxInFlightCommands  int     `json:"maxInFlightCommands"`
}

type AuthOK struct {
	Role            string    `json:"role"`
	ParticipantID   string    `json:"participantId,omitempty"`
	ConnectionID    string    `json:"connectionId"`
	ProtocolVersion string    `json:"protocolVersion"`
	Capabilities    []string  `json:"capabilities"`
	Heartbeat       Heartbeat `json:"heartbeat"`
	Limits          Limits    `json:"limits"`
	RTC             *RTCInfo  `json:"rtc,omitempty"`
}

// RTCInfo tells a controller how to try the WebRTC DataChannel transport.
type RTCInfo struct {
	ICEServers []ICEServer `json:"iceServers"`
}

type ICEServer struct {
	URLs []string `json:"urls"`
}

type ErrorPayload struct {
	Error Error `json:"error"`
}

type Target struct {
	Label      string `json:"label"`
	FocusCheck string `json:"focusCheck"`
}

// Focus check modes.
const (
	FocusTargeted   = "targeted"
	FocusVerified   = "verified"
	FocusUnverified = "unverified"
	FocusSimulated  = "simulated"
)

// Confirmation levels.
const (
	ConfirmSimulated     = "simulated"
	ConfirmInputInjected = "input-injected"
	ConfirmAppAPI        = "application-api"
	ConfirmVerified      = "effect-verified"
)

type ActionDescriptor struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Description  string          `json:"description,omitempty"`
	Capability   string          `json:"capability"`
	Parameters   json.RawMessage `json:"parameters"`
	Confirmation string          `json:"confirmation"`
}

type Catalog struct {
	Revision int64              `json:"revision"`
	Actions  []ActionDescriptor `json:"actions"`
}

type AgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type HostRegister struct {
	AgentInstanceID string    `json:"agentInstanceId"`
	Agent           AgentInfo `json:"agent"`
	Ready           bool      `json:"ready"`
	ReadinessReason string    `json:"readinessReason,omitempty"`
	Target          Target    `json:"target"`
	Catalog         Catalog   `json:"catalog"`
	Features        []string  `json:"features,omitempty"`
}

type HostRegistered struct {
	Epoch int64 `json:"epoch"`
}

type HostUpdate struct {
	Ready           *bool    `json:"ready,omitempty"`
	ReadinessReason *string  `json:"readinessReason,omitempty"`
	Target          *Target  `json:"target,omitempty"`
	Catalog         *Catalog `json:"catalog,omitempty"`
}

// Participant states.
const (
	StatePending  = "pending"
	StateEnabled  = "enabled"
	StateDisabled = "disabled"
	StateRevoked  = "revoked"
)

type Participant struct {
	ParticipantID string   `json:"participantId"`
	DisplayName   string   `json:"displayName"`
	State         string   `json:"state"`
	Connected     bool     `json:"connected"`
	Capabilities  []string `json:"capabilities"`
	JoinedAt      string   `json:"joinedAt"`
}

type HostStatus struct {
	Online          bool    `json:"online"`
	Ready           bool    `json:"ready"`
	ReadinessReason string  `json:"readinessReason,omitempty"`
	Target          *Target `json:"target,omitempty"`
	Epoch           int64   `json:"epoch,omitempty"`
}

type SessionView struct {
	SessionID       string        `json:"sessionId"`
	Status          string        `json:"status"`
	Paused          bool          `json:"paused"`
	RequireApproval bool          `json:"requireApproval"`
	AuthzRevision   int64         `json:"authzRevision"`
	Host            HostStatus    `json:"host"`
	Catalog         *Catalog      `json:"catalog"`
	ExpiresAt       string        `json:"expiresAt"`
	You             *Participant  `json:"you,omitempty"`
	Participants    []Participant `json:"participants,omitempty"`
}

type SessionClosedPayload struct {
	Reason string `json:"reason"`
}

type ParticipantUpdated struct {
	Participant   Participant `json:"participant"`
	AuthzRevision int64       `json:"authzRevision"`
}

type CommandRequest struct {
	CommandID       string          `json:"commandId"`
	ClientSequence  int64           `json:"clientSequence"`
	Action          string          `json:"action"`
	Parameters      json.RawMessage `json:"parameters"`
	CatalogRevision int64           `json:"catalogRevision"`
}

type CommandAccepted struct {
	CommandID string `json:"commandId"`
	Sequence  int64  `json:"sequence"`
}

type CommandRejected struct {
	CommandID string `json:"commandId"`
	Error     Error  `json:"error"`
}

type IssuedBy struct {
	ParticipantID string `json:"participantId"`
	DisplayName   string `json:"displayName"`
}

type CommandDispatch struct {
	CommandID       string          `json:"commandId"`
	Sequence        int64           `json:"sequence"`
	Epoch           int64           `json:"epoch"`
	Action          string          `json:"action"`
	Parameters      json.RawMessage `json:"parameters"`
	CatalogRevision int64           `json:"catalogRevision"`
	AuthzRevision   int64           `json:"authzRevision"`
	IssuedBy        IssuedBy        `json:"issuedBy"`
	ValidForMs      int64           `json:"validForMs"`
}

// Command statuses.
const (
	StatusAccepted   = "accepted"
	StatusDispatched = "dispatched"
	StatusExecuted   = "executed"
	StatusFailed     = "failed"
	StatusRejected   = "rejected"
	StatusExpired    = "expired"
	StatusUnknown    = "unknown"
)

// Terminal reports whether a command status is final.
func Terminal(status string) bool {
	switch status {
	case StatusExecuted, StatusFailed, StatusRejected, StatusExpired, StatusUnknown:
		return true
	}
	return false
}

// HostCommandResult is command.result host -> relay.
type HostCommandResult struct {
	CommandID    string `json:"commandId"`
	Sequence     int64  `json:"sequence"`
	Epoch        int64  `json:"epoch"`
	Status       string `json:"status"`
	Confirmation string `json:"confirmation,omitempty"`
	Error        *Error `json:"error,omitempty"`
	DurationMs   int64  `json:"durationMs,omitempty"`
}

// CommandResult is command.result relay -> controller.
type CommandResult struct {
	CommandID    string `json:"commandId"`
	Sequence     int64  `json:"sequence,omitempty"`
	Status       string `json:"status"`
	Confirmation string `json:"confirmation,omitempty"`
	Error        *Error `json:"error,omitempty"`
}

type CommandStatusRequest struct {
	CommandID string `json:"commandId"`
}

type CommandStatus struct {
	CommandID    string `json:"commandId"`
	Found        bool   `json:"found"`
	Status       string `json:"status,omitempty"`
	Sequence     int64  `json:"sequence,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
	Error        *Error `json:"error,omitempty"`
}

type Empty struct{}

// Encode builds a full message.
func Encode(typ, messageID, sessionID, correlationID string, payload any) ([]byte, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{
		ProtocolVersion: Version, Type: typ, MessageID: messageID,
		SessionID: sessionID, CorrelationID: correlationID, Payload: p,
	})
}
