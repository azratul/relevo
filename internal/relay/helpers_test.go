package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"relevo/internal/auth"
	"relevo/internal/protocol"
	"relevo/internal/secret"
)

type env struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
}

func newEnv(t *testing.T, mod func(*Config)) *env {
	t.Helper()
	cfg := DefaultConfig()
	cfg.DevInsecure = true
	cfg.Argon2 = auth.Params{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, MaxConcurrent: 4}
	cfg.ValidateOutbound = true
	cfg.LoginPerIPPerMinute = 600
	cfg.CreatePerIPPerHour = 3600
	cfg.HeartbeatInterval = time.Second
	ts := httptest.NewUnstartedServer(nil)
	ts.Start()
	cfg.Listen = strings.TrimPrefix(ts.URL, "http://")
	if mod != nil {
		mod(&cfg)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		log = slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	s, err := New(cfg, log, testWeb)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	e := &env{t: t, srv: s, ts: ts}
	t.Cleanup(func() {
		if n := s.OutboundViolations.Load(); n != 0 {
			t.Errorf("%d outbound messages violated the schemas", n)
		}
		s.Shutdown(context.Background())
		ts.Close()
	})
	return e
}

var testWeb = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>t</title>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func (e *env) do(method, path, token string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &out)
	// every error body must conform to the contract
	if res.StatusCode >= 400 {
		v, _ := protocol.DefaultValidator()
		if err := v.Validate("http/ErrorResponse", raw); err != nil {
			e.t.Errorf("%s %s: error body violates schema: %v %s", method, path, err, raw)
		}
	}
	return res.StatusCode, out
}

type session struct {
	id, hostToken, password string
}

func (e *env) create(body map[string]any) session {
	e.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	code, out := e.do("POST", "/api/v1/sessions", "", body)
	if code != 201 {
		e.t.Fatalf("create: %d %v", code, out)
	}
	pw, _ := out["generatedPassword"].(string)
	if body != nil && body["password"] != nil {
		pw = body["password"].(string)
	}
	return session{id: out["sessionId"].(string), hostToken: out["hostToken"].(string), password: pw}
}

func (e *env) join(s session, name, pw string) (int, map[string]any) {
	return e.do("POST", "/api/v1/sessions/"+s.id+"/join", "", map[string]any{"displayName": name, "password": pw})
}

func (e *env) mustJoin(s session, name string) string {
	e.t.Helper()
	code, out := e.join(s, name, s.password)
	if code != 200 {
		e.t.Fatalf("join: %d %v", code, out)
	}
	return out["token"].(string)
}

// ---- WebSocket test client ----

type msg struct {
	protocol.Envelope
	payload map[string]any
}

type wsc struct {
	t      *testing.T
	c      *websocket.Conn
	sid    string
	in     chan msg
	closed chan websocket.StatusCode
}

func (e *env) dial(sid string, hdr http.Header) (*wsc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(e.ts.URL, "http", "ws", 1)+"/api/v1/ws", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, err
	}
	w := &wsc{t: e.t, c: c, sid: sid, in: make(chan msg, 256), closed: make(chan websocket.StatusCode, 1)}
	go func() {
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				w.closed <- websocket.CloseStatus(err)
				close(w.in)
				return
			}
			env, err := protocol.DecodeEnvelope(data)
			if err != nil {
				e.t.Errorf("bad frame %s", data)
				continue
			}
			m := msg{Envelope: *env}
			_ = json.Unmarshal(env.Payload, &m.payload)
			w.in <- m
		}
	}()
	e.t.Cleanup(func() { c.CloseNow() })
	return w, nil
}

func (w *wsc) send(typ string, payload any) string {
	w.t.Helper()
	id := secret.UUID()
	b, _ := protocol.Encode(typ, id, w.sid, "", payload)
	if err := w.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		w.t.Fatalf("write: %v", err)
	}
	return id
}

