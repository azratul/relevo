package relay

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/azratul/relevo/internal/auth"
	"github.com/azratul/relevo/internal/protocol"
	"github.com/azratul/relevo/internal/ratelimit"
	"github.com/azratul/relevo/internal/secret"
)

const maxBody = 8 * 1024

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, e *protocol.Error) {
	if e.RetryAfterMs > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt((e.RetryAfterMs+999)/1000, 10))
	}
	if e.Code == protocol.AuthFailed || e.Code == protocol.AuthExpired {
		w.Header().Set("WWW-Authenticate", `Bearer realm="relevo"`)
	}
	writeJSON(w, protocol.HTTPStatus(e.Code), protocol.ErrorPayload{Error: *e})
}

func rateErr(wait time.Duration) *protocol.Error {
	e := protocol.NewError(protocol.RateLimited, "Too many requests, try again later")
	e.RetryAfterMs = max(wait.Milliseconds(), 1000)
	return e
}

// readBody reads and schema-validates a JSON body. Empty bodies count as {} when allowEmpty.
func (s *Server) readBody(r *http.Request, schema string, allowEmpty bool, dst any) *protocol.Error {
	return s.readBodyLimit(r, schema, allowEmpty, dst, maxBody)
}

func (s *Server) readBodyLimit(r *http.Request, schema string, allowEmpty bool, dst any, limit int64) *protocol.Error {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, limit))
	if err != nil {
		return protocol.NewError(protocol.InvalidMessage, "Body too large or unreadable")
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		if !allowEmpty {
			return protocol.NewError(protocol.InvalidMessage, "Body required")
		}
		raw = []byte("{}")
	} else {
		mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mt != "application/json" {
			return protocol.NewError(protocol.InvalidMessage, "Content-Type must be application/json")
		}
	}
	if err := s.validator.Validate(schema, raw); err != nil {
		return protocol.NewError(protocol.InvalidMessage, "Invalid body: "+err.Error())
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return protocol.NewError(protocol.InvalidMessage, "Invalid body")
	}
	return nil
}

// checkOrigin rejects browser requests from origins not in the exact allow list.
// Requests without Origin (native clients) pass; the credential is still required.
func (s *Server) checkOrigin(r *http.Request) *protocol.Error {
	if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
		return protocol.NewError(protocol.Forbidden, "Origin not allowed")
	}
	return nil
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// principal is the authenticated caller of an HTTP request.
type principal struct {
	sess *Session
	host bool
	p    *participant
}

// authenticate resolves the bearer token for the session in the path.
// It never reveals whether the session exists.
func (s *Server) authenticate(r *http.Request) (*principal, *protocol.Error) {
	if e := s.checkOrigin(r); e != nil {
		return nil, e
	}
	tok := bearer(r)
	sess := s.getSession(r.PathValue("id"))
	if tok == "" || sess == nil {
		return nil, protocol.NewError(protocol.AuthFailed, "Authentication failed")
	}
	h := secret.HashToken(tok)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return nil, protocol.NewError(protocol.AuthFailed, "Authentication failed")
	}
	if secret.Equal(h, sess.hostTokenHash) {
		return &principal{sess: sess, host: true}, nil
	}
	p := sess.byToken[h]
	if p == nil {
		return nil, protocol.NewError(protocol.AuthFailed, "Authentication failed")
	}
	if time.Now().After(p.expiresAt) {
		return nil, protocol.NewError(protocol.AuthExpired, "Credential expired, join again")
	}
	return &principal{sess: sess, p: p}, nil
}

func (s *Server) requireHost(w http.ResponseWriter, r *http.Request) *principal {
	pr, e := s.authenticate(r)
	if e != nil {
		writeErr(w, e)
		return nil
	}
	if !pr.host {
		writeErr(w, protocol.NewError(protocol.Forbidden, "Only the host may do this"))
		return nil
	}
	return pr
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) limits() protocol.Limits {
	return protocol.Limits{
		MaxMessageBytes: s.cfg.MaxMessageBytes, MaxMessagesPerSecond: s.cfg.MaxMessagesPerSecond,
		CommandValidForMs: s.cfg.CommandValidFor.Milliseconds(), MaxInFlightCommands: s.cfg.MaxInFlight,
	}
}

