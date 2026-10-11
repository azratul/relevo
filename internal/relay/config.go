package relay

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"relevo/internal/auth"
)

// Config holds relay settings. See docs/self-hosting.md for the env variables.
type Config struct {
	Listen        string
	PublicURL     string   // e.g. https://relay.example.org (no trailing slash)
	AllowedOrigin []string // exact browser origins; default: origin of PublicURL
	// DevInsecure allows an http:// PublicURL. Only accepted with a loopback listener.
	DevInsecure    bool
	TrustedProxies []netip.Prefix

	SessionCreation string // "open" or "token"
	CreateToken     string

	MaxSessions          int
	MaxParticipants      int
	DefaultSessionTTL    time.Duration
	MaxSessionTTL        time.Duration
	HostIdleTimeout      time.Duration // close session if no host for this long
	ParticipantTokenTTL  time.Duration
	AuthTimeout          time.Duration
	HeartbeatInterval    time.Duration
	HeartbeatTimeout     time.Duration
	MaxMessageBytes      int64
	MaxMessagesPerSecond float64
	MaxUnauthConns       int
	MaxConnsPerIP        int
	CommandValidFor      time.Duration
	ResultGrace          time.Duration
	MaxInFlight          int
	CommandsPerSecond    float64
	CommandBurst         int
	DedupRetention       int

	LoginPerIPPerMinute float64
	CreatePerIPPerHour  float64
	LoginFreeFailures   int
	// LoginLockoutMax caps the exponential lockout after failed logins. Keep it
	// short when client IPs are unknown (all speakers share one key).
	LoginLockoutMax  time.Duration
	Argon2           auth.Params
	WebDir           string // optional override of the embedded controller
	ValidateOutbound bool   // tests: validate every relay->peer message against the schemas
	AppVersion       string

	// WebRTC DataChannel transport (ADR-0014). STUN only: public servers
	// tell each side its public address; nothing is relayed through them.
	EnableWebRTC bool
	STUNServers  []string
}

func DefaultConfig() Config {
	return Config{
		Listen:               "127.0.0.1:8080",
		SessionCreation:      "open",
		MaxSessions:          500,
		MaxParticipants:      50,
		DefaultSessionTTL:    8 * time.Hour,
		MaxSessionTTL:        24 * time.Hour,
		HostIdleTimeout:      30 * time.Minute,
		ParticipantTokenTTL:  12 * time.Hour,
		AuthTimeout:          5 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		HeartbeatTimeout:     10 * time.Second,
		MaxMessageBytes:      16 * 1024,
		MaxMessagesPerSecond: 20,
		MaxUnauthConns:       1000,
		MaxConnsPerIP:        30,
		CommandValidFor:      3 * time.Second,
		ResultGrace:          3 * time.Second,
		MaxInFlight:          8,
		CommandsPerSecond:    5,
		CommandBurst:         10,
		DedupRetention:       256,
		LoginPerIPPerMinute:  10,
		CreatePerIPPerHour:   20,
		LoginFreeFailures:    5,
		LoginLockoutMax:      5 * time.Minute,
		Argon2:               auth.DefaultParams(),
		AppVersion:           "dev",
		STUNServers:          []string{"stun:stun.l.google.com:19302", "stun:stun.cloudflare.com:3478"},
	}
}

// Finalize validates the configuration and fills derived defaults.
func (c *Config) Finalize() error {
	if c.PublicURL == "" {
		c.PublicURL = "http://" + c.Listen
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Path != "" {
		return fmt.Errorf("invalid public URL %q (want scheme://host[:port])", c.PublicURL)
	}
	if u.Scheme == "http" {
		if !c.DevInsecure {
			return errors.New("public URL uses http://; set RELEVO_DEV_INSECURE=1 for loopback development or use https via a TLS proxy")
		}
		host, _, err := net.SplitHostPort(c.Listen)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if host != "localhost" && (err != nil || !ip.IsLoopback()) {
			return errors.New("insecure development mode requires a loopback listen address")
		}
	}
	if own := u.Scheme + "://" + u.Host; !c.originAllowed(own) {
		c.AllowedOrigin = append(c.AllowedOrigin, own)
	}
	switch c.SessionCreation {
	case "open":
	case "token":
		if len(c.CreateToken) < 16 {
			return errors.New("session creation mode 'token' needs RELEVO_CREATE_TOKEN of at least 16 characters")
		}
	default:
		return fmt.Errorf("unknown session creation mode %q", c.SessionCreation)
	}
	if c.DefaultSessionTTL > c.MaxSessionTTL {
		c.DefaultSessionTTL = c.MaxSessionTTL
	}
	return nil
}

func originOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Scheme + "://" + p.Host
}

func (c *Config) wsURL() string {
	if strings.HasPrefix(c.PublicURL, "https://") {
		return "wss://" + strings.TrimPrefix(c.PublicURL, "https://") + "/api/v1/ws"
	}
	return "ws://" + strings.TrimPrefix(c.PublicURL, "http://") + "/api/v1/ws"
}

func (c *Config) originAllowed(o string) bool {
	for _, a := range c.AllowedOrigin {
		if o == a {
			return true
		}
	}
	return false
}
