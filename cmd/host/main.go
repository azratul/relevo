// Command relevo-host is the native receiver: tray app + local panel + CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"relevo/internal/adapter"
	"relevo/internal/hostagent"
	"relevo/internal/tunnel"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "relevo-host:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: relevo-host [flags] [command]

Commands:
  (none)       run the receiver (tray icon if available, local panel otherwise)
  windows      list candidate target windows for the selected backend
  check        print backend readiness (permissions, target)
  backends     list compiled backends
  try ACTION [PARAMS_JSON]
               execute one action locally (same path as a remote command,
               including the focus check) for manual platform validation
  init-config  write a default config file
  version      print version

Flags:
`)
	flag.PrintDefaults()
}

func run() error {
	var (
		cfgPath   = flag.String("config", hostagent.DefaultPath(), "config file")
		tunnelArg = flag.String("tunnel", "", "how speakers connect: cloudflared | ngrok | localhost.run | serveo | tailscale | bore | custom | url:https://… (your own tunnel to --local-port)")
		localPort = flag.Int("local-port", 0, "local port of the embedded relay (needed with url:…)")
		insecure  = flag.Bool("allow-insecure-tunnel", false, "accept tunnels without TLS (bore.pub): password and commands travel in clear text")
		backend   = flag.String("backend", "", "key backend (overrides config): auto, sim, x11, hyprland, wtype, portal, windows, darwin")
		noTray    = flag.Bool("no-tray", false, "do not use the system tray")
		noBrowser = flag.Bool("no-browser", false, "do not open the local panel in a browser")
		start     = flag.Bool("start", false, "create a session at startup and print link and password")
		logLevel  = flag.String("log-level", "info", "debug | info | warn | error")
		langFlag  = flag.String("lang", "", "interface language: en (default) or es")
	)
	flag.Usage = usage
	flag.Parse()
	var level slog.Level
	_ = level.UnmarshalText([]byte(*logLevel))
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := hostagent.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if *langFlag == "en" || *langFlag == "es" {
		cfg.Language = *langFlag
	}
	if cfg.Language == "es" {
		lang = "es"
	}
	if *tunnelArg != "" {
		cfg.Connection.Mode = hostagent.ModeTunnel
		cfg.Connection.Tunnel = *tunnelArg
		if u, ok := strings.CutPrefix(*tunnelArg, "url:"); ok {
			cfg.Connection = hostagent.Connection{Mode: hostagent.ModeURL, PublicURL: u, LocalPort: *localPort, WebRTC: cfg.Connection.WebRTC, STUNServers: cfg.Connection.STUNServers}
		}
	}
	if *localPort != 0 {
		cfg.Connection.LocalPort = *localPort
	}
	if *insecure {
		cfg.Connection.AllowInsecureTunnel = true
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if *backend != "" {
		cfg.Backend = *backend
	}
	switch flag.Arg(0) {
	case "version":
		fmt.Println(version)
		return nil
	case "backends":
		for _, b := range adapter.Backends() {
			fmt.Printf("%-10s %-12s %s\n", b.Name, b.Status, b.Description)
		}
		fmt.Println("auto ->", adapter.AutoBackend())
		fmt.Println("\ntunnels:")
		for _, t := range tunnel.Available() {
			fmt.Printf("%-14s installed=%-5v https=%-5v client-ip=%-5v %s\n", t.ID, t.Installed, t.Secure, t.ClientIP, t.Notes)
		}
		return nil
	case "init-config":
		if _, err := os.Stat(*cfgPath); err == nil {
			return fmt.Errorf("%s already exists", *cfgPath)
		}
		if err := cfg.Save(*cfgPath); err != nil {
			return err
		}
		fmt.Println("wrote", *cfgPath)
		return nil
	case "try":
		b, err := adapter.NewBackend(cfg.Backend)
		if err != nil {
			return err
		}
		res, err := adapter.Resolve(cfg.Profile, cfg.Actions, cfg.Bindings, cfg.CustomActions)
		if err != nil {
			return err
		}
		kb := adapter.NewKeyboard(b, res, cfg.Target)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rd := kb.Readiness(ctx); !rd.Ready {
			return fmt.Errorf("not ready: %s", rd.Reason)
		}
		params := json.RawMessage(flag.Arg(2))
		if len(params) == 0 {
			params = json.RawMessage("{}")
		}
		start := time.Now()
		conf, err := kb.Execute(ctx, flag.Arg(1), params)
		if err != nil {
			fmt.Printf("result: NOT executed or uncertain: %v (%s)\n", err, time.Since(start).Round(time.Microsecond))
			os.Exit(3)
		}
		fmt.Printf("result: executed, confirmation=%s (%s)\n", conf, time.Since(start).Round(time.Microsecond))
		return nil
	case "windows", "check":
		b, err := adapter.NewBackend(cfg.Backend)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if flag.Arg(0) == "windows" {
			ws, err := b.Windows(ctx)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			for _, w := range ws {
				_ = enc.Encode(w)
			}
			return nil
		}
		res, err := adapter.Resolve(cfg.Profile, cfg.Actions, cfg.Bindings, cfg.CustomActions)
		if err != nil {
			return err
		}
		rd := adapter.NewKeyboard(b, res, cfg.Target).Readiness(ctx)
		fmt.Printf("backend:     %s (%s)\nfocus check: %s\ntarget:      %s\nready:       %v %s\n", b.Name(), b.Platform(), rd.Target.FocusCheck, rd.Target.Label, rd.Ready, rd.Reason)
		for _, p := range b.Permissions() {
			fmt.Println("permission: ", p)
		}
		if !rd.Ready {
			os.Exit(2)
		}
		return nil
	case "":
	default:
		usage()
		return fmt.Errorf("unknown command %q", flag.Arg(0))
	}

	agent, err := hostagent.New(cfg, *cfgPath, version, log)
	if err != nil {
		return err
	}
	admin, err := hostagent.StartAdmin(agent, cfg.AdminListen)
	if err != nil {
		return err
	}
	defer admin.Close()
	fmt.Println(T("panel"), admin.URL())
	if *start {
		var choice *hostagent.Connection
		if cfg.Connection.Mode == hostagent.ModeAsk {
			c, err := askConnection()
			if err != nil {
				return err
			}
			choice = c
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		info, err := agent.CreateSession(ctx, hostagent.CreateOptions{Connection: choice})
		cancel()
		if err != nil {
			return err
		}
		// Printed to the local terminal only, never logged.
		fmt.Printf("\n%s %s\n%s %s\n%s\n\n", T("link"), info.JoinURL, T("password"), info.Password, T("share"))
	}
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		agent.Close(ctx)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	if !*noTray {
		err := runTray(agent, admin, sig, shutdown, !*noBrowser)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errNoTray) {
			return err
		}
		log.Warn("system tray not available; use the local panel or Ctrl+C to quit", "reason", err)
	}
	if !*noBrowser {
		openBrowser(admin.URL())
	}
	fmt.Println(T("ctrlc"))
	select {
	case <-sig:
	case <-admin.Quit:
	}
	shutdown()
	return nil
}

// askConnection asks in the terminal how speakers will connect.
func askConnection() (*hostagent.Connection, error) {
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil, errors.New("choose how speakers connect: --tunnel <provider> | --tunnel url:https://…")
	}
	type opt struct {
		label string
		conn  hostagent.Connection
	}
	var opts []opt
	for _, t := range tunnel.Available() {
		if !t.Installed {
			continue
		}
		label := fmt.Sprintf("%s — %s", t.Name, t.Notes)
		if !t.Secure {
			label += T("unencrypted")
		}
		opts = append(opts, opt{label, hostagent.Connection{Mode: hostagent.ModeTunnel, Tunnel: t.ID, AllowInsecureTunnel: !t.Secure}})
	}
	fmt.Println(T("how"))
	for i, o := range opts {
		fmt.Printf("  %d) %s\n", i+1, o.label)
	}
	fmt.Printf("  %d) %s\n", len(opts)+1, T("own"))
	fmt.Print(T("option"))
	var n int
	if _, err := fmt.Scanln(&n); err != nil || n < 1 || n > len(opts)+1 {
		return nil, errors.New("invalid choice")
	}
	if n <= len(opts) {
		c := opts[n-1].conn
		if c.AllowInsecureTunnel {
			fmt.Print(T("confirmPlain"))
			var yes string
			fmt.Scanln(&yes)
			if yes != "yes" && yes != "sí" && yes != "si" {
				return nil, errors.New("cancelled")
			}
		}
		return &c, nil
	}
	var u string
	var port int
	fmt.Print(T("publicURL"))
	fmt.Scanln(&u)
	fmt.Print(T("localPort"))
	fmt.Scanln(&port)
	return &hostagent.Connection{Mode: hostagent.ModeURL, PublicURL: u, LocalPort: port}, nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err == nil {
		go cmd.Wait()
	}
}