func (s *Server) capabilities() []string {
	if s.cfg.EnableWebRTC {
		return []string{"webrtc"}
	}
	return []string{}
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"protocolVersions": []string{protocol.Version}, "appVersion": s.cfg.AppVersion,
		"sessionCreation": s.cfg.SessionCreation, "limits": s.limits(), "capabilities": s.capabilities(),
	})
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if e := s.checkOrigin(r); e != nil {
		writeErr(w, e)
		return
	}
	ip := s.clientIP(r)
	if s.cfg.SessionCreation == "token" {
		if subtle.ConstantTimeCompare([]byte(bearer(r)), []byte(s.cfg.CreateToken)) != 1 {
			writeErr(w, protocol.NewError(protocol.AuthFailed, "Session creation requires a valid creation token"))
			return
		}
	}
	if ok, wait := s.createIP.Allow(ip); !ok {
		writeErr(w, rateErr(wait))
		return
	}
	var req struct {
		Password            string   `json:"password"`
		RequireApproval     bool     `json:"requireApproval"`
		DefaultCapabilities []string `json:"defaultCapabilities"`
		TTLSeconds          int64    `json:"ttlSeconds"`
	}
	if e := s.readBody(r, "http/CreateSessionRequest", true, &req); e != nil {
		writeErr(w, e)
		return
	}
	s.mu.RLock()
	n := len(s.sessions)
	s.mu.RUnlock()
	if n >= s.cfg.MaxSessions {
		writeErr(w, rateErr(time.Minute))
		return
	}
	generated := ""
	if req.Password == "" {
		generated = secret.Password()
		req.Password = generated
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	hash, err := s.hasher.Hash(ctx, req.Password)
	if err != nil {
		writeErr(w, rateErr(time.Second))
		return
	}
	ttl := s.cfg.DefaultSessionTTL
	if req.TTLSeconds > 0 {
		ttl = min(time.Duration(req.TTLSeconds)*time.Second, s.cfg.MaxSessionTTL)
	}
	caps := req.DefaultCapabilities
	if caps == nil {
		caps = []string{"presentation.navigate"}
	}
	hostTok := secret.Token("rlvh")
	now := time.Now()
	sess := &Session{
		srv: s, id: secret.SessionID(), pw: hash, hostTokenHash: secret.HashToken(hostTok),
		createdAt: now, expiresAt: now.Add(ttl), requireApproval: req.RequireApproval,
		defaultCaps: caps, participants: map[string]*participant{}, byToken: map[[32]byte]*participant{},
		inflight: map[int64]*cmdRecord{}, hostOffline: now,
	}
	s.mu.Lock()
	s.sessions[sess.id] = sess
	s.mu.Unlock()
	s.log.Info("session created", "session", shortID(sess.id), "ip", ip, "approval", req.RequireApproval)
	resp := map[string]any{
		"sessionId": sess.id, "joinUrl": s.publicURL() + "/s/" + sess.id, "wsUrl": s.wsURL(),
		"hostToken": hostTok, "expiresAt": sess.expiresAt.UTC().Format(time.RFC3339),
	}
	if generated != "" {
		resp["generatedPassword"] = generated
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	if e := s.checkOrigin(r); e != nil {
		writeErr(w, e)
		return
	}
	ip := s.clientIP(r)
	sid := r.PathValue("id")
	if ok, wait := s.loginIP.Allow(ip); !ok {
		writeErr(w, rateErr(wait))
		return
	}
	failKey := sid + "|" + ip
	if d := s.loginFail.Check(failKey); d > 0 {
		writeErr(w, rateErr(d))
		return
	}
	var req struct {
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
	}
	if e := s.readBody(r, "http/JoinRequest", false, &req); e != nil {
		writeErr(w, e)
		return
	}
	sess := s.getSession(sid)
	var hash *auth.Hash
	if sess != nil {
		sess.mu.Lock()
		if !sess.closed {
			h := sess.pw
			hash = &h
		}
		sess.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	ok, err := s.hasher.Verify(ctx, hash, req.Password)
	if errors.Is(err, auth.ErrBusy) {
		writeErr(w, rateErr(time.Second))
		return
	}
	if !ok {
		s.loginFail.Fail(failKey)
		if allowed, _ := s.sessionFail.Allow(sid); !allowed {
			s.log.Warn("many failed logins on session", "session", shortID(sid))
		}
		s.log.Info("join failed", "session", shortID(sid), "ip", ip)
		writeErr(w, protocol.NewError(protocol.AuthFailed, "Authentication failed"))
		return
	}
	s.loginFail.Success(failKey)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed || hash == nil || hash.Equal(sess.pw) == false {
		// password rotated while hashing
		writeErr(w, protocol.NewError(protocol.AuthFailed, "Authentication failed"))
		return
	}
	active := 0
	for _, p := range sess.participants {
		if p.state != protocol.StateRevoked {
			active++
		}
	}
	if active >= s.cfg.MaxParticipants {
		writeErr(w, protocol.NewError(protocol.RateLimited, "The session is full"))
		return
	}
	tok := secret.Token("rlvp")
	now := time.Now()
	p := &participant{
		id: secret.ParticipantID(), name: req.DisplayName, state: protocol.StateEnabled,
		caps: slices.Clone(sess.defaultCaps), joinedAt: now,
		expiresAt: minTime(sess.expiresAt, now.Add(s.cfg.ParticipantTokenTTL)),
		tokenHash: secret.HashToken(tok), recent: map[string]*cmdRecord{},
		cmdLimiter: ratelimit.New(s.cfg.CommandsPerSecond, s.cfg.CommandBurst, 1),
	}
	if sess.requireApproval {
		p.state = protocol.StatePending
	}
	sess.participants[p.id] = p
	sess.byToken[p.tokenHash] = p
	sess.authzRev++
	sess.notifyParticipant(p)
	s.log.Info("participant joined", "session", shortID(sid), "participant", p.id, "state", p.state)
	writeJSON(w, http.StatusOK, map[string]any{
		"participantId": p.id, "token": tok, "state": p.state,
		"expiresAt": p.expiresAt.UTC().Format(time.RFC3339), "wsUrl": s.wsURL(),
	})
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	pr, e := s.authenticate(r)
	if e != nil {
		writeErr(w, e)
		return
	}
	if pr.host {
		writeErr(w, protocol.NewError(protocol.Forbidden, "The host ends the session with DELETE"))
		return
	}
	pr.sess.mu.Lock()
	pr.sess.revokeLocked(pr.p, "revoked")
	pr.sess.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	pr, e := s.authenticate(r)
	if e != nil {
		writeErr(w, e)
		return
	}
	pr.sess.mu.Lock()
	v := pr.sess.view(pr.p)
	pr.sess.mu.Unlock()
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleParticipants(w http.ResponseWriter, r *http.Request) {
	pr := s.requireHost(w, r)
	if pr == nil {
		return
	}
	pr.sess.mu.Lock()
	v := pr.sess.view(nil)
	pr.sess.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"participants": v.Participants, "authzRevision": v.AuthzRevision})
}

func (s *Server) handlePatchParticipant(w http.ResponseWriter, r *http.Request) {
	pr := s.requireHost(w, r)
	if pr == nil {
		return
	}
	var req struct {
		State        string   `json:"state"`
		Capabilities []string `json:"capabilities"`
	}
	if e := s.readBody(r, "http/ParticipantPatch", false, &req); e != nil {
		writeErr(w, e)
		return
	}
	ss := pr.sess
	ss.mu.Lock()
	defer ss.mu.Unlock()
	p := ss.participants[r.PathValue("pid")]
	if p == nil {
		writeErr(w, protocol.NewError(protocol.NotFound, "No such participant"))
		return
	}
	if p.state == protocol.StateRevoked && req.State != "" && req.State != protocol.StateRevoked {
		writeErr(w, protocol.NewError(protocol.InvalidMessage, "A revoked participant cannot be re-enabled; they must join again"))
		return
	}
	if req.Capabilities != nil {
		p.caps = slices.Clone(req.Capabilities)
	}
	switch req.State {
	case protocol.StateRevoked:
		ss.revokeLocked(p, "revoked")
	case protocol.StateEnabled, protocol.StateDisabled:
		p.state = req.State
	}
	if req.State != protocol.StateRevoked {
		ss.authzRev++
		ss.notifyParticipant(p)
	}
	writeJSON(w, http.StatusOK, p.view())
}

func (s *Server) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	pr := s.requireHost(w, r)
	if pr == nil {
		return
	}
	var req struct {
		Paused          *bool `json:"paused"`
		RequireApproval *bool `json:"requireApproval"`
	}
	if e := s.readBody(r, "http/SessionPatch", false, &req); e != nil {
		writeErr(w, e)
		return
	}
	ss := pr.sess
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if req.Paused != nil {
		ss.paused = *req.Paused
	}
	if req.RequireApproval != nil {
		ss.requireApproval = *req.RequireApproval
	}
	ss.authzRev++
	ss.broadcast()
	writeJSON(w, http.StatusOK, ss.view(nil))
}

