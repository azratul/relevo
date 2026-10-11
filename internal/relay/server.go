// Package relay implements the Relevo relay: it serves the web controller,
// manages sessions and authentication and routes messages. It never executes
// operating-system actions (ADR-0001, ADR-0009).
package relay

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"

	"relevo/internal/auth"
	"relevo/internal/protocol"
	"relevo/internal/ratelimit"
)

type Server struct {
	cfg       Config
	log       *slog.Logger
	hasher    *auth.Hasher
	validator *protocol.Validator
	web       fs.FS

	mu       sync.RWMutex
	sessions map[string]*Session

	pubMu sync.RWMutex // guards cfg.PublicURL and cfg.AllowedOrigin (tunnels can change URL)

	loginIP     *ratelimit.Limiter
	createIP    *ratelimit.Limiter
	loginFail   *ratelimit.Backoff // key: session|ip
	sessionFail *ratelimit.Limiter // failed logins per session (global cap)

	connMu    sync.Mutex
	unauth    int
	connsByIP map[string]int

	rtcOnce  sync.Once
	rtc      *webrtc.API
	rtcLimit *ratelimit.Limiter

	OutboundViolations atomic.Int64
	stop               chan struct{}
	stopOnce           sync.Once
}

// New creates a relay. web may be nil (API only).
func New(cfg Config, log *slog.Logger, web fs.FS) (*Server, error) {
	if err := cfg.Finalize(); err != nil {
		return nil, err
	}
	v, err := protocol.DefaultValidator()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg: cfg, log: log, hasher: auth.NewHasher(cfg.Argon2), validator: v, web: web,
		sessions:    map[string]*Session{},
		loginIP:     ratelimit.New(cfg.LoginPerIPPerMinute/60, int(cfg.LoginPerIPPerMinute), 100000),
		createIP:    ratelimit.New(cfg.CreatePerIPPerHour/3600, max(1, int(cfg.CreatePerIPPerHour/4)), 100000),
		loginFail:   ratelimit.NewBackoff(cfg.LoginFreeFailures, time.Second, cfg.LoginLockoutMax, 100000),
		sessionFail: ratelimit.New(1, 60, 100000), // ≤60 failed logins burst, 1/s sustained per session
		connsByIP:   map[string]int{},
		rtcLimit:    ratelimit.New(0.5, 3, 100000),
		stop:        make(chan struct{}),
	}
	go s.janitor()
	return s, nil
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/info", s.handleInfo)
	mux.HandleFunc("POST /api/v1/sessions", s.handleCreate)
	mux.HandleFunc("POST /api/v1/sessions/{id}/join", s.handleJoin)
	mux.HandleFunc("POST /api/v1/sessions/{id}/leave", s.handleLeave)
	mux.HandleFunc("GET /api/v1/sessions/{id}/state", s.handleState)
	mux.HandleFunc("GET /api/v1/sessions/{id}/participants", s.handleParticipants)
	mux.HandleFunc("PATCH /api/v1/sessions/{id}/participants/{pid}", s.handlePatchParticipant)
	mux.HandleFunc("PATCH /api/v1/sessions/{id}", s.handlePatchSession)
	mux.HandleFunc("POST /api/v1/sessions/{id}/password", s.handleRotatePassword)
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.handleDelete)
	mux.HandleFunc("GET /api/v1/ws", s.handleWS)
	mux.HandleFunc("POST /api/v1/sessions/{id}/rtc", s.handleRTC)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, protocol.NewError(protocol.NotFound, "Unknown endpoint"))
	})
	if s.web != nil {
		mux.Handle("/", s.webHandler())
	}
	return s.securityHeaders(mux)
}

