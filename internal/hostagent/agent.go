package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"relevo/internal/adapter"
	"relevo/internal/catalog"
	"relevo/internal/protocol"
	"relevo/internal/relay"
	"relevo/internal/secret"
	"relevo/internal/tunnel"
)

const (
	pingInterval = 5 * time.Second
	// leaseTimeout: without a pong for this long the host stops executing,
	// even if the TCP connection still looks open.
	leaseTimeout = 15 * time.Second
	queueSize    = 16
	historySize  = 50
)

var errSessionGone = errors.New("session no longer valid")

// HistoryEntry is a local record of an executed or refused command (UI only).
type HistoryEntry struct {
	At       time.Time `json:"at"`
	Sequence int64     `json:"sequence"`
	Action   string    `json:"action"`
	By       string    `json:"by"`
	Status   string    `json:"status"`
	Detail   string    `json:"detail,omitempty"`
}

type job struct {
	d        protocol.CommandDispatch
	gen      int64
	received time.Time // includes Go's monotonic clock reading
}

// Agent is the host receiver.
type Agent struct {
	log        *slog.Logger
	configPath string
	instanceID string
	version    string

	mu           sync.Mutex
	cfg          Config
	kb           *adapter.Keyboard
	cat          *catalog.Compiled
	catRev       int64
	sess         *SessionInfo
	base         string    // embedded relay base URL (loopback) for admin calls
	emb          *embedded // embedded relay + tunnel
	sessCancel   context.CancelFunc
	sessDone     chan struct{}
	connected    bool
	connErr      string
	epoch        int64
	lastSeq      int64
	seen         map[string]bool
	gen          int64
	lastPong     time.Time
	send         func(typ, corr string, payload any) error
	remotePaused bool
	localPaused  bool
	authzRev     int64
	participants map[string]protocol.Participant
	readiness    adapter.Readiness
	history      []HistoryEntry
	endedReason  string

	queue   chan job
	changed chan struct{} // UI change notifications (best effort)

	// Test hooks: route the host's loopback connection through a fault-
	// injecting proxy, and tweak the embedded relay configuration.
	dialOverride func(wsURL string) string
	relayTweak   func(*relay.Config)
}

// New builds an agent from config. The backend is created immediately so
// missing permissions are reported before any session exists.
func New(cfg Config, configPath, version string, log *slog.Logger) (*Agent, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	a := &Agent{log: log, configPath: configPath, version: version, instanceID: secret.UUID(), cfg: cfg,
		queue: make(chan job, queueSize), changed: make(chan struct{}, 1), participants: map[string]protocol.Participant{}}
	if err := a.applyConfigLocked(cfg); err != nil {
		return nil, err
	}
	go a.executor()
	go a.readinessLoop()
	return a, nil
}

// applyConfigLocked (re)creates the adapter and bumps the catalog revision.
func (a *Agent) applyConfigLocked(cfg Config) error {
	if cfg.Backend == "sim" && cfg.Target.Empty() {
		cfg.Target = adapter.Target{Class: "relevo-sim"} // demo default: the simulated window
	}
	res, err := cfg.resolve()
	if err != nil {
		return err
	}
	if a.kb == nil || a.cfg.Backend != cfg.Backend {
		b, err := adapter.NewBackend(cfg.Backend)
		if err != nil {
			return err
		}
		a.kb = adapter.NewKeyboard(b, res, cfg.Target)
	} else {
		a.kb.Reconfigure(res, cfg.Target)
	}
	a.catRev++
	cat, err := catalog.Compile(protocol.Catalog{Revision: a.catRev, Actions: a.kb.Catalog()})
	if err != nil {
		return err
	}
	a.cfg, a.cat = cfg, cat
	return nil
}

// Keyboard exposes the adapter (tests, CLI diagnostics).
func (a *Agent) Keyboard() *adapter.Keyboard { a.mu.Lock(); defer a.mu.Unlock(); return a.kb }

// Config returns a copy of the current configuration.
func (a *Agent) Config() Config { a.mu.Lock(); defer a.mu.Unlock(); return a.cfg }

