package hostagent

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"relevo/internal/adapter"
)

func TestAdminPanelGuards(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Backend = "sim"
	cfg.Target = adapter.Target{Class: "relevo-sim"}
	a, err := New(cfg, "", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ad, err := StartAdmin(a, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ad.Close()
	if _, err := StartAdmin(a, "0.0.0.0:0"); err == nil {
		t.Fatal("non-loopback admin accepted")
	}
	base := "http://" + ad.addr
	tok := strings.SplitN(ad.URL(), "#t=", 2)[1]
	do := func(method, path, token string, hdr map[string]string) int {
		req, _ := http.NewRequest(method, base+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range hdr {
			if k == "Host" {
				req.Host = v
			} else {
				req.Header.Set(k, v)
			}
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := do("GET", "/", "", nil); c != 200 {
		t.Fatalf("index %d", c)
	}
	if c := do("GET", "/api/status", "", nil); c != 401 {
		t.Fatalf("no token %d", c)
	}
	if c := do("GET", "/api/status", tok, nil); c != 200 {
		t.Fatalf("token %d", c)
	}
	if c := do("GET", "/api/status", tok, map[string]string{"Host": "evil.example"}); c != 403 {
		t.Fatalf("rebinding host %d", c)
	}
	if c := do("POST", "/api/session/reveal", tok, map[string]string{"Origin": "https://evil.example"}); c != 403 {
		t.Fatalf("foreign origin %d", c)
	}
	if c := do("GET", "/api/session/reveal", tok, nil); c != 405 {
		t.Fatalf("password reveal via GET %d", c)
	}
}