func (s *Server) handleRotatePassword(w http.ResponseWriter, r *http.Request) {
	pr := s.requireHost(w, r)
	if pr == nil {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if e := s.readBody(r, "http/PasswordRotateRequest", true, &req); e != nil {
		writeErr(w, e)
		return
	}
	generated := ""
	if req.Password == "" {
		generated = secret.Password()
		req.Password = generated
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	hash, err := s.hasher.Hash(ctx, req.Password)
	if err != nil {
		writeErr(w, rateErr(time.Second))
		return
	}
	ss := pr.sess
	ss.mu.Lock()
	ss.pw = hash
	n := 0
	for _, p := range ss.participants {
		if p.state != protocol.StateRevoked {
			ss.revokeLocked(p, "password-rotated")
			n++
		}
	}
	ss.authzRev++
	ss.broadcast()
	ss.mu.Unlock()
	s.log.Info("password rotated", "session", shortID(ss.id), "revoked", n)
	resp := map[string]any{"revokedParticipants": n}
	if generated != "" {
		resp["generatedPassword"] = generated
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	pr := s.requireHost(w, r)
	if pr == nil {
		return
	}
	pr.sess.mu.Lock()
	pr.sess.closeLocked("host-ended")
	pr.sess.mu.Unlock()
	s.removeSession(pr.sess.id)
	s.log.Info("session ended by host", "session", shortID(pr.sess.id))
	w.WriteHeader(http.StatusNoContent)
}