// UpdateConfig validates, saves and applies new settings. A connected host
// announces the new catalog revision; controllers with the old one get STALE_CATALOG.
func (a *Agent) UpdateConfig(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.normalize()
	a.mu.Lock()
	if err := a.applyConfigLocked(cfg); err != nil {
		a.mu.Unlock()
		return err
	}
	rd := a.kb.Readiness(context.Background())
	a.readiness = rd
	send, cat := a.send, a.cat.Raw
	a.mu.Unlock()
	if a.configPath != "" {
		if err := cfg.Save(a.configPath); err != nil {
			a.log.Warn("cannot save config", "err", err)
		}
	}
	if send != nil {
		ready := rd.Ready
		reason := rd.Reason
		_ = send(protocol.TypeHostUpdate, "", protocol.HostUpdate{Catalog: &cat, Ready: &ready, ReadinessReason: &reason, Target: &rd.Target})
	}
	a.notify()
	return nil
}

func (a *Agent) notify() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

// Changed is signalled (coalesced) when visible state changes.
func (a *Agent) Changed() <-chan struct{} { return a.changed }

// ---- session lifecycle ----

// CreateOptions are chosen when the session is created.
type CreateOptions struct {
	Password string
	// Connection overrides the configured connection (the panel asks).
	Connection *Connection
}

// ErrNeedMode means the config says "ask" and no choice was given.
var ErrNeedMode = errNeedMode

// CreateSession starts the connection (embedded relay + tunnel, or a remote
// relay), creates a session and starts the connection loop.
func (a *Agent) CreateSession(ctx context.Context, opts CreateOptions) (*SessionInfo, error) {
	a.mu.Lock()
	if a.sess != nil {
		a.mu.Unlock()
		return nil, errors.New("a session is already active")
	}
	cfg := a.cfg
	a.mu.Unlock()
	cn := cfg.Connection
	if opts.Connection != nil {
		cn = *opts.Connection
	}
	if cn.Mode == ModeAsk || cn.Mode == "" {
		return nil, errNeedMode
	}
	if err := cn.validate(cfg); err != nil {
		return nil, err
	}
	req := createReq{Password: opts.Password, RequireApproval: cfg.RequireApproval, DefaultCapabilities: cfg.DefaultCapabilities}
	emb, err := a.startEmbedded(ctx, cn)
	if err != nil {
		return nil, err
	}
	base := emb.base
	info, err := newRelayClient(base).create(ctx, emb.createToken, req)
	if err != nil {
		emb.stop()
		return nil, err
	}
	// The host reaches its own relay over loopback, not through the tunnel.
	info.WSURL = "ws" + strings.TrimPrefix(base, "http") + "/api/v1/ws"
	if a.dialOverride != nil {
		info.WSURL = a.dialOverride(info.WSURL)
	}
	sctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.sess, a.sessCancel, a.sessDone, a.base, a.emb = info, cancel, make(chan struct{}), base, emb
	a.endedReason, a.connErr = "", ""
	a.participants = map[string]protocol.Participant{}
	a.remotePaused, a.localPaused = false, false
	done := a.sessDone
	a.mu.Unlock()
	go func() {
		defer close(done)
		a.runSession(sctx, info)
	}()
	a.log.Info("session created", "join", info.JoinURL, "mode", cn.Mode)
	a.notify()
	return info, nil
}

// Session returns the current session or nil.
func (a *Agent) Session() *SessionInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil {
		return nil
	}
	c := *a.sess
	return &c
}

func (a *Agent) adminCall(ctx context.Context, method, path string, body, out any) error {
	a.mu.Lock()
	s, base := a.sess, a.base
	a.mu.Unlock()
	if s == nil {
		return errors.New("no active session")
	}
	return newRelayClient(base).call(ctx, method, "/api/v1/sessions/"+s.ID+path, s.HostToken, body, out)
}

// EndSession closes the session on the relay (all credentials die) and stops.
func (a *Agent) EndSession(ctx context.Context) error {
	err := a.adminCall(ctx, "DELETE", "", nil, nil)
	a.stopSession("host-ended")
	return err
}

func (a *Agent) stopSession(reason string) {
	a.mu.Lock()
	cancel, done, emb := a.sessCancel, a.sessDone, a.emb
	a.sess, a.sessCancel, a.emb = nil, nil, nil
	a.endedReason = reason
	a.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if emb != nil {
		emb.stop()
	}
	a.notify()
}

