// Package hostagent implements the native receiver: it connects outbound to
// the relay, keeps local configuration and permissions, and executes the
// semantic actions through an adapter.
package hostagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"relevo/internal/adapter"
	"relevo/internal/tunnel"
)

// Config is the local, administrative configuration of the host. Key
// bindings and targets live only here (ADR-0005); the relay never sees them.
type Config struct {
	Backend             string                 `json:"backend"`
	Target              adapter.Target         `json:"target"`
	Profile             string                 `json:"profile"`
	Actions             []string               `json:"actions,omitempty"`
	Bindings            map[string][]string    `json:"bindings,omitempty"`
	CustomActions       []adapter.CustomAction `json:"customActions,omitempty"`
	RequireApproval     bool                   `json:"requireApproval"`
	DefaultCapabilities []string               `json:"defaultCapabilities,omitempty"`
	AdminListen         string                 `json:"adminListen"`
	Connection          Connection             `json:"connection"`
	Language            string                 `json:"language,omitempty"` // "en" (default) or "es": panel, tray and terminal
}

// Connection modes (ADR-0001).
const (
	ModeAsk    = "ask"    // choose when creating a session
	ModeTunnel = "tunnel" // embedded relay exposed by a tunnel client started by relevo-host
	ModeURL    = "url"    // embedded relay; the host runs their own tunnel/proxy (Pangolin, frp, …) to LocalPort
)

// Connection says how speakers reach this host.
type Connection struct {
	Mode   string         `json:"mode"`
	Tunnel string         `json:"tunnel,omitempty"` // provider id, or "custom"
	Custom *tunnel.Custom `json:"custom,omitempty"`
	// PublicURL for ModeURL: https URL that your own tunnel forwards to LocalPort.
	PublicURL string `json:"publicUrl,omitempty"`
	// LocalPort of the embedded relay (0 = random; ModeURL needs a fixed one).
	LocalPort int `json:"localPort,omitempty"`
	// AllowInsecureTunnel accepts providers without TLS (bore.pub). The
	// password and commands then travel in clear text.
	AllowInsecureTunnel bool `json:"allowInsecureTunnel,omitempty"`
	// WebRTC lets controllers upgrade to a direct DataChannel (default on).
	WebRTC *bool `json:"webrtc,omitempty"`
	// STUNServers for WebRTC address discovery (nothing is relayed through them).
	STUNServers []string `json:"stunServers,omitempty"`
}

func (c Connection) webRTC() bool { return c.WebRTC == nil || *c.WebRTC }

func DefaultConfig() Config {
	return Config{
		Backend: "auto", Profile: "presentation",
		AdminListen: "127.0.0.1:0",
		Connection:  Connection{Mode: ModeAsk},
	}
}

// DefaultPath returns the per-user config file location.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "relevo", "host.json")
}

func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	c.normalize()
	return c, c.Validate()
}

func (c Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// normalize maps empty or retired modes (the old remote "relay" mode) to "ask".
func (c *Config) normalize() {
	switch c.Connection.Mode {
	case ModeAsk, ModeTunnel, ModeURL:
	default:
		c.Connection.Mode = ModeAsk
	}
}

// Validate checks the connection settings and the action bindings.
func (c Config) Validate() error {
	c.normalize()
	if _, err := adapter.Resolve(c.Profile, c.Actions, c.Bindings, c.CustomActions); err != nil {
		return err
	}
	return c.Connection.validate(c)
}

// ValidateMode checks that a concrete connection mode is usable.
func (cn Connection) validate(c Config) error {
	switch cn.Mode {
	case ModeAsk:
		return nil
	case ModeTunnel:
		if cn.Tunnel == "custom" {
			if cn.Custom == nil {
				return errors.New("custom tunnel needs connection.custom {command, urlRegex}")
			}
			_, err := tunnel.FromCustom(*cn.Custom)
			return err
		}
		if _, ok := tunnel.Find(cn.Tunnel); !ok {
			return fmt.Errorf("unknown tunnel provider %q", cn.Tunnel)
		}
		return nil
	case ModeURL:
		u, err := url.Parse(cn.PublicURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && cn.AllowInsecureTunnel)) {
			return errors.New("connection.publicUrl must be the https:// URL of your own tunnel or proxy")
		}
		if cn.LocalPort <= 0 {
			return errors.New("connection.localPort must be fixed when you run your own tunnel")
		}
		return nil
	}
	return fmt.Errorf("unknown connection mode %q", cn.Mode)
}

func (c Config) resolve() (*adapter.Resolved, error) {
	return adapter.Resolve(c.Profile, c.Actions, c.Bindings, c.CustomActions)
}
