package relay

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"relevo/internal/protocol"
)

func errCode(m msg) string {
	if e, ok := m.payload["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

// Criterion 1: a leaked link and a wrong password give no state and no control.
func TestLeakedLinkWithoutPassword(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(nil)
	if c, out := e.join(s, "Mallory", "wrong-password"); c != 401 || out["error"].(map[string]any)["code"] != "AUTH_FAILED" {
		t.Fatalf("join with wrong password: %d %v", c, out)
	}
	// unknown session answers exactly like a wrong password
	if c, _ := e.do("POST", "/api/v1/sessions/AAAAAAAAAAAAAAAAAAAAAA/join", "", map[string]any{"displayName": "x", "password": "y"}); c != 401 {
		t.Fatalf("unknown session: %d", c)
	}
	for _, tok := range []string{"", "rlvp_forged_token_value_123456"} {
		if c, _ := e.do("GET", "/api/v1/sessions/"+s.id+"/state", tok, nil); c != 401 {
			t.Fatalf("state with token %q: %d", tok, c)
		}
	}
	// WS: forged token -> auth.error + close 4001
	w, err := e.dial(s.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.send("auth.hello", map[string]any{"token": "rlvp_forged_token_value_123456"})
	if m := w.next("auth.error"); errCode(m) != "AUTH_FAILED" {
		t.Fatalf("got %v", m.payload)
	}
	if st := w.waitClosed(); st != protocol.CloseAuthFailed {
		t.Fatalf("close %d", st)
	}
	// WS: command before auth -> rejected, no state revealed
	w2, _ := e.dial(s.id, nil)
	w2.send("command.request", map[string]any{"commandId": "809d09d7-e84c-40d5-a4e6-02514a467c0e", "clientSequence": 1, "action": "presentation.next", "parameters": map[string]any{}, "catalogRevision": 1})
	m := w2.next("auth.error", "session.snapshot")
	if m.Type != "auth.error" {
		t.Fatalf("state leaked: %v", m)
	}
	w2.waitClosed()
}

// Criterion 2: a participant token cannot do host operations.
func TestParticipantCannotAdminister(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(nil)
	tok := e.mustJoin(s, "Ana")
	code, out := e.do("GET", "/api/v1/sessions/"+s.id+"/state", tok, nil)
	if code != 200 || out["participants"] != nil || out["you"] == nil {
		t.Fatalf("participant view must not list others: %d %v", code, out)
	}
	pid := out["you"].(map[string]any)["participantId"].(string)
	for _, c := range []struct {
		m, p string
		body any
	}{
		{"GET", "/participants", nil},
		{"PATCH", "/participants/" + pid, map[string]any{"state": "enabled", "capabilities": []string{"presentation.jump"}}},
		{"PATCH", "", map[string]any{"paused": true}},
		{"POST", "/password", map[string]any{}},
		{"DELETE", "", nil},
	} {
		if got, out := e.do(c.m, "/api/v1/sessions/"+s.id+c.p, tok, c.body); got != 403 || out["error"].(map[string]any)["code"] != "FORBIDDEN" {
			t.Errorf("%s %s: %d %v", c.m, c.p, got, out)
		}
	}
	// over WS a controller cannot register as host or send results
	w := e.connect(s.id, tok)
	w.send("host.register", map[string]any{})
	if m := w.next("error"); errCode(m) != "FORBIDDEN" {
		t.Fatalf("got %v", m.payload)
	}
	// host token is required: participant token for another session fails
	s2 := e.create(nil)
	if c, _ := e.do("GET", "/api/v1/sessions/"+s2.id+"/state", tok, nil); c != 401 {
		t.Fatalf("cross-session token: %d", c)
	}
}

// Criterion 3: unauthenticated sockets expire; bad Origin and malformed messages are rejected.
func TestUnauthenticatedTimeoutOriginAndMalformed(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.AuthTimeout = 200 * time.Millisecond })
	s := e.create(nil)
	w, _ := e.dial(s.id, nil)
	if m := w.next("auth.error"); errCode(m) != "AUTH_FAILED" {
		t.Fatal(m.payload)
	}
	if st := w.waitClosed(); st != protocol.CloseAuthFailed {
		t.Fatalf("close %d", st)
	}
	if _, err := e.dial(s.id, http.Header{"Origin": {"https://evil.example"}}); err == nil {
		t.Fatal("foreign origin accepted")
	}
	if c, _ := e.do("POST", "/api/v1/sessions/"+s.id+"/join", "", nil); c != 400 {
		t.Fatalf("empty join body: %d", c)
	}
	ok, err := e.dial(s.id, http.Header{"Origin": {e.ts.URL}})
	if err != nil {
		t.Fatalf("own origin rejected: %v", err)
	}
	ok.c.CloseNow()

	tok := e.mustJoin(s, "Ana")
	c := e.connect(s.id, tok)
	c.sendRaw([]byte(`{not json`))
	if m := c.next("error"); errCode(m) != "INVALID_MESSAGE" {
		t.Fatal(m.payload)
	}
	c.sendRaw([]byte(`{"protocolVersion":"1.0","type":"command.request","messageId":"4f4f3838-cdf9-45aa-a786-000000000001","sessionId":"` + s.id + `","payload":{"commandId":"x","action":"presentation.next"}}`))
	if m := c.next("error"); errCode(m) != "INVALID_MESSAGE" || m.CorrelationID != "4f4f3838-cdf9-45aa-a786-000000000001" {
		t.Fatal(m)
	}
	c.sendRaw([]byte(`{"protocolVersion":"2.0","type":"ping","messageId":"4f4f3838-cdf9-45aa-a786-000000000002","payload":{}}`))
	if m := c.next("error"); errCode(m) != "UNSUPPORTED_VERSION" {
		t.Fatal(m.payload)
	}
	// oversized frame closes the connection (message too big)
	c.sendRaw([]byte(`{"x":"` + strings.Repeat("a", 20000) + `"}`))
	if st := c.waitClosed(); st != websocket.StatusMessageTooBig {
		t.Fatalf("oversize close %d", st)
	}
	// unsupported major version at auth
	w3, _ := e.dial(s.id, nil)
	w3.sendRaw([]byte(`{"protocolVersion":"2.0","type":"auth.hello","messageId":"4f4f3838-cdf9-45aa-a786-000000000003","sessionId":"` + s.id + `","payload":{"token":"abcdefghijklmnopqrstu"}}`))
	if m := w3.next("auth.error"); errCode(m) != "UNSUPPORTED_VERSION" {
		t.Fatal(m.payload)
	}
	if st := w3.waitClosed(); st != protocol.CloseUnsupported {
		t.Fatalf("close %d", st)
	}
}

// Criterion 4: two speakers, total order, correlated results.
func TestTwoSpeakersOrderedAndCorrelated(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(nil)
	h, _ := e.connectHost(s)
	a := e.controller(s, "Ana")
	b := e.controller(s, "Beto")
	idA, msgA := a.command("presentation.next", nil)
	accA := a.next("command.accepted")
	idB, _ := b.command("presentation.previous", nil)
	accB := b.next("command.accepted")
	if accA.CorrelationID != msgA || accA.payload["commandId"] != idA {
		t.Fatal("accepted not correlated")
	}
	sa, sb := accA.payload["sequence"].(float64), accB.payload["sequence"].(float64)
	if sb != sa+1 {
		t.Fatalf("sequences %v %v", sa, sb)
	}
	d1 := executeOne(t, h)
	d2 := executeOne(t, h)
	if d1.payload["commandId"] != idA || d2.payload["commandId"] != idB {
		t.Fatal("dispatch order differs from acceptance order")
	}
	if d1.payload["issuedBy"].(map[string]any)["displayName"] != "Ana" {
		t.Fatal("issuedBy not set by relay")
	}
	ra := a.next("command.result")
	rb := b.next("command.result")
	if ra.payload["commandId"] != idA || rb.payload["commandId"] != idB || ra.payload["status"] != "executed" {
		t.Fatalf("results %v %v", ra.payload, rb.payload)
	}
	a.none(150*time.Millisecond, "command.result") // A never sees B's result
}

// Criterion 5: same commandId never repeats the effect; a different payload is rejected.
func TestDeduplication(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(nil)
	h, _ := e.connectHost(s)
	a := e.controller(s, "Ana")
	id, _ := a.command("presentation.next", nil)
	a.next("command.accepted")
	executeOne(t, h)
	a.next("command.result")
	// exact retry: same id, same sequence, same payload
	a.send("command.request", map[string]any{"commandId": id, "clientSequence": 1, "action": "presentation.next", "parameters": map[string]any{}, "catalogRevision": 1})
	a.next("command.accepted")
	if r := a.next("command.result"); r.payload["status"] != "executed" {
		t.Fatal(r.payload)
	}
	h.none(200*time.Millisecond, "command.dispatch")
	// same id, different payload
	a.send("command.request", map[string]any{"commandId": id, "clientSequence": 1, "action": "presentation.previous", "parameters": map[string]any{}, "catalogRevision": 1})
	if r := a.next("command.rejected"); errCode(r) != "COMMAND_ID_CONFLICT" {
		t.Fatal(r.payload)
	}
	// unknown id with an old clientSequence (e.g. evicted from cache) is never executed
	a.send("command.request", map[string]any{"commandId": "11111111-2222-4333-8444-555555555555", "clientSequence": 1, "action": "presentation.next", "parameters": map[string]any{}, "catalogRevision": 1})
	if r := a.next("command.rejected"); errCode(r) != "COMMAND_EXPIRED" {
		t.Fatal(r.payload)
	}
	h.none(200*time.Millisecond, "command.dispatch")
	// status query does not re-execute
	a.send("command.status.request", map[string]any{"commandId": id})
	if st := a.next("command.status"); st.payload["status"] != "executed" || st.payload["found"] != true {
		t.Fatal(st.payload)
	}
	h.none(100*time.Millisecond, "command.dispatch")
}

// Criterion 6: host offline rejects immediately; reconnecting does not replay.
func TestHostOfflineAndNoReplay(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(nil)
	a := e.controller(s, "Ana")
	a.command("presentation.next", nil)
	if r := a.next("command.rejected"); errCode(r) != "HOST_OFFLINE" {
		t.Fatal(r.payload)
	}
	h, epoch1 := e.connectHost(s)
	h.c.CloseNow()
	time.Sleep(100 * time.Millisecond)
	a.command("presentation.next", nil)
	if r := a.next("command.rejected"); errCode(r) != "HOST_OFFLINE" {
		t.Fatal(r.payload)
	}
	h2, epoch2 := e.connectHost(s)
	if epoch2 <= epoch1 {
		t.Fatalf("epoch did not increase: %d %d", epoch1, epoch2)
	}
	h2.none(300*time.Millisecond, "command.dispatch")
	a.command("presentation.next", nil)
	a.next("command.accepted")
	if d := h2.next("command.dispatch"); int64(d.payload["epoch"].(float64)) != epoch2 {
		t.Fatal("dispatch with old epoch")
	}
}

// Criterion 7: crash after execution and before ack -> explicit unknown, no automatic retry.
func TestCrashBeforeAckIsUnknown(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(nil)
	h, _ := e.connectHost(s)
	a := e.controller(s, "Ana")
	id, _ := a.command("presentation.next", nil)
	a.next("command.accepted")
	h.next("command.dispatch")
	h.c.CloseNow() // "executed" locally, never acknowledged
	r := a.next("command.result")
	if r.payload["status"] != "unknown" || errCode(r) != "EXECUTION_UNKNOWN" || r.payload["commandId"] != id {
		t.Fatal(r.payload)
	}
	h2, _ := e.connectHost(s)
	h2.none(300*time.Millisecond, "command.dispatch")
	// a late result from the old epoch can not change anything
	a.send("command.status.request", map[string]any{"commandId": id})
	if st := a.next("command.status"); st.payload["status"] != "unknown" {
		t.Fatal(st.payload)
	}
}

// No result in time -> unknown, and a late result does not flip it.
func TestResultTimeoutIsUnknown(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.CommandValidFor = 100 * time.Millisecond; c.ResultGrace = 100 * time.Millisecond })
	s := e.create(nil)
	h, _ := e.connectHost(s)
	a := e.controller(s, "Ana")
	a.command("presentation.next", nil)
	d := h.next("command.dispatch")
	if r := a.next("command.result"); r.payload["status"] != "unknown" {
		t.Fatal(r.payload)
	}
	h.send("command.result", map[string]any{"commandId": d.payload["commandId"], "sequence": d.payload["sequence"], "epoch": d.payload["epoch"], "status": "executed"})
	a.none(200*time.Millisecond, "command.result")
}

