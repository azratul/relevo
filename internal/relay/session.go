package relay

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"relevo/internal/auth"
	"relevo/internal/catalog"
	"relevo/internal/protocol"
	"relevo/internal/ratelimit"
)

type cmdRecord struct {
	commandID     string
	requestMsgID  string // messageId of the command.request, used as correlationId
	participantID string
	clientSeq     int64
	hash          [32]byte
	sequence      int64
	epoch         int64
	status        string
	confirmation  string
	err           *protocol.Error
	relayRejected bool // rejected by the relay before dispatch
	timer         *time.Timer
}

type participant struct {
	id            string
	name          string
	state         string
	caps          []string
	joinedAt      time.Time
	expiresAt     time.Time
	tokenHash     [32]byte
	conn          *conn
	lastClientSeq int64
	recent        map[string]*cmdRecord
	recentOrder   []string
	cmdLimiter    *ratelimit.Limiter
}

func (p *participant) remember(r *cmdRecord, max int) {
	p.recent[r.commandID] = r
	p.recentOrder = append(p.recentOrder, r.commandID)
	for len(p.recentOrder) > max {
		delete(p.recent, p.recentOrder[0])
		p.recentOrder = p.recentOrder[1:]
	}
}

func (p *participant) view() protocol.Participant {
	return protocol.Participant{
		ParticipantID: p.id, DisplayName: p.name, State: p.state, Connected: p.conn != nil,
		Capabilities: slices.Clone(p.caps), JoinedAt: p.joinedAt.UTC().Format(time.RFC3339),
	}
}

type hostState struct {
	conn            *conn
	epoch           int64
	agentInstanceID string
	ready           bool
	reason          string
	target          protocol.Target
	catalog         *catalog.Compiled
}

// Session is the in-memory state of one control session (ADR-0009).
type Session struct {
	mu              sync.Mutex
	srv             *Server
	id              string
	pw              auth.Hash
	hostTokenHash   [32]byte
	hostConn        *conn // authenticated host connection (registered or not)
	createdAt       time.Time
	expiresAt       time.Time
	closed          bool
	paused          bool
	requireApproval bool
	authzRev        int64
	defaultCaps     []string
	participants    map[string]*participant
	byToken         map[[32]byte]*participant
	host            *hostState // registered executor; nil if none
	epochCounter    int64
	seq             int64
	inflight        map[int64]*cmdRecord
	hostOffline     time.Time // when the executor went away (zero if online)
}

func payloadHash(req *protocol.CommandRequest) [32]byte {
	var params any
	_ = json.Unmarshal(req.Parameters, &params)
	canon, _ := json.Marshal(struct {
		A string
		P any
		R int64
		S int64
	}{req.Action, params, req.CatalogRevision, req.ClientSequence})
	return sha256.Sum256(canon)
}

// view builds the role-filtered snapshot. Caller holds s.mu.
func (s *Session) view(p *participant) protocol.SessionView {
	v := protocol.SessionView{
		SessionID: s.id, Status: "open", Paused: s.paused, RequireApproval: s.requireApproval,
		AuthzRevision: s.authzRev, ExpiresAt: s.expiresAt.UTC().Format(time.RFC3339),
	}
	if s.closed {
		v.Status = "closed"
	}
	if s.host != nil && s.host.conn != nil {
		t := s.host.target
		v.Host = protocol.HostStatus{Online: true, Ready: s.host.ready, ReadinessReason: s.host.reason, Target: &t, Epoch: s.host.epoch}
		if s.host.catalog != nil {
			c := s.host.catalog.Raw
			v.Catalog = &c
		}
	}
	if p != nil {
		pv := p.view()
		v.You = &pv
	} else {
		v.Participants = []protocol.Participant{}
		for _, q := range s.sortedParticipants() {
			v.Participants = append(v.Participants, q.view())
		}
	}
	return v
}

func (s *Session) sortedParticipants() []*participant {
	ps := make([]*participant, 0, len(s.participants))
	for _, p := range s.participants {
		ps = append(ps, p)
	}
	slices.SortFunc(ps, func(a, b *participant) int { return a.joinedAt.Compare(b.joinedAt) })
	return ps
}

// broadcast sends session.updated to every connection. Caller holds s.mu.
func (s *Session) broadcast() {
	if s.hostConn != nil {
		s.hostConn.sendMsg(protocol.TypeSessionUpdated, "", s.view(nil))
	}
	for _, p := range s.participants {
		if p.conn != nil {
			p.conn.sendMsg(protocol.TypeSessionUpdated, "", s.view(p))
		}
	}
}

// notifyParticipant sends participant.updated to the host and to p. Caller holds s.mu.
func (s *Session) notifyParticipant(p *participant) {
	msg := protocol.ParticipantUpdated{Participant: p.view(), AuthzRevision: s.authzRev}
	if s.hostConn != nil {
		s.hostConn.sendMsg(protocol.TypeParticipantUpdated, "", msg)
	}
	if p.conn != nil {
		p.conn.sendMsg(protocol.TypeParticipantUpdated, "", msg)
	}
}

// revokeLocked invalidates a participant token and closes its connection.
func (s *Session) revokeLocked(p *participant, reason string) {
	if p.state == protocol.StateRevoked {
		return
	}
	p.state = protocol.StateRevoked
	delete(s.byToken, p.tokenHash)
	s.authzRev++
	s.notifyParticipant(p)
	if p.conn != nil {
		p.conn.sendMsg(protocol.TypeSessionClosed, "", protocol.SessionClosedPayload{Reason: reason})
		p.conn.closeAfterFlush(protocol.CloseRevoked, reason)
		p.conn = nil
	}
}

// failInflight marks all dispatched commands of the current executor unknown.
func (s *Session) failInflight(msg string) {
	for seq, r := range s.inflight {
		s.finish(r, protocol.StatusUnknown, "", protocol.NewError(protocol.ExecutionUnknown, msg))
		delete(s.inflight, seq)
	}
}

// finish sets a terminal status and notifies the issuer. Caller holds s.mu.
func (s *Session) finish(r *cmdRecord, status, confirmation string, e *protocol.Error) {
	if protocol.Terminal(r.status) {
		return
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.status, r.confirmation, r.err = status, confirmation, e
	if p := s.participants[r.participantID]; p != nil && p.conn != nil {
		p.conn.sendMsg(protocol.TypeCommandResult, r.requestMsgID, protocol.CommandResult{
			CommandID: r.commandID, Sequence: r.sequence, Status: status, Confirmation: confirmation, Error: e,
		})
	}
}

// closeLocked terminates the session and every credential.
func (s *Session) closeLocked(reason string) {
	if s.closed {
		return
	}
	s.closed = true
	s.failInflight("session closed before the host confirmed")
	conns := []*conn{s.hostConn}
	for _, p := range s.participants {
		conns = append(conns, p.conn)
		p.conn = nil
	}
	for _, c := range conns {
		if c != nil {
			c.sendMsg(protocol.TypeSessionClosed, "", protocol.SessionClosedPayload{Reason: reason})
			c.closeAfterFlush(protocol.CloseSessionClosed, "session closed")
		}
	}
	s.hostConn, s.host = nil, nil
	s.byToken = map[[32]byte]*participant{}
}
