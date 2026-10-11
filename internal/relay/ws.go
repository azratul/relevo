package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"relevo/internal/protocol"
	"relevo/internal/ratelimit"
	"relevo/internal/secret"
)

type outMsg struct {
	data      []byte
	closeCode int
	reason    string
}

// errBinary is returned by transport.Read for non-text messages.
var errBinary = errors.New("binary message")

// transport carries protocol envelopes (one JSON text message each). The
// protocol is the same over every transport (WebSocket, WebRTC DataChannel).
type transport interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, b []byte) error
	Close(code int, reason string) error
	CloseNow() error
	Ping(ctx context.Context) error
	Kind() string
}

type wsTransport struct{ c *websocket.Conn }

func (t wsTransport) Read(ctx context.Context) ([]byte, error) {
	typ, b, err := t.c.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, errBinary
	}
	return b, nil
}
func (t wsTransport) Write(ctx context.Context, b []byte) error {
	return t.c.Write(ctx, websocket.MessageText, b)
}
func (t wsTransport) Close(code int, reason string) error {
	return t.c.Close(websocket.StatusCode(code), reason)
}
func (t wsTransport) CloseNow() error                { return t.c.CloseNow() }
func (t wsTransport) Ping(ctx context.Context) error { return t.c.Ping(ctx) }
func (t wsTransport) Kind() string                   { return "websocket" }

// conn is one authenticated (or authenticating) peer. All writes go through out (single writer).
type conn struct {
	srv     *Server
	ws      transport
	id      string
	ip      string
	role    string // "controller" or "host" after auth
	sess    *Session
	p       *participant
	out     chan outMsg
	ctx     context.Context
	cancel  context.CancelFunc
	closing atomic.Bool
	authed  atomic.Bool
	limiter *ratelimit.Limiter
	strikes int
	done    chan struct{}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (c *conn) sendMsg(typ, corr string, payload any) {
	sid := ""
	if c.sess != nil {
		sid = c.sess.id
	}
	data, err := protocol.Encode(typ, secret.UUID(), sid, corr, payload)
	if err != nil {
		c.srv.log.Error("encode", "type", typ, "err", err)
		return
	}
	if c.srv.cfg.ValidateOutbound {
		if schema := protocol.OutboundSchema(c.role, typ); schema != "" {
			if err := c.srv.validator.Validate(schema, data); err != nil {
				c.srv.OutboundViolations.Add(1)
				c.srv.log.Error("outbound message violates schema", "type", typ, "role", c.role, "err", err)
			}
		}
	}
	c.push(outMsg{data: data})
}

func (c *conn) sendError(code protocol.Code, msg, corr string) {
	if !uuidRe.MatchString(corr) {
		corr = ""
	}
	c.sendMsg(protocol.TypeError, corr, protocol.ErrorPayload{Error: *protocol.NewError(code, msg)})
}

func (c *conn) push(m outMsg) {
	if c.closing.Load() && m.closeCode == 0 {
		return
	}
	select {
	case c.out <- m:
	default:
		c.srv.log.Warn("closing slow connection", "conn", c.id)
		c.cancel()
	}
}

// closeAfterFlush closes the socket after previously queued messages are written.
func (c *conn) closeAfterFlush(code int, reason string) {
	if c.closing.Swap(true) {
		return
	}
	select {
	case c.out <- outMsg{closeCode: code, reason: reason}:
	default:
		c.cancel()
	}
}

func (c *conn) writer() {
	defer close(c.done)
	for {
		select {
		case m := <-c.out:
			if m.closeCode != 0 {
				_ = c.ws.Close(m.closeCode, m.reason)
				c.cancel()
				return
			}
			wctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			err := c.ws.Write(wctx, m.data)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		case <-c.ctx.Done():
			_ = c.ws.CloseNow()
			return
		}
	}
}

// pinger checks liveness (WebSocket ping frames; for DataChannels, the ICE/SCTP state).
func (c *conn) pinger() {
	t := time.NewTicker(c.srv.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(c.ctx, c.srv.cfg.HeartbeatTimeout)
			err := c.ws.Ping(ctx)
			cancel()
			if err != nil {
				c.srv.log.Info("heartbeat timeout", "conn", c.id, "role", c.role)
				c.cancel()
				return
			}
		}
	}
}

func (s *Server) admitConn(ip string) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.unauth >= s.cfg.MaxUnauthConns || s.connsByIP[ip] >= s.cfg.MaxConnsPerIP {
		return false
	}
	s.unauth++
	s.connsByIP[ip]++
	return true
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Exact Origin allow list for browsers. Native clients send no Origin and
	// still need a credential: Origin is not authentication.
	if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
		s.log.Info("ws origin rejected", "origin", o)
		writeErr(w, protocol.NewError(protocol.Forbidden, "Origin not allowed"))
		return
	}
	ip := s.clientIP(r)
	if !s.admitConn(ip) {
		writeErr(w, rateErr(5*time.Second))
		return
	}
	unauthHeld := true
	defer func() {
		s.connMu.Lock()
		if unauthHeld {
			s.unauth--
		}
		if s.connsByIP[ip]--; s.connsByIP[ip] <= 0 {
			delete(s.connsByIP, ip)
		}
		s.connMu.Unlock()
	}()
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Origin already checked above against the exact list
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	ws.SetReadLimit(s.cfg.MaxMessageBytes)
	s.serveConn(wsTransport{ws}, ip, func() {
		s.connMu.Lock()
		s.unauth--
		unauthHeld = false
		s.connMu.Unlock()
	})
}

