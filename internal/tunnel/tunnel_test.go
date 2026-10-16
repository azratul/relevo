package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestURLExtraction(t *testing.T) {
	cases := map[string]string{
		"cloudflared": "2026-10-16T10:00:00Z INF |  https://quiet-river-1234.trycloudflare.com                                |",
		"ngrok":       `t=2026-10-16T10:00:00 lvl=info msg="started tunnel" obj=tunnels name=command_line addr=http://127.0.0.1:8080 url=https://ab12-1-2-3-4.ngrok-free.app`,
		"tailscale":   "Available on the internet:\nhttps://magi.tail1234.ts.net/",
	}
	want := map[string]string{
		"cloudflared": "https://quiet-river-1234.trycloudflare.com", "ngrok": "https://ab12-1-2-3-4.ngrok-free.app",
		"tailscale": "https://magi.tail1234.ts.net",
	}
	for id, line := range cases {
		p, _ := Find(id)
		var got string
		for _, l := range strings.Split(line, "\n") {
			if m := p.url.FindStringSubmatch(l); m != nil {
				got = m[0]
				if len(m) > 1 {
					got = m[1]
				}
				if p.build != nil {
					got = p.build(m)
				}
				break
			}
		}
		if strings.TrimRight(got, "/") != want[id] {
			t.Errorf("%s: got %q want %q", id, got, want[id])
		}
	}
}

func TestCustomLifecycleAndInsecure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	p, err := FromCustom(Custom{Command: []string{"sh", "-c", "echo starting; echo 'public: https://demo-{port}.example.net'; sleep 30"}, URLRegex: `https://\S+`, Secure: true})
	if err != nil {
		t.Fatal(err)
	}
	tun, err := Start(context.Background(), p, 4321, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if tun.URL != "https://demo-4321.example.net" {
		t.Fatal(tun.URL)
	}
	start := time.Now()
	tun.Close()
	select {
	case <-tun.Done():
	default:
		t.Fatal("not stopped")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("slow stop")
	}
	bore, _ := Find("bore")
	if _, err := Start(context.Background(), bore, 1, false, time.Second); !errors.Is(err, ErrInsecure) {
		t.Fatalf("insecure tunnel started without opt-in: %v", err)
	}
	dead, _ := FromCustom(Custom{Command: []string{"sh", "-c", "echo boom; exit 3"}, URLRegex: `https://\S+`, Secure: true})
	if _, err := Start(context.Background(), dead, 1, false, 5*time.Second); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want exit error with output, got %v", err)
	}
}

func TestTOFUHostKeys(t *testing.T) {
	path := t.TempDir() + "/known_hosts"
	cb := tofuHostKeys(path)
	k1, k2 := testKey(t), testKey(t)
	addr := &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 22}
	if err := cb("example.org:22", addr, k1); err != nil {
		t.Fatal("first use must pin", err)
	}
	if err := cb("example.org:22", addr, k1); err != nil {
		t.Fatal("same key must pass", err)
	}
	if err := cb("example.org:22", addr, k2); err == nil {
		t.Fatal("changed key of the same type must be refused")
	}
}

func testKey(t *testing.T) ssh.PublicKey {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s.PublicKey()
}

// A client whose processes ignore SIGTERM (as seen on the macOS CI runner)
// keeps the output pipe open through its children; Close must still return fast.
func TestCloseWhenSIGTERMIsIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	p, _ := FromCustom(Custom{Command: []string{"sh", "-c", "trap '' TERM; echo https://stubborn.example.net; sleep 30; true"}, URLRegex: `https://\S+`, Secure: true})
	tun, err := Start(context.Background(), p, 1, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	tun.Close()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Close took %s", d)
	}
}
