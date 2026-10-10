package protocol

import "net/http"

// Code is a protocol error code. See docs/protocol.md#errores.
type Code string

const (
	AuthFailed        Code = "AUTH_FAILED"
	AuthExpired       Code = "AUTH_EXPIRED"
	Forbidden         Code = "FORBIDDEN"
	SessionClosed     Code = "SESSION_CLOSED"
	HostOffline       Code = "HOST_OFFLINE"
	HostNotReady      Code = "HOST_NOT_READY"
	SessionPaused     Code = "SESSION_PAUSED"
	RateLimited       Code = "RATE_LIMITED"
	InvalidMessage    Code = "INVALID_MESSAGE"
	UnsupportedVer    Code = "UNSUPPORTED_VERSION"
	UnsupportedAction Code = "UNSUPPORTED_ACTION"
	StaleCatalog      Code = "STALE_CATALOG"
	TargetNotActive   Code = "TARGET_NOT_ACTIVE"
	PermissionDenied  Code = "PERMISSION_DENIED"
	CommandExpired    Code = "COMMAND_EXPIRED"
	CommandIDConflict Code = "COMMAND_ID_CONFLICT"
	ExecutionUnknown  Code = "EXECUTION_UNKNOWN"
	ExecutionFailed   Code = "EXECUTION_FAILED"
	NotFound          Code = "NOT_FOUND"
	InternalError     Code = "INTERNAL_ERROR"
)

type codeInfo struct {
	http      int
	retryable bool
}

var codes = map[Code]codeInfo{
	AuthFailed:        {http.StatusUnauthorized, false},
	AuthExpired:       {http.StatusUnauthorized, false},
	Forbidden:         {http.StatusForbidden, false},
	SessionClosed:     {http.StatusGone, false},
	HostOffline:       {http.StatusConflict, true},
	HostNotReady:      {http.StatusConflict, true},
	SessionPaused:     {http.StatusConflict, true},
	RateLimited:       {http.StatusTooManyRequests, true},
	InvalidMessage:    {http.StatusBadRequest, false},
	UnsupportedVer:    {http.StatusBadRequest, false},
	UnsupportedAction: {http.StatusBadRequest, false},
	StaleCatalog:      {http.StatusConflict, true},
	TargetNotActive:   {http.StatusConflict, true},
	PermissionDenied:  {http.StatusForbidden, false},
	CommandExpired:    {http.StatusConflict, false},
	CommandIDConflict: {http.StatusConflict, false},
	ExecutionUnknown:  {http.StatusInternalServerError, false},
	ExecutionFailed:   {http.StatusInternalServerError, true},
	NotFound:          {http.StatusNotFound, false},
	InternalError:     {http.StatusInternalServerError, true},
}

// Codes returns all defined codes (used by conformance tests against the schema enum).
func Codes() []Code {
	out := make([]Code, 0, len(codes))
	for c := range codes {
		out = append(out, c)
	}
	return out
}

// NewError builds an error with the default retryability of the code.
func NewError(c Code, msg string) *Error {
	return &Error{Code: c, Message: msg, Retryable: codes[c].retryable}
}

// HTTPStatus maps a code to its HTTP status.
func HTTPStatus(c Code) int {
	if i, ok := codes[c]; ok {
		return i.http
	}
	return http.StatusInternalServerError
}

// WebSocket close codes (application range 4000-4999). See docs/protocol.md.
const (
	CloseProtocolError = 4000
	CloseAuthFailed    = 4001
	CloseUnsupported   = 4002
	CloseRevoked       = 4003
	CloseSessionClosed = 4004
	ClosePolicy        = 4008
	CloseSuperseded    = 4009
)