// Criterion 8: pause, revocation, password rotation and closing.
func TestPauseRevokeRotateClose(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(nil)
	h, _ := e.connectHost(s)
	a := e.controller(s, "Ana")
	b := e.controller(s, "Beto")

	e.do("PATCH", "/api/v1/sessions/"+s.id, s.hostToken, map[string]any{"paused": true})
	h.next("session.updated")
	a.command("presentation.next", nil)
	if r := a.next("command.rejected"); errCode(r) != "SESSION_PAUSED" {
		t.Fatal(r.payload)
	}
	e.do("PATCH", "/api/v1/sessions/"+s.id, s.hostToken, map[string]any{"paused": false})

	// revoke B: socket closed with reason, token dead
	_, st := e.do("GET", "/api/v1/sessions/"+s.id+"/participants", s.hostToken, nil)
	var bid string
	for _, p := range st["participants"].([]any) {
		if p.(map[string]any)["displayName"] == "Beto" {
			bid = p.(map[string]any)["participantId"].(string)
		}
	}
	if c, _ := e.do("PATCH", "/api/v1/sessions/"+s.id+"/participants/"+bid, s.hostToken, map[string]any{"state": "revoked"}); c != 200 {
		t.Fatal(c)
	}
	if m := b.next("session.closed"); m.payload["reason"] != "revoked" {
		t.Fatal(m.payload)
	}
	if st := b.waitClosed(); st != protocol.CloseRevoked {
		t.Fatalf("close %d", st)
	}

	// disable A: commands denied, re-enable works
	_, st = e.do("GET", "/api/v1/sessions/"+s.id+"/participants", s.hostToken, nil)
	aid := st["participants"].([]any)[0].(map[string]any)["participantId"].(string)
	e.do("PATCH", "/api/v1/sessions/"+s.id+"/participants/"+aid, s.hostToken, map[string]any{"state": "disabled"})
	a.command("presentation.next", nil)
	if r := a.next("command.rejected"); errCode(r) != "PERMISSION_DENIED" {
		t.Fatal(r.payload)
	}
	e.do("PATCH", "/api/v1/sessions/"+s.id+"/participants/"+aid, s.hostToken, map[string]any{"state": "enabled"})
	a.command("presentation.next", nil)
	a.next("command.accepted")
	executeOne(t, h)
	a.next("command.result")

	// rotation: all speakers out, host keeps authority, old password useless
	c, out := e.do("POST", "/api/v1/sessions/"+s.id+"/password", s.hostToken, map[string]any{})
	if c != 200 || out["revokedParticipants"].(float64) != 1 {
		t.Fatal(c, out)
	}
	if m := a.next("session.closed"); m.payload["reason"] != "password-rotated" {
		t.Fatal(m.payload)
	}
	if c, _ := e.join(s, "Ana", s.password); c != 401 {
		t.Fatal("old password still works")
	}
	newPw := out["generatedPassword"].(string)
	if c, _ := e.join(s, "Ana", newPw); c != 200 {
		t.Fatal("new password does not work")
	}
	if c, _ := e.do("GET", "/api/v1/sessions/"+s.id+"/participants", s.hostToken, nil); c != 200 {
		t.Fatal("host lost authority")
	}

	// close: everything invalid
	if c, _ := e.do("DELETE", "/api/v1/sessions/"+s.id, s.hostToken, nil); c != 204 {
		t.Fatal(c)
	}
	if m := h.next("session.closed"); m.payload["reason"] != "host-ended" {
		t.Fatal(m.payload)
	}
	if c, _ := e.do("GET", "/api/v1/sessions/"+s.id+"/participants", s.hostToken, nil); c != 401 {
		t.Fatal("host token survived close")
	}
	if c, _ := e.join(s, "Ana", newPw); c != 401 {
		t.Fatal("join after close")
	}
}

