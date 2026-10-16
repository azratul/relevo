package relay

import (
	"slices"
	"time"

	"github.com/azratul/relevo/internal/catalog"
	"github.com/azratul/relevo/internal/protocol"
)

// handleCommand validates and routes a command.request (docs/protocol.md#comandos).
// All of it runs under the session lock, which gives a total order per session.
func (s *Session) handleCommand(c *conn, msgID string, req *protocol.CommandRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := c.p
	reject := func(code protocol.Code, msg string) {
		c.sendMsg(protocol.TypeCommandRejected, msgID, protocol.CommandRejected{CommandID: req.CommandID, Error: *protocol.NewError(code, msg)})
	}
	if s.closed || p.conn != c {
		reject(protocol.SessionClosed, "Session closed")
		return
	}
	h := payloadHash(req)
	// 1. Deduplication (ADR-0013): same id -> same answer, never a second dispatch.
	if r, ok := p.recent[req.CommandID]; ok {
		if r.hash != h {
			reject(protocol.CommandIDConflict, "This commandId was already used with a different payload")
			return
		}
		r.requestMsgID = msgID
		switch {
		case r.relayRejected:
			c.sendMsg(protocol.TypeCommandRejected, msgID, protocol.CommandRejected{CommandID: r.commandID, Error: *r.err})
		case protocol.Terminal(r.status):
			c.sendMsg(protocol.TypeCommandAccepted, msgID, protocol.CommandAccepted{CommandID: r.commandID, Sequence: r.sequence})
			c.sendMsg(protocol.TypeCommandResult, msgID, protocol.CommandResult{CommandID: r.commandID, Sequence: r.sequence, Status: r.status, Confirmation: r.confirmation, Error: r.err})
		default:
			c.sendMsg(protocol.TypeCommandAccepted, msgID, protocol.CommandAccepted{CommandID: r.commandID, Sequence: r.sequence})
		}
		return
	}
	// 2. Unknown id with an old clientSequence: may be an evicted command. Never execute.
	if req.ClientSequence <= p.lastClientSeq {
		reject(protocol.CommandExpired, "Stale clientSequence; send a new command")
		return
	}
	p.lastClientSeq = req.ClientSequence
	r := &cmdRecord{commandID: req.CommandID, requestMsgID: msgID, participantID: p.id, clientSeq: req.ClientSequence, hash: h}
	p.remember(r, s.srv.cfg.DedupRetention)
	fail := func(code protocol.Code, msg string) {
		r.status, r.relayRejected, r.err = protocol.StatusRejected, true, protocol.NewError(code, msg)
		c.sendMsg(protocol.TypeCommandRejected, msgID, protocol.CommandRejected{CommandID: r.commandID, Error: *r.err})
	}
	// 3. Authorization and readiness, evaluated now (not at login).
	switch p.state {
	case protocol.StatePending:
		fail(protocol.PermissionDenied, "Waiting for the host to approve you")
		return
	case protocol.StateDisabled, protocol.StateRevoked:
		fail(protocol.PermissionDenied, "The host has not enabled you")
		return
	}
	if ok, _ := p.cmdLimiter.Allow(""); !ok {
		fail(protocol.RateLimited, "Too many commands, slow down")
		return
	}
	if s.paused {
		fail(protocol.SessionPaused, "The host paused control")
		return
	}
	if s.host == nil || s.host.conn == nil {
		fail(protocol.HostOffline, "The host is not connected")
		return
	}
	if !s.host.ready || s.host.catalog == nil {
		fail(protocol.HostNotReady, "The host is not ready: "+s.host.reason)
		return
	}
	if req.CatalogRevision != s.host.catalog.Revision {
		fail(protocol.StaleCatalog, "The action catalog changed; refresh")
		return
	}
	a := s.host.catalog.Actions[req.Action]
	if a == nil {
		fail(protocol.UnsupportedAction, "Action not offered by the host")
		return
	}
	if !slices.Contains(p.caps, a.Capability) {
		fail(protocol.PermissionDenied, "Missing capability "+a.Capability)
		return
	}
	if err := a.ValidateParams(req.Parameters); err != nil {
		fail(protocol.InvalidMessage, err.Error())
		return
	}
	if len(s.inflight) >= s.srv.cfg.MaxInFlight {
		fail(protocol.RateLimited, "Too many commands waiting for the host")
		return
	}
	// 4. Accept and dispatch. Fields below are set by the relay, never by the controller.
	s.seq++
	r.sequence, r.epoch, r.status = s.seq, s.host.epoch, protocol.StatusDispatched
	s.inflight[r.sequence] = r
	params := req.Parameters
	if len(params) == 0 {
		params = []byte("{}")
	}
	c.sendMsg(protocol.TypeCommandAccepted, msgID, protocol.CommandAccepted{CommandID: r.commandID, Sequence: r.sequence})
	validFor := s.srv.cfg.CommandValidFor
	s.host.conn.sendMsg(protocol.TypeCommandDispatch, "", protocol.CommandDispatch{
		CommandID: r.commandID, Sequence: r.sequence, Epoch: r.epoch, Action: req.Action, Parameters: params,
		CatalogRevision: req.CatalogRevision, AuthzRevision: s.authzRev,
		IssuedBy:   protocol.IssuedBy{ParticipantID: p.id, DisplayName: p.name},
		ValidForMs: validFor.Milliseconds(),
	})
	// If no result arrives within budget + grace, the outcome is unknown (never retried).
	r.timer = time.AfterFunc(validFor+s.srv.cfg.ResultGrace, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.inflight[r.sequence] == r {
			delete(s.inflight, r.sequence)
			s.finish(r, protocol.StatusUnknown, "", protocol.NewError(protocol.ExecutionUnknown, "No result from the host in time; the action may or may not have run"))
		}
	})
}