func (s *Server) securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")
		hd.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(s.publicURL(), "https://") {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			hd.Set("Cache-Control", "no-store")
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) webHandler() http.Handler {
	files := http.FileServerFS(s.web)
	index := func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(s.web, "index.html")
		if err != nil {
			http.Error(w, "web controller not built (run: make web)", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := r.URL.Path
		if p == "/" || strings.HasPrefix(p, "/s/") {
			index(w, r)
			return
		}
		if p == "/index.html" {
			http.Redirect(w, r, "/", http.StatusMovedPermanently)
			return
		}
		if strings.HasPrefix(p, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Server) publicURL() string {
	s.pubMu.RLock()
	defer s.pubMu.RUnlock()
	return s.cfg.PublicURL
}

func (s *Server) wsURL() string {
	s.pubMu.RLock()
	defer s.pubMu.RUnlock()
	return s.cfg.wsURL()
}

func (s *Server) originAllowed(o string) bool {
	s.pubMu.RLock()
	defer s.pubMu.RUnlock()
	return s.cfg.originAllowed(o)
}

// SetPublicURL changes the public base URL (e.g. a tunnel restarted with a new
// address). The previous public origin stops being an allowed Origin.
func (s *Server) SetPublicURL(u string) error {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	next := s.cfg
	oldOrigin := originOf(next.PublicURL)
	next.PublicURL = u
	next.AllowedOrigin = nil
	for _, o := range s.cfg.AllowedOrigin {
		if o != oldOrigin {
			next.AllowedOrigin = append(next.AllowedOrigin, o)
		}
	}
	if err := next.Finalize(); err != nil {
		return err
	}
	s.cfg.PublicURL, s.cfg.AllowedOrigin = next.PublicURL, next.AllowedOrigin
	return nil
}

// PublicURL returns the current public base URL.
func (s *Server) PublicURL() string { return s.publicURL() }

// clientIP returns the peer address, honouring X-Forwarded-For only from trusted proxies.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	if !s.trusted(addr) {
		return addr.String()
	}
	xff := r.Header.Values("X-Forwarded-For")
	hops := []string{}
	for _, h := range xff {
		for _, part := range strings.Split(h, ",") {
			hops = append(hops, strings.TrimSpace(part))
		}
	}
	for i := len(hops) - 1; i >= 0; i-- { // rightmost untrusted hop
		a, err := netip.ParseAddr(hops[i])
		if err != nil {
			break
		}
		if !s.trusted(a) {
			return a.String()
		}
		addr = a
	}
	return addr.String()
}

func (s *Server) trusted(a netip.Addr) bool {
	for _, p := range s.cfg.TrustedProxies {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}

func (s *Server) getSession(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *Server) removeSession(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// janitor expires sessions and cleans rate-limit tables.
func (s *Server) janitor() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
		}
		now := time.Now()
		s.mu.RLock()
		all := make([]*Session, 0, len(s.sessions))
		for _, ss := range s.sessions {
			all = append(all, ss)
		}
		s.mu.RUnlock()
		for _, ss := range all {
			ss.mu.Lock()
			reason := ""
			switch {
			case now.After(ss.expiresAt):
				reason = "expired"
			case ss.host == nil && !ss.hostOffline.IsZero() && now.Sub(ss.hostOffline) > s.cfg.HostIdleTimeout:
				reason = "idle"
			}
			for _, p := range ss.participants {
				if p.state != protocol.StateRevoked && now.After(p.expiresAt) {
					ss.revokeLocked(p, "expired")
				}
			}
			if reason != "" {
				ss.closeLocked(reason)
			}
			closed := ss.closed
			ss.mu.Unlock()
			if closed {
				s.removeSession(ss.id)
				s.log.Info("session closed", "session", shortID(ss.id), "reason", reason)
			}
		}
		s.loginIP.GC()
		s.createIP.GC()
		s.sessionFail.GC()
	}
}

// Shutdown closes every session (state is ephemeral, ADR-0009).
func (s *Server) Shutdown(ctx context.Context) {
	s.stopOnce.Do(func() { close(s.stop) })
	s.mu.Lock()
	all := s.sessions
	s.sessions = map[string]*Session{}
	s.mu.Unlock()
	for _, ss := range all {
		ss.mu.Lock()
		ss.closeLocked("relay-shutdown")
		ss.mu.Unlock()
	}
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond): // let close frames flush
	}
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[:6] + "…"
	}
	return id
}