// serveConn runs the lifecycle of one connection on any transport:
// authentication in the first message, then the read loop. authDone is
// called once the authentication phase ends (successfully or not).
func (s *Server) serveConn(t transport, ip string, authDone func()) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{
		srv: s, ws: t, id: secret.ConnectionID(), ip: ip, out: make(chan outMsg, 128), ctx: ctx, cancel: cancel,
		limiter: ratelimit.New(s.cfg.MaxMessagesPerSecond, int(s.cfg.MaxMessagesPerSecond*2), 1),
		done:    make(chan struct{}),
	}
	go c.writer()
	timer := time.AfterFunc(s.cfg.AuthTimeout, func() {
		if !c.authed.Load() {
			c.sendMsg(protocol.TypeAuthError, "", protocol.ErrorPayload{Error: *protocol.NewError(protocol.AuthFailed, "Authentication timeout")})
			c.closeAfterFlush(protocol.CloseAuthFailed, "auth timeout")
		}
	})
	ok := s.authPhase(c)
	timer.Stop()
	if authDone != nil {
		authDone()
	}
	if ok {
		go c.pinger()
		c.readLoop()
		s.unregister(c)
	}
	cancelAfter := time.AfterFunc(6*time.Second, cancel) // bound the close handshake
	<-c.done
	cancelAfter.Stop()
	cancel()
}

func (s *Server) authFail(c *conn, code protocol.Code, msg string, close int) bool {
	c.sendMsg(protocol.TypeAuthError, "", protocol.ErrorPayload{Error: *protocol.NewError(code, msg)})
	c.closeAfterFlush(close, string(code))
	return false
}

func (s *Server) authPhase(c *conn) bool {
	data, err := c.ws.Read(c.ctx)
	if errors.Is(err, errBinary) {
		return s.authFail(c, protocol.InvalidMessage, "Text frames only", protocol.CloseProtocolError)
	}
	if err != nil {
		return false
	}
	env, err := protocol.DecodeEnvelope(data)
	if err != nil {
		return s.authFail(c, protocol.InvalidMessage, "Malformed message", protocol.CloseProtocolError)
	}
	if env.Type != protocol.TypeAuthHello {
		return s.authFail(c, protocol.InvalidMessage, "The first message must be auth.hello", protocol.CloseAuthFailed)
	}
	if !protocol.Compatible(env.ProtocolVersion) {
		return s.authFail(c, protocol.UnsupportedVer, "Unsupported protocol version; this relay speaks "+protocol.Version, protocol.CloseUnsupported)
	}
	if err := s.validator.Validate("ws/auth.hello", data); err != nil {
		return s.authFail(c, protocol.InvalidMessage, "Invalid auth.hello: "+err.Error(), protocol.CloseProtocolError)
	}
	var hello protocol.AuthHello
	_ = json.Unmarshal(env.Payload, &hello)
	sess := s.getSession(env.SessionID)
	if sess == nil {
		return s.authFail(c, protocol.AuthFailed, "Authentication failed", protocol.CloseAuthFailed)
	}
	h := secret.HashToken(hello.Token)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return s.authFail(c, protocol.AuthFailed, "Authentication failed", protocol.CloseAuthFailed)
	}
	ok := protocol.AuthOK{
		ConnectionID: c.id, ProtocolVersion: protocol.Version, Capabilities: s.capabilities(),
		Heartbeat: protocol.Heartbeat{IntervalMs: s.cfg.HeartbeatInterval.Milliseconds(), TimeoutMs: (s.cfg.HeartbeatInterval + s.cfg.HeartbeatTimeout).Milliseconds()},
		Limits:    s.limits(),
	}
	c.sess = sess
	if secret.Equal(h, sess.hostTokenHash) {
		c.role = "host"
		ok.Role = "host"
		if old := sess.hostConn; old != nil {
			old.sendError(protocol.Forbidden, "Another host connection took over", "")
			old.closeAfterFlush(protocol.CloseSuperseded, "superseded")
			if sess.host != nil && sess.host.conn == old {
				sess.failInflight("host connection replaced before confirming")
				sess.host = nil
				sess.hostOffline = time.Now()
				sess.broadcast()
			}
		}
		sess.hostConn = c
	} else {
		p := sess.byToken[h]
		if p == nil {
			c.sess = nil
			return s.authFail(c, protocol.AuthFailed, "Authentication failed", protocol.CloseAuthFailed)
		}
		if time.Now().After(p.expiresAt) {
			c.sess = nil
			return s.authFail(c, protocol.AuthExpired, "Credential expired, join again", protocol.CloseAuthFailed)
		}
		c.role, c.p = "controller", p
		ok.Role, ok.ParticipantID = "controller", p.id
		if s.cfg.EnableWebRTC && c.ws.Kind() != "webrtc" {
			ok.RTC = &protocol.RTCInfo{ICEServers: []protocol.ICEServer{}}
			for _, u := range s.cfg.STUNServers {
				ok.RTC.ICEServers = append(ok.RTC.ICEServers, protocol.ICEServer{URLs: []string{u}})
			}
		}
		if old := p.conn; old != nil {
			old.closeAfterFlush(protocol.CloseSuperseded, "superseded")
		}
		p.conn = c
	}
	// Order is part of the contract: auth.ok, then session.snapshot, then updates.
	c.authed.Store(true)
	c.sendMsg(protocol.TypeAuthOK, env.MessageID, ok)
	c.sendMsg(protocol.TypeSessionSnapshot, "", sess.view(c.p))
	if c.p != nil {
		sess.notifyParticipant(c.p) // tells the host the speaker is connected
	}
	s.log.Info("authenticated", "session", shortID(sess.id), "role", c.role, "conn", c.id, "transport", c.ws.Kind())
	return true
}