// SetPaused pauses locally at once (the executor refuses immediately) and on the relay.
func (a *Agent) SetPaused(ctx context.Context, paused bool) error {
	a.mu.Lock()
	a.localPaused = paused
	a.mu.Unlock()
	a.notify()
	return a.adminCall(ctx, "PATCH", "", map[string]any{"paused": paused}, nil)
}

// RotatePassword revokes every speaker and sets a new password (generated if empty).
func (a *Agent) RotatePassword(ctx context.Context, password string) (string, error) {
	body := map[string]any{}
	if password != "" {
		body["password"] = password
	}
	var out struct {
		GeneratedPassword string `json:"generatedPassword"`
	}
	if err := a.adminCall(ctx, "POST", "/password", body, &out); err != nil {
		return "", err
	}
	if password == "" {
		password = out.GeneratedPassword
	}
	a.mu.Lock()
	if a.sess != nil {
		a.sess.Password = password
	}
	a.mu.Unlock()
	a.notify()
	return password, nil
}

// PatchParticipant enables, disables or revokes a speaker, or sets capabilities.
func (a *Agent) PatchParticipant(ctx context.Context, id string, state string, caps []string) error {
	body := map[string]any{}
	if state != "" {
		body["state"] = state
	}
	if caps != nil {
		body["capabilities"] = caps
	}
	return a.adminCall(ctx, "PATCH", "/participants/"+id, body, nil)
}

// ---- connection loop ----