func TestApprovalAndCapabilities(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(map[string]any{"requireApproval": true})
	h, _ := e.connectHost(s)
	c, out := e.join(s, "Ana", s.password)
	if c != 200 || out["state"] != "pending" {
		t.Fatal(c, out)
	}
	pid := out["participantId"].(string)
	if pu := h.next("participant.updated"); pu.payload["participant"].(map[string]any)["state"] != "pending" {
		t.Fatal(pu.payload)
	}
	a := &controller{wsc: e.connect(s.id, out["token"].(string))}
	a.command("presentation.next", nil)
	if r := a.next("command.rejected"); errCode(r) != "PERMISSION_DENIED" {
		t.Fatal(r.payload)
	}
	e.do("PATCH", "/api/v1/sessions/"+s.id+"/participants/"+pid, s.hostToken, map[string]any{"state": "enabled"})
	if pu := a.next("participant.updated"); pu.payload["participant"].(map[string]any)["state"] != "enabled" {
		t.Fatal(pu.payload)
	}
	a.command("presentation.next", nil)
	a.next("command.accepted")
	executeOne(t, h)
	// capability not granted: presentation.goto needs presentation.jump
	a.command("presentation.goto", map[string]any{"slide": 3})
	if r := a.next("command.rejected"); errCode(r) != "PERMISSION_DENIED" {
		t.Fatal(r.payload)
	}
	e.do("PATCH", "/api/v1/sessions/"+s.id+"/participants/"+pid, s.hostToken, map[string]any{"capabilities": []string{"presentation.navigate", "presentation.jump"}})
	a.command("presentation.goto", map[string]any{"slide": 0})
	if r := a.next("command.rejected"); errCode(r) != "INVALID_MESSAGE" {
		t.Fatal(r.payload)
	}
	a.command("presentation.goto", map[string]any{"slide": 3})
	a.next("command.accepted")
	if d := executeOne(t, h); d.payload["parameters"].(map[string]any)["slide"].(float64) != 3 {
		t.Fatal(d.payload)
	}
	// unknown action / stale catalog
	a.command("system.shutdown", nil)
	if r := a.next("command.rejected"); errCode(r) != "UNSUPPORTED_ACTION" {
		t.Fatal(r.payload)
	}
	a.seq++
	a.send("command.request", map[string]any{"commandId": "21111111-2222-4333-8444-555555555555", "clientSequence": a.seq, "action": "presentation.next", "parameters": map[string]any{}, "catalogRevision": 7})
	if r := a.next("command.rejected"); errCode(r) != "STALE_CATALOG" {
		t.Fatal(r.payload)
	}
}

