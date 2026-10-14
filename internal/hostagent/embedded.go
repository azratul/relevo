package hostagent

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"relevo/internal/relay"
	"relevo/internal/secret"
	"relevo/internal/tunnel"
	webcontroller "relevo/web/controller"
)

// embedded is a relay running inside relevo-host, reachable from the
// Internet only through the tunnel the host chose (ADR-0001). There is no
// Relevo server anywhere else.
type embedded struct {
	ln          net.Listener
	srv         *relay.Server
	hs          *http.Server
	base        string // http://127.0.0.1:port (host agent talks to it directly)
	createToken string
	provider    tunnel.Provider
	conn        Connection

	mu         sync.Mutex
	tun        *tunnel.Tunnel
	publicURL  string
	tunnelErr  string
	tunnelCode string // tunnel_down | tunnel_failed | link_changed
	stopping   bool
	stopped    chan struct{}
}

var errNeedMode = errors.New("choose how speakers will connect (a tunnel or your own URL)")

func providerFor(cn Connection) (tunnel.Provider, error) {
	if cn.Tunnel == "custom" {
		if cn.Custom == nil {
			return tunnel.Provider{}, errors.New("custom tunnel not configured")
		}
		return tunnel.FromCustom(*cn.Custom)
	}
	p, ok := tunnel.Find(cn.Tunnel)
	if !ok {
		return tunnel.Provider{}, fmt.Errorf("unknown tunnel provider %q", cn.Tunnel)
	}
	return p, nil
}

func (a *Agent) startEmbedded(ctx context.Context, cn Connection) (*embedded, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cn.LocalPort))
	if err != nil {
		return nil, fmt.Errorf("cannot listen on the local port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	e := &embedded{ln: ln, base: "http://" + ln.Addr().String(), createToken: secret.Token("rlvc"), conn: cn, stopped: make(chan struct{})}
	switch cn.Mode {
	case ModeTunnel:
		p, err := providerFor(cn)
		if err != nil {
			ln.Close()
			return nil, err
		}
		e.provider = p
		a.log.Info("starting tunnel", "provider", p.ID, "port", port)
		t, err := tunnel.Start(ctx, p, port, cn.AllowInsecureTunnel, 45*time.Second)
		if err != nil {
			ln.Close()
			return nil, err
		}
		e.tun, e.publicURL = t, t.URL
	case ModeURL:
		e.provider = tunnel.Provider{ID: "url", Name: "Own URL", Secure: strings.HasPrefix(cn.PublicURL, "https://")}
		e.publicURL = strings.TrimRight(cn.PublicURL, "/")
	default:
		ln.Close()
		return nil, fmt.Errorf("mode %q has no embedded relay", cn.Mode)
	}
	rc := relay.DefaultConfig()
	rc.Listen = ln.Addr().String()
	rc.PublicURL = e.publicURL
	rc.DevInsecure = strings.HasPrefix(e.publicURL, "http://")  // only reachable here with AllowInsecureTunnel
	rc.SessionCreation, rc.CreateToken = "token", e.createToken // nobody on the Internet can create sessions here
	rc.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	rc.MaxSessions = 1
	rc.EnableWebRTC = cn.webRTC()
	if len(cn.STUNServers) > 0 {
		rc.STUNServers = cn.STUNServers
	}
	if !e.provider.ClientIP {
		// Every speaker arrives as 127.0.0.1: keep lockouts short so one
		// person guessing passwords cannot lock everybody out.
		rc.LoginLockoutMax = 10 * time.Second
		rc.LoginPerIPPerMinute = 120
		rc.MaxConnsPerIP = 200
	}
	rc.AppVersion = a.version
	if a.relayTweak != nil {
		a.relayTweak(&rc)
	}
	srv, err := relay.New(rc, a.log.With("component", "relay"), webcontroller.FS())
	if err != nil {
		e.closeTunnel()
		ln.Close()
		return nil, err
	}
	e.srv = srv
	e.hs = &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	go e.hs.Serve(ln)
	if e.tun != nil {
		go a.watchTunnel(e)
	}
	a.log.Info("embedded relay ready", "public", e.publicURL, "local", e.base, "webrtc", rc.EnableWebRTC)
	return e, nil
}

func (e *embedded) closeTunnel() {
	e.mu.Lock()
	t := e.tun
	e.mu.Unlock()
	if t != nil {
		t.Close()
	}
}

// watchTunnel restarts a dead tunnel. Quick tunnels get a NEW public URL:
// the join link changes and must be shared again.
func (a *Agent) watchTunnel(e *embedded) {
	backoff := time.Second
	for {
		e.mu.Lock()
		t := e.tun
		e.mu.Unlock()
		select {
		case <-t.Done():
		case <-e.stopped:
			return
		}
		e.mu.Lock()
		if e.stopping {
			e.mu.Unlock()
			return
		}
		e.tunnelErr, e.tunnelCode = "The tunnel went down; reconnecting…", "tunnel_down"
		e.mu.Unlock()
		a.log.Warn("tunnel exited; restarting", "provider", e.provider.ID, "output", lastLine(t.Output()))
		a.notify()
		for {
			select {
			case <-e.stopped:
				return
			case <-time.After(backoff/2 + time.Duration(rand.Int64N(int64(backoff)))):
			}
			port := e.ln.Addr().(*net.TCPAddr).Port
			nt, err := tunnel.Start(context.Background(), e.provider, port, e.conn.AllowInsecureTunnel, 45*time.Second)
			if err != nil {
				backoff = min(backoff*2, 60*time.Second)
				e.mu.Lock()
				e.tunnelErr, e.tunnelCode = "Could not reopen the tunnel: "+lastLine(err.Error()), "tunnel_failed"
				e.mu.Unlock()
				a.notify()
				continue
			}
			backoff = time.Second
			if err := e.srv.SetPublicURL(nt.URL); err != nil {
				nt.Close()
				continue
			}
			e.mu.Lock()
			changed := nt.URL != e.publicURL
			e.tun, e.publicURL, e.tunnelErr, e.tunnelCode = nt, nt.URL, "", ""
			if changed {
				e.tunnelErr, e.tunnelCode = "The link changed: share it again", "link_changed"
			}
			e.mu.Unlock()
			a.mu.Lock()
			if a.sess != nil {
				a.sess.JoinURL = nt.URL + "/s/" + a.sess.ID
			}
			a.mu.Unlock()
			a.log.Info("tunnel restarted", "public", nt.URL)
			a.notify()
			break
		}
	}
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func (e *embedded) stop() {
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return
	}
	e.stopping = true
	close(e.stopped)
	e.mu.Unlock()
	e.closeTunnel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e.srv.Shutdown(ctx)
	_ = e.hs.Shutdown(ctx)
}

// ConnectionStatus is shown in the panel and tray.
type ConnectionStatus struct {
	Mode        string `json:"mode"`
	Provider    string `json:"provider,omitempty"`
	Name        string `json:"name,omitempty"`
	PublicURL   string `json:"publicUrl,omitempty"`
	Secure      bool   `json:"secure"`
	ClientIP    bool   `json:"clientIp"`
	WebRTC      bool   `json:"webrtc"`
	Warning     string `json:"warning,omitempty"`
	WarningCode string `json:"warningCode,omitempty"`
}

func (e *embedded) status() ConnectionStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return ConnectionStatus{Mode: e.conn.Mode, Provider: e.provider.ID, Name: e.provider.Name, PublicURL: e.publicURL,
		Secure: e.provider.Secure, ClientIP: e.provider.ClientIP, WebRTC: e.conn.webRTC(), Warning: e.tunnelErr, WarningCode: e.tunnelCode}
}