func (a *Agent) runSession(ctx context.Context, info *SessionInfo) {
	backoff := 500 * time.Millisecond
	for ctx.Err() == nil {
		start := time.Now()
		err := a.connectOnce(ctx, info)
		if errors.Is(err, errSessionGone) {
			a.log.Info("session ended by relay", "err", err)
			a.mu.Lock()
			var emb *embedded
			if a.sess == info {
				a.sess, emb, a.emb = nil, a.emb, nil
				a.endedReason = err.Error()
			}
			a.mu.Unlock()
			if emb != nil {
				go emb.stop()
			}
			a.notify()
			return
		}
		if ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		a.connErr = errString(err)
		a.mu.Unlock()
		a.notify()
		if time.Since(start) > 30*time.Second {
			backoff = 500 * time.Millisecond
		}
		// full jitter
		d := time.Duration(rand.Int64N(int64(backoff))) + 100*time.Millisecond
		a.log.Info("reconnecting", "in", d.Round(time.Millisecond), "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (a *Agent) connectOnce(ctx context.Context, info *SessionInfo) error {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, info.WSURL, nil)
	if err != nil {
		return fmt.Errorf("cannot open WebSocket (check the relay URL and any proxy that may block WebSocket): %w", err)
	}
	ws.SetReadLimit(64 * 1024)
	cctx, ccancel := context.WithCancel(ctx)
	defer ccancel()
	defer ws.CloseNow()
	var wmu sync.Mutex
	send := func(typ, corr string, payload any) error {
		b, err := protocol.Encode(typ, secret.UUID(), info.ID, corr, payload)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		wctx, c := context.WithTimeout(cctx, 5*time.Second)
		defer c()
		return ws.Write(wctx, websocket.MessageText, b)
	}
	if err := send(protocol.TypeAuthHello, "", protocol.AuthHello{Token: info.HostToken, Client: &protocol.ClientInfo{Name: "relevo-host", Version: a.version}}); err != nil {
		return err
	}
	a.mu.Lock()
	a.gen++
	gen := a.gen
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.gen == gen {
			a.connected, a.send, a.epoch = false, nil, 0
			a.gen++ // invalidates queued jobs of this connection
		}
		a.mu.Unlock()
		a.notify()
	}()
	registered := false
	for {
		_, data, err := ws.Read(cctx)
		if err != nil {
			switch websocket.CloseStatus(err) {
			case protocol.CloseSessionClosed, protocol.CloseAuthFailed:
				return fmt.Errorf("%w: closed by relay", errSessionGone)
			}
			return err
		}
		env, err := protocol.DecodeEnvelope(data)
		if err != nil {
			continue
		}
		switch env.Type {
		case protocol.TypeAuthOK:
			a.mu.Lock()
			cat, rd := a.cat.Raw, a.readiness
			a.mu.Unlock()
			if rd.Target.Label == "" {
				rd = a.Keyboard().Readiness(cctx)
			}
			if err := send(protocol.TypeHostRegister, "", protocol.HostRegister{
				AgentInstanceID: a.instanceID, Agent: protocol.AgentInfo{Name: "relevo-host", Version: a.version},
				Ready: rd.Ready, ReadinessReason: rd.Reason, Target: rd.Target, Catalog: cat,
			}); err != nil {
				return err
			}
		case protocol.TypeAuthError:
			var p protocol.ErrorPayload
			_ = json.Unmarshal(env.Payload, &p)
			if p.Error.Code == protocol.UnsupportedVer {
				return fmt.Errorf("relay protocol incompatible: %s", p.Error.Message)
			}
			return fmt.Errorf("%w: %s", errSessionGone, p.Error.Message)
		case protocol.TypeHostRegistered:
			var p protocol.HostRegistered
			_ = json.Unmarshal(env.Payload, &p)
			a.mu.Lock()
			a.epoch, a.lastSeq, a.seen = p.Epoch, 0, map[string]bool{}
			a.connected, a.connErr, a.send, a.lastPong = true, "", send, time.Now()
			a.mu.Unlock()
			if !registered {
				registered = true
				go a.pinger(cctx, ccancel, send, gen)
			}
			a.log.Info("registered with relay", "epoch", p.Epoch)
			a.notify()
		case protocol.TypeSessionSnapshot, protocol.TypeSessionUpdated:
			var v protocol.SessionView
			_ = json.Unmarshal(env.Payload, &v)
			a.applyView(v)
		case protocol.TypeParticipantUpdated:
			var p protocol.ParticipantUpdated
			_ = json.Unmarshal(env.Payload, &p)
			a.mu.Lock()
			a.participants[p.Participant.ParticipantID] = p.Participant
			a.authzRev = max(a.authzRev, p.AuthzRevision)
			a.mu.Unlock()
			a.notify()
		case protocol.TypeCommandDispatch:
			var d protocol.CommandDispatch
			_ = json.Unmarshal(env.Payload, &d)
			a.accept(d, gen, send)
		case protocol.TypeSessionClosed:
			return fmt.Errorf("%w: %s", errSessionGone, string(env.Payload))
		case protocol.TypePong:
			a.mu.Lock()
			a.lastPong = time.Now()
			a.mu.Unlock()
		case protocol.TypeError:
			a.log.Warn("relay error", "payload", string(env.Payload))
		}
	}
}

func (a *Agent) applyView(v protocol.SessionView) {
	a.mu.Lock()
	a.remotePaused = v.Paused
	a.authzRev = max(a.authzRev, v.AuthzRevision)
	if v.Participants != nil {
		a.participants = map[string]protocol.Participant{}
		for _, p := range v.Participants {
			a.participants[p.ParticipantID] = p
		}
	}
	if !v.Paused {
		a.localPaused = false
	}
	a.mu.Unlock()
	a.notify()
}

// pinger keeps the lease: without pongs the host stops executing and reconnects.
func (a *Agent) pinger(ctx context.Context, cancel context.CancelFunc, send func(string, string, any) error, gen int64) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		expired := time.Since(a.lastPong) > leaseTimeout
		a.mu.Unlock()
		if expired {
			a.log.Warn("lease expired: no pong from relay; reconnecting")
			cancel()
			return
		}
		if err := send(protocol.TypePing, "", protocol.Empty{}); err != nil {
			cancel()
			return
		}
	}
}

// accept applies fencing and deduplication before queueing (ADR-0006, ADR-0013).
func (a *Agent) accept(d protocol.CommandDispatch, gen int64, send func(string, string, any) error) {
	a.mu.Lock()
	switch {
	case gen != a.gen || d.Epoch != a.epoch:
		a.mu.Unlock()
		a.log.Warn("dispatch from another epoch ignored", "epoch", d.Epoch, "current", a.epoch)
		return
	case d.Sequence <= a.lastSeq || a.seen[d.CommandID]:
		a.mu.Unlock()
		a.log.Warn("duplicate or out-of-order dispatch ignored", "sequence", d.Sequence, "last", a.lastSeq)
		return
	}
	a.lastSeq = d.Sequence
	a.seen[d.CommandID] = true
	if len(a.seen) > 4096 { // the monotonic sequence check above still blocks replays
		a.seen = map[string]bool{d.CommandID: true}
	}
	a.mu.Unlock()
	select {
	case a.queue <- job{d: d, gen: gen, received: time.Now()}:
	default:
		a.reply(job{d: d, gen: gen}, protocol.StatusRejected, "", protocol.NewError(protocol.RateLimited, "Host queue full"), 0)
	}
}

