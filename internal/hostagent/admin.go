package hostagent

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"relevo/internal/adapter"
	"relevo/internal/secret"
)

//go:embed adminui
var adminFS embed.FS

// Admin is the local control panel, bound to loopback and protected by a
// random token passed in the URL fragment (never sent to any server).
type Admin struct {
	agent *Agent
	token string
	addr  string
	srv   *http.Server
	Quit  chan struct{}
}

// StartAdmin listens on addr (loopback only).
func StartAdmin(a *Agent, addr string) (*Admin, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, &net.AddrError{Err: "admin panel must listen on a loopback address", Addr: addr}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ad := &Admin{agent: a, token: strings.TrimPrefix(secret.Token("adm"), "adm_"), addr: ln.Addr().String(), Quit: make(chan struct{})}
	ad.srv = &http.Server{Handler: ad.handler(), ReadHeaderTimeout: 5 * time.Second}
	go ad.srv.Serve(ln)
	return ad, nil
}

// URL returns the panel URL including the secret fragment.
func (ad *Admin) URL() string { return "http://" + ad.addr + "/#t=" + ad.token }

func (ad *Admin) Close() { _ = ad.srv.Close() }

func (ad *Admin) handler() http.Handler {
	static, _ := fs.Sub(adminFS, "adminui")
	files := http.FileServerFS(static)
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		files.ServeHTTP(w, r)
	}))
	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", ad.status)
	api.HandleFunc("POST /api/session", ad.createSession)
	api.HandleFunc("DELETE /api/session", ad.endSession)
	api.HandleFunc("POST /api/session/reveal", ad.reveal)
	api.HandleFunc("POST /api/session/rotate", ad.rotate)
	api.HandleFunc("POST /api/session/pause", ad.pause)
	api.HandleFunc("POST /api/participants/{id}", ad.patchParticipant)
	api.HandleFunc("GET /api/windows", ad.windows)
	api.HandleFunc("PUT /api/settings", ad.settings)
	api.HandleFunc("POST /api/quit", ad.quit)
	mux.Handle("/api/", ad.guard(api))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// DNS-rebinding defence: only our own host:port.
		_, port, _ := net.SplitHostPort(ad.addr)
		if r.Host != ad.addr && r.Host != "localhost:"+port {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (ad *Admin) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(ad.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10)).Decode(v)
}

func ctx15(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 15*time.Second)
}

func (ad *Admin) status(w http.ResponseWriter, r *http.Request) { jsonOut(w, ad.agent.Status()) }

func (ad *Admin) createSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password        string      `json:"password"`
		RequireApproval bool        `json:"requireApproval"`
		Connection      *Connection `json:"connection"`
		Remember        bool        `json:"remember"`
	}
	_ = readJSON(r, &req)
	cfg := ad.agent.Config()
	changed := cfg.RequireApproval != req.RequireApproval
	cfg.RequireApproval = req.RequireApproval
	if req.Connection != nil && req.Remember {
		cfg.Connection = *req.Connection
		changed = true
	}
	if changed {
		if err := ad.agent.UpdateConfig(cfg); err != nil {
			jsonErr(w, err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second) // tunnels can take a while
	defer cancel()
	if _, err := ad.agent.CreateSession(ctx, CreateOptions{Password: req.Password, Connection: req.Connection}); err != nil {
		jsonErr(w, err)
		return
	}
	jsonOut(w, map[string]any{"ok": true})
}

func (ad *Admin) endSession(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctx15(r)
	defer cancel()
	if err := ad.agent.EndSession(ctx); err != nil {
		jsonErr(w, err)
		return
	}
	jsonOut(w, map[string]any{"ok": true})
}

// reveal is a POST so the password is only returned on an explicit action.
func (ad *Admin) reveal(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]string{"password": ad.agent.Password()})
}

func (ad *Admin) rotate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	_ = readJSON(r, &req)
	ctx, cancel := ctx15(r)
	defer cancel()
	if _, err := ad.agent.RotatePassword(ctx, req.Password); err != nil {
		jsonErr(w, err)
		return
	}
	jsonOut(w, map[string]any{"ok": true})
}

func (ad *Admin) pause(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paused bool `json:"paused"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, err)
		return
	}
	ctx, cancel := ctx15(r)
	defer cancel()
	if err := ad.agent.SetPaused(ctx, req.Paused); err != nil {
		jsonErr(w, err)
		return
	}
	jsonOut(w, map[string]any{"ok": true})
}

func (ad *Admin) patchParticipant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State        string   `json:"state"`
		Capabilities []string `json:"capabilities"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, err)
		return
	}
	ctx, cancel := ctx15(r)
	defer cancel()
	if err := ad.agent.PatchParticipant(ctx, r.PathValue("id"), req.State, req.Capabilities); err != nil {
		jsonErr(w, err)
		return
	}
	jsonOut(w, map[string]any{"ok": true})
}

func (ad *Admin) windows(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	b := ad.agent.Keyboard().Backend()
	ws, err := b.Windows(ctx)
	if err != nil {
		jsonOut(w, map[string]any{"windows": []adapter.Window{}, "error": err.Error()})
		return
	}
	jsonOut(w, map[string]any{"windows": ws})
}

func (ad *Admin) settings(w http.ResponseWriter, r *http.Request) {
	cfg := ad.agent.Config()
	var in Config
	if err := readJSON(r, &in); err != nil {
		jsonErr(w, err)
		return
	}
	// Only fields editable from the panel; the creation token stays in the file.
	cfg.Backend, cfg.Target, cfg.Profile, cfg.Actions = in.Backend, in.Target, in.Profile, in.Actions
	cfg.RequireApproval = in.RequireApproval
	if in.Language == "en" || in.Language == "es" {
		cfg.Language = in.Language
	}
	if in.Bindings != nil {
		cfg.Bindings = in.Bindings
	}
	if err := ad.agent.UpdateConfig(cfg); err != nil {
		jsonErr(w, err)
		return
	}
	jsonOut(w, map[string]any{"ok": true})
}

func (ad *Admin) quit(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"ok": true})
	select {
	case <-ad.Quit:
	default:
		close(ad.Quit)
	}
}