func TestHostFencing(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(nil)
	h1, _ := e.connectHost(s)
	h2, _ := e.connectHost(s)
	if st := h1.waitClosed(); st != protocol.CloseSuperseded {
		t.Fatalf("old host close %d", st)
	}
	a := e.controller(s, "Ana")
	a.command("presentation.next", nil)
	a.next("command.accepted")
	executeOne(t, h2)
	if r := a.next("command.result"); r.payload["status"] != "executed" {
		t.Fatal(r.payload)
	}
}

func TestCreationTokenPolicyAndLoginBackoff(t *testing.T) {
	e := newEnv(t, func(c *Config) {
		c.SessionCreation = "token"
		c.CreateToken = "0123456789abcdef-create"
		c.LoginFreeFailures = 2
	})
	if c, _ := e.do("POST", "/api/v1/sessions", "", map[string]any{}); c != 401 {
		t.Fatalf("creation without token: %d", c)
	}
	c, out := e.do("POST", "/api/v1/sessions", "0123456789abcdef-create", map[string]any{})
	if c != 201 {
		t.Fatal(c, out)
	}
	s := session{id: out["sessionId"].(string), password: out["generatedPassword"].(string)}
	e.join(s, "M", "bad1")
	e.join(s, "M", "bad2")
	e.join(s, "M", "bad3")
	if c, out := e.join(s, "M", s.password); c != 429 || out["error"].(map[string]any)["retryAfterMs"] == nil {
		t.Fatalf("expected lockout even with right password: %d %v", c, out)
	}
}