func (a *Agent) executor() {
	for j := range a.queue {
		a.execute(j)
	}
}

// execute revalidates against the current local state and runs the action.
func (a *Agent) execute(j job) {
	d := j.d
	a.mu.Lock()
	live := j.gen == a.gen && a.connected && time.Since(a.lastPong) < leaseTimeout
	paused := a.localPaused || a.remotePaused
	p, known := a.participants[d.IssuedBy.ParticipantID]
	cat, kb := a.cat, a.kb
	a.mu.Unlock()
	if !live {
		// Cannot report anyway; the relay marks it unknown. Never execute.
		a.record(d, "dropped", "connection lost before execution")
		return
	}
	budget := time.Duration(d.ValidForMs) * time.Millisecond
	if time.Since(j.received) > budget {
		a.reply(j, protocol.StatusExpired, "", protocol.NewError(protocol.CommandExpired, "Command expired in the host queue"), 0)
		return
	}
	reject := func(code protocol.Code, msg string) {
		a.reply(j, protocol.StatusRejected, "", protocol.NewError(code, msg), 0)
	}
	switch {
	case paused:
		reject(protocol.SessionPaused, "The host paused control")
		return
	case !known || p.State != protocol.StateEnabled:
		reject(protocol.PermissionDenied, "Speaker not enabled")
		return
	case d.CatalogRevision != cat.Revision:
		reject(protocol.StaleCatalog, "Catalog changed")
		return
	}
	act := cat.Actions[d.Action]
	if act == nil {
		reject(protocol.UnsupportedAction, "Action not offered")
		return
	}
	if !slices.Contains(p.Capabilities, act.Capability) {
		reject(protocol.PermissionDenied, "Missing capability")
		return
	}
	if err := act.ValidateParams(d.Parameters); err != nil {
		reject(protocol.InvalidMessage, err.Error())
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), j.received.Add(budget))
	defer cancel()
	start := time.Now()
	conf, err := kb.Execute(ctx, d.Action, d.Parameters)
	dur := time.Since(start).Milliseconds()
	var rej *adapter.Rejection
	switch {
	case err == nil:
		a.reply(j, protocol.StatusExecuted, conf, nil, dur)
	case errors.As(err, &rej):
		a.reply(j, protocol.StatusRejected, "", protocol.NewError(rej.Code, rej.Message), dur)
	case errors.Is(err, adapter.ErrUncertain):
		a.reply(j, protocol.StatusFailed, "", protocol.NewError(protocol.ExecutionUnknown, "Input may have been partially injected"), dur)
	default:
		a.log.Warn("adapter failed", "action", d.Action, "err", err)
		a.reply(j, protocol.StatusFailed, "", protocol.NewError(protocol.ExecutionFailed, "The adapter could not execute the action"), dur)
	}
}

func (a *Agent) reply(j job, status, conf string, e *protocol.Error, durMs int64) {
	d := j.d
	detail := ""
	if e != nil {
		detail = string(e.Code)
	}
	a.record(d, status, detail)
	a.mu.Lock()
	send := a.send
	ok := j.gen == a.gen
	a.mu.Unlock()
	if !ok || send == nil {
		return
	}
	_ = send(protocol.TypeCommandResult, "", protocol.HostCommandResult{
		CommandID: d.CommandID, Sequence: d.Sequence, Epoch: d.Epoch, Status: status, Confirmation: conf, Error: e, DurationMs: durMs,
	})
}

func (a *Agent) record(d protocol.CommandDispatch, status, detail string) {
	a.mu.Lock()
	a.history = append(a.history, HistoryEntry{At: time.Now(), Sequence: d.Sequence, Action: d.Action, By: d.IssuedBy.DisplayName, Status: status, Detail: detail})
	if len(a.history) > historySize {
		a.history = a.history[len(a.history)-historySize:]
	}
	a.mu.Unlock()
	a.log.Info("command", "seq", d.Sequence, "action", d.Action, "by", d.IssuedBy.ParticipantID, "status", status, "detail", detail)
	a.notify()
}

