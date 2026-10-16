package hostagent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/azratul/relevo/internal/adapter"
	"github.com/azratul/relevo/internal/auth"
	"github.com/azratul/relevo/internal/protocol"
	"github.com/azratul/relevo/internal/relay"
	"github.com/azratul/relevo/internal/secret"
)

// proxy forwards TCP and can sever every connection (network failure).
type proxy struct {
	ln     net.Listener
	target string
	mu     sync.Mutex
	conns  []net.Conn
}

func newProxy(t *testing.T, target string) *proxy {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{ln: ln, target: target}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", target)
			if err != nil {
				c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, u)
			p.mu.Unlock()
			go func() { io.Copy(u, c); u.Close() }()
			go func() { io.Copy(c, u); c.Close() }()
		}
	}()
	t.Cleanup(func() { ln.Close(); p.kill() })
	return p
}

func (p *proxy) kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.Close()
	}
	p.conns = nil
}

type fixture struct {
	t     *testing.T
	agent *Agent
	sim   *adapter.Sim
	prox  *proxy // between the host and its embedded relay: lets tests cut that link
	sess  *SessionInfo
	base  string // embedded relay base URL the speakers use (stands in for the tunnel)
}

func newFixture(t *testing.T) *fixture {
	port := freePort(t)
	prox := newProxy(t, "127.0.0.1:"+strconv.Itoa(port))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hc := DefaultConfig()
	hc.Backend = "sim"
	hc.Target = adapter.Target{Class: "relevo-sim"}
	hc.Connection = Connection{Mode: ModeURL, PublicURL: "https://relevo.invalid", LocalPort: port, STUNServers: []string{}}
	a, err := New(hc, "", "test", log)
	if err != nil {
		t.Fatal(err)
	}
	a.dialOverride = func(string) string { return "ws://" + prox.ln.Addr().String() + "/api/v1/ws" }
	a.relayTweak = func(c *relay.Config) {
		c.ValidateOutbound = true
		c.Argon2 = auth.Params{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, MaxConcurrent: 2}
	}
	sess, err := a.CreateSession(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	srv := a.emb.srv
	t.Cleanup(func() {
		if n := srv.OutboundViolations.Load(); n != 0 {
			t.Errorf("%d schema violations", n)
		}
		a.Close(context.Background())
	})
	f := &fixture{t: t, agent: a, sim: a.Keyboard().Backend().(*adapter.Sim), prox: prox, sess: sess, base: a.emb.base}
	f.waitConnected()
	return f
}

func (f *fixture) waitConnected() {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f.agent.Status().Connected {
		if time.Now().After(deadline) {
			f.t.Fatal("agent did not connect: " + f.agent.Status().ConnError)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// speaker is a minimal controller (direct relay URL, like the browser).
type speaker struct {
	t   *testing.T
	ws  *websocket.Conn
	sid string
	seq int64
	in  chan map[string]any
	rev int64
}

func (f *fixture) speaker(name string) *speaker {
	f.t.Helper()
	body := strings.NewReader(`{"displayName":"` + name + `","password":"` + f.sess.Password + `"}`)
	res, err := http.Post(f.base+"/api/v1/sessions/"+f.sess.ID+"/join", "application/json", body)
	if err != nil || res.StatusCode != 200 {
		f.t.Fatalf("join: %v %v", err, res.Status)
	}
	var jr struct{ Token string }
	json.NewDecoder(res.Body).Decode(&jr)
	ws, _, err := websocket.Dial(context.Background(), strings.Replace(f.base, "http", "ws", 1)+"/api/v1/ws", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	s := &speaker{t: f.t, ws: ws, sid: f.sess.ID, in: make(chan map[string]any, 64)}
	go func() {
		for {
			_, data, err := ws.Read(context.Background())
			if err != nil {
				close(s.in)
				return
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			s.in <- m
		}
	}()
	s.send("auth.hello", map[string]any{"token": jr.Token})
	snap := s.next("session.snapshot")
	s.rev = int64(snap["payload"].(map[string]any)["catalog"].(map[string]any)["revision"].(float64))
	f.t.Cleanup(func() { ws.CloseNow() })
	return s
}

func (s *speaker) send(typ string, payload any) {
	b, _ := protocol.Encode(typ, secret.UUID(), s.sid, "", payload)
	s.ws.Write(context.Background(), websocket.MessageText, b)
}

func (s *speaker) next(types ...string) map[string]any {
	s.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-s.in:
			if !ok {
				s.t.Fatal("closed")
			}
			for _, t := range types {
				if m["type"] == t {
					return m
				}
			}
		case <-timeout:
			s.t.Fatalf("timeout waiting %v", types)
		}
	}
}

func (s *speaker) command(action string) map[string]any {
	s.t.Helper()
	s.seq++
	s.send("command.request", map[string]any{"commandId": secret.UUID(), "clientSequence": s.seq, "action": action, "parameters": map[string]any{}, "catalogRevision": s.rev})
	m := s.next("command.rejected", "command.result")
	return m["payload"].(map[string]any)
}

func errorCode(p map[string]any) string {
	if e, ok := p["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

// Milestone 2 vertical slice: two speakers control the (simulated) target.
func TestVerticalSliceTwoSpeakers(t *testing.T) {
	f := newFixture(t)
	a, b := f.speaker("Ana"), f.speaker("Beto")
	if r := a.command("presentation.next"); r["status"] != "executed" || r["confirmation"] != "simulated" {
		t.Fatal(r)
	}
	if r := b.command("presentation.previous"); r["status"] != "executed" {
		t.Fatal(r)
	}
	if got := f.sim.Sent(); len(got) != 2 || got[0] != "Right" || got[1] != "Left" {
		t.Fatal(got)
	}
	// Focus moves to another window: the host refuses (criterion 9, sim backend).
	f.sim.SetFocused(false)
	if r := a.command("presentation.next"); r["status"] != "rejected" || errorCode(r) != "TARGET_NOT_ACTIVE" {
		t.Fatal(r)
	}
	f.sim.SetFocused(true)
	// Local pause takes effect immediately.
	if err := f.agent.SetPaused(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if r := a.command("presentation.next"); errorCode(r) != "SESSION_PAUSED" {
		t.Fatal(r)
	}
	f.agent.SetPaused(context.Background(), false)
	if len(f.sim.Sent()) != 2 {
		t.Fatal("paused or unfocused commands were executed")
	}
}

// Criterion 7 with the real agent: the network dies right after the key was
// injected and before the result reaches the relay.
func TestCrashAfterExecuteBeforeAck(t *testing.T) {
	f := newFixture(t)
	a := f.speaker("Ana")
	var once sync.Once
	f.sim.SetOnSend(func(adapter.Window, adapter.Chord) { once.Do(f.prox.kill) })
	r := a.command("presentation.next")
	if r["status"] != "unknown" || errorCode(r) != "EXECUTION_UNKNOWN" {
		t.Fatal(r)
	}
	f.sim.SetOnSend(nil)
	// The agent reconnects (new epoch) and must not execute anything again.
	time.Sleep(300 * time.Millisecond)
	f.waitConnected()
	time.Sleep(300 * time.Millisecond)
	if n := len(f.sim.Sent()); n != 1 {
		t.Fatalf("executed %d times", n)
	}
	// New commands work in the new epoch.
	if r := a.command("presentation.next"); r["status"] != "executed" {
		t.Fatal(r)
	}
	if st := f.agent.Status(); st.Epoch < 2 {
		t.Fatalf("epoch %d", st.Epoch)
	}
}

// Criterion 6 with the real agent: host offline -> immediate rejection, no replay.
func TestHostOfflineThenReconnect(t *testing.T) {
	f := newFixture(t)
	a := f.speaker("Ana")
	f.prox.ln.Close() // relay unreachable for the host
	f.prox.kill()
	deadline := time.Now().Add(3 * time.Second)
	for {
		r := a.command("presentation.next")
		if errorCode(r) == "HOST_OFFLINE" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never offline", r)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(f.sim.Sent()); n > 1 {
		t.Fatalf("executed %d", n)
	}
}

// Revocation in the host mirror: a dispatch from a participant the host knows
// is disabled is refused even if the relay sent it.
func TestHostRevalidates(t *testing.T) {
	f := newFixture(t)
	f.agent.mu.Lock()
	gen, epoch := f.agent.gen, f.agent.epoch
	f.agent.participants["p_AAAAAAAAAAAAAAAA"] = protocol.Participant{ParticipantID: "p_AAAAAAAAAAAAAAAA", State: protocol.StateDisabled, Capabilities: []string{"presentation.navigate"}}
	rev := f.agent.cat.Revision
	f.agent.mu.Unlock()
	f.agent.execute(job{gen: gen, received: time.Now(), d: protocol.CommandDispatch{
		CommandID: secret.UUID(), Sequence: 99, Epoch: epoch, Action: "presentation.next", Parameters: json.RawMessage(`{}`),
		CatalogRevision: rev, IssuedBy: protocol.IssuedBy{ParticipantID: "p_AAAAAAAAAAAAAAAA", DisplayName: "x"}, ValidForMs: 3000,
	}})
	if len(f.sim.Sent()) != 0 {
		t.Fatal("disabled participant executed")
	}
	// expired in queue
	f.agent.execute(job{gen: gen, received: time.Now().Add(-5 * time.Second), d: protocol.CommandDispatch{
		CommandID: secret.UUID(), Sequence: 100, Epoch: epoch, Action: "presentation.next", CatalogRevision: rev,
		IssuedBy: protocol.IssuedBy{ParticipantID: "p_AAAAAAAAAAAAAAAA"}, ValidForMs: 3000,
	}})
	h := f.agent.Status().History
	if len(h) != 2 || h[0].Detail != "PERMISSION_DENIED" || h[1].Status != "expired" {
		t.Fatalf("%+v", h)
	}
}

func TestPasswordRotationKeepsHost(t *testing.T) {
	f := newFixture(t)
	a := f.speaker("Ana")
	old := f.sess.Password
	pw, err := f.agent.RotatePassword(context.Background(), "")
	if err != nil || pw == old {
		t.Fatal(err)
	}
	if m := a.next("session.closed"); m["payload"].(map[string]any)["reason"] != "password-rotated" {
		t.Fatal(m)
	}
	f.sess.Password = pw
	b := f.speaker("Beto")
	if r := b.command("presentation.next"); r["status"] != "executed" {
		t.Fatal(r)
	}
}

func TestCatalogChangeIsStale(t *testing.T) {
	f := newFixture(t)
	a := f.speaker("Ana")
	cfg := f.agent.Config()
	cfg.Profile = "presentation-full"
	if err := f.agent.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a.next("session.updated")
	if r := a.command("presentation.next"); errorCode(r) != "STALE_CATALOG" {
		t.Fatal(r)
	}
}

func TestEndSession(t *testing.T) {
	f := newFixture(t)
	a := f.speaker("Ana")
	if err := f.agent.EndSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := a.next("session.closed"); m["payload"].(map[string]any)["reason"] != "host-ended" {
		t.Fatal(m)
	}
	if f.agent.Session() != nil {
		t.Fatal("session still active")
	}
}