func (s *Session) handleStatus(c *conn, msgID, commandID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := c.p.recent[commandID]
	st := protocol.CommandStatus{CommandID: commandID, Found: r != nil}
	if r != nil {
		st.Status, st.Sequence, st.Confirmation, st.Error = r.status, r.sequence, r.confirmation, r.err
	}
	c.sendMsg(protocol.TypeCommandStatus, msgID, st)
}

func (s *Session) handleRegister(c *conn, msgID string, req *protocol.HostRegister) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hostConn != c {
		c.sendError(protocol.Forbidden, "Connection superseded", msgID)
		return
	}
	cat, err := catalog.Compile(req.Catalog)
	if err != nil {
		c.sendError(protocol.InvalidMessage, "Invalid catalog: "+err.Error(), msgID)
		return
	}
	// New epoch: anything dispatched under the previous one is now unknown and fenced.
	s.failInflight("host re-registered before confirming")
	s.epochCounter++
	s.host = &hostState{
		conn: c, epoch: s.epochCounter, agentInstanceID: req.AgentInstanceID,
		ready: req.Ready, reason: req.ReadinessReason, target: req.Target, catalog: cat,
	}
	s.hostOffline = time.Time{}
	c.sendMsg(protocol.TypeHostRegistered, msgID, protocol.HostRegistered{Epoch: s.host.epoch})
	s.broadcast()
	s.srv.log.Info("host registered", "session", shortID(s.id), "epoch", s.host.epoch, "actions", len(cat.Actions), "ready", req.Ready)
}

func (s *Session) handleHostUpdate(c *conn, msgID string, req *protocol.HostUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host == nil || s.host.conn != c {
		c.sendError(protocol.Forbidden, "Send host.register first", msgID)
		return
	}
	if req.Catalog != nil {
		if req.Catalog.Revision <= s.host.catalog.Revision {
			c.sendError(protocol.InvalidMessage, "Catalog revision must increase", msgID)
			return
		}
		cat, err := catalog.Compile(*req.Catalog)
		if err != nil {
			c.sendError(protocol.InvalidMessage, "Invalid catalog: "+err.Error(), msgID)
			return
		}
		s.host.catalog = cat
	}
	if req.Ready != nil {
		s.host.ready = *req.Ready
	}
	if req.ReadinessReason != nil {
		s.host.reason = *req.ReadinessReason
	}
	if req.Ready != nil && *req.Ready && req.ReadinessReason == nil {
		s.host.reason = ""
	}
	if req.Target != nil {
		s.host.target = *req.Target
	}
	s.broadcast()
}

func (s *Session) handleResult(c *conn, msgID string, res *protocol.HostCommandResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host == nil || s.host.conn != c || res.Epoch != s.host.epoch {
		s.srv.log.Info("result from stale epoch ignored", "session", shortID(s.id), "epoch", res.Epoch)
		return
	}
	r := s.inflight[res.Sequence]
	if r == nil {
		// Late result after the command was declared unknown: terminal states never change.
		s.srv.log.Info("late or unknown result ignored", "session", shortID(s.id), "sequence", res.Sequence, "status", res.Status)
		return
	}
	if r.commandID != res.CommandID {
		c.sendError(protocol.InvalidMessage, "commandId does not match sequence", msgID)
		return
	}
	delete(s.inflight, res.Sequence)
	e := res.Error
	if e != nil && res.Status == protocol.StatusExecuted {
		e = nil
	}
	if e == nil && res.Status != protocol.StatusExecuted {
		e = protocol.NewError(protocol.ExecutionFailed, "The host could not execute the action")
	}
	status := res.Status
	if status == protocol.StatusFailed && e.Code == protocol.ExecutionUnknown {
		status = protocol.StatusUnknown // input may have been partially injected
	}
	s.finish(r, status, res.Confirmation, e)
}