// readinessLoop polls the adapter (focus/permissions) and announces changes.
func (a *Agent) readinessLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		kb := a.Keyboard()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		rd := kb.Readiness(ctx)
		cancel()
		a.mu.Lock()
		changed := rd != a.readiness
		a.readiness = rd
		send := a.send
		a.mu.Unlock()
		if changed {
			if send != nil {
				ready, reason := rd.Ready, rd.Reason
				_ = send(protocol.TypeHostUpdate, "", protocol.HostUpdate{Ready: &ready, ReadinessReason: &reason, Target: &rd.Target})
			}
			a.notify()
		}
		<-t.C
	}
}

// Status is the local view for the admin UI and tray.
type Status struct {
	Session      *PublicSession              `json:"session"`
	EndedReason  string                      `json:"endedReason,omitempty"`
	Connected    bool                        `json:"connected"`
	ConnError    string                      `json:"connError,omitempty"`
	Epoch        int64                       `json:"epoch"`
	Paused       bool                        `json:"paused"`
	Participants []protocol.Participant      `json:"participants"`
	Ready        bool                        `json:"ready"`
	Reason       string                      `json:"reason,omitempty"`
	ReasonCode   string                      `json:"reasonCode,omitempty"`
	Target       protocol.Target             `json:"target"`
	Backend      string                      `json:"backend"`
	Backends     []adapter.BackendInfo       `json:"backends"`
	Catalog      []protocol.ActionDescriptor `json:"catalog"`
	CatalogRev   int64                       `json:"catalogRevision"`
	Config       Config                      `json:"config"`
	History      []HistoryEntry              `json:"history"`
	Profiles     map[string]string           `json:"profiles"`
	AllActions   []map[string]string         `json:"allActions"`
	Permissions  []string                    `json:"permissions"`
	Connection   *ConnectionStatus           `json:"connection,omitempty"`
	Tunnels      []tunnel.Info               `json:"tunnels"`
}

// PublicSession omits the host token; the password is only revealed on demand.
type PublicSession struct {
	ID        string `json:"id"`
	JoinURL   string `json:"joinUrl"`
	ExpiresAt string `json:"expiresAt"`
}

func (a *Agent) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := Status{
		Connected: a.connected, ConnError: a.connErr, Epoch: a.epoch, Paused: a.localPaused || a.remotePaused,
		Ready: a.readiness.Ready, Reason: a.readiness.Reason, ReasonCode: a.readiness.Code, Target: a.readiness.Target,
		Backend: a.kb.Backend().Name(), Backends: adapter.Backends(), Catalog: a.cat.Raw.Actions, CatalogRev: a.cat.Revision,
		Config: a.cfg, History: append([]HistoryEntry{}, a.history...), Participants: []protocol.Participant{}, EndedReason: a.endedReason,
		Profiles: map[string]string{}, Permissions: a.kb.Backend().Permissions(),
	}
	for k, p := range adapter.Profiles() {
		st.Profiles[k] = p.Name
	}
	for _, d := range adapter.BuiltinActions() {
		st.AllActions = append(st.AllActions, map[string]string{"id": d.ID, "title": d.Title, "description": d.Description, "capability": d.Capability})
	}
	if a.sess != nil {
		st.Session = &PublicSession{ID: a.sess.ID, JoinURL: a.sess.JoinURL, ExpiresAt: a.sess.ExpiresAt}
		if a.emb != nil {
			cs := a.emb.status()
			st.Connection = &cs
		}
	}
	st.Tunnels = tunnel.Available()
	for _, p := range a.participants {
		st.Participants = append(st.Participants, p)
	}
	slices.SortFunc(st.Participants, func(x, y protocol.Participant) int {
		if x.JoinedAt == y.JoinedAt {
			return 0
		}
		if x.JoinedAt < y.JoinedAt {
			return -1
		}
		return 1
	})
	return st
}

// Password returns the session password (explicit reveal only).
func (a *Agent) Password() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil {
		return ""
	}
	return a.sess.Password
}

// Close ends any active session (best effort) and stops.
func (a *Agent) Close(ctx context.Context) {
	if a.Session() != nil {
		if err := a.EndSession(ctx); err != nil {
			a.log.Warn("could not end session on relay", "err", err)
		}
	}
}
