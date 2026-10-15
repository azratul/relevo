package tunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestLiveProviders opens REAL public tunnels for a few seconds to a throwaway
// server. Opt-in: RELEVO_TEST_TUNNELS=ngrok,localhost.run,serveo,bore,cloudflared
func TestLiveProviders(t *testing.T) {
	list := os.Getenv("RELEVO_TEST_TUNNELS")
	if list == "" {
		t.Skip("set RELEVO_TEST_TUNNELS to test real providers")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("relevo-tunnel-ok xff=" + r.Header.Get("X-Forwarded-For")))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			c.Write(r.Context(), typ, b)
		}
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()
	for _, id := range strings.Split(list, ",") {
		t.Run(id, func(t *testing.T) {
			p, ok := Find(id)
			if !ok {
				t.Fatalf("unknown provider %s", id)
			}
			start := time.Now()
			tun, err := Start(context.Background(), p, port, true, 45*time.Second)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			defer tun.Close()
			t.Logf("%s: %s (URL in %s)", id, tun.URL, time.Since(start).Round(time.Millisecond))
			var body string
			for i := 0; i < 10; i++ { // DNS/edge may need a moment
				req, _ := http.NewRequest("GET", tun.URL+"/hello", nil)
				req.Header.Set("ngrok-skip-browser-warning", "1")
				res, err := http.DefaultClient.Do(req)
				if err == nil {
					b, _ := io.ReadAll(res.Body)
					res.Body.Close()
					body = string(b)
					if strings.Contains(body, "relevo-tunnel-ok") {
						break
					}
				}
				time.Sleep(time.Second)
			}
			if !strings.Contains(body, "relevo-tunnel-ok") {
				t.Fatalf("HTTP through tunnel failed: %q", body)
			}
			t.Logf("%s: HTTP ok, %s", id, strings.TrimPrefix(body, "relevo-tunnel-ok "))
			wsURL := "ws" + strings.TrimPrefix(tun.URL, "http") + "/ws"
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"ngrok-skip-browser-warning": {"1"}}})
			if err != nil {
				t.Fatalf("WebSocket through tunnel: %v", err)
			}
			defer c.CloseNow()
			var rtts []time.Duration
			for i := 0; i < 10; i++ {
				t0 := time.Now()
				c.Write(ctx, websocket.MessageText, []byte("ping"))
				if _, b, err := c.Read(ctx); err != nil || string(b) != "ping" {
					t.Fatalf("echo: %v %q", err, b)
				}
				rtts = append(rtts, time.Since(t0))
			}
			t.Logf("%s: WebSocket ok, echo RTT first=%s last=%s", id, rtts[0].Round(time.Millisecond), rtts[9].Round(time.Millisecond))
		})
	}
}