func (w *wsc) sendRaw(b []byte) {
	_ = w.c.Write(context.Background(), websocket.MessageText, b)
}

// next waits for the next message of one of the given types, skipping others.
func (w *wsc) next(types ...string) msg {
	w.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m, ok := <-w.in:
			if !ok {
				w.t.Fatalf("connection closed while waiting for %v", types)
			}
			for _, t := range types {
				if m.Type == t {
					return m
				}
			}
		case <-deadline:
			w.t.Fatalf("timeout waiting for %v", types)
		}
	}
}

// none asserts that no message of the given type arrives within d.
func (w *wsc) none(d time.Duration, typ string) {
	w.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m, ok := <-w.in:
			if !ok {
				return
			}
			if m.Type == typ {
				w.t.Fatalf("unexpected %s: %v", typ, m.payload)
			}
		case <-deadline:
			return
		}
	}
}

func (w *wsc) waitClosed() websocket.StatusCode {
	w.t.Helper()
	for {
		select {
		case _, ok := <-w.in:
			if !ok {
				return <-w.closed
			}
		case <-time.After(5 * time.Second):
			w.t.Fatal("connection not closed")
		}
	}
}

func (e *env) connect(sid, token string) *wsc {
	e.t.Helper()
	w, err := e.dial(sid, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	w.send("auth.hello", map[string]any{"token": token, "client": map[string]any{"name": "test"}})
	ok := w.next("auth.ok", "auth.error")
	if ok.Type != "auth.ok" {
		e.t.Fatalf("auth failed: %v", ok.payload)
	}
	w.next("session.snapshot")
	return w
}

var testCatalog = map[string]any{"revision": 1, "actions": []any{
	map[string]any{"id": "presentation.next", "title": "Next", "capability": "presentation.navigate", "confirmation": "simulated",
		"parameters": map[string]any{"type": "object", "additionalProperties": false}},
	map[string]any{"id": "presentation.previous", "title": "Previous", "capability": "presentation.navigate", "confirmation": "simulated",
		"parameters": map[string]any{"type": "object", "additionalProperties": false}},
	map[string]any{"id": "presentation.goto", "title": "Go to", "capability": "presentation.jump", "confirmation": "simulated",
		"parameters": map[string]any{"type": "object", "additionalProperties": false, "required": []any{"slide"},
			"properties": map[string]any{"slide": map[string]any{"type": "integer", "minimum": 1}}}},
}}

// connectHost authenticates as host and registers; returns the client and epoch.
func (e *env) connectHost(s session) (*wsc, int64) {
	e.t.Helper()
	h := e.connect(s.id, s.hostToken)
	h.send("host.register", map[string]any{
		"agentInstanceId": secret.UUID(), "agent": map[string]any{"name": "fake-host", "version": "0"},
		"ready": true, "target": map[string]any{"label": "test", "focusCheck": "simulated"}, "catalog": testCatalog,
	})
	m := h.next("host.registered")
	return h, int64(m.payload["epoch"].(float64))
}

type controller struct {
	*wsc
	seq int64
}

func (c *controller) command(action string, params map[string]any) (cmdID, msgID string) {
	c.seq++
	cmdID = secret.UUID()
	if params == nil {
		params = map[string]any{}
	}
	msgID = c.send("command.request", map[string]any{"commandId": cmdID, "clientSequence": c.seq, "action": action, "parameters": params, "catalogRevision": 1})
	return cmdID, msgID
}

func (e *env) controller(s session, name string) *controller {
	tok := e.mustJoin(s, name)
	return &controller{wsc: e.connect(s.id, tok)}
}

// executeAll makes the fake host answer n dispatches with status executed.
func executeOne(t *testing.T, h *wsc) msg {
	t.Helper()
	d := h.next("command.dispatch")
	h.send("command.result", map[string]any{
		"commandId": d.payload["commandId"], "sequence": d.payload["sequence"], "epoch": d.payload["epoch"],
		"status": "executed", "confirmation": "simulated",
	})
	return d
}
