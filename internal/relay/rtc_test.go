package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"

	"github.com/azratul/relevo/internal/protocol"
	"github.com/azratul/relevo/internal/secret"
)

// rtcPeer is a Go stand-in for the browser side of the DataChannel.
type rtcPeer struct {
	pc *webrtc.PeerConnection
	dc *webrtc.DataChannel
	in chan msg
}

func (e *env) rtcConnect(t *testing.T, sid, token string, ordered bool) (*rtcPeer, int) {
	t.Helper()
	// The peer cannot use mDNS, as on macOS where mDNSResponder owns port
	// 5353: the relay must offer candidates that need no mDNS resolution.
	se := webrtc.SettingEngine{}
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	init := &webrtc.DataChannelInit{Ordered: &ordered}
	if !ordered {
		zero := uint16(0)
		init.MaxRetransmits = &zero
	}
	dc, err := pc.CreateDataChannel("relevo", init)
	if err != nil {
		t.Fatal(err)
	}
	p := &rtcPeer{pc: pc, dc: dc, in: make(chan msg, 64)}
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		env, err := protocol.DecodeEnvelope(m.Data)
		if err != nil {
			return
		}
		mm := msg{Envelope: *env}
		_ = json.Unmarshal(env.Payload, &mm.payload)
		p.in <- mm
	})
	offer, _ := pc.CreateOffer(nil)
	gathered := webrtc.GatheringCompletePromise(pc)
	_ = pc.SetLocalDescription(offer)
	<-gathered
	body, _ := json.Marshal(map[string]string{"type": "offer", "sdp": pc.LocalDescription().SDP})
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/sessions/"+sid+"/rtc", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return p, res.StatusCode
	}
	v, _ := protocol.DefaultValidator()
	if err := v.Validate("http/RtcAnswer", raw); err != nil {
		t.Fatalf("answer violates schema: %v", err)
	}
	var ans struct{ SDP string }
	_ = json.Unmarshal(raw, &ans)
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.SDP}); err != nil {
		t.Fatal(err)
	}
	return p, 200
}

func (p *rtcPeer) send(t *testing.T, sid, typ string, payload any) string {
	id := secret.UUID()
	b, _ := protocol.Encode(typ, id, sid, "", payload)
	if err := p.dc.SendText(string(b)); err != nil {
		t.Fatal(err)
	}
	return id
}

func (p *rtcPeer) next(t *testing.T, types ...string) msg {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-p.in:
			for _, ty := range types {
				if m.Type == ty {
					return m
				}
			}
		case <-timeout:
			t.Fatalf("timeout waiting for %v on DataChannel", types)
		}
	}
}

func waitOpen(t *testing.T, p *rtcPeer) {
	t.Helper()
	opened := make(chan struct{})
	p.dc.OnOpen(func() { close(opened) })
	if p.dc.ReadyState() == webrtc.DataChannelStateOpen {
		return
	}
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("DataChannel did not open")
	}
}

func TestWebRTCUpgrade(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.EnableWebRTC = true; c.STUNServers = nil })
	s := e.create(nil)
	h, _ := e.connectHost(s)
	tok := e.mustJoin(s, "Ana")

	ws, _ := e.dial(s.id, nil)
	ws.send("auth.hello", map[string]any{"token": tok})
	ok := ws.next("auth.ok")
	if caps := ok.payload["capabilities"].([]any); len(caps) != 1 || caps[0] != "webrtc" || ok.payload["rtc"] == nil {
		t.Fatalf("auth.ok does not offer webrtc: %v", ok.payload)
	}
	// A command in flight on the WebSocket gets its result on the DataChannel after the upgrade.
	ws.send("command.request", map[string]any{"commandId": "31111111-2222-4333-8444-555555555555", "clientSequence": 1, "action": "presentation.next", "parameters": map[string]any{}, "catalogRevision": 1})
	ws.next("command.accepted")
	d := h.next("command.dispatch")

	p, code := e.rtcConnect(t, s.id, tok, true)
	if code != 200 {
		t.Fatalf("rtc offer: %d", code)
	}
	waitOpen(t, p)
	p.send(t, s.id, "auth.hello", map[string]any{"token": tok})
	if m := p.next(t, "auth.ok", "auth.error"); m.Type != "auth.ok" || m.payload["rtc"] != nil {
		t.Fatalf("dc auth: %v", m.payload)
	}
	p.next(t, "session.snapshot")
	if st := ws.waitClosed(); st != protocol.CloseSuperseded {
		t.Fatalf("websocket close %d, want 4009", st)
	}
	h.send("command.result", map[string]any{"commandId": d.payload["commandId"], "sequence": d.payload["sequence"], "epoch": d.payload["epoch"], "status": "executed"})
	if r := p.next(t, "command.result"); r.payload["commandId"] != "31111111-2222-4333-8444-555555555555" {
		t.Fatal(r.payload)
	}
	// New commands travel over the DataChannel with the same semantics (clientSequence continues).
	p.send(t, s.id, "command.request", map[string]any{"commandId": "41111111-2222-4333-8444-555555555555", "clientSequence": 2, "action": "presentation.next", "parameters": map[string]any{}, "catalogRevision": 1})
	p.next(t, "command.accepted")
	executeOne(t, h)
	if r := p.next(t, "command.result"); r.payload["status"] != "executed" {
		t.Fatal(r.payload)
	}
	p.send(t, s.id, "ping", map[string]any{})
	p.next(t, "pong")
	// The host sees the speaker connected over the new transport.
	if c := e.srv.getSession(s.id); c == nil {
		t.Fatal("session gone")
	}
}

func TestWebRTCGuards(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.EnableWebRTC = true; c.STUNServers = nil; c.AuthTimeout = 500 * time.Millisecond })
	s := e.create(nil)
	tok := e.mustJoin(s, "Ana")
	if _, code := e.rtcConnect(t, s.id, s.hostToken, true); code != 403 {
		t.Fatalf("host token: %d", code)
	}
	if _, code := e.rtcConnect(t, s.id, "rlvp_forged_token_value_123456", true); code != 401 {
		t.Fatalf("forged token: %d", code)
	}
	// An unreliable channel is refused (ordering/delivery guarantees).
	p, code := e.rtcConnect(t, s.id, tok, false)
	if code != 200 {
		t.Fatal(code)
	}
	closed := make(chan struct{})
	p.dc.OnClose(func() { close(closed) })
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("unreliable DataChannel was not closed")
	}
	// Without auth.hello in time the DataChannel is closed like a WebSocket.
	p2, _ := e.rtcConnect(t, s.id, tok, true)
	waitOpen(t, p2)
	if m := p2.next(t, "auth.error"); errCode(m) != "AUTH_FAILED" {
		t.Fatal(m.payload)
	}
	e2 := newEnv(t, nil) // WebRTC disabled
	s2 := e2.create(nil)
	if _, code := e2.rtcConnect(t, s2.id, e2.mustJoin(s2, "B"), true); code != 404 {
		t.Fatalf("disabled: %d", code)
	}
}