func (c *conn) readLoop() {
	s := c.srv
	for {
		data, err := c.ws.Read(c.ctx)
		if errors.Is(err, errBinary) {
			c.closeAfterFlush(protocol.CloseProtocolError, "text frames only")
			return
		}
		if err != nil {
			return
		}
		if ok, _ := c.limiter.Allow(""); !ok {
			c.strikes++
			c.sendError(protocol.RateLimited, "Too many messages", "")
			if c.strikes > 20 {
				c.closeAfterFlush(protocol.ClosePolicy, "rate limit")
				return
			}
			continue
		}
		env, err := protocol.DecodeEnvelope(data)
		if err != nil {
			c.sendError(protocol.InvalidMessage, "Malformed message", "")
			continue
		}
		if !protocol.Compatible(env.ProtocolVersion) {
			c.sendError(protocol.UnsupportedVer, "Unsupported protocol version", env.MessageID)
			continue
		}
		schema, allowed := protocol.InboundSchema(c.role, env.Type)
		if schema == "" {
			c.sendError(protocol.InvalidMessage, "Unknown message type", env.MessageID)
			continue
		}
		if !allowed {
			c.sendError(protocol.Forbidden, "This role may not send "+env.Type, env.MessageID)
			continue
		}
		if err := s.validator.Validate(schema, data); err != nil {
			c.sendError(protocol.InvalidMessage, err.Error(), env.MessageID)
			continue
		}
		if env.Type != protocol.TypePing && env.SessionID != c.sess.id {
			c.sendError(protocol.InvalidMessage, "sessionId does not match the authenticated session", env.MessageID)
			continue
		}
		switch env.Type {
		case protocol.TypePing:
			c.sendMsg(protocol.TypePong, env.MessageID, protocol.Empty{})
		case protocol.TypeAuthHello:
			c.sendError(protocol.InvalidMessage, "Already authenticated", env.MessageID)
		case protocol.TypeCommandRequest:
			var req protocol.CommandRequest
			_ = json.Unmarshal(env.Payload, &req)
			c.sess.handleCommand(c, env.MessageID, &req)
		case protocol.TypeCommandStatusRequest:
			var req protocol.CommandStatusRequest
			_ = json.Unmarshal(env.Payload, &req)
			c.sess.handleStatus(c, env.MessageID, req.CommandID)
		case protocol.TypeHostRegister:
			var req protocol.HostRegister
			_ = json.Unmarshal(env.Payload, &req)
			c.sess.handleRegister(c, env.MessageID, &req)
		case protocol.TypeHostUpdate:
			var req protocol.HostUpdate
			_ = json.Unmarshal(env.Payload, &req)
			c.sess.handleHostUpdate(c, env.MessageID, &req)
		case protocol.TypeCommandResult:
			var res protocol.HostCommandResult
			_ = json.Unmarshal(env.Payload, &res)
			c.sess.handleResult(c, env.MessageID, &res)
		}
		if c.closing.Load() {
			return
		}
	}
}

func (s *Server) unregister(c *conn) {
	ss := c.sess
	if ss == nil {
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	switch c.role {
	case "host":
		if ss.hostConn == c {
			ss.hostConn = nil
		}
		if ss.host != nil && ss.host.conn == c {
			ss.failInflight("host disconnected before confirming; the action may or may not have run")
			ss.host = nil
			ss.hostOffline = time.Now()
			ss.broadcast()
		}
	case "controller":
		if c.p.conn == c {
			c.p.conn = nil
			if c.p.state != protocol.StateRevoked {
				ss.notifyParticipant(c.p)
			}
		}
	}
	s.log.Info("connection closed", "session", shortID(ss.id), "role", c.role, "conn", c.id, "transport", c.ws.Kind())
}
