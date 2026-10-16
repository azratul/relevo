package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/azratul/relevo/internal/protocol"
)

// SessionInfo is what the host keeps about its session. The password stays in
// memory only and is never written to disk or logs.
type SessionInfo struct {
	ID        string
	HostToken string
	Password  string
	JoinURL   string
	WSURL     string
	ExpiresAt string
}

type relayClient struct {
	base string
	http *http.Client
}

func newRelayClient(base string) *relayClient {
	return &relayClient{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

// apiError is a relay error response.
type apiError struct {
	Status int
	Err    protocol.Error
}

func (e *apiError) Error() string { return fmt.Sprintf("relay: %s (%s)", e.Err.Message, e.Err.Code) }

func (r *relayClient) call(ctx context.Context, method, path, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the relay: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 400 {
		var e protocol.ErrorPayload
		if json.Unmarshal(raw, &e) != nil || e.Error.Code == "" {
			return fmt.Errorf("relay answered HTTP %d", res.StatusCode)
		}
		return &apiError{Status: res.StatusCode, Err: e.Error}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type createReq struct {
	Password            string   `json:"password,omitempty"`
	RequireApproval     bool     `json:"requireApproval"`
	DefaultCapabilities []string `json:"defaultCapabilities,omitempty"`
}

func (r *relayClient) create(ctx context.Context, createToken string, req createReq) (*SessionInfo, error) {
	var out struct {
		SessionID, JoinURL, WSURL, HostToken, GeneratedPassword, ExpiresAt string
	}
	if err := r.call(ctx, "POST", "/api/v1/sessions", createToken, req, &out); err != nil {
		return nil, err
	}
	pw := out.GeneratedPassword
	if req.Password != "" {
		pw = req.Password
	}
	return &SessionInfo{ID: out.SessionID, HostToken: out.HostToken, Password: pw, JoinURL: out.JoinURL, WSURL: out.WSURL, ExpiresAt: out.ExpiresAt}, nil
}
