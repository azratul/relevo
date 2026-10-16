package hostagent

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azratul/relevo/internal/adapter"
	"github.com/azratul/relevo/internal/tunnel"
)

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func newEmbeddedAgent(t *testing.T, cn Connection) *Agent {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Backend = "sim"
	cfg.Target = adapter.Target{Class: "relevo-sim"}
	cfg.Connection = cn
	a, err := New(cfg, "", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(context.Background()) })
	return a
}

func TestEmbeddedOwnURL(t *testing.T) {
	port := freePort(t)
	a := newEmbeddedAgent(t, Connection{Mode: ModeURL, PublicURL: "https://relevo.invalid", LocalPort: port})
	if _, err := a.CreateSession(context.Background(), CreateOptions{Connection: &Connection{Mode: ModeAsk}}); err != ErrNeedMode {
		t.Fatalf("ask mode must require a choice, got %v", err)
	}
	info, err := a.CreateSession(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.JoinURL, "https://relevo.invalid/s/") {
		t.Fatal(info.JoinURL)
	}
	f := &fixture{t: t, agent: a, sim: a.Keyboard().Backend().(*adapter.Sim), sess: info, base: a.emb.base}
	f.waitConnected()
	if st := a.Status(); st.Connection == nil || st.Connection.Mode != ModeURL || st.Connection.PublicURL != "https://relevo.invalid" {
		t.Fatalf("%+v", st.Connection)
	}
	// Nobody reaching the tunnel can create sessions on the host's relay.
	res, err := http.Post(a.emb.base+"/api/v1/sessions", "application/json", strings.NewReader("{}"))
	if err != nil || res.StatusCode != 401 {
		t.Fatalf("anonymous creation: %v %v", err, res.Status)
	}
	sp := f.speaker("Ana")
	if r := sp.command("presentation.next"); r["status"] != "executed" {
		t.Fatal(r)
	}
	if err := a.EndSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second); err == nil {
		t.Fatal("embedded relay still listening after the session ended")
	}
}

func TestEmbeddedTunnelRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	// A fake provider that prints a new URL and dies after one second.
	a := newEmbeddedAgent(t, Connection{Mode: ModeTunnel, Tunnel: "custom", Custom: &tunnel.Custom{
		Command:  []string{"sh", "-c", "echo https://t$$.example.net; sleep 1; exit 1"},
		URLRegex: `https://\S+`, Secure: true,
	}})
	info, err := a.CreateSession(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first := info.JoinURL
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := a.Session()
		if s != nil && s.JoinURL != first {
			if !strings.HasPrefix(s.JoinURL, "https://t") || !strings.HasSuffix(s.JoinURL, "/s/"+info.ID) {
				t.Fatal(s.JoinURL)
			}
			if a.emb.srv.PublicURL()+"/s/"+info.ID != s.JoinURL {
				t.Fatal("relay public URL not updated")
			}
			if c := a.Status().Connection.WarningCode; c != "link_changed" && c != "tunnel_down" {
				t.Fatalf("no warning code: %q", c)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tunnel was not restarted with a new URL")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
