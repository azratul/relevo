// Package tunnel exposes the host's embedded relay through a third-party
// tunnel chosen by the host (ADR-0001). Relevo never runs a server of its own:
// the provider's client program is started locally and the public URL is read
// from its output.
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider describes how to start one tunnel client.
type Provider struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Binary string `json:"binary"`
	Secure bool   `json:"secure"` // public URL is https (TLS terminated by the provider)
	// ClientIP: the provider passes the speaker's IP (X-Forwarded-For). Without
	// it every speaker looks like 127.0.0.1 to the relay.
	ClientIP    bool   `json:"clientIp"`
	Account     string `json:"account"`
	Notes       string `json:"notes"`
	InstallHint string `json:"installHint"`
	// Builtin: implemented inside relevo-host (nothing to install).
	Builtin bool `json:"builtin"`
	native  func(ctx context.Context, port int) (*Tunnel, error)
	args    func(port int) []string
	url     *regexp.Regexp // first submatch (or whole match) is the public URL
	build   func(match []string) string
}

// Custom is a user-defined provider (Pangolin/newt, zrok, frp, …).
type Custom struct {
	Command  []string `json:"command"`  // "{port}" is replaced by the local port
	URLRegex string   `json:"urlRegex"` // first submatch, or the whole match, is the public URL
	Secure   bool     `json:"secure"`   // the operator asserts the URL is https
}

func stateDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	d = filepath.Join(d, "relevo")
	_ = os.MkdirAll(d, 0o700)
	return d
}

// Providers returns the known providers: built-in ones first (nothing to
// install), the unencrypted bore.pub last.
func Providers() []Provider {
	all := providers()
	slices.SortStableFunc(all, func(a, b Provider) int {
		rank := func(p Provider) int {
			switch {
			case !p.Secure:
				return 2
			case p.Builtin:
				return 0
			}
			return 1
		}
		return rank(a) - rank(b)
	})
	return all
}

func providers() []Provider {
	return []Provider{
		{ID: "cloudflared", ClientIP: true, Name: "Cloudflare Tunnel (quick tunnel)", Binary: "cloudflared", Secure: true, Account: "no",
			Notes: "trycloudflare.com: no account, no uptime guarantee; Cloudflare terminates TLS", InstallHint: "https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/",
			args: func(p int) []string {
				return []string{"tunnel", "--no-autoupdate", "--url", "http://127.0.0.1:" + strconv.Itoa(p)}
			},
			url: regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)},
		{ID: "ngrok", ClientIP: true, Name: "ngrok", Binary: "ngrok", Secure: true, Account: "yes (authtoken configured)",
			Notes: "the free plan may show a warning page when opening the link", InstallHint: "https://ngrok.com/download",
			args: func(p int) []string {
				return []string{"http", "127.0.0.1:" + strconv.Itoa(p), "--log", "stdout", "--log-format", "logfmt"}
			},
			url: regexp.MustCompile(`url=(https://[^\s"]+)`)},
		{ID: "serveo", ClientIP: true, Name: "serveo.net", Builtin: true, Secure: true, Account: "no",
			Notes: "built in; HTTPS; speakers accept a warning page the first time",
			native: func(ctx context.Context, p int) (*Tunnel, error) {
				return startSSH(ctx, "serveo.net:22", "relevo", p, regexp.MustCompile(`https://[a-z0-9.-]+\.(?:serveo\.net|serveousercontent\.com)`))
			}},
		{ID: "localhost.run", ClientIP: false, Name: "localhost.run", Builtin: true, Secure: true, Account: "no",
			Notes: "built in; HTTPS; does not pass the speakers' IP",
			native: func(ctx context.Context, p int) (*Tunnel, error) {
				return startSSH(ctx, "localhost.run:22", "nokey", p, regexp.MustCompile(`tunneled with tls termination, (https://[a-z0-9.-]+)`))
			}},
		{ID: "tailscale", ClientIP: false, Name: "Tailscale Funnel", Binary: "tailscale", Secure: true, Account: "yes (tailnet with Funnel enabled)",
			Notes: "Funnel must be allowed in the tailnet policy", InstallHint: "https://tailscale.com/download",
			args: func(p int) []string { return []string{"funnel", strconv.Itoa(p)} },
			url:  regexp.MustCompile(`https://[a-z0-9.-]+\.ts\.net`)},
		{ID: "bore", ClientIP: false, Name: "bore.pub (UNENCRYPTED)", Builtin: true, Secure: false, Account: "no",
			Notes:  "built in; plain http://: password and commands travel unencrypted",
			native: func(ctx context.Context, p int) (*Tunnel, error) { return startBore(ctx, "bore.pub", p) }},
	}
}