func TestInsecureConfigRefused(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen = "0.0.0.0:8080"
	cfg.PublicURL = "http://relay.example.org"
	if cfg.Finalize() == nil {
		t.Fatal("http public URL without dev flag accepted")
	}
	cfg.DevInsecure = true
	if cfg.Finalize() == nil {
		t.Fatal("dev insecure on non-loopback accepted")
	}
}

func TestWebAndSecurityHeaders(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/", "/s/k3J9sX2bQp7LmN4vT8wYzA", "/assets/app.js"} {
		res, err := http.Get(e.ts.URL + p)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("%s: %v %v", p, err, res.Status)
		}
		csp := res.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("%s: CSP %q", p, csp)
		}
		if res.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("referrer policy")
		}
	}
	res, _ := http.Post(e.ts.URL+"/", "text/plain", nil)
	if res.StatusCode != 405 {
		t.Fatalf("POST / = %d", res.StatusCode)
	}
	if c, out := e.do("GET", "/api/v1/nope", "", nil); c != 404 || out["error"] == nil {
		t.Fatal(c, out)
	}
	if c, out := e.do("GET", "/healthz", "", nil); c != 200 || out["status"] != "ok" {
		t.Fatal(c, out)
	}
}

// The first two messages after auth.hello are always auth.ok and session.snapshot.
func TestAuthMessageOrder(t *testing.T) {
	e := newEnv(t, nil)
	s := e.create(nil)
	e.connectHost(s)
	tok := e.mustJoin(s, "Ana")
	w, _ := e.dial(s.id, nil)
	w.send("auth.hello", map[string]any{"token": tok})
	var got []string
	for len(got) < 2 {
		select {
		case m := <-w.in:
			got = append(got, m.Type)
		case <-time.After(3 * time.Second):
			t.Fatal("timeout")
		}
	}
	if got[0] != "auth.ok" || got[1] != "session.snapshot" {
		t.Fatalf("order %v", got)
	}
}

func TestSetPublicURL(t *testing.T) {
	e := newEnv(t, nil)
	old := e.srv.PublicURL()
	if err := e.srv.SetPublicURL("https://new-tunnel.example.net"); err != nil {
		t.Fatal(err)
	}
	if e.srv.originAllowed(old) || !e.srv.originAllowed("https://new-tunnel.example.net") {
		t.Fatal("origins not swapped")
	}
	_, out := e.do("POST", "/api/v1/sessions", "", map[string]any{})
	if !strings.HasPrefix(out["joinUrl"].(string), "https://new-tunnel.example.net/s/") || out["wsUrl"] != "wss://new-tunnel.example.net/api/v1/ws" {
		t.Fatal(out)
	}
	if err := e.srv.SetPublicURL("ftp://x"); err == nil {
		t.Fatal("invalid URL accepted")
	}
}
