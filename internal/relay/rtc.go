package relay

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"

	"github.com/azratul/relevo/internal/protocol"
)

// WebRTC DataChannel transport (ADR-0014). Signalling is a single HTTP
// exchange (offer -> answer, non-trickle ICE) authenticated with the
// participant token. The DataChannel then carries the same envelopes as the
// WebSocket, starting again with auth.hello; a successful auth supersedes the
// participant's WebSocket (close 4009), which the official controller expects.

const (
	rtcLabel       = "relevo"
	maxSDP         = 32 * 1024
	rtcOpenTimeout = 20 * time.Second
	rtcGatherWait  = 3 * time.Second
)

func (s *Server) rtcAPI() *webrtc.API {
	s.rtcOnce.Do(func() {
		se := webrtc.SettingEngine{}
		// Fast failure detection; consent freshness keeps the path alive.
		se.SetICETimeouts(4*time.Second, 8*time.Second, 2*time.Second)
		// Resolve the browsers' mDNS .local candidates, but announce real IPs:
		// gathering .local names needs our own mDNS responder on port 5353,
		// which mDNSResponder owns on macOS (the names would never resolve).
		se.SetICEMulticastDNSMode(ice.MulticastDNSModeQueryOnly)
		if os.Getenv("RELEVO_TEST_RTC_NO_ROUTE") == "1" {
			// Test hook: offer no candidates, as if no network path existed, so ICE
			// fails and controllers must stay on the tunnel (E2E NO_P2P=1).
			se.SetIPFilter(func(net.IP) bool { return false })
			se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
		}
		s.rtc = webrtc.NewAPI(webrtc.WithSettingEngine(se))
	})
	return s.rtc
}

func (s *Server) iceServers() []webrtc.ICEServer {
	var out []webrtc.ICEServer
	for _, u := range s.cfg.STUNServers {
		out = append(out, webrtc.ICEServer{URLs: []string{u}})
	}
	return out
}

func (s *Server) handleRTC(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.EnableWebRTC {
		writeErr(w, protocol.NewError(protocol.NotFound, "WebRTC is not enabled on this relay"))
		return
	}
	pr, e := s.authenticate(r)
	if e != nil {
		writeErr(w, e)
		return
	}
	if pr.host {
		writeErr(w, protocol.NewError(protocol.Forbidden, "WebRTC is offered to controllers only"))
		return
	}
	if ok, wait := s.rtcLimit.Allow(pr.p.id); !ok {
		writeErr(w, rateErr(wait))
		return
	}
	var req struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}
	if e := s.readBodyLimit(r, "http/RtcOffer", false, &req, maxSDP+1024); e != nil {
		writeErr(w, e)
		return
	}
	ip := s.clientIP(r)
	pc, err := s.rtcAPI().NewPeerConnection(webrtc.Configuration{ICEServers: s.iceServers()})
	if err != nil {
		writeErr(w, protocol.NewError(protocol.InternalError, "Cannot create peer connection"))
		return
	}
	opened := make(chan struct{})
	var openOnce sync.Once
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != rtcLabel || !dc.Ordered() || dc.MaxRetransmits() != nil || dc.MaxPacketLifeTime() != nil {
			_ = dc.Close() // only a reliable, ordered channel preserves the protocol guarantees
			return
		}
		t := newDCTransport(pc, dc, s.cfg.MaxMessageBytes)
		dc.OnOpen(func() {
			openOnce.Do(func() { close(opened) })
			if !s.admitConn(ip) {
				_ = pc.Close()
				return
			}
			go func() {
				s.serveConn(t, ip, func() { s.connMu.Lock(); s.unauth--; s.connMu.Unlock() })
				s.connMu.Lock()
				if s.connsByIP[ip]--; s.connsByIP[ip] <= 0 {
					delete(s.connsByIP, ip)
				}
				s.connMu.Unlock()
			}()
		})
	})
	go func() {
		select {
		case <-opened:
		case <-time.After(rtcOpenTimeout):
			_ = pc.Close() // never opened: free the resources
		}
	}()
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: req.SDP}); err != nil {
		_ = pc.Close()
		writeErr(w, protocol.NewError(protocol.InvalidMessage, "Invalid SDP offer"))
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		writeErr(w, protocol.NewError(protocol.InvalidMessage, "Cannot answer this offer"))
		return
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		writeErr(w, protocol.NewError(protocol.InternalError, "Cannot set local description"))
		return
	}
	select {
	case <-gathered:
	case <-time.After(rtcGatherWait): // answer with whatever candidates we have
	}
	s.log.Info("webrtc offer answered", "session", shortID(pr.sess.id), "participant", pr.p.id)
	writeJSON(w, http.StatusOK, map[string]string{"type": "answer", "sdp": pc.LocalDescription().SDP})
}

// dcTransport adapts a DataChannel to the transport interface.
type dcTransport struct {
	pc     *webrtc.PeerConnection
	dc     *webrtc.DataChannel
	in     chan []byte
	dead   chan struct{}
	once   sync.Once
	binary bool
}

func newDCTransport(pc *webrtc.PeerConnection, dc *webrtc.DataChannel, max int64) *dcTransport {
	t := &dcTransport{pc: pc, dc: dc, in: make(chan []byte, 64), dead: make(chan struct{})}
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if !m.IsString {
			t.binary = true
			t.kill()
			return
		}
		if int64(len(m.Data)) > max {
			t.kill() // like WebSocket 1009: oversized messages end the connection
			return
		}
		select {
		case t.in <- m.Data:
		default:
			t.kill() // flooding faster than we read
		}
	})
	dc.OnClose(t.kill)
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed || st == webrtc.PeerConnectionStateDisconnected {
			t.kill()
		}
	})
	return t
}

func (t *dcTransport) kill() { t.once.Do(func() { close(t.dead) }) }

func (t *dcTransport) Read(ctx context.Context) ([]byte, error) {
	select {
	case b := <-t.in:
		return b, nil
	case <-t.dead:
		select {
		case b := <-t.in: // drain what already arrived
			return b, nil
		default:
		}
		if t.binary {
			return nil, errBinary
		}
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *dcTransport) Write(ctx context.Context, b []byte) error {
	select {
	case <-t.dead:
		return io.ErrClosedPipe
	default:
	}
	return t.dc.SendText(string(b))
}

// Close flushes what is queued (DataChannels have no close codes; the relay
// always sends session.closed/auth.error before closing) and tears down.
func (t *dcTransport) Close(int, string) error {
	deadline := time.Now().Add(time.Second)
	for t.dc.BufferedAmount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	return t.CloseNow()
}

func (t *dcTransport) CloseNow() error {
	t.kill()
	_ = t.dc.Close()
	return t.pc.Close()
}

func (t *dcTransport) Ping(ctx context.Context) error {
	if t.pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
		return errors.New("peer connection not connected")
	}
	return nil
}

func (t *dcTransport) Kind() string { return "webrtc" }