// Info is a provider plus local availability, for the UI.
type Info struct {
	Provider
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

// Available lists built-in providers and whether their client is installed.
func Available() []Info {
	var out []Info
	for _, p := range Providers() {
		if p.Builtin {
			out = append(out, Info{Provider: p, Installed: true})
			continue
		}
		path, err := exec.LookPath(p.Binary)
		out = append(out, Info{Provider: p, Installed: err == nil, Path: path})
	}
	return out
}

// Find returns a built-in provider by id.
func Find(id string) (Provider, bool) {
	i := slices.IndexFunc(Providers(), func(p Provider) bool { return p.ID == id })
	if i < 0 {
		return Provider{}, false
	}
	return Providers()[i], true
}

// FromCustom builds a provider from a user definition.
func FromCustom(c Custom) (Provider, error) {
	if len(c.Command) == 0 {
		return Provider{}, errors.New("custom tunnel: empty command")
	}
	re, err := regexp.Compile(c.URLRegex)
	if err != nil {
		return Provider{}, fmt.Errorf("custom tunnel: %w", err)
	}
	cmd := slices.Clone(c.Command)
	return Provider{ID: "custom", Name: "Custom command", Binary: cmd[0], Secure: c.Secure,
		args: func(p int) []string {
			out := make([]string, 0, len(cmd)-1)
			for _, a := range cmd[1:] {
				out = append(out, strings.ReplaceAll(a, "{port}", strconv.Itoa(p)))
			}
			return out
		}, url: re}, nil
}

// Tunnel is a running tunnel client (external program or built-in).
type Tunnel struct {
	URL      string
	Provider Provider
	cmd      *exec.Cmd
	stop     func() // built-in tunnels
	done     chan struct{}
	err      error
	mu       sync.Mutex
	tail     []string // last output lines, for diagnostics (never contains our secrets)
}

func (t *Tunnel) log(line string) {
	t.mu.Lock()
	t.tail = append(t.tail, line)
	if len(t.tail) > 20 {
		t.tail = t.tail[1:]
	}
	t.mu.Unlock()
}

// ErrInsecure is returned for providers without TLS unless explicitly allowed.
var ErrInsecure = errors.New("this tunnel does not encrypt traffic (http://); enable it explicitly to accept that risk")

// Start launches the provider for localPort and waits for its public URL.
func Start(ctx context.Context, p Provider, localPort int, allowInsecure bool, timeout time.Duration) (*Tunnel, error) {
	if !p.Secure && !allowInsecure {
		return nil, ErrInsecure
	}
	if p.native != nil {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		t, err := p.native(ctx, localPort)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		t.Provider = p
		return t, nil
	}
	bin, err := exec.LookPath(p.Binary)
	if err != nil {
		return nil, fmt.Errorf("%s is not installed (%s)", p.Binary, p.InstallHint)
	}
	cmd := exec.Command(bin, p.args(localPort)...)
	cmd.Stdin = nil // /dev/null: never prompt
	setProcAttrs(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	t := &Tunnel{Provider: p, cmd: cmd, done: make(chan struct{})}
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			t.log(line)
			if sent {
				continue
			}
			if m := p.url.FindStringSubmatch(line); m != nil {
				u := m[0]
				if len(m) > 1 {
					u = m[1]
				}
				if p.build != nil {
					u = p.build(m)
				}
				found <- strings.TrimRight(u, "/")
				sent = true
			}
		}
	}()
	go func() {
		t.err = cmd.Wait()
		pw.Close()
		close(t.done)
	}()
	select {
	case u := <-found:
		t.URL = u
		return t, nil
	case <-t.done:
		return nil, fmt.Errorf("%s exited before giving a URL: %v\n%s", p.Name, t.err, t.Output())
	case <-time.After(timeout):
		t.Close()
		return nil, fmt.Errorf("%s gave no public URL within %s\n%s", p.Name, timeout, t.Output())
	case <-ctx.Done():
		t.Close()
		return nil, ctx.Err()
	}
}

// Done is closed when the tunnel client exits.
func (t *Tunnel) Done() <-chan struct{} { return t.done }

// Output returns the last lines printed by the client (diagnostics).
func (t *Tunnel) Output() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.tail, "\n")
}

// Close stops the client.
func (t *Tunnel) Close() {
	if t.stop != nil {
		t.stop()
		<-t.done
		return
	}
	if t.cmd.Process != nil {
		terminate(t.cmd)
		select {
		case <-t.done:
		case <-time.After(3 * time.Second):
			_ = t.cmd.Process.Kill()
			<-t.done
		}
	}
}
